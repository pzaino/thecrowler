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
	mockParser := &MockRuleParser{}
	engine, err := NewRuleEngineWithParser(mockParser, "./test-ruleset.yaml")
	if err != nil {
		t.Fatalf("InitializeLibrary returned an error: %v", err)
	}
	if engine == nil {
		t.Errorf("Expected non-nil engine, got nil")
	}
}

func TestGetPluginName(t *testing.T) {
	tests := []struct {
		name       string
		pluginBody string
		file       string
		want       string
	}{
		{
			name:       "Extract name from comment",
			pluginBody: "// name: TestPlugin\nconsole.log('Hello, World!');",
			file:       "test-plugin.js",
			want:       "TestPlugin",
		},
		{
			name:       "Extract name from file name",
			pluginBody: "console.log('Hello, World!');",
			file:       "test-plugin.js",
			want:       "test-plugin",
		},
		{
			name:       "Extract name from comment without prefix",
			pluginBody: "// TestPlugin\nconsole.log('Hello, World!');",
			file:       "test-plugin.js",
			want:       "test-plugin",
		},
		{
			name:       "Empty plugin body",
			pluginBody: "",
			file:       "test-plugin.js",
			want:       "test-plugin",
		},
		{
			name:       "No name in comment",
			pluginBody: "// This is a plugin\nconsole.log('Hello, World!');",
			file:       "test-plugin.js",
			want:       "test-plugin",
		},
		{
			name:       "Name with extra spaces",
			pluginBody: "// name:    TestPlugin   \nconsole.log('Hello, World!');",
			file:       "test-plugin.js",
			want:       "TestPlugin",
		},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getPluginName(tt.pluginBody, tt.file); got != tt.want {
				t.Errorf("getPluginName() = %v, test n.%d want %v", got, i, tt.want)
			}
		})
	}
}
