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

// Package database is responsible for handling the database
// setup, configuration and abstraction.
package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	cfg "github.com/pzaino/thecrowler/pkg/config"

	_ "github.com/lib/pq" // PostgreSQL driver
)

// ---------------------------------------------------------------
// SQLite handlers
// ---------------------------------------------------------------

// SQLiteHandler is the implementation of the DatabaseHandler interface
type SQLiteHandler struct {
	db   *sql.DB
	dbms string
}

// Connect connects to an SQLite database
func (handler *SQLiteHandler) Connect(c cfg.Config) error {
	// Construct the connection string from the Config struct
	connectionString := fmt.Sprintf("file:%s?cache=shared&mode=rwc", c.Database.DBName)

	var err error
	handler.db, err = sql.Open("sqlite3", connectionString)

	// Optimize the database connection
	if err == nil {
		_, err = handler.db.Exec("PRAGMA journal_mode=WAL;")
		if err == nil {
			_, err = handler.db.Exec("PRAGMA synchronous=1;")
		}
		if err == nil {
			err = ensureSQLiteSourceSchema(handler.db)
		}
		if err == nil {
			err = ensureSQLiteInformationSeedSchema(handler.db)
		}
	}

	return err
}

func ensureSQLiteSourceSchema(db *sql.DB) error {
	var tableName string
	err := db.QueryRow("SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'Sources'").Scan(&tableName)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}

	columns, err := sqliteColumns(db, "Sources")
	if err != nil {
		return err
	}

	migrations := []struct {
		column    string
		statement string
	}{
		{column: "name", statement: "ALTER TABLE Sources ADD COLUMN name VARCHAR(255)"},
		{column: "priority", statement: "ALTER TABLE Sources ADD COLUMN priority VARCHAR(64) DEFAULT '' NOT NULL"},
	}
	for _, migration := range migrations {
		if !columns[migration.column] {
			if _, err = db.Exec(migration.statement); err != nil {
				return err
			}
		}
	}

	if _, err = db.Exec("CREATE INDEX IF NOT EXISTS idx_sources_priority ON Sources(priority)"); err != nil {
		return err
	}

	return nil
}

type sqliteColumnMigration struct {
	column    string
	statement string
}

var sqliteInformationSeedLifecycleMigrations = []sqliteColumnMigration{
	{column: "status", statement: "ALTER TABLE InformationSeed ADD COLUMN status VARCHAR(50) DEFAULT 'new' NOT NULL"},
	{column: "priority", statement: "ALTER TABLE InformationSeed ADD COLUMN priority VARCHAR(64) DEFAULT '' NOT NULL"},
	{column: "engine", statement: "ALTER TABLE InformationSeed ADD COLUMN engine VARCHAR(256) DEFAULT '' NOT NULL"},
	{column: "last_processed_at", statement: "ALTER TABLE InformationSeed ADD COLUMN last_processed_at TIMESTAMP"},
	{column: "last_error", statement: "ALTER TABLE InformationSeed ADD COLUMN last_error TEXT"},
	{column: "last_error_at", statement: "ALTER TABLE InformationSeed ADD COLUMN last_error_at TIMESTAMP"},
	{column: "disabled", statement: "ALTER TABLE InformationSeed ADD COLUMN disabled BOOLEAN DEFAULT FALSE"},
	{column: "attempts", statement: "ALTER TABLE InformationSeed ADD COLUMN attempts INTEGER DEFAULT 0 NOT NULL"},
}

var sqliteInformationSeedLifecycleIndexes = []string{
	"CREATE INDEX IF NOT EXISTS idx_informationseed_status ON InformationSeed(status)",
	"CREATE INDEX IF NOT EXISTS idx_informationseed_priority ON InformationSeed(priority)",
	"CREATE INDEX IF NOT EXISTS idx_informationseed_disabled ON InformationSeed(disabled)",
	"CREATE INDEX IF NOT EXISTS idx_informationseed_last_processed_at ON InformationSeed(last_processed_at)",
	"CREATE INDEX IF NOT EXISTS idx_informationseed_last_error_at ON InformationSeed(last_error_at)",
	"CREATE INDEX IF NOT EXISTS idx_informationseed_processing_stale ON InformationSeed(status, disabled, last_processed_at)",
}

func ensureSQLiteInformationSeedSchema(db *sql.DB) error {
	var tableName string
	err := db.QueryRow("SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'InformationSeed'").Scan(&tableName)
	if err == sql.ErrNoRows {
		return ensureSQLiteSourceInformationSeedProvenance(db)
	}
	if err != nil {
		return err
	}

	if err = applySQLiteColumnMigrations(db, "InformationSeed", sqliteInformationSeedLifecycleMigrations); err != nil {
		return err
	}

	for _, statement := range sqliteInformationSeedLifecycleIndexes {
		if _, err = db.Exec(statement); err != nil {
			return err
		}
	}

	return ensureSQLiteSourceInformationSeedProvenance(db)
}

