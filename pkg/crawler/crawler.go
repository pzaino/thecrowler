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
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/abadojack/whatlanggo"
	cmn "github.com/pzaino/thecrowler/pkg/common"
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
	dbConnCheckErr = "Error checking database connection: %v\n"
)

var (
	config cfg.Config // Configuration "object"
)

type processContext struct {
	db         *cdb.Handler
	wd         selenium.WebDriver
	linksMutex sync.Mutex
	newLinks   []string
	source     *cdb.Source
	wg         sync.WaitGroup
}

var indexPageMutex sync.Mutex // Mutex to ensure that only one goroutine is indexing a page at a time

// CrawlWebsite is responsible for crawling a website, it's the main entry point
// and it's called from the main.go when there is a Source to crawl.
func CrawlWebsite(db cdb.Handler, source cdb.Source, sel SeleniumInstance, SeleniumInstances chan SeleniumInstance) {

	// Define wd
	var err error
	var processCtx processContext
	processCtx.db = &db
	processCtx.source = &source

	// Connect to Selenium
	processCtx.wd, err = ConnectSelenium(sel, 0)
	if err != nil {
		log.Println("Error connecting to Selenium:", err)
		// Return the Selenium instance to the channel
		SeleniumInstances <- sel
		return
	}
	defer QuitSelenium(&processCtx.wd)
	log.Println("Connected to Selenium WebDriver successfully.")

	// Crawl the initial URL and get the HTML content
	// This is where you'd use Selenium or another method to get the page content
	log.Println("Crawling URL:", source.URL)
	pageSource, err := getURLContent(source.URL, processCtx.wd, 0)
	if err != nil {
		log.Println("Error getting HTML content:", err)
		// Return the Selenium instance to the channel
		SeleniumInstances <- sel
		// Update the source state in the database
		updateSourceState(db, source.URL, err)
		return
	}

	// Try to find and click the 'Accept' or 'Consent' button in different languages
	for _, text := range append(acceptTexts, consentTexts...) {
		if clicked := findAndClickButton(processCtx.wd, text); clicked {
			break
		}
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
		err = TakeScreenshot(&processCtx.wd, imageName)
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
		// Return the Selenium instance to the channel
		SeleniumInstances <- sel
		// Update the source state in the database
		updateSourceState(db, source.URL, err)
		return
	}
	initialLinks := extractLinks(htmlContent)

	allLinks := initialLinks // links extracted from the initial page
	var currentDepth int
	maxDepth := config.Crawler.MaxDepth // set a maximum depth for crawling

	if maxDepth == 0 {
		maxDepth = 1
	}

	// Refresh the page
	if err := processCtx.wd.Refresh(); err != nil {
		processCtx.wd, err = ConnectSelenium(sel, 0)
		if err != nil {
			log.Println("Error re-connecting to Selenium:", err)
			// Return the Selenium instance to the channel
			SeleniumInstances <- sel
			// Update the source state in the database
			updateSourceState(db, source.URL, err)
			return
		}
	}

	// Crawl the website
	newLinksFound := len(initialLinks)
	for (currentDepth < maxDepth) && (newLinksFound > 0) {
		// Create a channel to enqueue jobs
		jobs := make(chan string, len(allLinks))

		// Create a channel to signal completion
		done := make(chan bool, config.Crawler.Workers)

		// Launch worker goroutines
		for w := 1; w <= config.Crawler.Workers; w++ {
			processCtx.wg.Add(1)
			go worker(&processCtx, w, jobs, done)
		}

		// Enqueue jobs (allLinks)
		for _, link := range allLinks {
			jobs <- link
		}
		close(jobs)

		log.Println("Enqueued jobs:", len(allLinks))

		log.Println("Waiting for workers to finish...")
		// Wait for workers to finish and collect new links
		for i := 0; i < config.Crawler.Workers; i++ {
			<-done
		}
		// Wait for all the wg workers to finish
		processCtx.wg.Wait()
		log.Println("All workers finished.")

		// Prepare for the next iteration
		processCtx.linksMutex.Lock()
		if len(processCtx.newLinks) > 0 {
			newLinksFound = len(processCtx.newLinks)
			allLinks = processCtx.newLinks
			processCtx.newLinks = []string{} // reset newLinks
		} else {
			newLinksFound = 0
			processCtx.newLinks = []string{} // reset newLinks
		}
		processCtx.linksMutex.Unlock()
		currentDepth++
		if config.Crawler.MaxDepth == 0 {
			maxDepth = currentDepth + 1
		}
	}

	// Return the Selenium instance to the channel
	SeleniumInstances <- sel

	// Update the source state in the database
	updateSourceState(db, source.URL, nil)
	log.Printf("Finished crawling website: %s\n", source.URL)
}

