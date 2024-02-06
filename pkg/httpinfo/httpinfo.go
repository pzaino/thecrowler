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

// Package httpinfo provides functionality to extract HTTP header information
package httpinfo

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
)

// CreateConfig creates a default HTTPInfoConfig
func CreateConfig(url string, c cfg.Config) HTTPInfoConfig {
	sel := c.Selenium[0]
	usrAgent := cmn.UsrAgentStrMap[sel.Type+"-desktop01"]
	return HTTPInfoConfig{
		URL:             url,
		CustomHeader:    map[string]string{"User-Agent": usrAgent},
		FollowRedirects: true,
	}
}

// ExtractHTTPInfo extracts HTTP header information based on the provided configuration
func ExtractHTTPInfo(config HTTPInfoConfig) (*HTTPInfoResponse, error) {
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if !config.FollowRedirects {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}

	req, err := http.NewRequest("GET", config.URL, nil)
	if err != nil {
		return nil, err
	}

	// Add custom headers if specified
	for key, value := range config.CustomHeader {
		req.Header.Add(key, value)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Check if the response is a redirect (3xx)
	if config.FollowRedirects && (resp.StatusCode >= 300 && resp.StatusCode < 400) {
		fmt.Println("Redirect detected, handle it as needed.")
	}

	// Collect response headers
	responseHeaders := resp.Header

	// Create a new HTTPInfoResponse object
	info := new(HTTPInfoResponse)

	info.ResponseHeaders = responseHeaders

	// Extract response headers
	info.URL = config.URL
	info.CustomHeaders = config.CustomHeader
	info.FollowRedirects = config.FollowRedirects
	info.ContentLength = responseHeaders.Get("Content-Length")
	info.ContentEncoding = responseHeaders.Get("Content-Encoding")
	info.TransferEncoding = responseHeaders.Get("Transfer-Encoding")
	info.ServerType = responseHeaders.Get("Server")
	info.PoweredBy = responseHeaders.Get("X-Powered-By")
	info.AspNetVersion = responseHeaders.Get("X-AspNet-Version")
	info.FrameOptions = responseHeaders.Get("X-Frame-Options")
	info.XSSProtection = responseHeaders.Get("X-XSS-Protection")
	info.HSTS = responseHeaders.Get("Strict-Transport-Security")
	info.ContentType = responseHeaders.Get("Content-Type")
	info.ContentTypeOptions = responseHeaders.Get("X-Content-Type-Options")
	info.ContentSecurityPolicy = responseHeaders.Get("Content-Security-Policy")
	info.StrictTransportSecurity = responseHeaders.Get("Strict-Transport-Security")
	info.AccessControlAllowOrigin = responseHeaders.Get("Access-Control-Allow-Origin")
	info.AccessControlAllowMethods = responseHeaders.Get("Access-Control-Allow-Methods")
	info.AccessControlAllowHeaders = responseHeaders.Get("Access-Control-Allow-Headers")
	info.AccessControlAllowCredentials = responseHeaders.Get("Access-Control-Allow-Credentials")
	info.AccessControlExposeHeaders = responseHeaders.Get("Access-Control-Expose-Headers")
	info.SetCookie = responseHeaders.Get("Set-Cookie")
	info.WwwAuthenticate = responseHeaders.Get("WWW-Authenticate")
	info.ProxyAuthenticate = responseHeaders.Get("Proxy-Authenticate")
	info.KeepAlive = responseHeaders.Get("Keep-Alive")
	info.Expires = responseHeaders.Get("Expires")
	info.LastModified = responseHeaders.Get("Last-Modified")
	info.ETag = responseHeaders.Get("ETag")
	info.ContentDisposition = responseHeaders.Get("Content-Disposition")

	// Analyze response body for additional information
	responseBody, err := analyzeResponseBody(resp)
	if err != nil {
		return nil, err
	}
	info.ResponseBodyInfo = responseBody

	return info, nil
}

// AnalyzeResponseBody analyzes the response body for additional server-related information
func analyzeResponseBody(resp *http.Response) ([]string, error) {
	// Read the response body
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// Convert the response body to a string
	responseBody := string(bodyBytes)

	// Initialize a slice to store the collected information
	var infoList []string

	// Example: Look for multiple patterns or keywords in the response body
	if strings.Contains(responseBody, "Powered by Laravel") {
		infoList = append(infoList, "Laravel Framework")
	}

	if strings.Contains(responseBody, "JavaScript Framework: React") {
		infoList = append(infoList, "React JavaScript Framework")
	}

	// Extract custom element information from a custom header, e.g., "X-Custom-Header"
	if custom := resp.Header.Get("X-Custom-Header"); custom != "" {
		infoList = append(infoList, "Custom: "+custom)
	}

	// Add more data extraction logic to search for and append additional HTTP, server and frameworks information

	// If no additional information is found, add a default message
	if len(infoList) == 0 {
		infoList = append(infoList, "No additional information found")
	}

	if len(infoList) == 0 {
		infoList = append(infoList, "No additional information found")
	}

	return infoList, nil
}

/* Not ready yet:
// checkCDN checks the response headers for common CDN-related headers
func checkCDN(headers http.Header) {
	cdnHeaders := map[string]string{
		"Via":             "Might indicate a CDN or proxy",
		"X-Cache":         "Common in CDN responses",
		"X-Cache-Hit":     "Indicates cache status in some CDNs",
		"CF-Cache-Status": "Specific to Cloudflare",
		// Add more CDN-specific headers as needed
	}

	for header, desc := range cdnHeaders {
		if value := headers.Get(header); value != "" {
			fmt.Printf("%v: %v (%s)\n", header, value, desc)
		}
	}
}
*/
