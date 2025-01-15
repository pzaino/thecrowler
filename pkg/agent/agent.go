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
	"os/exec"
	"strconv"
	"strings"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	plg "github.com/pzaino/thecrowler/pkg/plugin"
	"github.com/tebeka/selenium"
)

const (
	// ErrMissingConfig is the error message for invalid config format
	ErrMissingConfig = "invalid `config` format or missing config section in parameters section for the step"
	// StatusSuccess is the success status
	StatusSuccess = "success"
	// StatusError is the error status
	StatusError = "error"
	// StatusWarning is the warning status
	StatusWarning = "warning"
	// StrConfig is the string representation of the config field
	StrConfig = "config"
	// StrStatus is the string representation of the status field
	StrStatus = "status"
	// StrMessage is the string representation of the message field
	StrMessage = "message"
	// StrResponse is the string representation of the output field
	StrResponse = "output"
	// StrRequest is the string representation of the input field
	StrRequest = "input"
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
	rval := make(map[string]interface{})
	rval[StrResponse] = nil
	rval[StrConfig] = nil

	// Check if the job is empty
	if len(job) == 0 {
		rval[StrStatus] = StatusError
		rval[StrMessage] = "empty job"
		return rval, fmt.Errorf("empty job")
	}

	// Check if the first step is a config step

	// Execute each step in the job
	lastResult := make(map[string]interface{})

	for _, step := range job {
		actionName, ok := step["action"].(string)
		if !ok {
			return nil, fmt.Errorf("missing 'action' field in job step")
		}
		params, _ := step["params"].(map[string]interface{})

		// Inject previous result into current params (if needed)
		for k, v := range lastResult {
			// Check if k is config
			if k == StrConfig {
				// If it's config, ensure it gets merged correctly with the config in the params
				if params[StrConfig] == nil {
					params[StrConfig] = v
				} else {
					// Merge the two configs
					vMap, ok := v.(map[string]interface{})
					if !ok {
						return nil, fmt.Errorf("invalid config format")
					}
					paramsMap, ok := params[StrConfig].(map[string]interface{})
					if !ok {
						return nil, fmt.Errorf("invalid config format")
					}
					for k2, v2 := range vMap {
						paramsMap[k2] = v2
					}
					params[StrConfig] = paramsMap
				}
				continue
			}

			// Check if the key already exists in the params
			if _, exists := params[k]; exists {
				// Merge the two values
				v2 := []interface{}{v}
				if vArr, ok := params[k].([]interface{}); ok {
					v2 = append(v2, vArr...)
				}
				params[k] = v2
			} else {
				params[k] = v
			}
		}

		action, exists := je.actions[actionName]
		if !exists {
			return nil, fmt.Errorf("unknown action: %s", actionName)
		}

		result, err := action.Execute(params)
		if err != nil {
			return result, fmt.Errorf("action %s failed: %v", actionName, err)
		}

		lastResult = result // Pass the result to the next action
	}

	return lastResult, nil
}

