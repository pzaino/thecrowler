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

package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"

	cfg "TheCrow/pkg/config"

	"github.com/lib/pq"
)

var (
	config cfg.Config
)

func removeSite(db *sql.DB, siteURL string) error {
	// Start a transaction
	tx, err := db.Begin()
	if err != nil {
		return err
	}

	// Delete from Sources
	_, err = tx.Exec(`DELETE FROM Sources WHERE url = $1`, siteURL)
	if err != nil {
		tx.Rollback()
		return err
	}

	// Find and delete associated entries in SearchIndex, MetaTags, and KeywordIndex
	// Delete associated entries in SearchIndex and get their IDs
	var indexIDs []int
	rows, err := tx.Query(`SELECT index_id FROM SearchIndex WHERE source_id = (SELECT source_id FROM Sources WHERE url = $1)`, siteURL)
	if err != nil {
		tx.Rollback()
		return err
	}
	for rows.Next() {
		var indexID int
		if err := rows.Scan(&indexID); err != nil {
			rows.Close()
			tx.Rollback()
			return err
		}
		indexIDs = append(indexIDs, indexID)
	}
	rows.Close()

	_, err = tx.Exec(`DELETE FROM SearchIndex WHERE source_id = (SELECT source_id FROM Sources WHERE url = $1)`, siteURL)
	if err != nil {
		tx.Rollback()
		return err
	}

	// Delete associated entries in MetaTags
	for _, id := range indexIDs {
		_, err = tx.Exec(`DELETE FROM MetaTags WHERE index_id = $1`, id)
		if err != nil {
			tx.Rollback()
			return err
		}
	}

	// Delete from KeywordIndex only if the keyword is not associated with other sources
	_, err = tx.Exec(`
		DELETE FROM KeywordIndex 
		WHERE index_id = ANY($1) 
		AND NOT EXISTS (
			SELECT 1 FROM SearchIndex 
			WHERE index_id = KeywordIndex.index_id 
			AND source_id != (SELECT source_id FROM Sources WHERE url = $2)
		)`, pq.Array(indexIDs), siteURL)
	if err != nil {
		tx.Rollback()
		return err
	}

	// Commit the transaction
	if err := tx.Commit(); err != nil {
		tx.Rollback()
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
		os.Exit(1)
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
