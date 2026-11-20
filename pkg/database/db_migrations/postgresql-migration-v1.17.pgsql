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


-- BEGIN AGENT GOAL PERSISTENCE (schema 1.17)
-- Asynchronous goal checkpoint persistence. This is internal agent state; it
-- never stores prompts, model messages, raw tool arguments/results, evidence
-- payloads or credentials. The schema contract is goal-async-v2.

CREATE TABLE IF NOT EXISTS AgentGoalRuns (
    goal_run_id                 TEXT PRIMARY KEY,
    checkpoint_version          SMALLINT NOT NULL,
    contract_version            TEXT NOT NULL,
    revision                    BIGINT NOT NULL,
    trace_id                    TEXT NOT NULL,
    correlation_id              TEXT,
    agent_id                    TEXT NOT NULL,
    owner                       TEXT NOT NULL,
    principal_kind              TEXT NOT NULL,
    principal_user_id           BIGINT NOT NULL DEFAULT 0,
    principal_username          TEXT NOT NULL DEFAULT '',
    principal_issuer            TEXT NOT NULL DEFAULT '',
    goal_id                     TEXT NOT NULL,
    goal_spec_hash              CHAR(64) NOT NULL,
    policy_sha256               CHAR(64) NOT NULL,
    state                       TEXT NOT NULL,
    slices                      INTEGER NOT NULL DEFAULT 0,
    created_at                  TIMESTAMPTZ(6) NOT NULL,
    updated_at                  TIMESTAMPTZ(6) NOT NULL,
    deadline                    TIMESTAMPTZ(6) NOT NULL,
    reason_code                 TEXT NOT NULL,
    ledger_id                   TEXT NOT NULL,
    run_nonce_hash              CHAR(64) NOT NULL,
    last_transition_id          TEXT,
    last_transition_hash        CHAR(64),
    verification_verifier       TEXT,
    verification_sha256         CHAR(64),
    verified_at                 TIMESTAMPTZ(6),
    cancel_requested_at         TIMESTAMPTZ(6),
    cancel_requested_by_user_id BIGINT,
    max_slices                  INTEGER NOT NULL,
    max_model_rounds            INTEGER NOT NULL,
    max_tool_attempts           INTEGER NOT NULL,
    max_result_bytes            BIGINT NOT NULL,
    max_evidence_records        INTEGER NOT NULL,
    max_evidence_bytes          BIGINT NOT NULL,
    max_retries                 INTEGER NOT NULL,
    max_scheduled_work_requests INTEGER NOT NULL,
    max_consecutive_denials     INTEGER NOT NULL,
    max_slice_elapsed_ns        BIGINT NOT NULL,
    used_model_rounds           INTEGER NOT NULL DEFAULT 0,
    used_tool_attempts          INTEGER NOT NULL DEFAULT 0,
    used_result_bytes           BIGINT NOT NULL DEFAULT 0,
    used_evidence_records       INTEGER NOT NULL DEFAULT 0,
    used_evidence_bytes         BIGINT NOT NULL DEFAULT 0,
    used_retries                INTEGER NOT NULL DEFAULT 0,
    used_scheduled_work_requests INTEGER NOT NULL DEFAULT 0,
    used_consecutive_denials    INTEGER NOT NULL DEFAULT 0,
    CONSTRAINT chk_agent_goal_versions CHECK (
        checkpoint_version = 2 AND contract_version = 'goal-async-v2'
    ),
    CONSTRAINT chk_agent_goal_revision CHECK (revision > 0),
    CONSTRAINT chk_agent_goal_state CHECK (state IN (
        'created', 'running', 'awaiting_approval', 'awaiting_external_work',
        'criteria_met', 'incomplete', 'budget_exhausted', 'cancelled',
        'denied', 'failed'
    )),
    CONSTRAINT chk_agent_goal_principal CHECK (
        (principal_kind = 'user' AND principal_user_id > 0
            AND length(btrim(principal_username)) BETWEEN 1 AND 256
            AND length(btrim(principal_issuer)) BETWEEN 1 AND 512)
        OR
        (principal_kind = 'system' AND principal_user_id = 0
            AND principal_username = '' AND principal_issuer = '')
    ),
    CONSTRAINT chk_agent_goal_identity CHECK (
        length(btrim(goal_run_id)) BETWEEN 1 AND 128
        AND length(btrim(trace_id)) BETWEEN 1 AND 128
        AND length(btrim(agent_id)) BETWEEN 1 AND 128
        AND length(btrim(owner)) BETWEEN 1 AND 256
        AND length(btrim(goal_id)) BETWEEN 1 AND 128
        AND length(btrim(ledger_id)) BETWEEN 1 AND 128
        AND (correlation_id IS NULL
             OR length(btrim(correlation_id)) BETWEEN 1 AND 128)
    ),
    CONSTRAINT chk_agent_goal_hashes CHECK (
        goal_spec_hash ~ '^[0-9a-f]{64}$'
        AND policy_sha256 ~ '^[0-9a-f]{64}$'
        AND run_nonce_hash ~ '^[0-9a-f]{64}$'
        AND (last_transition_hash IS NULL OR last_transition_hash ~ '^[0-9a-f]{64}$')
        AND (verification_sha256 IS NULL OR verification_sha256 ~ '^[0-9a-f]{64}$')
    ),
    CONSTRAINT chk_agent_goal_timestamps CHECK (
        updated_at >= created_at
        AND deadline > created_at
        AND deadline <= created_at + INTERVAL '24 hours'
    ),
    CONSTRAINT chk_agent_goal_active_deadline CHECK (
        state NOT IN ('created', 'running', 'awaiting_approval',
                      'awaiting_external_work', 'criteria_met', 'incomplete')
        OR updated_at < deadline
    ),
    CONSTRAINT chk_agent_goal_ceilings CHECK (
        max_slices BETWEEN 1 AND 64
        AND max_model_rounds BETWEEN 1 AND 8
        AND max_tool_attempts BETWEEN 1 AND 16
        AND max_result_bytes BETWEEN 1024 AND 65536
        AND max_evidence_records BETWEEN 1 AND 32
        AND max_evidence_bytes BETWEEN 1024 AND 65536
        AND max_retries BETWEEN 0 AND 16
        AND max_scheduled_work_requests BETWEEN 0 AND 32
        AND max_consecutive_denials BETWEEN 1 AND 5
        AND max_slice_elapsed_ns BETWEEN 1000000000 AND 120000000000
    ),
    CONSTRAINT chk_agent_goal_used CHECK (
        slices BETWEEN 0 AND max_slices
        AND used_model_rounds BETWEEN 0 AND max_model_rounds
        AND used_tool_attempts BETWEEN 0 AND max_tool_attempts
        AND used_result_bytes BETWEEN 0 AND max_result_bytes
        AND used_evidence_records BETWEEN 0 AND max_evidence_records
        AND used_evidence_bytes BETWEEN 0 AND max_evidence_bytes
        AND used_retries BETWEEN 0 AND max_retries
        AND used_scheduled_work_requests BETWEEN 0 AND max_scheduled_work_requests
        AND used_consecutive_denials BETWEEN 0 AND max_consecutive_denials
    ),
    CONSTRAINT chk_agent_goal_verification CHECK (
        (state = 'criteria_met'
            AND verification_verifier = 'deterministic_verifier'
            AND verification_sha256 IS NOT NULL AND verified_at IS NOT NULL)
        OR
        (state <> 'criteria_met'
            AND verification_verifier IS NULL
            AND verification_sha256 IS NULL AND verified_at IS NULL)
    ),
    CONSTRAINT chk_agent_goal_last_transition CHECK (
        (revision = 1 AND last_transition_id IS NULL AND last_transition_hash IS NULL)
        OR
        (revision > 1 AND length(btrim(last_transition_id)) BETWEEN 1 AND 128
            AND last_transition_hash IS NOT NULL)
    ),
    CONSTRAINT chk_agent_goal_cancel CHECK (
        (cancel_requested_at IS NULL AND cancel_requested_by_user_id IS NULL)
        OR
        (cancel_requested_at IS NOT NULL AND cancel_requested_by_user_id > 0)
    ),
    CONSTRAINT chk_agent_goal_reason CHECK (
        length(btrim(reason_code)) BETWEEN 1 AND 64
        AND (state <> 'criteria_met'
             OR reason_code IN ('criteria_all_met', 'criteria_any_met'))
    )
);

