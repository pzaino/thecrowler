package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func newDatabaseApprovalStore(t *testing.T) (*DatabaseCrawlApprovalStore, CrawlSourceAuthorizer) {
	t.Helper()
	handler := newCrawlSchedulingTestDB(t)
	authorizer := DatabaseCrawlSourceAuthorizer{
		Lookup:     DatabaseSourceOwnerLookup{DB: handler},
		AllowAdmin: true,
	}
	return &DatabaseCrawlApprovalStore{
		DB:               handler,
		SourceAuthorizer: authorizer,
		Issuer:           "crowler-api",
		MaxLifetime:      time.Hour,
	}, authorizer
}

func databaseApprovalRequest(now time.Time) CrawlApprovalIssuanceRequest {
	proposal := CrawlRequestProposal{
		RequestID: "database-approval-request-1", SourceID: 42,
		OperationType: cdb.CrawlOperationType, ExpiresAt: now.Add(time.Hour),
	}
	principal := CrawlPrincipal{UserID: 7, Username: "owner-1", Issuer: "crowler", Roles: []string{"user"}}
	return CrawlApprovalIssuanceRequest{
		Proposal:        proposal,
		Identity:        AgentIdentity{AgentID: "agent-1", Owner: "owner-1", TrustLevel: "trusted"},
		Principal:       principal,
		RunID:           "run-1",
		IssuerPrincipal: principal,
		Approver:        principal,
		Issuer:          "crowler-api",
		ApprovedAt:      now,
		ExpiresAt:       now.Add(30 * time.Minute),
	}
}

func TestDatabaseCrawlApprovalStoreIssueLookupRevoke(t *testing.T) {
	store, _ := newDatabaseApprovalStore(t)
	now := time.Now().UTC()
	request := databaseApprovalRequest(now)
	approval, err := store.IssueCrawlApproval(context.Background(), request)
	if err != nil {
		t.Fatalf("issue approval: %v", err)
	}
	if len(approval.ApprovalID) != 64 || approval.ApprovedBy != "owner-1" || approval.Issuer != "crowler-api" {
		t.Fatalf("unexpected approval: %+v", approval)
	}

	record, found, err := store.LookupCrawlApproval(context.Background(), approval.ApprovalID)
	if err != nil || !found {
		t.Fatalf("lookup approval: found=%v err=%v", found, err)
	}
	if record.Approval.ProposalSHA256 != request.Proposal.Fingerprint() ||
		record.Proposal.RequestID != request.Proposal.RequestID ||
		record.ApproverPrincipal.UserID != 7 ||
		record.IssuerPrincipal.UserID != 7 ||
		record.Revoked {
		t.Fatalf("unexpected record: %+v", record)
	}
	byRequest, found, err := store.LookupCrawlApprovalByRequest(context.Background(),
		request.RunID, request.Proposal.RequestID, request.Principal.UserID, request.Proposal.SourceID, request.Proposal.OperationType)
	if err != nil || !found || byRequest.Approval.ApprovalID != approval.ApprovalID {
		t.Fatalf("lookup by request: %+v found=%v err=%v", byRequest, found, err)
	}

	duplicate, err := store.IssueCrawlApproval(context.Background(), request)
	if err != nil {
		t.Fatalf("duplicate issue: %v", err)
	}
	if duplicate.ApprovalID != approval.ApprovalID {
		t.Fatalf("duplicate issue created a new approval: %s vs %s", duplicate.ApprovalID, approval.ApprovalID)
	}

	if err := store.RevokeCrawlApproval(context.Background(), request.Principal, approval.ApprovalID, "test", now); err != nil {
		t.Fatalf("revoke approval: %v", err)
	}
	revoked, found, err := store.LookupCrawlApproval(context.Background(), approval.ApprovalID)
	if err != nil || !found || !revoked.Revoked || revoked.RevokedAt == nil {
		t.Fatalf("revoked record: %+v found=%v err=%v", revoked, found, err)
	}
	if _, err := store.IssueCrawlApproval(context.Background(), request); !errors.Is(err, ErrCrawlApprovalRevoked) {
		t.Fatalf("reissue after revoke = %v", err)
	}
}

