-- Keep this migration transaction-safe for the normal migration runner.
-- Large production tables must use the CONCURRENTLY statements documented in
-- doc/timeseries-postgresql-index-rollout.md instead of running this file.
CREATE INDEX IF NOT EXISTS idx_timeseriesobservations_active_metric_observed
    ON TimeSeriesObservations(metric_id, observed_at, observation_id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_timeseriesobservations_active_metric_effective
    ON TimeSeriesObservations(metric_id, effective_at, observation_id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_timeseriesobservations_active_metric_source_updated
    ON TimeSeriesObservations(metric_id, source_updated_at, observation_id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_timeseriesaggregates_active_metric_bucket
    ON TimeSeriesAggregates(metric_id, bucket_start, aggregate_id)
    WHERE deleted_at IS NULL;

-- Drop the covered prefix only after its replacement has been built.
DROP INDEX IF EXISTS idx_timeseriesaggregates_metric_bucket;
