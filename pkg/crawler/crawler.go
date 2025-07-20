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
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"image"
	"image/png"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	gohtml "golang.org/x/net/html"

	"github.com/PuerkitoBio/goquery"
	"github.com/abadojack/whatlanggo"
	cdp "github.com/mafredri/cdp"
	"github.com/mafredri/cdp/protocol/emulation"
	"github.com/mafredri/cdp/rpcc"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	detect "github.com/pzaino/thecrowler/pkg/detection"
	exi "github.com/pzaino/thecrowler/pkg/exprterpreter"
	httpi "github.com/pzaino/thecrowler/pkg/httpinfo"
	neti "github.com/pzaino/thecrowler/pkg/netinfo"
	rules "github.com/pzaino/thecrowler/pkg/ruleset"
	vdi "github.com/pzaino/thecrowler/pkg/vdi"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
)

const (
	dbConnCheckErr             = "checking database connection: %v\n"
	dbConnTransErr             = "committing transaction: %v"
	errFailedToRetrieveMetrics = "failed to retrieve navigation timing metrics: %v"
	errCriticalError           = "[critical]"
	errWExtractingPageInfo     = "Worker %d: Error extracting page info: %v\n"
	errWorkerLog               = "Worker %d: Error indexing page %s: %v\n"

	optDNSLookup = "dns_lookup"
	optTCPConn   = "tcp_connection"
	optTTFB      = "time_to_first_byte"
	optContent   = "content_load"
	optPageLoad  = "page_load"

	optBrowsingHuman  = "human"
	optBrowsingAuto   = "auto"
	optBrowsingRecu   = "recursive"
	optBrowsingRCRecu = "right_click_recursive"
	optBrowsingMobile = "mobile"
	optCookiesOnReq   = "on_request"
)

var (
	config           cfg.Config // Configuration "object"
	allowedProtocols = strings.Split("http://,https://,ftp://,ftps://", ",")
)

var indexPageMutex sync.Mutex // Mutex to ensure that only one goroutine is indexing a page at a time

// ProcessContext is a struct that holds the context of the crawling process
// It's used to pass data between functions and goroutines and holds the
// DB index of the source page after it's indexed.
type ProcessContext struct {
	pStatus              int                    // Process status
	SelID                int                    // The Selenium ID
	SelInstance          vdi.SeleniumInstance   // The Selenium instance
	WG                   *sync.WaitGroup        // The Caller's WaitGroup
	fpIdx                uint64                 // The index of the source page after it's indexed
	config               cfg.Config             // The configuration object (from the config package)
	db                   *cdb.Handler           // The database handler
	wd                   vdi.WebDriver          // The Selenium WebDriver
	linksMutex           sync.Mutex             // Mutex to protect the newLinks slice
	newLinks             []LinkItem             // The new links found during the crawling process
	source               *cdb.Source            // The source to crawl
	srcCfg               map[string]interface{} // Will store the source Config in an Unmarshaled format
	wg                   sync.WaitGroup         // WaitGroup to wait for all page workers to finish
	wgNetInfo            sync.WaitGroup         // WaitGroup to wait for network info to finish
	sel                  *vdi.Pool              // The Selenium instances channel (sel               *chan vdi.SeleniumInstance)
	ni                   *neti.NetInfo          // The network information of the web page
	hi                   *httpi.HTTPDetails     // The HTTP header information of the web page
	re                   *rules.RuleEngine      // The rule engine
	getURLMutex          sync.Mutex             // Mutex to protect the getURLContent function
	closeSession         sync.Mutex
	visitedLinks         map[string]bool        // Map to keep track of visited links
	userURLPatterns      []string               // User-defined URL patterns
	userURLBlockPatterns []string               // User-defined URL block patterns
	Status               *Status                // Status of the crawling process
	CollectedCookies     map[string]interface{} // Collected cookies
	VDIReturned          bool                   // Flag to indicate if the VDI instance was returned
	SelClosed            bool                   // Flag to indicate if the Selenium instance was closed
	VDIOperationMutex    sync.Mutex             // Mutex to protect the VDI operations
	RefreshCrawlingTimer func()                 // Function to refresh the crawling timer
}

// GetContextID returns a unique context ID for the ProcessContext
func (ctx *ProcessContext) GetContextID() string {
	return fmt.Sprintf("%d-%d", ctx.SelID, ctx.source.ID)
}

// GetWebDriver returns the WebDriver object from the ProcessContext
func (ctx *ProcessContext) GetWebDriver() *vdi.WebDriver {
	return &ctx.wd
}

// GetConfig returns the Config object from the ProcessContext
func (ctx *ProcessContext) GetConfig() *cfg.Config {
	return &ctx.config
}

// GetVDIClosedFlag returns the VDI closed flag from the ProcessContext
func (ctx *ProcessContext) GetVDIClosedFlag() *bool {
	return &ctx.SelClosed
}

// SetVDIClosedFlag sets the VDI closed flag in the ProcessContext
func (ctx *ProcessContext) SetVDIClosedFlag(flag bool) {
	ctx.SelClosed = flag
}

// GetVDIOperationMutex returns the VDI operation mutex from the ProcessContext
func (ctx *ProcessContext) GetVDIOperationMutex() *sync.Mutex {
	return &ctx.VDIOperationMutex
}

// GetVDIReturnedFlag returns the VDI returned flag from the ProcessContext
func (ctx *ProcessContext) GetVDIReturnedFlag() *bool {
	return &ctx.VDIReturned
}

// SetVDIReturnedFlag sets the VDI returned flag in the ProcessContext
func (ctx *ProcessContext) SetVDIReturnedFlag(flag bool) {
	ctx.VDIReturned = flag
}

// GetVDIInstance returns the VDI instance from the ProcessContext
func (ctx *ProcessContext) GetVDIInstance() *vdi.SeleniumInstance {
	return &ctx.SelInstance
}

