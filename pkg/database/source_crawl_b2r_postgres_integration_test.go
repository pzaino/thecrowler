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

// WP-05B3C3B2R PostgreSQL integration coverage for the fenced execution-safety
// work. Every test uses the disposable database created by the existing
// integration harness; no shared database is contacted.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"
)

func sourceCrawlB2RPostgresUnboundClaim(t *testing.T, db *Handler, adminDB *sql.DB,
	priority, label string) (uint64, SourceCrawlClaim, sourceCrawlTerminalReceipt) {
	t.Helper()
	sourceID := createCrawlSchedulingSource(t, db, priority)
	t.Cleanup(func() { cleanupSourceCrawlAttemptRows(t, adminDB, sourceID) })
	setSourceCrawlAttemptMode(t, adminDB, "fenced")
	now := time.Now().UTC().Truncate(time.Microsecond)
	request := sourceCrawlAttemptRequestFor("pg-b2r-"+label, priority)
	request.Limit = 1
	claims, err := claimSourceCrawlAttemptsAt(context.Background(), db, request, now)
	if err != nil || len(claims) != 1 || claims[0].Outcome != SourceCrawlUnattributed {
		t.Fatalf("b2r claim %s = %+v err=%v", label, claims, err)
	}
	claim := claims[0]
	receipt := sourceCrawlTerminalReceipt{
		Attempt: sourceCrawlAttempt{
			AttemptID: claim.AttemptID, ClaimRequestID: claim.ClaimRequestID,
			SourceID: claim.SourceID, Generation: claim.Generation, EngineID: claim.EngineID,
			State: sourceCrawlAttemptClaimed, ClaimedAt: claim.ClaimedAt,
			LeaseExpiresAt: claim.LeaseExpiresAt,
		},
		Attribution: sourceCrawlHandoffUnattributed,
		Outcome:     sourceCrawlTerminalCompletedOutcome,
		ReasonCode:  "crawl_completed",
		ObservedAt:  claim.ClaimedAt.Add(time.Second),
	}
	return sourceID, claim, receipt
}

func TestPostgresSourceCrawlB2RFenceBoundary(t *testing.T) {
	db, _ := openPostgresIntegrationTestDB(t)
	adminDB := openAsyncGoalPostgresAdminDB(t)
	priority := fmt.Sprintf("wp05b3c3b2r-fence-%d", time.Now().UnixNano())
	sourceID, claim, _ := sourceCrawlB2RPostgresUnboundClaim(t, db, adminDB, priority, "boundary")
	handoff, err := NewSourceCrawlHandoff(claim)
	if err != nil {
		t.Fatalf("handoff: %v", err)
	}
	cases := []struct {
		name string
		at   time.Time
		want SourceCrawlFenceResult
	}{
		{"before expiry", claim.LeaseExpiresAt.Add(-time.Microsecond), SourceCrawlFenceActive},
		{"exactly at expiry", claim.LeaseExpiresAt, SourceCrawlFenceStale},
		{"after expiry", claim.LeaseExpiresAt.Add(time.Second), SourceCrawlFenceStale},
	}
	for _, tc := range cases {
		status, err := handoff.validateCurrentAt(context.Background(), db, tc.at)
		if status.Result != tc.want {
			t.Fatalf("%s validation = %+v err=%v", tc.name, status, err)
		}
	}
	if _, err := adminDB.Exec(`UPDATE Sources SET disabled=TRUE WHERE source_id=$1`,
		sourceID); err != nil {
		t.Fatalf("disable source: %v", err)
	}
	status, err := handoff.validateCurrentAt(context.Background(), db,
		claim.ClaimedAt.Add(time.Second))
	if status.Result != SourceCrawlFenceStale || status.Reason != "source_disabled" {
		t.Fatalf("disabled validation = %+v err=%v", status, err)
	}
}

func TestPostgresSourceCrawlB2RValidateTxSupersession(t *testing.T) {
	db, _ := openPostgresIntegrationTestDB(t)
	adminDB := openAsyncGoalPostgresAdminDB(t)
	priority := fmt.Sprintf("wp05b3c3b2r-tx-%d", time.Now().UnixNano())
	sourceID, claim, _ := sourceCrawlB2RPostgresUnboundClaim(t, db, adminDB, priority, "tx")
	handoff, err := NewSourceCrawlHandoff(claim)
	if err != nil {
		t.Fatalf("handoff: %v", err)
	}
	tx, err := (*db).BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	if err := handoff.validateTxAt(context.Background(), tx, db,
		claim.ClaimedAt.Add(time.Second)); err != nil {
		_ = tx.Rollback()
		t.Fatalf("active ValidateTx = %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback active tx: %v", err)
	}
	if _, err := adminDB.Exec(`UPDATE SourceCrawlAttempts
		SET state='superseded', superseded_by_generation=generation+1, terminal_at=NOW()
		WHERE source_id=$1`, sourceID); err != nil {
		t.Fatalf("supersede attempt: %v", err)
	}
	tx, err = (*db).BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("second begin: %v", err)
	}
	defer tx.Rollback() //nolint:errcheck
	if err := handoff.validateTxAt(context.Background(), tx, db,
		claim.ClaimedAt.Add(time.Second)); !errors.Is(err, errSourceCrawlFenceStale) {
		t.Fatalf("superseded ValidateTx = %v, want stale fence", err)
	}
}

