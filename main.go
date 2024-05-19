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
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"sync"
	"syscall"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	crowler "github.com/pzaino/thecrowler/pkg/crawler"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	rules "github.com/pzaino/thecrowler/pkg/ruleset"

	_ "github.com/lib/pq"
)

const (
	sleepTime = 30 * time.Second // Time to sleep when no URLs are found
)

var (
	configFile   *string    // Configuration file path
	config       cfg.Config // Configuration "object"
	configMutex  sync.Mutex
	GRulesEngine rules.RuleEngine // Global rules engine
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
			"VACUUM Keywords",
			"VACUUM MetaTags",
			"VACUUM WebObjects",
			"VACUUM SearchIndex",
			"VACUUM KeywordIndex",
			"VACUUM MetaTagsIndex",
			"VACUUM WebObjectsIndex",
			"REINDEX TABLE WebObjects",
			"REINDEX TABLE SearchIndex",
			"REINDEX TABLE KeywordIndex",
			"REINDEX TABLE WebObjectsIndex",
			"REINDEX TABLE MetaTagsIndex",
			"REINDEX TABLE NetInfoIndex",
			"REINDEX TABLE HTTPInfoIndex",
			"REINDEX TABLE SourceInformationSeed",
			"REINDEX TABLE SourceOwnerIndex",
			"REINDEX TABLE SourceSearchIndex",
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
	// Check DB connection:
	if err := db.CheckConnection(config); err != nil {
		return nil, fmt.Errorf("error pinging the database: %w", err)
	}

	// Start a transaction
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	// Update the SQL query to fetch all necessary fields
	query := `
	SELECT
		l.source_id,
		l.url,
		l.restricted,
		l.flags,
		l.config
	FROM
		update_sources($1,$2) AS l
	ORDER BY l.last_updated_at ASC;`

	// Execute the query within the transaction
	rows, err := tx.Query(query, config.Crawler.MaxSources, cmn.GetEngineID())
	if err != nil {
		err2 := tx.Rollback()
		if err2 != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Error rolling back transaction: %v", err2)
		}
		return nil, err
	}

	// Iterate over the results and store them in a slice
	var sourcesToCrawl []cdb.Source
	for rows.Next() {
		var src cdb.Source
		if err := rows.Scan(&src.ID, &src.URL, &src.Restricted, &src.Flags, &src.Config); err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Error scanning rows: %v", err)
			rows.Close()
			err2 := tx.Rollback()
			if err2 != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "Error rolling back transaction: %v", err2)
			}
			return nil, err
		}

		// Check if Config is nil and assign a default configuration if so
		if src.Config == nil {
			src.Config = new(json.RawMessage)
			*src.Config = cdb.DefaultSourceCfgJSON
		}

		// Append the source to the slice
		sourcesToCrawl = append(sourcesToCrawl, src)
	}
	rows.Close() // Close the rows iterator

	// Commit the transaction if everything is successful
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return sourcesToCrawl, nil
}

