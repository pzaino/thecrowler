// Copyright 2023 Paolo Fabio Zaino, all rights reserved.
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

package agent

// Internal transactional storage for asynchronous goal checkpoints.
//
// The store is deliberately unexported and is not constructed by any
// production service in this work package. It persists the schema-1.17 goal
// tables, validates every loaded checkpoint through the pure kernel, and
// applies transitions through the existing pure transition function plus an
// optimistic compare-and-swap. A checkpoint is historical state, not a live
// authorization grant: loading it does not start a worker, create a resume
// grant, schedule work, or execute a model, crawler, tool or MCP call.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

var (
	// errGoalAsyncStoreUnavailable means the handler cannot be used for goal
	// persistence (nil handler or missing schema).
	errGoalAsyncStoreUnavailable = errors.New("goal async checkpoint store is unavailable")
	// errGoalAsyncStoreCorrupt means the persisted root and child records do
	// not form one consistent, validated checkpoint.
	errGoalAsyncStoreCorrupt = errors.New("goal async checkpoint store contains corrupt state")
	// errGoalAsyncStoreNotFound is returned when a run ID does not exist.
	errGoalAsyncStoreNotFound = errors.New("goal async checkpoint not found")
	// errGoalAsyncStoreConflict means creation found an existing incompatible
	// checkpoint or a write could not be applied safely.
	errGoalAsyncStoreConflict = errors.New("goal async checkpoint store conflict")
)

const (
	goalAsyncStoreReconcileTimeout = 10 * time.Second
	goalAsyncStoreLockAttempts     = 1
)

// goalAsyncStore persists goal checkpoints through the existing database
// abstraction. The type is internal; callers must explicitly construct it.
type goalAsyncStore struct {
	db       *cdb.Handler
	postgres bool
}

// goalAsyncStoreQueryer is implemented by *sql.Tx and database.Handler.
type goalAsyncStoreQueryer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// newGoalAsyncStore validates the backend and the additive schema before any
// write is permitted. MySQL and every other unsupported backend fail with the
// existing typed database error and never fall back to in-memory storage.
func newGoalAsyncStore(ctx context.Context, db *cdb.Handler) (*goalAsyncStore, error) {
	if db == nil || *db == nil {
		return nil, fmt.Errorf("%w: nil database handler", errGoalAsyncStoreUnavailable)
	}
	if !cdb.AsyncGoalPersistenceSupported((*db).DBMS()) {
		return nil, fmt.Errorf("%w: %s", cdb.ErrAsyncGoalPersistenceUnsupported, (*db).DBMS())
	}
	if err := cdb.CheckAsyncGoalPersistenceSchema(ctx, db); err != nil {
		return nil, err
	}
	backend := strings.ToLower(strings.TrimSpace((*db).DBMS()))
	return &goalAsyncStore{
		db:       db,
		postgres: backend == "postgres" || backend == "postgresql",
	}, nil
}

// CreateCheckpoint stores a newly created revision-1 checkpoint and its frozen
// tool/source scope in one transaction. A matching existing checkpoint is an
// idempotent success; an existing run ID with different contents is a typed
// conflict.
func (s *goalAsyncStore) CreateCheckpoint(ctx context.Context, cp goalAsyncCheckpoint) error {
	if s == nil || s.db == nil {
		return errGoalAsyncStoreUnavailable
	}
	if err := validateGoalAsyncCheckpoint(cp); err != nil {
		return err
	}
	if cp.Revision != 1 || cp.State != goalAsyncCreated {
		return fmt.Errorf("%w: create requires revision 1 in the created state", ErrGoalAsyncCheckpointInvalid)
	}
	// Validate before normalizing. Normalization is only used to store the
	// trusted canonical representation; it must never repair malformed input.
	cp = normalizeGoalAsyncCheckpoint(cp)

	txCtx, cancel := cdb.TransactionContextForHandler(ctx, s.db)
	defer cancel()
	tx, err := (*s.db).BeginTx(txCtx, nil)
	if err != nil {
		return fmt.Errorf("begin goal checkpoint create: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if !s.postgres {
		// SQLite starts a deferred transaction. A no-op write on the root
		// acquires the write lock before any read, serializing writer
		// transactions without nesting BEGIN IMMEDIATE inside sql.Tx.
		if _, err := tx.ExecContext(txCtx,
			`UPDATE AgentGoalRuns SET revision = revision WHERE goal_run_id = `+s.placeholder(1),
			cp.RunID); err != nil {
			return s.mapWriteError("acquire SQLite goal store writer lock", err)
		}
	}

	existing, found, err := s.loadCheckpointIfExists(txCtx, tx, cp.RunID)
	if err != nil {
		return err
	}
	if found {
		want, err := goalAsyncCheckpointDigest(cp)
		if err != nil {
			return err
		}
		got, err := goalAsyncCheckpointDigest(existing)
		if err != nil {
			return err
		}
		if want != got {
			return fmt.Errorf("%w: run %q already exists with different checkpoint contents", errGoalAsyncStoreConflict, cp.RunID)
		}
		if err := tx.Commit(); err != nil {
			return s.reconcileCreateAfterError(ctx, cp, cdb.NormalizeTransactionError(txCtx, err))
		}
		committed = true
		return nil
	}

	if err := s.insertRoot(txCtx, tx, cp); err != nil {
		if isUniqueConstraintError(err) {
			_ = tx.Rollback()
			return s.reconcileCreateAfterError(ctx, cp, fmt.Errorf("%w: concurrent create for run %q", errGoalAsyncStoreConflict, cp.RunID))
		}
		return err
	}
	if err := s.insertScope(txCtx, tx, cp); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return s.reconcileCreateAfterError(ctx, cp, cdb.NormalizeTransactionError(txCtx, err))
	}
	committed = true
	return nil
}

// LoadCheckpoint reads one checkpoint and all of its child records from a
// single consistent snapshot, then validates the result through the existing
// pure validator and transition ledger checks. It performs no writes and no
// authorization.
func (s *goalAsyncStore) LoadCheckpoint(ctx context.Context, runID string) (goalAsyncCheckpoint, error) {
	cp, _, err := s.LoadCheckpointWithCancellation(ctx, runID)
	return cp, err
}

