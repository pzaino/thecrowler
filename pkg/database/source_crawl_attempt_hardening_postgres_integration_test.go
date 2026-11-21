//go:build integration

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
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/lib/pq"
)

func sourceCrawlAttemptInsertHistory(t *testing.T, admin *sql.DB, sourceID uint64, label, attemptID string, generation int64, state string) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := admin.Exec(`INSERT INTO SourceCrawlAttempts
		(attempt_id, claim_request_id, source_id, generation, engine_id, state,
		 claimed_at, lease_expires_at, created_at, last_updated_at)
		VALUES ($1,$2,$3,$4,'fixture-engine',$5,$6,$7,$6,$6)`,
		attemptID, sourceCrawlClaimTestID("history-"+label), sourceID, generation,
		state, now, now.Add(time.Hour)); err != nil {
		t.Fatalf("insert history %s: %v", label, err)
	}
}

func sourceCrawlAttemptOperationRequestID(t *testing.T, admin *sql.DB, operationID string) string {
	t.Helper()
	var requestID string
	if err := admin.QueryRow(`SELECT request_id FROM CrawlProposals WHERE operation_id=$1`, operationID).Scan(&requestID); err != nil {
		t.Fatalf("read operation request ID: %v", err)
	}
	return requestID
}

func TestPostgresSourceCrawlAttemptExactHistoryFK(t *testing.T) {
	db, _ := openPostgresIntegrationTestDB(t)
	adminDB := openAsyncGoalPostgresAdminDB(t)
	priority := fmt.Sprintf("wp05b3c2c-fk-%d", time.Now().UnixNano())
	sourceID := createCrawlSchedulingSource(t, db, priority)
	t.Cleanup(func() { cleanupSourceCrawlAttemptRows(t, adminDB, sourceID) })
	setSourceCrawlAttemptMode(t, adminDB, "fenced")

	attemptA := sourceCrawlClaimTestID("fk-a")
	attemptB := sourceCrawlClaimTestID("fk-b")
	sourceCrawlAttemptInsertHistory(t, adminDB, sourceID, "a", attemptA, 1, "claimed")
	sourceCrawlAttemptInsertHistory(t, adminDB, sourceID, "b", attemptB, 2, "claimed")

	tx, err := adminDB.Begin()
	if err != nil {
		t.Fatalf("begin mixed-claim transaction: %v", err)
	}
	_, err = tx.Exec(`INSERT INTO SourceCrawlClaims
		(source_id, attempt_id, claim_request_id, generation, engine_id,
		 claimed_at, lease_expires_at, last_updated_at)
		VALUES ($1,$2,$3,2,'fixture-engine',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP + INTERVAL '1 hour',CURRENT_TIMESTAMP)`,
		sourceID, attemptA, sourceCrawlClaimTestID("fk-mixed"))
	if err == nil {
		err = tx.Commit()
	} else {
		_ = tx.Rollback()
	}
	if err == nil {
		t.Fatal("current claim mixed attempt A with generation B")
	}
	var count int
	if err := adminDB.QueryRow(`SELECT COUNT(*) FROM SourceCrawlClaims WHERE source_id=$1`, sourceID).Scan(&count); err != nil {
		t.Fatalf("count mixed claims: %v", err)
	}
	if count != 0 {
		t.Fatalf("mixed claim persisted: %d", count)
	}

	// The exact mapping is accepted.
	if _, err := adminDB.Exec(`INSERT INTO SourceCrawlClaims
		(source_id, attempt_id, claim_request_id, generation, engine_id,
		 claimed_at, lease_expires_at, last_updated_at)
		VALUES ($1,$2,$3,1,'fixture-engine',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP + INTERVAL '1 hour',CURRENT_TIMESTAMP)`,
		sourceID, attemptA, sourceCrawlClaimTestID("fk-valid")); err != nil {
		t.Fatalf("exact history claim rejected: %v", err)
	}
	if _, err := adminDB.Exec(`UPDATE SourceCrawlClaims SET generation=2 WHERE source_id=$1`, sourceID); err == nil {
		t.Fatal("current claim update mixed attempt A with generation B")
	}
}

func TestPostgresSourceCrawlAttemptRuntimeCannotChangeMode(t *testing.T) {
	db, _ := openPostgresIntegrationTestDB(t)
	adminDB := openAsyncGoalPostgresAdminDB(t)
	var currentUser string
	if err := adminDB.QueryRow(`SELECT current_user`).Scan(&currentUser); err != nil {
		t.Fatalf("read admin user: %v", err)
	}
	var runtimeUser string
	if err := (*db).QueryRowContext(context.Background(), `SELECT current_user`).Scan(&runtimeUser); err != nil {
		t.Fatalf("read runtime user: %v", err)
	}
	if runtimeUser == currentUser {
		t.Skip("runtime and administrative connections use the same PostgreSQL role")
	}
	if _, err := (*db).Exec(`SELECT set_source_crawl_claim_mode('fenced')`); err == nil {
		t.Fatal("runtime role executed the operator mode function")
	}
	if _, err := (*db).Exec(`UPDATE SourceCrawlClaimPolicy SET mode='fenced' WHERE policy_id=1`); err == nil {
		t.Fatal("runtime role updated the policy table directly")
	}
}

func TestPostgresSourceCrawlAttemptPolicyLock(t *testing.T) {
	db, _ := openPostgresIntegrationTestDB(t)
	adminDB := openAsyncGoalPostgresAdminDB(t)
	setSourceCrawlAttemptMode(t, adminDB, "legacy")
	ctx := context.Background()

	tx1, err := (*db).BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin first shared-lock tx: %v", err)
	}
	defer tx1.Rollback()
	tx2, err := (*db).BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin second shared-lock tx: %v", err)
	}
	defer tx2.Rollback()
	for i, tx := range []*sql.Tx{tx1, tx2} {
		if _, err := tx.ExecContext(ctx,
			`SELECT pg_advisory_xact_lock_shared(hashtext('crowler:source-crawl-claim-policy'))`); err != nil {
			t.Fatalf("shared policy lock %d: %v", i+1, err)
		}
	}

	modeTx, err := adminDB.Begin()
	if err != nil {
		t.Fatalf("begin mode switch tx: %v", err)
	}
	if _, err := modeTx.Exec(`SET LOCAL lock_timeout='150ms'`); err != nil {
		_ = modeTx.Rollback()
		t.Fatalf("set lock timeout: %v", err)
	}
	if _, err := modeTx.Exec(`SELECT set_source_crawl_claim_mode('fenced')`); err == nil {
		_ = modeTx.Rollback()
		t.Fatal("mode switch committed while shared claim locks were held")
	}
	_ = modeTx.Rollback()
	if err := tx1.Commit(); err != nil {
		t.Fatalf("commit first shared lock: %v", err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatalf("commit second shared lock: %v", err)
	}
	setSourceCrawlAttemptMode(t, adminDB, "legacy")
}

