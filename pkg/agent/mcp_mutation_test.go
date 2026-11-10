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

func mcpMutationServerConfig(t *testing.T, counterPath string, tool cfg.MCPApprovedTool) cfg.MCPServerConfig {
	t.Helper()
	if tool.Name == "" {
		tool = cfg.MCPApprovedTool{
			Name: "mutate_counter", Effect: "mutation", AllowMutationProposal: true,
			Description:          "Increment an isolated fixture counter.",
			RequiredCapabilities: []string{"tool_execution"},
		}
	}
	server := mcpFixtureServerConfig(t, "normal", tool)
	server.Environment["CROWLER_MCP_MUTATION_COUNTER"] = counterPath
	return server
}

func mcpMutationTestPolicy(alias string) MCPMutationPolicy {
	return MCPMutationPolicy{
		Enabled: true, PolicyID: "mcp-mutation-test", Version: "1",
		Operations: []MCPMutationOperationPolicy{{
			ServerID: "fixture", ToolName: "mutate_counter", ToolAlias: alias,
			AllowedAgents:          []string{"mcp-test-agent"},
			RequiredCapabilities:   []string{"tool_execution"},
			MinTrustLevel:          "trusted",
			AllowedResources:       []string{"fixture-resource"},
			RequiredArgumentFields: []string{"resource"},
			AllowedArgumentFields:  []string{"resource", "source_id"},
			ProposalTTL:            5 * time.Minute,
			MaxProposalsPerRun:     4, MaxPendingProposals: 8,
			MaxInputBytes: 4096, MaxOutputBytes: 4096,
			RequireApproval: true, ExecutionBackend: "none",
			AllowedTransports: []string{mcpTransportStdio},
			AuditRequired:     true,
		}},
	}
}

func mcpMutationRuntime(identity AgentIdentity, alias, runID, callID string) context.Context {
	return ContextWithToolRuntime(context.Background(), ToolRuntime{
		Auth: ToolAuthContext{
			Identity: identity, Enforcement: true,
			Allowlist:         map[string]bool{alias: true},
			AllowedOperations: map[string]bool{alias: true},
			MutationAllowed:   true, RunID: runID, TraceID: "mutation-trace",
		},
		CallID:    callID,
		Principal: MCPMutationPrincipal{UserID: 7, Username: identity.Owner, Issuer: "test-issuer"},
	})
}