// LoadCheckpointWithCancellation reads the current checkpoint and the durable
// cancellation marker from one read-only snapshot. The bool reports whether a
// cancellation request is outstanding. It performs no writes and no
// authorization; callers must treat the checkpoint as historical state.
func (s *goalAsyncStore) LoadCheckpointWithCancellation(ctx context.Context, runID string) (goalAsyncCheckpoint, bool, error) {
	if s == nil || s.db == nil {
		return goalAsyncCheckpoint{}, false, errGoalAsyncStoreUnavailable
	}
	if !goalAsyncIDPattern.MatchString(runID) {
		return goalAsyncCheckpoint{}, false, fmt.Errorf("%w: run_id", ErrGoalAsyncCheckpointInvalid)
	}
	txCtx, cancel := cdb.TransactionContextForHandler(ctx, s.db)
	defer cancel()
	opts := &sql.TxOptions{ReadOnly: true}
	if s.postgres {
		opts.Isolation = sql.LevelRepeatableRead
	}
	tx, err := (*s.db).BeginTx(txCtx, opts)
	if err != nil {
		return goalAsyncCheckpoint{}, false, fmt.Errorf("begin goal checkpoint load: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	cp, err := s.loadCheckpoint(txCtx, tx, runID, false)
	if err != nil {
		return goalAsyncCheckpoint{}, false, cdb.NormalizeTransactionError(txCtx, err)
	}
	pending, err := s.cancellationPending(txCtx, tx, runID)
	if err != nil {
		return goalAsyncCheckpoint{}, false, cdb.NormalizeTransactionError(txCtx, err)
	}
	if err := tx.Commit(); err != nil {
		return goalAsyncCheckpoint{}, false, cdb.NormalizeTransactionError(txCtx, err)
	}
	return cp, pending, nil
}

// TransitionCheckpoint applies an authorized transition atomically. The
// caller supplies the run ID and the pure transition request. The store
// consults the complete operation ledger before applying a new revision, so an
// exact replay of any older operation is recognised without writing.
func (s *goalAsyncStore) TransitionCheckpoint(ctx context.Context, runID string, request goalAsyncTransitionRequest) (goalAsyncCheckpoint, goalAsyncTransitionOutcome, error) {
	if s == nil || s.db == nil {
		return goalAsyncCheckpoint{}, "", errGoalAsyncStoreUnavailable
	}
	if !goalAsyncIDPattern.MatchString(runID) {
		return goalAsyncCheckpoint{}, "", fmt.Errorf("%w: run_id", ErrGoalAsyncCheckpointInvalid)
	}
	if !goalAsyncIDPattern.MatchString(request.OperationID) {
		return goalAsyncCheckpoint{}, "", fmt.Errorf("%w: operation_id", ErrGoalAsyncCheckpointInvalid)
	}

	txCtx, cancel := cdb.TransactionContextForHandler(ctx, s.db)
	defer cancel()
	tx, err := (*s.db).BeginTx(txCtx, nil)
	if err != nil {
		return goalAsyncCheckpoint{}, "", fmt.Errorf("begin goal checkpoint transition: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if !s.postgres {
		// A SQLite deferred transaction is upgraded to a writer before any
		// read. This serializes competing writers without issuing a nested
		// BEGIN IMMEDIATE inside the active sql.Tx.
		if _, err := tx.ExecContext(txCtx,
			`UPDATE AgentGoalRuns SET revision = revision WHERE goal_run_id = `+s.placeholder(1),
			runID); err != nil {
			return goalAsyncCheckpoint{}, "", s.mapWriteError("acquire SQLite goal store writer lock", err)
		}
	}

	previous, err := s.loadCheckpoint(txCtx, tx, runID, true)
	if err != nil {
		if errors.Is(err, errGoalAsyncStoreNotFound) {
			return goalAsyncCheckpoint{}, "", err
		}
		return goalAsyncCheckpoint{}, "", cdb.NormalizeTransactionError(txCtx, err)
	}

	record, found, err := s.findTransition(txCtx, tx, runID, request.OperationID)
	if err != nil {
		return goalAsyncCheckpoint{}, "", cdb.NormalizeTransactionError(txCtx, err)
	}
	if found {
		expected := goalAsyncTransitionRequestHash(request)
		if record.TransitionHash != expected {
			return goalAsyncCheckpoint{}, "", ErrGoalAsyncReplayConflict
		}
		if err := tx.Commit(); err != nil {
			return s.reconcileTransitionAfterError(ctx, runID, request, cdb.NormalizeTransactionError(txCtx, err))
		}
		committed = true
		return previous, goalAsyncTransitionReplayed, nil
	}

	pending, err := s.cancellationPending(txCtx, tx, runID)
	if err != nil {
		return goalAsyncCheckpoint{}, "", cdb.NormalizeTransactionError(txCtx, err)
	}
	if pending {
		return goalAsyncCheckpoint{}, "", ErrGoalAsyncCancellationPending
	}

	next, outcome, err := applyGoalAsyncTransition(previous, request)
	if err != nil {
		return goalAsyncCheckpoint{}, "", err
	}
	if outcome == goalAsyncTransitionReplayed {
		// The ledger is complete, so this should only be reachable if a
		// caller uses an operation ID that is the current last transition
		// but absent from the ledger. Fail closed rather than silently
		// accepting an incomplete ledger.
		return goalAsyncCheckpoint{}, "", fmt.Errorf("%w: missing transition ledger row for %q", errGoalAsyncStoreCorrupt, request.OperationID)
	}

	if err := s.updateRoot(txCtx, tx, previous, next); err != nil {
		return goalAsyncCheckpoint{}, "", err
	}
	if err := s.syncChildren(txCtx, tx, previous, next); err != nil {
		return goalAsyncCheckpoint{}, "", err
	}
	if err := s.insertTransition(txCtx, tx, previous, next); err != nil {
		return goalAsyncCheckpoint{}, "", s.mapWriteError("insert goal transition ledger", err)
	}
	if err := tx.Commit(); err != nil {
		return s.reconcileTransitionAfterError(ctx, runID, request, cdb.NormalizeTransactionError(txCtx, err))
	}
	committed = true
	return next, goalAsyncTransitionApplied, nil
}

func (s *goalAsyncStore) reconcileCreateAfterError(ctx context.Context, cp goalAsyncCheckpoint, cause error) error {
	parent := ctx
	if parent == nil {
		parent = context.Background()
	}
	reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), goalAsyncStoreReconcileTimeout)
	defer cancel()
	existing, err := s.LoadCheckpoint(reconcileCtx, cp.RunID)
	if err != nil {
		return cause
	}
	want, err := goalAsyncCheckpointDigest(cp)
	if err != nil {
		return cause
	}
	got, err := goalAsyncCheckpointDigest(existing)
	if err != nil {
		return cause
	}
	if want == got {
		return nil
	}
	return fmt.Errorf("%w: checkpoint commit outcome could not be reconciled", errGoalAsyncStoreConflict)
}

func (s *goalAsyncStore) reconcileTransitionAfterError(ctx context.Context, runID string, request goalAsyncTransitionRequest, cause error) (goalAsyncCheckpoint, goalAsyncTransitionOutcome, error) {
	parent := ctx
	if parent == nil {
		parent = context.Background()
	}
	reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), goalAsyncStoreReconcileTimeout)
	defer cancel()
	cp, err := s.LoadCheckpoint(reconcileCtx, runID)
	if err != nil {
		return goalAsyncCheckpoint{}, "", cause
	}
	record, found, err := s.findTransition(reconcileCtx, *s.db, runID, request.OperationID)
	if err != nil || !found {
		return goalAsyncCheckpoint{}, "", cause
	}
	if record.TransitionHash != goalAsyncTransitionRequestHash(request) {
		return goalAsyncCheckpoint{}, "", ErrGoalAsyncReplayConflict
	}
	return cp, goalAsyncTransitionReplayed, nil
}

