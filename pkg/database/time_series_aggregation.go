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
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

var (
	// ErrTimeSeriesAggregationRunning indicates that another worker owns the aggregation lease.
	ErrTimeSeriesAggregationRunning = errors.New("time-series aggregation already running")
	timeSeriesAggregationMutex      sync.Mutex
)

// TimeSeriesRange is a half-open UTC interval [Start, End).
type TimeSeriesRange struct {
	Start time.Time
	End   time.Time
}

// TimeSeriesAggregationOptions bounds one incremental or explicit range run.
type TimeSeriesAggregationOptions struct {
	Range      *TimeSeriesRange
	Overlap    time.Duration
	BatchSize  int
	MaxBatches int
	Now        time.Time
	RunKey     string
}

// TimeSeriesAggregationResult reports deterministic work performed by a run.
type TimeSeriesAggregationResult struct {
	Range                 TimeSeriesRange
	ObservationsProcessed int
	AggregatesReplaced    int
	BatchesProcessed      int
	WindowsProcessed      int
	Checkpoint            time.Time
}

// TimeSeriesRetentionOptions configures bounded raw and aggregate pruning.
type TimeSeriesRetentionOptions struct {
	Now                time.Time
	RawRetention       time.Duration
	AggregateRetention time.Duration
	BatchSize          int
	MaxBatches         int
	DryRun             bool
}

// TimeSeriesRetentionResult reports candidate and deleted rows without deleting metric definitions.
type TimeSeriesRetentionResult struct {
	RawCandidates       int64
	AggregateCandidates int64
	RawDeleted          int64
	AggregatesDeleted   int64
	BatchesProcessed    int
}

type timeSeriesAggregateAccumulator struct {
	aggregate TimeSeriesAggregate
	values    []float64
	distinct  map[string]struct{}
	firstTime time.Time
	lastTime  time.Time
}

// AggregateTimeSeriesRange recomputes complete buckets intersecting an explicit range.
// Existing rows in those buckets are replaced in one transaction, which also repairs
// groups whose entity or other scope assignment changed after their first aggregation.
func AggregateTimeSeriesRange(ctx context.Context, db *Handler, affected TimeSeriesRange, options TimeSeriesAggregationOptions) (TimeSeriesAggregationResult, error) {
	options.Range = &affected
	return RunTimeSeriesAggregation(ctx, db, options)
}

// ReaggregateTimeSeriesBackfill accepts the affected range emitted by delayed
// entity assignment backfill. AffectedEnd is inclusive in the backfill result,
// so it is converted to this service's half-open range.
func ReaggregateTimeSeriesBackfill(ctx context.Context, db *Handler, backfill EntityObservationBackfillResult, options TimeSeriesAggregationOptions) (TimeSeriesAggregationResult, error) {
	if backfill.AffectedStart == nil || backfill.AffectedEnd == nil {
		return TimeSeriesAggregationResult{}, nil
	}
	return AggregateTimeSeriesRange(ctx, db, TimeSeriesRange{Start: backfill.AffectedStart.UTC(), End: backfill.AffectedEnd.UTC().Add(time.Nanosecond)}, options)
}

