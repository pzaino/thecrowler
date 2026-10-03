// Copyright 2026 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

const timeSeriesObservationColumns = `observation_id, metric_id, observed_at, effective_at, collected_at,
	source_updated_at, bucket_start, bucket_end, information_seed_id, information_seed_candidate_id,
	source_id, source_information_seed_id, index_id, entity_id, subject_type, subject_id,
	object_type, object_id, correlation_rule_id, correlation_object_type_1, correlation_object_id_1,
	correlation_object_type_2, correlation_object_id_2, value_numeric, value_integer, value_boolean,
	value_text, value_json, value_timestamp, value_hash, series_hash, previous_observation_id, previous_value_hash,
	is_changed, change_type, change_delta_numeric, change_detected_at, dedupe_key, dimensions,
	provenance, provenance_hash, created_at, deleted_at, last_updated_at`

const timeSeriesObservationDimensionChunkSize = 1000

const (
	// Each observation contributes 40 bind parameters. Keeping chunks to 250
	// rows (10,000 parameters) leaves ample room below PostgreSQL's 65,535
	// parameter limit and avoids exceptionally large statements.
	timeSeriesPostgresObservationBatchSize = 250
)

const timeSeriesObservationSavepoint = "timeseries_observation_persistence"

func timeSeriesFailureCanSkip(policy cfg.TimeSeriesFailurePolicy) bool {
	return policy == cfg.TimeSeriesFailureSkip || policy == cfg.TimeSeriesFailureLog || policy == cfg.TimeSeriesFailureLogSkip
}

// insertObservationWithCardinality is intentionally the only persistence path
// that may acquire PostgreSQL cardinality slots. The early dedupe lookup means
// an already persisted key cannot consume capacity or increment references.
func insertObservationWithCardinality(ctx context.Context, tx *sql.Tx, dbms string, o *TimeSeriesObservation, cardinality cfg.TimeSeriesCardinalityConfig, failurePolicy cfg.TimeSeriesFailurePolicy) (TimeSeriesInsertResult, error) {
	useSavepoint := timeSeriesFailureCanSkip(failurePolicy)
	if useSavepoint {
		if _, err := tx.ExecContext(ctx, `SAVEPOINT `+timeSeriesObservationSavepoint); err != nil {
			return TimeSeriesInsertResult{}, fmt.Errorf("create time-series observation savepoint: %w", err)
		}
	}
	rollback := func(cause error) (TimeSeriesInsertResult, error) {
		if useSavepoint {
			if _, err := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT `+timeSeriesObservationSavepoint); err != nil {
				return TimeSeriesInsertResult{}, fmt.Errorf("rollback time-series observation savepoint after %v: %w", cause, err)
			}
			if _, err := tx.ExecContext(ctx, `RELEASE SAVEPOINT `+timeSeriesObservationSavepoint); err != nil {
				return TimeSeriesInsertResult{}, fmt.Errorf("release rolled-back time-series observation savepoint: %w", err)
			}
		}
		return TimeSeriesInsertResult{}, cause
	}
	release := func() error {
		if !useSavepoint {
			return nil
		}
		_, err := tx.ExecContext(ctx, `RELEASE SAVEPOINT `+timeSeriesObservationSavepoint)
		return err
	}

	// Prepare before touching capacity, but inside the savepoint: canonical JSON
	// and series hashing can fail and must leave the outer transaction usable.
	if o == nil {
		return rollback(fmt.Errorf("time-series observation is nil"))
	}
	if cardinality.MaxDimensions > 0 && len(o.Dimensions) > cardinality.MaxDimensions {
		return rollback(fmt.Errorf("%w: dimensions %d exceed limit %d", ErrTimeSeriesValueRejected, len(o.Dimensions), cardinality.MaxDimensions))
	}
	if _, err := prepareTimeSeriesObservationInsert(dbms, o); err != nil {
		return rollback(err)
	}

	p := newInformationSeedPlaceholders(dbms)
	var existing uint64
	err := tx.QueryRowContext(ctx, `SELECT observation_id FROM TimeSeriesObservations WHERE dedupe_key=`+p.Next(), o.DedupeKey).Scan(&existing)
	if err == nil {
		o.ID = existing
		if releaseErr := release(); releaseErr != nil {
			return TimeSeriesInsertResult{}, releaseErr
		}
		return TimeSeriesInsertResult{ObservationID: existing, Duplicate: true}, nil
	}
	if err != sql.ErrNoRows {
		return rollback(fmt.Errorf("detect duplicate time-series observation: %w", err))
	}

	exceeded, err := newTimeSeriesCardinalityMutation(ctx, tx, dbms).admit(o.MetricID, o.Scope, o.Dimensions, cardinality)
	if err != nil {
		return rollback(fmt.Errorf("admit time-series observation cardinality: %w", err))
	}
	if exceeded {
		return rollback(fmt.Errorf("%w: cardinality limit exceeded", ErrTimeSeriesValueRejected))
	}
	result, err := insertTimeSeriesObservationTxContext(ctx, tx, dbms, o)
	if err != nil {
		return rollback(err)
	}
	// A concurrent winner can still turn the insert into a duplicate. Rolling
	// back the unit (rather than trying to identify individual reservations)
	// guarantees that the loser contributes neither slots nor references.
	if result.Duplicate {
		if useSavepoint {
			if _, rollbackErr := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT `+timeSeriesObservationSavepoint); rollbackErr != nil {
				return TimeSeriesInsertResult{}, rollbackErr
			}
			if _, releaseErr := tx.ExecContext(ctx, `RELEASE SAVEPOINT `+timeSeriesObservationSavepoint); releaseErr != nil {
				return TimeSeriesInsertResult{}, releaseErr
			}
		}
		return result, nil
	}
	if err = release(); err != nil {
		return TimeSeriesInsertResult{}, fmt.Errorf("release time-series observation savepoint: %w", err)
	}
	return result, nil
}

