-- PostgreSQL setup script for the search engine database.
-- Adjusted for better performance and best practices.

-- To run this setup script manually from a PostgreSQL UI
-- Define the following variables in psql replacing their values
-- with your own and then run the script.
--\set POSTGRES_DB 'SitesIndex'
--\set CROWLER_DB_USER 'your_username'
--\set CROWLER_DB_PASSWORD 'your_password'

--------------------------------------------------------------------------------
-- Database Tables setup

-- Sources table stores the URLs or the information's seed to be crawled
CREATE TABLE IF NOT EXISTS Sources (
    source_id BIGSERIAL PRIMARY KEY,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP,
    url TEXT NOT NULL UNIQUE,         -- Using TEXT for long URLs
    status VARCHAR(50) DEFAULT 'new' NOT NULL, -- All new sources are set to 'new' by default
    last_crawled_at TIMESTAMP,
    last_error TEXT,                  -- Using TEXT for potentially long error messages
    last_error_at TIMESTAMP,
    restricted INTEGER DEFAULT 2 NOT NULL,     -- 0 = fully restricted (just this URL)
                                      -- 1 = l3 domain restricted (everything within this URL l3 domain)
                                      -- 2 = l2 domain restricted
                                      -- 3 = l1 domain restricted
                                      -- 4 = no restrictions
    disabled BOOLEAN DEFAULT FALSE,
    flags INTEGER DEFAULT 0 NOT NULL,
    config JSONB                      -- Stores JSON document with all details about the source
                                      -- configuration for the crawler
);

-- Owners table stores the information about the owners of the sources
CREATE TABLE IF NOT EXISTS Owners (
    owner_id BIGSERIAL PRIMARY KEY,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    details_hash VARCHAR(64) UNIQUE NOT NULL, -- SHA256 hash of the details for fast comparison and uniqueness
    details JSONB NOT NULL             -- Stores JSON document with all details about the owner
);

-- NetInfo table stores the network information retrieved from the sources
CREATE TABLE IF NOT EXISTS NetInfo (
    netinfo_id BIGSERIAL PRIMARY KEY,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    details_hash VARCHAR(64) UNIQUE NOT NULL, -- SHA256 hash of the details for fast comparison and uniqueness
    details JSONB NOT NULL
);

-- HTTPInfo table stores the HTTP header information retrieved from the sources
CREATE TABLE IF NOT EXISTS HTTPInfo (
    httpinfo_id BIGSERIAL PRIMARY KEY,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    details_hash VARCHAR(64) UNIQUE NOT NULL, -- SHA256 hash of the details for fast comparison and uniqueness
    details JSONB NOT NULL
);

-- SearchIndex table stores the indexed information from the sources
CREATE TABLE IF NOT EXISTS SearchIndex (
    index_id BIGSERIAL PRIMARY KEY,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    page_url TEXT NOT NULL UNIQUE,                  -- Using TEXT for long URLs
    title VARCHAR(255),
    summary TEXT NOT NULL,                          -- Assuming summary is always required
    detected_type VARCHAR(8),                       -- (content type) denormalized for fast searches
    detected_lang VARCHAR(8),                       -- (URI language) denormalized for fast searches
    content TEXT
);

-- Screenshots table stores the screenshots details of the indexed pages
CREATE TABLE IF NOT EXISTS Screenshots (
    screenshot_id BIGSERIAL PRIMARY KEY,
    index_id BIGINT REFERENCES SearchIndex(index_id),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    type VARCHAR(10) NOT NULL DEFAULT 'desktop',
    screenshot_link TEXT NOT NULL,
    height INTEGER NOT NULL DEFAULT 0,
    width INTEGER NOT NULL DEFAULT 0,
    byte_size INTEGER NOT NULL DEFAULT 0,
    thumbnail_height INTEGER NOT NULL DEFAULT 0,
    thumbnail_width INTEGER NOT NULL DEFAULT 0,
    thumbnail_link TEXT NOT NULL DEFAULT '',
    format VARCHAR(10) NOT NULL DEFAULT 'png',
    FOREIGN KEY (index_id) REFERENCES SearchIndex(index_id) ON DELETE CASCADE
);

