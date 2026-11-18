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

	for _, field := range []struct {
		name   string
		mutate func(*SourceCrawlClaim)
	}{
		{"operation id", func(c *SourceCrawlClaim) { c.OperationID = sourceCrawlClaimTestID("leak-operation") }},
		{"request id", func(c *SourceCrawlClaim) { c.RequestID = sourceCrawlClaimTestID("leak-request") }},
		{"run id", func(c *SourceCrawlClaim) { c.RunID = "leak-run" }},
		{"agent id", func(c *SourceCrawlClaim) { c.AgentID = "leak-agent" }},
		{"owner", func(c *SourceCrawlClaim) { c.Owner = "leak-owner" }},
		{"principal", func(c *SourceCrawlClaim) { c.PrincipalUserID = 99 }},
		{"approval id", func(c *SourceCrawlClaim) { c.ApprovalID = sourceCrawlClaimTestID("leak-approval") }},
		{"approval hash", func(c *SourceCrawlClaim) { c.ApprovalSHA256 = sourceCrawlClaimTestID("leak-approval-sha") }},
	} {
		t.Run("unbound "+field.name, func(t *testing.T) {
			unboundWithOperation := base
			unboundWithOperation.Outcome = SourceCrawlUnattributed
			unboundWithOperation.OperationID, unboundWithOperation.RequestID = "", ""
			unboundWithOperation.RunID, unboundWithOperation.AgentID, unboundWithOperation.Owner = "", "", ""
			unboundWithOperation.PrincipalUserID = 0
			unboundWithOperation.ApprovalID, unboundWithOperation.ApprovalSHA256 = "", ""
			field.mutate(&unboundWithOperation)
			if _, err := newSourceCrawlCrawlerHandoff(unboundWithOperation); !errors.Is(err, errSourceCrawlAttemptInvalid) {
				t.Fatalf("unbound claim with %s error = %v", field.name, err)
			}
		})
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

func TestSourceCrawlTerminalReceiptObservationTimeStructure(t *testing.T) {
	_, receipt := sourceCrawlHandoffTestFixture(t)
	receipt.Attempt.ClaimedAt = sourceCrawlHandoffTestNow.Add(-time.Hour)
	receipt.Attempt.LeaseExpiresAt = sourceCrawlHandoffTestNow.Add(time.Hour)

	cases := []struct {
		name       string
		observedAt time.Time
		wantErr    bool
	}{
		{"inside lease", sourceCrawlHandoffTestNow, false},
		{"exactly at claim", receipt.Attempt.ClaimedAt, false},
		{"just before lease expiry", receipt.Attempt.LeaseExpiresAt.Add(-time.Microsecond), false},
		{"before claim", receipt.Attempt.ClaimedAt.Add(-time.Microsecond), true},
		{"exactly at lease expiry", receipt.Attempt.LeaseExpiresAt, true},
		{"after lease expiry", receipt.Attempt.LeaseExpiresAt.Add(time.Microsecond), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			subject := copySourceCrawlTerminalReceipt(receipt)
			subject.ObservedAt = tc.observedAt
			err := subject.validateStructure()
			if tc.wantErr && err == nil {
				t.Fatal("structurally invalid observation was accepted")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("valid in-lease observation rejected: %v", err)
			}
			// The structural rule compares receipt-carried values only. It must
			// remain valid long after the live lease has expired so an exact
			// historical result can be replayed read-only.
			if !tc.wantErr {
				if err := subject.validate(sourceCrawlHandoffTestNow.Add(24 * time.Hour)); err == nil {
					t.Fatal("live validator accepted a receipt after lease expiry")
				}
			}
		})
	}
}

