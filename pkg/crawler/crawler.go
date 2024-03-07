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
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
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
	exi "github.com/pzaino/thecrowler/pkg/exprterpreter"
	httpi "github.com/pzaino/thecrowler/pkg/httpinfo"
	neti "github.com/pzaino/thecrowler/pkg/netinfo"
	rules "github.com/pzaino/thecrowler/pkg/ruleset"

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

// processContext is a struct that holds the context of the crawling process
// It's used to pass data between functions and goroutines and holds the
// DB index of the source page after it's indexed.
type processContext struct {
	fpIdx      int64                 // The index of the source page after it's indexed
	config     cfg.Config            // The configuration object (from the config package)
	db         *cdb.Handler          // The database handler
	wd         selenium.WebDriver    // The Selenium WebDriver
	linksMutex sync.Mutex            // Mutex to protect the newLinks slice
	newLinks   []string              // The new links found during the crawling process
	source     *cdb.Source           // The source to crawl
	wg         sync.WaitGroup        // WaitGroup to wait for all workers to finish
	sel        chan SeleniumInstance // The Selenium instances channel
	ni         *neti.NetInfo         // The network information of the web page
	hi         *httpi.HTTPDetails    // The HTTP header information of the web page
	re         *rules.RuleEngine     // The rule engine
}

var indexPageMutex sync.Mutex // Mutex to ensure that only one goroutine is indexing a page at a time

// CrawlWebsite is responsible for crawling a website, it's the main entry point
// and it's called from the main.go when there is a Source to crawl.
func CrawlWebsite(db cdb.Handler, source cdb.Source, sel SeleniumInstance, SeleniumInstances chan SeleniumInstance, re *rules.RuleEngine) {
	// Initialize the process context
	var err error
	processCtx := processContext{
		source: &source,
		db:     &db,
		sel:    SeleniumInstances,
		config: config,
		re:     re,
	}

	// Connect to Selenium
	if err := processCtx.ConnectToSelenium(sel); err != nil {
		return
	}
	defer QuitSelenium(&processCtx.wd)

	// Crawl the initial URL and get the HTML content
	var pageSource selenium.WebDriver
	pageSource, err = processCtx.CrawlInitialURL(sel)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error crawling initial URL: %v", err)
		return
	}

	// Get screenshot of the page
	processCtx.TakeScreenshot(pageSource, source.URL, processCtx.fpIdx)

	// Extract the HTML content and extract links
	htmlContent, err := pageSource.PageSource()
	if err != nil {
		// Return the Selenium instance to the channel
		// and update the source state in the database
		cmn.DebugMsg(cmn.DbgLvlError, "Error getting page source: %v", err)
		SeleniumInstances <- sel
		updateSourceState(db, source.URL, err)
		return
	}
	initialLinks := extractLinks(htmlContent)

	// Refresh the page
	processCtx.RefreshSeleniumConnection(sel)

	// Crawl the website
	allLinks := initialLinks // links extracted from the initial page
	var currentDepth int
	maxDepth := config.Crawler.MaxDepth // set a maximum depth for crawling
	if maxDepth == 0 {
		maxDepth = 1
	}
	newLinksFound := len(initialLinks)
	for (currentDepth < maxDepth) && (newLinksFound > 0) {
		// Create a channel to enqueue jobs
		jobs := make(chan string, len(allLinks))

		// Launch worker goroutines
		for w := 1; w <= config.Crawler.Workers; w++ {
			processCtx.wg.Add(1)
			go worker(&processCtx, w, jobs)
		}

		// Enqueue jobs (allLinks)
		for _, link := range allLinks {
			jobs <- link
		}
		close(jobs)

		cmn.DebugMsg(cmn.DbgLvlDebug2, "Enqueued jobs: %d", len(allLinks))

		// Wait for workers to finish and collect new links
		cmn.DebugMsg(cmn.DbgLvlDebug, "Waiting for workers to finish...")
		processCtx.wg.Wait()
		cmn.DebugMsg(cmn.DbgLvlDebug, "All workers finished.")

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

		// Increment the current depth
		currentDepth++
		if config.Crawler.MaxDepth == 0 {
			maxDepth = currentDepth + 1
		}
	}

	// Return the Selenium instance to the channel
	SeleniumInstances <- sel

	// Update the source state in the database
	updateSourceState(db, source.URL, nil)
	cmn.DebugMsg(cmn.DbgLvlInfo, "Finished crawling website: %s", source.URL)
}

// ConnectSelenium is responsible for connecting to a Selenium instance
func (ctx *processContext) ConnectToSelenium(sel SeleniumInstance) error {
	var err error
	ctx.wd, err = ConnectSelenium(sel, 0)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error connecting to Selenium: %v", err)
		ctx.sel <- sel
		return err
	}
	cmn.DebugMsg(cmn.DbgLvlDebug1, "Connected to Selenium WebDriver successfully.")
	return nil
}

