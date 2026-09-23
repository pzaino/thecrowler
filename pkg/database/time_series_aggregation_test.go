package database

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func TestPostgresTimeSeriesAggregationLeaseUsesRetainedConnection(t *testing.T) {
	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close() //nolint:errcheck

	handler := Handler(&PostgresHandler{db: database, dbms: DBPostgresStr})
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT pg_try_advisory_lock(hashtext($1))`)).
		WithArgs(timeSeriesAggregationLockKey).
		WillReturnRows(sqlmock.NewRows([]string{"acquired"}).AddRow(true))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT pg_advisory_unlock(hashtext($1))`)).
		WithArgs(timeSeriesAggregationLockKey).
		WillReturnRows(sqlmock.NewRows([]string{"unlocked"}).AddRow(true))

	ctx, cancel := context.WithCancel(context.Background())
	lease, err := acquireTimeSeriesAggregationLease(ctx, &handler, DBPostgresStr)
	if err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	cancel()
	if err = lease.release(); err != nil {
		t.Fatalf("release lease after aggregation context cancellation: %v", err)
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresTimeSeriesAggregationLeaseClosesWhenAlreadyOwned(t *testing.T) {
	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close() //nolint:errcheck

	database.SetMaxOpenConns(1)
	handler := Handler(&PostgresHandler{db: database, dbms: DBPostgresStr})
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT pg_try_advisory_lock(hashtext($1))`)).
		WithArgs(timeSeriesAggregationLockKey).
		WillReturnRows(sqlmock.NewRows([]string{"acquired"}).AddRow(false))

	lease, err := acquireTimeSeriesAggregationLease(context.Background(), &handler, DBPostgresStr)
	if lease != nil {
		t.Fatal("lease returned when advisory lock was already owned")
	}
	if !errors.Is(err, ErrTimeSeriesAggregationRunning) {
		t.Fatalf("error = %v, want ErrTimeSeriesAggregationRunning", err)
	}
	if stats := database.Stats(); stats.InUse != 0 {
		t.Fatalf("reserved connection remains in use: %+v", stats)
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteTimeSeriesAggregationLeaseIsProcessLocalNoOp(t *testing.T) {
	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close() //nolint:errcheck

	handler := Handler(&SQLiteHandler{db: database, dbms: DBSQLiteStr})
	lease, err := acquireTimeSeriesAggregationLease(context.Background(), &handler, DBSQLiteStr)
	if err != nil {
		t.Fatalf("acquire SQLite lease: %v", err)
	}
	if lease == nil || lease.conn != nil {
		t.Fatalf("SQLite lease = %#v, want non-nil no-op lease", lease)
	}
	if err = lease.release(); err != nil {
		t.Fatalf("release SQLite lease: %v", err)
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQLite lease unexpectedly accessed the database: %v", err)
	}
}

func TestTimeSeriesAggregationLeaseRejectsUnsupportedDBMS(t *testing.T) {
	lease, err := acquireTimeSeriesAggregationLease(context.Background(), nil, DBMySQLStr)
	if lease != nil {
		t.Fatalf("lease = %#v, want nil", lease)
	}
	if err == nil || !strings.Contains(err.Error(), "unsupported database type for time-series aggregation lease: mysql") {
		t.Fatalf("error = %v, want explicit unsupported-DBMS error", err)
	}
}

func TestRunTimeSeriesAggregationLeaseContentionIsIndependentOfRunKey(t *testing.T) {
	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close() //nolint:errcheck

	handler := Handler(&PostgresHandler{db: database, dbms: DBPostgresStr})
	for _, runKey := range []string{"tenant-a", "tenant-b"} {
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT pg_try_advisory_lock(hashtext($1))`)).
			WithArgs(timeSeriesAggregationLockKey).
			WillReturnRows(sqlmock.NewRows([]string{"acquired"}).AddRow(false))

		_, err = RunTimeSeriesAggregation(
			context.Background(),
			&handler,
			TimeSeriesAggregationOptions{RunKey: runKey},
		)
		if !errors.Is(err, ErrTimeSeriesAggregationRunning) {
			t.Fatalf("RunKey %q: error = %v, want ErrTimeSeriesAggregationRunning", runKey, err)
		}
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("aggregation accessed metrics, checkpoints, observations, or replacements after lease rejection: %v", err)
	}
}