// CrawlWebsite is responsible for crawling a website, it's the main entry point
// and it's called from the main.go when there is a Source to crawl.
func CrawlWebsite(args *Pars, sel vdi.SeleniumInstance, releaseVDI chan<- vdi.SeleniumInstance) {
	// Initialize the process context
	processCtx := NewProcessContext(args)

	// Pipeline has started
	processCtx.Status.StartTime = time.Now()
	processCtx.Status.PipelineRunning = 1
	processCtx.SelInstance = sel
	processCtx.CollectedCookies = make(map[string]interface{})
	processCtx.VDIReturned = false
	processCtx.RefreshCrawlingTimer = args.Refresh
	processCtx.pStatus = 1 // Processing started

	if contentTypeDetectionMap.IsEmpty() {
		cmn.DebugMsg(cmn.DbgLvlDebug, "Content type detection rules are empty, loading them...")
		// Load the content type detection rules
		err := loadContentTypeDetectionRules("./support/content_type_detection.yaml")
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "loading content type detection rules: %v", err)
		}
	}

	var err error
	// Combine default configuration with the source configuration
	if processCtx.source.Config != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug, "Custom Source configuration found, proceeding to combine it with the default one for this source...")
		processCtx.config, err = cfg.CombineConfig(processCtx.config, *processCtx.source.Config)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "combining source configuration: %v", err)
		} else {
			cmn.DebugMsg(cmn.DbgLvlDebug, "Source configuration combined successfully.")
		}
	}

	// Log the crawling process
	cmn.DebugMsg(cmn.DbgLvlDebug5, "Crawling using: %s", processCtx.config.Crawler.BrowsingMode)

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
		cmn.DebugMsg(cmn.DbgLvlInfo, "Finished crawling website: %s", args.Src.URL)
		closeSession(processCtx, args, &sel, releaseVDI, err)
		return
	}

	// Initialize the Selenium instance
	if err = processCtx.ConnectToVDI(sel); err != nil {
		UpdateSourceState(args.DB, args.Src.URL, err)
		processCtx.Status.EndTime = time.Now()
		processCtx.Status.PipelineRunning = 3
		processCtx.Status.TotalErrors++
		processCtx.Status.LastError = err.Error()
		cmn.DebugMsg(cmn.DbgLvlError, vdi.VDIConnError, err)
		closeSession(processCtx, args, &sel, releaseVDI, err)
		return
	}
	processCtx.Status.CrawlingRunning = 1
	defer closeSession(processCtx, args, &sel, releaseVDI, err)

	// Extract custom configuration from the source
	sourceConfig := make(map[string]interface{})
	if processCtx.source.Config != nil {
		// Unmarshal the JSON RawMessage into a map[string]interface{}
		err := json.Unmarshal(*processCtx.source.Config, &sourceConfig)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "unmarshalling source configuration: %v", err)
		}
		processCtx.srcCfg = sourceConfig
		cmn.DebugMsg(cmn.DbgLvlDebug2, "Source configuration extracted: %v", processCtx.srcCfg)
	}

	// Extract URLs patterns the user wants to include/exclude
	processCtx.userURLPatterns = make([]string, 0)
	processCtx.userURLBlockPatterns = make([]string, 0)

	// Navigate the hierarchy: execution_plan -> conditions -> url_patterns
	if executionPlanRaw, ok := sourceConfig["execution_plan"]; ok {
		if executionPlan, ok := executionPlanRaw.([]interface{}); ok {
			for _, planRaw := range executionPlan {
				if plan, ok := planRaw.(map[string]interface{}); ok {
					if conditionsRaw, ok := plan["conditions"]; ok {
						if conditions, ok := conditionsRaw.(map[string]interface{}); ok {
							// Extract the include and exclude patterns
							if urlPatternsRaw, ok := conditions["url_patterns"]; ok {
								if urlPatterns, ok := urlPatternsRaw.([]interface{}); ok {
									// Convert []interface{} to []string
									for _, pattern := range urlPatterns {
										if strPattern, ok := pattern.(string); ok {
											processCtx.userURLPatterns = append(processCtx.userURLPatterns, strPattern)
										}
									}
								}
							}
							if urlBlockPatternsRaw, ok := conditions["url_block_patterns"]; ok {
								if urlBlockPatterns, ok := urlBlockPatternsRaw.([]interface{}); ok {
									// Convert []interface{} to []string
									for _, pattern := range urlBlockPatterns {
										if strPattern, ok := pattern.(string); ok {
											processCtx.userURLBlockPatterns = append(processCtx.userURLBlockPatterns, strPattern)
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}

	var timeout time.Duration
	timeout = parseProcessingTimeout(processCtx.config.Crawler.ProcessingTimeout) - 1
	if timeout == 0 {
		timeout = 20 * time.Minute // default fallback
		timeout -= time.Second
	}

	timeoutTimer := time.NewTimer(timeout)
	defer timeoutTimer.Stop()

	done := make(chan struct{})

	// Wrap the entire crawling workflow into a goroutine
	go func() {
		defer close(done)

		// Crawl the initial URL and get the HTML content
		var pageSource vdi.WebDriver
		pageSource, err = processCtx.CrawlInitialURL(sel)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "crawling initial URL: %v", err)
			processCtx.Status.EndTime = time.Now()
			processCtx.Status.CrawlingRunning = 3
			processCtx.Status.PipelineRunning = 3
			processCtx.Status.TotalErrors++
			processCtx.Status.LastError = err.Error()
			return
		}

		// Get screenshot of the page
		processCtx.TakeScreenshot(pageSource, args.Src.URL, processCtx.fpIdx)

		// Extract the HTML content and extract links
		var htmlContent string
		htmlContent, err = pageSource.PageSource()
		if err != nil {
			// Return the Selenium instance to the channel
			// and update the source state in the database
			cmn.DebugMsg(cmn.DbgLvlError, "getting page source: %v", err)
			processCtx.Status.EndTime = time.Now()
			processCtx.Status.CrawlingRunning = 3
			processCtx.Status.PipelineRunning = 3
			processCtx.Status.TotalErrors++
			processCtx.Status.LastError = err.Error()
			return
		}
		initialLinks := extractLinks(processCtx, htmlContent, args.Src.URL)

		// Refresh the page
		err = processCtx.RefreshVDIConnection(sel)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "refreshing VDI connection: %v", err)
			processCtx.Status.EndTime = time.Now()
			processCtx.Status.CrawlingRunning = 3
			processCtx.Status.PipelineRunning = 3
			processCtx.Status.TotalErrors++
			processCtx.Status.LastError = err.Error()
			return
		}

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
		if processCtx.config.HTTPHeaders.Enabled {
			processCtx.wgNetInfo.Add(1)
			go func(ctx *ProcessContext, htmlContent string) {
				defer ctx.wgNetInfo.Done()
				ctx.GetHTTPInfo(ctx.source.URL, htmlContent)
				_, err := ctx.IndexNetInfo(2)
				if err != nil {
					cmn.DebugMsg(cmn.DbgLvlError, "indexing HTTP information: %v", err)
				}
			}(processCtx, htmlContent)
		} else {
			processCtx.Status.HTTPInfoRunning = 2
		}

		// Crawl the website
		allLinks := initialLinks // links extracted from the initial page
		var currentDepth int
		maxDepth := checkMaxDepth(processCtx.config.Crawler.MaxDepth) // set a maximum depth for crawling
		newLinksFound := len(initialLinks)
		processCtx.Status.TotalLinks = newLinksFound
		if processCtx.source.Restricted != 0 {
			// Restriction level is higher than 0, so we need to crawl the website
			for (currentDepth < maxDepth) && (newLinksFound > 0) {
				// Create a channel to enqueue jobs
				jobs := make(chan LinkItem, len(allLinks))
				// Create a channel to collect errors
				errChan := make(chan error, config.Crawler.Workers-2)

				// Launch worker goroutines
				for w := 1; w <= config.Crawler.Workers-2; w++ {
					processCtx.wg.Add(1)

					go func(w int) {
						defer processCtx.wg.Done()
						if err := worker(processCtx, w, jobs); err != nil {
							// Send any error from the worker to the error channel
							errChan <- err
						}
					}(w)
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
				close(errChan)

				// Handle any errors from workers
				for err = range errChan {
					if err != nil {
						// Log the error
						cmn.DebugMsg(cmn.DbgLvlError, "Worker error: %v", err)

						// Check if the error contains errCriticalError
						if strings.Contains(err.Error(), errCriticalError) {
							// Update source with error state
							processCtx.Status.EndTime = time.Now()
							processCtx.Status.CrawlingRunning = 3
							processCtx.Status.PipelineRunning = 3
							processCtx.Status.TotalErrors++
							processCtx.Status.LastError = err.Error()

							// Log the critical error and return to stop processing
							cmn.DebugMsg(cmn.DbgLvlError, "encountered "+errCriticalError+": %v. Stopping crawling for Source: %d", err, processCtx.source.ID)
							return
						}
					}
				}
				cmn.DebugMsg(cmn.DbgLvlDebug, "All workers finished.")

				// Prepare for the next iteration
				processCtx.linksMutex.Lock()
				if len(processCtx.newLinks) > 0 {
					// If MaxLinks is set, limit the number of new links
					if processCtx.config.Crawler.MaxLinks > 0 && ((processCtx.Status.TotalPages + len(processCtx.newLinks)) > processCtx.config.Crawler.MaxLinks) {
						linksToCrawl := processCtx.config.Crawler.MaxLinks - processCtx.Status.TotalPages
						if linksToCrawl <= 0 {
							// Remove all new links
							processCtx.newLinks = []LinkItem{}
						} else {
							processCtx.newLinks = processCtx.newLinks[:linksToCrawl]
						}
					}
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
				if processCtx.config.Crawler.MaxDepth == 0 {
					maxDepth = currentDepth + 1
				}
			}
		}

		if processCtx.config.Crawler.ResetCookiesPolicy == cmn.AlwaysStr {
			// Reset cookies after crawling
			_ = ResetSiteSession(processCtx)
		}

		// Return the Selenium instance to the channel
		processCtx.Status.CrawlingRunning = 2
		vdi.ReturnVDIInstance(args.WG, processCtx, &sel, releaseVDI)

		// Index the network information
		processCtx.wgNetInfo.Wait()

		// Pipeline has completed
		processCtx.Status.EndTime = time.Now()
		processCtx.Status.PipelineRunning = 2
	}()

	// Wait for the crawling process to finish or timeout
	select {
	case <-timeoutTimer.C:
		// Timeout hit
		cmn.DebugMsg(cmn.DbgLvlError, "Crawling timed out for source: %s", args.Src.URL)
		processCtx.Status.PipelineRunning = 3
		processCtx.Status.CrawlingRunning = 3
		processCtx.Status.LastError = "timeout during crawling"
		UpdateSourceState(args.DB, args.Src.URL, errors.New("timeout during crawling"))
		closeSession(processCtx, args, &sel, releaseVDI, errors.New("timeout"))
		return
	case <-done:
		// Crawling completed successfully
	}
}

func parseProcessingTimeout(timeoutStr string) time.Duration {
	timeoutStr = strings.TrimSpace(strings.ToLower(timeoutStr))

	// Normalize user input
	timeoutStr = strings.ReplaceAll(timeoutStr, " minutes", "m")
	timeoutStr = strings.ReplaceAll(timeoutStr, " minute", "m")
	timeoutStr = strings.ReplaceAll(timeoutStr, " min", "m")
	timeoutStr = strings.ReplaceAll(timeoutStr, " mins", "m")

	timeoutStr = strings.ReplaceAll(timeoutStr, " hours", "h")
	timeoutStr = strings.ReplaceAll(timeoutStr, " hour", "h")
	timeoutStr = strings.ReplaceAll(timeoutStr, " hrs", "h")
	timeoutStr = strings.ReplaceAll(timeoutStr, " hr", "h")

	timeoutStr = strings.ReplaceAll(timeoutStr, " seconds", "s")
	timeoutStr = strings.ReplaceAll(timeoutStr, " second", "s")
	timeoutStr = strings.ReplaceAll(timeoutStr, " secs", "s")
	timeoutStr = strings.ReplaceAll(timeoutStr, " sec", "s")

	dur, err := time.ParseDuration(timeoutStr)
	if err != nil || dur <= 0 {
		// fallback
		cmn.DebugMsg(cmn.DbgLvlWarn, "Invalid timeout format: %s, falling back to 20m", timeoutStr)
		dur = 20 * time.Minute
	}
	// Ensure the duration is not negative
	if dur < 0 {
		cmn.DebugMsg(cmn.DbgLvlWarn, "Negative timeout duration: %s, falling back to 20m", timeoutStr)
		dur = 20 * time.Minute
	}
	// Ensure the duration is not too large
	if dur > 24*time.Hour {
		cmn.DebugMsg(cmn.DbgLvlWarn, "Timeout duration too large: %s, falling back to 24h", timeoutStr)
		dur = 24 * time.Hour
	}
	// Ensure the duration is not too small
	if dur < 1*time.Second {
		cmn.DebugMsg(cmn.DbgLvlWarn, "Timeout duration too small: %s, falling back to 1s", timeoutStr)
		dur = 30 * time.Second
	}

	// Subtract 1 second for safety
	if dur > time.Second {
		dur -= time.Second
	}

	return dur
}

func closeSession(ctx *ProcessContext,
	args *Pars, sel *vdi.SeleniumInstance,
	releaseVDI chan<- vdi.SeleniumInstance,
	err error) {
	ctx.closeSession.Lock()
	if ctx.pStatus != 1 {
		return
	}

	// Release VDI connection
	// (this allows the next source to be processed, if any, in this batch job)
	vdi.ReturnVDIInstance(args.WG, ctx, sel, releaseVDI)

	// Allow a new sources batch job to be processed (if any)
	// in the caller:
	if ctx.WG != nil {
		ctx.WG.Done()
	}

	// Signal pipeline completion
	if ctx.Status.PipelineRunning == 1 || err != nil {
		ctx.Status.PipelineRunning = 3
	}
	cmn.DebugMsg(cmn.DbgLvlInfo, "Pipeline completed for source: %v", ctx.source.ID)
	ctx.Status.EndTime = time.Now()
	UpdateSourceState(args.DB, args.Src.URL, err)

	// Create a database event to indicate the crawl has completed
	if ctx.config.Crawler.CreateEventWhenDone {
		err := CreateCrawlCompletedEvent(*ctx.db, ctx.source.ID, ctx.Status)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Failed to create crawl completed event in DB: %v", err)
		}
	}

	// Optionally clean up session-specific data
	cmn.KVStore.CleanSession(ctx.GetContextID())

	// Release other resources in ctx
	ctx.linksMutex.Lock()
	ctx.newLinks = nil         // Clear the slice to release memory
	ctx.visitedLinks = nil     // Clear the map to release memory
	ctx.CollectedCookies = nil // Clear cookies
	ctx.linksMutex.Unlock()

	// Set the context object to nil
	//*ctx = ProcessContext{} // Reset the struct
	//ctx = nil               // Signal that ctx is no longer needed
	ctx.pStatus = 10 // Processing completed
	cmn.DebugMsg(cmn.DbgLvlDebug, "Returning from crawling a source.")
}

// CreateCrawlCompletedEvent creates a new event in the database to indicate that the crawl has completed
func CreateCrawlCompletedEvent(db cdb.Handler, sourceID uint64, status *Status) error {
	// Convert Status into a JSON string
	statusJSON, err := json.Marshal(status)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "marshalling status to JSON: %v", err)
	}
	var statusMap map[string]interface{}
	err = json.Unmarshal(statusJSON, &statusMap)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "unmarshalling status to map: %v", err)
	}

	// Create a new event
	event := cdb.Event{
		SourceID: sourceID,
		Type:     "crawl_completed",
		Severity: cdb.EventSeverityInfo,
		Details:  statusMap,
	}

	// Use PostgreSQL placeholders ($1, $2, etc.) and include event_timestamp
	_, err = cdb.CreateEvent(&db, event)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "inserting event into database: %v", err)
		return err
	}

	return err
}