// RunTimeSeriesAggregation performs a bounded incremental run. When Range is nil,
// the durable checkpoint minus Overlap is used so delayed observations update existing buckets.
func RunTimeSeriesAggregation(
	ctx context.Context,
	db *Handler,
	options TimeSeriesAggregationOptions,
) (result TimeSeriesAggregationResult, err error) {
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
	if options.RunKey == "" {
		options.RunKey = "timeseries-aggregation"
	}

	timeSeriesAggregationMutex.Lock()
	defer timeSeriesAggregationMutex.Unlock()

	metrics, err := ListTimeSeriesMetrics(
		db,
		TimeSeriesMetricFilter{
			Enabled:    boolPointer(true),
			Pagination: TimeSeriesPagination{Limit: 100000},
		},
	)
	if err != nil {
		return result, fmt.Errorf("list aggregation metrics: %w", err)
	}

	// Explicit rebuilds remain atomic. Their observation budget is per metric,
	// never shared across metrics.
	if options.Range != nil {
		affected := TimeSeriesRange{
			Start: options.Range.Start.UTC(),
			End:   options.Range.End.UTC(),
		}

		if affected.Start.IsZero() || !affected.Start.Before(affected.End) {
			return result, fmt.Errorf(
				"time-series aggregation range must satisfy start < end",
			)
		}

		result, err = aggregateTimeSeriesWindow(
			ctx,
			db,
			dbms,
			metrics,
			affected,
			options.BatchSize,
			options.BatchSize*options.MaxBatches,
			options.RunKey,
			affected.End,
		)
		if err != nil {
			if stateErr := recordTimeSeriesAggregationFailure(
				db,
				dbms,
				options.RunKey,
				affected,
				time.Time{},
				err,
			); stateErr != nil {
				return result, fmt.Errorf(
					"%w; persist aggregation failure: %v",
					err,
					stateErr,
				)
			}
			return result, err
		}

		result.WindowsProcessed = 1
		return result, nil
	}

	checkpoint, err := timeSeriesAggregationCheckpoint(db, options.RunKey)
	if err != nil {
		return result, err
	}

	if checkpoint.IsZero() {
		checkpoint, err = earliestTimeSeriesObservation(db)
		if err != nil {
			return result, err
		}
	}

	if checkpoint.IsZero() {
		result.Checkpoint = options.Now
		result.Range = TimeSeriesRange{
			Start: options.Now,
			End:   options.Now,
		}
		return result, nil
	}

	checkpoint = checkpoint.UTC()

	if !checkpoint.Before(options.Now) {
		result.Checkpoint = checkpoint
		result.Range = TimeSeriesRange{
			Start: checkpoint,
			End:   checkpoint,
		}
		return result, nil
	}

	// Apply late-data overlap once for the invocation. Subsequent catch-up
	// windows are contiguous and already belong to the same invocation.
	initialStart := checkpoint.Add(-options.Overlap)

	result.Range = TimeSeriesRange{
		Start: initialStart,
		End:   checkpoint,
	}
	result.Checkpoint = checkpoint

	for window := 0; window < options.MaxBatches && checkpoint.Before(options.Now); window++ {

		windowEnd, boundaryErr := nextTimeSeriesAggregationWindowEnd(
			checkpoint,
			options.Now,
			metrics,
		)
		if boundaryErr != nil {
			return result, boundaryErr
		}

		if !checkpoint.Before(windowEnd) {
			return result, fmt.Errorf(
				"time-series aggregation window did not advance checkpoint %s",
				checkpoint.Format(time.RFC3339Nano),
			)
		}

		windowStart := checkpoint
		if window == 0 {
			windowStart = initialStart
		}

		affected := TimeSeriesRange{
			Start: windowStart,
			End:   windowEnd,
		}

		// Incremental aggregation deliberately has no observation-count cutoff.
		// The unit of atomic progress is the complete bucket-safe time window.
		partial, aggregateErr := aggregateTimeSeriesWindow(
			ctx,
			db,
			dbms,
			metrics,
			affected,
			options.BatchSize,
			0,
			options.RunKey,
			windowEnd,
		)
		if aggregateErr != nil {
			if stateErr := recordTimeSeriesAggregationFailure(
				db,
				dbms,
				options.RunKey,
				affected,
				checkpoint,
				aggregateErr,
			); stateErr != nil {
				return result, fmt.Errorf(
					"%w; persist aggregation failure: %v",
					aggregateErr,
					stateErr,
				)
			}
			return result, aggregateErr
		}

		result.ObservationsProcessed += partial.ObservationsProcessed
		result.AggregatesReplaced += partial.AggregatesReplaced
		result.BatchesProcessed += partial.BatchesProcessed
		result.WindowsProcessed++

		checkpoint = windowEnd
		result.Checkpoint = checkpoint
		result.Range.End = checkpoint
	}

	return result, nil
}

func nextTimeSeriesAggregationWindowEnd(
	checkpoint time.Time,
	now time.Time,
	metrics []TimeSeriesMetric,
) (time.Time, error) {
	checkpoint = checkpoint.UTC()
	end := now.UTC()

	for i := range metrics {
		metric := metrics[i]

		if metric.Bucket == cfg.TimeSeriesBucketNone {
			continue
		}

		_, bucketEnd, err := TimeSeriesBucketBounds(
			checkpoint,
			metric.Bucket,
		)
		if err != nil {
			return time.Time{}, fmt.Errorf(
				"resolve aggregation boundary for metric %d: %w",
				metric.ID,
				err,
			)
		}

		if bucketEnd.After(checkpoint) && bucketEnd.Before(end) {
			end = bucketEnd
		}
	}

	return end, nil
}

