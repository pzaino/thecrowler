--------------------------------
-- Partitions setup (experimental)

--------------------------------
-- Partitioning logic

-- Creates a function to create the next quarter partition for the Sources table
CREATE OR REPLACE FUNCTION create_next_quarter_partition_sources()
RETURNS void AS $$
DECLARE
    next_quarter_start DATE;
    next_quarter_end DATE;
    partition_name TEXT;
BEGIN
    -- Calculate the start date of the next quarter
    next_quarter_start := DATE_TRUNC('quarter', CURRENT_DATE) + INTERVAL '3 months';
    -- Calculate the end date of the next quarter
    next_quarter_end := DATE_TRUNC('quarter', CURRENT_DATE) + INTERVAL '6 months';
    -- Construct the partition name
    partition_name := 'sources_' || TO_CHAR(next_quarter_start, 'YYYY') || '_Q' || TO_CHAR(next_quarter_start, 'Q');

    -- Check if the partition already exists
    IF NOT EXISTS (
        SELECT FROM pg_class c
        JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE c.relkind = 'r' AND c.relname = lower(partition_name) AND n.nspname = 'public'
    ) THEN
        -- Create the partition if it does not exist
        EXECUTE FORMAT('CREATE TABLE public.%I PARTITION OF Sources FOR VALUES FROM (%L) TO (%L)',
                       partition_name, next_quarter_start, next_quarter_end);
    END IF;
END;
$$ LANGUAGE plpgsql;

-- Creates a function to create the next quarter partition for the SearchIndex table
CREATE OR REPLACE FUNCTION create_next_quarter_partition_searchindex()
RETURNS void AS $$
DECLARE
    next_quarter_start DATE;
    next_quarter_end DATE;
    partition_name TEXT;
BEGIN
    -- Calculate the start date of the next quarter
    next_quarter_start := DATE_TRUNC('quarter', CURRENT_DATE) + INTERVAL '3 months';
    -- Calculate the end date of the next quarter
    next_quarter_end := DATE_TRUNC('quarter', CURRENT_DATE) + INTERVAL '6 months';
    -- Construct the partition name
    partition_name := 'searchindex_' || TO_CHAR(next_quarter_start, 'YYYY') || '_Q' || TO_CHAR(next_quarter_start, 'Q');

    -- Check if the partition already exists
    IF NOT EXISTS (
        SELECT FROM pg_class c
        JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE c.relkind = 'r' AND c.relname = lower(partition_name) AND n.nspname = 'public'
    ) THEN
        -- Create the partition if it does not exist
        EXECUTE FORMAT('CREATE TABLE public.%I PARTITION OF SearchIndex FOR VALUES FROM (%L) TO (%L)',
                       partition_name, next_quarter_start, next_quarter_end);
    END IF;
END;
$$ LANGUAGE plpgsql;

-- Creates a function to create the next quarter partition for the NetInfo table
CREATE OR REPLACE FUNCTION create_next_quarter_partition_netinfo()
RETURNS void AS $$
DECLARE
    next_quarter_start DATE;
    next_quarter_end DATE;
    partition_name TEXT;
BEGIN
    -- Calculate the start date of the next quarter
    next_quarter_start := DATE_TRUNC('quarter', CURRENT_DATE) + INTERVAL '3 months';
    -- Calculate the end date of the next quarter
    next_quarter_end := DATE_TRUNC('quarter', CURRENT_DATE) + INTERVAL '6 months';
    -- Construct the partition name
    partition_name := 'netinfo_' || TO_CHAR(next_quarter_start, 'YYYY') || '_Q' || TO_CHAR(next_quarter_start, 'Q');

    -- Check if the partition already exists
    IF NOT EXISTS (
        SELECT FROM pg_class c
        JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE c.relkind = 'r' AND c.relname = lower(partition_name) AND n.nspname = 'public'
    ) THEN
        -- Create the partition if it does not exist
        EXECUTE FORMAT('CREATE TABLE public.%I PARTITION OF NetInfo FOR VALUES FROM (%L) TO (%L)',
                       partition_name, next_quarter_start, next_quarter_end);
    END IF;
END;
$$ LANGUAGE plpgsql;

-- Creates a function to create the next quarter partition for the MetaTags table
CREATE OR REPLACE FUNCTION create_next_quarter_partition_metatags()
RETURNS void AS $$
DECLARE
    next_quarter_start DATE;
    next_quarter_end DATE;
    partition_name TEXT;
