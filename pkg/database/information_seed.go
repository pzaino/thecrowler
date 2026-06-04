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

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// InformationSeedDiscoveryMetadata contains provenance recorded on the relationship
// between a source and the information seed that discovered it. Pointer fields are
// intentionally used so callers can update only the metadata attributes they know
// without clearing attributes recorded by an earlier discovery pass.
type InformationSeedDiscoveryMetadata struct {
	DiscoveryProvider *string
	DiscoveryQuery    *string
	DiscoveryRank     *int
	CandidateScore    *float64
	CandidateReason   *string
	DiscoveryMetadata *json.RawMessage
}

// InformationSeed represents a row from the InformationSeed table.
type InformationSeed struct {
	ID              uint64
	CreatedAt       sql.NullTime
	LastUpdatedAt   sql.NullTime
	CategoryID      uint64
	UsrID           uint64
	InformationSeed string
	Status          string
	Priority        string
	Engine          string
	LastProcessedAt sql.NullTime
	LastError       sql.NullString
	LastErrorAt     sql.NullTime
	Disabled        bool
	Attempts        int
	Config          *json.RawMessage
}

// ClaimInformationSeeds atomically marks eligible InformationSeed rows as processing for engine.
//
// Eligible seeds are enabled rows whose status is new or pending, processing rows whose
// last_processed_at is older than processingTimeout, and error rows whose last_error_at is
// older than retryAfter. The implementation deliberately branches by DBMS because row-locking
// and UPDATE ... RETURNING support differ across supported databases.
func ClaimInformationSeeds(db *Handler, limit int, engine string, processingTimeout, retryAfter time.Duration) ([]InformationSeed, error) {
	if db == nil || *db == nil {
		return nil, fmt.Errorf("database handler is nil")
	}
	if limit <= 0 {
		return []InformationSeed{}, nil
	}
	engine = strings.TrimSpace(engine)
	if engine == "" {
		return nil, fmt.Errorf("engine is required to claim information seeds")
	}

	now := time.Now().UTC()
	processingBefore := now.Add(-processingTimeout)
	retryBefore := now.Add(-retryAfter)

	switch normalizeInformationSeedDBMS((*db).DBMS()) {
	case DBPostgresStr:
		return claimInformationSeedsPostgres(db, limit, engine, now, processingBefore, retryBefore)
	case DBMySQLStr:
		return claimInformationSeedsMySQL(db, limit, engine, now, processingBefore, retryBefore)
	case DBSQLiteStr:
		return claimInformationSeedsSQLite(db, limit, engine, now, processingBefore, retryBefore)
	default:
		return nil, fmt.Errorf("unsupported database type for information seed claims: %s", (*db).DBMS())
	}
}

func normalizeInformationSeedDBMS(dbms string) string {
	dbms = strings.ToLower(strings.TrimSpace(dbms))
	switch {
	case strings.Contains(dbms, "postgres"):
		return DBPostgresStr
	case strings.Contains(dbms, "mysql"):
		return DBMySQLStr
	case strings.Contains(dbms, "sqlite"):
		return DBSQLiteStr
	default:
		return dbms
	}
}

func claimInformationSeedsPostgres(db *Handler, limit int, engine string, claimedAt, processingBefore, retryBefore time.Time) ([]InformationSeed, error) {
	tx, err := (*db).Begin()
	if err != nil {
		return nil, err
	}
	committed := false
	defer rollbackIfUncommitted(tx, &committed)

	rows, err := tx.Query(`
		WITH selected AS (
			SELECT information_seed_id
			FROM InformationSeed
			WHERE COALESCE(disabled, FALSE) = FALSE
			  AND (
				LOWER(TRIM(status)) IN ('new', 'pending')
				OR (LOWER(TRIM(status)) = 'processing' AND (last_processed_at IS NULL OR last_processed_at < $1))
				OR (LOWER(TRIM(status)) = 'error' AND (last_error_at IS NULL OR last_error_at < $2))
			  )
			ORDER BY created_at ASC, information_seed_id ASC
			FOR UPDATE SKIP LOCKED
			LIMIT $3
		)
		UPDATE InformationSeed AS seed
		SET status = 'processing',
			engine = $4,
			last_processed_at = $5,
			attempts = COALESCE(seed.attempts, 0) + 1
		FROM selected
		WHERE seed.information_seed_id = selected.information_seed_id
		RETURNING seed.information_seed_id, seed.created_at, seed.last_updated_at, seed.category_id,
			seed.usr_id, seed.information_seed, seed.status, seed.priority, seed.engine,
			seed.last_processed_at, seed.last_error, seed.last_error_at, seed.disabled,
			seed.attempts, seed.config`, processingBefore, retryBefore, limit, engine, claimedAt)
	if err != nil {
		return nil, err
	}

	seeds, err := scanInformationSeedRows(rows)
	if err != nil {
		return nil, err
	}

	if err = tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	return seeds, nil
}

