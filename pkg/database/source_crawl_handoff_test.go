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
	"errors"
	"strings"
	"testing"
	"time"
)

var sourceCrawlHandoffTestNow = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

func sourceCrawlHandoffTestBinding(sourceID uint64, label string) sourceCrawlOperationBinding {
	requestID := sourceCrawlClaimTestID("handoff-request-" + label)
	return sourceCrawlOperationBinding{
		OperationID:     CrawlOperationKey("owner-handoff", "agent-handoff", sourceID, string(CrawlOperationType), requestID),
		RequestID:       requestID,
		RunID:           "run-handoff-" + label,
		AgentID:         "agent-handoff",
		Owner:           "owner-handoff",
		PrincipalUserID: 7,
		ApprovalID:      sourceCrawlClaimTestID("handoff-approval-" + label),
		ApprovalSHA256:  sourceCrawlClaimTestID("handoff-approval-sha-" + label),
	}
}

func sourceCrawlHandoffTestAttempt(sourceID uint64, binding *sourceCrawlOperationBinding) sourceCrawlAttempt {
	return sourceCrawlAttempt{
		AttemptID:      sourceCrawlClaimTestID("handoff-attempt"),
		ClaimRequestID: sourceCrawlClaimTestID("handoff-claim-request"),
		SourceID:       sourceID,
		Generation:     3,
		EngineID:       "engine-handoff",
		State:          sourceCrawlAttemptClaimed,
		ClaimedAt:      sourceCrawlHandoffTestNow.Add(-time.Minute),
		LeaseExpiresAt: sourceCrawlHandoffTestNow.Add(9 * time.Minute),
		Binding:        binding,
	}
}

func sourceCrawlHandoffTestFixture(t *testing.T) (sourceCrawlCrawlerHandoff, sourceCrawlTerminalReceipt) {
	t.Helper()
	binding := sourceCrawlHandoffTestBinding(42, "fixture")
	attempt := sourceCrawlHandoffTestAttempt(42, &binding)
	handoff := sourceCrawlCrawlerHandoff{Attempt: attempt, Attribution: sourceCrawlHandoffBound}
	receipt := sourceCrawlTerminalReceipt{
		Attempt:     attempt,
		Attribution: sourceCrawlHandoffBound,
		Outcome:     sourceCrawlTerminalCompletedOutcome,
		ReasonCode:  "crawl_completed",
		ObservedAt:  sourceCrawlHandoffTestNow,
		OperationResult: &sourceCrawlTerminalOperationResult{
			Binding:       binding,
			ExpectedState: CrawlOperationAccepted,
			ResultState:   CrawlOperationCompleted,
		},
	}
	return handoff, receipt
}

