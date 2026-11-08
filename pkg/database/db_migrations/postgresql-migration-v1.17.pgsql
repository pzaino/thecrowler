-- Version 1.17 adds durable crawl-approval persistence for the trusted
-- operator approval path. This migration is additive, atomic and idempotent.
-- It is a review proposal for existing deployments; the runtime writer keeps
-- requiring schema 1.16 until the migration is explicitly approved and the
-- startup compatibility constant is advanced with it.
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

UPDATE DBSchemaVersion SET is_current=FALSE;
INSERT INTO DBSchemaVersion(version,description,is_current)
VALUES ('1.17','Durable crawl approval persistence',TRUE)
ON CONFLICT (version) DO UPDATE SET description=EXCLUDED.description,is_current=TRUE;
CREATE UNIQUE INDEX IF NOT EXISTS uq_dbschemaversion_one_current
  ON DBSchemaVersion (is_current) WHERE is_current=TRUE;

COMMIT;