BEGIN
    -- Calculate the start date of the next quarter
    next_quarter_start := DATE_TRUNC('quarter', CURRENT_DATE) + INTERVAL '3 months';
    -- Calculate the end date of the next quarter
    next_quarter_end := DATE_TRUNC('quarter', CURRENT_DATE) + INTERVAL '6 months';
    -- Construct the partition name
    partition_name := 'metatags_' || TO_CHAR(next_quarter_start, 'YYYY') || '_Q' || TO_CHAR(next_quarter_start, 'Q');

    -- Check if the partition already exists
    IF NOT EXISTS (
        SELECT FROM pg_class c
        JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE c.relkind = 'r' AND c.relname = lower(partition_name) AND n.nspname = 'public'
    ) THEN
        -- Create the partition if it does not exist
        EXECUTE FORMAT('CREATE TABLE public.%I PARTITION OF MetaTags FOR VALUES FROM (%L) TO (%L)',
                       partition_name, next_quarter_start, next_quarter_end);
    END IF;
END;
$$ LANGUAGE plpgsql;

-- Creates a function to create the next quarter partition for the Keywords table
CREATE OR REPLACE FUNCTION create_next_quarter_partition_keywords()
RETURNS void AS $$
DECLARE
    next_quarter_start DATE;
    next_quarter_end DATE;
    partition_name TEXT;
BEGIN
    -- Calculate the start date of the next quarter
    next_quarter_start := DATE_TRUNC('quarter', CURRENT_DATE) + INTERVAL '3 months';
    -- Calculate the end date of the next quarter
    next_quarter_end := DATE_TRUNC('quarter', CURRENT_DATE) + INTERVAL '6 months';
    -- Construct the partition name
    partition_name := 'keywords_' || TO_CHAR(next_quarter_start, 'YYYY') || '_Q' || TO_CHAR(next_quarter_start, 'Q');

    -- Check if the partition already exists
    IF NOT EXISTS (
        SELECT FROM pg_class c
        JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE c.relkind = 'r' AND c.relname = lower(partition_name) AND n.nspname = 'public'
    ) THEN
        -- Create the partition if it does not exist
        EXECUTE FORMAT('CREATE TABLE public.%I PARTITION OF Keywords FOR VALUES FROM (%L) TO (%L)',
                       partition_name, next_quarter_start, next_quarter_end);
    END IF;
END;
$$ LANGUAGE plpgsql;

-- Creates a function to create the next quarter partition for the SourceOwner table
CREATE OR REPLACE FUNCTION create_next_quarter_partition_sourceowner()
RETURNS void AS $$
DECLARE
    next_quarter_start DATE;
    next_quarter_end DATE;
    partition_name TEXT;
BEGIN
    -- Calculate the start date of the next quarter
    next_quarter_start := DATE_TRUNC('quarter', CURRENT_DATE) + INTERVAL '3 months';
    -- Calculate the end date of the next quarter
    next_quarter_end := DATE_TRUNC('quarter', CURRENT_DATE) + INTERVAL '6 months';
    -- Construct the partition name
    partition_name := 'sourceowner_' || TO_CHAR(next_quarter_start, 'YYYY') || '_Q' || TO_CHAR(next_quarter_start, 'Q');

    -- Check if the partition already exists
    IF NOT EXISTS (
        SELECT FROM pg_class c
        JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE c.relkind = 'r' AND c.relname = lower(partition_name) AND n.nspname = 'public'
    ) THEN
        -- Create the partition if it does not exist
        EXECUTE FORMAT('CREATE TABLE public.%I PARTITION OF SourceOwner FOR VALUES FROM (%L) TO (%L)',
                       partition_name, next_quarter_start, next_quarter_end);
    END IF;
END;
$$ LANGUAGE plpgsql;

-- Creates a function to create the next quarter partition for the SourceSearchIndex table
CREATE OR REPLACE FUNCTION create_next_quarter_partition_sourcesearchindex()
RETURNS void AS $$
DECLARE
    next_quarter_start DATE;
    next_quarter_end DATE;
    partition_name TEXT;
BEGIN
    -- Calculate the start date of the next quarter
    next_quarter_start := DATE_TRUNC('quarter', CURRENT_DATE) + INTERVAL '3 months';
    -- Calculate the end date of the next quarter
    next_quarter_end := DATE_TRUNC('quarter', CURRENT_DATE) + INTERVAL '6 months';
    -- Construct the partition name
    partition_name := 'sourcesearchindex_' || TO_CHAR(next_quarter_start, 'YYYY') || '_Q' || TO_CHAR(next_quarter_start, 'Q');

    -- Check if the partition already exists
    IF NOT EXISTS (
        SELECT FROM pg_class c
        JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE c.relkind = 'r' AND c.relname = lower(partition_name) AND n.nspname = 'public'
    ) THEN
        -- Create the partition if it does not exist
        EXECUTE FORMAT('CREATE TABLE public.%I PARTITION OF SourceSearchIndex FOR VALUES FROM (%L) TO (%L)',
                       partition_name, next_quarter_start, next_quarter_end);
    END IF;
