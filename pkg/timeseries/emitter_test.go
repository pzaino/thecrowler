package timeseries

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

type fakeRepository struct {
	metrics      []cdb.TimeSeriesMetric
	observations []cdb.TimeSeriesObservation
	insertErr    error
	listErr      error
}

func (f *fakeRepository) ListMetrics(cdb.TimeSeriesMetricFilter) ([]cdb.TimeSeriesMetric, error) {
	return f.metrics, f.listErr
}
func (f *fakeRepository) PreviousObservation(lookup cdb.TimeSeriesChangeLookup) (*cdb.TimeSeriesObservation, error) {
	for i := len(f.observations) - 1; i >= 0; i-- {
		if f.observations[i].MetricID == lookup.MetricID {
			copy := f.observations[i]
			return &copy, nil
		}
	}
	return nil, cdb.ErrTimeSeriesObservationNotFound
}
func (f *fakeRepository) InsertObservation(observation *cdb.TimeSeriesObservation) (cdb.TimeSeriesInsertResult, error) {
	if f.insertErr != nil {
		return cdb.TimeSeriesInsertResult{}, f.insertErr
	}
	for _, existing := range f.observations {
		if existing.DedupeKey == observation.DedupeKey {
			return cdb.TimeSeriesInsertResult{ObservationID: existing.ID, Duplicate: true}, nil
		}
	}
	copy := *observation
	copy.ID = uint64(len(f.observations) + 1)
	f.observations = append(f.observations, copy)
	return cdb.TimeSeriesInsertResult{ObservationID: copy.ID, Inserted: true}, nil
}

type fakeScopes struct {
	scopes []cdb.TimeSeriesScope
	err    error
}

func (f fakeScopes) ResolveScopes(ObjectAttributeInput) ([]cdb.TimeSeriesScope, error) {
	return f.scopes, f.err
}

type fakeLogger struct{ calls int }

func (f *fakeLogger) Printf(string, ...interface{}) { f.calls++ }

func TestObjectAttributeValueTypes(t *testing.T) {
	tests := []struct {
		name  string
		typ   cfg.TimeSeriesValueType
		input interface{}
		check func(cdb.TimeSeriesValue) bool
	}{
		{"count", cfg.TimeSeriesValueCount, "ignored", func(v cdb.TimeSeriesValue) bool { return v.Integer != nil && *v.Integer == 1 }},
		{"integer", cfg.TimeSeriesValueInteger, "42", func(v cdb.TimeSeriesValue) bool { return v.Integer != nil && *v.Integer == 42 }},
		{"decimal", cfg.TimeSeriesValueDecimal, "4.25", func(v cdb.TimeSeriesValue) bool { return v.Numeric != nil && *v.Numeric == 4.25 }},
		{"boolean", cfg.TimeSeriesValueBoolean, "true", func(v cdb.TimeSeriesValue) bool { return v.Boolean != nil && *v.Boolean }},
		{"string", cfg.TimeSeriesValueString, "hello", func(v cdb.TimeSeriesValue) bool { return v.Text != nil && *v.Text == "hello" }},
		{"json", cfg.TimeSeriesValueJSON, `{"b":2,"a":1}`, func(v cdb.TimeSeriesValue) bool { return json.Valid(v.JSON) }},
		{"duration", cfg.TimeSeriesValueDuration, "1500ms", func(v cdb.TimeSeriesValue) bool { return v.Numeric != nil && *v.Numeric == 1.5 }},
		{"timestamp", cfg.TimeSeriesValueTimestamp, "2026-06-06T12:30:00Z", func(v cdb.TimeSeriesValue) bool { return v.Timestamp != nil && v.Timestamp.Year() == 2026 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			value, err := parseValue(tc.typ, tc.input)
			if err != nil {
				t.Fatal(err)
			}
			if !tc.check(value) {
				t.Fatalf("unexpected parsed value: %#v", value)
			}
		})
	}
}

