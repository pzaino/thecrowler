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

// Internal, opt-in fenced source-crawl result repository (WP-05B3C3 Stage B1).
// It uses only the existing schema-1.17 SourceCrawlClaims, SourceCrawlAttempts,
// SourceCrawlClaimPolicy, Sources, SourceOwnership, CrawlProposals,
// CrawlApprovals and Status objects. No production caller is installed here;
// the methods are exercised only by package tests until Stage B2 wires the
// handoff into the crawler.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	sourceCrawlMaxLeaseExtension = time.Hour
	sourceCrawlMaxAttemptLease   = 24 * time.Hour
)

var (
	errSourceCrawlResultStale       = errors.New("source crawl result fence is stale")
	errSourceCrawlResultConflict    = errors.New("source crawl result conflicts with durable authority")
	errSourceCrawlResultUnavailable = errors.New("source crawl result store is unavailable")
	errSourceCrawlResultUnsupported = errors.New("source crawl result store is unsupported")
)

// sourceCrawlLeaseRenewalRequest is trusted internal input. The identity fields
// come from the claim returned by ClaimSourceCrawlAttempts, never from a model,
// event, page or manifest.
type sourceCrawlLeaseRenewalRequest struct {
	SourceID       uint64
	AttemptID      string
	Generation     uint64
	ClaimRequestID string
	EngineID       string
	Extension      time.Duration
}

// sourceCrawlLeaseRenewalResult is the closed heartbeat result. Claim is the
// authoritative identity and resulting lease when Result is applied or
// idempotent_replay.
type sourceCrawlLeaseRenewalResult struct {
	Result sourceCrawlTerminalReportResult
	Reason string
	Claim  SourceCrawlClaim
}

// sourceCrawlTerminalExecutionResult is the closed terminal-report result.
// Attempt is the authoritative history snapshot; for a replay it is the stored
// terminal attempt.
type sourceCrawlTerminalExecutionResult struct {
	Result              sourceCrawlTerminalReportResult
	Reason              string
	Attempt             sourceCrawlAttempt
	OperationState      CrawlOperationState
	SourceStatusChanged bool
}

type sourceCrawlCurrentClaim struct {
	AttemptID      string
	ClaimRequestID string
	Generation     uint64
	EngineID       string
	ClaimedAt      time.Time
	LeaseExpiresAt time.Time
}

type sourceCrawlResultSource struct {
	UserID   uint64
	Disabled bool
	Deleted  bool
	Status   string
}

func (c sourceCrawlCurrentClaim) matchesAttempt(a sourceCrawlAttempt) bool {
	return c.AttemptID == a.AttemptID &&
		c.ClaimRequestID == a.ClaimRequestID &&
		c.Generation == a.Generation &&
		c.EngineID == a.EngineID &&
		c.ClaimedAt.Equal(a.ClaimedAt) &&
		c.LeaseExpiresAt.Equal(a.LeaseExpiresAt)
}

func sourceCrawlResultClass(err error) sourceCrawlTerminalReportResult {
	switch {
	case errors.Is(err, errSourceCrawlResultStale),
		errors.Is(err, errSourceCrawlAttemptStale),
		errors.Is(err, errSourceCrawlAttemptNotOwner):
		return sourceCrawlTerminalStaleFence
	case errors.Is(err, errSourceCrawlResultConflict),
		errors.Is(err, errSourceCrawlTerminalConflict),
		errors.Is(err, errSourceCrawlTerminalCorrupt),
		errors.Is(err, ErrSourceCrawlClaimCorrupt),
		errors.Is(err, errSourceCrawlAttemptInvalid),
		errors.Is(err, ErrSourceCrawlClaimConflict):
		return sourceCrawlTerminalConflict
	case errors.Is(err, errSourceCrawlResultUnsupported),
		errors.Is(err, errSourceCrawlTerminalUnsupported):
		return sourceCrawlTerminalUnsupported
	case errors.Is(err, errSourceCrawlTerminalUnavailable):
		return sourceCrawlTerminalUnavailable
	default:
		return sourceCrawlTerminalUnavailable
	}
}

func sourceCrawlLoadResultClass(err error) sourceCrawlTerminalReportResult {
	if errors.Is(err, ErrSourceCrawlClaimCorrupt) ||
		errors.Is(err, errSourceCrawlResultConflict) ||
		errors.Is(err, errSourceCrawlAttemptInvalid) {
		return sourceCrawlTerminalConflict
	}
	return sourceCrawlTerminalUnavailable
}

func sourceCrawlFixedClock(now time.Time) sourceCrawlClaimClock {
	return func(context.Context, *sql.Tx, Handler) (time.Time, error) {
		return now, nil
	}
}

// sourceCrawlBeginResultTx opens a bounded transaction, acquires the SQLite
// writer lock when needed, takes the shared PostgreSQL policy lock and confirms
// fenced mode. It returns a classified result when the mutation must stop
// before any row is read.
func sourceCrawlBeginResultTx(ctx context.Context, db *Handler) (
	context.Context, *sql.Tx, context.CancelFunc, sourceCrawlTerminalReportResult, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil || *db == nil {
		return nil, nil, nil, sourceCrawlTerminalUnavailable, "database_unavailable",
			fmt.Errorf("source crawl result database is unavailable")
	}
	if !SourceCrawlAttemptSupported((*db).DBMS()) {
		return nil, nil, nil, sourceCrawlTerminalUnsupported, "backend_unsupported",
			errSourceCrawlResultUnsupported
	}
	if err := CheckSourceCrawlAttemptSchema(ctx, db); err != nil {
		if errors.Is(err, ErrSourceCrawlAttemptSchemaUnavailable) {
			return nil, nil, nil, sourceCrawlTerminalUnsupported, "schema_unavailable", err
		}
		return nil, nil, nil, sourceCrawlTerminalUnavailable, "schema_unavailable", err
	}
	txCtx, cancel := TransactionContextForHandler(ctx, db)
	tx, err := (*db).BeginTx(txCtx, nil)
	if err != nil {
		cancel()
		return nil, nil, nil, sourceCrawlTerminalUnavailable, "transaction_unavailable",
			NormalizeTransactionError(txCtx, err)
	}
	fail := func(class sourceCrawlTerminalReportResult, reason string, cause error) (
		context.Context, *sql.Tx, context.CancelFunc, sourceCrawlTerminalReportResult, string, error) {
		_ = tx.Rollback()
		cancel()
		return nil, nil, nil, class, reason, cause
	}
	if normalizeDBMS((*db).DBMS()) != "postgres" {
		if _, err := tx.ExecContext(txCtx,
			`UPDATE SourceCrawlClaims SET last_updated_at = last_updated_at WHERE 0`); err != nil {
			return fail(sourceCrawlTerminalUnavailable, "writer_lock_unavailable",
				fmt.Errorf("%w: acquire claim writer lock", ErrSourceCrawlClaimUnavailable))
		}
	}
	mode, err := sourceCrawlClaimModeTx(txCtx, tx, *db)
	if err != nil {
		return fail(sourceCrawlTerminalUnavailable, "policy_unavailable", err)
	}
	if mode != "fenced" {
		return fail(sourceCrawlTerminalUnsupported, "legacy_mode", ErrSourceCrawlClaimLegacyMode)
	}
	return txCtx, tx, cancel, "", "", nil
}