func aggregateTimeSeriesWindow(
	ctx context.Context,
	db *Handler,
	dbms string,
	metrics []TimeSeriesMetric,
	affected TimeSeriesRange,
	batchSize int,
	metricBudget int,
	runKey string,
	checkpoint time.Time,
) (result TimeSeriesAggregationResult, err error) {
	result.Range = affected

	aggregates := make(map[string]*timeSeriesAggregateAccumulator)
	metricRanges := make(map[uint64]TimeSeriesRange)

	for i := range metrics {
		metric := metrics[i]

		if metric.Bucket == cfg.TimeSeriesBucketNone {
			continue
		}

		start, _, boundsErr := TimeSeriesBucketBounds(
			affected.Start,
			metric.Bucket,
		)
		if boundsErr != nil {
			return result, boundsErr
		}

		_, end, boundsErr := TimeSeriesBucketBounds(
			affected.End.Add(-time.Nanosecond),
			metric.Bucket,
		)
		if boundsErr != nil {
			return result, boundsErr
		}

		metricRange := TimeSeriesRange{
			Start: start,
			End:   end,
		}
		metricRanges[metric.ID] = metricRange

		offset := 0
		remaining := metricBudget

		for {
			limit := batchSize

			if metricBudget > 0 && remaining < limit {
				limit = remaining
			}

			if limit <= 0 {
				return result, fmt.Errorf(
					"time-series aggregation batch budget exhausted "+
						"before metric %d range was complete",
					metric.ID,
				)
			}

			query := TimeSeriesQueryFilter{
				MetricID:  &metric.ID,
				Start:     &metricRange.Start,
				End:       &metricRange.End,
				TimeBasis: metric.TimeBasis,
				Pagination: TimeSeriesPagination{
					Limit:  limit,
					Offset: offset,
				},
			}

			page, queryErr := QueryTimeSeriesObservationsContext(
				ctx,
				db,
				query,
			)
			if queryErr != nil {
				return result, fmt.Errorf(
					"query observations for metric %d: %w",
					metric.ID,
					queryErr,
				)
			}

			for j := range page.Observations {
				observation := page.Observations[j]

				basis, ok := timeSeriesObservationBasis(
					observation,
					metric.TimeBasis,
				)
				if !ok ||
					basis.Before(metricRange.Start) ||
					!basis.Before(metricRange.End) {
					continue
				}

				if aggregateErr := addTimeSeriesObservation(
					aggregates,
					metric,
					observation,
					basis,
				); aggregateErr != nil {
					return result, aggregateErr
				}

				result.ObservationsProcessed++
			}

			result.BatchesProcessed++

			if metricBudget > 0 {
				remaining -= page.Count
			}

			if !page.HasMore || page.Count == 0 {
				break
			}

			if metricBudget > 0 && remaining <= 0 {
				return result, fmt.Errorf(
					"time-series aggregation batch budget exhausted "+
						"before metric %d range was complete",
					metric.ID,
				)
			}

			offset += page.Count
		}
	}

	computed := make([]TimeSeriesAggregate, 0, len(aggregates))

	for _, accumulator := range aggregates {
		finalizeTimeSeriesAggregate(accumulator)
		computed = append(computed, accumulator.aggregate)
	}

	sort.Slice(computed, func(i, j int) bool {
		return computed[i].AggregateHash < computed[j].AggregateHash
	})

	tx, err := (*db).BeginTx(
		ctx,
		&sql.TxOptions{Isolation: sql.LevelSerializable},
	)
	if err != nil {
		return result, fmt.Errorf(
			"begin aggregation replacement: %w",
			err,
		)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err = acquireTimeSeriesAggregationLock(
		ctx,
		tx,
		dbms,
		runKey,
	); err != nil {
		return result, err
	}

	for metricID, metricRange := range metricRanges {
		p := newInformationSeedPlaceholders(dbms)

		if _, err = tx.ExecContext(
			ctx,
			`DELETE FROM TimeSeriesAggregates
			 WHERE metric_id = `+p.Next()+`
			   AND bucket_start < `+p.Next()+`
			   AND bucket_end > `+p.Next(),
			metricID,
			metricRange.End,
			metricRange.Start,
		); err != nil {
			return result, fmt.Errorf(
				"delete affected aggregates: %w",
				err,
			)
		}
	}

	for i := range computed {
		query, args, buildErr := buildTimeSeriesAggregateUpsert(
			dbms,
			&computed[i],
		)
		if buildErr != nil {
			return result, buildErr
		}

		if _, err = tx.ExecContext(
			ctx,
			query,
			args...,
		); err != nil {
			return result, fmt.Errorf(
				"replace aggregate %s: %w",
				computed[i].AggregateHash,
				err,
			)
		}
	}

	result.AggregatesReplaced = len(computed)
	result.Checkpoint = checkpoint.UTC()

	if err = recordTimeSeriesAggregationRun(
		ctx,
		tx,
		dbms,
		runKey,
		affected,
		result.Checkpoint,
		"completed",
		"",
	); err != nil {
		return result, err
	}

	if dbms == DBMySQLStr {
		if _, err = tx.ExecContext(
			ctx,
			`DO RELEASE_LOCK(?)`,
			runKey,
		); err != nil {
			return result, fmt.Errorf(
				"release aggregation lock: %w",
				err,
			)
		}
	}

	if err = tx.Commit(); err != nil {
		return result, fmt.Errorf(
			"commit aggregation replacement: %w",
			err,
		)
	}

	return result, nil
}

func recordTimeSeriesAggregationFailure(
	db *Handler,
	dbms string,
	key string,
	affected TimeSeriesRange,
	checkpoint time.Time,
	runErr error,
) error {
	if runErr == nil || errors.Is(runErr, ErrTimeSeriesAggregationRunning) {
		return nil
	}

	var checkpointValue interface{}
	if !checkpoint.IsZero() {
		checkpointValue = checkpoint.UTC()
	}

	p := newInformationSeedPlaceholders(dbms)

	args := []interface{}{
		key,
		"failed",
		checkpointValue,
		affected.Start.UTC(),
		affected.End.UTC(),
		runErr.Error(),
	}

	values := make([]string, len(args))
	for i := range values {
		values[i] = p.Next()
	}

	query := `
		INSERT INTO TimeSeriesAggregationRuns (
			run_key,
			status,
			checkpoint_at,
			range_start,
			range_end,
			last_error
		) VALUES (` + strings.Join(values, ",") + `)`

	if dbms == DBMySQLStr {
		query += `
			ON DUPLICATE KEY UPDATE
				status = VALUES(status),
				range_start = VALUES(range_start),
				range_end = VALUES(range_end),
				last_error = VALUES(last_error),
				last_updated_at = CURRENT_TIMESTAMP`
	} else {
		query += `
			ON CONFLICT (run_key) DO UPDATE SET
				status = excluded.status,
				range_start = excluded.range_start,
				range_end = excluded.range_end,
				last_error = excluded.last_error,
				last_updated_at = CURRENT_TIMESTAMP`
	}

	// Do not reuse the aggregation context here. A timeout/cancellation is
	// precisely one of the failures that must still be recorded.
	stateCtx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Second,
	)
	defer cancel()

	_, err := (*db).ExecContext(stateCtx, query, args...)
	return err
}

