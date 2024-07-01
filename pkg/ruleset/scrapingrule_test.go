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
	"strings"
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

// TestScrapingRule_GetJSONFieldMappings tests the GetJSONFieldMappings method of ScrapingRule
func TestScrapingRuleGetJSONFieldMappings(t *testing.T) {
	expectedMappings := map[string]string{
		"field1": "mapping1",
		"field2": "mapping2",
	}
	r := ScrapingRule{
		JSONFieldMappings: map[string]string{
			"field1": "mapping1",
			"field2": "mapping2",
		},
	}
	if got := r.GetJSONFieldMappings(); !reflect.DeepEqual(got, expectedMappings) {
		t.Errorf("GetJSONFieldMappings() = %v, want %v", got, expectedMappings)
	}
}

// TestScrapingRule_GetWaitConditions tests the GetWaitConditions method of ScrapingRule
func TestScrapingRuleGetWaitConditions(t *testing.T) {
	expectedConditions := []WaitCondition{
		{ConditionType: "Condition1", Selector: Selector{SelectorType: "css", Selector: "div"}},
		{ConditionType: "Condition2", Selector: Selector{SelectorType: "xpath", Selector: "//a"}},
	}
	r := ScrapingRule{
		WaitConditions: []WaitCondition{
			{ConditionType: "Condition1", Selector: Selector{SelectorType: "css", Selector: "div"}},
			{ConditionType: "Condition2", Selector: Selector{SelectorType: "xpath", Selector: "//a"}},
		},
	}
	if got := r.GetWaitConditions(); !reflect.DeepEqual(got, expectedConditions) {
		t.Errorf("GetWaitConditions() = %v, want %v", got, expectedConditions)
	}
}

// TestScrapingRule_GetPostProcessing tests the GetPostProcessing method of ScrapingRule
func TestScrapingRuleGetPostProcessing(t *testing.T) {
	expectedSteps := []PostProcessingStep{
		{Type: "Step1", Details: map[string]interface{}{"key1": "value1"}},
		{Type: "Step2", Details: map[string]interface{}{"key2": "value2"}},
	}
	r := ScrapingRule{
		PostProcessing: []PostProcessingStep{
			{Type: "Step1", Details: map[string]interface{}{"key1": "value1"}},
			{Type: "Step2", Details: map[string]interface{}{"key2": "value2"}},
		},
	}
	if got := r.GetPostProcessing(); !reflect.DeepEqual(got, expectedSteps) {
		t.Errorf("GetPostProcessing() = %v, want %v", got, expectedSteps)
	}
}

// TestWaitCondition_GetConditionType tests the GetConditionType method of WaitCondition
func TestWaitConditionGetConditionType(t *testing.T) {
	conditionType := "TestConditionType"
	w := WaitCondition{ConditionType: "  " + conditionType + "  "}
	if got := w.GetConditionType(); got != strings.ToLower(strings.TrimSpace(conditionType)) {
		t.Errorf("GetConditionType() = %v, want %v", got, conditionType)
	}
}

// TestWaitCondition_GetSelector tests the GetSelector method of WaitCondition
func TestWaitConditionGetSelector(t *testing.T) {
	expectedSelector := Selector{
		SelectorType:          "css",
		Selector:              "div",
		ExtractAllOccurrences: true,
	}
	w := WaitCondition{
		Selector: Selector{
			SelectorType:          "css",
			Selector:              "div",
			ExtractAllOccurrences: true,
		},
	}
	if got := w.GetSelector(); !reflect.DeepEqual(got, expectedSelector) {
		t.Errorf("GetSelector() = %v, want %v", got, expectedSelector)
	}
}

// TestPostProcessingStep_GetStepType tests the GetStepType method of PostProcessingStep
func TestPostProcessingStepGetStepType(t *testing.T) {
	stepType := "TestStepType"
	p := PostProcessingStep{Type: "  " + stepType + "  "}
	if got := p.GetStepType(); got != strings.ToLower(strings.TrimSpace(stepType)) {
		t.Errorf("GetStepType() = %v, want %v", got, stepType)
	}
}

// TestPostProcessingStep_GetDetails tests the GetDetails method of PostProcessingStep
func TestPostProcessingStepGetDetails(t *testing.T) {
	expectedDetails := map[string]interface{}{
		"key1": "value1",
		"key2": "value2",
	}
	p := PostProcessingStep{
		Details: map[string]interface{}{
			"key1": "value1",
			"key2": "value2",
		},
	}
	if got := p.GetDetails(); !reflect.DeepEqual(got, expectedDetails) {
		t.Errorf("GetDetails() = %v, want %v", got, expectedDetails)
	}
}
