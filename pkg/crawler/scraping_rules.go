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
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	rules "github.com/pzaino/thecrowler/pkg/ruleset"
	scraper "github.com/pzaino/thecrowler/pkg/scraper"
	vdi "github.com/pzaino/thecrowler/pkg/vdi"
)

const (
	errExecutingScraping = "executing scraping rule: %v"
	strFalse             = "false"
	strTrue              = "true"
)

// processScrapingRules processes the scraping rules
func processScrapingRules(wd *vdi.WebDriver, ctx *ProcessContext, url string, pageCache *PageInfo) (string, error) {
	cmn.DebugMsg(cmn.DbgLvlDebug2, "[DEBUG-ProcScrapingRules] Starting to search and process CROWler Scraping rules...")

	scrapedDataDoc := ""

	// Run Scraping Rules if any
	if ctx.source.Config != nil {
		// Execute the CROWler rules
		cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-ProcScrapingRules] Executing CROWler configured Scraping rules (if any)...")
		// Execute the rules
		if strings.TrimSpace(string((*ctx.source.Config))) == "{\"config\":\"default\"}" {
			addScrapedDataToDocument(&scrapedDataDoc, runDefaultScrapingRules(wd, ctx, pageCache))
		} else {
			configStr := string((*ctx.source.Config))
			cmn.DebugMsg(cmn.DbgLvlDebug5, "[DEBUG-ProcScrapingRules] Source custom configuration detected: %v", configStr)
		}
	}

	// Check for rules based on the URL
	cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-ProcScrapingRules] Executing CROWler URL-based Scraping rules (if any)...")
	// If the URL matches a rule, execute it
	data, err := executeScrapingRulesByURL(wd, ctx, url, pageCache)
	addScrapedDataToDocument(&scrapedDataDoc, data)

	// Check if scrapedDataDOc already has "{" and "}" at the beginning and end
	if strings.HasPrefix(scrapedDataDoc, "{") && strings.HasSuffix(scrapedDataDoc, "}") {
		scrapedDataDoc = scrapedDataDoc[1:]
		scrapedDataDoc = scrapedDataDoc[:len(scrapedDataDoc)-1]
	}

	// log scraped data for debugging purposes
	cmn.DebugMsg(cmn.DbgLvlDebug5, "[DEBUG-ProcScrapingRules] Scraped data (at processScrapingRules level): {%v}", scrapedDataDoc)

	return "{" + scrapedDataDoc + "}", err
}

func addScrapedDataToDocument(scrapedDataDoc *string, newScrapedData string) {
	if (*scrapedDataDoc) == "" {
		(*scrapedDataDoc) = newScrapedData
	} else {
		(*scrapedDataDoc) += "," + newScrapedData
	}
}

func executeScrapingRulesByURL(wd *vdi.WebDriver, ctx *ProcessContext, url string, pageCache *PageInfo) (string, error) {
	scrapedDataDoc := ""
	var errList []error

	// Retrieve the rule group by URL
	rgl, err := ctx.re.GetAllRulesGroupByURL(url)
	if (err == nil) && (len(rgl) != 0) {
		cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-FindRules] Found %d Rulegroups for URL with the URL '%v'", len(rgl), url) // We can't count the number of Scraping rules in the returned RGL
		for _, rg := range rgl {
			// Execute all the rules in the rule group (the following function also set the Env and clears it)
			var data string
			data, err = executeScrapingRulesInRuleGroup(ctx, rg, wd, pageCache)
			// Add the data to the document
			data = strings.TrimSpace(data)
			if data != "" && data != "{}" && data != strFalse && data != strTrue {
				// Check if data has double "{{" and "}}" at the beginning and end
				if strings.HasPrefix(data, "{{") && strings.HasSuffix(data, "}}") {
					data = data[1:]
					data = data[:len(data)-1]
				}
				addScrapedDataToDocument(&scrapedDataDoc, data)
			}
		}
	} else {
		cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-FindRules] No rule group found for URL with either rulegroup name or rulegroup url: %v", url)
	}
	if err != nil {
		errList = append(errList, fmt.Errorf("%v", err))
	}

	// Retrieve the ruleset by URL
	rsl, err := ctx.re.GetAllRulesetByURL(url)
	if (err == nil) && (len(rsl) != 0) {
		for _, rs := range rsl {
			// Execute all the rules in the ruleset
			var data string
			data, err = executeScrapingRulesInRuleset(ctx, rs, wd, pageCache)
			addScrapedDataToDocument(&scrapedDataDoc, data)
		}
	} else {
		cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-FindRules] No ruleset found for URL with the URL in the ruleset name: '%v'", url)
	}
	if err != nil {
		errList = append(errList, fmt.Errorf("%v", err))
	}

	// log scraped data for debugging purposes
	cmn.DebugMsg(cmn.DbgLvlDebug5, "[DEBUG-FindRules] Scraped data (at executeScrapingRulesByURL level): {%v}", scrapedDataDoc)

	// Join all errors
	errStr := ""
	for _, e := range errList {
		errStr += e.Error() + "\n"
	}
	if errStr != "" {
		return scrapedDataDoc, fmt.Errorf("%v", errStr)
	}

	return scrapedDataDoc, nil
}

