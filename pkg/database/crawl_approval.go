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

var (
	// ErrCrawlApprovalStoreUnsupported means the backend has no durable
	// approval store.
	ErrCrawlApprovalStoreUnsupported = errors.New("durable crawl approvals are unsupported by this database backend")
	// ErrCrawlApprovalSchemaUnavailable means the CrawlApprovals table has not
	// been migrated into this database.
	ErrCrawlApprovalSchemaUnavailable = errors.New("crawl approval schema is unavailable")
)

// CrawlApprovalRow is one authoritative durable operator approval.
type CrawlApprovalRow struct {
	ApprovalID string

	ProposalSHA256    string
	RequestID         string
	RunID             string
	AgentID           string
	Owner             string
	PrincipalUserID   uint64
	SourceID          uint64
	OperationType     string
	Issuer            string
	IssuedByUserID    uint64
	IssuedBy          string
	IssuedByIssuer    string
	ApprovedByUserID  uint64
	ApprovedBy        string
	ApprovedByIssuer  string
	ApprovalSHA256    string
	ProposalExpiresAt time.Time
	ApprovedAt        time.Time
	ExpiresAt         time.Time

	RevokedAt        sql.NullTime
	RevokedByUserID  sql.NullInt64
	RevokedBy        sql.NullString
	RevocationReason sql.NullString

	CreatedAt     time.Time
	LastUpdatedAt time.Time
}

func crawlApprovalPlaceholder(db Handler, position int) string {
	if normalizeInformationSeedDBMS(db.DBMS()) == DBPostgresStr {
		return "$" + strconv.Itoa(position)
	}
	return "?"
}

func supportsCrawlApprovalStore(db Handler) bool {
	switch normalizeInformationSeedDBMS(db.DBMS()) {
	case DBPostgresStr, DBSQLiteStr:
		return true
	default:
		return false
	}
}

func crawlApprovalColumns() string {
	return `approval_id, proposal_sha256, request_id, run_id, agent_id, owner,
		principal_user_id, source_id, operation_type, issuer,
		issued_by_user_id, issued_by, issued_by_issuer,
		approved_by_user_id, approved_by, approved_by_issuer,
		approval_sha256, proposal_expires_at, approved_at, expires_at,
		revoked_at, revoked_by_user_id, revoked_by, revocation_reason,
		created_at, last_updated_at`
}

func scanCrawlApprovalRow(scanner interface {
	Scan(dest ...any) error
}) (CrawlApprovalRow, error) {
	var row CrawlApprovalRow
	err := scanner.Scan(
		&row.ApprovalID, &row.ProposalSHA256, &row.RequestID, &row.RunID,
		&row.AgentID, &row.Owner, &row.PrincipalUserID, &row.SourceID,
		&row.OperationType, &row.Issuer, &row.IssuedByUserID, &row.IssuedBy,
		&row.IssuedByIssuer, &row.ApprovedByUserID, &row.ApprovedBy,
		&row.ApprovedByIssuer, &row.ApprovalSHA256, &row.ProposalExpiresAt,
		&row.ApprovedAt, &row.ExpiresAt, &row.RevokedAt, &row.RevokedByUserID,
		&row.RevokedBy, &row.RevocationReason, &row.CreatedAt, &row.LastUpdatedAt,
	)
	return row, err
}

