-- Version 1.17 adds durable crawl-approval persistence, verified source
-- ownership provenance, and Information Seed claim lease/fencing/retry state
-- for the trusted operator/agent authorization paths.
-- This migration is additive, atomic and idempotent. It also advances the
-- writer generation so old writers must stop before the new writer starts.
--
-- Run with: psql -v CROWLER_DB_USER=crowler -f postgresql-migration-v1.17.pgsql
BEGIN;

LOCK TABLE DBSchemaVersion IN EXCLUSIVE MODE;
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM DBSchemaVersion WHERE version IN ('1.16', '1.17')) THEN
    RAISE EXCEPTION 'v1.17 requires schema version 1.16 (or 1.17 for a repeat run)';
  END IF;
END $$;


----------------------------------------------------------------
-- SourceOwnership records verified, authoritative source ownership.
-- Sources.usr_id remains the persisted owner identifier, but a source is only
-- eligible for authenticated owner mutations and agent scheduling when this
-- table contains a matching verified row. Historical rows without a provenance
-- record remain legacy/unverified until an administrator verifies them.
CREATE TABLE IF NOT EXISTS SourceOwnership (
    source_id BIGINT PRIMARY KEY,
    user_id BIGINT NOT NULL,
    verified_at TIMESTAMPTZ NOT NULL,
    verified_by_user_id BIGINT NOT NULL,
    verified_by TEXT NOT NULL,
    verification_method TEXT NOT NULL,
    verification_reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT chk_sourceownership_user CHECK (user_id > 0),
    CONSTRAINT chk_sourceownership_verifier CHECK (verified_by_user_id > 0)
);

CREATE INDEX IF NOT EXISTS idx_sourceownership_user
ON SourceOwnership(user_id);

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_update_sourceownership_last_updated_before_update') THEN
        CREATE TRIGGER trg_update_sourceownership_last_updated_before_update
        BEFORE UPDATE ON SourceOwnership
        FOR EACH ROW
        EXECUTE FUNCTION update_last_updated_at_column();
    END IF;
END
$$;

ALTER TABLE SourceOwnership OWNER TO :CROWLER_DB_USER;
GRANT SELECT, INSERT, UPDATE, DELETE ON SourceOwnership TO :CROWLER_DB_USER;

