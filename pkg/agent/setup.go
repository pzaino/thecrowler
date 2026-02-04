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

/*
  The CROWler is the foundation; the user builds and deploys autonomous or semi-autonomous agents that leverage CROWler's capabilities to perform high-level content discovery, threat simulation, and intelligence extraction.
*/

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"

	"gopkg.in/yaml.v2"
)

var (
	// AgentsEngine is the agent engine
	AgentsEngine *JobEngine
	// AgentsRegistry is the default agent configurations repository
	AgentsRegistry *JobConfig
)

// RetryConfig defines retry behavior
type RetryConfig struct {
	MaxRetries int
	BaseDelay  time.Duration
	Backoff    float64
}

// JobConfig represents the structure of a job configuration file
type JobConfig struct {
	Jobs []Job `yaml:"jobs" json:"jobs"`
}

// Job represents a job configuration
type Job struct {
	Name           string           `yaml:"name" json:"name"`
	Process        string           `yaml:"process" json:"process"`
	TriggerType    string           `yaml:"trigger_type" json:"trigger_type"`
	TriggerName    string           `yaml:"trigger_name" json:"trigger_name"`
	Steps          []map[string]any `yaml:"steps" json:"steps"`
	AgentsTimeout  int              `yaml:"timeout" json:"timeout"`
	PluginsTimeout int              `yaml:"plugins_timeout" json:"plugins_timeout"`
}

// NewJobConfig creates a new job configuration
func NewJobConfig() *JobConfig {
	return &JobConfig{}
}

// LoadJob loads a job into the JobConfig
func (jc *JobConfig) LoadJob(j Job) {
	if jc == nil {
		jc = NewJobConfig()
	}
	if jc.Jobs == nil {
		jc.Jobs = make([]Job, 0)
	}
	jc.Jobs = append(jc.Jobs, j)
}

// --- helpers: parsing and normalization ---

// parseAgentsBytes decodes YAML or JSON into a JobConfig
func parseAgentsBytes(data []byte, fileType string) (JobConfig, error) {
	var cfg JobConfig
	var err error
	switch strings.ToLower(strings.TrimPrefix(fileType, ".")) {
	case "yaml", "yml":
		err = yaml.NewDecoder(bytes.NewReader(data)).Decode(&cfg)
	case "json":
		err = json.NewDecoder(bytes.NewReader(data)).Decode(&cfg)
	default:
		err = fmt.Errorf("unsupported file format: %s", fileType)
	}
	return cfg, err
}

// applyGlobalParams merges AgentsTimeout/PluginsTimeout and GlobalParameters into every step
func applyGlobalParams(cfg *JobConfig, agt cfg.AgentsConfig) {
	globalParams := agt.GlobalParameters
	if len(cfg.Jobs) == 0 || (len(globalParams) == 0 && agt.AgentsTimeout == 0 && agt.PluginsTimeout == 0) {
		return
	}
	for i := range cfg.Jobs {
		// Defaults for timeouts
		if cfg.Jobs[i].AgentsTimeout == 0 {
			cfg.Jobs[i].AgentsTimeout = agt.AgentsTimeout
		}
		if cfg.Jobs[i].PluginsTimeout == 0 {
			cfg.Jobs[i].PluginsTimeout = agt.PluginsTimeout
		}
		// Ensure step params/config map exists, then merge global params
		for j := range cfg.Jobs[i].Steps {
			// Ensure "params" map exists
			if _, ok := cfg.Jobs[i].Steps[j]["params"]; !ok {
				cfg.Jobs[i].Steps[j]["params"] = make(map[interface{}]interface{})
			}
			// Ensure "config" inside params exists
			if _, ok := cfg.Jobs[i].Steps[j]["params"].(map[interface{}]interface{})[StrConfig]; !ok {
				cfg.Jobs[i].Steps[j]["params"].(map[interface{}]interface{})[StrConfig] = make(map[interface{}]interface{})
			}
			// Merge global parameters into params
			if paramMap, ok := cfg.Jobs[i].Steps[j]["params"].(map[interface{}]interface{}); ok {
				for k, v := range globalParams {
					paramMap[k] = v
				}
			} else {
				cmn.DebugMsg(cmn.DbgLvlError, "params field is not map[interface{}]interface{} but %T", cfg.Jobs[i].Steps[j]["params"])
			}
		}
	}
}

// --- LOCAL loader (extracted from your current code) ---

