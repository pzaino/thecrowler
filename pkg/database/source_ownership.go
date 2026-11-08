package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SourceOwnershipTable is the v1.17 provenance table that distinguishes
// authoritative ownership from a legacy Sources.usr_id value.
const SourceOwnershipTable = "SourceOwnership"

var (
	// ErrSourceMutationNotFound means the source row does not exist.
	ErrSourceMutationNotFound = errors.New("source not found")
	// ErrSourceMutationDeleted means the source is soft-deleted.
	ErrSourceMutationDeleted = errors.New("source is deleted")
	// ErrSourceMutationForbidden means the principal may not mutate the source.
	ErrSourceMutationForbidden = errors.New("source mutation is forbidden")
	// ErrSourceMutationUnverified means ownership has no verified provenance.
	ErrSourceMutationUnverified = errors.New("source ownership is not verified")
	// ErrSourceOwnershipAdminRequired means the caller lacks an active
	// administrative role for the requested ownership verification.
	ErrSourceOwnershipAdminRequired = errors.New("source ownership verification requires an administrator")
)

// SourceOwner is the persisted owner information for one source.
//
// Sources.usr_id remains the stored owner identifier. Verified is true only
// when SourceOwnership contains a row whose user_id matches Sources.usr_id.
// A legacy nonzero usr_id without provenance is therefore not authoritative.
type SourceOwner struct {
	SourceID           uint64
	UserID             uint64
	Found              bool
	Deleted            bool
	Verified           bool
	VerificationMethod string
	VerifiedAt         time.Time
}

// SourceOwnershipVerification describes an explicit administrative ownership
// verification or transfer. It is never populated from model arguments.
type SourceOwnershipVerification struct {
	SourceID           uint64
	UserID             uint64
	VerifiedByUserID   uint64
	VerifiedBy         string
	VerificationMethod string
	Reason             string
	VerifiedAt         time.Time
}

func sourceOwnershipPlaceholder(db Handler, position int) string {
	if normalizeInformationSeedDBMS(db.DBMS()) == DBMySQLStr {
		return "?"
	}
	// SQLite accepts numbered parameters, which lets a query reference the
	// same value more than once without duplicating arguments.
	return "$" + fmt.Sprintf("%d", position)
}

// GetSourceOwner looks up the persisted owner of a live source, including
// whether that ownership has authoritative provenance.
func GetSourceOwner(ctx context.Context, db *Handler, sourceID uint64) (SourceOwner, error) {
	return getSourceOwner(ctx, db, sourceID, false)
}

// GetVerifiedSourceOwner returns a live source owner only when SourceOwnership
// verifies the stored usr_id. Callers that require authoritative ownership
// should use this function instead of GetSourceOwner.
func GetVerifiedSourceOwner(ctx context.Context, db *Handler, sourceID uint64) (SourceOwner, error) {
	owner, err := GetSourceOwner(ctx, db, sourceID)
	if err != nil {
		return SourceOwner{}, err
	}
	if !owner.Found || !owner.Verified {
		return SourceOwner{SourceID: sourceID}, nil
	}
	return owner, nil
}

func getSourceOwner(ctx context.Context, db *Handler, sourceID uint64, includeDeleted bool) (SourceOwner, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil || *db == nil {
		return SourceOwner{}, fmt.Errorf("database handler is nil")
	}
	if sourceID == 0 {
		return SourceOwner{}, fmt.Errorf("source ID is required")
	}

	dbms := normalizeInformationSeedDBMS((*db).DBMS())
	placeholder := sourceOwnershipPlaceholder(*db, 1)
	deletedFilter := ""
	selectDeleted := ""
	if dbms != DBMySQLStr {
		if !includeDeleted {
			deletedFilter = " AND s.deleted_at IS NULL"
		}
		selectDeleted = ", (s.deleted_at IS NOT NULL)"
	}
	query := `SELECT s.source_id, s.usr_id` + selectDeleted + ` FROM Sources s
		WHERE s.source_id = ` + placeholder + deletedFilter

	var owner SourceOwner
	var deleted bool
	scanArgs := []any{&owner.SourceID, &owner.UserID}
	if selectDeleted != "" {
		scanArgs = append(scanArgs, &deleted)
	}
	err := (*db).QueryRowContext(ctx, query, sourceID).Scan(scanArgs...)
	if errors.Is(err, sql.ErrNoRows) {
		return SourceOwner{SourceID: sourceID}, nil
	}
	if err != nil {
		return SourceOwner{}, fmt.Errorf("look up source owner: %w", err)
	}
	owner.Found = true
	owner.Deleted = deleted
	if err := populateSourceOwnership(ctx, *db, &owner); err != nil {
		return SourceOwner{}, err
	}
	return owner, nil
}

