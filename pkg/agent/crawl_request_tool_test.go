package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func newCrawlToolTestRuntime(t *testing.T, handler *cdb.Handler, sources []uint64, now time.Time) *CrawlRequestToolRuntime {
	t.Helper()
	runtime, err := BuildCrawlRequestToolRuntime(context.Background(), CrawlRequestToolRuntimeConfig{
		Enabled: true, DB: *handler,
		Identity: AgentIdentity{
			AgentID: "agent-1", Owner: "owner-1", TrustLevel: "trusted",
			Capabilities: []string{"tool_execution", "db_write"},
		},
		AllowedOperations: []string{crawlRequestToolName},
		AllowedSources:    sources,
		MaxRequests:       2,
		BudgetWindow:      time.Hour,
		Issuer:            "crowler-api",
		ProposalTTL:       time.Minute,
		Now:               func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("build crawl tool runtime: %v", err)
	}
	return runtime
}

func crawlToolContext(t *testing.T, handler *cdb.Handler, runtime *CrawlRequestToolRuntime, callID string) context.Context {
	t.Helper()
	return ContextWithToolRuntime(context.Background(), ToolRuntime{
		DB:     *handler,
		CallID: callID,
		Crawl:  runtime,
		Auth: ToolAuthContext{
			Identity:          runtime.Identity,
			Enforcement:       true,
			Allowlist:         map[string]bool{crawlRequestToolName: true},
			AllowedSources:    map[uint64]bool{42: true},
			AllowedOperations: map[string]bool{crawlRequestToolName: true},
			MutationAllowed:   true,
			RunID:             "run-1",
			TraceID:           "trace-1",
		},
	})
}

func TestCrawlRequestToolPersistsProposalWithoutScheduling(t *testing.T) {
	handler := newCrawlSchedulingTestDB(t)
	now := time.Now().UTC()
	runtime := newCrawlToolTestRuntime(t, handler, []uint64{42}, now)
	tool := &crawlRequestTool{runtime: runtime}

	result, err := tool.Execute(crawlToolContext(t, handler, runtime, "call-1"), map[string]any{"source_id": float64(42)})
	if err != nil {
		t.Fatalf("execute crawl request: %v", err)
	}
	if result["state"] != "approval_required" || result["approval_required"] != true {
		t.Fatalf("unexpected result: %+v", result)
	}
	requestID, _ := result["request_id"].(string)
	expected, _ := CrawlToolRequestID("run-1", "call-1", 42)
	if requestID != expected {
		t.Fatalf("request ID = %q, want %q", requestID, expected)
	}
	row, found, err := cdb.GetCrawlProposalByRequest(context.Background(), handler, requestID)
	if err != nil || !found {
		t.Fatalf("read durable proposal: found=%v err=%v", found, err)
	}
	if row.State != cdb.CrawlProposalStatePending || row.PrincipalUserID != 7 ||
		row.SourceID != 42 || row.MaxRequests != 2 || row.BudgetWindowSeconds != 3600 ||
		row.PolicyJSON == "" {
		t.Fatalf("unexpected durable proposal: %+v", row)
	}
	var snapshot crawlProposalPolicySnapshot
	if err := json.Unmarshal([]byte(row.PolicyJSON), &snapshot); err != nil {
		t.Fatalf("decode policy snapshot: %v", err)
	}
	if snapshot.Identity.AgentID != "agent-1" || snapshot.Identity.Owner != "owner-1" ||
		!containsString(snapshot.AllowedOperations, crawlRequestToolName) ||
		!containsUint64(snapshot.AllowedSources, 42) {
		t.Fatalf("unexpected policy snapshot: %+v", snapshot)
	}
	status, operations := crawlSchedulingTestSourceState(t, handler, 42)
	if status != "completed" || operations != 0 {
		t.Fatalf("proposal mutated source state: status=%q operations=%d", status, operations)
	}

	duplicate, err := tool.Execute(crawlToolContext(t, handler, runtime, "call-1"), map[string]any{"source_id": float64(42)})
	if err != nil {
		t.Fatalf("duplicate execute: %v", err)
	}
	if duplicate["request_id"] != result["request_id"] || duplicate["state"] != "approval_required" {
		t.Fatalf("duplicate proposal identity changed: %+v vs %+v", duplicate, result)
	}
}

func TestCrawlRequestToolDeniesMissingMutationAuthority(t *testing.T) {
	handler := newCrawlSchedulingTestDB(t)
	now := time.Now().UTC()
	if _, err := BuildCrawlRequestToolRuntime(context.Background(), CrawlRequestToolRuntimeConfig{
		Enabled: false, DB: *handler,
	}); !errors.Is(err, ErrCrawlMutationDisabled) {
		t.Fatalf("disabled gate error = %v", err)
	}
	if _, err := BuildCrawlRequestToolRuntime(context.Background(), CrawlRequestToolRuntimeConfig{
		Enabled: true, DB: *handler,
		Identity:          AgentIdentity{AgentID: "agent-1", Owner: "owner-1"},
		AllowedOperations: []string{"request_source_crawl"},
		AllowedSources:    []uint64{42}, MaxRequests: 1, BudgetWindow: time.Hour,
		Issuer: "crowler-api",
	}); err != nil {
		t.Fatalf("valid runtime build failed: %v", err)
	}
	if _, err := BuildCrawlRequestToolRuntime(context.Background(), CrawlRequestToolRuntimeConfig{
		Enabled: true, DB: *handler,
		Identity:          AgentIdentity{AgentID: "agent-1", Owner: "owner-1"},
		AllowedOperations: []string{"other_tool"},
		AllowedSources:    []uint64{42}, MaxRequests: 1, BudgetWindow: time.Hour,
		Issuer: "crowler-api",
	}); !errors.Is(err, ErrCrawlMutationOperationRequired) {
		t.Fatalf("operation gate error = %v", err)
	}
	if _, err := BuildCrawlRequestToolRuntime(context.Background(), CrawlRequestToolRuntimeConfig{
		Enabled: true, DB: *handler,
		Identity:          AgentIdentity{AgentID: "agent-1", Owner: "missing-owner"},
		AllowedOperations: []string{"request_source_crawl"},
		AllowedSources:    []uint64{42}, MaxRequests: 1, BudgetWindow: time.Hour,
		Issuer: "crowler-api",
	}); !errors.Is(err, ErrCrawlPrincipalUnavailable) {
		t.Fatalf("principal gate error = %v", err)
	}

	runtime := newCrawlToolTestRuntime(t, handler, []uint64{42}, now)
	registry := NewAgentToolRegistry()
	if err := RegisterCrawlRequestTool(registry, runtime); err != nil {
		t.Fatalf("register mutation tool: %v", err)
	}
	if err := registry.Register(&crawlRequestTool{runtime: runtime}); err == nil {
		t.Fatalf("read-only Register accepted a mutation tool")
	}
	tool, _ := registry.Get(crawlRequestToolName)
	args := map[string]any{"source_id": float64(42)}
	base := ToolAuthContext{
		Identity: runtime.Identity, Enforcement: true,
		Allowlist:      map[string]bool{crawlRequestToolName: true},
		AllowedSources: map[uint64]bool{42: true},
		RunID:          "run-1",
	}
	if err := AuthorizeToolCall(base, tool, args); err == nil {
		t.Fatalf("mutation without MutationAllowed was accepted")
	} else if authErr, ok := err.(*ToolAuthError); !ok || authErr.Code != ToolDenyMutationDisabled {
		t.Fatalf("unexpected mutation gate error: %v", err)
	}
	base.MutationAllowed = true
	if err := AuthorizeToolCall(base, tool, args); err == nil {
		t.Fatalf("mutation without operation allowlist was accepted")
	} else if authErr, ok := err.(*ToolAuthError); !ok || authErr.Code != ToolDenyOperation {
		t.Fatalf("unexpected operation gate error: %v", err)
	}
	base.AllowedOperations = map[string]bool{crawlRequestToolName: true}
	if err := AuthorizeToolCall(base, tool, args); err != nil {
		t.Fatalf("fully authorized mutation denied: %v", err)
	}

	outOfScope := newCrawlToolTestRuntime(t, handler, []uint64{42}, now)
	if _, err := (&crawlRequestTool{runtime: outOfScope}).Execute(
		crawlToolContext(t, handler, outOfScope, "call-scope"),
		map[string]any{"source_id": float64(43)},
	); err == nil {
		t.Fatalf("out-of-scope source was accepted")
	} else if authErr, ok := err.(*ToolAuthError); !ok || authErr.Code != CrawlReasonSourceOutOfScope {
		t.Fatalf("unexpected scope denial: %v", err)
	}
}

func TestCrawlRequestToolApprovalIsRecordedButNeverScheduledByModel(t *testing.T) {
	handler := newCrawlSchedulingTestDB(t)
	now := time.Now().UTC()
	runtime := newCrawlToolTestRuntime(t, handler, []uint64{42}, now)
	tool := &crawlRequestTool{runtime: runtime}
	result, err := tool.Execute(crawlToolContext(t, handler, runtime, "call-2"), map[string]any{"source_id": float64(42)})
	if err != nil {
		t.Fatalf("execute crawl request: %v", err)
	}
	requestID := result["request_id"].(string)
	row, found, err := cdb.GetCrawlProposalByRequest(context.Background(), handler, requestID)
	if err != nil || !found {
		t.Fatalf("read proposal: found=%v err=%v", found, err)
	}
	store := runtime.ApprovalStore.(*DatabaseCrawlApprovalStore)
	if _, err := store.IssueCrawlApproval(context.Background(), CrawlApprovalIssuanceRequest{
		Proposal: CrawlRequestProposal{
			RequestID: row.RequestID, SourceID: row.SourceID,
			OperationType: row.OperationType, ExpiresAt: row.ExpiresAt,
		},
		Identity: runtime.Identity, Principal: runtime.Policy.Principal,
		RunID: row.RunID, IssuerPrincipal: runtime.Policy.Principal,
		Approver: runtime.Policy.Principal, Issuer: "crowler-api",
		ApprovedAt: now, ExpiresAt: now.Add(10 * time.Minute),
	}); err != nil {
		t.Fatalf("issue approval: %v", err)
	}
	again, err := tool.Execute(crawlToolContext(t, handler, runtime, "call-2"), map[string]any{"source_id": float64(42)})
	if err != nil {
		t.Fatalf("execute with approval: %v", err)
	}
	if again["state"] != "approval_recorded" || again["approval_required"] != false {
		t.Fatalf("unexpected approved proposal result: %+v", again)
	}
	status, operations := crawlSchedulingTestSourceState(t, handler, 42)
	if status != "completed" || operations != 0 {
		t.Fatalf("model-facing tool scheduled a crawl: status=%q operations=%d", status, operations)
	}
}

func TestCrawlRequestApprovedSubmissionIsIdempotent(t *testing.T) {
	handler := newCrawlSchedulingTestDB(t)
	now := time.Now().UTC()
	runtime := newCrawlToolTestRuntime(t, handler, []uint64{42}, now)
	tool := &crawlRequestTool{runtime: runtime}
	result, err := tool.Execute(crawlToolContext(t, handler, runtime, "call-3"), map[string]any{"source_id": float64(42)})
	if err != nil {
		t.Fatalf("execute crawl request: %v", err)
	}
	requestID := result["request_id"].(string)
	row, found, err := cdb.GetCrawlProposalByRequest(context.Background(), handler, requestID)
	if err != nil || !found {
		t.Fatalf("read proposal: found=%v err=%v", found, err)
	}
	store := runtime.ApprovalStore.(*DatabaseCrawlApprovalStore)
	approval, err := store.IssueCrawlApproval(context.Background(), CrawlApprovalIssuanceRequest{
		Proposal: CrawlRequestProposal{
			RequestID: row.RequestID, SourceID: row.SourceID,
			OperationType: row.OperationType, ExpiresAt: row.ExpiresAt,
		},
		Identity: runtime.Identity, Principal: runtime.Policy.Principal,
		RunID: row.RunID, IssuerPrincipal: runtime.Policy.Principal,
		Approver: runtime.Policy.Principal, Issuer: "crowler-api",
		ApprovedAt: now, ExpiresAt: now.Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatalf("issue approval: %v", err)
	}
	submitted, err := runtime.SubmitApprovedProposal(context.Background(), requestID, approval.ApprovalID, runtime.Policy.Principal)
	if err != nil {
		t.Fatalf("submit approved proposal: %v", err)
	}
	if submitted.State != cdb.CrawlOperationAccepted || submitted.Reused {
		t.Fatalf("unexpected scheduling result: %+v", submitted)
	}
	status, operations := crawlSchedulingTestSourceState(t, handler, 42)
	if status != "pending" || operations != 1 {
		t.Fatalf("scheduling result status=%q operations=%d", status, operations)
	}
	stored, found, err := cdb.GetCrawlProposalByRequest(context.Background(), handler, requestID)
	if err != nil || !found || stored.State != cdb.CrawlProposalStateSubmitted || stored.OperationID.String != submitted.OperationID {
		t.Fatalf("proposal not marked submitted: %+v found=%v err=%v", stored, found, err)
	}
	repeat, err := runtime.SubmitApprovedProposal(context.Background(), requestID, approval.ApprovalID, runtime.Policy.Principal)
	if err != nil {
		t.Fatalf("repeat submission: %v", err)
	}
	if !repeat.Reused {
		t.Fatalf("repeat submission was not idempotent: %+v", repeat)
	}
	status, operations = crawlSchedulingTestSourceState(t, handler, 42)
	if status != "pending" || operations != 1 {
		t.Fatalf("repeat created duplicate work: status=%q operations=%d", status, operations)
	}
}

func TestCrawlRequestSubmissionRejectsBindingAndAuthorizationChanges(t *testing.T) {
	handler := newCrawlSchedulingTestDB(t)
	now := time.Now().UTC()
	runtime := newCrawlToolTestRuntime(t, handler, []uint64{42}, now)
	tool := &crawlRequestTool{runtime: runtime}
	result, err := tool.Execute(crawlToolContext(t, handler, runtime, "call-4"), map[string]any{"source_id": float64(42)})
	if err != nil {
		t.Fatalf("execute crawl request: %v", err)
	}
	requestID := result["request_id"].(string)
	row, found, err := cdb.GetCrawlProposalByRequest(context.Background(), handler, requestID)
	if err != nil || !found {
		t.Fatalf("read proposal: found=%v err=%v", found, err)
	}
	store := runtime.ApprovalStore.(*DatabaseCrawlApprovalStore)
	issue := func(runID string) CrawlRequestApproval {
		approval, err := store.IssueCrawlApproval(context.Background(), CrawlApprovalIssuanceRequest{
			Proposal: CrawlRequestProposal{
				RequestID: row.RequestID, SourceID: row.SourceID,
				OperationType: row.OperationType, ExpiresAt: row.ExpiresAt,
			},
			Identity: runtime.Identity, Principal: runtime.Policy.Principal,
			RunID: runID, IssuerPrincipal: runtime.Policy.Principal,
			Approver: runtime.Policy.Principal, Issuer: "crowler-api",
			ApprovedAt: now, ExpiresAt: now.Add(10 * time.Minute),
		})
		if err != nil {
			t.Fatalf("issue approval for %s: %v", runID, err)
		}
		return approval
	}
	approval := issue(row.RunID)
	if _, err := runtime.SubmitApprovedProposal(context.Background(), requestID, strings.Repeat("0", 64), runtime.Policy.Principal); !errors.Is(err, ErrCrawlApprovalUnknown) {
		t.Fatalf("unknown approval error = %v", err)
	}

	if _, err := (*handler).Exec(`UPDATE SourceOwnership SET user_id=8, verified_by_user_id=8 WHERE source_id=42`); err != nil {
		t.Fatalf("transfer source ownership: %v", err)
	}
	if _, err := runtime.SubmitApprovedProposal(context.Background(), requestID, approval.ApprovalID, runtime.Policy.Principal); !errors.Is(err, ErrCrawlSubmissionDenied) && !errors.Is(err, ErrCrawlApprovalApproverUnauthorized) {
		t.Fatalf("ownership transfer submission error = %v", err)
	}
	if _, err := (*handler).Exec(`UPDATE SourceOwnership SET user_id=7, verified_by_user_id=7 WHERE source_id=42`); err != nil {
		t.Fatalf("restore source ownership: %v", err)
	}
	if _, err := (*handler).Exec(`UPDATE Users SET disabled=TRUE WHERE user_id=7`); err != nil {
		t.Fatalf("disable principal: %v", err)
	}
	if _, err := runtime.SubmitApprovedProposal(context.Background(), requestID, approval.ApprovalID, runtime.Policy.Principal); !errors.Is(err, ErrCrawlApprovalApproverUnauthorized) {
		t.Fatalf("disabled principal submission error = %v", err)
	}
	if _, err := (*handler).Exec(`UPDATE Users SET disabled=FALSE WHERE user_id=7`); err != nil {
		t.Fatalf("re-enable principal: %v", err)
	}

	crossRun := issue("run-other")
	if _, err := runtime.SubmitApprovedProposal(context.Background(), requestID, crossRun.ApprovalID, runtime.Policy.Principal); !errors.Is(err, ErrCrawlApprovalForged) {
		t.Fatalf("cross-run submission error = %v", err)
	}
	if _, err := (*handler).Exec(`UPDATE CrawlProposals SET expires_at=? WHERE request_id=?`, now.Add(-time.Minute), requestID); err != nil {
		t.Fatalf("expire proposal: %v", err)
	}
	if _, err := runtime.SubmitApprovedProposal(context.Background(), requestID, approval.ApprovalID, runtime.Policy.Principal); !errors.Is(err, ErrCrawlProposalExpired) {
		t.Fatalf("expired proposal submission error = %v", err)
	}
	status, operations := crawlSchedulingTestSourceState(t, handler, 42)
	if status != "completed" || operations != 0 {
		t.Fatalf("denied submissions mutated source: status=%q operations=%d", status, operations)
	}
}

func TestCrawlRequestProposalIsNotGoalCompletionEvidence(t *testing.T) {
	handler := newCrawlSchedulingTestDB(t)
	now := time.Now().UTC()
	runtime := newCrawlToolTestRuntime(t, handler, []uint64{42}, now)
	registry := NewAgentToolRegistry()
	if err := RegisterCrawlRequestTool(registry, runtime); err != nil {
		t.Fatalf("register crawl mutation tool: %v", err)
	}
	provider := &scriptedProvider{name: "scripted", responses: []any{
		chatWithCalls(stringCall("goal-call-1", crawlRequestToolName, `{"source_id":42}`)),
		chatWithText("proposal recorded"),
	}}
	spec := &AgentGoalSpec{
		GoalID: "goal-crawl-proposal", Objective: "propose a crawl without completing it",
		Provider: "scripted", URL: "https://example.com/v1/chat", Model: "scripted",
		Prompt:    "propose the crawl",
		Allowlist: []string{crawlRequestToolName}, AllowedSources: []uint64{42},
		MutationPolicy: &MutationPolicy{
			Enabled: true, AllowedOperations: []string{crawlRequestToolName},
			Issuer: "crowler-api", ProposalTTL: time.Minute,
			MaxRequests: 1, BudgetWindow: time.Hour,
		},
		Evidence: EvidencePolicy{
			Allow: []EvidenceKind{EvidenceSourceStatus}, DefaultMaxAge: 15 * time.Minute,
			MaxPayloadBytes: 4096,
		},
		Budgets: GoalBudgets{
			MaxModelRounds: 3, MaxToolCalls: 3, MaxEvidenceRecords: 8,
			MaxEvidenceBytes: 16384, MaxTotalResultBytes: 32768,
			MaxElapsed: 20 * time.Second, MaxConsecutiveDenials: 3,
		},
		Acceptance: AcceptanceSpec{
			Mode: AcceptanceAll,
			Criteria: []CriterionSpec{{
				ID: "source-pending", Description: "source must be pending",
				Predicate: Predicate{
					Kind: PredicateResourceStatus, Resource: "source", ResourceID: 42, Status: "pending",
				},
			}},
		},
	}
	spec.SpecHash = computeGoalSpecHash(spec)
	result, err := RunGoal(goalRunConfig{
		Spec: spec,
		Identity: AgentIdentity{
			AgentID: "agent-1", Owner: "owner-1", TrustLevel: "trusted",
			Capabilities: []string{"goal_execution", "ai_reasoning", "db_read", "db_write", "tool_execution"},
		},
		DB: *handler, Registry: registry,
		EvidenceRegistry: NewDefaultGoalEvidenceRegistry(*handler),
		Provider:         provider, RunID: "goal-run-1", TraceID: "goal-trace-1",
		AllowedOperations: map[string]bool{crawlRequestToolName: true},
		MutationAllowed:   true, CrawlRuntime: runtime,
	})
	if err != nil {
		t.Fatalf("run goal: %v", err)
	}
	if result.Status == GoalCriteriaMet {
		t.Fatalf("a proposal was treated as crawl completion: %+v", result)
	}
	if result.Status != GoalIncomplete || len(result.ToolCalls) != 1 ||
		result.ToolCalls[0].Name != crawlRequestToolName || result.ToolCalls[0].Status != "executed" {
		t.Fatalf("unexpected goal result: %+v", result)
	}
	status, operations := crawlSchedulingTestSourceState(t, handler, 42)
	if status != "completed" || operations != 0 {
		t.Fatalf("goal proposal mutated source: status=%q operations=%d", status, operations)
	}
	var proposals int
	if err := (*handler).QueryRow(`SELECT COUNT(*) FROM CrawlProposals`).Scan(&proposals); err != nil {
		t.Fatalf("count goal proposals: %v", err)
	}
	if proposals != 1 {
		t.Fatalf("goal proposal count = %d, want 1", proposals)
	}
}

func TestCrawlRequestToolRetryKeepsDurableExpiry(t *testing.T) {
	handler := newCrawlSchedulingTestDB(t)
	firstNow := time.Now().UTC()
	first := newCrawlToolTestRuntime(t, handler, []uint64{42}, firstNow)
	firstResult, err := (&crawlRequestTool{runtime: first}).Execute(
		crawlToolContext(t, handler, first, "stable-call"), map[string]any{"source_id": float64(42)})
	if err != nil {
		t.Fatalf("first proposal: %v", err)
	}
	requestID := firstResult["request_id"].(string)
	before, found, err := cdb.GetCrawlProposalByRequest(context.Background(), handler, requestID)
	if err != nil || !found {
		t.Fatalf("read first proposal: found=%v err=%v", found, err)
	}
	secondNow := firstNow.Add(10 * time.Minute)
	second := newCrawlToolTestRuntime(t, handler, []uint64{42}, secondNow)
	secondResult, err := (&crawlRequestTool{runtime: second}).Execute(
		crawlToolContext(t, handler, second, "stable-call"), map[string]any{"source_id": float64(42)})
	if err != nil {
		t.Fatalf("retry proposal: %v", err)
	}
	after, found, err := cdb.GetCrawlProposalByRequest(context.Background(), handler, requestID)
	if err != nil || !found {
		t.Fatalf("read retry proposal: found=%v err=%v", found, err)
	}
	if secondResult["request_id"] != firstResult["request_id"] ||
		!before.ExpiresAt.Equal(after.ExpiresAt) ||
		!secondResult["expires_at"].(time.Time).Equal(before.ExpiresAt) {
		t.Fatalf("retry changed durable identity: before=%+v after=%+v result=%+v", before, after, secondResult)
	}
}