func newMCPMutationHarness(t *testing.T) (*MCPToolManager, *MCPMutationProposalTool, *InMemoryMCPMutationProposalStore, AgentIdentity, string, string) {
	t.Helper()
	counterPath := filepath.Join(t.TempDir(), "mutation-counter.log")
	server := mcpMutationServerConfig(t, counterPath, cfg.MCPApprovedTool{})
	config := mcpTestConfigFor(server)
	setMCPTestConfig(t, config)
	identity := mcpTestIdentity()
	alias := MCPToolAlias("fixture", "mutate_counter")
	manager, err := NewMCPToolManager(context.Background(), config, identity, []string{alias}, nil)
	if err != nil {
		t.Fatalf("new mutation manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	if _, ok := manager.MutationTool(alias); !ok {
		t.Fatalf("mutation descriptor was not frozen")
	}
	store := NewInMemoryMCPMutationProposalStore()
	tool, err := NewMCPMutationProposalTool(manager, mcpMutationTestPolicy(alias), store)
	if err != nil {
		t.Fatalf("new mutation proposal tool: %v", err)
	}
	return manager, tool, store, identity, counterPath, alias
}

func TestMCPMutationClassificationAndProposalIsInert(t *testing.T) {
	manager, tool, store, identity, counterPath, alias := newMCPMutationHarness(t)
	_ = manager
	if _, ok := CurrentMCPApprovedMutationTool(alias); !ok {
		t.Fatalf("mutation lookup failed")
	}
	if _, ok := CurrentMCPApprovedTool(alias); ok {
		t.Fatalf("mutation tool was exposed as read-only")
	}
	registry := NewAgentToolRegistry()
	if err := registry.Register(tool); err == nil {
		t.Fatalf("mutation proposal tool was accepted by read-only Register")
	}
	if err := registry.RegisterMutation(tool); err != nil {
		t.Fatalf("mutation proposal tool rejected by RegisterMutation: %v", err)
	}
	ctx := mcpMutationRuntime(identity, alias, "mutation-run", "mutation-call")
	result, err := tool.Execute(ctx, map[string]any{"resource": "fixture-resource"})
	if err != nil {
		t.Fatalf("proposal execution: %v", err)
	}
	if result["status"] != string(MCPProposalApprovalRequired) || result["proposal_id"] == "" {
		t.Fatalf("unexpected proposal response: %+v", result)
	}
	proposalID, _ := result["proposal_id"].(string)
	proposal, ok, err := store.Get(context.Background(), proposalID)
	if err != nil || !ok {
		t.Fatalf("proposal store read: %v %v", ok, err)
	}
	if proposal.State != MCPProposalApprovalRequired || !proposal.ApprovalRequired ||
		proposal.ServerID != "fixture" || proposal.RawToolName != "mutate_counter" ||
		proposal.ToolAlias != alias || proposal.ArgumentsSHA256 == "" ||
		proposal.ProposalSHA256 == "" || proposal.PolicySHA256 == "" {
		t.Fatalf("proposal identity incomplete: %+v", proposal)
	}
	if data, err := os.ReadFile(counterPath); err == nil && len(strings.TrimSpace(string(data))) > 0 {
		t.Fatalf("inert proposal invoked the mutating fixture: %s", data)
	}
}

func TestMCPMutationProposalFingerprintDeterminismAndReplay(t *testing.T) {
	_, tool, store, identity, _, alias := newMCPMutationHarness(t)
	ctx := mcpMutationRuntime(identity, alias, "mutation-run", "mutation-call")
	first, err := tool.Execute(ctx, map[string]any{"resource": "fixture-resource"})
	if err != nil {
		t.Fatalf("first proposal: %v", err)
	}
	second, err := tool.Execute(ctx, map[string]any{"resource": "fixture-resource"})
	if err != nil {
		t.Fatalf("replayed proposal: %v", err)
	}
	if first["proposal_id"] != second["proposal_id"] || first["proposal_sha256"] != second["proposal_sha256"] {
		t.Fatalf("identical logical proposals were not stable: %+v %+v", first, second)
	}
	changed, err := tool.Execute(ctx, map[string]any{"resource": "other-resource"})
	if err == nil {
		t.Fatalf("out-of-scope resource was accepted: %+v", changed)
	}
	changedRun := mcpMutationRuntime(identity, alias, "other-run", "mutation-call")
	third, err := tool.Execute(changedRun, map[string]any{"resource": "fixture-resource"})
	if err != nil {
		t.Fatalf("changed-run proposal: %v", err)
	}
	if third["proposal_sha256"] == first["proposal_sha256"] {
		t.Fatalf("proposal fingerprint ignored run identity")
	}
	if len(store.byID) != 2 {
		t.Fatalf("proposal store count = %d, want 2", len(store.byID))
	}
}

func TestMCPMutationProposalAuthorizationDenials(t *testing.T) {
	_, tool, _, identity, _, alias := newMCPMutationHarness(t)
	cases := map[string]context.Context{
		"missing operation allowlist": ContextWithToolRuntime(context.Background(), ToolRuntime{
			Auth: ToolAuthContext{Identity: identity, Enforcement: true, Allowlist: map[string]bool{alias: true},
				MutationAllowed: true, RunID: "run"}, CallID: "call",
			Principal: MCPMutationPrincipal{UserID: 7, Username: identity.Owner, Issuer: "test-issuer"},
		}),
		"missing capability": func() context.Context {
			weak := identity
			weak.Capabilities = []string{"db_read"}
			return mcpMutationRuntime(weak, alias, "run", "call")
		}(),
		"forbidden contract": func() context.Context {
			forbidden := identity
			forbidden.Contract = &AgentContract{ForbiddenActions: []string{"tool:" + alias}}
			return mcpMutationRuntime(forbidden, alias, "run", "call")
		}(),
		"missing effective call ID": ContextWithToolRuntime(context.Background(), ToolRuntime{
			Auth: ToolAuthContext{Identity: identity, Enforcement: true, Allowlist: map[string]bool{alias: true},
				AllowedOperations: map[string]bool{alias: true}, MutationAllowed: true, RunID: "run"},
			Principal: MCPMutationPrincipal{UserID: 7, Username: identity.Owner, Issuer: "test-issuer"},
		}),
	}
	for name, ctx := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := tool.Execute(ctx, map[string]any{"resource": "fixture-resource"}); err == nil {
				t.Fatalf("%s was accepted", name)
			}
		})
	}
	if _, err := tool.Execute(mcpMutationRuntime(identity, alias, "run", "call"), map[string]any{}); err == nil {
		t.Fatalf("missing required mutation argument was accepted")
	}
	if _, err := tool.Execute(mcpMutationRuntime(identity, alias, "run", "call"), map[string]any{"unknown": "x"}); err == nil {
		t.Fatalf("unexpected mutation argument was accepted")
	}
}

