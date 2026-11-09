//go:build integration

package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func openCrawlApprovalPostgresDB(t *testing.T) *cdb.Handler {
	t.Helper()
	if os.Getenv("THECROWLER_POSTGRES_INTEGRATION") != "1" {
		t.Skip("set THECROWLER_POSTGRES_INTEGRATION=1 to run PostgreSQL integration tests")
	}
	port, _ := strconv.Atoi(func() string {
		if v := os.Getenv("DOCKER_POSTGRES_DB_PORT"); v != "" {
			return v
		}
		return "5432"
	}())
	databaseConfig := cfg.Config{Database: cfg.Database{
		Type:               "postgres",
		Host:               envOrCrawlApproval("DOCKER_POSTGRES_DB_HOST", "127.0.0.1"),
		Port:               port,
		User:               envOrCrawlApproval("DOCKER_POSTGRES_DB_USER", "postgres"),
		Password:           envOrCrawlApproval("DOCKER_POSTGRES_PASSWORD", "postgres"),
		DBName:             envOrCrawlApproval("DOCKER_POSTGRES_DB_NAME", "SitesIndex"),
		SSLMode:            envOrCrawlApproval("DOCKER_POSTGRES_SSL_MODE", "disable"),
		TransactionTimeout: 30,
	}}
	handler, err := cdb.NewHandler(databaseConfig)
	if err != nil {
		t.Fatalf("create postgres handler: %v", err)
	}
	if err := handler.Connect(databaseConfig); err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	if err := cdb.CheckStartupCompatibility(context.Background(), handler); err != nil {
		t.Fatalf("postgres compatibility: %v", err)
	}
	return &handler
}

