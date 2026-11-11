package agent

import (
	"context"
	"encoding/json"
	"errors"
	"math"
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
			AllowedArgumentFields:  []string{"resource", "source_id", "amount"},
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
	expiredProposal := proposal
	expiredProposal.ExpiresAt = time.Now().UTC().Add(-time.Second)
	if _, err := verifier.Verify(context.Background(), expiredProposal, approval.ApprovalID, approver); err == nil {
		t.Fatalf("approval for an expired proposal accepted")
	}
	futureApproval := approval
	futureApproval.ApprovalID = "approval-future"
	futureApproval.ApprovedAt = time.Now().UTC().Add(time.Hour)
	futureApproval.ExpiresAt = futureApproval.ApprovedAt.Add(time.Hour)
	_ = approvalStore.Put(context.Background(), futureApproval)
	if _, err := verifier.Verify(context.Background(), proposal, futureApproval.ApprovalID, approver); err == nil {
		t.Fatalf("future-dated approval accepted")
	}
	earlyApproval := approval
	earlyApproval.ApprovalID = "approval-early"
	earlyApproval.ApprovedAt = proposal.CreatedAt.Add(-time.Second)
	_ = approvalStore.Put(context.Background(), earlyApproval)
	if _, err := verifier.Verify(context.Background(), proposal, earlyApproval.ApprovalID, approver); err == nil {
		t.Fatalf("approval predating the proposal accepted")
	}
	changedPolicy := approval
	changedPolicy.ApprovalID = "approval-policy"
	changedPolicy.PolicySHA256 = "changed-policy"
	_ = approvalStore.Put(context.Background(), changedPolicy)
	if _, err := verifier.Verify(context.Background(), proposal, changedPolicy.ApprovalID, approver); err == nil {
		t.Fatalf("approval bound to a changed policy accepted")
	}
	changedScope := proposal
	changedScope.SourceIDs = []uint64{99}
	if _, err := verifier.Verify(context.Background(), changedScope, approval.ApprovalID, approver); err == nil {
		t.Fatalf("approval reused for a changed proposal scope")
	}
	exhausted := approval
	exhausted.ApprovalID = "approval-exhausted"
	exhausted.ExecutionsStarted = exhausted.MaxExecutions
	_ = approvalStore.Put(context.Background(), exhausted)
	if _, err := verifier.Verify(context.Background(), proposal, exhausted.ApprovalID, approver); err == nil {
		t.Fatalf("exhausted approval accepted")
	}
	revokedState := approval
	revokedState.ApprovalID = "approval-revoked-state"
	revokedState.State = "revoked"
	_ = approvalStore.Put(context.Background(), revokedState)
	if _, err := verifier.Verify(context.Background(), proposal, revokedState.ApprovalID, approver); err == nil {
		t.Fatalf("revoked-state approval accepted")
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
		CallID: "principal-call-2", Principal: MCPMutationPrincipal{UserID: 8, Username: identity.Owner, Issuer: "test-issuer"},
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

func TestMCPMutationCanonicalJSONNumbersRemainNumbers(t *testing.T) {
	type testCase struct {
		name  string
		left  map[string]any
		right map[string]any
		same  bool
	}
	cases := []testCase{
		{"number vs numeric string", map[string]any{"x": 1.25}, map[string]any{"x": "1.25"}, false},
		{"integer vs numeric string", map[string]any{"x": 1}, map[string]any{"x": "1"}, false},
		{"decimal and integer equivalence", map[string]any{"x": json.Number("1.0")}, map[string]any{"x": json.Number("1")}, true},
		{"exponent and integer equivalence", map[string]any{"x": json.Number("1e0")}, map[string]any{"x": json.Number("1")}, true},
		{"exponent decimal equivalence", map[string]any{"x": json.Number("1e-3")}, map[string]any{"x": json.Number("0.001")}, true},
		{"scaled exponent equivalence", map[string]any{"x": json.Number("1000e-3")}, map[string]any{"x": json.Number("1")}, true},
		{"trailing decimal zeros", map[string]any{"x": json.Number("1.250")}, map[string]any{"x": json.Number("1.25")}, true},
		{"scaled decimal equivalence", map[string]any{"x": json.Number("125e-2")}, map[string]any{"x": json.Number("1.25")}, true},
		{"positive exponent", map[string]any{"x": json.Number("1e2")}, map[string]any{"x": json.Number("100")}, true},
		{"decimal exponent to integer", map[string]any{"x": json.Number("1.2e1")}, map[string]any{"x": json.Number("12")}, true},
		{"large integer exactness", map[string]any{"x": json.Number("9007199254740993")}, map[string]any{"x": json.Number("9007199254740993.0")}, true},
		{"negative zero", map[string]any{"x": json.Number("-0")}, map[string]any{"x": json.Number("0.0")}, true},
		{"nested numeric/string distinction", map[string]any{"a": []any{map[string]any{"x": json.Number("2.5")}}}, map[string]any{"a": []any{map[string]any{"x": "2.5"}}}, false},
		{"unicode preserved", map[string]any{"x": "café"}, map[string]any{"x": "café"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			left, _, err := canonicalMCPArguments(tc.left, 4096)
			if err != nil {
				t.Fatalf("left canonicalization: %v", err)
			}
			right, _, err := canonicalMCPArguments(tc.right, 4096)
			if err != nil {
				t.Fatalf("right canonicalization: %v", err)
			}
			if same := string(left) == string(right); same != tc.same {
				t.Fatalf("canonical equality = %t, want %t: %s vs %s", same, tc.same, left, right)
			}
			if !tc.same && sha256Hex(left) == sha256Hex(right) {
				t.Fatalf("distinct canonical values collided")
			}
		})
	}
}

func TestMCPMutationCanonicalNumberRejections(t *testing.T) {
	bad := []any{
		math.NaN(), math.Inf(1), math.Inf(-1),
		json.Number("01"), json.Number("1."), json.Number(".1"),
		json.Number("1e99999"), json.Number("1e-99999"),
		json.Number("1e999999999999999999999"),
		string([]byte{0xff, 0xfe}),
		map[string]any{string([]byte{0xff}): "value"},
	}
	for _, value := range bad {
		if _, _, err := canonicalMCPArguments(map[string]any{"x": value}, 4096); err == nil {
			t.Errorf("invalid canonical value %T %v was accepted", value, value)
		}
	}
	if _, _, err := canonicalMCPArguments(map[string]any{"x": 9007199254740993.0}, 4096); err == nil {
		t.Fatalf("inexact large float64 was accepted")
	}
}

func TestMCPMutationProposalUsesCanonicalArguments(t *testing.T) {
	_, tool, store, identity, _, alias := newMCPMutationHarness(t)
	args := map[string]any{"resource": "fixture-resource", "amount": json.Number("1.25")}
	result, err := tool.Execute(mcpMutationRuntime(identity, alias, "canonical-run", "canonical-call"), args)
	if err != nil {
		t.Fatalf("canonical proposal: %v", err)
	}
	proposal, ok, err := store.Get(context.Background(), result["proposal_id"].(string))
	if err != nil || !ok {
		t.Fatalf("canonical proposal lookup: %v %v", ok, err)
	}
	encodedArgs, err := json.Marshal(proposal.Arguments)
	if err != nil {
		t.Fatalf("marshal proposal arguments: %v", err)
	}
	if string(encodedArgs) != string(proposal.CanonicalArguments) {
		t.Fatalf("proposal Arguments and CanonicalArguments differ: %s vs %s", encodedArgs, proposal.CanonicalArguments)
	}
	if proposal.ArgumentsSHA256 != sha256Hex(proposal.CanonicalArguments) {
		t.Fatalf("argument fingerprint is not the canonical bytes hash")
	}
	if _, ok := proposal.Arguments["amount"].(json.Number); !ok {
		t.Fatalf("canonical number lost its JSON number type: %T", proposal.Arguments["amount"])
	}
	// Mutating the caller's map must not alter the immutable proposal.
	args["resource"] = "changed-after-admission"
	if proposal.Arguments["resource"] != "fixture-resource" {
		t.Fatalf("proposal arguments were mutable after admission")
	}
}

func TestMCPMutationSameLogicalCallChangedArgumentsConflicts(t *testing.T) {
	_, tool, store, identity, _, alias := newMCPMutationHarness(t)
	ctx := mcpMutationRuntime(identity, alias, "conflict-run", "conflict-call")
	first, err := tool.Execute(ctx, map[string]any{"resource": "fixture-resource", "amount": float64(1)})
	if err != nil {
		t.Fatalf("first logical call: %v", err)
	}
	second, err := tool.Execute(ctx, map[string]any{"resource": "fixture-resource", "amount": float64(2)})
	if err == nil {
		t.Fatalf("changed arguments for the same run/call were accepted: %+v", second)
	}
	if !errors.Is(err, ErrMCPMutationProposalConflict) {
		t.Fatalf("changed argument error = %v, want conflict", err)
	}
	if count, _ := store.CountByRun(context.Background(), "conflict-run"); count != 1 {
		t.Fatalf("conflicting proposal count = %d", count)
	}
	if first["proposal_id"] == "" {
		t.Fatalf("first proposal identity lost")
	}
}

func TestMCPMutationAtomicQuotaUnderConcurrentDistinctCalls(t *testing.T) {
	_, tool, store, identity, _, alias := newMCPMutationHarness(t)
	policy := mcpMutationTestPolicy(alias)
	policy.Operations[0].MaxProposalsPerRun = 1
	policy.Operations[0].MaxPendingProposals = 2
	limited, err := NewMCPMutationProposalTool(tool.descriptor.manager, policy, store)
	if err != nil {
		t.Fatalf("limited proposal tool: %v", err)
	}
	results := make(chan error, 2)
	go func() {
		_, err := limited.Execute(mcpMutationRuntime(identity, alias, "quota-run", "quota-call-1"),
			map[string]any{"resource": "fixture-resource", "amount": float64(1)})
		results <- err
	}()
	go func() {
		_, err := limited.Execute(mcpMutationRuntime(identity, alias, "quota-run", "quota-call-2"),
			map[string]any{"resource": "fixture-resource", "amount": float64(2)})
		results <- err
	}()
	successes, quotaErrors := 0, 0
	for i := 0; i < 2; i++ {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrMCPMutationQuota):
			quotaErrors++
		default:
			t.Fatalf("unexpected quota error: %v", err)
		}
	}
	if successes != 1 || quotaErrors != 1 {
		t.Fatalf("atomic quota results: success=%d quota=%d", successes, quotaErrors)
	}
}

