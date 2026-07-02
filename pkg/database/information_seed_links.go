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
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
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
	dbms := normalizeInformationSeedDBMS((*db).DBMS())
	if !isSupportedInformationSeedDBMS(dbms) {
		return fmt.Errorf("unsupported database type for source/information-seed links: %s", (*db).DBMS())
	}
	metadataJSON, err := discoveryMetadataString(metadata.DiscoveryMetadata)
	if err != nil {
		return err
	}
	tx, err := (*db).Begin()
	if err != nil {
		return fmt.Errorf("failed to start source discovery transaction: %w", err)
	}
	committed := false
	defer rollbackIfUncommitted(tx, &committed)
	args := []interface{}{sourceID, informationSeedID, nullableArg(metadata.DiscoveryProvider), nullableArg(metadata.DiscoveryQuery), nullableArg(metadata.DiscoveryRank), nullableArg(metadata.CandidateScore), nullableArg(metadata.CandidateReason), nullableArg(metadataJSON)}
	var query string
	switch dbms {
	case DBPostgresStr:
		query = `INSERT INTO SourceInformationSeedIndex (
			source_id, information_seed_id, discovery_provider, discovery_query,
			discovery_rank, candidate_score, candidate_reason, discovery_metadata)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb)
			ON CONFLICT (source_id, information_seed_id) DO UPDATE SET
			discovery_provider = COALESCE(EXCLUDED.discovery_provider, SourceInformationSeedIndex.discovery_provider),
			discovery_query = COALESCE(EXCLUDED.discovery_query, SourceInformationSeedIndex.discovery_query),
			discovery_rank = COALESCE(EXCLUDED.discovery_rank, SourceInformationSeedIndex.discovery_rank),
			candidate_score = COALESCE(EXCLUDED.candidate_score, SourceInformationSeedIndex.candidate_score),
			candidate_reason = COALESCE(EXCLUDED.candidate_reason, SourceInformationSeedIndex.candidate_reason),
			discovery_metadata = CASE WHEN EXCLUDED.discovery_metadata IS NULL THEN SourceInformationSeedIndex.discovery_metadata
			ELSE COALESCE(SourceInformationSeedIndex.discovery_metadata, '{}'::jsonb) || EXCLUDED.discovery_metadata END`
	case DBMySQLStr:
		query = `INSERT INTO SourceInformationSeedIndex (
			source_id, information_seed_id, discovery_provider, discovery_query,
			discovery_rank, candidate_score, candidate_reason, discovery_metadata)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE
			discovery_provider = COALESCE(VALUES(discovery_provider), discovery_provider),
			discovery_query = COALESCE(VALUES(discovery_query), discovery_query),
			discovery_rank = COALESCE(VALUES(discovery_rank), discovery_rank),
			candidate_score = COALESCE(VALUES(candidate_score), candidate_score),
			candidate_reason = COALESCE(VALUES(candidate_reason), candidate_reason),
			discovery_metadata = CASE WHEN VALUES(discovery_metadata) IS NULL THEN discovery_metadata
			ELSE JSON_MERGE_PATCH(COALESCE(discovery_metadata, JSON_OBJECT()), VALUES(discovery_metadata)) END`
	case DBSQLiteStr:
		query = `INSERT INTO SourceInformationSeedIndex (
			source_id, information_seed_id, discovery_provider, discovery_query,
			discovery_rank, candidate_score, candidate_reason, discovery_metadata)
			VALUES ($1, $2, $3, $4, $5, $6, $7, json($8))
			ON CONFLICT (source_id, information_seed_id) DO UPDATE SET
			discovery_provider = COALESCE(excluded.discovery_provider, SourceInformationSeedIndex.discovery_provider),
			discovery_query = COALESCE(excluded.discovery_query, SourceInformationSeedIndex.discovery_query),
			discovery_rank = COALESCE(excluded.discovery_rank, SourceInformationSeedIndex.discovery_rank),
			candidate_score = COALESCE(excluded.candidate_score, SourceInformationSeedIndex.candidate_score),
			candidate_reason = COALESCE(excluded.candidate_reason, SourceInformationSeedIndex.candidate_reason),
			discovery_metadata = CASE WHEN excluded.discovery_metadata IS NULL THEN SourceInformationSeedIndex.discovery_metadata
			ELSE json_patch(COALESCE(SourceInformationSeedIndex.discovery_metadata, '{}'), excluded.discovery_metadata) END`
	}
	if _, err = tx.Exec(query, args...); err != nil {
		return fmt.Errorf("failed to link source %d to information seed %d with discovery metadata: %w", sourceID, informationSeedID, err)
	}
	link, err := getSourceInformationSeedIndexTx(tx, dbms, sourceID, informationSeedID)
	if err != nil {
		return err
	}
	if err = emitSourceDiscoveryObservationsTx(tx, dbms, *link); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit source discovery transaction: %w", err)
	}
	committed = true
	return nil
}

