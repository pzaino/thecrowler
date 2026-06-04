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
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/PuerkitoBio/goquery"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	detect "github.com/pzaino/thecrowler/pkg/detection"
	exi "github.com/pzaino/thecrowler/pkg/exprterpreter"
	httpi "github.com/pzaino/thecrowler/pkg/httpinfo"
	neti "github.com/pzaino/thecrowler/pkg/netinfo"
	vdi "github.com/pzaino/thecrowler/pkg/vdi"
)

const (
	dbConnCheckErr             = "checking database connection: %v\n"
	dbConnTransErr             = "committing transaction: %v"
	errFailedToRetrieveMetrics = "failed to retrieve navigation timing metrics: %v"
	errCriticalError           = "[critical]"
	errWExtractingPageInfo     = "Worker %s: Error extracting page info: %v\n"
	errWorkerLog               = "Worker %s: Error indexing page %s: %v\n"

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

	binaryDataOmitted = "[binary data omitted]"
)

var (
	config cfg.Config // Configuration "object"
)

var indexPageMutex sync.Mutex // Mutex to ensure that only one goroutine is indexing a page at a time

// CrawlWebsite is responsible for crawling a website, it's the main entry point
// and it's called from the main.go when there is a Source to crawl.
func CrawlWebsite(args *Pars, sel vdi.SeleniumInstance, releaseVDI chan<- vdi.SeleniumInstance) {
	var (
		closeChanOnce sync.Once
		err           error
	)

	// Initialize the process context
	processCtx := NewProcessContext(args)
	if processCtx == nil {
		cmn.DebugMsg(cmn.DbgLvlError, "failed to create a new ProcessContext")
		UpdateSourceState(args.DB, args.Src.URL, errors.New("failed to create a new ProcessContext"))
		args.Status.EndTime = time.Now()
		args.Status.PipelineRunning.Store(3) // Set the pipeline status to error
		args.Status.TotalErrors.Add(1)
		args.Status.LastError = "failed to create a new ProcessContext"
		cmn.DebugMsg(cmn.DbgLvlError, "Crawling process aborted for source: %s", args.Src.URL)
		return
	}

	// We have process context, so we can proceed:

	// Pipeline has started
	processCtx.Status.StartTime = time.Now()
	processCtx.Status.PipelineRunning.Store(1) // Set the pipeline status to running
	processCtx.SelInstance = sel
	processCtx.CollectedCookies = make(map[string]any)
	processCtx.SetVDIReturnedFlag(false)
	processCtx.RefreshCrawlingTimer = args.Refresh
	processCtx.pStatus = 1 // Processing started

	cid := processCtx.GetContextID()
	processCtx.Status.ContextID = cid // Store context ID in status (this should be the only place where it's set!)

	if contentTypeDetectionMap.IsEmpty() {
		cmn.DebugMsg(cmn.DbgLvlDebug, "Content type detection rules are empty, loading them...")
		// Load the content type detection rules
		err = loadContentTypeDetectionRules("./support/content_type_detection.yaml")
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "loading content type detection rules: %v", err)
		}
	}

	// Combine default configuration with the source configuration
	if processCtx.source.Config != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-CrawlWebsite] Custom Source configuration found, proceeding to combine it with the default one for this source...")
		processCtx.config, err = cfg.CombineConfig(processCtx.config, *processCtx.source.Config)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "combining source configuration: %v", err)
		} else {
			cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-CrawlWebsite] Source configuration combined successfully.")
		}
	}

	// Log the crawling process
	cmn.DebugMsg(cmn.DbgLvlDebug5, "[DEBUG-CrawlWebsite] Crawling using: %s", processCtx.config.Crawler.BrowsingMode)

	var timeout time.Duration
	timeout = parseProcessingTimeout(processCtx.config.Crawler.ProcessingTimeout) - 1
	if timeout == 0 {
		timeout = 20 * time.Minute // default fallback
		timeout -= time.Second
	}

	timeoutTimer := time.NewTimer(timeout)
	defer func() {
		timeoutTimer.Stop()

		defer func() {
			recover() //nolint:errcheck // avoid panic if somehow closed elsewhere
		}()

		closeChanOnce.Do(func() {
			// Ensure we always close the session correctly
			closeSession(processCtx, args, &sel, releaseVDI, err)
		})
	}()

	// If the URL has no HTTP(S) or FTP(S) protocol, do only NETInfo
	if classifySourceProtocol(args.Src.URL) != SourceProtocolWeb {
		cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-CrawlWebsite] URL %s has no HTTP(S) or FTP(S) protocol, skipping crawling...", args.Src.URL)
		processCtx.GetNetInfo(args.Src.URL)
		_, err := processCtx.IndexNetInfo(1)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "indexing network information: %v", err)
			processCtx.Status.PipelineRunning.Store(3)
		} else {
			processCtx.Status.PipelineRunning.Store(2)
		}
		UpdateSourceState(args.DB, args.Src.URL, nil)
		processCtx.Status.EndTime = time.Now()
		cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-CrawlWebsite] Finished crawling website: %s", args.Src.URL)
		return
	}

	// Initialize the Selenium instance
	if err = processCtx.ConnectToVDI(sel); err != nil {
		UpdateSourceState(args.DB, args.Src.URL, err)
		processCtx.Status.EndTime = time.Now()
		processCtx.Status.PipelineRunning.Store(3)
		processCtx.Status.TotalErrors.Add(1)
		processCtx.Status.LastError = err.Error()
		cmn.DebugMsg(cmn.DbgLvlError, vdi.VDIConnError, err)
		return
	}
	processCtx.Status.CrawlingRunning.Store(1)

	// Extract custom configuration from the source
	sourceConfig := make(map[string]interface{})
	if processCtx.source.Config != nil {
		// Unmarshal the JSON RawMessage into a map[string]interface{}
		err := json.Unmarshal(*processCtx.source.Config, &sourceConfig)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "unmarshalling source configuration: %v", err)
		}
		processCtx.srcCfg = sourceConfig
		cmn.DebugMsg(cmn.DbgLvlDebug2, "[DEBUG-CrawlWebsite] Source configuration extracted: %v", processCtx.srcCfg)
		crawlingConfig := make(map[string]any)
		crawlingConfig, _ = processCtx.srcCfg["crawling_config"].(map[string]interface{})
		// Check if we have UnwantedURLs in the source configuration (and if so compile the patterns)
		unwantedURLs, ok := crawlingConfig["unwanted_urls"]
		if ok {
			cmn.DebugMsg(cmn.DbgLvlDebug2, "[DEBUG-CrawlWebsite] Found unwanted_urls in source configuration: %v", unwantedURLs)
			if unwantedURLsSlice, ok := unwantedURLs.([]interface{}); ok {
				cmn.DebugMsg(cmn.DbgLvlDebug2, "[DEBUG-CrawlWebsite] Found unwanted_urls in source configuration: %v", unwantedURLsSlice)
				processCtx.compiledUURLs = make(map[string]*regexp.Regexp)
				for _, pattern := range unwantedURLsSlice {
					if strPattern, ok := pattern.(string); ok {
						// Compile the regex pattern
						re, err := regexp.Compile(strPattern)
						if err != nil {
							cmn.DebugMsg(cmn.DbgLvlError, "compiling unwanted URL pattern '%s': %v", strPattern, err)
							continue
						}
						processCtx.compiledUURLs[strPattern] = re
					}
				}
			}
		}
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

	done := make(chan struct{})

	// Wrap the entire crawling workflow into a goroutine
	go func() {
		defer close(done)

		// Crawl the initial URL and collect its page data.
		var (
			pageSource   vdi.WebDriver
			htmlContent  string
			initialLinks []LinkItem
		)
		pageSource, htmlContent, initialLinks, tErr := processCtx.CrawlInitialURL(sel)
		if tErr != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "crawling initial URL: %v", err)
			processCtx.Status.EndTime = time.Now()
			processCtx.Status.CrawlingRunning.Store(3)
			processCtx.Status.PipelineRunning.Store(3)
			processCtx.Status.TotalErrors.Add(1)
			processCtx.Status.LastError = tErr.Error()
			return
		}
		processCtx.RefreshCrawlingTimer()
		_ = vdi.Refresh(processCtx) // Refresh the WebDriver session

		// Get screenshot of the page
		processCtx.TakeScreenshot(pageSource, args.Src.URL, processCtx.fpIdx)
		processCtx.RefreshCrawlingTimer()
		_ = vdi.Refresh(processCtx) // Refresh the WebDriver session

		// Initial links were collected with the initial page data. Keep the
		// existing refresh point that occurred after first-page link extraction.
		processCtx.RefreshCrawlingTimer()
		_ = vdi.Refresh(processCtx) // Refresh the WebDriver session

		// Add alternative_links to initial links:
		srcConfig := processCtx.srcCfg["crawling_config"]
		if srcConfig != nil {
			if crawlingConfig, ok := srcConfig.(map[string]interface{}); ok {
				// Check if there are any user-defined URL patterns to match
				if urlPatterns, ok := crawlingConfig["alternative_links"]; ok {
					if patterns, ok := urlPatterns.([]interface{}); ok {
						// Use the user-defined URL patterns
						for _, pattern := range patterns {
							if patternStr, ok := pattern.(string); ok {
								// Check if pattern is already in initialLinks
								found := false
								for _, link := range initialLinks {
									if link.Link == patternStr {
										found = true
										break
									}
								}
								if !found {
									pURL, _ := processCtx.wd.CurrentURL()
									link := LinkItem{
										PageURL:   pURL,
										PageLevel: 1,
										Link:      patternStr,
										ElementID: "",
									}
									// Add the user-defined link to the pageInfo.Links
									initialLinks = append(initialLinks, link)
									cmn.DebugMsg(cmn.DbgLvlDebug3, "[DEBUG-CrawlWebsite] Added user-defined link: %s", patternStr)
								}
							}
						}
					}
				}
			}
		}
		cmn.DebugMsg(cmn.DbgLvlDebug3, "[DEBUG-CrawlWebsite] Initial links extracted: %d", len(initialLinks))
		if processCtx.RefreshCrawlingTimer != nil {
			processCtx.RefreshCrawlingTimer()
		}
		_ = vdi.Refresh(processCtx) // Refresh the WebDriver session

		// Refresh the page
		tErr = processCtx.RefreshVDIConnection(sel)
		if tErr != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "refreshing VDI connection: %v", tErr)
			if processCtx != nil {
				if processCtx.Status != nil {
					processCtx.Status.EndTime = time.Now()
					processCtx.Status.CrawlingRunning.Store(3)
					processCtx.Status.PipelineRunning.Store(3)
					processCtx.Status.TotalErrors.Add(1)
					processCtx.Status.LastError = tErr.Error()
				}
			}
			return
		}

		// track workers used for NetInfo and HTTPInfo to wait for them later before indexing the network information
		usedWorkersFromPool := 0

		// Get network information
		processCtx.wgNetInfo.Add(1)
		usedWorkersFromPool++
		go func(ctx *ProcessContext) {
			defer ctx.wgNetInfo.Done()
			ctx.GetNetInfo(ctx.source.URL)
			_, tErr := ctx.IndexNetInfo(1)
			if tErr != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "indexing network information: %v", tErr)
			}
		}(processCtx)

		// Get HTTP header information
		if processCtx.config.HTTPHeaders.Enabled {
			processCtx.wgNetInfo.Add(1)
			usedWorkersFromPool++
			go func(ctx *ProcessContext, htmlContent string) {
				defer ctx.wgNetInfo.Done()
				ctx.GetHTTPInfo(ctx.source.URL, htmlContent)
				_, tErr := ctx.IndexNetInfo(2)
				if tErr != nil {
					cmn.DebugMsg(cmn.DbgLvlError, "indexing HTTP information: %v", tErr)
				}
			}(processCtx, htmlContent)
		} else {
			processCtx.Status.HTTPInfoRunning.Store(2)
		}

		// Crawl the website
		allLinks := initialLinks // links extracted from the initial page
		var currentDepth int
		maxDepth := checkMaxDepth(processCtx.config.Crawler.MaxDepth) // set a maximum depth for crawling
		newLinksFound := int32(len(initialLinks))                     // nolint:gosec // this is generated by the code and handled by the code
		processCtx.Status.TotalLinks.Store(newLinksFound)
		if processCtx.source.Restricted != 0 {
			// Restriction level is higher than 0, so we need to crawl the website
			for (currentDepth < maxDepth) && (newLinksFound > 0) {
				// Create a channel to enqueue jobs
				jobs := make(chan LinkItem, len(allLinks))
				// Create a channel to collect errors
				errChan := make(chan error, config.Crawler.Workers-usedWorkersFromPool)

				// Launch worker goroutines
				for w := 1; w <= config.Crawler.Workers-usedWorkersFromPool; w++ {
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
				cmn.DebugMsg(cmn.DbgLvlDebug2, "[DEBUG-CrawlWebsite] Enqueued jobs: %d", len(allLinks))

				// Wait for workers to finish and collect new links
				cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-CrawlWebsite] Waiting for workers to finish...")
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
							processCtx.Status.CrawlingRunning.Store(3)
							processCtx.Status.PipelineRunning.Store(3)
							processCtx.Status.TotalErrors.Add(1)
							processCtx.Status.LastError = err.Error()

							// Log the critical error and return to stop processing
							cmn.DebugMsg(cmn.DbgLvlError, "encountered "+errCriticalError+": %v. Stopping crawling for Source: %d", err, processCtx.source.ID)
							return
						}
					}
				}
				cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-CrawlWebsite] All workers finished.")

				// Prepare for the next iteration
				func() {
					processCtx.linksMutex.Lock()
					defer processCtx.linksMutex.Unlock()
					if len(processCtx.newLinks) > 0 {
						// If MaxLinks is set, limit the number of new links
						if processCtx.config.Crawler.MaxLinks > 0 && ((processCtx.Status.TotalPages.Load() + int32(len(processCtx.newLinks))) > int32(processCtx.config.Crawler.MaxLinks)) { // nolint:gosec // this is generated by the code and handled by the code
							linksToCrawl := int32(processCtx.config.Crawler.MaxLinks) - processCtx.Status.TotalPages.Load() // nolint:gosec // this is generated by the code and handled by the code
							if linksToCrawl <= 0 {
								// Remove all new links
								processCtx.newLinks = []LinkItem{}
							} else {
								processCtx.newLinks = processCtx.newLinks[:linksToCrawl]
							}
						}
						newLinksFound = int32(len(processCtx.newLinks)) // nolint:gosec // this is generated by the code and handled by the code
						processCtx.Status.TotalLinks.Add(newLinksFound)
						allLinks = processCtx.newLinks
					} else {
						newLinksFound = 0
					}
					processCtx.newLinks = []LinkItem{} // reset newLinks
				}()

				// Increment the current depth
				currentDepth++
				processCtx.Status.CurrentDepth.Add(1)
				if processCtx.config.Crawler.MaxDepth == 0 {
					maxDepth = currentDepth + 1
				}
			}
		}

		if processCtx.config.Crawler.ResetCookiesPolicy == cmn.AlwaysStr {
			// Reset cookies after crawling
			func() {
				processCtx.getURLMutex.Lock()
				defer processCtx.getURLMutex.Unlock()
				_ = ResetSiteSession(processCtx)
			}()
		}

		// Return the Selenium instance to the channel
		processCtx.Status.CrawlingRunning.Store(2)
		vdi.ReturnVDIInstance(args.WG, processCtx, &sel, releaseVDI)

		// Index the network information
		processCtx.wgNetInfo.Wait()

		// Pipeline has completed
		processCtx.Status.EndTime = time.Now()
		processCtx.Status.PipelineRunning.Store(2)
	}()

	// Wait for the crawling process to finish or timeout
	select {
	case <-timeoutTimer.C:
		// Timeout hit
		cmn.DebugMsg(cmn.DbgLvlError, "Crawling timed out for source: %s", args.Src.URL)
		processCtx.Status.PipelineRunning.Store(3)
		processCtx.Status.CrawlingRunning.Store(3)
		processCtx.Status.LastError = "timeout during crawling"
		UpdateSourceState(args.DB, args.Src.URL, errors.New("timeout during crawling"))
		err = errors.New("timeout")
		return
	case <-done:
		// Crawling completed successfully
		cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-CrawlWebsite] Crawling completed for source: %s", args.Src.URL)
	}
}

