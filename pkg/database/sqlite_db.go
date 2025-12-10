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
	}

	return err
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
