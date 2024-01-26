// Copyright 2023 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package crawler implements the crawling logic of the application.
// It's responsible for crawling a website and extracting information from it.
package crawler

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"image"
	"image/png"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"

	"github.com/PuerkitoBio/goquery"
	"github.com/tebeka/selenium"
	"github.com/tebeka/selenium/chrome"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
)

const (
	numberOfWorkers = 5 // Adjust this based on your needs
	dbConnCheckErr  = "Error checking database connection: %v\n"
)

var (
	config cfg.Config
)

var indexPageMutex sync.Mutex // Mutex to ensure that only one goroutine is indexing a page at a time

// CrawlWebsite is responsible for crawling a website, it's the main entry point
// and it's called from the main.go when there is a Source to crawl.
func CrawlWebsite(db cdb.DatabaseHandler, source cdb.Source, wd selenium.WebDriver) {
	// Crawl the initial URL and get the HTML content
	// This is where you'd use Selenium or another method to get the page content
	pageSource, err := getHTMLContent(source.URL, wd)
	if err != nil {
		log.Println("Error getting HTML content:", err)
		// Update the source state in the database
		updateSourceState(db, source.URL, err)
		return
	}

	// Extract necessary information from the content
	pageInfo := extractPageInfo(pageSource)

	// Index the page content in the database
	pageInfo.sourceID = source.ID
	indexPage(db, source.URL, pageInfo)

	// Get screenshot of the page
	if config.Crawler.SourceScreenshot {
		log.Printf("Taking screenshot of %s...\n", source.URL)
		// Get the current date and time
		currentTime := time.Now()
		// For example, using the format yyyyMMddHHmmss
		timeStr := currentTime.Format("20060102150405")
		// Create imageName
		imageName := fmt.Sprintf("%d_%s.png", source.ID, timeStr)
		err = TakeScreenshot(wd, imageName)
		if err != nil {
			log.Println("Error taking screenshot:", err)
		}
		// Update DB SearchIndex Table with the screenshot filename
		_, err = db.Exec(`UPDATE SearchIndex SET snapshot_url = $1 WHERE page_url = $2`, imageName, source.URL)
		if err != nil {
			log.Printf("Error updating database with screenshot URL: %v\n", err)
		}
	}

	// Parse the HTML and extract links
	htmlContent, err := pageSource.PageSource()
	if err != nil {
		log.Println("Error getting page source:", err)
		// Update the source state in the database
		updateSourceState(db, source.URL, err)
		return
	}
	links := extractLinks(htmlContent)

	// Create a channel to enqueue jobs
	jobs := make(chan string, len(links))

	// Use a WaitGroup to wait for all goroutines to finish
	var wg sync.WaitGroup

	// Launch worker goroutines
	for w := 1; w <= numberOfWorkers; w++ {
		wg.Add(1)
		go worker(db, wd, w, jobs, &wg, &source)
	}

	// Enqueue jobs (links)
	for _, link := range links {
		jobs <- link
	}
	close(jobs)

	// Wait for all the workers to finish
	wg.Wait()

	// Update the source state in the database
	updateSourceState(db, source.URL, nil)
}

// updateSourceState is responsible for updating the state of a Source in
// the database after crawling it (it does consider errors too)
func updateSourceState(db cdb.DatabaseHandler, sourceURL string, crawlError error) {
	var err error

	// Before updating the source state, check if the database connection is still alive
	err = db.CheckConnection(config)
	if err != nil {
		log.Printf(dbConnCheckErr, err)
		return
	}

	if crawlError != nil {
		// Update the source with error details
		_, err = db.Exec(`UPDATE Sources SET last_crawled_at = NOW(), status = 'error',
                          last_error = $1, last_error_at = NOW()
                          WHERE url = $2`, crawlError.Error(), sourceURL)
	} else {
		// Update the source as successfully crawled
		_, err = db.Exec(`UPDATE Sources SET last_crawled_at = NOW(), status = 'completed'
                          WHERE url = $1`, sourceURL)
	}

	if err != nil {
		log.Printf("Error updating source state for URL %s: %v", sourceURL, err)
	}
}