func TestObjectAttributeTimeSeriesScopesDimensionsPrivacyAndChange(t *testing.T) {
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	selector := json.RawMessage(`{"attribute_key":"latency","path":"value","transformations":["trim"]}`)
	dimensions, _ := json.Marshal([]cfg.TimeSeriesDimensionConfig{{Key: "region", Selector: map[string]interface{}{"path": "region"}}, {Key: "status", Selector: map[string]interface{}{"from": "sibling", "attribute_key": "status"}}, {Key: "kind", Selector: map[string]interface{}{"from": "metric", "path": "object_type"}}, {Key: "fixed", Selector: map[string]interface{}{"constant": "secret-123"}}})
	metric := cdb.TimeSeriesMetric{ID: 9, Key: "latency", SourceKind: cfg.TimeSeriesSourceObjectAttribute, ObjectType: cfg.TimeSeriesObjectWebObject, ValueType: cfg.TimeSeriesValueDuration, Bucket: cfg.TimeSeriesBucketOneHour, TimeBasis: cfg.TimeSeriesTimeObservedAt, DedupeScope: cfg.TimeSeriesDedupeObject, FailurePolicy: cfg.TimeSeriesFailureLogSkip, Selector: selector, Dimensions: dimensions, Enabled: true}
	source, seed, sourceSeed, index, object, entity := uint64(3), uint64(4), uint64(5), uint64(6), uint64(7), uint64(8)
	repo := &fakeRepository{metrics: []cdb.TimeSeriesMetric{metric}}
	configuration := &cfg.TimeSeriesConfig{Enabled: true, Defaults: cfg.TimeSeriesMetricDefaults{FailurePolicy: cfg.TimeSeriesFailureLogSkip}, Privacy: cfg.TimeSeriesPrivacyConfig{StoreValueText: true, MaxValueLength: 2048, RedactPatterns: []string{`secret-[0-9]+`}}, Cardinality: cfg.TimeSeriesCardinalityConfig{MaxDimensions: 10, Overflow: cfg.TimeSeriesCardinalityDrop}}
	emitter := Emitter{Repository: repo, Scopes: fakeScopes{scopes: []cdb.TimeSeriesScope{{SourceID: &source, InformationSeedID: &seed, SourceInformationSeedID: &sourceSeed, IndexID: &index, ObjectType: "webobject", ObjectID: &object, EntityID: &entity}}}, Config: configuration, Now: func() time.Time { return now }}
	input := ObjectAttributeInput{ObjectType: "webobject", ObjectID: object, AttributeKey: "latency", RawValue: `{"value":"1500ms"}`, NormalizedValue: `{"value":"1500ms"}`, ObjectDetails: map[string]interface{}{"region": "west"}, SiblingAttributes: map[string]interface{}{"status": "ok"}, ObservedAt: now}
	if err := emitter.EmitObjectAttribute(input); err != nil {
		t.Fatal(err)
	}
	if len(repo.observations) != 1 {
		t.Fatalf("expected one observation, got %d", len(repo.observations))
	}
	first := repo.observations[0]
	if first.Value.Numeric == nil || *first.Value.Numeric != 1.5 {
		t.Fatalf("duration not parsed: %#v", first.Value)
	}
	if first.Scope.SourceID == nil || first.Scope.InformationSeedID == nil || first.Scope.IndexID == nil || first.Scope.ObjectID == nil || first.Scope.EntityID == nil {
		t.Fatalf("scope incomplete: %#v", first.Scope)
	}
	if first.Dimensions["fixed"] != "[REDACTED]" || first.Dimensions["region"] != "west" || first.Dimensions["status"] != "ok" || first.Dimensions["kind"] != "webobject" {
		t.Fatalf("dimensions incorrect: %#v", first.Dimensions)
	}
	if first.ChangeType != "new" || !first.IsChanged {
		t.Fatalf("new change state incorrect: %#v", first)
	}
	input.ObservedAt = now.Add(time.Hour)
	if err := emitter.EmitObjectAttribute(input); err != nil {
		t.Fatal(err)
	}
	if got := repo.observations[1]; got.ChangeType != "unchanged" || got.IsChanged || got.PreviousValueHash == "" {
		t.Fatalf("unchanged state incorrect: %#v", got)
	}
	input.RawValue, input.NormalizedValue, input.ObservedAt = `{"value":"2s"}`, `{"value":"2s"}`, now.Add(2*time.Hour)
	if err := emitter.EmitObjectAttribute(input); err != nil {
		t.Fatal(err)
	}
	if got := repo.observations[2]; got.ChangeType != "changed" || !got.IsChanged || got.ChangeDeltaNumeric == nil || *got.ChangeDeltaNumeric != .5 {
		t.Fatalf("changed state incorrect: %#v", got)
	}
}

