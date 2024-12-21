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

// Package ruleset implements the ruleset library for the Crowler and
// the scrapper.
package ruleset

import (
	"testing"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
)

// Helper function to create a RuleGroup for testing
func createTestRuleGroup(validFrom, validTo time.Time, isEnabled bool) RuleGroup {
	return RuleGroup{
		GroupName: "TestGroup",
		ValidFrom: CustomTime{Time: validFrom},
		ValidTo:   CustomTime{Time: validTo},
		IsEnabled: isEnabled,
	}
}

func TestRuleGroupIsValid(t *testing.T) {
	now := time.Now()
	oneHourBefore := now.Add(-1 * time.Hour)
	oneHourAfter := now.Add(1 * time.Hour)

	tests := []struct {
		name      string
		ruleGroup RuleGroup
		want      bool
	}{
		{
			name:      "Valid group within date range",
			ruleGroup: createTestRuleGroup(oneHourBefore, oneHourAfter, true),
			want:      true,
		},
		{
			name:      "Invalid group before date range",
			ruleGroup: createTestRuleGroup(oneHourAfter, oneHourAfter.Add(1*time.Hour), true),
			want:      false,
		},
		{
			name:      "Invalid group after date range",
			ruleGroup: createTestRuleGroup(oneHourBefore.Add(-2*time.Hour), oneHourBefore, true),
			want:      false,
		},
		{
			name:      "Invalid group disabled",
			ruleGroup: createTestRuleGroup(oneHourBefore, oneHourAfter, false),
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.ruleGroup.IsValid(); got != tt.want {
				t.Errorf("IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Example test for GetActionRules
func TestRuleGroupGetActionRules(t *testing.T) {
	expectedActionRule := ActionRule{RuleName: "ClickLoginButton", ActionType: cmn.ClickStr}
	ruleGroup := RuleGroup{
		ActionRules: []ActionRule{expectedActionRule},
	}

	gotActionRules := ruleGroup.GetActionRules()
	if len(gotActionRules) != 1 || gotActionRules[0].RuleName != expectedActionRule.RuleName {
		t.Errorf("GetActionRules() did not return the expected action rules")
	}
}

// Example test for GetActionRuleByName
func TestRuleGroupGetActionRuleByName(t *testing.T) {
	actionRuleName := "ClickLoginButton"
	expectedActionRule := ActionRule{RuleName: actionRuleName, ActionType: cmn.ClickStr}
	ruleGroup := RuleGroup{
		ActionRules: []ActionRule{expectedActionRule},
	}

	gotActionRule, err := ruleGroup.GetActionRuleByName(actionRuleName)
	if err != nil || gotActionRule.RuleName != actionRuleName {
		t.Errorf("GetActionRuleByName() failed to retrieve the action rule by name, or retrieved the wrong rule")
	}
}

// Example test for GetScrapingRuleByName
func TestRuleGroupGetScrapingRuleByName(t *testing.T) {
	scrapingRuleName := "ClickLoginButton"
	expectedScrapingRule := ScrapingRule{RuleName: scrapingRuleName}
	ruleGroup := RuleGroup{
		ScrapingRules: []ScrapingRule{expectedScrapingRule},
	}

	gotScrapingRule, err := ruleGroup.GetScrapingRuleByName(scrapingRuleName)
	if err != nil || gotScrapingRule.RuleName != scrapingRuleName {
		t.Errorf("GetScrapingRuleByName() failed to retrieve the action rule by name, or retrieved the wrong rule")
	}
}
func TestRuleGroupIsEmpty(t *testing.T) {
	emptyRuleGroup := RuleGroup{}
	nonEmptyRuleGroup := RuleGroup{
		ActionRules:    []ActionRule{{}},
		ScrapingRules:  []ScrapingRule{{}},
		CrawlingRules:  []CrawlingRule{{}},
		DetectionRules: []DetectionRule{{}},
		GroupName:      "TestGroup",
	}

	empty := emptyRuleGroup.IsEmpty()
	if !empty {
		t.Errorf("IsEmpty() returned false for an empty RuleGroup")
	}

	notEmpty := nonEmptyRuleGroup.IsEmpty()
	if notEmpty {
		t.Errorf("IsEmpty() returned true for a non-empty RuleGroup")
	}
}

func TestRuleGroupGetScrapingRules(t *testing.T) {
	scrapingRule1 := ScrapingRule{RuleName: "Rule1"}
	scrapingRule2 := ScrapingRule{RuleName: "Rule2"}
	ruleGroup := RuleGroup{
		ScrapingRules: []ScrapingRule{scrapingRule1, scrapingRule2},
	}

	gotScrapingRules := ruleGroup.GetScrapingRules()
	if len(gotScrapingRules) != 2 || gotScrapingRules[0].RuleName != scrapingRule1.RuleName || gotScrapingRules[1].RuleName != scrapingRule2.RuleName {
		t.Errorf("GetScrapingRules() did not return the expected scraping rules")
	}
}

func TestRuleGroupGetActionRuleByURL(t *testing.T) {
	urlStr := "https://example.com/login"
	expectedActionRule := ActionRule{URL: urlStr}
	ruleGroup := RuleGroup{
		ActionRules: []ActionRule{expectedActionRule},
	}

	gotActionRule, err := ruleGroup.GetActionRuleByURL(urlStr)
	if err != nil || gotActionRule.URL != urlStr {
		t.Errorf("GetActionRuleByURL() failed to retrieve the action rule by URL, or retrieved the wrong rule")
	}
}

func TestRuleGroupGetScrapingRuleByPath(t *testing.T) {
	path := "/login"
	expectedScrapingRule := ScrapingRule{RuleName: "LoginRule"}
	ruleGroup := RuleGroup{
		ScrapingRules: []ScrapingRule{
			{
				RuleName: "SomeRule",
				PreConditions: []PreCondition{
					{Path: "/home"},
					{Path: "/about"},
				},
			},
			{
				RuleName: "AnotherRule",
				PreConditions: []PreCondition{
					{Path: "/products"},
					{Path: "/services"},
				},
			},
			{
				RuleName: "LoginRule",
				PreConditions: []PreCondition{
					{Path: path},
				},
			},
		},
	}

	gotScrapingRule, err := ruleGroup.GetScrapingRuleByPath(path)
	if err != nil || gotScrapingRule.RuleName != expectedScrapingRule.RuleName {
		t.Errorf("GetScrapingRuleByPath() failed to retrieve the scraping rule by path, or retrieved the wrong rule")
	}
}

func TestRuleGroupGetScrapingRuleByURL(t *testing.T) {
	urlStr := "https://example.com/login"
	expectedScrapingRule := ScrapingRule{RuleName: "LoginRule"}
	ruleGroup := RuleGroup{
		ScrapingRules: []ScrapingRule{
			{
				RuleName: "SomeRule",
				PreConditions: []PreCondition{
					{URL: "https://example.com/home"},
					{URL: "https://example.com/about"},
				},
			},
			{
				RuleName: "AnotherRule",
				PreConditions: []PreCondition{
					{URL: "https://example.com/products"},
					{URL: "https://example.com/services"},
				},
			},
			{
				RuleName: "LoginRule",
				PreConditions: []PreCondition{
					{URL: urlStr},
				},
			},
		},
	}

	gotScrapingRule, err := ruleGroup.GetScrapingRuleByURL(urlStr)
	if err != nil || gotScrapingRule.RuleName != expectedScrapingRule.RuleName {
		t.Errorf("GetScrapingRuleByURL() failed to retrieve the scraping rule by URL, or retrieved the wrong rule")
	}
}
