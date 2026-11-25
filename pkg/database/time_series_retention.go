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
	"sort"
	"strings"
	"sync"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

var (
	// ErrTimeSeriesRetentionRunning indicates that another process owns the
	// cluster-wide retention lease.
	ErrTimeSeriesRetentionRunning = errors.New("time-series retention already running")
	// ErrTimeSeriesRetentionAggregationBusy indicates that an aggregation run
	// owns the shared maintenance lease; retention yields rather than deleting
	// observations while aggregates are being computed.
	ErrTimeSeriesRetentionAggregationBusy = errors.New("time-series aggregation is running; retention deferred")

	timeSeriesRetentionMutex sync.Mutex
)

const (
	timeSeriesRetentionLockKey      = "crowler:timeseries:retention"
	timeSeriesRetentionRunKeyPrefix = "timeseries-retention:v1:"
)

// timeSeriesStoredRetentionPolicy is the JSON stored in
// TimeSeriesMetrics.retention_policy. Only the two windows are policy; the
// scheduling knobs are operator configuration and never duplicated per metric.
type timeSeriesStoredRetentionPolicy struct {
	Raw        string `json:"raw,omitempty"`
	Aggregated string `json:"aggregated,omitempty"`
}

type timeSeriesRetentionSkipReason string

const (
	timeSeriesRetentionSkipInvalidPolicy     timeSeriesRetentionSkipReason = "invalid_policy"
	timeSeriesRetentionSkipInvalidTimeBasis  timeSeriesRetentionSkipReason = "invalid_time_basis"
	timeSeriesRetentionSkipInvalidBucket     timeSeriesRetentionSkipReason = "invalid_bucket"
	timeSeriesRetentionSkipCheckpointMissing timeSeriesRetentionSkipReason = "aggregation_checkpoint_missing"
)

func (r timeSeriesRetentionSkipReason) String() string { return string(r) }

func acquireTimeSeriesRetentionLease(ctx context.Context, db *Handler, dbms string) (*timeSeriesAggregationLease, error) {
	lease := &timeSeriesAggregationLease{key: timeSeriesRetentionLockKey}
	switch dbms {
	case DBSQLiteStr:
		// SQLite retention is serialized only by timeSeriesRetentionMutex. The
		// no-op lease does not coordinate separate processes.
		return lease, nil
	case DBPostgresStr:
		// PostgreSQL advisory locks belong to a session, so retain one
		// dedicated connection for the complete retention invocation.
	default:
		return nil, fmt.Errorf("unsupported database type for time-series retention lease: %s", dbms)
	}

	provider, ok := (*db).(DedicatedConnectionProvider)
	if !ok {
		return nil, fmt.Errorf("acquire retention lease: PostgreSQL handler does not provide dedicated connections")
	}
	conn, err := provider.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("reserve retention lease connection: %w", err)
	}
	lease.conn = conn
	lease.unlock = func(ctx context.Context, key string) (bool, error) {
		var unlocked bool
		err := conn.QueryRowContext(ctx, `SELECT pg_advisory_unlock(hashtext($1))`, key).Scan(&unlocked)
		return unlocked, err
	}
	lease.close = conn.Close

	var acquired bool
	if err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtext($1))`, lease.key).Scan(&acquired); err != nil {
		closeErr := conn.Close()
		if closeErr != nil {
			return nil, errors.Join(err, fmt.Errorf("close retention lease connection: %w", closeErr))
		}
		return nil, err
	}
	if !acquired {
		closeErr := conn.Close()
		if closeErr != nil {
			return nil, errors.Join(ErrTimeSeriesRetentionRunning, fmt.Errorf("close retention lease connection: %w", closeErr))
		}
		return nil, ErrTimeSeriesRetentionRunning
	}
	return lease, nil
}

func acquireTimeSeriesRetentionMaintenanceLease(ctx context.Context, db *Handler, dbms string) (*timeSeriesAggregationLease, error) {
	lease := &timeSeriesAggregationLease{keys: []string{timeSeriesRetentionLockKey, timeSeriesAggregationLockKey}}
	switch dbms {
	case DBSQLiteStr:
		return lease, nil
	case DBPostgresStr:
	default:
		return nil, fmt.Errorf("unsupported database type for time-series retention lease: %s", dbms)
	}
	provider, ok := (*db).(DedicatedConnectionProvider)
	if !ok {
		return nil, fmt.Errorf("acquire retention lease: PostgreSQL handler does not provide dedicated connections")
	}
	conn, err := provider.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("reserve retention lease connection: %w", err)
	}
	lease.conn = conn
	lease.unlock = func(ctx context.Context, key string) (bool, error) {
		var unlocked bool
		err := conn.QueryRowContext(ctx, `SELECT pg_advisory_unlock(hashtext($1))`, key).Scan(&unlocked)
		return unlocked, err
	}
	lease.close = conn.Close
	for _, key := range lease.keys {
		var acquired bool
		if err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtext($1))`, key).Scan(&acquired); err != nil {
			closeErr := conn.Close()
			if closeErr != nil {
				return nil, errors.Join(err, fmt.Errorf("close retention lease connection: %w", closeErr))
			}
			return nil, err
		}
		if acquired {
			continue
		}
		closeErr := conn.Close()
		busyErr := ErrTimeSeriesRetentionRunning
		if key == timeSeriesAggregationLockKey {
			busyErr = ErrTimeSeriesRetentionAggregationBusy
		}
		if closeErr != nil {
			return nil, errors.Join(busyErr, fmt.Errorf("close retention lease connection: %w", closeErr))
		}
		return nil, busyErr
	}
	return lease, nil
}

