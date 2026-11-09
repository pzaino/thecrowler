package database

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func newCrawlProposalTestDB(t *testing.T) (*sql.DB, Handler) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE CrawlProposals (
		request_id CHAR(64) PRIMARY KEY,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		last_updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		run_id TEXT NOT NULL,
		tool_call_id TEXT NOT NULL,
		agent_id TEXT NOT NULL,
		owner TEXT NOT NULL,
		principal_user_id INTEGER NOT NULL,
		source_id INTEGER NOT NULL,
		operation_type TEXT NOT NULL,
		proposal_sha256 CHAR(64) NOT NULL,
		trace_id TEXT NOT NULL DEFAULT '',
		max_requests INTEGER NOT NULL,
		budget_window_seconds INTEGER NOT NULL,
		policy_json TEXT NOT NULL,
		state TEXT NOT NULL DEFAULT 'pending',
		expires_at TIMESTAMP NOT NULL,
		submitted_at TIMESTAMP,
		operation_id TEXT,
		UNIQUE (run_id, tool_call_id, source_id, operation_type),
		CHECK (state IN ('pending','submitted','expired','denied'))
	)`); err != nil {
		t.Fatalf("create CrawlProposals: %v", err)
	}
	return db, Handler(&SQLiteHandler{db: db, dbms: DBSQLiteStr})
}

func insertProposalFixture(t *testing.T, handler *Handler, requestID string, expiresAt time.Time) CrawlProposalRow {
	t.Helper()
	row := CrawlProposalRow{
		RequestID: requestID, RunID: "run-" + requestID[:8], ToolCallID: "call-" + requestID[:8],
		AgentID: "agent-1", Owner: "owner-1", PrincipalUserID: 7,
		SourceID: 42, OperationType: CrawlOperationType,
		ProposalSHA256: strings.Repeat("a", 64), TraceID: "trace-1",
		MaxRequests: 2, BudgetWindowSeconds: 3600, PolicyJSON: "{}",
		State: CrawlProposalStatePending, ExpiresAt: expiresAt.UTC().Truncate(time.Microsecond),
	}
	inserted, err := InsertCrawlProposal(context.Background(), handler, row)
	if err != nil || !inserted {
		t.Fatalf("insert proposal: inserted=%v err=%v", inserted, err)
	}
	return row
}

func readProposalState(t *testing.T, handler *Handler, requestID string) CrawlProposalRow {
	t.Helper()
	row, found, err := GetCrawlProposalByRequest(context.Background(), handler, requestID)
	if err != nil || !found {
		t.Fatalf("read proposal: found=%v err=%v", found, err)
	}
	return row
}

func TestCrawlProposalTransitionsAreStrictCompareAndSet(t *testing.T) {
	_, handler := newCrawlProposalTestDB(t)
	now := time.Now().UTC()
	requestID := strings.Repeat("1", 64)
	insertProposalFixture(t, &handler, requestID, now.Add(time.Hour))

	applied, err := MarkCrawlProposalSubmitted(context.Background(), &handler, requestID, strings.Repeat("b", 64), now)
	if err != nil || applied.Outcome != CrawlProposalTransitionApplied {
		t.Fatalf("pending -> submitted: outcome=%s err=%v", applied.Outcome, err)
	}
	row := readProposalState(t, &handler, requestID)
	if row.State != CrawlProposalStateSubmitted || !row.OperationID.Valid ||
		row.OperationID.String != strings.Repeat("b", 64) || !row.SubmittedAt.Valid {
		t.Fatalf("submitted state not durable: %+v", row)
	}
	again, err := MarkCrawlProposalSubmitted(context.Background(), &handler, requestID, strings.Repeat("b", 64), now)
	if err != nil || again.Outcome != CrawlProposalTransitionAlready {
		t.Fatalf("repeat submitted: outcome=%s err=%v", again.Outcome, err)
	}
	conflict, err := MarkCrawlProposalSubmitted(context.Background(), &handler, requestID, strings.Repeat("c", 64), now)
	if !errors.Is(err, ErrCrawlProposalStateConflict) || conflict.Outcome != CrawlProposalTransitionConflict {
		t.Fatalf("different operation: outcome=%s err=%v", conflict.Outcome, err)
	}
	expire, err := MarkCrawlProposalExpired(context.Background(), &handler, requestID)
	if !errors.Is(err, ErrCrawlProposalStateConflict) || expire.Outcome != CrawlProposalTransitionConflict {
		t.Fatalf("submitted -> expired: outcome=%s err=%v", expire.Outcome, err)
	}
	row = readProposalState(t, &handler, requestID)
	if row.State != CrawlProposalStateSubmitted {
		t.Fatalf("submitted proposal changed to %q", row.State)
	}
}

func TestCrawlProposalExpiryAndDenialTransitions(t *testing.T) {
	_, handler := newCrawlProposalTestDB(t)
	now := time.Now().UTC()
	expireID := strings.Repeat("2", 64)
	insertProposalFixture(t, &handler, expireID, now.Add(time.Hour))
	applied, err := MarkCrawlProposalExpired(context.Background(), &handler, expireID)
	if err != nil || applied.Outcome != CrawlProposalTransitionApplied {
		t.Fatalf("pending -> expired: outcome=%s err=%v", applied.Outcome, err)
	}
	again, err := MarkCrawlProposalExpired(context.Background(), &handler, expireID)
	if err != nil || again.Outcome != CrawlProposalTransitionAlready {
		t.Fatalf("repeat expired: outcome=%s err=%v", again.Outcome, err)
	}
	submit, err := MarkCrawlProposalSubmitted(context.Background(), &handler, expireID, strings.Repeat("d", 64), now)
	if !errors.Is(err, ErrCrawlProposalStateConflict) || submit.Outcome != CrawlProposalTransitionConflict {
		t.Fatalf("expired -> submitted: outcome=%s err=%v", submit.Outcome, err)
	}
	reconcile, err := ReconcileCrawlProposalSubmitted(context.Background(), &handler, expireID, strings.Repeat("d", 64), now)
	if err != nil || reconcile.Outcome != CrawlProposalTransitionApplied {
		t.Fatalf("expired -> submitted reconciliation: outcome=%s err=%v", reconcile.Outcome, err)
	}
	row := readProposalState(t, &handler, expireID)
	if row.State != CrawlProposalStateSubmitted || row.OperationID.String != strings.Repeat("d", 64) {
		t.Fatalf("reconciled proposal = %+v", row)
	}
	denyID := strings.Repeat("3", 64)
	insertProposalFixture(t, &handler, denyID, now.Add(time.Hour))
	denied, err := MarkCrawlProposalDenied(context.Background(), &handler, denyID)
	if err != nil || denied.Outcome != CrawlProposalTransitionApplied {
		t.Fatalf("pending -> denied: outcome=%s err=%v", denied.Outcome, err)
	}
	submit, err = MarkCrawlProposalSubmitted(context.Background(), &handler, denyID, strings.Repeat("e", 64), now)
	if !errors.Is(err, ErrCrawlProposalStateConflict) || submit.Outcome != CrawlProposalTransitionConflict {
		t.Fatalf("denied -> submitted: outcome=%s err=%v", submit.Outcome, err)
	}
}

func TestCrawlProposalTransitionFailuresAreExplicit(t *testing.T) {
	_, handler := newCrawlProposalTestDB(t)
	now := time.Now().UTC()
	if _, err := MarkCrawlProposalSubmitted(context.Background(), &handler, strings.Repeat("9", 64), strings.Repeat("a", 64), now); !errors.Is(err, ErrCrawlProposalRowNotFound) {
		t.Fatalf("unknown proposal error = %v", err)
	}
	requestID := strings.Repeat("4", 64)
	insertProposalFixture(t, &handler, requestID, now.Add(time.Hour))
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := MarkCrawlProposalExpired(cancelled, &handler, requestID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled transition error = %v", err)
	}
	deadline, cancelDeadline := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelDeadline()
	if _, err := MarkCrawlProposalExpired(deadline, &handler, requestID); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline transition error = %v", err)
	}
	if row := readProposalState(t, &handler, requestID); row.State != CrawlProposalStatePending {
		t.Fatalf("failed transitions changed state to %q", row.State)
	}
	if _, err := handler.Exec(`CREATE TRIGGER fail_proposal_update BEFORE UPDATE ON CrawlProposals BEGIN SELECT RAISE(ABORT, 'injected failure'); END;`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}
	transition, err := MarkCrawlProposalSubmitted(context.Background(), &handler, requestID, strings.Repeat("f", 64), now)
	if err == nil || transition.Outcome == CrawlProposalTransitionApplied {
		t.Fatalf("injected database failure was reported as success: outcome=%s err=%v", transition.Outcome, err)
	}
	if row := readProposalState(t, &handler, requestID); row.State != CrawlProposalStatePending {
		t.Fatalf("failed transition changed state to %q", row.State)
	}
}

func TestCrawlProposalConcurrentSubmissionAndExpiry(t *testing.T) {
	_, handler := newCrawlProposalTestDB(t)
	now := time.Now().UTC()
	requestID := strings.Repeat("5", 64)
	insertProposalFixture(t, &handler, requestID, now.Add(time.Hour))
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, err := MarkCrawlProposalSubmitted(context.Background(), &handler, requestID, strings.Repeat("a", 64), now)
		results <- err
	}()
	go func() {
		defer wg.Done()
		<-start
		_, err := MarkCrawlProposalExpired(context.Background(), &handler, requestID)
		results <- err
	}()
	close(start)
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent transitions succeeded %d times, want 1", successes)
	}
	row := readProposalState(t, &handler, requestID)
	if row.State != CrawlProposalStateSubmitted && row.State != CrawlProposalStateExpired {
		t.Fatalf("concurrent transition left invalid state %q", row.State)
	}
}
