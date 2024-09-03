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

// ----------------- IP Scanners -----------------

// ScanWithAbuseIPDB scans an IP address with AbuseIPDB.
func ScanWithAbuseIPDB(apiKey, ip string) map[string]interface{} {
	client := &http.Client{}
	req, _ := http.NewRequest("GET", "https://api.abuseipdb.com/api/v2/check?ipAddress="+ip, nil)
	req.Header.Set("Key", apiKey)
	req.Header.Set(contentTypeHeader, contentType)

	resp, err := client.Do(req)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error scanning with AbuseIPDB: %v", err)
		return nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result map[string]interface{}
	err = json.Unmarshal(body, &result)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, errDecJSON, err)
		return nil
	}

	return result
}

// ScanWithIPVoid scans an IP address with IPVoid.
func ScanWithIPVoid(apiKey, ip string) map[string]interface{} {
	client := &http.Client{}
	req, _ := http.NewRequest("GET", "https://www.ipvoid.com/api/ip/"+ip+"/", nil)
	req.Header.Set("Key", apiKey)
	req.Header.Set(contentTypeHeader, contentType)

	resp, err := client.Do(req)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error scanning with IPVoid: %v", err)
		return nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result map[string]interface{}
	err = json.Unmarshal(body, &result)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, errDecJSON, err)
		return nil
	}

	return result
}

// ScanWithCensys scans an IP address with Censys.
func ScanWithCensys(apiID, apiSecret, ip string) map[string]interface{} {
	client := &http.Client{}
	req, _ := http.NewRequest("GET", "https://censys.io/ipv4/"+ip, nil)
	req.SetBasicAuth(apiID, apiSecret)
	req.Header.Set(contentTypeHeader, contentType)

	resp, err := client.Do(req)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error scanning with Censys: %v", err)
		return nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result map[string]interface{}
	err = json.Unmarshal(body, &result)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, errDecJSON, err)
		return nil
	}

	return result
}

// ----------------- URL Scanners -----------------

// ScanWithSSLLabs scans a URL with SSL Labs.
func ScanWithSSLLabs(url string) map[string]interface{} {
	client := &http.Client{}
	req, _ := http.NewRequest("GET", "https://api.ssllabs.com/api/v3/analyze?host="+url, nil)
	req.Header.Set(contentTypeHeader, contentType)

	resp, err := client.Do(req)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error scanning with SSL Labs: %v", err)
		return nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result map[string]interface{}
	err = json.Unmarshal(body, &result)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, errDecJSON, err)
		return nil
	}

	return result
}

// ScanWithURLHaus scans a URL with URLHaus.
func ScanWithURLHaus(url string) map[string]interface{} {
	client := &http.Client{}
	reqBody := map[string]string{"url": url}
	jsonBody, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("POST", "https://urlhaus-api.abuse.ch/v1/url/", bytes.NewBuffer(jsonBody))
	req.Header.Set(contentTypeHeader, contentType)

	resp, err := client.Do(req)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error scanning with URLHaus: %v", err)
		return nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result map[string]interface{}
	err = json.Unmarshal(body, &result)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, errDecJSON, err)
		return nil
	}

	return result
}

// ScanWidthThreatCrowd scans a URL with ThreatCrowd.
func ScanWidthThreatCrowd(url string) map[string]interface{} {
	client := &http.Client{}
	req, _ := http.NewRequest("GET", "https://api.threatcrowd.org/v1/url/report/?url="+url, nil)
	req.Header.Set(contentTypeHeader, contentType)

	resp, err := client.Do(req)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error scanning with ThreatCrowd: %v", err)
		return nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result map[string]interface{}
	err = json.Unmarshal(body, &result)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, errDecJSON, err)
		return nil
	}

	return result
}