func sourceCrawlLockResultSourceTx(ctx context.Context, tx *sql.Tx, db Handler,
	sourceID uint64) (sourceCrawlResultSource, bool, error) {
	p := func(n int) string { return sourceCrawlPlaceholder(db, n) }
	query := `SELECT usr_id, disabled, deleted_at, status FROM Sources WHERE source_id = ` + p(1)
	if normalizeDBMS(db.DBMS()) == "postgres" {
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

func sourceCrawlLockCurrentClaimTx(ctx context.Context, tx *sql.Tx, db Handler,
	sourceID uint64) (sourceCrawlCurrentClaim, bool, error) {
	p := func(n int) string { return sourceCrawlPlaceholder(db, n) }
	query := `SELECT attempt_id, claim_request_id, generation, engine_id,
		claimed_at, lease_expires_at FROM SourceCrawlClaims WHERE source_id = ` + p(1)
	if normalizeDBMS(db.DBMS()) == "postgres" {
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
			fmt.Errorf("%w: current claim generation", errSourceCrawlResultConflict)
	}
	current.Generation = uint64(generation)
	current.ClaimedAt = current.ClaimedAt.UTC().Truncate(time.Microsecond)
	current.LeaseExpiresAt = current.LeaseExpiresAt.UTC().Truncate(time.Microsecond)
	return current, true, nil
}

func sourceCrawlLoadAttemptTx(ctx context.Context, tx *sql.Tx, db Handler,
	attemptID string, sourceID, generation uint64, lock bool) (sourceCrawlAttempt, bool, error) {
	p := func(n int) string { return sourceCrawlPlaceholder(db, n) }
	query := `SELECT attempt_id, claim_request_id, source_id, generation, engine_id,
		state, claimed_at, lease_expires_at, superseded_by_generation, terminal_at,
		COALESCE(reason_code, ''), operation_id, request_id, run_id, agent_id,
		owner, principal_user_id, approval_id, approval_sha256
		FROM SourceCrawlAttempts
		WHERE attempt_id = ` + p(1) + ` AND source_id = ` + p(2) + ` AND generation = ` + p(3)
	if lock && normalizeDBMS(db.DBMS()) == "postgres" {
		query += ` FOR UPDATE`
	}
	var (
		attempt                        sourceCrawlAttempt
		state, reasonCode              string
		supersededBy                   sql.NullInt64
		terminalAt                     sql.NullTime
		operation, request, run, agent sql.NullString
		owner, approval, approvalSHA   sql.NullString
		principal                      sql.NullInt64
		generationValue                int64
	)
	err := tx.QueryRowContext(ctx, query, attemptID, sourceID, generation).Scan(
		&attempt.AttemptID, &attempt.ClaimRequestID, &attempt.SourceID, &generationValue,
		&attempt.EngineID, &state, &attempt.ClaimedAt, &attempt.LeaseExpiresAt,
		&supersededBy, &terminalAt, &reasonCode, &operation, &request, &run,
		&agent, &owner, &principal, &approval, &approvalSHA)
	if errors.Is(err, sql.ErrNoRows) {
		return sourceCrawlAttempt{}, false, nil
	}
	if err != nil {
		return sourceCrawlAttempt{}, false, err
	}
	if generationValue <= 0 {
		return sourceCrawlAttempt{}, false,
			fmt.Errorf("%w: attempt generation", errSourceCrawlResultConflict)
	}
	attempt.Generation = uint64(generationValue)
	attempt.State = sourceCrawlAttemptState(state)
	attempt.ClaimedAt = attempt.ClaimedAt.UTC().Truncate(time.Microsecond)
	attempt.LeaseExpiresAt = attempt.LeaseExpiresAt.UTC().Truncate(time.Microsecond)
	if supersededBy.Valid {
		if supersededBy.Int64 <= 0 {
			return sourceCrawlAttempt{}, false,
				fmt.Errorf("%w: superseded generation", errSourceCrawlResultConflict)
		}
		attempt.SupersededBy = uint64(supersededBy.Int64)
	}
	if terminalAt.Valid {
		attempt.TerminalAt = terminalAt.Time.UTC().Truncate(time.Microsecond)
	}
	attempt.ReasonCode = reasonCode
	binding, bound, err := sourceCrawlDecodePersistedBinding(sourceID, operation, request,
		run, agent, owner, approval, approvalSHA, principal)
	if err != nil {
		return sourceCrawlAttempt{}, false, err
	}
	if bound {
		attempt.Binding = &sourceCrawlOperationBinding{
			OperationID: binding.OperationID, RequestID: binding.RequestID,
			RunID: binding.RunID, AgentID: binding.AgentID, Owner: binding.Owner,
			PrincipalUserID: binding.PrincipalUserID, ApprovalID: binding.ApprovalID,
			ApprovalSHA256: binding.ApprovalSHA256,
		}
	}
	if err := attempt.validate(); err != nil {
		return sourceCrawlAttempt{}, false, err
	}
	if attempt.State == sourceCrawlAttemptClaimed &&
		(attempt.SupersededBy != 0 || !attempt.TerminalAt.IsZero() || attempt.ReasonCode != "") {
		return sourceCrawlAttempt{}, false,
			fmt.Errorf("%w: claimed attempt carries terminal fields", errSourceCrawlResultConflict)
	}
	return attempt, true, nil
}

func sourceCrawlLoadProposalByRequestTx(ctx context.Context, tx *sql.Tx, db Handler,
	requestID string, sourceID uint64) (CrawlProposalRow, bool, error) {
	p := func(n int) string { return sourceCrawlPlaceholder(db, n) }
	query := `SELECT ` + crawlProposalColumns() + ` FROM CrawlProposals
		WHERE request_id = ` + p(1) + ` AND source_id = ` + p(2)
	row, err := scanCrawlProposalRow(tx.QueryRowContext(ctx, query, requestID, sourceID))
	if errors.Is(err, sql.ErrNoRows) {
		return CrawlProposalRow{}, false, nil
	}
	if err != nil {
		return CrawlProposalRow{}, false, err
	}
	return row, true, nil
}

// sourceCrawlValidateBoundOperationTx revalidates the immutable operation
// binding against the durable proposal, approval and Status rows. Proposal and
// approval reads are intentionally unlocked so this path cannot form a cycle
// with scheduling.
func sourceCrawlValidateBoundOperationTx(ctx context.Context, tx *sql.Tx, db Handler,
	sourceID uint64, binding sourceCrawlOperationBinding,
	now time.Time) (CrawlOperationState, sourceCrawlApprovalHistoryState, error) {
	if err := binding.validate(); err != nil {
		return "", "", fmt.Errorf("%w: %v", errSourceCrawlResultConflict, err)
	}
	proposal, found, err := sourceCrawlLoadProposalByRequestTx(ctx, tx, db, binding.RequestID, sourceID)
	if err != nil {
		return "", "", err
	}
	if !found {
		return "", "", fmt.Errorf("%w: proposal binding", errSourceCrawlResultConflict)
	}
	// A bound terminal report must still have one unambiguous submitted
	// proposal for the source. A later competing proposal makes attribution
	// ambiguous even though the attempt binding is immutable.
	var submitted int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM CrawlProposals WHERE source_id = `+sourceCrawlPlaceholder(db, 1)+
			` AND state = 'submitted'`, sourceID).Scan(&submitted); err != nil {
		return "", "", err
	}
	if submitted != 1 {
		return "", "", fmt.Errorf("%w: competing proposals", errSourceCrawlResultConflict)
	}
	if proposal.State != string(CrawlProposalStateSubmitted) ||
		!proposal.OperationID.Valid || proposal.OperationID.String != binding.OperationID ||
		proposal.RequestID != binding.RequestID || proposal.RunID != binding.RunID ||
		proposal.AgentID != binding.AgentID || proposal.Owner != binding.Owner ||
		proposal.PrincipalUserID != binding.PrincipalUserID || proposal.SourceID != sourceID ||
		proposal.OperationType != string(CrawlOperationType) {
		return "", "", fmt.Errorf("%w: proposal binding", errSourceCrawlResultConflict)
	}
	approval, found, err := getCrawlApprovalByIDTx(ctx, tx, db, binding.ApprovalID)
	if err != nil {
		return "", "", err
	}
	if !found || approval.ProposalSHA256 != proposal.ProposalSHA256 ||
		approval.RequestID != proposal.RequestID || approval.RunID != proposal.RunID ||
		approval.AgentID != proposal.AgentID || approval.Owner != proposal.Owner ||
		approval.PrincipalUserID != proposal.PrincipalUserID ||
		approval.SourceID != sourceID || approval.OperationType != proposal.OperationType ||
		approval.ApprovalSHA256 != binding.ApprovalSHA256 {
		return "", "", fmt.Errorf("%w: approval binding", errSourceCrawlResultConflict)
	}
	status, found, err := sourceCrawlOperationStatusTx(ctx, tx, db, binding.OperationID)
	if err != nil {
		return "", "", err
	}
	if !found || status.DeletedAt.Valid ||
		status.SourceID != sourceID || status.AgentID != proposal.AgentID ||
		status.OperationType != proposal.OperationType {
		return "", "", fmt.Errorf("%w: status binding", errSourceCrawlResultConflict)
	}
	details, err := decodeSourceCrawlOperationDetails(status.Details)
	if err != nil {
		return "", "", fmt.Errorf("%w: operation details", errSourceCrawlResultConflict)
	}
	if !sourceCrawlDetailsMatchProposal(details, proposal) ||
		!sourceCrawlDetailsMatchApproval(details, approval) {
		return "", "", fmt.Errorf("%w: operation details", errSourceCrawlResultConflict)
	}
	if !sourceCrawlAcceptanceChronology(status, proposal, approval, details) {
		return "", "", fmt.Errorf("%w: acceptance chronology", errSourceCrawlResultConflict)
	}
	// The authoritative operation row decides whether a terminal CAS is
	// permitted. Schema 1.17 allows both accepted and processing; the worker
	// does not get to choose or assert one.
	state := CrawlOperationState(status.Status)
	if state != CrawlOperationAccepted && state != CrawlOperationProcessing {
		return "", "", fmt.Errorf("%w: operation state", errSourceCrawlResultConflict)
	}
	history := sourceCrawlApprovalValidAtAcceptance
	switch {
	case approval.RevokedAt.Valid && !approval.RevokedAt.Time.After(status.CreatedAt):
		history = sourceCrawlApprovalRevokedBeforeAcceptance
	case approval.RevokedAt.Valid:
		history = sourceCrawlApprovalRevokedAfterAcceptance
	case !now.Before(approval.ExpiresAt):
		history = sourceCrawlApprovalExpiredAfterAcceptance
	}
	if history == sourceCrawlApprovalRevokedBeforeAcceptance {
		return "", "", fmt.Errorf("%w: approval revoked before acceptance", errSourceCrawlResultConflict)
	}
	return state, history, nil
}

func sourceCrawlTerminalAttemptState(outcome sourceCrawlTerminalOutcome) (sourceCrawlAttemptState, bool) {
	switch outcome {
	case sourceCrawlTerminalCompletedOutcome:
		return sourceCrawlAttemptCompleted, true
	case sourceCrawlTerminalFailedOutcome:
		return sourceCrawlAttemptFailed, true
	case sourceCrawlTerminalCancelledOutcome:
		return sourceCrawlAttemptCancelled, true
	default:
		return "", false
	}
}

func sourceCrawlSourceStatusForOutcome(outcome sourceCrawlTerminalOutcome) (string, string) {
	switch outcome {
	case sourceCrawlTerminalCompletedOutcome:
		return "completed", ""
	case sourceCrawlTerminalFailedOutcome:
		return "error", "crawl_failed"
	case sourceCrawlTerminalCancelledOutcome:
		return "error", "crawl_cancelled"
	default:
		return "", ""
	}
}

func sourceCrawlCASAttemptTerminal(ctx context.Context, tx *sql.Tx, db Handler,
	attempt sourceCrawlAttempt, state sourceCrawlAttemptState, terminalAt time.Time,
	reasonCode string, now time.Time) error {
	p := func(n int) string { return sourceCrawlPlaceholder(db, n) }
	result, err := tx.ExecContext(ctx,
		`UPDATE SourceCrawlAttempts SET state = `+p(1)+`, terminal_at = `+p(2)+`,
		 reason_code = `+p(3)+`, last_updated_at = `+p(4)+`
		 WHERE attempt_id = `+p(5)+` AND source_id = `+p(6)+` AND generation = `+p(7)+`
		   AND state = 'claimed'`,
		string(state), terminalAt, reasonCode, now, attempt.AttemptID, attempt.SourceID,
		attempt.Generation)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("%w: attempt terminal CAS", errSourceCrawlResultConflict)
	}
	return nil
}

func sourceCrawlCASStatusTerminal(ctx context.Context, tx *sql.Tx, db Handler,
	operationID string, expected, result CrawlOperationState, now time.Time) error {
	p := func(n int) string { return sourceCrawlPlaceholder(db, n) }
	updated, err := tx.ExecContext(ctx,
		`UPDATE Status SET status = `+p(1)+`, last_updated_at = `+p(2)+`
		 WHERE status_sha256 = `+p(3)+` AND status = `+p(4)+` AND deleted_at IS NULL`,
		string(result), now, operationID, string(expected))
	if err != nil {
		return err
	}
	rows, err := updated.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("%w: status terminal CAS", errSourceCrawlResultConflict)
	}
	return nil
}

func sourceCrawlUpdateSourceTerminal(ctx context.Context, tx *sql.Tx, db Handler,
	sourceID, ownerID uint64, status, lastError string, now time.Time) (bool, error) {
	p := func(n int) string { return sourceCrawlPlaceholder(db, n) }
	var (
		query string
		args  []any
	)
	if lastError == "" {
		query = `UPDATE Sources SET status = ` + p(1) + `, last_crawled_at = ` + p(2) + `,
			last_error = NULL, last_error_at = NULL, last_updated_at = ` + p(3) + `
			WHERE source_id = ` + p(4) + ` AND deleted_at IS NULL AND disabled = FALSE
			  AND usr_id = ` + p(5) + ` AND status = 'processing'`
		args = []any{status, now, now, sourceID, ownerID}
	} else {
		query = `UPDATE Sources SET status = ` + p(1) + `, last_crawled_at = ` + p(2) + `,
			last_error = ` + p(3) + `, last_error_at = ` + p(4) + `, last_updated_at = ` + p(5) + `
			WHERE source_id = ` + p(6) + ` AND deleted_at IS NULL AND disabled = FALSE
			  AND usr_id = ` + p(7) + ` AND status = 'processing'`
		args = []any{status, now, lastError, now, now, sourceID, ownerID}
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

// sourceCrawlStoredTerminal reconstructs the durable terminal result for a
// stored history row. Bound results also require the authoritative terminal
// Status state; unbound results must not fabricate one.
func sourceCrawlStoredTerminal(ctx context.Context, tx *sql.Tx, db Handler,
	attempt sourceCrawlAttempt) (sourceCrawlPersistedTerminal, error) {
	if attempt.State == sourceCrawlAttemptClaimed {
		return sourceCrawlPersistedTerminal{},
			fmt.Errorf("%w: attempt is not terminal", errSourceCrawlResultConflict)
	}
	var operationResult CrawlOperationState
	if attempt.Binding != nil {
		status, found, err := sourceCrawlOperationStatusTx(ctx, tx, db, attempt.Binding.OperationID)
		if err != nil {
			return sourceCrawlPersistedTerminal{}, err
		}
		if !found || status.DeletedAt.Valid {
			return sourceCrawlPersistedTerminal{},
				fmt.Errorf("%w: terminal status is missing", errSourceCrawlResultConflict)
		}
		operationResult = CrawlOperationState(status.Status)
	}
	stored, err := newSourceCrawlPersistedTerminal(attempt, operationResult)
	if err != nil {
		return sourceCrawlPersistedTerminal{}, fmt.Errorf("%w: %v", errSourceCrawlResultConflict, err)
	}
	return stored, nil
}

func sourceCrawlReplayTerminal(ctx context.Context, tx *sql.Tx, db Handler,
	receipt sourceCrawlTerminalReceipt) sourceCrawlTerminalExecutionResult {
	attempt, found, err := sourceCrawlLoadAttemptTx(ctx, tx, db,
		receipt.Attempt.AttemptID, receipt.Attempt.SourceID, receipt.Attempt.Generation, false)
	if err != nil {
		return sourceCrawlTerminalExecutionResult{
			Result: sourceCrawlLoadResultClass(err),
			Reason: "attempt_read",
		}
	}
	if !found {
		return sourceCrawlTerminalExecutionResult{
			Result: sourceCrawlTerminalStaleFence,
			Reason: "attempt_missing",
		}
	}
	if attempt.State != sourceCrawlAttemptCompleted &&
		attempt.State != sourceCrawlAttemptFailed &&
		attempt.State != sourceCrawlAttemptCancelled {
		return sourceCrawlTerminalExecutionResult{
			Result:  sourceCrawlTerminalStaleFence,
			Reason:  "attempt_not_terminal",
			Attempt: attempt,
		}
	}
	stored, err := sourceCrawlStoredTerminal(ctx, tx, db, attempt)
	if err != nil {
		return sourceCrawlTerminalExecutionResult{
			Result:  sourceCrawlTerminalConflict,
			Reason:  "stored_terminal_corrupt",
			Attempt: attempt,
		}
	}
	class := classifySourceCrawlPersistedTerminalReplay(&stored, receipt)
	result := sourceCrawlTerminalExecutionResult{
		Result:         class,
		Reason:         "terminal_replay",
		Attempt:        attempt,
		OperationState: stored.OperationResult,
	}
	if class == sourceCrawlTerminalConflict {
		result.Reason = "terminal_replay_conflict"
	}
	return result
}

// reportSourceCrawlAttemptTerminal is the Stage B1 internal terminal writer. It
// is intentionally unexported: no crawler or service calls it in this stage.
func reportSourceCrawlAttemptTerminal(ctx context.Context, db *Handler,
	receipt sourceCrawlTerminalReceipt) (sourceCrawlTerminalExecutionResult, error) {
	return reportSourceCrawlAttemptTerminalWithClock(ctx, db, receipt, sourceCrawlClaimDatabaseClock)
}

// reportSourceCrawlAttemptTerminalAt is the deterministic test seam.
func reportSourceCrawlAttemptTerminalAt(ctx context.Context, db *Handler,
	receipt sourceCrawlTerminalReceipt, now time.Time) (sourceCrawlTerminalExecutionResult, error) {
	return reportSourceCrawlAttemptTerminalWithClock(ctx, db, receipt, sourceCrawlFixedClock(now))
}

func reportSourceCrawlAttemptTerminalWithClock(ctx context.Context, db *Handler,
	receipt sourceCrawlTerminalReceipt, clock sourceCrawlClaimClock) (sourceCrawlTerminalExecutionResult, error) {
	if err := receipt.validateStructure(); err != nil {
		return sourceCrawlTerminalExecutionResult{
			Result: sourceCrawlTerminalConflict,
			Reason: "invalid_receipt",
		}, nil
	}
	txCtx, tx, cancel, class, reason, err := sourceCrawlBeginResultTx(ctx, db)
	if class != "" {
		if class == sourceCrawlTerminalUnavailable {
			return sourceCrawlTerminalExecutionResult{Result: class, Reason: reason}, err
		}
		return sourceCrawlTerminalExecutionResult{Result: class, Reason: reason}, nil
	}
	defer cancel()
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	source, found, err := sourceCrawlLockResultSourceTx(txCtx, tx, *db, receipt.Attempt.SourceID)
	if err != nil {
		return sourceCrawlTerminalExecutionResult{
			Result: sourceCrawlTerminalUnavailable, Reason: "source_read_unavailable",
		}, err
	}
	if !found || source.Deleted {
		return sourceCrawlReplayTerminal(txCtx, tx, *db, receipt), nil
	}
	// A committed terminal result is read-only replayable even after a newer
	// generation has replaced the current claim. Read the history before
	// comparing the current claim; the source lock above serializes this path
	// with claim allocation and heartbeat/terminal mutations.
	history, found, err := sourceCrawlLoadAttemptTx(txCtx, tx, *db,
		receipt.Attempt.AttemptID, receipt.Attempt.SourceID, receipt.Attempt.Generation, false)
	if err != nil {
		class := sourceCrawlLoadResultClass(err)
		if class == sourceCrawlTerminalConflict {
			return sourceCrawlTerminalExecutionResult{Result: class, Reason: "attempt_read"}, nil
		}
		return sourceCrawlTerminalExecutionResult{Result: class, Reason: "attempt_read"}, err
	}
	if !found {
		return sourceCrawlTerminalExecutionResult{
			Result: sourceCrawlTerminalStaleFence, Reason: "attempt_missing",
		}, nil
	}
	if history.State != sourceCrawlAttemptClaimed {
		return sourceCrawlReplayTerminal(txCtx, tx, *db, receipt), nil
	}
	current, found, err := sourceCrawlLockCurrentClaimTx(txCtx, tx, *db, receipt.Attempt.SourceID)
	if err != nil {
		return sourceCrawlTerminalExecutionResult{
			Result: sourceCrawlTerminalUnavailable, Reason: "claim_read_unavailable",
		}, err
	}
	if !found {
		return sourceCrawlTerminalExecutionResult{
			Result: sourceCrawlTerminalStaleFence, Reason: "claim_missing", Attempt: history,
		}, nil
	}
	if current.AttemptID != history.AttemptID || current.Generation != history.Generation {
		return sourceCrawlTerminalExecutionResult{
			Result: sourceCrawlTerminalStaleFence, Reason: "attempt_not_current", Attempt: history,
		}, nil
	}
	if !current.matchesAttempt(history) {
		return sourceCrawlTerminalExecutionResult{
			Result: sourceCrawlTerminalConflict, Reason: "claim_history_mismatch", Attempt: history,
		}, nil
	}
	// Re-read under the documented source -> current claim -> history order
	// before any mutation. The source lock makes this a consistency check.
	history, found, err = sourceCrawlLoadAttemptTx(txCtx, tx, *db,
		receipt.Attempt.AttemptID, receipt.Attempt.SourceID, receipt.Attempt.Generation, true)
	if err != nil {
		class := sourceCrawlLoadResultClass(err)
		if class == sourceCrawlTerminalConflict {
			return sourceCrawlTerminalExecutionResult{Result: class, Reason: "attempt_read"}, nil
		}
		return sourceCrawlTerminalExecutionResult{Result: class, Reason: "attempt_read"}, err
	}
	if !found {
		return sourceCrawlTerminalExecutionResult{
			Result: sourceCrawlTerminalStaleFence, Reason: "attempt_missing",
		}, nil
	}
	if history.State != sourceCrawlAttemptClaimed {
		return sourceCrawlReplayTerminal(txCtx, tx, *db, receipt), nil
	}
	if history.AttemptID != receipt.Attempt.AttemptID ||
		history.Generation != receipt.Attempt.Generation {
		return sourceCrawlTerminalExecutionResult{
			Result: sourceCrawlTerminalStaleFence, Reason: "attempt_not_current", Attempt: history,
		}, nil
	}
	if history.ClaimRequestID != receipt.Attempt.ClaimRequestID ||
		history.EngineID != receipt.Attempt.EngineID ||
		!history.ClaimedAt.Equal(receipt.Attempt.ClaimedAt) ||
		!history.LeaseExpiresAt.Equal(receipt.Attempt.LeaseExpiresAt) {
		return sourceCrawlTerminalExecutionResult{
			Result: sourceCrawlTerminalConflict, Reason: "receipt_identity_mismatch", Attempt: history,
		}, nil
	}
	if history.State != sourceCrawlAttemptClaimed {
		return sourceCrawlReplayTerminal(txCtx, tx, *db, receipt), nil
	}
	now, err := sourceCrawlReadClaimClock(txCtx, tx, *db, clock)
	if err != nil {
		return sourceCrawlTerminalExecutionResult{
			Result: sourceCrawlTerminalUnavailable, Reason: "database_clock_unavailable",
		}, NormalizeTransactionError(txCtx, err)
	}
	if !now.Before(history.LeaseExpiresAt) || !now.Before(current.LeaseExpiresAt) {
		return sourceCrawlTerminalExecutionResult{
			Result: sourceCrawlTerminalStaleFence, Reason: "lease_expired", Attempt: history,
		}, nil
	}
	if receipt.ObservedAt.After(now) {
		return sourceCrawlTerminalExecutionResult{
			Result: sourceCrawlTerminalConflict, Reason: "observation_in_future", Attempt: history,
		}, nil
	}
	if source.Disabled && history.Binding == nil {
		return sourceCrawlTerminalExecutionResult{
			Result: sourceCrawlTerminalStaleFence, Reason: "source_disabled", Attempt: history,
		}, nil
	}

	var (
		operationState  CrawlOperationState
		approvalHistory = sourceCrawlApprovalValidAtAcceptance
	)
	if history.Binding != nil {
		if receipt.Attribution != sourceCrawlHandoffBound || receipt.OperationResult == nil {
			return sourceCrawlTerminalExecutionResult{
				Result: sourceCrawlTerminalConflict, Reason: "bound_receipt_mismatch", Attempt: history,
			}, nil
		}
		state, approval, err := sourceCrawlValidateBoundOperationTx(txCtx, tx, *db,
			history.SourceID, *history.Binding, now)
		if err != nil {
			return sourceCrawlTerminalExecutionResult{
				Result: sourceCrawlResultClass(err), Reason: "operation_authority", Attempt: history,
			}, nil
		}
		operationState = state
		approvalHistory = approval
	} else if receipt.Attribution == sourceCrawlHandoffBound || receipt.OperationResult != nil {
		return sourceCrawlTerminalExecutionResult{
			Result: sourceCrawlTerminalConflict, Reason: "unbound_receipt_mismatch", Attempt: history,
		}, nil
	}

	ownershipVerified := false
	if history.Binding != nil {
		ownershipVerified, err = sourceCrawlVerifiedOwnerTx(txCtx, tx, *db,
			history.SourceID, history.Binding.PrincipalUserID)
		if err != nil {
			return sourceCrawlTerminalExecutionResult{
				Result: sourceCrawlTerminalUnavailable, Reason: "ownership_read_unavailable", Attempt: history,
			}, err
		}
		ownershipVerified = ownershipVerified &&
			source.UserID == history.Binding.PrincipalUserID
	}
	// An unbound attempt has no persisted operation principal. Schema 1.17
	// cannot prove owner continuity for it (see the source-status eligibility
	// contract), so no source-status read is attempted and the mutation is
	// suppressed.
	authority := sourceCrawlTerminalAuthority{
		Available: true, Fenced: true, Found: true,
		SourceID: history.SourceID, AttemptID: history.AttemptID,
		Generation: history.Generation, ClaimRequestID: history.ClaimRequestID,
		EngineID: history.EngineID, ClaimedAt: history.ClaimedAt,
		HistorySourceID: history.SourceID, HistoryAttemptID: history.AttemptID,
		HistoryGeneration: history.Generation, HistoryClaimRequestID: history.ClaimRequestID,
		HistoryEngineID: history.EngineID, HistoryClaimedAt: history.ClaimedAt,
		CurrentLeaseExpiresAt:    current.LeaseExpiresAt,
		HistoryLeaseExpiresAt:    history.LeaseExpiresAt,
		AttemptState:             history.State,
		Binding:                  history.Binding,
		SourceDeleted:            source.Deleted,
		SourceDisabled:           source.Disabled,
		SourceOwnershipVerified:  ownershipVerified,
		CurrentSourceOwnerUserID: source.UserID,
		ApprovalHistory:          approvalHistory,
	}
	if err := authority.validate(receipt, now); err != nil {
		return sourceCrawlTerminalExecutionResult{
			Result: sourceCrawlResultClass(err), Reason: "authority_validation", Attempt: history,
		}, nil
	}
	targetState, ok := sourceCrawlTerminalAttemptState(receipt.Outcome)
	if !ok {
		return sourceCrawlTerminalExecutionResult{
			Result: sourceCrawlTerminalConflict, Reason: "terminal_outcome", Attempt: history,
		}, nil
	}
	if err := sourceCrawlCASAttemptTerminal(txCtx, tx, *db, history, targetState,
		receipt.ObservedAt, receipt.ReasonCode, now); err != nil {
		return sourceCrawlTerminalExecutionResult{
			Result: sourceCrawlResultClass(err), Reason: "attempt_cas", Attempt: history,
		}, nil
	}
	if history.Binding != nil {
		target, _ := sourceCrawlTerminalOperationTarget(receipt.Outcome)
		if receipt.OperationResult == nil || receipt.OperationResult.ResultState != target {
			return sourceCrawlTerminalExecutionResult{
				Result: sourceCrawlTerminalConflict, Reason: "operation_result", Attempt: history,
			}, nil
		}
		if err := sourceCrawlCASStatusTerminal(txCtx, tx, *db, history.Binding.OperationID,
			operationState, receipt.OperationResult.ResultState, now); err != nil {
			return sourceCrawlTerminalExecutionResult{
				Result: sourceCrawlResultClass(err), Reason: "status_cas", Attempt: history,
			}, nil
		}
	}
	sourceChanged := false
	if authority.sourceCrawlTerminalSourceStatusEligible(receipt, now) {
		status, lastError := sourceCrawlSourceStatusForOutcome(receipt.Outcome)
		changed, err := sourceCrawlUpdateSourceTerminal(txCtx, tx, *db, history.SourceID,
			source.UserID, status, lastError, now)
		if err != nil {
			return sourceCrawlTerminalExecutionResult{
				Result: sourceCrawlTerminalUnavailable, Reason: "source_update_unavailable", Attempt: history,
			}, err
		}
		sourceChanged = changed
	}
	if err := tx.Commit(); err != nil {
		committedResult, reconcileErr := sourceCrawlReconcileTerminalCommit(ctx, db, receipt)
		if reconcileErr != nil {
			return committedResult, reconcileErr
		}
		return committedResult, nil
	}
	committed = true
	terminalAttempt := history
	terminalAttempt.State = targetState
	terminalAttempt.TerminalAt = receipt.ObservedAt
	terminalAttempt.ReasonCode = receipt.ReasonCode
	return sourceCrawlTerminalExecutionResult{
		Result: sourceCrawlTerminalApplied, Reason: "terminal_applied",
		Attempt: terminalAttempt, OperationState: receipt.OperationResultState(),
		SourceStatusChanged: sourceChanged,
	}, nil
}

func (r sourceCrawlTerminalReceipt) OperationResultState() CrawlOperationState {
	if r.OperationResult == nil {
		return ""
	}
	return r.OperationResult.ResultState
}

func sourceCrawlReconcileTerminalCommit(ctx context.Context, db *Handler,
	receipt sourceCrawlTerminalReceipt) (sourceCrawlTerminalExecutionResult, error) {
	reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	tx, err := (*db).BeginTx(reconcileCtx, nil)
	if err != nil {
		return sourceCrawlTerminalExecutionResult{
			Result: sourceCrawlTerminalUnavailable, Reason: "commit_uncertain",
		}, err
	}
	defer tx.Rollback() //nolint:errcheck
	result := sourceCrawlReplayTerminal(reconcileCtx, tx, *db, receipt)
	switch result.Result {
	case sourceCrawlTerminalIdempotentReplay:
		result.Reason = "commit_reconciled"
	case sourceCrawlTerminalUnavailable:
		result.Reason = "commit_uncertain"
	default:
		result.Result = sourceCrawlTerminalConflict
		result.Reason = "commit_conflict"
	}
	return result, nil
}

// renewSourceCrawlAttemptLease is the Stage B1 internal heartbeat. It is
// intentionally unexported: no crawler or service calls it in this stage.
func renewSourceCrawlAttemptLease(ctx context.Context, db *Handler,
	req sourceCrawlLeaseRenewalRequest) (sourceCrawlLeaseRenewalResult, error) {
	return renewSourceCrawlAttemptLeaseWithClock(ctx, db, req, sourceCrawlClaimDatabaseClock)
}

// renewSourceCrawlAttemptLeaseAt is the deterministic test seam.
func renewSourceCrawlAttemptLeaseAt(ctx context.Context, db *Handler,
	req sourceCrawlLeaseRenewalRequest, now time.Time) (sourceCrawlLeaseRenewalResult, error) {
	return renewSourceCrawlAttemptLeaseWithClock(ctx, db, req, sourceCrawlFixedClock(now))
}

func renewSourceCrawlAttemptLeaseWithClock(ctx context.Context, db *Handler,
	req sourceCrawlLeaseRenewalRequest, clock sourceCrawlClaimClock) (sourceCrawlLeaseRenewalResult, error) {
	invalid := func(reason string) (sourceCrawlLeaseRenewalResult, error) {
		return sourceCrawlLeaseRenewalResult{Result: sourceCrawlTerminalConflict, Reason: reason}, nil
	}
	if req.SourceID == 0 || !sourceCrawlAttemptIDPattern.MatchString(req.AttemptID) ||
		req.Generation == 0 || !sourceCrawlHashPattern.MatchString(req.ClaimRequestID) ||
		strings.TrimSpace(req.EngineID) == "" || len(req.EngineID) > 256 {
		return invalid("heartbeat_identity")
	}
	extension := req.Extension.Truncate(time.Microsecond)
	if extension <= 0 || extension > sourceCrawlMaxLeaseExtension {
		return invalid("heartbeat_extension")
	}
	txCtx, tx, cancel, class, reason, err := sourceCrawlBeginResultTx(ctx, db)
	if class != "" {
		if class == sourceCrawlTerminalUnavailable {
			return sourceCrawlLeaseRenewalResult{Result: class, Reason: reason}, err
		}
		return sourceCrawlLeaseRenewalResult{Result: class, Reason: reason}, nil
	}
	defer cancel()
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	source, found, err := sourceCrawlLockResultSourceTx(txCtx, tx, *db, req.SourceID)
	if err != nil {
		return sourceCrawlLeaseRenewalResult{
			Result: sourceCrawlTerminalUnavailable, Reason: "source_read_unavailable",
		}, err
	}
	if !found {
		return sourceCrawlLeaseRenewalResult{
			Result: sourceCrawlTerminalStaleFence, Reason: "source_missing",
		}, nil
	}
	if source.Deleted || source.Disabled {
		return sourceCrawlLeaseRenewalResult{
			Result: sourceCrawlTerminalStaleFence, Reason: "source_unavailable",
		}, nil
	}
	current, found, err := sourceCrawlLockCurrentClaimTx(txCtx, tx, *db, req.SourceID)
	if err != nil {
		return sourceCrawlLeaseRenewalResult{
			Result: sourceCrawlTerminalUnavailable, Reason: "claim_read_unavailable",
		}, err
	}
	if !found {
		return sourceCrawlLeaseRenewalResult{
			Result: sourceCrawlTerminalStaleFence, Reason: "claim_missing",
		}, nil
	}
	history, found, err := sourceCrawlLoadAttemptTx(txCtx, tx, *db,
		req.AttemptID, req.SourceID, req.Generation, true)
	if err != nil {
		class := sourceCrawlLoadResultClass(err)
		if class == sourceCrawlTerminalConflict {
			return sourceCrawlLeaseRenewalResult{Result: class, Reason: "attempt_read"}, nil
		}
		return sourceCrawlLeaseRenewalResult{Result: class, Reason: "attempt_read"}, err
	}
	if !found {
		return sourceCrawlLeaseRenewalResult{
			Result: sourceCrawlTerminalStaleFence, Reason: "attempt_missing",
		}, nil
	}
	if history.AttemptID != req.AttemptID || history.Generation != req.Generation {
		return sourceCrawlLeaseRenewalResult{
			Result: sourceCrawlTerminalStaleFence, Reason: "attempt_not_current",
		}, nil
	}
	if current.AttemptID != history.AttemptID || current.Generation != history.Generation {
		return sourceCrawlLeaseRenewalResult{
			Result: sourceCrawlTerminalStaleFence, Reason: "attempt_not_current",
		}, nil
	}
	if !current.matchesAttempt(history) {
		return sourceCrawlLeaseRenewalResult{
			Result: sourceCrawlTerminalConflict, Reason: "claim_history_mismatch",
		}, nil
	}
	if history.ClaimRequestID != req.ClaimRequestID || history.EngineID != req.EngineID {
		return sourceCrawlLeaseRenewalResult{
			Result: sourceCrawlTerminalConflict, Reason: "heartbeat_identity",
		}, nil
	}
	if history.State != sourceCrawlAttemptClaimed {
		return sourceCrawlLeaseRenewalResult{
			Result: sourceCrawlTerminalStaleFence, Reason: "attempt_terminal",
		}, nil
	}
	now, err := sourceCrawlReadClaimClock(txCtx, tx, *db, clock)
	if err != nil {
		return sourceCrawlLeaseRenewalResult{
			Result: sourceCrawlTerminalUnavailable, Reason: "database_clock_unavailable",
		}, NormalizeTransactionError(txCtx, err)
	}
	if !now.Before(history.LeaseExpiresAt) || !now.Before(current.LeaseExpiresAt) {
		return sourceCrawlLeaseRenewalResult{
			Result: sourceCrawlTerminalStaleFence, Reason: "lease_expired",
		}, nil
	}
	newLease := history.LeaseExpiresAt.Add(extension)
	if !newLease.After(history.LeaseExpiresAt) {
		return sourceCrawlLeaseRenewalResult{
			Result: sourceCrawlTerminalConflict, Reason: "lease_extension",
		}, nil
	}
	if newLease.After(history.ClaimedAt.Add(sourceCrawlMaxAttemptLease)) {
		return sourceCrawlLeaseRenewalResult{
			Result: sourceCrawlTerminalConflict, Reason: "lease_lifetime_exceeded",
		}, nil
	}
	p := func(n int) string { return sourceCrawlPlaceholder(*db, n) }
	currentUpdate, err := tx.ExecContext(txCtx,
		`UPDATE SourceCrawlClaims SET lease_expires_at = `+p(1)+`, last_updated_at = `+p(2)+`
		 WHERE source_id = `+p(3)+` AND attempt_id = `+p(4)+` AND generation = `+p(5)+`
		   AND claim_request_id = `+p(6)+` AND engine_id = `+p(7)+`
		   AND lease_expires_at = `+p(8),
		newLease, now, req.SourceID, req.AttemptID, req.Generation, req.ClaimRequestID,
		req.EngineID, history.LeaseExpiresAt)
	if err != nil {
		return sourceCrawlLeaseRenewalResult{
			Result: sourceCrawlTerminalUnavailable, Reason: "heartbeat_update_unavailable",
		}, err
	}
	rows, err := currentUpdate.RowsAffected()
	if err != nil {
		return sourceCrawlLeaseRenewalResult{
			Result: sourceCrawlTerminalUnavailable, Reason: "heartbeat_update_unavailable",
		}, err
	}
	if rows != 1 {
		return sourceCrawlLeaseRenewalResult{
			Result: sourceCrawlTerminalConflict, Reason: "heartbeat_claim_conflict",
		}, nil
	}
	historyUpdate, err := tx.ExecContext(txCtx,
		`UPDATE SourceCrawlAttempts SET lease_expires_at = `+p(1)+`, last_updated_at = `+p(2)+`
		 WHERE attempt_id = `+p(3)+` AND source_id = `+p(4)+` AND generation = `+p(5)+`
		   AND state = 'claimed' AND lease_expires_at = `+p(6),
		newLease, now, req.AttemptID, req.SourceID, req.Generation, history.LeaseExpiresAt)
	if err != nil {
		return sourceCrawlLeaseRenewalResult{
			Result: sourceCrawlTerminalUnavailable, Reason: "heartbeat_update_unavailable",
		}, err
	}
	rows, err = historyUpdate.RowsAffected()
	if err != nil {
		return sourceCrawlLeaseRenewalResult{
			Result: sourceCrawlTerminalUnavailable, Reason: "heartbeat_update_unavailable",
		}, err
	}
	if rows != 1 {
		return sourceCrawlLeaseRenewalResult{
			Result: sourceCrawlTerminalConflict, Reason: "heartbeat_history_conflict",
		}, nil
	}
	if err := tx.Commit(); err != nil {
		reconciled, reconcileErr := sourceCrawlReconcileLeaseCommit(ctx, *db, req, newLease)
		if reconcileErr != nil {
			return reconciled, reconcileErr
		}
		return reconciled, nil
	}
	committed = true
	return sourceCrawlLeaseRenewalResult{
		Result: sourceCrawlTerminalApplied,
		Reason: "lease_renewed",
		Claim:  sourceCrawlClaimFromAttempt(history, newLease),
	}, nil
}

func sourceCrawlClaimFromAttempt(attempt sourceCrawlAttempt, lease time.Time) SourceCrawlClaim {
	outcome := SourceCrawlClaimed
	if attempt.Binding == nil {
		outcome = SourceCrawlUnattributed
	}
	claim := SourceCrawlClaim{
		Outcome: outcome, SourceID: attempt.SourceID,
		ClaimRequestID: attempt.ClaimRequestID, AttemptID: attempt.AttemptID,
		Generation: attempt.Generation, EngineID: attempt.EngineID,
		ClaimedAt: attempt.ClaimedAt, LeaseExpiresAt: lease,
	}
	if attempt.Binding != nil {
		claim.OperationID = attempt.Binding.OperationID
		claim.RequestID = attempt.Binding.RequestID
		claim.RunID = attempt.Binding.RunID
		claim.AgentID = attempt.Binding.AgentID
		claim.Owner = attempt.Binding.Owner
		claim.PrincipalUserID = attempt.Binding.PrincipalUserID
		claim.ApprovalID = attempt.Binding.ApprovalID
		claim.ApprovalSHA256 = attempt.Binding.ApprovalSHA256
	}
	return claim
}

func sourceCrawlReconcileLeaseCommit(ctx context.Context, db Handler,
	req sourceCrawlLeaseRenewalRequest, target time.Time) (sourceCrawlLeaseRenewalResult, error) {
	reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	tx, err := db.BeginTx(reconcileCtx, nil)
	if err != nil {
		return sourceCrawlLeaseRenewalResult{
			Result: sourceCrawlTerminalUnavailable, Reason: "commit_uncertain",
		}, err
	}
	defer tx.Rollback() //nolint:errcheck
	current, found, err := sourceCrawlLockCurrentClaimTx(reconcileCtx, tx, db, req.SourceID)
	if err != nil || !found {
		return sourceCrawlLeaseRenewalResult{
			Result: sourceCrawlTerminalUnavailable, Reason: "commit_uncertain",
		}, err
	}
	history, found, err := sourceCrawlLoadAttemptTx(reconcileCtx, tx, db,
		req.AttemptID, req.SourceID, req.Generation, false)
	if err != nil || !found {
		return sourceCrawlLeaseRenewalResult{
			Result: sourceCrawlTerminalUnavailable, Reason: "commit_uncertain",
		}, err
	}
	if current.LeaseExpiresAt.Equal(target) && history.LeaseExpiresAt.Equal(target) {
		return sourceCrawlLeaseRenewalResult{
			Result: sourceCrawlTerminalApplied, Reason: "lease_renewed_reconciled",
			Claim: sourceCrawlClaimFromAttempt(history, target),
		}, nil
	}
	return sourceCrawlLeaseRenewalResult{
		Result: sourceCrawlTerminalConflict, Reason: "heartbeat_conflict",
	}, nil
}
