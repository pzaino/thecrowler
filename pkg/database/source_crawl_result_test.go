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
	"fmt"
	"testing"
	"time"
)

func sourceCrawlResultSeedSource(t *testing.T, raw *sql.DB, sourceID, ownerID uint64,
	priority string) {
	t.Helper()
	if _, err := raw.Exec(`INSERT INTO Sources
		(source_id, source_uid, url, name, status, disabled, usr_id, priority, sub_priority)
		VALUES (?, ?, ?, 'result fixture', 'new', FALSE, ?, ?, 0)`,
		sourceID, fmt.Sprintf("uid-result-%d", sourceID),
		fmt.Sprintf("https://result-%d.example/", sourceID), ownerID, priority); err != nil {
		t.Fatalf("insert result source %d: %v", sourceID, err)
	}
	if _, err := raw.Exec(`INSERT INTO SourceOwnership
		(source_id, user_id, verified_at, verified_by_user_id, verified_by, verification_method, verification_reason)
		VALUES (?, ?, CURRENT_TIMESTAMP, 9, 'operator', 'result_test', 'result')`,
		sourceID, ownerID); err != nil {
		t.Fatalf("insert result ownership %d: %v", sourceID, err)
	}
}

func sourceCrawlResultClaim(t *testing.T, raw *sql.DB, handler *Handler,
	sourceID uint64, label string, bound bool) (SourceCrawlClaim, sourceCrawlTerminalReceipt) {
	t.Helper()
	sourceCrawlResultSeedSource(t, raw, sourceID, 7, label)
	enableSourceCrawlClaimMode(t, raw)
	if bound {
		sourceCrawlInsertBoundOperation(t, raw, sourceID, 7, label, "accepted")
	}
	request := sourceCrawlClaimRequest("result-" + label)
	request.Limit = 1
	claims, err := claimSourceCrawlAttemptsAt(context.Background(), handler, request, sourceCrawlClaimTestNow)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claim %s = %+v err=%v", label, claims, err)
	}
	claim := claims[0]
	if bound && claim.Outcome != SourceCrawlClaimed {
		t.Fatalf("bound claim outcome = %s", claim.Outcome)
	}
	if !bound && claim.Outcome != SourceCrawlUnattributed {
		t.Fatalf("unbound claim outcome = %s", claim.Outcome)
	}
	attempt := sourceCrawlAttempt{
		AttemptID: claim.AttemptID, ClaimRequestID: claim.ClaimRequestID,
		SourceID: claim.SourceID, Generation: claim.Generation, EngineID: claim.EngineID,
		State: sourceCrawlAttemptClaimed, ClaimedAt: claim.ClaimedAt,
		LeaseExpiresAt: claim.LeaseExpiresAt,
	}
	receipt := sourceCrawlTerminalReceipt{
		Attempt: attempt, Outcome: sourceCrawlTerminalCompletedOutcome,
		ReasonCode: "crawl_completed", ObservedAt: claim.ClaimedAt.Add(time.Second),
	}
	if bound {
		binding := &sourceCrawlOperationBinding{
			OperationID: claim.OperationID, RequestID: claim.RequestID,
			RunID: claim.RunID, AgentID: claim.AgentID, Owner: claim.Owner,
			PrincipalUserID: claim.PrincipalUserID, ApprovalID: claim.ApprovalID,
			ApprovalSHA256: claim.ApprovalSHA256,
		}
		attempt.Binding = binding
		receipt.Attempt = attempt
		receipt.Attribution = sourceCrawlHandoffBound
		receipt.OperationResult = &sourceCrawlTerminalOperationResult{
			Binding: *binding, ExpectedState: CrawlOperationAccepted,
			ResultState: CrawlOperationCompleted,
		}
	} else {
		receipt.Attribution = sourceCrawlHandoffUnattributed
	}
	return claim, receipt
}

func sourceCrawlResultOutcome(t *testing.T, receipt sourceCrawlTerminalReceipt,
	outcome sourceCrawlTerminalOutcome) sourceCrawlTerminalReceipt {
	t.Helper()
	receipt.Outcome = outcome
	switch outcome {
	case sourceCrawlTerminalCompletedOutcome:
		receipt.ReasonCode = "crawl_completed"
		if receipt.OperationResult != nil {
			receipt.OperationResult.ResultState = CrawlOperationCompleted
		}
	case sourceCrawlTerminalFailedOutcome:
		receipt.ReasonCode = "crawl_failed"
		if receipt.OperationResult != nil {
			receipt.OperationResult.ResultState = CrawlOperationFailed
		}
	case sourceCrawlTerminalCancelledOutcome:
		receipt.ReasonCode = "crawl_cancelled"
		if receipt.OperationResult != nil {
			receipt.OperationResult.ResultState = CrawlOperationCancelled
		}
	default:
		t.Fatalf("unknown outcome %s", outcome)
	}
	return receipt
}

func sourceCrawlResultReadLease(t *testing.T, raw *sql.DB, sourceID uint64) (time.Time, time.Time) {
	t.Helper()
	var current, history time.Time
	if err := raw.QueryRow(`SELECT lease_expires_at FROM SourceCrawlClaims WHERE source_id=?`,
		sourceID).Scan(&current); err != nil {
		t.Fatalf("read current lease: %v", err)
	}
	if err := raw.QueryRow(`SELECT lease_expires_at FROM SourceCrawlAttempts WHERE source_id=?`,
		sourceID).Scan(&history); err != nil {
		t.Fatalf("read history lease: %v", err)
	}
	return current, history
}

