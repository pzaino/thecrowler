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
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"

	"github.com/PuerkitoBio/goquery"
	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	ruleset "github.com/pzaino/thecrowler/pkg/ruleset"
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
func ExtractHTTPInfo(config Config, re *ruleset.RuleEngine) (*HTTPDetails, error) {
	cmn.DebugMsg(cmn.DbgLvlDebug3, "Extracting HTTP information for URL: %s", config.URL)

	// Validate the URL
	if ok, err := validateURL(config.URL); !ok {
		return nil, err
	}

	// Validate IP address
	if err := validateIPAddress(config.URL); err != nil {
		return nil, err
	}

	// Retrieve SSL Info (if it's HTTPS)
	cmn.DebugMsg(cmn.DbgLvlDebug1, "Collecting SSL/TLS information for URL: %s", config.URL)
	sslInfo, err := getSSLInfo(config.URL)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug1, "Error retrieving SSL information: %v", err)
	}

	// Create a new HTTP client
	httpClient := createHTTPClient(config)

	// Send HTTP request
	cmn.DebugMsg(cmn.DbgLvlDebug1, "Collecting HTTP Header information for URL: %s", config.URL)
	resp, err := sendHTTPRequest(httpClient, config)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Handle redirects
	if shouldFollowRedirects(config, resp) {
		return handleRedirects(config, re, resp)
	}

	// Create a new HTTPDetails object
	info := new(HTTPDetails)

	// Collect response headers
	info.ResponseHeaders = resp.Header

	// Extract response headers
	info.URL = config.URL
	info.CustomHeaders = config.CustomHeader
	info.FollowRedirects = config.FollowRedirects
	info.SSLInfo, err = ConvertSSLInfoToDetails(*sslInfo)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug1, "Error converting SSL info to details: %v", err)
	}

	// Analyze response body for additional information
	detectedItems, err := analyzeResponse(resp, info, sslInfo, re)
	if err != nil {
		return nil, err
	}
	info.DetectedAssets = make(map[string]string)
	for k, v := range detectedItems {
		info.DetectedAssets[k] = v
	}

	return info, nil
}

func validateIPAddress(url string) error {
	host := urlToHost(url)
	ips := cmn.HostToIP(host)
	for _, ip := range ips {
		if cmn.IsDisallowedIP(ip, 0) {
			return fmt.Errorf("IP address not allowed: %s", url)
		}
	}
	return nil
}

func getSSLInfo(url string) (*SSLInfo, error) {
	sslInfo := NewSSLInfo()
	if strings.HasPrefix(url, "https") {
		err := sslInfo.GetSSLInfo(url, "443")
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlDebug1, "Error retrieving SSL information: %v", err)
		}
		err = sslInfo.ValidateCertificate()
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlDebug1, "Error validating SSL certificate: %v", err)
		}
	}
	return sslInfo, nil
}

func createHTTPClient(config Config) *http.Client {
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
			return handleRedirect(req, via, config, transport)
		},
	}
	return httpClient
}

func sendHTTPRequest(httpClient *http.Client, config Config) (*http.Response, error) {
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
	return resp, nil
}

func shouldFollowRedirects(config Config, resp *http.Response) bool {
	return config.FollowRedirects && (resp.StatusCode >= 300 && resp.StatusCode < 400)
}

func handleRedirects(config Config, re *ruleset.RuleEngine, resp *http.Response) (*HTTPDetails, error) {
	newLocation := resp.Header.Get("Location")
	cmn.DebugMsg(cmn.DbgLvlDebug2, "Redirect location: %s", newLocation)

	newConfig := config
	newConfig.URL = newLocation
	newConfig.CustomHeader = map[string]string{"User-Agent": cmn.UsrAgentStrMap["desktop01"]}
	newConfig.FollowRedirects = true

	return ExtractHTTPInfo(newConfig, re)
}

func handleRedirect(req *http.Request, via []*http.Request, config Config, transport *http.Transport) error {
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
}

