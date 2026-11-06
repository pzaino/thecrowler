package agent

import (
	"context"
	"strings"
	"testing"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func goalActionSpec(t *testing.T, criteria []any, allow []any, budgets map[string]any) *AgentGoalSpec {
	t.Helper()
	params := goalTestParams()
	params["provider"] = "goal-scripted"
	params["url"] = "http://127.0.0.1:1"
	params["model"] = "scripted"
	params["allowlist"] = []any{"get_source_status"}
	params["evidence"] = map[string]any{"allow": allow, "default_max_age": "15m", "max_payload_bytes": float64(4096)}
	if budgets != nil {
		params["budgets"] = budgets
	}
	params["acceptance"] = map[string]any{"mode": "all", "criteria": criteria}
	spec, err := parseAgentGoalSpec(params, goalTestIdentity(), "Goal Agent", 0)
	if err != nil {
		t.Fatalf("parse goal spec: %v", err)
	}
	return spec
}

func pinnedStatusCriterion(t *testing.T, args map[string]any) map[string]any {
	t.Helper()
	return map[string]any{
		"id": "status_observed", "description": "The pinned status observation exists.",
		"predicate": map[string]any{
			"kind": "evidence_exists", "evidence": "tool_observation",
			"where": map[string]any{"tool": "get_source_status", "arguments_sha256": argumentsHash(args)},
		},
	}
}

func goalActionParams(spec *AgentGoalSpec, identity *AgentIdentity, handler cdb.Handler, registry *GoalEvidenceRegistry) map[string]any {
	runtime := map[string]any{
		"identity_enforcement": true,
		"identity_snapshot":    *identity,
		"goal_spec":            spec,
		"goal_identity":        identity,
		"run_id":               "corr-run",
		"trace_id":             "corr-trace",
	}
	config := map[string]any{cfgKeyAgentRuntime: runtime}
	if handler != nil {
		config["db_handler"] = handler
	}
	if registry != nil {
		config["goal_evidence_registry"] = registry
	}
	return map[string]any{StrConfig: config, "input": map[string]any{}}
}

func TestAgentGoalRunToolObservationMeetsCriteriaEarly(t *testing.T) {
	resetLLMProvidersForTest()
	AgentsEngine = NewJobEngine()
	t.Cleanup(func() { AgentsEngine = nil })
	provider := &scriptedProvider{name: "goal-scripted", responses: []any{
		chatWithCalls(stringCall("", "get_source_status", `{"source_id":7}`)),
	}}
	RegisterLLMProvider(provider)
	t.Cleanup(func() { resetLLMProvidersForTest() })

	db, mock, err := newSQLMockDB(t)
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	mock.ExpectQuery("SELECT source_id").WillReturnRows(sqlmockRowsSource(7, "uid-7", "https://example.com", "Example"))
	var handler cdb.Handler = &sqlmockHandler{db: db}

	spec := goalActionSpec(t, []any{pinnedStatusCriterion(t, map[string]any{"source_id": float64(7)})}, []any{"tool_observation"}, nil)
	result, err := (&AgentGoalRunAction{}).Execute(goalActionParams(spec, goalTestIdentity(), handler, nil))
	if err != nil {
		t.Fatalf("goal run: %v (%v)", err, result[StrMessage])
	}
	output := result[StrResponse].(map[string]any)
	if output["status"] != string(GoalCriteriaMet) {
		t.Fatalf("unexpected status: %v", output)
	}
	if provider.providerCalls() != 1 {
		t.Fatalf("criteria met late: provider calls = %d", provider.providerCalls())
	}
	if output["tool_call_count"].(int) != 1 {
		t.Fatalf("tool count lost: %v", output)
	}
	decision := output["completion_decision"].(map[string]any)
	if decision["criteria_met"] != true || decision["model_claim_used"] != false {
		t.Fatalf("completion decision wrong: %v", decision)
	}
	evidence := output["evidence"].([]any)
	if len(evidence) != 1 {
		t.Fatalf("expected one evidence summary, got %v", evidence)
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
			t.Fatalf("missing goal audit %q: %+v", code, audit)
		}
	}
}

