// Copyright 2023 Paolo Fabio Zaino
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

var sourceCrawlClaimTestNow = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

func sourceCrawlClaimTestID(label string) string {
	sum := sourceCrawlTestHash(label)
	return sum
}

func sourceCrawlTestHash(label string) string {
	// Stable 64-hex fixture IDs without importing crypto in every test.
	var out strings.Builder
	for i := 0; i < 64; i++ {
		out.WriteByte("0123456789abcdef"[int(label[i%len(label)])%16])
	}
	return out.String()
}

func seedSourceCrawlClaimSource(t *testing.T, db *sql.DB, sourceID, ownerID uint64, status string, disabled bool) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO Sources(source_id, source_uid, url, name, status, disabled, usr_id)
		VALUES (?, ?, ?, 'claim fixture', ?, ?, ?)`,
		sourceID, "uid-"+sourceCrawlTestHash("s")[:8], "https://claim.example/", status, disabled, ownerID); err != nil {
		t.Fatalf("insert claim source: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO SourceOwnership
		(source_id, user_id, verified_at, verified_by_user_id, verified_by, verification_method, verification_reason)
		VALUES (?, ?, CURRENT_TIMESTAMP, 9, 'operator', 'claim_test', 'claim')`, sourceID, ownerID); err != nil {
		t.Fatalf("insert claim ownership: %v", err)
	}
}

func enableSourceCrawlClaimMode(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`UPDATE SourceCrawlClaimPolicy SET mode='fenced' WHERE policy_id=1`); err != nil {
		t.Fatalf("enable fenced mode: %v", err)
	}
}

func sourceCrawlClaimRequest(label string) SourceCrawlClaimRequest {
	return SourceCrawlClaimRequest{
		ClaimRequestID: sourceCrawlClaimTestID(label),
		Limit:          5,
		EngineID:       "engine-claim-test",
		Lease:          5 * time.Minute,
	}
}

func TestSourceCrawlAttemptSQLiteReadiness(t *testing.T) {
	raw, handler := openAsyncGoalSQLite(t)
	if err := CheckSourceCrawlAttemptSchema(context.Background(), &handler); err != nil {
		t.Fatalf("fresh schema readiness: %v", err)
	}
	enableSourceCrawlClaimMode(t, raw)
	mode, err := SourceCrawlClaimMode(context.Background(), &handler)
	if err != nil || mode != "fenced" {
		t.Fatalf("claim mode = %q err=%v", mode, err)
	}
	if _, err := raw.Exec(`UPDATE SchemaCapabilities SET checksum=? WHERE capability='source-crawl-attempt-v1'`, strings.Repeat("0", 64)); err != nil {
		t.Fatalf("corrupt marker: %v", err)
	}
	if err := CheckSourceCrawlAttemptSchema(context.Background(), &handler); !errors.Is(err, ErrSourceCrawlAttemptSchemaUnavailable) {
		t.Fatalf("corrupt marker error = %v", err)
	}
	if _, err := raw.Exec(`DROP TABLE SourceCrawlClaims`); err != nil {
		t.Fatalf("drop claims: %v", err)
	}
	if err := CheckSourceCrawlAttemptSchema(context.Background(), &handler); !errors.Is(err, ErrSourceCrawlAttemptSchemaUnavailable) {
		t.Fatalf("missing table error = %v", err)
	}
}

func TestSourceCrawlAttemptSQLiteLegacyMode(t *testing.T) {
	_, handler := openAsyncGoalSQLite(t)
	_, err := ClaimSourceCrawlAttempts(context.Background(), &handler, sourceCrawlClaimRequest("legacy"))
	if !errors.Is(err, ErrSourceCrawlClaimLegacyMode) {
		t.Fatalf("legacy mode error = %v", err)
	}
}

func TestSourceCrawlAttemptSQLiteClaimAndIdempotency(t *testing.T) {
	raw, handler := openAsyncGoalSQLite(t)
	seedSourceCrawlClaimSource(t, raw, 42, 7, "new", false)
	enableSourceCrawlClaimMode(t, raw)

	request := sourceCrawlClaimRequest("claim-one")
	claims, err := claimSourceCrawlAttemptsAt(context.Background(), &handler, request, sourceCrawlClaimTestNow)
	if err != nil {
		t.Fatalf("claim source: %v", err)
	}
	if len(claims) != 1 {
		t.Fatalf("claims = %+v", claims)
	}
	first := claims[0]
	if first.Outcome != SourceCrawlUnattributed || first.SourceID != 42 ||
		first.Generation != 1 || len(first.AttemptID) != 64 || first.Reused {
		t.Fatalf("first claim = %+v", first)
	}
	var status, engine string
	if err := raw.QueryRow(`SELECT status, engine FROM Sources WHERE source_id=42`).Scan(&status, &engine); err != nil {
		t.Fatalf("read claimed source: %v", err)
	}
	if status != "processing" || engine != "engine-claim-test" {
		t.Fatalf("source claim state = %q/%q", status, engine)
	}

	replay, err := claimSourceCrawlAttemptsAt(context.Background(), &handler, request, sourceCrawlClaimTestNow.Add(time.Minute))
	if err != nil {
		t.Fatalf("replay claim: %v", err)
	}
	if len(replay) != 1 || replay[0].AttemptID != first.AttemptID ||
		replay[0].Generation != first.Generation || !replay[0].Reused {
		t.Fatalf("idempotent replay = %+v", replay)
	}

	if _, err := raw.Exec(`UPDATE Sources SET status='pending' WHERE source_id=42`); err != nil {
		t.Fatalf("make source claimable again: %v", err)
	}
	second, err := claimSourceCrawlAttemptsAt(context.Background(), &handler, sourceCrawlClaimRequest("claim-two"), sourceCrawlClaimTestNow.Add(6*time.Minute))
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if len(second) != 1 || second[0].Generation != 2 || second[0].AttemptID == first.AttemptID {
		t.Fatalf("second claim = %+v", second)
	}
	var oldState string
	var supersededBy int64
	if err := raw.QueryRow(`SELECT state, superseded_by_generation FROM SourceCrawlAttempts WHERE attempt_id=?`, first.AttemptID).Scan(&oldState, &supersededBy); err != nil {
		t.Fatalf("read superseded attempt: %v", err)
	}
	if oldState != "superseded" || supersededBy != 2 {
		t.Fatalf("old attempt state = %q/%d", oldState, supersededBy)
	}
}

