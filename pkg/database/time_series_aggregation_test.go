package database

import (
	"context"
	"math"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func TestTimeSeriesAggregationPercentilesDeterministic(t *testing.T) {
	values := []float64{1, 2, 3, 4, 5}
	for percentile, want := range map[float64]float64{.50: 3, .75: 4, .90: 4.6, .95: 4.8, .99: 4.96} {
		if got := TimeSeriesPercentile(values, percentile); math.Abs(got-want) > 1e-9 {
			t.Fatalf("p%.0f = %v, want %v", percentile*100, got, want)
		}
	}
}

func TestTimeSeriesAggregationAllFunctionsAndLateObservation(t *testing.T) {
	db, closeDB := openEntityTimeSeriesTestDB(t)
	defer closeDB()
	metric, err := UpsertTimeSeriesMetric(db, &TimeSeriesMetric{Key: "aggregation-all", DisplayName: "aggregation-all", SourceKind: cfg.TimeSeriesSourceCustom, ValueType: cfg.TimeSeriesValueDecimal, Aggregate: cfg.TimeSeriesAggregateAverage, Bucket: cfg.TimeSeriesBucketOneHour, TimeBasis: cfg.TimeSeriesTimeObservedAt, DedupeScope: cfg.TimeSeriesDedupeObject, ObjectType: cfg.TimeSeriesObjectWebObject, FailurePolicy: cfg.TimeSeriesFailureLogSkip, Selector: []byte(`{}`), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	createdEntity, entityErr := UpsertEntity(db, &Entity{Type: "organization"})
	if entityErr != nil {
		t.Fatal(entityErr)
	}
	entity := createdEntity.ID
	insert := func(id int, minute int, value float64, changed bool) {
		o := TimeSeriesObservation{MetricID: metric.ID, ObservedAt: start.Add(time.Duration(minute) * time.Minute), CollectedAt: start, BucketStart: start, BucketEnd: start.Add(time.Hour), Scope: TimeSeriesScope{EntityID: &entity, ObjectType: "webobject", ObjectID: timeSeriesUint64Pointer(9)}, Value: TimeSeriesValue{Numeric: &value}, ValueHash: string(rune('a' + id)), DedupeKey: "aggregation-" + string(rune('a'+id)), IsChanged: changed, Dimensions: map[string]interface{}{"region": "us"}}
		if _, insertErr := InsertTimeSeriesObservation(db, &o); insertErr != nil {
			t.Fatal(insertErr)
		}
	}
	insert(0, 1, 1, false)
	insert(1, 2, 2, true)
	insert(2, 3, 3, true)
	insert(3, 4, 4, false)
	rangeToRun := TimeSeriesRange{Start: start, End: start.Add(time.Hour)}
	if _, err = AggregateTimeSeriesRange(context.Background(), db, rangeToRun, TimeSeriesAggregationOptions{BatchSize: 2, MaxBatches: 10, RunKey: "test-all"}); err != nil {
		t.Fatal(err)
	}
	result, err := QueryTimeSeriesAggregates(db, TimeSeriesQueryFilter{MetricID: &metric.ID, Pagination: TimeSeriesPagination{Limit: 10}})
	if err != nil || len(result.Aggregates) != 1 {
		t.Fatalf("aggregates=%d err=%v", len(result.Aggregates), err)
	}
	a := result.Aggregates[0]
	if a.ValueCount != 4 || a.DistinctValueCount != 4 || a.NumericCount != 4 || a.ChangeCount != 2 || a.NumericSum == nil || *a.NumericSum != 10 || a.NumericAverage == nil || *a.NumericAverage != 2.5 || a.Percentile50 == nil || *a.Percentile50 != 2.5 {
		t.Fatalf("unexpected aggregate: %#v", a)
	}
	insert(4, 5, 10, true)
	if _, err = AggregateTimeSeriesRange(context.Background(), db, rangeToRun, TimeSeriesAggregationOptions{BatchSize: 10, MaxBatches: 10, RunKey: "test-all"}); err != nil {
		t.Fatal(err)
	}
	result, _ = QueryTimeSeriesAggregates(db, TimeSeriesQueryFilter{MetricID: &metric.ID, Pagination: TimeSeriesPagination{Limit: 10}})
	if len(result.Aggregates) != 1 || result.Aggregates[0].ValueCount != 5 || *result.Aggregates[0].NumericSum != 20 {
		t.Fatalf("late observation not incorporated: %#v", result.Aggregates)
	}
}

func TestTimeSeriesRetentionDryRunAndMetricOverride(t *testing.T) {
	db, closeDB := openEntityTimeSeriesTestDB(t)
	defer closeDB()
	metric, err := UpsertTimeSeriesMetric(db, &TimeSeriesMetric{Key: "retention", DisplayName: "retention", SourceKind: cfg.TimeSeriesSourceCustom, ValueType: cfg.TimeSeriesValueDecimal, Aggregate: cfg.TimeSeriesAggregateAverage, Bucket: cfg.TimeSeriesBucketOneHour, TimeBasis: cfg.TimeSeriesTimeObservedAt, DedupeScope: cfg.TimeSeriesDedupeGlobal, ObjectType: cfg.TimeSeriesObjectWebObject, FailurePolicy: cfg.TimeSeriesFailureLogSkip, Selector: []byte(`{}`), RetentionPolicy: []byte(`{"raw":"1h","aggregated":"2h"}`), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	value := 1.0
	o := TimeSeriesObservation{MetricID: metric.ID, ObservedAt: now.Add(-2 * time.Hour), CollectedAt: now, BucketStart: now.Add(-2 * time.Hour), BucketEnd: now.Add(-time.Hour), Value: TimeSeriesValue{Numeric: &value}, ValueHash: "old", DedupeKey: "old"}
	if _, err = InsertTimeSeriesObservation(db, &o); err != nil {
		t.Fatal(err)
	}
	dry, err := PruneTimeSeriesRetention(context.Background(), db, TimeSeriesRetentionOptions{Now: now, RawRetention: 24 * time.Hour, AggregateRetention: 24 * time.Hour, DryRun: true})
	if err != nil || dry.RawCandidates != 1 || dry.RawDeleted != 0 {
		t.Fatalf("dry run %#v %v", dry, err)
	}
	pruned, err := PruneTimeSeriesRetention(context.Background(), db, TimeSeriesRetentionOptions{Now: now, RawRetention: 24 * time.Hour, AggregateRetention: 24 * time.Hour, BatchSize: 10, MaxBatches: 1})
	if err != nil || pruned.RawDeleted != 1 {
		t.Fatalf("prune %#v %v", pruned, err)
	}
	if _, err = GetTimeSeriesMetricByID(db, metric.ID); err != nil {
		t.Fatalf("metric definition deleted: %v", err)
	}
}
