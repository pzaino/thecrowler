package agent

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

type authorityNativeMutationTool struct{ name string }

func (t *authorityNativeMutationTool) Name() string { return t.name }
func (t *authorityNativeMutationTool) Description() string {
	return "native mutation authority fixture"
}
func (t *authorityNativeMutationTool) InputSchema() map[string]any {
	return map[string]any{"type": "object"}
}
func (t *authorityNativeMutationTool) RequiredCapabilities() []string { return nil }
func (t *authorityNativeMutationTool) Execute(context.Context, map[string]any) (map[string]any, error) {
	return map[string]any{"ok": true}, nil
}
func (t *authorityNativeMutationTool) Effect() ToolEffect { return ToolEffectMutation }

type authorityMCPProposalTool struct{ name string }

func (t *authorityMCPProposalTool) Name() string        { return t.name }
func (t *authorityMCPProposalTool) Description() string { return "MCP proposal authority fixture" }
func (t *authorityMCPProposalTool) InputSchema() map[string]any {
	return map[string]any{"type": "object"}
}
func (t *authorityMCPProposalTool) RequiredCapabilities() []string { return nil }
func (t *authorityMCPProposalTool) Execute(context.Context, map[string]any) (map[string]any, error) {
	return map[string]any{"ok": true}, nil
}
func (t *authorityMCPProposalTool) Effect() ToolEffect          { return ToolEffectMutation }
func (t *authorityMCPProposalTool) IsMCPMutationProposal() bool { return true }

func authorityTestAuth(toolName string) ToolAuthContext {
	return ToolAuthContext{
		Identity:    AgentIdentity{AgentID: "authority-agent", TrustLevel: "trusted"},
		Enforcement: true, Allowlist: map[string]bool{toolName: true},
		RunID: "authority-run", TraceID: "authority-trace",
	}
}

func TestMCPAuthorityIsIndependentFromNativeCrawl(t *testing.T) {
	mcpTool := &authorityMCPProposalTool{name: "mcp_alias"}
	nativeTool := &authorityNativeMutationTool{name: "native_operation"}

	auth := authorityTestAuth("mcp_alias")
	auth.MCPMutationAllowed = true
	auth.MCPAllowedOperations = map[string]bool{"mcp_alias": true}
	if err := AuthorizeToolCall(auth, mcpTool, map[string]any{}); err != nil {
		t.Fatalf("MCP proposal with independent authority denied: %v", err)
	}
	if err := AuthorizeToolCall(auth, nativeTool, map[string]any{}); err == nil {
		t.Fatalf("native operation was authorized by MCP authority")
	}

	auth = authorityTestAuth("native_operation")
	auth.MutationAllowed = true
	auth.AllowedOperations = map[string]bool{"native_operation": true, "all": true}
	if err := AuthorizeToolCall(auth, nativeTool, map[string]any{}); err != nil {
		t.Fatalf("native operation with native authority denied: %v", err)
	}
	if err := AuthorizeToolCall(auth, mcpTool, map[string]any{}); err == nil {
		t.Fatalf("MCP proposal was authorized by native crawl authority")
	}

	auth = authorityTestAuth("mcp_alias")
	auth.MCPMutationAllowed = true
	auth.MCPAllowedOperations = map[string]bool{"other_alias": true}
	if err := AuthorizeToolCall(auth, mcpTool, map[string]any{}); err == nil {
		t.Fatalf("MCP proposal was authorized by an unrelated alias entry")
	}
}