func sourceCrawlInsertBoundOperation(t *testing.T, raw *sql.DB, sourceID, ownerID uint64, label string, operationStatus string) string {
	t.Helper()
	requestID := sourceCrawlClaimTestID("req-" + label)
	operationID := CrawlOperationKey("owner-claim", "agent-claim", sourceID, string(CrawlOperationType), requestID)
	proposalSHA := sourceCrawlClaimTestID("proposal-" + label)
	approvalID := sourceCrawlClaimTestID("approval-" + label)
	approvalSHA := sourceCrawlClaimTestID("approval-sha-" + label)
	now := sourceCrawlClaimTestNow
	toolCallID := "tool-claim-" + label
	if _, err := raw.Exec(`INSERT INTO CrawlProposals
		(request_id, run_id, tool_call_id, agent_id, owner, principal_user_id, source_id,
		 operation_type, proposal_sha256, trace_id, max_requests, budget_window_seconds,
		 policy_json, state, expires_at, submitted_at, operation_id, created_at, last_updated_at)
		VALUES (?, 'run-claim', ?, 'agent-claim', 'owner-claim', ?, ?,
		 'crawl', ?, 'trace-claim', 1, 3600, '{}', 'submitted', ?, ?, ?, ?, ?)`,
		requestID, toolCallID, ownerID, sourceID, proposalSHA, now.Add(2*time.Hour), now, operationID, now, now); err != nil {
		t.Fatalf("insert claim proposal: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO CrawlApprovals
		(approval_id, proposal_sha256, request_id, run_id, agent_id, owner,
		 principal_user_id, source_id, operation_type, issuer, issued_by_user_id,
		 issued_by, issued_by_issuer, approved_by_user_id, approved_by,
		 approved_by_issuer, approval_sha256, proposal_expires_at, approved_at,
		 expires_at, created_at, last_updated_at)
		VALUES (?, ?, ?, 'run-claim', 'agent-claim', 'owner-claim', ?, ?, 'crawl', 'crowler', 9,
		 'operator', 'local', 9, 'operator', 'local', ?, ?, ?, ?, ?, ?)`,
		approvalID, proposalSHA, requestID, ownerID, sourceID, approvalSHA,
		now.Add(2*time.Hour), now.Add(-time.Minute), now.Add(time.Hour), now, now); err != nil {
		t.Fatalf("insert claim approval: %v", err)
	}
	details, err := json.Marshal(crawlOperationDetails{
		RequestID: requestID, RunID: "run-claim", PrincipalUserID: ownerID,
		AgentID: "agent-claim", Owner: "owner-claim", SourceID: sourceID,
		OperationType: "crawl", ParametersSHA256: proposalSHA,
		ApprovalID: approvalID, ApprovalSHA256: approvalSHA,
		ApprovedBy: "operator", ApprovedByUserID: 9,
		ApprovedAt: now.Add(-time.Minute).Format(time.RFC3339Nano),
		ExpiresAt:  now.Add(time.Hour).Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("marshal claim details: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO Status
		(status_sha256, agent_id, source_id, operation_type, status, details, created_at, last_updated_at)
		VALUES (?, 'agent-claim', ?, 'crawl', ?, ?, ?, ?)`,
		operationID, sourceID, operationStatus, string(details), now, now); err != nil {
		t.Fatalf("insert claim status: %v", err)
	}
	return operationID
}

func TestSourceCrawlAttemptSQLiteOperationBinding(t *testing.T) {
	raw, handler := openAsyncGoalSQLite(t)
	seedSourceCrawlClaimSource(t, raw, 42, 7, "new", false)
	enableSourceCrawlClaimMode(t, raw)
	operationID := sourceCrawlInsertBoundOperation(t, raw, 42, 7, "one", "accepted")

	claims, err := claimSourceCrawlAttemptsAt(context.Background(), &handler, sourceCrawlClaimRequest("bound"), sourceCrawlClaimTestNow)
	if err != nil || len(claims) != 1 {
		t.Fatalf("bound claim = %+v err=%v", claims, err)
	}
	if claims[0].Outcome != SourceCrawlClaimed || claims[0].OperationID != operationID ||
		claims[0].PrincipalUserID != 7 || claims[0].ApprovalID == "" {
		t.Fatalf("bound claim details = %+v", claims[0])
	}

	replay, err := claimSourceCrawlAttemptsAt(context.Background(), &handler, sourceCrawlClaimRequest("bound"), sourceCrawlClaimTestNow.Add(time.Minute))
	if err != nil || len(replay) != 1 || replay[0].OperationID != operationID || !replay[0].Reused {
		t.Fatalf("bound replay = %+v err=%v", replay, err)
	}
}

func TestSourceCrawlAttemptSQLiteAmbiguousAndUnattributed(t *testing.T) {
	t.Run("two candidates are ambiguous", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		seedSourceCrawlClaimSource(t, raw, 42, 7, "new", false)
		enableSourceCrawlClaimMode(t, raw)
		sourceCrawlInsertBoundOperation(t, raw, 42, 7, "one", "accepted")
		sourceCrawlInsertBoundOperation(t, raw, 42, 7, "two", "accepted")
		claims, err := claimSourceCrawlAttemptsAt(context.Background(), &handler, sourceCrawlClaimRequest("ambiguous"), sourceCrawlClaimTestNow)
		if err != nil || len(claims) != 1 || claims[0].Outcome != SourceCrawlAmbiguous ||
			claims[0].OperationID != "" {
			t.Fatalf("ambiguous claim = %+v err=%v", claims, err)
		}
	})
	t.Run("already pending is not ownership", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		seedSourceCrawlClaimSource(t, raw, 42, 7, "new", false)
		enableSourceCrawlClaimMode(t, raw)
		sourceCrawlInsertBoundOperation(t, raw, 42, 7, "pending", "already_pending")
		claims, err := claimSourceCrawlAttemptsAt(context.Background(), &handler, sourceCrawlClaimRequest("pending"), sourceCrawlClaimTestNow)
		if err != nil || len(claims) != 1 || claims[0].Outcome != SourceCrawlUnattributed ||
			claims[0].OperationID != "" {
			t.Fatalf("already-pending claim = %+v err=%v", claims, err)
		}
	})
	t.Run("transferred source is not attributable", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		seedSourceCrawlClaimSource(t, raw, 42, 7, "new", false)
		enableSourceCrawlClaimMode(t, raw)
		sourceCrawlInsertBoundOperation(t, raw, 42, 7, "transfer", "accepted")
		if _, err := raw.Exec(`UPDATE Sources SET usr_id=8 WHERE source_id=42`); err != nil {
			t.Fatalf("transfer source: %v", err)
		}
		if _, err := raw.Exec(`UPDATE SourceOwnership SET user_id=8 WHERE source_id=42`); err != nil {
			t.Fatalf("transfer provenance: %v", err)
		}
		claims, err := claimSourceCrawlAttemptsAt(context.Background(), &handler, sourceCrawlClaimRequest("transfer"), sourceCrawlClaimTestNow)
		if err != nil || len(claims) != 1 || claims[0].Outcome != SourceCrawlNotAttributable ||
			claims[0].OperationID != "" {
			t.Fatalf("transferred claim = %+v err=%v", claims, err)
		}
	})
	t.Run("disabled source is not selected", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		seedSourceCrawlClaimSource(t, raw, 42, 7, "new", true)
		enableSourceCrawlClaimMode(t, raw)
		claims, err := claimSourceCrawlAttemptsAt(context.Background(), &handler, sourceCrawlClaimRequest("disabled"), sourceCrawlClaimTestNow)
		if err != nil || len(claims) != 0 {
			t.Fatalf("disabled source claim = %+v err=%v", claims, err)
		}
	})
}