func envOrCrawlApproval(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func setupCrawlApprovalPostgresUsers(t *testing.T, db *cdb.Handler) (ownerID, adminID uint64) {
	t.Helper()
	ownerID = uint64(990000 + time.Now().UnixNano()%10000)
	adminID = ownerID + 1
	ownerName := fmt.Sprintf("wp03b2b-owner-%d", ownerID)
	adminName := fmt.Sprintf("wp03b2b-admin-%d", adminID)
	if _, err := (*db).Exec(`INSERT INTO AuthRoles(name, description) VALUES
		('admin', 'test administrator'), ('user', 'test user')
		ON CONFLICT (name) DO NOTHING`); err != nil {
		t.Fatalf("ensure approval roles: %v", err)
	}
	if _, err := (*db).Exec(`INSERT INTO Users(user_id, username, disabled, deleted_at) VALUES
		($1, $2, FALSE, NULL), ($3, $4, FALSE, NULL)
		ON CONFLICT (user_id) DO UPDATE SET username=EXCLUDED.username, disabled=FALSE, deleted_at=NULL`,
		ownerID, ownerName, adminID, adminName); err != nil {
		t.Fatalf("insert users: %v", err)
	}
	if _, err := (*db).Exec(`INSERT INTO UserRoles(user_id, role_id)
		SELECT $1, role_id FROM AuthRoles WHERE name='user'
		ON CONFLICT DO NOTHING`, ownerID); err != nil {
		t.Fatalf("grant user role: %v", err)
	}
	if _, err := (*db).Exec(`INSERT INTO UserRoles(user_id, role_id)
		SELECT $1, role_id FROM AuthRoles WHERE name='admin'
		ON CONFLICT DO NOTHING`, adminID); err != nil {
		t.Fatalf("grant admin role: %v", err)
	}
	t.Cleanup(func() {
		_, _ = (*db).Exec(`DELETE FROM UserRoles WHERE user_id IN ($1,$2)`, ownerID, adminID)
		_, _ = (*db).Exec(`DELETE FROM UserScopes WHERE user_id IN ($1,$2)`, ownerID, adminID)
		_, _ = (*db).Exec(`DELETE FROM Users WHERE user_id IN ($1,$2)`, ownerID, adminID)
	})
	return ownerID, adminID
}

func createCrawlApprovalPostgresSource(t *testing.T, db *cdb.Handler, ownerID uint64) uint64 {
	t.Helper()
	source := &cdb.Source{
		URL:  fmt.Sprintf("https://wp03b2b-%d.example/", time.Now().UnixNano()),
		Name: "wp03b2b-approval", Priority: "high", Restricted: 1, UsrID: ownerID,
	}
	sourceID, err := cdb.CreateSourceContext(context.Background(), db, source, cfg.SourceConfig{})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	if _, err := (*db).Exec(`UPDATE Sources SET status='completed', last_updated_at=NOW() WHERE source_id=$1`, sourceID); err != nil {
		t.Fatalf("complete source: %v", err)
	}
	if _, err := (*db).Exec(`INSERT INTO SourceOwnership
		(source_id, user_id, verified_at, verified_by_user_id, verified_by, verification_method)
		VALUES ($1, $2, NOW(), $2, 'wp03b2b-fixture', 'admin_verification')
		ON CONFLICT (source_id) DO UPDATE SET user_id=EXCLUDED.user_id, verified_at=EXCLUDED.verified_at`, sourceID, ownerID); err != nil {
		t.Fatalf("record approval source ownership: %v", err)
	}
	t.Cleanup(func() {
		_, _ = (*db).Exec(`DELETE FROM Status WHERE source_id=$1`, sourceID)
		_, _ = (*db).Exec(`DELETE FROM CrawlProposals WHERE source_id=$1`, sourceID)
		_, _ = (*db).Exec(`DELETE FROM CrawlApprovals WHERE source_id=$1`, sourceID)
		_, _ = (*db).Exec(`DELETE FROM SourceOwnership WHERE source_id=$1`, sourceID)
		_, _ = (*db).Exec(`DELETE FROM Sources WHERE source_id=$1`, sourceID)
	})
	return sourceID
}

func postgresApprovalStore(db *cdb.Handler) *DatabaseCrawlApprovalStore {
	return &DatabaseCrawlApprovalStore{
		DB: db,
		SourceAuthorizer: DatabaseCrawlSourceAuthorizer{
			Lookup: DatabaseSourceOwnerLookup{DB: db}, AllowAdmin: true,
		},
		Issuer: "crowler-api", MaxLifetime: 24 * time.Hour,
	}
}

func postgresApprovalProposal(sourceID uint64, requestID string, now time.Time) CrawlRequestProposal {
	return CrawlRequestProposal{RequestID: requestID, SourceID: sourceID, OperationType: cdb.CrawlOperationType, ExpiresAt: now.Add(time.Hour)}
}

func postgresApprovalRequest(proposal CrawlRequestProposal, identity AgentIdentity, principal CrawlPrincipal, now time.Time) CrawlApprovalIssuanceRequest {
	return CrawlApprovalIssuanceRequest{
		Proposal: proposal, Identity: identity, Principal: principal, RunID: "wp03b2b-run",
		IssuerPrincipal: principal, Approver: principal, Issuer: "crowler-api",
		ApprovedAt: now, ExpiresAt: now.Add(30 * time.Minute),
	}
}

func postgresScheduleRequest(proposal CrawlRequestProposal, identity AgentIdentity, principal CrawlPrincipal, approval CrawlRequestApproval, now time.Time) cdb.CrawlScheduleRequest {
	expiresAt := proposal.ExpiresAt
	if approval.ExpiresAt.Before(expiresAt) {
		expiresAt = approval.ExpiresAt
	}
	return cdb.CrawlScheduleRequest{
		RequestID: proposal.RequestID, RunID: "wp03b2b-run", PrincipalUserID: principal.UserID,
		AgentID: identity.AgentID, Owner: identity.Owner, SourceID: proposal.SourceID,
		AllowedSources: []uint64{proposal.SourceID}, OperationType: proposal.OperationType,
		ParametersSHA256: proposal.Fingerprint(),
		ApprovalID:       approval.ApprovalID, ApprovalSHA256: approval.Fingerprint(),
		ApprovedBy: approval.ApprovedBy, ApprovedByUserID: approval.ApproverUserID,
		ApprovedAt: approval.ApprovedAt, ExpiresAt: expiresAt,
		MaxRequests: 10, BudgetWindow: time.Hour,
	}
}

func TestPostgresCrawlApprovalPersistenceAndScheduling(t *testing.T) {
	db := openCrawlApprovalPostgresDB(t)
	ownerID, adminID := setupCrawlApprovalPostgresUsers(t, db)
	sourceID := createCrawlApprovalPostgresSource(t, db, ownerID)
	now := time.Now().UTC()
	principal := CrawlPrincipal{UserID: ownerID, Username: fmt.Sprintf("wp03b2b-owner-%d", ownerID), Issuer: "crowler"}
	identity := AgentIdentity{AgentID: "wp03b2b-agent", Owner: principal.Username, TrustLevel: "trusted"}
	proposal := postgresApprovalProposal(sourceID, fmt.Sprintf("wp03b2b-request-%d", now.UnixNano()), now)
	store := postgresApprovalStore(db)

	approval, err := store.IssueCrawlApproval(context.Background(), postgresApprovalRequest(proposal, identity, principal, now))
	if err != nil {
		t.Fatalf("issue approval: %v", err)
	}
	if approval.ApproverUserID != ownerID {
		t.Fatalf("approver user ID = %d, want %d", approval.ApproverUserID, ownerID)
	}
	record, found, err := store.LookupCrawlApproval(context.Background(), approval.ApprovalID)
	if err != nil || !found {
		t.Fatalf("lookup approval: found=%v err=%v", found, err)
	}
	if !record.ApproverPrincipal.HasRole("user") || record.Revoked {
		t.Fatalf("unexpected approval record: %+v", record)
	}
	byRequest, found, err := store.LookupCrawlApprovalByRequest(context.Background(),
		"wp03b2b-run", proposal.RequestID, principal.UserID, sourceID, proposal.OperationType)
	if err != nil || !found || byRequest.Approval.ApprovalID != approval.ApprovalID {
		t.Fatalf("lookup by request: %+v found=%v err=%v", byRequest, found, err)
	}

	request := postgresScheduleRequest(proposal, identity, principal, approval, now)
	result, err := cdb.ScheduleSourceCrawlRequest(context.Background(), db, request)
	if err != nil || result.State != cdb.CrawlOperationAccepted {
		t.Fatalf("schedule: %+v err=%v", result, err)
	}

	if err := store.RevokeCrawlApproval(context.Background(), principal, approval.ApprovalID, "test", now); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	revoked, found, err := store.LookupCrawlApproval(context.Background(), approval.ApprovalID)
	if err != nil || !found || !revoked.Revoked {
		t.Fatalf("revoked lookup: %+v found=%v err=%v", revoked, found, err)
	}

	// A legitimate retry reuses the already-accepted operation even after the
	// approval is revoked.
	retry, err := cdb.ScheduleSourceCrawlRequest(context.Background(), db, request)
	if err != nil || retry.State != cdb.CrawlOperationAccepted || !retry.Reused {
		t.Fatalf("retry after revoke: %+v err=%v", retry, err)
	}

	// A new request ID cannot reuse the revoked approval.
	newProposal := proposal
	newProposal.RequestID = proposal.RequestID + "-new"
	newProposal.ExpiresAt = now.Add(time.Hour)
	reusedApprovalRequest := postgresScheduleRequest(newProposal, identity, principal, approval, now)
	denied, err := cdb.ScheduleSourceCrawlRequest(context.Background(), db, reusedApprovalRequest)
	if err != nil || denied.State != cdb.CrawlOperationDenied || denied.ReasonCode != cdb.CrawlReasonApprovalRejected {
		t.Fatalf("new request reused revoked approval: %+v err=%v", denied, err)
	}

	// An admin may issue for another owner's source.
	admin := CrawlPrincipal{UserID: adminID, Username: fmt.Sprintf("wp03b2b-admin-%d", adminID), Issuer: "crowler"}
	adminProposal := postgresApprovalProposal(sourceID, fmt.Sprintf("wp03b2b-admin-request-%d", now.UnixNano()), now)
	adminIdentity := AgentIdentity{AgentID: "wp03b2b-agent", Owner: admin.Username, TrustLevel: "trusted"}
	adminRequest := postgresApprovalRequest(adminProposal, adminIdentity, admin, now)
	adminRequest.IssuerPrincipal = admin
	adminRequest.Approver = admin
	approvedByAdmin, err := store.IssueCrawlApproval(context.Background(), adminRequest)
	if err != nil {
		t.Fatalf("admin issue: %v", err)
	}
	if approvedByAdmin.ApproverUserID != adminID {
		t.Fatalf("admin approval user ID = %d", approvedByAdmin.ApproverUserID)
	}
	// The console binds the authenticated operator as the run principal. An
	// administrator approving for their own principal cannot schedule another
	// owner's source; cross-owner run-principal binding awaits WP-03B2C.
	adminSchedule := postgresScheduleRequest(adminProposal, adminIdentity, admin, approvedByAdmin, now)
	denied, err = cdb.ScheduleSourceCrawlRequest(context.Background(), db, adminSchedule)
	if err != nil {
		t.Fatalf("admin schedule: %v", err)
	}
	if denied.State != cdb.CrawlOperationDenied || denied.ReasonCode != cdb.CrawlReasonOwnershipRejected {
		t.Fatalf("cross-owner admin principal was not denied ownership: %+v", denied)
	}
}

func TestPostgresCrawlApprovalSubmissionRevalidation(t *testing.T) {
	db := openCrawlApprovalPostgresDB(t)
	ownerID, _ := setupCrawlApprovalPostgresUsers(t, db)
	sourceID := createCrawlApprovalPostgresSource(t, db, ownerID)
	now := time.Now().UTC()
	principal := CrawlPrincipal{UserID: ownerID, Username: fmt.Sprintf("wp03b2b-owner-%d", ownerID), Issuer: "crowler"}
	identity := AgentIdentity{AgentID: "wp03b2b-agent", Owner: principal.Username, TrustLevel: "trusted"}
	store := postgresApprovalStore(db)

	t.Run("transferred source is rejected", func(t *testing.T) {
		proposal := postgresApprovalProposal(sourceID, fmt.Sprintf("wp03b2b-transfer-%d", now.UnixNano()), now)
		approval, err := store.IssueCrawlApproval(context.Background(), postgresApprovalRequest(proposal, identity, principal, now))
		if err != nil {
			t.Fatalf("issue: %v", err)
		}
		if _, err := (*db).Exec(`UPDATE Sources SET usr_id=$1 WHERE source_id=$2`, ownerID+1, sourceID); err != nil {
			t.Fatalf("transfer source: %v", err)
		}
		result, err := cdb.ScheduleSourceCrawlRequest(context.Background(), db, postgresScheduleRequest(proposal, identity, principal, approval, now))
		if err != nil || result.State != cdb.CrawlOperationDenied || result.ReasonCode != cdb.CrawlReasonOwnershipRejected {
			t.Fatalf("transferred source result: %+v err=%v", result, err)
		}
		if _, err := (*db).Exec(`UPDATE Sources SET usr_id=$1 WHERE source_id=$2`, ownerID, sourceID); err != nil {
			t.Fatalf("restore source owner: %v", err)
		}
	})

	t.Run("disabled source is not scheduled", func(t *testing.T) {
		proposal := postgresApprovalProposal(sourceID, fmt.Sprintf("wp03b2b-disabled-%d", now.UnixNano()), now)
		approval, err := store.IssueCrawlApproval(context.Background(), postgresApprovalRequest(proposal, identity, principal, now))
		if err != nil {
			t.Fatalf("issue: %v", err)
		}
		if _, err := (*db).Exec(`UPDATE Sources SET disabled=TRUE WHERE source_id=$1`, sourceID); err != nil {
			t.Fatalf("disable source: %v", err)
		}
		result, err := cdb.ScheduleSourceCrawlRequest(context.Background(), db, postgresScheduleRequest(proposal, identity, principal, approval, now))
		if err != nil || result.State != cdb.CrawlOperationDisabled {
			t.Fatalf("disabled source result: %+v err=%v", result, err)
		}
		if _, err := (*db).Exec(`UPDATE Sources SET disabled=FALSE, status='completed' WHERE source_id=$1`, sourceID); err != nil {
			t.Fatalf("re-enable source: %v", err)
		}
	})

	t.Run("concurrent revocation and submission are linearizable", func(t *testing.T) {
		if _, err := (*db).Exec(`UPDATE Sources SET status='completed', disabled=FALSE WHERE source_id=$1`, sourceID); err != nil {
			t.Fatalf("reset source: %v", err)
		}
		proposal := postgresApprovalProposal(sourceID, fmt.Sprintf("wp03b2b-concurrent-%d", now.UnixNano()), now)
		approval, err := store.IssueCrawlApproval(context.Background(), postgresApprovalRequest(proposal, identity, principal, now))
		if err != nil {
			t.Fatalf("issue: %v", err)
		}
		request := postgresScheduleRequest(proposal, identity, principal, approval, now)
		start := make(chan struct{})
		var wg sync.WaitGroup
		var scheduleResult cdb.CrawlScheduleResult
		var scheduleErr error
		var revokeErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			scheduleResult, scheduleErr = cdb.ScheduleSourceCrawlRequest(context.Background(), db, request)
		}()
		go func() {
			defer wg.Done()
			<-start
			revokeErr = store.RevokeCrawlApproval(context.Background(), principal, approval.ApprovalID, "concurrent", now)
		}()
		close(start)
		wg.Wait()
		if scheduleErr != nil || revokeErr != nil {
			t.Fatalf("concurrent schedule err=%v revoke err=%v", scheduleErr, revokeErr)
		}
		if scheduleResult.State != cdb.CrawlOperationAccepted && scheduleResult.State != cdb.CrawlOperationDenied {
			t.Fatalf("unexpected concurrent schedule state: %+v", scheduleResult)
		}
		final, found, err := store.LookupCrawlApproval(context.Background(), approval.ApprovalID)
		if err != nil || !found || !final.Revoked {
			t.Fatalf("approval not revoked after concurrent test: %+v found=%v err=%v", final, found, err)
		}
		if scheduleResult.State == cdb.CrawlOperationDenied && scheduleResult.ReasonCode != cdb.CrawlReasonApprovalRejected {
			t.Fatalf("concurrent denial reason: %+v", scheduleResult)
		}
	})
}

