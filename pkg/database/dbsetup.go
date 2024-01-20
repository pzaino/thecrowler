package database

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/lib/pq" // PostgreSQL driver
)

func DBSetup(db *sql.DB, dbms, dbname string) {
	// Check if the PostgreSQL database exists
	if !dbExists(db, dbms, dbname) {
		// If not, execute the SQL file to create it
		executeSQLFile(db, dbms, dbname)
	}
}

func postgresDBExists(db *sql.DB, dbName string) bool {
	// Check if the database exists
	var exists bool
	err := db.QueryRow(`SELECT EXISTS(SELECT datname FROM pg_catalog.pg_database WHERE datname = $1)`, dbName).Scan(&exists)
	if err != nil {
		log.Fatalf("Error checking if the database exists: %v", err)
	}

	return exists
}

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

func executeSQLFile(db *sql.DB, dbms, dbName string) {
	// Get the path to the right SQL file
	filePath := ""
	if dbms == "postgres" {
		filePath = fmt.Sprintf("pkg/database/psql-%s.sql", dbName)
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
