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
	"encoding/json"
	"fmt"
	"regexp"
	"sync"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	httpi "github.com/pzaino/thecrowler/pkg/httpinfo"
	neti "github.com/pzaino/thecrowler/pkg/netinfo"
	rules "github.com/pzaino/thecrowler/pkg/ruleset"
	vdi "github.com/pzaino/thecrowler/pkg/vdi"
)

// ProcessContext is a struct that holds the context of the crawling process
// It's used to pass data between functions and goroutines and holds the
// DB index of the source page after it's indexed.
type ProcessContext struct {
	pStatus              int                       // Process status
	SelID                int                       // The Selenium ID
	SelInstance          vdi.SeleniumInstance      // The Selenium instance
	WG                   *sync.WaitGroup           // The Caller's WaitGroup
	fpIdx                uint64                    // The index of the source page after it's indexed
	config               cfg.Config                // The configuration object (from the config package)
	db                   *cdb.Handler              // The database handler
	wd                   vdi.WebDriver             // The Selenium WebDriver
	linksMutex           cmn.SafeMutex             // Mutex to protect the newLinks slice
	newLinks             []LinkItem                // The new links found during the crawling process
	source               *cdb.Source               // The source to crawl
	srcCfg               map[string]interface{}    // Will store the source Config in an Unmarshaled format
	compiledUURLs        map[string]*regexp.Regexp // Compiled regex patterns for unwanted URLs
	wg                   sync.WaitGroup            // WaitGroup to wait for all page workers to finish
	wgNetInfo            sync.WaitGroup            // WaitGroup to wait for network info to finish
	sel                  *vdi.Pool                 // The Selenium instances channel (sel *chan vdi.SeleniumInstance)
	ni                   *neti.NetInfo             // The network information of the web page
	hi                   *httpi.HTTPDetails        // The HTTP header information of the web page
	re                   *rules.RuleEngine         // The rule engine
	getURLMutex          cmn.SafeMutex             // Mutex to protect the getURLContent function
	accessVDIMutex       cmn.SafeMutex             // Mutex to protect access to the VDI instance
	closeSession         cmn.SafeMutex             // Mutex to protect the closeSession function
	visitedLinks         map[string]bool           // Map to keep track of visited links
	userURLPatterns      []string                  // User-defined URL patterns
	userURLBlockPatterns []string                  // User-defined URL block patterns
	Status               *Status                   // Status of the crawling process
	CollectedCookies     map[string]interface{}    // Collected cookies
	VDIReturned          bool                      // Flag to indicate if the VDI instance was returned
	SelClosed            bool                      // Flag to indicate if the Selenium instance was closed
	VDIOperationMutex    sync.Mutex                // Mutex to protect the VDI operations
	RefreshCrawlingTimer func()                    // Function to refresh the crawling timer
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

// LoadSourceConfiguration extracts and prepares the source configuration
// from the database Source entry and populates the ProcessContext fields.
func (ctx *ProcessContext) LoadSourceConfiguration() error {
	if ctx.source == nil || ctx.source.Config == nil {
		cmn.DebugMsg(cmn.DbgLvlDebug2, "[ProcessContext] No source configuration found.")
		return nil
	}

	sourceConfig := make(map[string]interface{})
	err := json.Unmarshal(*ctx.source.Config, &sourceConfig)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "[ProcessContext] Unmarshalling source configuration: %v", err)
		return err
	}

	ctx.srcCfg = sourceConfig
	cmn.DebugMsg(cmn.DbgLvlDebug3, "[ProcessContext] Source configuration extracted: %v", ctx.srcCfg)

	// Extract crawling_config section if present
	crawlingConfig, _ := ctx.srcCfg["crawling_config"].(map[string]interface{})

	// Process unwanted_urls patterns
	if unwantedURLs, ok := crawlingConfig["unwanted_urls"]; ok {
		if unwantedURLsSlice, ok := unwantedURLs.([]interface{}); ok {
			ctx.compiledUURLs = make(map[string]*regexp.Regexp)
			for _, pattern := range unwantedURLsSlice {
				if strPattern, ok := pattern.(string); ok {
					re, err := regexp.Compile(strPattern)
					if err != nil {
						cmn.DebugMsg(cmn.DbgLvlError, "Compiling unwanted URL pattern '%s': %v", strPattern, err)
						continue
					}
					ctx.compiledUURLs[strPattern] = re
				}
			}
			cmn.DebugMsg(cmn.DbgLvlDebug3, "[ProcessContext] Compiled unwanted URL patterns: %d", len(ctx.compiledUURLs))
		}
	}

	// Extract URL patterns and block patterns from execution_plan
	ctx.userURLPatterns = []string{}
	ctx.userURLBlockPatterns = []string{}

	if executionPlanRaw, ok := sourceConfig["execution_plan"]; ok {
		if executionPlan, ok := executionPlanRaw.([]interface{}); ok {
			for _, planRaw := range executionPlan {
				if plan, ok := planRaw.(map[string]interface{}); ok {
					if conditionsRaw, ok := plan["conditions"]; ok {
						if conditions, ok := conditionsRaw.(map[string]interface{}); ok {
							if urlPatternsRaw, ok := conditions["url_patterns"]; ok {
								if urlPatterns, ok := urlPatternsRaw.([]interface{}); ok {
									for _, pattern := range urlPatterns {
										if strPattern, ok := pattern.(string); ok {
											ctx.userURLPatterns = append(ctx.userURLPatterns, strPattern)
										}
									}
								}
							}
							if urlBlockPatternsRaw, ok := conditions["url_block_patterns"]; ok {
								if urlBlockPatterns, ok := urlBlockPatternsRaw.([]interface{}); ok {
									for _, pattern := range urlBlockPatterns {
										if strPattern, ok := pattern.(string); ok {
											ctx.userURLBlockPatterns = append(ctx.userURLBlockPatterns, strPattern)
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

	cmn.DebugMsg(cmn.DbgLvlDebug3,
		"[ProcessContext] Loaded %d include and %d exclude URL patterns",
		len(ctx.userURLPatterns), len(ctx.userURLBlockPatterns))

	return nil
}
