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
	"strconv"

	cmn "github.com/pzaino/thecrowler/pkg/common"
)

// AIInteractionAction interacts with an AI API
type AIInteractionAction struct{}

// Name returns the name of the action
func (a *AIInteractionAction) Name() string {
	return "AIInteraction"
}

// Execute sends a request to an AI API
func (a *AIInteractionAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
	rval := make(map[string]interface{})
	rval[StrResponse] = nil
	rval[StrConfig] = nil

	config, err := getConfig(params)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, err
	}
	rval[StrConfig] = config

	inputRaw, err := getInput(params)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, err
	}

	// Combine the prompt with the request
	var prompt string
	if params["prompt"] != nil {
		prompt = params["prompt"].(string)
		// Check if prompt needs to be resolved
		prompt = resolveResponseString(inputRaw, prompt)
	}
	if prompt == "" {
		prompt, _ = inputRaw[StrRequest].(string)
	}
	if prompt == "" {
		// Check is params has a message field
		if params[StrMessage] != nil {
			prompt = params[StrMessage].(string)
			// Check if prompt needs to be resolved
			prompt = resolveResponseString(inputRaw, prompt)
		}
	}
	if prompt == "" {
		rval[StrStatus] = StatusError
		rval[StrMessage] = "missing 'prompt' or 'message' parameter"
		return rval, fmt.Errorf("missing 'prompt' or 'message' parameter")
	}

	// Get the URL from the config
	url := ""
	if params["url"] == nil {
		// Try the config
		if config["url"] == nil {
			rval[StrStatus] = StatusError
			rval[StrMessage] = ErrMissingURL
			return rval, errors.New(ErrMissingURL)
		}
		url, _ = config["url"].(string)
	} else {
		urlRaw := params["url"]
		// Check if urlRaw is a string or a map
		_, ok := urlRaw.(string)
		if !ok {
			// Check if it's a map
			urlMap := urlRaw.(map[string]interface{})
			url = urlMap[StrRequest].(string)
		} else {
			url = urlRaw.(string)
		}
	}
	// Check if url needs to be resolved
	url = resolveResponseString(inputRaw, url)
	// Check if the final URL is valid
	if !cmn.IsURLValid(url) {
		rval[StrStatus] = StatusError
		rval[StrMessage] = fmt.Sprintf("invalid URL: %s", cmn.SafeEscapeJSONString(url))
		return rval, fmt.Errorf("invalid URL: %s", cmn.SafeEscapeJSONString(url))
	}

	// Generate the API request based on the input and parameters
	request := map[string]string{
		"url": url,
	}

	// Prepare request body
	requestBody := make(map[string]interface{}, 1)
	// Check if we have a prompt or messages
	if msgs, ok := params["messages"].([]interface{}); ok && len(msgs) > 0 {
		requestBody["messages"] = resolveValue(inputRaw, msgs)
	} else {
		requestBody["prompt"] = prompt
	}
	// Check if we have additional parameters for AI in params like temperature, max_tokens, etc.
	if params["temperature"] != nil {
		// Temperature should be a float value between 0 and 1
		value, ok := params["temperature"].(float64)
		if ok {
			requestBody["temperature"] = value
		} else {
			rval[StrStatus] = StatusError
			rval[StrMessage] = fmt.Sprintf("temperature '%s' parameter doesn't appear to be a valid float", cmn.SafeEscapeJSONString(config["temperature"]))
			return rval, fmt.Errorf("temperature '%s' parameter doesn't appear to be a valid float", cmn.SafeEscapeJSONString(config["temperature"]))
		}
	}
	// Check if we have max_tokens
	if params["max_tokens"] != nil {
		value, ok := params["max_tokens"].(float64)
		if ok {
			// Max tokens should be an integer value
			requestBody["max_tokens"] = int(value)
		} else {
			rval[StrStatus] = StatusError
			rval[StrMessage] = fmt.Sprintf("max_tokens '%s' parameter doesn't appear to be a valid integer", cmn.SafeEscapeJSONString(config["max_tokens"]))
			return rval, fmt.Errorf("max_tokens '%s' parameter doesn't appear to be a valid integer", cmn.SafeEscapeJSONString(config["max_tokens"]))
		}
	}
	// Check if we have top_p
	if params["top_p"] != nil {
		value, ok := params["top_p"].(string)
		if ok {
			// Check if value needs to be resolved
			value = resolveResponseString(inputRaw, value)
			// Convert value to a float
			valueFloat, err := strconv.ParseFloat(value, 64)
			if err != nil {
				rval[StrStatus] = StatusError
				rval[StrMessage] = fmt.Sprintf("top_p '%s' parameter doesn't appear to be a valid float", cmn.SafeEscapeJSONString(config["top_p"]))
				return rval, fmt.Errorf("top_p '%s' parameter doesn't appear to be a valid float", cmn.SafeEscapeJSONString(config["top_p"]))
			}
			// Top p should be a float value between 0 and 1
			requestBody["top_p"] = valueFloat
		} else {
			rval[StrStatus] = StatusError
			rval[StrMessage] = fmt.Sprintf("top_p '%s' parameter doesn't appear to be a valid float", cmn.SafeEscapeJSONString(config["top_p"]))
			return rval, fmt.Errorf("top_p '%s' parameter doesn't appear to be a valid float", cmn.SafeEscapeJSONString(config["top_p"]))
		}
	}
	// Check if we have presence_penalty
	if params["presence_penalty"] != nil {
		value, ok := params["presence_penalty"].(string)
		if ok {
			// Check if value needs to be resolved
			value = resolveResponseString(inputRaw, value)
			// Convert value to a float
			valueFloat, err := strconv.ParseFloat(value, 64)
			if err != nil {
				rval[StrStatus] = StatusError
				rval[StrMessage] = fmt.Sprintf("presence_penalty '%s' parameter doesn't appear to be a valid float", cmn.SafeEscapeJSONString(config["presence_penalty"]))
				return rval, fmt.Errorf("presence_penalty '%s' parameter doesn't appear to be a valid float", cmn.SafeEscapeJSONString(config["presence_penalty"]))
			}
			// Presence penalty should be a float value between 0 and 1
			requestBody["presence_penalty"] = valueFloat
		} else {
			rval[StrStatus] = StatusError
			rval[StrMessage] = fmt.Sprintf("presence_penalty '%s' parameter doesn't appear to be a valid float", cmn.SafeEscapeJSONString(config["presence_penalty"]))
			return rval, fmt.Errorf("presence_penalty '%s' parameter doesn't appear to be a valid float", cmn.SafeEscapeJSONString(config["presence_penalty"]))
		}
	}
	// Check if we have frequency_penalty
	if params["frequency_penalty"] != nil {
		value, ok := params["frequency_penalty"].(string)
		if ok {
			// Check if value needs to be resolved
			value = resolveResponseString(inputRaw, value)
			// Convert value to a float
			valueFloat, err := strconv.ParseFloat(value, 64)
			if err != nil {
				rval[StrStatus] = StatusError
				rval[StrMessage] = fmt.Sprintf("frequency_penalty '%s' parameter doesn't appear to be a valid float", cmn.SafeEscapeJSONString(config["frequency_penalty"]))
				return rval, fmt.Errorf("frequency_penalty '%s' parameter doesn't appear to be a valid float", cmn.SafeEscapeJSONString(config["frequency_penalty"]))
			}
			// Frequency penalty should be a float value between 0 and 1
			requestBody["frequency_penalty"] = valueFloat
		} else {
			rval[StrStatus] = StatusError
			rval[StrMessage] = fmt.Sprintf("frequency_penalty '%s' parameter doesn't appear to be a valid float", cmn.SafeEscapeJSONString(config["frequency_penalty"]))
			return rval, fmt.Errorf("frequency_penalty '%s' parameter doesn't appear to be a valid float", cmn.SafeEscapeJSONString(config["frequency_penalty"]))
		}
	}
	// Check if we have stop
	if params["stop"] != nil {
		value, ok := params["stop"].(string)
		if ok {
			// Check if value needs to be resolved
			value = resolveResponseString(inputRaw, value)
			// Stop should be a boolean value
			requestBody["stop"] = value
		} else {
			rval[StrStatus] = StatusError
			rval[StrMessage] = fmt.Sprintf("stop '%s' parameter doesn't appear to be a valid boolean", cmn.SafeEscapeJSONString(config["stop"]))
			return rval, fmt.Errorf("stop '%s' parameter doesn't appear to be a valid boolean", cmn.SafeEscapeJSONString(config["stop"]))
		}
	}
	// Check if we have echo
	if params["echo"] != nil {
		value, ok := params["echo"].(string)
		if ok {
			// Check if value needs to be resolved
			value = resolveResponseString(inputRaw, value)
			// Echo should be a boolean value
			requestBody["echo"] = value
		} else {
			rval[StrStatus] = StatusError
			rval[StrMessage] = fmt.Sprintf("echo '%s' parameter doesn't appear to be a valid boolean", cmn.SafeEscapeJSONString(config["echo"]))
			return rval, fmt.Errorf("echo '%s' parameter doesn't appear to be a valid boolean", cmn.SafeEscapeJSONString(config["echo"]))
		}
	}
	// Check if we have logprobs
	if params["logprobs"] != nil {
		value, ok := params["logprobs"].(string)
		if ok {
			// Check if value needs to be resolved
			value = resolveResponseString(inputRaw, value)
			// Logprobs should be an integer value
			requestBody["logprobs"] = value
		} else {
			rval[StrStatus] = StatusError
			rval[StrMessage] = fmt.Sprintf("logprobs '%s' parameter doesn't appear to be a valid integer", cmn.SafeEscapeJSONString(config["logprobs"]))
			return rval, fmt.Errorf("logprobs '%s' parameter doesn't appear to be a valid integer", cmn.SafeEscapeJSONString(config["logprobs"]))
		}
	}
	// Check if we have n
	if params["n"] != nil {
		valid := true
		value, ok := params["n"].(string)
		if ok {
			// Check if value needs to be resolved
			value = resolveResponseString(inputRaw, value)
			// Convert value to an integer
			valueInt, err := strconv.Atoi(value)
			if err == nil {
				// N should be an integer value
				requestBody["n"] = valueInt
			} else {
				valid = false
			}
		} else {
			valid = false
		}
		if !valid {
			rval[StrStatus] = StatusError
			rval[StrMessage] = fmt.Sprintf("n '%s' parameter doesn't appear to be a valid integer", cmn.SafeEscapeJSONString(config["n"]))
			return rval, fmt.Errorf("n '%s' parameter doesn't appear to be a valid integer", cmn.SafeEscapeJSONString(config["n"]))
		}
	}
	// Check if we have stream

	// Check if we have logit_bias
	if params["logit_bias"] != nil {
		valid := true
		value, ok := params["logit_bias"].(string)
		if ok {
			// Check if value needs to be resolved
			value = resolveResponseString(inputRaw, value)
			// Convert value to a float
			valueFloat, err := strconv.ParseFloat(value, 64)
			if err != nil {
				valid = false
			} else {
				// Logit bias should be a float value
				requestBody["logit_bias"] = valueFloat
			}
		} else {
			valid = false
		}
		if !valid {
			rval[StrStatus] = StatusError
			rval[StrMessage] = fmt.Sprintf("logit_bias '%s' parameter doesn't appear to be a valid float", cmn.SafeEscapeJSONString(config["logit_bias"]))
			return rval, fmt.Errorf("logit_bias '%s' parameter doesn't appear to be a valid float", cmn.SafeEscapeJSONString(config["logit_bias"]))
		}
	}

	// Create the request body:
	request["body"] = string(cmn.ConvertMapToJSON(requestBody))

	// Prepare request headers
	requestHeaders := make(map[string]interface{})
	// Add JSON document type
	requestHeaders["Content-Type"] = jsonAppType
	if config["auth"] != nil {
		requestHeaders["Authorization"] = config["auth"].(string)
	}
	request["headers"] = string(cmn.ConvertMapToJSON(requestHeaders))
	//request["type"] = "POST" // AI interactions are usually POST requests

	response, err := cmn.GenericAPIRequest(request)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = fmt.Sprintf("AI interaction failed: %v", err)
		return rval, fmt.Errorf("AI interaction failed: %v", err)
	}

	responseMap, err := cmn.JSONStrToMap(response)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = fmt.Sprintf("failed to parse AI response: %v", err)
		return rval, fmt.Errorf("failed to parse AI response: %v", err)
	}

	rval[StrResponse] = responseMap
	rval[StrStatus] = StatusSuccess
	rval[StrMessage] = "AI interaction successful"

	return rval, nil
}
