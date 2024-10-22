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
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/push"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	crowler "github.com/pzaino/thecrowler/pkg/crawler"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	rules "github.com/pzaino/thecrowler/pkg/ruleset"
	"golang.org/x/time/rate"

	_ "github.com/lib/pq"
)

const (
	sleepTime = 30 * time.Second // Time to sleep when no URLs are found
)

var (
	limiter      *rate.Limiter    // Rate limiter
	configFile   *string          // Configuration file path
	config       cfg.Config       // Configuration "object"
	configMutex  sync.Mutex       // Mutex to protect the configuration
	GRulesEngine rules.RuleEngine // Global rules engine

	// Prometheus metrics
	totalPages = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "crowler_total_pages",
			Help: "Total number of pages crawled.",
		},
		[]string{"pipeline_id", "source"},
	)
	totalLinks = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "crowler_total_links",
			Help: "Total number of links collected.",
		},
		[]string{"pipeline_id", "source"},
	)
	totalErrors = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "crowler_total_errors",
			Help: "Total number of errors encountered.",
		},
		[]string{"pipeline_id", "source"},
	)
	// TODO: Define more prometheus metrics here...

)

// WorkBlock is a struct that holds all the necessary information to instantiate a new
// crawling job on the pipeline. It's used to pass the information to the goroutines
// that will perform the actual crawling.
type WorkBlock struct {
	db             cdb.Handler
	sel            *chan crowler.SeleniumInstance
	sources        *[]cdb.Source
	RulesEngine    *rules.RuleEngine
	PipelineStatus *[]crowler.Status
	Config         *cfg.Config
}