func TestObjectAttributeTimeSeriesDirectSourceDedupeAndPolicies(t *testing.T) {
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	metric := cdb.TimeSeriesMetric{ID: 1, Key: "name", SourceKind: cfg.TimeSeriesSourceObjectAttribute, ObjectType: cfg.TimeSeriesObjectWebObject, ValueType: cfg.TimeSeriesValueString, Bucket: cfg.TimeSeriesBucketNone, TimeBasis: cfg.TimeSeriesTimeObservedAt, DedupeScope: cfg.TimeSeriesDedupeObject, FailurePolicy: cfg.TimeSeriesFailureLogSkip, Selector: json.RawMessage(`{"attribute_key":"name"}`), Enabled: true, HashOnly: true}
	source, index, object := uint64(2), uint64(3), uint64(4)
	repo := &fakeRepository{metrics: []cdb.TimeSeriesMetric{metric}}
	logger := &fakeLogger{}
	configuration := &cfg.TimeSeriesConfig{Enabled: true, Defaults: cfg.TimeSeriesMetricDefaults{FailurePolicy: cfg.TimeSeriesFailureLogSkip}, Privacy: cfg.TimeSeriesPrivacyConfig{MaxValueLength: 100}, Cardinality: cfg.TimeSeriesCardinalityConfig{MaxDimensions: 2, Overflow: cfg.TimeSeriesCardinalityDrop}}
	emitter := Emitter{Repository: repo, Scopes: fakeScopes{scopes: []cdb.TimeSeriesScope{{SourceID: &source, IndexID: &index, ObjectType: "webobject", ObjectID: &object}}}, Config: configuration, Logger: logger, Now: func() time.Time { return now }}
	input := ObjectAttributeInput{ObjectType: "webobject", ObjectID: object, AttributeKey: "name", RawValue: "Alice", NormalizedValue: "Alice", ObservedAt: now}
	if err := emitter.EmitObjectAttribute(input); err != nil {
		t.Fatal(err)
	}
	if err := emitter.EmitObjectAttribute(input); err != nil {
		t.Fatal(err)
	}
	if len(repo.observations) != 1 {
		t.Fatalf("dedupe failed: %d observations", len(repo.observations))
	}
	if repo.observations[0].Scope.InformationSeedID != nil || repo.observations[0].Value.Text != nil || repo.observations[0].ValueHash == "" {
		t.Fatalf("direct/hash-only observation incorrect: %#v", repo.observations[0])
	}
	repo.insertErr = errors.New("write failed")
	if err := emitter.EmitObjectAttribute(ObjectAttributeInput{ObjectType: "webobject", ObjectID: object, AttributeKey: "name", RawValue: "Bob", NormalizedValue: "Bob", ObservedAt: now.Add(time.Hour)}); err != nil {
		t.Fatalf("default policy interrupted indexing: %v", err)
	}
	if logger.calls == 0 {
		t.Fatal("expected safe failure to be logged")
	}
	repo.metrics[0].FailurePolicy = cfg.TimeSeriesFailureFailIndexing
	if err := emitter.EmitObjectAttribute(ObjectAttributeInput{ObjectType: "webobject", ObjectID: object, AttributeKey: "name", RawValue: "Carol", NormalizedValue: "Carol", ObservedAt: now.Add(2 * time.Hour)}); err == nil || !errors.Is(err, repo.insertErr) {
		t.Fatalf("expected fail_indexing error, got %v", err)
	}
}

