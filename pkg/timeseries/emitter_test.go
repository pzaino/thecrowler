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

func TestHTTPInfoArtifactSelectorsPresenceValueAndCertificateDays(t *testing.T) {
	observed := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	metrics := []cdb.TimeSeriesMetric{
		{ID: 101, Key: "server_present", SourceKind: cfg.TimeSeriesSourceHTTPInfo, ValueType: cfg.TimeSeriesValueBoolean, Bucket: cfg.TimeSeriesBucketNone, TimeBasis: cfg.TimeSeriesTimeObservedAt, DedupeScope: cfg.TimeSeriesDedupeObject, Selector: json.RawMessage(`{"from":"details","path":"response_headers.server","operation":"presence"}`), Enabled: true},
		{ID: 102, Key: "server_value", SourceKind: cfg.TimeSeriesSourceHTTPInfo, ValueType: cfg.TimeSeriesValueString, Bucket: cfg.TimeSeriesBucketNone, TimeBasis: cfg.TimeSeriesTimeObservedAt, DedupeScope: cfg.TimeSeriesDedupeObject, Selector: json.RawMessage(`{"from":"details","path":"response_headers.Server","transform":"first"}`), Enabled: true},
		{ID: 103, Key: "cert_days", SourceKind: cfg.TimeSeriesSourceHTTPInfo, ValueType: cfg.TimeSeriesValueDecimal, Bucket: cfg.TimeSeriesBucketNone, TimeBasis: cfg.TimeSeriesTimeObservedAt, DedupeScope: cfg.TimeSeriesDedupeObject, Selector: json.RawMessage(`{"from":"details","path":"ssl_info.cert_expiration","derive":"days_until"}`), Enabled: true},
		{ID: 104, Key: "cert_expiration", SourceKind: cfg.TimeSeriesSourceHTTPInfo, ValueType: cfg.TimeSeriesValueTimestamp, Bucket: cfg.TimeSeriesBucketNone, TimeBasis: cfg.TimeSeriesTimeObservedAt, DedupeScope: cfg.TimeSeriesDedupeObject, Selector: json.RawMessage(`{"from":"details","path":"ssl_info.cert_expiration"}`), Enabled: true},
	}
	repo := &fakeRepository{metrics: metrics}
	index := uint64(5)
	emitter := Emitter{Repository: repo, ArtifactScopes: fakeArtifactScopes{scopes: []cdb.TimeSeriesScope{{IndexID: &index}}}, Config: &cfg.TimeSeriesConfig{Enabled: true, Defaults: cfg.TimeSeriesMetricDefaults{FailurePolicy: cfg.TimeSeriesFailureLogSkip}, Privacy: cfg.TimeSeriesPrivacyConfig{StoreValueText: true, MaxValueLength: 2048}, Cardinality: cfg.TimeSeriesCardinalityConfig{MaxDimensions: 10, Overflow: cfg.TimeSeriesCardinalityDrop}}, Now: func() time.Time { return observed }}
	details := map[string]interface{}{"response_headers": map[string]interface{}{"Server": []interface{}{"nginx"}}, "ssl_info": map[string]interface{}{"cert_expiration": "2026-06-16T12:00:00Z"}}
	if err := emitter.EmitIndexedArtifact(IndexedArtifactInput{SourceKind: cfg.TimeSeriesSourceHTTPInfo, IndexID: index, RowID: 8, ObjectType: "httpinfo", ObjectID: 8, Details: details, ObservedAt: observed}); err != nil {
		t.Fatal(err)
	}
	if len(repo.observations) != 4 {
		t.Fatalf("expected four HTTP observations, got %d: %#v", len(repo.observations), repo.observations)
	}
	if repo.observations[0].Value.Boolean == nil || !*repo.observations[0].Value.Boolean {
		t.Fatalf("header presence is not boolean true: %#v", repo.observations[0].Value)
	}
	if repo.observations[1].Value.Text == nil || *repo.observations[1].Value.Text != "nginx" {
		t.Fatalf("header value was not selected: %#v", repo.observations[1].Value)
	}
	if repo.observations[2].Value.Numeric == nil || *repo.observations[2].Value.Numeric != 10 {
		t.Fatalf("remaining certificate days are incorrect: %#v", repo.observations[2].Value)
	}
	if repo.observations[3].Value.Timestamp == nil || repo.observations[3].EffectiveAt != nil {
		t.Fatalf("certificate expiry was reinterpreted as event time: %#v", repo.observations[3])
	}
}

