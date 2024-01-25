#!/bin/bash
set -e

# Perform all actions as $DOCKER_POSTGRES_USER
export PGPASSWORD=$POSTGRES_PASSWORD
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOSQL

-- PostgreSQL setup script for the search engine database.
-- Adjusted for better performance and best practices.

CREATE TABLE IF NOT EXISTS Sources (
    source_id SERIAL PRIMARY KEY,
    url TEXT NOT NULL, -- Changed to TEXT for potentially long URLs
    last_crawled_at TIMESTAMP,
    status VARCHAR(50),
    last_error TEXT, -- Changed to TEXT for potentially long error messages
    last_error_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP,
    restricted BOOLEAN DEFAULT TRUE
);

CREATE TABLE IF NOT EXISTS SearchIndex (
    index_id SERIAL PRIMARY KEY,
    source_id INTEGER REFERENCES Sources(source_id),
    page_url TEXT NOT NULL UNIQUE, -- Changed to TEXT for long URLs
    title VARCHAR(255),
    summary TEXT NOT NULL, -- Assuming summary is always required
    content TEXT,
    snapshot_url TEXT, -- Changed to TEXT for long URLs
    indexed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP -- Added default value
);

CREATE TABLE IF NOT EXISTS MetaTags (
    metatag_id SERIAL PRIMARY KEY,
    index_id INTEGER REFERENCES SearchIndex(index_id),
    name VARCHAR(255),
    content TEXT
);

CREATE TABLE IF NOT EXISTS Keywords (
    keyword_id SERIAL PRIMARY KEY,
    keyword VARCHAR(100) NOT NULL UNIQUE
);

CREATE TABLE IF NOT EXISTS KeywordIndex (
    keyword_index_id SERIAL PRIMARY KEY,
    keyword_id INTEGER REFERENCES Keywords(keyword_id),
    index_id INTEGER REFERENCES SearchIndex(index_id),
    occurrences INTEGER
);

-- Create an index for the Sources url column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_sources_url') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_sources_url ON Sources(url text_pattern_ops);
    END IF;
END
$$;

-- Create an index for the Sources status column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_sources_status') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_sources_status ON Sources(status);
    END IF;
END
$$;

-- Create an index for the Sources source_id column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_sources_source_id') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_sources_source_id ON Sources(source_id);
    END IF;
END
$$;

-- Create an index for the SearchIndex title column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_searchindex_title') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_searchindex_title ON SearchIndex(title text_pattern_ops) WHERE title IS NOT NULL;
    END IF;
END
$$;

-- Create an index for the SearchIndex summary column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_searchindex_summary') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_searchindex_summary ON SearchIndex(left(summary, 1000) text_pattern_ops) WHERE summary IS NOT NULL;
    END IF;
END
$$;

-- Create an index for the SearchIndex content column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_searchindex_content') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_searchindex_content ON SearchIndex(left(content, 1000) text_pattern_ops) WHERE content IS NOT NULL;
    END IF;
END
$$;

-- Create an index for the SearchIndex snapshot_url column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_searchindex_snapshot_url') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_searchindex_snapshot_url ON SearchIndex(snapshot_url) WHERE snapshot_url IS NOT NULL;
    END IF;
END
$$;

-- Create an index for the SearchIndex indexed_at column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_searchindex_indexed_at') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_searchindex_indexed_at ON SearchIndex(indexed_at);
    END IF;
END
$$;

-- Create an index for the MetaTags index_id column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_metatags_index_id') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_metatags_index_id ON MetaTags(index_id);
    END IF;
END
$$;

-- Create an index for the MetaTags name column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_metatags_name') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_metatags_name ON MetaTags(name text_pattern_ops) WHERE name IS NOT NULL;
    END IF;
END
$$;

-- KeywordIndex foreign keys are already indexed due to the REFERENCES constraint
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_keywordindex_occurrences') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_keywordindex_occurrences ON KeywordIndex(occurrences);
    END IF;
END
$$;

-- Add a tsvector column for full-text search
DO $$
BEGIN
    -- Check and add the content_fts column if it does not exist
    IF NOT EXISTS (
        SELECT
            1
        FROM
            information_schema.columns
        WHERE
            table_name = 'searchindex' AND
            column_name = 'content_fts'
    ) THEN
        ALTER TABLE SearchIndex ADD COLUMN content_fts tsvector;
    END IF;
END
$$;

-- Create an index on the tsvector column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_searchindex_content_fts') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_searchindex_content_fts ON SearchIndex USING gin(content_fts);
    END IF;
END
$$;

-- Create a function to update the tsvector column
CREATE OR REPLACE FUNCTION searchindex_content_trigger() RETURNS trigger AS $$
BEGIN
  NEW.content_fts := to_tsvector('english', coalesce(NEW.content, ''));
  RETURN NEW;
END
$$ LANGUAGE plpgsql;

-- Create a trigger to update the tsvector column
DO $$
BEGIN
    -- Check if the trigger already exists
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_searchindex_content') THEN
        -- Create the trigger if it doesn't exist
        CREATE TRIGGER trg_searchindex_content BEFORE INSERT OR UPDATE
        ON SearchIndex FOR EACH ROW EXECUTE FUNCTION searchindex_content_trigger();
    END IF;
END
$$;

-- Create a new user
CREATE USER $CROWLER_DB_USER WITH ENCRYPTED PASSWORD '$CROWLER_DB_PASSWORD';

-- Grant permissions to the user on the '$POSTGRES_DB' database
GRANT CONNECT ON DATABASE "$POSTGRES_DB" TO $CROWLER_DB_USER;
GRANT USAGE ON SCHEMA public TO $CROWLER_DB_USER;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO $CROWLER_DB_USER;
GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA public TO $CROWLER_DB_USER;
ALTER ROLE $CROWLER_DB_USER SET search_path TO public;

EOSQL
