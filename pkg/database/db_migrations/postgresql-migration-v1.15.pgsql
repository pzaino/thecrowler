CREATE TABLE IF NOT EXISTS TimeSeriesCardinalityTokens (token_number BIGINT PRIMARY KEY CHECK(token_number >= 0));
INSERT INTO TimeSeriesCardinalityTokens(token_number) SELECT generate_series(0,99999) ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS TimeSeriesSeriesSlots (
 metric_id BIGINT NOT NULL REFERENCES TimeSeriesMetrics(metric_id) ON DELETE RESTRICT,
 slot_number BIGINT NOT NULL, series_hash VARCHAR(64) NOT NULL, identity_value TEXT NOT NULL,
 PRIMARY KEY(metric_id,slot_number), UNIQUE(metric_id,series_hash));
CREATE TABLE IF NOT EXISTS TimeSeriesDimensionSlots (
 metric_id BIGINT NOT NULL REFERENCES TimeSeriesMetrics(metric_id) ON DELETE RESTRICT,
 dimension_key TEXT NOT NULL, slot_number BIGINT NOT NULL, value_hash VARCHAR(64) NOT NULL, identity_value TEXT NOT NULL,
 PRIMARY KEY(metric_id,dimension_key,slot_number), UNIQUE(metric_id,dimension_key,value_hash));
-- Backfill through the same token reservation model used by production writes.
-- The available token is selected and locked for each identity; slot numbers are
-- not synthesized from the current row ordering.
DO $$
DECLARE r RECORD; chosen BIGINT;
BEGIN
 FOR r IN SELECT metric_id,series_hash,series_identity FROM TimeSeriesActiveSeries ORDER BY metric_id,series_hash LOOP
  SELECT t.token_number INTO chosen FROM TimeSeriesCardinalityTokens t
   WHERE NOT EXISTS (SELECT 1 FROM TimeSeriesSeriesSlots s WHERE s.metric_id=r.metric_id AND s.slot_number=t.token_number)
   ORDER BY t.token_number FOR UPDATE OF t SKIP LOCKED LIMIT 1;
  IF chosen IS NULL THEN RAISE EXCEPTION 'no cardinality token for metric %', r.metric_id; END IF;
  INSERT INTO TimeSeriesSeriesSlots(metric_id,slot_number,series_hash,identity_value) VALUES(r.metric_id,chosen,r.series_hash,r.series_identity) ON CONFLICT DO NOTHING;
 END LOOP;
 FOR r IN SELECT metric_id,dimension_key,value_hash,canonical_value FROM TimeSeriesActiveDimensionValues ORDER BY metric_id,dimension_key,value_hash LOOP
  SELECT t.token_number INTO chosen FROM TimeSeriesCardinalityTokens t
   WHERE NOT EXISTS (SELECT 1 FROM TimeSeriesDimensionSlots s WHERE s.metric_id=r.metric_id AND s.dimension_key=r.dimension_key AND s.slot_number=t.token_number)
   ORDER BY t.token_number FOR UPDATE OF t SKIP LOCKED LIMIT 1;
  IF chosen IS NULL THEN RAISE EXCEPTION 'no cardinality token for metric % dimension %', r.metric_id,r.dimension_key; END IF;
  INSERT INTO TimeSeriesDimensionSlots(metric_id,dimension_key,slot_number,value_hash,identity_value) VALUES(r.metric_id,r.dimension_key,chosen,r.value_hash,r.canonical_value) ON CONFLICT DO NOTHING;
 END LOOP;
END $$;
CREATE TABLE IF NOT EXISTS TimeSeriesObservationSeries (
 observation_id BIGINT NOT NULL REFERENCES TimeSeriesObservations(observation_id) ON DELETE CASCADE,
 metric_id BIGINT NOT NULL, series_hash VARCHAR(64) NOT NULL,
 PRIMARY KEY(observation_id,metric_id,series_hash),
 FOREIGN KEY(metric_id,series_hash) REFERENCES TimeSeriesActiveSeries(metric_id,series_hash) ON DELETE RESTRICT);
CREATE INDEX IF NOT EXISTS idx_ts_observation_series_identity ON TimeSeriesObservationSeries(metric_id,series_hash);
CREATE TABLE IF NOT EXISTS TimeSeriesObservationDimensions (
 observation_id BIGINT NOT NULL REFERENCES TimeSeriesObservations(observation_id) ON DELETE CASCADE,
 metric_id BIGINT NOT NULL, dimension_key TEXT NOT NULL, value_hash VARCHAR(64) NOT NULL,
 PRIMARY KEY(observation_id,metric_id,dimension_key,value_hash),
 FOREIGN KEY(metric_id,dimension_key,value_hash) REFERENCES TimeSeriesActiveDimensionValues(metric_id,dimension_key,value_hash) ON DELETE RESTRICT);
CREATE INDEX IF NOT EXISTS idx_ts_observation_dimensions_identity ON TimeSeriesObservationDimensions(metric_id,dimension_key,value_hash);
INSERT INTO TimeSeriesObservationSeries SELECT observation_id,metric_id,series_hash FROM TimeSeriesObservations WHERE deleted_at IS NULL ON CONFLICT DO NOTHING;
INSERT INTO TimeSeriesObservationDimensions
 SELECT o.observation_id,o.metric_id,a.dimension_key,a.value_hash FROM TimeSeriesObservations o JOIN TimeSeriesActiveDimensionValues a ON a.metric_id=o.metric_id AND o.dimensions ? a.dimension_key AND (o.dimensions -> a.dimension_key)::text=a.canonical_value WHERE o.deleted_at IS NULL ON CONFLICT DO NOTHING;
ALTER TABLE TimeSeriesActiveSeries ALTER COLUMN reference_count SET DEFAULT 1;
ALTER TABLE TimeSeriesActiveDimensionValues ALTER COLUMN reference_count SET DEFAULT 1;
INSERT INTO DBSchemaVersion(version,description,is_current) SELECT '1.15','Fine-grained cardinality slots and normalized observation memberships',TRUE WHERE NOT EXISTS (SELECT 1 FROM DBSchemaVersion WHERE version='1.15');
UPDATE DBSchemaVersion SET is_current=(version='1.15') WHERE version IN ('1.14','1.15');
DO $$
DECLARE crowler_owner TEXT;
BEGIN
 SELECT tableowner INTO crowler_owner FROM pg_tables WHERE schemaname=current_schema() AND tablename=lower('TimeSeriesActiveSeries');
 IF crowler_owner IS NOT NULL THEN
  EXECUTE format('ALTER TABLE TimeSeriesSeriesSlots OWNER TO %I', crowler_owner);
  EXECUTE format('ALTER TABLE TimeSeriesDimensionSlots OWNER TO %I', crowler_owner);
  EXECUTE format('ALTER TABLE TimeSeriesCardinalityTokens OWNER TO %I', crowler_owner);
  EXECUTE format('ALTER TABLE TimeSeriesObservationSeries OWNER TO %I', crowler_owner);
  EXECUTE format('ALTER TABLE TimeSeriesObservationDimensions OWNER TO %I', crowler_owner);
 END IF;
END $$;