func TestDatabaseCrawlApprovalStoreAuthorizationAndConflict(t *testing.T) {
	store, _ := newDatabaseApprovalStore(t)
	now := time.Now().UTC()
	request := databaseApprovalRequest(now)
	approval, err := store.IssueCrawlApproval(context.Background(), request)
	if err != nil {
		t.Fatalf("issue approval: %v", err)
	}

	admin := CrawlPrincipal{UserID: 9, Username: "admin-1", Issuer: "crowler", Roles: []string{"admin"}}
	changed := request
	changed.Proposal.ExpiresAt = now.Add(2 * time.Hour)
	if _, err := store.IssueCrawlApproval(context.Background(), changed); !errors.Is(err, ErrCrawlApprovalConflict) {
		t.Fatalf("changed proposal error = %v", err)
	}
	differentIssuer := request
	differentIssuer.IssuerPrincipal = admin
	if _, err := store.IssueCrawlApproval(context.Background(), differentIssuer); !errors.Is(err, ErrCrawlApprovalConflict) {
		t.Fatalf("changed issuer error = %v", err)
	}

	unauthorized := request
	unauthorized.Approver = CrawlPrincipal{UserID: 8, Username: "owner-2", Issuer: "crowler", Roles: []string{"user"}}
	unauthorized.IssuerPrincipal = unauthorized.Approver
	if _, err := store.IssueCrawlApproval(context.Background(), unauthorized); !errors.Is(err, ErrCrawlApprovalApproverUnauthorized) {
		t.Fatalf("unauthorized approval error = %v", err)
	}

	adminRequest := request
	adminRequest.Proposal.RequestID = "database-approval-request-admin"
	adminRequest.Proposal.ExpiresAt = now.Add(time.Hour)
	adminRequest.Approver = admin
	adminRequest.IssuerPrincipal = admin
	if _, err := store.IssueCrawlApproval(context.Background(), adminRequest); err != nil {
		t.Fatalf("admin approval failed: %v", err)
	}

	if err := store.RevokeCrawlApproval(context.Background(), CrawlPrincipal{UserID: 8, Username: "owner-2", Issuer: "crowler"}, approval.ApprovalID, "denied", now); !errors.Is(err, ErrCrawlApprovalApproverUnauthorized) {
		t.Fatalf("unauthorized revoke error = %v", err)
	}
	if err := store.RevokeCrawlApproval(context.Background(), admin, approval.ApprovalID, "admin revoke", now); err != nil {
		t.Fatalf("admin revoke failed: %v", err)
	}
}

func TestDatabaseCrawlApprovalStoreLifetimeAndSchema(t *testing.T) {
	store, _ := newDatabaseApprovalStore(t)
	now := time.Now().UTC()
	for name, mutate := range map[string]func(*CrawlApprovalIssuanceRequest){
		"expiry before issuance": func(r *CrawlApprovalIssuanceRequest) { r.ExpiresAt = r.ApprovedAt.Add(-time.Minute) },
		"oversized lifetime":     func(r *CrawlApprovalIssuanceRequest) { r.ExpiresAt = r.ApprovedAt.Add(2 * time.Hour) },
		"expired approval": func(r *CrawlApprovalIssuanceRequest) {
			r.ApprovedAt = now.Add(-2 * time.Hour)
			r.ExpiresAt = now.Add(-time.Hour)
		},
	} {
		t.Run(name, func(t *testing.T) {
			request := databaseApprovalRequest(now)
			mutate(&request)
			if _, err := store.IssueCrawlApproval(context.Background(), request); !errors.Is(err, ErrCrawlApprovalLifetime) {
				t.Fatalf("got %v, want ErrCrawlApprovalLifetime", err)
			}
		})
	}

	t.Run("expired proposal", func(t *testing.T) {
		request := databaseApprovalRequest(now)
		request.Proposal.ExpiresAt = now.Add(-time.Minute)
		if _, err := store.IssueCrawlApproval(context.Background(), request); !errors.Is(err, ErrCrawlApprovalForged) {
			t.Fatalf("expired proposal error = %v", err)
		}
	})

	empty := &DatabaseCrawlApprovalStore{}
	if _, err := empty.IssueCrawlApproval(context.Background(), databaseApprovalRequest(now)); !errors.Is(err, ErrCrawlApprovalStoreUnavailable) {
		t.Fatalf("empty store error = %v", err)
	}
}

