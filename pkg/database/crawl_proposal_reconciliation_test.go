package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func insertReconciliationOperation(t *testing.T, handler *Handler, key, status string, details crawlOperationDetails) {
	t.Helper()
	raw, err := json.Marshal(details)
	if err != nil {
		t.Fatalf("marshal operation details: %v", err)
	}
	if _, err := (*handler).Exec(`INSERT INTO Status
		(status_sha256, agent_id, source_id, operation_type, status, details)
		VALUES (?, ?, ?, ?, ?, ?)`,
		key, details.AgentID, details.SourceID, details.OperationType, status, string(raw)); err != nil {
		t.Fatalf("insert reconciliation operation: %v", err)
	}
}

func insertRawReconciliationOperation(t *testing.T, handler *Handler, key, status, raw string) {
	t.Helper()
	if _, err := (*handler).Exec(`INSERT INTO Status
		(status_sha256, agent_id, source_id, operation_type, status, details)
		VALUES (?, 'agent-1', 42, 'crawl', ?, ?)`, key, status, raw); err != nil {
		t.Fatalf("insert raw reconciliation operation: %v", err)
	}
}

func reconciliationFixture(t *testing.T) (*sql.DB, Handler, CrawlScheduleRequest, CrawlProposalRow, string) {
	t.Helper()
	db, handler := newCrawlOperationTestDB(t)
	createScheduleProposalTable(t, db)
	insertCrawlOperationSource(t, db, 42, "completed", false)
	req := crawlOperationRequest(42)
	proposal := insertScheduleProposal(t, &handler, req, crawlOperationNow().Add(time.Hour))
	ensureCrawlApprovalFixture(t, db, req)
	key := CrawlOperationKey(req.Owner, req.AgentID, req.SourceID, req.OperationType, req.RequestID)
	return db, handler, req, proposal, key
}

