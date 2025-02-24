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
	Name        string                   `yaml:"name" json:"name"`
	Process     string                   `yaml:"process" json:"process"`
	TriggerType string                   `yaml:"trigger_type" json:"trigger_type"`
	TriggerName string                   `yaml:"trigger_name" json:"trigger_name"`
	Steps       []map[string]interface{} `yaml:"steps" json:"steps"`
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

// LoadConfig loads a YAML or JSON configuration file
// TODO: implement also remote loading of Agents Definitions
func (jc *JobConfig) LoadConfig(agtConfigs []cfg.AgentsConfig) error {
	// iterate over all the configuration options
	for _, agtConfig := range agtConfigs {
		// Extract the GlobalParameters for this entry
		globalParams := agtConfig.GlobalParameters

		// Load the configuration from the file
		paths := agtConfig.Path
		if len(paths) == 0 {
			paths = []string{"./agents/*.yaml"}
		}
		for _, path := range paths {
			// Check if the path is wildcard
			files, err := filepath.Glob(path)
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "Error finding rule files:", err)
				return err
			}

			// Process all files in the directory
			if len(files) > 0 {
				// Iterate over all files in the directory
				for _, filePath := range files {
					fileType := cmn.GetFileExt(filePath)
					if (fileType != "yaml") && (fileType != "json") && (fileType != "") && (fileType != "yml") {
						// Ignore unsupported file types
						continue
					}
					cmn.DebugMsg(cmn.DbgLvlDebug, "Loading agents definition file: %s", filePath)

					// Load the configuration file
					file, err := os.Open(filePath) //nolint:gosec // The path here is handled by the service not an end-user
					if err != nil {
						return fmt.Errorf("failed to open config file: %v", err)
					}
					defer file.Close() //nolint:errcheck // Don't lint for error not checked, this is a defer statement

					// Transform file into a string for interpolation
					fileStr, err := io.ReadAll(file)
					if err != nil {
						return fmt.Errorf("failed to read config file: %v", err)
					}

					// Interpolate environment variables and process includes
					interpolatedData := cmn.InterpolateEnvVars(string(fileStr))

					// transform the string back into a reader
					readCloser := io.NopCloser(strings.NewReader(interpolatedData))
					defer readCloser.Close() //nolint:errcheck // Don't lint for error not checked, this is a defer statement

					// Decode the configuration file
					var agtConfigStorage JobConfig
					if strings.HasSuffix(filePath, ".yaml") || strings.HasSuffix(filePath, ".yml") {
						err = yaml.NewDecoder(readCloser).Decode(&agtConfigStorage)
					} else if strings.HasSuffix(filePath, ".json") {
						err = json.NewDecoder(readCloser).Decode(&agtConfigStorage)
					} else {
						return fmt.Errorf("unsupported file format: %s", filePath)
					}
					if err != nil {
						return fmt.Errorf("failed to parse config file: %v", err)
					}

					// Add the global parameters to the configuration
					if len(globalParams) > 0 {
						for i := 0; i < len(agtConfigStorage.Jobs); i++ {
							for j := 0; j < len(agtConfigStorage.Jobs[i].Steps); j++ {
								// Check if each Job's step has a params field
								if _, ok := agtConfigStorage.Jobs[i].Steps[0]["params"]; !ok {
									// If not, create the "params" key with an empty map[interface{}]interface{}
									agtConfigStorage.Jobs[i].Steps[0]["params"] = make(map[interface{}]interface{})
								}
								// Check if each job's step has a "config" field in params, if not add it
								if _, ok := agtConfigStorage.Jobs[i].Steps[j]["params"].(map[interface{}]interface{})[StrConfig]; !ok {
									agtConfigStorage.Jobs[i].Steps[j]["params"].(map[interface{}]interface{})[StrConfig] = make(map[interface{}]interface{})
								}
							}

							// Add the global parameters to the configuration
							for j := 0; j < len(agtConfigStorage.Jobs[i].Steps); j++ {
								for k, v := range globalParams {
									// Check if the params field has a k field
									if _, ok := agtConfigStorage.Jobs[i].Steps[j]["params"]; !ok {
										// If not, create the "params" key with an empty map[interface{}]interface{}
										agtConfigStorage.Jobs[i].Steps[j]["params"] = make(map[interface{}]interface{})
									}
									// Ensure the type assertion works
									if paramMap, ok := agtConfigStorage.Jobs[i].Steps[j]["params"].(map[interface{}]interface{}); ok {
										// Convert globalParams into map[interface{}]interface{} before merging
										paramMap[k] = v
									} else {
										// Handle unexpected types
										cmn.DebugMsg(cmn.DbgLvlError, "params field is not of type map[interface{}]interface{}, but is %T", agtConfigStorage.Jobs[i].Steps[j]["params"])
									}
								}
							}
						}
					}

					// Add the configuration to the list
					jc.Jobs = append(jc.Jobs, agtConfigStorage.Jobs...)
				}
			}
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
			jc := &JobConfig{}
			jc.Jobs = append(jc.Jobs, jc.Jobs[i])
			return jc, true
		}
	}
	return nil, false
}

