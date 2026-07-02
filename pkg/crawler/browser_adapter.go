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
	"errors"
	"fmt"
	"time"

	browser "github.com/pzaino/thecrowler/pkg/browser"
	cmn "github.com/pzaino/thecrowler/pkg/common"
	"github.com/pzaino/thecrowler/pkg/vdi"
)

// Crawler-specific browser setup composes the shared lifecycle mechanics with
// URL filtering, XHR collection, and source policy.

func cleanUpBrowser(wd vdi.WebDriver) error {
	if err := browser.Cleanup(wd, browser.CleanupOptions{}); err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug3, "failed to clean up browser: %v", err)
		return err
	}
	return nil
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
	if ctx.config.Crawler.ResetCookiesPolicy == "always" {
		err = changeUserAgent(&(ctx.wd), ctx)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "changing UserAgent: %v", err)
		}
	}

	err = browser.Setup(context.Background(), wd, browser.SetupOptions{
		InitialURL:               "about:blank",
		SetGPUPatch:              ctx.config.Crawler.SetVDIGPUPatch,
		ReinforceBrowserSettings: true,
	})
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "setting up browser session: %v", err)
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

	// XHR collection is crawler behavior, not browser session setup.
	if ctx.config.Crawler.CollectXHR {
		err = enableCDPNetworkLogging(ctx.wd, getCDPDelay(ctx.config))
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "adding XHR Hook: %v", err)
		}
	}
}

func waitForDomComplete(wd vdi.WebDriver, timeout time.Duration) error {
	return browser.WaitForDOMReady(context.Background(), wd, timeout, 200*time.Millisecond)
}

func getWithTimeout(wd *vdi.WebDriver, url string, timeout time.Duration) error {
	if wd == nil {
		return errors.New("[critical] WebDriver is nil")
	}
	if err := browser.Navigate(context.Background(), *wd, url, timeout); err != nil {
		return fmt.Errorf("[critical] %w", err)
	}
	return nil
}