func TestNetInfoArtifactScalarWildcardCollectionAndCount(t *testing.T) {
	observed := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	metrics := []cdb.TimeSeriesMetric{
		{ID: 111, Key: "country", SourceKind: cfg.TimeSeriesSourceNetInfo, ValueType: cfg.TimeSeriesValueString, Bucket: cfg.TimeSeriesBucketNone, TimeBasis: cfg.TimeSeriesTimeObservedAt, DedupeScope: cfg.TimeSeriesDedupeObject, Selector: json.RawMessage(`{"from":"details","path":"ips.country.0"}`), Enabled: true},
		{ID: 112, Key: "port_count", SourceKind: cfg.TimeSeriesSourceNetInfo, ValueType: cfg.TimeSeriesValueCount, Bucket: cfg.TimeSeriesBucketNone, TimeBasis: cfg.TimeSeriesTimeObservedAt, DedupeScope: cfg.TimeSeriesDedupeObject, Selector: json.RawMessage(`{"from":"details","path":"service_scout.hosts[*].ports[*]"}`), Enabled: true},
	}
	repo := &fakeRepository{metrics: metrics}
	emitter := Emitter{Repository: repo, ArtifactScopes: fakeArtifactScopes{}, Config: &cfg.TimeSeriesConfig{Enabled: true, Defaults: cfg.TimeSeriesMetricDefaults{FailurePolicy: cfg.TimeSeriesFailureLogSkip}, Privacy: cfg.TimeSeriesPrivacyConfig{StoreValueText: true, MaxValueLength: 2048}, Cardinality: cfg.TimeSeriesCardinalityConfig{MaxDimensions: 10, Overflow: cfg.TimeSeriesCardinalityDrop}}}
	details := map[string]interface{}{"ips": map[string]interface{}{"country": []interface{}{"US"}}, "service_scout": map[string]interface{}{"hosts": []interface{}{map[string]interface{}{"ports": []interface{}{map[string]interface{}{"port": 80}, map[string]interface{}{"port": 443}}}}}}
	if err := emitter.EmitIndexedArtifact(IndexedArtifactInput{SourceKind: cfg.TimeSeriesSourceNetInfo, IndexID: 4, RowID: 7, ObjectType: "netinfo", ObjectID: 7, Details: details, ObservedAt: observed}); err != nil {
		t.Fatal(err)
	}
	if len(repo.observations) != 2 || repo.observations[0].Value.Text == nil || *repo.observations[0].Value.Text != "US" || repo.observations[1].Value.Integer == nil || *repo.observations[1].Value.Integer != 2 {
		t.Fatalf("unexpected NetInfo selector observations: %#v", repo.observations)
	}
}

func TestArtifactMetricPrefersEquivalentNormalizedObjectAttribute(t *testing.T) {
	artifact := cdb.TimeSeriesMetric{ID: 121, Key: "artifact_server", SourceKind: cfg.TimeSeriesSourceHTTPInfo, ValueType: cfg.TimeSeriesValueString, Bucket: cfg.TimeSeriesBucketNone, TimeBasis: cfg.TimeSeriesTimeObservedAt, DedupeScope: cfg.TimeSeriesDedupeObject, Selector: json.RawMessage(`{"from":"details","path":"response_headers.Server","transform":"first"}`), Enabled: true}
	attribute := cdb.TimeSeriesMetric{ID: 122, Key: "normalized_server", SourceKind: cfg.TimeSeriesSourceObjectAttribute, ObjectType: cfg.TimeSeriesObjectHTTPInfo, ValueType: cfg.TimeSeriesValueString, Selector: json.RawMessage(`{"attribute_key":"server"}`), Enabled: true}
	repo := &fakeRepository{metrics: []cdb.TimeSeriesMetric{artifact, attribute}}
	emitter := Emitter{Repository: repo, ArtifactScopes: fakeArtifactScopes{}, Config: &cfg.TimeSeriesConfig{Enabled: true, Defaults: cfg.TimeSeriesMetricDefaults{FailurePolicy: cfg.TimeSeriesFailureLogSkip}, Cardinality: cfg.TimeSeriesCardinalityConfig{MaxDimensions: 10, Overflow: cfg.TimeSeriesCardinalityDrop}}}
	input := IndexedArtifactInput{SourceKind: cfg.TimeSeriesSourceHTTPInfo, IndexID: 1, RowID: 2, ObjectType: "httpinfo", ObjectID: 2, Details: map[string]interface{}{"response_headers": map[string]interface{}{"Server": []interface{}{"nginx"}}}, NormalizedAttributes: map[string]interface{}{"server": "nginx"}, AttributePaths: map[string]string{"server": "response_headers.Server"}}
	if err := emitter.EmitIndexedArtifact(input); err != nil {
		t.Fatal(err)
	}
	if len(repo.observations) != 0 {
		t.Fatalf("equivalent artifact metric double counted normalized attribute: %#v", repo.observations)
	}
}

