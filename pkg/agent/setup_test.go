package agent

import (
	"testing"
)

func TestInitialize(t *testing.T) {
	// Reset the global variables before each test
	AgentsEngine = nil

	// Call the Initialize function
	Initialize()

	// Check if AgentsEngine is not nil
	if AgentsEngine == nil {
		t.Errorf("Expected AgentsEngine to be initialized, but it is nil")
	}

	// Check if actions are registered
	expectedActions := []string{
		"APIRequest",
		"CreateEvent",
		"RunCommand",
		"AIInteraction",
		"DBQuery",
		"PluginExecution",
		"Decision",
	}

	for _, action := range expectedActions {
		if _, exists := AgentsEngine.actions[action]; !exists {
			t.Errorf("Expected action %s to be registered, but it is not", action)
		}
	}
}

func TestRegisterActions(t *testing.T) {
	// Create a new JobEngine instance
	engine := NewJobEngine()

	// Call the RegisterActions function
	RegisterActions(engine)

	// Check if actions are registered
	expectedActions := []string{
		"APIRequest",
		"CreateEvent",
		"RunCommand",
		"AIInteraction",
		"DBQuery",
		"PluginExecution",
		"Decision",
	}

	for _, action := range expectedActions {
		if _, exists := engine.actions[action]; !exists {
			t.Errorf("Expected action %s to be registered, but it is not", action)
		}
	}
}

func TestRegisterActionsWithNilEngine(t *testing.T) {
	// Call the RegisterActions function with nil engine
	RegisterActions(nil)

	// Check if a new engine is created and actions are registered
	if AgentsEngine == nil {
		t.Errorf("Expected a new JobEngine to be created, but it is nil")
	}

	expectedActions := []string{
		"APIRequest",
		"CreateEvent",
		"RunCommand",
		"AIInteraction",
		"DBQuery",
		"PluginExecution",
		"Decision",
	}

	for _, action := range expectedActions {
		if _, exists := AgentsEngine.GetAction(action); !exists {
			t.Errorf("Expected action %s to be registered, but it is not", action)
		}
	}
}

func TestNewJobConfig(t *testing.T) {
	// Call the NewJobConfig function
	jobConfig := NewJobConfig()

	// Check if the returned JobConfig is not nil
	if jobConfig == nil {
		t.Errorf("Expected JobConfig to be initialized, but it is nil")
	}

	// Check if the Jobs slice is initialized and empty
	if jobConfig.Jobs != nil {
		t.Errorf("Expected Jobs slice to be nil after initialization, but it is not")
	}
	if len(jobConfig.Jobs) != 0 {
		t.Errorf("Expected Jobs slice to be empty, but it has %d elements", len(jobConfig.Jobs))
	}
}
