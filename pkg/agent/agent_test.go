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
	"strings"
	"testing"
)

func TestNewJobEngine(t *testing.T) {
	jobEngine := NewJobEngine()

	jobEngine.Initialize()

	if jobEngine == nil {
		t.Fatalf("Expected non-nil JobEngine, got nil")
	}

	if jobEngine.actions == nil {
		t.Fatalf("Expected non-nil actions map, got nil")
	}

	if len(jobEngine.actions) == 0 {
		t.Fatalf("Expected actions map, got %d elements", len(jobEngine.actions))
	}
}

type MockAction struct {
	name string
}

func (m *MockAction) Name() string {
	return m.name
}

func (m *MockAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
	return nil, nil
}

func TestRegisterAction(t *testing.T) {
	jobEngine := NewJobEngine()
	action := &MockAction{name: "TestAction"}

	baseActions := len(jobEngine.actions)

	jobEngine.RegisterAction(action)

	if len(jobEngine.actions) != baseActions+1 {
		t.Fatalf("Expected %d action in actions map, got %d", baseActions+1, len(jobEngine.actions))
	}

	if jobEngine.actions["TestAction"] != action {
		t.Fatalf("Expected action 'TestAction' to be registered")
	}
}

func TestExecuteJob(t *testing.T) {
	jobEngine := NewJobEngine()
	mockAction := &MockAction{name: "TestAction"}
	jobEngine.RegisterAction(mockAction)

	job := []map[string]interface{}{
		{
			"action": "TestAction",
			"params": map[string]interface{}{
				"param1": "value1",
			},
		},
	}

	result, err := jobEngine.ExecuteJob(job)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if result != nil {
		t.Fatalf("Expected nil result, got non-nil")
	}
}

func TestExecuteJob_MissingActionField(t *testing.T) {
	jobEngine := NewJobEngine()

	job := []map[string]interface{}{
		{
			"params": map[string]interface{}{
				"param1": "value1",
			},
		},
	}

	_, err := jobEngine.ExecuteJob(job)
	if err == nil {
		t.Fatalf("Expected error, got nil")
	}

	expectedErr := "missing 'action' field in job step"
	if err.Error() != expectedErr {
		t.Fatalf("Expected error '%s', got '%s'", expectedErr, err.Error())
	}
}

func TestExecuteJob_UnknownAction(t *testing.T) {
	jobEngine := NewJobEngine()
	jobEngine.Initialize()

	job := []map[string]interface{}{
		{
			"action": "UnknownAction",
			"params": map[string]interface{}{
				"param1": "value1",
			},
		},
	}

	// COnvert job to Job struct
	Job := Job{Name: "TestJob", Steps: job}

	AgentsRegistry.LoadJob(Job)

	_, err := jobEngine.ExecuteJob(job)
	if err == nil {
		t.Fatalf("Expected error, got nil")
	}

	expectedErr := "unknown action: UnknownAction"
	if err.Error() != expectedErr {
		t.Fatalf("Expected error '%s', got '%s'", expectedErr, err.Error())
	}
}

func TestRunCommandAction_Execute(t *testing.T) {
	action := &RunCommandAction{}

	tests := []struct {
		name          string
		params        map[string]interface{}
		expectedError string
	}{
		{
			name: "ValidCommand",
			params: map[string]interface{}{
				"config": map[string]interface{}{},
				"input":  "/bin/echo Hello, World!",
			},
			expectedError: "",
		},
		{
			name: "MissingCommandParameter",
			params: map[string]interface{}{
				"config": map[string]interface{}{},
			},
			expectedError: "missing 'input' parameter",
		},
		{
			name: "InvalidCommand",
			params: map[string]interface{}{
				"input": "invalidcommand",
			},
			expectedError: "start failed: exec: \"invalidcommand\": executable file not found in $PATH",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := action.Execute(tt.params)

			if tt.expectedError == "" && err != nil {
				t.Fatalf("Expected no error, got %v", err)
			}

			if tt.expectedError != "" {
				if err == nil {
					t.Fatalf("Expected error '%s', got nil", tt.expectedError)
				}
				if !contains(err.Error(), tt.expectedError) {
					t.Fatalf("Expected error '%s', got '%s'", tt.expectedError, err.Error())
				}
			}

			if tt.expectedError == "" && result == nil {
				t.Fatalf("Expected non-nil result, got nil")
			}

			if tt.expectedError == "" {
				response, ok := result[StrResponse].(string)
				if !ok {
					t.Fatalf("Expected response to be a string, got %T", result[StrResponse])
				}
				if response == "" {
					t.Fatalf("Expected non-empty response, got empty string")
				}
			}
		})
	}
}

