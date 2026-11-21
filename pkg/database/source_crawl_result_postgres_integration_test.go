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
	"fmt"
	"sync"
	"testing"
	"time"
)

func sourceCrawlPostgresResultClaim(t *testing.T, db *Handler, adminDB *sql.DB,
	priority, label string) (uint64, SourceCrawlClaim, sourceCrawlTerminalReceipt,
	SourceCrawlClaimRequest) {
	t.Helper()
	sourceID := createCrawlSchedulingSource(t, db, priority)
	t.Cleanup(func() { cleanupSourceCrawlAttemptRows(t, adminDB, sourceID) })
	setSourceCrawlAttemptMode(t, adminDB, "fenced")
	now := time.Now().UTC().Truncate(time.Microsecond)
	sourceCrawlAttemptInsertOperation(t, db, sourceID, 7, label, "accepted",
		now.Add(-time.Minute), now)
	request := sourceCrawlAttemptRequestFor("pg-result-"+label, priority)
	request.Limit = 1
	claims, err := claimSourceCrawlAttemptsAt(context.Background(), db, request, now)
	if err != nil || len(claims) != 1 || claims[0].Outcome != SourceCrawlClaimed {
		t.Fatalf("result claim %s = %+v err=%v", label, claims, err)
	}
	claim := claims[0]
	binding := &sourceCrawlOperationBinding{
		OperationID: claim.OperationID, RequestID: claim.RequestID,
		RunID: claim.RunID, AgentID: claim.AgentID, Owner: claim.Owner,
		PrincipalUserID: claim.PrincipalUserID, ApprovalID: claim.ApprovalID,
		ApprovalSHA256: claim.ApprovalSHA256,
	}
	attempt := sourceCrawlAttempt{
		AttemptID: claim.AttemptID, ClaimRequestID: claim.ClaimRequestID,
		SourceID: claim.SourceID, Generation: claim.Generation, EngineID: claim.EngineID,
		State: sourceCrawlAttemptClaimed, ClaimedAt: claim.ClaimedAt,
		LeaseExpiresAt: claim.LeaseExpiresAt, Binding: binding,
	}
	receipt := sourceCrawlTerminalReceipt{
		Attempt: attempt, Attribution: sourceCrawlHandoffBound,
		Outcome: sourceCrawlTerminalCompletedOutcome, ReasonCode: "crawl_completed",
		ObservedAt: claim.ClaimedAt.Add(time.Second),
		OperationResult: &sourceCrawlTerminalOperationResult{
			Binding: *binding, ExpectedState: CrawlOperationAccepted,
			ResultState: CrawlOperationCompleted,
		},
	}
	return sourceID, claim, receipt, request
}

func sourceCrawlPostgresResultStates(t *testing.T, adminDB *sql.DB, sourceID uint64) (string, string, string) {
	t.Helper()
	var attemptState, operationState, sourceState string
	if err := adminDB.QueryRow(`SELECT state FROM SourceCrawlAttempts WHERE source_id=$1`,
		sourceID).Scan(&attemptState); err != nil {
		t.Fatalf("read PostgreSQL attempt state: %v", err)
	}
	if err := adminDB.QueryRow(`SELECT status FROM Status WHERE source_id=$1`,
		sourceID).Scan(&operationState); err != nil {
		t.Fatalf("read PostgreSQL operation state: %v", err)
	}
	if err := adminDB.QueryRow(`SELECT status FROM Sources WHERE source_id=$1`,
		sourceID).Scan(&sourceState); err != nil {
		t.Fatalf("read PostgreSQL source state: %v", err)
	}
	return attemptState, operationState, sourceState
}

