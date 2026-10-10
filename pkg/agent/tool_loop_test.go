package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// scriptedProvider replays canned chat-completion objects or errors.
// It is safe for concurrent use by parallel job groups.
type scriptedProvider struct {
	mu        sync.Mutex
	name      string
	responses []any
	calls     int
	requests  []LLMRequest
	block     func(ctx context.Context)
}

func (p *scriptedProvider) Name() string { return p.name }

func (p *scriptedProvider) Execute(req LLMRequest) (map[string]interface{}, error) {
	return p.ExecuteWithContext(context.Background(), req)
}

func (p *scriptedProvider) ExecuteWithContext(ctx context.Context, req LLMRequest) (map[string]interface{}, error) {
	p.mu.Lock()
	p.calls++
	callIndex := p.calls
	p.requests = append(p.requests, req)
	p.mu.Unlock()
	if p.block != nil {
		p.block(ctx)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	if callIndex > len(p.responses) {
		return nil, fmt.Errorf("script exhausted")
	}
	switch r := p.responses[callIndex-1].(type) {
	case error:
		return nil, r
	case map[string]any:
		return r, nil
	default:
		return nil, fmt.Errorf("bad script entry")
	}
}

// providerCalls reports total transport calls (race-safe).
func (p *scriptedProvider) providerCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func chatWithCalls(calls ...map[string]any) map[string]any {
	entries := make([]any, 0, len(calls))
	for _, call := range calls {
		entries = append(entries, call)
	}
	return map[string]any{
		"choices": []any{map[string]any{
			"message": map[string]any{
				"role": "assistant", "content": "",
				"tool_calls": entries,
			},
			"finish_reason": "tool_calls",
		}},
		"model": "scripted",
	}
}

func chatWithText(content string) map[string]any {
	return map[string]any{
		"choices": []any{map[string]any{
			"message":       map[string]any{"role": "assistant", "content": content},
			"finish_reason": "stop",
		}},
		"model": "scripted",
	}
}

func stringCall(id, name, args string) map[string]any {
	entry := map[string]any{"type": "function",
		"function": map[string]any{"name": name, "arguments": args}}
	if id != "" {
		entry["id"] = id
	}
	return entry
}

func loopTestRegistry(t *testing.T) *AgentToolRegistry {
	t.Helper()
	registry := NewAgentToolRegistry()
	if err := RegisterReadTools(registry); err != nil {
		t.Fatalf("register read tools: %v", err)
	}
	return registry
}

func loopTestConfig(provider *scriptedProvider, registry *AgentToolRegistry) ToolLoopConfig {
	return ToolLoopConfig{
		Provider:     provider,
		ProviderName: "scripted",
		Model:        "scripted",
		URL:          "https://example.com/v1/chat",
		Registry:     registry,
		Allowlist:    []string{"get_source_status", "search_indexed_pages"},
		Identity: AgentIdentity{AgentID: "op", TrustLevel: "trusted",
			Capabilities: []string{"db_read"}},
		Enforcement: true,
		Prompt:      "go",
		Limits:      DefaultToolLoopLimits(),
		RunID:       "run-1",
		TraceID:     "trace-1",
	}
}

// TestToolLoopTextOnlyTermination ends on the first text reply.
func TestToolLoopTextOnlyTermination(t *testing.T) {
	provider := &scriptedProvider{name: "scripted",
		responses: []any{chatWithText("done")}}
	result, err := RunToolLoop(loopTestConfig(provider, loopTestRegistry(t)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != ToolLoopCompleted || result.Content != "done" {
		t.Fatalf("bad terminal: %+v", result)
	}
	if result.ModelRounds != 1 || result.ToolCallCount != 0 {
		t.Fatalf("bad counts: %+v", result)
	}
	if provider.calls != 1 {
		t.Fatalf("expected one provider call, got %d", provider.calls)
	}
}

// TestToolLoopHappyPath runs one vetted call to final text.
func TestToolLoopHappyPath(t *testing.T) {
	provider := &scriptedProvider{name: "scripted", responses: []any{
		chatWithCalls(stringCall("c1", "search_indexed_pages", `{"query":"forecast"}`)),
		chatWithText("here you go"),
	}}
	// search_indexed_pages needs a DB; swap in a counting fake registry tool
	// with the same contract surface instead.
	registry := NewAgentToolRegistry()
	counter := &stubAgentTool{name: "search_indexed_pages", description: "fake search",
		schema:       map[string]any{"type": "object"},
		capabilities: []string{"db_read"}}
	if err := registry.Register(counter); err != nil {
		t.Fatalf("register: %v", err)
	}
	config := loopTestConfig(provider, registry)
	config.Allowlist = []string{"search_indexed_pages"}
	result, err := RunToolLoop(config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != ToolLoopCompleted || result.Content != "here you go" {
		t.Fatalf("bad terminal: %+v", result)
	}
	if result.ToolCallCount != 1 || result.ModelRounds != 2 {
		t.Fatalf("bad counts: %+v", result)
	}
	if counter.calls != 1 {
		t.Fatalf("handler must run exactly once, ran %d", counter.calls)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Name != "search_indexed_pages" ||
		result.ToolCalls[0].Status != "executed" {
		t.Fatalf("bad summaries: %+v", result.ToolCalls)
	}
	// Assistant echo precedes the tool message with matching IDs.
	if len(provider.requests) != 2 {
		t.Fatalf("expected 2 provider calls, got %d", len(provider.requests))
	}
	msgs := provider.requests[1].Messages
	if len(msgs) != 3 {
		t.Fatalf("expected 3 follow-up messages, got %d", len(msgs))
	}
	assistant, ok := msgs[1].(map[string]any)
	if !ok {
		t.Fatalf("bad assistant echo: %#v", msgs[1])
	}
	echoCalls, ok := assistant["tool_calls"].([]any)
	if !ok || len(echoCalls) != 1 {
		t.Fatalf("assistant echo must carry calls: %#v", assistant)
	}
	echoEntry, _ := echoCalls[0].(map[string]any)
	toolMsg, ok := msgs[2].(map[string]any)
	if !ok || toolMsg["role"] != "tool" || toolMsg["tool_call_id"] != echoEntry["id"] {
		t.Fatalf("tool message must match echo ID: %#v vs %#v", toolMsg, echoEntry)
	}
}

// TestToolLoopTwoCallsPreservesOrder runs sequential calls in order.
func TestToolLoopTwoCallsPreservesOrder(t *testing.T) {
	order := []string{}
	recorder := &orderTool{name: "search_indexed_pages", order: &order}
	registry := NewAgentToolRegistry()
	if err := registry.Register(recorder); err != nil {
		t.Fatalf("register: %v", err)
	}
	provider := &scriptedProvider{name: "scripted", responses: []any{
		chatWithCalls(
			stringCall("c1", "search_indexed_pages", `{"seq":"first"}`),
			stringCall("c2", "search_indexed_pages", `{"seq":"second"}`)),
		chatWithText("done"),
	}}
	config := loopTestConfig(provider, registry)
	config.Allowlist = []string{"search_indexed_pages"}
	result, err := RunToolLoop(config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ToolCallCount != 2 || len(order) != 2 || order[0] != "first" || order[1] != "second" {
		t.Fatalf("order lost: %+v %v", result, order)
	}
}

type orderTool struct {
	name  string
	order *[]string
}

func (t *orderTool) Name() string { return t.name }

func (t *orderTool) Description() string { return "order recorder" }

func (t *orderTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"seq": map[string]any{"type": "string"}}, "required": []any{"seq"}}
}

func (t *orderTool) RequiredCapabilities() []string { return []string{"db_read"} }

func (t *orderTool) Execute(ctx context.Context, args map[string]any) (map[string]any, error) {
	seq, _ := args["seq"].(string)
	*t.order = append(*t.order, seq)
	return map[string]any{"ok": true}, nil
}

// TestToolLoopMissingIDsSynthesized pins local-ID round-tripping.
func TestToolLoopMissingIDsSynthesized(t *testing.T) {
	provider := &scriptedProvider{name: "scripted", responses: []any{
		chatWithCalls(map[string]any{"type": "function",
			"function": map[string]any{"name": "search_indexed_pages", "arguments": map[string]any{}}}),
		chatWithText("done"),
	}}
	registry := NewAgentToolRegistry()
	counter := &stubAgentTool{name: "search_indexed_pages", description: "d",
		schema: map[string]any{"type": "object"}, capabilities: []string{"db_read"}}
	if err := registry.Register(counter); err != nil {
		t.Fatalf("register: %v", err)
	}
	config := loopTestConfig(provider, registry)
	config.Allowlist = []string{"search_indexed_pages"}
	result, err := RunToolLoop(config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != ToolLoopCompleted || len(result.ToolCalls) != 1 {
		t.Fatalf("bad terminal: %+v", result)
	}
	if result.ToolCalls[0].ID != "" {
		t.Fatalf("provider-absent ID must stay empty in summaries: %+v", result.ToolCalls[0])
	}
	msgs := provider.requests[1].Messages
	assistant, _ := msgs[1].(map[string]any)
	echoCalls, _ := assistant["tool_calls"].([]any)
	echoEntry, _ := echoCalls[0].(map[string]any)
	toolMsg, _ := msgs[2].(map[string]any)
	if echoEntry["id"] != "local-0" || toolMsg["tool_call_id"] != "local-0" {
		t.Fatalf("local ID must round-trip: %#v vs %#v", echoEntry, toolMsg)
	}
}

// TestToolLoopDuplicateIDsDenied rejects ambiguous correlation.
func TestToolLoopDuplicateIDsDenied(t *testing.T) {
	provider := &scriptedProvider{name: "scripted", responses: []any{
		chatWithCalls(
			stringCall("dup", "search_indexed_pages", `{}`),
			stringCall("dup", "search_indexed_pages", `{}`)),
	}}
	registry := NewAgentToolRegistry()
	counter := &stubAgentTool{name: "search_indexed_pages", description: "d",
		schema: map[string]any{"type": "object"}, capabilities: []string{"db_read"}}
	if err := registry.Register(counter); err != nil {
		t.Fatalf("register: %v", err)
	}
	config := loopTestConfig(provider, registry)
	config.Allowlist = []string{"search_indexed_pages"}
	result, err := RunToolLoop(config)
	if err == nil || result.Status != ToolLoopDenied {
		t.Fatalf("expected denied duplicate IDs, got %+v, %v", result, err)
	}
	if counter.calls != 1 {
		t.Fatalf("first occurrence executes once, repeats denied; ran %d", counter.calls)
	}
}

// TestToolLoopUnknownToolDenied stops after repeated denials without effect.
func TestToolLoopUnknownToolDenied(t *testing.T) {
	provider := &scriptedProvider{name: "scripted", responses: []any{
		chatWithCalls(stringCall("u1", "RunCommand", `{"command":"x"}`)),
		chatWithCalls(stringCall("u2", "RunCommand", `{"command":"x"}`)),
		chatWithCalls(stringCall("u3", "RunCommand", `{"command":"y"}`)),
		chatWithCalls(stringCall("u4", "RunCommand", `{"command":"z"}`)),
	}}
	config := loopTestConfig(provider, loopTestRegistry(t))
	result, err := RunToolLoop(config)
	if err == nil || result.Status != ToolLoopDenied {
		t.Fatalf("expected denied termination, got %+v, %v", result, err)
	}
	if result.ToolCallCount != 0 {
		t.Fatalf("no calls must execute: %+v", result)
	}
}

// TestToolLoopNoInfiniteLoop caps a model that always proposes.
func TestToolLoopNoInfiniteLoop(t *testing.T) {
	responses := []any{}
	for i := 0; i < 10; i++ {
		responses = append(responses,
			chatWithCalls(stringCall("", "search_indexed_pages", `{}`)))
	}
	provider := &scriptedProvider{name: "scripted", responses: responses}
	registry := NewAgentToolRegistry()
	counter := &stubAgentTool{name: "search_indexed_pages", description: "d",
		schema: map[string]any{"type": "object"}, capabilities: []string{"db_read"}}
	if err := registry.Register(counter); err != nil {
		t.Fatalf("register: %v", err)
	}
	config := loopTestConfig(provider, registry)
	config.Allowlist = []string{"search_indexed_pages"}
	config.Limits = ToolLoopLimits{MaxModelRounds: 2, MaxToolCalls: 8,
		MaxResultBytes: hardMaxResultBytes, MaxTotalResultBytes: hardMaxTotalResultBytes,
		Timeout: hardLoopTimeout}
	result, err := RunToolLoop(config)
	if err == nil || result.Status != ToolLoopBudgetExhausted {
		t.Fatalf("expected budget exhaustion, got %+v, %v", result, err)
	}
	if provider.calls > 2 {
		t.Fatalf("model rounds exceeded cap: %d", provider.calls)
	}
}

// TestToolLoopCancelledContext stops before transport on expired parents.
func TestToolLoopCancelledContext(t *testing.T) {
	provider := &scriptedProvider{name: "scripted",
		responses: []any{chatWithText("never")}}
	config := loopTestConfig(provider, loopTestRegistry(t))
	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Minute))
	defer cancel()
	config.Ctx = expired
	result, err := RunToolLoop(config)
	if err == nil || result.Status != ToolLoopCancelled {
		t.Fatalf("expected cancellation, got %+v, %v", result, err)
	}
	if provider.calls != 0 {
		t.Fatalf("cancelled run must not call transport")
	}
}

// TestToolLoopSlowProviderCancels proves deadline-bounded model calls.
func TestToolLoopSlowProviderCancels(t *testing.T) {
	provider := &scriptedProvider{name: "scripted",
		responses: []any{chatWithText("never")}}
	provider.block = func(ctx context.Context) {
		<-ctx.Done()
	}
	config := loopTestConfig(provider, loopTestRegistry(t))
	config.Limits = DefaultToolLoopLimits()
	config.Limits.Timeout = 50 * time.Millisecond
	result, err := RunToolLoop(config)
	if err == nil || result.Status != ToolLoopCancelled {
		t.Fatalf("expected cancellation, got %+v, %v", result, err)
	}
}

// TestToolLoopOversizedResult bounds observations without executing twice.
func TestToolLoopOversizedResult(t *testing.T) {
	provider := &scriptedProvider{name: "scripted", responses: []any{
		chatWithCalls(stringCall("big", "search_indexed_pages", `{}`)),
		chatWithText("done"),
	}}
	registry := NewAgentToolRegistry()
	huge := &hugeTool{name: "search_indexed_pages"}
	if err := registry.Register(huge); err != nil {
		t.Fatalf("register: %v", err)
	}
	config := loopTestConfig(provider, registry)
	config.Allowlist = []string{"search_indexed_pages"}
	config.Limits = DefaultToolLoopLimits()
	config.Limits.MaxResultBytes = 64
	result, err := RunToolLoop(config)
	if err != nil {
		t.Fatalf("oversized results truncate, got %v", err)
	}
	if result.Status != ToolLoopCompleted {
		t.Fatalf("expected completion with truncation, got %+v", result)
	}
	if huge.calls != 1 {
		t.Fatalf("handler must run exactly once, ran %d", huge.calls)
	}
}

type hugeTool struct {
	name  string
	calls int
}

func (t *hugeTool) Name() string { return t.name }

func (t *hugeTool) Description() string { return "huge output" }

func (t *hugeTool) InputSchema() map[string]any { return map[string]any{"type": "object"} }

func (t *hugeTool) RequiredCapabilities() []string { return []string{"db_read"} }

func (t *hugeTool) Execute(ctx context.Context, args map[string]any) (map[string]any, error) {
	t.calls++
	blob := make([]byte, 1024)
	for i := range blob {
		blob[i] = 'x'
	}
	return map[string]any{"blob": string(blob)}, nil
}

// TestToolLoopAuditTrail pins lifecycle auditing without secrets.
func TestToolLoopAuditTrail(t *testing.T) {
	provider := &scriptedProvider{name: "scripted", responses: []any{
		chatWithCalls(stringCall("a1", "search_indexed_pages", `{"query":"s3cret-plans"}`)),
		chatWithText("done"),
	}}
	registry := NewAgentToolRegistry()
	counter := &stubAgentTool{name: "search_indexed_pages", description: "d",
		schema: map[string]any{"type": "object"}, capabilities: []string{"db_read"}}
	if err := registry.Register(counter); err != nil {
		t.Fatalf("register: %v", err)
	}
	engine := NewJobEngine()
	AgentsEngine = engine
	config := loopTestConfig(provider, registry)
	config.Allowlist = []string{"search_indexed_pages"}
	if _, err := RunToolLoop(config); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	events := engine.AuditEvents()
	if len(events) == 0 {
		t.Fatalf("expected audit events")
	}
	sawStarted, sawCall := false, false
	for _, event := range events {
		if event.RunID != "run-1" || event.TraceID != "trace-1" {
			t.Fatalf("trace IDs lost: %+v", event)
		}
		if event.Reason == "tool_loop_started" {
			sawStarted = true
		}
		if event.DelegationTarget == "search_indexed_pages" {
			sawCall = true
		}
		if strings.Contains(event.Reason, "s3cret") {
			t.Fatalf("argument value leaked into audit: %+v", event)
		}
	}
	if !sawStarted || !sawCall {
		t.Fatalf("lifecycle incomplete: started=%v call=%v", sawStarted, sawCall)
	}
}

// TestToolLoopDeniedAuditReasonCode pins denial audit content.
func TestToolLoopDeniedAuditReasonCode(t *testing.T) {
	provider := &scriptedProvider{name: "scripted", responses: []any{
		chatWithCalls(stringCall("u1", "RunCommand", `{"command":"x"}`)),
	}}
	engine := NewJobEngine()
	AgentsEngine = engine
	config := loopTestConfig(provider, loopTestRegistry(t))
	if _, err := RunToolLoop(config); err == nil {
		t.Fatalf("expected denial termination")
	}
	found := false
	for _, event := range engine.AuditEvents() {
		if event.Outcome == auditOutcomeDenied && event.DelegationTarget == "RunCommand" {
			found = true
			if !strings.Contains(event.Reason, ToolDenyUnknownTool) &&
				!strings.Contains(event.Reason, ToolDenyNotAllowlisted) {
				t.Fatalf("denial needs reason code, got %+v", event)
			}
		}
	}
	if !found {
		t.Fatalf("expected denial audit for RunCommand")
	}
}

// TestToolLoopRejectsForgedIdentityFields proves extra argument keys —
// including identity-shaped ones — are rejected by additionalProperties.
func TestToolLoopRejectsForgedIdentityFields(t *testing.T) {
	registry := NewAgentToolRegistry()
	if err := RegisterReadTools(registry); err != nil {
		t.Fatalf("register: %v", err)
	}
	tool, _ := registry.Get("get_source_status")
	forged := map[string]any{"source_id": float64(7), "role": "admin"}
	args, err := normalizeToolArguments(forged)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	err = AuthorizeToolCall(ToolAuthContext{
		Identity:    AgentIdentity{AgentID: "op", TrustLevel: "trusted", Capabilities: []string{"db_read"}},
		Enforcement: true,
		Allowlist:   map[string]bool{"get_source_status": true},
		RunID:       "r", TraceID: "t",
	}, tool, args)
	if err == nil {
		t.Fatalf("forged identity fields must be denied")
	}
}

// TestSanitizeToolResultRedactsSecrets pins key-based redaction.
func TestSanitizeToolResultRedactsSecrets(t *testing.T) {
	result := sanitizeToolResult(map[string]any{
		"name":      "Example",
		"api_key":   "AKIA123",
		"nested":    map[string]any{"db_password": "hunter2", "port": float64(5432)},
		"authToken": "tok",
	})
	if result["api_key"] != "[redacted]" || result["authToken"] != "[redacted]" {
		t.Fatalf("top-level secrets kept: %v", result)
	}
	nested, ok := result["nested"].(map[string]any)
	if !ok || nested["db_password"] != "[redacted]" || nested["port"] != float64(5432) {
		t.Fatalf("nested redaction wrong: %v", result)
	}
	if result["name"] != "Example" {
		t.Fatalf("safe fields altered: %v", result)
	}
}

// TestParallelGroupsRunIsolatedLoops runs one tool loop per parallel group:
// budgets, registries, and results must not leak across groups. Always-
// proposing models hit per-group round caps deterministically.
func TestParallelGroupsRunIsolatedLoops(t *testing.T) {
	resetLLMProvidersForTest()
	t.Cleanup(func() {
		resetLLMProvidersForTest()
		RegisterLLMProvider(&OpenAICompatibleProvider{})
	})
	provider := &scriptedProvider{name: "par-transport"}
	for i := 0; i < 8; i++ {
		provider.responses = append(provider.responses,
			chatWithCalls(stringCall("", "get_source_status", `{"source_id": 7}`)))
	}
	RegisterLLMProvider(provider)

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	sourceRows := func() *sqlmock.Rows {
		return sqlmock.NewRows([]string{"source_id", "source_uid", "url", "name", "priority",
			"sub_priority", "category_id", "usr_id", "restricted", "flags", "config"}).
			AddRow(int64(7), "uid-7", "https://example.com", "Example", "high",
				int64(0), int64(1), int64(99), int64(0), int64(0), []byte(`{}`))
	}
	mock.ExpectQuery("SELECT source_id").WillReturnRows(sourceRows())
	mock.ExpectQuery("SELECT source_id").WillReturnRows(sourceRows())
	var handler cdb.Handler = &sqlmockHandler{db: db}

	engine := NewJobEngine()
	engine.RegisterAction(&AgentToolLoopAction{})
	AgentsEngine = engine

	loopParams := func() map[string]any {
		return map[string]any{
			"input":     map[string]any{},
			"provider":  "par-transport",
			"url":       "https://example.com/v1/chat",
			"model":     "scripted",
			"prompt":    "check source 7",
			"allowlist": []any{"get_source_status"},
			"limits":    map[string]any{"max_model_rounds": float64(2)},
		}
	}
	agentCfg := &JobConfig{
		FormatVersion: AgentFormatVersionV2,
		AgentIdentity: &AgentIdentity{AgentID: "par", Name: "ParLoop",
			TrustLevel:   "trusted",
			Capabilities: []string{"tool_execution", "db_read"},
			Constraints:  &AgentConstraints{MaxSteps: 5},
		},
		Jobs: []Job{
			{Name: "ParLoop", Process: "parallel", TriggerType: "manual", TriggerName: "run",
				Steps: []map[string]any{
					{"action": "AgentToolLoop", "params": loopParams()},
				}},
			{Name: "ParLoopTwo", Process: "parallel", TriggerType: "manual", TriggerName: "run",
				Steps: []map[string]any{
					{"action": "AgentToolLoop", "params": loopParams()},
				}},
		},
	}
	// Identity name must match the first job for normalization.
	agentCfg.AgentIdentity.Name = "ParLoop"
	iCfg := map[string]any{
		"agent_runtime": cfg.AgentRuntimeConfig{IdentityEnforcement: true, ContractEnforcement: true},
		"db_handler":    handler,
	}
	err = engine.ExecuteJobs(agentCfg, iCfg)
	if err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("expected per-group budget exhaustion, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("both groups must execute independently: %v", err)
	}
	if got := provider.providerCalls(); got != 4 {
		t.Fatalf("expected 4 model rounds (2 per group), got %d", got)
	}
}
