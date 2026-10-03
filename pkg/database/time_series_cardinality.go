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

// timeSeriesCardinalityMutation is the single internal write boundary for
// active cardinality state and PostgreSQL capacity reservations. Code outside
// this file must never write the active or slot tables directly.
type timeSeriesCardinalityMutation struct {
	ctx  context.Context
	tx   *sql.Tx
	dbms string
}

// lockTimeSeriesCardinalityMutation participates in the database-wide
// cardinality maintenance fence. PostgreSQL's shared advisory transaction lock
// preserves writer concurrency, while MySQL and SQLite use the singleton row
// installed by the schema. Rebuild takes the corresponding exclusive lock.
func lockTimeSeriesCardinalityMutation(ctx context.Context, tx *sql.Tx, dbms string, exclusive bool) error {
	switch dbms {
	case DBPostgresStr:
		fn := "pg_advisory_xact_lock_shared"
		if exclusive {
			fn = "pg_advisory_xact_lock"
		}
		_, err := tx.ExecContext(ctx, `SELECT `+fn+`(741953102846276103)`)
		return err
	case DBMySQLStr:
		_, err := tx.ExecContext(ctx, `SELECT lock_id FROM TimeSeriesCardinalityMaintenanceLock WHERE lock_id=1 FOR UPDATE`)
		return err
	default: // SQLite: a write to the singleton obtains the database write lock.
		_, err := tx.ExecContext(ctx, `UPDATE TimeSeriesCardinalityMaintenanceLock SET lock_id=lock_id WHERE lock_id=1`)
		return err
	}
}

func newTimeSeriesCardinalityMutation(ctx context.Context, tx *sql.Tx, dbms string) timeSeriesCardinalityMutation {
	return timeSeriesCardinalityMutation{ctx: ctx, tx: tx, dbms: dbms}
}

// replaceIdentity claims and references the complete replacement identity
// before the observation is changed or its former identity is released. Any
// failure is returned to the owning transaction, which must roll the unit back.
func (m timeSeriesCardinalityMutation) replaceIdentity(old, replacement *TimeSeriesObservation, policy cfg.TimeSeriesCardinalityConfig, update func() error) error {
	exceeded, err := m.admit(replacement.MetricID, replacement.Scope, replacement.Dimensions, policy)
	if err != nil {
		return fmt.Errorf("claim replacement time-series identity: %w", err)
	}
	if exceeded {
		return fmt.Errorf("%w: replacement cardinality limit exceeded", ErrTimeSeriesValueRejected)
	}
	if err = m.add(replacement); err != nil {
		return err
	}
	if err = update(); err != nil {
		return err
	}
	return m.release(old)
}