func TestPostgresSourceCrawlResultLifecycle(t *testing.T) {
	db, _ := openPostgresIntegrationTestDB(t)
	adminDB := openAsyncGoalPostgresAdminDB(t)
	priority := fmt.Sprintf("wp05b3c3b1-pg-lifecycle-%d", time.Now().UnixNano())
	sourceID, claim, receipt, _ := sourceCrawlPostgresResultClaim(t, db, adminDB,
		priority, "pg-result-lifecycle")

	result, err := reportSourceCrawlAttemptTerminalAt(context.Background(), db, receipt,
		claim.ClaimedAt.Add(2*time.Second))
	if err != nil || result.Result != sourceCrawlTerminalApplied || !result.SourceStatusChanged {
		t.Fatalf("PostgreSQL terminal = %+v err=%v", result, err)
	}
	attemptState, operationState, sourceState := sourceCrawlPostgresResultStates(t, adminDB, sourceID)
	if attemptState != "completed" || operationState != "completed" || sourceState != "completed" {
		t.Fatalf("PostgreSQL states = %s/%s/%s", attemptState, operationState, sourceState)
	}
	replay, err := reportSourceCrawlAttemptTerminalAt(context.Background(), db, receipt,
		claim.LeaseExpiresAt.Add(time.Hour))
	if err != nil || replay.Result != sourceCrawlTerminalIdempotentReplay {
		t.Fatalf("PostgreSQL replay = %+v err=%v", replay, err)
	}
	if second := openSourceCrawlAttemptPostgresSecondHandler(t, db); second != nil {
		restarted, err := reportSourceCrawlAttemptTerminalAt(context.Background(), second, receipt,
			claim.LeaseExpiresAt.Add(time.Hour))
		if err != nil || restarted.Result != sourceCrawlTerminalIdempotentReplay {
			t.Fatalf("PostgreSQL restart replay = %+v err=%v", restarted, err)
		}
	}
	conflict := sourceCrawlResultOutcome(t, receipt, sourceCrawlTerminalFailedOutcome)
	if got, err := reportSourceCrawlAttemptTerminalAt(context.Background(), db, conflict,
		claim.LeaseExpiresAt.Add(time.Hour)); err != nil ||
		got.Result != sourceCrawlTerminalConflict {
		t.Fatalf("PostgreSQL conflict = %+v err=%v", got, err)
	}
}

func TestPostgresSourceCrawlResultHeartbeat(t *testing.T) {
	db, _ := openPostgresIntegrationTestDB(t)
	adminDB := openAsyncGoalPostgresAdminDB(t)
	priority := fmt.Sprintf("wp05b3c3b1-pg-heartbeat-%d", time.Now().UnixNano())
	_, claim, _, claimRequest := sourceCrawlPostgresResultClaim(t, db, adminDB, priority, "pg-result-heartbeat")
	request := sourceCrawlLeaseRenewalRequest{
		SourceID: claim.SourceID, AttemptID: claim.AttemptID,
		Generation: claim.Generation, ClaimRequestID: claim.ClaimRequestID,
		EngineID: claim.EngineID, Extension: time.Minute,
	}
	now := claim.ClaimedAt.Add(time.Second)
	result, err := renewSourceCrawlAttemptLeaseAt(context.Background(), db, request, now)
	if err != nil || result.Result != sourceCrawlTerminalApplied {
		t.Fatalf("PostgreSQL heartbeat = %+v err=%v", result, err)
	}
	wantLease := claim.LeaseExpiresAt.Add(time.Minute)
	var currentLease, historyLease time.Time
	if err := adminDB.QueryRow(`SELECT lease_expires_at FROM SourceCrawlClaims WHERE source_id=$1`,
		claim.SourceID).Scan(&currentLease); err != nil {
		t.Fatalf("read PostgreSQL current lease: %v", err)
	}
	if err := adminDB.QueryRow(`SELECT lease_expires_at FROM SourceCrawlAttempts WHERE source_id=$1`,
		claim.SourceID).Scan(&historyLease); err != nil {
		t.Fatalf("read PostgreSQL history lease: %v", err)
	}
	if !currentLease.Equal(wantLease) || !historyLease.Equal(wantLease) {
		t.Fatalf("PostgreSQL leases current=%v history=%v want=%v", currentLease, historyLease, wantLease)
	}
	if _, err := claimSourceCrawlAttemptsAt(context.Background(), db,
		claimRequest, now.Add(time.Minute)); err == nil {
		t.Fatal("PostgreSQL claim request replay after heartbeat succeeded")
	}
	over, err := renewSourceCrawlAttemptLeaseAt(context.Background(), db,
		sourceCrawlLeaseRenewalRequest{
			SourceID: claim.SourceID, AttemptID: claim.AttemptID,
			Generation: claim.Generation, ClaimRequestID: claim.ClaimRequestID,
			EngineID: claim.EngineID, Extension: 24 * time.Hour,
		}, now)
	if err != nil || over.Result != sourceCrawlTerminalConflict {
		t.Fatalf("PostgreSQL 24h heartbeat = %+v err=%v", over, err)
	}
	if got, err := renewSourceCrawlAttemptLeaseAt(context.Background(), db, request, wantLease); err != nil ||
		got.Result != sourceCrawlTerminalStaleFence {
		t.Fatalf("PostgreSQL expired heartbeat = %+v err=%v", got, err)
	}
}

