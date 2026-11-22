package database

import (
	"encoding/json"
	"os"
	"testing"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func TestSyncConfiguredTimeSeriesMetricsUpsertsYAMLDefinitions(t *testing.T) {
	db := openSQLiteMemoryDB(t)
	defer db.Close()

	schema, err := os.ReadFile("sqlite-setup.sqlite3")
	if err != nil {
		t.Fatalf("read sqlite schema: %v", err)
	}
	if _, err = db.Exec(string(schema)); err != nil {
		t.Fatalf("create sqlite schema: %v", err)
	}
	handler := Handler(&SQLiteHandler{db: db, dbms: DBSQLiteStr})

	configuration := cfg.TimeSeriesConfig{
		Enabled: true,
		Defaults: cfg.TimeSeriesMetricDefaults{
			ValueType:      cfg.TimeSeriesValueInteger,
			Aggregates:     []cfg.TimeSeriesAggregate{cfg.TimeSeriesAggregateCount},
			BucketInterval: cfg.TimeSeriesBucketOneHour,
			TimeBasis:      cfg.TimeSeriesTimeObservedAt,
			DedupeScope:    cfg.TimeSeriesDedupeObject,
			FailurePolicy:  cfg.TimeSeriesFailureLogSkip,
		},
		Privacy: cfg.TimeSeriesPrivacyConfig{StoreValueText: true},
		Metrics: []cfg.TimeSeriesMetricConfig{{
			Key:            "ig.creator.profile_followers",
			Enabled:        true,
			Description:    "followers",
			SourceKind:     cfg.TimeSeriesSourceWebObject,
			Selector:       map[string]interface{}{"from": "details", "path": "scraped_data.creator_followers"},
			ValueType:      cfg.TimeSeriesValueInteger,
			Unit:           "followers",
			Aggregates:     []cfg.TimeSeriesAggregate{cfg.TimeSeriesAggregateAverage, cfg.TimeSeriesAggregateMinimum, cfg.TimeSeriesAggregateMaximum},
			BucketInterval: cfg.TimeSeriesBucketOneDay,
			Dimensions: []cfg.TimeSeriesDimensionConfig{{
				Key:      "username",
				Selector: map[string]interface{}{"from": "details", "path": "crowler_meta.meta_data.username"},
			}},
		}},
	}

	if err = SyncConfiguredTimeSeriesMetrics(&handler, configuration); err != nil {
		t.Fatalf("sync configured metrics: %v", err)
	}

	metric, err := GetTimeSeriesMetricByKey(&handler, "ig.creator.profile_followers")
	if err != nil {
		t.Fatalf("get synced metric: %v", err)
	}
	if metric.SourceKind != cfg.TimeSeriesSourceWebObject || !metric.Enabled || metric.Bucket != cfg.TimeSeriesBucketOneDay || metric.Aggregate != cfg.TimeSeriesAggregateAverage {
		t.Fatalf("unexpected synced metric: %#v", metric)
	}
	var selector map[string]interface{}
	if err = json.Unmarshal(metric.Selector, &selector); err != nil {
		t.Fatalf("decode selector: %v", err)
	}
	if selector["path"] != "scraped_data.creator_followers" {
		t.Fatalf("selector path = %v", selector["path"])
	}
	var dimensions []cfg.TimeSeriesDimensionConfig
	if err = json.Unmarshal(metric.Dimensions, &dimensions); err != nil {
		t.Fatalf("decode dimensions: %v", err)
	}
	if len(dimensions) != 1 || dimensions[0].Key != "username" {
		t.Fatalf("unexpected dimensions: %#v", dimensions)
	}
}

func TestSyncConfiguredTimeSeriesMetricsPersistsEffectiveRetentionAndCardinality(t *testing.T) {
	db := openSQLiteMemoryDB(t)
	defer db.Close()
	schema, err := os.ReadFile("sqlite-setup.sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	handler := Handler(&SQLiteHandler{db: db, dbms: DBSQLiteStr})
	configuration := cfg.TimeSeriesConfig{
		Enabled:   true,
		Retention: cfg.TimeSeriesRetentionConfig{Raw: "45d", Aggregated: "13mo", Schedule: "2h", BatchSize: 50, MaxBatches: 4, MaxDuration: "3m"},
		Cardinality: cfg.TimeSeriesCardinalityConfig{
			MaxSeriesPerMetric:    7,
			MaxDimensions:         4,
			MaxValuesPerDimension: 9,
			Overflow:              cfg.TimeSeriesCardinalityDrop,
		},
		Metrics: []cfg.TimeSeriesMetricConfig{
			{
				Key:        "inherited",
				Enabled:    true,
				SourceKind: cfg.TimeSeriesSourceCustom,
				Selector:   map[string]interface{}{"field": "value"},
			},
			{
				Key:        "overridden",
				Enabled:    true,
				SourceKind: cfg.TimeSeriesSourceCustom,
				Selector:   map[string]interface{}{"field": "value"},
				Retention:  &cfg.TimeSeriesRetentionOverride{Raw: "0", Aggregated: "2mo"},
				Cardinality: &cfg.TimeSeriesCardinalityConfig{
					MaxSeriesPerMetric: 3,
				},
			},
		},
	}
	if err = SyncConfiguredTimeSeriesMetrics(&handler, configuration); err != nil {
		t.Fatal(err)
	}
	inherited, err := GetTimeSeriesMetricByKey(&handler, "inherited")
	if err != nil {
		t.Fatal(err)
	}
	var retention timeSeriesStoredRetentionPolicy
	if err = json.Unmarshal(inherited.RetentionPolicy, &retention); err != nil {
		t.Fatalf("decode inherited retention: %v", err)
	}
	if retention.Raw != "45d" || retention.Aggregated != "13mo" {
		t.Fatalf("inherited retention = %#v", retention)
	}
	var cardinality cfg.TimeSeriesCardinalityConfig
	if err = json.Unmarshal(inherited.CardinalityPolicy, &cardinality); err != nil {
		t.Fatalf("decode inherited cardinality: %v", err)
	}
	if cardinality.MaxSeriesPerMetric != 7 || cardinality.MaxDimensions != 4 || cardinality.MaxValuesPerDimension != 9 {
		t.Fatalf("inherited cardinality = %#v", cardinality)
	}

	overridden, err := GetTimeSeriesMetricByKey(&handler, "overridden")
	if err != nil {
		t.Fatal(err)
	}
	retention = timeSeriesStoredRetentionPolicy{}
	if err = json.Unmarshal(overridden.RetentionPolicy, &retention); err != nil {
		t.Fatalf("decode overridden retention: %v", err)
	}
	if retention.Raw != "0" || retention.Aggregated != "2mo" {
		t.Fatalf("overridden retention = %#v", retention)
	}
	cardinality = cfg.TimeSeriesCardinalityConfig{}
	if err = json.Unmarshal(overridden.CardinalityPolicy, &cardinality); err != nil {
		t.Fatalf("decode overridden cardinality: %v", err)
	}
	if cardinality.MaxSeriesPerMetric != 3 || cardinality.MaxDimensions != 4 || cardinality.MaxValuesPerDimension != 9 {
		t.Fatalf("overridden cardinality = %#v", cardinality)
	}
}

func TestSyncConfiguredTimeSeriesMetricsTreatsExplicitZeroDimensionsAsDefault(t *testing.T) {
	db := openSQLiteMemoryDB(t)
	defer db.Close()
	schema, err := os.ReadFile("sqlite-setup.sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	handler := Handler(&SQLiteHandler{db: db, dbms: DBSQLiteStr})
	configuration := cfg.TimeSeriesConfig{
		Enabled:     true,
		Cardinality: cfg.TimeSeriesCardinalityConfig{MaxDimensions: 0},
		Metrics: []cfg.TimeSeriesMetricConfig{{
			Key:        "zero-dimensions",
			Enabled:    true,
			SourceKind: cfg.TimeSeriesSourceCustom,
			Selector:   map[string]interface{}{"field": "value"},
		}},
	}
	if err = SyncConfiguredTimeSeriesMetrics(&handler, configuration); err != nil {
		t.Fatal(err)
	}
	metric, err := GetTimeSeriesMetricByKey(&handler, "zero-dimensions")
	if err != nil {
		t.Fatal(err)
	}
	var cardinality cfg.TimeSeriesCardinalityConfig
	if err = json.Unmarshal(metric.CardinalityPolicy, &cardinality); err != nil {
		t.Fatal(err)
	}
	if cardinality.MaxDimensions != 10 {
		t.Fatalf("max_dimensions = %d, want the built-in default 10 (0 means inherit, not unlimited)", cardinality.MaxDimensions)
	}
}