func sourceCrawlResultReadState(t *testing.T, raw *sql.DB, sourceID uint64) (string, string, string) {
	t.Helper()
	var attemptState, operationState, sourceState string
	if err := raw.QueryRow(`SELECT state FROM SourceCrawlAttempts WHERE source_id=?`,
		sourceID).Scan(&attemptState); err != nil {
		t.Fatalf("read attempt state: %v", err)
	}
	if err := raw.QueryRow(`SELECT status FROM Status WHERE source_id=?`, sourceID).Scan(&operationState); err != nil {
		t.Fatalf("read operation state: %v", err)
	}
	if err := raw.QueryRow(`SELECT status FROM Sources WHERE source_id=?`, sourceID).Scan(&sourceState); err != nil {
		t.Fatalf("read source state: %v", err)
	}
	return attemptState, operationState, sourceState
}

func TestSourceCrawlResultSQLiteTerminalBoundLifecycle(t *testing.T) {
	cases := []struct {
		name          string
		processing    bool
		outcome       sourceCrawlTerminalOutcome
		wantAttempt   sourceCrawlAttemptState
		wantOperation CrawlOperationState
		wantSource    string
	}{
		{"accepted to completed", false, sourceCrawlTerminalCompletedOutcome,
			sourceCrawlAttemptCompleted, CrawlOperationCompleted, "completed"},
		{"processing to failed", true, sourceCrawlTerminalFailedOutcome,
			sourceCrawlAttemptFailed, CrawlOperationFailed, "error"},
		{"accepted to cancelled", false, sourceCrawlTerminalCancelledOutcome,
			sourceCrawlAttemptCancelled, CrawlOperationCancelled, "error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, handler := openAsyncGoalSQLite(t)
			claim, receipt := sourceCrawlResultClaim(t, raw, &handler, 42, tc.name, true)
			if tc.processing {
				if _, err := raw.Exec(`UPDATE Status SET status='processing' WHERE source_id=?`, 42); err != nil {
					t.Fatalf("set processing: %v", err)
				}
				receipt.OperationResult.ExpectedState = CrawlOperationProcessing
			}
			receipt = sourceCrawlResultOutcome(t, receipt, tc.outcome)
			now := claim.ClaimedAt.Add(2 * time.Second)
			result, err := reportSourceCrawlAttemptTerminalAt(context.Background(), &handler, receipt, now)
			if err != nil || result.Result != sourceCrawlTerminalApplied {
				t.Fatalf("terminal report = %+v err=%v", result, err)
			}
			if !result.SourceStatusChanged {
				t.Fatal("eligible bound source status was not updated")
			}
			attemptState, operationState, sourceState := sourceCrawlResultReadState(t, raw, 42)
			if attemptState != string(tc.wantAttempt) || operationState != string(tc.wantOperation) ||
				sourceState != tc.wantSource {
				t.Fatalf("states = attempt=%s operation=%s source=%s", attemptState, operationState, sourceState)
			}
			var terminalAt time.Time
			var reason string
			if err := raw.QueryRow(`SELECT terminal_at, reason_code FROM SourceCrawlAttempts WHERE source_id=?`,
				42).Scan(&terminalAt, &reason); err != nil {
				t.Fatalf("read terminal attempt: %v", err)
			}
			if !terminalAt.Equal(receipt.ObservedAt) || reason != receipt.ReasonCode {
				t.Fatalf("terminal attempt = %v %s", terminalAt, reason)
			}

			// A read-only replay after lease expiry must not change anything.
			replay, err := reportSourceCrawlAttemptTerminalAt(context.Background(), &handler,
				receipt, claim.LeaseExpiresAt.Add(time.Hour))
			if err != nil || replay.Result != sourceCrawlTerminalIdempotentReplay {
				t.Fatalf("terminal replay = %+v err=%v", replay, err)
			}
			afterAttempt, afterOperation, afterSource := sourceCrawlResultReadState(t, raw, 42)
			if afterAttempt != attemptState || afterOperation != operationState || afterSource != sourceState {
				t.Fatal("terminal replay mutated durable state")
			}

			// Conflicting outcome and identity are read-only conflicts.
			conflictOutcome := sourceCrawlTerminalFailedOutcome
			if receipt.Outcome == sourceCrawlTerminalFailedOutcome {
				conflictOutcome = sourceCrawlTerminalCompletedOutcome
			}
			conflict := sourceCrawlResultOutcome(t, receipt, conflictOutcome)
			if got, err := reportSourceCrawlAttemptTerminalAt(context.Background(), &handler,
				conflict, claim.LeaseExpiresAt.Add(time.Hour)); err != nil ||
				got.Result != sourceCrawlTerminalConflict {
				t.Fatalf("conflicting replay = %+v err=%v", got, err)
			}
			changedEngine := receipt
			changedEngine.Attempt.EngineID = "other-engine"
			if got, err := reportSourceCrawlAttemptTerminalAt(context.Background(), &handler,
				changedEngine, now); err != nil || got.Result != sourceCrawlTerminalConflict {
				t.Fatalf("changed engine = %+v err=%v", got, err)
			}
		})
	}
}

