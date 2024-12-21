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

// Package main (removeSite) is a command line that allows to remove
// sources from crawl to TheCROWler DB.
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"

	_ "github.com/lib/pq"
)

var (
	config cfg.Config
)

// ConsoleResponse represents the structure of the response
// returned by the console API (addSource/removeSOurce etc.).
type ConsoleResponse struct {
	Message string `json:"message"`
}

func removeSource(tx *sql.Tx, sourceURL string) (ConsoleResponse, error) {
	var results ConsoleResponse
	results.Message = "Failed to remove the source"

	// First, get the source_id for the given URL to ensure it exists and to use in cascading deletes if necessary
	var sourceID int64
	err := tx.QueryRow("SELECT source_id FROM Sources WHERE url = $1", sourceURL).Scan(&sourceID)
	if err != nil {
		return results, err
	}

	// Proceed with deleting the source using the obtained source_id
	_, err = tx.Exec("DELETE FROM Sources WHERE source_id = $1", sourceID)
	if err != nil {
		err2 := tx.Rollback() // Rollback in case of error
		if err2 != nil {
			return ConsoleResponse{Message: "Failed to delete source"}, err2
		}
		return ConsoleResponse{Message: "Failed to delete source and related data"}, err
	}
	_, err = tx.Exec("SELECT cleanup_orphaned_httpinfo();")
	if err != nil {
		err2 := tx.Rollback() // Rollback in case of error
		if err2 != nil {
			return ConsoleResponse{Message: "Failed to cleanup orphaned httpinfo"}, err2
		}
		return ConsoleResponse{Message: "Failed to cleanup orphaned httpinfo"}, err
	}
	_, err = tx.Exec("SELECT cleanup_orphaned_netinfo();")
	if err != nil {
		err2 := tx.Rollback() // Rollback in case of error
		if err2 != nil {
			return ConsoleResponse{Message: "Failed to cleanup orphaned netinfo"}, err2
		}
		return ConsoleResponse{Message: "Failed to cleanup orphaned netinfo"}, err
	}

	results.Message = "Source and related data removed successfully"
	return results, nil
}

// removeSite removes a site from the database along with its associated entries in other tables.
// It takes a *sql.DB as the database connection and a siteURL string as the URL of the site to be removed.
// It starts a transaction, deletes the site from the Sources table, and then deletes the associated entries
// in the SearchIndex, MetaTags, and KeywordIndex tables. Finally, it commits the transaction.
// If any error occurs during the process, the transaction is rolled back and the error is returned.
func removeSite(db *sql.DB, siteURL string) error {
	// Start a transaction
	tx, err := db.Begin()
	if err != nil {
		return err
	}

	_, err = removeSource(tx, siteURL)
	if err != nil {
		return err
	}

	// Commit the transaction
	err = tx.Commit()
	if err != nil {
		return err
	}

	return nil
}

func main() {
	configFile := flag.String("config", "config.yaml", "Path to the configuration file")
	siteURL := flag.String("url", "", "URL of the website to remove")
	flag.Parse()

	// Read the configuration file
	var err error
	config, err = cfg.LoadConfig(*configFile)
	if err != nil {
		log.Fatal(err)
	}

	// Check if the URL is provided
	if *siteURL == "" {
		log.Fatal("Please provide a URL of the website to remove.")
	}

	// Database connection setup (replace with your actual database configuration)
	psqlInfo := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		config.Database.Host, config.Database.Port,
		config.Database.User, config.Database.Password, config.Database.DBName)
	db, err := sql.Open(cdb.DBPostgresStr, psqlInfo)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close() //nolint:errcheck // We can't check the error in a defer statement

	// Remove the website
	err = removeSite(db, *siteURL)
	if err != nil {
		log.Fatal(err)
	}
}
