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

// Stage B2 crawler-facing adapter for the B1 fenced result repository. The
// crawler carries one SourceCrawlHandoff from ClaimSourceCrawlAttempts through
// its queue, heartbeat loop and terminal report. The handoff is an identity,
// not permission: every RenewLease and ReportTerminal call revalidates the
// authoritative database fence inside its own transaction.

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// SourceCrawlResult is the closed crawler-facing result class.
type SourceCrawlResult string

const (
	SourceCrawlResultApplied          SourceCrawlResult = "applied"
	SourceCrawlResultIdempotentReplay SourceCrawlResult = "idempotent_replay"
	SourceCrawlResultStaleFence       SourceCrawlResult = "stale_fence"
	SourceCrawlResultConflict         SourceCrawlResult = "conflict"
	SourceCrawlResultUnavailable      SourceCrawlResult = "unavailable"
	SourceCrawlResultUnsupported      SourceCrawlResult = "unsupported"
)

// SourceCrawlTerminalOutcome is the closed crawler outcome vocabulary.
type SourceCrawlTerminalOutcome string

const (
	SourceCrawlOutcomeCompleted SourceCrawlTerminalOutcome = "completed"
	SourceCrawlOutcomeFailed    SourceCrawlTerminalOutcome = "failed"
	SourceCrawlOutcomeCancelled SourceCrawlTerminalOutcome = "cancelled"
)

// SourceCrawlLeaseResult is the heartbeat result returned to the crawler.
type SourceCrawlLeaseResult struct {
	Result         SourceCrawlResult
	Reason         string
	LeaseExpiresAt time.Time
}

// SourceCrawlTerminalResult is the terminal-report result returned to the
// crawler. SourceStatusChanged reports whether this call also updated the
// source-level status under the strict bound-owner policy.
type SourceCrawlTerminalResult struct {
	Result              SourceCrawlResult
	Reason              string
	SourceStatusChanged bool
}

// SourceCrawlHandoff is the crawler-owned identity for one fenced attempt. It
// is safe for concurrent heartbeat and terminal-report use.
type SourceCrawlHandoff struct {
	mu          sync.Mutex
	attempt     sourceCrawlAttempt
	attribution sourceCrawlHandoffAttribution
	terminal    bool
}

// NewSourceCrawlHandoff converts one allocated claim into the handoff the
// crawler carries. Stale, conflicting or legacy outcomes are rejected.
func NewSourceCrawlHandoff(claim SourceCrawlClaim) (*SourceCrawlHandoff, error) {
	handoff, err := newSourceCrawlCrawlerHandoff(claim)
	if err != nil {
		return nil, err
	}
	return &SourceCrawlHandoff{
		attempt:     handoff.Attempt,
		attribution: handoff.Attribution,
	}, nil
}

