package database

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func openEntityTimeSeriesTestDB(t *testing.T) (*Handler, func()) {
	t.Helper()
	db := openSQLiteMemoryDB(t)
	schema, err := os.ReadFile("sqlite-setup.sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(schema)); err != nil {
		db.Close()
		t.Fatalf("apply sqlite schema: %v", err)
	}
	var handler Handler = &SQLiteHandler{db: db, dbms: "SQLite"}
	return &handler, func() { _ = db.Close() }
}

func testCorrelationMetric(t *testing.T, db *Handler, key string, source cfg.TimeSeriesSourceKind, field string, dimensions []cfg.TimeSeriesDimensionConfig) uint64 {
	t.Helper()
	selector, _ := json.Marshal(map[string]interface{}{"field": field})
	dimensionJSON, _ := json.Marshal(dimensions)
	metric, err := UpsertTimeSeriesMetric(db, &TimeSeriesMetric{Key: key, DisplayName: key, SourceKind: source, ValueType: cfg.TimeSeriesValueDecimal, Aggregate: cfg.TimeSeriesAggregateAverage, Bucket: cfg.TimeSeriesBucketOneHour, TimeBasis: cfg.TimeSeriesTimeObservedAt, DedupeScope: cfg.TimeSeriesDedupeObject, ObjectType: cfg.TimeSeriesObjectWebObject, FailurePolicy: cfg.TimeSeriesFailureFailIndexing, Selector: selector, Dimensions: dimensionJSON, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return metric.ID
}

func TestEntityMembershipCorrelationTimeSeriesEmission(t *testing.T) {
	db, closeDB := openEntityTimeSeriesTestDB(t)
	defer closeDB()
	membershipMetric := testCorrelationMetric(t, db, "membership_confidence", cfg.TimeSeriesSourceEntityMembership, "confidence", []cfg.TimeSeriesDimensionConfig{{Key: "membership_role", Selector: map[string]interface{}{"field": "membership_role"}}, {Key: "membership_type", Selector: map[string]interface{}{"field": "membership_type"}}})
	correlationMetric := testCorrelationMetric(t, db, "correlation_score", cfg.TimeSeriesSourceObjectCorrelation, "score", nil)
	ruleMetric := testCorrelationMetric(t, db, "rule_confidence", cfg.TimeSeriesSourceCorrelationRule, "confidence", nil)
	entity, err := UpsertEntity(db, &Entity{Type: "generic"})
	if err != nil {
		t.Fatal(err)
	}
	confidence := 0.91
	if err = UpsertEntityMembership(db, &EntityMembership{EntityID: entity.ID, ObjectType: "webobject", ObjectID: 10, Confidence: &confidence, MembershipRole: "primary", MembershipType: "inferred", Evidence: json.RawMessage(`{"extractor":"title"}`)}); err != nil {
		t.Fatal(err)
	}
	membershipResult, err := QueryTimeSeriesObservations(db, TimeSeriesQueryFilter{MetricID: &membershipMetric})
	if err != nil {
		t.Fatal(err)
	}
	if membershipResult.Count != 1 {
		t.Fatalf("membership observations=%d", membershipResult.Count)
	}
	membership := membershipResult.Observations[0]
	if membership.Scope.EntityID == nil || *membership.Scope.EntityID != entity.ID || membership.Value.Numeric == nil || *membership.Value.Numeric != confidence {
		t.Fatalf("unexpected membership observation: %#v", membership)
	}
	if membership.Dimensions["membership_role"] != "primary" || membership.Dimensions["membership_type"] != "inferred" {
		t.Fatalf("membership dimensions: %#v", membership.Dimensions)
	}

	rule, err := UpsertCorrelationRule(db, &CorrelationRule{Name: "generic fuzzy", Definition: json.RawMessage(`{"secret":"not-observed"}`), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	score, correlationConfidence := 0.87, 0.76
	if err = UpsertObjectCorrelation(db, &ObjectCorrelation{ObjectType1: "webobject", ObjectID1: 10, ObjectType2: "webobject", ObjectID2: 20, RuleID: rule.ID, EntityID: &entity.ID, Score: &score, Confidence: &correlationConfidence}); err != nil {
		t.Fatal(err)
	}
	correlationResult, err := QueryTimeSeriesObservations(db, TimeSeriesQueryFilter{MetricID: &correlationMetric, CorrelationRuleID: &rule.ID})
	if err != nil {
		t.Fatal(err)
	}
	if correlationResult.Count != 1 {
		t.Fatalf("correlation observations=%d", correlationResult.Count)
	}
	correlation := correlationResult.Observations[0]
	if correlation.Value.Numeric == nil || *correlation.Value.Numeric != score || correlation.Scope.CorrelationObjectID2 == nil || *correlation.Scope.CorrelationObjectID2 != 20 {
		t.Fatalf("unexpected correlation observation: %#v", correlation)
	}
	ruleResult, err := QueryTimeSeriesObservations(db, TimeSeriesQueryFilter{MetricID: &ruleMetric, CorrelationRuleID: &rule.ID})
	if err != nil {
		t.Fatal(err)
	}
	if ruleResult.Count != 1 || ruleResult.Observations[0].Value.Numeric == nil || *ruleResult.Observations[0].Value.Numeric != correlationConfidence {
		t.Fatalf("unexpected rule observation: %#v", ruleResult)
	}
	if string(ruleResult.Observations[0].Provenance) == "" || containsJSONText(ruleResult.Observations[0].Provenance, "secret") {
		t.Fatalf("rule definition leaked into provenance: %s", ruleResult.Observations[0].Provenance)
	}
}

func TestEntityObservationBackfillIdempotentPreservesConfidenceAndProvenance(t *testing.T) {
	db, closeDB := openEntityTimeSeriesTestDB(t)
	defer closeDB()
	metricID := testCorrelationMetric(t, db, "historical_value", cfg.TimeSeriesSourceCustom, "value", nil)
	now := time.Now().UTC().Truncate(time.Second)
	value := 4.2
	observation := TimeSeriesObservation{MetricID: metricID, ObservedAt: now, CollectedAt: now, BucketStart: now.Truncate(time.Hour), BucketEnd: now.Truncate(time.Hour).Add(time.Hour), Scope: TimeSeriesScope{ObjectType: "webobject", ObjectID: uint64Pointer(44)}, Value: TimeSeriesValue{Numeric: &value}, ValueHash: "historical", DedupeKey: "historical-44", Dimensions: map[string]interface{}{"confidence": 0.33, "extractor": "existing"}, Provenance: json.RawMessage(`{"extractor":{"name":"existing"},"correlation":{"rule":2}}`)}
	if _, err := InsertTimeSeriesObservation(db, &observation); err != nil {
		t.Fatal(err)
	}
	entity, err := UpsertEntity(db, &Entity{Type: "generic"})
	if err != nil {
		t.Fatal(err)
	}
	membershipConfidence := 0.95
	if err = UpsertEntityMembership(db, &EntityMembership{EntityID: entity.ID, ObjectType: "webobject", ObjectID: 44, Confidence: &membershipConfidence, Evidence: json.RawMessage(`{"source":"late"}`)}); err != nil {
		t.Fatal(err)
	}
	result, err := RunEntityObservationBackfillJob(db, "entity-test", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if result.Updated != 1 || result.Done || result.AffectedStart == nil || result.AffectedEnd == nil {
		t.Fatalf("unexpected first backfill result: %#v", result)
	}
	resumed, err := RunEntityObservationBackfillJob(db, "entity-test", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Updated != 0 || !resumed.Done {
		t.Fatalf("checkpointed backfill did not resume: %#v", resumed)
	}
	filtered, err := QueryTimeSeriesObservations(db, TimeSeriesQueryFilter{ObjectType: "webobject", ObjectID: uint64Pointer(44)})
	if err != nil {
		t.Fatal(err)
	}
	if filtered.Count != 1 {
		t.Fatalf("observations=%d", filtered.Count)
	}
	got := filtered.Observations[0]
	if got.Scope.EntityID == nil || *got.Scope.EntityID != entity.ID {
		t.Fatalf("entity not attached: %#v", got.Scope)
	}
	if got.Dimensions["confidence"] != 0.33 {
		t.Fatalf("existing confidence replaced: %#v", got.Dimensions)
	}
	if !containsJSONText(got.Provenance, "existing") || !containsJSONText(got.Provenance, "late") {
		t.Fatalf("provenance not merged: %s", got.Provenance)
	}
	again, err := RunEntityObservationBackfillJob(db, "entity-test", 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	if again.Updated != 0 {
		t.Fatalf("idempotent rerun updated %d observations", again.Updated)
	}
}

func uint64Pointer(value uint64) *uint64 { return &value }
func containsJSONText(raw json.RawMessage, text string) bool {
	return len(raw) > 0 && json.Valid(raw) && strings.Contains(string(raw), text)
}