func TestSourceCrawlResultSQLiteTerminalUnboundOnly(t *testing.T) {
	raw, handler := openAsyncGoalSQLite(t)
	claim, receipt := sourceCrawlResultClaim(t, raw, &handler, 42, "unbound", false)
	result, err := reportSourceCrawlAttemptTerminalAt(context.Background(), &handler,
		receipt, claim.ClaimedAt.Add(2*time.Second))
	if err != nil || result.Result != sourceCrawlTerminalApplied {
		t.Fatalf("unbound terminal = %+v err=%v", result, err)
	}
	if result.SourceStatusChanged {
		t.Fatal("unbound attempt changed source status")
	}
	if count := sourceCrawlAttemptHistoryCount(t, raw, 42); count != 1 {
		t.Fatalf("history count = %d", count)
	}
	var attemptState, sourceState string
	if err := raw.QueryRow(`SELECT state FROM SourceCrawlAttempts WHERE source_id=?`, 42).Scan(&attemptState); err != nil {
		t.Fatalf("read attempt: %v", err)
	}
	if err := raw.QueryRow(`SELECT status FROM Sources WHERE source_id=?`, 42).Scan(&sourceState); err != nil {
		t.Fatalf("read source: %v", err)
	}
	if attemptState != string(sourceCrawlAttemptCompleted) || sourceState != "processing" {
		t.Fatalf("unbound states = attempt=%s source=%s", attemptState, sourceState)
	}
	if operations := sourceCrawlStatusOperationIDs(t, raw, 42); len(operations) != 0 {
		t.Fatalf("unbound attempt created Status rows: %v", operations)
	}
	replay, err := reportSourceCrawlAttemptTerminalAt(context.Background(), &handler,
		receipt, claim.LeaseExpiresAt.Add(time.Hour))
	if err != nil || replay.Result != sourceCrawlTerminalIdempotentReplay {
		t.Fatalf("unbound replay = %+v err=%v", replay, err)
	}
}

func TestSourceCrawlResultSQLiteTerminalPolicy(t *testing.T) {
	t.Run("revoked before acceptance", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		claim, receipt := sourceCrawlResultClaim(t, raw, &handler, 42, "revoked-before", true)
		if _, err := raw.Exec(`UPDATE CrawlApprovals SET revoked_at=? WHERE source_id=?`,
			sourceCrawlClaimTestNow.Add(-30*time.Second), 42); err != nil {
			t.Fatalf("revoke approval: %v", err)
		}
		result, err := reportSourceCrawlAttemptTerminalAt(context.Background(), &handler,
			receipt, claim.ClaimedAt.Add(2*time.Second))
		if err != nil || result.Result != sourceCrawlTerminalConflict {
			t.Fatalf("revoked-before report = %+v err=%v", result, err)
		}
		attemptState, operationState, sourceState := sourceCrawlResultReadState(t, raw, 42)
		if attemptState != "claimed" || operationState != "accepted" || sourceState != "processing" {
			t.Fatalf("revoked-before mutated states: %s/%s/%s", attemptState, operationState, sourceState)
		}
	})

	t.Run("revoked after acceptance", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		claim, receipt := sourceCrawlResultClaim(t, raw, &handler, 42, "revoked-after", true)
		if _, err := raw.Exec(`UPDATE CrawlApprovals SET revoked_at=? WHERE source_id=?`,
			sourceCrawlClaimTestNow.Add(500*time.Millisecond), 42); err != nil {
			t.Fatalf("revoke approval: %v", err)
		}
		result, err := reportSourceCrawlAttemptTerminalAt(context.Background(), &handler,
			receipt, claim.ClaimedAt.Add(2*time.Second))
		if err != nil || result.Result != sourceCrawlTerminalApplied {
			t.Fatalf("revoked-after report = %+v err=%v", result, err)
		}
	})

	t.Run("expired after acceptance", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		claim, receipt := sourceCrawlResultClaim(t, raw, &handler, 42, "expired-after", true)
		expiresAt := sourceCrawlClaimTestNow.Add(500 * time.Millisecond)
		var rawDetails string
		if err := raw.QueryRow(`SELECT details FROM Status WHERE source_id=42`).Scan(&rawDetails); err != nil {
			t.Fatalf("read status details: %v", err)
		}
		var details crawlOperationDetails
		if err := json.Unmarshal([]byte(rawDetails), &details); err != nil {
			t.Fatalf("decode status details: %v", err)
		}
		details.ExpiresAt = expiresAt.Format(time.RFC3339Nano)
		detailsJSON, err := json.Marshal(details)
		if err != nil {
			t.Fatalf("encode status details: %v", err)
		}
		if _, err := raw.Exec(`UPDATE CrawlApprovals SET expires_at=? WHERE source_id=?`,
			expiresAt, 42); err != nil {
			t.Fatalf("expire approval: %v", err)
		}
		if _, err := raw.Exec(`UPDATE Status SET details=? WHERE source_id=?`,
			string(detailsJSON), 42); err != nil {
			t.Fatalf("update status details: %v", err)
		}
		result, err := reportSourceCrawlAttemptTerminalAt(context.Background(), &handler,
			receipt, claim.ClaimedAt.Add(2*time.Second))
		if err != nil || result.Result != sourceCrawlTerminalApplied {
			t.Fatalf("expired-after report = %+v err=%v", result, err)
		}
	})

	t.Run("competing proposal", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		claim, receipt := sourceCrawlResultClaim(t, raw, &handler, 42, "competing", true)
		sourceCrawlSubmittedProposalFixture(t, raw, 42, 7, "competitor", nil, "submitted")
		result, err := reportSourceCrawlAttemptTerminalAt(context.Background(), &handler,
			receipt, claim.ClaimedAt.Add(2*time.Second))
		if err != nil || result.Result != sourceCrawlTerminalConflict {
			t.Fatalf("competing proposal = %+v err=%v", result, err)
		}
		attemptState, operationState, _ := sourceCrawlResultReadState(t, raw, 42)
		if attemptState != "claimed" || operationState != "accepted" {
			t.Fatalf("competing proposal mutated states: %s/%s", attemptState, operationState)
		}
	})

	t.Run("invalid submitted proposal", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		claim, receipt := sourceCrawlResultClaim(t, raw, &handler, 42, "invalid-proposal", true)
		if _, err := raw.Exec(`UPDATE CrawlProposals SET state='pending' WHERE source_id=?`, 42); err != nil {
			t.Fatalf("invalidate proposal: %v", err)
		}
		result, err := reportSourceCrawlAttemptTerminalAt(context.Background(), &handler,
			receipt, claim.ClaimedAt.Add(2*time.Second))
		if err != nil || result.Result != sourceCrawlTerminalConflict {
			t.Fatalf("invalid proposal = %+v err=%v", result, err)
		}
	})
}

