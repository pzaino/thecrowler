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

func TestActionRuleGetActionType(t *testing.T) {
	ar := ActionRule{ActionType: " Click "}
	expected := "click"
	if got := ar.GetActionType(); got != expected {
		t.Errorf("GetActionType() = %v, want %v", got, expected)
	}
}

func TestActionRuleGetRuleName(t *testing.T) {
	ar := ActionRule{RuleName: " LoginButton "}
	expected := "LoginButton"
	if got := ar.GetRuleName(); got != expected {
		t.Errorf("GetRuleName() = %v, want %v", got, expected)
	}
}

func TestActionRuleGetURL(t *testing.T) {
	ar := ActionRule{URL: " https://example.com/login "}
	expected := "https://example.com/login"
	if got := ar.GetURL(); got != expected {
		t.Errorf("GetURL() = %v, want %v", got, expected)
	}
}

func TestActionRuleGetSelectors(t *testing.T) {
	ar := ActionRule{
		Selectors: []Selector{
			{SelectorType: "css", Selector: "#login"},
		},
	}
	expected := []Selector{{SelectorType: "css", Selector: "#login"}}
	if got := ar.GetSelectors(); !reflect.DeepEqual(got, expected) {
		t.Errorf("GetSelectors() = %v, want %v", got, expected)
	}
}

func TestActionRuleGetValue(t *testing.T) {
	ar := ActionRule{Value: " user@example.com "}
	expected := "user@example.com"
	if got := ar.GetValue(); got != expected {
		t.Errorf("GetValue() = %v, want %v", got, expected)
	}
}

func TestActionRuleGetWaitConditions(t *testing.T) {
	ar := ActionRule{WaitConditions: []WaitCondition{
		{
			ConditionType: "wait",
			Selector:      Selector{},
			CustomJS:      "",
			Value:         "2",
		},
	}}
	expected := []WaitCondition{{ConditionType: "wait", Selector: Selector{}, CustomJS: "", Value: "2"}}
	if got := ar.GetWaitConditions(); !reflect.DeepEqual(got, expected) {
		t.Errorf("GetWaitConditions() = %v, want %v", got, expected)
	}
}

func TestActionRuleGetConditions(t *testing.T) {
	ar := ActionRule{Conditions: map[string]interface{}{
		"test": "test",
	}}
	expected := map[string]interface{}{
		"test": "test",
	}
	if got := ar.GetConditions(); !reflect.DeepEqual(got, expected) {
		t.Errorf("GetConditions() = %v, want %v", got, expected)
	}
}

func TestActionRuleGetErrorHandling(t *testing.T) {
	ar := ActionRule{ErrorHandling: ErrorHandling{
		Ignore: true,
	}}
	expected := ErrorHandling{Ignore: true}
	if got := ar.GetErrorHandling(); !reflect.DeepEqual(got, expected) {
		t.Errorf("GetErrorHandling() = %v, want %v", got, expected)
	}
}

func TestSelectorGetSelectorType(t *testing.T) {
	s := Selector{SelectorType: " ID "}
	expected := "id"
	if got := s.GetSelectorType(); got != expected {
		t.Errorf("GetSelectorType() = %v, want %v", got, expected)
	}
}

func TestSelectorGetSelector(t *testing.T) {
	s := Selector{Selector: " #username "}
	expected := "#username"
	if got := s.GetSelector(); got != expected {
		t.Errorf("GetSelector() = %v, want %v", got, expected)
	}
}

func TestSelectorGetAttribute(t *testing.T) {
	s := Selector{
		Attribute: struct {
			Name  string `json:"name" yaml:"name"`
			Value string `json:"value" yaml:"value"`
		}{
			Name:  "value",
			Value: "value",
		},
	}
	expected := "value"
	if gotName, _ := s.GetAttribute(); gotName != expected {
		t.Errorf("GetAttribute() = %v, want %v", gotName, expected)
	}
}
