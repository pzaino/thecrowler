// Copyright 2023 Paolo Fabio Zaino, all rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func goalAsyncObservationPostgres(db cdb.Handler) bool {
	switch strings.ToLower(strings.TrimSpace(db.DBMS())) {
	case cdb.DBPostgresStr, "postgresql":
		return true
	default:
		return false
	}
}

func goalAsyncObservationPlaceholder(db cdb.Handler, position int) string {
	if goalAsyncObservationPostgres(db) {
		return fmt.Sprintf("$%d", position)
	}
	return "?"
}

func goalAsyncObservationJSONArg(db cdb.Handler, position int) string {
	placeholder := goalAsyncObservationPlaceholder(db, position)
	if goalAsyncObservationPostgres(db) {
		return placeholder + "::jsonb"
	}
	return placeholder
}

func goalAsyncObservationCheckpoint(t *testing.T) (goalAsyncCheckpoint, goalAsyncWorkRef) {
	t.Helper()
	cp := testAsyncGoalBaseCheckpoint(t)
	cp = testAsyncGoalStart(t, cp)
	cp = testAsyncGoalAwaitApproval(t, cp)

	work := testAsyncOperationWork()
	work.OperationID = cdb.CrawlOperationKey(cp.Owner, cp.AgentID, work.SourceID, cdb.CrawlOperationType, work.RequestID)
	work.ProposalExpiresAt = cp.CreatedAt.Add(30 * time.Minute)
	work.ApprovalExpiresAt = cp.CreatedAt.Add(30 * time.Minute)
	work.SubmitBy = cp.CreatedAt.Add(30 * time.Minute)
	work.AcceptedAt = cp.CreatedAt.Add(time.Second)
	scheduling := testAsyncSchedulingPrerequisite(t, cp, work, work.AcceptedAt)
	used := goalAsyncUsed{ScheduledWorkRequests: 1}
	next, _, err := applyGoalAsyncTransition(cp, goalAsyncTransitionRequest{
		OperationID:      "op-observation-external",
		ExpectedRevision: cp.Revision,
		Kind:             goalAsyncTransitionAwaitExternalWork,
		Now:              cp.UpdatedAt.Add(time.Second),
		ReasonCode:       goalAsyncReasonAwaitingExternalWork,
		Used:             &used,
		Scheduling:       &scheduling,
	})
	if err != nil {
		t.Fatalf("build observation checkpoint: %v", err)
	}
	return next, work
}

func goalAsyncObservationDetailsFor(cp goalAsyncCheckpoint, work goalAsyncWorkRef, approvedAt time.Time, reason string) goalAsyncOperationDetails {
	return goalAsyncOperationDetails{
		RequestID:        work.RequestID,
		RunID:            cp.RunID,
		PrincipalUserID:  cp.PrincipalUserID,
		AgentID:          cp.AgentID,
		Owner:            cp.Owner,
		SourceID:         work.SourceID,
		OperationType:    cdb.CrawlOperationType,
		ParametersSHA256: work.ProposalSHA256,
		ApprovalID:       work.ApprovalID,
		ApprovalSHA256:   work.ApprovalSHA256,
		ApprovedBy:       "operator",
		ApprovedByUserID: 9,
		ApprovedAt:       approvedAt.UTC().Format(time.RFC3339Nano),
		ExpiresAt:        work.SubmitBy.UTC().Format(time.RFC3339Nano),
		ReasonCode:       reason,
	}
}

