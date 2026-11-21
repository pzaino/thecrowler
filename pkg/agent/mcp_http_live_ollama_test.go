//go:build integration

package agent

import (
	"context"
	"fmt"
	"testing"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func liveHTTPMCPManager(t *testing.T, tools ...cfg.MCPApprovedTool) (*MCPToolManager, *AgentToolRegistry, AgentIdentity) {
	t.Helper()
	server, caFile := mcpHTTPFixtureTLS(t, "json", "live-http-token")
	t.Setenv("MCP_HTTP_LIVE_TOKEN", "live-http-token")
	serverConfig := mcpHTTPFixtureConfig(t, server.URL, caFile, tools...)
	serverConfig.Auth = cfg.MCPHTTPAuthConfig{Mode: "bearer", TokenEnv: "MCP_HTTP_LIVE_TOKEN"}
	config := mcpTestConfigFor(serverConfig)
	setMCPTestConfig(t, config)
	identity := mcpTestIdentity()
	allowlist := make([]string, 0, len(tools))
	for _, tool := range tools {
		allowlist = append(allowlist, MCPToolAlias("remote", tool.Name))
	}
	manager, err := NewMCPToolManager(context.Background(), config, identity, allowlist, []uint64{42})
	if err != nil {
		t.Fatalf("new live HTTP MCP manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	registry, err := DefaultToolRegistry()
	if err != nil {
		t.Fatalf("native registry: %v", err)
	}
	if err := manager.Register(registry); err != nil {
		t.Fatalf("register live HTTP MCP tools: %v", err)
	}
	return manager, registry, identity
}

func TestMCPLiveOllamaStreamableHTTPToolLoopOptional(t *testing.T) {
	baseURL, model := ollamaSmokeConfig(t)
	db := openCrawlApprovalPostgresDB(t)
	ownerID, _ := setupCrawlApprovalPostgresUsers(t, db)
	sourceID := createCrawlApprovalPostgresSource(t, db, ownerID)
	manager, registry, identity := liveHTTPMCPManager(t, cfg.MCPApprovedTool{Name: "echo", ReadOnly: true})
	identity.Owner = fmt.Sprintf("wp03b2b-owner-%d", ownerID)
	provider := NewOllamaProvider([]string{model})
	alias := MCPToolAlias("remote", "echo")
	loop := loopTestConfig(provider, registry)
	loop.ProviderName = "ollama"
	loop.Model = model
	loop.URL = baseURL
	loop.Allowlist = []string{alias, "get_source_status"}
	loop.AllowedSources = []uint64{sourceID}
	loop.Identity = identity
	loop.DB = *db
	loop.MCPManager = manager
	loop.RunID = "mcp-http-live-run"
	loop.TraceID = "mcp-http-live-trace"
	loop.Prompt = fmt.Sprintf(`Call both tools, one after the other: first call %s with {"text":"live-http-mcp"}, then call get_source_status with {"source_id":%d}. After both tool results, reply with: done. Do not call any other tool.`, alias, sourceID)
	result, err := RunToolLoop(loop)
	if err != nil {
		t.Fatalf("live HTTP MCP tool loop: %v", err)
	}
	calls := map[string]string{}
	for _, call := range result.ToolCalls {
		calls[call.Name] = call.Status
	}
	if calls[alias] != "executed" || calls["get_source_status"] != "executed" {
		t.Fatalf("live model did not execute both approved HTTP/native tools: %+v", result.ToolCalls)
	}
	t.Logf("live HTTP MCP+native tool loop: %+v", result.ToolCalls)
}

func TestMCPLiveOllamaStreamableHTTPGoalOptional(t *testing.T) {
	baseURL, model := ollamaSmokeConfig(t)
	manager, registry, identity := liveHTTPMCPManager(t, cfg.MCPApprovedTool{Name: "echo", ReadOnly: true})
	_ = manager
	provider := NewOllamaProvider([]string{model})
	alias := MCPToolAlias("remote", "echo")
	args := map[string]any{"text": "http-goal-evidence"}
	providerSpec := mcpGoalSpecFor(t, alias, args, false)
	providerSpec.Provider = "ollama"
	providerSpec.URL = baseURL
	providerSpec.Model = model
	providerSpec.Prompt = fmt.Sprintf(`Call the approved MCP tool %s once with {"text":"http-goal-evidence"}. After the tool result, reply with: goal evidence recorded.`, alias)
	providerSpec.SpecHash = computeGoalSpecHash(providerSpec)
	result, err := RunGoal(goalRunConfig{
		Spec: providerSpec, Identity: identity, Registry: registry,
		EvidenceRegistry: NewGoalEvidenceRegistry(), Provider: provider,
		RunID: "mcp-http-live-goal", TraceID: "mcp-http-live-goal-trace",
	})
	if err != nil {
		t.Fatalf("live HTTP MCP goal: %v", err)
	}
	if result.Status != GoalCriteriaMet || len(result.Evidence) != 1 {
		t.Fatalf("live HTTP MCP goal did not satisfy evidence: %+v", result)
	}
	t.Logf("live HTTP MCP goal: status=%s calls=%+v evidence=%d", result.Status, result.ToolCalls, len(result.Evidence))
}

func TestMCPLiveOllamaStreamableHTTPFailedToolCannotSatisfyGoalOptional(t *testing.T) {
	baseURL, model := ollamaSmokeConfig(t)
	manager, registry, identity := liveHTTPMCPManager(t, cfg.MCPApprovedTool{Name: "fail", ReadOnly: true})
	_ = manager
	provider := NewOllamaProvider([]string{model})
	alias := MCPToolAlias("remote", "fail")
	providerSpec := mcpGoalSpecFor(t, alias, map[string]any{}, false)
	providerSpec.Provider = "ollama"
	providerSpec.URL = baseURL
	providerSpec.Model = model
	providerSpec.Prompt = fmt.Sprintf(`Call the approved MCP tool %s exactly once with {}. After the tool result, reply with: done. Do not call any other tool.`, alias)
	providerSpec.SpecHash = computeGoalSpecHash(providerSpec)
	result, err := RunGoal(goalRunConfig{
		Spec: providerSpec, Identity: identity, Registry: registry,
		EvidenceRegistry: NewGoalEvidenceRegistry(), Provider: provider,
		RunID: "mcp-http-live-error-goal", TraceID: "mcp-http-live-error-trace",
	})
	if err != nil {
		t.Fatalf("live HTTP failed-tool goal: %v", err)
	}
	if result.Status == GoalCriteriaMet || len(result.Evidence) != 0 {
		t.Fatalf("live HTTP failed tool satisfied evidence: %+v", result)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Status != "failed" ||
		result.ToolCalls[0].ReasonCode != "mcp_tool_error" {
		t.Fatalf("live HTTP failure classification lost: %+v", result.ToolCalls)
	}
	t.Logf("live HTTP error goal: status=%s calls=%+v evidence=%d", result.Status, result.ToolCalls, len(result.Evidence))
}
