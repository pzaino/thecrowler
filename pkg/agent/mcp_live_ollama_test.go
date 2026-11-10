//go:build integration

package agent

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestMCPLiveOllamaToolLoopOptional proves a real model can call an approved
// read-only MCP tool and a native CROWler tool in the same bounded loop.
func TestMCPLiveOllamaToolLoopOptional(t *testing.T) {
	baseURL, model := ollamaSmokeConfig(t)
	db := openCrawlApprovalPostgresDB(t)
	ownerID, _ := setupCrawlApprovalPostgresUsers(t, db)
	sourceID := createCrawlApprovalPostgresSource(t, db, ownerID)
	ownerName := fmt.Sprintf("wp03b2b-owner-%d", ownerID)
	server := mcpFixtureServerConfig(t, "normal")
	config := mcpTestConfigFor(server)
	setMCPTestConfig(t, config)
	identity := mcpTestIdentity()
	identity.Owner = ownerName
	alias := MCPToolAlias("fixture", "echo")
	manager, err := NewMCPToolManager(context.Background(), config, identity, []string{alias, "get_source_status"}, []uint64{sourceID})
	if err != nil {
		t.Fatalf("new live MCP manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	registry, err := DefaultToolRegistry()
	if err != nil {
		t.Fatalf("native registry: %v", err)
	}
	if err := manager.Register(registry); err != nil {
		t.Fatalf("register live MCP tools: %v", err)
	}
	provider := NewOllamaProvider([]string{model})
	configLoop := loopTestConfig(provider, registry)
	configLoop.ProviderName = "ollama"
	configLoop.Model = model
	configLoop.URL = baseURL
	configLoop.Allowlist = []string{alias, "get_source_status"}
	configLoop.AllowedSources = []uint64{sourceID}
	configLoop.Identity = identity
	configLoop.DB = *db
	configLoop.MCPManager = manager
	configLoop.RunID = "mcp-live-run"
	configLoop.TraceID = "mcp-live-trace"
	configLoop.Prompt = fmt.Sprintf(`Call both tools, one after the other: first call %s with {"text":"live-mcp"}, then call get_source_status with {"source_id":%d}. After both tool results, reply with: done. Do not call any other tool.`, alias, sourceID)
	result, err := RunToolLoop(configLoop)
	if err != nil {
		t.Fatalf("live MCP tool loop: %v", err)
	}
	calls := map[string]string{}
	for _, call := range result.ToolCalls {
		calls[call.Name] = call.Status
	}
	if calls[alias] != "executed" || calls["get_source_status"] != "executed" {
		t.Fatalf("live model did not execute both approved tools: %+v", result.ToolCalls)
	}
	t.Logf("live MCP+native tool loop: %+v", result.ToolCalls)
}

// TestMCPLiveOllamaGoalOptional proves a real model's MCP call can satisfy only
// the deterministic acceptance criterion; model text alone is insufficient.
func TestMCPLiveOllamaGoalOptional(t *testing.T) {
	baseURL, model := ollamaSmokeConfig(t)
	server := mcpFixtureServerConfig(t, "normal")
	config := mcpTestConfigFor(server)
	setMCPTestConfig(t, config)
	identity := mcpTestIdentity()
	alias := MCPToolAlias("fixture", "echo")
	manager, err := NewMCPToolManager(context.Background(), config, identity, []string{alias}, nil)
	if err != nil {
		t.Fatalf("new live MCP goal manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	registry := NewAgentToolRegistry()
	if err := manager.Register(registry); err != nil {
		t.Fatalf("register live MCP goal tool: %v", err)
	}
	provider := NewOllamaProvider([]string{model})
	spec := &AgentGoalSpec{
		GoalID: "mcp-live-goal", Objective: "produce a trusted MCP observation",
		Provider: "ollama", URL: baseURL, Model: model, Prompt: `Call the approved MCP tool ` + alias + ` once with {"text":"goal-evidence"}. After the tool result, reply with: goal evidence recorded.`,
		Allowlist: []string{alias},
		Evidence:  EvidencePolicy{Allow: []EvidenceKind{EvidenceToolObservation}, DefaultMaxAge: 15 * time.Minute, MaxPayloadBytes: 4096},
		Budgets: GoalBudgets{MaxModelRounds: 3, MaxToolCalls: 2, MaxEvidenceRecords: 4, MaxEvidenceBytes: 8192,
			MaxTotalResultBytes: 16384, MaxElapsed: 25 * time.Second, MaxConsecutiveDenials: 2},
		Acceptance: AcceptanceSpec{Mode: AcceptanceAll, Criteria: []CriterionSpec{{
			ID: "mcp-observation", Description: "trusted MCP observation exists",
			Predicate: Predicate{Kind: PredicateEvidenceExists, Evidence: EvidenceToolObservation,
				Where: &EvidenceSelector{Tool: alias, ArgumentsSHA256: argumentsHash(map[string]any{"text": "goal-evidence"})}},
		}}},
	}
	spec.SpecHash = computeGoalSpecHash(spec)
	result, err := RunGoal(goalRunConfig{
		Spec: spec, Identity: identity, Registry: registry,
		EvidenceRegistry: NewGoalEvidenceRegistry(), Provider: provider,
		RunID: "mcp-live-goal-run", TraceID: "mcp-live-goal-trace", MCPManager: manager,
	})
	if err != nil {
		t.Fatalf("live MCP goal run: %v", err)
	}
	if result.Status != GoalCriteriaMet {
		t.Fatalf("live MCP goal terminal = %s, want criteria_met: %+v", result.Status, result)
	}
	t.Logf("live MCP goal: status=%s model_rounds=%d tool_calls=%d evidence=%d", result.Status, result.ModelRounds, result.ToolCallCount, len(result.Evidence))
}
