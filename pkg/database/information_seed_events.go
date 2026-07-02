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
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// InformationSeedEventFilter describes pagination for information-seed event lookups.
type InformationSeedEventFilter struct {
	Limit  int
	Offset int
}

// ListInformationSeedEvents returns discovery events emitted for an information seed.
func ListInformationSeedEvents(db *Handler, seedID uint64, filter InformationSeedEventFilter) ([]Event, error) {
	if db == nil || *db == nil {
		return nil, fmt.Errorf("database handler is nil")
	}
	if seedID == 0 {
		return nil, fmt.Errorf("information seed ID must be provided")
	}
	if filter.Limit < 0 {
		return nil, fmt.Errorf("limit must be non-negative")
	}
	if filter.Offset < 0 {
		return nil, fmt.Errorf("offset must be non-negative")
	}
	if filter.Limit == 0 {
		filter.Limit = 100
	}

	detailsColumn, err := informationSeedEventDetailsColumn(db)
	if err != nil {
		return nil, err
	}
	dbms := normalizeInformationSeedDBMS((*db).DBMS())
	if !isSupportedInformationSeedDBMS(dbms) {
		return nil, fmt.Errorf("unsupported database type for information seed event listing: %s", (*db).DBMS())
	}
	p1 := informationSeedPlaceholderForDBMS(dbms, 1)
	p2 := informationSeedPlaceholderForDBMS(dbms, 2)
	p3 := informationSeedPlaceholderForDBMS(dbms, 3)

	var seedPredicate string
	switch dbms {
	case DBPostgresStr:
		seedPredicate = fmt.Sprintf("details->>'information_seed_id' = %s", p1)
	case DBMySQLStr:
		seedPredicate = fmt.Sprintf("JSON_UNQUOTE(JSON_EXTRACT(%s, '$.information_seed_id')) = %s", detailsColumn, p1)
	case DBSQLiteStr:
		seedPredicate = fmt.Sprintf("CAST(json_extract(%s, '$.information_seed_id') AS TEXT) = %s", detailsColumn, p1)
	}

	query := fmt.Sprintf(`
		SELECT event_sha256, source_id, event_type, event_severity, event_timestamp, %s
		FROM Events
		WHERE event_type LIKE 'information_seed.%%'
		  AND %s
		ORDER BY event_timestamp DESC, event_sha256 DESC
		LIMIT %s OFFSET %s`, detailsColumn, seedPredicate, p2, p3)
	rows, err := (*db).QueryContext(context.Background(), query, fmt.Sprintf("%d", seedID), filter.Limit, filter.Offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list information seed events: %w", err)
	}
	return scanInformationSeedEventRows(rows)
}

func informationSeedEventDetailsColumn(db *Handler) (string, error) {
	exists, err := tableColumnExists(db, "Events", "details")
	if err != nil {
		return "", err
	}
	if exists {
		return "details", nil
	}
	exists, err = tableColumnExists(db, "Events", "event_details")
	if err != nil {
		return "", err
	}
	if exists {
		return "event_details", nil
	}
	return "", fmt.Errorf("Events details column not found")
}

func scanInformationSeedEventRows(rows *sql.Rows) ([]Event, error) {
	defer rows.Close()
	events := []Event{}
	for rows.Next() {
		var event Event
		var sourceID sql.NullInt64
		var details []byte
		if err := rows.Scan(&event.ID, &sourceID, &event.Type, &event.Severity, &event.Timestamp, &details); err != nil {
			return nil, err
		}
		if sourceID.Valid && sourceID.Int64 > 0 {
			event.SourceID = uint64(sourceID.Int64)
		}
		if len(details) > 0 {
			if err := json.Unmarshal(details, &event.Details); err != nil {
				return nil, fmt.Errorf("failed to decode event details: %w", err)
			}
		} else {
			event.Details = make(map[string]interface{})
		}
		events = append(events, event)
	}
	return events, rows.Err()
}
