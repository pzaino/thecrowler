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
	"errors"
	"fmt"
	"strings"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
)

// ErrInformationSeedClaimLost means a worker no longer owns the claim it is
// trying to heartbeat, finalize or release.
var ErrInformationSeedClaimLost = errors.New("information seed claim is no longer owned")

// UpdateInformationSeedStatus updates a seed's lifecycle status and latest error details.
func UpdateInformationSeedStatus(db *Handler, id uint64, status string, errText string) error {
	return UpdateInformationSeedStatusContext(context.Background(), db, id, status, errText)
}

func UpdateInformationSeedStatusContext(ctx context.Context, db *Handler, id uint64, status string, errText string) error {
	return updateInformationSeedStatusContext(ctx, db, id, "", 0, status, errText, nil)
}

// UpdateInformationSeedStatusFencedContext atomically updates a seed only while
// the caller still owns the current claim token and fencing generation. It is
// used by workers for terminal transitions and deliberate claim release.
func UpdateInformationSeedStatusFencedContext(ctx context.Context, db *Handler, id uint64, claimToken string, fencingToken int64, status string, errText string, nextRetryAt *time.Time) error {
	if strings.TrimSpace(claimToken) == "" {
		return fmt.Errorf("claim token is required for fenced status update")
	}
	return updateInformationSeedStatusContext(ctx, db, id, strings.TrimSpace(claimToken), fencingToken, status, errText, nextRetryAt)
}

