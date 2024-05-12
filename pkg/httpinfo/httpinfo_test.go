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
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	"github.com/pzaino/thecrowler/pkg/ruleset"
)

var (
	url1 = "https://www.example.com"
	url2 = "https://jsonplaceholder.typicode.com/posts/1"

	usrAgentField = "User-Agent"

	sysConfig = cfg.Config{
		Selenium: []cfg.Selenium{
			{
				Type: "chrome",
			},
		},
	}
)

func TestCreateConfig(t *testing.T) {
	sel := sysConfig.Selenium[0]
	usrAgent := cmn.UsrAgentStrMap[sel.Type+"-desktop01"]
	expected := Config{
		URL:             url1,
		CustomHeader:    map[string]string{usrAgentField: usrAgent},
		FollowRedirects: true,
		Timeout:         60,
		SSLMode:         "none",
	}

	result := CreateConfig(url1, sysConfig)

	if !reflect.DeepEqual(result, expected) {
		t.Errorf("CreateConfig() = %v; want %v", result, expected)
	}
}

func TestExtractHTTPInfo(t *testing.T) {
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		t.Skip("Skipping this test in GitHub Actions.")
	}

	sel := sysConfig.Selenium[0]
	usrAgent := cmn.UsrAgentStrMap[sel.Type+"-desktop01"]
	config := Config{
		URL:             url2,
		CustomHeader:    map[string]string{usrAgentField: usrAgent},
		FollowRedirects: true,
		Timeout:         120,
		SSLMode:         "none",
	}

	rs := []ruleset.Ruleset{}
	re := ruleset.NewRuleEngine("", rs)

	info, err := ExtractHTTPInfo(config, re, "")
	if err != nil {
		t.Errorf("ExtractHTTPInfo() returned an error: %v", err)
	}

	/*
		// Create an expected result with the collected information
		respBody := []string{"No additional information found"}
		expected := &Response{
			URL:              url2,
			CustomHeaders:    map[string]string{usrAgentField: usrAgent},
			FollowRedirects:  true,
			ResponseHeaders:  info.ResponseHeaders,
			ServerType:       info.ServerType,
			ResponseBodyInfo: respBody,
		}

		// Marshal the expected result to JSON
		expectedJSON, err := json.Marshal(expected)
		if err != nil {
			t.Errorf("Error marshaling expected result to JSON: %v", err)
		}
	*/

	// Marshal the actual result to JSON
	actualJSON, err := json.Marshal(info)
	if err != nil {
		t.Errorf("Error marshaling actual result to JSON: %v", err)
	}

	// Check if the actual JSON matches the expected JSON
	if actualJSON == nil {
		t.Errorf("ExtractHTTPInfo() returned an empty result")
	}
	// display formatted JSON for actualJSON:
	fmt.Println(string(actualJSON))
	/*
		if !reflect.DeepEqual(actualJSON, expectedJSON) {
			t.Errorf("ExtractHTTPInfo() result does not match the expected result.\nExpected: %s\nActual: %s", expectedJSON, actualJSON)
		}
	*/
}
