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

// This is the main package of the application.
// It's responsible for starting the crawler and kickstart the configuration
// reading and the database connection.
// Actual crawling is performed by the pkg/crawler package.
// The database connection is handled by the pkg/database package.
// The configuration is handled by the pkg/config package.
// Page info extraction is handled by the pkg/scrapper package.
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	crowler "github.com/pzaino/thecrowler/pkg/crawler"
	database "github.com/pzaino/thecrowler/pkg/database"

	_ "github.com/lib/pq"
	"github.com/tebeka/selenium"
)

const (
	sleepTime = 1 * time.Minute // Time to sleep when no URLs are found
)

var (
	config cfg.Config // Configuration "object"
)

// This function is responsible for performing database maintenance
// to keep it lean and fast. Note: it's specific for PostgreSQL.
func performDBMaintenance(db *sql.DB) error {
	maintenanceCommands := []string{
		"VACUUM searchindex",
		"VACUUM keywords",
		"VACUUM keywordindex",
		"REINDEX TABLE searchindex",
		"REINDEX TABLE keywordindex",
	}

	for _, cmd := range maintenanceCommands {
		_, err := db.Exec(cmd)
		if err != nil {
			return fmt.Errorf("error executing maintenance command (%s): %w", cmd, err)
		}
	}

	return nil
}

// This function simply query the database for URLs that need to be crawled
func retrieveAvailableSources(db *sql.DB) ([]database.Source, error) {
	// Update the SQL query to fetch all necessary fields
	query := `SELECT source_id, url, restricted, 0 AS flags FROM Sources WHERE (last_crawled_at IS NULL OR last_crawled_at < NOW() - INTERVAL '3 days') OR (status = 'error' AND last_crawled_at < NOW() - INTERVAL '15 minutes') OR (status = 'completed' AND last_crawled_at < NOW() - INTERVAL '1 week') OR (status = 'pending') ORDER BY last_crawled_at ASC`

	// Execute the query
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}

	// Iterate over the results and store them in a slice
	var sourcesToCrawl []database.Source
	for rows.Next() {
		var src database.Source
		if err := rows.Scan(&src.ID, &src.URL, &src.Restricted, &src.Flags); err != nil {
			log.Println("Error scanning rows:", err)
			continue
		}
		sourcesToCrawl = append(sourcesToCrawl, src)
	}
	rows.Close()

	return sourcesToCrawl, nil
}

// This function is responsible for checking the database for URLs that need to be crawled
// and kickstart the crawling process for each of them
func checkSources(db *sql.DB, wd selenium.WebDriver) {
	if config.DebugLevel > 0 {
		fmt.Println("Checking sources...")
	}
	maintenanceTime := time.Now().Add(time.Duration(config.Crawler.Maintenance) * time.Minute)
	for {
		// Retrieve the sources to crawl
		sourcesToCrawl, err := retrieveAvailableSources(db)
		if err != nil {
			log.Println("Error retrieving sources:", err)
			time.Sleep(sleepTime)
			continue
		}

		// Check if there are sources to crawl
		if len(sourcesToCrawl) == 0 {
			if config.DebugLevel > 0 {
				fmt.Println("No sources to crawl, sleeping...")
			}
			// Perform database maintenance if it's time
			if time.Now().After(maintenanceTime) {
				performDatabaseMaintenance(db)
				maintenanceTime = time.Now().Add(time.Duration(config.Crawler.Maintenance) * time.Minute)
			}
			time.Sleep(sleepTime)
			continue
		}

		// Crawl each source
		crawlSources(db, wd, sourcesToCrawl)
	}
}

func performDatabaseMaintenance(db *sql.DB) {
	log.Printf("Performing database maintenance...")
	if err := performDBMaintenance(db); err != nil {
		log.Printf("Error performing database maintenance: %v", err)
	} else {
		log.Printf("Database maintenance completed successfully.")
	}
}

func crawlSources(db *sql.DB, wd selenium.WebDriver, sources []database.Source) {
	for _, source := range sources {
		log.Println("Crawling URL:", source.URL)
		crowler.CrawlWebsite(db, source, wd)
	}
}

func main() {
	// Reading command line arguments
	configFile := flag.String("config", "./config.yaml", "Path to the configuration file")
	flag.Parse()

	// Reading the configuration file
	var err error
	config, err = cfg.LoadConfig(*configFile)
	if err != nil {
		log.Fatal("Error loading configuration file:", err)
	}

	// Database connection setup
	psqlInfo := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		config.Database.Host, config.Database.Port,
		config.Database.User, config.Database.Password, config.Database.DBName)
	db, err := sql.Open("postgres", psqlInfo)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// Check database connection
	err = db.Ping()
	if err != nil {
		log.Fatal(err)
	}
	log.Println("Successfully connected to the database!")

	crowler.StartCrawler(config)

	sel, err := crowler.StartSelenium()
	if err != nil {
		log.Fatal("Error starting Selenium:", err)
	}
	defer crowler.StopSelenium(sel)

	wd, err := crowler.ConnectSelenium(sel, config)
	if err != nil {
		log.Fatal("Error connecting to Selenium:", err)
	}
	defer crowler.QuitSelenium(wd)

	// Setting up a channel to listen for termination signals
	signals := make(chan os.Signal, 1)
	// Catch SIGINT (Ctrl+C) and SIGTERM signals
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	// Use a select statement to block until a signal is received
	go func() {
		sig := <-signals
		fmt.Printf("Received %v signal, shutting down...\n", sig)

		// Close resources
		closeResources(db, wd) // Assuming db is your DB connection and wd is the WebDriver

		os.Exit(0)
	}()

	// Start the checkSources function in a goroutine
	go checkSources(db, wd)

	// Keep the main function alive
	select {} // Infinite empty select block to keep the main goroutine running
}

func closeResources(db *sql.DB, wd selenium.WebDriver) {
	// Close the database connection
	if db != nil {
		db.Close()
		log.Println("Database connection closed.")
	}

	// Close the WebDriver
	if wd != nil {
		err := wd.Quit()
		if err != nil {
			log.Printf("Error closing WebDriver: %v", err)
			return
		}
		log.Println("WebDriver closed.")
	}
}
