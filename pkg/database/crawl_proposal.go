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

// MarkCrawlProposalSubmitted records a successful scheduling submission
// without changing its immutable proposal identity or expiry.
func MarkCrawlProposalSubmitted(ctx context.Context, db *Handler, requestID, operationID string, submittedAt time.Time) error {
	return updateCrawlProposalState(ctx, db, requestID, CrawlProposalStateSubmitted, operationID, submittedAt)
}

// MarkCrawlProposalExpired records expiry of a pending proposal.
func MarkCrawlProposalExpired(ctx context.Context, db *Handler, requestID string) error {
	return updateCrawlProposalState(ctx, db, requestID, CrawlProposalStateExpired, "", time.Time{})
}

func updateCrawlProposalState(ctx context.Context, db *Handler, requestID, state, operationID string, submittedAt time.Time) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil || *db == nil {
		return fmt.Errorf("database handler is nil")
	}
	if !supportsCrawlProposalStore(*db) {
		return ErrCrawlApprovalStoreUnsupported
	}
	p := func(n int) string { return crawlProposalPlaceholder(*db, n) }
	query := `UPDATE CrawlProposals
		SET state = ` + p(1) + `,
		    operation_id = ` + p(2) + `,
		    submitted_at = ` + p(3) + `,
		    last_updated_at = CURRENT_TIMESTAMP
		WHERE request_id = ` + p(4) + ` AND state = 'pending'`
	var submitted interface{}
	if !submittedAt.IsZero() {
		submitted = submittedAt.UTC().Truncate(time.Microsecond)
	}
	var op interface{}
	if operationID != "" {
		op = operationID
	}
	_, err := (*db).ExecContext(ctx, query, state, op, submitted, strings.TrimSpace(requestID))
	if err != nil {
		return crawlApprovalStoreError(err)
	}
	return nil
}
