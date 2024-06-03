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

// Package crawler implements the crawling logic of the application.
// It's responsible for crawling a website and extracting information from it.
package crawler

import (
	"fmt"
	"log"
	"testing"
	"time"

	rs "github.com/pzaino/thecrowler/pkg/ruleset"
)

const (
	goodTestFile = "./test_data/test-ruleset.yaml"
)

func TestParseRules(t *testing.T) {
	schema, _ := rs.LoadSchema("../../schemas/ruleset-schema.json")

	// Call the function
	sites, err := rs.BulkLoadRules(schema, goodTestFile)

	// Check for errors
	if err != nil {
		t.Errorf("BulkLoadRules returned an error: %v", err)
	}

	// Check if the returned structure matches the expected output
	if len(sites) == 0 {
		t.Errorf("No rules were parsed")
	}

	// Additional checks can be added to validate the contents of 'sites'
}

func TestParseRulesInvalidFile(t *testing.T) {
	schema, _ := rs.LoadSchema("../../schemas/ruleset-schema.json")

	r, err := rs.BulkLoadRules(schema, "./test_data/invalid_ruleset.yaml")
	if (schema != nil) && (err == nil) {
		if r != nil && r[0].Name == "InvalidRuleset" {
			// Given we are testing an invalid ruleset, we should not get any rules back
			t.Errorf("Expected nil, got %v", r)
		}
	}
}

func TestIsGroupValid(t *testing.T) {
	// Create a RuleEngine instance with test data
	re, err := rs.InitializeLibrary(goodTestFile)
	if err != nil {
		t.Errorf("InitializeLibrary returned an error: %v", err)
	}

	// Test data
	layoutFrom := rs.CustomTime{
		Time: time.Date(2021, time.January, 1, 0, 0, 0, 0, time.UTC),
	}
	layoutTo := rs.CustomTime{
		Time: time.Date(2023, time.December, 31, 0, 0, 0, 0, time.UTC),
	}
	//const layoutFrom = "2021-01-01"
	//const layoutTo = "2023-12-31"

	LayoutToFuture := rs.CustomTime{
		Time: time.Now().AddDate(0, 0, 1),
	}

	// Test cases
	testCases := []struct {
		name     string
		group    rs.RuleGroup
		expected bool
	}{
		{"ValidGroup", rs.RuleGroup{
			GroupName: "ValidGroup",
			IsEnabled: true,
			ValidFrom: layoutFrom,
			ValidTo:   LayoutToFuture},
			true},
		{"InvalidGroupDisabled", rs.RuleGroup{
			GroupName: "InvalidGroupDisabled",
			IsEnabled: false,
			ValidFrom: layoutFrom,
			ValidTo:   layoutTo},
			false},
		{"InvalidGroupDate", rs.RuleGroup{
			GroupName: "InvalidGroupDate",
			IsEnabled: true,
			ValidFrom: layoutFrom,
			ValidTo:   layoutTo},
			false},
		// Add more test cases as needed
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := re.IsGroupValid(tc.group)
			if result != tc.expected {
				t.Errorf("Expected %v, got %v", tc.expected, result)
			}
		})
	}
}

func TestFindRulesForSite(t *testing.T) {
	// Mock data or load from a test file
	engine, err := rs.InitializeLibrary(goodTestFile)
	if err != nil {
		t.Errorf("InitializeLibrary returned an error: %v", err)
	}

	testCases := []struct {
		name     string
		url      string
		expected string
		isError  bool
	}{
		{"ValidSite", "http://example.com/somepath", "example.com", false},
		{"NonMatchingSite", "http://nonexistentsite.com", "", false},
		{"EmptyURL", "", "", true},
		// Add more test cases as needed
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			log.Printf("Testing %s", tc.name)
			siteRules, err := engine.FindRulesForSite(tc.url)
			if (err != nil) != tc.isError {
				t.Errorf("findRulesForSite(%s) unexpected error result: %v", tc.url, err)
			}

			if siteRules == nil {
				fmt.Printf("No rules found for '%s'\n", tc.url)
			} else {
				if err == nil && siteRules.Name != tc.expected {
					t.Errorf("findRulesForSite(%s) expected %s, got %s", tc.url, tc.expected, siteRules.Name)
				}
			}
		})
	}
}
