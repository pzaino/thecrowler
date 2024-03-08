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
	"fmt"
	"strings"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	rules "github.com/pzaino/thecrowler/pkg/ruleset"
	"github.com/tebeka/selenium"
)

// processScrapingRules processes the scraping rules
func processScrapingRules(wd *selenium.WebDriver, ctx *processContext, url string) string {
	cmn.DebugMsg(cmn.DbgLvlDebug2, "Starting to search and process CROWler Scraping rules...")

	scrapedDataDoc := ""

	// Run Scraping Rules if any
	if ctx.source.Config != nil && strings.TrimSpace(string((*ctx.source.Config))) != "{\"config\":\"default\"}" {
		// Execute the CROWler rules
		cmn.DebugMsg(cmn.DbgLvlDebug, "Executing CROWler configured Scraping rules...")
		// Execute the rules
		configStr := string((*ctx.source.Config))
		cmn.DebugMsg(cmn.DbgLvlDebug, "Configuration: %v", configStr)
	} else {
		// Check for rules based on the URL
		cmn.DebugMsg(cmn.DbgLvlDebug, "Executing CROWler URL based Scraping rules...")
		// If the URL matches a rule, execute it
		rs, err := ctx.re.GetRulesetByURL(url)
		if err == nil {
			if rs != nil {
				// Execute all the rules in the ruleset
				scrapedDataDoc += executeScrapingRulesInRuleset(rs, wd)
			}
		} else {
			rg, err := ctx.re.GetRuleGroupByURL(url)
			if err == nil {
				if rg != nil {
					// Execute all the rules in the rule group
					scrapedDataDoc += executeScrapingRulesInRuleGroup(rg, wd)
				}
			}
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
			cmn.DebugMsg(cmn.DbgLvlError, "Error executing scraping rule: %v", err)
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
			cmn.DebugMsg(cmn.DbgLvlError, "Error executing scraping rule: %v", err)
		}
		scrapedDataDoc += scrapedData
	}
	return scrapedDataDoc
}

// executeScrapingRule executes a single ScrapingRule
func executeScrapingRule(r *rules.ScrapingRule, wd *selenium.WebDriver) (string, error) {
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

	return "", fmt.Errorf("rule '%s' not supported", r.RuleName)
}
