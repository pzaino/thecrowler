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

// Package database is responsible for handling the database setup, configuration and abstraction.
package database

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/lib/pq"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

// GetSourceByID retrieves a source from the database by its ID.
func GetSourceByID(db *Handler, sourceID uint64) (*Source, error) {
	source := &Source{}

	// Query the database
	err := (*db).QueryRow(`SELECT source_id, source_uid, url, name, category_id, usr_id, restricted, flags, config FROM Sources WHERE source_id = $1`, sourceID).Scan(&source.ID, &source.UID, &source.URL, &source.Name, &source.CategoryID, &source.UsrID, &source.Restricted, &source.Flags, &source.Config)
	if err != nil {
		return nil, fmt.Errorf("no source found with ID %d", sourceID)
	}

	return source, nil
}

// CreateSource inserts a new source into the database with detailed configuration validation and marshaling.
func CreateSource(db *Handler, source *Source, config cfg.SourceConfig) (uint64, error) {
	if db == nil || *db == nil {
		return 0, fmt.Errorf("database handler is nil")
	}
	if source == nil {
		return 0, fmt.Errorf("source is nil")
	}

	// Validate source config
	if !config.IsEmpty() {
		if err := validateSourceConfig(config); err != nil {
			return 0, fmt.Errorf("invalid source configuration: %v", err)
		}
	}

	// Marshal config JSON
	details, err := json.Marshal(config)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal source configuration: %v", err)
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
		Disabled:   source.Disabled,
	}
	prepared.UID = CalculateSourceUID(prepared.Name, prepared.URL)
	source.UID = prepared.UID

	switch normalizeInformationSeedDBMS((*db).DBMS()) {
	case DBPostgresStr:
		return createSourcePostgres(db, prepared)
	case DBSQLiteStr:
		return createSourceSQLite(db, prepared)
	case DBMySQLStr:
		return createSourceMySQL(db, prepared)
	default:
		return 0, fmt.Errorf("unsupported database type for source creation: %s", (*db).DBMS())
	}
}

// CalculateSourceUID returns a stable SHA-256 identifier for a source.
func CalculateSourceUID(name, sourceURL string) string {
	normalizedName := strings.TrimSpace(name)
	normalizedURL := NormalizeSourceURL(sourceURL)
	payload := fmt.Sprintf("%d:%s%d:%s", len(normalizedName), normalizedName, len(normalizedURL), normalizedURL)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(payload)))
}

// NormalizeSourceURL prepares a source URL for storage and text-based search.
//
// URL producers commonly percent-encode the ":" and "/" characters in nested
// URLs used as query parameter values. Those characters do not need escaping
// inside a query, and keeping them escaped prevents searches for the nested URL
// from matching the stored source. Delimiters such as '&', '=', '#', and '+'
// remain escaped so normalization does not change the query's meaning.
func NormalizeSourceURL(rawURL string) string {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return ""
	}

	parsedURL, err := url.Parse(trimmed)
	if err != nil || parsedURL.RawQuery == "" {
		return trimmed
	}

	parsedURL.RawQuery = decodeSearchableQueryCharacters(parsedURL.RawQuery)
	return parsedURL.String()
}

func decodeSearchableQueryCharacters(rawQuery string) string {
	replacer := strings.NewReplacer(
		"%2F", "/",
		"%2f", "/",
		"%3A", ":",
		"%3a", ":",
	)
	return replacer.Replace(rawQuery)
}

type preparedSourceInsert struct {
	UID        string
	URL        string
	Name       string
	Priority   string
	CategoryID uint64
	UsrID      uint64
	Restricted uint
	Flags      uint
	Config     []byte
	Disabled   bool
	Status     string
}

func (source preparedSourceInsert) args() []interface{} {
	return []interface{}{
		source.URL,
		source.Name,
		source.Priority,
		source.CategoryID,
		source.UsrID,
		source.Restricted,
		source.Flags,
		source.Config,
		source.Disabled,
		source.UID,
	}
}

func (source preparedSourceInsert) argsWithStatus() []interface{} {
	args := source.args()
	return append(args, source.Status)
}

