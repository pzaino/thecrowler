-- MySQL setup script for the search engine database.
-- Adjusted for better performance and best practices.

--------------------------------------------------------------------------------
-- Database Tables setup

-- InformationSeeds table stores the seed information for the crawler
CREATE TABLE IF NOT EXISTS InformationSeed (
    information_seed_id BIGINT AUTO_INCREMENT PRIMARY KEY,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    category_id BIGINT DEFAULT 0 NOT NULL,      -- The category of the information seed.
    usr_id BIGINT DEFAULT 0 NOT NULL,           -- The user that created the information seed
    information_seed VARCHAR(256) NOT NULL,     -- The size of an information seed is limited to 256
                                                -- characters due to the fact that it's used to dork
                                                -- search engines for sources that may be related to
                                                -- the information seed.
    config JSON                                 -- Stores JSON document with all details about
                                                -- the information seed configuration for the crawler
);

-- Sources table stores the URLs or the information's seed to be crawled
CREATE TABLE IF NOT EXISTS Sources (
    source_id BIGINT AUTO_INCREMENT PRIMARY KEY,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    usr_id BIGINT DEFAULT 0 NOT NULL,           -- The user that created the source.
    category_id BIGINT DEFAULT 0 NOT NULL,      -- The category of the source.
    url TEXT NOT NULL UNIQUE,                   -- The Source URL.
    status VARCHAR(50) DEFAULT 'new' NOT NULL,  -- All new sources are set to 'new' by default.
    engine VARCHAR(256) DEFAULT '' NOT NULL,    -- The engine crawling the source.
    last_crawled_at TIMESTAMP,                  -- The last time the source was crawled.
    last_error TEXT,                            -- Last error message that occurred during crawling.
    last_error_at TIMESTAMP,                    -- The date/time of the last error occurred.
    restricted INT DEFAULT 2 NOT NULL,          -- 0 = fully restricted (just this URL)
                                                -- 1 = l3 domain restricted (everything within this
                                                --     URL l3 domain)
                                                -- 2 = l2 domain restricted
                                                -- 3 = l1 domain restricted
                                                -- 4 = no restrictions
    disabled BOOLEAN DEFAULT FALSE,             -- If the automatic re-crawling/re-scanning of the
                                                -- source is disabled.
    flags INT DEFAULT 0 NOT NULL,               -- Bitwise flags for the source (used for various
                                                -- purposes, included but not limited to the Rules).
    config JSON                                 -- Stores JSON document with all details about
                                                -- the source configuration for the crawler.
);

-- Owners table stores the information about the owners of the sources
CREATE TABLE IF NOT EXISTS Owners (
    owner_id BIGINT AUTO_INCREMENT PRIMARY KEY,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    usr_id BIGINT NOT NULL,                     -- The user that created the owner
    details_hash VARCHAR(64) UNIQUE NOT NULL,   -- SHA256 hash of the details for fast comparison
                                                -- and uniqueness.
    details JSON NOT NULL                       -- Stores JSON document with all details about
                                                -- the owner.
);

-- SearchIndex table stores the indexed information from the sources
CREATE TABLE IF NOT EXISTS SearchIndex (
    index_id BIGINT AUTO_INCREMENT PRIMARY KEY,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    page_url TEXT NOT NULL UNIQUE,              -- Using TEXT for long URLs
    title VARCHAR(255),                         -- Page title might be NULL
    summary TEXT NOT NULL,                      -- Assuming summary is always required
    detected_type VARCHAR(8),                   -- (content type) denormalized for fast searches
    detected_lang VARCHAR(8)                    -- (URI language) denormalized for fast searches
);

-- Category table stores the categories (and subcategories) for the sources
CREATE TABLE IF NOT EXISTS Category (
    category_id BIGINT AUTO_INCREMENT PRIMARY KEY,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    name VARCHAR(255) UNIQUE NOT NULL,
    description TEXT,
    parent_id BIGINT,
    FOREIGN KEY(parent_id) REFERENCES Category(category_id) ON DELETE SET NULL
);

-- NetInfo table stores the network information retrieved from the sources
CREATE TABLE IF NOT EXISTS NetInfo (
    netinfo_id BIGINT AUTO_INCREMENT PRIMARY KEY,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    details_hash VARCHAR(64) UNIQUE NOT NULL,   -- SHA256 hash of the details for fast comparison
                                                -- and uniqueness.
    details JSON NOT NULL
);

