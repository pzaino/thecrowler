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
	"github.com/google/go-cmp/cmp"

	"testing"
)

func TestParseRules(t *testing.T) {
	// Create a temporary YAML file for testing
	tempFile := "./test-ruleset.yaml"

	// Call the ParseRules function with the temporary file
	sites, err := BulkLoadRules(nil, tempFile)
	if err != nil {
		t.Fatalf("ParseRules returned an error: %v", err)
	}

	// Verify the parsed rules
	expectedSites := rulesets
	if diff := cmp.Diff(expectedSites, sites); diff != "" {
		t.Errorf("Parsed rules mismatch (-expected +actual):\n%s", diff)
	}

	/*
		if !reflect.DeepEqual(sites, expectedSites) {
			t.Errorf("Parsed rules do not match expected rules")
		}
	*/
}

func TestInitializeLibrary(t *testing.T) {
	engine, err := InitializeLibrary("./test-ruleset.yaml")
	if err != nil {
		t.Fatalf("InitializeLibrary returned an error: %v", err)
	}
	if engine == nil {
		t.Errorf("Expected non-nil engine, got nil")
	}
}