// AnalyzeResponse analyzes the response body and header for additional server-related information
// and possible technologies used
// Note: In the future this needs to be moved in http_rules logic
func analyzeResponse(resp *http.Response, info *HTTPDetails, sslInfo *SSLInfo, re *ruleset.RuleEngine) (map[string]string, error) {
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
	x := detectTechnologies(info.URL, responseBody, header, sslInfo, re)
	for k, v := range *x {
		infoList[k] = v
	}

	// If no additional information is found, add a default message
	if len(infoList) == 0 {
		infoList["analysis_result"] = "No additional information found"
	} else {
		infoList["analysis_result"] = "Additional information found"
	}

	return infoList, nil
}

func detectTechnologies(url string, responseBody string, header *http.Header, sslInfo *SSLInfo, re *ruleset.RuleEngine) *map[string]string {
	const (
		hostHeader = "Host-Header"
		xGenerator = "X-Generator"
	)

	// CMS micro-signatures
	Patterns := re.GetAllEnabledDetectionRules()

	// Initialize a slice to store the detected CMS
	var detectedTech map[string]float32 = make(map[string]float32)

	// Normalize the response body
	responseBody = strings.ToLower(strings.TrimSpace(responseBody))

	// Iterate through all the header tags and check for CMS signatures
	for headerTag := range *header {
		// Get the HTTP header fields for the specific tag
		var Signatures map[string]map[string]ruleset.HTTPHeaderField
		if headerTag == hostHeader {
			Signatures = ruleset.GetAllHTTPHeaderFieldsMap(&Patterns)
		} else {
			Signatures = ruleset.GetHTTPHeaderFieldsMapByKey(&Patterns, headerTag)
		}
		if (Signatures != nil) && len(Signatures) > 0 {
			detectTechByTag(header, headerTag, &Signatures, &detectedTech)
		}
	}

	// Try to detect technologies using URL's micro-signatures (e.g., /wp-content/)
	URLSignatures := ruleset.GetAllURLMicroSignaturesMap(&Patterns)
	detectTechByURL(url, &URLSignatures, &detectedTech)
	URLSignatures = nil

	// Try to detect technologies using meta tags
	MetaTagsSignatures := ruleset.GetAllMetaTagsMap(&Patterns)
	detectTechByMetaTags(responseBody, &MetaTagsSignatures, &detectedTech)
	MetaTagsSignatures = nil

	// Some extra tags that may help:
	if header.Get(xGenerator) != "" {
		detectedTech[xGenerator] += 1
	}

	// Check the response body for Technologies signatures
	Signatures := ruleset.GetAllPageContentPatternsMap(&Patterns)
	detectTechnologiesByKeyword(responseBody, &Signatures, &detectedTech)
	Signatures = nil

	// Check for SSL/TLS technologies
	if sslInfo != nil {
		sslSignatures := ruleset.GetAllSSLSignaturesMap(&Patterns)
		detectTechBySSL(sslInfo, &sslSignatures, &detectedTech)
		sslSignatures = nil
	}

	// Transform the detectedTech map into a map of strings
	var detectedTechStr map[string]string = make(map[string]string)
	for k, v := range detectedTech {
		// calculate "confidence" based on the value of v
		if v <= re.DetectionConfig.NoiseThreshold {
			continue
		}
		if v < re.DetectionConfig.MaybeThreshold {
			detectedTechStr[k] = "traces"
		} else if v >= re.DetectionConfig.DetectedThreshold {
			detectedTechStr[k] = "yes"
		} else {
			detectedTechStr[k] = "maybe"
		}
	}
	return &detectedTechStr
}

func detectTechBySSL(sslInfo *SSLInfo, sslSignatures *map[string][]ruleset.SSLSignature, detectedTech *map[string]float32) {
	for ObjName := range *sslSignatures {
		for _, signature := range (*sslSignatures)[ObjName] {
			detectSSLTechBySignatureValue(sslInfo.CertChain, signature, detectedTech, ObjName)
		}
	}
}

func detectSSLTechBySignatureValue(certChain []*x509.Certificate, signature ruleset.SSLSignature, detectedTech *map[string]float32, ObjName string) {
	for _, cert := range certChain {
		// Get Certificate field based on the signature key
		certField, err := getCertificateField(cert, signature.Key)
		if err != nil {
			continue
		} else {
			for _, signatureValue := range signature.Value {
				if strings.Contains(certField, signatureValue) {
					(*detectedTech)[ObjName] += signature.Confidence
				}
			}
		}
	}
}