func TestSourceCrawlAttemptSQLiteRollback(t *testing.T) {
	raw, handler := openAsyncGoalSQLite(t)
	seedSourceCrawlClaimSource(t, raw, 42, 7, "new", false)
	enableSourceCrawlClaimMode(t, raw)
	if _, err := raw.Exec(`CREATE TRIGGER fail_claim_insert BEFORE INSERT ON SourceCrawlAttempts
		BEGIN SELECT RAISE(ABORT, 'injected claim failure'); END`); err != nil {
		t.Fatalf("create claim failure trigger: %v", err)
	}
	if _, err := ClaimSourceCrawlAttempts(context.Background(), &handler, sourceCrawlClaimRequest("rollback")); err == nil {
		t.Fatal("claim unexpectedly succeeded")
	}
	var status string
	if err := raw.QueryRow(`SELECT status FROM Sources WHERE source_id=42`).Scan(&status); err != nil {
		t.Fatalf("read source after rollback: %v", err)
	}
	if status != "new" {
		t.Fatalf("source status after rollback = %q", status)
	}
	var claims, attempts int
	if err := raw.QueryRow(`SELECT
		(SELECT COUNT(*) FROM SourceCrawlClaims),
		(SELECT COUNT(*) FROM SourceCrawlAttempts)`).Scan(&claims, &attempts); err != nil {
		t.Fatalf("count rows after rollback: %v", err)
	}
	if claims != 0 || attempts != 0 {
		t.Fatalf("rollback left claims=%d attempts=%d", claims, attempts)
	}
}

