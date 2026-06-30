-- PostgreSQL upgrade migration for Information Seed schema v1.8.
-- Safe to run on existing databases; all DDL is guarded or idempotent.

BEGIN;

INSERT INTO DBSchemaVersion (version, description, is_current)
SELECT '1.8', 'Information Seed lifecycle, candidate audit, provenance, and lookup indexes', TRUE
WHERE NOT EXISTS (SELECT 1 FROM DBSchemaVersion WHERE version = '1.8');

UPDATE DBSchemaVersion
SET is_current = (version = '1.8'), last_updated_at = CURRENT_TIMESTAMP
WHERE version IN ('1.7', '1.8');

ALTER TABLE InformationSeed ADD COLUMN IF NOT EXISTS status VARCHAR(50) DEFAULT 'new' NOT NULL;
ALTER TABLE InformationSeed ADD COLUMN IF NOT EXISTS priority VARCHAR(64) DEFAULT '' NOT NULL;
ALTER TABLE InformationSeed ADD COLUMN IF NOT EXISTS engine VARCHAR(256) DEFAULT '' NOT NULL;
ALTER TABLE InformationSeed ADD COLUMN IF NOT EXISTS last_processed_at TIMESTAMPTZ;
ALTER TABLE InformationSeed ADD COLUMN IF NOT EXISTS last_error TEXT;
ALTER TABLE InformationSeed ADD COLUMN IF NOT EXISTS last_error_at TIMESTAMPTZ;
ALTER TABLE InformationSeed ADD COLUMN IF NOT EXISTS disabled BOOLEAN DEFAULT FALSE;
ALTER TABLE InformationSeed ADD COLUMN IF NOT EXISTS attempts INTEGER DEFAULT 0 NOT NULL;

CREATE TABLE IF NOT EXISTS InformationSeedCandidate (
    information_seed_candidate_id BIGSERIAL PRIMARY KEY,
    information_seed_id BIGINT NOT NULL REFERENCES InformationSeed(information_seed_id) ON DELETE CASCADE,
    normalized_url VARCHAR(2048) NOT NULL,
    host VARCHAR(255),
    provider VARCHAR(255),
    query TEXT,
    rank INTEGER DEFAULT 0 NOT NULL,
    score DOUBLE PRECISION DEFAULT 0 NOT NULL,
    decision_status VARCHAR(32) NOT NULL CHECK (decision_status IN ('accepted', 'rejected')),
    rejection_reason TEXT DEFAULT '' NOT NULL,
    metadata JSONB,
    run_attempt INTEGER DEFAULT 0 NOT NULL,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (information_seed_id, normalized_url, provider, query, rank, run_attempt)
);

ALTER TABLE SourceInformationSeedIndex ADD COLUMN IF NOT EXISTS discovery_provider VARCHAR(255);
ALTER TABLE SourceInformationSeedIndex ADD COLUMN IF NOT EXISTS discovery_query TEXT;
ALTER TABLE SourceInformationSeedIndex ADD COLUMN IF NOT EXISTS discovery_rank INTEGER;
ALTER TABLE SourceInformationSeedIndex ADD COLUMN IF NOT EXISTS candidate_score DOUBLE PRECISION;
ALTER TABLE SourceInformationSeedIndex ADD COLUMN IF NOT EXISTS candidate_reason TEXT;
ALTER TABLE SourceInformationSeedIndex ADD COLUMN IF NOT EXISTS discovery_metadata JSONB;

CREATE INDEX IF NOT EXISTS idx_informationseed_status ON InformationSeed(status);
CREATE INDEX IF NOT EXISTS idx_informationseed_priority ON InformationSeed(priority);
CREATE INDEX IF NOT EXISTS idx_informationseed_disabled ON InformationSeed(disabled);
CREATE INDEX IF NOT EXISTS idx_informationseed_last_processed_at ON InformationSeed(last_processed_at);
CREATE INDEX IF NOT EXISTS idx_informationseed_last_error_at ON InformationSeed(last_error_at);
CREATE INDEX IF NOT EXISTS idx_informationseed_processing_stale
    ON InformationSeed(status, disabled, last_processed_at)
    WHERE status = 'processing' AND disabled = FALSE;
CREATE INDEX IF NOT EXISTS idx_informationseed_claim_queue
    ON InformationSeed(disabled, status, priority, created_at, information_seed_id);
CREATE INDEX IF NOT EXISTS idx_informationseed_engine_status
    ON InformationSeed(engine, status, last_processed_at);

CREATE INDEX IF NOT EXISTS idx_informationseedcandidate_seed
    ON InformationSeedCandidate(information_seed_id, run_attempt DESC, last_updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_informationseedcandidate_decision
    ON InformationSeedCandidate(decision_status);
CREATE INDEX IF NOT EXISTS idx_informationseedcandidate_seed_decision
    ON InformationSeedCandidate(information_seed_id, decision_status, run_attempt DESC);
CREATE INDEX IF NOT EXISTS idx_informationseedcandidate_host
    ON InformationSeedCandidate(host);
CREATE INDEX IF NOT EXISTS idx_informationseedcandidate_provider
    ON InformationSeedCandidate(provider);
CREATE INDEX IF NOT EXISTS idx_informationseedcandidate_normalized_url
    ON InformationSeedCandidate(normalized_url);

CREATE INDEX IF NOT EXISTS idx_sourceinformationseedindex_information_seed_id
    ON SourceInformationSeedIndex(information_seed_id);
CREATE INDEX IF NOT EXISTS idx_sourceinformationseedindex_source_id
    ON SourceInformationSeedIndex(source_id);
CREATE INDEX IF NOT EXISTS idx_sourceinformationseedindex_seed_source
    ON SourceInformationSeedIndex(information_seed_id, source_id);
CREATE INDEX IF NOT EXISTS idx_sourceinformationseedindex_provider_rank
    ON SourceInformationSeedIndex(information_seed_id, discovery_provider, discovery_rank);

COMMIT;