// GetSourceOwnerByURL resolves a source by canonical URL. When includeDeleted
// is true, deleted rows are returned with Deleted=true so callers can prevent
// a URL upsert from crossing an existing ownership boundary.
func GetSourceOwnerByURL(ctx context.Context, db *Handler, sourceURL string, includeDeleted bool) (SourceOwner, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil || *db == nil {
		return SourceOwner{}, fmt.Errorf("database handler is nil")
	}
	sourceURL = NormalizeSourceURL(sourceURL)
	if sourceURL == "" {
		return SourceOwner{}, fmt.Errorf("source URL is required")
	}
	dbms := normalizeInformationSeedDBMS((*db).DBMS())
	placeholder := sourceOwnershipPlaceholder(*db, 1)
	deletedFilter := ""
	selectDeleted := ""
	if dbms != DBMySQLStr {
		if !includeDeleted {
			deletedFilter = " AND s.deleted_at IS NULL"
		}
		selectDeleted = ", (s.deleted_at IS NOT NULL)"
	}
	query := `SELECT s.source_id, s.usr_id` + selectDeleted + ` FROM Sources s
		WHERE s.url = ` + placeholder + deletedFilter + ` LIMIT 1`
	var owner SourceOwner
	var deleted bool
	scanArgs := []any{&owner.SourceID, &owner.UserID}
	if selectDeleted != "" {
		scanArgs = append(scanArgs, &deleted)
	}
	err := (*db).QueryRowContext(ctx, query, sourceURL).Scan(scanArgs...)
	if errors.Is(err, sql.ErrNoRows) {
		return SourceOwner{}, nil
	}
	if err != nil {
		return SourceOwner{}, fmt.Errorf("look up source owner by URL: %w", err)
	}
	owner.Found = true
	owner.Deleted = deleted
	if err := populateSourceOwnership(ctx, *db, &owner); err != nil {
		return SourceOwner{}, err
	}
	return owner, nil
}

// lockSourceOwnerTx locks the source row and reads its ownership provenance.
// On PostgreSQL the source row is locked before the provenance row so callers
// sharing an approval/scheduling flow keep a consistent source-first order.
func lockSourceOwnerTx(ctx context.Context, tx *sql.Tx, db Handler, sourceID uint64) (SourceOwner, error) {
	if tx == nil {
		return SourceOwner{}, fmt.Errorf("transaction is nil")
	}
	placeholder := sourceOwnershipPlaceholder(db, 1)
	deletedExpr := "s.deleted_at IS NOT NULL"
	if normalizeInformationSeedDBMS(db.DBMS()) == DBMySQLStr {
		deletedExpr = "FALSE"
	}
	query := `SELECT s.source_id, s.usr_id, ` + deletedExpr + ` FROM Sources s
		WHERE s.source_id = ` + placeholder
	if normalizeInformationSeedDBMS(db.DBMS()) == DBPostgresStr {
		query += ` FOR UPDATE`
	}
	var owner SourceOwner
	if err := tx.QueryRowContext(ctx, query, sourceID).Scan(&owner.SourceID, &owner.UserID, &owner.Deleted); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SourceOwner{SourceID: sourceID}, nil
		}
		return SourceOwner{}, fmt.Errorf("lock source owner: %w", err)
	}
	owner.Found = true
	if err := populateSourceOwnershipTx(ctx, tx, db, &owner); err != nil {
		return SourceOwner{}, err
	}
	return owner, nil
}

