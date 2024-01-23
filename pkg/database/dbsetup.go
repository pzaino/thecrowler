package database

import (
	"database/sql"
	"fmt"
	"log"
	"os"

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
		log.Fatalf("Error checking if the database exists: %v", err)
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
		log.Fatalf("Unsupported database management system: %s", dbms)
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
		filePath = fmt.Sprintf("pkg/database/psql-%s.pgsql", dbName)
	} else {
		log.Fatalf("Unsupported database management system: %s", dbms)
	}

	// Read the SQL file
	content, err := os.ReadFile(filePath)
	if err != nil {
		log.Fatalf("Error reading the SQL file: %v", err)
	}

	// Execute the SQL commands
	_, err = db.Exec(string(content))
	if err != nil {
		log.Fatalf("Error executing the SQL file: %v", err)
	}

	log.Println("Database created successfully.")
}
