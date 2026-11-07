package agent

import (
	"context"
	"sync/atomic"
	"testing"
)

// liveProbeTool is a controlled, side-effect-free tool for the live goal test.
// It exercises the real model -> authorization -> execution -> evidence path
// without depending on production crawl data.
type liveProbeTool struct {
	executed int32
	sourceID uint64
}

func (t *liveProbeTool) Name() string { return "live_probe" }

func (t *liveProbeTool) Description() string {
	return "Record a source id for a controlled goal-run smoke test. Read-only."
}

func (t *liveProbeTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"source_id": map[string]any{"type": "integer", "minimum": 1},
		},
		"required":             []any{"source_id"},
		"additionalProperties": false,
	}
}

func (t *liveProbeTool) RequiredCapabilities() []string { return nil }

func (t *liveProbeTool) Execute(ctx context.Context, args map[string]any) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sourceID, err := toSourceIDArg(args["source_id"])
	if err != nil {
		return nil, err
	}
	t.sourceID = sourceID
	atomic.StoreInt32(&t.executed, 1)
	return map[string]any{"ok": true, "source_id": sourceID}, nil
}

func (t *liveProbeTool) EvidenceKind() EvidenceKind     { return EvidenceToolObservation }
func (t *liveProbeTool) EvidenceTrustClass() TrustClass { return TrustToolObservation }
func (t *liveProbeTool) IsReadOnly() bool               { return true }

type liveGoalStatusProvider struct {
	tool  *liveProbeTool
	calls int32
}

func (p *liveGoalStatusProvider) Kind() EvidenceKind { return EvidenceSourceStatus }

func (p *liveGoalStatusProvider) Read(ctx context.Context, req EvidenceRequest) ([]EvidenceObservation, error) {
	atomic.AddInt32(&p.calls, 1)
	if err := ctx.Err(); err != nil {
		return nil, ErrEvidenceUnavailable
	}
	if atomic.LoadInt32(&p.tool.executed) == 0 || p.tool.sourceID != 7 {
		return nil, nil
	}
	return []EvidenceObservation{{
		Kind: EvidenceSourceStatus, Producer: "live-fixture", SourceIDs: []uint64{7},
		SourceUID: "live-fixture-uid", Operation: "read",
		ObservedAt: req.Now, DataUpdatedAt: req.Now,
		Payload: map[string]any{"source_id": 7, "status": "completed"},
	}}, nil
}

func liveGoalSpec(t *testing.T, baseURL, model, prompt string) *AgentGoalSpec {
	t.Helper()
	params := goalTestParams()
	params["provider"] = "ollama"
	params["url"] = baseURL
	params["model"] = model
	params["prompt"] = prompt
	params["allowlist"] = []any{"live_probe"}
	params["allowed_sources"] = []any{float64(7)}
	params["evidence"] = map[string]any{
		"allow":           []any{"source_status", "tool_observation"},
		"default_max_age": "15m", "max_payload_bytes": float64(4096),
	}
	params["budgets"] = map[string]any{
		"max_model_rounds": float64(3), "max_tool_calls": float64(2),
		"max_evidence_records": float64(8), "max_evidence_bytes": float64(32768),
		"max_total_result_bytes": float64(65536), "max_elapsed": "90s",
		"max_consecutive_denials": float64(2),
	}
	params["acceptance"] = map[string]any{
		"mode": "all",
		"criteria": []any{map[string]any{
			"id": "source_status_observed", "description": "Source 7 status is observed.",
			"predicate": map[string]any{
				"kind": "evidence_count", "evidence": "source_status",
				"where": map[string]any{"source_id": float64(7)},
				"op":    "gte", "value": float64(1),
			},
		}},
	}
	spec, err := parseAgentGoalSpec(params, goalTestIdentity(), "Goal Agent", 0)
	if err != nil {
		t.Fatalf("live goal spec: %v", err)
	}
	return spec
}

