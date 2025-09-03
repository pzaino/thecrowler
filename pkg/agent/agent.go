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
	"regexp"
	"strings"

	cmn "github.com/pzaino/thecrowler/pkg/common"
)

const (
	// ErrMissingConfig is the error message for invalid config format
	ErrMissingConfig = "invalid `config` format or missing config section in parameters section for the step"
	// ErrMissingURL is the error message for missing URL parameter
	ErrMissingURL = "missing 'url' parameter"

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
	// StrEvent is the string representation of the event field
	StrEvent = "event"

	// jsonAppType is the application type for JSON
	jsonAppType = "application/json"
)

var responseTokenPattern1 = regexp.MustCompile(`\$response(?:\.[a-zA-Z0-9_]+)+`)
var responseTokenPattern2 = regexp.MustCompile(`{{(.*?)}}`)

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

// Initialize initializes the agent engine
func Initialize() {
	if AgentsEngine == nil {
		AgentsEngine = NewJobEngine() // Ensure `AgentsEngine` is not nil
	}
	RegisterActions(AgentsEngine)
}

// RegisterActions registers all available actions with the engine
func RegisterActions(engine *JobEngine) {
	if engine == nil {
		engine = NewJobEngine()
	}
	engine.RegisterAction(&APIRequestAction{})
	engine.RegisterAction(&CreateEventAction{})
	engine.RegisterAction(&RunCommandAction{})
	engine.RegisterAction(&AIInteractionAction{})
	engine.RegisterAction(&DBQueryAction{})
	engine.RegisterAction(&PluginAction{})
	engine.RegisterAction(&DecisionAction{})
}

// NewJobEngine creates a new job engine
func NewJobEngine() *JobEngine {
	return &JobEngine{}
}

// Initialize registers all actions with the engine
func (je *JobEngine) Initialize() {
	if je == nil {
		je = NewJobEngine()
	}
	// Register actions
	RegisterActions(je)
}

// GetAction returns an action by name
func (je *JobEngine) GetAction(name string) (Action, bool) {
	action, exists := je.actions[name]
	return action, exists
}

// RegisterAction registers a new action with the engine
func (je *JobEngine) RegisterAction(action Action) {
	if je == nil {
		je = NewJobEngine()
	}
	if je.actions == nil {
		je.actions = make(map[string]Action)
	}
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

		// Check if the action exists and is valid
		if je == nil || je.actions == nil || len(je.actions) == 0 {
			return nil, errors.New("no actions registered")
		}

		if je.actions[actionName] == nil {
			return nil, fmt.Errorf("unknown action: %s", actionName)
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
			if config[StrEvent] != nil {
				input["input"] = config[StrEvent]
				return input, nil
			}
		}
		return nil, fmt.Errorf("missing '%s' parameter", StrRequest)
	}
	input[StrRequest] = params[StrRequest]
	return input, nil
}

// ResolveResponseToken takes a token string (e.g. "$response.container.source_id")
// and resolves it using the provided JSON document.
func resolveResponseToken(doc map[string]interface{}, token string) interface{} {
	tokenStr := strings.TrimSpace(token)
	if tokenStr == "" {
		return token
	}

	if tokenStr == "$response" {
		// If token is just "$response", return the whole response document
		return doc
	}

	const prefix = "$response"
	if !strings.HasPrefix(tokenStr, prefix) {
		// not a response token, return as-is
		return token
	}

	// remove the "$response" prefix
	path := strings.TrimPrefix(tokenStr, prefix)
	if path == "" {
		// token was just "$response": return the whole document
		return doc
	}

	// remove a leading dot if present, then split the path into keys
	path = strings.TrimPrefix(path, ".")

	keys := strings.Split(path, ".")
	return cmn.JsonParser(doc, keys...)
}

// resolveResponseString scans an input string for any occurrences of tokens like
// "$response.xxx" and replaces them with the corresponding value from the JSON document.
func resolveResponseString(doc map[string]interface{}, input string) string {
	if doc == nil {
		return input
	}
	inputStr := strings.TrimSpace(input)
	if inputStr == "" {
		return input
	}
	// check if input has {{ and }} and resolve them
	result := responseTokenPattern2.ReplaceAllStringFunc(inputStr, func(token string) string {
		key := strings.Trim(token, "{}")
		key = strings.TrimSpace(key)
		// Check if key is a valid key
		if key == "" {
			return token
		}
		value, _, err := cmn.KVStore.Get(key, "")
		if err != nil {
			return token // Keep original if key is missing
		}
		valueStr, _ := value.(string)
		return valueStr
	})
	// pattern matches "$response" followed by one or more dot-prefixed keys
	matches := responseTokenPattern1.FindAllString(result, -1)
	for _, token := range matches {
		value := resolveResponseToken(doc, token)
		result = strings.ReplaceAll(result, token, fmt.Sprintf("%v", value))
	}
	return result
}

// resolveValue takes an arbitrary value that might be a string containing tokens,
// a map, or a slice (array) and recursively resolves all $response tokens within it.
func resolveValue(doc map[string]interface{}, value interface{}) interface{} {
	switch v := value.(type) {
	case string:
		// Resolve any tokens within the string.
		return resolveResponseString(doc, v)
	case map[string]interface{}:
		// Recursively process each key-value pair.
		for key, val := range v {
			v[key] = resolveValue(doc, val)
		}
		return v
	case []interface{}:
		// Recursively process each element in the slice.
		for i, item := range v {
			v[i] = resolveValue(doc, item)
		}
		return v
	default:
		return v
	}
}

// Action interface for generic actions
type Action interface {
	Name() string
	Execute(params map[string]interface{}) (map[string]interface{}, error)
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
