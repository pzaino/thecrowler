package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func TestParseOptionsRequiresBackupForApply(t *testing.T) {
	if _, err := parseOptions([]string{"-apply"}); err == nil {
		t.Fatalf("apply without backup directory was accepted")
	}
	opts, err := parseOptions([]string{"-apply", "-backup-dir", t.TempDir()})
	if err != nil {
		t.Fatalf("parse apply options: %v", err)
	}
	if !opts.apply || opts.check || opts.disable {
		t.Fatalf("unexpected action selection: %+v", opts)
	}
}

func TestParseOptionsMutuallyExclusive(t *testing.T) {
	if _, err := parseOptions([]string{"-check", "-apply", "-backup-dir", t.TempDir()}); err == nil {
		t.Fatalf("check/apply combination was accepted")
	}
	if _, err := parseOptions([]string{"-apply", "-disable", "-backup-dir", t.TempDir()}); err == nil {
		t.Fatalf("apply/disable combination was accepted")
	}
}

func TestBackupFilePath(t *testing.T) {
	config := configForBackupTest("/tmp/my database.db")
	path := backupFilePath(config, "/backups")
	if !strings.HasPrefix(path, "/backups/my_database.db-mcp-") || !strings.HasSuffix(path, ".db") {
		t.Fatalf("unexpected SQLite backup path: %s", path)
	}
	config.Database.Type = "postgres"
	config.Database.DBName = "SitesIndex"
	path = backupFilePath(config, "/backups")
	if !strings.HasPrefix(path, "/backups/SitesIndex-mcp-") || !strings.HasSuffix(path, ".dump") {
		t.Fatalf("unexpected PostgreSQL backup path: %s", path)
	}
}

func configForBackupTest(dbname string) cfg.Config {
	var config cfg.Config
	config.Database.Type = "sqlite"
	config.Database.DBName = dbname
	return config
}

func TestRunSQLiteCheckApplyDisable(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "g3-cli.db")
	raw, err := sql.Open(cdb.DBSQLiteStr, dbPath)
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	raw.SetMaxOpenConns(1)
	setup, err := os.ReadFile("../../pkg/database/sqlite-setup.sqlite3")
	if err != nil {
		t.Fatalf("read SQLite setup: %v", err)
	}
	if _, err := raw.Exec(string(setup)); err != nil {
		t.Fatalf("apply SQLite setup: %v", err)
	}
	for _, statement := range []string{
		`DROP TABLE IF EXISTS MCPMutationDispatchAttempts`,
		`DROP TABLE IF EXISTS MCPMutationApprovals`,
		`DROP TABLE IF EXISTS MCPMutationAudit`,
		`DROP TABLE IF EXISTS MCPMutationRunQuota`,
		`DROP TABLE IF EXISTS MCPMutationPrincipalQuota`,
		`DROP TABLE IF EXISTS MCPMutationProposals`,
		`DROP TABLE IF EXISTS SchemaCapabilities`,
	} {
		if _, err := raw.Exec(statement); err != nil {
			t.Fatalf("drop MCP deployment fixture: %v", err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close SQLite: %v", err)
	}
	configPath := filepath.Join(dir, "config.yaml")
	configText := "database:\n  type: sqlite\n  dbname: " + dbPath + "\n  transaction_timeout: 30\n"
	if err := os.WriteFile(configPath, []byte(configText), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := run([]string{"-config", configPath, "-check"}); err == nil {
		t.Fatalf("pre-deployment check unexpectedly passed")
	}
	backupDir := filepath.Join(dir, "backups")
	if err := run([]string{"-config", configPath, "-apply", "-backup-dir", backupDir}); err != nil {
		t.Fatalf("apply deployment: %v", err)
	}
	if err := run([]string{"-config", configPath, "-check"}); err != nil {
		t.Fatalf("post-deployment check: %v", err)
	}
	// Insert one audit row so disable can prove history preservation.
	handler, err := cdb.ConnectForMaintenance(ctx, configForSQLiteCLI(t, dbPath))
	if err != nil {
		t.Fatalf("connect for history insert: %v", err)
	}
	if err := cdb.InsertMCPMutationAudit(ctx, &handler, cdb.MCPMutationAuditEvent{
		OccurredAt: time.Now().UTC(), EventType: "mcp_mutation_admission_denied",
		ActorType: "agent", ActorID: "g3-operator", ReasonCode: "admission_denied",
		RedactedDetail: []byte(`{"stage":"proposal"}`),
	}); err != nil {
		_ = handler.Close()
		t.Fatalf("insert audit history: %v", err)
	}
	_ = handler.Close()
	if err := run([]string{"-config", configPath, "-disable"}); err != nil {
		t.Fatalf("disable capability: %v", err)
	}
	if err := run([]string{"-config", configPath, "-check"}); err == nil {
		t.Fatalf("post-disable check unexpectedly passed")
	}
	raw, err = sql.Open(cdb.DBSQLiteStr, dbPath)
	if err != nil {
		t.Fatalf("reopen SQLite: %v", err)
	}
	defer raw.Close()
	var count int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM MCPMutationAudit`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("audit history after disable count=%d err=%v", count, err)
	}
}

func configForSQLiteCLI(t *testing.T, dbPath string) cfg.Config {
	t.Helper()
	var config cfg.Config
	config.Database.Type = "sqlite"
	config.Database.DBName = dbPath
	config.Database.TransactionTimeout = 30
	return config
}
