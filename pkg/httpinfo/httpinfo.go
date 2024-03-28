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
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	"golang.org/x/net/publicsuffix"
)

// CreateConfig creates a default Config
func CreateConfig(url string, c cfg.Config) Config {
	sel := c.Selenium[0]
	usrAgent := cmn.UsrAgentStrMap[sel.Type+"-desktop01"]
	return Config{
		URL:             url,
		CustomHeader:    map[string]string{"User-Agent": usrAgent},
		FollowRedirects: true,
		Timeout:         60,
		SSLMode:         "none",
	}
}

// Check if the URL is valid and allowed
func validateURL(inputURL string) (bool, error) {
	parsedURL, err := url.Parse(inputURL)
	if err != nil {
		return false, err
	}

	// Ensure the scheme is http or https
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return false, fmt.Errorf("invalid URL scheme: %s", parsedURL.Scheme)
	}

	// Add more checks as needed, e.g., against a domain whitelist
	return true, nil
}

// ExtractHTTPInfo extracts HTTP header information based on the provided configuration
func ExtractHTTPInfo(config Config) (*HTTPDetails, error) {
	// Validate the URL
	if ok, err := validateURL(config.URL); !ok {
		return nil, err
	}
	// Validate IP address
	if cmn.IsDisallowedIP(config.URL, 0) {
		return nil, fmt.Errorf("IP address not allowed: %s", config.URL)
	}
	// Ok, the URL is safe, let's create a new HTTP request config.SSLMode
	transport := cmn.SafeTransport(config.Timeout, "ignore")
	transport.TLSClientConfig = &tls.Config{
		InsecureSkipVerify: true, // Skip TLS certificate verification
		MinVersion:         tls.VersionTLS10,
		MaxVersion:         tls.VersionTLS13,
	}
	sn := urlToDomain(config.URL)
	transport.TLSClientConfig.ServerName = sn
	httpClient := &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if !config.FollowRedirects {
				return http.ErrUseLastResponse
			}

			// Update ServerName for SNI in case of domain change due to redirect
			lastURL, err := url.Parse(req.URL.String())
			if err != nil {
				return fmt.Errorf("error parsing redirect URL: %v", err)
			}
			lastDomain := lastURL.Hostname()
			req.URL.Scheme = "https"
			transport.TLSClientConfig.ServerName = lastDomain

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

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Check if the response is a redirect (3xx)
	if config.FollowRedirects && (resp.StatusCode >= 300 && resp.StatusCode < 400) {
		// Handle the redirect as needed
		cmn.DebugMsg(cmn.DbgLvlDebug1, "Redirect detected, handle it as needed.")

		// Extract the new location from the response header
		newLocation := resp.Header.Get("Location")
		cmn.DebugMsg(cmn.DbgLvlDebug2, "Redirect location: %s", newLocation)

		// Create a new configuration with the new location
		newConfig := config
		newConfig.URL = newLocation
		newConfig.CustomHeader = map[string]string{"User-Agent": cmn.UsrAgentStrMap["desktop01"]}
		newConfig.FollowRedirects = true

		// Recursively call ExtractHTTPInfo with the new configuration to extract the new HTTP information
		return ExtractHTTPInfo(newConfig)
	}

	// Create a new HTTPDetails object
	info := new(HTTPDetails)

	// Collect response headers
	info.ResponseHeaders = resp.Header

	// Extract response headers
	info.URL = config.URL
	info.CustomHeaders = config.CustomHeader
	info.FollowRedirects = config.FollowRedirects

	// Analyze response body for additional information
	detectedItems, err := analyzeResponse(resp, info)
	if err != nil {
		return nil, err
	}
	info.DetectedAssets = make(map[string]string)
	for k, v := range detectedItems {
		info.DetectedAssets[k] = v
	}

	return info, nil
}

// AnalyzeResponse analyzes the response body and header for additional server-related information
// and possible technologies used
// Note: In the future this needs to be moved in http_rules logic
func analyzeResponse(resp *http.Response, info *HTTPDetails) (map[string]string, error) {
	// Get the response headers
	header := &(*info).ResponseHeaders

	// Read the response body
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// Convert the response body to a string
	responseBody := string(bodyBytes)

	// Initialize the infoList map
	// infoList := make(map[string]string)
	var infoList map[string]string = make(map[string]string)

	// Detect CMS
	x := detectCMS(responseBody, header)
	for k, v := range *x {
		infoList[k] = v
	}

	// Look for multiple patterns or keywords in the response body
	if strings.Contains(strings.ToLower(responseBody), "powered by laravel") {
		infoList["laravel_framework"] = "yes"
	}

	if strings.Contains(strings.ToLower(responseBody), "javaScript framework: react") {
		infoList["react_framework"] = "yes"
	}

	// If no additional information is found, add a default message
	if len(infoList) == 0 {
		infoList["analysis_result"] = "No additional information found"
	} else {
		infoList["analysis_result"] = "Additional information found"
	}

	return infoList, nil
}