func TestPostgresCrawlApprovalRollback(t *testing.T) {
	db := openCrawlApprovalPostgresDB(t)
	ownerID, _ := setupCrawlApprovalPostgresUsers(t, db)
	sourceID := createCrawlApprovalPostgresSource(t, db, ownerID)
	now := time.Now().UTC()
	principal := CrawlPrincipal{UserID: ownerID, Username: fmt.Sprintf("wp03b2b-owner-%d", ownerID), Issuer: "crowler"}
	identity := AgentIdentity{AgentID: "wp03b2b-agent", Owner: principal.Username, TrustLevel: "trusted"}
	store := postgresApprovalStore(db)
	proposal := postgresApprovalProposal(sourceID, fmt.Sprintf("wp03b2b-rollback-%d", now.UnixNano()), now)
	approval, err := store.IssueCrawlApproval(context.Background(), postgresApprovalRequest(proposal, identity, principal, now))
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	constraint := fmt.Sprintf("wp03b2b_fail_pending_%d", sourceID)
	if _, err := (*db).Exec(`ALTER TABLE Sources ADD CONSTRAINT ` + constraint +
		` CHECK (source_id <> ` + fmt.Sprintf("%d", sourceID) + ` OR status <> 'pending')`); err != nil {
		t.Fatalf("create rollback constraint: %v", err)
	}
	t.Cleanup(func() {
		_, _ = (*db).Exec(`ALTER TABLE Sources DROP CONSTRAINT IF EXISTS ` + constraint)
	})
	result, err := cdb.ScheduleSourceCrawlRequest(context.Background(), db, postgresScheduleRequest(proposal, identity, principal, approval, now))
	if err == nil || result.State != cdb.CrawlOperationFailed {
		t.Fatalf("rollback schedule: %+v err=%v", result, err)
	}
	var status string
	var operations int
	if err := (*db).QueryRow(`SELECT status FROM Sources WHERE source_id=$1`, sourceID).Scan(&status); err != nil {
		t.Fatalf("read source: %v", err)
	}
	if err := (*db).QueryRow(`SELECT COUNT(*) FROM Status WHERE source_id=$1`, sourceID).Scan(&operations); err != nil {
		t.Fatalf("count operations: %v", err)
	}
	if status != "completed" || operations != 0 {
		t.Fatalf("rollback left status=%q operations=%d", status, operations)
	}
}

