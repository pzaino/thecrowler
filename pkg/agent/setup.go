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
	"os"
	"strings"

	"gopkg.in/yaml.v2"
)

var (
	AgentsEngine *JobEngine
)

// Initialize initializes the agent engine
func Initialize() {
	AgentsEngine = NewJobEngine()
	RegisterActions(AgentsEngine)
}

// RegisterActions registers all available actions with the engine
func RegisterActions(engine *JobEngine) {
	engine.RegisterAction(&APIRequestAction{})
	engine.RegisterAction(&CreateEventAction{})
	engine.RegisterAction(&RunCommandAction{})
	engine.RegisterAction(&AIInteractionAction{})
	engine.RegisterAction(&DBQueryAction{})
}

// JobConfig represents the structure of a job configuration file
type JobConfig struct {
	Jobs []struct {
		Name  string                   `yaml:"name" json:"name"`
		Steps []map[string]interface{} `yaml:"steps" json:"steps"`
	} `yaml:"jobs" json:"jobs"`
}

// LoadConfig loads a YAML or JSON configuration file
func LoadConfig(filePath string) (*JobConfig, error) {
	file, err := os.Open(filePath) //nolint:gosec // The path here is handled by the service not an end-user
	if err != nil {
		return nil, fmt.Errorf("failed to open config file: %v", err)
	}
	defer file.Close() //nolint:errcheck // Don't lint for error not checked, this is a defer statement

	var config JobConfig
	if strings.HasSuffix(filePath, ".yaml") || strings.HasSuffix(filePath, ".yml") {
		err = yaml.NewDecoder(file).Decode(&config)
	} else if strings.HasSuffix(filePath, ".json") {
		err = json.NewDecoder(file).Decode(&config)
	} else {
		return nil, fmt.Errorf("unsupported file format: %s", filePath)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to parse config file: %v", err)
	}

	return &config, nil
}

// ExecuteJobs executes all jobs in the configuration
func (e *JobEngine) ExecuteJobs(engine *JobEngine, config *JobConfig) error {
	for _, job := range config.Jobs {
		fmt.Printf("Executing Job: %s\n", job.Name)
		result, err := engine.ExecuteJob(job.Steps)
		if err != nil {
			return fmt.Errorf("failed to execute job '%s': %v", job.Name, err)
		}
		fmt.Printf("Job '%s' completed successfully. Result: %v\n", job.Name, result)
	}
	return nil
}
