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

// Package common package is used to store common functions and variables
package common

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Helped functions to deal with different date formats in JSON

// FlexibleDate is a type that can be used to parse dates in different formats
type FlexibleDate time.Time

var (
	dateFormats = []string{
		"2006-01-02",
		"2006-01-02T15:04:05Z07:00",
	}
)

// UnmarshalJSON parses a date from a JSON string
func (fd *FlexibleDate) UnmarshalJSON(b []byte) error {
	str := string(b)
	// Trim the quotes
	str = str[1 : len(str)-1]
	var parsedTime time.Time
	var err error
	for _, format := range dateFormats {
		parsedTime, err = time.Parse(format, str)
		if err == nil {
			*fd = FlexibleDate(parsedTime)
			return nil
		}
	}
	return fmt.Errorf("could not parse date: %s", str)
}

// MarshalJSON returns a date as a JSON string
func (fd FlexibleDate) MarshalJSON() ([]byte, error) {
	formatted := fmt.Sprintf("\"%s\"", time.Time(fd).Format(dateFormats[0]))
	return []byte(formatted), nil
}

// String returns a date as a string
func (fd FlexibleDate) String() string {
	return time.Time(fd).Format(dateFormats[0])
}

// Time returns a date as a time.Time
func (fd FlexibleDate) Time() time.Time {
	return time.Time(fd)
}

// IsJSON checks if a string is a JSON
func IsJSON(s string) bool {
	var js map[string]interface{}
	return json.Unmarshal([]byte(s), &js) == nil
}

// JSONStrToMap converts a JSON string to a map
func JSONStrToMap(s string) (map[string]interface{}, error) {
	var js map[string]interface{}
	err := json.Unmarshal([]byte(s), &js)
	return js, err
}

// MapToJSONStr converts a map to a JSON string
func MapToJSONStr(m map[string]interface{}) (string, error) {
	js, err := json.Marshal(m)
	return string(js), err
}

// MapStrToJSONStr converts a map[string]string to a JSON string
func MapStrToJSONStr(m map[string]string) (string, error) {
	js, err := json.Marshal(m)
	return string(js), err
}

// SafeEscapeJSONString escapes a string for JSON and for embedding in quoted contexts
func SafeEscapeJSONString(s any) string {
	ss := strings.ReplaceAll(fmt.Sprintf("%v", s), "\\", "\\\\")
	ss = strings.ReplaceAll(ss, "\"", "\\\"")
	ss = strings.ReplaceAll(ss, "'", "\\'")
	return ss
}

// SanitizeJSON tries to fix common JSON issues like unescaped quotes, duplicate commas, trailing commas
func SanitizeJSON(input string) string {
	var out strings.Builder
	inString := false
	escape := false

	for i := 0; i < len(input); i++ {
		c := input[i]

		// Look ahead for forbidden values like "null"
		if !inString && c == '"' {
			// Look ahead for the closing quote
			end := i + 1
			for end < len(input) {
				if input[end] == '"' && input[end-1] != '\\' {
					break
				}
				end++
			}
			if end < len(input) {
				// Extract the string content
				content := input[i+1 : end]
				if content == "null" {
					// Replace the whole string with bare null
					out.WriteString("null")
					i = end // skip closing quote
					continue
				}
			}
		}

		if escape {
			out.WriteByte(c)
			escape = false
			continue
		}

		if c == '\\' {
			escape = true
			out.WriteByte(c)
			continue
		}

		if c == '"' {
			inString = !inString
			out.WriteByte(c)
			continue
		}

		if !inString {
			// --- outside of string ---

			// detect "colon followed by comma" → insert null
			if c == ':' && i+1 < len(input) && input[i+1] == ',' {
				out.WriteByte(':')
				out.WriteString("null")
				continue
			}

			// collapse duplicate commas
			if c == ',' {
				// skip any extra commas
				for i+1 < len(input) && input[i+1] == ',' {
					i++
				}
				// if next char closes an object/array, skip this comma
				if i+1 < len(input) && (input[i+1] == '}' || input[i+1] == ']') {
					continue
				}
				// if next char is another comma, colon, or end of string, skip it
				if i+1 < len(input) && (input[i+1] == ',' || input[i+1] == ':') {
					continue
				}
				out.WriteByte(',')
				continue
			}

			// drop trailing commas before } or ]
			if c == ',' && i+1 < len(input) && (input[i+1] == '}' || input[i+1] == ']') {
				continue
			}

			out.WriteByte(c)
		} else {
			// --- inside string ---

			// repair unescaped quotes like She"s → She\"s
			if c == '"' {
				prev := byte(0)
				next := byte(0)
				if i > 0 {
					prev = input[i-1]
				}
				if i+1 < len(input) {
					next = input[i+1]
				}
				if isAlphaNum(prev) && isAlphaNum(next) {
					out.WriteString("\\\"")
					continue
				}
			}

			out.WriteByte(c)
		}
	}

	candidate := out.String()

	// try to validate it
	var tmp interface{}
	if err := json.Unmarshal([]byte(candidate), &tmp); err == nil {
		// re-encode to strict JSON
		buf := new(bytes.Buffer)
		enc := json.NewEncoder(buf)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(tmp)
		return strings.TrimSpace(buf.String())
	}

	// fallback: return original if still invalid
	return input
}

func isAlphaNum(c byte) bool {
	return (c >= 'A' && c <= 'Z') ||
		(c >= 'a' && c <= 'z') ||
		(c >= '0' && c <= '9')
}