func findAndClickButton(wd selenium.WebDriver, buttonText string) bool {
	buttonTextLower := strings.ToLower(buttonText)
	consentSelectors := []string{
		// IDs and classes containing buttonText
		fmt.Sprintf("//*[@id[contains(translate(., 'ABCDEFGHIJKLMNOPQRSTUVWXYZ', 'abcdefghijklmnopqrstuvwxyz'), '%s')]]", buttonTextLower),
		fmt.Sprintf("//*[@class[contains(translate(., 'ABCDEFGHIJKLMNOPQRSTUVWXYZ', 'abcdefghijklmnopqrstuvwxyz'), '%s')]]", buttonTextLower),
		// Text-based buttons and links
		fmt.Sprintf("//*[contains(translate(., 'ABCDEFGHIJKLMNOPQRSTUVWXYZ', 'abcdefghijklmnopqrstuvwxyz'), '%s')]", buttonTextLower),
		// Image-based buttons (alt text)
		fmt.Sprintf("//img[contains(@alt, '%s')]", buttonText),
		// Add more selectors as necessary
	}

	for _, selector := range consentSelectors {
		if clickButtonBySelector(wd, selector) {
			return true
		}
	}

	return false
}

func clickButtonBySelector(wd selenium.WebDriver, selector string) bool {
	elements, err := wd.FindElements(selenium.ByXPATH, selector)
	if err != nil {
		return false
	}

	for _, element := range elements {
		if isVisibleAndClickable(element) {
			err = element.Click()
			if err == nil {
				return true
			}
			// Log or handle the error if click failed
		}
	}

	time.Sleep(1 * time.Second)
	return false
}

func isVisibleAndClickable(element selenium.WebElement) bool {
	visible, _ := element.IsDisplayed()
	clickable, _ := element.IsEnabled()
	return visible && clickable
}