func TestSourceCrawlAttemptSQLiteExactHistoryFK(t *testing.T) {
	raw, _ := openAsyncGoalSQLite(t)
	seedSourceCrawlClaimSource(t, raw, 42, 7, "new", false)
	enableSourceCrawlClaimMode(t, raw)
	now := sourceCrawlClaimTestNow
	for _, attempt := range []struct {
		id  string
		gen int64
	}{
		{sourceCrawlClaimTestID("fk-a"), 1},
		{sourceCrawlClaimTestID("fk-b"), 2},
	} {
		if _, err := raw.Exec(`INSERT INTO SourceCrawlAttempts
			(attempt_id, claim_request_id, source_id, generation, engine_id, state,
			 claimed_at, lease_expires_at, created_at, last_updated_at)
			VALUES (?,?,?,?, 'fixture-engine', 'claimed', ?, ?, ?, ?)`,
			attempt.id, sourceCrawlClaimTestID("fk-req-"+attempt.id), 42, attempt.gen,
			now, now.Add(time.Hour), now, now); err != nil {
			t.Fatalf("insert history attempt: %v", err)
		}
	}
	tx, err := raw.Begin()
	if err != nil {
		t.Fatalf("begin mixed claim: %v", err)
	}
	_, err = tx.Exec(`INSERT INTO SourceCrawlClaims
		(source_id, attempt_id, claim_request_id, generation, engine_id,
		 claimed_at, lease_expires_at, last_updated_at)
		VALUES (42, ?, ?, 2, 'fixture-engine', ?, ?, ?)`,
		sourceCrawlClaimTestID("fk-a"), sourceCrawlClaimTestID("fk-mixed"), now, now.Add(time.Hour), now)
	if err == nil {
		err = tx.Commit()
	} else {
		_ = tx.Rollback()
	}
	if err == nil {
		t.Fatal("SQLite accepted a current claim mixing attempt and generation")
	}
	var count int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM SourceCrawlClaims WHERE source_id=42`).Scan(&count); err != nil {
		t.Fatalf("count mixed claims: %v", err)
	}
	if count != 0 {
		t.Fatalf("mixed claim persisted: %d", count)
	}
	if _, err := raw.Exec(`INSERT INTO SourceCrawlClaims
		(source_id, attempt_id, claim_request_id, generation, engine_id,
		 claimed_at, lease_expires_at, last_updated_at)
		VALUES (42, ?, ?, 1, 'fixture-engine', ?, ?, ?)`,
		sourceCrawlClaimTestID("fk-a"), sourceCrawlClaimTestID("fk-valid"), now, now.Add(time.Hour), now); err != nil {
		t.Fatalf("exact SQLite claim rejected: %v", err)
	}
	if _, err := raw.Exec(`UPDATE SourceCrawlClaims SET generation=2 WHERE source_id=42`); err == nil {
		t.Fatal("SQLite current claim update mixed attempt and generation")
	}
}

func TestSourceCrawlAttemptSQLiteGenerationRecovery(t *testing.T) {
	raw, handler := openAsyncGoalSQLite(t)
	seedSourceCrawlClaimSource(t, raw, 42, 7, "new", false)
	enableSourceCrawlClaimMode(t, raw)
	first, err := claimSourceCrawlAttemptsAt(context.Background(), &handler, sourceCrawlClaimRequest("gen-first"), sourceCrawlClaimTestNow)
	if err != nil || len(first) != 1 || first[0].Generation != 1 {
		t.Fatalf("first generation = %+v err=%v", first, err)
	}
	if _, err := raw.Exec(`DELETE FROM SourceCrawlClaims WHERE source_id=42`); err != nil {
		t.Fatalf("remove current claim: %v", err)
	}
	if _, err := raw.Exec(`UPDATE Sources SET status='pending' WHERE source_id=42`); err != nil {
		t.Fatalf("reset source: %v", err)
	}
	second, err := claimSourceCrawlAttemptsAt(context.Background(), &handler, sourceCrawlClaimRequest("gen-second"), sourceCrawlClaimTestNow.Add(time.Minute))
	if err != nil || len(second) != 1 || second[0].Generation != 2 {
		t.Fatalf("recovered generation = %+v err=%v", second, err)
	}
	now := sourceCrawlClaimTestNow
	if _, err := raw.Exec(`INSERT INTO SourceCrawlAttempts
		(attempt_id, claim_request_id, source_id, generation, engine_id, state,
		 claimed_at, lease_expires_at, created_at, last_updated_at)
		VALUES (?, ?, 42, 9223372036854775807, 'fixture-engine', 'claimed', ?, ?, ?, ?)`,
		sourceCrawlClaimTestID("gen-max"), sourceCrawlClaimTestID("gen-max-req"), now, now.Add(time.Hour), now, now); err != nil {
		t.Fatalf("insert maximum generation: %v", err)
	}
	if _, err := raw.Exec(`DELETE FROM SourceCrawlClaims WHERE source_id=42`); err != nil {
		t.Fatalf("remove recovered claim: %v", err)
	}
	if _, err := raw.Exec(`UPDATE Sources SET status='pending' WHERE source_id=42`); err != nil {
		t.Fatalf("reset source for overflow: %v", err)
	}
	if _, err := claimSourceCrawlAttemptsAt(context.Background(), &handler, sourceCrawlClaimRequest("gen-overflow"), sourceCrawlClaimTestNow.Add(2*time.Minute)); !errors.Is(err, ErrSourceCrawlClaimGenerationDone) {
		t.Fatalf("overflow error = %v", err)
	}
}

func TestSourceCrawlAttemptSQLiteReplayValidation(t *testing.T) {
	raw, handler := openAsyncGoalSQLite(t)
	seedSourceCrawlClaimSource(t, raw, 42, 7, "new", false)
	enableSourceCrawlClaimMode(t, raw)
	request := sourceCrawlClaimRequest("replay")
	if _, err := claimSourceCrawlAttemptsAt(context.Background(), &handler, request, sourceCrawlClaimTestNow); err != nil {
		t.Fatalf("base replay claim: %v", err)
	}
	changedEngine := request
	changedEngine.EngineID = "other-engine"
	if _, err := claimSourceCrawlAttemptsAt(context.Background(), &handler, changedEngine, sourceCrawlClaimTestNow.Add(time.Minute)); !errors.Is(err, ErrSourceCrawlClaimConflict) {
		t.Fatalf("changed engine error = %v", err)
	}
	changedLease := request
	changedLease.Lease += time.Minute
	if _, err := claimSourceCrawlAttemptsAt(context.Background(), &handler, changedLease, sourceCrawlClaimTestNow.Add(time.Minute)); !errors.Is(err, ErrSourceCrawlClaimConflict) {
		t.Fatalf("changed lease error = %v", err)
	}
}

func TestSourceCrawlAttemptSQLiteDuplicateDetailsKey(t *testing.T) {
	raw, handler := openAsyncGoalSQLite(t)
	seedSourceCrawlClaimSource(t, raw, 42, 7, "new", false)
	enableSourceCrawlClaimMode(t, raw)
	operationID := sourceCrawlInsertBoundOperation(t, raw, 42, 7, "duplicate", "accepted")
	var details string
	if err := raw.QueryRow(`SELECT details FROM Status WHERE status_sha256=?`, operationID).Scan(&details); err != nil {
		t.Fatalf("read details: %v", err)
	}
	duplicate := strings.TrimSuffix(details, "}") + `,"request_id":"duplicate"}`
	if _, err := raw.Exec(`UPDATE Status SET details=? WHERE status_sha256=?`, duplicate, operationID); err != nil {
		t.Fatalf("write duplicate details: %v", err)
	}
	claims, err := claimSourceCrawlAttemptsAt(context.Background(), &handler, sourceCrawlClaimRequest("duplicate"), sourceCrawlClaimTestNow)
	if err != nil || len(claims) != 1 || claims[0].Outcome != SourceCrawlNotAttributable || claims[0].OperationID != "" {
		t.Fatalf("duplicate details claim = %+v err=%v", claims, err)
	}
}

func TestSourceCrawlAttemptSQLiteGenuineSchedulerBinding(t *testing.T) {
	raw, handler := openAsyncGoalSQLite(t)
	seedSourceCrawlClaimSource(t, raw, 42, 7, "completed", false)
	if _, err := raw.Exec(`CREATE TABLE IF NOT EXISTS Users (
		user_id INTEGER PRIMARY KEY,
		username TEXT NOT NULL,
		disabled BOOLEAN NOT NULL DEFAULT FALSE,
		deleted_at TIMESTAMP
	);
	INSERT INTO Users(user_id, username, disabled) VALUES (7, 'owner-claim', FALSE)
		ON CONFLICT(user_id) DO UPDATE SET username=EXCLUDED.username, disabled=FALSE`); err != nil {
		t.Fatalf("insert genuine scheduler user: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	req := crawlOperationRequest(42)
	req.RequestID = sourceCrawlClaimTestID("genuine-request")
	req.ApprovedAt = now.Add(-time.Minute)
	req.ExpiresAt = now.Add(time.Hour)
	req.RunID = "run-genuine-scheduler"
	req.AgentID = "agent-genuine-scheduler"
	req.Owner = "owner-claim"
	req.ParametersSHA256 = sourceCrawlClaimTestID("genuine-proposal")
	req.ApprovalID = sourceCrawlClaimTestID("genuine-approval")
	req.ApprovalSHA256 = sourceCrawlClaimTestID("genuine-approval-sha")
	req.ApprovedBy = "owner-claim"
	req.ApprovedByUserID = 7
	ensureCrawlApprovalFixture(t, raw, req)
	proposal := insertScheduleProposal(t, &handler, req, now.Add(time.Hour))
	result, err := scheduleSourceCrawlRequestForProposal(context.Background(), &handler, req,
		proposal.RequestID, proposal.ProposalSHA256, now)
	if err != nil || result.State != CrawlOperationAccepted {
		t.Fatalf("schedule genuine operation = %+v err=%v", result, err)
	}
	enableSourceCrawlClaimMode(t, raw)
	claims, err := claimSourceCrawlAttemptsAt(context.Background(), &handler,
		sourceCrawlClaimRequest("genuine-scheduler"), now)
	if err != nil || len(claims) != 1 || claims[0].Outcome != SourceCrawlClaimed ||
		claims[0].OperationID != result.OperationID || claims[0].PrincipalUserID != 7 {
		t.Fatalf("genuine scheduler binding = %+v err=%v", claims, err)
	}

	var storedOperation, storedRequest, storedRun, storedAgent, storedOwner sql.NullString
	var storedPrincipal sql.NullInt64
	var storedApproval, storedApprovalSHA sql.NullString
	if err := raw.QueryRow(`SELECT operation_id, request_id, run_id, agent_id, owner,
		principal_user_id, approval_id, approval_sha256 FROM SourceCrawlAttempts WHERE source_id=42`).
		Scan(&storedOperation, &storedRequest, &storedRun, &storedAgent, &storedOwner,
			&storedPrincipal, &storedApproval, &storedApprovalSHA); err != nil {
		t.Fatalf("read persisted attempt binding: %v", err)
	}
	if storedOperation.String != result.OperationID ||
		storedRequest.String != proposal.RequestID ||
		storedRun.String != proposal.RunID ||
		storedAgent.String != proposal.AgentID ||
		storedOwner.String != proposal.Owner ||
		uint64(storedPrincipal.Int64) != proposal.PrincipalUserID ||
		storedApproval.String != req.ApprovalID ||
		storedApprovalSHA.String != req.ApprovalSHA256 {
		t.Fatalf("persisted attempt binding does not match proposal/approval: op=%q request=%q run=%q agent=%q owner=%q principal=%d approval=%q approval_sha=%q",
			storedOperation.String, storedRequest.String, storedRun.String, storedAgent.String,
			storedOwner.String, storedPrincipal.Int64, storedApproval.String, storedApprovalSHA.String)
	}
	var proposalState, proposalOperation string
	if err := raw.QueryRow(`SELECT state, operation_id FROM CrawlProposals WHERE request_id=?`,
		proposal.RequestID).Scan(&proposalState, &proposalOperation); err != nil {
		t.Fatalf("read scheduled proposal: %v", err)
	}
	if proposalState != CrawlProposalStateSubmitted || proposalOperation != result.OperationID {
		t.Fatalf("proposal identity after scheduling = state=%q operation=%q", proposalState, proposalOperation)
	}
	var operationState, sourceStatus string
	if err := raw.QueryRow(`SELECT status FROM Status WHERE status_sha256=?`, result.OperationID).Scan(&operationState); err != nil {
		t.Fatalf("read scheduled operation: %v", err)
	}
	if operationState != string(CrawlOperationAccepted) {
		t.Fatalf("scheduled operation state = %q, want accepted", operationState)
	}
	if err := raw.QueryRow(`SELECT status FROM Sources WHERE source_id=42`).Scan(&sourceStatus); err != nil {
		t.Fatalf("read claimed source status: %v", err)
	}
	if sourceStatus != "processing" {
		t.Fatalf("claimed source status = %q, want processing", sourceStatus)
	}
}

func TestSourceCrawlAttemptSQLiteDatabaseClockReconciliation(t *testing.T) {
	raw, handler := openAsyncGoalSQLite(t)
	seedSourceCrawlClaimSource(t, raw, 42, 7, "new", false)
	enableSourceCrawlClaimMode(t, raw)
	sourceCrawlInsertBoundOperation(t, raw, 42, 7, "db-clock", "accepted")
	request := sourceCrawlClaimRequest("db-clock")
	now := time.Now().UTC().Truncate(time.Microsecond)
	claims, err := claimSourceCrawlAttemptsAt(context.Background(), &handler, request, now)
	if err != nil || len(claims) != 1 || claims[0].Outcome != SourceCrawlClaimed {
		t.Fatalf("base database-clock claim = %+v err=%v", claims, err)
	}
	loaded, found, err := loadSourceCrawlClaimRequest(context.Background(), handler, request.ClaimRequestID)
	if err != nil || !found || len(loaded) != 1 || !loaded[0].Reused ||
		loaded[0].Outcome != SourceCrawlClaimed || loaded[0].AttemptID != claims[0].AttemptID {
		t.Fatalf("database-clock reconciliation = %+v found=%v err=%v", loaded, found, err)
	}
	if !sameSourceCrawlClaims(loaded, claims) {
		t.Fatalf("database-clock reconciliation identity differs: loaded=%+v claims=%+v", loaded, claims)
	}
	if _, err := raw.Exec(`UPDATE SourceCrawlAttempts SET claimed_at=?, lease_expires_at=? WHERE source_id=42`,
		now.Add(-2*time.Minute), now.Add(-time.Minute)); err != nil {
		t.Fatalf("expire attempt lease: %v", err)
	}
	expired, found, err := loadSourceCrawlClaimRequest(context.Background(), handler, request.ClaimRequestID)
	if err != nil || !found || len(expired) != 1 || expired[0].Outcome != SourceCrawlClaimStaleFence {
		t.Fatalf("expired database-clock reconciliation = %+v found=%v err=%v", expired, found, err)
	}
}

func sourceCrawlSubmittedProposalFixture(t *testing.T, raw *sql.DB, sourceID, ownerID uint64,
	label string, operationID any, state string) string {
	t.Helper()
	requestID := sourceCrawlClaimTestID("submitted-request-" + label)
	proposalSHA := sourceCrawlClaimTestID("submitted-proposal-" + label)
	now := sourceCrawlClaimTestNow
	if _, err := raw.Exec(`INSERT INTO CrawlProposals
		(request_id, run_id, tool_call_id, agent_id, owner, principal_user_id, source_id,
		 operation_type, proposal_sha256, trace_id, max_requests, budget_window_seconds,
		 policy_json, state, expires_at, submitted_at, operation_id, created_at, last_updated_at)
		VALUES (?, ?, ?, 'agent-claim', 'owner-claim', ?, ?, 'crawl', ?, 'trace-submitted',
		 1, 3600, '{}', ?, ?, ?, ?, ?, ?)`,
		requestID, "run-submitted-"+label, "tool-submitted-"+label, ownerID, sourceID,
		proposalSHA, state, now.Add(2*time.Hour), now, operationID, now, now); err != nil {
		t.Fatalf("insert %s submitted proposal: %v", label, err)
	}
	return requestID
}

func sourceCrawlAttemptHistoryCount(t *testing.T, raw *sql.DB, sourceID uint64) int {
	t.Helper()
	var count int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM SourceCrawlAttempts WHERE source_id=?`, sourceID).Scan(&count); err != nil {
		t.Fatalf("count attempt history: %v", err)
	}
	return count
}

