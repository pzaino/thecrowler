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

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
)

// SourceStatusRow represents the source status fields exposed by status lookups.
type SourceStatusRow struct {
	SourceID      uint64
	SourceUID     string
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
	EmailStatus   *SourceEmailStatusRow
}

// SourceEmailStatusRow is a source-level, privacy-safe summary of durable email
// ingestion state. Cursor values, mailbox identifiers, raw errors, message
// content, and credentials are deliberately excluded.
type SourceEmailStatusRow struct {
	ListenerStatus        string
	LastSynchronizedAt    sql.NullString
	MailboxCount          uint64
	CheckpointedMailboxes uint64
	HasTokenCursor        bool
	HasHistoryCursor      bool
	HasUIDCursor          bool
	ProcessedCount        uint64
	FailedCount           uint64
	LastErrorCategory     string
}

const sourceStatusSelect = `
	SELECT source_id,
	       source_uid,
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

// GetSourceStatusByUID retrieves the source status row matching sourceUID.
func GetSourceStatusByUID(db *Handler, sourceUID string) ([]SourceStatusRow, error) {
	if db == nil || *db == nil {
		return nil, fmt.Errorf("database handler is nil")
	}

	query := sourceStatusSelect + "\n\tWHERE source_uid = " + sourceStatusPlaceholder(db)
	rows, err := (*db).ExecuteQuery(query, sourceUID)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve source status by UID: %w", err)
	}
	defer rows.Close() //nolint:errcheck // We can't check return value on defer

	statuses, err := scanSourceStatusRows(rows)
	if err != nil {
		return nil, err
	}
	if err := attachSourceEmailStatuses(db, statuses); err != nil {
		return nil, err
	}
	return statuses, nil
}

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

	statuses, err := scanSourceStatusRows(rows)
	if err != nil {
		return nil, err
	}
	if err := attachSourceEmailStatuses(db, statuses); err != nil {
		return nil, err
	}
	return statuses, nil
}

// ListSourceStatusesByURLFilter retrieves source status rows whose URL contains urlFilter.
// The filter is applied directly as a SQL LIKE contains match.
func ListSourceStatusesByURLFilter(db *Handler, urlFilter string) ([]SourceStatusRow, error) {
	if db == nil || *db == nil {
		return nil, fmt.Errorf("database handler is nil")
	}

	filter := strings.TrimSpace(urlFilter)
	query := sourceStatusSelect + "\n\tWHERE url LIKE " + sourceStatusPlaceholder(db)
	rows, err := (*db).ExecuteQuery(query, "%"+filter+"%")
	if err != nil {
		return nil, fmt.Errorf("failed to list source statuses by URL filter: %w", err)
	}
	defer rows.Close() //nolint:errcheck // We can't check return value on defer

	statuses, err := scanSourceStatusRows(rows)
	if err != nil {
		return nil, err
	}
	if err := attachSourceEmailStatuses(db, statuses); err != nil {
		return nil, err
	}
	return statuses, nil
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

	statuses, err := scanSourceStatusRows(rows)
	if err != nil {
		return nil, err
	}
	if err := attachSourceEmailStatuses(db, statuses); err != nil {
		return nil, err
	}
	return statuses, nil
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
		if err := rows.Scan(&row.SourceID, &row.SourceUID, &row.URL, &row.Status, &row.Priority, &row.Engine, &row.CreatedAt, &row.LastUpdatedAt, &row.LastCrawledAt, &row.LastError, &row.LastErrorAt, &row.Restricted, &row.Disabled, &row.Flags, &configJSON); err != nil {
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

const sourceEmailStatusSelect = `
	SELECT mailbox.source_id,
	       COUNT(*) AS mailbox_count,
	       SUM(CASE WHEN mailbox.active = TRUE THEN 1 ELSE 0 END) AS active_mailbox_count,
	       MAX(mailbox.last_reconciled_at) AS last_reconciled_at,
	       MAX(mailbox.listener_healthy_at) AS listener_healthy_at,
	       MAX(CASE WHEN COALESCE(mailbox.cursor_token, '') <> '' THEN 1 ELSE 0 END) AS has_token_cursor,
	       MAX(CASE WHEN COALESCE(mailbox.cursor_history_id, '0') NOT IN ('', '0') THEN 1 ELSE 0 END) AS has_history_cursor,
	       MAX(CASE WHEN COALESCE(mailbox.cursor_uid, 0) > 0 OR COALESCE(mailbox.cursor_uid_validity, 0) > 0 THEN 1 ELSE 0 END) AS has_uid_cursor,
	       SUM(CASE WHEN COALESCE(mailbox.cursor_token, '') <> ''
	                     OR COALESCE(mailbox.cursor_history_id, '0') NOT IN ('', '0')
	                     OR COALESCE(mailbox.cursor_uid, 0) > 0
	                     OR COALESCE(mailbox.cursor_uid_validity, 0) > 0 THEN 1 ELSE 0 END) AS checkpointed_mailboxes,
	       COALESCE(messages.processed_count, 0) AS processed_count,
	       COALESCE(messages.failed_count, 0) AS failed_count
	FROM EmailMailboxState mailbox
	LEFT JOIN (
		SELECT source_id,
		       SUM(CASE WHEN disposition IN ('indexed', 'deleted', 'skipped') THEN 1 ELSE 0 END) AS processed_count,
		       SUM(CASE WHEN disposition = 'quarantined' THEN 1 ELSE 0 END) AS failed_count
		FROM EmailMessageState
		GROUP BY source_id
	) messages ON messages.source_id = mailbox.source_id
	GROUP BY mailbox.source_id, messages.processed_count, messages.failed_count`

const sourceEmailLastErrorsSelect = `
	SELECT source_id, message_status
	FROM EmailMailboxState
	WHERE COALESCE(last_error, '') <> ''
	ORDER BY source_id, last_updated_at DESC`

func attachSourceEmailStatuses(db *Handler, statuses []SourceStatusRow) error {
	if len(statuses) == 0 {
		return nil
	}

	bySourceID := make(map[uint64]*SourceEmailStatusRow, len(statuses))
	rows, err := (*db).ExecuteQuery(sourceEmailStatusSelect)
	if err != nil {
		return fmt.Errorf("failed to retrieve source email status: %w", err)
	}
	for rows.Next() {
		var (
			sourceID           uint64
			status             SourceEmailStatusRow
			activeMailboxCount uint64
			listenerHealthyAt  sql.NullString
			hasTokenCursor     int
			hasHistoryCursor   int
			hasUIDCursor       int
		)
		if err := rows.Scan(
			&sourceID,
			&status.MailboxCount,
			&activeMailboxCount,
			&status.LastSynchronizedAt,
			&listenerHealthyAt,
			&hasTokenCursor,
			&hasHistoryCursor,
			&hasUIDCursor,
			&status.CheckpointedMailboxes,
			&status.ProcessedCount,
			&status.FailedCount,
		); err != nil {
			_ = rows.Close()
			return fmt.Errorf("failed to scan source email status: %w", err)
		}
		status.HasTokenCursor = hasTokenCursor != 0
		status.HasHistoryCursor = hasHistoryCursor != 0
		status.HasUIDCursor = hasUIDCursor != 0
		switch {
		case activeMailboxCount == 0:
			status.ListenerStatus = "stopped"
		case listenerHealthyAt.Valid:
			status.ListenerStatus = "active"
		default:
			status.ListenerStatus = "starting"
		}
		bySourceID[sourceID] = &status
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("failed to iterate source email statuses: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("failed to close source email status rows: %w", err)
	}

	if len(bySourceID) != 0 {
		if err := attachSourceEmailErrorCategories(db, bySourceID); err != nil {
			return err
		}
	}
	for i := range statuses {
		statuses[i].EmailStatus = bySourceID[statuses[i].SourceID]
	}
	return nil
}

func attachSourceEmailErrorCategories(db *Handler, statuses map[uint64]*SourceEmailStatusRow) error {
	rows, err := (*db).ExecuteQuery(sourceEmailLastErrorsSelect)
	if err != nil {
		return fmt.Errorf("failed to retrieve source email error categories: %w", err)
	}
	defer rows.Close() //nolint:errcheck // iteration errors are checked below

	seen := make(map[uint64]struct{}, len(statuses))
	for rows.Next() {
		var sourceID uint64
		var messageStatus sql.NullString
		if err := rows.Scan(&sourceID, &messageStatus); err != nil {
			return fmt.Errorf("failed to scan source email error category: %w", err)
		}
		status, wanted := statuses[sourceID]
		if !wanted {
			continue
		}
		if _, exists := seen[sourceID]; exists {
			continue
		}
		status.LastErrorCategory = sourceEmailErrorCategory(messageStatus.String)
		status.ListenerStatus = "degraded"
		seen[sourceID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed to iterate source email error categories: %w", err)
	}
	return nil
}

func sourceEmailErrorCategory(messageStatus string) string {
	switch messageStatus {
	case "retryable_failure":
		return "transient"
	case "permanent_failure":
		return "permanent"
	default:
		return "unknown"
	}
}
