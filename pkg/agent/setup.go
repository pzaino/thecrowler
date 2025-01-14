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
	"math"
	"os"
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

// GetAction returns an action by name
func (je *JobEngine) GetAction(name string) (Action, bool) {
	action, exists := je.actions[name]
	return action, exists
}

// JobConfig represents the structure of a job configuration file
type JobConfig struct {
	Jobs []struct {
		Name        string                   `yaml:"name" json:"name"`
		Process     string                   `yaml:"process" json:"process"`
		TriggerType string                   `yaml:"trigger_type" json:"trigger_type"`
		TriggerName string                   `yaml:"trigger_name" json:"trigger_name"`
		Steps       []map[string]interface{} `yaml:"steps" json:"steps"`
	} `yaml:"jobs" json:"jobs"`
}

// NewJobConfig creates a new job configuration
func NewJobConfig() *JobConfig {
	return &JobConfig{}
}

// LoadConfig loads a YAML or JSON configuration file
// TODO: implement also remote loading of Agents Definitions
func (jc *JobConfig) LoadConfig(agtConfigs []cfg.AgentsConfig) error {
	var err error

	// iterate over all the configuration options
	for _, agtConfig := range agtConfigs {
		paths := agtConfig.Path
		for _, path := range paths {
			// Check if the path is wildcard
			var files []os.DirEntry
			if strings.Contains(path, "*") {
				// Get all files in the directory
				files, err = os.ReadDir(path)
				if err != nil {
					return fmt.Errorf("failed to read directory: %v", err)
				}
			}
			if len(files) == 0 {
				// Get all files in the directory
				files, err = os.ReadDir("./agents/*.yaml")
				if err != nil {
					return fmt.Errorf("failed to read directory: %v", err)
				}
			}
			if len(files) > 0 {
				// Iterate over all files in the directory
				for _, fileP := range files {
					filePath := fileP.Name()
					cmn.DebugMsg(cmn.DbgLvlDebug, "Loading Agents definition file: %s", filePath)

					// Load the configuration file
					file, err := os.Open(filePath) //nolint:gosec // The path here is handled by the service not an end-user
					if err != nil {
						return fmt.Errorf("failed to open config file: %v", err)
					}
					defer file.Close() //nolint:errcheck // Don't lint for error not checked, this is a defer statement

					// Decode the configuration file
					var agtConfigStorage JobConfig
					if strings.HasSuffix(filePath, ".yaml") || strings.HasSuffix(filePath, ".yml") {
						err = yaml.NewDecoder(file).Decode(&agtConfigStorage)
					} else if strings.HasSuffix(filePath, ".json") {
						err = json.NewDecoder(file).Decode(&agtConfigStorage)
					} else {
						return fmt.Errorf("unsupported file format: %s", filePath)
					}
					if err != nil {
						return fmt.Errorf("failed to parse config file: %v", err)
					}
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

// GetAgentsByEventType returns all agents that are triggered by a specific event type
func (jc *JobConfig) GetAgentsByEventType(eventType string) ([]*JobConfig, bool) {
	var agents []*JobConfig

	for _, agent := range jc.Jobs {
		if strings.ToLower(strings.TrimSpace(agent.TriggerType)) == "event" && strings.TrimSpace(agent.TriggerName) == eventType {
			agents = append(agents, &JobConfig{Jobs: []struct {
				Name        string                   `yaml:"name" json:"name"`
				Process     string                   `yaml:"process" json:"process"`
				TriggerType string                   `yaml:"trigger_type" json:"trigger_type"`
				TriggerName string                   `yaml:"trigger_name" json:"trigger_name"`
				Steps       []map[string]interface{} `yaml:"steps" json:"steps"`
			}{agent}})
		}
	}

	return agents, len(agents) > 0
}

// ExecuteJobs executes all jobs in the configuration
func (je *JobEngine) ExecuteJobs(j *JobConfig, iCfg map[string]interface{}) error {
	// Create a waiting group for parallel group processing
	var wg sync.WaitGroup

	// Iterate over job groups
	for _, jobGroup := range j.Jobs {
		cmn.DebugMsg(cmn.DbgLvlDebug, "Executing Job Group: %s", jobGroup.Name)

		// Add iCfg to the first step as "config" field
		// this is the "base" configuration that will be passed to all steps
		// and contains things like *wd and *dbHandler
		if len(jobGroup.Steps) > 0 {
			// Check if the first step already has a params field
			if _, ok := jobGroup.Steps[0]["params"]; !ok {
				// If not, add the params field
				jobGroup.Steps[0]["params"] = nil
			}
			// Next check if the params field has a config field
			if _, ok := jobGroup.Steps[0]["params"].(map[string]interface{})["config"]; !ok {
				// If not, add the config field
				jobGroup.Steps[0]["params"].(map[string]interface{})["config"] = iCfg
			} else {
				// If yes, merge the two maps
				for k, v := range iCfg {
					jobGroup.Steps[0]["params"].(map[string]interface{})["config"].(map[string]interface{})[k] = v
				}
			}
		}

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

		// Inject previous result into current params (if needed)
		for k, v := range lastResult {
			params[k] = v
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
