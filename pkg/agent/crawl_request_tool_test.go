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
	expire, err := cdb.MarkCrawlProposalExpired(context.Background(), handler, requestID)
	if err != nil || expire.Outcome != cdb.CrawlProposalTransitionApplied {
		t.Fatalf("expire proposal: outcome=%s err=%v", expire.Outcome, err)
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

type failingApprovalLookupStore struct {
	err error
}

func (s failingApprovalLookupStore) LookupCrawlApproval(ctx context.Context, approvalID string) (CrawlApprovalRecord, bool, error) {
	return CrawlApprovalRecord{}, false, s.err
}

func (s failingApprovalLookupStore) LookupCrawlApprovalByRequest(ctx context.Context, runID, requestID string, principalUserID, sourceID uint64, operationType string) (CrawlApprovalRecord, bool, error) {
	return CrawlApprovalRecord{}, false, s.err
}

func TestCrawlRequestToolDistinguishesApprovalLookupFailureAndState(t *testing.T) {
	handler := newCrawlSchedulingTestDB(t)
	now := time.Now().UTC()
	runtime := newCrawlToolTestRuntime(t, handler, []uint64{42}, now)
	tool := &crawlRequestTool{runtime: runtime}
	result, err := tool.Execute(crawlToolContext(t, handler, runtime, "lookup-call"), map[string]any{"source_id": float64(42)})
	if err != nil {
		t.Fatalf("initial proposal: %v", err)
	}
	requestID := result["request_id"].(string)
	runtime.ApprovalStore = failingApprovalLookupStore{err: errors.New("database down")}
	if _, err := tool.Execute(crawlToolContext(t, handler, runtime, "lookup-call"), map[string]any{"source_id": float64(42)}); err == nil {
		t.Fatalf("approval lookup failure was reported as approval absence")
	} else if authErr, ok := err.(*ToolAuthError); !ok || authErr.Code != CrawlReasonApprovalStoreUnavailable {
		t.Fatalf("approval lookup failure error = %v", err)
	}
	status, operations := crawlSchedulingTestSourceState(t, handler, 42)
	if status != "completed" || operations != 0 {
		t.Fatalf("approval lookup failure mutated source: status=%q operations=%d", status, operations)
	}

	// Restore the real store and issue an approval, then revoke it.
	runtime = newCrawlToolTestRuntime(t, handler, []uint64{42}, now)
	tool = &crawlRequestTool{runtime: runtime}
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
	if _, err := (*handler).Exec(`UPDATE CrawlApprovals SET approved_at=?, expires_at=? WHERE approval_id=?`,
		now.Add(-20*time.Minute), now.Add(-10*time.Minute), approval.ApprovalID); err != nil {
		t.Fatalf("expire approval: %v", err)
	}
	expired, err := tool.Execute(crawlToolContext(t, handler, runtime, "lookup-call"), map[string]any{"source_id": float64(42)})
	if err != nil {
		t.Fatalf("expired approval proposal: %v", err)
	}
	if expired["state"] != "approval_expired" || expired["reason_code"] != CrawlReasonApprovalExpired {
		t.Fatalf("expired approval state = %+v", expired)
	}
	if _, err := (*handler).Exec(`UPDATE CrawlApprovals SET approved_at=?, expires_at=? WHERE approval_id=?`,
		now, now.Add(10*time.Minute), approval.ApprovalID); err != nil {
		t.Fatalf("restore approval lifetime: %v", err)
	}
	if err := store.RevokeCrawlApproval(context.Background(), runtime.Policy.Principal, approval.ApprovalID, "test", now); err != nil {
		t.Fatalf("revoke approval: %v", err)
	}
	revoked, err := tool.Execute(crawlToolContext(t, handler, runtime, "lookup-call"), map[string]any{"source_id": float64(42)})
	if err != nil {
		t.Fatalf("revoked approval proposal: %v", err)
	}
	if revoked["state"] != "approval_revoked" || revoked["reason_code"] != CrawlReasonApprovalRevoked {
		t.Fatalf("revoked approval state = %+v", revoked)
	}
	status, operations = crawlSchedulingTestSourceState(t, handler, 42)
	if status != "completed" || operations != 0 {
		t.Fatalf("approval states mutated source: status=%q operations=%d", status, operations)
	}
}

func TestCrawlRequestToolLoopReportsApprovalLookupFailure(t *testing.T) {
	handler := newCrawlSchedulingTestDB(t)
	now := time.Now().UTC()
	runtime := newCrawlToolTestRuntime(t, handler, []uint64{42}, now)
	tool := &crawlRequestTool{runtime: runtime}
	if _, err := tool.Execute(crawlToolContext(t, handler, runtime, "loop-lookup-call"), map[string]any{"source_id": float64(42)}); err != nil {
		t.Fatalf("seed proposal: %v", err)
	}
	runtime.ApprovalStore = failingApprovalLookupStore{err: errors.New("database down")}
	registry := NewAgentToolRegistry()
	if err := RegisterCrawlRequestTool(registry, runtime); err != nil {
		t.Fatalf("register tool: %v", err)
	}
	provider := &scriptedProvider{name: "scripted", responses: []any{
		chatWithCalls(stringCall("loop-call", crawlRequestToolName, `{"source_id":42}`)),
		chatWithText("done"),
	}}
	result, err := RunToolLoop(ToolLoopConfig{
		Provider: provider, ProviderName: "scripted", Model: "scripted",
		URL: "https://example.com/v1/chat", Prompt: "propose", Registry: registry,
		Allowlist: []string{crawlRequestToolName}, Identity: runtime.Identity, Enforcement: true,
		AllowedSources: []uint64{42}, AllowedOperations: map[string]bool{crawlRequestToolName: true},
		MutationAllowed: true, CrawlRuntime: runtime, DB: *handler,
		Limits: DefaultToolLoopLimits(), RunID: "loop-run", TraceID: "loop-trace",
	})
	if err != nil {
		t.Fatalf("tool loop with lookup failure: %v", err)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Status != "failed" {
		t.Fatalf("approval lookup failure was not reported as a failed tool call: %+v", result.ToolCalls)
	}
	status, operations := crawlSchedulingTestSourceState(t, handler, 42)
	if status != "completed" || operations != 0 {
		t.Fatalf("lookup failure in loop mutated source: status=%q operations=%d", status, operations)
	}
}

func insertExpiredProposalFixture(t *testing.T, handler *cdb.Handler, runtime *CrawlRequestToolRuntime, callID string, now time.Time) (string, string) {
	t.Helper()
	requestID, err := CrawlToolRequestID("run-1", callID, 42)
	if err != nil {
		t.Fatalf("derive expired request ID: %v", err)
	}
	proposal := CrawlRequestProposal{
		RequestID: requestID, SourceID: 42, OperationType: cdb.CrawlOperationType,
		ExpiresAt: now.Add(-time.Minute).UTC().Truncate(time.Microsecond),
	}
	row := cdb.CrawlProposalRow{
		RequestID: requestID, RunID: "run-1", ToolCallID: callID,
		AgentID: runtime.Identity.AgentID, Owner: runtime.Identity.Owner,
		PrincipalUserID: runtime.Policy.Principal.UserID, SourceID: 42,
		OperationType: cdb.CrawlOperationType, ProposalSHA256: proposal.Fingerprint(),
		TraceID: "trace-1", MaxRequests: runtime.Policy.MaxRequests,
		BudgetWindowSeconds: int64(runtime.Policy.BudgetWindow / time.Second),
		PolicyJSON:          "{}", State: cdb.CrawlProposalStatePending, ExpiresAt: proposal.ExpiresAt,
	}
	if inserted, err := cdb.InsertCrawlProposal(context.Background(), handler, row); err != nil || !inserted {
		t.Fatalf("insert expired proposal: inserted=%v err=%v", inserted, err)
	}
	key := cdb.CrawlOperationKey(row.Owner, row.AgentID, row.SourceID, row.OperationType, row.RequestID)
	return requestID, key
}

func TestCrawlRequestExpiryDoesNotSwallowReconciliationFailure(t *testing.T) {
	handler := newCrawlSchedulingTestDB(t)
	now := time.Now().UTC()
	runtime := newCrawlToolTestRuntime(t, handler, []uint64{42}, now)
	tool := &crawlRequestTool{runtime: runtime}
	requestID, key := insertExpiredProposalFixture(t, handler, runtime, "expire-call", now)
	if _, err := (*handler).Exec(`INSERT INTO Status
		(status_sha256, agent_id, source_id, operation_type, status, details)
		VALUES (?, 'agent-1', 42, 'crawl', 'accepted', '{bad')`, key); err != nil {
		t.Fatalf("insert malformed operation: %v", err)
	}
	if _, err := tool.Execute(crawlToolContext(t, handler, runtime, "expire-call"), map[string]any{"source_id": float64(42)}); err == nil {
		t.Fatalf("reconciliation failure was treated as expiry success")
	} else if authErr, ok := err.(*ToolAuthError); !ok || authErr.Code != "crawl_operation_metadata_invalid" {
		t.Fatalf("reconciliation failure error = %v", err)
	}
	stored, found, err := cdb.GetCrawlProposalByRequest(context.Background(), handler, requestID)
	if err != nil || !found || stored.State != cdb.CrawlProposalStatePending {
		t.Fatalf("failed reconciliation mutated proposal: %+v found=%v err=%v", stored, found, err)
	}

	// A transient metadata failure can be repaired; retry must then expire the
	// proposal only after a successful authoritative lookup proves absence.
	if _, err := (*handler).Exec(`DELETE FROM Status WHERE status_sha256=?`, key); err != nil {
		t.Fatalf("remove malformed operation: %v", err)
	}
	expired, err := tool.Execute(crawlToolContext(t, handler, runtime, "expire-call"), map[string]any{"source_id": float64(42)})
	if err != nil {
		t.Fatalf("retry after metadata repair: %v", err)
	}
	if expired["state"] != cdb.CrawlProposalStateExpired {
		t.Fatalf("retry state = %+v", expired)
	}
	stored, found, err = cdb.GetCrawlProposalByRequest(context.Background(), handler, requestID)
	if err != nil || !found || stored.State != cdb.CrawlProposalStateExpired {
		t.Fatalf("retry did not expire proposal: %+v found=%v err=%v", stored, found, err)
	}

	// Cancellation during reconciliation must not mutate proposal state.
	requestID2, _ := insertExpiredProposalFixture(t, handler, runtime, "cancel-call", now)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tool.Execute(crawlToolContextWithContext(cancelled, t, handler, runtime, "cancel-call"), map[string]any{"source_id": float64(42)}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled expiry error = %v", err)
	}
	stored, found, err = cdb.GetCrawlProposalByRequest(context.Background(), handler, requestID2)
	if err != nil || !found || stored.State != cdb.CrawlProposalStatePending {
		t.Fatalf("cancelled reconciliation mutated proposal: %+v found=%v err=%v", stored, found, err)
	}
}

func crawlToolContextWithContext(ctx context.Context, t *testing.T, handler *cdb.Handler, runtime *CrawlRequestToolRuntime, callID string) context.Context {
	t.Helper()
	return ContextWithToolRuntime(ctx, ToolRuntime{
		DB: *handler, CallID: callID, Crawl: runtime,
		Auth: ToolAuthContext{
			Identity: runtime.Identity, Enforcement: true,
			Allowlist:         map[string]bool{crawlRequestToolName: true},
			AllowedSources:    map[uint64]bool{42: true},
			AllowedOperations: map[string]bool{crawlRequestToolName: true},
			MutationAllowed:   true, RunID: "run-1", TraceID: "trace-1",
		},
	})
}

func TestCrawlRequestExpiryDoesNotTreatConflictAsAbsence(t *testing.T) {
	handler := newCrawlSchedulingTestDB(t)
	now := time.Now().UTC()
	runtime := newCrawlToolTestRuntime(t, handler, []uint64{42}, now)
	tool := &crawlRequestTool{runtime: runtime}
	requestID, key := insertExpiredProposalFixture(t, handler, runtime, "conflict-call", now)
	details := `{"request_id":"` + requestID + `","run_id":"different-run","principal_user_id":7,` +
		`"agent_id":"agent-1","owner":"owner-1","source_id":42,"operation_type":"crawl",` +
		`"parameters_sha256":"` + strings.Repeat("a", 64) + `"}`
	if _, err := (*handler).Exec(`INSERT INTO Status
		(status_sha256, agent_id, source_id, operation_type, status, details)
		VALUES (?, 'agent-1', 42, 'crawl', 'accepted', ?)`, key, details); err != nil {
		t.Fatalf("insert conflicting operation: %v", err)
	}
	if _, err := tool.Execute(crawlToolContext(t, handler, runtime, "conflict-call"), map[string]any{"source_id": float64(42)}); err == nil {
		t.Fatalf("conflicting operation was treated as expiry success")
	} else if authErr, ok := err.(*ToolAuthError); !ok || authErr.Code != "crawl_operation_conflict" {
		t.Fatalf("conflicting operation error = %v", err)
	}
	stored, found, err := cdb.GetCrawlProposalByRequest(context.Background(), handler, requestID)
	if err != nil || !found || stored.State != cdb.CrawlProposalStatePending {
		t.Fatalf("conflict mutated proposal: %+v found=%v err=%v", stored, found, err)
	}
}

func TestCrawlRequestSubmissionConflictIsNeverSuccessfulReuse(t *testing.T) {
	handler := newCrawlSchedulingTestDB(t)
	now := time.Now().UTC()
	runtime := newCrawlToolTestRuntime(t, handler, []uint64{42}, now)
	tool := &crawlRequestTool{runtime: runtime}
	result, err := tool.Execute(crawlToolContext(t, handler, runtime, "submit-conflict-call"), map[string]any{"source_id": float64(42)})
	if err != nil {
		t.Fatalf("proposal: %v", err)
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
	key := cdb.CrawlOperationKey(row.Owner, row.AgentID, row.SourceID, row.OperationType, row.RequestID)
	details := `{"request_id":"` + row.RequestID + `","run_id":"different-run","principal_user_id":7,` +
		`"agent_id":"agent-1","owner":"owner-1","source_id":42,"operation_type":"crawl",` +
		`"parameters_sha256":"` + strings.Repeat("a", 64) + `"}`
	if _, err := (*handler).Exec(`INSERT INTO Status
		(status_sha256, agent_id, source_id, operation_type, status, details)
		VALUES (?, 'agent-1', 42, 'crawl', 'accepted', ?)`, key, details); err != nil {
		t.Fatalf("insert conflicting operation: %v", err)
	}
	submitted, err := runtime.SubmitApprovedProposal(context.Background(), requestID, approval.ApprovalID, runtime.Policy.Principal)
	if !errors.Is(err, ErrCrawlOperationConflict) {
		t.Fatalf("conflicting submission error = %v", err)
	}
	if submitted.Reused || submitted.State == cdb.CrawlOperationAccepted {
		t.Fatalf("conflicting operation reported as successful reuse: %+v", submitted)
	}
	stored, found, err := cdb.GetCrawlProposalByRequest(context.Background(), handler, requestID)
	if err != nil || !found || stored.State != cdb.CrawlProposalStatePending {
		t.Fatalf("conflict changed proposal: %+v found=%v err=%v", stored, found, err)
	}
	status, operations := crawlSchedulingTestSourceState(t, handler, 42)
	if status != "completed" || operations != 1 {
		t.Fatalf("conflict source status=%q operations=%d", status, operations)
	}

	// Malformed metadata must also fail rather than expire/schedule.
	runtime2 := newCrawlToolTestRuntime(t, handler, []uint64{42}, now)
	tool2 := &crawlRequestTool{runtime: runtime2}
	result2, err := tool2.Execute(crawlToolContext(t, handler, runtime2, "submit-metadata-call"), map[string]any{"source_id": float64(42)})
	if err != nil {
		t.Fatalf("second proposal: %v", err)
	}
	requestID2 := result2["request_id"].(string)
	row2, _, _ := cdb.GetCrawlProposalByRequest(context.Background(), handler, requestID2)
	approval2, err := store.IssueCrawlApproval(context.Background(), CrawlApprovalIssuanceRequest{
		Proposal: CrawlRequestProposal{
			RequestID: row2.RequestID, SourceID: row2.SourceID,
			OperationType: row2.OperationType, ExpiresAt: row2.ExpiresAt,
		},
		Identity: runtime2.Identity, Principal: runtime2.Policy.Principal,
		RunID: row2.RunID, IssuerPrincipal: runtime2.Policy.Principal,
		Approver: runtime2.Policy.Principal, Issuer: "crowler-api",
		ApprovedAt: now, ExpiresAt: now.Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatalf("issue second approval: %v", err)
	}
	key2 := cdb.CrawlOperationKey(row2.Owner, row2.AgentID, row2.SourceID, row2.OperationType, row2.RequestID)
	if _, err := (*handler).Exec(`INSERT INTO Status
		(status_sha256, agent_id, source_id, operation_type, status, details)
		VALUES (?, 'agent-1', 42, 'crawl', 'accepted', '{bad')`, key2); err != nil {
		t.Fatalf("insert malformed operation: %v", err)
	}
	if _, err := runtime2.SubmitApprovedProposal(context.Background(), requestID2, approval2.ApprovalID, runtime2.Policy.Principal); !errors.Is(err, ErrCrawlOperationMetadataInvalid) {
		t.Fatalf("malformed metadata submission error = %v", err)
	}
	stored2, _, _ := cdb.GetCrawlProposalByRequest(context.Background(), handler, requestID2)
	if stored2.State != cdb.CrawlProposalStatePending {
		t.Fatalf("malformed metadata changed proposal to %q", stored2.State)
	}
}
