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
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	detect "github.com/pzaino/thecrowler/pkg/detection"
	exi "github.com/pzaino/thecrowler/pkg/exprterpreter"
	"github.com/pzaino/thecrowler/pkg/vdi"
)

// ConnectToVDI is responsible for connecting to the CROWler VDI Instance
func (ctx *ProcessContext) ConnectToVDI(sel vdi.SeleniumInstance) error {
	var err error
	var browserType int
	if ctx.config.Crawler.Platform == optBrowsingMobile {
		browserType = 1
	}
	cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-ConnectToVDI] Connecting to VDI %s...", sel.Config.Name)
	ctx.wd, err = vdi.ConnectVDI(ctx, sel, browserType)
	if err != nil {
		//(*ctx.sel) <- sel
		cmn.DebugMsg(cmn.DbgLvlError, vdi.VDIConnError, err)
		return err
	}
	ctx.Status.VDISID = ctx.wd.SessionID() // Update the report with the current VDI session ID
	cmn.DebugMsg(cmn.DbgLvlDebug1, "[DEBUG-ConnectToVDI] Connected to VDI successfully.")
	return nil
}

// RefreshVDIConnection is responsible for refreshing the Selenium connection
func (ctx *ProcessContext) RefreshVDIConnection(sel vdi.SeleniumInstance) error {
	if (ctx.Status.DetectedState.Load() & 0x01) != 0 {
		// Stale-Processing detected, we need to abort the process
		err := errors.New("stale-processing detected")
		UpdateSourceState(*ctx.db, ctx.source.URL, err)
		cmn.DebugMsg(cmn.DbgLvlError, "Stale-Processing detected, aborting the process.")
		return err
	}
	title, err := ctx.wd.Title()
	if err != nil {
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
	cmn.DebugMsg(cmn.DbgLvlDebug3, "[DEBUG-RefreshVDIConnection] Refreshed VDI connection, current page title: %s", title)
	return nil
}

// KeepSessionAlive is a dummy function that keeps the WebDriver session alive.
// It's used to prevent the WebDriver session from timing out.
func KeepSessionAlive(wd vdi.WebDriver) error {
	if wd == nil {
		return errors.New("WebDriver is nil, cannot keep session alive")
	}

	// Check if we have a SessionID
	sessionID := wd.SessionID()
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("invalid parameters: WebDriver SessionID is empty, unable to find session with id")
	}
	cmn.DebugMsg(cmn.DbgLvlDebug5, "[DEBUG-KeepAlive] Refreshing session with ID '%s'...", sessionID)

	// Keep session alive
	titleStr, err := wd.Title()
	if err != nil {
		return fmt.Errorf("failed to keep session alive: %v", err)
	}
	cmn.DebugMsg(cmn.DbgLvlDebug5, "[DEBUG-KeepAlive] Sent 'Keep Session Alive' command for page '%s'", titleStr)

	return nil
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

	_ = cleanUpBrowser(ctx.wd)

	return nil
}

func cleanUpBrowser(wd vdi.WebDriver) error {
	if wd == nil {
		cmn.DebugMsg(cmn.DbgLvlError, "WebDriver is nil, cannot clean up browser.")
		return errors.New("WebDriver is nil")
	}

	// Check if the current page is `data:`, if so skip this routine and return
	currentURL, err := wd.CurrentURL()
	if err == nil && strings.HasPrefix(currentURL, "data:") {
		err = wd.DeleteAllCookies()
		return err
	}

	// Clear everything before resetting the VDI session
	script := `
	window.localStorage.clear();
	window.sessionStorage.clear();
	if (window.indexedDB) {
		indexedDB.databases().then(dbs => {
			dbs.forEach(db => indexedDB.deleteDatabase(db.name));
		});
	}
	if ('caches' in window) {
		caches.keys().then(keys => {
			keys.forEach(key => caches.delete(key));
		});
	}
	`
	_, err = wd.ExecuteScript(script, nil)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug3, "failed to clear storage: %v", err)
		return err
	}

	err = wd.DeleteAllCookies()
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug3, "failed to delete cookies: %v", err)
	}
	return err
}