func TestTimeSeriesAggregationLeaseReleaseUsesFreshContextAndClosesLast(t *testing.T) {
	primaryErr := errors.New("aggregation failed")
	unlockErr := errors.New("unlock failed")
	closeErr := errors.New("close failed")
	events := make([]string, 0, 2)
	lease := &timeSeriesAggregationLease{
		key: timeSeriesAggregationLockKey,
		unlock: func(ctx context.Context, key string) (bool, error) {
			events = append(events, "unlock")
			if key != timeSeriesAggregationLockKey {
				t.Fatalf("unlock key = %q, want %q", key, timeSeriesAggregationLockKey)
			}
			if err := ctx.Err(); err != nil {
				t.Fatalf("unlock context is already done: %v", err)
			}
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Fatal("unlock context has no deadline")
			}
			remaining := time.Until(deadline)
			if remaining <= 0 || remaining > timeSeriesAggregationReleaseTimeout {
				t.Fatalf("unlock context deadline remaining = %v, want (0, %v]", remaining, timeSeriesAggregationReleaseTimeout)
			}
			return false, unlockErr
		},
		close: func() error {
			events = append(events, "close")
			return closeErr
		},
	}

	err := releaseTimeSeriesAggregationLease(lease, primaryErr)
	if !errors.Is(err, primaryErr) || !errors.Is(err, unlockErr) || !errors.Is(err, closeErr) {
		t.Fatalf("aggregation error = %v, want primary, unlock, and close errors", err)
	}
	if got := strings.Join(events, ","); got != "unlock,close" {
		t.Fatalf("cleanup order = %q, want unlock,close", got)
	}
}

