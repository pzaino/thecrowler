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

// WP-05B3C3B2R negative and boundary coverage for the fenced execution
// safety work: authoritative fence validation, unbound owner continuity and
// operation-state compatibility.

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func sourceCrawlB2RReadAttemptSource(t *testing.T, raw *sql.DB,
	sourceID uint64) (string, string) {
	t.Helper()
	var attempt, source string
	if err := raw.QueryRow(`SELECT state FROM SourceCrawlAttempts WHERE source_id=?`,
		sourceID).Scan(&attempt); err != nil {
		t.Fatalf("read attempt: %v", err)
	}
	if err := raw.QueryRow(`SELECT status FROM Sources WHERE source_id=?`,
		sourceID).Scan(&source); err != nil {
		t.Fatalf("read source: %v", err)
	}
	return attempt, source
}

func TestSourceCrawlB2RFenceValidationSQLite(t *testing.T) {
	t.Run("active", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		claim, _ := sourceCrawlResultClaim(t, raw, &handler, 42, "fence-active", false)
		handoff, err := NewSourceCrawlHandoff(claim)
		if err != nil {
			t.Fatalf("handoff: %v", err)
		}
		status, err := handoff.validateCurrentAt(context.Background(), &handler,
			claim.ClaimedAt.Add(time.Minute))
		if err != nil || status.Result != SourceCrawlFenceActive {
			t.Fatalf("active fence = %+v err=%v", status, err)
		}
		if !status.LeaseExpiresAt.Equal(claim.LeaseExpiresAt) {
			t.Fatalf("active lease = %v want %v", status.LeaseExpiresAt, claim.LeaseExpiresAt)
		}
	})

	t.Run("just before expiry", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		claim, _ := sourceCrawlResultClaim(t, raw, &handler, 42, "fence-before", false)
		handoff, _ := NewSourceCrawlHandoff(claim)
		status, err := handoff.validateCurrentAt(context.Background(), &handler,
			claim.LeaseExpiresAt.Add(-time.Microsecond))
		if err != nil || status.Result != SourceCrawlFenceActive {
			t.Fatalf("pre-expiry fence = %+v err=%v", status, err)
		}
	})

	t.Run("exactly at expiry", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		claim, _ := sourceCrawlResultClaim(t, raw, &handler, 42, "fence-exact", false)
		handoff, _ := NewSourceCrawlHandoff(claim)
		status, err := handoff.validateCurrentAt(context.Background(), &handler,
			claim.LeaseExpiresAt)
		if err != nil || status.Result != SourceCrawlFenceStale || status.Reason != "lease_expired" {
			t.Fatalf("exact-expiry fence = %+v err=%v", status, err)
		}
	})

	t.Run("superseded or terminal history", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		claim, _ := sourceCrawlResultClaim(t, raw, &handler, 42, "fence-superseded", false)
		handoff, _ := NewSourceCrawlHandoff(claim)
		if _, err := raw.Exec(`UPDATE SourceCrawlAttempts
			SET state='superseded', superseded_by_generation=generation+1, terminal_at=?
			WHERE source_id=?`, claim.ClaimedAt.Add(time.Second), 42); err != nil {
			t.Fatalf("supersede attempt: %v", err)
		}
		status, err := handoff.validateCurrentAt(context.Background(), &handler,
			claim.ClaimedAt.Add(2*time.Second))
		if err != nil || status.Result != SourceCrawlFenceStale {
			t.Fatalf("superseded fence = %+v err=%v", status, err)
		}
	})

	t.Run("source disabled", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		claim, _ := sourceCrawlResultClaim(t, raw, &handler, 42, "fence-disabled", false)
		handoff, _ := NewSourceCrawlHandoff(claim)
		if _, err := raw.Exec(`UPDATE Sources SET disabled=TRUE WHERE source_id=42`); err != nil {
			t.Fatalf("disable source: %v", err)
		}
		status, err := handoff.validateCurrentAt(context.Background(), &handler,
			claim.ClaimedAt.Add(time.Second))
		if err != nil || status.Result != SourceCrawlFenceStale || status.Reason != "source_disabled" {
			t.Fatalf("disabled fence = %+v err=%v", status, err)
		}
	})

	t.Run("source deleted", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		claim, _ := sourceCrawlResultClaim(t, raw, &handler, 42, "fence-deleted", false)
		handoff, _ := NewSourceCrawlHandoff(claim)
		if _, err := raw.Exec(`UPDATE Sources SET deleted_at=CURRENT_TIMESTAMP WHERE source_id=42`); err != nil {
			t.Fatalf("delete source: %v", err)
		}
		status, err := handoff.validateCurrentAt(context.Background(), &handler,
			claim.ClaimedAt.Add(time.Second))
		if err != nil || status.Result != SourceCrawlFenceStale || status.Reason != "source_missing" {
			t.Fatalf("deleted fence = %+v err=%v", status, err)
		}
	})

	t.Run("legacy mode", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		claim, _ := sourceCrawlResultClaim(t, raw, &handler, 42, "fence-legacy", false)
		handoff, _ := NewSourceCrawlHandoff(claim)
		if _, err := raw.Exec(`UPDATE SourceCrawlClaimPolicy SET mode='legacy' WHERE policy_id=1`); err != nil {
			t.Fatalf("set legacy mode: %v", err)
		}
		status, err := handoff.validateCurrentAt(context.Background(), &handler,
			claim.ClaimedAt.Add(time.Second))
		if !errors.Is(err, ErrSourceCrawlClaimLegacyMode) ||
			status.Result != SourceCrawlFenceLegacy {
			t.Fatalf("legacy fence = %+v err=%v", status, err)
		}
	})

	t.Run("transaction-held validation", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		claim, _ := sourceCrawlResultClaim(t, raw, &handler, 42, "fence-tx", false)
		handoff, _ := NewSourceCrawlHandoff(claim)
		tx, err := raw.Begin()
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer tx.Rollback() //nolint:errcheck
		if err := handoff.validateTxAt(context.Background(), tx, &handler,
			claim.ClaimedAt.Add(time.Second)); err != nil {
			t.Fatalf("active ValidateTx = %v", err)
		}
		if err := handoff.validateTxAt(context.Background(), tx, &handler,
			claim.LeaseExpiresAt); !errors.Is(err, errSourceCrawlFenceStale) {
			t.Fatalf("expired ValidateTx = %v", err)
		}
	})

	t.Run("terminal handoff cannot renew or validate", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		claim, _ := sourceCrawlResultClaim(t, raw, &handler, 42, "fence-terminal", false)
		handoff, _ := NewSourceCrawlHandoff(claim)
		if _, err := handoff.reportTerminalAt(context.Background(), &handler,
			SourceCrawlOutcomeCompleted, claim.ClaimedAt.Add(time.Second),
			claim.ClaimedAt.Add(time.Second)); err != nil {
			t.Fatalf("terminal: %v", err)
		}
		status, err := handoff.ValidateCurrent(context.Background(), &handler)
		if err != nil || status.Result != SourceCrawlFenceStale ||
			status.Reason != "handoff_terminal" {
			t.Fatalf("terminal ValidateCurrent = %+v err=%v", status, err)
		}
	})
}