END;
$$ LANGUAGE plpgsql;

-- Creates a function to create the next quarter partition for the KeywordIndex table
CREATE OR REPLACE FUNCTION create_next_quarter_partition_keywordindex()
RETURNS void AS $$
DECLARE
    next_quarter_start DATE;
    next_quarter_end DATE;
    partition_name TEXT;
BEGIN
    -- Calculate the start date of the next quarter
    next_quarter_start := DATE_TRUNC('quarter', CURRENT_DATE) + INTERVAL '3 months';
    -- Calculate the end date of the next quarter
    next_quarter_end := DATE_TRUNC('quarter', CURRENT_DATE) + INTERVAL '6 months';
    -- Construct the partition name
    partition_name := 'keywordindex_' || TO_CHAR(next_quarter_start, 'YYYY') || '_Q' || TO_CHAR(next_quarter_start, 'Q');

    -- Check if the partition already exists
    IF NOT EXISTS (
        SELECT FROM pg_class c
        JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE c.relkind = 'r' AND c.relname = lower(partition_name) AND n.nspname = 'public'
    ) THEN
        -- Create the partition if it does not exist
        EXECUTE FORMAT('CREATE TABLE public.%I PARTITION OF KeywordIndex FOR VALUES FROM (%L) TO (%L)',
                       partition_name, next_quarter_start, next_quarter_end);
    END IF;
END;
$$ LANGUAGE plpgsql;

-- Create a UNIQUE index on the Sources table for source_id
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_sources_source_id') THEN
        -- Create the index if it doesn't exist
        CREATE UNIQUE INDEX idx_sources_source_id ON Sources (source_id);
    END IF;
END
$$;

-- Create a UNIQUE index on the Owners table for owner_id
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_owners_owner_id') THEN
        -- Create the index if it doesn't exist
        CREATE UNIQUE INDEX idx_owners_owner_id ON Owners (owner_id);
    END IF;
END
$$;

-- Create a UNIQUE index on the NetInfo table for netinfo_id
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_netinfo_netinfo_id') THEN
        -- Create the index if it doesn't exist
        CREATE UNIQUE INDEX idx_netinfo_netinfo_id ON NetInfo (netinfo_id);
    END IF;
END
$$;

-- Create a UNIQUE index on the SearchIndex table for index_id
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_searchindex_index_id') THEN
        -- Create the index if it doesn't exist
        CREATE UNIQUE INDEX idx_searchindex_index_id ON SearchIndex (index_id);
    END IF;
END
$$;

-- Create a UNIQUE index on the MetaTags table for metatag_id
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_metatags_metatag_id') THEN
        -- Create the index if it doesn't exist
        CREATE UNIQUE INDEX idx_metatags_metatag_id ON MetaTags (metatag_id);
    END IF;
END
$$;

-- Create a UNIQUE index on the Keywords table for keyword_id
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_keywords_keyword_id') THEN
        -- Create the index if it doesn't exist
        CREATE UNIQUE INDEX idx_keywords_keyword_id ON Keywords (keyword_id);
    END IF;
END
$$;

-- Create a UNIQUE index on the SourceOwner table for source_owner_id
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_sourceowner_source_owner_id') THEN
        -- Create the index if it doesn't exist
        CREATE UNIQUE INDEX idx_sourceowner_source_owner_id ON SourceOwner (source_owner_id);
    END IF;
END
$$;

-- Create a UNIQUE index on the SourceSearchIndex table for ss_index_id
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_sourcesearchindex_ss_index_id') THEN
        -- Create the index if it doesn't exist
        CREATE UNIQUE INDEX idx_sourcesearchindex_ss_index_id ON SourceSearchIndex (ss_index_id);
    END IF;
END
$$;

-- Create a UNIQUE index on the KeywordIndex table for keyword_index_id
DO $$
BEGIN
    -- Check if the index already exists
    IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'idx_keywordindex_keyword_index_id') THEN
        -- Create the index if it doesn't exist
        CREATE UNIQUE INDEX idx_keywordindex_keyword_index_id ON KeywordIndex (keyword_index_id);
    END IF;
END
$$;