// indexPage is responsible for indexing a crawled page in the database
// I had to write this function quickly, so it's not very efficient.
// In an ideal world, I would have used multiple transactions to index the page
// and avoid deadlocks when inserting keywords. However, using a mutex to enter
// this function (and so treat it as a critical section) should be enough for now.
// Another thought is, the mutex also helps slow down the crawling process, which
// is a good thing. You don't want to overwhelm the Source site with requests.
func indexPage(db cdb.DatabaseHandler, url string, pageInfo PageInfo) {
	// Acquire a lock to ensure that only one goroutine is accessing the database
	indexPageMutex.Lock()
	defer indexPageMutex.Unlock()

	// Before updating the source state, check if the database connection is still alive
	err := db.CheckConnection(config)
	if err != nil {
		log.Printf(dbConnCheckErr, err)
		return
	}

	// Start a transaction
	tx, err := db.Begin()
	if err != nil {
		log.Printf("Error starting transaction: %v\n", err)
		return
	}

	// Insert or update the page in SearchIndex
	indexID, err := insertOrUpdateSearchIndex(tx, url, pageInfo)
	if err != nil {
		log.Printf("Error inserting or updating SearchIndex: %v\n", err)
		rollbackTransaction(tx)
		return
	}

	// Insert MetaTags
	err = insertMetaTags(tx, indexID, pageInfo.MetaTags)
	if err != nil {
		log.Printf("Error inserting meta tags: %v\n", err)
		rollbackTransaction(tx)
		return
	}

	// Insert into KeywordIndex
	err = insertKeywords(tx, db, indexID, pageInfo)
	if err != nil {
		log.Printf("Error inserting keywords: %v\n", err)
		rollbackTransaction(tx)
		return
	}

	// Commit the transaction
	err = commitTransaction(tx)
	if err != nil {
		log.Printf("Error committing transaction: %v\n", err)
		rollbackTransaction(tx)
		return
	}
}

// insertOrUpdateSearchIndex inserts or updates a search index entry in the database.
// It takes a transaction object (tx), the URL of the page (url), and the page information (pageInfo).
// It returns the index ID of the inserted or updated entry and an error, if any.
func insertOrUpdateSearchIndex(tx *sql.Tx, url string, pageInfo PageInfo) (int, error) {
	var indexID int
	err := tx.QueryRow(`
        INSERT INTO SearchIndex (source_id, page_url, title, summary, content, indexed_at)
        VALUES ($1, $2, $3, $4, $5, NOW())
        ON CONFLICT (page_url) DO UPDATE
        SET title = EXCLUDED.title, summary = EXCLUDED.summary, content = EXCLUDED.content, indexed_at = NOW()
        RETURNING index_id`, pageInfo.sourceID, url, pageInfo.Title, pageInfo.Summary, pageInfo.BodyText).
		Scan(&indexID)
	return indexID, err
}

// insertMetaTags inserts meta tags into the database for a given index ID.
// It takes a transaction, index ID, and a map of meta tags as parameters.
// Each meta tag is inserted into the MetaTags table with the corresponding index ID, name, and content.
// Returns an error if there was a problem executing the SQL statement.
func insertMetaTags(tx *sql.Tx, indexID int, metaTags map[string]string) error {
	for name, content := range metaTags {
		_, err := tx.Exec(`INSERT INTO MetaTags (index_id, name, content)
                          VALUES ($1, $2, $3)`, indexID, name, content)
		if err != nil {
			return err
		}
	}
	return nil
}