func claimInformationSeedsMySQL(db *Handler, limit int, engine string, claimedAt, processingBefore, retryBefore time.Time) ([]InformationSeed, error) {
	tx, err := (*db).Begin()
	if err != nil {
		return nil, err
	}
	committed := false
	defer rollbackIfUncommitted(tx, &committed)

	rows, err := tx.Query(`
		SELECT information_seed_id
		FROM InformationSeed
		WHERE COALESCE(disabled, FALSE) = FALSE
		  AND (
			LOWER(TRIM(status)) IN ('new', 'pending')
			OR (LOWER(TRIM(status)) = 'processing' AND (last_processed_at IS NULL OR last_processed_at < ?))
			OR (LOWER(TRIM(status)) = 'error' AND (last_error_at IS NULL OR last_error_at < ?))
		  )
		ORDER BY created_at ASC, information_seed_id ASC
		LIMIT ?
		FOR UPDATE SKIP LOCKED`, processingBefore, retryBefore, limit)
	if err != nil {
		return nil, err
	}
	ids, err := scanInformationSeedIDs(rows)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		committed = true
		return []InformationSeed{}, nil
	}

	placeholders := questionPlaceholders(len(ids))
	args := make([]interface{}, 0, len(ids)+3)
	args = append(args, engine, claimedAt)
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, processingBefore, retryBefore)

	_, err = tx.Exec(fmt.Sprintf(`
		UPDATE InformationSeed
		SET status = 'processing',
			engine = ?,
			last_processed_at = ?,
			attempts = COALESCE(attempts, 0) + 1
		WHERE information_seed_id IN (%s)
		  AND COALESCE(disabled, FALSE) = FALSE
		  AND (
			LOWER(TRIM(status)) IN ('new', 'pending')
			OR (LOWER(TRIM(status)) = 'processing' AND (last_processed_at IS NULL OR last_processed_at < ?))
			OR (LOWER(TRIM(status)) = 'error' AND (last_error_at IS NULL OR last_error_at < ?))
		  )`, placeholders), args...)
	if err != nil {
		return nil, err
	}

	seeds, err := selectInformationSeedsByIDsMySQL(tx, ids, engine)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	return seeds, nil
}

func claimInformationSeedsSQLite(db *Handler, limit int, engine string, claimedAt, processingBefore, retryBefore time.Time) ([]InformationSeed, error) {
	tx, err := (*db).Begin()
	if err != nil {
		return nil, err
	}
	committed := false
	defer rollbackIfUncommitted(tx, &committed)

	_, err = tx.Exec(`
		UPDATE InformationSeed
		SET status = 'processing',
			engine = ?,
			last_processed_at = ?,
			attempts = COALESCE(attempts, 0) + 1
		WHERE information_seed_id IN (
			SELECT information_seed_id
			FROM InformationSeed
			WHERE COALESCE(disabled, 0) = 0
			  AND (
				LOWER(TRIM(status)) IN ('new', 'pending')
				OR (LOWER(TRIM(status)) = 'processing' AND (last_processed_at IS NULL OR last_processed_at < ?))
				OR (LOWER(TRIM(status)) = 'error' AND (last_error_at IS NULL OR last_error_at < ?))
			  )
			ORDER BY created_at ASC, information_seed_id ASC
			LIMIT ?
		)`, engine, claimedAt, processingBefore, retryBefore, limit)
	if err != nil {
		return nil, err
	}

	rows, err := tx.Query(`
		SELECT information_seed_id, created_at, last_updated_at, category_id, usr_id,
			information_seed, status, priority, engine, last_processed_at, last_error,
			last_error_at, disabled, attempts, config
		FROM InformationSeed
		WHERE engine = ?
		  AND status = 'processing'
		  AND last_processed_at = ?
		ORDER BY created_at ASC, information_seed_id ASC`, engine, claimedAt)
	if err != nil {
		return nil, err
	}
	seeds, err := scanInformationSeedRows(rows)
	if err != nil {
		return nil, err
	}

	if err = tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	return seeds, nil
}