// This function is responsible for checking the database for URLs that need to be crawled
// and kickstart the crawling process for each of them
func checkSources(db *cdb.Handler, sel *chan crowler.SeleniumInstance, RulesEngine *rules.RuleEngine) {
	cmn.DebugMsg(cmn.DbgLvlInfo, "Checking sources...")

	maintenanceTime := time.Now().Add(time.Duration(config.Crawler.Maintenance) * time.Minute)
	resourceReleaseTime := time.Now().Add(time.Duration(5) * time.Minute)
	defer configMutex.Unlock()
	for {
		configMutex.Lock()

		// Retrieve the sources to crawl
		sourcesToCrawl, err := retrieveAvailableSources(*db)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Error retrieving sources: %v", err)
			// We are about to go to sleep, so we can handle signals for reloading the configuration
			configMutex.Unlock()
			time.Sleep(sleepTime)
			continue
		}
		cmn.DebugMsg(cmn.DbgLvlDebug2, "Sources to crawl: %d", len(sourcesToCrawl))

		// Check if there are sources to crawl
		if len(sourcesToCrawl) == 0 {
			cmn.DebugMsg(cmn.DbgLvlDebug, "No sources to crawl, sleeping...")

			// Perform database maintenance if it's time
			if time.Now().After(maintenanceTime) {
				performDatabaseMaintenance(*db)
				maintenanceTime = time.Now().Add(time.Duration(config.Crawler.Maintenance) * time.Minute)
				cmn.DebugMsg(cmn.DbgLvlDebug2, "Database maintenance every: %d", config.Crawler.Maintenance)
			}
			// We are about to go to sleep, so we can handle signals for reloading the configuration
			configMutex.Unlock()
			if time.Now().After(resourceReleaseTime) {
				// Release unneeded resources:
				runtime.GC()         // Run the garbage collector
				debug.FreeOSMemory() // Force release of unused memory to the OS
				resourceReleaseTime = time.Now().Add(time.Duration(5) * time.Minute)
			}
			time.Sleep(sleepTime)
			continue
		}

		// Crawl each source
		crawlSources(*db, sel, &sourcesToCrawl, RulesEngine)

		// We have completed all jobs, so we can handle signals for reloading the configuration
		configMutex.Unlock()
	}
}

func performDatabaseMaintenance(db cdb.Handler) {
	cmn.DebugMsg(cmn.DbgLvlInfo, "Performing database maintenance...")
	if err := performDBMaintenance(db); err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error performing database maintenance: %v", err)
	} else {
		cmn.DebugMsg(cmn.DbgLvlInfo, "Database maintenance completed successfully.")
	}
}

func crawlSources(db cdb.Handler, sel *chan crowler.SeleniumInstance, sources *[]cdb.Source, RulesEngine *rules.RuleEngine) {
	if len(*sources) == 0 {
		return
	}

	// We have some sources to crawl, let's start the crawling process
	var wg sync.WaitGroup
	selIdx := 0 // Selenium instance index
	PipelineStatus := []crowler.CrawlerStatus{}
	for idx, source := range *sources {
		wg.Add(1)

		// Initialize the status
		PipelineStatus = append(PipelineStatus, crowler.CrawlerStatus{Source: source.URL, StartTime: time.Now()})

		// Prepare the go routine parameters
		args := crowler.CrawlerPars{
			WG:      &wg,
			DB:      db,
			Src:     source,
			Sel:     sel,
			SelIdx:  selIdx,
			RE:      RulesEngine,
			Sources: sources,
			Index:   idx,
			Status:  &PipelineStatus[idx],
		}

		// Start a goroutine to crawl the website
		go func(args crowler.CrawlerPars) {
			crowler.CrawlWebsite(args)
			(*args.Sources)[args.Index].Status = 1
		}(args)

		// Move to the next Selenium instance
		if len(config.Selenium) > 1 {
			selIdx++
			if selIdx >= len(config.Selenium) {
				selIdx = 0
			}
		}
	}

	// Start a goroutine to log the status periodically
	go func(plStatus *[]crowler.CrawlerStatus) {
		ticker := time.NewTicker(1 * time.Minute) // Adjust the interval as needed
		defer ticker.Stop()
		for range ticker.C {
			logStatus(plStatus)
		}
	}(&PipelineStatus)

	wg.Wait() // Block until all goroutines have decremented the counter
}