func TestSourceCrawlB2RUnboundSourceStatusSQLite(t *testing.T) {
	t.Run("unbound terminal suppresses source status", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		claim, receipt := sourceCrawlResultClaim(t, raw, &handler, 42, "unbound-suppress", false)
		result, err := reportSourceCrawlAttemptTerminalAt(context.Background(), &handler,
			receipt, claim.ClaimedAt.Add(time.Second))
		if err != nil || result.Result != sourceCrawlTerminalApplied ||
			result.SourceStatusChanged {
			t.Fatalf("unbound terminal = %+v err=%v", result, err)
		}
		attempt, source := sourceCrawlB2RReadAttemptSource(t, raw, 42)
		if attempt != "completed" || source != "processing" {
			t.Fatalf("attempt/source = %s/%s", attempt, source)
		}
	})

	t.Run("ownership transfer suppresses source status", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		claim, receipt := sourceCrawlResultClaim(t, raw, &handler, 42, "unbound-transfer", false)
		if _, err := raw.Exec(`UPDATE Sources SET usr_id=8 WHERE source_id=42`); err != nil {
			t.Fatalf("transfer source: %v", err)
		}
		if _, err := raw.Exec(`UPDATE SourceOwnership
			SET user_id=8, verified_at=CURRENT_TIMESTAMP, verified_by_user_id=9,
			    verification_method='transfer', last_updated_at=CURRENT_TIMESTAMP
			WHERE source_id=42`); err != nil {
			t.Fatalf("transfer provenance: %v", err)
		}
		result, err := reportSourceCrawlAttemptTerminalAt(context.Background(), &handler,
			receipt, claim.ClaimedAt.Add(time.Second))
		if err != nil || result.Result != sourceCrawlTerminalApplied ||
			result.SourceStatusChanged {
			t.Fatalf("transferred unbound terminal = %+v err=%v", result, err)
		}
		attempt, source := sourceCrawlB2RReadAttemptSource(t, raw, 42)
		if attempt != "completed" || source != "processing" {
			t.Fatalf("attempt/source = %s/%s", attempt, source)
		}
	})

	t.Run("changed source status is not overwritten", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		claim, receipt := sourceCrawlResultClaim(t, raw, &handler, 42, "unbound-status", false)
		if _, err := raw.Exec(`UPDATE Sources SET status='error', last_error='operator'
			WHERE source_id=42`); err != nil {
			t.Fatalf("change source status: %v", err)
		}
		result, err := reportSourceCrawlAttemptTerminalAt(context.Background(), &handler,
			receipt, claim.ClaimedAt.Add(time.Second))
		if err != nil || result.Result != sourceCrawlTerminalApplied ||
			result.SourceStatusChanged {
			t.Fatalf("changed-status terminal = %+v err=%v", result, err)
		}
		var status, lastError string
		if err := raw.QueryRow(`SELECT status, last_error FROM Sources WHERE source_id=42`).
			Scan(&status, &lastError); err != nil {
			t.Fatalf("read source: %v", err)
		}
		if status != "error" || lastError != "operator" {
			t.Fatalf("source state = %s/%s", status, lastError)
		}
	})

	t.Run("disabled source suppresses source status", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		claim, receipt := sourceCrawlResultClaim(t, raw, &handler, 42, "unbound-disabled", false)
		if _, err := raw.Exec(`UPDATE Sources SET disabled=TRUE WHERE source_id=42`); err != nil {
			t.Fatalf("disable source: %v", err)
		}
		result, err := reportSourceCrawlAttemptTerminalAt(context.Background(), &handler,
			receipt, claim.ClaimedAt.Add(time.Second))
		if err != nil || result.Result != sourceCrawlTerminalStaleFence {
			t.Fatalf("disabled terminal = %+v err=%v", result, err)
		}
		attempt, source := sourceCrawlB2RReadAttemptSource(t, raw, 42)
		if attempt != "claimed" || source != "processing" {
			t.Fatalf("disabled attempt/source = %s/%s", attempt, source)
		}
	})

	t.Run("expired claim is reclaimable after suppression", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		claim, receipt := sourceCrawlResultClaim(t, raw, &handler, 42, "unbound-reclaim", false)
		result, err := reportSourceCrawlAttemptTerminalAt(context.Background(), &handler,
			receipt, claim.ClaimedAt.Add(time.Second))
		if err != nil || result.Result != sourceCrawlTerminalApplied ||
			result.SourceStatusChanged {
			t.Fatalf("unbound terminal = %+v err=%v", result, err)
		}
		// Simulate the elapsed claim lease. The source is still `processing`,
		// but an expired current claim makes it eligible again.
		expiredLease := claim.ClaimedAt.Add(time.Second)
		if _, err := raw.Exec(`UPDATE SourceCrawlClaims SET lease_expires_at=?
			WHERE source_id=42`, expiredLease); err != nil {
			t.Fatalf("expire current claim: %v", err)
		}
		if _, err := raw.Exec(`UPDATE SourceCrawlAttempts SET lease_expires_at=?
			WHERE source_id=42`, expiredLease); err != nil {
			t.Fatalf("expire history claim: %v", err)
		}
		request := sourceCrawlClaimRequest("unbound-reclaim-next")
		request.Limit = 1
		claims, err := claimSourceCrawlAttemptsAt(context.Background(), &handler, request,
			claim.LeaseExpiresAt.Add(time.Minute))
		if err != nil || len(claims) != 1 || claims[0].SourceID != 42 ||
			claims[0].Generation != claim.Generation+1 {
			t.Fatalf("reclaim = %+v err=%v", claims, err)
		}
		var source string
		if err := raw.QueryRow(`SELECT status FROM Sources WHERE source_id=42`).Scan(&source); err != nil {
			t.Fatalf("read source: %v", err)
		}
		if source != "processing" {
			t.Fatalf("reclaimed source status = %s", source)
		}
	})

	t.Run("deleted source is stale", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		claim, receipt := sourceCrawlResultClaim(t, raw, &handler, 42, "unbound-deleted", false)
		if _, err := raw.Exec(`UPDATE Sources SET deleted_at=CURRENT_TIMESTAMP WHERE source_id=42`); err != nil {
			t.Fatalf("delete source: %v", err)
		}
		result, err := reportSourceCrawlAttemptTerminalAt(context.Background(), &handler,
			receipt, claim.ClaimedAt.Add(time.Second))
		if err != nil || result.Result != sourceCrawlTerminalStaleFence {
			t.Fatalf("deleted terminal = %+v err=%v", result, err)
		}
		attempt, _ := sourceCrawlB2RReadAttemptSource(t, raw, 42)
		if attempt != "claimed" {
			t.Fatalf("deleted source attempt = %s, want claimed", attempt)
		}
	})
}