func TestMCPMutationExpiredReplayDoesNotRefresh(t *testing.T) {
	_, tool, store, identity, _, alias := newMCPMutationHarness(t)
	now := time.Now().UTC()
	store.Now = func() time.Time { return now }
	ctx := mcpMutationRuntime(identity, alias, "expiry-run", "expiry-call")
	first, err := tool.Execute(ctx, map[string]any{"resource": "fixture-resource"})
	if err != nil {
		t.Fatalf("initial proposal: %v", err)
	}
	proposalID := first["proposal_id"].(string)
	before, ok, _ := store.Get(context.Background(), proposalID)
	if !ok {
		t.Fatalf("proposal missing before expiry")
	}
	store.Now = func() time.Time { return before.ExpiresAt.Add(time.Second) }
	if _, err := tool.Execute(ctx, map[string]any{"resource": "fixture-resource"}); !errors.Is(err, ErrMCPMutationProposalExpired) {
		t.Fatalf("expired replay error = %v", err)
	}
	after, ok, _ := store.Get(context.Background(), proposalID)
	if !ok || !after.ExpiresAt.Equal(before.ExpiresAt) || after.ProposalID != before.ProposalID {
		t.Fatalf("expired replay refreshed the proposal: before=%+v after=%+v", before, after)
	}
}