func loadAgentsFromLocal(paths []string) ([]JobConfig, error) {
	if len(paths) == 0 {
		paths = []string{"./agents/*.yaml"}
	}

	var out []JobConfig
	for _, path := range paths {
		// Expand wildcards
		files, err := filepath.Glob(path)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Error finding agent files: %v", err)
			return nil, err
		}
		if len(files) == 0 {
			continue
		}

		for _, filePath := range files {
			ext := cmn.GetFileExt(filePath) // no dot, e.g., "yaml"
			if ext != "" && ext != "yaml" && ext != "yml" && ext != "json" {
				continue
			}
			cmn.DebugMsg(cmn.DbgLvlDebug, "Loading agents definition file: %s", filePath)

			f, err := os.Open(filePath) //nolint:gosec
			if err != nil {
				return nil, fmt.Errorf("failed to open config file: %w", err)
			}
			data, rerr := io.ReadAll(f)
			_ = f.Close()
			if rerr != nil {
				return nil, fmt.Errorf("failed to read config file: %w", rerr)
			}

			// Interpolate env vars
			interpolated := cmn.InterpolateEnvVars(string(data))
			cfg, err := parseAgentsBytes([]byte(interpolated), filepath.Ext(filePath))
			if err != nil {
				return nil, fmt.Errorf("failed to parse config file %s: %w", filePath, err)
			}
			out = append(out, cfg)
		}
	}
	return out, nil
}

// --- REMOTE loader (mirrors your plugins/rules remote approach) ---

func loadAgentsFromRemote(agt cfg.AgentsConfig) ([]JobConfig, error) {
	if agt.Path == nil {
		return nil, fmt.Errorf("agents path is empty")
	}

	var out []JobConfig
	for _, path := range agt.Path {
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
		if ext != "yaml" && ext != "yml" && ext != "json" {
			// ignore unsupported types
			continue
		}

		// protocol selection (http/https, ftp/ftps, s3); mirrors your existing code
		var proto string
		switch strings.ToLower(strings.TrimSpace(agt.Type)) {
		case "http":
			proto = "http"
		case "ftp":
			proto = "ftp"
		default:
			proto = "s3"
		}
		if agt.SSLMode == cmn.EnableStr && proto == "http" {
			proto = "https"
		}
		if agt.SSLMode == cmn.EnableStr && proto == "ftp" {
			proto = "ftps"
		}

		// URL compose
		var url string
		if agt.Port != "" && agt.Port != "80" && agt.Port != "443" {
			url = fmt.Sprintf("%s://%s:%s/%s", proto, agt.Host, agt.Port, path)
		} else {
			url = fmt.Sprintf("%s://%s/%s", proto, agt.Host, path)
		}
		cmn.DebugMsg(cmn.DbgLvlDebug, "Downloading agents definition from %s", url)

		// Download
		body, err := cmn.FetchRemoteFile(url, agt.Timeout, agt.SSLMode)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch agents from %s: %w", url, err)
		}

		// Env interpolation, then parse
		interpolated := cmn.InterpolateEnvVars(body)
		cfg, err := parseAgentsBytes([]byte(interpolated), filepath.Ext(path))
		if err != nil {
			return nil, fmt.Errorf("failed to parse remote agents from %s: %w", url, err)
		}
		out = append(out, cfg)
	}
	return out, nil
}

// --- Public method with remote support ---

// LoadConfig loads YAML/JSON Agent Definitions from local paths or remote hosts (http/ftp/s3)
// and applies global parameters/timeouts from each AgentsConfig entry.
func (jc *JobConfig) LoadConfig(agtConfigs []cfg.AgentsConfig) error {
	for _, agt := range agtConfigs {
		var chunks []JobConfig
		var err error

		if strings.TrimSpace(agt.Host) == "" {
			// LOCAL
			paths := agt.Path
			if len(paths) == 0 {
				paths = []string{"./agents/*.yaml"}
			}
			chunks, err = loadAgentsFromLocal(paths)
			if err != nil {
				return err
			}
		} else {
			// REMOTE
			chunks, err = loadAgentsFromRemote(agt)
			if err != nil {
				return err
			}
		}

		// Normalize and merge global params for each loaded chunk, then append
		for idx := range chunks {
			applyGlobalParams(&chunks[idx], agt)
			jc.Jobs = append(jc.Jobs, chunks[idx].Jobs...)
		}
	}
	return nil
}

// RegisterAgent registers an agent with the JobConfig
func (jc *JobConfig) RegisterAgent(agent *JobConfig) {
	jc.Jobs = append(jc.Jobs, agent.Jobs...)
}