// InsertTimeSeriesObservation inserts one fact. A duplicate dedupe_key returns a
// successful result with Duplicate=true and the existing observation ID.
func InsertTimeSeriesObservation(db *Handler, observation *TimeSeriesObservation) (TimeSeriesInsertResult, error) {
	dbms, err := validateTimeSeriesDB(db)
	if err != nil {
		return TimeSeriesInsertResult{}, err
	}
	if observation == nil {
		return TimeSeriesInsertResult{}, fmt.Errorf("time-series observation is nil")
	}
	tx, err := (*db).BeginTx(context.Background(), nil)
	if err != nil {
		return TimeSeriesInsertResult{}, fmt.Errorf("begin time-series observation transaction: %w", err)
	}
	result, err := insertTimeSeriesObservationTx(tx, dbms, observation)
	if err != nil {
		_ = (*db).Rollback(tx)
		return TimeSeriesInsertResult{}, err
	}
	if err = (*db).Commit(tx); err != nil {
		_ = (*db).Rollback(tx)
		return TimeSeriesInsertResult{}, fmt.Errorf("commit time-series observation: %w", err)
	}
	return result, nil
}

// InsertTimeSeriesObservations inserts a batch atomically. Duplicates are
// policy successes; any other error rolls the whole batch back.
func InsertTimeSeriesObservations(db *Handler, observations []TimeSeriesObservation) ([]TimeSeriesInsertResult, error) {
	dbms, err := validateTimeSeriesDB(db)
	if err != nil {
		return nil, err
	}
	if len(observations) == 0 {
		return []TimeSeriesInsertResult{}, nil
	}
	tx, err := (*db).BeginTx(context.Background(), nil)
	if err != nil {
		return nil, fmt.Errorf("begin time-series observation batch: %w", err)
	}
	if dbms == DBPostgresStr {
		results, insertErr := insertPostgresTimeSeriesObservationsTx(context.Background(), tx, observations)
		if insertErr != nil {
			_ = (*db).Rollback(tx)
			return nil, insertErr
		}
		if err = (*db).Commit(tx); err != nil {
			_ = (*db).Rollback(tx)
			return nil, fmt.Errorf("commit time-series observation batch: %w", err)
		}
		return results, nil
	}

	results := make([]TimeSeriesInsertResult, 0, len(observations))
	for i := range observations {
		result, insertErr := insertTimeSeriesObservationTx(tx, dbms, &observations[i])
		if insertErr != nil {
			_ = (*db).Rollback(tx)
			return nil, fmt.Errorf("insert time-series observation batch item %d: %w", i, insertErr)
		}
		results = append(results, result)
	}
	if err = (*db).Commit(tx); err != nil {
		_ = (*db).Rollback(tx)
		return nil, fmt.Errorf("commit time-series observation batch: %w", err)
	}
	return results, nil
}

func insertTimeSeriesObservationTx(tx *sql.Tx, dbms string, o *TimeSeriesObservation) (TimeSeriesInsertResult, error) {
	return insertTimeSeriesObservationTxContext(context.Background(), tx, dbms, o)
}