func loadTimeSeriesCardinalityPolicyTx(ctx context.Context, tx *sql.Tx, dbms string, metricID uint64) (cfg.TimeSeriesCardinalityConfig, error) {
	var policy cfg.TimeSeriesCardinalityConfig
	p := newInformationSeedPlaceholders(dbms)
	var raw sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT cardinality_policy FROM TimeSeriesMetrics WHERE metric_id=`+p.Next(), metricID).Scan(&raw); err != nil {
		return policy, err
	}
	if raw.Valid && raw.String != "" && raw.String != "null" {
		if err := json.Unmarshal([]byte(raw.String), &policy); err != nil {
			return policy, fmt.Errorf("decode time-series cardinality policy: %w", err)
		}
	}
	return policy, nil
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
	if err = newTimeSeriesCardinalityMutation(ctx, tx, dbms).release(o); err != nil {
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
	return newTimeSeriesCardinalityMutation(ctx, tx, dbms).admit(metricID, scope, dimensions, policy)
}

func (m timeSeriesCardinalityMutation) admit(metricID uint64, scope TimeSeriesScope, dimensions map[string]interface{}, policy cfg.TimeSeriesCardinalityConfig) (bool, error) {
	ctx, tx, dbms := m.ctx, m.tx, m.dbms
	if tx == nil {
		return false, fmt.Errorf("time-series cardinality transaction is nil")
	}
	if err := lockTimeSeriesCardinalityMutation(ctx, tx, dbms, false); err != nil {
		return false, fmt.Errorf("acquire time-series cardinality mutation fence: %w", err)
	}
	seriesHash, err := TimeSeriesSeriesHash(metricID, scope, dimensions)
	if err != nil {
		return false, err
	}
	if dbms != DBPostgresStr {
		exceeded, _, decisionErr := timeSeriesCardinalityDecision(ctx, tx, dbms, metricID, seriesHash, scope, dimensions, policy)
		return exceeded, decisionErr
	}
	// PostgreSQL reservations are bounded by unique (owner, slot_number) keys.
	// A writer normally touches only the slot selected by its identity hash, so
	// an open page transaction cannot fence all new series for a metric.
	if _, err = tx.ExecContext(ctx, `SAVEPOINT timeseries_cardinality_admission`); err != nil {
		return false, err
	}
	reject := func(cause error) (bool, error) {
		_, rollbackErr := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT timeseries_cardinality_admission`)
		if rollbackErr == nil {
			_, rollbackErr = tx.ExecContext(ctx, `RELEASE SAVEPOINT timeseries_cardinality_admission`)
		}
		if cause != nil {
			return false, cause
		}
		return true, rollbackErr
	}
	identity, err := timeSeriesSeriesIdentityJSON(&TimeSeriesObservation{MetricID: metricID, Scope: scope, Dimensions: dimensions})
	if err != nil {
		return reject(err)
	}
	if policy.MaxSeriesPerMetric > 0 {
		admitted, claimErr := claimPostgresCardinalitySlot(ctx, tx, "TimeSeriesSeriesSlots", metricID, "", seriesHash, identity, policy.MaxSeriesPerMetric)
		if claimErr != nil {
			return reject(claimErr)
		}
		if !admitted {
			return reject(nil)
		}
	}
	keys := make([]string, 0, len(dimensions))
	for key := range dimensions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if policy.MaxValuesPerDimension > 0 {
		for _, key := range keys {
			hash, canonical, hashErr := timeSeriesDimensionValueHash(metricID, key, dimensions[key])
			if hashErr != nil {
				return reject(hashErr)
			}
			admitted, claimErr := claimPostgresCardinalitySlot(ctx, tx, "TimeSeriesDimensionSlots", metricID, key, hash, canonical, policy.MaxValuesPerDimension)
			if claimErr != nil {
				return reject(claimErr)
			}
			if !admitted {
				return reject(nil)
			}
		}
	}
	_, err = tx.ExecContext(ctx, `RELEASE SAVEPOINT timeseries_cardinality_admission`)
	return false, err
}

