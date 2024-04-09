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
	expectedActionRule := ActionRule{RuleName: "ClickLoginButton", ActionType: "click"}
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
	expectedActionRule := ActionRule{RuleName: actionRuleName, ActionType: "click"}
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