func TestSourceCrawlAttemptSQLiteSubmittedProposalCompetition(t *testing.T) {
	t.Run("valid plus null operation id is ambiguous", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		seedSourceCrawlClaimSource(t, raw, 42, 7, "new", false)
		enableSourceCrawlClaimMode(t, raw)
		sourceCrawlInsertBoundOperation(t, raw, 42, 7, "competition-null", "accepted")
		sourceCrawlSubmittedProposalFixture(t, raw, 42, 7, "null", nil, "submitted")

		request := sourceCrawlClaimRequest("competition-null")
		claims, err := claimSourceCrawlAttemptsAt(context.Background(), &handler, request, sourceCrawlClaimTestNow)
		if err != nil || len(claims) != 1 {
			t.Fatalf("claim with null competitor = %+v err=%v", claims, err)
		}
		if claims[0].Outcome != SourceCrawlAmbiguous || claims[0].OperationID != "" {
			t.Fatalf("valid plus null operation = %+v", claims[0])
		}
		replay, err := claimSourceCrawlAttemptsAt(context.Background(), &handler, request, sourceCrawlClaimTestNow.Add(time.Minute))
		if err != nil || len(replay) != 1 || !replay[0].Reused ||
			replay[0].Outcome != SourceCrawlUnattributed || replay[0].OperationID != "" ||
			replay[0].AttemptID != claims[0].AttemptID || replay[0].Generation != claims[0].Generation {
			t.Fatalf("ambiguous replay = %+v err=%v", replay, err)
		}
		if count := sourceCrawlAttemptHistoryCount(t, raw, 42); count != 1 {
			t.Fatalf("history after replay = %d, want 1", count)
		}
	})

	t.Run("valid plus blank operation id is ambiguous", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		seedSourceCrawlClaimSource(t, raw, 42, 7, "new", false)
		enableSourceCrawlClaimMode(t, raw)
		sourceCrawlInsertBoundOperation(t, raw, 42, 7, "competition-blank", "accepted")
		sourceCrawlSubmittedProposalFixture(t, raw, 42, 7, "blank", "", "submitted")

		claims, err := claimSourceCrawlAttemptsAt(context.Background(), &handler,
			sourceCrawlClaimRequest("competition-blank"), sourceCrawlClaimTestNow)
		if err != nil || len(claims) != 1 || claims[0].Outcome != SourceCrawlAmbiguous || claims[0].OperationID != "" {
			t.Fatalf("valid plus blank operation = %+v err=%v", claims, err)
		}
	})

	t.Run("valid plus forged operation id is ambiguous", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		seedSourceCrawlClaimSource(t, raw, 42, 7, "new", false)
		enableSourceCrawlClaimMode(t, raw)
		sourceCrawlInsertBoundOperation(t, raw, 42, 7, "competition-forged", "accepted")
		sourceCrawlSubmittedProposalFixture(t, raw, 42, 7, "forged-competitor",
			sourceCrawlClaimTestID("forged-competitor-operation"), "submitted")
		claims, err := claimSourceCrawlAttemptsAt(context.Background(), &handler,
			sourceCrawlClaimRequest("competition-forged"), sourceCrawlClaimTestNow)
		if err != nil || len(claims) != 1 || claims[0].Outcome != SourceCrawlAmbiguous || claims[0].OperationID != "" {
			t.Fatalf("valid plus forged operation = %+v err=%v", claims, err)
		}
	})

	t.Run("single malformed submitted proposal is not attributable", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		seedSourceCrawlClaimSource(t, raw, 42, 7, "new", false)
		enableSourceCrawlClaimMode(t, raw)
		sourceCrawlSubmittedProposalFixture(t, raw, 42, 7, "single-null", nil, "submitted")

		request := sourceCrawlClaimRequest("single-malformed")
		claims, err := claimSourceCrawlAttemptsAt(context.Background(), &handler, request, sourceCrawlClaimTestNow)
		if err != nil || len(claims) != 1 || claims[0].Outcome != SourceCrawlNotAttributable || claims[0].OperationID != "" {
			t.Fatalf("single malformed proposal = %+v err=%v", claims, err)
		}
		replay, err := claimSourceCrawlAttemptsAt(context.Background(), &handler, request, sourceCrawlClaimTestNow.Add(time.Minute))
		if err != nil || len(replay) != 1 || !replay[0].Reused || replay[0].Outcome != SourceCrawlUnattributed {
			t.Fatalf("single malformed replay = %+v err=%v", replay, err)
		}
	})

	t.Run("pending proposal is not a submitted competitor", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		seedSourceCrawlClaimSource(t, raw, 42, 7, "new", false)
		enableSourceCrawlClaimMode(t, raw)
		operationID := sourceCrawlInsertBoundOperation(t, raw, 42, 7, "competition-pending", "accepted")
		now := sourceCrawlClaimTestNow
		inserted, err := InsertCrawlProposal(context.Background(), &handler, CrawlProposalRow{
			RequestID: sourceCrawlClaimTestID("pending-request"), RunID: "run-pending",
			ToolCallID: "tool-pending", AgentID: "agent-claim", Owner: "owner-claim",
			PrincipalUserID: 7, SourceID: 42, OperationType: CrawlOperationType,
			ProposalSHA256: sourceCrawlClaimTestID("pending-proposal"), TraceID: "trace-pending",
			MaxRequests: 1, BudgetWindowSeconds: 3600, PolicyJSON: "{}",
			State: CrawlProposalStatePending, ExpiresAt: now.Add(2 * time.Hour),
		})
		if err != nil || !inserted {
			t.Fatalf("insert pending proposal: inserted=%v err=%v", inserted, err)
		}
		claims, err := claimSourceCrawlAttemptsAt(context.Background(), &handler,
			sourceCrawlClaimRequest("competition-pending"), now)
		if err != nil || len(claims) != 1 || claims[0].Outcome != SourceCrawlClaimed ||
			claims[0].OperationID != operationID {
			t.Fatalf("pending competitor claim = %+v err=%v", claims, err)
		}
	})

	t.Run("single forged operation id is not attributable", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		seedSourceCrawlClaimSource(t, raw, 42, 7, "new", false)
		enableSourceCrawlClaimMode(t, raw)
		sourceCrawlSubmittedProposalFixture(t, raw, 42, 7, "forged", sourceCrawlClaimTestID("forged-operation"), "submitted")
		claims, err := claimSourceCrawlAttemptsAt(context.Background(), &handler,
			sourceCrawlClaimRequest("single-forged"), sourceCrawlClaimTestNow)
		if err != nil || len(claims) != 1 || claims[0].Outcome != SourceCrawlNotAttributable || claims[0].OperationID != "" {
			t.Fatalf("single forged operation = %+v err=%v", claims, err)
		}
	})

	t.Run("candidate load failure fails closed", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		seedSourceCrawlClaimSource(t, raw, 42, 7, "new", false)
		enableSourceCrawlClaimMode(t, raw)
		if _, err := raw.Exec(`DROP TABLE CrawlProposals`); err != nil {
			t.Fatalf("drop proposals: %v", err)
		}
		claims, err := claimSourceCrawlAttemptsAt(context.Background(), &handler,
			sourceCrawlClaimRequest("candidate-load-failure"), sourceCrawlClaimTestNow)
		if err == nil || len(claims) != 0 {
			t.Fatalf("candidate load failure = %+v err=%v", claims, err)
		}
		if count := sourceCrawlAttemptHistoryCount(t, raw, 42); count != 0 {
			t.Fatalf("history after failed candidate load = %d, want 0", count)
		}
		var status string
		if err := raw.QueryRow(`SELECT status FROM Sources WHERE source_id=42`).Scan(&status); err != nil {
			t.Fatalf("read source status: %v", err)
		}
		if status != "new" {
			t.Fatalf("source status after failed claim = %q, want new", status)
		}
	})
}

