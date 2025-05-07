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
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	plg "github.com/pzaino/thecrowler/pkg/plugin"
	vdi "github.com/pzaino/thecrowler/pkg/vdi"

	"github.com/Knetic/govaluate"
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
	if params["type"] != nil {
		request["type"] = params["type"].(string)
		request["type"] = strings.ToUpper(strings.TrimSpace(resolveResponseString(inputMap, request["type"])))
	} else {
		request["type"] = "GET"
	}

	// Create requestBody (if the request is a POST or PUT)
	if request["type"] == postType || request["type"] == putType {
		requestBody := ""
		rawBody, _ := params["body"].(map[string]interface{})
		if rawBody != nil {
			// Check if there are any $response.xxx tokens in rawBody
			requestBody, _ = resolveValue(inputMap, rawBody).(string)
			if requestBody == "" {
				// Check if this is a "POST" or "PUT" request, if so generate an error
				// if the body is missing
				if request["type"] == postType || request["type"] == putType {
					rval[StrStatus] = StatusError
					rval[StrMessage] = "missing 'input' parameter"
					return rval, fmt.Errorf("missing 'input' parameter")
				}
			}
		}
		request["body"] = requestBody
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
	eventMap, _ := eventRaw[StrRequest].(map[string]interface{})

	// Transform eventRaw into an event struct
	event := cdb.Event{}
	event.Details = eventRaw
	if params["event_type"] != nil {
		eType, _ := params["event_type"].(string)
		eType = strings.TrimSpace(resolveResponseString(eventMap, eType))
		event.Type = eType
	}
	if params["source"] != nil {
		eSource, _ := params["source"].(string)
		eSource = resolveResponseString(eventMap, eSource)
		eSourceInt, err := strconv.ParseUint(eSource, 10, 64)
		if err != nil {
			rval[StrStatus] = StatusError
			rval[StrMessage] = fmt.Sprintf("invalid source ID: %v", err)
			return rval, err
		}
		event.SourceID = eSourceInt
	} else {
		event.SourceID = 0
	}
	// Check if there is a timestamp params
	if params["timestamp"] != nil {
		eTimestamp, _ := params["timestamp"].(string)
		eTimestamp = resolveResponseString(eventMap, eTimestamp)
		// convert eTimestamp to a time.Time
		t, err := time.Parse("2006-01-02 15:04:05", eTimestamp)
		if err != nil {
			rval[StrStatus] = StatusError
			rval[StrMessage] = fmt.Sprintf("invalid timestamp: %v", err)
			return rval, err
		}
		event.Timestamp = t.Format("2006-01-02 15:04:05")
	} else {
		event.Timestamp = string(time.Now().Format("2006-01-02 15:04:05"))
	}
	// Check if there is a Severity params
	if params["severity"] != nil {
		eSeverity, _ := params["severity"].(string)
		eSeverity = strings.ToUpper(strings.TrimSpace(resolveResponseString(eventMap, eSeverity)))
		event.Severity = eSeverity
	} else {
		event.Severity = "MEDIUM"
	}
	// Check if there is a Details params
	if params["details"] != nil {
		eDetails, _ := params["details"].(map[string]interface{})
		eDetailsProcessed := resolveValue(eventMap, eDetails)
		event.Details, _ = eDetailsProcessed.(map[string]interface{})
	}

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

func executeIsolatedCommand(command string, args []string, chrootDir string, uid, gid int) (string, error) {
	// Prepare attributes for syscall.ForkExec
	attr := &syscall.ProcAttr{
		Env:   []string{"PATH=/usr/bin:/bin"},
		Files: []uintptr{os.Stdin.Fd(), os.Stdout.Fd(), os.Stderr.Fd()},
	}

	// If chrootDir is specified, set it
	if chrootDir != "" {
		attr.Dir = "/"
		err := syscall.Chroot(chrootDir)
		if err != nil {
			return "", fmt.Errorf("failed to chroot to %s: %w", chrootDir, err)
		}
		cmn.DebugMsg(cmn.DbgLvlDebug2, "Chrooted to: %s", chrootDir)
	}

	// Drop privileges if UID and GID are specified
	if uid != 0 {
		err := syscall.Setuid(uid)
		if err != nil {
			return "", fmt.Errorf("failed to set UID: %w", err)
		}
	}
	if gid != 0 {
		err := syscall.Setgid(gid)
		if err != nil {
			return "", fmt.Errorf("failed to set GID: %w", err)
		}
	}
	if uid != 0 || gid != 0 {
		cmn.DebugMsg(cmn.DbgLvlDebug2, "Privileges dropped to UID: %d, GID: %d", uid, gid)
	}

	fmt.Printf("Executing command: %s %v\n", command, args)

	// Execute the command
	pid, err := syscall.ForkExec(command, args, attr) //nolint:gosec // This is a controlled environment
	if err != nil {
		return "", fmt.Errorf("failed to execute command: %w", err)
	}

	// Wait for the process to finish
	var ws syscall.WaitStatus
	_, err = syscall.Wait4(pid, &ws, 0, nil)
	if err != nil {
		return "", fmt.Errorf("failed to wait for process: %w", err)
	}

	// Check exit status
	if ws.ExitStatus() != 0 {
		return "", fmt.Errorf("command exited with status %d", ws.ExitStatus())
	}

	return "Command executed successfully", nil
}

// Execute runs a command in ch-rooted and/or with dropped privileges
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
	// Check if the command is a direct command or a command map
	if commandRaw[StrRequest] == nil {
		// Check if there is a "command" in params
		if params["command"] == nil {
			rval[StrStatus] = StatusError
			rval[StrMessage] = "missing 'command' parameter"
			return rval, fmt.Errorf("missing 'command' parameter")
		}
		commandRaw[StrRequest] = params["command"]
	}
	// Check if commandRaw has an input that is a string or a map
	cmdStr := ""
	commandMap := make(map[string]interface{})
	_, ok := commandRaw[StrRequest].(string)
	if !ok {
		// Check if it's a map
		_, ok = commandRaw[StrRequest].(map[string]interface{})
		if !ok {
			rval[StrStatus] = StatusError
			rval[StrMessage] = "invalid command format"
			return rval, fmt.Errorf("invalid command format")
		}
		commandMap = commandRaw[StrRequest].(map[string]interface{})
	} else {
		// Must be a direct command
		cmdStr = commandRaw[StrRequest].(string)
		commandMap["command"] = cmdStr
	}
	// Check if cmdStr needs to be resolved
	cmdStr = resolveResponseString(commandMap, cmdStr)

	command := cmdStr
	args := strings.Fields(command)
	if len(args) == 0 {
		rval[StrStatus] = StatusError
		rval[StrMessage] = "empty command"
		return rval, fmt.Errorf("empty command")
	}

	argsList := []string{}
	argsList = append(argsList, "")
	argsList = append(argsList, strings.Join(args[1:], " "))

	// Retrieve chrootDir and privileges from parameters
	chrootDir := ""
	if params["chroot_dir"] != nil {
		tChrootDir, ok := params["chroot_dir"].(string)
		if ok {
			// Check if tChrootDir needs to be resolved
			chrootDir = resolveResponseString(commandMap, tChrootDir)
		}
	}
	// Check if we have UID and GID
	uid := 0
	gid := 0
	if params["uid"] != nil {
		tUID, ok := params["uid"].(string)
		if ok {
			// Check if tUID needs to be resolved
			tUID = resolveResponseString(commandMap, tUID)
		}
		// Convert tUID to an integer
		uid, err = strconv.Atoi(tUID)
		if err != nil {
			rval[StrStatus] = StatusError
			rval[StrMessage] = fmt.Sprintf("invalid UID: %v", err)
			return rval, err
		}
	}
	if params["gid"] != nil {
		tGID, ok := params["gid"].(string)
		if ok {
			// Check if tGID needs to be resolved
			tGID = resolveResponseString(commandMap, tGID)
		}
		// Convert tGID to an integer
		gid, err = strconv.Atoi(tGID)
		if err != nil {
			rval[StrStatus] = StatusError
			rval[StrMessage] = fmt.Sprintf("invalid GID: %v", err)
			return rval, err
		}
	}

	// Log execution
	cmn.DebugMsg(cmn.DbgLvlDebug2, "Executing command: %s %v", args[0], argsList)

	// Execute the command in isolation
	output, err := executeIsolatedCommand(args[0], argsList, chrootDir, uid, gid)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = fmt.Sprintf("command execution failed: %v", err)
		return rval, err
	}

	rval[StrResponse] = output
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

	inputRaw, err := getInput(params)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, err
	}

	// Combine the prompt with the request
	var prompt string
	var promptType string
	if params["prompt"] != nil {
		prompt = params["prompt"].(string)
		// Check if prompt needs to be resolved
		prompt = resolveResponseString(inputRaw, prompt)
		promptType = "prompt"
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
			promptType = StrMessage
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
	// Check if we have a prompt or a message
	if promptType == StrMessage {
		requestBody[StrMessage] = prompt
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
			rval[StrMessage] = fmt.Sprintf("temperature '%v' parameter doesn't appear to be a valid float", cmn.SafeEscapeJSONString(config["temperature"]))
			return rval, fmt.Errorf("temperature '%v' parameter doesn't appear to be a valid float", cmn.SafeEscapeJSONString(config["temperature"]))
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
			rval[StrMessage] = fmt.Sprintf("max_tokens '%v' parameter doesn't appear to be a valid integer", cmn.SafeEscapeJSONString(config["max_tokens"]))
			return rval, fmt.Errorf("max_tokens '%v' parameter doesn't appear to be a valid integer", cmn.SafeEscapeJSONString(config["max_tokens"]))
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
				rval[StrMessage] = fmt.Sprintf("top_p '%v' parameter doesn't appear to be a valid float", cmn.SafeEscapeJSONString(config["top_p"]))
				return rval, fmt.Errorf("top_p '%v' parameter doesn't appear to be a valid float", cmn.SafeEscapeJSONString(config["top_p"]))
			}
			// Top p should be a float value between 0 and 1
			requestBody["top_p"] = valueFloat
		} else {
			rval[StrStatus] = StatusError
			rval[StrMessage] = fmt.Sprintf("top_p '%v' parameter doesn't appear to be a valid float", cmn.SafeEscapeJSONString(config["top_p"]))
			return rval, fmt.Errorf("top_p '%v' parameter doesn't appear to be a valid float", config["top_p"])
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
				rval[StrMessage] = fmt.Sprintf("presence_penalty '%v' parameter doesn't appear to be a valid float", cmn.SafeEscapeJSONString(config["presence_penalty"]))
				return rval, fmt.Errorf("presence_penalty '%v' parameter doesn't appear to be a valid float", cmn.SafeEscapeJSONString(config["presence_penalty"]))
			}
			// Presence penalty should be a float value between 0 and 1
			requestBody["presence_penalty"] = valueFloat
		} else {
			rval[StrStatus] = StatusError
			rval[StrMessage] = fmt.Sprintf("presence_penalty '%v' parameter doesn't appear to be a valid float", cmn.SafeEscapeJSONString(config["presence_penalty"]))
			return rval, fmt.Errorf("presence_penalty '%v' parameter doesn't appear to be a valid float", cmn.SafeEscapeJSONString(config["presence_penalty"]))
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
				rval[StrMessage] = fmt.Sprintf("frequency_penalty '%v' parameter doesn't appear to be a valid float", cmn.SafeEscapeJSONString(config["frequency_penalty"]))
				return rval, fmt.Errorf("frequency_penalty '%v' parameter doesn't appear to be a valid float", cmn.SafeEscapeJSONString(config["frequency_penalty"]))
			}
			// Frequency penalty should be a float value between 0 and 1
			requestBody["frequency_penalty"] = valueFloat
		} else {
			rval[StrStatus] = StatusError
			rval[StrMessage] = fmt.Sprintf("frequency_penalty '%v' parameter doesn't appear to be a valid float", cmn.SafeEscapeJSONString(config["frequency_penalty"]))
			return rval, fmt.Errorf("frequency_penalty '%v' parameter doesn't appear to be a valid float", cmn.SafeEscapeJSONString(config["frequency_penalty"]))
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
			rval[StrMessage] = fmt.Sprintf("stop '%v' parameter doesn't appear to be a valid boolean", cmn.SafeEscapeJSONString(config["stop"]))
			return rval, fmt.Errorf("stop '%v' parameter doesn't appear to be a valid boolean", cmn.SafeEscapeJSONString(config["stop"]))
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
			rval[StrMessage] = fmt.Sprintf("echo '%v' parameter doesn't appear to be a valid boolean", cmn.SafeEscapeJSONString(config["echo"]))
			return rval, fmt.Errorf("echo '%v' parameter doesn't appear to be a valid boolean", cmn.SafeEscapeJSONString(config["echo"]))
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
			rval[StrMessage] = fmt.Sprintf("logprobs '%v' parameter doesn't appear to be a valid integer", cmn.SafeEscapeJSONString(config["logprobs"]))
			return rval, fmt.Errorf("logprobs '%v' parameter doesn't appear to be a valid integer", cmn.SafeEscapeJSONString(config["logprobs"]))
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
			rval[StrMessage] = fmt.Sprintf("n '%v' parameter doesn't appear to be a valid integer", cmn.SafeEscapeJSONString(config["n"]))
			return rval, fmt.Errorf("n '%v' parameter doesn't appear to be a valid integer", cmn.SafeEscapeJSONString(config["n"]))
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
			rval[StrMessage] = fmt.Sprintf("logit_bias '%v' parameter doesn't appear to be a valid float", cmn.SafeEscapeJSONString(config["logit_bias"]))
			return rval, fmt.Errorf("logit_bias '%v' parameter doesn't appear to be a valid float", cmn.SafeEscapeJSONString(config["logit_bias"]))
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

	// Extract query string
	inputRaw, err := getInput(params)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, err
	}

	// Check if there is a params field called query
	if params["query"] == nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = "missing 'query' parameter"
		return rval, fmt.Errorf("missing 'query' parameter")
	}
	query, _ := params["query"].(string)
	// Check if query needs to be resolved
	query = resolveResponseString(inputRaw, query)

	// Execute the query based on type
	var result interface{}
	result, err = dbHandler.ExecuteQuery(query)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = fmt.Sprintf("database operation failed: %v", err)
		return rval, fmt.Errorf("database operation failed: %v", err)
	}

	// Transform the result into a JSON document where the headers are they keys
	// and the values are the values
	resultMap := cmn.ConvertMapToJSON(cmn.ConvertInfToMap(result)) // This converts result into a map[string]interface{} and then into a JSON document

	// Return the result
	rval[StrResponse] = resultMap
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
	plugins, ok := config["plugins_register"].(*plg.JSPluginRegister)
	if !ok {
		rval[StrStatus] = StatusError
		rval[StrMessage] = "missing 'pluginRegister' in config section"
		return rval, errors.New("missing 'pluginRegister' in config")
	}

	// Recover previous step response
	inputRaw, err := getInput(params)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = err.Error()
		return rval, err
	}

	// Extract plugin's names from params
	plgName, ok := params["plugin_name"].(string)
	if !ok {
		rval[StrStatus] = StatusError
		rval[StrMessage] = "missing 'plugin_name' in parameters section"
		return rval, errors.New("missing 'plugin' parameter")
	}
	// Check if plgName needs to be resolved
	plgName = strings.TrimSpace(resolveResponseString(inputRaw, plgName))
	if plgName == "" {
		rval[StrStatus] = StatusError
		rval[StrMessage] = "empty plugin name"
		return rval, fmt.Errorf("empty plugin name")
	}

	// Retrieve the plugin
	plg, exists := plugins.GetPlugin(plgName)
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
	wd, ok := config["vdi_hook"].(*vdi.WebDriver)
	if !ok {
		wd = nil
	}
	plgParams := make(map[string]interface{})
	// Check if we have an event field
	// Add event from config if available
	if event, exists := config[StrEvent]; exists {
		plgParams[StrEvent] = event
	} else {
		if event, exists := params[StrEvent]; exists {
			plgParams[StrEvent] = event
		} else {
			plgParams[StrEvent] = nil
		}
	}
	// Add meta_data from config if available
	if meta, exists := config["meta_data"]; exists {
		plgParams["meta_data"] = meta
	} else {
		if meta, exists := params["meta_data"]; exists {
			plgParams["meta_data"] = meta
		} else {
			plgParams["meta_data"] = nil
		}
	}
	// Collect custom params
	for k, v := range params {
		if k != "plugin_name" &&
			k != StrEvent &&
			k != "meta_data" &&
			k != "config" &&
			k != "vdi_hook" &&
			k != "db_handler" {
			// Check if the value needs to be resolved
			// To do that, first check which type of value it is
			// If it's a string, resolve it
			// If it's a map, resolve the values
			if _, ok := v.(string); ok {
				plgParams[k] = resolveResponseString(inputRaw, v.(string))
			} else {
				// Check if it's a map
				if _, ok := v.(map[string]interface{}); ok {
					plgParams[k] = resolveValue(inputRaw, v)
				} else {
					plgParams[k] = v
				}
			}
		}
	}
	// Check if params have a response field
	if params[StrRequest] != nil {
		plgParams["json_data"] = params[StrRequest]
	}

	// log plgParams
	cmn.DebugMsg(cmn.DbgLvlDebug5, "Plugin plgParams: %v", plgParams)

	// Execute the plugin
	pRval, err := plg.Execute(wd, &dbHandler, 150, plgParams)
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

	// Extract previous step response
	inputRaw, _ := getInput(params)

	condition, ok := params["condition"].(map[string]interface{})
	if !ok {
		rval[StrStatus] = StatusError
		rval[StrMessage] = "missing 'condition' parameter"
		return rval, fmt.Errorf("missing 'condition' parameter")
	}

	result, err := evaluateCondition(condition, params, inputRaw)
	if err != nil {
		rval[StrStatus] = StatusError
		rval[StrMessage] = fmt.Sprintf("failed to evaluate condition: %v", err)
		return rval, err
	}
	var nextStep map[string]interface{}
	// Check if result is a boolean
	resultBool, ok := result.(bool)
	if !ok {
		// result is not a boolean, so it's a set of steps
		nextStep, ok = result.(map[string]interface{})
		if !ok {
			rval[StrStatus] = StatusError
			rval[StrMessage] = "invalid result from condition evaluation"
			return rval, fmt.Errorf("invalid result from condition evaluation")
		}
	} else {
		if resultBool {
			nextStep, ok = condition["on_true"].(map[string]interface{})
			if !ok {
				fmt.Printf("condition: %v\n", condition)
				rval[StrStatus] = StatusError
				rval[StrMessage] = "missing 'on_true' step"
				return rval, fmt.Errorf("missing 'on_true' step")
			}
		} else {
			nextStep, ok = condition["on_false"].(map[string]interface{})
			if !ok {
				fmt.Printf("condition: %v\n", condition)
				rval[StrStatus] = StatusError
				rval[StrMessage] = "missing 'on_false' step"
				return rval, fmt.Errorf("missing 'on_false' step")
			}
		}
	}

	var results map[string]interface{}
	if nextStep != nil {
		// extract the call_agent from the nextStep
		agentName, ok := nextStep["call_agent"].(string)
		if !ok {
			rval[StrStatus] = StatusError
			rval[StrMessage] = "missing 'call_agent' in next step"
			return rval, fmt.Errorf("missing 'call_agent' in next step")
		}
		// Check if agentName needs to be resolved
		agentName = resolveResponseString(inputRaw, agentName)

		// Retrieve the agent
		agent, exists := AgentsEngine.GetAgentByName(agentName)
		if !exists {
			rval[StrStatus] = StatusError
			rval[StrMessage] = fmt.Sprintf("agent '%s' not found", cmn.SafeEscapeJSONString(agentName))
			return rval, fmt.Errorf("agent '%s' not found", cmn.SafeEscapeJSONString(agentName))
		}

		err = AgentsEngine.ExecuteJobs(agent, params)
		if err != nil {
			rval[StrStatus] = StatusError
			rval[StrMessage] = fmt.Sprintf("failed to execute steps: %v", err)
			return rval, err
		}
	}

	rval[StrResponse] = results
	rval[StrStatus] = StatusSuccess
	rval[StrMessage] = "decision executed successfully"

	return rval, nil
}

