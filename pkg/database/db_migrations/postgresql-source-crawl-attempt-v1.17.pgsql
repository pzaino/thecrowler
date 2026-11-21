-- Optional CROWler 1.17 add-on: read docs/database-migrations.md before running.
-- Fenced crawl attempt tables. Apply AFTER the base 1.17 migration, and
-- only when the capability is absent (development databases already at 1.17).
-- This file uses the psql token :CROWLER_DB_USER; a GUI or raw-SQL client
-- needs the prepared copy from scripts/prepare-postgresql-migration.sh.

-- Additive fenced crawl-attempt ownership for development databases already
-- stamped 1.17. Manual and opt-in; not applied automatically at startup.
-- Run with:
--   psql -v CROWLER_DB_USER=crowler -f postgresql-source-crawl-attempt-v1.17.pgsql
BEGIN;
SELECT pg_advisory_xact_lock(hashtext('crowler:source-crawl-attempt-v1'));

DO $$
BEGIN
    IF NOT has_schema_privilege(current_user, current_schema(), 'CREATE') THEN
        RAISE EXCEPTION 'source crawl attempt addon must be run by an administrator with CREATE privilege';
    END IF;
END $$;

-- BEGIN SOURCE CRAWL ATTEMPT (schema 1.17)
-- Fenced source-crawl attempt ownership. Internal state only; no prompts,
-- cookies, secrets, HTML, model text or unbounded errors are stored here.

CREATE TABLE IF NOT EXISTS SchemaCapabilities (
    capability              TEXT PRIMARY KEY,
    applied_at              TIMESTAMPTZ NOT NULL,
    applied_by              TEXT NOT NULL,
    checksum                CHAR(64) NOT NULL,
    details                 JSONB NOT NULL DEFAULT '{}',
    CONSTRAINT chk_schema_capabilities_checksum
        CHECK (checksum ~ '^[0-9a-f]{64}$')
);

CREATE TABLE IF NOT EXISTS SourceCrawlAttempts (
    attempt_id       CHAR(64) PRIMARY KEY,
    claim_request_id CHAR(64) NOT NULL,
    source_id        BIGINT NOT NULL,
    generation       BIGINT NOT NULL CHECK (generation > 0),
    engine_id        TEXT NOT NULL CHECK (length(engine_id) BETWEEN 1 AND 256),
    state            TEXT NOT NULL CHECK (state IN (
                         'claimed','superseded','expired','abandoned',
                         'completed','failed','cancelled')),
    claimed_at       TIMESTAMPTZ(6) NOT NULL,
    lease_expires_at TIMESTAMPTZ(6) NOT NULL,
    superseded_by_generation BIGINT,
    terminal_at      TIMESTAMPTZ(6),
    reason_code      VARCHAR(64),
    operation_id     CHAR(64),
    request_id       CHAR(64),
    run_id           TEXT,
    agent_id         TEXT,
    owner            TEXT,
    principal_user_id BIGINT,
    approval_id      CHAR(64),
    approval_sha256  CHAR(64),
    created_at       TIMESTAMPTZ(6) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_updated_at  TIMESTAMPTZ(6) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_source_crawl_attempt_generation UNIQUE (source_id, generation),
    CONSTRAINT uq_source_crawl_attempt_exact_identity UNIQUE (source_id, generation, attempt_id),
    CONSTRAINT uq_source_crawl_attempt_request_source UNIQUE (claim_request_id, source_id),
    CONSTRAINT chk_source_crawl_attempt_request CHECK (claim_request_id ~ '^[a-f0-9]{64}$'),
    CONSTRAINT chk_source_crawl_attempt_id CHECK (attempt_id ~ '^[a-f0-9]{64}$'),
    CONSTRAINT chk_source_crawl_attempt_lease CHECK (lease_expires_at > claimed_at),
    CONSTRAINT chk_source_crawl_attempt_binding CHECK (
        (operation_id IS NULL AND request_id IS NULL AND run_id IS NULL AND
         agent_id IS NULL AND owner IS NULL AND principal_user_id IS NULL AND
         approval_id IS NULL AND approval_sha256 IS NULL)
        OR
        (operation_id IS NOT NULL AND request_id IS NOT NULL AND run_id IS NOT NULL AND
         agent_id IS NOT NULL AND owner IS NOT NULL AND principal_user_id > 0 AND
         approval_id IS NOT NULL AND approval_sha256 IS NOT NULL)
    ),
    CONSTRAINT chk_source_crawl_attempt_state CHECK (
        (state = 'claimed' AND superseded_by_generation IS NULL AND terminal_at IS NULL)
        OR (state = 'superseded' AND superseded_by_generation IS NOT NULL AND
            superseded_by_generation > generation AND terminal_at IS NOT NULL)
        OR (state IN ('expired','abandoned','completed','failed','cancelled') AND terminal_at IS NOT NULL)
    ),
    CONSTRAINT chk_source_crawl_attempt_reason CHECK (
        reason_code IS NULL OR reason_code ~ '^[a-z][a-z0-9_]{0,63}$'
    )
);
CREATE INDEX IF NOT EXISTS idx_source_crawl_attempts_source
    ON SourceCrawlAttempts (source_id, generation DESC);