func TestMCPMutationSameLogicalCallChangedPrincipalConflicts(t *testing.T) {
	_, tool, _, identity, _, alias := newMCPMutationHarness(t)
	first := mcpMutationRuntime(identity, alias, "principal-conflict-run", "principal-conflict-call")
	if _, err := tool.Execute(first, map[string]any{"resource": "fixture-resource"}); err != nil {
		t.Fatalf("first principal proposal: %v", err)
	}
	second := ContextWithToolRuntime(context.Background(), ToolRuntime{
		Auth: ToolAuthContext{Identity: identity, Enforcement: true,
			Allowlist: map[string]bool{alias: true}, AllowedOperations: map[string]bool{alias: true},
			MutationAllowed: true, RunID: "principal-conflict-run", TraceID: "trace"},
		CallID:    "principal-conflict-call",
		Principal: MCPMutationPrincipal{UserID: 8, Username: identity.Owner, Issuer: "test-issuer"},
	})
	if _, err := tool.Execute(second, map[string]any{"resource": "fixture-resource"}); !errors.Is(err, ErrMCPMutationProposalConflict) {
		t.Fatalf("changed principal for the same run/call error = %v, want conflict", err)
	}
}

func TestMCPMutationPendingQuotaIsPerPrincipalAcrossRuns(t *testing.T) {
	_, tool, store, identity, _, alias := newMCPMutationHarness(t)
	policy := mcpMutationTestPolicy(alias)
	policy.Operations[0].MaxProposalsPerRun = 4
	policy.Operations[0].MaxPendingProposals = 1
	limited, err := NewMCPMutationProposalTool(tool.descriptor.manager, policy, store)
	if err != nil {
		t.Fatalf("limited proposal tool: %v", err)
	}
	if _, err := limited.Execute(mcpMutationRuntime(identity, alias, "pending-run-1", "call-1"),
		map[string]any{"resource": "fixture-resource"}); err != nil {
		t.Fatalf("first pending proposal: %v", err)
	}
	if _, err := limited.Execute(mcpMutationRuntime(identity, alias, "pending-run-2", "call-2"),
		map[string]any{"resource": "fixture-resource"}); !errors.Is(err, ErrMCPMutationQuota) {
		t.Fatalf("cross-run pending quota error = %v, want quota", err)
	}
}

