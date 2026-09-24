package main

import (
	"context"
	"errors"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func isTimeSeriesAggregationContention(err error) bool {
	return errors.Is(err, cdb.ErrTimeSeriesAggregationRunning)
}

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
			_, runErr := runTimeSeriesAggregation(
				context.Background(),
				db,
				config,
				now.UTC(),
			)

			if runErr != nil && !isTimeSeriesAggregationContention(runErr) {
				cmn.DebugMsg(
					cmn.DbgLvlError,
					"Time-series aggregation failed: %v",
					runErr,
				)
			}
		}
	}()
}