func insertTimeSeriesObservationTxContext(ctx context.Context, tx *sql.Tx, dbms string, o *TimeSeriesObservation) (TimeSeriesInsertResult, error) {
	args, err := prepareTimeSeriesObservationInsert(dbms, o)
	if err != nil {
		return TimeSeriesInsertResult{}, err
	}
	values := timeSeriesObservationInsertPlaceholders(dbms, len(args), 0)
	query := timeSeriesObservationInsertPrefix + ` VALUES (` + strings.Join(values, ",") + `)`
	if dbms == DBMySQLStr {
		query += ` ON DUPLICATE KEY UPDATE dedupe_key=VALUES(dedupe_key)`
	} else if dbms == DBSQLiteStr {
		query += ` ON CONFLICT (dedupe_key) DO NOTHING`
	}
	if dbms == DBPostgresStr {
		result, insertErr := insertPostgresTimeSeriesObservation(ctx, tx, query+` ON CONFLICT (dedupe_key) DO NOTHING RETURNING observation_id`, args, o)
		if insertErr == nil && result.Inserted {
			insertErr = newTimeSeriesCardinalityMutation(ctx, tx, dbms).add(o)
		} else if insertErr == nil && result.Duplicate {
			insertErr = newTimeSeriesCardinalityMutation(ctx, tx, dbms).releaseUnreferencedReservations(o)
		}
		return result, insertErr
	}
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return TimeSeriesInsertResult{}, fmt.Errorf("insert time-series observation: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return TimeSeriesInsertResult{}, fmt.Errorf("inspect time-series observation insert: %w", err)
	}
	var id uint64
	lookup := `SELECT observation_id FROM TimeSeriesObservations WHERE dedupe_key = ` + informationSeedPlaceholderForDBMS(dbms, 1)
	if err = tx.QueryRowContext(ctx, lookup, o.DedupeKey).Scan(&id); err != nil {
		return TimeSeriesInsertResult{}, fmt.Errorf("lookup inserted time-series observation: %w", err)
	}
	o.ID = id
	insertResult := TimeSeriesInsertResult{ObservationID: id, Inserted: affected == 1, Duplicate: affected != 1}
	if insertResult.Inserted {
		if err = newTimeSeriesCardinalityMutation(ctx, tx, dbms).add(o); err != nil {
			return TimeSeriesInsertResult{}, err
		}
	}
	return insertResult, nil
}

const timeSeriesObservationInsertPrefix = `INSERT INTO TimeSeriesObservations (metric_id, observed_at, effective_at, collected_at, source_updated_at,
		bucket_start, bucket_end, information_seed_id, information_seed_candidate_id, source_id,
		source_information_seed_id, index_id, entity_id, subject_type, subject_id, object_type, object_id,
		correlation_rule_id, correlation_object_type_1, correlation_object_id_1, correlation_object_type_2,
		correlation_object_id_2, value_numeric, value_integer, value_boolean, value_text, value_json,
		value_timestamp, value_hash, series_hash, previous_observation_id, previous_value_hash, is_changed, change_type,
		change_delta_numeric, change_detected_at, dedupe_key, dimensions, provenance, provenance_hash)`

func prepareTimeSeriesObservationInsert(dbms string, o *TimeSeriesObservation) ([]interface{}, error) {
	if o.MetricID == 0 {
		return nil, fmt.Errorf("time-series observation metric ID is required")
	}
	if o.ObservedAt.IsZero() {
		return nil, fmt.Errorf("time-series observation observed_at is required")
	}
	if o.DedupeKey == "" {
		return nil, fmt.Errorf("time-series observation dedupe key is required")
	}
	if o.ValueHash == "" {
		return nil, fmt.Errorf("time-series observation value hash is required")
	}
	seriesHash, err := TimeSeriesSeriesHash(o.MetricID, o.Scope, o.Dimensions)
	if err != nil {
		return nil, fmt.Errorf("hash time-series logical series: %w", err)
	}
	o.SeriesHash = seriesHash
	if o.CollectedAt.IsZero() {
		o.CollectedAt = time.Now().UTC()
	}
	if o.BucketStart.IsZero() || o.BucketEnd.IsZero() {
		return nil, fmt.Errorf("time-series observation bucket bounds are required")
	}
	dimensions, err := optionalCanonicalJSON(o.Dimensions)
	if err != nil {
		return nil, err
	}
	provenance, err := optionalCanonicalRawJSON(o.Provenance)
	if err != nil {
		return nil, err
	}
	valueJSON, err := optionalCanonicalRawJSON(o.Value.JSON)
	if err != nil {
		return nil, err
	}
	args := []interface{}{o.MetricID, o.ObservedAt.UTC(), o.EffectiveAt, o.CollectedAt.UTC(), o.SourceUpdatedAt, o.BucketStart.UTC(), o.BucketEnd.UTC(), o.Scope.InformationSeedID, o.Scope.InformationSeedCandidateID, o.Scope.SourceID, o.Scope.SourceInformationSeedID, o.Scope.IndexID, o.Scope.EntityID, nullableString(o.Scope.SubjectType), o.Scope.SubjectID, nullableString(o.Scope.ObjectType), o.Scope.ObjectID, o.Scope.CorrelationRuleID, nullableString(o.Scope.CorrelationObjectType1), o.Scope.CorrelationObjectID1, nullableString(o.Scope.CorrelationObjectType2), o.Scope.CorrelationObjectID2, o.Value.Numeric, o.Value.Integer, o.Value.Boolean, o.Value.Text, valueJSON, o.Value.Timestamp, o.ValueHash, o.SeriesHash, o.PreviousObservationID, nullableString(o.PreviousValueHash), o.IsChanged, nullableString(o.ChangeType), o.ChangeDeltaNumeric, o.ChangeDetectedAt, o.DedupeKey, dimensions, provenance, nullableString(o.ProvenanceHash)}
	return args, nil
}