// GetAgentByName returns an agent by name
func (jc *JobConfig) GetAgentByName(name string) (*JobConfig, bool) {
	if jc == nil {
		return nil, false
	}
	if len(jc.Jobs) == 0 {
		return nil, false
	}
	for i := 0; i < len(jc.Jobs); i++ {
		if strings.TrimSpace(jc.Jobs[i].Name) == name {
			// Return the Job with the specified name inside a JobConfig struct
			rjc := &JobConfig{}
			rjc.Jobs = append(rjc.Jobs, jc.Jobs[i])
			return rjc, true
		}
	}
	return nil, false
}

// GetAgentsByEventType returns all agents that are triggered by a specific event type
func (jc *JobConfig) GetAgentsByEventType(eventType string) ([]*JobConfig, bool) {
	if jc == nil {
		return nil, false
	}
	if len(jc.Jobs) == 0 {
		return nil, false
	}

	var agents []*JobConfig
	for i := 0; i < len(jc.Jobs); i++ {
		if strings.ToLower(strings.TrimSpace(jc.Jobs[i].TriggerType)) == "event" && strings.TrimSpace(jc.Jobs[i].TriggerName) == eventType {
			rjc := &JobConfig{}
			rjc.Jobs = append(rjc.Jobs, jc.Jobs[i])
			agents = append(agents, rjc)
		}
	}

	return agents, len(agents) > 0
}

// GetAgentByName returns an agent by name from the JobEngine
func (je *JobEngine) GetAgentByName(name string) (*JobConfig, bool) {
	return AgentsRegistry.GetAgentByName(name)
}

// GetAgentsByEventType returns all agents that are triggered by a specific event type from the JobEngine
func (je *JobEngine) GetAgentsByEventType(eventType string) ([]*JobConfig, bool) {
	return AgentsRegistry.GetAgentsByEventType(eventType)
}

func deepCopyJob(j Job) Job {
	out := j // copy scalars

	out.Steps = make([]map[string]any, len(j.Steps))
	for i, step := range j.Steps {
		m := make(map[string]any, len(step))
		for k, v := range step {
			m[k] = v
		}
		out.Steps[i] = m
	}

	return out
}

// ExecuteJobs executes all jobs in the configuration
func (je *JobEngine) ExecuteJobs(j *JobConfig, iCfg map[string]any) error {
	// Create a waiting group for parallel group processing
	var wg sync.WaitGroup

	// Create a deep copy of j.Jobs to avoid modifying the original configuration
	localJobs := make([]Job, len(j.Jobs))
	for i, job := range j.Jobs {
		localJobs[i] = deepCopyJob(job)
	}

	// Iterate over job groups
	for _, jobGroup := range localJobs {
		cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-Agents] Executing Job Group: %s", jobGroup.Name)

		// Add iCfg to the first step as StrConfig field
		// this is the "base" configuration that will be passed to all steps
		// and contains things like *wd and *dbHandler
		if len(jobGroup.Steps) > 0 {
			params, ok := jobGroup.Steps[0]["params"]
			if !ok {
				jobGroup.Steps[0]["params"] = make(map[string]interface{})
			} else {
				jobGroup.Steps[0]["params"] = cmn.ConvertMapIIToSI(params)
			}

			paramsMap := jobGroup.Steps[0]["params"].(map[string]interface{})

			// Ensure "StrConfig" exists within "params" and is a map[string]interface{}
			if _, ok := paramsMap[StrConfig]; !ok || paramsMap[StrConfig] == nil {
				paramsMap[StrConfig] = make(map[string]interface{})
			}

			configMap := paramsMap[StrConfig].(map[string]interface{})

			// Merge iCfg into configMap
			for k, v := range iCfg {
				k = strings.ToLower(strings.TrimSpace(k))
				if k == "metadata" {
					k = "meta_data"
				}
				configMap[k] = v
				if k == "meta_data" {
					cmn.DebugMsg(cmn.DbgLvlDebug3, "[DEBUG-Agents] Merged meta_data into job config: %v", v)
				}
			}
		}

		// log the configuration for debugging purposes
		cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-Agents] Job Group Configuration: %v", jobGroup)

		// Check if the group should run in parallel
		if strings.ToLower(strings.TrimSpace(jobGroup.Process)) == "parallel" {
			// Increment the wait group counter
			wg.Add(1)

			// Execute the group in parallel
			go func(jg []map[string]any) {
				defer wg.Done()
				if err := executeJobGroup(je, jg); err != nil {
					cmn.DebugMsg(cmn.DbgLvlError, "[DEBUG-Agents] Failed to execute job group '%s': %v", jobGroup.Name, err)
				}
				cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-Agents] Job Group '%s' completed successfully", jobGroup.Name)
			}(jobGroup.Steps)

		} else {
			// Execute the group serially
			if err := executeJobGroup(je, jobGroup.Steps); err != nil {
				return fmt.Errorf("failed to execute job group '%s': %v", jobGroup.Name, err)
			}
			cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG-Agents] Job Group '%s' completed successfully", jobGroup.Name)
		}
	}

	// Wait for all parallel groups to finish
	wg.Wait()
	return nil
}