func releaseTimeSeriesRetentionLease(lease *timeSeriesAggregationLease, primaryErr error) error {
	return errors.Join(primaryErr, lease.release())
}

func parseStoredTimeSeriesRetentionPolicy(raw json.RawMessage) (cfg.TimeSeriesRetentionWindow, cfg.TimeSeriesRetentionWindow, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return cfg.TimeSeriesRetentionWindow{}, cfg.TimeSeriesRetentionWindow{}, nil
	}
	var policy timeSeriesStoredRetentionPolicy
	if err := json.Unmarshal(raw, &policy); err != nil {
		return cfg.TimeSeriesRetentionWindow{}, cfg.TimeSeriesRetentionWindow{}, fmt.Errorf("decode retention policy: %w", err)
	}
	parse := func(value string) (cfg.TimeSeriesRetentionWindow, error) {
		if strings.TrimSpace(value) == "" {
			return cfg.TimeSeriesRetentionWindow{}, nil
		}
		return cfg.ParseTimeSeriesRetentionWindow(value)
	}
	rawWindow, err := parse(policy.Raw)
	if err != nil {
		return cfg.TimeSeriesRetentionWindow{}, cfg.TimeSeriesRetentionWindow{}, fmt.Errorf("raw retention: %w", err)
	}
	aggregateWindow, err := parse(policy.Aggregated)
	if err != nil {
		return cfg.TimeSeriesRetentionWindow{}, cfg.TimeSeriesRetentionWindow{}, fmt.Errorf("aggregated retention: %w", err)
	}
	return rawWindow, aggregateWindow, nil
}

func resolveTimeSeriesRetentionWindow(override cfg.TimeSeriesRetentionWindow, option cfg.TimeSeriesRetentionWindow, legacy time.Duration) cfg.TimeSeriesRetentionWindow {
	if override.Specified {
		return override
	}
	if option.Specified {
		return option
	}
	if legacy > 0 {
		return cfg.TimeSeriesRetentionWindow{Specified: true, Fixed: legacy}
	}
	return cfg.TimeSeriesRetentionWindow{}
}

func timeSeriesRetentionRunKey(metricID uint64) string {
	return fmt.Sprintf("%s%d", timeSeriesRetentionRunKeyPrefix, metricID)
}