// RefreshSeleniumConnection is responsible for refreshing the Selenium connection
func (ctx *processContext) RefreshSeleniumConnection(sel SeleniumInstance) {
	if err := ctx.wd.Refresh(); err != nil {
		ctx.wd, err = ConnectSelenium(sel, 0)
		if err != nil {
			// Return the Selenium instance to the channel
			// and update the source state in the database
			cmn.DebugMsg(cmn.DbgLvlError, "Error re-connecting to Selenium: %v", err)
			ctx.sel <- sel
			updateSourceState(*ctx.db, ctx.source.URL, err)
			return
		}
	}
}

// CrawlInitialURL is responsible for crawling the initial URL of a Source
func (ctx *processContext) CrawlInitialURL(sel SeleniumInstance) (selenium.WebDriver, error) {
	cmn.DebugMsg(cmn.DbgLvlInfo, "Crawling URL: %s", ctx.source.URL)
	pageSource, err := getURLContent(ctx.source.URL, ctx.wd, 0, ctx)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error getting HTML content: %v", err)
		ctx.sel <- sel // Assuming 'sel' is accessible
		updateSourceState(*ctx.db, ctx.source.URL, err)
		return pageSource, err
	}

	// Handle consent
	handleConsent(ctx.wd)

	// Get network information
	ctx.GetNetInfo(ctx.source.URL)

	// Get HTTP information
	ctx.GetHTTPInfo(ctx.source.URL)

	// Continue with extracting page info and indexing
	pageInfo := extractPageInfo(pageSource, ctx)
	pageInfo.HTTPInfo = ctx.hi
	pageInfo.NetInfo = ctx.ni

	// Index the page
	ctx.fpIdx = ctx.IndexPage(pageInfo)
	return pageSource, nil
}

// TakeScreenshot takes a screenshot of the current page and saves it to the filesystem
func (ctx *processContext) TakeScreenshot(wd selenium.WebDriver, url string, indexID int64) {
	// Take screenshot if enabled
	takeScreenshot := false

	tmpURL1 := strings.ToLower(strings.TrimSpace(url))
	tmpURL2 := strings.ToLower(strings.TrimSpace(ctx.source.URL))

	if tmpURL1 == tmpURL2 {
		takeScreenshot = config.Crawler.SourceScreenshot
	} else {
		takeScreenshot = config.Crawler.FullSiteScreenshot
	}

	if takeScreenshot {
		cmn.DebugMsg(cmn.DbgLvlInfo, "Taking screenshot of %s...", url)
		// Create imageName using the hash. Adding a suffix like '.png' is optional depending on your use case.
		imageName := generateUniqueName(url, "-desktop")
		ss, err := TakeScreenshot(&wd, imageName)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Error taking screenshot: %v", err)
		}
		ss.IndexID = indexID
		if ss.IndexID == 0 {
			ss.IndexID = ctx.fpIdx
		}

		// Update DB SearchIndex Table with the screenshot filename
		dbx := *ctx.db
		err = insertScreenshot(dbx, ss)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Error updating database with screenshot URL: %v", err)
		}
	}
}

// generateImageName generates a unique name for a web object using the URL and the type
func generateUniqueName(url string, imageType string) string {
	// Hash the URL using SHA-256
	hasher := sha256.New()
	hasher.Write([]byte(url + imageType))
	hashBytes := hasher.Sum(nil)

	// Convert the hash to a hexadecimal string
	hashStr := hex.EncodeToString(hashBytes)

	// Create imageName using the hash. Adding a suffix like '.png' is optional depending on your use case.
	imageName := fmt.Sprintf("%s.png", hashStr)

	return imageName
}

// insertScreenshot inserts a screenshot into the database
func insertScreenshot(db cdb.Handler, screenshot Screenshot) error {
	if screenshot.IndexID == 0 {
		return errors.New("index ID is required")
	}

	_, err := db.Exec(`
        INSERT INTO Screenshots (
            index_id,
            screenshot_link,
            height,
            width,
            byte_size,
            thumbnail_height,
            thumbnail_width,
            thumbnail_link,
            format
        )
        SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9
        WHERE NOT EXISTS (
            SELECT 1 FROM Screenshots
            WHERE index_id = $1 AND screenshot_link = $2
        );
    `,
		screenshot.IndexID,
		screenshot.ScreenshotLink,
		screenshot.Height,
		screenshot.Width,
		screenshot.ByteSize,
		screenshot.ThumbnailHeight,
		screenshot.ThumbnailWidth,
		screenshot.ThumbnailLink,
		screenshot.Format,
	)
	return err
}

// GetNetInfo is responsible for gathering network information for a Source
func (ctx *processContext) GetNetInfo(url string) {
	// Create a new NetInfo instance
	ctx.ni = &neti.NetInfo{}
	c := ctx.config.NetworkInfo
	ctx.ni.Config = &c

	// Call GetNetInfo to retrieve network information
	cmn.DebugMsg(cmn.DbgLvlInfo, "Gathering network information for %s...", ctx.source.URL)
	err := ctx.ni.GetNetInfo(ctx.source.URL)

	// Check for errors
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "GetNetInfo(%s) returned an error: %v", ctx.source.URL, err)
		return
	}
}

