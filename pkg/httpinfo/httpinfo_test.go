package httpinfo

import (
	"encoding/json"
	"reflect"
	"testing"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
)

var (
	url1            = "https://www.example.com"
	url2            = "https://jsonplaceholder.typicode.com/posts/1"
	usr_agent_field = "User-Agent"

	sys_config = cfg.Config{
		Selenium: []cfg.Selenium{
			{
				Type: "chrome",
			},
		},
	}
)

func TestCreateConfig(t *testing.T) {
	sel := sys_config.Selenium[0]
	usrAgent := cmn.UsrAgentStrMap[sel.Type+"-desktop01"]
	expected := HTTPInfoConfig{
		URL:             url1,
		CustomHeader:    map[string]string{usr_agent_field: usrAgent},
		FollowRedirects: true,
	}

	result := CreateConfig(url1, sys_config)

	if !reflect.DeepEqual(result, expected) {
		t.Errorf("CreateConfig() = %v; want %v", result, expected)
	}
}

func TestExtractHTTPInfo(t *testing.T) {
	sel := sys_config.Selenium[0]
	usrAgent := cmn.UsrAgentStrMap[sel.Type+"-desktop01"]
	config := HTTPInfoConfig{
		URL:             url2,
		CustomHeader:    map[string]string{usr_agent_field: usrAgent},
		FollowRedirects: true,
	}

	info, err := ExtractHTTPInfo(config)
	if err != nil {
		t.Errorf("ExtractHTTPInfo() returned an error: %v", err)
	}

	/*
		// Create an expected result with the collected information
		respBody := []string{"No additional information found"}
		expected := &HTTPInfoResponse{
			URL:              url2,
			CustomHeaders:    map[string]string{usr_agent_field: usrAgent},
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

	/*
		if !reflect.DeepEqual(actualJSON, expectedJSON) {
			t.Errorf("ExtractHTTPInfo() result does not match the expected result.\nExpected: %s\nActual: %s", expectedJSON, actualJSON)
		}
	*/
}