func TestPostgresSourceCrawlAttemptLegacyModeCutoverLock(t *testing.T) {
	db, _ := openPostgresIntegrationTestDB(t)
	adminDB := openAsyncGoalPostgresAdminDB(t)
	priority := fmt.Sprintf("wp05b3c2c-cutover-%d", time.Now().UnixNano())
	sourceID := createCrawlSchedulingSource(t, db, priority)
	t.Cleanup(func() { cleanupSourceCrawlAttemptRows(t, adminDB, sourceID) })
	setSourceCrawlAttemptMode(t, adminDB, "legacy")

	tx, err := (*db).BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin legacy claim tx: %v", err)
	}
	defer tx.Rollback()
	var claimedID uint64
	if err := tx.QueryRow(`SELECT source_id FROM update_sources($1,$2,$3,$4,$5,$6,$7)`,
		10, priority, "cutover-engine", "", "", "", "1 day").Scan(&claimedID); err != nil {
		t.Fatalf("legacy update_sources: %v", err)
	}
	if claimedID != sourceID {
		t.Fatalf("legacy claim returned source %d, want %d", claimedID, sourceID)
	}

	modeTx, err := adminDB.Begin()
	if err != nil {
		t.Fatalf("begin cutover mode tx: %v", err)
	}
	if _, err := modeTx.Exec(`SET LOCAL lock_timeout='150ms'`); err != nil {
		_ = modeTx.Rollback()
		t.Fatalf("set cutover lock timeout: %v", err)
	}
	if _, err := modeTx.Exec(`SELECT set_source_crawl_claim_mode('fenced')`); err == nil {
		_ = modeTx.Rollback()
		t.Fatal("mode switch committed during an in-flight legacy claim")
	}
	_ = modeTx.Rollback()
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit legacy claim: %v", err)
	}
	setSourceCrawlAttemptMode(t, adminDB, "fenced")
	var rows int
	if err := adminDB.QueryRow(`SELECT COUNT(*) FROM update_sources($1,$2,$3,$4,$5,$6,$7)`,
		10, priority, "cutover-old-worker", "", "", "", "1 day").Scan(&rows); err != nil {
		t.Fatalf("read fenced update_sources rows: %v", err)
	}
	if rows != 0 {
		t.Fatalf("fenced update_sources returned %d rows", rows)
	}
}

func TestPostgresSourceCrawlAttemptGenerationRecovery(t *testing.T) {
	db, _ := openPostgresIntegrationTestDB(t)
	adminDB := openAsyncGoalPostgresAdminDB(t)
	priority := fmt.Sprintf("wp05b3c2c-generation-%d", time.Now().UnixNano())
	sourceID := createCrawlSchedulingSource(t, db, priority)
	t.Cleanup(func() { cleanupSourceCrawlAttemptRows(t, adminDB, sourceID) })
	setSourceCrawlAttemptMode(t, adminDB, "fenced")

	first, err := ClaimSourceCrawlAttempts(context.Background(), db, sourceCrawlAttemptRequestFor("gen-first", priority))
	if err != nil || len(first) != 1 || first[0].SourceID != sourceID || first[0].Generation != 1 {
		t.Fatalf("first generation claim = %+v err=%v (source %d)", first, err, sourceID)
	}
	if _, err := adminDB.Exec(`DELETE FROM SourceCrawlClaims WHERE source_id=$1`, sourceID); err != nil {
		t.Fatalf("remove current claim: %v", err)
	}
	resetSourceCrawlAttemptSource(t, db, sourceID, "pending")
	second, err := ClaimSourceCrawlAttempts(context.Background(), db, sourceCrawlAttemptRequestFor("gen-second", priority))
	if err != nil || len(second) != 1 || second[0].SourceID != sourceID || second[0].Generation != 2 {
		t.Fatalf("recovered generation = %+v err=%v (source %d)", second, err, sourceID)
	}

	sourceCrawlAttemptInsertHistory(t, adminDB, sourceID, "max", sourceCrawlClaimTestID("gen-max"), math.MaxInt64, "claimed")
	if _, err := adminDB.Exec(`DELETE FROM SourceCrawlClaims WHERE source_id=$1`, sourceID); err != nil {
		t.Fatalf("remove current generation claim: %v", err)
	}
	resetSourceCrawlAttemptSource(t, db, sourceID, "pending")
	if _, err := ClaimSourceCrawlAttempts(context.Background(), db, sourceCrawlAttemptRequestFor("gen-overflow", priority)); !errors.Is(err, ErrSourceCrawlClaimGenerationDone) {
		t.Fatalf("overflow generation error = %v", err)
	}
}

func TestPostgresSourceCrawlAttemptReplayValidation(t *testing.T) {
	db, _ := openPostgresIntegrationTestDB(t)
	adminDB := openAsyncGoalPostgresAdminDB(t)
	priority := fmt.Sprintf("wp05b3c2c-replay-%d", time.Now().UnixNano())
	sourceID := createCrawlSchedulingSource(t, db, priority)
	t.Cleanup(func() { cleanupSourceCrawlAttemptRows(t, adminDB, sourceID) })
	setSourceCrawlAttemptMode(t, adminDB, "fenced")

	request := sourceCrawlAttemptRequestFor("replay", priority)
	first, err := ClaimSourceCrawlAttempts(context.Background(), db, request)
	if err != nil || len(first) != 1 {
		t.Fatalf("replay base claim = %+v err=%v", first, err)
	}
	replay, err := ClaimSourceCrawlAttempts(context.Background(), db, request)
	if err != nil || len(replay) != 1 || !replay[0].Reused ||
		replay[0].AttemptID != first[0].AttemptID || replay[0].EngineID != request.EngineID {
		t.Fatalf("matching replay = %+v err=%v", replay, err)
	}
	changedEngine := request
	changedEngine.EngineID = "different-engine"
	if _, err := ClaimSourceCrawlAttempts(context.Background(), db, changedEngine); !errors.Is(err, ErrSourceCrawlClaimConflict) {
		t.Fatalf("changed engine replay error = %v", err)
	}
	changedLease := request
	changedLease.Lease = request.Lease + time.Minute
	if _, err := ClaimSourceCrawlAttempts(context.Background(), db, changedLease); !errors.Is(err, ErrSourceCrawlClaimConflict) {
		t.Fatalf("changed lease replay error = %v", err)
	}
}