func insertGoalAsyncObservationFixture(t *testing.T, db cdb.Handler, cp goalAsyncCheckpoint, work goalAsyncWorkRef) {
	t.Helper()
	ctx := context.Background()
	approvedAt := testAsyncGoalNow
	details := goalAsyncObservationDetailsFor(cp, work, approvedAt, cdb.CrawlReasonScheduled)
	detailsJSON, err := json.Marshal(details)
	if err != nil {
		t.Fatalf("encode operation details: %v", err)
	}
	p := func(n int) string { return goalAsyncObservationPlaceholder(db, n) }

	proposalQuery := `INSERT INTO CrawlProposals
		(request_id, run_id, tool_call_id, agent_id, owner, principal_user_id, source_id,
		 operation_type, proposal_sha256, trace_id, max_requests, budget_window_seconds,
		 policy_json, state, expires_at, submitted_at, operation_id, created_at, last_updated_at)
		VALUES (` + strings.Join([]string{
		p(1), p(2), p(3), p(4), p(5), p(6), p(7), p(8), p(9), p(10),
		p(11), p(12), p(13), p(14), p(15), p(16), p(17), p(18), p(19),
	}, ", ") + `)`
	if _, err := db.ExecContext(ctx, proposalQuery,
		work.RequestID, cp.RunID, "observation-call", cp.AgentID, cp.Owner,
		cp.PrincipalUserID, work.SourceID, cdb.CrawlOperationType,
		work.ProposalSHA256, cp.TraceID, 1, int64(3600), "{}",
		cdb.CrawlProposalStateSubmitted, work.ProposalExpiresAt, work.AcceptedAt,
		work.OperationID, cp.CreatedAt, cp.UpdatedAt); err != nil {
		t.Fatalf("insert observation proposal: %v", err)
	}

	approvalQuery := `INSERT INTO CrawlApprovals
		(approval_id, proposal_sha256, request_id, run_id, agent_id, owner,
		 principal_user_id, source_id, operation_type, issuer, issued_by_user_id,
		 issued_by, issued_by_issuer, approved_by_user_id, approved_by,
		 approved_by_issuer, approval_sha256, proposal_expires_at, approved_at,
		 expires_at, revoked_at, revoked_by_user_id, revoked_by, revocation_reason,
		 created_at, last_updated_at)
		VALUES (` + strings.Join([]string{
		p(1), p(2), p(3), p(4), p(5), p(6), p(7), p(8), p(9), p(10),
		p(11), p(12), p(13), p(14), p(15), p(16), p(17), p(18), p(19),
		p(20), p(21), p(22), p(23), p(24), p(25), p(26),
	}, ", ") + `)`
	if _, err := db.ExecContext(ctx, approvalQuery,
		work.ApprovalID, work.ProposalSHA256, work.RequestID, cp.RunID, cp.AgentID,
		cp.Owner, cp.PrincipalUserID, work.SourceID, cdb.CrawlOperationType,
		"crowler", 9, "operator", "local", 9, "operator", "local",
		work.ApprovalSHA256, work.ProposalExpiresAt, approvedAt,
		work.ApprovalExpiresAt, nil, nil, nil, nil, cp.CreatedAt, cp.UpdatedAt); err != nil {
		t.Fatalf("insert observation approval: %v", err)
	}

	statusQuery := `INSERT INTO Status
		(status_sha256, agent_id, source_id, operation_type, status, details, created_at, last_updated_at)
		VALUES (` + p(1) + `, ` + p(2) + `, ` + p(3) + `, ` + p(4) + `, ` + p(5) + `, ` +
		goalAsyncObservationJSONArg(db, 6) + `, ` + p(7) + `, ` + p(8) + `)`
	if _, err := db.ExecContext(ctx, statusQuery,
		work.OperationID, cp.AgentID, work.SourceID, cdb.CrawlOperationType,
		string(cdb.CrawlOperationAccepted), string(detailsJSON),
		work.AcceptedAt, work.AcceptedAt); err != nil {
		t.Fatalf("insert observation Status row: %v", err)
	}
}

func setGoalAsyncObservationStatus(t *testing.T, db cdb.Handler, work goalAsyncWorkRef, state, reason string, updatedAt time.Time) {
	t.Helper()
	ctx := context.Background()
	var raw []byte
	query := `SELECT details FROM Status WHERE status_sha256 = ` + goalAsyncObservationPlaceholder(db, 1)
	if err := db.QueryRowContext(ctx, query, work.OperationID).Scan(&raw); err != nil {
		t.Fatalf("read observation details: %v", err)
	}
	var details goalAsyncOperationDetails
	if err := json.Unmarshal(raw, &details); err != nil {
		t.Fatalf("decode observation details: %v", err)
	}
	details.ReasonCode = reason
	encoded, err := json.Marshal(details)
	if err != nil {
		t.Fatalf("encode observation details: %v", err)
	}
	update := `UPDATE Status SET status = ` + goalAsyncObservationPlaceholder(db, 1) +
		`, details = ` + goalAsyncObservationJSONArg(db, 2) +
		`, last_updated_at = ` + goalAsyncObservationPlaceholder(db, 3) +
		` WHERE status_sha256 = ` + goalAsyncObservationPlaceholder(db, 4)
	if _, err := db.ExecContext(ctx, update, state, string(encoded), updatedAt, work.OperationID); err != nil {
		t.Fatalf("update observation Status: %v", err)
	}
}

