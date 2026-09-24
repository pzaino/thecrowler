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