func executeScrapingRulesInRuleset(ctx *ProcessContext, rs *rules.Ruleset, wd *vdi.WebDriver, pageCache *PageInfo) (string, error) {
	scrapedDataDoc := ""

	// Setup the environment
	rs.SetEnv(ctx.GetContextID())

	for _, r := range rs.GetAllEnabledScrapingRules() {
		// Execute the rule
		cmn.DebugMsg(cmn.DbgLvlDebug3, "Executing rule: %v", r.RuleName)
		scrapedData, err := executeScrapingRule(ctx, &r, wd, pageCache)
		if err != nil {
			if strings.Contains(err.Error(), "Critical") {
				return "", fmt.Errorf("%v", err)
			}
			cmn.DebugMsg(cmn.DbgLvlError, errExecutingScraping, err)
		}
		addScrapedDataToDocument(&scrapedDataDoc, scrapedData)
	}

	// Reset the environment
	cmn.KVStore.DeleteNonPersistentByCID(ctx.GetContextID())

	return scrapedDataDoc, nil
}

func executeScrapingRulesInRuleGroup(ctx *ProcessContext, rg *rules.RuleGroup, wd *vdi.WebDriver, pageCache *PageInfo) (string, error) {
	scrapedDataDoc := ""

	// Set the environment
	rg.SetEnv(ctx.GetContextID())

	var err error
	for _, r := range rg.GetScrapingRules() {
		cmn.DebugMsg(cmn.DbgLvlDebug2, "[DEBUG-FindRules] Executing Rule: %v", r.RuleName)
		// Execute the rule
		var scrapedData string
		scrapedData, err = executeScrapingRule(ctx, &r, wd, pageCache)
		addScrapedDataToDocument(&scrapedDataDoc, scrapedData)
	}

	// Apply the post-processing steps to the extracted data
	if len(rg.PostProcessing) != 0 {
		cmn.DebugMsg(cmn.DbgLvlDebug2, "Applying Rulesgroup's post-processing steps to the extracted data")
		// Convert the JSON data to a map
		var extractedData map[string]interface{}
		scrapedDataDoc = cmn.SanitizeJSON(scrapedDataDoc)
		err = json.Unmarshal([]byte("{"+scrapedDataDoc+"}"), &extractedData)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "unmarshalling JSON: %v, for JSON: %v", err, scrapedDataDoc)
			return scrapedDataDoc, fmt.Errorf("unmarshalling JSON: %v", err)
		}
		// Convert the map to JSON
		data := cmn.ConvertMapToJSON(extractedData)
		for i := range rg.PostProcessing {
			updated, stepErr := scraper.ApplyPostProcessingStep(context.Background(), newScraperRuntimeAdapterWithPageInfo(ctx, nil, pageCache), "", 0, &rg.PostProcessing[i], data)
			if stepErr == nil {
				data = updated
			}
		}

		// Check if data has double "{{" and "}}" at the beginning and end
		if strings.HasPrefix(string(data), "{{") && strings.HasSuffix(string(data), "}}") {
			data = data[1:]
			data = data[:len(data)-1]
		}

		// Unmarshal the JSON data back into a map
		err = json.Unmarshal(data, &extractedData)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "unmarshalling JSON: %v, for JSON: %v", err, data)
		}
		// Convert the map back to JSON string
		scrapedDataDoc = string(cmn.ConvertMapToJSON(extractedData))
	}

	// Reset the environment
	cmn.KVStore.DeleteNonPersistentByCID(ctx.GetContextID())

	return scrapedDataDoc, err
}

