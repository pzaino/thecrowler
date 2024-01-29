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

// Package main (TheCROWler) is the application.
// It's responsible for starting the crawler and kickstart the configuration
// reading and the database connection.
// Actual crawling is performed by the pkg/crawler package.
// The database connection is handled by the pkg/database package.
// The configuration is handled by the pkg/config package.
// Page info extraction is handled by the pkg/scrapper package.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	crowler "github.com/pzaino/thecrowler/pkg/crawler"
	cdb "github.com/pzaino/thecrowler/pkg/database"

	_ "github.com/lib/pq"
)

const (
	sleepTime = 1 * time.Minute // Time to sleep when no URLs are found
)

var (
	config cfg.Config // Configuration "object"
)

// This function is responsible for performing database maintenance
// to keep it lean and fast. Note: it's specific for PostgreSQL.
func performDBMaintenance(db cdb.Handler) error {
	if db.DBMS() == "sqlite" {
		return nil
	}

	// Define the maintenance commands
	var maintenanceCommands []string

	if db.DBMS() == "postgres" {
		maintenanceCommands = []string{
			"VACUUM searchindex",
			"VACUUM keywords",
			"VACUUM keywordindex",
			"REINDEX TABLE searchindex",
			"REINDEX TABLE keywordindex",
		}
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
func retrieveAvailableSources(db cdb.Handler) ([]cdb.Source, error) {
	// Update the SQL query to fetch all necessary fields
	query := `SELECT
				source_id, url, restricted, 0 AS flags
			FROM
				Sources
			WHERE
				disabled = FALSE AND (
				   (last_crawled_at IS NULL OR last_crawled_at < NOW() - INTERVAL '3 days')
				OR (status = 'error' AND last_crawled_at < NOW() - INTERVAL '15 minutes')
				OR (status = 'completed' AND last_crawled_at < NOW() - INTERVAL '1 week')
				OR (status = 'pending'))
			ORDER BY last_crawled_at ASC`

	// Execute the query
	rows, err := db.ExecuteQuery(query)
	if err != nil {
		return nil, err
	}

	// Iterate over the results and store them in a slice
	var sourcesToCrawl []cdb.Source
	for rows.Next() {
		var src cdb.Source
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
func checkSources(db cdb.Handler, sel chan crowler.SeleniumInstance) {
	if config.DebugLevel > 0 {
		log.Println("Checking sources...")
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
				log.Println("No sources to crawl, sleeping...")
			}
			// Perform database maintenance if it's time
			if time.Now().After(maintenanceTime) {
				performDatabaseMaintenance(db)
				maintenanceTime = time.Now().Add(time.Duration(config.Crawler.Maintenance) * time.Minute)
				log.Println("Database maintenance time: ", config.Crawler.Maintenance)
			}
			time.Sleep(sleepTime)
			continue
		}

		// Crawl each source
		crawlSources(db, sel, sourcesToCrawl)
	}
}

func performDatabaseMaintenance(db cdb.Handler) {
	log.Printf("Performing database maintenance...")
	if err := performDBMaintenance(db); err != nil {
		log.Printf("Error performing database maintenance: %v", err)
	} else {
		log.Printf("Database maintenance completed successfully.")
	}
}

func crawlSources(db cdb.Handler, sel chan crowler.SeleniumInstance, sources []cdb.Source) {
	var wg sync.WaitGroup // Declare a WaitGroup

	for _, source := range sources {
		wg.Add(1) // Increment the WaitGroup counter
		log.Println("Crawling URL:", source.URL)
		go func(src cdb.Source) { // Pass the source as a parameter to the goroutine
			defer wg.Done() // Decrement the counter when the goroutine completes
			crowler.CrawlWebsite(db, src, <-sel)
		}(source) // Pass the current source
	}

	wg.Wait() // Block until all goroutines have decremented the counter
}

func initAll(configFile *string, config *cfg.Config, db *cdb.Handler, seleniumInstances *chan crowler.SeleniumInstance) {
	var err error
	// Reload the configuration file
	*config, err = cfg.LoadConfig(*configFile)
	if err != nil {
		closeResources(*db, *seleniumInstances) // Release resources
		log.Fatal("Error loading configuration file:", err)
	}

	// Reconnect to the database
	*db, err = cdb.NewHandler(*config)
	if err != nil {
		closeResources(*db, *seleniumInstances) // Release resources
		log.Fatal("Error creating database handler:", err)
	}

	// Reinitialize the Selenium services
	*seleniumInstances = make(chan crowler.SeleniumInstance, len(config.Selenium))
	for _, seleniumConfig := range config.Selenium {
		selService, err := crowler.NewSeleniumService(seleniumConfig)
		if err != nil {
			closeResources(*db, *seleniumInstances) // Release resources
			log.Fatal(err)
		}
		*seleniumInstances <- crowler.SeleniumInstance{
			Service: selService,
			Config:  seleniumConfig,
		}
	}

	// Start the crawler
	crowler.StartCrawler(*config)

}

func main() {
	// Reading command line arguments
	configFile := flag.String("config", "./config.yaml", "Path to the configuration file")
	flag.Parse()

	// Define db before we set signal handlers
	var db cdb.Handler

	// Define sel before we set signal handlers
	var seleniumInstances chan crowler.SeleniumInstance

	// Setting up a channel to listen for termination signals
	log.Println("Setting up termination signals listener...")
	signals := make(chan os.Signal, 1)
	// Catch SIGINT (Ctrl+C) and SIGTERM signals
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	// Use a select statement to block until a signal is received
	go func() {
		sig := <-signals
		switch sig {
		case syscall.SIGINT:
			// Handle SIGINT (Ctrl+C)
			log.Println("SIGINT received, shutting down...")
			closeResources(db, seleniumInstances) // Release resources
			os.Exit(0)

		case syscall.SIGTERM:
			// Handle SIGTERM
			fmt.Println("SIGTERM received, shutting down...")
			closeResources(db, seleniumInstances) // Release resources
			os.Exit(0)

		case syscall.SIGQUIT:
			// Handle SIGQUIT
			fmt.Println("SIGQUIT received, shutting down...")
			closeResources(db, seleniumInstances) // Release resources
			os.Exit(0)

		case syscall.SIGKILL:
			// Handle SIGKILL
			fmt.Println("SIGKILL received, shutting down...")
			closeResources(db, seleniumInstances) // Release resources
			os.Exit(0)

		case syscall.SIGSTOP:
			// Handle SIGSTOP
			fmt.Println("SIGSTOP received, shutting down...")
			closeResources(db, seleniumInstances) // Release resources
			os.Exit(0)

		case syscall.SIGHUP:
			// Handle SIGHUP
			fmt.Println("SIGHUP received, reloading configuration...")
			initAll(configFile, &config, &db, &seleniumInstances)
			// Connect to the database
			err := db.Connect(config)
			if err != nil {
				closeResources(db, seleniumInstances) // Release resources
				log.Fatal(err)
			}
		}
	}()

	// Initialize the crawler
	initAll(configFile, &config, &db, &seleniumInstances)

	// Connect to the database
	err := db.Connect(config)
	if err != nil {
		closeResources(db, seleniumInstances) // Release resources
		log.Fatal(err)
	}
	defer closeResources(db, seleniumInstances)

	// Start the checkSources function in a goroutine
	log.Println("Starting processing data (if any)...")
	checkSources(db, seleniumInstances)

	// Keep the main function alive
	//select {} // Infinite empty select block to keep the main goroutine running
}

func closeResources(db cdb.Handler, sel chan crowler.SeleniumInstance) {
	// Close the database connection
	if db != nil {
		db.Close()
		log.Println("Database connection closed.")
	}
	// Stop the Selenium services
	close(sel)
	for seleniumInstance := range sel {
		if seleniumInstance.Service != nil {
			err := seleniumInstance.Service.Stop()
			if err != nil {
				log.Println("Selenium instance: ", err)
			}
		}
	}
	log.Println("All services stopped.")
}
