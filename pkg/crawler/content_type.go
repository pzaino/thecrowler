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

// Package crawler implements the crawler library for the Crowler
package crawler

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"

	cmn "github.com/pzaino/thecrowler/pkg/common"

	"golang.org/x/net/html"
	"gopkg.in/yaml.v2"
)

const (
	// ErrUnknownContentType is the error message for unknown content types
	ErrUnknownContentType = "unknown"

	// XMLType1 is the XML content type
	XMLType1 = "application/xml"
	// XMLType2 is the XML content type
	XMLType2 = "text/xml"
)

var (
	// contentTypeDetectionMap maps the content type detection rules to the file extension.
	contentTypeDetectionMap ContentTypeDetectionRules

	// loadMutex is the mutex to protect the loading of the content type detection rules.
	loadMutex sync.Mutex
)

// ContentTypeDetectionRules represents the content type detection rules for a web page.
type ContentTypeDetectionRules map[string]struct {
	Tag                 string           `json:"tag" yaml:"tag"`                           // The tag to return if matched.
	ContentPatterns     []string         `json:"content_patterns" yaml:"content_patterns"` // The data content patterns to match.
	URLPatterns         []string         `json:"url_patterns" yaml:"url_patterns"`         // The URL patterns to match.
	CompContentPatterns []*regexp.Regexp // Compiled content patterns
	CompURLPatterns     []*regexp.Regexp // Compiled URL patterns
}

// IsEmpty checks if the content type detection rules are empty.
func (c *ContentTypeDetectionRules) IsEmpty() bool {
	return len(*c) == 0
}

func loadContentTypeDetectionRules(filePath string) error {
	loadMutex.Lock()
	defer loadMutex.Unlock()

	data, err := os.ReadFile(filePath) //nolint:gosec // This is a read-only operation and the path is provided by the system, not the user.
	if err != nil {
		return err
	}

	// Let's allocate the map
	if contentTypeDetectionMap == nil {
		contentTypeDetectionMap = make(ContentTypeDetectionRules)
	}

	err = yaml.Unmarshal(data, &contentTypeDetectionMap)
	if err != nil {
		return err
	}

	// Compile the regular expressions
	for key, rule := range contentTypeDetectionMap {
		// Create a new struct to modify
		newRule := rule

		for _, pattern := range rule.ContentPatterns {
			if pattern == "" {
				continue
			}
			re := regexp.MustCompile(pattern)
			if re == nil {
				cmn.DebugMsg(cmn.DbgLvlError, "Failed to compile content pattern: %s", pattern)
				continue
			}
			cmn.DebugMsg(cmn.DbgLvlDebug5, "Compiled content pattern: %v", re)
			newRule.CompContentPatterns = append(newRule.CompContentPatterns, re)
		}

		for _, pattern := range rule.URLPatterns {
			if pattern == "" {
				continue
			}
			re := regexp.MustCompile(pattern)
			if re == nil {
				cmn.DebugMsg(cmn.DbgLvlError, "Failed to compile URL pattern: %s", pattern)
				continue
			}
			cmn.DebugMsg(cmn.DbgLvlDebug5, "Compiled URL pattern: %v", re)
			newRule.CompURLPatterns = append(newRule.CompURLPatterns, re)
		}

		// Store the modified struct back into the map
		contentTypeDetectionMap[key] = newRule
	}

	return nil
}

func detectContentType(body, url string) string {
	cmn.DebugMsg(cmn.DbgLvlDebug3, "Detecting content using detectContentType()...")

	// Copy body to an object we can modify
	bodyStr := strings.TrimSpace(body)
	bodyStr = html.UnescapeString(bodyStr)
	urlStr := strings.TrimSpace(url)
	urlStr = html.UnescapeString(urlStr)

	for _, rule := range contentTypeDetectionMap {
		// Check content-based patterns
		if bodyStr != "" {
			for _, pattern := range rule.CompContentPatterns {
				if pattern == nil {
					continue
				}
				if pattern.MatchString(bodyStr) {
					cmn.DebugMsg(cmn.DbgLvlDebug3, "Matched content pattern: %s", pattern.String())
					return strings.ToLower(strings.TrimSpace(rule.Tag))
				}
				//cmn.DebugMsg(cmn.DbgLvlDebug5, "No match for content pattern: '%s' for '%s...'", pattern.String(), bodyStr[:20])
			}
		}
		// Check URL-based patterns (file extension, API patterns, etc.)
		if urlStr != "" {
			for _, pattern := range rule.CompURLPatterns {
				if pattern == nil {
					continue
				}
				if pattern.MatchString(urlStr) {
					cmn.DebugMsg(cmn.DbgLvlDebug3, "Matched URL pattern: %s", pattern.String())
					return strings.ToLower(strings.TrimSpace(rule.Tag))
				}
			}
		}
	}
	return ErrUnknownContentType
}

// Convert XML to JSON (map format)
// Convert XML to JSON (map format)
func xmlToJSON(xmlStr string) (interface{}, error) {
	// Define a generic container
	var result interface{}

	// Create an XML decoder
	decoder := xml.NewDecoder(strings.NewReader(xmlStr))

	// Decode XML into a structured format
	err := decoder.Decode(&result)
	if err != nil {
		return nil, fmt.Errorf("failed to decode XML: %w", err)
	}

	// Marshal back to JSON (ensures we have a proper structure)
	jsonData, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal XML to JSON: %w", err)
	}

	// Convert JSON string to a map
	var finalResult interface{}
	if err := json.Unmarshal(jsonData, &finalResult); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %w", err)
	}

	return finalResult, nil
}