// timeSeriesRetentionWatermark reports the oldest instant that may have lost
// raw observations to scheduled retention for one metric. It is zero until the
// first physical raw deletion, which means complete history is still available
// from the earliest observation.
func timeSeriesRetentionWatermark(ctx context.Context, db *Handler, metricID uint64) (time.Time, error) {
	dbms, err := validateTimeSeriesDB(db)
	if err != nil {
		return time.Time{}, err
	}
	p := newInformationSeedPlaceholders(dbms)
	var watermark sql.NullTime
	err = (*db).QueryRowContext(ctx, `SELECT checkpoint_at FROM TimeSeriesAggregationRuns WHERE run_key = `+p.Next(), timeSeriesRetentionRunKey(metricID)).Scan(&watermark)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("read time-series retention watermark: %w", err)
	}
	if !watermark.Valid {
		return time.Time{}, nil
	}
	return watermark.Time.UTC(), nil
}

func recordTimeSeriesRetentionWatermarkTx(ctx context.Context, tx *sql.Tx, dbms string, metricID uint64, cutoff, now time.Time) error {
	key := timeSeriesRetentionRunKey(metricID)
	p := newInformationSeedPlaceholders(dbms)
	var existing sql.NullTime
	err := tx.QueryRowContext(ctx, `SELECT checkpoint_at FROM TimeSeriesAggregationRuns WHERE run_key = `+p.Next(), key).Scan(&existing)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read existing retention watermark: %w", err)
	}
	// The database stores microsecond precision. Round the cutoff up so a range
	// that starts at the stored watermark can never include a row that was
	// deleted just below it.
	watermark := cutoff.UTC().Truncate(time.Microsecond)
	if watermark.Before(cutoff.UTC()) {
		watermark = watermark.Add(time.Microsecond)
	}
	if existing.Valid && existing.Time.UTC().After(watermark) {
		watermark = existing.Time.UTC()
	}
	p = newInformationSeedPlaceholders(dbms)
	args := []interface{}{key, "completed", watermark, cutoff.UTC(), now.UTC()}
	values := make([]string, len(args))
	for i := range values {
		values[i] = p.Next()
	}
	query := `INSERT INTO TimeSeriesAggregationRuns (run_key,status,checkpoint_at,range_start,range_end,completed_at,last_error) VALUES (` +
		strings.Join(values, ",") + `,CURRENT_TIMESTAMP,NULL)`
	if dbms == DBMySQLStr {
		query += ` ON DUPLICATE KEY UPDATE status=VALUES(status),checkpoint_at=VALUES(checkpoint_at),range_start=VALUES(range_start),range_end=VALUES(range_end),completed_at=CURRENT_TIMESTAMP,last_error=NULL`
	} else {
		query += ` ON CONFLICT (run_key) DO UPDATE SET status=excluded.status,checkpoint_at=excluded.checkpoint_at,range_start=excluded.range_start,range_end=excluded.range_end,completed_at=CURRENT_TIMESTAMP,last_error=NULL`
	}
	if _, err = tx.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("record time-series retention watermark: %w", err)
	}
	return nil
}

