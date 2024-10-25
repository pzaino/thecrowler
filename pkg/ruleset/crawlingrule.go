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
)

///// --------------------- CrawlingRule ------------------------------- /////

// GetRuleName returns the rule name for the specified crawling rule.
func (c *CrawlingRule) GetRuleName() string {
	return strings.TrimSpace(c.RuleName)
}

// GetRequestType returns the request type for the specified crawling rule.
// GET or POST etc.
func (c *CrawlingRule) GetRequestType() string {
	return strings.ToUpper(strings.TrimSpace(c.RequestType))
}

// GetTargetElements returns the target elements for the specified crawling rule.
func (c *CrawlingRule) GetTargetElements() []TargetElement {
	return c.TargetElements
}

// GetFuzzingParameters returns the fuzzing parameters for the specified crawling rule.
func (c *CrawlingRule) GetFuzzingParameters() []FuzzingParameter {
	return c.FuzzingParameters
}

///// --------------------- TargetElement ------------------------------- /////

// GetSelectorType returns the selector type for the specified target element.
func (t *TargetElement) GetSelectorType() string {
	return strings.ToLower(strings.TrimSpace(t.SelectorType))
}

// GetSelector returns the selector for the specified target element.
func (t *TargetElement) GetSelector() string {
	return strings.TrimSpace(t.Selector)
}

///// --------------------- FuzzingParameter ------------------------------- /////

// GetParameterName returns the parameter name for the specified fuzzing parameter.
func (f *FuzzingParameter) GetParameterName() string {
	return strings.TrimSpace(f.ParameterName)
}

// GetFuzzingType returns the fuzzing type for the specified fuzzing parameter.
func (f *FuzzingParameter) GetFuzzingType() string {
	return strings.ToLower(strings.TrimSpace(f.FuzzingType))
}

// GetValues returns the list of values for the specified fuzzing parameter.
func (f *FuzzingParameter) GetValues() []string {
	trimmedValues := []string{}
	for _, v := range f.Values {
		trimmedValues = append(trimmedValues, strings.TrimSpace(v))
	}
	return trimmedValues
}

// GetPattern returns the pattern for the specified fuzzing parameter.
func (f *FuzzingParameter) GetPattern() string {
	return strings.TrimSpace(f.Pattern)
}

// GetSelector returns the selector for the specified fuzzing parameter.
func (f *FuzzingParameter) GetSelector() string {
	return strings.TrimSpace(f.Selector)
}
