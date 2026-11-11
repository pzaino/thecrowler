-- WP-04C2-B additive MCP mutation persistence capability.
--
-- This migration is additive within schema 1.17. It does not change the schema
-- version or the writer generation. It is safe to run repeatedly.
--
-- Run with: psql -v CROWLER_DB_USER=crowler -f postgresql-mcp-mutation-v1.17.pgsql
BEGIN;

SELECT pg_advisory_xact_lock(hashtext('crowler:mcp-mutation-authorization-v1'));

CREATE TABLE IF NOT EXISTS MCPMutationProposals (
    proposal_id             CHAR(64) PRIMARY KEY,
    proposal_sha256         CHAR(64) NOT NULL UNIQUE,
    identity_version        INTEGER NOT NULL CHECK (identity_version >= 1),

    run_id                  TEXT NOT NULL,
    trace_id                TEXT NOT NULL DEFAULT '',
    effective_call_id       TEXT NOT NULL,
    agent_id                TEXT NOT NULL,
    owner                   TEXT NOT NULL,

    principal_user_id       BIGINT NOT NULL CHECK (principal_user_id > 0),
    principal_username      TEXT NOT NULL,
    principal_issuer        TEXT NOT NULL,

    server_id               TEXT NOT NULL,
    raw_tool_name           TEXT NOT NULL,
    tool_alias              TEXT NOT NULL,
    transport               TEXT NOT NULL CHECK (transport IN ('stdio', 'streamable_http')),
    transport_identity      TEXT NOT NULL,
    schema_sha256           CHAR(64) NOT NULL,

    canonical_arguments     BYTEA NOT NULL,
    arguments_sha256        CHAR(64) NOT NULL,
    policy_id               TEXT NOT NULL,
    policy_version          TEXT NOT NULL,
    policy_sha256           CHAR(64) NOT NULL,
    policy_snapshot         JSONB NOT NULL,

    source_ids              BIGINT[] NOT NULL DEFAULT '{}',
    external_resource       TEXT,
    destructive             BOOLEAN NOT NULL DEFAULT FALSE,
    approval_required       BOOLEAN NOT NULL DEFAULT TRUE,
    execution_backend       TEXT NOT NULL DEFAULT 'none'
                            CHECK (execution_backend IN ('none', 'single_submission')),

    state                   TEXT NOT NULL CHECK (state IN (
        'proposed', 'approval_required', 'approved', 'denied', 'expired',
        'dispatching', 'succeeded', 'failed', 'uncertain',
        'reconciliation_required')),

    created_at              TIMESTAMPTZ NOT NULL,
    last_updated_at         TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at              TIMESTAMPTZ NOT NULL,
    approved_at             TIMESTAMPTZ,
    terminal_at             TIMESTAMPTZ,
    terminal_reason         TEXT,

    CONSTRAINT uq_mcp_proposal_logical_call UNIQUE (run_id, effective_call_id),
    CONSTRAINT chk_mcp_proposal_hashes CHECK (
        proposal_id ~ '^[0-9a-f]{64}$' AND
        proposal_sha256 ~ '^[0-9a-f]{64}$' AND
        schema_sha256 ~ '^[0-9a-f]{64}$' AND
        arguments_sha256 ~ '^[0-9a-f]{64}$' AND
        policy_sha256 ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT chk_mcp_proposal_canonical_bytes CHECK (
        octet_length(canonical_arguments) BETWEEN 2 AND 1048576
    ),
    CONSTRAINT chk_mcp_proposal_policy_snapshot CHECK (
        jsonb_typeof(policy_snapshot) = 'object'
    ),
    CONSTRAINT chk_mcp_proposal_scope CHECK (
        (cardinality(source_ids) > 0 AND external_resource IS NULL) OR
        (cardinality(source_ids) = 0 AND external_resource IS NOT NULL)
    ),
    CONSTRAINT chk_mcp_proposal_source_ids CHECK (0 <> ALL (source_ids)),
    CONSTRAINT chk_mcp_proposal_expiry CHECK (expires_at > created_at),
    CONSTRAINT chk_mcp_proposal_approval_time CHECK (
        approved_at IS NULL OR approved_at >= created_at
    ),
    CONSTRAINT chk_mcp_proposal_terminal_time CHECK (
        (state IN ('denied', 'expired', 'succeeded', 'failed') AND terminal_at IS NOT NULL) OR
        (state NOT IN ('denied', 'expired', 'succeeded', 'failed') AND terminal_at IS NULL)
    )
);

CREATE INDEX IF NOT EXISTS idx_mcp_proposals_pending
    ON MCPMutationProposals (state, expires_at);
CREATE INDEX IF NOT EXISTS idx_mcp_proposals_principal
    ON MCPMutationProposals (principal_user_id, state, created_at);
CREATE INDEX IF NOT EXISTS idx_mcp_proposals_run
    ON MCPMutationProposals (run_id, state);
CREATE INDEX IF NOT EXISTS idx_mcp_proposals_expiry
    ON MCPMutationProposals (expires_at)
    WHERE state IN ('proposed', 'approval_required', 'approved', 'dispatching',
                    'uncertain', 'reconciliation_required');

CREATE TABLE IF NOT EXISTS MCPMutationApprovals (
    approval_id             CHAR(64) PRIMARY KEY,
    proposal_id             CHAR(64) NOT NULL
                            REFERENCES MCPMutationProposals (proposal_id)
                            ON DELETE RESTRICT,
    proposal_sha256         CHAR(64) NOT NULL
                            REFERENCES MCPMutationProposals (proposal_sha256)
                            ON DELETE RESTRICT,

    run_id                  TEXT NOT NULL,
    agent_id                TEXT NOT NULL,
    server_id               TEXT NOT NULL,
    tool_alias              TEXT NOT NULL,
    arguments_sha256        CHAR(64) NOT NULL,
    policy_sha256           CHAR(64) NOT NULL,

    approved_by_user_id     BIGINT NOT NULL CHECK (approved_by_user_id > 0),
    approved_by             TEXT NOT NULL,
    approved_by_issuer      TEXT NOT NULL,

    approved_at             TIMESTAMPTZ NOT NULL,
    expires_at              TIMESTAMPTZ NOT NULL,
    state                   TEXT NOT NULL DEFAULT 'active'
                            CHECK (state IN ('active', 'revoked', 'expired', 'exhausted')),

    revoked_at              TIMESTAMPTZ,
    revoked_by_user_id      BIGINT,
    revoked_by              TEXT,
    revocation_reason       TEXT,

    max_executions          INTEGER NOT NULL CHECK (max_executions > 0),
    executions_started      INTEGER NOT NULL DEFAULT 0 CHECK (executions_started >= 0),
    last_updated_at         TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT chk_mcp_approval_hashes CHECK (
        approval_id ~ '^[0-9a-f]{64}$' AND
        proposal_sha256 ~ '^[0-9a-f]{64}$' AND
        arguments_sha256 ~ '^[0-9a-f]{64}$' AND
        policy_sha256 ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT chk_mcp_approval_expiry CHECK (expires_at > approved_at),
    CONSTRAINT chk_mcp_approval_executions CHECK (executions_started <= max_executions),
    CONSTRAINT chk_mcp_approval_revocation CHECK (
        (state = 'revoked' AND revoked_at IS NOT NULL AND revoked_by_user_id IS NOT NULL) OR
        (state <> 'revoked' AND revoked_at IS NULL)
    ),
    CONSTRAINT chk_mcp_approval_exhausted CHECK (
        state <> 'exhausted' OR executions_started = max_executions
    )
);

CREATE INDEX IF NOT EXISTS idx_mcp_approvals_pending
    ON MCPMutationApprovals (state, expires_at);
CREATE INDEX IF NOT EXISTS idx_mcp_approvals_proposal
    ON MCPMutationApprovals (proposal_sha256, approved_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS uq_mcp_approvals_one_active
    ON MCPMutationApprovals (proposal_sha256)
    WHERE state = 'active';

CREATE TABLE IF NOT EXISTS MCPMutationDispatchAttempts (
    attempt_id              CHAR(64) PRIMARY KEY,
    proposal_id             CHAR(64) NOT NULL
                            REFERENCES MCPMutationProposals (proposal_id)
                            ON DELETE RESTRICT,
    proposal_sha256         CHAR(64) NOT NULL
                            REFERENCES MCPMutationProposals (proposal_sha256)
                            ON DELETE RESTRICT,
    approval_id             CHAR(64) NOT NULL
                            REFERENCES MCPMutationApprovals (approval_id)
                            ON DELETE RESTRICT,

    submission_identity     CHAR(64) NOT NULL,
    claimed_by              TEXT NOT NULL,
    claimed_at              TIMESTAMPTZ NOT NULL,
    lease_expires_at        TIMESTAMPTZ NOT NULL,
    backend_identity        TEXT NOT NULL,
    external_operation_id   TEXT,
    reconciliation_key      TEXT,

    state                   TEXT NOT NULL CHECK (state IN (
        'dispatching', 'succeeded', 'failed', 'uncertain',
        'reconciliation_required')),
    response_digest         CHAR(64),
    reason_code             TEXT,
    terminal_at             TIMESTAMPTZ,
    last_updated_at         TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT uq_mcp_attempt_submission UNIQUE (proposal_sha256, submission_identity),
    CONSTRAINT chk_mcp_attempt_hashes CHECK (
        attempt_id ~ '^[0-9a-f]{64}$' AND
        proposal_sha256 ~ '^[0-9a-f]{64}$' AND
        submission_identity ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT chk_mcp_attempt_lease CHECK (lease_expires_at > claimed_at),
    CONSTRAINT chk_mcp_attempt_terminal_time CHECK (
        (state IN ('succeeded', 'failed') AND terminal_at IS NOT NULL) OR
        (state NOT IN ('succeeded', 'failed') AND terminal_at IS NULL)
    )
);

CREATE INDEX IF NOT EXISTS idx_mcp_attempts_reconciliation
    ON MCPMutationDispatchAttempts (state, lease_expires_at);
CREATE INDEX IF NOT EXISTS idx_mcp_attempts_proposal
    ON MCPMutationDispatchAttempts (proposal_sha256, claimed_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS uq_mcp_attempts_one_unresolved
    ON MCPMutationDispatchAttempts (proposal_sha256)
    WHERE state IN ('dispatching', 'uncertain', 'reconciliation_required');

CREATE TABLE IF NOT EXISTS MCPMutationAudit (
    event_id                BIGSERIAL PRIMARY KEY,
    occurred_at             TIMESTAMPTZ NOT NULL,
    event_type              TEXT NOT NULL,
    actor_type              TEXT NOT NULL CHECK (actor_type IN ('agent', 'operator', 'system')),
    actor_id                TEXT NOT NULL,
    actor_issuer            TEXT,
    proposal_id             CHAR(64),
    proposal_sha256         CHAR(64),
    approval_id             CHAR(64),
    attempt_id              CHAR(64),
    run_id                  TEXT,
    trace_id                TEXT,
    agent_id                TEXT,
    principal_user_id       BIGINT,
    server_id               TEXT,
    tool_alias              TEXT,
    reason_code             TEXT,
    redacted_detail         JSONB NOT NULL DEFAULT '{}',
    CONSTRAINT chk_mcp_audit_detail CHECK (jsonb_typeof(redacted_detail) = 'object')
);

CREATE INDEX IF NOT EXISTS idx_mcp_audit_proposal
    ON MCPMutationAudit (proposal_id, occurred_at);
CREATE INDEX IF NOT EXISTS idx_mcp_audit_type
    ON MCPMutationAudit (event_type, occurred_at);
CREATE INDEX IF NOT EXISTS idx_mcp_audit_actor
    ON MCPMutationAudit (actor_id, occurred_at);
CREATE INDEX IF NOT EXISTS idx_mcp_audit_run
    ON MCPMutationAudit (run_id, occurred_at);

CREATE TABLE IF NOT EXISTS MCPMutationRunQuota (
    run_id                  TEXT PRIMARY KEY,
    active_count            INTEGER NOT NULL DEFAULT 0 CHECK (active_count >= 0),
    last_updated_at         TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS MCPMutationPrincipalQuota (
    principal_user_id       BIGINT PRIMARY KEY CHECK (principal_user_id > 0),
    pending_count           INTEGER NOT NULL DEFAULT 0 CHECK (pending_count >= 0),
    last_updated_at         TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS SchemaCapabilities (
    capability              TEXT PRIMARY KEY,
    applied_at              TIMESTAMPTZ NOT NULL,
    applied_by              TEXT NOT NULL,
    checksum                CHAR(64) NOT NULL,
    details                 JSONB NOT NULL DEFAULT '{}',
    CONSTRAINT chk_schema_capabilities_checksum
        CHECK (checksum ~ '^[0-9a-f]{64}$')
);

ALTER TABLE MCPMutationProposals OWNER TO :CROWLER_DB_USER;
ALTER TABLE MCPMutationApprovals OWNER TO :CROWLER_DB_USER;
ALTER TABLE MCPMutationDispatchAttempts OWNER TO :CROWLER_DB_USER;
ALTER TABLE MCPMutationAudit OWNER TO :CROWLER_DB_USER;
ALTER TABLE MCPMutationRunQuota OWNER TO :CROWLER_DB_USER;
ALTER TABLE MCPMutationPrincipalQuota OWNER TO :CROWLER_DB_USER;
ALTER TABLE SchemaCapabilities OWNER TO :CROWLER_DB_USER;
GRANT SELECT, INSERT, UPDATE ON MCPMutationProposals TO :CROWLER_DB_USER;
GRANT SELECT, INSERT, UPDATE ON MCPMutationApprovals TO :CROWLER_DB_USER;
GRANT SELECT, INSERT, UPDATE ON MCPMutationDispatchAttempts TO :CROWLER_DB_USER;
GRANT SELECT, INSERT ON MCPMutationAudit TO :CROWLER_DB_USER;
GRANT USAGE, SELECT ON SEQUENCE MCPMutationAudit_event_id_seq TO :CROWLER_DB_USER;
GRANT SELECT, INSERT, UPDATE ON MCPMutationRunQuota TO :CROWLER_DB_USER;
GRANT SELECT, INSERT, UPDATE ON MCPMutationPrincipalQuota TO :CROWLER_DB_USER;
GRANT SELECT, INSERT ON SchemaCapabilities TO :CROWLER_DB_USER;

INSERT INTO SchemaCapabilities (capability, applied_at, applied_by, checksum, details)
VALUES ('mcp-mutation-authorization-v1', CURRENT_TIMESTAMP, 'migration',
        '00cb16b3efc2a3a619335c4e3bc2b1c470bb34b2378d249a81e746cddc704eed',
        '{"schema_version":"1.17"}'::jsonb)
ON CONFLICT (capability) DO UPDATE
    SET applied_at = EXCLUDED.applied_at,
        applied_by = EXCLUDED.applied_by,
        checksum = EXCLUDED.checksum,
        details = EXCLUDED.details;

COMMIT;
