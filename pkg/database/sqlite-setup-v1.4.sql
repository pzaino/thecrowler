-- SQLite setup script for the search engine database.
-- Adjusted for better performance and best practices.

--------------------------------------------------------------------------------
-- Database Tables setup
SET ANSI_NULLS ON;
SET QUOTED_IDENTIFIER ON;
SET NOCOUNT ON;
SET TRANSACTION ISOLATION LEVEL READ UNCOMMITTED;

-- InformationSeeds table stores the seed information for the crawler
CREATE TABLE IF NOT EXISTS InformationSeed (
    information_seed_id INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    category_id INTEGER DEFAULT 0 NOT NULL,     -- The category of the information seed.
    usr_id INTEGER DEFAULT 0 NOT NULL,          -- The user that created the information seed
    information_seed VARCHAR(256) NOT NULL,     -- The size of an information seed is limited to 256
                                                -- characters due to the fact that it's used to dork
                                                -- search engines for sources that may be related to
                                                -- the information seed.
    config TEXT                                 -- Stores JSON document with all details about
                                                -- the information seed configuration for the crawler
);

-- Sources table stores the URLs or the information's seed to be crawled
CREATE TABLE IF NOT EXISTS Sources (
    source_id INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP,
    usr_id INTEGER DEFAULT 0 NOT NULL,          -- The user that created the source.
    category_id INTEGER DEFAULT 0 NOT NULL,     -- The category of the source.
    url TEXT NOT NULL UNIQUE,                   -- The Source URL.
    status VARCHAR(50) DEFAULT 'new' NOT NULL,  -- All new sources are set to 'new' by default.
    engine VARCHAR(256) DEFAULT '' NOT NULL,    -- The engine crawling the source.
    last_crawled_at TIMESTAMP,                  -- The last time the source was crawled.
    last_error TEXT,                            -- Last error message that occurred during crawling.
    last_error_at TIMESTAMP,                    -- The date/time of the last error occurred.
    restricted INTEGER DEFAULT 2 NOT NULL,      -- 0 = fully restricted (just this URL)
                                                -- 1 = l3 domain restricted (everything within this
                                                --     URL l3 domain)
                                                -- 2 = l2 domain restricted
                                                -- 3 = l1 domain restricted
                                                -- 4 = no restrictions
    disabled BOOLEAN DEFAULT FALSE,             -- If the automatic re-crawling/re-scanning of the
                                                -- source is disabled.
    flags INTEGER DEFAULT 0 NOT NULL,           -- Bitwise flags for the source (used for various
                                                -- purposes, included but not limited to the Rules).
    config TEXT                                 -- Stores JSON document with all details about
                                                -- the source configuration for the crawler.
);

-- Owners table stores the information about the owners of the sources
CREATE TABLE IF NOT EXISTS Owners (
    owner_id INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    usr_id INTEGER NOT NULL,                     -- The user that created the owner
    details_hash VARCHAR(64) UNIQUE NOT NULL,   -- SHA256 hash of the details for fast comparison
                                                -- and uniqueness.
    details TEXT NOT NULL                       -- Stores JSON document with all details about
                                                -- the owner.
);

-- SearchIndex table stores the indexed information from the sources
CREATE TABLE IF NOT EXISTS SearchIndex (
    index_id INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    page_url TEXT NOT NULL UNIQUE,              -- Using TEXT for long URLs
    title VARCHAR(255),                         -- Page title might be NULL
    summary TEXT NOT NULL,                      -- Assuming summary is always required
    detected_type VARCHAR(8),                   -- (content type) denormalized for fast searches
    detected_lang VARCHAR(8)                    -- (URI language) denormalized for fast searches
);

-- Category table stores the categories (and subcategories) for the sources
CREATE TABLE IF NOT EXISTS Category (
    category_id INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    name VARCHAR(255) UNIQUE NOT NULL,
    description TEXT,
    parent_id INTEGER,
    FOREIGN KEY(parent_id) REFERENCES Category(category_id) ON DELETE SET NULL
);

-- NetInfo table stores the network information retrieved from the sources
CREATE TABLE IF NOT EXISTS NetInfo (
    netinfo_id INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    details_hash VARCHAR(64) UNIQUE NOT NULL,   -- SHA256 hash of the details for fast comparison
                                                -- and uniqueness.
    details TEXT NOT NULL
);

-- HTTPInfo table stores the HTTP header information retrieved from the sources
CREATE TABLE IF NOT EXISTS HTTPInfo (
    httpinfo_id INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    details_hash VARCHAR(64) UNIQUE NOT NULL,   -- SHA256 hash of the details for fast comparison
                                                -- and uniqueness
    details TEXT NOT NULL
);

-- Screenshots table stores the screenshots details of the indexed pages
CREATE TABLE IF NOT EXISTS Screenshots (
    screenshot_id INTEGER PRIMARY KEY AUTOINCREMENT,
    index_id INTEGER,
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
    object_id INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    object_link TEXT NOT NULL DEFAULT 'db',     -- The link to where the object is stored if not
                                                -- in the DB.
    object_type VARCHAR(255) NOT NULL DEFAULT 'text/html', -- The type of the object, for fast searches
    object_hash VARCHAR(64) UNIQUE NOT NULL,    -- SHA256 hash of the object for fast comparison
                                                -- and uniqueness.
    object_content TEXT,                        -- The actual content of the object, nullable if
                                                -- stored externally.
    object_html TEXT,                            -- The HTML content of the object, nullable if
                                                -- stored externally.
    details TEXT NOT NULL                       -- Stores JSON document with all details about
                                                -- the object.
);

