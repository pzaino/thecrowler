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
	"unicode/utf8"

	"github.com/abadojack/whatlanggo"
	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	detect "github.com/pzaino/thecrowler/pkg/detection"
	exi "github.com/pzaino/thecrowler/pkg/exprterpreter"
	httpi "github.com/pzaino/thecrowler/pkg/httpinfo"
	neti "github.com/pzaino/thecrowler/pkg/netinfo"
	rules "github.com/pzaino/thecrowler/pkg/ruleset"

	"github.com/PuerkitoBio/goquery"
	"github.com/tebeka/selenium"
	"github.com/tebeka/selenium/chrome"
	"github.com/tebeka/selenium/firefox"
	"github.com/tebeka/selenium/log"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
)

const (
	dbConnCheckErr             = "checking database connection: %v\n"
	dbConnTransErr             = "committing transaction: %v"
	selConnError               = "connecting to Selenium: %v"
	errFailedToRetrieveMetrics = "failed to retrieve navigation timing metrics: %v"
)

var (
	config cfg.Config // Configuration "object"
)

// ProcessContext is a struct that holds the context of the crawling process
// It's used to pass data between functions and goroutines and holds the
// DB index of the source page after it's indexed.
type ProcessContext struct {
	SelID            int                    // The Selenium ID
	SelInstance      SeleniumInstance       // The Selenium instance
	WG               *sync.WaitGroup        // The WaitGroup
	fpIdx            uint64                 // The index of the source page after it's indexed
	config           cfg.Config             // The configuration object (from the config package)
	db               *cdb.Handler           // The database handler
	wd               selenium.WebDriver     // The Selenium WebDriver
	linksMutex       sync.Mutex             // Mutex to protect the newLinks slice
	newLinks         []LinkItem             // The new links found during the crawling process
	source           *cdb.Source            // The source to crawl
	wg               sync.WaitGroup         // WaitGroup to wait for all page workers to finish
	wgNetInfo        sync.WaitGroup         // WaitGroup to wait for network info to finish
	sel              *chan SeleniumInstance // The Selenium instances channel
	ni               *neti.NetInfo          // The network information of the web page
	hi               *httpi.HTTPDetails     // The HTTP header information of the web page
	re               *rules.RuleEngine      // The rule engine
	getURLMutex      sync.Mutex             // Mutex to protect the getURLContent function
	visitedLinks     map[string]bool        // Map to keep track of visited links
	Status           *Status                // Status of the crawling process
	CollectedCookies map[string]interface{} // Collected cookies
}

var indexPageMutex sync.Mutex // Mutex to ensure that only one goroutine is indexing a page at a time

// GetContextID returns a unique context ID for the ProcessContext
func (ctx *ProcessContext) GetContextID() string {
	return fmt.Sprintf("%d-%d", ctx.SelID, ctx.source.ID)
}

// CrawlWebsite is responsible for crawling a website, it's the main entry point
// and it's called from the main.go when there is a Source to crawl.
func CrawlWebsite(args Pars, sel SeleniumInstance, releaseSelenium chan<- SeleniumInstance) {
	// Initialize the process context
	processCtx := NewProcessContext(args)

	// Pipeline has started
	processCtx.Status.StartTime = time.Now()
	processCtx.Status.PipelineRunning = 1
	processCtx.SelInstance = sel

	// If the URL has no HTTP(S) or FTP(S) protocol, do only NETInfo
	if !IsValidURIProtocol(args.Src.URL) {
		cmn.DebugMsg(cmn.DbgLvlInfo, "URL %s has no HTTP(S) or FTP(S) protocol, skipping crawling...", args.Src.URL)
		processCtx.GetNetInfo(args.Src.URL)
		_, err := processCtx.IndexNetInfo(1)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "indexing network information: %v", err)
			processCtx.Status.PipelineRunning = 3
		} else {
			processCtx.Status.PipelineRunning = 2
		}
		UpdateSourceState(args.DB, args.Src.URL, nil)
		processCtx.Status.EndTime = time.Now()
		releaseSelenium <- sel
		cmn.DebugMsg(cmn.DbgLvlInfo, "Finished crawling website: %s", args.Src.URL)
		return
	}

	// Initialize the Selenium instance
	var err error
	if err := processCtx.ConnectToVDI(sel); err != nil {
		UpdateSourceState(args.DB, args.Src.URL, err)
		cmn.DebugMsg(cmn.DbgLvlInfo, selConnError, err)
		processCtx.Status.EndTime = time.Now()
		processCtx.Status.PipelineRunning = 3
		releaseSelenium <- sel
		return
	}
	processCtx.Status.CrawlingRunning = 1
	defer ReturnSeleniumInstance(args.WG, processCtx, &sel, releaseSelenium)
	defer UpdateSourceState(args.DB, args.Src.URL, err)
	defer processCtx.WG.Done()

	// Crawl the initial URL and get the HTML content
	pageSource, err := processCtx.CrawlInitialURL(sel)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "crawling initial URL: %v", err)
		processCtx.Status.EndTime = time.Now()
		processCtx.Status.PipelineRunning = 3
		processCtx.Status.TotalErrors++
		ReturnSeleniumInstance(args.WG, processCtx, &sel, releaseSelenium)
		return
	}

	// Get screenshot of the page
	processCtx.TakeScreenshot(pageSource, args.Src.URL, processCtx.fpIdx)

	// Extract the HTML content and extract links
	htmlContent, err := pageSource.PageSource()
	if err != nil {
		// Return the Selenium instance to the channel
		// and update the source state in the database
		cmn.DebugMsg(cmn.DbgLvlError, "getting page source: %v", err)
		processCtx.Status.EndTime = time.Now()
		processCtx.Status.PipelineRunning = 3
		processCtx.Status.TotalErrors++
		ReturnSeleniumInstance(args.WG, processCtx, &sel, releaseSelenium)
		return
	}
	initialLinks := extractLinks(processCtx, htmlContent, args.Src.URL)

	// Refresh the page
	processCtx.RefreshSeleniumConnection(sel)

	// Get network information
	processCtx.wgNetInfo.Add(1)
	go func(ctx *ProcessContext) {
		defer ctx.wgNetInfo.Done()
		ctx.GetNetInfo(ctx.source.URL)
		_, err := ctx.IndexNetInfo(1)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "indexing network information: %v", err)
		}
	}(processCtx)

	// Get HTTP header information
	processCtx.wgNetInfo.Add(1)
	go func(ctx *ProcessContext, htmlContent string) {
		defer ctx.wgNetInfo.Done()
		ctx.GetHTTPInfo(ctx.source.URL, htmlContent)
		_, err := ctx.IndexNetInfo(2)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "indexing HTTP information: %v", err)
		}
	}(processCtx, htmlContent)

	// Crawl the website
	allLinks := initialLinks // links extracted from the initial page
	var currentDepth int
	maxDepth := checkMaxDepth(config.Crawler.MaxDepth) // set a maximum depth for crawling
	newLinksFound := len(initialLinks)
	processCtx.Status.TotalLinks = newLinksFound
	for (currentDepth < maxDepth) && (newLinksFound > 0) {
		// Create a channel to enqueue jobs
		jobs := make(chan LinkItem, len(allLinks))

		// Launch worker goroutines
		for w := 1; w <= config.Crawler.Workers-2; w++ {
			processCtx.wg.Add(1)
			go worker(processCtx, w, jobs) // crawling worker
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
			processCtx.Status.TotalLinks += newLinksFound
			allLinks = processCtx.newLinks
		} else {
			newLinksFound = 0
		}
		processCtx.newLinks = []LinkItem{} // reset newLinks
		processCtx.linksMutex.Unlock()

		// Increment the current depth
		currentDepth++
		processCtx.Status.CurrentDepth = currentDepth
		if config.Crawler.MaxDepth == 0 {
			maxDepth = currentDepth + 1
		}
	}

	// Return the Selenium instance to the channel
	ReturnSeleniumInstance(args.WG, processCtx, &sel, releaseSelenium)

	// Index the network information
	processCtx.wgNetInfo.Wait()

	// Pipeline has completed
	processCtx.Status.EndTime = time.Now()
	processCtx.Status.PipelineRunning = 2

	// Update the source state in the database
	cmn.DebugMsg(cmn.DbgLvlInfo, "Finished crawling website: %s", args.Src.URL)
}

