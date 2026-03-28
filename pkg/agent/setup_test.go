package agent

import (
	"os"
	"path/filepath"
	"testing"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func TestInitialize(t *testing.T) {
	// Reset the global variables before each test
	AgentsEngine = nil

	// Call the Initialize function (global variable AgentsEngine should be initialized)
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

func TestParseAgentsBytesWithDerivedIdentity(t *testing.T) {
	content := []byte(`jobs:
  - name: "Legacy Agent"
    process: "serial"
    trigger_type: "manual"
    trigger_name: "legacy_agent"
    steps:
      - action: "RunCommand"
        params:
          command: "echo legacy"
`)

	cfg, err := parseAgentsBytes(content, "yaml")
	if err != nil {
		t.Fatalf("expected no error parsing legacy config, got %v", err)
	}
	if cfg.AgentIdentity == nil {
		t.Fatal("expected derived agent_identity to be set")
	}
	if cfg.AgentIdentity.Name != "Legacy Agent" {
		t.Fatalf("expected derived name 'Legacy Agent', got %q", cfg.AgentIdentity.Name)
	}
	if cfg.AgentIdentity.AgentID != "legacy-agent" {
		t.Fatalf("expected derived agent_id 'legacy-agent', got %q", cfg.AgentIdentity.AgentID)
	}
	if cfg.AgentIdentity.AgentType != "executor" {
		t.Fatalf("expected default agent_type 'executor', got %q", cfg.AgentIdentity.AgentType)
	}
}

func TestParseAgentsBytesWithAgentIdentity(t *testing.T) {
	content := []byte(`{
  "agent_identity": {
    "name": "AI Agent",
    "agent_type": "planner"
  },
  "jobs": [
    {
      "name": "AI Agent",
      "process": "serial",
      "trigger_type": "agent",
      "trigger_name": "entrypoint",
      "steps": [
        {
          "action": "AIInteraction",
          "params": {
            "model": "gpt-4o-mini",
            "prompt": "hello"
          }
        }
      ]
    }
  ]
}`)

	cfg, err := parseAgentsBytes(content, "json")
	if err != nil {
		t.Fatalf("expected no error parsing identity config, got %v", err)
	}
	if cfg.AgentIdentity == nil {
		t.Fatal("expected explicit agent_identity")
	}
	if cfg.AgentIdentity.AgentID != "ai-agent" {
		t.Fatalf("expected derived agent_id 'ai-agent', got %q", cfg.AgentIdentity.AgentID)
	}
	if cfg.AgentIdentity.AgentType != "planner" {
		t.Fatalf("expected explicit agent_type 'planner', got %q", cfg.AgentIdentity.AgentType)
	}
	if cfg.AgentIdentity.Memory == nil || cfg.AgentIdentity.Memory.Scope != "persistent" {
		t.Fatalf("expected default memory scope 'persistent', got %+v", cfg.AgentIdentity.Memory)
	}
}

func TestParseAgentsBytesRejectsMismatchedIdentityName(t *testing.T) {
	content := []byte(`{
  "agent_identity": {
    "name": "Agent A"
  },
  "jobs": [
    {
      "name": "Agent B",
      "process": "serial",
      "trigger_type": "manual",
      "trigger_name": "agent_b",
      "steps": [
        {
          "action": "RunCommand",
          "params": {
            "command": "echo test"
          }
        }
      ]
    }
  ]
}`)

	_, err := parseAgentsBytes(content, "json")
	if err == nil {
		t.Fatal("expected mismatch error but got nil")
	}
}

func TestParseAgentsBytesAssignsLegacyVersionMarker(t *testing.T) {
	content := []byte(`jobs:
  - name: "Legacy Agent"
    process: "serial"
    trigger_type: "manual"
    trigger_name: "legacy_agent"
    steps:
      - action: "RunCommand"
        params:
          command: "echo legacy"
`)

	cfg, err := parseAgentsBytes(content, "yaml")
	if err != nil {
		t.Fatalf("expected no error parsing legacy config, got %v", err)
	}
	if cfg.FormatVersion != AgentFormatVersionV1 {
		t.Fatalf("expected format_version %q, got %q", AgentFormatVersionV1, cfg.FormatVersion)
	}
}

func TestParseAgentsBytesAssignsIdentityVersionMarker(t *testing.T) {
	content := []byte(`{
  "agent_identity": {
    "name": "AI Agent",
    "agent_type": "planner"
  },
  "jobs": [
    {
      "name": "AI Agent",
      "process": "serial",
      "trigger_type": "agent",
      "trigger_name": "entrypoint",
      "steps": [
        {
          "action": "AIInteraction",
          "params": {
            "model": "gpt-4o-mini",
            "prompt": "hello"
          }
        }
      ]
    }
  ]
}`)

	cfg, err := parseAgentsBytes(content, "json")
	if err != nil {
		t.Fatalf("expected no error parsing identity config, got %v", err)
	}
	if cfg.FormatVersion != AgentFormatVersionV2 {
		t.Fatalf("expected format_version %q, got %q", AgentFormatVersionV2, cfg.FormatVersion)
	}
}

func TestGoldenFixturesParse(t *testing.T) {
	tests := []struct {
		name          string
		path          string
		fileType      string
		expectVersion string
	}{
		{
			name:          "legacy json",
			path:          "testdata/legacy.valid.json",
			fileType:      "json",
			expectVersion: AgentFormatVersionV1,
		},
		{
			name:          "legacy yaml",
			path:          "testdata/legacy.valid.yaml",
			fileType:      "yaml",
			expectVersion: AgentFormatVersionV1,
		},
		{
			name:          "identity json",
			path:          "testdata/identity.valid.json",
			fileType:      "json",
			expectVersion: AgentFormatVersionV2,
		},
		{
			name:          "identity yaml",
			path:          "testdata/identity.valid.yaml",
			fileType:      "yaml",
			expectVersion: AgentFormatVersionV2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Clean(tt.path))
			if err != nil {
				t.Fatalf("failed reading fixture %s: %v", tt.path, err)
			}

			cfg, err := parseAgentsBytes(data, tt.fileType)
			if err != nil {
				t.Fatalf("expected fixture %s to parse, got error: %v", tt.path, err)
			}
			if cfg.FormatVersion != tt.expectVersion {
				t.Fatalf("expected format_version %q, got %q", tt.expectVersion, cfg.FormatVersion)
			}
			if len(cfg.Jobs) == 0 {
				t.Fatalf("expected at least one job in fixture %s", tt.path)
			}
		})
	}
}

func TestLoadConfigRegressionHarnessLegacyAndIdentity(t *testing.T) {
	jc := NewJobConfig()
	err := jc.LoadConfig([]cfg.AgentsConfig{
		{
			Path: []string{
				"./testdata/legacy.valid.yaml",
				"./testdata/identity.valid.json",
			},
		},
	})
	if err != nil {
		t.Fatalf("expected no error loading baseline fixtures, got %v", err)
	}
	if len(jc.Jobs) != 2 {
		t.Fatalf("expected 2 jobs loaded from baseline fixtures, got %d", len(jc.Jobs))
	}
}
