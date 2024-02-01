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

package scrapper

import (
	"log"
	"testing"
	"time"
)

const (
	goodTestFile = "./test_rules.yaml"
)

func TestParseRules(t *testing.T) {
	// Call the function
	sites, err := ParseRules(goodTestFile)

	// Check for errors
	if err != nil {
		t.Errorf("ParseRules returned an error: %v", err)
	}

	// Check if the returned structure matches the expected output
	if len(sites) == 0 {
		t.Errorf("No rules were parsed")
	}

	// Additional checks can be added to validate the contents of 'sites'
}

func TestParseRulesInvalidFile(t *testing.T) {
	_, err := ParseRules("./invalid_test_rules.yaml")
	if err == nil {
		t.Errorf("Expected error, got none")
	}
}

func TestIsGroupValid(t *testing.T) {
	// Create a RuleEngine instance with test data
	re, err := InitializeLibrary(goodTestFile)
	if err != nil {
		t.Errorf("InitializeLibrary returned an error: %v", err)
	}

	const layoutFrom = "2021-01-01"
	const layoutTo = "2023-12-31"

	LayoutToFuture := time.Now().AddDate(0, 0, 1).Format(layoutTo)

	// Test cases
	testCases := []struct {
		name     string
		group    RuleGroup
		expected bool
	}{
		{"ValidGroup", RuleGroup{
			GroupName: "ValidGroup",
			IsEnabled: true,
			ValidFrom: layoutFrom,
			ValidTo:   LayoutToFuture},
			false},
		{"InvalidGroupDisabled", RuleGroup{
			GroupName: "InvalidGroupDisabled",
			IsEnabled: false,
			ValidFrom: layoutFrom,
			ValidTo:   layoutTo},
			false},
		{"InvalidGroupDate", RuleGroup{
			GroupName: "InvalidGroupDate",
			IsEnabled: true,
			ValidFrom: layoutFrom,
			ValidTo:   layoutTo},
			false},
		// Add more test cases as needed
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := re.isGroupValid(tc.group)
			if result != tc.expected {
				t.Errorf("Expected %v, got %v", tc.expected, result)
			}
		})
	}
}

func TestFindRulesForSite(t *testing.T) {
	// Mock data or load from a test file
	engine, err := InitializeLibrary(goodTestFile)
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
		{"NonMatchingSite", "http://nonexistentsite.com", "", true},
		{"EmptyURL", "", "", true},
		// Add more test cases as needed
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			log.Printf("Testing %s", tc.name)
			siteRules, err := engine.findRulesForSite(tc.url)

			if (err != nil) != tc.isError {
				t.Errorf("findRulesForSite(%s) unexpected error result: %v", tc.url, err)
			}

			if err == nil && siteRules.Site != tc.expected {
				t.Errorf("findRulesForSite(%s) expected %s, got %s", tc.url, tc.expected, siteRules.Site)
			}
		})
	}
}

func TestExtractData(t *testing.T) {
	// Example mock HTML content
	mockHTML := `
        <html>
        <head><title>Test Page</title></head>
        <body>
            <h1 class="article-title">Example Title</h1>
            <div class="article-content">Example content</div>
            <span class="date">2023-03-01</span>
            <script src="script.js"></script>
        </body>
        </html>
    `

	// Mock RuleGroup
	ruleGroup := RuleGroup{
		ScrapingRules: []ScrapingRule{
			{
				Path: "/body",
				Elements: []Element{
					{
						Key:          "title",
						SelectorType: "css",
						Selector:     "h1.article-title",
					},
					{
						Key:          "content",
						SelectorType: "css",
						Selector:     "div.article-content",
					},
					{
						Key:          "date",
						SelectorType: "css",
						Selector:     "span.date",
					},
				},
				JsFiles: true,
			},
		},
	}

	// Initialize RuleEngine (assuming it's already implemented)
	engine := &RuleEngine{}

	// Test extraction
	t.Run("ValidExtraction", func(t *testing.T) {
		extractedData, err := engine.extractData(ruleGroup, "https://example.com/body/", mockHTML)
		if err != nil {
			t.Fatalf("extractData returned an error: %v", err)
		}

		// Log the extracted data for debugging
		// it's a map of string -> interface{}, so we need to convert the values to strings
		log.Printf("Extracted data: %v", extractedData)

		if extractedData["title"] != "Example Title" ||
			extractedData["content"] != "Example content" ||
			extractedData["date"] != "2023-03-01" {
			t.Errorf("extractData did not extract expected data")
		}

		// Additional checks for JS files, etc.
	})

	// Additional test cases for partial matches, no matches, and malformed HTML
}
