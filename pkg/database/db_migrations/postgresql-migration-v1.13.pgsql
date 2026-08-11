ALTER TABLE Sources
ADD COLUMN IF NOT EXISTS sub_priority INTEGER DEFAULT 0 NOT NULL;

CREATE INDEX IF NOT EXISTS idx_sources_priority_sub_priority
ON Sources(priority, sub_priority DESC, source_id ASC);

DROP FUNCTION IF EXISTS update_sources(
    INTEGER,
    VARCHAR,
    VARCHAR,
    VARCHAR,
    VARCHAR,
    VARCHAR,
    VARCHAR
);

-- Recreate update_sources() using:
-- ORDER BY s.sub_priority DESC, s.source_id ASC
-- and return sub_priority as described above.
