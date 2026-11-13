// Copyright 2023 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

// SourceUpsertPolicy controls how discovery flows create or reuse Sources.
type SourceUpsertPolicy struct {
	CreateSources              bool
	LinkExistingSources        bool
	UpdateExistingSourceConfig bool
	Disabled                   bool
	Status                     string
	// AuthoritativeOwnership records verified SourceOwnership provenance for a
	// newly created source. Callers may set it only when the seed owner was
	// derived from an authenticated principal.
	AuthoritativeOwnership bool
}

// SourceUpsertResult describes whether a policy-aware upsert created or reused a Source.
type SourceUpsertResult struct {
	SourceID uint64
	Created  bool
	Existing bool
}

// UpsertSourceWithPolicy creates a Source or returns an existing Source according to policy.
// Existing rows are never overwritten except for the optional config refresh controlled by
// UpdateExistingSourceConfig. Config refreshes require verified ownership by the seed actor
// and never touch a source that is processing or soft-deleted.
func UpsertSourceWithPolicy(db *Handler, source *Source, config cfg.SourceConfig, policy SourceUpsertPolicy) (SourceUpsertResult, error) {
	return UpsertSourceWithPolicyContext(context.Background(), db, source, config, policy)
}

// UpsertSourceWithPolicyContext is the context-aware form of UpsertSourceWithPolicy.
func UpsertSourceWithPolicyContext(ctx context.Context, db *Handler, source *Source, config cfg.SourceConfig, policy SourceUpsertPolicy) (SourceUpsertResult, error) {
	if db == nil || *db == nil {
		return SourceUpsertResult{}, fmt.Errorf("database handler is nil")
	}
	if source == nil {
		return SourceUpsertResult{}, fmt.Errorf("source is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	details, hasConfig, err := sourceConfigJSON(config)
	if err != nil {
		return SourceUpsertResult{}, err
	}

	prepared := preparedSourceInsert{
		URL:         NormalizeSourceURL(source.URL),
		Name:        strings.TrimSpace(source.Name),
		Priority:    strings.TrimSpace(source.Priority),
		SubPriority: source.SubPriority,
		CategoryID:  source.CategoryID,
		UsrID:       source.UsrID,
		Restricted:  source.Restricted,
		Flags:       source.Flags,
		Config:      details,
		Disabled:    policy.Disabled,
		Status:      normalizedSourcePolicyStatus(policy.Status),
	}
	if prepared.URL == "" {
		return SourceUpsertResult{}, fmt.Errorf("source URL must be provided")
	}
	prepared.UID = CalculateSourceUID(prepared.Name, prepared.URL)
	source.UID = prepared.UID

	owner, err := GetSourceOwnerByURL(ctx, db, prepared.URL, true)
	if err != nil {
		return SourceUpsertResult{}, err
	}
	if owner.Found {
		return reuseExistingSourceForPolicy(ctx, db, owner, prepared.UsrID, details, hasConfig, policy)
	}

	if !policy.CreateSources {
		return SourceUpsertResult{}, nil
	}

	createdID, err := insertSourceWithPolicyContext(ctx, db, prepared, policy)
	if err != nil {
		// If another worker inserted the same URL between lookup and insert, apply
		// the existing-source policy again. The lookup includes deleted rows so a
		// soft-deleted URL is never silently resurrected.
		owner, lookupErr := GetSourceOwnerByURL(ctx, db, prepared.URL, true)
		if lookupErr == nil && owner.Found {
			return reuseExistingSourceForPolicy(ctx, db, owner, prepared.UsrID, details, hasConfig, policy)
		}
		return SourceUpsertResult{}, err
	}

	return SourceUpsertResult{SourceID: createdID, Created: true}, nil
}

func reuseExistingSourceForPolicy(ctx context.Context, db *Handler, owner SourceOwner, actorUserID uint64, details []byte, hasConfig bool, policy SourceUpsertPolicy) (SourceUpsertResult, error) {
	if owner.Deleted {
		return SourceUpsertResult{}, fmt.Errorf("%w: source %d is deleted", ErrSourceMutationDeleted, owner.SourceID)
	}
	if !policy.LinkExistingSources {
		return SourceUpsertResult{SourceID: owner.SourceID, Existing: true}, nil
	}
	if err := authorizePolicySourceOwner(owner, actorUserID); err != nil {
		return SourceUpsertResult{}, err
	}
	if policy.UpdateExistingSourceConfig && hasConfig && canRefreshPolicySourceConfig(owner, actorUserID) {
		if err := updateExistingSourceConfigForPolicyContext(ctx, db, owner.SourceID, details); err != nil {
			return SourceUpsertResult{}, err
		}
	}
	return SourceUpsertResult{SourceID: owner.SourceID, Existing: true}, nil
}

func authorizePolicySourceOwner(owner SourceOwner, actorUserID uint64) error {
	if actorUserID == 0 || owner.UserID == 0 {
		// Legacy/system discovery may link unowned rows, but config refreshes are
		// still disabled below because they require verified ownership.
		return nil
	}
	if owner.UserID != actorUserID {
		return fmt.Errorf("%w: source %d belongs to user %d", ErrSourceMutationForbidden, owner.SourceID, owner.UserID)
	}
	return nil
}

func canRefreshPolicySourceConfig(owner SourceOwner, actorUserID uint64) bool {
	return actorUserID != 0 && owner.UserID == actorUserID && owner.Verified
}

func normalizedSourcePolicyStatus(status string) string {
	status = strings.TrimSpace(status)
	if status == "" {
		return "new"
	}
	return status
}

func sourceConfigJSON(config cfg.SourceConfig) ([]byte, bool, error) {
	if config.IsEmpty() {
		return nil, false, nil
	}
	if err := validateSourceConfig(config); err != nil {
		return nil, false, fmt.Errorf("invalid source configuration: %v", err)
	}
	details, err := json.Marshal(config)
	if err != nil {
		return nil, false, fmt.Errorf("failed to marshal source configuration: %v", err)
	}
	if !sourceConfigJSONIsMeaningful(details) {
		return nil, false, nil
	}
	return details, true, nil
}

func sourceConfigJSONIsMeaningful(details []byte) bool {
	trimmed := strings.TrimSpace(string(details))
	return trimmed != "" && trimmed != "null" && trimmed != "{}"
}

func insertSourceWithPolicyContext(ctx context.Context, db *Handler, source preparedSourceInsert, policy SourceUpsertPolicy) (sourceID uint64, retErr error) {
	txCtx, cancelTx := TransactionContextForHandler(ctx, db)
	defer cancelTx()
	tx, err := (*db).BeginTx(txCtx, nil)
	if err != nil {
		return 0, NormalizeTransactionError(txCtx, err)
	}
	committed := false
	defer rollbackIfUncommitted(tx, &committed)
	defer func() { retErr = NormalizeTransactionError(txCtx, retErr) }()

	sourceID, err = insertSourceWithPolicyTx(txCtx, db, tx, source)
	if err != nil {
		_ = tx.Rollback()
		committed = true
		return 0, err
	}
	if source.UsrID != 0 && policy.AuthoritativeOwnership {
		if err := recordPolicySourceOwnershipTx(txCtx, db, tx, sourceID, source.UsrID); err != nil {
			_ = tx.Rollback()
			committed = true
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	committed = true
	return sourceID, nil
}

func insertSourceWithPolicyTx(ctx context.Context, db *Handler, tx *sql.Tx, source preparedSourceInsert) (uint64, error) {
	switch normalizeInformationSeedDBMS((*db).DBMS()) {
	case DBPostgresStr:
		var sourceID uint64
		err := tx.QueryRowContext(ctx, `
			INSERT INTO Sources
				(url, name, priority, sub_priority, category_id, usr_id, restricted, flags, config, disabled, source_uid, status)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb, $10, $11, $12)
			RETURNING source_id`, source.argsWithStatus()...).Scan(&sourceID)
		if err != nil {
			return 0, fmt.Errorf("failed to insert PostgreSQL source: %w", err)
		}
		return sourceID, nil
	case DBSQLiteStr:
		var sourceID uint64
		err := tx.QueryRowContext(ctx, `
			INSERT INTO Sources
				(url, name, priority, sub_priority, category_id, usr_id, restricted, flags, config, disabled, source_uid, status)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			RETURNING source_id`, source.argsWithStatus()...).Scan(&sourceID)
		if err != nil {
			return 0, fmt.Errorf("failed to insert SQLite source: %w", err)
		}
		return sourceID, nil
	case DBMySQLStr:
		result, err := tx.ExecContext(ctx, `
			INSERT INTO Sources
				(url, name, priority, sub_priority, category_id, usr_id, restricted, flags, config, disabled, source_uid, status)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, source.argsWithStatus()...)
		if err != nil {
			return 0, fmt.Errorf("failed to insert MySQL source: %w", err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			return 0, fmt.Errorf("failed to retrieve MySQL source ID: %w", err)
		}
		if id < 0 {
			return 0, fmt.Errorf("failed to retrieve MySQL source ID: negative ID %d", id)
		}
		return uint64(id), nil
	default:
		return 0, fmt.Errorf("unsupported database type for source creation: %s", (*db).DBMS())
	}
}

func recordPolicySourceOwnershipTx(ctx context.Context, db *Handler, tx *sql.Tx, sourceID, userID uint64) error {
	dbms := normalizeInformationSeedDBMS((*db).DBMS())
	const verifiedBy = "information_seed_discovery"
	const reason = "source created by information seed discovery"
	switch dbms {
	case DBPostgresStr, DBSQLiteStr:
		_, err := tx.ExecContext(ctx, `
			INSERT INTO SourceOwnership
				(source_id, user_id, verified_at, verified_by_user_id, verified_by, verification_method, verification_reason)
			VALUES ($1, $2, CURRENT_TIMESTAMP, $3, $4, 'information_seed_discovery', $5)
			ON CONFLICT (source_id) DO UPDATE SET
				user_id = EXCLUDED.user_id,
				verified_at = EXCLUDED.verified_at,
				verified_by_user_id = EXCLUDED.verified_by_user_id,
				verified_by = EXCLUDED.verified_by,
				verification_method = EXCLUDED.verification_method,
				verification_reason = EXCLUDED.verification_reason,
				last_updated_at = CURRENT_TIMESTAMP`,
			sourceID, userID, userID, verifiedBy, reason)
		if err != nil {
			return fmt.Errorf("record information seed source ownership: %w", err)
		}
	case DBMySQLStr:
		_, err := tx.ExecContext(ctx, `
			INSERT INTO SourceOwnership
				(source_id, user_id, verified_at, verified_by_user_id, verified_by, verification_method, verification_reason)
			VALUES (?, ?, CURRENT_TIMESTAMP, ?, ?, 'information_seed_discovery', ?)
			ON DUPLICATE KEY UPDATE
				user_id = VALUES(user_id),
				verified_at = VALUES(verified_at),
				verified_by_user_id = VALUES(verified_by_user_id),
				verified_by = VALUES(verified_by),
				verification_method = VALUES(verification_method),
				verification_reason = VALUES(verification_reason),
				last_updated_at = CURRENT_TIMESTAMP`,
			sourceID, userID, userID, verifiedBy, reason)
		if err != nil {
			return fmt.Errorf("record information seed source ownership: %w", err)
		}
	default:
		return fmt.Errorf("unsupported database type for source ownership recording: %s", (*db).DBMS())
	}
	return nil
}

func updateExistingSourceConfigForPolicyContext(ctx context.Context, db *Handler, sourceID uint64, details []byte) error {
	switch normalizeInformationSeedDBMS((*db).DBMS()) {
	case DBPostgresStr:
		_, err := (*db).ExecContext(ctx, `
			UPDATE Sources
			SET config = $1::jsonb, last_updated_at = NOW()
			WHERE source_id = $2
			  AND status <> 'processing'
			  AND deleted_at IS NULL
			  AND $1::jsonb IS NOT NULL
			  AND $1::jsonb <> '{}'::jsonb
			  AND COALESCE(md5($1::jsonb::text), '') <> COALESCE(md5(config::text), '')`, string(details), sourceID)
		if err != nil {
			return fmt.Errorf("failed to update existing PostgreSQL source config: %w", err)
		}
	case DBSQLiteStr:
		_, err := (*db).ExecContext(ctx, `
			UPDATE Sources
			SET config = $1, last_updated_at = CURRENT_TIMESTAMP
			WHERE source_id = $2
			  AND status <> 'processing'
			  AND deleted_at IS NULL
			  AND $1 IS NOT NULL
			  AND TRIM($1) <> ''
			  AND TRIM($1) <> '{}'
			  AND COALESCE($1, '') <> COALESCE(config, '')`, details, sourceID)
		if err != nil {
			return fmt.Errorf("failed to update existing SQLite source config: %w", err)
		}
	case DBMySQLStr:
		_, err := (*db).ExecContext(ctx, `
			UPDATE Sources
			SET config = ?, last_updated_at = CURRENT_TIMESTAMP
			WHERE source_id = ?
			  AND status <> 'processing'
			  AND ? IS NOT NULL
			  AND JSON_LENGTH(?) > 0
			  AND COALESCE(MD5(CAST(? AS CHAR)), '') <> COALESCE(MD5(CAST(config AS CHAR)), '')`, details, sourceID, details, details, details)
		if err != nil {
			return fmt.Errorf("failed to update existing MySQL source config: %w", err)
		}
	default:
		return fmt.Errorf("unsupported database type for source config update: %s", (*db).DBMS())
	}
	return nil
}
