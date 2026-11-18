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
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"
)

// SourceCrawlClaimOutcome is the closed result class for one claimed source.
type SourceCrawlClaimOutcome string

const (
	SourceCrawlClaimed SourceCrawlClaimOutcome = "claimed"
	// SourceCrawlAlreadyClaimed is reserved for a future single-source claim
	// diagnostic. ClaimSourceCrawlAttempts simply omits actively claimed
	// sources because it allocates new work rather than reporting one source.
	SourceCrawlAlreadyClaimed SourceCrawlClaimOutcome = "already_claimed"
	SourceCrawlUnattributed   SourceCrawlClaimOutcome = "unattributed"
	SourceCrawlAmbiguous      SourceCrawlClaimOutcome = "ambiguous"
	// SourceCrawlNotAttributable means one structurally valid candidate exists
	// but current source ownership does not authorize the operation principal.
	SourceCrawlNotAttributable SourceCrawlClaimOutcome = "not_attributable"
	SourceCrawlClaimConflict   SourceCrawlClaimOutcome = "conflict"
	SourceCrawlClaimStaleFence SourceCrawlClaimOutcome = "stale_fence"
	// SourceCrawlClaimLegacyMode is reserved for callers that expose the mode
	// as a result. ClaimSourceCrawlAttempts returns ErrSourceCrawlClaimLegacyMode
	// instead because no claim is allocated.
	SourceCrawlClaimLegacyMode SourceCrawlClaimOutcome = "legacy_mode"
)

var (
	ErrSourceCrawlClaimLegacyMode     = errors.New("source crawl claim API is disabled in legacy mode")
	ErrSourceCrawlClaimInvalidRequest = errors.New("source crawl claim request is invalid")
	ErrSourceCrawlClaimConflict       = errors.New("source crawl claim conflict")
	ErrSourceCrawlClaimUnavailable    = errors.New("source crawl claim store is unavailable")
	ErrSourceCrawlClaimCorrupt        = errors.New("source crawl claim history is corrupt")
	ErrSourceCrawlClaimGenerationDone = errors.New("source crawl claim generation is exhausted")
)

// SourceCrawlClaimRequest is trusted internal input. ClaimRequestID and
// EngineID must come from trusted runtime code, never from a model, event, web
// page or agent manifest.
//
// ClaimRequestID is an idempotency key for attempts that were actually
// persisted. It is not an exactly-once batch token:
//   - A request that selected no eligible sources leaves no durable
//     acknowledgement, so replaying the same ID later can claim new work.
//   - Replay verifies the persisted engine and lease duration, but priority,
//     limits and the other timing intervals are not stored and cannot be
//     compared. See sourceCrawlClaimRequestFingerprint for a pure digest that a
//     future, separately approved schema extension could persist.
//   - A live worker must allocate one fresh 256-bit claim request ID for every
//     new batch and must not treat an empty or failed batch as committed.
//
// Commit-outcome reconciliation reads the durable attempt by this ID and
// classifies it with database time; it never allocates a second generation.
type SourceCrawlClaimRequest struct {
	ClaimRequestID    string
	Limit             int
	Priority          string
	EngineID          string
	Lease             time.Duration
	LastOKUpdate      time.Duration
	LastErrorInterval time.Duration
	RegularCrawling   time.Duration
	ProcessingTimeout time.Duration
}

// SourceCrawlClaim is the bounded attempt identity returned for one source.
type SourceCrawlClaim struct {
	Outcome         SourceCrawlClaimOutcome
	SourceID        uint64
	ClaimRequestID  string
	AttemptID       string
	Generation      uint64
	EngineID        string
	OperationID     string
	RequestID       string
	RunID           string
	AgentID         string
	Owner           string
	PrincipalUserID uint64
	ApprovalID      string
	ApprovalSHA256  string
	ClaimedAt       time.Time
	LeaseExpiresAt  time.Time
	Reused          bool
}

// ClaimSourceCrawlAttempts allocates a new fenced attempt for each eligible
// source. It is read-only for operation/proposal rows and writes only source
// status/engine plus the optional attempt tables. No crawler caller exists in
// this package.
//
// A returned SourceCrawlClaim is an identity, not execution authority: the
// current claim, lease, policy and operation binding must be revalidated in the
// database transaction that performs a privileged mutation. Replaying an
// expired or superseded request returns SourceCrawlClaimStaleFence, and an
// unbound attempt replays as SourceCrawlUnattributed rather than a fabricated
// bound success.
func ClaimSourceCrawlAttempts(ctx context.Context, db *Handler, req SourceCrawlClaimRequest) ([]SourceCrawlClaim, error) {
	return claimSourceCrawlAttemptsWithClock(ctx, db, req, sourceCrawlClaimDatabaseClock)
}

// claimSourceCrawlAttemptsAt is the deterministic test seam. The fixed clock is
// still read only after the policy and writer locks have been acquired.
func claimSourceCrawlAttemptsAt(ctx context.Context, db *Handler, req SourceCrawlClaimRequest, now time.Time) ([]SourceCrawlClaim, error) {
	return claimSourceCrawlAttemptsWithClock(ctx, db, req,
		func(context.Context, *sql.Tx, Handler) (time.Time, error) { return now, nil })
}

type sourceCrawlClaimClock func(context.Context, *sql.Tx, Handler) (time.Time, error)

func sourceCrawlClaimDatabaseClock(ctx context.Context, tx *sql.Tx, db Handler) (time.Time, error) {
	return sourceCrawlClaimDatabaseNow(ctx, tx, db)
}

