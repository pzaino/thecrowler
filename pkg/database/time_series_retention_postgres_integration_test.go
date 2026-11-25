//go:build integration

package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func TestPostgresTimeSeriesRetentionReclaimsSlotsAndProtectsAggregates(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	db, sqlDB := openPostgresIntegrationTestDB(t)
	now := time.Now().UTC().Truncate(time.Hour)
	suffix := fmt.Sprint(time.Now().UnixNano())
	policy := cfg.TimeSeriesCardinalityConfig{MaxSeriesPerMetric: 1, MaxDimensions: 1, MaxValuesPerDimension: 10, Overflow: cfg.TimeSeriesCardinalityDrop}
	metric, err := UpsertTimeSeriesMetric(db, &TimeSeriesMetric{
		Key: "retention-postgres-" + suffix, DisplayName: "retention postgres", SourceKind: cfg.TimeSeriesSourceCustom,
		ValueType: cfg.TimeSeriesValueDecimal, Aggregate: cfg.TimeSeriesAggregateAverage, Bucket: cfg.TimeSeriesBucketOneHour,
		TimeBasis: cfg.TimeSeriesTimeObservedAt, DedupeScope: cfg.TimeSeriesDedupeObject, FailurePolicy: cfg.TimeSeriesFailureLogSkip,
		Selector: json.RawMessage(`{}`), Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	aggregationRunKey := "retention-postgres-aggregation-" + suffix
	t.Cleanup(func() {
		_, _ = sqlDB.Exec(`DELETE FROM TimeSeriesAggregationRuns WHERE run_key=$1 OR run_key=$2`, aggregationRunKey, timeSeriesRetentionRunKey(metric.ID))
		_, _ = sqlDB.Exec(`DELETE FROM TimeSeriesAggregates WHERE metric_id=$1`, metric.ID)
		_, _ = sqlDB.Exec(`DELETE FROM TimeSeriesObservations WHERE metric_id=$1`, metric.ID)
		_, _ = sqlDB.Exec(`DELETE FROM TimeSeriesSeriesSlots WHERE metric_id=$1`, metric.ID)
		_, _ = sqlDB.Exec(`DELETE FROM TimeSeriesDimensionSlots WHERE metric_id=$1`, metric.ID)
		_, _ = sqlDB.Exec(`DELETE FROM TimeSeriesActiveSeries WHERE metric_id=$1`, metric.ID)
		_, _ = sqlDB.Exec(`DELETE FROM TimeSeriesActiveDimensionValues WHERE metric_id=$1`, metric.ID)
		_, _ = sqlDB.Exec(`DELETE FROM TimeSeriesMetrics WHERE metric_id=$1`, metric.ID)
	})

	insert := func(subject uint64, at time.Time, value float64, key string) error {
		tx, beginErr := sqlDB.BeginTx(ctx, nil)
		if beginErr != nil {
			return beginErr
		}
		bucketStart, bucketEnd, boundsErr := TimeSeriesBucketBounds(at, cfg.TimeSeriesBucketOneHour)
		if boundsErr != nil {
			_ = tx.Rollback()
			return boundsErr
		}
		observation := TimeSeriesObservation{
			MetricID: metric.ID, ObservedAt: at, CollectedAt: now, BucketStart: bucketStart, BucketEnd: bucketEnd,
			Scope: TimeSeriesScope{SubjectType: "retention_pg", SubjectID: &subject},
			Value: TimeSeriesValue{Numeric: &value}, ValueHash: key + "-value", DedupeKey: key,
		}
		_, insertErr := (TransactionTimeSeriesRepository{Tx: tx, DBMS: DBPostgresStr}).InsertObservationWithCardinalityContext(ctx, &observation, policy, cfg.TimeSeriesFailureLogSkip)
		if insertErr != nil {
			_ = tx.Rollback()
			return insertErr
		}
		return tx.Commit()
	}

	if err = insert(1, now.Add(-2*time.Hour), 10, "pg-retention-old-"+suffix); err != nil {
		t.Fatal(err)
	}
	rangeToAggregate := TimeSeriesRange{Start: now.Add(-2 * time.Hour), End: now.Add(-time.Hour)}
	if _, err = AggregateTimeSeriesRange(ctx, db, rangeToAggregate, TimeSeriesAggregationOptions{BatchSize: 10, MaxBatches: 10, RunKey: aggregationRunKey}); err != nil {
		t.Fatal(err)
	}
	if err = insert(2, now, 99, "pg-retention-blocked-"+suffix); !errors.Is(err, ErrTimeSeriesValueRejected) {
		t.Fatalf("second series insert = %v, want ErrTimeSeriesValueRejected", err)
	}

	result, err := PruneTimeSeriesRetention(ctx, db, TimeSeriesRetentionOptions{
		Now:             now,
		RawWindow:       cfg.TimeSeriesRetentionWindow{Specified: true, Fixed: time.Hour},
		AggregateWindow: cfg.TimeSeriesRetentionWindow{Specified: true, Forever: true},
		BatchSize:       10,
		MaxBatches:      10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RawDeleted != 1 || result.WatermarksUpdated != 1 {
		t.Fatalf("retention result = %#v", result)
	}
	var slots, active, aggregates int
	if err = sqlDB.QueryRow(`SELECT COUNT(*) FROM TimeSeriesSeriesSlots WHERE metric_id=$1`, metric.ID).Scan(&slots); err != nil {
		t.Fatal(err)
	}
	if err = sqlDB.QueryRow(`SELECT COUNT(*) FROM TimeSeriesActiveSeries WHERE metric_id=$1`, metric.ID).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if err = sqlDB.QueryRow(`SELECT COUNT(*) FROM TimeSeriesAggregates WHERE metric_id=$1`, metric.ID).Scan(&aggregates); err != nil {
		t.Fatal(err)
	}
	if slots != 0 || active != 0 || aggregates != 1 {
		t.Fatalf("after retention slots=%d active=%d aggregates=%d, want 0/0/1", slots, active, aggregates)
	}
	var watermark time.Time
	if err = sqlDB.QueryRow(`SELECT checkpoint_at FROM TimeSeriesAggregationRuns WHERE run_key=$1`, timeSeriesRetentionRunKey(metric.ID)).Scan(&watermark); err != nil {
		t.Fatal(err)
	}
	if !watermark.Equal(now.Add(-time.Hour)) {
		t.Fatalf("watermark = %s, want %s", watermark, now.Add(-time.Hour))
	}

	// The new identity can now claim the released capacity.
	if err = insert(2, now, 99, "pg-retention-admitted-"+suffix); err != nil {
		t.Fatalf("insert after capacity release: %v", err)
	}

	// Rebuilding the pruned range must not replace the materialized aggregate
	// with a value computed from incomplete raw history. Other metrics may exist
	// in a shared integration database, so the guarded metric may be omitted
	// (MetricsSkipped) instead of failing the whole run.
	rebuildResult, rebuildErr := AggregateTimeSeriesRange(ctx, db, rangeToAggregate, TimeSeriesAggregationOptions{BatchSize: 10, MaxBatches: 10, RunKey: aggregationRunKey + "-rebuild"})
	if rebuildErr != nil && !errors.Is(rebuildErr, ErrTimeSeriesAggregationIncompleteHistory) {
		t.Fatalf("incomplete rebuild error = %v", rebuildErr)
	}
	if rebuildErr == nil && rebuildResult.MetricsSkipped == 0 {
		t.Fatal("incomplete rebuild reported neither an error nor a skipped metric")
	}
	var average float64
	if err = sqlDB.QueryRow(`SELECT numeric_avg FROM TimeSeriesAggregates WHERE metric_id=$1`, metric.ID).Scan(&average); err != nil {
		t.Fatal(err)
	}
	if average != 10 {
		t.Fatalf("aggregate average = %v, want the preserved 10", average)
	}
}