// GetHTTPInfo is responsible for gathering HTTP header information for a Source
func (ctx *processContext) GetHTTPInfo(url string) {
	// Create a new HTTPDetails instance
	ctx.hi = &httpi.HTTPDetails{}
	var err error
	c := httpi.Config{
		URL:             url,
		CustomHeader:    map[string]string{"User-Agent": cmn.UsrAgentStrMap["chrome-desktop01"]},
		FollowRedirects: true,
	}

	// Call GetHTTPInfo to retrieve HTTP header information
	cmn.DebugMsg(cmn.DbgLvlInfo, "Gathering HTTP information for %s...", ctx.source.URL)
	ctx.hi, err = httpi.ExtractHTTPInfo(c)

	// Check for errors
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error while retrieving HTTP Information: %v", ctx.source.URL, err)
		return
	}
}

// IndexPage is responsible for indexing a crawled page in the database
func (ctx *processContext) IndexPage(pageInfo PageInfo) int64 {
	pageInfo.sourceID = ctx.source.ID
	return indexPage(*ctx.db, ctx.source.URL, pageInfo)
}

// handleConsent is responsible for handling consent windows (e.g., cookie consent)
func handleConsent(wd selenium.WebDriver) {
	cmn.DebugMsg(cmn.DbgLvlInfo, "Checking for 'consent' or 'cookie accept' windows...")
	for _, text := range append(acceptTexts, consentTexts...) {
		if clicked := findAndClickButton(wd, text); clicked {
			break
		}
	}
	cmn.DebugMsg(cmn.DbgLvlInfo, "completed.")
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
		cmn.DebugMsg(cmn.DbgLvlError, dbConnCheckErr, err)
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
		cmn.DebugMsg(cmn.DbgLvlError, "Error updating source state for URL %s: %v", sourceURL, err)
	}
}

// indexPage is responsible for indexing a crawled page in the database
// I had to write this function quickly, so it's not very efficient.
// In an ideal world, I would have used multiple transactions to index the page
// and avoid deadlocks when inserting keywords. However, using a mutex to enter
// this function (and so treat it as a critical section) should be enough for now.
// Another thought is, the mutex also helps slow down the crawling process, which
// is a good thing. You don't want to overwhelm the Source site with requests.
func indexPage(db cdb.Handler, url string, pageInfo PageInfo) int64 {
	// Acquire a lock to ensure that only one goroutine is accessing the database
	indexPageMutex.Lock()
	defer indexPageMutex.Unlock()

	pageInfo.URL = url

	// Before updating the source state, check if the database connection is still alive
	err := db.CheckConnection(config)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, dbConnCheckErr, err)
		return 0
	}

	// Start a transaction
	tx, err := db.Begin()
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error starting transaction: %v", err)
		return 0
	}

	// Insert or update the page in SearchIndex
	indexID, err := insertOrUpdateSearchIndex(tx, url, pageInfo)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error inserting or updating SearchIndex: %v", err)
		rollbackTransaction(tx)
		return 0
	}

	// Insert or update the page in WebObjects
	err = insertOrUpdateWebObjects(tx, indexID, pageInfo)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error inserting or updating WebObjects: %v", err)
		rollbackTransaction(tx)
		return 0
	}

	// Insert NetInfo into the database (if available)
	if pageInfo.NetInfo != nil {
		err = insertNetInfo(tx, indexID, pageInfo.NetInfo)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Error inserting NetInfo: %v", err)
			rollbackTransaction(tx)
			return 0
		}
	}

	// Insert HTTPInfo into the database (if available)
	if pageInfo.HTTPInfo != nil {
		err = insertHTTPInfo(tx, indexID, pageInfo.HTTPInfo)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Error inserting HTTPInfo: %v", err)
			rollbackTransaction(tx)
			return 0
		}
	}

	// Insert MetaTags
	err = insertMetaTags(tx, indexID, pageInfo.MetaTags)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error inserting meta tags: %v", err)
		rollbackTransaction(tx)
		return 0
	}

	// Insert into KeywordIndex
	err = insertKeywords(tx, db, indexID, pageInfo)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error inserting keywords: %v", err)
		rollbackTransaction(tx)
		return 0
	}

	// Commit the transaction
	err = commitTransaction(tx)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error committing transaction: %v", err)
		rollbackTransaction(tx)
		return 0
	}

	// Return the index ID
	return indexID
}

// insertOrUpdateSearchIndex inserts or updates a search index entry in the database.
// It takes a transaction object (tx), the URL of the page (url), and the page information (pageInfo).
// It returns the index ID of the inserted or updated entry and an error, if any.
func insertOrUpdateSearchIndex(tx *sql.Tx, url string, pageInfo PageInfo) (int64, error) {
	var indexID int64
	// Step 1: Insert into SearchIndex
	err := tx.QueryRow(`
		INSERT INTO SearchIndex
			(page_url, title, summary, detected_lang, detected_type, last_updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (page_url) DO UPDATE
		SET title = EXCLUDED.title, summary = EXCLUDED.summary, detected_lang = EXCLUDED.detected_lang, detected_type = EXCLUDED.detected_type, last_updated_at = NOW()
		RETURNING index_id`, url, pageInfo.Title, pageInfo.Summary, strLeft(pageInfo.DetectedLang, 8), strLeft(pageInfo.DetectedType, 8)).
		Scan(&indexID)
	if err != nil {
		return 0, err // Handle error appropriately
	}

	// Step 2: Insert into SourceSearchIndex for the associated sourceID
	_, err = tx.Exec(`
		INSERT INTO SourceSearchIndex (source_id, index_id)
		VALUES ($1, $2)
		ON CONFLICT (source_id, index_id) DO NOTHING`, pageInfo.sourceID, indexID)
	if err != nil {
		return 0, err // Handle error appropriately
	}

	return indexID, nil
}

