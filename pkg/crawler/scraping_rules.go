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

/////////
// This file is used as a wrapper to the scrapper package, to avoid circular dependencies.
/////////

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	rules "github.com/pzaino/thecrowler/pkg/ruleset"
	scraper "github.com/pzaino/thecrowler/pkg/scraper"

	"github.com/tebeka/selenium"
)

const (
	errExecutingScraping = "error executing scraping rule: %v"
)

// processScrapingRules processes the scraping rules
func processScrapingRules(wd *selenium.WebDriver, ctx *processContext, url string) string {
	cmn.DebugMsg(cmn.DbgLvlDebug2, "Starting to search and process CROWler Scraping rules...")

	scrapedDataDoc := ""

	// Run Scraping Rules if any
	if ctx.source.Config != nil {
		// Execute the CROWler rules
		cmn.DebugMsg(cmn.DbgLvlDebug, "Executing CROWler configured Scraping rules...")
		// Execute the rules
		if strings.TrimSpace(string((*ctx.source.Config))) == "{\"config\":\"default\"}" {
			runDefaultScrapingRules(wd, ctx)
		} else {
			configStr := string((*ctx.source.Config))
			cmn.DebugMsg(cmn.DbgLvlDebug, "Configuration: %v", configStr)
		}
	} else {
		// Check for rules based on the URL
		cmn.DebugMsg(cmn.DbgLvlDebug, "Executing CROWler URL based Scraping rules...")
		// If the URL matches a rule, execute it
		scrapedDataDoc += executeScrapingRulesByURL(wd, ctx, url)
	}

	return scrapedDataDoc
}

func executeScrapingRulesByURL(wd *selenium.WebDriver, ctx *processContext, url string) string {
	scrapedDataDoc := ""

	rs, err := ctx.re.GetRulesetByURL(url)
	if err == nil && rs != nil {
		// Execute all the rules in the ruleset
		scrapedDataDoc += executeScrapingRulesInRuleset(rs, wd)
	} else {
		rg, err := ctx.re.GetRuleGroupByURL(url)
		if err == nil && rg != nil {
			// Execute all the rules in the rule group
			scrapedDataDoc += executeScrapingRulesInRuleGroup(rg, wd)
		}
	}

	return scrapedDataDoc
}

func executeScrapingRulesInRuleset(rs *rules.Ruleset, wd *selenium.WebDriver) string {
	scrapedDataDoc := ""
	for _, r := range rs.GetScrapingRules() {
		// Execute the rule
		scrapedData, err := executeScrapingRule(&r, wd)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, errExecutingScraping, err)
		}
		scrapedDataDoc += scrapedData
	}
	return scrapedDataDoc
}

func executeScrapingRulesInRuleGroup(rg *rules.RuleGroup, wd *selenium.WebDriver) string {
	scrapedDataDoc := ""
	for _, r := range rg.GetScrapingRules() {
		// Execute the rule
		scrapedData, err := executeScrapingRule(&r, wd)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, errExecutingScraping, err)
		}
		scrapedDataDoc += scrapedData
	}
	return scrapedDataDoc
}

// executeScrapingRule executes a single ScrapingRule
func executeScrapingRule(r *rules.ScrapingRule, wd *selenium.WebDriver) (string, error) {
	var jsonDocument string

	// Execute Wait condition first
	if len(r.WaitConditions) != 0 {
		for _, wc := range r.WaitConditions {
			err := executeWaitCondition(&wc, wd)
			if err != nil {
				return "", fmt.Errorf("error executing wait condition: %v", err)
			}
		}
	}

	// Execute the scraping rule
	if (len(r.Conditions) == 0) || checkActionConditions(r.Conditions, wd) {
		extractedData := scraper.ApplyRule(r, wd)

		// Transform the extracted data into a JSON document
		jsonData, err := json.Marshal(extractedData)
		if err != nil {
			return "", fmt.Errorf("error marshalling JSON: %v", err)
		}

		// Convert bytes to string to get a JSON document
		jsonDocument = string(jsonData)
	}

	return jsonDocument, nil
}

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
					UrlPatterns: []string{url},
				},
				Rules: []string{""},
			},
		},
	}
}

func runDefaultScrapingRules(wd *selenium.WebDriver, ctx *processContext) {
	// Execute the default scraping rules
	cmn.DebugMsg(cmn.DbgLvlDebug, "Executing default scraping rules...")

	// Get the default scraping rules
	url, err := (*wd).CurrentURL()
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error getting the current URL: %v", err)
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
		scrapedDataDoc += executeRulesInExecutionPlan(r, wd, ctx)
	}
}

func executeRulesInExecutionPlan(epi cfg.ExecutionPlanItem, wd *selenium.WebDriver, ctx *processContext) string {
	var scrapedDataDoc string
	// Get the rule
	for _, ruleName := range epi.Rules {
		if ruleName == "" {
			continue
		}
		rule, err := ctx.re.GetScrapingRuleByName(ruleName)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Error getting scraping rule: %v", err)
		} else {
			// Execute the rule
			scrapedData, err := executeScrapingRule(rule, wd)
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
	if len(conditions.UrlPatterns) > 0 {
		for _, pattern := range conditions.UrlPatterns {
			if strings.Contains(url, pattern) {
				canProceed = true
			} else {
				canProceed = false
			}
		}
	}
	return canProceed
}

/*
// checkScrapingConditions checks all types of conditions: Scraping and Config Conditions
// These are page related conditions, for instance check if an element is present
// or if the page is in the desired language etc.
func checkScrapingConditions(conditions map[string]interface{}, wd *selenium.WebDriver) bool {
	canProceed := true
	// Check the additional conditions
	if len(conditions) > 0 {
		// Check if the page contains a specific element
		if _, ok := conditions["element"]; ok {
			// Check if the element is present
			_, err := (*wd).FindElement(selenium.ByCSSSelector, conditions["element"].(string))
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
	}
	return canProceed
}
*/
