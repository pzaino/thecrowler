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
	"testing"

	rules "github.com/pzaino/thecrowler/pkg/ruleset"
)

// Mock data for tests
var testRules = []struct {
	name             string
	rule             rules.CrawlingRule
	expectedURLCount int
}{
	{
		name: "FuzzQueryParameter",
		rule: rules.CrawlingRule{
			RequestType: "GET",
			TargetElements: []rules.TargetElement{
				{
					SelectorType: "query",
					Selector:     "q",
				},
			},
			FuzzingParameters: []rules.FuzzingParameter{
				{
					ParameterName: "query",
					FuzzingType:   "fixed_list",
					Values:        []string{"fuzz1", "fuzz2", "fuzz3"},
				},
			},
		},
		expectedURLCount: 3,
	},
	{
		name: "FuzzURLPath",
		rule: rules.CrawlingRule{
			RequestType: "GET",
			TargetElements: []rules.TargetElement{
				{
					SelectorType: "path",
					Selector:     "to-be-fuzzed",
				},
			},
			FuzzingParameters: []rules.FuzzingParameter{
				{
					ParameterName: "path",
					FuzzingType:   "fixed_list",
					Values:        []string{"fuzzedPath1", "fuzzedPath2"},
				},
			},
		},
		expectedURLCount: 2,
	},
	{
		name: "FuzzBothQueryAndPath",
		rule: rules.CrawlingRule{
			RequestType: "GET",
			TargetElements: []rules.TargetElement{
				{
					SelectorType: "query",
					Selector:     "page",
				},
				{
					SelectorType: "path",
					Selector:     "to-be-fuzzed",
				},
			},
			FuzzingParameters: []rules.FuzzingParameter{
				{
					ParameterName: "query",
					FuzzingType:   "fixed_list",
					Values:        []string{"10", "20"},
				},
				{
					ParameterName: "path",
					FuzzingType:   "fixed_list",
					Values:        []string{"fuzzedPath1", "fuzzedPath2"},
				},
			},
		},
		expectedURLCount: 4, // 2 for the query "page" parameter and 2 for the path
	},
}

func TestFuzzURL(t *testing.T) {
	baseURL := "http://example.com/search?q=test&page=1"

	for _, tc := range testRules {
		t.Run(tc.name, func(t *testing.T) {
			fuzzedURLs, err := FuzzURL(baseURL, tc.rule)
			if err != nil {
				t.Errorf("FuzzURL returned an error: %v", err)
			}
			if len(fuzzedURLs) != tc.expectedURLCount {
				t.Errorf("Expected %d fuzzed URLs, got %d", tc.expectedURLCount, len(fuzzedURLs))
			}
		})
	}
}