func TestSourceCrawlResultSQLiteSourceStatusPolicy(t *testing.T) {
	t.Run("owner transferred", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		claim, receipt := sourceCrawlResultClaim(t, raw, &handler, 42, "owner-transfer", true)
		if _, err := raw.Exec(`UPDATE Sources SET usr_id=8 WHERE source_id=42`); err != nil {
			t.Fatalf("transfer source: %v", err)
		}
		if _, err := raw.Exec(`UPDATE SourceOwnership SET user_id=8 WHERE source_id=42`); err != nil {
			t.Fatalf("transfer ownership: %v", err)
		}
		result, err := reportSourceCrawlAttemptTerminalAt(context.Background(), &handler,
			receipt, claim.ClaimedAt.Add(2*time.Second))
		if err != nil || result.Result != sourceCrawlTerminalApplied || result.SourceStatusChanged {
			t.Fatalf("owner transfer result = %+v err=%v", result, err)
		}
		var sourceState string
		if err := raw.QueryRow(`SELECT status FROM Sources WHERE source_id=42`).Scan(&sourceState); err != nil {
			t.Fatalf("read source: %v", err)
		}
		if sourceState != "processing" {
			t.Fatalf("owner transfer changed source status to %s", sourceState)
		}
	})

	t.Run("disabled source preserves operation result", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		claim, receipt := sourceCrawlResultClaim(t, raw, &handler, 42, "disabled", true)
		if _, err := raw.Exec(`UPDATE Sources SET disabled=TRUE WHERE source_id=42`); err != nil {
			t.Fatalf("disable source: %v", err)
		}
		result, err := reportSourceCrawlAttemptTerminalAt(context.Background(), &handler,
			receipt, claim.ClaimedAt.Add(2*time.Second))
		if err != nil || result.Result != sourceCrawlTerminalApplied || result.SourceStatusChanged {
			t.Fatalf("disabled source result = %+v err=%v", result, err)
		}
		var sourceState string
		if err := raw.QueryRow(`SELECT status FROM Sources WHERE source_id=42`).Scan(&sourceState); err != nil {
			t.Fatalf("read source: %v", err)
		}
		if sourceState != "processing" {
			t.Fatalf("disabled source status = %s", sourceState)
		}
	})

	t.Run("deleted source rejects", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		claim, receipt := sourceCrawlResultClaim(t, raw, &handler, 42, "deleted", true)
		if _, err := raw.Exec(`UPDATE Sources SET deleted_at=CURRENT_TIMESTAMP WHERE source_id=42`); err != nil {
			t.Fatalf("delete source: %v", err)
		}
		result, err := reportSourceCrawlAttemptTerminalAt(context.Background(), &handler,
			receipt, claim.ClaimedAt.Add(2*time.Second))
		if err != nil || result.Result != sourceCrawlTerminalStaleFence {
			t.Fatalf("deleted source result = %+v err=%v", result, err)
		}
		attemptState, operationState, _ := sourceCrawlResultReadState(t, raw, 42)
		if attemptState != "claimed" || operationState != "accepted" {
			t.Fatalf("deleted source mutated states: %s/%s", attemptState, operationState)
		}
	})
}

func TestSourceCrawlResultSQLiteReplayAfterSourceDeletion(t *testing.T) {
	raw, handler := openAsyncGoalSQLite(t)
	claim, receipt := sourceCrawlResultClaim(t, raw, &handler, 42, "deleted-replay", true)
	if result, err := reportSourceCrawlAttemptTerminalAt(context.Background(), &handler,
		receipt, claim.ClaimedAt.Add(2*time.Second)); err != nil ||
		result.Result != sourceCrawlTerminalApplied {
		t.Fatalf("initial terminal = %+v err=%v", result, err)
	}
	if _, err := raw.Exec(`UPDATE Sources SET deleted_at=CURRENT_TIMESTAMP WHERE source_id=42`); err != nil {
		t.Fatalf("delete source: %v", err)
	}
	replay, err := reportSourceCrawlAttemptTerminalAt(context.Background(), &handler,
		receipt, claim.LeaseExpiresAt.Add(time.Hour))
	if err != nil || replay.Result != sourceCrawlTerminalIdempotentReplay {
		t.Fatalf("deleted-source replay = %+v err=%v", replay, err)
	}
}

