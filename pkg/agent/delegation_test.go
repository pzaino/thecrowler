package agent

import (
	"strings"
	"testing"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

type delegationRecorderAction struct {
	name string
	fn   func(params map[string]interface{})
}

func (a *delegationRecorderAction) Name() string { return a.name }

func (a *delegationRecorderAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
	if a.fn != nil {
		a.fn(params)
	}
	return map[string]interface{}{StrStatus: StatusSuccess, StrResponse: map[string]interface{}{}}, nil
}

func makeDelegatingAgent(agentID, name, trust string, capabilities []string, decisionStep map[string]interface{}) *JobConfig {
	return &JobConfig{
		FormatVersion: AgentFormatVersionV2,
		AgentIdentity: &AgentIdentity{AgentID: agentID, Name: name, TrustLevel: trust, Capabilities: capabilities},
		Jobs: []Job{{
			Name: name, Process: "serial", TriggerType: "manual", TriggerName: "run",
			Steps: []map[string]interface{}{{"action": "Decision", "params": map[string]interface{}{"condition": decisionStep}}},
		}},
	}
}

func makeMarkerAgent(agentID, name, trust string) *JobConfig {
	return &JobConfig{
		FormatVersion: AgentFormatVersionV2,
		AgentIdentity: &AgentIdentity{AgentID: agentID, Name: name, TrustLevel: trust, Capabilities: []string{"all"}},
		Jobs: []Job{{
			Name: name, Process: "serial", TriggerType: "manual", TriggerName: "run",
			Steps: []map[string]interface{}{{"action": "Marker", "params": map[string]interface{}{}}},
		}},
	}
}

func enforcedRuntimeCfg() map[string]any {
	return map[string]any{cfgKeyAgentRuntime: cfg.AgentRuntimeConfig{IdentityEnforcement: true}}
}

func TestDecisionDelegationResolveByAgentID(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&DecisionAction{})
	calls := map[string]int{}
	engine.RegisterAction(&delegationRecorderAction{name: "Marker", fn: func(params map[string]interface{}) { calls["id-target"]++ }})

	AgentsEngine = engine
	AgentsRegistry = NewJobConfig()

	source := makeDelegatingAgent("source-id", "Source", "trusted", []string{"decision", "delegate"}, map[string]interface{}{
		"condition_type": "if", "expression": "true", "on_true": map[string]interface{}{"agent_id": "id-target"},
	})
	target := makeMarkerAgent("id-target", "Target", "trusted")
	AgentsRegistry.RegisterAgent(source)
	AgentsRegistry.RegisterAgent(target)

	if err := engine.ExecuteAgent("source-id", enforcedRuntimeCfg()); err != nil {
		t.Fatalf("expected delegation by id to succeed, got %v", err)
	}
	if calls["id-target"] != 1 {
		t.Fatalf("expected id target to run once, got %d", calls["id-target"])
	}
}

func TestDecisionDelegationResolveByAgentName(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&DecisionAction{})
	calls := map[string]int{}
	engine.RegisterAction(&delegationRecorderAction{name: "Marker", fn: func(params map[string]interface{}) { calls["name-target"]++ }})

	AgentsEngine = engine
	AgentsRegistry = NewJobConfig()

	source := makeDelegatingAgent("source-id", "Source", "trusted", []string{"decision", "delegate"}, map[string]interface{}{
		"condition_type": "if", "expression": "true", "on_true": map[string]interface{}{"agent_name": "Name Target"},
	})
	target := makeMarkerAgent("name-target", "Name Target", "trusted")
	AgentsRegistry.RegisterAgent(source)
	AgentsRegistry.RegisterAgent(target)

	if err := engine.ExecuteAgent("source-id", enforcedRuntimeCfg()); err != nil {
		t.Fatalf("expected delegation by name to succeed, got %v", err)
	}
	if calls["name-target"] != 1 {
		t.Fatalf("expected name target to run once, got %d", calls["name-target"])
	}
}

