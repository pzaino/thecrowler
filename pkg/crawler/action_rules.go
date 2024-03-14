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
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	rules "github.com/pzaino/thecrowler/pkg/ruleset"
	"github.com/tebeka/selenium"
)

func processActionRules(wd *selenium.WebDriver, ctx *processContext, url string) {
	cmn.DebugMsg(cmn.DbgLvlDebug2, "Starting to search and process CROWler Action rules...")
	// Run Action Rules if any
	if ctx.source.Config != nil {
		// Execute the CROWler rules
		cmn.DebugMsg(cmn.DbgLvlDebug, "Executing CROWler configured Action rules...")
		// Execute the rules
		if strings.TrimSpace(string((*ctx.source.Config))) == "{\"config\":\"default\"}" {
			runDefaultActionRules(wd, ctx)
		} else {
			configStr := string((*ctx.source.Config))
			cmn.DebugMsg(cmn.DbgLvlDebug, "Configuration: %v", configStr)
		}
	} else {
		// Check for rules based on the URL
		cmn.DebugMsg(cmn.DbgLvlDebug, "Executing CROWler URL based Action rules...")
		// If the URL matches a rule, execute it
		processURLRules(wd, ctx, url)
	}
}

func processURLRules(wd *selenium.WebDriver, ctx *processContext, url string) {
	rs, err := ctx.re.GetRulesetByURL(url)
	if err == nil {
		if rs != nil {
			cmn.DebugMsg(cmn.DbgLvlDebug, "Executing ruleset: %s", rs.Name)
			// Execute all the rules in the ruleset
			executeActionRules(rs.GetActionRules(), wd)
		}
	} else {
		rg, err := ctx.re.GetRuleGroupByURL(url)
		if err == nil {
			if rg != nil {
				cmn.DebugMsg(cmn.DbgLvlDebug, "Executing rule group: %s", rg.GroupName)
				// Execute all the rules in the rule group
				executeActionRules(rg.GetActionRules(), wd)
			}
		}
	}
}

func executeActionRules(rules []rules.ActionRule, wd *selenium.WebDriver) {
	for _, r := range rules {
		// Execute the rule
		err := executeActionRule(&r, wd)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Error executing action rule: %v", err)
		}
	}
}

// executeActionRule executes a single ActionRule
func executeActionRule(r *rules.ActionRule, wd *selenium.WebDriver) error {
	// Execute Wait condition first
	if len(r.WaitConditions) != 0 {
		for _, wc := range r.WaitConditions {
			// Execute the wait condition
			err := executeWaitCondition(&wc, wd)
			if err != nil {
				return err
			}
		}
	}
	// Execute the action based on the ActionType
	if (len(r.Conditions) == 0) || checkActionConditions(r.Conditions, wd) {
		switch strings.ToLower(strings.TrimSpace(r.ActionType)) {
		case "click":
			return executeActionClick(r, wd)
		case "scroll":
			return executeActionScroll(r, wd)
		case "input_text":
			return executeActionInput(r, wd)
		case "execute_javascript":
			return executeActionJS(r, wd)
		}
		return fmt.Errorf("action type not supported: %s", r.ActionType)
	}
	return nil
}

// executeWaitCondition is responsible for executing a "wait" condition
func executeWaitCondition(r *rules.WaitCondition, wd *selenium.WebDriver) error {
	// Execute the wait condition
	switch strings.ToLower(strings.TrimSpace(r.ConditionType)) {
	case "element":
		return nil
	case "delay":
		return nil
	case "custom_js":
		_, err := (*wd).ExecuteScript(r.CustomJS, nil)
		return err
	default:
		return fmt.Errorf("wait condition not supported: %s", r.ConditionType)
	}
}

// executeActionClick is responsible for executing a "click" action
func executeActionClick(r *rules.ActionRule, wd *selenium.WebDriver) error {
	// Find the element
	wdf, _, err := findElementBySelectorType(wd, r.Selectors)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug3, "No element '%v' found.", err)
		err = nil
	}

	// If the element is found, click it
	if wdf != nil {
		err := wdf.Click()
		return err
	}
	return err
}

// executeActionScroll is responsible for executing a "scroll" action
func executeActionScroll(r *rules.ActionRule, wd *selenium.WebDriver) error {
	// Get Selectors list
	value := r.Value

	// Get the attribute to scroll to
	var attribute string
	if value == "" {
		attribute = "document.body.scrollHeight"
	} else {
		attribute = value
	}

	// Use Sprintf to dynamically create the script string with the attribute value
	script := fmt.Sprintf("window.scrollTo(0, %s)", attribute)

	// Scroll the page
	_, err := (*wd).ExecuteScript(script, nil)
	return err
}

// executeActionJS is responsible for executing a "execute_javascript" action
func executeActionJS(r *rules.ActionRule, wd *selenium.WebDriver) error {
	// Execute the JavaScript
	_, err := (*wd).ExecuteScript(r.Value, nil)
	return err
}

// executeActionInput is responsible for executing an "input" action
func executeActionInput(r *rules.ActionRule, wd *selenium.WebDriver) error {
	// Find the element
	wdf, selector, err := findElementBySelectorType(wd, r.Selectors)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug3, "No element '%v' found.", err)
		err = nil
	}

	// If the element is found, input the text
	if wdf != nil {
		err = wdf.SendKeys(selector.Attribute)
	}
	return err
}