func createSourcePostgres(db *Handler, source preparedSourceInsert) (uint64, error) {
	var sourceID uint64

	query := `
        INSERT INTO Sources
            (url, name, priority, category_id, usr_id, restricted, flags, config, disabled, source_uid, status)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'new')
        ON CONFLICT (url) DO UPDATE
        SET
            -- update name only if not processing AND non-empty trimmed string
            name = CASE
                WHEN Sources.status <> 'processing'
                     AND BTRIM(EXCLUDED.name) <> ''
                THEN EXCLUDED.name
                ELSE Sources.name
            END,
            source_uid = CASE
                WHEN Sources.status <> 'processing' AND BTRIM(EXCLUDED.name) <> ''
                THEN EXCLUDED.source_uid
                ELSE Sources.source_uid
            END,

            -- update priority only if not processing AND non-empty trimmed string
            priority = CASE
                WHEN Sources.status <> 'processing'
                     AND BTRIM(EXCLUDED.priority) <> ''
                THEN EXCLUDED.priority
                ELSE Sources.priority
            END,

            -- update integers only if not processing
            category_id = CASE
                WHEN Sources.status <> 'processing'
                THEN EXCLUDED.category_id
                ELSE Sources.category_id
            END,

            usr_id = CASE
                WHEN Sources.status <> 'processing'
                THEN EXCLUDED.usr_id
                ELSE Sources.usr_id
            END,

            restricted = CASE
                WHEN Sources.status <> 'processing'
                THEN EXCLUDED.restricted
                ELSE Sources.restricted
            END,

            flags = CASE
                WHEN Sources.status <> 'processing'
                THEN EXCLUDED.flags
                ELSE Sources.flags
            END,

            -- config updated only when:
            -- 1) NOT processing
            -- 2) new config is meaningful
            -- 3) new config differs from existing config
            config = CASE
                WHEN Sources.status <> 'processing'
                     AND EXCLUDED.config IS NOT NULL
                     AND EXCLUDED.config <> '{}'::jsonb
                     AND md5(EXCLUDED.config::text) <> md5(Sources.config::text)
                THEN EXCLUDED.config
                ELSE Sources.config
            END,

			disabled = CASE
				WHEN Sources.status <> 'processing'
				THEN EXCLUDED.disabled
				ELSE Sources.disabled
			END,

			status = CASE
				WHEN Sources.status = 'processing'
				THEN Sources.status
				ELSE EXCLUDED.status
			END,

            last_updated_at = NOW()
        RETURNING source_id;
    `

	err := (*db).QueryRow(query, source.args()...).Scan(&sourceID)
	if err != nil {
		return 0, fmt.Errorf("failed to create PostgreSQL source: %v", err)
	}

	return sourceID, nil
}

func createSourceSQLite(db *Handler, source preparedSourceInsert) (uint64, error) {
	var sourceID uint64

	query := `
		INSERT INTO Sources
			(url, name, priority, category_id, usr_id, restricted, flags, config, disabled, source_uid, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'new')
		ON CONFLICT (url) DO UPDATE SET
			name = CASE
				WHEN Sources.status <> 'processing'
					 AND TRIM(excluded.name) <> ''
				THEN excluded.name
				ELSE Sources.name
			END,
			source_uid = CASE
				WHEN Sources.status <> 'processing' AND TRIM(excluded.name) <> ''
				THEN excluded.source_uid
				ELSE Sources.source_uid
			END,
			priority = CASE
				WHEN Sources.status <> 'processing'
					 AND TRIM(excluded.priority) <> ''
				THEN excluded.priority
				ELSE Sources.priority
			END,
			category_id = CASE
				WHEN Sources.status <> 'processing'
				THEN excluded.category_id
				ELSE Sources.category_id
			END,
			usr_id = CASE
				WHEN Sources.status <> 'processing'
				THEN excluded.usr_id
				ELSE Sources.usr_id
			END,
			restricted = CASE
				WHEN Sources.status <> 'processing'
				THEN excluded.restricted
				ELSE Sources.restricted
			END,
			flags = CASE
				WHEN Sources.status <> 'processing'
				THEN excluded.flags
				ELSE Sources.flags
			END,
			config = CASE
				WHEN Sources.status <> 'processing'
					 AND excluded.config IS NOT NULL
					 AND TRIM(excluded.config) <> ''
					 AND TRIM(excluded.config) <> '{}'
					 AND COALESCE(excluded.config, '') <> COALESCE(Sources.config, '')
				THEN excluded.config
				ELSE Sources.config
			END,
			disabled = CASE
				WHEN Sources.status <> 'processing'
				THEN excluded.disabled
				ELSE Sources.disabled
			END,
			status = CASE
				WHEN Sources.status = 'processing'
				THEN Sources.status
				ELSE excluded.status
			END,
			last_updated_at = CURRENT_TIMESTAMP
		RETURNING source_id`

	err := (*db).QueryRow(query, source.args()...).Scan(&sourceID)
	if err != nil {
		return 0, fmt.Errorf("failed to create SQLite source: %v", err)
	}

	return sourceID, nil
}