func TestObjectAttributeSelectorMismatchDoesNotEmit(t *testing.T) {
	repo := &fakeRepository{metrics: []cdb.TimeSeriesMetric{{ID: 1, Key: "x", SourceKind: cfg.TimeSeriesSourceObjectAttribute, ObjectType: cfg.TimeSeriesObjectWebObject, ValueType: cfg.TimeSeriesValueInteger, Bucket: cfg.TimeSeriesBucketNone, TimeBasis: cfg.TimeSeriesTimeObservedAt, DedupeScope: cfg.TimeSeriesDedupeObject, Selector: json.RawMessage(`{"attribute_key":"other"}`), Enabled: true}}}
	emitter := Emitter{Repository: repo, Scopes: fakeScopes{}, Config: &cfg.TimeSeriesConfig{Enabled: true}}
	if err := emitter.EmitObjectAttribute(ObjectAttributeInput{ObjectType: "webobject", ObjectID: 1, AttributeKey: "value", NormalizedValue: "1"}); err != nil {
		t.Fatal(err)
	}
	if len(repo.observations) != 0 {
		t.Fatal(fmt.Sprintf("unexpected observations: %d", len(repo.observations)))
	}
}

type fakeArtifactScopes struct {
	scopes []cdb.TimeSeriesScope
	err    error
}

func (f fakeArtifactScopes) ResolveIndexedArtifactScopes(IndexedArtifactInput) ([]cdb.TimeSeriesScope, error) {
	return f.scopes, f.err
}