func mcpAuthorityConfig(t *testing.T, counterPath string) (cfg.MCPConfig, []string, MCPMutationPolicy, func()) {
	t.Helper()
	server := mcpMutationServerConfig(t, counterPath, cfg.MCPApprovedTool{
		Name: "mutate_counter", Effect: "mutation", AllowMutationProposal: true,
		Description: "Fixture resource mutation.", RequiredCapabilities: []string{"tool_execution"},
	})
	server.AllowedAgents = []string{"mcp-test-agent"}
	server.ApprovedTools = append(server.ApprovedTools, cfg.MCPApprovedTool{
		Name: "mutate_source_counter", Effect: "mutation", AllowMutationProposal: true,
		Description: "Fixture source mutation.", RequiredCapabilities: []string{"tool_execution"}, AllowedSources: []uint64{42},
	})
	config := cfg.MCPConfig{
		Servers: []cfg.MCPServerConfig{server},
		MutationPolicy: cfg.MCPMutationPolicyConfig{
			Enabled: true, PolicyID: "authority-policy", Version: "1",
			Operations: []cfg.MCPMutationOperationConfig{
				{
					ServerID: "fixture", ToolName: "mutate_counter",
					AllowedAgents: []string{"mcp-test-agent"}, RequiredCapabilities: []string{"tool_execution"},
					MinTrustLevel: "trusted", AllowedResources: []string{"fixture-resource"},
					RequiredArgumentFields: []string{"resource"}, AllowedArgumentFields: []string{"resource"},
					ProposalTTL: "5m", MaxProposalsPerRun: 4, MaxPendingProposals: 8,
					MaxInputBytes: 4096, MaxOutputBytes: 4096, RequireApproval: true,
					ExecutionBackend: "none", AllowedTransports: []string{"stdio"}, AuditRequired: true,
				},
				{
					ServerID: "fixture", ToolName: "mutate_source_counter",
					AllowedAgents: []string{"mcp-test-agent"}, RequiredCapabilities: []string{"tool_execution"},
					MinTrustLevel: "trusted", AllowedSources: []uint64{42},
					RequiredArgumentFields: []string{"source_id"}, AllowedArgumentFields: []string{"source_id"},
					ProposalTTL: "5m", MaxProposalsPerRun: 4, MaxPendingProposals: 8,
					MaxInputBytes: 4096, MaxOutputBytes: 4096, RequireApproval: true,
					ExecutionBackend: "none", AllowedTransports: []string{"stdio"}, AuditRequired: true,
				},
			},
		},
	}
	aliases := []string{
		MCPToolAlias("fixture", "mutate_counter"),
		MCPToolAlias("fixture", "mutate_source_counter"),
	}
	policy, err := MCPMutationPolicyFromConfig(config)
	if err != nil {
		t.Fatalf("build policy: %v", err)
	}
	return config, aliases, policy, func() {}
}

func TestRegisterMCPMutationProposalToolsExactAndMultiOperation(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "authority-counter.log")
	config, aliases, policy, _ := mcpAuthorityConfig(t, counterPath)
	setMCPTestConfig(t, config)
	identity := mcpTestIdentity()
	manager, err := NewMCPToolManager(context.Background(), config, identity, aliases, []uint64{42})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	registry := NewAgentToolRegistry()
	handler := newMCPMutationStoreTestHandler(t)
	allowed, err := registerMCPMutationProposalTools(context.Background(), manager, registry, policy, identity,
		MCPMutationPrincipal{UserID: 7, Username: identity.Owner, Issuer: "test-issuer"}, aliases, []uint64{42}, handler,
		"authority-run", "authority-trace")
	if err != nil {
		t.Fatalf("register proposal tools: %v", err)
	}
	if len(allowed) != 2 || !allowed[aliases[0]] || !allowed[aliases[1]] {
		t.Fatalf("registered aliases = %+v", allowed)
	}
	for _, alias := range aliases {
		if _, ok := registry.Get(alias); !ok {
			t.Fatalf("missing proposal tool %q", alias)
		}
	}
	if _, err := NewMCPMutationProposalTool(manager, policy, NewInMemoryMCPMutationProposalStore()); err == nil {
		t.Fatalf("ambiguous first-match constructor accepted multiple operations")
	}
	tool, err := NewMCPMutationProposalToolForOperation(manager, policy, NewInMemoryMCPMutationProposalStore(), aliases[0])
	if err != nil {
		t.Fatalf("exact proposal tool: %v", err)
	}
	ctx := ContextWithToolRuntime(context.Background(), ToolRuntime{
		Auth: ToolAuthContext{
			Identity: identity, Enforcement: true,
			Allowlist:          map[string]bool{aliases[0]: true},
			MCPMutationAllowed: true, MCPAllowedOperations: map[string]bool{aliases[0]: true},
			AllowedSources: map[uint64]bool{42: true}, RunID: "authority-run", TraceID: "authority-trace",
		},
		CallID: "authority-call", Principal: MCPMutationPrincipal{UserID: 7, Username: identity.Owner, Issuer: "test-issuer"},
	})
	if _, err := tool.Execute(ctx, map[string]any{"resource": "fixture-resource"}); err != nil {
		t.Fatalf("exact proposal execution: %v", err)
	}
	if data, err := os.ReadFile(counterPath); err == nil && len(data) > 0 {
		t.Fatalf("mutating MCP fixture executed: %s", data)
	}
}