func TestSourceCrawlAttemptSQLiteReplayLeaseBoundary(t *testing.T) {
	const lease = 5 * time.Minute
	cases := []struct {
		name    string
		replay  time.Duration
		outcome SourceCrawlClaimOutcome
	}{
		{"before expiry", lease - time.Microsecond, SourceCrawlClaimed},
		{"at expiry", lease, SourceCrawlClaimStaleFence},
		{"after expiry", lease + time.Microsecond, SourceCrawlClaimStaleFence},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, handler := openAsyncGoalSQLite(t)
			seedSourceCrawlClaimSource(t, raw, 42, 7, "new", false)
			enableSourceCrawlClaimMode(t, raw)
			sourceCrawlInsertBoundOperation(t, raw, 42, 7, "lease-"+tc.name, "accepted")
			request := sourceCrawlClaimRequest("lease-" + tc.name)
			claims, err := claimSourceCrawlAttemptsAt(context.Background(), &handler, request, sourceCrawlClaimTestNow)
			if err != nil || len(claims) != 1 || claims[0].Outcome != SourceCrawlClaimed {
				t.Fatalf("base claim = %+v err=%v", claims, err)
			}
			replay, err := claimSourceCrawlAttemptsAt(context.Background(), &handler, request,
				sourceCrawlClaimTestNow.Add(tc.replay))
			if err != nil || len(replay) != 1 || !replay[0].Reused ||
				replay[0].Outcome != tc.outcome ||
				replay[0].AttemptID != claims[0].AttemptID || replay[0].Generation != claims[0].Generation {
				t.Fatalf("replay at %s = %+v err=%v", tc.name, replay, err)
			}
			if count := sourceCrawlAttemptHistoryCount(t, raw, 42); count != 1 {
				t.Fatalf("history after replay = %d, want 1", count)
			}
		})
	}
}

