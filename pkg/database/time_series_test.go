package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func TestPostgresCardinalityUsesAdvisoryLockAndRechecks(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectBegin()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	// Both the optimistic probe and the authoritative post-lock probe see a
	// missing identity and an available slot.
	for pass := 0; pass < 2; pass++ {
		mock.ExpectQuery(`SELECT series_identity FROM TimeSeriesActiveSeries`).WillReturnError(sql.ErrNoRows)
		mock.ExpectQuery(`SELECT COUNT\(\*\) FROM TimeSeriesActiveSeries`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
		if pass == 0 {
			mock.ExpectExec(`SELECT pg_advisory_xact_lock\(\$1\)`).WithArgs(timeSeriesCardinalityLockKey(17)).WillReturnResult(sqlmock.NewResult(0, 1))
		}
	}
	exceeded, err := TimeSeriesCardinalityExceededTx(context.Background(), tx, DBPostgresStr, 17, TimeSeriesScope{}, nil, cfg.TimeSeriesCardinalityConfig{MaxSeriesPerMetric: 1})
	if err != nil || exceeded {
		t.Fatalf("decision = exceeded %t, err %v", exceeded, err)
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestTransactionRepositoryClassifiesPostgresTransactionErrors(t *testing.T) {
	repo := TransactionTimeSeriesRepository{DBMS: DBPostgresStr}
	for _, code := range []pq.ErrorCode{"40P01", "40001", "23505", "25P02"} {
		if !repo.IsTransactionFatalError(fmt.Errorf("write observation: %w", &pq.Error{Code: code, Severity: "ERROR"})) {
			t.Errorf("SQLSTATE %s was not classified as transaction-fatal", code)
		}
	}
	if repo.IsTransactionFatalError(errors.New("invalid metric value")) {
		t.Fatal("ordinary metric errors must remain subject to failure_policy")
	}
}

type timeSeriesArgumentMatcher func(driver.Value) bool

func (m timeSeriesArgumentMatcher) Match(value driver.Value) bool { return m(value) }

func expectTimeSeriesAccounting(mock sqlmock.Sqlmock, dimensions int) {
	mock.ExpectQuery(`SELECT series_identity FROM TimeSeriesActiveSeries`).WillReturnRows(sqlmock.NewRows([]string{"series_identity"}))
	mock.ExpectExec(`INSERT INTO TimeSeriesActiveSeries`).WillReturnResult(sqlmock.NewResult(0, 1))
	for i := 0; i < dimensions; i++ {
		mock.ExpectQuery(`SELECT canonical_value FROM TimeSeriesActiveDimensionValues`).WillReturnRows(sqlmock.NewRows([]string{"canonical_value"}))
		mock.ExpectExec(`INSERT INTO TimeSeriesActiveDimensionValues`).WillReturnResult(sqlmock.NewResult(0, 1))
	}
}

func TestTimeSeriesCanonicalHashes(t *testing.T) {
	left := map[string]interface{}{"z": 1, "nested": map[string]interface{}{"b": true, "a": "x"}}
	right := map[string]interface{}{"nested": map[string]interface{}{"a": "x", "b": true}, "z": 1}
	leftHash, err := TimeSeriesDimensionHash(left)
	if err != nil {
		t.Fatal(err)
	}
	rightHash, err := TimeSeriesDimensionHash(right)
	if err != nil {
		t.Fatal(err)
	}
	if leftHash != rightHash {
		t.Fatalf("dimension hashes differ: %s != %s", leftHash, rightHash)
	}
	if TimeSeriesSubjectHash("  Example\tSUBJECT\n") != TimeSeriesSubjectHash("example subject") {
		t.Fatal("normalized subjects must hash identically")
	}
	if absent, _ := TimeSeriesDimensionHash(nil); absent == leftHash {
		t.Fatal("absent dimensions must have an explicit distinct hash")
	}
}

func TestTimeSeriesSeriesHashIsCanonicalAndIncludesCompleteGrouping(t *testing.T) {
	ids := []uint64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	scope := TimeSeriesScope{
		InformationSeedID: &ids[0], InformationSeedCandidateID: &ids[1], SourceID: &ids[2],
		SourceInformationSeedID: &ids[3], IndexID: &ids[4], EntityID: &ids[5],
		SubjectType: "subject", SubjectID: &ids[6], ObjectType: "object", ObjectID: &ids[7],
		CorrelationRuleID: &ids[8], CorrelationObjectType1: "left", CorrelationObjectID1: &ids[8],
		CorrelationObjectType2: "right", CorrelationObjectID2: &ids[9],
	}
	left := map[string]interface{}{"number": json.Number("1"), "nested": map[string]interface{}{"b": true, "a": "x"}}
	right := map[string]interface{}{"nested": map[string]interface{}{"a": "x", "b": true}, "number": 1}
	want, err := TimeSeriesSeriesHash(42, scope, left)
	if err != nil {
		t.Fatal(err)
	}
	equivalent, err := TimeSeriesSeriesHash(42, scope, right)
	if err != nil || want != equivalent {
		t.Fatalf("equivalent logical groups differ: %q != %q (%v)", want, equivalent, err)
	}

	assertDifferent := func(name string, metricID uint64, changed TimeSeriesScope, dimensions map[string]interface{}) {
		t.Helper()
		got, hashErr := TimeSeriesSeriesHash(metricID, changed, dimensions)
		if hashErr != nil {
			t.Fatalf("%s: %v", name, hashErr)
		}
		if got == want {
			t.Errorf("%s difference was omitted from series identity", name)
		}
	}
	assertDifferent("metric", 43, scope, left)
	assertDifferent("dimensions", 42, scope, map[string]interface{}{"number": 2, "nested": map[string]interface{}{"a": "x", "b": true}})
	mutations := []struct {
		name string
		edit func(*TimeSeriesScope)
	}{
		{"information seed", func(s *TimeSeriesScope) { s.InformationSeedID = nil }},
		{"candidate", func(s *TimeSeriesScope) { s.InformationSeedCandidateID = nil }},
		{"source", func(s *TimeSeriesScope) { s.SourceID = nil }},
		{"source seed", func(s *TimeSeriesScope) { s.SourceInformationSeedID = nil }},
		{"index", func(s *TimeSeriesScope) { s.IndexID = nil }},
		{"entity", func(s *TimeSeriesScope) { s.EntityID = nil }},
		{"subject type", func(s *TimeSeriesScope) { s.SubjectType = "other" }},
		{"subject id", func(s *TimeSeriesScope) { s.SubjectID = nil }},
		{"object type", func(s *TimeSeriesScope) { s.ObjectType = "other" }},
		{"object id", func(s *TimeSeriesScope) { s.ObjectID = nil }},
		{"rule", func(s *TimeSeriesScope) { s.CorrelationRuleID = nil }},
		{"correlation type 1", func(s *TimeSeriesScope) { s.CorrelationObjectType1 = "other" }},
		{"correlation id 1", func(s *TimeSeriesScope) { s.CorrelationObjectID1 = nil }},
		{"correlation type 2", func(s *TimeSeriesScope) { s.CorrelationObjectType2 = "other" }},
		{"correlation id 2", func(s *TimeSeriesScope) { s.CorrelationObjectID2 = nil }},
	}
	for _, mutation := range mutations {
		changed := scope
		mutation.edit(&changed)
		assertDifferent(mutation.name, 42, changed, left)
	}
}

func TestTimeSeriesExtendedValueAndProvenanceHashes(t *testing.T) {
	count := int64(1)
	if hash, err := TimeSeriesValueHash(cfg.TimeSeriesValueCount, TimeSeriesValue{Integer: &count}); err != nil || hash == "" {
		t.Fatalf("count hash: %s, %v", hash, err)
	}
	timestamp := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	if hash, err := TimeSeriesValueHash(cfg.TimeSeriesValueTimestamp, TimeSeriesValue{Timestamp: &timestamp}); err != nil || hash == "" {
		t.Fatalf("timestamp hash: %s, %v", hash, err)
	}
	left, err := TimeSeriesProvenanceHash(json.RawMessage(`{"b":2,"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	right, err := TimeSeriesProvenanceHash(json.RawMessage(`{"a":1,"b":2}`))
	if err != nil || left != right {
		t.Fatalf("provenance hash is not canonical: %s %s %v", left, right, err)
	}
}

func TestTimeSeriesDedupeScopes(t *testing.T) {
	source1, source2, object := uint64(1), uint64(2), uint64(9)
	base := TimeSeriesObservation{ObservedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), ValueHash: "value", Dimensions: map[string]interface{}{"region": "eu"}, Scope: TimeSeriesScope{SourceID: &source1, ObjectID: &object, ObjectType: "page"}}
	for _, scope := range []cfg.TimeSeriesDedupeScope{cfg.TimeSeriesDedupeNone, cfg.TimeSeriesDedupeSource, cfg.TimeSeriesDedupeObject, cfg.TimeSeriesDedupeGlobal} {
		nonce := ""
		if scope == cfg.TimeSeriesDedupeNone {
			nonce = "event-1"
		}
		one, err := TimeSeriesDedupeKey(scope, 7, base, nonce)
		if err != nil {
			t.Fatalf("scope %s: %v", scope, err)
		}
		two, err := TimeSeriesDedupeKey(scope, 7, base, nonce)
		if err != nil || one != two {
			t.Fatalf("scope %s is not deterministic", scope)
		}
	}
	if _, err := TimeSeriesDedupeKey(cfg.TimeSeriesDedupeNone, 7, base, ""); err == nil {
		t.Fatal("none scope must require a nonce")
	}
	changed := base
	changed.Scope.SourceID = &source2
	sourceA, _ := TimeSeriesDedupeKey(cfg.TimeSeriesDedupeSource, 7, base, "")
	sourceB, _ := TimeSeriesDedupeKey(cfg.TimeSeriesDedupeSource, 7, changed, "")
	if sourceA == sourceB {
		t.Fatal("source scope must include source ownership")
	}
	globalA, _ := TimeSeriesDedupeKey(cfg.TimeSeriesDedupeGlobal, 7, base, "")
	globalB, _ := TimeSeriesDedupeKey(cfg.TimeSeriesDedupeGlobal, 7, changed, "")
	if globalA != globalB {
		t.Fatal("global scope must ignore source ownership")
	}
}

func TestTimeSeriesAggregateHashIncludesGroupingFields(t *testing.T) {
	source := uint64(2)
	base := TimeSeriesAggregate{MetricID: 1, BucketStart: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), BucketEnd: time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC), Scope: TimeSeriesScope{SourceID: &source}, Dimensions: map[string]interface{}{"a": 1}}
	one, _ := TimeSeriesAggregateHash(base)
	base.Scope.SourceID = nil
	two, _ := TimeSeriesAggregateHash(base)
	if one == two {
		t.Fatal("aggregate hash omitted a grouping field")
	}
}

func TestTimeSeriesBucketBoundsUTC(t *testing.T) {
	at := time.Date(2026, time.March, 18, 15, 42, 0, 0, time.FixedZone("offset", 2*60*60))
	start, end, err := TimeSeriesBucketBounds(at, cfg.TimeSeriesBucketInterval("1mo"))
	if err != nil {
		t.Fatal(err)
	}
	if start.Location() != time.UTC || start != time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC) || end != time.Date(2026, time.April, 1, 0, 0, 0, 0, time.UTC) {
		t.Fatalf("unexpected month bounds %s %s", start, end)
	}
	weekStart, _, _ := TimeSeriesBucketBounds(at, cfg.TimeSeriesBucketOneWeek)
	if weekStart.Weekday() != time.Monday || weekStart.Hour() != 0 {
		t.Fatalf("week did not start Monday UTC: %s", weekStart)
	}
}

func TestTimeSeriesPrepareObservationPolicies(t *testing.T) {
	value := "secret-123456"
	o := TimeSeriesObservation{Value: TimeSeriesValue{Text: &value}, Dimensions: map[string]interface{}{"kind": "x"}}
	prepared, err := PrepareTimeSeriesObservation(o, cfg.TimeSeriesValueString, TimeSeriesPreparationPolicy{StoreValueText: true, MaxValueLength: 10, RedactPatterns: []string{`secret`}})
	if err != nil {
		t.Fatal(err)
	}
	if !prepared.Redacted || !prepared.Truncated || prepared.Observation.Value.Text == nil || *prepared.Observation.Value.Text != "[REDACTED]" {
		t.Fatalf("unexpected preparation: %#v", prepared)
	}
	_, err = PrepareTimeSeriesObservation(o, cfg.TimeSeriesValueString, TimeSeriesPreparationPolicy{CardinalityExceeded: true, Overflow: cfg.TimeSeriesCardinalityDrop})
	if !errors.Is(err, ErrTimeSeriesValueRejected) {
		t.Fatalf("expected policy rejection, got %v", err)
	}
}

func TestTimeSeriesObservationDuplicateAndBatchRollback(t *testing.T) {
	db := openSQLiteMemoryDB(t)
	defer db.Close()
	_, err := db.Exec(`CREATE TABLE TimeSeriesObservations (
		observation_id INTEGER PRIMARY KEY AUTOINCREMENT, metric_id INTEGER NOT NULL CHECK(metric_id > 0), observed_at TIMESTAMP NOT NULL,
		effective_at TIMESTAMP, collected_at TIMESTAMP NOT NULL, source_updated_at TIMESTAMP, bucket_start TIMESTAMP NOT NULL, bucket_end TIMESTAMP NOT NULL,
		information_seed_id INTEGER, information_seed_candidate_id INTEGER, source_id INTEGER, source_information_seed_id INTEGER, index_id INTEGER, entity_id INTEGER,
		subject_type TEXT, subject_id INTEGER, object_type TEXT, object_id INTEGER, correlation_rule_id INTEGER, correlation_object_type_1 TEXT,
		correlation_object_id_1 INTEGER, correlation_object_type_2 TEXT, correlation_object_id_2 INTEGER, value_numeric NUMERIC, value_integer INTEGER,
		value_boolean INTEGER, value_text TEXT, value_json TEXT, value_timestamp TIMESTAMP, value_hash TEXT NOT NULL, series_hash TEXT, previous_observation_id INTEGER,
		previous_value_hash TEXT, is_changed INTEGER NOT NULL, change_type TEXT, change_delta_numeric NUMERIC, change_detected_at TIMESTAMP,
		dedupe_key TEXT NOT NULL UNIQUE, dimensions TEXT, provenance TEXT, provenance_hash TEXT, created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		deleted_at TIMESTAMP, last_updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE TimeSeriesActiveSeries (metric_id INTEGER NOT NULL, series_hash TEXT NOT NULL, series_identity TEXT NOT NULL, reference_count INTEGER NOT NULL CHECK(reference_count > 0), PRIMARY KEY(metric_id, series_hash)); CREATE TABLE TimeSeriesActiveDimensionValues (metric_id INTEGER NOT NULL, dimension_key TEXT NOT NULL, value_hash TEXT NOT NULL, canonical_value TEXT NOT NULL, reference_count INTEGER NOT NULL CHECK(reference_count > 0), PRIMARY KEY(metric_id, dimension_key, value_hash))`); err != nil {
		t.Fatal(err)
	}
	var handler Handler = &SQLiteHandler{db: db, dbms: "SQLite"}
	now := time.Now().UTC().Truncate(time.Second)
	o := TimeSeriesObservation{MetricID: 1, ObservedAt: now, CollectedAt: now, BucketStart: now, BucketEnd: now.Add(time.Hour), ValueHash: "hash", DedupeKey: "same"}
	first, err := InsertTimeSeriesObservation(&handler, &o)
	if err != nil || !first.Inserted || first.Duplicate {
		t.Fatalf("first insert: %#v %v", first, err)
	}
	duplicate := o
	duplicate.ValueHash = "different-value-with-same-dedupe-identity"
	second, err := InsertTimeSeriesObservation(&handler, &duplicate)
	if err != nil || !second.Duplicate || second.ObservationID != first.ObservationID {
		t.Fatalf("duplicate insert: %#v %v", second, err)
	}
	var references int
	if err = db.QueryRow(`SELECT reference_count FROM TimeSeriesActiveSeries WHERE metric_id=? AND series_hash=?`, o.MetricID, o.SeriesHash).Scan(&references); err != nil || references != 1 {
		t.Fatalf("duplicate changed exact series references: count=%d err=%v", references, err)
	}
	var storedMetricID uint64
	var storedValueHash, storedSeriesHash, storedDedupeKey string
	if err = db.QueryRow(`SELECT metric_id, value_hash, series_hash, dedupe_key FROM TimeSeriesObservations WHERE observation_id=?`, first.ObservationID).
		Scan(&storedMetricID, &storedValueHash, &storedSeriesHash, &storedDedupeKey); err != nil {
		t.Fatalf("read inserted observation: %v", err)
	}
	if storedMetricID != o.MetricID || storedValueHash != o.ValueHash || storedDedupeKey != o.DedupeKey {
		t.Fatalf("unexpected stored observation: metric=%d value_hash=%q dedupe_key=%q", storedMetricID, storedValueHash, storedDedupeKey)
	}
	wantSeriesHash, hashErr := TimeSeriesSeriesHash(o.MetricID, o.Scope, o.Dimensions)
	if hashErr != nil || o.SeriesHash != wantSeriesHash || storedSeriesHash != wantSeriesHash {
		t.Fatalf("series hash was not populated deterministically: object=%q stored=%q want=%q err=%v", o.SeriesHash, storedSeriesHash, wantSeriesHash, hashErr)
	}
	bad := o
	bad.DedupeKey = "bad"
	bad.MetricID = 0
	good := o
	good.DedupeKey = "rolled-back"
	if _, err = InsertTimeSeriesObservations(&handler, []TimeSeriesObservation{good, bad}); err == nil {
		t.Fatal("expected batch error")
	}
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM TimeSeriesObservations WHERE dedupe_key='rolled-back'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("batch did not roll back: count=%d err=%v", count, err)
	}
	if err = db.QueryRow(`SELECT reference_count FROM TimeSeriesActiveSeries WHERE metric_id=? AND series_hash=?`, o.MetricID, o.SeriesHash).Scan(&references); err != nil || references != 1 {
		t.Fatalf("failed batch changed exact series references: count=%d err=%v", references, err)
	}
}

func TestPostgresTimeSeriesObservationInsertReturnsIDInOneStatement(t *testing.T) {
	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	handler := Handler(&PostgresHandler{db: database, dbms: DBPostgresStr})
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	o := TimeSeriesObservation{
		MetricID: 8, ObservedAt: now, CollectedAt: now.Add(time.Minute),
		BucketStart: now, BucketEnd: now.Add(time.Hour), ValueHash: "value-hash",
		DedupeKey: "postgres-new", Dimensions: map[string]interface{}{"region": "eu"},
		Provenance: json.RawMessage(`{"source":"test"}`),
	}

	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)^INSERT INTO TimeSeriesObservations .* ON CONFLICT \(dedupe_key\) DO NOTHING RETURNING observation_id$`).
		WillReturnRows(sqlmock.NewRows([]string{"observation_id"}).AddRow(uint64(91)))
	expectTimeSeriesAccounting(mock, 1)
	mock.ExpectCommit()

	result, err := InsertTimeSeriesObservation(&handler, &o)
	if err != nil {
		t.Fatalf("insert observation: %v", err)
	}
	if result.ObservationID != 91 || !result.Inserted || result.Duplicate || o.ID != 91 {
		t.Fatalf("unexpected insert result: %#v observation ID=%d", result, o.ID)
	}
	if o.DedupeKey != "postgres-new" || o.ValueHash != "value-hash" || o.Dimensions["region"] != "eu" || string(o.Provenance) != `{"source":"test"}` {
		t.Fatalf("insert mutated observation contents: %#v", o)
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("new-row path should issue exactly one persistence statement: %v", err)
	}
}

func TestPostgresTimeSeriesObservationDuplicateLooksUpExistingID(t *testing.T) {
	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	handler := Handler(&PostgresHandler{db: database, dbms: DBPostgresStr})
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	o := TimeSeriesObservation{MetricID: 8, ObservedAt: now, CollectedAt: now, BucketStart: now, BucketEnd: now.Add(time.Hour), ValueHash: "same-value", DedupeKey: "same-key"}

	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)^INSERT INTO TimeSeriesObservations .* ON CONFLICT \(dedupe_key\) DO NOTHING RETURNING observation_id$`).
		WillReturnRows(sqlmock.NewRows([]string{"observation_id"}))
	mock.ExpectQuery(`^SELECT observation_id FROM TimeSeriesObservations WHERE dedupe_key = \$1$`).
		WithArgs("same-key").WillReturnRows(sqlmock.NewRows([]string{"observation_id"}).AddRow(uint64(37)))
	mock.ExpectCommit()

	result, err := InsertTimeSeriesObservation(&handler, &o)
	if err != nil {
		t.Fatalf("insert duplicate: %v", err)
	}
	if result.ObservationID != 37 || result.Inserted || !result.Duplicate || o.ID != 37 {
		t.Fatalf("unexpected duplicate result: %#v observation ID=%d", result, o.ID)
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresTimeSeriesObservationInsertErrorRollsBack(t *testing.T) {
	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	handler := Handler(&PostgresHandler{db: database, dbms: DBPostgresStr})
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	o := TimeSeriesObservation{MetricID: 8, ObservedAt: now, CollectedAt: now, BucketStart: now, BucketEnd: now.Add(time.Hour), ValueHash: "value", DedupeKey: "error-key"}
	insertErr := errors.New("constraint failure unrelated to dedupe")

	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)^INSERT INTO TimeSeriesObservations .* RETURNING observation_id$`).WillReturnError(insertErr)
	mock.ExpectRollback()

	_, err = InsertTimeSeriesObservation(&handler, &o)
	if !errors.Is(err, insertErr) {
		t.Fatalf("expected non-dedupe insert error, got %v", err)
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresTimeSeriesObservationBatchPreservesResultsAndPayloads(t *testing.T) {
	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	handler := Handler(&PostgresHandler{db: database, dbms: DBPostgresStr})
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	numeric := 12.5
	sourceID := uint64(44)
	observations := []TimeSeriesObservation{
		{MetricID: 8, ObservedAt: now, CollectedAt: now, BucketStart: now, BucketEnd: now.Add(time.Hour), Value: TimeSeriesValue{Numeric: &numeric}, ValueHash: "hash-a", DedupeKey: "new-a", Dimensions: map[string]interface{}{"region": "eu", "rank": 1}, Provenance: json.RawMessage(`{"source":"feed","page":2}`), ProvenanceHash: "provenance-a", Scope: TimeSeriesScope{SourceID: &sourceID}},
		{MetricID: 8, ObservedAt: now.Add(time.Minute), CollectedAt: now, BucketStart: now, BucketEnd: now.Add(time.Hour), ValueHash: "hash-old", DedupeKey: "existing"},
		{MetricID: 8, ObservedAt: now.Add(2 * time.Minute), CollectedAt: now, BucketStart: now, BucketEnd: now.Add(time.Hour), ValueHash: "hash-a-copy", DedupeKey: "new-a"},
		{MetricID: 8, ObservedAt: now.Add(3 * time.Minute), CollectedAt: now, BucketStart: now, BucketEnd: now.Add(time.Hour), ValueHash: "hash-b", DedupeKey: "new-b"},
	}
	wantSeriesHash, err := TimeSeriesSeriesHash(observations[0].MetricID, observations[0].Scope, observations[0].Dimensions)
	if err != nil {
		t.Fatal(err)
	}

	args := make([]driver.Value, len(observations)*40)
	for i := range args {
		args[i] = sqlmock.AnyArg()
	}
	args[28] = "hash-a"
	args[29] = wantSeriesHash
	args[36] = "new-a"
	args[37] = timeSeriesArgumentMatcher(func(v driver.Value) bool { return v == `{"rank":1,"region":"eu"}` })
	args[38] = timeSeriesArgumentMatcher(func(v driver.Value) bool { return v == `{"page":2,"source":"feed"}` })
	args[39] = "provenance-a"

	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)^INSERT INTO TimeSeriesObservations .* VALUES \(.*\),\(.*\),\(.*\),\(.*\) ON CONFLICT \(dedupe_key\) DO NOTHING RETURNING observation_id, dedupe_key$`).
		WithArgs(args...).WillReturnRows(sqlmock.NewRows([]string{"observation_id", "dedupe_key"}).AddRow(uint64(101), "new-a").AddRow(uint64(103), "new-b"))
	mock.ExpectQuery(`^SELECT observation_id, dedupe_key FROM TimeSeriesObservations WHERE dedupe_key IN \(\$1,\$2\)$`).
		WithArgs("existing", "new-a").WillReturnRows(sqlmock.NewRows([]string{"observation_id", "dedupe_key"}).AddRow(uint64(55), "existing").AddRow(uint64(101), "new-a"))
	expectTimeSeriesAccounting(mock, 2)
	expectTimeSeriesAccounting(mock, 0)
	mock.ExpectCommit()

	results, err := InsertTimeSeriesObservations(&handler, observations)
	if err != nil {
		t.Fatalf("insert batch: %v", err)
	}
	want := []TimeSeriesInsertResult{
		{ObservationID: 101, Inserted: true},
		{ObservationID: 55, Duplicate: true},
		{ObservationID: 101, Duplicate: true},
		{ObservationID: 103, Inserted: true},
	}
	if fmt.Sprint(results) != fmt.Sprint(want) {
		t.Fatalf("results lost input order or conflict identities: got %#v want %#v", results, want)
	}
	for i := range observations {
		if observations[i].ID != want[i].ObservationID {
			t.Errorf("observation %d ID=%d want %d", i, observations[i].ID, want[i].ObservationID)
		}
	}
	if observations[0].SeriesHash != wantSeriesHash || observations[0].Value.Numeric == nil || *observations[0].Value.Numeric != numeric || observations[0].Dimensions["region"] != "eu" || string(observations[0].Provenance) != `{"source":"feed","page":2}` {
		t.Fatalf("batch changed prepared values, dimensions, hashes, or provenance: %#v", observations[0])
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("four inputs should use two persistence statements, fewer than four single-row inserts: %v", err)
	}
}

func TestPostgresTimeSeriesObservationBatchAllNewUsesOneStatement(t *testing.T) {
	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	handler := Handler(&PostgresHandler{db: database, dbms: DBPostgresStr})
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	observations := make([]TimeSeriesObservation, 3)
	rows := sqlmock.NewRows([]string{"observation_id", "dedupe_key"})
	for i := range observations {
		observations[i] = TimeSeriesObservation{MetricID: 1, ObservedAt: now, CollectedAt: now, BucketStart: now, BucketEnd: now.Add(time.Hour), ValueHash: fmt.Sprintf("hash-%d", i), DedupeKey: fmt.Sprintf("key-%d", i)}
		rows.AddRow(uint64(i+1), observations[i].DedupeKey)
	}
	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)^INSERT INTO TimeSeriesObservations .* RETURNING observation_id, dedupe_key$`).WillReturnRows(rows)
	for range observations {
		expectTimeSeriesAccounting(mock, 0)
	}
	mock.ExpectCommit()
	results, err := InsertTimeSeriesObservations(&handler, observations)
	if err != nil || len(results) != 3 {
		t.Fatalf("results=%#v err=%v", results, err)
	}
	for i, result := range results {
		if !result.Inserted || result.Duplicate || result.ObservationID != uint64(i+1) {
			t.Fatalf("result %d: %#v", i, result)
		}
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresTimeSeriesObservationBatchAllConflictsAndRollback(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	makeObservations := func() []TimeSeriesObservation {
		out := make([]TimeSeriesObservation, 2)
		for i := range out {
			out[i] = TimeSeriesObservation{MetricID: 1, ObservedAt: now, CollectedAt: now, BucketStart: now, BucketEnd: now.Add(time.Hour), ValueHash: "hash", DedupeKey: fmt.Sprintf("old-%d", i)}
		}
		return out
	}
	t.Run("all conflicts", func(t *testing.T) {
		database, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer database.Close()
		handler := Handler(&PostgresHandler{db: database, dbms: DBPostgresStr})
		mock.ExpectBegin()
		mock.ExpectQuery(`(?s)^INSERT INTO TimeSeriesObservations .* RETURNING observation_id, dedupe_key$`).WillReturnRows(sqlmock.NewRows([]string{"observation_id", "dedupe_key"}))
		mock.ExpectQuery(`^SELECT observation_id, dedupe_key .*`).WillReturnRows(sqlmock.NewRows([]string{"observation_id", "dedupe_key"}).AddRow(7, "old-0").AddRow(8, "old-1"))
		mock.ExpectCommit()
		results, err := InsertTimeSeriesObservations(&handler, makeObservations())
		if err != nil || len(results) != 2 || !results[0].Duplicate || !results[1].Duplicate {
			t.Fatalf("results=%#v err=%v", results, err)
		}
		if err = mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("second chunk failure rolls back first", func(t *testing.T) {
		database, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer database.Close()
		handler := Handler(&PostgresHandler{db: database, dbms: DBPostgresStr})
		observations := make([]TimeSeriesObservation, timeSeriesPostgresObservationBatchSize+1)
		for i := range observations {
			observations[i] = TimeSeriesObservation{MetricID: 1, ObservedAt: now, CollectedAt: now, BucketStart: now, BucketEnd: now.Add(time.Hour), ValueHash: "hash", DedupeKey: fmt.Sprintf("key-%d", i)}
		}
		firstRows := sqlmock.NewRows([]string{"observation_id", "dedupe_key"})
		for i := 0; i < timeSeriesPostgresObservationBatchSize; i++ {
			firstRows.AddRow(i+1, observations[i].DedupeKey)
		}
		failure := errors.New("non-dedupe database failure")
		mock.ExpectBegin()
		mock.ExpectQuery(`(?s)^INSERT INTO TimeSeriesObservations .* RETURNING observation_id, dedupe_key$`).WillReturnRows(firstRows)
		for i := 0; i < timeSeriesPostgresObservationBatchSize; i++ {
			expectTimeSeriesAccounting(mock, 0)
		}
		mock.ExpectQuery(`(?s)^INSERT INTO TimeSeriesObservations .* RETURNING observation_id, dedupe_key$`).WillReturnError(failure)
		mock.ExpectRollback()
		_, err = InsertTimeSeriesObservations(&handler, observations)
		if !errors.Is(err, failure) || !strings.Contains(err.Error(), "item 250") {
			t.Fatalf("expected indexed chunk failure, got %v", err)
		}
		if err = mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	if timeSeriesPostgresObservationBatchSize*40 >= 65535 {
		t.Fatalf("batch uses too many PostgreSQL parameters")
	}
}