// ScanWithCuckoo scans a URL with Cuckoo.
func ScanWithCuckoo(cuckooHost, url string) map[string]interface{} {
	client := &http.Client{}
	reqBody := map[string]string{"url": url}
	jsonBody, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("POST", cuckooHost+"/tasks/create/url", bytes.NewBuffer(jsonBody))
	req.Header.Set(contentTypeHeader, contentType)

	resp, err := client.Do(req)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error scanning with Cuckoo: %v", err)
		return nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result map[string]interface{}
	err = json.Unmarshal(body, &result)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, errDecJSON, err)
		return nil
	}

	return result
}

// ScanWithVirusTotal scans a URL with VirusTotal.
func ScanWithVirusTotal(apiKey, url string) map[string]interface{} {
	client := &http.Client{}
	reqBody := map[string]interface{}{"url": url}
	jsonBody, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("POST", "https://www.virustotal.com/vtapi/v2/url/scan", bytes.NewBuffer(jsonBody))
	req.Header.Set("x-apikey", apiKey)
	req.Header.Set(contentTypeHeader, contentType)

	resp, err := client.Do(req)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error scanning with VirusTotal: %v", err)
		return nil
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&result)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, errDecJSON, err)
		return nil
	}

	return result
}

// ScanWithPhishTank scans a URL with PhishTank.
func ScanWithPhishTank(apiKey, url string) map[string]interface{} {
	client := &http.Client{}
	reqBody := map[string]string{"url": url, "format": "json", "app_key": apiKey}
	jsonBody, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("POST", "https://checkurl.phishtank.com/checkurl/", bytes.NewBuffer(jsonBody))
	req.Header.Set(contentTypeHeader, contentType)

	resp, err := client.Do(req)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error scanning with PhishTank: %v", err)
		return nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result map[string]interface{}
	err = json.Unmarshal(body, &result)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, errDecJSON, err)
		return nil
	}

	return result
}

// ScanWithGoogleSafeBrowsing scans a URL with Google Safe Browsing.
func ScanWithGoogleSafeBrowsing(apiKey, url string) map[string]interface{} {
	client := &http.Client{}
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
	jsonBody, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("POST", "https://safebrowsing.googleapis.com/v4/threatMatches:find?key="+apiKey, bytes.NewBuffer(jsonBody))
	req.Header.Set(contentTypeHeader, contentType)

	resp, err := client.Do(req)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error scanning with Google Safe Browsing: %v", err)
		return nil
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&result)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, errDecJSON, err)
		return nil
	}

	return result
}

// ScanWithOpenPhish scans a URL with OpenPhish.
func ScanWithOpenPhish(apiKey, url string) map[string]interface{} {
	client := &http.Client{}
	reqBody := map[string]string{"url": url}
	jsonBody, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("POST", "https://openphish.com/check", bytes.NewBuffer(jsonBody))
	req.Header.Set("Authorization", bearerPrefix+apiKey)
	req.Header.Set(contentTypeHeader, contentType)

	resp, err := client.Do(req)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error scanning with OpenPhish: %v", err)
		return nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result map[string]interface{}
	err = json.Unmarshal(body, &result)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, errDecJSON, err)
		return nil
	}

	return result
}

// ScanWithHybridAnalysis scans a URL with Hybrid Analysis.
func ScanWithHybridAnalysis(apiKey, url string) map[string]interface{} {
	client := &http.Client{}
	reqBody := map[string]string{"url": url}
	jsonBody, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("POST", "https://www.hybrid-analysis.com/api/v2/quick-scan/url", bytes.NewBuffer(jsonBody))
	req.Header.Set("User-Agent", "Falcon Sandbox")
	req.Header.Set("api-key", apiKey)
	req.Header.Set(contentTypeHeader, contentType)

	resp, err := client.Do(req)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error scanning with Hybrid Analysis: %v", err)
		return nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result map[string]interface{}
	err = json.Unmarshal(body, &result)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, errDecJSON, err)
		return nil
	}

	return result
}