CREATE TABLE IF NOT EXISTS AgentGoalRunTools (
    goal_run_id TEXT NOT NULL,
    tool_name   TEXT NOT NULL,
    PRIMARY KEY (goal_run_id, tool_name),
    CONSTRAINT fk_agent_goal_tool_run FOREIGN KEY (goal_run_id)
        REFERENCES AgentGoalRuns (goal_run_id) ON DELETE CASCADE,
    CONSTRAINT chk_agent_goal_tool_name CHECK (length(btrim(tool_name)) BETWEEN 1 AND 64)
);

CREATE TABLE IF NOT EXISTS AgentGoalRunSources (
    goal_run_id TEXT NOT NULL,
    source_id   BIGINT NOT NULL CONSTRAINT chk_agent_goal_source_id CHECK (source_id > 0),
    PRIMARY KEY (goal_run_id, source_id),
    CONSTRAINT fk_agent_goal_source_run FOREIGN KEY (goal_run_id)
        REFERENCES AgentGoalRuns (goal_run_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS AgentGoalRunWork (
    goal_run_id          TEXT NOT NULL,
    request_id           CHAR(64) NOT NULL,
    kind                 TEXT NOT NULL,
    state                TEXT NOT NULL,
    proposal_sha256      CHAR(64) NOT NULL,
    approval_id          CHAR(64),
    approval_sha256      CHAR(64),
    operation_id         CHAR(64),
    source_id            BIGINT NOT NULL,
    proposal_expires_at  TIMESTAMPTZ(6) NOT NULL,
    approval_expires_at  TIMESTAMPTZ(6),
    submit_by            TIMESTAMPTZ(6),
    accepted_at          TIMESTAMPTZ(6),
    observed_at          TIMESTAMPTZ(6),
    created_at           TIMESTAMPTZ(6) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at           TIMESTAMPTZ(6) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (goal_run_id, request_id),
    CONSTRAINT fk_agent_goal_work_run FOREIGN KEY (goal_run_id)
        REFERENCES AgentGoalRuns (goal_run_id) ON DELETE CASCADE,
    CONSTRAINT fk_agent_goal_work_source FOREIGN KEY (goal_run_id, source_id)
        REFERENCES AgentGoalRunSources (goal_run_id, source_id) ON DELETE CASCADE,
    CONSTRAINT chk_agent_goal_work_ids CHECK (
        request_id ~ '^[0-9a-f]{64}$'
        AND proposal_sha256 ~ '^[0-9a-f]{64}$'
        AND (approval_id IS NULL OR approval_id ~ '^[0-9a-f]{64}$')
        AND (approval_sha256 IS NULL OR approval_sha256 ~ '^[0-9a-f]{64}$')
        AND (operation_id IS NULL OR operation_id ~ '^[0-9a-f]{64}$')
        AND source_id > 0
    ),
    CONSTRAINT chk_agent_goal_work_state CHECK (
        (kind = 'crawl_proposal' AND state = 'awaiting_approval'
            AND approval_id IS NULL AND approval_sha256 IS NULL
            AND operation_id IS NULL AND approval_expires_at IS NULL
            AND submit_by IS NULL AND accepted_at IS NULL AND observed_at IS NULL)
        OR
        (kind = 'crawl_operation' AND state IN ('scheduled', 'processing', 'completed')
            AND approval_id IS NOT NULL AND approval_sha256 IS NOT NULL
            AND operation_id IS NOT NULL AND approval_expires_at IS NOT NULL
            AND submit_by IS NOT NULL AND accepted_at IS NOT NULL
            AND ((state = 'completed' AND observed_at IS NOT NULL)
                 OR (state <> 'completed' AND observed_at IS NULL)))
    ),
    CONSTRAINT chk_agent_goal_work_times CHECK (
        updated_at >= created_at
        AND (kind = 'crawl_proposal'
            OR (accepted_at <= updated_at
                AND accepted_at < submit_by
                AND submit_by <= proposal_expires_at
                AND submit_by <= approval_expires_at
                AND accepted_at < proposal_expires_at
                AND accepted_at < approval_expires_at
                AND (observed_at IS NULL
                    OR (observed_at >= accepted_at AND observed_at <= updated_at))))
    )
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_agent_goal_work_operation
    ON AgentGoalRunWork (goal_run_id, operation_id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_agent_goal_work_approval
    ON AgentGoalRunWork (goal_run_id, approval_id);
CREATE INDEX IF NOT EXISTS idx_agent_goal_work_state
    ON AgentGoalRunWork (goal_run_id, kind, state);

CREATE TABLE IF NOT EXISTS AgentGoalRunEvidence (
    goal_run_id      TEXT NOT NULL,
    evidence_id      TEXT NOT NULL,
    kind             TEXT NOT NULL,
    trust_class      TEXT NOT NULL,
    producer         TEXT NOT NULL,
    source_ids       JSONB NOT NULL DEFAULT '[]'::jsonb,
    observed_at      TIMESTAMPTZ(6) NOT NULL,
    expires_at       TIMESTAMPTZ(6) NOT NULL,
    payload_sha256   CHAR(64) NOT NULL,
    record_sha256    CHAR(64) NOT NULL,
    size_bytes       INTEGER NOT NULL,
    created_at       TIMESTAMPTZ(6) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (goal_run_id, evidence_id),
    CONSTRAINT fk_agent_goal_evidence_run FOREIGN KEY (goal_run_id)
        REFERENCES AgentGoalRuns (goal_run_id) ON DELETE CASCADE,
    CONSTRAINT chk_agent_goal_evidence_ids CHECK (
        evidence_id ~ '^ev_[0-9a-f]{32}$'
        AND payload_sha256 ~ '^[0-9a-f]{64}$'
        AND record_sha256 ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT chk_agent_goal_evidence_kind CHECK (
        kind IN ('source_status', 'source_metadata', 'indexed_page',
                 'scraped_field', 'artifact', 'tool_observation')
        AND trust_class IN ('system_verified', 'tool_observation')
        AND length(btrim(producer)) BETWEEN 1 AND 256
    ),
    CONSTRAINT chk_agent_goal_evidence_sources CHECK (
        jsonb_typeof(source_ids) = 'array'
        AND jsonb_array_length(source_ids) <= 32
    ),
    CONSTRAINT chk_agent_goal_evidence_times CHECK (expires_at > observed_at),
    CONSTRAINT chk_agent_goal_evidence_size CHECK (size_bytes BETWEEN 1 AND 16384)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_agent_goal_evidence_record
    ON AgentGoalRunEvidence (goal_run_id, record_sha256);
CREATE INDEX IF NOT EXISTS idx_agent_goal_evidence_observed
    ON AgentGoalRunEvidence (goal_run_id, observed_at);

CREATE TABLE IF NOT EXISTS AgentGoalRunVerificationEvidence (
    goal_run_id TEXT NOT NULL,
    evidence_id TEXT NOT NULL,
    created_at  TIMESTAMPTZ(6) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (goal_run_id, evidence_id),
    CONSTRAINT fk_agent_goal_verification_evidence FOREIGN KEY (goal_run_id, evidence_id)
        REFERENCES AgentGoalRunEvidence (goal_run_id, evidence_id) ON DELETE CASCADE,
    CONSTRAINT chk_agent_goal_verification_evidence_time CHECK (created_at IS NOT NULL)
);

CREATE TABLE IF NOT EXISTS AgentGoalRunAuditRefs (
    goal_run_id TEXT NOT NULL,
    audit_ref   TEXT NOT NULL,
    created_at  TIMESTAMPTZ(6) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (goal_run_id, audit_ref),
    CONSTRAINT fk_agent_goal_audit_run FOREIGN KEY (goal_run_id)
        REFERENCES AgentGoalRuns (goal_run_id) ON DELETE CASCADE,
    CONSTRAINT chk_agent_goal_audit_ref CHECK (length(btrim(audit_ref)) BETWEEN 1 AND 128)
);

CREATE TABLE IF NOT EXISTS AgentGoalRunTransitions (
    goal_run_id     TEXT NOT NULL,
    revision        BIGINT NOT NULL,
    operation_id    TEXT NOT NULL,
    transition_hash CHAR(64) NOT NULL,
    from_state      TEXT NOT NULL,
    to_state        TEXT NOT NULL,
    reason_code     TEXT NOT NULL,
    created_at      TIMESTAMPTZ(6) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (goal_run_id, revision),
    CONSTRAINT fk_agent_goal_transition_run FOREIGN KEY (goal_run_id)
        REFERENCES AgentGoalRuns (goal_run_id) ON DELETE CASCADE,
    CONSTRAINT chk_agent_goal_transition_ids CHECK (
        revision > 0
        AND length(btrim(operation_id)) BETWEEN 1 AND 128
        AND transition_hash ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT chk_agent_goal_transition_states CHECK (
        from_state IN ('created', 'running', 'awaiting_approval',
                       'awaiting_external_work', 'criteria_met', 'incomplete',
                       'budget_exhausted', 'cancelled', 'denied', 'failed')
        AND to_state IN ('running', 'awaiting_approval',
                         'awaiting_external_work', 'criteria_met', 'incomplete',
                         'budget_exhausted', 'cancelled', 'denied', 'failed')
        AND from_state <> to_state
    ),
    CONSTRAINT chk_agent_goal_transition_reason CHECK (
        length(btrim(reason_code)) BETWEEN 1 AND 64
    )
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_agent_goal_transition_operation
    ON AgentGoalRunTransitions (goal_run_id, operation_id);
CREATE INDEX IF NOT EXISTS idx_agent_goal_transitions_created
    ON AgentGoalRunTransitions (goal_run_id, created_at);

CREATE INDEX IF NOT EXISTS idx_agent_goal_runs_state_deadline
    ON AgentGoalRuns (state, deadline);
CREATE INDEX IF NOT EXISTS idx_agent_goal_runs_principal
    ON AgentGoalRuns (principal_user_id, state);
CREATE INDEX IF NOT EXISTS idx_agent_goal_runs_agent
    ON AgentGoalRuns (agent_id, state);
CREATE INDEX IF NOT EXISTS idx_agent_goal_runs_owner
    ON AgentGoalRuns (owner, state);
-- END AGENT GOAL PERSISTENCE (schema 1.17)

ALTER TABLE AgentGoalRuns OWNER TO :CROWLER_DB_USER;
ALTER TABLE AgentGoalRunTools OWNER TO :CROWLER_DB_USER;
ALTER TABLE AgentGoalRunSources OWNER TO :CROWLER_DB_USER;
ALTER TABLE AgentGoalRunWork OWNER TO :CROWLER_DB_USER;
ALTER TABLE AgentGoalRunEvidence OWNER TO :CROWLER_DB_USER;
ALTER TABLE AgentGoalRunVerificationEvidence OWNER TO :CROWLER_DB_USER;
ALTER TABLE AgentGoalRunAuditRefs OWNER TO :CROWLER_DB_USER;
ALTER TABLE AgentGoalRunTransitions OWNER TO :CROWLER_DB_USER;
GRANT SELECT, INSERT, UPDATE ON AgentGoalRuns TO :CROWLER_DB_USER;
GRANT SELECT, INSERT ON AgentGoalRunTools TO :CROWLER_DB_USER;
GRANT SELECT, INSERT ON AgentGoalRunSources TO :CROWLER_DB_USER;
GRANT SELECT, INSERT, UPDATE, DELETE ON AgentGoalRunWork TO :CROWLER_DB_USER;
GRANT SELECT, INSERT ON AgentGoalRunEvidence TO :CROWLER_DB_USER;
GRANT SELECT, INSERT ON AgentGoalRunVerificationEvidence TO :CROWLER_DB_USER;
GRANT SELECT, INSERT ON AgentGoalRunAuditRefs TO :CROWLER_DB_USER;
GRANT SELECT, INSERT ON AgentGoalRunTransitions TO :CROWLER_DB_USER;

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
GRANT SELECT, INSERT, UPDATE ON SourceCrawlAttempts TO :CROWLER_DB_USER;
GRANT SELECT, INSERT, UPDATE ON SourceCrawlClaims TO :CROWLER_DB_USER;
GRANT SELECT ON SourceCrawlClaimPolicy TO :CROWLER_DB_USER;
REVOKE INSERT, UPDATE, DELETE ON SourceCrawlClaimPolicy FROM :CROWLER_DB_USER;
REVOKE DELETE ON SourceCrawlAttempts, SourceCrawlClaims FROM :CROWLER_DB_USER;


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

--
-- Shared-artifact orphan cleanup (WP-REL230-RC2 / RC2-A)
--
-- A database upgraded from 1.16 keeps whatever version of
-- handle_shared_entity_deletion() it already had, because 1.17 previously only
-- defined it in the fresh-install script. Re-define it here, and create the
-- triggers if they are missing, so an upgraded database converges with a fresh
-- 1.17 install. The body locks the parent artifact row before counting links;
-- see postgresql-setup.pgsql for the concurrency rationale.
--
CREATE OR REPLACE FUNCTION handle_shared_entity_deletion()
RETURNS TRIGGER AS $$
BEGIN
    IF TG_TABLE_NAME = 'metatagsindex' THEN
        PERFORM 1 FROM MetaTags WHERE metatag_id = OLD.metatag_id FOR UPDATE;
        IF FOUND THEN
            IF (SELECT COUNT(*) FROM MetaTagsIndex WHERE metatag_id = OLD.metatag_id) = 0 THEN
                DELETE FROM MetaTags WHERE metatag_id = OLD.metatag_id;
            END IF;
        END IF;
    ELSIF TG_TABLE_NAME = 'webobjectsindex' THEN
        PERFORM 1 FROM WebObjects WHERE object_id = OLD.object_id FOR UPDATE;
        IF FOUND THEN
            IF (SELECT COUNT(*) FROM WebObjectsIndex WHERE object_id = OLD.object_id) = 0 THEN
                DELETE FROM WebObjects WHERE object_id = OLD.object_id;
            END IF;
        END IF;
    ELSIF TG_TABLE_NAME = 'keywordindex' THEN
        PERFORM 1 FROM Keywords WHERE keyword_id = OLD.keyword_id FOR UPDATE;
        IF FOUND THEN
            IF (SELECT COUNT(*) FROM KeywordIndex WHERE keyword_id = OLD.keyword_id) = 0 THEN
                DELETE FROM Keywords WHERE keyword_id = OLD.keyword_id;
            END IF;
        END IF;
    ELSIF TG_TABLE_NAME = 'netinfoindex' THEN
        PERFORM 1 FROM NetInfo WHERE netinfo_id = OLD.netinfo_id FOR UPDATE;
        IF FOUND THEN
            IF (SELECT COUNT(*) FROM NetInfoIndex WHERE netinfo_id = OLD.netinfo_id) = 0 THEN
                DELETE FROM NetInfo WHERE netinfo_id = OLD.netinfo_id;
            END IF;
        END IF;
    ELSIF TG_TABLE_NAME = 'httpinfoindex' THEN
        PERFORM 1 FROM HTTPInfo WHERE httpinfo_id = OLD.httpinfo_id FOR UPDATE;
        IF FOUND THEN
            IF (SELECT COUNT(*) FROM HTTPInfoIndex WHERE httpinfo_id = OLD.httpinfo_id) = 0 THEN
                DELETE FROM HTTPInfo WHERE httpinfo_id = OLD.httpinfo_id;
            END IF;
        END IF;
    END IF;
    RETURN OLD;
END;
$$ LANGUAGE plpgsql;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_after_delete_metatagsindex') THEN
        CREATE TRIGGER trg_after_delete_metatagsindex
        AFTER DELETE ON MetaTagsIndex
        FOR EACH ROW
        EXECUTE FUNCTION handle_shared_entity_deletion();
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_after_delete_webobjectsindex') THEN
        CREATE TRIGGER trg_after_delete_webobjectsindex
        AFTER DELETE ON WebObjectsIndex
        FOR EACH ROW
        EXECUTE FUNCTION handle_shared_entity_deletion();
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_after_delete_keywordindex') THEN
        CREATE TRIGGER trg_after_delete_keywordindex
        AFTER DELETE ON KeywordIndex
        FOR EACH ROW
        EXECUTE FUNCTION handle_shared_entity_deletion();
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_after_delete_netinfoindex') THEN
        CREATE TRIGGER trg_after_delete_netinfoindex
        AFTER DELETE ON NetInfoIndex
        FOR EACH ROW
        EXECUTE FUNCTION handle_shared_entity_deletion();
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_after_delete_httpinfoindex') THEN
        CREATE TRIGGER trg_after_delete_httpinfoindex
        AFTER DELETE ON HTTPInfoIndex
        FOR EACH ROW
        EXECUTE FUNCTION handle_shared_entity_deletion();
    END IF;
END
$$;

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
