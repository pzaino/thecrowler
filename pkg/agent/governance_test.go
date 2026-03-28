package agent

import (
	"fmt"
	"strings"
	"testing"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

type governanceAction struct {
	name string
	fn   func(params map[string]interface{}) (map[string]interface{}, error)
}

func (a *governanceAction) Name() string { return a.name }
func (a *governanceAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
	if a.fn != nil {
		return a.fn(params)
	}
	return map[string]interface{}{StrStatus: StatusSuccess, StrResponse: map[string]interface{}{}}, nil
}

func governanceAgent(actions ...string) *JobConfig {
	steps := make([]map[string]interface{}, 0, len(actions))
	for _, action := range actions {
		steps = append(steps, map[string]interface{}{"action": action, "params": map[string]interface{}{}})
	}
	return &JobConfig{
		FormatVersion: AgentFormatVersionV2,
		AgentIdentity: &AgentIdentity{
			AgentID:      "gov-agent",
			Name:         "Governed Agent",
			Owner:        "risk-team",
			TrustLevel:   "trusted",
			Capabilities: []string{"all"},
			Contract:     &AgentContract{},
		},
		Jobs: []Job{{Name: "Governed Agent", Process: "serial", TriggerType: "manual", TriggerName: "run", Steps: steps}},
	}
}

func governanceCfg(identity, contract bool) map[string]any {
	return map[string]any{cfgKeyAgentRuntime: cfg.AgentRuntimeConfig{IdentityEnforcement: identity, ContractEnforcement: contract}}
}

func TestContractForbiddenActionEnforcement(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&governanceAction{name: "RunCommand"})

	agent := governanceAgent("RunCommand")
	agent.AgentIdentity.Contract.ForbiddenActions = []string{"RunCommand"}

	err := engine.ExecuteJobs(agent, governanceCfg(true, true))
	if err == nil || !strings.Contains(err.Error(), "contract gate denied action RunCommand") {
		t.Fatalf("expected contract denial, got %v", err)
	}
	events := engine.AuditEvents()
	if len(events) < 2 || events[len(events)-1].Outcome != auditOutcomeDenied {
		t.Fatalf("expected denied audit event, got %#v", events)
	}
}

func TestFailurePolicyContinueBehavior(t *testing.T) {
	engine := NewJobEngine()
	failed := false
	continued := false
	engine.RegisterAction(&governanceAction{name: "RunCommand", fn: func(params map[string]interface{}) (map[string]interface{}, error) {
		failed = true
		return nil, fmt.Errorf("boom")
	}})
	engine.RegisterAction(&governanceAction{name: "AIInteraction", fn: func(params map[string]interface{}) (map[string]interface{}, error) {
		continued = true
		return map[string]interface{}{StrStatus: StatusSuccess, StrResponse: map[string]interface{}{}}, nil
	}})

	agent := governanceAgent("RunCommand", "AIInteraction")
	agent.AgentIdentity.Contract.FailurePolicy = "continue"

	if err := engine.ExecuteJobs(agent, governanceCfg(true, true)); err != nil {
		t.Fatalf("expected continue policy to avoid fatal error, got %v", err)
	}
	if !failed || !continued {
		t.Fatalf("expected failed first action and continued second action")
	}
}

func TestAuditPayloadAndSnapshotDeterministic(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&governanceAction{name: "AIInteraction"})
	agent := governanceAgent("AIInteraction")

	if err := engine.ExecuteJobs(agent, governanceCfg(true, true)); err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	events := engine.AuditEvents()
	if len(events) < 2 {
		t.Fatalf("expected at least identity and action audit events")
	}
	if events[1].Action != "AIInteraction" || events[1].AgentID != "gov-agent" || events[1].Owner != "risk-team" {
		t.Fatalf("unexpected audit payload: %#v", events[1])
	}
	snap := snapshotAuditEvents(events)
	if !strings.Contains(snap, "\"sequence\": 1") || !strings.Contains(snap, "\"action\": \"AIInteraction\"") {
		t.Fatalf("snapshot missing deterministic entries: %s", snap)
	}
}

func TestBlockedExecutionProducesTrace(t *testing.T) {
	engine := NewJobEngine()
	engine.RegisterAction(&governanceAction{name: "RunCommand"})
	agent := governanceAgent("RunCommand")
	agent.AgentIdentity.Contract.ForbiddenActions = []string{"runcommand"}

	_ = engine.ExecuteJobs(agent, governanceCfg(true, true))
	trace := engine.TraceLogs()
	if len(trace) == 0 || !strings.Contains(trace[len(trace)-1], "outcome=denied") {
		t.Fatalf("expected denied deterministic trace log, got %v", trace)
	}
}

func TestFailurePolicyDeterministicConsistency(t *testing.T) {
	build := func() *JobEngine {
		engine := NewJobEngine()
		engine.RegisterAction(&governanceAction{name: "RunCommand", fn: func(params map[string]interface{}) (map[string]interface{}, error) {
			return nil, fmt.Errorf("boom")
		}})
		engine.RegisterAction(&governanceAction{name: "AIInteraction"})
		return engine
	}
	run := func() []string {
		engine := build()
		agent := governanceAgent("RunCommand", "AIInteraction")
		agent.AgentIdentity.Contract.FailurePolicy = "continue"
		if err := engine.ExecuteJobs(agent, governanceCfg(true, true)); err != nil {
			t.Fatalf("run failed: %v", err)
		}
		return engine.TraceLogs()
	}

	first := run()
	second := run()
	if len(first) != len(second) {
		t.Fatalf("trace length mismatch: %d != %d", len(first), len(second))
	}
	for i := range first {
		f := first[i]
		s := second[i]
		// Run IDs differ; compare stable suffix after first '|'.
		if strings.SplitN(f, "|", 2)[1] != strings.SplitN(s, "|", 2)[1] {
			t.Fatalf("trace mismatch at %d: %q != %q", i, f, s)
		}
	}
}