func evaluateCondition(condition, params, rawInput map[string]interface{}) (interface{}, error) {
	// Check which condition to evaluate (agents usually support `if` and `switch` type conditions)
	conditionType, _ := condition["condition_type"].(string)
	conditionType = strings.ToLower(strings.TrimSpace(conditionType))

	// Check if the condition is a simple `if` condition
	if conditionType == "if" {
		// Extract the condition to evaluate
		// This should be a string expression like "$response.success == true && ($response.status == 'active' || $response.value > 10)"
		expr, ok := condition["expression"].(string)
		if !ok {
			return false, fmt.Errorf("missing 'expression' in condition")
		}

		// Evaluate the condition
		return evaluateIfCondition(expr, rawInput)
	}

	// Check if the condition is a `switch` condition
	if conditionType == "switch" {
		// Extract the switch condition
		expr, ok := params["expression"].(string)
		if !ok {
			return false, fmt.Errorf("missing 'expression' in condition")
		}

		// Also, check if the switch many cases needs to be resolved
		// This should be an array of cases like {"1": "case1", "2": "case2", "default": "default"}
		rawCases, ok := condition["cases"].(map[string]interface{})
		if !ok {
			return false, fmt.Errorf("missing 'cases' in condition")
		}

		// Evaluate the switch condition
		return evaluateSwitchCondition(expr, rawCases, rawInput)
	}

	return false, fmt.Errorf("unsupported condition type: %s", conditionType)
}

