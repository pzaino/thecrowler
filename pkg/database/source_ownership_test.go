package database

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func newSourceOwnershipTestDB(t *testing.T) (*sql.DB, Handler) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite database: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE Sources (
		source_id INTEGER PRIMARY KEY,
		url TEXT UNIQUE,
		usr_id INTEGER NOT NULL DEFAULT 0,
		deleted_at TIMESTAMP
	)`); err != nil {
		t.Fatalf("create Sources: %v", err)
	}
	return db, Handler(&SQLiteHandler{db: db, dbms: DBSQLiteStr})
}

func TestGetSourceOwner(t *testing.T) {
	t.Run("owned source", func(t *testing.T) {
		db, handler := newSourceOwnershipTestDB(t)
		if _, err := db.Exec(`INSERT INTO Sources(source_id, usr_id) VALUES (42, 7)`); err != nil {
			t.Fatalf("insert source: %v", err)
		}
		owner, err := GetSourceOwner(context.Background(), &handler, 42)
		if err != nil {
			t.Fatalf("get owner: %v", err)
		}
		if !owner.Found || owner.SourceID != 42 || owner.UserID != 7 {
			t.Fatalf("unexpected owner: %+v", owner)
		}
	})

	t.Run("zero owner", func(t *testing.T) {
		db, handler := newSourceOwnershipTestDB(t)
		if _, err := db.Exec(`INSERT INTO Sources(source_id, usr_id) VALUES (42, 0)`); err != nil {
			t.Fatalf("insert source: %v", err)
		}
		owner, err := GetSourceOwner(context.Background(), &handler, 42)
		if err != nil {
			t.Fatalf("get owner: %v", err)
		}
		if !owner.Found || owner.UserID != 0 {
			t.Fatalf("unexpected owner: %+v", owner)
		}
	})

	t.Run("missing source", func(t *testing.T) {
		_, handler := newSourceOwnershipTestDB(t)
		owner, err := GetSourceOwner(context.Background(), &handler, 404)
		if err != nil {
			t.Fatalf("get owner: %v", err)
		}
		if owner.Found {
			t.Fatalf("missing source reported as found: %+v", owner)
		}
	})

	t.Run("deleted source", func(t *testing.T) {
		db, handler := newSourceOwnershipTestDB(t)
		if _, err := db.Exec(`INSERT INTO Sources(source_id, usr_id, deleted_at) VALUES (42, 7, CURRENT_TIMESTAMP)`); err != nil {
			t.Fatalf("insert source: %v", err)
		}
		owner, err := GetSourceOwner(context.Background(), &handler, 42)
		if err != nil {
			t.Fatalf("get owner: %v", err)
		}
		if owner.Found {
			t.Fatalf("deleted source reported as found: %+v", owner)
		}
	})

	t.Run("invalid inputs", func(t *testing.T) {
		_, handler := newSourceOwnershipTestDB(t)
		if _, err := GetSourceOwner(context.Background(), &handler, 0); err == nil {
			t.Fatal("zero source ID accepted")
		}
		if _, err := GetSourceOwner(context.Background(), nil, 42); err == nil {
			t.Fatal("nil handler accepted")
		}
	})

	t.Run("cancelled context", func(t *testing.T) {
		db, handler := newSourceOwnershipTestDB(t)
		if _, err := db.Exec(`INSERT INTO Sources(source_id, usr_id) VALUES (42, 7)`); err != nil {
			t.Fatalf("insert source: %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := GetSourceOwner(ctx, &handler, 42); !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want context.Canceled", err)
		}
	})
}

func TestGetSourceOwnerByURL(t *testing.T) {
	db, handler := newSourceOwnershipTestDB(t)
	if _, err := db.Exec(`INSERT INTO Sources(source_id, url, usr_id, deleted_at) VALUES
		(42, 'https://active.example/', 7, NULL),
		(43, 'https://deleted.example/', 8, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("insert sources: %v", err)
	}
	active, err := GetSourceOwnerByURL(context.Background(), &handler, "https://active.example/", false)
	if err != nil || !active.Found || active.UserID != 7 || active.Deleted || active.SourceID != 42 {
		t.Fatalf("active URL owner: %+v, %v", active, err)
	}
	if owner, err := GetSourceOwnerByURL(context.Background(), &handler, "https://deleted.example/", false); err != nil || owner.Found {
		t.Fatalf("deleted source visible through active URL lookup: %+v, %v", owner, err)
	}
	deleted, err := GetSourceOwnerByURL(context.Background(), &handler, "https://deleted.example/", true)
	if err != nil || !deleted.Found || deleted.UserID != 8 || !deleted.Deleted || deleted.SourceID != 43 {
		t.Fatalf("deleted URL owner: %+v, %v", deleted, err)
	}
	if owner, err := GetSourceOwnerByURL(context.Background(), &handler, "https://missing.example/", true); err != nil || owner.Found {
		t.Fatalf("missing URL owner: %+v, %v", owner, err)
	}
}
