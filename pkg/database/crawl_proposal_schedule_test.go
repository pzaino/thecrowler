package database

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

func createScheduleProposalTable(t *testing.T, db *sql.DB) {
	t.Helper()
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
}

func insertScheduleProposal(t *testing.T, handler *Handler, req CrawlScheduleRequest, expiresAt time.Time) CrawlProposalRow {
	t.Helper()
	row := CrawlProposalRow{
		RequestID: req.RequestID, RunID: req.RunID, ToolCallID: "tool-call-1",
		AgentID: req.AgentID, Owner: req.Owner, PrincipalUserID: req.PrincipalUserID,
		SourceID: req.SourceID, OperationType: req.OperationType,
		ProposalSHA256: req.ParametersSHA256, TraceID: "trace-1",
		MaxRequests: req.MaxRequests, BudgetWindowSeconds: int64(req.BudgetWindow / time.Second),
		PolicyJSON: "{}", State: CrawlProposalStatePending,
		ExpiresAt: expiresAt.UTC().Truncate(time.Microsecond),
	}
	inserted, err := InsertCrawlProposal(context.Background(), handler, row)
	if err != nil || !inserted {
		t.Fatalf("insert schedule proposal: inserted=%v err=%v", inserted, err)
	}
	return row
}

func TestScheduleSourceCrawlRequestForProposalCommitsBothRecords(t *testing.T) {
	db, handler := newCrawlOperationTestDB(t)
	createScheduleProposalTable(t, db)
	insertCrawlOperationSource(t, db, 42, "completed", false)
	req := crawlOperationRequest(42)
	proposal := insertScheduleProposal(t, &handler, req, crawlOperationNow().Add(time.Hour))
	ensureCrawlApprovalFixture(t, db, req)

	result, err := scheduleSourceCrawlRequestForProposal(context.Background(), &handler, req, proposal.RequestID, proposal.ProposalSHA256, crawlOperationNow())
	if err != nil {
		t.Fatalf("schedule for proposal: %v", err)
	}
	if result.State != CrawlOperationAccepted || result.Reused {
		t.Fatalf("unexpected scheduling result: %+v", result)
	}
	if got := crawlOperationSourceStatus(t, db, 42); got != "pending" {
		t.Fatalf("source status = %q, want pending", got)
	}
	stored := readProposalState(t, &handler, proposal.RequestID)
	if stored.State != CrawlProposalStateSubmitted || stored.OperationID.String != result.OperationID {
		t.Fatalf("proposal not submitted atomically: %+v", stored)
	}
	if got := crawlOperationStatusState(t, db, result.OperationID); got != string(CrawlOperationAccepted) {
		t.Fatalf("operation state = %q", got)
	}
	retry, err := scheduleSourceCrawlRequestForProposal(context.Background(), &handler, req, proposal.RequestID, proposal.ProposalSHA256, crawlOperationNow())
	if err != nil || !retry.Reused || retry.OperationID != result.OperationID {
		t.Fatalf("retry result = %+v err=%v", retry, err)
	}
	if got := crawlOperationStatusCount(t, db); got != 1 {
		t.Fatalf("retry created %d operation rows", got)
	}
}

func TestScheduleSourceCrawlRequestForProposalExpiredWithoutOperation(t *testing.T) {
	db, handler := newCrawlOperationTestDB(t)
	createScheduleProposalTable(t, db)
	insertCrawlOperationSource(t, db, 42, "completed", false)
	req := crawlOperationRequest(42)
	proposal := insertScheduleProposal(t, &handler, req, crawlOperationNow().Add(-time.Hour))
	ensureCrawlApprovalFixture(t, db, req)

	result, err := scheduleSourceCrawlRequestForProposal(context.Background(), &handler, req, proposal.RequestID, proposal.ProposalSHA256, crawlOperationNow())
	if err != nil {
		t.Fatalf("expired proposal returned error: %v", err)
	}
	if result.State != CrawlOperationExpired || result.ReasonCode != CrawlReasonExpired {
		t.Fatalf("expired proposal result = %+v", result)
	}
	if got := crawlOperationSourceStatus(t, db, 42); got != "completed" {
		t.Fatalf("expired proposal changed source status to %q", got)
	}
	if got := crawlOperationStatusCount(t, db); got != 0 {
		t.Fatalf("expired proposal wrote %d operation rows", got)
	}
	stored := readProposalState(t, &handler, proposal.RequestID)
	if stored.State != CrawlProposalStateExpired {
		t.Fatalf("expired proposal state = %q", stored.State)
	}
}

