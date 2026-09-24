package database

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func TestRetryPostgresTransaction(t *testing.T) {
	serializationFailure := func() error { return &pq.Error{Code: "40001"} }
	deadlock := func() error { return &pq.Error{Code: "40P01"} }
	foreignKeyViolation := func() error { return &pq.Error{Code: "23503"} }
	uniqueViolation := func() error { return &pq.Error{Code: "23505"} }
	arbitraryError := errors.New("query failed")

	tests := []struct {
		name        string
		errors      []error
		cancelAfter int
		wantCalls   int
		wantErr     error
		wantState   string
	}{
		{name: "serialization failure eligible", errors: []error{serializationFailure(), nil}, wantCalls: 2},
		{name: "deadlock eligible", errors: []error{deadlock(), nil}, wantCalls: 2},
		{name: "foreign key violation ineligible", errors: []error{foreignKeyViolation()}, wantCalls: 1, wantState: "23503"},
		{name: "unique violation ineligible", errors: []error{uniqueViolation()}, wantCalls: 1, wantState: "23505"},
		{name: "arbitrary error ineligible", errors: []error{arbitraryError}, wantCalls: 1, wantErr: arbitraryError},
		{name: "eligible errors exhausted", errors: []error{deadlock(), serializationFailure(), deadlock()}, wantCalls: 3, wantState: "40P01"},
		{name: "successful after retry", errors: []error{serializationFailure(), deadlock(), nil}, wantCalls: 3},
		{name: "context cancelled during backoff", errors: []error{serializationFailure()}, cancelAfter: 1, wantCalls: 1, wantErr: context.Canceled},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			err := retryPostgresTransaction(ctx, 3, time.Millisecond, func() error {
				calls++
				if calls == test.cancelAfter {
					cancel()
				}
				if calls <= len(test.errors) {
					return test.errors[calls-1]
				}
				return nil
			})
			if calls != test.wantCalls {
				t.Fatalf("operation calls = %d, want %d", calls, test.wantCalls)
			}
			if test.wantErr != nil && !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
			if test.wantErr == nil && test.wantState == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if test.wantState != "" {
				var stateErr sqlStateError
				if !errors.As(err, &stateErr) || stateErr.SQLState() != test.wantState {
					t.Fatalf("error = %v, want SQLSTATE %s", err, test.wantState)
				}
			}
		})
	}
}

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
		if err != ErrTimeSeriesAggregationRunning {
			t.Fatalf("RunKey %q: error = %v, want unchanged ErrTimeSeriesAggregationRunning", runKey, err)
		}
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("losing worker read or updated its checkpoint, wrote failed state, or accessed aggregation data: %v", err)
	}
}

