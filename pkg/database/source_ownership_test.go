package database

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	cfg "github.com/pzaino/thecrowler/pkg/config"

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
	); CREATE TABLE SourceOwnership (
		source_id INTEGER PRIMARY KEY,
		user_id INTEGER NOT NULL,
		verified_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		verified_by_user_id INTEGER NOT NULL,
		verified_by TEXT NOT NULL,
		verification_method TEXT NOT NULL,
		verification_reason TEXT
	)`); err != nil {
		t.Fatalf("create Sources: %v", err)
	}
	return db, Handler(&SQLiteHandler{db: db, dbms: DBSQLiteStr})
}

func TestGetSourceOwner(t *testing.T) {
	t.Run("verified owned source", func(t *testing.T) {
		db, handler := newSourceOwnershipTestDB(t)
		if _, err := db.Exec(`INSERT INTO Sources(source_id, usr_id) VALUES (42, 7);
			INSERT INTO SourceOwnership(source_id, user_id, verified_by_user_id, verified_by, verification_method)
			VALUES (42, 7, 9, 'admin', 'admin_verification')`); err != nil {
			t.Fatalf("insert source: %v", err)
		}
		owner, err := GetSourceOwner(context.Background(), &handler, 42)
		if err != nil {
			t.Fatalf("get owner: %v", err)
		}
		if !owner.Found || owner.SourceID != 42 || owner.UserID != 7 || !owner.Verified {
			t.Fatalf("unexpected owner: %+v", owner)
		}
		verified, err := GetVerifiedSourceOwner(context.Background(), &handler, 42)
		if err != nil || !verified.Found || !verified.Verified || verified.UserID != 7 {
			t.Fatalf("verified owner lookup: %+v, %v", verified, err)
		}
	})

	t.Run("legacy owner without provenance", func(t *testing.T) {
		db, handler := newSourceOwnershipTestDB(t)
		if _, err := db.Exec(`INSERT INTO Sources(source_id, usr_id) VALUES (42, 7)`); err != nil {
			t.Fatalf("insert source: %v", err)
		}
		owner, err := GetSourceOwner(context.Background(), &handler, 42)
		if err != nil {
			t.Fatalf("get owner: %v", err)
		}
		if !owner.Found || owner.UserID != 7 || owner.Verified {
			t.Fatalf("legacy owner should be unverified: %+v", owner)
		}
		verified, err := GetVerifiedSourceOwner(context.Background(), &handler, 42)
		if err != nil || verified.Found || verified.UserID != 0 {
			t.Fatalf("legacy source exposed as verified: %+v, %v", verified, err)
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

func TestAuthenticatedSourceMutationUnsupportedBackend(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	handler := Handler(&SQLiteHandler{db: db, dbms: DBMySQLStr})
	if _, _, err := UpsertAuthenticatedSourceContext(context.Background(), &handler,
		&Source{URL: "https://unsupported.example/"}, cfg.SourceConfig{}, 7, "owner", false); !errors.Is(err, ErrAuthenticatedSourceUnsupported) {
		t.Fatalf("unsupported authenticated upsert error = %v", err)
	}
	if _, err := VerifySourceOwnershipContext(context.Background(), &handler, SourceOwnershipVerification{
		SourceID: 1, UserID: 7, VerifiedByUserID: 9,
	}); !errors.Is(err, ErrAuthenticatedSourceUnsupported) {
		t.Fatalf("unsupported ownership verification error = %v", err)
	}
}