func TestDecisionDelegationPrefersAgentIDWhenBothPresent(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&DecisionAction{})
	idCalls := 0
	nameCalls := 0
	engine.RegisterAction(&delegationRecorderAction{name: "MarkID", fn: func(params map[string]interface{}) { idCalls++ }})
	engine.RegisterAction(&delegationRecorderAction{name: "MarkName", fn: func(params map[string]interface{}) { nameCalls++ }})

	AgentsEngine = engine
	AgentsRegistry = NewJobConfig()

	source := makeDelegatingAgent("source-id", "Source", "trusted", []string{"decision", "delegate"}, map[string]interface{}{
		"condition_type": "if", "expression": "true", "on_true": map[string]interface{}{"agent_id": "id-target", "agent_name": "Name Target"},
	})
	idTarget := &JobConfig{FormatVersion: AgentFormatVersionV2, AgentIdentity: &AgentIdentity{AgentID: "id-target", Name: "ID Target", TrustLevel: "trusted", Capabilities: []string{"all"}}, Jobs: []Job{{Name: "ID Target", Process: "serial", TriggerType: "manual", TriggerName: "run", Steps: []map[string]interface{}{{"action": "MarkID", "params": map[string]interface{}{}}}}}}
	nameTarget := &JobConfig{FormatVersion: AgentFormatVersionV2, AgentIdentity: &AgentIdentity{AgentID: "name-target", Name: "Name Target", TrustLevel: "trusted", Capabilities: []string{"all"}}, Jobs: []Job{{Name: "Name Target", Process: "serial", TriggerType: "manual", TriggerName: "run", Steps: []map[string]interface{}{{"action": "MarkName", "params": map[string]interface{}{}}}}}}
	AgentsRegistry.RegisterAgent(source)
	AgentsRegistry.RegisterAgent(idTarget)
	AgentsRegistry.RegisterAgent(nameTarget)

	if err := engine.ExecuteAgent("source-id", enforcedRuntimeCfg()); err != nil {
		t.Fatalf("expected delegation to succeed, got %v", err)
	}
	if idCalls != 1 {
		t.Fatalf("expected id target to run once, got %d", idCalls)
	}
	if nameCalls != 0 {
		t.Fatalf("expected name target to not run when agent_id provided, got %d", nameCalls)
	}
}

func TestDecisionDelegationMissingTarget(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&DecisionAction{})
	AgentsEngine = engine
	AgentsRegistry = NewJobConfig()

	source := makeDelegatingAgent("source-id", "Source", "trusted", []string{"decision", "delegate"}, map[string]interface{}{
		"condition_type": "if", "expression": "true", "on_true": map[string]interface{}{"agent_id": "missing-id"},
	})
	AgentsRegistry.RegisterAgent(source)

	err := engine.ExecuteAgent("source-id", enforcedRuntimeCfg())
	if err == nil || !strings.Contains(err.Error(), "delegation target unavailable") {
		t.Fatalf("expected missing target error, got %v", err)
	}
}

func TestDecisionDelegationCapabilityDenial(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&DecisionAction{})
	engine.RegisterAction(&delegationRecorderAction{name: "Marker"})
	AgentsEngine = engine
	AgentsRegistry = NewJobConfig()

	source := makeDelegatingAgent("source-id", "Source", "trusted", []string{"decision"}, map[string]interface{}{
		"condition_type": "if", "expression": "true", "on_true": map[string]interface{}{"agent_id": "target-id"},
	})
	target := makeMarkerAgent("target-id", "Target", "trusted")
	AgentsRegistry.RegisterAgent(source)
	AgentsRegistry.RegisterAgent(target)

	err := engine.ExecuteAgent("source-id", enforcedRuntimeCfg())
	if err == nil || !strings.Contains(err.Error(), "delegation denied: caller capability missing 'delegate'") {
		t.Fatalf("expected capability denial error, got %v", err)
	}
}

