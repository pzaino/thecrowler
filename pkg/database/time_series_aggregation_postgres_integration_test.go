//go:build integration

package database

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	_ "github.com/lib/pq"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func openPostgresIntegrationTestDB(t *testing.T) (*Handler, *sql.DB) {
	t.Helper()
	if os.Getenv("THECROWLER_POSTGRES_INTEGRATION") != "1" {
		t.Skip("set THECROWLER_POSTGRES_INTEGRATION=1 to run PostgreSQL integration tests")
	}
	valueOrDefault := func(name, fallback string) string {
		if value := os.Getenv(name); value != "" {
			return value
		}
		return fallback
	}
	port, err := strconv.Atoi(valueOrDefault("DOCKER_POSTGRES_DB_PORT", "5432"))
	if err != nil {
		t.Fatalf("invalid DOCKER_POSTGRES_DB_PORT: %v", err)
	}
	user := valueOrDefault("DOCKER_POSTGRES_DB_USER", valueOrDefault("DOCKER_POSTGRES_USER", "postgres"))
	dsn := &url.URL{Scheme: "postgres", Host: fmt.Sprintf("%s:%d", valueOrDefault("DOCKER_POSTGRES_DB_HOST", "127.0.0.1"), port), Path: valueOrDefault("DOCKER_POSTGRES_DB_NAME", "SitesIndex"), User: url.UserPassword(user, valueOrDefault("DOCKER_POSTGRES_PASSWORD", "postgres"))}
	query := dsn.Query()
	query.Set("sslmode", valueOrDefault("DOCKER_POSTGRES_SSL_MODE", "disable"))
	dsn.RawQuery = query.Encode()

	database, err := sql.Open(DBPostgresStr, dsn.String())
	if err != nil {
		t.Fatalf("open PostgreSQL integration database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	database.SetMaxOpenConns(10)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = database.PingContext(ctx); err != nil {
		t.Fatalf("ping PostgreSQL integration database: %v", err)
	}
	var schemaReady bool
	if err = database.QueryRowContext(ctx, `SELECT to_regclass('timeseriesmetrics') IS NOT NULL`).Scan(&schemaReady); err != nil || !schemaReady {
		t.Fatalf("PostgreSQL integration database does not have the repository schema (err=%v)", err)
	}
	var handler Handler = &PostgresHandler{db: database, dbms: DBPostgresStr, connStr: dsn.String()}
	return &handler, database
}

func createPostgresAggregationFixture(t *testing.T, db *Handler, sqlDB *sql.DB, runKey string) (uint64, TimeSeriesRange, time.Time) {
	t.Helper()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	metric, err := UpsertTimeSeriesMetric(db, &TimeSeriesMetric{Key: "lease-integration-" + suffix, DisplayName: "lease integration " + suffix, SourceKind: cfg.TimeSeriesSourceCustom, ValueType: cfg.TimeSeriesValueDecimal, Aggregate: cfg.TimeSeriesAggregateAverage, Bucket: cfg.TimeSeriesBucketOneHour, TimeBasis: cfg.TimeSeriesTimeObservedAt, DedupeScope: cfg.TimeSeriesDedupeGlobal, ObjectType: cfg.TimeSeriesObjectWebObject, FailurePolicy: cfg.TimeSeriesFailureLogSkip, Selector: []byte(`{}`), Enabled: true})
	if err != nil {
		t.Fatalf("create integration metric: %v", err)
	}
	t.Cleanup(func() {
		_, _ = sqlDB.Exec(`DELETE FROM TimeSeriesAggregationRuns WHERE run_key = $1`, runKey)
		_, _ = sqlDB.Exec(`DELETE FROM TimeSeriesAggregates WHERE metric_id = $1`, metric.ID)
		_, _ = sqlDB.Exec(`DELETE FROM TimeSeriesObservations WHERE metric_id = $1`, metric.ID)
		_, _ = sqlDB.Exec(`DELETE FROM TimeSeriesMetrics WHERE metric_id = $1`, metric.ID)
	})

	start := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Hour)
	rangeToRun := TimeSeriesRange{Start: start, End: start.Add(time.Hour)}
	value := 42.5
	_, err = InsertTimeSeriesObservation(db, &TimeSeriesObservation{MetricID: metric.ID, ObservedAt: start.Add(time.Minute), CollectedAt: start.Add(time.Minute), BucketStart: start, BucketEnd: start.Add(time.Hour), Value: TimeSeriesValue{Numeric: &value}, ValueHash: "lease-value-" + suffix, DedupeKey: "lease-observation-" + suffix})
	if err != nil {
		t.Fatalf("insert integration observation: %v", err)
	}
	checkpointBefore := start.Add(-time.Hour)
	if _, err = sqlDB.Exec(`INSERT INTO TimeSeriesAggregationRuns (run_key, status, checkpoint_at) VALUES ($1, 'complete', $2)`, runKey, checkpointBefore); err != nil {
		t.Fatalf("seed aggregation checkpoint: %v", err)
	}
	return metric.ID, rangeToRun, checkpointBefore
}