// NewProcessContext creates a new process context
func NewProcessContext(args Pars) *ProcessContext {
	if config.IsEmpty() {
		config = *cfg.NewConfig()
	}
	newPCtx := ProcessContext{
		source: &args.Src,
		db:     &args.DB,
		sel:    args.Sel,
		config: config,
		re:     args.RE,
		SelID:  args.SelIdx,
		Status: args.Status,
		WG:     args.WG,
	}
	newPCtx.visitedLinks = make(map[string]bool)
	return &newPCtx
}

func checkMaxDepth(maxDepth int) int {
	if maxDepth == 0 {
		return 1
	}
	return maxDepth
}

func resetPageInfo(p *PageInfo) {
	p.URL = ""
	p.Title = ""
	p.HTML = ""
	p.BodyText = ""
	p.Summary = ""
	p.DetectedLang = ""
	p.DetectedType = ""
	p.PerfInfo = PerformanceLog{}
	p.MetaTags = []MetaTag{}
	p.ScrapedData = []ScrapedItem{}
	p.Links = p.Links[:0] // Reset slice without reallocating
}

// ConnectToVDI is responsible for connecting to the CROWler VDI Instance
func (ctx *ProcessContext) ConnectToVDI(sel SeleniumInstance) error {
	var err error
	ctx.wd, err = ConnectVDI(sel, 0)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, selConnError, err)
		(*ctx.sel) <- sel
		return err
	}
	cmn.DebugMsg(cmn.DbgLvlDebug1, "Connected to Selenium WebDriver successfully.")
	return nil
}

// RefreshSeleniumConnection is responsible for refreshing the Selenium connection
func (ctx *ProcessContext) RefreshSeleniumConnection(sel SeleniumInstance) {
	if err := ctx.wd.Refresh(); err != nil {
		ctx.wd, err = ConnectVDI(sel, 0)
		if err != nil {
			// Return the Selenium instance to the channel
			// and update the source state in the database
			cmn.DebugMsg(cmn.DbgLvlError, "re-connecting to Selenium: %v", err)
			(*ctx.sel) <- sel
			UpdateSourceState(*ctx.db, ctx.source.URL, err)
			return
		}
	}
}

// CrawlInitialURL is responsible for crawling the initial URL of a Source
func (ctx *ProcessContext) CrawlInitialURL(sel SeleniumInstance) (selenium.WebDriver, error) {
	cmn.DebugMsg(cmn.DbgLvlInfo, "Crawling URL: %s", ctx.source.URL)

	// Set the processCtx.GetURLMutex to protect the getURLContent function
	ctx.getURLMutex.Lock()
	defer ctx.getURLMutex.Unlock()

	// Get the initial URL
	pageSource, docType, err := getURLContent(ctx.source.URL, ctx.wd, 0, ctx)
	if err != nil {
		UpdateSourceState(*ctx.db, ctx.source.URL, err)
		return pageSource, err
	}

	// Continue with extracting page info and indexing
	pageInfo := extractPageInfo(&pageSource, ctx, docType)
	pageInfo.DetectedType = docType
	pageInfo.HTTPInfo = ctx.hi
	pageInfo.NetInfo = ctx.ni
	pageInfo.Links = extractLinks(ctx, pageInfo.HTML, ctx.source.URL)

	// Collect Navigation Timing metrics
	collectNavigationMetrics(&ctx.wd, &pageInfo)

	// Collect Page logs
	collectPageLogs(&pageSource, &pageInfo)

	// Detect technologies used on the page
	detectCtx := detect.DetectionContext{
		TargetURL:    ctx.source.URL,
		ResponseBody: nil,
		Header:       nil,
		HSSLInfo:     nil,
		WD:           &(ctx.wd),
		RE:           ctx.re,
	}
	detectedTech := detect.DetectTechnologies(&detectCtx)
	if detectedTech != nil {
		pageInfo.DetectedTech = (*detectedTech)
	}

	// Use external Detection if enabled
	if len(ctx.config.ExternalDetection) > 0 {
		pageInfo.ExtDetectionResults = UseExternalDetection(ctx, ctx.source.URL)
	}

	if !ctx.config.Crawler.CollectHTML {
		// If we don't need to collect HTML content, clear it
		pageInfo.HTML = ""
	}

	if !ctx.config.Crawler.CollectContent {
		// If we don't need to collect content, clear it
		pageInfo.BodyText = ""
	}

	// Index the page
	ctx.fpIdx, err = ctx.IndexPage(&pageInfo)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "indexing page: %v", err)
		UpdateSourceState(*ctx.db, ctx.source.URL, err)
	}
	resetPageInfo(&pageInfo) // Reset the PageInfo struct
	ctx.visitedLinks[ctx.source.URL] = true
	ctx.Status.TotalPages = 1

	// Delay before processing the next job
	if config.Crawler.Delay != "0" {
		delay := exi.GetFloat(config.Crawler.Delay)
		ctx.Status.LastDelay = delay
		time.Sleep(time.Duration(delay) * time.Second)
	}

	return pageSource, nil
}

// UseExternalDetection is responsible for using external detection services
func UseExternalDetection(ctx *ProcessContext, url string) []map[string]interface{} {
	var results []map[string]interface{}
	for _, extDet := range ctx.config.ExternalDetection {
		if extDet.Enabled {
			// Call the external detection service
			switch strings.ToLower(strings.TrimSpace(extDet.Name)) {
			case "virustotal": // VirusTotal
				vtResults := detect.ScanWithVirusTotal(extDet.APIKey, url)
				if vtResults != nil {
					results = append(results, vtResults)
				}
			case "shodan": // Shodan
				shodanResults := detect.ScanWithShodan(extDet.APIKey, url)
				if shodanResults != nil {
					results = append(results, shodanResults)
				}
			case "urlhaus": // URLScan
				uhResults := detect.ScanWithURLHaus(url)
				if uhResults != nil {
					results = append(results, uhResults)
				}
			case "hybridanalysis": // HybridAnalysis
				haResults := detect.ScanWithHybridAnalysis(extDet.APIKey, url)
				if haResults != nil {
					results = append(results, haResults)
				}
			case "alienvalut": // AlienVault
				avResults := detect.ScanWithAlienVault(extDet.APIKey, url)
				if avResults != nil {
					results = append(results, avResults)
				}
			case "phishtank": // PhishTank
				ptResults := detect.ScanWithPhishTank(extDet.APIKey, url)
				if ptResults != nil {
					results = append(results, ptResults)
				}
			case "googlesafebrowsing": // Google Safe Browsing
				gsbResults := detect.ScanWithGoogleSafeBrowsing(extDet.APIKey, url)
				if gsbResults != nil {
					results = append(results, gsbResults)
				}
			case "openphish": // OpenPhish
				ofResults := detect.ScanWithOpenPhish(extDet.APIKey, url)
				if ofResults != nil {
					results = append(results, ofResults)
				}
			case "cuckoo": // Cuckoo
				ckResults := detect.ScanWithCuckoo(extDet.APIKey, url)
				if ckResults != nil {
					results = append(results, ckResults)
				}
			case "ciscoumbrella": // Cisco Umbrella
				cuResults := detect.ScanWithCiscoUmbrella(extDet.APIKey, url)
				if cuResults != nil {
					results = append(results, cuResults)
				}
			case "threatcrowd": // ThreatCrowd
				tcResults := detect.ScanWidthThreatCrowd(url)
				if tcResults != nil {
					results = append(results, tcResults)
				}
			}
		}
	}
	return results
}

