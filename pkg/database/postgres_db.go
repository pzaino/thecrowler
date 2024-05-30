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

	var err error
	for {
		// Connect to the database
		handler.db, err = sql.Open("postgres", connectionString)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "connecting to the database: %v", err)
			time.Sleep(time.Duration(c.Database.RetryTime) * time.Second)
		} else {
			for {
				// Check database connection
				err = handler.db.Ping()
				if err != nil {
					cmn.DebugMsg(cmn.DbgLvlError, "pinging the database: %v", err)
					time.Sleep(time.Duration(c.Database.PingTime) * time.Second)
				} else {
					break
				}
			}
			cmn.DebugMsg(cmn.DbgLvlInfo, "Successfully connected to the database!")
			break
		}
	}

	// Set the database management system
	optFor := strings.ToLower(strings.TrimSpace(c.Database.OptimizeFor))
	if optFor == "" || optFor == "none" {
		handler.db.SetConnMaxLifetime(time.Minute * 5)
		handler.db.SetMaxOpenConns(25)
		handler.db.SetMaxIdleConns(25)
	}
	if optFor == "write" {
		handler.ConfigForWrite()
		handler.db.SetConnMaxLifetime(time.Minute * 5)
		handler.db.SetMaxOpenConns(100)
		handler.db.SetMaxIdleConns(100)
	}
	if optFor == "query" {
		handler.ConfigForQuery()
		handler.db.SetConnMaxLifetime(time.Minute * 5)
		handler.db.SetMaxOpenConns(100)
		handler.db.SetMaxIdleConns(100)
	}

	return err
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
