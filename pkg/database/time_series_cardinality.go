// Copyright 2026 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

// TimeSeriesCardinalityReconciliation is returned by the administrative
// rebuild. Observations are authoritative; the two other values are the exact
// active sets installed by the operation.
type TimeSeriesCardinalityReconciliation struct {
	Observations    int64
	Series          int64
	DimensionValues int64
}

// LogicallyDeleteTimeSeriesObservation marks a live observation deleted and
// removes its exact references in the same transaction. Repeated calls are
// idempotent.
func LogicallyDeleteTimeSeriesObservation(ctx context.Context, db *Handler, observationID uint64) error {
	dbms, err := validateTimeSeriesDB(db)
	if err != nil {
		return err
	}
	tx, err := (*db).BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	p := newInformationSeedPlaceholders(dbms)
	query := `SELECT ` + timeSeriesObservationColumns + ` FROM TimeSeriesObservations WHERE observation_id=` + p.Next()
	if dbms == DBPostgresStr {
		query += ` FOR UPDATE`
	}
	o, err := scanTimeSeriesObservation(func(dest ...interface{}) error { return tx.QueryRowContext(ctx, query, observationID).Scan(dest...) })
	if err == sql.ErrNoRows {
		return ErrTimeSeriesObservationNotFound
	}
	if err != nil {
		return err
	}
	if o.DeletedAt != nil {
		return tx.Commit()
	}
	if err = decrementTimeSeriesCardinality(ctx, tx, dbms, o); err != nil {
		return err
	}
	p = newInformationSeedPlaceholders(dbms)
	if _, err = tx.ExecContext(ctx, `UPDATE TimeSeriesObservations SET deleted_at=CURRENT_TIMESTAMP,last_updated_at=CURRENT_TIMESTAMP WHERE observation_id=`+p.Next()+` AND deleted_at IS NULL`, observationID); err != nil {
		return err
	}
	return tx.Commit()
}

// TimeSeriesCardinalityExceededTx makes the admission decision from the exact
// active sets. Callers keep this transaction through the eventual insert.
func TimeSeriesCardinalityExceededTx(ctx context.Context, tx *sql.Tx, dbms string, metricID uint64, scope TimeSeriesScope, dimensions map[string]interface{}, policy cfg.TimeSeriesCardinalityConfig) (bool, error) {
	if tx == nil {
		return false, fmt.Errorf("time-series cardinality transaction is nil")
	}
	seriesHash, err := TimeSeriesSeriesHash(metricID, scope, dimensions)
	if err != nil {
		return false, err
	}
	exceeded, missing, err := timeSeriesCardinalityDecision(ctx, tx, dbms, metricID, seriesHash, scope, dimensions, policy)
	if err != nil || exceeded || dbms != DBPostgresStr || !missing {
		return exceeded, err
	}
	// Only new identities need serialization. The transaction-scoped lock is
	// independent of TimeSeriesMetrics, and therefore cannot conflict with the
	// parent-row key locks taken while inserting aggregates. Rechecking after
	// acquiring it makes the count-and-admit decision exact across processes.
	lockKey, err := timeSeriesCardinalityLockKey(metricID)
	if err != nil {
		return false, fmt.Errorf("build PostgreSQL time-series cardinality lock key for metric %d: %w", metricID, err)
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, lockKey); err != nil {
		return false, fmt.Errorf("lock PostgreSQL time-series cardinality for metric %d: %w", metricID, err)
	}
	exceeded, _, err = timeSeriesCardinalityDecision(ctx, tx, dbms, metricID, seriesHash, scope, dimensions, policy)
	return exceeded, err
}

// timeSeriesCardinalityLockKey uses SHA-256 to provide a stable, versioned lock
// namespace. It parses the first 64 bits from the hexadecimal digest and clears
// the high bit to fit PostgreSQL's signed bigint advisory-lock API. Collisions
// are possible only at the resulting 63-bit truncation boundary; they reduce
// concurrency but cannot weaken cardinality correctness.
func timeSeriesCardinalityLockKey(metricID uint64) (int64, error) {
	digest := timeSeriesSHA256("thecrowler:timeseries-cardinality:v1", fmt.Sprintf("metric=%d", metricID))
	raw, err := strconv.ParseUint(digest[:16], 16, 64)
	if err != nil {
		return 0, fmt.Errorf("parse time-series cardinality lock digest: %w", err)
	}
	return int64(raw & 0x7fffffffffffffff), nil
}

