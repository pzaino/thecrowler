package database

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func startupFixture(t *testing.T, version string, complete bool, generation string) Handler {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	statements := []string{
		`PRAGMA foreign_keys=ON`,
		`CREATE TABLE DBSchemaVersion(version TEXT PRIMARY KEY,is_current INTEGER)`,
		`INSERT INTO DBSchemaVersion VALUES('` + version + `',1)`,
		`CREATE TABLE TimeSeriesMetrics(metric_id INTEGER PRIMARY KEY,cardinality_policy TEXT)`,
		`CREATE TABLE TimeSeriesActiveSeries(metric_id INTEGER REFERENCES TimeSeriesMetrics(metric_id),series_hash TEXT,series_identity TEXT,reference_count INTEGER,PRIMARY KEY(metric_id,series_hash))`,
		`CREATE TABLE TimeSeriesActiveDimensionValues(metric_id INTEGER REFERENCES TimeSeriesMetrics(metric_id),dimension_key TEXT,value_hash TEXT,canonical_value TEXT,reference_count INTEGER,PRIMARY KEY(metric_id,dimension_key,value_hash))`,
		`CREATE TABLE TimeSeriesObservations(observation_id INTEGER PRIMARY KEY,metric_id INTEGER,series_hash TEXT,dimensions TEXT,deleted_at TIMESTAMP)`,
		`CREATE TABLE TimeSeriesObservationSeries(observation_id INTEGER REFERENCES TimeSeriesObservations(observation_id),metric_id INTEGER,series_hash TEXT,PRIMARY KEY(observation_id,metric_id,series_hash),FOREIGN KEY(metric_id,series_hash) REFERENCES TimeSeriesActiveSeries(metric_id,series_hash))`,
		`CREATE TABLE TimeSeriesObservationDimensions(observation_id INTEGER REFERENCES TimeSeriesObservations(observation_id),metric_id INTEGER,dimension_key TEXT,value_hash TEXT,PRIMARY KEY(observation_id,metric_id,dimension_key,value_hash),FOREIGN KEY(metric_id,dimension_key,value_hash) REFERENCES TimeSeriesActiveDimensionValues(metric_id,dimension_key,value_hash))`,
		`CREATE TABLE TimeSeriesCardinalityMaintenanceLock(lock_id INTEGER PRIMARY KEY)`,
		`CREATE TABLE DatabaseWriterCompatibility(lock_id INTEGER PRIMARY KEY,writer_generation TEXT NOT NULL)`,
		`INSERT INTO DatabaseWriterCompatibility VALUES(1,'` + generation + `')`,
		`CREATE INDEX idx_ts_observation_series_identity ON TimeSeriesObservationSeries(metric_id,series_hash)`,
	}
	if complete {
		statements = append(statements, `CREATE INDEX idx_ts_observation_dimensions_identity ON TimeSeriesObservationDimensions(metric_id,dimension_key,value_hash)`)
	}
	for _, statement := range statements {
		if _, err = db.Exec(statement); err != nil {
			t.Fatalf("fixture statement %q: %v", statement, err)
		}
	}
	return &SQLiteHandler{db: db, dbms: DBSQLiteStr}
}

func TestStartupCompatibilityRejectsV114(t *testing.T) {
	err := CheckStartupCompatibility(context.Background(), startupFixture(t, "1.14", true, WriterGeneration))
	if err == nil || !strings.Contains(err.Error(), "required schema=1.15") || !strings.Contains(err.Error(), "detected schema=1.14") || !strings.Contains(err.Error(), "stop all old writers") {
		t.Fatalf("unexpected diagnostic: %v", err)
	}
}

func TestStartupCompatibilityRejectsFalselyLabeledIncompleteV115(t *testing.T) {
	err := CheckStartupCompatibility(context.Background(), startupFixture(t, "1.15", false, WriterGeneration))
	if err == nil || !strings.Contains(err.Error(), "idx_ts_observation_dimensions_identity") {
		t.Fatalf("unexpected diagnostic: %v", err)
	}
}

func TestStartupCompatibilityAcceptsValidV115(t *testing.T) {
	if err := CheckStartupCompatibility(context.Background(), startupFixture(t, "1.15", true, WriterGeneration)); err != nil {
		t.Fatalf("valid schema rejected: %v", err)
	}
}

func TestStartupCompatibilityRejectsIncompatibleActiveWriterGeneration(t *testing.T) {
	err := CheckStartupCompatibility(context.Background(), startupFixture(t, "1.15", true, "reservation-v1.14"))
	if err == nil || !strings.Contains(err.Error(), `incompatible active writer generation "reservation-v1.14"`) {
		t.Fatalf("unexpected diagnostic: %v", err)
	}
}