// InsertCrawlApproval inserts one approval unless the logical approval key
// already exists. It returns true when this call created the row.
func InsertCrawlApproval(ctx context.Context, db *Handler, row CrawlApprovalRow) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil || *db == nil {
		return false, fmt.Errorf("database handler is nil")
	}
	if !supportsCrawlApprovalStore(*db) {
		return false, ErrCrawlApprovalStoreUnsupported
	}
	p := func(n int) string { return crawlApprovalPlaceholder(*db, n) }
	conflict := "ON CONFLICT DO NOTHING"
	if normalizeInformationSeedDBMS((*db).DBMS()) == DBPostgresStr {
		conflict = "ON CONFLICT (request_id, run_id, principal_user_id, source_id, operation_type) DO NOTHING"
	}
	query := `INSERT INTO CrawlApprovals
		(approval_id, proposal_sha256, request_id, run_id, agent_id, owner,
		 principal_user_id, source_id, operation_type, issuer,
		 issued_by_user_id, issued_by, issued_by_issuer,
		 approved_by_user_id, approved_by, approved_by_issuer,
		 approval_sha256, proposal_expires_at, approved_at, expires_at)
		VALUES (` + p(1) + `, ` + p(2) + `, ` + p(3) + `, ` + p(4) + `, ` + p(5) + `, ` + p(6) + `,
		 ` + p(7) + `, ` + p(8) + `, ` + p(9) + `, ` + p(10) + `,
		 ` + p(11) + `, ` + p(12) + `, ` + p(13) + `, ` + p(14) + `, ` + p(15) + `, ` + p(16) + `,
		 ` + p(17) + `, ` + p(18) + `, ` + p(19) + `, ` + p(20) + `)
		` + conflict
	result, err := (*db).ExecContext(ctx, query,
		row.ApprovalID, row.ProposalSHA256, row.RequestID, row.RunID, row.AgentID,
		row.Owner, row.PrincipalUserID, row.SourceID, row.OperationType,
		row.Issuer, row.IssuedByUserID, row.IssuedBy, row.IssuedByIssuer,
		row.ApprovedByUserID, row.ApprovedBy, row.ApprovedByIssuer,
		row.ApprovalSHA256, row.ProposalExpiresAt, row.ApprovedAt, row.ExpiresAt,
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

// GetCrawlApprovalByID returns one approval by its stable approval ID.
func GetCrawlApprovalByID(ctx context.Context, db *Handler, approvalID string) (CrawlApprovalRow, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil || *db == nil {
		return CrawlApprovalRow{}, false, fmt.Errorf("database handler is nil")
	}
	if !supportsCrawlApprovalStore(*db) {
		return CrawlApprovalRow{}, false, ErrCrawlApprovalStoreUnsupported
	}
	query := `SELECT ` + crawlApprovalColumns() + ` FROM CrawlApprovals
		WHERE approval_id = ` + crawlApprovalPlaceholder(*db, 1)
	row, err := scanCrawlApprovalRow((*db).QueryRowContext(ctx, query, approvalID))
	if errors.Is(err, sql.ErrNoRows) {
		return CrawlApprovalRow{}, false, nil
	}
	if err != nil {
		return CrawlApprovalRow{}, false, crawlApprovalStoreError(err)
	}
	return row, true, nil
}

// GetCrawlApprovalByRequest returns the approval for one logical request.
func GetCrawlApprovalByRequest(ctx context.Context, db *Handler, runID, requestID string, principalUserID, sourceID uint64, operationType string) (CrawlApprovalRow, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil || *db == nil {
		return CrawlApprovalRow{}, false, fmt.Errorf("database handler is nil")
	}
	if !supportsCrawlApprovalStore(*db) {
		return CrawlApprovalRow{}, false, ErrCrawlApprovalStoreUnsupported
	}
	p := func(n int) string { return crawlApprovalPlaceholder(*db, n) }
	query := `SELECT ` + crawlApprovalColumns() + ` FROM CrawlApprovals
		WHERE run_id = ` + p(1) + `
		  AND request_id = ` + p(2) + `
		  AND principal_user_id = ` + p(3) + `
		  AND source_id = ` + p(4) + `
		  AND operation_type = ` + p(5)
	row, err := scanCrawlApprovalRow((*db).QueryRowContext(ctx, query, runID, requestID, principalUserID, sourceID, operationType))
	if errors.Is(err, sql.ErrNoRows) {
		return CrawlApprovalRow{}, false, nil
	}
	if err != nil {
		return CrawlApprovalRow{}, false, crawlApprovalStoreError(err)
	}
	return row, true, nil
}

// RevokeCrawlApproval persists revocation without deleting history. It returns
// revoked=true only when this call transitioned the record to revoked.
func RevokeCrawlApproval(ctx context.Context, db *Handler, approvalID string, revokedByUserID uint64, revokedBy, reason string, revokedAt time.Time) (bool, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil || *db == nil {
		return false, false, fmt.Errorf("database handler is nil")
	}
	if !supportsCrawlApprovalStore(*db) {
		return false, false, ErrCrawlApprovalStoreUnsupported
	}
	if revokedAt.IsZero() {
		revokedAt = time.Now().UTC()
	}
	p := func(n int) string { return crawlApprovalPlaceholder(*db, n) }
	query := `UPDATE CrawlApprovals
		SET revoked_at = ` + p(1) + `,
		    revoked_by_user_id = ` + p(2) + `,
		    revoked_by = ` + p(3) + `,
		    revocation_reason = ` + p(4) + `,
		    last_updated_at = CURRENT_TIMESTAMP
		WHERE approval_id = ` + p(5) + ` AND revoked_at IS NULL`
	result, err := (*db).ExecContext(ctx, query, revokedAt, revokedByUserID, revokedBy, reason, approvalID)
	if err != nil {
		return false, false, crawlApprovalStoreError(err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, false, err
	}
	if affected > 0 {
		return true, true, nil
	}
	var exists bool
	existsQuery := `SELECT EXISTS(SELECT 1 FROM CrawlApprovals WHERE approval_id = ` + p(1) + `)`
	if err := (*db).QueryRowContext(ctx, existsQuery, approvalID).Scan(&exists); err != nil {
		return false, false, crawlApprovalStoreError(err)
	}
	return false, exists, nil
}

func crawlApprovalStoreError(err error) error {
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "no such table") ||
		strings.Contains(message, "does not exist") ||
		strings.Contains(message, "undefined table") {
		return fmt.Errorf("%w: %v", ErrCrawlApprovalSchemaUnavailable, err)
	}
	return err
}