func timeSeriesCardinalityDecision(ctx context.Context, tx *sql.Tx, dbms string, metricID uint64, seriesHash string, scope TimeSeriesScope, dimensions map[string]interface{}, policy cfg.TimeSeriesCardinalityConfig) (exceeded, missing bool, err error) {
	if policy.MaxSeriesPerMetric > 0 {
		probe := &TimeSeriesObservation{MetricID: metricID, Scope: scope, Dimensions: dimensions}
		identity, identityErr := timeSeriesSeriesIdentityJSON(probe)
		if identityErr != nil {
			return false, missing, identityErr
		}
		p := newInformationSeedPlaceholders(dbms)
		var stored string
		err = tx.QueryRowContext(ctx, `SELECT series_identity FROM TimeSeriesActiveSeries WHERE metric_id=`+p.Next()+` AND series_hash=`+p.Next(), metricID, seriesHash).Scan(&stored)
		if err != nil && err != sql.ErrNoRows {
			return false, missing, err
		}
		if err == nil && stored != identity {
			return false, missing, fmt.Errorf("time-series series hash collision for metric %d", metricID)
		}
		if err == sql.ErrNoRows {
			missing = true
			var count int
			p = newInformationSeedPlaceholders(dbms)
			if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM TimeSeriesActiveSeries WHERE metric_id=`+p.Next(), metricID).Scan(&count); err != nil {
				return false, missing, err
			}
			if count >= policy.MaxSeriesPerMetric {
				return true, missing, nil
			}
		}
	}
	if policy.MaxValuesPerDimension > 0 {
		keys := make([]string, 0, len(dimensions))
		for key := range dimensions {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			hash, _, hashErr := timeSeriesDimensionValueHash(metricID, key, dimensions[key])
			if hashErr != nil {
				return false, missing, hashErr
			}
			p := newInformationSeedPlaceholders(dbms)
			var storedValue string
			if err = tx.QueryRowContext(ctx, `SELECT canonical_value FROM TimeSeriesActiveDimensionValues WHERE metric_id=`+p.Next()+` AND dimension_key=`+p.Next()+` AND value_hash=`+p.Next(), metricID, key, hash).Scan(&storedValue); err != nil && err != sql.ErrNoRows {
				return false, missing, err
			}
			_, canonical, _ := timeSeriesDimensionValueHash(metricID, key, dimensions[key])
			if err == nil && storedValue != canonical {
				return false, missing, fmt.Errorf("time-series dimension value hash collision for metric %d dimension %q", metricID, key)
			}
			if err == sql.ErrNoRows {
				missing = true
				var count int
				p = newInformationSeedPlaceholders(dbms)
				if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM TimeSeriesActiveDimensionValues WHERE metric_id=`+p.Next()+` AND dimension_key=`+p.Next(), metricID, key).Scan(&count); err != nil {
					return false, missing, err
				}
				if count >= policy.MaxValuesPerDimension {
					return true, missing, nil
				}
			}
		}
	}
	return false, missing, nil
}

func timeSeriesDimensionValueHash(metricID uint64, key string, value interface{}) (string, string, error) {
	raw, err := CanonicalTimeSeriesJSON(value)
	if err != nil {
		return "", "", err
	}
	hash := timeSeriesSHA256("timeseries-dimension-value-v1", fmt.Sprintf("metric=%d", metricID), "key="+key, "value="+string(raw))
	return hash, string(raw), nil
}