func TestMCPMutationQuotaStateSemantics(t *testing.T) {
	cases := map[MCPMutationProposalState]bool{
		MCPProposalProposed:               true,
		MCPProposalApprovalRequired:       true,
		MCPProposalApproved:               true,
		MCPProposalDispatching:            true,
		MCPProposalUncertain:              true,
		MCPProposalReconciliationRequired: true,
		MCPProposalDenied:                 false,
		MCPProposalExpired:                false,
		MCPProposalSucceeded:              false,
		MCPProposalFailed:                 false,
	}
	for state, want := range cases {
		if got := mcpProposalConsumesQuota(state); got != want {
			t.Errorf("state %q consumes quota = %t, want %t", state, got, want)
		}
	}
}

func TestMCPMutationAdmissionDefensiveInvariants(t *testing.T) {
	_, tool, store, identity, _, alias := newMCPMutationHarness(t)
	result, err := tool.Execute(mcpMutationRuntime(identity, alias, "invariant-run", "invariant-call"),
		map[string]any{"resource": "fixture-resource"})
	if err != nil {
		t.Fatalf("initial proposal: %v", err)
	}
	base, ok, err := store.Get(context.Background(), result["proposal_id"].(string))
	if err != nil || !ok {
		t.Fatalf("initial proposal lookup: %v %v", ok, err)
	}
	t.Run("canonical argument mismatch", func(t *testing.T) {
		tampered := base
		tampered.ProposalID = "tampered-canonical"
		tampered.RunID = "tampered-canonical-run"
		tampered.EffectiveCallID = "tampered-canonical-call"
		tampered.CanonicalArguments = []byte(`{"resource":"other-resource"}`)
		tampered.ProposalSHA256 = proposalFingerprint(tampered)
		if _, err := store.Admit(context.Background(), tampered, 8, 8); !errors.Is(err, ErrMCPMutationArguments) {
			t.Fatalf("canonical mismatch error = %v, want arguments error", err)
		}
	})
	t.Run("fingerprint mismatch", func(t *testing.T) {
		mismatched := base
		mismatched.ProposalID = "mismatched-fingerprint"
		mismatched.RunID = "mismatched-fingerprint-run"
		mismatched.EffectiveCallID = "mismatched-fingerprint-call"
		mismatched.ProposalSHA256 = "not-the-recomputed-fingerprint"
		if _, err := store.Admit(context.Background(), mismatched, 8, 8); !errors.Is(err, ErrMCPMutationProposalConflict) {
			t.Fatalf("fingerprint mismatch error = %v, want conflict", err)
		}
	})
	t.Run("proposal ID reuse", func(t *testing.T) {
		reused := base
		reused.RunID = "reused-id-run"
		reused.EffectiveCallID = "reused-id-call"
		reused.ProposalSHA256 = proposalFingerprint(reused)
		if _, err := store.Admit(context.Background(), reused, 8, 8); !errors.Is(err, ErrMCPMutationProposalConflict) {
			t.Fatalf("proposal ID reuse error = %v, want conflict", err)
		}
	})
	t.Run("non-initial state", func(t *testing.T) {
		dispatched := base
		dispatched.ProposalID = "dispatched-direct"
		dispatched.RunID = "dispatched-direct-run"
		dispatched.EffectiveCallID = "dispatched-direct-call"
		dispatched.ProposalSHA256 = proposalFingerprint(dispatched)
		dispatched.State = MCPProposalDispatching
		if _, err := store.Admit(context.Background(), dispatched, 8, 8); !errors.Is(err, ErrMCPMutationProposalConflict) {
			t.Fatalf("direct dispatching admission error = %v, want conflict", err)
		}
	})
	t.Run("cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		cancelled := base
		cancelled.ProposalID = "cancelled-admission"
		cancelled.RunID = "cancelled-admission-run"
		cancelled.EffectiveCallID = "cancelled-admission-call"
		cancelled.ProposalSHA256 = "cancelled-admission-fingerprint"
		if _, err := store.Admit(ctx, cancelled, 8, 8); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled admission error = %v, want context cancellation", err)
		}
	})
}

