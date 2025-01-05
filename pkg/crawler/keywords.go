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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"unicode"

	cmn "github.com/pzaino/thecrowler/pkg/common"

	"github.com/PuerkitoBio/goquery"
)

const (
	p string = ".,?!:;\"'()[]{}<>"
)

var (
	stopWords     map[string]map[string]struct{}
	initStopWords sync.Once
)

// loadStopWords loads stop words from a JSON file into a map[string]map[string]struct{}
func loadStopWords() {
	stopWords = make(map[string]map[string]struct{}) // Initialize the outer map

	// Get the current working directory
	cwd, err := os.Getwd()
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to get current working directory: %v", err)
		return
	}
	// If running tests, move two levels up
	if filepath.Base(cwd) == "crawler" { // Adjust "crawler" to your module's directory name
		cwd = filepath.Join(cwd, "../../")
	}

	// Construct the full path to stopWords.json
	filePath := filepath.Join(cwd, "stopWords.json")

	// Check if the file exists in the current directory or if it exists in the ./support directory
	if _, err := os.Stat(filePath); err != nil {
		filePath = filepath.Join(cwd, "support", "stopWords.json")
		if _, err := os.Stat(filePath); err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "stopWords.json file not found")
			return // If the file does not exist, return
		}
	}

	// Open the stopWords.json file
	file, err := os.Open(filePath) //nolint:gosec // We are not opening a file based on user input
	if err != nil {
		panic(fmt.Sprintf("Failed to open stopWords.json: %v", err))
	}
	defer file.Close() //nolint:errcheck // We cannot check for the returned value in a defer

	// Decode the JSON into a temporary structure
	temp := make(map[string][]string)
	if err := json.NewDecoder(file).Decode(&temp); err != nil {
		panic(fmt.Sprintf("Failed to decode stopWords.json: %v", err))
	}

	// Convert the temporary map to a map[string]map[string]struct{}
	for lang, words := range temp {
		stopWords[lang] = make(map[string]struct{}, len(words))
		for _, word := range words {
			stopWords[lang][word] = struct{}{}
		}
	}
}

// Function that normalizes a keyword by converting it to lowercase
func normalizeKeyword(word string) string {
	word = strings.ToLower(strings.TrimSpace(word))
	word = strings.Trim(word, p)
	word = strings.TrimSpace(word)
	return word
}

// Function that returns false if the keyword is
// an English language stop word, article, or preposition
// and true otherwise
func isKeyword(word, lang string) bool {
	// Ensure stopWords is initialized
	initStopWords.Do(loadStopWords)

	if strings.TrimSpace(lang) == "" {
		lang = "en" // Default to English
	}

	// Normalize the word: lowercase and trim spaces
	word = strings.ToLower(strings.TrimSpace(word))

	// Check basic conditions:
	// 1. Word length should be at least 3
	// 2. Word should not be a string of only symbols
	// 3. Word should not contain comment markers
	if len(word) < 3 ||
		strings.Trim(word, ".,?!:;'\"()[]{}<>-=+/*\\_") == "" ||
		word == "/*" || word == "*/" || word == "<!--" || word == "-->" {
		return false
	}

	// Check if the language is supported
	langWords, exists := stopWords[lang]
	if !exists {
		// If the language is not supported, treat all words as keywords
		return true
	}

	// Check if the word is a stop word
	_, isStopWord := langWords[word]
	return !isStopWord
}

