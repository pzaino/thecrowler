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
	plg "github.com/pzaino/thecrowler/pkg/plugin"
	"github.com/tebeka/selenium"
)

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

// return job configuration
func getConfig(params map[string]interface{}) (map[string]interface{}, error) {
	if params["config"] == nil {
		return params, nil
	}
	config, ok := params["config"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid 'config' format")
	}
	return config, nil
}

// Action interface for generic actions
type Action interface {
	Name() string
	Execute(params map[string]interface{}) (map[string]interface{}, error)
}

// APIRequestAction performs an HTTP API request
type APIRequestAction struct{}

// Name returns the name of the action
func (a *APIRequestAction) Name() string {
	return "APIRequest"
}

// Execute performs the API request
func (a *APIRequestAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
	config, err := getConfig(params)
	if err != nil {
		return nil, err
	}

	url, ok := config["url"].(string)
	if !ok {
		return nil, fmt.Errorf("missing 'url' parameter")
	}

	if !cmn.IsURLValid(url) {
		return nil, fmt.Errorf("invalid URL: %s", url)
	}

	response, err := cmn.GenericAPIRequest(map[string]string{
		"url": url,
	})
	if err != nil {
		return nil, fmt.Errorf("API request failed: %v", err)
	}

	responseMap, err := cmn.JSONStrToMap(response)
	if err != nil {
		return nil, fmt.Errorf("could not parse response: %v", err)
	}

	return map[string]interface{}{
		"response": responseMap,
		"config":   config,
	}, nil
}

// CreateEventAction creates a database event
type CreateEventAction struct{}

// Name returns the name of the action
func (e *CreateEventAction) Name() string {
	return "CreateEvent"
}

// Execute creates a database event
func (e *CreateEventAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
	config, err := getConfig(params)
	if err != nil {
		return nil, err
	}

	dbHandler, ok := config["dbHandler"].(cdb.Handler)
	if !ok {
		return nil, fmt.Errorf("missing 'dbHandler' parameter")
	}

	query, ok := config["query"].(string)
	if !ok {
		return nil, fmt.Errorf("missing 'query' parameter")
	}

	result, err := dbHandler.ExecuteQuery(query)
	if err != nil {
		return nil, fmt.Errorf("failed to execute query: %v", err)
	}

	return map[string]interface{}{
		"response": result,
		"config":   config,
	}, nil
}

// RunCommandAction executes local commands
type RunCommandAction struct{}

// Name returns the name of the action
func (r *RunCommandAction) Name() string {
	return "RunCommand"
}

// Execute runs a shell command
func (r *RunCommandAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
	config, err := getConfig(params)
	if err != nil {
		return nil, err
	}

	command, ok := config["command"].(string)
	if !ok {
		return nil, fmt.Errorf("missing 'command' parameter")
	}

	cmd := exec.Command("sh", "-c", command) //nolint:gosec // This is a controlled command execution
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("command execution failed: %v", err)
	}

	return map[string]interface{}{
		"response": string(output),
		"config":   config,
	}, nil
}

// AIInteractionAction interacts with an AI API
type AIInteractionAction struct{}

// Name returns the name of the action
func (a *AIInteractionAction) Name() string {
	return "AIInteraction"
}

// Execute sends a request to an AI API
func (a *AIInteractionAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
	config, err := getConfig(params)
	if err != nil {
		return nil, err
	}

	prompt, ok := config["prompt"].(string)
	if !ok {
		return nil, fmt.Errorf("missing 'prompt' parameter")
	}

	response, err := cmn.GenericAPIRequest(map[string]string{
		"url":     config["url"].(string),
		"prompt":  prompt,
		"api_key": config["api_key"].(string),
	})
	if err != nil {
		return nil, fmt.Errorf("AI interaction failed: %v", err)
	}

	responseMap, err := cmn.JSONStrToMap(response)
	if err != nil {
		return nil, fmt.Errorf("failed to parse AI response: %v", err)
	}

	return map[string]interface{}{
		"response": responseMap,
		"config":   config,
	}, nil
}

// DBQueryAction performs database queries or operations
type DBQueryAction struct{}

// Name returns the name of the action
func (d *DBQueryAction) Name() string {
	return "DBQuery"
}

// Execute runs a database query or operation
func (d *DBQueryAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
	// Check if params has a field called config
	config, err := getConfig(params)
	if err != nil {
		return nil, err
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

// PluginAction executes plugins
type PluginAction struct{}

// Name returns the name of the action
func (p *PluginAction) Name() string {
	return "PluginExecution"
}

// Execute runs a plugin using the CROWler plugin system
func (p *PluginAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
	// Check if params has a field called config
	config, err := getConfig(params)
	if err != nil {
		return nil, err
	}

	// Extract the Plugin library pointer from the config
	plugin, ok := config["pluginRegister"].(*plg.JSPluginRegister)
	if !ok {
		return nil, fmt.Errorf("missing 'pluginRegister' in config")
	}

	// Extract plugin's names from params
	plgNameList, ok := params["plugin"].([]string)
	if !ok {
		return nil, fmt.Errorf("missing 'plugin' parameter")
	}

	// Execute all retrieved plugins
	var results []map[string]interface{}
	for _, plgName := range plgNameList {
		// Retrieve the plugin
		plg, exists := plugin.GetPlugin(plgName)
		if !exists {
			return nil, fmt.Errorf("plugin '%s' not found", plgName)
		}
		// Prepare the plugin parameters
		dbHandler, ok := config["dbHandler"].(cdb.Handler)
		if !ok {
			dbHandler = nil
		}
		wd, ok := config["vdi_hook"].(*selenium.WebDriver)
		if !ok {
			wd = nil
		}
		plgParams := make(map[string]interface{})
		if params != nil {
			// Check if params have a response field
			if params["response"] != nil {
				plgParams["jsonData"] = params["response"]
			}
		}

		// Execute the plugin
		rval, err := plg.Execute(wd, &dbHandler, 30, plgParams)
		if err != nil {
			return nil, fmt.Errorf("executing plugin '%s': %v", plgName, err)
		}

		// Parse the plugin response
		rvalStr := string(cmn.ConvertMapToJSON(rval))
		if rvalStr == "" {
			return nil, fmt.Errorf("empty plugin response")
		}

		// Handle plugin response (e.g., storing or logging results)
		results = append(results, map[string]interface{}{
			"plugin_name": plgName,
			"response":    rval,
		})
	}

	// Return the aggregated plugin execution results
	rval := make(map[string]interface{})
	rval["response"] = results
	rval["config"] = config

	return rval, nil
}

// DecisionAction makes decisions based on conditions
type DecisionAction struct{}

// Name returns the name of the action
func (d *DecisionAction) Name() string {
	return "Decision"
}

// Execute evaluates conditions and executes steps
func (d *DecisionAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
	condition, ok := params["condition"].(string)
	if !ok {
		return nil, fmt.Errorf("missing 'condition' parameter")
	}

	if evaluateCondition(condition, params) {
		if steps, ok := params["on_true"].([]map[string]interface{}); ok {
			return AgentsEngine.ExecuteJob(steps)
		}
		return nil, fmt.Errorf("missing 'on_true' steps")
	}

	if steps, ok := params["on_false"].([]map[string]interface{}); ok {
		return AgentsEngine.ExecuteJob(steps)
	}
	return nil, fmt.Errorf("missing 'on_false' steps")
}

func evaluateCondition(condition string, params map[string]interface{}) bool {
	return params[condition] != nil
}
