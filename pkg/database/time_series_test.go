package database

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

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
		value_boolean INTEGER, value_text TEXT, value_json TEXT, value_timestamp TIMESTAMP, value_hash TEXT NOT NULL, previous_observation_id INTEGER,
		previous_value_hash TEXT, is_changed INTEGER NOT NULL, change_type TEXT, change_delta_numeric NUMERIC, change_detected_at TIMESTAMP,
		dedupe_key TEXT NOT NULL UNIQUE, dimensions TEXT, provenance TEXT, provenance_hash TEXT, created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		deleted_at TIMESTAMP, last_updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
	if err != nil {
		t.Fatal(err)
	}
	var handler Handler = &SQLiteHandler{db: db, dbms: "SQLite"}
	now := time.Now().UTC().Truncate(time.Second)
	o := TimeSeriesObservation{MetricID: 1, ObservedAt: now, CollectedAt: now, BucketStart: now, BucketEnd: now.Add(time.Hour), ValueHash: "hash", DedupeKey: "same"}
	first, err := InsertTimeSeriesObservation(&handler, &o)
	if err != nil || !first.Inserted || first.Duplicate {
		t.Fatalf("first insert: %#v %v", first, err)
	}
	second, err := InsertTimeSeriesObservation(&handler, &o)
	if err != nil || !second.Duplicate || second.ObservationID != first.ObservationID {
		t.Fatalf("duplicate insert: %#v %v", second, err)
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
}