func setupBrowser(wd vdi.WebDriver, ctx *ProcessContext) {
	if wd == nil {
		cmn.DebugMsg(cmn.DbgLvlError, "WebDriver is nil, cannot setup browser.")
		return
	}

	if ctx.VDIReturned {
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
	err = wd.Get("about:blank")
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "failed to load blank page: %v", err)
	}

	// Set GPU properties
	if ctx.config.Crawler.SetVDIGPUPatch {
		err = vdi.GPUPatch(wd)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Failed to set GPU: %v", err)
		}
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
				_ = wd.Get(homeURL)
			}
		}
	}

	// Add XHR Hook
	if ctx.config.Crawler.CollectXHR {
		err = enableCDPNetworkLogging(ctx.wd, getCDPDelay(ctx.config))
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "adding XHR Hook: %v", err)
		}
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
	if referrerURLInf != nil {
		referrerURL, ok := referrerURLInf.(string)
		if !ok {
			cmn.DebugMsg(cmn.DbgLvlError, "referrerURLInf is not a string: %v", referrerURLInf)
			return
		}
		if referrerURL != "" {
			err := vdi.EnableNetwork(*wd, getCDPDelay(ctx.config), nil)
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "failed to enable Network domain: %v", err)
				return
			}
			err = vdi.SetExtraHTTPHeaders(*wd, getCDPDelay(ctx.config), map[string]interface{}{
				"referer": referrerURL,
			})
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "failed to set referrer URL: %v", err)
				return
			}
		}
	}
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
			_ = vdi.EnableNetwork(*wd, getCDPDelay(ctx.config), nil)
			// Set the User-Agent using CDP
			err = vdi.SetUserAgentOverride(*wd, getCDPDelay(ctx.config), userAgent, ctx.config.Crawler.Platform)
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

// changeUserAgentCDP modifies the User-Agent using CDP via Selenium bridge.
func changeUserAgentCDP(pctx *ProcessContext, userAgent string) error {
	if pctx == nil || pctx.wd == nil {
		return fmt.Errorf("invalid process context or webdriver")
	}

	err := vdi.EnableNetwork(pctx.wd, getCDPDelay(pctx.config), nil)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug2, "[CDP] failed to enable Network domain via Selenium CDP bridge: %v", err)
		return err
	}

	err = vdi.SetUserAgentOverride(pctx.wd, getCDPDelay(pctx.config), userAgent, pctx.config.Crawler.Platform)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug2, "[CDP] failed to change User-Agent using Selenium CDP bridge: %v", err)
		return err
	}

	cmn.DebugMsg(cmn.DbgLvlDebug2, "[CDP] Successfully changed UserAgent to: '%s', using Selenium CDP bridge", userAgent)
	return nil
}

func blockCDPURLs(wd vdi.WebDriver, ctx *ProcessContext) error {
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
	err := vdi.EnableNetwork(wd, getCDPDelay(ctx.config), nil)
	if err != nil {
		return fmt.Errorf("failed to enable Network domain: %w", err)
	}

	// Then: set the blocked URL patterns
	err = vdi.SetBlockedURLs(wd, getCDPDelay(ctx.config), patterns)
	if err != nil {
		return fmt.Errorf("failed to set blocked URLs: %w", err)
	}

	return nil
}

func getCookies(ctx *ProcessContext, wd *vdi.WebDriver) error {
	if ctx.VDIReturned {
		// If the VDI session is returned, stop the process
		return nil
	}
	if wd == nil || *wd == nil {
		return errors.New("WebDriver is nil, cannot get cookies")
	}

	// Get the cookies
	cookies, err := (*wd).GetCookies()
	if err != nil {
		return fmt.Errorf("failed to get cookies: %v", err)
	}

	// Add new cookies to the context
	for _, cookie := range cookies {
		if ctx.CollectedCookies == nil {
			// SOmething must have close the session here
			break
		}
		ctx.CollectedCookies[cookie.Name] = cookie.Value
	}

	return nil
}