func claimSourceCrawlAttemptsWithClock(ctx context.Context, db *Handler, req SourceCrawlClaimRequest,
	clock sourceCrawlClaimClock) ([]SourceCrawlClaim, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil || *db == nil {
		return nil, fmt.Errorf("%w: database handler is nil", ErrSourceCrawlClaimUnavailable)
	}
	if err := validateSourceCrawlClaimRequest(req); err != nil {
		return nil, err
	}
	if !SourceCrawlAttemptSupported((*db).DBMS()) {
		return nil, ErrSourceCrawlAttemptUnsupported
	}
	if err := CheckSourceCrawlAttemptSchema(ctx, db); err != nil {
		return nil, err
	}
	txCtx, cancel := TransactionContextForHandler(ctx, db)
	defer cancel()
	tx, err := (*db).BeginTx(txCtx, nil)
	if err != nil {
		return nil, NormalizeTransactionError(txCtx, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if normalizeDBMS((*db).DBMS()) != "postgres" {
		// Acquire SQLite's writer lock before the first read without nesting
		// BEGIN IMMEDIATE inside the active sql.Tx.
		if _, err := tx.ExecContext(txCtx, `UPDATE SourceCrawlClaims SET last_updated_at = last_updated_at WHERE 0`); err != nil {
			return nil, fmt.Errorf("%w: acquire claim writer lock", ErrSourceCrawlClaimUnavailable)
		}
	}
	mode, err := sourceCrawlClaimModeTx(txCtx, tx, *db)
	if err != nil {
		return nil, err
	}
	if mode != "fenced" {
		return nil, ErrSourceCrawlClaimLegacyMode
	}
	now, err := sourceCrawlReadClaimClock(txCtx, tx, *db, clock)
	if err != nil {
		return nil, NormalizeTransactionError(txCtx, err)
	}

	if existing, found, err := loadSourceCrawlClaimRequestTx(txCtx, tx, *db, req.ClaimRequestID, now); err != nil {
		return nil, NormalizeTransactionError(txCtx, err)
	} else if found {
		if err := validateSourceCrawlClaimReplay(existing, req); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, NormalizeTransactionError(txCtx, err)
		}
		committed = true
		return existing, nil
	}

	sources, err := selectSourceCrawlClaimSourcesTx(txCtx, tx, *db, req, now)
	if err != nil {
		return nil, NormalizeTransactionError(txCtx, err)
	}
	claims := make([]SourceCrawlClaim, 0, len(sources))
	for _, source := range sources {
		// The batch selection timestamp can be stale by the time this source is
		// persisted. Read fresh time after the source row is locked and before
		// the claim/lease mutation so the lease starts at persistence time.
		sourceNow, err := sourceCrawlReadClaimClock(txCtx, tx, *db, clock)
		if err != nil {
			return nil, NormalizeTransactionError(txCtx, err)
		}
		claim, err := claimOneSourceCrawlAttemptTx(txCtx, tx, *db, req, sourceNow, source)
		if err != nil {
			return nil, NormalizeTransactionError(txCtx, err)
		}
		claims = append(claims, claim)
	}
	if len(claims) > 0 {
		// A long batch can outlive a short lease. Every entry here is a newly
		// allocated attempt, including unattributed, ambiguous,
		// not-attributable and conflict outcomes. No expired attempt may be
		// returned in a live-looking outcome, so demote all of them to
		// stale_fence while keeping the durable history unchanged.
		finalNow, err := sourceCrawlReadClaimClock(txCtx, tx, *db, clock)
		if err != nil {
			return nil, NormalizeTransactionError(txCtx, err)
		}
		for i := range claims {
			if claims[i].AttemptID != "" &&
				!finalNow.Before(claims[i].LeaseExpiresAt) {
				claims[i].Outcome = SourceCrawlClaimStaleFence
			}
		}
	}
	if err := tx.Commit(); err != nil {
		// Reconcile an uncertain commit by the trusted claim request ID. Never
		// retry into a blind second claim.
		reconcileCtx, reconcileCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer reconcileCancel()
		if existing, found, readErr := loadSourceCrawlClaimRequest(reconcileCtx, *db, req.ClaimRequestID); readErr == nil && found {
			if sameSourceCrawlClaims(existing, claims) {
				return existing, nil
			}
			return nil, fmt.Errorf("%w: committed claim identity differs", ErrSourceCrawlClaimConflict)
		}
		return nil, NormalizeTransactionError(txCtx, err)
	}
	committed = true
	return claims, nil
}

func sourceCrawlReadClaimClock(ctx context.Context, tx *sql.Tx, db Handler,
	clock sourceCrawlClaimClock) (time.Time, error) {
	now, err := clock(ctx, tx, db)
	if err != nil {
		return time.Time{}, err
	}
	now = now.UTC().Truncate(time.Microsecond)
	if now.IsZero() {
		return time.Time{}, fmt.Errorf("%w: trusted clock is zero", ErrSourceCrawlClaimInvalidRequest)
	}
	return now, nil
}

type sourceCrawlSourceRow struct {
	SourceID uint64
	UserID   uint64
}

func sameSourceCrawlClaims(existing, expected []SourceCrawlClaim) bool {
	if len(existing) != len(expected) {
		return false
	}
	bySource := make(map[uint64]SourceCrawlClaim, len(expected))
	for _, claim := range expected {
		bySource[claim.SourceID] = claim
	}
	for _, claim := range existing {
		want, ok := bySource[claim.SourceID]
		if !ok || want.AttemptID != claim.AttemptID ||
			want.ClaimRequestID != claim.ClaimRequestID ||
			want.Generation != claim.Generation || want.EngineID != claim.EngineID ||
			want.OperationID != claim.OperationID || want.RequestID != claim.RequestID ||
			want.ApprovalID != claim.ApprovalID || want.ApprovalSHA256 != claim.ApprovalSHA256 ||
			want.PrincipalUserID != claim.PrincipalUserID ||
			!want.LeaseExpiresAt.Equal(claim.LeaseExpiresAt) {
			return false
		}
	}
	return true
}

func validateSourceCrawlClaimReplay(existing []SourceCrawlClaim, req SourceCrawlClaimRequest) error {
	lease := req.Lease.Truncate(time.Microsecond)
	for _, claim := range existing {
		if claim.EngineID != req.EngineID {
			return fmt.Errorf("%w: claim request engine changed", ErrSourceCrawlClaimConflict)
		}
		if claim.LeaseExpiresAt.Sub(claim.ClaimedAt) != lease {
			return fmt.Errorf("%w: claim request lease changed", ErrSourceCrawlClaimConflict)
		}
	}
	return nil
}

func validateSourceCrawlClaimRequest(req SourceCrawlClaimRequest) error {
	if !sourceCrawlHashPattern.MatchString(strings.TrimSpace(req.ClaimRequestID)) {
		return fmt.Errorf("%w: claim_request_id", ErrSourceCrawlClaimInvalidRequest)
	}
	if req.Limit < 1 || req.Limit > 100 {
		return fmt.Errorf("%w: limit", ErrSourceCrawlClaimInvalidRequest)
	}
	if strings.TrimSpace(req.EngineID) == "" || len(req.EngineID) > 256 {
		return fmt.Errorf("%w: engine_id", ErrSourceCrawlClaimInvalidRequest)
	}
	if req.Lease <= 0 || req.Lease > 24*time.Hour {
		return fmt.Errorf("%w: lease", ErrSourceCrawlClaimInvalidRequest)
	}
	if req.LastOKUpdate < 0 || req.LastErrorInterval < 0 ||
		req.RegularCrawling < 0 || req.ProcessingTimeout < 0 {
		return fmt.Errorf("%w: intervals", ErrSourceCrawlClaimInvalidRequest)
	}
	return nil
}

func sourceCrawlClaimMode(ctx context.Context, db Handler) (string, error) {
	var mode string
	err := db.QueryRowContext(ctx, `SELECT mode FROM SourceCrawlClaimPolicy WHERE policy_id = 1`).Scan(&mode)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: policy row", ErrSourceCrawlAttemptSchemaUnavailable)
	}
	if err != nil {
		return "", fmt.Errorf("%w: policy read", ErrSourceCrawlAttemptSchemaUnavailable)
	}
	return mode, nil
}

