package main

import (
	"context"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func TestEventsTimeSeriesAggregationRejectsInvalidOverlap(t *testing.T) {
	configuration := cfg.TimeSeriesConfig{Aggregation: cfg.TimeSeriesAggregationConfig{Overlap: "not-a-duration", BatchSize: 1, MaxBatches: 1}}
	if _, err := runTimeSeriesAggregation(context.Background(), nil, configuration, time.Now()); err == nil {
		t.Fatal("expected invalid overlap to fail before database access")
	}
}
