package database

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func openAsyncGoalSQLite(t *testing.T) (*sql.DB, Handler) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "async-goal.db")
	raw, err := sql.Open(DBSQLiteStr, path+"?_foreign_keys=on")
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })
	applySQLiteSetupFile(t, raw)
	var handler Handler = &SQLiteHandler{db: raw, dbms: DBSQLiteStr}
	return raw, handler
}

func insertAsyncGoalSQLiteRoot(t *testing.T, db *sql.DB, runID string) {
	t.Helper()
	query := `INSERT INTO AgentGoalRuns (
		goal_run_id, checkpoint_version, contract_version, revision, trace_id,
		agent_id, owner, principal_kind, principal_user_id, principal_username,
		principal_issuer, goal_id, goal_spec_hash, policy_sha256, state, slices,
		created_at, updated_at, deadline, reason_code, ledger_id, run_nonce_hash,
		max_slices, max_model_rounds, max_tool_attempts, max_result_bytes,
		max_evidence_records, max_evidence_bytes, max_retries,
		max_scheduled_work_requests, max_consecutive_denials, max_slice_elapsed_ns
	) VALUES (?, 2, 'goal-async-v2', 1, 'trace-1', 'agent-1', 'owner', 'user', 7,
		'owner', 'local', 'goal-1', lower(hex(randomblob(32))),
		lower(hex(randomblob(32))), 'created', 0, CURRENT_TIMESTAMP,
		CURRENT_TIMESTAMP, datetime('now', '+10 minutes'), 'goal_async_created',
		'ledger-1', lower(hex(randomblob(32))), 8, 4, 8, 4096, 4, 4096, 2, 4, 3,
		120000000000)`
	if _, err := db.Exec(query, runID); err != nil {
		t.Fatalf("insert SQLite goal root: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO AgentGoalRunTools(goal_run_id, tool_name) VALUES (?, 'get_source_status')`, runID); err != nil {
		t.Fatalf("insert SQLite goal tool: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO AgentGoalRunSources(goal_run_id, source_id) VALUES (?, 42)`, runID); err != nil {
		t.Fatalf("insert SQLite goal source: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO AgentGoalRunAuditRefs(goal_run_id, audit_ref) VALUES (?, 'audit-1')`, runID); err != nil {
		t.Fatalf("insert SQLite goal audit ref: %v", err)
	}
}

func TestAsyncGoalPersistenceSQLiteReadiness(t *testing.T) {
	raw, handler := openAsyncGoalSQLite(t)
	ctx := context.Background()
	if !AsyncGoalPersistenceSupported("sqlite") || !AsyncGoalPersistenceSupported("postgres") {
		t.Fatal("PostgreSQL/SQLite goal persistence should be supported")
	}
	if AsyncGoalPersistenceSupported("mysql") {
		t.Fatal("MySQL goal persistence must remain unsupported")
	}
	if err := CheckAsyncGoalPersistenceSchema(ctx, &handler); err != nil {
		t.Fatalf("fresh SQLite goal schema: %v", err)
	}

	if _, err := raw.Exec(`DROP INDEX uq_agent_goal_work_operation`); err != nil {
		t.Fatalf("drop unique index: %v", err)
	}
	if err := CheckAsyncGoalPersistenceSchema(ctx, &handler); !errors.Is(err, ErrAsyncGoalPersistenceSchemaUnavailable) {
		t.Fatalf("missing unique index error = %v, want unavailable", err)
	}
	addon, err := os.ReadFile("db_migrations/sqlite-agent-goal-v1.17.sqlite3")
	if err != nil {
		t.Fatalf("read SQLite addon: %v", err)
	}
	if _, err := raw.Exec(string(addon)); err != nil {
		t.Fatalf("reapply SQLite addon: %v", err)
	}
	if err := CheckAsyncGoalPersistenceSchema(ctx, &handler); err != nil {
		t.Fatalf("readiness after addon repair: %v", err)
	}

	if _, err := raw.Exec(`DROP TABLE AgentGoalRunTools;
		CREATE TABLE AgentGoalRunTools (
			goal_run_id TEXT NOT NULL,
			tool_name   TEXT NOT NULL,
			PRIMARY KEY (goal_run_id, tool_name),
			FOREIGN KEY (goal_run_id) REFERENCES AgentGoalRuns(goal_run_id) ON DELETE CASCADE
		)`); err != nil {
		t.Fatalf("replace goal tools without its check constraint: %v", err)
	}
	if err := CheckAsyncGoalPersistenceSchema(ctx, &handler); !errors.Is(err, ErrAsyncGoalPersistenceSchemaUnavailable) {
		t.Fatalf("missing check constraint error = %v, want unavailable", err)
	}

	if _, err := raw.Exec(`ALTER TABLE AgentGoalRunEvidence ADD COLUMN payload TEXT`); err != nil {
		t.Fatalf("add forbidden payload column: %v", err)
	}
	if err := CheckAsyncGoalPersistenceSchema(ctx, &handler); !errors.Is(err, ErrAsyncGoalPersistenceSchemaUnavailable) {
		t.Fatalf("forbidden column error = %v, want unavailable", err)
	}
}

func TestAsyncGoalPersistenceSQLiteAddonPreservesData(t *testing.T) {
	raw, handler := openAsyncGoalSQLite(t)
	ctx := context.Background()
	for _, table := range []string{
		"AgentGoalRunVerificationEvidence", "AgentGoalRunEvidence",
		"AgentGoalRunAuditRefs", "AgentGoalRunTransitions", "AgentGoalRunWork",
		"AgentGoalRunSources", "AgentGoalRunTools", "AgentGoalRuns",
	} {
		if _, err := raw.Exec(`DROP TABLE IF EXISTS ` + table); err != nil {
			t.Fatalf("drop %s: %v", table, err)
		}
	}
	addon, err := os.ReadFile("db_migrations/sqlite-agent-goal-v1.17.sqlite3")
	if err != nil {
		t.Fatalf("read SQLite addon: %v", err)
	}
	if _, err := raw.Exec(string(addon)); err != nil {
		t.Fatalf("first SQLite addon: %v", err)
	}
	if err := CheckAsyncGoalPersistenceSchema(ctx, &handler); err != nil {
		t.Fatalf("readiness after addon: %v", err)
	}
	insertAsyncGoalSQLiteRoot(t, raw, "run-addon-1")
	if _, err := raw.Exec(string(addon)); err != nil {
		t.Fatalf("second SQLite addon: %v", err)
	}
	var roots, tools, sources, audits int
	if err := raw.QueryRow(`SELECT
		(SELECT COUNT(*) FROM AgentGoalRuns),
		(SELECT COUNT(*) FROM AgentGoalRunTools),
		(SELECT COUNT(*) FROM AgentGoalRunSources),
		(SELECT COUNT(*) FROM AgentGoalRunAuditRefs)`).Scan(&roots, &tools, &sources, &audits); err != nil {
		t.Fatalf("count goal history: %v", err)
	}
	if roots != 1 || tools != 1 || sources != 1 || audits != 1 {
		t.Fatalf("addon changed history roots=%d tools=%d sources=%d audits=%d", roots, tools, sources, audits)
	}
	var version, generation string
	if err := raw.QueryRow(`SELECT version FROM DBSchemaVersion WHERE is_current=1`).Scan(&version); err != nil {
		t.Fatalf("read SQLite schema version: %v", err)
	}
	if err := raw.QueryRow(`SELECT writer_generation FROM DatabaseWriterCompatibility WHERE lock_id=1`).Scan(&generation); err != nil {
		t.Fatalf("read SQLite writer generation: %v", err)
	}
	if version != "1.17" || generation != WriterGeneration {
		t.Fatalf("addon changed markers version=%q generation=%q", version, generation)
	}
	if _, err := raw.Exec(`DROP TABLE AgentGoalRunTools;
		CREATE TABLE AgentGoalRunTools (
			goal_run_id TEXT NOT NULL,
			tool_name   TEXT NOT NULL,
			PRIMARY KEY (goal_run_id, tool_name),
			FOREIGN KEY (goal_run_id) REFERENCES AgentGoalRuns(goal_run_id) ON DELETE CASCADE
		)`); err != nil {
		t.Fatalf("replace goal tools without its check constraint: %v", err)
	}
	if _, err := raw.Exec(string(addon)); err == nil {
		t.Fatal("SQLite addon accepted a table with a missing check constraint")
	}
}

func TestAsyncGoalPersistenceSQLiteMigrationConverges(t *testing.T) {
	raw, handler := openAsyncGoalSQLite(t)
	ctx := context.Background()
	for _, table := range []string{
		"AgentGoalRunVerificationEvidence", "AgentGoalRunEvidence",
		"AgentGoalRunAuditRefs", "AgentGoalRunTransitions", "AgentGoalRunWork",
		"AgentGoalRunSources", "AgentGoalRunTools", "AgentGoalRuns",
		"CrawlApprovals", "SourceOwnership",
	} {
		if _, err := raw.Exec(`DROP TABLE IF EXISTS ` + table); err != nil {
			t.Fatalf("drop %s: %v", table, err)
		}
	}
	if _, err := raw.Exec(`UPDATE DBSchemaVersion SET is_current=(version='1.16');
		UPDATE DatabaseWriterCompatibility SET writer_generation='reservation-v1.15' WHERE lock_id=1;`); err != nil {
		t.Fatalf("prepare SQLite v1.16 markers: %v", err)
	}
	migration, err := os.ReadFile("db_migrations/sqlite-migration-v1.17.sqlite3")
	if err != nil {
		t.Fatalf("read SQLite v1.17 migration: %v", err)
	}
	if _, err := raw.Exec(string(migration)); err != nil {
		t.Fatalf("apply SQLite v1.17 migration: %v", err)
	}
	var version, generation string
	if err := raw.QueryRow(`SELECT version FROM DBSchemaVersion WHERE is_current=1`).Scan(&version); err != nil {
		t.Fatalf("read migrated schema version: %v", err)
	}
	if err := raw.QueryRow(`SELECT writer_generation FROM DatabaseWriterCompatibility WHERE lock_id=1`).Scan(&generation); err != nil {
		t.Fatalf("read migrated writer generation: %v", err)
	}
	if version != "1.17" || generation != WriterGeneration {
		t.Fatalf("migration markers version=%q generation=%q", version, generation)
	}
	if err := CheckAsyncGoalPersistenceSchema(ctx, &handler); err != nil {
		t.Fatalf("readiness after SQLite migration: %v", err)
	}
}

func TestAsyncGoalPersistenceSQLiteAddonRejectsIncompatible(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad-async-goal.db")
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
		CREATE TABLE AgentGoalRuns (goal_run_id TEXT PRIMARY KEY, wrong_column TEXT);`); err != nil {
		t.Fatalf("create incompatible SQLite fixture: %v", err)
	}
	addon, err := os.ReadFile("db_migrations/sqlite-agent-goal-v1.17.sqlite3")
	if err != nil {
		t.Fatalf("read SQLite addon: %v", err)
	}
	if _, err := raw.Exec(string(addon)); err == nil {
		t.Fatal("SQLite addon accepted an incompatible pre-existing table")
	}
}

func TestAsyncGoalPersistenceSQLCoverage(t *testing.T) {
	t.Helper()
	files := []string{
		"postgresql-setup.pgsql", "sqlite-setup.sqlite3",
		"db_migrations/postgresql-migration-v1.17.pgsql",
		"db_migrations/sqlite-migration-v1.17.sqlite3",
		"db_migrations/postgresql-agent-goal-v1.17.pgsql",
		"db_migrations/sqlite-agent-goal-v1.17.sqlite3",
	}
	tables := []string{
		"AgentGoalRuns", "AgentGoalRunTools", "AgentGoalRunSources",
		"AgentGoalRunWork", "AgentGoalRunEvidence",
		"AgentGoalRunVerificationEvidence", "AgentGoalRunAuditRefs",
		"AgentGoalRunTransitions",
	}
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		text := string(content)
		for _, table := range tables {
			if !strings.Contains(text, table) {
				t.Errorf("%s missing %s", file, table)
			}
		}
	}
	mysql, err := os.ReadFile("mysql-setup.mysql")
	if err != nil {
		t.Fatalf("read MySQL setup: %v", err)
	}
	if strings.Contains(string(mysql), "AgentGoalRuns") {
		t.Fatal("MySQL setup unexpectedly advertises async goal persistence")
	}
	for _, file := range []string{
		"db_migrations/postgresql-agent-goal-v1.17.pgsql",
		"db_migrations/sqlite-agent-goal-v1.17.sqlite3",
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
}

func asyncGoalDDLBlock(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	const begin = "-- BEGIN AGENT GOAL PERSISTENCE (schema 1.17)"
	const end = "-- END AGENT GOAL PERSISTENCE (schema 1.17)"
	text := string(content)
	if strings.Count(text, begin) != 1 || strings.Count(text, end) != 1 {
		t.Fatalf("%s must contain exactly one canonical goal DDL block", path)
	}
	start := strings.Index(text, begin)
	finish := strings.Index(text, end)
	if start < 0 || finish < start {
		t.Fatalf("%s has malformed goal DDL markers", path)
	}
	return text[start : finish+len(end)]
}

func TestAsyncGoalPersistenceCanonicalDDLConverges(t *testing.T) {
	t.Parallel()

	postgresFiles := []string{
		"postgresql-setup.pgsql",
		"db_migrations/postgresql-migration-v1.17.pgsql",
		"db_migrations/postgresql-agent-goal-v1.17.pgsql",
	}
	sqliteFiles := []string{
		"sqlite-setup.sqlite3",
		"db_migrations/sqlite-migration-v1.17.sqlite3",
		"db_migrations/sqlite-agent-goal-v1.17.sqlite3",
	}
	for _, files := range [][]string{postgresFiles, sqliteFiles} {
		want := asyncGoalDDLBlock(t, files[0])
		for _, file := range files[1:] {
			if got := asyncGoalDDLBlock(t, file); got != want {
				t.Errorf("%s goal DDL differs from %s", file, files[0])
			}
		}
	}
}

func TestAsyncGoalPersistenceKeepsSchema117(t *testing.T) {
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
		"db_migrations/postgresql-agent-goal-v1.17.pgsql",
		"db_migrations/sqlite-agent-goal-v1.17.sqlite3",
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
	mysqlFiles := []string{"mysql-setup.mysql", "db_migrations/mysql-migration-v1.17.mysql"}
	for _, file := range mysqlFiles {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		text := string(content)
		if !strings.Contains(text, "1.17") {
			t.Errorf("%s no longer advertises schema 1.17", file)
		}
		if strings.Contains(text, "AgentGoalRun") {
			t.Errorf("%s unexpectedly advertises asynchronous goal persistence", file)
		}
	}
}

type asyncGoalUnsupportedHandler struct{ Handler }

func (asyncGoalUnsupportedHandler) DBMS() string { return DBMySQLStr }

func TestAsyncGoalPersistenceMySQLIsUnsupported(t *testing.T) {
	t.Parallel()

	if AsyncGoalPersistenceSupported(DBMySQLStr) {
		t.Fatal("MySQL must not advertise asynchronous goal persistence")
	}
	var handler Handler = asyncGoalUnsupportedHandler{}
	if err := CheckAsyncGoalPersistenceSchema(context.Background(), &handler); !errors.Is(err, ErrAsyncGoalPersistenceUnsupported) {
		t.Fatalf("MySQL readiness error = %v, want unsupported", err)
	}
}

func TestAsyncGoalPersistenceReadinessDoesNotAffectStartup(t *testing.T) {
	t.Parallel()

	handler := startupFixture(t, "1.17", true, WriterGeneration)
	if err := CheckStartupCompatibility(context.Background(), handler); err != nil {
		t.Fatalf("ordinary 1.17 startup rejected: %v", err)
	}
	if err := CheckAsyncGoalPersistenceSchema(context.Background(), &handler); !errors.Is(err, ErrAsyncGoalPersistenceSchemaUnavailable) {
		t.Fatalf("goal readiness error = %v, want unavailable", err)
	}
}

func TestAsyncGoalPersistenceSQLiteRejectsInvalidTimestamps(t *testing.T) {
	raw, _ := openAsyncGoalSQLite(t)
	insertAsyncGoalSQLiteRoot(t, raw, "run-invalid-time")

	for _, statement := range []string{
		`UPDATE AgentGoalRuns SET created_at='not-a-time' WHERE goal_run_id='run-invalid-time'`,
		`UPDATE AgentGoalRuns SET updated_at='not-a-time' WHERE goal_run_id='run-invalid-time'`,
		`UPDATE AgentGoalRuns SET deadline='not-a-time' WHERE goal_run_id='run-invalid-time'`,
		`INSERT INTO AgentGoalRunAuditRefs(goal_run_id, audit_ref, created_at)
			VALUES ('run-invalid-time','audit-invalid-time','not-a-time')`,
		`INSERT INTO AgentGoalRunTransitions(goal_run_id, revision, operation_id, transition_hash,
			from_state, to_state, reason_code, created_at)
			VALUES ('run-invalid-time',2,'op-invalid-time',lower(hex(randomblob(32))),
			'created','running','goal_async_started','not-a-time')`,
		`INSERT INTO AgentGoalRunWork(goal_run_id, request_id, kind, state, proposal_sha256,
			approval_id, approval_sha256, operation_id, source_id, proposal_expires_at,
			approval_expires_at, submit_by, accepted_at, observed_at, created_at, updated_at)
			VALUES ('run-invalid-time',lower(hex(randomblob(32))),'crawl_operation','completed',
			lower(hex(randomblob(32))),lower(hex(randomblob(32))),lower(hex(randomblob(32))),
			lower(hex(randomblob(32))),42,datetime('now','+30 minutes'),
			datetime('now','+30 minutes'),datetime('now','+20 minutes'),'not-a-time',
			datetime('now','+1 minute'),datetime('now'),datetime('now','+2 minutes'))`,
		`INSERT INTO AgentGoalRunEvidence(goal_run_id, evidence_id, kind, trust_class, producer,
			source_ids, observed_at, expires_at, payload_sha256, record_sha256, size_bytes, created_at)
			VALUES ('run-invalid-time','ev_'||lower(hex(randomblob(16))),'tool_observation',
			'tool_observation','tool:test','[]','not-a-time',datetime('now','+1 hour'),
			lower(hex(randomblob(32))),lower(hex(randomblob(32))),128,datetime('now'))`,
	} {
		if _, err := raw.Exec(statement); err == nil {
			t.Errorf("invalid timestamp was accepted by: %s", statement)
		}
	}
}

func TestAsyncGoalPersistenceSQLiteAddonRejectsWrongForeignKey(t *testing.T) {
	raw, _ := openAsyncGoalSQLite(t)
	if _, err := raw.Exec(`PRAGMA foreign_keys=OFF;
		DROP TABLE AgentGoalRunTools;
		CREATE TABLE WrongGoalParent (goal_run_id TEXT PRIMARY KEY);
		CREATE TABLE AgentGoalRunTools (
			goal_run_id TEXT NOT NULL,
			tool_name   TEXT NOT NULL,
			PRIMARY KEY (goal_run_id, tool_name),
			FOREIGN KEY (goal_run_id) REFERENCES WrongGoalParent(goal_run_id) ON DELETE CASCADE,
			CONSTRAINT chk_agent_goal_tool_name CHECK (length(trim(tool_name)) > 0)
		);
		PRAGMA foreign_keys=ON;`); err != nil {
		t.Fatalf("replace goal foreign key with a wrong target: %v", err)
	}
	addon, err := os.ReadFile("db_migrations/sqlite-agent-goal-v1.17.sqlite3")
	if err != nil {
		t.Fatalf("read SQLite addon: %v", err)
	}
	if _, err := raw.Exec(string(addon)); err == nil {
		t.Fatal("SQLite addon accepted a foreign key pointing at the wrong parent")
	}
}

func TestAsyncGoalPersistenceSQLiteRejectsOversizedValues(t *testing.T) {
	raw, _ := openAsyncGoalSQLite(t)
	insertAsyncGoalSQLiteRoot(t, raw, "run-oversized")

	if _, err := raw.Exec(`UPDATE AgentGoalRuns SET owner=? WHERE goal_run_id='run-oversized'`,
		strings.Repeat("a", 257)); err == nil {
		t.Fatal("oversized owner was accepted")
	}
	if _, err := raw.Exec(`UPDATE AgentGoalRunTools SET tool_name=? WHERE goal_run_id='run-oversized'`,
		strings.Repeat("a", 65)); err == nil {
		t.Fatal("oversized tool name was accepted")
	}
	if _, err := raw.Exec(`INSERT INTO AgentGoalRunAuditRefs(goal_run_id, audit_ref)
		VALUES ('run-oversized', ?)`, strings.Repeat("a", 129)); err == nil {
		t.Fatal("oversized audit reference was accepted")
	}
}
