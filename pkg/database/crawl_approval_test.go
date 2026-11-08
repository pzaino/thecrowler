package database

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func newCrawlApprovalTestDB(t *testing.T) (*sql.DB, Handler) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE CrawlApprovals (
		approval_id CHAR(64) PRIMARY KEY,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		last_updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		proposal_sha256 CHAR(64) NOT NULL,
		request_id TEXT NOT NULL,
		run_id TEXT NOT NULL,
		agent_id TEXT NOT NULL,
		owner TEXT NOT NULL,
		principal_user_id INTEGER NOT NULL,
		source_id INTEGER NOT NULL,
		operation_type TEXT NOT NULL,
		issuer TEXT NOT NULL,
		issued_by_user_id INTEGER NOT NULL,
		issued_by TEXT NOT NULL,
		issued_by_issuer TEXT NOT NULL,
		approved_by_user_id INTEGER NOT NULL,
		approved_by TEXT NOT NULL,
		approved_by_issuer TEXT NOT NULL,
		approval_sha256 CHAR(64) NOT NULL,
		proposal_expires_at TIMESTAMP NOT NULL,
		approved_at TIMESTAMP NOT NULL,
		expires_at TIMESTAMP NOT NULL,
		revoked_at TIMESTAMP,
		revoked_by_user_id INTEGER,
		revoked_by TEXT,
		revocation_reason TEXT,
		UNIQUE (request_id, run_id, principal_user_id, source_id, operation_type),
		CHECK (expires_at > approved_at),
		CHECK (julianday(expires_at) - julianday(approved_at) <= 1.0)
	)`); err != nil {
		t.Fatalf("create CrawlApprovals: %v", err)
	}
	return db, Handler(&SQLiteHandler{db: db, dbms: DBSQLiteStr})
}

func crawlApprovalRowFixture(now time.Time) CrawlApprovalRow {
	return CrawlApprovalRow{
		ApprovalID:        strings.Repeat("a", 64),
		ProposalSHA256:    strings.Repeat("b", 64),
		RequestID:         "request-1",
		RunID:             "run-1",
		AgentID:           "agent-1",
		Owner:             "owner-1",
		PrincipalUserID:   7,
		SourceID:          42,
		OperationType:     CrawlOperationType,
		Issuer:            "crowler-api",
		IssuedByUserID:    7,
		IssuedBy:          "owner-1",
		IssuedByIssuer:    "crowler",
		ApprovedByUserID:  7,
		ApprovedBy:        "owner-1",
		ApprovedByIssuer:  "crowler",
		ApprovalSHA256:    strings.Repeat("c", 64),
		ProposalExpiresAt: now.Add(time.Hour),
		ApprovedAt:        now.Add(-time.Minute),
		ExpiresAt:         now.Add(30 * time.Minute),
	}
}

func TestCrawlApprovalDurableStore(t *testing.T) {
	_, handler := newCrawlApprovalTestDB(t)
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	row := crawlApprovalRowFixture(now)
	inserted, err := InsertCrawlApproval(context.Background(), &handler, row)
	if err != nil || !inserted {
		t.Fatalf("insert approval: inserted=%v err=%v", inserted, err)
	}
	inserted, err = InsertCrawlApproval(context.Background(), &handler, row)
	if err != nil || inserted {
		t.Fatalf("duplicate insert: inserted=%v err=%v", inserted, err)
	}

	byID, found, err := GetCrawlApprovalByID(context.Background(), &handler, row.ApprovalID)
	if err != nil || !found {
		t.Fatalf("get by ID: found=%v err=%v", found, err)
	}
	if byID.RunID != row.RunID || byID.SourceID != row.SourceID || byID.ApprovedBy != row.ApprovedBy {
		t.Fatalf("unexpected row: %+v", byID)
	}

	byRequest, found, err := GetCrawlApprovalByRequest(context.Background(), &handler, row.RunID, row.RequestID, row.PrincipalUserID, row.SourceID, row.OperationType)
	if err != nil || !found || byRequest.ApprovalID != row.ApprovalID {
		t.Fatalf("get by request: %+v found=%v err=%v", byRequest, found, err)
	}

	revoked, exists, err := RevokeCrawlApproval(context.Background(), &handler, row.ApprovalID, 7, "owner-1", "test", now)
	if err != nil || !revoked || !exists {
		t.Fatalf("revoke: revoked=%v exists=%v err=%v", revoked, exists, err)
	}
	revoked, exists, err = RevokeCrawlApproval(context.Background(), &handler, row.ApprovalID, 7, "owner-1", "retry", now)
	if err != nil || revoked || !exists {
		t.Fatalf("repeat revoke: revoked=%v exists=%v err=%v", revoked, exists, err)
	}
	revokedRow, found, err := GetCrawlApprovalByID(context.Background(), &handler, row.ApprovalID)
	if err != nil || !found || !revokedRow.RevokedAt.Valid {
		t.Fatalf("revoked row: %+v found=%v err=%v", revokedRow, found, err)
	}

	if _, found, err := GetCrawlApprovalByID(context.Background(), &handler, strings.Repeat("d", 64)); err != nil || found {
		t.Fatalf("missing approval: found=%v err=%v", found, err)
	}
}

func TestCrawlApprovalStoreFailures(t *testing.T) {
	db, handler := newCrawlApprovalTestDB(t)
	if _, err := db.Exec(`DROP TABLE CrawlApprovals`); err != nil {
		t.Fatalf("drop table: %v", err)
	}
	if _, _, err := GetCrawlApprovalByID(context.Background(), &handler, strings.Repeat("a", 64)); !errors.Is(err, ErrCrawlApprovalSchemaUnavailable) {
		t.Fatalf("missing schema error = %v", err)
	}
	unsupported := Handler(&SQLiteHandler{db: db, dbms: DBMySQLStr})
	if _, err := InsertCrawlApproval(context.Background(), &unsupported, crawlApprovalRowFixture(time.Now().UTC())); !errors.Is(err, ErrCrawlApprovalStoreUnsupported) {
		t.Fatalf("unsupported backend error = %v", err)
	}
}
