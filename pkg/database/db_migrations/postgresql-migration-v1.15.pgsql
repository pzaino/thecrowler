-- Version 1.15 is deliberately one atomic unit.  In particular, do not move
-- ownership/version bookkeeping outside this transaction: a failed GRANT must
-- leave both the schema and its advertised version unchanged.
BEGIN;

-- Serialize migration attempts and verify that this is either a 1.14 upgrade
-- or a repeat of an already completed 1.15 migration.
LOCK TABLE DBSchemaVersion IN EXCLUSIVE MODE;
DO $$
DECLARE missing_objects TEXT;
BEGIN
  IF NOT EXISTS (SELECT 1 FROM DBSchemaVersion WHERE version IN ('1.14', '1.15')) THEN
    RAISE EXCEPTION 'v1.15 requires schema version 1.14 (or 1.15 for a repeat run)';
  END IF;

  SELECT string_agg(name, ', ' ORDER BY name) INTO missing_objects
  FROM (VALUES
    ('TimeSeriesMetrics'), ('TimeSeriesObservations'),
    ('TimeSeriesActiveSeries'), ('TimeSeriesActiveDimensionValues')
  ) required(name)
  WHERE to_regclass(current_schema() || '.' || name) IS NULL;
  IF missing_objects IS NOT NULL THEN
    RAISE EXCEPTION 'v1.15 is missing required source objects: %', missing_objects;
  END IF;
END $$;

CREATE TABLE IF NOT EXISTS TimeSeriesCardinalityTokens (
  token_number BIGINT PRIMARY KEY CHECK(token_number >= 0)
);
-- Stop-the-world writer fence.  Deployments must stop every pre-1.15 writer
-- before this migration; generation-aware writers refuse a different marker.
CREATE TABLE IF NOT EXISTS DatabaseWriterCompatibility (
  lock_id INTEGER PRIMARY KEY CHECK(lock_id = 1), writer_generation TEXT NOT NULL
);
INSERT INTO DatabaseWriterCompatibility(lock_id,writer_generation)
VALUES (1,'reservation-v1.15')
ON CONFLICT (lock_id) DO UPDATE SET writer_generation=EXCLUDED.writer_generation;
INSERT INTO TimeSeriesCardinalityTokens(token_number)
SELECT generate_series(0,99999) ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS TimeSeriesSeriesSlots (
  metric_id BIGINT NOT NULL REFERENCES TimeSeriesMetrics(metric_id) ON DELETE RESTRICT,
  slot_number BIGINT NOT NULL, series_hash VARCHAR(64) NOT NULL, identity_value TEXT NOT NULL,
  PRIMARY KEY(metric_id,slot_number), UNIQUE(metric_id,series_hash)
);
CREATE TABLE IF NOT EXISTS TimeSeriesDimensionSlots (
  metric_id BIGINT NOT NULL REFERENCES TimeSeriesMetrics(metric_id) ON DELETE RESTRICT,
  dimension_key TEXT NOT NULL, slot_number BIGINT NOT NULL, value_hash VARCHAR(64) NOT NULL, identity_value TEXT NOT NULL,
  PRIMARY KEY(metric_id,dimension_key,slot_number), UNIQUE(metric_id,dimension_key,value_hash)
);
CREATE TABLE IF NOT EXISTS TimeSeriesObservationSeries (
  observation_id BIGINT NOT NULL REFERENCES TimeSeriesObservations(observation_id) ON DELETE CASCADE,
  metric_id BIGINT NOT NULL, series_hash VARCHAR(64) NOT NULL,
  PRIMARY KEY(observation_id,metric_id,series_hash),
  FOREIGN KEY(metric_id,series_hash) REFERENCES TimeSeriesActiveSeries(metric_id,series_hash) ON DELETE RESTRICT
);
CREATE INDEX IF NOT EXISTS idx_ts_observation_series_identity ON TimeSeriesObservationSeries(metric_id,series_hash);
CREATE TABLE IF NOT EXISTS TimeSeriesObservationDimensions (
  observation_id BIGINT NOT NULL REFERENCES TimeSeriesObservations(observation_id) ON DELETE CASCADE,
  metric_id BIGINT NOT NULL, dimension_key TEXT NOT NULL, value_hash VARCHAR(64) NOT NULL,
  PRIMARY KEY(observation_id,metric_id,dimension_key,value_hash),
  FOREIGN KEY(metric_id,dimension_key,value_hash) REFERENCES TimeSeriesActiveDimensionValues(metric_id,dimension_key,value_hash) ON DELETE RESTRICT
);
CREATE INDEX IF NOT EXISTS idx_ts_observation_dimensions_identity ON TimeSeriesObservationDimensions(metric_id,dimension_key,value_hash);