func TestSourceCrawlHandoffFromClaim(t *testing.T) {
	binding := sourceCrawlHandoffTestBinding(42, "claim")
	base := SourceCrawlClaim{
		Outcome: SourceCrawlClaimed, SourceID: 42,
		ClaimRequestID: binding.RequestID, AttemptID: sourceCrawlClaimTestID("claim-attempt"),
		Generation: 2, EngineID: "engine-claim",
		OperationID: binding.OperationID, RequestID: binding.RequestID,
		RunID: binding.RunID, AgentID: binding.AgentID, Owner: binding.Owner,
		PrincipalUserID: binding.PrincipalUserID,
		ApprovalID:      binding.ApprovalID, ApprovalSHA256: binding.ApprovalSHA256,
		ClaimedAt: sourceCrawlHandoffTestNow.Add(-time.Minute), LeaseExpiresAt: sourceCrawlHandoffTestNow.Add(time.Minute),
	}
	handoff, err := newSourceCrawlCrawlerHandoff(base)
	if err != nil {
		t.Fatalf("bound handoff: %v", err)
	}
	if handoff.Attribution != sourceCrawlHandoffBound || handoff.Attempt.Binding == nil ||
		handoff.Attempt.Binding.OperationID != binding.OperationID {
		t.Fatalf("bound handoff = %+v", handoff)
	}
	if err := handoff.validate(sourceCrawlHandoffTestNow); err != nil {
		t.Fatalf("bound handoff validation: %v", err)
	}

	for _, outcome := range []SourceCrawlClaimOutcome{
		SourceCrawlUnattributed, SourceCrawlAmbiguous, SourceCrawlNotAttributable,
	} {
		unbound := base
		unbound.Outcome = outcome
		unbound.OperationID, unbound.RequestID, unbound.RunID = "", "", ""
		unbound.AgentID, unbound.Owner, unbound.ApprovalID, unbound.ApprovalSHA256 = "", "", "", ""
		unbound.PrincipalUserID = 0
		handoff, err := newSourceCrawlCrawlerHandoff(unbound)
		if err != nil {
			t.Fatalf("handoff for %s: %v", outcome, err)
		}
		if handoff.Attempt.Binding != nil {
			t.Fatalf("handoff for %s fabricated a binding", outcome)
		}
		if err := handoff.validate(sourceCrawlHandoffTestNow); err != nil {
			t.Fatalf("handoff validation for %s: %v", outcome, err)
		}
	}

	unboundWithOperation := base
	unboundWithOperation.Outcome = SourceCrawlUnattributed
	if _, err := newSourceCrawlCrawlerHandoff(unboundWithOperation); !errors.Is(err, errSourceCrawlAttemptInvalid) {
		t.Fatalf("unbound claim with operation error = %v", err)
	}

	for _, outcome := range []SourceCrawlClaimOutcome{SourceCrawlClaimStaleFence, SourceCrawlClaimConflict} {
		stale := base
		stale.Outcome = outcome
		if _, err := newSourceCrawlCrawlerHandoff(stale); !errors.Is(err, errSourceCrawlAttemptStale) {
			t.Fatalf("stale claim %s error = %v", outcome, err)
		}
	}
}