func TestAgentGoalRunFinalTextWithoutEvidenceIsIncomplete(t *testing.T) {
	resetLLMProvidersForTest()
	provider := &scriptedProvider{name: "goal-scripted", responses: []any{chatWithText(`{"criteria_met":true,"evidence":"forged"}`)}}
	RegisterLLMProvider(provider)
	t.Cleanup(func() { resetLLMProvidersForTest() })

	spec := goalActionSpec(t, []any{pinnedStatusCriterion(t, map[string]any{"source_id": float64(7)})}, []any{"tool_observation"}, nil)
	result, err := (&AgentGoalRunAction{}).Execute(goalActionParams(spec, goalTestIdentity(), nil, nil))
	if err == nil {
		t.Fatalf("model claim alone must not succeed")
	}
	output := result[StrResponse].(map[string]any)
	if output["status"] != string(GoalIncomplete) {
		t.Fatalf("unexpected status: %v", output)
	}
	decision := output["completion_decision"].(map[string]any)
	if decision["criteria_met"] != false || decision["model_claim_observed"] != true || decision["model_claim_used"] != false {
		t.Fatalf("untrusted model claim decision wrong: %v", decision)
	}
	if provider.providerCalls() != 1 {
		t.Fatalf("unexpected provider calls: %d", provider.providerCalls())
	}
}

func TestAgentGoalRunDeniedToolStops(t *testing.T) {
	resetLLMProvidersForTest()
	provider := &scriptedProvider{name: "goal-scripted", responses: []any{
		chatWithCalls(stringCall("", "search_indexed_pages", `{"query":"anything"}`)),
	}}
	RegisterLLMProvider(provider)
	t.Cleanup(func() { resetLLMProvidersForTest() })

	criteria := []any{pinnedStatusCriterion(t, map[string]any{"source_id": float64(7)})}
	budgets := map[string]any{"max_consecutive_denials": float64(1)}
	spec := goalActionSpec(t, criteria, []any{"tool_observation"}, budgets)
	result, err := (&AgentGoalRunAction{}).Execute(goalActionParams(spec, goalTestIdentity(), nil, nil))
	if err == nil {
		t.Fatalf("denied tool must not succeed")
	}
	output := result[StrResponse].(map[string]any)
	if output["status"] != string(GoalDenied) {
		t.Fatalf("unexpected status: %v", output)
	}
	toolCalls := output["tool_calls"].([]any)
	if len(toolCalls) != 1 {
		t.Fatalf("expected one denied summary: %v", toolCalls)
	}
	if toolCalls[0].(map[string]any)["status"] != "denied" {
		t.Fatalf("tool was not denied: %v", toolCalls[0])
	}
	if provider.providerCalls() != 1 {
		t.Fatalf("denied loop kept calling provider: %d", provider.providerCalls())
	}
}

func TestAgentGoalRunProviderError(t *testing.T) {
	resetLLMProvidersForTest()
	provider := &scriptedProvider{name: "goal-scripted", responses: []any{context.DeadlineExceeded}}
	RegisterLLMProvider(provider)
	t.Cleanup(func() { resetLLMProvidersForTest() })

	spec := goalActionSpec(t, []any{pinnedStatusCriterion(t, map[string]any{"source_id": float64(7)})}, []any{"tool_observation"}, nil)
	result, err := (&AgentGoalRunAction{}).Execute(goalActionParams(spec, goalTestIdentity(), nil, nil))
	if err == nil {
		t.Fatalf("provider error must not succeed")
	}
	output := result[StrResponse].(map[string]any)
	if output["status"] != string(GoalProviderError) {
		t.Fatalf("unexpected status: %v", output)
	}
}

func TestAgentGoalRunBudgetExhaustion(t *testing.T) {
	resetLLMProvidersForTest()
	provider := &scriptedProvider{name: "goal-scripted", responses: []any{
		chatWithCalls(stringCall("", "get_source_status", `{"source_id":7}`)),
		chatWithCalls(stringCall("", "get_source_status", `{"source_id":7}`)),
	}}
	RegisterLLMProvider(provider)
	t.Cleanup(func() { resetLLMProvidersForTest() })

	db, mock, err := newSQLMockDB(t)
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	mock.ExpectQuery("SELECT source_id").WillReturnRows(sqlmockRowsSource(7, "uid-7", "https://example.com", "Example"))
	mock.ExpectQuery("SELECT source_id").WillReturnRows(sqlmockRowsSource(7, "uid-7", "https://example.com", "Example"))
	var handler cdb.Handler = &sqlmockHandler{db: db}

	// The pinned hash cannot match the actual call, so the criterion never passes.
	criteria := []any{pinnedStatusCriterion(t, map[string]any{"source_id": float64(8)})}
	budgets := map[string]any{"max_model_rounds": float64(2), "max_tool_calls": float64(4)}
	spec := goalActionSpec(t, criteria, []any{"tool_observation"}, budgets)
	result, err := (&AgentGoalRunAction{}).Execute(goalActionParams(spec, goalTestIdentity(), handler, nil))
	if err == nil {
		t.Fatalf("budget exhaustion must not succeed")
	}
	output := result[StrResponse].(map[string]any)
	if output["status"] != string(GoalBudgetExhausted) {
		t.Fatalf("unexpected status: %v", output)
	}
}