// return job configuration
func getConfig(params map[string]interface{}) (map[string]interface{}, error) {
	if params[StrConfig] == nil {
		return params, nil
	}
	config, ok := params[StrConfig].(map[string]interface{})
	if !ok {
		return nil, errors.New(ErrMissingConfig)
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

	url, ok := config["url"].(string)
	if !ok {
		rval[StrStatus] = StatusError
		rval[StrMessage] = "missing 'url' parameter"
		return rval, fmt.Errorf("missing 'url' parameter")
	}

	if !cmn.IsURLValid(url) {
		rval[StrStatus] = StatusError
		rval[StrMessage] = fmt.Sprintf("invalid URL: %s", url)
		return rval, fmt.Errorf("invalid URL: %s", url)
	}

	response, err := cmn.GenericAPIRequest(map[string]string{
		"url": url,
	})
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

// CreateEventAction creates a database event
type CreateEventAction struct{}

// Name returns the name of the action
func (e *CreateEventAction) Name() string {
	return "CreateEvent"
}

// Execute creates a database event
func (e *CreateEventAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
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

	dbHandler, ok := config["db_handler"].(cdb.Handler)
	if !ok {
		rval[StrStatus] = StatusError
		rval[StrMessage] = "missing 'db_handler' parameter in config section"
		return rval, fmt.Errorf("missing 'db_handler' parameter")
	}

	query, ok := config["query"].(string)
	if !ok {
		rval[StrStatus] = StatusError
		rval[StrMessage] = "missing 'query' parameter in config section"
		return rval, errors.New("missing 'query' parameter")
	}

	result, err := dbHandler.ExecuteQuery(query)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = fmt.Sprintf("failed to execute query: %v", err)
		return rval, fmt.Errorf("failed to execute query: %v", err)
	}

	rval[StrResponse] = result
	rval[StrStatus] = StatusSuccess
	rval[StrMessage] = "event created successfully"

	return rval, nil
}

// RunCommandAction executes local commands
type RunCommandAction struct{}

// Name returns the name of the action
func (r *RunCommandAction) Name() string {
	return "RunCommand"
}

// Execute runs a shell command
func (r *RunCommandAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
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

	command, ok := config["command"].(string)
	if !ok {
		rval[StrStatus] = StatusError
		rval[StrMessage] = "missing 'command' parameter"
		return rval, fmt.Errorf("missing 'command' parameter")
	}

	cmd := exec.Command("sh", "-c", command) //nolint:gosec // This is a controlled command execution
	output, err := cmd.CombinedOutput()
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = fmt.Sprintf("command execution failed: %v", err)
		return rval, fmt.Errorf("command execution failed: %v", err)
	}

	rval[StrResponse] = string(output)
	rval[StrStatus] = StatusSuccess
	rval[StrMessage] = "command executed successfully"

	return rval, nil
}

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

	prompt, ok := config["prompt"].(string)
	if !ok {
		rval[StrStatus] = StatusError
		rval[StrMessage] = "missing 'prompt' parameter"
		return rval, fmt.Errorf("missing 'prompt' parameter")
	}

	response, err := cmn.GenericAPIRequest(map[string]string{
		"url":     config["url"].(string),
		"prompt":  prompt,
		"api_key": config["api_key"].(string),
	})
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

// DBQueryAction performs database queries or operations
type DBQueryAction struct{}

// Name returns the name of the action
func (d *DBQueryAction) Name() string {
	return "DBQuery"
}

