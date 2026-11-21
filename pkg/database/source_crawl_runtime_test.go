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
	"testing"
	"time"
)

func TestSourceCrawlHandoffSQLiteBound(t *testing.T) {
	raw, handler := openAsyncGoalSQLite(t)
	claim, _ := sourceCrawlResultClaim(t, raw, &handler, 42, "runtime-bound", true)
	handoff, err := NewSourceCrawlHandoff(claim)
	if err != nil {
		t.Fatalf("new handoff: %v", err)
	}
	if !handoff.Bound() || handoff.SourceID() != 42 || handoff.AttemptID() != claim.AttemptID {
		t.Fatalf("handoff identity = source=%d attempt=%s bound=%v",
			handoff.SourceID(), handoff.AttemptID(), handoff.Bound())
	}
	renewed, err := handoff.renewLeaseAt(context.Background(), &handler, time.Minute,
		claim.ClaimedAt.Add(time.Second))
	if err != nil || renewed.Result != SourceCrawlResultApplied {
		t.Fatalf("handoff renewal = %+v err=%v", renewed, err)
	}
	wantLease := claim.LeaseExpiresAt.Add(time.Minute)
	if !renewed.LeaseExpiresAt.Equal(wantLease) || !handoff.LeaseExpiresAt().Equal(wantLease) {
		t.Fatalf("handoff lease = %v/%v want %v", renewed.LeaseExpiresAt, handoff.LeaseExpiresAt(), wantLease)
	}
	current, history := sourceCrawlResultReadLease(t, raw, 42)
	if !current.Equal(wantLease) || !history.Equal(wantLease) {
		t.Fatalf("database leases = %v/%v want %v", current, history, wantLease)
	}

	terminal, err := handoff.reportTerminalAt(context.Background(), &handler,
		SourceCrawlOutcomeCompleted, claim.ClaimedAt.Add(2*time.Second), claim.ClaimedAt.Add(2*time.Second))
	if err != nil || terminal.Result != SourceCrawlResultApplied || !terminal.SourceStatusChanged {
		t.Fatalf("handoff terminal = %+v err=%v", terminal, err)
	}
	attemptState, operationState, sourceState := sourceCrawlResultReadState(t, raw, 42)
	if attemptState != "completed" || operationState != "completed" || sourceState != "completed" {
		t.Fatalf("handoff states = %s/%s/%s", attemptState, operationState, sourceState)
	}
	replay, err := handoff.reportTerminalAt(context.Background(), &handler,
		SourceCrawlOutcomeCompleted, claim.ClaimedAt.Add(3*time.Second),
		claim.LeaseExpiresAt.Add(time.Hour))
	if err != nil || replay.Result != SourceCrawlResultIdempotentReplay {
		t.Fatalf("handoff replay = %+v err=%v", replay, err)
	}
	after, err := handoff.RenewLease(context.Background(), &handler, time.Minute)
	if err != nil || after.Result != SourceCrawlResultStaleFence || after.Reason != "handoff_terminal" {
		t.Fatalf("terminal handoff renewal = %+v err=%v", after, err)
	}
}

func TestSourceCrawlHandoffSQLiteUnbound(t *testing.T) {
	raw, handler := openAsyncGoalSQLite(t)
	claim, _ := sourceCrawlResultClaim(t, raw, &handler, 42, "runtime-unbound", false)
	handoff, err := NewSourceCrawlHandoff(claim)
	if err != nil {
		t.Fatalf("new unbound handoff: %v", err)
	}
	if handoff.Bound() {
		t.Fatal("unbound handoff reports a binding")
	}
	renewed, err := handoff.renewLeaseAt(context.Background(), &handler, time.Minute,
		claim.ClaimedAt.Add(time.Second))
	if err != nil || renewed.Result != SourceCrawlResultApplied {
		t.Fatalf("unbound renewal = %+v err=%v", renewed, err)
	}
	terminal, err := handoff.reportTerminalAt(context.Background(), &handler,
		SourceCrawlOutcomeCompleted, claim.ClaimedAt.Add(2*time.Second), claim.ClaimedAt.Add(2*time.Second))
	if err != nil || terminal.Result != SourceCrawlResultApplied || terminal.SourceStatusChanged {
		t.Fatalf("unbound terminal = %+v err=%v", terminal, err)
	}
	if operations := sourceCrawlStatusOperationIDs(t, raw, 42); len(operations) != 0 {
		t.Fatalf("unbound handoff created Status rows: %v", operations)
	}
	var sourceState string
	if err := raw.QueryRow(`SELECT status FROM Sources WHERE source_id=42`).Scan(&sourceState); err != nil {
		t.Fatalf("read unbound source status: %v", err)
	}
	if sourceState != "processing" {
		t.Fatalf("unbound source status = %s, want processing (suppressed)", sourceState)
	}
	replay, err := handoff.reportTerminalAt(context.Background(), &handler,
		SourceCrawlOutcomeCompleted, claim.ClaimedAt.Add(2*time.Second),
		claim.LeaseExpiresAt.Add(time.Hour))
	if err != nil || replay.Result != SourceCrawlResultIdempotentReplay || replay.SourceStatusChanged {
		t.Fatalf("unbound terminal replay = %+v err=%v", replay, err)
	}
}

func TestSourceCrawlHandoffSQLiteRejectsInvalidInput(t *testing.T) {
	raw, handler := openAsyncGoalSQLite(t)
	claim, _ := sourceCrawlResultClaim(t, raw, &handler, 42, "runtime-invalid", true)
	handoff, err := NewSourceCrawlHandoff(claim)
	if err != nil {
		t.Fatalf("new handoff: %v", err)
	}
	if got, err := handoff.ReportTerminal(context.Background(), &handler,
		SourceCrawlTerminalOutcome("other"), claim.ClaimedAt.Add(time.Second)); err != nil ||
		got.Result != SourceCrawlResultConflict {
		t.Fatalf("invalid outcome = %+v err=%v", got, err)
	}
	// A stale claim cannot become a handoff.
	stale := claim
	stale.Outcome = SourceCrawlClaimStaleFence
	if _, err := NewSourceCrawlHandoff(stale); err == nil {
		t.Fatal("stale claim became a handoff")
	}
}

func TestSourceCrawlHandoffSQLiteLegacyMode(t *testing.T) {
	raw, handler := openAsyncGoalSQLite(t)
	sourceCrawlResultSeedSource(t, raw, 42, 7, "runtime-legacy")
	claim := SourceCrawlClaim{
		Outcome: SourceCrawlUnattributed, SourceID: 42,
		ClaimRequestID: sourceCrawlClaimTestID("legacy-request"),
		AttemptID:      sourceCrawlClaimTestID("legacy-attempt"),
		Generation:     1, EngineID: "engine",
		ClaimedAt: sourceCrawlClaimTestNow, LeaseExpiresAt: sourceCrawlClaimTestNow.Add(time.Minute),
	}
	handoff, err := NewSourceCrawlHandoff(claim)
	if err != nil {
		t.Fatalf("legacy handoff: %v", err)
	}
	result, err := handoff.RenewLease(context.Background(), &handler, time.Minute)
	if err != nil || result.Result != SourceCrawlResultUnsupported {
		t.Fatalf("legacy renewal = %+v err=%v", result, err)
	}
	terminal, err := handoff.ReportTerminal(context.Background(), &handler,
		SourceCrawlOutcomeCompleted, sourceCrawlClaimTestNow.Add(time.Second))
	if err != nil || terminal.Result != SourceCrawlResultUnsupported {
		t.Fatalf("legacy terminal = %+v err=%v", terminal, err)
	}
}
