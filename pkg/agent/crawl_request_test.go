package agent

import (
	"testing"
	"time"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

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
		AgentID: "agent-1", Owner: "owner-1", SourceID: 42,
		OperationType: cdb.CrawlOperationType, ApprovedBy: "operator-1",
		ApprovedAt: now.Add(-time.Minute), ExpiresAt: now.Add(30 * time.Minute),
	}
}

func TestAuthorizeCrawlRequestBuildsBoundRequest(t *testing.T) {
	now := validCrawlPolicyNow()
	identity := validCrawlPolicyIdentity()
	proposal := validCrawlPolicyProposal()
	approval := validCrawlPolicyApproval(proposal)
	decision := AuthorizeCrawlRequest(identity, map[uint64]bool{42: true}, proposal, &approval, "run-1", "trace-1", now)
	if !decision.Allowed || decision.ReasonCode != CrawlReasonApproved {
		t.Fatalf("valid proposal denied: %+v", decision)
	}
	request, err := BuildCrawlScheduleRequest(identity, proposal, approval, decision, now)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if len(request.AllowedSources) != 1 || request.AllowedSources[0] != proposal.SourceID {
		t.Fatalf("approved source scope missing: %+v", request.AllowedSources)
	}
	if request.RequestID != proposal.RequestID || request.AgentID != identity.AgentID ||
		request.Owner != identity.Owner || request.SourceID != proposal.SourceID ||
		request.ParametersSHA256 != decision.ProposalSHA256 ||
		request.ApprovalSHA256 != decision.ApprovalSHA256 ||
		request.ApprovedBy != approval.ApprovedBy {
		t.Fatalf("unexpected scheduling request: %+v", request)
	}
	if !request.ExpiresAt.Equal(approval.ExpiresAt) {
		t.Fatalf("request lifetime should be bounded by approval: %v", request.ExpiresAt)
	}
}

func TestAuthorizeCrawlRequestRejectsPolicyFailures(t *testing.T) {
	now := validCrawlPolicyNow()
	proposal := validCrawlPolicyProposal()
	approval := validCrawlPolicyApproval(proposal)
	base := validCrawlPolicyIdentity()
	scope := map[uint64]bool{42: true}

	cases := []struct {
		name     string
		identity AgentIdentity
		scope    map[uint64]bool
		proposal CrawlRequestProposal
		approval *CrawlRequestApproval
		runID    string
		now      time.Time
		want     string
	}{
		{"missing identity", AgentIdentity{}, scope, proposal, &approval, "run-1", now, CrawlReasonMissingIdentity},
		{"missing run", base, scope, proposal, &approval, "", now, CrawlReasonRunRequired},
		{"insufficient trust", func() AgentIdentity { v := base; v.TrustLevel = "restricted"; return v }(), scope, proposal, &approval, "run-1", now, CrawlReasonTrustRequired},
		{"missing tool_execution", func() AgentIdentity { v := base; v.Capabilities = []string{"db_write"}; return v }(), scope, proposal, &approval, "run-1", now, CrawlReasonCapabilityRequired},
		{"missing db_write", func() AgentIdentity { v := base; v.Capabilities = []string{"tool_execution"}; return v }(), scope, proposal, &approval, "run-1", now, CrawlReasonCapabilityRequired},
		{"empty source scope", base, nil, proposal, &approval, "run-1", now, CrawlReasonSourceScopeRequired},
		{"source outside scope", base, map[uint64]bool{7: true}, proposal, &approval, "run-1", now, CrawlReasonSourceOutOfScope},
		{"missing approval", base, scope, proposal, nil, "run-1", now, CrawlReasonApprovalRequired},
		{"proposal expired", base, scope, func() CrawlRequestProposal { v := proposal; v.ExpiresAt = now; return v }(), &approval, "run-1", now, CrawlReasonProposalExpired},
		{"approval expired", base, scope, proposal, func() *CrawlRequestApproval {
			v := approval
			v.ExpiresAt = now
			return &v
		}(), "run-1", now, CrawlReasonApprovalExpired},
		{"modified agent", base, scope, proposal, func() *CrawlRequestApproval {
			v := approval
			v.AgentID = "agent-2"
			return &v
		}(), "run-1", now, CrawlReasonApprovalMismatch},
		{"modified owner", base, scope, proposal, func() *CrawlRequestApproval {
			v := approval
			v.Owner = "owner-2"
			return &v
		}(), "run-1", now, CrawlReasonApprovalMismatch},
		{"modified operation", base, scope, proposal, func() *CrawlRequestApproval {
			v := approval
			v.OperationType = "scrape"
			return &v
		}(), "run-1", now, CrawlReasonApprovalMismatch},
		{"modified source", base, scope, proposal, func() *CrawlRequestApproval {
			v := approval
			v.SourceID = 7
			return &v
		}(), "run-1", now, CrawlReasonApprovalMismatch},
		{"modified proposal", base, scope, func() CrawlRequestProposal {
			v := proposal
			v.ExpiresAt = now.Add(2 * time.Hour)
			return v
		}(), &approval, "run-1", now, CrawlReasonApprovalMismatch},
		{"contract forbidden", func() AgentIdentity {
			v := base
			v.Contract = &AgentContract{ForbiddenActions: []string{"tool:" + crawlRequestToolName}}
			return v
		}(), scope, proposal, &approval, "run-1", now, CrawlReasonContractForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision := AuthorizeCrawlRequest(tc.identity, tc.scope, tc.proposal, tc.approval, tc.runID, "trace-1", tc.now)
			if decision.Allowed || decision.ReasonCode != tc.want {
				t.Fatalf("got allowed=%v reason=%s, want %s", decision.Allowed, decision.ReasonCode, tc.want)
			}
			approvalArg := tc.approval
			if approvalArg == nil {
				approvalArg = &approval
			}
			if _, err := BuildCrawlScheduleRequest(tc.identity, tc.proposal, *approvalArg, decision, tc.now); err == nil {
				t.Fatal("denied decision produced a submittable request")
			}
		})
	}
}