func TestSourceCrawlHandoffValidation(t *testing.T) {
	handoff, _ := sourceCrawlHandoffTestFixture(t)
	binding := *handoff.Attempt.Binding
	cases := []struct {
		name    string
		mutate  func(*sourceCrawlCrawlerHandoff)
		now     time.Time
		wantErr error
	}{
		{"wrong source", func(h *sourceCrawlCrawlerHandoff) { h.Attempt.SourceID = 0 }, sourceCrawlHandoffTestNow, errSourceCrawlAttemptInvalid},
		{"missing engine", func(h *sourceCrawlCrawlerHandoff) { h.Attempt.EngineID = "" }, sourceCrawlHandoffTestNow, errSourceCrawlAttemptInvalid},
		{"bad attempt id", func(h *sourceCrawlCrawlerHandoff) { h.Attempt.AttemptID = "bad" }, sourceCrawlHandoffTestNow, errSourceCrawlAttemptInvalid},
		{"zero generation", func(h *sourceCrawlCrawlerHandoff) { h.Attempt.Generation = 0 }, sourceCrawlHandoffTestNow, errSourceCrawlAttemptInvalid},
		{"bad claim request", func(h *sourceCrawlCrawlerHandoff) { h.Attempt.ClaimRequestID = "bad" }, sourceCrawlHandoffTestNow, errSourceCrawlAttemptInvalid},
		{"expired lease", func(h *sourceCrawlCrawlerHandoff) {}, sourceCrawlHandoffTestNow.Add(9 * time.Minute), errSourceCrawlAttemptStale},
		{"clock before claim", func(h *sourceCrawlCrawlerHandoff) {}, sourceCrawlHandoffTestNow.Add(-2 * time.Minute), errSourceCrawlAttemptInvalid},
		{"bound without binding", func(h *sourceCrawlCrawlerHandoff) { h.Attempt.Binding = nil }, sourceCrawlHandoffTestNow, errSourceCrawlAttemptInvalid},
		{"unbound with binding", func(h *sourceCrawlCrawlerHandoff) {
			h.Attribution = sourceCrawlHandoffUnattributed
			h.Attempt.Binding = &binding
		}, sourceCrawlHandoffTestNow, errSourceCrawlAttemptInvalid},
		{"deterministic key mismatch", func(h *sourceCrawlCrawlerHandoff) {
			h.Attempt.Binding.OperationID = sourceCrawlClaimTestID("wrong-operation")
		}, sourceCrawlHandoffTestNow, errSourceCrawlAttemptInvalid},
		{"unknown attribution", func(h *sourceCrawlCrawlerHandoff) { h.Attribution = "other" }, sourceCrawlHandoffTestNow, errSourceCrawlAttemptInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			subject := handoff
			copiedBinding := binding
			subject.Attempt.Binding = &copiedBinding
			tc.mutate(&subject)
			err := subject.validate(tc.now)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("validation error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestSourceCrawlHandoffLeaseExtension(t *testing.T) {
	handoff, _ := sourceCrawlHandoffTestFixture(t)
	extended, err := extendSourceCrawlHandoffLease(handoff, sourceCrawlHandoffTestNow, 15*time.Minute)
	if err != nil {
		t.Fatalf("extend lease: %v", err)
	}
	if want := sourceCrawlHandoffTestNow.Add(15 * time.Minute); !extended.Attempt.LeaseExpiresAt.Equal(want) {
		t.Fatalf("extended lease = %v, want %v", extended.Attempt.LeaseExpiresAt, want)
	}
	if !extended.Attempt.ClaimedAt.Equal(handoff.Attempt.ClaimedAt) {
		t.Fatal("lease extension changed the immutable claim time")
	}
	noop, err := extendSourceCrawlHandoffLease(handoff, sourceCrawlHandoffTestNow, 5*time.Minute)
	if err != nil || !noop.Attempt.LeaseExpiresAt.Equal(handoff.Attempt.LeaseExpiresAt) {
		t.Fatalf("short extension should not shorten the lease: %+v err=%v", noop.Attempt.LeaseExpiresAt, err)
	}
	cappedHandoff := handoff
	cappedHandoff.Attempt.ClaimedAt = sourceCrawlHandoffTestNow.Add(-23*time.Hour - 30*time.Minute)
	cappedHandoff.Attempt.LeaseExpiresAt = sourceCrawlHandoffTestNow.Add(10 * time.Minute)
	capped, err := extendSourceCrawlHandoffLease(cappedHandoff, sourceCrawlHandoffTestNow, time.Hour)
	if err != nil {
		t.Fatalf("capped extension: %v", err)
	}
	if want := cappedHandoff.Attempt.ClaimedAt.Add(sourceCrawlHandoffMaxLifetime); !capped.Attempt.LeaseExpiresAt.Equal(want) {
		t.Fatalf("capped lease = %v, want %v", capped.Attempt.LeaseExpiresAt, want)
	}
	for _, extension := range []time.Duration{0, -time.Minute, sourceCrawlHandoffMaxLeaseExtension + time.Nanosecond} {
		if _, err := extendSourceCrawlHandoffLease(handoff, sourceCrawlHandoffTestNow, extension); !errors.Is(err, errSourceCrawlAttemptInvalid) {
			t.Fatalf("extension %v error = %v", extension, err)
		}
	}
	if _, err := extendSourceCrawlHandoffLease(handoff, sourceCrawlHandoffTestNow.Add(9*time.Minute), time.Minute); !errors.Is(err, errSourceCrawlAttemptStale) {
		t.Fatalf("expired extension error = %v", err)
	}
}

func TestSourceCrawlTerminalReceiptValidation(t *testing.T) {
	_, receipt := sourceCrawlHandoffTestFixture(t)
	if err := receipt.validate(sourceCrawlHandoffTestNow); err != nil {
		t.Fatalf("valid bound receipt: %v", err)
	}
	unbound := receipt
	unbound.Attribution = sourceCrawlHandoffUnattributed
	unbound.Attempt.Binding = nil
	unbound.OperationResult = nil
	if err := unbound.validate(sourceCrawlHandoffTestNow); err != nil {
		t.Fatalf("valid unbound receipt: %v", err)
	}

	boundNoResult := receipt
	boundNoResult.OperationResult = nil
	unboundWithResult := unbound
	unboundWithResult.OperationResult = receipt.OperationResult

	cases := []struct {
		name   string
		mutate func(*sourceCrawlTerminalReceipt)
		now    time.Time
	}{
		{"bound without result", func(r *sourceCrawlTerminalReceipt) { *r = boundNoResult }, sourceCrawlHandoffTestNow},
		{"unbound with result", func(r *sourceCrawlTerminalReceipt) { *r = unboundWithResult }, sourceCrawlHandoffTestNow},
		{"wrong operation", func(r *sourceCrawlTerminalReceipt) {
			r.OperationResult.Binding.OperationID = sourceCrawlClaimTestID("other-operation")
		}, sourceCrawlHandoffTestNow},
		{"wrong run", func(r *sourceCrawlTerminalReceipt) { r.OperationResult.Binding.RunID = "other-run" }, sourceCrawlHandoffTestNow},
		{"wrong owner", func(r *sourceCrawlTerminalReceipt) { r.OperationResult.Binding.Owner = "other-owner" }, sourceCrawlHandoffTestNow},
		{"wrong principal", func(r *sourceCrawlTerminalReceipt) { r.OperationResult.Binding.PrincipalUserID = 8 }, sourceCrawlHandoffTestNow},
		{"wrong approval", func(r *sourceCrawlTerminalReceipt) {
			r.OperationResult.Binding.ApprovalID = sourceCrawlClaimTestID("other-approval")
		}, sourceCrawlHandoffTestNow},
		{"already pending is not ownership", func(r *sourceCrawlTerminalReceipt) {
			r.OperationResult.ExpectedState = CrawlOperationAlreadyPending
		}, sourceCrawlHandoffTestNow},
		{"terminal expected state", func(r *sourceCrawlTerminalReceipt) {
			r.OperationResult.ExpectedState = CrawlOperationCompleted
		}, sourceCrawlHandoffTestNow},
		{"non terminal result", func(r *sourceCrawlTerminalReceipt) {
			r.OperationResult.ResultState = CrawlOperationAccepted
		}, sourceCrawlHandoffTestNow},
		{"outcome and result disagree", func(r *sourceCrawlTerminalReceipt) {
			r.OperationResult.ResultState = CrawlOperationFailed
		}, sourceCrawlHandoffTestNow},
		{"wrong reason", func(r *sourceCrawlTerminalReceipt) { r.ReasonCode = "crawl_failed" }, sourceCrawlHandoffTestNow},
		{"oversized reason", func(r *sourceCrawlTerminalReceipt) {
			r.ReasonCode = "crawl_" + strings.Repeat("a", 100)
		}, sourceCrawlHandoffTestNow},
		{"future observation", func(r *sourceCrawlTerminalReceipt) {
			r.ObservedAt = sourceCrawlHandoffTestNow.Add(time.Minute)
		}, sourceCrawlHandoffTestNow},
		{"observation before claim", func(r *sourceCrawlTerminalReceipt) {
			r.ObservedAt = r.Attempt.ClaimedAt.Add(-time.Second)
		}, sourceCrawlHandoffTestNow},
		{"observation at lease expiry", func(r *sourceCrawlTerminalReceipt) {
			r.Attempt.ClaimedAt = sourceCrawlHandoffTestNow.Add(-2 * time.Hour)
			r.Attempt.LeaseExpiresAt = sourceCrawlHandoffTestNow
			r.ObservedAt = sourceCrawlHandoffTestNow
		}, sourceCrawlHandoffTestNow},
		{"binding lost from attempt", func(r *sourceCrawlTerminalReceipt) {
			r.Attempt.Binding = nil
		}, sourceCrawlHandoffTestNow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			subject := receipt
			resultCopy := *receipt.OperationResult
			bindingCopy := resultCopy.Binding
			resultCopy.Binding = bindingCopy
			subject.OperationResult = &resultCopy
			attemptBinding := bindingCopy
			subject.Attempt.Binding = &attemptBinding
			tc.mutate(&subject)
			if err := subject.validate(tc.now); err == nil {
				t.Fatal("invalid terminal receipt was accepted")
			}
		})
	}
}

func TestSourceCrawlTerminalAuthority(t *testing.T) {
	_, receipt := sourceCrawlHandoffTestFixture(t)
	authority := sourceCrawlTerminalAuthority{
		Fenced: true, CurrentAttemptID: receipt.Attempt.AttemptID,
		CurrentGeneration:        receipt.Attempt.Generation,
		CurrentClaimRequestID:    receipt.Attempt.ClaimRequestID,
		CurrentEngineID:          receipt.Attempt.EngineID,
		AttemptState:             sourceCrawlAttemptClaimed,
		CurrentSourceOwnerUserID: receipt.Attempt.Binding.PrincipalUserID,
	}
	if err := authority.validate(receipt, sourceCrawlHandoffTestNow); err != nil {
		t.Fatalf("valid authority: %v", err)
	}
	if !authority.sourceCrawlTerminalSourceStatusEligible(receipt) {
		t.Fatal("valid authority is not eligible for source status")
	}

	cases := []struct {
		name    string
		mutate  func(*sourceCrawlTerminalAuthority)
		wantErr error
	}{
		{"not fenced", func(a *sourceCrawlTerminalAuthority) { a.Fenced = false }, errSourceCrawlTerminalUnsupported},
		{"not current attempt", func(a *sourceCrawlTerminalAuthority) { a.CurrentAttemptID = sourceCrawlClaimTestID("other") }, errSourceCrawlAttemptStale},
		{"wrong generation", func(a *sourceCrawlTerminalAuthority) { a.CurrentGeneration++ }, errSourceCrawlAttemptStale},
		{"wrong claim request", func(a *sourceCrawlTerminalAuthority) { a.CurrentClaimRequestID = sourceCrawlClaimTestID("other") }, errSourceCrawlAttemptStale},
		{"wrong engine", func(a *sourceCrawlTerminalAuthority) { a.CurrentEngineID = "other-engine" }, errSourceCrawlAttemptStale},
		{"terminal attempt", func(a *sourceCrawlTerminalAuthority) { a.AttemptState = sourceCrawlAttemptCompleted }, errSourceCrawlAttemptStale},
		{"deleted source", func(a *sourceCrawlTerminalAuthority) { a.SourceDeleted = true }, errSourceCrawlAttemptStale},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			subject := authority
			tc.mutate(&subject)
			if err := subject.validate(receipt, sourceCrawlHandoffTestNow); !errors.Is(err, tc.wantErr) {
				t.Fatalf("authority error = %v, want %v", err, tc.wantErr)
			}
		})
	}

	disabled := authority
	disabled.SourceDisabled = true
	if err := disabled.validate(receipt, sourceCrawlHandoffTestNow); err != nil {
		t.Fatalf("disabled source operation result should still be recordable: %v", err)
	}
	if disabled.sourceCrawlTerminalSourceStatusEligible(receipt) {
		t.Fatal("disabled source must not receive a source-level status update")
	}

	unknownOwner := authority
	unknownOwner.CurrentSourceOwnerUserID = 0
	if unknownOwner.sourceCrawlTerminalSourceStatusEligible(receipt) {
		t.Fatal("unknown source ownership must not receive a source-level status update")
	}

	transferred := authority
	transferred.CurrentSourceOwnerUserID++
	if err := transferred.validate(receipt, sourceCrawlHandoffTestNow); err != nil {
		t.Fatalf("ownership transfer should not rewrite historical operation identity: %v", err)
	}
	if transferred.sourceCrawlTerminalSourceStatusEligible(receipt) {
		t.Fatal("transferred source must not receive the old worker's source-level status")
	}

	revoked := authority
	revoked.ApprovalRevokedBeforeAcceptance = true
	if err := revoked.validate(receipt, sourceCrawlHandoffTestNow); !errors.Is(err, errSourceCrawlTerminalConflict) {
		t.Fatalf("approval revoked before acceptance error = %v", err)
	}
}

