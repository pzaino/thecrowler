CREATE TABLE Sources (
    source_id SERIAL PRIMARY KEY,
    url VARCHAR(255) NOT NULL,
    last_crawled_at TIMESTAMP,
    status VARCHAR(50),
    last_error VARCHAR(2048),
    last_error_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP,
    restricted BOOLEAN DEFAULT TRUE
);

CREATE TABLE SearchIndex (
    index_id SERIAL PRIMARY KEY,
    source_id INTEGER REFERENCES Sources(source_id),
    page_url VARCHAR(2048) NOT NULL UNIQUE,
    title VARCHAR(255),
    summary TEXT,
    content TEXT,
    snapshot_url VARCHAR(2048),
    indexed_at TIMESTAMP
);

CREATE TABLE MetaTags (
    metatag_id SERIAL PRIMARY KEY,
    index_id INTEGER REFERENCES SearchIndex(index_id),
    name VARCHAR(255),
    content TEXT
);

CREATE TABLE Keywords (
    keyword_id SERIAL PRIMARY KEY,
    keyword VARCHAR(100) NOT NULL UNIQUE
);

CREATE TABLE KeywordIndex (
    keyword_index_id SERIAL PRIMARY KEY,
    keyword_id INTEGER REFERENCES Keywords(keyword_id),
    index_id INTEGER REFERENCES SearchIndex(index_id),
    occurrences INTEGER
);