func TestPostgresSourceCrawlAttemptAttributionFailClosed(t *testing.T) {
	db, _ := openPostgresIntegrationTestDB(t)
	adminDB := openAsyncGoalPostgresAdminDB(t)
	priority := fmt.Sprintf("wp05b3c2c-attribution-%d", time.Now().UnixNano())
	sourceID := createCrawlSchedulingSource(t, db, priority)
	t.Cleanup(func() { cleanupSourceCrawlAttemptRows(t, adminDB, sourceID) })
	setSourceCrawlAttemptMode(t, adminDB, "fenced")
	now := time.Now().UTC().Truncate(time.Microsecond)

	t.Run("valid plus missing approval is ambiguous", func(t *testing.T) {
		first := sourceCrawlAttemptInsertOperation(t, db, sourceID, 7, "attr-valid", "accepted", now.Add(-time.Minute), now)
		second := sourceCrawlAttemptInsertOperation(t, db, sourceID, 7, "attr-missing", "accepted", now.Add(-time.Minute), now)
		_ = first
		requestID := sourceCrawlAttemptOperationRequestID(t, adminDB, second)
		if _, err := adminDB.Exec(`DELETE FROM CrawlApprovals WHERE request_id=$1`, requestID); err != nil {
			t.Fatalf("remove competing approval: %v", err)
		}
		claims, err := ClaimSourceCrawlAttempts(context.Background(), db, sourceCrawlAttemptRequestFor("attr-ambiguous-approval", priority))
		if err != nil || len(claims) != 1 || claims[0].Outcome != SourceCrawlAmbiguous || claims[0].OperationID != "" {
			t.Fatalf("valid plus missing approval = %+v err=%v", claims, err)
		}
		cleanupSourceCrawlAttemptRows(t, adminDB, sourceID)
		setSourceCrawlAttemptMode(t, adminDB, "fenced")
	})

	t.Run("valid plus malformed details is ambiguous", func(t *testing.T) {
		first := sourceCrawlAttemptInsertOperation(t, db, sourceID, 7, "attr-valid2", "accepted", now.Add(-time.Minute), now)
		second := sourceCrawlAttemptInsertOperation(t, db, sourceID, 7, "attr-malformed", "accepted", now.Add(-time.Minute), now)
		_ = first
		if _, err := adminDB.Exec(`UPDATE Status SET details='{"request_id":123}'::jsonb WHERE status_sha256=$1`, second); err != nil {
			t.Fatalf("corrupt competing details: %v", err)
		}
		claims, err := ClaimSourceCrawlAttempts(context.Background(), db, sourceCrawlAttemptRequestFor("attr-ambiguous-details", priority))
		if err != nil || len(claims) != 1 || claims[0].Outcome != SourceCrawlAmbiguous || claims[0].OperationID != "" {
			t.Fatalf("valid plus malformed details = %+v err=%v", claims, err)
		}
		cleanupSourceCrawlAttemptRows(t, adminDB, sourceID)
		setSourceCrawlAttemptMode(t, adminDB, "fenced")
	})
}

func TestPostgresSourceCrawlAttemptForgedAndDuplicateDetails(t *testing.T) {
	db, _ := openPostgresIntegrationTestDB(t)
	adminDB := openAsyncGoalPostgresAdminDB(t)
	priority := fmt.Sprintf("wp05b3c2c-forged-%d", time.Now().UnixNano())
	sourceID := createCrawlSchedulingSource(t, db, priority)
	t.Cleanup(func() { cleanupSourceCrawlAttemptRows(t, adminDB, sourceID) })
	setSourceCrawlAttemptMode(t, adminDB, "fenced")
	now := time.Now().UTC().Truncate(time.Microsecond)

	t.Run("forged deterministic operation ID", func(t *testing.T) {
		operationID := sourceCrawlAttemptInsertOperation(t, db, sourceID, 7, "forged-key", "accepted", now.Add(-time.Minute), now)
		forged := sourceCrawlClaimTestID("forged-operation")
		if _, err := adminDB.Exec(`UPDATE CrawlProposals SET operation_id=$1 WHERE operation_id=$2`, forged, operationID); err != nil {
			t.Fatalf("forge operation ID: %v", err)
		}
		if _, err := adminDB.Exec(`UPDATE Status SET status_sha256=$1 WHERE status_sha256=$2`, forged, operationID); err != nil {
			t.Fatalf("forge status ID: %v", err)
		}
		claims, err := ClaimSourceCrawlAttempts(context.Background(), db, sourceCrawlAttemptRequestFor("forged-key-claim", priority))
		if err != nil || len(claims) != 1 || claims[0].Outcome != SourceCrawlNotAttributable || claims[0].OperationID != "" {
			t.Fatalf("forged operation = %+v err=%v", claims, err)
		}
		cleanupSourceCrawlAttemptRows(t, adminDB, sourceID)
		setSourceCrawlAttemptMode(t, adminDB, "fenced")
	})

	t.Run("malformed details shape", func(t *testing.T) {
		operationID := sourceCrawlAttemptInsertOperation(t, db, sourceID, 7, "dup-key", "accepted", now.Add(-time.Minute), now)
		if _, err := adminDB.Exec(`UPDATE Status SET details='{"request_id":123}'::jsonb WHERE status_sha256=$1`, operationID); err != nil {
			t.Fatalf("add malformed details shape: %v", err)
		}
		claims, err := ClaimSourceCrawlAttempts(context.Background(), db, sourceCrawlAttemptRequestFor("dup-key-claim", priority))
		if err != nil || len(claims) != 1 || claims[0].Outcome != SourceCrawlNotAttributable || claims[0].OperationID != "" {
			t.Fatalf("malformed details = %+v err=%v", claims, err)
		}
	})
}

func TestPostgresSourceCrawlAttemptWrongChronology(t *testing.T) {
	db, _ := openPostgresIntegrationTestDB(t)
	adminDB := openAsyncGoalPostgresAdminDB(t)
	priority := fmt.Sprintf("wp05b3c2c-chrono-%d", time.Now().UnixNano())
	sourceID := createCrawlSchedulingSource(t, db, priority)
	t.Cleanup(func() { cleanupSourceCrawlAttemptRows(t, adminDB, sourceID) })
	setSourceCrawlAttemptMode(t, adminDB, "fenced")
	now := time.Now().UTC().Truncate(time.Microsecond)
	operationID := sourceCrawlAttemptInsertOperation(t, db, sourceID, 7, "chrono", "accepted", now.Add(-time.Minute), now)
	if _, err := adminDB.Exec(`UPDATE CrawlApprovals SET approved_at=$1 WHERE approval_id=(
		SELECT approval_id FROM CrawlApprovals WHERE source_id=$2)`, now.Add(30*time.Minute), sourceID); err != nil {
		t.Fatalf("move approval after acceptance: %v", err)
	}
	_ = operationID
	claims, err := ClaimSourceCrawlAttempts(context.Background(), db, sourceCrawlAttemptRequestFor("chrono-claim", priority))
	if err != nil || len(claims) != 1 || claims[0].Outcome != SourceCrawlNotAttributable || claims[0].OperationID != "" {
		t.Fatalf("wrong chronology = %+v err=%v", claims, err)
	}
}

func TestPostgresSourceCrawlAttemptGenuineSchedulerBinding(t *testing.T) {
	db, _ := openPostgresIntegrationTestDB(t)
	adminDB := openAsyncGoalPostgresAdminDB(t)
	priority := fmt.Sprintf("wp05b3c2c-genuine-%d", time.Now().UnixNano())
	sourceID := createCrawlSchedulingSource(t, db, priority)
	t.Cleanup(func() { cleanupSourceCrawlAttemptRows(t, adminDB, sourceID) })
	setCrawlSchedulingSourceState(t, db, sourceID, "completed", "", false)
	now := time.Now().UTC()
	req := crawlSchedulingRequest(sourceID)
	req.RequestID = sourceCrawlClaimTestID("genuine-request")
	req.RunID = fmt.Sprintf("wp05b3c2c-genuine-run-%d", time.Now().UnixNano())
	req.ApprovedAt = now.Add(-time.Minute)
	req.ExpiresAt = now.Add(time.Hour)
	req = preparePostgresApprovalRequest(req)
	ensurePostgresCrawlApproval(t, db, req)
	result, err := ScheduleSourceCrawlRequest(context.Background(), db, req)
	if err != nil || result.State != CrawlOperationAccepted {
		t.Fatalf("schedule genuine PostgreSQL operation = %+v err=%v", result, err)
	}
	if _, err := adminDB.Exec(`INSERT INTO CrawlProposals
		(request_id, run_id, tool_call_id, agent_id, owner, principal_user_id, source_id,
		 operation_type, proposal_sha256, trace_id, max_requests, budget_window_seconds,
		 policy_json, state, expires_at, submitted_at, operation_id, created_at, last_updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'crawl',$8,'wp05b3c2c-genuine-trace',1,3600,'{}',
		 'submitted',$9,$10,$11,$10,$10)`,
		req.RequestID, req.RunID, "wp05b3c2c-genuine-tool", req.AgentID, req.Owner,
		req.PrincipalUserID, req.SourceID, req.ParametersSHA256, req.ExpiresAt,
		now, result.OperationID); err != nil {
		t.Fatalf("insert genuine proposal: %v", err)
	}
	setSourceCrawlAttemptMode(t, adminDB, "fenced")
	claims, err := ClaimSourceCrawlAttempts(context.Background(), db, sourceCrawlAttemptRequestFor("pg-genuine", priority))
	if err != nil || len(claims) != 1 || claims[0].Outcome != SourceCrawlClaimed ||
		claims[0].OperationID != result.OperationID || claims[0].PrincipalUserID != 7 {
		t.Fatalf("genuine PostgreSQL binding = %+v err=%v", claims, err)
	}
}

