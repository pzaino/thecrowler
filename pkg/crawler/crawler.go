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

package crawler

import (
	"database/sql"
	"fmt"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	cfg "TheCrow/pkg/config"
	db "TheCrow/pkg/database"

	"github.com/PuerkitoBio/goquery"
	"github.com/tebeka/selenium"
	"github.com/tebeka/selenium/chrome"
)

const (
	numberOfWorkers = 5 // Adjust this based on your needs
)

var (
	config cfg.Config
)

// This struct represents the information that we want to extract from a page
// and store in the database.
type PageInfo struct {
	Title           string
	Summary         string
	BodyText        string
	ContainsAppInfo bool
	MetaTags        map[string]string // Add a field for meta tags
}

var indexPageMutex sync.Mutex // Mutex to ensure that only one goroutine is indexing a page at a time

// This function is responsible for crawling a website, it's the main entry point
// and it's called from the main.go when there is a Source to crawl.
func CrawlWebsite(db *sql.DB, source db.Source, wd selenium.WebDriver) {
	// Crawl the initial URL and get the HTML content
	// This is where you'd use Selenium or another method to get the page content
	pageSource, err := getHTMLContent(source.URL, wd)
	if err != nil {
		log.Println("Error getting HTML content:", err)
		// Update the source state in the database
		updateSourceState(db, source.URL, err)
		return
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

// This function is responsible for updating the state of a Source in
// the database after crawling it (it does consider errors too)
func updateSourceState(db *sql.DB, sourceURL string, crawlError error) {
	var err error

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

// This function is responsible for indexing a crawled page in the database
// I had to write this function quickly, so it's not very efficient.
// In an ideal world, I would have used multiple transactions to index the page
// and avoid deadlocks when inserting keywords. However, using a mutex to enter
// this function (and so treat it as a critical section) should be enough for now.
// Another thought is, the mutex also helps slow down the crawling process, which
// is a good thing. You don't want to overwhelm the Source site with requests.
func indexPage(db *sql.DB, url string, pageInfo PageInfo) {
	// Acquire a lock to ensure that only one goroutine is accessing the database
	indexPageMutex.Lock()
	defer indexPageMutex.Unlock()

	// Start a transaction
	tx, err := db.Begin()
	if err != nil {
		log.Printf("Error starting transaction: %v\n", err)
		return
	}

	// Insert or update the page in SearchIndex
	var indexID int
	err = tx.QueryRow(`
        INSERT INTO SearchIndex (page_url, title, summary, content, indexed_at)
        VALUES ($1, $2, $3, $4, NOW())
        ON CONFLICT (page_url) DO UPDATE
        SET title = EXCLUDED.title, summary = EXCLUDED.summary, content = EXCLUDED.content, indexed_at = NOW()
        RETURNING index_id`, url, pageInfo.Title, pageInfo.Summary, pageInfo.BodyText).
		Scan(&indexID)
	if err != nil {
		log.Printf("Error inserting into or updating SearchIndex: %v\n", err)
		tx.Rollback()
		return
	}

	// Insert MetaTags
	for name, content := range pageInfo.MetaTags {
		_, err = tx.Exec(`INSERT INTO MetaTags (index_id, name, content)
                          VALUES ($1, $2, $3)`, indexID, name, content)
		if err != nil {
			log.Printf("Error inserting meta tag: %v\n", err)
			tx.Rollback()
			return
		}
	}

	// Insert into KeywordIndex
	for _, keyword := range extractKeywords(pageInfo) {
		// Insert or find keyword ID
		if config.DebugLevel > 0 {
			fmt.Println("Inserting keyword:", keyword)
		}
		var keywordID int
		keywordID, err := insertKeywordWithRetries(db, keyword)
		if err != nil {
			log.Printf("Error inserting or finding keyword: %v\n", err)
			tx.Rollback()
			return
		}

		// Insert into KeywordIndex
		_, err = tx.Exec(`INSERT INTO KeywordIndex (keyword_id, index_id)
                          VALUES ($1, $2)`, keywordID, indexID)
		if err != nil {
			log.Printf("Error inserting into KeywordIndex: %v\n", err)
			tx.Rollback()
			return
		}
	}

	// Commit the transaction
	if err := tx.Commit(); err != nil {
		log.Printf("Error committing transaction: %v\n", err)
		tx.Rollback()
		return
	}
}

// This function is responsible for storing the extracted keywords in the database
// It's written to be efficient and avoid deadlocks, but this right now is not required
// because indexPage uses a mutex to ensure that only one goroutine is indexing a page
// at a time. However, when implementing multiple transactions in indexPage, this function
// will be way more useful than it is now.
func insertKeywordWithRetries(db *sql.DB, keyword string) (int, error) {
	const maxRetries = 3
	var keywordID int
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

// This function is responsible for retrieving the HTML content of a page
// from Selenium and returning it as a WebDriver object
func getHTMLContent(url string, wd selenium.WebDriver) (selenium.WebDriver, error) {
	// Navigate to a page and interact with elements.
	if err := wd.Get(url); err != nil {
		return nil, err
	}
	time.Sleep(time.Second * time.Duration(config.Crawler.Interval)) // Pause to let page load
	return wd, nil
}

// This function is responsible for extracting information from a collected page.
// In the future we may want to expand this function to extract more information
// from the page, such as images, videos, etc. and do a better job at screen scraping.
func extractPageInfo(webPage selenium.WebDriver) PageInfo {
	htmlContent, _ := webPage.PageSource()
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		log.Printf("Error loading HTML content: %v", err)
		return PageInfo{} // Return an empty struct in case of an error
	}

	//title := doc.Find("title").Text()
	title, _ := webPage.Title()
	summary := doc.Find("meta[name=description]").AttrOr("content", "")
	bodyText := doc.Find("body").Text()
	//bodyText, _ := webPage.PageSource()

	containsAppInfo := strings.Contains(bodyText, "app") || strings.Contains(bodyText, "mobile")

	metaTags := extractMetaTags(doc)

	return PageInfo{
		Title:           title,
		Summary:         summary,
		BodyText:        bodyText,
		ContainsAppInfo: containsAppInfo,
		MetaTags:        metaTags,
	}
}

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

func extractLinks(htmlContent string) []string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		log.Fatal("Error loading HTTP response body. ", err)
	}

	var links []string
	doc.Find("a").Each(func(index int, item *goquery.Selection) {
		linkTag := item
		link, _ := linkTag.Attr("href")
		links = append(links, link)
	})
	return links
}

// Check if the link is external (aka outside the Source domain)
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
	// Compare hostnames
	return sourceParsed.Hostname() != linkParsed.Hostname()
}

// This is the worker function that is responsible for crawling a page
func worker(db *sql.DB, wd selenium.WebDriver,
	id int, jobs <-chan string, wg *sync.WaitGroup, source *db.Source) {
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
		indexPage(db, url, pageCache)

		log.Printf("Worker %d: finished job %s\n", id, url)
	}
}

// Utility function to combine a base URL with a relative URL
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

// This function is responsible for initializing the crawler
func StartCrawler(cf cfg.Config) {
	config = cf
}

// This function is responsible for initializing Selenium Driver
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
	var err error = nil
	log.Printf("Done!\n")

	return service, err
}

// Stop the Selenium server instance (if local)
func StopSelenium(sel *selenium.Service) {
	sel.Stop()
}

// This function is responsible for connecting to the Selenium server instance
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

// This function is responsible for quitting the Selenium server instance
func QuitSelenium(wd selenium.WebDriver) {
	wd.Quit()
}