func mutateGoalAsyncObservationDetails(t *testing.T, db cdb.Handler, work goalAsyncWorkRef, mutate func(*goalAsyncOperationDetails)) {
	t.Helper()
	ctx := context.Background()
	var raw []byte
	query := `SELECT details FROM Status WHERE status_sha256 = ` + goalAsyncObservationPlaceholder(db, 1)
	if err := db.QueryRowContext(ctx, query, work.OperationID).Scan(&raw); err != nil {
		t.Fatalf("read observation details: %v", err)
	}
	var details goalAsyncOperationDetails
	if err := json.Unmarshal(raw, &details); err != nil {
		t.Fatalf("decode observation details: %v", err)
	}
	mutate(&details)
	encoded, err := json.Marshal(details)
	if err != nil {
		t.Fatalf("encode mutated observation details: %v", err)
	}
	update := `UPDATE Status SET details = ` + goalAsyncObservationJSONArg(db, 1) +
		` WHERE status_sha256 = ` + goalAsyncObservationPlaceholder(db, 2)
	if _, err := db.ExecContext(ctx, update, string(encoded), work.OperationID); err != nil {
		t.Fatalf("mutate observation details: %v", err)
	}
}

func setGoalAsyncObservationRawDetails(t *testing.T, db cdb.Handler, work goalAsyncWorkRef, raw string) {
	t.Helper()
	query := `UPDATE Status SET details = ` + goalAsyncObservationJSONArg(db, 1) +
		` WHERE status_sha256 = ` + goalAsyncObservationPlaceholder(db, 2)
	if _, err := db.ExecContext(context.Background(), query, raw, work.OperationID); err != nil {
		t.Fatalf("set raw observation details: %v", err)
	}
}

func observeGoalAsyncTestOperation(t *testing.T, store *goalAsyncStore, cp goalAsyncCheckpoint, work goalAsyncWorkRef, now time.Time) (goalAsyncCrawlObservation, error) {
	t.Helper()
	return store.observeCrawlOperationAt(context.Background(), cp, work.OperationID, now)
}

