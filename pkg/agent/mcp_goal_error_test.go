package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func mcpGoalSpecFor(t *testing.T, alias string, args map[string]any, stopOnToolError bool) *AgentGoalSpec {
	t.Helper()
	spec := &AgentGoalSpec{
		GoalID: "mcp-tool-outcome-goal", Objective: "require successful MCP evidence",
		Provider: "scripted", URL: "https://example.com/v1/chat", Model: "scripted",
		Prompt: "call the approved MCP tool", Allowlist: []string{alias},
		Evidence: EvidencePolicy{Allow: []EvidenceKind{EvidenceToolObservation}, DefaultMaxAge: 15 * time.Minute, MaxPayloadBytes: 4096},
		Budgets: GoalBudgets{MaxModelRounds: 3, MaxToolCalls: 3, MaxEvidenceRecords: 8, MaxEvidenceBytes: 16384,
			MaxTotalResultBytes: 32768, MaxElapsed: 20 * time.Second, MaxConsecutiveDenials: 3},
		Acceptance: AcceptanceSpec{Mode: AcceptanceAll, Criteria: []CriterionSpec{{
			ID: "successful-observation", Description: "the MCP tool must return a successful observation",
			Predicate: Predicate{Kind: PredicateEvidenceExists, Evidence: EvidenceToolObservation,
				Where: &EvidenceSelector{Tool: alias, ArgumentsSHA256: argumentsHash(args)}},
		}}},
		StopOnToolError: stopOnToolError,
	}
	spec.SpecHash = computeGoalSpecHash(spec)
	return spec
}