func TestDecisionDelegationTrustMismatchDenial(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&DecisionAction{})
	engine.RegisterAction(&delegationRecorderAction{name: "Marker"})
	AgentsEngine = engine
	AgentsRegistry = NewJobConfig()

	source := makeDelegatingAgent("source-id", "Source", "restricted", []string{"decision", "delegate"}, map[string]interface{}{
		"condition_type": "if", "expression": "true", "on_true": map[string]interface{}{"agent_id": "target-id"},
	})
	target := makeMarkerAgent("target-id", "Target", "privileged")
	AgentsRegistry.RegisterAgent(source)
	AgentsRegistry.RegisterAgent(target)

	err := engine.ExecuteAgent("source-id", enforcedRuntimeCfg())
	if err == nil || !strings.Contains(err.Error(), "delegation denied: caller trust_level") {
		t.Fatalf("expected trust mismatch denial error, got %v", err)
	}
}

func TestDecisionDelegationCycleDetection(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&DecisionAction{})
	engine.RegisterAction(&delegationRecorderAction{name: "Marker"})
	AgentsEngine = engine
	AgentsRegistry = NewJobConfig()

	agentA := makeDelegatingAgent("agent-a", "Agent A", "trusted", []string{"decision", "delegate"}, map[string]interface{}{
		"condition_type": "if", "expression": "true", "on_true": map[string]interface{}{"agent_id": "agent-b"},
	})
	agentB := makeDelegatingAgent("agent-b", "Agent B", "trusted", []string{"decision", "delegate"}, map[string]interface{}{
		"condition_type": "if", "expression": "true", "on_true": map[string]interface{}{"agent_id": "agent-a"},
	})
	AgentsRegistry.RegisterAgent(agentA)
	AgentsRegistry.RegisterAgent(agentB)

	err := engine.ExecuteAgent("agent-a", enforcedRuntimeCfg())
	if err == nil || !strings.Contains(err.Error(), "delegation cycle detected") {
		t.Fatalf("expected cycle detection error, got %v", err)
	}
}

func TestDecisionDelegationIntegrationFlow(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&DecisionAction{})
	calls := 0
	engine.RegisterAction(&delegationRecorderAction{name: "Marker", fn: func(params map[string]interface{}) { calls++ }})
	AgentsEngine = engine
	AgentsRegistry = NewJobConfig()

	source := makeDelegatingAgent("source-id", "Source", "trusted", []string{"decision", "delegate"}, map[string]interface{}{
		"condition_type": "if", "expression": "true", "on_true": map[string]interface{}{"agent_id": "target-id"},
	})
	target := makeMarkerAgent("target-id", "Target", "trusted")
	AgentsRegistry.RegisterAgent(source)
	AgentsRegistry.RegisterAgent(target)

	if err := engine.ExecuteAgent("source-id", enforcedRuntimeCfg()); err != nil {
		t.Fatalf("expected integration delegation flow to pass, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected target action to run once, got %d", calls)
	}
}

func TestRegressionNonDelegatingJobStillRuns(t *testing.T) {
	engine := NewJobEngine()
	calls := 0
	engine.RegisterAction(&delegationRecorderAction{name: "Marker", fn: func(params map[string]interface{}) { calls++ }})

	nonDelegating := &JobConfig{Jobs: []Job{{Name: "Legacy", Process: "serial", TriggerType: "manual", TriggerName: "legacy", Steps: []map[string]interface{}{{"action": "Marker", "params": map[string]interface{}{}}}}}}

	if err := engine.ExecuteJobs(nonDelegating, map[string]any{cfgKeyAgentRuntime: cfg.AgentRuntimeConfig{IdentityEnforcement: false}}); err != nil {
		t.Fatalf("expected non-delegating flow to remain unchanged, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected marker action to run once, got %d", calls)
	}
}