func liveGoalParams(t *testing.T, spec *AgentGoalSpec, toolRegistry *AgentToolRegistry, evidenceRegistry *GoalEvidenceRegistry) map[string]any {
	t.Helper()
	params := goalActionParams(spec, goalTestIdentity(), nil, evidenceRegistry)
	config := params[StrConfig].(map[string]any)
	config["goal_tool_registry"] = toolRegistry
	return params
}

func TestAgentGoalRunLiveOllamaOptional(t *testing.T) {
	baseURL, model := ollamaSmokeConfig(t)
	resetLLMProvidersForTest()
	RegisterLLMProvider(NewOllamaProvider([]string{model}))
	t.Cleanup(func() { resetLLMProvidersForTest() })
	AgentsEngine = NewJobEngine()
	t.Cleanup(func() { AgentsEngine = nil })

	tool := &liveProbeTool{}
	toolRegistry := NewAgentToolRegistry()
	if err := toolRegistry.Register(tool); err != nil {
		t.Fatalf("register live tool: %v", err)
	}
	statusProvider := &liveGoalStatusProvider{tool: tool}
	evidenceRegistry := NewGoalEvidenceRegistry()
	if err := evidenceRegistry.Register(statusProvider); err != nil {
		t.Fatalf("register live evidence: %v", err)
	}
	spec := liveGoalSpec(t, baseURL, model,
		"You must call the live_probe tool with source_id 7 before answering. After the tool result, reply with the single word done.")
	result, err := (&AgentGoalRunAction{}).Execute(liveGoalParams(t, spec, toolRegistry, evidenceRegistry))
	if err != nil {
		t.Fatalf("live goal run failed: %v (%v)", err, result[StrMessage])
	}
	output := result[StrResponse].(map[string]any)
	if output["status"] != string(GoalCriteriaMet) {
		t.Fatalf("live goal did not meet criteria: %v", output)
	}
	if atomic.LoadInt32(&tool.executed) == 0 {
		t.Fatalf("model did not execute the authorized tool")
	}
	toolCalls := output["tool_calls"].([]any)
	if len(toolCalls) == 0 {
		t.Fatalf("no tool-call summary in live result")
	}
	foundExecuted := false
	for _, item := range toolCalls {
		call := item.(map[string]any)
		if call["name"] == "live_probe" && call["status"] == "executed" {
			foundExecuted = true
		}
	}
	if !foundExecuted {
		t.Fatalf("live_probe was not recorded as executed: %v", toolCalls)
	}
	evidence := output["evidence"].([]any)
	foundStatus, foundTool := false, false
	for _, item := range evidence {
		record := item.(map[string]any)
		switch record["kind"] {
		case string(EvidenceSourceStatus):
			foundStatus = true
		case string(EvidenceToolObservation):
			foundTool = true
		}
	}
	if !foundStatus || !foundTool {
		t.Fatalf("live evidence missing source status/tool observation: %v", evidence)
	}
	decision := output["completion_decision"].(map[string]any)
	if decision["criteria_met"] != true || decision["model_claim_used"] != false {
		t.Fatalf("live completion decision wrong: %v", decision)
	}
	audit := AgentsEngine.AuditEvents()
	seen := map[string]bool{}
	for _, event := range audit {
		if event.Action == "AgentGoalRun" {
			seen[event.ReasonCode] = true
		}
	}
	for _, code := range []string{"goal_run_started", "evidence_recorded", "criteria_all_met"} {
		if !seen[code] {
			t.Fatalf("live audit missing %q: %+v", code, audit)
		}
	}
	t.Logf("live goal run result: status=%v model_rounds=%v tool_calls=%d evidence=%d",
		output["status"], output["model_rounds"], len(toolCalls), len(evidence))
}