func TestSourceCrawlB2ROperationStateCompatibilitySQLite(t *testing.T) {
	cases := []struct {
		name          string
		operation     CrawlOperationState
		outcome       sourceCrawlTerminalOutcome
		wantOperation CrawlOperationState
	}{
		{"accepted to completed", CrawlOperationAccepted,
			sourceCrawlTerminalCompletedOutcome, CrawlOperationCompleted},
		{"processing to completed", CrawlOperationProcessing,
			sourceCrawlTerminalCompletedOutcome, CrawlOperationCompleted},
		{"accepted to failed", CrawlOperationAccepted,
			sourceCrawlTerminalFailedOutcome, CrawlOperationFailed},
		{"processing to failed", CrawlOperationProcessing,
			sourceCrawlTerminalFailedOutcome, CrawlOperationFailed},
		{"accepted to cancelled", CrawlOperationAccepted,
			sourceCrawlTerminalCancelledOutcome, CrawlOperationCancelled},
		{"processing to cancelled", CrawlOperationProcessing,
			sourceCrawlTerminalCancelledOutcome, CrawlOperationCancelled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, handler := openAsyncGoalSQLite(t)
			claim, receipt := sourceCrawlResultClaim(t, raw, &handler, 42, tc.name, true)
			if _, err := raw.Exec(`UPDATE Status SET status=? WHERE source_id=?`,
				string(tc.operation), 42); err != nil {
				t.Fatalf("set operation state: %v", err)
			}
			// The worker supplies no expected state; the repository reads the
			// authoritative row under its own locks.
			receipt = sourceCrawlResultOutcome(t, receipt, tc.outcome)
			now := claim.ClaimedAt.Add(2 * time.Second)
			result, err := reportSourceCrawlAttemptTerminalAt(context.Background(), &handler,
				receipt, now)
			if err != nil || result.Result != sourceCrawlTerminalApplied {
				t.Fatalf("terminal = %+v err=%v", result, err)
			}
			attempt, operation, source := sourceCrawlResultReadState(t, raw, 42)
			if operation != string(tc.wantOperation) {
				t.Fatalf("operation = %s want %s", operation, tc.wantOperation)
			}
			if attempt == "claimed" || source == "processing" {
				t.Fatalf("attempt/source = %s/%s", attempt, source)
			}
		})
	}

	t.Run("already_pending is rejected", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		claim, receipt := sourceCrawlResultClaim(t, raw, &handler, 42, "already-pending", true)
		if _, err := raw.Exec(`UPDATE Status SET status='already_pending' WHERE source_id=?`,
			42); err != nil {
			t.Fatalf("set already_pending: %v", err)
		}
		result, err := reportSourceCrawlAttemptTerminalAt(context.Background(), &handler,
			receipt, claim.ClaimedAt.Add(2*time.Second))
		if err != nil || result.Result != sourceCrawlTerminalConflict {
			t.Fatalf("already_pending terminal = %+v err=%v", result, err)
		}
		attempt, operation, _ := sourceCrawlResultReadState(t, raw, 42)
		if attempt != "claimed" || operation != "already_pending" {
			t.Fatalf("attempt/operation = %s/%s", attempt, operation)
		}
	})

	t.Run("changed binding is rejected", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		claim, receipt := sourceCrawlResultClaim(t, raw, &handler, 42, "changed-binding", true)
		receipt.OperationResult.Binding.OperationID = sourceCrawlClaimTestID("other-operation")
		result, err := reportSourceCrawlAttemptTerminalAt(context.Background(), &handler,
			receipt, claim.ClaimedAt.Add(2*time.Second))
		if err != nil || result.Result != sourceCrawlTerminalConflict {
			t.Fatalf("changed binding = %+v err=%v", result, err)
		}
	})
}