func pruneRawObservationBatch(ctx context.Context, db *Handler, dbms string, metricID uint64, timeColumn string, cutoff, now time.Time, batchSize int) (deleted int64, watermarkUpdated bool, retErr error) {
	txCtx, cancelTx := TransactionContextForHandler(ctx, db)
	defer cancelTx()
	tx, err := (*db).BeginTx(txCtx, nil)
	if err != nil {
		return 0, false, NormalizeTransactionError(txCtx, err)
	}
	defer func() {
		_ = tx.Rollback()
		retErr = NormalizeTransactionError(txCtx, retErr)
	}()
	// Take the cardinality maintenance fence before selecting or deleting rows.
	// It matches the admission/release ordering; a concurrent deletion of the
	// same membership is detected by release and rolls the whole batch back
	// rather than leaving counters inconsistent.
	if err = lockTimeSeriesCardinalityMutation(txCtx, tx, dbms, false); err != nil {
		return 0, false, fmt.Errorf("acquire time-series cardinality mutation fence for retention: %w", err)
	}
	p := newInformationSeedPlaceholders(dbms)
	where := `metric_id = ` + p.Next() + ` AND ` + timeColumn + ` IS NOT NULL AND ` + timeColumn + ` < ` + p.Next()
	n, err := deleteTimeSeriesObservationsWithAccountingOrder(txCtx, tx, dbms, where, timeColumn+", observation_id", []interface{}{metricID, cutoff.UTC()}, batchSize)
	if err != nil {
		return 0, false, err
	}
	if n > 0 {
		if err = recordTimeSeriesRetentionWatermarkTx(txCtx, tx, dbms, metricID, cutoff.UTC(), now.UTC()); err != nil {
			return 0, false, err
		}
		watermarkUpdated = true
	}
	if err = tx.Commit(); err != nil {
		return 0, false, err
	}
	return n, watermarkUpdated, nil
}

func pruneAggregateBatch(ctx context.Context, db *Handler, dbms string, metricID uint64, cutoff time.Time, batchSize int) (deleted int64, retErr error) {
	txCtx, cancelTx := TransactionContextForHandler(ctx, db)
	defer cancelTx()
	tx, err := (*db).BeginTx(txCtx, nil)
	if err != nil {
		return 0, NormalizeTransactionError(txCtx, err)
	}
	defer func() {
		_ = tx.Rollback()
		retErr = NormalizeTransactionError(txCtx, retErr)
	}()
	p := newInformationSeedPlaceholders(dbms)
	query := `DELETE FROM TimeSeriesAggregates WHERE aggregate_id IN (SELECT aggregate_id FROM TimeSeriesAggregates WHERE metric_id = ` + p.Next() + ` AND bucket_end < ` + p.Next() + ` AND bucket_start < ` + p.Next() + ` ORDER BY aggregate_id LIMIT ` + p.Next() + `)`
	if dbms == DBMySQLStr {
		query = `DELETE target FROM TimeSeriesAggregates target JOIN (SELECT aggregate_id FROM TimeSeriesAggregates WHERE metric_id = ? AND bucket_end < ? AND bucket_start < ? ORDER BY aggregate_id LIMIT ?) doomed ON target.aggregate_id = doomed.aggregate_id`
	}
	result, err := tx.ExecContext(txCtx, query, metricID, cutoff.UTC(), cutoff.UTC(), batchSize)
	if err != nil {
		return 0, err
	}
	n, _ := result.RowsAffected()
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return n, nil
}