func logStatus(PipelineStatus *[]crowler.CrawlerStatus) {
	// Log the status of the pipelines
	const (
		sepLine = "====================================="
	)
	cmn.DebugMsg(cmn.DbgLvlInfo, sepLine)
	cmn.DebugMsg(cmn.DbgLvlInfo, "Pipelines status report")
	cmn.DebugMsg(cmn.DbgLvlInfo, sepLine)
	for idx, status := range *PipelineStatus {
		totalRunningTime := time.Since(status.StartTime)
		totalLinksToGo := status.TotalLinks - (status.TotalPages + status.TotalSkipped + status.TotalDuplicates)
		cmn.DebugMsg(cmn.DbgLvlInfo, "               Pipeline: %d", idx)
		cmn.DebugMsg(cmn.DbgLvlInfo, "                 Source: %s", status.Source)
		cmn.DebugMsg(cmn.DbgLvlInfo, "        Pipeline status: %s", StatusStr(status.SiteInfoRunning))
		cmn.DebugMsg(cmn.DbgLvlInfo, "        Crawling status: %s", StatusStr(status.CrawlingRunning))
		cmn.DebugMsg(cmn.DbgLvlInfo, "         NetInfo status: %s", StatusStr(status.NetInfoRunning))
		cmn.DebugMsg(cmn.DbgLvlInfo, "        HTTPInfo status: %s", StatusStr(status.HTTPInfoRunning))
		cmn.DebugMsg(cmn.DbgLvlInfo, "           Running Time: %s", totalRunningTime)
		cmn.DebugMsg(cmn.DbgLvlInfo, "    Total Crawled Pages: %d", status.TotalPages)
		cmn.DebugMsg(cmn.DbgLvlInfo, "           Total Errors: %d", status.TotalErrors)
		cmn.DebugMsg(cmn.DbgLvlInfo, "  Total Collected Links: %d", status.TotalLinks)
		cmn.DebugMsg(cmn.DbgLvlInfo, "    Total Skipped Links: %d", status.TotalSkipped)
		cmn.DebugMsg(cmn.DbgLvlInfo, " Total Duplicated Links: %d", status.TotalDuplicates)
		cmn.DebugMsg(cmn.DbgLvlInfo, "Total Links to complete: %d", totalLinksToGo)
		cmn.DebugMsg(cmn.DbgLvlInfo, "          Total Scrapes: %d", status.TotalScraped)
	}
	cmn.DebugMsg(cmn.DbgLvlInfo, sepLine)
}

func StatusStr(condition int) string {
	switch condition {
	case 0:
		return "Not started yet"
	case 1:
		return "Running"
	case 2:
		return "Completed"
	case 3:
		return "Completed with errors"
	default:
		return "Unknown"
	}
}

func initAll(configFile *string, config *cfg.Config, db *cdb.Handler, seleniumInstances *chan crowler.SeleniumInstance, RulesEngine *rules.RuleEngine) error {
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

	// Initialize the rules engine
	*RulesEngine = rules.NewEmptyRuleEngine(config.RulesetsSchemaPath)
	err = RulesEngine.LoadRulesFromConfig(config)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error loading rules from configuration: %v", err)
	}
	cmn.DebugMsg(cmn.DbgLvlInfo, "Rulesets loaded: %d", RulesEngine.CountRulesets())
	cmn.DebugMsg(cmn.DbgLvlInfo, "Detection rules loaded: %d", RulesEngine.CountDetectionRules())
	cmn.DebugMsg(cmn.DbgLvlInfo, "  Detection Noise Threshold: %f", RulesEngine.DetectionConfig.NoiseThreshold)
	cmn.DebugMsg(cmn.DbgLvlInfo, "  Detection Maybe Threshold: %f", RulesEngine.DetectionConfig.MaybeThreshold)
	cmn.DebugMsg(cmn.DbgLvlInfo, "  Detection Detected Threshold: %f", RulesEngine.DetectionConfig.DetectedThreshold)
	cmn.DebugMsg(cmn.DbgLvlInfo, "Action rules loaded: %d", RulesEngine.CountActionRules())
	cmn.DebugMsg(cmn.DbgLvlInfo, "Scraping rules loaded: %d", RulesEngine.CountScrapingRules())
	cmn.DebugMsg(cmn.DbgLvlInfo, "Crawling rules loaded: %d", RulesEngine.CountCrawlingRules())

	// Start the crawler
	crowler.StartCrawler(*config)

	return nil
}

