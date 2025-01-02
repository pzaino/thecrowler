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
	"fmt"
	"os/exec"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// Action interface for generic actions
type Action interface {
	Name() string
	Execute(params map[string]interface{}) (map[string]interface{}, error)
}

// APIRequestAction performs an HTTP API request (example action)
type APIRequestAction struct{}

// Name returns the name of the action
func (a *APIRequestAction) Name() string {
	return "APIRequest"
}

// Execute performs the API request
func (a *APIRequestAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
	// Check if params has a field called config
	var config map[string]interface{}
	if params["config"] == nil {
		config = params
	} else {
		config = params["config"].(map[string]interface{})
	}

	// Extract URL from params
	url, ok := config["url"].(string)
	if !ok {
		return nil, fmt.Errorf("missing 'url' parameter")
	}
	// Check if the URL is valid
	if !cmn.IsURLValid(url) {
		return nil, fmt.Errorf("invalid URL: %s", url)
	}

	// Transform config into a map[string]string
	var pars = make(map[string]string)
	for k, v := range config {
		if k != "url" {
			pars[k] = v.(string)
		}
		if k == "headers" {
			// Extract headers into a map[string]string
			headers, err := cmn.JSONStrToMap(v.(string))
			if err != nil {
				return nil, fmt.Errorf("could not parse headers: %v", err)
			}
			for hk, hv := range headers {
				pars[hk] = hv.(string)
			}
		}
		if k == "auth" {
			// Extract auth into a map[string]string
			auth, err := cmn.JSONStrToMap(v.(string))
			if err != nil {
				return nil, fmt.Errorf("could not parse auth: %v", err)
			}
			for ak, av := range auth {
				pars[ak] = av.(string)
			}
		}
		if k == "body" {
			// Extract body into a map[string]string
			body, err := cmn.JSONStrToMap(v.(string))
			if err != nil {
				return nil, fmt.Errorf("could not parse body: %v", err)
			}
			for bk, bv := range body {
				pars[bk] = bv.(string)
			}
		}
	}

	// Prepare the API request
	response, err := cmn.GenericAPIRequest(pars)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %v", err)
	}

	// Transform response into a map[string]interface{}
	responseMap, err := cmn.JSONStrToMap(response)
	if err != nil {
		return nil, fmt.Errorf("could not parse response: %v", err)
	}

	// Create the response object
	rval := make(map[string]interface{})
	rval["response"] = responseMap

	// Augment responseMap with the URL and the dbHandler
	rval["config"] = config

	return rval, nil
}

// CreateEventAction simulates creating an event
type CreateEventAction struct{}

// Name returns the name of the action
func (e *CreateEventAction) Name() string {
	return "CreateEvent"
}

// Execute creates an event
func (e *CreateEventAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
	// Check if params has a field called name config
	var config map[string]interface{}
	if params["config"] == nil {
		config = params
	} else {
		config = params["config"].(map[string]interface{})
	}

	// Check if config has a file called source_id, if so use it to populate srcID
	var srcID uint64
	if val, ok := config["source_id"]; ok {
		// Check if source_id is an integer
		if val.(uint64) > 0 {
			srcID = val.(uint64)
		} else {
			return nil, fmt.Errorf("source_id must be an integer")
		}
	}

	// Check if config has a field called details, if not set it to an empty string
	detailsStr, ok := config["details"].(string)
	if !ok {
		detailsStr = "{}"
	}

	// Extracts detailsStr and transform it into a map[string]interface{}
	details, err := cmn.JSONStrToMap(detailsStr)
	if err != nil {
		return nil, fmt.Errorf("could not parse details: %v", err)
	}

	// Extract config["type"] into a string
	eType, ok := config["type"].(string)
	if !ok {
		eType = "generic"
	}

	// extract dbHandler from params
	dbHandler, ok := config["dbHandler"].(cdb.Handler)
	if !ok {
		return nil, fmt.Errorf("missing 'dbHandler' parameter")
	}

	// transform params into a cdb.Event
	event := cdb.Event{
		SourceID: srcID,
		Type:     eType,
		Details:  details,
	}

	// Insert the event into the database
	uid, err := cdb.CreateEvent(&dbHandler, event)
	if err != nil {
		return nil, fmt.Errorf("could not create event: %v", err)
	}

	// Return a map[string]interface{} with uid, the event and the dbHandler
	response := map[string]interface{}{}
	response["response"] = map[string]interface{}{
		"uid":   uid,
		"event": event,
	}
	response["config"] = config

	return response, nil
}

// JobEngine executes a sequence of actions
type JobEngine struct {
	actions map[string]Action
}

// NewJobEngine creates a new job engine
func NewJobEngine() *JobEngine {
	return &JobEngine{
		actions: make(map[string]Action),
	}
}

// RegisterAction registers a new action with the engine
func (je *JobEngine) RegisterAction(action Action) {
	je.actions[action.Name()] = action
}

// ExecuteJob runs a sequence of actions as defined in the job
func (je *JobEngine) ExecuteJob(job []map[string]interface{}) (map[string]interface{}, error) {
	lastResult := make(map[string]interface{})

	for _, step := range job {
		actionName, ok := step["action"].(string)
		if !ok {
			return nil, fmt.Errorf("missing 'action' field in job step")
		}
		params, _ := step["params"].(map[string]interface{})

		// Inject previous result into current params (if needed)
		for k, v := range lastResult {
			params[k] = v
		}

		action, exists := je.actions[actionName]
		if !exists {
			return nil, fmt.Errorf("unknown action: %s", actionName)
		}

		result, err := action.Execute(params)
		if err != nil {
			return nil, fmt.Errorf("action %s failed: %v", actionName, err)
		}

		lastResult = result // Pass the result to the next action
	}

	return lastResult, nil
}

