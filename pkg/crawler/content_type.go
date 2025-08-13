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
)

var (
	// contentTypeDetectionMap maps the content type detection rules to the file extension.
	contentTypeDetectionMap ContentTypeDetectionRules

	// loadMutex is the mutex to protect the loading of the content type detection rules.
	loadMutex sync.Mutex
)

// HTMLNode represents the structure for JSON output
type HTMLNode struct {
	Tag        string            `json:"tag,omitempty"`
	Text       string            `json:"text,omitempty"`
	URL        string            `json:"url,omitempty"`
	Comment    string            `json:"comment,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
	Children   []HTMLNode        `json:"children,omitempty"`
}

// List of attributes to ignore
var ignoredAttributes = map[string]bool{
	"style": true,
	"class": true,
	"id":    true,
}

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
			KeepSessionAlive(&wd)
		}
		index++
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

// ExtractHTMLData extracts the relevant data from an HTML node
func ExtractHTMLData(n *html.Node) HTMLNode {
	var node HTMLNode

	switch n.Type {
	case html.ElementNode:
		// Skip useless tags
		tag := strings.ToLower(n.Data)
		if tag == "script" ||
			tag == "noscript" ||
			tag == "iframe" ||
			tag == "svg" ||
			tag == "img" ||
			tag == "base" ||
			tag == "input" ||
			tag == "button" ||
			tag == "select" ||
			tag == "option" ||
			tag == "textarea" ||
			tag == "form" ||
			tag == "style" {
			return HTMLNode{} // Return empty node, effectively removing it
		}

		node.Tag = n.Data
		node.Attributes = make(map[string]string)

		// **If parent is <span>, merge child text instead of creating nested <span>**
		isParentSpan := n.Parent != nil && strings.ToLower(n.Parent.Data) == spanTag
		var childTextBuffer strings.Builder

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.TextNode {
				text := strings.TrimSpace(c.Data)
				if text != "" {
					if isParentSpan {
						// Merge text directly if parent is also <span>
						childTextBuffer.WriteString(" " + text)
					} else {
						node.Text = text
					}
				}
			}
		}

		// Merge collected text into the parent <span>
		if isParentSpan && childTextBuffer.Len() > 0 {
			return HTMLNode{Text: childTextBuffer.String()}
		}

		// Extract URLs and relevant attributes
		for _, attr := range n.Attr {
			key := strings.ToLower(attr.Key)

			// Store URLs in the `URL` field
			if key == "href" || key == "src" || key == "action" {
				if !ignoredURLs(attr.Val) {
					node.URL = attr.Val
				} else {
					// Skip ignored URLs
					return HTMLNode{}
				}
			} else if !ignoredAttributes[key] {
				// Store only meaningful attributes
				node.Attributes[key] = attr.Val
			}
		}

	case html.CommentNode:
		node.Comment = strings.TrimSpace(n.Data)
	}

	// Recursively parse child nodes, ignoring empty nodes
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		child := ExtractHTMLData(c)

		// **Skip unnecessary nested <span> elements**
		if strings.ToLower(n.Data) == spanTag && strings.ToLower(child.Tag) == spanTag {
			// Merge text from nested <span> instead of nesting it
			if child.Text != "" {
				node.Text += " " + child.Text
			}
		} else {
			if child.Tag != "" || child.Text != "" || child.URL != "" || child.Comment != "" || len(child.Attributes) > 0 {
				node.Children = append(node.Children, child)
			}
		}
	}

	return node
}

// ignoredURLs checks if the URL should be ignored
func ignoredURLs(url string) bool {
	// Check if the URL is empty
	if url == "" {
		return true
	}

	// Check if the URL is a data URL
	if strings.HasPrefix(url, "data:") {
		return true
	}

	// Check if the URL is a JavaScript URL
	if strings.HasPrefix(url, "javascript:") {
		return true
	}

	// Check if the URL is a mailto URL
	if strings.HasPrefix(url, "mailto:") {
		return true
	}

	// Check if the URL is a tel URL
	if strings.HasPrefix(url, "tel:") {
		return true
	}

	// Looks like a valid URL
	return false
}