func updateInformationSeedStatusContext(ctx context.Context, db *Handler, id uint64, claimToken string, fencingToken int64, status string, errText string, nextRetryAt *time.Time) (retErr error) {
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
	if ctx == nil {
		ctx = context.Background()
	}
	errText = strings.TrimSpace(errText)
	var lastError, lastErrorAt interface{}
	if errText != "" {
		lastError = errText
		lastErrorAt = time.Now().UTC()
	}
	dbms := normalizeInformationSeedDBMS((*db).DBMS())
	if !isSupportedInformationSeedDBMS(dbms) {
		return fmt.Errorf("unsupported database type for information seed status update: %s", (*db).DBMS())
	}
	// Bound the transaction (see TransactionContext): legacy callers pass
	// context.Background() here, which would otherwise leave the tx open forever.
	txCtx, cancelTx := TransactionContextForHandler(ctx, db)
	defer cancelTx()
	tx, err := (*db).BeginTx(txCtx, nil)
	if err != nil {
		return NormalizeTransactionError(txCtx, err)
	}
	committed := false
	defer rollbackIfUncommitted(tx, &committed)
	defer func() { retErr = NormalizeTransactionError(txCtx, retErr) }()
	p := newInformationSeedPlaceholders(dbms)
	statusQuery := `SELECT status FROM InformationSeed WHERE information_seed_id = ` + p.Next()
	statusArgs := []interface{}{id}
	if claimToken != "" {
		statusQuery += ` AND claim_token = ` + p.Next() + ` AND fencing_token = ` + p.Next()
		statusArgs = append(statusArgs, claimToken, fencingToken)
	}
	var previous string
	if err = tx.QueryRowContext(ctx, statusQuery, statusArgs...).Scan(&previous); err != nil {
		if errors.Is(err, sql.ErrNoRows) && claimToken != "" {
			return ErrInformationSeedClaimLost
		}
		return fmt.Errorf("no information seed found with ID %d: %w", id, err)
	}
	nowExpr := "CURRENT_TIMESTAMP"
	if dbms == DBPostgresStr {
		nowExpr = "NOW()"
	}
	p = newInformationSeedPlaceholders(dbms)
	clearClaim := status != "processing"
	query := `UPDATE InformationSeed SET status = ` + p.Next() + `, last_error = ` + p.Next() + `, last_error_at = ` + p.Next() + `, last_processed_at = ` + nowExpr
	if clearClaim {
		query += `, claim_token = NULL, lease_expires_at = NULL, heartbeat_at = NULL`
	}
	if status == "error" {
		query += `, next_retry_at = ` + p.Next()
	} else if status == "exhausted" || status == "completed" || status == "pending" || status == "new" {
		query += `, next_retry_at = NULL`
	}
	// The token/fencing pair is authoritative. An operator may disable a row
	// while it is still owned; the owner must be able to write its final state.
	query += ` WHERE information_seed_id = ` + p.Next()
	if dbms != DBMySQLStr {
		query += ` AND deleted_at IS NULL`
	}
	args := []interface{}{status, lastError, lastErrorAt}
	if status == "error" {
		args = append(args, nextRetryAt)
	}
	args = append(args, id)
	if claimToken != "" {
		query += ` AND claim_token = ` + p.Next() + ` AND fencing_token = ` + p.Next()
		args = append(args, claimToken, fencingToken)
	}
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to update information seed %d status: %w", id, err)
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr == nil && rows == 0 {
		if claimToken != "" {
			return ErrInformationSeedClaimLost
		}
		return fmt.Errorf("no information seed found with ID %d", id)
	}
	seed, err := getInformationSeedByIDTxContext(ctx, tx, dbms, id)
	if err != nil {
		return err
	}
	if normalizedSelectorString(previous) != normalizedSelectorString(status) {
		if err = emitInformationSeedLifecycleObservationsTxContext(ctx, tx, dbms, *seed, "status_changed", previous, status); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// HeartbeatInformationSeedClaim extends a live lease only while the worker
// still owns the claim token and fencing generation.
func HeartbeatInformationSeedClaimContext(ctx context.Context, db *Handler, id uint64, claimToken string, fencingToken int64, leaseDuration time.Duration) error {
	if db == nil || *db == nil {
		return fmt.Errorf("database handler is nil")
	}
	if id == 0 || strings.TrimSpace(claimToken) == "" {
		return fmt.Errorf("information seed ID and claim token are required")
	}
	if leaseDuration <= 0 {
		return fmt.Errorf("lease duration must be positive")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	dbms := normalizeInformationSeedDBMS((*db).DBMS())
	if !isSupportedInformationSeedDBMS(dbms) {
		return fmt.Errorf("unsupported database type for information seed heartbeat: %s", (*db).DBMS())
	}
	now := time.Now().UTC()
	leaseExpires := now.Add(leaseDuration)
	p := newInformationSeedPlaceholders(dbms)
	// Disabled rows are not claimable, but an already-owned row must still be
	// able to extend its lease while it finishes the in-flight run.
	query := `UPDATE InformationSeed SET heartbeat_at = ` + p.Next() + `, lease_expires_at = ` + p.Next() + `
		WHERE information_seed_id = ` + p.Next() + ` AND claim_token = ` + p.Next() + ` AND fencing_token = ` + p.Next() + ` AND status = 'processing'`
	if dbms != DBMySQLStr {
		query += ` AND deleted_at IS NULL`
	}
	result, err := (*db).ExecContext(ctx, query, now, leaseExpires, id, strings.TrimSpace(claimToken), fencingToken)
	if err != nil {
		return fmt.Errorf("heartbeat information seed %d: %w", id, err)
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr == nil && rows == 0 {
		return ErrInformationSeedClaimLost
	}
	return nil
}

// ReleaseInformationSeedClaimContext returns a still-owned seed to pending (or
// a documented retryable error state) using a fresh cleanup context supplied by
// the caller. Graceful shutdown releases do not consume an automatic attempt.
func ReleaseInformationSeedClaimContext(ctx context.Context, db *Handler, id uint64, claimToken string, fencingToken int64, status string, errText string, nextRetryAt *time.Time) error {
	status = strings.TrimSpace(status)
	if status == "" {
		status = "pending"
	}
	if status != "pending" {
		return UpdateInformationSeedStatusFencedContext(ctx, db, id, claimToken, fencingToken, status, errText, nextRetryAt)
	}
	if db == nil || *db == nil {
		return fmt.Errorf("database handler is nil")
	}
	dbms := normalizeInformationSeedDBMS((*db).DBMS())
	if !isSupportedInformationSeedDBMS(dbms) {
		return fmt.Errorf("unsupported database type for information seed release: %s", (*db).DBMS())
	}
	nowExpr := "CURRENT_TIMESTAMP"
	if dbms == DBPostgresStr {
		nowExpr = "NOW()"
	}
	p := newInformationSeedPlaceholders(dbms)
	query := `UPDATE InformationSeed
		SET status = 'pending', attempts = CASE WHEN COALESCE(attempts,0) > 0 THEN attempts - 1 ELSE 0 END,
			last_error = NULL, last_error_at = NULL, next_retry_at = NULL,
			claim_token = NULL, lease_expires_at = NULL, heartbeat_at = NULL,
			last_processed_at = ` + nowExpr + `
		WHERE information_seed_id = ` + p.Next() + ` AND claim_token = ` + p.Next() + ` AND fencing_token = ` + p.Next() + ` AND status = 'processing'`
	if dbms != DBMySQLStr {
		query += ` AND deleted_at IS NULL`
	}
	result, err := (*db).ExecContext(ctx, query, id, strings.TrimSpace(claimToken), fencingToken)
	if err != nil {
		return fmt.Errorf("release information seed claim %d: %w", id, err)
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr == nil && rows == 0 {
		return ErrInformationSeedClaimLost
	}
	return nil
}

// ExhaustExpiredInformationSeedClaimsContext moves abandoned claims that have
// already consumed the automatic attempt ceiling into the terminal exhausted
// state so they cannot remain stranded in processing or pending forever.
func ExhaustExpiredInformationSeedClaimsContext(ctx context.Context, db *Handler, maxAttempts int) error {
	if maxAttempts <= 0 {
		return nil
	}
	if db == nil || *db == nil {
		return fmt.Errorf("database handler is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	dbms := normalizeInformationSeedDBMS((*db).DBMS())
	if !isSupportedInformationSeedDBMS(dbms) {
		return fmt.Errorf("unsupported database type for information seed exhaustion: %s", (*db).DBMS())
	}
	now := time.Now().UTC()
	nowExpr := "CURRENT_TIMESTAMP"
	if dbms == DBPostgresStr {
		nowExpr = "NOW()"
	}
	p := newInformationSeedPlaceholders(dbms)
	query := `UPDATE InformationSeed
		SET status = 'exhausted', claim_token = NULL, lease_expires_at = NULL, heartbeat_at = NULL,
			last_processed_at = ` + nowExpr + `
		WHERE COALESCE(attempts, 0) >= ` + p.Next() + `
		  AND (
			(LOWER(TRIM(status)) = 'processing' AND (lease_expires_at IS NULL OR lease_expires_at < ` + p.Next() + `))
			OR (LOWER(TRIM(status)) IN ('new','pending') AND claim_token IS NULL)
		  )`
	args := []interface{}{maxAttempts, now}
	if dbms != DBMySQLStr {
		query += ` AND deleted_at IS NULL`
	}
	if _, err := (*db).ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("exhaust expired information seed claims: %w", err)
	}
	return nil
}

// ClaimInformationSeeds atomically marks eligible InformationSeed rows as processing for engine.
func ClaimInformationSeeds(db *Handler, limit int, priority string, engine string, processingTimeout, retryAfter time.Duration) ([]InformationSeed, error) {
	return ClaimInformationSeedsContext(context.Background(), db, limit, priority, engine, "", processingTimeout, retryAfter, processingTimeout, 0)
}

// ClaimInformationSeedsContext is the context-aware claim entry point. A
// non-empty claimToken is used for tests and deterministic callers; otherwise a
// new token is generated. A leaseDuration <= 0 falls back to processingTimeout
// for backwards compatibility. maxAttempts <= 0 disables the attempt ceiling.
func ClaimInformationSeedsContext(ctx context.Context, db *Handler, limit int, priority string, engine string, claimToken string, processingTimeout, retryAfter, leaseDuration time.Duration, maxAttempts int) ([]InformationSeed, error) {
	if db == nil || *db == nil {
		return nil, fmt.Errorf("database handler is nil")
	}
	if limit <= 0 {
		return []InformationSeed{}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	priority = strings.TrimSpace(priority)
	engine = strings.TrimSpace(engine)
	if engine == "" {
		return nil, fmt.Errorf("engine is required to claim information seeds")
	}
	claimToken = strings.TrimSpace(claimToken)
	if claimToken == "" {
		claimToken = cmn.GenerateUID()
	}
	if leaseDuration <= 0 {
		leaseDuration = processingTimeout
	}
	if leaseDuration <= 0 {
		leaseDuration = 2 * time.Minute
	}
	now := time.Now().UTC()
	leaseExpires := now.Add(leaseDuration)
	processingBefore := now.Add(-processingTimeout)
	retryBefore := now.Add(-retryAfter)

	switch normalizeInformationSeedDBMS((*db).DBMS()) {
	case DBPostgresStr:
		return claimInformationSeedsPostgres(ctx, db, limit, priority, engine, claimToken, leaseExpires, processingTimeout, retryAfter, maxAttempts)
	case DBMySQLStr:
		return claimInformationSeedsMySQL(ctx, db, limit, priority, engine, claimToken, now, leaseExpires, processingBefore, retryBefore, maxAttempts)
	case DBSQLiteStr:
		return claimInformationSeedsSQLite(ctx, db, limit, priority, engine, claimToken, now, leaseExpires, processingBefore, retryBefore, maxAttempts)
	default:
		return nil, fmt.Errorf("unsupported database type for information seed claims: %s", (*db).DBMS())
	}
}

func claimInformationSeedsPostgres(ctx context.Context, db *Handler, limit int, priority, engine, claimToken string, leaseExpires time.Time, processingTimeout, retryAfter time.Duration, maxAttempts int) (seedsResult []InformationSeed, retErr error) {
	txCtx, cancelTx := TransactionContextForHandler(ctx, db)
	defer cancelTx()
	tx, err := (*db).BeginTx(txCtx, nil)
	if err != nil {
		return nil, NormalizeTransactionError(txCtx, err)
	}
	committed := false
	defer rollbackIfUncommitted(tx, &committed)
	defer func() { retErr = NormalizeTransactionError(txCtx, retErr) }()

	rows, err := tx.QueryContext(txCtx, `
		SELECT `+informationSeedClaimColumns("claimed_seed")+`
		FROM update_informationseed($1, $2, $3, $4, $5, $6, $7, $8) AS claimed_seed
		ORDER BY CASE WHEN LOWER(TRIM(claimed_seed.status)) IN ('new','pending') THEN 0
			WHEN LOWER(TRIM(claimed_seed.status)) = 'processing' THEN 1 ELSE 2 END,
			CASE WHEN LOWER(TRIM(claimed_seed.status)) = 'error' THEN claimed_seed.next_retry_at ELSE claimed_seed.created_at END ASC,
			claimed_seed.created_at ASC, claimed_seed.information_seed_id ASC`,
		limit, priority, engine, postgresIntervalDuration(processingTimeout), postgresIntervalDuration(retryAfter), postgresIntervalDuration(time.Until(leaseExpires)), maxAttempts, claimToken)
	if err != nil {
		return nil, err
	}
	seeds, err := scanInformationSeedClaimRows(rows)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	return seeds, nil
}

func postgresIntervalDuration(duration time.Duration) string {
	if duration < 0 {
		duration = 0
	}
	return fmt.Sprintf("%f seconds", duration.Seconds())
}

func claimInformationSeedsMySQL(ctx context.Context, db *Handler, limit int, priority, engine, claimToken string, claimedAt, leaseExpires, processingBefore, retryBefore time.Time, maxAttempts int) (seedsResult []InformationSeed, retErr error) {
	txCtx, cancelTx := TransactionContextForHandler(ctx, db)
	defer cancelTx()
	tx, err := (*db).BeginTx(txCtx, nil)
	if err != nil {
		return nil, NormalizeTransactionError(txCtx, err)
	}
	committed := false
	defer rollbackIfUncommitted(tx, &committed)
	defer func() { retErr = NormalizeTransactionError(txCtx, retErr) }()

	attemptPredicate := ""
	attemptArgs := []interface{}{}
	if maxAttempts > 0 {
		attemptPredicate = " AND COALESCE(attempts, 0) < ?"
		attemptArgs = append(attemptArgs, maxAttempts)
	}
	priorityClause, priorityArgs := claimPriorityClause(priority)
	selectArgs := []interface{}{claimedAt, processingBefore, claimedAt, retryBefore}
	selectArgs = append(selectArgs, priorityArgs...)
	selectArgs = append(selectArgs, attemptArgs...)
	selectArgs = append(selectArgs, limit)
	rows, err := tx.QueryContext(txCtx, `
		SELECT information_seed_id
		FROM InformationSeed
		WHERE COALESCE(disabled, FALSE) = FALSE
		  AND (
			LOWER(TRIM(status)) IN ('new', 'pending')
			OR (LOWER(TRIM(status)) = 'processing' AND (
				(lease_expires_at IS NOT NULL AND lease_expires_at < ?)
				OR (lease_expires_at IS NULL AND (last_processed_at IS NULL OR last_processed_at < ?))
			))
			OR (LOWER(TRIM(status)) = 'error' AND (
				(next_retry_at IS NOT NULL AND next_retry_at <= ?)
				OR (next_retry_at IS NULL AND (last_error_at IS NULL OR last_error_at < ?))
			))
		  )
		  `+priorityClause+attemptPredicate+`
		ORDER BY CASE WHEN LOWER(TRIM(status)) IN ('new','pending') THEN 0 WHEN LOWER(TRIM(status)) = 'processing' THEN 1 ELSE 2 END,
			CASE WHEN LOWER(TRIM(status)) = 'error' THEN next_retry_at ELSE created_at END ASC,
			created_at ASC, information_seed_id ASC
		LIMIT ?
		FOR UPDATE SKIP LOCKED`, selectArgs...)
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
	args := []interface{}{engine, claimedAt, claimToken, leaseExpires, claimedAt}
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, claimedAt, processingBefore, claimedAt, retryBefore)
	args = append(args, priorityArgs...)
	args = append(args, attemptArgs...)
	_, err = tx.ExecContext(txCtx, fmt.Sprintf(`
		UPDATE InformationSeed
		SET status = 'processing',
			engine = ?,
			last_processed_at = ?,
			attempts = COALESCE(attempts, 0) + 1,
			claim_token = ?,
			fencing_token = COALESCE(fencing_token, 0) + 1,
			lease_expires_at = ?,
			heartbeat_at = ?,
			next_retry_at = NULL
		WHERE information_seed_id IN (%s)
		  AND COALESCE(disabled, FALSE) = FALSE
		  AND (
			LOWER(TRIM(status)) IN ('new', 'pending')
			OR (LOWER(TRIM(status)) = 'processing' AND (
				(lease_expires_at IS NOT NULL AND lease_expires_at < ?)
				OR (lease_expires_at IS NULL AND (last_processed_at IS NULL OR last_processed_at < ?))
			))
			OR (LOWER(TRIM(status)) = 'error' AND (
				(next_retry_at IS NOT NULL AND next_retry_at <= ?)
				OR (next_retry_at IS NULL AND (last_error_at IS NULL OR last_error_at < ?))
			))
		  )
		  %s%s`, placeholders, priorityClause, attemptPredicate), args...)
	if err != nil {
		return nil, err
	}

	seeds, err := selectInformationSeedsByClaimTokenMySQL(txCtx, tx, ids, claimToken)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	return seeds, nil
}

func claimInformationSeedsSQLite(ctx context.Context, db *Handler, limit int, priority, engine, claimToken string, claimedAt, leaseExpires, processingBefore, retryBefore time.Time, maxAttempts int) (seedsResult []InformationSeed, retErr error) {
	txCtx, cancelTx := TransactionContextForHandler(ctx, db)
	defer cancelTx()
	tx, err := (*db).BeginTx(txCtx, nil)
	if err != nil {
		return nil, NormalizeTransactionError(txCtx, err)
	}
	committed := false
	defer rollbackIfUncommitted(tx, &committed)
	defer func() { retErr = NormalizeTransactionError(txCtx, retErr) }()

	attemptPredicate := ""
	if maxAttempts > 0 {
		attemptPredicate = " AND COALESCE(attempts, 0) < ?"
	}
	priorityClause, priorityArgs := claimPriorityClause(priority)
	selectArgs := []interface{}{claimedAt, processingBefore, claimedAt, retryBefore}
	selectArgs = append(selectArgs, priorityArgs...)
	if maxAttempts > 0 {
		selectArgs = append(selectArgs, maxAttempts)
	}
	selectArgs = append(selectArgs, limit)
	_, err = tx.ExecContext(txCtx, `
		UPDATE InformationSeed
		SET status = 'processing',
			engine = ?,
			last_processed_at = ?,
			attempts = COALESCE(attempts, 0) + 1,
			claim_token = ?,
			fencing_token = COALESCE(fencing_token, 0) + 1,
			lease_expires_at = ?,
			heartbeat_at = ?,
			next_retry_at = NULL
		WHERE information_seed_id IN (
			SELECT information_seed_id
			FROM InformationSeed
			WHERE COALESCE(disabled, 0) = 0
			  AND (
				LOWER(TRIM(status)) IN ('new', 'pending')
				OR (LOWER(TRIM(status)) = 'processing' AND (
					(lease_expires_at IS NOT NULL AND lease_expires_at < ?)
					OR (lease_expires_at IS NULL AND (last_processed_at IS NULL OR last_processed_at < ?))
				))
				OR (LOWER(TRIM(status)) = 'error' AND (
					(next_retry_at IS NOT NULL AND next_retry_at <= ?)
					OR (next_retry_at IS NULL AND (last_error_at IS NULL OR last_error_at < ?))
				))
			  )
			  `+priorityClause+attemptPredicate+`
			ORDER BY CASE WHEN LOWER(TRIM(status)) IN ('new','pending') THEN 0 WHEN LOWER(TRIM(status)) = 'processing' THEN 1 ELSE 2 END,
				CASE WHEN LOWER(TRIM(status)) = 'error' THEN next_retry_at ELSE created_at END ASC,
				created_at ASC, information_seed_id ASC
			LIMIT ?
		)`, append([]interface{}{engine, claimedAt, claimToken, leaseExpires, claimedAt}, selectArgs...)...)
	if err != nil {
		return nil, err
	}

	rows, err := tx.QueryContext(txCtx, `
		SELECT `+informationSeedClaimColumns("")+`
		FROM InformationSeed
		WHERE claim_token = ?
		  AND status = 'processing'
		ORDER BY created_at ASC, information_seed_id ASC`, claimToken)
	if err != nil {
		return nil, err
	}
	seeds, err := scanInformationSeedClaimRows(rows)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	return seeds, nil
}

func selectInformationSeedsByClaimTokenMySQL(ctx context.Context, tx *sql.Tx, ids []uint64, claimToken string) ([]InformationSeed, error) {
	placeholders := questionPlaceholders(len(ids))
	args := make([]interface{}, 0, len(ids)+1)
	args = append(args, claimToken)
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`
		SELECT %s
		FROM InformationSeed
		WHERE claim_token = ?
		  AND status = 'processing'
		  AND information_seed_id IN (%s)
		ORDER BY created_at ASC, information_seed_id ASC`, informationSeedClaimColumns(""), placeholders), args...)
	if err != nil {
		return nil, err
	}
	return scanInformationSeedClaimRows(rows)
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

func claimPriorityClause(priority string) (string, []interface{}) {
	parts := strings.Split(priority, ",")
	values := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		value := strings.ToLower(strings.TrimSpace(part))
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	if len(values) == 0 {
		return "", nil
	}
	args := make([]interface{}, len(values))
	for index, value := range values {
		args[index] = value
	}
	return ` AND LOWER(TRIM(priority)) IN (` + questionPlaceholders(len(values)) + `)`, args
}

func questionPlaceholders(count int) string {
	parts := make([]string, count)
	for i := range parts {
		parts[i] = "?"
	}
	return strings.Join(parts, ",")
}