// Collects the performance metrics logs from the browser
func collectNavigationMetrics(wd *selenium.WebDriver, pageInfo *PageInfo) {
	// Retrieve Navigation Timing metrics
	navigationTimingScript := `
		var timing = window.performance.timing;
		var metrics = {
			"dns_lookup": timing.domainLookupEnd - timing.domainLookupStart,
			"tcp_connection": timing.connectEnd - timing.connectStart,
			"time_to_first_byte": timing.responseStart - timing.requestStart,
			"content_load": timing.domContentLoadedEventEnd - timing.navigationStart,
			"page_load": timing.loadEventEnd - timing.navigationStart
		};
		return metrics;
	`

	// Execute JavaScript and retrieve metrics
	result, err := (*wd).ExecuteScript(navigationTimingScript, nil)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "executing script: %v", err)
		return
	}

	// Convert the result to a map for easier processing
	metrics, ok := result.(map[string]interface{})
	if !ok {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to convert metrics to map[string]interface{}")
		return
	}

	// Process the metrics
	for key, value := range metrics {
		switch key {
		case "dns_lookup":
			pageInfo.PerfInfo.DNSLookup = value.(float64)
		case "tcp_connection":
			pageInfo.PerfInfo.TCPConnection = value.(float64)
		case "time_to_first_byte":
			pageInfo.PerfInfo.TimeToFirstByte = value.(float64)
		case "content_load":
			pageInfo.PerfInfo.ContentLoad = value.(float64)
		case "page_load":
			pageInfo.PerfInfo.PageLoad = value.(float64)
		}
	}
}

// Collects the page logs from the browser
func collectPageLogs(pageSource *selenium.WebDriver, pageInfo *PageInfo) {
	logs, err := (*pageSource).Log("performance")
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to retrieve performance logs: %v", err)
		return
	}

	for _, entry := range logs {
		var log PerformanceLogEntry
		err := json.Unmarshal([]byte(entry.Message), &log)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Failed to parse log entry: %v", err)
			continue
		}
		if len(log.Message.Params.ResponseInfo.URL) > 0 {
			pageInfo.PerfInfo.LogEntries = append(pageInfo.PerfInfo.LogEntries, log)
		}
	}
}

// Collects the performance metrics logs from the browser
func retrieveNavigationMetrics(wd *selenium.WebDriver) (map[string]interface{}, error) {
	// Retrieve Navigation Timing metrics
	navigationTimingScript := `
		var timing = window.performance.timing;
		var metrics = {
			"dns_lookup": timing.domainLookupEnd - timing.domainLookupStart,
			"tcp_connection": timing.connectEnd - timing.connectStart,
			"time_to_first_byte": timing.responseStart - timing.requestStart,
			"content_load": timing.domContentLoadedEventEnd - timing.navigationStart,
			"page_load": timing.loadEventEnd - timing.navigationStart
		};
		return metrics;
	`

	// Execute JavaScript and retrieve metrics
	result, err := (*wd).ExecuteScript(navigationTimingScript, nil)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "executing script: %v", err)
		return nil, err
	}

	// Convert the result to a map for easier processing
	metrics, ok := result.(map[string]interface{})
	if !ok {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to parse navigation timing metrics")
		return nil, errors.New("failed to parse navigation timing metrics")
	}

	return metrics, nil
}

// TakeScreenshot takes a screenshot of the current page and saves it to the filesystem
func (ctx *ProcessContext) TakeScreenshot(wd selenium.WebDriver, url string, indexID uint64) {
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
		sid := strconv.FormatUint(ctx.source.ID, 10)
		imageName := "s" + sid + "-" + generateUniqueName(url, "-desktop")
		ss, err := TakeScreenshot(&wd, imageName, ctx.config.Crawler.ScreenshotMaxHeight)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "taking screenshot: %v", err)
		}
		ss.IndexID = indexID
		if ss.IndexID == 0 {
			ss.IndexID = ctx.fpIdx
		}

		// Update DB SearchIndex Table with the screenshot filename
		dbx := *ctx.db
		err = insertScreenshot(dbx, ss)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "updating database with screenshot URL: %v", err)
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
func (ctx *ProcessContext) GetNetInfo(url string) {
	ctx.Status.NetInfoRunning = 1

	// Create a new NetInfo instance
	ctx.ni = &neti.NetInfo{}
	c := ctx.config.NetworkInfo
	ctx.ni.Config = &c

	// Call GetNetInfo to retrieve network information
	cmn.DebugMsg(cmn.DbgLvlInfo, "Gathering network information for %s...", ctx.source.URL)
	err := ctx.ni.GetNetInfo(ctx.source.URL)
	ctx.Status.NetInfoRunning = 2

	// Check for errors
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "GetNetInfo(%s) returned an error: %v", ctx.source.URL, err)
		ctx.Status.NetInfoRunning = 3
		return
	}
}

// GetHTTPInfo is responsible for gathering HTTP header information for a Source
func (ctx *ProcessContext) GetHTTPInfo(url string, htmlContent string) {
	ctx.Status.HTTPInfoRunning = 1
	// Create a new HTTPDetails instance
	ctx.hi = &httpi.HTTPDetails{}
	browser := ctx.config.Selenium[ctx.SelID].Type
	var err error
	c := httpi.Config{
		URL:             url,
		CustomHeader:    map[string]string{"User-Agent": cmn.UsrAgentStrMap[browser+"-desktop01"]},
		FollowRedirects: ctx.config.HTTPHeaders.FollowRedirects,
		Timeout:         ctx.config.HTTPHeaders.Timeout,
		SSLDiscovery:    ctx.config.HTTPHeaders.SSLDiscovery,
	}
	if len(ctx.config.HTTPHeaders.Proxies) > 0 {
		c.Proxies = ctx.config.HTTPHeaders.Proxies
	}

	// Call GetHTTPInfo to retrieve HTTP header information
	cmn.DebugMsg(cmn.DbgLvlInfo, "Gathering HTTP information for %s...", ctx.source.URL)
	ctx.hi, err = httpi.ExtractHTTPInfo(c, ctx.re, htmlContent)
	ctx.Status.HTTPInfoRunning = 2

	// Check for errors
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "while retrieving HTTP Information: %v", ctx.source.URL, err)
		ctx.Status.HTTPInfoRunning = 3
		return
	}
}

// IndexPage is responsible for indexing a crawled page in the database
func (ctx *ProcessContext) IndexPage(pageInfo *PageInfo) (uint64, error) {
	(*pageInfo).sourceID = ctx.source.ID
	(*pageInfo).Config = &ctx.config
	return indexPage(*ctx.db, ctx.source.URL, pageInfo)
}

// IndexNetInfo indexes the network information of a source in the database
func (ctx *ProcessContext) IndexNetInfo(flags int) (uint64, error) {
	pageInfo := PageInfo{}
	pageInfo.HTTPInfo = ctx.hi
	pageInfo.NetInfo = ctx.ni
	pageInfo.sourceID = ctx.source.ID
	return indexNetInfo(*ctx.db, ctx.source.URL, &pageInfo, flags)
}

// UpdateSourceState is responsible for updating the state of a Source in
// the database after crawling it (it does consider errors too)
func UpdateSourceState(db cdb.Handler, sourceURL string, crawlError error) {
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
		cmn.DebugMsg(cmn.DbgLvlError, "updating source state for URL %s: %v", sourceURL, err)
	}
}

