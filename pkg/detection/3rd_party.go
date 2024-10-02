package detection

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	cmn "github.com/pzaino/thecrowler/pkg/common"
)

const (
	errDecJSON        = "Error decoding JSON response: %v"
	contentType       = "application/json"
	contentTypeHeader = "Content-Type"
	bearerPrefix      = "Bearer "
)

// --------------- Generic Methods ---------------

type trdPRequest struct {
	Provider    string
	Method      string
	URL         string
	APIKeyLabel string
	APIKey      string
	APIID       string
	APISecret   string
	APIToken    string
	Body        map[string]interface{}
	UserAgent   string
}

func trdPRequestInfo(reqInfo *trdPRequest) (map[string]interface{}, error) {
	if reqInfo.Method == "" {
		reqInfo.Method = "GET"
	}
	if reqInfo.URL == "" {
		cmn.DebugMsg(cmn.DbgLvlError, "Error scanning with %s: URL is required", reqInfo.Provider)
		return nil, nil
	}

	// Prepare client and request
	client := &http.Client{}

	var buffer *bytes.Buffer = nil
	var reqBody []byte = nil
	if reqInfo.Method == "POST" {
		reqBody, _ := json.Marshal(reqInfo.Body)
		buffer = bytes.NewBuffer(reqBody)
	}
	req, _ := http.NewRequest(reqInfo.Method, reqInfo.URL, buffer)
	if buffer != nil {
		req.Body = io.NopCloser(bytes.NewReader(reqBody))
	}

	if reqInfo.APIID != "" {
		req.SetBasicAuth(reqInfo.APIID, reqInfo.APISecret)
	}
	if reqInfo.APIToken != "" {
		req.Header.Set("Authorization", bearerPrefix+reqInfo.APIToken)
	}
	if reqInfo.APIKey != "" {
		req.Header.Set(reqInfo.APIKeyLabel, reqInfo.APIKey)
	}
	if reqInfo.UserAgent != "" {
		req.Header.Set("User-Agent", reqInfo.UserAgent)
	}
	req.Header.Set(contentTypeHeader, contentType)

	resp, err := client.Do(req)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error scanning with %s: %v", reqInfo.Provider, err)
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error reading response body for %s: %v", reqInfo.Provider, err)
		return nil, err
	}

	var result map[string]interface{}
	err = json.Unmarshal(body, &result)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, errDecJSON, err)
		return nil, err
	}

	return result, nil
}

func requestInfo(reqInfo *trdPRequest) map[string]interface{} {
	result, err := trdPRequestInfo(reqInfo)
	if err != nil {
		return nil
	}
	return result
}

// ----------------- IP Scanners -----------------

// ScanWithAbuseIPDB scans an IP address with AbuseIPDB.
func ScanWithAbuseIPDB(apiKey, ip string) map[string]interface{} {
	reqInfo := &trdPRequest{
		Provider:    "AbuseIPDB",
		Method:      "GET",
		URL:         "https://api.abuseipdb.com/api/v2/check?ipAddress=" + ip,
		APIKeyLabel: "Key",
		APIKey:      apiKey,
	}
	return requestInfo(reqInfo)
}

// ScanWithIPVoid scans an IP address with IPVoid.
func ScanWithIPVoid(apiKey, ip string) map[string]interface{} {
	reqInfo := &trdPRequest{
		Provider:    "IPVoid",
		Method:      "GET",
		URL:         "https://www.ipvoid.com/api/ip/" + ip + "/",
		APIKeyLabel: "Key",
		APIKey:      apiKey,
	}
	return requestInfo(reqInfo)
}

// ScanWithCensys scans an IP address with Censys.
func ScanWithCensys(apiID, apiSecret, ip string) map[string]interface{} {
	reqInfo := &trdPRequest{
		Provider:  "Censys",
		Method:    "GET",
		URL:       "https://censys.io/ipv4/" + ip,
		APIID:     apiID,
		APISecret: apiSecret,
	}
	return requestInfo(reqInfo)
}