func TestSourceCrawlResultSQLiteHeartbeat(t *testing.T) {
	raw, handler := openAsyncGoalSQLite(t)
	claim, _ := sourceCrawlResultClaim(t, raw, &handler, 42, "heartbeat", true)
	request := sourceCrawlLeaseRenewalRequest{
		SourceID: claim.SourceID, AttemptID: claim.AttemptID,
		Generation: claim.Generation, ClaimRequestID: claim.ClaimRequestID,
		EngineID: claim.EngineID, Extension: time.Minute,
	}
	now := claim.ClaimedAt.Add(time.Second)
	result, err := renewSourceCrawlAttemptLeaseAt(context.Background(), &handler, request, now)
	if err != nil || result.Result != sourceCrawlTerminalApplied {
		t.Fatalf("heartbeat = %+v err=%v", result, err)
	}
	wantLease := claim.LeaseExpiresAt.Add(time.Minute)
	current, history := sourceCrawlResultReadLease(t, raw, 42)
	if !current.Equal(wantLease) || !history.Equal(wantLease) {
		t.Fatalf("leases current=%v history=%v want=%v", current, history, wantLease)
	}
	if result.Claim.AttemptID != claim.AttemptID || !result.Claim.LeaseExpiresAt.Equal(wantLease) {
		t.Fatalf("heartbeat result claim = %+v", result.Claim)
	}
	// Original claim request replay must now fail closed.
	if _, err := claimSourceCrawlAttemptsAt(context.Background(), &handler,
		sourceCrawlClaimRequest("result-heartbeat"), now.Add(time.Minute)); err == nil {
		t.Fatal("original claim request replay after heartbeat succeeded")
	}
	// More than one hour per heartbeat is rejected before any write.
	if over, err := renewSourceCrawlAttemptLeaseAt(context.Background(), &handler,
		sourceCrawlLeaseRenewalRequest{
			SourceID: claim.SourceID, AttemptID: claim.AttemptID,
			Generation: claim.Generation, ClaimRequestID: claim.ClaimRequestID,
			EngineID: claim.EngineID, Extension: 2 * time.Hour,
		}, now); err != nil || over.Result != sourceCrawlTerminalConflict {
		t.Fatalf("2h heartbeat = %+v err=%v", over, err)
	}
	// Over the 24-hour lifetime.
	over, err := renewSourceCrawlAttemptLeaseAt(context.Background(), &handler,
		sourceCrawlLeaseRenewalRequest{
			SourceID: claim.SourceID, AttemptID: claim.AttemptID,
			Generation: claim.Generation, ClaimRequestID: claim.ClaimRequestID,
			EngineID: claim.EngineID, Extension: 24 * time.Hour,
		}, now)
	if err != nil || over.Result != sourceCrawlTerminalConflict {
		t.Fatalf("24h heartbeat = %+v err=%v", over, err)
	}
	// Wrong engine and wrong generation.
	for _, mutate := range []func(*sourceCrawlLeaseRenewalRequest){
		func(r *sourceCrawlLeaseRenewalRequest) { r.EngineID = "other-engine" },
		func(r *sourceCrawlLeaseRenewalRequest) { r.Generation++ },
		func(r *sourceCrawlLeaseRenewalRequest) { r.AttemptID = sourceCrawlClaimTestID("other") },
	} {
		subject := request
		mutate(&subject)
		got, err := renewSourceCrawlAttemptLeaseAt(context.Background(), &handler, subject, now)
		if err != nil || (got.Result != sourceCrawlTerminalConflict && got.Result != sourceCrawlTerminalStaleFence) {
			t.Fatalf("invalid heartbeat = %+v err=%v", got, err)
		}
	}
	// At exact expiry the fence is stale.
	if got, err := renewSourceCrawlAttemptLeaseAt(context.Background(), &handler, request, wantLease); err != nil ||
		got.Result != sourceCrawlTerminalStaleFence {
		t.Fatalf("expired heartbeat = %+v err=%v", got, err)
	}
}

func TestSourceCrawlResultSQLiteUnboundHeartbeat(t *testing.T) {
	raw, handler := openAsyncGoalSQLite(t)
	claim, _ := sourceCrawlResultClaim(t, raw, &handler, 42, "unbound-heartbeat", false)
	request := sourceCrawlLeaseRenewalRequest{
		SourceID: claim.SourceID, AttemptID: claim.AttemptID,
		Generation: claim.Generation, ClaimRequestID: claim.ClaimRequestID,
		EngineID: claim.EngineID, Extension: time.Minute,
	}
	result, err := renewSourceCrawlAttemptLeaseAt(context.Background(), &handler,
		request, claim.ClaimedAt.Add(time.Second))
	if err != nil || result.Result != sourceCrawlTerminalApplied {
		t.Fatalf("unbound heartbeat = %+v err=%v", result, err)
	}
	if result.Claim.Outcome != SourceCrawlUnattributed ||
		result.Claim.OperationID != "" || result.Claim.ApprovalID != "" {
		t.Fatalf("unbound heartbeat claim = %+v", result.Claim)
	}
	current, history := sourceCrawlResultReadLease(t, raw, 42)
	want := claim.LeaseExpiresAt.Add(time.Minute)
	if !current.Equal(want) || !history.Equal(want) {
		t.Fatalf("unbound heartbeat leases = %v/%v want %v", current, history, want)
	}
	if operations := sourceCrawlStatusOperationIDs(t, raw, 42); len(operations) != 0 {
		t.Fatalf("unbound heartbeat created Status rows: %v", operations)
	}
}

func TestSourceCrawlResultSQLiteSupersededAndTerminalFences(t *testing.T) {
	raw, handler := openAsyncGoalSQLite(t)
	claim, receipt := sourceCrawlResultClaim(t, raw, &handler, 42, "superseded", true)
	// Expire the current claim and allocate a new generation.
	if _, err := raw.Exec(`UPDATE SourceCrawlClaims SET claimed_at=?, lease_expires_at=? WHERE source_id=42`,
		claim.ClaimedAt.Add(-2*time.Second), claim.ClaimedAt.Add(-time.Second)); err != nil {
		t.Fatalf("expire claim: %v", err)
	}
	if _, err := raw.Exec(`UPDATE Sources SET status='pending' WHERE source_id=42`); err != nil {
		t.Fatalf("reset source: %v", err)
	}
	request := sourceCrawlClaimRequest("superseded-second")
	request.Limit = 1
	if second, err := claimSourceCrawlAttemptsAt(context.Background(), &handler, request,
		claim.ClaimedAt.Add(time.Minute)); err != nil || len(second) != 1 || second[0].Generation != 2 {
		t.Fatalf("second claim = %+v err=%v", second, err)
	}
	result, err := reportSourceCrawlAttemptTerminalAt(context.Background(), &handler,
		receipt, claim.LeaseExpiresAt.Add(time.Hour))
	if err != nil || result.Result != sourceCrawlTerminalStaleFence {
		t.Fatalf("superseded terminal = %+v err=%v", result, err)
	}
	var state string
	if err := raw.QueryRow(`SELECT state FROM SourceCrawlAttempts WHERE attempt_id=?`,
		claim.AttemptID).Scan(&state); err != nil {
		t.Fatalf("read superseded attempt: %v", err)
	}
	if state != "superseded" {
		t.Fatalf("superseded attempt state = %s", state)
	}
	// Heartbeat against the superseded attempt must also fail closed.
	heartbeat, err := renewSourceCrawlAttemptLeaseAt(context.Background(), &handler,
		sourceCrawlLeaseRenewalRequest{
			SourceID: claim.SourceID, AttemptID: claim.AttemptID,
			Generation: claim.Generation, ClaimRequestID: claim.ClaimRequestID,
			EngineID: claim.EngineID, Extension: time.Minute,
		}, claim.LeaseExpiresAt.Add(time.Hour))
	if err != nil || heartbeat.Result != sourceCrawlTerminalStaleFence {
		t.Fatalf("superseded heartbeat = %+v err=%v", heartbeat, err)
	}
}