func sourceCrawlClaimModeTx(ctx context.Context, tx *sql.Tx, db Handler) (string, error) {
	if normalizeDBMS(db.DBMS()) == "postgres" {
		if _, err := tx.ExecContext(ctx,
			`SELECT pg_advisory_xact_lock_shared(hashtext('crowler:source-crawl-claim-policy'))`); err != nil {
			return "", fmt.Errorf("%w: policy lock", ErrSourceCrawlClaimUnavailable)
		}
	}
	var mode string
	err := tx.QueryRowContext(ctx, `SELECT mode FROM SourceCrawlClaimPolicy WHERE policy_id = 1`).Scan(&mode)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: policy row", ErrSourceCrawlAttemptSchemaUnavailable)
	}
	if err != nil {
		return "", fmt.Errorf("%w: policy read", ErrSourceCrawlAttemptSchemaUnavailable)
	}
	return mode, nil
}

func selectSourceCrawlClaimSourcesTx(ctx context.Context, tx *sql.Tx, db Handler, req SourceCrawlClaimRequest, now time.Time) ([]sourceCrawlSourceRow, error) {
	p := func(n int) string { return sourceCrawlPlaceholder(db, n) }
	args := []any{}
	arg := func(v any) string {
		args = append(args, v)
		return p(len(args))
	}
	lastOK := now.Add(-req.LastOKUpdate)
	lastError := now.Add(-req.LastErrorInterval)
	regular := now.Add(-req.RegularCrawling)
	processing := now.Add(-req.ProcessingTimeout)
	claimExpired := arg(now)
	where := []string{`s.disabled = FALSE`, `s.deleted_at IS NULL`,
		`(c.source_id IS NULL OR c.lease_expires_at <= ` + claimExpired + `)`}
	eligibility := []string{
		`LOWER(TRIM(s.status)) IN ('new','pending')`,
		`s.status IS NULL`,
	}
	if req.LastOKUpdate > 0 {
		eligibility = append(eligibility, `(s.last_updated_at IS NULL OR s.last_updated_at < `+arg(lastOK)+`)`)
	}
	if req.RegularCrawling > 0 {
		eligibility = append(eligibility, `(LOWER(TRIM(s.status)) = 'completed' AND s.last_updated_at < `+arg(regular)+`)`)
	}
	if req.LastErrorInterval > 0 {
		eligibility = append(eligibility, `(LOWER(TRIM(s.status)) = 'error' AND s.last_updated_at < `+arg(lastError)+`)`)
	}
	claimExpiredProcessing := arg(now)
	processingCondition := `(c.source_id IS NOT NULL AND c.lease_expires_at <= ` + claimExpiredProcessing + `)`
	if req.ProcessingTimeout > 0 {
		processingCondition += ` OR (c.source_id IS NULL AND s.last_updated_at < ` + arg(processing) + `)`
	}
	eligibility = append(eligibility, `(LOWER(TRIM(s.status)) = 'processing' AND (`+processingCondition+`))`)
	where = append(where, `(`+strings.Join(eligibility, ` OR `)+`)`)

	priorities := splitSourceCrawlPriorities(req.Priority)
	if len(priorities) > 0 {
		placeholders := make([]string, len(priorities))
		for i, priority := range priorities {
			args = append(args, priority)
			placeholders[i] = p(len(args))
		}
		where = append(where, `LOWER(TRIM(s.priority)) IN (`+strings.Join(placeholders, `,`)+`)`)
	}
	limitPlaceholder := arg(req.Limit)
	query := `SELECT s.source_id, s.usr_id FROM Sources s
		LEFT JOIN SourceCrawlClaims c ON c.source_id = s.source_id
		WHERE ` + strings.Join(where, ` AND `) + `
		ORDER BY s.sub_priority DESC, s.source_id ASC
		LIMIT ` + limitPlaceholder
	if normalizeDBMS(db.DBMS()) == "postgres" {
		query += ` FOR UPDATE OF s SKIP LOCKED`
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []sourceCrawlSourceRow
	for rows.Next() {
		var row sourceCrawlSourceRow
		var userID int64
		if err := rows.Scan(&row.SourceID, &userID); err != nil {
			return nil, err
		}
		if userID > 0 {
			row.UserID = uint64(userID)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func splitSourceCrawlPriorities(raw string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.ToLower(strings.TrimSpace(part))
		if part != "" && !seen[part] {
			seen[part] = true
			out = append(out, part)
		}
	}
	return out
}

func sourceCrawlNextGenerationTx(ctx context.Context, tx *sql.Tx, db Handler, sourceID uint64, previousAttempt string, previousGeneration int64) (uint64, error) {
	p := func(n int) string { return sourceCrawlPlaceholder(db, n) }
	var maxGeneration sql.NullInt64
	if err := tx.QueryRowContext(ctx,
		`SELECT MAX(generation) FROM SourceCrawlAttempts WHERE source_id=`+p(1),
		sourceID).Scan(&maxGeneration); err != nil {
		return 0, err
	}
	if previousGeneration < 0 || (previousAttempt != "" && previousGeneration == 0) ||
		(previousAttempt == "" && previousGeneration > 0) {
		return 0, fmt.Errorf("%w: invalid current generation", ErrSourceCrawlClaimCorrupt)
	}
	if previousGeneration > 0 {
		if strings.TrimSpace(previousAttempt) == "" || !maxGeneration.Valid ||
			maxGeneration.Int64 != previousGeneration {
			return 0, fmt.Errorf("%w: current claim does not match the latest history row", ErrSourceCrawlClaimCorrupt)
		}
		var matches int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM SourceCrawlAttempts
			 WHERE source_id=`+p(1)+` AND generation=`+p(2)+` AND attempt_id=`+p(3),
			sourceID, previousGeneration, previousAttempt).Scan(&matches); err != nil {
			return 0, err
		}
		if matches != 1 {
			return 0, fmt.Errorf("%w: current claim has no exact history row", ErrSourceCrawlClaimCorrupt)
		}
	}
	next := int64(1)
	if maxGeneration.Valid {
		if maxGeneration.Int64 <= 0 {
			return 0, fmt.Errorf("%w: invalid history generation", ErrSourceCrawlClaimCorrupt)
		}
		if maxGeneration.Int64 == math.MaxInt64 {
			return 0, ErrSourceCrawlClaimGenerationDone
		}
		next = maxGeneration.Int64 + 1
	}
	if next <= 0 {
		return 0, ErrSourceCrawlClaimGenerationDone
	}
	return uint64(next), nil
}

func claimOneSourceCrawlAttemptTx(ctx context.Context, tx *sql.Tx, db Handler, req SourceCrawlClaimRequest, now time.Time, source sourceCrawlSourceRow) (SourceCrawlClaim, error) {
	p := func(n int) string { return sourceCrawlPlaceholder(db, n) }
	var previousAttempt string
	var previousGeneration int64
	lockClause := ""
	if normalizeDBMS(db.DBMS()) == "postgres" {
		lockClause = " FOR UPDATE"
	}
	err := tx.QueryRowContext(ctx,
		`SELECT attempt_id, generation FROM SourceCrawlClaims WHERE source_id = `+p(1)+lockClause,
		source.SourceID).Scan(&previousAttempt, &previousGeneration)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return SourceCrawlClaim{}, err
	}
	generation, err := sourceCrawlNextGenerationTx(ctx, tx, db, source.SourceID, previousAttempt, previousGeneration)
	if err != nil {
		return SourceCrawlClaim{}, err
	}
	attemptID, err := newSourceCrawlAttemptID(nil)
	if err != nil {
		return SourceCrawlClaim{}, err
	}
	leaseExpires := now.Add(req.Lease)

	if previousAttempt != "" {
		if _, err := tx.ExecContext(ctx,
			`UPDATE SourceCrawlAttempts
			 SET state='superseded', superseded_by_generation=`+p(1)+`,
			     terminal_at=`+p(2)+`, last_updated_at=`+p(3)+`
			 WHERE attempt_id=`+p(4)+` AND source_id=`+p(5)+` AND state='claimed'`,
			generation, now, now, previousAttempt, source.SourceID); err != nil {
			return SourceCrawlClaim{}, err
		}
	}

	if normalizeDBMS(db.DBMS()) == "postgres" {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO SourceCrawlClaims
			 (source_id, attempt_id, claim_request_id, generation, engine_id, claimed_at, lease_expires_at, last_updated_at)
			 VALUES (`+p(1)+`,`+p(2)+`,`+p(3)+`,`+p(4)+`,`+p(5)+`,`+p(6)+`,`+p(7)+`,`+p(8)+`)
			 ON CONFLICT (source_id) DO UPDATE SET
			   attempt_id=EXCLUDED.attempt_id, claim_request_id=EXCLUDED.claim_request_id,
			   generation=EXCLUDED.generation, engine_id=EXCLUDED.engine_id,
			   claimed_at=EXCLUDED.claimed_at, lease_expires_at=EXCLUDED.lease_expires_at,
			   last_updated_at=EXCLUDED.last_updated_at`,
			source.SourceID, attemptID, req.ClaimRequestID, generation, req.EngineID, now, leaseExpires, now)
	} else {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO SourceCrawlClaims
			 (source_id, attempt_id, claim_request_id, generation, engine_id, claimed_at, lease_expires_at, last_updated_at)
			 VALUES (?,?,?,?,?,?,?,?)
			 ON CONFLICT (source_id) DO UPDATE SET
			   attempt_id=excluded.attempt_id, claim_request_id=excluded.claim_request_id,
			   generation=excluded.generation, engine_id=excluded.engine_id,
			   claimed_at=excluded.claimed_at, lease_expires_at=excluded.lease_expires_at,
			   last_updated_at=excluded.last_updated_at`,
			source.SourceID, attemptID, req.ClaimRequestID, generation, req.EngineID, now, leaseExpires, now)
	}
	if err != nil {
		return SourceCrawlClaim{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO SourceCrawlAttempts
		 (attempt_id, claim_request_id, source_id, generation, engine_id, state,
		  claimed_at, lease_expires_at, created_at, last_updated_at)
		 VALUES (`+p(1)+`,`+p(2)+`,`+p(3)+`,`+p(4)+`,`+p(5)+`,'claimed',`+p(6)+`,`+p(7)+`,`+p(8)+`,`+p(9)+`)`,
		attemptID, req.ClaimRequestID, source.SourceID, generation, req.EngineID, now, leaseExpires, now, now); err != nil {
		return SourceCrawlClaim{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE Sources SET status='processing', engine=`+p(1)+`, last_updated_at=CURRENT_TIMESTAMP
		 WHERE source_id=`+p(2), req.EngineID, source.SourceID); err != nil {
		return SourceCrawlClaim{}, err
	}

	claim := SourceCrawlClaim{
		Outcome: SourceCrawlUnattributed, SourceID: source.SourceID,
		ClaimRequestID: req.ClaimRequestID,
		AttemptID:      attemptID, Generation: generation, EngineID: req.EngineID,
		ClaimedAt: now, LeaseExpiresAt: leaseExpires,
	}
	candidate, outcome, err := resolveSourceCrawlCandidateTx(ctx, tx, db, source, claim)
	if err != nil {
		return SourceCrawlClaim{}, err
	}
	claim.Outcome = outcome
	if candidate == nil || outcome == SourceCrawlClaimConflict || outcome == SourceCrawlAmbiguous || outcome == SourceCrawlNotAttributable {
		return claim, nil
	}
	claim.OperationID = candidate.OperationID
	claim.RequestID = candidate.RequestID
	claim.RunID = candidate.RunID
	claim.AgentID = candidate.AgentID
	claim.Owner = candidate.Owner
	claim.PrincipalUserID = candidate.PrincipalUserID
	claim.ApprovalID = candidate.ApprovalID
	claim.ApprovalSHA256 = candidate.ApprovalSHA256
	if _, err := tx.ExecContext(ctx,
		`UPDATE SourceCrawlAttempts SET operation_id=`+p(1)+`, request_id=`+p(2)+`,
		 run_id=`+p(3)+`, agent_id=`+p(4)+`, owner=`+p(5)+`,
		 principal_user_id=`+p(6)+`, approval_id=`+p(7)+`, approval_sha256=`+p(8)+`,
		 last_updated_at=`+p(9)+`
		 WHERE attempt_id=`+p(10)+` AND source_id=`+p(11)+` AND state='claimed'`,
		candidate.OperationID, candidate.RequestID, candidate.RunID, candidate.AgentID,
		candidate.Owner, candidate.PrincipalUserID, candidate.ApprovalID,
		candidate.ApprovalSHA256, now, attemptID, source.SourceID); err != nil {
		return SourceCrawlClaim{}, err
	}
	return claim, nil
}

type sourceCrawlCandidate struct {
	OperationID     string
	RequestID       string
	RunID           string
	AgentID         string
	Owner           string
	PrincipalUserID uint64
	ApprovalID      string
	ApprovalSHA256  string
}

type sourceCrawlStatusRow struct {
	Status        string
	AgentID       string
	SourceID      uint64
	OperationType string
	Details       []byte
	CreatedAt     time.Time
	DeletedAt     sql.NullTime
}

func sourceCrawlSubmittedProposalsTx(ctx context.Context, tx *sql.Tx, db Handler, sourceID uint64) ([]CrawlProposalRow, error) {
	p := func(n int) string { return sourceCrawlPlaceholder(db, n) }
	rows, err := tx.QueryContext(ctx,
		`SELECT `+crawlProposalColumns()+` FROM CrawlProposals
		 WHERE source_id=`+p(1)+` AND state='submitted'
		 ORDER BY request_id`, sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []CrawlProposalRow
	for rows.Next() {
		row, err := scanCrawlProposalRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func sourceCrawlOperationStatusTx(ctx context.Context, tx *sql.Tx, db Handler, operationID string) (sourceCrawlStatusRow, bool, error) {
	p := func(n int) string { return sourceCrawlPlaceholder(db, n) }
	var row sourceCrawlStatusRow
	err := tx.QueryRowContext(ctx,
		`SELECT status, COALESCE(agent_id,''), COALESCE(source_id,0),
		        COALESCE(operation_type,''), details, created_at, deleted_at
		 FROM Status WHERE status_sha256=`+p(1), operationID).Scan(
		&row.Status, &row.AgentID, &row.SourceID, &row.OperationType,
		&row.Details, &row.CreatedAt, &row.DeletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return sourceCrawlStatusRow{}, false, nil
	}
	if err != nil {
		return sourceCrawlStatusRow{}, false, err
	}
	return row, true, nil
}

func resolveSourceCrawlCandidateTx(ctx context.Context, tx *sql.Tx, db Handler, source sourceCrawlSourceRow, claim SourceCrawlClaim) (*sourceCrawlCandidate, SourceCrawlClaimOutcome, error) {
	proposals, err := sourceCrawlSubmittedProposalsTx(ctx, tx, db, source.SourceID)
	if err != nil {
		return nil, "", err
	}
	if len(proposals) == 0 {
		return nil, SourceCrawlUnattributed, nil
	}
	if len(proposals) > 1 {
		// Every submitted operation competes for this source. A malformed
		// competing row is still competition and must block attribution.
		return nil, SourceCrawlAmbiguous, nil
	}
	proposal := proposals[0]
	if !sourceCrawlProposalValid(proposal) {
		return nil, SourceCrawlNotAttributable, nil
	}
	operationID := proposal.OperationID.String
	if CrawlOperationKey(proposal.Owner, proposal.AgentID, proposal.SourceID,
		proposal.OperationType, proposal.RequestID) != operationID {
		return nil, SourceCrawlNotAttributable, nil
	}
	status, found, err := sourceCrawlOperationStatusTx(ctx, tx, db, operationID)
	if err != nil {
		return nil, "", err
	}
	if !found || status.DeletedAt.Valid {
		return nil, SourceCrawlNotAttributable, nil
	}
	if status.SourceID != proposal.SourceID ||
		status.AgentID != proposal.AgentID ||
		status.OperationType != proposal.OperationType {
		return nil, SourceCrawlNotAttributable, nil
	}
	details, err := decodeSourceCrawlOperationDetails(status.Details)
	if err != nil {
		return nil, SourceCrawlNotAttributable, nil
	}
	approval, found, err := getCrawlApprovalByIDTx(ctx, tx, db, details.ApprovalID)
	if err != nil {
		return nil, "", err
	}
	if !found || !sourceCrawlApprovalMatchesProposal(approval, proposal, details) {
		return nil, SourceCrawlNotAttributable, nil
	}
	if !sourceCrawlDetailsMatchProposal(details, proposal) ||
		!sourceCrawlDetailsMatchApproval(details, approval) {
		return nil, SourceCrawlNotAttributable, nil
	}
	if !sourceCrawlAcceptanceChronology(status, proposal, approval, details) {
		return nil, SourceCrawlNotAttributable, nil
	}
	if status.Status != string(CrawlOperationAccepted) {
		if status.Status == string(CrawlOperationAlreadyPending) {
			// already_pending is a scheduling acknowledgement, not ownership
			// of the crawler attempt that is actually running.
			return nil, SourceCrawlUnattributed, nil
		}
		return nil, SourceCrawlNotAttributable, nil
	}
	var active int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM SourceCrawlAttempts
		 WHERE operation_id=`+sourceCrawlPlaceholder(db, 1)+`
		   AND state='claimed' AND attempt_id<>`+sourceCrawlPlaceholder(db, 2),
		operationID, claim.AttemptID).Scan(&active); err != nil {
		return nil, "", err
	}
	if active > 0 {
		return nil, SourceCrawlClaimConflict, nil
	}
	verified, err := sourceCrawlVerifiedOwnerTx(ctx, tx, db, source.SourceID, proposal.PrincipalUserID)
	if err != nil {
		return nil, "", err
	}
	if source.UserID == 0 || source.UserID != proposal.PrincipalUserID || !verified {
		return nil, SourceCrawlNotAttributable, nil
	}
	return &sourceCrawlCandidate{
		OperationID: operationID, RequestID: proposal.RequestID,
		RunID: proposal.RunID, AgentID: proposal.AgentID, Owner: proposal.Owner,
		PrincipalUserID: proposal.PrincipalUserID,
		ApprovalID:      approval.ApprovalID, ApprovalSHA256: approval.ApprovalSHA256,
	}, SourceCrawlClaimed, nil
}

func sourceCrawlProposalValid(p CrawlProposalRow) bool {
	if !p.OperationID.Valid || !sourceCrawlHashPattern.MatchString(p.OperationID.String) ||
		!sourceCrawlHashPattern.MatchString(p.RequestID) ||
		!sourceCrawlHashPattern.MatchString(p.ProposalSHA256) {
		return false
	}
	if strings.TrimSpace(p.RunID) == "" || len(p.RunID) > 256 ||
		strings.TrimSpace(p.AgentID) == "" || len(p.AgentID) > 256 ||
		strings.TrimSpace(p.Owner) == "" || len(p.Owner) > 256 ||
		p.PrincipalUserID == 0 || p.SourceID == 0 ||
		p.OperationType != CrawlOperationType || p.ExpiresAt.IsZero() {
		return false
	}
	return true
}

func sourceCrawlApprovalMatchesProposal(approval CrawlApprovalRow, proposal CrawlProposalRow, details crawlOperationDetails) bool {
	return approval.ProposalSHA256 == proposal.ProposalSHA256 &&
		approval.RequestID == proposal.RequestID &&
		approval.RunID == proposal.RunID &&
		approval.AgentID == proposal.AgentID &&
		approval.Owner == proposal.Owner &&
		approval.PrincipalUserID == proposal.PrincipalUserID &&
		approval.SourceID == proposal.SourceID &&
		approval.OperationType == proposal.OperationType &&
		approval.ApprovalID == details.ApprovalID &&
		approval.ApprovalSHA256 == details.ApprovalSHA256 &&
		approval.ApprovedBy == details.ApprovedBy &&
		approval.ApprovedByUserID == details.ApprovedByUserID
}

func sourceCrawlDetailsMatchProposal(details crawlOperationDetails, proposal CrawlProposalRow) bool {
	return details.RequestID == proposal.RequestID &&
		details.RunID == proposal.RunID &&
		details.PrincipalUserID == proposal.PrincipalUserID &&
		details.AgentID == proposal.AgentID &&
		details.Owner == proposal.Owner &&
		details.SourceID == proposal.SourceID &&
		details.OperationType == proposal.OperationType &&
		details.ParametersSHA256 == proposal.ProposalSHA256
}

func sourceCrawlDetailsMatchApproval(details crawlOperationDetails, approval CrawlApprovalRow) bool {
	return details.ApprovalID == approval.ApprovalID &&
		details.ApprovalSHA256 == approval.ApprovalSHA256 &&
		details.ApprovedBy == approval.ApprovedBy &&
		details.ApprovedByUserID == approval.ApprovedByUserID
}

func sourceCrawlTimeNear(left, right time.Time) bool {
	delta := left.Sub(right)
	if delta < 0 {
		delta = -delta
	}
	return delta <= 2*time.Microsecond
}

func sourceCrawlAcceptanceChronology(status sourceCrawlStatusRow, proposal CrawlProposalRow,
	approval CrawlApprovalRow, details crawlOperationDetails) bool {
	approvedAt, err := time.Parse(time.RFC3339Nano, details.ApprovedAt)
	if err != nil || approvedAt.IsZero() ||
		!sourceCrawlTimeNear(approvedAt, approval.ApprovedAt) {
		return false
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, details.ExpiresAt)
	if err != nil || expiresAt.IsZero() {
		return false
	}
	expectedExpiry := proposal.ExpiresAt.UTC()
	if approval.ExpiresAt.Before(expectedExpiry) {
		expectedExpiry = approval.ExpiresAt.UTC()
	}
	if !sourceCrawlTimeNear(expiresAt, expectedExpiry) {
		return false
	}
	if status.CreatedAt.Before(approval.ApprovedAt) ||
		!status.CreatedAt.Before(expiresAt) ||
		!status.CreatedAt.Before(proposal.ExpiresAt) ||
		!status.CreatedAt.Before(approval.ExpiresAt) ||
		!status.CreatedAt.Before(approval.ProposalExpiresAt) {
		return false
	}
	if approval.RevokedAt.Valid && !approval.RevokedAt.Time.After(status.CreatedAt) {
		return false
	}
	return true
}

const (
	maxSourceCrawlDetailsBytes = 64 << 10
	maxSourceCrawlJSONDepth    = 64
)

var errSourceCrawlJSONDuplicateKey = errors.New("duplicate JSON key")

func decodeSourceCrawlOperationDetails(raw []byte) (crawlOperationDetails, error) {
	if len(raw) == 0 || len(raw) > maxSourceCrawlDetailsBytes {
		return crawlOperationDetails{}, ErrSourceCrawlClaimCorrupt
	}
	if err := validateSourceCrawlJSONShape(raw); err != nil {
		return crawlOperationDetails{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var details crawlOperationDetails
	if err := decoder.Decode(&details); err != nil {
		return crawlOperationDetails{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return crawlOperationDetails{}, ErrSourceCrawlClaimCorrupt
	}
	if strings.TrimSpace(details.RequestID) == "" ||
		strings.TrimSpace(details.RunID) == "" ||
		strings.TrimSpace(details.AgentID) == "" ||
		strings.TrimSpace(details.Owner) == "" ||
		details.PrincipalUserID == 0 || details.SourceID == 0 ||
		details.OperationType != string(CrawlOperationType) ||
		!sourceCrawlHashPattern.MatchString(details.ParametersSHA256) ||
		!sourceCrawlHashPattern.MatchString(details.ApprovalID) ||
		!sourceCrawlHashPattern.MatchString(details.ApprovalSHA256) ||
		strings.TrimSpace(details.ApprovedBy) == "" || details.ApprovedByUserID == 0 ||
		strings.TrimSpace(details.ApprovedAt) == "" || strings.TrimSpace(details.ExpiresAt) == "" {
		return crawlOperationDetails{}, ErrSourceCrawlClaimCorrupt
	}
	if details.ReasonCode != "" && !sourceCrawlReasonPattern.MatchString(details.ReasonCode) {
		return crawlOperationDetails{}, ErrSourceCrawlClaimCorrupt
	}
	return details, nil
}

func validateSourceCrawlJSONShape(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := scanSourceCrawlJSONValue(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return ErrSourceCrawlClaimCorrupt
	}
	return nil
}

func scanSourceCrawlJSONValue(decoder *json.Decoder, depth int) error {
	if depth > maxSourceCrawlJSONDepth {
		return ErrSourceCrawlClaimCorrupt
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return ErrSourceCrawlClaimCorrupt
			}
			if seen[key] {
				return errSourceCrawlJSONDuplicateKey
			}
			seen[key] = true
			if err := scanSourceCrawlJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		_, err := decoder.Token()
		return err
	case '[':
		for decoder.More() {
			if err := scanSourceCrawlJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		_, err := decoder.Token()
		return err
	default:
		return ErrSourceCrawlClaimCorrupt
	}
}

func sourceCrawlVerifiedOwnerTx(ctx context.Context, tx *sql.Tx, db Handler, sourceID, principalUserID uint64) (bool, error) {
	p := func(n int) string { return sourceCrawlPlaceholder(db, n) }
	var provenanceUser uint64
	err := tx.QueryRowContext(ctx,
		`SELECT user_id FROM SourceOwnership WHERE source_id=`+p(1), sourceID).Scan(&provenanceUser)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return provenanceUser == principalUserID, nil
}

func loadSourceCrawlClaimRequestTx(ctx context.Context, tx *sql.Tx, db Handler, claimRequestID string, now time.Time) ([]SourceCrawlClaim, bool, error) {
	return loadSourceCrawlClaimRequestWithAt(ctx, tx, db, claimRequestID, now)
}

// loadSourceCrawlClaimRequest reads a persisted claim request for commit-outcome
// reconciliation. It classifies execution eligibility with the database clock,
// not the reconnecting worker's wall clock.
func loadSourceCrawlClaimRequest(ctx context.Context, db Handler, claimRequestID string) ([]SourceCrawlClaim, bool, error) {
	now, err := sourceCrawlClaimDatabaseNow(ctx, db, db)
	if err != nil {
		return nil, false, err
	}
	return loadSourceCrawlClaimRequestWithAt(ctx, db, db, claimRequestID, now)
}

type sourceCrawlClaimQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// sourceCrawlClaimDatabaseNow returns the authoritative clock used to decide
// whether a persisted attempt may still execute. Workers must not substitute
// their own wall clock for this decision before a privileged mutation.
//
// PostgreSQL CURRENT_TIMESTAMP is transaction-start time. Claim allocation and
// replay decisions need a fresh value after deliberate lock waits, so this uses
// clock_timestamp(). SQLite's julianday('now') is evaluated by the statement,
// not at transaction start. Both results are truncated to microseconds and the
// expiry boundary is exclusive: now == lease_expires_at is expired.
func sourceCrawlClaimDatabaseNow(ctx context.Context, q sourceCrawlClaimQueryer, db Handler) (time.Time, error) {
	if normalizeDBMS(db.DBMS()) == "postgres" {
		var now time.Time
		if err := q.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
			return time.Time{}, fmt.Errorf("%w: database clock", ErrSourceCrawlClaimUnavailable)
		}
		if now.IsZero() {
			return time.Time{}, fmt.Errorf("%w: database clock is zero", ErrSourceCrawlClaimUnavailable)
		}
		return now.UTC().Truncate(time.Microsecond), nil
	}
	var julian float64
	if err := q.QueryRowContext(ctx, `SELECT julianday('now')`).Scan(&julian); err != nil {
		return time.Time{}, fmt.Errorf("%w: database clock", ErrSourceCrawlClaimUnavailable)
	}
	const unixEpochJulianDay = 2440587.5
	seconds := (julian - unixEpochJulianDay) * 86400
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return time.Time{}, fmt.Errorf("%w: database clock is invalid", ErrSourceCrawlClaimUnavailable)
	}
	return time.Unix(0, int64(math.Round(seconds*float64(time.Second)))).UTC().Truncate(time.Microsecond), nil
}

type sourceCrawlPersistedBinding struct {
	OperationID     string
	RequestID       string
	RunID           string
	AgentID         string
	Owner           string
	PrincipalUserID uint64
	ApprovalID      string
	ApprovalSHA256  string
}

func sourceCrawlDecodePersistedBinding(sourceID uint64, operation, request, run, agent, owner,
	approval, approvalSHA sql.NullString, principal sql.NullInt64) (sourceCrawlPersistedBinding, bool, error) {
	present := 0
	for _, value := range []sql.NullString{operation, request, run, agent, owner, approval, approvalSHA} {
		if value.Valid {
			present++
		}
	}
	if principal.Valid {
		present++
	}
	if present == 0 {
		return sourceCrawlPersistedBinding{}, false, nil
	}
	if present != 8 {
		return sourceCrawlPersistedBinding{}, false,
			fmt.Errorf("%w: partial attempt operation binding", ErrSourceCrawlClaimCorrupt)
	}
	binding := sourceCrawlPersistedBinding{
		OperationID: operation.String, RequestID: request.String,
		RunID: run.String, AgentID: agent.String, Owner: owner.String,
		ApprovalID: approval.String, ApprovalSHA256: approvalSHA.String,
	}
	if principal.Int64 <= 0 {
		return sourceCrawlPersistedBinding{}, false,
			fmt.Errorf("%w: invalid attempt operation principal", ErrSourceCrawlClaimCorrupt)
	}
	binding.PrincipalUserID = uint64(principal.Int64)
	if !sourceCrawlHashPattern.MatchString(binding.OperationID) ||
		!sourceCrawlHashPattern.MatchString(binding.RequestID) ||
		!sourceCrawlHashPattern.MatchString(binding.ApprovalID) ||
		!sourceCrawlHashPattern.MatchString(binding.ApprovalSHA256) ||
		strings.TrimSpace(binding.RunID) == "" || len(binding.RunID) > 256 ||
		strings.TrimSpace(binding.AgentID) == "" || len(binding.AgentID) > 256 ||
		strings.TrimSpace(binding.Owner) == "" || len(binding.Owner) > 256 {
		return sourceCrawlPersistedBinding{}, false,
			fmt.Errorf("%w: malformed attempt operation binding", ErrSourceCrawlClaimCorrupt)
	}
	if CrawlOperationKey(binding.Owner, binding.AgentID, sourceID, string(CrawlOperationType),
		binding.RequestID) != binding.OperationID {
		return sourceCrawlPersistedBinding{}, false,
			fmt.Errorf("%w: attempt operation identity is inconsistent", ErrSourceCrawlClaimCorrupt)
	}
	return binding, true, nil
}

func sourceCrawlReplayOutcome(now time.Time, claim SourceCrawlClaim, state string, current, bound bool) SourceCrawlClaimOutcome {
	if state != "claimed" || !current {
		return SourceCrawlClaimStaleFence
	}
	if !now.Before(claim.LeaseExpiresAt) {
		return SourceCrawlClaimStaleFence
	}
	if bound {
		return SourceCrawlClaimed
	}
	return SourceCrawlUnattributed
}

func loadSourceCrawlClaimRequestWithAt(ctx context.Context, q sourceCrawlClaimQueryer, db Handler,
	claimRequestID string, now time.Time) ([]SourceCrawlClaim, bool, error) {
	if now.IsZero() {
		return nil, false, fmt.Errorf("%w: replay clock is zero", ErrSourceCrawlClaimUnavailable)
	}
	now = now.UTC().Truncate(time.Microsecond)
	p := func(n int) string { return sourceCrawlPlaceholder(db, n) }
	rows, err := q.QueryContext(ctx,
		`SELECT attempt_id, source_id, generation, engine_id, state, claimed_at, lease_expires_at,
		 operation_id, request_id, run_id, agent_id, owner, principal_user_id,
		 approval_id, approval_sha256
		 FROM SourceCrawlAttempts WHERE claim_request_id=`+p(1)+` ORDER BY source_id`, claimRequestID)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close() //nolint:errcheck
	var out []SourceCrawlClaim
	type scanned struct {
		claim   SourceCrawlClaim
		state   string
		binding sourceCrawlPersistedBinding
		bound   bool
	}
	scannedRows := make([]scanned, 0)
	for rows.Next() {
		var c SourceCrawlClaim
		var state string
		var operation, request, run, agent, owner, approval, approvalSHA sql.NullString
		var principal sql.NullInt64
		if err := rows.Scan(&c.AttemptID, &c.SourceID, &c.Generation, &c.EngineID, &state,
			&c.ClaimedAt, &c.LeaseExpiresAt, &operation, &request, &run, &agent,
			&owner, &principal, &approval, &approvalSHA); err != nil {
			return nil, false, err
		}
		binding, bound, err := sourceCrawlDecodePersistedBinding(c.SourceID,
			operation, request, run, agent, owner, approval, approvalSHA, principal)
		if err != nil {
			return nil, false, err
		}
		c.Reused = true
		c.ClaimRequestID = claimRequestID
		if bound {
			c.OperationID = binding.OperationID
			c.RequestID = binding.RequestID
			c.RunID = binding.RunID
			c.AgentID = binding.AgentID
			c.Owner = binding.Owner
			c.PrincipalUserID = binding.PrincipalUserID
			c.ApprovalID = binding.ApprovalID
			c.ApprovalSHA256 = binding.ApprovalSHA256
		}
		scannedRows = append(scannedRows, scanned{claim: c, state: state, binding: binding, bound: bound})
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if err := rows.Close(); err != nil {
		return nil, false, err
	}
	for _, row := range scannedRows {
		c := row.claim
		current, err := sourceCrawlClaimIsCurrent(ctx, q, db, c.SourceID, c.AttemptID, c.Generation)
		if err != nil {
			return nil, false, err
		}
		c.Outcome = sourceCrawlReplayOutcome(now, c, row.state, current, row.bound)
		out = append(out, c)
	}
	return out, len(out) > 0, nil
}

func sourceCrawlClaimIsCurrent(ctx context.Context, q sourceCrawlClaimQueryer, db Handler, sourceID uint64, attemptID string, generation uint64) (bool, error) {
	p := func(n int) string { return sourceCrawlPlaceholder(db, n) }
	var count int
	if err := q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM SourceCrawlClaims WHERE source_id=`+p(1)+` AND attempt_id=`+p(2)+` AND generation=`+p(3),
		sourceID, attemptID, generation).Scan(&count); err != nil {
		return false, err
	}
	return count == 1, nil
}

// SourceCrawlClaimModePolicy reads only the operator-owned cutover mode after
// proving that the optional attempt objects exist. It is the command-level
// dispatch check: a present but unreadable policy fails closed, while a
// genuinely uninstalled optional capability is reported as absent by the
// caller. Full structural readiness is still enforced before a claim.
func SourceCrawlClaimModePolicy(ctx context.Context, db *Handler) (string, error) {
	if db == nil || *db == nil {
		return "", fmt.Errorf("database handler is nil")
	}
	if !SourceCrawlAttemptSupported((*db).DBMS()) {
		return "", ErrSourceCrawlAttemptUnsupported
	}
	present, err := SourceCrawlAttemptSchemaPresent(ctx, db)
	if err != nil {
		return "", fmt.Errorf("%w: policy presence: %v", ErrSourceCrawlClaimUnavailable, err)
	}
	if !present {
		return "", fmt.Errorf("%w: policy tables", ErrSourceCrawlAttemptSchemaUnavailable)
	}
	mode, err := sourceCrawlClaimMode(ctx, *db)
	if err != nil {
		// The objects exist, so an unreadable or missing policy row is a
		// corrupt fenced claim store, never an uninstalled optional capability.
		return "", fmt.Errorf("%w: policy row: %v", ErrSourceCrawlClaimUnavailable, err)
	}
	return mode, nil
}

// SourceCrawlClaimMode is a read-only view of the operator-owned cutover mode.
func SourceCrawlClaimMode(ctx context.Context, db *Handler) (string, error) {
	if db == nil || *db == nil {
		return "", fmt.Errorf("database handler is nil")
	}
	if !SourceCrawlAttemptSupported((*db).DBMS()) {
		return "", ErrSourceCrawlAttemptUnsupported
	}
	if err := CheckSourceCrawlAttemptSchema(ctx, db); err != nil {
		return "", err
	}
	return sourceCrawlClaimMode(ctx, *db)
}