-- HTTPInfo table stores the HTTP header information retrieved from the sources
CREATE TABLE IF NOT EXISTS HTTPInfo (
    httpinfo_id BIGINT AUTO_INCREMENT PRIMARY KEY,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    details_hash VARCHAR(64) UNIQUE NOT NULL,   -- SHA256 hash of the details for fast comparison
                                                -- and uniqueness
    details JSON NOT NULL
);

-- Screenshots table stores the screenshots details of the indexed pages
CREATE TABLE IF NOT EXISTS Screenshots (
    screenshot_id BIGINT AUTO_INCREMENT PRIMARY KEY,
    index_id BIGINT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    type VARCHAR(10) NOT NULL DEFAULT 'desktop',
    screenshot_link TEXT NOT NULL,
    height INT NOT NULL DEFAULT 0,
    width INT NOT NULL DEFAULT 0,
    byte_size INT NOT NULL DEFAULT 0,
    thumbnail_height INT NOT NULL DEFAULT 0,
    thumbnail_width INT NOT NULL DEFAULT 0,
    thumbnail_link TEXT NOT NULL DEFAULT '',
    format VARCHAR(10) NOT NULL DEFAULT 'png',
    FOREIGN KEY (index_id) REFERENCES SearchIndex(index_id) ON DELETE CASCADE
);

-- WebObjects table stores all types of web objects found in the indexed pages
-- This includes scripts, styles, images, iframes, HTML etc.
CREATE TABLE IF NOT EXISTS WebObjects (
    object_id BIGINT AUTO_INCREMENT PRIMARY KEY,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    object_link TEXT NOT NULL DEFAULT 'db',     -- The link to where the object is stored if not
                                                -- in the DB.
    object_type VARCHAR(255) NOT NULL DEFAULT 'text/html', -- The type of the object, for fast searches
    object_hash VARCHAR(64) UNIQUE NOT NULL,    -- SHA256 hash of the object for fast comparison
                                                -- and uniqueness.
    object_content TEXT,                        -- The actual content of the object, nullable if
                                                -- stored externally.
    object_html TEXT,                           -- The HTML content of the object, nullable if
                                                -- stored externally.
    details JSON NOT NULL                       -- Stores JSON document with all details about
                                                -- the object.
);

-- MetaTags table stores the meta tags from the SearchIndex
CREATE TABLE IF NOT EXISTS MetaTags (
    metatag_id BIGINT AUTO_INCREMENT PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    content TEXT NOT NULL,
    UNIQUE(name, content)                       -- Ensure that each name-content pair is unique
);

-- Keywords table stores all the found keywords during an indexing
CREATE TABLE IF NOT EXISTS Keywords (
    keyword_id BIGINT AUTO_INCREMENT PRIMARY KEY,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    keyword VARCHAR(256) NOT NULL UNIQUE
);

-- Events table stores the events generated by the system
CREATE TABLE IF NOT EXISTS Events (
    event_sha256 CHAR(64) PRIMARY KEY,
    source_id BIGINT,
    event_type VARCHAR(255) NOT NULL,
    event_severity VARCHAR(50) NOT NULL,
    event_timestamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    event_details JSON NOT NULL,
    FOREIGN KEY(source_id) REFERENCES Sources(source_id) ON DELETE CASCADE
);

----------------------------------------
-- Relationship tables

-- SourceInformationSeedIndex table stores the relationship between sources and their information seeds
CREATE TABLE IF NOT EXISTS SourceInformationSeedIndex (
    source_information_seed_id BIGINT AUTO_INCREMENT PRIMARY KEY,
    source_id BIGINT NOT NULL,
    information_seed_id BIGINT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE (source_id, information_seed_id),
    FOREIGN KEY(source_id) REFERENCES Sources(source_id) ON DELETE CASCADE,
    FOREIGN KEY(information_seed_id) REFERENCES InformationSeed(information_seed_id) ON DELETE CASCADE
);