func incrementTimeSeriesCardinality(ctx context.Context, tx *sql.Tx, dbms string, o *TimeSeriesObservation) error {
	if o.SeriesHash == "" {
		return fmt.Errorf("time-series cardinality series hash is required")
	}
	seriesIdentity, err := timeSeriesSeriesIdentityJSON(o)
	if err != nil {
		return err
	}
	p := newInformationSeedPlaceholders(dbms)
	var storedIdentity string
	lookupErr := tx.QueryRowContext(ctx, `SELECT series_identity FROM TimeSeriesActiveSeries WHERE metric_id=`+p.Next()+` AND series_hash=`+p.Next(), o.MetricID, o.SeriesHash).Scan(&storedIdentity)
	if lookupErr != nil && lookupErr != sql.ErrNoRows {
		return lookupErr
	}
	if lookupErr == nil && storedIdentity != seriesIdentity {
		return fmt.Errorf("time-series series hash collision for metric %d", o.MetricID)
	}
	seriesQuery := `INSERT INTO TimeSeriesActiveSeries (metric_id, series_hash, series_identity, reference_count) VALUES (` +
		placeholders(dbms, 4) + `)`
	if dbms == DBMySQLStr {
		seriesQuery += ` ON DUPLICATE KEY UPDATE reference_count=reference_count+1`
	} else {
		seriesQuery += ` ON CONFLICT (metric_id, series_hash) DO UPDATE SET reference_count=TimeSeriesActiveSeries.reference_count+1`
	}
	if _, err = tx.ExecContext(ctx, seriesQuery, o.MetricID, o.SeriesHash, seriesIdentity, 1); err != nil {
		return fmt.Errorf("increment time-series active series: %w", err)
	}
	keys := make([]string, 0, len(o.Dimensions))
	for key := range o.Dimensions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		hash, canonical, hashErr := timeSeriesDimensionValueHash(o.MetricID, key, o.Dimensions[key])
		if hashErr != nil {
			return hashErr
		}
		p = newInformationSeedPlaceholders(dbms)
		var storedValue string
		lookupErr = tx.QueryRowContext(ctx, `SELECT canonical_value FROM TimeSeriesActiveDimensionValues WHERE metric_id=`+p.Next()+` AND dimension_key=`+p.Next()+` AND value_hash=`+p.Next(), o.MetricID, key, hash).Scan(&storedValue)
		if lookupErr != nil && lookupErr != sql.ErrNoRows {
			return lookupErr
		}
		if lookupErr == nil && storedValue != canonical {
			return fmt.Errorf("time-series dimension value hash collision for metric %d dimension %q", o.MetricID, key)
		}
		query := `INSERT INTO TimeSeriesActiveDimensionValues (metric_id, dimension_key, value_hash, canonical_value, reference_count) VALUES (` + placeholders(dbms, 5) + `)`
		if dbms == DBMySQLStr {
			query += ` ON DUPLICATE KEY UPDATE reference_count=reference_count+1`
		} else {
			query += ` ON CONFLICT (metric_id, dimension_key, value_hash) DO UPDATE SET reference_count=TimeSeriesActiveDimensionValues.reference_count+1`
		}
		if _, err = tx.ExecContext(ctx, query, o.MetricID, key, hash, canonical, 1); err != nil {
			return fmt.Errorf("increment time-series active dimension value %q: %w", key, err)
		}
	}
	return nil
}

func decrementTimeSeriesCardinality(ctx context.Context, tx *sql.Tx, dbms string, o *TimeSeriesObservation) error {
	p := newInformationSeedPlaceholders(dbms)
	deleted, err := tx.ExecContext(ctx, `DELETE FROM TimeSeriesActiveSeries WHERE metric_id=`+p.Next()+` AND series_hash=`+p.Next()+` AND reference_count=1`, o.MetricID, o.SeriesHash)
	if err != nil {
		return err
	}
	p = newInformationSeedPlaceholders(dbms)
	updated, err := tx.ExecContext(ctx, `UPDATE TimeSeriesActiveSeries SET reference_count=reference_count-1 WHERE metric_id=`+p.Next()+` AND series_hash=`+p.Next()+` AND reference_count>1`, o.MetricID, o.SeriesHash)
	if err != nil {
		return err
	}
	deletedCount, _ := deleted.RowsAffected()
	updatedCount, _ := updated.RowsAffected()
	if deletedCount+updatedCount != 1 {
		return fmt.Errorf("missing exact series reference for observation %d", o.ID)
	}
	keys := make([]string, 0, len(o.Dimensions))
	for key := range o.Dimensions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		hash, _, err := timeSeriesDimensionValueHash(o.MetricID, key, o.Dimensions[key])
		if err != nil {
			return err
		}
		p = newInformationSeedPlaceholders(dbms)
		deleted, err = tx.ExecContext(ctx, `DELETE FROM TimeSeriesActiveDimensionValues WHERE metric_id=`+p.Next()+` AND dimension_key=`+p.Next()+` AND value_hash=`+p.Next()+` AND reference_count=1`, o.MetricID, key, hash)
		if err != nil {
			return err
		}
		p = newInformationSeedPlaceholders(dbms)
		updated, err = tx.ExecContext(ctx, `UPDATE TimeSeriesActiveDimensionValues SET reference_count=reference_count-1 WHERE metric_id=`+p.Next()+` AND dimension_key=`+p.Next()+` AND value_hash=`+p.Next()+` AND reference_count>1`, o.MetricID, key, hash)
		if err != nil {
			return err
		}
		deletedCount, _ = deleted.RowsAffected()
		updatedCount, _ = updated.RowsAffected()
		if deletedCount+updatedCount != 1 {
			return fmt.Errorf("missing exact dimension reference for observation %d dimension %q", o.ID, key)
		}
	}
	return nil
}

