package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func TestEventsTimeSeriesAggregationRejectsInvalidOverlap(t *testing.T) {
	configuration := cfg.TimeSeriesConfig{Aggregation: cfg.TimeSeriesAggregationConfig{Overlap: "not-a-duration", BatchSize: 1, MaxBatches: 1}}
	if _, err := runTimeSeriesAggregation(context.Background(), nil, configuration, time.Now()); err == nil {
		t.Fatal("expected invalid overlap to fail before database access")
	}
}

func TestEventsTimeSeriesAggregationContentionIsExpected(t *testing.T) {
	for _, err := range []error{
		cdb.ErrTimeSeriesAggregationRunning,
		fmt.Errorf("release lease: %w", cdb.ErrTimeSeriesAggregationRunning),
		errors.Join(cdb.ErrTimeSeriesAggregationRunning, errors.New("close lease connection")),
	} {
		if !isTimeSeriesAggregationContention(err) {
			t.Fatalf("error %v should be treated as expected contention", err)
		}
	}

	if isTimeSeriesAggregationContention(errors.New("aggregation failed")) {
		t.Fatal("ordinary aggregation failure was treated as contention")
	}
}

func TestEventsTimeSeriesRetentionOptionsResolveCalendarWindows(t *testing.T) {
	now := time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC)
	configuration := cfg.TimeSeriesConfig{
		Enabled:     true,
		Retention:   cfg.TimeSeriesRetentionConfig{Enabled: true, Raw: "1mo", Aggregated: "0", Schedule: "2h", BatchSize: 25, MaxBatches: 3, MaxDuration: "2m", DryRun: true},
		Aggregation: cfg.TimeSeriesAggregationConfig{Enabled: true, Overlap: "15m"},
	}
	options, err := timeSeriesRetentionOptionsFromConfig(configuration, now)
	if err != nil {
		t.Fatal(err)
	}
	if !options.RequireAggregationCheckpoint {
		t.Fatal("scheduled retention must protect the aggregation checkpoint")
	}
	if options.AggregationOverlap != 15*time.Minute {
		t.Fatalf("overlap = %s", options.AggregationOverlap)
	}
	if !options.RawWindow.Specified || options.RawWindow.Forever || options.RawWindow.Months != 1 {
		t.Fatalf("raw window = %#v", options.RawWindow)
	}
	if !options.AggregateWindow.Specified || !options.AggregateWindow.Forever {
		t.Fatalf("aggregate window = %#v", options.AggregateWindow)
	}
	if !options.DryRun || options.BatchSize != 25 || options.MaxBatches != 3 || options.MaxDuration != 2*time.Minute {
		t.Fatalf("scheduled options = %#v", options)
	}
	cutoff, active := options.RawWindow.Cutoff(now)
	if !active || !cutoff.Equal(time.Date(2026, 2, 28, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("calendar cutoff = %s", cutoff)
	}
}

func TestEventsTimeSeriesRetentionRejectsInvalidRetentionValues(t *testing.T) {
	for name, configuration := range map[string]cfg.TimeSeriesConfig{
		"invalid raw":          {Enabled: true, Retention: cfg.TimeSeriesRetentionConfig{Enabled: true, Raw: "1mo2d", Aggregated: "30d", Schedule: "1h", MaxDuration: "1m"}},
		"invalid max duration": {Enabled: true, Retention: cfg.TimeSeriesRetentionConfig{Enabled: true, Raw: "30d", Aggregated: "30d", Schedule: "1h", MaxDuration: "monthly"}},
	} {
		if _, err := timeSeriesRetentionOptionsFromConfig(configuration, time.Now()); err == nil {
			t.Fatalf("%s retention configuration was accepted", name)
		}
	}
	if _, err := cfg.ParseTimeSeriesFixedDuration("not-a-duration"); err == nil {
		t.Fatal("invalid schedule duration was accepted")
	}
}

func TestEventsTimeSeriesRetentionContentionIsExpected(t *testing.T) {
	for _, err := range []error{
		cdb.ErrTimeSeriesRetentionRunning,
		cdb.ErrTimeSeriesRetentionAggregationBusy,
		fmt.Errorf("release lease: %w", cdb.ErrTimeSeriesRetentionRunning),
		errors.Join(cdb.ErrTimeSeriesRetentionRunning, errors.New("close lease connection")),
	} {
		if !isTimeSeriesRetentionContention(err) {
			t.Fatalf("error %v should be treated as expected retention contention", err)
		}
	}
	if isTimeSeriesRetentionContention(errors.New("retention failed")) {
		t.Fatal("ordinary retention failure was treated as contention")
	}
}