-- SourceOwnerIndex table stores the relationship between sources and their owners
CREATE TABLE IF NOT EXISTS SourceOwnerIndex (
    source_owner_id BIGINT AUTO_INCREMENT PRIMARY KEY,
    source_id BIGINT NOT NULL,
    owner_id BIGINT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE(source_id, owner_id),
    FOREIGN KEY(source_id) REFERENCES Sources(source_id) ON DELETE CASCADE,
    FOREIGN KEY(owner_id) REFERENCES Owners(owner_id) ON DELETE CASCADE
);

-- SourceSearchIndex table stores the relationship between sources and the indexed pages
CREATE TABLE IF NOT EXISTS SourceSearchIndex (
    ss_index_id BIGINT AUTO_INCREMENT PRIMARY KEY,
    source_id BIGINT NOT NULL,
    index_id BIGINT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE(source_id, index_id),
    FOREIGN KEY(source_id) REFERENCES Sources(source_id) ON DELETE CASCADE,
    FOREIGN KEY(index_id) REFERENCES SearchIndex(index_id) ON DELETE CASCADE
);

-- SourceCategoryIndex table stores the relationship between sources and their categories
CREATE TABLE IF NOT EXISTS SourceCategoryIndex (
    source_category_id BIGINT AUTO_INCREMENT PRIMARY KEY,
    source_id BIGINT NOT NULL,
    category_id BIGINT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE(source_id, category_id),
    FOREIGN KEY(source_id) REFERENCES Sources(source_id) ON DELETE CASCADE,
    FOREIGN KEY(category_id) REFERENCES Category(category_id) ON DELETE CASCADE
);

-- WebObjectsIndex table stores the relationship between indexed pages and the objects found in them
CREATE TABLE IF NOT EXISTS WebObjectsIndex (
    page_object_id BIGINT AUTO_INCREMENT PRIMARY KEY,
    index_id BIGINT NOT NULL,
    object_id BIGINT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE(index_id, object_id),
    FOREIGN KEY(index_id) REFERENCES SearchIndex(index_id) ON DELETE CASCADE,
    FOREIGN KEY(object_id) REFERENCES WebObjects(object_id) ON DELETE CASCADE
);

-- Relationship table between SearchIndex and MetaTags
CREATE TABLE IF NOT EXISTS MetaTagsIndex (
    sim_id BIGINT AUTO_INCREMENT PRIMARY KEY,
    index_id BIGINT NOT NULL,
    metatag_id BIGINT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    UNIQUE(index_id, metatag_id),
    FOREIGN KEY(index_id) REFERENCES SearchIndex(index_id) ON DELETE CASCADE,
    FOREIGN KEY(metatag_id) REFERENCES MetaTags(metatag_id) ON DELETE CASCADE
);

-- KeywordIndex table stores the relationship between keywords and the indexed pages
CREATE TABLE IF NOT EXISTS KeywordIndex (
    keyword_index_id BIGINT AUTO_INCREMENT PRIMARY KEY,
    keyword_id BIGINT,
    index_id BIGINT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    occurrences INT,
    UNIQUE(keyword_id, index_id),
    FOREIGN KEY(index_id) REFERENCES SearchIndex(index_id) ON DELETE CASCADE,
    FOREIGN KEY(keyword_id) REFERENCES Keywords(keyword_id) ON DELETE CASCADE
);

-- NetInfoIndex table stores the relationship between network information and the indexed pages
CREATE TABLE IF NOT EXISTS NetInfoIndex (
    netinfo_index_id BIGINT AUTO_INCREMENT PRIMARY KEY,
    netinfo_id BIGINT,
    index_id BIGINT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE(netinfo_id, index_id),
    FOREIGN KEY(index_id) REFERENCES SearchIndex(index_id) ON DELETE CASCADE,
    FOREIGN KEY(netinfo_id) REFERENCES NetInfo(netinfo_id) ON DELETE CASCADE
);

-- HTTPInfoIndex table stores the relationship between HTTP information and the indexed pages
CREATE TABLE IF NOT EXISTS HTTPInfoIndex (
    httpinfo_index_id BIGINT AUTO_INCREMENT PRIMARY KEY,
    httpinfo_id BIGINT,
    index_id BIGINT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE(httpinfo_id, index_id),
    FOREIGN KEY(index_id) REFERENCES SearchIndex(index_id) ON DELETE CASCADE,
    FOREIGN KEY(httpinfo_id) REFERENCES HTTPInfo(httpinfo_id) ON DELETE CASCADE
);

