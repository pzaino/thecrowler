package agent

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

type testCrawlApprovalVerifier struct {
	accepted map[string]bool
	err      error
	calls    int32
}

func (v *testCrawlApprovalVerifier) VerifyCrawlApproval(_ context.Context, approval CrawlRequestApproval, _ CrawlRequestProposal, _ AgentIdentity, _ string, _ time.Time) error {
	if v == nil {
		return ErrCrawlApprovalUnverified
	}
	atomic.AddInt32(&v.calls, 1)
	if v.err != nil {
		return v.err
	}
	if approval.Issuer != "test-authority" || !v.accepted[approval.ApprovalID] {
		return ErrCrawlApprovalUnverified
	}
	return nil
}

func validCrawlPolicyNow() time.Time {
	return time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
}

func validCrawlPolicyIdentity() AgentIdentity {
	return AgentIdentity{
		AgentID: "agent-1", Name: "Crawl Agent", Owner: "owner-1", TrustLevel: "trusted",
		Capabilities: []string{"tool_execution", "db_write"},
	}
}

func validCrawlPolicyProposal() CrawlRequestProposal {
	now := validCrawlPolicyNow()
	return CrawlRequestProposal{
		RequestID: "request-1", SourceID: 42, OperationType: cdb.CrawlOperationType,
		ExpiresAt: now.Add(time.Hour),
	}
}

func validCrawlPolicyApproval(proposal CrawlRequestProposal) CrawlRequestApproval {
	now := validCrawlPolicyNow()
	return CrawlRequestApproval{
		ApprovalID: "approval-1", ProposalSHA256: proposal.Fingerprint(),
		RunID: "run-1", AgentID: "agent-1", Owner: "owner-1", SourceID: 42,
		OperationType: cdb.CrawlOperationType, Issuer: "test-authority",
		ApprovedBy: "operator-1", ApprovedAt: now.Add(-time.Minute),
		ExpiresAt: now.Add(30 * time.Minute),
	}
}

func validCrawlPolicy(verifier CrawlApprovalVerifier) CrawlRequestPolicy {
	return CrawlRequestPolicy{
		AllowedOperations: map[string]bool{crawlRequestToolName: true},
		AllowedSources:    map[uint64]bool{42: true},
		ApprovalVerifier:  verifier,
		MaxRequests:       2,
		BudgetWindow:      time.Hour,
	}
}

func TestAuthorizeCrawlRequestBuildsBoundRequest(t *testing.T) {
	now := validCrawlPolicyNow()
	identity := validCrawlPolicyIdentity()
	proposal := validCrawlPolicyProposal()
	verifier := &testCrawlApprovalVerifier{accepted: map[string]bool{"approval-1": true}}
	approval := validCrawlPolicyApproval(proposal)
	policy := validCrawlPolicy(verifier)
	decision := authorizeCrawlRequestAt(context.Background(), identity, policy, proposal, &approval, "run-1", "trace-1", now)
	if !decision.Allowed || decision.ReasonCode != CrawlReasonApproved || !decision.verified {
		t.Fatalf("valid proposal denied: %+v", decision)
	}
	request, err := buildCrawlScheduleRequestAt(identity, proposal, approval, decision, "run-1", now)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if len(request.AllowedSources) != 1 || request.AllowedSources[0] != proposal.SourceID {
		t.Fatalf("approved source scope missing: %+v", request.AllowedSources)
	}
	if request.RequestID != proposal.RequestID || request.RunID != "run-1" ||
		request.AgentID != identity.AgentID || request.Owner != identity.Owner ||
		request.SourceID != proposal.SourceID ||
		request.ParametersSHA256 != decision.ProposalSHA256 ||
		request.ApprovalSHA256 != decision.ApprovalSHA256 ||
		request.ApprovedBy != approval.ApprovedBy ||
		request.MaxRequests != policy.MaxRequests || request.BudgetWindow != policy.BudgetWindow {
		t.Fatalf("unexpected scheduling request: %+v", request)
	}
	if !request.ExpiresAt.Equal(approval.ExpiresAt) {
		t.Fatalf("request lifetime should be bounded by approval: %v", request.ExpiresAt)
	}
	if maxRequests, window := decision.Quota(); maxRequests != policy.MaxRequests || window != policy.BudgetWindow {
		t.Fatalf("decision quota = (%d,%v), want (%d,%v)", maxRequests, window, policy.MaxRequests, policy.BudgetWindow)
	}
}