func TestMCPMutationScopeRejectsMixedSelectors(t *testing.T) {
	sourceOperation := MCPMutationOperationPolicy{
		AllowedSources: []uint64{42},
	}
	if _, _, err := mcpMutationScope(sourceOperation, map[string]any{
		"source_id": float64(42), "resource": "fixture-resource",
	}); !errors.Is(err, ErrMCPMutationArguments) {
		t.Fatalf("mixed source/resource selectors error = %v, want arguments error", err)
	}
	if _, _, err := mcpMutationScope(sourceOperation, map[string]any{
		"source_id": float64(42), "resource_id": "fixture-resource",
	}); !errors.Is(err, ErrMCPMutationArguments) {
		t.Fatalf("mixed source/resource_id selectors error = %v, want arguments error", err)
	}
	resourceOperation := MCPMutationOperationPolicy{
		AllowedResources: []string{"fixture-resource"},
	}
	if _, _, err := mcpMutationScope(resourceOperation, map[string]any{
		"source_id": float64(42), "resource": "fixture-resource",
	}); !errors.Is(err, ErrMCPMutationArguments) {
		t.Fatalf("mixed resource/source selector error = %v, want arguments error", err)
	}
}

func TestMCPMutationFingerprintNormalizesSourceIDs(t *testing.T) {
	base := MCPMutationProposal{
		RunID: "normalize-run", EffectiveCallID: "normalize-call",
		AgentID: "agent", Owner: "owner", ServerID: "server",
		RawToolName: "tool", ToolAlias: "alias", Transport: "stdio",
		TransportIdentity: "command", SchemaSHA256: "schema",
		ArgumentsSHA256: "arguments", PolicyID: "policy",
		PolicyVersion: "1", PolicySHA256: "policy-hash",
	}
	withoutSources := base
	withoutSources.SourceIDs = nil
	withEmptySources := base
	withEmptySources.SourceIDs = []uint64{}
	if proposalFingerprint(withoutSources) != proposalFingerprint(withEmptySources) {
		t.Fatalf("nil and empty source lists produced different fingerprints")
	}
}