func selectInformationSeedsByIDsMySQL(tx *sql.Tx, ids []uint64, engine string) ([]InformationSeed, error) {
	placeholders := questionPlaceholders(len(ids))
	args := make([]interface{}, 0, len(ids)+1)
	args = append(args, engine)
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := tx.Query(fmt.Sprintf(`
		SELECT information_seed_id, created_at, last_updated_at, category_id, usr_id,
			information_seed, status, priority, engine, last_processed_at, last_error,
			last_error_at, disabled, attempts, config
		FROM InformationSeed
		WHERE engine = ?
		  AND status = 'processing'
		  AND information_seed_id IN (%s)
		ORDER BY created_at ASC, information_seed_id ASC`, placeholders), args...)
	if err != nil {
		return nil, err
	}
	return scanInformationSeedRows(rows)
}

func scanInformationSeedIDs(rows *sql.Rows) ([]uint64, error) {
	defer rows.Close()
	ids := []uint64{}
	for rows.Next() {
		var id uint64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func scanInformationSeedRows(rows *sql.Rows) ([]InformationSeed, error) {
	defer rows.Close()
	seeds := []InformationSeed{}
	for rows.Next() {
		var seed InformationSeed
		var config sql.NullString
		if err := rows.Scan(
			&seed.ID,
			&seed.CreatedAt,
			&seed.LastUpdatedAt,
			&seed.CategoryID,
			&seed.UsrID,
			&seed.InformationSeed,
			&seed.Status,
			&seed.Priority,
			&seed.Engine,
			&seed.LastProcessedAt,
			&seed.LastError,
			&seed.LastErrorAt,
			&seed.Disabled,
			&seed.Attempts,
			&config,
		); err != nil {
			return nil, err
		}
		if config.Valid {
			raw := json.RawMessage(config.String)
			seed.Config = &raw
		}
		seeds = append(seeds, seed)
	}
	return seeds, rows.Err()
}

func rollbackIfUncommitted(tx *sql.Tx, committed *bool) {
	if !*committed {
		_ = tx.Rollback()
	}
}

func questionPlaceholders(count int) string {
	parts := make([]string, count)
	for i := range parts {
		parts[i] = "?"
	}
	return strings.Join(parts, ",")
}

// LinkSourceToInformationSeed idempotently records that a source is associated
// with an information seed. Duplicate source/seed pairs are ignored and do not
// modify discovery provenance already stored on the relationship row.
func LinkSourceToInformationSeed(db *Handler, sourceID, informationSeedID uint64) error {
	if db == nil || *db == nil {
		return fmt.Errorf("database handler is nil")
	}
	if sourceID == 0 || informationSeedID == 0 {
		return fmt.Errorf("sourceID and informationSeedID must be provided")
	}

	switch normalizeInformationSeedDBMS((*db).DBMS()) {
	case DBMySQLStr:
		_, err := (*db).Exec(`
			INSERT IGNORE INTO SourceInformationSeedIndex (source_id, information_seed_id)
			VALUES (?, ?)`, sourceID, informationSeedID)
		if err != nil {
			return fmt.Errorf("failed to link source %d to information seed %d: %w", sourceID, informationSeedID, err)
		}
	case DBPostgresStr, DBSQLiteStr:
		_, err := (*db).Exec(`
			INSERT INTO SourceInformationSeedIndex (source_id, information_seed_id)
			VALUES ($1, $2)
			ON CONFLICT (source_id, information_seed_id) DO NOTHING`, sourceID, informationSeedID)
		if err != nil {
			return fmt.Errorf("failed to link source %d to information seed %d: %w", sourceID, informationSeedID, err)
		}
	default:
		return fmt.Errorf("unsupported database type for source/information-seed links: %s", (*db).DBMS())
	}

	return nil
}

// LinkSourceToInformationSeedWithDiscoveryMetadata inserts or updates the
// source/seed relationship and records per-discovery provenance on that exact
// relationship row. Nil metadata fields are left unchanged on duplicate links so
// partial updates cannot clear unrelated metadata attributes.
func LinkSourceToInformationSeedWithDiscoveryMetadata(db *Handler, sourceID, informationSeedID uint64, metadata InformationSeedDiscoveryMetadata) error {
	if db == nil || *db == nil {
		return fmt.Errorf("database handler is nil")
	}
	if sourceID == 0 || informationSeedID == 0 {
		return fmt.Errorf("sourceID and informationSeedID must be provided")
	}

	metadataJSON, err := discoveryMetadataString(metadata.DiscoveryMetadata)
	if err != nil {
		return err
	}

	args := []interface{}{
		sourceID,
		informationSeedID,
		nullableArg(metadata.DiscoveryProvider),
		nullableArg(metadata.DiscoveryQuery),
		nullableArg(metadata.DiscoveryRank),
		nullableArg(metadata.CandidateScore),
		nullableArg(metadata.CandidateReason),
		nullableArg(metadataJSON),
	}

	switch normalizeInformationSeedDBMS((*db).DBMS()) {
	case DBPostgresStr:
		_, err = (*db).Exec(`
			INSERT INTO SourceInformationSeedIndex (
				source_id, information_seed_id, discovery_provider, discovery_query,
				discovery_rank, candidate_score, candidate_reason, discovery_metadata
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb)
			ON CONFLICT (source_id, information_seed_id) DO UPDATE SET
				discovery_provider = COALESCE(EXCLUDED.discovery_provider, SourceInformationSeedIndex.discovery_provider),
				discovery_query = COALESCE(EXCLUDED.discovery_query, SourceInformationSeedIndex.discovery_query),
				discovery_rank = COALESCE(EXCLUDED.discovery_rank, SourceInformationSeedIndex.discovery_rank),
				candidate_score = COALESCE(EXCLUDED.candidate_score, SourceInformationSeedIndex.candidate_score),
				candidate_reason = COALESCE(EXCLUDED.candidate_reason, SourceInformationSeedIndex.candidate_reason),
				discovery_metadata = CASE
					WHEN EXCLUDED.discovery_metadata IS NULL THEN SourceInformationSeedIndex.discovery_metadata
					ELSE COALESCE(SourceInformationSeedIndex.discovery_metadata, '{}'::jsonb) || EXCLUDED.discovery_metadata
				END`, args...)
	case DBMySQLStr:
		_, err = (*db).Exec(`
			INSERT INTO SourceInformationSeedIndex (
				source_id, information_seed_id, discovery_provider, discovery_query,
				discovery_rank, candidate_score, candidate_reason, discovery_metadata
			)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE
				discovery_provider = COALESCE(VALUES(discovery_provider), discovery_provider),
				discovery_query = COALESCE(VALUES(discovery_query), discovery_query),
				discovery_rank = COALESCE(VALUES(discovery_rank), discovery_rank),
				candidate_score = COALESCE(VALUES(candidate_score), candidate_score),
				candidate_reason = COALESCE(VALUES(candidate_reason), candidate_reason),
				discovery_metadata = CASE
					WHEN VALUES(discovery_metadata) IS NULL THEN discovery_metadata
					ELSE JSON_MERGE_PATCH(COALESCE(discovery_metadata, JSON_OBJECT()), VALUES(discovery_metadata))
				END`, args...)
	case DBSQLiteStr:
		_, err = (*db).Exec(`
			INSERT INTO SourceInformationSeedIndex (
				source_id, information_seed_id, discovery_provider, discovery_query,
				discovery_rank, candidate_score, candidate_reason, discovery_metadata
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, json($8))
			ON CONFLICT (source_id, information_seed_id) DO UPDATE SET
				discovery_provider = COALESCE(excluded.discovery_provider, SourceInformationSeedIndex.discovery_provider),
				discovery_query = COALESCE(excluded.discovery_query, SourceInformationSeedIndex.discovery_query),
				discovery_rank = COALESCE(excluded.discovery_rank, SourceInformationSeedIndex.discovery_rank),
				candidate_score = COALESCE(excluded.candidate_score, SourceInformationSeedIndex.candidate_score),
				candidate_reason = COALESCE(excluded.candidate_reason, SourceInformationSeedIndex.candidate_reason),
				discovery_metadata = CASE
					WHEN excluded.discovery_metadata IS NULL THEN SourceInformationSeedIndex.discovery_metadata
					ELSE json_patch(COALESCE(SourceInformationSeedIndex.discovery_metadata, '{}'), excluded.discovery_metadata)
				END`, args...)
	default:
		return fmt.Errorf("unsupported database type for source/information-seed links: %s", (*db).DBMS())
	}
	if err != nil {
		return fmt.Errorf("failed to link source %d to information seed %d with discovery metadata: %w", sourceID, informationSeedID, err)
	}
	return nil
}

func nullableArg[T any](value *T) interface{} {
	if value == nil {
		return nil
	}
	return *value
}

func discoveryMetadataString(metadata *json.RawMessage) (*string, error) {
	if metadata == nil {
		return nil, nil
	}
	if len(*metadata) == 0 {
		return nil, fmt.Errorf("discovery metadata must be valid JSON when provided")
	}
	if !json.Valid(*metadata) {
		return nil, fmt.Errorf("discovery metadata must be valid JSON")
	}
	metadataString := string(*metadata)
	return &metadataString, nil
}