--------------------------------------------------------------------------------
-- Indexes and triggers setup

-- Creates an index for the WebObjects table on the object_id column
CREATE INDEX IF NOT EXISTS idx_webobjects_object_id ON WebObjects (object_id);

-- Creates an index for the WebObjectsIndex table on the object_id column
CREATE INDEX IF NOT EXISTS idx_webobjectsindex_object_id ON WebObjectsIndex (object_id);

-- Creates an index for the WebObjectsIndex table on the index_id column
CREATE INDEX IF NOT EXISTS idx_webobjectsindex_index_id ON WebObjectsIndex (index_id);

-- Creates an index for the SearchIndex table on the index_id column
CREATE INDEX IF NOT EXISTS idx_searchindex_index_id ON SearchIndex (index_id);

-- Creates an index for the SearchIndex table on the page_url column (for lower-cased searches)
CREATE INDEX IF NOT EXISTS idx_searchindex_page_url_lower ON SearchIndex (LOWER(page_url));

-- Creates an index for the SearchIndex table on the title column (for lower-cased searches)
CREATE INDEX IF NOT EXISTS idx_searchindex_title_lower ON SearchIndex (LOWER(title));

-- Creates an index for the SearchIndex table on the summary column (for lower-cased searches)
CREATE INDEX IF NOT EXISTS idx_searchindex_summary_lower ON SearchIndex (LOWER(summary));

-- Creates an index for the Keywords table on the keyword column (for lower-cased searches)
CREATE INDEX IF NOT EXISTS idx_keywords_keyword_lower ON Keywords (LOWER(keyword));

-- Creates an index for the KeywordIndex table on the keyword_id column
CREATE INDEX IF NOT EXISTS idx_keywordindex_keyword_id ON KeywordIndex (keyword_id);

-- Creates an index for the KeywordIndex table on the index_id column
CREATE INDEX IF NOT EXISTS idx_keywordindex_index_id ON KeywordIndex (index_id);

-- Creates an index for the Sources url column
CREATE INDEX IF NOT EXISTS idx_sources_url ON Sources(url);

-- Creates an index for the Sources status column
CREATE INDEX IF NOT EXISTS idx_sources_status ON Sources(status);

-- Creates an index for the Sources last_crawled_at column
CREATE INDEX IF NOT EXISTS idx_sources_last_crawled_at ON Sources(last_crawled_at);

-- Creates an index for the Sources source_id column
CREATE INDEX IF NOT EXISTS idx_sources_source_id ON Sources(source_id);

-- Creates a gin index for the Source config column
CREATE INDEX IF NOT EXISTS idx_sources_config ON Sources(config);

-- Creates an index for the Owners details column
CREATE INDEX IF NOT EXISTS idx_owners_details ON Owners(details);

-- Creates an index for the Owners details_hash column
CREATE INDEX IF NOT EXISTS idx_owners_details_hash ON Owners(details_hash);

-- Creates an index for the Owners last_updated_at column
CREATE INDEX IF NOT EXISTS idx_owners_last_updated_at ON Owners(last_updated_at);

-- Creates an index for the Owners created_at column
CREATE INDEX IF NOT EXISTS idx_owners_created_at ON Owners(created_at);

-- Creates an index for the NetInfo last_updated_at column
CREATE INDEX IF NOT EXISTS idx_netinfo_last_updated_at ON NetInfo(last_updated_at);

-- Creates an index for the NetInfo created_at column
CREATE INDEX IF NOT EXISTS idx_netinfo_created_at ON NetInfo(created_at);

-- Creates an index for the NetInfo details_hash column
CREATE INDEX IF NOT EXISTS idx_netinfo_details_hash ON NetInfo(details_hash);

-- Creates an index for the details column in the NetInfo table
CREATE INDEX IF NOT EXISTS idx_json_netinfo_details ON NetInfo(details);

-- Creates an index for the HTTPInfo last_updated_at column
CREATE INDEX IF NOT EXISTS idx_httpinfo_last_updated_at ON HTTPInfo(last_updated_at);