func sourceCrawlAttemptInsertSubmittedProposal(t *testing.T, admin *sql.DB, sourceID, ownerID uint64,
	label string, operationID any) string {
	t.Helper()
	requestID := sourceCrawlClaimTestID("pg-submitted-request-" + label)
	proposalSHA := sourceCrawlClaimTestID("pg-submitted-proposal-" + label)
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := admin.Exec(`INSERT INTO CrawlProposals
		(request_id, run_id, tool_call_id, agent_id, owner, principal_user_id, source_id,
		 operation_type, proposal_sha256, trace_id, max_requests, budget_window_seconds,
		 policy_json, state, expires_at, submitted_at, operation_id, created_at, last_updated_at)
		VALUES ($1,$2,$3,'wp05b3c2-agent','wp03b1-owner',$4,$5,'crawl',$6,'wp05b3c2-trace',
		 1,3600,'{}','submitted',$7,$8,$9,$8,$8)`,
		requestID, "run-"+label, "tool-"+label, ownerID, sourceID, proposalSHA,
		now.Add(2*time.Hour), now, operationID); err != nil {
		t.Fatalf("insert submitted proposal %s: %v", label, err)
	}
	return requestID
}

func TestPostgresSourceCrawlAttemptSubmittedProposalCompetition(t *testing.T) {
	db, _ := openPostgresIntegrationTestDB(t)
	adminDB := openAsyncGoalPostgresAdminDB(t)
	now := time.Now().UTC().Truncate(time.Microsecond)

	t.Run("valid plus null operation id is ambiguous", func(t *testing.T) {
		priority := fmt.Sprintf("wp05b3c3a-null-%d", time.Now().UnixNano())
		sourceID := createCrawlSchedulingSource(t, db, priority)
		t.Cleanup(func() { cleanupSourceCrawlAttemptRows(t, adminDB, sourceID) })
		setSourceCrawlAttemptMode(t, adminDB, "fenced")
		sourceCrawlAttemptInsertOperation(t, db, sourceID, 7, "null-valid", "accepted", now.Add(-time.Minute), now)
		sourceCrawlAttemptInsertSubmittedProposal(t, adminDB, sourceID, 7, "null", nil)
		request := sourceCrawlAttemptRequestFor("pg-null-competition", priority)
		claims, err := ClaimSourceCrawlAttempts(context.Background(), db, request)
		if err != nil || len(claims) != 1 || claims[0].Outcome != SourceCrawlAmbiguous || claims[0].OperationID != "" {
			t.Fatalf("valid plus null operation = %+v err=%v", claims, err)
		}
		replay, err := ClaimSourceCrawlAttempts(context.Background(), db, request)
		if err != nil || len(replay) != 1 || !replay[0].Reused ||
			replay[0].Outcome != SourceCrawlUnattributed || replay[0].OperationID != "" ||
			replay[0].AttemptID != claims[0].AttemptID || replay[0].Generation != claims[0].Generation {
			t.Fatalf("ambiguous replay = %+v err=%v", replay, err)
		}
	})

	t.Run("valid plus blank operation id is ambiguous", func(t *testing.T) {
		priority := fmt.Sprintf("wp05b3c3a-blank-%d", time.Now().UnixNano())
		sourceID := createCrawlSchedulingSource(t, db, priority)
		t.Cleanup(func() { cleanupSourceCrawlAttemptRows(t, adminDB, sourceID) })
		setSourceCrawlAttemptMode(t, adminDB, "fenced")
		sourceCrawlAttemptInsertOperation(t, db, sourceID, 7, "blank-valid", "accepted", now.Add(-time.Minute), now)
		sourceCrawlAttemptInsertSubmittedProposal(t, adminDB, sourceID, 7, "blank", "")
		claims, err := ClaimSourceCrawlAttempts(context.Background(), db,
			sourceCrawlAttemptRequestFor("pg-blank-competition", priority))
		if err != nil || len(claims) != 1 || claims[0].Outcome != SourceCrawlAmbiguous || claims[0].OperationID != "" {
			t.Fatalf("valid plus blank operation = %+v err=%v", claims, err)
		}
	})

	t.Run("valid plus forged operation id is ambiguous", func(t *testing.T) {
		priority := fmt.Sprintf("wp05b3c3a-forged-%d", time.Now().UnixNano())
		sourceID := createCrawlSchedulingSource(t, db, priority)
		t.Cleanup(func() { cleanupSourceCrawlAttemptRows(t, adminDB, sourceID) })
		setSourceCrawlAttemptMode(t, adminDB, "fenced")
		sourceCrawlAttemptInsertOperation(t, db, sourceID, 7, "forged-valid", "accepted", now.Add(-time.Minute), now)
		sourceCrawlAttemptInsertSubmittedProposal(t, adminDB, sourceID, 7, "forged",
			sourceCrawlClaimTestID("forged-competitor-operation"))
		claims, err := ClaimSourceCrawlAttempts(context.Background(), db,
			sourceCrawlAttemptRequestFor("pg-forged-competition", priority))
		if err != nil || len(claims) != 1 || claims[0].Outcome != SourceCrawlAmbiguous || claims[0].OperationID != "" {
			t.Fatalf("valid plus forged operation = %+v err=%v", claims, err)
		}
	})

	t.Run("single malformed submitted proposal is not attributable", func(t *testing.T) {
		priority := fmt.Sprintf("wp05b3c3a-single-%d", time.Now().UnixNano())
		sourceID := createCrawlSchedulingSource(t, db, priority)
		t.Cleanup(func() { cleanupSourceCrawlAttemptRows(t, adminDB, sourceID) })
		setSourceCrawlAttemptMode(t, adminDB, "fenced")
		sourceCrawlAttemptInsertSubmittedProposal(t, adminDB, sourceID, 7, "single-null", nil)
		request := sourceCrawlAttemptRequestFor("pg-single-malformed", priority)
		claims, err := ClaimSourceCrawlAttempts(context.Background(), db, request)
		if err != nil || len(claims) != 1 || claims[0].Outcome != SourceCrawlNotAttributable || claims[0].OperationID != "" {
			t.Fatalf("single malformed proposal = %+v err=%v", claims, err)
		}
		replay, err := ClaimSourceCrawlAttempts(context.Background(), db, request)
		if err != nil || len(replay) != 1 || !replay[0].Reused || replay[0].Outcome != SourceCrawlUnattributed {
			t.Fatalf("single malformed replay = %+v err=%v", replay, err)
		}
	})

	t.Run("pending proposal is not a submitted competitor", func(t *testing.T) {
		priority := fmt.Sprintf("wp05b3c3a-pending-%d", time.Now().UnixNano())
		sourceID := createCrawlSchedulingSource(t, db, priority)
		t.Cleanup(func() { cleanupSourceCrawlAttemptRows(t, adminDB, sourceID) })
		setSourceCrawlAttemptMode(t, adminDB, "fenced")
		operationID := sourceCrawlAttemptInsertOperation(t, db, sourceID, 7, "pending-valid", "accepted", now.Add(-time.Minute), now)
		inserted, err := InsertCrawlProposal(context.Background(), db, CrawlProposalRow{
			RequestID: sourceCrawlClaimTestID("pg-pending-request"), RunID: "pg-pending-run",
			ToolCallID: "pg-pending-tool", AgentID: "wp05b3c2-agent", Owner: "wp03b1-owner",
			PrincipalUserID: 7, SourceID: sourceID, OperationType: CrawlOperationType,
			ProposalSHA256: sourceCrawlClaimTestID("pg-pending-proposal"), TraceID: "pg-pending-trace",
			MaxRequests: 1, BudgetWindowSeconds: 3600, PolicyJSON: "{}",
			State: CrawlProposalStatePending, ExpiresAt: now.Add(2 * time.Hour),
		})
		if err != nil || !inserted {
			t.Fatalf("insert pending proposal: inserted=%v err=%v", inserted, err)
		}
		claims, err := ClaimSourceCrawlAttempts(context.Background(), db,
			sourceCrawlAttemptRequestFor("pg-pending-competition", priority))
		if err != nil || len(claims) != 1 || claims[0].Outcome != SourceCrawlClaimed ||
			claims[0].OperationID != operationID {
			t.Fatalf("pending competitor claim = %+v err=%v", claims, err)
		}
	})
}