func TestAuthorizeCrawlRequestRejectsPolicyFailures(t *testing.T) {
	now := validCrawlPolicyNow()
	proposal := validCrawlPolicyProposal()
	approval := validCrawlPolicyApproval(proposal)
	base := validCrawlPolicyIdentity()
	acceptedVerifier := func() *testCrawlApprovalVerifier {
		return &testCrawlApprovalVerifier{accepted: map[string]bool{"approval-1": true}}
	}
	basePolicy := validCrawlPolicy(acceptedVerifier())

	withoutOperation := basePolicy
	withoutOperation.AllowedOperations = nil
	wrongOperation := basePolicy
	wrongOperation.AllowedOperations = map[string]bool{"other_operation": true}
	noScope := basePolicy
	noScope.AllowedSources = nil
	wrongScope := basePolicy
	wrongScope.AllowedSources = map[uint64]bool{7: true}
	noBudget := basePolicy
	noBudget.MaxRequests = 0
	noVerifier := basePolicy
	noVerifier.ApprovalVerifier = nil
	rejectingVerifier := basePolicy
	rejectingVerifier.ApprovalVerifier = &testCrawlApprovalVerifier{}
	revokedVerifier := basePolicy
	revokedVerifier.ApprovalVerifier = &testCrawlApprovalVerifier{
		accepted: map[string]bool{"approval-1": true}, err: ErrCrawlApprovalRevoked,
	}

	cases := []struct {
		name     string
		identity AgentIdentity
		policy   CrawlRequestPolicy
		proposal CrawlRequestProposal
		approval *CrawlRequestApproval
		runID    string
		now      time.Time
		want     string
	}{
		{"missing identity", AgentIdentity{}, basePolicy, proposal, &approval, "run-1", now, CrawlReasonMissingIdentity},
		{"missing run", base, basePolicy, proposal, &approval, "", now, CrawlReasonRunRequired},
		{"insufficient trust", func() AgentIdentity { v := base; v.TrustLevel = "restricted"; return v }(), basePolicy, proposal, &approval, "run-1", now, CrawlReasonTrustRequired},
		{"missing tool_execution", func() AgentIdentity { v := base; v.Capabilities = []string{"db_write"}; return v }(), basePolicy, proposal, &approval, "run-1", now, CrawlReasonCapabilityRequired},
		{"missing db_write", func() AgentIdentity { v := base; v.Capabilities = []string{"tool_execution"}; return v }(), basePolicy, proposal, &approval, "run-1", now, CrawlReasonCapabilityRequired},
		{"operation allowlist missing", base, withoutOperation, proposal, &approval, "run-1", now, CrawlReasonOperationNotAllowlisted},
		{"operation not allowlisted", base, wrongOperation, proposal, &approval, "run-1", now, CrawlReasonOperationNotAllowlisted},
		{"empty source scope", base, noScope, proposal, &approval, "run-1", now, CrawlReasonSourceScopeRequired},
		{"source outside scope", base, wrongScope, proposal, &approval, "run-1", now, CrawlReasonSourceOutOfScope},
		{"missing budget", base, noBudget, proposal, &approval, "run-1", now, CrawlReasonBudgetRequired},
		{"missing approval", base, basePolicy, proposal, nil, "run-1", now, CrawlReasonApprovalRequired},
		{"proposal expired", base, basePolicy, func() CrawlRequestProposal { v := proposal; v.ExpiresAt = now; return v }(), &approval, "run-1", now, CrawlReasonProposalExpired},
		{"approval expired", base, basePolicy, proposal, func() *CrawlRequestApproval {
			v := approval
			v.ExpiresAt = now
			return &v
		}(), "run-1", now, CrawlReasonApprovalExpired},
		{"cross-run approval", base, basePolicy, proposal, func() *CrawlRequestApproval {
			v := approval
			v.RunID = "run-2"
			return &v
		}(), "run-1", now, CrawlReasonApprovalMismatch},
		{"modified agent", base, basePolicy, proposal, func() *CrawlRequestApproval {
			v := approval
			v.AgentID = "agent-2"
			return &v
		}(), "run-1", now, CrawlReasonApprovalMismatch},
		{"modified owner", base, basePolicy, proposal, func() *CrawlRequestApproval {
			v := approval
			v.Owner = "owner-2"
			return &v
		}(), "run-1", now, CrawlReasonApprovalMismatch},
		{"modified operation", base, basePolicy, proposal, func() *CrawlRequestApproval {
			v := approval
			v.OperationType = "scrape"
			return &v
		}(), "run-1", now, CrawlReasonApprovalMismatch},
		{"modified source", base, basePolicy, proposal, func() *CrawlRequestApproval {
			v := approval
			v.SourceID = 7
			return &v
		}(), "run-1", now, CrawlReasonApprovalMismatch},
		{"modified proposal", base, basePolicy, func() CrawlRequestProposal {
			v := proposal
			v.ExpiresAt = now.Add(2 * time.Hour)
			return v
		}(), &approval, "run-1", now, CrawlReasonApprovalMismatch},
		{"contract forbidden", func() AgentIdentity {
			v := base
			v.Contract = &AgentContract{ForbiddenActions: []string{"tool:" + crawlRequestToolName}}
			return v
		}(), basePolicy, proposal, &approval, "run-1", now, CrawlReasonContractForbidden},
		{"verifier unavailable", base, noVerifier, proposal, &approval, "run-1", now, CrawlReasonApprovalVerifierUnavailable},
		{"approval unverified", base, rejectingVerifier, proposal, &approval, "run-1", now, CrawlReasonApprovalUnverified},
		{"approval revoked", base, revokedVerifier, proposal, &approval, "run-1", now, CrawlReasonApprovalRevoked},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision := authorizeCrawlRequestAt(context.Background(), tc.identity, tc.policy, tc.proposal, tc.approval, tc.runID, "trace-1", tc.now)
			if decision.Allowed || decision.ReasonCode != tc.want {
				t.Fatalf("got allowed=%v reason=%s, want %s", decision.Allowed, decision.ReasonCode, tc.want)
			}
			approvalArg := tc.approval
			if approvalArg == nil {
				approvalArg = &approval
			}
			if _, err := buildCrawlScheduleRequestAt(tc.identity, tc.proposal, *approvalArg, decision, tc.runID, tc.now); err == nil {
				t.Fatal("denied decision produced a submittable request")
			}
		})
	}
}

