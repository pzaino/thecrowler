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
	"net/url"
	"strings"

	rules "github.com/pzaino/thecrowler/pkg/ruleset"
)

// FuzzURL takes a base URL and a CrawlingRule, generating fuzzed URLs based on the rule's parameters.
func FuzzURL(baseURL string, rule rules.CrawlingRule) ([]string, error) {
	var fuzzedURLs []string

	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}

	fuzzedURLs = fuzzQueryParameters(parsedURL, rule, fuzzedURLs)
	fuzzedURLs = fuzzURLPath(parsedURL, rule, fuzzedURLs)

	return fuzzedURLs, nil
}

func fuzzQueryParameters(parsedURL *url.URL, rule rules.CrawlingRule, fuzzedURLs []string) []string {
	if parsedURL.RawQuery != "" {
		originalQuery := parsedURL.Query()

		for _, fuzzParam := range rule.GetFuzzingParameters() {
			// Check if this parameter is meant to be fuzzed
			if fuzzParam.GetParameterName() == "query" {
				values := generateFuzzValues(fuzzParam)
				for _, value := range values {
					fuzzedQuery := cloneQueryValues(originalQuery)
					selector := fuzzParam.GetSelector() // Declare the selector variable
					fuzzedQuery.Set(selector, value)
					fuzzedURL := *parsedURL
					fuzzedURL.RawQuery = fuzzedQuery.Encode()
					fuzzedURLs = append(fuzzedURLs, fuzzedURL.String())
				}
			}
		}
	}

	return fuzzedURLs
}

func fuzzURLPath(parsedURL *url.URL, rule rules.CrawlingRule, fuzzedURLs []string) []string {
	const strPath = "path"
	for _, target := range rule.GetTargetElements() {
		selectorType := target.GetSelectorType()
		selector := target.GetSelector()

		if selectorType == strPath {
			for _, fuzzParam := range rule.GetFuzzingParameters() {
				if fuzzParam.GetParameterName() == strPath {
					values := generateFuzzValues(fuzzParam)
					for _, value := range values {
						fuzzedURL := *parsedURL
						fuzzedURL.Path = strings.Replace(fuzzedURL.Path, selector, value, 1)
						fuzzedURLs = append(fuzzedURLs, fuzzedURL.String())
					}
				}
			}
		}
	}

	return fuzzedURLs
}

// Helper function to generate fuzz values based on the fuzzing parameter
func generateFuzzValues(fuzzParam rules.FuzzingParameter) []string {
	var values []string
	if fuzzParam.GetFuzzingType() == "pattern_based" {
		// Implement pattern-based value generation logic
		values = append(values, fuzzParam.GetPattern()) // Simplification
	} else {
		values = fuzzParam.GetValues()
	}
	return values
}

// Helper function to clone query values to avoid mutating the original
func cloneQueryValues(originalQuery url.Values) url.Values {
	fuzzedQuery := url.Values{}
	for k, v := range originalQuery {
		fuzzedQuery[k] = v
	}
	return fuzzedQuery
}