func TestPostgresCrawlApprovalConcurrentRevocationAuthorization(t *testing.T) {
	db := openCrawlApprovalPostgresDB(t)
	ownerID, _ := setupCrawlApprovalPostgresUsers(t, db)
	otherID := ownerID + 2
	otherName := fmt.Sprintf("wp03b2b-other-%d", otherID)
	if _, err := (*db).Exec(`INSERT INTO Users(user_id, username, disabled, deleted_at)
		VALUES ($1, $2, FALSE, NULL)
		ON CONFLICT (user_id) DO UPDATE SET username=EXCLUDED.username, disabled=FALSE, deleted_at=NULL`, otherID, otherName); err != nil {
		t.Fatalf("insert other user: %v", err)
	}
	if _, err := (*db).Exec(`INSERT INTO UserRoles(user_id, role_id)
		SELECT $1, role_id FROM AuthRoles WHERE name='user'
		ON CONFLICT DO NOTHING`, otherID); err != nil {
		t.Fatalf("grant other user role: %v", err)
	}
	t.Cleanup(func() {
		_, _ = (*db).Exec(`DELETE FROM UserRoles WHERE user_id=$1`, otherID)
		_, _ = (*db).Exec(`DELETE FROM UserScopes WHERE user_id=$1`, otherID)
		_, _ = (*db).Exec(`DELETE FROM Users WHERE user_id=$1`, otherID)
	})

	sourceID := createCrawlApprovalPostgresSource(t, db, ownerID)
	now := time.Now().UTC()
	owner := CrawlPrincipal{UserID: ownerID, Username: fmt.Sprintf("wp03b2b-owner-%d", ownerID), Issuer: "crowler"}
	other := CrawlPrincipal{UserID: otherID, Username: otherName, Issuer: "crowler"}
	identity := AgentIdentity{AgentID: "wp03b2b-revoke-agent", Owner: owner.Username, TrustLevel: "trusted"}
	proposal := postgresApprovalProposal(sourceID, fmt.Sprintf("wp03b2b-revoke-%d", now.UnixNano()), now)
	store := postgresApprovalStore(db)
	approval, err := store.IssueCrawlApproval(context.Background(), postgresApprovalRequest(proposal, identity, owner, now))
	if err != nil {
		t.Fatalf("issue approval: %v", err)
	}

	type outcome struct {
		principal string
		err       error
	}
	results := make(chan outcome, 2)
	var wg sync.WaitGroup
	for _, principal := range []CrawlPrincipal{owner, other} {
		principal := principal
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := store.RevokeCrawlApproval(context.Background(), principal, approval.ApprovalID, principal.Username+" request", now)
			results <- outcome{principal: principal.Username, err: err}
		}()
	}
	wg.Wait()
	close(results)
	ownerSuccess, otherDenied := false, false
	for result := range results {
		switch result.principal {
		case owner.Username:
			ownerSuccess = result.err == nil
		case other.Username:
			otherDenied = errors.Is(result.err, ErrCrawlApprovalApproverUnauthorized)
		}
	}
	if !ownerSuccess || !otherDenied {
		t.Fatalf("concurrent revocation outcomes ownerSuccess=%v otherDenied=%v", ownerSuccess, otherDenied)
	}
	raw, found, err := cdb.GetCrawlApprovalByID(context.Background(), db, approval.ApprovalID)
	if err != nil || !found || !raw.RevokedAt.Valid || raw.RevokedByUserID.Int64 != int64(ownerID) {
		t.Fatalf("unexpected concurrent revocation metadata: %+v found=%v err=%v", raw, found, err)
	}
}