func TestRecordTimeSeriesAggregationFailureIgnoresContention(t *testing.T) {
	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close() //nolint:errcheck

	handler := Handler(&PostgresHandler{db: database, dbms: DBPostgresStr})
	runErr := fmt.Errorf("lease acquisition: %w", ErrTimeSeriesAggregationRunning)
	err = recordTimeSeriesAggregationFailure(
		&handler,
		DBPostgresStr,
		"losing-worker",
		TimeSeriesRange{Start: time.Now().Add(-time.Hour), End: time.Now()},
		time.Now().Add(-2*time.Hour),
		runErr,
	)
	if err != nil {
		t.Fatalf("record contention failure: %v", err)
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("contention wrote a failed run state or checkpoint: %v", err)
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

func TestTimeSeriesAggregationKeysetMatchesReference(t *testing.T) {
	bases := []cfg.TimeSeriesTimeBasis{
		cfg.TimeSeriesTimeObservedAt,
		cfg.TimeSeriesTimeEventAt,
		cfg.TimeSeriesTimeSourceTimestamp,
	}
	for _, basis := range bases {
		basis := basis
		for _, size := range []int{0, 1, 4, 7} { // empty, one page, exact boundary, and multiple pages
			t.Run(fmt.Sprintf("%s/rows_%d", basis, size), func(t *testing.T) {
				db, closeDB := openEntityTimeSeriesTestDB(t)
				defer closeDB()
				metric, err := UpsertTimeSeriesMetric(db, &TimeSeriesMetric{Key: fmt.Sprintf("keyset-%s-%d", basis, size), DisplayName: "keyset", SourceKind: cfg.TimeSeriesSourceCustom, ValueType: cfg.TimeSeriesValueDecimal, Aggregate: cfg.TimeSeriesAggregateAverage, Bucket: cfg.TimeSeriesBucketOneHour, TimeBasis: basis, DedupeScope: cfg.TimeSeriesDedupeGlobal, ObjectType: cfg.TimeSeriesObjectWebObject, FailurePolicy: cfg.TimeSeriesFailureLogSkip, Selector: []byte(`{}`), Enabled: true})
				if err != nil {
					t.Fatal(err)
				}
				start := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
				want := make([]uint64, 0, size)
				for i := 0; i < size; i++ {
					// Repeated timestamps exercise the observation_id tie-breaker;
					// observed_at is reversed to catch ordering by the wrong clock.
					active := start.Add(time.Duration(i/2+1) * time.Minute)
					observed := start.Add(time.Duration(size-i+20) * time.Minute)
					value := float64(i)
					o := TimeSeriesObservation{MetricID: metric.ID, ObservedAt: observed, CollectedAt: observed, BucketStart: start, BucketEnd: start.Add(time.Hour), Value: TimeSeriesValue{Numeric: &value}, ValueHash: fmt.Sprintf("v-%d", i), DedupeKey: fmt.Sprintf("keyset-%s-%d-%d", basis, size, i)}
					switch basis {
					case cfg.TimeSeriesTimeObservedAt:
						o.ObservedAt = active
					case cfg.TimeSeriesTimeEventAt:
						o.EffectiveAt = timePointer(active)
					case cfg.TimeSeriesTimeSourceTimestamp:
						o.SourceUpdatedAt = timePointer(active)
					}
					inserted, insertErr := InsertTimeSeriesObservation(db, &o)
					if insertErr != nil {
						t.Fatal(insertErr)
					}
					want = append(want, inserted.ObservationID)
				}

				// A soft-deleted row must not disturb either page boundaries or results.
				if size > 4 {
					if _, err = (*db).Exec(`UPDATE TimeSeriesObservations SET deleted_at = CURRENT_TIMESTAMP WHERE observation_id = ?`, want[2]); err != nil {
						t.Fatal(err)
					}
					want = append(want[:2], want[3:]...)
				}

				scanRange := TimeSeriesRange{Start: start, End: start.Add(time.Hour)}
				cursor := timeSeriesAggregationCursor{}
				got := make([]uint64, 0, len(want))
				for {
					page, pageErr := queryTimeSeriesAggregationPage(context.Background(), db, DBSQLiteStr, metric.ID, basis, scanRange, cursor, 2)
					if pageErr != nil {
						t.Fatal(pageErr)
					}
					for _, observation := range page.Observations {
						got = append(got, observation.ID)
					}
					if page.Count > 0 {
						last := page.Observations[page.Count-1]
						at, ok := timeSeriesObservationBasis(last, basis)
						if !ok {
							t.Fatal("page returned an observation without its active timestamp")
						}
						cursor = timeSeriesAggregationCursor{timestamp: at, observationID: last.ID, valid: true}
					}
					if !page.HasMore {
						break
					}
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("keyset IDs = %v, reference IDs = %v", got, want)
				}
			})
		}
	}
}

func TestTimeSeriesAggregationKeysetIncludesLateRowsAfterCursor(t *testing.T) {
	db, closeDB := openEntityTimeSeriesTestDB(t)
	defer closeDB()
	metric, err := UpsertTimeSeriesMetric(db, &TimeSeriesMetric{Key: "keyset-late", DisplayName: "keyset late", SourceKind: cfg.TimeSeriesSourceCustom, ValueType: cfg.TimeSeriesValueDecimal, Aggregate: cfg.TimeSeriesAggregateAverage, Bucket: cfg.TimeSeriesBucketOneHour, TimeBasis: cfg.TimeSeriesTimeObservedAt, DedupeScope: cfg.TimeSeriesDedupeGlobal, ObjectType: cfg.TimeSeriesObjectWebObject, FailurePolicy: cfg.TimeSeriesFailureLogSkip, Selector: []byte(`{}`), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	insert := func(minute int, key string) uint64 {
		value := float64(minute)
		o := TimeSeriesObservation{MetricID: metric.ID, ObservedAt: start.Add(time.Duration(minute) * time.Minute), CollectedAt: start, BucketStart: start, BucketEnd: start.Add(time.Hour), Value: TimeSeriesValue{Numeric: &value}, ValueHash: key, DedupeKey: key}
		result, insertErr := InsertTimeSeriesObservation(db, &o)
		if insertErr != nil {
			t.Fatal(insertErr)
		}
		return result.ObservationID
	}
	insert(1, "late-1")
	insert(2, "late-2")
	rng := TimeSeriesRange{Start: start, End: start.Add(time.Hour)}
	first, err := queryTimeSeriesAggregationPage(context.Background(), db, DBSQLiteStr, metric.ID, metric.TimeBasis, rng, timeSeriesAggregationCursor{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	last := first.Observations[0]
	lateID := insert(3, "late-3")
	second, err := queryTimeSeriesAggregationPage(context.Background(), db, DBSQLiteStr, metric.ID, metric.TimeBasis, rng, timeSeriesAggregationCursor{timestamp: last.ObservedAt, observationID: last.ID, valid: true}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Observations) != 2 || second.Observations[1].ID != lateID {
		t.Fatalf("continuation did not include eligible late row: %#v", second.Observations)
	}
}

func TestTimeSeriesAggregationKeysetHonorsContextCancellation(t *testing.T) {
	db, closeDB := openEntityTimeSeriesTestDB(t)
	defer closeDB()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := queryTimeSeriesAggregationPage(ctx, db, DBSQLiteStr, 1, cfg.TimeSeriesTimeObservedAt, TimeSeriesRange{Start: time.Now().Add(-time.Hour), End: time.Now()}, timeSeriesAggregationCursor{}, 2)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
}

func TestTimeSeriesAggregateBatchUpsertEquivalentToSingleRows(t *testing.T) {
	type fixture struct {
		db      *Handler
		closeDB func()
		metric  *TimeSeriesMetric
	}
	newFixture := func(key string) fixture {
		db, closeDB := openEntityTimeSeriesTestDB(t)
		metric, err := UpsertTimeSeriesMetric(db, &TimeSeriesMetric{Key: key, DisplayName: key, SourceKind: cfg.TimeSeriesSourceCustom, ValueType: cfg.TimeSeriesValueDecimal, Aggregate: cfg.TimeSeriesAggregateAverage, Bucket: cfg.TimeSeriesBucketOneHour, TimeBasis: cfg.TimeSeriesTimeObservedAt, DedupeScope: cfg.TimeSeriesDedupeObject, ObjectType: cfg.TimeSeriesObjectWebObject, FailurePolicy: cfg.TimeSeriesFailureLogSkip, Selector: []byte(`{}`), Enabled: true})
		if err != nil {
			closeDB()
			t.Fatal(err)
		}
		return fixture{db: db, closeDB: closeDB, metric: metric}
	}
	single := newFixture("single-row-semantics")
	defer single.closeDB()
	batched := newFixture("batch-semantics")
	defer batched.closeDB()

	build := func(metricID uint64, late bool) []TimeSeriesAggregate {
		start := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
		rows := make([]TimeSeriesAggregate, 0, 257)
		for i := 0; i < 257; i++ { // crosses the production chunk boundary
			first, last := float64(i)+0.25, float64(i)+9.75
			count := int64(3 + i%5)
			if late && i%17 == 0 {
				count++
				last += 100
			}
			objectID := uint64(i + 1)
			firstAt, lastAt := start.Add(time.Minute), start.Add(59*time.Minute)
			sum := first + last
			avg := sum / 2
			rows = append(rows, TimeSeriesAggregate{
				MetricID: metricID, BucketStart: start, BucketEnd: start.Add(time.Hour),
				Scope: TimeSeriesScope{ObjectType: "webobject", ObjectID: &objectID}, Dimensions: map[string]interface{}{"region": []interface{}{"us", i % 3}, "shard": i % 17},
				ValueCount: count, OccurrenceTotal: float64(count + 2), DistinctValueCount: count - 1, NumericCount: 2,
				NumericSum: &sum, NumericMin: &first, NumericMax: &last, NumericAverage: &avg,
				Percentile50: &avg, Percentile75: &last, Percentile90: &last, Percentile95: &last, Percentile99: &last,
				First:       TimeSeriesAggregateEdge{ObservedAt: &firstAt, ValueNumeric: &first, ValueHash: fmt.Sprintf("first-%d", i)},
				Last:        TimeSeriesAggregateEdge{ObservedAt: &lastAt, ValueNumeric: &last, ValueHash: fmt.Sprintf("last-%d", i)},
				FirstSeenAt: &firstAt, LastSeenAt: &lastAt, ChangeCount: int64(i % 4),
			})
		}
		return rows
	}
	write := func(db *Handler, rows []TimeSeriesAggregate, batchSize int) {
		t.Helper()
		tx, err := (*db).BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback() //nolint:errcheck
		for start := 0; start < len(rows); start += batchSize {
			end := min(start+batchSize, len(rows))
			query, args, buildErr := buildTimeSeriesAggregateBatchUpsert(DBSQLiteStr, rows[start:end])
			if buildErr != nil {
				t.Fatal(buildErr)
			}
			if _, err = tx.Exec(query, args...); err != nil {
				t.Fatal(err)
			}
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	// The first pass covers insert semantics; the second simulates late data and
	// covers aggregate_hash conflict updates without changing group identity.
	for _, late := range []bool{false, true} {
		write(single.db, build(single.metric.ID, late), 1)
		write(batched.db, build(batched.metric.ID, late), timeSeriesAggregateUpsertBatchSize(DBSQLiteStr))
	}
	read := func(f fixture) []TimeSeriesAggregate {
		result, err := QueryTimeSeriesAggregates(f.db, TimeSeriesQueryFilter{MetricID: &f.metric.ID, Pagination: TimeSeriesPagination{Limit: 1000}})
		if err != nil {
			t.Fatal(err)
		}
		for i := range result.Aggregates {
			a := &result.Aggregates[i]
			a.ID, a.MetricID = 0, 0
			a.CreatedAt, a.LastUpdatedAt = time.Time{}, time.Time{}
		}
		return result.Aggregates
	}
	if got, want := read(batched), read(single); !reflect.DeepEqual(got, want) {
		t.Fatalf("batched aggregates differ from single-row semantics\ngot:  %#v\nwant: %#v", got, want)
	}
}

func TestTimeSeriesAggregateBatchUpsertIsBoundedAndUsesPostgresPlaceholders(t *testing.T) {
	rows := make([]TimeSeriesAggregate, timeSeriesAggregateUpsertMaxBatchSize)
	for i := range rows {
		rows[i] = TimeSeriesAggregate{MetricID: 1, BucketStart: time.Unix(int64(i), 0), BucketEnd: time.Unix(int64(i+1), 0), AggregateHash: fmt.Sprintf("hash-%d", i)}
	}
	query, args, err := buildTimeSeriesAggregateBatchUpsert(DBPostgresStr, rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != timeSeriesAggregateUpsertMaxBatchSize*48 || !strings.Contains(query, "$12000") || strings.Contains(query, "?") {
		t.Fatalf("unexpected PostgreSQL batch: args=%d query suffix present=%t", len(args), strings.Contains(query, "$12000"))
	}
	if _, _, err = buildTimeSeriesAggregateBatchUpsert(DBPostgresStr, append(rows, TimeSeriesAggregate{})); err == nil {
		t.Fatal("oversized batch was accepted")
	}
}

func BenchmarkTimeSeriesAggregateUpsertRoundTrips(b *testing.B) {
	for _, batchSize := range []int{1, 10, 50, 100, timeSeriesAggregateUpsertMaxBatchSize} {
		b.Run(fmt.Sprintf("batch_%d", batchSize), func(b *testing.B) {
			db, closeDB := openEntityTimeSeriesTestDB(b)
			defer closeDB()
			metric, err := UpsertTimeSeriesMetric(db, &TimeSeriesMetric{Key: fmt.Sprintf("benchmark-%d", batchSize), DisplayName: "benchmark", SourceKind: cfg.TimeSeriesSourceCustom, ValueType: cfg.TimeSeriesValueDecimal, Aggregate: cfg.TimeSeriesAggregateAverage, Bucket: cfg.TimeSeriesBucketOneHour, TimeBasis: cfg.TimeSeriesTimeObservedAt, DedupeScope: cfg.TimeSeriesDedupeGlobal, ObjectType: cfg.TimeSeriesObjectWebObject, FailurePolicy: cfg.TimeSeriesFailureLogSkip, Selector: []byte(`{}`), Enabled: true})
			if err != nil {
				b.Fatal(err)
			}
			rows := make([]TimeSeriesAggregate, timeSeriesAggregateUpsertMaxBatchSize)
			for i := range rows {
				rows[i] = TimeSeriesAggregate{MetricID: metric.ID, BucketStart: time.Unix(int64(i), 0), BucketEnd: time.Unix(int64(i+1), 0), AggregateHash: fmt.Sprintf("benchmark-%d", i), Dimensions: map[string]interface{}{"cardinality": i}}
			}
			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				tx, beginErr := (*db).BeginTx(context.Background(), nil)
				if beginErr != nil {
					b.Fatal(beginErr)
				}
				for start := 0; start < len(rows); start += batchSize {
					end := min(start+batchSize, len(rows))
					query, args, buildErr := buildTimeSeriesAggregateBatchUpsert(DBSQLiteStr, rows[start:end])
					if buildErr != nil {
						b.Fatal(buildErr)
					}
					if _, execErr := tx.Exec(query, args...); execErr != nil {
						b.Fatal(execErr)
					}
				}
				if err = tx.Commit(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestTimeSeriesAggregationRepeatedRunKeepsCompletionTimestamp(t *testing.T) {
	db, closeDB := openEntityTimeSeriesTestDB(t)
	defer closeDB()

	metric, err := UpsertTimeSeriesMetric(db, &TimeSeriesMetric{
		Key:           "aggregation-repeated-completion",
		DisplayName:   "aggregation-repeated-completion",
		SourceKind:    cfg.TimeSeriesSourceCustom,
		ValueType:     cfg.TimeSeriesValueDecimal,
		Aggregate:     cfg.TimeSeriesAggregateAverage,
		Bucket:        cfg.TimeSeriesBucketOneHour,
		TimeBasis:     cfg.TimeSeriesTimeObservedAt,
		DedupeScope:   cfg.TimeSeriesDedupeGlobal,
		ObjectType:    cfg.TimeSeriesObjectWebObject,
		FailurePolicy: cfg.TimeSeriesFailureLogSkip,
		Selector:      []byte(`{}`),
		Enabled:       true,
	})
	if err != nil {
		t.Fatal(err)
	}

	start := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	value := 3.5
	if _, err = InsertTimeSeriesObservation(db, &TimeSeriesObservation{
		MetricID:    metric.ID,
		ObservedAt:  start.Add(time.Minute),
		CollectedAt: start.Add(time.Minute),
		BucketStart: start,
		BucketEnd:   start.Add(time.Hour),
		Value:       TimeSeriesValue{Numeric: &value},
		ValueHash:   "repeated-completion-value",
		DedupeKey:   "repeated-completion-observation",
	}); err != nil {
		t.Fatal(err)
	}

	const runKey = "test-repeated-completion"
	checkpoints := []time.Time{start.Add(30 * time.Minute), start.Add(45 * time.Minute)}
	for i, wantCheckpoint := range checkpoints {
		result, runErr := RunTimeSeriesAggregation(context.Background(), db, TimeSeriesAggregationOptions{
			BatchSize:  10,
			MaxBatches: 1,
			Now:        wantCheckpoint,
			RunKey:     runKey,
		})
		if runErr != nil {
			t.Fatalf("aggregation %d: %v", i+1, runErr)
		}
		if !result.Checkpoint.Equal(wantCheckpoint) {
			t.Fatalf("aggregation %d checkpoint = %s, want %s", i+1, result.Checkpoint, wantCheckpoint)
		}

		var status string
		var checkpoint time.Time
		var hasCompletedAt bool
		if err = (*db).QueryRow(`
			SELECT status, checkpoint_at, completed_at IS NOT NULL
			FROM TimeSeriesAggregationRuns
			WHERE run_key = ?`, runKey).Scan(&status, &checkpoint, &hasCompletedAt); err != nil {
			t.Fatalf("read aggregation %d run state: %v", i+1, err)
		}
		if status != "completed" {
			t.Fatalf("aggregation %d status = %q, want completed", i+1, status)
		}
		if !checkpoint.Equal(wantCheckpoint) {
			t.Fatalf("aggregation %d stored checkpoint = %s, want %s", i+1, checkpoint, wantCheckpoint)
		}
		if !hasCompletedAt {
			t.Fatalf("aggregation %d completed_at is NULL", i+1)
		}
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