-- WebObjects table stores all types of web objects found in the indexed pages
-- This includes scripts, styles, images, iframes, HTML etc.
CREATE TABLE IF NOT EXISTS WebObjects (
    object_id BIGSERIAL PRIMARY KEY,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    object_link TEXT NOT NULL DEFAULT 'db', -- The link to where the object is stored if not in the DB
    object_type VARCHAR(255) NOT NULL DEFAULT 'text/html', -- The type of the object, for fast searches
    object_hash VARCHAR(64) UNIQUE NOT NULL, -- SHA256 hash of the object for fast comparison and uniqueness
    object_content TEXT, -- The actual content of the object, nullable if stored externally
    object_html TEXT -- The HTML content of the object, nullable if stored externally
);

-- MetaTags table stores the meta tags from the SearchIndex
CREATE TABLE IF NOT EXISTS MetaTags (
    metatag_id BIGSERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    content TEXT NOT NULL,
    UNIQUE(name, content) -- Ensure that each name-content pair is unique
);

-- Keywords table stores all the found keywords during an indexing
CREATE TABLE IF NOT EXISTS Keywords (
    keyword_id BIGSERIAL PRIMARY KEY,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    keyword VARCHAR(100) NOT NULL UNIQUE
);

-- SourceOwnerIndex table stores the relationship between sources and their owners
CREATE TABLE IF NOT EXISTS SourceOwnerIndex (
    source_owner_id BIGSERIAL PRIMARY KEY,
    source_id BIGINT NOT NULL,
    owner_id BIGINT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_source
        FOREIGN KEY(source_id)
        REFERENCES Sources(source_id)
        ON DELETE CASCADE,
    CONSTRAINT fk_owner
        FOREIGN KEY(owner_id)
        REFERENCES Owners(owner_id)
        ON DELETE CASCADE,
    UNIQUE(source_id, owner_id), -- Ensures unique combinations of source_id and owner_id
    FOREIGN KEY (source_id) REFERENCES Sources(source_id) ON DELETE CASCADE,
    FOREIGN KEY (owner_id) REFERENCES Owners(owner_id) ON DELETE CASCADE
);

-- SourceSearchIndex table stores the relationship between sources and the indexed pages
CREATE TABLE IF NOT EXISTS SourceSearchIndex (
    ss_index_id BIGSERIAL PRIMARY KEY,
    source_id BIGINT NOT NULL,
    index_id BIGINT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_source
        FOREIGN KEY(source_id)
        REFERENCES Sources(source_id)
        ON DELETE CASCADE,
    CONSTRAINT fk_search_index
        FOREIGN KEY(index_id)
        REFERENCES SearchIndex(index_id)
        ON DELETE CASCADE,
    UNIQUE(source_id, index_id), -- Ensures unique combinations of source_id and index_id
    FOREIGN KEY (source_id) REFERENCES Sources(source_id) ON DELETE CASCADE,
    FOREIGN KEY (index_id) REFERENCES SearchIndex(index_id) ON DELETE CASCADE
);

-- PageWebObjectsIndex table stores the relationship between indexed pages and the objects found in them
CREATE TABLE IF NOT EXISTS PageWebObjectsIndex (
    page_object_id BIGSERIAL PRIMARY KEY,
    index_id BIGINT NOT NULL REFERENCES SearchIndex(index_id),
    object_id BIGINT NOT NULL REFERENCES WebObjects(object_id),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(index_id, object_id), -- Ensures that the same object is not linked multiple times to the same page
    FOREIGN KEY (index_id) REFERENCES SearchIndex(index_id) ON DELETE CASCADE,
    FOREIGN KEY (object_id) REFERENCES WebObjects(object_id) ON DELETE CASCADE
);

-- Relationship table between SearchIndex and MetaTags
CREATE TABLE IF NOT EXISTS SearchIndexMetaTags (
    sim_id BIGSERIAL PRIMARY KEY,
    index_id BIGINT NOT NULL REFERENCES SearchIndex(index_id),
    metatag_id BIGINT NOT NULL REFERENCES MetaTags(metatag_id),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    UNIQUE(index_id, metatag_id), -- Prevents duplicate associations
    FOREIGN KEY (index_id) REFERENCES SearchIndex(index_id) ON DELETE CASCADE,
    FOREIGN KEY (metatag_id) REFERENCES MetaTags(metatag_id) ON DELETE CASCADE
);

