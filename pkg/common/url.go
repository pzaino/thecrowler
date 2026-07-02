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

import (
	"net/url"
	"strings"
)

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
func IsURLValid(rawURL string) bool {
	if rawURL == "" {
		return false
	}

	trimmedURL := strings.TrimSpace(rawURL)
	lowerURL := strings.ToLower(trimmedURL)
	if strings.ContainsAny(lowerURL, " \n\t") {
		return false
	}

	for _, scheme := range []string{"http", "https", "ws", "wss", "ftp", "ftps"} {
		if strings.HasPrefix(lowerURL, scheme+"://") {
			// Preserve the existing domain/TLD requirement for network URLs.
			return strings.Contains(lowerURL, ".")
		}
	}

	parsedURL, err := url.Parse(trimmedURL)
	if err != nil {
		return false
	}

	scheme := strings.ToLower(parsedURL.Scheme)
	switch scheme {
	case "email", "imap", "imaps", "pop3", "pop3s", "gmail", "graph-mail":
		return strings.HasPrefix(lowerURL, scheme+"://") && parsedURL.Host != ""
	case "maildir", "mbox":
		return strings.HasPrefix(lowerURL, scheme+"://") &&
			parsedURL.Host == "" && strings.HasPrefix(parsedURL.Path, "/") && parsedURL.Path != "/"
	default:
		return false
	}
}
