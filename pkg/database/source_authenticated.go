package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

var (
	// ErrAuthenticatedSourceUnsupported means the backend cannot enforce the
	// atomic authenticated source-mutation contract.
	ErrAuthenticatedSourceUnsupported = errors.New("authenticated source mutation is unsupported by this database backend")
	// ErrAuthenticatedSourceConflict means another principal owns the URL.
	ErrAuthenticatedSourceConflict = errors.New("source URL belongs to another user")
)

func supportsAuthenticatedSourceMutation(db Handler) bool {
	switch normalizeInformationSeedDBMS(db.DBMS()) {
	case DBPostgresStr, DBSQLiteStr:
		return true
	default:
		return false
	}
}

// UpsertAuthenticatedSourceContext is the ownership-aware source-add path used
// when API authentication is enabled. It performs the insert-or-authorize step
// and the source mutation in one transaction, so a concurrent creator cannot
// turn an authorized "new URL" decision into an unauthorized update of another
// user's source.
//
// New sources are owned by principalUserID and receive verified provenance.
// An existing source is updated only when the caller is its verified owner or
// has an active administrative grant. Ownership is never transferred here.
func UpsertAuthenticatedSourceContext(
	ctx context.Context,
	db *Handler,
	source *Source,
	config cfg.SourceConfig,
	principalUserID uint64,
	principalUsername string,
	admin bool,
) (uint64, bool, error) {
	if db == nil || *db == nil {
		return 0, false, fmt.Errorf("database handler is nil")
	}
	if source == nil {
		return 0, false, fmt.Errorf("source is nil")
	}
	if principalUserID == 0 {
		return 0, false, ErrSourceMutationForbidden
	}
	if !config.IsEmpty() {
		if err := validateSourceConfig(config); err != nil {
			return 0, false, fmt.Errorf("invalid source configuration: %v", err)
		}
	}
	configJSON, err := json.Marshal(config)
	if err != nil {
		return 0, false, fmt.Errorf("failed to marshal source configuration: %v", err)
	}
	prepared := preparedSourceInsert{
		URL:         NormalizeSourceURL(source.URL),
		Name:        strings.TrimSpace(source.Name),
		Priority:    strings.TrimSpace(source.Priority),
		SubPriority: source.SubPriority,
		CategoryID:  source.CategoryID,
		UsrID:       principalUserID,
		Restricted:  source.Restricted,
		Flags:       source.Flags,
		Config:      configJSON,
		Disabled:    source.Disabled,
	}
	if prepared.URL == "" {
		return 0, false, fmt.Errorf("source URL is required")
	}
	prepared.UID = CalculateSourceUID(prepared.Name, prepared.URL)
	source.UID = prepared.UID

	if !supportsAuthenticatedSourceMutation(*db) {
		return 0, false, ErrAuthenticatedSourceUnsupported
	}

	txCtx, cancel := TransactionContextForHandler(ctx, db)
	defer cancel()
	tx, err := (*db).BeginTx(txCtx, nil)
	if err != nil {
		return 0, false, NormalizeTransactionError(txCtx, err)
	}
	defer func() { _ = tx.Rollback() }()

	inserted, err := insertAuthenticatedSourceRow(txCtx, tx, *db, prepared)
	if err != nil {
		return 0, false, err
	}
	if inserted {
		// The insert may have completed before a concurrent disable. Lock the
		// account before recording provenance; a disable which committed first
		// aborts this transaction, while a later disable waits for this commit.
		if err := lockActiveUserTx(txCtx, tx, *db, principalUserID); err != nil {
			return 0, false, err
		}
		sourceID, err := sourceIDByURLTx(txCtx, tx, *db, prepared.URL)
		if err != nil {
			return 0, false, err
		}
		if err := insertAuthenticatedSourceOwnership(txCtx, tx, *db, sourceID, principalUserID, principalUsername); err != nil {
			return 0, false, err
		}
		if err := tx.Commit(); err != nil {
			return 0, false, NormalizeTransactionError(txCtx, err)
		}
		return sourceID, true, nil
	}

	owner, err := lockSourceOwnerByURLTx(txCtx, tx, *db, prepared.URL, true)
	if err != nil {
		return 0, false, err
	}
	if !owner.Found {
		// The conflicting insert disappeared between the insert attempt and the
		// lock. Fail closed rather than retrying indefinitely.
		return 0, false, ErrAuthenticatedSourceConflict
	}
	if owner.Deleted {
		return 0, false, ErrSourceMutationDeleted
	}
	// The source row is locked now; recheck account state and current grants
	// under that lock before deciding whether this upsert may update it.
	if owner.UserID == principalUserID && owner.Verified {
		if err := lockActiveUserTx(txCtx, tx, *db, principalUserID); err != nil {
			return 0, false, err
		}
	} else if admin {
		if err := lockUserAuthorizationTx(txCtx, tx, *db, principalUserID, true); err != nil {
			return 0, false, err
		}
	} else {
		if err := lockActiveUserTx(txCtx, tx, *db, principalUserID); err != nil {
			return 0, false, err
		}
		if owner.UserID == 0 || !owner.Verified {
			return 0, false, ErrSourceMutationUnverified
		}
		if owner.UserID != principalUserID {
			return 0, false, ErrAuthenticatedSourceConflict
		}
	}

	if err := updateAuthenticatedSourceRow(txCtx, tx, *db, prepared, owner.SourceID, owner.UserID); err != nil {
		return 0, false, err
	}
	if err := tx.Commit(); err != nil {
		return 0, false, NormalizeTransactionError(txCtx, err)
	}
	return owner.SourceID, false, nil
}