func TestWebObjectHashChangeStatesAcrossPersistedRows(t *testing.T) {
	metric := cdb.TimeSeriesMetric{ID: 131, Key: "web_hash", SourceKind: cfg.TimeSeriesSourceWebObject, ValueType: cfg.TimeSeriesValueString, Bucket: cfg.TimeSeriesBucketNone, TimeBasis: cfg.TimeSeriesTimeObservedAt, DedupeScope: cfg.TimeSeriesDedupeNone, Selector: json.RawMessage(`{"from":"hash"}`), Enabled: true}
	repo := &fakeRepository{metrics: []cdb.TimeSeriesMetric{metric}}
	index := uint64(3)
	emitter := Emitter{Repository: repo, ArtifactScopes: fakeArtifactScopes{scopes: []cdb.TimeSeriesScope{{IndexID: &index}}}, Config: &cfg.TimeSeriesConfig{Enabled: true, Defaults: cfg.TimeSeriesMetricDefaults{FailurePolicy: cfg.TimeSeriesFailureLogSkip}, Privacy: cfg.TimeSeriesPrivacyConfig{StoreValueText: true, MaxValueLength: 2048}, Cardinality: cfg.TimeSeriesCardinalityConfig{MaxDimensions: 10, Overflow: cfg.TimeSeriesCardinalityDrop}}}
	at := time.Date(2026, 6, 6, 10, 0, 0, 0, time.UTC)
	inputs := []IndexedArtifactInput{
		{SourceKind: cfg.TimeSeriesSourceWebObject, IndexID: index, RowID: 10, ObjectType: "webobject", ObjectID: 10, Hash: "hash-a", ObservedAt: at},
		{SourceKind: cfg.TimeSeriesSourceWebObject, IndexID: index, RowID: 11, ObjectType: "webobject", ObjectID: 11, Hash: "hash-b", ObservedAt: at.Add(time.Hour)},
		{SourceKind: cfg.TimeSeriesSourceWebObject, IndexID: index, RowID: 12, ObjectType: "webobject", ObjectID: 12, Hash: "hash-b", ObservedAt: at.Add(2 * time.Hour)},
	}
	for _, input := range inputs {
		if err := emitter.EmitIndexedArtifact(input); err != nil {
			t.Fatal(err)
		}
	}
	if len(repo.observations) != 3 || repo.observations[0].ChangeType != "new" || repo.observations[1].ChangeType != "changed" || repo.observations[2].ChangeType != "unchanged" {
		t.Fatalf("unexpected web object hash change states: %#v", repo.observations)
	}
}

func TestScreenshotAndFileMetadataSelectors(t *testing.T) {
	metrics := []cdb.TimeSeriesMetric{
		{ID: 141, Key: "screenshot_bytes", SourceKind: cfg.TimeSeriesSourceScreenshot, ValueType: cfg.TimeSeriesValueInteger, Bucket: cfg.TimeSeriesBucketNone, TimeBasis: cfg.TimeSeriesTimeObservedAt, DedupeScope: cfg.TimeSeriesDedupeObject, Selector: json.RawMessage(`{"from":"details","path":"byte_size"}`), Enabled: true},
		{ID: 142, Key: "file_location_hash", SourceKind: cfg.TimeSeriesSourceFile, ValueType: cfg.TimeSeriesValueString, Bucket: cfg.TimeSeriesBucketNone, TimeBasis: cfg.TimeSeriesTimeObservedAt, DedupeScope: cfg.TimeSeriesDedupeObject, Selector: json.RawMessage(`{"from":"details","path":"location_hash"}`), Enabled: true},
	}
	repo := &fakeRepository{metrics: metrics}
	emitter := Emitter{Repository: repo, ArtifactScopes: fakeArtifactScopes{}, Config: &cfg.TimeSeriesConfig{Enabled: true, Defaults: cfg.TimeSeriesMetricDefaults{FailurePolicy: cfg.TimeSeriesFailureLogSkip}, Privacy: cfg.TimeSeriesPrivacyConfig{StoreValueText: true, MaxValueLength: 2048}, Cardinality: cfg.TimeSeriesCardinalityConfig{MaxDimensions: 10, Overflow: cfg.TimeSeriesCardinalityDrop}}}
	metadata := map[string]interface{}{"byte_size": 4096, "format": "png", "width": 1280, "height": 720, "location_hash": "stable-location"}
	for _, kind := range []cfg.TimeSeriesSourceKind{cfg.TimeSeriesSourceScreenshot, cfg.TimeSeriesSourceFile} {
		if err := emitter.EmitIndexedArtifact(IndexedArtifactInput{SourceKind: kind, IndexID: 1, RowID: 2, ObjectType: string(kind), ObjectID: 2, Details: metadata, Hash: "content-hash"}); err != nil {
			t.Fatal(err)
		}
	}
	if len(repo.observations) != 2 || repo.observations[0].Value.Integer == nil || *repo.observations[0].Value.Integer != 4096 || repo.observations[1].Value.Text == nil || *repo.observations[1].Value.Text != "stable-location" {
		t.Fatalf("unexpected screenshot/file metadata observations: %#v", repo.observations)
	}
}