func TestPostgresSourceCrawlAttemptReplayClassification(t *testing.T) {
	db, _ := openPostgresIntegrationTestDB(t)
	adminDB := openAsyncGoalPostgresAdminDB(t)
	now := time.Now().UTC().Truncate(time.Microsecond)

	t.Run("lease boundaries", func(t *testing.T) {
		priority := fmt.Sprintf("wp05b3c3a-lease-%d", time.Now().UnixNano())
		sourceID := createCrawlSchedulingSource(t, db, priority)
		t.Cleanup(func() { cleanupSourceCrawlAttemptRows(t, adminDB, sourceID) })
		setSourceCrawlAttemptMode(t, adminDB, "fenced")
		sourceCrawlAttemptInsertOperation(t, db, sourceID, 7, "lease", "accepted", now.Add(-time.Minute), now)
		request := sourceCrawlAttemptRequestFor("pg-lease", priority)
		claims, err := claimSourceCrawlAttemptsAt(context.Background(), db, request, now)
		if err != nil || len(claims) != 1 || claims[0].Outcome != SourceCrawlClaimed {
			t.Fatalf("base lease claim = %+v err=%v", claims, err)
		}
		cases := []struct {
			name    string
			replay  time.Time
			outcome SourceCrawlClaimOutcome
		}{
			{"before expiry", now.Add(request.Lease - time.Microsecond), SourceCrawlClaimed},
			{"at expiry", now.Add(request.Lease), SourceCrawlClaimStaleFence},
			{"after expiry", now.Add(request.Lease + time.Microsecond), SourceCrawlClaimStaleFence},
		}
		for _, tc := range cases {
			replay, err := claimSourceCrawlAttemptsAt(context.Background(), db, request, tc.replay)
			if err != nil || len(replay) != 1 || !replay[0].Reused ||
				replay[0].Outcome != tc.outcome || replay[0].AttemptID != claims[0].AttemptID {
				t.Fatalf("%s replay = %+v err=%v", tc.name, replay, err)
			}
		}
	})

	t.Run("concurrent replay", func(t *testing.T) {
		priority := fmt.Sprintf("wp05b3c3a-concurrent-replay-%d", time.Now().UnixNano())
		sourceID := createCrawlSchedulingSource(t, db, priority)
		t.Cleanup(func() { cleanupSourceCrawlAttemptRows(t, adminDB, sourceID) })
		setSourceCrawlAttemptMode(t, adminDB, "fenced")
		sourceCrawlAttemptInsertOperation(t, db, sourceID, 7, "concurrent-replay", "accepted", now.Add(-time.Minute), now)
		request := sourceCrawlAttemptRequestFor("pg-concurrent-replay", priority)
		first, err := claimSourceCrawlAttemptsAt(context.Background(), db, request, now)
		if err != nil || len(first) != 1 {
			t.Fatalf("base concurrent replay claim = %+v err=%v", first, err)
		}
		type result struct {
			claims []SourceCrawlClaim
			err    error
		}
		results := make(chan result, 2)
		for i := 0; i < 2; i++ {
			go func() {
				claims, err := claimSourceCrawlAttemptsAt(context.Background(), db, request, now.Add(time.Minute))
				results <- result{claims: claims, err: err}
			}()
		}
		for i := 0; i < 2; i++ {
			res := <-results
			if res.err != nil || len(res.claims) != 1 || !res.claims[0].Reused ||
				res.claims[0].Outcome != SourceCrawlClaimed || res.claims[0].AttemptID != first[0].AttemptID {
				t.Fatalf("concurrent replay = %+v err=%v", res.claims, res.err)
			}
		}
	})

	t.Run("attribution classes replay without binding fabrication", func(t *testing.T) {
		cases := []struct {
			name    string
			setup   func(*testing.T, uint64)
			initial SourceCrawlClaimOutcome
			replay  SourceCrawlClaimOutcome
			bound   bool
		}{
			{"bound", func(t *testing.T, sourceID uint64) {
				sourceCrawlAttemptInsertOperation(t, db, sourceID, 7, "pg-class-bound", "accepted", now.Add(-time.Minute), now)
			}, SourceCrawlClaimed, SourceCrawlClaimed, true},
			{"unattributed", func(*testing.T, uint64) {}, SourceCrawlUnattributed, SourceCrawlUnattributed, false},
			{"ambiguous", func(t *testing.T, sourceID uint64) {
				sourceCrawlAttemptInsertOperation(t, db, sourceID, 7, "pg-class-ambiguous-a", "accepted", now.Add(-time.Minute), now)
				sourceCrawlAttemptInsertOperation(t, db, sourceID, 7, "pg-class-ambiguous-b", "accepted", now.Add(-time.Minute), now)
			}, SourceCrawlAmbiguous, SourceCrawlUnattributed, false},
			{"not attributable", func(t *testing.T, sourceID uint64) {
				sourceCrawlAttemptInsertOperation(t, db, sourceID, 7, "pg-class-transfer", "accepted", now.Add(-time.Minute), now)
				if _, err := adminDB.Exec(`UPDATE Sources SET usr_id=8 WHERE source_id=$1`, sourceID); err != nil {
					t.Fatalf("transfer source: %v", err)
				}
				if _, err := adminDB.Exec(`UPDATE SourceOwnership SET user_id=8 WHERE source_id=$1`, sourceID); err != nil {
					t.Fatalf("transfer ownership: %v", err)
				}
			}, SourceCrawlNotAttributable, SourceCrawlUnattributed, false},
			{"already pending", func(t *testing.T, sourceID uint64) {
				sourceCrawlAttemptInsertOperation(t, db, sourceID, 7, "pg-class-pending", string(CrawlOperationAlreadyPending), now.Add(-time.Minute), now)
			}, SourceCrawlUnattributed, SourceCrawlUnattributed, false},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				priority := fmt.Sprintf("wp05b3c3a-class-%d-%d", time.Now().UnixNano(), len(tc.name))
				sourceID := createCrawlSchedulingSource(t, db, priority)
				t.Cleanup(func() { cleanupSourceCrawlAttemptRows(t, adminDB, sourceID) })
				setSourceCrawlAttemptMode(t, adminDB, "fenced")
				tc.setup(t, sourceID)
				request := sourceCrawlAttemptRequestFor("pg-class-"+tc.name, priority)
				claims, err := claimSourceCrawlAttemptsAt(context.Background(), db, request, now)
				if err != nil || len(claims) != 1 || claims[0].Outcome != tc.initial {
					t.Fatalf("initial claim = %+v err=%v", claims, err)
				}
				replay, err := claimSourceCrawlAttemptsAt(context.Background(), db, request, now.Add(time.Minute))
				if err != nil || len(replay) != 1 || !replay[0].Reused ||
					replay[0].Outcome != tc.replay || replay[0].AttemptID != claims[0].AttemptID {
					t.Fatalf("replay = %+v err=%v", replay, err)
				}
				if tc.bound {
					if replay[0].OperationID == "" || replay[0].OperationID != claims[0].OperationID ||
						replay[0].ApprovalID != claims[0].ApprovalID {
						t.Fatalf("bound replay lost binding: %+v", replay[0])
					}
				} else if replay[0].OperationID != "" || replay[0].ApprovalID != "" {
					t.Fatalf("unbound replay fabricated binding: %+v", replay[0])
				}
			})
		}
	})

	t.Run("terminal and superseded attempts stay stale", func(t *testing.T) {
		priority := fmt.Sprintf("wp05b3c3a-terminal-%d", time.Now().UnixNano())
		sourceID := createCrawlSchedulingSource(t, db, priority)
		t.Cleanup(func() { cleanupSourceCrawlAttemptRows(t, adminDB, sourceID) })
		setSourceCrawlAttemptMode(t, adminDB, "fenced")
		sourceCrawlAttemptInsertOperation(t, db, sourceID, 7, "pg-terminal", "accepted", now.Add(-time.Minute), now)
		request := sourceCrawlAttemptRequestFor("pg-terminal", priority)
		if _, err := claimSourceCrawlAttemptsAt(context.Background(), db, request, now); err != nil {
			t.Fatalf("terminal base claim: %v", err)
		}
		if _, err := adminDB.Exec(`UPDATE SourceCrawlAttempts SET state='completed', terminal_at=$1 WHERE source_id=$2`,
			now.Add(time.Second), sourceID); err != nil {
			t.Fatalf("complete attempt history: %v", err)
		}
		replay, err := claimSourceCrawlAttemptsAt(context.Background(), db, request, now.Add(time.Minute))
		if err != nil || len(replay) != 1 || !replay[0].Reused || replay[0].Outcome != SourceCrawlClaimStaleFence {
			t.Fatalf("terminal replay = %+v err=%v", replay, err)
		}

		if _, err := adminDB.Exec(`UPDATE Sources SET status='pending' WHERE source_id=$1`, sourceID); err != nil {
			t.Fatalf("reset source for supersession: %v", err)
		}
		second, err := claimSourceCrawlAttemptsAt(context.Background(), db,
			sourceCrawlAttemptRequestFor("pg-terminal-second", priority), now.Add(11*time.Minute))
		if err != nil || len(second) != 1 || second[0].Generation != 2 {
			t.Fatalf("superseding claim = %+v err=%v", second, err)
		}
		superseded, err := claimSourceCrawlAttemptsAt(context.Background(), db, request, now.Add(12*time.Minute))
		if err != nil || len(superseded) != 1 || !superseded[0].Reused ||
			superseded[0].Outcome != SourceCrawlClaimStaleFence {
			t.Fatalf("superseded replay = %+v err=%v", superseded, err)
		}
	})
}