func insertAuthenticatedSourceRow(ctx context.Context, tx *sql.Tx, db Handler, source preparedSourceInsert) (bool, error) {
	p := func(n int) string { return sourceOwnershipPlaceholder(db, n) }
	query := `INSERT INTO Sources
		(url, name, priority, sub_priority, category_id, usr_id, restricted, flags, config, disabled, source_uid, status)
		VALUES (` + p(1) + `, ` + p(2) + `, ` + p(3) + `, ` + p(4) + `, ` + p(5) + `, ` + p(6) + `, ` + p(7) + `, ` + p(8) + `, ` + p(9) + `, ` + p(10) + `, ` + p(11) + `, 'new')
		ON CONFLICT (url) DO NOTHING`
	result, err := tx.ExecContext(ctx, query, source.args()...)
	if err != nil {
		return false, fmt.Errorf("insert authenticated source: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

func sourceIDByURLTx(ctx context.Context, tx *sql.Tx, db Handler, sourceURL string) (uint64, error) {
	query := `SELECT source_id FROM Sources WHERE url = ` + sourceOwnershipPlaceholder(db, 1)
	var sourceID uint64
	if err := tx.QueryRowContext(ctx, query, sourceURL).Scan(&sourceID); err != nil {
		return 0, fmt.Errorf("read inserted source ID: %w", err)
	}
	return sourceID, nil
}

func insertAuthenticatedSourceOwnership(ctx context.Context, tx *sql.Tx, db Handler, sourceID, userID uint64, username string) error {
	p := func(n int) string { return sourceOwnershipPlaceholder(db, n) }
	query := `INSERT INTO SourceOwnership
		(source_id, user_id, verified_at, verified_by_user_id, verified_by, verification_method, verification_reason)
		VALUES (` + p(1) + `, ` + p(2) + `, CURRENT_TIMESTAMP, ` + p(3) + `, ` + p(4) + `, 'authenticated_creation', 'authenticated source creation')
		ON CONFLICT (source_id) DO UPDATE SET
			user_id = EXCLUDED.user_id,
			verified_at = EXCLUDED.verified_at,
			verified_by_user_id = EXCLUDED.verified_by_user_id,
			verified_by = EXCLUDED.verified_by,
			verification_method = EXCLUDED.verification_method,
			verification_reason = EXCLUDED.verification_reason,
			last_updated_at = CURRENT_TIMESTAMP`
	if _, err := tx.ExecContext(ctx, query, sourceID, userID, userID, strings.TrimSpace(username)); err != nil {
		return fmt.Errorf("record authenticated source ownership: %w", err)
	}
	return nil
}

func updateAuthenticatedSourceRow(ctx context.Context, tx *sql.Tx, db Handler, source preparedSourceInsert, sourceID, ownerID uint64) error {
	p := func(n int) string { return sourceOwnershipPlaceholder(db, n) }
	var query string
	if normalizeInformationSeedDBMS(db.DBMS()) == DBPostgresStr {
		query = `UPDATE Sources SET
			name = CASE WHEN status <> 'processing' AND BTRIM(` + p(1) + `) <> '' THEN ` + p(1) + ` ELSE name END,
			source_uid = CASE WHEN status <> 'processing' AND BTRIM(` + p(2) + `) <> '' THEN ` + p(3) + ` ELSE source_uid END,
			priority = CASE WHEN status <> 'processing' AND BTRIM(` + p(4) + `) <> '' THEN ` + p(4) + ` ELSE priority END,
			sub_priority = CASE WHEN status <> 'processing' THEN ` + p(5) + ` ELSE sub_priority END,
			category_id = CASE WHEN status <> 'processing' THEN ` + p(6) + ` ELSE category_id END,
			restricted = CASE WHEN status <> 'processing' THEN ` + p(7) + ` ELSE restricted END,
			flags = CASE WHEN status <> 'processing' THEN ` + p(8) + ` ELSE flags END,
			config = CASE
				WHEN status <> 'processing'
				 AND ` + p(9) + `::jsonb IS NOT NULL
				 AND ` + p(9) + `::jsonb <> '{}'::jsonb
				 AND md5((` + p(9) + `::jsonb)::text) <> md5(COALESCE(config, '{}'::jsonb)::text)
				THEN ` + p(9) + `::jsonb ELSE config END,
			disabled = CASE WHEN status <> 'processing' THEN ` + p(10) + ` ELSE disabled END,
			status = CASE WHEN status = 'processing' THEN status ELSE 'new' END,
			last_updated_at = NOW()
			WHERE source_id = ` + p(11) + ` AND usr_id = ` + p(12) + ` AND deleted_at IS NULL`
	} else {
		query = `UPDATE Sources SET
			name = CASE WHEN status <> 'processing' AND TRIM(` + p(1) + `) <> '' THEN ` + p(1) + ` ELSE name END,
			source_uid = CASE WHEN status <> 'processing' AND TRIM(` + p(2) + `) <> '' THEN ` + p(3) + ` ELSE source_uid END,
			priority = CASE WHEN status <> 'processing' AND TRIM(` + p(4) + `) <> '' THEN ` + p(4) + ` ELSE priority END,
			sub_priority = CASE WHEN status <> 'processing' THEN ` + p(5) + ` ELSE sub_priority END,
			category_id = CASE WHEN status <> 'processing' THEN ` + p(6) + ` ELSE category_id END,
			restricted = CASE WHEN status <> 'processing' THEN ` + p(7) + ` ELSE restricted END,
			flags = CASE WHEN status <> 'processing' THEN ` + p(8) + ` ELSE flags END,
			config = CASE
				WHEN status <> 'processing'
				 AND ` + p(9) + ` IS NOT NULL
				 AND TRIM(` + p(9) + `) <> ''
				 AND TRIM(` + p(9) + `) <> '{}'
				 AND COALESCE(` + p(9) + `, '') <> COALESCE(config, '')
				THEN ` + p(9) + ` ELSE config END,
			disabled = CASE WHEN status <> 'processing' THEN ` + p(10) + ` ELSE disabled END,
			status = CASE WHEN status = 'processing' THEN status ELSE 'new' END,
			last_updated_at = CURRENT_TIMESTAMP
			WHERE source_id = ` + p(11) + ` AND usr_id = ` + p(12) + ` AND deleted_at IS NULL`
	}
	result, err := tx.ExecContext(ctx, query,
		source.Name, source.Name, source.UID, source.Priority, source.SubPriority,
		source.CategoryID, source.Restricted, source.Flags, source.Config,
		source.Disabled, sourceID, ownerID,
	)
	if err != nil {
		return fmt.Errorf("update authenticated source: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrSourceMutationForbidden
	}
	return nil
}
