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
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"net/url"

	"golang.org/x/net/publicsuffix"
)

// helper function to extract the host from a URL
func urlToHost(url string) string {
	host := url
	if strings.Contains(host, "://") {
		host = host[strings.Index(host, "://")+3:]
	}
	if strings.Contains(host, "/") {
		host = host[:strings.Index(host, "/")]
	}
	host = strings.TrimSuffix(host, "/")
	host = strings.TrimSpace(host)
	return host
}

// helper function to extract the domain from a URL
func urlToDomain(inputURL string) string {
	_, err := url.Parse(inputURL)
	if err != nil {
		return ""
	}

	// Given that url.Parse() does always extract a hostname correctly
	// we can safely ignore the error here
	h := urlToHost(inputURL)

	// Use EffectiveTLDPlusOne to correctly handle domains like "example.co.uk"
	domain, err := publicsuffix.EffectiveTLDPlusOne(h)
	if err != nil {
		fmt.Printf("Error extracting domain from URL: %v\n", err)
		return ""
	}
	return domain
}

// Helper function to return "N/A" for empty strings
func defaultNA(s string) string {
	if s == "" {
		return naStr
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

	var o rune
	for _, r := range s {
		switch {
		case r == '"' && o != '\\':
			inQuotes = !inQuotes // Toggle the inQuotes state
		case unicode.IsSpace(r) && !inQuotes:
			if len(buf) > 0 {
				fields = append(fields, string(buf))
				buf = buf[:0] // Reset buffer
			}
		default:
			buf = append(buf, r)
		}
		o = r
	}

	// Add the last field if it's non-empty
	if len(buf) > 0 {
		fields = append(fields, string(buf))
		return fields
	}

	return []string{}
}
