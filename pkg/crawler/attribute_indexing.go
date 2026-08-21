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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

// ExtractedValue retains the flattened value together with its JSON provenance.
// ContextPath is the concrete path through the last wildcard/array selection.
type ExtractedValue struct {
	Value       interface{}
	SourcePath  string
	ContextPath string
	ContextRef  string
}

type traversalNode struct {
	value         interface{}
	path, context string
}

var pathCache sync.Map

type CommandContext struct {
	ObjectID int64
	Data     map[string]interface{}
	// optional later:
	PageURL string
}

type Command func(ctx CommandContext) interface{}

var commands = map[string]Command{
	"now()": func(ctx CommandContext) interface{} {
		return time.Now().UTC().Format(time.RFC3339)
	},
	"object_id": func(ctx CommandContext) interface{} {
		return ctx.ObjectID
	},
	"timestamp()": func(ctx CommandContext) interface{} {
		return time.Now().Unix()
	},
	"json_size": func(ctx CommandContext) interface{} {
		return len(ctx.Data)
	},
}

func ExecuteCommand(a cfg.AttributeDefinition, ctx CommandContext) []interface{} {
	cmdName := a.ParseCommand()

	if cmd, ok := commands[cmdName]; ok {
		return []interface{}{cmd(ctx)}
	}

	return nil
}

// Normalizer is a function type that takes a string as input and returns a normalized version of that string.
type Normalizer func(string) string

// all names lower-case and trimmed for consistency:
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
	"unix_to_datetime":  UnixToDateTime,
}

// UnixToDateTime Transform typical timestamps into date-time format
func UnixToDateTime(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}

	// Try parsing as integer (seconds or milliseconds)
	i, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return s // fallback: keep original
	}

	// Detect milliseconds vs seconds
	// heuristic: anything > year ~ 2286 in seconds is probably ms
	if i > 9999999999 {
		i = i / 1000
	}

	t := time.Unix(i, 0).UTC()

	// Return ISO 8601 (PostgreSQL friendly)
	return t.Format(time.RFC3339)
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
	Key      string
	IsArray  bool
	Index    *int // nil = not index, set = specific index, -1 = wildcard
	Wildcard bool
	Prefix   string
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

		// Case: *
		if p == "*" {
			token.Wildcard = true
			tokens = append(tokens, token)
			continue
		}

		// Case: prefix*
		if strings.HasSuffix(p, "*") {
			token.Prefix = strings.TrimSuffix(p, "*")
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
	extracted := ExtractWithTokensAndContext(data, tokens)
	values := make([]interface{}, len(extracted))
	for i := range extracted {
		values[i] = extracted[i].Value
	}
	return values
}

// ExtractWithTokensAndContext is the canonical path traversal implementation.
func ExtractWithTokensAndContext(data interface{}, tokens []PathToken) []ExtractedValue {
	current := []traversalNode{{value: data}}

	for _, token := range tokens {
		next := []traversalNode{}

		for _, node := range current {
			appendNode := func(v interface{}, component string, selected bool) {
				p := component
				if node.path != "" && component != "" && component[0] != '[' {
					p = node.path + "." + component
				} else {
					p = node.path + component
				}
				ctx := node.context
				if selected {
					ctx = p
				}
				next = append(next, traversalNode{v, p, ctx})
			}
			switch n := node.value.(type) {

			case map[string]interface{}:
				// --- Wildcard: match all keys ---
				if token.Wildcard {
					keys := make([]string, 0, len(n))
					for k := range n {
						keys = append(keys, k)
					}
					sort.Strings(keys)
					for _, k := range keys {
						appendNode(n[k], k, true)
					}
					continue
				}

				// --- Prefix match ---
				if token.Prefix != "" {
					keys := make([]string, 0, len(n))
					for k := range n {
						keys = append(keys, k)
					}
					sort.Strings(keys)
					for _, k := range keys {
						v := n[k]
						if strings.HasPrefix(k, token.Prefix) {
							appendNode(v, k, true)
						}
					}
					continue
				}

				// --- Normal key ---
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
							appendNode(arr[*token.Index], token.Key+"["+strconv.Itoa(*token.Index)+"]", true)
						}
					} else {
						for i, v := range arr {
							appendNode(v, token.Key+"["+strconv.Itoa(i)+"]", true)
						}
					}
					continue
				}
				appendNode(val, token.Key, false)

			case []interface{}:

				if token.Key == "" && token.IsArray {
					for i, v := range n {
						appendNode(v, "["+strconv.Itoa(i)+"]", true)
					}
					continue
				}

				for i, item := range n {

					// Handle direct index access like [2]
					if token.Key == "" && token.Index != nil {
						if *token.Index < len(n) {
							appendNode(n[*token.Index], "["+strconv.Itoa(*token.Index)+"]", true)
						}
						continue
					}

					// If no key (edge case), just propagate values
					if token.Key == "" {
						appendNode(item, "["+strconv.Itoa(i)+"]", true)
						continue
					}

					if m, ok := item.(map[string]interface{}); ok {
						val, exists := m[token.Key]
						if !exists {
							continue
						}

						if token.IsArray {
							if arr, ok := val.([]interface{}); ok {
								for j, v := range arr {
									appendNode(v, token.Key+"["+strconv.Itoa(j)+"]", true)
								}
							}
						} else {
							appendNode(val, token.Key, false)
						}
						continue
					}

					// handle primitive array elements
					if token.Key == "" && !token.IsArray {
						appendNode(item, "["+strconv.Itoa(i)+"]", true)
					}
				}
			}
		}

		current = next
	}

	result := make([]ExtractedValue, 0, len(current))
	for _, n := range current {
		if arr, ok := n.value.([]interface{}); ok {
			for i, v := range arr {
				p := n.path + "[" + strconv.Itoa(i) + "]"
				result = append(result, makeExtracted(v, p, p))
			}
			continue
		}
		result = append(result, makeExtracted(n.value, n.path, n.context))
	}
	return result
}

func makeExtracted(value interface{}, source, context string) ExtractedValue {
	ref := ""
	if context != "" {
		sum := sha256.Sum256([]byte(context))
		ref = hex.EncodeToString(sum[:])
	}
	return ExtractedValue{Value: value, SourcePath: source, ContextPath: context, ContextRef: ref}
}

func ExtractValuesWithContext(data interface{}, path string) []ExtractedValue {
	return ExtractWithTokensAndContext(data, GetParsedPath(path))
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
		rule = strings.ToLower(strings.TrimSpace(rule))
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
