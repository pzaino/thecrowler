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
	"math/rand/v2"
	"strconv"
	"strings"

	cmn "github.com/pzaino/thecrowler/pkg/common"
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
