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

	"github.com/lib/pq"
)

var (
	config cfg.Config
)

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

	// Delete from Sources
	err = deleteFromSources(tx, siteURL)
	if err != nil {
		return err
	}

	// Find and delete associated entries in SearchIndex, MetaTags, and KeywordIndex
	err = deleteAssociatedEntries(tx, siteURL)
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

func deleteFromSources(tx *sql.Tx, siteURL string) error {
	_, err := tx.Exec(`DELETE FROM Sources WHERE url = $1`, siteURL)
	if err != nil {
		rollbackTransaction(tx)
		return err
	}
	return nil
}

func deleteAssociatedEntries(tx *sql.Tx, siteURL string) error {
	indexIDs, err := getAssociatedIndexIDs(tx, siteURL)
	if err != nil {
		rollbackTransaction(tx)
		return err
	}

	err = deleteFromSearchIndex(tx, siteURL)
	if err != nil {
		rollbackTransaction(tx)
		return err
	}

	err = deleteFromMetaTags(tx, indexIDs)
	if err != nil {
		rollbackTransaction(tx)
		return err
	}

	err = deleteFromKeywordIndex(tx, indexIDs, siteURL)
	if err != nil {
		rollbackTransaction(tx)
		return err
	}

	return nil
}

func getAssociatedIndexIDs(tx *sql.Tx, siteURL string) ([]int, error) {
	var indexIDs []int
	rows, err := tx.Query(`SELECT index_id FROM SearchIndex WHERE source_id = (SELECT source_id FROM Sources WHERE url = $1)`, siteURL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var indexID int
		if err := rows.Scan(&indexID); err != nil {
			return nil, err
		}
		indexIDs = append(indexIDs, indexID)
	}

	return indexIDs, nil
}

func deleteFromSearchIndex(tx *sql.Tx, siteURL string) error {
	_, err := tx.Exec(`DELETE FROM SearchIndex WHERE source_id = (SELECT source_id FROM Sources WHERE url = $1)`, siteURL)
	if err != nil {
		return err
	}
	return nil
}

func deleteFromMetaTags(tx *sql.Tx, indexIDs []int) error {
	for _, id := range indexIDs {
		_, err := tx.Exec(`DELETE FROM MetaTags WHERE index_id = $1`, id)
		if err != nil {
			return err
		}
	}
	return nil
}

func deleteFromKeywordIndex(tx *sql.Tx, indexIDs []int, siteURL string) error {
	_, err := tx.Exec(`
		DELETE FROM KeywordIndex
		WHERE index_id = ANY($1)
		AND NOT EXISTS (
			SELECT 1 FROM SearchIndex
			WHERE index_id = KeywordIndex.index_id
			AND source_id != (SELECT source_id FROM Sources WHERE url = $2)
		)`, pq.Array(indexIDs), siteURL)
	if err != nil {
		return err
	}
	return nil
}

func rollbackTransaction(tx *sql.Tx) {
	err := tx.Rollback()
	if err != nil {
		log.Printf("Error rolling back transaction: %v\n", err)
	}
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
	db, err := sql.Open("postgres", psqlInfo)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// Remove the website
	err = removeSite(db, *siteURL)
	if err != nil {
		log.Fatal(err)
	}
}
