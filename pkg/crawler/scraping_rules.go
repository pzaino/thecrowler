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
	"golang.org/x/net/html"

	"github.com/tebeka/selenium"
)

const (
	errExecutingScraping = "executing scraping rule: %v"
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
	}

	// Check for rules based on the URL
	cmn.DebugMsg(cmn.DbgLvlDebug, "Executing CROWler URL-based Scraping rules (if any)...")
	// If the URL matches a rule, execute it
	scrapedDataDoc += executeScrapingRulesByURL(wd, ctx, url)

	return scrapedDataDoc
}

func executeScrapingRulesByURL(wd *selenium.WebDriver, ctx *processContext, url string) string {
	scrapedDataDoc := ""

	// Retrieve the rule group by URL
	rg, err := ctx.re.GetRuleGroupByURL(url)
	if err == nil && rg != nil {
		// Execute all the rules in the rule group
		scrapedDataDoc += executeScrapingRulesInRuleGroup(ctx, rg, wd)
	} else {
		cmn.DebugMsg(cmn.DbgLvlDebug, "No rule group found for URL: %v", url)
	}

	// Retrieve the ruleset by URL
	rs, err := ctx.re.GetRulesetByURL(url)
	if err == nil && rs != nil {
		// Execute all the rules in the ruleset
		scrapedDataDoc += executeScrapingRulesInRuleset(ctx, rs, wd)
	} else {
		cmn.DebugMsg(cmn.DbgLvlDebug, "No ruleset found for URL: %v", url)
	}

	return scrapedDataDoc
}

func executeScrapingRulesInRuleset(ctx *processContext, rs *rules.Ruleset, wd *selenium.WebDriver) string {
	scrapedDataDoc := ""
	for _, r := range rs.GetAllEnabledScrapingRules() {
		// Execute the rule
		cmn.DebugMsg(cmn.DbgLvlDebug3, "Executing rule: %v", r.RuleName)
		scrapedData, err := executeScrapingRule(ctx, &r, wd)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, errExecutingScraping, err)
		}
		scrapedDataDoc += scrapedData
	}
	return scrapedDataDoc
}

func executeScrapingRulesInRuleGroup(ctx *processContext, rg *rules.RuleGroup, wd *selenium.WebDriver) string {
	scrapedDataDoc := ""
	for _, r := range rg.GetScrapingRules() {
		// Execute the rule
		scrapedData, err := executeScrapingRule(ctx, &r, wd)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, errExecutingScraping, err)
		}
		scrapedDataDoc += scrapedData
	}
	return scrapedDataDoc
}

// executeScrapingRule executes a single ScrapingRule
func executeScrapingRule(ctx *processContext, r *rules.ScrapingRule,
	wd *selenium.WebDriver) (string, error) {
	var jsonDocument string

	// Execute Wait condition first
	if len(r.WaitConditions) != 0 {
		err := executeWaitConditions(ctx, r.WaitConditions, wd)
		if err != nil {
			return "", fmt.Errorf("executing wait conditions: %v", err)
		}
	}

	// Execute the scraping rule
	if shouldExecuteScrapingRule(r, wd) {
		extractedData := ApplyRule(ctx, r, wd)
		processedData := processExtractedData(extractedData)
		jsonData, err := json.Marshal(processedData)
		if err != nil {
			return "", fmt.Errorf("marshalling JSON: %v", err)
		}
		if len(r.PostProcessing) != 0 {
			runPostProcessingSteps(ctx, &r.PostProcessing, &jsonData)
		}
		jsonDocument = string(jsonData)
	}

	return jsonDocument, nil
}

func executeWaitConditions(ctx *processContext, conditions []rules.WaitCondition, wd *selenium.WebDriver) error {
	for _, wc := range conditions {
		err := executeWaitCondition(ctx, &wc, wd)
		if err != nil {
			return fmt.Errorf("executing wait condition: %v", err)
		}
	}
	return nil
}