-- KeywordIndex table stores the relationship between keywords and the indexed pages
CREATE TABLE IF NOT EXISTS KeywordIndex (
    keyword_index_id BIGSERIAL PRIMARY KEY,
    keyword_id BIGINT REFERENCES Keywords(keyword_id),
    index_id BIGINT REFERENCES SearchIndex(index_id),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    occurrences INTEGER,
    UNIQUE(keyword_id, index_id), -- Ensures unique combinations of keyword_id and index_id
    FOREIGN KEY (index_id) REFERENCES SearchIndex(index_id) ON DELETE CASCADE,
    FOREIGN KEY (keyword_id) REFERENCES Keywords(keyword_id) ON DELETE CASCADE
);

-- NetInfoIndex table stores the relationship between network information and the indexed pages
CREATE TABLE IF NOT EXISTS NetInfoIndex (
    netinfo_index_id BIGSERIAL PRIMARY KEY,
    netinfo_id BIGINT REFERENCES NetInfo(netinfo_id),
    index_id BIGINT REFERENCES SearchIndex(index_id),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(netinfo_id, index_id), -- Ensures unique combinations of netinfo_id and index_id
    FOREIGN KEY (index_id) REFERENCES SearchIndex(index_id) ON DELETE CASCADE,
    FOREIGN KEY (netinfo_id) REFERENCES NetInfo(netinfo_id) ON DELETE CASCADE
);

-- HTTPInfoIndex table stores the relationship between HTTP information and the indexed pages
CREATE TABLE IF NOT EXISTS HTTPInfoIndex (
    httpinfo_index_id BIGSERIAL PRIMARY KEY,
    httpinfo_id BIGINT REFERENCES HTTPInfo(httpinfo_id),
    index_id BIGINT REFERENCES SearchIndex(index_id),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(httpinfo_id, index_id), -- Ensures unique combinations of httpinfo_id and index_id
    FOREIGN KEY (index_id) REFERENCES SearchIndex(index_id) ON DELETE CASCADE,
    FOREIGN KEY (httpinfo_id) REFERENCES HTTPInfo(httpinfo_id) ON DELETE CASCADE
);

--------------------------------------------------------------------------------
-- Indexes and triggers setup

-- Creates an index for the Sources url column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_sources_url') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_sources_url ON Sources(url text_pattern_ops);
    END IF;
END
$$;

-- Creates an index for the Sources status column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_sources_status') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_sources_status ON Sources(status);
    END IF;
END
$$;

-- Creates an index for the Sources last_crawled_at column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_sources_last_crawled_at') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_sources_last_crawled_at ON Sources(last_crawled_at);
    END IF;
END
$$;

-- Creates an index for the Sources source_id column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_sources_source_id') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_sources_source_id ON Sources(source_id);
    END IF;
END
$$;

-- Creates a gin index for the Source config column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_sources_config') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_sources_config ON Sources USING gin(config jsonb_path_ops);
    END IF;
END
$$;

-- Creates an index for the Owners details column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_owners_details') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_owners_details ON Owners USING gin(details jsonb_path_ops);
    END IF;
END
$$;

-- Creates an index for the Owners details_hash column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_owners_details_hash') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_owners_details_hash ON Owners(details_hash);
    END IF;
END
$$;

-- Creates an index for the Owners last_updated_at column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_owners_last_updated_at') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_owners_last_updated_at ON Owners(last_updated_at);
    END IF;
END
$$;

-- Creates an index for the Owners created_at column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_owners_created_at') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_owners_created_at ON Owners(created_at);
    END IF;
END
$$;

-- Creates an index for the NetInfo last_updated_at column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_netinfo_last_updated_at') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_netinfo_last_updated_at ON NetInfo(last_updated_at);
    END IF;
END
$$;

-- Creates an index for the NetInfo created_at column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_netinfo_created_at') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_netinfo_created_at ON NetInfo(created_at);
    END IF;
END
$$;

-- Creates an index for the NetInfo details_hash column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_netinfo_details_hash') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_netinfo_details_hash ON NetInfo(details_hash);
    END IF;
END
$$;

-- Creates an index for the details column in the NetInfo table
-- This index is used to search for specific keys in the JSONB column
-- The jsonb_path_ops operator class is used to index the JSONB column
-- for queries that use the @> operator to search for keys in the JSONB column
-- The jsonb_path_ops operator class is optimized for queries that use the @> operator
-- to search for keys in the JSONB column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_json_netinfo_details') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_json_netinfo_details ON NetInfo USING gin (details jsonb_path_ops);
    END IF;