func TestMCPMutationApprovalContract(t *testing.T) {
	_, tool, store, identity, _, alias := newMCPMutationHarness(t)
	ctx := mcpMutationRuntime(identity, alias, "run", "call")
	result, err := tool.Execute(ctx, map[string]any{"resource": "fixture-resource"})
	if err != nil {
		t.Fatalf("proposal: %v", err)
	}
	proposalID := result["proposal_id"].(string)
	proposal, ok, _ := store.Get(context.Background(), proposalID)
	if !ok {
		t.Fatalf("proposal missing")
	}
	approvalStore := NewInMemoryMCPMutationApprovalStore()
	approver := MCPMutationPrincipal{UserID: 99, Username: "operator", Issuer: "test-issuer"}
	approval := MCPMutationApproval{
		ApprovalID: "approval-1", ProposalSHA256: proposal.ProposalSHA256,
		RunID: proposal.RunID, AgentID: proposal.AgentID, ServerID: proposal.ServerID,
		ToolAlias: proposal.ToolAlias, ArgumentsSHA256: proposal.ArgumentsSHA256,
		PolicySHA256: proposal.PolicySHA256, ApprovedByUserID: approver.UserID,
		ApprovedBy: approver.Username, Issuer: approver.Issuer,
		ApprovedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour), MaxExecutions: 1,
	}
	if err := approvalStore.Put(context.Background(), approval); err != nil {
		t.Fatalf("approval store: %v", err)
	}
	verifier := MCPMutationApprovalVerifier{Store: approvalStore}
	if _, err := verifier.Verify(context.Background(), proposal, approval.ApprovalID, approver); err != nil {
		t.Fatalf("matching approval rejected: %v", err)
	}
	forged := approval
	forged.ArgumentsSHA256 = "forged"
	_ = approvalStore.Put(context.Background(), forged)
	if _, err := verifier.Verify(context.Background(), proposal, forged.ApprovalID, approver); err == nil {
		t.Fatalf("forged approval accepted")
	}
	revoked := approval
	revoked.ApprovalID = "approval-revoked"
	now := time.Now().UTC()
	revoked.RevokedAt = &now
	_ = approvalStore.Put(context.Background(), revoked)
	if _, err := verifier.Verify(context.Background(), proposal, revoked.ApprovalID, approver); err == nil {
		t.Fatalf("revoked approval accepted")
	}
	expired := approval
	expired.ApprovalID = "approval-expired"
	expired.ExpiresAt = time.Now().UTC().Add(-time.Second)
	_ = approvalStore.Put(context.Background(), expired)
	if _, err := verifier.Verify(context.Background(), proposal, expired.ApprovalID, approver); err == nil {
		t.Fatalf("expired approval accepted")
	}
	wrongApprover := MCPMutationPrincipal{UserID: 100, Username: "other", Issuer: "test-issuer"}
	if _, err := verifier.Verify(context.Background(), proposal, approval.ApprovalID, wrongApprover); err == nil {
		t.Fatalf("wrong approver accepted")
	}
}