func (ctx *ProcessContext) tryAlternativeLinksLocked(
	wid string,
	origErr error,
) (vdi.WebDriver, string, error) {
	wd := ctx.wd
	docType := ""
	err := origErr

	srcCfg := ctx.srcCfg["crawling_config"]
	crawlingConfig, ok := srcCfg.(map[string]interface{})
	if !ok {
		return wd, docType, err
	}

	urlPatterns, ok := crawlingConfig["alternative_links"]
	if !ok {
		return wd, docType, err
	}

	patterns, ok := urlPatterns.([]interface{})
	if !ok {
		return wd, docType, err
	}

	for _, p := range patterns {
		patternStr, ok := p.(string)
		if !ok || patternStr == "" {
			continue
		}

		// If visitedLinks is shared across goroutines, guard this with a mutex.
		found := false
		for visitedLink := range ctx.visitedLinks {
			if cmn.NormalizeURL(visitedLink) == cmn.NormalizeURL(patternStr) {
				found = true
				break
			}
		}
		if found {
			continue
		}

		cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-Worker] %s: Trying alternative link: %s", wid, patternStr)

		var newWD vdi.WebDriver
		newWD, docType, err = getURLContent(patternStr, ctx.wd, -1, ctx, wid)
		if err == nil {
			// Keep ctx.wd in sync if getURLContent returns a replacement driver
			ctx.wd = newWD
			return newWD, docType, nil
		}
	}

	return wd, docType, err
}

func (ctx *ProcessContext) crawlInitialURLVDI(wid string) (*PageInfo, string, error) {
	ctx.getURLMutex.Lock()
	defer ctx.getURLMutex.Unlock()

	cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-Worker] %s: Crawling Source: %d", wid, ctx.source.ID)
	cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-Worker] %s: Crawling URL: %s", wid, ctx.source.URL)

	pageSource, docType, err := getURLContent(ctx.source.URL, ctx.wd, -1, ctx, wid)
	if err != nil {
		// try alternative links (still under the mutex, since it uses ctx.wd)
		pageSource, docType, err = ctx.tryAlternativeLinksLocked(wid, err)
		if err != nil {
			UpdateSourceState(*ctx.db, ctx.source.URL, err)
			return nil, "", err
		}
	}

	if ctx.RefreshCrawlingTimer != nil {
		ctx.RefreshCrawlingTimer()
	}
	_ = vdi.Refresh(ctx)

	url, err := ctx.wd.CurrentURL()
	if err != nil {
		UpdateSourceState(*ctx.db, ctx.source.URL, err)
		return nil, "", err
	}

	pageInfo := &PageInfo{}

	detectCtx := detect.DContext{
		CtxID:     ctx.GetContextID(),
		TargetURL: url,
		WD:        &ctx.wd,
		RE:        ctx.re,
		Config:    &ctx.config,
	}
	// tech detect
	if detectedTech := detect.DetectTechnologies(&detectCtx); detectedTech != nil {
		pageInfo.DetectedTech = *detectedTech
		publishDetectionResults(ctx, url, detectedTech)
	}

	// extract page info (uses pageSource, and may use wd depending on your implementation)
	if err := extractPageInfo(&pageSource, ctx, docType, pageInfo); err != nil {
		if strings.Contains(err.Error(), errCriticalError) {
			UpdateSourceState(*ctx.db, ctx.source.URL, err)
			return pageInfo, url, err
		}
	}

	pageInfo.DetectedType = docType
	pageInfo.HTTPInfo = ctx.hi
	pageInfo.NetInfo = ctx.ni

	// links, keywords, metrics, logs, XHR
	pageInfo.Links = extractLinks(ctx, pageInfo.HTML, url)
	pageInfo.Keywords = extractKeywords(*pageInfo)

	if ctx.config.Crawler.CollectPerfMetrics {
		collectNavigationMetrics(&ctx.wd, pageInfo)
	}
	if ctx.config.Crawler.CollectPageEvents {
		collectPageLogs(&pageSource, pageInfo)
	}
	if ctx.config.Crawler.CollectXHR {
		collectXHR(ctx, pageInfo)
	}

	if !ctx.config.Crawler.CollectHTML {
		pageInfo.HTML = ""
	}
	if !ctx.config.Crawler.CollectContent {
		pageInfo.BodyText = ""
	}

	// Delay after full page collection
	if ctx.config.Crawler.Delay != "0" {
		delay := exi.GetFloat(ctx.config.Crawler.Delay)
		totalDelay, _ := vdiSleep(ctx, delay)
		ctx.Status.LastDelay = totalDelay.Seconds()
	}

	return pageInfo, url, nil
}

