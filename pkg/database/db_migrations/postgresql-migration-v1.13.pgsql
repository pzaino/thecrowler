ALTER TABLE Sources
ADD COLUMN IF NOT EXISTS sub_priority INTEGER DEFAULT 0 NOT NULL;

CREATE INDEX IF NOT EXISTS idx_sources_priority_sub_priority
ON Sources(priority, sub_priority DESC, source_id ASC);

-- A Source owns its time-series history. Other provenance references retain
-- their non-destructive deletion rules.
ALTER TABLE TimeSeriesObservations
    DROP CONSTRAINT IF EXISTS timeseriesobservations_source_id_fkey,
    ADD CONSTRAINT timeseriesobservations_source_id_fkey
        FOREIGN KEY (source_id) REFERENCES Sources(source_id) ON DELETE CASCADE;

ALTER TABLE TimeSeriesAggregates
    DROP CONSTRAINT IF EXISTS timeseriesaggregates_source_id_fkey,
    ADD CONSTRAINT timeseriesaggregates_source_id_fkey
        FOREIGN KEY (source_id) REFERENCES Sources(source_id) ON DELETE CASCADE;

DROP FUNCTION IF EXISTS update_sources(
    INTEGER,
    VARCHAR,
    VARCHAR,
    VARCHAR,
    VARCHAR,
    VARCHAR,
    VARCHAR
);

CREATE OR REPLACE FUNCTION update_sources(
    limit_val INTEGER,
    p_priority VARCHAR,
    p_engineID VARCHAR,
    p_last_ok_update VARCHAR,
    p_last_error VARCHAR,
    p_regular_crawling VARCHAR,
    p_processing_timeout VARCHAR
)
RETURNS TABLE(source_id BIGINT, source_uid TEXT, url TEXT, restricted INT, flags INT, config JSONB, last_updated_at TIMESTAMPTZ, sub_priority INT) AS
$$
DECLARE
    priority_list TEXT[];
    use_priority_filter BOOLEAN := FALSE;
BEGIN
    -- Handle nulls and defaults
    p_priority := COALESCE(TRIM(p_priority), '');
    p_last_ok_update := COALESCE(TRIM(p_last_ok_update));
    p_regular_crawling := COALESCE(TRIM(p_regular_crawling));
    p_last_error := COALESCE(TRIM(p_last_error));
    p_processing_timeout := COALESCE(TRIM(p_processing_timeout));

    IF p_last_error = '' THEN
        p_last_error := '15 minutes';
    END IF;
    IF p_processing_timeout = '' THEN
        p_processing_timeout := '1 day';
    END IF;

    -- Parse priority list
    IF p_priority <> '' THEN
        priority_list := ARRAY(
            SELECT TRIM(LOWER(value))
            FROM unnest(string_to_array(p_priority, ',')) AS value
        );
        use_priority_filter := TRUE;
    END IF;

    RETURN QUERY
    WITH SelectedSources AS (
        SELECT s.source_id
        FROM Sources AS s
        WHERE s.disabled = FALSE
          AND (
                -- Priority clause only if priorities provided
                (NOT use_priority_filter OR LOWER(TRIM(s.priority)) = ANY(priority_list))
                AND (
                    -- last_ok_update filter
                    (p_last_ok_update <> '' AND (s.last_updated_at IS NULL OR s.last_updated_at < NOW() - p_last_ok_update::INTERVAL))
                    OR
                    -- regular_crawling filter
                    (p_regular_crawling <> '' AND LOWER(TRIM(s.status)) = 'completed' AND s.last_updated_at < NOW() - p_regular_crawling::INTERVAL)
                    OR
                    -- error fallback
                    (LOWER(TRIM(s.status)) = 'error' AND s.last_updated_at < NOW() - p_last_error::INTERVAL)
                    OR LOWER(TRIM(s.status)) = 'pending'
                    OR LOWER(TRIM(s.status)) = 'new'
                    OR (LOWER(TRIM(s.status)) = 'processing' AND s.last_updated_at < NOW() - p_processing_timeout::INTERVAL)
                    OR s.status IS NULL
                )
              )
        ORDER BY s.sub_priority DESC, s.source_id ASC
        FOR UPDATE SKIP LOCKED
        LIMIT limit_val
    )
    UPDATE Sources
    SET status = 'processing',
        engine = p_engineID
    WHERE Sources.source_id IN (SELECT SelectedSources.source_id FROM SelectedSources)
    RETURNING Sources.source_id, Sources.source_uid::TEXT, Sources.url, Sources.restricted, Sources.flags, Sources.config, Sources.last_updated_at, Sources.sub_priority;
END;
$$
LANGUAGE plpgsql;
-- Preserve JSON extraction provenance on ObjectAttributes.
ALTER TABLE ObjectAttributes ADD COLUMN IF NOT EXISTS source_path TEXT;
ALTER TABLE ObjectAttributes ADD COLUMN IF NOT EXISTS context_path TEXT;
ALTER TABLE ObjectAttributes ADD COLUMN IF NOT EXISTS context_ref VARCHAR(64);
ALTER TABLE ObjectAttributes DROP CONSTRAINT IF EXISTS objectattributes_pkey;
ALTER TABLE ObjectAttributes ADD CONSTRAINT objectattributes_pkey PRIMARY KEY (attribute_id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_objattr_unscoped ON ObjectAttributes(object_type, object_id, attribute_key, value_hash) WHERE context_ref IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_objattr_scoped ON ObjectAttributes(object_type, object_id, attribute_key, value_hash, context_ref) WHERE context_ref IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_objattr_context ON ObjectAttributes(object_type, object_id, context_ref) WHERE context_ref IS NOT NULL;
