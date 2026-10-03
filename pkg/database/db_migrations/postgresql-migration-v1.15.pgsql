CREATE TABLE IF NOT EXISTS TimeSeriesSeriesSlots (
 metric_id BIGINT NOT NULL REFERENCES TimeSeriesMetrics(metric_id) ON DELETE RESTRICT,
 slot_number BIGINT NOT NULL, series_hash VARCHAR(64) NOT NULL, identity_value TEXT NOT NULL,
 PRIMARY KEY(metric_id,slot_number), UNIQUE(metric_id,series_hash));
CREATE TABLE IF NOT EXISTS TimeSeriesDimensionSlots (
 metric_id BIGINT NOT NULL REFERENCES TimeSeriesMetrics(metric_id) ON DELETE RESTRICT,
 dimension_key TEXT NOT NULL, slot_number BIGINT NOT NULL, value_hash VARCHAR(64) NOT NULL, identity_value TEXT NOT NULL,
 PRIMARY KEY(metric_id,dimension_key,slot_number), UNIQUE(metric_id,dimension_key,value_hash));
INSERT INTO TimeSeriesSeriesSlots(metric_id,slot_number,series_hash,identity_value)
SELECT metric_id,ROW_NUMBER() OVER(PARTITION BY metric_id ORDER BY series_hash)-1,series_hash,series_identity FROM TimeSeriesActiveSeries ON CONFLICT DO NOTHING;
INSERT INTO TimeSeriesDimensionSlots(metric_id,dimension_key,slot_number,value_hash,identity_value)
SELECT metric_id,dimension_key,ROW_NUMBER() OVER(PARTITION BY metric_id,dimension_key ORDER BY value_hash)-1,value_hash,canonical_value FROM TimeSeriesActiveDimensionValues ON CONFLICT DO NOTHING;
INSERT INTO DBSchemaVersion(version,description,is_current) SELECT '1.15','Fine-grained time-series cardinality slots',TRUE WHERE NOT EXISTS (SELECT 1 FROM DBSchemaVersion WHERE version='1.15');
UPDATE DBSchemaVersion SET is_current=(version='1.15') WHERE version IN ('1.14','1.15');
DO $$
DECLARE crowler_owner TEXT;
BEGIN
 SELECT tableowner INTO crowler_owner FROM pg_tables WHERE schemaname=current_schema() AND tablename=lower('TimeSeriesActiveSeries');
 IF crowler_owner IS NOT NULL THEN
  EXECUTE format('ALTER TABLE TimeSeriesSeriesSlots OWNER TO %I', crowler_owner);
  EXECUTE format('ALTER TABLE TimeSeriesDimensionSlots OWNER TO %I', crowler_owner);
 END IF;
END $$;