// NewProcessContext creates a new process context
func NewProcessContext(args *Pars) *ProcessContext {
	if config.IsEmpty() {
		config = *cfg.NewConfig()
	}
	newPCtx := ProcessContext{
		source: &args.Src,
		db:     &args.DB,
		sel:    args.Sel,
		re:     args.RE,
		SelID:  args.SelIdx,
		Status: args.Status,
		WG:     args.WG,
	}
	newPCtx.config = *cfg.DeepCopyConfig(&config)
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
func (ctx *ProcessContext) ConnectToVDI(sel vdi.SeleniumInstance) error {
	var err error
	var browserType int
	if ctx.config.Crawler.Platform == optBrowsingMobile {
		browserType = 1
	}
	cmn.DebugMsg(cmn.DbgLvlDebug, "Connecting to VDI %s...", sel.Config.Name)
	ctx.wd, err = vdi.ConnectVDI(ctx, sel, browserType)
	if err != nil {
		//(*ctx.sel) <- sel
		cmn.DebugMsg(cmn.DbgLvlError, vdi.VDIConnError, err)
		return err
	}
	cmn.DebugMsg(cmn.DbgLvlDebug1, "Connected to VDI successfully.")
	return nil
}

// RefreshVDIConnection is responsible for refreshing the Selenium connection
func (ctx *ProcessContext) RefreshVDIConnection(sel vdi.SeleniumInstance) error {
	if ctx.Status.DetectedState&0x01 != 0 {
		// Stale-Processing detected, we need to abort the process
		err := errors.New("stale-processing detected")
		UpdateSourceState(*ctx.db, ctx.source.URL, err)
		cmn.DebugMsg(cmn.DbgLvlError, "Stale-Processing detected, aborting the process.")
		return err
	}
	if err := ctx.wd.Refresh(); err != nil {
		var browserType int
		if ctx.config.Crawler.Platform == optBrowsingMobile {
			browserType = 1
		}
		ctx.wd, err = vdi.ConnectVDI(ctx, sel, browserType)
		if err != nil {
			// Return the Selenium instance to the channel
			// and update the source state in the database
			UpdateSourceState(*ctx.db, ctx.source.URL, err)
			//(*ctx.sel) <- sel
			cmn.DebugMsg(cmn.DbgLvlError, "re-"+vdi.VDIConnError, err)
			return err
		}
	}
	return nil
}

// CrawlInitialURL is responsible for crawling the initial URL of a Source
func (ctx *ProcessContext) CrawlInitialURL(_ vdi.SeleniumInstance) (vdi.WebDriver, error) {
	cmn.DebugMsg(cmn.DbgLvlDebug, "Crawling Source: %d", ctx.source.ID)
	cmn.DebugMsg(cmn.DbgLvlDebug, "Crawling URL: %s", ctx.source.URL)

	// Set the processCtx.GetURLMutex to protect the getURLContent function
	ctx.getURLMutex.Lock()
	defer ctx.getURLMutex.Unlock()

	if ctx.config.Crawler.ResetCookiesPolicy == optCookiesOnReq ||
		ctx.config.Crawler.ResetCookiesPolicy == "on_start" ||
		ctx.config.Crawler.ResetCookiesPolicy == cmn.AlwaysStr {
		// Reset cookies on each request
		_ = ResetSiteSession(ctx)
	}

	// Get the initial URL
	pageSource, docType, err := getURLContent(ctx.source.URL, ctx.wd, -1, ctx)
	if err != nil {
		UpdateSourceState(*ctx.db, ctx.source.URL, err)
		return pageSource, err
	}

	// Create a new PageInfo struct
	var pageInfo PageInfo

	// Detect technologies used on the page
	detectCtx := detect.DContext{
		CtxID:        ctx.GetContextID(),
		TargetURL:    ctx.source.URL,
		ResponseBody: nil,
		Header:       nil,
		HSSLInfo:     nil,
		WD:           &(ctx.wd),
		RE:           ctx.re,
		Config:       &ctx.config,
	}
	detectedTech := detect.DetectTechnologies(&detectCtx)
	if detectedTech != nil {
		pageInfo.DetectedTech = (*detectedTech)
	}

	// Continue with extracting page info and indexing
	err = extractPageInfo(&pageSource, ctx, docType, &pageInfo)
	if err != nil {
		if strings.Contains(err.Error(), errCriticalError) {
			UpdateSourceState(*ctx.db, ctx.source.URL, err)
			cmn.DebugMsg(cmn.DbgLvlError, "extracting page info: %v", err)
			return pageSource, err
		}
	}
	pageInfo.DetectedType = docType
	pageInfo.HTTPInfo = ctx.hi
	pageInfo.NetInfo = ctx.ni
	pageInfo.Links = extractLinks(ctx, pageInfo.HTML, ctx.source.URL)
	if len(pageInfo.Links) == 0 {
		// Not links were found, so we need to check if we have any user-defined URL patterns
		srcConfig := ctx.srcCfg["crawling_config"]
		if srcConfig != nil {
			if crawlingConfig, ok := srcConfig.(map[string]interface{}); ok {
				// Check if there are any user-defined URL patterns to match
				if urlPatterns, ok := crawlingConfig["alternative_links"]; ok {
					if patterns, ok := urlPatterns.([]interface{}); ok {
						// Use the user-defined URL patterns
						for _, pattern := range patterns {
							if patternStr, ok := pattern.(string); ok {
								link := LinkItem{
									PageURL:   patternStr,
									PageLevel: 1,
									Link:      patternStr,
									ElementID: "",
								}
								// Add the user-defined link to the pageInfo.Links
								pageInfo.Links = append(pageInfo.Links, link)
								cmn.DebugMsg(cmn.DbgLvlDebug3, "[DEBUG] Added user-defined link: %s", patternStr)
							}
						}
					}
				}
			}
		}
	}
	// Generate Keywords from the page content
	pageInfo.Keywords = extractKeywords(pageInfo)

	// Collect Navigation Timing metrics
	if ctx.config.Crawler.CollectPerfMetrics {
		collectNavigationMetrics(&ctx.wd, &pageInfo)
	}

	// Collect Page logs
	if ctx.config.Crawler.CollectPageEvents {
		collectPageLogs(&pageSource, &pageInfo)
	}

	// Collect XHR
	if ctx.config.Crawler.CollectXHR {
		collectXHR(ctx, &pageInfo)
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
	fURL := cmn.NormalizeURL(ctx.source.URL)
	if ctx.visitedLinks == nil {
		ctx.visitedLinks = make(map[string]bool)
	}
	ctx.visitedLinks[fURL] = true
	ctx.Status.TotalPages = 1

	// Delay before processing the next job
	if ctx.config.Crawler.Delay != "0" {
		delay := exi.GetFloat(ctx.config.Crawler.Delay)
		ctx.Status.LastDelay = delay
		_ = vdiSleep(ctx, delay)
	}

	return pageSource, nil
}

// Collects the performance metrics logs from the browser
func collectNavigationMetrics(wd *vdi.WebDriver, pageInfo *PageInfo) {
	// Retrieve Navigation Timing metrics
	const navigationTimingScript = `
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
		case optDNSLookup:
			pageInfo.PerfInfo.DNSLookup = value.(float64)
		case optTCPConn:
			pageInfo.PerfInfo.TCPConnection = value.(float64)
		case optTTFB:
			pageInfo.PerfInfo.TimeToFirstByte = value.(float64)
		case optContent:
			pageInfo.PerfInfo.ContentLoad = value.(float64)
		case optPageLoad:
			pageInfo.PerfInfo.PageLoad = value.(float64)
		}
	}
}

// CollectXHR collects the XHR requests from the browser
func collectXHR(ctx *ProcessContext, pageInfo *PageInfo) {

	// Convert to Go structure
	xhrData, err := collectCDPRequests(ctx)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug5, "XHR Data: Invalid XHR log format: %v", xhrData)
		return
	}

	// Store data in PageInfo
	xhr := map[string]interface{}{"xhr": xhrData}
	pageInfo.ScrapedData = append(pageInfo.ScrapedData, xhr)

	// Debug output
	jsonData, _ := json.MarshalIndent(xhr, "", "  ")
	cmn.DebugMsg(cmn.DbgLvlDebug5, "XHR Data Captured: %s", jsonData)
}

// Collects the page logs from the browser
func collectPageLogs(pageSource *vdi.WebDriver, pageInfo *PageInfo) {
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
func retrieveNavigationMetrics(wd *vdi.WebDriver) (map[string]interface{}, error) {
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
func (ctx *ProcessContext) TakeScreenshot(wd vdi.WebDriver, url string, indexID uint64) {
	// Take screenshot if enabled
	takeScreenshot := false

	tmpURL1 := strings.ToLower(strings.TrimSpace(url))
	tmpURL2 := strings.ToLower(strings.TrimSpace(ctx.source.URL))

	if tmpURL1 == tmpURL2 {
		takeScreenshot = ctx.config.Crawler.SourceScreenshot
	} else {
		takeScreenshot = ctx.config.Crawler.FullSiteScreenshot
	}

	if takeScreenshot {
		// Create imageName using the hash. Adding a suffix like '.png' is optional depending on your use case.
		sid := strconv.FormatUint(ctx.source.ID, 10)
		imageName := "s" + sid + "-" + generateUniqueName(url, "-desktop")
		cmn.DebugMsg(cmn.DbgLvlDebug, "Taking screenshot: %s", imageName)
		cmn.DebugMsg(cmn.DbgLvlDebug, "Taking screenshot of %s...", url)
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
func (ctx *ProcessContext) GetNetInfo(_ string) {
	ctx.Status.NetInfoRunning = 1

	// Create a new NetInfo instance
	ctx.ni = &neti.NetInfo{}
	c := ctx.config.NetworkInfo
	ctx.ni.Config = &c

	// Call GetNetInfo to retrieve network information
	cmn.DebugMsg(cmn.DbgLvlDebug, "Gathering network information for %s...", ctx.source.URL)
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
	cmn.DebugMsg(cmn.DbgLvlInfo, "Gathering HTTP Headers information for %s...", ctx.source.URL)
	ctx.hi, err = httpi.ExtractHTTPInfo(c, ctx.re, htmlContent)
	ctx.Status.HTTPInfoRunning = 2

	// Check for errors
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "while retrieving HTTP Headers Information: %v", ctx.source.URL, err)
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

	detectedTechJSON, err := json.Marshal((*pageInfo).DetectedTech)
	if err != nil {
		detectedTechJSON = []byte{}
	}

	var scrapedDataJSON []byte
	if len((*pageInfo).ScrapedData) > 0 {
		// Prepare the "Scraped Data" field for insertion
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
		scrapedDataJSON, err = json.Marshal(scrapedDoc1)
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

	// Extract Scraped Data and Detected Tech from detailsJSON

	// Calculate the SHA256 hash of the body text
	hasher := sha256.New()
	bytesToHash := []byte{}
	if len((*pageInfo).BodyText) > 0 {
		bytesToHash = []byte((*pageInfo).BodyText)
	} else if len((*pageInfo).HTML) > 0 {
		bytesToHash = []byte((*pageInfo).HTML)
	} else {
		hasher.Write([]byte(detailsJSON))
	}
	bytesToHash = append(bytesToHash, scrapedDataJSON...)
	bytesToHash = append(bytesToHash, detectedTechJSON...)
	hasher.Write(bytesToHash)
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
	for _, keyword := range pageInfo.Keywords {
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

func addXHRHook(wd *vdi.WebDriver) error {
	if wd == nil {
		return errors.New("WebDriver is nil")
	}
	cmn.DebugMsg(cmn.DbgLvlDebug4, "[DEBUG-addXHRHook] Adding XHR Hook to WebDriver...")

	script := `
		(function() {
			if (window.__XCAP_HOOK__) return;
			window.__XCAP_HOOK__ = true;
			window.__XCAP_LOG__ = [];

			console.log("[XCAP] Hook activated.");

			// Obfuscate function names
			const _originalFetch = window.fetch;
			window.fetch = async function(..._args) {
				const [_url, _options] = _args;
				let _method = _options?.method || "GET";
				let _headers = _options?.headers || {};
				let _body = _options?.body || null;

				const _logEntry = {
					t: "fetch",
					u: _url,
					m: _method,
					h: _headers,
					b: _body
				};

				console.log("[XCAP] Fetch Request:", _logEntry);

				try {
					const _resp = await _originalFetch(..._args);
					_logEntry.s = _resp.status;
					_logEntry.rh = Object.fromEntries(_resp.headers.entries());

					window.__XCAP_LOG__.push(_logEntry);
					console.log("[XCAP] Fetch Response:", _logEntry);
					return _resp;
				} catch (_err) {
					_logEntry.err = "Error: " + _err.message;
					window.__XCAP_LOG__.push(_logEntry);
					console.log("[XCAP] Fetch Error:", _logEntry);
					throw _err;
				}
			};

			// Obfuscate XMLHttpRequest Hook
			const _originalXHR = window.XMLHttpRequest;
			window.XMLHttpRequest = function() {
				const _xhr = new _originalXHR();
				let _requestData = null;
				let _requestHeaders = {};

				_xhr.open = function(_method, _url) {
					this._u = _url;
					this._m = _method;
					_originalXHR.prototype.open.apply(this, arguments);
				};

				_xhr.setRequestHeader = function(_h, _v) {
					_requestHeaders[_h] = _v;
					_originalXHR.prototype.setRequestHeader.apply(this, arguments);
				};

				_xhr.send = function(_body) {
					_requestData = _body;
					_originalXHR.prototype.send.apply(this, arguments);
				};

				_xhr.onreadystatechange = function() {
					if (_xhr.readyState === 4) {
						const _logEntry = {
							t: "xhr",
							u: this._u,
							m: this._m,
							h: _requestHeaders,
							b: _requestData,
							s: _xhr.status
						};

						window.__XCAP_LOG__.push(_logEntry);
						console.log("[XCAP] XHR Response:", _logEntry);
					}
				};

				return _xhr;
			};
		})();
	`
	_, err := (*wd).ExecuteScript(script, nil)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to inject XHR Hook: %v", err)
	}
	return err
}

func enableCDPNetworkLogging(wd vdi.WebDriver) error {
	// Enable full network tracking (includes POST bodies)
	_, err := wd.ExecuteChromeDPCommand("Network.enable", map[string]interface{}{
		"maxPostDataSize": -1, // Ensure full request/response capture
	})
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to enable Network domain: %v", err)
		return err
	}

	// Disable caching to prevent early response disposal
	_, err = wd.ExecuteChromeDPCommand("Network.setCacheDisabled", map[string]interface{}{
		"cacheDisabled": true,
	})
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to disable cache: %v", err)
	}

	// Capture Service Worker Requests
	_, err = wd.ExecuteChromeDPCommand("ServiceWorker.enable", map[string]interface{}{})
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to enable Service Worker capture: %v", err)
	}

	// Capture ALL frames
	_, err = wd.ExecuteChromeDPCommand("Target.setAutoAttach", map[string]interface{}{
		"autoAttach":             true,
		"waitForDebuggerOnStart": false,
		"flatten":                true,
	})
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to enable iframe logging: %v", err)
	}

	// Enable Log domain
	_, err = wd.ExecuteChromeDPCommand("Log.enable", map[string]interface{}{})
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to enable Log domain: %v", err)
		return err
	}

	// Enable Page Events (for iframe tracking)
	_, err = wd.ExecuteChromeDPCommand("Page.enable", map[string]interface{}{})
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to enable Page domain: %v", err)
		return err
	}

	// Optimized delay to ensure all logging hooks are active
	time.Sleep(750 * time.Millisecond)

	cmn.DebugMsg(cmn.DbgLvlDebug5, "CDP Network logging enabled successfully.")
	return nil
}

func listenForCDPEvents(ctx context.Context, _ *ProcessContext, wd vdi.WebDriver, collectedRequests *[]map[string]interface{}) {
	for {
		select {
		case <-ctx.Done():
			// Stop listening when context is cancelled
			cmn.DebugMsg(cmn.DbgLvlDebug5, "Stopping CDP event listener.")
			return
		default:
			// Fetch CDP Events
			//events, err := wd.ExecuteChromeDPCommand("Log.entryAdded", map[string]interface{}{})
			logs, err := wd.Log("performance")
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "Failed to retrieve CDP events: %v", err)
				continue
			}

			// Process Each Event
			for _, entry := range logs {
				var logEntry map[string]interface{}
				if err := json.Unmarshal([]byte(entry.Message), &logEntry); err != nil {
					cmn.DebugMsg(cmn.DbgLvlError, "Failed to parse log entry: %v", err)
					continue
				}
				message, ok := logEntry["message"].(map[string]interface{})
				if !ok {
					continue
				}

				method, ok := message["method"].(string)
				if !ok {
					continue
				}

				switch method {
				// Capture Request Events
				case "Network.requestWillBeSent":
					params := message["params"].(map[string]interface{})
					request := params["request"].(map[string]interface{})
					requestID, _ := params["requestId"].(string)
					url, _ := request["url"].(string)
					headers, _ := request["headers"].(map[string]interface{})
					methodType, _ := request["method"].(string)
					postData, _ := request["postData"].(string)
					contentType, _ := request["mimeType"].(string)
					if contentType == "" {
						contentType, _ = headers["content-type"].(string)
					}
					postDataDecoded, detectedContentType := decodeBodyContent(wd, postData, false, url)
					if contentType == "" {
						contentType = detectedContentType
					}

					// Store Request Data
					*collectedRequests = append(*collectedRequests, map[string]interface{}{
						"object_type":          "request",
						"requestId":            requestID,
						"type":                 "http",
						"url":                  url,
						"method":               methodType,
						"headers":              headers,
						"request_body":         postDataDecoded,
						"request_content_type": contentType,
					})

				// Capture Response Metadata
				case "Network.responseReceived":
					params := message["params"].(map[string]interface{})
					response := params["response"].(map[string]interface{})
					requestID, _ := params["requestId"].(string)
					url, _ := response["url"].(string)
					headers, _ := response["headers"].(map[string]interface{})
					status, _ := response["status"].(float64)
					contentType, _ := response["mimeType"].(string)
					if contentType == "" {
						contentType, _ = headers["content-type"].(string)
					}
					postData, _ := response["body"].(string)
					decodedPostData, detectedContentType := decodeBodyContent(wd, postData, false, "")
					if contentType == "" {
						contentType = detectedContentType
					}

					// Store Response Metadata
					for i := range *collectedRequests {
						if (*collectedRequests)[i]["requestId"] == requestID {
							(*collectedRequests)[i]["url"] = url
							(*collectedRequests)[i]["status"] = status
							(*collectedRequests)[i]["response_headers"] = headers
							(*collectedRequests)[i]["response_content_type"] = contentType
							(*collectedRequests)[i]["response_body"] = decodedPostData
							break
						}
					}

				// Capture Response Body When Fully Loaded
				case "Network.loadingFinished":
					params := message["params"].(map[string]interface{})
					requestID, _ := params["requestId"].(string)

					// Fetch Response Body
					responseBody, isBase64 := fetchResponseBody(wd, requestID)
					if responseBody == "" {
						cmn.DebugMsg(cmn.DbgLvlDebug5, "⚠️ Failed to get response body for requestId %s: %v", requestID, err)
						continue
					}

					// Decode Response Body (if Base64)
					decodedBody, detectedType := decodeBodyContent(wd, responseBody, isBase64, "")

					// Store Response Body
					for i := range *collectedRequests {
						if (*collectedRequests)[i]["requestId"] == requestID {
							(*collectedRequests)[i]["response_body"] = decodedBody
							(*collectedRequests)[i]["response_type"] = detectedType
							break
						}
					}
				}
			}

			time.Sleep(250 * time.Millisecond) // Prevents 100% CPU usage
		}
	}
}

// StartCDPLogging starts CDP Logging
func StartCDPLogging(pCtx *ProcessContext) (context.CancelFunc, *[]map[string]interface{}) {
	// Store Collected Data
	var collectedRequests []map[string]interface{}

	wd := pCtx.wd

	// Create a Context with a Cancel Function
	ctx, cancel := context.WithCancel(context.Background())

	// Start Listening (Runs in a Goroutine)
	go listenForCDPEvents(ctx, pCtx, wd, &collectedRequests)

	// Return cancel function & collected data reference
	return cancel, &collectedRequests
}

func collectXHRLogs(ctx *ProcessContext, collectedResponses []map[string]interface{}) ([]map[string]interface{}, error) {
	cmn.DebugMsg(cmn.DbgLvlDebug5, "Collecting XHR logs...")
	wd := ctx.wd
	// Injected JavaScript to return the collected XHR logs
	script := "return window.__XCAP_LOG__ || [];"
	data, err := wd.ExecuteScript(script, nil)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to retrieve injected XHR logs: %v", err)
		return nil, err
	}

	if data == nil {
		cmn.DebugMsg(cmn.DbgLvlError, "XHR log data is nil.")
		return nil, errors.New("XHR log data is nil")
	}

	// Convert XHR data into Go structs
	xhrData, ok := data.([]interface{})
	if !ok {
		cmn.DebugMsg(cmn.DbgLvlError, "Invalid XHR log format: %v", data)
		return nil, errors.New("invalid XHR log format")
	}

	var matchedXHR []map[string]interface{}
	for _, entry := range xhrData {
		if logEntry, ok := entry.(map[string]interface{}); ok {
			// Extract XHR request details
			method, _ := logEntry["m"].(string)
			url, _ := logEntry["u"].(string)
			status, _ := logEntry["s"].(float64)
			if method == "" || url == "" {
				continue
			}

			// Decode Request Body
			requestBody, _ := logEntry["b"].(string)
			decodedReqBody, detectedReqType := decodeBodyContent(wd, requestBody, false, url)

			// Try to find a matching response
			matched := false
			detectedType := ""
			var decodedRespBody interface{}
			for _, resp := range collectedResponses {
				respMethod, _ := resp["method"].(string)
				respURL, _ := resp["url"].(string)
				respStatus, _ := resp["status"].(float64)
				responseBody, _ := resp["response_body"].(string)
				decodedRespBody, detectedType = decodeBodyContent(wd, responseBody, false, "")

				// Match method, status, and normalized URL
				if (method == respMethod) &&
					(status == respStatus) &&
					(cmn.NormalizeURL(url) == cmn.NormalizeURL(respURL)) {
					// Merge request with response
					logEntry["response_body"] = decodedRespBody
					logEntry["response_content_type"] = detectedType
					matched = true
					break
				}
			}

			// Reformat the request entry
			headers, _ := logEntry["h"].(map[string]interface{})
			logEntry["object_type"] = "request"
			rType, _ := logEntry["t"].(string)
			logEntry["type"] = rType
			if logEntry["t"] != nil {
				delete(logEntry, "t")
			}
			logEntry["headers"] = headers
			if logEntry["h"] != nil {
				delete(logEntry, "h")
			}
			logEntry["method"] = method
			delete(logEntry, "m")
			logEntry["url"] = url
			delete(logEntry, "u")
			logEntry["status"] = status
			delete(logEntry, "s")
			if logEntry["b"] != nil {
				delete(logEntry, "b")
			}
			logEntry["request_body"] = decodedReqBody
			logEntry["request_content_type"] = detectedReqType

			// If no response was found, add the request without response
			if !matched {
				logEntry["response_body"] = ""
				logEntry["response_content_type"] = TextEmptyType
			}

			// Append to matched XHR logs
			matchedXHR = append(matchedXHR, logEntry)
		}
	}

	// Debugging Output
	if len(matchedXHR) == 0 {
		cmn.DebugMsg(cmn.DbgLvlDebug5, "No matched XHR requests captured!")
	} else {
		cmn.DebugMsg(cmn.DbgLvlDebug5, "Matched JavaScript Fetch/XHR Requests with Responses: %v", matchedXHR)
	}
	cmn.DebugMsg(cmn.DbgLvlDebug5, "XHR logs collected successfully.")
	return matchedXHR, nil
}

// Collect All Requests
func collectCDPRequests(ctx *ProcessContext) ([]map[string]interface{}, error) {
	cmn.DebugMsg(cmn.DbgLvlDebug5, "Collecting request logs...")
	wd := ctx.wd
	logs, err := wd.Log("performance")
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to retrieve performance logs: %v", err)
		return nil, err
	}

	var collectedRequests []map[string]interface{}
	var collectedResponses []map[string]interface{}
	responseBodies := make(map[string]interface{}) // Store response metadata

	// Process logs
	for _, entry := range logs {
		var logEntry map[string]interface{}
		if err := json.Unmarshal([]byte(entry.Message), &logEntry); err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Failed to parse log entry: %v", err)
			continue
		}

		// Extract method
		message, ok := logEntry["message"].(map[string]interface{})
		if !ok {
			continue
		}
		method, ok := message["method"].(string)
		if !ok {
			continue
		}

		// Capture Requests
		if method == "Network.requestWillBeSent" {
			request := extractRequest(ctx, message)
			if request == nil {
				continue
			}
			collectedRequests = append(collectedRequests, request)
			reqID, _ := request["requestId"].(string)
			if reqID == "" {
				continue
			}
			responseBodies[reqID] = request
		}

		// Capture Responses (Metadata Only)
		if method == "Network.responseReceived" {
			storeResponseMetadata(ctx, message, responseBodies, &collectedResponses)
		}

		KeepSessionAlive(wd)
	}

	// Fetch Response Bodies & Attach Them
	collectResponses(ctx, responseBodies)

	// Collect XHR logs
	xhrLogs, err := collectXHRLogs(ctx, collectedResponses)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to collect XHR logs: %v", err)
		return collectedRequests, err
	}

	// Append XHR logs to the network logs
	collectedRequests = append(collectedRequests, xhrLogs...)
	cmn.DebugMsg(cmn.DbgLvlDebug5, "Request logs collection completed.")

	// Filter out all unwanted requests
	filteredRequests := make([]map[string]interface{}, 0)
	if len(ctx.config.Crawler.FilterXHR) == 0 {
		return collectedRequests, nil
	}
	for i := 0; i < len(collectedRequests); i++ {
		if collectedRequests[i] == nil {
			continue
		}
		rct, _ := collectedRequests[i]["request_content_type"].(string)
		rst, _ := collectedRequests[i]["response_content_type"].(string)
		rctFCheck := filterXHRRequests(ctx, rct)
		rstFCheck := filterXHRRequests(ctx, rst)
		if (rctFCheck && rstFCheck) ||
			(rct == ErrUnknownContentType && rstFCheck) ||
			(rctFCheck && rst == ErrUnknownContentType) ||
			(rct == TextEmptyType && rstFCheck) ||
			(rctFCheck && rst == TextEmptyType) {
			continue
		}
		filteredRequests = append(filteredRequests, collectedRequests[i])
	}
	cmn.DebugMsg(cmn.DbgLvlDebug5, "Filtering logs collection completed.")

	// Return all collected requests (with response bodies inside)
	return filteredRequests, nil
}

// FilterXHRRequests returns true if the detectedType is in the list of filtered XHR types
func filterXHRRequests(ctx *ProcessContext, detectedType string) bool {
	if len(ctx.config.Crawler.FilterXHR) == 0 || detectedType == "" {
		return false
	}
	// Filter XHR Responses
	for i := 0; i < len(ctx.config.Crawler.FilterXHR); i++ {
		if detectedType == strings.ToLower(strings.TrimSpace(ctx.config.Crawler.FilterXHR[i])) {
			return true
		}
	}

	KeepSessionAlive(ctx.wd)
	return false
}

// Extract Request Data
func extractRequest(ctx *ProcessContext, message map[string]interface{}) map[string]interface{} {
	params := message["params"].(map[string]interface{})
	request := params["request"].(map[string]interface{})
	requestID, _ := params["requestId"].(string)
	url, _ := request["url"].(string)
	headers, _ := request["headers"].(map[string]interface{})
	contentType, _ := headers["Content-Type"].(string)
	methodType, _ := request["method"].(string)
	postData, _ := request["postData"].(string)
	postDataDecoded, detectedType := decodeBodyContent(ctx.wd, postData, false, url)
	if contentType == "" {
		contentType = detectedType
	}

	return map[string]interface{}{
		"object_type":           "request",
		"object_content_type":   contentType,
		"requestId":             requestID,
		"type":                  "http",
		"url":                   url,
		"method":                methodType,
		"headers":               headers,
		"request_body":          postDataDecoded,
		"request_content_type":  detectedType,
		"response_body":         "",            // Placeholder for response
		"response_content_type": TextEmptyType, // Placeholder for response
	}
}

// Store Response Metadata (For Later Retrieval)
func storeResponseMetadata(ctx *ProcessContext, message map[string]interface{}, responseBodies map[string]interface{}, collectedResponses *[]map[string]interface{}) {
	params := message["params"].(map[string]interface{})
	response, _ := params["response"].(map[string]interface{})
	requestID, _ := params["requestId"].(string)
	headers, _ := response["headers"].(map[string]interface{})
	contentType, _ := headers["Content-Type"].(string)
	url, _ := response["url"].(string)
	respType, _ := response["type"].(string)
	status, _ := response["status"].(float64)
	respBody, _ := response["body"].(string)
	respBodyDecoded, detectedType := decodeBodyContent(ctx.wd, respBody, false, "")
	if contentType == "" {
		contentType = detectedType
	}

	// add to collectedResponses
	*collectedResponses = append(*collectedResponses, map[string]interface{}{
		"object_type":           "response",
		"requestId":             requestID,
		"type":                  respType,
		"url":                   url,
		"status":                status,
		"headers":               headers,
		"response_body":         respBodyDecoded,
		"response_content_type": detectedType,
	})

	// Check if we already have a request stored for this response
	if request, exists := responseBodies[requestID]; exists {
		requestMap := request.(map[string]interface{})
		requestMap["response_content_type"] = contentType
	}
}

// Fetch & Attach Response Bodies
func collectResponses(ctx *ProcessContext, responseBodies map[string]interface{}) {
	wd := ctx.wd
	// Fetch Response Bodies
	for requestID, request := range responseBodies {
		time.Sleep(100 * time.Millisecond) // Small delay

		KeepSessionAlive(wd)

		// Fetch Response Body
		body, isBase64 := fetchResponseBody(wd, requestID)

		// Decode if necessary
		decodedBody, detectedType := decodeBodyContent(wd, body, isBase64, "")

		// Store response body inside the original request
		requestMap := request.(map[string]interface{})
		requestMap["response_body"] = decodedBody
		requestMap["response_content_type"] = detectedType
	}
}

// Fetch Response Body from ChromeDP
func fetchResponseBody(wd vdi.WebDriver, requestID string) (string, bool) {
	responseBodyArgs := map[string]interface{}{
		"requestId": requestID,
	}

	// Try fetching response body (Retry if empty)
	var responseInf interface{}
	var err error
	for i := 0; i < 5; i++ { // Retry up to 3 times
		responseInf, err = wd.ExecuteChromeDPCommand("Network.getResponseBody", responseBodyArgs)
		if err == nil && responseInf != nil {
			break
		}
		time.Sleep(200 * time.Millisecond) // Wait before retrying
	}

	KeepSessionAlive(wd)

	// Convert response to map
	bodyData, ok := responseInf.(map[string]interface{})
	if !ok || bodyData["body"] == nil {
		return "", false
	}

	/*
		contentType, _ := bodyData["mimeType"].(string)
		if contentType == "" {
			contentType, _ = bodyData["content-type"].(string)
		}
	*/

	// Check if it's Base64 encoded
	bodyText, _ := bodyData["body"].(string)
	isBase64, _ := bodyData["base64Encoded"].(bool)

	return bodyText, isBase64
}

// Decode Base64 & Parse JSON Responses
func decodeBodyContent(wd vdi.WebDriver, body string, isBase64 bool, url string) (interface{}, string) {
	// Decode Base64 if needed
	if isBase64 {
		decoded, err := base64.StdEncoding.DecodeString(body)
		if err == nil {
			body = string(decoded)
		} else {
			cmn.DebugMsg(cmn.DbgLvlDebug5, "Failed to decode Base64 body: %v", err)
		}
	}

	// Create a copy of body we can manipulate
	bodyStr := body

	bodyStr = removeAntiXSSIHeaders(bodyStr)

	// Detect Content Type
	detectedContentType := detectContentType(bodyStr, url, wd)

	if detectedContentType == XMLType2 || detectedContentType == XMLType1 {
		// Attempt to parse as XML
		xmlBody, err := xmlToJSON(bodyStr)
		if err != nil {
			return body, XMLType1
		}
		// Convert XML interface{} to string
		xmlBodyStr, err := json.MarshalIndent(xmlBody, "", "  ")
		if err == nil {
			bodyStr = string(xmlBodyStr)
			if detectedContentType != XMLType1 {
				detectedContentType = XMLType1
			}
		}

		KeepSessionAlive(wd)
	}

	if detectedContentType == HTMLType {
		// attempt to convert HTML to JSON
		doc, err := gohtml.Parse(strings.NewReader(bodyStr))
		if err != nil {
			return body, detectedContentType
		}
		processedData := ExtractHTMLData(doc)
		jsonBody, _ := json.MarshalIndent(processedData, "", "  ")
		bodyStr = string(jsonBody)

		KeepSessionAlive(wd)
	}

	// Attempt to parse as JSON (even without Content-Type check)
	var jsonBody map[string]interface{}
	if err := json.Unmarshal([]byte(bodyStr), &jsonBody); err == nil {
		jsonBody = deepConvertJSONFields(jsonBody)
		if detectedContentType != HTMLType {
			detectedContentType = JSONType
		}

		KeepSessionAlive(wd)
		return jsonBody, detectedContentType
	}

	// Return raw body if not JSON
	return body, detectedContentType
}

func removeAntiXSSIHeaders(bodyStr string) string {
	// Trim whitespace
	body := strings.TrimSpace(bodyStr)
	if body == "" {
		return body
	}

	// Strip potential anti-XSSI prefixes
	body = strings.TrimPrefix(body, "for (;;);")
	body = strings.TrimPrefix(body, "while(1);")
	body = strings.TrimPrefix(body, "\"use strict\";")

	// Remove potential JSON prefix
	if strings.HasPrefix(body, "J{") {
		body = strings.TrimPrefix(body, "J")
	}

	return body
}

func deepConvertJSONFields(data map[string]interface{}) map[string]interface{} {
	for key, value := range data {
		// If the value is a string, check if it contains JSON
		if strVal, ok := value.(string); ok {
			strVal = html.UnescapeString(strVal)

			strVal = removeAntiXSSIHeaders(strVal)

			// Detect XML inside JSON fields
			if strings.HasPrefix(strVal, "<?xml") {
				xmlParsed, err := xmlToJSON(strVal)
				if err == nil {
					data[key] = xmlParsed // Store structured JSON, not a string!
					continue
				}
			}

			var nestedJSON interface{}
			if err := json.Unmarshal([]byte(strVal), &nestedJSON); err == nil {
				// If parsing succeeds, store the converted JSON structure
				data[key] = deepConvertJSON(nestedJSON)
			}
		} else if nestedMap, ok := value.(map[string]interface{}); ok {
			// Recursively process nested maps
			data[key] = deepConvertJSONFields(nestedMap)
		} else if nestedArray, ok := value.([]interface{}); ok {
			// Recursively process arrays
			for i, item := range nestedArray {
				if itemMap, ok := item.(map[string]interface{}); ok {
					nestedArray[i] = deepConvertJSONFields(itemMap)
				} else if itemStr, ok := item.(string); ok {
					var arrayJSON interface{}
					if err := json.Unmarshal([]byte(itemStr), &arrayJSON); err == nil {
						nestedArray[i] = deepConvertJSON(arrayJSON)
					}
				}
			}
			data[key] = nestedArray
		}
	}
	return data
}

// Helper function to deeply process JSON-converted values
func deepConvertJSON(value interface{}) interface{} {
	switch v := value.(type) {
	case map[string]interface{}:
		return deepConvertJSONFields(v) // Recursively process nested objects
	case []interface{}:
		for i, item := range v {
			v[i] = deepConvertJSON(item) // Recursively process array elements
		}
		return v
	default:
		return v // Return the original value if it's not JSON
	}
}

func setReferrerHeader(wd *vdi.WebDriver, ctx *ProcessContext) {
	if wd == nil || *wd == nil {
		cmn.DebugMsg(cmn.DbgLvlError, "WebDriver is nil, cannot set referrer header.")
		return
	}

	srcConfig := ctx.srcCfg["crawling_config"]
	srcConfigMap, ok := srcConfig.(map[string]interface{})
	if !ok {
		cmn.DebugMsg(cmn.DbgLvlError, "srcConfig is not a map[string]interface{}, so I cannot extract the referrer URL")
		return
	}
	cmn.DebugMsg(cmn.DbgLvlDebug2, "crawl_config: %v", srcConfig)
	referrerURLInf := srcConfigMap["url_referrer"]
	referrerURL, ok := referrerURLInf.(string)
	if !ok {
		cmn.DebugMsg(cmn.DbgLvlError, "referrerURLInf is not a string: %v", referrerURLInf)
		return
	}
	if referrerURL != "" {
		_, err := (*wd).ExecuteChromeDPCommand("Network.enable", nil)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "failed to enable Network domain: %v", err)
			return
		}
		_, err = (*wd).ExecuteChromeDPCommand("Network.setExtraHTTPHeaders", map[string]interface{}{
			"headers": map[string]interface{}{
				"referer": referrerURL,
			},
		})
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "failed to set referrer URL: %v", err)
			return
		}
	}
}

func setupBrowser(wd *vdi.WebDriver, ctx *ProcessContext) {
	if wd == nil || *wd == nil {
		cmn.DebugMsg(cmn.DbgLvlError, "WebDriver is nil, cannot setup browser.")
		return
	}

	var err error
	// Change the User Agent (if needed)
	/*
		if ctx.config.Crawler.ResetCookiesPolicy == "always" {
			err = changeUserAgent(&(ctx.wd), ctx)
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "changing UserAgent: %v", err)
			}
		}
	*/

	// Get about blank
	err = (*wd).Get("about:blank")
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "failed to load blank page: %v", err)
	}

	// Set GPU properties
	err = vdi.GPUPatch((*wd))
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to set GPU: %v", err)
	}

	// Reinforce Browser Settings
	err = vdi.ReinforceBrowserSettings(wd)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "reinforcing VDI Session settings: %v", err)
	}

	// Block URLs if any (URLs firewall)
	err = blockCDPURLs(wd, ctx)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "blocking URLs: %v", err)
	}

	if ctx.config.Crawler.ForceSFSSameOrigin {
		// We need to ensure that sec-fetch-site is set to same-origin before we get our URL
		srcConfig := ctx.srcCfg["crawling_config"]
		srcConfigMap, ok := srcConfig.(map[string]interface{})
		if !ok {
			cmn.DebugMsg(cmn.DbgLvlError, "srcConfig is not a map[string]interface{}")
		} else {
			homeURLInf := srcConfigMap["site"]
			homeURL, ok := homeURLInf.(string)
			if !ok {
				cmn.DebugMsg(cmn.DbgLvlError, "homeURLInf is not a string: %v", homeURLInf)
			} else {
				_ = (*wd).Get(homeURL)
			}
		}
	}

	// Add XHR Hook
	if ctx.config.Crawler.CollectXHR {
		err = enableCDPNetworkLogging(ctx.wd)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "adding XHR Hook: %v", err)
		}
	}
}

// getURLContent is responsible for retrieving the HTML content of a page
// from Selenium and returning it as a vdi.WebDriver object
func getURLContent(url string, wd vdi.WebDriver, level int, ctx *ProcessContext) (vdi.WebDriver, string, error) {
	// Refresh Crawler Instance work timeout
	if ctx.RefreshCrawlingTimer != nil {
		ctx.RefreshCrawlingTimer()
	}

	// Check if the URL is empty
	url = strings.TrimSpace(url)
	if url == "" {
		return nil, "", errors.New("URL is empty")
	}

	// Check if the vdi.WebDriver is still alive
	if wd == nil {
		return nil, "", errors.New("WebDriver is nil")
	}
	if ctx.VDIReturned {
		// If the VDI session is returned, return the WebDriver
		return wd, "", nil
	}

	var err error

	// Reset the Selenium session for a clean browser with new User-Agent
	if ctx.config.Crawler.ChangeUserAgent == "always" {
		err = vdi.ResetVDI(ctx, ctx.SelID) // 0 = desktop; use 1 for mobile if needed
		if ctx.RefreshCrawlingTimer != nil {
			ctx.RefreshCrawlingTimer()
		}
		if err != nil {
			return nil, "", fmt.Errorf("failed to reset VDI session: %v", err)
		}
		wd = *ctx.GetWebDriver()
		if wd == nil {
			return nil, "", errors.New("WebDriver is nil after reset")
		}
	} else {
		// check if webdriver session is still good, if not open a new one
		_, err = wd.CurrentURL()
		if err != nil {
			if strings.Contains(strings.ToLower(strings.TrimSpace(err.Error())), "invalid Session id") {
				// If the session is not found, create a new one
				err = ctx.ConnectToVDI((*ctx).SelInstance)
				wd = ctx.wd
				if err != nil {
					return nil, "", fmt.Errorf("failed to create a new WebDriver session: %v", err)
				}
			}
		}
	}

	// Setup the Browser before requesting a page
	setupBrowser(&wd, ctx)

	// Set the HTTP referer if we are on the first URL
	if level == -1 {
		setReferrerHeader(&wd, ctx)
	}

	// Navigate to a page and interact with elements.
	if ctx.RefreshCrawlingTimer != nil {
		ctx.RefreshCrawlingTimer()
	}

	// Get the page and process the interval
	if err := wd.Get(url); err != nil {
		if strings.Contains(strings.ToLower(strings.TrimSpace(err.Error())), "unable to find session with id") {
			// If the session is not found, create a new one
			err = ctx.ConnectToVDI((*ctx).SelInstance)
			wd = ctx.wd
			if err != nil {
				return nil, "", fmt.Errorf("failed to create a new WebDriver session: %v", err)
			}
			// Setup the Browser before requesting a page
			setupBrowser(&wd, ctx)
			// Set the HTTP referer if we are on the first URL
			if level == -1 {
				setReferrerHeader(&wd, ctx)
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

	// Add XHR Hook (before any action is made, but after the page has been requested)
	if ctx.config.Crawler.CollectXHR {
		err = addXHRHook(&wd)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Failed to add XHR hook: %v", err)
		}
	}

	// Wait for Page to Load
	interval := exi.GetFloat(ctx.config.Crawler.Interval)
	if interval <= 0 {
		interval = 3
	}
	ctx.Status.LastWait = interval
	if level > 0 {
		_ = vdiSleep(ctx, interval) // Pause to let page load
	} else {
		_ = vdiSleep(ctx, (interval + 5)) // Pause to let Home page load
	}

	// Get Session Cookies
	err = getCookies(ctx, &wd)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "failed to get cookies: %v", err)
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

	// Get Post-Actions Cookies (if any)
	err = getCookies(ctx, &wd)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "failed to get post-actions cookies: %v", err)
	}

	/*
		if ctx.config.Crawler.CollectXHR {
			// Stop listening for CDP events
			cancel()
			// Log all requests for debugging
			cmn.DebugMsg(cmn.DbgLvlDebug5, "Collected Events: %v", *collectedRequests)
		}
	*/

	if ctx.RefreshCrawlingTimer != nil {
		ctx.RefreshCrawlingTimer()
	}
	return wd, docType, nil
}

func blockCDPURLs(wd *vdi.WebDriver, ctx *ProcessContext) error {
	// Check if we have any blocked URLs configured
	if len((*ctx).userURLBlockPatterns) == 0 {
		return nil // No patterns to block
	}

	// Extract patterns from the configuration
	patterns := make([]string, 0)
	for _, pattern := range (*ctx).userURLBlockPatterns {
		if pattern != "" {
			patterns = append(patterns, pattern)
		}
	}

	// First: enable the Network domain
	_, err := (*wd).ExecuteChromeDPCommand("Network.enable", map[string]interface{}{})
	if err != nil {
		return fmt.Errorf("failed to enable Network domain: %w", err)
	}

	// Then: set the blocked URL patterns
	_, err = (*wd).ExecuteChromeDPCommand("Network.setBlockedURLs", map[string]interface{}{
		"urls": patterns,
	})
	if err != nil {
		return fmt.Errorf("failed to set blocked URLs: %w", err)
	}

	return nil
}

func changeUserAgent(wd *vdi.WebDriver, ctx *ProcessContext) error {
	var err error

	// Get the User Agent
	userAgent := cmn.UADB.GetAgentByTypeAndOSAndBRG(ctx.config.Crawler.Platform, ctx.config.Crawler.BrowserPlatform, ctx.config.Selenium[ctx.SelID].Type)
	if userAgent == "" {
		if ctx.config.Crawler.Platform == "desktop" {
			userAgent = cmn.UsrAgentStrMap[ctx.config.Selenium[ctx.SelID].Type+"-desktop01"]
		} else {
			userAgent = cmn.UsrAgentStrMap[ctx.config.Selenium[ctx.SelID].Type+"-mobile01"]
		}
	}

	// Parse the User Agent string for {random_int1}
	if strings.Contains(userAgent, "{random_int1}") {
		// Generates a random integer in the range [0, 999)
		randInt := rand.IntN(8000) // nolint:gosec // We are using "math/rand/v2" here
		userAgent = strings.ReplaceAll(userAgent, "{random_int1}", strconv.Itoa(randInt))
	}

	// Parse the User Agent string for {random_int2}
	if strings.Contains(userAgent, "{random_int2}") {
		// Generates a random integer in the range [0, 999)
		randInt := rand.IntN(999) // nolint:gosec // We are using "math/rand/v2" here
		userAgent = strings.ReplaceAll(userAgent, "{random_int2}", strconv.Itoa(randInt))
	}

	// Check if the browser is Chrome and CDP is available
	if ctx.config.Selenium[ctx.SelID].Type == "chrome" || ctx.config.Selenium[ctx.SelID].Type == "chromium" {
		// Try with changeUserAgentCDP
		err := changeUserAgentCDP(ctx, userAgent)
		if err != nil {
			// Enable the Network domain
			_, _ = (*wd).ExecuteChromeDPCommand("Network.enable", map[string]interface{}{})
			// Set the User-Agent using CDP
			_, err = (*wd).ExecuteChromeDPCommand("Network.setUserAgentOverride", map[string]interface{}{
				"userAgent": userAgent,
				"platform":  ctx.config.Crawler.Platform,
			})
			if err == nil {
				cmn.DebugMsg(cmn.DbgLvlDebug3, "[CDP] UserAgent changed via CDP to: %s", userAgent)
				//return nil
			} else {
				cmn.DebugMsg(cmn.DbgLvlError, "[CDP] Failed to change UserAgent using CDP: %v", err)
			}
		}
	}

	// Fallback: Override userAgent using JavaScript injection
	js := fmt.Sprintf(`
		Object.defineProperty(navigator, 'userAgent', {
			get: function() { return '%s'; }
		});
	`, userAgent)

	_, err = (*wd).ExecuteScript(js, nil)
	if err != nil {
		return fmt.Errorf("failed to dynamically change User-Agent: %v", err)
	}

	cmn.DebugMsg(cmn.DbgLvlDebug3, "UserAgent changed via JavaScript to: %s", userAgent)
	return nil
}

/*
// Input for the Network.setUserAgentOverride command
type setUserAgentOverrideParams struct {
	UserAgent string `json:"userAgent"`
}

// Implement easyjson.Marshaler for the input
func (p *setUserAgentOverrideParams) MarshalJSON() ([]byte, error) {
	w := &jwriter.Writer{}
	p.MarshalEasyJSON(w)
	return w.Buffer.BuildBytes(), w.Error
}

func (p *setUserAgentOverrideParams) MarshalEasyJSON(w *jwriter.Writer) {
	w.RawString(`{"userAgent":"`)
	w.String(p.UserAgent)
	w.RawString(`"}`)
}

// Empty response struct for the command (no response expected)
type emptyResponse struct{}

// Implement easyjson.Unmarshaler for the output
func (r *emptyResponse) UnmarshalJSON(_ []byte) error {
	return nil
}

// Implement easyjson.Unmarshaler for the output
func (r *emptyResponse) UnmarshalEasyJSON(_ *jlexer.Lexer) {
	// No-op since there is no data to unmarshal
}

func changeUserAgentCDP(_ *vdi.WebDriver, pctx *ProcessContext, userAgent string) error {
	// Connect to the existing Chrome instance with CDP enabled
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Use cdp.NewRemoteAllocator to connect to the Chrome container
	remoteAllocatorCtx, cancelRemoteAllocator := cdp.NewRemoteAllocator(ctx, "http://"+pctx.config.Selenium[pctx.SelID].Host+":9222")
	defer cancelRemoteAllocator()

	// Create a new browser context
	browserCtx, cancelBrowser := cdp.NewContext(remoteAllocatorCtx)
	defer cancelBrowser()

	// Run CDP command to change the user-agent
	params := &setUserAgentOverrideParams{
		UserAgent: userAgent,
	}

	// Send the raw CDP command
	err := cdp.Run(browserCtx,
		cdp.ActionFunc(func(ctx context.Context) error {
			return cdp.FromContext(ctx).Browser.Execute(
				ctx,
				"Network.setUserAgentOverride",
				params,           // Input parameters
				&emptyResponse{}, // Empty output since no response is expected
			)
		}),
	)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "failed to change User-Agent using CDP: %v", err)
	}

	return err
}
*/

// changeUserAgentCDP modifies the User-Agent using CDP
func changeUserAgentCDP(pctx *ProcessContext, userAgent string) error {
	// Connect to the existing Selenium CDP instance
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Get WebSocket URL from Selenium-controlled browser
	port := fmt.Sprintf("%d", 9222+pctx.SelID)
	wsURL := "ws://" + pctx.config.Selenium[pctx.SelID].Host + ":" + port + "/devtools/browser/" + pctx.wd.SessionID()
	cmn.DebugMsg(cmn.DbgLvlDebug2, "[CDP] Connecting to CDP via 'ws': %s", wsURL)

	// Dial the Chrome Debugger Protocol
	conn, err := rpcc.DialContext(ctx, wsURL)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug2, "[CDP] failed to connect to CDP via 'ws': %v", err)
		return err
	}
	defer conn.Close() //nolint:errcheck // Close the connection when done

	// Create a CDP client
	cdpClient := cdp.NewClient(conn)

	// Enable Network domain
	err = cdpClient.Network.Enable(ctx, nil)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug2, "[CDP] failed to enable Network domain via 'ws': %v", err)
		return err
	}

	// Enable Emulation domain
	err = cdpClient.Emulation.SetUserAgentOverride(ctx, &emulation.SetUserAgentOverrideArgs{
		UserAgent: userAgent,
	})
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug2, "[CDP] failed to change User-Agent using CDP via 'ws': %v", err)
		return err
	}

	cmn.DebugMsg(cmn.DbgLvlDebug2, "[CDP] Successfully changed UserAgent to: '%s', using CDP via 'ws'", userAgent)
	return nil
}