func placeholders(dbms string, count int) string {
	p := newInformationSeedPlaceholders(dbms)
	out := ""
	for i := 0; i < count; i++ {
		if i > 0 {
			out += ","
		}
		out += p.Next()
	}
	return out
}

func timeSeriesSeriesIdentityJSON(o *TimeSeriesObservation) (string, error) {
	value := struct {
		Scope      TimeSeriesScope        `json:"scope"`
		Dimensions map[string]interface{} `json:"dimensions"`
	}{o.Scope, o.Dimensions}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	canonical, err := CanonicalTimeSeriesJSON(raw)
	return string(canonical), err
}

// RebuildTimeSeriesCardinality atomically replaces derived state from retained,
// non-deleted observations. The transaction provides the mutation fence.
func RebuildTimeSeriesCardinality(ctx context.Context, db *Handler) (TimeSeriesCardinalityReconciliation, error) {
	var result TimeSeriesCardinalityReconciliation
	dbms, err := validateTimeSeriesDB(db)
	if err != nil {
		return result, err
	}
	tx, err := (*db).BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `DELETE FROM TimeSeriesActiveDimensionValues`); err != nil {
		return result, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM TimeSeriesActiveSeries`); err != nil {
		return result, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+timeSeriesObservationColumns+` FROM TimeSeriesObservations WHERE deleted_at IS NULL ORDER BY observation_id`)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		o, scanErr := scanTimeSeriesObservation(rows.Scan)
		if scanErr != nil {
			_ = rows.Close()
			return result, scanErr
		}
		if err = incrementTimeSeriesCardinality(ctx, tx, dbms, o); err != nil {
			_ = rows.Close()
			return result, err
		}
		result.Observations++
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return result, err
	}
	_ = rows.Close()
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM TimeSeriesActiveSeries`).Scan(&result.Series); err != nil {
		return result, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM TimeSeriesActiveDimensionValues`).Scan(&result.DimensionValues); err != nil {
		return result, err
	}
	if err = tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}

func deleteTimeSeriesObservationsWithAccounting(ctx context.Context, tx *sql.Tx, dbms, where string, args []interface{}, limit int) (int64, error) {
	query := `SELECT ` + timeSeriesObservationColumns + ` FROM TimeSeriesObservations WHERE ` + where + ` ORDER BY observation_id`
	if limit > 0 {
		query += ` LIMIT ` + fmt.Sprint(limit)
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	observations := make([]*TimeSeriesObservation, 0)
	for rows.Next() {
		o, scanErr := scanTimeSeriesObservation(rows.Scan)
		if scanErr != nil {
			_ = rows.Close()
			return 0, scanErr
		}
		observations = append(observations, o)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return 0, err
	}
	_ = rows.Close()
	for _, o := range observations {
		if o.DeletedAt == nil {
			if err = decrementTimeSeriesCardinality(ctx, tx, dbms, o); err != nil {
				return 0, err
			}
		}
		p := newInformationSeedPlaceholders(dbms)
		if _, err = tx.ExecContext(ctx, `DELETE FROM TimeSeriesObservations WHERE observation_id=`+p.Next(), o.ID); err != nil {
			return 0, err
		}
	}
	return int64(len(observations)), nil
}