// claimPostgresCardinalitySlot returns true for both an existing identity and
// a newly reserved slot. Capacity tokens are a physical namespace shared by
// all owners and are not derived from the current policy. Locking one available
// token lets other transactions skip it and claim another one.
func claimPostgresCardinalitySlot(ctx context.Context, tx *sql.Tx, table string, metricID uint64, dimensionKey, identityHash, identity string, limit int) (bool, error) {
	keyPredicate, keyArgs := "metric_id=$1", []interface{}{metricID}
	identityColumn := "series_hash"
	if dimensionKey != "" {
		keyPredicate += " AND dimension_key=$2"
		keyArgs = append(keyArgs, dimensionKey)
		identityColumn = "value_hash"
	}
	var stored string
	args := append(append([]interface{}{}, keyArgs...), identityHash)
	identityPlaceholder := fmt.Sprintf("$%d", len(args))
	if err := tx.QueryRowContext(ctx, `SELECT identity_value FROM `+table+` WHERE `+keyPredicate+` AND `+identityColumn+`=`+identityPlaceholder, args...).Scan(&stored); err == nil {
		if stored != identity {
			return false, fmt.Errorf("time-series cardinality hash collision for metric %d", metricID)
		}
		return true, nil
	} else if err != sql.ErrNoRows {
		return false, err
	}
	// Extending the common token namespace is a single set operation. It is
	// normally a no-op (the setup script seeds the default 100,000 tokens), and
	// makes policy increases immediately usable without renumbering allocations.
	if _, err := tx.ExecContext(ctx, `INSERT INTO TimeSeriesCardinalityTokens (token_number) SELECT generate_series(COALESCE((SELECT MAX(token_number)+1 FROM TimeSeriesCardinalityTokens),0),$1-1) ON CONFLICT DO NOTHING`, limit); err != nil {
		return false, err
	}
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE `+keyPredicate, keyArgs...).Scan(&active); err != nil {
		return false, err
	}
	if active >= limit { // In particular, policy reductions admit nothing new.
		return false, nil
	}
	var slot int64
	claimQuery := `SELECT t.token_number FROM TimeSeriesCardinalityTokens t WHERE t.token_number < $1 AND NOT EXISTS (SELECT 1 FROM ` + table + ` s WHERE s.slot_number=t.token_number AND s.metric_id=$2) ORDER BY t.token_number FOR UPDATE OF t SKIP LOCKED LIMIT 1`
	claimArgs := []interface{}{limit, metricID}
	if dimensionKey != "" {
		claimQuery = `SELECT t.token_number FROM TimeSeriesCardinalityTokens t WHERE t.token_number < $1 AND NOT EXISTS (SELECT 1 FROM ` + table + ` s WHERE s.slot_number=t.token_number AND s.metric_id=$2 AND s.dimension_key=$3) ORDER BY t.token_number FOR UPDATE OF t SKIP LOCKED LIMIT 1`
		claimArgs = append(claimArgs, dimensionKey)
	}
	if err := tx.QueryRowContext(ctx, claimQuery, claimArgs...).Scan(&slot); err == sql.ErrNoRows {
		return false, nil
	} else if err != nil {
		return false, err
	}
	var query string
	var insertArgs []interface{}
	if dimensionKey == "" {
		query = `INSERT INTO ` + table + ` (metric_id,slot_number,series_hash,identity_value) VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING RETURNING slot_number`
		insertArgs = []interface{}{metricID, slot, identityHash, identity}
	} else {
		query = `INSERT INTO ` + table + ` (metric_id,dimension_key,slot_number,value_hash,identity_value) VALUES ($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING RETURNING slot_number`
		insertArgs = []interface{}{metricID, dimensionKey, slot, identityHash, identity}
	}
	if err := tx.QueryRowContext(ctx, query, insertArgs...).Scan(&slot); err == nil {
		return true, nil
	} else if err != sql.ErrNoRows {
		return false, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT identity_value FROM `+table+` WHERE `+keyPredicate+` AND `+identityColumn+`=`+identityPlaceholder, args...).Scan(&stored); err != nil {
		return false, err
	}
	if stored != identity {
		return false, fmt.Errorf("time-series cardinality hash collision for metric %d", metricID)
	}
	return true, nil
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

func (m timeSeriesCardinalityMutation) add(o *TimeSeriesObservation) error {
	ctx, tx, dbms := m.ctx, m.tx, m.dbms
	if err := lockTimeSeriesCardinalityMutation(ctx, tx, dbms, false); err != nil {
		return fmt.Errorf("acquire time-series cardinality mutation fence: %w", err)
	}
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
	// reference_count is supplied only for compatibility with databases upgraded
	// from 1.14. Membership rows, not this immutable value, are authoritative.
	seriesQuery := `INSERT INTO TimeSeriesActiveSeries (metric_id, series_hash, series_identity, reference_count) VALUES (` +
		placeholders(dbms, 4) + `)`
	if dbms == DBMySQLStr {
		seriesQuery += ` ON DUPLICATE KEY UPDATE series_hash=VALUES(series_hash)`
	} else {
		seriesQuery += ` ON CONFLICT (metric_id, series_hash) DO NOTHING`
	}
	if _, err = tx.ExecContext(ctx, seriesQuery, o.MetricID, o.SeriesHash, seriesIdentity, 1); err != nil {
		return fmt.Errorf("insert time-series active series: %w", err)
	}
	membershipQuery := `INSERT INTO TimeSeriesObservationSeries (observation_id,metric_id,series_hash) VALUES (` + placeholders(dbms, 3) + `)`
	if dbms == DBMySQLStr {
		membershipQuery += ` ON DUPLICATE KEY UPDATE observation_id=VALUES(observation_id)`
	} else {
		membershipQuery += ` ON CONFLICT (observation_id,metric_id,series_hash) DO NOTHING`
	}
	if _, err = tx.ExecContext(ctx, membershipQuery, o.ID, o.MetricID, o.SeriesHash); err != nil {
		return fmt.Errorf("insert time-series observation-series membership: %w", err)
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
			query += ` ON DUPLICATE KEY UPDATE value_hash=VALUES(value_hash)`
		} else {
			query += ` ON CONFLICT (metric_id, dimension_key, value_hash) DO NOTHING`
		}
		if _, err = tx.ExecContext(ctx, query, o.MetricID, key, hash, canonical, 1); err != nil {
			return fmt.Errorf("insert time-series active dimension value %q: %w", key, err)
		}
		membershipQuery = `INSERT INTO TimeSeriesObservationDimensions (observation_id,metric_id,dimension_key,value_hash) VALUES (` + placeholders(dbms, 4) + `)`
		if dbms == DBMySQLStr {
			membershipQuery += ` ON DUPLICATE KEY UPDATE observation_id=VALUES(observation_id)`
		} else {
			membershipQuery += ` ON CONFLICT (observation_id,metric_id,dimension_key,value_hash) DO NOTHING`
		}
		if _, err = tx.ExecContext(ctx, membershipQuery, o.ID, o.MetricID, key, hash); err != nil {
			return fmt.Errorf("insert time-series observation-dimension membership %q: %w", key, err)
		}
	}
	return nil
}