func timeSeriesObservationInsertPlaceholders(dbms string, count, offset int) []string {
	values := make([]string, count)
	for i := range values {
		values[i] = informationSeedPlaceholderForDBMS(dbms, offset+i+1)
	}
	if dbms == DBPostgresStr {
		for _, idx := range []int{26, 37, 38} {
			if idx < count {
				values[idx] += "::jsonb"
			}
		}
	}
	return values
}

// insertPostgresTimeSeriesObservationsTx inserts bounded chunks inside the
// caller's transaction. RETURNING identifies newly inserted keys, then one
// set-oriented lookup resolves every conflicting key (including keys repeated
// earlier in this call).
func insertPostgresTimeSeriesObservationsTx(ctx context.Context, tx *sql.Tx, observations []TimeSeriesObservation) ([]TimeSeriesInsertResult, error) {
	results := make([]TimeSeriesInsertResult, len(observations))
	for start := 0; start < len(observations); start += timeSeriesPostgresObservationBatchSize {
		end := start + timeSeriesPostgresObservationBatchSize
		if end > len(observations) {
			end = len(observations)
		}

		args := make([]interface{}, 0, (end-start)*40)
		rowsSQL := make([]string, 0, end-start)
		for i := start; i < end; i++ {
			rowArgs, err := prepareTimeSeriesObservationInsert(DBPostgresStr, &observations[i])
			if err != nil {
				return nil, fmt.Errorf("insert time-series observation batch item %d: %w", i, err)
			}
			placeholders := timeSeriesObservationInsertPlaceholders(DBPostgresStr, len(rowArgs), len(args))
			rowsSQL = append(rowsSQL, `(`+strings.Join(placeholders, ",")+`)`)
			args = append(args, rowArgs...)
		}

		query := timeSeriesObservationInsertPrefix + ` VALUES ` + strings.Join(rowsSQL, ",") +
			` ON CONFLICT (dedupe_key) DO NOTHING RETURNING observation_id, dedupe_key`
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("insert time-series observation batch chunk starting at item %d: %w", start, err)
		}
		insertedIDs := make(map[string]uint64, end-start)
		for rows.Next() {
			var id uint64
			var key string
			if err = rows.Scan(&id, &key); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("scan inserted time-series observation batch chunk: %w", err)
			}
			insertedIDs[key] = id
		}
		if err = rows.Err(); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("read inserted time-series observation batch chunk: %w", err)
		}
		if err = rows.Close(); err != nil {
			return nil, fmt.Errorf("close inserted time-series observation batch chunk: %w", err)
		}

		unresolved := make([]string, 0, end-start)
		seenUnresolved := make(map[string]struct{}, end-start)
		claimedInserted := make(map[string]bool, len(insertedIDs))
		for i := start; i < end; i++ {
			key := observations[i].DedupeKey
			if id, ok := insertedIDs[key]; ok && !claimedInserted[key] {
				results[i] = TimeSeriesInsertResult{ObservationID: id, Inserted: true}
				observations[i].ID = id
				claimedInserted[key] = true
				continue
			}
			if _, ok := seenUnresolved[key]; !ok {
				seenUnresolved[key] = struct{}{}
				unresolved = append(unresolved, key)
			}
		}
		if len(unresolved) == 0 {
			for i := start; i < end; i++ {
				if results[i].Inserted {
					if err = newTimeSeriesCardinalityMutation(ctx, tx, DBPostgresStr).add(&observations[i]); err != nil {
						return nil, err
					}
				}
			}
			continue
		}

		lookupArgs := make([]interface{}, len(unresolved))
		lookupPlaceholders := make([]string, len(unresolved))
		for i, key := range unresolved {
			lookupArgs[i] = key
			lookupPlaceholders[i] = informationSeedPlaceholderForDBMS(DBPostgresStr, i+1)
		}
		lookup := `SELECT observation_id, dedupe_key FROM TimeSeriesObservations WHERE dedupe_key IN (` + strings.Join(lookupPlaceholders, ",") + `)`
		lookupRows, err := tx.QueryContext(ctx, lookup, lookupArgs...)
		if err != nil {
			return nil, fmt.Errorf("lookup existing time-series observation batch chunk: %w", err)
		}
		existingIDs := make(map[string]uint64, len(unresolved))
		for lookupRows.Next() {
			var id uint64
			var key string
			if err = lookupRows.Scan(&id, &key); err != nil {
				_ = lookupRows.Close()
				return nil, fmt.Errorf("scan existing time-series observation batch chunk: %w", err)
			}
			existingIDs[key] = id
		}
		if err = lookupRows.Err(); err != nil {
			_ = lookupRows.Close()
			return nil, fmt.Errorf("read existing time-series observation batch chunk: %w", err)
		}
		if err = lookupRows.Close(); err != nil {
			return nil, fmt.Errorf("close existing time-series observation batch chunk: %w", err)
		}
		for i := start; i < end; i++ {
			if results[i].Inserted {
				continue
			}
			id, ok := existingIDs[observations[i].DedupeKey]
			if !ok {
				return nil, fmt.Errorf("lookup existing time-series observation batch item %d: no row for dedupe key", i)
			}
			results[i] = TimeSeriesInsertResult{ObservationID: id, Duplicate: true}
			observations[i].ID = id
		}
		for i := start; i < end; i++ {
			if results[i].Inserted {
				if err = newTimeSeriesCardinalityMutation(ctx, tx, DBPostgresStr).add(&observations[i]); err != nil {
					return nil, err
				}
			}
		}
	}
	return results, nil
}

