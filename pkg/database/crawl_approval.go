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

func crawlApprovalInsertSQL(db Handler) string {
	p := func(n int) string { return crawlApprovalPlaceholder(db, n) }
	conflict := "ON CONFLICT DO NOTHING"
	if normalizeInformationSeedDBMS(db.DBMS()) == DBPostgresStr {
		conflict = "ON CONFLICT (request_id, run_id, principal_user_id, source_id, operation_type) DO NOTHING"
	}
	return `INSERT INTO CrawlApprovals
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
}

func crawlApprovalInsertArgs(row CrawlApprovalRow) []any {
	return []any{
		row.ApprovalID, row.ProposalSHA256, row.RequestID, row.RunID, row.AgentID,
		row.Owner, row.PrincipalUserID, row.SourceID, row.OperationType,
		row.Issuer, row.IssuedByUserID, row.IssuedBy, row.IssuedByIssuer,
		row.ApprovedByUserID, row.ApprovedBy, row.ApprovedByIssuer,
		row.ApprovalSHA256, row.ProposalExpiresAt, row.ApprovedAt, row.ExpiresAt,
	}
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
	result, err := (*db).ExecContext(ctx, crawlApprovalInsertSQL(*db), crawlApprovalInsertArgs(row)...)
	if err != nil {
		return false, crawlApprovalStoreError(err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

// InsertCrawlApprovalAuthorized performs approval issuance in one transaction
// that locks the source row, rechecks verified ownership/provenance and the
// approver's current account state and administrative grant (when the approver
// is not the owner), then inserts the approval. This prevents an ownership or
// permission change from committing between the permission check and approval
// persistence.
func InsertCrawlApprovalAuthorized(ctx context.Context, db *Handler, row CrawlApprovalRow) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil || *db == nil {
		return false, fmt.Errorf("database handler is nil")
	}
	if !supportsCrawlApprovalStore(*db) {
		return false, ErrCrawlApprovalStoreUnsupported
	}
	txCtx, cancel := TransactionContextForHandler(ctx, db)
	defer cancel()
	tx, err := (*db).BeginTx(txCtx, nil)
	if err != nil {
		return false, NormalizeTransactionError(txCtx, err)
	}
	defer func() { _ = tx.Rollback() }()

	owner, err := lockSourceOwnerTx(txCtx, tx, *db, row.SourceID)
	if err != nil {
		return false, err
	}
	if !owner.Found {
		return false, ErrSourceMutationNotFound
	}
	if owner.Deleted {
		return false, ErrSourceMutationDeleted
	}
	if !owner.Verified || owner.UserID == 0 {
		return false, ErrSourceMutationUnverified
	}
	if row.ApprovedByUserID == 0 || row.IssuedByUserID == 0 {
		return false, ErrSourceMutationForbidden
	}
	if err := lockActiveUsersTx(txCtx, tx, *db, row.ApprovedByUserID, row.IssuedByUserID); err != nil {
		return false, err
	}
	if row.ApprovedByUserID != owner.UserID {
		if err := lockUserAdminRoleTx(txCtx, tx, *db, row.ApprovedByUserID); err != nil {
			return false, err
		}
	}

	result, err := tx.ExecContext(txCtx, crawlApprovalInsertSQL(*db), crawlApprovalInsertArgs(row)...)
	if err != nil {
		return false, crawlApprovalStoreError(err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, NormalizeTransactionError(txCtx, err)
	}
	return affected > 0, nil
}

// RevokeCrawlApprovalAuthorized performs an authorization-first,
// idempotent revocation. It locks the source and current account/grant rows,
// rechecks verified ownership or a current administrative grant, and only then
// applies the revocation. Repeating an authorized revocation returns success
// without changing the original revocation metadata.
func RevokeCrawlApprovalAuthorized(ctx context.Context, db *Handler, approvalID string, principalUserID uint64, admin bool, revokedBy, reason string, revokedAt time.Time) (bool, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil || *db == nil {
		return false, false, fmt.Errorf("database handler is nil")
	}
	if !supportsCrawlApprovalStore(*db) {
		return false, false, ErrCrawlApprovalStoreUnsupported
	}
	if strings.TrimSpace(approvalID) == "" || principalUserID == 0 {
		return false, false, ErrSourceMutationForbidden
	}
	if revokedAt.IsZero() {
		revokedAt = time.Now().UTC()
	}
	txCtx, cancel := TransactionContextForHandler(ctx, db)
	defer cancel()
	tx, err := (*db).BeginTx(txCtx, nil)
	if err != nil {
		return false, false, NormalizeTransactionError(txCtx, err)
	}
	defer func() { _ = tx.Rollback() }()

	p := func(n int) string { return crawlApprovalPlaceholder(*db, n) }
	var sourceID uint64
	if err := tx.QueryRowContext(txCtx, `SELECT source_id FROM CrawlApprovals WHERE approval_id = `+p(1), approvalID).Scan(&sourceID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, false, nil
		}
		return false, false, crawlApprovalStoreError(err)
	}

	// Authorize before the idempotent-revocation decision. This is the defect
	// this helper closes: an already-revoked row must not bypass source and
	// account authorization.
	if _, err := AuthorizeSourceMutationTx(txCtx, tx, *db, sourceID, principalUserID, admin); err != nil {
		return false, true, err
	}

	lockQuery := `SELECT revoked_at FROM CrawlApprovals WHERE approval_id = ` + p(1)
	if normalizeInformationSeedDBMS((*db).DBMS()) == DBPostgresStr {
		lockQuery += ` FOR UPDATE`
	}
	var alreadyRevoked sql.NullTime
	if err := tx.QueryRowContext(txCtx, lockQuery, approvalID).Scan(&alreadyRevoked); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, false, nil
		}
		return false, false, crawlApprovalStoreError(err)
	}
	if alreadyRevoked.Valid {
		if err := tx.Commit(); err != nil {
			return false, false, NormalizeTransactionError(txCtx, err)
		}
		return false, true, nil
	}

	update := `UPDATE CrawlApprovals
		SET revoked_at = ` + p(1) + `,
		    revoked_by_user_id = ` + p(2) + `,
		    revoked_by = ` + p(3) + `,
		    revocation_reason = ` + p(4) + `,
		    last_updated_at = CURRENT_TIMESTAMP
		WHERE approval_id = ` + p(5) + ` AND revoked_at IS NULL`
	result, err := tx.ExecContext(txCtx, update, revokedAt, principalUserID, strings.TrimSpace(revokedBy), strings.TrimSpace(reason), approvalID)
	if err != nil {
		return false, true, crawlApprovalStoreError(err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, true, err
	}
	if err := tx.Commit(); err != nil {
		return false, false, NormalizeTransactionError(txCtx, err)
	}
	return affected > 0, true, nil
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