func TestPostgresSourceCrawlResultConcurrency(t *testing.T) {
	db, _ := openPostgresIntegrationTestDB(t)
	adminDB := openAsyncGoalPostgresAdminDB(t)
	priority := fmt.Sprintf("wp05b3c3b1-pg-concurrent-%d", time.Now().UnixNano())
	_, claim, receipt, _ := sourceCrawlPostgresResultClaim(t, db, adminDB, priority, "pg-result-concurrent")
	now := claim.ClaimedAt.Add(2 * time.Second)

	type outcome struct {
		result sourceCrawlTerminalExecutionResult
		err    error
	}
	results := make(chan outcome, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := reportSourceCrawlAttemptTerminalAt(context.Background(), db, receipt, now)
			results <- outcome{result: result, err: err}
		}()
	}
	wg.Wait()
	close(results)
	applied, replay := 0, 0
	for item := range results {
		if item.err != nil {
			t.Fatalf("concurrent terminal err=%v", item.err)
		}
		switch item.result.Result {
		case sourceCrawlTerminalApplied:
			applied++
		case sourceCrawlTerminalIdempotentReplay:
			replay++
		default:
			t.Fatalf("concurrent terminal = %+v", item.result)
		}
	}
	if applied != 1 || replay != 1 {
		t.Fatalf("concurrent terminal applied=%d replay=%d", applied, replay)
	}

	// Conflicting receipts: only one may commit.
	secondPriority := fmt.Sprintf("wp05b3c3b1-pg-conflict-%d", time.Now().UnixNano())
	_, secondClaim, secondReceipt, _ := sourceCrawlPostgresResultClaim(t, db, adminDB,
		secondPriority, "pg-result-conflict")
	failed := sourceCrawlResultOutcome(t, secondReceipt, sourceCrawlTerminalFailedOutcome)
	results = make(chan outcome, 2)
	for _, subject := range []sourceCrawlTerminalReceipt{secondReceipt, failed} {
		subject := subject
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := reportSourceCrawlAttemptTerminalAt(context.Background(), db, subject,
				secondClaim.ClaimedAt.Add(2*time.Second))
			results <- outcome{result: result, err: err}
		}()
	}
	wg.Wait()
	close(results)
	applied, conflict := 0, 0
	for item := range results {
		if item.err != nil {
			t.Fatalf("conflicting terminal err=%v", item.err)
		}
		switch item.result.Result {
		case sourceCrawlTerminalApplied:
			applied++
		case sourceCrawlTerminalConflict:
			conflict++
		default:
			t.Fatalf("conflicting terminal = %+v", item.result)
		}
	}
	if applied != 1 || conflict != 1 {
		t.Fatalf("conflicting terminal applied=%d conflict=%d", applied, conflict)
	}
}

func TestPostgresSourceCrawlResultRestrictedRole(t *testing.T) {
	db, _ := openPostgresIntegrationTestDB(t)
	adminDB := openAsyncGoalPostgresAdminDB(t)
	priority := fmt.Sprintf("wp05b3c3b1-pg-role-%d", time.Now().UnixNano())
	sourceID, _, _, _ := sourceCrawlPostgresResultClaim(t, db, adminDB, priority, "pg-result-role")
	if _, err := (*db).Exec(`UPDATE SourceCrawlClaimPolicy SET mode='legacy' WHERE policy_id=1`); err == nil {
		t.Fatal("runtime role changed the cutover policy")
	}
	if _, err := (*db).Exec(`SELECT set_source_crawl_claim_mode('legacy')`); err == nil {
		t.Fatal("runtime role executed the operator mode function")
	}
	if _, err := (*db).Exec(`DELETE FROM SourceCrawlAttempts WHERE source_id=$1`, sourceID); err == nil {
		t.Fatal("runtime role deleted attempt history")
	}
}