func TestSourceCrawlB2RLateWorkerCannotReplaceTerminalSQLite(t *testing.T) {
	raw, handler := openAsyncGoalSQLite(t)
	claim, receipt := sourceCrawlResultClaim(t, raw, &handler, 42, "late-worker", true)
	now := claim.ClaimedAt.Add(2 * time.Second)
	failed := sourceCrawlResultOutcome(t, receipt, sourceCrawlTerminalFailedOutcome)
	if got, err := reportSourceCrawlAttemptTerminalAt(context.Background(), &handler,
		failed, now); err != nil || got.Result != sourceCrawlTerminalApplied {
		t.Fatalf("first terminal = %+v err=%v", got, err)
	}
	// A late completion report for the same attempt is a read-only replay
	// conflict and must not rewrite the operation.
	late := sourceCrawlResultOutcome(t, receipt, sourceCrawlTerminalCompletedOutcome)
	if got, err := reportSourceCrawlAttemptTerminalAt(context.Background(), &handler,
		late, claim.LeaseExpiresAt.Add(time.Hour)); err != nil ||
		got.Result != sourceCrawlTerminalConflict {
		t.Fatalf("late completion = %+v err=%v", got, err)
	}
	attempt, operation, source := sourceCrawlResultReadState(t, raw, 42)
	if attempt != "failed" || operation != "failed" || source != "error" {
		t.Fatalf("states = %s/%s/%s", attempt, operation, source)
	}
}

