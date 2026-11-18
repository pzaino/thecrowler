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
	"database/sql"
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
		receipt.OperationResult = &sourceCrawlTerminalOperationResult{
			Binding:       *h.attempt.Binding,
			ExpectedState: CrawlOperationAccepted,
			ResultState:   target,
		}
	}
	internal, err := reportSourceCrawlAttemptTerminalWithClock(ctx, db, receipt, clock)
	return SourceCrawlTerminalResult{
		Result:              sourceCrawlResultFromInternal(internal.Result),
		Reason:              internal.Reason,
		SourceStatusChanged: internal.SourceStatusChanged,
	}, err
}

// UpdateUnboundSourceStatus records the operational source status for an
// unbound fenced attempt. It is deliberately separate from the B1 terminal
// report: B1 never fabricates an operation or applies the bound-owner source
// policy to unbound work. The current claim/attempt/generation and the terminal
// history are the fence; this method never creates a Status row and is not
// operation or goal evidence.
func (h *SourceCrawlHandoff) UpdateUnboundSourceStatus(ctx context.Context, db *Handler,
	outcome SourceCrawlTerminalOutcome, observedAt time.Time) (SourceCrawlTerminalResult, error) {
	if h == nil {
		return SourceCrawlTerminalResult{Result: SourceCrawlResultStaleFence, Reason: "handoff_missing"}, nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.attempt.Binding != nil {
		return SourceCrawlTerminalResult{Result: SourceCrawlResultConflict, Reason: "handoff_bound"}, nil
	}
	internalOutcome, err := sourceCrawlInternalOutcome(outcome)
	if err != nil {
		return SourceCrawlTerminalResult{Result: SourceCrawlResultConflict, Reason: "invalid_outcome"}, nil
	}
	targetState, ok := sourceCrawlTerminalAttemptState(internalOutcome)
	if !ok {
		return SourceCrawlTerminalResult{Result: SourceCrawlResultConflict, Reason: "invalid_outcome"}, nil
	}
	targetStatus, lastError := sourceCrawlSourceStatusForOutcome(internalOutcome)
	if targetStatus == "" {
		return SourceCrawlTerminalResult{Result: SourceCrawlResultConflict, Reason: "invalid_outcome"}, nil
	}
	txCtx, tx, cancel, class, reason, err := sourceCrawlBeginResultTx(ctx, db)
	if class != "" {
		if class == sourceCrawlTerminalUnavailable {
			return SourceCrawlTerminalResult{Result: sourceCrawlResultFromInternal(class), Reason: reason}, err
		}
		return SourceCrawlTerminalResult{Result: sourceCrawlResultFromInternal(class), Reason: reason}, nil
	}
	defer cancel()
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	source, found, err := sourceCrawlLockResultSourceTx(txCtx, tx, *db, h.attempt.SourceID)
	if err != nil {
		return SourceCrawlTerminalResult{Result: SourceCrawlResultUnavailable, Reason: "source_read_unavailable"}, err
	}
	if !found || source.Deleted {
		return SourceCrawlTerminalResult{Result: SourceCrawlResultStaleFence, Reason: "source_missing"}, nil
	}
	current, found, err := sourceCrawlLockCurrentClaimTx(txCtx, tx, *db, h.attempt.SourceID)
	if err != nil {
		return SourceCrawlTerminalResult{Result: SourceCrawlResultUnavailable, Reason: "claim_read_unavailable"}, err
	}
	if !found {
		return SourceCrawlTerminalResult{Result: SourceCrawlResultStaleFence, Reason: "claim_missing"}, nil
	}
	history, found, err := sourceCrawlLoadAttemptTx(txCtx, tx, *db,
		h.attempt.AttemptID, h.attempt.SourceID, h.attempt.Generation, true)
	if err != nil {
		class := sourceCrawlLoadResultClass(err)
		return SourceCrawlTerminalResult{Result: sourceCrawlResultFromInternal(class), Reason: "attempt_read"}, nil
	}
	if !found {
		return SourceCrawlTerminalResult{Result: SourceCrawlResultStaleFence, Reason: "attempt_missing"}, nil
	}
	if current.AttemptID != h.attempt.AttemptID || current.Generation != h.attempt.Generation {
		return SourceCrawlTerminalResult{Result: SourceCrawlResultStaleFence, Reason: "attempt_not_current"}, nil
	}
	if !current.matchesAttempt(history) {
		return SourceCrawlTerminalResult{Result: SourceCrawlResultConflict, Reason: "claim_history_mismatch"}, nil
	}
	if history.Binding != nil {
		return SourceCrawlTerminalResult{Result: SourceCrawlResultConflict, Reason: "attempt_bound"}, nil
	}
	if history.State != targetState {
		return SourceCrawlTerminalResult{Result: SourceCrawlResultConflict, Reason: "terminal_state_conflict"}, nil
	}
	if source.Status == targetStatus {
		return SourceCrawlTerminalResult{
			Result: SourceCrawlResultIdempotentReplay, Reason: "source_status_replay",
			SourceStatusChanged: false,
		}, nil
	}
	if source.Disabled {
		return SourceCrawlTerminalResult{Result: SourceCrawlResultStaleFence, Reason: "source_disabled"}, nil
	}
	if source.Status != "processing" {
		return SourceCrawlTerminalResult{Result: SourceCrawlResultConflict, Reason: "source_status_conflict"}, nil
	}
	now, err := sourceCrawlReadClaimClock(txCtx, tx, *db, sourceCrawlClaimDatabaseClock)
	if err != nil {
		return SourceCrawlTerminalResult{Result: SourceCrawlResultUnavailable, Reason: "database_clock_unavailable"}, err
	}
	changed, err := sourceCrawlUpdateUnboundSourceTerminal(txCtx, tx, *db, history.SourceID,
		targetStatus, lastError, history.TerminalAt, now)
	if err != nil {
		return SourceCrawlTerminalResult{Result: SourceCrawlResultUnavailable, Reason: "source_update_unavailable"}, err
	}
	if !changed {
		return SourceCrawlTerminalResult{Result: SourceCrawlResultConflict, Reason: "source_status_conflict"}, nil
	}
	if err := tx.Commit(); err != nil {
		return SourceCrawlTerminalResult{Result: SourceCrawlResultUnavailable, Reason: "commit_uncertain"}, err
	}
	committed = true
	return SourceCrawlTerminalResult{
		Result: SourceCrawlResultApplied, Reason: "source_status_applied",
		SourceStatusChanged: true,
	}, nil
}

func sourceCrawlUpdateUnboundSourceTerminal(ctx context.Context, tx *sql.Tx, db Handler,
	sourceID uint64, status, lastError string, crawledAt, now time.Time) (bool, error) {
	p := func(n int) string { return sourceCrawlPlaceholder(db, n) }
	var (
		query string
		args  []any
	)
	if lastError == "" {
		query = `UPDATE Sources SET status = ` + p(1) + `, last_crawled_at = ` + p(2) + `,
			last_error = NULL, last_error_at = NULL, last_updated_at = ` + p(3) + `
			WHERE source_id = ` + p(4) + ` AND deleted_at IS NULL AND disabled = FALSE
			  AND status = 'processing'`
		args = []any{status, crawledAt, now, sourceID}
	} else {
		query = `UPDATE Sources SET status = ` + p(1) + `, last_crawled_at = ` + p(2) + `,
			last_error = ` + p(3) + `, last_error_at = ` + p(4) + `, last_updated_at = ` + p(5) + `
			WHERE source_id = ` + p(6) + ` AND deleted_at IS NULL AND disabled = FALSE
			  AND status = 'processing'`
		args = []any{status, crawledAt, lastError, crawledAt, now, sourceID}
	}
	updated, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return false, err
	}
	rows, err := updated.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows == 1, nil
}