// Execute runs a database query or operation
func (d *DBQueryAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
	rval := make(map[string]interface{})
	rval[StrResponse] = nil
	rval[StrConfig] = nil

	// Check if params has a field called config
	config, err := getConfig(params)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, err
	}
	rval[StrConfig] = config

	// Extract dbHandler from config
	dbHandler, ok := config["db_handler"].(cdb.Handler)
	if !ok {
		rval[StrStatus] = StatusError
		rval[StrMessage] = "missing 'dbHandler' in config"
		return rval, fmt.Errorf("missing 'dbHandler' in config")
	}

	// Extract query type (e.g., "select", "insert", "update", etc.)
	queryType, ok := params["type"].(string)
	if !ok {
		rval[StrStatus] = StatusError
		rval[StrMessage] = "missing 'type' parameter for DB operation"
		return rval, fmt.Errorf("missing 'type' parameter for DB operation")
	}

	// Extract query string
	query, ok := params["query"].(string)
	if !ok {
		rval[StrStatus] = StatusError
		rval[StrMessage] = "missing 'query' parameter"
		return rval, fmt.Errorf("missing 'query' parameter")
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
		rval[StrStatus] = StatusError
		rval[StrMessage] = fmt.Sprintf("database operation failed: %v", err)
		return rval, fmt.Errorf("database operation failed: %v", err)
	}

	// Return the result
	rval[StrResponse] = result
	rval[StrStatus] = StatusSuccess
	rval[StrMessage] = "database operation successful"

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
	rval := make(map[string]interface{})
	rval[StrResponse] = nil
	rval[StrConfig] = nil

	// Check if params has a field called config
	config, err := getConfig(params)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, err
	}
	rval[StrConfig] = config

	// Extract the Plugin library pointer from the config
	plugin, ok := config["plugins_register"].(*plg.JSPluginRegister)
	if !ok {
		rval[StrStatus] = StatusError
		rval[StrMessage] = "missing 'pluginRegister' in config section"
		return rval, errors.New("missing 'pluginRegister' in config")
	}

	// Extract plugin's names from params
	plgName, ok := params["plugin_name"].(string)
	if !ok {
		rval[StrStatus] = StatusError
		rval[StrMessage] = "missing 'plugin_name' in parameters section"
		return rval, errors.New("missing 'plugin' parameter")
	}

	// Retrieve the plugin
	plg, exists := plugin.GetPlugin(plgName)
	if !exists {
		rval[StrStatus] = StatusError
		rval[StrMessage] = fmt.Sprintf("plugin '%s' not found", plgName)
		return rval, fmt.Errorf("plugin '%s' not found", plgName)
	}

	// Prepare the plugin parameters
	dbHandler, ok := config["db_handler"].(cdb.Handler)
	if !ok {
		dbHandler = nil
	}
	wd, ok := config["vdi_hook"].(*selenium.WebDriver)
	if !ok {
		wd = nil
	}
	plgParams := make(map[string]interface{})
	if params != nil {
		// Check if we have an event field
		if config["event"] != nil {
			plgParams["event"] = config["event"]
		} else {
			plgParams["event"] = nil
		}
		// Check if params have a response field
		if params[StrResponse] != nil {
			plgParams["jsonData"] = params[StrResponse]
		}
	}

	// Execute the plugin
	pRval, err := plg.Execute(wd, &dbHandler, 30, plgParams)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, fmt.Errorf("executing plugin '%s': %v", plgName, err)
	}

	// Parse the plugin response
	pRvalStr := string(cmn.ConvertMapToJSON(pRval))
	if pRvalStr == "" {
		rval[StrStatus] = StatusError
		rval[StrMessage] = "empty plugin response"
		return rval, fmt.Errorf("empty plugin response")
	}

	// Handle plugin response (e.g., storing or logging results)
	var results []map[string]interface{}
	results = append(results, map[string]interface{}{
		"plugin_name": plgName,
		"response":    pRval,
	})

	// Return the aggregated plugin execution results
	rval[StrResponse] = results
	rval[StrStatus] = StatusSuccess
	rval[StrMessage] = "plugin executed successfully"

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
	rval := make(map[string]interface{})
	rval[StrResponse] = nil
	rval[StrConfig] = nil

	// Check if params has a field called config
	config, err := getConfig(params)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, err
	}
	rval[StrConfig] = config

	condition, ok := params["condition"].(string)
	if !ok {
		rval[StrStatus] = StatusError
		rval[StrMessage] = "missing 'condition' parameter"
		return rval, fmt.Errorf("missing 'condition' parameter")
	}

	if evaluateCondition(condition, params) {
		if steps, ok := params["on_true"].([]map[string]interface{}); ok {
			return AgentsEngine.ExecuteJob(steps)
		}
		rval[StrStatus] = StatusError
		rval[StrMessage] = "missing 'on_true' steps"
		return rval, fmt.Errorf("missing 'on_true' steps")
	}

	if steps, ok := params["on_false"].([]map[string]interface{}); ok {
		return AgentsEngine.ExecuteJob(steps)
	}

	rval[StrStatus] = StatusError
	rval[StrMessage] = "missing 'on_false' steps"
	return rval, fmt.Errorf("missing 'on_false' steps")
}

