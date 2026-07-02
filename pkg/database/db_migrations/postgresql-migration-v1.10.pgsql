-- PostgreSQL upgrade migration for atomic Information Seed claims.
-- Safe to run on existing databases; the function definition is idempotent.

BEGIN;

INSERT INTO DBSchemaVersion (version, description, is_current)
SELECT '1.10', 'Atomic Information Seed claims for scaled crowler-engine deployments', TRUE
WHERE NOT EXISTS (SELECT 1 FROM DBSchemaVersion WHERE version = '1.10');

UPDATE DBSchemaVersion
SET is_current = (version = '1.10'), last_updated_at = CURRENT_TIMESTAMP;

-- Atomically claims InformationSeed work for one crowler-engine replica. The
-- row locks and lifecycle update happen in the same statement so concurrent
-- engines cannot receive the same seed.
CREATE OR REPLACE FUNCTION update_informationseed(
    limit_val INTEGER,
    p_priority VARCHAR,
    p_engineID VARCHAR,
    p_processing_timeout VARCHAR,
    p_retry_after VARCHAR
)
RETURNS TABLE(
    information_seed_id BIGINT,
    created_at TIMESTAMPTZ,
    last_updated_at TIMESTAMPTZ,
    category_id BIGINT,
    usr_id BIGINT,
    information_seed VARCHAR,
    status VARCHAR,
    priority VARCHAR,
    engine VARCHAR,
    last_processed_at TIMESTAMPTZ,
    last_error TEXT,
    last_error_at TIMESTAMPTZ,
    disabled BOOLEAN,
    attempts INTEGER,
    config JSONB
) AS
$$
DECLARE
    priority_list TEXT[];
    use_priority_filter BOOLEAN := FALSE;
BEGIN
    limit_val := GREATEST(COALESCE(limit_val, 0), 0);
    p_priority := COALESCE(TRIM(p_priority), '');
    p_engineID := COALESCE(TRIM(p_engineID), '');
    p_processing_timeout := COALESCE(TRIM(p_processing_timeout), '');
    p_retry_after := COALESCE(TRIM(p_retry_after), '');

    IF p_engineID = '' THEN
        RAISE EXCEPTION 'engine ID is required to claim information seeds';
    END IF;
    IF p_processing_timeout = '' THEN
        p_processing_timeout := '30 minutes';
    END IF;
    IF p_retry_after = '' THEN
        p_retry_after := '1 minute';
    END IF;

    IF p_priority <> '' THEN
        priority_list := ARRAY(
            SELECT TRIM(LOWER(value))
            FROM unnest(string_to_array(p_priority, ',')) AS value
            WHERE TRIM(value) <> ''
        );
        use_priority_filter := COALESCE(array_length(priority_list, 1), 0) > 0;
    END IF;

    RETURN QUERY
    WITH selected_seeds AS (
        SELECT seed.information_seed_id
        FROM InformationSeed AS seed
        WHERE COALESCE(seed.disabled, FALSE) = FALSE
          AND (
                NOT use_priority_filter
                OR LOWER(TRIM(seed.priority)) = ANY(priority_list)
              )
          AND (
                LOWER(TRIM(seed.status)) IN ('new', 'pending')
                OR (
                    LOWER(TRIM(seed.status)) = 'processing'
                    AND (
                        seed.last_processed_at IS NULL
                        OR seed.last_processed_at < NOW() - p_processing_timeout::INTERVAL
                    )
                )
                OR (
                    LOWER(TRIM(seed.status)) = 'error'
                    AND (
                        seed.last_error_at IS NULL
                        OR seed.last_error_at < NOW() - p_retry_after::INTERVAL
                    )
                )
              )
        ORDER BY seed.created_at ASC, seed.information_seed_id ASC
        FOR UPDATE SKIP LOCKED
        LIMIT limit_val
    )
    UPDATE InformationSeed AS claimed_seed
    SET status = 'processing',
        engine = p_engineID,
        last_processed_at = NOW(),
        attempts = COALESCE(claimed_seed.attempts, 0) + 1
    FROM selected_seeds
    WHERE claimed_seed.information_seed_id = selected_seeds.information_seed_id
    RETURNING claimed_seed.information_seed_id,
              claimed_seed.created_at,
              claimed_seed.last_updated_at,
              claimed_seed.category_id,
              claimed_seed.usr_id,
              claimed_seed.information_seed,
              claimed_seed.status,
              claimed_seed.priority,
              claimed_seed.engine,
              claimed_seed.last_processed_at,
              claimed_seed.last_error,
              claimed_seed.last_error_at,
              claimed_seed.disabled,
              claimed_seed.attempts,
              claimed_seed.config;
END;
$$
LANGUAGE plpgsql;

COMMIT;
