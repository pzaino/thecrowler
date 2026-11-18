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

// Stage B2R authoritative pre-start and transaction-held fence validation.
//
// A fenced worker carries an immutable attempt identity. That identity is not
// execution authority: before external work and again inside every
// current-state mutation transaction the database must re-establish that the
// operator policy is still fenced, the claim is still current, the lease has
// not expired and the source is still live and enabled. The source row is the
// serialization point shared with claim allocation, heartbeat and terminal
// reporting, so a validation that holds it cannot race those paths.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SourceCrawlFenceResult is the closed fence-validation vocabulary.
type SourceCrawlFenceResult string

const (
	SourceCrawlFenceActive      SourceCrawlFenceResult = "active"
	SourceCrawlFenceStale       SourceCrawlFenceResult = "stale_fence"
	SourceCrawlFenceLegacy      SourceCrawlFenceResult = "legacy_mode"
	SourceCrawlFenceUnsupported SourceCrawlFenceResult = "unsupported"
	SourceCrawlFenceUnavailable SourceCrawlFenceResult = "unavailable"
	SourceCrawlFenceConflict    SourceCrawlFenceResult = "conflict"
)

var (
	errSourceCrawlFenceStale       = errors.New("source crawl fence is not current")
	errSourceCrawlFenceUnavailable = errors.New("source crawl fence authority is unavailable")
	errSourceCrawlFenceUnsupported = errors.New("source crawl fence is unsupported")
	errSourceCrawlFenceConflict    = errors.New("source crawl fence is corrupt")
)

// SourceCrawlFenceStatus is the bounded result of a pre-start or
// transaction-held fence validation. LeaseExpiresAt is the authoritative lease
// last observed by the validator, not a worker claim.
type SourceCrawlFenceStatus struct {
	Result         SourceCrawlFenceResult
	Reason         string
	LeaseExpiresAt time.Time
}

// FenceResultFromError maps a ValidateTx error to the crawler result
// vocabulary. It is deliberately closed: unknown errors are unavailable.
func FenceResultFromError(err error) SourceCrawlFenceStatus {
	switch {
	case err == nil:
		return SourceCrawlFenceStatus{Result: SourceCrawlFenceActive}
	case errors.Is(err, ErrSourceCrawlClaimLegacyMode):
		return SourceCrawlFenceStatus{Result: SourceCrawlFenceLegacy, Reason: "legacy_mode"}
	case errors.Is(err, errSourceCrawlFenceStale):
		return SourceCrawlFenceStatus{Result: SourceCrawlFenceStale, Reason: "stale_fence"}
	case errors.Is(err, errSourceCrawlFenceConflict):
		return SourceCrawlFenceStatus{Result: SourceCrawlFenceConflict, Reason: "conflict"}
	case errors.Is(err, errSourceCrawlFenceUnsupported):
		return SourceCrawlFenceStatus{Result: SourceCrawlFenceUnsupported, Reason: "unsupported"}
	default:
		return SourceCrawlFenceStatus{Result: SourceCrawlFenceUnavailable, Reason: "unavailable"}
	}
}

