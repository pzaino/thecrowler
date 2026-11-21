-- Optional CROWler 1.17 add-on: read docs/database-migrations.md before running.
-- Asynchronous agent-goal tables. Apply AFTER the base 1.17 migration, and
-- only when the capability is absent (development databases already at 1.17).
-- This file uses the psql token :CROWLER_DB_USER; a GUI or raw-SQL client
-- needs the prepared copy from scripts/prepare-postgresql-migration.sh.

-- Additive async-goal persistence for development databases already stamped 1.17.
-- This script does not change DBSchemaVersion or DatabaseWriterCompatibility.
-- It is manual and opt-in; it is not applied automatically at startup.
--
-- Run with:
--   psql -v CROWLER_DB_USER=crowler -f postgresql-agent-goal-v1.17.pgsql
BEGIN;
SELECT pg_advisory_xact_lock(hashtext('crowler:async-goal-persistence-v1'));

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

-- Fail closed if an existing same-named object is structurally incompatible.
DO $$
DECLARE
    missing TEXT;
    current_version TEXT;
    current_generation TEXT;
BEGIN
    SELECT version INTO current_version FROM DBSchemaVersion WHERE is_current;
    IF current_version IS DISTINCT FROM '1.17' THEN
        RAISE EXCEPTION 'async goal schema requires database schema 1.17';
    END IF;
    SELECT writer_generation INTO current_generation
      FROM DatabaseWriterCompatibility WHERE lock_id = 1;
    IF current_generation IS DISTINCT FROM 'verified-ownership-v1.17' THEN
        RAISE EXCEPTION 'async goal schema requires writer generation verified-ownership-v1.17';
    END IF;

    SELECT string_agg(e.t || '.' || e.c, ', ' ORDER BY e.t, e.c)
      INTO missing
      FROM (VALUES
        ('AgentGoalRuns','goal_run_id'),
        ('AgentGoalRuns','checkpoint_version'),
        ('AgentGoalRuns','contract_version'),
        ('AgentGoalRuns','revision'),
        ('AgentGoalRuns','trace_id'),
        ('AgentGoalRuns','correlation_id'),
        ('AgentGoalRuns','agent_id'),
        ('AgentGoalRuns','owner'),
        ('AgentGoalRuns','principal_kind'),
        ('AgentGoalRuns','principal_user_id'),
        ('AgentGoalRuns','principal_username'),
        ('AgentGoalRuns','principal_issuer'),
        ('AgentGoalRuns','goal_id'),
        ('AgentGoalRuns','goal_spec_hash'),
        ('AgentGoalRuns','policy_sha256'),
        ('AgentGoalRuns','state'),
        ('AgentGoalRuns','slices'),
        ('AgentGoalRuns','created_at'),
        ('AgentGoalRuns','updated_at'),
        ('AgentGoalRuns','deadline'),
        ('AgentGoalRuns','reason_code'),
        ('AgentGoalRuns','ledger_id'),
        ('AgentGoalRuns','run_nonce_hash'),
        ('AgentGoalRuns','last_transition_id'),
        ('AgentGoalRuns','last_transition_hash'),
        ('AgentGoalRuns','verification_verifier'),
        ('AgentGoalRuns','verification_sha256'),
        ('AgentGoalRuns','verified_at'),
        ('AgentGoalRuns','cancel_requested_at'),
        ('AgentGoalRuns','cancel_requested_by_user_id'),
        ('AgentGoalRuns','max_slices'),
        ('AgentGoalRuns','max_model_rounds'),
        ('AgentGoalRuns','max_tool_attempts'),
        ('AgentGoalRuns','max_result_bytes'),
        ('AgentGoalRuns','max_evidence_records'),
        ('AgentGoalRuns','max_evidence_bytes'),
        ('AgentGoalRuns','max_retries'),
        ('AgentGoalRuns','max_scheduled_work_requests'),
        ('AgentGoalRuns','max_consecutive_denials'),
        ('AgentGoalRuns','max_slice_elapsed_ns'),
        ('AgentGoalRuns','used_model_rounds'),
        ('AgentGoalRuns','used_tool_attempts'),
        ('AgentGoalRuns','used_result_bytes'),
        ('AgentGoalRuns','used_evidence_records'),
        ('AgentGoalRuns','used_evidence_bytes'),
        ('AgentGoalRuns','used_retries'),
        ('AgentGoalRuns','used_scheduled_work_requests'),
        ('AgentGoalRuns','used_consecutive_denials'),
        ('AgentGoalRunTools','goal_run_id'),
        ('AgentGoalRunTools','tool_name'),
        ('AgentGoalRunSources','goal_run_id'),
        ('AgentGoalRunSources','source_id'),
        ('AgentGoalRunWork','goal_run_id'),
        ('AgentGoalRunWork','request_id'),
        ('AgentGoalRunWork','kind'),
        ('AgentGoalRunWork','state'),
        ('AgentGoalRunWork','proposal_sha256'),
        ('AgentGoalRunWork','approval_id'),
        ('AgentGoalRunWork','approval_sha256'),
        ('AgentGoalRunWork','operation_id'),
        ('AgentGoalRunWork','source_id'),
        ('AgentGoalRunWork','proposal_expires_at'),
        ('AgentGoalRunWork','approval_expires_at'),
        ('AgentGoalRunWork','submit_by'),
        ('AgentGoalRunWork','accepted_at'),
        ('AgentGoalRunWork','observed_at'),
        ('AgentGoalRunWork','created_at'),
        ('AgentGoalRunWork','updated_at'),
        ('AgentGoalRunEvidence','goal_run_id'),
        ('AgentGoalRunEvidence','evidence_id'),
        ('AgentGoalRunEvidence','kind'),
        ('AgentGoalRunEvidence','trust_class'),
        ('AgentGoalRunEvidence','producer'),
        ('AgentGoalRunEvidence','source_ids'),
        ('AgentGoalRunEvidence','observed_at'),
        ('AgentGoalRunEvidence','expires_at'),
        ('AgentGoalRunEvidence','payload_sha256'),
        ('AgentGoalRunEvidence','record_sha256'),
        ('AgentGoalRunEvidence','size_bytes'),
        ('AgentGoalRunEvidence','created_at'),
        ('AgentGoalRunVerificationEvidence','goal_run_id'),
        ('AgentGoalRunVerificationEvidence','evidence_id'),
        ('AgentGoalRunVerificationEvidence','created_at'),
        ('AgentGoalRunAuditRefs','goal_run_id'),
        ('AgentGoalRunAuditRefs','audit_ref'),
        ('AgentGoalRunAuditRefs','created_at'),
        ('AgentGoalRunTransitions','goal_run_id'),
        ('AgentGoalRunTransitions','revision'),
        ('AgentGoalRunTransitions','operation_id'),
        ('AgentGoalRunTransitions','transition_hash'),
        ('AgentGoalRunTransitions','from_state'),
        ('AgentGoalRunTransitions','to_state'),
        ('AgentGoalRunTransitions','reason_code'),
        ('AgentGoalRunTransitions','created_at')
      ) AS e(t, c)
      LEFT JOIN information_schema.columns c
        ON c.table_schema = current_schema()
       AND lower(c.table_name) = lower(e.t)
       AND c.column_name = e.c
     WHERE c.column_name IS NULL;
    IF missing IS NOT NULL THEN
        RAISE EXCEPTION 'async goal schema is missing columns: %', missing;
    END IF;

    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = current_schema()
          AND (lower(table_name), lower(column_name)) IN (
        ('AgentGoalRuns','payload'),
        ('AgentGoalRuns','raw_payload'),
        ('AgentGoalRuns','prompt'),
        ('AgentGoalRuns','prompts'),
        ('AgentGoalRuns','messages'),
        ('AgentGoalRuns','tool_arguments'),
        ('AgentGoalRuns','arguments'),
        ('AgentGoalRuns','raw_result'),
        ('AgentGoalRuns','result'),
        ('AgentGoalRuns','token'),
        ('AgentGoalRuns','password'),
        ('AgentGoalRuns','secret'),
        ('AgentGoalRuns','dsn'),
        ('AgentGoalRuns','cookie'),
        ('AgentGoalRuns','cookies'),
        ('AgentGoalRuns','request_body'),
        ('AgentGoalRuns','response_body'),
        ('AgentGoalRuns','model_text'),
        ('AgentGoalRunTools','payload'),
        ('AgentGoalRunTools','raw_payload'),
        ('AgentGoalRunTools','prompt'),
        ('AgentGoalRunTools','prompts'),
        ('AgentGoalRunTools','messages'),
        ('AgentGoalRunTools','tool_arguments'),
        ('AgentGoalRunTools','arguments'),
        ('AgentGoalRunTools','raw_result'),
        ('AgentGoalRunTools','result'),
        ('AgentGoalRunTools','token'),
        ('AgentGoalRunTools','password'),
        ('AgentGoalRunTools','secret'),
        ('AgentGoalRunTools','dsn'),
        ('AgentGoalRunTools','cookie'),
        ('AgentGoalRunTools','cookies'),
        ('AgentGoalRunTools','request_body'),
        ('AgentGoalRunTools','response_body'),
        ('AgentGoalRunTools','model_text'),
        ('AgentGoalRunSources','payload'),
        ('AgentGoalRunSources','raw_payload'),
        ('AgentGoalRunSources','prompt'),
        ('AgentGoalRunSources','prompts'),
        ('AgentGoalRunSources','messages'),
        ('AgentGoalRunSources','tool_arguments'),
        ('AgentGoalRunSources','arguments'),
        ('AgentGoalRunSources','raw_result'),
        ('AgentGoalRunSources','result'),
        ('AgentGoalRunSources','token'),
        ('AgentGoalRunSources','password'),
        ('AgentGoalRunSources','secret'),
        ('AgentGoalRunSources','dsn'),
        ('AgentGoalRunSources','cookie'),
        ('AgentGoalRunSources','cookies'),
        ('AgentGoalRunSources','request_body'),
        ('AgentGoalRunSources','response_body'),
        ('AgentGoalRunSources','model_text'),
        ('AgentGoalRunWork','payload'),
        ('AgentGoalRunWork','raw_payload'),
        ('AgentGoalRunWork','prompt'),
        ('AgentGoalRunWork','prompts'),
        ('AgentGoalRunWork','messages'),
        ('AgentGoalRunWork','tool_arguments'),
        ('AgentGoalRunWork','arguments'),
        ('AgentGoalRunWork','raw_result'),
        ('AgentGoalRunWork','result'),
        ('AgentGoalRunWork','token'),
        ('AgentGoalRunWork','password'),
        ('AgentGoalRunWork','secret'),
        ('AgentGoalRunWork','dsn'),
        ('AgentGoalRunWork','cookie'),
        ('AgentGoalRunWork','cookies'),
        ('AgentGoalRunWork','request_body'),
        ('AgentGoalRunWork','response_body'),
        ('AgentGoalRunWork','model_text'),
        ('AgentGoalRunEvidence','payload'),
        ('AgentGoalRunEvidence','raw_payload'),
        ('AgentGoalRunEvidence','prompt'),
        ('AgentGoalRunEvidence','prompts'),
        ('AgentGoalRunEvidence','messages'),
        ('AgentGoalRunEvidence','tool_arguments'),
        ('AgentGoalRunEvidence','arguments'),
        ('AgentGoalRunEvidence','raw_result'),
        ('AgentGoalRunEvidence','result'),
        ('AgentGoalRunEvidence','token'),
        ('AgentGoalRunEvidence','password'),
        ('AgentGoalRunEvidence','secret'),
        ('AgentGoalRunEvidence','dsn'),
        ('AgentGoalRunEvidence','cookie'),
        ('AgentGoalRunEvidence','cookies'),
        ('AgentGoalRunEvidence','request_body'),
        ('AgentGoalRunEvidence','response_body'),
        ('AgentGoalRunEvidence','model_text'),
        ('AgentGoalRunVerificationEvidence','payload'),
        ('AgentGoalRunVerificationEvidence','raw_payload'),
        ('AgentGoalRunVerificationEvidence','prompt'),
        ('AgentGoalRunVerificationEvidence','prompts'),
        ('AgentGoalRunVerificationEvidence','messages'),
        ('AgentGoalRunVerificationEvidence','tool_arguments'),
        ('AgentGoalRunVerificationEvidence','arguments'),
        ('AgentGoalRunVerificationEvidence','raw_result'),
        ('AgentGoalRunVerificationEvidence','result'),
        ('AgentGoalRunVerificationEvidence','token'),
        ('AgentGoalRunVerificationEvidence','password'),
        ('AgentGoalRunVerificationEvidence','secret'),
        ('AgentGoalRunVerificationEvidence','dsn'),
        ('AgentGoalRunVerificationEvidence','cookie'),
        ('AgentGoalRunVerificationEvidence','cookies'),
        ('AgentGoalRunVerificationEvidence','request_body'),
        ('AgentGoalRunVerificationEvidence','response_body'),
        ('AgentGoalRunVerificationEvidence','model_text'),
        ('AgentGoalRunAuditRefs','payload'),
        ('AgentGoalRunAuditRefs','raw_payload'),
        ('AgentGoalRunAuditRefs','prompt'),
        ('AgentGoalRunAuditRefs','prompts'),
        ('AgentGoalRunAuditRefs','messages'),
        ('AgentGoalRunAuditRefs','tool_arguments'),
        ('AgentGoalRunAuditRefs','arguments'),
        ('AgentGoalRunAuditRefs','raw_result'),
        ('AgentGoalRunAuditRefs','result'),
        ('AgentGoalRunAuditRefs','token'),
        ('AgentGoalRunAuditRefs','password'),
        ('AgentGoalRunAuditRefs','secret'),
        ('AgentGoalRunAuditRefs','dsn'),
        ('AgentGoalRunAuditRefs','cookie'),
        ('AgentGoalRunAuditRefs','cookies'),
        ('AgentGoalRunAuditRefs','request_body'),
        ('AgentGoalRunAuditRefs','response_body'),
        ('AgentGoalRunAuditRefs','model_text'),
        ('AgentGoalRunTransitions','payload'),
        ('AgentGoalRunTransitions','raw_payload'),
        ('AgentGoalRunTransitions','prompt'),
        ('AgentGoalRunTransitions','prompts'),
        ('AgentGoalRunTransitions','messages'),
        ('AgentGoalRunTransitions','tool_arguments'),
        ('AgentGoalRunTransitions','arguments'),
        ('AgentGoalRunTransitions','raw_result'),
        ('AgentGoalRunTransitions','result'),
        ('AgentGoalRunTransitions','token'),
        ('AgentGoalRunTransitions','password'),
        ('AgentGoalRunTransitions','secret'),
        ('AgentGoalRunTransitions','dsn'),
        ('AgentGoalRunTransitions','cookie'),
        ('AgentGoalRunTransitions','cookies'),
        ('AgentGoalRunTransitions','request_body'),
        ('AgentGoalRunTransitions','response_body'),
        ('AgentGoalRunTransitions','model_text')
          )
    ) THEN
        RAISE EXCEPTION 'async goal schema contains a forbidden raw-content column';
    END IF;

    IF EXISTS (
        SELECT 1 FROM (VALUES
        ('idx_agent_goal_runs_state_deadline'),
        ('idx_agent_goal_runs_principal'),
        ('idx_agent_goal_runs_agent'),
        ('idx_agent_goal_runs_owner'),
        ('idx_agent_goal_work_state'),
        ('idx_agent_goal_evidence_observed'),
        ('idx_agent_goal_transitions_created'),
        ('uq_agent_goal_work_operation'),
        ('uq_agent_goal_work_approval'),
        ('uq_agent_goal_evidence_record'),
        ('uq_agent_goal_transition_operation')
        ) AS e(n)
        LEFT JOIN pg_indexes p
          ON p.schemaname = current_schema() AND p.indexname = e.n
        WHERE p.indexname IS NULL
    ) THEN
        RAISE EXCEPTION 'async goal schema is missing a required index';
    END IF;

  IF NOT EXISTS (
      SELECT 1 FROM pg_indexes
      WHERE schemaname = current_schema()
        AND indexname = 'uq_agent_goal_work_operation'
        AND indexdef ILIKE 'CREATE UNIQUE INDEX%'
        AND position('(goal_run_id, operation_id)' in indexdef) > 0
  ) THEN
      RAISE EXCEPTION 'async goal schema has incompatible or missing unique index uq_agent_goal_work_operation';
  END IF;
  IF NOT EXISTS (
      SELECT 1 FROM pg_indexes
      WHERE schemaname = current_schema()
        AND indexname = 'uq_agent_goal_work_approval'
        AND indexdef ILIKE 'CREATE UNIQUE INDEX%'
        AND position('(goal_run_id, approval_id)' in indexdef) > 0
  ) THEN
      RAISE EXCEPTION 'async goal schema has incompatible or missing unique index uq_agent_goal_work_approval';
  END IF;
  IF NOT EXISTS (
      SELECT 1 FROM pg_indexes
      WHERE schemaname = current_schema()
        AND indexname = 'uq_agent_goal_evidence_record'
        AND indexdef ILIKE 'CREATE UNIQUE INDEX%'
        AND position('(goal_run_id, record_sha256)' in indexdef) > 0
  ) THEN
      RAISE EXCEPTION 'async goal schema has incompatible or missing unique index uq_agent_goal_evidence_record';
  END IF;
  IF NOT EXISTS (
      SELECT 1 FROM pg_indexes
      WHERE schemaname = current_schema()
        AND indexname = 'uq_agent_goal_transition_operation'
        AND indexdef ILIKE 'CREATE UNIQUE INDEX%'
        AND position('(goal_run_id, operation_id)' in indexdef) > 0
  ) THEN
      RAISE EXCEPTION 'async goal schema has incompatible or missing unique index uq_agent_goal_transition_operation';
  END IF;

    IF EXISTS (
        SELECT 1 FROM (VALUES
        ('AgentGoalRunTools','fk_agent_goal_tool_run','AgentGoalRuns','goal_run_id','goal_run_id'),
        ('AgentGoalRunSources','fk_agent_goal_source_run','AgentGoalRuns','goal_run_id','goal_run_id'),
        ('AgentGoalRunWork','fk_agent_goal_work_run','AgentGoalRuns','goal_run_id','goal_run_id'),
        ('AgentGoalRunWork','fk_agent_goal_work_source','AgentGoalRunSources','goal_run_id, source_id','goal_run_id, source_id'),
        ('AgentGoalRunEvidence','fk_agent_goal_evidence_run','AgentGoalRuns','goal_run_id','goal_run_id'),
        ('AgentGoalRunVerificationEvidence','fk_agent_goal_verification_evidence','AgentGoalRunEvidence','goal_run_id, evidence_id','goal_run_id, evidence_id'),
        ('AgentGoalRunAuditRefs','fk_agent_goal_audit_run','AgentGoalRuns','goal_run_id','goal_run_id'),
        ('AgentGoalRunTransitions','fk_agent_goal_transition_run','AgentGoalRuns','goal_run_id','goal_run_id')
        ) AS e(t, n, p, c, pc)
        LEFT JOIN pg_namespace ns ON ns.nspname = current_schema()
        LEFT JOIN pg_class r
          ON r.relnamespace = ns.oid AND lower(r.relname) = lower(e.t)
        LEFT JOIN pg_constraint c
          ON c.conrelid = r.oid AND c.conname = e.n AND c.contype = 'f'
        WHERE c.oid IS NULL
           OR position('foreign key (' || e.c || ')' in lower(pg_get_constraintdef(c.oid))) = 0
           OR position(lower(e.p) || '(' || e.pc || ')' in lower(pg_get_constraintdef(c.oid))) = 0
           OR position('on delete cascade' in lower(pg_get_constraintdef(c.oid))) = 0
    ) THEN
        RAISE EXCEPTION 'async goal schema is missing or has an incompatible foreign key';
    END IF;

    IF EXISTS (
        SELECT 1 FROM (VALUES
        ('AgentGoalRunAuditRefs',1),
        ('AgentGoalRunEvidence',1),
        ('AgentGoalRunSources',1),
        ('AgentGoalRunTools',1),
        ('AgentGoalRunTransitions',1),
        ('AgentGoalRunVerificationEvidence',1),
        ('AgentGoalRunWork',2)
        ) AS e(t, n)
        LEFT JOIN pg_namespace ns ON ns.nspname = current_schema()
        LEFT JOIN pg_class r
          ON r.relnamespace = ns.oid AND lower(r.relname) = lower(e.t)
        WHERE (SELECT count(*) FROM pg_constraint c
                WHERE c.conrelid = r.oid AND c.contype = 'f') <> e.n
    ) THEN
        RAISE EXCEPTION 'async goal schema has an unexpected foreign key set';
    END IF;

    IF EXISTS (
        SELECT 1 FROM (VALUES
        ('AgentGoalRuns','chk_agent_goal_versions'),
        ('AgentGoalRuns','chk_agent_goal_revision'),
        ('AgentGoalRuns','chk_agent_goal_state'),
        ('AgentGoalRuns','chk_agent_goal_principal'),
        ('AgentGoalRuns','chk_agent_goal_identity'),
        ('AgentGoalRuns','chk_agent_goal_hashes'),
        ('AgentGoalRuns','chk_agent_goal_timestamps'),
        ('AgentGoalRuns','chk_agent_goal_active_deadline'),
        ('AgentGoalRuns','chk_agent_goal_ceilings'),
        ('AgentGoalRuns','chk_agent_goal_used'),
        ('AgentGoalRuns','chk_agent_goal_verification'),
        ('AgentGoalRuns','chk_agent_goal_last_transition'),
        ('AgentGoalRuns','chk_agent_goal_cancel'),
        ('AgentGoalRuns','chk_agent_goal_reason'),
        ('AgentGoalRunTools','chk_agent_goal_tool_name'),
        ('AgentGoalRunSources','chk_agent_goal_source_id'),
        ('AgentGoalRunWork','chk_agent_goal_work_ids'),
        ('AgentGoalRunWork','chk_agent_goal_work_state'),
        ('AgentGoalRunWork','chk_agent_goal_work_times'),
        ('AgentGoalRunEvidence','chk_agent_goal_evidence_ids'),
        ('AgentGoalRunEvidence','chk_agent_goal_evidence_kind'),
        ('AgentGoalRunEvidence','chk_agent_goal_evidence_sources'),
        ('AgentGoalRunEvidence','chk_agent_goal_evidence_times'),
        ('AgentGoalRunEvidence','chk_agent_goal_evidence_size'),
        ('AgentGoalRunVerificationEvidence','chk_agent_goal_verification_evidence_time'),
        ('AgentGoalRunAuditRefs','chk_agent_goal_audit_ref'),
        ('AgentGoalRunTransitions','chk_agent_goal_transition_ids'),
        ('AgentGoalRunTransitions','chk_agent_goal_transition_states'),
        ('AgentGoalRunTransitions','chk_agent_goal_transition_reason')
        ) AS e(t, n)
        LEFT JOIN pg_namespace ns ON ns.nspname = current_schema()
        LEFT JOIN pg_class r
          ON r.relnamespace = ns.oid AND lower(r.relname) = lower(e.t)
        LEFT JOIN pg_constraint c
          ON c.conrelid = r.oid AND c.conname = e.n AND c.contype = 'c'
        WHERE c.oid IS NULL
    ) THEN
        RAISE EXCEPTION 'async goal schema is missing or has an incompatible check constraint';
    END IF;

    IF EXISTS (
        SELECT 1 FROM (VALUES
        ('AgentGoalRuns'),
        ('AgentGoalRunTools'),
        ('AgentGoalRunSources'),
        ('AgentGoalRunWork'),
        ('AgentGoalRunEvidence'),
        ('AgentGoalRunVerificationEvidence'),
        ('AgentGoalRunAuditRefs'),
        ('AgentGoalRunTransitions')
        ) AS e(t)
        LEFT JOIN pg_constraint c
          ON c.conrelid = (quote_ident(current_schema()) || '.' || quote_ident(lower(e.t)))::regclass
         AND c.contype = 'p'
        WHERE c.oid IS NULL
    ) THEN
        RAISE EXCEPTION 'async goal schema is missing a primary key';
    END IF;
END $$;

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

COMMIT;