func TestCrawlRequestAllCapabilityStillNeedsAllowlistScopeAndApproval(t *testing.T) {
	now := validCrawlPolicyNow()
	identity := validCrawlPolicyIdentity()
	identity.Capabilities = []string{"all"}
	proposal := validCrawlPolicyProposal()
	verifier := &testCrawlApprovalVerifier{accepted: map[string]bool{"approval-1": true}}
	approval := validCrawlPolicyApproval(proposal)
	policy := validCrawlPolicy(verifier)

	withoutOperation := policy
	withoutOperation.AllowedOperations = nil
	decision := authorizeCrawlRequestAt(context.Background(), identity, withoutOperation, proposal, &approval, "run-1", "trace-1", now)
	if decision.Allowed || decision.ReasonCode != CrawlReasonOperationNotAllowlisted {
		t.Fatalf("all capability bypassed operation allowlist: %+v", decision)
	}
	withoutScope := policy
	withoutScope.AllowedSources = nil
	decision = authorizeCrawlRequestAt(context.Background(), identity, withoutScope, proposal, &approval, "run-1", "trace-1", now)
	if decision.Allowed || decision.ReasonCode != CrawlReasonSourceScopeRequired {
		t.Fatalf("all capability bypassed source scope: %+v", decision)
	}
	decision = authorizeCrawlRequestAt(context.Background(), identity, policy, proposal, nil, "run-1", "trace-1", now)
	if decision.Allowed || decision.ReasonCode != CrawlReasonApprovalRequired {
		t.Fatalf("all capability bypassed approval: %+v", decision)
	}
	decision = authorizeCrawlRequestAt(context.Background(), identity, policy, proposal, &approval, "run-1", "trace-1", now)
	if !decision.Allowed {
		t.Fatalf("all capability with allowlist/scope/approval should be allowed: %+v", decision)
	}
}