func extractFromMetaTag(metaTags []MetaTag, tagName string) []string {
	var keywords []string
	tagName = strings.ToLower(strings.TrimSpace(tagName))
	for _, metaTag := range metaTags {
		if strings.ToLower(strings.TrimSpace(metaTag.Name)) == tagName {
			content := metaTag.Content

			// Split the content by spaces, commas, and other punctuation
			words := strings.FieldsFunc(content, func(r rune) bool {
				return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) || strings.ContainsRune(p, r)
			})

			// Process each word
			for _, word := range words {
				trimmedKeyword := normalizeKeyword(word)

				if trimmedKeyword == "" || len(trimmedKeyword) > 45 {
					// Skip empty or too long keywords
					continue
				}

				// Always store if the keyword starts with # or @
				if strings.HasPrefix(trimmedKeyword, "#") || strings.HasPrefix(trimmedKeyword, "@") {
					keywords = append(keywords, trimmedKeyword)
				} else if isKeyword(trimmedKeyword, "") {
					// Check if it is a valid keyword
					keywords = append(keywords, trimmedKeyword)
				}
			}
		}
	}
	return keywords
}

func extractContentKeywords(content string) []string {
	// Split the content by spaces, commas, and other punctuation
	// to extract individual keywords
	words := []string{}

	// Split the content by spaces, commas, and other punctuation
	words = append(words, strings.FieldsFunc(content, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) || strings.ContainsRune(p, r)
	})...)

	keywords := []string{}
	for _, word := range words {
		trimmedWord := normalizeKeyword(word)

		// Remove punctuation
		//trimmedWord = strings.TrimSpace(removePunctuation(trimmedWord))

		if trimmedWord == "" || len(trimmedWord) > 45 {
			// Skip empty (or too long) keywords
			continue
		}

		if strings.HasPrefix(trimmedWord, "#") || strings.HasPrefix(trimmedWord, "@") {
			keywords = append(keywords, trimmedWord)
		} else if isKeyword(trimmedWord, "") {
			keywords = append(keywords, trimmedWord)
		}
	}
	return keywords
}

func unique(strSlice []string) []string {
	keys := make(map[string]bool)
	list := []string{}
	for _, entry := range strSlice {
		if _, value := keys[entry]; !value {
			keys[entry] = true
			list = append(list, entry)
		}
	}
	return list
}

func extractKeywords(pageInfo PageInfo) []string {
	var keywords []string

	// Load the HTML document
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(pageInfo.BodyText))
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "loading HTML content: %v", err)
		return nil
	}

	// Remove script, style tags, and other non-relevant elements
	doc.Find("script, style").Remove()

	// Extract text content with proper spacing
	content := ""
	doc.Find("body").Contents().Each(func(i int, s *goquery.Selection) {
		if goquery.NodeName(s) == "#text" {
			// Append text directly if it's a text node
			content += s.Text() + " "
		} else {
			// Add a space for non-text nodes (block elements)
			content += " "
		}
	})
	content = normalizeText(content)
	content = strings.ReplaceAll(content, "\n", " ")
	content = strings.Join(strings.Fields(content), " ") // Normalize spacing

	// Extract from main content
	contentKeywords := extractContentKeywords(content)
	keywords = append(keywords, contentKeywords...)

	// Extract from meta tags (keywords and description)
	keywords = append(keywords, extractFromMetaTag(pageInfo.MetaTags, "keywords")...)
	keywords = append(keywords, extractFromMetaTag(pageInfo.MetaTags, "description")...)

	return unique(keywords) // Remove duplicates and return
}

func normalizeText(text string) string {
	// Remove all HTML tags
	re := regexp.MustCompile("<[^>]*>")
	text = re.ReplaceAllString(text, " ")

	// Remove inline JavaScript and CSS remnants
	re = regexp.MustCompile(`(?i)<script.*?\/script>|<style.*?\/style>`)
	text = re.ReplaceAllString(text, " ")

	// Replace punctuation with spaces
	re = regexp.MustCompile(`[.,?!:;'\"(){}<>\-]`)
	text = re.ReplaceAllString(text, " ")

	// Convert to lowercase
	text = strings.ToLower(text)

	// Normalize whitespace
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.ReplaceAll(text, "\t", " ")
	text = strings.Join(strings.Fields(text), " ") // Collapse multiple spaces

	return text
}

/*
func removePunctuation(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsPunct(r) || unicode.IsSymbol(r) {
			return -1
		}
		return r
	}, text)
}
*/