func sourceCrawlTerminalAuthorityFixture(receipt sourceCrawlTerminalReceipt) sourceCrawlTerminalAuthority {
	var ownerID uint64
	if receipt.Attempt.Binding != nil {
		ownerID = receipt.Attempt.Binding.PrincipalUserID
	}
	return sourceCrawlTerminalAuthority{
		Available: true, Fenced: true, Found: true,
		SourceID:                 receipt.Attempt.SourceID,
		AttemptID:                receipt.Attempt.AttemptID,
		Generation:               receipt.Attempt.Generation,
		ClaimRequestID:           receipt.Attempt.ClaimRequestID,
		EngineID:                 receipt.Attempt.EngineID,
		ClaimedAt:                receipt.Attempt.ClaimedAt,
		HistorySourceID:          receipt.Attempt.SourceID,
		HistoryAttemptID:         receipt.Attempt.AttemptID,
		HistoryGeneration:        receipt.Attempt.Generation,
		HistoryClaimRequestID:    receipt.Attempt.ClaimRequestID,
		HistoryEngineID:          receipt.Attempt.EngineID,
		HistoryClaimedAt:         receipt.Attempt.ClaimedAt,
		CurrentLeaseExpiresAt:    receipt.Attempt.LeaseExpiresAt,
		HistoryLeaseExpiresAt:    receipt.Attempt.LeaseExpiresAt,
		AttemptState:             sourceCrawlAttemptClaimed,
		Binding:                  receipt.Attempt.Binding,
		SourceOwnershipVerified:  true,
		CurrentSourceOwnerUserID: ownerID,
		ApprovalHistory:          sourceCrawlApprovalValidAtAcceptance,
	}
}

func TestSourceCrawlTerminalAuthorityClassification(t *testing.T) {
	_, receipt := sourceCrawlHandoffTestFixture(t)
	live := sourceCrawlTerminalAuthorityFixture(receipt)
	cases := []struct {
		name     string
		mutate   func(*sourceCrawlTerminalAuthority)
		now      time.Time
		expected sourceCrawlTerminalAuthorityState
	}{
		{"current live", func(*sourceCrawlTerminalAuthority) {}, sourceCrawlHandoffTestNow, sourceCrawlTerminalAuthorityCurrentLive},
		{"current expired at boundary", func(*sourceCrawlTerminalAuthority) {}, sourceCrawlHandoffTestNow.Add(9 * time.Minute), sourceCrawlTerminalAuthorityCurrentExpired},
		{"superseded", func(a *sourceCrawlTerminalAuthority) { a.AttemptState = sourceCrawlAttemptSuperseded }, sourceCrawlHandoffTestNow, sourceCrawlTerminalAuthoritySuperseded},
		{"terminal", func(a *sourceCrawlTerminalAuthority) { a.AttemptState = sourceCrawlAttemptCompleted }, sourceCrawlHandoffTestNow, sourceCrawlTerminalAuthorityTerminal},
		{"missing", func(a *sourceCrawlTerminalAuthority) { a.Found = false }, sourceCrawlHandoffTestNow, sourceCrawlTerminalAuthorityMissing},
		{"corrupt lease disagreement", func(a *sourceCrawlTerminalAuthority) {
			a.CurrentLeaseExpiresAt = a.HistoryLeaseExpiresAt.Add(time.Second)
		}, sourceCrawlHandoffTestNow, sourceCrawlTerminalAuthorityCorrupt},
		{"corrupt zero attempt", func(a *sourceCrawlTerminalAuthority) { a.AttemptID = "" }, sourceCrawlHandoffTestNow, sourceCrawlTerminalAuthorityCorrupt},
		{"corrupt deterministic binding", func(a *sourceCrawlTerminalAuthority) {
			binding := *a.Binding
			binding.OperationID = sourceCrawlClaimTestID("wrong-operation")
			a.Binding = &binding
		}, sourceCrawlHandoffTestNow, sourceCrawlTerminalAuthorityCorrupt},
		{"history source mismatch", func(a *sourceCrawlTerminalAuthority) { a.HistorySourceID++ }, sourceCrawlHandoffTestNow, sourceCrawlTerminalAuthorityCorrupt},
		{"history attempt mismatch", func(a *sourceCrawlTerminalAuthority) { a.HistoryAttemptID = sourceCrawlClaimTestID("other") }, sourceCrawlHandoffTestNow, sourceCrawlTerminalAuthorityCorrupt},
		{"history generation mismatch", func(a *sourceCrawlTerminalAuthority) { a.HistoryGeneration++ }, sourceCrawlHandoffTestNow, sourceCrawlTerminalAuthorityCorrupt},
		{"history claim request mismatch", func(a *sourceCrawlTerminalAuthority) { a.HistoryClaimRequestID = sourceCrawlClaimTestID("other") }, sourceCrawlHandoffTestNow, sourceCrawlTerminalAuthorityCorrupt},
		{"history engine mismatch", func(a *sourceCrawlTerminalAuthority) { a.HistoryEngineID = "other-engine" }, sourceCrawlHandoffTestNow, sourceCrawlTerminalAuthorityCorrupt},
		{"history claimed time mismatch", func(a *sourceCrawlTerminalAuthority) { a.HistoryClaimedAt = a.HistoryClaimedAt.Add(time.Microsecond) }, sourceCrawlHandoffTestNow, sourceCrawlTerminalAuthorityCorrupt},
		{"unavailable", func(a *sourceCrawlTerminalAuthority) { a.Available = false }, sourceCrawlHandoffTestNow, sourceCrawlTerminalAuthorityUnavailable},
		{"legacy mode", func(a *sourceCrawlTerminalAuthority) { a.Fenced = false }, sourceCrawlHandoffTestNow, sourceCrawlTerminalAuthorityLegacyMode},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			subject := live
			tc.mutate(&subject)
			if got := subject.classify(tc.now); got != tc.expected {
				t.Fatalf("classify = %q, want %q", got, tc.expected)
			}
		})
	}
	nilNow := live
	if got := nilNow.classify(time.Time{}); got != sourceCrawlTerminalAuthorityCorrupt {
		t.Fatalf("zero clock classify = %q", got)
	}
	var emptyAuthority sourceCrawlTerminalAuthority
	if got := emptyAuthority.classify(sourceCrawlHandoffTestNow); got != sourceCrawlTerminalAuthorityUnavailable {
		t.Fatalf("empty authority classify = %q", got)
	}
	if err := emptyAuthority.validate(receipt, sourceCrawlHandoffTestNow); !errors.Is(err, errSourceCrawlTerminalUnavailable) {
		t.Fatalf("empty authority validate = %v", err)
	}
	if emptyAuthority.sourceCrawlTerminalSourceStatusEligible(receipt, sourceCrawlHandoffTestNow) {
		t.Fatal("empty authority was eligible for a source status update")
	}
}