END
$$;

-- Creates an index for the HTTPInfo last_updated_at column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_httpinfo_last_updated_at') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_httpinfo_last_updated_at ON HTTPInfo(last_updated_at);
    END IF;
END
$$;

-- Creates an index for the HTTPInfo created_at column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_httpinfo_created_at') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_httpinfo_created_at ON HTTPInfo(created_at);
    END IF;
END
$$;

-- Creates an index for the HTTPInfo details_hash column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_httpinfo_details_hash') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_httpinfo_details_hash ON HTTPInfo(details_hash);
    END IF;
END
$$;

-- Creates an index for the HTTPInfo details column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_json_httpinfo_details') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_json_httpinfo_details ON HTTPInfo USING gin (details jsonb_path_ops);
    END IF;
END
$$;

-- Creates an index for the SearchIndex title column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_searchindex_title') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_searchindex_title ON SearchIndex(title text_pattern_ops) WHERE title IS NOT NULL;
    END IF;
END
$$;

-- Creates an index for the SearchIndex summary column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_searchindex_summary') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_searchindex_summary ON SearchIndex(left(summary, 1000) text_pattern_ops) WHERE summary IS NOT NULL;
    END IF;
END
$$;

-- Creates an index for the SearchIndex content column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_searchindex_content') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_searchindex_content ON SearchIndex(left(content, 1000) text_pattern_ops) WHERE content IS NOT NULL;
    END IF;
END
$$;

-- Creates an index for the SearchIndex last_updated_at column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_searchindex_last_updated_at') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_searchindex_last_updated_at ON SearchIndex(last_updated_at);
    END IF;
END
$$;

-- Creates an index for the Screenshots index_id column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_screenshots_index_id') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_screenshots_index_id ON Screenshots(index_id);
    END IF;
END
$$;

-- Creates an index for the Screenshots screenshot_link column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_screenshots_screenshot_link') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_screenshots_screenshot_link ON Screenshots(screenshot_link);
    END IF;
END
$$;

-- Creates an index for the Screenshots last_updated_at column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_screenshots_last_updated_at') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_screenshots_last_updated_at ON Screenshots(last_updated_at);
    END IF;
END
$$;

-- Creates an index for the Screenshots created_at column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_screenshots_created_at') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_screenshots_created_at ON Screenshots(created_at);
    END IF;
END
$$;

-- Creates an index for the WebObjects object_link column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_webobjects_object_link') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_webobjects_object_link ON WebObjects(object_link text_pattern_ops);
    END IF;
END
$$;

-- Creates an index for the WebObjects object_type column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_webobjects_object_type') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_webobjects_object_type ON WebObjects(object_type);
    END IF;
END
$$;

-- Create an index for the WebObjects object_hash column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_webobjects_object_hash') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_webobjects_object_hash ON WebObjects(object_hash);
    END IF;
END
$$;

-- Creates an index for the WebObjects object_content column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_webobjects_object_content') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_webobjects_object_content ON WebObjects(left(object_content, 1024) text_pattern_ops) WHERE object_content IS NOT NULL AND object_link = 'db';
    END IF;
END
$$;

-- Creates an index for the WebObjects created_at column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_webobjects_created_at') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_webobjects_created_at ON WebObjects(created_at);
    END IF;
END
$$;

-- Creates an index for the WebObjects last_updated_at column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_webobjects_last_updated_at') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_webobjects_last_updated_at ON WebObjects(last_updated_at);
    END IF;
END
$$;

-- Creates an index for the MetaTags name column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_metatags_name') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_metatags_name ON MetaTags(name text_pattern_ops) WHERE name IS NOT NULL;
    END IF;
END
$$;

-- Creates an index for the MetaTags content column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_metatags_content') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_metatags_content ON MetaTags(left(content, 1024) text_pattern_ops) WHERE content IS NOT NULL;
    END IF;
END
$$;

-- Creates and index for the Keywords ocurences column to help
-- with keyowrds analysis
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_keywordindex_occurrences') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_keywordindex_occurrences ON KeywordIndex(occurrences);
    END IF;
END
$$;

-- Creates an index for SourceOwnerIndex owner_id column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_sourceownerindex_owner_id') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX IF NOT EXISTS idx_sourceownerindex_owner_id ON SourceOwnerIndex(owner_id);
    END IF;