CREATE INDEX IF NOT EXISTS idx_source_crawl_attempts_request
    ON SourceCrawlAttempts (claim_request_id, source_id);
CREATE INDEX IF NOT EXISTS idx_source_crawl_attempts_lease
    ON SourceCrawlAttempts (state, lease_expires_at);
CREATE UNIQUE INDEX IF NOT EXISTS uq_source_crawl_attempts_active_operation
    ON SourceCrawlAttempts (operation_id)
    WHERE operation_id IS NOT NULL AND state = 'claimed';

CREATE TABLE IF NOT EXISTS SourceCrawlClaims (
    source_id        BIGINT PRIMARY KEY,
    attempt_id       CHAR(64) NOT NULL UNIQUE,
    claim_request_id CHAR(64) NOT NULL,
    generation       BIGINT NOT NULL CHECK (generation > 0),
    engine_id        TEXT NOT NULL CHECK (length(engine_id) BETWEEN 1 AND 256),
    claimed_at       TIMESTAMPTZ(6) NOT NULL,
    lease_expires_at TIMESTAMPTZ(6) NOT NULL,
    last_updated_at  TIMESTAMPTZ(6) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_source_crawl_claim_generation UNIQUE (source_id, generation),
    CONSTRAINT fk_source_crawl_claim_source
        FOREIGN KEY (source_id) REFERENCES Sources(source_id) ON DELETE CASCADE,
    CONSTRAINT fk_source_crawl_claim_exact_attempt
        FOREIGN KEY (source_id, generation, attempt_id)
        REFERENCES SourceCrawlAttempts(source_id, generation, attempt_id)
        DEFERRABLE INITIALLY DEFERRED,
    CONSTRAINT chk_source_crawl_claim_request CHECK (claim_request_id ~ '^[a-f0-9]{64}$'),
    CONSTRAINT chk_source_crawl_claim_lease CHECK (lease_expires_at > claimed_at)
);
CREATE INDEX IF NOT EXISTS idx_source_crawl_claims_attempt
    ON SourceCrawlClaims (attempt_id);
CREATE INDEX IF NOT EXISTS idx_source_crawl_claims_request
    ON SourceCrawlClaims (claim_request_id);