func TestRunTimeSeriesAggregationJoinsWorkAndLeaseReleaseErrors(t *testing.T) {
	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close() //nolint:errcheck

	workErr := errors.New("metric query failed")
	releaseErr := errors.New("advisory unlock failed")
	handler := Handler(&PostgresHandler{db: database, dbms: DBPostgresStr})
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT pg_try_advisory_lock(hashtext($1))`)).
		WithArgs(timeSeriesAggregationLockKey).
		WillReturnRows(sqlmock.NewRows([]string{"acquired"}).AddRow(true))
	mock.ExpectQuery(`SELECT .* FROM TimeSeriesMetrics`).
		WillReturnError(workErr)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT pg_advisory_unlock(hashtext($1))`)).
		WithArgs(timeSeriesAggregationLockKey).
		WillReturnError(releaseErr)

	_, err = RunTimeSeriesAggregation(
		context.Background(),
		&handler,
		TimeSeriesAggregationOptions{},
	)
	if !errors.Is(err, workErr) {
		t.Fatalf("error = %v, want original work error", err)
	}
	if !errors.Is(err, releaseErr) {
		t.Fatalf("error = %v, want lease release error", err)
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

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

func TestTimeSeriesAggregationIncrementalCompletesBucketLargerThanConfiguredPageBudget(t *testing.T) {
	db, closeDB := openEntityTimeSeriesTestDB(t)
	defer closeDB()

	metric, err := UpsertTimeSeriesMetric(
		db,
		&TimeSeriesMetric{
			Key:           "aggregation-large-daily",
			DisplayName:   "aggregation-large-daily",
			SourceKind:    cfg.TimeSeriesSourceCustom,
			ValueType:     cfg.TimeSeriesValueInteger,
			Aggregate:     cfg.TimeSeriesAggregateAverage,
			Bucket:        cfg.TimeSeriesBucketOneDay,
			TimeBasis:     cfg.TimeSeriesTimeObservedAt,
			DedupeScope:   cfg.TimeSeriesDedupeObject,
			ObjectType:    cfg.TimeSeriesObjectWebObject,
			FailurePolicy: cfg.TimeSeriesFailureLogSkip,
			Selector:      []byte(`{}`),
			Enabled:       true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	start := time.Date(
		2026,
		time.August,
		25,
		0,
		0,
		0,
		0,
		time.UTC,
	)

	for i := 0; i < 25; i++ {
		value := int64(i)

		observation := TimeSeriesObservation{
			MetricID:    metric.ID,
			ObservedAt:  start.Add(time.Duration(i) * time.Minute),
			CollectedAt: start.Add(time.Duration(i) * time.Minute),
			BucketStart: start,
			BucketEnd:   start.Add(24 * time.Hour),
			Scope: TimeSeriesScope{
				ObjectType: "webobject",
				ObjectID:   timeSeriesUint64Pointer(uint64(i + 1)),
			},
			Value: TimeSeriesValue{
				Integer: &value,
			},
			ValueHash: fmt.Sprintf("value-%d", i),
			DedupeKey: fmt.Sprintf("large-daily-%d", i),
		}

		if _, insertErr := InsertTimeSeriesObservation(
			db,
			&observation,
		); insertErr != nil {
			t.Fatal(insertErr)
		}
	}

	result, err := RunTimeSeriesAggregation(
		context.Background(),
		db,
		TimeSeriesAggregationOptions{
			BatchSize:  2,
			MaxBatches: 1,
			Now:        start.Add(12 * time.Hour),
			RunKey:     "test-large-daily",
		},
	)
	if err != nil {
		t.Fatalf("aggregation failed: %v", err)
	}

	if result.ObservationsProcessed != 25 {
		t.Fatalf(
			"observations processed = %d, want 25",
			result.ObservationsProcessed,
		)
	}

	if result.WindowsProcessed != 1 {
		t.Fatalf(
			"windows processed = %d, want 1",
			result.WindowsProcessed,
		)
	}

	aggregates, err := QueryTimeSeriesAggregates(
		db,
		TimeSeriesQueryFilter{
			MetricID: &metric.ID,
			Pagination: TimeSeriesPagination{
				Limit: 100,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if aggregates.Count != 25 {
		t.Fatalf(
			"aggregate count = %d, want 25",
			aggregates.Count,
		)
	}
}

func TestEarliestTimeSeriesObservationSQLite(t *testing.T) {
	db, closeDB := openEntityTimeSeriesTestDB(t)
	defer closeDB()

	metric, err := UpsertTimeSeriesMetric(
		db,
		&TimeSeriesMetric{
			Key:           "earliest-observation",
			DisplayName:   "earliest-observation",
			SourceKind:    cfg.TimeSeriesSourceCustom,
			ValueType:     cfg.TimeSeriesValueInteger,
			Aggregate:     cfg.TimeSeriesAggregateAverage,
			Bucket:        cfg.TimeSeriesBucketOneDay,
			TimeBasis:     cfg.TimeSeriesTimeObservedAt,
			DedupeScope:   cfg.TimeSeriesDedupeObject,
			ObjectType:    cfg.TimeSeriesObjectWebObject,
			FailurePolicy: cfg.TimeSeriesFailureLogSkip,
			Selector:      []byte(`{}`),
			Enabled:       true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	start := time.Date(
		2026,
		time.August,
		25,
		0,
		0,
		0,
		0,
		time.UTC,
	)

	value := int64(1)

	observation := TimeSeriesObservation{
		MetricID:    metric.ID,
		ObservedAt:  start,
		CollectedAt: start,
		BucketStart: start,
		BucketEnd:   start.Add(24 * time.Hour),
		Value: TimeSeriesValue{
			Integer: &value,
		},
		ValueHash: "earliest-value",
		DedupeKey: "earliest-observation",
	}

	if _, err = InsertTimeSeriesObservation(
		db,
		&observation,
	); err != nil {
		t.Fatal(err)
	}

	got, err := earliestTimeSeriesObservation(db)
	if err != nil {
		t.Fatal(err)
	}

	if !got.Equal(start) {
		t.Fatalf(
			"earliest observation = %s, want %s",
			got,
			start,
		)
	}
}