func TestGoalAsyncObservationSQLiteValidStates(t *testing.T) {
	store, handler, _ := openGoalAsyncSQLiteStore(t)
	cp, work := goalAsyncObservationCheckpoint(t)
	insertGoalAsyncObservationFixture(t, handler, cp, work)
	original := cloneGoalAsyncCheckpoint(cp)

	observation, err := observeGoalAsyncTestOperation(t, store, cp, work, testAsyncGoalNow.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("observe accepted operation: %v", err)
	}
	if observation.Class != goalAsyncObservationPending ||
		observation.OperationState != string(cdb.CrawlOperationAccepted) ||
		observation.ProposalState != cdb.CrawlProposalStateSubmitted ||
		observation.ApprovalState != "active" ||
		observation.ReasonCode != cdb.CrawlReasonScheduled {
		t.Fatalf("unexpected accepted observation: %+v", observation)
	}

	setGoalAsyncObservationStatus(t, handler, work, string(cdb.CrawlOperationProcessing), cdb.CrawlReasonProcessing, testAsyncGoalNow.Add(6*time.Minute))
	observation, err = observeGoalAsyncTestOperation(t, store, cp, work, testAsyncGoalNow.Add(6*time.Minute))
	if err != nil || observation.Class != goalAsyncObservationProcessing {
		t.Fatalf("observe processing operation = %+v err=%v", observation, err)
	}

	setGoalAsyncObservationStatus(t, handler, work, string(cdb.CrawlOperationCompleted), "crawl_completed", testAsyncGoalNow.Add(7*time.Minute))
	observation, err = observeGoalAsyncTestOperation(t, store, cp, work, testAsyncGoalNow.Add(7*time.Minute))
	if err != nil || observation.Class != goalAsyncObservationReportedCompleted {
		t.Fatalf("observe completed operation = %+v err=%v", observation, err)
	}
	if cp.State != goalAsyncAwaitingExternalWork {
		t.Fatalf("observation changed checkpoint state to %s", cp.State)
	}
	if cp.Verification != nil || len(cp.Evidence) != 0 || cp.State == goalAsyncCriteriaMet {
		t.Fatal("reported operation completion created goal evidence or verification")
	}
	var goalRuns, goalTransitions, goalWork int
	if err := handler.QueryRow(`SELECT
		(SELECT COUNT(*) FROM AgentGoalRuns),
		(SELECT COUNT(*) FROM AgentGoalRunTransitions),
		(SELECT COUNT(*) FROM AgentGoalRunWork)`).Scan(&goalRuns, &goalTransitions, &goalWork); err != nil {
		t.Fatalf("count goal tables after observation: %v", err)
	}
	if goalRuns != 0 || goalTransitions != 0 || goalWork != 0 {
		t.Fatalf("observation wrote goal rows: runs=%d transitions=%d work=%d", goalRuns, goalTransitions, goalWork)
	}

	setGoalAsyncObservationStatus(t, handler, work, string(cdb.CrawlOperationFailed), "crawl_failed", testAsyncGoalNow.Add(8*time.Minute))
	observation, err = observeGoalAsyncTestOperation(t, store, cp, work, testAsyncGoalNow.Add(8*time.Minute))
	if err != nil || observation.Class != goalAsyncObservationTerminalNonSuccess {
		t.Fatalf("observe failed operation = %+v err=%v", observation, err)
	}

	if !reflect.DeepEqual(original, cp) {
		t.Fatal("observation mutated checkpoint")
	}
	var proposals, approvals, statuses int
	if err := handler.QueryRow(`SELECT
		(SELECT COUNT(*) FROM CrawlProposals WHERE request_id=?),
		(SELECT COUNT(*) FROM CrawlApprovals WHERE approval_id=?),
		(SELECT COUNT(*) FROM Status WHERE status_sha256=?)`,
		work.RequestID, work.ApprovalID, work.OperationID).Scan(&proposals, &approvals, &statuses); err != nil {
		t.Fatalf("count observation rows: %v", err)
	}
	if proposals != 1 || approvals != 1 || statuses != 1 {
		t.Fatalf("observation changed authoritative rows: proposals=%d approvals=%d statuses=%d", proposals, approvals, statuses)
	}
}

func TestGoalAsyncObservationSQLiteHistoricalExpiryAndRevocation(t *testing.T) {
	store, handler, _ := openGoalAsyncSQLiteStore(t)
	cp, work := goalAsyncObservationCheckpoint(t)
	insertGoalAsyncObservationFixture(t, handler, cp, work)

	observation, err := observeGoalAsyncTestOperation(t, store, cp, work, work.ApprovalExpiresAt.Add(time.Minute))
	if err != nil || observation.Class != goalAsyncObservationPending || observation.ApprovalState != "expired" {
		t.Fatalf("observe after approval expiry = %+v err=%v", observation, err)
	}

	if _, err := handler.Exec(`UPDATE CrawlApprovals SET revoked_at=? WHERE approval_id=?`,
		work.AcceptedAt.Add(time.Second), work.ApprovalID); err != nil {
		t.Fatalf("revoke observation approval: %v", err)
	}
	observation, err = observeGoalAsyncTestOperation(t, store, cp, work, work.ApprovalExpiresAt.Add(2*time.Minute))
	if err != nil || observation.Class != goalAsyncObservationPending || observation.ApprovalState != "revoked" {
		t.Fatalf("observe after approval revocation = %+v err=%v", observation, err)
	}
}

