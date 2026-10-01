\set ON_ERROR_STOP on
DROP TABLE IF EXISTS TimeSeriesAggregates;
DROP TABLE IF EXISTS TimeSeriesObservations;

CREATE TABLE TimeSeriesObservations (
    observation_id BIGSERIAL PRIMARY KEY,
    metric_id BIGINT NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL,
    effective_at TIMESTAMPTZ,
    source_updated_at TIMESTAMPTZ,
    bucket_start TIMESTAMPTZ NOT NULL,
    deleted_at TIMESTAMPTZ,
    payload TEXT
);
CREATE TABLE TimeSeriesAggregates (
    aggregate_id BIGSERIAL PRIMARY KEY,
    metric_id BIGINT NOT NULL,
    bucket_start TIMESTAMPTZ NOT NULL,
    deleted_at TIMESTAMPTZ,
    payload TEXT
);

-- One million raw rows, 250,000 buckets, 100 metrics, one year, and 5% soft
-- deletion approximate a production analytical workload while remaining local.
INSERT INTO TimeSeriesObservations
    (metric_id, observed_at, effective_at, source_updated_at, bucket_start, deleted_at, payload)
SELECT 1 + (g % 100),
       TIMESTAMPTZ '2025-01-01' + (g % 525600) * INTERVAL '1 minute',
       TIMESTAMPTZ '2025-01-01' + ((g * 7) % 525600) * INTERVAL '1 minute',
       TIMESTAMPTZ '2025-01-01' + ((g * 13) % 525600) * INTERVAL '1 minute',
       date_trunc('hour', TIMESTAMPTZ '2025-01-01' + (g % 525600) * INTERVAL '1 minute'),
       CASE WHEN g % 20 = 0 THEN TIMESTAMPTZ '2026-01-01' END,
       repeat(md5(g::text), 2)
FROM generate_series(1, 1000000) AS g;

INSERT INTO TimeSeriesAggregates(metric_id, bucket_start, deleted_at, payload)
SELECT 1 + (g % 100), TIMESTAMPTZ '2025-01-01' + (g % 8760) * INTERVAL '1 hour',
       CASE WHEN g % 20 = 0 THEN TIMESTAMPTZ '2026-01-01' END,
       repeat(md5(g::text), 2)
FROM generate_series(1, 250000) AS g;

-- Existing production definitions relevant to these scans.
CREATE INDEX idx_timeseriesobservations_metric_bucket
    ON TimeSeriesObservations(metric_id, bucket_start);
CREATE INDEX idx_timeseriesaggregates_metric_bucket
    ON TimeSeriesAggregates(metric_id, bucket_start);
ANALYZE TimeSeriesObservations;
ANALYZE TimeSeriesAggregates;