-- MetaTags table stores the meta tags from the SearchIndex
CREATE TABLE IF NOT EXISTS MetaTags (
    metatag_id INTEGER PRIMARY KEY AUTOINCREMENT,
    name VARCHAR(255) NOT NULL,
    content TEXT NOT NULL,
    UNIQUE(name, content)                       -- Ensure that each name-content pair is unique
);

-- Keywords table stores all the found keywords during an indexing
CREATE TABLE IF NOT EXISTS Keywords (
    keyword_id INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    keyword VARCHAR(256) NOT NULL UNIQUE
);

-- Events table stores the events generated by the system
CREATE TABLE IF NOT EXISTS Events (
    event_sha256 CHAR(64) PRIMARY KEY,
    source_id INTEGER,
    event_type VARCHAR(255) NOT NULL,
    event_severity VARCHAR(50) NOT NULL,
    event_timestamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    event_details TEXT NOT NULL,
    FOREIGN KEY(source_id) REFERENCES Sources(source_id) ON DELETE CASCADE
);

----------------------------------------
-- Relationship tables

-- SourceInformationSeedIndex table stores the relationship between sources and their information seeds
CREATE TABLE IF NOT EXISTS SourceInformationSeedIndex (
    source_information_seed_id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id INTEGER NOT NULL,
    information_seed_id INTEGER NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (source_id, information_seed_id),
    FOREIGN KEY(source_id) REFERENCES Sources(source_id) ON DELETE CASCADE,
    FOREIGN KEY(information_seed_id) REFERENCES InformationSeed(information_seed_id) ON DELETE CASCADE
);

-- SourceOwnerIndex table stores the relationship between sources and their owners
CREATE TABLE IF NOT EXISTS SourceOwnerIndex (
    source_owner_id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id INTEGER NOT NULL,
    owner_id INTEGER NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(source_id, owner_id),
    FOREIGN KEY(source_id) REFERENCES Sources(source_id) ON DELETE CASCADE,
    FOREIGN KEY(owner_id) REFERENCES Owners(owner_id) ON DELETE CASCADE
);

-- SourceSearchIndex table stores the relationship between sources and the indexed pages
CREATE TABLE IF NOT EXISTS SourceSearchIndex (
    ss_index_id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id INTEGER NOT NULL,
    index_id INTEGER NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(source_id, index_id),
    FOREIGN KEY(source_id) REFERENCES Sources(source_id) ON DELETE CASCADE,
    FOREIGN KEY(index_id) REFERENCES SearchIndex(index_id) ON DELETE CASCADE
);

-- SourceCategoryIndex table stores the relationship between sources and their categories
CREATE TABLE IF NOT EXISTS SourceCategoryIndex (
    source_category_id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id INTEGER NOT NULL,
    category_id INTEGER NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(source_id, category_id),
    FOREIGN KEY(source_id) REFERENCES Sources(source_id) ON DELETE CASCADE,
    FOREIGN KEY(category_id) REFERENCES Category(category_id) ON DELETE CASCADE
);

-- WebObjectsIndex table stores the relationship between indexed pages and the objects found in them
CREATE TABLE IF NOT EXISTS WebObjectsIndex (
    page_object_id INTEGER PRIMARY KEY AUTOINCREMENT,
    index_id INTEGER NOT NULL,
    object_id INTEGER NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(index_id, object_id),
    FOREIGN KEY(index_id) REFERENCES SearchIndex(index_id) ON DELETE CASCADE,
    FOREIGN KEY(object_id) REFERENCES WebObjects(object_id) ON DELETE CASCADE
);

-- Relationship table between SearchIndex and MetaTags
CREATE TABLE IF NOT EXISTS MetaTagsIndex (
    sim_id INTEGER PRIMARY KEY AUTOINCREMENT,
    index_id INTEGER NOT NULL,
    metatag_id INTEGER NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    UNIQUE(index_id, metatag_id),
    FOREIGN KEY(index_id) REFERENCES SearchIndex(index_id) ON DELETE CASCADE,
    FOREIGN KEY(metatag_id) REFERENCES MetaTags(metatag_id) ON DELETE CASCADE
);

-- KeywordIndex table stores the relationship between keywords and the indexed pages
CREATE TABLE IF NOT EXISTS KeywordIndex (
    keyword_index_id INTEGER PRIMARY KEY AUTOINCREMENT,
    keyword_id INTEGER,
    index_id INTEGER,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    occurrences INTEGER,
    UNIQUE(keyword_id, index_id),
    FOREIGN KEY(index_id) REFERENCES SearchIndex(index_id) ON DELETE CASCADE,
    FOREIGN KEY(keyword_id) REFERENCES Keywords(keyword_id) ON DELETE CASCADE
);

-- NetInfoIndex table stores the relationship between network information and the indexed pages
CREATE TABLE IF NOT EXISTS NetInfoIndex (
    netinfo_index_id INTEGER PRIMARY KEY AUTOINCREMENT,
    netinfo_id INTEGER,
    index_id INTEGER,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(netinfo_id, index_id),
    FOREIGN KEY(index_id) REFERENCES SearchIndex(index_id) ON DELETE CASCADE,
    FOREIGN KEY(netinfo_id) REFERENCES NetInfo(netinfo_id) ON DELETE CASCADE
);

-- HTTPInfoIndex table stores the relationship between HTTP information and the indexed pages
CREATE TABLE IF NOT EXISTS HTTPInfoIndex (
    httpinfo_index_id INTEGER PRIMARY KEY AUTOINCREMENT,
    httpinfo_id INTEGER,
    index_id INTEGER,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(httpinfo_id, index_id),
    FOREIGN KEY(index_id) REFERENCES SearchIndex(index_id) ON DELETE CASCADE,
    FOREIGN KEY(httpinfo_id) REFERENCES HTTPInfo(httpinfo_id) ON DELETE CASCADE
);
