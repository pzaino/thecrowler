package netinfo

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestGetNetInfo(t *testing.T) {
	// Replace "example.com" with the URL you want to test
	url := "example.com"

	// Create a new NetInfo instance
	ni := &NetInfo{}

	// Call GetNetInfo to retrieve network information
	err := ni.GetNetInfo(url)

	// Check for errors
	if err != nil {
		t.Errorf("GetNetInfo(%s) returned an error: %v", url, err)
		return
	}

	// Print the full NetInfo content for debugging purposes
	fmt.Printf("NetInfo for URL %s:\n", url)
	jsonData, _ := json.MarshalIndent(ni, "", "  ")
	fmt.Println(string(jsonData))

	// Validate the returned NetInfo struct
	if ni.URL != url {
		t.Errorf("Expected URL in NetInfo to be %s, but got %s", url, ni.URL)
	}

	// Add more validation as needed for Hosts, IPs, and WHOIS data
}
