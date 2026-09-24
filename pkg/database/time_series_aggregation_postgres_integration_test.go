//go:build integration

package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"

	_ "github.com/lib/pq"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

const widgetHistoryDocument = `{"kind":"Widget","name":"backlog-widget","operational":{"count":7,"price":12.5},"region":"eu"}`
const widgetReplacementDocument = `{"kind":"Widget","name":"backlog-widget","operational":{"count":9,"price":15.75},"region":"eu"}`

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

// TestPostgresWebObjectHistorySurvivesReplacementAndFollowsSourceOwnership is
// deliberately end-to-end at the persistence boundary.  In particular, the
// WebObject row is replaced (rather than updated in place), just as the crawler
// refresh path does when refresh_content is enabled.
func TestPostgresWebObjectHistorySurvivesReplacementAndFollowsSourceOwnership(t *testing.T) {
	db, sqlDB := openPostgresIntegrationTestDB(t)
	suffix := fmt.Sprint(time.Now().UnixNano())
	createSource := func(name string) uint64 {
		t.Helper()
		var id uint64
		err := sqlDB.QueryRow(`INSERT INTO Sources (url, name, priority, category_id, usr_id, restricted, flags, config, disabled)
			VALUES ($1,$2,'normal',0,0,0,0,'{}'::jsonb,false) RETURNING source_id`, "https://"+name+".invalid/"+suffix, name).Scan(&id)
		if err != nil {
			t.Fatalf("create source %s: %v", name, err)
		}
		return id
	}
	primary, control := createSource("widget-primary"), createSource("widget-control")

	metric, err := UpsertTimeSeriesMetric(db, &TimeSeriesMetric{Key: "widget-history-" + suffix, DisplayName: "widget history", SourceKind: cfg.TimeSeriesSourceWebObject, ValueType: cfg.TimeSeriesValueJSON, Aggregate: cfg.TimeSeriesAggregateCount, Bucket: cfg.TimeSeriesBucketOneHour, TimeBasis: cfg.TimeSeriesTimeObservedAt, DedupeScope: cfg.TimeSeriesDedupeObject, ObjectType: cfg.TimeSeriesObjectWebObject, FailurePolicy: cfg.TimeSeriesFailureFailIndexing, Selector: json.RawMessage(`{"path":"operational"}`), Dimensions: json.RawMessage(`[{"key":"region","selector":{"path":"region"}}]`), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = sqlDB.Exec(`DELETE FROM ObjectAttributes WHERE context_ref=$1`, suffix)
		_, _ = sqlDB.Exec(`DELETE FROM Sources WHERE source_id IN ($1,$2)`, primary, control)
		_, _ = sqlDB.Exec(`DELETE FROM TimeSeriesMetrics WHERE metric_id=$1`, metric.ID)
	})

	base := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	insertObject := func(source uint64, document string, at time.Time) (uint64, uint64) {
		t.Helper()
		var indexID, objectID uint64
		url := fmt.Sprintf("https://widget.invalid/%d/%s", source, suffix)
		if err := sqlDB.QueryRow(`INSERT INTO SearchIndex(page_url,title,last_updated_at) VALUES($1,'Widget',$2)
			ON CONFLICT(page_url) DO UPDATE SET last_updated_at=EXCLUDED.last_updated_at RETURNING index_id`, url, at).Scan(&indexID); err != nil {
			t.Fatal(err)
		}
		if _, err := sqlDB.Exec(`INSERT INTO SourceSearchIndex(source_id,index_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, source, indexID); err != nil {
			t.Fatal(err)
		}
		// This is the crawler's refresh/replacement sequence: delete the linked
		// WebObject and then persist/link the newly hashed operational document.
		if _, err := sqlDB.Exec(`DELETE FROM WebObjects WHERE object_id IN (SELECT object_id FROM WebObjectsIndex WHERE index_id=$1)`, indexID); err != nil {
			t.Fatal(err)
		}
		objectHash := fmt.Sprintf("%x", sha256.Sum256([]byte(document)))
		if err := sqlDB.QueryRow(`INSERT INTO WebObjects(object_hash,object_content,details) VALUES($1,$2,$2::jsonb) RETURNING object_id`, objectHash, document).Scan(&objectID); err != nil {
			t.Fatal(err)
		}
		if _, err := sqlDB.Exec(`INSERT INTO WebObjectsIndex(index_id,object_id) VALUES($1,$2)`, indexID, objectID); err != nil {
			t.Fatal(err)
		}
		var decoded struct {
			Operational struct {
				Count int `json:"count"`
			} `json:"operational"`
		}
		if err := json.Unmarshal([]byte(document), &decoded); err != nil {
			t.Fatal(err)
		}
		attributeValue := strconv.Itoa(decoded.Operational.Count)
		attributeHash := fmt.Sprintf("%x", sha256.Sum256([]byte(attributeValue)))
		if _, err := sqlDB.Exec(`INSERT INTO ObjectAttributes(object_id,object_type,attribute_key,attribute_value,normalized_value,value_hash,attribute_type,source_path,context_ref)
			VALUES($1,'webobject','operational_count',$2,$2,$3,'integer','operational.count',$4)`, objectID, attributeValue, attributeHash, suffix); err != nil {
			t.Fatal(err)
		}
		return indexID, objectID
	}
	insertObservation := func(source, indexID, objectID uint64, document string, at time.Time, previous *TimeSeriesObservation) TimeSeriesObservation {
		t.Helper()
		var state map[string]interface{}
		if err := json.Unmarshal([]byte(document), &state); err != nil {
			t.Fatal(err)
		}
		value, _ := json.Marshal(state["operational"])
		o := TimeSeriesObservation{MetricID: metric.ID, ObservedAt: at, EffectiveAt: integrationTimePointer(at.Add(-time.Minute)), CollectedAt: at.Add(time.Second), SourceUpdatedAt: integrationTimePointer(at.Add(-time.Second)), BucketStart: at.Truncate(time.Hour), BucketEnd: at.Truncate(time.Hour).Add(time.Hour), Scope: TimeSeriesScope{SourceID: &source, IndexID: &indexID, SubjectType: "widget", SubjectID: &objectID, ObjectType: "webobject", ObjectID: &objectID}, Value: TimeSeriesValue{JSON: value}, Dimensions: map[string]interface{}{"region": state["region"], "kind": state["kind"]}, Provenance: json.RawMessage(fmt.Sprintf(`{"source_id":%d,"selector":"operational","document":%s}`, source, document))}
		prepared, prepErr := PrepareTimeSeriesObservation(o, cfg.TimeSeriesValueJSON, TimeSeriesPreparationPolicy{})
		if prepErr != nil {
			t.Fatal(prepErr)
		}
		o = prepared.Observation
		o.ProvenanceHash, err = TimeSeriesProvenanceHash(o.Provenance)
		if err != nil {
			t.Fatal(err)
		}
		if previous != nil {
			o.PreviousObservationID = &previous.ID
			o.PreviousValueHash = previous.ValueHash
			o.IsChanged = previous.ValueHash != o.ValueHash
			o.ChangeType = "updated"
			o.ChangeDetectedAt = integrationTimePointer(at)
		}
		o.DedupeKey, err = TimeSeriesDedupeKey(metric.DedupeScope, metric.ID, o, "")
		if err != nil {
			t.Fatal(err)
		}
		result, insertErr := InsertTimeSeriesObservation(db, &o)
		if insertErr != nil {
			t.Fatal(insertErr)
		}
		o.ID = result.ObservationID
		return o
	}

	indexID, oldObjectID := insertObject(primary, widgetHistoryDocument, base)
	old := insertObservation(primary, indexID, oldObjectID, widgetHistoryDocument, base, nil)
	controlIndex, controlObject := insertObject(control, widgetHistoryDocument, base)
	_ = insertObservation(control, controlIndex, controlObject, widgetHistoryDocument, base, nil)
	before, err := QueryTimeSeriesObservations(db, TimeSeriesQueryFilter{SourceID: &primary, Pagination: TimeSeriesPagination{Limit: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if before.Count != 1 {
		t.Fatalf("old observations=%d, want 1", before.Count)
	}
	snapshot := before.Observations[0]

	_, newObjectID := insertObject(primary, widgetReplacementDocument, base.Add(10*time.Minute))
	newObservation := insertObservation(primary, indexID, newObjectID, widgetReplacementDocument, base.Add(10*time.Minute), &old)
	after, err := QueryTimeSeriesObservations(db, TimeSeriesQueryFilter{SourceID: &primary, Pagination: TimeSeriesPagination{Limit: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if after.Count != 2 {
		t.Fatalf("refreshed observations=%d, want 2", after.Count)
	}
	if !reflect.DeepEqual(snapshot, after.Observations[0]) {
		t.Fatalf("historical observation mutated\nbefore: %#v\nafter:  %#v", snapshot, after.Observations[0])
	}
	for i, want := range []string{widgetHistoryDocument, widgetReplacementDocument} {
		var provenance struct {
			Document json.RawMessage `json:"document"`
		}
		if err := json.Unmarshal(after.Observations[i].Provenance, &provenance); err != nil {
			t.Fatal(err)
		}
		var got, expected interface{}
		_ = json.Unmarshal(provenance.Document, &got)
		_ = json.Unmarshal([]byte(want), &expected)
		if !reflect.DeepEqual(got, expected) {
			t.Fatalf("state %d was not reconstructed from history: %#v", i, got)
		}
	}
	if newObservation.PreviousObservationID == nil || *newObservation.PreviousObservationID != old.ID || !newObservation.IsChanged {
		t.Fatalf("change chain not appended: %#v", newObservation)
	}

	if _, err = AggregateTimeSeriesRange(context.Background(), db, TimeSeriesRange{Start: base.Truncate(time.Hour), End: base.Truncate(time.Hour).Add(time.Hour)}, TimeSeriesAggregationOptions{RunKey: "widget-lifecycle-" + suffix, BatchSize: 100, MaxBatches: 2}); err != nil {
		t.Fatal(err)
	}
	if err = DeleteSource(db, primary); err != nil {
		t.Fatal(err)
	}
	primaryObservations, _ := QueryTimeSeriesObservations(db, TimeSeriesQueryFilter{SourceID: &primary})
	primaryAggregates, _ := QueryTimeSeriesAggregates(db, TimeSeriesQueryFilter{SourceID: &primary})
	controlObservations, _ := QueryTimeSeriesObservations(db, TimeSeriesQueryFilter{SourceID: &control})
	if primaryObservations.Count != 0 || primaryAggregates.Count != 0 || controlObservations.Count != 1 {
		t.Fatalf("source cascade/control isolation failed: primary obs=%d agg=%d control=%d", primaryObservations.Count, primaryAggregates.Count, controlObservations.Count)
	}
}

func integrationTimePointer(value time.Time) *time.Time { return &value }

func TestPostgresDeterministicAggregateEquivalenceFixture(t *testing.T) {
	db, sqlDB := openPostgresIntegrationTestDB(t)
	suffix := fmt.Sprint(time.Now().UnixNano())
	decimal, err := UpsertTimeSeriesMetric(db, &TimeSeriesMetric{Key: "aggregate-decimal-" + suffix, DisplayName: "aggregate decimal", SourceKind: cfg.TimeSeriesSourceCustom, ValueType: cfg.TimeSeriesValueDecimal, Aggregate: cfg.TimeSeriesAggregateAverage, Bucket: cfg.TimeSeriesBucketOneHour, TimeBasis: cfg.TimeSeriesTimeObservedAt, DedupeScope: cfg.TimeSeriesDedupeObject, ObjectType: cfg.TimeSeriesObjectWebObject, FailurePolicy: cfg.TimeSeriesFailureLogSkip, Selector: json.RawMessage(`{}`), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	count, err := UpsertTimeSeriesMetric(db, &TimeSeriesMetric{Key: "aggregate-count-" + suffix, DisplayName: "aggregate count", SourceKind: cfg.TimeSeriesSourceCustom, ValueType: cfg.TimeSeriesValueCount, Aggregate: cfg.TimeSeriesAggregateCount, Bucket: cfg.TimeSeriesBucketOneHour, TimeBasis: cfg.TimeSeriesTimeObservedAt, DedupeScope: cfg.TimeSeriesDedupeObject, ObjectType: cfg.TimeSeriesObjectWebObject, FailurePolicy: cfg.TimeSeriesFailureLogSkip, Selector: json.RawMessage(`{}`), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	runKey := "aggregate-equivalence-" + suffix
	t.Cleanup(func() {
		_, _ = sqlDB.Exec(`DELETE FROM TimeSeriesAggregationRuns WHERE run_key=$1`, runKey)
		_, _ = sqlDB.Exec(`DELETE FROM TimeSeriesAggregates WHERE metric_id IN ($1,$2)`, decimal.ID, count.ID)
		_, _ = sqlDB.Exec(`DELETE FROM TimeSeriesObservations WHERE metric_id IN ($1,$2)`, decimal.ID, count.ID)
		_, _ = sqlDB.Exec(`DELETE FROM TimeSeriesMetrics WHERE metric_id IN ($1,$2)`, decimal.ID, count.ID)
	})
	start := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	objectA, objectB := uint64(101), uint64(202)
	insert := func(metric TimeSeriesMetric, objectID uint64, dims map[string]interface{}, minute int, numeric *float64, integer *int64, hash string, changed bool) {
		t.Helper()
		at := start.Add(time.Duration(minute) * time.Minute)
		o := TimeSeriesObservation{MetricID: metric.ID, ObservedAt: at, CollectedAt: at.Add(time.Second), BucketStart: at.Truncate(time.Hour), BucketEnd: at.Truncate(time.Hour).Add(time.Hour), Scope: TimeSeriesScope{SubjectType: "widget", SubjectID: &objectID, ObjectType: "webobject", ObjectID: &objectID}, Value: TimeSeriesValue{Numeric: numeric, Integer: integer}, ValueHash: hash, DedupeKey: fmt.Sprintf("%s-%d-%d-%s", suffix, metric.ID, minute, hash), Dimensions: dims, IsChanged: changed}
		if _, insertErr := InsertTimeSeriesObservation(db, &o); insertErr != nil {
			t.Fatal(insertErr)
		}
	}
	regionEU := map[string]interface{}{"region": "eu", "tier": "gold"}
	for i, value := range []float64{1.25, 2.50, 2.50, 4.75, 9.00} {
		v := value
		// minute 5 is inserted after the first aggregate below, exercising late data.
		if i == 4 {
			continue
		}
		insert(*decimal, objectA, regionEU, 10+i*10, &v, nil, []string{"a", "b", "b", "c"}[i], i == 1 || i == 3)
	}
	one, three := int64(1), int64(3)
	insert(*count, objectB, map[string]interface{}{"region": "us", "tier": "silver"}, 5, nil, &one, "one", false)
	insert(*count, objectB, map[string]interface{}{"region": "us", "tier": "silver"}, 35, nil, &three, "three", true)
	insert(*count, objectB, map[string]interface{}{"region": "us", "tier": "silver"}, 65, nil, &one, "next-bucket", false)
	rng := TimeSeriesRange{Start: start, End: start.Add(2 * time.Hour)}
	if _, err = AggregateTimeSeriesRange(context.Background(), db, rng, TimeSeriesAggregationOptions{RunKey: runKey, BatchSize: 2, MaxBatches: 20}); err != nil {
		t.Fatal(err)
	}
	late := 9.0
	insert(*decimal, objectA, regionEU, 15, &late, nil, "late", true)
	if _, err = AggregateTimeSeriesRange(context.Background(), db, rng, TimeSeriesAggregationOptions{RunKey: runKey, BatchSize: 3, MaxBatches: 20}); err != nil {
		t.Fatal(err)
	}

	result, err := QueryTimeSeriesAggregates(db, TimeSeriesQueryFilter{Start: &start, End: integrationTimePointer(start.Add(2 * time.Hour)), Pagination: TimeSeriesPagination{Limit: 20}})
	if err != nil {
		t.Fatal(err)
	}
	byMetric := map[uint64]TimeSeriesAggregate{}
	var nextBucket TimeSeriesAggregate
	for _, aggregate := range result.Aggregates {
		if aggregate.MetricID == count.ID && aggregate.BucketStart.Equal(start.Add(time.Hour)) {
			nextBucket = aggregate
		} else if aggregate.MetricID == decimal.ID || aggregate.MetricID == count.ID {
			byMetric[aggregate.MetricID] = aggregate
		}
	}
	d := byMetric[decimal.ID]
	assertFloat := func(name string, got *float64, want float64) {
		t.Helper()
		if got == nil || *got != want {
			t.Fatalf("%s=%v want %v", name, got, want)
		}
	}
	if d.ValueCount != 5 || d.OccurrenceTotal != 5 || d.DistinctValueCount != 4 || d.NumericCount != 5 || d.ChangeCount != 3 || d.Scope.ObjectID == nil || *d.Scope.ObjectID != objectA || !reflect.DeepEqual(d.Dimensions, regionEU) {
		t.Fatalf("decimal aggregate metadata/scope = %#v", d)
	}
	assertFloat("decimal sum", d.NumericSum, 20)
	assertFloat("decimal min", d.NumericMin, 1.25)
	assertFloat("decimal max", d.NumericMax, 9)
	assertFloat("decimal average", d.NumericAverage, 4)
	assertFloat("decimal p50", d.Percentile50, 2.5)
	assertFloat("decimal p75", d.Percentile75, 4.75)
	assertFloat("decimal p90", d.Percentile90, 7.3)
	assertFloat("decimal p95", d.Percentile95, 8.15)
	assertFloat("decimal p99", d.Percentile99, 8.83)
	if d.First.ObservedAt == nil || !d.First.ObservedAt.Equal(start.Add(10*time.Minute)) || d.Last.ObservedAt == nil || !d.Last.ObservedAt.Equal(start.Add(40*time.Minute)) || d.First.ValueHash != "a" || d.Last.ValueHash != "c" {
		t.Fatalf("decimal first/last = %#v / %#v", d.First, d.Last)
	}
	c := byMetric[count.ID]
	if c.ValueCount != 2 || c.OccurrenceTotal != 4 || c.DistinctValueCount != 2 || c.NumericCount != 2 || c.ChangeCount != 1 || c.Scope.ObjectID == nil || *c.Scope.ObjectID != objectB {
		t.Fatalf("count aggregate = %#v", c)
	}
	assertFloat("count sum", c.NumericSum, 4)
	assertFloat("count average", c.NumericAverage, 2)
	assertFloat("count p50", c.Percentile50, 2)
	assertFloat("count p75", c.Percentile75, 2.5)
	assertFloat("count p90", c.Percentile90, 2.8)
	assertFloat("count p95", c.Percentile95, 2.9)
	assertFloat("count p99", c.Percentile99, 2.98)
	if nextBucket.ValueCount != 1 || nextBucket.OccurrenceTotal != 1 || nextBucket.First.ValueHash != "next-bucket" || !nextBucket.BucketEnd.Equal(start.Add(2*time.Hour)) {
		t.Fatalf("second bucket aggregate = %#v", nextBucket)
	}
	for _, aggregate := range []TimeSeriesAggregate{d, c, nextBucket} {
		expected, hashErr := TimeSeriesAggregateHash(aggregate)
		if hashErr != nil || aggregate.AggregateHash != expected {
			t.Fatalf("aggregate hash=%q expected=%q err=%v scope=%#v", aggregate.AggregateHash, expected, hashErr, aggregate.Scope)
		}
	}
}
