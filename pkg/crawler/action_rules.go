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

func processActionRules(wd *selenium.WebDriver, ctx *processContext, url string) {
	cmn.DebugMsg(cmn.DbgLvlDebug2, "Starting to search and process CROWler Action rules...")
	// Run Action Rules if any
	if ctx.source.Config != nil && strings.TrimSpace(string((*ctx.source.Config))) != "{\"config\":\"default\"}" {
		// Execute the CROWler rules
		cmn.DebugMsg(cmn.DbgLvlDebug, "Executing CROWler configured Action rules...")
		// Execute the rules

		// Convert ctx.source.Config JSONB to a string
		configStr := string((*ctx.source.Config))
		cmn.DebugMsg(cmn.DbgLvlDebug, "Configuration: %v", configStr)
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
		return err
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
		return err
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