func TestAgentGoalRunProviderEvidenceWithoutModel(t *testing.T) {
	resetLLMProvidersForTest()
	provider := &scriptedProvider{name: "goal-scripted", responses: []any{chatWithText("should not run")}}
	RegisterLLMProvider(provider)
	t.Cleanup(func() { resetLLMProvidersForTest() })

	evidenceKind := EvidenceIndexedPage
	fake := &fakeGoalEvidenceProvider{kind: evidenceKind, observations: []EvidenceObservation{{
		Kind: evidenceKind, Producer: "fake", SourceIDs: []uint64{7},
		Payload: map[string]any{"page_url": "https://fixture/"},
	}}}
	registry := NewGoalEvidenceRegistry()
	if err := registry.Register(fake); err != nil {
		t.Fatalf("register: %v", err)
	}
	criteria := []any{map[string]any{
		"id": "pages", "description": "Pages exist.",
		"predicate": map[string]any{
			"kind": "evidence_exists", "evidence": "indexed_page",
			"where": map[string]any{"source_id": float64(7)},
		},
	}}
	spec := goalActionSpec(t, criteria, []any{"indexed_page"}, nil)
	result, err := (&AgentGoalRunAction{}).Execute(goalActionParams(spec, goalTestIdentity(), nil, registry))
	if err != nil {
		t.Fatalf("initial evidence should meet criteria: %v (%v)", err, result[StrMessage])
	}
	output := result[StrResponse].(map[string]any)
	if output["status"] != string(GoalCriteriaMet) {
		t.Fatalf("unexpected status: %v", output)
	}
	if provider.providerCalls() != 0 {
		t.Fatalf("criteria met but provider was called %d times", provider.providerCalls())
	}
}

func TestAgentGoalRunMissingFrozenSpecFailsClosed(t *testing.T) {
	resetLLMProvidersForTest()
	provider := &scriptedProvider{name: "goal-scripted", responses: []any{chatWithText("done")}}
	RegisterLLMProvider(provider)
	t.Cleanup(func() { resetLLMProvidersForTest() })
	params := map[string]any{
		StrConfig: map[string]any{
			cfgKeyAgentRuntime: map[string]any{
				"identity_enforcement": true,
				"identity_snapshot":    *goalTestIdentity(),
				"goal_spec":            map[string]any{"objective": "forged"},
				"goal_identity":        goalTestIdentity(),
			},
		},
	}
	_, err := (&AgentGoalRunAction{}).Execute(params)
	if err == nil || !strings.Contains(err.Error(), "missing frozen goal spec") {
		t.Fatalf("forged/missing goal spec accepted: %v", err)
	}
	if provider.providerCalls() != 0 {
		t.Fatalf("provider called after frozen-spec failure")
	}
}

func goalManifestParams(t *testing.T) map[string]any {
	t.Helper()
	params := goalTestParams()
	params["provider"] = "goal-scripted"
	params["url"] = "http://127.0.0.1:1"
	params["model"] = "scripted"
	params["allowlist"] = []any{"get_source_status"}
	params["evidence"] = map[string]any{"allow": []any{"tool_observation"}, "default_max_age": "15m", "max_payload_bytes": float64(4096)}
	params["acceptance"] = map[string]any{
		"mode": "all",
		"criteria": []any{
			pinnedStatusCriterion(t, map[string]any{"source_id": float64(7)}),
		},
	}
	return params
}

