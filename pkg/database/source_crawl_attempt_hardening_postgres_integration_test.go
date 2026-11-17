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
