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
	"strings"
	"testing"
	"time"
)

// mockRuleset generates a basic Ruleset for testing purposes
func mockRuleset(name string) Ruleset {
	return Ruleset{
		FormatVersion: "1.0",
		Author:        "Tester",
		CreatedAt:     CustomTime{Time: time.Now()},
		Description:   "A test ruleset",
		Name:          name,
		RuleGroups:    []RuleGroup{},
	}
}

func TestNewRuleEngine2(t *testing.T) {
	schemaPath := "" // Assuming the schema path or handling is mocked or not needed for this test
	rulesets := []Ruleset{mockRuleset("TestRuleset1"), mockRuleset("TestRuleset2")}

	re := NewRuleEngine(schemaPath, rulesets)
	if re == nil {
		t.Errorf("NewRuleEngine() returned nil")
	} else if re.Rulesets == nil {
		t.Errorf("Rulesets field is nil")
	}

	if len(re.Rulesets) != 2 {
		t.Errorf("Expected 2 rulesets, got %d", len(re.Rulesets))
	}
}

func TestRuleEngineAddRuleset(t *testing.T) {
	re := &RuleEngine{Rulesets: []Ruleset{}}
	rulesetToAdd := mockRuleset("TestRuleset")
	re.AddRuleset(rulesetToAdd)

	if len(re.Rulesets) != 1 {
		t.Errorf("Expected 1 ruleset after adding, got %d", len(re.Rulesets))
	}

	if re.Rulesets[0].Name != "TestRuleset" {
		t.Errorf("Expected ruleset name to be 'TestRuleset', got '%s'", re.Rulesets[0].Name)
	}
}

func TestRuleEngineRemoveRuleset(t *testing.T) {
	initialRuleset := mockRuleset("InitialRuleset")
	re := &RuleEngine{Rulesets: []Ruleset{initialRuleset}}
	re.RemoveRuleset(initialRuleset)

	if len(re.Rulesets) != 0 {
		t.Errorf("Expected 0 rulesets after removal, got %d", len(re.Rulesets))
	}
}

func TestRuleEngineUpdateRuleset(t *testing.T) {
	initialRuleset := mockRuleset("InitialRuleset")
	updatedRuleset := initialRuleset
	updatedDescription := "Updated Description"
	updatedRuleset.Description = updatedDescription

	re := &RuleEngine{Rulesets: []Ruleset{initialRuleset}}
	re.UpdateRuleset(updatedRuleset)

	if re.Rulesets[0].Description != updatedDescription {
		t.Errorf("Expected description to be updated to '%s', got '%s'", updatedDescription, re.Rulesets[0].Description)
	}
}

func TestRuleEngineMarshalJSON(t *testing.T) {
	re := &RuleEngine{
		Schema: nil,
		Rulesets: []Ruleset{
			{},
		},
		DetectionConfig: DetectionConfig{
			NoiseThreshold:    1.0,
			MaybeThreshold:    5.0,
			DetectedThreshold: 10.0,
		},
	}

	jsonData, err := re.MarshalJSON()
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	expectedJSON := `{
          "rulesets": [
            {
              "FormatVersion": "",
              "Author": "",
              "CreatedAt": "0001-01-01T00:00:00Z",
              "Description": "",
              "Name": "",
              "RuleGroups": null
            }
          ],
          "schema": null
        }`

	got := strings.ReplaceAll(string(jsonData), " ", "")
	want := strings.ReplaceAll(expectedJSON, " ", "")
	if got != want {
		t.Errorf("Expected JSON: %s, got: %s", expectedJSON, string(jsonData))
	}
}

func TestGetAllRulesets(t *testing.T) {
	re := &RuleEngine{
		Rulesets: []Ruleset{
			mockRuleset("TestRuleset1"),
			mockRuleset("TestRuleset2"),
		},
	}

	rulesets := re.GetAllRulesets()

	if len(rulesets) != 2 {
		t.Errorf("Expected 2 rulesets, got %d", len(rulesets))
	}
}

func TestGetAllRuleGroups(t *testing.T) {
	re := &RuleEngine{
		Rulesets: []Ruleset{
			mockRuleset("TestRuleset1"),
			mockRuleset("TestRuleset2"),
		},
	}

	ruleGroups := re.GetAllRuleGroups()

	if len(ruleGroups) != 0 {
		t.Errorf("Expected 0 rule groups, got %d", len(ruleGroups))
	}
}