CREATE TABLE IF NOT EXISTS CrawlApprovals (
    approval_id CHAR(64) PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    proposal_sha256 CHAR(64) NOT NULL,
    request_id TEXT NOT NULL,
    run_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    owner TEXT NOT NULL,
    principal_user_id BIGINT NOT NULL,
    source_id BIGINT NOT NULL,
    operation_type TEXT NOT NULL,

    issuer TEXT NOT NULL,
    issued_by_user_id BIGINT NOT NULL,
    issued_by TEXT NOT NULL,
    issued_by_issuer TEXT NOT NULL,
    approved_by_user_id BIGINT NOT NULL,
    approved_by TEXT NOT NULL,
    approved_by_issuer TEXT NOT NULL,
    approval_sha256 CHAR(64) NOT NULL,

    proposal_expires_at TIMESTAMPTZ NOT NULL,
    approved_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,

    revoked_at TIMESTAMPTZ,
    revoked_by_user_id BIGINT,
    revoked_by TEXT,
    revocation_reason TEXT,

    CONSTRAINT uq_crawlapprovals_logical UNIQUE
        (request_id, run_id, principal_user_id, source_id, operation_type),
    CONSTRAINT chk_crawlapprovals_hashes CHECK (
        approval_id ~ '^[0-9a-f]{64}$' AND
        proposal_sha256 ~ '^[0-9a-f]{64}$' AND
        approval_sha256 ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT chk_crawlapprovals_lifetime CHECK (expires_at > approved_at),
    CONSTRAINT chk_crawlapprovals_max_lifetime
        CHECK (expires_at <= approved_at + INTERVAL '24 hours')
);

CREATE INDEX IF NOT EXISTS idx_crawlapprovals_source
ON CrawlApprovals(source_id, expires_at);

CREATE INDEX IF NOT EXISTS idx_crawlapprovals_principal
ON CrawlApprovals(principal_user_id, expires_at);

CREATE INDEX IF NOT EXISTS idx_crawlapprovals_request
ON CrawlApprovals(run_id, request_id);

ALTER TABLE CrawlApprovals OWNER TO :CROWLER_DB_USER;
GRANT SELECT, INSERT, UPDATE, DELETE ON CrawlApprovals TO :CROWLER_DB_USER;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_update_crawlapprovals_last_updated_before_update') THEN
        CREATE TRIGGER trg_update_crawlapprovals_last_updated_before_update
        BEFORE UPDATE ON CrawlApprovals
        FOR EACH ROW
        EXECUTE FUNCTION update_last_updated_at_column();
    END IF;
END
$$;

----------------------------------------------------------------
-- Durable crawl proposals. A proposal is created by the trusted agent
-- runtime before any scheduling mutation and is the restart-safe anchor used
-- by operator approval and submission. Approval state remains in
-- CrawlApprovals; this table records proposal provenance and lifecycle.
CREATE TABLE IF NOT EXISTS CrawlProposals (
    request_id CHAR(64) PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    run_id TEXT NOT NULL,
    tool_call_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    owner TEXT NOT NULL,
    principal_user_id BIGINT NOT NULL,
    source_id BIGINT NOT NULL,
    operation_type TEXT NOT NULL,
    proposal_sha256 CHAR(64) NOT NULL,
    trace_id TEXT NOT NULL DEFAULT '',
    max_requests INTEGER NOT NULL,
    budget_window_seconds BIGINT NOT NULL,
    policy_json TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT 'pending',
    expires_at TIMESTAMPTZ NOT NULL,
    submitted_at TIMESTAMPTZ,
    operation_id TEXT,

    CONSTRAINT uq_crawlproposals_logical UNIQUE
        (run_id, tool_call_id, source_id, operation_type),
    CONSTRAINT chk_crawlproposals_hashes CHECK (
        request_id ~ '^[0-9a-f]{64}$' AND proposal_sha256 ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT chk_crawlproposals_state CHECK (
        state IN ('pending', 'submitted', 'expired', 'denied')
    )
);

CREATE INDEX IF NOT EXISTS idx_crawlproposals_run_request
ON CrawlProposals(run_id, request_id);

CREATE INDEX IF NOT EXISTS idx_crawlproposals_principal
ON CrawlProposals(principal_user_id, expires_at);

CREATE INDEX IF NOT EXISTS idx_crawlproposals_source
ON CrawlProposals(source_id, expires_at);

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_update_crawlproposals_last_updated_before_update') THEN
        CREATE TRIGGER trg_update_crawlproposals_last_updated_before_update
        BEFORE UPDATE ON CrawlProposals
        FOR EACH ROW
        EXECUTE FUNCTION update_last_updated_at_column();
    END IF;
END
$$;

ALTER TABLE CrawlProposals OWNER TO :CROWLER_DB_USER;
GRANT SELECT, INSERT, UPDATE, DELETE ON CrawlProposals TO :CROWLER_DB_USER;


----------------------------------------------------------------
-- Information Seed claim leases, fencing, heartbeat and retry metadata.
-- These are additive fields on the existing 1.17 schema contract.
ALTER TABLE InformationSeed ADD COLUMN IF NOT EXISTS claim_token VARCHAR(64);
ALTER TABLE InformationSeed ADD COLUMN IF NOT EXISTS fencing_token BIGINT DEFAULT 0 NOT NULL;
ALTER TABLE InformationSeed ADD COLUMN IF NOT EXISTS lease_expires_at TIMESTAMPTZ;
ALTER TABLE InformationSeed ADD COLUMN IF NOT EXISTS heartbeat_at TIMESTAMPTZ;
ALTER TABLE InformationSeed ADD COLUMN IF NOT EXISTS next_retry_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_informationseed_lease_expiry
    ON InformationSeed(status, disabled, lease_expires_at);
CREATE INDEX IF NOT EXISTS idx_informationseed_next_retry
    ON InformationSeed(status, disabled, next_retry_at);
CREATE INDEX IF NOT EXISTS idx_informationseed_claim_token
    ON InformationSeed(claim_token);

-- Remove the legacy five-argument claim function so old writers cannot keep
-- claiming without the token/lease/fencing semantics.
DROP FUNCTION IF EXISTS update_informationseed(integer, varchar, varchar, varchar, varchar);

CREATE OR REPLACE FUNCTION update_informationseed(
    limit_val INTEGER,
    p_priority VARCHAR,
    p_engineID VARCHAR,
    p_processing_timeout VARCHAR,
    p_retry_after VARCHAR,
    p_lease_duration VARCHAR,
    p_max_attempts INTEGER,
    p_claim_token VARCHAR
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
    claim_token VARCHAR,
    fencing_token BIGINT,
    lease_expires_at TIMESTAMPTZ,
    heartbeat_at TIMESTAMPTZ,
    next_retry_at TIMESTAMPTZ,
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
    p_lease_duration := COALESCE(TRIM(p_lease_duration), '');
    p_claim_token := COALESCE(TRIM(p_claim_token), '');

    IF p_engineID = '' THEN
        RAISE EXCEPTION 'engine ID is required to claim information seeds';
    END IF;
    IF p_claim_token = '' THEN
        RAISE EXCEPTION 'claim token is required to claim information seeds';
    END IF;
    IF p_processing_timeout = '' THEN
        p_processing_timeout := '30 minutes';
    END IF;
    IF p_retry_after = '' THEN
        p_retry_after := '1 minute';
    END IF;
    IF p_lease_duration = '' THEN
        p_lease_duration := p_processing_timeout;
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
          AND (COALESCE(p_max_attempts, 0) = 0 OR COALESCE(seed.attempts, 0) < p_max_attempts)
          AND (
                NOT use_priority_filter
                OR LOWER(TRIM(seed.priority)) = ANY(priority_list)
              )
          AND (
                LOWER(TRIM(seed.status)) IN ('new', 'pending')
                OR (
                    LOWER(TRIM(seed.status)) = 'processing'
                    AND (
                        (seed.lease_expires_at IS NOT NULL AND seed.lease_expires_at < NOW())
                        OR (
                            seed.lease_expires_at IS NULL
                            AND (
                                seed.last_processed_at IS NULL
                                OR seed.last_processed_at < NOW() - p_processing_timeout::INTERVAL
                            )
                        )
                    )
                )
                OR (
                    LOWER(TRIM(seed.status)) = 'error'
                    AND (
                        (seed.next_retry_at IS NOT NULL AND seed.next_retry_at <= NOW())
                        OR (
                            seed.next_retry_at IS NULL
                            AND (
                                seed.last_error_at IS NULL
                                OR seed.last_error_at < NOW() - p_retry_after::INTERVAL
                            )
                        )
                    )
                )
              )
        ORDER BY CASE
                WHEN LOWER(TRIM(seed.status)) IN ('new', 'pending') THEN 0
                WHEN LOWER(TRIM(seed.status)) = 'processing' THEN 1
                ELSE 2
            END ASC,
            CASE
                WHEN LOWER(TRIM(seed.status)) = 'error' THEN seed.next_retry_at
                ELSE seed.created_at
            END ASC,
            seed.created_at ASC,
            seed.information_seed_id ASC
        FOR UPDATE SKIP LOCKED
        LIMIT limit_val
    )
    UPDATE InformationSeed AS claimed_seed
    SET status = 'processing',
        engine = p_engineID,
        last_processed_at = NOW(),
        attempts = COALESCE(claimed_seed.attempts, 0) + 1,
        claim_token = p_claim_token,
        fencing_token = COALESCE(claimed_seed.fencing_token, 0) + 1,
        lease_expires_at = NOW() + p_lease_duration::INTERVAL,
        heartbeat_at = NOW(),
        next_retry_at = NULL
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
              claimed_seed.claim_token,
              claimed_seed.fencing_token,
              claimed_seed.lease_expires_at,
              claimed_seed.heartbeat_at,
              claimed_seed.next_retry_at,
              claimed_seed.config;
END;
$$
LANGUAGE plpgsql;


UPDATE DBSchemaVersion SET is_current=FALSE;
INSERT INTO DBSchemaVersion(version,description,is_current)
VALUES ('1.17','Durable crawl approval persistence',TRUE)
ON CONFLICT (version) DO UPDATE SET description=EXCLUDED.description,is_current=TRUE;
CREATE UNIQUE INDEX IF NOT EXISTS uq_dbschemaversion_one_current
  ON DBSchemaVersion (is_current) WHERE is_current=TRUE;

INSERT INTO DatabaseWriterCompatibility(lock_id,writer_generation)
VALUES (1,'verified-ownership-v1.17')
ON CONFLICT (lock_id) DO UPDATE SET writer_generation=EXCLUDED.writer_generation;

COMMIT;