// PruneTimeSeriesRetention applies global policy with optional metric JSON
// overrides {"raw":"...","aggregated":"..."}. Raw deletion participates in the
// transactional cardinality accounting and advances a per-metric watermark that
// prevents later aggregate rebuilds from treating pruned history as complete.
//
// At most one retention pass runs per process; PostgreSQL additionally takes a
// session advisory lease so replicas cannot prune concurrently.
func PruneTimeSeriesRetention(ctx context.Context, db *Handler, options TimeSeriesRetentionOptions) (result TimeSeriesRetentionResult, err error) {
	dbms, err := validateTimeSeriesDB(db)
	if err != nil {
		return result, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if options.Now.IsZero() {
		options.Now = time.Now().UTC()
	}
	options.Now = options.Now.UTC()
	if options.BatchSize <= 0 {
		options.BatchSize = 1000
	}
	if options.MaxBatches <= 0 {
		options.MaxBatches = 10
	}
	if options.RequireAggregationCheckpoint && options.AggregationOverlap <= 0 {
		// The overlap is still needed to repair late data. A caller that does not
		// provide one gets the documented default rather than pruning into it.
		options.AggregationOverlap = 15 * time.Minute
	}
	if err = options.RawWindow.Validate(); err != nil {
		return result, fmt.Errorf("raw retention window: %w", err)
	}
	if err = options.AggregateWindow.Validate(); err != nil {
		return result, fmt.Errorf("aggregate retention window: %w", err)
	}

	timeSeriesRetentionMutex.Lock()
	// Serialize with in-process aggregation as well. The PostgreSQL session
	// leases coordinate replicas; this mutex prevents the same process from
	// scanning aggregates while retention is deleting their raw observations.
	timeSeriesAggregationMutex.Lock()
	lease, err := acquireTimeSeriesRetentionMaintenanceLease(ctx, db, dbms)
	if err != nil {
		timeSeriesAggregationMutex.Unlock()
		timeSeriesRetentionMutex.Unlock()
		if errors.Is(err, ErrTimeSeriesRetentionRunning) || errors.Is(err, ErrTimeSeriesRetentionAggregationBusy) {
			timeSeriesRetentionRuns.WithLabelValues("contention").Inc()
		} else {
			timeSeriesRetentionRuns.WithLabelValues("failed").Inc()
		}
		return result, err
	}
	started := time.Now()
	defer func() {
		releaseErr := releaseTimeSeriesRetentionLease(lease, err)
		err = releaseErr
		timeSeriesAggregationMutex.Unlock()
		timeSeriesRetentionMutex.Unlock()
		outcome := "completed"
		if err != nil {
			outcome = "failed"
		} else if result.BudgetExhausted {
			outcome = "budget_exhausted"
		}
		timeSeriesRetentionRuns.WithLabelValues(outcome).Inc()
		timeSeriesRetentionRunDuration.Observe(time.Since(started).Seconds())
	}()

	checkpoint := time.Time{}
	if options.RequireAggregationCheckpoint {
		checkpoint, err = timeSeriesAggregationCheckpointContext(ctx, db, "timeseries-aggregation")
		if err != nil {
			return result, err
		}
		checkpoint = checkpoint.UTC()
	}

	metrics, err := ListTimeSeriesMetricsContext(ctx, db, TimeSeriesMetricFilter{IncludeDeleted: true, Pagination: TimeSeriesPagination{Limit: 100000}})
	if err != nil {
		return result, err
	}
	// Deterministic processing order keeps lock acquisition and work budgets
	// reproducible across replicas and tests.
	sort.Slice(metrics, func(i, j int) bool { return metrics[i].ID < metrics[j].ID })

	batches := 0
	budgetExhausted := false
	stop := func() bool {
		if batches >= options.MaxBatches {
			budgetExhausted = true
			return true
		}
		if options.MaxDuration > 0 && time.Since(started) >= options.MaxDuration {
			budgetExhausted = true
			return true
		}
		return false
	}
	recordSkip := func(reason timeSeriesRetentionSkipReason) {
		result.MetricsSkipped++
		result.SkipReasons = appendBoundedRetentionReason(result.SkipReasons, reason.String())
		timeSeriesRetentionSkippedMetrics.WithLabelValues(reason.String()).Inc()
	}

	for i := range metrics {
		metric := metrics[i]
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if stop() {
			break
		}
		result.MetricsProcessed++

		rawOverride, aggregateOverride, policyErr := parseStoredTimeSeriesRetentionPolicy(metric.RetentionPolicy)
		if policyErr != nil {
			recordSkip(timeSeriesRetentionSkipInvalidPolicy)
			continue
		}
		rawWindow := resolveTimeSeriesRetentionWindow(rawOverride, options.RawWindow, options.RawRetention)
		aggregateWindow := resolveTimeSeriesRetentionWindow(aggregateOverride, options.AggregateWindow, options.AggregateRetention)

		timeColumn, columnErr := timeSeriesAggregationTimeColumn(metric.TimeBasis)
		if columnErr != nil {
			recordSkip(timeSeriesRetentionSkipInvalidTimeBasis)
			continue
		}

		rawCutoff, rawActive := rawWindow.Cutoff(options.Now)
		if rawActive && options.RequireAggregationCheckpoint && metric.Bucket != cfg.TimeSeriesBucketNone {
			if checkpoint.IsZero() {
				recordSkip(timeSeriesRetentionSkipCheckpointMissing)
				rawActive = false
			} else {
				safeCutoff := checkpoint.Add(-options.AggregationOverlap)
				// Aggregation expands a window to whole metric buckets. Keep raw
				// observations from the start of the first bucket the overlap can
				// recompute, not merely from the overlap instant.
				bucketStart, _, bucketErr := TimeSeriesBucketBounds(safeCutoff, metric.Bucket)
				if bucketErr != nil {
					recordSkip(timeSeriesRetentionSkipInvalidBucket)
					continue
				}
				safeCutoff = bucketStart
				if rawCutoff.After(safeCutoff) {
					rawCutoff = safeCutoff
				}
			}
		}
		if rawActive {
			p := newInformationSeedPlaceholders(dbms)
			var candidates int64
			countQuery := `SELECT COUNT(*) FROM TimeSeriesObservations WHERE metric_id = ` + p.Next() + ` AND ` + timeColumn + ` IS NOT NULL AND ` + timeColumn + ` < ` + p.Next()
			if err = (*db).QueryRowContext(ctx, countQuery, metric.ID, rawCutoff.UTC()).Scan(&candidates); err != nil {
				return result, err
			}
			result.RawCandidates += candidates
			if candidates > 0 && !options.DryRun {
				remaining := candidates
				for batches < options.MaxBatches {
					n, watermarkUpdated, pruneErr := pruneRawObservationBatch(ctx, db, dbms, metric.ID, timeColumn, rawCutoff, options.Now, options.BatchSize)
					if pruneErr != nil {
						return result, pruneErr
					}
					result.RawDeleted += n
					remaining -= n
					if watermarkUpdated {
						result.WatermarksUpdated++
					}
					result.BatchesProcessed++
					batches++
					timeSeriesRetentionRowsDeleted.WithLabelValues("raw").Add(float64(n))
					if n < int64(options.BatchSize) || remaining <= 0 {
						break
					}
					if stop() {
						break
					}
				}
			}
		}

		if ctx.Err() != nil {
			return result, ctx.Err()
		}

		aggregateCutoff, aggregateActive := aggregateWindow.Cutoff(options.Now)
		if aggregateActive {
			p := newInformationSeedPlaceholders(dbms)
			var candidates int64
			if err = (*db).QueryRowContext(ctx, `SELECT COUNT(*) FROM TimeSeriesAggregates WHERE metric_id = `+p.Next()+` AND bucket_end < `+p.Next()+` AND bucket_start < `+p.Next(), metric.ID, aggregateCutoff.UTC(), aggregateCutoff.UTC()).Scan(&candidates); err != nil {
				return result, err
			}
			result.AggregateCandidates += candidates
			if candidates > 0 && !options.DryRun {
				if stop() {
					break
				}
				remaining := candidates
				for batches < options.MaxBatches {
					n, pruneErr := pruneAggregateBatch(ctx, db, dbms, metric.ID, aggregateCutoff, options.BatchSize)
					if pruneErr != nil {
						return result, pruneErr
					}
					result.AggregatesDeleted += n
					remaining -= n
					result.BatchesProcessed++
					batches++
					timeSeriesRetentionRowsDeleted.WithLabelValues("aggregate").Add(float64(n))
					if n < int64(options.BatchSize) || remaining <= 0 {
						break
					}
					if stop() {
						break
					}
				}
			}
		}
	}

	result.BudgetExhausted = budgetExhausted
	if result.BudgetExhausted {
		timeSeriesRetentionBudgetStops.Inc()
	}
	return result, nil
}

func appendBoundedRetentionReason(reasons []string, reason string) []string {
	const maxReasons = 16
	for _, existing := range reasons {
		if existing == reason {
			return reasons
		}
	}
	if len(reasons) >= maxReasons {
		return reasons
	}
	return append(reasons, reason)
}
