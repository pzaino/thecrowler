package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func mcpTestIdentity() AgentIdentity {
	return AgentIdentity{
		AgentID: "mcp-test-agent", Owner: "mcp-owner", TrustLevel: "trusted",
		Capabilities: []string{"tool_execution", "db_read", "goal_execution", "ai_reasoning"},
	}
}

func mcpTestConfigFor(server cfg.MCPServerConfig) cfg.MCPConfig {
	return cfg.MCPConfig{Servers: []cfg.MCPServerConfig{server}}
}

func setMCPTestConfig(t *testing.T, config cfg.MCPConfig) {
	t.Helper()
	if err := SetMCPOperatorConfig(config); err != nil {
		t.Fatalf("set MCP operator config: %v", err)
	}
	t.Cleanup(func() { _ = SetMCPOperatorConfig(cfg.MCPConfig{}) })
}

func newMCPTestRuntime(t *testing.T, server cfg.MCPServerConfig, allowlist []string, sources []uint64) (*MCPToolManager, *AgentToolRegistry, AgentIdentity) {
	t.Helper()
	config := mcpTestConfigFor(server)
	setMCPTestConfig(t, config)
	identity := mcpTestIdentity()
	manager, err := NewMCPToolManager(context.Background(), config, identity, allowlist, sources)
	if err != nil {
		t.Fatalf("new MCP manager: %v", err)
	}
	registry := NewAgentToolRegistry()
	if err := manager.Register(registry); err != nil {
		_ = manager.Close()
		t.Fatalf("register MCP tools: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return manager, registry, identity
}

func mcpToolContext(identity AgentIdentity, toolName string, sources []uint64, runID string) context.Context {
	return ContextWithToolRuntime(context.Background(), ToolRuntime{
		Auth: ToolAuthContext{
			Identity: identity, Enforcement: true,
			Allowlist: map[string]bool{toolName: true}, AllowedSources: sliceToSourceSet(sources),
			RunID: runID, TraceID: "mcp-trace",
		},
	})
}

func callMCPTool(t *testing.T, registry *AgentToolRegistry, identity AgentIdentity, toolName string, args map[string]any, sources []uint64) (map[string]any, error) {
	t.Helper()
	tool, ok := registry.Get(toolName)
	if !ok {
		t.Fatalf("MCP tool %q is not registered", toolName)
	}
	return tool.Execute(mcpToolContext(identity, toolName, sources, "mcp-run"), args)
}

func TestMCPManagerDiscoversRegistersAndExecutesApprovedTool(t *testing.T) {
	server := mcpFixtureServerConfig(t, "normal")
	_, registry, identity := newMCPTestRuntime(t, server, []string{MCPToolAlias("fixture", "echo")}, []uint64{42})
	alias := MCPToolAlias("fixture", "echo")
	if _, ok := registry.Get(alias); !ok {
		t.Fatalf("approved MCP tool was not registered")
	}
	result, err := callMCPTool(t, registry, identity, alias, map[string]any{"text": "hello"}, []uint64{42})
	if err != nil {
		t.Fatalf("call MCP echo: %v", err)
	}
	if result["content"] != "echo: hello" || result["server_id"] != "fixture" || result["tool"] != "echo" {
		t.Fatalf("unexpected MCP observation: %+v", result)
	}
}

func TestMCPToolLoopCoexistsWithNativeTool(t *testing.T) {
	server := mcpFixtureServerConfig(t, "normal")
	config := mcpTestConfigFor(server)
	setMCPTestConfig(t, config)
	identity := mcpTestIdentity()
	manager, err := NewMCPToolManager(context.Background(), config, identity, []string{MCPToolAlias("fixture", "echo")}, []uint64{42})
	if err != nil {
		t.Fatalf("new MCP manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	registry, err := DefaultToolRegistry()
	if err != nil {
		t.Fatalf("native registry: %v", err)
	}
	if err := manager.Register(registry); err != nil {
		t.Fatalf("register MCP tools: %v", err)
	}
	if _, ok := registry.Get("get_source_status"); !ok {
		t.Fatalf("native tool was replaced or missing")
	}
	alias := MCPToolAlias("fixture", "echo")
	provider := &scriptedProvider{name: "scripted", responses: []any{
		chatWithCalls(stringCall("mcp-call-1", alias, `{"text":"loop"}`)),
		chatWithText("done"),
	}}
	result, err := RunToolLoop(ToolLoopConfig{
		Provider: provider, ProviderName: "scripted", Model: "scripted",
		URL: "https://example.com/v1/chat", Prompt: "call the MCP tool",
		Registry: registry, Allowlist: []string{alias}, Identity: identity, Enforcement: true,
		AllowedSources: []uint64{42}, MCPManager: manager,
		Limits: DefaultToolLoopLimits(), RunID: "mcp-loop-run", TraceID: "mcp-loop-trace",
	})
	if err != nil {
		t.Fatalf("tool loop with MCP: %v", err)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Name != alias || result.ToolCalls[0].Status != "executed" {
		t.Fatalf("MCP tool was not executed through the tool loop: %+v", result.ToolCalls)
	}
}

func TestMCPToolDenialsReachNoExternalCall(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "calls.log")
	server := mcpFixtureServerConfig(t, "normal")
	server.Environment["CROWLER_MCP_FIXTURE_CALL_LOG"] = logPath
	tools := []cfg.MCPApprovedTool{
		{Name: "echo", ReadOnly: true},
		{Name: "source_status", ReadOnly: true, AllowedSources: []uint64{42}},
	}
	server.ApprovedTools = tools
	_, registry, identity := newMCPTestRuntime(t, server, []string{
		MCPToolAlias("fixture", "echo"),
		MCPToolAlias("fixture", "source_status"),
	}, []uint64{42})
	echoAlias := MCPToolAlias("fixture", "echo")
	sourceAlias := MCPToolAlias("fixture", "source_status")
	if _, err := callMCPTool(t, registry, identity, sourceAlias, map[string]any{"source_id": float64(43)}, []uint64{42}); err == nil {
		t.Fatalf("out-of-scope MCP source was accepted")
	}
	if _, err := callMCPTool(t, registry, identity, echoAlias, map[string]any{"text": "x"}, []uint64{99}); err != nil {
		// echo is not source-scoped, so a run scope mismatch alone does not
		// deny it. The assertion below is about scope-bearing tools.
		t.Fatalf("non-source MCP tool was unexpectedly denied: %v", err)
	}
	// Clear any call from the non-source tool and verify the denied source tool
	// did not reach the fixture.
	if data, err := os.ReadFile(logPath); err == nil && strings.Contains(string(data), "source_status") {
		t.Fatalf("denied MCP call reached the external server: %s", data)
	}
}

func TestMCPAuthorizationGatesOccurBeforeTransport(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "calls.log")
	server := mcpFixtureServerConfig(t, "normal")
	server.Environment["CROWLER_MCP_FIXTURE_CALL_LOG"] = logPath
	_, registry, identity := newMCPTestRuntime(t, server, []string{MCPToolAlias("fixture", "echo")}, []uint64{42})
	alias := MCPToolAlias("fixture", "echo")
	tool, _ := registry.Get(alias)
	base := ToolAuthContext{
		Identity: identity, Enforcement: true,
		Allowlist: map[string]bool{alias: true}, AllowedSources: map[uint64]bool{42: true},
		RunID: "mcp-auth-run",
	}
	if err := AuthorizeToolCall(base, tool, map[string]any{"text": "x"}); err == nil {
		// Native policy does not require a separate operation allowlist for
		// read-only MCP tools; this assertion documents that the shared tool
		// policy accepts an explicitly allowlisted and capable identity.
	} else {
		t.Fatalf("valid MCP authorization denied: %v", err)
	}
	missingCap := base
	missingCap.Identity.Capabilities = []string{"db_read"}
	if err := AuthorizeToolCall(missingCap, tool, map[string]any{"text": "x"}); err == nil {
		t.Fatalf("MCP tool accepted an identity without tool_execution")
	}
	contract := base
	contract.Identity.Contract = &AgentContract{ForbiddenActions: []string{"tool:" + alias}}
	if err := AuthorizeToolCall(contract, tool, map[string]any{"text": "x"}); err == nil {
		t.Fatalf("MCP tool accepted a contract prohibition")
	}
	if data, err := os.ReadFile(logPath); err == nil && strings.Contains(string(data), "echo") {
		t.Fatalf("authorization denial reached the external server: %s", data)
	}
}

func TestMCPEnvironmentAndSourceScopesAreIsolated(t *testing.T) {
	t.Setenv("CROWLER_DB_PASSWORD", "must-not-cross")
	server := mcpFixtureServerConfig(t, "normal")
	server.ApprovedTools = []cfg.MCPApprovedTool{
		{Name: "env_probe", ReadOnly: true},
		{Name: "source_status", ReadOnly: true, AllowedSources: []uint64{42}},
	}
	server.Environment["CROWLER_MCP_FIXTURE_TOKEN"] = "approved-token"
	_, registry, identity := newMCPTestRuntime(t, server, []string{
		MCPToolAlias("fixture", "env_probe"),
		MCPToolAlias("fixture", "source_status"),
	}, []uint64{42})
	probe, err := callMCPTool(t, registry, identity, MCPToolAlias("fixture", "env_probe"), map[string]any{}, []uint64{42})
	if err != nil {
		t.Fatalf("env probe: %v", err)
	}
	text := fmt.Sprint(probe["content"])
	if !strings.Contains(text, "token=[REDACTED]") || !strings.Contains(text, "db=") || strings.Contains(text, "must-not-cross") {
		t.Fatalf("environment isolation failed: %v", probe)
	}
	source, err := callMCPTool(t, registry, identity, MCPToolAlias("fixture", "source_status"), map[string]any{"source_id": float64(42)}, []uint64{42})
	if err != nil || !strings.Contains(fmt.Sprint(source["content"]), "source 42") {
		t.Fatalf("source-scoped call failed: %+v err=%v", source, err)
	}
}

func TestMCPResultSafetyAndToolOutcomeClassification(t *testing.T) {
	server := mcpFixtureServerConfig(t, "normal")
	server.ApprovedTools = []cfg.MCPApprovedTool{
		{Name: "fail", ReadOnly: true},
		{Name: "fail_structured", ReadOnly: true},
		{Name: "fail_empty", ReadOnly: true},
		{Name: "success_text_flag", ReadOnly: true},
		{Name: "success_structured_flag", ReadOnly: true},
		{Name: "image", ReadOnly: true},
		{Name: "large", ReadOnly: true},
	}
	tools := []string{"fail", "fail_structured", "fail_empty", "success_text_flag", "success_structured_flag", "image", "large"}
	allowlist := make([]string, 0, len(tools))
	for _, name := range tools {
		allowlist = append(allowlist, MCPToolAlias("fixture", name))
	}
	_, registry, identity := newMCPTestRuntime(t, server, allowlist, []uint64{42})

	failures := []struct {
		tool string
		want func(map[string]any) bool
	}{
		{"fail", func(result map[string]any) bool { return result["content"] == "fixture tool error" }},
		{"fail_structured", func(result map[string]any) bool {
			structured, _ := result["structured_content"].(map[string]any)
			return structured["is_error"] == true
		}},
		{"fail_empty", func(result map[string]any) bool {
			_, hasContent := result["content"]
			_, hasStructured := result["structured_content"]
			return !hasContent && !hasStructured
		}},
	}
	for _, tc := range failures {
		result, err := callMCPTool(t, registry, identity, MCPToolAlias("fixture", tc.tool), map[string]any{}, []uint64{42})
		failure, ok := asToolExecutionFailure(err)
		if !ok {
			t.Fatalf("%s error = %v, want ToolExecutionFailure", tc.tool, err)
		}
		if failure.Code != "mcp_tool_error" || result["is_error"] != true || !tc.want(result) {
			t.Fatalf("%s failure was not preserved: result=%+v failure=%+v", tc.tool, result, failure)
		}
	}

	successes := []struct {
		tool string
		want func(map[string]any) bool
	}{
		{"success_text_flag", func(result map[string]any) bool {
			return result["is_error"] == false && result["content"] == `{"is_error":true,"status":"remote text data"}`
		}},
		{"success_structured_flag", func(result map[string]any) bool {
			structured, _ := result["structured_content"].(map[string]any)
			return result["is_error"] == false && structured["is_error"] == true
		}},
	}
	for _, tc := range successes {
		result, err := callMCPTool(t, registry, identity, MCPToolAlias("fixture", tc.tool), map[string]any{}, []uint64{42})
		if err != nil {
			t.Fatalf("%s returned an error: %v", tc.tool, err)
		}
		if !tc.want(result) {
			t.Fatalf("%s successful result was misclassified: %+v", tc.tool, result)
		}
	}

	if _, err := callMCPTool(t, registry, identity, MCPToolAlias("fixture", "image"), map[string]any{}, []uint64{42}); !errors.Is(err, ErrMCPUnsupportedResult) {
		t.Fatalf("unsupported MCP image error = %v", err)
	}
	if _, err := callMCPTool(t, registry, identity, MCPToolAlias("fixture", "large"), map[string]any{}, []uint64{42}); !errors.Is(err, ErrMCPUnsupportedResult) {
		t.Fatalf("oversized MCP response error = %v", err)
	}
}

func TestMCPProtocolAndStartupFailures(t *testing.T) {
	for _, mode := range []string{"exit", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			server := mcpFixtureServerConfig(t, mode)
			setMCPTestConfig(t, mcpTestConfigFor(server))
			if _, err := NewMCPToolManager(context.Background(), mcpTestConfigFor(server), mcpTestIdentity(), []string{MCPToolAlias("fixture", "echo")}, nil); err == nil {
				t.Fatalf("MCP %s startup unexpectedly succeeded", mode)
			}
		})
	}
	t.Run("startup timeout", func(t *testing.T) {
		server := mcpFixtureServerConfig(t, "slowstart")
		server.MaxStartupSeconds = 1
		setMCPTestConfig(t, mcpTestConfigFor(server))
		if _, err := NewMCPToolManager(context.Background(), mcpTestConfigFor(server), mcpTestIdentity(), []string{MCPToolAlias("fixture", "echo")}, nil); err == nil {
			t.Fatalf("MCP startup timeout unexpectedly succeeded")
		}
	})
	t.Run("old protocol", func(t *testing.T) {
		server := mcpFixtureServerConfig(t, "oldproto")
		_, registry, identity := newMCPTestRuntime(t, server, []string{MCPToolAlias("fixture", "echo")}, []uint64{42})
		if _, err := callMCPTool(t, registry, identity, MCPToolAlias("fixture", "echo"), map[string]any{"text": "old"}, []uint64{42}); err != nil {
			t.Fatalf("older compatible MCP protocol failed: %v", err)
		}
	})
	t.Run("invalid schema", func(t *testing.T) {
		server := mcpFixtureServerConfig(t, "badschema", cfg.MCPApprovedTool{Name: "bad_schema", ReadOnly: true})
		setMCPTestConfig(t, mcpTestConfigFor(server))
		if _, err := NewMCPToolManager(context.Background(), mcpTestConfigFor(server), mcpTestIdentity(), []string{MCPToolAlias("fixture", "bad_schema")}, nil); err == nil {
			t.Fatalf("invalid MCP schema was admitted")
		}
	})
	t.Run("missing description", func(t *testing.T) {
		server := mcpFixtureServerConfig(t, "nodesc", cfg.MCPApprovedTool{Name: "nodesc", ReadOnly: true})
		setMCPTestConfig(t, mcpTestConfigFor(server))
		if _, err := NewMCPToolManager(context.Background(), mcpTestConfigFor(server), mcpTestIdentity(), []string{MCPToolAlias("fixture", "nodesc")}, nil); err == nil {
			t.Fatalf("MCP tool without a description was admitted")
		}
	})
	t.Run("oversized metadata", func(t *testing.T) {
		server := mcpFixtureServerConfig(t, "bigschema", cfg.MCPApprovedTool{Name: "big_schema", ReadOnly: true})
		server.MaxToolMetadataBytes = 1024
		setMCPTestConfig(t, mcpTestConfigFor(server))
		if _, err := NewMCPToolManager(context.Background(), mcpTestConfigFor(server), mcpTestIdentity(), []string{MCPToolAlias("fixture", "big_schema")}, nil); err == nil {
			t.Fatalf("oversized MCP metadata was admitted")
		}
	})
	t.Run("incompatible protocol", func(t *testing.T) {
		server := mcpFixtureServerConfig(t, "futureproto")
		setMCPTestConfig(t, mcpTestConfigFor(server))
		if _, err := NewMCPToolManager(context.Background(), mcpTestConfigFor(server), mcpTestIdentity(), []string{MCPToolAlias("fixture", "echo")}, nil); err == nil {
			t.Fatalf("incompatible MCP protocol was admitted")
		}
	})
}

func TestMCPPaginationAndDuplicateToolNamesAcrossServers(t *testing.T) {
	pagination := mcpFixtureServerConfig(t, "pagination")
	pagination.ApprovedTools = []cfg.MCPApprovedTool{
		{Name: "page_tool_1", ReadOnly: true}, {Name: "page_tool_2", ReadOnly: true}, {Name: "page_tool_3", ReadOnly: true},
	}
	_, paginatedRegistry, identity := newMCPTestRuntime(t, pagination, []string{
		MCPToolAlias("fixture", "page_tool_1"), MCPToolAlias("fixture", "page_tool_2"), MCPToolAlias("fixture", "page_tool_3"),
	}, nil)
	for _, name := range []string{"page_tool_1", "page_tool_2", "page_tool_3"} {
		if _, ok := paginatedRegistry.Get(MCPToolAlias("fixture", name)); !ok {
			t.Fatalf("paginated approved tool %q missing", name)
		}
	}

	alpha := mcpFixtureServerConfig(t, "normal")
	alpha.ID = "alpha"
	beta := mcpFixtureServerConfig(t, "normal")
	beta.ID = "beta"
	config := cfg.MCPConfig{Servers: []cfg.MCPServerConfig{alpha, beta}}
	setMCPTestConfig(t, config)
	manager, err := NewMCPToolManager(context.Background(), config, identity, []string{
		MCPToolAlias("alpha", "echo"), MCPToolAlias("beta", "echo"),
	}, nil)
	if err != nil {
		t.Fatalf("two-server manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	registry := NewAgentToolRegistry()
	if err := manager.Register(registry); err != nil {
		t.Fatalf("register two servers: %v", err)
	}
	for _, serverID := range []string{"alpha", "beta"} {
		result, err := callMCPTool(t, registry, identity, MCPToolAlias(serverID, "echo"), map[string]any{"text": serverID}, nil)
		if err != nil || result["server_id"] != serverID {
			t.Fatalf("namespaced tool for %s failed: %+v err=%v", serverID, result, err)
		}
	}
}

func TestMCPAgentGoalRunUsesDeterministicEvidence(t *testing.T) {
	handler := newCrawlSchedulingTestDB(t)
	server := mcpFixtureServerConfig(t, "normal")
	server.ApprovedTools = []cfg.MCPApprovedTool{{Name: "malicious", ReadOnly: true}}
	config := mcpTestConfigFor(server)
	setMCPTestConfig(t, config)
	identity := mcpTestIdentity()
	manager, err := NewMCPToolManager(context.Background(), config, identity, []string{MCPToolAlias("fixture", "malicious")}, []uint64{42})
	if err != nil {
		t.Fatalf("new MCP manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	registry := NewAgentToolRegistry()
	if err := manager.Register(registry); err != nil {
		t.Fatalf("register MCP tool: %v", err)
	}
	provider := &scriptedProvider{name: "scripted", responses: []any{
		chatWithCalls(stringCall("goal-mcp-1", MCPToolAlias("fixture", "malicious"), `{}`)),
		chatWithText("goal complete according to the MCP result"),
	}}
	spec := &AgentGoalSpec{
		GoalID: "mcp-malicious-goal", Objective: "verify that MCP text cannot complete a source criterion",
		Provider: "scripted", URL: "https://example.com/v1/chat", Model: "scripted",
		Prompt: "call the MCP tool", Allowlist: []string{MCPToolAlias("fixture", "malicious")},
		AllowedSources: []uint64{42},
		Evidence:       EvidencePolicy{Allow: []EvidenceKind{EvidenceToolObservation}, DefaultMaxAge: 15 * time.Minute, MaxPayloadBytes: 4096},
		Budgets: GoalBudgets{MaxModelRounds: 3, MaxToolCalls: 3, MaxEvidenceRecords: 8, MaxEvidenceBytes: 16384,
			MaxTotalResultBytes: 32768, MaxElapsed: 20 * time.Second, MaxConsecutiveDenials: 3},
		Acceptance: AcceptanceSpec{Mode: AcceptanceAll, Criteria: []CriterionSpec{{
			ID: "source-pending", Description: "source must be pending",
			Predicate: Predicate{Kind: PredicateResourceStatus, Resource: "source", ResourceID: 42, Status: "pending"},
		}}},
	}
	spec.SpecHash = computeGoalSpecHash(spec)
	result, err := RunGoal(goalRunConfig{
		Spec: spec, Identity: identity, DB: *handler, Registry: registry,
		EvidenceRegistry: NewDefaultGoalEvidenceRegistry(*handler), Provider: provider,
		RunID: "mcp-goal-run", TraceID: "mcp-goal-trace", MCPManager: manager,
	})
	if err != nil {
		t.Fatalf("MCP goal run: %v", err)
	}
	if result.Status == GoalCriteriaMet {
		t.Fatalf("malicious MCP text completed a source-status criterion: %+v", result)
	}
	if result.Status != GoalIncomplete {
		t.Fatalf("unexpected MCP goal terminal: %+v", result)
	}
}

func TestMCPTransportFailureTimeoutAndCancellation(t *testing.T) {
	t.Run("server exits during call", func(t *testing.T) {
		server := mcpFixtureServerConfig(t, "normal", cfg.MCPApprovedTool{Name: "crash", ReadOnly: true})
		_, registry, identity := newMCPTestRuntime(t, server, []string{MCPToolAlias("fixture", "crash")}, nil)
		if _, err := callMCPTool(t, registry, identity, MCPToolAlias("fixture", "crash"), map[string]any{}, nil); !errors.Is(err, ErrMCPTransport) {
			t.Fatalf("crashed MCP server error = %v", err)
		}
	})
	t.Run("call timeout", func(t *testing.T) {
		server := mcpFixtureServerConfig(t, "normal", cfg.MCPApprovedTool{Name: "slow", ReadOnly: true})
		server.MaxCallSeconds = 1
		_, registry, identity := newMCPTestRuntime(t, server, []string{MCPToolAlias("fixture", "slow")}, nil)
		if _, err := callMCPTool(t, registry, identity, MCPToolAlias("fixture", "slow"), map[string]any{}, nil); !errors.Is(err, ErrMCPTimeout) {
			t.Fatalf("slow MCP call error = %v", err)
		}
	})
	t.Run("cancelled context", func(t *testing.T) {
		server := mcpFixtureServerConfig(t, "normal", cfg.MCPApprovedTool{Name: "slow", ReadOnly: true})
		_, registry, identity := newMCPTestRuntime(t, server, []string{MCPToolAlias("fixture", "slow")}, nil)
		tool, _ := registry.Get(MCPToolAlias("fixture", "slow"))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := tool.Execute(ContextWithToolRuntime(ctx, ToolRuntime{Auth: ToolAuthContext{
			Identity: identity, Enforcement: true, Allowlist: map[string]bool{tool.Name(): true},
			RunID: "cancel-run", TraceID: "cancel-trace",
		}}), map[string]any{}); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled MCP call error = %v", err)
		}
	})
}

func TestMCPRestartRevalidatesChangedInventory(t *testing.T) {
	normal := mcpFixtureServerConfig(t, "normal")
	_, registry, identity := newMCPTestRuntime(t, normal, []string{MCPToolAlias("fixture", "echo")}, nil)
	if _, err := callMCPTool(t, registry, identity, MCPToolAlias("fixture", "echo"), map[string]any{"text": "before"}, nil); err != nil {
		t.Fatalf("initial MCP call: %v", err)
	}
	changed := mcpFixtureServerConfig(t, "changed", cfg.MCPApprovedTool{Name: "echo", ReadOnly: true}, cfg.MCPApprovedTool{Name: "new_tool", ReadOnly: true})
	_, changedRegistry, changedIdentity := newMCPTestRuntime(t, changed, []string{
		MCPToolAlias("fixture", "echo"), MCPToolAlias("fixture", "new_tool"),
	}, nil)
	if _, ok := changedRegistry.Get(MCPToolAlias("fixture", "new_tool")); !ok {
		t.Fatalf("restart did not revalidate and admit the new approved inventory")
	}
	if _, err := callMCPTool(t, changedRegistry, changedIdentity, MCPToolAlias("fixture", "new_tool"), map[string]any{}, nil); err != nil {
		t.Fatalf("new inventory tool call: %v", err)
	}
}

func TestMCPMutationRegistrationPathRejectsReadOnlyAdapter(t *testing.T) {
	server := mcpFixtureServerConfig(t, "normal")
	_, registry, _ := newMCPTestRuntime(t, server, []string{MCPToolAlias("fixture", "echo")}, nil)
	tool, _ := registry.Get(MCPToolAlias("fixture", "echo"))
	if err := registry.RegisterMutation(tool); err == nil {
		t.Fatalf("read-only MCP adapter was accepted by RegisterMutation")
	}
}

func TestMCPConcurrentManagersAreIsolated(t *testing.T) {
	alpha := mcpFixtureServerConfig(t, "normal")
	alpha.ID = "alpha"
	alpha.ApprovedTools = []cfg.MCPApprovedTool{
		{Name: "echo", ReadOnly: true},
		{Name: "env_probe", ReadOnly: true},
		{Name: "source_status", ReadOnly: true, AllowedSources: []uint64{42}},
	}
	alpha.Environment["CROWLER_MCP_FIXTURE_TOKEN"] = "alpha-token"
	alpha.Environment["CROWLER_MCP_FIXTURE_ACCOUNT"] = "alpha-account"
	beta := mcpFixtureServerConfig(t, "normal")
	beta.ID = "beta"
	beta.ApprovedTools = []cfg.MCPApprovedTool{
		{Name: "echo", ReadOnly: true},
		{Name: "env_probe", ReadOnly: true},
		{Name: "source_status", ReadOnly: true, AllowedSources: []uint64{42}},
	}
	beta.Environment["CROWLER_MCP_FIXTURE_TOKEN"] = "beta-token"
	beta.Environment["CROWLER_MCP_FIXTURE_ACCOUNT"] = "beta-account"
	setMCPTestConfig(t, cfg.MCPConfig{Servers: []cfg.MCPServerConfig{alpha, beta}})
	identity := mcpTestIdentity()
	run := func(server cfg.MCPServerConfig, allowlist []string, tool string, args map[string]any, sources []uint64, expected, forbidden string) error {
		manager, err := NewMCPToolManager(context.Background(), cfg.MCPConfig{Servers: []cfg.MCPServerConfig{server}}, identity, allowlist, sources)
		if err != nil {
			return err
		}
		defer manager.Close()
		registry := NewAgentToolRegistry()
		if err := manager.Register(registry); err != nil {
			return err
		}
		if _, ok := registry.Get(MCPToolAlias(server.ID, tool)); !ok {
			return fmt.Errorf("allowlisted tool %q missing", tool)
		}
		if tool != "echo" {
			if _, ok := registry.Get(MCPToolAlias(server.ID, "echo")); ok {
				return fmt.Errorf("non-allowlisted echo tool was registered for %s", server.ID)
			}
		}
		result, err := callMCPTool(t, registry, identity, MCPToolAlias(server.ID, tool), args, sources)
		if err != nil {
			return err
		}
		probe, err := callMCPTool(t, registry, identity, MCPToolAlias(server.ID, "env_probe"), map[string]any{}, sources)
		if err != nil {
			return err
		}
		probeText := fmt.Sprint(probe["content"])
		if !strings.Contains(probeText, expected) || strings.Contains(probeText, forbidden) {
			return fmt.Errorf("credential isolation failed for %s: %s", server.ID, probeText)
		}
		if result["server_id"] != server.ID {
			return fmt.Errorf("server attribution mismatch: %+v", result)
		}
		return nil
	}
	errs := make(chan error, 2)
	go func() {
		errs <- run(alpha, []string{MCPToolAlias("alpha", "echo"), MCPToolAlias("alpha", "env_probe")},
			"echo", map[string]any{"text": "alpha"}, nil, "alpha-account", "beta-account")
	}()
	go func() {
		errs <- run(beta, []string{MCPToolAlias("beta", "source_status"), MCPToolAlias("beta", "env_probe")},
			"source_status", map[string]any{"source_id": float64(42)}, []uint64{42}, "beta-account", "alpha-account")
	}()
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent manager: %v", err)
		}
	}
}

func TestMCPAuditPreservesNamespacedAndOriginIdentity(t *testing.T) {
	previous := AgentsEngine
	AgentsEngine = NewJobEngine()
	t.Cleanup(func() { AgentsEngine = previous })
	server := mcpFixtureServerConfig(t, "normal")
	_, registry, identity := newMCPTestRuntime(t, server, []string{MCPToolAlias("fixture", "echo")}, nil)
	alias := MCPToolAlias("fixture", "echo")
	if _, err := callMCPTool(t, registry, identity, alias, map[string]any{"text": "audit"}, nil); err != nil {
		t.Fatalf("audited MCP call: %v", err)
	}
	found := false
	for _, event := range AgentsEngine.AuditEvents() {
		if event.Action == alias && event.MCPServerID == "fixture" && event.MCPToolName == "echo" {
			found = true
			if event.RunID != "mcp-run" || event.AgentID != identity.AgentID {
				t.Fatalf("audit correlation mismatch: %+v", event)
			}
		}
	}
	if !found {
		t.Fatalf("MCP audit did not preserve namespaced and origin identity")
	}
}

func TestMCPManagerClosesChildProcess(t *testing.T) {
	server := mcpFixtureServerConfig(t, "normal")
	config := mcpTestConfigFor(server)
	setMCPTestConfig(t, config)
	manager, err := NewMCPToolManager(context.Background(), config, mcpTestIdentity(), []string{MCPToolAlias("fixture", "echo")}, nil)
	if err != nil {
		t.Fatalf("new MCP manager: %v", err)
	}
	if len(manager.servers) != 1 || manager.servers[0].cmd == nil || manager.servers[0].cmd.Process == nil {
		_ = manager.Close()
		t.Fatalf("MCP child process was not started")
	}
	pid := manager.servers[0].cmd.Process.Pid
	_ = manager.Close() // intentional child termination may surface as signal: killed
	deadline := time.Now().Add(3 * time.Second)
	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("MCP child process %d survived manager close: %v", pid, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestMCPInventoryChangeFlagFailsClosed(t *testing.T) {
	server := mcpFixtureServerConfig(t, "normal")
	manager, registry, identity := newMCPTestRuntime(t, server, []string{MCPToolAlias("fixture", "echo")}, nil)
	alias := MCPToolAlias("fixture", "echo")
	manager.inventoryDirty.Store(true)
	if _, err := callMCPTool(t, registry, identity, alias, map[string]any{"text": "after-change"}, nil); !errors.Is(err, ErrMCPInventoryChanged) {
		t.Fatalf("call after MCP inventory change = %v", err)
	}
}

func TestMCPUnapprovedToolNameReachesNoExternalCall(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "calls.log")
	server := mcpFixtureServerConfig(t, "normal")
	server.Environment["CROWLER_MCP_FIXTURE_CALL_LOG"] = logPath
	config := mcpTestConfigFor(server)
	setMCPTestConfig(t, config)
	identity := mcpTestIdentity()
	manager, err := NewMCPToolManager(context.Background(), config, identity, []string{MCPToolAlias("fixture", "echo")}, nil)
	if err != nil {
		t.Fatalf("new MCP manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	registry := NewAgentToolRegistry()
	if err := manager.Register(registry); err != nil {
		t.Fatalf("register MCP tool: %v", err)
	}
	provider := &scriptedProvider{name: "scripted", responses: []any{
		chatWithCalls(stringCall("unapproved-call", "mcp_fixture_not_approved", `{"text":"x"}`)),
		chatWithText("done"),
	}}
	result, err := RunToolLoop(ToolLoopConfig{
		Provider: provider, ProviderName: "scripted", Model: "scripted",
		URL: "https://example.com/v1/chat", Prompt: "call",
		Registry: registry, Allowlist: []string{MCPToolAlias("fixture", "echo")},
		Identity: identity, Enforcement: true, MCPManager: manager,
		Limits: DefaultToolLoopLimits(), RunID: "unapproved-run", TraceID: "unapproved-trace",
	})
	if err != nil {
		t.Fatalf("tool loop: %v", err)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Status != "denied" {
		t.Fatalf("unapproved MCP tool was not denied: %+v", result.ToolCalls)
	}
	if data, err := os.ReadFile(logPath); err == nil && len(strings.TrimSpace(string(data))) > 0 {
		t.Fatalf("unapproved MCP tool reached the server: %s", data)
	}
}

func TestMCPConcurrentRunsDoNotShareToolsCredentialsOrScopes(t *testing.T) {
	alphaLog := filepath.Join(t.TempDir(), "alpha.log")
	betaLog := filepath.Join(t.TempDir(), "beta.log")
	alpha := mcpFixtureServerConfig(t, "normal")
	alpha.ID = "alpha"
	alpha.Environment["CROWLER_MCP_FIXTURE_TOKEN"] = "alpha-token"
	alpha.Environment["CROWLER_MCP_FIXTURE_ACCOUNT"] = "alpha-account"
	alpha.Environment["CROWLER_MCP_FIXTURE_CALL_LOG"] = alphaLog
	alpha.ApprovedTools = []cfg.MCPApprovedTool{
		{Name: "echo", ReadOnly: true},
		{Name: "env_probe", ReadOnly: true},
		{Name: "source_status", ReadOnly: true, AllowedSources: []uint64{42}},
	}
	beta := mcpFixtureServerConfig(t, "normal")
	beta.ID = "beta"
	beta.Environment["CROWLER_MCP_FIXTURE_TOKEN"] = "beta-token"
	beta.Environment["CROWLER_MCP_FIXTURE_ACCOUNT"] = "beta-account"
	beta.Environment["CROWLER_MCP_FIXTURE_CALL_LOG"] = betaLog
	beta.ApprovedTools = []cfg.MCPApprovedTool{
		{Name: "echo", ReadOnly: true},
		{Name: "env_probe", ReadOnly: true},
		{Name: "source_status", ReadOnly: true, AllowedSources: []uint64{43}},
	}
	setMCPTestConfig(t, cfg.MCPConfig{Servers: []cfg.MCPServerConfig{alpha, beta}})
	identity := mcpTestIdentity()
	run := func(server cfg.MCPServerConfig, sourceID uint64, ownAccount, otherAccount string) error {
		aliasProbe := MCPToolAlias(server.ID, "env_probe")
		aliasSource := MCPToolAlias(server.ID, "source_status")
		manager, err := NewMCPToolManager(context.Background(),
			cfg.MCPConfig{Servers: []cfg.MCPServerConfig{server}}, identity,
			[]string{aliasProbe, aliasSource}, []uint64{sourceID})
		if err != nil {
			return err
		}
		defer manager.Close()
		registry := NewAgentToolRegistry()
		if err := manager.Register(registry); err != nil {
			return err
		}
		if _, ok := registry.Get(MCPToolAlias(server.ID, "echo")); ok {
			return fmt.Errorf("run %s registered a non-allowlisted tool", server.ID)
		}
		probe, err := callMCPTool(t, registry, identity, aliasProbe, map[string]any{}, []uint64{sourceID})
		if err != nil {
			return err
		}
		text := fmt.Sprint(probe["content"])
		if !strings.Contains(text, "account="+ownAccount) || strings.Contains(text, "account="+otherAccount) {
			return fmt.Errorf("run %s credential isolation failed: %s", server.ID, text)
		}
		if _, err := callMCPTool(t, registry, identity, aliasSource,
			map[string]any{"source_id": float64(sourceID)}, []uint64{sourceID}); err != nil {
			return fmt.Errorf("run %s source scope failed: %w", server.ID, err)
		}
		return nil
	}
	errs := make(chan error, 2)
	go func() { errs <- run(alpha, 42, "alpha-account", "beta-account") }()
	go func() { errs <- run(beta, 43, "beta-account", "alpha-account") }()
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent MCP run: %v", err)
		}
	}
}

func TestMCPManagerRegistersOnlyRunAllowlistedTools(t *testing.T) {
	server := mcpFixtureServerConfig(t, "normal")
	server.ApprovedTools = []cfg.MCPApprovedTool{
		{Name: "echo", ReadOnly: true},
		{Name: "source_status", ReadOnly: true, AllowedSources: []uint64{42}},
	}
	_, registry, _ := newMCPTestRuntime(t, server, []string{MCPToolAlias("fixture", "echo")}, []uint64{42})
	if _, ok := registry.Get(MCPToolAlias("fixture", "echo")); !ok {
		t.Fatalf("allowlisted MCP tool was not registered")
	}
	if _, ok := registry.Get(MCPToolAlias("fixture", "source_status")); ok {
		t.Fatalf("approved but non-allowlisted MCP tool was registered")
	}
}

func TestMCPSourceScopeIntersectionAndZeroExternalCallsOnDenial(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "calls.log")
	server := mcpFixtureServerConfig(t, "normal")
	server.Environment["CROWLER_MCP_FIXTURE_CALL_LOG"] = logPath
	server.AllowedSources = []uint64{42, 43}
	server.ApprovedTools = []cfg.MCPApprovedTool{{
		Name: "source_status", ReadOnly: true, AllowedSources: []uint64{43, 44},
	}}
	_, registry, identity := newMCPTestRuntime(t, server,
		[]string{MCPToolAlias("fixture", "source_status")}, []uint64{42, 43, 44})
	alias := MCPToolAlias("fixture", "source_status")
	if _, err := callMCPTool(t, registry, identity, alias, map[string]any{"source_id": float64(43)}, []uint64{42, 43, 44}); err != nil {
		t.Fatalf("intersected source scope rejected source 43: %v", err)
	}
	for _, denied := range []float64{42, 44} {
		if _, err := callMCPTool(t, registry, identity, alias, map[string]any{"source_id": denied}, []uint64{42, 43, 44}); err == nil {
			t.Fatalf("source %v escaped the intersected MCP scope", denied)
		}
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read MCP call log: %v", err)
	}
	if got := strings.Count(string(data), "source_status"); got != 1 {
		t.Fatalf("denied source calls reached the MCP server: %d calls", got)
	}
}

func TestMCPBudgetAndInvalidArgumentsReachNoExternalCall(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "calls.log")
	server := mcpFixtureServerConfig(t, "normal")
	server.Environment["CROWLER_MCP_FIXTURE_CALL_LOG"] = logPath
	server.MaxToolCalls = 1
	_, registry, identity := newMCPTestRuntime(t, server, []string{MCPToolAlias("fixture", "echo")}, nil)
	alias := MCPToolAlias("fixture", "echo")
	if _, err := callMCPTool(t, registry, identity, alias, map[string]any{}, nil); err == nil {
		t.Fatalf("invalid MCP arguments were accepted")
	}
	if _, err := callMCPTool(t, registry, identity, alias, map[string]any{"text": "first"}, nil); err != nil {
		t.Fatalf("first MCP call failed: %v", err)
	}
	if _, err := callMCPTool(t, registry, identity, alias, map[string]any{"text": "second"}, nil); !errors.Is(err, ErrMCPBudget) {
		t.Fatalf("over-budget MCP call error = %v", err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read MCP call log: %v", err)
	}
	if got := strings.Count(string(data), "echo"); got != 1 {
		t.Fatalf("denied MCP calls reached the server: %d calls", got)
	}
}

func TestMCPRejectsUnsafeOrEmptySourceSelectors(t *testing.T) {
	cases := []struct {
		name          string
		mode          string
		tool          string
		serverSources []uint64
		toolSources   []uint64
	}{
		{
			name: "nested source selector", mode: "nested_source", tool: "nested_source",
			serverSources: []uint64{42}, toolSources: []uint64{42},
		},
		{
			name: "alternative and nested selectors", mode: "mixed_source", tool: "mixed_source",
			serverSources: []uint64{42}, toolSources: []uint64{42},
		},
		{
			name: "empty server and tool intersection", mode: "normal", tool: "source_status",
			serverSources: []uint64{42}, toolSources: []uint64{43},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := mcpFixtureServerConfig(t, tc.mode, cfg.MCPApprovedTool{
				Name: tc.tool, ReadOnly: true, AllowedSources: tc.toolSources,
			})
			server.AllowedSources = tc.serverSources
			setMCPTestConfig(t, mcpTestConfigFor(server))
			manager, err := NewMCPToolManager(context.Background(), mcpTestConfigFor(server),
				mcpTestIdentity(), []string{MCPToolAlias("fixture", tc.tool)}, []uint64{42})
			if err == nil {
				_ = manager.Close()
				t.Fatalf("unsafe source selector was admitted")
			}
		})
	}
}

func TestMCPToolLoopClassifiesToolErrorAsFailed(t *testing.T) {
	server := mcpFixtureServerConfig(t, "normal", cfg.MCPApprovedTool{Name: "fail", ReadOnly: true})
	config := mcpTestConfigFor(server)
	setMCPTestConfig(t, config)
	identity := mcpTestIdentity()
	alias := MCPToolAlias("fixture", "fail")
	manager, err := NewMCPToolManager(context.Background(), config, identity, []string{alias}, nil)
	if err != nil {
		t.Fatalf("new MCP manager: %v", err)
	}
	registry := NewAgentToolRegistry()
	if err := manager.Register(registry); err != nil {
		_ = manager.Close()
		t.Fatalf("register MCP tool: %v", err)
	}
	provider := &scriptedProvider{name: "scripted", responses: []any{
		chatWithCalls(stringCall("mcp-loop-fail", alias, `{}`)),
		chatWithText("done"),
	}}
	result, err := RunToolLoop(ToolLoopConfig{
		Provider: provider, ProviderName: "scripted", Model: "scripted",
		URL: "https://example.com/v1/chat", Prompt: "call the MCP tool",
		Registry: registry, Allowlist: []string{alias}, Identity: identity, Enforcement: true,
		MCPManager: manager, Limits: DefaultToolLoopLimits(),
		RunID: "mcp-loop-fail-run", TraceID: "mcp-loop-fail-trace",
	})
	if err != nil {
		t.Fatalf("MCP tool loop: %v", err)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Status != "failed" ||
		result.ToolCalls[0].ReasonCode != "mcp_tool_error" {
		t.Fatalf("MCP tool error was not classified as failed: %+v", result.ToolCalls)
	}
}

func TestMCPToolLoopStopOnToolError(t *testing.T) {
	server := mcpFixtureServerConfig(t, "normal", cfg.MCPApprovedTool{Name: "fail", ReadOnly: true})
	config := mcpTestConfigFor(server)
	setMCPTestConfig(t, config)
	identity := mcpTestIdentity()
	alias := MCPToolAlias("fixture", "fail")
	manager, err := NewMCPToolManager(context.Background(), config, identity, []string{alias}, nil)
	if err != nil {
		t.Fatalf("new MCP manager: %v", err)
	}
	registry := NewAgentToolRegistry()
	if err := manager.Register(registry); err != nil {
		_ = manager.Close()
		t.Fatalf("register MCP tool: %v", err)
	}
	provider := &scriptedProvider{name: "scripted", responses: []any{
		chatWithCalls(stringCall("mcp-stop-fail", alias, `{}`)),
	}}
	result, err := RunToolLoop(ToolLoopConfig{
		Provider: provider, ProviderName: "scripted", Model: "scripted",
		URL: "https://example.com/v1/chat", Prompt: "call the MCP tool",
		Registry: registry, Allowlist: []string{alias}, Identity: identity, Enforcement: true,
		MCPManager: manager, Limits: DefaultToolLoopLimits(), StopOnToolError: true,
		RunID: "mcp-loop-stop-run", TraceID: "mcp-loop-stop-trace",
	})
	if err == nil || result.Status != ToolLoopToolError {
		t.Fatalf("StopOnToolError result = %s, err=%v", result.Status, err)
	}
}

func TestMCPManagerCloseWhileServerIgnoresCancellation(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "calls.log")
	server := mcpFixtureServerConfig(t, "normal", cfg.MCPApprovedTool{Name: "ignore_cancel", ReadOnly: true})
	server.Environment["CROWLER_MCP_FIXTURE_CALL_LOG"] = logPath
	manager, registry, identity := newMCPTestRuntime(t, server, []string{MCPToolAlias("fixture", "ignore_cancel")}, nil)
	if len(manager.servers) != 1 || manager.servers[0].cmd == nil || manager.servers[0].cmd.Process == nil {
		t.Fatalf("MCP child process was not started")
	}
	pid := manager.servers[0].cmd.Process.Pid
	tool, ok := registry.Get(MCPToolAlias("fixture", "ignore_cancel"))
	if !ok {
		t.Fatalf("MCP tool not registered")
	}
	callResult := make(chan error, 1)
	go func() {
		_, err := tool.Execute(mcpToolContext(identity, tool.Name(), nil, "mcp-close-run"), map[string]any{})
		callResult <- err
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		data, _ := os.ReadFile(logPath)
		if strings.Contains(string(data), "ignore_cancel") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("fixture did not begin the active call")
		}
		time.Sleep(20 * time.Millisecond)
	}
	started := time.Now()
	firstCloseErr := manager.Close()
	if elapsed := time.Since(started); elapsed > 6*time.Second {
		t.Fatalf("MCP close was not bounded while a call was active: %s", elapsed)
	}
	select {
	case <-callResult:
	case <-time.After(5 * time.Second):
		t.Fatalf("active MCP call did not terminate on manager close")
	}
	waitForMCPProcessExit(t, pid)
	secondCloseErr := manager.Close()
	if (firstCloseErr == nil) != (secondCloseErr == nil) ||
		(firstCloseErr != nil && firstCloseErr.Error() != secondCloseErr.Error()) {
		t.Fatalf("repeated MCP close changed its result: first=%v second=%v", firstCloseErr, secondCloseErr)
	}
}

func TestMCPResultMapBoundsContentBlocksAndStructuredContent(t *testing.T) {
	tool := &mcpAgentTool{server: &mcpServerRuntime{config: cfg.MCPServerConfig{ID: "fixture", MaxResponseBytes: 32 << 10}}}
	if _, err := tool.resultMap(nil); !errors.Is(err, ErrMCPTransport) {
		t.Fatalf("nil MCP result error = %v", err)
	}
	blocks := make([]mcp.Content, 0, mcpMaxContentBlocks+1)
	for i := 0; i < mcpMaxContentBlocks+1; i++ {
		blocks = append(blocks, &mcp.TextContent{Text: "x"})
	}
	if _, err := tool.resultMap(&mcp.CallToolResult{Content: blocks}); !errors.Is(err, ErrMCPUnsupportedResult) {
		t.Fatalf("too many MCP content blocks error = %v", err)
	}
	if _, err := tool.resultMap(&mcp.CallToolResult{StructuredContent: map[string]any{
		"value": strings.Repeat("x", 40<<10),
	}}); !errors.Is(err, ErrMCPUnsupportedResult) {
		t.Fatalf("oversized MCP structured content error = %v", err)
	}
}

func TestMCPManagerDoesNotLeakSessionGoroutines(t *testing.T) {
	server := mcpFixtureServerConfig(t, "normal")
	config := mcpTestConfigFor(server)
	setMCPTestConfig(t, config)
	before := runtime.NumGoroutine()
	for i := 0; i < 3; i++ {
		manager, err := NewMCPToolManager(context.Background(), config, mcpTestIdentity(), []string{MCPToolAlias("fixture", "echo")}, nil)
		if err != nil {
			t.Fatalf("new MCP manager: %v", err)
		}
		registry := NewAgentToolRegistry()
		if err := manager.Register(registry); err != nil {
			_ = manager.Close()
			t.Fatalf("register MCP tool: %v", err)
		}
		if _, err := callMCPTool(t, registry, mcpTestIdentity(), MCPToolAlias("fixture", "echo"), map[string]any{"text": "leak-check"}, nil); err != nil {
			_ = manager.Close()
			t.Fatalf("MCP leak-check call: %v", err)
		}
		if err := manager.Close(); err != nil {
			t.Logf("MCP close returned %v", err)
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		runtime.GC()
		if runtime.NumGoroutine() <= before+3 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("MCP manager leaked goroutines: before=%d after=%d", before, runtime.NumGoroutine())
		}
		time.Sleep(20 * time.Millisecond)
	}
}