func assertPostgresAggregationUnchanged(t *testing.T, db *Handler, metricID uint64, runKey string, checkpointBefore time.Time) {
	t.Helper()
	checkpointAfter, err := timeSeriesAggregationCheckpoint(db, runKey)
	if err != nil || !checkpointAfter.Equal(checkpointBefore) {
		t.Fatalf("checkpoint after rejected worker = %s, %v; want %s", checkpointAfter, err, checkpointBefore)
	}
	var status string
	var lastError sql.NullString
	if err = (*db).QueryRow(`SELECT status, last_error FROM TimeSeriesAggregationRuns WHERE run_key = $1`, runKey).Scan(&status, &lastError); err != nil {
		t.Fatalf("read aggregation run after rejected worker: %v", err)
	}
	if status != "complete" || lastError.Valid {
		t.Fatalf("aggregation run after rejected worker = status %q, last_error %#v; want unchanged complete state", status, lastError)
	}
	aggregates, err := QueryTimeSeriesAggregates(db, TimeSeriesQueryFilter{MetricID: &metricID})
	if err != nil || aggregates.Count != 0 {
		t.Fatalf("aggregates after rejected worker = %d, %v; want no replacement", aggregates.Count, err)
	}
}

func TestPostgresTimeSeriesAggregationSameRunKeyContention(t *testing.T) {
	db, sqlDB := openPostgresIntegrationTestDB(t)
	runKey := "lease-same-run-" + fmt.Sprint(time.Now().UnixNano())
	metricID, rangeToRun, checkpointBefore := createPostgresAggregationFixture(t, db, sqlDB, runKey)
	// Direct acquisition makes worker A's ownership deterministic while leaving
	// the process mutex free for the separately simulated worker B.
	workerA, err := acquireTimeSeriesAggregationLease(context.Background(), db, DBPostgresStr)
	if err != nil {
		t.Fatalf("worker A acquire lease: %v", err)
	}
	resultB, err := RunTimeSeriesAggregation(context.Background(), db, TimeSeriesAggregationOptions{Range: &rangeToRun, RunKey: runKey})
	if err != ErrTimeSeriesAggregationRunning {
		t.Fatalf("worker B error = %v, want unchanged ErrTimeSeriesAggregationRunning", err)
	}
	if resultB.AggregatesReplaced != 0 {
		t.Fatalf("worker B replaced %d aggregates, want 0", resultB.AggregatesReplaced)
	}
	assertPostgresAggregationUnchanged(t, db, metricID, runKey, checkpointBefore)
	if err = workerA.release(); err != nil {
		t.Fatalf("worker A release lease: %v", err)
	}
	resultA, err := RunTimeSeriesAggregation(context.Background(), db, TimeSeriesAggregationOptions{Range: &rangeToRun, RunKey: runKey})
	if err != nil {
		t.Fatalf("complete worker A aggregation: %v", err)
	}
	if resultA.AggregatesReplaced != 1 || !resultA.Checkpoint.Equal(rangeToRun.End) {
		t.Fatalf("worker A result = %#v, want one replacement and checkpoint %s", resultA, rangeToRun.End)
	}
	aggregates, err := QueryTimeSeriesAggregates(db, TimeSeriesQueryFilter{MetricID: &metricID})
	if err != nil || aggregates.Count != 1 || aggregates.Aggregates[0].NumericAverage == nil || *aggregates.Aggregates[0].NumericAverage != 42.5 {
		t.Fatalf("worker A aggregates = %#v, %v", aggregates, err)
	}
	checkpointAfter, err := timeSeriesAggregationCheckpoint(db, runKey)
	if err != nil || !checkpointAfter.Equal(rangeToRun.End) {
		t.Fatalf("worker A checkpoint = %s, %v; want %s", checkpointAfter, err, rangeToRun.End)
	}
}

func TestPostgresTimeSeriesAggregationLeaseIsGlobalAcrossRunKeys(t *testing.T) {
	db, sqlDB := openPostgresIntegrationTestDB(t)
	workerARunKey := "timeseries-aggregation"
	otherRunKey := "lease-other-run-" + fmt.Sprint(time.Now().UnixNano())
	metricID, rangeToRun, checkpointBefore := createPostgresAggregationFixture(t, db, sqlDB, otherRunKey)
	// Acquiring the production fixed lease directly represents worker A after
	// its RunKey has been normalized to the default above. Advisory-lock
	// ownership intentionally contains no checkpoint/run-key component.
	workerA, err := acquireTimeSeriesAggregationLease(context.Background(), db, DBPostgresStr)
	if err != nil {
		t.Fatalf("%s worker acquire lease: %v", workerARunKey, err)
	}
	defer func() { _ = workerA.release() }()
	result, err := RunTimeSeriesAggregation(context.Background(), db, TimeSeriesAggregationOptions{Range: &rangeToRun, RunKey: otherRunKey})
	if err != ErrTimeSeriesAggregationRunning {
		t.Fatalf("worker with RunKey %q error = %v, want unchanged ErrTimeSeriesAggregationRunning while %q owns lease", otherRunKey, err, workerARunKey)
	}
	if result.AggregatesReplaced != 0 {
		t.Fatalf("second worker replaced %d aggregates, want 0", result.AggregatesReplaced)
	}
	assertPostgresAggregationUnchanged(t, db, metricID, otherRunKey, checkpointBefore)
}

func TestPostgresTimeSeriesAggregationLeaseRecoversWhenOwningSessionCloses(t *testing.T) {
	db, _ := openPostgresIntegrationTestDB(t)
	owner, err := acquireTimeSeriesAggregationLease(context.Background(), db, DBPostgresStr)
	if err != nil {
		t.Fatalf("acquire lease on owning session: %v", err)
	}
	if err = owner.close(); err != nil {
		t.Fatalf("close owning session without unlock: %v", err)
	}
	recovered, err := acquireTimeSeriesAggregationLease(context.Background(), db, DBPostgresStr)
	if err != nil {
		t.Fatalf("new PostgreSQL session did not recover fixed lease: %v", err)
	}
	if err = recovered.release(); err != nil {
		t.Fatalf("release recovered lease: %v", err)
	}
}
