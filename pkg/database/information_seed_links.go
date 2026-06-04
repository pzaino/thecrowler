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
)

// LinkSourcesToInformationSeed idempotently records source/information-seed
// relationships. Duplicate source/seed pairs are ignored and treated as success.
func LinkSourcesToInformationSeed(db *Handler, links []SourceSeedLink) error {
	if db == nil || *db == nil {
		return fmt.Errorf("database handler is nil")
	}
	if len(links) == 0 {
		return nil
	}

	tx, err := (*db).Begin()
	if err != nil {
		return fmt.Errorf("failed to start source/information-seed link transaction: %w", err)
	}
	committed := false
	defer rollbackIfUncommitted(tx, &committed)

	var query string
	switch normalizeInformationSeedDBMS((*db).DBMS()) {
	case DBMySQLStr:
		query = `
			INSERT IGNORE INTO SourceInformationSeedIndex (source_id, information_seed_id)
			VALUES (?, ?)`
	case DBPostgresStr, DBSQLiteStr:
		query = `
			INSERT INTO SourceInformationSeedIndex (source_id, information_seed_id)
			VALUES ($1, $2)
			ON CONFLICT (source_id, information_seed_id) DO NOTHING`
	default:
		return fmt.Errorf("unsupported database type for source/information-seed links: %s", (*db).DBMS())
	}

	for _, link := range links {
		if link.SourceID == 0 || link.InformationSeedID == 0 {
			return fmt.Errorf("sourceID and informationSeedID must be provided")
		}
		if _, err = tx.Exec(query, link.SourceID, link.InformationSeedID); err != nil {
			return fmt.Errorf("failed to link source %d to information seed %d: %w", link.SourceID, link.InformationSeedID, err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit source/information-seed link transaction: %w", err)
	}
	committed = true
	return nil
}

// GetSourcesForInformationSeed returns the sources linked to an information seed.
func GetSourcesForInformationSeed(db *Handler, seedID uint64) ([]Source, error) {
	if db == nil || *db == nil {
		return nil, fmt.Errorf("database handler is nil")
	}
	if seedID == 0 {
		return nil, fmt.Errorf("information seed ID must be provided")
	}

	joinDeletedFilter, err := sourceInformationSeedDeletedAtJoinFilter(db)
	if err != nil {
		return nil, err
	}
	dbms := normalizeInformationSeedDBMS((*db).DBMS())
	if !isSupportedInformationSeedDBMS(dbms) {
		return nil, fmt.Errorf("unsupported database type for source/information-seed lookup: %s", (*db).DBMS())
	}
	placeholder := informationSeedPlaceholderForDBMS(dbms, 1)
	query := fmt.Sprintf(`
		SELECT source.source_id, source.priority, source.category_id, source.name,
			source.usr_id, source.url, source.restricted, source.flags, source.config,
			source.disabled
		FROM Sources AS source
		INNER JOIN SourceInformationSeedIndex AS link
			ON link.source_id = source.source_id%s
		WHERE link.information_seed_id = %s
		ORDER BY source.source_id ASC`, joinDeletedFilter, placeholder)
	rows, err := (*db).ExecuteQuery(query, seedID)
	if err != nil {
		return nil, fmt.Errorf("failed to get sources for information seed %d: %w", seedID, err)
	}
	return scanInformationSeedSourceRows(rows)
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

func scanInformationSeedSourceRows(rows *sql.Rows) ([]Source, error) {
	defer rows.Close()
	sources := []Source{}
	for rows.Next() {
		var source Source
		var config sql.NullString
		if err := rows.Scan(
			&source.ID,
			&source.Priority,
			&source.CategoryID,
			&source.Name,
			&source.UsrID,
			&source.URL,
			&source.Restricted,
			&source.Flags,
			&config,
			&source.Disabled,
		); err != nil {
			return nil, err
		}
		if config.Valid {
			raw := json.RawMessage(config.String)
			source.Config = &raw
		}
		sources = append(sources, source)
	}
	return sources, rows.Err()
}