func createSourceMySQL(db *Handler, source preparedSourceInsert) (uint64, error) {
	query := `
		INSERT INTO Sources
			(url, name, priority, category_id, usr_id, restricted, flags, config, disabled, source_uid, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'new')
		ON DUPLICATE KEY UPDATE
			source_id = LAST_INSERT_ID(source_id),
			name = CASE
				WHEN status <> 'processing'
					 AND TRIM(VALUES(name)) <> ''
				THEN VALUES(name)
				ELSE name
			END,
			source_uid = CASE
				WHEN status <> 'processing' AND TRIM(VALUES(name)) <> ''
				THEN VALUES(source_uid)
				ELSE source_uid
			END,
			priority = CASE
				WHEN status <> 'processing'
					 AND TRIM(VALUES(priority)) <> ''
				THEN VALUES(priority)
				ELSE priority
			END,
			category_id = CASE
				WHEN status <> 'processing'
				THEN VALUES(category_id)
				ELSE category_id
			END,
			usr_id = CASE
				WHEN status <> 'processing'
				THEN VALUES(usr_id)
				ELSE usr_id
			END,
			restricted = CASE
				WHEN status <> 'processing'
				THEN VALUES(restricted)
				ELSE restricted
			END,
			flags = CASE
				WHEN status <> 'processing'
				THEN VALUES(flags)
				ELSE flags
			END,
			config = CASE
				WHEN status <> 'processing'
					 AND VALUES(config) IS NOT NULL
					 AND JSON_LENGTH(VALUES(config)) > 0
					 AND COALESCE(MD5(CAST(VALUES(config) AS CHAR)), '') <> COALESCE(MD5(CAST(config AS CHAR)), '')
				THEN VALUES(config)
				ELSE config
			END,
			disabled = CASE
				WHEN status <> 'processing'
				THEN VALUES(disabled)
				ELSE disabled
			END,
			status = CASE
				WHEN status = 'processing'
				THEN status
				ELSE VALUES(status)
			END,
			last_updated_at = CURRENT_TIMESTAMP`

	result, err := (*db).Exec(query, source.args()...)
	if err != nil {
		return 0, fmt.Errorf("failed to create MySQL source: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to retrieve MySQL source ID: %v", err)
	}
	if id < 0 {
		return 0, fmt.Errorf("failed to retrieve MySQL source ID: negative ID %d", id)
	}

	return uint64(id), nil
}

// validateSourceConfig validates the SourceConfig struct.
func validateSourceConfig(config cfg.SourceConfig) error {
	if config.Version == "" || config.FormatVersion == "" || config.SourceName == "" {
		return fmt.Errorf("version, format_version, and source_name are required fields")
	}

	if err := validateURL(config.CrawlingConfig.Site); err != nil {
		return fmt.Errorf("invalid URL in crawling_config: %v", err)
	}

	for _, item := range config.ExecutionPlan {
		if item.Label == "" || len(item.Conditions.URLPatterns) == 0 {
			return fmt.Errorf("execution plan items must have a label and at least one URL pattern")
		}
	}

	// Add more validation as needed based on your specific requirements.
	return nil
}

// validateURL checks if a given URL is valid.
func validateURL(site string) error {
	parsedURL, err := url.Parse(site)
	if err != nil || parsedURL.Scheme == "" {
		return fmt.Errorf("invalid URL: %s", site)
	}

	scheme := strings.ToLower(parsedURL.Scheme)
	if scheme == "maildir" || scheme == "mbox" {
		if strings.HasPrefix(strings.ToLower(site), scheme+"://") &&
			parsedURL.Host == "" && strings.HasPrefix(parsedURL.Path, "/") && parsedURL.Path != "/" {
			return nil
		}
		return fmt.Errorf("invalid URL: %s", site)
	}

	if parsedURL.Host != "" {
		return nil
	}

	return fmt.Errorf("invalid URL: %s", site)
}

// UpdateSource updates an existing source in the database by ID.
func UpdateSource(db *Handler, source *Source) error {
	query := `
        UPDATE Sources
        SET url = $1, name = $2, category_id = $3, usr_id = $4, restricted = $5, flags = $6, config = $7, last_updated_at = NOW()
        WHERE source_id = $8
    `
	_, err := (*db).Exec(query, NormalizeSourceURL(source.URL), source.Name, source.CategoryID, source.UsrID, source.Restricted, source.Flags, source.Config, source.ID)
	if err != nil {
		return fmt.Errorf("failed to update source with ID %d: %v", source.ID, err)
	}
	return nil
}

// DeleteSource removes a source from the database by ID.
func DeleteSource(db *Handler, sourceID uint64) error {
	query := `DELETE FROM Sources WHERE source_id = $1`
	_, err := (*db).Exec(query, sourceID)
	if err != nil {
		return fmt.Errorf("failed to delete source with ID %d: %v", sourceID, err)
	}
	return nil
}