func (m timeSeriesCardinalityMutation) release(o *TimeSeriesObservation) error {
	ctx, tx, dbms := m.ctx, m.tx, m.dbms
	if err := lockTimeSeriesCardinalityMutation(ctx, tx, dbms, false); err != nil {
		return fmt.Errorf("acquire time-series cardinality mutation fence: %w", err)
	}
	p := newInformationSeedPlaceholders(dbms)
	removed, err := tx.ExecContext(ctx, `DELETE FROM TimeSeriesObservationSeries WHERE observation_id=`+p.Next()+` AND metric_id=`+p.Next()+` AND series_hash=`+p.Next(), o.ID, o.MetricID, o.SeriesHash)
	if err != nil {
		return err
	}
	removedCount, _ := removed.RowsAffected()
	if removedCount != 1 {
		return fmt.Errorf("missing exact series membership for observation %d", o.ID)
	}
	p = newInformationSeedPlaceholders(dbms)
	deleted, err := tx.ExecContext(ctx, `DELETE FROM TimeSeriesActiveSeries WHERE metric_id=`+p.Next()+` AND series_hash=`+p.Next()+` AND NOT EXISTS (SELECT 1 FROM TimeSeriesObservationSeries m WHERE m.metric_id=TimeSeriesActiveSeries.metric_id AND m.series_hash=TimeSeriesActiveSeries.series_hash)`, o.MetricID, o.SeriesHash)
	if err != nil {
		return err
	}
	deletedCount, _ := deleted.RowsAffected()
	if deletedCount == 1 && dbms == DBPostgresStr {
		if _, err = tx.ExecContext(ctx, `DELETE FROM TimeSeriesSeriesSlots WHERE metric_id=$1 AND series_hash=$2`, o.MetricID, o.SeriesHash); err != nil {
			return err
		}
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
		removed, err = tx.ExecContext(ctx, `DELETE FROM TimeSeriesObservationDimensions WHERE observation_id=`+p.Next()+` AND metric_id=`+p.Next()+` AND dimension_key=`+p.Next()+` AND value_hash=`+p.Next(), o.ID, o.MetricID, key, hash)
		if err != nil {
			return err
		}
		removedCount, _ = removed.RowsAffected()
		if removedCount != 1 {
			return fmt.Errorf("missing exact dimension membership for observation %d dimension %q", o.ID, key)
		}
		p = newInformationSeedPlaceholders(dbms)
		deleted, err = tx.ExecContext(ctx, `DELETE FROM TimeSeriesActiveDimensionValues WHERE metric_id=`+p.Next()+` AND dimension_key=`+p.Next()+` AND value_hash=`+p.Next()+` AND NOT EXISTS (SELECT 1 FROM TimeSeriesObservationDimensions m WHERE m.metric_id=TimeSeriesActiveDimensionValues.metric_id AND m.dimension_key=TimeSeriesActiveDimensionValues.dimension_key AND m.value_hash=TimeSeriesActiveDimensionValues.value_hash)`, o.MetricID, key, hash)
		if err != nil {
			return err
		}
		deletedCount, _ = deleted.RowsAffected()
		if deletedCount == 1 && dbms == DBPostgresStr {
			if _, err = tx.ExecContext(ctx, `DELETE FROM TimeSeriesDimensionSlots WHERE metric_id=$1 AND dimension_key=$2 AND value_hash=$3`, o.MetricID, key, hash); err != nil {
				return err
			}
		}
	}
	return nil
}