// indexPage is responsible for indexing a crawled page in the database
// I had to write this function quickly, so it's not very efficient.
// In an ideal world, I would have used multiple transactions to index the page
// and avoid deadlocks when inserting keywords. However, using a mutex to enter
// this function (and so treat it as a critical section) should be enough for now.
// Another thought is, the mutex also helps slow down the crawling process, which
// is a good thing. You don't want to overwhelm the Source site with requests.
func indexPage(db cdb.Handler, url string, pageInfo *PageInfo) (uint64, error) {
	// Acquire a lock to ensure that only one goroutine is accessing the database
	indexPageMutex.Lock()
	defer indexPageMutex.Unlock()

	pageInfo.URL = url

	// Before updating the source state, check if the database connection is still alive
	err := db.CheckConnection(config)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, dbConnCheckErr, err)
		return 0, err
	}

	// Start a transaction
	tx, err := db.Begin()
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "starting transaction: %v", err)
		return 0, err
	}

	// Insert or update the page in SearchIndex
	indexID, err := insertOrUpdateSearchIndex(tx, url, pageInfo)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "inserting or updating SearchIndex: %v", err)
		rollbackTransaction(tx)
		return 0, err
	}

	// Insert or update the page in WebObjects
	err = insertOrUpdateWebObjects(tx, indexID, pageInfo)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "inserting or updating WebObjects: %v", err)
		rollbackTransaction(tx)
		return 0, err
	}

	// Insert MetaTags
	if pageInfo.Config.Crawler.CollectMetaTags {
		err = insertMetaTags(tx, indexID, pageInfo.MetaTags)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "inserting meta tags: %v", err)
			rollbackTransaction(tx)
			return 0, err
		}
	}

	// Insert into KeywordIndex
	if pageInfo.Config.Crawler.CollectKeywords {
		err = insertKeywords(tx, db, indexID, pageInfo)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "inserting keywords: %v", err)
			rollbackTransaction(tx)
			return 0, err
		}
	}

	// Commit the transaction
	err = commitTransaction(tx)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, dbConnTransErr, err)
		rollbackTransaction(tx)
		return 0, err
	}

	// Return the index ID
	return indexID, nil
}

// indexNetInfo indexes the network information of a source in the database
func indexNetInfo(db cdb.Handler, url string, pageInfo *PageInfo, flags int) (uint64, error) {
	// Acquire a lock to ensure that only one goroutine is accessing the database
	indexPageMutex.Lock()
	defer indexPageMutex.Unlock()

	pageInfo.URL = url

	// Before updating the source state, check if the database connection is still alive
	err := db.CheckConnection(config)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, dbConnCheckErr, err)
		return 0, err
	}

	// Start a transaction
	tx, err := db.Begin()
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "starting transaction: %v", err)
		return 0, err
	}

	// Insert or update the page in SearchIndex
	indexID, err := insertOrUpdateSearchIndex(tx, url, pageInfo)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "inserting or updating SearchIndex: %v", err)
		rollbackTransaction(tx)
		return 0, err
	}

	// If flags first bit is set to 1 or if flags is 0, try to insert NetInfo
	if flags == 1 || flags == 0 {
		// Insert NetInfo into the database (if available)
		if pageInfo.NetInfo != nil {
			err = insertNetInfo(tx, indexID, pageInfo.NetInfo)
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "inserting NetInfo: %v", err)
				rollbackTransaction(tx)
				return 0, err
			}
		}
	}

	// If flags second bit is set to 1 or if flags is 0, try to insert HTTPInfo
	if flags == 2 || flags == 0 {
		// Insert HTTPInfo into the database (if available)
		if pageInfo.HTTPInfo != nil {
			err = insertHTTPInfo(tx, indexID, pageInfo.HTTPInfo)
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "inserting HTTPInfo: %v", err)
				rollbackTransaction(tx)
				return 0, err
			}
		}
	}

	// Commit the transaction
	err = commitTransaction(tx)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, dbConnTransErr, err)
		rollbackTransaction(tx)
		return 0, err
	}

	// Return the index ID
	return indexID, nil
}