func TestDatabaseCrawlApprovalStoreRejectsInvalidIdentityBinding(t *testing.T) {
	store, _ := newDatabaseApprovalStore(t)
	now := time.Now().UTC()
	request := databaseApprovalRequest(now)
	request.Principal.Username = "owner-2"
	if _, err := store.IssueCrawlApproval(context.Background(), request); !errors.Is(err, ErrCrawlApprovalForged) {
		t.Fatalf("principal/owner mismatch error = %v", err)
	}
	request = databaseApprovalRequest(now)
	request.Issuer = "other-issuer"
	if _, err := store.IssueCrawlApproval(context.Background(), request); !errors.Is(err, ErrCrawlApprovalIssuerUntrusted) {
		t.Fatalf("untrusted issuer error = %v", err)
	}
	request = databaseApprovalRequest(now)
	request.RunID = ""
	if _, err := store.IssueCrawlApproval(context.Background(), request); !errors.Is(err, ErrCrawlApprovalForged) {
		t.Fatalf("missing run error = %v", err)
	}
	if !strings.HasPrefix(databaseApprovalRequest(now).RunID, "run-") {
		t.Fatal("test fixture changed unexpectedly")
	}
}

func TestDatabaseCrawlApprovalStoreRevokesWhenApproverDisabled(t *testing.T) {
	store, _ := newDatabaseApprovalStore(t)
	now := time.Now().UTC()
	request := databaseApprovalRequest(now)
	approval, err := store.IssueCrawlApproval(context.Background(), request)
	if err != nil {
		t.Fatalf("issue approval: %v", err)
	}
	if _, err := (*store.DB).Exec(`UPDATE Users SET disabled=TRUE WHERE user_id=7`); err != nil {
		t.Fatalf("disable approver: %v", err)
	}
	t.Cleanup(func() { _, _ = (*store.DB).Exec(`UPDATE Users SET disabled=FALSE WHERE user_id=7`) })
	admin := CrawlPrincipal{UserID: 9, Username: "admin-1", Issuer: "crowler", Roles: []string{"admin"}}
	if err := store.RevokeCrawlApproval(context.Background(), admin, approval.ApprovalID, "approver disabled", now); err != nil {
		t.Fatalf("admin revoke after approver disable: %v", err)
	}
	raw, found, err := store.LookupCrawlApprovalRaw(context.Background(), approval.ApprovalID)
	if err != nil || !found || !raw.Revoked {
		t.Fatalf("raw revoked approval: %+v found=%v err=%v", raw, found, err)
	}
}

func TestDatabaseCrawlApprovalStoreRejectsUnverifiedSource(t *testing.T) {
	store, _ := newDatabaseApprovalStore(t)
	now := time.Now().UTC()
	if _, err := (*store.DB).Exec(`DELETE FROM SourceOwnership WHERE source_id=42`); err != nil {
		t.Fatalf("remove source provenance: %v", err)
	}
	if _, err := store.IssueCrawlApproval(context.Background(), databaseApprovalRequest(now)); !errors.Is(err, ErrCrawlApprovalApproverUnauthorized) {
		t.Fatalf("unverified source approval error = %v", err)
	}
	var count int
	if err := (*store.DB).QueryRow(`SELECT COUNT(*) FROM CrawlApprovals`).Scan(&count); err != nil {
		t.Fatalf("count approvals: %v", err)
	}
	if count != 0 {
		t.Fatalf("unverified source persisted %d approvals", count)
	}
}