func TestPostgresSourceCrawlResultRollback(t *testing.T) {
	db, _ := openPostgresIntegrationTestDB(t)
	adminDB := openAsyncGoalPostgresAdminDB(t)
	priority := fmt.Sprintf("wp05b3c3b1-pg-rollback-%d", time.Now().UnixNano())
	sourceID, claim, receipt, _ := sourceCrawlPostgresResultClaim(t, db, adminDB,
		priority, "pg-result-rollback")
	if _, err := adminDB.Exec(`CREATE FUNCTION wp05b3c3b1_fail_status() RETURNS trigger AS $$
		BEGIN RAISE EXCEPTION 'injected status failure'; END; $$ LANGUAGE plpgsql`); err != nil {
		t.Fatalf("create status failure function: %v", err)
	}
	if _, err := adminDB.Exec(`CREATE TRIGGER wp05b3c3b1_fail_status BEFORE UPDATE ON Status
		FOR EACH ROW EXECUTE FUNCTION wp05b3c3b1_fail_status()`); err != nil {
		t.Fatalf("create status failure trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = adminDB.Exec(`DROP TRIGGER IF EXISTS wp05b3c3b1_fail_status ON Status`)
		_, _ = adminDB.Exec(`DROP FUNCTION IF EXISTS wp05b3c3b1_fail_status()`)
	})
	result, err := reportSourceCrawlAttemptTerminalAt(context.Background(), db, receipt,
		claim.ClaimedAt.Add(2*time.Second))
	if err == nil && result.Result == sourceCrawlTerminalApplied {
		t.Fatal("PostgreSQL terminal report unexpectedly succeeded")
	}
	attemptState, operationState, sourceState := sourceCrawlPostgresResultStates(t, adminDB, sourceID)
	if attemptState != "claimed" || operationState != "accepted" || sourceState != "processing" {
		t.Fatalf("PostgreSQL rollback left states: %s/%s/%s", attemptState, operationState, sourceState)
	}
}

func TestPostgresSourceCrawlResultReplayAfterReplacement(t *testing.T) {
	db, _ := openPostgresIntegrationTestDB(t)
	adminDB := openAsyncGoalPostgresAdminDB(t)
	priority := fmt.Sprintf("wp05b3c3b1-pg-replacement-%d", time.Now().UnixNano())
	sourceID, claim, receipt, _ := sourceCrawlPostgresResultClaim(t, db, adminDB,
		priority, "pg-result-replacement")
	if result, err := reportSourceCrawlAttemptTerminalAt(context.Background(), db, receipt,
		claim.ClaimedAt.Add(2*time.Second)); err != nil ||
		result.Result != sourceCrawlTerminalApplied {
		t.Fatalf("PostgreSQL initial terminal = %+v err=%v", result, err)
	}
	if _, err := adminDB.Exec(`UPDATE SourceCrawlClaims SET claimed_at=$1, lease_expires_at=$2
		WHERE source_id=$3`, claim.ClaimedAt.Add(-2*time.Second),
		claim.ClaimedAt.Add(-time.Second), sourceID); err != nil {
		t.Fatalf("expire PostgreSQL current claim: %v", err)
	}
	if _, err := adminDB.Exec(`UPDATE Sources SET status='pending' WHERE source_id=$1`,
		sourceID); err != nil {
		t.Fatalf("reset PostgreSQL source: %v", err)
	}
	request := sourceCrawlAttemptRequestFor("pg-result-replacement-second", priority)
	request.Limit = 1
	second, err := claimSourceCrawlAttemptsAt(context.Background(), db, request,
		claim.ClaimedAt.Add(time.Minute))
	if err != nil || len(second) != 1 || second[0].Generation != 2 {
		t.Fatalf("PostgreSQL second claim = %+v err=%v", second, err)
	}
	replay, err := reportSourceCrawlAttemptTerminalAt(context.Background(), db, receipt,
		claim.LeaseExpiresAt.Add(time.Hour))
	if err != nil || replay.Result != sourceCrawlTerminalIdempotentReplay {
		t.Fatalf("PostgreSQL replay after replacement = %+v err=%v", replay, err)
	}
	var generation uint64
	if err := adminDB.QueryRow(`SELECT generation FROM SourceCrawlClaims WHERE source_id=$1`,
		sourceID).Scan(&generation); err != nil {
		t.Fatalf("read PostgreSQL current generation: %v", err)
	}
	if generation != 2 {
		t.Fatalf("PostgreSQL replay changed current generation to %d", generation)
	}
}

