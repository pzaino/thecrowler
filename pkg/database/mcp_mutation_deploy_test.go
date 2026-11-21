package database

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func applySQLiteSetupFile(t *testing.T, db *sql.DB) {
	t.Helper()
	setup, err := os.ReadFile("sqlite-setup.sqlite3")
	if err != nil {
		t.Fatalf("read SQLite setup: %v", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(string(setup)); err != nil {
		t.Fatalf("apply SQLite setup: %v", err)
	}
}

func openSQLiteG3Handler(t *testing.T, path string) Handler {
	t.Helper()
	config := cfg.Config{Database: cfg.Database{
		Type: DBSQLiteStr, DBName: path, TransactionTimeout: 30,
	}}
	handler, err := ConnectForMaintenance(context.Background(), config)
	if err != nil {
		t.Fatalf("connect SQLite maintenance handler: %v", err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	return handler
}

func dropMCPMutationCapabilityObjects(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, statement := range []string{
		`DROP TABLE IF EXISTS MCPMutationDispatchAttempts`,
		`DROP TABLE IF EXISTS MCPMutationApprovals`,
		`DROP TABLE IF EXISTS MCPMutationAudit`,
		`DROP TABLE IF EXISTS MCPMutationRunQuota`,
		`DROP TABLE IF EXISTS MCPMutationPrincipalQuota`,
		`DROP TABLE IF EXISTS MCPMutationProposals`,
		`DROP TABLE IF EXISTS SchemaCapabilities`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("drop MCP fixture object: %v", err)
		}
	}
}

func TestMCPMutationDeploymentSQLiteAppliesAndDisablesWithoutLosingHistory(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "g3-deploy.db")
	raw, err := sql.Open(DBSQLiteStr, path)
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	applySQLiteSetupFile(t, raw)
	dropMCPMutationCapabilityObjects(t, raw)
	if err := raw.Close(); err != nil {
		t.Fatalf("close SQLite: %v", err)
	}

	handler := openSQLiteG3Handler(t, path)
	if err := CheckSchemaVersion117(ctx, handler); err != nil {
		t.Fatalf("schema version: %v", err)
	}
	if err := CheckStartupCompatibility(ctx, handler); err != nil {
		t.Fatalf("base compatibility: %v", err)
	}
	if err := CheckMCPMutationCapability(ctx, &handler); !errors.Is(err, ErrMCPMutationSchemaUnavailable) {
		t.Fatalf("pre-deployment capability error = %v", err)
	}
	if err := ApplyMCPMutationMigration(ctx, handler, ""); err != nil {
		t.Fatalf("apply SQLite MCP migration: %v", err)
	}
	if err := CheckMCPMutationCapability(ctx, &handler); err != nil {
		t.Fatalf("post-deployment capability: %v", err)
	}
	if err := CheckStartupCompatibility(ctx, handler); err != nil {
		t.Fatalf("post-deployment compatibility: %v", err)
	}
	event := validMCPMutationAuditEvent(time.Now().UTC())
	if err := InsertMCPMutationAudit(ctx, &handler, event); err != nil {
		t.Fatalf("insert history: %v", err)
	}
	before, err := CountMCPMutationHistory(ctx, handler)
	if err != nil {
		t.Fatalf("count history before disable: %v", err)
	}
	removed, err := DisableMCPMutationCapability(ctx, handler)
	if err != nil {
		t.Fatalf("disable capability: %v", err)
	}
	if removed != 1 {
		t.Fatalf("disable removed %d markers, want 1", removed)
	}
	after, err := CountMCPMutationHistory(ctx, handler)
	if err != nil {
		t.Fatalf("count history after disable: %v", err)
	}
	if after["MCPMutationAudit"] != before["MCPMutationAudit"] {
		t.Fatalf("disable changed audit history: before=%d after=%d", before["MCPMutationAudit"], after["MCPMutationAudit"])
	}
	if err := CheckMCPMutationCapability(ctx, &handler); !errors.Is(err, ErrMCPMutationSchemaUnavailable) {
		t.Fatalf("post-disable capability error = %v", err)
	}
	var count int
	sqlite := handler.(*SQLiteHandler)
	if err := sqlite.db.QueryRow(`SELECT COUNT(*) FROM MCPMutationAudit`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("audit history after disable count=%d err=%v", count, err)
	}

	backupPath := filepath.Join(t.TempDir(), "g3-history.db")
	if err := BackupMCPMutationHistory(ctx, handler, cfg.Config{Database: cfg.Database{
		Type: DBSQLiteStr, DBName: path,
	}}, backupPath, ""); err != nil {
		t.Fatalf("SQLite backup: %v", err)
	}
	backup, err := sql.Open(DBSQLiteStr, backupPath+"?_foreign_keys=on")
	if err != nil {
		t.Fatalf("open SQLite backup: %v", err)
	}
	defer backup.Close()
	if err := backup.QueryRow(`SELECT COUNT(*) FROM MCPMutationAudit`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("backup audit count=%d err=%v", count, err)
	}
}

func TestSQLiteForeignKeyEnforcementRejectsOrphansAndReportsViolations(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "g3-fk.db")
	raw, err := sql.Open(DBSQLiteStr, path)
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	raw.SetMaxOpenConns(1)
	for _, statement := range []string{
		`CREATE TABLE fk_parent (id INTEGER PRIMARY KEY)`,
		`CREATE TABLE fk_child (id INTEGER PRIMARY KEY, parent_id INTEGER REFERENCES fk_parent(id))`,
		`INSERT INTO fk_child (id, parent_id) VALUES (1, 999)`,
	} {
		if _, err := raw.Exec(statement); err != nil {
			t.Fatalf("create FK violation fixture: %v", err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close SQLite: %v", err)
	}
	handler := openSQLiteG3Handler(t, path)
	if err := CheckSQLiteForeignKeyEnforcement(ctx, handler); err == nil ||
		!strings.Contains(err.Error(), "foreign key violations") {
		t.Fatalf("orphan FK check error = %v", err)
	}
}

func TestSQLiteRuntimeForeignKeysEnforcedOnPooledConnections(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "g3-pool.db")
	handler := openSQLiteG3Handler(t, path)
	sqlite := handler.(*SQLiteHandler)
	sqlite.db.SetMaxOpenConns(2)
	conn1, err := sqlite.db.Conn(ctx)
	if err != nil {
		t.Fatalf("open connection 1: %v", err)
	}
	defer conn1.Close()
	conn2, err := sqlite.db.Conn(ctx)
	if err != nil {
		t.Fatalf("open connection 2: %v", err)
	}
	defer conn2.Close()
	for i, conn := range []*sql.Conn{conn1, conn2} {
		var enabled int
		if err := conn.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&enabled); err != nil {
			t.Fatalf("read connection %d foreign_keys: %v", i+1, err)
		}
		if enabled != 1 {
			t.Fatalf("connection %d foreign_keys = %d, want 1", i+1, enabled)
		}
	}
	for _, statement := range []string{
		`CREATE TABLE fk_parent (id INTEGER PRIMARY KEY)`,
		`CREATE TABLE fk_child (id INTEGER PRIMARY KEY, parent_id INTEGER REFERENCES fk_parent(id))`,
	} {
		if _, err := conn1.ExecContext(ctx, statement); err != nil {
			t.Fatalf("create FK fixture: %v", err)
		}
	}
	if _, err := conn1.ExecContext(ctx, `INSERT INTO fk_child (id, parent_id) VALUES (1, 999)`); err == nil {
		t.Fatalf("orphan insert on connection 1 succeeded")
	}
	if _, err := conn2.ExecContext(ctx, `INSERT INTO fk_child (id, parent_id) VALUES (2, 999)`); err == nil {
		t.Fatalf("orphan insert on connection 2 succeeded")
	}
}

func TestMCPMutationMigrationScriptsEmbeddedAt117(t *testing.T) {
	for _, backend := range []string{"postgres", "sqlite"} {
		script, err := MCPMutationMigrationScript(backend)
		if err != nil {
			t.Fatalf("%s migration script: %v", backend, err)
		}
		for _, fragment := range []string{
			"MCPMutationProposals", "MCPMutationApprovals", "MCPMutationAudit",
			"SchemaCapabilities", MCPMutationCapability, MCPMutationCapabilityChecksum,
		} {
			if !strings.Contains(script, fragment) {
				t.Errorf("%s migration script missing %q", backend, fragment)
			}
		}
	}
	if RequiredSchemaVersion != "1.17" || WriterGeneration != "verified-ownership-v1.17" {
		t.Fatalf("schema contract changed: version=%q generation=%q", RequiredSchemaVersion, WriterGeneration)
	}
}

func TestMCPMutationDeploymentRejectsWrongSchemaVersion(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "g3-wrong-version.db")
	raw, err := sql.Open(DBSQLiteStr, path)
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	raw.SetMaxOpenConns(1)
	if _, err := raw.Exec(`CREATE TABLE DBSchemaVersion (
		schema_version_id INTEGER PRIMARY KEY AUTOINCREMENT,
		version TEXT NOT NULL UNIQUE,
		is_current INTEGER NOT NULL DEFAULT 1
	)`); err != nil {
		t.Fatalf("create schema version table: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO DBSchemaVersion (version, is_current) VALUES ('1.16', 1)`); err != nil {
		t.Fatalf("mark schema 1.16: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close SQLite: %v", err)
	}
	handler := openSQLiteG3Handler(t, path)
	if err := CheckSchemaVersion117(ctx, handler); err == nil ||
		!strings.Contains(err.Error(), `required "1.17"`) {
		t.Fatalf("wrong schema version error = %v", err)
	}
}
