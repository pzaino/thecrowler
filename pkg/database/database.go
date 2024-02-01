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
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"

	_ "github.com/lib/pq" // PostgreSQL driver
)

// ---------------------------------------------------------------
// Postgres handlers
// ---------------------------------------------------------------

// Connect connects to the database
func (handler *PostgresHandler) Connect(c cfg.Config) error {
	// Construct the connection string from the Config struct
	var dbPort int
	if c.Database.Port == 0 {
		dbPort = 5432
	} else {
		dbPort = c.Database.Port
	}
	var dbHost string
	if c.Database.Host == "" {
		dbHost = "localhost"
	} else {
		dbHost = c.Database.Host
	}
	var dbUser string
	if c.Database.User == "" {
		dbUser = "crowler"
	} else {
		dbUser = c.Database.User
	}
	dbPassword := c.Database.Password
	var dbName string
	if c.Database.DBName == "" {
		dbName = "SitesIndex"
	} else {
		dbName = c.Database.DBName
	}
	var dbSSLMode string
	if c.Database.SSLMode == "" {
		dbSSLMode = "disable"
	} else {
		dbSSLMode = c.Database.SSLMode
	}
	connectionString := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		dbHost, dbPort, dbUser, dbPassword, dbName, dbSSLMode)

	var err error
	for {
		handler.db, err = sql.Open("postgres", connectionString)
		if err != nil {
			log.Println(err)
			time.Sleep(time.Duration(c.Database.RetryTime) * time.Second)
		} else {
			for {
				// Check database connection
				err = handler.db.Ping()
				if err != nil {
					log.Println(err)
					time.Sleep(time.Duration(c.Database.PingTime) * time.Second)
				} else {
					break
				}
			}
			log.Println("Successfully connected to the database!")
			break
		}
	}

	return err
}

// Close closes the database connection
func (handler *PostgresHandler) Close() error {
	return handler.db.Close()
}

// Ping checks if the database connection is still alive
func (handler *PostgresHandler) Ping() error {
	return handler.db.Ping()
}

// ExecuteQuery executes a query and returns the result
func (handler *PostgresHandler) ExecuteQuery(query string, args ...interface{}) (*sql.Rows, error) {
	return handler.db.Query(query, args...)
}

// Exec executes a commands on the database
func (handler *PostgresHandler) Exec(query string, args ...interface{}) (sql.Result, error) {
	return handler.db.Exec(query, args...)
}

// DBMS returns the database management system
func (handler *PostgresHandler) DBMS() string {
	return handler.dbms
}

// Begin starts a transaction
func (handler *PostgresHandler) Begin() (*sql.Tx, error) {
	return handler.db.Begin()
}

// Commit commits a transaction
func (handler *PostgresHandler) Commit(tx *sql.Tx) error {
	return tx.Commit()
}

// Rollback rolls back a transaction
func (handler *PostgresHandler) Rollback(tx *sql.Tx) error {
	return tx.Rollback()
}

// QueryRow executes a query that is expected to return at most one row.
// QueryRow always returns a non-nil value. Errors are deferred until
// Row's Scan method is called.
func (handler *PostgresHandler) QueryRow(query string, args ...interface{}) *sql.Row {
	return handler.db.QueryRow(query, args...)
}

// CheckConnection checks if the database connection is still alive
func (handler *PostgresHandler) CheckConnection(c cfg.Config) error {
	var err error
	if handler.Ping() != nil {
		err = handler.Connect(c)
	}
	return err
}

// ---------------------------------------------------------------
// SQLite handlers
// ---------------------------------------------------------------

// Connect connects to an SQLite database
func (handler *SQLiteHandler) Connect(c cfg.Config) error {
	// Construct the connection string from the Config struct
	connectionString := fmt.Sprintf("file:%s?cache=shared&mode=rwc", c.Database.DBName)

	var err error
	handler.db, err = sql.Open("sqlite3", connectionString)

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

// DBMS returns the database management system
func (handler *SQLiteHandler) DBMS() string {
	return handler.dbms
}

// Begin starts a transaction
func (handler *SQLiteHandler) Begin() (*sql.Tx, error) {
	return handler.db.Begin()
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

// ---------------------------------------------------------------
// Database abstraction layer
// ---------------------------------------------------------------

// NewHandler returns a new Handler based on the
// database type specified in the Config struct.
func NewHandler(c cfg.Config) (Handler, error) {
	dbms := strings.ToLower(strings.TrimSpace(c.Database.Type))
	switch dbms {
	case "postgres":
		handler := &PostgresHandler{}
		handler.dbms = dbms
		return handler, nil
	case "sqlite":
		handler := &SQLiteHandler{}
		handler.dbms = dbms
		return handler, nil
	// Add cases for MySQL, Snowflake, etc.
	default:
		return nil, fmt.Errorf("unsupported database type: '%s'", c.Database.Type)
	}
}