func TestDatabaseCrawlApprovalStoreRestartSafeProposalFingerprint(t *testing.T) {
	store, _ := newDatabaseApprovalStore(t)
	now := time.Now().UTC()
	request := databaseApprovalRequest(now)
	request.Proposal.ExpiresAt = now.Add(time.Hour + 321*time.Nanosecond)
	approval, err := store.IssueCrawlApproval(context.Background(), request)
	if err != nil {
		t.Fatalf("issue approval with sub-microsecond proposal expiry: %v", err)
	}
	record, found, err := store.LookupCrawlApprovalByRequest(context.Background(),
		request.RunID, request.Proposal.RequestID, request.Principal.UserID,
		request.Proposal.SourceID, request.Proposal.OperationType)
	if err != nil || !found {
		t.Fatalf("lookup by request: found=%v err=%v", found, err)
	}
	if record.Proposal.Fingerprint() != approval.ProposalSHA256 {
		t.Fatalf("restart-safe proposal fingerprint mismatch: %s vs %s",
			record.Proposal.Fingerprint(), approval.ProposalSHA256)
	}
}

func TestDatabaseCrawlApprovalStoreRepeatedRevocationRequiresAuthorization(t *testing.T) {
	store, _ := newDatabaseApprovalStore(t)
	now := time.Now().UTC()
	request := databaseApprovalRequest(now)
	approval, err := store.IssueCrawlApproval(context.Background(), request)
	if err != nil {
		t.Fatalf("issue approval: %v", err)
	}
	owner := request.Principal
	other := CrawlPrincipal{UserID: 8, Username: "owner-2", Issuer: "crowler", Roles: []string{"user"}}
	if err := store.RevokeCrawlApproval(context.Background(), owner, approval.ApprovalID, "owner request", now); err != nil {
		t.Fatalf("owner revoke: %v", err)
	}
	raw, found, err := cdb.GetCrawlApprovalByID(context.Background(), store.DB, approval.ApprovalID)
	if err != nil || !found {
		t.Fatalf("read revoked approval: found=%v err=%v", found, err)
	}
	originalRevokedAt, originalRevokedBy, originalReason := raw.RevokedAt, raw.RevokedByUserID, raw.RevocationReason
	if err := store.RevokeCrawlApproval(context.Background(), owner, approval.ApprovalID, "owner repeat", now); err != nil {
		t.Fatalf("authorized repeat revoke: %v", err)
	}
	afterOwner, found, err := cdb.GetCrawlApprovalByID(context.Background(), store.DB, approval.ApprovalID)
	if err != nil || !found {
		t.Fatalf("read after owner repeat: found=%v err=%v", found, err)
	}
	if afterOwner.RevokedAt != originalRevokedAt || afterOwner.RevokedByUserID != originalRevokedBy ||
		afterOwner.RevocationReason != originalReason {
		t.Fatalf("authorized repeat changed revocation metadata: before=%+v after=%+v", raw, afterOwner)
	}
	if err := store.RevokeCrawlApproval(context.Background(), other, approval.ApprovalID, "unauthorized repeat", now); !errors.Is(err, ErrCrawlApprovalApproverUnauthorized) {
		t.Fatalf("unauthorized repeat error = %v", err)
	}
	afterOther, found, err := cdb.GetCrawlApprovalByID(context.Background(), store.DB, approval.ApprovalID)
	if err != nil || !found {
		t.Fatalf("read after unauthorized repeat: found=%v err=%v", found, err)
	}
	if afterOther.RevokedAt != originalRevokedAt || afterOther.RevokedByUserID != originalRevokedBy ||
		afterOther.RevocationReason != originalReason {
		t.Fatalf("unauthorized repeat changed revocation metadata: before=%+v after=%+v", raw, afterOther)
	}
}

