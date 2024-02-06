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

// Package netinfo provides functionality to extract network information
package netinfo

import (
	"strconv"
	"strings"
	"unicode"
)

// helper function to extract the host from a URL
func urlToHost(url string) string {
	host := url
	if strings.HasPrefix(url, "http://") {
		host = strings.TrimPrefix(url, "http://")
	}
	if strings.HasPrefix(url, "https://") {
		host = strings.TrimPrefix(url, "https://")
	}
	if strings.Contains(host, "/") {
		host = host[:strings.Index(host, "/")]
	}
	host = strings.TrimSuffix(host, "/")
	host = strings.TrimSpace(host)
	return host
}

// helper function to extract the domain from a URL
func urlToDomain(url string) string {
	host := urlToHost(url)
	if strings.Contains(host, ":") {
		host = host[:strings.Index(host, ":")]
	}
	// take only the last two parts of the domain: for example example.com
	parts := strings.Split(host, ".")
	if len(parts) > 2 {
		host = parts[len(parts)-2] + "." + parts[len(parts)-1]
	}
	return host
}

// Helper function to return "N/A" for empty strings
func defaultNA(s string) string {
	if s == "" {
		return "N/A"
	}
	return s
}

// Helper function to check if a string is numeric
func isNumeric(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil
}

// fieldsQuotes splits the string around each instance of one or more consecutive white space characters,
// as defined by unicode.IsSpace, taking into consideration quoted substrings.
func fieldsQuotes(s string) []string {
	var fields []string
	var buf []rune
	inQuotes := false

	for _, r := range s {
		switch {
		case r == '"':
			inQuotes = !inQuotes // Toggle the inQuotes state
			buf = append(buf, r) // Keep quotes as part of the field
		case unicode.IsSpace(r) && !inQuotes:
			if len(buf) > 0 {
				fields = append(fields, string(buf))
				buf = buf[:0] // Reset buffer
			}
		default:
			buf = append(buf, r)
		}
	}

	// Add the last field if it's non-empty
	if len(buf) > 0 {
		fields = append(fields, string(buf))
	}

	return fields
}