func TestSourceCrawlB2RClaimBindsProcessingOperationSQLite(t *testing.T) {
	raw, handler := openAsyncGoalSQLite(t)
	sourceCrawlResultSeedSource(t, raw, 42, 7, "bind-processing")
	enableSourceCrawlClaimMode(t, raw)
	sourceCrawlInsertBoundOperation(t, raw, 42, 7, "bind-processing", "processing")
	request := sourceCrawlClaimRequest("bind-processing")
	request.Limit = 1
	claims, err := claimSourceCrawlAttemptsAt(context.Background(), &handler, request,
		sourceCrawlClaimTestNow)
	if err != nil || len(claims) != 1 || claims[0].Outcome != SourceCrawlClaimed ||
		claims[0].OperationID == "" {
		t.Fatalf("processing claim = %+v err=%v", claims, err)
	}
	claim := claims[0]
	binding := &sourceCrawlOperationBinding{
		OperationID: claim.OperationID, RequestID: claim.RequestID,
		RunID: claim.RunID, AgentID: claim.AgentID, Owner: claim.Owner,
		PrincipalUserID: claim.PrincipalUserID, ApprovalID: claim.ApprovalID,
		ApprovalSHA256: claim.ApprovalSHA256,
	}
	receipt := sourceCrawlTerminalReceipt{
		Attempt: sourceCrawlAttempt{
			AttemptID: claim.AttemptID, ClaimRequestID: claim.ClaimRequestID,
			SourceID: claim.SourceID, Generation: claim.Generation, EngineID: claim.EngineID,
			State: sourceCrawlAttemptClaimed, ClaimedAt: claim.ClaimedAt,
			LeaseExpiresAt: claim.LeaseExpiresAt, Binding: binding,
		},
		Attribution: sourceCrawlHandoffBound,
		Outcome:     sourceCrawlTerminalCompletedOutcome,
		ReasonCode:  "crawl_completed",
		ObservedAt:  claim.ClaimedAt.Add(time.Second),
		OperationResult: &sourceCrawlTerminalOperationResult{
			Binding: *binding, ResultState: CrawlOperationCompleted,
		},
	}
	result, err := reportSourceCrawlAttemptTerminalAt(context.Background(), &handler,
		receipt, claim.ClaimedAt.Add(2*time.Second))
	if err != nil || result.Result != sourceCrawlTerminalApplied ||
		!result.SourceStatusChanged {
		t.Fatalf("processing terminal = %+v err=%v", result, err)
	}
	attempt, operation, source := sourceCrawlResultReadState(t, raw, 42)
	if attempt != "completed" || operation != "completed" || source != "completed" {
		t.Fatalf("processing states = %s/%s/%s", attempt, operation, source)
	}
}