// RunCommandAction executes local shell commands
type RunCommandAction struct{}

// Name returns the name of the action
func (r *RunCommandAction) Name() string {
	return "RunCommand"
}

// Execute runs a local shell command
func (r *RunCommandAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
	// Check if params has a field called config
	var config map[string]interface{}
	if params["config"] == nil {
		config = params
	} else {
		config = params["config"].(map[string]interface{})
	}

	// Extract command from params
	command, ok := params["command"].(string)
	if !ok {
		return nil, fmt.Errorf("missing 'command' parameter")
	}

	// Execute the command
	cmd := exec.Command("sh", "-c", command) //nolint:gosec // This command is not created by an end-user, is instead provided by the agent
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("command execution failed: %v", err)
	}

	// Create the rval map
	rval := make(map[string]interface{})
	rval["response"] = string(output)
	rval["config"] = config

	// Return the command output
	return rval, nil
}

// AIInteractionAction interacts with an AI API using the GenericAPIRequest utility
type AIInteractionAction struct{}

// Name returns the name of the action
func (a *AIInteractionAction) Name() string {
	return "AIInteraction"
}

// Execute sends a prompt to the AI API and processes the response
func (a *AIInteractionAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
	// Check if params has a field called config
	var config map[string]interface{}
	if params["config"] == nil {
		config = params
	} else {
		config = params["config"].(map[string]interface{})
	}

	// Extract required parameters
	prompt, ok := params["prompt"].(string)
	if !ok {
		return nil, fmt.Errorf("missing 'prompt' parameter")
	}

	// Build the API request configuration
	headers := map[string]string{
		"Authorization": "Bearer " + config["api_key"].(string),
		"Content-Type":  "application/json",
	}

	// Extract max_tokens and temperature from params
	maxTokens, ok := params["max_tokens"].(string)
	if !ok {
		maxTokens = "150"
	}

	temperature, ok := params["temperature"].(string)
	if !ok {
		temperature = "0.7"
	}

	body := map[string]string{
		"prompt":      prompt,
		"max_tokens":  maxTokens,
		"temperature": temperature,
	}

	// Convert body to JSON string using your existing utility
	bodyStr, err := cmn.MapStrToJSONStr(body)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize body: %v", err)
	}

	headersStr, err := cmn.MapStrToJSONStr(headers)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize headers: %v", err)
	}

	// Prepare the API request parameters
	apiRequest := map[string]string{
		"url":     config["url"].(string),
		"method":  "POST",
		"headers": headersStr,
		"body":    bodyStr,
	}

	// Execute the API request
	response, err := cmn.GenericAPIRequest(apiRequest)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %v", err)
	}

	// Parse the response into a map
	responseMap, err := cmn.JSONStrToMap(response)
	if err != nil {
		return nil, fmt.Errorf("failed to parse response: %v", err)
	}

	// Create the rval map
	rval := make(map[string]interface{})
	rval["response"] = responseMap
	rval["config"] = config

	// Return the AI-generated text
	return rval, nil
}

// DBQueryAction performs database queries or operations
type DBQueryAction struct{}

// Name returns the name of the action
func (d *DBQueryAction) Name() string {
	return "DBQuery"
}

// Execute runs a database query or operation
func (d *DBQueryAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
	// Extract config from params
	var config map[string]interface{}
	if params["config"] == nil {
		config = params
	} else {
		config = params["config"].(map[string]interface{})
	}

	// Extract dbHandler from config
	dbHandler, ok := config["dbHandler"].(cdb.Handler)
	if !ok {
		return nil, fmt.Errorf("missing 'dbHandler' in config")
	}

	// Extract query type (e.g., "select", "insert", "update", etc.)
	queryType, ok := params["type"].(string)
	if !ok {
		return nil, fmt.Errorf("missing 'type' parameter for DB operation")
	}

	// Extract query string
	query, ok := params["query"].(string)
	if !ok {
		return nil, fmt.Errorf("missing 'query' parameter")
	}

	// Execute the query based on type
	var result interface{}
	var err error
	switch queryType {
	case "select":
		// Assume query returns rows
		result, err = dbHandler.ExecuteQuery(query)
	case "insert":
		result, err = dbHandler.ExecuteQuery(query)
	case "update":
		result, err = dbHandler.ExecuteQuery(query)
	case "delete":
		result, err = dbHandler.ExecuteQuery(query)
	default:
		return nil, fmt.Errorf("unsupported query type: %s", queryType)
	}

	if err != nil {
		return nil, fmt.Errorf("database operation failed: %v", err)
	}

	// Return the result
	rval := make(map[string]interface{})
	rval["response"] = result
	rval["config"] = config

	return rval, nil
}

/*
func main() {
	// Initialize the engine
	engine := NewJobEngine()

	// Register actions
	engine.RegisterAction(&APIRequestAction{})
	engine.RegisterAction(&CreateEventAction{})

	// Define a job (could be read from a JSON or YAML config)
	job := []map[string]interface{}{
		{
			"action": "APIRequest",
			"params": map[string]string{
				"url": "http://example.com",
			},
		},
		{
			"action": "CreateEvent",
			"params": map[string]string{
				"name": "API Response Event",
			},
		},
	}

	// Execute the job
	result, err := engine.ExecuteJob(job)
	if err != nil {
		log.Fatalf("Error executing job: %v", err)
	}

	fmt.Println("Final Result:", result)
}
*/