func parseProcessingTimeout(timeoutStr string) time.Duration {
	timeoutStr = strings.TrimSpace(strings.ToLower(timeoutStr))

	if timeoutStr == "" {
		cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-parseProcessingTimeout] Processing timeout is empty, using default 20m")
		return 20 * time.Minute // Default fallback
	}
	cmn.DebugMsg(cmn.DbgLvlDebug2, "[DEBUG-parseProcessingTimeout] Processing timeout set to: %s", timeoutStr)

	// Normalize known time units
	replacements := []struct {
		old string
		new string
	}{
		{" minutes", "m"},
		{" minute", "m"},
		{" mins", "m"},
		{" min", "m"},
		{" hours", "h"},
		{" hour", "h"},
		{" hrs", "h"},
		{" hr", "h"},
		{" seconds", "s"},
		{" second", "s"},
		{" secs", "s"},
		{" sec", "s"},
	}

	for _, i := range replacements {
		timeoutStr = strings.ReplaceAll(timeoutStr, i.old, i.new)
	}

	// Handle days, weeks, months, years
	// Note: months and years are approximate (30 and 365 days)
	unitMultipliers := map[string]time.Duration{
		"s":       1 * time.Second,
		"sec":     1 * time.Second,
		"secs":    1 * time.Second,
		"second":  1 * time.Second,
		"seconds": 1 * time.Second,
		"minute":  1 * time.Minute,
		"minutes": 1 * time.Minute,
		"mutes":   1 * time.Minute,
		"m":       1 * time.Minute,
		"h":       1 * time.Hour,
		"hr":      1 * time.Hour,
		"hrs":     1 * time.Hour,
		"hour":    1 * time.Hour,
		"hours":   1 * time.Hour,
		"d":       24 * time.Hour,
		"day":     24 * time.Hour,
		"days":    24 * time.Hour,
		"w":       7 * 24 * time.Hour,
		"week":    7 * 24 * time.Hour,
		"weeks":   7 * 24 * time.Hour,
		"mo":      30 * 24 * time.Hour,
		"month":   30 * 24 * time.Hour,
		"months":  30 * 24 * time.Hour,
		"y":       365 * 24 * time.Hour,
		"year":    365 * 24 * time.Hour,
		"years":   365 * 24 * time.Hour,
	}

	// Match numeric value followed by unit
	re := regexp.MustCompile(`^(\d+)\s*(s|sec|secs|second|seconds|m|minute|minutes|mutes|h|hr|hrs|hour|hours|day|days|week|weeks|month|months|year|years|d|w|mo|y)$`)
	if matches := re.FindStringSubmatch(timeoutStr); matches != nil {
		value, err := strconv.Atoi(matches[1])
		if err == nil {
			unit := matches[2]
			if mult, ok := unitMultipliers[unit]; ok {
				dur := time.Duration(value) * mult
				return clampDuration(dur, timeoutStr)
			}
		}
	}

	// Fallback to native duration parsing
	dur, err := time.ParseDuration(timeoutStr)
	if err != nil || dur <= 0 {
		cmn.DebugMsg(cmn.DbgLvlWarn, "Invalid timeout format: %s, falling back to 20m", timeoutStr)
		dur = 20 * time.Minute
	}

	return clampDuration(dur, timeoutStr)
}