func TestCrawlDecisionCannotBeForged(t *testing.T) {
	now := validCrawlPolicyNow()
	identity := validCrawlPolicyIdentity()
	proposal := validCrawlPolicyProposal()
	verifier := &testCrawlApprovalVerifier{accepted: map[string]bool{"approval-1": true}}
	approval := validCrawlPolicyApproval(proposal)
	policy := validCrawlPolicy(verifier)
	decision := authorizeCrawlRequestAt(context.Background(), identity, policy, proposal, &approval, "run-1", "trace-1", now)
	if !decision.Allowed {
		t.Fatalf("valid decision denied: %+v", decision)
	}
	forged := decision
	forged.verified = false
	if _, err := buildCrawlScheduleRequestAt(identity, proposal, approval, forged, "run-1", now); err == nil {
		t.Fatal("forged decision built a scheduling request")
	}
	changedRun := decision
	changedRun.RunID = "run-2"
	if _, err := buildCrawlScheduleRequestAt(identity, proposal, approval, changedRun, "run-2", now); err == nil {
		t.Fatal("decision was accepted for another run")
	}
	changedApproval := approval
	changedApproval.ApprovedBy = "attacker"
	if _, err := buildCrawlScheduleRequestAt(identity, proposal, changedApproval, decision, "run-1", now); err == nil {
		t.Fatal("altered approval produced a scheduling request")
	}

	otherSource := proposal
	otherSource.SourceID = 7
	otherApproval := approval
	otherApproval.SourceID = 7
	otherApproval.ProposalSHA256 = otherSource.Fingerprint()
	if _, err := buildCrawlScheduleRequestAt(identity, otherSource, otherApproval, decision, "run-1", now); err == nil {
		t.Fatal("decision for one source authorized another source")
	}

	otherRequest := proposal
	otherRequest.RequestID = "request-2"
	otherApproval = approval
	otherApproval.ProposalSHA256 = otherRequest.Fingerprint()
	if _, err := buildCrawlScheduleRequestAt(identity, otherRequest, otherApproval, decision, "run-1", now); err == nil {
		t.Fatal("decision for one request ID authorized another request ID")
	}
}

func TestAuditCrawlSchedulingDecision(t *testing.T) {
	now := validCrawlPolicyNow()
	identity := validCrawlPolicyIdentity()
	proposal := validCrawlPolicyProposal()
	verifier := &testCrawlApprovalVerifier{accepted: map[string]bool{"approval-1": true}}
	approval := validCrawlPolicyApproval(proposal)
	decision := authorizeCrawlRequestAt(context.Background(), identity, validCrawlPolicy(verifier), proposal, &approval, "run-1", "trace-1", now)
	result := cdb.CrawlScheduleResult{
		OperationID: "operation-1", RequestID: proposal.RequestID,
		RunID: "run-1", AgentID: identity.AgentID, Owner: identity.Owner,
		SourceID: proposal.SourceID, OperationType: proposal.OperationType,
		State: cdb.CrawlOperationAccepted, ReasonCode: cdb.CrawlReasonScheduled,
	}
	engine := NewJobEngine()
	engine.AuditCrawlSchedulingDecision(identity, proposal, decision, &result, "run-1", "trace-1")
	events := engine.AuditEvents()
	if len(events) != 1 {
		t.Fatalf("expected one audit event, got %d", len(events))
	}
	event := events[0]
	if event.Action != "CrawlRequest" || event.RunID != "run-1" || event.TraceID != "trace-1" ||
		event.AgentID != identity.AgentID || event.Owner != identity.Owner ||
		event.SourceID != 42 || event.RequestID != proposal.RequestID ||
		event.OperationType != cdb.CrawlOperationType || event.IdempotencyKey != "operation-1" ||
		event.Outcome != auditOutcomeAllowed || event.ReasonCode != cdb.CrawlReasonScheduled ||
		event.AuthorizationOutcome != auditOutcomeAllowed || event.AuthorizationReasonCode != CrawlReasonApproved ||
		event.SchedulingState != string(cdb.CrawlOperationAccepted) ||
		event.Timestamp == nil || event.Timestamp.IsZero() {
		t.Fatalf("unexpected audit event: %+v", event)
	}

	denied := decision
	denied.Allowed = false
	denied.verified = false
	denied.ReasonCode = CrawlReasonApprovalUnverified
	engine.AuditCrawlSchedulingDecision(identity, proposal, denied, nil, "run-2", "trace-2")
	events = engine.AuditEvents()
	if events[1].Outcome != auditOutcomeDenied || events[1].ReasonCode != CrawlReasonApprovalUnverified ||
		events[1].AuthorizationOutcome != auditOutcomeDenied ||
		events[1].AuthorizationReasonCode != CrawlReasonApprovalUnverified ||
		events[1].SchedulingState != "" ||
		events[1].Timestamp == nil || events[1].Timestamp.IsZero() {
		t.Fatalf("unexpected denial audit: %+v", events[1])
	}
}

func TestCrawlSchedulingIsNotExposedAsTool(t *testing.T) {
	registry, err := DefaultToolRegistry()
	if err != nil {
		t.Fatalf("default registry: %v", err)
	}
	if _, exists := registry.Get(crawlRequestToolName); exists {
		t.Fatalf("state-changing crawl request is registered as a model tool")
	}
	for _, definition := range registry.ToolDefinitions() {
		if definition.Function.Name == crawlRequestToolName {
			t.Fatalf("state-changing crawl request is advertised to models")
		}
	}
}