-- Backfill through precisely the token reservation representation used by
-- runtime writes; row ordering is not used as a synthetic slot number.
DO $$
DECLARE r RECORD; chosen BIGINT;
BEGIN
 FOR r IN SELECT metric_id,series_hash,series_identity FROM TimeSeriesActiveSeries ORDER BY metric_id,series_hash LOOP
  SELECT t.token_number INTO chosen FROM TimeSeriesCardinalityTokens t
   WHERE NOT EXISTS (SELECT 1 FROM TimeSeriesSeriesSlots s WHERE s.metric_id=r.metric_id AND s.slot_number=t.token_number)
   ORDER BY t.token_number FOR UPDATE OF t SKIP LOCKED LIMIT 1;
  IF chosen IS NULL THEN RAISE EXCEPTION 'no cardinality token for metric %', r.metric_id; END IF;
  INSERT INTO TimeSeriesSeriesSlots(metric_id,slot_number,series_hash,identity_value)
  VALUES(r.metric_id,chosen,r.series_hash,r.series_identity) ON CONFLICT (metric_id,series_hash) DO NOTHING;
 END LOOP;
 FOR r IN SELECT metric_id,dimension_key,value_hash,canonical_value FROM TimeSeriesActiveDimensionValues ORDER BY metric_id,dimension_key,value_hash LOOP
  SELECT t.token_number INTO chosen FROM TimeSeriesCardinalityTokens t
   WHERE NOT EXISTS (SELECT 1 FROM TimeSeriesDimensionSlots s WHERE s.metric_id=r.metric_id AND s.dimension_key=r.dimension_key AND s.slot_number=t.token_number)
   ORDER BY t.token_number FOR UPDATE OF t SKIP LOCKED LIMIT 1;
  IF chosen IS NULL THEN RAISE EXCEPTION 'no cardinality token for metric % dimension %', r.metric_id,r.dimension_key; END IF;
  INSERT INTO TimeSeriesDimensionSlots(metric_id,dimension_key,slot_number,value_hash,identity_value)
  VALUES(r.metric_id,r.dimension_key,chosen,r.value_hash,r.canonical_value) ON CONFLICT (metric_id,dimension_key,value_hash) DO NOTHING;
 END LOOP;
END $$;

INSERT INTO TimeSeriesObservationSeries
SELECT observation_id,metric_id,series_hash FROM TimeSeriesObservations WHERE deleted_at IS NULL ON CONFLICT DO NOTHING;
INSERT INTO TimeSeriesObservationDimensions
SELECT o.observation_id,o.metric_id,a.dimension_key,a.value_hash
FROM TimeSeriesObservations o JOIN TimeSeriesActiveDimensionValues a
  ON a.metric_id=o.metric_id AND o.dimensions ? a.dimension_key
 AND (o.dimensions -> a.dimension_key)::text=a.canonical_value
WHERE o.deleted_at IS NULL ON CONFLICT DO NOTHING;

-- Refuse to publish a schema whose reservation/accounting state is incomplete
-- or whose reference counts disagree with normalized live memberships.
DO $$
BEGIN
 IF EXISTS (SELECT metric_id,series_hash,series_identity FROM TimeSeriesActiveSeries
            EXCEPT SELECT metric_id,series_hash,identity_value FROM TimeSeriesSeriesSlots)
    OR EXISTS (SELECT metric_id,series_hash,identity_value FROM TimeSeriesSeriesSlots
               EXCEPT SELECT metric_id,series_hash,series_identity FROM TimeSeriesActiveSeries)
    OR EXISTS (SELECT metric_id,dimension_key,value_hash,canonical_value FROM TimeSeriesActiveDimensionValues
               EXCEPT SELECT metric_id,dimension_key,value_hash,identity_value FROM TimeSeriesDimensionSlots)
    OR EXISTS (SELECT metric_id,dimension_key,value_hash,identity_value FROM TimeSeriesDimensionSlots
               EXCEPT SELECT metric_id,dimension_key,value_hash,canonical_value FROM TimeSeriesActiveDimensionValues) THEN
   RAISE EXCEPTION 'v1.15 cardinality reservation invariant failed';
 END IF;
 IF EXISTS (SELECT 1 FROM TimeSeriesActiveSeries a LEFT JOIN TimeSeriesObservationSeries m
              ON (m.metric_id,m.series_hash)=(a.metric_id,a.series_hash)
            GROUP BY a.metric_id,a.series_hash,a.reference_count HAVING count(m.observation_id) <> a.reference_count)
    OR EXISTS (SELECT 1 FROM TimeSeriesActiveDimensionValues a LEFT JOIN TimeSeriesObservationDimensions m
                 ON (m.metric_id,m.dimension_key,m.value_hash)=(a.metric_id,a.dimension_key,a.value_hash)
               GROUP BY a.metric_id,a.dimension_key,a.value_hash,a.reference_count HAVING count(m.observation_id) <> a.reference_count) THEN
   RAISE EXCEPTION 'v1.15 active reference-count invariant failed';
 END IF;
END $$;

ALTER TABLE TimeSeriesActiveSeries ALTER COLUMN reference_count SET DEFAULT 1;
ALTER TABLE TimeSeriesActiveDimensionValues ALTER COLUMN reference_count SET DEFAULT 1;

GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE
  TimeSeriesCardinalityTokens, TimeSeriesSeriesSlots, TimeSeriesDimensionSlots,
  TimeSeriesObservationSeries, TimeSeriesObservationDimensions, DatabaseWriterCompatibility
TO :CROWLER_DB_USER;

UPDATE DBSchemaVersion SET is_current=FALSE;
INSERT INTO DBSchemaVersion(version,description,is_current)
VALUES ('1.15','Fine-grained cardinality slots and normalized observation memberships',TRUE)
ON CONFLICT (version) DO UPDATE SET description=EXCLUDED.description,is_current=TRUE;
CREATE UNIQUE INDEX IF NOT EXISTS uq_dbschemaversion_one_current
  ON DBSchemaVersion (is_current) WHERE is_current=TRUE;

COMMIT;
