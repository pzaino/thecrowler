// Copyright 2023 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package database

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sourceCrawlAttemptDDLBlock(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	const begin = "-- BEGIN SOURCE CRAWL ATTEMPT (schema 1.17)"
	const end = "-- END SOURCE CRAWL ATTEMPT (schema 1.17)"
	text := string(content)
	if strings.Count(text, begin) != 1 || strings.Count(text, end) != 1 {
		t.Fatalf("%s must contain exactly one canonical attempt DDL block", path)
	}
	start := strings.Index(text, begin)
	finish := strings.Index(text, end)
	if start < 0 || finish < start {
		t.Fatalf("%s has malformed attempt DDL markers", path)
	}
	return text[start : finish+len(end)]
}

func TestSourceCrawlAttemptCanonicalDDLConverges(t *testing.T) {
	t.Parallel()
	postgresFiles := []string{
		"postgresql-setup.pgsql",
		"db_migrations/postgresql-migration-v1.17.pgsql",
		"db_migrations/postgresql-source-crawl-attempt-v1.17.pgsql",
	}
	sqliteFiles := []string{
		"sqlite-setup.sqlite3",
		"db_migrations/sqlite-migration-v1.17.sqlite3",
		"db_migrations/sqlite-source-crawl-attempt-v1.17.sqlite3",
	}
	for _, files := range [][]string{postgresFiles, sqliteFiles} {
		want := sourceCrawlAttemptDDLBlock(t, files[0])
		for _, file := range files[1:] {
			if got := sourceCrawlAttemptDDLBlock(t, file); got != want {
				t.Errorf("%s attempt DDL differs from %s", file, files[0])
			}
		}
	}
}

func TestSourceCrawlAttemptKeepsSchema117(t *testing.T) {
	t.Parallel()
	if RequiredSchemaVersion != "1.17" {
		t.Fatalf("RequiredSchemaVersion = %q, want 1.17", RequiredSchemaVersion)
	}
	if WriterGeneration != "verified-ownership-v1.17" {
		t.Fatalf("WriterGeneration = %q, want verified-ownership-v1.17", WriterGeneration)
	}
	files := []string{
		"postgresql-setup.pgsql", "sqlite-setup.sqlite3", "mysql-setup.mysql",
		"db_migrations/postgresql-migration-v1.17.pgsql",
		"db_migrations/sqlite-migration-v1.17.sqlite3",
		"db_migrations/mysql-migration-v1.17.mysql",
		"db_migrations/postgresql-source-crawl-attempt-v1.17.pgsql",
		"db_migrations/sqlite-source-crawl-attempt-v1.17.sqlite3",
	}
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		text := string(content)
		for _, forbidden := range []string{"'1.18'", "\"1.18\"", "'1.19'", "\"1.19\"", "'2.0'", "\"2.0\""} {
			if strings.Contains(text, forbidden) {
				t.Errorf("%s advertises schema version beyond 1.17: %s", file, forbidden)
			}
		}
	}
	for _, file := range []string{
		"db_migrations/postgresql-source-crawl-attempt-v1.17.pgsql",
		"db_migrations/sqlite-source-crawl-attempt-v1.17.sqlite3",
	} {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read addon %s: %v", file, err)
		}
		text := string(content)
		if strings.Contains(text, "UPDATE DBSchemaVersion") ||
			strings.Contains(text, "UPDATE DatabaseWriterCompatibility") {
			t.Errorf("%s must not change schema or writer markers", file)
		}
	}
	for _, file := range []string{"mysql-setup.mysql", "db_migrations/mysql-migration-v1.17.mysql"} {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		if strings.Contains(string(content), "SourceCrawlAttempt") {
			t.Errorf("%s unexpectedly advertises source crawl attempt persistence", file)
		}
	}
}

func TestSourceCrawlAttemptFencedUpdateSourcesIsPresent(t *testing.T) {
	t.Parallel()
	for _, file := range []string{
		"postgresql-setup.pgsql",
		"db_migrations/postgresql-migration-v1.17.pgsql",
	} {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		text := string(content)
		if !strings.Contains(text, "CREATE OR REPLACE FUNCTION update_sources(") {
			t.Errorf("%s does not define update_sources", file)
		}
		if !strings.Contains(text, "SourceCrawlClaimPolicy") ||
			!strings.Contains(text, "mode = 'fenced'") {
			t.Errorf("%s update_sources is not fenced-mode aware", file)
		}
	}
}