func TestCrawlRequestLifetimeBoundsAndExpiry(t *testing.T) {
	now := validCrawlPolicyNow()
	identity := validCrawlPolicyIdentity()
	verifier := &testCrawlApprovalVerifier{accepted: map[string]bool{"approval-1": true}}
	proposal := validCrawlPolicyProposal()
	policy := validCrawlPolicy(verifier)

	cases := []struct {
		name     string
		proposal CrawlRequestProposal
		approval func(CrawlRequestProposal) CrawlRequestApproval
		want     string
	}{
		{
			name: "proposal beyond lifetime",
			proposal: func() CrawlRequestProposal {
				p := proposal
				p.ExpiresAt = now.Add(maxCrawlProposalLifetime + time.Second)
				return p
			}(),
			approval: validCrawlPolicyApproval,
			want:     CrawlReasonInvalidProposal,
		},
		{
			name:     "approval older than lifetime",
			proposal: proposal,
			approval: func(p CrawlRequestProposal) CrawlRequestApproval {
				a := validCrawlPolicyApproval(p)
				a.ApprovedAt = now.Add(-maxCrawlApprovalLifetime - time.Second)
				return a
			},
			want: CrawlReasonApprovalMismatch,
		},
		{
			name:     "approval beyond lifetime",
			proposal: proposal,
			approval: func(p CrawlRequestProposal) CrawlRequestApproval {
				a := validCrawlPolicyApproval(p)
				a.ExpiresAt = now.Add(maxCrawlApprovalLifetime + time.Second)
				return a
			},
			want: CrawlReasonApprovalMismatch,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			approval := tc.approval(tc.proposal)
			decision := authorizeCrawlRequestAt(context.Background(), identity, policy, tc.proposal, &approval, "run-1", "trace-1", now)
			if decision.Allowed || decision.ReasonCode != tc.want {
				t.Fatalf("got allowed=%v reason=%s, want %s", decision.Allowed, decision.ReasonCode, tc.want)
			}
		})
	}

	t.Run("decision cannot be reused after approval expiry", func(t *testing.T) {
		approval := validCrawlPolicyApproval(proposal)
		decision := authorizeCrawlRequestAt(context.Background(), identity, policy, proposal, &approval, "run-1", "trace-1", now)
		if !decision.Allowed {
			t.Fatalf("valid decision denied: %+v", decision)
		}
		afterExpiry := approval.ExpiresAt.Add(time.Second)
		if _, err := buildCrawlScheduleRequestAt(identity, proposal, approval, decision, "run-1", afterExpiry); err == nil {
			t.Fatal("expired decision produced a scheduling request")
		}
	})
}

func TestCrawlApprovalVerifierOnlyRunsAfterPolicyChecks(t *testing.T) {
	now := validCrawlPolicyNow()
	identity := validCrawlPolicyIdentity()
	proposal := validCrawlPolicyProposal()
	verifier := &testCrawlApprovalVerifier{accepted: map[string]bool{"approval-1": true}}
	policy := validCrawlPolicy(verifier)
	approval := validCrawlPolicyApproval(proposal)

	if decision := authorizeCrawlRequestAt(context.Background(), identity, policy, proposal, nil, "run-1", "trace-1", now); decision.Allowed {
		t.Fatal("missing approval was allowed")
	}
	if got := atomic.LoadInt32(&verifier.calls); got != 0 {
		t.Fatalf("verifier ran without an approval: %d calls", got)
	}

	weakIdentity := identity
	weakIdentity.Capabilities = []string{"tool_execution"}
	if decision := authorizeCrawlRequestAt(context.Background(), weakIdentity, policy, proposal, &approval, "run-1", "trace-1", now); decision.Allowed {
		t.Fatal("missing capability was allowed")
	}
	if got := atomic.LoadInt32(&verifier.calls); got != 0 {
		t.Fatalf("verifier ran before capability checks: %d calls", got)
	}

	if decision := authorizeCrawlRequestAt(context.Background(), identity, policy, proposal, &approval, "run-1", "trace-1", now); !decision.Allowed {
		t.Fatalf("valid request denied: %+v", decision)
	}
	if got := atomic.LoadInt32(&verifier.calls); got != 1 {
		t.Fatalf("verifier calls = %d, want 1", got)
	}
}
