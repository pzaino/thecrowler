package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Durable crawl-proposal lifecycle states.
const (
	CrawlProposalStatePending   = "pending"
	CrawlProposalStateSubmitted = "submitted"
	CrawlProposalStateExpired   = "expired"
	CrawlProposalStateDenied    = "denied"
)

// CrawlProposalRow is one durable, trusted agent proposal. It is created before
// any scheduling mutation and is the restart-safe anchor used by operator
// approval and submission.
type CrawlProposalRow struct {
	RequestID string

	RunID               string
	ToolCallID          string
	AgentID             string
	Owner               string
	PrincipalUserID     uint64
	SourceID            uint64
	OperationType       string
	ProposalSHA256      string
	TraceID             string
	MaxRequests         int
	BudgetWindowSeconds int64
	PolicyJSON          string
	State               string
	ExpiresAt           time.Time

	SubmittedAt   sql.NullTime
	OperationID   sql.NullString
	CreatedAt     time.Time
	LastUpdatedAt time.Time
}

func crawlProposalPlaceholder(db Handler, position int) string {
	if normalizeInformationSeedDBMS(db.DBMS()) == DBPostgresStr {
		return "$" + strconv.Itoa(position)
	}
	return "?"
}

func supportsCrawlProposalStore(db Handler) bool {
	switch normalizeInformationSeedDBMS(db.DBMS()) {
	case DBPostgresStr, DBSQLiteStr:
		return true
	default:
		return false
	}
}

func crawlProposalColumns() string {
	return `request_id, run_id, tool_call_id, agent_id, owner,
		principal_user_id, source_id, operation_type, proposal_sha256,
		trace_id, max_requests, budget_window_seconds, policy_json, state, expires_at,
		submitted_at, operation_id, created_at, last_updated_at`
}

func scanCrawlProposalRow(scanner interface{ Scan(dest ...any) error }) (CrawlProposalRow, error) {
	var row CrawlProposalRow
	err := scanner.Scan(
		&row.RequestID, &row.RunID, &row.ToolCallID, &row.AgentID, &row.Owner,
		&row.PrincipalUserID, &row.SourceID, &row.OperationType, &row.ProposalSHA256,
		&row.TraceID, &row.MaxRequests, &row.BudgetWindowSeconds, &row.PolicyJSON, &row.State,
		&row.ExpiresAt, &row.SubmittedAt, &row.OperationID,
		&row.CreatedAt, &row.LastUpdatedAt,
	)
	return row, err
}