-- Creates an index for the HTTPInfo created_at column
CREATE INDEX IF NOT EXISTS idx_httpinfo_created_at ON HTTPInfo(created_at);

-- Creates an index for the HTTPInfo details_hash column
CREATE INDEX IF NOT EXISTS idx_httpinfo_details_hash ON HTTPInfo(details_hash);

-- Creates an index for the HTTPInfo details column
CREATE INDEX IF NOT EXISTS idx_json_httpinfo_details ON HTTPInfo(details);

-- Creates an index for the SearchIndex title column
CREATE INDEX IF NOT EXISTS idx_searchindex_title ON SearchIndex(title);

-- Creates an index for the SearchIndex summary column
CREATE INDEX IF NOT EXISTS idx_searchindex_summary ON SearchIndex(summary(1000));

-- Creates an index for the SearchIndex last_updated_at column
CREATE INDEX IF NOT EXISTS idx_searchindex_last_updated_at ON SearchIndex(last_updated_at);

-- Creates an index for the Screenshots index_id column
CREATE INDEX IF NOT EXISTS idx_screenshots_index_id ON Screenshots(index_id);

-- Creates an index for the Screenshots screenshot_link column
CREATE INDEX IF NOT EXISTS idx_screenshots_screenshot_link ON Screenshots(screenshot_link);

-- Creates an index for the Screenshots last_updated_at column
CREATE INDEX IF NOT EXISTS idx_screenshots_last_updated_at ON Screenshots(last_updated_at);

-- Creates an index for the Screenshots created_at column
CREATE INDEX IF NOT EXISTS idx_screenshots_created_at ON Screenshots(created_at);

-- Creates an index for the WebObjects object_link column
CREATE INDEX IF NOT EXISTS idx_webobjects_object_link ON WebObjects(object_link);

-- Creates an index for the WebObjects object_type column
CREATE INDEX IF NOT EXISTS idx_webobjects_object_type ON WebObjects(object_type);

-- Create an index for the WebObjects object_hash column
CREATE INDEX IF NOT EXISTS idx_webobjects_object_hash ON WebObjects(object_hash);

-- Creates an index for the WebObjects object_content column
CREATE INDEX IF NOT EXISTS idx_webobjects_object_content ON WebObjects(object_content(1024));

-- Creates an index for the WebObjects created_at column
CREATE INDEX IF NOT EXISTS idx_webobjects_created_at ON WebObjects(created_at);

-- Creates an index for the WebObjects last_updated_at column
CREATE INDEX IF NOT EXISTS idx_webobjects_last_updated_at ON WebObjects(last_updated_at);

-- Creates an index for the details column in the WebObjects table
CREATE INDEX IF NOT EXISTS idx_json_webobjects_details ON WebObjects(details);

-- Creates an index for the MetaTags name column
CREATE INDEX IF NOT EXISTS idx_metatags_name ON MetaTags(name);

-- Creates an index for the MetaTags content column
CREATE INDEX IF NOT EXISTS idx_metatags_content ON MetaTags(content(1024));

-- Creates and index for the Keywords occurrences column to help
-- with keywords analysis
CREATE INDEX IF NOT EXISTS idx_keywordindex_occurrences ON KeywordIndex(occurrences);

-- Creates an index for SourceOwnerIndex owner_id column
CREATE INDEX IF NOT EXISTS idx_sourceownerindex_owner_id ON SourceOwnerIndex(owner_id);

-- Creates an index for SourceOwnerIndex source_id column
CREATE INDEX IF NOT EXISTS idx_sourceownerindex_source_id ON SourceOwnerIndex(source_id);

-- Creates an index for the SourceSearchIndex source_id column
CREATE INDEX IF NOT EXISTS idx_ssi_source_id ON SourceSearchIndex(source_id);

-- Creates an index for the SourceSearchIndex index_id column
CREATE INDEX IF NOT EXISTS idx_ssi_index_id ON SourceSearchIndex(index_id);

-- Creates an index for the WebObjectsIndex index_id column
CREATE INDEX IF NOT EXISTS idx_woi_index_id ON WebObjectsIndex(index_id);

-- Creates an index for the WebObjectsIndex object_id column
CREATE INDEX IF NOT EXISTS idx_woi_object_id ON WebObjectsIndex(object_id);