func addTimeSeriesObservation(groups map[string]*timeSeriesAggregateAccumulator, metric TimeSeriesMetric, observation TimeSeriesObservation, basis time.Time) error {
	bucketStart, bucketEnd, err := TimeSeriesBucketBounds(basis, metric.Bucket)
	if err != nil {
		return err
	}
	aggregate := TimeSeriesAggregate{MetricID: metric.ID, BucketStart: bucketStart, BucketEnd: bucketEnd, Scope: observation.Scope, Dimensions: observation.Dimensions}
	hash, err := TimeSeriesAggregateHash(aggregate)
	if err != nil {
		return err
	}
	accumulator := groups[hash]
	if accumulator == nil {
		aggregate.AggregateHash = hash
		accumulator = &timeSeriesAggregateAccumulator{aggregate: aggregate, distinct: make(map[string]struct{})}
		groups[hash] = accumulator
	}
	a := &accumulator.aggregate
	a.ValueCount++
	a.OccurrenceTotal += timeSeriesOccurrence(metric.ValueType, observation.Value)
	accumulator.distinct[observation.ValueHash] = struct{}{}
	if observation.IsChanged {
		a.ChangeCount++
	}
	if a.FirstSeenAt == nil || basis.Before(*a.FirstSeenAt) {
		t := basis
		a.FirstSeenAt = &t
	}
	if a.LastSeenAt == nil || basis.After(*a.LastSeenAt) {
		t := basis
		a.LastSeenAt = &t
	}
	if numeric, ok := timeSeriesNumericValue(metric.ValueType, observation.Value); ok {
		accumulator.values = append(accumulator.values, numeric)
		a.NumericCount++
		if a.NumericSum == nil {
			a.NumericSum, a.NumericMin, a.NumericMax = floatPointer(numeric), floatPointer(numeric), floatPointer(numeric)
		} else {
			*a.NumericSum += numeric
			if numeric < *a.NumericMin {
				*a.NumericMin = numeric
			}
			if numeric > *a.NumericMax {
				*a.NumericMax = numeric
			}
		}
	}
	if accumulator.firstTime.IsZero() || basis.Before(accumulator.firstTime) || (basis.Equal(accumulator.firstTime) && observation.ID < dereferenceUint(a.First.ObservationID)) {
		accumulator.firstTime = basis
		a.First = aggregateEdge(observation, basis, metric.ValueType)
	}
	if accumulator.lastTime.IsZero() || basis.After(accumulator.lastTime) || (basis.Equal(accumulator.lastTime) && observation.ID > dereferenceUint(a.Last.ObservationID)) {
		accumulator.lastTime = basis
		a.Last = aggregateEdge(observation, basis, metric.ValueType)
		a.LastValueBoolean = nil
		a.LastValueJSON = nil
		if metric.ValueType == cfg.TimeSeriesValueBoolean && observation.Value.Boolean != nil {
			v := *observation.Value.Boolean
			a.LastValueBoolean = &v
		}
		if metric.ValueType == cfg.TimeSeriesValueJSON && len(observation.Value.JSON) != 0 {
			a.LastValueJSON = append(json.RawMessage(nil), observation.Value.JSON...)
		}
	}
	return nil
}

