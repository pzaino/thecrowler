package database

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestEnsureSQLiteInformationSeedSchemaMigratesLegacyTablePreservingRows(t *testing.T) {
	db := openSQLiteMemoryDB(t)
	defer db.Close()

	_, err := db.Exec(`
		CREATE TABLE InformationSeed (
			information_seed_id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
			last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			category_id INTEGER DEFAULT 0 NOT NULL,
			usr_id INTEGER DEFAULT 0 NOT NULL,
			information_seed VARCHAR(256) NOT NULL,
			config TEXT
		);
		INSERT INTO InformationSeed (category_id, usr_id, information_seed, config)
		VALUES (7, 11, 'existing seed', '{"keep":true}');
	`)
	if err != nil {
		t.Fatalf("create legacy InformationSeed table: %v", err)
	}

	if err = ensureSQLiteInformationSeedSchema(db); err != nil {
		t.Fatalf("migrate legacy InformationSeed table: %v", err)
	}

	assertSQLiteColumns(t, db, "InformationSeed", []string{
		"status",
		"priority",
		"engine",
		"last_processed_at",
		"last_error",
		"last_error_at",
		"disabled",
		"attempts",
	})
	assertSQLiteIndexes(t, db, []string{
		"idx_informationseed_status",
		"idx_informationseed_priority",
		"idx_informationseed_disabled",
		"idx_informationseed_last_processed_at",
		"idx_informationseed_last_error_at",
		"idx_informationseed_processing_stale",
		"idx_informationseed_claim_queue",
		"idx_informationseed_engine_status",
		"idx_informationseedcandidate_seed",
		"idx_informationseedcandidate_decision",
		"idx_informationseedcandidate_seed_decision",
		"idx_informationseedcandidate_host",
		"idx_informationseedcandidate_provider",
		"idx_informationseedcandidate_normalized_url",
	})

	var (
		categoryID      int
		usrID           int
		informationSeed string
		config          string
		status          string
		priority        string
		engine          string
		disabled        bool
		attempts        int
	)
	err = db.QueryRow(`
		SELECT category_id, usr_id, information_seed, config, status, priority, engine, disabled, attempts
		FROM InformationSeed
		WHERE information_seed_id = 1
	`).Scan(&categoryID, &usrID, &informationSeed, &config, &status, &priority, &engine, &disabled, &attempts)
	if err != nil {
		t.Fatalf("read migrated seed row: %v", err)
	}
	if categoryID != 7 || usrID != 11 || informationSeed != "existing seed" || config != `{"keep":true}` {
		t.Fatalf("legacy row was not preserved: category=%d usr=%d seed=%q config=%q", categoryID, usrID, informationSeed, config)
	}
	if status != "new" || priority != "" || engine != "" || disabled || attempts != 0 {
		t.Fatalf("unexpected lifecycle defaults: status=%q priority=%q engine=%q disabled=%v attempts=%d", status, priority, engine, disabled, attempts)
	}
}