func TestSourceCrawlAttemptSQLiteFreshAndMigration(t *testing.T) {
	ctx := context.Background()
	raw, handler := openAsyncGoalSQLite(t)
	if err := CheckSourceCrawlAttemptSchema(ctx, &handler); err != nil {
		t.Fatalf("fresh attempt readiness: %v", err)
	}

	for _, table := range []string{
		"SourceCrawlClaims", "SourceCrawlAttempts", "SourceCrawlClaimPolicy",
	} {
		if _, err := raw.Exec(`DROP TABLE IF EXISTS ` + table); err != nil {
			t.Fatalf("drop %s: %v", table, err)
		}
	}
	if _, err := raw.Exec(`DELETE FROM SchemaCapabilities WHERE capability=?`, SourceCrawlAttemptCapability); err != nil {
		t.Fatalf("remove attempt marker: %v", err)
	}
	if _, err := raw.Exec(`UPDATE DBSchemaVersion SET is_current=(version='1.16');
		UPDATE DatabaseWriterCompatibility SET writer_generation='reservation-v1.15' WHERE lock_id=1;`); err != nil {
		t.Fatalf("prepare SQLite v1.16 markers: %v", err)
	}
	migration, err := os.ReadFile("db_migrations/sqlite-migration-v1.17.sqlite3")
	if err != nil {
		t.Fatalf("read SQLite migration: %v", err)
	}
	if _, err := raw.Exec(string(migration)); err != nil {
		t.Fatalf("apply SQLite v1.17 migration: %v", err)
	}
	if err := CheckSourceCrawlAttemptSchema(ctx, &handler); err != nil {
		t.Fatalf("attempt readiness after migration: %v", err)
	}
	var version, generation string
	if err := raw.QueryRow(`SELECT version FROM DBSchemaVersion WHERE is_current=1`).Scan(&version); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if err := raw.QueryRow(`SELECT writer_generation FROM DatabaseWriterCompatibility WHERE lock_id=1`).Scan(&generation); err != nil {
		t.Fatalf("read writer generation: %v", err)
	}
	if version != "1.17" || generation != WriterGeneration {
		t.Fatalf("migration markers version=%q generation=%q", version, generation)
	}
}

func TestSourceCrawlAttemptSQLiteAddonRepeatable(t *testing.T) {
	raw, handler := openAsyncGoalSQLite(t)
	seedSourceCrawlClaimSource(t, raw, 42, 7, "new", false)
	enableSourceCrawlClaimMode(t, raw)
	claims, err := claimSourceCrawlAttemptsAt(context.Background(), &handler,
		sourceCrawlClaimRequest("addon"), sourceCrawlClaimTestNow)
	if err != nil || len(claims) != 1 {
		t.Fatalf("seed claim = %+v err=%v", claims, err)
	}
	addon, err := os.ReadFile("db_migrations/sqlite-source-crawl-attempt-v1.17.sqlite3")
	if err != nil {
		t.Fatalf("read SQLite attempt addon: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := raw.Exec(string(addon)); err != nil {
			t.Fatalf("apply SQLite attempt addon pass %d: %v", i+1, err)
		}
	}
	var mode string
	if err := raw.QueryRow(`SELECT mode FROM SourceCrawlClaimPolicy WHERE policy_id=1`).Scan(&mode); err != nil {
		t.Fatalf("read policy mode: %v", err)
	}
	if mode != "fenced" {
		t.Fatalf("addon reset policy mode to %q", mode)
	}
	var attempts, current int
	if err := raw.QueryRow(`SELECT
		(SELECT COUNT(*) FROM SourceCrawlAttempts WHERE claim_request_id=?),
		(SELECT COUNT(*) FROM SourceCrawlClaims WHERE source_id=42)`,
		sourceCrawlClaimRequest("addon").ClaimRequestID).Scan(&attempts, &current); err != nil {
		t.Fatalf("count attempt rows: %v", err)
	}
	if attempts != 1 || current != 1 {
		t.Fatalf("addon changed attempt rows attempts=%d current=%d", attempts, current)
	}
}

