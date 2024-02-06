package netinfo

import (
	"testing"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

/*
func TestExampleOfUsingGetDNSInfo(t *testing.T) {
	domain := "example.com"
	dnsInfo, err := GetDNSInfo(domain)
	if err != nil {
		fmt.Println("Error getting DNS info:", err)
		return
	}

	jsonInfo, _ := json.MarshalIndent(dnsInfo, "", "    ")
	fmt.Println(string(jsonInfo))
}
*/

func TestGetDNSInfo(t *testing.T) {
	ni := &NetInfo{URL: "www.example.com"} // Replace with your NetInfo initialization
	c := cfg.NewConfig()
	ni.Config = &c.NetworkInfo

	err := ni.GetDNSInfo()
	if err != nil {
		t.Errorf("Error getting DNS info: %v", err)
		return
	}

	expectedDNSInfo := createExpectedDNSInfo("www.example.com")

	foundMatch := checkDNSInfoMatch(ni.DNS, expectedDNSInfo, t)

	if !foundMatch {
		t.Errorf("No matching DNSInfo found for domain %s", expectedDNSInfo.Domain)
	}

	// Optional: Print the retrieved DNSInfo
	//jsonInfo, _ := json.MarshalIndent(ni.DNS, "", "    ")
	//fmt.Println(string(jsonInfo))
}

func createExpectedDNSInfo(domain string) *DNSInfo {
	expectedDNSInfo := &DNSInfo{
		Domain: domain,
	}

	// Modify expectedDNSInfo based on your expected DNS records
	// For example, if you expect an 'A' record:
	expectedDNSInfo.Records = append(expectedDNSInfo.Records, DNSRecord{
		Name:     "www.example.com.",
		Type:     "A",
		TTL:      "80143",
		Class:    "IN",
		Response: "93.184.216.34",
		Value:    "www.example.com. 80143 IN A 93.184.216.34",
	})

	return expectedDNSInfo
}

func checkDNSInfoMatch(dnsInfoList []DNSInfo, expectedDNSInfo *DNSInfo, t *testing.T) bool {
	foundMatch := false

	for _, dnsInfo := range dnsInfoList {
		if dnsInfo.Domain == expectedDNSInfo.Domain {
			foundMatch = true
			if len(dnsInfo.Records) == 0 {
				t.Errorf("Expected %d DNS records, but got %d", len(expectedDNSInfo.Records), len(dnsInfo.Records))
				break
			}
			if !checkRecordsMatch(dnsInfo.Records, expectedDNSInfo.Records, t) {
				break
			}
		}
	}

	return foundMatch
}

func checkRecordsMatch(records []DNSRecord, expectedRecords []DNSRecord, t *testing.T) bool {
	for i, expectedRecord := range expectedRecords {
		if records[i].Type == "A" || records[i].Type == "AAAA" {
			return true
		}
		if expectedRecord.Type == "" {
			t.Errorf("Expected record type is empty")
			return false
		}
	}
	return true
}