func clampDuration(dur time.Duration, originalInput string) time.Duration {
	if dur < 0 {
		cmn.DebugMsg(cmn.DbgLvlWarn, "Negative timeout duration: %s, falling back to 20m", originalInput)
		dur = 20 * time.Minute
	}
	if dur > 24*time.Hour {
		cmn.DebugMsg(cmn.DbgLvlWarn, "Timeout duration too large: %s, falling back to 24h", originalInput)
		dur = 24 * time.Hour
	}
	if dur < 1*time.Second {
		cmn.DebugMsg(cmn.DbgLvlWarn, "Timeout duration too small: %s, falling back to 30s", originalInput)
		dur = 30 * time.Second
	}
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
	defer ctx.closeSession.Unlock()

	if ctx.pStatus != 1 {
		cmn.DebugMsg(cmn.DbgLvlDebug2, "[DEBUG-closeSession] Pipeline already completed for source: %v", ctx.source.ID)
		return
	}

	// Allow a new sources batch job to be processed (if any)
	// in the caller:
	if ctx.WG != nil {
		defer ctx.WG.Done()
	} else {
		if args.WG != nil {
			defer ctx.WG.Done()
		}
	}

	// Release VDI connection
	// (this allows the next source to be processed, if any, in this batch job)
	vdi.ReturnVDIInstance(args.WG, ctx, sel, releaseVDI)
	ctx.SetVDIReturnedFlag(true)

	// Signal pipeline completion
	if ctx.Status.PipelineRunning.Load() == 1 || err != nil {
		ctx.Status.PipelineRunning.Store(3)
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
	defer ctx.linksMutex.Unlock()
	ctx.newLinks = nil         // Clear the slice to release memory
	ctx.visitedLinks = nil     // Clear the map to release memory
	ctx.CollectedCookies = nil // Clear cookies

	ctx.pStatus = 10 // Processing completed
	cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-closeSession] Returning from crawling a source.")
}

// CreateCrawlCompletedEvent creates a new event in the database to indicate that the crawl has completed
func CreateCrawlCompletedEvent(db cdb.Handler, sourceID uint64, status *Status) error {
	lStatus := NonAtomicStatus{
		PipelineID:      status.PipelineID,
		SourceID:        status.SourceID,
		VDIID:           status.VDIID,
		Source:          status.Source,
		TotalPages:      status.TotalPages.Load(),
		TotalLinks:      status.TotalLinks.Load(),
		TotalSkipped:    status.TotalSkipped.Load(),
		TotalDuplicates: status.TotalDuplicates.Load(),
		TotalErrors:     status.TotalErrors.Load(),
		TotalScraped:    status.TotalScraped.Load(),
		TotalActions:    status.TotalActions.Load(),
		TotalFuzzing:    status.TotalFuzzing.Load(),
		StartTime:       status.StartTime,
		EndTime:         status.EndTime,
		CurrentDepth:    status.CurrentDepth.Load(),
		LastWait:        status.LastWait,
		LastDelay:       status.LastDelay,
		LastError:       status.LastError,
		// Flags values: 0 - Not started yet, 1 - Running, 2 - Completed, 3 - Error
		NetInfoRunning:  status.NetInfoRunning.Load(),  // Flag to check if network info is already gathered
		HTTPInfoRunning: status.HTTPInfoRunning.Load(), // Flag to check if HTTP info is already gathered
		PipelineRunning: status.PipelineRunning.Load(), // Flag to check if the pipeline is still running
		CrawlingRunning: status.CrawlingRunning.Load(), // Flag to check if the crawling is still running
		DetectedState:   status.DetectedState.Load(),   // Detected state of the source
	}

	// Convert Status into a JSON string
	statusJSON, err := json.Marshal(lStatus)
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
		SourceID:  sourceID,
		Type:      "crawl_completed",
		Severity:  cdb.EventSeverityInfo,
		ExpiresAt: time.Now().Add(2 * time.Minute).Format(time.RFC3339),
		Details:   statusMap,
	}

	// Use PostgreSQL placeholders ($1, $2, etc.) and include event_timestamp
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_, err = cdb.CreateEvent(ctx, &db, event)
	cancel()
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