func TestEnsureSQLiteInformationSeedSchemaIsIdempotent(t *testing.T) {
	db := openSQLiteMemoryDB(t)
	defer db.Close()

	_, err := db.Exec(`
		CREATE TABLE InformationSeed (
			information_seed_id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
			last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			category_id INTEGER DEFAULT 0 NOT NULL,
			usr_id INTEGER DEFAULT 0 NOT NULL,
			information_seed VARCHAR(256) NOT NULL,
			config TEXT
		);
		INSERT INTO InformationSeed (information_seed) VALUES ('idempotent seed');
	`)
	if err != nil {
		t.Fatalf("create legacy InformationSeed table: %v", err)
	}

	for i := 0; i < 2; i++ {
		if err = ensureSQLiteInformationSeedSchema(db); err != nil {
			t.Fatalf("migration run %d failed: %v", i+1, err)
		}
	}

	var rowCount int
	if err = db.QueryRow("SELECT COUNT(*) FROM InformationSeed").Scan(&rowCount); err != nil {
		t.Fatalf("count seed rows: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("expected one preserved seed row, got %d", rowCount)
	}
}

func TestEnsureSQLiteSourceInformationSeedProvenanceMigratesLegacyTable(t *testing.T) {
	db := openSQLiteMemoryDB(t)
	defer db.Close()

	_, err := db.Exec(`
		CREATE TABLE SourceInformationSeedIndex (
			source_information_seed_id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_id INTEGER NOT NULL,
			information_seed_id INTEGER NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
			last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			UNIQUE (source_id, information_seed_id)
		);
		INSERT INTO SourceInformationSeedIndex (source_id, information_seed_id) VALUES (9, 3);
	`)
	if err != nil {
		t.Fatalf("create legacy SourceInformationSeedIndex table: %v", err)
	}

	for i := 0; i < 2; i++ {
		if err = ensureSQLiteSourceInformationSeedProvenance(db); err != nil {
			t.Fatalf("source/seed provenance migration run %d failed: %v", i+1, err)
		}
	}

	assertSQLiteColumns(t, db, "SourceInformationSeedIndex", []string{
		"discovery_provider",
		"discovery_query",
		"discovery_rank",
		"candidate_score",
		"candidate_reason",
		"discovery_metadata",
	})
	assertSQLiteIndexes(t, db, []string{
		"idx_sourceinformationseedindex_information_seed_id",
		"idx_sourceinformationseedindex_source_id",
		"idx_sourceinformationseedindex_seed_source",
		"idx_sourceinformationseedindex_provider_rank",
	})

	var sourceID, seedID int
	if err = db.QueryRow("SELECT source_id, information_seed_id FROM SourceInformationSeedIndex WHERE source_information_seed_id = 1").Scan(&sourceID, &seedID); err != nil {
		t.Fatalf("read migrated source/seed link row: %v", err)
	}
	if sourceID != 9 || seedID != 3 {
		t.Fatalf("legacy source/seed link row was not preserved: source=%d seed=%d", sourceID, seedID)
	}
}

func openSQLiteMemoryDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite database: %v", err)
	}
	return db
}

func assertSQLiteColumns(t *testing.T, db *sql.DB, table string, names []string) {
	t.Helper()

	columns, err := sqliteColumns(db, table)
	if err != nil {
		t.Fatalf("read %s columns: %v", table, err)
	}
	for _, name := range names {
		if !columns[name] {
			t.Fatalf("missing column %s.%s", table, name)
		}
	}
}

func assertSQLiteIndexes(t *testing.T, db *sql.DB, names []string) {
	t.Helper()

	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type = 'index'")
	if err != nil {
		t.Fatalf("read sqlite indexes: %v", err)
	}
	defer rows.Close()

	indexes := map[string]bool{}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			t.Fatalf("scan sqlite index: %v", err)
		}
		indexes[name] = true
	}
	if err = rows.Err(); err != nil {
		t.Fatalf("iterate sqlite indexes: %v", err)
	}

	for _, name := range names {
		if !indexes[name] {
			t.Fatalf("missing index %s", name)
		}
	}
}

