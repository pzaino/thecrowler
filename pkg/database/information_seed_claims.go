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
	"fmt"
	"strings"
	"time"
)

// UpdateInformationSeedStatus updates a seed's lifecycle status and latest error details.
func UpdateInformationSeedStatus(db *Handler, id uint64, status string, errText string) error {
	if db == nil || *db == nil {
		return fmt.Errorf("database handler is nil")
	}
	if id == 0 {
		return fmt.Errorf("information seed ID must be provided")
	}
	status = strings.TrimSpace(status)
	if status == "" {
		return fmt.Errorf("status is required")
	}

	errText = strings.TrimSpace(errText)
	var lastError interface{}
	var lastErrorAt interface{}
	if errText != "" {
		lastError = errText
		lastErrorAt = time.Now().UTC()
	}

	dbms := normalizeInformationSeedDBMS((*db).DBMS())
	if !isSupportedInformationSeedDBMS(dbms) {
		return fmt.Errorf("unsupported database type for information seed status update: %s", (*db).DBMS())
	}
	nowExpr := "CURRENT_TIMESTAMP"
	if dbms == DBPostgresStr {
		nowExpr = "NOW()"
	}
	p1 := informationSeedPlaceholderForDBMS(dbms, 1)
	p2 := informationSeedPlaceholderForDBMS(dbms, 2)
	p3 := informationSeedPlaceholderForDBMS(dbms, 3)
	p4 := informationSeedPlaceholderForDBMS(dbms, 4)
	query := fmt.Sprintf(`
		UPDATE InformationSeed
		SET status = %s,
			last_error = %s,
			last_error_at = %s,
			last_processed_at = %s
		WHERE information_seed_id = %s`, p1, p2, p3, nowExpr, p4)
	result, err := (*db).Exec(query, status, lastError, lastErrorAt, id)
	if err != nil {
		return fmt.Errorf("failed to update information seed %d status: %w", id, err)
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr == nil && rows == 0 {
		return fmt.Errorf("no information seed found with ID %d", id)
	}
	return nil
}

// ClaimInformationSeeds atomically marks eligible InformationSeed rows as processing for engine.
//
// Eligible seeds are enabled rows whose status is new or pending, processing rows whose
// last_processed_at is older than processingTimeout, and error rows whose last_error_at is
// older than retryAfter. When priority is non-empty, only seeds matching that priority are
// eligible. The implementation deliberately branches by DBMS because row-locking and
// UPDATE ... RETURNING support differ across supported databases.
func ClaimInformationSeeds(db *Handler, limit int, priority string, engine string, processingTimeout, retryAfter time.Duration) ([]InformationSeed, error) {
	if db == nil || *db == nil {
		return nil, fmt.Errorf("database handler is nil")
	}
	if limit <= 0 {
		return []InformationSeed{}, nil
	}
	priority = strings.TrimSpace(priority)
	engine = strings.TrimSpace(engine)
	if engine == "" {
		return nil, fmt.Errorf("engine is required to claim information seeds")
	}

	now := time.Now().UTC()
	processingBefore := now.Add(-processingTimeout)
	retryBefore := now.Add(-retryAfter)

	switch normalizeInformationSeedDBMS((*db).DBMS()) {
	case DBPostgresStr:
		return claimInformationSeedsPostgres(db, limit, priority, engine, now, processingBefore, retryBefore)
	case DBMySQLStr:
		return claimInformationSeedsMySQL(db, limit, priority, engine, now, processingBefore, retryBefore)
	case DBSQLiteStr:
		return claimInformationSeedsSQLite(db, limit, priority, engine, now, processingBefore, retryBefore)
	default:
		return nil, fmt.Errorf("unsupported database type for information seed claims: %s", (*db).DBMS())
	}
}

func claimInformationSeedsPostgres(db *Handler, limit int, priority string, engine string, claimedAt, processingBefore, retryBefore time.Time) ([]InformationSeed, error) {
	tx, err := (*db).Begin()
	if err != nil {
		return nil, err
	}
	committed := false
	defer rollbackIfUncommitted(tx, &committed)

	rows, err := tx.Query(`
		WITH selected AS (
			SELECT information_seed_id
			FROM InformationSeed
			WHERE COALESCE(disabled, FALSE) = FALSE
			  AND (
				LOWER(TRIM(status)) IN ('new', 'pending')
				OR (LOWER(TRIM(status)) = 'processing' AND (last_processed_at IS NULL OR last_processed_at < $1))
				OR (LOWER(TRIM(status)) = 'error' AND (last_error_at IS NULL OR last_error_at < $2))
			  )
			  AND ($3 = '' OR priority = $3)
			ORDER BY created_at ASC, information_seed_id ASC
			FOR UPDATE SKIP LOCKED
			LIMIT $4
		)
		UPDATE InformationSeed AS seed
		SET status = 'processing',
			engine = $5,
			last_processed_at = $6,
			attempts = COALESCE(seed.attempts, 0) + 1
		FROM selected
		WHERE seed.information_seed_id = selected.information_seed_id
		RETURNING seed.information_seed_id, seed.created_at, seed.last_updated_at, seed.category_id,
			seed.usr_id, seed.information_seed, seed.status, seed.priority, seed.engine,
			seed.last_processed_at, seed.last_error, seed.last_error_at, seed.disabled,
			seed.attempts, seed.config`, processingBefore, retryBefore, priority, limit, engine, claimedAt)
	if err != nil {
		return nil, err
	}

	seeds, err := scanInformationSeedRows(rows)
	if err != nil {
		return nil, err
	}

	if err = tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	return seeds, nil
}

func claimInformationSeedsMySQL(db *Handler, limit int, priority string, engine string, claimedAt, processingBefore, retryBefore time.Time) ([]InformationSeed, error) {
	tx, err := (*db).Begin()
	if err != nil {
		return nil, err
	}
	committed := false
	defer rollbackIfUncommitted(tx, &committed)

	rows, err := tx.Query(`
		SELECT information_seed_id
		FROM InformationSeed
		WHERE COALESCE(disabled, FALSE) = FALSE
		  AND (
			LOWER(TRIM(status)) IN ('new', 'pending')
			OR (LOWER(TRIM(status)) = 'processing' AND (last_processed_at IS NULL OR last_processed_at < ?))
			OR (LOWER(TRIM(status)) = 'error' AND (last_error_at IS NULL OR last_error_at < ?))
		  )
		  AND (? = '' OR priority = ?)
		ORDER BY created_at ASC, information_seed_id ASC
		LIMIT ?
		FOR UPDATE SKIP LOCKED`, processingBefore, retryBefore, priority, priority, limit)
	if err != nil {
		return nil, err
	}
	ids, err := scanInformationSeedIDs(rows)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		committed = true
		return []InformationSeed{}, nil
	}

	placeholders := questionPlaceholders(len(ids))
	args := make([]interface{}, 0, len(ids)+5)
	args = append(args, engine, claimedAt)
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, processingBefore, retryBefore, priority, priority)

	_, err = tx.Exec(fmt.Sprintf(`
		UPDATE InformationSeed
		SET status = 'processing',
			engine = ?,
			last_processed_at = ?,
			attempts = COALESCE(attempts, 0) + 1
		WHERE information_seed_id IN (%s)
		  AND COALESCE(disabled, FALSE) = FALSE
		  AND (
			LOWER(TRIM(status)) IN ('new', 'pending')
			OR (LOWER(TRIM(status)) = 'processing' AND (last_processed_at IS NULL OR last_processed_at < ?))
			OR (LOWER(TRIM(status)) = 'error' AND (last_error_at IS NULL OR last_error_at < ?))
		  )
		  AND (? = '' OR priority = ?)`, placeholders), args...)
	if err != nil {
		return nil, err
	}

	seeds, err := selectInformationSeedsByIDsMySQL(tx, ids, engine)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	return seeds, nil
}