func lockSourceOwnerByURLTx(ctx context.Context, tx *sql.Tx, db Handler, sourceURL string, includeDeleted bool) (SourceOwner, error) {
	if tx == nil {
		return SourceOwner{}, fmt.Errorf("transaction is nil")
	}
	placeholder := sourceOwnershipPlaceholder(db, 1)
	deletedExpr := "s.deleted_at IS NOT NULL"
	if normalizeInformationSeedDBMS(db.DBMS()) == DBMySQLStr {
		deletedExpr = "FALSE"
	}
	deletedFilter := ""
	if !includeDeleted && normalizeInformationSeedDBMS(db.DBMS()) != DBMySQLStr {
		deletedFilter = " AND s.deleted_at IS NULL"
	}
	query := `SELECT s.source_id, s.usr_id, ` + deletedExpr + ` FROM Sources s
		WHERE s.url = ` + placeholder + deletedFilter + ` LIMIT 1`
	if normalizeInformationSeedDBMS(db.DBMS()) == DBPostgresStr {
		query += ` FOR UPDATE`
	}
	var owner SourceOwner
	if err := tx.QueryRowContext(ctx, query, NormalizeSourceURL(sourceURL)).Scan(&owner.SourceID, &owner.UserID, &owner.Deleted); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SourceOwner{}, nil
		}
		return SourceOwner{}, fmt.Errorf("lock source owner by URL: %w", err)
	}
	owner.Found = true
	if err := populateSourceOwnershipTx(ctx, tx, db, &owner); err != nil {
		return SourceOwner{}, err
	}
	return owner, nil
}

func populateSourceOwnership(ctx context.Context, db Handler, owner *SourceOwner) error {
	if owner == nil || !owner.Found || owner.UserID == 0 {
		return nil
	}
	placeholder := sourceOwnershipPlaceholder(db, 1)
	query := `SELECT user_id, verification_method, verified_at FROM SourceOwnership WHERE source_id = ` + placeholder
	var userID uint64
	var method string
	var verifiedAt time.Time
	err := db.QueryRowContext(ctx, query, owner.SourceID).Scan(&userID, &method, &verifiedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("look up source ownership provenance: %w", err)
	}
	if userID == owner.UserID {
		owner.Verified = true
		owner.VerificationMethod = method
		owner.VerifiedAt = verifiedAt
	}
	return nil
}

func populateSourceOwnershipTx(ctx context.Context, tx *sql.Tx, db Handler, owner *SourceOwner) error {
	if owner == nil || !owner.Found || owner.UserID == 0 {
		return nil
	}
	placeholder := sourceOwnershipPlaceholder(db, 1)
	query := `SELECT user_id, verification_method, verified_at FROM SourceOwnership WHERE source_id = ` + placeholder
	var userID uint64
	var method string
	var verifiedAt time.Time
	err := tx.QueryRowContext(ctx, query, owner.SourceID).Scan(&userID, &method, &verifiedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("look up source ownership provenance: %w", err)
	}
	if userID == owner.UserID {
		owner.Verified = true
		owner.VerificationMethod = method
		owner.VerifiedAt = verifiedAt
	}
	return nil
}

// AuthorizeSourceMutationTx rechecks ownership and, for administrators, the
// current administrative grant inside the mutation transaction. Ordinary users
// must match a verified SourceOwnership row. A zero or legacy owner fails
// closed for ordinary users. Administrators may manage unverified sources but
// cannot change ownership through this path.
func AuthorizeSourceMutationTx(ctx context.Context, tx *sql.Tx, db Handler, sourceID, principalUserID uint64, admin bool) (SourceOwner, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if tx == nil {
		return SourceOwner{}, fmt.Errorf("transaction is nil")
	}
	if principalUserID == 0 {
		return SourceOwner{}, ErrSourceMutationForbidden
	}
	if !supportsAuthenticatedSourceMutation(db) {
		return SourceOwner{}, ErrAuthenticatedSourceUnsupported
	}
	owner, err := lockSourceOwnerTx(ctx, tx, db, sourceID)
	if err != nil {
		return SourceOwner{}, err
	}
	if !owner.Found {
		return SourceOwner{}, ErrSourceMutationNotFound
	}
	if owner.Deleted {
		return SourceOwner{}, ErrSourceMutationDeleted
	}
	if !admin {
		active, err := activeUserTx(ctx, tx, db, principalUserID)
		if err != nil {
			return owner, err
		}
		if !active {
			return owner, ErrSourceMutationForbidden
		}
		if owner.UserID == 0 || !owner.Verified {
			return owner, ErrSourceMutationUnverified
		}
		if owner.UserID != principalUserID {
			return owner, ErrSourceMutationForbidden
		}
		return owner, nil
	}
	active, err := activeUserHasAnyRoleTx(ctx, tx, db, principalUserID, []string{"admin", "superadmin"})
	if err != nil {
		return owner, err
	}
	if !active {
		return owner, ErrSourceMutationForbidden
	}
	return owner, nil
}

