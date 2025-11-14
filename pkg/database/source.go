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
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

// GetSourceByID retrieves a source from the database by its ID.
func GetSourceByID(db *Handler, sourceID uint64) (*Source, error) {
	source := &Source{}

	// Query the database
	err := (*db).QueryRow(`SELECT source_id, url, name, category_id, usr_id, restricted, flags, config FROM Sources WHERE source_id = $1`, sourceID).Scan(&source.ID, &source.URL, &source.Name, &source.CategoryID, &source.UsrID, &source.Restricted, &source.Flags, &source.Config)
	if err != nil {
		return nil, fmt.Errorf("no source found with ID %d", sourceID)
	}

	return source, nil
}

// CreateSource inserts a new source into the database with detailed configuration validation and marshaling.
func CreateSource(db *Handler, source *Source, config cfg.SourceConfig) (uint64, error) {
	// Validate source config
	if err := validateSourceConfig(config); err != nil {
		return 0, fmt.Errorf("invalid source configuration: %v", err)
	}

	// Marshal config JSON
	details, err := json.Marshal(config)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal source configuration: %v", err)
	}

	// Trim strings to guarantee consistency
	url := strings.TrimSpace(source.URL)
	name := strings.TrimSpace(source.Name)
	priority := strings.TrimSpace(source.Priority)

	var sourceID uint64

	query := `
        INSERT INTO Sources
            (url, name, priority, category_id, usr_id, restricted, flags, config)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
        ON CONFLICT (url) DO UPDATE
        SET
            -- update name only if not processing AND non-empty trimmed string
            name = CASE
                WHEN Sources.status <> 'processing'
                     AND BTRIM(EXCLUDED.name) <> ''
                THEN EXCLUDED.name
                ELSE Sources.name
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

            last_updated_at = NOW()
        RETURNING source_id;
    `

	err = (*db).QueryRow(
		query,
		url,
		name,
		priority,
		source.CategoryID,
		source.UsrID,
		source.Restricted,
		source.Flags,
		details,
	).Scan(&sourceID)

	if err != nil {
		return 0, fmt.Errorf("failed to create source: %v", err)
	}

	return sourceID, nil
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
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		return fmt.Errorf("invalid URL: %s", site)
	}
	return nil
}

// UpdateSource updates an existing source in the database by ID.
func UpdateSource(db *Handler, source *Source) error {
	query := `
        UPDATE Sources
        SET url = $1, name = $2, category_id = $3, usr_id = $4, restricted = $5, flags = $6, config = $7, last_updated_at = NOW()
        WHERE source_id = $8
    `
	_, err := (*db).Exec(query, source.URL, source.Name, source.CategoryID, source.UsrID, source.Restricted, source.Flags, source.Config, source.ID)
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
	query := `SELECT source_id, url, name, category_id, usr_id, restricted, flags, config FROM Sources`
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
		err := rows.Scan(&source.ID, &source.URL, &source.Name, &source.CategoryID, &source.UsrID, &source.Restricted, &source.Flags, &source.Config)
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
        SELECT source_id, url, name, category_id, usr_id, restricted, flags, config
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
		err := rows.Scan(&source.ID, &source.URL, &source.Name, &source.CategoryID, &source.UsrID, &source.Restricted, &source.Flags, &source.Config)
		if err != nil {
			return nil, fmt.Errorf("failed to scan source: %v", err)
		}
		sources = append(sources, source)
	}
	return sources, nil
}
