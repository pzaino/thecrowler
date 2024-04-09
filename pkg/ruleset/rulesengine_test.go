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