// ----------------- URL Scanners -----------------

// ScanWithSSLLabs scans a URL with SSL Labs.
func ScanWithSSLLabs(url string) map[string]interface{} {
	reqInfo := &trdPRequest{
		Provider: "SSLLabs",
		Method:   "GET",
		URL:      "https://api.ssllabs.com/api/v3/analyze?host=" + url,
	}
	return requestInfo(reqInfo)
}

// ScanWithURLHaus scans a URL with URLHaus.
func ScanWithURLHaus(url string) map[string]interface{} {
	reqBody := map[string]interface{}{"url": url}
	reqInfo := &trdPRequest{
		Provider: "URLHaus",
		Method:   "POST",
		URL:      "https://urlhaus-api.abuse.ch/v1/url/",
		Body:     reqBody,
	}
	return requestInfo(reqInfo)
}

// ScanWidthThreatCrowd scans a URL with ThreatCrowd.
func ScanWithThreatCrowd(url string) map[string]interface{} {
	reqInfo := &trdPRequest{
		Provider: "ThreatCrowd",
		Method:   "GET",
		URL:      "https://api.threatcrowd.org/v1/url/report/?url=" + url,
	}
	return requestInfo(reqInfo)
}

// ScanWithCuckoo scans a URL with Cuckoo.
func ScanWithCuckoo(cuckooHost, url string) map[string]interface{} {
	reqBody := map[string]interface{}{"url": url}
	reqInfo := &trdPRequest{
		Provider: "Cuckoo",
		Method:   "POST",
		URL:      cuckooHost + "/tasks/create/url",
		Body:     reqBody,
	}
	return requestInfo(reqInfo)
}

// ScanWithVirusTotal scans a URL with VirusTotal.
func ScanWithVirusTotal(apiKey, url string) map[string]interface{} {
	reqBody := map[string]interface{}{"url": url}
	reqInfo := &trdPRequest{
		Provider:    "VirusTotal",
		Method:      "POST",
		URL:         "https://www.virustotal.com/vtapi/v2/url/scan",
		APIKeyLabel: "x-apikey",
		APIKey:      apiKey,
		Body:        reqBody,
	}
	return requestInfo(reqInfo)
}

// ScanWithPhishTank scans a URL with PhishTank.
func ScanWithPhishTank(apiKey, url string) map[string]interface{} {
	reqBody := map[string]interface{}{"url": url, "format": "json", "app_key": apiKey}
	reqInfo := &trdPRequest{
		Provider: "PhishTank",
		Method:   "POST",
		URL:      "https://checkurl.phishtank.com/checkurl/",
		Body:     reqBody,
	}
	return requestInfo(reqInfo)
}

// ScanWithGoogleSafeBrowsing scans a URL with Google Safe Browsing.
func ScanWithGoogleSafeBrowsing(apiKey, url string) map[string]interface{} {
	reqBody := map[string]interface{}{
		"client": map[string]string{
			"clientId":      "yourcompany",
			"clientVersion": "1.5.2",
		},
		"threatInfo": map[string]interface{}{
			"threatTypes":      []string{"MALWARE", "SOCIAL_ENGINEERING"},
			"platformTypes":    []string{"WINDOWS"},
			"threatEntryTypes": []string{"URL"},
			"threatEntries": []map[string]string{
				{"url": url},
			},
		},
	}
	reqInfo := &trdPRequest{
		Provider: "GoogleSafeBrowsing",
		Method:   "POST",
		URL:      "https://safebrowsing.googleapis.com/v4/threatMatches:find?key=" + apiKey,
		Body:     reqBody,
	}
	return requestInfo(reqInfo)
}

// ScanWithOpenPhish scans a URL with OpenPhish.
func ScanWithOpenPhish(apiKey, url string) map[string]interface{} {
	reqBody := map[string]interface{}{"url": url}
	reqInfo := &trdPRequest{
		Provider:    "OpenPhish",
		Method:      "POST",
		URL:         "https://openphish.com/check",
		APIKeyLabel: "Authorization",
		APIKey:      bearerPrefix + apiKey,
		Body:        reqBody,
	}
	return requestInfo(reqInfo)
}

