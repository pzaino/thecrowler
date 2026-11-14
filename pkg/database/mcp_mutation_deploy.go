package database

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/lib/pq"
	cfg "github.com/pzaino/thecrowler/pkg/config"
)

//go:embed db_migrations/postgresql-mcp-mutation-v1.17.pgsql
var mcpPostgresMigration string

//go:embed db_migrations/sqlite-mcp-mutation-v1.17.sqlite3
var mcpSQLiteMigration string

// MCPMutationMigrationScript returns the embedded additive schema-1.17 MCP
// capability script for a supported backend.
func MCPMutationMigrationScript(backend string) (string, error) {
	switch normalizeDBMS(backend) {
	case "postgres":
		return mcpPostgresMigration, nil
	case "sqlite":
		return mcpSQLiteMigration, nil
	default:
		return "", fmt.Errorf("MCP mutation capability is unsupported for backend %q", backend)
	}
}

// CurrentSchemaVersion returns the active DBSchemaVersion row.
func CurrentSchemaVersion(ctx context.Context, db Handler) (string, error) {
	if db == nil {
		return "", fmt.Errorf("database handler is nil")
	}
	var version string
	err := db.QueryRowContext(ctx, `SELECT version FROM DBSchemaVersion WHERE is_current = `+trueLiteral(db.DBMS())).Scan(&version)
	if err != nil {
		return "", fmt.Errorf("read active schema version: %w", err)
	}
	return version, nil
}

// CheckSchemaVersion117 refuses to deploy the additive capability on a
// database that is not already on the base schema 1.17 contract.
func CheckSchemaVersion117(ctx context.Context, db Handler) error {
	version, err := CurrentSchemaVersion(ctx, db)
	if err != nil {
		return err
	}
	if version != RequiredSchemaVersion {
		return fmt.Errorf("database schema version is %q, required %q; apply the base v%s migration first", version, RequiredSchemaVersion, RequiredSchemaVersion)
	}
	return nil
}

// ApplyMCPMutationMigration applies the additive, re-runnable schema-1.17 MCP
// capability without changing the schema version or writer generation.
func ApplyMCPMutationMigration(ctx context.Context, db Handler, dbUser string) error {
	if db == nil {
		return fmt.Errorf("database handler is nil")
	}
	switch normalizeDBMS(db.DBMS()) {
	case "postgres":
		if strings.TrimSpace(dbUser) == "" {
			return fmt.Errorf("database user is required for PostgreSQL MCP mutation ownership and grants")
		}
		return applyMCPPostgresMigration(ctx, db, dbUser)
	case "sqlite":
		return applyMCPSQLiteMigration(ctx, db)
	default:
		return fmt.Errorf("MCP mutation capability is unsupported for backend %q", db.DBMS())
	}
}