func applySQLiteColumnMigrations(db *sql.DB, table string, migrations []sqliteColumnMigration) error {
	columns, err := sqliteColumns(db, table)
	if err != nil {
		return err
	}

	for _, migration := range migrations {
		if columns[migration.column] {
			continue
		}
		if _, err = db.Exec(migration.statement); err != nil {
			return err
		}
		columns[migration.column] = true
	}

	return nil
}

func ensureSQLiteSourceInformationSeedProvenance(db *sql.DB) error {
	var tableName string
	err := db.QueryRow("SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'SourceInformationSeedIndex'").Scan(&tableName)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}

	migrations := []sqliteColumnMigration{
		{column: "discovery_provider", statement: "ALTER TABLE SourceInformationSeedIndex ADD COLUMN discovery_provider VARCHAR(255)"},
		{column: "discovery_query", statement: "ALTER TABLE SourceInformationSeedIndex ADD COLUMN discovery_query TEXT"},
		{column: "discovery_rank", statement: "ALTER TABLE SourceInformationSeedIndex ADD COLUMN discovery_rank INTEGER"},
		{column: "candidate_score", statement: "ALTER TABLE SourceInformationSeedIndex ADD COLUMN candidate_score REAL"},
		{column: "candidate_reason", statement: "ALTER TABLE SourceInformationSeedIndex ADD COLUMN candidate_reason TEXT"},
		{column: "discovery_metadata", statement: "ALTER TABLE SourceInformationSeedIndex ADD COLUMN discovery_metadata TEXT"},
	}

	return applySQLiteColumnMigrations(db, "SourceInformationSeedIndex", migrations)
}

func sqliteColumns(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query("PRAGMA table_info('" + strings.ReplaceAll(table, "'", "''") + "')")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns := map[string]bool{}
	for rows.Next() {
		var (
			cid        int
			name       string
			columnType string
			notNull    int
			dfltValue  sql.NullString
			pk         int
		)
		if err = rows.Scan(&cid, &name, &columnType, &notNull, &dfltValue, &pk); err != nil {
			return nil, err
		}
		columns[name] = true
	}

	return columns, rows.Err()
}

// Close closes the database connection
func (handler *SQLiteHandler) Close() error {
	return handler.db.Close()
}

// Ping checks if the database connection is still alive
func (handler *SQLiteHandler) Ping() error {
	return handler.db.Ping()
}

// ExecuteQuery executes a query and returns the result
func (handler *SQLiteHandler) ExecuteQuery(query string, args ...interface{}) (*sql.Rows, error) {
	return handler.db.Query(query, args...)
}

// Exec executes a commands on the database
func (handler *SQLiteHandler) Exec(query string, args ...interface{}) (sql.Result, error) {
	return handler.db.Exec(query, args...)
}

// ExecContext executes a command on the database with a context
func (handler *SQLiteHandler) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	return handler.db.ExecContext(ctx, query, args...)
}

// DBMS returns the database management system
func (handler *SQLiteHandler) DBMS() string {
	return handler.dbms
}

// Begin starts a transaction
func (handler *SQLiteHandler) Begin() (*sql.Tx, error) {
	return handler.db.Begin()
}

// BeginTx starts a transaction with the given context and options
func (handler *SQLiteHandler) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return handler.db.BeginTx(ctx, opts)
}

// Commit commits a transaction
func (handler *SQLiteHandler) Commit(tx *sql.Tx) error {
	return tx.Commit()
}

// Rollback rolls back a transaction
func (handler *SQLiteHandler) Rollback(tx *sql.Tx) error {
	return tx.Rollback()
}

// QueryRow executes a query that is expected to return at most one row.
// QueryRow always returns a non-nil value. Errors are deferred until
// Row's Scan method is called.
func (handler *SQLiteHandler) QueryRow(query string, args ...interface{}) *sql.Row {
	return handler.db.QueryRow(query, args...)
}

// QueryContext executes a query with the given context and returns the result
func (handler *SQLiteHandler) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	return handler.db.QueryContext(ctx, query, args...)
}

// CheckConnection checks if the database connection is still alive
func (handler *SQLiteHandler) CheckConnection(c cfg.Config) error {
	var err error
	if handler.Ping() != nil {
		err = handler.Connect(c)
	}
	return err
}

// NewListener returns a new Listener for the database
func (handler *SQLiteHandler) NewListener() Listener {
	return nil
}

// ---------------------------------------------------------------
// Database abstraction layer
// ---------------------------------------------------------------

// NewHandler returns a new Handler based on the
// database type specified in the Config struct.
func NewHandler(c cfg.Config) (Handler, error) {
	dbms := strings.ToLower(strings.TrimSpace(c.Database.Type))
	switch dbms {
	case DBPostgresStr:
		handler := &PostgresHandler{}
		handler.dbms = dbms
		return handler, nil
	case DBSQLiteStr:
		handler := &SQLiteHandler{}
		handler.dbms = dbms
		return handler, nil
	// Add cases for MySQL, Snowflake, etc.
	default:
		return nil, fmt.Errorf("unsupported database type: '%s'", c.Database.Type)
	}
}
