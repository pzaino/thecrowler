# TheCROWler DB architecture

The CROWler uses a PostgreSQL database to store the data it collects. However
it's internal data API is designed to be database agnostic, so it could be
easily adapted to use other databases in the future.

Here below is a diagram of the database architecture:

```mermaid
erDiagram
    InformationSeed {
        BIGSERIAL information_seed_id PK
        TIMESTAMP created_at
        TIMESTAMP last_updated_at
        BIGINT category_id
        BIGINT usr_id
        VARCHAR information_seed
        JSONB config
    }

    Sources {
        BIGSERIAL source_id PK
        TIMESTAMP created_at
        TIMESTAMP last_updated_at
        BIGINT usr_id
        BIGINT category_id
        TEXT url
        VARCHAR status
        VARCHAR engine
        TIMESTAMP last_crawled_at
        TEXT last_error
        TIMESTAMP last_error_at
        INTEGER restricted
        BOOLEAN disabled
        INTEGER flags
        JSONB config
    }

    Owners {
        BIGSERIAL owner_id PK
        TIMESTAMP created_at
        TIMESTAMP last_updated_at
        BIGINT usr_id
        VARCHAR details_hash
        JSONB details
    }

    SearchIndex {
        BIGSERIAL index_id PK
        TIMESTAMP created_at
        TIMESTAMP last_updated_at
        TEXT page_url
        VARCHAR title
        TEXT summary
        VARCHAR detected_type
        VARCHAR detected_lang
        TSVECTOR tsv
    }

    Categories {
        BIGSERIAL category_id PK
        TIMESTAMP created_at
        TIMESTAMP last_updated_at
        VARCHAR name
        TEXT description
        BIGINT parent_id FK "REFERENCES Categories(category_id)"
    }

    NetInfo {
        BIGSERIAL netinfo_id PK
        TIMESTAMP created_at
        TIMESTAMP last_updated_at
        VARCHAR details_hash
        JSONB details
    }

    HTTPInfo {
        BIGSERIAL httpinfo_id PK
        TIMESTAMP created_at
        TIMESTAMP last_updated_at
        VARCHAR details_hash
        JSONB details
    }

    Screenshots {
        BIGSERIAL screenshot_id PK
        BIGINT index_id FK "REFERENCES SearchIndex(index_id)"
        TIMESTAMP created_at
        TIMESTAMP last_updated_at
        VARCHAR type
        TEXT screenshot_link
        INTEGER height
        INTEGER width
        INTEGER byte_size
        INTEGER thumbnail_height
        INTEGER thumbnail_width
        TEXT thumbnail_link
        VARCHAR format
    }

    WebObjects {
        BIGSERIAL object_id PK
        TIMESTAMP created_at
        TIMESTAMP last_updated_at
        TEXT object_link
        VARCHAR object_type
        VARCHAR object_hash
        TEXT object_content
        TEXT object_html
        JSONB details
    }

    MetaTags {
        BIGSERIAL metatag_id PK
        VARCHAR name
        TEXT content
    }

    Keywords {
        BIGSERIAL keyword_id PK
        TIMESTAMP created_at
        TIMESTAMP last_updated_at
        VARCHAR keyword
    }

    Events {
        CHAR event_sha256 PK
        TIMESTAMP created_at
        TIMESTAMP last_updated_at
        BIGINT source_id FK "REFERENCES Sources(source_id)"
        VARCHAR event_type
        VARCHAR event_severity
        TIMESTAMP event_timestamp
        JSONB details
    }

    SourceInformationSeedIndex {
        BIGSERIAL source_information_seed_id PK
        BIGINT source_id FK "REFERENCES Sources(source_id)"
        BIGINT information_seed_id FK "REFERENCES InformationSeed(information_seed_id)"
        TIMESTAMP created_at
        TIMESTAMP last_updated_at
    }

    SourceOwnerIndex {
        BIGSERIAL source_owner_id PK
        BIGINT source_id FK "REFERENCES Sources(source_id)"
        BIGINT owner_id FK "REFERENCES Owners(owner_id)"
        TIMESTAMP created_at
        TIMESTAMP last_updated_at
    }

    SourceSearchIndex {
        BIGSERIAL ss_index_id PK
        BIGINT source_id FK "REFERENCES Sources(source_id)"
        BIGINT index_id FK "REFERENCES SearchIndex(index_id)"
        TIMESTAMP created_at
        TIMESTAMP last_updated_at
    }

    SourceCategoryIndex {
        BIGSERIAL source_category_id PK
        BIGINT source_id FK "REFERENCES Sources(source_id)"
        BIGINT category_id FK "REFERENCES Categories(category_id)"
        TIMESTAMP created_at
        TIMESTAMP last_updated_at
    }

    WebObjectsIndex {
        BIGSERIAL page_object_id PK
        BIGINT index_id FK "REFERENCES SearchIndex(index_id)"
        BIGINT object_id FK "REFERENCES WebObjects(object_id)"
        TIMESTAMP created_at
        TIMESTAMP last_updated_at
    }

    MetaTagsIndex {
        BIGSERIAL sim_id PK
        BIGINT index_id FK "REFERENCES SearchIndex(index_id)"
        BIGINT metatag_id FK "REFERENCES MetaTags(metatag_id)"
        TIMESTAMP created_at
    }

    KeywordIndex {
        BIGSERIAL keyword_index_id PK
        BIGINT keyword_id FK "REFERENCES Keywords(keyword_id)"
        BIGINT index_id FK "REFERENCES SearchIndex(index_id)"
        TIMESTAMP created_at
        TIMESTAMP last_updated_at
        INTEGER occurrences
    }

    NetInfoIndex {
        BIGSERIAL netinfo_index_id PK
        BIGINT netinfo_id FK "REFERENCES NetInfo(netinfo_id)"
        BIGINT index_id FK "REFERENCES SearchIndex(index_id)"
        TIMESTAMP created_at
        TIMESTAMP last_updated_at
    }

    HTTPInfoIndex {
        BIGSERIAL httpinfo_index_id PK
        BIGINT httpinfo_id FK "REFERENCES HTTPInfo(httpinfo_id)"
        BIGINT index_id FK "REFERENCES SearchIndex(index_id)"
        TIMESTAMP created_at
        TIMESTAMP last_updated_at
    }

    Categories ||--|{ Categories : "parent_id"
    InformationSeed ||--o{ Categories : "category_id"
    InformationSeed ||--o{ Sources : "usr_id"
    Sources ||--o{ Categories : "category_id"
    Sources ||--o{ Owners : "usr_id"
    Owners ||--o{ Sources : "usr_id"
    Owners ||--o{ Owners : "usr_id"
    SourceInformationSeedIndex ||--|{ InformationSeed : "information_seed_id"
    SourceInformationSeedIndex ||--|{ Sources : "source_id"
    SourceOwnerIndex ||--|{ Sources : "source_id"
    SourceOwnerIndex ||--|{ Owners : "owner_id"
    SourceSearchIndex ||--|{ Sources : "source_id"
    SourceSearchIndex ||--|{ SearchIndex : "index_id"
    SourceCategoryIndex ||--|{ Sources : "source_id"
    SourceCategoryIndex ||--|{ Categories : "category_id"
    WebObjectsIndex ||--|{ WebObjects : "object_id"
    WebObjectsIndex ||--|{ SearchIndex : "index_id"
    MetaTagsIndex ||--|{ MetaTags : "metatag_id"
    MetaTagsIndex ||--|{ SearchIndex : "index_id"
    KeywordIndex ||--|{ Keywords : "keyword_id"
    KeywordIndex ||--|{ SearchIndex : "index_id"
    NetInfoIndex ||--|{ NetInfo : "netinfo_id"
    NetInfoIndex ||--|{ SearchIndex : "index_id"
    HTTPInfoIndex ||--|{ HTTPInfo : "httpinfo_id"
    HTTPInfoIndex ||--|{ SearchIndex : "index_id"
    Screenshots ||--|{ SearchIndex : "index_id"
```