func main() {
	// Reading command line arguments
	configFile = flag.String("config", "./config.yaml", "Path to the configuration file")
	flag.Parse()

	// Initialize the logger
	cmn.InitLogger("TheCROWler")
	cmn.DebugMsg(cmn.DbgLvlInfo, "The CROWler is starting...")

	// Define db before we set signal handlers
	var db cdb.Handler

	// Define sel before we set signal handlers
	seleniumInstances := make(chan crowler.SeleniumInstance)

	// Setting up a channel to listen for termination signals
	cmn.DebugMsg(cmn.DbgLvlInfo, "Setting up termination signals listener...")
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT, syscall.SIGHUP)

	// Use a select statement to block until a signal is received
	go func() {
		for {
			sig := <-signals
			switch sig {
			case syscall.SIGINT:
				// Handle SIGINT (Ctrl+C)
				cmn.DebugMsg(cmn.DbgLvlInfo, "SIGINT received, shutting down...")
				closeResources(db, seleniumInstances) // Release resources
				os.Exit(0)

			case syscall.SIGTERM:
				// Handle SIGTERM
				cmn.DebugMsg(cmn.DbgLvlInfo, "SIGTERM received, shutting down...")
				closeResources(db, seleniumInstances) // Release resources
				os.Exit(0)

			case syscall.SIGQUIT:
				// Handle SIGQUIT
				cmn.DebugMsg(cmn.DbgLvlInfo, "SIGQUIT received, shutting down...")
				closeResources(db, seleniumInstances) // Release resources
				os.Exit(0)

			case syscall.SIGHUP:
				// Handle SIGHUP
				cmn.DebugMsg(cmn.DbgLvlInfo, "SIGHUP received, will reload configuration as soon as all pending jobs are completed...")
				configMutex.Lock()
				err := initAll(configFile, &config, &db, &seleniumInstances, &GRulesEngine)
				if err != nil {
					configMutex.Unlock()
					cmn.DebugMsg(cmn.DbgLvlFatal, "Error initializing the crawler: %v", err)
				}
				// Connect to the database
				err = db.Connect(config)
				if err != nil {
					configMutex.Unlock()
					closeResources(db, seleniumInstances) // Release resources
					cmn.DebugMsg(cmn.DbgLvlFatal, "Error connecting to the database: %v", err)
				}
				configMutex.Unlock()
				//go checkSources(&db, seleniumInstances)
			}
		}
	}()

	// Initialize the crawler
	err := initAll(configFile, &config, &db, &seleniumInstances, &GRulesEngine)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlFatal, "Error initializing the crawler: %v", err)
	}

	// Connect to the database
	err = db.Connect(config)
	if err != nil {
		closeResources(db, seleniumInstances) // Release resources
		cmn.DebugMsg(cmn.DbgLvlFatal, "Error connecting to the database: %v", err)
	}
	defer closeResources(db, seleniumInstances)

	// Start the checkSources function in a goroutine
	cmn.DebugMsg(cmn.DbgLvlInfo, "Starting processing data (if any)...")
	checkSources(&db, &seleniumInstances, &GRulesEngine)

	// Wait forever
	//select {}
}

func closeResources(db cdb.Handler, sel chan crowler.SeleniumInstance) {
	// Close the database connection
	if db != nil {
		db.Close()
		cmn.DebugMsg(cmn.DbgLvlInfo, "Database connection closed.")
	}
	// Stop the Selenium services
	close(sel)
	for seleniumInstance := range sel {
		if seleniumInstance.Service != nil {
			err := seleniumInstance.Service.Stop()
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "Error stopping Selenium instance: %v", err)
			}
		}
	}
	cmn.DebugMsg(cmn.DbgLvlInfo, "All services stopped.")
}
