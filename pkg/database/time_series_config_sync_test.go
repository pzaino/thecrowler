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