func TestKeywordTimeSeriesGenericExactAndOccurrences(t *testing.T) {
	now := time.Date(2026, 6, 6, 14, 0, 0, 0, time.UTC)
	metrics := []cdb.TimeSeriesMetric{
		{ID: 1, Key: "all_keywords", SourceKind: cfg.TimeSeriesSourceKeyword, ValueType: cfg.TimeSeriesValueCount, Bucket: cfg.TimeSeriesBucketNone, TimeBasis: cfg.TimeSeriesTimeObservedAt, DedupeScope: cfg.TimeSeriesDedupeObject, Selector: json.RawMessage(`{}`), Enabled: true},
		{ID: 2, Key: "exact_keyword", SourceKind: cfg.TimeSeriesSourceKeyword, ValueType: cfg.TimeSeriesValueInteger, Bucket: cfg.TimeSeriesBucketNone, TimeBasis: cfg.TimeSeriesTimeObservedAt, DedupeScope: cfg.TimeSeriesDedupeObject, Selector: json.RawMessage(`{"keyword":"crowler"}`), Enabled: true},
		{ID: 3, Key: "rule_keyword", SourceKind: cfg.TimeSeriesSourceKeyword, ValueType: cfg.TimeSeriesValueInteger, Bucket: cfg.TimeSeriesBucketNone, TimeBasis: cfg.TimeSeriesTimeObservedAt, DedupeScope: cfg.TimeSeriesDedupeObject, Selector: json.RawMessage(`{"rule":{"prefix":"crow"}}`), Enabled: true},
	}
	repo := &fakeRepository{metrics: metrics}
	source, seed, sourceSeed, index, entity := uint64(3), uint64(4), uint64(5), uint64(6), uint64(7)
	emitter := Emitter{
		Repository:     repo,
		ArtifactScopes: fakeArtifactScopes{scopes: []cdb.TimeSeriesScope{{SourceID: &source, InformationSeedID: &seed, SourceInformationSeedID: &sourceSeed, IndexID: &index, EntityID: &entity}}},
		Config:         &cfg.TimeSeriesConfig{Enabled: true, Defaults: cfg.TimeSeriesMetricDefaults{FailurePolicy: cfg.TimeSeriesFailureLogSkip}, Cardinality: cfg.TimeSeriesCardinalityConfig{MaxDimensions: 10, Overflow: cfg.TimeSeriesCardinalityDrop}},
		Now:            func() time.Time { return now },
	}
	if err := emitter.EmitIndexedArtifact(IndexedArtifactInput{SourceKind: cfg.TimeSeriesSourceKeyword, IndexID: index, RowID: 11, LinkID: 12, SubjectKey: "crowler", Value: int64(9), Occurrences: 9, ObservedAt: now}); err != nil {
		t.Fatal(err)
	}
	if len(repo.observations) != 3 {
		t.Fatalf("expected generic, exact, and rule observations, got %d", len(repo.observations))
	}
	for _, observation := range repo.observations {
		if observation.Value.Integer == nil || *observation.Value.Integer != 9 {
			t.Fatalf("stored occurrences were not emitted: %#v", observation.Value)
		}
		if observation.Scope.SubjectType != "keyword" || observation.Scope.SubjectID == nil || *observation.Scope.SubjectID != 11 || observation.Scope.SubjectText != "crowler" {
			t.Fatalf("keyword subject scope is incorrect: %#v", observation.Scope)
		}
		if observation.Scope.SourceID == nil || observation.Scope.InformationSeedID == nil || observation.Scope.SourceInformationSeedID == nil || observation.Scope.IndexID == nil || observation.Scope.EntityID == nil {
			t.Fatalf("keyword ownership scope is incomplete: %#v", observation.Scope)
		}
		var provenance map[string]interface{}
		if err := json.Unmarshal(observation.Provenance, &provenance); err != nil {
			t.Fatal(err)
		}
		if provenance["normalized_keyword"] != "crowler" || provenance["parser"] == nil || provenance["keyword_index_id"] == nil {
			t.Fatalf("keyword provenance is incomplete: %#v", provenance)
		}
	}
}

func TestMetaTagTimeSeriesTypedValueCaseInsensitiveNameAndTimestamp(t *testing.T) {
	observed := time.Date(2026, 6, 6, 15, 0, 0, 0, time.UTC)
	eventAt := "2026-06-05T10:30:00Z"
	metric := cdb.TimeSeriesMetric{ID: 21, Key: "published", SourceKind: cfg.TimeSeriesSourceMetatag, ValueType: cfg.TimeSeriesValueTimestamp, Bucket: cfg.TimeSeriesBucketNone, TimeBasis: cfg.TimeSeriesTimeEventAt, DedupeScope: cfg.TimeSeriesDedupeObject, Selector: json.RawMessage(`{"metatag_name":"ARTICLE:PUBLISHED_TIME"}`), Enabled: true}
	repo := &fakeRepository{metrics: []cdb.TimeSeriesMetric{metric}}
	index := uint64(8)
	configuration := &cfg.TimeSeriesConfig{
		Enabled:     true,
		Defaults:    cfg.TimeSeriesMetricDefaults{FailurePolicy: cfg.TimeSeriesFailureLogSkip},
		Cardinality: cfg.TimeSeriesCardinalityConfig{MaxDimensions: 10, Overflow: cfg.TimeSeriesCardinalityDrop},
		Metrics:     []cfg.TimeSeriesMetricConfig{{Key: "published", TimestampSelector: map[string]interface{}{"from": "content"}}},
	}
	emitter := Emitter{Repository: repo, ArtifactScopes: fakeArtifactScopes{scopes: []cdb.TimeSeriesScope{{IndexID: &index}}}, Config: configuration, Now: func() time.Time { return observed }}
	input := IndexedArtifactInput{SourceKind: cfg.TimeSeriesSourceMetatag, IndexID: index, RowID: 31, LinkID: 32, SubjectKey: "article:published_time", Name: "Article:Published_Time", RawValue: eventAt, Value: eventAt, ObservedAt: observed}
	if err := emitter.EmitIndexedArtifact(input); err != nil {
		t.Fatal(err)
	}
	if len(repo.observations) != 1 {
		t.Fatalf("expected one metatag observation, got %d", len(repo.observations))
	}
	observation := repo.observations[0]
	if observation.Value.Timestamp == nil || observation.EffectiveAt == nil || !observation.EffectiveAt.Equal(*observation.Value.Timestamp) {
		t.Fatalf("timestamp value/event time is incorrect: %#v", observation)
	}
	if !observation.ObservedAt.Equal(observed) {
		t.Fatalf("metadata timestamp overwrote ingestion time: %s", observation.ObservedAt)
	}
	if observation.Scope.SubjectText != "article:published_time" || observation.Scope.SubjectType != "metatag" {
		t.Fatalf("metatag subject is incorrect: %#v", observation.Scope)
	}
	var provenance map[string]interface{}
	if err := json.Unmarshal(observation.Provenance, &provenance); err != nil {
		t.Fatal(err)
	}
	if provenance["normalized_name"] != "article:published_time" || provenance["timestamp_source"] != "content" || provenance["metatag_id"] == nil {
		t.Fatalf("metatag provenance is incomplete: %#v", provenance)
	}
}