func copySourceCrawlTerminalReceipt(receipt sourceCrawlTerminalReceipt) sourceCrawlTerminalReceipt {
	if receipt.OperationResult != nil {
		resultCopy := *receipt.OperationResult
		receipt.OperationResult = &resultCopy
	}
	return receipt
}

func TestSourceCrawlTerminalReplayClassification(t *testing.T) {
	_, receipt := sourceCrawlHandoffTestFixture(t)
	if got := classifySourceCrawlTerminalReplay(nil, receipt); got != sourceCrawlTerminalConflict {
		t.Fatalf("missing stored receipt = %q", got)
	}
	retry := copySourceCrawlTerminalReceipt(receipt)
	retry.ObservedAt = sourceCrawlHandoffTestNow.Add(30 * time.Second)
	if got := classifySourceCrawlTerminalReplay(&receipt, retry); got != sourceCrawlTerminalIdempotentReplay {
		t.Fatalf("identical retry = %q", got)
	}
	changedOutcome := copySourceCrawlTerminalReceipt(receipt)
	changedOutcome.Outcome = sourceCrawlTerminalFailedOutcome
	changedOutcome.ReasonCode = "crawl_failed"
	changedOutcome.OperationResult.ResultState = CrawlOperationFailed
	if got := classifySourceCrawlTerminalReplay(&receipt, changedOutcome); got != sourceCrawlTerminalConflict {
		t.Fatalf("changed outcome = %q", got)
	}
	changedResult := copySourceCrawlTerminalReceipt(receipt)
	changedResult.OperationResult.ResultState = CrawlOperationFailed
	if got := classifySourceCrawlTerminalReplay(&receipt, changedResult); got != sourceCrawlTerminalConflict {
		t.Fatalf("changed result = %q", got)
	}
	changedGeneration := copySourceCrawlTerminalReceipt(receipt)
	changedGeneration.Attempt.Generation++
	if got := classifySourceCrawlTerminalReplay(&receipt, changedGeneration); got != sourceCrawlTerminalConflict {
		t.Fatalf("changed generation = %q", got)
	}
}

