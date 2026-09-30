-- series_hash is derived acceleration data. The metric, scope, and dimensions
-- columns remain authoritative. Add it nullable first so old writers continue
-- to work while this release is rolled out.
CREATE EXTENSION IF NOT EXISTS pgcrypto;
ALTER TABLE TimeSeriesObservations ADD COLUMN IF NOT EXISTS series_hash VARCHAR(64);

-- These helpers reproduce CanonicalTimeSeriesJSON and timeSeriesSHA256. jsonb
-- object keys are explicitly sorted rather than relying on physical ordering.
CREATE OR REPLACE FUNCTION crowler_ts_canonical_json(v JSONB) RETURNS TEXT
LANGUAGE SQL IMMUTABLE STRICT PARALLEL SAFE AS $$
SELECT CASE jsonb_typeof(v)
  WHEN 'object' THEN '{' || COALESCE((SELECT string_agg(to_jsonb(key)::text || ':' || crowler_ts_canonical_json(value), ',' ORDER BY key COLLATE "C") FROM jsonb_each(v)), '') || '}'
  WHEN 'array' THEN '[' || COALESCE((SELECT string_agg(crowler_ts_canonical_json(value), ',' ORDER BY ordinality) FROM jsonb_array_elements(v) WITH ORDINALITY), '') || ']'
  ELSE replace(replace(replace(v::text, '&', '\u0026'), '<', '\u003c'), '>', '\u003e')
END
$$;
CREATE OR REPLACE FUNCTION crowler_ts_sha256(parts TEXT[]) RETURNS TEXT
LANGUAGE SQL IMMUTABLE STRICT PARALLEL SAFE AS $$
SELECT encode(digest(convert_to(COALESCE((SELECT string_agg(octet_length(part)::text || ':' || part || '|', '' ORDER BY ordinality) FROM unnest(parts) WITH ORDINALITY AS p(part, ordinality)), ''), 'UTF8'), 'sha256'), 'hex')
$$;
CREATE OR REPLACE FUNCTION crowler_ts_optional_id(v BIGINT) RETURNS TEXT
LANGUAGE SQL IMMUTABLE PARALLEL SAFE AS $$ SELECT CASE WHEN v IS NULL THEN 'absent' ELSE 'present:' || v::text END $$;
CREATE OR REPLACE FUNCTION crowler_ts_optional_text(v TEXT) RETURNS TEXT
LANGUAGE SQL IMMUTABLE PARALLEL SAFE AS $$ SELECT CASE WHEN v IS NULL OR v = '' THEN 'absent' ELSE 'present:' || v END $$;

-- Deterministic, restartable backfill. The dimensions hash preserves the
-- absent-vs-present distinction used by the application implementation.
UPDATE TimeSeriesObservations o SET series_hash = crowler_ts_sha256(ARRAY[
  'series', 'metric=' || metric_id::text,
  'seed=' || crowler_ts_optional_id(information_seed_id),
  'candidate=' || crowler_ts_optional_id(information_seed_candidate_id),
  'source=' || crowler_ts_optional_id(source_id),
  'source_seed=' || crowler_ts_optional_id(source_information_seed_id),
  'index=' || crowler_ts_optional_id(index_id),
  'entity=' || crowler_ts_optional_id(entity_id),
  'subject_type=' || crowler_ts_optional_text(subject_type),
  'subject_id=' || crowler_ts_optional_id(subject_id),
  'object_type=' || crowler_ts_optional_text(object_type),
  'object_id=' || crowler_ts_optional_id(object_id),
  'rule=' || crowler_ts_optional_id(correlation_rule_id),
  'correlation_type_1=' || crowler_ts_optional_text(correlation_object_type_1),
  'correlation_id_1=' || crowler_ts_optional_id(correlation_object_id_1),
  'correlation_type_2=' || crowler_ts_optional_text(correlation_object_type_2),
  'correlation_id_2=' || crowler_ts_optional_id(correlation_object_id_2),
  'dimension_hash=' || crowler_ts_sha256(ARRAY['dimensions', CASE WHEN dimensions IS NULL THEN 'absent' ELSE 'present:' || crowler_ts_canonical_json(dimensions) END])
]) WHERE series_hash IS NULL;

-- Validate without taking the stronger lock up front, then make the validated
-- invariant the column contract. The application also computes every new row.
ALTER TABLE TimeSeriesObservations
  ADD CONSTRAINT timeseriesobservations_series_hash_present
  CHECK (series_hash IS NOT NULL) NOT VALID;
ALTER TABLE TimeSeriesObservations VALIDATE CONSTRAINT timeseriesobservations_series_hash_present;
ALTER TABLE TimeSeriesObservations ALTER COLUMN series_hash SET NOT NULL;
ALTER TABLE TimeSeriesObservations DROP CONSTRAINT timeseriesobservations_series_hash_present;

-- This exact index is supported by previous-series history lookups.
CREATE INDEX IF NOT EXISTS idx_timeseriesobservations_series_history
  ON TimeSeriesObservations(series_hash, observed_at, observation_id);

DROP FUNCTION crowler_ts_optional_text(TEXT);
DROP FUNCTION crowler_ts_optional_id(BIGINT);
DROP FUNCTION crowler_ts_sha256(TEXT[]);
DROP FUNCTION crowler_ts_canonical_json(JSONB);

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