func TestRegisterMCPMutationProposalToolsFailsClosed(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "authority-counter.log")
	config, aliases, policy, _ := mcpAuthorityConfig(t, counterPath)
	setMCPTestConfig(t, config)
	identity := mcpTestIdentity()
	manager, err := NewMCPToolManager(context.Background(), config, identity, aliases, []uint64{42})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	registry := NewAgentToolRegistry()

	if _, err := registerMCPMutationProposalTools(context.Background(), manager, registry, policy, identity,
		MCPMutationPrincipal{}, aliases, []uint64{42}, newMCPMutationStoreTestHandler(t),
		"authority-run", "authority-trace"); !errors.Is(err, ErrMCPMutationPrincipal) {
		t.Fatalf("missing principal error = %v", err)
	}
	if _, err := registerMCPMutationProposalTools(context.Background(), manager, registry, policy, identity,
		MCPMutationPrincipal{UserID: 7, Username: identity.Owner, Issuer: "test-issuer"}, aliases, []uint64{43}, newMCPMutationStoreTestHandler(t),
		"authority-run", "authority-trace"); err == nil {
		t.Fatalf("out-of-run source scope was accepted")
	}
	noOwner := identity
	noOwner.Owner = ""
	if _, err := registerMCPMutationProposalTools(context.Background(), manager, registry, policy, noOwner,
		MCPMutationPrincipal{UserID: 7, Username: identity.Owner, Issuer: "test-issuer"}, aliases, []uint64{42}, newMCPMutationStoreTestHandler(t),
		"authority-run", "authority-trace"); err == nil {
		t.Fatalf("missing owner audit label was accepted")
	}
	if _, err := registerMCPMutationProposalTools(context.Background(), manager, registry, policy, identity,
		MCPMutationPrincipal{UserID: 7, Username: identity.Owner, Issuer: "test-issuer"}, []string{"all"}, []uint64{42}, newMCPMutationStoreTestHandler(t),
		"authority-run", "authority-trace"); err != nil {
		t.Fatalf("unrelated allowlist error = %v", err)
	}
	noSchemaDB, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open no-schema SQLite: %v", err)
	}
	defer noSchemaDB.Close()
	noSchema := cdb.Handler(&mcpMutationSQLiteHandler{db: noSchemaDB})
	if _, err := registerMCPMutationProposalTools(context.Background(), manager, NewAgentToolRegistry(), policy, identity,
		MCPMutationPrincipal{UserID: 7, Username: identity.Owner, Issuer: "test-issuer"}, aliases, []uint64{42}, noSchema,
		"authority-run", "authority-trace"); !errors.Is(err, ErrMCPMutationStoreUnavailable) {
		t.Fatalf("missing durable capability error = %v", err)
	}
}

func TestMCPProposalToolRequiresMCPAuthorityInToolLoop(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "authority-counter.log")
	config, aliases, _, _ := mcpAuthorityConfig(t, counterPath)
	setMCPTestConfig(t, config)
	params := loopTestParams()
	configMap := params[StrConfig].(map[string]interface{})
	configMap["db_handler"] = newMCPMutationStoreTestHandler(t)
	params["allowlist"] = []any{aliases[0]}
	runtime := configMap[cfgKeyAgentRuntime].(map[string]interface{})
	runtime[cfgKeyMCPMutationPrincipal] = MCPMutationPrincipal{UserID: 7, Username: "mcp-owner", Issuer: "test-issuer"}
	loop, err := normalizeToolLoopConfig(params, configMap, NewInputContext(params), mcpTestIdentity())
	if err != nil {
		t.Fatalf("normalize tool loop: %v", err)
	}
	defer loop.cancel()
	if !loop.MCPMutationAllowed || !loop.MCPAllowedOperations[aliases[0]] {
		t.Fatalf("MCP authority not registered: allowed=%v ops=%+v", loop.MCPMutationAllowed, loop.MCPAllowedOperations)
	}
	if loop.MutationAllowed || loop.AllowedOperations != nil {
		t.Fatalf("native crawl authority was activated: %v %+v", loop.MutationAllowed, loop.AllowedOperations)
	}
	if _, ok := loop.Registry.Get(aliases[0]); !ok {
		t.Fatalf("proposal tool was not registered")
	}
}