// SourceID returns the claimed source ID.
func (h *SourceCrawlHandoff) SourceID() uint64 {
	if h == nil {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.attempt.SourceID
}

// AttemptID returns the fenced attempt ID.
func (h *SourceCrawlHandoff) AttemptID() string {
	if h == nil {
		return ""
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.attempt.AttemptID
}

// Generation returns the monotonic attempt generation.
func (h *SourceCrawlHandoff) Generation() uint64 {
	if h == nil {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.attempt.Generation
}

// ClaimedAt returns the immutable claim time.
func (h *SourceCrawlHandoff) ClaimedAt() time.Time {
	if h == nil {
		return time.Time{}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.attempt.ClaimedAt
}

// LeaseExpiresAt returns the latest authoritative lease known to the crawler.
func (h *SourceCrawlHandoff) LeaseExpiresAt() time.Time {
	if h == nil {
		return time.Time{}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.attempt.LeaseExpiresAt
}

// Bound reports whether the attempt carries an immutable operation binding.
func (h *SourceCrawlHandoff) Bound() bool {
	if h == nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.attempt.Binding != nil
}

func sourceCrawlResultFromInternal(result sourceCrawlTerminalReportResult) SourceCrawlResult {
	switch result {
	case sourceCrawlTerminalApplied:
		return SourceCrawlResultApplied
	case sourceCrawlTerminalIdempotentReplay:
		return SourceCrawlResultIdempotentReplay
	case sourceCrawlTerminalStaleFence:
		return SourceCrawlResultStaleFence
	case sourceCrawlTerminalConflict:
		return SourceCrawlResultConflict
	case sourceCrawlTerminalUnsupported:
		return SourceCrawlResultUnsupported
	default:
		return SourceCrawlResultUnavailable
	}
}

func sourceCrawlInternalOutcome(outcome SourceCrawlTerminalOutcome) (sourceCrawlTerminalOutcome, error) {
	switch outcome {
	case SourceCrawlOutcomeCompleted:
		return sourceCrawlTerminalCompletedOutcome, nil
	case SourceCrawlOutcomeFailed:
		return sourceCrawlTerminalFailedOutcome, nil
	case SourceCrawlOutcomeCancelled:
		return sourceCrawlTerminalCancelledOutcome, nil
	default:
		return "", fmt.Errorf("%w: terminal outcome %q", errSourceCrawlResultConflict, outcome)
	}
}

// RenewLease renews the exact current attempt and updates the handoff's
// authoritative lease on success. The database remains the fence authority.
func (h *SourceCrawlHandoff) RenewLease(ctx context.Context, db *Handler,
	extension time.Duration) (SourceCrawlLeaseResult, error) {
	return h.renewLeaseWithClock(ctx, db, extension, sourceCrawlClaimDatabaseClock)
}

// renewLeaseAt is the deterministic test seam for the runtime adapter.
func (h *SourceCrawlHandoff) renewLeaseAt(ctx context.Context, db *Handler,
	extension time.Duration, now time.Time) (SourceCrawlLeaseResult, error) {
	return h.renewLeaseWithClock(ctx, db, extension, sourceCrawlFixedClock(now))
}

func (h *SourceCrawlHandoff) renewLeaseWithClock(ctx context.Context, db *Handler,
	extension time.Duration, clock sourceCrawlClaimClock) (SourceCrawlLeaseResult, error) {
	if h == nil {
		return SourceCrawlLeaseResult{Result: SourceCrawlResultStaleFence, Reason: "handoff_missing"}, nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.terminal {
		return SourceCrawlLeaseResult{
			Result: SourceCrawlResultStaleFence, Reason: "handoff_terminal",
			LeaseExpiresAt: h.attempt.LeaseExpiresAt,
		}, nil
	}
	request := sourceCrawlLeaseRenewalRequest{
		SourceID: h.attempt.SourceID, AttemptID: h.attempt.AttemptID,
		Generation: h.attempt.Generation, ClaimRequestID: h.attempt.ClaimRequestID,
		EngineID: h.attempt.EngineID, Extension: extension,
	}
	internal, err := renewSourceCrawlAttemptLeaseWithClock(ctx, db, request, clock)
	result := SourceCrawlLeaseResult{
		Result: sourceCrawlResultFromInternal(internal.Result),
		Reason: internal.Reason,
	}
	if internal.Result == sourceCrawlTerminalApplied {
		h.attempt.LeaseExpiresAt = internal.Claim.LeaseExpiresAt
		result.LeaseExpiresAt = internal.Claim.LeaseExpiresAt
	} else {
		result.LeaseExpiresAt = h.attempt.LeaseExpiresAt
	}
	return result, err
}

// ReportTerminal reports one completed, failed or cancelled crawl. It marks the
// handoff terminal so a concurrent heartbeat loop stops renewing. The database
// revalidates the fence, binding and source policy inside its transaction.
func (h *SourceCrawlHandoff) ReportTerminal(ctx context.Context, db *Handler,
	outcome SourceCrawlTerminalOutcome, observedAt time.Time) (SourceCrawlTerminalResult, error) {
	return h.reportTerminalWithClock(ctx, db, outcome, observedAt, sourceCrawlClaimDatabaseClock)
}

// reportTerminalAt is the deterministic test seam for the runtime adapter.
func (h *SourceCrawlHandoff) reportTerminalAt(ctx context.Context, db *Handler,
	outcome SourceCrawlTerminalOutcome, observedAt time.Time,
	now time.Time) (SourceCrawlTerminalResult, error) {
	return h.reportTerminalWithClock(ctx, db, outcome, observedAt, sourceCrawlFixedClock(now))
}

func (h *SourceCrawlHandoff) reportTerminalWithClock(ctx context.Context, db *Handler,
	outcome SourceCrawlTerminalOutcome, observedAt time.Time,
	clock sourceCrawlClaimClock) (SourceCrawlTerminalResult, error) {
	if h == nil {
		return SourceCrawlTerminalResult{Result: SourceCrawlResultStaleFence, Reason: "handoff_missing"}, nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.terminal = true
	internalOutcome, err := sourceCrawlInternalOutcome(outcome)
	if err != nil {
		return SourceCrawlTerminalResult{Result: SourceCrawlResultConflict, Reason: "invalid_outcome"}, nil
	}
	receipt := sourceCrawlTerminalReceipt{
		Attempt:     h.attempt,
		Attribution: h.attribution,
		Outcome:     internalOutcome,
		ReasonCode:  sourceCrawlTerminalReasonCodes[internalOutcome],
		ObservedAt:  observedAt.UTC().Truncate(time.Microsecond),
	}
	if h.attempt.Binding != nil {
		target, ok := sourceCrawlTerminalOperationTarget(internalOutcome)
		if !ok {
			return SourceCrawlTerminalResult{Result: SourceCrawlResultConflict, Reason: "invalid_outcome"}, nil
		}
		// The database reads the authoritative Status row under its own locks
		// and permits a terminal CAS from accepted or processing. The worker
		// deliberately supplies no expected state; a worker-supplied value is
		// a precondition at most, never authority.
		receipt.OperationResult = &sourceCrawlTerminalOperationResult{
			Binding:     *h.attempt.Binding,
			ResultState: target,
		}
	}
	internal, err := reportSourceCrawlAttemptTerminalWithClock(ctx, db, receipt, clock)
	return SourceCrawlTerminalResult{
		Result:              sourceCrawlResultFromInternal(internal.Result),
		Reason:              internal.Reason,
		SourceStatusChanged: internal.SourceStatusChanged,
	}, err
}