func evaluateCondition(condition string, params map[string]interface{}) bool {
	// Check which condition to evaluate (agents usually support `if` and `switch` type conditions)
	condition = strings.ToLower(strings.TrimSpace(condition))

	// Check if the condition is a simple `if` condition
	if condition == "if" {
		// Extract the condition to evaluate
		cond, ok := params["expr"].(string)
		if !ok {
			cond, ok = params["expression"].(string)
			if !ok {
				return false
			}
		}

		// Evaluate the condition
		return evaluateIfCondition(cond, params)
	}

	// Check if the condition is a `switch` condition
	if condition == "switch" {
		// Extract the switch condition
		cond, ok := params["expr"].(string)
		if !ok {
			cond, ok = params["expression"].(string)
			if !ok {
				return false
			}
		}

		// Evaluate the switch condition
		return evaluateSwitchCondition(cond, params)
	}

	return params[condition] != nil
}

// evaluateIfCondition evaluates a boolean condition based on the given expression and parameters.
func evaluateIfCondition(expression string, params map[string]interface{}) bool {
	// Parse the expression (basic implementation)
	// Example expressions: "response.success == true", "value > 10", "status == 'active'"
	parts := strings.Fields(expression)

	if len(parts) < 3 {
		fmt.Printf("Invalid if condition: %s\n", expression)
		return false
	}

	leftOperand := parts[0]
	operator := parts[1]
	rightOperand := strings.Join(parts[2:], " ")

	// Get the left operand value from params
	leftValue, exists := params[leftOperand]
	if !exists {
		fmt.Printf("Missing parameter for if condition: %s\n", leftOperand)
		return false
	}

	// Perform the comparison
	switch operator {
	case "==":
		return fmt.Sprintf("%v", leftValue) == strings.Trim(rightOperand, "'\"")
	case "!=":
		return fmt.Sprintf("%v", leftValue) != strings.Trim(rightOperand, "'\"")
	case ">":
		return compareNumeric(leftValue, rightOperand, func(a, b float64) bool { return a > b })
	case "<":
		return compareNumeric(leftValue, rightOperand, func(a, b float64) bool { return a < b })
	case ">=":
		return compareNumeric(leftValue, rightOperand, func(a, b float64) bool { return a >= b })
	case "<=":
		return compareNumeric(leftValue, rightOperand, func(a, b float64) bool { return a <= b })
	default:
		fmt.Printf("Unsupported operator: %s\n", operator)
		return false
	}
}

// evaluateSwitchCondition evaluates a switch-like condition based on the given expression and cases.
func evaluateSwitchCondition(expression string, params map[string]interface{}) bool {
	// Check if the expression exists in the params
	value, exists := params[expression]
	if !exists {
		fmt.Printf("Missing parameter for switch condition: %s\n", expression)
		return false
	}

	// Look for matching cases in params
	cases, ok := params["cases"].(map[string]interface{})
	if !ok {
		fmt.Printf("Invalid 'cases' for switch condition\n")
		return false
	}

	valueStr := fmt.Sprintf("%v", value)
	if _, match := cases[valueStr]; match {
		return true
	}

	// Fallback to 'default' case if defined
	if _, defaultExists := cases["default"]; defaultExists {
		return true
	}

	return false
}

// compareNumeric performs numeric comparison with a custom comparator function.
func compareNumeric(left interface{}, right string, comparator func(a, b float64) bool) bool {
	leftFloat, ok1 := toFloat(left)
	rightFloat, ok2 := toFloat(right)
	if ok1 && ok2 {
		return comparator(leftFloat, rightFloat)
	}
	return false
}

// toFloat attempts to convert a value to a float64.
func toFloat(value interface{}) (float64, bool) {
	switch v := value.(type) {
	case int:
		return float64(v), true
	case float64:
		return v, true
	case string:
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f, true
		}
	}
	return 0, false
}
