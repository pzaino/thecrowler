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
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	plg "github.com/pzaino/thecrowler/pkg/plugin"
)

// mockRuleset generates a basic Ruleset for testing purposes
func mockRuleset(name string) Ruleset {
	return Ruleset{
		FormatVersion: "1.0",
		Author:        "Tester",
		CreatedAt:     cmn.CustomTime{Time: time.Now()},
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
          "detection_config": {
            "noise_threshold": 1,
            "maybe_threshold": 5,
            "detected_threshold": 10
          },
          "rulesets": [
            {
              "format_version": "",
              "author": "",
              "created_at": "0001-01-01T00:00:00Z",
              "description": "",
              "ruleset_name": "",
              "rule_groups": null
            }
          ],
          "schema": null
        }`

	got := strings.ReplaceAll(string(jsonData), " ", "")
	want := strings.ReplaceAll(expectedJSON, " ", "")
	got = strings.ReplaceAll(got, "\n", "")
	want = strings.ReplaceAll(want, "\n", "")
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

	detectionRules := re.GetAllDetectionRules("")

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

	scrapingRules := re.GetAllEnabledScrapingRules("")

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

	actionRules := re.GetAllEnabledActionRules("")

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

	crawlingRules := re.GetAllEnabledCrawlingRules("")

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

	detectionRules := re.GetAllEnabledDetectionRules("")

	if len(detectionRules) != 3 {
		t.Errorf("Expected 3 detection rules, got %d", len(detectionRules))
	}
}

func TestGetAllScrapingRulesByURL(t *testing.T) {
	re := &RuleEngine{
		Rulesets: []Ruleset{
			{
				RuleGroups: []RuleGroup{
					{
						GroupName: "RuleGroup1",
						ScrapingRules: []ScrapingRule{
							{
								RuleName: "ScrapingRule1",
								PreConditions: []PreCondition{
									{
										URL: "https://example.com",
									},
								},
							},
							{
								RuleName: "ScrapingRule2",
								PreConditions: []PreCondition{
									{
										URL: "https://example.com",
									},
								},
							},
						},
						IsEnabled: true,
					},
					{
						GroupName: "RuleGroup2",
						ScrapingRules: []ScrapingRule{
							{
								RuleName: "ScrapingRule3",
								PreConditions: []PreCondition{
									{
										URL: "https://example.org",
									},
								},
							},
						},
						IsEnabled: true,
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
								PreConditions: []PreCondition{
									{
										URL: "https://example.com",
									},
								},
							},
						},
						IsEnabled: false,
					},
				},
			},
		},
	}

	scrapingRules := re.GetAllScrapingRulesByURL("https://example.com", "")

	if len(scrapingRules) != 2 {
		t.Errorf("Expected 2 scraping rules, but got %d", len(scrapingRules))
	}
}

func TestCountRulesets(t *testing.T) {
	re := &RuleEngine{
		Rulesets: []Ruleset{
			mockRuleset("TestRuleset1"),
			mockRuleset("TestRuleset2"),
		},
	}

	count := re.CountRulesets()

	if count != 2 {
		t.Errorf("Expected count to be 2, but got %d", count)
	}
}

func TestCountRuleGroups(t *testing.T) {
	re := &RuleEngine{
		Rulesets: []Ruleset{
			{
				RuleGroups: []RuleGroup{
					{
						GroupName: "RuleGroup1",
					},
					{
						GroupName: "RuleGroup2",
					},
				},
			},
			{
				RuleGroups: []RuleGroup{
					{
						GroupName: "RuleGroup3",
					},
				},
			},
		},
	}

	count := re.CountRuleGroups()

	if count != 3 {
		t.Errorf("Expected count to be 3, but got %d", count)
	}
}

func TestCountScrapingRules(t *testing.T) {
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

	count := re.CountScrapingRules()

	if count != 4 {
		t.Errorf("Expected 4 scraping rules, but got %d", count)
	}
}

func TestCountActionRules(t *testing.T) {
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

	count := re.CountActionRules()

	if count != 4 {
		t.Errorf("Expected count to be 4, but got %d", count)
	}
}

func TestCountCrawlingRules(t *testing.T) {
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

	count := re.CountCrawlingRules()

	if count != 4 {
		t.Errorf("Expected 4 crawling rules, but got %d", count)
	}
}

func TestCountDetectionRules(t *testing.T) {
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

	count := re.CountDetectionRules()

	if count != 4 {
		t.Errorf("Expected count to be 4, but got %d", count)
	}
}

func TestCountRules(t *testing.T) {
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
						ActionRules: []ActionRule{
							{
								RuleName: "ActionRule1",
							},
							{
								RuleName: "ActionRule2",
							},
						},
						CrawlingRules: []CrawlingRule{
							{
								RuleName: "CrawlingRule1",
							},
							{
								RuleName: "CrawlingRule2",
							},
						},
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
						ScrapingRules: []ScrapingRule{
							{
								RuleName: "ScrapingRule3",
							},
						},
						ActionRules: []ActionRule{
							{
								RuleName: "ActionRule3",
							},
						},
						CrawlingRules: []CrawlingRule{
							{
								RuleName: "CrawlingRule3",
							},
						},
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
						ScrapingRules: []ScrapingRule{
							{
								RuleName: "ScrapingRule4",
							},
						},
						ActionRules: []ActionRule{
							{
								RuleName: "ActionRule4",
							},
						},
						CrawlingRules: []CrawlingRule{
							{
								RuleName: "CrawlingRule4",
							},
						},
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

	count := re.CountRules()

	if count != 16 {
		t.Errorf("Expected 16 rules, but got %d", count)
	}
}

func TestCountPlugins(t *testing.T) {
	re := &RuleEngine{
		Rulesets: []Ruleset{},
		JSPlugins: plg.JSPluginRegister{
			Registry: map[string]plg.JSPlugin{
				"plugin1": {},
				"plugin2": {},
			},
		},
	}

	count := re.CountPlugins()

	if count != 2 {
		t.Errorf("Expected count to be 2, but got %d", count)
	}
}

func TestCountPluginsWithEmptyRegistry(t *testing.T) {
	re := &RuleEngine{
		Rulesets: []Ruleset{},
		JSPlugins: plg.JSPluginRegister{
			Registry: map[string]plg.JSPlugin{},
		},
	}

	count := re.CountPlugins()

	if count != 0 {
		t.Errorf("Expected count to be 0, but got %d", count)
	}
}

func TestGetRulesetByURL(t *testing.T) {
	re := &RuleEngine{
		Rulesets: []Ruleset{
			{
				Name: "http://example.com",
			},
			{
				Name: "TestRuleset2",
			},
		},
	}

	// Test case 1: Matching URL
	url1 := "http://example.com"
	ruleset1, err := re.GetRulesetByURL(url1)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if ruleset1 == nil {
		t.Error("Expected ruleset, got nil")
	} else if ruleset1.Name != "http://example.com" {
		t.Errorf("Expected ruleset name 'TestRuleset1', got '%s'", ruleset1.Name)
	}

	// Test case 2: Non-matching URL
	url2 := "http://example.org"
	ruleset2, err := re.GetRulesetByURL(url2)
	if err == nil {
		t.Error("Expected error, got nil")
	}
	if ruleset2 != nil {
		t.Errorf("Expected nil ruleset, got %+v", ruleset2)
	}
}

func TestGetRulesetByName(t *testing.T) {
	re := &RuleEngine{
		Rulesets: []Ruleset{
			{
				Name: "TestRuleset1",
			},
			{
				Name: "TestRuleset2",
			},
		},
	}

	// Test case 1: Existing ruleset
	name := "TestRuleset1"
	ruleset, err := re.GetRulesetByName(name)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if ruleset.Name != name {
		t.Errorf("Expected ruleset name: %s, got: %s", name, ruleset.Name)
	}

	// Test case 2: Non-existing ruleset
	name = "NonExistingRuleset"
	_, err = re.GetRulesetByName(name)
	if err == nil {
		t.Errorf("Expected error, got nil")
	}
	expectedError := errRulesetNotFound
	if err.Error() != expectedError {
		t.Errorf("Expected error: %s, got: %v", expectedError, err)
	}
}

func TestGetRulesGroupByURL(t *testing.T) {
	re := &RuleEngine{
		Rulesets: []Ruleset{
			{
				RuleGroups: []RuleGroup{
					{
						GroupName: "http://example.com",
						IsEnabled: true,
					},
					{
						GroupName: "RuleGroup2",
						IsEnabled: false,
					},
				},
			},
			{
				RuleGroups: []RuleGroup{
					{
						GroupName: "RuleGroup3",
						IsEnabled: true,
					},
				},
			},
		},
	}

	// Test case 1: Valid URL with matching rule group
	url1 := "http://example.com"
	expectedGroup1 := RuleGroup{
		GroupName: "http://example.com",
		IsEnabled: true,
	}
	group1, err := re.GetRulesGroupByURL(url1)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if !reflect.DeepEqual(*group1, expectedGroup1) {
		t.Errorf("Expected group: %v, but got: %v", expectedGroup1, *group1)
	}

	// Test case 2: Valid URL with no matching rule group
	url2 := "http://example.org"
	_, err = re.GetRulesGroupByURL(url2)
	if err == nil {
		t.Error("Expected error, but got nil")
	}
	expectedError := fmt.Errorf("%s", errRuleGroupNotFound)
	if err.Error() != expectedError.Error() {
		t.Errorf("Expected error: %v, but got: %v", expectedError, err)
	}

	// Test case 3: Invalid URL
	url3 := "invalid-url"
	_, err = re.GetRulesGroupByURL(url3)
	if err == nil {
		t.Error("Expected error, but got nil")
	}

}

func TestGetRuleGroupByName(t *testing.T) {
	re := &RuleEngine{
		Rulesets: []Ruleset{
			{
				RuleGroups: []RuleGroup{
					{
						GroupName: "RuleGroup1",
						IsEnabled: true,
					},
					{
						GroupName: "RuleGroup2",
						IsEnabled: true,
					},
				},
			},
			{
				RuleGroups: []RuleGroup{
					{
						GroupName: "RuleGroup3",
						IsEnabled: true,
					},
				},
			},
		},
	}

	// Test case 1: Existing rule group
	groupName := "RuleGroup2"
	expectedGroup := RuleGroup{
		GroupName: "RuleGroup2",
		IsEnabled: true,
	}
	group, err := re.GetRuleGroupByName(groupName)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if !reflect.DeepEqual(*group, expectedGroup) {
		t.Errorf("Expected group: %v, but got: %v", expectedGroup, *group)
	}

	// Test case 2: Non-existing rule group
	groupName = "NonExistingGroup"
	_, err = re.GetRuleGroupByName(groupName)
	if err == nil {
		t.Errorf("Expected error, but got nil")
	}
}

func TestGetActionRuleByName(t *testing.T) {
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

	// Test case 1: Existing action rule
	name := "ActionRule2"
	expectedRule := ActionRule{
		RuleName: "ActionRule2",
	}
	rule, err := re.GetActionRuleByName(name)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if !reflect.DeepEqual(*rule, expectedRule) {
		t.Errorf("Expected rule: %v, but got: %v", expectedRule, *rule)
	}

	// Test case 2: Non-existing action rule
	name = "NonExistingRule"
	_, err = re.GetActionRuleByName(name)
	if err == nil {
		t.Errorf("Expected error, but got nil")
	}
}

func TestGetActionRuleByURL(t *testing.T) {
	re := &RuleEngine{
		Rulesets: []Ruleset{
			{
				RuleGroups: []RuleGroup{
					{
						ActionRules: []ActionRule{
							{
								RuleName: "ActionRule1",
								URL:      "https://example.com",
							},
							{
								RuleName: "ActionRule2",
								URL:      "https://example.org",
							},
						},
						IsEnabled: true,
					},
					{
						ActionRules: []ActionRule{
							{
								RuleName: "ActionRule3",
								URL:      "https://example.net",
							},
						},
						IsEnabled: true,
					},
				},
			},
			{
				RuleGroups: []RuleGroup{
					{
						ActionRules: []ActionRule{
							{
								RuleName: "ActionRule4",
								URL:      "https://example.com/foo",
							},
						},
					},
				},
			},
		},
	}

	// Test case 1: Valid URL with matching action rule
	url1 := "https://example.com"
	actionRule1, err := re.GetActionRuleByURL(url1)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if actionRule1 != nil {
		if actionRule1.RuleName != "ActionRule1" {
			t.Errorf("Expected action rule name 'ActionRule1', got '%s'", actionRule1.RuleName)
		}
	} else {
		t.Errorf("Expected action rule, got nil")
	}

	// Test case 2: Valid URL with no matching action rule
	url2 := "https://example.org/foo"
	actionRule2, err := re.GetActionRuleByURL(url2)
	if err != nil {
		t.Errorf("Unexpected error, expected nil, got %v", err)
	}
	if actionRule2 == nil {
		t.Errorf("Expected action rule, got nil")
	}

	// Test case 3: Invalid URL
	url3 := "invalid-url"
	actionRule3, err := re.GetActionRuleByURL(url3)
	if err == nil {
		t.Errorf("Expected error, got nil")
	}
	if actionRule3 != nil {
		t.Errorf("Expected nil action rule, got %+v", actionRule3)
	}
}

func TestGetScrapingRuleByName(t *testing.T) {
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

	// Test case 1: Existing scraping rule
	scrapingRuleName := "ScrapingRule2"
	expectedScrapingRule := ScrapingRule{
		RuleName: "ScrapingRule2",
	}
	scrapingRule, err := re.GetScrapingRuleByName(scrapingRuleName)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if !reflect.DeepEqual(*scrapingRule, expectedScrapingRule) {
		t.Errorf("Expected scraping rule: %v, but got: %v", expectedScrapingRule, *scrapingRule)
	}

	// Test case 2: Non-existing scraping rule
	nonExistingScrapingRuleName := "NonExistingScrapingRule"
	_, err = re.GetScrapingRuleByName(nonExistingScrapingRuleName)
	if err == nil {
		t.Errorf("Expected error, but got nil")
	}
}

func TestGetScrapingRuleByPath(t *testing.T) {
	re := &RuleEngine{
		Rulesets: []Ruleset{
			{
				RuleGroups: []RuleGroup{
					{
						ScrapingRules: []ScrapingRule{
							{
								RuleName: "ScrapingRule1",
								PreConditions: []PreCondition{
									{
										Path: "/path1",
									},
								},
							},
							{
								RuleName: "ScrapingRule2",
								PreConditions: []PreCondition{
									{
										Path: "/path2",
									},
								},
							},
						},
					},
					{
						ScrapingRules: []ScrapingRule{
							{
								RuleName: "ScrapingRule3",
								PreConditions: []PreCondition{
									{
										Path: "/path3",
									},
								},
							},
						},
						IsEnabled: false,
					},
				},
			},
			{
				RuleGroups: []RuleGroup{
					{
						ScrapingRules: []ScrapingRule{
							{
								RuleName: "ScrapingRule4",
								PreConditions: []PreCondition{
									{
										Path: "/path4",
									},
								},
							},
						},
					},
				},
			},
		},
	}

	// Test case 1: Valid path
	path1 := "/path1"
	expectedRule1 := &ScrapingRule{
		RuleName: "ScrapingRule1",
		PreConditions: []PreCondition{{
			Path: "/path1",
		}},
	}
	rule1, err := re.GetScrapingRuleByPath(path1)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if !reflect.DeepEqual(rule1, expectedRule1) {
		t.Errorf("Expected rule: %v, but got: %v", expectedRule1, rule1)
	}

	// Test case 2: Invalid path
	invalidPath := "/invalid"
	_, err = re.GetScrapingRuleByPath(invalidPath)
	if err == nil {
		t.Error("Expected error, but got nil")
	}
	expectedErrorMsg := "scraping rule not found"
	if err.Error() != expectedErrorMsg {
		t.Errorf("Expected error message: %s, but got: %s", expectedErrorMsg, err.Error())
	}
}

func TestGetCrawlingRuleByName(t *testing.T) {
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

	// Test case 1: Crawling rule exists
	crawlingRule, err := re.GetCrawlingRuleByName("CrawlingRule2")
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if crawlingRule.RuleName != "CrawlingRule2" {
		t.Errorf("Expected CrawlingRule2, got %s", crawlingRule.RuleName)
	}

	// Test case 2: Crawling rule does not exist
	_, err = re.GetCrawlingRuleByName("NonExistentRule")
	if err == nil {
		t.Error("Expected error, got nil")
	}
}

func TestGetDetectionRuleByName(t *testing.T) {
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

	// Test case 1: Existing detection rule
	detectionRule, err := re.GetDetectionRuleByName("DetectionRule2")
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if detectionRule.RuleName != "DetectionRule2" {
		t.Errorf("Expected detection rule name: DetectionRule2, got: %s", detectionRule.RuleName)
	}

	// Test case 2: Non-existing detection rule
	_, err = re.GetDetectionRuleByName("NonExistingRule")
	if err == nil {
		t.Error("Expected error, but got nil")
	}
}

func TestFindRulesForSite(t *testing.T) {
	re := &RuleEngine{
		Rulesets: []Ruleset{
			{
				Name: "https://example.com",
			},
			{
				Name: "https://google.co.uk",
			},
		},
	}

	// Test case: matching domain
	url := "https://example.com"
	expectedRuleset := &re.Rulesets[0]
	ruleset, err := re.FindRulesForSite(url)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if ruleset != nil {
		if (*ruleset).Name != (*expectedRuleset).Name {
			t.Errorf("Expected ruleset: %v, but got: %v", expectedRuleset, ruleset)
		}
	} else {
		t.Errorf("Expected ruleset to be %v, but got nil", expectedRuleset)
	}

	// Test case: non-matching domain
	url = "https://example.org"
	expectedRuleset = nil

	ruleset, err = re.FindRulesForSite(url)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if ruleset != expectedRuleset {
		t.Errorf("Expected ruleset to be nil, but got %v", ruleset)
	}
}

func TestGetAllRulesetByURL(t *testing.T) {
	re := &RuleEngine{
		Rulesets: []Ruleset{
			{
				Name: "http://example.com",
			},
			{
				Name: "http://example.org",
			},
			{
				Name: "http://example.com/test",
			},
		},
	}

	// Test case 1: Matching URL
	url1 := "http://example.com"
	rulesets1, err := re.GetAllRulesetByURL(url1)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if len(rulesets1) != 1 {
		t.Errorf("Expected 1 rulesets, got %d", len(rulesets1))
	}

	// Test case 2: Non-matching URL
	url2 := "http://nonexistent.com"
	rulesets2, err := re.GetAllRulesetByURL(url2)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if len(rulesets2) != 0 {
		t.Errorf("Expected 0 rulesets, got %d", len(rulesets2))
	}

	// Test case 3: Invalid URL
	url3 := "invalid-url"
	results, err := re.GetAllRulesetByURL(url3)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("Expected 0 rules, got %d", len(results))
	}
}
