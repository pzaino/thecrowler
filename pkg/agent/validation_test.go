package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateAgentConfigJSONSuccessAndFailure(t *testing.T) {
	validJSON := `{"jobs":[{"name":"Legacy Agent","process":"serial","trigger_type":"manual","trigger_name":"legacy_agent","steps":[{"action":"RunCommand","params":{"command":"echo ok"}}]}]}`
	if err := ValidateAgentConfig([]byte(validJSON), "json", ValidationModeLenient, nil); err != nil {
		t.Fatalf("expected valid json to pass validation, got: %v", err)
	}

	invalidJSON := `{"jobs":[{"name":"Legacy Agent","process":"serial","trigger_type":"manual","trigger_name":"legacy_agent","steps":[{"action":"NotARealAction","params":{}}]}]}`
	err := ValidateAgentConfig([]byte(invalidJSON), "json", ValidationModeLenient, nil)
	if err == nil {
		t.Fatal("expected invalid json action to fail schema validation")
	}
	if !strings.Contains(err.Error(), "jobs") {
		t.Fatalf("expected actionable path in schema error, got: %v", err)
	}
}

func TestValidateAgentConfigYAMLSuccessAndFailure(t *testing.T) {
	validYAML := `jobs:
  - name: Legacy YAML Agent
    process: serial
    trigger_type: manual
    trigger_name: legacy_yaml
    steps:
      - action: RunCommand
        params:
          command: echo yaml
`
	if err := ValidateAgentConfig([]byte(validYAML), "yaml", ValidationModeLenient, nil); err != nil {
		t.Fatalf("expected valid yaml to pass validation, got: %v", err)
	}

	invalidYAML := `jobs:
  - name: Legacy YAML Agent
    process: serial
    trigger_type: manual
    trigger_name: legacy_yaml
    steps:
      - action: PluginExecution
        params: {}
`
	err := ValidateAgentConfig([]byte(invalidYAML), "yaml", ValidationModeLenient, nil)
	if err == nil {
		t.Fatal("expected invalid yaml to fail schema validation")
	}
	if !strings.Contains(err.Error(), "plugin_name") {
		t.Fatalf("expected plugin_name guidance in error, got: %v", err)
	}
}

func TestValidateAgentConfigSemanticRulesStrict(t *testing.T) {
	strictBad := `{
  "agent_identity": {"name": "@bad name", "agent_id": "bad-agent"},
  "jobs": [
    {
      "name": "bad*job",
      "process": "serial",
      "trigger_type": "manual",
      "trigger_name": "strict_bad",
      "steps": [
        {
          "action": "Decision",
          "params": {
            "condition": {
              "condition_type": "if",
              "expression": "true",
              "on_true": {"agent_name": "unknown_agent"},
              "on_false": {"agent_name": "unknown_agent"}
            }
          }
        }
      ]
    }
  ]
}`

	err := ValidateAgentConfig([]byte(strictBad), "json", ValidationModeStrict, nil)
	if err == nil {
		t.Fatal("expected strict semantic validation to fail")
	}
	msg := err.Error()
	if !strings.Contains(msg, "agent_identity.name") {
		t.Fatalf("expected agent_identity.name semantic error, got: %v", msg)
	}
	if !strings.Contains(msg, "jobs[0].name") {
		t.Fatalf("expected job name semantic error, got: %v", msg)
	}
	if !strings.Contains(msg, "Decision target is not resolvable") {
		t.Fatalf("expected unresolved decision target semantic error, got: %v", msg)
	}
}

func TestValidateAgentConfigStrictVsLenientFixtureBehavior(t *testing.T) {
	fixturePath := filepath.Clean("testdata/strict.invalid.decision.json")
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("failed to load fixture %s: %v", fixturePath, err)
	}

	if err := ValidateAgentConfig(data, "json", ValidationModeLenient, nil); err != nil {
		t.Fatalf("expected lenient mode to accept fixture, got: %v", err)
	}
	if err := ValidateAgentConfig(data, "json", ValidationModeStrict, nil); err == nil {
		t.Fatal("expected strict mode to reject unresolved decision fixture")
	}
}

func TestValidateAgentConfigLegacyRegressionLenient(t *testing.T) {
	legacyPath := filepath.Clean("testdata/legacy.valid.yaml")
	data, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatalf("failed to load fixture %s: %v", legacyPath, err)
	}
	if err := ValidateAgentConfig(data, "yaml", ValidationModeLenient, nil); err != nil {
		t.Fatalf("expected legacy config accepted in lenient mode, got: %v", err)
	}
}

func TestValidateAgentConfigErrorSnapshotPaths(t *testing.T) {
	invalid := `{
  "jobs": [
    {
      "name": "x",
      "process": "serial",
      "trigger_type": "manual",
      "trigger_name": "snap",
      "steps": [
        {
          "action": "PluginExecution",
          "params": {}
        }
      ]
    }
  ]
}`

	err := ValidateAgentConfig([]byte(invalid), "json", ValidationModeLenient, nil)
	if err == nil {
		t.Fatal("expected validation error for missing plugin_name")
	}
	expectedFragments := []string{"jobs", "plugin_name"}
	for _, frag := range expectedFragments {
		if !strings.Contains(err.Error(), frag) {
			t.Fatalf("expected error snapshot to contain %q, got: %v", frag, err)
		}
	}
}
