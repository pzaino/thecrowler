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
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	detect "github.com/pzaino/thecrowler/pkg/detection"
	exi "github.com/pzaino/thecrowler/pkg/exprterpreter"
	"github.com/pzaino/thecrowler/pkg/vdi"

	gohtml "golang.org/x/net/html"
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
	// Send a KeepSessionAlive to prevent session timeout
	err := KeepSessionAlive(ctx.wd)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "keeping session alive: %v", err)
		ctx.pStatus = 3
		return
	}

	cmn.DebugMsg(cmn.DbgLvlDebug5, "Starting collecting XHR requests...")

	// Convert to Go structure
	xhrData, err := collectCDPRequests(ctx, 1000) // Let's cap them to 1000 entries (some site is extremely chatty and can lead to a very long time processing)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug5, "Error during XHR data collection: %v", xhrData)
		return
	}
	cmn.DebugMsg(cmn.DbgLvlDebug5, "XHR returned from collectCDPRequests\n")

	// Send a keep alive to the VDI
	err = KeepSessionAlive(ctx.wd)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "keeping session alive: %v", err)
		ctx.pStatus = 3
	}

	// Store data in PageInfo
	xhr := map[string]any{"xhr": xhrData}
	pageInfo.ScrapedData = append(pageInfo.ScrapedData, xhr)
	cmn.DebugMsg(cmn.DbgLvlDebug5, "XHR Data Captured")

	// Debug output
	//jsonData, err := json.MarshalIndent(xhr, "", "  ")
	//if err != nil {
	//	cmn.DebugMsg(cmn.DbgLvlError, "marshalling XHR data to JSON: %v", err)
	//}
	//cmn.DebugMsg(cmn.DbgLvlDebug5, "XHR Data Captured: %s", jsonData)
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

func getCDPDelay(cfg cfg.Config) int {
	if cfg.Crawler.CDPDelay < 0 {
		return 0
	}
	return cfg.Crawler.CDPDelay
}

func enableCDPNetworkLogging(wd vdi.WebDriver, cdpDelay int) error {
	// Enable full network tracking (includes POST bodies)
	maxPostDataSize := -1
	err := vdi.EnableNetwork(wd, cdpDelay, &maxPostDataSize)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to enable Network domain: %v", err)
		return err
	}

	// Disable caching to prevent early response disposal
	err = vdi.SetCacheDisabled(wd, cdpDelay, true)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to disable cache: %v", err)
	}

	// Capture Service Worker Requests
	err = vdi.EnableServiceWorker(wd, cdpDelay)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to enable Service Worker capture: %v", err)
	}

	// Capture ALL frames
	err = vdi.SetTargetAutoAttach(wd, cdpDelay, true, false, true)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to enable iframe logging: %v", err)
	}

	// Enable Log domain
	err = vdi.EnableLog(wd, cdpDelay)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to enable Log domain: %v", err)
		return err
	}

	// Enable Page Events (for iframe tracking)
	err = vdi.EnablePage(wd, cdpDelay)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to enable Page domain: %v", err)
		return err
	}

	// Optimized delay to ensure all logging hooks are active
	time.Sleep(750 * time.Millisecond)

	cmn.DebugMsg(cmn.DbgLvlDebug5, "CDP Network logging enabled successfully.")
	return nil
}

// isDBSafeText returns false if the data cannot be stored
// as JSON/text in a DB.

// checkTextBytes returns false if the data cannot be stored
// as JSON/text in a DB.

