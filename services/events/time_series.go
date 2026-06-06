package main

import (
	"context"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// runTimeSeriesAggregation is deliberately independent from HTTP event handling:
// aggregation failures are logged and never propagate into indexing/event work.
func runTimeSeriesAggregation(ctx context.Context, db *cdb.Handler, config cfg.TimeSeriesConfig, now time.Time) (cdb.TimeSeriesAggregationResult, error) {
	overlap, err := time.ParseDuration(config.Aggregation.Overlap)
	if err != nil {
		return cdb.TimeSeriesAggregationResult{}, err
	}
	return cdb.RunTimeSeriesAggregation(ctx, db, cdb.TimeSeriesAggregationOptions{
		Overlap: overlap, BatchSize: config.Aggregation.BatchSize, MaxBatches: config.Aggregation.MaxBatches, Now: now,
	})
}

func startTimeSeriesAggregationScheduler(db *cdb.Handler, config cfg.TimeSeriesConfig) {
	if !config.Enabled || !config.Aggregation.Enabled {
		return
	}
	interval, err := time.ParseDuration(config.Aggregation.Schedule)
	if err != nil || interval <= 0 {
		cmn.DebugMsg(cmn.DbgLvlError, "Invalid time-series aggregation schedule %q: %v", config.Aggregation.Schedule, err)
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for now := range ticker.C {
			ctx, cancel := context.WithTimeout(context.Background(), interval)
			_, runErr := runTimeSeriesAggregation(ctx, db, config, now.UTC())
			cancel()
			if runErr != nil && runErr != cdb.ErrTimeSeriesAggregationRunning {
				cmn.DebugMsg(cmn.DbgLvlError, "Time-series aggregation failed: %v", runErr)
			}
		}
	}()
}