// insertPostgresTimeSeriesObservation uses RETURNING so a successful new insert
// needs no follow-up query. Only the conflict/no-row case performs the dedupe
// lookup needed to return the existing observation's identity.
func insertPostgresTimeSeriesObservation(ctx context.Context, tx *sql.Tx, query string, args []interface{}, o *TimeSeriesObservation) (TimeSeriesInsertResult, error) {
	var id uint64
	err := tx.QueryRowContext(ctx, query, args...).Scan(&id)
	if err == nil {
		o.ID = id
		return TimeSeriesInsertResult{ObservationID: id, Inserted: true, Duplicate: false}, nil
	}
	if err != sql.ErrNoRows {
		return TimeSeriesInsertResult{}, fmt.Errorf("insert time-series observation: %w", err)
	}

	lookup := `SELECT observation_id FROM TimeSeriesObservations WHERE dedupe_key = $1`
	if err = tx.QueryRowContext(ctx, lookup, o.DedupeKey).Scan(&id); err != nil {
		return TimeSeriesInsertResult{}, fmt.Errorf("lookup existing time-series observation: %w", err)
	}
	o.ID = id
	return TimeSeriesInsertResult{ObservationID: id, Inserted: false, Duplicate: true}, nil
}

func optionalCanonicalJSON(value map[string]interface{}) (interface{}, error) {
	if value == nil {
		return nil, nil
	}
	raw, err := CanonicalTimeSeriesJSON(value)
	if err != nil {
		return nil, err
	}
	return string(raw), nil
}
func optionalCanonicalRawJSON(value json.RawMessage) (interface{}, error) {
	if len(value) == 0 {
		return nil, nil
	}
	raw, err := CanonicalTimeSeriesJSON(value)
	if err != nil {
		return nil, err
	}
	return string(raw), nil
}

