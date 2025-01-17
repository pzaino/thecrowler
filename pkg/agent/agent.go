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
	"time"

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

	// jsonAppType is the application type for JSON
	jsonAppType = "application/json"
)

// DecisionTrace represents a decision trace for human readable explanations
type DecisionTrace struct {
	Action      string                 `json:"action"`
	Conditions  map[string]bool        `json:"conditions"` // Condition evaluated and result
	Parameters  map[string]interface{} `json:"parameters"`
	Explanation string                 `json:"explanation"` // Human-readable explanation
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

func getInput(params map[string]interface{}) (map[string]interface{}, error) {
	input := make(map[string]interface{})
	if params[StrRequest] == nil {
		// Check if there is an event in "config" instead
		if params[StrConfig] != nil {
			config, ok := params[StrConfig].(map[string]interface{})
			if !ok {
				return input, fmt.Errorf("missing '%s' parameter", StrRequest)
			}
			if config["event"] != nil {
				input["input"] = config["event"]
				return input, nil
			}
		}
		return nil, fmt.Errorf("missing '%s' parameter", StrRequest)
	}
	input[StrRequest] = params[StrRequest]
	return input, nil
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

	url, ok := params["url"].(string)
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

	// Create request object
	request := map[string]string{
		"url": url,
	}

	// Create requestBody
	requestBody, err := getInput(params)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, err
	}
	request["body"] = requestBody[StrRequest].(string)

	// Create RequestHeaders
	requestHeaders := make(map[string]interface{})
	requestHeaders["Content-Type"] = jsonAppType
	if params["auth"] != nil {
		requestHeaders["Authorization"] = config["auth"].(string)
	}
	if params["headers"] != nil {
		headers, ok := params["headers"].(map[string]interface{})
		if ok {
			for k, v := range headers {
				requestHeaders[k] = v
			}
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

	eventRaw, err := getInput(params)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, err
	}

	// Transform eventRaw into an event struct
	event := cdb.Event{}
	event.Details = eventRaw
	if params["type"] != nil {
		event.Type = params["type"].(string)
	}
	if params["source"] != nil {
		event.SourceID = params["source"].(uint64)
	} else {
		event.SourceID = 0
	}
	event.Timestamp = string(time.Now().Format("2006-01-02 15:04:05"))

	result, err := cdb.CreateEvent(&dbHandler, event)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = fmt.Sprintf("failed to create event: %v", err)
		return rval, fmt.Errorf("failed to create event: %v", err)
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

	commandRaw, err := getInput(params)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, err
	}
	command := commandRaw[StrRequest].(string)

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

	promptRaw, err := getInput(params)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, err
	}
	prompt := promptRaw[StrRequest].(string)

	// Generate the API request based on the input and parameters
	request := map[string]string{
		"url": config["url"].(string),
	}

	// Prepare request body
	requestBody := map[string]interface{}{
		"prompt": prompt,
	}
	// Check if we have additional parameters for AI in params like temperature, max_tokens, etc.
	if params["temperature"] != nil {
		// Temperature should be a float value between 0 and 1
		value, ok := params["temperature"].(float64)
		if ok {
			requestBody["temperature"] = value
		} else {
			rval[StrStatus] = StatusError
			rval[StrMessage] = fmt.Sprintf("temperature '%v' parameter doesn't appear to be a valid float", config["temperature"])
			return rval, fmt.Errorf("temperature '%v' parameter doesn't appear to be a valid float", config["temperature"])
		}
	}
	if params["max_tokens"] != nil {
		value, ok := params["max_tokens"].(float64)
		if ok {
			// Max tokens should be an integer value
			requestBody["max_tokens"] = int(value)
		} else {
			rval[StrStatus] = StatusError
			rval[StrMessage] = fmt.Sprintf("max_tokens '%v' parameter doesn't appear to be a valid integer", config["max_tokens"])
			return rval, fmt.Errorf("max_tokens '%v' parameter doesn't appear to be a valid integer", config["max_tokens"])
		}
	}
	request["body"] = string(cmn.ConvertMapToJSON(requestBody))

	// Prepare request headers
	requestHeaders := make(map[string]interface{})
	// Add JSON document type
	requestHeaders["Content-Type"] = jsonAppType
	if config["auth"] != nil {
		requestHeaders["Authorization"] = config["auth"].(string)
	}
	request["headers"] = string(cmn.ConvertMapToJSON(requestHeaders))

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
	queryRaw, err := getInput(params)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, err
	}
	query := queryRaw[StrRequest].(string)

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
	// Check if we have an event field
	// Add event from config if available
	if event, exists := config["event"]; exists {
		plgParams["event"] = event
	} else {
		if event, exists := params["event"]; exists {
			plgParams["event"] = event
		} else {
			plgParams["event"] = nil
		}
	}
	// Check if params have a response field
	if params[StrRequest] != nil {
		plgParams["jsonData"] = params[StrRequest]
	}

	// log plgParams
	cmn.DebugMsg(cmn.DbgLvlDebug5, "Plugin plgParams: %v", plgParams)

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
		cond, ok := params[StrRequest].(string)
		if !ok {
			return false
		}

		// Evaluate the condition
		return evaluateIfCondition(cond, params)
	}

	// Check if the condition is a `switch` condition
	if condition == "switch" {
		// Extract the switch condition
		cond, ok := params[StrRequest].(string)
		if !ok {
			return false
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

/*
// LearningSystem is a system to learn/store from historical data
type LearningSystem struct {
	historyDB cdb.Handler // Database to store and analyze historical data
}

// Learn stores the result of an action in the learning system
// Update learning model with new action outcomes
func (ls *LearningSystem) Learn(action Action, result map[string]interface{}) {
	// Log the result into the database
	// Analyze and update parameters for the action
}
*/
