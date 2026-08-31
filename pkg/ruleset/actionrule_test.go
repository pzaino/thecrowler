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
	"encoding/json"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"

	cmn "github.com/pzaino/thecrowler/pkg/common"
)

func TestActionRuleTargetSelectorsAndStoreAsRoundTrip(t *testing.T) {
	original := ActionRule{
		RuleName:        "drag-alert-result",
		TargetSelectors: []Selector{{SelectorType: "css", Selector: "#drop-zone"}},
		StoreAs:         "dialog.message",
	}

	tests := []struct {
		name      string
		marshal   func(interface{}) ([]byte, error)
		unmarshal func([]byte, interface{}) error
	}{
		{name: "JSON", marshal: json.Marshal, unmarshal: json.Unmarshal},
		{name: "YAML", marshal: yaml.Marshal, unmarshal: yaml.Unmarshal},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data, err := tc.marshal(original)
			if err != nil {
				t.Fatal(err)
			}
			var decoded ActionRule
			if err := tc.unmarshal(data, &decoded); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(decoded.TargetSelectors, original.TargetSelectors) {
				t.Fatalf("target selectors did not survive round trip: %#v", decoded.TargetSelectors)
			}
			if decoded.StoreAs != original.StoreAs {
				t.Fatalf("store_as did not survive round trip: %q", decoded.StoreAs)
			}
		})
	}
}

func TestActionRuleGetActionType(t *testing.T) {
	ar := ActionRule{ActionType: " Click "}
	expected := cmn.ClickStr
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
			Value:         "2",
		},
	}}
	expected := []WaitCondition{{ConditionType: "wait", Selector: Selector{}, Value: "2"}}
	if got := ar.GetWaitConditions(); !reflect.DeepEqual(got, expected) {
		t.Errorf("GetWaitConditions() = %v, want %v", got, expected)
	}
}

func TestActionRuleGetConditions(t *testing.T) {
	condition := &ActionCondition{Type: "element", Selector: "#test"}
	ar := ActionRule{Conditions: condition}
	expected := condition
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

func TestActionRuleErrorHandlingDecoding(t *testing.T) {
	tests := []struct {
		name   string
		data   string
		decode func([]byte, interface{}) error
		ignore bool
	}{
		{name: "JSON false", data: `{"rule_name":"retry","action_type":"click","error_handling":{"ignore":false,"retry_count":2,"retry_delay":3}}`, decode: json.Unmarshal},
		{name: "JSON true", data: `{"rule_name":"retry","action_type":"click","error_handling":{"ignore":true,"retry_count":2,"retry_delay":3}}`, decode: json.Unmarshal, ignore: true},
		{name: "YAML false", data: "rule_name: retry\naction_type: click\nerror_handling:\n  ignore: false\n  retry_count: 2\n  retry_delay: 3\n", decode: yaml.Unmarshal},
		{name: "YAML true", data: "rule_name: retry\naction_type: click\nerror_handling:\n  ignore: true\n  retry_count: 2\n  retry_delay: 3\n", decode: yaml.Unmarshal, ignore: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rule ActionRule
			if err := tt.decode([]byte(tt.data), &rule); err != nil {
				t.Fatalf("decode action rule: %v", err)
			}
			if rule.ErrorHandling.Ignore != tt.ignore {
				t.Errorf("Ignore = %t, want %t", rule.ErrorHandling.Ignore, tt.ignore)
			}
			if rule.ErrorHandling.RetryCount != 2 {
				t.Errorf("RetryCount = %d, want 2", rule.ErrorHandling.RetryCount)
			}
			if rule.ErrorHandling.RetryDelay != 3 {
				t.Errorf("RetryDelay = %d, want 3", rule.ErrorHandling.RetryDelay)
			}
		})
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
