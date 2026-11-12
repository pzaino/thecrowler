//go:build integration

package database

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
)

const (
	mcpPostgresDisposableEnv = "CROWLER_MCP_POSTGRES_TEST_DISPOSABLE"
	mcpPostgresDSNEnv        = "CROWLER_MCP_POSTGRES_DSN"
)

var (
	errMCPPostgresDisposableOptInRequired = errors.New("MCP PostgreSQL integration tests require " + mcpPostgresDisposableEnv + "=1")
	errMCPPostgresDSNRequired             = errors.New("MCP PostgreSQL integration tests require " + mcpPostgresDSNEnv)
)

// mcpPostgresDisposableTarget returns the explicitly opted-in PostgreSQL DSN.
// A DSN alone is never enough to authorize destructive test setup.
func mcpPostgresDisposableTarget() (*url.URL, error) {
	if strings.TrimSpace(os.Getenv(mcpPostgresDisposableEnv)) != "1" {
		return nil, errMCPPostgresDisposableOptInRequired
	}
	raw := strings.TrimSpace(os.Getenv(mcpPostgresDSNEnv))
	if raw == "" {
		return nil, errMCPPostgresDSNRequired
	}
	target, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid %s: %w", mcpPostgresDSNEnv, err)
	}
	if target.Scheme != "postgres" && target.Scheme != "postgresql" {
		return nil, fmt.Errorf("%s must be a postgres:// URL", mcpPostgresDSNEnv)
	}
	if strings.TrimSpace(target.Host) == "" {
		return nil, fmt.Errorf("%s must include a host", mcpPostgresDSNEnv)
	}
	return target, nil
}

func mcpPostgresRandomSuffix(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 6)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("generate PostgreSQL test database suffix: %v", err)
	}
	return hex.EncodeToString(raw)
}

func mcpPostgresAdminURL(target *url.URL) *url.URL {
	admin := *target
	admin.Path = "/postgres"
	return &admin
}

func mcpPostgresTestURL(target *url.URL, databaseName string) *url.URL {
	testURL := *target
	testURL.Path = "/" + databaseName
	return &testURL
}

// newMCPPostgresDisposableDB creates a unique, throwaway database. It never
// truncates or deletes tables in the caller-selected database; cleanup drops
// only the database this helper created.
func newMCPPostgresDisposableDB(t *testing.T, target *url.URL) (*sql.DB, *sql.DB, string) {
	t.Helper()
	adminURL := mcpPostgresAdminURL(target)
	admin, err := sql.Open(DBPostgresStr, adminURL.String())
	if err != nil {
		t.Fatalf("open PostgreSQL admin database: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := admin.PingContext(ctx); err != nil {
		fallback := *target
		_ = admin.Close()
		admin, err = sql.Open(DBPostgresStr, fallback.String())
		if err != nil {
			t.Fatalf("open PostgreSQL maintenance database: %v", err)
		}
		if err := admin.PingContext(ctx); err != nil {
			_ = admin.Close()
			t.Fatalf("ping PostgreSQL maintenance database: %v", err)
		}
	}
	dbName := "crowler_mcp_test_" + mcpPostgresRandomSuffix(t)
	if _, err := admin.ExecContext(ctx, `CREATE DATABASE `+pq.QuoteIdentifier(dbName)); err != nil {
		_ = admin.Close()
		t.Fatalf("create disposable PostgreSQL database: %v", err)
	}
	t.Cleanup(func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dropCancel()
		if _, err := admin.ExecContext(dropCtx, `DROP DATABASE IF EXISTS `+pq.QuoteIdentifier(dbName)+` WITH (FORCE)`); err != nil {
			t.Logf("drop disposable PostgreSQL database %s: %v", dbName, err)
		}
		_ = admin.Close()
	})

	testURL := mcpPostgresTestURL(target, dbName).String()
	testDB, err := sql.Open(DBPostgresStr, testURL)
	if err != nil {
		t.Fatalf("open disposable PostgreSQL database: %v", err)
	}
	testDB.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = testDB.Close() })
	if err := testDB.PingContext(ctx); err != nil {
		t.Fatalf("ping disposable PostgreSQL database: %v", err)
	}
	return testDB, admin, testURL
}

