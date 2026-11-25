package main

import (
	"context"
	"errors"
	"sync"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

var (
	timeSeriesMaintenanceMtx    sync.Mutex
	timeSeriesMaintenanceCancel context.CancelFunc
)

func setTimeSeriesMaintenanceCancel(cancel context.CancelFunc) {
	timeSeriesMaintenanceMtx.Lock()
	timeSeriesMaintenanceCancel = cancel
	timeSeriesMaintenanceMtx.Unlock()
}

// stopTimeSeriesMaintenance asks the singleton maintenance schedulers to stop.
// Signal handling calls this before process exit; the database leases are
// session-scoped and are therefore released automatically if the process exits
// before the goroutines observe cancellation.
func stopTimeSeriesMaintenance() {
	timeSeriesMaintenanceMtx.Lock()
	cancel := timeSeriesMaintenanceCancel
	timeSeriesMaintenanceMtx.Unlock()
	if cancel != nil {
		cancel()
	}
}

func isTimeSeriesAggregationContention(err error) bool {
	return errors.Is(err, cdb.ErrTimeSeriesAggregationRunning)
}

func isTimeSeriesRetentionContention(err error) bool {
	return errors.Is(err, cdb.ErrTimeSeriesRetentionRunning) || errors.Is(err, cdb.ErrTimeSeriesRetentionAggregationBusy)
}

// runTimeSeriesAggregation is deliberately independent from HTTP event handling:
// aggregation failures are logged and never propagate into indexing/event work.
func runTimeSeriesAggregation(ctx context.Context, db *cdb.Handler, config cfg.TimeSeriesConfig, now time.Time) (cdb.TimeSeriesAggregationResult, error) {
	overlap, err := cfg.ParseTimeSeriesFixedDuration(config.Aggregation.Overlap)
	if err != nil {
		return cdb.TimeSeriesAggregationResult{}, err
	}
	var maxDuration time.Duration
	if config.Aggregation.MaxDuration != "" {
		maxDuration, err = cfg.ParseTimeSeriesFixedDuration(config.Aggregation.MaxDuration)
		if err != nil {
			return cdb.TimeSeriesAggregationResult{}, err
		}
	}
	return cdb.RunTimeSeriesAggregation(ctx, db, cdb.TimeSeriesAggregationOptions{
		Overlap: overlap, BatchSize: config.Aggregation.BatchSize, MaxBatches: config.Aggregation.MaxBatches, MaxDuration: maxDuration, Now: now,
	})
}

// timeSeriesRetentionOptionsFromConfig resolves the operator contract once so a
// scheduled run and an administrative run use the same calendar semantics.
func timeSeriesRetentionOptionsFromConfig(config cfg.TimeSeriesConfig, now time.Time) (cdb.TimeSeriesRetentionOptions, error) {
	rawWindow, err := cfg.ParseTimeSeriesRetentionWindow(config.Retention.Raw)
	if err != nil {
		return cdb.TimeSeriesRetentionOptions{}, err
	}
	aggregateWindow, err := cfg.ParseTimeSeriesRetentionWindow(config.Retention.Aggregated)
	if err != nil {
		return cdb.TimeSeriesRetentionOptions{}, err
	}
	maxDuration, err := cfg.ParseTimeSeriesFixedDuration(config.Retention.MaxDuration)
	if err != nil {
		return cdb.TimeSeriesRetentionOptions{}, err
	}
	var aggregationOverlap time.Duration
	if config.Aggregation.Overlap != "" {
		aggregationOverlap, err = cfg.ParseTimeSeriesFixedDuration(config.Aggregation.Overlap)
		if err != nil {
			return cdb.TimeSeriesRetentionOptions{}, err
		}
	}
	return cdb.TimeSeriesRetentionOptions{
		Now:                          now,
		RawWindow:                    rawWindow,
		AggregateWindow:              aggregateWindow,
		BatchSize:                    config.Retention.BatchSize,
		MaxBatches:                   config.Retention.MaxBatches,
		MaxDuration:                  maxDuration,
		DryRun:                       config.Retention.DryRun,
		RequireAggregationCheckpoint: config.Aggregation.Enabled,
		AggregationOverlap:           aggregationOverlap,
	}, nil
}

func runTimeSeriesRetention(ctx context.Context, db *cdb.Handler, config cfg.TimeSeriesConfig, now time.Time) (cdb.TimeSeriesRetentionResult, error) {
	options, err := timeSeriesRetentionOptionsFromConfig(config, now)
	if err != nil {
		return cdb.TimeSeriesRetentionResult{}, err
	}
	return cdb.PruneTimeSeriesRetention(ctx, db, options)
}

func startTimeSeriesAggregationScheduler(ctx context.Context, db *cdb.Handler, config cfg.TimeSeriesConfig) {
	if !config.Enabled || !config.Aggregation.Enabled {
		return
	}
	interval, err := cfg.ParseTimeSeriesFixedDuration(config.Aggregation.Schedule)
	if err != nil || interval <= 0 {
		cmn.DebugMsg(cmn.DbgLvlError, "Invalid time-series aggregation schedule %q: %v", config.Aggregation.Schedule, err)
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				_, runErr := runTimeSeriesAggregation(ctx, db, config, now.UTC())
				if runErr != nil && !isTimeSeriesAggregationContention(runErr) && !errors.Is(runErr, context.Canceled) && !errors.Is(runErr, context.DeadlineExceeded) {
					cmn.DebugMsg(cmn.DbgLvlError, "Time-series aggregation failed: %v", runErr)
				}
			}
		}
	}()
}

func startTimeSeriesRetentionScheduler(ctx context.Context, db *cdb.Handler, config cfg.TimeSeriesConfig) {
	if !config.Enabled || !config.Retention.Enabled {
		return
	}
	interval, err := cfg.ParseTimeSeriesFixedDuration(config.Retention.Schedule)
	if err != nil || interval <= 0 {
		cmn.DebugMsg(cmn.DbgLvlError, "Invalid time-series retention schedule %q: %v", config.Retention.Schedule, err)
		return
	}
	maxDuration, err := cfg.ParseTimeSeriesFixedDuration(config.Retention.MaxDuration)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Invalid time-series retention max_duration %q: %v", config.Retention.MaxDuration, err)
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				runCtx := ctx
				cancel := func() {}
				if maxDuration > 0 {
					// The batch loop enforces the soft budget itself; this hard
					// deadline bounds a stalled statement or connection.
					runCtx, cancel = context.WithTimeout(ctx, maxDuration+time.Minute)
				}
				result, runErr := runTimeSeriesRetention(runCtx, db, config, now.UTC())
				cancel()
				if runErr != nil {
					if isTimeSeriesRetentionContention(runErr) {
						cmn.DebugMsg(cmn.DbgLvlDebug, "Time-series retention skipped: another run owns the lease")
					} else if !errors.Is(runErr, context.Canceled) && !errors.Is(runErr, context.DeadlineExceeded) {
						cmn.DebugMsg(cmn.DbgLvlError, "Time-series retention failed: %v", runErr)
					}
					continue
				}
				cmn.DebugMsg(cmn.DbgLvlDebug, "Time-series retention completed: metrics=%d skipped=%d raw_deleted=%d aggregates_deleted=%d batches=%d dry_run=%t budget_exhausted=%t",
					result.MetricsProcessed, result.MetricsSkipped, result.RawDeleted, result.AggregatesDeleted, result.BatchesProcessed, config.Retention.DryRun, result.BudgetExhausted)
			}
		}
	}()
}
