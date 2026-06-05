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

// ListInformationSeeds retrieves information seeds matching the supplied filter.
func ListInformationSeeds(db *Handler, filter InformationSeedFilter) ([]InformationSeed, error) {
	if db == nil || *db == nil {
		return nil, fmt.Errorf("database handler is nil")
	}

	dbms := normalizeInformationSeedDBMS((*db).DBMS())
	if !isSupportedInformationSeedDBMS(dbms) {
		return nil, fmt.Errorf("unsupported database type for information seed listing: %s", (*db).DBMS())
	}
	placeholders := newInformationSeedPlaceholders(dbms)
	query := fmt.Sprintf("SELECT %s FROM InformationSeed", informationSeedSelectColumns())
	conditions := []string{}
	args := []interface{}{}

	if filter.ID != 0 {
		conditions = append(conditions, "information_seed_id = "+placeholders.Next())
		args = append(args, filter.ID)
	}
	if strings.TrimSpace(filter.Status) != "" {
		conditions = append(conditions, "status = "+placeholders.Next())
		args = append(args, strings.TrimSpace(filter.Status))
	}
	if strings.TrimSpace(filter.Priority) != "" {
		conditions = append(conditions, "priority = "+placeholders.Next())
		args = append(args, strings.TrimSpace(filter.Priority))
	}
	if filter.Disabled != nil {
		conditions = append(conditions, "disabled = "+placeholders.Next())
		args = append(args, *filter.Disabled)
	}
	if filter.CategoryID != nil {
		conditions = append(conditions, "category_id = "+placeholders.Next())
		args = append(args, *filter.CategoryID)
	}
	userID := filter.UsrID
	if userID == nil {
		userID = filter.UserID
	}
	if userID != nil {
		conditions = append(conditions, "usr_id = "+placeholders.Next())
		args = append(args, *userID)
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY created_at ASC, information_seed_id ASC"
	if filter.Limit < 0 || filter.Offset < 0 {
		return nil, fmt.Errorf("limit and offset must be non-negative")
	}
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
		return nil, fmt.Errorf("failed to list information seeds: %w", err)
	}
	return scanInformationSeedRows(rows)
}

// ListInformationSeedsWithStats returns information seeds matching the supplied
// filter with the number of sources currently linked to each seed. The count is
// computed in the database with a single LEFT JOIN aggregate so callers do not
// have to fetch sources one seed at a time.
func ListInformationSeedsWithStats(db *Handler, filters ...InformationSeedFilter) ([]InformationSeedWithStats, error) {
	if db == nil || *db == nil {
		return nil, fmt.Errorf("database handler is nil")
	}

	filter := InformationSeedFilter{}
	if len(filters) > 0 {
		filter = filters[0]
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
		return nil, fmt.Errorf("unsupported database type for information seed listing with stats: %s", (*db).DBMS())
	}
	placeholders := newInformationSeedPlaceholders(dbms)
	conditions := []string{}
	args := []interface{}{}
	if filter.ID != 0 {
		conditions = append(conditions, "seed.information_seed_id = "+placeholders.Next())
		args = append(args, filter.ID)
	}
	if strings.TrimSpace(filter.Status) != "" {
		conditions = append(conditions, "seed.status = "+placeholders.Next())
		args = append(args, strings.TrimSpace(filter.Status))
	}
	if strings.TrimSpace(filter.Priority) != "" {
		conditions = append(conditions, "seed.priority = "+placeholders.Next())
		args = append(args, strings.TrimSpace(filter.Priority))
	}
	if filter.Disabled != nil {
		conditions = append(conditions, "seed.disabled = "+placeholders.Next())
		args = append(args, *filter.Disabled)
	}
	if filter.CategoryID != nil {
		conditions = append(conditions, "seed.category_id = "+placeholders.Next())
		args = append(args, *filter.CategoryID)
	}
	userID := filter.UsrID
	if userID == nil {
		userID = filter.UserID
	}
	if userID != nil {
		conditions = append(conditions, "seed.usr_id = "+placeholders.Next())
		args = append(args, *userID)
	}

	query := fmt.Sprintf(`
		SELECT seed.information_seed_id, seed.created_at, seed.last_updated_at, seed.category_id,
			seed.usr_id, seed.information_seed, seed.status, seed.priority, seed.engine,
			seed.last_processed_at, seed.last_error, seed.last_error_at, seed.disabled,
			seed.attempts, seed.config, COUNT(link.source_id) AS discovered_source_count
		FROM InformationSeed AS seed
		LEFT JOIN SourceInformationSeedIndex AS link
			ON link.information_seed_id = seed.information_seed_id%s`, joinDeletedFilter)
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += `
		GROUP BY seed.information_seed_id, seed.created_at, seed.last_updated_at, seed.category_id,
			seed.usr_id, seed.information_seed, seed.status, seed.priority, seed.engine,
			seed.last_processed_at, seed.last_error, seed.last_error_at, seed.disabled,
			seed.attempts, seed.config
		ORDER BY seed.created_at ASC, seed.information_seed_id ASC`
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
		return nil, fmt.Errorf("failed to list information seeds with stats: %w", err)
	}
	return scanInformationSeedWithStatsRows(rows)
}

func scanInformationSeedWithStatsRows(rows *sql.Rows) ([]InformationSeedWithStats, error) {
	defer rows.Close()
	seeds := []InformationSeedWithStats{}
	for rows.Next() {
		var seed InformationSeedWithStats
		var config sql.NullString
		if err := rows.Scan(
			&seed.ID,
			&seed.CreatedAt,
			&seed.LastUpdatedAt,
			&seed.CategoryID,
			&seed.UsrID,
			&seed.InformationSeed.InformationSeed,
			&seed.Status,
			&seed.Priority,
			&seed.Engine,
			&seed.LastProcessedAt,
			&seed.LastError,
			&seed.LastErrorAt,
			&seed.Disabled,
			&seed.Attempts,
			&config,
			&seed.DiscoveredSourceCount,
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
