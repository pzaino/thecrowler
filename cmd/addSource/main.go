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
	"flag"
	"fmt"
	"log"
	"strings"

	cfg "github.com/pzaino/thecrowler/pkg/config"

	_ "github.com/lib/pq"
)

var (
	config cfg.Config
)

func insertWebsite(db *sql.DB, url string) error {
	// SQL statement to insert a new website
	stmt := `INSERT INTO Sources (url, last_crawled_at, status) VALUES ($1, NULL, 'pending')`

	// Normalize the URL
	url = normalizeURL(url)

	// Execute the SQL statement
	_, err := db.Exec(stmt, url)
	if err != nil {
		return err
	}

	fmt.Println("Website inserted successfully:", url)
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
	flag.Parse()

	// Read the configuration file
	var err error
	config, err = cfg.LoadConfig(*configFile)
	if err != nil {
		log.Fatal(err)
	}

	// Check if the URL is provided
	if *url == "" {
		log.Fatal("Please provide a URL of the website to add.")
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

	// Insert the website
	if err := insertWebsite(db, *url); err != nil {
		log.Fatalf("Error inserting website: %v", err)
	}
}
