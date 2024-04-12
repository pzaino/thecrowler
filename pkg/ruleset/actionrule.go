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

///// --------------------- ActionRule ------------------------------- /////

// GetActionType returns the action type for the specified action rule.
func (r *ActionRule) GetActionType() string {
	return strings.ToLower(strings.TrimSpace(r.ActionType))
}

// GetRuleName returns the rule name for the specified action rule.
func (r *ActionRule) GetRuleName() string {
	return strings.TrimSpace(r.RuleName)
}

// GetURL returns the URL for the specified action rule.
func (r *ActionRule) GetURL() string {
	return strings.TrimSpace(r.URL)
}

// GetSelectors returns the selectors for the specified action rule.
func (r *ActionRule) GetSelectors() []Selector {
	return r.Selectors
}

// GetValue returns the value for the specified action rule.
func (r *ActionRule) GetValue() string {
	return strings.TrimSpace(r.Value)
}

// GetWaitConditions returns the wait conditions for the specified action rule.
func (r *ActionRule) GetWaitConditions() []WaitCondition {
	return r.WaitConditions
}

// GetConditions returns the conditions for the specified action rule.
func (r *ActionRule) GetConditions() map[string]interface{} {
	return r.Conditions
}

// GetErrorHandling returns the error handling configuration for the specified action rule.
func (r *ActionRule) GetErrorHandling() ErrorHandling {
	return r.ErrorHandling
}

///// ------------------------ Selector ---------------------------- /////

// GetSelectorType returns the selector type for the specified selector.
func (s *Selector) GetSelectorType() string {
	return strings.ToLower(strings.TrimSpace(s.SelectorType))
}

// GetSelector returns the selector for the specified selector.
func (s *Selector) GetSelector() string {
	return strings.TrimSpace(s.Selector)
}

// GetAttribute returns the attribute for the specified selector.
func (s *Selector) GetAttribute() (string, string) {
	return strings.TrimSpace(s.Attribute.Name), strings.TrimSpace(s.Attribute.Value)
}