// CrawlInitialURL is responsible for crawling the initial URL of a Source
func (ctx *ProcessContext) CrawlInitialURL(_ vdi.SeleniumInstance) (vdi.WebDriver, error) {
	wid := ctx.GetContextID() + "_0"

	pageInfo, url, err := ctx.crawlInitialURLVDI(wid)
	if err != nil {
		return ctx.wd, err
	}
	if pageInfo == nil {
		return ctx.wd, nil
	}

	// Index outside mutex
	cmn.DebugMsg(cmn.DbgLvlDebug3, "[DEBUG-Worker] %s: Indexing page for URL: %s", wid, url)
	ctx.fpIdx, err = ctx.IndexPage(pageInfo)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "indexing page: %v", err)
		UpdateSourceState(*ctx.db, ctx.source.URL, err)
	}

	// visitedLinks update (make sure this is protected if used concurrently elsewhere)
	fURL := cmn.NormalizeURL(url)
	if ctx.visitedLinks == nil {
		ctx.visitedLinks = make(map[string]bool)
	}
	ctx.visitedLinks[fURL] = true
	ctx.Status.TotalPages.Add(1)

	resetPageInfo(pageInfo)

	return ctx.wd, nil
}

func waitForDomComplete(wd vdi.WebDriver, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		val, err := wd.ExecuteScript("return document.readyState", nil)
		if err == nil {
			state := strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", val)))
			if state == "complete" {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("document.readyState never reached 'complete' within timeout")
}

func getWithTimeout(wd *vdi.WebDriver, url string, timeout time.Duration) error {
	if wd == nil {
		return errors.New("[critical] WebDriver is nil")
	}

	done := make(chan error, 1)

	go func() {
		done <- (*wd).Get(url)
	}()

	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		return fmt.Errorf("[critical] wd.Get timeout after %s for URL %s", timeout, url)
	}
}

// getURLContent is responsible for retrieving the HTML content of a page
// from Selenium and returning it as a vdi.WebDriver object
func getURLContent(url string, wd vdi.WebDriver, level int, ctx *ProcessContext, id string) (vdi.WebDriver, string, error) {
	if ctx == nil {
		return nil, "", errors.New("ProcessContext is nil")
	}
	//ctx.accessVDIMutex.Lock() // Allow 1 worker per session	at the time
	//defer ctx.accessVDIMutex.Unlock()

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

	// Define page load timeout (this is to ensure the VDI's Selenium doesn't gets stuck)
	const getPageTimeout = 45 * time.Second

	// Let's start preparing for a fetch:

	var err error
	// Get the page and process the interval
	maxRetries := 5 // Default retries on redirect
	cfgSource := ctx.srcCfg["crawling_config"]
	cfgSourceMap, ok := cfgSource.(map[string]interface{})
	if ok {
		if val, exists := cfgSourceMap["retries_on_redirect"]; exists {
			maxRetries = int(val.(float64))
		}
	}

	// Check again if something has returned the session while we were waiting
	if ctx.VDIReturned {
		// If the VDI session is returned, return the WebDriver
		return wd, "", nil
	}

	// Check if we have a sessionID in the VDI
	sid := strings.TrimSpace(wd.SessionID())
	if sid == "" {
		// We need to reconnect to the VDI
		err = ctx.ConnectToVDI((*ctx).SelInstance)
		wd = ctx.wd
		if err != nil {
			return nil, "", fmt.Errorf("[critical] failed to connect to VDI: %v", err)
		}
	}

	// Reset cookies if needed
	if (ctx.config.Crawler.ResetCookiesPolicy == "on_start") && (level == -1) {
		// Reset cookies only on the first URL
		_ = ResetSiteSession(ctx)
	}
	if (ctx.config.Crawler.ResetCookiesPolicy == optCookiesOnReq) ||
		(ctx.config.Crawler.ResetCookiesPolicy == cmn.AlwaysStr) {
		// Reset cookies on each request
		_ = ResetSiteSession(ctx)
	}

	// Tracking validation retries (if any)
	validationRetryBudget := make(map[string]int)
	ctx.Status.LastRetry.Store(0)
	for retries := 0; retries <= maxRetries; retries++ {
		if ctx.VDIReturned {
			// If the VDI session is returned, return the WebDriver
			return wd, "", nil
		}

		// Reset the VDI session for a clean browser with new User-Agent
		if ctx.config.Crawler.ChangeUserAgent == "always" {
			err = cleanUpBrowser(wd) // Clear everything before resetting the VDI session
			if err != nil {
				// Let's check if session ID is invalid, if so we need to create a new one
				if strings.Contains(strings.ToLower(strings.TrimSpace(err.Error())), "invalid session id") ||
					strings.Contains(strings.ToLower(strings.TrimSpace(err.Error())), "unable to find session with id") {
					cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-Worker] %s: WebDriver session not found during cleanup, creating a new one...", id)
					err = ctx.ConnectToVDI((*ctx).SelInstance)
					wd = ctx.wd
					if err != nil {
						return nil, "", fmt.Errorf("[critical] failed to create a new WebDriver session: %v", err)
					}
				}
			}

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
				if strings.Contains(strings.ToLower(strings.TrimSpace(err.Error())), "invalid session id") ||
					strings.Contains(strings.ToLower(strings.TrimSpace(err.Error())), "unable to find session with id") {
					// If the session is not found, create a new one
					cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-Worker] %s: WebDriver session not found, creating a new one...", id)
					err = ctx.ConnectToVDI((*ctx).SelInstance)
					wd = ctx.wd
					if err != nil {
						return nil, "", fmt.Errorf("[critical] failed to create a new WebDriver session: %v", err)
					}
				}
			}
		}

		// Setup the Browser before requesting a page
		setupBrowser(wd, ctx)

		// Set the HTTP referer if we are on the first URL
		if level == -1 {
			setReferrerHeader(&wd, ctx)
		}

		// Navigate to a page and interact with elements.
		if ctx.RefreshCrawlingTimer != nil {
			ctx.RefreshCrawlingTimer()
		}

		PageLoadOk := false
		ctx.Status.LastRetry.Add(1) // Increment the report retry count
		if err := getWithTimeout(&wd, url, getPageTimeout); err != nil {
			if strings.Contains(strings.ToLower(strings.TrimSpace(err.Error())), "unable to find session with id") {
				// If the session is not found, create a new one
				cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-Worker] %s: WebDriver session not found, creating a new one...", id)
				err = ctx.ConnectToVDI((*ctx).SelInstance)
				wd = ctx.wd
				if err != nil {
					details := map[string]any{
						"source":  "crawler",
						"url":     url,
						"message": fmt.Sprintf("failed to create a new WebDriver session: %v", err),
					}
					e := cdb.Event{
						Action:    "new",
						SourceID:  ctx.source.ID,
						Type:      "vdi_failed_to_get_url",
						Severity:  ctx.source.Priority,
						ExpiresAt: time.Now().Add(2 * time.Minute).Format(time.RFC3339),
						Details:   details,
					}
					_, _ = cdb.CreateEventWithRetries(ctx.db, e)
					return nil, "", fmt.Errorf("[critical] failed to create a new WebDriver session: %v", err)
				}
				// Setup the Browser before requesting a page
				setupBrowser(wd, ctx)
				// Set the HTTP referer if we are on the first URL
				if level == -1 {
					setReferrerHeader(&wd, ctx)
				}
				// Retry navigating to the page
				err := getWithTimeout(&wd, url, getPageTimeout)
				if err != nil {
					details := map[string]any{
						"source":  "crawler",
						"url":     url,
						"message": fmt.Sprintf("failed to navigate to %s: %v", url, err),
					}
					e := cdb.Event{
						Action:    "new",
						SourceID:  ctx.source.ID,
						Type:      "vdi_failed_to_get_url",
						Severity:  ctx.source.Priority,
						ExpiresAt: time.Now().Add(2 * time.Minute).Format(time.RFC3339),
						Details:   details,
					}
					_, _ = cdb.CreateEventWithRetries(ctx.db, e)
					return nil, "", fmt.Errorf("[critical] failed to navigate to %s: %v", url, err)
				}
			} else {
				details := map[string]any{
					"source":  "crawler",
					"url":     url,
					"message": fmt.Sprintf("failed to navigate to %s: %v", url, err),
				}
				e := cdb.Event{
					Action:    "new",
					SourceID:  ctx.source.ID,
					Type:      "vdi_failed_to_get_url",
					Severity:  ctx.source.Priority,
					ExpiresAt: time.Now().Add(2 * time.Minute).Format(time.RFC3339),
					Details:   details,
				}
				_, _ = cdb.CreateEventWithRetries(ctx.db, e)
				return nil, "", fmt.Errorf("failed to navigate to %s: %v", url, err)
			}
		}

		// Add XHR Hook (before any action is made, but after the page has been requested)
		if ctx.config.Crawler.CollectXHR {
			err = addXHRHook(&wd)
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "[DEBUG-Worker] %s: Failed to add XHR hook: %v", id, err)
			}
		}

		// Wait for Page to Load
		interval := exi.GetFloat(ctx.config.Crawler.Interval)
		if interval <= 0 {
			interval = 3
		}
		var totalInterval time.Duration
		if level > 0 {
			totalInterval, _ = vdiSleep(ctx, interval) // Pause to let page load
		} else {
			totalInterval, _ = vdiSleep(ctx, (interval + 5)) // Pause to let Home page load
		}
		ctx.Status.LastWait = totalInterval.Seconds()

		// Check if we are on the right URL
		currentURL, err := wd.CurrentURL()
		if err != nil {
			return nil, "", fmt.Errorf("failed to get current URL after navigation: %v", err)
		}
		gotRedirectedToUURL := false
		if ctx.compiledUURLs != nil {
			cmn.DebugMsg(cmn.DbgLvlDebug3, "[DEBUG-Worker] %s: Checking for unwanted URLs (%d patterns) for URL: '%s'", id, len(ctx.compiledUURLs), currentURL)
			for _, UURL := range ctx.compiledUURLs {
				if UURL.MatchString(currentURL) {
					gotRedirectedToUURL = true
					break
				}
			}
		}
		if gotRedirectedToUURL {
			cmn.DebugMsg(cmn.DbgLvlDebug3, "[DEBUG-Worker] %s: Unwanted redirect detected: %s != %s", id, currentURL, url)
			if retries >= maxRetries {
				return nil, "", fmt.Errorf("failed to navigate to %s after %d retries", url, maxRetries)
			}
			cmn.DebugMsg(cmn.DbgLvlDebug3, "[DEBUG-Worker] %s: Retrying navigation to %s (%d/%d)", id, url, retries+1, maxRetries)
			continue
		}
		PageLoadOk = true

		// We are on an allowed URL. Run page-load validation here.
		status, _ := ApplyLoadValidation(ctx, &wd, level)

		// Case 1: valid (or log_only, which you treat as valid after logging)
		if status.Valid {
			if status.Action == VALogOnly {
				cmn.DebugMsg(cmn.DbgLvlWarn, "[DEBUG-Worker] %s: load_validation -> log_only for URL %s", id, url)
			}
			PageLoadOk = true
		} else {
			switch status.Action {
			case VARetry:
				used := validationRetryBudget[status.RetryKey]
				if used < status.MaxRetries {
					validationRetryBudget[status.RetryKey] = used + 1
					// Guard general retry exhaustion
					if !(maxRetries > 0 && retries >= maxRetries) {
						continue // outer loop will reload
					}
					return nil, "", fmt.Errorf("failed to navigate to %s after %d retries", url, maxRetries)
				}
				// Rule retry budget exhausted → fall back to general retry
				if !(maxRetries > 0 && retries >= maxRetries) {
					continue
				}
				return nil, "", fmt.Errorf("failed to navigate to %s after %d retries", url, maxRetries)

			case VANone:
				// No explicit action; rely on general retry policy
				if !(maxRetries > 0 && retries >= maxRetries) {
					continue
				}
				return nil, "", fmt.Errorf("failed to navigate to %s after %d retries", url, maxRetries)

			case VASkip:
				return wd, "", fmt.Errorf("page skipped by validation")

			case VAFail:
				return nil, "", fmt.Errorf("page validation failed")
			}
		}
		// Page validation returned Ok
		if PageLoadOk {
			break
		}

		if retries == maxRetries && maxRetries > 0 {
			return nil, "", fmt.Errorf("failed to navigate to %s after %d retries", url, maxRetries)
		}
		cmn.DebugMsg(cmn.DbgLvlDebug3, "[DEBUG-Worker] %s: Retrying navigation to %s (%d/%d)", id, url, retries+1, maxRetries)
	}

	// Get Session Cookies
	err = getCookies(ctx, &wd)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "[DEBUG-Worker] %s: failed to get cookies: %v", id, err)
	}

	// Get the Mime Type of the page
	docType := inferDocumentType(url, &wd)
	cmn.DebugMsg(cmn.DbgLvlDebug3, "[DEBUG-Worker] %s: Document Type: %s", id, docType)

	if docTypeIsHTML(docType) || (strings.TrimSpace(docType) == "") {
		// WaitForDomComplete
		startTime := time.Now()
		err = waitForDomComplete(wd, 5*time.Second)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "[DEBUG-Worker] %s: waitForDomComplete error: %v", id, err)
		}
		elapsed := time.Since(startTime)
		// Sum elapsed to last wait time
		ctx.Status.LastWait += elapsed.Seconds()

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
		cmn.DebugMsg(cmn.DbgLvlError, "[DEBUG-Worker] %s: failed to get post-actions cookies: %v", id, err)
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