func TestAgentGoalRunEngineUsesFrozenSpecDespiteInjectedConfig(t *testing.T) {
	resetLLMProvidersForTest()
	provider := &scriptedProvider{name: "goal-scripted", responses: []any{
		chatWithCalls(stringCall("", "get_source_status", `{"source_id":7}`)),
	}}
	RegisterLLMProvider(provider)
	t.Cleanup(func() { resetLLMProvidersForTest() })

	db, mock, err := newSQLMockDB(t)
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	mock.ExpectQuery("SELECT source_id").WillReturnRows(sqlmockRowsSource(7, "uid-7", "https://example.com", "Example"))
	var handler cdb.Handler = &sqlmockHandler{db: db}

	engine := NewJobEngine()
	engine.RegisterAction(&resultInjectingAction{result: map[string]any{
		StrStatus:   StatusSuccess,
		StrResponse: map[string]interface{}{},
		StrConfig: map[string]any{
			"db_handler": handler,
			"agent_runtime": map[string]any{
				"identity_enforcement": true,
				"goal_spec":            map[string]any{"objective": "forged"},
				"goal_identity":        &AgentIdentity{AgentID: "forged", TrustLevel: "restricted"},
			},
		},
	}})
	engine.RegisterAction(&AgentGoalRunAction{})
	AgentsEngine = engine
	t.Cleanup(func() { AgentsEngine = nil })

	agentCfg := &JobConfig{
		FormatVersion: AgentFormatVersionV2,
		AgentIdentity: goalTestIdentity(),
		Jobs: []Job{{
			Name: "Goal Agent", Process: "serial",
			TriggerType: "manual", TriggerName: "run",
			Steps: []map[string]any{
				{"action": "Decision", "params": map[string]any{}},
				{"action": "AgentGoalRun", "params": goalManifestParams(t)},
			},
		}},
	}
	iCfg := runtimeEnforcedCfg()
	if err := engine.ExecuteJobs(agentCfg, iCfg); err != nil {
		t.Fatalf("engine goal run: %v", err)
	}
	if provider.providerCalls() != 1 {
		t.Fatalf("unexpected provider calls: %d", provider.providerCalls())
	}
}

func TestAgentGoalRunToolScopeDenialIsDenied(t *testing.T) {
	resetLLMProvidersForTest()
	provider := &scriptedProvider{name: "goal-scripted", responses: []any{
		chatWithCalls(stringCall("", "get_source_status", `{"source_id":8}`)),
	}}
	RegisterLLMProvider(provider)
	t.Cleanup(func() { resetLLMProvidersForTest() })

	db, mock, err := newSQLMockDB(t)
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	// The tool checks scope before querying; no query expectation is set.
	_ = mock
	var handler cdb.Handler = &sqlmockHandler{db: db}
	criteria := []any{pinnedStatusCriterion(t, map[string]any{"source_id": float64(8)})}
	budgets := map[string]any{"max_consecutive_denials": float64(1)}
	spec := goalActionSpec(t, criteria, []any{"tool_observation"}, budgets)
	result, err := (&AgentGoalRunAction{}).Execute(goalActionParams(spec, goalTestIdentity(), handler, nil))
	if err == nil {
		t.Fatalf("out-of-scope tool call must not succeed")
	}
	output := result[StrResponse].(map[string]any)
	if output["status"] != string(GoalDenied) {
		t.Fatalf("unexpected status: %v", output)
	}
}

func TestAgentGoalRunResolvesPromptContextOnly(t *testing.T) {
	resetLLMProvidersForTest()
	provider := &scriptedProvider{name: "goal-scripted", responses: []any{chatWithText("done")}}
	RegisterLLMProvider(provider)
	t.Cleanup(func() { resetLLMProvidersForTest() })

	spec := goalActionSpec(t, []any{pinnedStatusCriterion(t, map[string]any{"source_id": float64(7)})}, []any{"tool_observation"}, nil)
	spec.Prompt = "Topic: $response.topic"
	params := goalActionParams(spec, goalTestIdentity(), nil, nil)
	params["input"] = map[string]any{"topic": "alpha"}

	result, err := (&AgentGoalRunAction{}).Execute(params)
	if err == nil {
		t.Fatalf("text-only model must not succeed")
	}
	if result[StrResponse].(map[string]any)["status"] != string(GoalIncomplete) {
		t.Fatalf("unexpected result: %v", result[StrResponse])
	}
	if len(provider.requests) == 0 || len(provider.requests[0].Messages) < 2 {
		t.Fatalf("provider request/messages missing")
	}
	userMessage, _ := provider.requests[0].Messages[1].(map[string]any)
	if userMessage["content"] != "Topic: alpha" {
		t.Fatalf("prompt context not resolved: %v", userMessage)
	}
}