func TestMCPProposalToolRequiresPrincipalInToolLoop(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "authority-counter.log")
	config, aliases, _, _ := mcpAuthorityConfig(t, counterPath)
	setMCPTestConfig(t, config)
	params := loopTestParams()
	configMap := params[StrConfig].(map[string]interface{})
	configMap["db_handler"] = newMCPMutationStoreTestHandler(t)
	params["allowlist"] = []any{aliases[0]}
	if _, err := normalizeToolLoopConfig(params, configMap, NewInputContext(params), mcpTestIdentity()); err == nil {
		t.Fatalf("MCP proposal tool registration without principal succeeded")
	}
}

func TestGoalRunRegistersExactProposalToolAndStaysInert(t *testing.T) {
	resetLLMProvidersForTest()
	provider := &scriptedProvider{name: "scripted", responses: []any{
		chatWithCalls(stringCall("goal-proposal-call", "mcp_fixture_mutate_counter", `{"resource":"fixture-resource"}`)),
		chatWithText("proposal recorded"),
	}}
	RegisterLLMProvider(provider)
	t.Cleanup(resetLLMProvidersForTest)

	counterPath := filepath.Join(t.TempDir(), "goal-authority-counter.log")
	config, aliases, _, _ := mcpAuthorityConfig(t, counterPath)
	setMCPTestConfig(t, config)
	handler := newMCPMutationStoreTestHandler(t)
	principal := MCPMutationPrincipal{UserID: 7, Username: "mcp-owner", Issuer: "test-issuer"}
	spec := mcpGoalSpecFor(t, aliases[0], map[string]any{"resource": "fixture-resource"}, false)
	identity := mcpTestIdentity()
	params := goalActionParams(spec, &identity, handler, nil)
	runtime := params[StrConfig].(map[string]any)[cfgKeyAgentRuntime].(map[string]any)
	runtime[cfgKeyMCPMutationPrincipal] = principal
	result, err := (&AgentGoalRunAction{}).Execute(params)
	if err == nil {
		t.Fatalf("goal unexpectedly completed without trusted evidence")
	}
	_ = result
	var count int
	if err := handler.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM MCPMutationProposals`).Scan(&count); err != nil {
		t.Fatalf("count proposals: %v", err)
	}
	if count != 1 {
		t.Fatalf("proposal count = %d, want 1", count)
	}
	if data, err := os.ReadFile(counterPath); err == nil && len(data) > 0 {
		t.Fatalf("goal proposal executed MCP mutation: %s", data)
	}
}

func TestToolLoopRegistersExactProposalToolAndStaysInert(t *testing.T) {
	resetLLMProvidersForTest()
	provider := &scriptedProvider{name: "scripted", responses: []any{
		chatWithCalls(stringCall("tool-loop-proposal-call", "mcp_fixture_mutate_counter", `{"resource":"fixture-resource"}`)),
		chatWithText("proposal recorded"),
	}}
	RegisterLLMProvider(provider)
	t.Cleanup(resetLLMProvidersForTest)

	counterPath := filepath.Join(t.TempDir(), "tool-loop-authority-counter.log")
	config, aliases, _, _ := mcpAuthorityConfig(t, counterPath)
	setMCPTestConfig(t, config)
	handler := newMCPMutationStoreTestHandler(t)
	identity := mcpTestIdentity()
	params := loopTestParams()
	params["provider"] = "scripted"
	params["allowlist"] = []any{aliases[0]}
	configMap := params[StrConfig].(map[string]interface{})
	configMap["db_handler"] = handler
	runtime := configMap[cfgKeyAgentRuntime].(map[string]interface{})
	runtime["identity_snapshot"] = identity
	runtime[cfgKeyMCPMutationPrincipal] = MCPMutationPrincipal{UserID: 7, Username: identity.Owner, Issuer: "test-issuer"}
	result, err := (&AgentToolLoopAction{}).Execute(params)
	if err != nil {
		t.Fatalf("tool loop: %v (%v)", err, result[StrMessage])
	}
	output := result[StrResponse].(map[string]any)
	calls := output["tool_calls"].([]any)
	if len(calls) != 1 || calls[0].(map[string]any)["status"] != "executed" {
		t.Fatalf("proposal call summary: %+v", calls)
	}
	var count int
	if err := handler.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM MCPMutationProposals`).Scan(&count); err != nil {
		t.Fatalf("count proposals: %v", err)
	}
	if count != 1 {
		t.Fatalf("proposal count = %d, want 1", count)
	}
	if data, err := os.ReadFile(counterPath); err == nil && len(data) > 0 {
		t.Fatalf("tool loop executed MCP mutation: %s", data)
	}
}