END
$$;

-- Creates an index for SourceOwnerIndex source_id column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_sourceownerindex_source_id') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX IF NOT EXISTS idx_sourceownerindex_source_id ON SourceOwnerIndex(source_id);
    END IF;
END
$$;

-- Creates an index for the SourceSearchIndex source_id column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_ssi_source_id') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_ssi_source_id ON SourceSearchIndex(source_id);
    END IF;
END
$$;

-- Creates an index for the SourceSearchIndex index_id column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_ssi_index_id') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_ssi_index_id ON SourceSearchIndex(index_id);
    END IF;
END
$$;

-- Creates an index for the PageWebObjectsIndex index_id column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_pwoi_index_id') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_pwoi_index_id ON PageWebObjectsIndex(index_id);
    END IF;
END
$$;

-- Creates an index for the PageWebObjectsIndex object_id column
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_pwoi_object_id') THEN
        -- Create the index if it doesn't exist
        CREATE INDEX idx_pwoi_object_id ON PageWebObjectsIndex(object_id);
    END IF;
END
$$;

--------------------------------------------------------------------------------
-- Full Text Search setup

-- Adds a tsvector column for full-text search
DO $$
BEGIN
    -- Check and add the content_fts column if it does not exist
    IF NOT EXISTS (
        SELECT
            1
        FROM
            information_schema.columns
        WHERE
            table_name = 'WebObjects' AND
            column_name = 'object_content_fts'
    ) THEN
        ALTER TABLE WebObjects ADD COLUMN object_content_fts tsvector;
    END IF;
END
$$;

--------------------------------------------------------------------------------
--  Functions and Triggers setup

-- Creates a function to update the tsvector column (FTS = Full Text Search)
CREATE OR REPLACE FUNCTION webobjects_content_trigger() RETURNS trigger AS $$
BEGIN
  NEW.object_content_fts := to_tsvector('english', coalesce(NEW.object_content, ''));
  RETURN NEW;
END
$$ LANGUAGE plpgsql;

-- Creates a trigger to update the tsvector column
DO $$
BEGIN
    -- Check if the trigger already exists
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_webobjects_content') THEN
        -- Create the trigger if it doesn't exist
        CREATE TRIGGER trg_webobjects_content BEFORE INSERT OR UPDATE
        ON WebObjects FOR EACH ROW EXECUTE FUNCTION webobjects_content_trigger();
    END IF;
END
$$;

-- Creates a function to update the last_updated_at column
CREATE OR REPLACE FUNCTION update_last_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.last_updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Creates a trigger to update the last_updated_at column on Sources table
DO $$
BEGIN
    -- Check if the trigger already exists
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_update_sources_last_updated_before_update') THEN
		CREATE TRIGGER trg_update_sources_last_updated_before_update
		BEFORE UPDATE ON Sources
		FOR EACH ROW
		EXECUTE FUNCTION update_last_updated_at_column();
	END IF;
END
$$;

-- Creates a trigger to update the last_updated_at column on SearchIndex table
DO $$
BEGIN
    -- Check if the trigger already exists
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_update_searchindex_last_updated_before_update') THEN
		CREATE TRIGGER trg_update_searchindex_last_updated_before_update
		BEFORE UPDATE ON SearchIndex
		FOR EACH ROW
		EXECUTE FUNCTION update_last_updated_at_column();
	END IF;
END
$$;

-- Creates a trigger to update the last_updated_at column on Owners table
DO $$
BEGIN
    -- Check if the trigger already exists
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_update_owners_last_updated_before_update') THEN
        CREATE TRIGGER trg_update_owners_last_updated_before_update
        BEFORE UPDATE ON Owners
        FOR EACH ROW
        EXECUTE FUNCTION update_last_updated_at_column();
    END IF;
END
$$;

-- Creates a trigger to update the last_updated_at column on NetInfo table
DO $$
BEGIN
    -- Check if the trigger already exists
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_update_netinfo_last_updated_before_update') THEN
        CREATE TRIGGER trg_update_netinfo_last_updated_before_update
        BEFORE UPDATE ON NetInfo
        FOR EACH ROW
        EXECUTE FUNCTION update_last_updated_at_column();
    END IF;
END
$$;