func TestSQLiteTimeSeriesMigrationIsIdempotentAndEnforcesPortableKeys(t *testing.T) {
	db := openSQLiteMemoryDB(t)
	defer db.Close()

	if _, err := db.Exec(`
		PRAGMA foreign_keys = ON;
		CREATE TABLE InformationSeed (information_seed_id INTEGER PRIMARY KEY);
		CREATE TABLE InformationSeedCandidate (information_seed_candidate_id INTEGER PRIMARY KEY);
		CREATE TABLE Sources (source_id INTEGER PRIMARY KEY);
		CREATE TABLE SourceInformationSeedIndex (source_information_seed_id INTEGER PRIMARY KEY);
		CREATE TABLE SearchIndex (index_id INTEGER PRIMARY KEY);
		CREATE TABLE Entities (entity_id INTEGER PRIMARY KEY);
		CREATE TABLE CorrelationRules (rule_id INTEGER PRIMARY KEY);
		INSERT INTO Sources (source_id) VALUES (7);
	`); err != nil {
		t.Fatalf("create time-series migration prerequisites: %v", err)
	}

	migration, err := os.ReadFile("db_migrations/sqlite-migration-v1.09.sqlite3")
	if err != nil {
		t.Fatalf("read SQLite time-series migration: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err = db.Exec(string(migration)); err != nil {
			t.Fatalf("execute SQLite time-series migration run %d: %v", i+1, err)
		}
	}

	assertSQLiteColumns(t, db, "TimeSeriesMetrics", []string{
		"metric_key", "selector", "retention_policy", "cardinality_policy", "store_value_text", "hash_only",
	})
	assertSQLiteColumns(t, db, "TimeSeriesObservations", []string{
		"information_seed_id", "information_seed_candidate_id", "source_id", "index_id", "entity_id",
		"subject_type", "object_type", "correlation_rule_id", "value_numeric", "value_integer", "value_boolean",
		"value_text", "value_json", "value_timestamp", "previous_value_hash", "is_changed", "dedupe_key", "provenance",
	})
	assertSQLiteColumns(t, db, "TimeSeriesAggregates", []string{
		"percentile_50", "percentile_90", "percentile_95", "percentile_99", "first_observation_id",
		"last_observation_id", "change_count", "aggregate_hash",
	})
	assertSQLiteIndexes(t, db, []string{
		"idx_timeseriesobservations_metric_bucket",
		"idx_timeseriesobservations_seed",
		"idx_timeseriesobservations_seed_candidate",
		"idx_timeseriesobservations_source",
		"idx_timeseriesobservations_index",
		"idx_timeseriesobservations_entity",
		"idx_timeseriesobservations_subject",
		"idx_timeseriesobservations_object",
		"idx_timeseriesobservations_correlation_rule",
		"idx_timeseriesobservations_dedupe_key",
		"idx_timeseriesaggregates_metric_bucket",
		"idx_timeseriesaggregates_aggregate_hash",
	})

	if _, err = db.Exec(`
		INSERT INTO TimeSeriesMetrics (
			metric_key, display_name, source_kind, value_type, aggregate, bucket,
			time_basis, dedupe_scope, object_type, failure_policy, selector
		) VALUES (
			'page.latency', 'Page latency', 'source', 'numeric', 'avg', 'minute',
			'observed_at', 'source', 'webobject', 'skip', '{}'
		);
		INSERT INTO TimeSeriesObservations (
			metric_id, observed_at, bucket_start, bucket_end, source_id,
			value_numeric, value_hash, dedupe_key, dimensions, provenance
		) VALUES (
			1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 7,
			12.5, 'value-hash-1', 'dedupe-key-1', '{}', '{}'
		);
		INSERT INTO TimeSeriesAggregates (
			metric_id, bucket_start, bucket_end, value_count, numeric_count,
			numeric_sum, numeric_avg, aggregate_hash
		) VALUES (
			1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 1, 1, 12.5, 12.5, 'aggregate-hash-1'
		);
	`); err != nil {
		t.Fatalf("insert direct-source time-series rows: %v", err)
	}

	var seedID sql.NullInt64
	if err = db.QueryRow("SELECT information_seed_id FROM TimeSeriesObservations WHERE observation_id = 1").Scan(&seedID); err != nil {
		t.Fatalf("read direct-source observation: %v", err)
	}
	if seedID.Valid {
		t.Fatalf("direct-source observation unexpectedly requires information_seed_id=%d", seedID.Int64)
	}

	if _, err = db.Exec(`
		INSERT INTO TimeSeriesObservations (
			metric_id, observed_at, bucket_start, bucket_end, value_hash, dedupe_key
		) VALUES (1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'value-hash-2', 'dedupe-key-1')
	`); err == nil {
		t.Fatal("duplicate observation dedupe_key was accepted")
	}
	if _, err = db.Exec(`
		INSERT INTO TimeSeriesAggregates (
			metric_id, bucket_start, bucket_end, aggregate_hash
		) VALUES (1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'aggregate-hash-1')
	`); err == nil {
		t.Fatal("duplicate aggregate_hash was accepted")
	}
	if _, err = db.Exec(`
		INSERT INTO TimeSeriesMetrics (
			metric_key, display_name, source_kind, value_type, aggregate, bucket,
			time_basis, dedupe_scope, object_type, failure_policy, selector
		) VALUES ('page.latency', 'Duplicate', 'source', 'numeric', 'avg', 'minute',
			'observed_at', 'source', 'webobject', 'skip', '{}')
	`); err == nil {
		t.Fatal("duplicate metric_key was accepted")
	}

	if _, err = db.Exec("DELETE FROM Sources WHERE source_id = 7"); err != nil {
		t.Fatalf("delete optional source: %v", err)
	}
	var sourceID sql.NullInt64
	if err = db.QueryRow("SELECT source_id FROM TimeSeriesObservations WHERE observation_id = 1").Scan(&sourceID); err != nil {
		t.Fatalf("read observation after source deletion: %v", err)
	}
	if sourceID.Valid {
		t.Fatalf("source reference was not cleared after source deletion: %d", sourceID.Int64)
	}
}