// GetNetInfo is responsible for gathering network information for a Source
func (ctx *ProcessContext) GetNetInfo(_ string) {
	ctx.Status.NetInfoRunning.Store(1)

	// Create a new NetInfo instance
	ctx.ni = &neti.NetInfo{}
	c := ctx.config.NetworkInfo
	ctx.ni.Config = &c

	// Call GetNetInfo to retrieve network information
	cmn.DebugMsg(cmn.DbgLvlDebug, "Gathering network information for %s...", ctx.source.URL)
	err := ctx.ni.GetNetInfo(ctx.source.URL)
	ctx.Status.NetInfoRunning.Store(2)

	// Check for errors
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "GetNetInfo(%s) returned an error: %v", ctx.source.URL, err)
		ctx.Status.NetInfoRunning.Store(3)
		return
	}
}

// GetHTTPInfo is responsible for gathering HTTP header information for a Source
func (ctx *ProcessContext) GetHTTPInfo(url string, htmlContent string) {
	ctx.Status.HTTPInfoRunning.Store(1)
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
	ctx.Status.HTTPInfoRunning.Store(2)

	// Check for errors
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "while retrieving HTTP Headers Information: %v", ctx.source.URL, err)
		ctx.Status.HTTPInfoRunning.Store(3)
		return
	}
}

// IndexPage is responsible for indexing a crawled page in the database
func (ctx *ProcessContext) IndexPage(pageInfo *PageInfo) (uint64, error) {
	(*pageInfo).sourceID = ctx.source.ID
	(*pageInfo).Config = &ctx.config
	return indexPage(ctx, ctx.source.URL, pageInfo)
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
func indexPage(ctx *ProcessContext, url string, pageInfo *PageInfo) (uint64, error) {
	if pageInfo == nil {
		return 0, errors.New("pageInfo cannot be nil")
	}

	if ctx == nil {
		return 0, errors.New("process context cannot be nil")
	}

	if (url == "") || (len(strings.TrimSpace(url)) == 0) {
		return 0, errors.New("url cannot be empty")
	}

	pageInfo.URL = url

	db := *ctx.db

	// Before updating the source state, check if the database connection is still alive
	err := db.CheckConnection(config)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, dbConnCheckErr, err)
		return 0, err
	}
	cmn.DebugMsg(cmn.DbgLvlDebug4, "[DEBUG-Indexing] Database ready, Indexing page: %s", url)

	// Start a transaction
	tx, err := db.Begin()
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug4, "[DEBUG-Indexing] Error starting transaction: %v", err)
		cmn.DebugMsg(cmn.DbgLvlError, "starting transaction: %v", err)
		return 0, err
	}
	cmn.DebugMsg(cmn.DbgLvlDebug4, "[DEBUG-Indexing] Transaction started...")

	// Insert or update the page in SearchIndex
	indexID, err := insertOrUpdateSearchIndex(tx, url, pageInfo)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug4, "[DEBUG-Indexing] Error inserting or updating SearchIndex: %v", err)
		cmn.DebugMsg(cmn.DbgLvlError, "inserting or updating SearchIndex: %v", err)
		rollbackTransaction(tx)
		return 0, err
	}
	cmn.DebugMsg(cmn.DbgLvlDebug4, "[DEBUG-Indexing] SearchIndex updated with indexID: %d", indexID)

	if ctx.config.Crawler.RefreshContent {
		// We need to delete existing webObjects for this indexID
		err = deleteWebObjects(tx, indexID)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlDebug4, "[DEBUG-Indexing] Error deleting existing WebObjects for indexID %d: %v", indexID, err)
			cmn.DebugMsg(cmn.DbgLvlError, "deleting existing WebObjects: %v", err)
			rollbackTransaction(tx)
			return 0, err
		}
	}

	// Insert or update the page in WebObjects
	objID, detailsJSON, err := insertOrUpdateWebObjects(tx, indexID, pageInfo)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug4, "[DEBUG-Indexing] Error inserting or updating WebObjects: %v", err)
		cmn.DebugMsg(cmn.DbgLvlError, "inserting or updating WebObjects: %v", err)
		rollbackTransaction(tx)
		return 0, err
	}
	cmn.DebugMsg(cmn.DbgLvlDebug4, "[DEBUG-Indexing] WebObjects updated with indexID: %d", indexID)

	// Index object attributes for WebObjet
	err = indexObjectAttributes(tx, objID, detailsJSON, ctx.GetConfig())
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug4, "[DEBUG-Indexing] Error inserting or updating Object Attributes: %v", err)
		rollbackTransaction(tx)
		return 0, err
	}
	cmn.DebugMsg(cmn.DbgLvlDebug4, "[DEBUG-Indexing] Object Attributes indexed for objectID: %d", objID)

	// Insert MetaTags
	if pageInfo.Config.Crawler.CollectMetaTags {
		err = insertMetaTags(tx, indexID, pageInfo.MetaTags)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlDebug4, "[DEBUG-Indexing] Error inserting meta tags for indexID: %d, error: %v", indexID, err)
			cmn.DebugMsg(cmn.DbgLvlError, "inserting meta tags: %v", err)
			rollbackTransaction(tx)
			return 0, err
		}
	}
	cmn.DebugMsg(cmn.DbgLvlDebug4, "[DEBUG-Indexing] MetaTags inserted for indexID: %d", indexID)

	// Insert into KeywordIndex
	if pageInfo.Config.Crawler.CollectKeywords {
		err = insertKeywords(tx, indexID, pageInfo)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlDebug4, "[DEBUG-Indexing] Error inserting keywords for indexID: %d, error: %v", indexID, err)
			cmn.DebugMsg(cmn.DbgLvlError, "inserting keywords: %v", err)
			rollbackTransaction(tx)
			return 0, err
		}
	}
	cmn.DebugMsg(cmn.DbgLvlDebug4, "[DEBUG-Indexing] Keywords inserted for indexID: %d", indexID)

	// Commit the transaction
	err = commitTransaction(tx)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug4, "[DEBUG-Indexing] Error committing transaction: %v", err)
		cmn.DebugMsg(cmn.DbgLvlError, dbConnTransErr, err)
		rollbackTransaction(tx)
		return 0, err
	}
	cmn.DebugMsg(cmn.DbgLvlDebug4, "[DEBUG-Indexing] Transaction committed successfully.")

	// Return the index ID
	return indexID, nil
}

func indexObjectAttributes(
	tx *sql.Tx,
	objectID int64,
	detailsJSON []byte,
	currCfg *cfg.Config,
) error {

	if currCfg == nil || currCfg.AttributesIndexing.IsEmpty() {
		return nil
	}

	var data map[string]interface{}
	if err := json.Unmarshal(detailsJSON, &data); err != nil {
		return err
	}

	attrs := currCfg.AttributesIndexing.WebObject

	// --- build lookup ---
	attrMap := make(map[string]cfg.AttributeDefinition)
	for _, a := range attrs {
		attrMap[a.Key] = a
	}

	// --- execution state ---
	executed := make(map[string]bool)

	// --- queue ---
	queue := []cfg.AttributeDefinition{}

	// --- seed: only index=true ---
	for _, a := range attrs {
		if a.Index {
			queue = append(queue, a)
		}
	}

	// --- process queue ---
	for len(queue) > 0 {
		attr := queue[0]
		queue = queue[1:]

		id := attr.Key + "|" + attr.Path
		if executed[id] {
			continue
		}
		executed[id] = true

		// --- extract ---
		var values []interface{}

		if attr.IsCommandPath() {
			ctxCmd := CommandContext{
				ObjectID: objectID,
				Data:     data,
			}
			values = ExecuteCommand(attr, ctxCmd)
		} else {
			tokens := GetParsedPath(attr.Path)
			values = ExtractWithTokens(data, tokens)
		}

		if len(values) == 0 {
			continue
		}

		cmn.DebugMsg(cmn.DbgLvlDebug4,
			"[DEBUG-Indexing] Attr key=%s extracted=%d",
			attr.Key, len(values),
		)

		// --- insert values ---
		for _, v := range values {
			raw := ToString(v)
			if raw == "" {
				continue
			}

			normalized := ApplyNormalizers(raw, attr.Normalizers)
			if normalized == "" {
				continue
			}

			hash := hashString(normalized)

			err := insertObjectAttribute(
				tx,
				objectID,
				attr.Key,
				raw,
				normalized,
				hash,
				attr.IndexType,
			)
			if err != nil {
				return err
			}
		}

		// --- trigger RunAlso ---
		for _, depKey := range attr.RunAlso {
			if dep, ok := attrMap[depKey]; ok {
				queue = append(queue, dep)
			}
		}
	}

	return nil
}