func (s *goalAsyncStore) loadCheckpointIfExists(ctx context.Context, q goalAsyncStoreQueryer, runID string) (goalAsyncCheckpoint, bool, error) {
	cp, err := s.loadCheckpoint(ctx, q, runID, true)
	if errors.Is(err, errGoalAsyncStoreNotFound) {
		return goalAsyncCheckpoint{}, false, nil
	}
	return cp, err == nil, err
}

func (s *goalAsyncStore) loadCheckpoint(ctx context.Context, q goalAsyncStoreQueryer, runID string, forUpdate bool) (goalAsyncCheckpoint, error) {
	query := `SELECT checkpoint_version, contract_version, revision, trace_id, correlation_id,
		agent_id, owner, principal_kind, principal_user_id, principal_username,
		principal_issuer, goal_id, goal_spec_hash, policy_sha256, state, slices,
		created_at, updated_at, deadline, reason_code, ledger_id, run_nonce_hash,
		last_transition_id, last_transition_hash, verification_verifier,
		verification_sha256, verified_at, max_slices, max_model_rounds,
		max_tool_attempts, max_result_bytes, max_evidence_records,
		max_evidence_bytes, max_retries, max_scheduled_work_requests,
		max_consecutive_denials, max_slice_elapsed_ns, used_model_rounds,
		used_tool_attempts, used_result_bytes, used_evidence_records,
		used_evidence_bytes, used_retries, used_scheduled_work_requests,
		used_consecutive_denials
		FROM AgentGoalRuns WHERE goal_run_id = ` + s.placeholder(1)
	if forUpdate && s.postgres {
		query += ` FOR UPDATE`
	}

	var (
		checkpointVersion         int
		contractVersion           string
		revision                  int64
		traceID                   string
		correlationID             sql.NullString
		agentID                   string
		owner                     string
		principalKind             string
		principalUserID           int64
		principalUsername         string
		principalIssuer           string
		goalID                    string
		goalSpecHash              string
		policySHA256              string
		state                     string
		slices                    int
		createdAt                 time.Time
		updatedAt                 time.Time
		deadline                  time.Time
		reasonCode                string
		ledgerID                  string
		runNonceHash              string
		lastTransitionID          sql.NullString
		lastTransitionHash        sql.NullString
		verificationVerifier      sql.NullString
		verificationSHA256        sql.NullString
		verifiedAt                sql.NullTime
		maxSlices                 int
		maxModelRounds            int
		maxToolAttempts           int
		maxResultBytes            int64
		maxEvidenceRecords        int
		maxEvidenceBytes          int64
		maxRetries                int
		maxScheduledWorkRequests  int
		maxConsecutiveDenials     int
		maxSliceElapsedNS         int64
		usedModelRounds           int
		usedToolAttempts          int
		usedResultBytes           int64
		usedEvidenceRecords       int
		usedEvidenceBytes         int64
		usedRetries               int
		usedScheduledWorkRequests int
		usedConsecutiveDenials    int
	)
	err := q.QueryRowContext(ctx, query, runID).Scan(
		&checkpointVersion, &contractVersion, &revision, &traceID, &correlationID,
		&agentID, &owner, &principalKind, &principalUserID, &principalUsername,
		&principalIssuer, &goalID, &goalSpecHash, &policySHA256, &state, &slices,
		&createdAt, &updatedAt, &deadline, &reasonCode, &ledgerID, &runNonceHash,
		&lastTransitionID, &lastTransitionHash, &verificationVerifier,
		&verificationSHA256, &verifiedAt, &maxSlices, &maxModelRounds,
		&maxToolAttempts, &maxResultBytes, &maxEvidenceRecords,
		&maxEvidenceBytes, &maxRetries, &maxScheduledWorkRequests,
		&maxConsecutiveDenials, &maxSliceElapsedNS, &usedModelRounds,
		&usedToolAttempts, &usedResultBytes, &usedEvidenceRecords,
		&usedEvidenceBytes, &usedRetries, &usedScheduledWorkRequests,
		&usedConsecutiveDenials,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return goalAsyncCheckpoint{}, fmt.Errorf("%w: %s", errGoalAsyncStoreNotFound, runID)
	}
	if err != nil {
		return goalAsyncCheckpoint{}, fmt.Errorf("read goal checkpoint root: %w", err)
	}
	if revision <= 0 || principalUserID < 0 {
		return goalAsyncCheckpoint{}, fmt.Errorf("%w: invalid signed identity or revision", errGoalAsyncStoreCorrupt)
	}
	cp := goalAsyncCheckpoint{
		CheckpointVersion: checkpointVersion,
		ContractVersion:   contractVersion,
		Revision:          uint64(revision),
		RunID:             runID,
		TraceID:           traceID,
		CorrelationID:     correlationID.String,
		AgentID:           agentID,
		Owner:             owner,
		PrincipalKind:     goalAsyncPrincipalKind(principalKind),
		PrincipalUserID:   uint64(principalUserID),
		PrincipalUsername: principalUsername,
		PrincipalIssuer:   principalIssuer,
		GoalID:            goalID,
		GoalSpecHash:      goalSpecHash,
		PolicySHA256:      policySHA256,
		State:             goalAsyncState(state),
		Slices:            slices,
		CreatedAt:         normalizeGoalAsyncTime(createdAt),
		UpdatedAt:         normalizeGoalAsyncTime(updatedAt),
		Deadline:          normalizeGoalAsyncTime(deadline),
		ReasonCode:        reasonCode,
		LedgerID:          ledgerID,
		RunNonceHash:      runNonceHash,
		Ceilings: goalAsyncCeilings{
			MaxSlices:                maxSlices,
			MaxModelRounds:           maxModelRounds,
			MaxToolAttempts:          maxToolAttempts,
			MaxResultBytes:           int(maxResultBytes),
			MaxEvidenceRecords:       maxEvidenceRecords,
			MaxEvidenceBytes:         int(maxEvidenceBytes),
			MaxRetries:               maxRetries,
			MaxScheduledWorkRequests: maxScheduledWorkRequests,
			MaxConsecutiveDenials:    maxConsecutiveDenials,
			MaxSliceElapsed:          time.Duration(maxSliceElapsedNS),
		},
		Used: goalAsyncUsed{
			ModelRounds:           usedModelRounds,
			ToolAttempts:          usedToolAttempts,
			ResultBytes:           int(usedResultBytes),
			EvidenceRecords:       usedEvidenceRecords,
			EvidenceBytes:         int(usedEvidenceBytes),
			Retries:               usedRetries,
			ScheduledWorkRequests: usedScheduledWorkRequests,
			ConsecutiveDenials:    usedConsecutiveDenials,
		},
		LastTransitionID:   lastTransitionID.String,
		LastTransitionHash: lastTransitionHash.String,
	}
	if verificationVerifier.Valid || verificationSHA256.Valid || verifiedAt.Valid {
		cp.Verification = &goalAsyncVerificationRef{
			Verifier:         verificationVerifier.String,
			EvaluationSHA256: verificationSHA256.String,
			VerifiedAt:       normalizeGoalAsyncTime(verifiedAt.Time),
		}
	}

	if cp.AllowedTools, err = s.loadTools(ctx, q, runID); err != nil {
		return goalAsyncCheckpoint{}, err
	}
	if cp.AllowedSources, err = s.loadSources(ctx, q, runID); err != nil {
		return goalAsyncCheckpoint{}, err
	}
	if cp.Work, err = s.loadWork(ctx, q, runID); err != nil {
		return goalAsyncCheckpoint{}, err
	}
	if cp.Evidence, err = s.loadEvidence(ctx, q, runID); err != nil {
		return goalAsyncCheckpoint{}, err
	}
	verificationIDs, err := s.loadVerificationEvidence(ctx, q, runID)
	if err != nil {
		return goalAsyncCheckpoint{}, err
	}
	if len(verificationIDs) > 0 {
		if cp.Verification == nil {
			return goalAsyncCheckpoint{}, fmt.Errorf("%w: verification evidence exists without root verification fields", errGoalAsyncStoreCorrupt)
		}
		cp.Verification.EvidenceIDs = verificationIDs
	}
	if cp.AuditRefs, err = s.loadAuditRefs(ctx, q, runID); err != nil {
		return goalAsyncCheckpoint{}, err
	}

	if err := validateGoalAsyncCheckpoint(cp); err != nil {
		return goalAsyncCheckpoint{}, fmt.Errorf("%w: %v", errGoalAsyncStoreCorrupt, err)
	}
	if err := s.validateTransitionLedger(ctx, q, cp); err != nil {
		return goalAsyncCheckpoint{}, err
	}
	return cp, nil
}

