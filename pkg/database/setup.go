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
	"os"

	cmn "github.com/pzaino/thecrowler/pkg/common"

	_ "github.com/lib/pq" // PostgreSQL driver
)

// DBSetup checks if the specified database exists and creates it if it doesn't.
// It takes a pointer to a sql.DB object, the DBMS type (e.g., PostgreSQL), and the database name as parameters.
// If the database doesn't exist, it executes an SQL file to create it.
func DBSetup(db *sql.DB, dbms, dbname string) {
	// Check if the PostgreSQL database exists
	if !dbExists(db, dbms, dbname) {
		// If not, execute the SQL file to create it
		executeSQLFile(db, dbms, dbname)
	}
}

// postgresDBExists checks if a PostgreSQL database with the given name exists.
// It returns true if the database exists, false otherwise.
func postgresDBExists(db *sql.DB, dbName string) bool {
	// Check if the database exists
	var exists bool
	err := db.QueryRow(`SELECT EXISTS(SELECT datname FROM pg_catalog.pg_database WHERE datname = $1)`, dbName).Scan(&exists)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "checking if the database exists: %v", err)
		return false
	}

	return exists
}

// dbExists checks if the specified database exists.
// It takes a *sql.DB object, the database management system (dbms), and the database name (dbName) as parameters.
// It returns a boolean value indicating whether the database exists or not.
func dbExists(db *sql.DB, dbms, dbName string) bool {
	// Check if the database exists
	var exists bool
	if dbms == "postgres" {
		exists = postgresDBExists(db, dbName)
	} else {
		cmn.DebugMsg(cmn.DbgLvlError, "Unsupported database management system: %s", dbms)
		return false
	}

	return exists
}

// executeSQLFile executes SQL commands from a file on the specified database.
// It takes a *sql.DB, dbms string, and dbName string as parameters.
// The dbms parameter specifies the type of database management system (e.g., "postgres").
// The dbName parameter specifies the name of the database.
// The function reads the SQL commands from the corresponding SQL file based on the dbms and dbName parameters.
// It then executes the SQL commands on the specified database using the db.Exec method.
// If any error occurs during file reading or SQL execution, the function logs a fatal error.
// After successful execution, it logs a message indicating that the database was created successfully.
func executeSQLFile(db *sql.DB, dbms, dbName string) {
	// Get the path to the right SQL file
	filePath := ""
	if dbms == "postgres" {
		filePath = fmt.Sprintf("./postgresql-%s.pgsql", dbName)
	} else {
		cmn.DebugMsg(cmn.DbgLvlError, "Unsupported database management system: %s", dbms)
		return
	}

	// Read the SQL file
	content, err := os.ReadFile(filePath)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "reading the SQL file: %v", err)
		return
	}

	// Execute the SQL commands
	_, err = db.Exec(string(content))
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "executing the SQL file: %v", err)
		return
	}

	cmn.DebugMsg(cmn.DbgLvlInfo, "Database created successfully.")
}