func TestSourceCrawlTerminalAuthority(t *testing.T) {
	_, receipt := sourceCrawlHandoffTestFixture(t)
	authority := sourceCrawlTerminalAuthorityFixture(receipt)
	if err := authority.validate(receipt, sourceCrawlHandoffTestNow); err != nil {
		t.Fatalf("valid authority: %v", err)
	}
	if !authority.sourceCrawlTerminalSourceStatusEligible(receipt, sourceCrawlHandoffTestNow) {
		t.Fatal("valid authority is not eligible for source status")
	}

	cases := []struct {
		name    string
		mutate  func(*sourceCrawlTerminalAuthority)
		wantErr error
	}{
		{"unavailable", func(a *sourceCrawlTerminalAuthority) { a.Available = false }, errSourceCrawlTerminalUnavailable},
		{"not fenced", func(a *sourceCrawlTerminalAuthority) { a.Fenced = false }, errSourceCrawlTerminalUnsupported},
		{"missing", func(a *sourceCrawlTerminalAuthority) { a.Found = false }, errSourceCrawlAttemptStale},
		{"corrupt lease", func(a *sourceCrawlTerminalAuthority) {
			a.CurrentLeaseExpiresAt = a.HistoryLeaseExpiresAt.Add(time.Second)
		}, errSourceCrawlTerminalCorrupt},
		{"expired", func(a *sourceCrawlTerminalAuthority) {}, errSourceCrawlAttemptStale},
		{"not current attempt", func(a *sourceCrawlTerminalAuthority) {
			a.AttemptID = sourceCrawlClaimTestID("other")
			a.HistoryAttemptID = a.AttemptID
		}, errSourceCrawlAttemptStale},
		{"wrong generation", func(a *sourceCrawlTerminalAuthority) {
			a.Generation++
			a.HistoryGeneration = a.Generation
		}, errSourceCrawlAttemptStale},
		{"wrong claim request", func(a *sourceCrawlTerminalAuthority) {
			a.ClaimRequestID = sourceCrawlClaimTestID("other")
			a.HistoryClaimRequestID = a.ClaimRequestID
		}, errSourceCrawlAttemptStale},
		{"wrong engine", func(a *sourceCrawlTerminalAuthority) {
			a.EngineID = "other-engine"
			a.HistoryEngineID = a.EngineID
		}, errSourceCrawlAttemptStale},
		{"terminal attempt", func(a *sourceCrawlTerminalAuthority) { a.AttemptState = sourceCrawlAttemptCompleted }, errSourceCrawlAttemptStale},
		{"deleted source", func(a *sourceCrawlTerminalAuthority) { a.SourceDeleted = true }, errSourceCrawlAttemptStale},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			subject := authority
			now := sourceCrawlHandoffTestNow
			if tc.name == "expired" {
				now = sourceCrawlHandoffTestNow.Add(9 * time.Minute)
			}
			tc.mutate(&subject)
			if err := subject.validate(receipt, now); !errors.Is(err, tc.wantErr) {
				t.Fatalf("authority error = %v, want %v", err, tc.wantErr)
			}
		})
	}

	expiredAuthority := authority
	expiredReceipt := copySourceCrawlTerminalReceipt(receipt)
	expiredReceipt.Attempt.LeaseExpiresAt = expiredReceipt.Attempt.LeaseExpiresAt.Add(time.Hour)
	if err := expiredAuthority.validate(expiredReceipt, sourceCrawlHandoffTestNow.Add(9*time.Minute)); !errors.Is(err, errSourceCrawlAttemptStale) {
		t.Fatalf("expired authority with forged later lease error = %v, want stale", err)
	}

	receiptCases := []struct {
		name   string
		mutate func(*sourceCrawlTerminalReceipt)
	}{
		{"forged later lease", func(r *sourceCrawlTerminalReceipt) {
			r.Attempt.LeaseExpiresAt = r.Attempt.LeaseExpiresAt.Add(time.Minute)
		}},
		{"forged claim time", func(r *sourceCrawlTerminalReceipt) { r.Attempt.ClaimedAt = r.Attempt.ClaimedAt.Add(-time.Minute) }},
		{"wrong receipt source", func(r *sourceCrawlTerminalReceipt) { r.Attempt.SourceID++ }},
		{"wrong receipt attempt", func(r *sourceCrawlTerminalReceipt) { r.Attempt.AttemptID = sourceCrawlClaimTestID("other") }},
		{"wrong receipt generation", func(r *sourceCrawlTerminalReceipt) { r.Attempt.Generation++ }},
		{"wrong receipt claim request", func(r *sourceCrawlTerminalReceipt) { r.Attempt.ClaimRequestID = sourceCrawlClaimTestID("other") }},
		{"wrong receipt engine", func(r *sourceCrawlTerminalReceipt) { r.Attempt.EngineID = "other-engine" }},
		{"wrong receipt binding", func(r *sourceCrawlTerminalReceipt) {
			binding := *r.Attempt.Binding
			binding.OperationID = sourceCrawlClaimTestID("other-operation")
			r.Attempt.Binding = &binding
		}},
	}
	for _, tc := range receiptCases {
		t.Run(tc.name, func(t *testing.T) {
			subject := copySourceCrawlTerminalReceipt(receipt)
			tc.mutate(&subject)
			want := errSourceCrawlTerminalConflict
			switch tc.name {
			case "wrong receipt source", "wrong receipt attempt", "wrong receipt generation",
				"wrong receipt claim request", "wrong receipt engine":
				want = errSourceCrawlAttemptStale
			}
			if err := authority.validate(subject, sourceCrawlHandoffTestNow); !errors.Is(err, want) {
				t.Fatalf("authority receipt error = %v, want %v", err, want)
			}
		})
	}

	disabled := authority
	disabled.SourceDisabled = true
	if err := disabled.validate(receipt, sourceCrawlHandoffTestNow); err != nil {
		t.Fatalf("disabled source operation result should still be recordable: %v", err)
	}
	if disabled.sourceCrawlTerminalSourceStatusEligible(receipt, sourceCrawlHandoffTestNow) {
		t.Fatal("disabled source must not receive a source-level status update")
	}

	unknownOwner := authority
	unknownOwner.CurrentSourceOwnerUserID = 0
	if unknownOwner.sourceCrawlTerminalSourceStatusEligible(receipt, sourceCrawlHandoffTestNow) {
		t.Fatal("unknown source ownership must not receive a source-level status update")
	}

	unverified := authority
	unverified.SourceOwnershipVerified = false
	if unverified.sourceCrawlTerminalSourceStatusEligible(receipt, sourceCrawlHandoffTestNow) {
		t.Fatal("unverified source ownership must not receive a source-level status update")
	}

	transferred := authority
	transferred.CurrentSourceOwnerUserID++
	if err := transferred.validate(receipt, sourceCrawlHandoffTestNow); err != nil {
		t.Fatalf("ownership transfer should not rewrite historical operation identity: %v", err)
	}
	if transferred.sourceCrawlTerminalSourceStatusEligible(receipt, sourceCrawlHandoffTestNow) {
		t.Fatal("transferred source must not receive the old worker's source-level status")
	}

	revoked := authority
	revoked.ApprovalHistory = sourceCrawlApprovalRevokedBeforeAcceptance
	if err := revoked.validate(receipt, sourceCrawlHandoffTestNow); !errors.Is(err, errSourceCrawlTerminalConflict) {
		t.Fatalf("approval revoked before acceptance error = %v", err)
	}
	if revoked.sourceCrawlTerminalSourceStatusEligible(receipt, sourceCrawlHandoffTestNow) {
		t.Fatal("revoked-before-acceptance authority must not update source status")
	}

	expired := authority
	if expired.sourceCrawlTerminalSourceStatusEligible(receipt, sourceCrawlHandoffTestNow.Add(9*time.Minute)) {
		t.Fatal("expired fence must not update source status")
	}
	missing := authority
	missing.Found = false
	if missing.sourceCrawlTerminalSourceStatusEligible(receipt, sourceCrawlHandoffTestNow) {
		t.Fatal("missing authority must not update source status")
	}
	corrupt := authority
	corrupt.HistoryEngineID = "other-engine"
	if corrupt.sourceCrawlTerminalSourceStatusEligible(receipt, sourceCrawlHandoffTestNow) {
		t.Fatal("corrupt authority must not update source status")
	}

	unboundReceipt := copySourceCrawlTerminalReceipt(receipt)
	unboundReceipt.Attempt.Binding = nil
	unboundReceipt.Attribution = sourceCrawlHandoffUnattributed
	unboundReceipt.OperationResult = nil
	unboundAuthority := sourceCrawlTerminalAuthorityFixture(unboundReceipt)
	unboundAuthority.SourceOwnershipVerified = true
	unboundAuthority.CurrentSourceOwnerUserID = 7
	if err := unboundAuthority.validate(unboundReceipt, sourceCrawlHandoffTestNow); err != nil {
		t.Fatalf("unbound historical result preflight: %v", err)
	}
	if unboundAuthority.sourceCrawlTerminalSourceStatusEligible(unboundReceipt, sourceCrawlHandoffTestNow) {
		t.Fatal("unbound attempt has no owner-continuity proof and must not update source status")
	}
	for _, history := range []sourceCrawlApprovalHistoryState{
		sourceCrawlApprovalRevokedAfterAcceptance, sourceCrawlApprovalExpiredAfterAcceptance,
	} {
		after := authority
		after.ApprovalHistory = history
		if err := after.validate(receipt, sourceCrawlHandoffTestNow); err != nil {
			t.Fatalf("historical acceptance should survive %s: %v", history, err)
		}
	}
}

