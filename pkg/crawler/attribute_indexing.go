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
	"net/url"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

var pathCache sync.Map

// Normalizer is a function type that takes a string as input and returns a normalized version of that string.
type Normalizer func(string) string

var normalizers = map[string]Normalizer{
	"lowercase": strings.ToLower,
	"uppercase": strings.ToUpper,
	"trim":      strings.TrimSpace,
	"collapse_spaces": func(s string) string {
		return strings.Join(strings.Fields(s), " ")
	},
	"remove_punctuation": func(s string) string {
		var b strings.Builder
		for _, r := range s {
			if unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsSpace(r) {
				b.WriteRune(r)
			}
		}
		return b.String()
	},
	"normalize_url": func(s string) string {
		u, err := url.Parse(s)
		if err != nil {
			return s
		}
		u.Scheme = strings.ToLower(u.Scheme)
		u.Host = strings.ToLower(u.Host)
		u.Fragment = ""
		return u.String()
	},
	"normalize_email": func(s string) string {
		return strings.ToLower(strings.TrimSpace(s))
	},
	"fix_utf8":          FixUTF8,
	"normalize_unicode": NormalizeUnicode,
	"sanitize_string":   SanitizeString,
}

// FixUTF8 takes a string as input and returns a version of the string that is valid UTF-8, with control characters removed and NULL bytes stripped out, making it safe for storage in databases like PostgreSQL.
func FixUTF8(s string) string {
	// Step 1: ensure valid UTF-8
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}

	// Step 2: remove NULL bytes (critical for PostgreSQL)
	s = strings.ReplaceAll(s, "\x00", "")

	// Step 3: remove control characters (except newline/tab if you want)
	var b strings.Builder
	for _, r := range s {
		if r == '\n' || r == '\t' {
			b.WriteRune(r)
			continue
		}
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}

	return b.String()
}

// NormalizeUnicode takes a string as input and returns a normalized version of that string using Unicode Normalization Form C (NFC), which composes characters into their canonical form, ensuring that visually identical characters are represented in a consistent way.
func NormalizeUnicode(s string) string {
	return norm.NFC.String(s)
}

// SanitizeString takes a string as input and returns a sanitized version of that string by removing invalid UTF-8 sequences and control characters, ensuring that the resulting string is safe for storage and processing.
func SanitizeString(s string) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}

	var b strings.Builder

	for _, r := range s {
		switch {
		case r == '\x00':
			continue
		case unicode.IsControl(r) && r != '\n' && r != '\t':
			continue
		case unicode.IsGraphic(r) || unicode.IsSpace(r):
			b.WriteRune(r)
		}
	}

	return b.String()
}

// PathToken represents a single token in a JSON path, which can be either a key or an array indicator.
type PathToken struct {
	Key     string
	IsArray bool
	Index   *int // nil = not index, set = specific index, -1 = wildcard
}

// GetParsedPath retrieves the parsed path tokens from the cache if available, otherwise it parses the path and stores it in the cache for future use.
func GetParsedPath(path string) []PathToken {
	if v, ok := pathCache.Load(path); ok {
		return v.([]PathToken)
	}

	tokens := ParsePath(path)

	actual, _ := pathCache.LoadOrStore(path, tokens)
	return actual.([]PathToken)
}

// ParsePath takes a JSON path string (e.g., "details.scraped_data.url") and parses it into a slice of PathTokens.
func ParsePath(path string) []PathToken {
	parts := strings.Split(path, ".")
	tokens := make([]PathToken, 0, len(parts))

	for _, p := range parts {
		p = strings.TrimSpace(p)

		token := PathToken{}

		// Case: [*]
		if p == "[*]" {
			token.IsArray = true
			tokens = append(tokens, token)
			continue
		}

		// Case: [2]
		if strings.HasPrefix(p, "[") && strings.HasSuffix(p, "]") {
			idxStr := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(p, "["), "]"))
			if i, err := strconv.Atoi(idxStr); err == nil {
				token.Index = &i
				token.IsArray = true
			}
			tokens = append(tokens, token)
			continue
		}

		// Case: key[*]
		if strings.HasSuffix(p, "[*]") {
			token.Key = strings.TrimSpace(strings.TrimSuffix(p, "[*]"))
			token.IsArray = true
			tokens = append(tokens, token)
			continue
		}

		// Case: key[2]
		if i := strings.Index(p, "["); i != -1 {
			key := p[:i]
			idxStr := p[i+1 : len(p)-1]

			if idx, err := strconv.Atoi(idxStr); err == nil {
				token.Key = key
				token.Index = &idx
				token.IsArray = true
			}
			tokens = append(tokens, token)
			continue
		}

		// Default
		token.Key = p
		tokens = append(tokens, token)
	}

	return tokens
}