func finalizeTimeSeriesAggregate(accumulator *timeSeriesAggregateAccumulator) {
	a := &accumulator.aggregate
	a.DistinctValueCount = int64(len(accumulator.distinct))
	if a.NumericCount == 0 {
		return
	}
	average := *a.NumericSum / float64(a.NumericCount)
	a.NumericAverage = &average
	sort.Float64s(accumulator.values)
	a.Percentile50 = floatPointer(TimeSeriesPercentile(accumulator.values, .50))
	a.Percentile75 = floatPointer(TimeSeriesPercentile(accumulator.values, .75))
	a.Percentile90 = floatPointer(TimeSeriesPercentile(accumulator.values, .90))
	a.Percentile95 = floatPointer(TimeSeriesPercentile(accumulator.values, .95))
	a.Percentile99 = floatPointer(TimeSeriesPercentile(accumulator.values, .99))
}

// TimeSeriesPercentile uses the inclusive linear interpolation method:
// rank=(n-1)*p, interpolating between floor(rank) and ceil(rank).
func TimeSeriesPercentile(sortedValues []float64, percentile float64) float64 {
	if len(sortedValues) == 0 || percentile < 0 || percentile > 1 {
		return math.NaN()
	}
	if len(sortedValues) == 1 {
		return sortedValues[0]
	}
	rank := float64(len(sortedValues)-1) * percentile
	lower := int(math.Floor(rank))
	upper := int(math.Ceil(rank))
	if lower == upper {
		return sortedValues[lower]
	}
	weight := rank - float64(lower)
	return sortedValues[lower] + (sortedValues[upper]-sortedValues[lower])*weight
}

func timeSeriesNumericValue(valueType cfg.TimeSeriesValueType, value TimeSeriesValue) (float64, bool) {
	switch valueType {
	case cfg.TimeSeriesValueDecimal, cfg.TimeSeriesValueDuration:
		if value.Numeric != nil {
			return *value.Numeric, true
		}
	case cfg.TimeSeriesValueInteger, cfg.TimeSeriesValueCount:
		if value.Integer != nil {
			return float64(*value.Integer), true
		}
	}
	return 0, false
}

func timeSeriesOccurrence(valueType cfg.TimeSeriesValueType, value TimeSeriesValue) float64 {
	if valueType == cfg.TimeSeriesValueCount {
		if value.Integer != nil {
			return float64(*value.Integer)
		}
		if value.Numeric != nil {
			return *value.Numeric
		}
	}
	return 1
}