func listenForCDPEvents(ctx context.Context, p *ProcessContext, wd vdi.WebDriver, collectedRequests *[]map[string]interface{}) {
	for {
		select {
		case <-ctx.Done():
			// Stop listening when context is cancelled
			cmn.DebugMsg(cmn.DbgLvlDebug5, "Stopping CDP event listener.")
			return
		default:
			// Fetch CDP Events
			// events can be polled through CDP Log domain if needed.
			p.getURLMutex.Lock()
			logs, err := wd.Log("performance")
			p.getURLMutex.Unlock()
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "Failed to retrieve CDP events: %v", err)
				time.Sleep(1 * time.Second) // backoff on failure
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
					p.getURLMutex.Lock()
					postDataDecoded, detectedContentType := decodeBodyContent(wd, postData, false, url)
					p.getURLMutex.Unlock()
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
					p.getURLMutex.Lock()
					decodedPostData, detectedContentType := decodeBodyContent(wd, postData, false, "")
					p.getURLMutex.Unlock()
					if contentType == "" {
						contentType = detectedContentType
					}

					// Check if decodedPostData is DBSafeText
					if !isDBSafeText(decodedPostData) {
						decodedPostData = binaryDataOmitted
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
					p.getURLMutex.Lock()
					responseBody, isBase64 := fetchResponseBody(wd, getCDPDelay(p.config), requestID)
					p.getURLMutex.Unlock()
					if responseBody == "" {
						cmn.DebugMsg(cmn.DbgLvlDebug5, "⚠️ Failed to get response body for requestId %s: %v", requestID, err)
						continue
					}

					// Decode Response Body (if Base64)
					p.getURLMutex.Lock()
					decodedBody, detectedType := decodeBodyContent(wd, responseBody, isBase64, "")
					p.getURLMutex.Unlock()

					// Check if decodedPostData is DBSafeText
					if !isDBSafeText(decodedBody) {
						decodedBody = binaryDataOmitted
					}

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

func collectXHRLogs(ctx *ProcessContext, collectedResponses []map[string]interface{}, maxItems int) ([]map[string]interface{}, error) {
	cmn.DebugMsg(cmn.DbgLvlDebug5, "Collecting XHR logs...")
	wd := ctx.wd

	if wd == nil {
		return nil, errors.New("WebDriver is nil")
	}

	if ctx.VDIReturned {
		cmn.DebugMsg(cmn.DbgLvlError, "WebDriver session has already been returned.")
		return nil, nil
	}

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
	if (maxItems > 0) && (len(xhrData) > maxItems) {
		cmn.DebugMsg(cmn.DbgLvlDebug5, "Trimming XHR logs to maxItems (%d).", maxItems)
		xhrData = xhrData[:maxItems] // Trim logs to maxItems
	}
	cmn.DebugMsg(cmn.DbgLvlDebug5, "XHR logs retrieved successfully (%d entries).", len(xhrData))

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

				// Check if decodedPostData is DBSafeText
				if !isDBSafeText(decodedRespBody) {
					decodedRespBody = binaryDataOmitted
				}

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
func collectCDPRequests(ctx *ProcessContext, maxItems int) ([]map[string]interface{}, error) {
	cmn.DebugMsg(cmn.DbgLvlDebug5, "Collecting request logs...")
	const (
		rbee = "http://127.0.0.1:3000/v1/rb"
	)
	wd := ctx.wd

	if wd == nil {
		return nil, errors.New("WebDriver is nil")
	}

	if ctx.VDIReturned {
		cmn.DebugMsg(cmn.DbgLvlError, "WebDriver session has already been returned.")
		return nil, nil
	}

	// Send a Keep alive
	err := KeepSessionAlive(wd)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "[BROWSER-LOGS] Failed to keep session alive: %v", err)
		ctx.pStatus = 3
		return nil, err
	}

	// Fetch Performance Logs
	logs, err := wd.Log("performance")
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "[BROWSER-LOGS] Failed to retrieve performance logs: %v", err)
		return nil, err
	}
	cmn.DebugMsg(cmn.DbgLvlDebug5, "[BROWSER-LOGS] Performance logs retrieved successfully (%d entries).", len(logs))

	if (maxItems > 0) && (len(logs) > maxItems) {
		cmn.DebugMsg(cmn.DbgLvlDebug5, "[BROWSER-LOGS] Trimming logs to maxItems (%d).", maxItems)
		logs = logs[:maxItems] // Trim logs to maxItems
	}

	var collectedRequests []map[string]any
	var collectedResponses []map[string]any
	responseBodies := make(map[string]any) // Store response metadata

	// Process logs
	totalLogs := len(logs)
	for i, entry := range logs {
		var logEntry map[string]any
		if (i % 100) == 0 {
			if ctx.RefreshCrawlingTimer != nil {
				ctx.RefreshCrawlingTimer() // Refresh crawling timer
			}
			err = KeepSessionAlive(wd)
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "[BROWSER-LOGS] Failed to keep session alive: %v", err)
				ctx.pStatus = 3
				return collectedRequests, err
			}
		}

		if err := json.Unmarshal([]byte(entry.Message), &logEntry); err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "[BROWSER-LOGS] Failed to parse log entry: %v", err)
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
			url, _ := request["url"].(string)
			if url == rbee {
				// Skip unwanted request and do NOT add it to tracking map
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
		cmn.DebugMsg(cmn.DbgLvlDebug5, "[BROWSER-LOGS] Processed log entry %d/%d: %s", i+1, totalLogs, method)
	}
	cmn.DebugMsg(cmn.DbgLvlDebug5, "[BROWSER-LOGS] Collected %d requests and %d responses.", len(collectedRequests), len(collectedResponses))

	// Fetch Response Bodies & Attach Them
	collectResponses(ctx, responseBodies)

	// Collect XHR logs
	xhrLogs, err := collectXHRLogs(ctx, collectedResponses, maxItems)
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
	cmn.DebugMsg(cmn.DbgLvlDebug5, "Filtering logs collection... %d requests to filter.", len(collectedRequests))
	for i := 0; i < len(collectedRequests); i++ {
		if collectedRequests[i] == nil {
			continue
		}
		url, _ := collectedRequests[i]["url"].(string)
		if url == rbee { // remove requests to Rbee
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
		cmn.DebugMsg(cmn.DbgLvlDebug5, "Filtered Request: %s %s", collectedRequests[i]["method"], collectedRequests[i]["url"])
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

	err := KeepSessionAlive(ctx.wd)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to keep session alive: %v", err)
		ctx.pStatus = 3
	}
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

	// Check if decodedPostData is DBSafeText
	if !isDBSafeText(respBodyDecoded) {
		respBodyDecoded = binaryDataOmitted
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
func collectResponses(ctx *ProcessContext, responseBodies map[string]any) {
	cmn.DebugMsg(cmn.DbgLvlDebug5, "Collecting response bodies...")
	wd := ctx.wd
	// Fetch Response Bodies
	for requestID, request := range responseBodies {
		time.Sleep(100 * time.Millisecond) // Small delay
		if ctx.RefreshCrawlingTimer != nil {
			ctx.RefreshCrawlingTimer() // Refresh crawling timer
		}
		err := KeepSessionAlive(wd)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Failed to keep session alive: %v", err)
			return
		}

		// Fetch Response Body
		body, isBase64 := fetchResponseBody(wd, getCDPDelay(ctx.config), requestID)

		// Decode if necessary
		decodedBody, detectedType := decodeBodyContent(wd, body, isBase64, "")

		// Check if decodedPostData is DBSafeText
		if !isDBSafeText(decodedBody) {
			decodedBody = binaryDataOmitted
		}

		// Store response body inside the original request
		requestMap := request.(map[string]interface{})
		requestMap["response_body"] = decodedBody
		requestMap["response_content_type"] = detectedType
	}
}

// Fetch Response Body from ChromeDP
func fetchResponseBody(wd vdi.WebDriver, cdpDelay int, requestID string) (string, bool) {
	cmn.DebugMsg(cmn.DbgLvlDebug5, "Fetching response body for requestId: %s", requestID)
	// Try fetching response body (Retry if empty)
	var err error
	for i := 0; i < 5; i++ { // Retry up to 3 times
		body, isBase64, err := vdi.GetResponseBody(wd, cdpDelay, requestID)
		if err == nil {
			return body, isBase64
		}
		time.Sleep(200 * time.Millisecond) // Wait before retrying
	}

	err = KeepSessionAlive(wd)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to keep session alive: %v", err)
		return "", false
	}
	return "", false
}