func TestPostgresCrawlProposalApprovalSubmissionChain(t *testing.T) {
	db := openCrawlApprovalPostgresDB(t)
	ownerID, _ := setupCrawlApprovalPostgresUsers(t, db)
	sourceID := createCrawlApprovalPostgresSource(t, db, ownerID)
	ownerName := fmt.Sprintf("wp03b2b-owner-%d", ownerID)
	now := time.Now().UTC()
	identity := AgentIdentity{
		AgentID: "wp03b2c-agent", Owner: ownerName, TrustLevel: "trusted",
		Capabilities: []string{"tool_execution", "db_write"},
	}
	runtime, err := BuildCrawlRequestToolRuntime(context.Background(), CrawlRequestToolRuntimeConfig{
		Enabled: true, DB: *db, Identity: identity,
		AllowedOperations: []string{crawlRequestToolName},
		AllowedSources:    []uint64{sourceID},
		MaxRequests:       2, BudgetWindow: time.Hour,
		Issuer: "crowler-api", ProposalTTL: 2 * time.Minute,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("build postgres crawl runtime: %v", err)
	}
	tool := &crawlRequestTool{runtime: runtime}
	ctx := ContextWithToolRuntime(context.Background(), ToolRuntime{
		DB: *db, CallID: "pg-call-1", Crawl: runtime,
		Auth: ToolAuthContext{
			Identity: identity, Enforcement: true,
			Allowlist:         map[string]bool{crawlRequestToolName: true},
			AllowedSources:    map[uint64]bool{sourceID: true},
			AllowedOperations: map[string]bool{crawlRequestToolName: true},
			MutationAllowed:   true, RunID: "pg-run-1", TraceID: "pg-trace-1",
		},
	})
	result, err := tool.Execute(ctx, map[string]any{"source_id": float64(sourceID)})
	if err != nil {
		t.Fatalf("postgres crawl proposal: %v", err)
	}
	requestID := result["request_id"].(string)
	row, found, err := cdb.GetCrawlProposalByRequest(context.Background(), db, requestID)
	if err != nil || !found || row.State != cdb.CrawlProposalStatePending {
		t.Fatalf("postgres proposal not durable: %+v found=%v err=%v", row, found, err)
	}
	var operations int
	if err := (*db).QueryRow(`SELECT COUNT(*) FROM Status WHERE source_id=$1`, sourceID).Scan(&operations); err != nil {
		t.Fatalf("count postgres operations before approval: %v", err)
	}
	if operations != 0 {
		t.Fatalf("postgres proposal scheduled %d operations", operations)
	}

	store := runtime.ApprovalStore.(*DatabaseCrawlApprovalStore)
	approval, err := store.IssueCrawlApproval(context.Background(), CrawlApprovalIssuanceRequest{
		Proposal: CrawlRequestProposal{
			RequestID: row.RequestID, SourceID: row.SourceID,
			OperationType: row.OperationType, ExpiresAt: row.ExpiresAt,
		},
		Identity: identity, Principal: runtime.Policy.Principal,
		RunID: row.RunID, IssuerPrincipal: runtime.Policy.Principal,
		Approver: runtime.Policy.Principal, Issuer: "crowler-api",
		ApprovedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatalf("issue postgres approval: %v", err)
	}
	submitted, err := runtime.SubmitApprovedProposal(context.Background(), requestID, approval.ApprovalID, runtime.Policy.Principal)
	if err != nil {
		t.Fatalf("submit postgres approved proposal: %v", err)
	}
	if submitted.State != cdb.CrawlOperationAccepted {
		t.Fatalf("postgres submission state = %q: %+v", submitted.State, submitted)
	}
	var status string
	if err := (*db).QueryRow(`SELECT status FROM Sources WHERE source_id=$1`, sourceID).Scan(&status); err != nil {
		t.Fatalf("read postgres source status: %v", err)
	}
	if err := (*db).QueryRow(`SELECT COUNT(*) FROM Status WHERE source_id=$1`, sourceID).Scan(&operations); err != nil {
		t.Fatalf("count postgres operations: %v", err)
	}
	if status != "pending" || operations != 1 {
		t.Fatalf("postgres scheduling result status=%q operations=%d", status, operations)
	}
	repeat, err := runtime.SubmitApprovedProposal(context.Background(), requestID, approval.ApprovalID, runtime.Policy.Principal)
	if err != nil || !repeat.Reused {
		t.Fatalf("postgres repeat submission not idempotent: %+v err=%v", repeat, err)
	}
	if err := (*db).QueryRow(`SELECT COUNT(*) FROM Status WHERE source_id=$1`, sourceID).Scan(&operations); err != nil {
		t.Fatalf("count postgres repeat operations: %v", err)
	}
	if operations != 1 {
		t.Fatalf("postgres repeat created %d operations", operations)
	}
	if err := store.RevokeCrawlApproval(context.Background(), runtime.Policy.Principal, approval.ApprovalID, "postgres test", time.Now().UTC()); err != nil {
		t.Fatalf("revoke postgres approval: %v", err)
	}
	if _, err := runtime.SubmitApprovedProposal(context.Background(), requestID, approval.ApprovalID, runtime.Policy.Principal); !errors.Is(err, ErrCrawlApprovalRevoked) {
		t.Fatalf("postgres revoked submission error = %v", err)
	}
}