func TestPostgresSourceCrawlB2RUnboundOwnershipTransfer(t *testing.T) {
	db, _ := openPostgresIntegrationTestDB(t)
	adminDB := openAsyncGoalPostgresAdminDB(t)
	priority := fmt.Sprintf("wp05b3c3b2r-owner-%d", time.Now().UnixNano())
	sourceID, claim, receipt := sourceCrawlB2RPostgresUnboundClaim(t, db, adminDB, priority, "owner")
	if _, err := adminDB.Exec(`UPDATE Sources SET usr_id=8 WHERE source_id=$1`,
		sourceID); err != nil {
		t.Fatalf("transfer source: %v", err)
	}
	if _, err := adminDB.Exec(`UPDATE SourceOwnership
		SET user_id=8, verified_at=NOW(), verified_by_user_id=9,
		    verification_method='transfer', last_updated_at=NOW()
		WHERE source_id=$1`, sourceID); err != nil {
		t.Fatalf("transfer provenance: %v", err)
	}
	result, err := reportSourceCrawlAttemptTerminalAt(context.Background(), db,
		receipt, claim.ClaimedAt.Add(2*time.Second))
	if err != nil || result.Result != sourceCrawlTerminalApplied ||
		result.SourceStatusChanged {
		t.Fatalf("transferred terminal = %+v err=%v", result, err)
	}
	var attemptState, sourceState string
	if err := adminDB.QueryRow(`SELECT state FROM SourceCrawlAttempts WHERE source_id=$1`,
		sourceID).Scan(&attemptState); err != nil {
		t.Fatalf("read attempt: %v", err)
	}
	if err := adminDB.QueryRow(`SELECT status FROM Sources WHERE source_id=$1`,
		sourceID).Scan(&sourceState); err != nil {
		t.Fatalf("read source: %v", err)
	}
	if attemptState != "completed" || sourceState != "processing" {
		t.Fatalf("transferred states = %s/%s", attemptState, sourceState)
	}
}

func TestPostgresSourceCrawlB2RContinuityThenTransfer(t *testing.T) {
	db, _ := openPostgresIntegrationTestDB(t)
	adminDB := openAsyncGoalPostgresAdminDB(t)
	priority := fmt.Sprintf("wp05b3c3b2r-continuity-%d", time.Now().UnixNano())
	sourceID, claim, receipt := sourceCrawlB2RPostgresUnboundClaim(t, db, adminDB, priority, "continuity")
	result, err := reportSourceCrawlAttemptTerminalAt(context.Background(), db,
		receipt, claim.ClaimedAt.Add(2*time.Second))
	if err != nil || result.Result != sourceCrawlTerminalApplied ||
		!result.SourceStatusChanged {
		t.Fatalf("continuity terminal = %+v err=%v", result, err)
	}
	if _, err := adminDB.Exec(`UPDATE Sources SET usr_id=8 WHERE source_id=$1`,
		sourceID); err != nil {
		t.Fatalf("later transfer: %v", err)
	}
	if _, err := adminDB.Exec(`UPDATE SourceOwnership
		SET user_id=8, verified_at=NOW(), last_updated_at=NOW() WHERE source_id=$1`,
		sourceID); err != nil {
		t.Fatalf("later transfer provenance: %v", err)
	}
	// A late retry is a read-only replay and must not rewrite the completed
	// source status.
	replay, err := reportSourceCrawlAttemptTerminalAt(context.Background(), db,
		receipt, claim.LeaseExpiresAt.Add(time.Hour))
	if err != nil || replay.Result != sourceCrawlTerminalIdempotentReplay {
		t.Fatalf("post-transfer replay = %+v err=%v", replay, err)
	}
	var sourceOwner uint64
	var sourceState string
	if err := adminDB.QueryRow(`SELECT usr_id, status FROM Sources WHERE source_id=$1`,
		sourceID).Scan(&sourceOwner, &sourceState); err != nil {
		t.Fatalf("read source: %v", err)
	}
	if sourceOwner != 8 || sourceState != "completed" {
		t.Fatalf("post-transfer source = owner=%d status=%s", sourceOwner, sourceState)
	}
}
