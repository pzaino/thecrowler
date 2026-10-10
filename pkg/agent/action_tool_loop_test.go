package agent

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// newSQLMockDB builds a sqlmock database for action-level tests.
func newSQLMockDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock, error) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		return nil, nil, err
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, mock, nil
}

// sqlmockRowsSource builds one source row for GetSourceByID scans.
func sqlmockRowsSource(id int64, uid, url, name string) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"source_id", "source_uid", "url", "name", "priority",
		"sub_priority", "category_id", "usr_id", "restricted", "flags", "config"}).
		AddRow(id, uid, url, name, "high",
			int64(0), int64(1), int64(99), int64(0), int64(0), []byte(`{}`))
}

// loopTestParams builds minimal AgentToolLoop step params.
func loopTestParams() map[string]interface{} {
	return map[string]interface{}{
		StrConfig: map[string]interface{}{
			"db_handler": &fakeDBHandler{},
			cfgKeyAgentRuntime: map[string]interface{}{
				"identity_enforcement": true,
				"identity_snapshot": AgentIdentity{
					AgentID: "op", TrustLevel: "trusted",
					Capabilities: []string{"tool_execution", "db_read"},
				},
			},
		},
		StrRequest:  map[string]interface{}{},
		"url":       "https://example.com/v1/chat",
		"model":     "mock-model",
		"prompt":    "go",
		"allowlist": []any{"get_source_status"},
	}
}

// TestToolLoopActionRequiresEnforcement pins inert-without-enforcement.
func TestToolLoopActionRequiresEnforcement(t *testing.T) {
	a := &AgentToolLoopAction{}
	if a.Name() != "AgentToolLoop" {
		t.Fatalf("bad action name %q", a.Name())
	}
	params := loopTestParams()
	config := params[StrConfig].(map[string]interface{})
	runtime := config[cfgKeyAgentRuntime].(map[string]interface{})
	runtime["identity_enforcement"] = false
	_, err := a.Execute(params)
	if err == nil || !strings.Contains(err.Error(), "identity enforcement is required") {
		t.Fatalf("expected enforcement denial, got %v", err)
	}
	// Missing identity snapshot likewise refuses.
	delete(runtime, "identity_snapshot")
	runtime["identity_enforcement"] = true
	if _, err := a.Execute(params); err == nil {
		t.Fatalf("expected missing-identity denial")
	}
}

// TestToolLoopActionMissingAllowlist pins explicit opt-in.
func TestToolLoopActionMissingAllowlist(t *testing.T) {
	a := &AgentToolLoopAction{}
	params := loopTestParams()
	delete(params, "allowlist")
	_, err := a.Execute(params)
	if err == nil || !strings.Contains(err.Error(), "missing explicit allowlist") {
		t.Fatalf("expected allowlist denial, got %v", err)
	}
}

// TestToolLoopActionStaticPolicy pins that $response markers in policy
// fields are rejected.
func TestToolLoopActionStaticPolicy(t *testing.T) {
	a := &AIInteractionAction{}
	_ = a
	action := &AgentToolLoopAction{}
	params := loopTestParams()
	params["allowlist"] = []any{"$response.tools"}
	if _, err := action.Execute(params); err == nil ||
		!strings.Contains(err.Error(), "statically declared") {
		t.Fatalf("expected static-policy denial, got %v", err)
	}
	params = loopTestParams()
	params["limits"] = map[string]any{"max_tool_calls": "$response.n"}
	if _, err := action.Execute(params); err == nil ||
		!strings.Contains(err.Error(), "statically declared") {
		t.Fatalf("expected static limits denial, got %v", err)
	}
}

// TestToolLoopActionBudgetCeilings pins limit validation.
func TestToolLoopActionBudgetCeilings(t *testing.T) {
	action := &AgentToolLoopAction{}
	for _, limits := range []any{
		map[string]any{"max_tool_calls": float64(99)},
		map[string]any{"max_model_rounds": float64(0)},
		map[string]any{"timeout": "99h"},
		map[string]any{"unknown_key": float64(1)},
	} {
		params := loopTestParams()
		params["limits"] = limits
		if _, err := action.Execute(params); err == nil {
			t.Fatalf("limits %v: expected denial", limits)
		}
	}
	// Zero budgets are rejected, not silently defaulted.
	params := loopTestParams()
	params["limits"] = map[string]any{"max_tool_calls": float64(0)}
	if _, err := action.Execute(params); err == nil {
		t.Fatalf("expected zero-budget denial")
	}
}

// TestToolLoopActionUnknownAllowlistedTool pins registry authority.
func TestToolLoopActionUnknownAllowlistedTool(t *testing.T) {
	action := &AgentToolLoopAction{}
	params := loopTestParams()
	params["allowlist"] = []any{"no_such_tool"}
	_, err := action.Execute(params)
	if err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("expected unknown-tool denial, got %v", err)
	}
}

// TestToolLoopActionRunsReadOnlyLoop runs the full action against scripted
// transport and a sqlmock database, proving end-to-end read-only execution.
func TestToolLoopActionRunsReadOnlyLoop(t *testing.T) {
	resetLLMProvidersForTest()
	t.Cleanup(func() {
		resetLLMProvidersForTest()
		RegisterLLMProvider(&OpenAICompatibleProvider{})
	})
	provider := &scriptedProvider{name: "loop-transport", responses: []any{
		chatWithCalls(stringCall("s1", "get_source_status", `{"source_id": 7}`)),
		chatWithText("source 7 looks healthy"),
	}}
	RegisterLLMProvider(provider)

	db, mock, err := newSQLMockDB(t)
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	_ = db
	mock.ExpectQuery("SELECT source_id").WillReturnRows(
		sqlmockRowsSource(7, "uid-7", "https://example.com", "Example"))

	var handler cdb.Handler = &sqlmockHandler{db: db}
	engine := NewJobEngine()
	engine.RegisterAction(&AgentToolLoopAction{})
	AgentsEngine = engine

	agentCfg := &JobConfig{
		FormatVersion: AgentFormatVersionV2,
		AgentIdentity: &AgentIdentity{AgentID: "looper", Name: "Looper",
			TrustLevel:   "trusted",
			Capabilities: []string{"tool_execution", "db_read"},
			Constraints:  &AgentConstraints{MaxSteps: 5},
		},
		Jobs: []Job{{Name: "Looper", Process: "serial", TriggerType: "manual", TriggerName: "run",
			Steps: []map[string]interface{}{
				{"action": "AgentToolLoop", "params": map[string]interface{}{
					"input":     map[string]interface{}{},
					"provider":  "loop-transport",
					"url":       "https://example.com/v1/chat",
					"model":     "scripted",
					"prompt":    "check source 7",
					"allowlist": []any{"get_source_status"},
					"config": map[string]interface{}{
						"db_handler": handler,
					},
				}},
			}}},
	}
	iCfg := map[string]any{
		"agent_runtime": cfg.AgentRuntimeConfig{IdentityEnforcement: true, ContractEnforcement: true},
		"db_handler":    handler,
	}
	if err := engine.ExecuteJobs(agentCfg, iCfg); err != nil {
		t.Fatalf("loop action failed: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet DB expectations: %v", err)
	}
	if provider.calls != 2 {
		t.Fatalf("expected 2 model rounds, got %d", provider.calls)
	}
}
