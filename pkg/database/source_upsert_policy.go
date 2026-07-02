// Copyright 2023 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
package database

import (
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
}

// SourceUpsertResult describes whether a policy-aware upsert created or reused a Source.
type SourceUpsertResult struct {
	SourceID uint64
	Created  bool
	Existing bool
}

// UpsertSourceWithPolicy creates a Source or returns an existing Source according to policy.
// Existing rows are never overwritten except for the optional config refresh controlled by
// UpdateExistingSourceConfig, which preserves the existing CreateSource behavior of ignoring
// empty configs and rows currently in processing status.
func UpsertSourceWithPolicy(db *Handler, source *Source, config cfg.SourceConfig, policy SourceUpsertPolicy) (SourceUpsertResult, error) {
	if db == nil || *db == nil {
		return SourceUpsertResult{}, fmt.Errorf("database handler is nil")
	}
	if source == nil {
		return SourceUpsertResult{}, fmt.Errorf("source is nil")
	}
	if !config.IsEmpty() {
		if err := validateSourceConfig(config); err != nil {
			return SourceUpsertResult{}, fmt.Errorf("invalid source configuration: %v", err)
		}
	}

	details, err := json.Marshal(config)
	if err != nil {
		return SourceUpsertResult{}, fmt.Errorf("failed to marshal source configuration: %v", err)
	}

	prepared := preparedSourceInsert{
		URL:        NormalizeSourceURL(source.URL),
		Name:       strings.TrimSpace(source.Name),
		Priority:   strings.TrimSpace(source.Priority),
		CategoryID: source.CategoryID,
		UsrID:      source.UsrID,
		Restricted: source.Restricted,
		Flags:      source.Flags,
		Config:     details,
		Disabled:   policy.Disabled,
		Status:     normalizedSourcePolicyStatus(policy.Status),
	}
	if prepared.URL == "" {
		return SourceUpsertResult{}, fmt.Errorf("source URL must be provided")
	}

	existingID, found, err := getSourceIDByURL(db, prepared.URL)
	if err != nil {
		return SourceUpsertResult{}, err
	}
	if found {
		if !policy.LinkExistingSources {
			return SourceUpsertResult{SourceID: existingID, Existing: true}, nil
		}
		if policy.UpdateExistingSourceConfig && sourceConfigJSONIsMeaningful(details) {
			if err := updateExistingSourceConfigForPolicy(db, existingID, details); err != nil {
				return SourceUpsertResult{}, err
			}
		}
		return SourceUpsertResult{SourceID: existingID, Existing: true}, nil
	}

	if !policy.CreateSources {
		return SourceUpsertResult{}, nil
	}

	createdID, err := insertSourceWithPolicy(db, prepared)
	if err != nil {
		// If another worker inserted the same URL between lookup and insert, honor
		// the existing-source policy instead of overwriting the row via an upsert.
		existingID, found, lookupErr := getSourceIDByURL(db, prepared.URL)
		if lookupErr == nil && found {
			if policy.LinkExistingSources && policy.UpdateExistingSourceConfig && sourceConfigJSONIsMeaningful(details) {
				if updateErr := updateExistingSourceConfigForPolicy(db, existingID, details); updateErr != nil {
					return SourceUpsertResult{}, updateErr
				}
			}
			return SourceUpsertResult{SourceID: existingID, Existing: true}, nil
		}
		return SourceUpsertResult{}, err
	}

	return SourceUpsertResult{SourceID: createdID, Created: true}, nil
}

func normalizedSourcePolicyStatus(status string) string {
	status = strings.TrimSpace(status)
	if status == "" {
		return "new"
	}
	return status
}

func sourceConfigJSONIsMeaningful(details []byte) bool {
	trimmed := strings.TrimSpace(string(details))
	return trimmed != "" && trimmed != "null" && trimmed != "{}"
}

func getSourceIDByURL(db *Handler, sourceURL string) (uint64, bool, error) {
	var sourceID uint64
	query := `SELECT source_id FROM Sources WHERE url = $1`
	if normalizeInformationSeedDBMS((*db).DBMS()) == DBMySQLStr {
		query = `SELECT source_id FROM Sources WHERE url = ?`
	}
	err := (*db).QueryRow(query, sourceURL).Scan(&sourceID)
	if err == nil {
		return sourceID, true, nil
	}
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	return 0, false, fmt.Errorf("failed to look up source by URL %q: %w", sourceURL, err)
}

func insertSourceWithPolicy(db *Handler, source preparedSourceInsert) (uint64, error) {
	switch normalizeInformationSeedDBMS((*db).DBMS()) {
	case DBPostgresStr:
		var sourceID uint64
		args := source.argsWithStatus()
		args[7] = string(source.Config)
		err := (*db).QueryRow(`
			INSERT INTO Sources
				(url, name, priority, category_id, usr_id, restricted, flags, config, disabled, status)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9, $10)
			RETURNING source_id`, args...).Scan(&sourceID)
		if err != nil {
			return 0, fmt.Errorf("failed to insert PostgreSQL source: %w", err)
		}
		return sourceID, nil
	case DBSQLiteStr:
		var sourceID uint64
		err := (*db).QueryRow(`
			INSERT INTO Sources
				(url, name, priority, category_id, usr_id, restricted, flags, config, disabled, status)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			RETURNING source_id`, source.argsWithStatus()...).Scan(&sourceID)
		if err != nil {
			return 0, fmt.Errorf("failed to insert SQLite source: %w", err)
		}
		return sourceID, nil
	case DBMySQLStr:
		result, err := (*db).Exec(`
			INSERT INTO Sources
				(url, name, priority, category_id, usr_id, restricted, flags, config, disabled, status)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, source.argsWithStatus()...)
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

func updateExistingSourceConfigForPolicy(db *Handler, sourceID uint64, details []byte) error {
	switch normalizeInformationSeedDBMS((*db).DBMS()) {
	case DBPostgresStr:
		_, err := (*db).Exec(`
			UPDATE Sources
			SET config = $1::jsonb, last_updated_at = NOW()
			WHERE source_id = $2
			  AND status <> 'processing'
			  AND $1 IS NOT NULL
			  AND $1::jsonb <> '{}'::jsonb
			  AND COALESCE(md5($1::jsonb::text), '') <> COALESCE(md5(config::text), '')`, string(details), sourceID)
		if err != nil {
			return fmt.Errorf("failed to update existing PostgreSQL source config: %w", err)
		}
	case DBSQLiteStr:
		_, err := (*db).Exec(`
			UPDATE Sources
			SET config = $1, last_updated_at = CURRENT_TIMESTAMP
			WHERE source_id = $2
			  AND status <> 'processing'
			  AND $1 IS NOT NULL
			  AND TRIM($1) <> ''
			  AND TRIM($1) <> '{}'
			  AND COALESCE($1, '') <> COALESCE(config, '')`, details, sourceID)
		if err != nil {
			return fmt.Errorf("failed to update existing SQLite source config: %w", err)
		}
	case DBMySQLStr:
		_, err := (*db).Exec(`
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
