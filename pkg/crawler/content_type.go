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
	"os"
	"regexp"
	"sync"

	"gopkg.in/yaml.v2"
)

const (
	// ErrUnknownContentType is the error message for unknown content types
	ErrUnknownContentType = "unknown"
)

var (
	// contentTypeDetectionMap maps the content type detection rules to the file extension.
	contentTypeDetectionMap ContentTypeDetectionRules

	// loadMutex is the mutex to protect the loading of the content type detection rules.
	loadMutex sync.Mutex
)

// ContentTypeDetectionRules represents the content type detection rules for a web page.
type ContentTypeDetectionRules map[string]struct {
	Tag             string   `json:"tag" yaml:"tag"`                           // The tag to return if matched.
	ContentPatterns []string `json:"content_patterns" yaml:"content_patterns"` // The data content patterns to match.
	URLPatterns     []string `json:"url_patterns" yaml:"url_patterns"`         // The URL patterns to match.
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
	return yaml.Unmarshal(data, &contentTypeDetectionMap)
}

func detectContentType(body, url string) string {
	for _, rule := range contentTypeDetectionMap {
		// ✅ Check content-based patterns
		for _, pattern := range rule.ContentPatterns {
			re := regexp.MustCompile(pattern)
			if re.MatchString(body) {
				return rule.Tag
			}
		}
		// ✅ Check URL-based patterns (file extension, API patterns, etc.)
		for _, pattern := range rule.URLPatterns {
			re := regexp.MustCompile(pattern)
			if re.MatchString(url) {
				return rule.Tag
			}
		}
	}
	return ErrUnknownContentType
}