// QueryTimeSeriesObservations queries both seed-associated and direct-source
// observations. Dimension subset matching is performed portably after SQL.
func QueryTimeSeriesObservations(db *Handler, filter TimeSeriesQueryFilter) (TimeSeriesObservationQueryResult, error) {
	return QueryTimeSeriesObservationsContext(context.Background(), db, filter)
}
func QueryTimeSeriesObservationsContext(ctx context.Context, db *Handler, filter TimeSeriesQueryFilter) (TimeSeriesObservationQueryResult, error) {
	dbms, err := validateTimeSeriesDB(db)
	if err != nil {
		return TimeSeriesObservationQueryResult{}, err
	}
	conditions, args, p, err := buildTimeSeriesQueryConditions(dbms, filter, "o")
	if err != nil {
		return TimeSeriesObservationQueryResult{}, err
	}
	limit, offset, err := normalizeTimeSeriesPagination(filter.Pagination)
	if err != nil {
		return TimeSeriesObservationQueryResult{}, err
	}
	sqlLimit := limit + 1
	direction := "ASC"
	if filter.Descending {
		direction = "DESC"
	}
	query := `SELECT ` + prefixColumns(timeSeriesObservationColumns, "o") + ` FROM TimeSeriesObservations o WHERE ` + strings.Join(conditions, " AND ") + ` ORDER BY o.observed_at ` + direction + `, o.observation_id ` + direction
	if len(filter.Dimensions) == 0 {
		query += ` LIMIT ` + p.Next() + ` OFFSET ` + p.Next()
		args = append(args, sqlLimit, offset)
		rows, queryErr := (*db).QueryContext(ctx, query, args...)
		if queryErr != nil {
			return TimeSeriesObservationQueryResult{}, fmt.Errorf("query time-series observations: %w", queryErr)
		}
		defer rows.Close()
		all := []TimeSeriesObservation{}
		for rows.Next() {
			o, scanErr := scanTimeSeriesObservation(rows.Scan)
			if scanErr != nil {
				return TimeSeriesObservationQueryResult{}, scanErr
			}
			if dimensionsContain(o.Dimensions, filter.Dimensions) {
				all = append(all, *o)
			}
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			return TimeSeriesObservationQueryResult{}, rowsErr
		}
		hasMore := len(all) > limit
		if hasMore {
			all = all[:limit]
		}
		return TimeSeriesObservationQueryResult{Observations: all, Count: len(all), HasMore: hasMore}, nil
	}

	baseQuery := `SELECT ` + prefixColumns(timeSeriesObservationColumns, "o") + ` FROM TimeSeriesObservations o WHERE ` + strings.Join(conditions, " AND ")
	matches := make([]TimeSeriesObservation, 0, limit+1)
	matchingOffset := offset
	var cursorObservedAt time.Time
	var cursorObservationID uint64
	hasCursor := false

	for len(matches) < limit+1 {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return TimeSeriesObservationQueryResult{}, ctxErr
		}

		chunkPlaceholders := newInformationSeedPlaceholders(dbms)
		for range args {
			chunkPlaceholders.Next()
		}
		chunkQuery := baseQuery
		chunkArgs := append([]interface{}{}, args...)
		if hasCursor {
			comparison := ">"
			if filter.Descending {
				comparison = "<"
			}
			observedAtComparisonPlaceholder := chunkPlaceholders.Next()
			observedAtEqualityPlaceholder := chunkPlaceholders.Next()
			observationIDPlaceholder := chunkPlaceholders.Next()
			chunkQuery += ` AND (o.observed_at ` + comparison + ` ` + observedAtComparisonPlaceholder +
				` OR (o.observed_at = ` + observedAtEqualityPlaceholder + ` AND o.observation_id ` + comparison + ` ` + observationIDPlaceholder + `))`
			chunkArgs = append(chunkArgs, cursorObservedAt, cursorObservedAt, cursorObservationID)
		}
		chunkQuery += ` ORDER BY o.observed_at ` + direction + `, o.observation_id ` + direction + ` LIMIT ` + chunkPlaceholders.Next()
		chunkArgs = append(chunkArgs, timeSeriesObservationDimensionChunkSize)

		rows, queryErr := (*db).QueryContext(ctx, chunkQuery, chunkArgs...)
		if queryErr != nil {
			return TimeSeriesObservationQueryResult{}, fmt.Errorf("query time-series observations: %w", queryErr)
		}
		rawCount := 0
		for rows.Next() {
			o, scanErr := scanTimeSeriesObservation(rows.Scan)
			if scanErr != nil {
				_ = rows.Close()
				return TimeSeriesObservationQueryResult{}, scanErr
			}
			rawCount++
			cursorObservedAt = o.ObservedAt
			cursorObservationID = o.ID
			hasCursor = true
			if !dimensionsContain(o.Dimensions, filter.Dimensions) {
				continue
			}
			if matchingOffset > 0 {
				matchingOffset--
				continue
			}
			matches = append(matches, *o)
			if len(matches) == limit+1 {
				break
			}
		}
		rowsErr := rows.Err()
		_ = rows.Close()
		if rowsErr != nil {
			return TimeSeriesObservationQueryResult{}, rowsErr
		}
		if len(matches) == limit+1 || rawCount < timeSeriesObservationDimensionChunkSize {
			break
		}
	}

	hasMore := len(matches) > limit
	if hasMore {
		matches = matches[:limit]
	}
	return TimeSeriesObservationQueryResult{Observations: matches, Count: len(matches), HasMore: hasMore}, nil
}