func strLeft(s string, x int) string {
	runes := []rune(s)
	if x < 0 || x > len(runes) {
		return s
	}
	return string(runes[:x])
}

// insertOrUpdateWebObjects inserts or updates a web object entry in the database.
// It takes a transaction object (tx), the index ID of the page (indexID), and the page information (pageInfo).
// It returns an error, if any.
func insertOrUpdateWebObjects(tx *sql.Tx, indexID int64, pageInfo PageInfo) error {

	// Calculate the SHA256 hash of the body text
	hasher := sha256.New()
	hasher.Write([]byte(pageInfo.BodyText))
	hash := hex.EncodeToString(hasher.Sum(nil))

	var objID int64

	// Step 1: Insert into WebObjects
	err := tx.QueryRow(`
		INSERT INTO WebObjects (object_html, object_hash, object_content)
		VALUES ($1, $2, $3)
		ON CONFLICT (object_hash) DO UPDATE
		SET object_content = EXCLUDED.object_content
		RETURNING object_id;`, pageInfo.HTML, hash, pageInfo.BodyText).Scan(&objID)
	if err != nil {
		return err
	}

	// Step 2: Insert into WebObjectsIndex for the associated sourceID
	_, err = tx.Exec(`
		INSERT INTO PageWebObjectsIndex (index_id, object_id)
		VALUES ($1, $2)
		ON CONFLICT (index_id, object_id) DO NOTHING`, indexID, objID)
	if err != nil {
		return err
	}

	return nil
}