func copySourceCrawlTerminalReceipt(receipt sourceCrawlTerminalReceipt) sourceCrawlTerminalReceipt {
	if receipt.OperationResult != nil {
		resultCopy := *receipt.OperationResult
		receipt.OperationResult = &resultCopy
	}
	if receipt.Attempt.Binding != nil {
		binding := *receipt.Attempt.Binding
		receipt.Attempt.Binding = &binding
	}
	return receipt
}

func sourceCrawlStoredTerminalFromReceipt(t *testing.T, receipt sourceCrawlTerminalReceipt,
	state sourceCrawlAttemptState, reason string, result CrawlOperationState) sourceCrawlPersistedTerminal {
	t.Helper()
	attempt := receipt.Attempt
	attempt.State = state
	attempt.TerminalAt = receipt.ObservedAt
	attempt.ReasonCode = reason
	stored, err := newSourceCrawlPersistedTerminal(attempt, result)
	if err != nil {
		t.Fatalf("construct persisted terminal: %v", err)
	}
	return stored
}

func TestSourceCrawlTerminalReplayClassification(t *testing.T) {
	_, receipt := sourceCrawlHandoffTestFixture(t)
	stored := sourceCrawlStoredTerminalFromReceipt(t, receipt,
		sourceCrawlAttemptCompleted, "crawl_completed", CrawlOperationCompleted)
	if got := classifySourceCrawlPersistedTerminalReplay(nil, receipt); got != sourceCrawlTerminalConflict {
		t.Fatalf("missing stored receipt = %q", got)
	}
	retry := copySourceCrawlTerminalReceipt(receipt)
	retry.ObservedAt = sourceCrawlHandoffTestNow.Add(30 * time.Second)
	if got := classifySourceCrawlPersistedTerminalReplay(&stored, retry); got != sourceCrawlTerminalIdempotentReplay {
		t.Fatalf("identical retry with changed observation time = %q", got)
	}
	changedExpected := copySourceCrawlTerminalReceipt(receipt)
	changedExpected.OperationResult.ExpectedState = CrawlOperationProcessing
	if got := classifySourceCrawlPersistedTerminalReplay(&stored, changedExpected); got != sourceCrawlTerminalIdempotentReplay {
		t.Fatalf("changed ExpectedState should be a precondition only = %q", got)
	}

	conflicts := []struct {
		name   string
		mutate func(*sourceCrawlTerminalReceipt)
	}{
		{"engine", func(r *sourceCrawlTerminalReceipt) { r.Attempt.EngineID = "other-engine" }},
		{"claim request", func(r *sourceCrawlTerminalReceipt) { r.Attempt.ClaimRequestID = sourceCrawlClaimTestID("other") }},
		{"source", func(r *sourceCrawlTerminalReceipt) { r.Attempt.SourceID++ }},
		{"generation", func(r *sourceCrawlTerminalReceipt) { r.Attempt.Generation++ }},
		{"attempt", func(r *sourceCrawlTerminalReceipt) { r.Attempt.AttemptID = sourceCrawlClaimTestID("other") }},
		{"claim time", func(r *sourceCrawlTerminalReceipt) { r.Attempt.ClaimedAt = r.Attempt.ClaimedAt.Add(-time.Minute) }},
		{"lease", func(r *sourceCrawlTerminalReceipt) {
			r.Attempt.LeaseExpiresAt = r.Attempt.LeaseExpiresAt.Add(time.Minute)
		}},
		{"binding", func(r *sourceCrawlTerminalReceipt) {
			r.Attempt.Binding.OperationID = sourceCrawlClaimTestID("other-operation")
			r.OperationResult.Binding = *r.Attempt.Binding
		}},
		{"outcome", func(r *sourceCrawlTerminalReceipt) {
			r.Outcome = sourceCrawlTerminalFailedOutcome
			r.ReasonCode = "crawl_failed"
			r.OperationResult.ResultState = CrawlOperationFailed
		}},
		{"reason", func(r *sourceCrawlTerminalReceipt) { r.ReasonCode = "crawl_failed" }},
		{"operation result", func(r *sourceCrawlTerminalReceipt) { r.OperationResult.ResultState = CrawlOperationFailed }},
		{"ineligible expected state", func(r *sourceCrawlTerminalReceipt) {
			r.OperationResult.ExpectedState = CrawlOperationAlreadyPending
		}},
		{"terminal attempt shape", func(r *sourceCrawlTerminalReceipt) {
			r.Attempt.State = sourceCrawlAttemptCompleted
			r.Attempt.TerminalAt = r.ObservedAt
		}},
		{"observation before claim", func(r *sourceCrawlTerminalReceipt) {
			r.ObservedAt = r.Attempt.ClaimedAt.Add(-time.Second)
		}},
		{"bound attribution without binding", func(r *sourceCrawlTerminalReceipt) {
			r.Attempt.Binding = nil
		}},
	}
	for _, tc := range conflicts {
		t.Run(tc.name, func(t *testing.T) {
			subject := copySourceCrawlTerminalReceipt(receipt)
			tc.mutate(&subject)
			if got := classifySourceCrawlPersistedTerminalReplay(&stored, subject); got != sourceCrawlTerminalConflict {
				t.Fatalf("changed %s = %q, want conflict", tc.name, got)
			}
		})
	}

	expiredStored := stored
	expiredStored.Attempt.ClaimedAt = sourceCrawlHandoffTestNow.Add(-2 * time.Hour)
	expiredStored.Attempt.LeaseExpiresAt = sourceCrawlHandoffTestNow.Add(-time.Hour)
	expiredStored.Attempt.TerminalAt = sourceCrawlHandoffTestNow.Add(-90 * time.Minute)
	expiredIncoming := copySourceCrawlTerminalReceipt(receipt)
	expiredIncoming.Attempt.ClaimedAt = expiredStored.Attempt.ClaimedAt
	expiredIncoming.Attempt.LeaseExpiresAt = expiredStored.Attempt.LeaseExpiresAt
	// The original observation must be plausible for the historical attempt,
	// but ObservedAt is not a replay-identity field: a different in-lease value
	// still replays the one persisted result.
	expiredIncoming.ObservedAt = expiredStored.Attempt.ClaimedAt.Add(20 * time.Minute)
	if got := classifySourceCrawlPersistedTerminalReplay(&expiredStored, expiredIncoming); got != sourceCrawlTerminalIdempotentReplay {
		t.Fatalf("post-expiry exact replay with in-lease observation = %q", got)
	}

	for _, tc := range []struct {
		name       string
		observedAt time.Time
	}{
		{"observation at original lease expiry", expiredStored.Attempt.LeaseExpiresAt},
		{"observation after original lease expiry", expiredStored.Attempt.LeaseExpiresAt.Add(time.Microsecond)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			subject := copySourceCrawlTerminalReceipt(expiredIncoming)
			subject.ObservedAt = tc.observedAt
			if got := classifySourceCrawlPersistedTerminalReplay(&expiredStored, subject); got != sourceCrawlTerminalConflict {
				t.Fatalf("%s = %q, want conflict", tc.name, got)
			}
		})
	}

	unboundAttempt := receipt.Attempt
	unboundAttempt.Binding = nil
	unboundAttempt.State = sourceCrawlAttemptCancelled
	unboundAttempt.TerminalAt = receipt.ObservedAt
	unboundAttempt.ReasonCode = "crawl_cancelled"
	unboundStored, err := newSourceCrawlPersistedTerminal(unboundAttempt, "")
	if err != nil {
		t.Fatalf("construct unbound persisted terminal: %v", err)
	}
	for _, class := range []sourceCrawlHandoffAttribution{
		sourceCrawlHandoffUnattributed, sourceCrawlHandoffAmbiguous, sourceCrawlHandoffNotAttributable,
	} {
		incoming := sourceCrawlTerminalReceipt{
			Attempt: unboundAttempt, Attribution: class,
			Outcome: sourceCrawlTerminalCancelledOutcome, ReasonCode: "crawl_cancelled",
			ObservedAt: receipt.ObservedAt,
		}
		incoming.Attempt.State = sourceCrawlAttemptClaimed
		incoming.Attempt.TerminalAt = time.Time{}
		incoming.Attempt.ReasonCode = ""
		if got := classifySourceCrawlPersistedTerminalReplay(&unboundStored, incoming); got != sourceCrawlTerminalIdempotentReplay {
			t.Fatalf("unbound %s replay = %q", class, got)
		}
	}
	boundIncoming := copySourceCrawlTerminalReceipt(receipt)
	if got := classifySourceCrawlPersistedTerminalReplay(&unboundStored, boundIncoming); got != sourceCrawlTerminalConflict {
		t.Fatalf("bound receipt against unbound history = %q", got)
	}

	corruptStored := stored
	corruptStored.OperationResult = CrawlOperationAccepted
	if got := classifySourceCrawlPersistedTerminalReplay(&corruptStored, receipt); got != sourceCrawlTerminalConflict {
		t.Fatalf("corrupt stored terminal = %q", got)
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