func TestSourceCrawlResultSQLiteLegacyMode(t *testing.T) {
	raw, handler := openAsyncGoalSQLite(t)
	sourceCrawlResultSeedSource(t, raw, 42, 7, "legacy")
	receipt := sourceCrawlTerminalReceipt{
		Attempt: sourceCrawlAttempt{
			AttemptID: sourceCrawlClaimTestID("legacy"), ClaimRequestID: sourceCrawlClaimTestID("legacy-request"),
			SourceID: 42, Generation: 1, EngineID: "engine", State: sourceCrawlAttemptClaimed,
			ClaimedAt: sourceCrawlClaimTestNow, LeaseExpiresAt: sourceCrawlClaimTestNow.Add(time.Minute),
		},
		Attribution: sourceCrawlHandoffUnattributed,
		Outcome:     sourceCrawlTerminalCompletedOutcome, ReasonCode: "crawl_completed",
		ObservedAt: sourceCrawlClaimTestNow.Add(time.Second),
	}
	result, err := reportSourceCrawlAttemptTerminalAt(context.Background(), &handler, receipt,
		sourceCrawlClaimTestNow.Add(2*time.Second))
	if err != nil || result.Result != sourceCrawlTerminalUnsupported {
		t.Fatalf("legacy terminal = %+v err=%v", result, err)
	}
	heartbeat, err := renewSourceCrawlAttemptLeaseAt(context.Background(), &handler,
		sourceCrawlLeaseRenewalRequest{
			SourceID: 42, AttemptID: receipt.Attempt.AttemptID, Generation: 1,
			ClaimRequestID: receipt.Attempt.ClaimRequestID, EngineID: "engine",
			Extension: time.Minute,
		}, sourceCrawlClaimTestNow.Add(time.Second))
	if err != nil || heartbeat.Result != sourceCrawlTerminalUnsupported {
		t.Fatalf("legacy heartbeat = %+v err=%v", heartbeat, err)
	}
}

func TestSourceCrawlResultSQLiteRollback(t *testing.T) {
	t.Run("terminal status failure rolls back attempt", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		claim, receipt := sourceCrawlResultClaim(t, raw, &handler, 42, "rollback-terminal", true)
		if _, err := raw.Exec(`CREATE TRIGGER fail_terminal_status BEFORE UPDATE ON Status
			WHEN NEW.status IN ('completed','failed','cancelled')
			BEGIN SELECT RAISE(ABORT, 'injected status failure'); END;`); err != nil {
			t.Fatalf("create status trigger: %v", err)
		}
		result, err := reportSourceCrawlAttemptTerminalAt(context.Background(), &handler,
			receipt, claim.ClaimedAt.Add(2*time.Second))
		if err == nil && result.Result == sourceCrawlTerminalApplied {
			t.Fatal("terminal report unexpectedly succeeded")
		}
		attemptState, operationState, sourceState := sourceCrawlResultReadState(t, raw, 42)
		if attemptState != "claimed" || operationState != "accepted" || sourceState != "processing" {
			t.Fatalf("rollback left states: %s/%s/%s", attemptState, operationState, sourceState)
		}
	})

	t.Run("heartbeat history failure rolls back both leases", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		claim, _ := sourceCrawlResultClaim(t, raw, &handler, 42, "rollback-heartbeat", true)
		if _, err := raw.Exec(`CREATE TRIGGER fail_lease_update BEFORE UPDATE ON SourceCrawlAttempts
			WHEN NEW.lease_expires_at <> OLD.lease_expires_at
			BEGIN SELECT RAISE(ABORT, 'injected lease failure'); END;`); err != nil {
			t.Fatalf("create lease trigger: %v", err)
		}
		request := sourceCrawlLeaseRenewalRequest{
			SourceID: claim.SourceID, AttemptID: claim.AttemptID,
			Generation: claim.Generation, ClaimRequestID: claim.ClaimRequestID,
			EngineID: claim.EngineID, Extension: time.Minute,
		}
		result, err := renewSourceCrawlAttemptLeaseAt(context.Background(), &handler, request,
			claim.ClaimedAt.Add(time.Second))
		if err == nil && result.Result == sourceCrawlTerminalApplied {
			t.Fatal("heartbeat unexpectedly succeeded")
		}
		current, history := sourceCrawlResultReadLease(t, raw, 42)
		if !current.Equal(claim.LeaseExpiresAt) || !history.Equal(claim.LeaseExpiresAt) {
			t.Fatalf("heartbeat rollback left leases current=%v history=%v", current, history)
		}
	})
}

