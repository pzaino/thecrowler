//go:build integration

package agent

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestMCPLiveOllamaMutationProposalOptional proves a real Ollama model can ask
// CROWler to create an inert, approval-required MCP mutation proposal. The
// proposal tool must never invoke the mutating fixture.
func TestMCPLiveOllamaMutationProposalOptional(t *testing.T) {
	baseURL, model := ollamaSmokeConfig(t)
	manager, proposalTool, store, identity, counterPath, alias := newMCPMutationHarness(t)
	registry := NewAgentToolRegistry()
	if err := registry.RegisterMutation(proposalTool); err != nil {
		t.Fatalf("register live mutation proposal tool: %v", err)
	}
	provider := NewOllamaProvider([]string{model})
	loop := loopTestConfig(provider, registry)
	loop.ProviderName = "ollama"
	loop.Model = model
	loop.URL = baseURL
	loop.Allowlist = []string{alias}
	loop.Identity = identity
	loop.Enforcement = true
	loop.MutationAllowed = true
	loop.AllowedOperations = map[string]bool{alias: true}
	loop.MCPMutationAllowed = true
	loop.MCPAllowedOperations = map[string]bool{alias: true}
	loop.Principal = MCPMutationPrincipal{UserID: 7, Username: identity.Owner, Issuer: "live-test-issuer"}
	loop.MCPManager = manager
	loop.RunID = "mcp-mutation-live-run"
	loop.TraceID = "mcp-mutation-live-trace"
	loop.Prompt = fmt.Sprintf(`Call the approved MCP mutation proposal tool %s exactly once with {"resource":"fixture-resource"}. After the tool result, reply with: proposal recorded. Do not call any other tool.`, alias)
	result, err := RunToolLoop(loop)
	if err != nil {
		t.Fatalf("live mutation proposal tool loop: %v", err)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Name != alias || result.ToolCalls[0].Status != "executed" {
		t.Fatalf("live model did not create the proposal: %+v", result.ToolCalls)
	}
	proposals, err := store.CountByRun(nil, loop.RunID)
	if err != nil || proposals != 1 {
		t.Fatalf("live proposal count = %d, err=%v", proposals, err)
	}
	if data, err := os.ReadFile(counterPath); err == nil && strings.TrimSpace(string(data)) != "" {
		t.Fatalf("live mutation fixture was executed during proposal: %s", data)
	}
}
