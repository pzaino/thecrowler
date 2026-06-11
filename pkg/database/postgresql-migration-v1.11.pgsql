
--------------------------------------------------------------------------------
-- Durable email ingestion checkpoints and bounded message reconciliation state.
CREATE TABLE IF NOT EXISTS EmailMailboxState (
    source_id BIGINT NOT NULL,
    provider VARCHAR(64) NOT NULL,
    account_key VARCHAR(191) NOT NULL,
    mailbox_key VARCHAR(191) NOT NULL,
    cursor_token TEXT NOT NULL DEFAULT '',
    cursor_history_id VARCHAR(20) NOT NULL DEFAULT '0',
    cursor_uid BIGINT NOT NULL DEFAULT 0 CHECK (cursor_uid BETWEEN 0 AND 4294967295),
    cursor_uid_validity BIGINT NOT NULL DEFAULT 0 CHECK (cursor_uid_validity BETWEEN 0 AND 4294967295),
    checkpoint_schema_version INTEGER NOT NULL DEFAULT 1 CHECK (checkpoint_schema_version > 0),
    config_fingerprint VARCHAR(128) NOT NULL DEFAULT '',
    message_status VARCHAR(32) CHECK (message_status IS NULL OR message_status IN ('discovered', 'fetched', 'parsed', 'normalized', 'attachments_processed', 'links_enqueued', 'completed', 'retryable_failure', 'permanent_failure')),
    content_hash VARCHAR(255) NOT NULL DEFAULT '',
    error_count BIGINT NOT NULL DEFAULT 0 CHECK (error_count >= 0),
    last_error VARCHAR(2048) NOT NULL DEFAULT '',
    version BIGINT NOT NULL DEFAULT 0 CHECK (version >= 0),
    lease_owner VARCHAR(191),
    lease_expires_at TIMESTAMPTZ,
    fencing_token BIGINT NOT NULL DEFAULT 0 CHECK (fencing_token >= 0),
    last_reconciled_at TIMESTAMPTZ,
    listener_healthy_at TIMESTAMPTZ,
    reset_reason VARCHAR(255),
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (source_id, provider, account_key, mailbox_key),
    CONSTRAINT fk_emailmailboxstate_source FOREIGN KEY (source_id) REFERENCES Sources(source_id) ON DELETE CASCADE,
    CONSTRAINT chk_emailmailboxstate_lease CHECK ((lease_owner IS NULL AND lease_expires_at IS NULL) OR (lease_owner IS NOT NULL AND lease_expires_at IS NOT NULL))
);

CREATE TABLE IF NOT EXISTS EmailMessageState (
    source_id BIGINT NOT NULL,
    provider VARCHAR(64) NOT NULL,
    account_key VARCHAR(191) NOT NULL,
    mailbox_key VARCHAR(191) NOT NULL,
    provider_message_key VARCHAR(191) NOT NULL,
    document_id VARCHAR(191) NOT NULL UNIQUE,
    provider_version VARCHAR(255) NOT NULL DEFAULT '',
    content_hash VARCHAR(255) NOT NULL DEFAULT '',
    disposition VARCHAR(16) NOT NULL CHECK (disposition IN ('indexed', 'deleted', 'skipped', 'quarantined')),
    failure_count BIGINT NOT NULL DEFAULT 0 CHECK (failure_count >= 0),
    last_error VARCHAR(2048) NOT NULL DEFAULT '',
    last_observed_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ,
    quarantined_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (source_id, provider, account_key, mailbox_key, provider_message_key),
    CONSTRAINT fk_emailmessagestate_mailbox FOREIGN KEY (source_id, provider, account_key, mailbox_key)
        REFERENCES EmailMailboxState(source_id, provider, account_key, mailbox_key) ON DELETE CASCADE,
    CONSTRAINT chk_emailmessagestate_deleted CHECK (disposition <> 'deleted' OR deleted_at IS NOT NULL),
    CONSTRAINT chk_emailmessagestate_quarantined CHECK (disposition <> 'quarantined' OR quarantined_at IS NOT NULL)
);

CREATE INDEX IF NOT EXISTS idx_emailmailboxstate_lease ON EmailMailboxState(lease_expires_at, lease_owner);
CREATE INDEX IF NOT EXISTS idx_emailmailboxstate_active ON EmailMailboxState(source_id, active);
CREATE INDEX IF NOT EXISTS idx_emailmessagestate_observed ON EmailMessageState(source_id, last_observed_at);
CREATE INDEX IF NOT EXISTS idx_emailmessagestate_disposition ON EmailMessageState(source_id, disposition, last_observed_at);