func shouldExecuteScrapingRule(r *rules.ScrapingRule, wd *selenium.WebDriver) bool {
	return len(r.Conditions) == 0 || checkScrapingConditions(r.Conditions, wd)
}

func processExtractedData(extractedData map[string]interface{}) map[string]interface{} {
	processedData := make(map[string]interface{})
	for key, data := range extractedData {
		dataStr := fmt.Sprintf("%v", data)
		if StrIsHTML(dataStr) {
			jsonData, err := ProcessHtmlToJson(dataStr)
			if err != nil {
				data = strings.ReplaceAll(dataStr, "\"", "\\\"")
				processedData[key] = data
			} else {
				processedData[key] = jsonData
			}
		} else {
			processedData[key] = data
		}
	}
	return processedData
}

// StrIsHTML checks if the given string could be HTML by trying to parse it.
func StrIsHTML(s string) bool {
	// Minimal check to quickly filter out definitely non-HTML content
	if !strings.Contains(s, "<") && !strings.Contains(s, ">") {
		return false
	}

	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		return false // If parsing fails, it's likely not HTML
	}

	var hasElement bool
	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data != "html" && n.Data != "head" && n.Data != "body" {
			hasElement = true
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(doc)
	return hasElement
}

// runPostProcessingSteps runs the post processing steps
func runPostProcessingSteps(ctx *processContext, pps *[]rules.PostProcessingStep, jsonData *[]byte) {
	for _, pp := range *pps {
		// Execute the post processing step
		ApplyPostProcessingStep(ctx, &pp, jsonData)
	}
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
			cmn.DebugMsg(cmn.DbgLvlError, "getting scraping rule: %v", err)
		} else {
			// Execute the rule
			scrapedData, err := executeScrapingRule(ctx, rule, wd)
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

func parseHTML(htmlData string) ([]map[string]interface{}, error) {
	doc, err := html.Parse(strings.NewReader(htmlData))
	if err != nil {
		return nil, err
	}
	var items []map[string]interface{}
	parseNode(doc, nil, &items)
	return items, nil
}

func parseNode(n *html.Node, currentItem map[string]interface{}, items *[]map[string]interface{}) {
	if n.Type == html.ElementNode {
		newItem := createNewItem(n)
		if len(newItem) > 0 {
			if currentItem != nil {
				addChild(currentItem, newItem)
			} else {
				addItem(items, newItem)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			parseNode(c, newItem, items)
		}
	} else if n.Type == html.TextNode && strings.TrimSpace(n.Data) != "" {
		if currentItem != nil && len(currentItem) == 0 {
			currentItem["text"] = strings.TrimSpace(n.Data)
		}
	}
}

func createNewItem(n *html.Node) map[string]interface{} {
	newItem := make(map[string]interface{})
	for _, a := range n.Attr {
		newItem[a.Key] = a.Val
	}
	if n.FirstChild != nil && n.FirstChild.Type == html.TextNode {
		newItem["text"] = strings.TrimSpace(n.FirstChild.Data)
	}
	return newItem
}

func addChild(currentItem map[string]interface{}, newItem map[string]interface{}) {
	if _, exists := currentItem["children"]; !exists {
		currentItem["children"] = []map[string]interface{}{}
	}
	currentItem["children"] = append(currentItem["children"].([]map[string]interface{}), newItem)
}

func addItem(items *[]map[string]interface{}, newItem map[string]interface{}) {
	*items = append(*items, newItem)
}

func toJson(data []map[string]interface{}) (string, error) {
	jsonData, err := json.MarshalIndent(data, "", "    ")
	if err != nil {
		return "", err
	}
	return string(jsonData), nil
}

func ProcessHtmlToJson(htmlData string) (string, error) {
	items, err := parseHTML(htmlData)
	if err != nil {
		return "", err
	}
	jsonOutput, err := toJson(items)
	if err != nil {
		return "", err
	}
	return jsonOutput, nil
}