// findElementBySelectorType is responsible for finding an element in the WebDriver
// using the appropriate selector type. It returns the first element found and an error.
func findElementBySelectorType(wd *selenium.WebDriver, selectors []rules.Selector) (selenium.WebElement, rules.Selector, error) {
	var wdf selenium.WebElement = nil
	var err error
	var selector rules.Selector
	for _, selector = range selectors {
		switch selector.SelectorType {
		case "css":
			wdf, err = (*wd).FindElement(selenium.ByCSSSelector, selector.Selector)
		case "xpath":
			wdf, err = (*wd).FindElement(selenium.ByXPATH, selector.Selector)
		case "id":
			wdf, err = (*wd).FindElement(selenium.ByID, selector.Selector)
		case "name":
			wdf, err = (*wd).FindElement(selenium.ByName, selector.Selector)
		case "linktext":
			wdf, err = (*wd).FindElement(selenium.ByLinkText, selector.Selector)
		case "partiallinktext":
			wdf, err = (*wd).FindElement(selenium.ByPartialLinkText, selector.Selector)
		case "tagname":
			wdf, err = (*wd).FindElement(selenium.ByTagName, selector.Selector)
		case "class":
			wdf, err = (*wd).FindElement(selenium.ByClassName, selector.Selector)
		}
		if err == nil && wdf != nil {
			break
		}
	}

	return wdf, selector, err
}

func DefaultActionConfig(url string) cfg.SourceConfig {
	return cfg.SourceConfig{
		FormatVersion: "1.0",
		Author:        "The CROWler team",
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
				RuleGroups: []string{"CookieAcceptanceRulesExtended"},
			},
		},
	}
}

func runDefaultActionRules(wd *selenium.WebDriver, ctx *processContext) {
	// Execute the default scraping rules
	cmn.DebugMsg(cmn.DbgLvlDebug, "Executing default action rules...")

	// Get the default scraping rules
	url, err := (*wd).CurrentURL()
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error getting the current URL: %v", err)
		url = ""
	}
	rs := DefaultActionConfig(url)
	// Check if the conditions are met
	if len(rs.ExecutionPlan) == 0 {
		cmn.DebugMsg(cmn.DbgLvlDebug, "No execution plan found for the current URL")
		return
	}
	// Execute all the rules in the ruleset
	for _, r := range rs.ExecutionPlan {
		// Check the conditions
		if !checkActionPreConditions(r.Conditions, url) {
			continue
		}
		if !checkActionConditions(r.AdditionalConditions, wd) {
			continue
		}
		if len(r.Rulesets) > 0 {
			executePlannedRulesets(wd, ctx, r)
		}
		if len(r.RuleGroups) > 0 {
			executePlannedRuleGroups(wd, ctx, r)
		}
		if len(r.Rules) > 0 {
			executePlannedRules(wd, ctx, r)
		}
	}
}

// checkActionPreConditions checks if the pre conditions are met
// for example if the page URL is listed in the list of URLs
// for which this rule is valid.
func checkActionPreConditions(conditions cfg.Condition, url string) bool {
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

// checkActionConditions checks all types of conditions: Action and Config Conditions
// These are page related conditions, for instance check if an element is present
// or if the page is in the desired language etc.
func checkActionConditions(conditions map[string]interface{}, wd *selenium.WebDriver) bool {
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

// executePlannedRules executes the rules in the execution plan
func executePlannedRules(wd *selenium.WebDriver, ctx *processContext, planned cfg.ExecutionPlanItem) {
	// Execute the rules in the execution plan
	cmn.DebugMsg(cmn.DbgLvlDebug, "Executing planned rules...")
	// Get the rule
	for _, ruleName := range planned.Rules {
		if ruleName == "" {
			continue
		}
		rule, err := ctx.re.GetActionRuleByName(ruleName)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Error getting action rule: %v", err)
		} else {
			// Execute the rule
			err := executeActionRule(rule, wd)
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "Error executing action rule: %v", err)
			}
		}
	}
}

// executePlannedRuleGroups executes the rule groups in the execution plan
func executePlannedRuleGroups(wd *selenium.WebDriver, ctx *processContext, planned cfg.ExecutionPlanItem) {
	// Execute the rule groups in the execution plan
	cmn.DebugMsg(cmn.DbgLvlDebug, "Executing planned rule groups...")
	// Get the rule group
	for _, ruleGroupName := range planned.RuleGroups {
		if strings.TrimSpace(ruleGroupName) == "" {
			continue
		}
		rg, err := ctx.re.GetRuleGroupByName(ruleGroupName)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Error getting rule group: %v", err)
		} else {
			// Execute the rule group
			executeActionRules(rg.GetActionRules(), wd)
		}
	}
}

// executePlannedRulesets executes the rulesets in the execution plan
func executePlannedRulesets(wd *selenium.WebDriver, ctx *processContext, planned cfg.ExecutionPlanItem) {
	// Execute the rulesets in the execution plan
	cmn.DebugMsg(cmn.DbgLvlDebug, "Executing planned rulesets...")
	// Get the ruleset
	for _, rulesetName := range planned.Rulesets {
		if rulesetName == "" {
			continue
		}
		rs, err := ctx.re.GetRulesetByName(rulesetName)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Error getting ruleset: %v", err)
		} else {
			// Execute the ruleset
			executeActionRules(rs.GetActionRules(), wd)
		}
	}
}
