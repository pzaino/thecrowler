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
	"reflect"
	"testing"
)

// TestScrapingRule_GetRuleName tests the GetRuleName method of ScrapingRule
func TestScrapingRuleGetRuleName(t *testing.T) {
	ruleName := "TestScrapingRule"
	r := ScrapingRule{RuleName: "  " + ruleName + "  "}
	if got := r.GetRuleName(); got != ruleName {
		t.Errorf("GetRuleName() = %v, want %v", got, ruleName)
	}
}

// TestScrapingRule_GetPaths tests the GetPaths method of ScrapingRule
func TestScrapingRuleGetPaths(t *testing.T) {
	expectedPaths := []string{"path1", "path2"}
	r := ScrapingRule{
		PreConditions: []PreCondition{
			{Path: " path1 "},
			{Path: " path2 "},
		},
	}
	if got := r.GetPaths(); !reflect.DeepEqual(got, expectedPaths) {
		t.Errorf("GetPaths() = %v, want %v", got, expectedPaths)
	}
}

// TestScrapingRule_GetURLs tests the GetURLs method of ScrapingRule
func TestScrapingRuleGetURLs(t *testing.T) {
	expectedURLs := []string{"https://example.com"}
	r := ScrapingRule{
		PreConditions: []PreCondition{
			{URL: " https://example.com "},
		},
	}
	if got := r.GetURLs(); !reflect.DeepEqual(got, expectedURLs) {
		t.Errorf("GetURLs() = %v, want %v", got, expectedURLs)
	}
}

// TestScrapingRule_GetElements tests the GetElements method of ScrapingRule
func TestScrapingRuleGetElements(t *testing.T) {
	// TODO: Assuming Element struct and initialization are correct
}

// TestScrapingRule_GetJsFiles tests the GetJsFiles method of ScrapingRule
func TestScrapingRuleGetJsFiles(t *testing.T) {
	// TODO: Assuming the JsFiles field is set correctly
}

// TestScrapingRule_GetTechnologyPatterns tests GetTechnologyPatterns of ScrapingRule
func TestScrapingRuleGetTechnologyPatterns(t *testing.T) {
	// TODO: Assuming TechnologyPatterns field is set correctly
}

// TestScrapingRule_GetJSONFieldMappings tests GetJSONFieldMappings of ScrapingRule
func TestScrapingRuleGetJSONFieldMappings(t *testing.T) {
	// TODO: Assuming JSONFieldMappings field is set correctly
}

// TestScrapingRule_GetWaitConditions tests GetWaitConditions of ScrapingRule
func TestScrapingRuleGetWaitConditions(t *testing.T) {
	// TODO: Assuming WaitCondition struct and initialization are correct
}

// TestScrapingRule_GetPostProcessing tests GetPostProcessing of ScrapingRule
func TestScrapingRuleGetPostProcessing(t *testing.T) {
	// TODO: Assuming PostProcessingStep struct and initialization are correct
}
