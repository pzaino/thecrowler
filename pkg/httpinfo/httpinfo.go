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
	detect "github.com/pzaino/thecrowler/pkg/detection"
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
func ExtractHTTPInfo(config Config, re *ruleset.RuleEngine, htmlContent string) (*HTTPDetails, error) {
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
	sslInfo, err := getSSLInfo(&config)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "retrieving SSL information: %v", err)
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
	detectedItems, err := analyzeResponse(resp, info, sslInfo, re, &htmlContent)
	if err != nil {
		return nil, err
	}
	info.DetectedEntities = make(map[string]detect.DetectedEntity)
	for k, v := range detectedItems {
		info.DetectedEntities[k] = v
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

func getSSLInfo(config *Config) (*SSLInfo, error) {
	// Check if the URL has a port number, if so, extract the port number
	url := strings.TrimSpace(config.URL)
	port := ""
	// first let's remove the scheme
	if strings.HasPrefix(strings.ToLower(url), "http") {
		url = strings.Replace(url, "http://", "", 1)
		url = strings.Replace(url, "https://", "", 1)
		port = "443"
	} else if strings.HasPrefix(strings.ToLower(url), "ftp") {
		url = strings.Replace(url, "ftp://", "", 1)
		url = strings.Replace(url, "ftps://", "", 1)
		port = "21"
	} else if strings.HasPrefix(strings.ToLower(url), "ws:") ||
		strings.HasPrefix(strings.ToLower(url), "wss:") {
		url = strings.Replace(url, "ws://", "", 1)
		url = strings.Replace(url, "wss://", "", 1)
		port = "80"
	}
	// now let's check if there is a port number
	if strings.Contains(url, ":") {
		// extract the port number
		port = strings.Split(url, ":")[1]
		// remove the port number from the URL
		url = strings.Split(url, ":")[0]
	}

	cmn.DebugMsg(cmn.DbgLvlDebug1, "URL: %s, Port: %s", url, port)

	// Get the SSL information
	sslInfo := NewSSLInfo()
	//err := sslInfo.GetSSLInfo(url, port)
	err := sslInfo.CollectSSLData(url, port, config)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug1, "Error retrieving SSL information: %v", err)
	}

	// Validate the SSL certificate
	err = sslInfo.ValidateCertificate()
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug1, "Error validating SSL certificate: %v", err)
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

	if len(config.Proxies) > 0 {
		proxyURL, err := url.Parse(config.Proxies[0].Address)
		if err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
		if config.Proxies[0].Username != "" {
			transport.ProxyConnectHeader = http.Header{}
			transport.ProxyConnectHeader.Set("Proxy-Authorization", basicAuth(config.Proxies[0].Username, config.Proxies[0].Password))
		}
	}

	httpClient := &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return handleRedirect(req, via, config, transport)
		},
	}
	return httpClient
}

func basicAuth(username, password string) string {
	auth := username + ":" + password
	return "Basic " + cmn.Base64Encode(auth)
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

	return ExtractHTTPInfo(newConfig, re, "")
}

// handleRedirect is a custom redirect handler that updates the ServerName for SNI in case of domain change due to redirect
func handleRedirect(req *http.Request, _ []*http.Request, config Config, transport *http.Transport) error {
	// TODO: rename _ to via and use it to check for infinite redirects
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
func analyzeResponse(resp *http.Response, info *HTTPDetails,
	sslInfo *SSLInfo, re *ruleset.RuleEngine,
	htmlContent *string) (map[string]detect.DetectedEntity, error) {
	// Get the response headers
	header := &(*info).ResponseHeaders

	// Read the response body
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// Convert the response body to a string
	responseBody := string(bodyBytes)
	if strings.TrimSpace(responseBody) == "" {
		// If the response body is empty, use the provided HTML content
		// (it is possible that a WAF or similar is blocking the request)
		responseBody = (*htmlContent)
	}

	// Initialize the infoList map
	// infoList := make(map[string]string)
	infoList := make(map[string]detect.DetectedEntity)

	// Detect Entities on the page/site
	sslInfoDetect := convertSSLInfoToSSLInfoDetect(sslInfo)
	detectCtx := detect.DetectionContext{
		WD:           nil,
		TargetURL:    info.URL,
		Header:       header,
		HSSLInfo:     &sslInfoDetect,
		ResponseBody: &responseBody,
	}
	x := detect.DetectTechnologies(&detectCtx)
	for k, v := range *x {
		infoList[k] = v
	}

	return infoList, nil
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
		cmn.DebugMsg(cmn.DbgLvlError, "extracting domain from URL: %v", err)
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

func convertSSLInfoToSSLInfoDetect(sslInfo *SSLInfo) detect.SSLInfo {
	return detect.SSLInfo{
		URL:                          sslInfo.URL,
		CertChain:                    sslInfo.CertChain,
		IntermediateAuthorities:      sslInfo.IntermediateAuthorities,
		IsCertChainOrderValid:        sslInfo.IsCertChainOrderValid,
		IsRootTrustworthy:            sslInfo.IsRootTrustworthy,
		IsCertValid:                  sslInfo.IsCertValid,
		IsCertExpired:                sslInfo.IsCertExpired,
		IsCertRevoked:                sslInfo.IsCertRevoked,
		IsCertSelfSigned:             sslInfo.IsCertSelfSigned,
		IsCertCA:                     sslInfo.IsCertCA,
		IsCertIntermediate:           sslInfo.IsCertIntermediate,
		IsCertLeaf:                   sslInfo.IsCertLeaf,
		IsCertTrusted:                sslInfo.IsCertTrusted,
		IsCertTechnicallyConstrained: sslInfo.IsCertTechnicallyConstrained,
		IsCertEV:                     sslInfo.IsCertEV,
		IsCertEVCodeSigning:          sslInfo.IsCertEVCodeSigning,
		IsCertEVSSL:                  sslInfo.IsCertEVSSL,
		IsCertEVSGC:                  sslInfo.IsCertEVSGC,
		IsCertEVSGCSSL:               sslInfo.IsCertEVSGCSSL,
		IsCertEVSGCCA:                sslInfo.IsCertEVSGCCA,
		IsCertEVSGCCASSL:             sslInfo.IsCertEVSGCCASSL,
		IsCertEVSGCCACodeSigning:     sslInfo.IsCertEVSGCCACodeSigning,
		IsCertEVSGCCACodeSigningSSL:  sslInfo.IsCertEVSGCCACodeSigningSSL,
		IsCertEVSGCCodeSigning:       sslInfo.IsCertEVSGCCodeSigning,
		IsCertEVSGCCodeSigningSSL:    sslInfo.IsCertEVSGCCodeSigningSSL,
		CertExpiration:               sslInfo.CertExpiration,
		Fingerprints:                 sslInfo.Fingerprints,
	}
}