-- Creates a trigger to update the last_updated_at column on HTTPInfo table
DO $$
BEGIN
    -- Check if the trigger already exists
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_update_httpinfo_last_updated_before_update') THEN
        CREATE TRIGGER trg_update_httpinfo_last_updated_before_update
        BEFORE UPDATE ON HTTPInfo
        FOR EACH ROW
        EXECUTE FUNCTION update_last_updated_at_column();
    END IF;
END
$$;

-- Creates a trigger to update the last_updated_at column on WebObjects table
DO $$
BEGIN
    -- Check if the trigger already exists
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_update_webobjects_last_updated_before_update') THEN
        CREATE TRIGGER trg_update_webobjects_last_updated_before_update
        BEFORE UPDATE ON WebObjects
        FOR EACH ROW
        EXECUTE FUNCTION update_last_updated_at_column();
    END IF;
END
$$;

-- Creates a trigger to update the last_updated_at column on MetaTags table
DO $$
BEGIN
    -- Check if the trigger already exists
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_update_metatags_last_updated_before_update') THEN
        CREATE TRIGGER trg_update_metatags_last_updated_before_update
        BEFORE UPDATE ON MetaTags
        FOR EACH ROW
        EXECUTE FUNCTION update_last_updated_at_column();
    END IF;
END
$$;

-- Creates a trigger to update the last_updated_at column on Keywords table
DO $$
BEGIN
    -- Check if the trigger already exists
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_update_keywords_last_updated_before_update') THEN
        CREATE TRIGGER trg_update_keywords_last_updated_before_update
        BEFORE UPDATE ON Keywords
        FOR EACH ROW
        EXECUTE FUNCTION update_last_updated_at_column();
    END IF;
END
$$;

-- Creates a trigger to update the last_updated_at column on SourceOwnerIndex table
DO $$
BEGIN
    -- Check if the trigger already exists
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_update_sourceowner_last_updated_before_update') THEN
        CREATE TRIGGER trg_update_sourceowner_last_updated_before_update
        BEFORE UPDATE ON SourceOwnerIndex
        FOR EACH ROW
        EXECUTE FUNCTION update_last_updated_at_column();
    END IF;
END
$$;

-- Creates a trigger to update the last_updated_at column on SourceSearchIndex table
DO $$
BEGIN
    -- Check if the trigger already exists
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_update_ssi_last_updated_before_update') THEN
        CREATE TRIGGER trg_update_ssi_last_updated_before_update
        BEFORE UPDATE ON SourceSearchIndex
        FOR EACH ROW
        EXECUTE FUNCTION update_last_updated_at_column();
    END IF;
END
$$;

-- Creates a trigger to update the last_updated_at column on KeywordIndex table
DO $$
BEGIN
    -- Check if the trigger already exists
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_update_keywordindex_last_updated_before_update') THEN
        CREATE TRIGGER trg_update_keywordindex_last_updated_before_update
        BEFORE UPDATE ON KeywordIndex
        FOR EACH ROW
        EXECUTE FUNCTION update_last_updated_at_column();
    END IF;
END
$$;

-- Function to handle orphaned records in the HTTPInfo and NetInfo tables
CREATE OR REPLACE FUNCTION cleanup_orphaned_httpinfo()
RETURNS void AS $$
BEGIN
    DELETE FROM HTTPInfo
    WHERE NOT EXISTS (
        SELECT 1 FROM HTTPInfoIndex
        WHERE HTTPInfo.httpinfo_id = HTTPInfoIndex.httpinfo_id
    );
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION cleanup_orphaned_netinfo()
RETURNS void AS $$
BEGIN
    DELETE FROM NetInfo
    WHERE NOT EXISTS (
        SELECT 1 FROM NetInfoIndex
        WHERE NetInfo.netinfo_id = NetInfoIndex.netinfo_id
    );
END;
$$ LANGUAGE plpgsql;

