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
	"time"
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

// CreateInformationSeed inserts a new information seed and returns its database ID.
func CreateInformationSeed(db *Handler, seed *InformationSeed) (uint64, error) {
	if db == nil || *db == nil {
		return 0, fmt.Errorf("database handler is nil")
	}
	if seed == nil {
		return 0, fmt.Errorf("information seed is nil")
	}

	seedText := strings.TrimSpace(seed.InformationSeed)
	if seedText == "" {
		return 0, fmt.Errorf("information seed text is required")
	}
	status := strings.TrimSpace(seed.Status)
	if status == "" {
		status = "new"
	}
	priority := strings.TrimSpace(seed.Priority)
	engine := strings.TrimSpace(seed.Engine)
	config, err := informationSeedConfigString(seed.Config)
	if err != nil {
		return 0, err
	}

	args := []interface{}{seed.CategoryID, seed.UsrID, seedText, status, priority, engine, seed.Disabled, config}

	switch normalizeInformationSeedDBMS((*db).DBMS()) {
	case DBPostgresStr:
		var id uint64
		err = (*db).QueryRow(`
			INSERT INTO InformationSeed (category_id, usr_id, information_seed, status, priority, engine, disabled, config)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb)
			RETURNING information_seed_id`, args...).Scan(&id)
		if err != nil {
			return 0, fmt.Errorf("failed to create PostgreSQL information seed: %w", err)
		}
		return id, nil
	case DBSQLiteStr:
		var id uint64
		err = (*db).QueryRow(`
			INSERT INTO InformationSeed (category_id, usr_id, information_seed, status, priority, engine, disabled, config)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			RETURNING information_seed_id`, args...).Scan(&id)
		if err != nil {
			return 0, fmt.Errorf("failed to create SQLite information seed: %w", err)
		}
		return id, nil
	case DBMySQLStr:
		result, err := (*db).Exec(`
			INSERT INTO InformationSeed (category_id, usr_id, information_seed, status, priority, engine, disabled, config)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, args...)
		if err != nil {
			return 0, fmt.Errorf("failed to create MySQL information seed: %w", err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			return 0, fmt.Errorf("failed to retrieve MySQL information seed ID: %w", err)
		}
		if id < 0 {
			return 0, fmt.Errorf("failed to retrieve MySQL information seed ID: negative ID %d", id)
		}
		return uint64(id), nil
	default:
		return 0, fmt.Errorf("unsupported database type for information seed creation: %s", (*db).DBMS())
	}
}

// GetInformationSeedByID retrieves an information seed by its ID.
func GetInformationSeedByID(db *Handler, id uint64) (*InformationSeed, error) {
	if db == nil || *db == nil {
		return nil, fmt.Errorf("database handler is nil")
	}
	if id == 0 {
		return nil, fmt.Errorf("information seed ID must be provided")
	}

	dbms := normalizeInformationSeedDBMS((*db).DBMS())
	if !isSupportedInformationSeedDBMS(dbms) {
		return nil, fmt.Errorf("unsupported database type for information seed retrieval: %s", (*db).DBMS())
	}
	placeholder := informationSeedPlaceholderForDBMS(dbms, 1)
	row := (*db).QueryRow(fmt.Sprintf(`
		SELECT %s
		FROM InformationSeed
		WHERE information_seed_id = %s`, informationSeedSelectColumns(), placeholder), id)
	seed, err := scanInformationSeedRow(row)
	if err != nil {
		return nil, fmt.Errorf("no information seed found with ID %d: %w", id, err)
	}
	return seed, nil
}

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

// SetInformationSeedDisabled updates whether an information seed is disabled.
func SetInformationSeedDisabled(db *Handler, id uint64, disabled bool) error {
	if db == nil || *db == nil {
		return fmt.Errorf("database handler is nil")
	}
	if id == 0 {
		return fmt.Errorf("information seed ID must be provided")
	}

	dbms := normalizeInformationSeedDBMS((*db).DBMS())
	if !isSupportedInformationSeedDBMS(dbms) {
		return fmt.Errorf("unsupported database type for information seed disabled update: %s", (*db).DBMS())
	}
	p1 := informationSeedPlaceholderForDBMS(dbms, 1)
	p2 := informationSeedPlaceholderForDBMS(dbms, 2)
	query := fmt.Sprintf(`
		UPDATE InformationSeed
		SET disabled = %s
		WHERE information_seed_id = %s`, p1, p2)
	result, err := (*db).Exec(query, disabled, id)
	if err != nil {
		return fmt.Errorf("failed to update information seed %d disabled flag: %w", id, err)
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

// ListInformationSeedsWithStats returns every information seed with the number
// of sources currently linked to it. The count is computed in the database with
// a single LEFT JOIN aggregate so callers do not have to fetch sources one seed
// at a time.
func ListInformationSeedsWithStats(db *Handler) ([]InformationSeedWithStats, error) {
	if db == nil || *db == nil {
		return nil, fmt.Errorf("database handler is nil")
	}

	joinDeletedFilter, err := sourceInformationSeedDeletedAtJoinFilter(db)
	if err != nil {
		return nil, err
	}

	query := fmt.Sprintf(`
		SELECT seed.information_seed_id, seed.created_at, seed.last_updated_at, seed.category_id,
			seed.usr_id, seed.information_seed, seed.status, seed.priority, seed.engine,
			seed.last_processed_at, seed.last_error, seed.last_error_at, seed.disabled,
			seed.attempts, seed.config, COUNT(link.source_id) AS discovered_source_count
		FROM InformationSeed AS seed
		LEFT JOIN SourceInformationSeedIndex AS link
			ON link.information_seed_id = seed.information_seed_id%s
		GROUP BY seed.information_seed_id, seed.created_at, seed.last_updated_at, seed.category_id,
			seed.usr_id, seed.information_seed, seed.status, seed.priority, seed.engine,
			seed.last_processed_at, seed.last_error, seed.last_error_at, seed.disabled,
			seed.attempts, seed.config
		ORDER BY seed.created_at ASC, seed.information_seed_id ASC`, joinDeletedFilter)

	rows, err := (*db).ExecuteQuery(query)
	if err != nil {
		return nil, fmt.Errorf("failed to list information seeds with stats: %w", err)
	}
	return scanInformationSeedWithStatsRows(rows)
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

func rollbackIfUncommitted(tx *sql.Tx, committed *bool) {
	if !*committed {
		_ = tx.Rollback()
	}
}

func questionPlaceholders(count int) string {
	parts := make([]string, count)
	for i := range parts {
		parts[i] = "?"
	}
	return strings.Join(parts, ",")
}

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

	metadataJSON, err := discoveryMetadataString(metadata.DiscoveryMetadata)
	if err != nil {
		return err
	}

	args := []interface{}{
		sourceID,
		informationSeedID,
		nullableArg(metadata.DiscoveryProvider),
		nullableArg(metadata.DiscoveryQuery),
		nullableArg(metadata.DiscoveryRank),
		nullableArg(metadata.CandidateScore),
		nullableArg(metadata.CandidateReason),
		nullableArg(metadataJSON),
	}

	switch normalizeInformationSeedDBMS((*db).DBMS()) {
	case DBPostgresStr:
		_, err = (*db).Exec(`
			INSERT INTO SourceInformationSeedIndex (
				source_id, information_seed_id, discovery_provider, discovery_query,
				discovery_rank, candidate_score, candidate_reason, discovery_metadata
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb)
			ON CONFLICT (source_id, information_seed_id) DO UPDATE SET
				discovery_provider = COALESCE(EXCLUDED.discovery_provider, SourceInformationSeedIndex.discovery_provider),
				discovery_query = COALESCE(EXCLUDED.discovery_query, SourceInformationSeedIndex.discovery_query),
				discovery_rank = COALESCE(EXCLUDED.discovery_rank, SourceInformationSeedIndex.discovery_rank),
				candidate_score = COALESCE(EXCLUDED.candidate_score, SourceInformationSeedIndex.candidate_score),
				candidate_reason = COALESCE(EXCLUDED.candidate_reason, SourceInformationSeedIndex.candidate_reason),
				discovery_metadata = CASE
					WHEN EXCLUDED.discovery_metadata IS NULL THEN SourceInformationSeedIndex.discovery_metadata
					ELSE COALESCE(SourceInformationSeedIndex.discovery_metadata, '{}'::jsonb) || EXCLUDED.discovery_metadata
				END`, args...)
	case DBMySQLStr:
		_, err = (*db).Exec(`
			INSERT INTO SourceInformationSeedIndex (
				source_id, information_seed_id, discovery_provider, discovery_query,
				discovery_rank, candidate_score, candidate_reason, discovery_metadata
			)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE
				discovery_provider = COALESCE(VALUES(discovery_provider), discovery_provider),
				discovery_query = COALESCE(VALUES(discovery_query), discovery_query),
				discovery_rank = COALESCE(VALUES(discovery_rank), discovery_rank),
				candidate_score = COALESCE(VALUES(candidate_score), candidate_score),
				candidate_reason = COALESCE(VALUES(candidate_reason), candidate_reason),
				discovery_metadata = CASE
					WHEN VALUES(discovery_metadata) IS NULL THEN discovery_metadata
					ELSE JSON_MERGE_PATCH(COALESCE(discovery_metadata, JSON_OBJECT()), VALUES(discovery_metadata))
				END`, args...)
	case DBSQLiteStr:
		_, err = (*db).Exec(`
			INSERT INTO SourceInformationSeedIndex (
				source_id, information_seed_id, discovery_provider, discovery_query,
				discovery_rank, candidate_score, candidate_reason, discovery_metadata
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, json($8))
			ON CONFLICT (source_id, information_seed_id) DO UPDATE SET
				discovery_provider = COALESCE(excluded.discovery_provider, SourceInformationSeedIndex.discovery_provider),
				discovery_query = COALESCE(excluded.discovery_query, SourceInformationSeedIndex.discovery_query),
				discovery_rank = COALESCE(excluded.discovery_rank, SourceInformationSeedIndex.discovery_rank),
				candidate_score = COALESCE(excluded.candidate_score, SourceInformationSeedIndex.candidate_score),
				candidate_reason = COALESCE(excluded.candidate_reason, SourceInformationSeedIndex.candidate_reason),
				discovery_metadata = CASE
					WHEN excluded.discovery_metadata IS NULL THEN SourceInformationSeedIndex.discovery_metadata
					ELSE json_patch(COALESCE(SourceInformationSeedIndex.discovery_metadata, '{}'), excluded.discovery_metadata)
				END`, args...)
	default:
		return fmt.Errorf("unsupported database type for source/information-seed links: %s", (*db).DBMS())
	}
	if err != nil {
		return fmt.Errorf("failed to link source %d to information seed %d with discovery metadata: %w", sourceID, informationSeedID, err)
	}
	return nil
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