--------------------------------------------------------------------------------
-- Triggers setup

-- Creates a trigger to update the last_updated_at column on Sources table
CREATE TRIGGER trg_update_sources_last_updated BEFORE UPDATE ON Sources
FOR EACH ROW SET NEW.last_updated_at = CURRENT_TIMESTAMP;

-- Create a trigger to update the last_updated_at column for InformationSeed table
CREATE TRIGGER trg_update_information_seed_last_updated BEFORE UPDATE ON InformationSeed
FOR EACH ROW SET NEW.last_updated_at = CURRENT_TIMESTAMP;

-- Create a trigger to update the last_updated_at column for SourceInformationSeedIndex table
CREATE TRIGGER trg_update_sourceinformationseedidx_last_updated BEFORE UPDATE ON SourceInformationSeedIndex
FOR EACH ROW SET NEW.last_updated_at = CURRENT_TIMESTAMP;

-- Creates a trigger to update the last_updated_at column on SearchIndex table
CREATE TRIGGER trg_update_searchindex_last_updated BEFORE UPDATE ON SearchIndex
FOR EACH ROW SET NEW.last_updated_at = CURRENT_TIMESTAMP;

-- Creates a trigger to update the last_updated_at column on Owners table
CREATE TRIGGER trg_update_owners_last_updated BEFORE UPDATE ON Owners
FOR EACH ROW SET NEW.last_updated_at = CURRENT_TIMESTAMP;

-- Creates a trigger to update the last_updated_at column on NetInfo table
CREATE TRIGGER trg_update_netinfo_last_updated BEFORE UPDATE ON NetInfo
FOR EACH ROW SET NEW.last_updated_at = CURRENT_TIMESTAMP;

-- Creates a trigger to update the last_updated_at column on HTTPInfo table
CREATE TRIGGER trg_update_httpinfo_last_updated BEFORE UPDATE ON HTTPInfo
FOR EACH ROW SET NEW.last_updated_at = CURRENT_TIMESTAMP;

-- Creates a trigger to update the last_updated_at column on WebObjects table
CREATE TRIGGER trg_update_webobjects_last_updated BEFORE UPDATE ON WebObjects
FOR EACH ROW SET NEW.last_updated_at = CURRENT_TIMESTAMP;

-- Creates a trigger to update the last_updated_at column on MetaTags table
CREATE TRIGGER trg_update_metatags_last_updated BEFORE UPDATE ON MetaTags
FOR EACH ROW SET NEW.last_updated_at = CURRENT_TIMESTAMP;

-- Creates a trigger to update the last_updated_at column on Keywords table
CREATE TRIGGER trg_update_keywords_last_updated BEFORE UPDATE ON Keywords
FOR EACH ROW SET NEW.last_updated_at = CURRENT_TIMESTAMP;

-- Creates a trigger to update the last_updated_at column on SourceOwnerIndex table
CREATE TRIGGER trg_update_sourceowner_last_updated BEFORE UPDATE ON SourceOwnerIndex
FOR EACH ROW SET NEW.last_updated_at = CURRENT_TIMESTAMP;

-- Creates a trigger to update the last_updated_at column on SourceSearchIndex table
CREATE TRIGGER trg_update_ssi_last_updated BEFORE UPDATE ON SourceSearchIndex
FOR EACH ROW SET NEW.last_updated_at = CURRENT_TIMESTAMP;

-- Creates a trigger to update the last_updated_at column on KeywordIndex table
CREATE TRIGGER trg_update_keywordindex_last_updated BEFORE UPDATE ON KeywordIndex
FOR EACH ROW SET NEW.last_updated_at = CURRENT_TIMESTAMP;

--------------------------------------------------------------------------------
-- Full Text Search setup

-- Add a FULLTEXT index for the full text search
ALTER TABLE SearchIndex ADD FULLTEXT INDEX idx_searchindex_fulltext (page_url, title, summary);

-- Add a FULLTEXT index for the WebObjects table
ALTER TABLE WebObjects ADD FULLTEXT INDEX idx_webobjects_fulltext (object_content);

--------------------------------------------------------------------------------
-- Functions and Procedures setup