// releaseUnreferencedReservations removes claims made by an admission attempt
// which subsequently resolved to a duplicate observation.
func (m timeSeriesCardinalityMutation) releaseUnreferencedReservations(o *TimeSeriesObservation) error {
	if m.dbms != DBPostgresStr {
		return nil
	}
	if _, err := m.tx.ExecContext(m.ctx, `DELETE FROM TimeSeriesSeriesSlots s WHERE metric_id=$1 AND series_hash=$2 AND NOT EXISTS (SELECT 1 FROM TimeSeriesActiveSeries a WHERE a.metric_id=s.metric_id AND a.series_hash=s.series_hash)`, o.MetricID, o.SeriesHash); err != nil {
		return err
	}
	for key, value := range o.Dimensions {
		hash, _, err := timeSeriesDimensionValueHash(o.MetricID, key, value)
		if err != nil {
			return err
		}
		if _, err = m.tx.ExecContext(m.ctx, `DELETE FROM TimeSeriesDimensionSlots s WHERE metric_id=$1 AND dimension_key=$2 AND value_hash=$3 AND NOT EXISTS (SELECT 1 FROM TimeSeriesActiveDimensionValues a WHERE a.metric_id=s.metric_id AND a.dimension_key=s.dimension_key AND a.value_hash=s.value_hash)`, o.MetricID, key, hash); err != nil {
			return err
		}
	}
	return nil
}

func (m timeSeriesCardinalityMutation) reset() error {
	if _, err := m.tx.ExecContext(m.ctx, `DELETE FROM TimeSeriesObservationDimensions`); err != nil {
		return err
	}
	if _, err := m.tx.ExecContext(m.ctx, `DELETE FROM TimeSeriesObservationSeries`); err != nil {
		return err
	}
	if _, err := m.tx.ExecContext(m.ctx, `DELETE FROM TimeSeriesActiveDimensionValues`); err != nil {
		return err
	}
	if _, err := m.tx.ExecContext(m.ctx, `DELETE FROM TimeSeriesActiveSeries`); err != nil {
		return err
	}
	if m.dbms == DBPostgresStr {
		if _, err := m.tx.ExecContext(m.ctx, `DELETE FROM TimeSeriesDimensionSlots`); err != nil {
			return err
		}
		if _, err := m.tx.ExecContext(m.ctx, `DELETE FROM TimeSeriesSeriesSlots`); err != nil {
			return err
		}
	}
	return nil
}

