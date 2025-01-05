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

// Package common package is used to store common functions and variables
package common

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// FetchRemoteFile fetches a remote file and returns the contents as a string.
func FetchRemoteFile(url string, timeout int, sslmode string) (string, error) {
	httpClient := &http.Client{
		Transport: SafeTransport(timeout, sslmode),
	}
	resp, err := httpClient.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to fetch file from %s: %v", url, err)
	}
	defer resp.Body.Close() //nolint:errcheck // Don't lint for error not checked, this is a defer statement

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("received non-200 response from %s: %d", url, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %v", err)
	}

	return string(body), nil
}

// APIResponse is a generic API response.
type APIResponse struct {
	StatusCode int    `json:"status_code" yaml:"status_code"`
	Body       string `json:"body" yaml:"body"`
}

// GenericAPIRequest is a generic API request. (it uses passed params to determine the request)
func GenericAPIRequest(params map[string]string) (string, error) {
	// Prepare the API request
	var response string

	// Check if the URL is valid
	if !IsURLValid(params["url"]) {
		return "", fmt.Errorf("invalid URL: %s", params["url"])
	}

	// Scan params for request authentication and headers
	headers := make(map[string]string)
	for key, value := range params {
		if key == "auth" {
			// Set authentication
			headers["Authorization"] = value
		} else if key == "headers" {
			// Set headers
			headers["headers"] = value
		}
	}

	// Extract the request method
	method, ok := params["method"]
	if !ok {
		method = "GET"
	}

	// Extract the request body
	var body io.Reader
	if params["body"] != "" {
		body = io.NopCloser(strings.NewReader(params["body"]))
	}

	// Create the request
	req, err := http.NewRequest(method, params["url"], body)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %v", err)
	}

	// Add headers
	for key, value := range headers {
		req.Header.Add(key, value)
	}

	// Send the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // Don't lint for error not checked, this is a defer statement

	// Read the response
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %v", err)
	}

	// prepare the response
	rs := APIResponse{
		StatusCode: resp.StatusCode,
		Body:       string(responseBody),
	}

	// Transform rs into a JSON string
	resByte, err := json.Marshal(rs)
	if err != nil {
		return "", fmt.Errorf("failed to marshal response: %v", err)
	}

	response = string(resByte)
	return response, nil
}
