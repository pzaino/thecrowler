package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	yaml "gopkg.in/yaml.v3"
)

type contextTestKey struct{}

type contextCaptureAction struct {
	name   string
	mu     sync.Mutex
	calls  int
	tokens []any
	block  func(context.Context)
}

func (a *contextCaptureAction) Name() string { return a.name }

func (a *contextCaptureAction) Execute(params map[string]interface{}) (map[string]interface{}, error) {
	return a.ExecuteContext(context.Background(), params)
}

func (a *contextCaptureAction) ExecuteContext(ctx context.Context, _ map[string]interface{}) (map[string]interface{}, error) {
	a.mu.Lock()
	a.calls++
	a.tokens = append(a.tokens, ctx.Value(contextTestKey{}))
	a.mu.Unlock()
	if a.block != nil {
		a.block(ctx)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return map[string]interface{}{StrResponse: map[string]any{"ok": true}, StrStatus: StatusSuccess}, nil
}

func (a *contextCaptureAction) snapshot() (int, []any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls, append([]any(nil), a.tokens...)
}

func contextTestManifest(t *testing.T, agentID, action string, params string) *JobConfig {
	t.Helper()
	manifest := `format_version: v2
agent_identity:
  agent_id: ` + agentID + `
  name: Context Manual
  trust_level: trusted
  capabilities: [api_request, tool_execution, db_read, ai_reasoning]
jobs:
  - name: Context Manual
    process: serial
    trigger_type: manual
    trigger_name: run
    steps:
      - action: ` + action + `
        params: ` + params + `
`
	var jobConfig JobConfig
	if err := yaml.Unmarshal([]byte(manifest), &jobConfig); err != nil {
		t.Fatalf("parse context manifest: %v", err)
	}
	return &jobConfig
}

func TestManualAgentContextCancelledBeforeExecution(t *testing.T) {
	previousRegistry := AgentsRegistry
	AgentsRegistry = NewJobConfig()
	t.Cleanup(func() { AgentsRegistry = previousRegistry })
	jobConfig := contextTestManifest(t, "context-cancel-agent", "APIRequest",
		`{url: "http://localhost/health", request_type: GET}`)
	if err := AgentsRegistry.RegisterAgentE(jobConfig); err != nil {
		t.Fatalf("register agent: %v", err)
	}
	engine := NewJobEngine()
	action := &contextCaptureAction{name: "APIRequest"}
	engine.RegisterAction(action)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := engine.ExecuteManualAgentResultContext(ctx, "context-cancel-agent", map[string]any{
		"agent_runtime": cfg.AgentRuntimeConfig{IdentityEnforcement: true, ContractEnforcement: true},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled manual run error = %v", err)
	}
	if calls, _ := action.snapshot(); calls != 0 {
		t.Fatalf("action ran after cancellation: %d", calls)
	}
}

func TestManualAgentContextCancellationReachesAction(t *testing.T) {
	previousRegistry := AgentsRegistry
	AgentsRegistry = NewJobConfig()
	t.Cleanup(func() { AgentsRegistry = previousRegistry })
	jobConfig := contextTestManifest(t, "context-during-agent", "APIRequest",
		`{url: "http://localhost/health", request_type: GET}`)
	if err := AgentsRegistry.RegisterAgentE(jobConfig); err != nil {
		t.Fatalf("register agent: %v", err)
	}
	engine := NewJobEngine()
	started := make(chan struct{})
	action := &contextCaptureAction{name: "APIRequest", block: func(ctx context.Context) {
		close(started)
		<-ctx.Done()
	}}
	engine.RegisterAction(action)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := engine.ExecuteManualAgentResultContext(ctx, "context-during-agent", map[string]any{
			"agent_runtime": cfg.AgentRuntimeConfig{IdentityEnforcement: true, ContractEnforcement: true},
		})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatalf("action did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "cancel") {
			t.Fatalf("cancelled action error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("cancelled run did not terminate")
	}
}

func TestManualAgentContextCancellationReachesProvider(t *testing.T) {
	previousRegistry := AgentsRegistry
	AgentsRegistry = NewJobConfig()
	t.Cleanup(func() { AgentsRegistry = previousRegistry })
	jobConfig := contextTestManifest(t, "context-provider-agent", "AgentToolLoop",
		`{provider: scripted, url: "https://example.com/v1/chat", model: scripted, prompt: go, allowlist: [get_source_status], input: {}}`)
	if err := AgentsRegistry.RegisterAgentE(jobConfig); err != nil {
		t.Fatalf("register agent: %v", err)
	}
	resetLLMProvidersForTest()
	t.Cleanup(resetLLMProvidersForTest)
	started := make(chan struct{})
	provider := &scriptedProvider{name: "scripted", block: func(ctx context.Context) {
		close(started)
		<-ctx.Done()
	}}
	RegisterLLMProvider(provider)
	engine := NewJobEngine()
	engine.RegisterAction(&AgentToolLoopAction{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := engine.ExecuteManualAgentResultContext(ctx, "context-provider-agent", map[string]any{
			"agent_runtime": cfg.AgentRuntimeConfig{IdentityEnforcement: true, ContractEnforcement: true},
			"db_handler":    &fakeDBHandler{},
		})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatalf("provider did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "cancel") {
			t.Fatalf("cancelled provider run error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("cancelled provider run did not terminate")
	}
	if provider.providerCalls() != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.providerCalls())
	}
}

func TestManualAgentConcurrentContextsDoNotLeak(t *testing.T) {
	previousRegistry := AgentsRegistry
	AgentsRegistry = NewJobConfig()
	t.Cleanup(func() { AgentsRegistry = previousRegistry })
	jobConfig := contextTestManifest(t, "context-concurrent-agent", "APIRequest",
		`{url: "http://localhost/health", request_type: GET}`)
	if err := AgentsRegistry.RegisterAgentE(jobConfig); err != nil {
		t.Fatalf("register agent: %v", err)
	}
	engine := NewJobEngine()
	action := &contextCaptureAction{name: "APIRequest"}
	engine.RegisterAction(action)
	first := context.WithValue(context.Background(), contextTestKey{}, "first")
	second := context.WithValue(context.Background(), contextTestKey{}, "second")
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, ctx := range []context.Context{first, second} {
		wg.Add(1)
		go func(ctx context.Context) {
			defer wg.Done()
			_, err := engine.ExecuteManualAgentResultContext(ctx, "context-concurrent-agent", map[string]any{
				"agent_runtime": cfg.AgentRuntimeConfig{IdentityEnforcement: true, ContractEnforcement: true},
			})
			errs <- err
		}(ctx)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent manual run: %v", err)
		}
	}
	calls, tokens := action.snapshot()
	if calls != 2 {
		t.Fatalf("action calls = %d, want 2", calls)
	}
	seen := map[any]bool{}
	for _, token := range tokens {
		seen[token] = true
	}
	if !seen["first"] || !seen["second"] {
		t.Fatalf("context values leaked or were lost: %#v", tokens)
	}
}

func TestMCPProposalCancelledBeforeAdmissionDoesNotInsert(t *testing.T) {
	manager, policy, aliases, identity, cleanup := newMCPAuthorityManager(t)
	defer cleanup()
	store := NewInMemoryMCPMutationProposalStore()
	tool, err := NewMCPMutationProposalToolForOperation(manager, policy, store, aliases[0])
	if err != nil {
		t.Fatalf("proposal tool: %v", err)
	}
	base := mcpMutationRuntime(identity, aliases[0], "cancelled-admission-run", "cancelled-admission-call")
	ctx, cancel := context.WithCancel(base)
	cancel()
	if _, err := tool.Execute(ctx, map[string]any{"resource": "fixture-resource"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled admission error = %v", err)
	}
	if len(store.byID) != 0 {
		t.Fatalf("cancelled admission inserted %d proposals", len(store.byID))
	}
}

func TestMCPManagerInitializationHonoursCancellation(t *testing.T) {
	server := mcpFixtureServerConfig(t, "normal")
	config := mcpTestConfigFor(server)
	alias := MCPToolAlias("fixture", "echo")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	manager, err := NewMCPToolManager(ctx, config, mcpTestIdentity(), []string{alias}, nil)
	if err == nil {
		if manager != nil {
			_ = manager.Close()
		}
		t.Fatalf("cancelled MCP initialization succeeded")
	}
}

func TestRegistrationHonoursCancelledContext(t *testing.T) {
	handler := newMCPMutationStoreTestHandler(t)
	manager, policy, aliases, identity, cleanup := newMCPAuthorityManager(t)
	defer cleanup()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := registerMCPMutationProposalTools(ctx, manager, NewAgentToolRegistry(), policy, identity,
		MCPMutationPrincipal{UserID: 7, Username: identity.Owner, Issuer: "test-issuer"},
		aliases, []uint64{42}, handler, "cancelled-register-run", "cancelled-register-trace"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled registration error = %v", err)
	}
	var count int
	if err := handler.(*mcpMutationSQLiteHandler).db.QueryRow(`SELECT COUNT(*) FROM MCPMutationProposals`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cancelled registration proposals = %d err=%v", count, err)
	}
}

func TestDatabaseAdmissionCancelledBeforeCommitDoesNotInsert(t *testing.T) {
	ctx := context.Background()
	handler := newMCPMutationStoreTestHandler(t)
	manager, policy, aliases, _, cleanup := newMCPAuthorityManager(t)
	defer cleanup()
	proposal := sourceAdmissionProposal(t, manager, policy, aliases[1], "cancel-db-run", "cancel-db-call")
	store, err := NewDatabaseMCPMutationProposalStore(&handler)
	if err != nil {
		t.Fatalf("new durable store: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.Admit(cancelled, proposal, 4, 8); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled durable admission error = %v", err)
	}
	var count int
	if err := handler.(*mcpMutationSQLiteHandler).db.QueryRow(`SELECT COUNT(*) FROM MCPMutationProposals`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cancelled durable admission proposals = %d err=%v", count, err)
	}
}

func TestAIInteractionCancellationReachesProvider(t *testing.T) {
	previousRegistry := AgentsRegistry
	AgentsRegistry = NewJobConfig()
	t.Cleanup(func() { AgentsRegistry = previousRegistry })
	jobConfig := contextTestManifest(t, "context-ai-agent", "AIInteraction",
		`{provider: scripted, url: "https://example.com/v1/chat", model: scripted, prompt: go, input: {}}`)
	if err := AgentsRegistry.RegisterAgentE(jobConfig); err != nil {
		t.Fatalf("register agent: %v", err)
	}
	resetLLMProvidersForTest()
	t.Cleanup(resetLLMProvidersForTest)
	started := make(chan struct{})
	provider := &scriptedProvider{name: "scripted", block: func(ctx context.Context) {
		close(started)
		<-ctx.Done()
	}}
	RegisterLLMProvider(provider)
	engine := NewJobEngine()
	engine.RegisterAction(&AIInteractionAction{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := engine.ExecuteManualAgentResultContext(ctx, "context-ai-agent", map[string]any{
			"agent_runtime": cfg.AgentRuntimeConfig{IdentityEnforcement: true, ContractEnforcement: true},
		})
		done <- err
	}()
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("AI interaction ended before provider start: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatalf("provider did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "cancel") {
			t.Fatalf("cancelled AI interaction error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("cancelled AI interaction did not terminate")
	}
	if provider.providerCalls() != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.providerCalls())
	}
}

func TestExecuteAgentResultContextReachesDelegatedAgent(t *testing.T) {
	previousRegistry := AgentsRegistry
	AgentsRegistry = NewJobConfig()
	t.Cleanup(func() { AgentsRegistry = previousRegistry })
	jobConfig := contextTestManifest(t, "delegated-context-agent", "APIRequest",
		`{url: "http://localhost/health", request_type: GET}`)
	if err := AgentsRegistry.RegisterAgentE(jobConfig); err != nil {
		t.Fatalf("register delegated agent: %v", err)
	}
	engine := NewJobEngine()
	action := &contextCaptureAction{name: "APIRequest"}
	engine.RegisterAction(action)
	ctx := context.WithValue(context.Background(), contextTestKey{}, "delegated")
	if _, err := engine.ExecuteAgentResultContext(ctx, "delegated-context-agent", map[string]any{
		"agent_runtime": cfg.AgentRuntimeConfig{IdentityEnforcement: true, ContractEnforcement: true},
	}); err != nil {
		t.Fatalf("delegated context execution: %v", err)
	}
	calls, tokens := action.snapshot()
	if calls != 1 || len(tokens) != 1 || tokens[0] != "delegated" {
		t.Fatalf("delegated context not propagated: calls=%d tokens=%#v", calls, tokens)
	}
}