CREATE TABLE IF NOT EXISTS SourceCrawlClaimPolicy (
    policy_id  SMALLINT PRIMARY KEY CHECK (policy_id = 1),
    mode       TEXT NOT NULL CHECK (mode IN ('legacy','fenced')),
    updated_at TIMESTAMPTZ(6) NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO SourceCrawlClaimPolicy(policy_id, mode)
VALUES (1, 'legacy') ON CONFLICT (policy_id) DO NOTHING;

-- Operator-only mode switch. Runtime claimers take the shared advisory lock;
-- this function takes the exclusive lock and therefore waits for in-flight
-- legacy or fenced claims. Use this function instead of a direct table UPDATE.
CREATE OR REPLACE FUNCTION set_source_crawl_claim_mode(new_mode TEXT)
RETURNS TEXT AS $$
BEGIN
    IF new_mode NOT IN ('legacy', 'fenced') THEN
        RAISE EXCEPTION 'source crawl claim mode must be legacy or fenced';
    END IF;
    PERFORM pg_advisory_xact_lock(hashtext('crowler:source-crawl-claim-policy'));
    UPDATE SourceCrawlClaimPolicy
       SET mode = new_mode, updated_at = CURRENT_TIMESTAMP
     WHERE policy_id = 1;
    RETURN new_mode;
END;
$$ LANGUAGE plpgsql;
ALTER FUNCTION set_source_crawl_claim_mode(TEXT) OWNER TO CURRENT_USER;
REVOKE ALL ON FUNCTION set_source_crawl_claim_mode(TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION set_source_crawl_claim_mode(TEXT) FROM :CROWLER_DB_USER;


INSERT INTO SchemaCapabilities (capability, applied_at, applied_by, checksum, details)
VALUES ('source-crawl-attempt-v1', CURRENT_TIMESTAMP, 'migration',
        'f545b987f0d77fcaac8219deefb70a9c00a354cf90ceef3aca4d3949c830206e',
        '{"schema_version":"1.17"}'::jsonb)
ON CONFLICT (capability) DO UPDATE
    SET applied_at = EXCLUDED.applied_at,
        applied_by = EXCLUDED.applied_by,
        checksum = EXCLUDED.checksum,
        details = EXCLUDED.details;
-- END SOURCE CRAWL ATTEMPT (schema 1.17)

-- Keep the legacy seven-argument source claim function compatible while making
-- fenced mode return no rows to old workers.
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
    policy_mode TEXT;
BEGIN
    -- Fenced mode belongs to the operator-owned attempt cutover. Take the
    -- shared advisory lock so an operator mode change waits for in-flight
    -- legacy claims, while legacy claimers do not serialize each other.
    PERFORM pg_advisory_xact_lock_shared(hashtext('crowler:source-crawl-claim-policy'));
    SELECT mode INTO policy_mode
      FROM SourceCrawlClaimPolicy
     WHERE policy_id = 1;
    IF policy_mode = 'fenced' THEN
        RETURN;
    END IF;

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

GRANT EXECUTE ON FUNCTION update_sources(INTEGER,VARCHAR,VARCHAR,VARCHAR,VARCHAR,VARCHAR,VARCHAR) TO :CROWLER_DB_USER;

-- Upgrade databases that already contain the earlier Stage B attempt objects.
-- Old split foreign keys permitted a current claim to mix an attempt ID from one
-- history row with the generation of another. Replace them with one exact
-- composite reference and fail closed on existing inconsistent data.
ALTER TABLE SourceCrawlClaims DROP CONSTRAINT IF EXISTS fk_source_crawl_claim_attempt_id;
ALTER TABLE SourceCrawlClaims DROP CONSTRAINT IF EXISTS fk_source_crawl_claim_attempt;
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'sourcecrawlattempts'::regclass
          AND conname = 'uq_source_crawl_attempt_exact_identity'
          AND contype = 'u'
    ) THEN
        ALTER TABLE SourceCrawlAttempts
            ADD CONSTRAINT uq_source_crawl_attempt_exact_identity
            UNIQUE (source_id, generation, attempt_id);
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'sourcecrawlclaims'::regclass
          AND conname = 'fk_source_crawl_claim_exact_attempt'
          AND contype = 'f'
    ) THEN
        ALTER TABLE SourceCrawlClaims
            ADD CONSTRAINT fk_source_crawl_claim_exact_attempt
            FOREIGN KEY (source_id, generation, attempt_id)
            REFERENCES SourceCrawlAttempts(source_id, generation, attempt_id)
            DEFERRABLE INITIALLY DEFERRED;
    END IF;
END $$;

-- The attempt tables are owned by the deploying administrator, not the
-- runtime claim role. The runtime may read the mode but cannot change it.
ALTER TABLE SourceCrawlAttempts OWNER TO CURRENT_USER;
ALTER TABLE SourceCrawlClaims OWNER TO CURRENT_USER;
ALTER TABLE SourceCrawlClaimPolicy OWNER TO CURRENT_USER;

-- Fail closed if an existing same-named object is structurally incompatible.
DO $$
DECLARE
    missing TEXT;
    current_version TEXT;
    current_generation TEXT;
    policy_mode TEXT;