func (s *goalAsyncStore) loadTools(ctx context.Context, q goalAsyncStoreQueryer, runID string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT tool_name FROM AgentGoalRunTools WHERE goal_run_id = `+s.placeholder(1)+` ORDER BY tool_name`, runID)
	if err != nil {
		return nil, fmt.Errorf("read goal tools: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("read goal tool: %w", err)
		}
		out = append(out, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate goal tools: %w", err)
	}
	return out, nil
}

func (s *goalAsyncStore) loadSources(ctx context.Context, q goalAsyncStoreQueryer, runID string) ([]uint64, error) {
	rows, err := q.QueryContext(ctx, `SELECT source_id FROM AgentGoalRunSources WHERE goal_run_id = `+s.placeholder(1)+` ORDER BY source_id`, runID)
	if err != nil {
		return nil, fmt.Errorf("read goal sources: %w", err)
	}
	defer rows.Close()
	var out []uint64
	for rows.Next() {
		var sourceID int64
		if err := rows.Scan(&sourceID); err != nil {
			return nil, fmt.Errorf("read goal source: %w", err)
		}
		if sourceID <= 0 {
			return nil, fmt.Errorf("%w: invalid source ID", errGoalAsyncStoreCorrupt)
		}
		out = append(out, uint64(sourceID))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate goal sources: %w", err)
	}
	return out, nil
}

func (s *goalAsyncStore) loadWork(ctx context.Context, q goalAsyncStoreQueryer, runID string) ([]goalAsyncWorkRef, error) {
	rows, err := q.QueryContext(ctx, `SELECT request_id, kind, state, proposal_sha256,
		approval_id, approval_sha256, operation_id, source_id, proposal_expires_at,
		approval_expires_at, submit_by, accepted_at, observed_at
		FROM AgentGoalRunWork WHERE goal_run_id = `+s.placeholder(1)+` ORDER BY request_id`, runID)
	if err != nil {
		return nil, fmt.Errorf("read goal work: %w", err)
	}
	defer rows.Close()
	var out []goalAsyncWorkRef
	for rows.Next() {
		var (
			requestID, kind, state, proposalSHA  string
			approvalID, approvalSHA, operationID sql.NullString
			sourceID                             int64
			proposalExpiresAt                    time.Time
			approvalExpiresAt, submitBy          sql.NullTime
			acceptedAt, observedAt               sql.NullTime
		)
		if err := rows.Scan(&requestID, &kind, &state, &proposalSHA,
			&approvalID, &approvalSHA, &operationID, &sourceID, &proposalExpiresAt,
			&approvalExpiresAt, &submitBy, &acceptedAt, &observedAt); err != nil {
			return nil, fmt.Errorf("read goal work row: %w", err)
		}
		if sourceID <= 0 {
			return nil, fmt.Errorf("%w: invalid work source ID", errGoalAsyncStoreCorrupt)
		}
		ref := goalAsyncWorkRef{
			Kind:              goalAsyncWorkKind(kind),
			State:             goalAsyncWorkState(state),
			RequestID:         requestID,
			ProposalSHA256:    proposalSHA,
			ApprovalID:        approvalID.String,
			ApprovalSHA256:    approvalSHA.String,
			OperationID:       operationID.String,
			SourceID:          uint64(sourceID),
			ProposalExpiresAt: normalizeGoalAsyncTime(proposalExpiresAt),
		}
		if approvalExpiresAt.Valid {
			ref.ApprovalExpiresAt = normalizeGoalAsyncTime(approvalExpiresAt.Time)
		}
		if submitBy.Valid {
			ref.SubmitBy = normalizeGoalAsyncTime(submitBy.Time)
		}
		if acceptedAt.Valid {
			ref.AcceptedAt = normalizeGoalAsyncTime(acceptedAt.Time)
		}
		if observedAt.Valid {
			ref.ObservedAt = normalizeGoalAsyncTime(observedAt.Time)
		}
		out = append(out, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate goal work: %w", err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].sortKey() < out[j].sortKey() })
	return out, nil
}

func (s *goalAsyncStore) loadEvidence(ctx context.Context, q goalAsyncStoreQueryer, runID string) ([]goalAsyncEvidenceRef, error) {
	rows, err := q.QueryContext(ctx, `SELECT evidence_id, kind, trust_class, producer, source_ids,
		observed_at, expires_at, payload_sha256, record_sha256, size_bytes
		FROM AgentGoalRunEvidence WHERE goal_run_id = `+s.placeholder(1)+` ORDER BY evidence_id`, runID)
	if err != nil {
		return nil, fmt.Errorf("read goal evidence: %w", err)
	}
	defer rows.Close()
	var out []goalAsyncEvidenceRef
	for rows.Next() {
		var (
			evidenceID, kind, trustClass, producer string
			sourceIDs                              []byte
			observedAt, expiresAt                  time.Time
			payloadSHA, recordSHA                  string
			sizeBytes                              int
		)
		if err := rows.Scan(&evidenceID, &kind, &trustClass, &producer, &sourceIDs,
			&observedAt, &expiresAt, &payloadSHA, &recordSHA, &sizeBytes); err != nil {
			return nil, fmt.Errorf("read goal evidence row: %w", err)
		}
		var ids []uint64
		if err := json.Unmarshal(sourceIDs, &ids); err != nil {
			return nil, fmt.Errorf("%w: invalid evidence source_ids: %v", errGoalAsyncStoreCorrupt, err)
		}
		out = append(out, goalAsyncEvidenceRef{
			EvidenceID:    evidenceID,
			Kind:          EvidenceKind(kind),
			TrustClass:    TrustClass(trustClass),
			Producer:      producer,
			SourceIDs:     ids,
			ObservedAt:    normalizeGoalAsyncTime(observedAt),
			ExpiresAt:     normalizeGoalAsyncTime(expiresAt),
			PayloadSHA256: payloadSHA,
			RecordSHA256:  recordSHA,
			SizeBytes:     sizeBytes,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate goal evidence: %w", err)
	}
	return out, nil
}

func (s *goalAsyncStore) loadVerificationEvidence(ctx context.Context, q goalAsyncStoreQueryer, runID string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT evidence_id FROM AgentGoalRunVerificationEvidence WHERE goal_run_id = `+s.placeholder(1)+` ORDER BY evidence_id`, runID)
	if err != nil {
		return nil, fmt.Errorf("read goal verification evidence: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var evidenceID string
		if err := rows.Scan(&evidenceID); err != nil {
			return nil, fmt.Errorf("read goal verification evidence row: %w", err)
		}
		out = append(out, evidenceID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate goal verification evidence: %w", err)
	}
	return out, nil
}

func (s *goalAsyncStore) loadAuditRefs(ctx context.Context, q goalAsyncStoreQueryer, runID string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT audit_ref FROM AgentGoalRunAuditRefs WHERE goal_run_id = `+s.placeholder(1)+` ORDER BY audit_ref`, runID)
	if err != nil {
		return nil, fmt.Errorf("read goal audit refs: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return nil, fmt.Errorf("read goal audit ref: %w", err)
		}
		out = append(out, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate goal audit refs: %w", err)
	}
	return out, nil
}

type goalAsyncTransitionRecord struct {
	Revision       uint64
	OperationID    string
	TransitionHash string
	FromState      goalAsyncState
	ToState        goalAsyncState
	ReasonCode     string
	CreatedAt      time.Time
}

func (s *goalAsyncStore) findTransition(ctx context.Context, q goalAsyncStoreQueryer, runID, operationID string) (goalAsyncTransitionRecord, bool, error) {
	query := `SELECT revision, operation_id, transition_hash, from_state, to_state, reason_code, created_at
		FROM AgentGoalRunTransitions WHERE goal_run_id = ` + s.placeholder(1) + ` AND operation_id = ` + s.placeholder(2)
	var record goalAsyncTransitionRecord
	var revision int64
	var fromState, toState string
	err := q.QueryRowContext(ctx, query, runID, operationID).Scan(
		&revision, &record.OperationID, &record.TransitionHash,
		&fromState, &toState, &record.ReasonCode, &record.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return goalAsyncTransitionRecord{}, false, nil
	}
	if err != nil {
		return goalAsyncTransitionRecord{}, false, fmt.Errorf("read goal transition: %w", err)
	}
	if revision <= 0 {
		return goalAsyncTransitionRecord{}, false, fmt.Errorf("%w: invalid transition revision", errGoalAsyncStoreCorrupt)
	}
	record.Revision = uint64(revision)
	record.FromState = goalAsyncState(fromState)
	record.ToState = goalAsyncState(toState)
	record.CreatedAt = normalizeGoalAsyncTime(record.CreatedAt)
	return record, true, nil
}

func (s *goalAsyncStore) validateTransitionLedger(ctx context.Context, q goalAsyncStoreQueryer, cp goalAsyncCheckpoint) error {
	rows, err := q.QueryContext(ctx, `SELECT revision, operation_id, transition_hash, from_state, to_state, reason_code, created_at
		FROM AgentGoalRunTransitions WHERE goal_run_id = `+s.placeholder(1)+` ORDER BY revision`, cp.RunID)
	if err != nil {
		return fmt.Errorf("read goal transition ledger: %w", err)
	}
	defer rows.Close()
	var records []goalAsyncTransitionRecord
	for rows.Next() {
		var record goalAsyncTransitionRecord
		var revision int64
		var fromState, toState string
		if err := rows.Scan(&revision, &record.OperationID, &record.TransitionHash,
			&fromState, &toState, &record.ReasonCode, &record.CreatedAt); err != nil {
			return fmt.Errorf("read goal transition ledger row: %w", err)
		}
		if revision <= 0 {
			return fmt.Errorf("%w: invalid transition revision", errGoalAsyncStoreCorrupt)
		}
		record.Revision = uint64(revision)
		record.FromState = goalAsyncState(fromState)
		record.ToState = goalAsyncState(toState)
		record.CreatedAt = normalizeGoalAsyncTime(record.CreatedAt)
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate goal transition ledger: %w", err)
	}
	if uint64(len(records)+1) != cp.Revision {
		return fmt.Errorf("%w: transition ledger has %d rows for checkpoint revision %d", errGoalAsyncStoreCorrupt, len(records), cp.Revision)
	}
	if cp.Revision == 1 {
		return nil
	}
	expectedFrom := goalAsyncCreated
	var previousCreatedAt time.Time
	for i, record := range records {
		wantRevision := uint64(i + 2)
		if record.Revision != wantRevision {
			return fmt.Errorf("%w: transition ledger revision %d, want %d", errGoalAsyncStoreCorrupt, record.Revision, wantRevision)
		}
		if record.FromState != expectedFrom {
			return fmt.Errorf("%w: transition ledger state chain breaks at revision %d", errGoalAsyncStoreCorrupt, record.Revision)
		}
		if !goalAsyncTransitionLedgerStatesAllowed(record.FromState, record.ToState) {
			return fmt.Errorf("%w: transition ledger state pair is invalid at revision %d", errGoalAsyncStoreCorrupt, record.Revision)
		}
		if record.CreatedAt.Before(cp.CreatedAt) || record.CreatedAt.After(cp.UpdatedAt) {
			return fmt.Errorf("%w: transition ledger timestamp is outside the checkpoint interval at revision %d", errGoalAsyncStoreCorrupt, record.Revision)
		}
		if !previousCreatedAt.IsZero() && record.CreatedAt.Before(previousCreatedAt) {
			return fmt.Errorf("%w: transition ledger timestamps run backwards at revision %d", errGoalAsyncStoreCorrupt, record.Revision)
		}
		previousCreatedAt = record.CreatedAt
		expectedFrom = record.ToState
	}
	last := records[len(records)-1]
	if last.TransitionHash != cp.LastTransitionHash ||
		last.OperationID != cp.LastTransitionID ||
		last.ToState != cp.State ||
		last.ReasonCode != cp.ReasonCode ||
		!last.CreatedAt.Equal(cp.UpdatedAt) {
		return fmt.Errorf("%w: checkpoint does not match its last transition ledger row", errGoalAsyncStoreCorrupt)
	}
	return nil
}

func (s *goalAsyncStore) insertTransition(ctx context.Context, q goalAsyncStoreQueryer, previous, next goalAsyncCheckpoint) error {
	query := `INSERT INTO AgentGoalRunTransitions
		(goal_run_id, revision, operation_id, transition_hash, from_state, to_state, reason_code, created_at)
		VALUES (` + s.placeholders(8) + `)`
	return s.exec(ctx, q, query,
		next.RunID, int64(next.Revision), next.LastTransitionID, next.LastTransitionHash,
		string(previous.State), string(next.State), next.ReasonCode, next.UpdatedAt)
}

func (s *goalAsyncStore) insertRoot(ctx context.Context, q goalAsyncStoreQueryer, cp goalAsyncCheckpoint) error {
	columns := []string{
		"goal_run_id", "checkpoint_version", "contract_version", "revision",
		"trace_id", "correlation_id", "agent_id", "owner", "principal_kind",
		"principal_user_id", "principal_username", "principal_issuer",
		"goal_id", "goal_spec_hash", "policy_sha256", "state", "slices",
		"created_at", "updated_at", "deadline", "reason_code", "ledger_id",
		"run_nonce_hash", "last_transition_id", "last_transition_hash",
		"verification_verifier", "verification_sha256", "verified_at",
		"cancel_requested_at", "cancel_requested_by_user_id", "max_slices",
		"max_model_rounds", "max_tool_attempts", "max_result_bytes",
		"max_evidence_records", "max_evidence_bytes", "max_retries",
		"max_scheduled_work_requests", "max_consecutive_denials",
		"max_slice_elapsed_ns", "used_model_rounds", "used_tool_attempts",
		"used_result_bytes", "used_evidence_records", "used_evidence_bytes",
		"used_retries", "used_scheduled_work_requests", "used_consecutive_denials",
	}
	values := s.rootValues(cp)
	query := `INSERT INTO AgentGoalRuns (` + strings.Join(columns, ", ") + `) VALUES (` + s.placeholders(len(values)) + `)`
	return s.exec(ctx, q, query, values...)
}

func (s *goalAsyncStore) rootValues(cp goalAsyncCheckpoint) []any {
	var verificationVerifier, verificationSHA any
	var verifiedAt any
	if cp.Verification != nil {
		verificationVerifier = cp.Verification.Verifier
		verificationSHA = cp.Verification.EvaluationSHA256
		verifiedAt = normalizeGoalAsyncTime(cp.Verification.VerifiedAt)
	}
	return []any{
		cp.RunID, cp.CheckpointVersion, cp.ContractVersion, int64(cp.Revision),
		cp.TraceID, nilIfEmpty(cp.CorrelationID), cp.AgentID, cp.Owner,
		string(cp.PrincipalKind), int64(cp.PrincipalUserID), cp.PrincipalUsername,
		cp.PrincipalIssuer, cp.GoalID, cp.GoalSpecHash, cp.PolicySHA256,
		string(cp.State), cp.Slices, normalizeGoalAsyncTime(cp.CreatedAt),
		normalizeGoalAsyncTime(cp.UpdatedAt), normalizeGoalAsyncTime(cp.Deadline),
		cp.ReasonCode, cp.LedgerID, cp.RunNonceHash, nilIfEmpty(cp.LastTransitionID),
		nilIfEmpty(cp.LastTransitionHash), verificationVerifier, verificationSHA,
		verifiedAt, nil, nil, cp.Ceilings.MaxSlices, cp.Ceilings.MaxModelRounds,
		cp.Ceilings.MaxToolAttempts, int64(cp.Ceilings.MaxResultBytes),
		cp.Ceilings.MaxEvidenceRecords, int64(cp.Ceilings.MaxEvidenceBytes),
		cp.Ceilings.MaxRetries, cp.Ceilings.MaxScheduledWorkRequests,
		cp.Ceilings.MaxConsecutiveDenials, int64(cp.Ceilings.MaxSliceElapsed),
		cp.Used.ModelRounds, cp.Used.ToolAttempts, int64(cp.Used.ResultBytes),
		cp.Used.EvidenceRecords, int64(cp.Used.EvidenceBytes), cp.Used.Retries,
		cp.Used.ScheduledWorkRequests, cp.Used.ConsecutiveDenials,
	}
}

func (s *goalAsyncStore) insertScope(ctx context.Context, q goalAsyncStoreQueryer, cp goalAsyncCheckpoint) error {
	for _, tool := range cp.AllowedTools {
		if err := s.exec(ctx, q, `INSERT INTO AgentGoalRunTools (goal_run_id, tool_name) VALUES (`+s.placeholders(2)+`)`, cp.RunID, tool); err != nil {
			return fmt.Errorf("insert goal tool: %w", err)
		}
	}
	for _, sourceID := range cp.AllowedSources {
		if err := s.exec(ctx, q, `INSERT INTO AgentGoalRunSources (goal_run_id, source_id) VALUES (`+s.placeholders(2)+`)`, cp.RunID, int64(sourceID)); err != nil {
			return fmt.Errorf("insert goal source: %w", err)
		}
	}
	return nil
}

func (s *goalAsyncStore) updateRoot(ctx context.Context, q goalAsyncStoreQueryer, previous, next goalAsyncCheckpoint) error {
	fields := []struct {
		name  string
		value any
	}{
		{"revision", int64(next.Revision)},
		{"state", string(next.State)},
		{"slices", next.Slices},
		{"updated_at", normalizeGoalAsyncTime(next.UpdatedAt)},
		{"reason_code", next.ReasonCode},
		{"last_transition_id", nilIfEmpty(next.LastTransitionID)},
		{"last_transition_hash", nilIfEmpty(next.LastTransitionHash)},
		{"verification_verifier", nil},
		{"verification_sha256", nil},
		{"verified_at", nil},
		{"used_model_rounds", next.Used.ModelRounds},
		{"used_tool_attempts", next.Used.ToolAttempts},
		{"used_result_bytes", int64(next.Used.ResultBytes)},
		{"used_evidence_records", next.Used.EvidenceRecords},
		{"used_evidence_bytes", int64(next.Used.EvidenceBytes)},
		{"used_retries", next.Used.Retries},
		{"used_scheduled_work_requests", next.Used.ScheduledWorkRequests},
		{"used_consecutive_denials", next.Used.ConsecutiveDenials},
	}
	if next.Verification != nil {
		fields[7].value = next.Verification.Verifier
		fields[8].value = next.Verification.EvaluationSHA256
		fields[9].value = normalizeGoalAsyncTime(next.Verification.VerifiedAt)
	}

	sets := make([]string, len(fields))
	args := make([]any, 0, len(fields)+3)
	for i, field := range fields {
		sets[i] = field.name + " = " + s.placeholder(i+1)
		args = append(args, field.value)
	}
	args = append(args, next.RunID, int64(previous.Revision), string(previous.State))
	where := `goal_run_id = ` + s.placeholder(len(fields)+1) +
		` AND revision = ` + s.placeholder(len(fields)+2) +
		` AND state = ` + s.placeholder(len(fields)+3) +
		` AND cancel_requested_at IS NULL`
	query := `UPDATE AgentGoalRuns SET ` + strings.Join(sets, ", ") + ` WHERE ` + where
	result, err := q.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update goal checkpoint root: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read goal checkpoint update result: %w", err)
	}
	if affected != 1 {
		pending, err := s.cancellationPending(ctx, q, next.RunID)
		if err != nil {
			return fmt.Errorf("classify goal checkpoint CAS: %w", err)
		}
		if pending {
			return ErrGoalAsyncCancellationPending
		}
		return ErrGoalAsyncRevisionConflict
	}
	return nil
}

func (s *goalAsyncStore) cancellationPending(ctx context.Context, q goalAsyncStoreQueryer, runID string) (bool, error) {
	var cancelRequestedAt sql.NullTime
	err := q.QueryRowContext(ctx,
		`SELECT cancel_requested_at FROM AgentGoalRuns WHERE goal_run_id = `+s.placeholder(1),
		runID).Scan(&cancelRequestedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read goal cancellation marker: %w", err)
	}
	return cancelRequestedAt.Valid, nil
}

func (s *goalAsyncStore) syncChildren(ctx context.Context, q goalAsyncStoreQueryer, previous, next goalAsyncCheckpoint) error {
	if err := s.syncWork(ctx, q, previous, next); err != nil {
		return err
	}
	if err := s.syncEvidence(ctx, q, previous, next); err != nil {
		return err
	}
	if err := s.syncVerification(ctx, q, previous, next); err != nil {
		return err
	}
	return s.syncAuditRefs(ctx, q, previous, next)
}

func (s *goalAsyncStore) syncWork(ctx context.Context, q goalAsyncStoreQueryer, previous, next goalAsyncCheckpoint) error {
	nextByRequest := make(map[string]goalAsyncWorkRef, len(next.Work))
	for _, work := range next.Work {
		nextByRequest[work.RequestID] = work
	}
	for _, old := range previous.Work {
		if _, ok := nextByRequest[old.RequestID]; ok {
			continue
		}
		if err := s.exec(ctx, q, `DELETE FROM AgentGoalRunWork WHERE goal_run_id = `+s.placeholder(1)+` AND request_id = `+s.placeholder(2), next.RunID, old.RequestID); err != nil {
			return fmt.Errorf("delete replaced goal work: %w", err)
		}
	}
	for _, work := range next.Work {
		query := `INSERT INTO AgentGoalRunWork
			(goal_run_id, request_id, kind, state, proposal_sha256, approval_id,
			 approval_sha256, operation_id, source_id, proposal_expires_at,
			 approval_expires_at, submit_by, accepted_at, observed_at, created_at, updated_at)
			VALUES (` + s.placeholders(16) + `)
			ON CONFLICT (goal_run_id, request_id) DO UPDATE SET
				kind = excluded.kind, state = excluded.state,
				proposal_sha256 = excluded.proposal_sha256,
				approval_id = excluded.approval_id,
				approval_sha256 = excluded.approval_sha256,
				operation_id = excluded.operation_id, source_id = excluded.source_id,
				proposal_expires_at = excluded.proposal_expires_at,
				approval_expires_at = excluded.approval_expires_at,
				submit_by = excluded.submit_by, accepted_at = excluded.accepted_at,
				observed_at = excluded.observed_at, updated_at = excluded.updated_at`
		if err := s.exec(ctx, q, query,
			next.RunID, work.RequestID, string(work.Kind), string(work.State),
			work.ProposalSHA256, nilIfEmpty(work.ApprovalID), nilIfEmpty(work.ApprovalSHA256),
			nilIfEmpty(work.OperationID), int64(work.SourceID),
			normalizeGoalAsyncTime(work.ProposalExpiresAt), nilIfZeroTime(work.ApprovalExpiresAt),
			nilIfZeroTime(work.SubmitBy), nilIfZeroTime(work.AcceptedAt),
			nilIfZeroTime(work.ObservedAt), normalizeGoalAsyncTime(next.UpdatedAt),
			normalizeGoalAsyncTime(next.UpdatedAt)); err != nil {
			return fmt.Errorf("write goal work: %w", err)
		}
	}
	return nil
}

func (s *goalAsyncStore) syncEvidence(ctx context.Context, q goalAsyncStoreQueryer, previous, next goalAsyncCheckpoint) error {
	previousByID := make(map[string]goalAsyncEvidenceRef, len(previous.Evidence))
	for _, ref := range previous.Evidence {
		previousByID[ref.EvidenceID] = ref
	}
	nextByID := make(map[string]bool, len(next.Evidence))
	for _, ref := range next.Evidence {
		nextByID[ref.EvidenceID] = true
	}
	for id := range previousByID {
		if !nextByID[id] {
			return fmt.Errorf("%w: evidence %q was removed", errGoalAsyncStoreCorrupt, id)
		}
	}
	for _, ref := range next.Evidence {
		if existing, ok := previousByID[ref.EvidenceID]; ok {
			if existing.RecordSHA256 != ref.RecordSHA256 {
				return fmt.Errorf("%w: evidence %q changed identity", errGoalAsyncStoreCorrupt, ref.EvidenceID)
			}
			continue
		}
		sourceIDList := ref.SourceIDs
		if len(sourceIDList) == 0 {
			sourceIDList = []uint64{}
		}
		sourceIDs, err := json.Marshal(sourceIDList)
		if err != nil {
			return fmt.Errorf("encode evidence source IDs: %w", err)
		}
		placeholders := make([]string, 12)
		for i := range placeholders {
			if i == 5 {
				placeholders[i] = s.jsonPlaceholder(i + 1)
			} else {
				placeholders[i] = s.placeholder(i + 1)
			}
		}
		query := `INSERT INTO AgentGoalRunEvidence
			(goal_run_id, evidence_id, kind, trust_class, producer, source_ids,
			 observed_at, expires_at, payload_sha256, record_sha256, size_bytes, created_at)
			VALUES (` + strings.Join(placeholders, ", ") + `)`
		if err := s.exec(ctx, q, query,
			next.RunID, ref.EvidenceID, string(ref.Kind), string(ref.TrustClass),
			ref.Producer, string(sourceIDs), normalizeGoalAsyncTime(ref.ObservedAt),
			normalizeGoalAsyncTime(ref.ExpiresAt), ref.PayloadSHA256,
			ref.RecordSHA256, ref.SizeBytes, normalizeGoalAsyncTime(next.UpdatedAt)); err != nil {
			return fmt.Errorf("insert goal evidence: %w", err)
		}
	}
	return nil
}

func (s *goalAsyncStore) syncVerification(ctx context.Context, q goalAsyncStoreQueryer, previous, next goalAsyncCheckpoint) error {
	if previous.Verification != nil && next.Verification == nil {
		return fmt.Errorf("%w: verification reference was removed", errGoalAsyncStoreCorrupt)
	}
	if next.Verification == nil {
		return nil
	}
	previousIDs := map[string]bool{}
	if previous.Verification != nil {
		for _, id := range previous.Verification.EvidenceIDs {
			previousIDs[id] = true
		}
	}
	nextIDs := map[string]bool{}
	for _, id := range next.Verification.EvidenceIDs {
		nextIDs[id] = true
		if previousIDs[id] {
			continue
		}
		query := `INSERT INTO AgentGoalRunVerificationEvidence (goal_run_id, evidence_id, created_at)
			VALUES (` + s.placeholders(3) + `) ON CONFLICT (goal_run_id, evidence_id) DO NOTHING`
		if err := s.exec(ctx, q, query, next.RunID, id, normalizeGoalAsyncTime(next.UpdatedAt)); err != nil {
			return fmt.Errorf("insert goal verification evidence: %w", err)
		}
	}
	for id := range previousIDs {
		if !nextIDs[id] {
			return fmt.Errorf("%w: verification evidence %q was removed", errGoalAsyncStoreCorrupt, id)
		}
	}
	return nil
}

func (s *goalAsyncStore) syncAuditRefs(ctx context.Context, q goalAsyncStoreQueryer, previous, next goalAsyncCheckpoint) error {
	previousRefs := map[string]bool{}
	for _, ref := range previous.AuditRefs {
		previousRefs[ref] = true
	}
	nextRefs := map[string]bool{}
	for _, ref := range next.AuditRefs {
		nextRefs[ref] = true
		if previousRefs[ref] {
			continue
		}
		query := `INSERT INTO AgentGoalRunAuditRefs (goal_run_id, audit_ref, created_at)
			VALUES (` + s.placeholders(3) + `) ON CONFLICT (goal_run_id, audit_ref) DO NOTHING`
		if err := s.exec(ctx, q, query, next.RunID, ref, normalizeGoalAsyncTime(next.UpdatedAt)); err != nil {
			return fmt.Errorf("insert goal audit ref: %w", err)
		}
	}
	for ref := range previousRefs {
		if !nextRefs[ref] {
			return fmt.Errorf("%w: audit ref %q was removed", errGoalAsyncStoreCorrupt, ref)
		}
	}
	return nil
}

func (s *goalAsyncStore) exec(ctx context.Context, q goalAsyncStoreQueryer, query string, args ...any) error {
	_, err := q.ExecContext(ctx, query, args...)
	return err
}

func (s *goalAsyncStore) placeholder(n int) string {
	if s.postgres {
		return fmt.Sprintf("$%d", n)
	}
	return "?"
}

func (s *goalAsyncStore) placeholders(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = s.placeholder(i + 1)
	}
	return strings.Join(parts, ", ")
}

func (s *goalAsyncStore) jsonPlaceholder(n int) string {
	if s.postgres {
		return s.placeholder(n) + "::jsonb"
	}
	return s.placeholder(n)
}

func (s *goalAsyncStore) mapWriteError(operation string, err error) error {
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	if isUniqueConstraintError(err) {
		return fmt.Errorf("%w: %s", ErrGoalAsyncReplayConflict, operation)
	}
	if s.postgres {
		return fmt.Errorf("%s: %w", operation, err)
	}
	if strings.Contains(message, "database is locked") ||
		strings.Contains(message, "database table is locked") ||
		strings.Contains(message, "sqlite_busy") {
		return fmt.Errorf("%w: %s", ErrGoalAsyncRevisionConflict, operation)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func nilIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nilIfZeroTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return normalizeGoalAsyncTime(value)
}

func isUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "duplicate key value violates unique constraint") ||
		strings.Contains(message, "unique constraint failed") ||
		strings.Contains(message, "unique constraint")
}

func goalAsyncTransitionLedgerStatesAllowed(from, to goalAsyncState) bool {
	targets, ok := goalAsyncTransitionTable[from]
	if !ok {
		return false
	}
	for _, target := range targets {
		if target == to {
			return true
		}
	}
	return false
}