func moveMouseRandomly(wd vdi.WebDriver) {
	// Example: moving mouse via Rbee
	x := rand.IntN(1920) // pick some random X coordinate
	y := rand.IntN(1080) // pick some random Y coordinate

	jsScript := fmt.Sprintf(`
		(function() {
			var xhr = new XMLHttpRequest();
			xhr.open("POST", "http://127.0.0.1:3000/v1/rb", true);
			xhr.setRequestHeader("Content-Type", "application/json;charset=UTF-8");
			var data = JSON.stringify({
				"Action": "moveMouse",
				"X": %d,
				"Y": %d
			});
			xhr.send(data);
		})();`, x, y)

	// Run the JavaScript in the browser
	_, err := wd.ExecuteScript(jsScript, nil)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug3, "failed to execute Rbee moveMouse: %v", err)
	}
}

func vdiSleep(ctx *ProcessContext, delay float64) error {
	driver := ctx.wd

	if delay < 3 {
		delay = 3
	}

	divider := math.Log10(delay+1) * 10 // Adjust multiplier as needed

	if divider >= float64(ctx.config.Crawler.Timeout) {
		divider = float64(ctx.config.Crawler.Timeout) - 1
	}

	waitDuration := time.Duration(delay) * time.Second
	pollInterval := time.Duration(delay/divider) * time.Second // Check every pollInterval seconds to keep alive

	startTime := time.Now()

	cmn.DebugMsg(cmn.DbgLvlDebug3, "Waiting for %v seconds...", delay)
	for time.Since(startTime) < waitDuration {
		// Perform a lightweight interaction to keep the session alive
		_, err := driver.Title()
		if err != nil {
			return err
		}
		time.Sleep(pollInterval)
		// refresh session timeout
		ctx.RefreshCrawlingTimer()
		// Move the mouse using rBee
		moveMouseRandomly(driver)
	}
	cmn.DebugMsg(cmn.DbgLvlDebug3, "Waited for %v seconds", delay)

	return nil
}