func claimInformationSeedsSQLite(db *Handler, limit int, priority string, engine string, claimedAt, processingBefore, retryBefore time.Time) ([]InformationSeed, error) {
	tx, err := (*db).Begin()
	if err != nil {
		return nil, err
	}
	committed := false
	defer rollbackIfUncommitted(tx, &committed)

	_, err = tx.Exec(`
		UPDATE InformationSeed
		SET status = 'processing',
			engine = ?,
			last_processed_at = ?,
			attempts = COALESCE(attempts, 0) + 1
		WHERE information_seed_id IN (
			SELECT information_seed_id
			FROM InformationSeed
			WHERE COALESCE(disabled, 0) = 0
			  AND (
				LOWER(TRIM(status)) IN ('new', 'pending')
				OR (LOWER(TRIM(status)) = 'processing' AND (last_processed_at IS NULL OR last_processed_at < ?))
				OR (LOWER(TRIM(status)) = 'error' AND (last_error_at IS NULL OR last_error_at < ?))
			  )
			  AND (? = '' OR priority = ?)
			ORDER BY created_at ASC, information_seed_id ASC
			LIMIT ?
		)`, engine, claimedAt, processingBefore, retryBefore, priority, priority, limit)
	if err != nil {
		return nil, err
	}

	rows, err := tx.Query(`
		SELECT information_seed_id, created_at, last_updated_at, category_id, usr_id,
			information_seed, status, priority, engine, last_processed_at, last_error,
			last_error_at, disabled, attempts, config
		FROM InformationSeed
		WHERE engine = ?
		  AND status = 'processing'
		  AND last_processed_at = ?
		  AND (? = '' OR priority = ?)
		ORDER BY created_at ASC, information_seed_id ASC`, engine, claimedAt, priority, priority)
	if err != nil {
		return nil, err
	}
	seeds, err := scanInformationSeedRows(rows)
	if err != nil {
		return nil, err
	}

	if err = tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	return seeds, nil
}

func selectInformationSeedsByIDsMySQL(tx *sql.Tx, ids []uint64, engine string) ([]InformationSeed, error) {
	placeholders := questionPlaceholders(len(ids))
	args := make([]interface{}, 0, len(ids)+1)
	args = append(args, engine)
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := tx.Query(fmt.Sprintf(`
		SELECT information_seed_id, created_at, last_updated_at, category_id, usr_id,
			information_seed, status, priority, engine, last_processed_at, last_error,
			last_error_at, disabled, attempts, config
		FROM InformationSeed
		WHERE engine = ?
		  AND status = 'processing'
		  AND information_seed_id IN (%s)
		ORDER BY created_at ASC, information_seed_id ASC`, placeholders), args...)
	if err != nil {
		return nil, err
	}
	return scanInformationSeedRows(rows)
}

func scanInformationSeedIDs(rows *sql.Rows) ([]uint64, error) {
	defer rows.Close()
	ids := []uint64{}
	for rows.Next() {
		var id uint64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func questionPlaceholders(count int) string {
	parts := make([]string, count)
	for i := range parts {
		parts[i] = "?"
	}
	return strings.Join(parts, ",")
}
