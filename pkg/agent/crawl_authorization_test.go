package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	auth "github.com/pzaino/thecrowler/pkg/auth"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

type memoryCrawlApprovalStore struct {
	mu               sync.Mutex
	records          map[string]CrawlApprovalRecord
	nextID           int
	sourceAuthorizer CrawlSourceAuthorizer
}

func newMemoryCrawlApprovalStore(sourceAuthorizer CrawlSourceAuthorizer) *memoryCrawlApprovalStore {
	return &memoryCrawlApprovalStore{
		records:          make(map[string]CrawlApprovalRecord),
		sourceAuthorizer: sourceAuthorizer,
	}
}

func (s *memoryCrawlApprovalStore) IssueCrawlApproval(ctx context.Context, request CrawlApprovalIssuanceRequest) (CrawlRequestApproval, error) {
	if err := ctx.Err(); err != nil {
		return CrawlRequestApproval{}, err
	}
	if s.sourceAuthorizer != nil {
		if err := s.sourceAuthorizer.AuthorizeCrawlSource(ctx, request.Approver, request.Proposal.SourceID); err != nil {
			return CrawlRequestApproval{}, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	approval := CrawlRequestApproval{
		ApprovalID:     fmt.Sprintf("approval-%d", s.nextID),
		ProposalSHA256: request.Proposal.Fingerprint(),
		RunID:          request.RunID,
		AgentID:        request.Identity.AgentID,
		Owner:          request.Identity.Owner,
		SourceID:       request.Proposal.SourceID,
		OperationType:  request.Proposal.OperationType,
		Issuer:         request.Issuer,
		ApprovedBy:     request.Approver.Username,
		ApproverUserID: request.Approver.UserID,
		ApprovedAt:     request.ApprovedAt,
		ExpiresAt:      request.ExpiresAt,
	}
	issuerPrincipal := request.IssuerPrincipal
	if issuerPrincipal.UserID == 0 {
		issuerPrincipal = request.Approver
	}
	s.records[approval.ApprovalID] = CrawlApprovalRecord{
		Approval:          approval,
		Proposal:          request.Proposal,
		IssuerPrincipal:   issuerPrincipal,
		ApproverPrincipal: request.Approver,
	}
	return approval, nil
}

func (s *memoryCrawlApprovalStore) LookupCrawlApproval(ctx context.Context, approvalID string) (CrawlApprovalRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return CrawlApprovalRecord{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, found := s.records[approvalID]
	return record, found, nil
}

func (s *memoryCrawlApprovalStore) LookupCrawlApprovalByRequest(ctx context.Context, runID, requestID string, principalUserID, sourceID uint64, operationType string) (CrawlApprovalRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return CrawlApprovalRecord{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, record := range s.records {
		proposal := record.Proposal
		if record.Approval.RunID == runID &&
			proposal.RequestID == requestID &&
			proposal.SourceID == sourceID &&
			proposal.OperationType == operationType &&
			record.Approval.SourceID == sourceID &&
			record.Approval.OperationType == operationType {
			return record, true, nil
		}
	}
	return CrawlApprovalRecord{}, false, nil
}

func (s *memoryCrawlApprovalStore) RevokeCrawlApproval(ctx context.Context, _ CrawlPrincipal, approvalID, _ string, _ time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, found := s.records[approvalID]
	if !found {
		return ErrCrawlApprovalUnknown
	}
	record.Revoked = true
	s.records[approvalID] = record
	return nil
}

type testSourceOwnerLookup struct {
	owner cdb.SourceOwner
	err   error
	calls int32
}

func (l *testSourceOwnerLookup) LookupCrawlSourceOwner(_ context.Context, _ uint64) (cdb.SourceOwner, error) {
	atomic.AddInt32(&l.calls, 1)
	if l.err != nil {
		return cdb.SourceOwner{}, l.err
	}
	return l.owner, nil
}

func validApprovalAuthority(t *testing.T) (*memoryCrawlApprovalStore, StoreBackedCrawlApprovalVerifier, CrawlRequestProposal, AgentIdentity, CrawlPrincipal, CrawlRequestApproval) {
	t.Helper()
	now := validCrawlPolicyNow()
	proposal := validCrawlPolicyProposal()
	identity := validCrawlPolicyIdentity()
	principal := validCrawlPrincipal()
	approver := CrawlPrincipal{UserID: 9, Username: "operator-1", Issuer: "crowler"}
	store := newMemoryCrawlApprovalStore(&testCrawlSourceAuthorizer{owners: map[uint64]uint64{42: 9}})
	approval, err := store.IssueCrawlApproval(context.Background(), CrawlApprovalIssuanceRequest{
		Proposal:   proposal,
		Identity:   identity,
		Principal:  principal,
		RunID:      "run-1",
		Approver:   approver,
		Issuer:     "test-authority",
		ApprovedAt: now.Add(-time.Minute),
		ExpiresAt:  now.Add(30 * time.Minute),
	})
	if err != nil {
		t.Fatalf("issue approval: %v", err)
	}
	verifier := StoreBackedCrawlApprovalVerifier{
		Store:            store,
		SourceAuthorizer: &testCrawlSourceAuthorizer{owners: map[uint64]uint64{42: 9}},
		TrustedIssuers:   map[string]bool{"test-authority": true},
		MaxLifetime:      time.Hour,
	}
	return store, verifier, proposal, identity, principal, approval
}

func TestStoreBackedCrawlApprovalVerifierAcceptsAuthoritativeApproval(t *testing.T) {
	_, verifier, proposal, identity, principal, approval := validApprovalAuthority(t)
	now := validCrawlPolicyNow()
	if err := verifier.VerifyCrawlApproval(context.Background(), CrawlApprovalVerification{
		Approval: approval, Proposal: proposal, Identity: identity,
		Principal: principal, RunID: "run-1", Now: now,
	}); err != nil {
		t.Fatalf("authoritative approval rejected: %v", err)
	}
}

func TestStoreBackedCrawlApprovalVerifierRejectsInvalidApprovals(t *testing.T) {
	now := validCrawlPolicyNow()
	_, verifier, proposal, identity, principal, approval := validApprovalAuthority(t)
	base := CrawlApprovalVerification{
		Approval: approval, Proposal: proposal, Identity: identity,
		Principal: principal, RunID: "run-1", Now: now,
	}

	cases := []struct {
		name   string
		mutate func(*CrawlApprovalVerification, *StoreBackedCrawlApprovalVerifier)
		want   error
	}{
		{"unknown approval", func(v *CrawlApprovalVerification, _ *StoreBackedCrawlApprovalVerifier) {
			v.Approval.ApprovalID = "missing"
		}, ErrCrawlApprovalUnknown},
		{"forged approved_by", func(v *CrawlApprovalVerification, _ *StoreBackedCrawlApprovalVerifier) {
			v.Approval.ApprovedBy = "attacker"
		}, ErrCrawlApprovalForged},
		{"forged expiry", func(v *CrawlApprovalVerification, _ *StoreBackedCrawlApprovalVerifier) {
			v.Approval.ExpiresAt = v.Approval.ExpiresAt.Add(time.Minute)
		}, ErrCrawlApprovalForged},
		{"cross-run", func(v *CrawlApprovalVerification, _ *StoreBackedCrawlApprovalVerifier) {
			v.RunID = "run-2"
		}, ErrCrawlApprovalForged},
		{"cross-source", func(v *CrawlApprovalVerification, _ *StoreBackedCrawlApprovalVerifier) {
			v.Proposal.SourceID = 7
		}, ErrCrawlApprovalForged},
		{"cross-owner", func(v *CrawlApprovalVerification, _ *StoreBackedCrawlApprovalVerifier) {
			v.Identity.Owner = "owner-2"
		}, ErrCrawlApprovalForged},
		{"cross-principal", func(v *CrawlApprovalVerification, _ *StoreBackedCrawlApprovalVerifier) {
			v.Principal.Username = "owner-2"
		}, ErrCrawlApprovalForged},
		{"missing principal", func(v *CrawlApprovalVerification, _ *StoreBackedCrawlApprovalVerifier) {
			v.Principal = CrawlPrincipal{}
		}, ErrCrawlApprovalForged},
		{"untrusted issuer", func(_ *CrawlApprovalVerification, verifier *StoreBackedCrawlApprovalVerifier) {
			verifier.TrustedIssuers = nil
		}, ErrCrawlApprovalIssuerUntrusted},
		{"approver unauthorized", func(_ *CrawlApprovalVerification, verifier *StoreBackedCrawlApprovalVerifier) {
			verifier.SourceAuthorizer = &testCrawlSourceAuthorizer{owners: map[uint64]uint64{42: 8}}
		}, ErrCrawlApprovalApproverUnauthorized},
		{"approver source unavailable", func(_ *CrawlApprovalVerification, verifier *StoreBackedCrawlApprovalVerifier) {
			verifier.SourceAuthorizer = &testCrawlSourceAuthorizer{err: errors.New("database unavailable")}
		}, ErrCrawlApprovalApproverUnavailable},
		{"store unavailable", func(_ *CrawlApprovalVerification, verifier *StoreBackedCrawlApprovalVerifier) {
			verifier.Store = nil
		}, ErrCrawlApprovalStoreUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			verification := base
			testVerifier := verifier
			tc.mutate(&verification, &testVerifier)
			if err := testVerifier.VerifyCrawlApproval(context.Background(), verification); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}

	t.Run("revoked approval", func(t *testing.T) {
		store, revokedVerifier, revokedProposal, revokedIdentity, revokedPrincipal, revokedApproval := validApprovalAuthority(t)
		if err := store.RevokeCrawlApproval(context.Background(), validCrawlPrincipal(), revokedApproval.ApprovalID, "test", now); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		err := revokedVerifier.VerifyCrawlApproval(context.Background(), CrawlApprovalVerification{
			Approval: revokedApproval, Proposal: revokedProposal, Identity: revokedIdentity,
			Principal: revokedPrincipal, RunID: "run-1", Now: now,
		})
		if !errors.Is(err, ErrCrawlApprovalRevoked) {
			t.Fatalf("got %v, want ErrCrawlApprovalRevoked", err)
		}
	})

	t.Run("oversized approval lifetime", func(t *testing.T) {
		store, lifetimeVerifier, lifetimeProposal, lifetimeIdentity, lifetimePrincipal, _ := validApprovalAuthority(t)
		approval, err := store.IssueCrawlApproval(context.Background(), CrawlApprovalIssuanceRequest{
			Proposal: lifetimeProposal, Identity: lifetimeIdentity, Principal: lifetimePrincipal,
			RunID: "run-1", Approver: CrawlPrincipal{UserID: 9, Username: "operator-1", Issuer: "crowler"},
			Issuer: "test-authority", ApprovedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(2 * time.Hour),
		})
		if err != nil {
			t.Fatalf("issue oversized approval: %v", err)
		}
		err = lifetimeVerifier.VerifyCrawlApproval(context.Background(), CrawlApprovalVerification{
			Approval: approval, Proposal: lifetimeProposal, Identity: lifetimeIdentity,
			Principal: lifetimePrincipal, RunID: "run-1", Now: now,
		})
		if !errors.Is(err, ErrCrawlApprovalLifetime) {
			t.Fatalf("got %v, want ErrCrawlApprovalLifetime", err)
		}
	})

	t.Run("cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := verifier.VerifyCrawlApproval(ctx, base); !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want context.Canceled", err)
		}
	})
}

func TestStoreBackedCrawlApprovalVerifierConcurrentReads(t *testing.T) {
	_, verifier, proposal, identity, principal, approval := validApprovalAuthority(t)
	now := validCrawlPolicyNow()
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- verifier.VerifyCrawlApproval(context.Background(), CrawlApprovalVerification{
				Approval: approval, Proposal: proposal, Identity: identity,
				Principal: principal, RunID: "run-1", Now: now,
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent verification: %v", err)
		}
	}
}

func TestDatabaseCrawlSourceAuthorizer(t *testing.T) {
	principal := validCrawlPrincipal()
	cases := []struct {
		name       string
		authorizer DatabaseCrawlSourceAuthorizer
		principal  CrawlPrincipal
		want       error
	}{
		{"valid owner", DatabaseCrawlSourceAuthorizer{Lookup: &testSourceOwnerLookup{owner: cdb.SourceOwner{SourceID: 42, UserID: 7, Found: true, Verified: true}}}, principal, nil},
		{"wrong owner", DatabaseCrawlSourceAuthorizer{Lookup: &testSourceOwnerLookup{owner: cdb.SourceOwner{SourceID: 42, UserID: 8, Found: true, Verified: true}}}, principal, ErrCrawlSourceForbidden},
		{"missing ownership", DatabaseCrawlSourceAuthorizer{Lookup: &testSourceOwnerLookup{owner: cdb.SourceOwner{SourceID: 42, UserID: 0, Found: true}}}, principal, ErrCrawlSourceOwnershipMissing},
		{"missing source", DatabaseCrawlSourceAuthorizer{Lookup: &testSourceOwnerLookup{owner: cdb.SourceOwner{SourceID: 42}}}, principal, ErrCrawlSourceOwnershipMissing},
		{"legacy unverified owner", DatabaseCrawlSourceAuthorizer{Lookup: &testSourceOwnerLookup{owner: cdb.SourceOwner{SourceID: 42, UserID: 7, Found: true}}}, principal, ErrCrawlSourceOwnershipMissing},
		{"lookup error", DatabaseCrawlSourceAuthorizer{Lookup: &testSourceOwnerLookup{err: errors.New("database unavailable")}}, principal, ErrCrawlSourceAuthorizationUnavailable},
		{"nil lookup", DatabaseCrawlSourceAuthorizer{}, principal, ErrCrawlSourceAuthorizationUnavailable},
		{"invalid principal", DatabaseCrawlSourceAuthorizer{Lookup: &testSourceOwnerLookup{owner: cdb.SourceOwner{SourceID: 42, UserID: 7, Found: true, Verified: true}}}, CrawlPrincipal{}, ErrCrawlSourceAuthorizationUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.authorizer.AuthorizeCrawlSource(context.Background(), tc.principal, 42)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}

	t.Run("cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		authorizer := DatabaseCrawlSourceAuthorizer{Lookup: &testSourceOwnerLookup{owner: cdb.SourceOwner{SourceID: 42, UserID: 7, Found: true, Verified: true}}}
		if err := authorizer.AuthorizeCrawlSource(ctx, principal, 42); !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want context.Canceled", err)
		}
	})
}

func TestCrawlPrincipalFromAuthIdentity(t *testing.T) {
	principal, err := CrawlPrincipalFromAuthIdentity(auth.Identity{Subject: "7", Username: "owner-1", Issuer: "crowler"})
	if err != nil {
		t.Fatalf("adapt auth identity: %v", err)
	}
	if principal.UserID != 7 || principal.Username != "owner-1" || principal.Issuer != "crowler" {
		t.Fatalf("unexpected principal: %+v", principal)
	}
	for _, identity := range []auth.Identity{
		{Subject: "external-subject", Username: "owner-1", Issuer: "crowler"},
		{Subject: "0", Username: "owner-1", Issuer: "crowler"},
		{Subject: "7", Username: "", Issuer: "crowler"},
		{Subject: "7", Username: "owner-1", Issuer: ""},
	} {
		if _, err := CrawlPrincipalFromAuthIdentity(identity); err == nil {
			t.Fatalf("invalid auth identity accepted: %+v", identity)
		}
	}
}

func TestCrawlRequestToolContract(t *testing.T) {
	if err := (CrawlRequestToolInput{SourceID: 0}).Validate(); err == nil {
		t.Fatal("zero source accepted")
	}
	if err := (CrawlRequestToolInput{SourceID: 42}).Validate(); err != nil {
		t.Fatalf("valid source rejected: %v", err)
	}

	first, err := CrawlToolRequestID("run-1", "call-1", 42)
	if err != nil {
		t.Fatalf("request ID: %v", err)
	}
	second, err := CrawlToolRequestID("run-1", "call-1", 42)
	if err != nil || first != second {
		t.Fatalf("request ID is not stable: %q %q %v", first, second, err)
	}
	differentSource, err := CrawlToolRequestID("run-1", "call-1", 43)
	if err != nil || differentSource == first {
		t.Fatalf("source change did not change request ID: %q %q %v", first, differentSource, err)
	}
	differentRun, err := CrawlToolRequestID("run-2", "call-1", 42)
	if err != nil || differentRun == first {
		t.Fatalf("run change did not change request ID: %q %q %v", first, differentRun, err)
	}
	if _, err := CrawlToolRequestID("", "call-1", 42); err == nil {
		t.Fatal("empty run ID accepted")
	}
	if _, err := CrawlToolRequestID("run-1", "", 42); err == nil {
		t.Fatal("empty tool call ID accepted")
	}

	observedAt := validCrawlPolicyNow()
	result := NewCrawlRequestToolResult(cdb.CrawlScheduleResult{
		OperationID: "operation-1", RequestID: first, SourceID: 42,
		State: cdb.CrawlOperationAccepted, ReasonCode: cdb.CrawlReasonScheduled,
		Reused: false, SourceObservedAt: observedAt,
	}, observedAt)
	if result.OperationID != "operation-1" || result.RequestID != first || result.SourceID != 42 ||
		result.State != string(cdb.CrawlOperationAccepted) || result.ReasonCode != cdb.CrawlReasonScheduled ||
		result.Reused || !result.ObservedAt.Equal(observedAt) {
		t.Fatalf("unexpected tool result: %+v", result)
	}
}

type mutableSourceOwnerLookup struct {
	mu    sync.Mutex
	owner cdb.SourceOwner
	err   error
}

func (l *mutableSourceOwnerLookup) LookupCrawlSourceOwner(_ context.Context, _ uint64) (cdb.SourceOwner, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.owner, l.err
}

func (l *mutableSourceOwnerLookup) SetOwner(userID uint64, found bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.owner.UserID = userID
	l.owner.Found = found
}

func TestMemoryApprovalIssuerRejectsUnauthorizedApprover(t *testing.T) {
	now := validCrawlPolicyNow()
	proposal := validCrawlPolicyProposal()
	identity := validCrawlPolicyIdentity()
	principal := validCrawlPrincipal()
	store := newMemoryCrawlApprovalStore(&testCrawlSourceAuthorizer{owners: map[uint64]uint64{42: 8}})
	_, err := store.IssueCrawlApproval(context.Background(), CrawlApprovalIssuanceRequest{
		Proposal: proposal, Identity: identity, Principal: principal, RunID: "run-1",
		Approver: CrawlPrincipal{UserID: 9, Username: "operator-1", Issuer: "crowler"},
		Issuer:   "test-authority", ApprovedAt: now.Add(-time.Minute), ExpiresAt: now.Add(30 * time.Minute),
	})
	if !errors.Is(err, ErrCrawlSourceForbidden) {
		t.Fatalf("got %v, want ErrCrawlSourceForbidden", err)
	}
}

func TestAuthorizeCrawlRequestRejectsRevokedStoredApproval(t *testing.T) {
	now := validCrawlPolicyNow()
	store, verifier, proposal, identity, _, approval := validApprovalAuthority(t)
	policy := validCrawlPolicy(verifier)
	if decision := authorizeCrawlRequestAt(context.Background(), identity, policy, proposal, &approval, "run-1", "trace-1", now); !decision.Allowed() {
		t.Fatalf("valid approval denied: %+v", decision)
	}
	if err := store.RevokeCrawlApproval(context.Background(), validCrawlPrincipal(), approval.ApprovalID, "test revocation", now); err != nil {
		t.Fatalf("revoke approval: %v", err)
	}
	decision := authorizeCrawlRequestAt(context.Background(), identity, policy, proposal, &approval, "run-1", "trace-1", now)
	if decision.Allowed() || decision.ReasonCode() != CrawlReasonApprovalRevoked {
		t.Fatalf("revoked approval was accepted: %+v", decision)
	}
	if decision.ApprovalOutcome() != CrawlApprovalOutcomeRevoked {
		t.Fatalf("unexpected revoked approval outcome: %+v", decision)
	}
}

func TestAuthorizeCrawlRequestRejectsApprovalForDifferentRequestOrSource(t *testing.T) {
	now := validCrawlPolicyNow()
	_, verifier, proposal, identity, _, approval := validApprovalAuthority(t)
	policy := validCrawlPolicy(verifier)

	changedRequest := proposal
	changedRequest.RequestID = "request-2"
	decision := authorizeCrawlRequestAt(context.Background(), identity, policy, changedRequest, &approval, "run-1", "trace-1", now)
	if decision.Allowed() || decision.ReasonCode() != CrawlReasonApprovalMismatch {
		t.Fatalf("approval was reused for a different request ID: %+v", decision)
	}

	changedSource := proposal
	changedSource.SourceID = 43
	decision = authorizeCrawlRequestAt(context.Background(), identity, policy, changedSource, &approval, "run-1", "trace-1", now)
	if decision.Allowed() || decision.ReasonCode() != CrawlReasonSourceOutOfScope {
		t.Fatalf("approval was reused for a different source: %+v", decision)
	}
}

func TestAuthorizeCrawlRequestConcurrentSameApproval(t *testing.T) {
	now := validCrawlPolicyNow()
	_, verifier, proposal, identity, _, approval := validApprovalAuthority(t)
	policy := validCrawlPolicy(verifier)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			decision := authorizeCrawlRequestAt(context.Background(), identity, policy, proposal, &approval, "run-1", "trace-1", now)
			if !decision.Allowed() {
				errs <- fmt.Errorf("concurrent authorization denied: %s", decision.ReasonCode())
				return
			}
			errs <- nil
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestDatabaseCrawlSourceAuthorizerOwnershipRevocation(t *testing.T) {
	lookup := &mutableSourceOwnerLookup{owner: cdb.SourceOwner{SourceID: 42, UserID: 7, Found: true, Verified: true}}
	authorizer := DatabaseCrawlSourceAuthorizer{Lookup: lookup}
	principal := validCrawlPrincipal()
	if err := authorizer.AuthorizeCrawlSource(context.Background(), principal, 42); err != nil {
		t.Fatalf("valid ownership denied: %v", err)
	}
	lookup.SetOwner(8, true)
	if err := authorizer.AuthorizeCrawlSource(context.Background(), principal, 42); !errors.Is(err, ErrCrawlSourceForbidden) {
		t.Fatalf("revoked ownership was accepted: %v", err)
	}
	lookup.SetOwner(0, false)
	if err := authorizer.AuthorizeCrawlSource(context.Background(), principal, 42); !errors.Is(err, ErrCrawlSourceOwnershipMissing) {
		t.Fatalf("missing ownership was accepted: %v", err)
	}
}

func TestCrawlSchedulingWithTestOnlyApproval(t *testing.T) {
	handler := newCrawlSchedulingTestDB(t)
	now := time.Now().UTC()
	proposal := CrawlRequestProposal{
		RequestID: "test-only-request-1", SourceID: 42,
		OperationType: cdb.CrawlOperationType, ExpiresAt: now.Add(time.Hour),
	}
	identity := validCrawlPolicyIdentity()
	principal := validCrawlPrincipal()
	policy := validCrawlPolicy(nil)
	policy.SourceAuthorizer = DatabaseCrawlSourceAuthorizer{
		Lookup: DatabaseSourceOwnerLookup{DB: handler},
	}

	approver := CrawlPrincipal{UserID: 9, Username: "operator-1", Issuer: "crowler", Roles: []string{"admin"}}
	operatorSourceAuthorizer := DatabaseCrawlSourceAuthorizer{
		Lookup: DatabaseSourceOwnerLookup{DB: handler}, AllowAdmin: true,
	}
	store := &DatabaseCrawlApprovalStore{
		DB: handler, SourceAuthorizer: operatorSourceAuthorizer,
		Issuer: "test-authority", MaxLifetime: time.Hour,
	}
	approval, err := store.IssueCrawlApproval(context.Background(), CrawlApprovalIssuanceRequest{
		Proposal: proposal, Identity: identity, Principal: principal, RunID: "run-1",
		IssuerPrincipal: approver, Approver: approver, Issuer: "test-authority",
		ApprovedAt: now.Add(-time.Minute), ExpiresAt: now.Add(30 * time.Minute),
	})
	if err != nil {
		t.Fatalf("issue test-only approval: %v", err)
	}
	policy.ApprovalVerifier = StoreBackedCrawlApprovalVerifier{
		Store: store, SourceAuthorizer: operatorSourceAuthorizer,
		TrustedIssuers: map[string]bool{"test-authority": true}, MaxLifetime: time.Hour,
	}
	decision := authorizeCrawlRequestAt(context.Background(), identity, policy, proposal, &approval, "run-1", "trace-1", now)
	if !decision.Allowed() {
		t.Fatalf("approved test-only request denied: %+v", decision)
	}
	if decision.SourceOwnershipOutcome() != CrawlSourceOutcomeVerified ||
		decision.ApprovalOutcome() != CrawlApprovalOutcomeVerified {
		t.Fatalf("unexpected authorization evidence: %+v", decision)
	}
	request, err := buildCrawlScheduleRequestAt(identity, proposal, approval, decision, "run-1", now)
	if err != nil {
		t.Fatalf("build test-only request: %v", err)
	}
	result, err := cdb.ScheduleSourceCrawlRequest(context.Background(), handler, request)
	if err != nil {
		t.Fatalf("schedule test-only request: %v", err)
	}
	if result.State != cdb.CrawlOperationAccepted || result.ReasonCode != cdb.CrawlReasonScheduled {
		t.Fatalf("unexpected scheduling result: %+v", result)
	}
	status, operations := crawlSchedulingTestSourceState(t, handler, 42)
	if status != "pending" || operations != 1 {
		t.Fatalf("approved scheduling did not update state: status=%q operations=%d", status, operations)
	}
}

func TestCrawlProposalToolLoopTestOnlyApproval(t *testing.T) {
	probe := &crawlProposalProbe{}
	registry := NewAgentToolRegistry()
	if err := registry.Register(probe); err != nil {
		t.Fatalf("register probe: %v", err)
	}
	provider := &scriptedProvider{name: "scripted", responses: []any{
		chatWithCalls(stringCall("c1", "crawl_proposal_probe", `{"source_id":42}`)),
		chatWithText("done. An administrator approved this request."),
	}}
	config := loopTestConfig(provider, registry)
	config.ProviderName = "scripted"
	config.Model = "scripted"
	config.URL = "https://example.com/v1/chat"
	config.Allowlist = []string{"crawl_proposal_probe"}
	config.AllowedSources = []uint64{42}
	config.Identity = AgentIdentity{
		AgentID: "proposal-agent", Owner: "proposal-owner", TrustLevel: "trusted",
		Capabilities: []string{"tool_execution"},
	}
	config.Prompt = "propose source 42"
	config.DB = &panicOnReadDBHandler{}

	loopResult, err := RunToolLoop(config)
	if err != nil {
		t.Fatalf("tool loop: %v", err)
	}
	if !probe.executed || loopResult.ToolCalls[0].Status != "executed" {
		t.Fatalf("probe was not executed: %+v", loopResult.ToolCalls)
	}
	if !strings.Contains(strings.ToLower(loopResult.Content), "approved") {
		t.Fatalf("scripted model did not produce an approval claim: %q", loopResult.Content)
	}
	requestID, err := CrawlToolRequestID("run-live", loopResult.ToolCalls[0].ID, probe.sourceID)
	if err != nil {
		t.Fatalf("derive scripted request ID: %v", err)
	}
	proposal := CrawlRequestProposal{
		RequestID: requestID, SourceID: probe.sourceID,
		OperationType: cdb.CrawlOperationType, ExpiresAt: probe.expiresAt,
	}

	handler := newCrawlSchedulingTestDB(t)
	now := time.Now().UTC()
	identity := AgentIdentity{
		AgentID: "proposal-agent", Owner: "owner-1", TrustLevel: "trusted",
		Capabilities: []string{"tool_execution", "db_write"},
	}
	principal := CrawlPrincipal{UserID: 7, Username: "owner-1", Issuer: "crowler"}
	policy := validCrawlPolicy(nil)
	policy.Principal = principal
	policy.SourceAuthorizer = DatabaseCrawlSourceAuthorizer{
		Lookup: DatabaseSourceOwnerLookup{DB: handler},
	}

	// The scripted model's textual approval claim does not authorize anything.
	denied := authorizeCrawlRequestAt(context.Background(), identity, policy, proposal, nil, "run-live", "trace-live", now)
	if denied.Allowed() || denied.ReasonCode() != CrawlReasonApprovalRequired {
		t.Fatalf("textual approval claim authorized work: %+v", denied)
	}

	approver := CrawlPrincipal{UserID: 9, Username: "operator-1", Issuer: "crowler", Roles: []string{"admin"}}
	operatorSourceAuthorizer := DatabaseCrawlSourceAuthorizer{
		Lookup: DatabaseSourceOwnerLookup{DB: handler}, AllowAdmin: true,
	}
	store := &DatabaseCrawlApprovalStore{
		DB: handler, SourceAuthorizer: operatorSourceAuthorizer,
		Issuer: "test-authority", MaxLifetime: time.Hour,
	}
	approval, err := store.IssueCrawlApproval(context.Background(), CrawlApprovalIssuanceRequest{
		Proposal: proposal, Identity: identity, Principal: principal, RunID: "run-live",
		IssuerPrincipal: approver, Approver: approver, Issuer: "test-authority",
		ApprovedAt: now.Add(-time.Minute), ExpiresAt: now.Add(30 * time.Minute),
	})
	if err != nil {
		t.Fatalf("issue approval: %v", err)
	}
	policy.ApprovalVerifier = StoreBackedCrawlApprovalVerifier{
		Store: store, SourceAuthorizer: operatorSourceAuthorizer,
		TrustedIssuers: map[string]bool{"test-authority": true}, MaxLifetime: time.Hour,
	}
	decision := authorizeCrawlRequestAt(context.Background(), identity, policy, proposal, &approval, "run-live", "trace-live", now)
	if !decision.Allowed() {
		t.Fatalf("approved proposal denied: %+v", decision)
	}
	request, err := buildCrawlScheduleRequestAt(identity, proposal, approval, decision, "run-live", now)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	result, err := cdb.ScheduleSourceCrawlRequest(context.Background(), handler, request)
	if err != nil {
		t.Fatalf("schedule request: %v", err)
	}
	if result.State != cdb.CrawlOperationAccepted {
		t.Fatalf("unexpected scheduling result: %+v", result)
	}
	if status, operations := crawlSchedulingTestSourceState(t, handler, 42); status != "pending" || operations != 1 {
		t.Fatalf("scheduling state mismatch: status=%q operations=%d", status, operations)
	}
}

func TestCrawlProposalFactoryStableAcrossDuplicateDelivery(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	registry := NewMemoryCrawlProposalRegistryWithOptions(DefaultCrawlProposalRegistryCapacity, time.Hour, clock)
	factory := CrawlProposalFactory{Registry: registry, TTL: time.Hour, Now: clock}

	first, err := factory.Proposal(context.Background(), "run-1", "call-1", 42)
	if err != nil {
		t.Fatalf("first proposal: %v", err)
	}
	now = now.Add(10 * time.Minute)
	second, err := factory.Proposal(context.Background(), "run-1", "call-1", 42)
	if err != nil {
		t.Fatalf("duplicate proposal: %v", err)
	}
	if first.RequestID != second.RequestID || !first.ExpiresAt.Equal(second.ExpiresAt) {
		t.Fatalf("duplicate delivery changed proposal: %+v vs %+v", first, second)
	}
	if !first.ExpiresAt.Equal(time.Date(2026, 11, 8, 13, 0, 0, 0, time.UTC)) {
		t.Fatalf("unexpected first expiry: %v", first.ExpiresAt)
	}

	differentCall, err := factory.Proposal(context.Background(), "run-1", "call-2", 42)
	if err != nil {
		t.Fatalf("different call: %v", err)
	}
	if differentCall.RequestID == first.RequestID {
		t.Fatal("different tool call reused request ID")
	}
	differentSource, err := factory.Proposal(context.Background(), "run-1", "call-1", 43)
	if err != nil {
		t.Fatalf("different source: %v", err)
	}
	if differentSource.RequestID == first.RequestID {
		t.Fatal("different source reused request ID")
	}

	longFactory := CrawlProposalFactory{Registry: NewMemoryCrawlProposalRegistryWithOptions(DefaultCrawlProposalRegistryCapacity, 48*time.Hour, clock), TTL: 48 * time.Hour, Now: clock}
	long, err := longFactory.Proposal(context.Background(), "run-2", "call-1", 42)
	if err != nil {
		t.Fatalf("long proposal: %v", err)
	}
	if long.ExpiresAt.Sub(now) > maxCrawlProposalLifetime {
		t.Fatalf("proposal lifetime exceeded cap: %v", long.ExpiresAt.Sub(now))
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := factory.Proposal(ctx, "run-3", "call-1", 42); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled proposal error = %v", err)
	}
	nilFactory := CrawlProposalFactory{}
	if _, err := nilFactory.Proposal(context.Background(), "run-1", "call-1", 42); err == nil {
		t.Fatal("proposal without registry was accepted")
	}
}