func TestPostgresSourceCrawlAttemptFinalFreshnessAllOutcomes(t *testing.T) {
	db, sqlDB := openPostgresIntegrationTestDB(t)
	adminDB := openAsyncGoalPostgresAdminDB(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	lease := time.Minute

	type attribution struct {
		name  string
		setup func(*testing.T, uint64, string)
		live  SourceCrawlClaimOutcome
		bound bool
	}
	fixtures := []attribution{
		{"bound", func(t *testing.T, sourceID uint64, label string) {
			sourceCrawlAttemptInsertOperation(t, db, sourceID, 7, label, "accepted",
				now.Add(-time.Minute), now)
		}, SourceCrawlClaimed, true},
		{"unattributed", func(*testing.T, uint64, string) {}, SourceCrawlUnattributed, false},
		{"ambiguous", func(t *testing.T, sourceID uint64, label string) {
			sourceCrawlAttemptInsertOperation(t, db, sourceID, 7, label+"-a", "accepted",
				now.Add(-time.Minute), now)
			sourceCrawlAttemptInsertOperation(t, db, sourceID, 7, label+"-b", "accepted",
				now.Add(-time.Minute), now)
		}, SourceCrawlAmbiguous, false},
		{"not-attributable", func(t *testing.T, sourceID uint64, label string) {
			sourceCrawlAttemptInsertOperation(t, db, sourceID, 7, label, "accepted",
				now.Add(-time.Minute), now)
			if _, err := adminDB.Exec(`UPDATE Sources SET usr_id=8 WHERE source_id=$1`, sourceID); err != nil {
				t.Fatalf("transfer freshness source: %v", err)
			}
			if _, err := adminDB.Exec(`UPDATE SourceOwnership SET user_id=8 WHERE source_id=$1`, sourceID); err != nil {
				t.Fatalf("transfer freshness ownership: %v", err)
			}
		}, SourceCrawlNotAttributable, false},
	}

	boundaries := []struct {
		name      string
		final     time.Time
		firstLive bool
	}{
		{"just before lease expiry", now.Add(lease - time.Microsecond), true},
		{"exactly at lease expiry", now.Add(lease), false},
		{"after lease expiry", now.Add(lease + time.Microsecond), false},
	}

	for _, fx := range fixtures {
		fx := fx
		for _, boundary := range boundaries {
			boundary := boundary
			t.Run(fx.name+"/"+boundary.name, func(t *testing.T) {
				priority := fmt.Sprintf("wp05b3c3b1s-%s-%d", fx.name, time.Now().UnixNano())
				sourceA := createCrawlSchedulingSource(t, db, priority)
				sourceB := createCrawlSchedulingSource(t, db, priority)
				t.Cleanup(func() {
					cleanupSourceCrawlAttemptRows(t, adminDB, sourceA)
					cleanupSourceCrawlAttemptRows(t, adminDB, sourceB)
				})
				setSourceCrawlAttemptMode(t, adminDB, "fenced")
				fx.setup(t, sourceA, fmt.Sprintf("freshness-%s-a", fx.name))
				fx.setup(t, sourceB, fmt.Sprintf("freshness-%s-b", fx.name))

				// Source A is persisted at now, source B 30 seconds later.
				// The final timestamp decides whether A expired during the
				// batch; B's longer remaining lease must stay live.
				times := []time.Time{now, now, now.Add(30 * time.Second), boundary.final}
				calls := 0
				clock := func(context.Context, *sql.Tx, Handler) (time.Time, error) {
					if calls >= len(times) {
						return times[len(times)-1], nil
					}
					value := times[calls]
					calls++
					return value, nil
				}
				request := sourceCrawlAttemptRequestFor(
					"pg-freshness-"+fx.name+"-"+boundary.name, priority)
				request.Limit = 2
				request.Lease = lease
				claims, err := claimSourceCrawlAttemptsWithClock(context.Background(), db, request, clock)
				if err != nil || len(claims) != 2 {
					t.Fatalf("final freshness claim = %+v err=%v", claims, err)
				}
				if calls != 4 {
					t.Fatalf("clock calls = %d, want 4", calls)
				}
				wantFirst := fx.live
				if !boundary.firstLive {
					wantFirst = SourceCrawlClaimStaleFence
				}
				if claims[0].SourceID != sourceA || claims[0].Outcome != wantFirst || claims[0].Reused {
					t.Fatalf("first claim = %+v, want source %d outcome %s", claims[0], sourceA, wantFirst)
				}
				if claims[1].SourceID != sourceB || claims[1].Outcome != fx.live || claims[1].Reused {
					t.Fatalf("second claim = %+v, want live source %d outcome %s", claims[1], sourceB, fx.live)
				}
				if claims[0].AttemptID == claims[1].AttemptID {
					t.Fatal("attempt IDs must be unique per source")
				}
				for _, claim := range claims {
					var (
						currentAttempt, currentRequest string
						currentGeneration              uint64
						currentLease                   time.Time
						historyAttempt, historyEngine  string
						historyOperation               sql.NullString
						historyLease                   time.Time
					)
					if err := sqlDB.QueryRow(`SELECT attempt_id, claim_request_id, generation, lease_expires_at
						FROM SourceCrawlClaims WHERE source_id=$1`, claim.SourceID).Scan(
						&currentAttempt, &currentRequest, &currentGeneration,
						&currentLease); err != nil {
						t.Fatalf("read current claim for %d: %v", claim.SourceID, err)
					}
					if err := sqlDB.QueryRow(`SELECT attempt_id, engine_id, operation_id, lease_expires_at
						FROM SourceCrawlAttempts WHERE source_id=$1`, claim.SourceID).Scan(
						&historyAttempt, &historyEngine, &historyOperation,
						&historyLease); err != nil {
						t.Fatalf("read attempt history for %d: %v", claim.SourceID, err)
					}
					if currentAttempt != claim.AttemptID || historyAttempt != claim.AttemptID ||
						currentRequest != claim.ClaimRequestID || currentGeneration != claim.Generation ||
						historyEngine != claim.EngineID {
						t.Fatalf("current/history identity disagree for %d: %+v", claim.SourceID, claim)
					}
					if !currentLease.Equal(historyLease) || !currentLease.Equal(claim.LeaseExpiresAt) {
						t.Fatalf("lease mismatch for %d: current=%v history=%v returned=%v",
							claim.SourceID, currentLease, historyLease, claim.LeaseExpiresAt)
					}
					if fx.bound {
						if claim.OperationID == "" || !historyOperation.Valid ||
							historyOperation.String != claim.OperationID {
							t.Fatalf("bound attempt lost its operation binding: %+v", claim)
						}
					} else if claim.OperationID != "" || historyOperation.Valid {
						t.Fatalf("unbound attempt fabricated operation identity: %+v", claim)
					}
					var historyCount int
					if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM SourceCrawlAttempts WHERE source_id=$1`,
						claim.SourceID).Scan(&historyCount); err != nil {
						t.Fatalf("count attempt history for %d: %v", claim.SourceID, err)
					}
					if historyCount != 1 {
						t.Fatalf("history count for %d = %d, want 1", claim.SourceID, historyCount)
					}
				}

				// Replaying after every lease expired must reuse the stored
				// attempts and return stale fences without a new generation.
				replayNow := now.Add(2 * lease)
				replayClock := func(context.Context, *sql.Tx, Handler) (time.Time, error) {
					return replayNow, nil
				}
				replay, err := claimSourceCrawlAttemptsWithClock(context.Background(), db, request, replayClock)
				if err != nil || len(replay) != 2 {
					t.Fatalf("expired replay = %+v err=%v", replay, err)
				}
				for i := range replay {
					if !replay[i].Reused || replay[i].Outcome != SourceCrawlClaimStaleFence ||
						replay[i].AttemptID != claims[i].AttemptID ||
						replay[i].Generation != claims[i].Generation {
						t.Fatalf("expired replay[%d] = %+v, want stale reuse of %+v", i, replay[i], claims[i])
					}
				}
			})
		}
	}
}

func TestPostgresSourceCrawlAttemptCandidateLoadFailure(t *testing.T) {
	db, _ := openPostgresIntegrationTestDB(t)
	adminDB := openAsyncGoalPostgresAdminDB(t)
	priority := fmt.Sprintf("wp05b3c3a-load-failure-%d", time.Now().UnixNano())
	sourceID := createCrawlSchedulingSource(t, db, priority)
	role := sourceCrawlAttemptRuntimeRole(t)
	t.Cleanup(func() {
		_, _ = adminDB.Exec(`GRANT SELECT ON CrawlProposals TO ` + pq.QuoteIdentifier(role))
		cleanupSourceCrawlAttemptRows(t, adminDB, sourceID)
	})
	if _, err := adminDB.Exec(`REVOKE SELECT ON CrawlProposals FROM ` + pq.QuoteIdentifier(role)); err != nil {
		t.Fatalf("revoke proposal read: %v", err)
	}
	setSourceCrawlAttemptMode(t, adminDB, "fenced")
	claims, err := ClaimSourceCrawlAttempts(context.Background(), db,
		sourceCrawlAttemptRequestFor("pg-load-failure", priority))
	if err == nil || len(claims) != 0 {
		t.Fatalf("candidate load failure = %+v err=%v", claims, err)
	}
	var attempts int
	if err := adminDB.QueryRow(`SELECT COUNT(*) FROM SourceCrawlAttempts WHERE source_id=$1`, sourceID).Scan(&attempts); err != nil {
		t.Fatalf("count attempts after load failure: %v", err)
	}
	if attempts != 0 {
		t.Fatalf("attempts after load failure = %d, want 0", attempts)
	}
	var status string
	if err := adminDB.QueryRow(`SELECT status FROM Sources WHERE source_id=$1`, sourceID).Scan(&status); err != nil {
		t.Fatalf("read source status after load failure: %v", err)
	}
	if status != "new" {
		t.Fatalf("source status after load failure = %q, want new", status)
	}
}

func TestPostgresSourceCrawlAttemptDatabaseClockReconciliation(t *testing.T) {
	db, _ := openPostgresIntegrationTestDB(t)
	adminDB := openAsyncGoalPostgresAdminDB(t)
	priority := fmt.Sprintf("wp05b3c3a-db-clock-%d", time.Now().UnixNano())
	sourceID := createCrawlSchedulingSource(t, db, priority)
	t.Cleanup(func() { cleanupSourceCrawlAttemptRows(t, adminDB, sourceID) })
	setSourceCrawlAttemptMode(t, adminDB, "fenced")
	now := time.Now().UTC().Truncate(time.Microsecond)
	sourceCrawlAttemptInsertOperation(t, db, sourceID, 7, "db-clock", "accepted", now.Add(-time.Minute), now)
	request := sourceCrawlAttemptRequestFor("pg-db-clock", priority)
	claims, err := claimSourceCrawlAttemptsAt(context.Background(), db, request, now)
	if err != nil || len(claims) != 1 || claims[0].Outcome != SourceCrawlClaimed {
		t.Fatalf("base database-clock claim = %+v err=%v", claims, err)
	}
	loaded, found, err := loadSourceCrawlClaimRequest(context.Background(), *db, request.ClaimRequestID)
	if err != nil || !found || len(loaded) != 1 || !loaded[0].Reused ||
		loaded[0].Outcome != SourceCrawlClaimed || loaded[0].AttemptID != claims[0].AttemptID {
		t.Fatalf("database-clock reconciliation = %+v found=%v err=%v", loaded, found, err)
	}
	if !sameSourceCrawlClaims(loaded, claims) {
		t.Fatalf("database-clock reconciliation identity differs")
	}
	if _, err := adminDB.Exec(`UPDATE SourceCrawlAttempts SET claimed_at=$1, lease_expires_at=$2 WHERE source_id=$3`,
		now.Add(-2*time.Minute), now.Add(-time.Minute), sourceID); err != nil {
		t.Fatalf("expire attempt lease: %v", err)
	}
	expired, found, err := loadSourceCrawlClaimRequest(context.Background(), *db, request.ClaimRequestID)
	if err != nil || !found || len(expired) != 1 || expired[0].Outcome != SourceCrawlClaimStaleFence {
		t.Fatalf("expired database-clock reconciliation = %+v found=%v err=%v", expired, found, err)
	}
}

func TestPostgresSourceCrawlAttemptPostLockClock(t *testing.T) {
	db, _ := openPostgresIntegrationTestDB(t)
	adminDB := openAsyncGoalPostgresAdminDB(t)
	priority := fmt.Sprintf("wp05b3c3b1-clock-%d", time.Now().UnixNano())
	sourceID := createCrawlSchedulingSource(t, db, priority)
	t.Cleanup(func() { cleanupSourceCrawlAttemptRows(t, adminDB, sourceID) })
	setSourceCrawlAttemptMode(t, adminDB, "fenced")

	firstRequest := sourceCrawlAttemptRequestFor("pg-clock-first", priority)
	firstRequest.Lease = 2 * time.Second
	first, err := ClaimSourceCrawlAttempts(context.Background(), db, firstRequest)
	if err != nil || len(first) != 1 || first[0].SourceID != sourceID {
		t.Fatalf("short-lease base claim = %+v err=%v", first, err)
	}
	var liveBeforeWait bool
	if err := adminDB.QueryRow(`SELECT clock_timestamp() < lease_expires_at
		FROM SourceCrawlClaims WHERE source_id=$1`, sourceID).Scan(&liveBeforeWait); err != nil {
		t.Fatalf("read first lease: %v", err)
	}
	if !liveBeforeWait {
		t.Fatalf("fixture lease expired before lock wait: %+v", first[0])
	}

	lockTx, err := adminDB.Begin()
	if err != nil {
		t.Fatalf("begin exclusive policy transaction: %v", err)
	}
	if _, err := lockTx.Exec(`SELECT pg_advisory_xact_lock(hashtext('crowler:source-crawl-claim-policy'))`); err != nil {
		_ = lockTx.Rollback()
		t.Fatalf("acquire exclusive policy lock: %v", err)
	}
	if err := lockTx.QueryRow(`SELECT clock_timestamp() < lease_expires_at
		FROM SourceCrawlClaims WHERE source_id=$1`, sourceID).Scan(&liveBeforeWait); err != nil {
		_ = lockTx.Rollback()
		t.Fatalf("re-read first lease before lock wait: %v", err)
	}
	if !liveBeforeWait {
		_ = lockTx.Rollback()
		t.Fatal("fixture lease expired before the waiting claim started")
	}

	type claimResult struct {
		claims []SourceCrawlClaim
		err    error
	}
	done := make(chan claimResult, 1)
	go func() {
		claims, err := ClaimSourceCrawlAttempts(context.Background(), db,
			sourceCrawlAttemptRequestFor("pg-clock-second", priority))
		done <- claimResult{claims: claims, err: err}
	}()

	barrierDeadline := time.Now().Add(5 * time.Second)
	for {
		var waiters int
		if err := adminDB.QueryRow(`SELECT COUNT(*) FROM pg_locks
			WHERE locktype='advisory' AND NOT granted`).Scan(&waiters); err != nil {
			_ = lockTx.Rollback()
			t.Fatalf("inspect advisory lock waiters: %v", err)
		}
		if waiters > 0 {
			break
		}
		if time.Now().After(barrierDeadline) {
			_ = lockTx.Rollback()
			t.Fatal("claim transaction did not block on the policy lock")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Hold the exclusive lock beyond the first lease. The waiting claim must
	// evaluate a fresh database clock after it acquires the shared lock; a
	// pre-lock timestamp would still see the old lease as live.
	if _, err := lockTx.Exec(`SELECT pg_sleep(2.3)`); err != nil {
		_ = lockTx.Rollback()
		t.Fatalf("hold policy lock: %v", err)
	}
	if err := lockTx.Commit(); err != nil {
		t.Fatalf("release policy lock: %v", err)
	}
	result := <-done
	if result.err != nil || len(result.claims) != 1 ||
		result.claims[0].SourceID != sourceID || result.claims[0].Generation != 2 {
		t.Fatalf("post-lock clock claim = %+v err=%v", result.claims, result.err)
	}
	var firstState, secondState string
	if err := adminDB.QueryRow(`SELECT state FROM SourceCrawlAttempts WHERE attempt_id=$1`,
		first[0].AttemptID).Scan(&firstState); err != nil {
		t.Fatalf("read superseded attempt state: %v", err)
	}
	if err := adminDB.QueryRow(`SELECT state FROM SourceCrawlAttempts WHERE attempt_id=$1`,
		result.claims[0].AttemptID).Scan(&secondState); err != nil {
		t.Fatalf("read new attempt state: %v", err)
	}
	if firstState != "superseded" || secondState != "claimed" {
		t.Fatalf("attempt states after lock wait = first=%q second=%q", firstState, secondState)
	}
}