func TestSourceCrawlAttemptSQLiteAddonRejectsIncompatible(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad-attempt.db")
	raw, err := sql.Open(DBSQLiteStr, path+"?_foreign_keys=on")
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })
	if _, err := raw.Exec(`CREATE TABLE DBSchemaVersion (version TEXT PRIMARY KEY, description TEXT, is_current INTEGER NOT NULL DEFAULT 0);
		CREATE UNIQUE INDEX uq_dbschemaversion_one_current ON DBSchemaVersion(is_current) WHERE is_current=1;
		INSERT INTO DBSchemaVersion VALUES ('1.17','test',1);
		CREATE TABLE DatabaseWriterCompatibility (lock_id INTEGER PRIMARY KEY, writer_generation TEXT NOT NULL);
		INSERT INTO DatabaseWriterCompatibility VALUES (1,'verified-ownership-v1.17');
		CREATE TABLE Sources (source_id INTEGER PRIMARY KEY, usr_id INTEGER NOT NULL DEFAULT 0);
		CREATE TABLE SourceCrawlClaims (source_id INTEGER PRIMARY KEY, wrong_column TEXT);`); err != nil {
		t.Fatalf("create incompatible SQLite fixture: %v", err)
	}
	addon, err := os.ReadFile("db_migrations/sqlite-source-crawl-attempt-v1.17.sqlite3")
	if err != nil {
		t.Fatalf("read SQLite attempt addon: %v", err)
	}
	if _, err := raw.Exec(string(addon)); err == nil {
		t.Fatal("SQLite attempt addon accepted an incompatible pre-existing table")
	}
}

func TestSourceCrawlAttemptReadinessDoesNotAffectStartup(t *testing.T) {
	ctx := context.Background()
	raw, handler := openAsyncGoalSQLite(t)
	if err := CheckStartupCompatibility(ctx, handler); err != nil {
		t.Fatalf("ordinary startup compatibility: %v", err)
	}
	for _, table := range []string{
		"SourceCrawlClaims", "SourceCrawlAttempts", "SourceCrawlClaimPolicy",
	} {
		if _, err := raw.Exec(`DROP TABLE IF EXISTS ` + table); err != nil {
			t.Fatalf("drop %s: %v", table, err)
		}
	}
	if err := CheckStartupCompatibility(ctx, handler); err != nil {
		t.Fatalf("ordinary startup requires optional attempt tables: %v", err)
	}
	if err := CheckSourceCrawlAttemptSchema(ctx, &handler); err == nil {
		t.Fatal("attempt readiness accepted missing optional storage")
	}
}

func TestSourceCrawlAttemptMySQLIsUnsupported(t *testing.T) {
	t.Parallel()
	if SourceCrawlAttemptSupported(DBMySQLStr) {
		t.Fatal("MySQL must not advertise fenced attempt persistence")
	}
	if SourceCrawlAttemptSupported(DBPostgresStr) == false || SourceCrawlAttemptSupported(DBSQLiteStr) == false {
		t.Fatal("PostgreSQL and SQLite must advertise fenced attempt persistence")
	}
}

func TestSourceCrawlAttemptAPIFailsClosedWithoutStorage(t *testing.T) {
	raw, handler := openAsyncGoalSQLite(t)
	seedSourceCrawlClaimSource(t, raw, 42, 7, "new", false)
	enableSourceCrawlClaimMode(t, raw)
	if _, err := raw.Exec(`DROP TABLE SourceCrawlClaims`); err != nil {
		t.Fatalf("drop claim storage: %v", err)
	}
	if _, err := ClaimSourceCrawlAttempts(context.Background(), &handler, sourceCrawlClaimRequest("missing")); err == nil {
		t.Fatal("claim API accepted missing storage")
	}
	var status string
	if err := raw.QueryRow(`SELECT status FROM Sources WHERE source_id=42`).Scan(&status); err != nil {
		t.Fatalf("read source after missing-storage claim: %v", err)
	}
	if status != "new" {
		t.Fatalf("claim API mutated source without storage: %q", status)
	}
}