func TestReconcileCrawlProposalOperationOutcomes(t *testing.T) {
	t.Run("not found", func(t *testing.T) {
		_, handler, _, proposal, _ := reconciliationFixture(t)
		reconciliation, err := ReconcileCrawlProposalOperation(context.Background(), &handler, proposal)
		if err != nil || reconciliation.Outcome != CrawlOperationReconciliationNotFound {
			t.Fatalf("not-found reconciliation = %+v err=%v", reconciliation, err)
		}
	})
	t.Run("matching operation", func(t *testing.T) {
		db, handler, req, proposal, key := reconciliationFixture(t)
		details, err := crawlOperationDetailsFor(req)
		if err != nil {
			t.Fatalf("operation details: %v", err)
		}
		details.ReasonCode = CrawlReasonScheduled
		insertReconciliationOperation(t, &handler, key, string(CrawlOperationAccepted), details)
		reconciliation, err := ReconcileCrawlProposalOperation(context.Background(), &handler, proposal)
		if err != nil || reconciliation.Outcome != CrawlOperationReconciliationFound ||
			reconciliation.Result.State != CrawlOperationAccepted || !reconciliation.Result.Reused {
			t.Fatalf("matching reconciliation = %+v err=%v", reconciliation, err)
		}
		stored := readProposalState(t, &handler, proposal.RequestID)
		if stored.State != CrawlProposalStateSubmitted || stored.OperationID.String != key {
			t.Fatalf("matching operation did not reconcile proposal: %+v", stored)
		}
		if count := crawlOperationStatusCount(t, db); count != 1 {
			t.Fatalf("reconciliation changed operation count to %d", count)
		}
	})
	t.Run("conflicting operation", func(t *testing.T) {
		_, handler, req, proposal, key := reconciliationFixture(t)
		details, err := crawlOperationDetailsFor(req)
		if err != nil {
			t.Fatalf("operation details: %v", err)
		}
		details.SourceID = 43
		insertReconciliationOperation(t, &handler, key, string(CrawlOperationAccepted), details)
		reconciliation, err := ReconcileCrawlProposalOperation(context.Background(), &handler, proposal)
		if !errors.Is(err, ErrCrawlOperationConflict) ||
			reconciliation.Outcome != CrawlOperationReconciliationConflict || reconciliation.Result.Reused {
			t.Fatalf("conflict reconciliation = %+v err=%v", reconciliation, err)
		}
		stored := readProposalState(t, &handler, proposal.RequestID)
		if stored.State != CrawlProposalStatePending {
			t.Fatalf("conflict changed proposal state to %q", stored.State)
		}
	})
	t.Run("malformed metadata", func(t *testing.T) {
		db, handler, _, proposal, key := reconciliationFixture(t)
		insertRawReconciliationOperation(t, &handler, key, string(CrawlOperationAccepted), `{"request_id":`)
		reconciliation, err := ReconcileCrawlProposalOperation(context.Background(), &handler, proposal)
		if !errors.Is(err, ErrCrawlOperationMetadataInvalid) ||
			reconciliation.Outcome != CrawlOperationReconciliationInvalidMetadata {
			t.Fatalf("malformed reconciliation = %+v err=%v", reconciliation, err)
		}
		if count := crawlOperationStatusCount(t, db); count != 1 {
			t.Fatalf("malformed metadata changed operation count to %d", count)
		}
		if stored := readProposalState(t, &handler, proposal.RequestID); stored.State != CrawlProposalStatePending {
			t.Fatalf("malformed metadata changed proposal state to %q", stored.State)
		}
	})
	t.Run("missing operation timestamp", func(t *testing.T) {
		_, handler, req, proposal, key := reconciliationFixture(t)
		details, _ := crawlOperationDetailsFor(req)
		details.ApprovedAt = ""
		insertReconciliationOperation(t, &handler, key, string(CrawlOperationAccepted), details)
		reconciliation, err := ReconcileCrawlProposalOperation(context.Background(), &handler, proposal)
		if !errors.Is(err, ErrCrawlOperationMetadataInvalid) ||
			reconciliation.Outcome != CrawlOperationReconciliationInvalidMetadata {
			t.Fatalf("missing timestamp reconciliation = %+v err=%v", reconciliation, err)
		}
		if stored := readProposalState(t, &handler, proposal.RequestID); stored.State != CrawlProposalStatePending {
			t.Fatalf("missing timestamp changed proposal state to %q", stored.State)
		}
	})
	t.Run("invalid operation state", func(t *testing.T) {
		_, handler, req, proposal, key := reconciliationFixture(t)
		details, _ := crawlOperationDetailsFor(req)
		insertReconciliationOperation(t, &handler, key, "scheduling", details)
		reconciliation, err := ReconcileCrawlProposalOperation(context.Background(), &handler, proposal)
		if !errors.Is(err, ErrCrawlOperationStateInvalid) ||
			reconciliation.Outcome != CrawlOperationReconciliationInvalidState {
			t.Fatalf("invalid-state reconciliation = %+v err=%v", reconciliation, err)
		}
	})
	t.Run("approval binding conflict", func(t *testing.T) {
		db, handler, req, proposal, key := reconciliationFixture(t)
		details, _ := crawlOperationDetailsFor(req)
		details.ApprovalID = strings.Repeat("f", 64)
		insertReconciliationOperation(t, &handler, key, string(CrawlOperationAccepted), details)
		reconciliation, err := ReconcileCrawlProposalOperation(context.Background(), &handler, proposal)
		if !errors.Is(err, ErrCrawlOperationConflict) ||
			reconciliation.Outcome != CrawlOperationReconciliationConflict {
			t.Fatalf("approval-conflict reconciliation = %+v err=%v", reconciliation, err)
		}
		if _, err := db.Exec(`SELECT 1 FROM CrawlApprovals WHERE approval_id = ?`, strings.Repeat("f", 64)); err != nil {
			// The missing approval is the fixture; the actual operation row is
			// still expected to remain for diagnostics.
			_ = err
		}
	})
	t.Run("store error", func(t *testing.T) {
		db, handler, _, proposal, _ := reconciliationFixture(t)
		if _, err := db.Exec(`DROP TABLE Status`); err != nil {
			t.Fatalf("drop Status: %v", err)
		}
		reconciliation, err := ReconcileCrawlProposalOperation(context.Background(), &handler, proposal)
		if !errors.Is(err, ErrCrawlOperationStoreUnavailable) ||
			reconciliation.Outcome != CrawlOperationReconciliationUnavailable {
			t.Fatalf("store-error reconciliation = %+v err=%v", reconciliation, err)
		}
		if stored := readProposalState(t, &handler, proposal.RequestID); stored.State != CrawlProposalStatePending {
			t.Fatalf("store error changed proposal state to %q", stored.State)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		_, handler, _, proposal, _ := reconciliationFixture(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		reconciliation, err := ReconcileCrawlProposalOperation(ctx, &handler, proposal)
		if !errors.Is(err, context.Canceled) || reconciliation.Outcome != CrawlOperationReconciliationCancelled {
			t.Fatalf("cancelled reconciliation = %+v err=%v", reconciliation, err)
		}
		if stored := readProposalState(t, &handler, proposal.RequestID); stored.State != CrawlProposalStatePending {
			t.Fatalf("cancelled reconciliation changed proposal state to %q", stored.State)
		}
	})
	t.Run("deadline", func(t *testing.T) {
		_, handler, _, proposal, _ := reconciliationFixture(t)
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		reconciliation, err := ReconcileCrawlProposalOperation(ctx, &handler, proposal)
		if !errors.Is(err, context.DeadlineExceeded) || reconciliation.Outcome != CrawlOperationReconciliationDeadline {
			t.Fatalf("deadline reconciliation = %+v err=%v", reconciliation, err)
		}
		if stored := readProposalState(t, &handler, proposal.RequestID); stored.State != CrawlProposalStatePending {
			t.Fatalf("deadline reconciliation changed proposal state to %q", stored.State)
		}
	})
	t.Run("transient metadata failure retries", func(t *testing.T) {
		_, handler, req, proposal, key := reconciliationFixture(t)
		insertRawReconciliationOperation(t, &handler, key, string(CrawlOperationAccepted), `{bad`)
		if reconciliation, err := ReconcileCrawlProposalOperation(context.Background(), &handler, proposal); !errors.Is(err, ErrCrawlOperationMetadataInvalid) {
			t.Fatalf("initial malformed metadata = %+v err=%v", reconciliation, err)
		}
		if _, err := handler.Exec(`DELETE FROM Status WHERE status_sha256 = ?`, key); err != nil {
			t.Fatalf("remove malformed operation: %v", err)
		}
		details, _ := crawlOperationDetailsFor(req)
		insertReconciliationOperation(t, &handler, key, string(CrawlOperationAccepted), details)
		reconciliation, err := ReconcileCrawlProposalOperation(context.Background(), &handler, proposal)
		if err != nil || reconciliation.Outcome != CrawlOperationReconciliationFound {
			t.Fatalf("retry reconciliation = %+v err=%v", reconciliation, err)
		}
		if stored := readProposalState(t, &handler, proposal.RequestID); stored.State != CrawlProposalStateSubmitted {
			t.Fatalf("retry did not reconcile proposal: %+v", stored)
		}
	})
}

func TestReconcileCrawlProposalOperationRejectsEveryBindingConflict(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*crawlOperationDetails)
	}{
		{"request id", func(d *crawlOperationDetails) { d.RequestID = "other-request" }},
		{"run id", func(d *crawlOperationDetails) { d.RunID = "other-run" }},
		{"agent id", func(d *crawlOperationDetails) { d.AgentID = "other-agent" }},
		{"owner", func(d *crawlOperationDetails) { d.Owner = "other-owner" }},
		{"principal", func(d *crawlOperationDetails) { d.PrincipalUserID++ }},
		{"source", func(d *crawlOperationDetails) { d.SourceID++ }},
		{"operation", func(d *crawlOperationDetails) { d.OperationType = "other" }},
		{"proposal fingerprint", func(d *crawlOperationDetails) { d.ParametersSHA256 = strings.Repeat("c", 64) }},
		{"approval id", func(d *crawlOperationDetails) { d.ApprovalID = strings.Repeat("d", 64) }},
		{"approval fingerprint", func(d *crawlOperationDetails) { d.ApprovalSHA256 = strings.Repeat("e", 64) }},
		{"approved by", func(d *crawlOperationDetails) { d.ApprovedBy = "other-operator" }},
		{"approved by user", func(d *crawlOperationDetails) { d.ApprovedByUserID++ }},
		{"approved at", func(d *crawlOperationDetails) { d.ApprovedAt = time.Now().UTC().Format(time.RFC3339Nano) }},
		{"expires at", func(d *crawlOperationDetails) {
			d.ExpiresAt = time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339Nano)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, handler, req, proposal, key := reconciliationFixture(t)
			details, err := crawlOperationDetailsFor(req)
			if err != nil {
				t.Fatalf("operation details: %v", err)
			}
			tc.mutate(&details)
			insertReconciliationOperation(t, &handler, key, string(CrawlOperationAccepted), details)
			reconciliation, err := ReconcileCrawlProposalOperation(context.Background(), &handler, proposal)
			if !errors.Is(err, ErrCrawlOperationConflict) ||
				reconciliation.Outcome != CrawlOperationReconciliationConflict || reconciliation.Result.Reused {
				t.Fatalf("conflict case = %+v err=%v", reconciliation, err)
			}
			if stored := readProposalState(t, &handler, proposal.RequestID); stored.State != CrawlProposalStatePending {
				t.Fatalf("conflict changed proposal state to %q", stored.State)
			}
			if count := crawlOperationStatusCount(t, db); count != 1 {
				t.Fatalf("conflict changed operation count to %d", count)
			}
		})
	}
}
