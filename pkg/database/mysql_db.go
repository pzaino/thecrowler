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

	_ "github.com/go-sql-driver/mysql" // MySQL driver
)

// ---------------------------------------------------------------
// MySQL handlers
// ---------------------------------------------------------------

// MySQLHandler struct to hold the DB connection
type MySQLHandler struct {
	db   *sql.DB
	dbms string
}

// Connect connects to the database
func (handler *MySQLHandler) Connect(c cfg.Config) error {
	connectionString := buildMySQLConnectionString(c)

	var err error
	for {
		// Connect to the database
		handler.db, err = sql.Open("mysql", connectionString)
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

func buildMySQLConnectionString(c cfg.Config) string {
	var dbPort int
	if c.Database.Port == 0 {
		dbPort = 3306
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

	connectionString := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s",
		dbUser, dbPassword, dbHost, dbPort, dbName)

	return connectionString
}

// Close closes the database connection
func (handler *MySQLHandler) Close() error {
	return handler.db.Close()
}

// Ping checks if the database connection is still alive
func (handler *MySQLHandler) Ping() error {
	return handler.db.Ping()
}

// ExecuteQuery executes a query and returns the result
func (handler *MySQLHandler) ExecuteQuery(query string, args ...interface{}) (*sql.Rows, error) {
	return handler.db.Query(query, args...)
}

// Exec executes a commands on the database
func (handler *MySQLHandler) Exec(query string, args ...interface{}) (sql.Result, error) {
	return handler.db.Exec(query, args...)
}

// DBMS returns the database management system
func (handler *MySQLHandler) DBMS() string {
	return handler.dbms
}

// Begin starts a transaction
func (handler *MySQLHandler) Begin() (*sql.Tx, error) {
	return handler.db.Begin()
}

// Commit commits a transaction
func (handler *MySQLHandler) Commit(tx *sql.Tx) error {
	return tx.Commit()
}

// Rollback rolls back a transaction
func (handler *MySQLHandler) Rollback(tx *sql.Tx) error {
	return tx.Rollback()
}

// QueryRow executes a query that is expected to return at most one row.
// QueryRow always returns a non-nil value. Errors are deferred until
// Row's Scan method is called.
func (handler *MySQLHandler) QueryRow(query string, args ...interface{}) *sql.Row {
	return handler.db.QueryRow(query, args...)
}

// CheckConnection checks if the database connection is still alive
func (handler *MySQLHandler) CheckConnection(c cfg.Config) error {
	var err error
	if handler.Ping() != nil {
		err = handler.Connect(c)
	}
	return err
}

// ---------------------------------------------------------------
// Server Configuration

// Note: MySQL doesn't support ALTER SYSTEM for dynamic reconfiguration like PostgreSQL.
// You might need to adjust these configurations at the server level or use a tool like `my.cnf`.

func (handler *MySQLHandler) ConfigForWrite() {
	params := map[string]string{
		"max_connections":                "1000",
		"innodb_buffer_pool_size":        "16G",
		"innodb_log_file_size":           "4G",
		"innodb_log_buffer_size":         "16M",
		"innodb_flush_log_at_trx_commit": "2",
		"query_cache_size":               "0",
		"query_cache_type":               "0",
		"tmp_table_size":                 "64M",
		"max_heap_table_size":            "64M",
		"innodb_file_per_table":          "ON",
		"innodb_flush_method":            "O_DIRECT",
	}

	for key, value := range params {
		_, err := handler.db.Exec(fmt.Sprintf("SET GLOBAL %s = %s", key, value))
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "setting %s: %v", key, err)
		} else {
			cmn.DebugMsg(cmn.DbgLvlInfo, "Successfully set %s to %s", key, value)
		}
	}
}

func (handler *MySQLHandler) ConfigForQuery() {
	params := map[string]string{
		"max_connections":                "100",
		"innodb_buffer_pool_size":        "4G",
		"innodb_log_file_size":           "1G",
		"innodb_log_buffer_size":         "8M",
		"innodb_flush_log_at_trx_commit": "1",
		"query_cache_size":               "0",
		"query_cache_type":               "0",
		"tmp_table_size":                 "32M",
		"max_heap_table_size":            "32M",
		"innodb_file_per_table":          "ON",
		"innodb_flush_method":            "O_DIRECT",
	}

	for key, value := range params {
		_, err := handler.db.Exec(fmt.Sprintf("SET GLOBAL %s = %s", key, value))
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "setting %s: %v", key, err)
		} else {
			cmn.DebugMsg(cmn.DbgLvlInfo, "Successfully set %s to %s", key, value)
		}
	}
}