func TestGetAllEnabledRuleGroups(t *testing.T) {
	re := &RuleEngine{
		Rulesets: []Ruleset{
			mockRuleset("TestRuleset1"),
			mockRuleset("TestRuleset2"),
		},
	}

	ruleGroups := re.GetAllEnabledRuleGroups()

	if len(ruleGroups) != 0 {
		t.Errorf("Expected 0 rule groups, got %d", len(ruleGroups))
	}
}

func TestGetAllScrapingRules(t *testing.T) {
	re := &RuleEngine{
		Rulesets: []Ruleset{
			{
				RuleGroups: []RuleGroup{
					{
						ScrapingRules: []ScrapingRule{
							{
								RuleName: "ScrapingRule1",
							},
							{
								RuleName: "ScrapingRule2",
							},
						},
					},
					{
						ScrapingRules: []ScrapingRule{
							{
								RuleName: "ScrapingRule3",
							},
						},
					},
				},
			},
			{
				RuleGroups: []RuleGroup{
					{
						ScrapingRules: []ScrapingRule{
							{
								RuleName: "ScrapingRule4",
							},
						},
					},
				},
			},
		},
	}

	scrapingRules := re.GetAllScrapingRules()

	if len(scrapingRules) != 4 {
		t.Errorf("Expected 4 scraping rules, got %d", len(scrapingRules))
	}
}

func TestGetAllActionRules(t *testing.T) {
	re := &RuleEngine{
		Rulesets: []Ruleset{
			{
				RuleGroups: []RuleGroup{
					{
						ActionRules: []ActionRule{
							{
								RuleName: "ActionRule1",
							},
							{
								RuleName: "ActionRule2",
							},
						},
					},
					{
						ActionRules: []ActionRule{
							{
								RuleName: "ActionRule3",
							},
						},
					},
				},
			},
			{
				RuleGroups: []RuleGroup{
					{
						ActionRules: []ActionRule{
							{
								RuleName: "ActionRule4",
							},
						},
					},
				},
			},
		},
	}

	actionRules := re.GetAllActionRules()

	if len(actionRules) != 4 {
		t.Errorf("Expected 4 action rules, got %d", len(actionRules))
	}
}

func TestGetAllCrawlingRules(t *testing.T) {
	re := &RuleEngine{
		Rulesets: []Ruleset{
			{
				RuleGroups: []RuleGroup{
					{
						CrawlingRules: []CrawlingRule{
							{
								RuleName: "CrawlingRule1",
							},
							{
								RuleName: "CrawlingRule2",
							},
						},
					},
					{
						CrawlingRules: []CrawlingRule{
							{
								RuleName: "CrawlingRule3",
							},
						},
					},
				},
			},
			{
				RuleGroups: []RuleGroup{
					{
						CrawlingRules: []CrawlingRule{
							{
								RuleName: "CrawlingRule4",
							},
						},
					},
				},
			},
		},
	}

	crawlingRules := re.GetAllCrawlingRules()

	if len(crawlingRules) != 4 {
		t.Errorf("Expected 4 crawling rules, got %d", len(crawlingRules))
	}
}

func TestGetAllDetectionRules(t *testing.T) {
	re := &RuleEngine{
		Rulesets: []Ruleset{
			{
				RuleGroups: []RuleGroup{
					{
						DetectionRules: []DetectionRule{
							{
								RuleName: "DetectionRule1",
							},
							{
								RuleName: "DetectionRule2",
							},
						},
					},
					{
						DetectionRules: []DetectionRule{
							{
								RuleName: "DetectionRule3",
							},
						},
					},
				},
			},
			{
				RuleGroups: []RuleGroup{
					{
						DetectionRules: []DetectionRule{
							{
								RuleName: "DetectionRule4",
							},
						},
					},
				},
			},
		},
	}

	detectionRules := re.GetAllDetectionRules()

	if len(detectionRules) != 4 {
		t.Errorf("Expected 4 detection rules, got %d", len(detectionRules))
	}
}

