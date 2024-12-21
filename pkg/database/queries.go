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

// Package database is responsible for handling the database
// setup, configuration and abstraction.
package database

import (
	"database/sql"
	"fmt"
	"strings"

	cmn "github.com/pzaino/thecrowler/pkg/common"
)

// GetSourceID retrieves the source ID from the database based on the provided filter.
func GetSourceID(filter SourceFilter, db *Handler) (uint64, error) {
	var sourceID int64 // Use int64 here since PostgreSQL BIGSERIAL maps to int64
	var whereClauses []string
	var args []interface{}
	parID := 1

	// Dynamically build the WHERE clause based on the input struct
	if filter.URL != "" {
		whereClauses = append(whereClauses, "url = $"+fmt.Sprint(parID))
		args = append(args, cmn.NormalizeURL(filter.URL))
		parID++
	}
	if filter.SourceID > 0 {
		whereClauses = append(whereClauses, "source_id = $"+fmt.Sprint(parID))
		args = append(args, filter.SourceID)
	}

	if len(whereClauses) == 0 {
		return 0, fmt.Errorf("at least one filter (URL or SourceID) must be provided")
	}

	query := fmt.Sprintf(`
        SELECT source_id
        FROM Sources
        WHERE %s
        LIMIT 1
    `, strings.Join(whereClauses, " AND "))

	// Query the database
	err := (*db).QueryRow(query, args...).Scan(&sourceID)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("no source found matching the provided filters")
	} else if err != nil {
		return 0, fmt.Errorf("error querying the source ID: %w", err)
	}

	// Ensure the value is non-negative
	if sourceID < 0 {
		return 0, fmt.Errorf("invalid source ID retrieved from database: %d", sourceID)
	}

	// Convert to uint64 (safe because of the check above)
	return uint64(sourceID), nil //nolint:gosec // This is a read-only operation and the int64 is never negative
}