// DeleteSources removes multiple sources from the database by ID.
func DeleteSources(db *Handler, sourceIDs []uint64) error {
	if len(sourceIDs) == 0 {
		return nil
	}

	ids := make([]int64, 0, len(sourceIDs))
	for _, sourceID := range sourceIDs {
		if sourceID == 0 {
			continue
		}
		if sourceID > uint64(1<<63-1) {
			return fmt.Errorf("source ID %d exceeds supported database integer range", sourceID)
		}
		ids = append(ids, int64(sourceID))
	}
	if len(ids) == 0 {
		return nil
	}

	if (*db).DBMS() == DBPostgresStr {
		_, err := (*db).Exec(`DELETE FROM Sources WHERE source_id = ANY($1)`, pq.Array(ids))
		if err != nil {
			return fmt.Errorf("failed to delete %d sources: %v", len(ids), err)
		}
		return nil
	}

	tx, err := (*db).Begin()
	if err != nil {
		return fmt.Errorf("failed to start source deletion transaction: %w", err)
	}
	for _, sourceID := range ids {
		if _, err = tx.Exec(`DELETE FROM Sources WHERE source_id = $1`, sourceID); err != nil {
			if rollbackErr := tx.Rollback(); rollbackErr != nil {
				return fmt.Errorf("failed to rollback source deletion transaction: %w (original error: %v)", rollbackErr, err)
			}
			return fmt.Errorf("failed to delete source with ID %d: %w", sourceID, err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit source deletion transaction: %w", err)
	}

	return nil
}

// VacuumSource performs a VACUUM operation for the collected data associated with a given Source ID.
func VacuumSource(db *Handler, sourceID uint64) error {
	if sourceID == 0 {
		return fmt.Errorf("sourceID must be provided")
	}

	tx, err := (*db).Begin()
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}

	// List of SQL queries to remove associated indexed data for the given source
	queries := []string{
		"DELETE FROM KeywordIndex WHERE index_id IN (SELECT index_id FROM SourceSearchIndex WHERE source_id = $1)",
		"DELETE FROM MetaTagsIndex WHERE index_id IN (SELECT index_id FROM SourceSearchIndex WHERE source_id = $1)",
		"DELETE FROM WebObjectsIndex WHERE index_id IN (SELECT index_id FROM SourceSearchIndex WHERE source_id = $1)",
		"DELETE FROM NetInfoIndex WHERE index_id IN (SELECT index_id FROM SourceSearchIndex WHERE source_id = $1)",
		"DELETE FROM HTTPInfoIndex WHERE index_id IN (SELECT index_id FROM SourceSearchIndex WHERE source_id = $1)",
		"DELETE FROM SourceSearchIndex WHERE source_id = $1",
	}

	// Execute each query in the list
	for _, query := range queries {
		_, err := tx.Exec(query, sourceID)
		if err != nil {
			rollbackErr := tx.Rollback() // Rollback the transaction on error
			if rollbackErr != nil {
				return fmt.Errorf("failed to rollback transaction: %w (original error: %v)", rollbackErr, err)
			}
			return fmt.Errorf("failed to execute query: %w", err)
		}
	}

	// Commit the transaction if all queries succeed
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// ListSources retrieves all sources from the database with optional filters.
func ListSources(db *Handler, categoryID *uint64, userID *uint64) ([]Source, error) {
	sources := []Source{}
	query := `SELECT source_id, source_uid, url, name, category_id, usr_id, restricted, flags, config FROM Sources`
	var args []interface{}
	var conditions []string

	if categoryID != nil {
		conditions = append(conditions, "category_id = $1")
		args = append(args, *categoryID)
	}
	if userID != nil {
		conditions = append(conditions, "usr_id = $2")
		args = append(args, *userID)
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	rows, err := (*db).ExecuteQuery(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list sources: %v", err)
	}
	defer rows.Close() //nolint:errcheck // We can't check return value on defer

	for rows.Next() {
		var source Source
		err := rows.Scan(&source.ID, &source.UID, &source.URL, &source.Name, &source.CategoryID, &source.UsrID, &source.Restricted, &source.Flags, &source.Config)
		if err != nil {
			return nil, fmt.Errorf("failed to scan source: %v", err)
		}
		sources = append(sources, source)
	}
	return sources, nil
}

// GetSourcesByStatus retrieves all sources with a specific status.
func GetSourcesByStatus(db *Handler, status string) ([]Source, error) {
	sources := []Source{}
	query := `
        SELECT source_id, source_uid, url, name, category_id, usr_id, restricted, flags, config
        FROM Sources
        WHERE status = $1
    `
	rows, err := (*db).ExecuteQuery(query, status)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve sources by status: %v", err)
	}
	defer rows.Close() //nolint:errcheck // We can't check return value on defer

	for rows.Next() {
		var source Source
		err := rows.Scan(&source.ID, &source.UID, &source.URL, &source.Name, &source.CategoryID, &source.UsrID, &source.Restricted, &source.Flags, &source.Config)
		if err != nil {
			return nil, fmt.Errorf("failed to scan source: %v", err)
		}
		sources = append(sources, source)
	}
	return sources, nil
}