func TestPostgresSourceCrawlResultConcurrentHeartbeat(t *testing.T) {
	db, _ := openPostgresIntegrationTestDB(t)
	adminDB := openAsyncGoalPostgresAdminDB(t)
	priority := fmt.Sprintf("wp05b3c3b1-pg-hb-concurrent-%d", time.Now().UnixNano())
	_, claim, _, _ := sourceCrawlPostgresResultClaim(t, db, adminDB, priority, "pg-result-hb-concurrent")
	request := sourceCrawlLeaseRenewalRequest{
		SourceID: claim.SourceID, AttemptID: claim.AttemptID,
		Generation: claim.Generation, ClaimRequestID: claim.ClaimRequestID,
		EngineID: claim.EngineID, Extension: time.Minute,
	}
	now := claim.ClaimedAt.Add(time.Second)
	type outcome struct {
		result sourceCrawlLeaseRenewalResult
		err    error
	}
	results := make(chan outcome, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := renewSourceCrawlAttemptLeaseAt(context.Background(), db, request, now)
			results <- outcome{result: result, err: err}
		}()
	}
	wg.Wait()
	close(results)
	for item := range results {
		if item.err != nil {
			t.Fatalf("concurrent heartbeat err=%v", item.err)
		}
		if item.result.Result != sourceCrawlTerminalApplied &&
			item.result.Result != sourceCrawlTerminalConflict {
			t.Fatalf("concurrent heartbeat = %+v", item.result)
		}
	}
	var currentLease, historyLease time.Time
	if err := adminDB.QueryRow(`SELECT lease_expires_at FROM SourceCrawlClaims WHERE source_id=$1`,
		claim.SourceID).Scan(&currentLease); err != nil {
		t.Fatalf("read concurrent current lease: %v", err)
	}
	if err := adminDB.QueryRow(`SELECT lease_expires_at FROM SourceCrawlAttempts WHERE source_id=$1`,
		claim.SourceID).Scan(&historyLease); err != nil {
		t.Fatalf("read concurrent history lease: %v", err)
	}
	if !currentLease.Equal(historyLease) {
		t.Fatalf("concurrent heartbeat lease mismatch: current=%v history=%v",
			currentLease, historyLease)
	}
	if currentLease.Before(claim.LeaseExpiresAt.Add(time.Minute)) ||
		currentLease.After(claim.LeaseExpiresAt.Add(2*time.Minute)) {
		t.Fatalf("concurrent heartbeat lease = %v", currentLease)
	}
}

func TestPostgresSourceCrawlResultLockTimeout(t *testing.T) {
	db, _ := openPostgresIntegrationTestDB(t)
	adminDB := openAsyncGoalPostgresAdminDB(t)
	priority := fmt.Sprintf("wp05b3c3b1-pg-lock-timeout-%d", time.Now().UnixNano())
	sourceID, claim, receipt, _ := sourceCrawlPostgresResultClaim(t, db, adminDB,
		priority, "pg-result-lock-timeout")
	second := openSourceCrawlAttemptPostgresSecondHandler(t, db)
	if second == nil {
		return
	}
	if _, err := (*second).Exec(`SET lock_timeout='200ms'`); err != nil {
		t.Fatalf("set lock timeout: %v", err)
	}
	holder, err := adminDB.Begin()
	if err != nil {
		t.Fatalf("begin lock holder: %v", err)
	}
	if _, err := holder.Exec(`SELECT 1 FROM Sources WHERE source_id=$1 FOR UPDATE`, sourceID); err != nil {
		_ = holder.Rollback()
		t.Fatalf("hold source lock: %v", err)
	}
	result, reportErr := reportSourceCrawlAttemptTerminalAt(context.Background(), second, receipt,
		claim.ClaimedAt.Add(2*time.Second))
	if reportErr == nil && result.Result == sourceCrawlTerminalApplied {
		_ = holder.Rollback()
		t.Fatal("locked terminal report unexpectedly succeeded")
	}
	if err := holder.Rollback(); err != nil {
		t.Fatalf("release source lock: %v", err)
	}
	attemptState, operationState, sourceState := sourceCrawlPostgresResultStates(t, adminDB, sourceID)
	if attemptState != "claimed" || operationState != "accepted" || sourceState != "processing" {
		t.Fatalf("lock timeout left states: %s/%s/%s", attemptState, operationState, sourceState)
	}
}