func aggregateEdge(observation TimeSeriesObservation, at time.Time, valueType cfg.TimeSeriesValueType) TimeSeriesAggregateEdge {
	edge := TimeSeriesAggregateEdge{ObservationID: timeSeriesUint64Pointer(observation.ID), ObservedAt: timePointer(at), ValueHash: observation.ValueHash}
	if numeric, ok := timeSeriesNumericValue(valueType, observation.Value); ok {
		edge.ValueNumeric = floatPointer(numeric)
	}
	if valueType == cfg.TimeSeriesValueString && observation.Value.Text != nil {
		v := *observation.Value.Text
		edge.ValueText = &v
	}
	return edge
}

func timeSeriesObservationBasis(observation TimeSeriesObservation, basis cfg.TimeSeriesTimeBasis) (time.Time, bool) {
	switch basis {
	case cfg.TimeSeriesTimeObservedAt, "":
		return observation.ObservedAt.UTC(), !observation.ObservedAt.IsZero()
	case cfg.TimeSeriesTimeEventAt:
		if observation.EffectiveAt != nil {
			return observation.EffectiveAt.UTC(), true
		}
	case cfg.TimeSeriesTimeSourceTimestamp:
		if observation.SourceUpdatedAt != nil {
			return observation.SourceUpdatedAt.UTC(), true
		}
	}
	return time.Time{}, false
}

func acquireTimeSeriesAggregationLock(ctx context.Context, tx *sql.Tx, dbms, key string) error {
	switch dbms {
	case DBPostgresStr:
		var acquired bool
		if err := tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock(hashtext($1))`, key).Scan(&acquired); err != nil {
			return err
		}
		if !acquired {
			return ErrTimeSeriesAggregationRunning
		}
	case DBMySQLStr:
		var acquired sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT GET_LOCK(?, 0)`, key).Scan(&acquired); err != nil {
			return err
		}
		if !acquired.Valid || acquired.Int64 != 1 {
			return ErrTimeSeriesAggregationRunning
		}
	}
	return nil
}