func applyMCPPostgresMigration(ctx context.Context, db Handler, dbUser string) error {
	script := strings.ReplaceAll(mcpPostgresMigration, ":CROWLER_DB_USER", pq.QuoteIdentifier(dbUser))
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return NormalizeTransactionError(ctx, err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range mcpDeploymentStatements(script) {
		upper := strings.ToUpper(strings.TrimSpace(statement))
		if upper == "BEGIN" || upper == "COMMIT" {
			continue
		}
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply PostgreSQL MCP mutation migration: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return NormalizeTransactionError(ctx, err)
	}
	return nil
}

func applyMCPSQLiteMigration(ctx context.Context, db Handler) error {
	h, ok := db.(*SQLiteHandler)
	if !ok || h.db == nil {
		return fmt.Errorf("SQLite handler is unavailable")
	}
	conn, err := h.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("open SQLite migration connection: %w", err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return NormalizeTransactionError(ctx, err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range mcpDeploymentStatements(mcpSQLiteMigration) {
		upper := strings.ToUpper(strings.TrimSpace(statement))
		if upper == "BEGIN IMMEDIATE" || upper == "COMMIT" ||
			strings.HasPrefix(upper, "PRAGMA ") {
			continue
		}
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply SQLite MCP mutation migration: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return NormalizeTransactionError(ctx, err)
	}
	return nil
}

func mcpDeploymentStatements(script string) []string {
	var clean strings.Builder
	for _, line := range strings.Split(script, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		clean.WriteString(line)
		clean.WriteString("\n")
	}
	var statements []string
	for _, statement := range strings.Split(clean.String(), ";") {
		statement = strings.TrimSpace(statement)
		if statement != "" {
			statements = append(statements, statement)
		}
	}
	return statements
}

// DisableMCPMutationCapability removes only the additive capability marker.
// It deliberately leaves all proposal, approval, dispatch and audit rows in
// place so rollback does not destroy history.
func DisableMCPMutationCapability(ctx context.Context, db Handler) (int64, error) {
	if db == nil {
		return 0, fmt.Errorf("database handler is nil")
	}
	switch normalizeDBMS(db.DBMS()) {
	case "postgres", "sqlite":
	default:
		return 0, fmt.Errorf("MCP mutation capability is unsupported for backend %q", db.DBMS())
	}
	p := mcpMutationPlaceholder(db, 1)
	res, err := db.ExecContext(ctx, `DELETE FROM SchemaCapabilities WHERE capability = `+p, MCPMutationCapability)
	if err != nil {
		if errors.Is(mcpMutationStoreError(err), ErrMCPMutationSchemaUnavailable) {
			return 0, nil
		}
		return 0, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return affected, nil
}

// CheckSQLiteForeignKeyEnforcement verifies that the runtime connection has
// foreign_keys enabled and that the database contains no existing violations.
func CheckSQLiteForeignKeyEnforcement(ctx context.Context, db Handler) error {
	if db == nil || normalizeDBMS(db.DBMS()) != "sqlite" {
		return nil
	}
	var enabled int
	if err := db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&enabled); err != nil {
		return fmt.Errorf("read SQLite foreign_keys setting: %w", err)
	}
	if enabled != 1 {
		return fmt.Errorf("SQLite foreign_keys is disabled; open the database with _foreign_keys=on")
	}
	rows, err := db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return fmt.Errorf("run SQLite foreign_key_check: %w", err)
	}
	defer rows.Close()
	var violations []string
	for rows.Next() {
		var table string
		var rowID sql.NullInt64
		var parent sql.NullString
		var fkID sql.NullInt64
		if err := rows.Scan(&table, &rowID, &parent, &fkID); err != nil {
			return fmt.Errorf("read SQLite foreign key violation: %w", err)
		}
		if len(violations) < 10 {
			violations = append(violations,
				fmt.Sprintf("%s rowid=%v parent=%s fk=%v", table, rowID.Int64, parent.String, fkID.Int64))
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate SQLite foreign key violations: %w", err)
	}
	if len(violations) != 0 {
		return fmt.Errorf("SQLite foreign key violations: %s", strings.Join(violations, "; "))
	}
	return nil
}

// CheckMCPMutationPrivileges verifies the application user's table and
// sequence privileges after deployment. SQLite privileges are filesystem
// enforced, so the check is a no-op there.
func CheckMCPMutationPrivileges(ctx context.Context, db Handler, dbUser string) error {
	if db == nil || normalizeDBMS(db.DBMS()) != "postgres" {
		return nil
	}
	if strings.TrimSpace(dbUser) == "" {
		return fmt.Errorf("database user is required for PostgreSQL privilege verification")
	}
	checks := []struct {
		table      string
		privileges string
	}{
		{"MCPMutationProposals", "SELECT,INSERT,UPDATE"},
		{"MCPMutationApprovals", "SELECT,INSERT,UPDATE"},
		{"MCPMutationDispatchAttempts", "SELECT,INSERT,UPDATE"},
		{"MCPMutationAudit", "SELECT,INSERT"},
		{"MCPMutationRunQuota", "SELECT,INSERT,UPDATE"},
		{"MCPMutationPrincipalQuota", "SELECT,INSERT,UPDATE"},
		{"SchemaCapabilities", "SELECT,INSERT"},
	}
	for _, check := range checks {
		var allowed bool
		if err := db.QueryRowContext(ctx,
			`SELECT has_table_privilege($1, $2, $3)`,
			dbUser, check.table, check.privileges).Scan(&allowed); err != nil {
			return fmt.Errorf("check privilege on %s: %w", check.table, err)
		}
		if !allowed {
			return fmt.Errorf("database user %q lacks %s on %s", dbUser, check.privileges, check.table)
		}
	}
	var sequence sql.NullString
	if err := db.QueryRowContext(ctx,
		`SELECT pg_get_serial_sequence('MCPMutationAudit', 'event_id')`).Scan(&sequence); err != nil {
		return fmt.Errorf("find MCPMutationAudit sequence: %w", err)
	}
	if sequence.Valid && strings.TrimSpace(sequence.String) != "" {
		var allowed bool
		if err := db.QueryRowContext(ctx,
			`SELECT has_sequence_privilege($1, $2, 'USAGE,SELECT')`,
			dbUser, sequence.String).Scan(&allowed); err != nil {
			return fmt.Errorf("check MCPMutationAudit sequence privilege: %w", err)
		}
		if !allowed {
			return fmt.Errorf("database user %q lacks USAGE,SELECT on %s", dbUser, sequence.String)
		}
	}
	return nil
}

// BackupMCPMutationHistory creates a full backend-consistent database backup.
// It deliberately refuses to overwrite an existing path.
func BackupMCPMutationHistory(ctx context.Context, handler Handler, c cfg.Config, path, pgDump string) error {
	if handler == nil {
		return fmt.Errorf("database handler is nil")
	}
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("backup path is required")
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("backup path already exists: %s", path)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect backup path: %w", err)
	}
	switch normalizeDBMS(handler.DBMS()) {
	case "sqlite":
		h, ok := handler.(*SQLiteHandler)
		if !ok || h.db == nil {
			return fmt.Errorf("SQLite handler is unavailable")
		}
		quoted := strings.ReplaceAll(path, "'", "''")
		if _, err := h.db.ExecContext(ctx, `VACUUM INTO '`+quoted+`'`); err != nil {
			return fmt.Errorf("create SQLite backup: %w", err)
		}
	case "postgres":
		if strings.TrimSpace(pgDump) == "" {
			pgDump = "pg_dump"
		}
		args := []string{"--format=custom", "--file", path, "--no-password", "--lock-wait-timeout=30s"}
		if strings.TrimSpace(c.Database.Host) != "" {
			args = append(args, "--host", c.Database.Host)
		}
		if c.Database.Port != 0 {
			args = append(args, "--port", strconv.Itoa(c.Database.Port))
		}
		if strings.TrimSpace(c.Database.User) != "" {
			args = append(args, "--username", c.Database.User)
		}
		if strings.TrimSpace(c.Database.DBName) != "" {
			args = append(args, "--dbname", c.Database.DBName)
		}
		cmd := exec.CommandContext(ctx, pgDump, args...)
		cmd.Env = os.Environ()
		if c.Database.Password != "" {
			cmd.Env = append(cmd.Env, "PGPASSWORD="+c.Database.Password)
		}
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("pg_dump failed: %w: %s", err, strings.TrimSpace(string(output)))
		}
	default:
		return fmt.Errorf("backup is unsupported for backend %q", handler.DBMS())
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("verify backup file: %w", err)
	}
	if info.Size() == 0 {
		return fmt.Errorf("backup file is empty: %s", path)
	}
	return nil
}

// ConnectForMaintenance opens a single bounded maintenance connection without
// the service's indefinite retry loop.
func ConnectForMaintenance(ctx context.Context, c cfg.Config) (Handler, error) {
	handler, err := NewHandler(c)
	if err != nil {
		return nil, err
	}
	switch h := handler.(type) {
	case *PostgresHandler:
		dsn := buildConnectionString(c)
		db, err := sql.Open(DBPostgresStr, dsn)
		if err != nil {
			return nil, err
		}
		if err := db.PingContext(ctx); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("connect to PostgreSQL: %w", err)
		}
		db.SetMaxOpenConns(2)
		db.SetMaxIdleConns(1)
		h.db = db
		h.dbms = "PostgreSQL"
		h.connStr = dsn
		h.transactionTimeout = normalizedTransactionTimeoutSeconds(c.Database.TransactionTimeout)
		return h, nil
	case *SQLiteHandler:
		if err := h.Connect(c); err != nil {
			return nil, err
		}
		return h, nil
	default:
		return nil, fmt.Errorf("maintenance connection is unsupported for backend %q", c.Database.Type)
	}
}

// CountMCPMutationHistory returns row counts for the append-only MCP history
// tables. It is used to prove that a capability disable preserved every row.
func CountMCPMutationHistory(ctx context.Context, db Handler) (map[string]int64, error) {
	if db == nil {
		return nil, fmt.Errorf("database handler is nil")
	}
	tables := []string{
		"MCPMutationProposals",
		"MCPMutationApprovals",
		"MCPMutationDispatchAttempts",
		"MCPMutationAudit",
	}
	out := make(map[string]int64, len(tables))
	for _, table := range tables {
		var count int64
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil {
			return nil, fmt.Errorf("count %s: %w", table, err)
		}
		out[table] = count
	}
	return out, nil
}