// evaluateIfCondition evaluates a boolean condition based on the given expression and parameters.
func evaluateIfCondition(expression string, rawInput map[string]interface{}) (bool, error) {
	// Check if expr needs to be resolved
	expression = resolveResponseString(rawInput, expression)

	// Wrap string values in single quotes
	expression = wrapStrings(expression)

	parsedExpr, err := govaluate.NewEvaluableExpression(expression)
	if err != nil {
		return false, fmt.Errorf("invalid expression: %s", expression)
	}

	// Step 4: Evaluate the expression
	result, err := parsedExpr.Evaluate(nil) // No need for additional parameters
	if err != nil {
		return false, fmt.Errorf("error evaluating expression: %v", err)
	}

	// Step 5: Ensure the result is a boolean
	booleanResult, ok := result.(bool)
	if !ok {
		return false, fmt.Errorf("expression did not return a boolean: %v", result)
	}

	return booleanResult, nil
}

// wrapStrings ensures string values in the expression are enclosed in single quotes.
func wrapStrings(expression string) string {
	// Regex to match words that are not part of an operator, number, or boolean.
	re := regexp.MustCompile(`(\b[a-zA-Z_][a-zA-Z0-9_]*\b)`)
	return re.ReplaceAllStringFunc(expression, func(match string) string {
		// Avoid modifying operators or boolean values
		lower := strings.ToLower(match)
		if lower == "true" || lower == "false" || lower == "and" || lower == "or" || lower == "not" {
			return match
		}
		// Wrap other words in quotes
		return fmt.Sprintf("'%s'", match)
	})
}