// executeScrapingRule delegates the complete rule lifecycle to the shared scraper runtime.
func executeScrapingRule(ctx *ProcessContext, rule *rules.ScrapingRule, wd *vdi.WebDriver, pageCache *PageInfo) (string, error) {
	return scraper.ExecuteRule(context.Background(), newScraperRuntimeAdapterWithPageInfo(ctx, wd, pageCache), rule, wd)
}

// DefaultCrawlingConfig returns a default configuration for crawling a page
func DefaultCrawlingConfig(url string) cfg.SourceConfig {
	return cfg.SourceConfig{
		FormatVersion: "1.0",
		Author:        "Your Name",
		CreatedAt:     time.Now(),
		Description:   "Default configuration",
		SourceName:    "Example Source",
		CrawlingConfig: cfg.CrawlingConfig{
			Site: url,
		},
		ExecutionPlan: []cfg.ExecutionPlanItem{
			{
				Label: "Default Execution Plan",
				Conditions: cfg.Condition{
					URLPatterns: []string{url},
				},
				Rules: []string{""},
			},
		},
	}
}

func runDefaultScrapingRules(wd *vdi.WebDriver, ctx *ProcessContext, pageCache *PageInfo) string {
	// Execute the default scraping rules
	cmn.DebugMsg(cmn.DbgLvlDebug, "Executing default scraping rules...")

	// Get the default scraping rules
	url, err := (*wd).CurrentURL()
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "getting the current URL: %v", err)
		url = ""
	}
	rs := DefaultCrawlingConfig(url)
	// Execute all the rules in the ruleset
	var scrapedDataDoc string
	for _, r := range rs.ExecutionPlan {
		// Check the conditions
		if !checkScrapingPreConditions(r.Conditions, url) {
			continue
		}
		addScrapedDataToDocument(&scrapedDataDoc, executeRulesInExecutionPlan(r, wd, ctx, pageCache))
	}
	return scrapedDataDoc
}

func executeRulesInExecutionPlan(epi cfg.ExecutionPlanItem, wd *vdi.WebDriver, ctx *ProcessContext, pageCache *PageInfo) string {
	var scrapedDataDoc string
	// Get the rule
	for _, ruleName := range epi.Rules {
		if ruleName == "" {
			continue
		}
		rule, err := ctx.re.GetScrapingRuleByName(ruleName)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "getting scraping rule: %v", err)
		} else {
			// Execute the rule
			scrapedData, err := executeScrapingRule(ctx, rule, wd, pageCache)
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, errExecutingScraping, err)
			} else {
				scrapedDataDoc += scrapedData
				cmn.DebugMsg(cmn.DbgLvlDebug3, "Scraped data: %v", scrapedDataDoc)
			}
		}
	}
	return scrapedDataDoc
}

// checkScrapingPreConditions checks if the pre conditions are met
// for example if the page URL is listed in the list of URLs
// for which this rule is valid.
func checkScrapingPreConditions(conditions cfg.Condition, url string) bool {
	canProceed := true
	// Check the URL patterns
	if len(conditions.URLPatterns) > 0 {
		for _, pattern := range conditions.URLPatterns {
			if strings.Contains(url, pattern) {
				canProceed = true
			} else {
				canProceed = false
			}
		}
	}
	return canProceed
}

// checkScrapingConditions checks all types of conditions: Scraping and Config Conditions
// These are page related conditions, for instance check if an element is present
// or if the page is in the desired language etc.
func checkScrapingConditions(ctx *ProcessContext, conditions map[string]interface{}, wd *vdi.WebDriver) bool {
	canProceed := true
	// Check the additional conditions
	if len(conditions) > 0 {
		// Check if the page contains a specific element
		if _, ok := conditions["element"]; ok {
			// Check if the element is present
			_, err := (*wd).FindElement(vdi.ByCSSSelector, conditions["element"].(string))
			if err != nil {
				canProceed = false
			}
		}
		// If a language condition is present, check if the page is in the correct language
		if _, ok := conditions["language"]; ok {
			// Get the page language
			lang, err := (*wd).ExecuteScript("return document.documentElement.lang", nil)
			if err != nil {
				canProceed = false
			}
			// Check if the language is correct
			if lang != conditions["language"] {
				canProceed = false
			}
		}
		if envConditions, ok := conditions["env"]; ok {
			if !checkEnvironmentCondition(ctx, envConditions) {
				canProceed = false
			}
		}
	}
	return canProceed
}
