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

// Package common provides common utilities and functions used across the application.
package common

import "strings"

// NormalizeURL normalizes a URL by trimming trailing slashes and converting it to lowercase.
func NormalizeURL(url string) string {
	// Trim spaces
	url = strings.TrimSpace(url)
	// Trim trailing slash
	url = strings.TrimRight(url, "/")
	// Convert to lowercase
	url = strings.ToLower(url)
	return url
}

// IsURLValid checks if a URL is valid.
func IsURLValid(url string) bool {
	// Check if the URL is empty
	if url == "" {
		return false
	}
	tURL := strings.ToLower(strings.TrimSpace(url))

	// Check if the URL starts with http:// or https://
	if !strings.HasPrefix(tURL, "http://") &&
		!strings.HasPrefix(tURL, "https://") &&
		!strings.HasPrefix(tURL, "ws://") &&
		!strings.HasPrefix(tURL, "wss://") &&
		!strings.HasPrefix(tURL, "ftp://") &&
		!strings.HasPrefix(tURL, "ftps://") {
		return false
	}

	// Check if the URL has a valid domain
	if strings.Contains(tURL, " ") || strings.Contains(tURL, "\n") || strings.Contains(tURL, "\t") {
		return false
	}

	// Check if the URL has a valid TLD
	if !strings.Contains(tURL, ".") {
		return false
	}

	// Looks like a valid URL
	return true
}