func getSourceInformationSeedIndexTx(tx *sql.Tx, dbms string, sourceID, seedID uint64) (*SourceInformationSeedIndex, error) {
	p := newInformationSeedPlaceholders(dbms)
	row := tx.QueryRow(`SELECT source_information_seed_id, source_id, information_seed_id,
		discovery_provider, discovery_query, discovery_rank, candidate_score, candidate_reason,
		discovery_metadata, created_at, last_updated_at FROM SourceInformationSeedIndex
		WHERE source_id = `+p.Next()+` AND information_seed_id = `+p.Next(), sourceID, seedID)
	var link SourceInformationSeedIndex
	if err := row.Scan(&link.ID, &link.SourceID, &link.InformationSeedID, &link.DiscoveryProvider,
		&link.DiscoveryQuery, &link.DiscoveryRank, &link.CandidateScore, &link.CandidateReason,
		&link.DiscoveryMetadata, &link.CreatedAt, &link.LastUpdatedAt); err != nil {
		return nil, fmt.Errorf("lookup persisted source discovery link: %w", err)
	}
	return &link, nil
}

func emitSourceDiscoveryObservationsTx(tx *sql.Tx, dbms string, link SourceInformationSeedIndex) error {
	metadata := map[string]interface{}{}
	if link.DiscoveryMetadata.Valid {
		_ = json.Unmarshal([]byte(link.DiscoveryMetadata.String), &metadata)
	}
	fields := map[string]interface{}{"count": 1, "promoted_count": 1, "provider": link.DiscoveryProvider.String,
		"discovery_provider": link.DiscoveryProvider.String, "query": link.DiscoveryQuery.String,
		"discovery_query": link.DiscoveryQuery.String, "rank": link.DiscoveryRank.Int64,
		"discovery_rank": link.DiscoveryRank.Int64, "score": link.CandidateScore.Float64,
		"candidate_score": link.CandidateScore.Float64, "reason": link.CandidateReason.String,
		"candidate_reason": link.CandidateReason.String, "metadata": metadata}
	observedAt := time.Now().UTC()
	if link.LastUpdatedAt.Valid {
		observedAt = link.LastUpdatedAt.Time.UTC()
	}
	seedID, sourceID, linkID := link.InformationSeedID, link.SourceID, link.ID
	return emitInformationSeedObservationsTx(tx, dbms, informationSeedObservationEvent{
		SourceKind: cfg.TimeSeriesSourceDiscovery, Event: "promoted", Identity: fmt.Sprintf("source-discovery:%d:promoted", link.ID), ObservedAt: observedAt,
		Scope: TimeSeriesScope{InformationSeedID: &seedID, SourceID: &sourceID, SourceInformationSeedID: &linkID,
			SubjectType: string(cfg.TimeSeriesSourceDiscovery), SubjectID: &linkID}, Fields: fields,
		Provenance: map[string]interface{}{"information_seed_id": link.InformationSeedID, "source_id": link.SourceID,
			"source_information_seed_id": link.ID, "link_row_id": link.ID, "provider_identifier": link.DiscoveryProvider.String,
			"discovery_query": link.DiscoveryQuery.String, "discovery_rank": link.DiscoveryRank.Int64,
			"candidate_score": link.CandidateScore.Float64, "candidate_reason": link.CandidateReason.String,
			"discovery_metadata": metadata, "transition": "promoted"},
	})
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

// ListSourcesForInformationSeed returns linked sources with per-link discovery
// provenance in stable source-ID order and with optional pagination.
func ListSourcesForInformationSeed(db *Handler, seedID uint64, filter InformationSeedLinkedSourceFilter) ([]InformationSeedLinkedSource, error) {
	if db == nil || *db == nil {
		return nil, fmt.Errorf("database handler is nil")
	}
	if seedID == 0 {
		return nil, fmt.Errorf("information seed ID must be provided")
	}
	if filter.Limit < 0 || filter.Offset < 0 {
		return nil, fmt.Errorf("limit and offset must be non-negative")
	}

	joinDeletedFilter, err := sourceInformationSeedDeletedAtJoinFilter(db)
	if err != nil {
		return nil, err
	}
	dbms := normalizeInformationSeedDBMS((*db).DBMS())
	if !isSupportedInformationSeedDBMS(dbms) {
		return nil, fmt.Errorf("unsupported database type for source/information-seed lookup: %s", (*db).DBMS())
	}
	placeholders := newInformationSeedPlaceholders(dbms)
	query := fmt.Sprintf(`
		SELECT source.source_id, source.priority, source.category_id, source.name,
			source.usr_id, source.url, source.restricted, source.flags, source.config,
			source.disabled,
			link.source_information_seed_id, link.source_id, link.information_seed_id,
			link.discovery_provider, link.discovery_query, link.discovery_rank,
			link.candidate_score, link.candidate_reason, link.discovery_metadata,
			link.created_at, link.last_updated_at
		FROM Sources AS source
		INNER JOIN SourceInformationSeedIndex AS link
			ON link.source_id = source.source_id%s
		WHERE link.information_seed_id = %s
		ORDER BY source.source_id ASC`, joinDeletedFilter, placeholders.Next())
	args := []interface{}{seedID}
	if filter.Limit > 0 {
		query += " LIMIT " + placeholders.Next()
		args = append(args, filter.Limit)
	}
	if filter.Offset > 0 {
		query += " OFFSET " + placeholders.Next()
		args = append(args, filter.Offset)
	}
	rows, err := (*db).ExecuteQuery(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list sources for information seed %d: %w", seedID, err)
	}
	return scanInformationSeedLinkedSourceRows(rows)
}

func scanInformationSeedLinkedSourceRows(rows *sql.Rows) ([]InformationSeedLinkedSource, error) {
	defer rows.Close()
	linked := []InformationSeedLinkedSource{}
	for rows.Next() {
		var item InformationSeedLinkedSource
		var config sql.NullString
		if err := rows.Scan(
			&item.Source.ID,
			&item.Source.Priority,
			&item.Source.CategoryID,
			&item.Source.Name,
			&item.Source.UsrID,
			&item.Source.URL,
			&item.Source.Restricted,
			&item.Source.Flags,
			&config,
			&item.Source.Disabled,
			&item.Index.ID,
			&item.Index.SourceID,
			&item.Index.InformationSeedID,
			&item.Index.DiscoveryProvider,
			&item.Index.DiscoveryQuery,
			&item.Index.DiscoveryRank,
			&item.Index.CandidateScore,
			&item.Index.CandidateReason,
			&item.Index.DiscoveryMetadata,
			&item.Index.CreatedAt,
			&item.Index.LastUpdatedAt,
		); err != nil {
			return nil, err
		}
		if config.Valid {
			raw := json.RawMessage(config.String)
			item.Source.Config = &raw
		}
		linked = append(linked, item)
	}
	return linked, rows.Err()
}