func buildTimeSeriesQueryConditions(dbms string, f TimeSeriesQueryFilter, alias string) ([]string, []interface{}, *informationSeedPlaceholders, error) {
	p := newInformationSeedPlaceholders(dbms)
	c := []string{"1=1"}
	a := []interface{}{}
	col := func(name string) string { return alias + "." + name }
	add := func(name string, value interface{}) { c = append(c, col(name)+" = "+p.Next()); a = append(a, value) }
	if !f.IncludeDeleted {
		c = append(c, col("deleted_at")+" IS NULL")
	}
	if f.MetricID != nil {
		add("metric_id", *f.MetricID)
	}
	if f.SeriesHash != "" {
		add("series_hash", f.SeriesHash)
	}
	if f.MetricKey != "" {
		c = append(c, col("metric_id")+" = (SELECT metric_id FROM TimeSeriesMetrics WHERE metric_key = "+p.Next()+" AND deleted_at IS NULL)")
		a = append(a, f.MetricKey)
	}
	if f.InformationSeedID != nil {
		add("information_seed_id", *f.InformationSeedID)
	}
	if f.InformationSeedCandidateID != nil {
		add("information_seed_candidate_id", *f.InformationSeedCandidateID)
	}
	if f.SourceID != nil {
		add("source_id", *f.SourceID)
	}
	if f.SourceInformationSeedID != nil {
		add("source_information_seed_id", *f.SourceInformationSeedID)
	}
	if f.IndexID != nil {
		add("index_id", *f.IndexID)
	}
	if f.EntityID != nil {
		add("entity_id", *f.EntityID)
	}
	if f.SubjectType != "" {
		add("subject_type", f.SubjectType)
	}
	if f.SubjectID != nil {
		add("subject_id", *f.SubjectID)
	}
	if f.SubjectText != "" {
		add("subject_text", f.SubjectText)
	}
	if f.ObjectType != "" {
		add("object_type", f.ObjectType)
	}
	if f.ObjectID != nil {
		add("object_id", *f.ObjectID)
	}
	if f.CorrelationRuleID != nil {
		add("correlation_rule_id", *f.CorrelationRuleID)
	}
	if f.CorrelationObjectType1 != "" {
		add("correlation_object_type_1", f.CorrelationObjectType1)
	}
	if f.CorrelationObjectID1 != nil {
		add("correlation_object_id_1", *f.CorrelationObjectID1)
	}
	if f.CorrelationObjectType2 != "" {
		add("correlation_object_type_2", f.CorrelationObjectType2)
	}
	if f.CorrelationObjectID2 != nil {
		add("correlation_object_id_2", *f.CorrelationObjectID2)
	}
	timeColumn := "observed_at"
	switch f.TimeBasis {
	case "", cfg.TimeSeriesTimeObservedAt:
	case cfg.TimeSeriesTimeEventAt:
		timeColumn = "effective_at"
	case cfg.TimeSeriesTimeSourceTimestamp:
		timeColumn = "source_updated_at"
	default:
		return nil, nil, nil, fmt.Errorf("unsupported time-series time basis %q", f.TimeBasis)
	}
	if f.Start != nil {
		c = append(c, col(timeColumn)+" >= "+p.Next())
		a = append(a, f.Start.UTC())
	}
	if f.End != nil {
		c = append(c, col(timeColumn)+" < "+p.Next())
		a = append(a, f.End.UTC())
	}
	if f.BucketStart != nil {
		c = append(c, col("bucket_start")+" >= "+p.Next())
		a = append(a, f.BucketStart.UTC())
	}
	if f.BucketEnd != nil {
		c = append(c, col("bucket_end")+" <= "+p.Next())
		a = append(a, f.BucketEnd.UTC())
	}
	if f.Bucket != "" {
		c = append(c, "EXISTS (SELECT 1 FROM TimeSeriesMetrics tm WHERE tm.metric_id = "+col("metric_id")+" AND tm.bucket = "+p.Next()+")")
		a = append(a, string(f.Bucket))
	}
	return c, a, p, nil
}

func prefixColumns(columns, alias string) string {
	parts := strings.Split(columns, ",")
	for i := range parts {
		parts[i] = alias + "." + strings.TrimSpace(parts[i])
	}
	return strings.Join(parts, ", ")
}