// insertOrUpdateSearchIndex inserts or updates a search index entry in the database.
// It takes a transaction object (tx), the URL of the page (url), and the page information (pageInfo).
// It returns the index ID of the inserted or updated entry and an error, if any.
func insertOrUpdateSearchIndex(tx *sql.Tx, url string, pageInfo *PageInfo) (uint64, error) {
	var indexID uint64 // The index ID of the page (supports very large numbers)

	// Check if detectedLang and detectedType are not empty and are valid UTF8 strings
	if !utf8.ValidString((*pageInfo).DetectedLang) {
		(*pageInfo).DetectedLang = ""
	}
	if !utf8.ValidString((*pageInfo).DetectedType) {
		(*pageInfo).DetectedType = ""
	}

	// Check if title and summary are not empty and are valid UTF8 strings
	if !utf8.ValidString((*pageInfo).Title) {
		(*pageInfo).Title = "No Title"
	}
	if !utf8.ValidString((*pageInfo).Summary) {
		(*pageInfo).Summary = ""
	}

	// Step 1: Insert into SearchIndex
	err := tx.QueryRow(`
		INSERT INTO SearchIndex
			(page_url, title, summary, detected_lang, detected_type, last_updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (page_url) DO UPDATE
		SET title = EXCLUDED.title, summary = EXCLUDED.summary, detected_lang = EXCLUDED.detected_lang, detected_type = EXCLUDED.detected_type, last_updated_at = NOW()
		RETURNING index_id`,
		url, (*pageInfo).Title, (*pageInfo).Summary,
		strLeft((*pageInfo).DetectedLang, 8), strLeft((*pageInfo).DetectedType, 8)).Scan(&indexID)
	if err != nil {
		return 0, err // Handle error appropriately
	}

	// Step 2: Insert into SourceSearchIndex for the associated sourceID
	_, err = tx.Exec(`
		INSERT INTO SourceSearchIndex (source_id, index_id)
		VALUES ($1, $2)
		ON CONFLICT (source_id, index_id) DO NOTHING`, (*pageInfo).sourceID, indexID)
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
func insertOrUpdateWebObjects(tx *sql.Tx, indexID uint64, pageInfo *PageInfo) error {
	// Prepare the "Details" field for insertion
	details := make(map[string]interface{})
	details["performance"] = (*pageInfo).PerfInfo
	links := []string{}
	for _, link := range (*pageInfo).Links {
		links = append(links, link.Link)
	}
	details["links"] = links
	details["detected_tech"] = (*pageInfo).DetectedTech

	// Create a JSON out of the details
	detailsJSON, err := json.Marshal(details)
	if err != nil {
		return err
	}
	// Print the detailsJSON
	//fmt.Println(string(detailsJSON))

	if len((*pageInfo).ScrapedData) > 0 {
		scrapedDoc1 := make(map[string]interface{})
		// Transform the scraped data into a JSON object
		for _, value := range (*pageInfo).ScrapedData {
			if value == nil {
				continue
			}
			scrapedItemJSON, err := json.Marshal(value)
			if err != nil {
				return err
			}
			doc2 := make(map[string]interface{})
			err = json.Unmarshal(scrapedItemJSON, &doc2)
			if err != nil {
				return err
			}

			// Add scrapedItemJSON to ScrapedJSON document
			mergeMaps(scrapedDoc1, doc2)
		}
		// Wrap ScrapedDoc1 in a "scraped" tag
		scrapedDoc1 = map[string]interface{}{"scraped_data": scrapedDoc1}

		// Convert the scraped data to JSON
		scrapedDataJSON, err := json.Marshal(scrapedDoc1)
		if err != nil {
			return err
		}

		// Combine the scraped data and the details
		if len(scrapedDataJSON) > 0 {
			var doc1 map[string]interface{}
			var doc2 map[string]interface{}

			err := json.Unmarshal(detailsJSON, &doc1)
			if err != nil {
				return err
			}
			err = json.Unmarshal(scrapedDataJSON, &doc2)
			if err != nil {
				return err
			}

			// Merges doc2 into doc1
			mergeMaps(doc1, doc2)

			detailsJSON, err = json.Marshal(doc1)
			if err != nil {
				return err
			}
		}
		// For debugging purposes:
		/*
			// Extract the "links" tag from detailsJSON
			var processedDetails map[string]interface{}
			err = json.Unmarshal(detailsJSON, &processedDetails)
			if err != nil {
				return err
			}
			// Print the links tag
			fmt.Printf("Processed Links: %v\n", processedDetails["links"])
			fmt.Printf("-------------------------\n")
			fmt.Printf("Received Links: %v\n", pageInfo.Links)
		*/
		//fmt.Println(string(detailsJSON))
	}

	// Calculate the SHA256 hash of the body text
	hasher := sha256.New()
	if len((*pageInfo).BodyText) > 0 {
		hasher.Write([]byte((*pageInfo).BodyText))
	} else if len((*pageInfo).HTML) > 0 {
		hasher.Write([]byte((*pageInfo).HTML))
	} else {
		hasher.Write([]byte(detailsJSON))
	}
	hash := hex.EncodeToString(hasher.Sum(nil))

	// Get HTML and text Content
	htmlContent := (*pageInfo).HTML
	textContent := (*pageInfo).BodyText

	var objID int64

	// Step 1: Insert into WebObjects
	err = tx.QueryRow(`
		INSERT INTO WebObjects (object_hash, object_content, object_html, details)
		VALUES ($1, $2, $3, $4::jsonb)
		ON CONFLICT (object_hash) DO UPDATE
		SET object_content = EXCLUDED.object_content,
	    	details = EXCLUDED.details
		RETURNING object_id;`, hash, textContent, htmlContent, detailsJSON).Scan(&objID)
	if err != nil {
		return err
	}

	// Step 2: Insert into WebObjectsIndex for the associated sourceID
	_, err = tx.Exec(`
		INSERT INTO WebObjectsIndex (index_id, object_id)
		VALUES ($1, $2)
		ON CONFLICT (index_id, object_id) DO NOTHING`, indexID, objID)
	if err != nil {
		return err
	}

	return nil
}

func mergeMaps(dst, src map[string]interface{}) {
	for key, valueSrc := range src {
		if valueDst, ok := dst[key]; ok {
			// Check if both are maps and do a recursive merge
			if mapValueSrc, okSrc := valueSrc.(map[string]interface{}); okSrc {
				if mapValueDst, okDst := valueDst.(map[string]interface{}); okDst {
					mergeMaps(mapValueDst, mapValueSrc)
					continue
				}
			}
			// Otherwise, replace the destination value with the source value
		}
		dst[key] = valueSrc
	}
}

// insertNetInfo inserts network information into the database for a given index ID.
// It takes a transaction, index ID, and a NetInfo object as parameters.
// It returns an error if there was a problem executing the SQL statement.
func insertNetInfo(tx *sql.Tx, indexID uint64, netInfo *neti.NetInfo) error {
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
func insertHTTPInfo(tx *sql.Tx, indexID uint64, httpInfo *httpi.HTTPDetails) error {
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
func insertMetaTags(tx *sql.Tx, indexID uint64, metaTags []MetaTag) error {
	for _, metatag := range metaTags {
		var name string
		if len(metatag.Name) > 256 {
			name = metatag.Name[:256]
		} else {
			name = metatag.Name
		}
		var content string
		if len(metatag.Content) > 1024 {
			content = metatag.Content[:1024]
		} else {
			content = metatag.Content
		}

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
            INSERT INTO MetaTagsIndex (index_id, metatag_id)
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
func insertKeywords(tx *sql.Tx, db cdb.Handler, indexID uint64, pageInfo *PageInfo) error {
	for _, keyword := range extractKeywords(*pageInfo) {
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
		cmn.DebugMsg(cmn.DbgLvlError, "rolling back transaction: %v", err)
	}
}

// commitTransaction commits the given SQL transaction.
// It returns an error if the commit fails.
func commitTransaction(tx *sql.Tx) error {
	err := tx.Commit()
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, dbConnTransErr, err)
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

	if len(keyword) > 256 {
		keyword = keyword[:256]
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
func getURLContent(url string, wd selenium.WebDriver, level int, ctx *ProcessContext) (selenium.WebDriver, string, error) {
	// Navigate to a page and interact with elements.
	if err := wd.Get(url); err != nil {
		if strings.Contains(strings.ToLower(strings.TrimSpace(err.Error())), "unable to find session with id") {
			// If the session is not found, create a new one
			err = ctx.ConnectToVDI((*ctx).SelInstance)
			wd = ctx.wd
			if err != nil {
				return nil, "", fmt.Errorf("failed to create a new WebDriver session: %v", err)
			}
			// Retry navigating to the page
			err := wd.Get(url)
			if err != nil {
				return nil, "", fmt.Errorf("failed to navigate to %s: %v", url, err)
			}
		} else {
			return nil, "", fmt.Errorf("failed to navigate to %s: %v", url, err)
		}
	}

	// Wait for Page to Load
	delay := exi.GetFloat(config.Crawler.Interval)
	if delay <= 0 {
		delay = 3
	}
	ctx.Status.LastWait = delay
	if level > 0 {
		time.Sleep(time.Second * time.Duration(delay)) // Pause to let page load
	} else {
		time.Sleep(time.Second * time.Duration((delay + 5))) // Pause to let Home page load
	}

	// Get the Mime Type of the page
	docType := inferDocumentType(url, &wd)
	cmn.DebugMsg(cmn.DbgLvlDebug3, "Document Type: %s", docType)

	if docTypeIsHTML(docType) {
		// Check current URL
		_, err := wd.CurrentURL()
		if err != nil {
			return wd, docType, fmt.Errorf("failed to get current URL after navigation: %v", err)
		}

		// Run Action Rules if any
		processActionRules(&wd, ctx, url)
	}

	return wd, docType, nil
}

func docTypeIsHTML(mime string) bool {
	if strings.Contains(mime, "text/") || strings.Contains(mime, "application/xhtml+xml") {
		return true
	}
	return false
}

// extractPageInfo is responsible for extracting information from a collected page.
// In the future we may want to expand this function to extract more information
// from the page, such as images, videos, etc. and do a better job at screen scraping.
func extractPageInfo(webPage *selenium.WebDriver, ctx *ProcessContext, docType string) PageInfo {
	currentURL, _ := (*webPage).CurrentURL()

	// Detect Object Type
	objType := docType
	title := currentURL
	summary := ""
	bodyText := ""
	htmlContent := ""
	metaTags := []MetaTag{}
	scrapedList := []ScrapedItem{}

	// Get the HTML content of the page
	if docTypeIsHTML(objType) {
		htmlContent, _ = (*webPage).PageSource()
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "loading HTML content: %v", err)
			return PageInfo{} // Return an empty struct in case of an error
		}

		// Run scraping rules if any
		var scrapedData string
		var url string
		url, err = (*webPage).CurrentURL()
		if err == nil {
			scrapedData = processScrapingRules(webPage, ctx, url)
		}
		if scrapedData != "" {
			// put ScrapedData into a map
			scrapedMap := make(map[string]interface{})
			err = json.Unmarshal([]byte(scrapedData), &scrapedMap)
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "unmarshalling scraped data: %v", err)
			}

			// append the map to the list
			scrapedList = append(scrapedList, scrapedMap)
			ctx.Status.TotalScraped++
		}

		title, _ = (*webPage).Title()
		// To get the summary, we extract the content of the "description" meta tag
		// if description tag is not found, we extract the content of og:description tag
		// if og:description tag is not found, we extract the content of twitter:description tag
		// if none of the above tags are found, we extract the first 200 characters of the body text
		tmp := doc.Find("meta[name=description]").AttrOr("content", "")
		if strings.TrimSpace(tmp) == "" {
			tmp = doc.Find("meta[property=og:description]").AttrOr("content", "")
		}
		if strings.TrimSpace(tmp) == "" {
			tmp = doc.Find("meta[name=twitter:description]").AttrOr("content", "")
		}
		if strings.TrimSpace(tmp) != "" {
			summary = tmp
		}

		// copy doc to avoid modifying the original
		docCopy := doc.Clone()
		// remove script tags
		docCopy.Find("script").Each(func(i int, s *goquery.Selection) {
			s.Remove()
		})
		bodyText = docCopy.Find("body").Text()
		// transform tabs into spaces
		bodyText = strings.ReplaceAll(bodyText, "\t", " ")
		// remove excessive spaces in bodyText
		bodyText = strings.Join(strings.Fields(bodyText), " ")
		if strings.TrimSpace(summary) == "" {
			// If we don't have a summary, extract the first 200 characters of the body text
			summary = bodyText
			if len(summary) > 200 {
				summary = summary[:200]
			}
		}
		// Clear docCopy
		docCopy = nil

		if ctx.config.Crawler.CollectMetaTags {
			// Extract meta tags from the document
			metaTags = extractMetaTags(doc)
		}
	} else {
		// Download the web object and store it in the database
		if err := (*webPage).Get(currentURL); err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Failed to download web object: %v", err)
		}
	}

	return PageInfo{
		Title:        title,
		Summary:      summary,
		BodyText:     bodyText,
		HTML:         htmlContent,
		MetaTags:     metaTags,
		DetectedLang: detectLang((*webPage)),
		DetectedType: objType,
		ScrapedData:  scrapedList,
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
			cmn.DebugMsg(cmn.DbgLvlError, "retrieving text: %v", err)
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
func inferDocumentType(url string, wd *selenium.WebDriver) string {
	extension := strings.TrimSpace(strings.ToLower(filepath.Ext(url)))
	if extension != "" {
		if docType, ok := docTypeMap[extension]; ok {
			return strings.ToLower(strings.TrimSpace(docType))
		}
	}
	// If the extension is not recognized, try to infer the document type from the content type
	script := `return document.contentType;`
	contentType, err := (*wd).ExecuteScript(script, nil)
	if err != nil {
		return "UNKNOWN"
	}
	return strings.ToLower(strings.TrimSpace(contentType.(string)))
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
	// Check the obvious
	u = strings.TrimSpace(u)
	if u == "" {
		return false
	}
	if u == "http://" || u == "https://" || u == "ftp://" || u == "ftps://" {
		return false
	}

	// Prepend a scheme if it's missing
	if !strings.Contains(u, "://") {
		u = "http://" + u
	}

	// Parse the URL and check for errors
	_, err := url.ParseRequestURI(u)
	return err == nil
}

// IsValidURIProtocol checks if the URI has a valid protocol.
func IsValidURIProtocol(u string) bool {
	u = strings.TrimSpace(u)
	if !strings.HasPrefix(u, "http://") &&
		!strings.HasPrefix(u, "https://") &&
		!strings.HasPrefix(u, "ftp://") &&
		!strings.HasPrefix(u, "ftps://") {
		return false
	}
	return true
}

// extractLinks extracts all the links from the given HTML content.
// It uses the goquery library to parse the HTML and find all the <a> tags.
// Each link is then added to a slice and returned.
func extractLinks(ctx *ProcessContext, htmlContent string, url string) []LinkItem {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "loading HTML content: %v", err)
	}

	// Find all the links in the document
	var links []LinkItem
	if ctx.config.Crawler.BrowsingMode == "human" || ctx.config.Crawler.BrowsingMode == "recursive" {
		doc.Find("a").Each(func(index int, item *goquery.Selection) {
			linkTag := item
			link, _ := linkTag.Attr("href")
			link = normalizeURL(link, 0)
			linkItem := LinkItem{
				PageURL:   url,
				Link:      link,
				ElementID: item.AttrOr("id", ""),
			}
			if link != "" && IsValidURL(link) {
				links = append(links, linkItem)
			}
		})
	} else {
		// Generate the link using fuzzing rules (crawling rules)
		links = generateLinks(ctx, url)
	}
	return links
}

// generateLinks generates links based on the crawling rules
// TODO: This function needs improvements
func generateLinks(ctx *ProcessContext, url string) []LinkItem {
	var links []LinkItem
	for _, rule := range ctx.re.GetAllCrawlingRules() {
		lnkSet, err := FuzzURL(url, rule)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "generating links: %v", err)
			continue
		}
		for _, lnk := range lnkSet {
			links = append(links, LinkItem{PageURL: url, Link: lnk})
		}
	}
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
// domainLevel:
// 0: Fully restricted          (only the base URL is allowed, discard everything else)
// 1: l1 domain restricted      (must contain base URL, for instance example.com/test/*)
// 2: l2 domain restricted      (must contain SLD, for instance example.com, so if base URL is example.com/test1/, example.com/test2/* is allowed)
// 3: l3 domain restricted      (must contain TLD only, for instance .com, so if base URL is example.com/test1/, example.com/test2/*, google.com/ is allowed)
// 4: No restrictions           (global crawl, aka crawl everything you find)
// Returns true if the link is external, false otherwise.
// Use domainLevel 0 if you want to restrict crawling to the base URL only.
// Use domainLevel 1 if you want to restrict crawling to the base URL and its subdirectories.
// Use domainLevel 2 if you want to restrict crawling to the base URL and its subdomains.
// Use domainLevel 3 if you want to restrict crawling to the base URL and its TLD.
// Use domainLevel 4 if you want to crawl everything.
func isExternalLink(sourceURL, linkURL string, domainLevel uint) bool {
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

	// Check if the link URL contains the source URL (if domainLevel is 1)
	if domainLevel == 1 {
		cmn.DebugMsg(cmn.DbgLvlDebug3, "Restriction level 1, Source Domain: %s, Link Domain: %s", sourceURL, linkParsed.String())
		return !strings.Contains(linkParsed.String(), sourceURL)
	}

	// Get domain parts based on domainLevel
	// This simplify handling domainLevel 2 and 3
	srcDomain, linkDomain := getDomainParts(srcDomainParts, domainLevel), getDomainParts(linkDomainParts, domainLevel)
	cmn.DebugMsg(cmn.DbgLvlDebug3, "Source Domain: %s, Link Domain: %s", srcDomain, linkDomain)

	// Compare the relevant parts of the domain
	return srcDomain != linkDomain
}