// insertNetInfo inserts network information into the database for a given index ID.
// It takes a transaction, index ID, and a NetInfo object as parameters.
// It returns an error if there was a problem executing the SQL statement.
func insertNetInfo(tx *sql.Tx, indexID int64, netInfo *neti.NetInfo) error {
	// encode the NetInfo object as JSON
	details, err := json.Marshal(netInfo)
	if err != nil {
		return err
	}

	// Calculate the SHA256 hash of the details
	hasher := sha256.New()
	hasher.Write(details)
	hash := hex.EncodeToString(hasher.Sum(nil))

	var netinfoID int64
	// Attempt to insert into NetInfo, or on conflict update as needed and return the netinfo_id
	err = tx.QueryRow(`
		INSERT INTO NetInfo (details_hash, details)
		VALUES ($1, $2::jsonb)
		ON CONFLICT (details_hash) DO UPDATE
		SET details = EXCLUDED.details
		RETURNING netinfo_id;
	`, hash, details).Scan(&netinfoID)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
    INSERT INTO NetInfoIndex (netinfo_id, index_id)
    VALUES ($1, $2)
    ON CONFLICT (netinfo_id, index_id) DO UPDATE
    SET last_updated_at = CURRENT_TIMESTAMP
	`, netinfoID, indexID)
	if err != nil {
		return err
	}

	return nil
}

// insertHTTPInfo inserts HTTP header information into the database for a given index ID.
// It takes a transaction, index ID, and an HTTPDetails object as parameters.
// It returns an error if there was a problem executing the SQL statement.
func insertHTTPInfo(tx *sql.Tx, indexID int64, httpInfo *httpi.HTTPDetails) error {
	// Encode the HTTPDetails object as JSON
	details, err := json.Marshal(httpInfo)
	if err != nil {
		return err
	}

	// calculate the SHA256 hash of the details
	hasher := sha256.New()
	hasher.Write(details)
	hash := hex.EncodeToString(hasher.Sum(nil))

	// Insert or update HTTPInfo and return httpinfo_id
	var httpinfoID int64
	err = tx.QueryRow(`
        INSERT INTO HTTPInfo (details_hash, details)
        VALUES ($1, $2::jsonb)
        ON CONFLICT (details_hash) DO UPDATE
        SET details = EXCLUDED.details
        RETURNING httpinfo_id;
    `, hash, details).Scan(&httpinfoID)
	if err != nil {
		return err
	}

	// Now, insert or update the HTTPInfoIndex to link the HTTPInfo entry with the indexID
	_, err = tx.Exec(`
        INSERT INTO HTTPInfoIndex (httpinfo_id, index_id)
        VALUES ($1, $2)
        ON CONFLICT (httpinfo_id, index_id) DO UPDATE
        SET last_updated_at = CURRENT_TIMESTAMP
    `, httpinfoID, indexID)
	if err != nil {
		return err
	}

	return nil
}

// insertMetaTags inserts meta tags into the database for a given index ID.
// It takes a transaction, index ID, and a map of meta tags as parameters.
// Each meta tag is inserted into the MetaTags table with the corresponding index ID, name, and content.
// Returns an error if there was a problem executing the SQL statement.
func insertMetaTags(tx *sql.Tx, indexID int64, metaTags []MetaTag) error {
	for _, metatag := range metaTags {
		name := metatag.Name
		content := metatag.Content

		var metatagID int64

		// Try to find the metatag ID first
		err := tx.QueryRow(`
            SELECT metatag_id FROM MetaTags WHERE name = $1 AND content = $2;`, name, content).Scan(&metatagID)

		// If not found, insert the new metatag and get its ID
		if err != nil {
			err = tx.QueryRow(`
                INSERT INTO MetaTags (name, content)
                VALUES ($1, $2)
                ON CONFLICT (name, content) DO UPDATE SET name = EXCLUDED.name
                RETURNING metatag_id;`, name, content).Scan(&metatagID)
			if err != nil {
				return err // Handle error appropriately
			}
		}

		// Link the metatag to the SearchIndex
		_, err = tx.Exec(`
            INSERT INTO SearchIndexMetaTags (index_id, metatag_id)
            VALUES ($1, $2)
            ON CONFLICT (index_id, metatag_id) DO NOTHING;`, indexID, metatagID)
		if err != nil {
			return err // Handle error appropriately
		}
	}
	return nil
}

// insertKeywords inserts keywords extracted from a web page into the database.
// It takes a transaction `tx` and a database connection `db` as parameters.
// The `indexID` parameter represents the ID of the index associated with the keywords.
// The `pageInfo` parameter contains information about the web page.
// It returns an error if there is any issue with inserting the keywords into the database.
func insertKeywords(tx *sql.Tx, db cdb.Handler, indexID int64, pageInfo PageInfo) error {
	for _, keyword := range extractKeywords(pageInfo) {
		keywordID, err := insertKeywordWithRetries(db, keyword)
		if err != nil {
			return err
		}
		// Use ON CONFLICT DO NOTHING to ignore the insert if the keyword_id and index_id combination already exists
		_, err = tx.Exec(`
            INSERT INTO KeywordIndex (keyword_id, index_id)
            VALUES ($1, $2)
            ON CONFLICT (keyword_id, index_id) DO NOTHING;`, keywordID, indexID)
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
		cmn.DebugMsg(cmn.DbgLvlError, "Error rolling back transaction: %v", err)
	}
}

// commitTransaction commits the given SQL transaction.
// It returns an error if the commit fails.
func commitTransaction(tx *sql.Tx) error {
	err := tx.Commit()
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error committing transaction: %v", err)
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
		cmn.DebugMsg(cmn.DbgLvlError, dbConnCheckErr, err)
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
func getURLContent(url string, wd selenium.WebDriver, level int, ctx *processContext) (selenium.WebDriver, error) {
	// Navigate to a page and interact with elements.
	err0 := wd.Get(url)
	cmd, _ := exi.ParseCmd(config.Crawler.Interval, 0)
	delayStr, _ := exi.InterpretCmd(cmd)
	delay, err := strconv.ParseFloat(delayStr, 64)
	if err != nil {
		delay = 1
	}
	if level > 0 {
		time.Sleep(time.Second * time.Duration(delay)) // Pause to let page load
	} else {
		time.Sleep(time.Second * time.Duration((delay + 5))) // Pause to let Home page load
	}

	// Run Action Rules if any
	processRules(&wd, ctx, url)

	return wd, err0
}

func processRules(wd *selenium.WebDriver, ctx *processContext, url string) {
	// Run Action Rules if any
	if ctx.source.Config != nil {
		// Execute the CROWler rules
		cmn.DebugMsg(cmn.DbgLvlDebug, "Executing CROWler rules...")
		// Execute the rules
	} else {
		// Check for rules based on the URL
		// If the URL matches a rule, execute it
		rs, err := ctx.re.GetRulesetByURL(url)
		if err == nil {
			if rs != nil {
				// Execute all the rules in the ruleset
				for _, r := range rs.GetActionRules() {
					// Execute the rule
					err := executeActionRule(&r, wd)
					if err != nil {
						cmn.DebugMsg(cmn.DbgLvlError, "Error executing action rule: %v", err)
					}
				}
			}
		}
		rg, err := ctx.re.GetRuleGroupByURL(url)
		if err == nil {
			if rg != nil {
				// Execute all the rules in the rule group
				for _, r := range rg.GetActionRules() {
					// Execute the rule
					err := executeActionRule(&r, wd)
					if err != nil {
						cmn.DebugMsg(cmn.DbgLvlError, "Error executing action rule: %v", err)
					}
				}
			}
		}
	}
}

func executeActionRule(r *rules.ActionRule, wd *selenium.WebDriver) error {
	switch strings.ToLower(strings.TrimSpace(r.ActionType)) {
	case "click":
		return executeActionClick(r, wd)
	case "scroll":
		return executeActionScroll(r, wd)
	case "input_text":
		return executeActionInput(r, wd)
	}

	return fmt.Errorf("action type not supported: %s", r.ActionType)
}

// executeActionClick is responsible for executing a "click" action
func executeActionClick(r *rules.ActionRule, wd *selenium.WebDriver) error {
	// Find the element
	wdf, _, err := findElementBySelectorType(wd, r.Selectors)
	if err != nil {
		return err
	}

	// If the element is found, click it
	if wdf != nil {
		err := wdf.Click()
		return err
	}
	return err
}

// executeActionScroll is responsible for executing a "scroll" action
func executeActionScroll(r *rules.ActionRule, wd *selenium.WebDriver) error {
	// Get Selectors list
	selectors := r.Selectors

	// Get the attribute to scroll to
	var attribute string
	if len(selectors) == 0 {
		attribute = "document.body.scrollHeight"
	} else {
		attribute = selectors[0].Attribute
	}

	// Use Sprintf to dynamically create the script string with the attribute value
	script := fmt.Sprintf("window.scrollTo(0, %s)", attribute)

	// Scroll the page
	_, err := (*wd).ExecuteScript(script, nil)
	return err
}

// executeActionInput is responsible for executing an "input" action
func executeActionInput(r *rules.ActionRule, wd *selenium.WebDriver) error {
	// Find the element
	wdf, selector, err := findElementBySelectorType(wd, r.Selectors)
	if err != nil {
		return err
	}

	// If the element is found, input the text
	if wdf != nil {
		err = wdf.SendKeys(selector.Attribute)
	}
	return err
}

// findElementBySelectorType is responsible for finding an element in the WebDriver
// using the appropriate selector type. It returns the first element found and an error.
func findElementBySelectorType(wd *selenium.WebDriver, selectors []rules.Selector) (selenium.WebElement, rules.Selector, error) {
	var wdf selenium.WebElement = nil
	var err error
	var selector rules.Selector
	for _, selector = range selectors {
		switch selector.SelectorType {
		case "css":
			wdf, err = (*wd).FindElement(selenium.ByCSSSelector, selector.Selector)
		case "xpath":
			wdf, err = (*wd).FindElement(selenium.ByXPATH, selector.Selector)
		case "id":
			wdf, err = (*wd).FindElement(selenium.ByID, selector.Selector)
		case "name":
			wdf, err = (*wd).FindElement(selenium.ByName, selector.Selector)
		case "linktext":
			wdf, err = (*wd).FindElement(selenium.ByLinkText, selector.Selector)
		case "partiallinktext":
			wdf, err = (*wd).FindElement(selenium.ByPartialLinkText, selector.Selector)
		case "tagname":
			wdf, err = (*wd).FindElement(selenium.ByTagName, selector.Selector)
		case "class":
			wdf, err = (*wd).FindElement(selenium.ByClassName, selector.Selector)
		}
		if err == nil && wdf != nil {
			break
		}
	}

	return wdf, selector, err
}

// extractPageInfo is responsible for extracting information from a collected page.
// In the future we may want to expand this function to extract more information
// from the page, such as images, videos, etc. and do a better job at screen scraping.
func extractPageInfo(webPage selenium.WebDriver, ctx *processContext) PageInfo {
	htmlContent, _ := webPage.PageSource()
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error loading HTML content: %v", err)
		return PageInfo{} // Return an empty struct in case of an error
	}

	// Run scraping rules if any
	if ctx.source.Config != nil {
		// Execute the CROWler rules
		cmn.DebugMsg(cmn.DbgLvlDebug, "Executing CROWler rules...")
		/*
			PageInfo.ScrapedData, err = rules.ExecuteScrapingRules(ctx.source.Config, ctx.wd)
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "Error executing CROWler rules: %v", err)
			}
		*/
	}

	title, _ := webPage.Title()
	summary := doc.Find("meta[name=description]").AttrOr("content", "")
	bodyText := doc.Find("body").Text()
	// transform tabs into spaces
	bodyText = strings.Replace(bodyText, "\t", " ", -1)
	// remove excessive spaces in bodyText
	bodyText = strings.Join(strings.Fields(bodyText), " ")

	metaTags := extractMetaTags(doc)

	currentURL, _ := webPage.CurrentURL()

	return PageInfo{
		Title:        title,
		Summary:      summary,
		BodyText:     bodyText,
		HTML:         htmlContent,
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
			cmn.DebugMsg(cmn.DbgLvlError, "Error retrieving text: %v", err)
			return "unknown"
		}
		bodyText, _ := bodyHTML.Text()
		info := whatlanggo.Detect(bodyText)
		lang = whatlanggo.LangToString(info.Lang)
		if lang != "" {
			lang = convertLangStrToLangCode(lang)
		}
	}
	cmn.DebugMsg(cmn.DbgLvlDebug3, "Language detected: %s", lang)
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
			cmn.DebugMsg(cmn.DbgLvlDebug3, "Document Type: %s", docType)
			return docType
		}
	}
	return "UNKNOWN"
}

// extractMetaTags is a function that extracts meta tags from a goquery.Document.
// It iterates over each "meta" element in the document and retrieves the "name" and "content" attributes.
// The extracted meta tags are stored in a []MetaTag, where the "name" attribute is the key and the "content" attribute is the value.
// The function returns the slice of extracted meta tags.
func extractMetaTags(doc *goquery.Document) []MetaTag {
	var metaTags []MetaTag
	doc.Find("meta").Each(func(_ int, s *goquery.Selection) {
		if name, exists := s.Attr("name"); exists {
			content, _ := s.Attr("content")
			metaTags = append(metaTags, MetaTag{Name: name, Content: content})
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
		cmn.DebugMsg(cmn.DbgLvlError, "Error loading HTML content: %v", err)
	}

	var links []string
	doc.Find("a").Each(func(index int, item *goquery.Selection) {
		linkTag := item
		link, _ := linkTag.Attr("href")
		link = normalizeURL(link, 0)
		if link != "" && IsValidURL(link) {
			links = append(links, link)
		}
	})
	return links
}

// normalizeURL normalizes a URL by trimming trailing slashes and converting it to lowercase.
/* flags:
   1: Convert to lowercase
*/
func normalizeURL(url string, flags uint) string {
	// Trim spaces
	url = strings.TrimSpace(url)
	// Trim trailing slash
	url = strings.TrimRight(url, "/")
	// Convert to lowercase
	if flags&1 == 1 {
		url = strings.ToLower(url)
	}
	return url
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
func worker(processCtx *processContext, id int, jobs chan string) {
	defer processCtx.wg.Done()
	for url := range jobs {
		skip := skipURL(processCtx, id, url)
		if skip {
			cmn.DebugMsg(cmn.DbgLvlDebug2, "Worker %d: Skipping URL %s\n", id, url)
			continue
		}
		cmn.DebugMsg(cmn.DbgLvlDebug, "Worker %d: processing job %s\n", id, url)
		processJob(processCtx, id, url)
		cmn.DebugMsg(cmn.DbgLvlDebug, "Worker %d: finished job %s\n", id, url)
		if config.Crawler.Delay != "0" {
			delay := getDelay()
			time.Sleep(time.Duration(delay) * time.Second)
		}
	}
}

func skipURL(processCtx *processContext, id int, url string) bool {
	url = strings.TrimSpace(url)
	if url == "" {
		return true
	}
	if strings.HasPrefix(url, "/") {
		url, _ = combineURLs(processCtx.source.URL, url)
	}
	if processCtx.source.Restricted != 4 && isExternalLink(processCtx.source.URL, url, processCtx.source.Restricted) {
		cmn.DebugMsg(cmn.DbgLvlDebug2, "Worker %d: Skipping URL '%s' due 'external' policy.\n", id, url)
		return true
	}
	return false
}

func processJob(processCtx *processContext, id int, url string) {
	htmlContent, err := getURLContent(url, processCtx.wd, 1, processCtx)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Worker %d: Error getting HTML content for %s: %v\n", id, url, err)
		return
	}
	pageCache := extractPageInfo(htmlContent, processCtx)
	pageCache.sourceID = processCtx.source.ID
	indexPage(*processCtx.db, url, pageCache)
	extractedLinks := extractLinks(pageCache.BodyText)
	if len(extractedLinks) > 0 {
		processCtx.linksMutex.Lock()
		processCtx.newLinks = append(processCtx.newLinks, extractedLinks...)
		processCtx.linksMutex.Unlock()
	}
}

func getDelay() float64 {
	if isNumber(config.Crawler.Delay) {
		delay, err := strconv.ParseFloat(config.Crawler.Delay, 64)
		if err != nil {
			delay = 1
		}
		return delay
	}

	cmd, _ := exi.ParseCmd(config.Crawler.Delay, 0)
	delayStr, _ := exi.InterpretCmd(cmd)
	delay, err := strconv.ParseFloat(delayStr, 64)
	if err != nil {
		delay = 1
	}
	return delay
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
	cmn.DebugMsg(cmn.DbgLvlInfo, "Configuring Selenium...")
	var service *selenium.Service

	var protocol string
	if c.SSLMode == "enable" {
		protocol = "https"
	} else {
		protocol = "http"
	}

	var err error
	var retries int
	if c.UseService {
		for {
			service, err = selenium.NewSeleniumService(fmt.Sprintf(protocol+"://"+c.Host+":%d/wd/hub"), c.Port)
			if err == nil {
				cmn.DebugMsg(cmn.DbgLvlInfo, "Selenium service started successfully.")
				break
			}
			cmn.DebugMsg(cmn.DbgLvlError, "Error starting Selenium service: %v", err)
			cmn.DebugMsg(cmn.DbgLvlDebug, "Check if Selenium is running on host '%s' at port '%d' and if that host is reachable from the system that is running thecrowler engine.", c.Host, c.Port)
			cmn.DebugMsg(cmn.DbgLvlInfo, "Retrying in 5 seconds...")
			retries++
			if retries > 5 {
				cmn.DebugMsg(cmn.DbgLvlError, "Exceeded maximum retries. Exiting...")
				break
			}
			time.Sleep(5 * time.Second)
		}
	}

	if err == nil {
		cmn.DebugMsg(cmn.DbgLvlInfo, "Selenium server started successfully.")
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
		cmn.DebugMsg(cmn.DbgLvlError, "Error stopping Selenium: %v", err)
	} else {
		cmn.DebugMsg(cmn.DbgLvlInfo, "Selenium stopped successfully.")
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
		userAgent = cmn.UsrAgentStrMap[sel.Config.Type+"-desktop01"]
	} else if browseType == 1 {
		userAgent = cmn.UsrAgentStrMap[sel.Config.Type+"mobile01"]
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

	var protocol string
	if sel.Config.SSLMode == "enable" {
		protocol = "https"
	} else {
		protocol = "http"
	}

	// Connect to the WebDriver instance running remotely.
	var wd selenium.WebDriver
	var err error
	for {
		wd, err = selenium.NewRemote(caps, fmt.Sprintf(protocol+"://"+sel.Config.Host+":%d/wd/hub", sel.Config.Port))
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Error connecting to Selenium: %v", err)
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
			cmn.DebugMsg(cmn.DbgLvlError, "WebDriver session may have already ended: %v", err)
			return
		}

		// Close the WebDriver if the session is still active
		err = (*wd).Quit()
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Error closing WebDriver: %v", err)
		} else {
			cmn.DebugMsg(cmn.DbgLvlInfo, "WebDriver closed successfully.")
		}
	}
}

// TakeScreenshot is responsible for taking a screenshot of the current page
func TakeScreenshot(wd *selenium.WebDriver, filename string) (Screenshot, error) {
	ss := Screenshot{}

	// Execute JavaScript to get the viewport height and width
	windowHeight, windowWidth, err := getWindowSize(wd)
	if err != nil {
		return Screenshot{}, err
	}

	totalHeight, err := getTotalHeight(wd)
	if err != nil {
		return Screenshot{}, err
	}

	screenshots, err := captureScreenshots(wd, totalHeight, windowHeight)
	if err != nil {
		return Screenshot{}, err
	}

	finalImg, err := stitchScreenshots(screenshots, windowWidth, totalHeight)
	if err != nil {
		return Screenshot{}, err
	}

	screenshot, err := encodeImage(finalImg)
	if err != nil {
		return Screenshot{}, err
	}

	location, err := saveScreenshot(filename, screenshot)
	if err != nil {
		return Screenshot{}, err
	}

	ss.ScreenshotLink = location
	ss.Format = "png"
	ss.Width = windowWidth
	ss.Height = totalHeight
	ss.ByteSize = len(screenshot)

	return ss, nil
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
func saveScreenshot(filename string, screenshot []byte) (string, error) {
	// Check if ImageStorageAPI is set
	if config.ImageStorageAPI.Host != "" {
		// Validate the ImageStorageAPI configuration
		if err := validateImageStorageAPIConfig(config); err != nil {
			return "", err
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
			return "", errors.New("unsupported storage type")
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
func writeDataViaHTTP(filename string, data []byte, saveCfg cfg.FileStorageAPI) (string, error) {
	// Check if Host IP is allowed:
	if cmn.IsDisallowedIP(saveCfg.Host, 1) {
		return "", fmt.Errorf("host %s is not allowed", saveCfg.Host)
	}

	var protocol string
	if saveCfg.SSLMode == "enable" {
		protocol = "https"
	} else {
		protocol = "http"
	}

	// Construct the API endpoint URL
	apiURL := fmt.Sprintf(protocol+"://%s:%d/"+saveCfg.Path, saveCfg.Host, saveCfg.Port)

	// Prepare the request
	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Filename", filename)
	req.Header.Set("Authorization", "Bearer "+saveCfg.Token) // Assuming token-based auth

	// Send the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// Check for a successful response
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to save file, status code: %d", resp.StatusCode)
	}
	// Return the location of the saved file
	location := resp.Header.Get("Location")
	if location == "" {
		return "", errors.New("location header not found")
	}

	return location, nil
}

// writeToFile is responsible for writing data to a file
func writeToFile(filename string, data []byte) (string, error) {
	// Write data to a file
	err := writeDataToFile(filename, data)
	if err != nil {
		return "", err
	}

	return filename, nil
}

// writeDataToFile is responsible for writing data to a file
func writeDataToFile(filename string, data []byte) error {
	// open file using READ & WRITE permission
	file, err := os.OpenFile(filename, os.O_RDWR|os.O_CREATE, cmn.DefaultFilePerms)
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
func writeDataToToS3(filename string, data []byte, saveCfg cfg.FileStorageAPI) (string, error) {
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
		return "", err
	}

	// Create an S3 service client
	svc := s3.New(sess)

	// Upload the screenshot to the S3 bucket
	_, err = svc.PutObject(&s3.PutObjectInput{
		Bucket: aws.String(saveCfg.Path),
		Key:    aws.String(filename),
		Body:   bytes.NewReader(data),
	})
	if err != nil {
		return "", err
	}

	// Return the location of the saved file
	return fmt.Sprintf("s3://%s/%s", saveCfg.Path, filename), nil
}
