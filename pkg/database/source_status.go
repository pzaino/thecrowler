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

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
)

// SourceStatusRow represents the source status fields exposed by status lookups.
type SourceStatusRow struct {
	SourceID      uint64
	URL           sql.NullString
	Status        sql.NullString
	Priority      sql.NullString
	Engine        sql.NullString
	CreatedAt     sql.NullString
	LastUpdatedAt sql.NullString
	LastCrawledAt sql.NullString
	LastError     sql.NullString
	LastErrorAt   sql.NullString
	Restricted    int
	Disabled      bool
	Flags         int
	Config        cfg.SourceConfig
}

const sourceStatusSelect = `
	SELECT source_id,
	       url,
	       status,
	       priority,
	       engine,
	       created_at,
	       last_updated_at,
	       last_crawled_at,
	       last_error,
	       last_error_at,
	       restricted,
	       disabled,
	       flags,
	       config
	FROM Sources`

// GetSourceStatusByURL retrieves source status rows matching sourceURL.
// The helper owns URL normalization so callers may pass raw user input.
func GetSourceStatusByURL(db *Handler, sourceURL string) ([]SourceStatusRow, error) {
	if db == nil || *db == nil {
		return nil, fmt.Errorf("database handler is nil")
	}

	normalizedURL := cmn.NormalizeURL(sourceURL)
	cmn.DebugMsg(cmn.DbgLvlDebug5, "Source URL: %s", normalizedURL)

	query := sourceStatusSelect + "\n\tWHERE url LIKE " + sourceStatusPlaceholder(db)
	rows, err := (*db).ExecuteQuery(query, "%"+normalizedURL+"%")
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve source status by URL: %w", err)
	}
	defer rows.Close() //nolint:errcheck // We can't check return value on defer

	return scanSourceStatusRows(rows)
}

// ListSourceStatuses retrieves all source status rows.
func ListSourceStatuses(db *Handler) ([]SourceStatusRow, error) {
	if db == nil || *db == nil {
		return nil, fmt.Errorf("database handler is nil")
	}

	rows, err := (*db).ExecuteQuery(sourceStatusSelect)
	if err != nil {
		return nil, fmt.Errorf("failed to list source statuses: %w", err)
	}
	defer rows.Close() //nolint:errcheck // We can't check return value on defer

	return scanSourceStatusRows(rows)
}

func sourceStatusPlaceholder(db *Handler) string {
	switch normalizeInformationSeedDBMS((*db).DBMS()) {
	case DBMySQLStr, DBSQLiteStr:
		return "?"
	default:
		return "$1"
	}
}

func scanSourceStatusRows(rows *sql.Rows) ([]SourceStatusRow, error) {
	statuses := []SourceStatusRow{}
	for rows.Next() {
		var row SourceStatusRow
		var configJSON []byte
		if err := rows.Scan(&row.SourceID, &row.URL, &row.Status, &row.Priority, &row.Engine, &row.CreatedAt, &row.LastUpdatedAt, &row.LastCrawledAt, &row.LastError, &row.LastErrorAt, &row.Restricted, &row.Disabled, &row.Flags, &configJSON); err != nil {
			return nil, fmt.Errorf("failed to scan source status: %w", err)
		}
		if len(configJSON) > 0 {
			if err := json.Unmarshal(configJSON, &row.Config); err != nil {
				return nil, fmt.Errorf("failed to unmarshal source status config: %w", err)
			}
		}
		statuses = append(statuses, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate source statuses: %w", err)
	}
	return statuses, nil
}