// insertKeywords inserts keywords extracted from a web page into the database.
// It takes a transaction `tx` and a database connection `db` as parameters.
// The `indexID` parameter represents the ID of the index associated with the keywords.
// The `pageInfo` parameter contains information about the web page.
// It returns an error if there is any issue with inserting the keywords into the database.
func insertKeywords(tx *sql.Tx, db cdb.DatabaseHandler, indexID int, pageInfo PageInfo) error {
	for _, keyword := range extractKeywords(pageInfo) {
		keywordID, err := insertKeywordWithRetries(db, keyword)
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO KeywordIndex (keyword_id, index_id)
                          VALUES ($1, $2)`, keywordID, indexID)
		if err != nil {
			return err
		}
	}
	return nil
}

// rollbackTransaction rolls back a transaction.
// It takes a pointer to a sql.Tx as input and rolls back the transaction.
// If an error occurs during the rollback, it logs the error.
func rollbackTransaction(tx *sql.Tx) {
	err := tx.Rollback()
	if err != nil {
		log.Printf("Error rolling back transaction: %v\n", err)
	}
}

// commitTransaction commits the given SQL transaction.
// It returns an error if the commit fails.
func commitTransaction(tx *sql.Tx) error {
	err := tx.Commit()
	if err != nil {
		log.Printf("Error committing transaction: %v\n", err)
		return err
	}
	return nil
}

// insertKeywordWithRetries is responsible for storing the extracted keywords in the database
// It's written to be efficient and avoid deadlocks, but this right now is not required
// because indexPage uses a mutex to ensure that only one goroutine is indexing a page
// at a time. However, when implementing multiple transactions in indexPage, this function
// will be way more useful than it is now.
func insertKeywordWithRetries(db cdb.DatabaseHandler, keyword string) (int, error) {
	const maxRetries = 3
	var keywordID int

	// Before updating the source state, check if the database connection is still alive
	err := db.CheckConnection(config)
	if err != nil {
		log.Printf(dbConnCheckErr, err)
		return 0, err
	}

	for i := 0; i < maxRetries; i++ {
		err := db.QueryRow(`INSERT INTO Keywords (keyword)
                            VALUES ($1) ON CONFLICT (keyword) DO UPDATE
                            SET keyword = EXCLUDED.keyword RETURNING keyword_id`, keyword).
			Scan(&keywordID)
		if err != nil {
			if strings.Contains(err.Error(), "deadlock detected") {
				if i == maxRetries-1 {
					return 0, err
				}
				time.Sleep(time.Duration(i) * 100 * time.Millisecond) // Exponential backoff
				continue
			}
			return 0, err
		}
		return keywordID, nil
	}
	return 0, fmt.Errorf("failed to insert keyword after retries: %s", keyword)
}

// getHTMLContent is responsible for retrieving the HTML content of a page
// from Selenium and returning it as a WebDriver object
func getHTMLContent(url string, wd selenium.WebDriver) (selenium.WebDriver, error) {
	// Navigate to a page and interact with elements.
	if err := wd.Get(url); err != nil {
		return nil, err
	}
	time.Sleep(time.Second * time.Duration(config.Crawler.Interval)) // Pause to let page load
	return wd, nil
}

// extractPageInfo is responsible for extracting information from a collected page.
// In the future we may want to expand this function to extract more information
// from the page, such as images, videos, etc. and do a better job at screen scraping.
func extractPageInfo(webPage selenium.WebDriver) PageInfo {
	htmlContent, _ := webPage.PageSource()
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		log.Printf("Error loading HTML content: %v", err)
		return PageInfo{} // Return an empty struct in case of an error
	}

	title, _ := webPage.Title()
	summary := doc.Find("meta[name=description]").AttrOr("content", "")
	bodyText := doc.Find("body").Text()

	//containsAppInfo := strings.Contains(bodyText, "app") || strings.Contains(bodyText, "mobile")

	metaTags := extractMetaTags(doc)

	return PageInfo{
		Title:    title,
		Summary:  summary,
		BodyText: bodyText,
		MetaTags: metaTags,
	}
}

// extractMetaTags is a function that extracts meta tags from a goquery.Document.
// It iterates over each "meta" element in the document and retrieves the "name" and "content" attributes.
// The extracted meta tags are stored in a map[string]string, where the "name" attribute is the key and the "content" attribute is the value.
// The function returns the map of extracted meta tags.
func extractMetaTags(doc *goquery.Document) map[string]string {
	metaTags := make(map[string]string)
	doc.Find("meta").Each(func(_ int, s *goquery.Selection) {
		if name, exists := s.Attr("name"); exists {
			content, _ := s.Attr("content")
			metaTags[name] = content
		}
	})
	return metaTags
}

// IsValidURL checks if the string is a valid URL.
func IsValidURL(u string) bool {
	// Prepend a scheme if it's missing
	if !strings.Contains(u, "://") {
		u = "http://" + u
	}

	// Parse the URL and check for errors
	_, err := url.ParseRequestURI(u)
	return err == nil
}

// extractLinks extracts all the links from the given HTML content.
// It uses the goquery library to parse the HTML and find all the <a> tags.
// Each link is then added to a slice and returned.
func extractLinks(htmlContent string) []string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		log.Fatal("Error loading HTTP response body. ", err)
	}

	var links []string
	doc.Find("a").Each(func(index int, item *goquery.Selection) {
		linkTag := item
		link, _ := linkTag.Attr("href")
		link = strings.TrimSpace(link)
		if link != "" && IsValidURL(link) {
			links = append(links, link)
		}
	})
	return links
}

// isExternalLink checks if the link is external (aka outside the Source domain)
func isExternalLink(sourceURL, linkURL string) bool {
	// remove trailing spaces from linkURL
	linkURL = strings.TrimSpace(linkURL)

	// Handle relative URLs (e.g., "/sub/page")
	if strings.HasPrefix(linkURL, "/") {
		return false // This is a relative URL, not external
	}

	// Parse the source URL
	sourceParsed, err := url.Parse(sourceURL)
	if err != nil {
		return false // Handle parsing error
	}

	// Parse the link URL
	linkParsed, err := url.Parse(linkURL)
	if err != nil {
		return false // Handle parsing error
	}

	if config.DebugLevel > 0 {
		fmt.Println("Source URL:", sourceURL)
		fmt.Println("Link URL:", linkURL)
		fmt.Println("Source hostname:", sourceParsed.Hostname())
		fmt.Println("Link hostname:", linkParsed.Hostname())
	}

	// Takes the substring that correspond to the 1st and 2nd level domain (e.g., google.com)
	// regardless the number of subdomains
	var srcDomainName string
	srcFqdnArr := strings.Split(sourceParsed.Hostname(), ".")
	if len(srcFqdnArr) < 3 {
		srcDomainName = strings.Join(srcFqdnArr, ".")
	} else {
		srcDomainName = strings.Join(srcFqdnArr[len(srcFqdnArr)-2:], ".")
	}
	linkFqdnArr := strings.Split(linkParsed.Hostname(), ".")
	var linkDomainName string
	if len(linkFqdnArr) < 3 {
		linkDomainName = strings.Join(linkFqdnArr, ".")
	} else {
		linkDomainName = strings.Join(linkFqdnArr[len(linkFqdnArr)-2:], ".")
	}

	// Compare hostnames
	return srcDomainName != linkDomainName
}

// worker is the worker function that is responsible for crawling a page
func worker(db cdb.DatabaseHandler, wd selenium.WebDriver,
	id int, jobs chan string, wg *sync.WaitGroup, source *cdb.Source) {
	defer wg.Done()
	for url := range jobs {
		if source.Restricted {
			// If the Source is restricted
			// check if the url is outside the Source domain
			// if so skip it
			if isExternalLink(source.URL, url) {
				log.Printf("Worker %d: Skipping restricted URL: %s\n", id, url)
				continue
			}
		}
		log.Printf("Worker %d: started job %s\n", id, url)

		// remove trailing space in url
		url = strings.TrimSpace(url)
		if strings.HasPrefix(url, "/") {
			url, _ = combineURLs(source.URL, url)
		}
		if url == "" {
			continue
		}

		// Get HTML content of the page
		htmlContent, err := getHTMLContent(url, wd)
		if err != nil {
			log.Printf("Worker %d: Error getting HTML content for %s: %v\n", id, url, err)
			continue
		}

		// Extract necessary information from the content
		// For simplicity, we're extracting just the title and full content
		// You might want to extract more specific information
		pageCache := extractPageInfo(htmlContent)

		// Index the page content in the database
		pageCache.sourceID = source.ID
		indexPage(db, url, pageCache)

		// Extract links from the page
		links := extractLinks(pageCache.BodyText)

		// Enqueue jobs (links)
		for _, link := range links {
			jobs <- link
		}

		log.Printf("Worker %d: finished job %s\n", id, url)
	}
}

// combineURLs is a utility function to combine a base URL with a relative URL
func combineURLs(baseURL, relativeURL string) (string, error) {
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return "", err // Handle parsing error
	}

	// Reconstruct the base URL with the scheme and hostname
	reconstructedBaseURL := parsedURL.Scheme + "://" + parsedURL.Host

	// Combine with relative URL
	if strings.HasPrefix(relativeURL, "/") {
		return reconstructedBaseURL + relativeURL, nil
	}
	return relativeURL, nil
}

// StartCrawler is responsible for initializing the crawler
func StartCrawler(cf cfg.Config) {
	config = cf
}

// StartSelenium is responsible for initializing Selenium Driver
// The commented out code could be used to initialize a local Selenium server
// instead of using only a container based one. However, I found that the container
// based Selenium server is more stable and reliable than the local one.
// and it's obviously easier to setup and more secure.
func StartSelenium() (*selenium.Service, error) {
	log.Println("Configuring Selenium...")
	// Start a Selenium WebDriver server instance (e.g., chromedriver)
	/*
		var opts []selenium.ServiceOption
		// Add debug option to see errors
		// opts = append(opts, selenium.Output(os.Stderr))

		// Add options to control the user selected browser
		if config.Selenium.Type == "firefox" {
			opts = append(opts, selenium.GeckoDriver(config.Selenium.DriverPath)) // Specify the path to GeckoDriver
		} else if config.Selenium.Type == "chrome" {
			opts = append(opts, selenium.ChromeDriver(config.Selenium.DriverPath)) // Specify the path to ChromeDriver
		} else if config.Selenium.Type == "chromium" {
			opts = append(opts, selenium.ChromeDriver(config.Selenium.DriverPath)) // Specify the path to ChromeDriver
		} else {
			log.Fatalf("  Error: Unsupported browser type: %s\n", config.Selenium.Type)
		}
	*/
	/*
		var cmd *exec.Cmd
		if config.OS == "darwin" {
			cmd = exec.Command("brew", "services", "start", "selenium-server")
		} else if config.OS == "linux" {
			cmd = exec.Command("sudo", "systemctl", "start", "selenium-server")
		} else {
			log.Fatalf("  Error: Unsupported OS: %s\n", config.OS)
		}
		// Execute the command
		if err := cmd.Run(); err != nil {
			log.Printf("  Error starting Selenium server: %v\n", err)
		} else {
			log.Println("  Selenium server started successfully... ")
		}
	*/

	// Start a Selenium WebDriver server instance (e.g., chromedriver)
	//service, err := selenium.NewSeleniumService(config.Selenium.Path, config.Selenium.Port, opts...)
	//service, err := selenium.NewChromeDriverService(config.Selenium.DriverPath, config.Selenium.Port)
	var service *selenium.Service
	var err error // = nil
	log.Printf("Done!\n")

	return service, err
}

// StopSelenium Stops the Selenium server instance (if local)
func StopSelenium(sel *selenium.Service) {
	err := sel.Stop()
	if err != nil {
		log.Printf("Error stopping Selenium: %v\n", err)
	}
}

// ConnectSelenium is responsible for connecting to the Selenium server instance
func ConnectSelenium(sel *selenium.Service, config cfg.Config) (selenium.WebDriver, error) {
	// Connect to the WebDriver instance running locally.
	caps := selenium.Capabilities{"browserName": config.Selenium.Type}
	caps.AddChrome(chrome.Capabilities{
		Args: []string{
			"--headless", // Updated headless mode argument
			"--no-sandbox",
			"--disable-dev-shm-usage",
		},
		W3C: true,
	})
	wd, err := selenium.NewRemote(caps, fmt.Sprintf("http://"+config.Selenium.Host+":%d/wd/hub", config.Selenium.Port))
	//wd, err := selenium.NewRemote(caps, "")
	return wd, err
}

// QuitSelenium is responsible for quitting the Selenium server instance
func QuitSelenium(wd selenium.WebDriver) {
	err := wd.Quit()
	if err != nil {
		log.Printf("Error quitting Selenium: %v\n", err)
	}
}

// TakeScreenshot is responsible for taking a screenshot of the current page
func TakeScreenshot(wd selenium.WebDriver, filename string) error {
	// Execute JavaScript to get the viewport height and width
	windowHeight, windowWidth, err := getWindowSize(wd)
	if err != nil {
		return err
	}

	totalHeight, err := getTotalHeight(wd)
	if err != nil {
		return err
	}

	screenshots, err := captureScreenshots(wd, totalHeight, windowHeight)
	if err != nil {
		return err
	}

	finalImg, err := stitchScreenshots(screenshots, windowWidth, totalHeight)
	if err != nil {
		return err
	}

	screenshot, err := encodeImage(finalImg)
	if err != nil {
		return err
	}

	err = saveScreenshot(filename, screenshot)
	if err != nil {
		return err
	}

	return nil
}

func getWindowSize(wd selenium.WebDriver) (int, int, error) {
	// Execute JavaScript to get the viewport height and width
	viewportSizeScript := "return [window.innerHeight, window.innerWidth]"
	viewportSizeRes, err := wd.ExecuteScript(viewportSizeScript, nil)
	if err != nil {
		return 0, 0, err
	}
	viewportSize, ok := viewportSizeRes.([]interface{})
	if !ok || len(viewportSize) != 2 {
		return 0, 0, fmt.Errorf("unexpected result format for viewport size: %+v", viewportSizeRes)
	}
	windowHeight, err := strconv.Atoi(fmt.Sprint(viewportSize[0]))
	if err != nil {
		return 0, 0, err
	}
	windowWidth, err := strconv.Atoi(fmt.Sprint(viewportSize[1]))
	if err != nil {
		return 0, 0, err
	}
	return windowHeight, windowWidth, nil
}

func getTotalHeight(wd selenium.WebDriver) (int, error) {
	// Execute JavaScript to get the total height of the page
	totalHeightScript := "return document.body.parentNode.scrollHeight"
	totalHeightRes, err := wd.ExecuteScript(totalHeightScript, nil)
	if err != nil {
		return 0, err
	}
	totalHeight, err := strconv.Atoi(fmt.Sprint(totalHeightRes))
	if err != nil {
		return 0, err
	}
	return totalHeight, nil
}

func captureScreenshots(wd selenium.WebDriver, totalHeight, windowHeight int) ([][]byte, error) {
	var screenshots [][]byte
	for y := 0; y < totalHeight; y += windowHeight {
		// Scroll to the next part of the page
		scrollScript := fmt.Sprintf("window.scrollTo(0, %d);", y)
		_, err := wd.ExecuteScript(scrollScript, nil)
		if err != nil {
			return nil, err
		}

		// Take screenshot of the current view
		screenshot, err := wd.Screenshot()
		if err != nil {
			return nil, err
		}

		screenshots = append(screenshots, screenshot)
	}
	return screenshots, nil
}

func stitchScreenshots(screenshots [][]byte, windowWidth, totalHeight int) (*image.RGBA, error) {
	finalImg := image.NewRGBA(image.Rect(0, 0, windowWidth, totalHeight))
	currentY := 0
	for i, screenshot := range screenshots {
		img, _, err := image.Decode(bytes.NewReader(screenshot))
		if err != nil {
			return nil, err
		}

		// If this is the last screenshot, we may need to adjust the y offset to avoid duplication
		if i == len(screenshots)-1 {
			// Calculate the remaining height to capture
			remainingHeight := totalHeight - currentY
			bounds := img.Bounds()
			imgHeight := bounds.Dy()

			// If the remaining height is less than the image height, adjust the bounds
			if remainingHeight < imgHeight {
				bounds = image.Rect(bounds.Min.X, bounds.Max.Y-remainingHeight, bounds.Max.X, bounds.Max.Y)
			}

			// Draw the remaining part of the image onto the final image
			currentY = drawRemainingImage(finalImg, img, bounds, currentY)
		} else {
			currentY = drawImage(finalImg, img, currentY)
		}
	}
	return finalImg, nil
}

func drawRemainingImage(finalImg *image.RGBA, img image.Image, bounds image.Rectangle, currentY int) int {
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			finalImg.Set(x, currentY, img.At(x, y))
		}
		currentY++
	}
	return currentY
}

func drawImage(finalImg *image.RGBA, img image.Image, currentY int) int {
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			finalImg.Set(x, currentY, img.At(x, y))
		}
		currentY++
	}
	return currentY
}

func encodeImage(img *image.RGBA) ([]byte, error) {
	buffer := new(bytes.Buffer)
	err := png.Encode(buffer, img)
	if err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// saveScreenshot is responsible for saving a screenshot to a file
func saveScreenshot(filename string, screenshot []byte) error {
	// Check if ImageStorageAPI is set
	if config.ImageStorageAPI.Host != "" {
		// Validate the ImageStorageAPI configuration
		if err := validateImageStorageAPIConfig(config); err != nil {
			return err
		}

		saveCfg := config.ImageStorageAPI

		// Determine storage method and call appropriate function
		switch config.ImageStorageAPI.Type {
		case "http":
			return writeDataViaHTTP(filename, screenshot, saveCfg)
		case "s3":
			return writeDataToToS3(filename, screenshot, saveCfg)
		// Add cases for other types if needed, e.g., shared volume, message queue, etc.
		default:
			return errors.New("unsupported storage type")
		}
	} else {
		// Fallback to local file saving
		return writeToFile(config.ImageStorageAPI.Path+"/"+filename, screenshot)
	}
}

// validateImageStorageAPIConfig validates the ImageStorageAPI configuration
func validateImageStorageAPIConfig(checkCfg cfg.Config) error {
	if checkCfg.ImageStorageAPI.Host == "" || checkCfg.ImageStorageAPI.Port == 0 {
		return errors.New("invalid ImageStorageAPI configuration: host and port must be set")
	}
	// Add additional validation as needed
	return nil
}

// saveScreenshotViaHTTP sends the screenshot to a remote API
func writeDataViaHTTP(filename string, data []byte, saveCfg cfg.FileStorageAPI) error {
	// Construct the API endpoint URL
	apiURL := fmt.Sprintf("http://%s:%d/store_image", saveCfg.Host, saveCfg.Port)

	// Prepare the request
	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Filename", filename)
	req.Header.Set("Authorization", "Bearer "+saveCfg.Token) // Assuming token-based auth

	// Send the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Check for a successful response
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to save file, status code: %d", resp.StatusCode)
	}

	return nil
}

// writeToFile is responsible for writing data to a file
func writeToFile(filename string, data []byte) error {
	// Write data to a file
	err := writeDataToFile(filename, data)
	if err != nil {
		return err
	}

	return nil
}

// writeDataToFile is responsible for writing data to a file
func writeDataToFile(filename string, data []byte) error {
	// open file using READ & WRITE permission
	file, err := os.OpenFile(filename, os.O_RDWR|os.O_CREATE, 0755)
	if err != nil {
		return err
	}
	defer file.Close()

	// write data to file
	_, err = file.Write(data)
	if err != nil {
		return err
	}

	return nil
}

// writeDataToToS3 is responsible for saving a screenshot to an S3 bucket
func writeDataToToS3(filename string, data []byte, saveCfg cfg.FileStorageAPI) error {
	// saveScreenshotToS3 uses:
	// - config.ImageStorageAPI.Region as AWS region
	// - config.ImageStorageAPI.Token as AWS access key ID
	// - config.ImageStorageAPI.Secret as AWS secret access key
	// - config.ImageStorageAPI.Path as S3 bucket name
	// - filename as S3 object key

	// Create an AWS session
	sess, err := session.NewSession(&aws.Config{
		Region:      aws.String(saveCfg.Region),
		Credentials: credentials.NewStaticCredentials(saveCfg.Token, saveCfg.Secret, ""),
	})
	if err != nil {
		return err
	}

	// Create an S3 service client
	svc := s3.New(sess)

	// Upload the screenshot to the S3 bucket
	_, err = svc.PutObject(&s3.PutObjectInput{
		Bucket: aws.String(saveCfg.Path),
		Key:    aws.String(filename),
		Body:   bytes.NewReader(data),
	})
	return err
}