// getDomainParts extracts domain parts based on the domainLevel.
func getDomainParts(parts []string, level uint) string {
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
func worker(processCtx *ProcessContext, id int, jobs chan LinkItem) {
	defer processCtx.wg.Done()
	var skippedURLs []LinkItem

	// Loop over the jobs channel and process each job
	for url := range jobs {

		// Check if the URL should be skipped
		skip := skipURL(processCtx, id, url.Link)
		if skip {
			processCtx.Status.TotalSkipped++
			skippedURLs = append(skippedURLs, url)
			continue
		}
		if processCtx.visitedLinks[url.Link] {
			// URL already visited
			processCtx.Status.TotalDuplicates++
			cmn.DebugMsg(cmn.DbgLvlDebug2, "Worker %d: URL %s already visited\n", id, url.Link)
			continue
		}

		// Process the job
		cmn.DebugMsg(cmn.DbgLvlDebug, "Worker %d: Processing job %s\n", id, url.Link)
		var err error
		if processCtx.config.Crawler.BrowsingMode == "recursive" {
			// Recursive Mode
			urlLink := url.Link
			if strings.HasPrefix(url.Link, "/") {
				urlLink, _ = combineURLs(processCtx.source.URL, url.Link)
			}
			err = processJob(processCtx, id, urlLink, skippedURLs)
		} else if processCtx.config.Crawler.BrowsingMode == "human" {
			// Human Mode
			// Find the <a> element that contains the URL and click it
			err = clickLink(processCtx, id, url)
		}
		if err == nil {
			processCtx.Status.TotalPages++
			cmn.DebugMsg(cmn.DbgLvlDebug, "Worker %d: Finished job %s\n", id, url.Link)
		} else {
			processCtx.Status.TotalErrors++
		}

		// Clear the skipped URLs
		skippedURLs = nil

		// Delay before processing the next job
		if config.Crawler.Delay != "0" {
			delay := exi.GetFloat(config.Crawler.Delay)
			processCtx.Status.LastDelay = delay
			time.Sleep(time.Duration(delay) * time.Second)
		}
	}
}

func skipURL(processCtx *ProcessContext, id int, url string) bool {
	url = strings.TrimSpace(url)
	if url == "" {
		return true
	}
	if strings.HasPrefix(url, "/") {
		url, _ = combineURLs(processCtx.source.URL, url)
	}
	if (processCtx.source.Restricted != 4) && isExternalLink(processCtx.source.URL, url, processCtx.source.Restricted) {
		cmn.DebugMsg(cmn.DbgLvlDebug2, "Worker %d: Skipping URL '%s' due 'external' policy.\n", id, url)
		return true
	}
	return false
}

func clickLink(processCtx *ProcessContext, id int, url LinkItem) error {
	// Set getURLMutex to ensure only one goroutine is accessing the WebDriver at a time
	processCtx.getURLMutex.Lock()
	defer processCtx.getURLMutex.Unlock()

	pageURL, err := processCtx.wd.CurrentURL()
	if err != nil {
		return err
	}
	if url.PageURL != pageURL {
		// Navigate to the page if not already there
		_, _, err := getURLContent(url.PageURL, processCtx.wd, 0, processCtx)
		if err != nil {
			return err
		}
	}

	// Get docType
	docType := inferDocumentType(url.Link, &processCtx.wd)

	// find the <a> element that contains the URL
	element, err := processCtx.wd.FindElement(selenium.ByLinkText, url.Link)
	if err != nil {
		return err
	}
	// Click the element
	err = element.Click()
	if err != nil {
		return err
	}

	// Wait for Page to Load
	delay := exi.GetFloat(config.Crawler.Interval)
	if delay <= 0 {
		delay = 3
	}
	time.Sleep(time.Second * time.Duration(delay)) // Pause to let page load

	// Check current URL
	currentURL, _ := processCtx.wd.CurrentURL()
	if currentURL != url.Link {
		cmn.DebugMsg(cmn.DbgLvlError, "Worker %d: Error navigating to %s: URL mismatch\n", id, url)
		return errors.New("URL mismatch")
	}

	// Execute Action Rules
	processActionRules(&processCtx.wd, processCtx, url.Link)

	// Extract page information
	pageCache := extractPageInfo(&processCtx.wd, processCtx, docType)
	pageCache.sourceID = processCtx.source.ID
	pageCache.Links = append(pageCache.Links, extractLinks(processCtx, pageCache.HTML, url.Link)...)
	urlItem := LinkItem{
		PageURL:   url.Link,
		Link:      currentURL,
		ElementID: "",
	}
	pageCache.Links = append(pageCache.Links, urlItem)

	// Collect Navigation Timing metrics
	metrics, err := retrieveNavigationMetrics(&processCtx.wd)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, errFailedToRetrieveMetrics, err)
	} else {
		for key, value := range metrics {
			switch key {
			case "dns_lookup":
				pageCache.PerfInfo.DNSLookup = value.(float64)
			case "tcp_connection":
				pageCache.PerfInfo.TCPConnection = value.(float64)
			case "time_to_first_byte":
				pageCache.PerfInfo.TimeToFirstByte = value.(float64)
			case "content_load":
				pageCache.PerfInfo.ContentLoad = value.(float64)
			case "page_load":
				pageCache.PerfInfo.PageLoad = value.(float64)
			}
		}
	}
	// Collect Page logs
	logs, err := processCtx.wd.Log("performance")
	if err != nil {
		return err
	}

	for _, entry := range logs {
		//cmn.DebugMsg(cmn.DbgLvlDebug2, "Performance log: %s", entry.Message)
		var log PerformanceLogEntry
		err := json.Unmarshal([]byte(entry.Message), &log)
		if err != nil {
			return err
		}
		if len(log.Message.Params.ResponseInfo.URL) > 0 {
			pageCache.PerfInfo.LogEntries = append(pageCache.PerfInfo.LogEntries, log)
		}
	}

	if !processCtx.config.Crawler.CollectHTML {
		// If we don't need to collect HTML content, clear it
		pageCache.HTML = ""
	}

	if !processCtx.config.Crawler.CollectContent {
		// If we don't need to collect content, clear it
		pageCache.BodyText = ""
	}

	// Index the page
	pageCache.Config = &processCtx.config
	_, err = indexPage(*processCtx.db, url.Link, &pageCache)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Worker %d: Error indexing page %s: %v\n", id, url.Link, err)
	}
	processCtx.visitedLinks[url.Link] = true

	// Add the new links to the process context
	if len(pageCache.Links) > 0 {
		processCtx.linksMutex.Lock()
		processCtx.newLinks = append(processCtx.newLinks, pageCache.Links...)
		processCtx.linksMutex.Unlock()
	}

	return err
}