func TestDatabaseCrawlApprovalStoreRevocationRequiresCurrentPrivilege(t *testing.T) {
	store, _ := newDatabaseApprovalStore(t)
	now := time.Now().UTC()
	request := databaseApprovalRequest(now)
	approval, err := store.IssueCrawlApproval(context.Background(), request)
	if err != nil {
		t.Fatalf("issue approval: %v", err)
	}
	admin := CrawlPrincipal{UserID: 9, Username: "admin-1", Issuer: "crowler", Roles: []string{"admin"}}
	if err := store.RevokeCrawlApproval(context.Background(), admin, approval.ApprovalID, "admin request", now); err != nil {
		t.Fatalf("admin revoke: %v", err)
	}
	raw, found, err := cdb.GetCrawlApprovalByID(context.Background(), store.DB, approval.ApprovalID)
	if err != nil || !found {
		t.Fatalf("read admin-revoked approval: found=%v err=%v", found, err)
	}
	if _, err := (*store.DB).Exec(`DELETE FROM UserRoles WHERE user_id=9`); err != nil {
		t.Fatalf("revoke administrator grant: %v", err)
	}
	if err := store.RevokeCrawlApproval(context.Background(), admin, approval.ApprovalID, "revoked admin repeat", now); !errors.Is(err, ErrCrawlApprovalApproverUnauthorized) {
		t.Fatalf("demoted admin repeat error = %v", err)
	}
	after, found, err := cdb.GetCrawlApprovalByID(context.Background(), store.DB, approval.ApprovalID)
	if err != nil || !found {
		t.Fatalf("read after demoted admin: found=%v err=%v", found, err)
	}
	if after.RevokedAt != raw.RevokedAt || after.RevokedByUserID != raw.RevokedByUserID ||
		after.RevocationReason != raw.RevocationReason {
		t.Fatalf("demoted admin changed revocation metadata: before=%+v after=%+v", raw, after)
	}
}

func TestDatabaseCrawlApprovalStoreRevocationRejectsDisabledOwner(t *testing.T) {
	store, _ := newDatabaseApprovalStore(t)
	now := time.Now().UTC()
	request := databaseApprovalRequest(now)
	approval, err := store.IssueCrawlApproval(context.Background(), request)
	if err != nil {
		t.Fatalf("issue approval: %v", err)
	}
	owner := request.Principal
	if err := store.RevokeCrawlApproval(context.Background(), owner, approval.ApprovalID, "owner request", now); err != nil {
		t.Fatalf("owner revoke: %v", err)
	}
	raw, found, err := cdb.GetCrawlApprovalByID(context.Background(), store.DB, approval.ApprovalID)
	if err != nil || !found {
		t.Fatalf("read revoked approval: found=%v err=%v", found, err)
	}
	if _, err := (*store.DB).Exec(`UPDATE Users SET disabled=TRUE WHERE user_id=7`); err != nil {
		t.Fatalf("disable owner: %v", err)
	}
	if err := store.RevokeCrawlApproval(context.Background(), owner, approval.ApprovalID, "disabled repeat", now); !errors.Is(err, ErrCrawlApprovalApproverUnauthorized) {
		t.Fatalf("disabled owner repeat error = %v", err)
	}
	after, found, err := cdb.GetCrawlApprovalByID(context.Background(), store.DB, approval.ApprovalID)
	if err != nil || !found {
		t.Fatalf("read after disabled owner: found=%v err=%v", found, err)
	}
	if after.RevokedAt != raw.RevokedAt || after.RevokedByUserID != raw.RevokedByUserID ||
		after.RevocationReason != raw.RevocationReason {
		t.Fatalf("disabled owner changed revocation metadata: before=%+v after=%+v", raw, after)
	}
}

func TestDatabaseCrawlApprovalStoreRejectsUnknownRevocation(t *testing.T) {
	store, _ := newDatabaseApprovalStore(t)
	owner := CrawlPrincipal{UserID: 7, Username: "owner-1", Issuer: "crowler", Roles: []string{"user"}}
	if err := store.RevokeCrawlApproval(context.Background(), owner, strings.Repeat("d", 64), "unknown", time.Now().UTC()); !errors.Is(err, ErrCrawlApprovalUnknown) {
		t.Fatalf("unknown revocation error = %v", err)
	}
}