func detectCMS(responseBody string, header *http.Header) *map[string]string {
	cmsNames := map[string]string{
		"wordpress":   "WordPress",
		"joomla":      "Joomla",
		"drupal":      "Drupal",
		"magento":     "Magento",
		"prestashop":  "PrestaShop",
		"shopify":     "Shopify",
		"typo3":       "TYPO3",
		"bitrix":      "1C-Bitrix",
		"modx":        "MODX",
		"opencart":    "OpenCart",
		"vbulletin":   "vBulletin",
		"whmcs":       "WHMCS",
		"mediawiki":   "MediaWiki",
		"django":      "Django",
		"laravel":     "Laravel",
		"codeigniter": "CodeIgniter",
		"symfony":     "Symfony",
		"express":     "Express",
		"angular":     "Angular",
		"react":       "React",
		"vue":         "Vue",
		"ember":       "Ember",
		"backbone":    "Backbone",
		"meteor":      "Meteor",
		"rails":       "Ruby on Rails",
		"phalcon":     "Phalcon",
		"yii":         "Yii",
		"flask":       "Flask",
		"spring":      "Spring",
		"struts":      "Apache Struts",
		"play":        "Play Framework",
		"koa":         "Koa",
		"sails":       "Sails.js",
		"nuxt":        "Nuxt.js",
		"next":        "Next.js",
		"gatsby":      "Gatsby",
	}

	const (
		xgenerator = "X-Generator"
	)

	// Initialize a slice to store the detected CMS
	var detectedCMS map[string]string = make(map[string]string)

	responseBody = strings.ToLower(strings.TrimSpace(responseBody))
	detectCMSByHostHeader(header, cmsNames, &detectedCMS)
	detectCMSByXPoweredByHeader(header, cmsNames, &detectedCMS)
	detectCMSByLinkHeader(header, &detectedCMS)

	// SOme extra tags that may help:
	if header.Get(xgenerator) != "" {
		detectedCMS[xgenerator] = header.Get(xgenerator)
	}
	if header.Get("X-Nextjs-Cache") != "" {
		detectedCMS["Next.js"] = "yes"
	}
	if header.Get("X-Drupal-Cache") != "" || header.Get("X-Drupal-Dynamic-Cache") != "" {
		detectedCMS["Drupal"] = "yes"
	}

	if len(detectedCMS) == 0 {
		// nothing found, so let's try the "heavy weapons": response body
		detectCMSByKeyword(responseBody, cmsNames, &detectedCMS)
	}
	return &detectedCMS
}

func detectCMSByKeyword(responseBody string, cmsNames map[string]string, detectedCMS *map[string]string) {
	for cms := range cmsNames {
		if strings.Contains(responseBody, "powered by "+cms) {
			(*detectedCMS)[cmsNames[cms]] = "yes"
		}
	}
}

func detectCMSByHostHeader(header *http.Header, cmsNames map[string]string, detectedCMS *map[string]string) {
	hh := (*header)["Host-Header"]
	if len(hh) != 0 {
		for _, tag := range hh {
			tag = strings.ToLower(tag)
			for cms := range cmsNames {
				if strings.Contains(tag, cms) {
					(*detectedCMS)[cmsNames[cms]] = "yes"
				}
			}
		}
	}
}

func detectCMSByXPoweredByHeader(header *http.Header, cmsNames map[string]string, detectedCMS *map[string]string) {
	xpb := (*header)["X-Powered-By"]
	if len(xpb) != 0 {
		for _, tag := range xpb {
			tag = strings.ToLower(tag)
			for cms := range cmsNames {
				if strings.Contains(tag, cms) {
					(*detectedCMS)[cmsNames[cms]] = "yes"
				}
			}
		}
	}
}

func detectCMSByLinkHeader(header *http.Header, detectedCMS *map[string]string) {
	lh := (*header)["Link"]
	if len(lh) != 0 {
		for _, tag := range lh {
			tag = strings.ToLower(tag)
			cms := detectCMSMicroSignatures(tag)
			if cms != "unknown" {
				(*detectedCMS)[cms] += "yes"
			}
		}
	}
}

func detectCMSMicroSignatures(text string) string {
	for cms, patterns := range CMSPatterns {
		allMatch := true // Assume all patterns match initially
		for _, pattern := range patterns {
			if !pattern.MatchString(text) {
				allMatch = false // If any pattern does not match, set to false and break
				break
			}
		}
		if allMatch {
			return cms // Return the CMS name if all patterns matched
		}
	}
	return "unknown" // Return "unknown" if no CMS matches all its patterns
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