// evaluateSwitchCondition evaluates a switch-like condition based on the given expression and cases.
func evaluateSwitchCondition(expression string, rawCases, rawInput map[string]interface{}) (interface{}, error) {

	// Parse the expression (basic implementation)
	// example expression: "test == test", or just "test"
	parts := strings.Fields(expression)
	var expr interface{}
	var err error
	if len(parts) > 1 {
		// use evaluateIfCondition for comparison
		expr, err = evaluateIfCondition(expression, rawInput)
		if err != nil {
			return false, fmt.Errorf("invalid switch condition: %s", expression)
		}
	} else {
		// Check if expression needs to be resolved
		expr = resolveResponseString(rawInput, expression)
	}

	// Check if cases needs to be resolved
	cases := make(map[string]interface{})
	// Convert rawCases to a map
	for k, v := range rawCases {
		// Check if k needs to be resolved
		k = resolveResponseString(rawInput, k)
		// Check if v needs to be resolved
		// Is V a map?
		if _, ok := v.(map[string]interface{}); ok {
			v = resolveValue(rawInput, v)
		} else {
			// Check if v is a string
			// If it is, resolve it
			v = resolveResponseString(rawInput, v.(string))
		}
		cases[k] = v
	}

	// Look for matching cases in params
	if expr != nil {
		if caseValue, exists := cases[fmt.Sprintf("%v", expr)]; exists {
			// Execute the case
			fmt.Printf("Case %v\n", caseValue)
			return caseValue, nil
		}
	}

	// Fallback to 'default' case if defined
	if _, defaultExists := cases["default"]; defaultExists {
		return cases["default"], nil
	}

	return nil, fmt.Errorf("no matching case found")
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