// ScanWithHybridAnalysis scans a URL with Hybrid Analysis.
func ScanWithHybridAnalysis(apiKey, url string) map[string]interface{} {
	reqBody := map[string]interface{}{"url": url}
	reqInfo := &trdPRequest{
		Provider:    "HybridAnalysis",
		Method:      "POST",
		URL:         "https://www.hybrid-analysis.com/api/v2/quick-scan/url",
		APIKeyLabel: "api-key",
		APIKey:      apiKey,
		Body:        reqBody,
		UserAgent:   "Falcon Sandbox",
	}
	return requestInfo(reqInfo)
}

// ScanWithCiscoUmbrella scans a URL with Cisco Umbrella.
func ScanWithCiscoUmbrella(apiKey, url string) map[string]interface{} {
	reqBody := map[string]interface{}{"domain": url}
	reqInfo := &trdPRequest{
		Provider:    "CiscoUmbrella",
		Method:      "POST",
		URL:         "https://investigate.api.umbrella.com/dnsdb/name/a/" + url + ".json",
		APIKeyLabel: "Authorization",
		APIKey:      bearerPrefix + apiKey,
		Body:        reqBody,
	}
	return requestInfo(reqInfo)
}

// ScanWithAlienVault scans a URL with AlienVault.
func ScanWithAlienVault(apiKey, url string) map[string]interface{} {
	reqBody := map[string]interface{}{"url": url}
	reqInfo := &trdPRequest{
		Provider:    "AlienVault",
		Method:      "POST",
		URL:         "https://otx.alienvault.com/api/v1/indicators/url/" + url + "/",
		APIKeyLabel: "X-OTX-API-KEY",
		APIKey:      apiKey,
		Body:        reqBody,
	}
	return requestInfo(reqInfo)
}

// ScanWithShodan scans an IP address with Shodan.
func ScanWithShodan(apiKey, ip string) map[string]interface{} {
	reqInfo := &trdPRequest{
		Provider:    "Shodan",
		Method:      "GET",
		URL:         "https://api.shodan.io/shodan/host/" + ip,
		APIKeyLabel: "Authorization",
		APIKey:      bearerPrefix + apiKey,
	}
	return requestInfo(reqInfo)
}

// ----------------- File Scanners -----------------

// ScanWithVirusTotalFile scans a file with VirusTotal.
func ScanWithVirusTotalFile(apiKey, file string) map[string]interface{} {
	reqBody := map[string]interface{}{"file": file}
	reqInfo := &trdPRequest{
		Provider:    "VirusTotal",
		Method:      "POST",
		URL:         "https://www.virustotal.com/vtapi/v2/file/scan",
		APIKeyLabel: "x-apikey",
		APIKey:      apiKey,
		Body:        reqBody,
	}
	return requestInfo(reqInfo)
}

// ScanWithHybridAnalysisFile scans a file with Hybrid Analysis.
func ScanWithHybridAnalysisFile(apiKey, file string) map[string]interface{} {
	reqBody := map[string]interface{}{"file": file}
	reqInfo := &trdPRequest{
		Provider:    "HybridAnalysis",
		Method:      "POST",
		URL:         "https://www.hybrid-analysis.com/api/v2/quick-scan/file",
		APIKeyLabel: "api-key",
		APIKey:      apiKey,
		Body:        reqBody,
		UserAgent:   "Falcon Sandbox",
	}
	return requestInfo(reqInfo)
}

// ScanWithCuckooFile scans a file with Cuckoo.
func ScanWithCuckooFile(cuckooHost, file string) map[string]interface{} {
	reqBody := map[string]interface{}{"file": file}
	reqInfo := &trdPRequest{
		Provider: "Cuckoo",
		Method:   "POST",
		URL:      cuckooHost + "/tasks/create/file",
		Body:     reqBody,
	}
	return requestInfo(reqInfo)
}
