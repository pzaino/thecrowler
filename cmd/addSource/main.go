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

// Package main (addSite) is a command line that allows to add sources to
// crawl to TheCROWler DB.
package main

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"

	_ "github.com/lib/pq"
)

var (
	config      cfg.Config
	forceInsert bool
)

func insertWebsite(db *sql.DB, source *cdb.Source) error {
	// SQL statement to insert a new website
	stmt := `INSERT INTO Sources (
				url,
				last_crawled_at,
				status,
				category_id,
				usr_id,
				restricted,
				flags,
				config
			) VALUES (
				$1,
				NULL,
				'pending',
				$2,
				$3,
				$4,
				$5,
				$6
			) RETURNING source_id;
		`

	// Normalize the URL
	source.URL = normalizeURL(source.URL)

	// Execute the SQL statement and get the ID of the inserted website
	results, err := db.Query(stmt, source.URL, source.CategoryID, source.UsrID, source.Restricted, source.Flags, source.Config)
	if err != nil {
		return err
	}

	// Get the ID of the inserted website
	var id uint64
	for results.Next() {
		err = results.Scan(&id)
		if err != nil {
			return err
		}
	}

	fmt.Println("Website inserted successfully! Assigned Source ID: ", id)
	return nil
}

// normalizeURL normalizes a URL by trimming trailing slashes and converting it to lowercase.
func normalizeURL(url string) string {
	// Trim spaces
	url = strings.TrimSpace(url)
	// Trim trailing slash
	url = strings.TrimRight(url, "/")
	// Convert to lowercase
	url = strings.ToLower(url)
	return url
}

func main() {
	configFile := flag.String("config", "config.yaml", "Path to the configuration file")
	url := flag.String("url", "", "URL of the website to add")
	bulk := flag.String("bulk", "", "Add multiple websites from a CSV file")
	catID := flag.Uint64("catID", 0, "Category ID")
	usrID := flag.Uint64("usrID", 0, "User ID")
	restricted := flag.Uint("restricted", 1, "Restricted crawling")
	flags := flag.Uint("flags", 0, "Flags")
	sourceConfig := flag.String("srccfg", "", "Source configuration file")
	force := flag.Bool("force", false, "Force the insertion of the website even if the config file is not found or it is invalid")
	flag.Parse()

	forceInsert = *force

	// Read the configuration file
	var err error
	config, err = cfg.LoadConfig(*configFile)
	if err != nil {
		log.Fatal(err)
	}

	// Check if the URL is provided
	if *url == "" && *bulk == "" {
		log.Fatal("Please provide a URL of the website to add or a file name for a bulk add.")
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

	// Check if the URL is provided
	if *url != "" {
		// Generate the Source
		source := cdb.Source{
			URL:        *url,
			CategoryID: *catID,
			UsrID:      *usrID,
			Restricted: *restricted,
			Flags:      *flags,
			Status:     0,
		}
		// Check if the source configuration file is provided
		if *sourceConfig != "" {
			source.Config, err = getSourceConfig(*sourceConfig)
			if err != nil {
				log.Fatalf("Error reading source configuration file: %v", err)
			}
		}

		// Insert the website
		if err := insertWebsite(db, &source); err != nil {
			log.Fatalf("Error inserting website: %v", err)
		}
	}

	// Check if the bulk file is provided
	if *bulk != "" {
		// Read the file and insert the websites
		if err := insertWebsitesFromFile(db, *bulk); err != nil {
			log.Fatalf("Error inserting websites from file: %v", err)
		}
	}
}

// insertWebsitesFromFile inserts websites from a CSV file into the database.
// CSV format: URL, Category ID, UsrID, Restricted, Flags, ConfigFileName
func insertWebsitesFromFile(db *sql.DB, filename string) error {
	// Read the csv file
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	// Create a new CSV reader
	r := csv.NewReader(file)

	// Read the CSV records
	for {
		// Read the record from the CSV file
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		// Check if record is empty
		if len(record) == 0 {
			continue
		}

		// Create a new SourceRecord
		var categoryID uint64
		if len(record) > 1 {
			categoryID, err = strconv.ParseUint(strings.TrimSpace(record[1]), 10, 64)
			if err != nil {
				return err
			}
		}

		var usrID uint64
		if len(record) > 2 {
			usrID, err = strconv.ParseUint(strings.TrimSpace(record[2]), 10, 64)
			if err != nil {
				return err
			}
		}

		restricted := uint(1)
		if len(record) > 3 {
			restricted64, err := strconv.ParseUint(strings.TrimSpace(record[3]), 10, 32)
			if err != nil {
				return err
			}
			restricted = uint(restricted64)
		}

		flags := uint(0)
		if len(record) > 4 {
			flags64, err := strconv.ParseUint(strings.TrimSpace(record[4]), 10, 32)
			if err != nil {
				return err
			}
			flags = uint(flags64)
		}

		sourceRecord := cdb.Source{
			URL:        record[0],
			CategoryID: categoryID,
			UsrID:      usrID,
			Restricted: restricted,
			Flags:      flags,
			Status:     0,
		}

		// Check if the row has a config file name
		if len(record) > 4 {
			sourceRecord.Config, err = getSourceConfig(record[5])
			if err != nil {
				if !forceInsert {
					fmt.Printf("Error reading source configuration file for %s: %v\n", sourceRecord.URL, err)
					sourceRecord.Config = nil
				} else {
					return err
				}
			}
		}

		// Insert the website
		if err := insertWebsite(db, &sourceRecord); err != nil {
			if !forceInsert {
				fmt.Printf("Error inserting website %s: %v\n", sourceRecord.URL, err)
				continue
			} else {
				return err
			}
		}
	}

	return nil
}

func getSourceConfig(configFile string) (*json.RawMessage, error) {
	sourceConfig := cfg.SourceConfig{}
	sourceConfigRaw := json.RawMessage{}
	if strings.TrimSpace(configFile) != "" {
		// Read the config file
		configFile, err := os.ReadFile(configFile)
		if err != nil {
			return &sourceConfigRaw, err
		}

		// Unmarshal the config file
		if err := json.Unmarshal(configFile, &sourceConfig); err != nil {
			return &sourceConfigRaw, err
		}

		// Cast the config to a JSON.RawMessage
		sourceConfigRaw = json.RawMessage(configFile)

	}
	return &sourceConfigRaw, nil
}
