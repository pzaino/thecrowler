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
	expectedElements := []Element{
		{Key: "div1", Selectors: []Selector{
			{SelectorType: "css", Selector: "div", ExtractAllOccurrences: true},
		}},
		{Key: "a1", Selectors: []Selector{
			{SelectorType: "css", Selector: "a", ExtractAllOccurrences: true},
		}},
	}
	r := ScrapingRule{
		Elements: []Element{
			{Key: "div1", Selectors: []Selector{
				{SelectorType: "css", Selector: "div", ExtractAllOccurrences: true},
			}},
			{Key: "a1", Selectors: []Selector{
				{SelectorType: "css", Selector: "a", ExtractAllOccurrences: true},
			}},
		},
	}
	if got := r.GetElements(); !reflect.DeepEqual(got, expectedElements) {
		t.Errorf("GetElements() = %v, want %v", got, expectedElements)
	}
}

// TestScrapingRule_GetJsFiles tests the GetJsFiles method of ScrapingRule
func TestScrapingRuleGetJsFiles(t *testing.T) {
	r := ScrapingRule{
		JsFiles: true,
	}
	if got := r.GetJsFiles(); got != true {
		t.Errorf("GetJsFiles() = %v, want %v", got, true)
	}
}