-- Function to handle the deletion of shared entities when no longer linked to any Source.
CREATE OR REPLACE FUNCTION handle_shared_entity_deletion()
RETURNS TRIGGER AS $$
BEGIN
    -- Example for MetaTags; replicate logic for WebObjects and Keywords by adjusting WHERE conditions.
    IF TG_TABLE_NAME = 'searchindexmetatags' THEN
        IF (SELECT COUNT(*) FROM SearchIndexMetaTags WHERE metatag_id = OLD.metatag_id) = 0 THEN
            DELETE FROM MetaTags WHERE metatag_id = OLD.metatag_id;
        END IF;
    ELSIF TG_TABLE_NAME = 'pagewebobjectsindex' THEN
        IF (SELECT COUNT(*) FROM PageWebObjectsIndex WHERE object_id = OLD.object_id) = 0 THEN
            DELETE FROM WebObjects WHERE object_id = OLD.object_id;
        END IF;
    ELSIF TG_TABLE_NAME = 'keywordindex' THEN
        IF (SELECT COUNT(*) FROM KeywordIndex WHERE keyword_id = OLD.keyword_id) = 0 THEN
            DELETE FROM Keywords WHERE keyword_id = OLD.keyword_id;
        END IF;
    END IF;
    RETURN OLD;
END;
$$ LANGUAGE plpgsql;

-- Triggers to handle the deletion of shared entities.
-- Repeat this logic for each linking table: SearchIndexMetaTags, PageWebObjectsIndex, KeywordIndex.
-- Adjust the referencing table name and the column names accordingly.

-- Creates a trigger to handle the deletion of shared entities when no longer linked to any Source.
DO $$
BEGIN
    -- Check if the trigger already exists
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_after_delete_searchindexmetatags') THEN
        -- Create the trigger if it doesn't exist
        CREATE TRIGGER trg_after_delete_searchindexmetatags
        AFTER DELETE ON SearchIndexMetaTags
        FOR EACH ROW
        EXECUTE FUNCTION handle_shared_entity_deletion();
    END IF;
END
$$;

-- Creates a trigger to handle the deletion of shared entities when no longer linked to any Source.
DO $$
BEGIN
    -- Check if the trigger already exists
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_after_delete_pagewebobjectsindex') THEN
        -- Create the trigger if it doesn't exist
        CREATE TRIGGER trg_after_delete_pagewebobjectsindex
        AFTER DELETE ON PageWebObjectsIndex
        FOR EACH ROW
        EXECUTE FUNCTION handle_shared_entity_deletion();
    END IF;
END
$$;

-- Creates a trigger to handle the deletion of shared entities when no longer linked to any Source.
DO $$
BEGIN
    -- Check if the trigger already exists
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_after_delete_keywordindex') THEN
        -- Create the trigger if it doesn't exist
        CREATE TRIGGER trg_after_delete_keywordindex
        AFTER DELETE ON KeywordIndex
        FOR EACH ROW
        EXECUTE FUNCTION handle_shared_entity_deletion();
    END IF;
END
$$;


CREATE OR REPLACE FUNCTION handle_searchindex_deletion()
RETURNS TRIGGER AS $$
BEGIN
    -- Check if there are no more links in SourceSearchIndex for the index_id of the deleted row
    IF (SELECT COUNT(*) FROM SourceSearchIndex WHERE index_id = OLD.index_id) = 0 THEN
        -- If no more links exist, delete the SearchIndex entry
        DELETE FROM SearchIndex WHERE index_id = OLD.index_id;
    END IF;
    RETURN OLD;
END;
$$ LANGUAGE plpgsql;


-- Creates a trigger to handle the deletion of SearchIndex entries when no longer linked to any Source.
DO $$
BEGIN
    -- Check if the trigger already exists
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'trg_after_delete_source_searchindex') THEN
        -- Create the trigger if it doesn't exist
        CREATE TRIGGER trg_after_delete_source_searchindex
        AFTER DELETE ON SourceSearchIndex
        FOR EACH ROW EXECUTE FUNCTION handle_searchindex_deletion();
    END IF;
END
$$;

-- Ensure that the ON CASCADE DELETE is defined correctly:

