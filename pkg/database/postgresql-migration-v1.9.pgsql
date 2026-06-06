--------------------------------------------------------------------------------
-- Foundational time-series schema (v1.9).
-- Metric definitions are configuration only; observations are append-only facts.
CREATE TABLE IF NOT EXISTS TimeSeriesMetrics (
    metric_id BIGSERIAL PRIMARY KEY,
    metric_key VARCHAR(255) NOT NULL UNIQUE,
    display_name VARCHAR(255) NOT NULL,
    description TEXT,
    source_kind VARCHAR(64) NOT NULL,
    value_type VARCHAR(32) NOT NULL,
    aggregate VARCHAR(32) NOT NULL,
    bucket VARCHAR(32) NOT NULL,
    time_basis VARCHAR(32) NOT NULL,
    dedupe_scope VARCHAR(64) NOT NULL,
    object_type VARCHAR(64) NOT NULL,
    failure_policy VARCHAR(64) NOT NULL,
    selector JSONB NOT NULL,
    dimensions JSONB,
    retention_policy JSONB,
    cardinality_policy JSONB,
    unit VARCHAR(64),
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    store_value_text BOOLEAN NOT NULL DEFAULT FALSE,
    hash_only BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ,
    last_updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- TimeSeriesObservations is append-only history. Optional references are nulled
-- when operational records are removed so historical measurements survive.
CREATE TABLE IF NOT EXISTS TimeSeriesObservations (
    observation_id BIGSERIAL PRIMARY KEY,
    metric_id BIGINT NOT NULL REFERENCES TimeSeriesMetrics(metric_id) ON DELETE RESTRICT,
    observed_at TIMESTAMPTZ NOT NULL,
    effective_at TIMESTAMPTZ,
    collected_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    source_updated_at TIMESTAMPTZ,
    bucket_start TIMESTAMPTZ NOT NULL,
    bucket_end TIMESTAMPTZ NOT NULL,
    information_seed_id BIGINT REFERENCES InformationSeed(information_seed_id) ON DELETE SET NULL,
    information_seed_candidate_id BIGINT REFERENCES InformationSeedCandidate(information_seed_candidate_id) ON DELETE SET NULL,
    source_id BIGINT REFERENCES Sources(source_id) ON DELETE SET NULL,
    source_information_seed_id BIGINT REFERENCES SourceInformationSeedIndex(source_information_seed_id) ON DELETE SET NULL,
    index_id BIGINT REFERENCES SearchIndex(index_id) ON DELETE SET NULL,
    entity_id BIGINT REFERENCES Entities(entity_id) ON DELETE SET NULL,
    subject_type VARCHAR(64),
    subject_id BIGINT,
    object_type VARCHAR(64),
    object_id BIGINT,
    correlation_rule_id BIGINT REFERENCES CorrelationRules(rule_id) ON DELETE SET NULL,
    correlation_object_type_1 VARCHAR(64),
    correlation_object_id_1 BIGINT,
    correlation_object_type_2 VARCHAR(64),
    correlation_object_id_2 BIGINT,
    value_numeric NUMERIC,
    value_integer BIGINT,
    value_boolean BOOLEAN,
    value_text TEXT,
    value_json JSONB,
    value_timestamp TIMESTAMPTZ,
    value_hash VARCHAR(64) NOT NULL,
    previous_observation_id BIGINT REFERENCES TimeSeriesObservations(observation_id) ON DELETE SET NULL,
    previous_value_hash VARCHAR(64),
    is_changed BOOLEAN NOT NULL DEFAULT FALSE,
    change_type VARCHAR(32),
    change_delta_numeric NUMERIC,
    change_detected_at TIMESTAMPTZ,
    dedupe_key VARCHAR(64) NOT NULL UNIQUE,
    dimensions JSONB,
    provenance JSONB,
    provenance_hash VARCHAR(64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ,
    last_updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS TimeSeriesAggregates (
    aggregate_id BIGSERIAL PRIMARY KEY,
    metric_id BIGINT NOT NULL REFERENCES TimeSeriesMetrics(metric_id) ON DELETE RESTRICT,
    bucket_start TIMESTAMPTZ NOT NULL,
    bucket_end TIMESTAMPTZ NOT NULL,
    information_seed_id BIGINT REFERENCES InformationSeed(information_seed_id) ON DELETE SET NULL,
    information_seed_candidate_id BIGINT REFERENCES InformationSeedCandidate(information_seed_candidate_id) ON DELETE SET NULL,
    source_id BIGINT REFERENCES Sources(source_id) ON DELETE SET NULL,
    source_information_seed_id BIGINT REFERENCES SourceInformationSeedIndex(source_information_seed_id) ON DELETE SET NULL,
    index_id BIGINT REFERENCES SearchIndex(index_id) ON DELETE SET NULL,
    entity_id BIGINT REFERENCES Entities(entity_id) ON DELETE SET NULL,
    subject_type VARCHAR(64),
    subject_id BIGINT,
    object_type VARCHAR(64),
    object_id BIGINT,
    correlation_rule_id BIGINT REFERENCES CorrelationRules(rule_id) ON DELETE SET NULL,
    correlation_object_type_1 VARCHAR(64),
    correlation_object_id_1 BIGINT,
    correlation_object_type_2 VARCHAR(64),
    correlation_object_id_2 BIGINT,
    dimensions JSONB,
    value_count BIGINT NOT NULL DEFAULT 0,
    numeric_count BIGINT NOT NULL DEFAULT 0,
    numeric_sum NUMERIC,
    numeric_min NUMERIC,
    numeric_max NUMERIC,
    numeric_avg NUMERIC,
    percentile_50 NUMERIC,
    percentile_75 NUMERIC,
    percentile_90 NUMERIC,
    percentile_95 NUMERIC,
    percentile_99 NUMERIC,
    first_observation_id BIGINT REFERENCES TimeSeriesObservations(observation_id) ON DELETE SET NULL,
    first_observed_at TIMESTAMPTZ,
    first_value_numeric NUMERIC,
    first_value_text TEXT,
    first_value_hash VARCHAR(64),
    last_observation_id BIGINT REFERENCES TimeSeriesObservations(observation_id) ON DELETE SET NULL,
    last_observed_at TIMESTAMPTZ,
    last_value_numeric NUMERIC,
    last_value_text TEXT,
    last_value_hash VARCHAR(64),
    change_count BIGINT NOT NULL DEFAULT 0,
    aggregate_hash VARCHAR(64) NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ,
    last_updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_timeseriesmetrics_enabled ON TimeSeriesMetrics(enabled, deleted_at);
CREATE INDEX IF NOT EXISTS idx_timeseriesmetrics_dimensions_gin ON TimeSeriesMetrics USING GIN(dimensions);
CREATE INDEX IF NOT EXISTS idx_timeseriesobservations_metric_bucket ON TimeSeriesObservations(metric_id, bucket_start);
CREATE INDEX IF NOT EXISTS idx_timeseriesobservations_seed ON TimeSeriesObservations(information_seed_id, observed_at);
CREATE INDEX IF NOT EXISTS idx_timeseriesobservations_seed_candidate ON TimeSeriesObservations(information_seed_candidate_id, observed_at);
CREATE INDEX IF NOT EXISTS idx_timeseriesobservations_source ON TimeSeriesObservations(source_id, observed_at);
CREATE INDEX IF NOT EXISTS idx_timeseriesobservations_index ON TimeSeriesObservations(index_id, observed_at);
CREATE INDEX IF NOT EXISTS idx_timeseriesobservations_entity ON TimeSeriesObservations(entity_id, observed_at);
CREATE INDEX IF NOT EXISTS idx_timeseriesobservations_subject ON TimeSeriesObservations(subject_type, subject_id, observed_at);
CREATE INDEX IF NOT EXISTS idx_timeseriesobservations_object ON TimeSeriesObservations(object_type, object_id, observed_at);
CREATE INDEX IF NOT EXISTS idx_timeseriesobservations_correlation_rule ON TimeSeriesObservations(correlation_rule_id, observed_at);
CREATE INDEX IF NOT EXISTS idx_timeseriesobservations_dedupe_key ON TimeSeriesObservations(dedupe_key);
CREATE INDEX IF NOT EXISTS idx_timeseriesobservations_dimensions_gin ON TimeSeriesObservations USING GIN(dimensions);
CREATE INDEX IF NOT EXISTS idx_timeseriesaggregates_metric_bucket ON TimeSeriesAggregates(metric_id, bucket_start);
CREATE INDEX IF NOT EXISTS idx_timeseriesaggregates_seed ON TimeSeriesAggregates(information_seed_id, bucket_start);
CREATE INDEX IF NOT EXISTS idx_timeseriesaggregates_seed_candidate ON TimeSeriesAggregates(information_seed_candidate_id, bucket_start);
CREATE INDEX IF NOT EXISTS idx_timeseriesaggregates_source ON TimeSeriesAggregates(source_id, bucket_start);
CREATE INDEX IF NOT EXISTS idx_timeseriesaggregates_index ON TimeSeriesAggregates(index_id, bucket_start);
CREATE INDEX IF NOT EXISTS idx_timeseriesaggregates_entity ON TimeSeriesAggregates(entity_id, bucket_start);
CREATE INDEX IF NOT EXISTS idx_timeseriesaggregates_subject ON TimeSeriesAggregates(subject_type, subject_id, bucket_start);
CREATE INDEX IF NOT EXISTS idx_timeseriesaggregates_object ON TimeSeriesAggregates(object_type, object_id, bucket_start);
CREATE INDEX IF NOT EXISTS idx_timeseriesaggregates_correlation_rule ON TimeSeriesAggregates(correlation_rule_id, bucket_start);
CREATE INDEX IF NOT EXISTS idx_timeseriesaggregates_aggregate_hash ON TimeSeriesAggregates(aggregate_hash);
CREATE INDEX IF NOT EXISTS idx_timeseriesaggregates_dimensions_gin ON TimeSeriesAggregates USING GIN(dimensions);
--------------------------------------------------------------------------------