func hashString(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func insertObjectAttribute(
	tx *sql.Tx,
	objectID int64,
	key string,
	raw string,
	normalized string,
	hash string,
	attrType string,
) error {

	_, err := tx.Exec(`
		INSERT INTO ObjectAttributes
			(object_id, object_type, attribute_key, attribute_value, normalized_value, value_hash, attribute_type)
		VALUES ($1, 'webobject', $2, $3, $4, $5, $6)
		ON CONFLICT DO NOTHING
	`,
		objectID,
		key,
		raw, // maps to attribute_value
		normalized,
		hash,
		attrType,
	)

	return err
}

// indexNetInfo indexes the network information of a source in the database
func indexNetInfo(db cdb.Handler, url string, pageInfo *PageInfo, flags int) (uint64, error) {
	// Acquire a lock to ensure that only one goroutine is accessing the database
	//indexPageMutex.Lock()
	//defer indexPageMutex.Unlock()

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
		SET
			title = COALESCE(NULLIF(BTRIM(EXCLUDED.title), ''), SearchIndex.title),
    		summary = COALESCE(NULLIF(BTRIM(EXCLUDED.summary), ''), SearchIndex.summary),

    		detected_lang = COALESCE(NULLIF(BTRIM(EXCLUDED.detected_lang), ''), SearchIndex.detected_lang),
    		detected_type = COALESCE(NULLIF(BTRIM(EXCLUDED.detected_type), ''), SearchIndex.detected_type),

			last_updated_at = NOW()
		RETURNING index_id`,
		url, strLeft((*pageInfo).Title, 255), (*pageInfo).Summary,
		strLeft((*pageInfo).DetectedLang, 8), strLeft((*pageInfo).DetectedType, 255)).Scan(&indexID)
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

// deleteWebObjects deletes web object entries associated with a given index ID from the database.
func deleteWebObjects(tx *sql.Tx, indexID uint64) error {
	_, err := tx.Exec(`
		DELETE FROM WebObjects
		WHERE object_id IN (
			SELECT object_id
			FROM WebObjectsIndex
			WHERE index_id = $1
		)`, indexID)
	return err
}

var nullEscape = regexp.MustCompile(`\\u0000`)

// insertOrUpdateWebObjects inserts or updates a web object entry in the database.
// It takes a transaction object (tx), the index ID of the page (indexID), and the page information (pageInfo).
// It returns an error, if any.
func insertOrUpdateWebObjects(tx *sql.Tx, indexID uint64, pageInfo *PageInfo) (int64, []byte, error) {
	// Prepare the "Details" field for insertion
	details := make(map[string]any)
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
		return 0, nil, err
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
				return 0, nil, err
			}
			doc2 := make(map[string]interface{})
			err = json.Unmarshal(scrapedItemJSON, &doc2)
			if err != nil {
				return 0, nil, err
			}

			// Add scrapedItemJSON to ScrapedJSON document
			mergeMaps(scrapedDoc1, doc2)
		}
		// Wrap ScrapedDoc1 in a "scraped" tag
		scrapedDoc1 = map[string]interface{}{"scraped_data": scrapedDoc1}

		// Convert the scraped data to JSON
		scrapedDataJSON, err = json.Marshal(scrapedDoc1)
		if err != nil {
			return 0, nil, err
		}

		// Combine the scraped data and the details
		if len(scrapedDataJSON) > 0 {
			var doc1 map[string]any
			var doc2 map[string]any

			err := json.Unmarshal(detailsJSON, &doc1)
			if err != nil {
				return 0, nil, err
			}
			err = json.Unmarshal(scrapedDataJSON, &doc2)
			if err != nil {
				return 0, nil, err
			}

			// Merges doc2 into doc1
			mergeMaps(doc1, doc2)

			detailsJSON, err = normalizeJSON(doc1)
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "normalizing JSON: %v", err)
				return 0, nil, err
			}
		}
		// For debugging purposes:
		/*
			// Extract the "links" tag from detailsJSON
			var processedDetails map[string]interface{}
			err = json.Unmarshal(detailsJSON, &processedDetails)
			if err != nil {
				return 0, nil, err
			}
			// Print the links tag
			fmt.Printf("Processed Links: %v\n", processedDetails["links"])
			fmt.Printf("-------------------------\n")
			fmt.Printf("Received Links: %v\n", pageInfo.Links)
		*/
		//fmt.Println(string(detailsJSON))
	}

	// Make sure detailsJSON is absolutely valid for JSONB objects:
	detailsJSON = bytes.ToValidUTF8(detailsJSON, []byte{})
	detailsJSON = removeSurrogateEscapes(detailsJSON)
	detailsJSON = nullEscape.ReplaceAll(detailsJSON, []byte(""))

	// Extract Scraped Data and Detected Tech from detailsJSON
	htmlContent := bytes.ToValidUTF8([]byte((*pageInfo).HTML), []byte{})
	textContent := bytes.ToValidUTF8([]byte((*pageInfo).BodyText), []byte{})

	// Calculate the SHA256 hash of the body text
	hasher := sha256.New()
	bytesToHash := []byte{}
	if len(textContent) > 0 {
		bytesToHash = []byte(textContent)
	} else if len(htmlContent) > 0 {
		bytesToHash = []byte(htmlContent)
	} else {
		hasher.Write([]byte(detailsJSON))
	}
	bytesToHash = append(bytesToHash, scrapedDataJSON...)
	bytesToHash = append(bytesToHash, detectedTechJSON...)
	hasher.Write(bytesToHash)
	hash := hex.EncodeToString(hasher.Sum(nil))

	var objID int64

	// Step 1: Insert into WebObjects
	err = tx.QueryRow(`
		WITH upsert AS (
		INSERT INTO WebObjects (object_hash, object_content, object_html, details)
		VALUES ($1, $2, $3, $4::jsonb)
		ON CONFLICT (object_hash) DO UPDATE
		SET
			object_content = COALESCE(
				NULLIF(BTRIM(EXCLUDED.object_content), ''),
				WebObjects.object_content
			),
			details = COALESCE(
				NULLIF(EXCLUDED.details, '{}'::jsonb),
				WebObjects.details
			)
		RETURNING object_id
	)
	SELECT object_id
	FROM upsert
	FOR UPDATE;`, hash, textContent, htmlContent, detailsJSON).Scan(&objID)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "inserting into WebObjectsIndex: %v", detailsJSON)
		return objID, detailsJSON, err
	}

	// Step 2: Insert into WebObjectsIndex for the associated sourceID
	_, err = tx.Exec(`
		INSERT INTO WebObjectsIndex (index_id, object_id)
		VALUES ($1, $2)
		ON CONFLICT (index_id, object_id) DO NOTHING`, indexID, objID)
	if err != nil {
		return objID, detailsJSON, err
	}

	return objID, detailsJSON, nil
}

var surrogateEscape = regexp.MustCompile(`\\u[dD][89a-fA-F][0-9a-fA-F]{2}`)

func removeSurrogateEscapes(jsonBytes []byte) []byte {
	return surrogateEscape.ReplaceAll(jsonBytes, []byte(""))
}

func normalizeJSON(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	// Remove invalid UTF-8 sequences
	raw = bytes.ToValidUTF8(raw, []byte{})

	// Re-parse and re-marshal to ensure JSON validity
	var clean interface{}
	if err := json.Unmarshal(raw, &clean); err != nil {
		return nil, err
	}

	return json.Marshal(clean)
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
		SET
			details = COALESCE(
				NULLIF(EXCLUDED.details, '{}'::jsonb),
				NetInfo.details
			)
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
		SET
			details = COALESCE(
				NULLIF(EXCLUDED.details, '{}'::jsonb),
				HTTPInfo.details
			)
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

func truncateUTF8(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes])
}

// insertMetaTags inserts meta tags into the database for a given index ID.
// It takes a transaction, index ID, and a map of meta tags as parameters.
// Each meta tag is inserted into the MetaTags table with the corresponding index ID, name, and content.
// Returns an error if there was a problem executing the SQL statement.
func insertMetaTags(tx *sql.Tx, indexID uint64, metaTags []MetaTag) error {
	for _, metatag := range metaTags {
		var name string
		if len(metatag.Name) > 256 {
			name = truncateUTF8(metatag.Name, 256)
		} else {
			name = metatag.Name
		}
		var content string
		if len(metatag.Content) > 1024 {
			content = truncateUTF8(metatag.Content, 1024)
		} else {
			content = metatag.Content
		}

		if !utf8.ValidString(name) {
			name = strings.ToValidUTF8(name, "")
		}
		if !utf8.ValidString(content) {
			content = strings.ToValidUTF8(content, "")
		}

		var metatagID int64

		// Try to find the metatag ID first
		err := tx.QueryRow(`
            SELECT metatag_id FROM MetaTags WHERE name = $1 AND content = $2;`, name, content).Scan(&metatagID)

		// If not found, insert the new metatag and get its ID
		if err == sql.ErrNoRows {
			err = tx.QueryRow(`
                INSERT INTO MetaTags (name, content)
                VALUES ($1, $2)
                ON CONFLICT (name, content) DO NOTHING
                RETURNING metatag_id;`, name, content).Scan(&metatagID)
			if err != nil {
				return err // Handle error appropriately
			}
		}

		if err != nil {
			// One valid case: DO NOTHING means RETURNING finds no row
			// So we need to handle that gracefully
			if err == sql.ErrNoRows {
				// Retrieve existing ID
				err = tx.QueryRow(`
					SELECT metatag_id FROM MetaTags
					WHERE name = $1 AND content = $2
				`,
					strings.TrimSpace(name),
					strings.TrimSpace(content),
				).Scan(&metatagID)
			}
			if err != nil {
				return err
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

func insertKeyword(tx *sql.Tx, keyword string) (int, error) {
	if len(keyword) > 256 {
		keyword = keyword[:256]
	}
	keyword = strings.TrimSpace(keyword)
	if !utf8.ValidString(keyword) {
		keyword = strings.ToValidUTF8(keyword, "")
	}
	if keyword == "" {
		return 0, fmt.Errorf("Invalid keyword")
	}

	keyword = strings.ToLower(norm.NFC.String(keyword))

	// Serialize per keyword
	if _, err := tx.Exec(`
		SELECT pg_advisory_xact_lock(hashtext($1))
	`, keyword); err != nil {
		return 0, err
	}

	var keywordID int
	err := tx.QueryRow(`
		INSERT INTO Keywords (keyword)
		VALUES ($1)
		ON CONFLICT (keyword)
		DO UPDATE SET keyword = EXCLUDED.keyword
		RETURNING keyword_id;
	`, keyword).Scan(&keywordID)

	if err != nil {
		return 0, err
	}

	return keywordID, nil
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))

	for _, s := range in {
		// Trim only prefix/suffix whitespace
		s = strings.TrimSpace(s)

		// Ensure valid UTF-8 (otherwise normalization can behave oddly)
		if !utf8.ValidString(s) {
			s = strings.ToValidUTF8(s, "")
		}
		if s == "" {
			continue
		}

		// Canonical Unicode normalization:
		// makes "é" (U+00E9) and "e\u0301" equivalent.
		s = norm.NFC.String(s)

		if s == "" {
			continue
		}

		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}

	return out
}

// insertKeywords inserts keywords extracted from a web page into the database.
// It takes a transaction `tx` and a database connection `db` as parameters.
// The `indexID` parameter represents the ID of the index associated with the keywords.
// The `pageInfo` parameter contains information about the web page.
// It returns an error if there is any issue with inserting the keywords into the database.
func insertKeywords(tx *sql.Tx, indexID uint64, pageInfo *PageInfo) error {
	cmn.DebugMsg(cmn.DbgLvlDebug4, "[DEBUG-Indexing] Inserting keywords for indexID: %d", indexID)

	// Filter duplicated keywords
	pageInfo.Keywords = uniqueStrings(pageInfo.Keywords)

	// Sort keywords to ensure consistent insertion order
	sort.Strings(pageInfo.Keywords)

	for _, kw := range pageInfo.Keywords {
		if kw == "" {
			continue
		}

		keywordID, err := insertKeyword(tx, kw)
		if err != nil {
			return err
		}

		_, err = tx.Exec(`
			INSERT INTO KeywordIndex (keyword_id, index_id)
			VALUES ($1, $2)
			ON CONFLICT (keyword_id, index_id) DO NOTHING;
		`, keywordID, indexID)
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

/*
// insertKeywordWithRetries is responsible for storing the extracted keywords in the database
// It's written to be efficient and avoid deadlocks, but this right now is not required
// because indexPage uses a mutex to ensure that only one goroutine is indexing a page
// at a time. However, when implementing multiple transactions in indexPage, this function
// will be way more useful than it is now.
func insertKeywordWithRetries(tx *sql.Tx, keyword string) (int, error) {
	const maxRetries = 3
	var keywordID int

	if len(keyword) > 256 {
		keyword = keyword[:255]
	}

	for i := 0; i < maxRetries; i++ {
		err := tx.QueryRow(`INSERT INTO Keywords (keyword)
                            VALUES ($1) ON CONFLICT (keyword) DO NOTHING
                            RETURNING keyword_id`, keyword).
			Scan(&keywordID)
		if err != nil {
			if err == sql.ErrNoRows {
				// Keyword already exists, fetch its ID
				err = tx.QueryRow(`
					SELECT keyword_id FROM Keywords WHERE keyword = $1
				`, strings.TrimSpace(keyword)).Scan(&keywordID)
			}
		}

		if err != nil {
			if strings.Contains(err.Error(), "deadlock detected") {
				if i == maxRetries-1 {
					cmn.DebugMsg(cmn.DbgLvlError, "Failed to insert keyword after retries: '%s', %v", keyword, err)
					cmn.DebugMsg(cmn.DbgLvlDebug4, "[DEBUG-Indexing] Failed to insert keyword after retries: '%s', %v", keyword, err)
					return 0, err
				}
				time.Sleep(time.Duration(i) * 100 * time.Millisecond) // Exponential backoff
				continue
			}
			return 0, err
		}
		return keywordID, nil
	}
	cmn.DebugMsg(cmn.DbgLvlDebug4, "[DEBUG-Indexing] Failed to insert keyword after retries: '%s'", keyword)
	return 0, fmt.Errorf("failed to insert keyword after retries: %s", keyword)
}
*/

func isDBSafeText(v any) bool {
	cmn.DebugMsg(cmn.DbgLvlDebug5, "XHR response_body dynamic type: %T", v)

	switch x := v.(type) {
	case nil:
		return true

	case string:
		return checkTextBytes([]byte(x))

	case []byte:
		return checkTextBytes(x)

	case json.RawMessage:
		return checkTextBytes([]byte(x))

	case *string:
		if x == nil {
			return true
		}
		return checkTextBytes([]byte(*x))

	case *json.RawMessage:
		if x == nil {
			return true
		}
		return checkTextBytes([]byte(*x))

	case map[string]any:
		for _, v := range x {
			if !isDBSafeText(v) {
				return false
			}
		}
		return true

	case []any:
		for _, v := range x {
			if !isDBSafeText(v) {
				return false
			}
		}
		return true

	default:
		// numbers, bools, structs, etc. are DB-safe
		return true
	}
}
func checkTextBytes(b []byte) bool {
	// TEXT / JSONB cannot contain NUL bytes
	if bytes.IndexByte(b, 0x00) != -1 {
		return false
	}

	// JSON text must be valid UTF-8
	if !utf8.Valid(b) {
		return false
	}

	return true
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
	if strings.HasSuffix(u, "://") && classifySourceProtocol(u) == SourceProtocolWeb {
		return false
	}

	// Parse the URL and check for errors
	_, err := url.ParseRequestURI(u)
	return err == nil
}

// IsValidURIProtocol checks if the URI has a valid protocol.
func IsValidURIProtocol(u string) bool {
	return classifySourceProtocol(u) == SourceProtocolWeb
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
	state := newLifecycleRuntimeState(20)
	for _, rule := range ctx.re.GetAllCrawlingRules() {
		lnkSet, err := FuzzURLWithLifecycle(ctx, &ctx.wd, state, url, rule, int(ctx.Status.CurrentDepth.Load()))
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

// worker is the worker function that is responsible for crawling a page
func worker(processCtx *ProcessContext, id int, jobs chan LinkItem) error {
	var skippedURLs []LinkItem
	var err error

	wid := processCtx.GetContextID() + "_" + strconv.Itoa(id)

	// Loop over the jobs channel and process each job
	for url := range jobs {
		if processCtx.Status.PipelineRunning.Load() > 1 {
			cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-Worker] %s: Stopping worker due to pipeline shutdown\n", wid)
			return nil // We return here because the pipeline is shutting down!
		}
		// Check if the URL should be skipped
		if (processCtx.config.Crawler.MaxLinks > 0) && (processCtx.Status.TotalPages.Load() >= int32(processCtx.config.Crawler.MaxLinks)) { // nolint:gosec // Values are generated and handled by the code
			cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-Worker] %s: Stopping due reached max_links limit: %d\n", wid, processCtx.Status.TotalPages.Load())
			return nil // We return here because we reached the max_links limit!
		}

		// Recursive Mode
		urlLink := url.Link
		if strings.HasPrefix(url.Link, "/") {
			urlLink, _ = combineURLs(processCtx.source.URL, url.Link)
		}

		// Check if the URL should be skipped
		skip := skipURL(processCtx, wid, urlLink)
		if skip {
			processCtx.Status.TotalSkipped.Add(1)
			skippedURLs = append(skippedURLs, url)
			cmn.DebugMsg(cmn.DbgLvlDebug2, "[DEBUG-Worker] %s: URL '%s' being skipped due skipping rules\n", wid, url.Link)
			continue
		}
		if processCtx.visitedLinks[cmn.NormalizeURL(urlLink)] {
			// URL already visited
			processCtx.Status.TotalDuplicates.Add(1)
			cmn.DebugMsg(cmn.DbgLvlDebug2, "[DEBUG-Worker] %s: URL '%s' already visited\n", wid, url.Link)
			continue
		}

		// Check if the URL should be skipped
		if (processCtx.config.Crawler.MaxLinks > 0) && (processCtx.Status.TotalPages.Load() >= int32(processCtx.config.Crawler.MaxLinks)) { // nolint:gosec // Values are generated and handled by the code
			cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-Worker] %s: Stopping due reached max_links limit: %d\n", wid, processCtx.Status.TotalPages.Load())
			return nil // We return here because we reached the max_links limit!
		}

		// Check if we have already crawled this URL from another instance
		if processCtx.config.Crawler.PreventDuplicateURLs {
			alreadyCrawled, _ := cdb.IsURLKnown(urlLink, processCtx.db)
			if alreadyCrawled {
				cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-Worker] %s: URL '%s' already crawled by another worker\n", wid, url.Link)
				continue
			}
		}

		cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-Worker] %s: Preparing to start job %s\n", wid, url.Link)

		// Process the job
		if strings.ToLower(strings.TrimSpace(processCtx.config.Crawler.BrowsingMode)) == optBrowsingRecu {
			err = processJob(processCtx, wid, urlLink, skippedURLs)
		} else if strings.ToLower(strings.TrimSpace(processCtx.config.Crawler.BrowsingMode)) == optBrowsingRCRecu {
			// Right Click Recursive Mode
			err = rightClick(processCtx, wid, url)
		} else if strings.ToLower(strings.TrimSpace(processCtx.config.Crawler.BrowsingMode)) == optBrowsingHuman {
			// Human Mode
			// Find the <a> element that contains the URL and click it
			err = clickLink(processCtx, wid, url)
		} else {
			// Fuzzing Mode
			// Fuzzy works like recursive, however instead of extracting links from the page, it generates links based on the crawling rules
			err = processJob(processCtx, wid, urlLink, skippedURLs)
		}
		if processCtx.visitedLinks == nil {
			processCtx.visitedLinks = make(map[string]bool)
		}
		processCtx.visitedLinks[cmn.NormalizeURL(urlLink)] = true

		if err == nil {
			processCtx.Status.TotalPages.Add(1)
			cmn.DebugMsg(cmn.DbgLvlDebug4, "[DEBUG-Worker] %s: Returned to the main worker routine for '%s'\n", wid, url.Link)
		} else {
			processCtx.Status.TotalErrors.Add(1)
			if strings.Contains(err.Error(), errCriticalError) {
				cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-Worker] %s: Returned to the main worker routine for '%s' with a critical error: %v\n", wid, url.Link, err)
				return err
			}
			cmn.DebugMsg(cmn.DbgLvlDebug4, "[DEBUG-Worker] %s: Returned to the main worker routine for '%s' with an error: %v\n", wid, url.Link, err)
		}

		// Clear the skipped URLs
		skippedURLs = nil

		if (processCtx.config.Crawler.MaxLinks > 0) && (processCtx.Status.TotalPages.Load() >= int32(processCtx.config.Crawler.MaxLinks)) { // nolint:gosec // Values are generated and handled by the code
			cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-Worker] %s: Stopping due reached max_links limit: %d\n", wid, processCtx.Status.TotalPages.Load())
			break // We break here because we reached the max_links limit!
		}
	}

	return nil
}

func skipURL(processCtx *ProcessContext, id string, url string) bool {
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
		cmn.DebugMsg(cmn.DbgLvlDebug2, "[DEBUG-Worker] %s: Skipping URL '%s' due 'external' policy.\n", id, url)
		return true
	}

	// Check if the URL matches any of the Unwanted URLs:
	if processCtx.compiledUURLs != nil {
		for _, UURL := range processCtx.compiledUURLs {
			if UURL.MatchString(url) {
				cmn.DebugMsg(cmn.DbgLvlDebug2, "[DEBUG-Worker] %s: Skipping URL '%s' due unwanted URL pattern.\n", id, url)
				return true
			}
		}
	}

	// Check if the URL is the same as the Source URL (in which case skip it)
	if url == processCtx.source.URL {
		cmn.DebugMsg(cmn.DbgLvlDebug2, "[DEBUG-Worker] %s: Skipping URL '%s' as it is the same as the source URL\n", id, url)
		return true
	}

	// Check if the URL matches user defined patterns (negative or positive)
	if len(processCtx.userURLPatterns) > 0 {
		// Flag to track whether the URL should be skipped
		shouldSkip := false
		matches := 0

		for _, pattern := range processCtx.userURLPatterns {
			re := regexp.MustCompile(pattern)
			cmn.DebugMsg(cmn.DbgLvlDebug5, "[DEBUG-Worker] %s: Checking URL '%s' against user-defined pattern '%s'\n", id, url, pattern)
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
			cmn.DebugMsg(cmn.DbgLvlDebug2, "[DEBUG-Worker] %s: Skipping URL '%s' due to user-defined pattern\n", id, url)
			return true
		}

		// If we did not find any matches, skip the URL
		if matches == 0 {
			cmn.DebugMsg(cmn.DbgLvlDebug2, "[DEBUG-Worker] %s: Skipping URL '%s' due to no user-defined pattern matches\n", id, url)
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
func rightClick(processCtx *ProcessContext, id string, url LinkItem) error {
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
		_, _, err := getURLContent(url.PageURL, processCtx.wd, 0, processCtx, id)
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
	_, _ = vdiSleep(processCtx, delay)

	// Check current URL (because some Action Rules may change the URL)
	currentURL, _ := processCtx.wd.CurrentURL()

	cmn.DebugMsg(cmn.DbgLvlDebug5, "Worker %s: Had to open '%s' link in the same tab were we had: %s\n", id, url.Link, currentURL)

	// Execute any action rules after the link is opened
	processActionRules(&processCtx.wd, processCtx, currentURL)
	if processCtx.RefreshCrawlingTimer != nil {
		processCtx.RefreshCrawlingTimer()
	}

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
		publishDetectionResults(processCtx, currentURL, detectedTech)
	}
	if processCtx.RefreshCrawlingTimer != nil {
		processCtx.RefreshCrawlingTimer()
	}

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
	_, err = indexPage(processCtx, url.Link, &pageCache)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, errWorkerLog, id, url.Link, err)
	}

	// Mark the link as visited and add new links to the process context
	if processCtx.visitedLinks == nil {
		processCtx.visitedLinks = make(map[string]bool)
	}
	processCtx.visitedLinks[cmn.NormalizeURL(url.Link)] = true
	processCtx.visitedLinks[cmn.NormalizeURL(currentURL)] = true

	// Add new links to the process context
	if len(pageCache.Links) > 0 {
		func() {
			processCtx.linksMutex.Lock()
			defer processCtx.linksMutex.Unlock()
			processCtx.newLinks = append(processCtx.newLinks, pageCache.Links...)
		}()
	}

	// Before we return, we need to call goBack to go back to the previous page
	err = goBack(processCtx)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Worker %s: Error navigating back: %v\n", id, err)
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
	_, _ = vdiSleep(processCtx, delay)
	return nil
}

func clickLink(processCtx *ProcessContext, id string, url LinkItem) error {
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
		_, _, err := getURLContent(url.PageURL, processCtx.wd, 0, processCtx, id)
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
	_, _ = vdiSleep(processCtx, delay) // Pause to let page load

	// Check current URL
	currentURL, _ := processCtx.wd.CurrentURL()
	if currentURL != url.Link {
		cmn.DebugMsg(cmn.DbgLvlError, "Worker %s: Error navigating to %s: URL mismatch\n", id, url)
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
		publishDetectionResults(processCtx, currentURL, detectedTech)
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
	_, err = indexPage(processCtx, url.Link, &pageCache)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, errWorkerLog, id, url.Link, err)
	}
	if processCtx.visitedLinks == nil {
		processCtx.visitedLinks = make(map[string]bool)
	}
	processCtx.visitedLinks[cmn.NormalizeURL(url.Link)] = true

	// Add the new links to the process context
	if len(pageCache.Links) > 0 {
		processCtx.linksMutex.Lock()
		defer processCtx.linksMutex.Unlock()
		processCtx.newLinks = append(processCtx.newLinks, pageCache.Links...)
	}

	return err
}

func processJobVDI(processCtx *ProcessContext, id string, url string, skippedURLs []LinkItem, processJobStartTime time.Time) (*PageInfo, string, error) {
	// Set getURLMutex to ensure only one goroutine is accessing the vdi.WebDriver at a time
	processCtx.getURLMutex.Lock()
	defer processCtx.getURLMutex.Unlock()
	cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-Worker] %s: Starting processJob with '%s'\n", id, url)

	if processCtx.VDIReturned {
		// If the VDI session has been returned we need to stop the worker
		return nil, "", nil
	}

	// Get the HTML content of the page
	cmn.DebugMsg(cmn.DbgLvlDebug3, "[DEBUG-Worker] %s: Getting HTML content for '%s'\n", id, url)
	startTime := time.Now()
	htmlContent, docType, err := getURLContent(url, processCtx.wd, 1, processCtx, id)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Worker %s: Error getting HTML content for '%s': %v. Moving to next Link if any.\n", id, url, err)
		return nil, "", err
	}
	elapsed := time.Since(startTime)
	cmn.DebugMsg(cmn.DbgLvlDebug3, "[DEBUG-Worker] %s: Successfully retrieved HTML content for '%s' in %v\n", id, url, elapsed)

	// Collect reusable browser-page data from the already-loaded page.
	cmn.DebugMsg(cmn.DbgLvlDebug3, "[DEBUG-Worker] %s: Collecting loaded page data for '%s'\n", id, url)
	startTime = time.Now()
	pageCache, currentURL, _, err := collectLoadedWebPage(processCtx, htmlContent, url, docType)
	if err != nil {
		return pageCache, currentURL, err
	}
	pageCache.Links = append(pageCache.Links, skippedURLs...)
	elapsed = time.Since(startTime)
	cmn.DebugMsg(cmn.DbgLvlDebug3, "[DEBUG-Worker] %s: Successfully collected loaded page data for '%s' in %v\n", id, currentURL, elapsed)

	// Delay before processing the next job (if total elapsed time is just few seconds)
	totalElapsedTime := time.Since(processJobStartTime)
	if totalElapsedTime < 30*time.Second {
		var totalDelay time.Duration
		if processCtx.config.Crawler.Delay != "0" {
			delay := exi.GetFloat(processCtx.config.Crawler.Delay)
			totalDelay, _ = vdiSleep(processCtx, delay)
		}
		processCtx.Status.LastDelay = totalDelay.Seconds()
	} else {
		processCtx.Status.LastDelay = totalElapsedTime.Seconds()
	}

	return pageCache, currentURL, nil
}

func processJob(processCtx *ProcessContext, id, url string, skippedURLs []LinkItem) error {
	// Get start time
	processJobStartTime := time.Now()

	// Process the job using VDI
	pageCache, currentURL, err := processJobVDI(processCtx, id, url, skippedURLs, processJobStartTime)
	if err != nil || pageCache == nil {
		// Get elapsed time
		elapsed := time.Since(processJobStartTime)
		cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-Worker] %s: Finished processing job '%s' with error: %v in %v\n", id, url, err, elapsed)
		return err
	}

	cmn.DebugMsg(cmn.DbgLvlDebug3, "[DEBUG-Worker] %s: Indexing page '%s' with %d links found.\n", id, currentURL, len(pageCache.Links))
	pageCache.Config = &processCtx.config
	startTime := time.Now()
	_, err = indexPage(processCtx, currentURL, pageCache)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, errWorkerLog, id, url, err)
	}
	elapsed := time.Since(startTime)
	if processCtx.visitedLinks == nil {
		processCtx.visitedLinks = make(map[string]bool)
	}
	processCtx.visitedLinks[cmn.NormalizeURL(url)] = true
	cmn.DebugMsg(cmn.DbgLvlDebug3, "[DEBUG-Worker] %s: Indexed page '%s' in %v\n", id, currentURL, elapsed)

	// Add the new links to the process context
	if len(pageCache.Links) > 0 {
		func() {
			cmn.DebugMsg(cmn.DbgLvlDebug3, "[DEBUG-Worker] %s: Adding %d new links to the process context.\n", id, len(pageCache.Links))
			startTime := time.Now()
			processCtx.linksMutex.Lock()
			defer processCtx.linksMutex.Unlock()
			processCtx.newLinks = append(processCtx.newLinks, pageCache.Links...)
			elapsed := time.Since(startTime)
			cmn.DebugMsg(cmn.DbgLvlDebug3, "[DEBUG-Worker] %s: Successfully added new links to the process context in %v\n", id, elapsed)
		}()
	}
	resetPageInfo(pageCache) // Reset the PageInfo object

	// Check if we have a Stale Processing:
	if (processCtx.Status.DetectedState.Load() & 0x01) != 0 {
		// We have a stale processing, so we need to set the error to stop the worker
		err = errors.New("[critical] Stale Processing detected, stopping worker")
	}

	elapsedFullTime := time.Since(processJobStartTime)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-Worker] %s: Finished processing job '%s' with error: %v in %v\n", id, url, err, elapsedFullTime)
	} else {
		cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-Worker] %s: Finished processing job '%s' in %v, returning to worker routine.\n", id, url, elapsedFullTime)
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