func TestDecisionAction_Execute(t *testing.T) {
	TestAgent := Job{
		Name: "TestAgent",
		Steps: []map[string]interface{}{
			{
				"action": "RunCommand",
				"params": map[string]interface{}{
					"command": "echo 'Hello from TestAgent'",
				},
			},
		},
	}

	// Create testAgent for testing purposes
	AgentsRegistry.LoadJob(TestAgent)

	action := &DecisionAction{}

	tests := []struct {
		name           string
		params         map[string]interface{}
		expectedError  string
		expectedStatus string
	}{
		{
			name: "ValidConditionTrue",
			params: map[string]interface{}{
				"condition": map[string]interface{}{
					"condition_type": "if",
					"expression":     "true",
					"on_true": map[string]interface{}{
						"call_agent": "TestAgent",
					},
				},
			},
			expectedError:  "agent 'TestAgent' not found",
			expectedStatus: StatusError,
		},
		{
			name: "ValidConditionFalse",
			params: map[string]interface{}{
				"condition": map[string]interface{}{
					"condition_type": "if",
					"expression":     "false",
					"on_false": map[string]interface{}{
						"call_agent": "TestAgent",
					},
				},
			},
			expectedError:  "agent 'TestAgent' not found",
			expectedStatus: StatusError,
		},
		{
			name: "MissingConditionParameter",
			params: map[string]interface{}{
				"on_true": map[string]interface{}{
					"call_agent": "TestAgent",
				},
			},
			expectedError:  "missing 'condition' parameter",
			expectedStatus: StatusError,
		},
		{
			name: "InvalidConditionResult",
			params: map[string]interface{}{
				"condition": map[string]interface{}{
					"condition_type": "if",
					"expression":     "invalid",
					"on_true": map[string]interface{}{
						"call_agent": "TestAgent",
					},
				},
			},
			expectedError:  "expression did not return a boolean: invalid",
			expectedStatus: StatusError,
		},
		{
			name: "MissingOnTrueSteps",
			params: map[string]interface{}{
				"condition": map[string]interface{}{
					"condition_type": "if",
					"expression":     "true",
				},
			},
			expectedError:  "missing 'on_true' step",
			expectedStatus: StatusError,
		},
		{
			name: "MissingOnFalseSteps",
			params: map[string]interface{}{
				"condition": map[string]interface{}{
					"condition_type": "if",
					"expression":     "false",
				},
			},
			expectedError:  "missing 'on_false' step",
			expectedStatus: StatusError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := action.Execute(tt.params)

			if tt.expectedError == "" && err != nil {
				t.Fatalf("Expected no error, got %v", err)
			}

			if tt.expectedError != "" {
				if err == nil {
					t.Fatalf("Expected error '%s', got nil", tt.expectedError)
				}
				if !contains(err.Error(), tt.expectedError) {
					t.Fatalf("Expected error '%s', got '%s'", tt.expectedError, err.Error())
				}
			}

			if tt.expectedError == "" && result == nil {
				t.Fatalf("Expected non-nil result, got nil")
			}

			if tt.expectedError == "" {
				status, ok := result[StrStatus].(string)
				if !ok {
					t.Fatalf("Expected status to be a string, got %T", result[StrStatus])
				}
				if status != tt.expectedStatus {
					t.Fatalf("Expected status '%s', got '%s'", tt.expectedStatus, status)
				}
			}
		})
	}
}

func contains(str, substr string) bool {
	return strings.Contains(str, substr)
}