func stripMCPPostgresSQLComments(statement string) string {
	lines := strings.Split(statement, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func applyMCPMutationPostgresSQL(t *testing.T, db *sql.DB, script, user string) {
	t.Helper()
	scriptText := strings.ReplaceAll(script, ":CROWLER_DB_USER", pq.QuoteIdentifier(user))
	for _, statement := range strings.Split(scriptText, ";") {
		statement = stripMCPPostgresSQLComments(statement)
		upper := strings.ToUpper(statement)
		if statement == "" || upper == "BEGIN" || upper == "COMMIT" ||
			strings.HasPrefix(upper, "SELECT PG_ADVISORY_XACT_LOCK") {
			continue
		}
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("apply PostgreSQL MCP mutation migration: %v\n%s", err, statement)
		}
	}
}

func applyMCPMutationPostgresMigration(t *testing.T, db *sql.DB, user string) {
	t.Helper()
	script, err := os.ReadFile("db_migrations/postgresql-mcp-mutation-v1.17.pgsql")
	if err != nil {
		t.Fatalf("read PostgreSQL MCP mutation migration: %v", err)
	}
	applyMCPMutationPostgresSQL(t, db, string(script), user)
}

func TestMCPPostgresDisposableTargetRequiresExplicitOptIn(t *testing.T) {
	t.Setenv(mcpPostgresDisposableEnv, "")
	t.Setenv(mcpPostgresDSNEnv, "postgres://user:pass@127.0.0.1:5432/test")
	if _, err := mcpPostgresDisposableTarget(); !errors.Is(err, errMCPPostgresDisposableOptInRequired) {
		t.Fatalf("guard error = %v, want explicit opt-in error", err)
	}
}

func TestMCPPostgresDisposableTargetRequiresDSN(t *testing.T) {
	t.Setenv(mcpPostgresDisposableEnv, "1")
	t.Setenv(mcpPostgresDSNEnv, "")
	if _, err := mcpPostgresDisposableTarget(); !errors.Is(err, errMCPPostgresDSNRequired) {
		t.Fatalf("guard error = %v, want DSN error", err)
	}
}

// TestPostgresMCPMutationDurableStore runs against a database created by this
// test harness. It never uses TRUNCATE or deletes rows from the caller-selected
// database. Set CROWLER_MCP_POSTGRES_TEST_DISPOSABLE=1 and supply a DSN for a
// deliberately disposable PostgreSQL instance.
func TestPostgresMCPMutationDurableStore(t *testing.T) {
	target, err := mcpPostgresDisposableTarget()
	if errors.Is(err, errMCPPostgresDisposableOptInRequired) || errors.Is(err, errMCPPostgresDSNRequired) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatalf("disposable PostgreSQL target: %v", err)
	}
	db, admin, testDSN := newMCPPostgresDisposableDB(t, target)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var user string
	if err := admin.QueryRowContext(ctx, `SELECT current_user`).Scan(&user); err != nil {
		t.Fatalf("read PostgreSQL current user: %v", err)
	}
	applyMCPMutationPostgresMigration(t, db, user)
	applyMCPMutationPostgresMigration(t, db, user)

	// A sentinel in the maintenance database proves that the test never deletes
	// unrelated records from the caller-selected database.
	sentinel := "mcp_test_sentinel_" + mcpPostgresRandomSuffix(t)
	if _, err := admin.ExecContext(ctx, `CREATE TABLE `+pq.QuoteIdentifier(sentinel)+` (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create sentinel table: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = admin.ExecContext(cleanupCtx, `DROP TABLE IF EXISTS `+pq.QuoteIdentifier(sentinel))
	})
	if _, err := admin.ExecContext(ctx, `INSERT INTO `+pq.QuoteIdentifier(sentinel)+` (id) VALUES (1)`); err != nil {
		t.Fatalf("insert sentinel row: %v", err)
	}

	var handler Handler = &PostgresHandler{db: db, dbms: DBPostgresStr, connStr: target.String()}
	if err := CheckMCPMutationCapability(ctx, &handler); err != nil {
		t.Fatalf("PostgreSQL MCP mutation capability: %v", err)
	}
	setup, err := os.ReadFile("postgresql-setup.pgsql")
	if err != nil {
		t.Fatalf("read PostgreSQL clean-install setup: %v", err)
	}
	setupText := string(setup)
	markerIndex := strings.Index(setupText, "-- WP-04C2-B additive MCP mutation persistence capability")
	if markerIndex < 0 {
		t.Fatalf("PostgreSQL clean-install MCP block not found")
	}
	applyMCPMutationPostgresSQL(t, db, setupText[markerIndex:], user)
	if err := CheckMCPMutationCapability(ctx, &handler); err != nil {
		t.Fatalf("PostgreSQL clean-install MCP capability: %v", err)
	}
	now := time.Now().UTC()
	proposal := mcpMutationTestProposal("postgres", "pg-run", "pg-call", now)
	if _, inserted, err := AdmitMCPMutationProposal(ctx, &handler, proposal, 2, 2); err != nil || !inserted {
		t.Fatalf("PostgreSQL admission inserted=%v err=%v", inserted, err)
	}
	loaded, found, err := GetMCPMutationProposal(ctx, &handler, proposal.ProposalID)
	if err != nil || !found || loaded.SourceIDs[0] != 42 || string(loaded.CanonicalArguments) != string(proposal.CanonicalArguments) {
		t.Fatalf("PostgreSQL proposal round-trip = %+v found=%v err=%v", loaded, found, err)
	}
	external := mcpMutationTestProposal("postgres-external", "pg-external-run", "pg-external-call", now)
	external.SourceIDs = []uint64{}
	external.ExternalResource = "external-resource"
	if _, inserted, err := AdmitMCPMutationProposal(ctx, &handler, external, 2, 2); err != nil || !inserted {
		t.Fatalf("PostgreSQL external admission inserted=%v err=%v", inserted, err)
	}
	externalLoaded, found, err := GetMCPMutationProposal(ctx, &handler, external.ProposalID)
	if err != nil || !found || len(externalLoaded.SourceIDs) != 0 || externalLoaded.ExternalResource != "external-resource" {
		t.Fatalf("PostgreSQL external round-trip = %+v found=%v err=%v", externalLoaded, found, err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE SourceOwnership (source_id BIGINT PRIMARY KEY, user_id BIGINT NOT NULL)`); err != nil {
		t.Fatalf("create test source ownership table: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO SourceOwnership (source_id, user_id) VALUES (42, 7)`); err != nil {
		t.Fatalf("insert test source ownership: %v", err)
	}
	ownerList, err := ListMCPMutationProposals(ctx, &handler, MCPMutationProposalFilter{
		State: MCPMutationStateApprovalRequired, Limit: 10, ViewerUserID: 7,
	})
	if err != nil || len(ownerList) != 1 || ownerList[0].ProposalID != proposal.ProposalID {
		t.Fatalf("PostgreSQL owner-visible list = %+v err=%v", ownerList, err)
	}
	otherList, err := ListMCPMutationProposals(ctx, &handler, MCPMutationProposalFilter{
		State: MCPMutationStateApprovalRequired, Limit: 10, ViewerUserID: 8,
	})
	if err != nil || len(otherList) != 0 {
		t.Fatalf("PostgreSQL cross-tenant list = %+v err=%v", otherList, err)
	}

	approval := MCPMutationApprovalRow{
		ApprovalID:       mcpMutationTestHash("approval:postgres"),
		ProposalID:       proposal.ProposalID,
		ApprovedByUserID: 9,
		ApprovedBy:       "admin-1",
		ApprovedByIssuer: "test-issuer",
		ApprovedAt:       now.Add(time.Minute),
		ExpiresAt:        now.Add(30 * time.Minute),
		MaxExecutions:    1,
		LastUpdatedAt:    now.Add(time.Minute),
	}
	issued, inserted, err := IssueMCPMutationApproval(ctx, &handler, approval, proposal.ProposalSHA256, now.Add(time.Minute))
	if err != nil || !inserted || issued.State != "active" {
		t.Fatalf("PostgreSQL approval = %+v inserted=%v err=%v", issued, inserted, err)
	}
	if active, found, err := GetActiveMCPMutationApprovalByProposal(ctx, &handler, proposal.ProposalSHA256, now.Add(2*time.Minute)); err != nil || !found || active.ApprovalID != issued.ApprovalID {
		t.Fatalf("PostgreSQL usable approval lookup = %+v found=%v err=%v", active, found, err)
	}
	revoked, transitioned, err := RevokeMCPMutationApproval(ctx, &handler, issued.ApprovalID, 9, "admin-1", "integration revoke", now.Add(2*time.Minute))
	if err != nil || !transitioned || revoked.State != "revoked" {
		t.Fatalf("PostgreSQL revocation = %+v transitioned=%v err=%v", revoked, transitioned, err)
	}
	if active, found, err := GetActiveMCPMutationApprovalByProposal(ctx, &handler, proposal.ProposalSHA256, now.Add(2*time.Minute)); err != nil || found {
		t.Fatalf("PostgreSQL active approval after revoke = %+v found=%v err=%v", active, found, err)
	}
	list, err := ListMCPMutationProposals(ctx, &handler, MCPMutationProposalFilter{
		State: MCPMutationStateApproved, Limit: 10, ViewerAdmin: true,
	})
	if err != nil || len(list) != 0 {
		t.Fatalf("PostgreSQL proposal list after revoke = %+v err=%v", list, err)
	}
	// Capability detection must fail closed when a required index or unique
	// constraint is missing, and when the marker is stale.
	if _, err := db.ExecContext(ctx, `DROP INDEX idx_mcp_proposals_pending`); err != nil {
		t.Fatalf("drop capability index: %v", err)
	}
	if err := CheckMCPMutationCapability(ctx, &handler); !errors.Is(err, ErrMCPMutationSchemaUnavailable) {
		t.Fatalf("missing index capability error = %v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_mcp_proposals_pending ON MCPMutationProposals (state, expires_at)`); err != nil {
		t.Fatalf("restore capability index: %v", err)
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE MCPMutationProposals DROP CONSTRAINT uq_mcp_proposal_logical_call`); err != nil {
		t.Fatalf("drop capability unique constraint: %v", err)
	}
	if err := CheckMCPMutationCapability(ctx, &handler); !errors.Is(err, ErrMCPMutationSchemaUnavailable) {
		t.Fatalf("missing unique constraint capability error = %v", err)
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE MCPMutationProposals ADD CONSTRAINT uq_mcp_proposal_logical_call UNIQUE (run_id, effective_call_id)`); err != nil {
		t.Fatalf("restore capability unique constraint: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE SchemaCapabilities SET checksum=$1 WHERE capability=$2`,
		strings.Repeat("0", 64), MCPMutationCapability); err != nil {
		t.Fatalf("stale capability marker: %v", err)
	}
	if err := CheckMCPMutationCapability(ctx, &handler); !errors.Is(err, ErrMCPMutationSchemaUnavailable) {
		t.Fatalf("stale marker capability error = %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE SchemaCapabilities SET checksum=$1 WHERE capability=$2`,
		MCPMutationCapabilityChecksum, MCPMutationCapability); err != nil {
		t.Fatalf("restore capability marker: %v", err)
	}
	if err := CheckMCPMutationCapability(ctx, &handler); err != nil {
		t.Fatalf("capability after restore: %v", err)
	}

	// Separate pools exercise concurrent admissions and issue/revoke races on
	// independent PostgreSQL connections.
	secondPool, err := sql.Open(DBPostgresStr, testDSN)
	if err != nil {
		t.Fatalf("open second PostgreSQL pool: %v", err)
	}
	secondPool.SetMaxOpenConns(2)
	t.Cleanup(func() { _ = secondPool.Close() })
	if err := secondPool.PingContext(ctx); err != nil {
		t.Fatalf("ping second PostgreSQL pool: %v", err)
	}
	var secondHandler Handler = &PostgresHandler{db: secondPool, dbms: DBPostgresStr, connStr: testDSN}
	concurrentA := mcpMutationTestProposal("pg-concurrent-a", "pg-concurrent-run", "pg-concurrent-a", now)
	concurrentB := mcpMutationTestProposal("pg-concurrent-b", "pg-concurrent-run", "pg-concurrent-b", now)
	results := make(chan error, 2)
	go func() {
		_, _, err := AdmitMCPMutationProposalAt(ctx, &handler, concurrentA, 1, 10, now)
		results <- err
	}()
	go func() {
		_, _, err := AdmitMCPMutationProposalAt(ctx, &secondHandler, concurrentB, 1, 10, now)
		results <- err
	}()
	successes := 0
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrMCPMutationQuotaReached) && !errors.Is(err, ErrMCPMutationProposalConflict) {
			t.Fatalf("concurrent admission error = %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent admission successes = %d, want 1", successes)
	}
	var concurrentRows, concurrentQuota int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM MCPMutationProposals WHERE run_id='pg-concurrent-run'`).Scan(&concurrentRows); err != nil {
		t.Fatalf("count concurrent proposals: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT active_count FROM MCPMutationRunQuota WHERE run_id='pg-concurrent-run'`).Scan(&concurrentQuota); err != nil {
		t.Fatalf("read concurrent quota: %v", err)
	}
	if concurrentRows != 1 || concurrentQuota != 1 {
		t.Fatalf("concurrent proposal rows=%d quota=%d", concurrentRows, concurrentQuota)
	}

	raceProposal := mcpMutationTestProposal("pg-approval-race", "pg-approval-race-run", "pg-approval-race-call", now)
	if _, inserted, err := AdmitMCPMutationProposalAt(ctx, &handler, raceProposal, 2, 10, now); err != nil || !inserted {
		t.Fatalf("admit approval race proposal inserted=%v err=%v", inserted, err)
	}
	approvalA := MCPMutationApprovalRow{
		ApprovalID: mcpMutationTestHash("pg-approval-race-a"), ProposalID: raceProposal.ProposalID,
		ApprovedByUserID: 9, ApprovedBy: "admin-1", ApprovedByIssuer: "test-issuer",
		ApprovedAt: now.Add(time.Minute), ExpiresAt: now.Add(20 * time.Minute),
		MaxExecutions: 1, LastUpdatedAt: now.Add(time.Minute),
	}
	approvalB := approvalA
	approvalB.ApprovalID = mcpMutationTestHash("pg-approval-race-b")
	approvalResults := make(chan error, 2)
	go func() {
		_, _, err := IssueMCPMutationApproval(ctx, &handler, approvalA, raceProposal.ProposalSHA256, now.Add(time.Minute))
		approvalResults <- err
	}()
	go func() {
		_, _, err := IssueMCPMutationApproval(ctx, &secondHandler, approvalB, raceProposal.ProposalSHA256, now.Add(time.Minute))
		approvalResults <- err
	}()
	for i := 0; i < 2; i++ {
		if err := <-approvalResults; err != nil {
			t.Fatalf("concurrent approval error = %v", err)
		}
	}
	var activeApprovals int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM MCPMutationApprovals WHERE proposal_sha256=$1 AND state='active'`, raceProposal.ProposalSHA256).Scan(&activeApprovals); err != nil {
		t.Fatalf("count concurrent approvals: %v", err)
	}
	if activeApprovals != 1 {
		t.Fatalf("concurrent active approvals = %d, want 1", activeApprovals)
	}

	revokeRaceProposal := mcpMutationTestProposal("pg-issue-revoke-race", "pg-issue-revoke-run", "pg-issue-revoke-call", now)
	if _, inserted, err := AdmitMCPMutationProposalAt(ctx, &handler, revokeRaceProposal, 2, 10, now); err != nil || !inserted {
		t.Fatalf("admit issue/revoke race proposal inserted=%v err=%v", inserted, err)
	}
	initial := approvalA
	initial.ApprovalID = mcpMutationTestHash("pg-issue-revoke-initial")
	initial.ProposalID = revokeRaceProposal.ProposalID
	initial.ApprovedAt = now.Add(2 * time.Minute)
	initial.ExpiresAt = now.Add(20 * time.Minute)
	initial.LastUpdatedAt = now.Add(2 * time.Minute)
	issuedInitial, _, err := IssueMCPMutationApproval(ctx, &handler, initial, revokeRaceProposal.ProposalSHA256, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("issue initial race approval: %v", err)
	}
	replacement := approvalA
	replacement.ApprovalID = mcpMutationTestHash("pg-issue-revoke-replacement")
	replacement.ApprovedAt = now.Add(3 * time.Minute)
	replacement.ExpiresAt = now.Add(20 * time.Minute)
	replacement.LastUpdatedAt = now.Add(3 * time.Minute)
	issueRevokeResults := make(chan error, 2)
	go func() {
		_, _, err := IssueMCPMutationApproval(ctx, &handler, replacement, revokeRaceProposal.ProposalSHA256, now.Add(3*time.Minute))
		issueRevokeResults <- err
	}()
	go func() {
		_, _, err := RevokeMCPMutationApproval(ctx, &secondHandler, issuedInitial.ApprovalID, 9, "admin-1", "race revoke", now.Add(3*time.Minute))
		issueRevokeResults <- err
	}()
	for i := 0; i < 2; i++ {
		err := <-issueRevokeResults
		if err != nil && !errors.Is(err, ErrMCPMutationProposalConflict) &&
			!errors.Is(err, ErrMCPMutationApprovalConflict) &&
			!errors.Is(err, ErrMCPMutationApprovalExpired) &&
			!errors.Is(err, ErrMCPMutationApprovalRevoked) {
			t.Fatalf("issue/revoke race error = %v", err)
		}
	}
	var finalActive int
	var finalProposalState string
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM MCPMutationApprovals WHERE proposal_sha256=$1 AND state='active'`, revokeRaceProposal.ProposalSHA256).Scan(&finalActive); err != nil {
		t.Fatalf("count final active approvals: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT state FROM MCPMutationProposals WHERE proposal_id=$1`, revokeRaceProposal.ProposalID).Scan(&finalProposalState); err != nil {
		t.Fatalf("read final proposal state: %v", err)
	}
	if finalActive > 1 {
		t.Fatalf("issue/revoke race left %d active approvals", finalActive)
	}
	if finalActive == 1 && finalProposalState != MCPMutationStateApproved {
		t.Fatalf("active approval with proposal state %q", finalProposalState)
	}
	if finalActive == 0 && finalProposalState != MCPMutationStateApprovalRequired {
		t.Fatalf("no active approval with proposal state %q", finalProposalState)
	}

	var sentinelRows int
	if err := admin.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+pq.QuoteIdentifier(sentinel)).Scan(&sentinelRows); err != nil || sentinelRows != 1 {
		t.Fatalf("sentinel rows = %d err=%v, unrelated maintenance data was affected", sentinelRows, err)
	}

	// A wrong critical column type must not be certified by the marker.
	if _, err := db.ExecContext(ctx, `ALTER TABLE MCPMutationProposals
		ALTER COLUMN canonical_arguments TYPE TEXT USING encode(canonical_arguments, 'hex')`); err != nil {
		t.Fatalf("alter canonical argument type: %v", err)
	}
	if err := CheckMCPMutationCapability(ctx, &handler); !errors.Is(err, ErrMCPMutationSchemaUnavailable) {
		t.Fatalf("wrong column type capability error = %v", err)
	}
}