func scanTimeSeriesObservation(scan func(...interface{}) error) (*TimeSeriesObservation, error) {
	o := &TimeSeriesObservation{}
	var effective, sourceUpdated, valueTimestamp, changeDetected, deleted sql.NullTime
	var ids [15]sql.NullInt64
	var subjectType, objectType, cType1, cType2, valueText, valueJSON, seriesHash, previousHash, changeType, dimensions, provenance, provenanceHash sql.NullString
	var numeric, delta sql.NullFloat64
	var integer sql.NullInt64
	var boolean sql.NullBool
	err := scan(&o.ID, &o.MetricID, &o.ObservedAt, &effective, &o.CollectedAt, &sourceUpdated, &o.BucketStart, &o.BucketEnd, &ids[0], &ids[1], &ids[2], &ids[3], &ids[4], &ids[5], &subjectType, &ids[6], &objectType, &ids[7], &ids[8], &cType1, &ids[9], &cType2, &ids[10], &numeric, &integer, &boolean, &valueText, &valueJSON, &valueTimestamp, &o.ValueHash, &seriesHash, &ids[11], &previousHash, &o.IsChanged, &changeType, &delta, &changeDetected, &o.DedupeKey, &dimensions, &provenance, &provenanceHash, &o.CreatedAt, &deleted, &o.LastUpdatedAt)
	if err != nil {
		return nil, err
	}
	o.EffectiveAt = nullTimePtr(effective)
	o.SourceUpdatedAt = nullTimePtr(sourceUpdated)
	o.Scope.InformationSeedID = nullUintPtr(ids[0])
	o.Scope.InformationSeedCandidateID = nullUintPtr(ids[1])
	o.Scope.SourceID = nullUintPtr(ids[2])
	o.Scope.SourceInformationSeedID = nullUintPtr(ids[3])
	o.Scope.IndexID = nullUintPtr(ids[4])
	o.Scope.EntityID = nullUintPtr(ids[5])
	o.Scope.SubjectType = subjectType.String
	o.Scope.SubjectID = nullUintPtr(ids[6])
	o.Scope.ObjectType = objectType.String
	o.Scope.ObjectID = nullUintPtr(ids[7])
	o.Scope.CorrelationRuleID = nullUintPtr(ids[8])
	o.Scope.CorrelationObjectType1 = cType1.String
	o.Scope.CorrelationObjectID1 = nullUintPtr(ids[9])
	o.Scope.CorrelationObjectType2 = cType2.String
	o.Scope.CorrelationObjectID2 = nullUintPtr(ids[10])
	if numeric.Valid {
		o.Value.Numeric = &numeric.Float64
	}
	if integer.Valid {
		o.Value.Integer = &integer.Int64
	}
	if boolean.Valid {
		o.Value.Boolean = &boolean.Bool
	}
	if valueText.Valid {
		o.Value.Text = &valueText.String
	}
	if valueJSON.Valid {
		o.Value.JSON = json.RawMessage(valueJSON.String)
	}
	o.Value.Timestamp = nullTimePtr(valueTimestamp)
	o.SeriesHash = seriesHash.String
	o.PreviousObservationID = nullUintPtr(ids[11])
	o.PreviousValueHash = previousHash.String
	o.ChangeType = changeType.String
	if delta.Valid {
		o.ChangeDeltaNumeric = &delta.Float64
	}
	o.ChangeDetectedAt = nullTimePtr(changeDetected)
	if dimensions.Valid {
		if err = json.Unmarshal([]byte(dimensions.String), &o.Dimensions); err != nil {
			return nil, err
		}
	}
	if provenance.Valid {
		o.Provenance = json.RawMessage(provenance.String)
	}
	o.ProvenanceHash = provenanceHash.String
	o.DeletedAt = nullTimePtr(deleted)
	return o, nil
}
func nullUintPtr(v sql.NullInt64) *uint64 {
	if !v.Valid {
		return nil
	}
	x := uint64(v.Int64)
	return &x
}
func nullTimePtr(v sql.NullTime) *time.Time {
	if !v.Valid {
		return nil
	}
	x := v.Time
	return &x
}
func dimensionsContain(actual, expected map[string]interface{}) bool {
	if len(expected) == 0 {
		return true
	}
	for key, want := range expected {
		got, ok := actual[key]
		if !ok {
			return false
		}
		a, _ := CanonicalTimeSeriesJSON(got)
		b, _ := CanonicalTimeSeriesJSON(want)
		if string(a) != string(b) {
			return false
		}
	}
	return true
}

// FindPreviousTimeSeriesObservation hides change-state SQL from emitters.
func FindPreviousTimeSeriesObservation(db *Handler, lookup TimeSeriesChangeLookup) (*TimeSeriesObservation, error) {
	dbms, err := validateTimeSeriesDB(db)
	if err != nil {
		return nil, err
	}
	if lookup.MetricID == 0 {
		return nil, fmt.Errorf("time-series change lookup metric ID is required")
	}
	query, args, err := previousTimeSeriesObservationQuery(dbms, lookup)
	if err != nil {
		return nil, err
	}
	observation, err := scanTimeSeriesObservation(func(dest ...interface{}) error {
		return (*db).QueryRowContext(context.Background(), query, args...).Scan(dest...)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTimeSeriesObservationNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lookup previous time-series observation: %w", err)
	}
	return observation, nil
}

// previousTimeSeriesObservationQuery is the single reference query used by
// handler- and transaction-backed emitters. Logically deleted observations are
// deliberately eligible: their presence is what lets emitters classify the
// next value as reappeared. Operational tables must not be joined here because
// their current lifecycle state cannot redefine an historical series.
func previousTimeSeriesObservationQuery(dbms string, lookup TimeSeriesChangeLookup) (string, []interface{}, error) {
	seriesHash, err := TimeSeriesSeriesHash(lookup.MetricID, lookup.Scope, lookup.Dimensions)
	if err != nil {
		return "", nil, err
	}
	timeColumn := "observed_at"
	switch lookup.TimeBasis {
	case "", cfg.TimeSeriesTimeObservedAt:
	case cfg.TimeSeriesTimeEventAt:
		timeColumn = "effective_at"
	case cfg.TimeSeriesTimeSourceTimestamp:
		timeColumn = "source_updated_at"
	default:
		return "", nil, fmt.Errorf("unsupported time-series time basis %q", lookup.TimeBasis)
	}
	p := newInformationSeedPlaceholders(dbms)
	query := `SELECT ` + prefixColumns(timeSeriesObservationColumns, "o") +
		` FROM TimeSeriesObservations o WHERE o.metric_id = ` + p.Next() +
		` AND o.series_hash = ` + p.Next() + ` AND o.` + timeColumn + ` < ` + p.Next() +
		` ORDER BY o.` + timeColumn + ` DESC, o.observation_id DESC LIMIT 1`
	return query, []interface{}{lookup.MetricID, seriesHash, lookup.Before.UTC()}, nil
}
