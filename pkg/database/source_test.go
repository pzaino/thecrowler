package database

import (
	"database/sql"
	"encoding/json"
	"testing"

	cfg "github.com/pzaino/thecrowler/pkg/config"

	_ "github.com/mattn/go-sqlite3"
)

func TestCreateSourceSQLiteUpsertPreservesProcessingRows(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite database: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE Sources (
			source_id INTEGER PRIMARY KEY AUTOINCREMENT,
			url TEXT NOT NULL UNIQUE,
			name VARCHAR(255),
			priority VARCHAR(64) DEFAULT '' NOT NULL,
			category_id INTEGER DEFAULT 0 NOT NULL,
			usr_id INTEGER DEFAULT 0 NOT NULL,
			restricted INTEGER DEFAULT 2 NOT NULL,
			flags INTEGER DEFAULT 0 NOT NULL,
			config TEXT,
			disabled BOOLEAN DEFAULT FALSE,
			status VARCHAR(50) DEFAULT 'new' NOT NULL,
			last_updated_at TIMESTAMP
		)`)
	if err != nil {
		t.Fatalf("create Sources table: %v", err)
	}

	handler := Handler(&SQLiteHandler{db: db, dbms: DBSQLiteStr})
	config := cfg.SourceConfig{}
	firstID, err := CreateSource(&handler, &Source{
		URL:        " https://example.test ",
		Name:       " first ",
		Priority:   " high ",
		CategoryID: 1,
		UsrID:      2,
		Restricted: 3,
		Flags:      4,
		Disabled:   false,
	}, config)
	if err != nil {
		t.Fatalf("insert source: %v", err)
	}
	if firstID == 0 {
		t.Fatal("expected inserted source id")
	}

	secondID, err := CreateSource(&handler, &Source{
		URL:        "https://example.test",
		Name:       " second ",
		Priority:   " low ",
		CategoryID: 10,
		UsrID:      20,
		Restricted: 30,
		Flags:      40,
		Disabled:   true,
	}, config)
	if err != nil {
		t.Fatalf("upsert source: %v", err)
	}
	if secondID != firstID {
		t.Fatalf("expected existing id %d, got %d", firstID, secondID)
	}

	assertSourceRow(t, db, firstID, "second", "low", 10, 20, 30, 40, true, "new")

	_, err = db.Exec(`UPDATE Sources SET status = 'processing' WHERE source_id = ?`, firstID)
	if err != nil {
		t.Fatalf("mark source processing: %v", err)
	}

	thirdID, err := CreateSource(&handler, &Source{
		URL:        "https://example.test",
		Name:       " blocked ",
		Priority:   " blocked ",
		CategoryID: 99,
		UsrID:      98,
		Restricted: 97,
		Flags:      96,
		Disabled:   false,
	}, config)
	if err != nil {
		t.Fatalf("upsert processing source: %v", err)
	}
	if thirdID != firstID {
		t.Fatalf("expected processing row id %d, got %d", firstID, thirdID)
	}

	assertSourceRow(t, db, firstID, "second", "low", 10, 20, 30, 40, true, "processing")
}

func assertSourceRow(t *testing.T, db *sql.DB, id uint64, expectedName, expectedPriority string, expectedCategoryID, expectedUsrID, expectedRestricted, expectedFlags uint64, expectedDisabled bool, expectedStatus string) {
	t.Helper()

	var name, priority, status string
	var categoryID, usrID, restricted, flags uint64
	var disabled bool
	var config sql.NullString
	err := db.QueryRow(`
		SELECT name, priority, category_id, usr_id, restricted, flags, disabled, status, config
		FROM Sources
		WHERE source_id = ?`, id).Scan(&name, &priority, &categoryID, &usrID, &restricted, &flags, &disabled, &status, &config)
	if err != nil {
		t.Fatalf("select source row: %v", err)
	}
	if name != expectedName || priority != expectedPriority || categoryID != expectedCategoryID || usrID != expectedUsrID || restricted != expectedRestricted || flags != expectedFlags || disabled != expectedDisabled || status != expectedStatus {
		t.Fatalf("unexpected source row: name=%q priority=%q category_id=%d usr_id=%d restricted=%d flags=%d disabled=%t status=%q", name, priority, categoryID, usrID, restricted, flags, disabled, status)
	}
	if !config.Valid || !json.Valid([]byte(config.String)) {
		t.Fatalf("expected valid JSON config, got %q", config.String)
	}
}

func TestSourceStatusHelpersNormalizeURLAndListStatuses(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite database: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE Sources (
			source_id INTEGER PRIMARY KEY AUTOINCREMENT,
			url TEXT NOT NULL UNIQUE,
			status TEXT,
			priority TEXT,
			engine TEXT,
			created_at TEXT,
			last_updated_at TEXT,
			last_crawled_at TEXT,
			last_error TEXT,
			last_error_at TEXT,
			restricted INTEGER DEFAULT 2 NOT NULL,
			disabled BOOLEAN DEFAULT FALSE,
			flags INTEGER DEFAULT 0 NOT NULL,
			config TEXT
		)`)
	if err != nil {
		t.Fatalf("create Sources table: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO Sources
			(url, status, priority, engine, created_at, last_updated_at, last_crawled_at, last_error, last_error_at, restricted, disabled, flags, config)
		VALUES
			('https://example.test/path', 'new', 'high', 'engine-a', '2026-01-01T00:00:00Z', '2026-01-02T00:00:00Z', '2026-01-03T00:00:00Z', '', '', 2, FALSE, 7, '{}'),
			('https://other.test', 'processing', 'low', 'engine-b', '2026-01-04T00:00:00Z', '2026-01-05T00:00:00Z', '2026-01-06T00:00:00Z', 'boom', '2026-01-07T00:00:00Z', 1, TRUE, 8, '{}')`)
	if err != nil {
		t.Fatalf("insert source statuses: %v", err)
	}

	handler := Handler(&SQLiteHandler{db: db, dbms: DBSQLiteStr})
	matches, err := GetSourceStatusByURL(&handler, " HTTPS://EXAMPLE.TEST/PATH/ ")
	if err != nil {
		t.Fatalf("get source status by URL: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one normalized URL match, got %d", len(matches))
	}
	if !matches[0].URL.Valid || matches[0].URL.String != "https://example.test/path" {
		t.Fatalf("unexpected URL match: %#v", matches[0].URL)
	}
	if matches[0].Flags != 7 {
		t.Fatalf("unexpected flags for matched status: %d", matches[0].Flags)
	}

	statuses, err := ListSourceStatuses(&handler)
	if err != nil {
		t.Fatalf("list source statuses: %v", err)
	}
	if len(statuses) != 2 {
		t.Fatalf("expected two statuses, got %d", len(statuses))
	}
}
