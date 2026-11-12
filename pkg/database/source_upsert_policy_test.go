package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	cfg "github.com/pzaino/thecrowler/pkg/config"

	_ "github.com/mattn/go-sqlite3"
)

func openSourcePolicyTestDB(t *testing.T) (*Handler, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE Sources (
			source_id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_uid VARCHAR(64) NOT NULL,
			url TEXT NOT NULL UNIQUE,
			name VARCHAR(255),
			priority VARCHAR(64) DEFAULT '' NOT NULL,
			sub_priority INTEGER DEFAULT 0 NOT NULL,
			category_id INTEGER DEFAULT 0 NOT NULL,
			usr_id INTEGER DEFAULT 0 NOT NULL,
			restricted INTEGER DEFAULT 2 NOT NULL,
			flags INTEGER DEFAULT 0 NOT NULL,
			config TEXT,
			disabled BOOLEAN DEFAULT FALSE,
			status VARCHAR(50) DEFAULT 'new' NOT NULL,
			last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			deleted_at TIMESTAMP
		);
		CREATE TABLE SourceOwnership (
			source_id INTEGER PRIMARY KEY,
			user_id INTEGER NOT NULL,
			verified_at TIMESTAMP NOT NULL,
			verified_by_user_id INTEGER NOT NULL,
			verified_by TEXT NOT NULL,
			verification_method TEXT NOT NULL,
			verification_reason TEXT,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
			last_updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL
		);`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	handler := Handler(&SQLiteHandler{db: db, dbms: DBSQLiteStr})
	return &handler, db
}

func validPolicySourceConfig(name, site string) cfg.SourceConfig {
	return cfg.SourceConfig{
		Version:       "1.0",
		FormatVersion: "1.0",
		SourceName:    name,
		CrawlingConfig: cfg.CrawlingConfig{
			Site:       site,
			SourceType: "website",
		},
	}
}

func TestUpsertSourceWithPolicyPersistsUIDStatusAndOwnership(t *testing.T) {
	handler, db := openSourcePolicyTestDB(t)
	result, err := UpsertSourceWithPolicyContext(context.Background(), handler, &Source{
		URL: "https://Example.Test/path", Name: "Example", Priority: "high", CategoryID: 4, UsrID: 7,
	}, validPolicySourceConfig("Example", "https://example.test"), SourceUpsertPolicy{
		CreateSources: true, LinkExistingSources: true, Status: "pending", AuthoritativeOwnership: true,
	})
	if err != nil {
		t.Fatalf("upsert source: %v", err)
	}
	if !result.Created || result.SourceID == 0 {
		t.Fatalf("expected created source, got %#v", result)
	}
	var uid, status, config, method string
	var owner uint64
	if err := db.QueryRow(`SELECT source_uid, status, config, usr_id FROM Sources WHERE source_id = ?`, result.SourceID).Scan(&uid, &status, &config, &owner); err != nil {
		t.Fatalf("read source: %v", err)
	}
	if uid != CalculateSourceUID("Example", "https://Example.Test/path") || len(uid) != 64 || strings.ToLower(uid) != uid {
		t.Fatalf("unexpected source UID %q", uid)
	}
	if status != "pending" || owner != 7 {
		t.Fatalf("unexpected source status/owner: %q/%d", status, owner)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(config), &decoded); err != nil || decoded["source_name"] != "Example" {
		t.Fatalf("unexpected persisted config: %q err=%v", config, err)
	}
	if err := db.QueryRow(`SELECT verification_method FROM SourceOwnership WHERE source_id = ?`, result.SourceID).Scan(&method); err != nil {
		t.Fatalf("read ownership: %v", err)
	}
	if method != "information_seed_discovery" {
		t.Fatalf("ownership method = %q", method)
	}
}

func TestUpsertSourceWithPolicyDoesNotOverwriteEmptyConfigOrCrossOwner(t *testing.T) {
	handler, db := openSourcePolicyTestDB(t)
	first, err := UpsertSourceWithPolicyContext(context.Background(), handler, &Source{URL: "https://existing.test/", Name: "existing", UsrID: 7}, validPolicySourceConfig("existing", "https://existing.test/"), SourceUpsertPolicy{CreateSources: true, LinkExistingSources: true})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	var original string
	if err := db.QueryRow(`SELECT config FROM Sources WHERE source_id = ?`, first.SourceID).Scan(&original); err != nil {
		t.Fatalf("read original config: %v", err)
	}
	if _, err := UpsertSourceWithPolicyContext(context.Background(), handler, &Source{URL: "https://existing.test/", Name: "changed", UsrID: 7}, cfg.SourceConfig{}, SourceUpsertPolicy{CreateSources: true, LinkExistingSources: true, UpdateExistingSourceConfig: true}); err != nil {
		t.Fatalf("empty-config upsert: %v", err)
	}
	var after string
	if err := db.QueryRow(`SELECT config FROM Sources WHERE source_id = ?`, first.SourceID).Scan(&after); err != nil {
		t.Fatalf("read after config: %v", err)
	}
	if after != original {
		t.Fatalf("empty config overwrote existing config: %q -> %q", original, after)
	}
	_, err = UpsertSourceWithPolicyContext(context.Background(), handler, &Source{URL: "https://existing.test/", Name: "intruder", UsrID: 8}, validPolicySourceConfig("intruder", "https://existing.test/"), SourceUpsertPolicy{CreateSources: true, LinkExistingSources: true, UpdateExistingSourceConfig: true})
	if !errors.Is(err, ErrSourceMutationForbidden) {
		t.Fatalf("cross-owner upsert error = %v, want forbidden", err)
	}
}

func TestUpsertSourceWithPolicyDoesNotClaimOwnershipWithoutTrustedPrincipal(t *testing.T) {
	handler, db := openSourcePolicyTestDB(t)
	result, err := UpsertSourceWithPolicyContext(context.Background(), handler, &Source{URL: "https://legacy-seed.test/", Name: "legacy", UsrID: 7}, cfg.SourceConfig{}, SourceUpsertPolicy{CreateSources: true, LinkExistingSources: true})
	if err != nil {
		t.Fatalf("upsert source: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM SourceOwnership WHERE source_id = ?`, result.SourceID).Scan(&count); err != nil {
		t.Fatalf("count ownership: %v", err)
	}
	if count != 0 {
		t.Fatalf("untrusted discovery created %d verified ownership rows", count)
	}
}