func TestGoalAsyncObservationSQLiteRejectsInvalidCorrelation(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(t *testing.T, handler cdb.Handler, cp goalAsyncCheckpoint, work goalAsyncWorkRef) string
		wantClass goalAsyncObservationClass
		wantErr   error
	}{
		{
			name: "wrong proposal run",
			mutate: func(t *testing.T, handler cdb.Handler, cp goalAsyncCheckpoint, work goalAsyncWorkRef) string {
				if _, err := handler.Exec(`UPDATE CrawlProposals SET run_id='wrong-run' WHERE request_id=?`, work.RequestID); err != nil {
					t.Fatalf("mutate proposal run: %v", err)
				}
				return work.OperationID
			},
			wantClass: goalAsyncObservationConflict,
			wantErr:   errGoalAsyncObservationConflict,
		},
		{
			name: "wrong approval owner",
			mutate: func(t *testing.T, handler cdb.Handler, cp goalAsyncCheckpoint, work goalAsyncWorkRef) string {
				if _, err := handler.Exec(`UPDATE CrawlApprovals SET owner='wrong-owner' WHERE approval_id=?`, work.ApprovalID); err != nil {
					t.Fatalf("mutate approval owner: %v", err)
				}
				return work.OperationID
			},
			wantClass: goalAsyncObservationConflict,
			wantErr:   errGoalAsyncObservationConflict,
		},
		{
			name: "wrong Status source",
			mutate: func(t *testing.T, handler cdb.Handler, cp goalAsyncCheckpoint, work goalAsyncWorkRef) string {
				mutateGoalAsyncObservationDetails(t, handler, work, func(details *goalAsyncOperationDetails) {
					details.SourceID = 43
				})
				return work.OperationID
			},
			wantClass: goalAsyncObservationConflict,
			wantErr:   errGoalAsyncObservationConflict,
		},
		{
			name: "missing operation row",
			mutate: func(t *testing.T, handler cdb.Handler, cp goalAsyncCheckpoint, work goalAsyncWorkRef) string {
				if _, err := handler.Exec(`DELETE FROM Status WHERE status_sha256=?`, work.OperationID); err != nil {
					t.Fatalf("delete operation row: %v", err)
				}
				return work.OperationID
			},
			wantClass: goalAsyncObservationUncertain,
			wantErr:   errGoalAsyncObservationUncertain,
		},
		{
			name: "missing proposal but operation exists",
			mutate: func(t *testing.T, handler cdb.Handler, cp goalAsyncCheckpoint, work goalAsyncWorkRef) string {
				if _, err := handler.Exec(`DELETE FROM CrawlProposals WHERE request_id=?`, work.RequestID); err != nil {
					t.Fatalf("delete proposal: %v", err)
				}
				return work.OperationID
			},
			wantClass: goalAsyncObservationConflict,
			wantErr:   errGoalAsyncObservationConflict,
		},
		{
			name: "malformed details",
			mutate: func(t *testing.T, handler cdb.Handler, cp goalAsyncCheckpoint, work goalAsyncWorkRef) string {
				setGoalAsyncObservationRawDetails(t, handler, work, `{"request_id":"not-a-valid-operation"}`)
				return work.OperationID
			},
			wantClass: goalAsyncObservationUncertain,
			wantErr:   errGoalAsyncObservationUncertain,
		},
		{
			name: "duplicate details key",
			mutate: func(t *testing.T, handler cdb.Handler, cp goalAsyncCheckpoint, work goalAsyncWorkRef) string {
				setGoalAsyncObservationRawDetails(t, handler, work, `{"request_id":"a","request_id":"b"}`)
				return work.OperationID
			},
			wantClass: goalAsyncObservationUncertain,
			wantErr:   errGoalAsyncObservationUncertain,
		},
		{
			name: "oversized details",
			mutate: func(t *testing.T, handler cdb.Handler, cp goalAsyncCheckpoint, work goalAsyncWorkRef) string {
				setGoalAsyncObservationRawDetails(t, handler, work, `"`+strings.Repeat("x", maxGoalAsyncOperationDetailsBytes+1)+`"`)
				return work.OperationID
			},
			wantClass: goalAsyncObservationUncertain,
			wantErr:   errGoalAsyncObservationUncertain,
		},
		{
			name: "unknown operation state",
			mutate: func(t *testing.T, handler cdb.Handler, cp goalAsyncCheckpoint, work goalAsyncWorkRef) string {
				setGoalAsyncObservationStatus(t, handler, work, "mystery", "crawl_failed", testAsyncGoalNow.Add(time.Minute))
				return work.OperationID
			},
			wantClass: goalAsyncObservationUncertain,
			wantErr:   errGoalAsyncObservationUncertain,
		},
		{
			name: "wrong proposal hash",
			mutate: func(t *testing.T, handler cdb.Handler, cp goalAsyncCheckpoint, work goalAsyncWorkRef) string {
				if _, err := handler.Exec(`UPDATE CrawlProposals SET proposal_sha256=? WHERE request_id=?`, strings.Repeat("c", 64), work.RequestID); err != nil {
					t.Fatalf("mutate proposal hash: %v", err)
				}
				return work.OperationID
			},
			wantClass: goalAsyncObservationConflict,
			wantErr:   errGoalAsyncObservationConflict,
		},
		{
			name: "wrong approval hash",
			mutate: func(t *testing.T, handler cdb.Handler, cp goalAsyncCheckpoint, work goalAsyncWorkRef) string {
				if _, err := handler.Exec(`UPDATE CrawlApprovals SET approval_sha256=? WHERE approval_id=?`, strings.Repeat("d", 64), work.ApprovalID); err != nil {
					t.Fatalf("mutate approval hash: %v", err)
				}
				return work.OperationID
			},
			wantClass: goalAsyncObservationConflict,
			wantErr:   errGoalAsyncObservationConflict,
		},
		{
			name: "wrong Status agent",
			mutate: func(t *testing.T, handler cdb.Handler, cp goalAsyncCheckpoint, work goalAsyncWorkRef) string {
				if _, err := handler.Exec(`UPDATE Status SET agent_id='other-agent' WHERE status_sha256=?`, work.OperationID); err != nil {
					t.Fatalf("mutate Status agent: %v", err)
				}
				return work.OperationID
			},
			wantClass: goalAsyncObservationConflict,
			wantErr:   errGoalAsyncObservationConflict,
		},
		{
			name: "wrong Status operation type",
			mutate: func(t *testing.T, handler cdb.Handler, cp goalAsyncCheckpoint, work goalAsyncWorkRef) string {
				if _, err := handler.Exec(`UPDATE Status SET operation_type='scrape' WHERE status_sha256=?`, work.OperationID); err != nil {
					t.Fatalf("mutate Status operation type: %v", err)
				}
				return work.OperationID
			},
			wantClass: goalAsyncObservationConflict,
			wantErr:   errGoalAsyncObservationConflict,
		},
		{
			name: "wrong submitted operation ID",
			mutate: func(t *testing.T, handler cdb.Handler, cp goalAsyncCheckpoint, work goalAsyncWorkRef) string {
				if _, err := handler.Exec(`UPDATE CrawlProposals SET operation_id=? WHERE request_id=?`, strings.Repeat("e", 64), work.RequestID); err != nil {
					t.Fatalf("mutate submitted operation ID: %v", err)
				}
				return work.OperationID
			},
			wantClass: goalAsyncObservationConflict,
			wantErr:   errGoalAsyncObservationConflict,
		},
		{
			name: "wrong principal in operation details",
			mutate: func(t *testing.T, handler cdb.Handler, cp goalAsyncCheckpoint, work goalAsyncWorkRef) string {
				mutateGoalAsyncObservationDetails(t, handler, work, func(details *goalAsyncOperationDetails) {
					details.PrincipalUserID = cp.PrincipalUserID + 1
				})
				return work.OperationID
			},
			wantClass: goalAsyncObservationConflict,
			wantErr:   errGoalAsyncObservationConflict,
		},
		{
			name: "unknown operation reference",
			mutate: func(t *testing.T, handler cdb.Handler, cp goalAsyncCheckpoint, work goalAsyncWorkRef) string {
				return strings.Repeat("f", 64)
			},
			wantClass: goalAsyncObservationConflict,
			wantErr:   errGoalAsyncObservationConflict,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			store, handler, _ := openGoalAsyncSQLiteStore(t)
			cp, work := goalAsyncObservationCheckpoint(t)
			insertGoalAsyncObservationFixture(t, handler, cp, work)
			operationID := test.mutate(t, handler, cp, work)
			observation, err := store.observeCrawlOperationAt(context.Background(), cp, operationID, testAsyncGoalNow.Add(5*time.Minute))
			if observation.Class != test.wantClass {
				t.Fatalf("observation class = %q, want %q (%+v)", observation.Class, test.wantClass, observation)
			}
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("observation error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestGoalAsyncObservationSQLiteUnsubmittedProposal(t *testing.T) {
	store, handler, _ := openGoalAsyncSQLiteStore(t)
	cp, work := goalAsyncObservationCheckpoint(t)
	insertGoalAsyncObservationFixture(t, handler, cp, work)
	if _, err := handler.Exec(`UPDATE CrawlProposals SET state='pending', operation_id=NULL, submitted_at=NULL WHERE request_id=?`, work.RequestID); err != nil {
		t.Fatalf("unsubmit proposal: %v", err)
	}
	if _, err := handler.Exec(`DELETE FROM CrawlApprovals WHERE approval_id=?`, work.ApprovalID); err != nil {
		t.Fatalf("delete approval: %v", err)
	}
	if _, err := handler.Exec(`DELETE FROM Status WHERE status_sha256=?`, work.OperationID); err != nil {
		t.Fatalf("delete Status: %v", err)
	}
	observation, err := observeGoalAsyncTestOperation(t, store, cp, work, testAsyncGoalNow.Add(time.Minute))
	if err != nil || observation.Class != goalAsyncObservationNotFound {
		t.Fatalf("unsubmitted proposal observation = %+v err=%v", observation, err)
	}
}

func TestGoalAsyncObservationSQLiteDoesNotReconcileProposal(t *testing.T) {
	store, handler, _ := openGoalAsyncSQLiteStore(t)
	cp, work := goalAsyncObservationCheckpoint(t)
	insertGoalAsyncObservationFixture(t, handler, cp, work)
	if _, err := handler.Exec(`UPDATE CrawlProposals SET state='expired' WHERE request_id=?`, work.RequestID); err != nil {
		t.Fatalf("mark proposal expired: %v", err)
	}
	observation, err := observeGoalAsyncTestOperation(t, store, cp, work, testAsyncGoalNow.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("observe expired proposal with accepted operation: %v", err)
	}
	if observation.Class != goalAsyncObservationPending {
		t.Fatalf("expired proposal observation class = %q, want pending", observation.Class)
	}
	var state string
	if err := handler.QueryRow(`SELECT state FROM CrawlProposals WHERE request_id=?`, work.RequestID).Scan(&state); err != nil {
		t.Fatalf("read proposal state: %v", err)
	}
	if state != cdb.CrawlProposalStateExpired {
		t.Fatalf("observation mutated proposal state to %q", state)
	}
}

func TestGoalAsyncObservationSQLiteRejectsWrongDeterministicKey(t *testing.T) {
	store, handler, _ := openGoalAsyncSQLiteStore(t)
	cp, work := goalAsyncObservationCheckpoint(t)
	insertGoalAsyncObservationFixture(t, handler, cp, work)

	fakeOperationID := strings.Repeat("a", 64)
	if fakeOperationID == work.OperationID {
		fakeOperationID = strings.Repeat("b", 64)
	}
	if _, err := handler.Exec(`UPDATE Status SET status_sha256=? WHERE status_sha256=?`, fakeOperationID, work.OperationID); err != nil {
		t.Fatalf("move Status row to fake operation ID: %v", err)
	}
	if _, err := handler.Exec(`UPDATE CrawlProposals SET operation_id=? WHERE request_id=?`, fakeOperationID, work.RequestID); err != nil {
		t.Fatalf("update proposal operation ID: %v", err)
	}
	observation, err := store.observeCrawlOperationAt(context.Background(), cp, fakeOperationID, testAsyncGoalNow.Add(5*time.Minute))
	if !errors.Is(err, errGoalAsyncObservationConflict) {
		t.Fatalf("deterministic key mismatch error = %v, want conflict", err)
	}
	if observation.Class != goalAsyncObservationConflict {
		t.Fatalf("deterministic key mismatch class = %q", observation.Class)
	}
}
