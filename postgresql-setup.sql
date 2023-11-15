CREATE TABLE Sources (
    source_id SERIAL PRIMARY KEY,
    url VARCHAR(255) NOT NULL,
    last_crawled_at TIMESTAMP,
    status VARCHAR(50)
);

CREATE TABLE SearchIndex (
    index_id SERIAL PRIMARY KEY,
    source_id INTEGER REFERENCES Sources(source_id),
    page_url VARCHAR(2048) NOT NULL,
    title VARCHAR(255),
    content TEXT,
    snapshot_url VARCHAR(2048),
    indexed_at TIMESTAMP
);

CREATE TABLE Keywords (
    keyword_id SERIAL PRIMARY KEY,
    keyword VARCHAR(100) NOT NULL
);

CREATE TABLE KeywordIndex (
    keyword_index_id SERIAL PRIMARY KEY,
    keyword_id INTEGER REFERENCES Keywords(keyword_id),
    index_id INTEGER REFERENCES SearchIndex(index_id),
    occurrences INTEGER
);