func recordTimeSeriesAggregationRun(ctx context.Context, tx *sql.Tx, dbms, key string, affected TimeSeriesRange, checkpoint time.Time, status, lastError string) error {
	p := newInformationSeedPlaceholders(dbms)
	args := []interface{}{key, status, checkpoint, affected.Start, affected.End, nullableString(lastError)}
	values := make([]string, len(args))
	for i := range values {
		values[i] = p.Next()
	}
	query := `INSERT INTO TimeSeriesAggregationRuns (run_key,status,checkpoint_at,range_start,range_end,last_error,completed_at) VALUES (` + strings.Join(values, ",") + `,CURRENT_TIMESTAMP)`
	if dbms == DBMySQLStr {
		query += `
			ON DUPLICATE KEY UPDATE
				status = VALUES(status),
				range_start = VALUES(range_start),
				range_end = VALUES(range_end),
				last_error = VALUES(last_error),
				completed_at = NULL,
				last_updated_at = CURRENT_TIMESTAMP`
	} else {
		query += `
			ON CONFLICT (run_key) DO UPDATE SET
				status = excluded.status,
				range_start = excluded.range_start,
				range_end = excluded.range_end,
				last_error = excluded.last_error,
				completed_at = NULL,
				last_updated_at = CURRENT_TIMESTAMP`
	}
	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

func timeSeriesAggregationCheckpoint(db *Handler, key string) (time.Time, error) {
	dbms, err := validateTimeSeriesDB(db)
	if err != nil {
		return time.Time{}, err
	}
	var checkpoint sql.NullTime
	err = (*db).QueryRow(`SELECT checkpoint_at FROM TimeSeriesAggregationRuns WHERE run_key = `+informationSeedPlaceholderForDBMS(dbms, 1), key).Scan(&checkpoint)
	if err == sql.ErrNoRows {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return checkpoint.Time.UTC(), nil
}

func earliestTimeSeriesObservation(db *Handler) (time.Time, error) {
	var earliest time.Time

	err := (*db).QueryRow(`
		SELECT observed_at
		FROM TimeSeriesObservations
		WHERE deleted_at IS NULL
		ORDER BY observed_at ASC
		LIMIT 1
	`).Scan(&earliest)

	if err == sql.ErrNoRows {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf(
			"query earliest time-series observation: %w",
			err,
		)
	}

	return earliest.UTC(), nil
}

func buildTimeSeriesAggregateUpsert(dbms string, a *TimeSeriesAggregate) (string, []interface{}, error) {
	if a.AggregateHash == "" {
		hash, err := TimeSeriesAggregateHash(*a)
		if err != nil {
			return "", nil, err
		}
		a.AggregateHash = hash
	}
	dimensions, err := optionalCanonicalJSON(a.Dimensions)
	if err != nil {
		return "", nil, err
	}
	lastJSON := optionalRawJSONForAggregate(a.LastValueJSON)
	args := []interface{}{a.MetricID, a.BucketStart.UTC(), a.BucketEnd.UTC(), a.Scope.InformationSeedID, a.Scope.InformationSeedCandidateID, a.Scope.SourceID, a.Scope.SourceInformationSeedID, a.Scope.IndexID, a.Scope.EntityID, nullableString(a.Scope.SubjectType), a.Scope.SubjectID, nullableString(a.Scope.ObjectType), a.Scope.ObjectID, a.Scope.CorrelationRuleID, nullableString(a.Scope.CorrelationObjectType1), a.Scope.CorrelationObjectID1, nullableString(a.Scope.CorrelationObjectType2), a.Scope.CorrelationObjectID2, dimensions, a.ValueCount, a.OccurrenceTotal, a.DistinctValueCount, a.NumericCount, a.NumericSum, a.NumericMin, a.NumericMax, a.NumericAverage, a.Percentile50, a.Percentile75, a.Percentile90, a.Percentile95, a.Percentile99, a.First.ObservationID, a.First.ObservedAt, a.First.ValueNumeric, a.First.ValueText, nullableString(a.First.ValueHash), a.Last.ObservationID, a.Last.ObservedAt, a.Last.ValueNumeric, a.Last.ValueText, nullableString(a.Last.ValueHash), a.LastValueBoolean, lastJSON, a.FirstSeenAt, a.LastSeenAt, a.ChangeCount, a.AggregateHash}
	p := newInformationSeedPlaceholders(dbms)
	values := make([]string, len(args))
	for i := range values {
		values[i] = p.Next()
	}
	if dbms == DBPostgresStr {
		for _, i := range []int{18, 43} {
			if args[i] != nil {
				values[i] += "::jsonb"
			}
		}
	}
	fields := []string{"metric_id", "bucket_start", "bucket_end", "information_seed_id", "information_seed_candidate_id", "source_id", "source_information_seed_id", "index_id", "entity_id", "subject_type", "subject_id", "object_type", "object_id", "correlation_rule_id", "correlation_object_type_1", "correlation_object_id_1", "correlation_object_type_2", "correlation_object_id_2", "dimensions", "value_count", "occurrence_total", "distinct_value_count", "numeric_count", "numeric_sum", "numeric_min", "numeric_max", "numeric_avg", "percentile_50", "percentile_75", "percentile_90", "percentile_95", "percentile_99", "first_observation_id", "first_observed_at", "first_value_numeric", "first_value_text", "first_value_hash", "last_observation_id", "last_observed_at", "last_value_numeric", "last_value_text", "last_value_hash", "last_value_boolean", "last_value_json", "first_seen_at", "last_seen_at", "change_count", "aggregate_hash"}
	query := `INSERT INTO TimeSeriesAggregates (` + strings.Join(fields, ",") + `) VALUES (` + strings.Join(values, ",") + ")"
	updates := make([]string, 0, len(fields))
	for _, field := range fields[:len(fields)-1] {
		if dbms == DBMySQLStr {
			updates = append(updates, field+"=VALUES("+field+")")
		} else {
			updates = append(updates, field+"=excluded."+field)
		}
	}
	updates = append(updates, "deleted_at=NULL", "last_updated_at=CURRENT_TIMESTAMP")
	if dbms == DBMySQLStr {
		query += ` ON DUPLICATE KEY UPDATE ` + strings.Join(updates, ",")
	} else {
		query += ` ON CONFLICT (aggregate_hash) DO UPDATE SET ` + strings.Join(updates, ",")
	}
	return query, args, nil
}

// PruneTimeSeriesRetention applies global defaults with optional metric JSON
// overrides {"raw":"...","aggregated":"..."}. Deletes are bounded and definitions are never touched.
func PruneTimeSeriesRetention(ctx context.Context, db *Handler, options TimeSeriesRetentionOptions) (TimeSeriesRetentionResult, error) {
	var result TimeSeriesRetentionResult
	dbms, err := validateTimeSeriesDB(db)
	if err != nil {
		return result, err
	}
	if options.Now.IsZero() {
		options.Now = time.Now().UTC()
	}
	if options.BatchSize <= 0 {
		options.BatchSize = 1000
	}
	if options.MaxBatches <= 0 {
		options.MaxBatches = 10
	}
	metrics, err := ListTimeSeriesMetrics(db, TimeSeriesMetricFilter{IncludeDeleted: true, Pagination: TimeSeriesPagination{Limit: 100000}})
	if err != nil {
		return result, err
	}
	for _, metric := range metrics {
		raw, agg := options.RawRetention, options.AggregateRetention
		var policy struct {
			Raw        string `json:"raw"`
			Aggregated string `json:"aggregated"`
		}
		if len(metric.RetentionPolicy) > 0 && json.Unmarshal(metric.RetentionPolicy, &policy) == nil {
			if d, e := parseTimeSeriesDuration(policy.Raw); e == nil && policy.Raw != "" {
				raw = d
			}
			if d, e := parseTimeSeriesDuration(policy.Aggregated); e == nil && policy.Aggregated != "" {
				agg = d
			}
		}
		for kind, duration := range map[string]time.Duration{"raw": raw, "aggregate": agg} {
			if duration <= 0 {
				continue
			}
			cutoff := options.Now.Add(-duration)
			table, timeColumn := "TimeSeriesObservations", "observed_at"
			if kind == "aggregate" {
				table, timeColumn = "TimeSeriesAggregates", "bucket_end"
			}
			p := newInformationSeedPlaceholders(dbms)
			var count int64
			if err = (*db).QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE metric_id = `+p.Next()+` AND `+timeColumn+` < `+p.Next(), metric.ID, cutoff).Scan(&count); err != nil {
				return result, err
			}
			if kind == "raw" {
				result.RawCandidates += count
			} else {
				result.AggregateCandidates += count
			}
			if options.DryRun {
				continue
			}
			for batch := 0; batch < options.MaxBatches; batch++ {
				p = newInformationSeedPlaceholders(dbms)
				idColumn := "observation_id"
				if kind == "aggregate" {
					idColumn = "aggregate_id"
				}
				query := `DELETE FROM ` + table + ` WHERE ` + idColumn + ` IN (SELECT ` + idColumn + ` FROM ` + table + ` WHERE metric_id = ` + p.Next() + ` AND ` + timeColumn + ` < ` + p.Next() + ` ORDER BY ` + idColumn + ` LIMIT ` + p.Next() + `)`
				if dbms == DBMySQLStr {
					query = `DELETE target FROM ` + table + ` target JOIN (SELECT ` + idColumn + ` FROM ` + table + ` WHERE metric_id = ? AND ` + timeColumn + ` < ? ORDER BY ` + idColumn + ` LIMIT ?) doomed ON target.` + idColumn + ` = doomed.` + idColumn
				}
				res, e := (*db).ExecContext(ctx, query, metric.ID, cutoff, options.BatchSize)
				if e != nil {
					return result, e
				}
				n, _ := res.RowsAffected()
				if kind == "raw" {
					result.RawDeleted += n
				} else {
					result.AggregatesDeleted += n
				}
				result.BatchesProcessed++
				if n < int64(options.BatchSize) {
					break
				}
			}
		}
	}
	return result, nil
}

func parseTimeSeriesDuration(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	multiplier := time.Duration(1)
	if strings.HasSuffix(value, "d") {
		multiplier = 24
		value = strings.TrimSuffix(value, "d") + "h"
	} else if strings.HasSuffix(value, "w") {
		multiplier = 7 * 24
		value = strings.TrimSuffix(value, "w") + "h"
	}
	duration, err := time.ParseDuration(value)
	return duration * multiplier, err
}
func timeSeriesUint64Pointer(v uint64) *uint64 { return &v }
func boolPointer(v bool) *bool                 { return &v }
func floatPointer(v float64) *float64          { return &v }
func timePointer(v time.Time) *time.Time       { return &v }
func dereferenceUint(v *uint64) uint64 {
	if v == nil {
		return 0
	}
	return *v
}
