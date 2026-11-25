// Copyright 2026 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package database

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func retentionTestMetric(t *testing.T, db *Handler, key string, basis cfg.TimeSeriesTimeBasis, cardinality *cfg.TimeSeriesCardinalityConfig) uint64 {
	t.Helper()
	selector, _ := jsonMarshalForRetentionTest(map[string]interface{}{"field": "value"})
	metric := &TimeSeriesMetric{
		Key:           key,
		DisplayName:   key,
		SourceKind:    cfg.TimeSeriesSourceCustom,
		ValueType:     cfg.TimeSeriesValueDecimal,
		Aggregate:     cfg.TimeSeriesAggregateAverage,
		Bucket:        cfg.TimeSeriesBucketOneHour,
		TimeBasis:     basis,
		DedupeScope:   cfg.TimeSeriesDedupeObject,
		FailurePolicy: cfg.TimeSeriesFailureLogSkip,
		Selector:      selector,
		Enabled:       true,
	}
	if cardinality != nil {
		raw, err := jsonMarshalForRetentionTest(cardinality)
		if err != nil {
			t.Fatal(err)
		}
		metric.CardinalityPolicy = raw
	}
	created, err := UpsertTimeSeriesMetric(db, metric)
	if err != nil {
		t.Fatal(err)
	}
	return created.ID
}

func jsonMarshalForRetentionTest(value interface{}) ([]byte, error) {
	return json.Marshal(value)
}

func retentionTestObservation(t *testing.T, metricID uint64, sourceID uint64, at time.Time, value float64, key string, basis cfg.TimeSeriesTimeBasis) TimeSeriesObservation {
	t.Helper()
	bucketStart, bucketEnd, err := TimeSeriesBucketBounds(at, cfg.TimeSeriesBucketOneHour)
	if err != nil {
		t.Fatal(err)
	}
	observation := TimeSeriesObservation{
		MetricID:    metricID,
		ObservedAt:  at,
		CollectedAt: time.Now().UTC(),
		BucketStart: bucketStart,
		BucketEnd:   bucketEnd,
		Scope:       TimeSeriesScope{SubjectType: "retention_test", SubjectID: &sourceID},
		Value:       TimeSeriesValue{Numeric: &value},
		ValueHash:   key + "-value",
		DedupeKey:   key,
	}
	switch basis {
	case cfg.TimeSeriesTimeEventAt:
		observation.EffectiveAt = &at
	case cfg.TimeSeriesTimeSourceTimestamp:
		observation.SourceUpdatedAt = &at
	}
	return observation
}

