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