func (h *SourceCrawlHandoff) attemptSnapshot() (sourceCrawlAttempt, bool) {
	if h == nil {
		return sourceCrawlAttempt{}, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.terminal {
		return h.attempt, true
	}
	return h.attempt, false
}

// ValidateCurrent performs one bounded, read-only authoritative fence check.
// It is used before a crawler starts external work and again after a possibly
// blocking VDI acquisition. It does not hold SQL locks for the crawl.
func (h *SourceCrawlHandoff) ValidateCurrent(ctx context.Context,
	db *Handler) (SourceCrawlFenceStatus, error) {
	return h.validateCurrentWithClock(ctx, db, sourceCrawlClaimDatabaseClock)
}

// validateCurrentAt is the deterministic test seam.
func (h *SourceCrawlHandoff) validateCurrentAt(ctx context.Context,
	db *Handler, now time.Time) (SourceCrawlFenceStatus, error) {
	return h.validateCurrentWithClock(ctx, db, sourceCrawlFixedClock(now))
}

func (h *SourceCrawlHandoff) validateCurrentWithClock(ctx context.Context,
	db *Handler, clock sourceCrawlClaimClock) (SourceCrawlFenceStatus, error) {
	attempt, terminal := h.attemptSnapshot()
	if terminal {
		return SourceCrawlFenceStatus{
			Result: SourceCrawlFenceStale, Reason: "handoff_terminal",
		}, nil
	}
	if err := sourceCrawlFencePreconditions(ctx, db); err != nil {
		return FenceResultFromError(err), err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	txCtx, cancel := TransactionContextForHandler(ctx, db)
	defer cancel()
	opts := &sql.TxOptions{}
	if normalizeDBMS((*db).DBMS()) == "postgres" {
		opts.Isolation = sql.LevelRepeatableRead
		opts.ReadOnly = true
	}
	tx, err := (*db).BeginTx(txCtx, opts)
	if err != nil {
		return SourceCrawlFenceStatus{
			Result: SourceCrawlFenceUnavailable, Reason: "transaction_unavailable",
		}, NormalizeTransactionError(txCtx, err)
	}
	defer tx.Rollback() //nolint:errcheck
	status, err := sourceCrawlCheckFenceTxWithClock(txCtx, tx, *db, attempt, false, clock)
	switch status.Result {
	case SourceCrawlFenceUnavailable, SourceCrawlFenceLegacy,
		SourceCrawlFenceUnsupported, SourceCrawlFenceConflict:
		return status, err
	default:
		return status, nil
	}
}

// ValidateTx revalidates the exact attempt inside a current-state mutation
// transaction. It locks the source row until the caller commits, so a terminal
// report, a supersession or an ownership transfer cannot interleave between
// the validation and the write. Callers must treat any error as a reason to
// roll back.
func (h *SourceCrawlHandoff) ValidateTx(ctx context.Context, tx *sql.Tx,
	db *Handler) error {
	return h.validateTxWithClock(ctx, tx, db, sourceCrawlClaimDatabaseClock)
}

// validateTxAt is the deterministic test seam.
func (h *SourceCrawlHandoff) validateTxAt(ctx context.Context, tx *sql.Tx,
	db *Handler, now time.Time) error {
	return h.validateTxWithClock(ctx, tx, db, sourceCrawlFixedClock(now))
}

func (h *SourceCrawlHandoff) validateTxWithClock(ctx context.Context, tx *sql.Tx,
	db *Handler, clock sourceCrawlClaimClock) error {
	attempt, terminal := h.attemptSnapshot()
	if terminal {
		return fmt.Errorf("%w: handoff_terminal", errSourceCrawlFenceStale)
	}
	if tx == nil || db == nil || *db == nil {
		return errSourceCrawlFenceUnavailable
	}
	if !SourceCrawlAttemptSupported((*db).DBMS()) {
		return errSourceCrawlFenceUnsupported
	}
	status, err := sourceCrawlCheckFenceTxWithClock(ctx, tx, *db, attempt, true, clock)
	if err == nil && status.Result == SourceCrawlFenceActive {
		return nil
	}
	if err != nil {
		switch status.Result {
		case SourceCrawlFenceUnavailable:
			return fmt.Errorf("%w: %v", errSourceCrawlFenceUnavailable, err)
		case SourceCrawlFenceConflict:
			return fmt.Errorf("%w: %v", errSourceCrawlFenceConflict, err)
		}
	}
	switch status.Result {
	case SourceCrawlFenceLegacy:
		return fmt.Errorf("%w: %s", ErrSourceCrawlClaimLegacyMode, status.Reason)
	case SourceCrawlFenceUnsupported:
		return fmt.Errorf("%w: %s", errSourceCrawlFenceUnsupported, status.Reason)
	case SourceCrawlFenceConflict:
		return fmt.Errorf("%w: %s", errSourceCrawlFenceConflict, status.Reason)
	case SourceCrawlFenceUnavailable:
		return fmt.Errorf("%w: %s", errSourceCrawlFenceUnavailable, status.Reason)
	default:
		return fmt.Errorf("%w: %s", errSourceCrawlFenceStale, status.Reason)
	}
}

func sourceCrawlFencePreconditions(ctx context.Context, db *Handler) error {
	if db == nil || *db == nil {
		return errSourceCrawlFenceUnavailable
	}
	if !SourceCrawlAttemptSupported((*db).DBMS()) {
		return errSourceCrawlFenceUnsupported
	}
	// One cheap existence probe. The full structural readiness check already
	// runs before claim allocation; repeating it for every page transaction
	// would dominate crawling cost.
	present, err := SourceCrawlAttemptSchemaPresent(ctx, db)
	if err != nil {
		return fmt.Errorf("%w: schema_unavailable", errSourceCrawlFenceUnavailable)
	}
	if !present {
		return fmt.Errorf("%w: schema_unavailable", errSourceCrawlFenceUnsupported)
	}
	return nil
}

// sourceCrawlCheckFenceTx is the shared validator. With lockSource it takes the
// source row lock, which serializes the caller with claim allocation,
// heartbeat, terminal reporting and ownership transfer. The read-only variant
// is a consistent snapshot instead.
func sourceCrawlCheckFenceTxWithClock(ctx context.Context, tx *sql.Tx, db Handler,
	attempt sourceCrawlAttempt, lockSource bool,
	clock sourceCrawlClaimClock) (SourceCrawlFenceStatus, error) {
	if tx == nil {
		return SourceCrawlFenceStatus{Result: SourceCrawlFenceUnavailable,
			Reason: "transaction_unavailable"}, errSourceCrawlFenceUnavailable
	}
	if attempt.SourceID == 0 || !sourceCrawlAttemptIDPattern.MatchString(attempt.AttemptID) ||
		attempt.Generation == 0 || !sourceCrawlHashPattern.MatchString(attempt.ClaimRequestID) ||
		strings.TrimSpace(attempt.EngineID) == "" || len(attempt.EngineID) > 256 {
		return SourceCrawlFenceStatus{Result: SourceCrawlFenceConflict,
			Reason: "attempt_identity"}, errSourceCrawlFenceConflict
	}
	mode, err := sourceCrawlFenceModeTx(ctx, tx)
	if err != nil {
		if errors.Is(err, errSourceCrawlFenceUnsupported) {
			return SourceCrawlFenceStatus{Result: SourceCrawlFenceUnsupported,
				Reason: "policy_missing"}, errSourceCrawlFenceUnsupported
		}
		return SourceCrawlFenceStatus{Result: SourceCrawlFenceUnavailable,
			Reason: "policy_unavailable"}, err
	}
	if mode != "fenced" {
		return SourceCrawlFenceStatus{Result: SourceCrawlFenceLegacy,
			Reason: "legacy_mode"}, ErrSourceCrawlClaimLegacyMode
	}
	source, found, err := sourceCrawlFenceSourceTx(ctx, tx, db, attempt.SourceID, lockSource)
	if err != nil {
		return SourceCrawlFenceStatus{Result: SourceCrawlFenceUnavailable,
			Reason: "source_unavailable"}, err
	}
	if !found || source.Deleted {
		return SourceCrawlFenceStatus{Result: SourceCrawlFenceStale,
			Reason: "source_missing"}, nil
	}
	if source.Disabled {
		return SourceCrawlFenceStatus{Result: SourceCrawlFenceStale,
			Reason: "source_disabled"}, nil
	}
	if source.Status != "processing" {
		return SourceCrawlFenceStatus{Result: SourceCrawlFenceStale,
			Reason: "source_not_processing"}, nil
	}
	current, found, err := sourceCrawlFenceCurrentClaimTx(ctx, tx, db, attempt.SourceID, lockSource)
	if err != nil {
		return SourceCrawlFenceStatus{Result: SourceCrawlFenceUnavailable,
			Reason: "claim_unavailable"}, err
	}
	if !found {
		return SourceCrawlFenceStatus{Result: SourceCrawlFenceStale,
			Reason: "claim_missing"}, nil
	}
	history, found, err := sourceCrawlLoadAttemptTx(ctx, tx, db,
		attempt.AttemptID, attempt.SourceID, attempt.Generation, lockSource)
	if err != nil {
		return SourceCrawlFenceStatus{Result: SourceCrawlFenceConflict,
			Reason: "claim_history_corrupt"}, err
	}
	if !found {
		return SourceCrawlFenceStatus{Result: SourceCrawlFenceStale,
			Reason: "attempt_missing"}, nil
	}
	if current.AttemptID != attempt.AttemptID || current.Generation != attempt.Generation {
		return SourceCrawlFenceStatus{Result: SourceCrawlFenceStale,
			Reason: "attempt_not_current"}, nil
	}
	if !current.matchesAttempt(history) {
		return SourceCrawlFenceStatus{Result: SourceCrawlFenceStale,
			Reason: "claim_history_mismatch"}, nil
	}
	if history.ClaimRequestID != attempt.ClaimRequestID || history.EngineID != attempt.EngineID ||
		!history.ClaimedAt.Equal(attempt.ClaimedAt) ||
		!history.LeaseExpiresAt.Equal(attempt.LeaseExpiresAt) {
		return SourceCrawlFenceStatus{Result: SourceCrawlFenceConflict,
			Reason: "attempt_identity"}, nil
	}
	if history.State != sourceCrawlAttemptClaimed {
		return SourceCrawlFenceStatus{Result: SourceCrawlFenceStale,
			Reason: "attempt_terminal"}, nil
	}
	now, err := sourceCrawlReadClaimClock(ctx, tx, db, clock)
	if err != nil {
		return SourceCrawlFenceStatus{Result: SourceCrawlFenceUnavailable,
			Reason: "database_clock_unavailable"}, err
	}
	if !now.Before(history.LeaseExpiresAt) || !now.Before(current.LeaseExpiresAt) {
		return SourceCrawlFenceStatus{Result: SourceCrawlFenceStale,
			Reason: "lease_expired", LeaseExpiresAt: history.LeaseExpiresAt}, nil
	}
	return SourceCrawlFenceStatus{
		Result: SourceCrawlFenceActive, LeaseExpiresAt: history.LeaseExpiresAt,
	}, nil
}

func sourceCrawlFenceModeTx(ctx context.Context, tx *sql.Tx) (string, error) {
	var mode string
	err := tx.QueryRowContext(ctx,
		`SELECT mode FROM SourceCrawlClaimPolicy WHERE policy_id = 1`).Scan(&mode)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: policy row", errSourceCrawlFenceUnsupported)
	}
	if err != nil {
		return "", fmt.Errorf("%w: policy read", errSourceCrawlFenceUnavailable)
	}
	return strings.TrimSpace(mode), nil
}

func sourceCrawlFenceSourceTx(ctx context.Context, tx *sql.Tx, db Handler,
	sourceID uint64, lock bool) (sourceCrawlResultSource, bool, error) {
	p := func(n int) string { return sourceCrawlPlaceholder(db, n) }
	query := `SELECT usr_id, disabled, deleted_at, status FROM Sources WHERE source_id = ` + p(1)
	if lock && normalizeDBMS(db.DBMS()) == "postgres" {
		query += ` FOR UPDATE`
	}
	var userID int64
	var disabled bool
	var deletedAt sql.NullTime
	var status string
	err := tx.QueryRowContext(ctx, query, sourceID).Scan(&userID, &disabled, &deletedAt, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return sourceCrawlResultSource{}, false, nil
	}
	if err != nil {
		return sourceCrawlResultSource{}, false, err
	}
	source := sourceCrawlResultSource{Disabled: disabled, Deleted: deletedAt.Valid, Status: status}
	if userID > 0 {
		source.UserID = uint64(userID)
	}
	return source, true, nil
}

func sourceCrawlFenceCurrentClaimTx(ctx context.Context, tx *sql.Tx, db Handler,
	sourceID uint64, lock bool) (sourceCrawlCurrentClaim, bool, error) {
	p := func(n int) string { return sourceCrawlPlaceholder(db, n) }
	query := `SELECT attempt_id, claim_request_id, generation, engine_id,
		claimed_at, lease_expires_at FROM SourceCrawlClaims WHERE source_id = ` + p(1)
	if lock && normalizeDBMS(db.DBMS()) == "postgres" {
		query += ` FOR UPDATE`
	}
	var current sourceCrawlCurrentClaim
	var generation int64
	err := tx.QueryRowContext(ctx, query, sourceID).Scan(
		&current.AttemptID, &current.ClaimRequestID, &generation, &current.EngineID,
		&current.ClaimedAt, &current.LeaseExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return sourceCrawlCurrentClaim{}, false, nil
	}
	if err != nil {
		return sourceCrawlCurrentClaim{}, false, err
	}
	if generation <= 0 {
		return sourceCrawlCurrentClaim{}, false,
			fmt.Errorf("%w: current claim generation", errSourceCrawlFenceConflict)
	}
	current.Generation = uint64(generation)
	current.ClaimedAt = current.ClaimedAt.UTC().Truncate(time.Microsecond)
	current.LeaseExpiresAt = current.LeaseExpiresAt.UTC().Truncate(time.Microsecond)
	return current, true, nil
}