// executeJobGroup runs jobs in a group serially
func executeJobGroup(je *JobEngine, steps []map[string]any) error {
	lastResult := make(map[string]any)

	// Execute each job in the group
	for i := 0; i < len(steps); i++ {
		step := &steps[i]

		// Get the action name
		actionName, ok := (*step)["action"].(string)
		if !ok {
			return fmt.Errorf("missing 'action' field in job step")
		}
		params, _ := (*step)["params"].(map[string]interface{})

		// If we are to a step that is not the first one, we need to transform StrResponse (from previous step) to StrRequest
		if i > 0 {
			if _, ok := params[StrRequest]; !ok {
				params[StrRequest] = lastResult[StrResponse]
			} else {
				// If yes, merge the two maps
				for k, v := range lastResult[StrResponse].(map[string]interface{}) {
					params[StrRequest].(map[string]interface{})[k] = v
				}
			}
		}

		// Inject previous result into current params (if needed)
		for k, v := range lastResult {
			// Skip key response, we have already converted it to input
			if k == StrResponse {
				continue
			}

			// Check if k == config, if so, merge the two maps
			if k == StrConfig {
				// Check if the params field has a config field
				if _, ok := params[StrConfig]; !ok {
					// If not, add the config field
					params[StrConfig] = v
					continue
				}
				// If yes, merge the two maps
				for k, v := range v.(map[string]interface{}) {
					params[StrConfig].(map[string]interface{})[k] = v
				}
				continue
			}

			// Check if the params field has a k field
			if _, ok := params[k]; !ok {
				// If not, add the k field
				params[k] = v
			} else {
				// If yes, merge the two maps
				for k, v := range v.(map[string]any) {
					params[k] = v
				}
			}
		}

		action, exists := je.actions[actionName]
		if !exists {
			return fmt.Errorf("unknown action: %s", actionName)
		}

		result, err := action.Execute(params)
		if err != nil {
			if retryConfig, hasRetry := (*step)["retry"].(RetryConfig); hasRetry {
				result, err = executeWithRetry(action, params, retryConfig)
			}
			if err != nil {
				if fallback, hasFallback := (*step)["fallback"].([]map[string]interface{}); hasFallback {
					cmn.DebugMsg(cmn.DbgLvlError, "Action %s failed, executing fallback steps", actionName)
					return executeJobGroup(je, fallback)
				}
				return fmt.Errorf("action %s failed: %v", actionName, err)
			}
		}

		// Update the result for the next job in the group
		lastResult = result
	}
	return nil
}

// executeWithRetry executes an action with retry logic
func executeWithRetry(action Action, params map[string]any, retryConfig RetryConfig) (map[string]interface{}, error) {
	var lastError error
	var result map[string]any

	for attempt := 1; attempt <= retryConfig.MaxRetries; attempt++ {
		result, lastError = action.Execute(params)
		if lastError == nil {
			return result, nil
		}

		delay := retryConfig.BaseDelay * time.Duration(math.Pow(retryConfig.Backoff, float64(attempt-1)))
		time.Sleep(delay)
	}

	return nil, fmt.Errorf("action failed after %d retries: %w", retryConfig.MaxRetries, lastError)
}

/*
Example of a job configuration file in YAML format:

jobs:
  - name: "Serial Group 1"
    process: "serial"
	trigger_type: event
	trigger_name: "event_name"
    steps:
      - action: "APIRequest"
        params:
          config:
            url: "http://example.com/api/data"
      - action: "AIInteraction"
        params:
          prompt: "Summarize the following data: $response"
          config:
            url: "https://api.openai.com/v1/completions"
            api_key: "your_api_key"

  - name: "Parallel Group 1"
    process: "parallel"
	trigger_type: event
	trigger_name: "event_name"
    steps:
      - action: "DBQuery"
        params:
          type: "insert"
          query: "INSERT INTO logs (message) VALUES ('Parallel job 1')"
      - action: "RunCommand"
        params:
          command: "echo 'Parallel job 2'"

  - name: "Serial Group 2"
    process: "serial"
	trigger_type: agent
	trigger_name: "agent_name"
    steps:
      - action: "PluginExecution"
        params:
          plugin: ["example_plugin"]




*/
