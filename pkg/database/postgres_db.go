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
	"strings"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"

	_ "github.com/lib/pq" // PostgreSQL driver
)

// ---------------------------------------------------------------
// Postgres handlers
// ---------------------------------------------------------------

// PostgresHandler is the implementation of the DatabaseHandler interface
type PostgresHandler struct {
	db   *sql.DB
	dbms string
}

// Connect connects to the database
func (handler *PostgresHandler) Connect(c cfg.Config) error {
	connectionString := buildConnectionString(c)

	// Set a limit for retries
	retryInterval := time.Duration(c.Database.RetryTime) * time.Second
	pingInterval := time.Duration(c.Database.PingTime) * time.Second

	var err error
	for {
		// Try to open the database connection
		handler.db, err = sql.Open("postgres", connectionString)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Error opening database connection: %v", err)
			time.Sleep(retryInterval)
			continue // Retry opening the connection
		}
		break
	}

	// Retry pinging the database to establish a live connection
	pingRetries := 15 // Limit ping retries
	for pingRetries > 0 {
		err = handler.db.Ping()
		if err == nil {
			cmn.DebugMsg(cmn.DbgLvlDebug, "Successfully connected to the database!")
			break // Ping successful, stop retrying
		}
		time.Sleep(pingInterval)
		pingRetries--
	}

	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Ping retries exhausted. We are connected, but the DB is not responding: %v", err)
		time.Sleep(retryInterval) // Give it one more wait before proceeding
	}

	// Set connection parameters (open and idle connections)
	mxConns, mxIdleConns := determineConnectionLimits(c)
	handler.db.SetConnMaxLifetime(time.Minute * 5)
	handler.db.SetMaxOpenConns(mxConns)
	handler.db.SetMaxIdleConns(mxIdleConns)

	return err
}

// determineConnectionLimits calculates connection limits based on config
func determineConnectionLimits(c cfg.Config) (int, int) {
	mxConns, mxIdleConns := 25, 25

	optFor := strings.ToLower(strings.TrimSpace(c.Database.OptimizeFor))
	switch optFor {
	case "write", "query":
		mxConns = 100
		mxIdleConns = 100
	}

	// Use config-defined max connections if smaller
	if c.Database.MaxConns > 0 && c.Database.MaxConns < mxConns {
		mxConns = c.Database.MaxConns
	}
	if c.Database.MaxIdleConns > 0 && c.Database.MaxIdleConns < mxIdleConns {
		mxIdleConns = c.Database.MaxIdleConns
	}

	return mxConns, mxIdleConns
}

func buildConnectionString(c cfg.Config) string {
	var dbPort int
	if c.Database.Port == 0 {
		dbPort = 5432
	} else {
		dbPort = c.Database.Port
	}
	var dbHost string
	if strings.TrimSpace(c.Database.Host) == "" {
		dbHost = "localhost"
	} else {
		dbHost = strings.TrimSpace(c.Database.Host)
	}
	var dbUser string
	if strings.TrimSpace(c.Database.User) == "" {
		dbUser = "crowler"
	} else {
		dbUser = strings.TrimSpace(c.Database.User)
	}
	dbPassword := c.Database.Password
	var dbName string
	if strings.TrimSpace(c.Database.DBName) == "" {
		dbName = "SitesIndex"
	} else {
		dbName = strings.TrimSpace(c.Database.DBName)
	}
	var dbSSLMode string
	if strings.TrimSpace(c.Database.SSLMode) == "" {
		dbSSLMode = "disable"
	} else {
		sslmode := strings.ToLower(strings.TrimSpace(c.Database.SSLMode))
		// Validate the SSL mode
		if sslmode != "disable" && sslmode != "require" && sslmode != "verify-ca" && sslmode != "verify-full" {
			sslmode = "disable"
		}
		dbSSLMode = sslmode
	}
	connectionString := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		dbHost, dbPort, dbUser, dbPassword, dbName, dbSSLMode)

	return connectionString
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
// Server Configuration

func (handler *PostgresHandler) ConfigForWrite() {
	params := map[string]string{
		"max_connections":                 "1000",
		"shared_buffers":                  "16GB",
		"effective_cache_size":            "48GB",
		"maintenance_work_mem":            "2GB",
		"checkpoint_completion_target":    "0.9",
		"wal_buffers":                     "16MB",
		"default_statistics_target":       "100",
		"random_page_cost":                "1.1",
		"effective_io_concurrency":        "200",
		"work_mem":                        "16MB",
		"min_wal_size":                    "1GB",
		"max_wal_size":                    "4GB",
		"max_worker_processes":            "8",
		"max_parallel_workers_per_gather": "4",
		"max_parallel_workers":            "8",
	}

	handler.ConfigForOptimize(params)
}

func (handler *PostgresHandler) ConfigForQuery() {
	params := map[string]string{
		"max_connections":                 "100",
		"shared_buffers":                  "4GB",
		"effective_cache_size":            "12GB",
		"maintenance_work_mem":            "1GB",
		"checkpoint_completion_target":    "0.5",
		"wal_buffers":                     "8MB",
		"default_statistics_target":       "100",
		"random_page_cost":                "1.1",
		"effective_io_concurrency":        "200",
		"work_mem":                        "4MB",
		"min_wal_size":                    "1GB",
		"max_wal_size":                    "2GB",
		"max_worker_processes":            "4",
		"max_parallel_workers_per_gather": "2",
		"max_parallel_workers":            "4",
	}

	handler.ConfigForOptimize(params)
}

func (handler *PostgresHandler) ConfigForOptimize(params map[string]string) {
	for key, value := range params {
		_, err := handler.db.Exec(fmt.Sprintf("ALTER SYSTEM SET %s TO '%s'", key, value))
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "setting %s: %v", key, err)
		} else {
			cmn.DebugMsg(cmn.DbgLvlInfo, "Successfully set %s to %s", key, value)
		}
	}

	// Reload PostgreSQL configuration
	_, err := handler.db.Exec("SELECT pg_reload_conf()")
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "reloading configuration: %v", err)
	}

	cmn.DebugMsg(cmn.DbgLvlInfo, "Configuration reloaded successfully")
}