func processJob(processCtx *ProcessContext, id int, url string, skippedURLs []LinkItem) error {
	// Set getURLMutex to ensure only one goroutine is accessing the WebDriver at a time
	processCtx.getURLMutex.Lock()
	defer processCtx.getURLMutex.Unlock()

	// Get the HTML content of the page
	htmlContent, docType, err := getURLContent(url, processCtx.wd, 1, processCtx)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Worker %d: Error getting HTML content for %s: %v\n", id, url, err)
		return err
	}

	// Extract page information
	pageCache := extractPageInfo(&htmlContent, processCtx, docType)
	pageCache.sourceID = processCtx.source.ID
	pageCache.Links = append(pageCache.Links, extractLinks(processCtx, pageCache.HTML, url)...)
	pageCache.Links = append(pageCache.Links, skippedURLs...)

	// Collect Navigation Timing metrics
	collectNavigationMetrics(&processCtx.wd, &pageCache)

	// Collect Page logs
	collectPageLogs(&htmlContent, &pageCache)

	// Collect Detected Technologies
	detectCtx := detect.DetectionContext{
		TargetURL:    url,
		ResponseBody: nil,
		Header:       nil,
		HSSLInfo:     nil,
		WD:           &processCtx.wd,
		RE:           processCtx.re,
	}
	detectedTech := detect.DetectTechnologies(&detectCtx)
	if detectedTech != nil {
		pageCache.DetectedTech = *detectedTech
	}

	if !processCtx.config.Crawler.CollectHTML {
		// If we don't need to collect HTML content, clear it
		pageCache.HTML = ""
	}

	if !processCtx.config.Crawler.CollectContent {
		// If we don't need to collect content, clear it
		pageCache.BodyText = ""
	}

	pageCache.Config = &processCtx.config
	_, err = indexPage(*processCtx.db, url, &pageCache)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Worker %d: Error indexing page %s: %v\n", id, url, err)
	}
	processCtx.visitedLinks[url] = true

	// Add the new links to the process context
	if len(pageCache.Links) > 0 {
		processCtx.linksMutex.Lock()
		processCtx.newLinks = append(processCtx.newLinks, pageCache.Links...)
		processCtx.linksMutex.Unlock()
	}
	resetPageInfo(&pageCache) // Reset the PageInfo object

	return err
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

