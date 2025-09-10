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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// FetchRemoteFile fetches a remote file and returns the contents as a string.
func FetchRemoteFile(url string, timeout int, sslmode string) (string, error) {
	ctx := context.Background()
	return FetchRemoteText(ctx, url, FetchOpts{
		Timeout:        time.Duration(timeout) * time.Second,
		ConnectTimeout: 10 * time.Second,
		SSLMode:        sslmode,
		MaxSize:        16 << 20,
		Retries:        2,
		SSRFGuard:      "", // or "on"/"strict"
	})
}

/*
func FetchRemoteFile(url string, timeout int, sslmode string) (string, error) {
	httpClient := &http.Client{
		Transport: SafeTransport(timeout, sslmode),
	}
	resp, err := httpClient.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to fetch file from %s: %v", url, err)
	}
	defer resp.Body.Close() //nolint:errcheck // Don't lint for error not checked, this is a defer statement

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("received non-200 response from %s: %d", url, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %v", err)
	}

	return string(body), nil
}
*/

// APIResponse is a generic API response.
type APIResponse struct {
	StatusCode int    `json:"status_code" yaml:"status_code"`
	Body       string `json:"body" yaml:"body"`
}

// GenericAPIRequest is a generic API request. (it uses passed params to determine the request)
// url = API URL
// method = HTTP method (GET, POST, etc.)
// headers = HTTP headers
// body = HTTP request body
// auth = HTTP request authentication
func GenericAPIRequest(params map[string]string) (string, error) {
	// Prepare the API request
	var response string

	// Check if the request contains a url
	if _, ok := params["url"]; !ok {
		return "", fmt.Errorf("missing URL parameter")
	}
	rawURL := params["url"]

	// Check if the URL is valid
	if !IsURLValid(rawURL) {
		return "", fmt.Errorf("invalid URL: %s", rawURL)
	}

	// Scan params for request authentication and headers
	headers := make(map[string]string)
	for key, value := range params {
		if key == "auth" {
			// Set authentication
			headers["Authorization"] = value
		} else if key == "headers" {
			// Set headers
			headers["headers"] = value
		}
	}

	// Extract the request method
	method, ok := params["method"]
	if !ok {
		method = http.MethodGet
	}

	// Extract the request body
	var body io.Reader
	if params["body"] != "" {
		body = io.NopCloser(strings.NewReader(params["body"]))
	}

	// Create the request
	req, err := http.NewRequest(method, rawURL, body)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %v", err)
	}

	// Add headers
	for key, value := range headers {
		req.Header.Add(key, value)
	}
	// Sensible defaults
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "theCROWler/1.0")
	}

	// Send the request
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			DialContext:         (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
			TLSHandshakeTimeout: 10 * time.Second,
			// TODO: (Optional) Disable HTTP/2 if we observe issues: ForceAttemptHTTP2: false,
		},
		// Prevent redirect → different host, and drop auth when host changes
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("stopped after 5 redirects")
			}
			// Enforce same host as original
			orig := via[0].URL
			if !strings.EqualFold(r.URL.Hostname(), orig.Hostname()) {
				// Strip sensitive headers on cross-host (and block if you prefer)
				r.Header.Del("Authorization")
				return errors.New("redirect to different host blocked")
			}
			return nil
		},
	}

	// --- Optional SSRF guard (OFF by default) ---
	// Turn on by setting params["ssrf_guard"]="on" or "strict".
	// "strict" also blocks 169.254.169.254 explicitly.
	if guard := strings.ToLower(strings.TrimSpace(params["ssrf_guard"])); guard == "on" || guard == "strict" {
		u, _ := url.Parse(rawURL)
		host := u.Hostname()
		ips, err := net.LookupIP(host)
		if err != nil || len(ips) == 0 {
			return "", fmt.Errorf("DNS lookup failed for %s: %v", host, err)
		}
		for _, ip := range ips {
			if isPrivateOrMeta(ip, guard == "strict") {
				return "", fmt.Errorf("destination IP blocked by ssrf_guard: %s (%s)", ip, host)
			}
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // Don't lint for error not checked, this is a defer statement

	// Read the response
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %v", err)
	}

	// prepare the response
	rs := APIResponse{
		StatusCode: resp.StatusCode,
		Body:       string(responseBody),
	}

	// Transform rs into a JSON string
	resByte, err := json.Marshal(rs)
	if err != nil {
		return "", fmt.Errorf("failed to marshal response: %v", err)
	}

	response = string(resByte)
	return response, nil
}

// Blocks loopback/link-local/private ranges; strict also blocks 169.254.169.254 explicitly.
func isPrivateOrMeta(ip net.IP, strict bool) bool {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		private := []net.IPNet{
			{IP: net.IPv4(10, 0, 0, 0), Mask: net.CIDRMask(8, 32)},
			{IP: net.IPv4(172, 16, 0, 0), Mask: net.CIDRMask(12, 32)},
			{IP: net.IPv4(192, 168, 0, 0), Mask: net.CIDRMask(16, 32)},
			{IP: net.IPv4(127, 0, 0, 0), Mask: net.CIDRMask(8, 32)},
			{IP: net.IPv4(169, 254, 0, 0), Mask: net.CIDRMask(16, 32)}, // link-local
		}
		for _, n := range private {
			if n.Contains(v4) {
				return true
			}
		}
		// metadata IP explicit only in strict mode
		if strict && v4.Equal(net.IPv4(169, 254, 169, 254)) {
			return true
		}
	}
	// IPv6 unique-local fc00::/7 and link-local fe80::/10
	private6 := []net.IPNet{
		{IP: net.ParseIP("fc00::"), Mask: net.CIDRMask(7, 128)},
		{IP: net.ParseIP("fe80::"), Mask: net.CIDRMask(10, 128)},
	}
	for _, n := range private6 {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
