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

// InformationSeedFilter describes optional predicates and pagination for
// listing information seeds. Zero-valued scalar fields are ignored; use pointer
// fields where zero is a meaningful database value.
type InformationSeedFilter struct {
	ID         uint64
	Status     string
	Priority   string
	Disabled   *bool
	CategoryID *uint64
	UsrID      *uint64
	UserID     *uint64
	Limit      int
	Offset     int
}

// SourceSeedLink represents a relationship between a source and an information
// seed. Inserting an already-existing source/seed pair is treated as success.
type SourceSeedLink struct {
	SourceID          uint64
	InformationSeedID uint64
}

// InformationSeedWithStats represents an InformationSeed row with aggregate
// relationship statistics.
type InformationSeedWithStats struct {
	InformationSeed
	DiscoveredSourceCount uint64
}

// SourceInformationSeedIndex represents per-relationship discovery provenance
// stored for the source/information-seed pair.
type SourceInformationSeedIndex struct {
	ID                uint64
	SourceID          uint64
	InformationSeedID uint64
	DiscoveryProvider sql.NullString
	DiscoveryQuery    sql.NullString
	DiscoveryRank     sql.NullInt64
	CandidateScore    sql.NullFloat64
	CandidateReason   sql.NullString
	DiscoveryMetadata sql.NullString
	CreatedAt         sql.NullTime
	LastUpdatedAt     sql.NullTime
}

// InformationSeedLinkedSource combines a linked Source with the
// SourceInformationSeedIndex row that explains how that seed discovered it.
type InformationSeedLinkedSource struct {
	Source Source
	Index  SourceInformationSeedIndex
}

// InformationSeedLinkedSourceFilter describes pagination for linked-source
// lookups.
type InformationSeedLinkedSourceFilter struct {
	Limit  int
	Offset int
}

func sourceInformationSeedDeletedAtJoinFilter(db *Handler) (string, error) {
	exists, err := tableColumnExists(db, "SourceInformationSeedIndex", "deleted_at")
	if err != nil {
		return "", err
	}
	if !exists {
		return "", nil
	}
	return " AND link.deleted_at IS NULL", nil
}

func tableColumnExists(db *Handler, tableName, columnName string) (bool, error) {
	var query string
	var args []interface{}
	switch normalizeInformationSeedDBMS((*db).DBMS()) {
	case DBPostgresStr:
		query = `
			SELECT COUNT(*)
			FROM information_schema.columns
			WHERE LOWER(table_name) = LOWER($1) AND LOWER(column_name) = LOWER($2)`
		args = []interface{}{tableName, columnName}
	case DBMySQLStr:
		query = `
			SELECT COUNT(*)
			FROM information_schema.columns
			WHERE table_schema = DATABASE() AND LOWER(table_name) = LOWER(?) AND LOWER(column_name) = LOWER(?)`
		args = []interface{}{tableName, columnName}
	case DBSQLiteStr:
		query = fmt.Sprintf("SELECT COUNT(*) FROM pragma_table_info(%q) WHERE name = ?", tableName)
		args = []interface{}{columnName}
	default:
		return false, fmt.Errorf("unsupported database type for schema inspection: %s", (*db).DBMS())
	}

	var count int
	if err := (*db).QueryRow(query, args...).Scan(&count); err != nil {
		return false, fmt.Errorf("failed to inspect column %s.%s: %w", tableName, columnName, err)
	}
	return count > 0, nil
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

func informationSeedSelectColumns() string {
	return `information_seed_id, created_at, last_updated_at, category_id, usr_id,
		information_seed, status, priority, engine, last_processed_at, last_error,
		last_error_at, disabled, attempts, config`
}

func informationSeedConfigString(config *json.RawMessage) (interface{}, error) {
	if config == nil {
		return nil, nil
	}
	if len(*config) == 0 {
		return nil, fmt.Errorf("information seed config must be valid JSON when provided")
	}
	if !json.Valid(*config) {
		return nil, fmt.Errorf("information seed config must be valid JSON")
	}
	return string(*config), nil
}

type informationSeedPlaceholders struct {
	dbms  string
	count int
}

func newInformationSeedPlaceholders(dbms string) *informationSeedPlaceholders {
	return &informationSeedPlaceholders{dbms: dbms}
}

func (placeholders *informationSeedPlaceholders) Next() string {
	placeholders.count++
	return informationSeedPlaceholderForDBMS(placeholders.dbms, placeholders.count)
}

func isSupportedInformationSeedDBMS(dbms string) bool {
	switch dbms {
	case DBPostgresStr, DBMySQLStr, DBSQLiteStr:
		return true
	default:
		return false
	}
}

func informationSeedPlaceholderForDBMS(dbms string, position int) string {
	if dbms == DBMySQLStr {
		return "?"
	}
	return fmt.Sprintf("$%d", position)
}

func scanInformationSeedRow(row *sql.Row) (*InformationSeed, error) {
	var seed InformationSeed
	var config sql.NullString
	if err := row.Scan(
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
	return &seed, nil
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