// AuthorizeSourceMutationByURLTx is the URL-addressed equivalent used by source
// removal, where the caller supplies a URL rather than an ID.
func AuthorizeSourceMutationByURLTx(ctx context.Context, tx *sql.Tx, db Handler, sourceURL string, principalUserID uint64, admin bool) (SourceOwner, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if tx == nil {
		return SourceOwner{}, fmt.Errorf("transaction is nil")
	}
	if principalUserID == 0 {
		return SourceOwner{}, ErrSourceMutationForbidden
	}
	if !supportsAuthenticatedSourceMutation(db) {
		return SourceOwner{}, ErrAuthenticatedSourceUnsupported
	}
	owner, err := lockSourceOwnerByURLTx(ctx, tx, db, sourceURL, false)
	if err != nil {
		return SourceOwner{}, err
	}
	if !owner.Found {
		return SourceOwner{}, ErrSourceMutationNotFound
	}
	if owner.Deleted {
		return SourceOwner{}, ErrSourceMutationDeleted
	}
	if !admin {
		active, err := activeUserTx(ctx, tx, db, principalUserID)
		if err != nil {
			return owner, err
		}
		if !active {
			return owner, ErrSourceMutationForbidden
		}
		if owner.UserID == 0 || !owner.Verified {
			return owner, ErrSourceMutationUnverified
		}
		if owner.UserID != principalUserID {
			return owner, ErrSourceMutationForbidden
		}
		return owner, nil
	}
	active, err := activeUserHasAnyRoleTx(ctx, tx, db, principalUserID, []string{"admin", "superadmin"})
	if err != nil {
		return owner, err
	}
	if !active {
		return owner, ErrSourceMutationForbidden
	}
	return owner, nil
}