// Decode Base64 & Parse JSON Responses
func decodeBodyContent(wd vdi.WebDriver, body string, isBase64 bool, url string) (interface{}, string) {
	cmn.DebugMsg(cmn.DbgLvlDebug5, "Decoding body content...")
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
		cmn.DebugMsg(cmn.DbgLvlDebug5, "Detected XML content type: %s", detectedContentType)
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

		err = KeepSessionAlive(wd)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Failed to keep session alive: %v", err)
			return body, detectedContentType
		}
	}

	if detectedContentType == HTMLType {
		cmn.DebugMsg(cmn.DbgLvlDebug5, "Detected HTML content type: %s", detectedContentType)
		// attempt to convert HTML to JSON
		doc, err := gohtml.Parse(strings.NewReader(bodyStr))
		if err != nil {
			return body, detectedContentType
		}
		processedData := ExtractHTMLData(doc)
		jsonBody, _ := json.MarshalIndent(processedData, "", "  ")
		bodyStr = string(jsonBody)

		err = KeepSessionAlive(wd)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Failed to keep session alive: %v", err)
			return body, detectedContentType
		}
	}

	// Attempt to parse as JSON (even without Content-Type check)
	var jsonBody map[string]interface{}
	if err := json.Unmarshal([]byte(bodyStr), &jsonBody); err == nil {
		cmn.DebugMsg(cmn.DbgLvlDebug5, "Detected JSON content type: %s", detectedContentType)
		jsonBody = deepConvertJSONFields(jsonBody)
		if detectedContentType != HTMLType {
			detectedContentType = JSONType
		}

		err = KeepSessionAlive(wd)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Failed to keep session alive: %v", err)
		}
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