func TestSourceCrawlTerminalStateTables(t *testing.T) {
	for _, outcome := range []sourceCrawlTerminalOutcome{
		sourceCrawlTerminalCompletedOutcome, sourceCrawlTerminalFailedOutcome,
		sourceCrawlTerminalCancelledOutcome,
	} {
		if !sourceCrawlAttemptTerminalTransitionAllowed(sourceCrawlAttemptClaimed, outcome) {
			t.Fatalf("claimed attempt cannot transition to %s", outcome)
		}
	}
	for _, state := range []sourceCrawlAttemptState{
		sourceCrawlAttemptSuperseded, sourceCrawlAttemptExpired, sourceCrawlAttemptAbandoned,
		sourceCrawlAttemptCompleted, sourceCrawlAttemptFailed, sourceCrawlAttemptCancelled,
	} {
		if sourceCrawlAttemptTerminalTransitionAllowed(state, sourceCrawlTerminalCompletedOutcome) {
			t.Fatalf("terminal attempt state %s accepted a new terminal transition", state)
		}
	}
	if sourceCrawlAttemptTerminalTransitionAllowed(sourceCrawlAttemptClaimed, "other") {
		t.Fatal("unknown terminal outcome accepted")
	}
	for _, expected := range []CrawlOperationState{CrawlOperationAccepted, CrawlOperationProcessing} {
		for _, result := range []CrawlOperationState{
			CrawlOperationCompleted, CrawlOperationFailed, CrawlOperationCancelled,
		} {
			if !sourceCrawlTerminalOperationTransitionAllowed(expected, result) {
				t.Fatalf("operation transition %s -> %s rejected", expected, result)
			}
		}
	}
	for _, expected := range []CrawlOperationState{
		CrawlOperationAlreadyPending, CrawlOperationCompleted, CrawlOperationDenied,
		CrawlOperationExpired, CrawlOperationUncertain,
	} {
		if sourceCrawlTerminalOperationTransitionAllowed(expected, CrawlOperationCompleted) {
			t.Fatalf("ineligible operation state %s accepted", expected)
		}
	}
	for _, result := range []CrawlOperationState{
		CrawlOperationAccepted, CrawlOperationProcessing, CrawlOperationExpired,
		CrawlOperationUncertain, CrawlOperationDenied,
	} {
		if sourceCrawlTerminalOperationTransitionAllowed(CrawlOperationAccepted, result) {
			t.Fatalf("non-terminal operation result %s accepted", result)
		}
	}
}