func TestSourceCrawlResultSQLiteTerminalReplayAfterReplacement(t *testing.T) {
	raw, handler := openAsyncGoalSQLite(t)
	claim, receipt := sourceCrawlResultClaim(t, raw, &handler, 42, "replacement", true)
	if result, err := reportSourceCrawlAttemptTerminalAt(context.Background(), &handler,
		receipt, claim.ClaimedAt.Add(2*time.Second)); err != nil ||
		result.Result != sourceCrawlTerminalApplied {
		t.Fatalf("initial terminal = %+v err=%v", result, err)
	}
	// Expire the current claim and schedule a new generation.
	if _, err := raw.Exec(`UPDATE SourceCrawlClaims SET claimed_at=?, lease_expires_at=? WHERE source_id=42`,
		claim.ClaimedAt.Add(-2*time.Second), claim.ClaimedAt.Add(-time.Second)); err != nil {
		t.Fatalf("expire current claim: %v", err)
	}
	if _, err := raw.Exec(`UPDATE Sources SET status='pending' WHERE source_id=42`); err != nil {
		t.Fatalf("reset source: %v", err)
	}
	request := sourceCrawlClaimRequest("replacement-second")
	request.Limit = 1
	second, err := claimSourceCrawlAttemptsAt(context.Background(), &handler, request,
		claim.ClaimedAt.Add(time.Minute))
	if err != nil || len(second) != 1 || second[0].Generation != 2 {
		t.Fatalf("second claim = %+v err=%v", second, err)
	}
	replay, err := reportSourceCrawlAttemptTerminalAt(context.Background(), &handler,
		receipt, claim.LeaseExpiresAt.Add(time.Hour))
	if err != nil || replay.Result != sourceCrawlTerminalIdempotentReplay {
		t.Fatalf("replay after replacement = %+v err=%v", replay, err)
	}
	var generation uint64
	if err := raw.QueryRow(`SELECT generation FROM SourceCrawlClaims WHERE source_id=42`).
		Scan(&generation); err != nil {
		t.Fatalf("read current generation: %v", err)
	}
	if generation != 2 {
		t.Fatalf("replay changed current generation to %d", generation)
	}
}

func TestSourceCrawlResultSQLiteClaimHistoryMismatch(t *testing.T) {
	cases := []struct {
		name  string
		query string
		value any
	}{
		{"claim request", `UPDATE SourceCrawlClaims SET claim_request_id=? WHERE source_id=42`,
			sourceCrawlClaimTestID("mismatch")},
		{"engine", `UPDATE SourceCrawlClaims SET engine_id=? WHERE source_id=42`, "other-engine"},
		{"claimed at", `UPDATE SourceCrawlClaims SET claimed_at=? WHERE source_id=42`,
			sourceCrawlClaimTestNow.Add(-time.Second)},
		{"lease", `UPDATE SourceCrawlClaims SET lease_expires_at=? WHERE source_id=42`,
			sourceCrawlClaimTestNow.Add(6 * time.Minute)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, handler := openAsyncGoalSQLite(t)
			claim, receipt := sourceCrawlResultClaim(t, raw, &handler, 42, "mismatch-"+tc.name, true)
			if _, err := raw.Exec(tc.query, tc.value); err != nil {
				t.Fatalf("corrupt current claim: %v", err)
			}
			result, err := reportSourceCrawlAttemptTerminalAt(context.Background(), &handler,
				receipt, claim.ClaimedAt.Add(2*time.Second))
			if err != nil || result.Result != sourceCrawlTerminalConflict {
				t.Fatalf("terminal with current/history mismatch = %+v err=%v", result, err)
			}
			heartbeat, err := renewSourceCrawlAttemptLeaseAt(context.Background(), &handler,
				sourceCrawlLeaseRenewalRequest{
					SourceID: claim.SourceID, AttemptID: claim.AttemptID,
					Generation: claim.Generation, ClaimRequestID: claim.ClaimRequestID,
					EngineID: claim.EngineID, Extension: time.Minute,
				}, claim.ClaimedAt.Add(2*time.Second))
			if err != nil || heartbeat.Result != sourceCrawlTerminalConflict {
				t.Fatalf("heartbeat with current/history mismatch = %+v err=%v", heartbeat, err)
			}
			attemptState, operationState, sourceState := sourceCrawlResultReadState(t, raw, 42)
			if attemptState != "claimed" || operationState != "accepted" || sourceState != "processing" {
				t.Fatalf("mismatch mutated states: %s/%s/%s", attemptState, operationState, sourceState)
			}
			var historyLease time.Time
			if err := raw.QueryRow(`SELECT lease_expires_at FROM SourceCrawlAttempts WHERE source_id=42`).
				Scan(&historyLease); err != nil {
				t.Fatalf("read history lease: %v", err)
			}
			if !historyLease.Equal(claim.LeaseExpiresAt) {
				t.Fatalf("history lease was modified: %v", historyLease)
			}
		})
	}
}

