//go:build integration

package database

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
)

type asyncGoalSQLExecer interface {
	Exec(string, ...any) (sql.Result, error)
}

// splitAsyncGoalPostgresSQL separates statements without being confused by
// comments, quoted literals or dollar-quoted function bodies.
func splitAsyncGoalPostgresSQL(script string) []string {
	var statements []string
	var current strings.Builder
	flush := func() {
		statement := strings.TrimSpace(current.String())
		if statement != "" {
			statements = append(statements, statement)
		}
		current.Reset()
	}
	for i := 0; i < len(script); {
		switch {
		case strings.HasPrefix(script[i:], "--"):
			for i < len(script) && script[i] != '\n' {
				i++
			}
		case strings.HasPrefix(script[i:], "/*"):
			i += 2
			for i+1 < len(script) && !strings.HasPrefix(script[i:], "*/") {
				i++
			}
			if i+1 < len(script) {
				i += 2
			} else {
				i = len(script)
			}
		case script[i] == '\'':
			current.WriteByte(script[i])
			i++
			for i < len(script) {
				current.WriteByte(script[i])
				if script[i] == '\'' {
					if i+1 < len(script) && script[i+1] == '\'' {
						current.WriteByte(script[i+1])
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
		case script[i] == '"':
			current.WriteByte(script[i])
			i++
			for i < len(script) {
				current.WriteByte(script[i])
				if script[i] == '"' {
					if i+1 < len(script) && script[i+1] == '"' {
						current.WriteByte(script[i+1])
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
		case script[i] == '$':
			j := i + 1
			for j < len(script) && (script[j] == '_' || script[j] >= '0' && script[j] <= '9' ||
				script[j] >= 'A' && script[j] <= 'Z' || script[j] >= 'a' && script[j] <= 'z') {
				j++
			}
			if j < len(script) && script[j] == '$' {
				tag := script[i : j+1]
				current.WriteString(tag)
				i = j + 1
				end := strings.Index(script[i:], tag)
				if end < 0 {
					current.WriteString(script[i:])
					i = len(script)
				} else {
					current.WriteString(script[i : i+end+len(tag)])
					i += end + len(tag)
				}
			} else {
				current.WriteByte(script[i])
				i++
			}
		case script[i] == ';':
			flush()
			i++
		default:
			current.WriteByte(script[i])
			i++
		}
	}
	flush()
	return statements
}

func applyAsyncGoalPostgresScript(execer asyncGoalSQLExecer, script, user string) error {
	script = strings.ReplaceAll(script, ":CROWLER_DB_USER", pq.QuoteIdentifier(user))
	for _, statement := range splitAsyncGoalPostgresSQL(script) {
		upper := strings.ToUpper(statement)
		if upper == "BEGIN" || upper == "COMMIT" ||
			strings.HasPrefix(upper, "SELECT PG_ADVISORY_XACT_LOCK") {
			continue
		}
		if _, err := execer.Exec(statement); err != nil {
			return fmt.Errorf("%w\n%s", err, statement)
		}
	}
	return nil
}

func openAsyncGoalPostgresAdminDB(t *testing.T) *sql.DB {
	t.Helper()
	user := os.Getenv("POSTGRES_USER")
	password := os.Getenv("POSTGRES_PASSWORD")
	if user == "" {
		t.Skip("POSTGRES_USER is not set; administrative PostgreSQL rehearsal is unavailable")
	}
	valueOrDefault := func(name, fallback string) string {
		if value := os.Getenv(name); value != "" {
			return value
		}
		return fallback
	}
	port, err := strconv.Atoi(valueOrDefault("DOCKER_POSTGRES_DB_PORT", "5432"))
	if err != nil {
		t.Fatalf("invalid DOCKER_POSTGRES_DB_PORT: %v", err)
	}
	dsn := &url.URL{
		Scheme: "postgres",
		Host:   fmt.Sprintf("%s:%d", valueOrDefault("DOCKER_POSTGRES_DB_HOST", "127.0.0.1"), port),
		Path:   valueOrDefault("DOCKER_POSTGRES_DB_NAME", "SitesIndex"),
		User:   url.UserPassword(user, password),
	}
	query := dsn.Query()
	query.Set("sslmode", valueOrDefault("DOCKER_POSTGRES_SSL_MODE", "disable"))
	dsn.RawQuery = query.Encode()
	database, err := sql.Open(DBPostgresStr, dsn.String())
	if err != nil {
		t.Fatalf("open administrative PostgreSQL connection: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := database.PingContext(ctx); err != nil {
		t.Skipf("administrative PostgreSQL connection is unavailable: %v", err)
	}
	return database
}

func currentPostgresUser(t *testing.T, db *sql.DB) string {
	t.Helper()
	var user string
	if err := db.QueryRow(`SELECT current_user`).Scan(&user); err != nil {
		t.Fatalf("read current PostgreSQL user: %v", err)
	}
	return user
}

func readAsyncGoalPostgresAddon(t *testing.T) string {
	t.Helper()
	addon, err := os.ReadFile("db_migrations/postgresql-agent-goal-v1.17.pgsql")
	if err != nil {
		t.Fatalf("read PostgreSQL goal addon: %v", err)
	}
	return string(addon)
}

func dropAsyncGoalPostgresTables(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, table := range []string{
		"AgentGoalRunVerificationEvidence", "AgentGoalRunEvidence",
		"AgentGoalRunAuditRefs", "AgentGoalRunTransitions", "AgentGoalRunWork",
		"AgentGoalRunSources", "AgentGoalRunTools", "AgentGoalRuns",
	} {
		if _, err := db.Exec(`DROP TABLE IF EXISTS ` + table + ` CASCADE`); err != nil {
			t.Fatalf("drop %s: %v", table, err)
		}
	}
}

func countAsyncGoalPostgresRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

func TestPostgresAsyncGoalPersistenceSchema(t *testing.T) {
	db, sqlDB := openPostgresIntegrationTestDB(t)
	adminDB := openAsyncGoalPostgresAdminDB(t)
	ctx := context.Background()
	if err := CheckAsyncGoalPersistenceSchema(ctx, db); err != nil {
		t.Fatalf("PostgreSQL goal persistence readiness: %v", err)
	}
	var version, generation string
	if err := sqlDB.QueryRow(`SELECT version FROM DBSchemaVersion WHERE is_current`).Scan(&version); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if err := sqlDB.QueryRow(`SELECT writer_generation FROM DatabaseWriterCompatibility WHERE lock_id=1`).Scan(&generation); err != nil {
		t.Fatalf("read writer generation: %v", err)
	}
	if version != "1.17" || generation != WriterGeneration {
		t.Fatalf("unexpected schema contract version=%q generation=%q", version, generation)
	}

	user := currentPostgresUser(t, sqlDB)
	addon := readAsyncGoalPostgresAddon(t)
	applicationTables := []string{
		"CrawlApprovals", "InformationSeed", "SourceOwnership",
		"TimeSeriesMetrics", "MCPMutationProposals",
	}
	before := make(map[string]int, len(applicationTables))
	for _, table := range applicationTables {
		before[table] = countAsyncGoalPostgresRows(t, sqlDB, table)
	}

	// The additive script is an administrator operation: it creates objects and
	// transfers ownership and grants to the runtime role.
	if err := applyAsyncGoalPostgresScript(adminDB, addon, user); err != nil {
		t.Fatalf("apply PostgreSQL goal addon to provisioned database: %v", err)
	}
	if err := CheckAsyncGoalPersistenceSchema(ctx, db); err != nil {
		t.Fatalf("readiness after full addon application: %v", err)
	}

	// Dropping the goal tables and reapplying must recreate the same schema.
	dropAsyncGoalPostgresTables(t, adminDB)
	if err := applyAsyncGoalPostgresScript(adminDB, addon, user); err != nil {
		t.Fatalf("create goal schema from scratch with addon: %v", err)
	}
	if err := CheckAsyncGoalPersistenceSchema(ctx, db); err != nil {
		t.Fatalf("readiness after addon recreation: %v", err)
	}

	insertSQL := `INSERT INTO AgentGoalRuns (
		goal_run_id, checkpoint_version, contract_version, revision, trace_id,
		correlation_id, agent_id, owner, principal_kind, principal_user_id,
		principal_username, principal_issuer, goal_id, goal_spec_hash,
		policy_sha256, state, slices, created_at, updated_at, deadline,
		reason_code, ledger_id, run_nonce_hash, max_slices, max_model_rounds,
		max_tool_attempts, max_result_bytes, max_evidence_records,
		max_evidence_bytes, max_retries, max_scheduled_work_requests,
		max_consecutive_denials, max_slice_elapsed_ns
	) VALUES ('run-pg-int', 2, 'goal-async-v2', 1, 'trace-pg-int', NULL,
		'agent-pg-int', 'owner', 'user', 7, 'owner', 'local', 'goal-pg-int',
		repeat('a',64), repeat('b',64), 'created', 0, now(), now(),
		now() + interval '10 minutes', 'goal_async_created', 'ledger-pg-int',
		repeat('c',64), 8, 4, 8, 4096, 4, 4096, 2, 4, 3, 120000000000)`
	if _, err := sqlDB.Exec(insertSQL); err != nil {
		t.Fatalf("insert PostgreSQL goal root: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO AgentGoalRunTools(goal_run_id, tool_name) VALUES ('run-pg-int','get_source_status')`); err != nil {
		t.Fatalf("insert PostgreSQL goal tool: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO AgentGoalRunSources(goal_run_id, source_id) VALUES ('run-pg-int',42)`); err != nil {
		t.Fatalf("insert PostgreSQL goal source: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO AgentGoalRunAuditRefs(goal_run_id, audit_ref) VALUES ('run-pg-int','audit-pg-int')`); err != nil {
		t.Fatalf("insert PostgreSQL goal audit: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO AgentGoalRunTransitions(goal_run_id, revision, operation_id, transition_hash, from_state, to_state, reason_code)
		VALUES ('run-pg-int',2,'op-pg-int',repeat('d',64),'created','running','goal_async_started')`); err != nil {
		t.Fatalf("insert PostgreSQL transition: %v", err)
	}

	// Reapplying the additive script must preserve goal history.
	if err := applyAsyncGoalPostgresScript(adminDB, addon, user); err != nil {
		t.Fatalf("reapply PostgreSQL goal addon after history: %v", err)
	}
	if roots := countAsyncGoalPostgresRows(t, sqlDB, "AgentGoalRuns"); roots != 1 {
		t.Fatalf("PostgreSQL addon changed goal roots=%d, want 1", roots)
	}
	if transitions := countAsyncGoalPostgresRows(t, sqlDB, "AgentGoalRunTransitions"); transitions != 1 {
		t.Fatalf("PostgreSQL addon changed transitions=%d, want 1", transitions)
	}
	for _, table := range applicationTables {
		if after := countAsyncGoalPostgresRows(t, sqlDB, table); after != before[table] {
			t.Fatalf("PostgreSQL addon changed %s rows=%d, want %d", table, after, before[table])
		}
	}

	// Submission validity and later completion are independent.
	if _, err := sqlDB.Exec(`INSERT INTO AgentGoalRunWork(
		goal_run_id, request_id, kind, state, proposal_sha256, approval_id,
		approval_sha256, operation_id, source_id, proposal_expires_at,
		approval_expires_at, submit_by, accepted_at, observed_at, created_at, updated_at)
		VALUES ('run-pg-int',repeat('1',64),'crawl_operation','completed',
		repeat('2',64),repeat('3',64),repeat('4',64),repeat('5',64),42,
		now()+interval '30 minutes',now()+interval '1 minute',
		now()+interval '1 minute',now(),now()+interval '3 minutes',
		now()+interval '1 minute',now()+interval '3 minutes')`); err != nil {
		t.Fatalf("completion after approval expiry rejected: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO AgentGoalRunWork(
		goal_run_id, request_id, kind, state, proposal_sha256, approval_id,
		approval_sha256, operation_id, source_id, proposal_expires_at,
		approval_expires_at, submit_by, accepted_at, observed_at, created_at, updated_at)
		VALUES ('run-pg-int',repeat('6',64),'crawl_operation','completed',
		repeat('2',64),repeat('7',64),repeat('4',64),repeat('5',64),42,
		now()+interval '30 minutes',now()+interval '30 minutes',
		now()+interval '20 minutes',now(),now()+interval '1 minute',
		now()+interval '1 minute',now()+interval '2 minutes')`); err == nil {
		t.Fatal("duplicate operation identity was accepted")
	}
	if _, err := sqlDB.Exec(`INSERT INTO AgentGoalRunWork(
		goal_run_id, request_id, kind, state, proposal_sha256, approval_id,
		approval_sha256, operation_id, source_id, proposal_expires_at,
		approval_expires_at, submit_by, accepted_at, observed_at, created_at, updated_at)
		VALUES ('run-pg-int',repeat('8',64),'crawl_operation','completed',
		repeat('2',64),repeat('9',64),repeat('4',64),repeat('a',64),42,
		now()+interval '30 minutes',now()+interval '30 minutes',
		now()+interval '20 minutes',now()+interval '4 minutes',
		now()+interval '3 minutes',now()+interval '1 minute',
		now()+interval '5 minutes')`); err == nil {
		t.Fatal("completion observed before acceptance was accepted")
	}
	// A durable operation may be accepted before its work row is inserted.
	if _, err := sqlDB.Exec(`INSERT INTO AgentGoalRunWork(
		goal_run_id, request_id, kind, state, proposal_sha256, approval_id,
		approval_sha256, operation_id, source_id, proposal_expires_at,
		approval_expires_at, submit_by, accepted_at, observed_at, created_at, updated_at)
		VALUES ('run-pg-int',repeat('b',64),'crawl_operation','completed',
		repeat('2',64),repeat('c',64),repeat('4',64),repeat('d',64),42,
		now()+interval '30 minutes',now()+interval '30 minutes',
		now()+interval '20 minutes',now(),now()+interval '1 minute',
		now()+interval '1 minute',now()+interval '2 minutes')`); err != nil {
		t.Fatalf("accepted-before-work-row insertion rejected: %v", err)
	}

	if _, err := adminDB.Exec(`DROP INDEX uq_agent_goal_work_operation`); err != nil {
		t.Fatalf("drop PostgreSQL unique index: %v", err)
	}
	if err := CheckAsyncGoalPersistenceSchema(ctx, db); err == nil {
		t.Fatal("readiness accepted a missing PostgreSQL unique index")
	}
	if err := applyAsyncGoalPostgresScript(adminDB, addon, user); err != nil {
		t.Fatalf("repair PostgreSQL unique index with addon: %v", err)
	}
	if err := CheckAsyncGoalPersistenceSchema(ctx, db); err != nil {
		t.Fatalf("readiness after PostgreSQL index repair: %v", err)
	}
}

func TestPostgresAsyncGoalPersistenceAddonRollsBack(t *testing.T) {
	db, appDB := openPostgresIntegrationTestDB(t)
	adminDB := openAsyncGoalPostgresAdminDB(t)
	ctx := context.Background()
	user := currentPostgresUser(t, appDB)
	addon := readAsyncGoalPostgresAddon(t)

	dropAsyncGoalPostgresTables(t, adminDB)
	t.Cleanup(func() {
		if err := applyAsyncGoalPostgresScript(adminDB, addon, user); err != nil {
			t.Logf("restore PostgreSQL goal schema after rollback test: %v", err)
		}
	})

	tx, err := adminDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin PostgreSQL rollback rehearsal: %v", err)
	}
	if err := applyAsyncGoalPostgresScript(tx, addon, user); err != nil {
		_ = tx.Rollback()
		t.Fatalf("apply addon inside rollback rehearsal: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("roll back PostgreSQL DDL rehearsal: %v", err)
	}
	var tables int
	if err := adminDB.QueryRow(`SELECT COUNT(*) FROM information_schema.tables
		WHERE table_schema = current_schema() AND lower(table_name) LIKE 'agentgoal%'`).Scan(&tables); err != nil {
		t.Fatalf("count goal tables after rollback: %v", err)
	}
	if tables != 0 {
		t.Fatalf("PostgreSQL DDL rollback left %d goal tables", tables)
	}
	if err := CheckAsyncGoalPersistenceSchema(ctx, db); err == nil {
		t.Fatal("readiness accepted a rolled-back PostgreSQL goal schema")
	}
}

func TestPostgresAsyncGoalPersistenceAddonRejectsIncompatible(t *testing.T) {
	adminDB := openAsyncGoalPostgresAdminDB(t)
	ctx := context.Background()
	user := currentPostgresUser(t, adminDB)
	addon := readAsyncGoalPostgresAddon(t)

	tx, err := adminDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin PostgreSQL incompatibility rehearsal: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	schema := "async_goal_incompatible"
	if _, err := tx.Exec(`CREATE SCHEMA ` + pq.QuoteIdentifier(schema)); err != nil {
		t.Fatalf("create temporary PostgreSQL schema: %v", err)
	}
	if _, err := tx.Exec(`SET LOCAL search_path TO ` + pq.QuoteIdentifier(schema)); err != nil {
		t.Fatalf("select temporary PostgreSQL schema: %v", err)
	}
	if _, err := tx.Exec(`CREATE TABLE DBSchemaVersion (
		version TEXT PRIMARY KEY,
		description TEXT,
		is_current BOOLEAN NOT NULL DEFAULT FALSE
	);
	INSERT INTO DBSchemaVersion(version, description, is_current) VALUES ('1.17','test',TRUE);
	CREATE TABLE DatabaseWriterCompatibility (
		lock_id INTEGER PRIMARY KEY,
		writer_generation TEXT NOT NULL
	);
	INSERT INTO DatabaseWriterCompatibility(lock_id, writer_generation)
		VALUES (1,'verified-ownership-v1.17');`); err != nil {
		t.Fatalf("create temporary schema markers: %v", err)
	}
	if err := applyAsyncGoalPostgresScript(tx, addon, user); err != nil {
		t.Fatalf("apply addon to empty temporary schema: %v", err)
	}
	if _, err := tx.Exec(`ALTER TABLE AgentGoalRunTools DROP CONSTRAINT chk_agent_goal_tool_name`); err != nil {
		t.Fatalf("drop goal check constraint: %v", err)
	}
	if err := applyAsyncGoalPostgresScript(tx, addon, user); err == nil {
		t.Fatal("addon accepted a table with a missing check constraint")
	}
}

func TestPostgresAsyncGoalPersistenceAddonRejectsWrongForeignKey(t *testing.T) {
	adminDB := openAsyncGoalPostgresAdminDB(t)
	ctx := context.Background()
	user := currentPostgresUser(t, adminDB)
	addon := readAsyncGoalPostgresAddon(t)

	tx, err := adminDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin PostgreSQL foreign-key rehearsal: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	schema := "async_goal_wrong_fk"
	if _, err := tx.Exec(`CREATE SCHEMA ` + pq.QuoteIdentifier(schema)); err != nil {
		t.Fatalf("create temporary PostgreSQL schema: %v", err)
	}
	if _, err := tx.Exec(`SET LOCAL search_path TO ` + pq.QuoteIdentifier(schema)); err != nil {
		t.Fatalf("select temporary PostgreSQL schema: %v", err)
	}
	if _, err := tx.Exec(`CREATE TABLE DBSchemaVersion (
		version TEXT PRIMARY KEY,
		description TEXT,
		is_current BOOLEAN NOT NULL DEFAULT FALSE
	);
	INSERT INTO DBSchemaVersion(version, description, is_current) VALUES ('1.17','test',TRUE);
	CREATE TABLE DatabaseWriterCompatibility (
		lock_id INTEGER PRIMARY KEY,
		writer_generation TEXT NOT NULL
	);
	INSERT INTO DatabaseWriterCompatibility(lock_id, writer_generation)
		VALUES (1,'verified-ownership-v1.17');`); err != nil {
		t.Fatalf("create temporary schema markers: %v", err)
	}
	if err := applyAsyncGoalPostgresScript(tx, addon, user); err != nil {
		t.Fatalf("apply addon to empty temporary schema: %v", err)
	}
	if _, err := tx.Exec(`ALTER TABLE AgentGoalRunTools DROP CONSTRAINT fk_agent_goal_tool_run;
		CREATE TABLE WrongGoalParent (goal_run_id TEXT PRIMARY KEY);
		ALTER TABLE AgentGoalRunTools ADD CONSTRAINT fk_agent_goal_tool_run
			FOREIGN KEY (goal_run_id) REFERENCES WrongGoalParent(goal_run_id) ON DELETE CASCADE`); err != nil {
		t.Fatalf("replace goal foreign key with a wrong target: %v", err)
	}
	if err := applyAsyncGoalPostgresScript(tx, addon, user); err == nil {
		t.Fatal("addon accepted a foreign key pointing at the wrong parent")
	}
}