func getCertificateField(cert *x509.Certificate, key string) (string, error) {
	sValue := reflect.ValueOf(cert.Subject)
	sType := sValue.Type()
	for i := 0; i < sValue.NumField(); i++ {
		if sType.Field(i).Name == key {
			return sValue.Field(i).String(), nil
		}
	}
	return "", fmt.Errorf("field not found: %s", key)
}

func detectTechnologiesByKeyword(responseBody string, signatures *map[string][]ruleset.PageContentSignature, detectedTech *map[string]float32) {
	// Create a new document from the HTML string
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(responseBody))
	if err != nil {
		fmt.Printf("error loading HTML: %s\n", err)
		return
	}
	// Iterate through all the signatures and check for possible technologies
	for sig := range *signatures {
		item := (*signatures)[sig]
		for _, signature := range item {
			detectTechBySignature(responseBody, doc, signature, sig, detectedTech)
		}
	}
}

func detectTechBySignature(responseBody string, doc *goquery.Document, signature ruleset.PageContentSignature, sig string, detectedTech *map[string]float32) {
	if signature.Key == "*" {
		detectTechBySignatureValue(responseBody, signature.Signature, sig, detectedTech, signature.Confidence)
	} else {
		doc.Find(signature.Key).Each(func(index int, htmlItem *goquery.Selection) {
			var text string
			if (signature.Attribute != "") && (signature.Attribute != "text") {
				text = htmlItem.AttrOr(strings.ToLower(strings.TrimSpace(signature.Attribute)), "")
			} else {
				text = htmlItem.Text()
			}
			detectTechBySignatureValue(text, signature.Signature, sig, detectedTech, signature.Confidence)
		})
	}
}

func detectTechBySignatureValue(text string, signatures []string, sig string, detectedTech *map[string]float32, confidence float32) {
	for _, sigValue := range signatures {
		if strings.Contains(text, sigValue) {
			(*detectedTech)[sig] += confidence
		}
	}
}

func detectTechByTag(header *http.Header, tagName string, cmsNames *map[string]map[string]ruleset.HTTPHeaderField, detectedTech *map[string]float32) {
	hh := (*header)[tagName] // get the header value (header tag name is case sensitive)
	tagName = strings.ToLower(tagName)
	if len(hh) != 0 {
		for _, tag := range hh {
			tag = strings.ToLower(tag)
			for ObjName := range *cmsNames {
				item := (*cmsNames)[ObjName]
				for _, signature := range item[tagName].Value {
					if strings.Contains(tag, strings.ToLower(strings.TrimSpace(signature))) {
						(*detectedTech)[ObjName] += item[tagName].Confidence
					}
				}
			}
		}
	}
}

func detectTechByMetaTags(responseBody string, signatures *map[string][]ruleset.MetaTag, detectedTech *map[string]float32) {
	// Create a new document from the HTML string
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(responseBody))
	if err != nil {
		fmt.Printf("error loading HTML: %s\n", err)
		return
	}
	// Iterate through all the meta tags and check for possible technologies
	for ObjName := range *signatures {
		for _, signature := range (*signatures)[ObjName] {
			doc.Find("meta").Each(func(index int, htmlItem *goquery.Selection) {
				if strings.EqualFold(htmlItem.AttrOr("name", ""), signature.Name) {
					text := strings.ToLower(htmlItem.AttrOr("content", ""))
					if strings.Contains(text, signature.Content) {
						(*detectedTech)[ObjName] += signature.Confidence
					}
				}
			})
		}
	}
}

func detectTechByURL(url string, URLSignatures *map[string][]ruleset.URLMicroSignature, detectedTech *map[string]float32) {
	for ObjName := range *URLSignatures {
		for _, signature := range (*URLSignatures)[ObjName] {
			if strings.Contains(url, signature.Signature) {
				(*detectedTech)[ObjName] += signature.Confidence
			}
		}
	}
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
