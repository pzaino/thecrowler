package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// loadSchemaDoc parses the repository agent JSON schema for parity checks.
func loadSchemaDoc(t *testing.T) map[string]any {
	t.Helper()
	data, err := loadAgentSchema()
	if err != nil {
		t.Fatalf("load schema: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	return doc
}

func schemaStringEnum(t *testing.T, doc map[string]any, path ...string) []string {
	t.Helper()
	current := any(doc)
	for _, key := range path {
		m, ok := current.(map[string]any)
		if !ok {
			t.Fatalf("schema path %v not found", path)
		}
		current, ok = m[key]
		if !ok {
			t.Fatalf("schema path %v not found", path)
		}
	}
	m, ok := current.(map[string]any)
	if !ok {
		t.Fatalf("schema path %v is not an object", path)
	}
	rawEnum, ok := m["enum"].([]any)
	if !ok {
		t.Fatalf("schema path %v has no enum", path)
	}
	out := make([]string, 0, len(rawEnum))
	for _, item := range rawEnum {
		s, ok := item.(string)
		if !ok {
			t.Fatalf("schema path %v has non-string enum entry", path)
		}
		out = append(out, s)
	}
	return out
}

// TestRuntimeActionsMatchSchemaEnum is the source-of-truth check: every
// registered action appears in the manifest action enum and vice versa.
func TestRuntimeActionsMatchSchemaEnum(t *testing.T) {
	engine := NewJobEngine()
	RegisterActions(engine)

	doc := loadSchemaDoc(t)
	enum := schemaStringEnum(t, doc, "properties", "jobs", "items", "properties", "steps", "items", "properties", "action")

	registered := map[string]bool{}
	for name := range engine.actions {
		registered[name] = true
	}
	for _, entry := range enum {
		if !registered[entry] {
			t.Fatalf("schema action %q has no registered runtime action", entry)
		}
	}
	for name := range registered {
		found := false
		for _, entry := range enum {
			if entry == name {
				found = true
			}
		}
		if !found {
			t.Fatalf("registered action %q missing from schema enum", name)
		}
	}
}

// TestCapabilityParityWithSchema ensures every runtime-required capability
// token validates against the schema. Reserved tokens with no current
// runtime consumer are listed explicitly instead of diverging silently.
func TestCapabilityParityWithSchema(t *testing.T) {
	doc := loadSchemaDoc(t)
	enum := schemaStringEnum(t, doc, "properties", "agent_identity", "properties", "capabilities", "items")
	admitted := map[string]bool{}
	for _, entry := range enum {
		admitted[entry] = true
	}

	// Every canonical runtime token must be schema-admitted.
	required := map[string]bool{"all": true, delegationCapabilityName: true}
	for _, token := range actionCapability {
		required[token] = true
	}
	// DBQuery's write branch and the plugin alias also go through gates.
	required["db_write"] = true
	required["call_plugin"] = true
	for token := range required {
		if !admitted[token] {
			t.Fatalf("runtime capability %q missing from schema enum", token)
		}
	}

	// Explicitly reserved: schema-admitted but consumed by no gate yet.
	reserved := []string{"network_access", "file_system_access", "schedule_event"}
	for _, token := range reserved {
		if !admitted[token] {
			t.Fatalf("reserved capability %q missing from schema enum", token)
		}
	}
}

// TestV1ManifestExecutesUnderEnforcement proves legacy jobs-only manifests
// keep working when enforcement is on (derived `all` grant).
func TestV1ManifestExecutesUnderEnforcement(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&testStepAction{name: "RunCommand"})
	legacy := &JobConfig{Jobs: []Job{{Name: "Legacy", Process: "serial",
		TriggerType: "manual", TriggerName: "legacy",
		Steps: []map[string]interface{}{
			{"action": "RunCommand", "params": map[string]interface{}{"command": "echo hi"}},
		}}}}
	if err := engine.ExecuteJobs(legacy, runtimeEnforcedCfg()); err != nil {
		t.Fatalf("v1 manifest must execute under enforcement via derived grant, got %v", err)
	}
}

// TestRestrictedV2DeniedOperations pins the fail-closed matrix for a
// least-privilege v2 identity under enforcement.
func TestRestrictedV2DeniedOperations(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&testStepAction{name: "APIRequest"})
	engine.RegisterAction(&testStepAction{name: "RunCommand"})
	agentCfg := &JobConfig{
		FormatVersion: AgentFormatVersionV2,
		AgentIdentity: &AgentIdentity{
			AgentID: "least", Name: "Least", TrustLevel: "trusted",
			Capabilities: []string{"api_request"},
		},
		Jobs: []Job{{Name: "Least", Process: "serial", TriggerType: "manual", TriggerName: "run",
			Steps: []map[string]interface{}{
				{"action": "RunCommand", "params": map[string]interface{}{}},
			}}},
	}
	err := engine.ExecuteJobs(agentCfg, runtimeEnforcedCfg())
	if err == nil || !strings.Contains(err.Error(), "capability gate denied action RunCommand") {
		t.Fatalf("expected RunCommand denial, got %v", err)
	}
}