func TestScheduleSourceCrawlRequestForProposalReconcilesCommittedOperation(t *testing.T) {
	db, handler := newCrawlOperationTestDB(t)
	createScheduleProposalTable(t, db)
	insertCrawlOperationSource(t, db, 42, "completed", false)
	req := crawlOperationRequest(42)
	proposal := insertScheduleProposal(t, &handler, req, crawlOperationNow().Add(time.Hour))
	ensureCrawlApprovalFixture(t, db, req)

	first, err := scheduleCrawlForTest(t, context.Background(), &handler, req)
	if err != nil || first.State != CrawlOperationAccepted {
		t.Fatalf("seed operation: %+v err=%v", first, err)
	}
	if _, err := MarkCrawlProposalExpired(context.Background(), &handler, proposal.RequestID); err != nil {
		t.Fatalf("expire proposal before reconciliation: %v", err)
	}
	reconciled, err := scheduleSourceCrawlRequestForProposal(context.Background(), &handler, req, proposal.RequestID, proposal.ProposalSHA256, crawlOperationNow())
	if err != nil {
		t.Fatalf("reconcile committed operation: %v", err)
	}
	if !reconciled.Reused || reconciled.OperationID != first.OperationID || reconciled.State != CrawlOperationAccepted {
		t.Fatalf("reconciled result = %+v", reconciled)
	}
	stored := readProposalState(t, &handler, proposal.RequestID)
	if stored.State != CrawlProposalStateSubmitted || stored.OperationID.String != first.OperationID {
		t.Fatalf("reconciled proposal = %+v", stored)
	}
	if got := crawlOperationStatusCount(t, db); got != 1 {
		t.Fatalf("reconciliation created %d operation rows", got)
	}
}

func TestScheduleSourceCrawlRequestForProposalRollsBackOnTransitionFailure(t *testing.T) {
	db, handler := newCrawlOperationTestDB(t)
	createScheduleProposalTable(t, db)
	insertCrawlOperationSource(t, db, 42, "completed", false)
	req := crawlOperationRequest(42)
	proposal := insertScheduleProposal(t, &handler, req, crawlOperationNow().Add(time.Hour))
	ensureCrawlApprovalFixture(t, db, req)
	if _, err := db.Exec(`CREATE TRIGGER fail_schedule_proposal_update BEFORE UPDATE ON CrawlProposals
		BEGIN SELECT RAISE(ABORT, 'injected proposal transition failure'); END;`); err != nil {
		t.Fatalf("create transition failure trigger: %v", err)
	}
	_, err := scheduleSourceCrawlRequestForProposal(context.Background(), &handler, req, proposal.RequestID, proposal.ProposalSHA256, crawlOperationNow())
	if err == nil {
		t.Fatalf("proposal transition failure was reported as scheduling success")
	}
	if got := crawlOperationSourceStatus(t, db, 42); got != "completed" {
		t.Fatalf("rolled-back scheduling changed source status to %q", got)
	}
	if got := crawlOperationStatusCount(t, db); got != 0 {
		t.Fatalf("rolled-back scheduling wrote %d operation rows", got)
	}
	stored := readProposalState(t, &handler, proposal.RequestID)
	if stored.State != CrawlProposalStatePending {
		t.Fatalf("rolled-back proposal state = %q", stored.State)
	}
}

func TestScheduleSourceCrawlRequestForProposalRejectsSubmittedWithoutOperation(t *testing.T) {
	db, handler := newCrawlOperationTestDB(t)
	createScheduleProposalTable(t, db)
	insertCrawlOperationSource(t, db, 42, "completed", false)
	req := crawlOperationRequest(42)
	proposal := insertScheduleProposal(t, &handler, req, crawlOperationNow().Add(time.Hour))
	ensureCrawlApprovalFixture(t, db, req)
	if _, err := MarkCrawlProposalSubmitted(context.Background(), &handler, proposal.RequestID, strings.Repeat("f", 64), time.Now().UTC()); err != nil {
		t.Fatalf("mark submitted fixture: %v", err)
	}
	result, err := scheduleSourceCrawlRequestForProposal(context.Background(), &handler, req, proposal.RequestID, proposal.ProposalSHA256, crawlOperationNow())
	if !errors.Is(err, ErrCrawlProposalStateConflict) {
		t.Fatalf("submitted-without-operation error = %v", err)
	}
	if result.State != CrawlOperationUncertain || result.ReasonCode != CrawlReasonProposalInconsistent {
		t.Fatalf("submitted-without-operation result = %+v", result)
	}
	if got := crawlOperationStatusCount(t, db); got != 0 {
		t.Fatalf("inconsistent proposal created %d operation rows", got)
	}
	if got := crawlOperationSourceStatus(t, db, 42); got != "completed" {
		t.Fatalf("inconsistent proposal changed source status to %q", got)
	}
}