// ScanWithCiscoUmbrella scans a URL with Cisco Umbrella.
func ScanWithCiscoUmbrella(apiKey, url string) map[string]interface{} {
	client := &http.Client{}
	reqBody := map[string]string{"domain": url}
	jsonBody, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("POST", "https://investigate.api.umbrella.com/dnsdb/name/a/"+url+".json", bytes.NewBuffer(jsonBody))
	req.Header.Set("Authorization", bearerPrefix+apiKey)
	req.Header.Set(contentTypeHeader, contentType)

	resp, err := client.Do(req)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error scanning with Cisco Umbrella: %v", err)
		return nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result map[string]interface{}
	err = json.Unmarshal(body, &result)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, errDecJSON, err)
		return nil
	}

	return result
}

// ScanWithAlienVault scans a URL with AlienVault.
func ScanWithAlienVault(apiKey, url string) map[string]interface{} {
	client := &http.Client{}
	reqBody := map[string]string{"url": url}
	jsonBody, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("POST", "https://otx.alienvault.com/api/v1/indicators/url/"+url+"/", bytes.NewBuffer(jsonBody))
	req.Header.Set("X-OTX-API-KEY", apiKey)
	req.Header.Set(contentTypeHeader, contentType)

	resp, err := client.Do(req)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error scanning with AlienVault: %v", err)
		return nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result map[string]interface{}
	err = json.Unmarshal(body, &result)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, errDecJSON, err)
		return nil
	}

	return result
}

// ScanWithShodan scans an IP address with Shodan.
func ScanWithShodan(apiKey, ip string) map[string]interface{} {
	client := &http.Client{}
	req, _ := http.NewRequest("GET", "https://api.shodan.io/shodan/host/"+ip, nil)
	req.Header.Set("Authorization", bearerPrefix+apiKey)
	req.Header.Set(contentTypeHeader, contentType)

	resp, err := client.Do(req)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error scanning with Shodan: %v", err)
		return nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result map[string]interface{}
	err = json.Unmarshal(body, &result)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, errDecJSON, err)
		return nil
	}

	return result
}

// ----------------- File Scanners -----------------

// ScanWithVirusTotalFile scans a file with VirusTotal.
func ScanWithVirusTotalFile(apiKey, file string) map[string]interface{} {
	client := &http.Client{}
	reqBody := map[string]string{"file": file}
	jsonBody, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("POST", "https://www.virustotal.com/vtapi/v2/file/scan", bytes.NewBuffer(jsonBody))
	req.Header.Set("x-apikey", apiKey)
	req.Header.Set(contentTypeHeader, contentType)

	resp, err := client.Do(req)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error scanning file with VirusTotal: %v", err)
		return nil
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&result)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, errDecJSON, err)
		return nil
	}

	return result
}

// ScanWithHybridAnalysisFile scans a file with Hybrid Analysis.
func ScanWithHybridAnalysisFile(apiKey, file string) map[string]interface{} {
	client := &http.Client{}
	reqBody := map[string]string{"file": file}
	jsonBody, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("POST", "https://www.hybrid-analysis.com/api/v2/quick-scan/file", bytes.NewBuffer(jsonBody))
	req.Header.Set("User-Agent", "Falcon Sandbox")
	req.Header.Set("api-key", apiKey)
	req.Header.Set(contentTypeHeader, contentType)

	resp, err := client.Do(req)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error scanning file with Hybrid Analysis: %v", err)
		return nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result map[string]interface{}
	err = json.Unmarshal(body, &result)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, errDecJSON, err)
		return nil
	}

	return result
}

// ScanWithCuckooFile scans a file with Cuckoo.
func ScanWithCuckooFile(cuckooHost, file string) map[string]interface{} {
	client := &http.Client{}
	reqBody := map[string]string{"file": file}
	jsonBody, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("POST", cuckooHost+"/tasks/create/file", bytes.NewBuffer(jsonBody))
	req.Header.Set(contentTypeHeader, contentType)

	resp, err := client.Do(req)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error scanning file with Cuckoo: %v", err)
		return nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result map[string]interface{}
	err = json.Unmarshal(body, &result)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, errDecJSON, err)
		return nil
	}

	return result
}
