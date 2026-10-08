-- Version 1.16 raises autovacuum aggressiveness for the high-churn
-- ObjectAttributes table (and its TOAST) so refresh_content replacements do
-- not pin the MVCC horizon behind long-lived dead-tuple chains, and replaces
-- the per-artifact cleanup triggers with statement-level triggers that process
-- each bulk DELETE once through a transition table.  Like v1.15, this
-- migration is one atomic unit and is safe to re-run: every statement is
-- idempotent and the version guard accepts both 1.15 and an already applied
-- 1.16.
BEGIN;

-- Serialize migration attempts and verify that this is either a 1.15 upgrade
-- or a repeat of an already completed 1.16 migration.
LOCK TABLE DBSchemaVersion IN EXCLUSIVE MODE;
DO $$
DECLARE missing_objects TEXT;
BEGIN
  IF NOT EXISTS (SELECT 1 FROM DBSchemaVersion WHERE version IN ('1.15', '1.16')) THEN
    RAISE EXCEPTION 'v1.16 requires schema version 1.15 (or 1.16 for a repeat run)';
  END IF;

  SELECT string_agg(name, ', ' ORDER BY name) INTO missing_objects
  FROM (VALUES
    ('ObjectAttributes'), ('EntityMemberships'), ('ObjectCorrelations'),
    ('WebObjects'), ('NetInfo'), ('HTTPInfo')
  ) required(name)
  WHERE to_regclass(current_schema() || '.' || name) IS NULL;
  IF missing_objects IS NOT NULL THEN
    RAISE EXCEPTION 'v1.16 is missing required source objects: %', missing_objects;
  END IF;
END $$;

-- Table-level parameters keep dead tuples short-lived under high-churn
-- refresh_content traffic; toast.* parameters are set explicitly so the large
-- TOASTed attribute values are vacuumed on the same schedule instead of
-- relying on default propagation from the main table.  PostgreSQL has no
-- toast.autovacuum_analyze_* variants (analyze settings always propagate).
ALTER TABLE ObjectAttributes SET (
    autovacuum_vacuum_scale_factor = 0.005,
    autovacuum_vacuum_threshold = 25000,
    autovacuum_analyze_scale_factor = 0.002,
    autovacuum_analyze_threshold = 5000,
    toast.autovacuum_vacuum_scale_factor = 0.005,
    toast.autovacuum_vacuum_threshold = 10000
);

-- Replace the legacy per-row cleanup triggers with statement-level triggers
-- over an OLD TABLE transition alias.  DROP+CREATE (instead of conditional
-- creation) is what makes re-runs converge: a database carrying either the
-- legacy row triggers or the new statement triggers ends up with exactly the
-- statement-level design.  cleanup_artifact_data() is dropped only after its
-- triggers are gone so the dependency no longer blocks it.
DROP TRIGGER IF EXISTS trg_cleanup_webobject ON WebObjects;
DROP TRIGGER IF EXISTS trg_cleanup_netinfo ON NetInfo;
DROP TRIGGER IF EXISTS trg_cleanup_httpinfo ON HTTPInfo;
DROP FUNCTION IF EXISTS cleanup_artifact_data();

-- See postgresql-setup.pgsql for the rationale; the function body must stay
-- identical in setup and migration (fresh installs must equal upgrades).
CREATE OR REPLACE FUNCTION cleanup_artifact_data_set()
RETURNS trigger AS $$
DECLARE
    artifact_type TEXT := TG_ARGV[0];
    id_column TEXT;
BEGIN
    IF artifact_type NOT IN ('webobject', 'netinfo', 'httpinfo') THEN
        RAISE EXCEPTION
            'cleanup_artifact_data_set(): unsupported artifact type %',
            artifact_type;
    END IF;

    -- The transition table exposes only the source table's own columns, so a
    -- static CASE over d.netinfo_id would fail to parse when the trigger fires
    -- on WebObjects.  Resolve the identifier column per artifact type and let
    -- dynamic SQL bind it, keeping one shared function body for all three
    -- triggers.  Four EXECUTEs per DELETE statement (not per row) keep the
    -- parse cost negligible for bulk refresh_content deletions.
    id_column := CASE artifact_type
        WHEN 'webobject' THEN 'object_id'
        WHEN 'netinfo'   THEN 'netinfo_id'
        ELSE 'httpinfo_id'
    END;

    EXECUTE format(
        'DELETE FROM ObjectAttributes oa USING deleted_artifacts d' ||
        ' WHERE oa.object_type = $1 AND oa.object_id = d.%I',
        id_column)
        USING artifact_type;

    EXECUTE format(
        'DELETE FROM EntityMemberships em USING deleted_artifacts d' ||
        ' WHERE em.object_type = $1 AND em.object_id = d.%I',
        id_column)
        USING artifact_type;

    -- One statement per correlation side so each can use its own composite
    -- index (idx_objectcorrelations_obj1 / idx_objectcorrelations_obj2)
    -- instead of fighting over an OR predicate.
    EXECUTE format(
        'DELETE FROM ObjectCorrelations oc USING deleted_artifacts d' ||
        ' WHERE oc.object_type_1 = $1 AND oc.object_id_1 = d.%I',
        id_column)
        USING artifact_type;

    EXECUTE format(
        'DELETE FROM ObjectCorrelations oc USING deleted_artifacts d' ||
        ' WHERE oc.object_type_2 = $1 AND oc.object_id_2 = d.%I',
        id_column)
        USING artifact_type;

    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_cleanup_webobject
AFTER DELETE ON WebObjects
REFERENCING OLD TABLE AS deleted_artifacts
FOR EACH STATEMENT
EXECUTE FUNCTION cleanup_artifact_data_set('webobject');

CREATE TRIGGER trg_cleanup_netinfo
AFTER DELETE ON NetInfo
REFERENCING OLD TABLE AS deleted_artifacts
FOR EACH STATEMENT
EXECUTE FUNCTION cleanup_artifact_data_set('netinfo');

CREATE TRIGGER trg_cleanup_httpinfo
AFTER DELETE ON HTTPInfo
REFERENCING OLD TABLE AS deleted_artifacts
FOR EACH STATEMENT
EXECUTE FUNCTION cleanup_artifact_data_set('httpinfo');

UPDATE DBSchemaVersion SET is_current=FALSE;
INSERT INTO DBSchemaVersion(version,description,is_current)
VALUES ('1.16','Aggressive autovacuum tuning and statement-level artifact cleanup for high-churn ObjectAttributes',TRUE)
ON CONFLICT (version) DO UPDATE SET description=EXCLUDED.description,is_current=TRUE;
CREATE UNIQUE INDEX IF NOT EXISTS uq_dbschemaversion_one_current
  ON DBSchemaVersion (is_current) WHERE is_current=TRUE;

COMMIT;