func TestCrawlRequestAllCapabilityStillNeedsScopeAndApproval(t *testing.T) {
	now := validCrawlPolicyNow()
	identity := validCrawlPolicyIdentity()
	identity.Capabilities = []string{"all"}
	proposal := validCrawlPolicyProposal()
	approval := validCrawlPolicyApproval(proposal)

	decision := AuthorizeCrawlRequest(identity, nil, proposal, &approval, "run-1", "trace-1", now)
	if decision.Allowed || decision.ReasonCode != CrawlReasonSourceScopeRequired {
		t.Fatalf("all capability bypassed source scope: %+v", decision)
	}
	decision = AuthorizeCrawlRequest(identity, map[uint64]bool{42: true}, proposal, nil, "run-1", "trace-1", now)
	if decision.Allowed || decision.ReasonCode != CrawlReasonApprovalRequired {
		t.Fatalf("all capability bypassed approval: %+v", decision)
	}
	decision = AuthorizeCrawlRequest(identity, map[uint64]bool{42: true}, proposal, &approval, "run-1", "trace-1", now)
	if !decision.Allowed {
		t.Fatalf("all capability with scope and approval should be allowed: %+v", decision)
	}
}

func TestAuditCrawlSchedulingDecision(t *testing.T) {
	now := validCrawlPolicyNow()
	identity := validCrawlPolicyIdentity()
	proposal := validCrawlPolicyProposal()
	approval := validCrawlPolicyApproval(proposal)
	decision := AuthorizeCrawlRequest(identity, map[uint64]bool{42: true}, proposal, &approval, "run-1", "trace-1", now)
	result := cdb.CrawlScheduleResult{
		OperationID: "operation-1", RequestID: proposal.RequestID,
		AgentID: identity.AgentID, Owner: identity.Owner, SourceID: proposal.SourceID,
		OperationType: proposal.OperationType, State: cdb.CrawlOperationAccepted,
		ReasonCode: cdb.CrawlReasonScheduled,
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
		event.Outcome != auditOutcomeAllowed || event.ReasonCode != cdb.CrawlReasonScheduled {
		t.Fatalf("unexpected audit event: %+v", event)
	}

	denied := decision
	denied.Allowed = false
	denied.ReasonCode = CrawlReasonApprovalRequired
	engine.AuditCrawlSchedulingDecision(identity, proposal, denied, nil, "run-2", "trace-2")
	events = engine.AuditEvents()
	if events[1].Outcome != auditOutcomeDenied || events[1].ReasonCode != CrawlReasonApprovalRequired {
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
