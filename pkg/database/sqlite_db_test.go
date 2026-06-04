package database

import (
	"database/sql"
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