func insertRetentionObservation(t *testing.T, db *Handler, observation TimeSeriesObservation, policy cfg.TimeSeriesCardinalityConfig) TimeSeriesInsertResult {
	t.Helper()
	tx, err := (*db).BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := insertObservationWithCardinality(context.Background(), tx, DBSQLiteStr, &observation, policy, cfg.TimeSeriesFailureLogSkip)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestTimeSeriesRetentionUsesMetricTimeBasis(t *testing.T) {
	db, closeDB := openEntityTimeSeriesTestDB(t)
	defer closeDB()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	metricID := retentionTestMetric(t, db, "retention-event-at", cfg.TimeSeriesTimeEventAt, nil)

	// Effective time is old even though collection time is recent: event_at is
	// the metric clock, so this row expires.
	oldEvent := retentionTestObservation(t, metricID, 1, now, 10, "old-event", cfg.TimeSeriesTimeEventAt)
	oldEvent.EffectiveAt = timePointer(now.Add(-3 * time.Hour))
	oldEvent.ObservedAt = now
	insertRetentionObservation(t, db, oldEvent, cfg.TimeSeriesCardinalityConfig{})

	// Collection time is old but the event happened recently: it must survive.
	recentEvent := retentionTestObservation(t, metricID, 2, now, 20, "recent-event", cfg.TimeSeriesTimeEventAt)
	recentEvent.EffectiveAt = timePointer(now.Add(-30 * time.Minute))
	recentEvent.ObservedAt = now.Add(-4 * time.Hour)
	insertRetentionObservation(t, db, recentEvent, cfg.TimeSeriesCardinalityConfig{})

	result, err := PruneTimeSeriesRetention(context.Background(), db, TimeSeriesRetentionOptions{
		Now:                now,
		RawWindow:          cfg.TimeSeriesRetentionWindow{Specified: true, Fixed: time.Hour},
		AggregateRetention: 0,
		BatchSize:          10,
		MaxBatches:         10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RawDeleted != 1 {
		t.Fatalf("raw deleted = %d, want 1", result.RawDeleted)
	}
	var remaining int
	if err = (*db).QueryRow(`SELECT COUNT(*) FROM TimeSeriesObservations WHERE metric_id = ?`, metricID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Fatalf("remaining observations = %d, want 1", remaining)
	}
	var key string
	if err = (*db).QueryRow(`SELECT dedupe_key FROM TimeSeriesObservations WHERE metric_id = ?`, metricID).Scan(&key); err != nil {
		t.Fatal(err)
	}
	if key != "recent-event" {
		t.Fatalf("retained observation = %q, want recent-event", key)
	}
}

func TestTimeSeriesRetentionRespectsAggregationCheckpointAndOverlap(t *testing.T) {
	db, closeDB := openEntityTimeSeriesTestDB(t)
	defer closeDB()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	metricID := retentionTestMetric(t, db, "retention-checkpoint", cfg.TimeSeriesTimeObservedAt, nil)
	for index, at := range []time.Time{now.Add(-2*time.Hour - 30*time.Minute), now.Add(-2 * time.Hour)} {
		observation := retentionTestObservation(t, metricID, uint64(index+1), at, float64(index+1), "checkpoint-"+time.Duration(index).String(), cfg.TimeSeriesTimeObservedAt)
		insertRetentionObservation(t, db, observation, cfg.TimeSeriesCardinalityConfig{})
	}
	// Aggregation has reached one hour ago. With a 15m overlap the first
	// recomputable bucket starts at 10:00, which caps a nominal 1h retention
	// (requested cutoff 11:00) at the bucket boundary.
	if _, err := (*db).Exec(`INSERT INTO TimeSeriesAggregationRuns (run_key,status,checkpoint_at) VALUES (?,?,?)`,
		"timeseries-aggregation", "completed", now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	result, err := PruneTimeSeriesRetention(context.Background(), db, TimeSeriesRetentionOptions{
		Now:                          now,
		RawWindow:                    cfg.TimeSeriesRetentionWindow{Specified: true, Fixed: time.Hour},
		BatchSize:                    10,
		MaxBatches:                   10,
		RequireAggregationCheckpoint: true,
		AggregationOverlap:           15 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RawDeleted != 1 {
		t.Fatalf("raw deleted = %d, want only the row older than the safe overlap boundary", result.RawDeleted)
	}
	var remaining int
	if err = (*db).QueryRow(`SELECT COUNT(*) FROM TimeSeriesObservations WHERE metric_id = ?`, metricID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Fatalf("remaining observations = %d, want 1", remaining)
	}
}

func TestTimeSeriesRetentionRequiresCheckpointWhenAggregationEnabled(t *testing.T) {
	db, closeDB := openEntityTimeSeriesTestDB(t)
	defer closeDB()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	metricID := retentionTestMetric(t, db, "retention-no-checkpoint", cfg.TimeSeriesTimeObservedAt, nil)
	insertRetentionObservation(t, db, retentionTestObservation(t, metricID, 1, now.Add(-2*time.Hour), 1, "no-checkpoint", cfg.TimeSeriesTimeObservedAt), cfg.TimeSeriesCardinalityConfig{})
	result, err := PruneTimeSeriesRetention(context.Background(), db, TimeSeriesRetentionOptions{
		Now:                          now,
		RawWindow:                    cfg.TimeSeriesRetentionWindow{Specified: true, Fixed: time.Hour},
		BatchSize:                    10,
		MaxBatches:                   10,
		RequireAggregationCheckpoint: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RawDeleted != 0 || result.MetricsSkipped != 1 {
		t.Fatalf("result = %#v, want raw deletion skipped for missing checkpoint", result)
	}
	if len(result.SkipReasons) != 1 || result.SkipReasons[0] != timeSeriesRetentionSkipCheckpointMissing.String() {
		t.Fatalf("skip reasons = %#v", result.SkipReasons)
	}
}

func TestTimeSeriesRetentionWatermarkBlocksIncompleteRebuild(t *testing.T) {
	db, closeDB := openEntityTimeSeriesTestDB(t)
	defer closeDB()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	metric := retentionTestMetric(t, db, "retention-rebuild", cfg.TimeSeriesTimeObservedAt, nil)
	observation := retentionTestObservation(t, metric, 1, now.Add(-2*time.Hour), 42.5, "rebuild-old", cfg.TimeSeriesTimeObservedAt)
	insertRetentionObservation(t, db, observation, cfg.TimeSeriesCardinalityConfig{})
	rangeToAggregate := TimeSeriesRange{Start: now.Add(-2 * time.Hour), End: now.Add(-time.Hour)}
	if _, err := AggregateTimeSeriesRange(context.Background(), db, rangeToAggregate, TimeSeriesAggregationOptions{BatchSize: 10, MaxBatches: 10, RunKey: "rebuild-before-retention"}); err != nil {
		t.Fatal(err)
	}
	if _, err := PruneTimeSeriesRetention(context.Background(), db, TimeSeriesRetentionOptions{
		Now:        now,
		RawWindow:  cfg.TimeSeriesRetentionWindow{Specified: true, Fixed: time.Hour},
		BatchSize:  10,
		MaxBatches: 10,
	}); err != nil {
		t.Fatal(err)
	}
	result, err := AggregateTimeSeriesRange(context.Background(), db, rangeToAggregate, TimeSeriesAggregationOptions{BatchSize: 10, MaxBatches: 10, RunKey: "rebuild-after-retention"})
	if !errors.Is(err, ErrTimeSeriesAggregationIncompleteHistory) {
		t.Fatalf("rebuild error = %v, want ErrTimeSeriesAggregationIncompleteHistory", err)
	}
	if result.AggregatesReplaced != 0 {
		t.Fatalf("replaced %d aggregates from incomplete history", result.AggregatesReplaced)
	}
	aggregates, err := QueryTimeSeriesAggregates(db, TimeSeriesQueryFilter{MetricID: &metric})
	if err != nil || aggregates.Count != 1 || aggregates.Aggregates[0].NumericAverage == nil || *aggregates.Aggregates[0].NumericAverage != 42.5 {
		t.Fatalf("pre-existing aggregate was not preserved: %#v, %v", aggregates, err)
	}
}

func TestTimeSeriesRetentionReclaimsCardinalityAndPreservesAggregates(t *testing.T) {
	db, closeDB := openEntityTimeSeriesTestDB(t)
	defer closeDB()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	policy := cfg.TimeSeriesCardinalityConfig{MaxSeriesPerMetric: 1, MaxDimensions: 1, MaxValuesPerDimension: 10, Overflow: cfg.TimeSeriesCardinalityDrop}
	metric := retentionTestMetric(t, db, "retention-demo", cfg.TimeSeriesTimeObservedAt, &policy)

	first := retentionTestObservation(t, metric, 1, now.Add(-2*time.Hour), 10, "demo-series-a", cfg.TimeSeriesTimeObservedAt)
	insertRetentionObservation(t, db, first, policy)
	t.Logf("step 1: admitted series A with max_series_per_metric=1 (observation %q)", first.DedupeKey)

	rangeToAggregate := TimeSeriesRange{Start: now.Add(-2 * time.Hour), End: now.Add(-time.Hour)}
	if _, err := AggregateTimeSeriesRange(context.Background(), db, rangeToAggregate, TimeSeriesAggregationOptions{BatchSize: 10, MaxBatches: 10, RunKey: "demo-aggregation"}); err != nil {
		t.Fatal(err)
	}

	second := retentionTestObservation(t, metric, 2, now, 99, "demo-series-b", cfg.TimeSeriesTimeObservedAt)
	tx, err := (*db).BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = insertObservationWithCardinality(context.Background(), tx, DBSQLiteStr, &second, policy, cfg.TimeSeriesFailureLogSkip)
	if err == nil || !errors.Is(err, ErrTimeSeriesValueRejected) {
		_ = tx.Rollback()
		t.Fatalf("second series error = %v, want ErrTimeSeriesValueRejected", err)
	}
	_ = tx.Rollback()
	t.Logf("step 2: series B was rejected because the single slot belongs to series A: %v", err)

	result, err := PruneTimeSeriesRetention(context.Background(), db, TimeSeriesRetentionOptions{
		Now:             now,
		RawWindow:       cfg.TimeSeriesRetentionWindow{Specified: true, Fixed: time.Hour},
		AggregateWindow: cfg.TimeSeriesRetentionWindow{Specified: true, Forever: true},
		BatchSize:       10,
		MaxBatches:      10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RawDeleted != 1 {
		t.Fatalf("raw deleted = %d, want 1", result.RawDeleted)
	}
	var activeSeries int
	if err = (*db).QueryRow(`SELECT COUNT(*) FROM TimeSeriesActiveSeries WHERE metric_id = ?`, metric).Scan(&activeSeries); err != nil {
		t.Fatal(err)
	}
	if activeSeries != 0 {
		t.Fatalf("active series after pruning = %d, want 0 before the new admission", activeSeries)
	}
	t.Logf("step 3: retention deleted %d expired raw observation(s) and released the series slot", result.RawDeleted)

	insertRetentionObservation(t, db, retentionTestObservation(t, metric, 2, now, 99, "demo-series-b", cfg.TimeSeriesTimeObservedAt), policy)
	if err = (*db).QueryRow(`SELECT COUNT(*) FROM TimeSeriesActiveSeries WHERE metric_id = ?`, metric).Scan(&activeSeries); err != nil {
		t.Fatal(err)
	}
	if activeSeries != 1 {
		t.Fatalf("active series after re-admission = %d, want 1", activeSeries)
	}
	t.Logf("step 4: series B was admitted into the reclaimed slot")
	aggregates, err := QueryTimeSeriesAggregates(db, TimeSeriesQueryFilter{MetricID: &metric})
	if err != nil {
		t.Fatal(err)
	}
	if aggregates.Count != 1 || aggregates.Aggregates[0].NumericAverage == nil || *aggregates.Aggregates[0].NumericAverage != 10 {
		t.Fatalf("aggregates after raw retention = %#v, want the original average 10", aggregates)
	}
	t.Logf("step 5: %d aggregate bucket(s) survived raw pruning with average=10", aggregates.Count)
}

func TestPostgresTimeSeriesRetentionLeaseContentionIsTyped(t *testing.T) {
	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close() //nolint:errcheck
	database.SetMaxOpenConns(1)
	handler := Handler(&PostgresHandler{db: database, dbms: DBPostgresStr})
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT pg_try_advisory_lock(hashtext($1))`)).
		WithArgs(timeSeriesRetentionLockKey).
		WillReturnRows(sqlmock.NewRows([]string{"acquired"}).AddRow(false))
	lease, err := acquireTimeSeriesRetentionLease(context.Background(), &handler, DBPostgresStr)
	if lease != nil {
		t.Fatal("lease returned when retention advisory lock was already owned")
	}
	if !errors.Is(err, ErrTimeSeriesRetentionRunning) {
		t.Fatalf("error = %v, want ErrTimeSeriesRetentionRunning", err)
	}
	if stats := database.Stats(); stats.InUse != 0 {
		t.Fatalf("reserved connection remains in use: %+v", stats)
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteTimeSeriesRetentionLeaseIsProcessLocalNoOp(t *testing.T) {
	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close() //nolint:errcheck
	handler := Handler(&SQLiteHandler{db: database, dbms: DBSQLiteStr})
	lease, err := acquireTimeSeriesRetentionLease(context.Background(), &handler, DBSQLiteStr)
	if err != nil {
		t.Fatalf("acquire SQLite retention lease: %v", err)
	}
	if lease == nil || lease.conn != nil {
		t.Fatalf("SQLite lease = %#v, want non-nil no-op lease", lease)
	}
	if err = lease.release(); err != nil {
		t.Fatalf("release SQLite retention lease: %v", err)
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQLite lease unexpectedly accessed the database: %v", err)
	}
}

func TestTimeSeriesRetentionLeaseRejectsUnsupportedDBMS(t *testing.T) {
	lease, err := acquireTimeSeriesRetentionLease(context.Background(), nil, DBMySQLStr)
	if lease != nil {
		t.Fatal("lease returned for unsupported DBMS")
	}
	if err == nil || !strings.Contains(err.Error(), "unsupported database type for time-series retention lease: mysql") {
		t.Fatalf("error = %v, want explicit unsupported-DBMS error", err)
	}
}

func TestTimeSeriesRetentionNonDryRunAdvancesMonotonicWatermark(t *testing.T) {
	db, closeDB := openEntityTimeSeriesTestDB(t)
	defer closeDB()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	metric := retentionTestMetric(t, db, "retention-watermark", cfg.TimeSeriesTimeObservedAt, nil)
	insertRetentionObservation(t, db, retentionTestObservation(t, metric, 1, now.Add(-3*time.Hour), 1, "watermark-old", cfg.TimeSeriesTimeObservedAt), cfg.TimeSeriesCardinalityConfig{})
	if _, err := PruneTimeSeriesRetention(context.Background(), db, TimeSeriesRetentionOptions{Now: now, RawWindow: cfg.TimeSeriesRetentionWindow{Specified: true, Fixed: time.Hour}, BatchSize: 10, MaxBatches: 10, DryRun: true}); err != nil {
		t.Fatal(err)
	}
	watermark, err := timeSeriesRetentionWatermark(context.Background(), db, metric)
	if err != nil {
		t.Fatal(err)
	}
	if !watermark.IsZero() {
		t.Fatalf("dry run advanced watermark to %s", watermark)
	}
	if _, err = PruneTimeSeriesRetention(context.Background(), db, TimeSeriesRetentionOptions{Now: now, RawWindow: cfg.TimeSeriesRetentionWindow{Specified: true, Fixed: time.Hour}, BatchSize: 10, MaxBatches: 10}); err != nil {
		t.Fatal(err)
	}
	watermark, err = timeSeriesRetentionWatermark(context.Background(), db, metric)
	if err != nil {
		t.Fatal(err)
	}
	if !watermark.Equal(now.Add(-time.Hour)) {
		t.Fatalf("watermark = %s, want %s", watermark, now.Add(-time.Hour))
	}
}

func TestTimeSeriesRetentionPrunesOnlyExpiredAggregates(t *testing.T) {
	db, closeDB := openEntityTimeSeriesTestDB(t)
	defer closeDB()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	metric := retentionTestMetric(t, db, "retention-aggregate-window", cfg.TimeSeriesTimeObservedAt, nil)
	insertRetentionObservation(t, db, retentionTestObservation(t, metric, 1, now.Add(-4*time.Hour), 10, "aggregate-expired", cfg.TimeSeriesTimeObservedAt), cfg.TimeSeriesCardinalityConfig{})
	insertRetentionObservation(t, db, retentionTestObservation(t, metric, 2, now.Add(-30*time.Minute), 20, "aggregate-live", cfg.TimeSeriesTimeObservedAt), cfg.TimeSeriesCardinalityConfig{})
	for _, window := range []TimeSeriesRange{
		{Start: now.Add(-4 * time.Hour), End: now.Add(-3 * time.Hour)},
		{Start: now.Add(-time.Hour), End: now},
	} {
		if _, err := AggregateTimeSeriesRange(context.Background(), db, window, TimeSeriesAggregationOptions{BatchSize: 10, MaxBatches: 10, RunKey: "aggregate-retention-" + window.Start.Format(time.RFC3339)}); err != nil {
			t.Fatal(err)
		}
	}
	result, err := PruneTimeSeriesRetention(context.Background(), db, TimeSeriesRetentionOptions{
		Now:             now,
		RawWindow:       cfg.TimeSeriesRetentionWindow{Specified: true, Forever: true},
		AggregateWindow: cfg.TimeSeriesRetentionWindow{Specified: true, Fixed: 2 * time.Hour},
		BatchSize:       10,
		MaxBatches:      10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.AggregatesDeleted != 1 {
		t.Fatalf("aggregates deleted = %d, want 1", result.AggregatesDeleted)
	}
	aggregates, err := QueryTimeSeriesAggregates(db, TimeSeriesQueryFilter{MetricID: &metric})
	if err != nil {
		t.Fatal(err)
	}
	if aggregates.Count != 1 || aggregates.Aggregates[0].NumericAverage == nil || *aggregates.Aggregates[0].NumericAverage != 20 {
		t.Fatalf("remaining aggregates = %#v, want only the live bucket with average 20", aggregates)
	}
}

func TestTimeSeriesRetentionBatchBudgetIsGlobalAndReportsExhaustion(t *testing.T) {
	db, closeDB := openEntityTimeSeriesTestDB(t)
	defer closeDB()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	metric := retentionTestMetric(t, db, "retention-budget", cfg.TimeSeriesTimeObservedAt, nil)
	for index := 0; index < 5; index++ {
		insertRetentionObservation(t, db, retentionTestObservation(t, metric, uint64(index+1), now.Add(-2*time.Hour), float64(index), "budget-"+time.Duration(index).String(), cfg.TimeSeriesTimeObservedAt), cfg.TimeSeriesCardinalityConfig{})
	}
	result, err := PruneTimeSeriesRetention(context.Background(), db, TimeSeriesRetentionOptions{
		Now:        now,
		RawWindow:  cfg.TimeSeriesRetentionWindow{Specified: true, Fixed: time.Hour},
		BatchSize:  2,
		MaxBatches: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RawDeleted != 2 || !result.BudgetExhausted || result.BatchesProcessed != 1 {
		t.Fatalf("result = %#v, want one batch of 2 and budget exhaustion", result)
	}
	var remaining int
	if err = (*db).QueryRow(`SELECT COUNT(*) FROM TimeSeriesObservations WHERE metric_id = ?`, metric).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 3 {
		t.Fatalf("remaining observations = %d, want 3", remaining)
	}
}

func TestTimeSeriesRetentionWatermarkUsesBucketStart(t *testing.T) {
	db, closeDB := openEntityTimeSeriesTestDB(t)
	defer closeDB()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	metric := retentionTestMetric(t, db, "retention-mid-bucket", cfg.TimeSeriesTimeObservedAt, nil)
	observation := retentionTestObservation(t, metric, 1, now.Add(-2*time.Hour), 7, "mid-bucket-old", cfg.TimeSeriesTimeObservedAt)
	insertRetentionObservation(t, db, observation, cfg.TimeSeriesCardinalityConfig{})
	if _, err := PruneTimeSeriesRetention(context.Background(), db, TimeSeriesRetentionOptions{
		Now:        now,
		RawWindow:  cfg.TimeSeriesRetentionWindow{Specified: true, Fixed: 90 * time.Minute},
		BatchSize:  10,
		MaxBatches: 10,
	}); err != nil {
		t.Fatal(err)
	}
	// The requested range starts after the watermark, but its 1h bucket starts
	// before it. Replacing that bucket would use incomplete raw history.
	rangeToAggregate := TimeSeriesRange{Start: now.Add(-75 * time.Minute), End: now.Add(-30 * time.Minute)}
	result, err := AggregateTimeSeriesRange(context.Background(), db, rangeToAggregate, TimeSeriesAggregationOptions{BatchSize: 10, MaxBatches: 10, RunKey: "mid-bucket-rebuild"})
	if err != nil && !errors.Is(err, ErrTimeSeriesAggregationIncompleteHistory) {
		t.Fatalf("mid-bucket rebuild error = %v", err)
	}
	if err == nil && result.MetricsSkipped == 0 {
		t.Fatal("mid-bucket rebuild reported neither an error nor a skipped metric")
	}
}

func TestTimeSeriesIncrementalAggregationSkipsPrunedBucket(t *testing.T) {
	db, closeDB := openEntityTimeSeriesTestDB(t)
	defer closeDB()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	metric := retentionTestMetric(t, db, "retention-incremental", cfg.TimeSeriesTimeObservedAt, nil)
	for index, at := range []time.Time{now.Add(-50 * time.Minute), now.Add(-10 * time.Minute)} {
		insertRetentionObservation(t, db, retentionTestObservation(t, metric, 1, at, float64(index*10+10), "incremental-"+time.Duration(index).String(), cfg.TimeSeriesTimeObservedAt), cfg.TimeSeriesCardinalityConfig{})
	}
	bucketRange := TimeSeriesRange{Start: now.Add(-time.Hour), End: now}
	if _, err := AggregateTimeSeriesRange(context.Background(), db, bucketRange, TimeSeriesAggregationOptions{BatchSize: 10, MaxBatches: 10, RunKey: "incremental-before-retention"}); err != nil {
		t.Fatal(err)
	}
	// Retention runs while the caller does not protect the aggregation
	// checkpoint, which advances the watermark past the checkpoint.
	if _, err := PruneTimeSeriesRetention(context.Background(), db, TimeSeriesRetentionOptions{
		Now:        now,
		RawWindow:  cfg.TimeSeriesRetentionWindow{Specified: true, Fixed: 5 * time.Minute},
		BatchSize:  10,
		MaxBatches: 10,
	}); err != nil {
		t.Fatal(err)
	}
	runKey := "incremental-after-retention"
	if _, err := (*db).Exec(`INSERT INTO TimeSeriesAggregationRuns (run_key,status,checkpoint_at) VALUES (?,?,?)`, runKey, "completed", now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	result, err := RunTimeSeriesAggregation(context.Background(), db, TimeSeriesAggregationOptions{BatchSize: 10, MaxBatches: 10, Overlap: 15 * time.Minute, Now: now, RunKey: runKey})
	if err != nil {
		t.Fatal(err)
	}
	if result.MetricsSkipped == 0 || result.AggregatesReplaced != 0 {
		t.Fatalf("incremental result = %#v, want the incomplete bucket skipped without replacement", result)
	}
	aggregates, err := QueryTimeSeriesAggregates(db, TimeSeriesQueryFilter{MetricID: &metric})
	if err != nil {
		t.Fatal(err)
	}
	if aggregates.Count != 1 || aggregates.Aggregates[0].NumericAverage == nil || *aggregates.Aggregates[0].NumericAverage != 15 {
		t.Fatalf("aggregates = %#v, want the original average 15 preserved", aggregates)
	}
}

func TestPostgresTimeSeriesRetentionMaintenanceLeaseAcquiresBothLocksOnOneSession(t *testing.T) {
	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close() //nolint:errcheck
	database.SetMaxOpenConns(1)
	handler := Handler(&PostgresHandler{db: database, dbms: DBPostgresStr})
	for _, key := range []string{timeSeriesRetentionLockKey, timeSeriesAggregationLockKey} {
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT pg_try_advisory_lock(hashtext($1))`)).
			WithArgs(key).
			WillReturnRows(sqlmock.NewRows([]string{"acquired"}).AddRow(true))
	}
	lease, err := acquireTimeSeriesRetentionMaintenanceLease(context.Background(), &handler, DBPostgresStr)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{timeSeriesRetentionLockKey, timeSeriesAggregationLockKey} {
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT pg_advisory_unlock(hashtext($1))`)).
			WithArgs(key).
			WillReturnRows(sqlmock.NewRows([]string{"unlocked"}).AddRow(true))
	}
	if err = lease.release(); err != nil {
		t.Fatal(err)
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestTimeSeriesRetentionHonoursCancellationBeforeWork(t *testing.T) {
	db, closeDB := openEntityTimeSeriesTestDB(t)
	defer closeDB()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	metric := retentionTestMetric(t, db, "retention-cancel", cfg.TimeSeriesTimeObservedAt, nil)
	insertRetentionObservation(t, db, retentionTestObservation(t, metric, 1, now.Add(-2*time.Hour), 1, "cancel-old", cfg.TimeSeriesTimeObservedAt), cfg.TimeSeriesCardinalityConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := PruneTimeSeriesRetention(ctx, db, TimeSeriesRetentionOptions{
		Now:        now,
		RawWindow:  cfg.TimeSeriesRetentionWindow{Specified: true, Fixed: time.Hour},
		BatchSize:  10,
		MaxBatches: 10,
	}); err == nil {
		t.Fatal("cancelled retention pass returned success")
	}
	var remaining int
	if err := (*db).QueryRow(`SELECT COUNT(*) FROM TimeSeriesObservations WHERE metric_id = ?`, metric).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Fatalf("cancelled retention deleted %d observations", 1-remaining)
	}
}

func TestTimeSeriesRetentionExactBatchCompletionIsNotBudgetExhaustion(t *testing.T) {
	db, closeDB := openEntityTimeSeriesTestDB(t)
	defer closeDB()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	metric := retentionTestMetric(t, db, "retention-exact-budget", cfg.TimeSeriesTimeObservedAt, nil)
	for index := 0; index < 2; index++ {
		insertRetentionObservation(t, db, retentionTestObservation(t, metric, uint64(index+1), now.Add(-2*time.Hour), float64(index), "exact-budget-"+time.Duration(index).String(), cfg.TimeSeriesTimeObservedAt), cfg.TimeSeriesCardinalityConfig{})
	}
	result, err := PruneTimeSeriesRetention(context.Background(), db, TimeSeriesRetentionOptions{
		Now:        now,
		RawWindow:  cfg.TimeSeriesRetentionWindow{Specified: true, Fixed: time.Hour},
		BatchSize:  2,
		MaxBatches: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RawDeleted != 2 || result.BudgetExhausted {
		t.Fatalf("result = %#v, want all work completed without budget exhaustion", result)
	}
}

func TestTimeSeriesIncrementalAggregationDoesNotPartiallyRebuildStraddlingBucket(t *testing.T) {
	db, closeDB := openEntityTimeSeriesTestDB(t)
	defer closeDB()
	now := time.Date(2026, 6, 1, 13, 0, 0, 0, time.UTC)
	metric := retentionTestMetric(t, db, "retention-straddling-bucket", cfg.TimeSeriesTimeObservedAt, nil)
	for index, at := range []time.Time{now.Add(-50 * time.Minute), now.Add(-10 * time.Minute)} {
		insertRetentionObservation(t, db, retentionTestObservation(t, metric, 1, at, float64(index*10+10), "straddling-"+time.Duration(index).String(), cfg.TimeSeriesTimeObservedAt), cfg.TimeSeriesCardinalityConfig{})
	}
	// Simulate retention that advanced beyond the aggregation checkpoint while
	// aggregation was disabled. The watermark is inside the 12:00-13:00 bucket.
	if _, err := (*db).Exec(`INSERT INTO TimeSeriesAggregationRuns (run_key,status,checkpoint_at) VALUES (?,?,?)`, timeSeriesRetentionRunKey(metric), "completed", now.Add(-53*time.Minute)); err != nil {
		t.Fatal(err)
	}
	runKey := "straddling-incremental"
	if _, err := (*db).Exec(`INSERT INTO TimeSeriesAggregationRuns (run_key,status,checkpoint_at) VALUES (?,?,?)`, runKey, "completed", now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	result, err := RunTimeSeriesAggregation(context.Background(), db, TimeSeriesAggregationOptions{BatchSize: 10, MaxBatches: 10, Overlap: 15 * time.Minute, Now: now, RunKey: runKey})
	if err != nil {
		t.Fatal(err)
	}
	if result.MetricsSkipped == 0 || result.AggregatesReplaced != 0 {
		t.Fatalf("incremental result = %#v, want the straddling bucket skipped", result)
	}
	aggregates, err := QueryTimeSeriesAggregates(db, TimeSeriesQueryFilter{MetricID: &metric})
	if err != nil {
		t.Fatal(err)
	}
	if aggregates.Count != 0 {
		t.Fatalf("straddling bucket was partially rebuilt: %#v", aggregates)
	}
	var raw int
	if err = (*db).QueryRow(`SELECT COUNT(*) FROM TimeSeriesObservations WHERE metric_id = ?`, metric).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != 2 {
		t.Fatalf("retained raw observations = %d, want 2", raw)
	}
}

func TestPostgresTimeSeriesRetentionMaintenanceLeaseReportsAggregationBusy(t *testing.T) {
	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close() //nolint:errcheck
	database.SetMaxOpenConns(1)
	handler := Handler(&PostgresHandler{db: database, dbms: DBPostgresStr})
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT pg_try_advisory_lock(hashtext($1))`)).
		WithArgs(timeSeriesRetentionLockKey).
		WillReturnRows(sqlmock.NewRows([]string{"acquired"}).AddRow(true))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT pg_try_advisory_lock(hashtext($1))`)).
		WithArgs(timeSeriesAggregationLockKey).
		WillReturnRows(sqlmock.NewRows([]string{"acquired"}).AddRow(false))
	lease, err := acquireTimeSeriesRetentionMaintenanceLease(context.Background(), &handler, DBPostgresStr)
	if lease != nil {
		t.Fatal("lease returned when aggregation lock was unavailable")
	}
	if !errors.Is(err, ErrTimeSeriesRetentionAggregationBusy) {
		t.Fatalf("error = %v, want ErrTimeSeriesRetentionAggregationBusy", err)
	}
	if stats := database.Stats(); stats.InUse != 0 {
		t.Fatalf("reserved connection remains in use: %+v", stats)
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
