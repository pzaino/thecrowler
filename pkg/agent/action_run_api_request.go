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

// Package agent provides the agent functionality for the CROWler.
package agent

import (
	"errors"
	"fmt"
	"maps"
	"strings"

	cmn "github.com/pzaino/thecrowler/pkg/common"
)

// APIRequestAction performs an HTTP API request
type APIRequestAction struct{}

// Name returns the name of the action
func (a *APIRequestAction) Name() string {
	return "APIRequest"
}

// Execute performs the API request
func (a *APIRequestAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
	rval := make(map[string]interface{})
	rval[StrResponse] = nil
	rval[StrConfig] = nil

	const postType = "POST"
	const putType = "PUT"
	const patchType = "PATCH"

	input, err := getInput(params)
	if err != nil {
		// This step has no input, so let's assume input is empty
		input = map[string]interface{}{}
	}
	inputMap, _ := input[StrRequest].(map[string]interface{})

	config, err := getConfig(params)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, err
	}
	rval[StrConfig] = config

	url, ok := params["url"].(string)
	if !ok {
		rval[StrStatus] = StatusError
		rval[StrMessage] = ErrMissingURL
		return rval, errors.New(ErrMissingURL)
	}
	// Resolve any $response.xxx tokens in the URL string (if any)
	url = resolveResponseString(input, url)
	// Check if the final URL is valid
	if !cmn.IsURLValid(url) {
		rval[StrStatus] = StatusError
		rval[StrMessage] = fmt.Sprintf("invalid URL: %s", url)
		return rval, fmt.Errorf("invalid URL: %s", url)
	}

	// Create request object
	request := map[string]string{
		"url": url,
	}

	// Get request type (GET, POST, PUT, DELETE, etc.)
	method := "GET"
	if v, ok := params["request_type"].(string); ok {
		method = v
	}
	if v, ok := params["type"].(string); ok {
		method = v
	} // backward compat
	method = strings.ToUpper(strings.TrimSpace(method))
	request["method"] = method

	// Create requestBody (if the request is a POST or PUT)
	if request["type"] == postType || request["type"] == putType || request["type"] == patchType {
		switch b := params["body"].(type) {
		case map[string]interface{}:
			b = resolveValue(inputMap, b).(map[string]interface{})
			request["body"] = string(cmn.ConvertMapToJSON(b))
		case string:
			request["body"] = resolveResponseString(inputMap, b)
		case nil:
			request["body"] = "{}"
		default:
			rval[StrStatus] = StatusError
			rval[StrMessage] = "missing 'input' parameter"
			return rval, fmt.Errorf("missing 'input' parameter")
		}
	}

	// Create RequestHeaders
	requestHeaders := make(map[string]interface{})
	requestHeaders["User-Agent"] = "CROWler"
	requestHeaders["Accept"] = jsonAppType
	if request["type"] == postType || request["type"] == putType {
		requestHeaders["Content-Type"] = jsonAppType
	}
	if params["auth"] != nil {
		auth, ok := params["auth"].(string)
		if ok {
			auth = strings.TrimSpace(resolveResponseString(inputMap, auth))
		}
		requestHeaders["Authorization"] = auth
	} else if config["api_key"] != nil {
		auth, ok := config["api_key"].(string)
		if ok {
			auth = strings.TrimSpace(resolveResponseString(inputMap, auth))
			requestHeaders["Authorization"] = auth
		}
	}
	// Check if we have additional headers in the params
	if params["headers"] != nil {
		headers, ok := params["headers"].(map[string]interface{})
		if ok {
			headersProcessed := resolveValue(inputMap, headers).(map[string]interface{})
			maps.Copy(requestHeaders, headersProcessed)
		}
	}
	request["headers"] = string(cmn.ConvertMapToJSON(requestHeaders))

	response, err := cmn.GenericAPIRequest(request)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = fmt.Sprintf("API request failed: %v", err)
		return rval, fmt.Errorf("API request failed: %v", err)
	}

	responseMap, err := cmn.JSONStrToMap(response)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = fmt.Sprintf("could not parse response: %v", err)
		return rval, fmt.Errorf("could not parse response: %v", err)
	}

	rval[StrResponse] = responseMap
	rval[StrStatus] = StatusSuccess
	rval[StrMessage] = "API request successful"

	return rval, nil
}