func TestSourceCrawlClaimRequestFingerprint(t *testing.T) {
	base := SourceCrawlClaimRequest{
		ClaimRequestID:    sourceCrawlClaimTestID("fingerprint"),
		Limit:             5,
		Priority:          "High, normal",
		EngineID:          "engine-fingerprint",
		Lease:             10 * time.Minute,
		LastOKUpdate:      time.Minute,
		LastErrorInterval: time.Hour,
		RegularCrawling:   time.Hour,
		ProcessingTimeout: 30 * time.Minute,
	}
	first, err := sourceCrawlClaimRequestFingerprint(base)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	second, err := sourceCrawlClaimRequestFingerprint(base)
	if err != nil || first != second || len(first) != 64 {
		t.Fatalf("fingerprint is not deterministic: %q %q err=%v", first, second, err)
	}
	normalized := base
	normalized.Priority = "  high, NORMAL "
	if got, err := sourceCrawlClaimRequestFingerprint(normalized); err != nil || got != first {
		t.Fatalf("priority normalization changed fingerprint: %q err=%v", got, err)
	}
	changes := []struct {
		name   string
		mutate func(*SourceCrawlClaimRequest)
	}{
		{"limit", func(r *SourceCrawlClaimRequest) { r.Limit++ }},
		{"priority", func(r *SourceCrawlClaimRequest) { r.Priority = "low" }},
		{"engine", func(r *SourceCrawlClaimRequest) { r.EngineID = "other-engine" }},
		{"lease", func(r *SourceCrawlClaimRequest) { r.Lease += time.Second }},
		{"last ok", func(r *SourceCrawlClaimRequest) { r.LastOKUpdate += time.Microsecond }},
		{"last error", func(r *SourceCrawlClaimRequest) { r.LastErrorInterval += time.Microsecond }},
		{"regular", func(r *SourceCrawlClaimRequest) { r.RegularCrawling += time.Microsecond }},
		{"processing", func(r *SourceCrawlClaimRequest) { r.ProcessingTimeout += time.Microsecond }},
	}
	for _, change := range changes {
		t.Run(change.name, func(t *testing.T) {
			changed := base
			change.mutate(&changed)
			digest, err := sourceCrawlClaimRequestFingerprint(changed)
			if err != nil {
				t.Fatalf("changed fingerprint: %v", err)
			}
			if digest == first {
				t.Fatal("changed request field did not change the fingerprint")
			}
		})
	}
	invalid := base
	invalid.EngineID = ""
	if _, err := sourceCrawlClaimRequestFingerprint(invalid); !errors.Is(err, ErrSourceCrawlClaimInvalidRequest) {
		t.Fatalf("invalid request fingerprint error = %v", err)
	}
}