// updateSourceState is responsible for updating the state of a Source in
// the database after crawling it (it does consider errors too)
func updateSourceState(db cdb.Handler, sourceURL string, crawlError error) {
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
func indexPage(db cdb.Handler, url string, pageInfo PageInfo) {
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
        INSERT INTO SearchIndex
			(source_id, page_url, title, summary, content, detected_lang, detected_type, indexed_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
        ON CONFLICT (page_url) DO UPDATE
        SET title = EXCLUDED.title, summary = EXCLUDED.summary, content = EXCLUDED.content, detected_lang = EXCLUDED.detected_lang, detected_type = EXCLUDED.detected_type, indexed_at = NOW()
        RETURNING index_id`, pageInfo.sourceID, url, pageInfo.Title, pageInfo.Summary, pageInfo.BodyText, strLeft(pageInfo.DetectedLang, 8), strLeft(pageInfo.DetectedType, 8)).
		Scan(&indexID)
	return indexID, err
}

func strLeft(s string, x int) string {
	runes := []rune(s)
	if x < 0 || x > len(runes) {
		return s
	}
	return string(runes[:x])
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
func insertKeywords(tx *sql.Tx, db cdb.Handler, indexID int, pageInfo PageInfo) error {
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
func insertKeywordWithRetries(db cdb.Handler, keyword string) (int, error) {
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

// getURLContent is responsible for retrieving the HTML content of a page
// from Selenium and returning it as a WebDriver object
func getURLContent(url string, wd selenium.WebDriver, level int) (selenium.WebDriver, error) {
	// Navigate to a page and interact with elements.
	err0 := wd.Get(url)
	cmd, _ := cmn.ParseCmd(config.Crawler.Interval, 0)
	delayStr, _ := cmn.InterpretCmd(cmd)
	delay, err := strconv.ParseFloat(delayStr, 64)
	if err != nil {
		delay = 1
	}
	if level > 0 {
		time.Sleep(time.Second * time.Duration(delay)) // Pause to let page load
	} else {
		time.Sleep(time.Second * time.Duration((delay + 5))) // Pause to let Home page load
	}
	return wd, err0
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

	currentURL, _ := webPage.CurrentURL()

	return PageInfo{
		Title:        title,
		Summary:      summary,
		BodyText:     bodyText,
		MetaTags:     metaTags,
		DetectedLang: detectLang(webPage),
		DetectedType: inferDocumentType(currentURL),
	}
}

func detectLang(wd selenium.WebDriver) string {
	var lang string
	var err error
	html, err := wd.FindElement(selenium.ByXPATH, "/html")
	if err == nil {
		lang, err = html.GetAttribute("lang")
		if err != nil {
			lang = ""
		}
	}
	if lang == "" {
		bodyHTML, err := wd.FindElement(selenium.ByTagName, "body")
		if err != nil {
			log.Println("Error retrieving text:", err)
			return "unknown"
		}
		bodyText, _ := bodyHTML.Text()
		info := whatlanggo.Detect(bodyText)
		lang = whatlanggo.LangToString(info.Lang)
		if lang != "" {
			lang = convertLangStrToLangCode(lang)
		}
	}
	cmn.DebugMsg(cmn.DbgLvlDebug, "Language detected: %s", lang)
	return lang
}

func convertLangStrToLangCode(lang string) string {
	lng := strings.TrimSpace(strings.ToLower(lang))
	lng = langMap[lng]
	return lng
}

// inferDocumentType returns the document type based on the file extension
func inferDocumentType(url string) string {
	extension := strings.TrimSpace(strings.ToLower(filepath.Ext(url)))
	if extension != "" {
		if docType, ok := docTypeMap[extension]; ok {
			log.Println("Document Type:", docType)
			return docType
		}
	}
	return "UNKNOWN"
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
// isExternalLink checks if linkURL is external to sourceURL based on domainLevel.
func isExternalLink(sourceURL, linkURL string, domainLevel int) bool {
	// No restrictions
	if domainLevel == 4 {
		return false
	}

	linkURL = strings.TrimSpace(linkURL)
	if strings.HasPrefix(linkURL, "/") {
		return false // Relative URL, not external
	}

	sourceParsed, err := url.Parse(sourceURL)
	if err != nil {
		return false // Parsing error
	}

	linkParsed, err := url.Parse(linkURL)
	if err != nil {
		return false // Parsing error
	}

	srcDomainParts := strings.Split(sourceParsed.Hostname(), ".")
	linkDomainParts := strings.Split(linkParsed.Hostname(), ".")

	// Fully restricted, compare the entire URLs
	if domainLevel == 0 {
		return sourceParsed.String() != linkParsed.String()
	}

	// Get domain parts based on domainLevel
	srcDomain, linkDomain := getDomainParts(srcDomainParts, domainLevel), getDomainParts(linkDomainParts, domainLevel)

	// Compare the relevant parts of the domain
	return srcDomain != linkDomain
}

// getDomainParts extracts domain parts based on the domainLevel.
func getDomainParts(parts []string, level int) string {
	partCount := len(parts)
	switch {
	case level == 1 && partCount >= 3: // l3 domain restricted
		return strings.Join(parts[partCount-3:], ".")
	case level == 2 && partCount >= 2: // l2 domain restricted
		return strings.Join(parts[partCount-2:], ".")
	case level == 3 && partCount >= 1: // l1 domain restricted
		return parts[partCount-1]
	default:
		return strings.Join(parts, ".")
	}
}

// worker is the worker function that is responsible for crawling a page
func worker(processCtx *processContext,
	id int, jobs chan string, done chan<- bool) {
	defer processCtx.wg.Done()
	skip := false
	for url := range jobs {
		skip = false

		url = strings.TrimSpace(url)
		if url == "" {
			skip = true
		}

		// Check if the URL is relative
		if strings.HasPrefix(url, "/") {
			url, _ = combineURLs(processCtx.source.URL, url)
		}

		// Check if the URL is external
		if processCtx.source.Restricted != 4 {
			// If the Source is restricted
			// check if the url is outside the Source domain
			// if so skip it
			if isExternalLink(processCtx.source.URL, url, processCtx.source.Restricted) {
				log.Printf("Worker %d: Skipping URL '%s' due 'restricted' policy.\n", id, url)
				skip = true
			}
		}

		if !skip {
			log.Printf("Worker %d: started job %s\n", id, url)

			// Get HTML content of the page
			htmlContent, err := getURLContent(url, processCtx.wd, 1)
			if err != nil {
				skip = true
				log.Printf("Worker %d: Error getting HTML content for %s: %v\n", id, url, err)
				continue
			}

			// Extract necessary information from the content
			// For simplicity, we're extracting just the title and full content
			// You might want to extract more specific information
			pageCache := extractPageInfo(htmlContent)

			// Index the page content in the database
			pageCache.sourceID = processCtx.source.ID
			indexPage(*processCtx.db, url, pageCache)

			// Extract links and add them to newLinks
			extractedLinks := extractLinks(pageCache.BodyText)
			if len(extractedLinks) > 0 {
				processCtx.linksMutex.Lock()
				processCtx.newLinks = append(processCtx.newLinks, extractedLinks...)
				processCtx.linksMutex.Unlock()
			}

			log.Printf("Worker %d: finished job %s\n", id, url)
			if config.Crawler.Delay != "0" {
				if isNumber(config.Crawler.Delay) {
					delay, err := strconv.ParseFloat(config.Crawler.Delay, 64)
					if err != nil {
						delay = 1
					}
					time.Sleep(time.Duration(delay) * time.Second)
				} else {
					cmd, _ := cmn.ParseCmd(config.Crawler.Delay, 0)
					delayStr, _ := cmn.InterpretCmd(cmd)
					delay, err := strconv.ParseFloat(delayStr, 64)
					if err != nil {
						delay = 1
					}
					time.Sleep(time.Duration(delay) * time.Second)
				}
			}
		}
	}
	if skip {
		log.Printf("Worker %d: finished job.\n", id)
	}
	done <- true
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

// isNumber checks if the given string is a number.
func isNumber(str string) bool {
	_, err := strconv.ParseFloat(str, 64)
	return err == nil
}

// StartCrawler is responsible for initializing the crawler
func StartCrawler(cf cfg.Config) {
	config = cf
}

// NewSeleniumService is responsible for initializing Selenium Driver
// The commented out code could be used to initialize a local Selenium server
// instead of using only a container based one. However, I found that the container
// based Selenium server is more stable and reliable than the local one.
// and it's obviously easier to setup and more secure.
func NewSeleniumService(c cfg.Selenium) (*selenium.Service, error) {
	log.Println("Configuring Selenium...")
	var service *selenium.Service
	var err error
	var retries int
	for {
		service, err = selenium.NewSeleniumService(fmt.Sprintf("http://"+c.Host+":%d/wd/hub"), c.Port)
		if err != nil {
			log.Printf("Error starting Selenium server: %v\n", err)
			log.Printf("Check if Selenium is running on host '%s' at port '%d'.\n", c.Host, c.Port)
			log.Printf("Retrying in 5 seconds...\n")
			retries++
			if retries > 5 {
				log.Printf("Exceeded maximum retries. Exiting...\n")
				break
			} else {
				time.Sleep(5 * time.Second)
			}
		} else {
			log.Println("Selenium server started successfully.")
			break
		}
	}

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

	if err == nil {
		log.Printf("Done!\n")
	}
	return service, err
}

// StopSelenium Stops the Selenium server instance (if local)
func StopSelenium(sel *selenium.Service) error {
	if sel == nil {
		return errors.New("selenium service is not running")
	}
	// Stop the Selenium WebDriver server instance
	err := sel.Stop()
	if err != nil {
		log.Printf("Error stopping Selenium: %v\n", err)
	} else {
		log.Println("Selenium stopped successfully.")
	}
	return err
}

// ConnectSelenium is responsible for connecting to the Selenium server instance
func ConnectSelenium(sel SeleniumInstance, browseType int) (selenium.WebDriver, error) {
	// Connect to the WebDriver instance running locally.
	caps := selenium.Capabilities{"browserName": sel.Config.Type}

	// Define the user agent string for a desktop Google Chrome browser
	var userAgent string
	if browseType == 0 {
		userAgent = agentStrMap["desktop1"]
	} else if browseType == 1 {
		userAgent = agentStrMap["mobile1"]
	}

	var args []string

	// Populate the args slice based on the browser type
	keys := []string{"WindowSize", "initialWindow", "sandbox", "gpu", "disableDevShm", "headless", "javascript", "incognito", "popups", "infoBars", "extensions"}
	for _, key := range keys {
		if value, ok := browserSettingsMap[sel.Config.Type][key]; ok && value != "" {
			args = append(args, value)
		}
	}

	// Append user-agent separately as it's a constant value
	args = append(args, "--user-agent="+userAgent)

	caps.AddChrome(chrome.Capabilities{
		Args: args,
		W3C:  true,
	})

	// Connect to the WebDriver instance running remotely.
	var wd selenium.WebDriver
	var err error
	for {
		wd, err = selenium.NewRemote(caps, fmt.Sprintf("http://"+sel.Config.Host+":%d/wd/hub", sel.Config.Port))
		if err != nil {
			log.Println("Error connecting to Selenium: ", err)
			time.Sleep(5 * time.Second)
		} else {
			break
		}
	}
	return wd, err
}

// QuitSelenium is responsible for quitting the Selenium server instance
func QuitSelenium(wd *selenium.WebDriver) {
	// Close the WebDriver
	if wd != nil {
		// Attempt a simple operation to check if the session is still valid
		_, err := (*wd).CurrentURL()
		if err != nil {
			log.Printf("WebDriver session may have already ended: %v", err)
			return
		}

		// Close the WebDriver if the session is still active
		err = (*wd).Quit()
		if err != nil {
			log.Printf("Error closing WebDriver: %v", err)
		} else {
			log.Println("WebDriver closed successfully.")
		}
	}
}

// TakeScreenshot is responsible for taking a screenshot of the current page
func TakeScreenshot(wd *selenium.WebDriver, filename string) error {
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

func getWindowSize(wd *selenium.WebDriver) (int, int, error) {
	// Execute JavaScript to get the viewport height and width
	viewportSizeScript := "return [window.innerHeight, window.innerWidth]"
	viewportSizeRes, err := (*wd).ExecuteScript(viewportSizeScript, nil)
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

func getTotalHeight(wd *selenium.WebDriver) (int, error) {
	// Execute JavaScript to get the total height of the page
	totalHeightScript := "return document.body.parentNode.scrollHeight"
	totalHeightRes, err := (*wd).ExecuteScript(totalHeightScript, nil)
	if err != nil {
		return 0, err
	}
	totalHeight, err := strconv.Atoi(fmt.Sprint(totalHeightRes))
	if err != nil {
		return 0, err
	}
	return totalHeight, nil
}

func captureScreenshots(wd *selenium.WebDriver, totalHeight, windowHeight int) ([][]byte, error) {
	var screenshots [][]byte
	for y := 0; y < totalHeight; y += windowHeight {
		// Scroll to the next part of the page
		scrollScript := fmt.Sprintf("window.scrollTo(0, %d);", y)
		_, err := (*wd).ExecuteScript(scrollScript, nil)
		if err != nil {
			return nil, err
		}
		time.Sleep(1 * time.Second) // Pause to let page load

		// Take screenshot of the current view
		screenshot, err := (*wd).Screenshot()
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