func TestSourceCrawlAttemptSQLiteReplayAttributionClasses(t *testing.T) {
	type fixture func(*testing.T, *sql.DB)
	cases := []struct {
		name         string
		fixture      fixture
		initial      SourceCrawlClaimOutcome
		replay       SourceCrawlClaimOutcome
		bound        bool
		transferSide bool
	}{
		{
			name: "bound",
			fixture: func(t *testing.T, raw *sql.DB) {
				sourceCrawlInsertBoundOperation(t, raw, 42, 7, "class-bound", "accepted")
			},
			initial: SourceCrawlClaimed, replay: SourceCrawlClaimed, bound: true,
		},
		{
			name:    "unattributed",
			fixture: func(*testing.T, *sql.DB) {},
			initial: SourceCrawlUnattributed, replay: SourceCrawlUnattributed,
		},
		{
			name: "ambiguous",
			fixture: func(t *testing.T, raw *sql.DB) {
				sourceCrawlInsertBoundOperation(t, raw, 42, 7, "class-ambiguous-a", "accepted")
				sourceCrawlInsertBoundOperation(t, raw, 42, 7, "class-ambiguous-b", "accepted")
			},
			initial: SourceCrawlAmbiguous, replay: SourceCrawlUnattributed,
		},
		{
			name: "not attributable after transfer",
			fixture: func(t *testing.T, raw *sql.DB) {
				sourceCrawlInsertBoundOperation(t, raw, 42, 7, "class-transfer", "accepted")
				if _, err := raw.Exec(`UPDATE Sources SET usr_id=8 WHERE source_id=42`); err != nil {
					t.Fatalf("transfer source: %v", err)
				}
				if _, err := raw.Exec(`UPDATE SourceOwnership SET user_id=8 WHERE source_id=42`); err != nil {
					t.Fatalf("transfer ownership: %v", err)
				}
			},
			initial: SourceCrawlNotAttributable, replay: SourceCrawlUnattributed, transferSide: true,
		},
		{
			name: "already pending",
			fixture: func(t *testing.T, raw *sql.DB) {
				sourceCrawlInsertBoundOperation(t, raw, 42, 7, "class-already-pending", string(CrawlOperationAlreadyPending))
			},
			initial: SourceCrawlUnattributed, replay: SourceCrawlUnattributed,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, handler := openAsyncGoalSQLite(t)
			seedSourceCrawlClaimSource(t, raw, 42, 7, "new", false)
			enableSourceCrawlClaimMode(t, raw)
			tc.fixture(t, raw)
			request := sourceCrawlClaimRequest("class-" + tc.name)
			claims, err := claimSourceCrawlAttemptsAt(context.Background(), &handler, request, sourceCrawlClaimTestNow)
			if err != nil || len(claims) != 1 || claims[0].Outcome != tc.initial {
				t.Fatalf("initial claim = %+v err=%v", claims, err)
			}
			replay, err := claimSourceCrawlAttemptsAt(context.Background(), &handler, request,
				sourceCrawlClaimTestNow.Add(time.Minute))
			if err != nil || len(replay) != 1 || !replay[0].Reused ||
				replay[0].Outcome != tc.replay ||
				replay[0].AttemptID != claims[0].AttemptID || replay[0].Generation != claims[0].Generation {
				t.Fatalf("replay = %+v err=%v", replay, err)
			}
			if tc.bound {
				if replay[0].OperationID == "" || replay[0].OperationID != claims[0].OperationID ||
					replay[0].ApprovalID != claims[0].ApprovalID ||
					replay[0].PrincipalUserID != claims[0].PrincipalUserID {
					t.Fatalf("bound replay lost binding: initial=%+v replay=%+v", claims[0], replay[0])
				}
			} else if replay[0].OperationID != "" || replay[0].PrincipalUserID != 0 ||
				replay[0].ApprovalID != "" {
				t.Fatalf("unbound replay fabricated binding: %+v", replay[0])
			}
			if count := sourceCrawlAttemptHistoryCount(t, raw, 42); count != 1 {
				t.Fatalf("history after replay = %d, want 1", count)
			}
		})
	}
}

