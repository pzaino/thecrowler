//go:build integration

package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
	cfg "github.com/pzaino/thecrowler/pkg/config"
)

// TestPostgresMCPMutationBackupRestoreRehearsal proves a backup produced by
// the G3 deployment command can be restored into a fresh disposable database.
func TestPostgresMCPMutationBackupRestoreRehearsal(t *testing.T) {
	target, err := mcpPostgresDisposableTarget()
	if errors.Is(err, errMCPPostgresDisposableOptInRequired) || errors.Is(err, errMCPPostgresDSNRequired) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatalf("disposable PostgreSQL target: %v", err)
	}
	sourceDB, admin, sourceDSN := newMCPPostgresDisposableDB(t, target)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var user string
	if err := sourceDB.QueryRowContext(ctx, `SELECT current_user`).Scan(&user); err != nil {
		t.Fatalf("read PostgreSQL current user: %v", err)
	}
	applyMCPMutationPostgresMigration(t, sourceDB, user)
	var handler Handler = &PostgresHandler{db: sourceDB, dbms: DBPostgresStr, connStr: sourceDSN}
	if err := InsertMCPMutationAudit(ctx, &handler, validMCPMutationAuditEvent(time.Now().UTC())); err != nil {
		t.Fatalf("insert restore-rehearsal history: %v", err)
	}
	parsed, err := url.Parse(sourceDSN)
	if err != nil {
		t.Fatalf("parse source DSN: %v", err)
	}
	password, _ := parsed.User.Password()
	port := 0
	if parsed.Port() != "" {
		if _, err := fmt.Sscanf(parsed.Port(), "%d", &port); err != nil {
			t.Fatalf("parse source port: %v", err)
		}
	}
	config := cfg.Config{Database: cfg.Database{
		Host: parsed.Hostname(), Port: port, User: parsed.User.Username(),
		Password: password, DBName: strings.TrimPrefix(parsed.Path, "/"),
		SSLMode: "disable",
	}}
	backupPath := filepath.Join(t.TempDir(), "mcp-history.dump")
	if err := BackupMCPMutationHistory(ctx, handler, config, backupPath, "pg_dump"); err != nil {
		t.Fatalf("create PostgreSQL backup: %v", err)
	}
	restoreName := "crowler_mcp_restore_" + mcpPostgresRandomSuffix(t)
	if _, err := admin.ExecContext(ctx, `CREATE DATABASE `+pq.QuoteIdentifier(restoreName)); err != nil {
		t.Fatalf("create restore database: %v", err)
	}
	t.Cleanup(func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dropCancel()
		if _, err := admin.ExecContext(dropCtx, `DROP DATABASE IF EXISTS `+pq.QuoteIdentifier(restoreName)+` WITH (FORCE)`); err != nil {
			t.Errorf("drop restore database: %v", err)
		}
	})
	restoreURL := *parsed
	restoreURL.Path = "/" + restoreName
	restoreDSN := restoreURL.String()
	cmd := exec.CommandContext(ctx, "pg_restore", "--dbname", restoreDSN, "--no-owner", "--no-privileges", backupPath)
	cmd.Env = append(os.Environ(), "PGPASSWORD="+password)
	output, restoreErr := cmd.CombinedOutput()
	if restoreErr != nil && !knownPostgresRestoreCompatibilityError(string(output)) {
		t.Fatalf("pg_restore rehearsal failed: %v: %s", restoreErr, strings.TrimSpace(string(output)))
	}
	restored, err := sql.Open(DBPostgresStr, restoreDSN)
	if err != nil {
		t.Fatalf("open restored database: %v", err)
	}
	defer restored.Close()
	var auditCount int
	if err := restored.QueryRowContext(ctx, `SELECT COUNT(*) FROM MCPMutationAudit`).Scan(&auditCount); err != nil {
		t.Fatalf("read restored audit history: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("restored audit count = %d, want 1", auditCount)
	}
}

// knownPostgresRestoreCompatibilityError reports the single harmless mismatch
// produced when a newer pg_dump client restores into an older PostgreSQL
// server: the dump preamble sets transaction_timeout, which PostgreSQL 15 does
// not recognize. pg_restore continues and restores the schema and data, but
// exits nonzero. The test still verifies the restored rows, so only that exact
// single-error signature is tolerated.
func knownPostgresRestoreCompatibilityError(output string) bool {
	const marker = `unrecognized configuration parameter "transaction_timeout"`
	if !strings.Contains(output, marker) {
		return false
	}
	errors := 0
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "pg_restore: error:") {
			errors++
		}
	}
	return errors == 1
}