func TestMCPMutationPolicyFailsClosed(t *testing.T) {
	alias := MCPToolAlias("fixture", "mutate_counter")
	policy := mcpMutationTestPolicy(alias)
	if err := policy.Validate(); err != nil {
		t.Fatalf("valid mutation policy rejected: %v", err)
	}
	clonePolicy := func() MCPMutationPolicy {
		out := policy
		out.Operations = append([]MCPMutationOperationPolicy(nil), policy.Operations...)
		return out
	}
	bad := clonePolicy()
	bad.Operations[0].ExecutionBackend = "remote"
	if err := bad.Validate(); err == nil {
		t.Fatalf("unsupported execution backend accepted")
	}
	bad = clonePolicy()
	bad.Operations[0].AllowedResources = nil
	bad.Operations[0].AllowedSources = nil
	if err := bad.Validate(); err == nil {
		t.Fatalf("mutation policy without exact scope accepted")
	}
	bad = clonePolicy()
	bad.Operations[0].RequireApproval = false
	if err := bad.Validate(); err == nil {
		t.Fatalf("mutation policy without approval accepted")
	}
}

func TestMCPMutationSourceScopeIsEnforced(t *testing.T) {
	counterPath := filepath.Join(t.TempDir(), "mutation-counter.log")
	toolConfig := cfg.MCPApprovedTool{
		Name: "mutate_source_counter", Effect: "mutation", AllowMutationProposal: true,
		Description:    "Source-scoped fixture mutation.",
		AllowedSources: []uint64{42}, RequiredCapabilities: []string{"tool_execution"},
	}
	server := mcpMutationServerConfig(t, counterPath, toolConfig)
	config := mcpTestConfigFor(server)
	setMCPTestConfig(t, config)
	identity := mcpTestIdentity()
	alias := MCPToolAlias("fixture", "mutate_source_counter")
	manager, err := NewMCPToolManager(context.Background(), config, identity, []string{alias}, []uint64{42})
	if err != nil {
		t.Fatalf("new source mutation manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	policy := mcpMutationTestPolicy(alias)
	policy.Operations[0].ToolName = "mutate_source_counter"
	policy.Operations[0].AllowedResources = nil
	policy.Operations[0].AllowedSources = []uint64{42}
	policy.Operations[0].RequiredArgumentFields = []string{"source_id"}
	policy.Operations[0].AllowedArgumentFields = []string{"source_id"}
	store := NewInMemoryMCPMutationProposalStore()
	proposalTool, err := NewMCPMutationProposalTool(manager, policy, store)
	if err != nil {
		t.Fatalf("new source proposal tool: %v", err)
	}
	ctx := ContextWithToolRuntime(context.Background(), ToolRuntime{
		Auth: ToolAuthContext{Identity: identity, Enforcement: true,
			Allowlist: map[string]bool{alias: true}, AllowedOperations: map[string]bool{alias: true},
			AllowedSources: map[uint64]bool{42: true}, MutationAllowed: true, RunID: "source-run"},
		CallID: "source-call", Principal: MCPMutationPrincipal{UserID: 7, Username: identity.Owner, Issuer: "test-issuer"},
	})
	if _, err := proposalTool.Execute(ctx, map[string]any{"source_id": float64(42)}); err != nil {
		t.Fatalf("in-scope source mutation proposal: %v", err)
	}
	if _, err := proposalTool.Execute(ctx, map[string]any{"source_id": float64(43)}); err == nil {
		t.Fatalf("out-of-scope source mutation proposal accepted")
	}
	if data, err := os.ReadFile(counterPath); err == nil && strings.TrimSpace(string(data)) != "" {
		t.Fatalf("source mutation fixture executed during proposal: %s", data)
	}
}

func TestMCPMutationProposalThroughToolLoopAndGoalStaysInert(t *testing.T) {
	manager, proposalTool, store, identity, counterPath, alias := newMCPMutationHarness(t)
	registry := NewAgentToolRegistry()
	if err := registry.RegisterMutation(proposalTool); err != nil {
		t.Fatalf("register mutation proposal: %v", err)
	}
	principal := MCPMutationPrincipal{UserID: 7, Username: identity.Owner, Issuer: "test-issuer"}

	t.Run("tool loop proposal only", func(t *testing.T) {
		provider := &scriptedProvider{name: "scripted", responses: []any{
			chatWithCalls(stringCall("mutation-call", alias, `{"resource":"fixture-resource"}`)),
			chatWithText("proposal recorded"),
		}}
		loop := loopTestConfig(provider, registry)
		loop.Allowlist = []string{alias}
		loop.Identity = identity
		loop.Enforcement = true
		loop.MutationAllowed = true
		loop.AllowedOperations = map[string]bool{alias: true}
		loop.Principal = principal
		loop.MCPManager = manager
		loop.RunID = "mutation-loop-run"
		loop.TraceID = "mutation-loop-trace"
		result, err := RunToolLoop(loop)
		if err != nil {
			t.Fatalf("mutation tool loop: %v", err)
		}
		if len(result.ToolCalls) != 1 || result.ToolCalls[0].Status != "executed" {
			t.Fatalf("proposal call summary: %+v", result.ToolCalls)
		}
		if data, err := os.ReadFile(counterPath); err == nil && strings.TrimSpace(string(data)) != "" {
			t.Fatalf("tool-loop proposal executed mutation: %s", data)
		}
	})

	t.Run("goal cannot treat proposal as success evidence", func(t *testing.T) {
		spec := mcpGoalSpecFor(t, alias, map[string]any{"resource": "fixture-resource"}, false)
		provider := &scriptedProvider{name: "scripted", responses: []any{
			chatWithCalls(stringCall("mutation-goal-call", alias, `{"resource":"fixture-resource"}`)),
			chatWithText("mutation complete"),
		}}
		result, err := RunGoal(goalRunConfig{
			Spec: spec, Identity: identity, Registry: registry,
			EvidenceRegistry: NewGoalEvidenceRegistry(), Provider: provider,
			RunID: "mutation-goal-run", TraceID: "mutation-goal-trace", Principal: principal,
			MutationAllowed: true, AllowedOperations: map[string]bool{alias: true},
		})
		if err != nil {
			t.Fatalf("mutation goal: %v", err)
		}
		if result.Status == GoalCriteriaMet || len(result.Evidence) != 0 {
			t.Fatalf("mutation proposal satisfied successful evidence: %+v", result)
		}
		if data, err := os.ReadFile(counterPath); err == nil && strings.TrimSpace(string(data)) != "" {
			t.Fatalf("goal proposal executed mutation: %s", data)
		}
		_ = store
	})
}

func TestMCPMutationProposalLimitsAndConcurrentReplay(t *testing.T) {
	_, tool, store, identity, _, alias := newMCPMutationHarness(t)
	policy := mcpMutationTestPolicy(alias)
	policy.Operations[0].MaxProposalsPerRun = 1
	policy.Operations[0].MaxPendingProposals = 2
	// Replace the tool with a policy-limited proposer sharing the same store.
	limited, err := NewMCPMutationProposalTool(tool.descriptor.manager, policy, store)
	if err != nil {
		t.Fatalf("new limited proposal tool: %v", err)
	}
	ctx1 := mcpMutationRuntime(identity, alias, "limit-run", "call-1")
	if _, err := limited.Execute(ctx1, map[string]any{"resource": "fixture-resource"}); err != nil {
		t.Fatalf("first limited proposal: %v", err)
	}
	if _, err := limited.Execute(ctx1, map[string]any{"resource": "fixture-resource"}); err != nil {
		t.Fatalf("replay while under limit: %v", err)
	}
	ctx2 := mcpMutationRuntime(identity, alias, "limit-run", "call-2")
	if _, err := limited.Execute(ctx2, map[string]any{"resource": "fixture-resource"}); err == nil {
		t.Fatalf("per-run proposal limit not enforced")
	}

	concurrentStore := NewInMemoryMCPMutationProposalStore()
	concurrent, err := NewMCPMutationProposalTool(tool.descriptor.manager, mcpMutationTestPolicy(alias), concurrentStore)
	if err != nil {
		t.Fatalf("new concurrent proposal tool: %v", err)
	}
	results := make(chan any, 2)
	for i := 0; i < 2; i++ {
		go func() {
			result, err := concurrent.Execute(mcpMutationRuntime(identity, alias, "concurrent-run", "same-call"),
				map[string]any{"resource": "fixture-resource"})
			if err != nil {
				results <- err
				return
			}
			results <- result["proposal_id"]
		}()
	}
	first := <-results
	second := <-results
	if first != second {
		t.Fatalf("concurrent identical proposals produced different identities: %v %v", first, second)
	}
	count, err := concurrentStore.CountByRun(context.Background(), "concurrent-run")
	if err != nil || count != 1 {
		t.Fatalf("concurrent proposal count = %d, err=%v", count, err)
	}
}

func TestMCPMutationProposalAdditionalDenialsAndAudit(t *testing.T) {
	previous := AgentsEngine
	AgentsEngine = NewJobEngine()
	t.Cleanup(func() { AgentsEngine = previous })
	_, tool, _, identity, _, alias := newMCPMutationHarness(t)

	t.Run("wrong agent", func(t *testing.T) {
		other := identity
		other.AgentID = "other-agent"
		ctx := mcpMutationRuntime(other, alias, "run", "call")
		if _, err := tool.Execute(ctx, map[string]any{"resource": "fixture-resource"}); err == nil {
			t.Fatalf("wrong agent was accepted")
		}
	})
	t.Run("missing principal", func(t *testing.T) {
		ctx := ContextWithToolRuntime(context.Background(), ToolRuntime{
			Auth: ToolAuthContext{Identity: identity, Enforcement: true,
				Allowlist: map[string]bool{alias: true}, AllowedOperations: map[string]bool{alias: true},
				MutationAllowed: true, RunID: "run"},
			CallID: "call",
		})
		if _, err := tool.Execute(ctx, map[string]any{"resource": "fixture-resource"}); err == nil {
			t.Fatalf("missing principal was accepted")
		}
	})
	t.Run("oversized arguments", func(t *testing.T) {
		ctx := mcpMutationRuntime(identity, alias, "run", "call")
		if _, err := tool.Execute(ctx, map[string]any{"resource": "fixture-resource", "extra": strings.Repeat("x", 8192)}); err == nil {
			t.Fatalf("oversized mutation arguments were accepted")
		}
	})
	t.Run("missing store", func(t *testing.T) {
		if _, err := NewMCPMutationProposalTool(tool.descriptor.manager, mcpMutationTestPolicy(alias), nil); err == nil {
			t.Fatalf("proposal tool accepted a nil store")
		}
	})
	t.Run("audit provenance", func(t *testing.T) {
		ctx := mcpMutationRuntime(identity, alias, "audit-run", "audit-call")
		if _, err := tool.Execute(ctx, map[string]any{"resource": "fixture-resource"}); err != nil {
			t.Fatalf("audited proposal: %v", err)
		}
		found := false
		for _, event := range AgentsEngine.AuditEvents() {
			if event.RunID == "audit-run" && event.MCPProposalID != "" &&
				event.MCPProposalSHA256 != "" && event.MCPPolicyID == "mcp-mutation-test" {
				found = true
			}
		}
		if !found {
			t.Fatalf("mutation proposal audit provenance missing: %+v", AgentsEngine.AuditEvents())
		}
	})
}

func TestMCPMutationFingerprintBindsPrincipal(t *testing.T) {
	_, tool, store, identity, _, alias := newMCPMutationHarness(t)
	base := mcpMutationRuntime(identity, alias, "principal-run", "principal-call")
	first, err := tool.Execute(base, map[string]any{"resource": "fixture-resource"})
	if err != nil {
		t.Fatalf("first principal proposal: %v", err)
	}
	changed := ContextWithToolRuntime(context.Background(), ToolRuntime{
		Auth: ToolAuthContext{Identity: identity, Enforcement: true,
			Allowlist: map[string]bool{alias: true}, AllowedOperations: map[string]bool{alias: true},
			MutationAllowed: true, RunID: "principal-run", TraceID: "trace"},
		CallID: "principal-call", Principal: MCPMutationPrincipal{UserID: 8, Username: identity.Owner, Issuer: "test-issuer"},
	})
	second, err := tool.Execute(changed, map[string]any{"resource": "fixture-resource"})
	if err != nil {
		t.Fatalf("changed principal proposal: %v", err)
	}
	if first["proposal_sha256"] == second["proposal_sha256"] {
		t.Fatalf("proposal fingerprint ignored principal identity")
	}
	if count, _ := store.CountByRun(context.Background(), "principal-run"); count != 2 {
		t.Fatalf("principal-bound proposal count = %d", count)
	}
}