func TestSourceCrawlAttemptSQLiteReplayTerminalStates(t *testing.T) {
	for _, state := range []string{"completed", "failed", "cancelled", "expired", "abandoned"} {
		t.Run(state, func(t *testing.T) {
			raw, handler := openAsyncGoalSQLite(t)
			seedSourceCrawlClaimSource(t, raw, 42, 7, "new", false)
			enableSourceCrawlClaimMode(t, raw)
			sourceCrawlInsertBoundOperation(t, raw, 42, 7, "terminal-"+state, "accepted")
			request := sourceCrawlClaimRequest("terminal-" + state)
			if _, err := claimSourceCrawlAttemptsAt(context.Background(), &handler, request, sourceCrawlClaimTestNow); err != nil {
				t.Fatalf("base claim: %v", err)
			}
			if _, err := raw.Exec(`UPDATE SourceCrawlAttempts SET state=?, terminal_at=? WHERE source_id=42`,
				state, sourceCrawlClaimTestNow.Add(time.Second)); err != nil {
				t.Fatalf("set terminal state: %v", err)
			}
			replay, err := claimSourceCrawlAttemptsAt(context.Background(), &handler, request,
				sourceCrawlClaimTestNow.Add(time.Minute))
			if err != nil || len(replay) != 1 || !replay[0].Reused || replay[0].Outcome != SourceCrawlClaimStaleFence {
				t.Fatalf("terminal replay = %+v err=%v", replay, err)
			}
			if count := sourceCrawlAttemptHistoryCount(t, raw, 42); count != 1 {
				t.Fatalf("history after terminal replay = %d, want 1", count)
			}
		})
	}
}

func TestSourceCrawlAttemptSQLiteReplaySuperseded(t *testing.T) {
	raw, handler := openAsyncGoalSQLite(t)
	seedSourceCrawlClaimSource(t, raw, 42, 7, "new", false)
	enableSourceCrawlClaimMode(t, raw)
	firstRequest := sourceCrawlClaimRequest("superseded-first")
	first, err := claimSourceCrawlAttemptsAt(context.Background(), &handler, firstRequest, sourceCrawlClaimTestNow)
	if err != nil || len(first) != 1 {
		t.Fatalf("first claim = %+v err=%v", first, err)
	}
	if _, err := raw.Exec(`UPDATE Sources SET status='pending' WHERE source_id=42`); err != nil {
		t.Fatalf("reset source: %v", err)
	}
	second, err := claimSourceCrawlAttemptsAt(context.Background(), &handler,
		sourceCrawlClaimRequest("superseded-second"), sourceCrawlClaimTestNow.Add(6*time.Minute))
	if err != nil || len(second) != 1 || second[0].Generation != 2 {
		t.Fatalf("second claim = %+v err=%v", second, err)
	}
	replay, err := claimSourceCrawlAttemptsAt(context.Background(), &handler, firstRequest,
		sourceCrawlClaimTestNow.Add(7*time.Minute))
	if err != nil || len(replay) != 1 || !replay[0].Reused ||
		replay[0].Outcome != SourceCrawlClaimStaleFence ||
		replay[0].AttemptID != first[0].AttemptID || replay[0].Generation != first[0].Generation {
		t.Fatalf("superseded replay = %+v err=%v", replay, err)
	}
	if count := sourceCrawlAttemptHistoryCount(t, raw, 42); count != 2 {
		t.Fatalf("history after superseded replay = %d, want 2", count)
	}
}

func TestSourceCrawlAttemptSQLiteConcurrentReplay(t *testing.T) {
	raw, handler := openAsyncGoalSQLite(t)
	seedSourceCrawlClaimSource(t, raw, 42, 7, "new", false)
	enableSourceCrawlClaimMode(t, raw)
	sourceCrawlInsertBoundOperation(t, raw, 42, 7, "concurrent-replay", "accepted")
	request := sourceCrawlClaimRequest("concurrent-replay")
	first, err := claimSourceCrawlAttemptsAt(context.Background(), &handler, request, sourceCrawlClaimTestNow)
	if err != nil || len(first) != 1 {
		t.Fatalf("base claim = %+v err=%v", first, err)
	}
	type result struct {
		claims []SourceCrawlClaim
		err    error
	}
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			claims, err := claimSourceCrawlAttemptsAt(context.Background(), &handler, request,
				sourceCrawlClaimTestNow.Add(time.Minute))
			results <- result{claims: claims, err: err}
		}()
	}
	for i := 0; i < 2; i++ {
		res := <-results
		if res.err != nil || len(res.claims) != 1 || !res.claims[0].Reused ||
			res.claims[0].Outcome != SourceCrawlClaimed ||
			res.claims[0].AttemptID != first[0].AttemptID {
			t.Fatalf("concurrent replay = %+v err=%v", res.claims, res.err)
		}
	}
	if count := sourceCrawlAttemptHistoryCount(t, raw, 42); count != 1 {
		t.Fatalf("history after concurrent replay = %d, want 1", count)
	}
	if _, err := raw.Exec(`UPDATE Sources SET status='pending' WHERE source_id=42`); err != nil {
		t.Fatalf("reset source: %v", err)
	}
}
