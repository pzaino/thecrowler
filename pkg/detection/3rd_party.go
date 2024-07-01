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
