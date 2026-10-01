\set ON_ERROR_STOP on
\pset pager off

DROP TABLE IF EXISTS upsert_reference;
DROP TABLE IF EXISTS upsert_optimized;
CREATE TABLE upsert_reference (
    link_id BIGSERIAL PRIMARY KEY,
    index_id BIGINT NOT NULL,
    artifact_id BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (index_id, artifact_id)
);
CREATE TABLE upsert_optimized (LIKE upsert_reference INCLUDING ALL);

INSERT INTO upsert_reference(index_id, artifact_id)
SELECT 1, g FROM generate_series(1, 10000) AS g;
INSERT INTO upsert_optimized(index_id, artifact_id)
SELECT 1, g FROM generate_series(1, 10000) AS g;

CREATE TEMP TABLE upsert_timestamp_baseline AS
SELECT 'reference'::text AS path, index_id, artifact_id, created_at, last_updated_at
FROM upsert_reference
UNION ALL
SELECT 'optimized', index_id, artifact_id, created_at, last_updated_at
FROM upsert_optimized;

SELECT pg_stat_reset();
SELECT pg_current_wal_lsn() AS reference_wal_start \gset
SELECT 'INSERT INTO upsert_reference(index_id, artifact_id)
        SELECT 1, g FROM generate_series(1, 10000) AS g
        ON CONFLICT (index_id, artifact_id)
        DO UPDATE SET artifact_id = EXCLUDED.artifact_id;'
FROM generate_series(1, 10) \gexec
SELECT pg_current_wal_lsn() AS reference_wal_end \gset

SELECT pg_current_wal_lsn() AS optimized_wal_start \gset
SELECT 'INSERT INTO upsert_optimized(index_id, artifact_id)
        SELECT 1, g FROM generate_series(1, 10000) AS g
        ON CONFLICT (index_id, artifact_id) DO NOTHING;'
FROM generate_series(1, 10) \gexec
SELECT pg_current_wal_lsn() AS optimized_wal_end \gset

-- Force statistics messages to be delivered before reading the counters.
SELECT pg_stat_force_next_flush();
SELECT relname, n_tup_ins, n_tup_upd
FROM pg_stat_user_tables
WHERE relname IN ('upsert_reference', 'upsert_optimized')
ORDER BY relname;
SELECT pg_wal_lsn_diff(:'reference_wal_end', :'reference_wal_start') AS reference_wal_bytes,
       pg_wal_lsn_diff(:'optimized_wal_end', :'optimized_wal_start') AS optimized_wal_bytes;

DO $$
DECLARE reference_digest text;
DECLARE optimized_digest text;
DECLARE optimized_updates bigint;
BEGIN
    SELECT md5(string_agg(format('%s|%s|%s', index_id, artifact_id, created_at), ','
                          ORDER BY index_id, artifact_id))
      INTO reference_digest FROM upsert_reference;
    SELECT md5(string_agg(format('%s|%s|%s', index_id, artifact_id, created_at), ','
                          ORDER BY index_id, artifact_id))
      INTO optimized_digest FROM upsert_optimized;
    -- The tables were seeded in separate statements, so compare logical keys
    -- and stable timestamps independently rather than sequence-generated IDs.
    IF (SELECT count(*) FROM upsert_reference) <> (SELECT count(*) FROM upsert_optimized)
       OR EXISTS (
          SELECT index_id, artifact_id FROM upsert_reference
          EXCEPT SELECT index_id, artifact_id FROM upsert_optimized
       ) THEN
        RAISE EXCEPTION 'reference and optimized logical states differ (% vs %)',
            reference_digest, optimized_digest;
    END IF;
    IF EXISTS (
        SELECT 1 FROM upsert_timestamp_baseline b
        JOIN upsert_reference r USING (index_id, artifact_id)
        WHERE b.path = 'reference'
          AND (b.created_at, b.last_updated_at) IS DISTINCT FROM (r.created_at, r.last_updated_at)
    ) OR EXISTS (
        SELECT 1 FROM upsert_timestamp_baseline b
        JOIN upsert_optimized o USING (index_id, artifact_id)
        WHERE b.path = 'optimized'
          AND (b.created_at, b.last_updated_at) IS DISTINCT FROM (o.created_at, o.last_updated_at)
    ) THEN
        RAISE EXCEPTION 'duplicate path changed an externally visible timestamp';
    END IF;
    SELECT n_tup_upd INTO optimized_updates
    FROM pg_stat_user_tables WHERE relname = 'upsert_optimized';
    IF optimized_updates <> 0 THEN
        RAISE EXCEPTION 'optimized path unexpectedly updated % rows', optimized_updates;
    END IF;
END $$;