// InsertCrawlProposal inserts a proposal unless its identity or logical key
// already exists. It returns true when this call created the row.
func InsertCrawlProposal(ctx context.Context, db *Handler, row CrawlProposalRow) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil || *db == nil {
		return false, fmt.Errorf("database handler is nil")
	}
	if !supportsCrawlProposalStore(*db) {
		return false, ErrCrawlApprovalStoreUnsupported
	}
	if row.State == "" {
		row.State = CrawlProposalStatePending
	}
	p := func(n int) string { return crawlProposalPlaceholder(*db, n) }
	query := `INSERT INTO CrawlProposals
		(request_id, run_id, tool_call_id, agent_id, owner, principal_user_id,
		 source_id, operation_type, proposal_sha256, trace_id, max_requests,
		 budget_window_seconds, policy_json, state, expires_at)
		VALUES (` + p(1) + `, ` + p(2) + `, ` + p(3) + `, ` + p(4) + `, ` + p(5) + `, ` + p(6) + `,
		 ` + p(7) + `, ` + p(8) + `, ` + p(9) + `, ` + p(10) + `, ` + p(11) + `, ` + p(12) + `,
		 ` + p(13) + `, ` + p(14) + `, ` + p(15) + `)
		ON CONFLICT DO NOTHING`
	result, err := (*db).ExecContext(ctx, query,
		row.RequestID, row.RunID, row.ToolCallID, row.AgentID, row.Owner,
		row.PrincipalUserID, row.SourceID, row.OperationType, row.ProposalSHA256,
		row.TraceID, row.MaxRequests, row.BudgetWindowSeconds, row.PolicyJSON, row.State,
		row.ExpiresAt.UTC().Truncate(time.Microsecond),
	)
	if err != nil {
		return false, crawlApprovalStoreError(err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

// GetCrawlProposalByRequest returns a proposal by its stable request ID.
func GetCrawlProposalByRequest(ctx context.Context, db *Handler, requestID string) (CrawlProposalRow, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil || *db == nil {
		return CrawlProposalRow{}, false, fmt.Errorf("database handler is nil")
	}
	if !supportsCrawlProposalStore(*db) {
		return CrawlProposalRow{}, false, ErrCrawlApprovalStoreUnsupported
	}
	query := `SELECT ` + crawlProposalColumns() + ` FROM CrawlProposals
		WHERE request_id = ` + crawlProposalPlaceholder(*db, 1)
	row, err := scanCrawlProposalRow((*db).QueryRowContext(ctx, query, strings.TrimSpace(requestID)))
	if errors.Is(err, sql.ErrNoRows) {
		return CrawlProposalRow{}, false, nil
	}
	if err != nil {
		return CrawlProposalRow{}, false, crawlApprovalStoreError(err)
	}
	return row, true, nil
}

// GetCrawlProposalByLogicalKey returns the proposal for one trusted logical
// tool invocation. This is the restart-safe duplicate-delivery lookup.
func GetCrawlProposalByLogicalKey(ctx context.Context, db *Handler, runID, toolCallID string, sourceID uint64, operationType string) (CrawlProposalRow, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil || *db == nil {
		return CrawlProposalRow{}, false, fmt.Errorf("database handler is nil")
	}
	if !supportsCrawlProposalStore(*db) {
		return CrawlProposalRow{}, false, ErrCrawlApprovalStoreUnsupported
	}
	p := func(n int) string { return crawlProposalPlaceholder(*db, n) }
	query := `SELECT ` + crawlProposalColumns() + ` FROM CrawlProposals
		WHERE run_id = ` + p(1) + ` AND tool_call_id = ` + p(2) + `
		  AND source_id = ` + p(3) + ` AND operation_type = ` + p(4) + ` LIMIT 1`
	row, err := scanCrawlProposalRow((*db).QueryRowContext(ctx, query, runID, toolCallID, sourceID, operationType))
	if errors.Is(err, sql.ErrNoRows) {
		return CrawlProposalRow{}, false, nil
	}
	if err != nil {
		return CrawlProposalRow{}, false, crawlApprovalStoreError(err)
	}
	return row, true, nil
}

// CrawlProposalTransitionOutcome describes the result of a compare-and-set
// proposal lifecycle transition. Callers must not treat every non-error return
// as an applied transition.
type CrawlProposalTransitionOutcome string

const (
	CrawlProposalTransitionApplied  CrawlProposalTransitionOutcome = "applied"
	CrawlProposalTransitionAlready  CrawlProposalTransitionOutcome = "already_in_state"
	CrawlProposalTransitionConflict CrawlProposalTransitionOutcome = "conflict"
	CrawlProposalTransitionNotFound CrawlProposalTransitionOutcome = "not_found"
)

// CrawlProposalTransitionResult carries the outcome and the authoritative row
// observed after the transition attempt.
type CrawlProposalTransitionResult struct {
	Outcome CrawlProposalTransitionOutcome
	Row     CrawlProposalRow
}

var (
	// ErrCrawlProposalRowNotFound means the durable proposal does not exist.
	ErrCrawlProposalRowNotFound = errors.New("crawl proposal row not found")
	// ErrCrawlProposalStateConflict means the current durable state does not
	// permit the requested transition.
	ErrCrawlProposalStateConflict = errors.New("crawl proposal state conflict")
)

// lockCrawlProposalTx locks and reads one proposal inside an existing
// transaction. PostgreSQL row locking serializes submission, expiry and
// reconciliation for the same request.
func lockCrawlProposalTx(ctx context.Context, tx *sql.Tx, db Handler, requestID string) (CrawlProposalRow, bool, error) {
	if tx == nil {
		return CrawlProposalRow{}, false, fmt.Errorf("transaction is nil")
	}
	query := `SELECT ` + crawlProposalColumns() + ` FROM CrawlProposals
		WHERE request_id = ` + crawlProposalPlaceholder(db, 1)
	if normalizeInformationSeedDBMS(db.DBMS()) == DBPostgresStr {
		query += ` FOR UPDATE`
	}
	row, err := scanCrawlProposalRow(tx.QueryRowContext(ctx, query, strings.TrimSpace(requestID)))
	if errors.Is(err, sql.ErrNoRows) {
		return CrawlProposalRow{}, false, nil
	}
	if err != nil {
		return CrawlProposalRow{}, false, crawlProposalContextError(ctx, crawlApprovalStoreError(err))
	}
	return row, true, nil
}

// MarkCrawlProposalSubmitted applies pending -> submitted. Repeating the same
// transition with the same operation ID returns Already; a different operation
// ID or a conflicting source state returns Conflict.
func MarkCrawlProposalSubmitted(ctx context.Context, db *Handler, requestID, operationID string, submittedAt time.Time) (CrawlProposalTransitionResult, error) {
	return transitionCrawlProposal(ctx, db, requestID, CrawlProposalStateSubmitted, operationID, submittedAt, false)
}

// ReconcileCrawlProposalSubmitted applies pending -> submitted or expired ->
// submitted when an authoritative accepted operation is already present. It is
// used to repair the pre-atomic divergence window and crash recovery.
func ReconcileCrawlProposalSubmitted(ctx context.Context, db *Handler, requestID, operationID string, submittedAt time.Time) (CrawlProposalTransitionResult, error) {
	return transitionCrawlProposal(ctx, db, requestID, CrawlProposalStateSubmitted, operationID, submittedAt, true)
}

// MarkCrawlProposalExpired applies pending -> expired. Submitted and denied
// proposals never return to expired or pending.
func MarkCrawlProposalExpired(ctx context.Context, db *Handler, requestID string) (CrawlProposalTransitionResult, error) {
	return transitionCrawlProposal(ctx, db, requestID, CrawlProposalStateExpired, "", time.Time{}, false)
}

// MarkCrawlProposalDenied applies pending -> denied.
func MarkCrawlProposalDenied(ctx context.Context, db *Handler, requestID string) (CrawlProposalTransitionResult, error) {
	return transitionCrawlProposal(ctx, db, requestID, CrawlProposalStateDenied, "", time.Time{}, false)
}

func transitionCrawlProposal(ctx context.Context, db *Handler, requestID, target, operationID string, submittedAt time.Time, allowExpiredToSubmitted bool) (CrawlProposalTransitionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil || *db == nil {
		return CrawlProposalTransitionResult{}, fmt.Errorf("database handler is nil")
	}
	if !supportsCrawlProposalStore(*db) {
		return CrawlProposalTransitionResult{}, ErrCrawlApprovalStoreUnsupported
	}
	txCtx, cancel := TransactionContextForHandler(ctx, db)
	defer cancel()
	tx, err := (*db).BeginTx(txCtx, nil)
	if err != nil {
		return CrawlProposalTransitionResult{}, crawlProposalContextError(txCtx, NormalizeTransactionError(txCtx, err))
	}
	defer func() { _ = tx.Rollback() }()
	row, found, err := lockCrawlProposalTx(txCtx, tx, *db, requestID)
	if err != nil {
		return CrawlProposalTransitionResult{}, err
	}
	if !found {
		return CrawlProposalTransitionResult{Outcome: CrawlProposalTransitionNotFound}, ErrCrawlProposalRowNotFound
	}
	result, err := transitionCrawlProposalLockedTx(txCtx, tx, *db, row, target, operationID, submittedAt, allowExpiredToSubmitted)
	if err != nil {
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return result, crawlProposalContextError(txCtx, NormalizeTransactionError(txCtx, err))
	}
	return result, nil
}

// transitionCrawlProposalLockedTx applies one strict compare-and-set
// transition on a locked row. The caller owns the transaction and commit.
func transitionCrawlProposalLockedTx(ctx context.Context, tx *sql.Tx, db Handler, row CrawlProposalRow, target, operationID string, submittedAt time.Time, allowExpiredToSubmitted bool) (CrawlProposalTransitionResult, error) {
	if target == CrawlProposalStateSubmitted && strings.TrimSpace(operationID) == "" {
		return CrawlProposalTransitionResult{Outcome: CrawlProposalTransitionConflict, Row: row}, ErrCrawlProposalStateConflict
	}
	if target == CrawlProposalStateSubmitted && submittedAt.IsZero() {
		submittedAt = time.Now().UTC()
	}
	if row.State == target {
		if target == CrawlProposalStateSubmitted && row.OperationID.Valid &&
			row.OperationID.String != "" && row.OperationID.String != strings.TrimSpace(operationID) {
			return CrawlProposalTransitionResult{Outcome: CrawlProposalTransitionConflict, Row: row}, ErrCrawlProposalStateConflict
		}
		return CrawlProposalTransitionResult{Outcome: CrawlProposalTransitionAlready, Row: row}, nil
	}
	allowed := false
	switch target {
	case CrawlProposalStateSubmitted:
		allowed = row.State == CrawlProposalStatePending ||
			(allowExpiredToSubmitted && row.State == CrawlProposalStateExpired)
	case CrawlProposalStateExpired, CrawlProposalStateDenied:
		allowed = row.State == CrawlProposalStatePending
	}
	if !allowed {
		return CrawlProposalTransitionResult{Outcome: CrawlProposalTransitionConflict, Row: row}, ErrCrawlProposalStateConflict
	}
	p := func(n int) string { return crawlProposalPlaceholder(db, n) }
	query := `UPDATE CrawlProposals
		SET state = ` + p(1) + `,
		    operation_id = ` + p(2) + `,
		    submitted_at = ` + p(3) + `,
		    last_updated_at = CURRENT_TIMESTAMP
		WHERE request_id = ` + p(4) + ` AND state = ` + p(5)
	var operation interface{}
	if target == CrawlProposalStateSubmitted {
		operation = strings.TrimSpace(operationID)
	}
	var submitted interface{}
	if target == CrawlProposalStateSubmitted {
		submitted = submittedAt.UTC().Truncate(time.Microsecond)
	}
	result, err := tx.ExecContext(ctx, query, target, operation, submitted, row.RequestID, row.State)
	if err != nil {
		return CrawlProposalTransitionResult{Outcome: CrawlProposalTransitionConflict, Row: row}, crawlProposalContextError(ctx, crawlApprovalStoreError(err))
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return CrawlProposalTransitionResult{Outcome: CrawlProposalTransitionConflict, Row: row}, err
	}
	if affected == 0 {
		current, found, readErr := lockCrawlProposalTx(ctx, tx, db, row.RequestID)
		if readErr != nil {
			return CrawlProposalTransitionResult{Outcome: CrawlProposalTransitionConflict, Row: row}, readErr
		}
		if !found {
			return CrawlProposalTransitionResult{Outcome: CrawlProposalTransitionNotFound, Row: row}, ErrCrawlProposalRowNotFound
		}
		if current.State == target {
			return CrawlProposalTransitionResult{Outcome: CrawlProposalTransitionAlready, Row: current}, nil
		}
		return CrawlProposalTransitionResult{Outcome: CrawlProposalTransitionConflict, Row: current}, ErrCrawlProposalStateConflict
	}
	row.State = target
	if target == CrawlProposalStateSubmitted {
		row.OperationID = sql.NullString{String: strings.TrimSpace(operationID), Valid: true}
		row.SubmittedAt = sql.NullTime{Time: submittedAt.UTC().Truncate(time.Microsecond), Valid: true}
	}
	return CrawlProposalTransitionResult{Outcome: CrawlProposalTransitionApplied, Row: row}, nil
}

func crawlProposalContextError(ctx context.Context, err error) error {
	if ctx != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
	}
	return err
}
