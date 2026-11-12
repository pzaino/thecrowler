package database

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func TestSQLiteV117MigrationConvergesAndPreservesHistory(t *testing.T) {
	setup, err := os.ReadFile("sqlite-setup.sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("db_migrations/sqlite-migration-v1.17.sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "v117-migration.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(string(setup)); err != nil {
		t.Fatalf("apply clean-install schema: %v", err)
	}
	// Simulate a v1.16 database that predates the v1.17 tables and claim-lease columns.
	if _, err := db.Exec(`DROP TABLE CrawlApprovals; DROP TABLE SourceOwnership;
		DROP INDEX IF EXISTS idx_informationseed_lease_expiry;
		DROP INDEX IF EXISTS idx_informationseed_next_retry;
		DROP INDEX IF EXISTS idx_informationseed_claim_token;
		ALTER TABLE InformationSeed DROP COLUMN claim_token;
		ALTER TABLE InformationSeed DROP COLUMN fencing_token;
		ALTER TABLE InformationSeed DROP COLUMN lease_expires_at;
		ALTER TABLE InformationSeed DROP COLUMN heartbeat_at;
		ALTER TABLE InformationSeed DROP COLUMN next_retry_at;
		INSERT INTO DBSchemaVersion(version, description, is_current) VALUES ('1.16','old schema',0)
			ON CONFLICT(version) DO UPDATE SET is_current=0;
		UPDATE DBSchemaVersion SET is_current=(version='1.16');
		UPDATE DatabaseWriterCompatibility SET writer_generation='reservation-v1.15' WHERE lock_id=1;`); err != nil {
		t.Fatalf("prepare v1.16 state: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO Sources(source_uid, url, name, status, disabled, usr_id)
		VALUES ('v117-legacy-uid','https://v117-legacy.example/','legacy','completed',FALSE,7)`); err != nil {
		t.Fatalf("insert legacy source: %v", err)
	}
	if _, err := db.Exec(string(migration)); err != nil {
		t.Fatalf("first v1.17 migration: %v", err)
	}
	handler := Handler(&SQLiteHandler{db: db, dbms: DBSQLiteStr})
	var version, generation string
	if err := db.QueryRow(`SELECT version FROM DBSchemaVersion WHERE is_current=1`).Scan(&version); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if err := db.QueryRow(`SELECT writer_generation FROM DatabaseWriterCompatibility WHERE lock_id=1`).Scan(&generation); err != nil {
		t.Fatalf("read writer generation: %v", err)
	}
	if version != "1.17" || generation != "verified-ownership-v1.17" {
		t.Fatalf("unexpected schema contract version=%q generation=%q", version, generation)
	}
	var provenanceCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM SourceOwnership`).Scan(&provenanceCount); err != nil {
		t.Fatalf("count provenance: %v", err)
	}
	if provenanceCount != 0 {
		t.Fatalf("migration auto-verified %d legacy sources", provenanceCount)
	}
	var sourceID uint64
	if err := db.QueryRow(`SELECT source_id FROM Sources WHERE url='https://v117-legacy.example/'`).Scan(&sourceID); err != nil {
		t.Fatalf("legacy source was not preserved: %v", err)
	}
	now := time.Now().UTC()
	row := crawlApprovalRowFixture(now)
	row.ApprovalID = strings.Repeat("e", 64)
	row.ProposalSHA256 = strings.Repeat("f", 64)
	row.RequestID = "v117-history"
	row.SourceID = sourceID
	if inserted, err := InsertCrawlApproval(context.Background(), &handler, row); err != nil || !inserted {
		t.Fatalf("insert approval history: inserted=%v err=%v", inserted, err)
	}
	if _, err := db.Exec(string(migration)); err != nil {
		t.Fatalf("repeat v1.17 migration: %v", err)
	}
	var approvalCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM CrawlApprovals`).Scan(&approvalCount); err != nil {
		t.Fatalf("count approval history: %v", err)
	}
	if approvalCount != 1 {
		t.Fatalf("repeat migration changed approval history: %d", approvalCount)
	}
	if err := ensureSQLiteInformationSeedSchema(db); err != nil {
		t.Fatalf("runtime v1.17 schema repair: %v", err)
	}
	if err := CheckStartupCompatibility(context.Background(), handler); err != nil {
		t.Fatalf("v1.17 compatibility after migration: %v", err)
	}
}