func getCookies(ctx *ProcessContext, wd *vdi.WebDriver) error {
	// Get the cookies
	cookies, err := (*wd).GetCookies()
	if err != nil {
		return fmt.Errorf("failed to get cookies: %v", err)
	}

	// Add new cookies to the context
	for _, cookie := range cookies {
		ctx.CollectedCookies[cookie.Name] = cookie.Value
	}

	return nil
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
func extractPageInfo(webPage *vdi.WebDriver, ctx *ProcessContext, docType string, PageCache *PageInfo) error {
	if ctx.VDIReturned {
		// If the VDI session is returned, stop the process
		return nil
	}

	currentURL, _ := (*webPage).CurrentURL()

	// Detect Object Type
	objType := docType
	title := currentURL
	summary := ""
	bodyText := ""
	htmlContent := ""
	metaTags := []MetaTag{}
	scrapedList := []ScrapedItem{}

	// Copy the current webPage object
	webPageCopy := *webPage

	// Get the HTML content of the page
	if docTypeIsHTML(objType) {
		htmlContent, _ = (*webPage).PageSource()
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "loading HTML content, during Page Info Extraction: %v", err)
			return err
		}

		// Run scraping rules if any
		var scrapedData string
		var url string
		url, err = (*webPage).CurrentURL()
		if err == nil {
			scrapedData, err = processScrapingRules(&webPageCopy, ctx, url)
			if err != nil {
				if strings.Contains(err.Error(), errCriticalError) {
					return err
				}
			}
		}
		if scrapedData != "" {
			// put ScrapedData into a map
			scrapedMap := make(map[string]interface{})
			//cmn.DebugMsg(cmn.DbgLvlDebug3, "Scraped Data: %v", scrapedData)
			err = json.Unmarshal([]byte(scrapedData), &scrapedMap)
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "unmarshalling scraped data: %v, full data: %v", err, scrapedData)
				// Try to remove impurities from the scraped data
				scrapedData = removeImpurities(scrapedData)
				err = json.Unmarshal([]byte(scrapedData), &scrapedMap)
				if err != nil {
					cmn.DebugMsg(cmn.DbgLvlError, "unmarshalling scraped data: %v, full data: %v", err, scrapedData)
				}
			}

			// append the map to the list
			scrapedList = append(scrapedList, scrapedMap)
			ctx.Status.TotalScraped++
		}
		cmn.DebugMsg(cmn.DbgLvlDebug3, "Scraped Data (JSON): %v", scrapedList)

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
		docCopy.Find("script").Each(func(_ int, s *goquery.Selection) {
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

	// Update the PageInfo object
	(*PageCache).Title = title
	(*PageCache).Summary = summary
	(*PageCache).BodyText = bodyText
	(*PageCache).HTML = htmlContent
	(*PageCache).MetaTags = metaTags
	(*PageCache).DetectedLang = detectLang((*webPage))
	(*PageCache).DetectedType = objType
	(*PageCache).ScrapedData = scrapedList

	return nil
}

// removeImpurities removes invalid JSON characters from a string, avoiding sequences like ",," or ",false,"
func removeImpurities(s string) string {
	var result strings.Builder
	quotes := false
	escape := false
	prevComma := false
	for _, r := range s {
		if escape {
			result.WriteRune(r)
			escape = false
			continue
		}
		if r == '\\' {
			escape = true
			result.WriteRune(r)
			continue
		}
		if r == '"' {
			quotes = !quotes
			result.WriteRune(r)
			prevComma = false
			continue
		}
		if quotes {
			result.WriteRune(r)
			continue
		}
		if r == ',' {
			if prevComma {
				continue
			}
			result.WriteRune(r)
			prevComma = true
			continue
		}
		if strings.ContainsRune("[]{}:truefalsenull0123456789", r) || unicode.IsSpace(r) {
			result.WriteRune(r)
			prevComma = false
			continue
		}
	}
	return result.String()
}

func detectLang(wd vdi.WebDriver) string {
	var lang string
	var err error
	html, err := wd.FindElement(vdi.ByXPATH, "/html")
	if err == nil {
		lang, err = html.GetAttribute("lang")
		if err != nil {
			lang = ""
		}
	}
	if lang == "" {
		bodyHTML, err := wd.FindElement(vdi.ByTagName, "body")
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
func inferDocumentType(url string, wd *vdi.WebDriver) string {
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

	// Prepend a scheme if it's missing
	if !strings.Contains(u, "://") {
		u = "http://" + u
	}

	// Check if the URL has an allowed protocol
	if !IsValidURIProtocol(u) {
		return false
	}

	// Check if u is ONLY a protocol (aka not a full URL)
	for _, proto := range allowedProtocols {
		if u == proto {
			return false
		}
	}

	// Parse the URL and check for errors
	_, err := url.ParseRequestURI(u)
	return err == nil
}

// IsValidURIProtocol checks if the URI has a valid protocol.
func IsValidURIProtocol(u string) bool {
	u = strings.TrimSpace(u)
	found := false
	for _, proto := range allowedProtocols {
		if strings.HasPrefix(u, proto) {
			found = true
			break
		}
	}
	return found
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
	if ctx.config.Crawler.BrowsingMode == optBrowsingHuman ||
		ctx.config.Crawler.BrowsingMode == optBrowsingRecu ||
		ctx.config.Crawler.BrowsingMode == optBrowsingRCRecu {
		doc.Find("a").Each(func(_ int, item *goquery.Selection) {
			linkTag := item
			link, _ := linkTag.Attr("href")
			link = normalizeURL(link, 0)
			linkItem := LinkItem{
				PageURL:   url,  // URL of the page where the link was found (CurrentURL)
				Link:      link, // Link to crawl
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

	// Check if the link URL has the source URL as prefix (if domainLevel is 1)
	if domainLevel == 1 {
		cmn.DebugMsg(cmn.DbgLvlDebug3, "Restriction level 1, Source Domain: %s, Link Domain: %s", sourceURL, linkParsed.String())
		return !strings.HasPrefix(linkParsed.String(), sourceURL)
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

// KeepSessionAlive is a dummy function that keeps the WebDriver session alive.
// It's used to prevent the WebDriver session from timing out.
func KeepSessionAlive(wd vdi.WebDriver) {
	if wd == nil {
		return
	}
	// Keep session alive
	_, _ = wd.Title()
}

// worker is the worker function that is responsible for crawling a page
func worker(processCtx *ProcessContext, id int, jobs chan LinkItem) error {
	var skippedURLs []LinkItem

	// Loop over the jobs channel and process each job
	for url := range jobs {
		KeepSessionAlive(processCtx.wd)

		// Check if the URL should be skipped
		if processCtx.config.Crawler.MaxLinks > 0 && (processCtx.Status.TotalPages >= processCtx.config.Crawler.MaxLinks) {
			cmn.DebugMsg(cmn.DbgLvlDebug, "Worker %d: Stopping due reached max_links limit: %d\n", id, processCtx.Status.TotalPages)
			break
		}

		// Recursive Mode
		urlLink := url.Link
		if strings.HasPrefix(url.Link, "/") {
			urlLink, _ = combineURLs(processCtx.source.URL, url.Link)
		}

		// Check if the URL should be skipped
		skip := skipURL(processCtx, id, urlLink)
		if skip {
			processCtx.Status.TotalSkipped++
			skippedURLs = append(skippedURLs, url)
			continue
		}
		if processCtx.visitedLinks[cmn.NormalizeURL(urlLink)] {
			// URL already visited
			processCtx.Status.TotalDuplicates++
			cmn.DebugMsg(cmn.DbgLvlDebug2, "Worker %d: URL %s already visited\n", id, url.Link)
			continue
		}

		if processCtx.config.Crawler.ResetCookiesPolicy == optCookiesOnReq || processCtx.config.Crawler.ResetCookiesPolicy == cmn.AlwaysStr {
			// Reset cookies on each request
			_ = ResetSiteSession(processCtx)
		}

		// Process the job
		cmn.DebugMsg(cmn.DbgLvlDebug, "Worker %d: Processing job %s\n", id, url.Link)
		var err error
		if strings.ToLower(strings.TrimSpace(processCtx.config.Crawler.BrowsingMode)) == optBrowsingRecu {
			err = processJob(processCtx, id, urlLink, skippedURLs)
		} else if strings.ToLower(strings.TrimSpace(processCtx.config.Crawler.BrowsingMode)) == optBrowsingRCRecu {
			// Right Click Recursive Mode
			err = rightClick(processCtx, id, url)
		} else if strings.ToLower(strings.TrimSpace(processCtx.config.Crawler.BrowsingMode)) == optBrowsingHuman {
			// Human Mode
			// Find the <a> element that contains the URL and click it
			err = clickLink(processCtx, id, url)
		} else {
			// Fuzzing Mode
			// Fuzzy works like recursive, however instead of extracting links from the page, it generates links based on the crawling rules
			err = processJob(processCtx, id, urlLink, skippedURLs)
		}
		processCtx.visitedLinks[cmn.NormalizeURL(urlLink)] = true

		if err == nil {
			processCtx.Status.TotalPages++
			cmn.DebugMsg(cmn.DbgLvlDebug, "Worker %d: Finished job %s\n", id, url.Link)
		} else {
			processCtx.Status.TotalErrors++
			cmn.DebugMsg(cmn.DbgLvlDebug, "Worker %d: Finished job %s with an error: %v\n", id, url.Link, err)
			if strings.Contains(err.Error(), errCriticalError) {
				return err
			}
		}

		// Clear the skipped URLs
		skippedURLs = nil

		// Delay before processing the next job
		if processCtx.config.Crawler.Delay != "0" {
			delay := exi.GetFloat(processCtx.config.Crawler.Delay)
			processCtx.Status.LastDelay = delay
			_ = vdiSleep(processCtx, delay)
		}
		if processCtx.RefreshCrawlingTimer != nil {
			processCtx.RefreshCrawlingTimer()
		}
		if processCtx.config.Crawler.MaxLinks > 0 && (processCtx.Status.TotalPages >= processCtx.config.Crawler.MaxLinks) {
			cmn.DebugMsg(cmn.DbgLvlDebug, "Worker %d: Stopping due reached max_links limit: %d\n", id, processCtx.Status.TotalPages)
			break
		}
	}

	return nil
}

func skipURL(processCtx *ProcessContext, id int, url string) bool {
	// Check if the URL is empty
	url = strings.TrimSpace(url)
	if url == "" {
		return true
	}

	// Check if the URL is absolute or relative
	if strings.HasPrefix(url, "/") {
		url, _ = combineURLs(processCtx.source.URL, url)
	}

	// Check if the URL is valid (aka if it's within the allowed restricted boundaries)
	if (processCtx.source.Restricted != 4) && isExternalLink(processCtx.source.URL, url, processCtx.source.Restricted) {
		cmn.DebugMsg(cmn.DbgLvlDebug2, "Worker %d: Skipping URL '%s' due 'external' policy.\n", id, url)
		return true
	}

	// Check if the URL is the same as the Source URL (in which case skip it)
	if url == processCtx.source.URL {
		cmn.DebugMsg(cmn.DbgLvlDebug2, "Worker %d: Skipping URL '%s' as it is the same as the source URL\n", id, url)
		return true
	}

	// Check if the URL matches user defined patterns (negative or positive)
	if len(processCtx.userURLPatterns) > 0 {
		// Flag to track whether the URL should be skipped
		shouldSkip := false
		matches := 0

		for _, pattern := range processCtx.userURLPatterns {
			re := regexp.MustCompile(pattern)
			cmn.DebugMsg(cmn.DbgLvlDebug5, "Worker %d: Checking URL '%s' against user-defined pattern '%s'\n", id, url, pattern)
			if re.MatchString(url) {
				matches++

				// Determine if this is a "negative" or "positive" pattern
				if isNegativePattern(pattern) {
					// Negative pattern found, skip the URL
					shouldSkip = true
					break
				}
				// Positive pattern found, do not skip
				shouldSkip = false
				break
			}
		}

		// If we decided to skip based on negative pattern, return true
		if shouldSkip {
			cmn.DebugMsg(cmn.DbgLvlDebug2, "Worker %d: Skipping URL '%s' due to user-defined pattern\n", id, url)
			return true
		}

		// If we did not find any matches, skip the URL
		if matches == 0 {
			cmn.DebugMsg(cmn.DbgLvlDebug2, "Worker %d: Skipping URL '%s' due to no user-defined pattern matches\n", id, url)
			return true
		}
	}

	// If none of the conditions matched, do not skip
	return false
}

// Function to determine if a pattern is negative (e.g., begins with a "!" or other logic you define)
func isNegativePattern(pattern string) bool {
	// For example, assume negative patterns start with "!".
	return strings.HasPrefix(pattern, "!")
}

// rightClick simulates right-clicking on a link and opening it in the current tab using custom JavaScript
func rightClick(processCtx *ProcessContext, id int, url LinkItem) error {
	// Lock the mutex to ensure only one goroutine accesses the vdi.WebDriver at a time
	processCtx.getURLMutex.Lock()
	defer processCtx.getURLMutex.Unlock()

	if processCtx.wd == nil || processCtx.VDIReturned {
		// If the VDI has returned, stop the worker
		return nil
	}

	var err error

	// Check if we are on the right page that should contain url.Link:
	pageURL, err := processCtx.wd.CurrentURL()
	if err != nil {
		return err
	}

	// If we are not already on the right page that should contain url.Link, navigate to it
	if (url.PageURL != pageURL) && (url.PageURL+"/" != pageURL) {
		// Navigate to the page if not already there
		_, _, err := getURLContent(url.PageURL, processCtx.wd, 0, processCtx)
		if err != nil {
			return err
		}
	}

	// JavaScript to simulate right-click and open the link in the same tab
	jsScript := `
		(function(url) {
			var link = document.querySelector('a[href="' + url + '"]');
			if (link) {
				// Simulate right-click event
				var rightClickEvent = new MouseEvent('contextmenu', {
					bubbles: true,
					cancelable: true,
					view: window,
					button: 2  // Right-click
				});
				link.dispatchEvent(rightClickEvent);

				// Open the link in the same tab
				window.location.href = link.href;
				return { success: "Opened link in the same tab: " + link.href };
			} else {
				return { error: "Link not found for the URL: " + url };
			}
		})(arguments[0]);
	`

	// Execute the custom JavaScript to right-click and open the link
	_, err = processCtx.wd.ExecuteScript(jsScript, []interface{}{url.Link})
	if err != nil {
		return err
	}

	// Wait for the page to load (adjustable delay based on configuration)
	delay := exi.GetFloat(processCtx.config.Crawler.Interval)
	_ = vdiSleep(processCtx, delay)

	// Check current URL (because some Action Rules may change the URL)
	currentURL, _ := processCtx.wd.CurrentURL()

	cmn.DebugMsg(cmn.DbgLvlDebug5, "Worker %d: Had to open '%s' link in the same tab were we had: %s\n", id, url.Link, currentURL)

	// Execute any action rules after the link is opened
	processActionRules(&processCtx.wd, processCtx, currentURL)
	processCtx.RefreshCrawlingTimer()

	// Re-Check current URL (because some Action Rules may change the URL)
	currentURL, _ = processCtx.wd.CurrentURL()

	// Allocate pageCache object
	pageCache := PageInfo{}

	// Collect Detected Technologies
	detectCtx := detect.DContext{
		CtxID:        processCtx.GetContextID(),
		TargetURL:    currentURL,
		ResponseBody: nil,
		Header:       nil,
		HSSLInfo:     nil,
		WD:           &processCtx.wd,
		RE:           processCtx.re,
		Config:       &processCtx.config,
	}
	detectedTech := detect.DetectTechnologies(&detectCtx)
	if detectedTech != nil {
		pageCache.DetectedTech = *detectedTech
	}
	processCtx.RefreshCrawlingTimer()

	// Extract page information and cache it for indexing
	docType := inferDocumentType(url.Link, &processCtx.wd)
	err = extractPageInfo(&processCtx.wd, processCtx, docType, &pageCache)
	if err != nil {
		if strings.Contains(err.Error(), errCriticalError) {
			return err
		}
		cmn.DebugMsg(cmn.DbgLvlError, errWExtractingPageInfo, id, err)
	}
	pageCache.sourceID = processCtx.source.ID
	// Extract links from the Current Page
	pageCache.Links = append(pageCache.Links, extractLinks(processCtx, pageCache.HTML, url.Link)...)
	/*
		urlItem := LinkItem{
			PageURL:   url.Link,
			Link:      currentURL,
			ElementID: "",
		}
		pageCache.Links = append(pageCache.Links, urlItem)
	*/

	// Collect performance metrics (optional)
	metrics, err := retrieveNavigationMetrics(&processCtx.wd)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, errFailedToRetrieveMetrics, err)
	} else {
		for key, value := range metrics {
			switch key {
			case optDNSLookup:
				pageCache.PerfInfo.DNSLookup = value.(float64)
			case optTCPConn:
				pageCache.PerfInfo.TCPConnection = value.(float64)
			case optTTFB:
				pageCache.PerfInfo.TimeToFirstByte = value.(float64)
			case optContent:
				pageCache.PerfInfo.ContentLoad = value.(float64)
			case optPageLoad:
				pageCache.PerfInfo.PageLoad = value.(float64)
			}
		}
	}

	// Collect performance logs
	logs, err := processCtx.wd.Log("performance")
	if err != nil {
		return err
	}

	// Parse and store performance logs
	for _, entry := range logs {
		var log PerformanceLogEntry
		err := json.Unmarshal([]byte(entry.Message), &log)
		if err != nil {
			return err
		}
		if len(log.Message.Params.ResponseInfo.URL) > 0 {
			pageCache.PerfInfo.LogEntries = append(pageCache.PerfInfo.LogEntries, log)
		}
	}

	// Collect XHR
	if processCtx.config.Crawler.CollectXHR {
		collectXHR(processCtx, &pageCache)
		processCtx.RefreshCrawlingTimer()
	}

	// Clear HTML and content if not required
	if !processCtx.config.Crawler.CollectHTML {
		pageCache.HTML = ""
	}
	if !processCtx.config.Crawler.CollectContent {
		pageCache.BodyText = ""
	}

	// Index the page after collecting data
	pageCache.Config = &processCtx.config
	_, err = indexPage(*processCtx.db, url.Link, &pageCache)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, errWorkerLog, id, url.Link, err)
	}

	// Mark the link as visited and add new links to the process context
	processCtx.visitedLinks[cmn.NormalizeURL(url.Link)] = true
	processCtx.visitedLinks[cmn.NormalizeURL(currentURL)] = true

	// Add new links to the process context
	if len(pageCache.Links) > 0 {
		processCtx.linksMutex.Lock()
		processCtx.newLinks = append(processCtx.newLinks, pageCache.Links...)
		processCtx.linksMutex.Unlock()
	}

	// Before we return, we need to call goBack to go back to the previous page
	err = goBack(processCtx)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Worker %d: Error navigating back: %v\n", id, err)
	}

	return nil
}

// Go back to the previous page
func goBack(processCtx *ProcessContext) error {
	_, err := processCtx.wd.ExecuteScript("window.history.back();", nil)
	if err != nil {
		return fmt.Errorf("Failed to navigate back: %v", err)
	}
	// Wait for the page to load after going back
	delay := exi.GetFloat(processCtx.config.Crawler.Interval)
	_ = vdiSleep(processCtx, delay)
	return nil
}

func clickLink(processCtx *ProcessContext, id int, url LinkItem) error {
	// Set getURLMutex to ensure only one goroutine is accessing the vdi.WebDriver at a time
	processCtx.getURLMutex.Lock()
	defer processCtx.getURLMutex.Unlock()

	if processCtx.VDIReturned {
		// If the VDI has returned, we need to stop the worker
		return nil
	}

	// Check if we are on the right page that should contain url.Link:
	pageURL, err := processCtx.wd.CurrentURL()
	if err != nil {
		return err
	}
	if (url.PageURL != pageURL) && (url.PageURL+"/" != pageURL) {
		// Navigate to the page if not already there
		_, _, err := getURLContent(url.PageURL, processCtx.wd, 0, processCtx)
		if err != nil {
			return err
		}
	}

	// find the <a> element that contains the URL
	element, err := processCtx.wd.FindElement(vdi.ByLinkText, url.Link)
	if err != nil {
		return err
	}
	// Click the element
	err = element.Click()
	if err != nil {
		return err
	}

	// Wait for Page to Load
	delay := exi.GetFloat(processCtx.config.Crawler.Interval)
	_ = vdiSleep(processCtx, delay) // Pause to let page load

	// Check current URL
	currentURL, _ := processCtx.wd.CurrentURL()
	if currentURL != url.Link {
		cmn.DebugMsg(cmn.DbgLvlError, "Worker %d: Error navigating to %s: URL mismatch\n", id, url)
		return errors.New("URL mismatch")
	}

	// Execute Action Rules
	processActionRules(&processCtx.wd, processCtx, url.Link)

	// Re-Get current URL (because some Action Rules may change the URL)
	currentURL, _ = processCtx.wd.CurrentURL()

	// Get docType (because some Action Rules may change the URL)
	docType := inferDocumentType(currentURL, &processCtx.wd)

	// Allocate pageCache object
	pageCache := PageInfo{}

	// Collect Detected Technologies
	detectCtx := detect.DContext{
		CtxID:        processCtx.GetContextID(),
		TargetURL:    currentURL,
		ResponseBody: nil,
		Header:       nil,
		HSSLInfo:     nil,
		WD:           &processCtx.wd,
		RE:           processCtx.re,
		Config:       &processCtx.config,
	}
	detectedTech := detect.DetectTechnologies(&detectCtx)
	if detectedTech != nil {
		pageCache.DetectedTech = *detectedTech
	}

	// Extract page information
	err = extractPageInfo(&processCtx.wd, processCtx, docType, &pageCache)
	if err != nil {
		if strings.Contains(err.Error(), errCriticalError) {
			return err
		}
		cmn.DebugMsg(cmn.DbgLvlError, errWExtractingPageInfo, id, err)
	}
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
			case optDNSLookup:
				pageCache.PerfInfo.DNSLookup = value.(float64)
			case optTCPConn:
				pageCache.PerfInfo.TCPConnection = value.(float64)
			case optTTFB:
				pageCache.PerfInfo.TimeToFirstByte = value.(float64)
			case optContent:
				pageCache.PerfInfo.ContentLoad = value.(float64)
			case optPageLoad:
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

	// Collect XHR
	if processCtx.config.Crawler.CollectXHR {
		collectXHR(processCtx, &pageCache)
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
		cmn.DebugMsg(cmn.DbgLvlError, errWorkerLog, id, url.Link, err)
	}
	processCtx.visitedLinks[cmn.NormalizeURL(url.Link)] = true

	// Add the new links to the process context
	if len(pageCache.Links) > 0 {
		processCtx.linksMutex.Lock()
		processCtx.newLinks = append(processCtx.newLinks, pageCache.Links...)
		processCtx.linksMutex.Unlock()
	}

	return err
}

// ResetSiteSession resets the site session by deleting all cookies and local storage
func ResetSiteSession(ctx *ProcessContext) error {
	// Check if session is still valid
	if ctx.wd == nil {
		return errors.New("Data Collection Session is nil")
	}

	// Clear cookies
	for name := range ctx.CollectedCookies {
		err := ctx.wd.DeleteCookie(name)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Failed to delete cookie '%s': %v", name, err)
		}
	}

	// Clear local storage
	_, _ = ctx.wd.ExecuteScript("window.localStorage.clear();", nil)

	// Clear session storage
	_, _ = ctx.wd.ExecuteScript("window.sessionStorage.clear();", nil)

	// Clear IndexedDB (if applicable)
	_, _ = ctx.wd.ExecuteScript("indexedDB.databases().then(dbs => dbs.forEach(db => indexedDB.deleteDatabase(db.name)));", nil)

	return nil
}

func processJob(processCtx *ProcessContext, id int, url string, skippedURLs []LinkItem) error {
	// Set getURLMutex to ensure only one goroutine is accessing the vdi.WebDriver at a time
	processCtx.getURLMutex.Lock()
	defer processCtx.getURLMutex.Unlock()

	if processCtx.VDIReturned {
		// If the VDI session has been returned we need to stop the worker
		return nil
	}

	// Get the HTML content of the page
	htmlContent, docType, err := getURLContent(url, processCtx.wd, 1, processCtx)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Worker %d: Error getting HTML content for %s: %v\n", id, url, err)
		return err
	}

	// Re-Get current URL (because some Action Rules may change the URL)
	currentURL, _ := processCtx.wd.CurrentURL()

	// Create PageInfo object to store the extracted information
	pageCache := PageInfo{}

	// Collect Detected Technologies
	detectCtx := detect.DContext{
		CtxID:        processCtx.GetContextID(),
		TargetURL:    currentURL,
		ResponseBody: nil,
		Header:       nil,
		HSSLInfo:     nil,
		WD:           &processCtx.wd,
		RE:           processCtx.re,
		Config:       &processCtx.config,
	}
	processCtx.RefreshCrawlingTimer()
	detectedTech := detect.DetectTechnologies(&detectCtx)
	if detectedTech != nil {
		pageCache.DetectedTech = *detectedTech
	}
	processCtx.RefreshCrawlingTimer()

	// Extract page information
	err = extractPageInfo(&htmlContent, processCtx, docType, &pageCache)
	if err != nil {
		if strings.Contains(err.Error(), errCriticalError) {
			return err
		}
		cmn.DebugMsg(cmn.DbgLvlError, errWExtractingPageInfo, id, err)
	}
	pageCache.sourceID = processCtx.source.ID
	pageCache.Links = append(pageCache.Links, extractLinks(processCtx, pageCache.HTML, currentURL)...)
	pageCache.Links = append(pageCache.Links, skippedURLs...)
	// Generate Keywords
	pageCache.Keywords = extractKeywords(pageCache)
	processCtx.RefreshCrawlingTimer()

	// Collect Navigation Timing metrics
	if processCtx.config.Crawler.CollectPerfMetrics {
		collectNavigationMetrics(&processCtx.wd, &pageCache)
		processCtx.RefreshCrawlingTimer()
	}

	// Collect Page logs
	if processCtx.config.Crawler.CollectPageEvents {
		collectPageLogs(&htmlContent, &pageCache)
		processCtx.RefreshCrawlingTimer()
	}

	// Collect XHR
	if processCtx.config.Crawler.CollectXHR {
		collectXHR(processCtx, &pageCache)
		processCtx.RefreshCrawlingTimer()
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
	_, err = indexPage(*processCtx.db, currentURL, &pageCache)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, errWorkerLog, id, url, err)
	}
	if processCtx.visitedLinks == nil {
		processCtx.visitedLinks = make(map[string]bool)
	}
	processCtx.visitedLinks[cmn.NormalizeURL(url)] = true

	// Add the new links to the process context
	if len(pageCache.Links) > 0 {
		processCtx.linksMutex.Lock()
		processCtx.newLinks = append(processCtx.newLinks, pageCache.Links...)
		processCtx.linksMutex.Unlock()
	}
	resetPageInfo(&pageCache) // Reset the PageInfo object

	if processCtx.RefreshCrawlingTimer != nil {
		processCtx.RefreshCrawlingTimer()
	}
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

/*
func getLocalNetworks() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return []string{}
	}
	var networks []string
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				networks = append(networks, ipnet.IP.String())
			}
		}
	}
	// Transform the networks to CIDR format
	for i, network := range networks {
		_, ipnet, _ := net.ParseCIDR(network + "/24")
		networks[i] = ipnet.String()
	}

	return networks
}
*/

// TakeScreenshot is responsible for taking a screenshot of the current page
func TakeScreenshot(wd *vdi.WebDriver, filename string, maxHeight int) (Screenshot, error) {
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

func getWindowSize(wd *vdi.WebDriver) (int, int, error) {
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

func getTotalHeight(wd *vdi.WebDriver) (int, error) {
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

func captureScreenshots(wd *vdi.WebDriver, totalHeight, windowHeight int) ([][]byte, error) {
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
		case cmn.HTTPStr:
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
	if saveCfg.SSLMode == cmn.EnableStr {
		protocol = cmn.HTTPSStr
	} else {
		protocol = cmn.HTTPStr
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
	defer resp.Body.Close() //nolint:errcheck // Don't lint for error not checked, this is a defer statement

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
	file, err := os.OpenFile(filename, os.O_RDWR|os.O_CREATE, cmn.DefaultFilePerms) //nolint:gosec // filename and path here is provided by the admin
	if err != nil {
		return err
	}
	defer file.Close() //nolint:errcheck // Don't lint for error not checked, this is a defer statement

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
