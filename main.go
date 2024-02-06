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

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	crowler "github.com/pzaino/thecrowler/pkg/crawler"
	cdb "github.com/pzaino/thecrowler/pkg/database"

	_ "github.com/lib/pq"
)

const (
	sleepTime = 30 * time.Second // Time to sleep when no URLs are found
)

var (
	config      cfg.Config // Configuration "object"
	configMutex sync.Mutex
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
	query := `
	SELECT
		l.source_id,
		l.url,
		l.restricted,
		l.flags
	FROM
		update_sources($1) AS l
	ORDER BY l.last_updated_at ASC;`

	// Execute the query
	rows, err := db.ExecuteQuery(query, config.Crawler.MaxSources)
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
func checkSources(db *cdb.Handler, sel chan crowler.SeleniumInstance) {
	cmn.DebugMsg(cmn.DbgLvlInfo, "Checking sources...")

	maintenanceTime := time.Now().Add(time.Duration(config.Crawler.Maintenance) * time.Minute)
	defer configMutex.Unlock()
	for {
		configMutex.Lock()

		// Retrieve the sources to crawl
		sourcesToCrawl, err := retrieveAvailableSources(*db)
		if err != nil {
			log.Println("Error retrieving sources:", err)
			// We are about to go to sleep, so we can handle signals for reloading the configuration
			configMutex.Unlock()
			time.Sleep(sleepTime)
			continue
		}
		cmn.DebugMsg(cmn.DbgLvlDebug, "Sources to crawl: %d", len(sourcesToCrawl))

		// Check if there are sources to crawl
		if len(sourcesToCrawl) == 0 {
			cmn.DebugMsg(cmn.DbgLvlInfo, "No sources to crawl, sleeping...")

			// Perform database maintenance if it's time
			if time.Now().After(maintenanceTime) {
				performDatabaseMaintenance(*db)
				maintenanceTime = time.Now().Add(time.Duration(config.Crawler.Maintenance) * time.Minute)
				cmn.DebugMsg(cmn.DbgLvlDebug, "Database maintenance every: %d", config.Crawler.Maintenance)
			}
			// We are about to go to sleep, so we can handle signals for reloading the configuration
			configMutex.Unlock()
			time.Sleep(sleepTime)
			continue
		}

		// Crawl each source
		crawlSources(*db, sel, sourcesToCrawl)

		// We have completed all jobs, so we can handle signals for reloading the configuration
		configMutex.Unlock()
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
	if len(sources) == 0 {
		return
	}

	// We have some sources to crawl, let's start the crawling process
	var wg sync.WaitGroup // Declare a WaitGroup

	for _, source := range sources {
		wg.Add(1)                 // Increment the WaitGroup counter
		go func(src cdb.Source) { // Pass the source as a parameter to the goroutine
			defer wg.Done() // Decrement the counter when the goroutine completes
			crowler.CrawlWebsite(db, src, <-sel, sel)
		}(source) // Pass the current source
	}

	wg.Wait() // Block until all goroutines have decremented the counter
}

func initAll(configFile *string, config *cfg.Config, db *cdb.Handler, seleniumInstances *chan crowler.SeleniumInstance) error {
	var err error
	// Reload the configuration file
	*config, err = cfg.LoadConfig(*configFile)
	if err != nil {
		return fmt.Errorf("error loading configuration file: %s", err)
	}

	// Reconnect to the database
	*db, err = cdb.NewHandler(*config)
	if err != nil {
		return fmt.Errorf("error creating database handler: %s", err)
	}

	// Reinitialize the Selenium services
	*seleniumInstances = make(chan crowler.SeleniumInstance, len(config.Selenium))
	for _, seleniumConfig := range config.Selenium {
		selService, err := crowler.NewSeleniumService(seleniumConfig)
		if err != nil {
			return fmt.Errorf("error creating Selenium Instances: %s", err)
		}
		*seleniumInstances <- crowler.SeleniumInstance{
			Service: selService,
			Config:  seleniumConfig,
		}
	}

	// Start the crawler
	crowler.StartCrawler(*config)

	return nil
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
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT, syscall.SIGHUP)

	// Use a select statement to block until a signal is received
	go func() {
		for {
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

			case syscall.SIGHUP:
				// Handle SIGHUP
				fmt.Println("SIGHUP received, will reload configuration as soon as all pending jobs are completed...")
				configMutex.Lock()
				err := initAll(configFile, &config, &db, &seleniumInstances)
				if err != nil {
					configMutex.Unlock()
					log.Fatal(err)
				}
				// Connect to the database
				err = db.Connect(config)
				if err != nil {
					configMutex.Unlock()
					closeResources(db, seleniumInstances) // Release resources
					log.Fatal(err)
				}
				configMutex.Unlock()
				//go checkSources(&db, seleniumInstances)
			}
		}
	}()

	// Initialize the crawler
	err := initAll(configFile, &config, &db, &seleniumInstances)
	if err != nil {
		log.Fatal(err)
	}

	// Connect to the database
	err = db.Connect(config)
	if err != nil {
		closeResources(db, seleniumInstances) // Release resources
		log.Fatal(err)
	}
	defer closeResources(db, seleniumInstances)

	// Start the checkSources function in a goroutine
	log.Println("Starting processing data (if any)...")
	checkSources(&db, seleniumInstances)

	// Wait forever
	//select {}
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