// GetAgentsByEventType returns all agents that are triggered by a specific event type
func (jc *JobConfig) GetAgentsByEventType(eventType string) ([]*JobConfig, bool) {
	var agents []*JobConfig

	for i := 0; i < len(jc.Jobs); i++ {
		if strings.ToLower(strings.TrimSpace(jc.Jobs[i].TriggerType)) == "event" && strings.TrimSpace(jc.Jobs[i].TriggerName) == eventType {
			jc := &JobConfig{}
			jc.Jobs = append(jc.Jobs, jc.Jobs[i])
			agents = append(agents, jc)
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

// ExecuteJobs executes all jobs in the configuration
func (je *JobEngine) ExecuteJobs(j *JobConfig, iCfg map[string]interface{}) error {
	// Create a waiting group for parallel group processing
	var wg sync.WaitGroup

	// Iterate over job groups
	for _, jobGroup := range j.Jobs {
		cmn.DebugMsg(cmn.DbgLvlDebug, "Executing Job Group: %s", jobGroup.Name)

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
				configMap[k] = v
			}
		}

		// log the configuration for debugging purposes
		cmn.DebugMsg(cmn.DbgLvlDebug, "Job Group Configuration: %v", jobGroup)

		// Check if the group should run in parallel
		if strings.ToLower(strings.TrimSpace(jobGroup.Process)) == "parallel" {
			// Increment the wait group counter
			wg.Add(1)

			// Execute the group in parallel
			go func(jg []map[string]interface{}) {
				defer wg.Done()
				if err := executeJobGroup(je, jg); err != nil {
					cmn.DebugMsg(cmn.DbgLvlError, "Failed to execute job group '%s': %v", jobGroup.Name, err)
				}
				cmn.DebugMsg(cmn.DbgLvlDebug, "Job Group '%s' completed successfully", jobGroup.Name)
			}(jobGroup.Steps)

		} else {
			// Execute the group serially
			if err := executeJobGroup(je, jobGroup.Steps); err != nil {
				return fmt.Errorf("failed to execute job group '%s': %v", jobGroup.Name, err)
			}
			cmn.DebugMsg(cmn.DbgLvlDebug, "Job Group '%s' completed successfully", jobGroup.Name)
		}
	}

	// Wait for all parallel groups to finish
	wg.Wait()
	return nil
}

// executeJobGroup runs jobs in a group serially
func executeJobGroup(je *JobEngine, steps []map[string]interface{}) error {
	lastResult := make(map[string]interface{})

	// Execute each job in the group
	for i := 0; i < len(steps); i++ {
		step := steps[i]

		// Get the action name
		actionName, ok := step["action"].(string)
		if !ok {
			return fmt.Errorf("missing 'action' field in job step")
		}
		params, _ := step["params"].(map[string]interface{})

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
				for k, v := range v.(map[string]interface{}) {
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
			if retryConfig, hasRetry := step["retry"].(RetryConfig); hasRetry {
				result, err = executeWithRetry(action, params, retryConfig)
			}
			if err != nil {
				if fallback, hasFallback := step["fallback"].([]map[string]interface{}); hasFallback {
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
func executeWithRetry(action Action, params map[string]interface{}, retryConfig RetryConfig) (map[string]interface{}, error) {
	var lastError error
	var result map[string]interface{}

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