func TestAgentGoalRunLiveOllamaTextClaimFailsOptional(t *testing.T) {
	baseURL, model := ollamaSmokeConfig(t)
	resetLLMProvidersForTest()
	RegisterLLMProvider(NewOllamaProvider([]string{model}))
	t.Cleanup(func() { resetLLMProvidersForTest() })

	tool := &liveProbeTool{}
	toolRegistry := NewAgentToolRegistry()
	if err := toolRegistry.Register(tool); err != nil {
		t.Fatalf("register live tool: %v", err)
	}
	// This provider never proves source status because the probe does not set
	// its source identity; a text-only success claim must remain incomplete.
	statusProvider := &liveGoalStatusProvider{tool: &liveProbeTool{}}
	evidenceRegistry := NewGoalEvidenceRegistry()
	if err := evidenceRegistry.Register(statusProvider); err != nil {
		t.Fatalf("register live evidence: %v", err)
	}
	spec := liveGoalSpec(t, baseURL, model,
		"Do not call any tools. Reply with the single word done. This is a text-only negative test.")
	result, err := (&AgentGoalRunAction{}).Execute(liveGoalParams(t, spec, toolRegistry, evidenceRegistry))
	if err == nil {
		t.Fatalf("text-only claim unexpectedly succeeded")
	}
	output := result[StrResponse].(map[string]any)
	if output["status"] == string(GoalCriteriaMet) {
		t.Fatalf("text-only claim produced criteria_met: %v", output)
	}
	if decision, ok := output["completion_decision"].(map[string]any); ok {
		if decision["criteria_met"] == true {
			t.Fatalf("text-only claim completion decision was success: %v", decision)
		}
	}
	t.Logf("live text-only result: status=%v reason=%v", output["status"], output["reason_code"])
}

var _ AgentTool = (*liveProbeTool)(nil)
var _ EvidenceSource = (*liveProbeTool)(nil)
var _ EvidenceProvider = (*liveGoalStatusProvider)(nil)

// TestAgentGoalRunLiveOllamaCapabilityDenialOptional proves that missing
// capabilities deny the run before any provider HTTP request or tool
// execution, even when a real Ollama provider is registered and reachable.
type countingLiveProvider struct {
	inner LLMProvider
	calls int32
}

func (p *countingLiveProvider) Name() string { return p.inner.Name() }

func (p *countingLiveProvider) Execute(req LLMRequest) (map[string]interface{}, error) {
	atomic.AddInt32(&p.calls, 1)
	return p.inner.Execute(req)
}

func TestAgentGoalRunLiveOllamaCapabilityDenialOptional(t *testing.T) {
	baseURL, model := ollamaSmokeConfig(t)
	resetLLMProvidersForTest()
	provider := &countingLiveProvider{inner: NewOllamaProvider([]string{model})}
	RegisterLLMProvider(provider)
	t.Cleanup(func() { resetLLMProvidersForTest() })
	AgentsEngine = NewJobEngine()
	t.Cleanup(func() { AgentsEngine = nil })

	tool := &liveProbeTool{}
	toolRegistry := NewAgentToolRegistry()
	if err := toolRegistry.Register(tool); err != nil {
		t.Fatalf("register live tool: %v", err)
	}
	evidenceRegistry := NewGoalEvidenceRegistry()
	if err := evidenceRegistry.Register(&liveGoalStatusProvider{tool: tool}); err != nil {
		t.Fatalf("register live evidence: %v", err)
	}
	spec := liveGoalSpec(t, baseURL, model,
		"You must call the live_probe tool with source_id 7 before answering.")

	identity := goalTestIdentity()
	identity.Capabilities = []string{"goal_execution", "db_read"} // no ai_reasoning
	params := goalActionParams(spec, identity, nil, evidenceRegistry)
	params[StrConfig].(map[string]any)["goal_tool_registry"] = toolRegistry
	result, err := (&AgentGoalRunAction{}).Execute(params)
	if err == nil {
		t.Fatalf("run without ai_reasoning must not succeed")
	}
	output := result[StrResponse].(map[string]any)
	if output["status"] != string(GoalDenied) || output["reason_code"] != "missing_ai_reasoning" {
		t.Fatalf("unexpected denial result: %v", output)
	}
	if atomic.LoadInt32(&tool.executed) != 0 {
		t.Fatalf("tool executed despite missing ai_reasoning")
	}
	if got := atomic.LoadInt32(&provider.calls); got != 0 {
		t.Fatalf("provider received %d calls despite missing ai_reasoning", got)
	}
}
