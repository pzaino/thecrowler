\set ON_ERROR_STOP on
\pset pager off

-- Run after time_series_postgres_fixture.sql for the baseline, then after the
-- v1.14 migration for the candidate. These are the repository query shapes.
EXPLAIN (ANALYZE, BUFFERS)
SELECT observation_id FROM TimeSeriesObservations
WHERE deleted_at IS NULL AND metric_id = 42
  AND observed_at >= TIMESTAMPTZ '2025-06-01' AND observed_at < TIMESTAMPTZ '2025-07-01'
ORDER BY observed_at, observation_id LIMIT 101;

EXPLAIN (ANALYZE, BUFFERS)
SELECT observation_id FROM TimeSeriesObservations
WHERE deleted_at IS NULL AND metric_id = 42
  AND effective_at >= TIMESTAMPTZ '2025-06-01' AND effective_at < TIMESTAMPTZ '2025-07-01'
ORDER BY observed_at, observation_id LIMIT 101;

EXPLAIN (ANALYZE, BUFFERS)
SELECT observation_id FROM TimeSeriesObservations
WHERE deleted_at IS NULL AND metric_id = 42
  AND source_updated_at >= TIMESTAMPTZ '2025-06-01' AND source_updated_at < TIMESTAMPTZ '2025-07-01'
ORDER BY observed_at, observation_id LIMIT 101;

EXPLAIN (ANALYZE, BUFFERS)
SELECT aggregate_id FROM TimeSeriesAggregates
WHERE deleted_at IS NULL AND metric_id = 42
  AND bucket_start >= TIMESTAMPTZ '2025-06-01' AND bucket_start < TIMESTAMPTZ '2025-07-01'
ORDER BY bucket_start, aggregate_id LIMIT 101;

-- TS-PERF-000: save this digest before migration and compare it afterward.
SELECT md5(string_agg(format('%s|%s', observation_id, observed_at), ',' ORDER BY observed_at, observation_id)) AS "TS-PERF-000-observations"
FROM (SELECT observation_id, observed_at FROM TimeSeriesObservations
      WHERE deleted_at IS NULL AND metric_id = 42
        AND observed_at >= TIMESTAMPTZ '2025-06-01' AND observed_at < TIMESTAMPTZ '2025-07-01'
      ORDER BY observed_at, observation_id LIMIT 101) AS result;
SELECT md5(string_agg(format('%s|%s', aggregate_id, bucket_start), ',' ORDER BY bucket_start, aggregate_id)) AS "TS-PERF-000-aggregates"
FROM (SELECT aggregate_id, bucket_start FROM TimeSeriesAggregates
      WHERE deleted_at IS NULL AND metric_id = 42
        AND bucket_start >= TIMESTAMPTZ '2025-06-01' AND bucket_start < TIMESTAMPTZ '2025-07-01'
      ORDER BY bucket_start, aggregate_id LIMIT 101) AS result;
