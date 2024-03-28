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

// Package netinfo provides functionality to extract network information
package netinfo

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func TestGetNetInfo(t *testing.T) {
	if os.Getenv("GITHUB_ACTIONS") == "true" || os.Getenv("CI") == "true" {
		t.Skip("Skipping this test in CI or GitHub Actions.")
	}

	// Replace "example.com" with the URL you want to test
	url := "https://www.example.com/"

	// Create a new NetInfo instance
	ni := &NetInfo{}
	c := cfg.NewConfig()
	ni.Config = &c.NetworkInfo

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