// NewSeleniumService is responsible for initializing Selenium Driver
// The commented out code could be used to initialize a local Selenium server
// instead of using only a container based one. However, I found that the container
// based Selenium server is more stable and reliable than the local one.
// and it's obviously easier to setup and more secure.
func NewSeleniumService(c cfg.Selenium) (*selenium.Service, error) {
	cmn.DebugMsg(cmn.DbgLvlInfo, "Configuring Selenium...")
	var service *selenium.Service

	if strings.TrimSpace(c.Host) == "" {
		c.Host = "selenium"
	}

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
			cmn.DebugMsg(cmn.DbgLvlError, "starting Selenium service: %v", err)
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
		cmn.DebugMsg(cmn.DbgLvlError, "stopping Selenium: %v", err)
	} else {
		cmn.DebugMsg(cmn.DbgLvlInfo, "Selenium stopped successfully.")
	}
	return err
}

// ConnectVDI is responsible for connecting to the Selenium server instance
func ConnectVDI(sel SeleniumInstance, browseType int) (selenium.WebDriver, error) {
	// Get the required browser
	browser := strings.ToLower(strings.TrimSpace(sel.Config.Type))
	if browser == "" {
		browser = "chrome"
	}

	// Connect to the WebDriver instance running locally.
	caps := selenium.Capabilities{"browserName": browser}

	// Define the user agent string for a desktop Google Chrome browser
	var userAgent string
	if browseType == 0 {
		userAgent = cmn.UsrAgentStrMap[browser+"-desktop01"]
	} else if browseType == 1 {
		userAgent = cmn.UsrAgentStrMap[browser+"-mobile01"]
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

	// Append proxy settings if available
	if sel.Config.ProxyURL != "" {
		args = append(args, "--proxy-server="+sel.Config.ProxyURL)
	}

	args = append(args, "--disable-blink-features=AutomationControlled")

	// Append logging settings if available
	args = append(args, "--enable-logging")
	args = append(args, "--v=1")

	if browser == "chrome" || browser == "chromium" {
		chromePrefs := map[string]interface{}{
			"profile.default_content_settings.popups": 0,
			"download.default_directory":              sel.Config.DownloadDir,
		}
		caps.AddChrome(chrome.Capabilities{
			Args:  args,
			W3C:   true,
			Prefs: chromePrefs,
		})
	} else if browser == "firefox" {
		firefoxCaps := map[string]interface{}{
			"browser.download.folderList":               2,
			"browser.download.dir":                      sel.Config.DownloadDir,
			"browser.helperApps.neverAsk.saveToDisk":    "application/zip",
			"browser.download.manager.showWhenStarting": false,
		}
		caps.AddFirefox(firefox.Capabilities{
			Args:  args,
			Prefs: firefoxCaps,
		})
	}

	// Enable logging
	logSel := log.Capabilities{}
	logSel["performance"] = "ALL"
	logSel["browser"] = "ALL"
	caps.AddLogging(logSel)

	var protocol string
	if sel.Config.SSLMode == "enable" {
		protocol = "https"
	} else {
		protocol = "http"
	}

	if strings.TrimSpace(sel.Config.Host) == "" {
		sel.Config.Host = "crowler_vdi"
	}

	// Connect to the WebDriver instance running remotely.
	var wd selenium.WebDriver
	var err error
	for {
		urlType := "wd/hub"
		/*
			In theory Selenium standalone should be different than Selenium Grid
			but in practice, it's not. So, we are using the same URL for both.
			if sel.Config.ServiceType == "standalone" {
				urlType = "wd"
			} else {
				urlType = "wd/hub"
			}
		*/
		wd, err = selenium.NewRemote(caps, fmt.Sprintf(protocol+"://"+sel.Config.Host+":%d/"+urlType, sel.Config.Port))
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, selConnError, err)
			time.Sleep(5 * time.Second)
		} else {
			break
		}
	}

	// Post-connection settings
	setNavigatorProperties(&wd, sel.Config.Language)

	return wd, err
}

func setNavigatorProperties(wd *selenium.WebDriver, lang string) {
	lang = strings.ToLower(strings.TrimSpace(lang))
	selectedLanguage := ""
	switch lang {
	case "en-us":
		selectedLanguage = "['en-US', 'en']"
	case "en-gb":
		selectedLanguage = "['en-GB', 'en']"
	case "fr-fr":
		selectedLanguage = "['fr-FR', 'fr']"
	case "de-de":
		selectedLanguage = "['de-DE', 'de']"
	case "es-es":
		selectedLanguage = "['es-ES', 'es']"
	case "it-it":
		selectedLanguage = "['it-IT', 'it']"
	case "pt-pt":
		selectedLanguage = "['pt-PT', 'pt']"
	case "pt-br":
		selectedLanguage = "['pt-BR', 'pt']"
	case "ja-jp":
		selectedLanguage = "['ja-JP', 'ja']"
	case "ko-kr":
		selectedLanguage = "['ko-KR', 'ko']"
	case "zh-cn":
		selectedLanguage = "['zh-CN', 'zh']"
	case "zh-tw":
		selectedLanguage = "['zh-TW', 'zh']"
	default:
		selectedLanguage = "['en-US', 'en']"
	}
	// Set the navigator properties
	scripts := []string{
		"Object.defineProperty(navigator, 'webdriver', {get: () => undefined})",
		"window.navigator.chrome = {runtime: {}}",
		"Object.defineProperty(navigator, 'languages', {get: () => " + selectedLanguage + "})",
		"Object.defineProperty(navigator, 'plugins', {get: () => [1, 2, 3, 4, 5]})",
	}
	for _, script := range scripts {
		_, err := (*wd).ExecuteScript(script, nil)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "setting navigator properties: %v", err)
		}
	}
}

// ReturnSeleniumInstance is responsible for returning the Selenium server instance
func ReturnSeleniumInstance(wg *sync.WaitGroup, pCtx *ProcessContext, sel *SeleniumInstance, releaseSelenium chan<- SeleniumInstance) {
	if (*pCtx).Status.CrawlingRunning == 1 {
		QuitSelenium((&(*pCtx).wd))
		if *(*pCtx).sel != nil {
			//*(*pCtx).sel <- (*sel)
			releaseSelenium <- (*sel)
		}
		(*pCtx).Status.CrawlingRunning = 2
		//wg.Done()
	}
}

// QuitSelenium is responsible for quitting the Selenium server instance
func QuitSelenium(wd *selenium.WebDriver) {
	// Close the WebDriver
	if wd != nil {
		// Attempt a simple operation to check if the session is still valid
		_, err := (*wd).CurrentURL()
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlDebug1, "WebDriver session may have already ended: %v", err)
			return
		}

		// Close the WebDriver if the session is still active
		err = (*wd).Quit()
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "closing WebDriver: %v", err)
		} else {
			cmn.DebugMsg(cmn.DbgLvlInfo, "WebDriver closed successfully.")
		}
	}
}

// TakeScreenshot is responsible for taking a screenshot of the current page
func TakeScreenshot(wd *selenium.WebDriver, filename string, maxHeight int) (Screenshot, error) {
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
	if maxHeight > 0 && totalHeight > maxHeight {
		totalHeight = maxHeight
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
		time.Sleep(time.Duration(config.Crawler.ScreenshotSectionWait) * time.Second) // Pause to let page load

		// Take screenshot of the current view
		screenshot, err := (*wd).Screenshot()
		if err != nil {
			// Check if the error is due to an alert
			if strings.Contains(err.Error(), "unexpected alert open") {
				// Accept the alert and retry
				err2 := (*wd).AcceptAlert()
				if err2 != nil {
					return nil, err
				}
				screenshot, err = (*wd).Screenshot()
				if err != nil {
					return nil, err
				}
			} else {
				return nil, err
			}
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
	httpClient := &http.Client{
		Transport: cmn.SafeTransport(saveCfg.Timeout, saveCfg.SSLMode),
	}
	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Filename", filename)
	req.Header.Set("Authorization", "Bearer "+saveCfg.Token) // Assuming token-based auth

	// Send the request
	resp, err := httpClient.Do(req)
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