// VerifySourceOwnershipContext performs an explicit administrative ownership
// verification or transfer. It locks the source row, updates Sources.usr_id and
// upserts the provenance record in one transaction. It never runs implicitly
// from source creation, add, update or model arguments.
func VerifySourceOwnershipContext(ctx context.Context, db *Handler, verification SourceOwnershipVerification) (SourceOwner, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil || *db == nil {
		return SourceOwner{}, fmt.Errorf("database handler is nil")
	}
	if verification.SourceID == 0 || verification.UserID == 0 || verification.VerifiedByUserID == 0 {
		return SourceOwner{}, fmt.Errorf("source, user and verifier IDs are required")
	}
	if !supportsAuthenticatedSourceMutation(*db) {
		return SourceOwner{}, ErrAuthenticatedSourceUnsupported
	}
	if verification.VerifiedAt.IsZero() {
		verification.VerifiedAt = time.Now().UTC()
	}
	if strings.TrimSpace(verification.VerificationMethod) == "" {
		verification.VerificationMethod = "admin_verification"
	}
	txCtx, cancel := TransactionContextForHandler(ctx, db)
	defer cancel()
	tx, err := (*db).BeginTx(txCtx, nil)
	if err != nil {
		return SourceOwner{}, NormalizeTransactionError(txCtx, err)
	}
	defer func() { _ = tx.Rollback() }()

	owner, err := lockSourceOwnerTx(txCtx, tx, *db, verification.SourceID)
	if err != nil {
		return SourceOwner{}, err
	}
	if !owner.Found {
		return SourceOwner{}, ErrSourceMutationNotFound
	}
	if owner.Deleted {
		return SourceOwner{}, ErrSourceMutationDeleted
	}
	active, err := activeUserHasAnyRoleTx(txCtx, tx, *db, verification.VerifiedByUserID, []string{"admin", "superadmin"})
	if err != nil {
		return SourceOwner{}, err
	}
	if !active {
		return SourceOwner{}, ErrSourceOwnershipAdminRequired
	}
	userActive, err := activeUserTx(txCtx, tx, *db, verification.UserID)
	if err != nil {
		return SourceOwner{}, err
	}
	if !userActive {
		return SourceOwner{}, ErrSourceMutationForbidden
	}

	updateOwner := `UPDATE Sources SET usr_id = ` + sourceOwnershipPlaceholder(*db, 1) + `, last_updated_at = CURRENT_TIMESTAMP WHERE source_id = ` + sourceOwnershipPlaceholder(*db, 2)
	if _, err := tx.ExecContext(txCtx, updateOwner, verification.UserID, verification.SourceID); err != nil {
		return SourceOwner{}, fmt.Errorf("update verified source owner: %w", err)
	}
	upsert := `INSERT INTO SourceOwnership
		(source_id, user_id, verified_at, verified_by_user_id, verified_by, verification_method, verification_reason)
		VALUES (` + sourceOwnershipPlaceholder(*db, 1) + `, ` + sourceOwnershipPlaceholder(*db, 2) + `, ` + sourceOwnershipPlaceholder(*db, 3) + `, ` + sourceOwnershipPlaceholder(*db, 4) + `, ` + sourceOwnershipPlaceholder(*db, 5) + `, ` + sourceOwnershipPlaceholder(*db, 6) + `, ` + sourceOwnershipPlaceholder(*db, 7) + `)
		ON CONFLICT (source_id) DO UPDATE SET
			user_id = EXCLUDED.user_id,
			verified_at = EXCLUDED.verified_at,
			verified_by_user_id = EXCLUDED.verified_by_user_id,
			verified_by = EXCLUDED.verified_by,
			verification_method = EXCLUDED.verification_method,
			verification_reason = EXCLUDED.verification_reason,
			last_updated_at = CURRENT_TIMESTAMP`
	if normalizeInformationSeedDBMS((*db).DBMS()) == DBMySQLStr {
		upsert = `INSERT INTO SourceOwnership
			(source_id, user_id, verified_at, verified_by_user_id, verified_by, verification_method, verification_reason)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE
				user_id = VALUES(user_id),
				verified_at = VALUES(verified_at),
				verified_by_user_id = VALUES(verified_by_user_id),
				verified_by = VALUES(verified_by),
				verification_method = VALUES(verification_method),
				verification_reason = VALUES(verification_reason),
				last_updated_at = CURRENT_TIMESTAMP`
	}
	if _, err := tx.ExecContext(txCtx, upsert,
		verification.SourceID, verification.UserID, verification.VerifiedAt.UTC().Truncate(time.Microsecond),
		verification.VerifiedByUserID, strings.TrimSpace(verification.VerifiedBy),
		strings.TrimSpace(verification.VerificationMethod), strings.TrimSpace(verification.Reason)); err != nil {
		return SourceOwner{}, fmt.Errorf("verify source ownership: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return SourceOwner{}, NormalizeTransactionError(txCtx, err)
	}
	return SourceOwner{
		SourceID: verification.SourceID, UserID: verification.UserID, Found: true,
		Verified: true, VerificationMethod: verification.VerificationMethod,
		VerifiedAt: verification.VerifiedAt.UTC().Truncate(time.Microsecond),
	}, nil
}

func activeUserTx(ctx context.Context, tx *sql.Tx, db Handler, userID uint64) (bool, error) {
	if tx == nil {
		return false, fmt.Errorf("transaction is nil")
	}
	placeholder := sourceOwnershipPlaceholder(db, 1)
	deletedExpr := "deleted_at IS NULL"
	if normalizeInformationSeedDBMS(db.DBMS()) == DBMySQLStr {
		deletedExpr = "TRUE"
	}
	query := `SELECT COUNT(*) FROM Users WHERE user_id = ` + placeholder + ` AND disabled = ` + falseLiteral(db) + ` AND ` + deletedExpr
	var count int
	if err := tx.QueryRowContext(ctx, query, userID).Scan(&count); err != nil {
		return false, fmt.Errorf("check user state: %w", err)
	}
	return count > 0, nil
}

func activeUserHasAnyRoleTx(ctx context.Context, tx *sql.Tx, db Handler, userID uint64, roles []string) (bool, error) {
	active, err := activeUserTx(ctx, tx, db, userID)
	if err != nil || !active {
		return false, err
	}
	if len(roles) == 0 {
		return false, nil
	}
	placeholders := make([]string, 0, len(roles)+1)
	placeholders = append(placeholders, sourceOwnershipPlaceholder(db, 1))
	args := []interface{}{userID}
	for i, role := range roles {
		placeholders = append(placeholders, sourceOwnershipPlaceholder(db, i+2))
		args = append(args, strings.ToLower(strings.TrimSpace(role)))
	}
	query := `SELECT COUNT(*) FROM UserRoles ur
		JOIN AuthRoles r ON r.role_id = ur.role_id
		WHERE ur.user_id = ` + placeholders[0] + `
		  AND LOWER(r.name) IN (` + strings.Join(placeholders[1:], ",") + `)`
	var count int
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return false, fmt.Errorf("check administrative role: %w", err)
	}
	return count > 0, nil
}

func falseLiteral(db Handler) string {
	if normalizeInformationSeedDBMS(db.DBMS()) == DBSQLiteStr {
		return "0"
	}
	return "FALSE"
}