func (t PathToken) String() string {
	if t.IsArray {
		return t.Key + "[*]"
	}
	return t.Key
}

// IsEmpty checks if the PathToken is empty (i.e., has an empty key).
func (t PathToken) IsEmpty() bool {
	return (t.Key == "") && !t.IsArray
}

// IsValid checks if the PathToken is valid (i.e., not empty and does not contain invalid characters).
func (t PathToken) IsValid() bool {
	if t.IsEmpty() {
		return false
	}
	if strings.Contains(t.Key, ".") || strings.Contains(t.Key, "[") || strings.Contains(t.Key, "]") {
		return false
	}
	return true
}

// Normalize trims whitespace from the key of the PathToken and returns a new PathToken with the normalized key.
func (t PathToken) Normalize() PathToken {
	t.Key = strings.TrimSpace(t.Key)
	return t
}

// ExtractWithTokens is a helper function that takes a data structure and a JSON path string, parses the path into tokens, and then extracts the corresponding values from the data structure using the tokens.
func ExtractWithTokens(data interface{}, tokens []PathToken) []interface{} {
	current := []interface{}{data}

	for _, token := range tokens {
		next := []interface{}{}

		for _, node := range current {
			switch n := node.(type) {

			case map[string]interface{}:
				val, ok := n[token.Key]
				if !ok {
					continue
				}

				if token.IsArray {
					arr, ok := val.([]interface{})
					if !ok {
						continue
					}

					if token.Index != nil {
						if *token.Index < len(arr) {
							next = append(next, arr[*token.Index])
						}
					} else {
						next = append(next, arr...)
					}
					continue
				}
				next = append(next, val)

			case []interface{}:

				if token.Key == "" && token.IsArray {
					if arr, ok := node.([]interface{}); ok {
						next = append(next, arr...)
					}
					continue
				}

				for _, item := range n {

					// Handle direct index access like [2]
					if token.Key == "" && token.Index != nil {
						if *token.Index < len(n) {
							next = append(next, n[*token.Index])
						}
						continue
					}

					// If no key (edge case), just propagate values
					if token.Key == "" {
						next = append(next, item)
						continue
					}

					if m, ok := item.(map[string]interface{}); ok {
						val, exists := m[token.Key]
						if !exists {
							continue
						}

						if token.IsArray {
							if arr, ok := val.([]interface{}); ok {
								next = append(next, arr...)
							}
						} else {
							next = append(next, val)
						}
						continue
					}

					// handle primitive array elements
					if token.Key == "" && !token.IsArray {
						next = append(next, item)
					}
				}
			}
		}

		current = next
	}

	return flatten(current)
}

// ExtractValues is a helper function that takes a data structure and a JSON path string, parses the path into tokens, and then extracts the corresponding values from the data structure using the tokens.
func ExtractValues(data interface{}, path string) []interface{} {
	tokens := GetParsedPath(path)
	return ExtractWithTokens(data, tokens)
}

func flatten(values []interface{}) []interface{} {
	var result []interface{}

	for _, v := range values {
		switch val := v.(type) {
		case []interface{}:
			result = append(result, flatten(val)...)
		default:
			result = append(result, val)
		}
	}

	return result
}

// ToString converts various types of values to their string representation.
func ToString(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(val), 'f', -1, 32)
	case int:
		return strconv.Itoa(val)
	case int64:
		return strconv.FormatInt(val, 10)
	case bool:
		return strconv.FormatBool(val)
	case json.Number:
		return val.String()
	case nil:
		return ""
	default:
		b, _ := json.Marshal(val)
		return string(b)
	}
}

// ApplyNormalizers takes a string value and a list of normalizer names, applies the corresponding normalizer functions to the value in the order they are specified, and returns the normalized string.
func ApplyNormalizers(value string, rules []string) string {
	result := value

	rules = EnsureSafeNormalizers(rules)

	for _, rule := range rules {
		if norm, ok := normalizers[rule]; ok {
			result = norm(result)
		}
	}

	return result
}

// EnsureSafeNormalizers takes a list of normalizer names and ensures that the "fix_utf8" normalizer is included in the list. If "fix_utf8" is not already present, it is added to the beginning of the list to ensure that all strings are properly sanitized for UTF-8 encoding before any other normalizations are applied.
func EnsureSafeNormalizers(rules []string) []string {
	for _, r := range rules {
		if r == "fix_utf8" {
			return rules
		}
	}
	return append([]string{"fix_utf8"}, rules...)
}