ALTER TABLE Screenshots DROP CONSTRAINT IF EXISTS screenshots_index_id_fkey;
ALTER TABLE Screenshots ADD CONSTRAINT screenshots_index_id_fkey FOREIGN KEY (index_id) REFERENCES SearchIndex(index_id) ON DELETE CASCADE;
ALTER TABLE pagewebobjectsindex DROP CONSTRAINT IF EXISTS pagewebobjectsindex_index_id_fkey;
ALTER TABLE pagewebobjectsindex ADD CONSTRAINT pagewebobjectsindex_index_id_fkey FOREIGN KEY (index_id) REFERENCES SearchIndex(index_id) ON DELETE CASCADE;
ALTER TABLE searchindexmetatags DROP CONSTRAINT IF EXISTS searchindexmetatags_index_id_fkey;
ALTER TABLE searchindexmetatags ADD CONSTRAINT searchindexmetatags_index_id_fkey FOREIGN KEY (index_id) REFERENCES SearchIndex(index_id) ON DELETE CASCADE;
ALTER TABLE keywordindex DROP CONSTRAINT IF EXISTS keywordindex_index_id_fkey;
ALTER TABLE keywordindex ADD CONSTRAINT keywordindex_index_id_fkey FOREIGN KEY (index_id) REFERENCES SearchIndex(index_id) ON DELETE CASCADE;
ALTER TABLE netinfoindex DROP CONSTRAINT IF EXISTS netinfoindex_index_id_fkey;
ALTER TABLE netinfoindex ADD CONSTRAINT netinfoindex_index_id_fkey FOREIGN KEY (index_id) REFERENCES SearchIndex(index_id) ON DELETE CASCADE;
ALTER TABLE httpinfoindex DROP CONSTRAINT IF EXISTS httpinfoindex_index_id_fkey;
ALTER TABLE httpinfoindex ADD CONSTRAINT httpinfoindex_index_id_fkey FOREIGN KEY (index_id) REFERENCES SearchIndex(index_id) ON DELETE CASCADE;

-- Creates a function to fetch and update the sources as an atomic operation
-- this is required to be able to deploy multiple crawlers without the risk of
-- fetching the same source multiple times
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_proc WHERE proname = 'update_sources') THEN
        DROP FUNCTION update_sources(INTEGER);
    END IF;
END
$$;
CREATE OR REPLACE FUNCTION update_sources(limit_val INTEGER)
RETURNS TABLE(source_id BIGINT, url TEXT, restricted INT, flags INT, config JSONB, last_updated_at TIMESTAMP) AS
$$
BEGIN
    RETURN QUERY
    WITH SelectedSources AS (
        SELECT s.source_id
        FROM Sources AS s
        WHERE s.disabled = FALSE
          AND (
               (s.last_updated_at IS NULL OR s.last_updated_at < NOW() - INTERVAL '3 days')
            OR (s.status = 'error' AND s.last_updated_at < NOW() - INTERVAL '15 minutes')
            OR (s.status = 'completed' AND s.last_updated_at < NOW() - INTERVAL '1 week')
            OR s.status = 'pending' OR s.status = 'new' OR s.status IS NULL
          )
        FOR UPDATE
        LIMIT limit_val
    )
    UPDATE Sources
        SET status = 'processing'
    WHERE Sources.source_id IN (SELECT SelectedSources.source_id FROM SelectedSources)
    RETURNING Sources.source_id, Sources.url, Sources.restricted, Sources.flags, Sources.config, Sources.last_updated_at;
END;
$$
LANGUAGE plpgsql;

--------------------------------------------------------------------------------
-- User and permissions setup

-- Helper functions:

CREATE OR REPLACE FUNCTION grant_sequence_permissions(schema_name text, target_user text)
RETURNS void AS
$$
DECLARE
    sequence_record record;
BEGIN
    FOR sequence_record IN SELECT sequence_name FROM information_schema.sequences WHERE sequence_schema = schema_name
    LOOP
        EXECUTE format('GRANT USAGE, SELECT, UPDATE ON SEQUENCE %I.%I TO %I', schema_name, sequence_record.sequence_name, target_user);
    END LOOP;
END;
$$
LANGUAGE plpgsql;


-- Creates a new user
CREATE USER :CROWLER_DB_USER WITH ENCRYPTED PASSWORD :'CROWLER_DB_PASSWORD';

-- Grants permissions to the user on the :"POSTGRES_DB" database
GRANT CONNECT ON DATABASE :"POSTGRES_DB" TO :CROWLER_DB_USER;
GRANT USAGE ON SCHEMA public TO :CROWLER_DB_USER;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO :CROWLER_DB_USER;
GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA public TO :CROWLER_DB_USER;
ALTER ROLE :CROWLER_DB_USER SET search_path TO public;
ALTER TABLE searchindex OWNER TO :CROWLER_DB_USER
ALTER TABLE keywordindex OWNER TO :CROWLER_DB_USER

-- Grants permissions to the user on the :"POSTGRES_DB" database
SELECT grant_sequence_permissions('public', :'CROWLER_DB_USER');