func moveMouseRandomly(wd vdi.WebDriver) {
	// Example: moving mouse via Rbee
	x := rand.IntN(1920) //nolint:gosec // We are using "math/rand/v2" here
	y := rand.IntN(1080) //nolint:gosec // We are using "math/rand/v2" here

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
		cmn.DebugMsg(cmn.DbgLvlDebug3, "[DEBUG-Mouse] failed to execute Rbee moveMouse: %v", err)
	}
}

func vdiSleep(ctx *ProcessContext, delay float64) (time.Duration, error) {
	driver := ctx.wd

	if driver == nil {
		return 0, errors.New("WebDriver is nil")
	}

	// Check if we have a SessionID
	sessionID := driver.SessionID()
	if strings.TrimSpace(sessionID) == "" {
		return 0, fmt.Errorf("invalid parameters: WebDriver SessionID is empty, unable to find session with id")
	}
	cmn.DebugMsg(cmn.DbgLvlDebug5, "[DEBUG-vdiSleep] Refreshing session with ID '%s'...", sessionID)

	sid := driver.SessionID()
	if strings.TrimSpace(sid) == "" {
		return 0, errors.New("WebDriver session ID is empty")
	}

	const minDelay = 3.0
	if delay < minDelay {
		delay = minDelay
	}

	// Divider formula for human-like polling rate
	divider := math.Log10(delay+1) * 10.0
	timeout := float64(ctx.config.Crawler.Timeout)

	if timeout > 0 && divider >= timeout {
		divider = timeout - 1.0
	}
	if divider <= 0 {
		divider = 1.0
	}

	waitDuration := time.Duration(delay * float64(time.Second))

	// Poll interval (keep-alive pings)
	pollSec := delay / divider
	if pollSec <= 0 {
		pollSec = 0.2 // sane fallback
	}
	pollInterval := time.Duration(pollSec * float64(time.Second))

	start := time.Now()
	cmn.DebugMsg(cmn.DbgLvlDebug3, "[DEBUG-Wait] Waiting for %.3f seconds...", delay)

	// Prevent the compiler from optimizing away keep-alive calls
	var seleniumKeepAliveSink any

	keepAliveWait := 0
	for {
		elapsed := time.Since(start)
		if elapsed >= waitDuration {
			break
		}

		// Keep-alive: must NOT be optimized out
		if keepAliveWait <= 0 {
			val, _ := driver.Title()
			seleniumKeepAliveSink = val
			if seleniumKeepAliveSink.(string) != "" {
				cmn.DebugMsg(cmn.DbgLvlDebug3, "[DEBUG-Wait] Sent keep-alive ping to VDI for page title: %s", seleniumKeepAliveSink)
			}
			keepAliveWait = 3 // every 3 iterations
		} else {
			keepAliveWait--
		}

		if ctx.RefreshCrawlingTimer != nil {
			ctx.RefreshCrawlingTimer()
		}

		remaining := waitDuration - time.Since(start)
		if remaining <= 0 {
			break
		}
		if pollInterval > remaining {
			time.Sleep(remaining)
		} else {
			time.Sleep(pollInterval)
		}
	}
	if seleniumKeepAliveSink == nil {
		cmn.DebugMsg(cmn.DbgLvlDebug5, "[DEBUG-Wait] keep-alive sink is nil (should not happen)")
	}

	// Simulated human mouse move
	moveMouseRandomly(driver)

	total := time.Since(start)
	cmn.DebugMsg(cmn.DbgLvlDebug3, "[DEBUG-Wait] Waited for %.3f seconds", total.Seconds())

	return total, nil
}