func (m timeSeriesCardinalityMutation) reserveForRebuild(o *TimeSeriesObservation, capacity int) error {
	if m.dbms != DBPostgresStr {
		return nil
	}
	identity, err := timeSeriesSeriesIdentityJSON(o)
	if err != nil {
		return err
	}
	if ok, err := claimPostgresCardinalitySlot(m.ctx, m.tx, "TimeSeriesSeriesSlots", o.MetricID, "", o.SeriesHash, identity, capacity); err != nil || !ok {
		if err != nil {
			return err
		}
		return fmt.Errorf("reserve rebuilt series identity for metric %d", o.MetricID)
	}
	for key, value := range o.Dimensions {
		hash, canonical, err := timeSeriesDimensionValueHash(o.MetricID, key, value)
		if err != nil {
			return err
		}
		if ok, err := claimPostgresCardinalitySlot(m.ctx, m.tx, "TimeSeriesDimensionSlots", o.MetricID, key, hash, canonical, capacity); err != nil || !ok {
			if err != nil {
				return err
			}
			return fmt.Errorf("reserve rebuilt dimension identity for metric %d dimension %q", o.MetricID, key)
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
// non-deleted observations. Every mutation entry point takes the shared side
// of the same database lock; this administrative operation holds its exclusive
// side until the fully validated replacement commits.
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
	if err = lockTimeSeriesCardinalityMutation(ctx, tx, dbms, true); err != nil {
		return result, fmt.Errorf("acquire time-series cardinality maintenance fence: %w", err)
	}
	mutation := newTimeSeriesCardinalityMutation(ctx, tx, dbms)
	if err = mutation.reset(); err != nil {
		return result, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+timeSeriesObservationColumns+` FROM TimeSeriesObservations WHERE deleted_at IS NULL ORDER BY observation_id`)
	if err != nil {
		return result, err
	}
	observations := make([]*TimeSeriesObservation, 0)
	for rows.Next() {
		o, scanErr := scanTimeSeriesObservation(rows.Scan)
		if scanErr != nil {
			_ = rows.Close()
			return result, scanErr
		}
		observations = append(observations, o)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return result, err
	}
	_ = rows.Close()
	result.Observations = int64(len(observations))
	capacity := len(observations)
	if capacity == 0 {
		capacity = 1
	}
	var expectedDimensions int64
	expectedSeriesIdentities := make(map[string]struct{})
	expectedDimensionIdentities := make(map[string]struct{})
	for _, o := range observations {
		if err = mutation.add(o); err != nil {
			return result, err
		}
		expectedSeriesIdentities[fmt.Sprintf("%d\x00%s", o.MetricID, o.SeriesHash)] = struct{}{}
		expectedDimensions += int64(len(o.Dimensions))
		for key, value := range o.Dimensions {
			hash, _, hashErr := timeSeriesDimensionValueHash(o.MetricID, key, value)
			if hashErr != nil {
				return result, hashErr
			}
			expectedDimensionIdentities[fmt.Sprintf("%d\x00%s\x00%s", o.MetricID, key, hash)] = struct{}{}
		}
	}
	for _, o := range observations {
		if err = mutation.reserveForRebuild(o, capacity); err != nil {
			return result, err
		}
	}
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM TimeSeriesActiveSeries`).Scan(&result.Series); err != nil {
		return result, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM TimeSeriesActiveDimensionValues`).Scan(&result.DimensionValues); err != nil {
		return result, err
	}
	if result.Series != int64(len(expectedSeriesIdentities)) || result.DimensionValues != int64(len(expectedDimensionIdentities)) {
		return result, fmt.Errorf("rebuilt identity validation failed: series=%d/%d dimensions=%d/%d", result.Series, len(expectedSeriesIdentities), result.DimensionValues, len(expectedDimensionIdentities))
	}
	var seriesMemberships, dimensionMemberships int64
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM TimeSeriesObservationSeries`).Scan(&seriesMemberships); err != nil {
		return result, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM TimeSeriesObservationDimensions`).Scan(&dimensionMemberships); err != nil {
		return result, err
	}
	if seriesMemberships != result.Observations || dimensionMemberships != expectedDimensions {
		return result, fmt.Errorf("rebuilt membership validation failed: series=%d/%d dimensions=%d/%d", seriesMemberships, result.Observations, dimensionMemberships, expectedDimensions)
	}
	if dbms == DBPostgresStr {
		var seriesReservations, dimensionReservations int64
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM TimeSeriesSeriesSlots`).Scan(&seriesReservations); err != nil {
			return result, err
		}
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM TimeSeriesDimensionSlots`).Scan(&dimensionReservations); err != nil {
			return result, err
		}
		if seriesReservations != result.Series || dimensionReservations != result.DimensionValues {
			return result, fmt.Errorf("rebuilt reservation validation failed: series=%d/%d dimensions=%d/%d", seriesReservations, result.Series, dimensionReservations, result.DimensionValues)
		}
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
			if err = newTimeSeriesCardinalityMutation(ctx, tx, dbms).release(o); err != nil {
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