-- Creates a procedure to fetch and update the sources as an atomic operation
DELIMITER //
CREATE PROCEDURE update_sources(IN limit_val INT, IN engineID VARCHAR(255))
BEGIN
    DECLARE done INT DEFAULT FALSE;
    DECLARE src_id BIGINT;
    DECLARE src_url TEXT;
    DECLARE src_restricted INT;
    DECLARE src_flags INT;
    DECLARE src_config JSON;
    DECLARE src_last_updated TIMESTAMP;

    DECLARE cur CURSOR FOR
        SELECT source_id, url, restricted, flags, config, last_updated_at
        FROM Sources
        WHERE disabled = FALSE
          AND (
               (last_updated_at IS NULL OR last_updated_at < NOW() - INTERVAL 3 DAY)
            OR (status = 'error' AND last_updated_at < NOW() - INTERVAL 15 MINUTE)
            OR (status = 'completed' AND last_updated_at < NOW() - INTERVAL 1 WEEK)
            OR status = 'pending' OR status = 'new' OR status IS NULL
          )
        LIMIT limit_val
        FOR UPDATE;

    DECLARE CONTINUE HANDLER FOR NOT FOUND SET done = TRUE;

    OPEN cur;

    read_loop: LOOP
        FETCH cur INTO src_id, src_url, src_restricted, src_flags, src_config, src_last_updated;
        IF done THEN
            LEAVE read_loop;
        END IF;

        UPDATE Sources
        SET status = 'processing', engine = engineID
        WHERE source_id = src_id;

        -- Return the updated rows
        SELECT src_id AS source_id, src_url AS url, src_restricted AS restricted, src_flags AS flags, src_config AS config, src_last_updated AS last_updated_at;
    END LOOP;

    CLOSE cur;
END //
DELIMITER ;

--------------------------------------------------------------------------------
-- Special function for data correlation

DELIMITER //
CREATE FUNCTION find_correlated_sources_by_domain(domain TEXT)
RETURNS JSON
BEGIN
    DECLARE result JSON DEFAULT JSON_ARRAY();

    -- Get partner sources from NetInfo
    SET result = JSON_ARRAYAGG(JSON_OBJECT('source_id', s.source_id, 'url', s.url)) FROM
        (SELECT DISTINCT s.source_id, s.url
        FROM NetInfo ni
        JOIN NetInfoIndex nii ON ni.netinfo_id = nii.netinfo_id
        JOIN SourceSearchIndex ssi ON nii.index_id = ssi.index_id
        JOIN Sources s ON ssi.source_id = s.source_id
        WHERE JSON_CONTAINS(ni.details, JSON_QUOTE(domain))) AS partners;

    -- Get partner sources from HTTPInfo
    SET result = JSON_ARRAYAGG(JSON_OBJECT('source_id', s.source_id, 'url', s.url)) FROM
        (SELECT DISTINCT s.source_id, s.url
        FROM HTTPInfo hi
        JOIN HTTPInfoIndex hii ON hi.httpinfo_id = hii.httpinfo_id
        JOIN SourceSearchIndex ssi ON hii.index_id = ssi.index_id
        JOIN Sources s ON ssi.source_id = s.source_id
        WHERE JSON_CONTAINS(hi.details, JSON_QUOTE(domain))) AS partners;

    -- Get partner sources from WebObjects
    SET result = JSON_ARRAYAGG(JSON_OBJECT('source_id', s.source_id, 'url', s.url)) FROM
        (SELECT DISTINCT s.source_id, s.url
        FROM WebObjects wo
        JOIN WebObjectsIndex woi ON wo.object_id = woi.object_id
        JOIN SourceSearchIndex ssi ON woi.index_id = ssi.index_id
        JOIN Sources s ON ssi.source_id = s.source_id
        WHERE JSON_CONTAINS(wo.details, JSON_QUOTE(domain))) AS partners;

    RETURN result;
END //
DELIMITER ;

--------------------------------------------------------------------------------
-- User and permissions setup

-- Creates a new user
CREATE USER 'your_username'@'localhost' IDENTIFIED BY 'your_password';

-- Grants permissions to the user on the SitesIndex database
GRANT ALL PRIVILEGES ON SitesIndex.* TO 'your_username'@'localhost';
FLUSH PRIVILEGES;
