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

func newCrawlOperationTestDB(t *testing.T) (*sql.DB, Handler) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite database: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE Sources (
		source_id INTEGER PRIMARY KEY,
		disabled BOOLEAN NOT NULL DEFAULT FALSE,
		status TEXT NOT NULL DEFAULT 'new',
		usr_id INTEGER NOT NULL DEFAULT 0,
		deleted_at TIMESTAMP,
		last_updated_at TIMESTAMP
	)`); err != nil {
		t.Fatalf("create Sources: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE SourceOwnership (
		source_id INTEGER PRIMARY KEY,
		user_id INTEGER NOT NULL,
		verified_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		verified_by_user_id INTEGER NOT NULL,
		verified_by TEXT NOT NULL DEFAULT 'fixture',
		verification_method TEXT NOT NULL DEFAULT 'admin_verification',
		verification_reason TEXT,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		last_updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatalf("create SourceOwnership: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE Users (
		user_id INTEGER PRIMARY KEY,
		username TEXT NOT NULL,
		disabled BOOLEAN NOT NULL DEFAULT FALSE,
		deleted_at TIMESTAMP
	)`); err != nil {
		t.Fatalf("create Users: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE AuthRoles (role_id INTEGER PRIMARY KEY, name TEXT NOT NULL)`); err != nil {
		t.Fatalf("create AuthRoles: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE UserRoles (user_id INTEGER NOT NULL, role_id INTEGER NOT NULL, PRIMARY KEY (user_id, role_id))`); err != nil {
		t.Fatalf("create UserRoles: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO Users(user_id, username, disabled, deleted_at) VALUES
		(7, 'owner-1', FALSE, NULL), (9, 'admin-1', FALSE, NULL);
		INSERT INTO AuthRoles(role_id, name) VALUES (1, 'admin'), (2, 'user');
		INSERT INTO UserRoles(user_id, role_id) VALUES (7, 2), (9, 1);`); err != nil {
		t.Fatalf("seed scheduling users: %v", err)
	}
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
	if _, err := db.Exec(`CREATE TABLE Status (
		status_sha256 CHAR(64) PRIMARY KEY,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		last_updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		deleted_at TIMESTAMP,
		parent_status_sha256 CHAR(64),
		agent_id TEXT,
		source_id INTEGER,
		operation_type TEXT,
		status TEXT NOT NULL,
		progress REAL,
		details TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("create Status: %v", err)
	}
	return db, Handler(&SQLiteHandler{db: db, dbms: DBSQLiteStr})
}

func insertCrawlOperationSource(t *testing.T, db *sql.DB, sourceID uint64, status string, disabled bool) {
	t.Helper()
	insertCrawlOperationSourceWithOwner(t, db, sourceID, status, disabled, 7)
}

func insertCrawlOperationSourceWithOwner(t *testing.T, db *sql.DB, sourceID uint64, status string, disabled bool, ownerID uint64) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO Sources(source_id, disabled, status, usr_id) VALUES (?,?,?,?)`, sourceID, disabled, status, ownerID); err != nil {
		t.Fatalf("insert source: %v", err)
	}
	if ownerID != 0 {
		if _, err := db.Exec(`INSERT INTO SourceOwnership(source_id, user_id, verified_by_user_id) VALUES (?,?,?)`,
			sourceID, ownerID, ownerID); err != nil {
			t.Fatalf("insert source ownership: %v", err)
		}
	}
}

func ensureCrawlApprovalFixture(t *testing.T, db *sql.DB, req CrawlScheduleRequest) {
	t.Helper()
	row := CrawlApprovalRow{
		ApprovalID:        req.ApprovalID,
		ProposalSHA256:    req.ParametersSHA256,
		RequestID:         req.RequestID,
		RunID:             req.RunID,
		AgentID:           req.AgentID,
		Owner:             req.Owner,
		PrincipalUserID:   req.PrincipalUserID,
		SourceID:          req.SourceID,
		OperationType:     req.OperationType,
		Issuer:            "test-authority",
		IssuedByUserID:    7,
		IssuedBy:          "owner-1",
		IssuedByIssuer:    "crowler",
		ApprovedByUserID:  req.ApprovedByUserID,
		ApprovedBy:        req.ApprovedBy,
		ApprovedByIssuer:  "crowler",
		ApprovalSHA256:    req.ApprovalSHA256,
		ProposalExpiresAt: req.ExpiresAt,
		ApprovedAt:        req.ApprovedAt,
		ExpiresAt:         req.ExpiresAt,
	}
	if _, err := InsertCrawlApproval(context.Background(), (*Handler)(nil), row); err == nil {
		return
	}
	// Test fixtures reuse one approval ID across different logical requests.
	// Replace the row when the request ID changes so the fixture represents a
	// fresh approval; same-request retries keep the original row unchanged.
	var existingRequestID string
	if err := db.QueryRow(`SELECT request_id FROM CrawlApprovals WHERE approval_id = ?`, req.ApprovalID).Scan(&existingRequestID); err == nil && existingRequestID != req.RequestID {
		_, _ = db.Exec(`DELETE FROM CrawlApprovals WHERE approval_id = ?`, req.ApprovalID)
	}
	handler := Handler(&SQLiteHandler{db: db, dbms: DBSQLiteStr})
	_, _ = InsertCrawlApproval(context.Background(), &handler, row)
}

func crawlOperationNow() time.Time {
	return time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
}

func crawlOperationRequest(sourceID uint64) CrawlScheduleRequest {
	now := crawlOperationNow()
	return CrawlScheduleRequest{
		RequestID: "request-1", RunID: "run-1", PrincipalUserID: 7, AgentID: "agent-1", Owner: "owner-1",
		SourceID: sourceID, AllowedSources: []uint64{sourceID}, OperationType: CrawlOperationType,
		ParametersSHA256: strings.Repeat("a", 64),
		ApprovalID:       "approval-1", ApprovalSHA256: strings.Repeat("b", 64),
		ApprovedBy: "operator-1", ApprovedByUserID: 9, ApprovedAt: now.Add(-time.Minute),
		ExpiresAt: now.Add(time.Hour), MaxRequests: 10, BudgetWindow: time.Hour,
	}
}

func scheduleCrawlForTest(t *testing.T, ctx context.Context, handler *Handler, req CrawlScheduleRequest) (CrawlScheduleResult, error) {
	if sqlite, ok := (*handler).(*SQLiteHandler); ok && sqlite != nil {
		ensureCrawlApprovalFixture(t, sqlite.db, req)
	}
	return scheduleSourceCrawlRequest(ctx, handler, req, crawlOperationNow())
}

func crawlOperationSourceStatus(t *testing.T, db *sql.DB, sourceID uint64) string {
	t.Helper()
	var status string
	if err := db.QueryRow(`SELECT status FROM Sources WHERE source_id=?`, sourceID).Scan(&status); err != nil {
		t.Fatalf("select source status: %v", err)
	}
	return status
}

func crawlOperationStatusCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM Status`).Scan(&count); err != nil {
		t.Fatalf("count status rows: %v", err)
	}
	return count
}

func crawlOperationStatusState(t *testing.T, db *sql.DB, operationID string) string {
	t.Helper()
	var state string
	if err := db.QueryRow(`SELECT status FROM Status WHERE status_sha256=?`, operationID).Scan(&state); err != nil {
		t.Fatalf("select operation state: %v", err)
	}
	return state
}

func TestScheduleSourceCrawlRequestAccepted(t *testing.T) {
	db, handler := newCrawlOperationTestDB(t)
	insertCrawlOperationSource(t, db, 42, "completed", false)
	req := crawlOperationRequest(42)

	result, err := scheduleCrawlForTest(t, context.Background(), &handler, req)
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if result.State != CrawlOperationAccepted || result.ReasonCode != CrawlReasonScheduled || result.SourceState != CrawlSourcePending {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.OperationID == "" || result.Reused {
		t.Fatalf("unexpected operation identity: %+v", result)
	}
	if got := crawlOperationSourceStatus(t, db, 42); got != "pending" {
		t.Fatalf("source status = %q, want pending", got)
	}
	if got := crawlOperationStatusState(t, db, result.OperationID); got != string(CrawlOperationAccepted) {
		t.Fatalf("operation state = %q, want accepted", got)
	}
}

func TestScheduleSourceCrawlRequestUnchangedStates(t *testing.T) {
	cases := []struct {
		name     string
		status   string
		disabled bool
		want     CrawlOperationState
		reason   string
		source   CrawlSourceState
	}{
		{"already pending", "pending", false, CrawlOperationAlreadyPending, CrawlReasonAlreadyPending, CrawlSourcePending},
		{"processing", "processing", false, CrawlOperationProcessing, CrawlReasonProcessing, CrawlSourceProcessing},
		{"disabled", "completed", true, CrawlOperationDisabled, CrawlReasonDisabled, CrawlSourceDisabled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, handler := newCrawlOperationTestDB(t)
			insertCrawlOperationSource(t, db, 42, tc.status, tc.disabled)
			result, err := scheduleCrawlForTest(t, context.Background(), &handler, crawlOperationRequest(42))
			if err != nil {
				t.Fatalf("schedule: %v", err)
			}
			if result.State != tc.want || result.ReasonCode != tc.reason || result.SourceState != tc.source {
				t.Fatalf("unexpected result: %+v", result)
			}
			if got := crawlOperationSourceStatus(t, db, 42); got != tc.status {
				t.Fatalf("source status changed to %q, want %q", got, tc.status)
			}
			if got := crawlOperationStatusCount(t, db); got != 1 {
				t.Fatalf("operation rows = %d, want 1", got)
			}
		})
	}
}

func TestScheduleSourceCrawlRequestMissingAndExpired(t *testing.T) {
	t.Run("missing source", func(t *testing.T) {
		db, handler := newCrawlOperationTestDB(t)
		result, err := scheduleCrawlForTest(t, context.Background(), &handler, crawlOperationRequest(404))
		if err != nil {
			t.Fatalf("schedule: %v", err)
		}
		if result.State != CrawlOperationNotFound || result.ReasonCode != CrawlReasonNotFound || result.SourceState != CrawlSourceMissing {
			t.Fatalf("unexpected result: %+v", result)
		}
		if got := crawlOperationStatusCount(t, db); got != 1 {
			t.Fatalf("operation rows = %d, want 1", got)
		}
	})

	t.Run("expired request", func(t *testing.T) {
		db, handler := newCrawlOperationTestDB(t)
		insertCrawlOperationSource(t, db, 42, "completed", false)
		req := crawlOperationRequest(42)
		req.ExpiresAt = crawlOperationNow()
		result, err := scheduleCrawlForTest(t, context.Background(), &handler, req)
		if err != nil {
			t.Fatalf("schedule: %v", err)
		}
		if result.State != CrawlOperationExpired || result.ReasonCode != CrawlReasonExpired {
			t.Fatalf("unexpected result: %+v", result)
		}
		if got := crawlOperationSourceStatus(t, db, 42); got != "completed" {
			t.Fatalf("expired request changed source to %q", got)
		}
		if got := crawlOperationStatusCount(t, db); got != 0 {
			t.Fatalf("expired request created %d operation rows", got)
		}
	})
}

func TestScheduleSourceCrawlRequestIdempotency(t *testing.T) {
	t.Run("duplicate request reuses accepted outcome", func(t *testing.T) {
		db, handler := newCrawlOperationTestDB(t)
		insertCrawlOperationSource(t, db, 42, "completed", false)
		req := crawlOperationRequest(42)
		first, err := scheduleCrawlForTest(t, context.Background(), &handler, req)
		if err != nil {
			t.Fatalf("first schedule: %v", err)
		}
		second, err := scheduleCrawlForTest(t, context.Background(), &handler, req)
		if err != nil {
			t.Fatalf("second schedule: %v", err)
		}
		if second.OperationID != first.OperationID || !second.Reused || second.State != CrawlOperationAccepted {
			t.Fatalf("duplicate did not reuse outcome: first=%+v second=%+v", first, second)
		}
		if got := crawlOperationStatusCount(t, db); got != 1 {
			t.Fatalf("operation rows = %d, want 1", got)
		}
	})

	t.Run("same key different parameters conflicts", func(t *testing.T) {
		db, handler := newCrawlOperationTestDB(t)
		insertCrawlOperationSource(t, db, 42, "completed", false)
		req := crawlOperationRequest(42)
		first, err := scheduleCrawlForTest(t, context.Background(), &handler, req)
		if err != nil {
			t.Fatalf("first schedule: %v", err)
		}
		changed := req
		changed.ParametersSHA256 = strings.Repeat("c", 64)
		second, err := scheduleCrawlForTest(t, context.Background(), &handler, changed)
		if err != nil {
			t.Fatalf("conflict schedule: %v", err)
		}
		if second.State != CrawlOperationConflict || second.ReasonCode != CrawlReasonConflict {
			t.Fatalf("unexpected conflict result: %+v", second)
		}
		if got := crawlOperationStatusState(t, db, first.OperationID); got != string(CrawlOperationAccepted) {
			t.Fatalf("conflicting request changed operation state to %q", got)
		}
		if got := crawlOperationSourceStatus(t, db, 42); got != "pending" {
			t.Fatalf("conflicting request changed source to %q", got)
		}
	})

	t.Run("different agent does not collide", func(t *testing.T) {
		db, handler := newCrawlOperationTestDB(t)
		insertCrawlOperationSource(t, db, 42, "completed", false)
		firstReq := crawlOperationRequest(42)
		first, err := scheduleCrawlForTest(t, context.Background(), &handler, firstReq)
		if err != nil {
			t.Fatalf("first schedule: %v", err)
		}
		secondReq := firstReq
		secondReq.RequestID = "request-agent-2"
		secondReq.AgentID = "agent-2"
		secondReq.ApprovalID = "approval-agent-2"
		secondReq.ApprovalSHA256 = strings.Repeat("d", 64)
		second, err := scheduleCrawlForTest(t, context.Background(), &handler, secondReq)
		if err != nil {
			t.Fatalf("second agent schedule: %v", err)
		}
		if first.OperationID == second.OperationID {
			t.Fatalf("different agents collided on operation ID")
		}
		if second.State != CrawlOperationAlreadyPending {
			t.Fatalf("second agent did not observe pending source: %+v", second)
		}
		if got := crawlOperationStatusCount(t, db); got != 2 {
			t.Fatalf("operation rows = %d, want 2", got)
		}
	})

	t.Run("different owner does not collide", func(t *testing.T) {
		db, handler := newCrawlOperationTestDB(t)
		insertCrawlOperationSource(t, db, 42, "completed", false)
		firstReq := crawlOperationRequest(42)
		first, err := scheduleCrawlForTest(t, context.Background(), &handler, firstReq)
		if err != nil {
			t.Fatalf("first schedule: %v", err)
		}
		secondReq := firstReq
		secondReq.RequestID = "request-owner-2"
		secondReq.Owner = "owner-2"
		secondReq.ApprovalID = "approval-owner-2"
		secondReq.ApprovalSHA256 = strings.Repeat("e", 64)
		second, err := scheduleCrawlForTest(t, context.Background(), &handler, secondReq)
		if err != nil {
			t.Fatalf("second owner schedule: %v", err)
		}
		if first.OperationID == second.OperationID {
			t.Fatalf("different owners collided on operation ID")
		}
		if second.State != CrawlOperationAlreadyPending {
			t.Fatalf("second owner did not observe pending source: %+v", second)
		}
		if got := crawlOperationStatusCount(t, db); got != 2 {
			t.Fatalf("operation rows = %d, want 2", got)
		}
	})
}

func TestScheduleSourceCrawlRequestCancellationAndRollback(t *testing.T) {
	t.Run("cancelled context", func(t *testing.T) {
		db, handler := newCrawlOperationTestDB(t)
		insertCrawlOperationSource(t, db, 42, "completed", false)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		result, err := scheduleCrawlForTest(t, ctx, &handler, crawlOperationRequest(42))
		if err == nil {
			t.Fatal("cancelled request must return an error")
		}
		if result.State != CrawlOperationCancelled {
			t.Fatalf("unexpected cancelled result: %+v", result)
		}
		if got := crawlOperationSourceStatus(t, db, 42); got != "completed" {
			t.Fatalf("cancelled request changed source to %q", got)
		}
		if got := crawlOperationStatusCount(t, db); got != 0 {
			t.Fatalf("cancelled request created %d operation rows", got)
		}
	})

	t.Run("expired deadline", func(t *testing.T) {
		db, handler := newCrawlOperationTestDB(t)
		insertCrawlOperationSource(t, db, 42, "completed", false)
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		result, err := scheduleCrawlForTest(t, ctx, &handler, crawlOperationRequest(42))
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("got %v, want context.DeadlineExceeded", err)
		}
		if result.State != CrawlOperationCancelled {
			t.Fatalf("unexpected deadline result: %+v", result)
		}
		if got := crawlOperationSourceStatus(t, db, 42); got != "completed" {
			t.Fatalf("deadline request changed source to %q", got)
		}
		if got := crawlOperationStatusCount(t, db); got != 0 {
			t.Fatalf("deadline request created %d operation rows", got)
		}
	})

	t.Run("source update rollback removes operation row", func(t *testing.T) {
		db, handler := newCrawlOperationTestDB(t)
		insertCrawlOperationSource(t, db, 42, "completed", false)
		if _, err := db.Exec(`CREATE TRIGGER fail_source_update BEFORE UPDATE ON Sources
			BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
			t.Fatalf("create trigger: %v", err)
		}
		result, err := scheduleCrawlForTest(t, context.Background(), &handler, crawlOperationRequest(42))
		if err == nil {
			t.Fatal("failed source update must return an error")
		}
		if result.State != CrawlOperationFailed {
			t.Fatalf("unexpected failed result: %+v", result)
		}
		if got := crawlOperationSourceStatus(t, db, 42); got != "completed" {
			t.Fatalf("failed request changed source to %q", got)
		}
		if got := crawlOperationStatusCount(t, db); got != 0 {
			t.Fatalf("rolled-back request left %d operation rows", got)
		}
	})
}

func TestScheduleSourceCrawlRequestInvalidAndUnsupported(t *testing.T) {
	t.Run("source scope is required before mutation", func(t *testing.T) {
		db, handler := newCrawlOperationTestDB(t)
		insertCrawlOperationSource(t, db, 42, "completed", false)
		req := crawlOperationRequest(42)
		req.AllowedSources = nil
		result, err := scheduleCrawlForTest(t, context.Background(), &handler, req)
		if err != nil || result.State != CrawlOperationDenied || result.ReasonCode != CrawlReasonSourceScopeRequired {
			t.Fatalf("unexpected scope result: %+v, %v", result, err)
		}
		req.AllowedSources = []uint64{7}
		result, err = scheduleCrawlForTest(t, context.Background(), &handler, req)
		if err != nil || result.State != CrawlOperationDenied || result.ReasonCode != CrawlReasonSourceOutOfScope {
			t.Fatalf("unexpected out-of-scope result: %+v, %v", result, err)
		}
		if got := crawlOperationSourceStatus(t, db, 42); got != "completed" {
			t.Fatalf("scope denial changed source to %q", got)
		}
		if got := crawlOperationStatusCount(t, db); got != 0 {
			t.Fatalf("scope denial created %d operation rows", got)
		}
	})

	t.Run("missing principal does not mutate", func(t *testing.T) {
		db, handler := newCrawlOperationTestDB(t)
		insertCrawlOperationSource(t, db, 42, "completed", false)
		req := crawlOperationRequest(42)
		req.PrincipalUserID = 0
		result, err := scheduleCrawlForTest(t, context.Background(), &handler, req)
		if err != nil {
			t.Fatalf("missing principal returned operational error: %v", err)
		}
		if result.State != CrawlOperationDenied || result.ReasonCode != CrawlReasonInvalid {
			t.Fatalf("unexpected principal result: %+v", result)
		}
		if got := crawlOperationSourceStatus(t, db, 42); got != "completed" {
			t.Fatalf("missing principal changed source to %q", got)
		}
		if got := crawlOperationStatusCount(t, db); got != 0 {
			t.Fatalf("missing principal created %d operation rows", got)
		}
	})

	t.Run("invalid request does not mutate", func(t *testing.T) {
		db, handler := newCrawlOperationTestDB(t)
		insertCrawlOperationSource(t, db, 42, "completed", false)
		req := crawlOperationRequest(42)
		req.ApprovalSHA256 = "short"
		result, err := scheduleCrawlForTest(t, context.Background(), &handler, req)
		if err != nil {
			t.Fatalf("invalid request returned operational error: %v", err)
		}
		if result.State != CrawlOperationDenied || result.ReasonCode != CrawlReasonInvalid {
			t.Fatalf("unexpected invalid result: %+v", result)
		}
		if got := crawlOperationSourceStatus(t, db, 42); got != "completed" {
			t.Fatalf("invalid request changed source to %q", got)
		}
		if got := crawlOperationStatusCount(t, db); got != 0 {
			t.Fatalf("invalid request created %d operation rows", got)
		}
	})

	t.Run("database failure does not acknowledge", func(t *testing.T) {
		db, handler := newCrawlOperationTestDB(t)
		insertCrawlOperationSource(t, db, 42, "completed", false)
		if err := db.Close(); err != nil {
			t.Fatalf("close database: %v", err)
		}
		result, err := scheduleCrawlForTest(t, context.Background(), &handler, crawlOperationRequest(42))
		if err == nil {
			t.Fatal("closed database must return an operational error")
		}
		if result.State != CrawlOperationFailed || result.ReasonCode != CrawlReasonDatabase {
			t.Fatalf("unexpected failure result: %+v", result)
		}
	})

	t.Run("unsupported backend fails closed", func(t *testing.T) {
		db, _ := newCrawlOperationTestDB(t)
		handler := Handler(&SQLiteHandler{db: db, dbms: DBMySQLStr})
		result, err := scheduleCrawlForTest(t, context.Background(), &handler, crawlOperationRequest(42))
		if !errors.Is(err, ErrCrawlSchedulingUnsupported) {
			t.Fatalf("got %v, want ErrCrawlSchedulingUnsupported", err)
		}
		if result.State != CrawlOperationUnsupported || result.ReasonCode != CrawlReasonUnsupported {
			t.Fatalf("unexpected unsupported result: %+v", result)
		}
		if got := crawlOperationStatusCount(t, db); got != 0 {
			t.Fatalf("unsupported backend created %d operation rows", got)
		}
	})
}

func TestScheduleSourceCrawlRequestReplayDoesNotRescheduleCompletedSource(t *testing.T) {
	db, handler := newCrawlOperationTestDB(t)
	insertCrawlOperationSource(t, db, 42, "completed", false)
	req := crawlOperationRequest(42)
	first, err := scheduleCrawlForTest(t, context.Background(), &handler, req)
	if err != nil {
		t.Fatalf("first schedule: %v", err)
	}
	if first.State != CrawlOperationAccepted {
		t.Fatalf("unexpected first result: %+v", first)
	}
	if _, err := db.Exec(`UPDATE Sources SET status='completed', last_updated_at=CURRENT_TIMESTAMP WHERE source_id=42`); err != nil {
		t.Fatalf("complete source: %v", err)
	}
	replay, err := scheduleCrawlForTest(t, context.Background(), &handler, req)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if replay.State != CrawlOperationAccepted || !replay.Reused || replay.SourceState != CrawlSourceCompleted {
		t.Fatalf("unexpected replay result: %+v", replay)
	}
	if got := crawlOperationSourceStatus(t, db, 42); got != "completed" {
		t.Fatalf("replay rescheduled completed source to %q", got)
	}
	if got := crawlOperationStatusCount(t, db); got != 1 {
		t.Fatalf("replay created %d operation rows", got)
	}
}

func TestScheduleSourceCrawlRequestBudget(t *testing.T) {
	db, handler := newCrawlOperationTestDB(t)
	insertCrawlOperationSource(t, db, 42, "completed", false)
	req := crawlOperationRequest(42)
	req.MaxRequests = 1
	req.BudgetWindow = time.Hour
	first, err := scheduleCrawlForTest(t, context.Background(), &handler, req)
	if err != nil || first.State != CrawlOperationAccepted {
		t.Fatalf("first budgeted request: %+v, %v", first, err)
	}
	second := req
	second.RequestID = "request-2"
	budgeted, err := scheduleCrawlForTest(t, context.Background(), &handler, second)
	if err != nil {
		t.Fatalf("second budgeted request: %v", err)
	}
	if budgeted.State != CrawlOperationDenied || budgeted.ReasonCode != CrawlReasonBudgetExhausted {
		t.Fatalf("budget was not enforced: %+v", budgeted)
	}
	if got := crawlOperationSourceStatus(t, db, 42); got != "pending" {
		t.Fatalf("budget denial changed source to %q", got)
	}
	if got := crawlOperationStatusCount(t, db); got != 1 {
		t.Fatalf("budget denial created %d operation rows", got)
	}
	// A retry of the accepted logical request remains idempotent even when the
	// budget is exhausted; it must not create a second operation.
	replay, err := scheduleCrawlForTest(t, context.Background(), &handler, req)
	if err != nil {
		t.Fatalf("accepted replay: %v", err)
	}
	if !replay.Reused || replay.State != CrawlOperationAccepted {
		t.Fatalf("accepted replay changed outcome: %+v", replay)
	}
}

func TestScheduleSourceCrawlRequestQuotaIsOwnerIsolated(t *testing.T) {
	db, handler := newCrawlOperationTestDB(t)
	insertCrawlOperationSource(t, db, 42, "completed", false)
	base := crawlOperationRequest(42)
	base.MaxRequests = 10
	base.BudgetWindow = time.Hour
	first, err := scheduleCrawlForTest(t, context.Background(), &handler, base)
	if err != nil || first.State != CrawlOperationAccepted {
		t.Fatalf("owner A first request: %+v, %v", first, err)
	}
	ownerB := base
	ownerB.RequestID = "request-b"
	ownerB.Owner = "owner-b"
	ownerB.ApprovalID = "approval-owner-b"
	ownerB.ApprovalSHA256 = strings.Repeat("f", 64)
	ownerBResult, err := scheduleCrawlForTest(t, context.Background(), &handler, ownerB)
	if err != nil {
		t.Fatalf("owner B request: %v", err)
	}
	ownerC := base
	ownerC.RequestID = "request-c"
	ownerC.Owner = "owner-c"
	ownerC.ApprovalID = "approval-owner-c"
	ownerC.ApprovalSHA256 = strings.Repeat("1", 64)
	ownerCResult, err := scheduleCrawlForTest(t, context.Background(), &handler, ownerC)
	if err != nil {
		t.Fatalf("owner C request: %v", err)
	}
	// Make other owners' qualifying records newer than owner A's record.
	if _, err := db.Exec(`UPDATE Status SET created_at=? WHERE status_sha256=?`, "2026-11-07 11:59:00", first.OperationID); err != nil {
		t.Fatalf("age owner A record: %v", err)
	}
	for _, operationID := range []string{ownerBResult.OperationID, ownerCResult.OperationID} {
		if _, err := db.Exec(`UPDATE Status SET created_at=? WHERE status_sha256=?`, "2026-11-07 12:00:30", operationID); err != nil {
			t.Fatalf("newer owner record: %v", err)
		}
	}
	second := base
	second.RequestID = "request-a-2"
	second.MaxRequests = 1
	before := crawlOperationStatusCount(t, db)
	result, err := scheduleCrawlForTest(t, context.Background(), &handler, second)
	if err != nil {
		t.Fatalf("owner A second request: %v", err)
	}
	if result.State != CrawlOperationDenied || result.ReasonCode != CrawlReasonBudgetExhausted {
		t.Fatalf("owner A quota was bypassed by newer other-owner rows: %+v", result)
	}
	if after := crawlOperationStatusCount(t, db); after != before {
		t.Fatalf("owner A budget denial created a row: before=%d after=%d", before, after)
	}
	if got := crawlOperationSourceStatus(t, db, 42); got != "pending" {
		t.Fatalf("owner A budget denial changed source to %q", got)
	}
}

func TestScheduleSourceCrawlRequestRequiresPositiveBoundedQuota(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*CrawlScheduleRequest)
	}{
		{"zero quota", func(r *CrawlScheduleRequest) { r.MaxRequests = 0 }},
		{"negative quota", func(r *CrawlScheduleRequest) { r.MaxRequests = -1 }},
		{"excessive quota", func(r *CrawlScheduleRequest) { r.MaxRequests = maxCrawlRequests + 1 }},
		{"zero window", func(r *CrawlScheduleRequest) { r.BudgetWindow = 0 }},
		{"excessive window", func(r *CrawlScheduleRequest) { r.BudgetWindow = maxCrawlBudgetWindow + time.Second }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, handler := newCrawlOperationTestDB(t)
			insertCrawlOperationSource(t, db, 42, "completed", false)
			req := crawlOperationRequest(42)
			tc.mutate(&req)
			result, err := scheduleCrawlForTest(t, context.Background(), &handler, req)
			if err != nil {
				t.Fatalf("invalid quota returned operational error: %v", err)
			}
			if result.State != CrawlOperationDenied || result.ReasonCode != CrawlReasonInvalid {
				t.Fatalf("unexpected quota validation result: %+v", result)
			}
			if got := crawlOperationSourceStatus(t, db, 42); got != "completed" {
				t.Fatalf("invalid quota changed source to %q", got)
			}
			if got := crawlOperationStatusCount(t, db); got != 0 {
				t.Fatalf("invalid quota created %d operation rows", got)
			}
		})
	}
}

func TestScheduleSourceCrawlRequestTrustedClockBounds(t *testing.T) {
	now := crawlOperationNow()
	cases := []struct {
		name   string
		mutate func(*CrawlScheduleRequest)
		state  CrawlOperationState
		reason string
	}{
		{"expiry exactly now", func(r *CrawlScheduleRequest) { r.ExpiresAt = now }, CrawlOperationExpired, CrawlReasonExpired},
		{"backdated approval", func(r *CrawlScheduleRequest) { r.ApprovedAt = now.Add(-maxCrawlRequestLifetime - time.Second) }, CrawlOperationDenied, CrawlReasonInvalid},
		{"future approval", func(r *CrawlScheduleRequest) { r.ApprovedAt = now.Add(time.Second) }, CrawlOperationDenied, CrawlReasonInvalid},
		{"excessive lifetime", func(r *CrawlScheduleRequest) { r.ExpiresAt = now.Add(maxCrawlRequestLifetime + time.Second) }, CrawlOperationDenied, CrawlReasonInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, handler := newCrawlOperationTestDB(t)
			insertCrawlOperationSource(t, db, 42, "completed", false)
			req := crawlOperationRequest(42)
			tc.mutate(&req)
			result, err := scheduleCrawlForTest(t, context.Background(), &handler, req)
			if err != nil {
				t.Fatalf("clock boundary returned operational error: %v", err)
			}
			if result.State != tc.state || result.ReasonCode != tc.reason {
				t.Fatalf("unexpected clock boundary result: %+v", result)
			}
			if got := crawlOperationSourceStatus(t, db, 42); got != "completed" {
				t.Fatalf("clock boundary changed source to %q", got)
			}
			if got := crawlOperationStatusCount(t, db); got != 0 {
				t.Fatalf("clock boundary created %d operation rows", got)
			}
		})
	}
}

func TestScheduleSourceCrawlRequestRunAndApprovalBinding(t *testing.T) {
	t.Run("different run conflicts", func(t *testing.T) {
		db, handler := newCrawlOperationTestDB(t)
		insertCrawlOperationSource(t, db, 42, "completed", false)
		req := crawlOperationRequest(42)
		first, err := scheduleCrawlForTest(t, context.Background(), &handler, req)
		if err != nil {
			t.Fatalf("first schedule: %v", err)
		}
		changed := req
		changed.RunID = "run-2"
		result, err := scheduleCrawlForTest(t, context.Background(), &handler, changed)
		if err != nil {
			t.Fatalf("changed run: %v", err)
		}
		if result.State != CrawlOperationConflict || result.ReasonCode != CrawlReasonConflict {
			t.Fatalf("changed run reused operation: %+v", result)
		}
		if got := crawlOperationStatusState(t, db, first.OperationID); got != string(CrawlOperationAccepted) {
			t.Fatalf("changed run altered operation state to %q", got)
		}
		if got := crawlOperationStatusCount(t, db); got != 1 {
			t.Fatalf("changed run created %d operation rows", got)
		}
	})

	t.Run("different principal conflicts", func(t *testing.T) {
		db, handler := newCrawlOperationTestDB(t)
		insertCrawlOperationSource(t, db, 42, "completed", false)
		req := crawlOperationRequest(42)
		first, err := scheduleCrawlForTest(t, context.Background(), &handler, req)
		if err != nil {
			t.Fatalf("first schedule: %v", err)
		}
		changed := req
		changed.PrincipalUserID = 8
		result, err := scheduleCrawlForTest(t, context.Background(), &handler, changed)
		if err != nil {
			t.Fatalf("changed principal: %v", err)
		}
		if result.State != CrawlOperationConflict || result.ReasonCode != CrawlReasonConflict {
			t.Fatalf("changed principal reused operation: %+v", result)
		}
		if got := crawlOperationStatusState(t, db, first.OperationID); got != string(CrawlOperationAccepted) {
			t.Fatalf("changed principal altered operation state to %q", got)
		}
	})

	t.Run("changed expiry conflicts", func(t *testing.T) {
		db, handler := newCrawlOperationTestDB(t)
		insertCrawlOperationSource(t, db, 42, "completed", false)
		req := crawlOperationRequest(42)
		first, err := scheduleCrawlForTest(t, context.Background(), &handler, req)
		if err != nil {
			t.Fatalf("first schedule: %v", err)
		}
		changed := req
		changed.ExpiresAt = req.ExpiresAt.Add(time.Minute)
		result, err := scheduleCrawlForTest(t, context.Background(), &handler, changed)
		if err != nil {
			t.Fatalf("changed expiry: %v", err)
		}
		if result.State != CrawlOperationConflict || result.ReasonCode != CrawlReasonConflict {
			t.Fatalf("changed expiry reused operation: %+v", result)
		}
		if got := crawlOperationStatusState(t, db, first.OperationID); got != string(CrawlOperationAccepted) {
			t.Fatalf("changed expiry altered operation state to %q", got)
		}
	})

	t.Run("changed approval id conflicts", func(t *testing.T) {
		db, handler := newCrawlOperationTestDB(t)
		insertCrawlOperationSource(t, db, 42, "completed", false)
		req := crawlOperationRequest(42)
		if _, err := scheduleCrawlForTest(t, context.Background(), &handler, req); err != nil {
			t.Fatalf("first schedule: %v", err)
		}
		changed := req
		changed.ApprovalID = "approval-2"
		result, err := scheduleCrawlForTest(t, context.Background(), &handler, changed)
		if err != nil {
			t.Fatalf("changed approval id: %v", err)
		}
		if result.State != CrawlOperationConflict || result.ReasonCode != CrawlReasonConflict {
			t.Fatalf("changed approval id reused operation: %+v", result)
		}
	})

	t.Run("changed approval conflicts", func(t *testing.T) {
		db, handler := newCrawlOperationTestDB(t)
		insertCrawlOperationSource(t, db, 42, "completed", false)
		req := crawlOperationRequest(42)
		first, err := scheduleCrawlForTest(t, context.Background(), &handler, req)
		if err != nil {
			t.Fatalf("first schedule: %v", err)
		}
		changed := req
		changed.ApprovalSHA256 = strings.Repeat("d", 64)
		result, err := scheduleCrawlForTest(t, context.Background(), &handler, changed)
		if err != nil {
			t.Fatalf("changed approval: %v", err)
		}
		if result.State != CrawlOperationConflict || result.ReasonCode != CrawlReasonConflict {
			t.Fatalf("changed approval reused operation: %+v", result)
		}
		if got := crawlOperationStatusState(t, db, first.OperationID); got != string(CrawlOperationAccepted) {
			t.Fatalf("changed approval altered operation state to %q", got)
		}
	})
}

func TestScheduleSourceCrawlRequestQuotaWindowTransition(t *testing.T) {
	db, handler := newCrawlOperationTestDB(t)
	insertCrawlOperationSource(t, db, 42, "completed", false)
	req := crawlOperationRequest(42)
	req.MaxRequests = 1
	first, err := scheduleCrawlForTest(t, context.Background(), &handler, req)
	if err != nil || first.State != CrawlOperationAccepted {
		t.Fatalf("first windowed request: %+v, %v", first, err)
	}
	// Move the qualifying record outside the one-hour budget window.
	old := crawlOperationNow().Add(-2 * time.Hour).Format("2006-01-02 15:04:05")
	if _, err := db.Exec(`UPDATE Status SET created_at=? WHERE status_sha256=?`, old, first.OperationID); err != nil {
		t.Fatalf("age operation: %v", err)
	}
	// The first request left the source pending; reset it so the second request
	// exercises the quota rather than the already-pending path.
	if _, err := db.Exec(`UPDATE Sources SET status='completed' WHERE source_id=42`); err != nil {
		t.Fatalf("reset source: %v", err)
	}
	next := req
	next.RequestID = "request-2"
	result, err := scheduleCrawlForTest(t, context.Background(), &handler, next)
	if err != nil {
		t.Fatalf("post-window request: %v", err)
	}
	if result.State != CrawlOperationAccepted || result.ReasonCode != CrawlReasonScheduled {
		t.Fatalf("record outside the window still consumed quota: %+v", result)
	}
	if got := crawlOperationStatusCount(t, db); got != 2 {
		t.Fatalf("operation rows = %d, want 2", got)
	}
}

func TestScheduleSourceCrawlRequestMalformedQuotaRecordsFailClosed(t *testing.T) {
	cases := []struct {
		name    string
		details string
	}{
		{"owner-less record", `{"request_id":"other","agent_id":"agent-1","operation_type":"crawl"}`},
		{"empty owner record", `{"request_id":"other","agent_id":"agent-1","owner":"","operation_type":"crawl"}`},
		{"non-string owner record", `{"request_id":"other","agent_id":"agent-1","owner":7,"operation_type":"crawl"}`},
		{"invalid JSON record", `not-json`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, handler := newCrawlOperationTestDB(t)
			insertCrawlOperationSource(t, db, 42, "completed", false)
			if _, err := db.Exec(`INSERT INTO Status
				(status_sha256, agent_id, source_id, operation_type, status, details)
				VALUES (?, 'agent-1', 42, 'crawl', 'accepted', ?)`,
				strings.Repeat("e", 64), tc.details); err != nil {
				t.Fatalf("insert malformed record: %v", err)
			}
			before := crawlOperationStatusCount(t, db)
			result, err := scheduleCrawlForTest(t, context.Background(), &handler, crawlOperationRequest(42))
			if err == nil {
				t.Fatal("malformed quota record did not fail closed")
			}
			if result.State != CrawlOperationFailed || result.ReasonCode != CrawlReasonDatabase {
				t.Fatalf("unexpected malformed result: %+v", result)
			}
			if got := crawlOperationSourceStatus(t, db, 42); got != "completed" {
				t.Fatalf("malformed quota row changed source to %q", got)
			}
			if got := crawlOperationStatusCount(t, db); got != before {
				t.Fatalf("malformed quota row created new operations: before=%d after=%d", before, got)
			}
		})
	}
}

func TestScheduleSourceCrawlRequestFutureDatedQuotaRecordCounts(t *testing.T) {
	db, handler := newCrawlOperationTestDB(t)
	insertCrawlOperationSource(t, db, 42, "completed", false)
	req := crawlOperationRequest(42)
	req.MaxRequests = 1
	first, err := scheduleCrawlForTest(t, context.Background(), &handler, req)
	if err != nil || first.State != CrawlOperationAccepted {
		t.Fatalf("first request: %+v, %v", first, err)
	}
	future := crawlOperationNow().Add(24 * time.Hour).Format("2006-01-02 15:04:05")
	if _, err := db.Exec(`UPDATE Status SET created_at=? WHERE status_sha256=?`, future, first.OperationID); err != nil {
		t.Fatalf("future-date operation: %v", err)
	}
	next := req
	next.RequestID = "request-2"
	result, err := scheduleCrawlForTest(t, context.Background(), &handler, next)
	if err != nil {
		t.Fatalf("future-dated quota request: %v", err)
	}
	if result.State != CrawlOperationDenied || result.ReasonCode != CrawlReasonBudgetExhausted {
		t.Fatalf("future-dated record bypassed quota: %+v", result)
	}
}

func TestScheduleSourceCrawlRequestRejectsRevokedApproval(t *testing.T) {
	db, handler := newCrawlOperationTestDB(t)
	insertCrawlOperationSource(t, db, 42, "completed", false)
	req := crawlOperationRequest(42)
	ensureCrawlApprovalFixture(t, db, req)
	if _, err := db.Exec(`UPDATE CrawlApprovals SET revoked_at=CURRENT_TIMESTAMP WHERE approval_id=?`, req.ApprovalID); err != nil {
		t.Fatalf("revoke approval fixture: %v", err)
	}
	result, err := scheduleCrawlForTest(t, context.Background(), &handler, req)
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if result.State != CrawlOperationDenied || result.ReasonCode != CrawlReasonApprovalRejected {
		t.Fatalf("unexpected result: %+v", result)
	}
	if got := crawlOperationSourceStatus(t, db, 42); got != "completed" {
		t.Fatalf("revoked approval changed source to %q", got)
	}
	if got := crawlOperationStatusCount(t, db); got != 0 {
		t.Fatalf("revoked approval created %d operation rows", got)
	}
}

func TestScheduleSourceCrawlRequestRejectsApprovalMetadataMismatch(t *testing.T) {
	db, handler := newCrawlOperationTestDB(t)
	insertCrawlOperationSource(t, db, 42, "completed", false)
	req := crawlOperationRequest(42)
	ensureCrawlApprovalFixture(t, db, req)
	if _, err := db.Exec(`UPDATE CrawlApprovals SET approved_by='attacker', approval_sha256=? WHERE approval_id=?`,
		strings.Repeat("9", 64), req.ApprovalID); err != nil {
		t.Fatalf("alter approval fixture: %v", err)
	}
	result, err := scheduleCrawlForTest(t, context.Background(), &handler, req)
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if result.State != CrawlOperationDenied || result.ReasonCode != CrawlReasonApprovalRejected {
		t.Fatalf("unexpected result: %+v", result)
	}
	if got := crawlOperationStatusCount(t, db); got != 0 {
		t.Fatalf("mismatched approval created %d operation rows", got)
	}
}

func TestScheduleSourceCrawlRequestRejectsExpiredApproval(t *testing.T) {
	db, handler := newCrawlOperationTestDB(t)
	insertCrawlOperationSource(t, db, 42, "completed", false)
	req := crawlOperationRequest(42)
	ensureCrawlApprovalFixture(t, db, req)
	if _, err := db.Exec(`UPDATE CrawlApprovals SET approved_at=?, expires_at=? WHERE approval_id=?`,
		crawlOperationNow().Add(-2*time.Hour), crawlOperationNow().Add(-time.Hour), req.ApprovalID); err != nil {
		t.Fatalf("expire approval fixture: %v", err)
	}
	result, err := scheduleCrawlForTest(t, context.Background(), &handler, req)
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if result.State != CrawlOperationDenied || result.ReasonCode != CrawlReasonApprovalRejected {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestScheduleSourceCrawlRequestRejectsTransferredOrUnownedSource(t *testing.T) {
	t.Run("transferred", func(t *testing.T) {
		db, handler := newCrawlOperationTestDB(t)
		insertCrawlOperationSource(t, db, 42, "completed", false)
		req := crawlOperationRequest(42)
		if _, err := db.Exec(`UPDATE Sources SET usr_id=8 WHERE source_id=42`); err != nil {
			t.Fatalf("transfer source: %v", err)
		}
		result, err := scheduleCrawlForTest(t, context.Background(), &handler, req)
		if err != nil {
			t.Fatalf("schedule: %v", err)
		}
		if result.State != CrawlOperationDenied || result.ReasonCode != CrawlReasonOwnershipRejected {
			t.Fatalf("unexpected result: %+v", result)
		}
		if got := crawlOperationSourceStatus(t, db, 42); got != "completed" {
			t.Fatalf("transferred source changed to %q", got)
		}
		if got := crawlOperationStatusCount(t, db); got != 0 {
			t.Fatalf("transferred source created %d operation rows", got)
		}
	})

	t.Run("zero owner", func(t *testing.T) {
		db, handler := newCrawlOperationTestDB(t)
		insertCrawlOperationSourceWithOwner(t, db, 42, "completed", false, 0)
		result, err := scheduleCrawlForTest(t, context.Background(), &handler, crawlOperationRequest(42))
		if err != nil {
			t.Fatalf("schedule: %v", err)
		}
		if result.State != CrawlOperationDenied || result.ReasonCode != CrawlReasonOwnershipRejected {
			t.Fatalf("unexpected result: %+v", result)
		}
		if got := crawlOperationStatusCount(t, db); got != 0 {
			t.Fatalf("unowned source created %d operation rows", got)
		}
	})
}

func TestScheduleSourceCrawlRequestApprovalUnavailableFailsClosed(t *testing.T) {
	db, handler := newCrawlOperationTestDB(t)
	insertCrawlOperationSource(t, db, 42, "completed", false)
	req := crawlOperationRequest(42)
	if _, err := db.Exec(`DROP TABLE CrawlApprovals`); err != nil {
		t.Fatalf("drop approvals: %v", err)
	}
	result, err := scheduleCrawlForTest(t, context.Background(), &handler, req)
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if result.State != CrawlOperationDenied || result.ReasonCode != CrawlReasonApprovalUnavailable {
		t.Fatalf("unexpected result: %+v", result)
	}
	if got := crawlOperationSourceStatus(t, db, 42); got != "completed" {
		t.Fatalf("missing approval schema changed source to %q", got)
	}
	if got := crawlOperationStatusCount(t, db); got != 0 {
		t.Fatalf("missing approval schema created %d operation rows", got)
	}
}

func TestScheduleSourceCrawlRequestRetrySurvivesApprovalRevocation(t *testing.T) {
	db, handler := newCrawlOperationTestDB(t)
	insertCrawlOperationSource(t, db, 42, "completed", false)
	req := crawlOperationRequest(42)
	first, err := scheduleCrawlForTest(t, context.Background(), &handler, req)
	if err != nil || first.State != CrawlOperationAccepted {
		t.Fatalf("first schedule: %+v, %v", first, err)
	}
	if _, err := db.Exec(`UPDATE CrawlApprovals SET revoked_at=CURRENT_TIMESTAMP WHERE approval_id=?`, req.ApprovalID); err != nil {
		t.Fatalf("revoke approval: %v", err)
	}
	replay, err := scheduleCrawlForTest(t, context.Background(), &handler, req)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if replay.State != CrawlOperationAccepted || !replay.Reused || replay.OperationID != first.OperationID {
		t.Fatalf("legitimate retry did not reuse operation: %+v", replay)
	}
	if got := crawlOperationStatusCount(t, db); got != 1 {
		t.Fatalf("retry created %d operation rows", got)
	}
}

func TestScheduleSourceCrawlRequestRejectsUnverifiedLegacyOwnership(t *testing.T) {
	db, handler := newCrawlOperationTestDB(t)
	insertCrawlOperationSourceWithOwner(t, db, 42, "completed", false, 7)
	if _, err := db.Exec(`DELETE FROM SourceOwnership WHERE source_id=42`); err != nil {
		t.Fatalf("remove source provenance: %v", err)
	}
	result, err := scheduleCrawlForTest(t, context.Background(), &handler, crawlOperationRequest(42))
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if result.State != CrawlOperationDenied || result.ReasonCode != CrawlReasonOwnershipRejected {
		t.Fatalf("unexpected result: %+v", result)
	}
	if got := crawlOperationSourceStatus(t, db, 42); got != "completed" {
		t.Fatalf("unverified ownership changed source to %q", got)
	}
	if got := crawlOperationStatusCount(t, db); got != 0 {
		t.Fatalf("unverified ownership created %d operation rows", got)
	}
}

func TestScheduleSourceCrawlRequestRejectsInactiveOrDemotedApprover(t *testing.T) {
	t.Run("disabled approver", func(t *testing.T) {
		db, handler := newCrawlOperationTestDB(t)
		insertCrawlOperationSource(t, db, 42, "completed", false)
		req := crawlOperationRequest(42)
		ensureCrawlApprovalFixture(t, db, req)
		if _, err := db.Exec(`UPDATE Users SET disabled=TRUE WHERE user_id=9`); err != nil {
			t.Fatalf("disable approver: %v", err)
		}
		result, err := scheduleCrawlForTest(t, context.Background(), &handler, req)
		if err != nil {
			t.Fatalf("schedule: %v", err)
		}
		if result.State != CrawlOperationDenied || result.ReasonCode != CrawlReasonApprovalRejected {
			t.Fatalf("unexpected result: %+v", result)
		}
		if got := crawlOperationStatusCount(t, db); got != 0 {
			t.Fatalf("disabled approver created %d operation rows", got)
		}
	})

	t.Run("disabled issuer", func(t *testing.T) {
		db, handler := newCrawlOperationTestDB(t)
		insertCrawlOperationSource(t, db, 42, "completed", false)
		req := crawlOperationRequest(42)
		ensureCrawlApprovalFixture(t, db, req)
		if _, err := db.Exec(`UPDATE Users SET disabled=TRUE WHERE user_id=7`); err != nil {
			t.Fatalf("disable issuer: %v", err)
		}
		result, err := scheduleCrawlForTest(t, context.Background(), &handler, req)
		if err != nil {
			t.Fatalf("schedule: %v", err)
		}
		if result.State != CrawlOperationDenied || result.ReasonCode != CrawlReasonApprovalRejected {
			t.Fatalf("unexpected result: %+v", result)
		}
		if got := crawlOperationStatusCount(t, db); got != 0 {
			t.Fatalf("disabled issuer created %d operation rows", got)
		}
	})

	t.Run("revoked administrator role", func(t *testing.T) {
		db, handler := newCrawlOperationTestDB(t)
		insertCrawlOperationSource(t, db, 42, "completed", false)
		req := crawlOperationRequest(42)
		ensureCrawlApprovalFixture(t, db, req)
		if _, err := db.Exec(`DELETE FROM UserRoles WHERE user_id=9 AND role_id=1`); err != nil {
			t.Fatalf("revoke administrator role: %v", err)
		}
		result, err := scheduleCrawlForTest(t, context.Background(), &handler, req)
		if err != nil {
			t.Fatalf("schedule: %v", err)
		}
		if result.State != CrawlOperationDenied || result.ReasonCode != CrawlReasonApprovalRejected {
			t.Fatalf("unexpected result: %+v", result)
		}
		if got := crawlOperationStatusCount(t, db); got != 0 {
			t.Fatalf("demoted approver created %d operation rows", got)
		}
	})
}

func TestScheduleSourceCrawlRequestRejectsExpiredProposalBinding(t *testing.T) {
	db, handler := newCrawlOperationTestDB(t)
	insertCrawlOperationSource(t, db, 42, "completed", false)
	req := crawlOperationRequest(42)
	ensureCrawlApprovalFixture(t, db, req)
	if _, err := db.Exec(`UPDATE CrawlApprovals SET proposal_expires_at=? WHERE approval_id=?`,
		crawlOperationNow().Add(-time.Minute), req.ApprovalID); err != nil {
		t.Fatalf("expire proposal binding: %v", err)
	}
	result, err := scheduleCrawlForTest(t, context.Background(), &handler, req)
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if result.State != CrawlOperationDenied || result.ReasonCode != CrawlReasonApprovalRejected {
		t.Fatalf("unexpected result: %+v", result)
	}
	if got := crawlOperationStatusCount(t, db); got != 0 {
		t.Fatalf("expired proposal binding created %d operation rows", got)
	}
}