func TestSourceCrawlResultSQLiteAlreadyPendingAndMissingProposal(t *testing.T) {
	t.Run("already pending is unbound history only", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		sourceCrawlResultSeedSource(t, raw, 42, 7, "already-pending")
		enableSourceCrawlClaimMode(t, raw)
		sourceCrawlInsertBoundOperation(t, raw, 42, 7, "already-pending", "already_pending")
		request := sourceCrawlClaimRequest("already-pending")
		request.Limit = 1
		claims, err := claimSourceCrawlAttemptsAt(context.Background(), &handler, request,
			sourceCrawlClaimTestNow)
		if err != nil || len(claims) != 1 || claims[0].Outcome != SourceCrawlUnattributed {
			t.Fatalf("already-pending claim = %+v err=%v", claims, err)
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
		result, err := reportSourceCrawlAttemptTerminalAt(context.Background(), &handler,
			receipt, claim.ClaimedAt.Add(2*time.Second))
		if err != nil || result.Result != sourceCrawlTerminalApplied || result.SourceStatusChanged {
			t.Fatalf("already-pending terminal = %+v err=%v", result, err)
		}
		var operationState, sourceState string
		if err := raw.QueryRow(`SELECT status FROM Status WHERE source_id=42`).Scan(&operationState); err != nil {
			t.Fatalf("read already-pending status: %v", err)
		}
		if err := raw.QueryRow(`SELECT status FROM Sources WHERE source_id=42`).Scan(&sourceState); err != nil {
			t.Fatalf("read already-pending source: %v", err)
		}
		if operationState != "already_pending" || sourceState != "processing" {
			t.Fatalf("already-pending states = %s/%s", operationState, sourceState)
		}
	})

	t.Run("missing proposal conflicts", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		claim, receipt := sourceCrawlResultClaim(t, raw, &handler, 42, "missing-proposal", true)
		if _, err := raw.Exec(`DELETE FROM CrawlProposals WHERE source_id=42`); err != nil {
			t.Fatalf("delete proposal: %v", err)
		}
		result, err := reportSourceCrawlAttemptTerminalAt(context.Background(), &handler,
			receipt, claim.ClaimedAt.Add(2*time.Second))
		if err != nil || result.Result != sourceCrawlTerminalConflict {
			t.Fatalf("missing proposal = %+v err=%v", result, err)
		}
	})

	t.Run("invalid approval chronology conflicts", func(t *testing.T) {
		raw, handler := openAsyncGoalSQLite(t)
		claim, receipt := sourceCrawlResultClaim(t, raw, &handler, 42, "bad-chronology", true)
		if _, err := raw.Exec(`UPDATE CrawlApprovals SET approved_at=? WHERE source_id=42`,
			sourceCrawlClaimTestNow.Add(time.Minute)); err != nil {
			t.Fatalf("corrupt approval chronology: %v", err)
		}
		result, err := reportSourceCrawlAttemptTerminalAt(context.Background(), &handler,
			receipt, claim.ClaimedAt.Add(2*time.Second))
		if err != nil || result.Result != sourceCrawlTerminalConflict {
			t.Fatalf("invalid chronology = %+v err=%v", result, err)
		}
	})
}

func TestSourceCrawlResultSQLiteReconcilePaths(t *testing.T) {
	raw, handler := openAsyncGoalSQLite(t)
	claim, receipt := sourceCrawlResultClaim(t, raw, &handler, 42, "reconcile", true)
	if result, err := reportSourceCrawlAttemptTerminalAt(context.Background(), &handler,
		receipt, claim.ClaimedAt.Add(2*time.Second)); err != nil ||
		result.Result != sourceCrawlTerminalApplied {
		t.Fatalf("base terminal = %+v err=%v", result, err)
	}
	reconciled, err := sourceCrawlReconcileTerminalCommit(context.Background(), &handler, receipt)
	if err != nil || reconciled.Result != sourceCrawlTerminalIdempotentReplay ||
		reconciled.Reason != "commit_reconciled" {
		t.Fatalf("terminal commit reconciliation = %+v err=%v", reconciled, err)
	}

	heartbeatRaw, heartbeatHandler := openAsyncGoalSQLite(t)
	heartbeatClaim, _ := sourceCrawlResultClaim(t, heartbeatRaw, &heartbeatHandler, 43,
		"reconcile-heartbeat", true)
	request := sourceCrawlLeaseRenewalRequest{
		SourceID: heartbeatClaim.SourceID, AttemptID: heartbeatClaim.AttemptID,
		Generation: heartbeatClaim.Generation, ClaimRequestID: heartbeatClaim.ClaimRequestID,
		EngineID: heartbeatClaim.EngineID, Extension: time.Minute,
	}
	if result, err := renewSourceCrawlAttemptLeaseAt(context.Background(), &heartbeatHandler,
		request, heartbeatClaim.ClaimedAt.Add(time.Second)); err != nil ||
		result.Result != sourceCrawlTerminalApplied {
		t.Fatalf("base heartbeat = %+v err=%v", result, err)
	}
	target := heartbeatClaim.LeaseExpiresAt.Add(time.Minute)
	leaseReconciled, err := sourceCrawlReconcileLeaseCommit(context.Background(), heartbeatHandler,
		request, target)
	if err != nil || leaseReconciled.Result != sourceCrawlTerminalApplied ||
		leaseReconciled.Reason != "lease_renewed_reconciled" {
		t.Fatalf("heartbeat commit reconciliation = %+v err=%v", leaseReconciled, err)
	}
}

func TestSourceCrawlResultSQLiteCorruptStoredReplay(t *testing.T) {
	raw, handler := openAsyncGoalSQLite(t)
	claim, receipt := sourceCrawlResultClaim(t, raw, &handler, 42, "corrupt-replay", true)
	if result, err := reportSourceCrawlAttemptTerminalAt(context.Background(), &handler,
		receipt, claim.ClaimedAt.Add(2*time.Second)); err != nil ||
		result.Result != sourceCrawlTerminalApplied {
		t.Fatalf("initial terminal = %+v err=%v", result, err)
	}
	if _, err := raw.Exec(`UPDATE SourceCrawlAttempts SET operation_id=? WHERE source_id=42`,
		sourceCrawlClaimTestID("corrupt-operation")); err != nil {
		t.Fatalf("corrupt stored binding: %v", err)
	}
	replay, err := reportSourceCrawlAttemptTerminalAt(context.Background(), &handler,
		receipt, claim.LeaseExpiresAt.Add(time.Hour))
	if err != nil || replay.Result != sourceCrawlTerminalConflict {
		t.Fatalf("corrupt stored replay = %+v err=%v", replay, err)
	}
}