func TestMetaTagInvalidTimestampFollowsFailurePolicy(t *testing.T) {
	observed := time.Date(2026, 6, 6, 16, 0, 0, 0, time.UTC)
	metric := cdb.TimeSeriesMetric{ID: 41, Key: "bad_time", SourceKind: cfg.TimeSeriesSourceMetatag, ValueType: cfg.TimeSeriesValueString, Bucket: cfg.TimeSeriesBucketNone, TimeBasis: cfg.TimeSeriesTimeSourceTimestamp, DedupeScope: cfg.TimeSeriesDedupeObject, FailurePolicy: cfg.TimeSeriesFailureLogSkip, Selector: json.RawMessage(`{"metatag_name":"last-modified"}`), Enabled: true}
	repo := &fakeRepository{metrics: []cdb.TimeSeriesMetric{metric}}
	logger := &fakeLogger{}
	configuration := &cfg.TimeSeriesConfig{Enabled: true, Defaults: cfg.TimeSeriesMetricDefaults{FailurePolicy: cfg.TimeSeriesFailureLogSkip}, Metrics: []cfg.TimeSeriesMetricConfig{{Key: "bad_time", TimestampSelector: map[string]interface{}{"from": "content"}}}}
	emitter := Emitter{Repository: repo, ArtifactScopes: fakeArtifactScopes{}, Config: configuration, Logger: logger, Now: func() time.Time { return observed }}
	input := IndexedArtifactInput{SourceKind: cfg.TimeSeriesSourceMetatag, IndexID: 1, RowID: 2, LinkID: 3, SubjectKey: "last-modified", RawValue: "not-a-time", Value: "not-a-time", ObservedAt: observed}
	if err := emitter.EmitIndexedArtifact(input); err != nil {
		t.Fatalf("log_skip should not fail indexing: %v", err)
	}
	if len(repo.observations) != 0 || logger.calls != 1 {
		t.Fatalf("invalid timestamp was not skipped/logged: observations=%d logs=%d", len(repo.observations), logger.calls)
	}
	metric.FailurePolicy = cfg.TimeSeriesFailureFailIndexing
	repo.metrics[0] = metric
	if err := emitter.EmitIndexedArtifact(input); err == nil {
		t.Fatal("fail_indexing should return the timestamp parsing error")
	}
	if len(repo.observations) != 0 {
		t.Fatal("invalid timestamp emitted an observation")
	}
}