func TestGetAllEnabledScrapingRules(t *testing.T) {
	re := &RuleEngine{
		Rulesets: []Ruleset{
			{
				RuleGroups: []RuleGroup{
					{
						GroupName: "RuleGroup1",
						ScrapingRules: []ScrapingRule{
							{
								RuleName: "ScrapingRule1",
							},
							{
								RuleName: "ScrapingRule2",
							},
						},
						IsEnabled: true,
					},
					{
						GroupName: "RuleGroup2",
						ScrapingRules: []ScrapingRule{
							{
								RuleName: "ScrapingRule3",
							},
						},
						IsEnabled: false,
					},
				},
			},
			{
				RuleGroups: []RuleGroup{
					{
						GroupName: "RuleGroup3",
						ScrapingRules: []ScrapingRule{
							{
								RuleName: "ScrapingRule4",
							},
						},
						IsEnabled: true,
					},
				},
			},
		},
	}

	scrapingRules := re.GetAllEnabledScrapingRules()

	if len(scrapingRules) != 3 {
		t.Errorf("Expected 2 enabled scraping rules, got %d", len(scrapingRules))
	}
}

func TestGetAllEnabledActionRules(t *testing.T) {
	re := &RuleEngine{
		Rulesets: []Ruleset{
			{
				RuleGroups: []RuleGroup{
					{
						GroupName: "RuleGroup1",
						ActionRules: []ActionRule{
							{
								RuleName: "ActionRule1",
							},
							{
								RuleName: "ActionRule2",
							},
						},
						IsEnabled: true,
					},
					{
						GroupName: "RuleGroup2",
						ActionRules: []ActionRule{
							{
								RuleName: "ActionRule3",
							},
						},
						IsEnabled: false,
					},
				},
			},
			{
				RuleGroups: []RuleGroup{
					{
						GroupName: "RuleGroup3",
						ActionRules: []ActionRule{
							{
								RuleName: "ActionRule4",
							},
						},
						IsEnabled: true,
					},
				},
			},
		},
	}

	actionRules := re.GetAllEnabledActionRules()

	if len(actionRules) != 3 {
		t.Errorf("Expected 2 enabled action rules, got %d", len(actionRules))
	}
}

func TestGetAllEnabledCrawlingRules(t *testing.T) {
	re := &RuleEngine{
		Rulesets: []Ruleset{
			{
				RuleGroups: []RuleGroup{
					{
						GroupName: "RuleGroup1",
						CrawlingRules: []CrawlingRule{
							{
								RuleName: "CrawlingRule1",
							},
							{
								RuleName: "CrawlingRule2",
							},
						},
						IsEnabled: true,
					},
					{
						GroupName: "RuleGroup2",
						CrawlingRules: []CrawlingRule{
							{
								RuleName: "CrawlingRule3",
							},
						},
						IsEnabled: false,
					},
				},
			},
			{
				RuleGroups: []RuleGroup{
					{
						GroupName: "RuleGroup3",
						CrawlingRules: []CrawlingRule{
							{
								RuleName: "CrawlingRule4",
							},
						},
						IsEnabled: true,
					},
				},
			},
		},
	}

	crawlingRules := re.GetAllEnabledCrawlingRules()

	if len(crawlingRules) != 3 {
		for _, cr := range crawlingRules {
			t.Logf("Crawling rule: %s", cr.RuleName)
		}
		t.Errorf("Expected 2 enabled crawling rules, got %d", len(crawlingRules))
	}
}

func TestGetAllEnabledDetectionRules(t *testing.T) {
	re := &RuleEngine{
		Rulesets: []Ruleset{
			{
				RuleGroups: []RuleGroup{
					{
						GroupName: "RuleGroup1",
						DetectionRules: []DetectionRule{
							{
								RuleName: "DetectionRule1",
							},
							{
								RuleName: "DetectionRule2",
							},
						},
						IsEnabled: true,
					},
					{
						GroupName: "RuleGroup2",
						DetectionRules: []DetectionRule{
							{
								RuleName: "DetectionRule3",
							},
						},
						IsEnabled: false,
					},
				},
			},
			{
				RuleGroups: []RuleGroup{
					{
						GroupName: "RuleGroup3",
						DetectionRules: []DetectionRule{
							{
								RuleName: "DetectionRule4",
							},
						},
						IsEnabled: true,
					},
				},
			},
		},
	}

	detectionRules := re.GetAllEnabledDetectionRules()

	if len(detectionRules) != 3 {
		t.Errorf("Expected 3 detection rules, got %d", len(detectionRules))
	}
}