func TestMCPToolErrorCannotSatisfyGoalEvidence(t *testing.T) {
	previous := AgentsEngine
	AgentsEngine = NewJobEngine()
	t.Cleanup(func() { AgentsEngine = previous })

	server := mcpFixtureServerConfig(t, "normal")
	server.ApprovedTools = []cfg.MCPApprovedTool{
		{Name: "fail", ReadOnly: true},
		{Name: "echo", ReadOnly: true},
	}
	callLog := filepath.Join(t.TempDir(), "mcp-calls.log")
	server.Environment["CROWLER_MCP_FIXTURE_CALL_LOG"] = callLog
	config := mcpTestConfigFor(server)
	setMCPTestConfig(t, config)
	identity := mcpTestIdentity()
	failAlias := MCPToolAlias("fixture", "fail")
	echoAlias := MCPToolAlias("fixture", "echo")
	manager, err := NewMCPToolManager(context.Background(), config, identity, []string{failAlias, echoAlias}, nil)
	if err != nil {
		t.Fatalf("new MCP manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	registry := NewAgentToolRegistry()
	if err := manager.Register(registry); err != nil {
		t.Fatalf("register MCP tools: %v", err)
	}

	t.Run("tool error is not successful evidence", func(t *testing.T) {
		failArgs := map[string]any{}
		provider := &scriptedProvider{name: "scripted", responses: []any{
			chatWithCalls(stringCall("mcp-fail-call", failAlias, `{}`)),
			chatWithText("The failed tool succeeded; the goal is complete."),
		}}
		result, err := RunGoal(goalRunConfig{
			Spec: mcpGoalSpecFor(t, failAlias, failArgs, false), Identity: identity, Registry: registry,
			EvidenceRegistry: NewGoalEvidenceRegistry(), Provider: provider,
			RunID: "mcp-fail-goal-run", TraceID: "mcp-fail-goal-trace",
		})
		if err != nil {
			t.Fatalf("MCP error goal run: %v", err)
		}
		if result.Status == GoalCriteriaMet {
			t.Fatalf("failed MCP tool satisfied a successful evidence criterion: %+v", result)
		}
		if result.Status != GoalIncomplete {
			t.Fatalf("unexpected terminal status: %s (%+v)", result.Status, result)
		}
		if len(result.ToolCalls) != 1 {
			t.Fatalf("tool call summaries = %+v", result.ToolCalls)
		}
		call := result.ToolCalls[0]
		if call.Status != "failed" || call.ReasonCode != "mcp_tool_error" {
			t.Fatalf("MCP failure classification lost: %+v", call)
		}
		if len(result.Evidence) != 0 {
			t.Fatalf("failed MCP tool recorded successful evidence: %+v", result.Evidence)
		}
		callLog, readErr := os.ReadFile(server.Environment["CROWLER_MCP_FIXTURE_CALL_LOG"])
		if readErr != nil {
			t.Fatalf("read fixture tool call log: %v", readErr)
		}
		if !strings.Contains(string(callLog), "fail") {
			t.Fatalf("fixture tool call log does not show the failed call: %s", callLog)
		}
		foundMCPAudit := false
		foundGoalAudit := false
		for _, event := range AgentsEngine.AuditEvents() {
			if event.Action == failAlias && event.Outcome == auditOutcomeError && event.ReasonCode == "mcp_tool_error" {
				foundMCPAudit = true
			}
			if event.Action == "AgentGoalRun" && event.ReasonCode == "mcp_tool_error" {
				foundGoalAudit = true
			}
		}
		if !foundMCPAudit || !foundGoalAudit {
			t.Fatalf("audit did not distinguish the MCP tool error (mcp=%t goal=%t)", foundMCPAudit, foundGoalAudit)
		}
	})

	t.Run("successful tool still produces evidence", func(t *testing.T) {
		spec := mcpGoalSpecFor(t, echoAlias, map[string]any{"text": "goal-ok"}, false)
		provider := &scriptedProvider{name: "scripted", responses: []any{
			chatWithCalls(stringCall("mcp-ok-call", echoAlias, `{"text":"goal-ok"}`)),
		}}
		result, err := RunGoal(goalRunConfig{
			Spec: spec, Identity: identity, Registry: registry,
			EvidenceRegistry: NewGoalEvidenceRegistry(), Provider: provider,
			RunID: "mcp-ok-goal-run", TraceID: "mcp-ok-goal-trace",
		})
		if err != nil {
			t.Fatalf("successful MCP goal run: %v", err)
		}
		if result.Status != GoalCriteriaMet {
			t.Fatalf("successful MCP tool did not satisfy evidence: %+v", result)
		}
		if len(result.Evidence) != 1 || result.ToolCalls[0].Status != "executed" {
			t.Fatalf("successful MCP evidence lost: %+v", result)
		}
	})
}

func TestMCPToolErrorHonorsGoalStopOnToolError(t *testing.T) {
	server := mcpFixtureServerConfig(t, "normal", cfg.MCPApprovedTool{Name: "fail", ReadOnly: true})
	config := mcpTestConfigFor(server)
	setMCPTestConfig(t, config)
	identity := mcpTestIdentity()
	alias := MCPToolAlias("fixture", "fail")
	manager, err := NewMCPToolManager(context.Background(), config, identity, []string{alias}, nil)
	if err != nil {
		t.Fatalf("new MCP manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	registry := NewAgentToolRegistry()
	if err := manager.Register(registry); err != nil {
		t.Fatalf("register MCP tool: %v", err)
	}
	provider := &scriptedProvider{name: "scripted", responses: []any{
		chatWithCalls(stringCall("mcp-stop-call", alias, `{}`)),
	}}
	result, err := RunGoal(goalRunConfig{
		Spec: mcpGoalSpecFor(t, alias, map[string]any{}, true), Identity: identity, Registry: registry,
		EvidenceRegistry: NewGoalEvidenceRegistry(), Provider: provider,
		RunID: "mcp-stop-goal-run", TraceID: "mcp-stop-goal-trace",
	})
	if err != nil {
		t.Fatalf("MCP stop-on-tool-error goal run: %v", err)
	}
	if result.Status != GoalToolError {
		t.Fatalf("StopOnToolError status = %s, want %s", result.Status, GoalToolError)
	}
	if len(result.Evidence) != 0 {
		t.Fatalf("stopped MCP error recorded evidence: %+v", result.Evidence)
	}
}
