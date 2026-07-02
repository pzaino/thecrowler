package database

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func newSourceScheduleTestDB(t *testing.T) (*sql.DB, Handler) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite database: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE Sources (
		source_id INTEGER PRIMARY KEY,
		disabled BOOLEAN NOT NULL DEFAULT FALSE,
		status TEXT NOT NULL DEFAULT 'new'
	)`); err != nil {
		db.Close()
		t.Fatalf("create Sources: %v", err)
	}
	return db, Handler(&SQLiteHandler{db: db, dbms: DBSQLiteStr})
}

func TestScheduleSourceCrawlMarksIdleSourcePending(t *testing.T) {
	db, handler := newSourceScheduleTestDB(t)
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO Sources(source_id, status) VALUES (42, 'completed')`); err != nil {
		t.Fatalf("insert source: %v", err)
	}

	err := ScheduleSourceCrawl(context.Background(), &handler, 42)
	if err != nil {
		t.Fatalf("ScheduleSourceCrawl() error = %v", err)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM Sources WHERE source_id = 42`).Scan(&status); err != nil {
		t.Fatalf("select source: %v", err)
	}
	if status != "pending" {
		t.Fatalf("source status = %q, want pending", status)
	}
}

func TestScheduleSourceCrawlLeavesProcessingSourceOwned(t *testing.T) {
	db, handler := newSourceScheduleTestDB(t)
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO Sources(source_id, status) VALUES (42, 'processing')`); err != nil {
		t.Fatalf("insert source: %v", err)
	}

	err := ScheduleSourceCrawl(context.Background(), &handler, 42)
	if err != nil {
		t.Fatalf("ScheduleSourceCrawl() error = %v", err)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM Sources WHERE source_id = 42`).Scan(&status); err != nil {
		t.Fatalf("select source: %v", err)
	}
	if status != "processing" {
		t.Fatalf("source status = %q, want processing", status)
	}
}

func TestScheduleSourceCrawlIgnoresDisabledOrMissingSource(t *testing.T) {
	db, handler := newSourceScheduleTestDB(t)
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO Sources(source_id, disabled, status) VALUES (7, TRUE, 'completed')`); err != nil {
		t.Fatalf("insert source: %v", err)
	}

	for _, sourceID := range []uint64{7, 999} {
		err := ScheduleSourceCrawl(context.Background(), &handler, sourceID)
		if err != nil {
			t.Fatalf("ScheduleSourceCrawl(%d) error = %v", sourceID, err)
		}
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM Sources WHERE source_id = 7`).Scan(&status); err != nil {
		t.Fatalf("select disabled source: %v", err)
	}
	if status != "completed" {
		t.Fatalf("disabled source status = %q, want completed", status)
	}
}

func TestScheduleSourceCrawlCoalescesPendingStatus(t *testing.T) {
	db, handler := newSourceScheduleTestDB(t)
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO Sources(source_id, status) VALUES (42, 'pending')`); err != nil {
		t.Fatalf("insert source: %v", err)
	}
	for range 2 {
		if err := ScheduleSourceCrawl(context.Background(), &handler, 42); err != nil {
			t.Fatalf("ScheduleSourceCrawl() error = %v", err)
		}
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM Sources WHERE source_id = 42`).Scan(&status); err != nil {
		t.Fatalf("select source: %v", err)
	}
	if status != "pending" {
		t.Fatalf("source status = %q, want pending", status)
	}
}