BEGIN
    SELECT version INTO current_version FROM DBSchemaVersion WHERE is_current;
    IF current_version IS DISTINCT FROM '1.17' THEN
        RAISE EXCEPTION 'source crawl attempt schema requires database schema 1.17';
    END IF;
    SELECT writer_generation INTO current_generation
      FROM DatabaseWriterCompatibility WHERE lock_id = 1;
    IF current_generation IS DISTINCT FROM 'verified-ownership-v1.17' THEN
        RAISE EXCEPTION 'source crawl attempt schema requires writer generation verified-ownership-v1.17';
    END IF;

    SELECT string_agg(e.t || '.' || e.c, ', ' ORDER BY e.t, e.c)
      INTO missing
      FROM (VALUES
        ('SchemaCapabilities','capability'),
        ('SchemaCapabilities','applied_at'),
        ('SchemaCapabilities','applied_by'),
        ('SchemaCapabilities','checksum'),
        ('SchemaCapabilities','details'),
        ('SourceCrawlAttempts','attempt_id'),
        ('SourceCrawlAttempts','claim_request_id'),
        ('SourceCrawlAttempts','source_id'),
        ('SourceCrawlAttempts','generation'),
        ('SourceCrawlAttempts','engine_id'),
        ('SourceCrawlAttempts','state'),
        ('SourceCrawlAttempts','claimed_at'),
        ('SourceCrawlAttempts','lease_expires_at'),
        ('SourceCrawlAttempts','superseded_by_generation'),
        ('SourceCrawlAttempts','terminal_at'),
        ('SourceCrawlAttempts','reason_code'),
        ('SourceCrawlAttempts','operation_id'),
        ('SourceCrawlAttempts','request_id'),
        ('SourceCrawlAttempts','run_id'),
        ('SourceCrawlAttempts','agent_id'),
        ('SourceCrawlAttempts','owner'),
        ('SourceCrawlAttempts','principal_user_id'),
        ('SourceCrawlAttempts','approval_id'),
        ('SourceCrawlAttempts','approval_sha256'),
        ('SourceCrawlAttempts','created_at'),
        ('SourceCrawlAttempts','last_updated_at'),
        ('SourceCrawlClaims','source_id'),
        ('SourceCrawlClaims','attempt_id'),
        ('SourceCrawlClaims','claim_request_id'),
        ('SourceCrawlClaims','generation'),
        ('SourceCrawlClaims','engine_id'),
        ('SourceCrawlClaims','claimed_at'),
        ('SourceCrawlClaims','lease_expires_at'),
        ('SourceCrawlClaims','last_updated_at'),
        ('SourceCrawlClaimPolicy','policy_id'),
        ('SourceCrawlClaimPolicy','mode'),
        ('SourceCrawlClaimPolicy','updated_at')
      ) AS e(t, c)
      LEFT JOIN information_schema.columns c
        ON c.table_schema = current_schema()
       AND lower(c.table_name) = lower(e.t)
       AND c.column_name = e.c
     WHERE c.column_name IS NULL;
    IF missing IS NOT NULL THEN
        RAISE EXCEPTION 'source crawl attempt schema is missing columns: %', missing;
    END IF;

    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = current_schema()
          AND lower(table_name) IN ('sourcecrawlclaims','sourcecrawlattempts')
          AND lower(column_name) IN ('payload','raw_payload','prompt','prompts','messages',
              'tool_arguments','arguments','raw_result','result','token','password','secret',
              'dsn','cookie','cookies','request_body','response_body','model_text','html')
    ) THEN
        RAISE EXCEPTION 'source crawl attempt schema contains forbidden raw-content columns';
    END IF;

    IF EXISTS (
        SELECT 1 FROM (VALUES
          ('idx_source_crawl_attempts_source'),
          ('idx_source_crawl_attempts_request'),
          ('idx_source_crawl_attempts_lease'),
          ('uq_source_crawl_attempts_active_operation'),
          ('idx_source_crawl_claims_attempt'),
          ('idx_source_crawl_claims_request')
        ) AS e(n)
        LEFT JOIN pg_indexes i
          ON lower(i.schemaname) = lower(current_schema()) AND lower(i.indexname) = lower(e.n)
        WHERE i.indexname IS NULL
    ) THEN
        RAISE EXCEPTION 'source crawl attempt schema is missing an index';
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_class i JOIN pg_namespace n ON n.oid = i.relnamespace
        WHERE n.nspname = current_schema() AND lower(i.relname) = 'uq_source_crawl_attempts_active_operation'
          AND i.relkind = 'i'
    ) THEN
        RAISE EXCEPTION 'source crawl attempt schema is missing the active-operation unique index';
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint c
        WHERE c.conrelid = 'sourcecrawlattempts'::regclass
          AND c.conname = 'uq_source_crawl_attempt_generation'
          AND c.contype = 'u'
    ) OR NOT EXISTS (
        SELECT 1 FROM pg_constraint c
        WHERE c.conrelid = 'sourcecrawlattempts'::regclass
          AND c.conname = 'uq_source_crawl_attempt_request_source'
          AND c.contype = 'u'
    ) OR NOT EXISTS (
        SELECT 1 FROM pg_constraint c
        WHERE c.conrelid = 'sourcecrawlattempts'::regclass
          AND c.conname = 'uq_source_crawl_attempt_exact_identity'
          AND c.contype = 'u'
    ) OR NOT EXISTS (
        SELECT 1 FROM pg_constraint c
        WHERE c.conrelid = 'sourcecrawlclaims'::regclass
          AND c.conname = 'uq_source_crawl_claim_generation'
          AND c.contype = 'u'
    ) THEN
        RAISE EXCEPTION 'source crawl attempt schema is missing a unique constraint';
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='sourcecrawlattempts'::regclass AND contype='p')
       OR NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='sourcecrawlclaims'::regclass AND contype='p')
       OR NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='sourcecrawlclaimpolicy'::regclass AND contype='p') THEN
        RAISE EXCEPTION 'source crawl attempt schema is missing a primary key';
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint c
        WHERE c.conrelid = 'sourcecrawlclaims'::regclass
          AND c.conname = 'fk_source_crawl_claim_source' AND c.contype = 'f'
    ) OR NOT EXISTS (
        SELECT 1 FROM pg_constraint c
        WHERE c.conrelid = 'sourcecrawlclaims'::regclass
          AND c.conname = 'fk_source_crawl_claim_exact_attempt' AND c.contype = 'f'
    ) THEN
        RAISE EXCEPTION 'source crawl attempt schema is missing a foreign key';
    END IF;

    IF EXISTS (
        SELECT 1 FROM (VALUES
          ('SourceCrawlAttempts','chk_source_crawl_attempt_request'),
          ('SourceCrawlAttempts','chk_source_crawl_attempt_id'),
          ('SourceCrawlAttempts','chk_source_crawl_attempt_lease'),
          ('SourceCrawlAttempts','chk_source_crawl_attempt_binding'),
          ('SourceCrawlAttempts','chk_source_crawl_attempt_state'),
          ('SourceCrawlAttempts','chk_source_crawl_attempt_reason'),
          ('SourceCrawlClaims','chk_source_crawl_claim_request'),
          ('SourceCrawlClaims','chk_source_crawl_claim_lease'),
          ('SourceCrawlClaimPolicy','sourcecrawlclaimpolicy_mode_check')
        ) AS e(t, n)
        LEFT JOIN pg_namespace ns ON ns.nspname = current_schema()
        LEFT JOIN pg_class r ON r.relnamespace = ns.oid AND lower(r.relname) = lower(e.t)
        LEFT JOIN pg_constraint c ON c.conrelid = r.oid AND c.conname = e.n AND c.contype = 'c'
        WHERE c.oid IS NULL
    ) THEN
        RAISE EXCEPTION 'source crawl attempt schema is missing a check constraint';
    END IF;

    SELECT mode INTO policy_mode FROM SourceCrawlClaimPolicy WHERE policy_id = 1;
    IF policy_mode IS NULL OR policy_mode NOT IN ('legacy','fenced') THEN
        RAISE EXCEPTION 'source crawl attempt policy row is missing or invalid';
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM SchemaCapabilities
        WHERE capability = 'source-crawl-attempt-v1'
          AND checksum = 'f545b987f0d77fcaac8219deefb70a9c00a354cf90ceef3aca4d3949c830206e'
    ) THEN
        RAISE EXCEPTION 'source crawl attempt capability marker is missing or invalid';
    END IF;

    IF to_regprocedure('set_source_crawl_claim_mode(text)') IS NULL THEN
        RAISE EXCEPTION 'source crawl attempt schema is missing the operator mode function';
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
        WHERE n.nspname = current_schema() AND p.proname = 'update_sources'
          AND pg_get_functiondef(p.oid) LIKE '%SourceCrawlClaimPolicy%'
          AND pg_get_functiondef(p.oid) LIKE '%fenced%'
    ) THEN
        RAISE EXCEPTION 'update_sources is not fenced-mode aware';
    END IF;
END $$;

GRANT SELECT, INSERT, UPDATE ON SourceCrawlAttempts TO :CROWLER_DB_USER;
GRANT SELECT, INSERT, UPDATE ON SourceCrawlClaims TO :CROWLER_DB_USER;
GRANT SELECT ON SourceCrawlClaimPolicy TO :CROWLER_DB_USER;
REVOKE INSERT, UPDATE, DELETE ON SourceCrawlClaimPolicy FROM :CROWLER_DB_USER;
REVOKE DELETE ON SourceCrawlAttempts, SourceCrawlClaims FROM :CROWLER_DB_USER;
GRANT SELECT, INSERT, UPDATE ON SchemaCapabilities TO :CROWLER_DB_USER;

COMMIT;
