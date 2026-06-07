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
	"encoding/xml"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	vdi "github.com/pzaino/thecrowler/pkg/vdi"

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
	// TextType is the text content type
	TextType = "text/plain"
	// TextEmptyType is the empty text content type
	TextEmptyType = "text/empty"
	// HTMLType is the HTML content type
	HTMLType = "text/html"
	// JSONType is the JSON content type
	JSONType = "application/json"

	spanTag = "span"

	// Maximum XML attributes per element to prevent allocation overflow
	maxAttributesPerElement = 1024
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
	cmn.DebugMsg(cmn.DbgLvlDebug3, "Loading content type detection rules from file: %s", filePath)
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

		reCounter := 0
		reGoodCounter := 0
		// Compile the patterns
		for _, pattern := range rule.ContentPatterns {
			reCounter++
			if pattern == "" {
				continue
			}
			re, err := regexp.Compile(pattern)
			if err != nil || re == nil {
				cmn.DebugMsg(cmn.DbgLvlError, "Failed to compile content pattern: %s", pattern)
				continue
			}
			reGoodCounter++
			newRule.CompContentPatterns = append(newRule.CompContentPatterns, re)
		}
		cmn.DebugMsg(cmn.DbgLvlInfo, "Successfully compiled %d content patterns for tag '%s' out of %d", reGoodCounter, rule.Tag, reCounter)

		// Compile the URL patterns
		reCounter = 0
		reGoodCounter = 0
		for _, pattern := range rule.URLPatterns {
			reCounter++
			if pattern == "" {
				continue
			}
			re, err := regexp.Compile(pattern)
			if err != nil || re == nil {
				cmn.DebugMsg(cmn.DbgLvlError, "Failed to compile URL pattern: %s", pattern)
				continue
			}
			reGoodCounter++
			newRule.CompURLPatterns = append(newRule.CompURLPatterns, re)
		}
		cmn.DebugMsg(cmn.DbgLvlInfo, "Successfully compiled %d URL patterns for tag '%s' out of %d", reGoodCounter, rule.Tag, reCounter)

		// Store the modified struct back into the map
		contentTypeDetectionMap[key] = newRule
	}

	return nil
}

func detectContentType(body, url string, wd vdi.WebDriver) string {
	//cmn.DebugMsg(cmn.DbgLvlDebug3, "Detecting content using detectContentType()...")

	// Check if the content is empty
	if body == "" && url == "" {
		return TextEmptyType
	}

	// Copy body to an object we can modify
	bodyStr := strings.TrimSpace(body)
	bodyStr = html.UnescapeString(bodyStr)
	urlStr := strings.TrimSpace(url)
	urlStr = html.UnescapeString(urlStr)

	// Check if the content is empty after unescaping
	if bodyStr == "" && urlStr == "" {
		return TextEmptyType
	}

	// Start content type detection
	index := 0
	for _, rule := range contentTypeDetectionMap {
		// Check content-based patterns
		if bodyStr != "" {
			for i := 0; i < len(rule.CompContentPatterns); i++ {
				if rule.CompContentPatterns[i] == nil {
					continue
				}
				if rule.CompContentPatterns[i].MatchString(bodyStr) {
					//cmn.DebugMsg(cmn.DbgLvlDebug3, "Matched content pattern: %s", rule.CompContentPatterns[i].String())
					return strings.ToLower(strings.TrimSpace(rule.Tag))
				}
				//cmn.DebugMsg(cmn.DbgLvlDebug5, "No match for content pattern: '%s' for '%s...'", CompContentPatterns[i].String(), bodyStr[:20])
			}
		}

		// Check URL-based patterns (file extension, API patterns, etc.)
		if urlStr != "" {
			for i := 0; i < len(rule.CompURLPatterns); i++ {
				if rule.CompURLPatterns[i] == nil {
					continue
				}
				if rule.CompURLPatterns[i].MatchString(urlStr) {
					//cmn.DebugMsg(cmn.DbgLvlDebug3, "Matched URL pattern: %s", rule.CompURLPatterns[i].String())
					return strings.ToLower(strings.TrimSpace(rule.Tag))
				}
			}
		}

		// Keep session alive
		if index%2 == 0 {
			err := KeepSessionAlive(wd)
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "Error keeping session alive during content type detection: %v", err)
				return ErrUnknownContentType
			}
		}
		index++
	}
	return ErrUnknownContentType
}

// Convert XML to JSON (map format)
/* old implementation:
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
*/

// xmlToJSON parses XML with a token walker and returns a generic JSONable structure.
// Shape:
//
//	{ "Root": { "@attr": "...", "#text": "...", "Child": [ {...}, {...} ] } }
func xmlToJSON(xmlStr string) (interface{}, error) {
	dec := xml.NewDecoder(strings.NewReader(xmlStr))

	type element struct {
		Name string
		Node map[string]interface{}
	}

	var stack []element

	// Helper to append child under key, auto-array on duplicate keys
	appendChild := func(parent map[string]interface{}, key string, val interface{}) {
		if existing, ok := parent[key]; ok {
			switch arr := existing.(type) {
			case []interface{}:
				parent[key] = append(arr, val)
			default:
				parent[key] = []interface{}{arr, val}
			}
		} else {
			parent[key] = val
		}
	}

	for {
		tok, err := dec.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return nil, fmt.Errorf("xml token error: %w", err)
		}

		switch t := tok.(type) {
		case xml.StartElement:
			allocSize := len(t.Attr)
			if allocSize < 0 {
				allocSize = 0
			}
			if allocSize > maxAttributesPerElement {
				allocSize = maxAttributesPerElement
			}
			// Ensure allocSize+2 won't overflow int
			if allocSize > (int(^uint(0)>>1))-2 {
				// Set allocSize to maximum allowed
				allocSize = (int(^uint(0) >> 1)) - 2
			}
			node := make(map[string]interface{}, allocSize+2)
			// attributes -> "@name"
			for _, a := range t.Attr {
				key := "@" + a.Name.Local
				node[key] = a.Value
			}
			stack = append(stack, element{Name: t.Name.Local, Node: node})

		case xml.CharData:
			if len(stack) == 0 {
				continue
			}
			txt := strings.TrimSpace(string([]byte(t)))
			if txt == "" {
				continue
			}
			// accumulate text
			if v, ok := stack[len(stack)-1].Node["#text"]; ok {
				stack[len(stack)-1].Node["#text"] = strings.TrimSpace(v.(string) + " " + txt)
			} else {
				stack[len(stack)-1].Node["#text"] = txt
			}

		case xml.EndElement:
			// pop
			if len(stack) == 0 {
				continue
			}
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]

			// finished root?
			if len(stack) == 0 {
				// return { RootName: cur.Node }
				return map[string]interface{}{cur.Name: cur.Node}, nil
			}

			// attach to parent
			parent := stack[len(stack)-1]
			appendChild(parent.Node, cur.Name, cur.Node)
		}
	}

	// no elements
	return nil, fmt.Errorf("empty XML")
}