// HealthCheck is a struct that holds the health status of the application.
type HealthCheck struct {
	Status string `json:"status"`
}

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
			"REINDEX TABLE SourceInformationSeedIndex",
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
		update_sources($1,$2,$3,$4,$5,$6) AS l
	ORDER BY l.last_updated_at ASC;`

	// Execute the query within the transaction
	// TODO: Add the intervals to the query to allow a user to decide how often to crawl a source etc.
	//       replace the empty strings here with: last_ok_update, last_error, regular_crawling, processing_timeout
	rows, err := tx.Query(query, config.Crawler.MaxSources, cmn.GetEngineID(), "", "", "", "")
	if err != nil {
		err2 := tx.Rollback()
		if err2 != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "rolling back transaction: %v", err2)
		}
		return nil, err
	}

	// Iterate over the results and store them in a slice
	var sourcesToCrawl []cdb.Source
	for rows.Next() {
		var src cdb.Source
		if err := rows.Scan(&src.ID, &src.URL, &src.Restricted, &src.Flags, &src.Config); err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "scanning rows: %v", err)
			rows.Close()
			err2 := tx.Rollback()
			if err2 != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "rolling back transaction: %v", err2)
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
		src = cdb.Source{} // Reset the source
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
	// Initialize the pipeline status
	PipelineStatus := make([]crowler.Status, config.Crawler.MaxSources)
	// Set the maintenance time
	maintenanceTime := time.Now().Add(time.Duration(config.Crawler.Maintenance) * time.Minute)
	// Set the resource release time
	resourceReleaseTime := time.Now().Add(time.Duration(5) * time.Minute)

	// Start the main loop
	defer configMutex.Unlock()
	for {
		configMutex.Lock()

		// Retrieve the sources to crawl
		sourcesToCrawl, err := retrieveAvailableSources(*db)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "retrieving sources: %v", err)
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
		workBlock := WorkBlock{
			db:             *db,
			sel:            sel,
			sources:        &sourcesToCrawl,
			RulesEngine:    RulesEngine,
			PipelineStatus: &PipelineStatus,
			Config:         &config,
		}
		crawlSources(&workBlock)

		// We have completed all jobs, so we can handle signals for reloading the configuration
		configMutex.Unlock()
		sourcesToCrawl = []cdb.Source{} // Reset the sources
	}
}

func performDatabaseMaintenance(db cdb.Handler) {
	cmn.DebugMsg(cmn.DbgLvlInfo, "Performing database maintenance...")
	if err := performDBMaintenance(db); err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "performing database maintenance: %v", err)
	} else {
		cmn.DebugMsg(cmn.DbgLvlInfo, "Database maintenance completed successfully.")
	}
}

func crawlSources(wb *WorkBlock) {
	// Start a goroutine to log the status periodically
	go func(plStatus *[]crowler.Status) {
		ticker := time.NewTicker(time.Duration(wb.Config.Crawler.ReportInterval) * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			// Check if all the pipelines have completed
			pipelinesRunning := false
			for _, status := range *plStatus {
				if status.PipelineRunning == 1 {
					pipelinesRunning = true
					break
				}
			}
			logStatus(plStatus)
			if !pipelinesRunning {
				// All pipelines have completed
				// Stop the ticker
				break
			}
		}
	}(wb.PipelineStatus)

	// Start the crawling process for each source
	var wg sync.WaitGroup // WaitGroup to wait for all goroutines to finish
	selIdx := 0           // Selenium instance index
	sourceIdx := 0        // Source index
	for idx := 0; idx < wb.Config.Crawler.MaxSources; idx++ {
		// Check if the pipeline is already running
		if (*wb.PipelineStatus)[idx].PipelineRunning == 1 {
			continue
		}

		// Get the source to crawl
		source := (*wb.sources)[sourceIdx]
		wg.Add(1)

		// Initialize the status
		(*wb.PipelineStatus)[idx] = crowler.Status{
			PipelineID:      uint64(idx),
			Source:          source.URL,
			SourceID:        source.ID,
			PipelineRunning: 0,
			CrawlingRunning: 0,
			NetInfoRunning:  0,
			HTTPInfoRunning: 0,
			TotalPages:      0,
			TotalErrors:     0,
			TotalLinks:      0,
			TotalSkipped:    0,
			TotalDuplicates: 0,
			TotalScraped:    0,
			TotalActions:    0,
			LastWait:        0,
			LastDelay:       0,
		}

		// Start a goroutine to crawl the website
		startCrawling(wb, &wg, selIdx, source, idx)

		// Increment the Source index to get the next source
		sourceIdx++
		if sourceIdx >= len(*wb.sources) {
			break // We have reached the end of the sources
		}
	}

	wg.Wait() // Block until all goroutines have decremented the counter
}

func startCrawling(wb *WorkBlock, wg *sync.WaitGroup, selIdx int, source cdb.Source, idx int) {
	// Prepare the go routine parameters
	args := crowler.Pars{
		WG:      wg,
		DB:      wb.db,
		Src:     source,
		Sel:     wb.sel,
		SelIdx:  selIdx,
		RE:      wb.RulesEngine,
		Sources: wb.sources,
		Index:   idx,
		Status:  &((*wb.PipelineStatus)[idx]), // Pointer to a single status element
	}

	// Start a goroutine to crawl the website
	go func(args crowler.Pars) {
		//defer wg.Done()

		// Acquire a Selenium instance
		seleniumInstance := <-*args.Sel

		// Channel to release the Selenium instance
		releaseSelenium := make(chan crowler.SeleniumInstance)

		// Start crawling the website synchronously
		go crowler.CrawlWebsite(args, seleniumInstance, releaseSelenium)

		// Release the Selenium instance when done
		*args.Sel <- <-releaseSelenium

	}(args)
}

func logStatus(PipelineStatus *[]crowler.Status) {
	// Log the status of the pipelines
	const (
		sepRLine = "====================================="
		sepPLine = "-------------------------------------"
	)
	report := "Pipelines status report\n"
	report += sepRLine + "\n"
	runningPipelines := 0
	for idx := 0; idx < len(*PipelineStatus); idx++ {
		status := (*PipelineStatus)[idx]
		if status.PipelineRunning == 0 {
			continue
		} else {
			runningPipelines++
		}
		var totalRunningTime time.Duration
		if status.EndTime.IsZero() {
			totalRunningTime = time.Since(status.StartTime)
		} else {
			totalRunningTime = status.EndTime.Sub(status.StartTime)
		}
		totalLinksToGo := status.TotalLinks - (status.TotalPages + status.TotalSkipped + status.TotalDuplicates)
		if totalLinksToGo < 0 {
			totalLinksToGo = 0
		}
		report += fmt.Sprintf("               Pipeline: %d\n", status.PipelineID)
		report += fmt.Sprintf("                 Source: %s\n", status.Source)
		report += fmt.Sprintf("        Pipeline status: %s\n", StatusStr(status.PipelineRunning))
		report += fmt.Sprintf("        Crawling status: %s\n", StatusStr(status.CrawlingRunning))
		report += fmt.Sprintf("         NetInfo status: %s\n", StatusStr(status.NetInfoRunning))
		report += fmt.Sprintf("        HTTPInfo status: %s\n", StatusStr(status.HTTPInfoRunning))
		report += fmt.Sprintf("           Running Time: %s\n", totalRunningTime)
		report += fmt.Sprintf("    Total Crawled Pages: %d\n", status.TotalPages)
		report += fmt.Sprintf("           Total Errors: %d\n", status.TotalErrors)
		report += fmt.Sprintf("  Total Collected Links: %d\n", status.TotalLinks)
		report += fmt.Sprintf("    Total Skipped Links: %d\n", status.TotalSkipped)
		report += fmt.Sprintf(" Total Duplicated Links: %d\n", status.TotalDuplicates)
		report += fmt.Sprintf("Total Links to complete: %d\n", totalLinksToGo)
		report += fmt.Sprintf("          Total Scrapes: %d\n", status.TotalScraped)
		report += fmt.Sprintf("          Total Actions: %d\n", status.TotalActions)
		report += fmt.Sprintf("         Last Page Wait: %f\n", status.LastWait)
		report += fmt.Sprintf("        Last Page Delay: %f\n", status.LastDelay)
		report += sepPLine + "\n"

		// Update the metrics
		updateMetrics(status)

		// Reset the status if the pipeline has completed (display only the last report)
		if status.PipelineRunning == 2 || status.PipelineRunning == 3 {
			status.PipelineRunning = 0
		}
	}
	report += sepRLine + "\n"
	if runningPipelines > 0 {
		cmn.DebugMsg(cmn.DbgLvlInfo, report)
	}
}

func updateMetrics(status crowler.Status) {
	if !config.Prometheus.Enabled {
		return
	}

	// Update the metrics
	labels := prometheus.Labels{
		"pipeline_id": fmt.Sprintf("%d", status.PipelineID),
		"source":      status.Source,
	}
	totalPages.With(labels).Set(float64(status.TotalPages))
	totalLinks.With(labels).Set(float64(status.TotalLinks))
	totalErrors.With(labels).Set(float64(status.TotalErrors))

	// Push metrics
	if err := push.New("http://"+config.Prometheus.Host+":"+strconv.Itoa(config.Prometheus.Port), "crowler_engine").
		Collector(totalPages).
		// Add other collectors...
		Grouping("pipeline_id", fmt.Sprintf("%d", status.PipelineID)).
		Push(); err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Could not push metrics: %v", err)
	}

	// Delete metrics if pipeline is complete
	if status.PipelineRunning == 2 || status.PipelineRunning == 3 {
		// Use the configured pushgateway URL
		if err := push.New("http://"+config.Prometheus.Host+":"+strconv.Itoa(config.Prometheus.Port), "crowler_engine").
			Grouping("pipeline_id", fmt.Sprintf("%d", status.PipelineID)).
			Delete(); err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Could not delete metrics: %v", err)
		}
	}
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

func initAll(configFile *string, config *cfg.Config,
	db *cdb.Handler, seleniumInstances *chan crowler.SeleniumInstance,
	RulesEngine *rules.RuleEngine, lmt **rate.Limiter) error {
	var err error

	// Reload the configuration file
	*config, err = cfg.LoadConfig(*configFile)
	if err != nil {
		return fmt.Errorf("loading configuration file: %s", err)
	}

	// Reset Key-Value Store
	cmn.KVStore = nil
	cmn.KVStore = cmn.NewKeyValueStore()

	// Reconnect to the database
	*db, err = cdb.NewHandler(*config)
	if err != nil {
		return fmt.Errorf("creating database handler: %s", err)
	}

	// Set the rate limiter
	var rl, bl int
	if strings.TrimSpace(config.Crawler.Control.RateLimit) == "" {
		config.Crawler.Control.RateLimit = "10,10"
	}
	if !strings.Contains(config.Crawler.Control.RateLimit, ",") {
		config.Crawler.Control.RateLimit = config.API.RateLimit + ",10"
	}
	rlStr := strings.Split(config.Crawler.Control.RateLimit, ",")[0]
	if rlStr == "" {
		rlStr = "10"
	}
	rl, err = strconv.Atoi(rlStr)
	if err != nil {
		rl = 10
	}
	blStr := strings.Split(config.Crawler.Control.RateLimit, ",")[1]
	if blStr == "" {
		blStr = "10"
	}
	bl, err = strconv.Atoi(blStr)
	if err != nil {
		bl = 10
	}
	*lmt = rate.NewLimiter(rate.Limit(rl), bl)

	// Reinitialize the Selenium services
	*seleniumInstances = make(chan crowler.SeleniumInstance, len(config.Selenium))
	for _, seleniumConfig := range config.Selenium {
		selService, err := crowler.NewSeleniumService(seleniumConfig)
		if err != nil {
			return fmt.Errorf("creating Selenium Instances: %s", err)
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
		cmn.DebugMsg(cmn.DbgLvlError, "loading rules from configuration: %v", err)
	}
	cmn.DebugMsg(cmn.DbgLvlInfo, "Rulesets loaded: %d", RulesEngine.CountRulesets())
	cmn.DebugMsg(cmn.DbgLvlInfo, "Detection rules loaded: %d", RulesEngine.CountDetectionRules())
	cmn.DebugMsg(cmn.DbgLvlInfo, "  Detection Noise Threshold: %f", RulesEngine.DetectionConfig.NoiseThreshold)
	cmn.DebugMsg(cmn.DbgLvlInfo, "  Detection Maybe Threshold: %f", RulesEngine.DetectionConfig.MaybeThreshold)
	cmn.DebugMsg(cmn.DbgLvlInfo, "  Detection Detected Threshold: %f", RulesEngine.DetectionConfig.DetectedThreshold)
	cmn.DebugMsg(cmn.DbgLvlInfo, "Action rules loaded: %d", RulesEngine.CountActionRules())
	cmn.DebugMsg(cmn.DbgLvlInfo, "Scraping rules loaded: %d", RulesEngine.CountScrapingRules())
	cmn.DebugMsg(cmn.DbgLvlInfo, "Crawling rules loaded: %d", RulesEngine.CountCrawlingRules())
	cmn.DebugMsg(cmn.DbgLvlInfo, "Plugins loaded: %d", RulesEngine.CountPlugins())

	// Initialize the prometheus metrics
	if config.Prometheus.Enabled {
		prometheus.MustRegister(totalPages)
		prometheus.MustRegister(totalLinks)
		prometheus.MustRegister(totalErrors)
	}

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
				err := initAll(configFile, &config, &db, &seleniumInstances, &GRulesEngine, &limiter)
				if err != nil {
					configMutex.Unlock()
					cmn.DebugMsg(cmn.DbgLvlFatal, "initializing the crawler: %v", err)
				}
				// Connect to the database
				err = db.Connect(config)
				if err != nil {
					configMutex.Unlock()
					closeResources(db, seleniumInstances) // Release resources
					cmn.DebugMsg(cmn.DbgLvlFatal, "connecting to the database: %v", err)
				}
				configMutex.Unlock()
				//go checkSources(&db, seleniumInstances)
			}
		}
	}()

	// Initialize the crawler
	err := initAll(configFile, &config, &db, &seleniumInstances, &GRulesEngine, &limiter)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlFatal, "initializing the crawler: %v", err)
	}

	// Connect to the database
	err = db.Connect(config)
	if err != nil {
		closeResources(db, seleniumInstances) // Release resources
		cmn.DebugMsg(cmn.DbgLvlFatal, "connecting to the database: %v", err)
	}
	defer closeResources(db, seleniumInstances)

	// Start the checkSources function in a goroutine
	cmn.DebugMsg(cmn.DbgLvlInfo, "Starting processing data (if any)...")
	go checkSources(&db, &seleniumInstances, &GRulesEngine)

	// Start the internal/control API server
	srv := &http.Server{
		Addr: config.Crawler.Control.Host + ":" + fmt.Sprintf("%d", config.Crawler.Control.Port),

		// ReadHeaderTimeout is the amount of time allowed to read
		// request headers. The connection's read deadline is reset
		// after reading the headers and the Handler can decide what
		// is considered too slow for the body. If ReadHeaderTimeout
		// is zero, the value of ReadTimeout is used. If both are
		// zero, there is no timeout.
		ReadHeaderTimeout: time.Duration(config.Crawler.Control.ReadHeaderTimeout) * time.Second,

		// ReadTimeout is the maximum duration for reading the entire
		// request, including the body. A zero or negative value means
		// there will be no timeout.
		//
		// Because ReadTimeout does not let Handlers make per-request
		// decisions on each request body's acceptable deadline or
		// upload rate, most users will prefer to use
		// ReadHeaderTimeout. It is valid to use them both.
		ReadTimeout: time.Duration(config.Crawler.Control.ReadTimeout) * time.Second,

		// WriteTimeout is the maximum duration before timing out
		// writes of the response. It is reset whenever a new
		// request's header is read. Like ReadTimeout, it does not
		// let Handlers make decisions on a per-request basis.
		// A zero or negative value means there will be no timeout.
		WriteTimeout: time.Duration(config.Crawler.Control.WriteTimeout) * time.Second,

		// IdleTimeout is the maximum amount of time to wait for the
		// next request when keep-alive are enabled. If IdleTimeout
		// is zero, the value of ReadTimeout is used. If both are
		// zero, there is no timeout.
		IdleTimeout: time.Duration(config.Crawler.Control.Timeout) * time.Second,
	}

	// Set the handlers
	initAPIv1()

	cmn.DebugMsg(cmn.DbgLvlInfo, "Starting server on %s:%d", config.Crawler.Control.Host, config.Crawler.Control.Port)
	var rStatus error
	if strings.ToLower(strings.TrimSpace(config.Crawler.Control.SSLMode)) == "enable" {
		rStatus = srv.ListenAndServeTLS(config.API.CertFile, config.API.KeyFile)
	} else {
		rStatus = srv.ListenAndServe()
	}
	statusMsg := "Server stopped."
	if rStatus != nil {
		statusMsg = fmt.Sprintf("Server stopped with error: %v", rStatus)
	}
	cmn.DebugMsg(cmn.DbgLvlFatal, statusMsg)
}

// initAPIv1 initializes the API v1 handlers
func initAPIv1() {
	// Health check
	healthCheckWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(healthCheckHandler)))

	http.Handle("/v1/health", healthCheckWithMiddlewares)

	// Config Check
	configCheckWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(configCheckHandler)))

	http.Handle("/v1/config", configCheckWithMiddlewares)
}

// RateLimitMiddleware is a middleware for rate limiting
func RateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !limiter.Allow() {
			cmn.DebugMsg(cmn.DbgLvlDebug, "Rate limit exceeded")
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// SecurityHeadersMiddleware adds security-related headers to responses
func SecurityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Add various security headers here
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'")

		next.ServeHTTP(w, r)
	})
}

// handleErrorAndRespond encapsulates common error handling and JSON response logic.
func handleErrorAndRespond(w http.ResponseWriter, err error, results interface{}, errMsg string, errCode int, successCode int) {
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug3, errMsg, err)
		http.Error(w, err.Error(), errCode)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(successCode) // Explicitly set the success status code
	if err := json.NewEncoder(w).Encode(results); err != nil {
		// Log the error and send a generic error message to the client
		cmn.DebugMsg(cmn.DbgLvlDebug3, "Error encoding JSON response: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

func healthCheckHandler(w http.ResponseWriter, r *http.Request) {
	// Create a JSON document with the health status
	healthStatus := HealthCheck{
		Status: "OK",
	}

	// Respond with the health status
	handleErrorAndRespond(w, nil, healthStatus, "Error in health Check: ", http.StatusInternalServerError, http.StatusOK)
}

func configCheckHandler(w http.ResponseWriter, r *http.Request) {
	// Make a copy of the configuration and remove the sensitive data
	configCopy := config
	configCopy.Database.Password = "********"

	// Respond with the configuration
	handleErrorAndRespond(w, nil, configCopy, "Error in configuration Check: ", http.StatusInternalServerError, http.StatusOK)
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
				cmn.DebugMsg(cmn.DbgLvlError, "stopping Selenium instance: %v", err)
			}
		}
	}
	cmn.DebugMsg(cmn.DbgLvlInfo, "All services stopped.")
}
