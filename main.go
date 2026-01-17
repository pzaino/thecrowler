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
	"math"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/push"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	crowler "github.com/pzaino/thecrowler/pkg/crawler"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	rules "github.com/pzaino/thecrowler/pkg/ruleset"
	vdi "github.com/pzaino/thecrowler/pkg/vdi"
	"golang.org/x/time/rate"

	_ "github.com/lib/pq"
)

// PipelineStatusReport is a struct that holds the status report of a crawling pipeline.
type PipelineStatusReport struct {
	PipelineID      uint64  `json:"pipeline_id"`
	Source          string  `json:"source"`
	SourceID        uint64  `json:"source_id"`
	VDIID           string  `json:"vdi_id"`
	PipelineStatus  string  `json:"pipeline_status"`
	CrawlingStatus  string  `json:"crawling_status"`
	NetInfoStatus   string  `json:"netinfo_status"`
	HTTPInfoStatus  string  `json:"httpinfo_status"`
	RunningTime     string  `json:"running_time"`
	TotalPages      int64   `json:"total_pages"`
	TotalErrors     int64   `json:"total_errors"`
	TotalLinks      int64   `json:"total_links"`
	TotalSkipped    int64   `json:"total_skipped"`
	TotalDuplicates int64   `json:"total_duplicates"`
	TotalLinksToGo  int64   `json:"total_links_to_go"`
	TotalScrapes    int64   `json:"total_scrapes"`
	TotalActions    int64   `json:"total_actions"`
	LastRetry       int64   `json:"last_retry"`
	LastWait        float64 `json:"last_wait"`
	LastDelay       float64 `json:"last_delay"`
	CollectionState string  `json:"collection_state"`
}

var (
	limiter     *rate.Limiter // Rate limiter
	configFile  *string       // Configuration file path
	config      cfg.Config    // Configuration "object"
	configMutex sync.RWMutex  // Mutex to protect the configuration
	sysReadyMtx sync.RWMutex  // Mutex to protect the SysReady variable
	sysReady    int           // System readiness status variable 0 = not ready, 1 = starting up, 2 = ready
	// GRulesEngine Global rules engine
	GRulesEngine rules.RuleEngine // GRulesEngine Global rules engine
	// Global DB handler
	dbHandler cdb.Handler
	// Global Pipeline Status (read only!!!!!)
	sysPipelineStatus *[]crowler.Status
)

// WorkBlock is a struct that holds all the necessary information to instantiate a new
// crawling job on the pipeline. It's used to pass the information to the goroutines
// that will perform the actual crawling.
type WorkBlock struct {
	db cdb.Handler
	//sel            *chan vdi.SeleniumInstance
	sel            *vdi.Pool
	sources        *[]cdb.Source
	RulesEngine    *rules.RuleEngine
	PipelineStatus *[]crowler.Status
	Config         *cfg.Config
}

// HealthCheck is a struct that holds the health status of the application.
type HealthCheck struct {
	Status string `json:"status"`
}

// ReadyCheck is a struct that holds the system readiness status.
type ReadyCheck struct {
	Status string `json:"ready"`
}

func setSysReady(newStatus int) {
	if newStatus < 0 || newStatus > 2 {
		return
	}
	sysReadyMtx.Lock()
	defer sysReadyMtx.Unlock()
	sysReady = newStatus
}

func getSysReady() int {
	sysReadyMtx.RLock()
	defer sysReadyMtx.RUnlock()
	return sysReady
}

// NewVDIPool is a function that creates a new VDI pool with the given configurations.
func NewVDIPool(p *vdi.Pool, configs []cfg.Selenium) error {
	p.Init(len(configs))
	if p == nil {
		return fmt.Errorf("creating VDI pool")
	}

	// Initialize the pool with the Selenium instances
	for i, seleniumConfig := range configs {
		selService, err := vdi.NewVDIService(seleniumConfig)
		if err != nil {
			return fmt.Errorf("creating VDI Instances: %s", err)
		}
		cmn.DebugMsg(cmn.DbgLvlDebug2, "VDI instance %s:%d:%d created", seleniumConfig.Host, seleniumConfig.Port, i)
		selInstance := vdi.SeleniumInstance{
			Service: selService,
			Config:  seleniumConfig,
		}
		err = p.Add(selInstance)
		if err != nil {
			return fmt.Errorf("adding VDI instance to pool: %s", err)
		}
		cmn.DebugMsg(cmn.DbgLvlDebug2, "VDI instance added to the pool")
	}
	return nil
}

// This function is responsible for performing database maintenance
// to keep it lean and fast. Note: it's specific for PostgreSQL.
func performDBMaintenance(db cdb.Handler) error {
	if db.DBMS() == cdb.DBSQLiteStr {
		return nil
	}

	// Define the maintenance commands
	var maintenanceCommands []string

	if db.DBMS() == cdb.DBPostgresStr {
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
func retrieveAvailableSources(db cdb.Handler, maxSources int) ([]cdb.Source, error) {
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
		update_sources($1,$2,$3,$4,$5,$6,$7) AS l
	ORDER BY l.last_updated_at ASC;`

	// Execute the query within the transaction
	if maxSources <= 0 {
		maxSources = config.Crawler.MaxSources
	}
	rows, err := tx.Query(query, maxSources, config.Crawler.SourcePriority, cmn.GetEngineID(), config.Crawler.CrawlingIfOk, config.Crawler.CrawlingIfError, config.Crawler.CrawlingInterval, config.Crawler.ProcessingTimeout)
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
			err2 := rows.Close()
			if err2 != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "closing rows iterator: %v", err2)
			}
			err2 = tx.Rollback()
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
	err = rows.Close() // Close the rows iterator
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "closing rows iterator: %v", err)
	}

	// Commit the transaction if everything is successful
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return sourcesToCrawl, nil
}

/*sel *chan vdi.SeleniumInstance */
// This function is responsible for checking the database for URLs that need to be crawled
// and kickstart the crawling process for each of them
func checkSources(db *cdb.Handler, sel *vdi.Pool, RulesEngine *rules.RuleEngine) {
	cmn.DebugMsg(cmn.DbgLvlInfo, "Checking sources...")
	// Initialize the pipeline status slice
	PipelineStatus := make([]crowler.Status, 0, len(config.Selenium))
	// Assign the global pipeline status pointer
	sysPipelineStatus = &PipelineStatus
	now := time.Now()
	// Set the maintenance time
	maintenanceTime := now.Add(time.Duration(config.Crawler.Maintenance) * time.Minute)
	// Set the resource release time
	resourceReleaseTime := now.Add(time.Duration(5) * time.Minute)
	// Flag used to avoid repeating 0 sources found constantly
	gotSources := true // set to true by default for first source check (so it will log the first time)

	// Start the main loop
	defer configMutex.RUnlock()
	for {
		configMutex.RLock()

		if !config.Crawler.Schedule.IsActive(time.Now()) {
			// We are not active right now, so we can handle signals for reloading the configuration
			configMutex.RUnlock()
			time.Sleep(time.Duration(config.Crawler.QueryTimer) * time.Second)
			continue
		}

		// Retrieve the sources to crawl
		sourcesToCrawl, err := retrieveAvailableSources(*db, 0)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "retrieving sources: %v", err)
			// We are about to go to sleep, so we can handle signals for reloading the configuration
			configMutex.RUnlock()
			time.Sleep(time.Duration(config.Crawler.QueryTimer) * time.Second)
			continue
		}

		// Check if there are sources to crawl
		if len(sourcesToCrawl) == 0 {
			if gotSources {
				cmn.DebugMsg(cmn.DbgLvlDebug2, "No sources to crawl, sleeping...")
				gotSources = false
			}

			// Perform database maintenance if it's time
			now := time.Now()
			if now.After(maintenanceTime) {
				performDatabaseMaintenance(*db)
				maintenanceTime = now.Add(time.Duration(config.Crawler.Maintenance) * time.Minute)
				cmn.DebugMsg(cmn.DbgLvlDebug2, "Database maintenance every: %d", config.Crawler.Maintenance)
			}
			// We are about to go to sleep, so we can handle signals for reloading the configuration
			configMutex.RUnlock()
			if now.After(resourceReleaseTime) {
				// Release unneeded resources:
				runtime.GC()         // Run the garbage collector
				debug.FreeOSMemory() // Force release of unused memory to the OS
				resourceReleaseTime = now.Add(time.Duration(5) * time.Minute)
			}
			time.Sleep(time.Duration(config.Crawler.QueryTimer) * time.Second)
			continue
		}
		cmn.DebugMsg(cmn.DbgLvlDebug2, "Sources to crawl: %d", len(sourcesToCrawl))
		gotSources = true

		// Crawl each source
		workBlock := WorkBlock{
			db:             *db,
			sel:            sel,
			sources:        &sourcesToCrawl,
			RulesEngine:    RulesEngine,
			PipelineStatus: &PipelineStatus,
			Config:         &config,
		}
		// Create a new WorkBlock for the crawling job
		batchUID := cmn.GenerateUID()
		event := cdb.Event{
			Action:    "new",
			Type:      "started_batch_crawling",
			SourceID:  0,
			Severity:  config.Crawler.SourcePriority,
			ExpiresAt: time.Now().Add(2 * time.Minute).Format(time.RFC3339),
			Details: map[string]interface{}{
				"uid":                batchUID,
				"node":               cmn.GetMicroServiceName(),
				"time":               time.Now(),
				"initial_batch_size": len(sourcesToCrawl),
			},
		}
		createEvent(*db, event, 0)
		totSrc := crawlSources(&workBlock) // Start the crawling of this batch of sources
		event = cdb.Event{
			Action:    "new",
			Type:      "completed_batch_crawling",
			SourceID:  0,
			Severity:  config.Crawler.SourcePriority,
			ExpiresAt: time.Now().Add(2 * time.Minute).Format(time.RFC3339),
			Details: map[string]interface{}{
				"uid":              batchUID,
				"node":             cmn.GetMicroServiceName(),
				"time":             time.Now(),
				"final_batch_size": totSrc,
			},
		}
		createEvent(*db, event, 0)
		cmn.DebugMsg(cmn.DbgLvlInfo, "Crawled '%d' sources in this batch", totSrc)

		// We have completed all jobs, so we can handle signals for reloading the configuration
		configMutex.RUnlock()

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

func createEvent(db cdb.Handler, event cdb.Event, flags int) {
	event.Action = strings.ToLower(strings.TrimSpace(event.Action))

	if (event.Action == "") && (flags&1 == 0) {
		cmn.DebugMsg(cmn.DbgLvlError, "Action field is empty, ignoring event")
		return
	}

	if event.Action == "new" { // nolint:goconst // it's ok here to have a duplicate "new"

		_, err := cdb.CreateEventWithRetries(&db, event)
		if err == nil {
			return // success!
		}

		// Final failure
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to create event on the DB after retries: %v", err)
	}
}

func getEngineID() int {
	engineName := cmn.GetMicroServiceName()
	engineMultiplier := 1
	// The engine ID is usually preceded by either a - or a _
	// So check if it contains either of them, if yes try to use them to extract the last part, otherwise use the last two chars
	if strings.Contains(engineName, "-") {
		parts := strings.Split(engineName, "-")
		lastPart := parts[len(parts)-1]
		if id, err := strconv.Atoi(lastPart); err == nil {
			engineMultiplier = id
		}
	} else if strings.Contains(engineName, "_") {
		parts := strings.Split(engineName, "_")
		lastPart := parts[len(parts)-1]
		if id, err := strconv.Atoi(lastPart); err == nil {
			engineMultiplier = id
		}
	} else if len(engineName) > 2 {
		lastPart := engineName[len(engineName)-2:]
		if id, err := strconv.Atoi(lastPart); err == nil {
			engineMultiplier = id
		}
	}
	return engineMultiplier
}

func crawlSources(wb *WorkBlock) uint64 {

	var (
		batchWg          sync.WaitGroup
		refillLock       cmn.SafeMutex // Mutex to protect the refill operation
		closeChanOnce    sync.Once
		statusLock       sync.Mutex    // Mutex to protect the PipelineStatus slice
		TotalSources     atomic.Uint64 // Total Crawled Sources Counter
		BatchCompleted   atomic.Bool   // Entire Batch Completed (included Refills)
		LastActivity     atomic.Value  // Last Activity Timestamp
		PipelinesRunning atomic.Bool   // Pipelines are currently running
		RampUpRunning    atomic.Bool   // Ramp Up is currently running
		BusyInstances    atomic.Int32  // Number of busy pipelines
	)
	TotalSources.Store(0)
	BatchCompleted.Store(false)
	LastActivity.Store(time.Now())
	PipelinesRunning.Store(true)
	RampUpRunning.Store(true)
	BusyInstances.Store(0)

	// Create the sources' queue (channel)
	sourceChan := make(chan cdb.Source, wb.Config.Crawler.MaxSources*2)

	maxPipelines := len(config.Selenium) //nolint:gosec
	cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG Pipeline] Max pipelines: %d", maxPipelines)
	cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG Pipeline] Max sources: %d", wb.Config.Crawler.MaxSources*2)

	// "Reports" go routine, used to produce periodic reports on the pipelines status (during crawling):
	go func(plStatus *[]crowler.Status) {
		interval := time.Duration(wb.Config.Crawler.ReportInterval) * time.Minute
		now := time.Now()
		next := now.Add(interval)

		for {
			anyPipelineStillRunning := false
			for idx := range *plStatus {
				status := &(*plStatus)[idx]
				if status.PipelineRunning.Load() > 0 {
					anyPipelineStillRunning = true
					break
				}
			}

			// Run the log
			logStatus(plStatus)

			// Exit condition
			if !anyPipelineStillRunning && !RampUpRunning.Load() {
				PipelinesRunning.Store(false)
				break
			}

			// Sleep until the exact next interval boundary
			time.Sleep(time.Until(next))
			next = next.Add(interval)
		}
	}(wb.PipelineStatus)

	// "Refill" go routine: (used to avoid pipeline starvation during crawling)
	go func() {
		inactivityTimeout := 60 * time.Second
		timer := time.NewTimer(inactivityTimeout)
		// Determine the target queue capacity for refilling decisions
		targetCap := cap(sourceChan)
		if targetCap == 0 {
			// Unbuffered or unknown: fall back to VDIs count so we don’t block forever
			targetCap = len(wb.Config.Selenium)
		}
		// Optional: low/high water marks to smooth bursts
		lowWater := targetCap / 2 // start refilling when queue drops below this
		highWater := targetCap    // never exceed channel cap

		// helper to reset timer
		resetTimer := func() {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(inactivityTimeout)
		}

		defer func() {
			// make sure timer is stopped/drained to avoid leaks
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}

			defer func() {
				recover() //nolint:errcheck // avoid panic if somehow closed elsewhere
			}()

			closeChanOnce.Do(func() {
				close(sourceChan)
				BatchCompleted.Store(true)
				cmn.DebugMsg(cmn.DbgLvlInfo, "No new sources received in the last %v — closing pipeline.", inactivityTimeout)
				refillLock.Unlock() // this is a special lock which is self-aware, so we can safely try to unlock it here to ensure that if is locked we'll unlock and if it isn't we won't panic
			})
		}()

		// ensure LastActivity sane
		if v := LastActivity.Load(); v == nil {
			LastActivity.Store(time.Now())
		}

		for {
			select {
			case <-timer.C:
				// Timeout expired → no new sources, close pipeline
				if PipelinesRunning.Load() || RampUpRunning.Load() {
					last := LastActivity.Load().(time.Time)
					if time.Since(last) < (1 * time.Minute) {
						// Yes, so reset the timer anyway
						resetTimer()
						continue
					}
					PipelinesRunning.Store(false)
					RampUpRunning.Store(false)
					BatchCompleted.Store(true)
					time.Sleep(15 * time.Second) // Give some time to the pipelines to complete
				} else {
					cmn.DebugMsg(cmn.DbgLvlInfo, "No new sources received in the last %v — closing pipeline.", inactivityTimeout)
					return
				}
			default:
				refillLock.Lock()

				// First let's check if we are still in an active schedule:
				configMutex.RLock()
				if !wb.Config.Crawler.Schedule.IsActive(time.Now()) {
					// Not active, so we can close the channel and exit
					configMutex.RUnlock()
					BatchCompleted.Store(true)
					closeChanOnce.Do(func() {
						close(sourceChan)
						cmn.DebugMsg(cmn.DbgLvlInfo, "Crawling schedule is no longer active — closing pipeline.")
					})
					refillLock.Unlock()
					return
				}
				configMutex.RUnlock()

				// We are in active schedule, so check if we need to refill the source channel
				if (wb.sel.Available() > 0) && (len(sourceChan) < lowWater) {
					// We need to refill the source channel
					need := highWater - len(sourceChan)

					newSources := []cdb.Source{}
					var err error
					if !BatchCompleted.Load() {
						newSources, err = monitorBatchAndRefill(wb, need)
					}
					if err != nil {
						cmn.DebugMsg(cmn.DbgLvlWarn, "monitorBatchAndRefill error: %v", err)
					} else if len(newSources) > 0 {
						LastActivity.Store(time.Now()) // Reset activity
						cmn.DebugMsg(cmn.DbgLvlDebug, "Refilling batch with %d new sources", len(newSources))
						if !BatchCompleted.Load() {
							for _, src := range newSources {
								sourceChan <- src
							}
							// Reset the timer because we received new sources
							resetTimer()
						}
					} else {
						// no data returned, but are we still collecting from sources?
						last := LastActivity.Load().(time.Time)
						if time.Since(last) < (1 * time.Minute) {
							// Yes, so reset the timer anyway
							resetTimer()
						}
					}
				} else if (wb.sel.Available() == 0) || (len(sourceChan) >= highWater) {
					// Reset the timer, we are busy
					resetTimer()
				}
				refillLock.Unlock()
				time.Sleep(2 * time.Second) // Avoid tight loop
			}
		}
	}()

	// "Inactivity Watchdog" (used to clean up pipelines that may be gone stale):
	go func() {
		ticker := time.NewTicker(30 * time.Second) // check every 30 seconds
		defer ticker.Stop()

		for { //nolint:gosimple // infinite loop is intentional
			select {
			case <-ticker.C:
				last := LastActivity.Load().(time.Time)
				if (time.Since(last) > (5 * time.Minute)) && !PipelinesRunning.Load() && !RampUpRunning.Load() {
					cmn.DebugMsg(cmn.DbgLvlInfo, "No crawling activity for 5 minutes, closing sourceChan.")
					closeChanOnce.Do(func() {
						BatchCompleted.Store(true) // Set the batch as completed
						close(sourceChan)
						cmn.DebugMsg(cmn.DbgLvlInfo, "Closed sourceChan")
					})
					return
				} // else, reset the timer
				ticker.Reset(30 * time.Second) // Reset the ticker to avoid tight loop
			}
		}
	}()

	// Function to refresh the last activity timestamp (from external functions)
	RefreshLastActivity := func() {
		LastActivity.Store(time.Now())
	}

	// Get engine name and if it has an ID at the end use it as a multiplier
	engineMultiplier := getEngineID()

	ramp := config.Crawler.InitialRampUp
	if ramp < 0 {
		// Calculate the ramp-up factor based on the number of sources and the number of Selenium instances
		if len(config.Selenium) > 0 {
			ramp = config.Crawler.MaxSources / len(config.Selenium)
		} else {
			ramp = 1 // fallback: minimal ramping
		}
	}
	cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG Pipeline] Ramp-up factor: %d (engine multiplier: %d)", ramp, engineMultiplier)

	// First batch load into the queue: (initial load)
	LastActivity.Store(time.Now()) // Reset activity
	for _, source := range *wb.sources {
		sourceChan <- source
	}
	LastActivity.Store(time.Now()) // Reset activity

	for vdiID := 0; vdiID < maxPipelines; vdiID++ {
		RefreshLastActivity() // Reset activity
		if ramp > 0 {
			PipelinesRunning.Store(true)
			if vdiID > 0 {
				// Sleep for a ramp-up time based on the VDI ID and the ramp factor
				_, _ = waitSomeTime(float64(ramp), RefreshLastActivity)
			}
		}

		batchWg.Add(1)

		go func(vdiSlot int) {
			defer batchWg.Done()

			var (
				currentStatusIdx *int
				starves          int // Counter for starvation
			)
			RefreshLastActivity() // Reset activity

			for {
				var source cdb.Source
				if !BatchCompleted.Load() {
					var ok bool
					// Fetch next available source in the queue:
					select {
					case source, ok = <-sourceChan:
						if !ok {
							// Channel is closed, let's check if we need to quit or not:
							cmn.DebugMsg(cmn.DbgLvlDebug2, "[DEBUG Pipeline] Batch completed, exiting goroutine for VDI slot %d", vdiSlot)
							return
						}
					default:
						// No source available right now
						starves++
						if starves > 5 {
							cmn.DebugMsg(cmn.DbgLvlDebug2, "[DEBUG Pipeline] No sources available for 5 iterations for VDI slot %d", vdiSlot)
							starves = 0 // Reset starvation counter
							// sleep 2 seconds and continue
							time.Sleep(2 * time.Second)
						} else {
							// short sleep to avoid tight loop
							time.Sleep(200 * time.Millisecond)
						}
						continue
					}
				} else {
					// Batch is completed, exit the goroutine
					cmn.DebugMsg(cmn.DbgLvlDebug2, "[DEBUG Pipeline] Batch completed, exiting goroutine for VDI slot %d", vdiSlot)
					return
				}
				starves = 0           // Reset starvation counter
				RefreshLastActivity() // Reset activity
				TotalSources.Add(1)   // Increment the total sources counter
				BusyInstances.Add(1)  // Increment busy instances counter

				// This makes sur we reuse always the same PipelineStatus index
				// for this goroutine instance:
				var statusIdx int
				// Getting a new status index must be protected by a lock
				statusLock.Lock()
				if currentStatusIdx == nil {
					// Get a new or available pipeline status index
					// (at startup or when extra data collections are ongoing and we need to move on to the next web collection)
					statusIdx = getAvailableOrNewPipelineStatus(wb)
				} else {
					// reuse current status index (we completed all types of collections)
					statusIdx = *currentStatusIdx
				}
				if statusIdx >= len(*wb.PipelineStatus) {
					// Safety check, if we are out of bounds, we need to append a new status
					*wb.PipelineStatus = append(*wb.PipelineStatus, crowler.Status{})
					if len(*wb.PipelineStatus) > maxPipelines {
						maxPipelines = len(*wb.PipelineStatus)
					}
				}

				now := time.Now()
				(*wb.PipelineStatus)[statusIdx] = crowler.Status{
					PipelineID:      uint64(statusIdx), //nolint:gosec // it's safe here.
					Source:          source.URL,
					SourceID:        source.ID,
					VDIID:           "",
					StartTime:       now,
					EndTime:         time.Time{},
					PipelineRunning: atomic.Int32{},
				}
				(*wb.PipelineStatus)[statusIdx].PipelineRunning.Store(1) // Set the pipeline running status
				statusLock.Unlock()

				cmn.DebugMsg(cmn.DbgLvlDebug2, "[DEBUG Pipeline] Received source: %s (ID: %d) for VDI slot %d on Pipeline: %d", source.URL, source.ID, vdiSlot, statusIdx)

				// Start crawling the website
				// startCrawling will spawn a crawling thread and return, so we need to wait for
				// that thread to complete:
				var crawlWG sync.WaitGroup
				startCrawling(wb, &crawlWG, source, statusIdx, RefreshLastActivity)
				crawlWG.Wait()

				status := &(*wb.PipelineStatus)[statusIdx]
				if status.NetInfoRunning.Load() == 1 || status.HTTPInfoRunning.Load() == 1 {
					currentStatusIdx = nil
				} else {
					currentStatusIdx = &statusIdx
				}
				RefreshLastActivity() // Reset activity
				BusyInstances.Add(-1) // Decrement busy instances counter
				if BusyInstances.Load() < 0 {
					BusyInstances.Store(0)
				}
			}
		}(vdiID)

		// Log the VDI instance started
		cmn.DebugMsg(cmn.DbgLvlDebug2, "[DEBUG Pipeline] Started VDI slot %d", vdiID)
	}

	RampUpRunning.Store(false) // Ramp-up phase is over
	batchWg.Wait()

	cmn.DebugMsg(cmn.DbgLvlInfo, "All sources in this batch have been crawled.")

	for idx := 0; idx < maxPipelines; idx++ {
		if idx < len(*wb.PipelineStatus) {
			(*wb.PipelineStatus)[idx].PipelineRunning.Store(0)
		}
	}

	return TotalSources.Load() // Return the total number of sources crawled
}

func waitSomeTime(delay float64, SessionRefresh func()) (time.Duration, error) {
	if delay < 1 {
		delay = 1
	}

	divider := math.Log10(delay+1) * 10 // Adjust multiplier as needed

	waitDuration := time.Duration(delay) * time.Second
	pollInterval := time.Duration(delay/divider) * time.Second // Check every pollInterval seconds to keep alive

	cmn.DebugMsg(cmn.DbgLvlDebug3, "[DEBUG-RampUp] Waiting for %v seconds...", delay)
	startTime := time.Now()
	for time.Since(startTime) < waitDuration {
		// Perform a controlled sleep so we can refresh the session timeout if needed
		time.Sleep(pollInterval)
		// refresh session timeout
		if SessionRefresh != nil {
			SessionRefresh()
		}
	}
	// Get the total delay
	totalDelay := time.Since(startTime)
	cmn.DebugMsg(cmn.DbgLvlDebug3, "[DEBUG-RampUp] Waited for %v seconds", totalDelay.Seconds())

	return totalDelay, nil
}

func monitorBatchAndRefill(wb *WorkBlock, need int) ([]cdb.Source, error) {
	if wb.sel.Available() <= 0 {
		return nil, nil
	}
	if need <= 0 {
		need = wb.sel.Available()
	}
	newSources, err := retrieveAvailableSources(wb.db, need)
	if err != nil {
		return nil, err
	}
	return newSources, nil
}

func getAvailableOrNewPipelineStatus(wb *WorkBlock) int {
	for idx := range *wb.PipelineStatus {
		status := &(*wb.PipelineStatus)[idx]
		if status.PipelineRunning.Load() != 1 && status.CrawlingRunning.Load() != 1 &&
			status.NetInfoRunning.Load() != 1 && status.HTTPInfoRunning.Load() != 1 {
			return idx //nolint:gosec // it's safe here.
		}
	}
	// All are busy or reserved → add a new one
	newIdx := len(*wb.PipelineStatus)
	*wb.PipelineStatus = append(*wb.PipelineStatus, crowler.Status{
		PipelineID: uint64(newIdx),
	})
	return newIdx
}

func startCrawling(wb *WorkBlock, wg *sync.WaitGroup, source cdb.Source, idx int, refresh func()) {
	if wg != nil {
		wg.Add(1)
	}

	// Prepare the go routine parameters
	args := crowler.Pars{
		WG:      wg,
		DB:      wb.db,
		Src:     source,
		Sel:     wb.sel,
		SelIdx:  0,
		RE:      wb.RulesEngine,
		Sources: wb.sources,
		Index:   uint64(idx),                  //nolint:gosec // it's safe here.
		Status:  &((*wb.PipelineStatus)[idx]), // Pointer to a single status element
		Refresh: refresh,
	}

	// Start a goroutine to crawl the website
	go func(args *crowler.Pars) {
		cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG startCrawling] Waiting for available VDI instance...")

		// Fetch the next available Selenium instance (VDI)
		//vdiInstance := <-*args.Sel
		vdiPool := args.Sel
		index, vdiInstance, err := vdiPool.Acquire()
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlWarn, "No VDI available right now: %v", err)
			return
		}
		cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG startCrawling] Acquired VDI instance: %v", vdiInstance.Config.Host)
		args.SelIdx = index // Update the index in the args

		// Assign VDI ID to the pipeline status
		(*args.Status).VDIID = vdiInstance.Config.Name

		// Create a channel that will signal when the VDI is no longer needed
		releaseVDI := make(chan vdi.SeleniumInstance, 1) // Make it buffered

		// Start the crawling in a separate goroutine
		go func() {
			crowler.CrawlWebsite(args, vdiInstance, releaseVDI)
		}()

		cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG startCrawling] Waiting for VDI release (after any crawling activities are completed)...")

		// Wait for `CrawlWebsite()` to release the VDI
		for {
			select {
			case recoveredVDI := <-releaseVDI:
				cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG startCrawling] VDI instance %v released for reuse", recoveredVDI.Config.Host)
				// Return the VDI instance to the pool
				vdiPool := args.Sel
				vdiPool.Release(index)
				cmn.DebugMsg(cmn.DbgLvlDebug, "[DEBUG startCrawling] quitting startCrawling() goroutine")
				return // Exit the loop once VDI is returned

			case <-time.After(10 * time.Minute): // Instead of quitting, just log
				cmn.DebugMsg(cmn.DbgLvlWarn, "[DEBUG startCrawling] VDI instance %v is still in use after 10 minutes, continuing to wait...", vdiInstance.Config.Host)
			}
		}
	}(&args)
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
		status := &(*PipelineStatus)[idx]
		if status.PipelineRunning.Load() == 0 {
			continue
		}
		runningPipelines++

		var totalRunningTime time.Duration
		if status.EndTime.IsZero() {
			totalRunningTime = time.Since(status.StartTime)
		} else {
			totalRunningTime = status.EndTime.Sub(status.StartTime)
		}
		totalLinksToGo := status.TotalLinks.Load() - (status.TotalPages.Load() + status.TotalSkipped.Load() + status.TotalDuplicates.Load())
		if totalLinksToGo < 0 {
			totalLinksToGo = 0
		}
		// Detect if we are stale-processing
		if (status.PipelineRunning.Load() == 1) && (totalRunningTime > time.Duration(2*time.Minute)) &&
			(status.CrawlingRunning.Load() == 0) && (status.NetInfoRunning.Load() == 0) &&
			(status.HTTPInfoRunning.Load() == 0) {
			// We are in a stale-processing state
			status.DetectedState.Store(1)
		} else {
			if (status.DetectedState.Load() & 0x01) != 0 {
				tmp := status.DetectedState.Load() & 0xfffe
				status.DetectedState.Store(tmp) // Reset the stale-processing state bit
			}
		}
		/*
			if (status.PipelineRunning == 1) && (status.CrawlingRunning > 1) &&
				(status.NetInfoRunning > 1) && (status.HTTPInfoRunning > 1) {
				status.PipelineRunning = status.CrawlingRunning | status.NetInfoRunning | status.HTTPInfoRunning
			}
		*/

		// Prepare the report (pipelineID + 1 is to avoid the pipeline 0 which seems to confuse many people)
		report += fmt.Sprintf("               Pipeline: %d of %d\n", status.PipelineID+1, len(*PipelineStatus))
		report += fmt.Sprintf("                 Source: %s\n", status.Source)
		report += fmt.Sprintf("              Source ID: %d\n", status.SourceID)
		report += fmt.Sprintf("                 VDI ID: %s\n", status.VDIID)
		report += fmt.Sprintf("        Pipeline status: %s\n", StatusStr(int(status.PipelineRunning.Load())))
		report += fmt.Sprintf("        Crawling status: %s\n", StatusStr(int(status.CrawlingRunning.Load())))
		report += fmt.Sprintf("         NetInfo status: %s\n", StatusStr(int(status.NetInfoRunning.Load())))
		report += fmt.Sprintf("        HTTPInfo status: %s\n", StatusStr(int(status.HTTPInfoRunning.Load())))
		report += fmt.Sprintf("           Running Time: %s\n", totalRunningTime)
		report += fmt.Sprintf("    Total Crawled Pages: %d\n", status.TotalPages.Load())
		report += fmt.Sprintf("           Total Errors: %d\n", status.TotalErrors.Load())
		report += fmt.Sprintf("  Total Collected Links: %d\n", status.TotalLinks.Load())
		report += fmt.Sprintf("    Total Skipped Links: %d\n", status.TotalSkipped.Load())
		report += fmt.Sprintf(" Total Duplicated Links: %d\n", status.TotalDuplicates.Load())
		report += fmt.Sprintf("Total Links to complete: %d\n", totalLinksToGo)
		report += fmt.Sprintf("          Total Scrapes: %d\n", status.TotalScraped.Load())
		report += fmt.Sprintf("          Total Actions: %d\n", status.TotalActions.Load())
		report += fmt.Sprintf("        Last Page Retry: %d\n", status.LastRetry.Load())
		report += fmt.Sprintf("         Last Page Wait: %f\n", status.LastWait)
		report += fmt.Sprintf("        Last Page Delay: %f\n", status.LastDelay)
		report += fmt.Sprintf("       Collection State: %s\n", CollectionState(int(status.DetectedState.Load())))
		report += sepPLine + "\n"

		// Update the metrics
		updateMetrics(status)

		// If the pipeline is completed or errored, delete metrics from Pushgateway
		if status.PipelineRunning.Load() == 2 || status.PipelineRunning.Load() == 3 {
			// Delete before resetting
			deleteMetricsFor(status)
		}

		// Reset the status if the pipeline has completed (display only the last report)
		if status.PipelineRunning.Load() >= 2 {
			status.PipelineRunning.Store(0)
		}
	}
	report += sepRLine + "\n"
	engine := cmn.GetMicroServiceName()
	labels := prometheus.Labels{
		"engine": engine,
	}
	totalPipelinesRunning.With(labels).Set(float64(runningPipelines))
	if runningPipelines > 0 {
		cmn.DebugMsg(cmn.DbgLvlInfo, report)
	}
}

var (
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
	totalPipelinesRunning = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "crowler_total_pipelines_running",
			Help: "Total number of pipelines currently running.",
		},
		[]string{"engine"},
	)

	gaugeTotalPages      = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "crowler_total_pages"}, []string{"engine", "pipeline_id", "source"})
	gaugeTotalLinks      = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "crowler_total_links"}, []string{"engine", "pipeline_id", "source"})
	gaugeTotalSkipped    = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "crowler_total_skipped"}, []string{"engine", "pipeline_id", "source"})
	gaugeTotalDuplicates = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "crowler_total_duplicates"}, []string{"engine", "pipeline_id", "source"})
	gaugeTotalErrors     = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "crowler_total_errors"}, []string{"engine", "pipeline_id", "source"})
	gaugeTotalScraped    = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "crowler_total_scraped"}, []string{"engine", "pipeline_id", "source"})
	gaugeTotalActions    = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "crowler_total_actions"}, []string{"engine", "pipeline_id", "source"})
	gaugeTotalFuzzing    = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "crowler_total_fuzzing"}, []string{"engine", "pipeline_id", "source"})
	gaugeCurrentDepth    = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "crowler_current_depth"}, []string{"engine", "pipeline_id", "source"})
	gaugeLastRetry       = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "crowler_last_retry"}, []string{"engine", "pipeline_id", "source"})
	gaugeLastWait        = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "crowler_last_wait"}, []string{"engine", "pipeline_id", "source"})
	gaugeLastDelay       = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "crowler_last_delay"}, []string{"engine", "pipeline_id", "source"})
	gaugePipelineRunning = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "crowler_pipeline_running"}, []string{"engine", "pipeline_id", "source"})
	gaugeCrawlingRunning = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "crowler_crawling_running"}, []string{"engine", "pipeline_id", "source"})
	gaugeNetInfoRunning  = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "crowler_netinfo_running"}, []string{"engine", "pipeline_id", "source"})
	gaugeHTTPInfoRunning = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "crowler_httpinfo_running"}, []string{"engine", "pipeline_id", "source"})
	gaugeDetectedState   = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "crowler_detected_state"}, []string{"engine", "pipeline_id", "source"})
)

func deleteMetricsFor(status *crowler.Status) {
	if !config.Prometheus.Enabled {
		return
	}

	engine := cmn.GetMicroServiceName()
	pid := fmt.Sprintf("%d", status.PipelineID)
	url := "http://" + config.Prometheus.Host + ":" + strconv.Itoa(config.Prometheus.Port)

	// Job only; no grouping labels
	if err := push.New(url, "crowler_engine").Delete(); err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Could not delete metrics for engine=%s pipeline=%s: %v", engine, pid, err)
	} else {
		cmn.DebugMsg(cmn.DbgLvlInfo, "Metrics deleted for engine=%s pipeline=%s", engine, pid)
	}
}

func updateMetrics(status *crowler.Status) {
	if !config.Prometheus.Enabled {
		return
	}

	engine := cmn.GetMicroServiceName()
	pid := fmt.Sprintf("%d", status.PipelineID)
	url := "http://" + config.Prometheus.Host + ":" + strconv.Itoa(config.Prometheus.Port)

	labels := prometheus.Labels{
		"engine":      engine,
		"pipeline_id": pid,
		"source":      status.Source,
	}

	// Update all metrics from Status
	gaugeTotalPages.With(labels).Set(float64(status.TotalPages.Load()))
	gaugeTotalLinks.With(labels).Set(float64(status.TotalLinks.Load()))
	gaugeTotalSkipped.With(labels).Set(float64(status.TotalSkipped.Load()))
	gaugeTotalDuplicates.With(labels).Set(float64(status.TotalDuplicates.Load()))
	gaugeTotalErrors.With(labels).Set(float64(status.TotalErrors.Load()))
	gaugeTotalScraped.With(labels).Set(float64(status.TotalScraped.Load()))
	gaugeTotalActions.With(labels).Set(float64(status.TotalActions.Load()))
	gaugeTotalFuzzing.With(labels).Set(float64(status.TotalFuzzing.Load()))
	gaugeCurrentDepth.With(labels).Set(float64(status.CurrentDepth.Load()))
	gaugeLastRetry.With(labels).Set(float64(status.LastRetry.Load()))
	gaugeLastWait.With(labels).Set(status.LastWait)
	gaugeLastDelay.With(labels).Set(status.LastDelay)
	gaugePipelineRunning.With(labels).Set(float64(status.PipelineRunning.Load()))
	gaugeCrawlingRunning.With(labels).Set(float64(status.CrawlingRunning.Load()))
	gaugeNetInfoRunning.With(labels).Set(float64(status.NetInfoRunning.Load()))
	gaugeHTTPInfoRunning.With(labels).Set(float64(status.HTTPInfoRunning.Load()))
	gaugeDetectedState.With(labels).Set(float64(status.DetectedState.Load()))

	// Prepare push collector (NO grouping labels)
	p := push.New(url, "crowler_engine").
		Collector(gaugeTotalPages).
		Collector(gaugeTotalLinks).
		Collector(gaugeTotalSkipped).
		Collector(gaugeTotalDuplicates).
		Collector(gaugeTotalErrors).
		Collector(gaugeTotalScraped).
		Collector(gaugeTotalActions).
		Collector(gaugeTotalFuzzing).
		Collector(gaugeCurrentDepth).
		Collector(gaugeLastRetry).
		Collector(gaugeLastWait).
		Collector(gaugeLastDelay).
		Collector(gaugePipelineRunning).
		Collector(gaugeCrawlingRunning).
		Collector(gaugeNetInfoRunning).
		Collector(gaugeHTTPInfoRunning).
		Collector(gaugeDetectedState).
		Collector(totalPipelinesRunning)

	// Push metrics
	if err := p.Push(); err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Could not push metrics: %v", err)
	} else {
		cmn.DebugMsg(cmn.DbgLvlInfo, "Metrics pushed for engine=%s pipeline=%s", engine, pid)
	}

	// Remove metrics when pipeline completes
	running := status.PipelineRunning.Load()
	if running == 2 || running == 3 {
		if err := push.New(url, "crowler_engine").Delete(); err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "Could not delete metrics: %v", err)
		} else {
			cmn.DebugMsg(cmn.DbgLvlInfo, "Metrics deleted for engine=%s pipeline=%s", engine, pid)
		}
	}
}

// StatusStr returns a string representation of the status
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

// CollectionState returns the comma separated collection states string
func CollectionState(condition int) string {
	csSTr := "Fully operational"
	if condition&0x01 != 0 {
		csSTr = "Stale-Processing detected!"
	}
	return csSTr
}

/* vdiInstances *chan vdi.SeleniumInstance */

func initAll(configFile *string, config *cfg.Config,
	db *cdb.Handler, vdiInstances *vdi.Pool,
	RulesEngine *rules.RuleEngine, lmt **rate.Limiter) error {
	var err error
	currentSysReady := getSysReady()
	setSysReady(1) // Indicate system is starting up or being restarted

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
	dbHandler = *db

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

	// Reinitialize the VDI instances available to this engine
	err = NewVDIPool(vdiInstances, config.Selenium)
	if err != nil {
		setSysReady(0) // Indicate system is NOT ready
		return fmt.Errorf("creating VDI pool: %s", err)
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

	setSysReady(currentSysReady) // Restore previous system ready state
	return nil
}

func main() {
	setSysReady(1) // Indicate system is starting

	// Reading command line arguments
	configFile = flag.String("config", "./config.yaml", "Path to the configuration file")
	flag.Parse()

	// Initialize the logger
	cmn.InitLogger("TheCROWler")
	cmn.DebugMsg(cmn.DbgLvlInfo, "The CROWler Engine is starting...")
	cmn.DebugMsg(cmn.DbgLvlInfo, "Node   ID: %s", cmn.GetEngineID())
	cmn.DebugMsg(cmn.DbgLvlInfo, "Node name: %s", cmn.GetMicroServiceName())

	// Define db before we set signal handlers
	var db cdb.Handler

	// Define sel before we set signal handlers
	//var vdiInstances chan vdi.SeleniumInstance
	var vdiInstances vdi.Pool

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
				closeResources(db, &vdiInstances) // Release resources
				os.Exit(0)

			case syscall.SIGTERM:
				// Handle SIGTERM
				cmn.DebugMsg(cmn.DbgLvlInfo, "SIGTERM received, shutting down...")
				closeResources(db, &vdiInstances) // Release resources
				os.Exit(0)

			case syscall.SIGQUIT:
				// Handle SIGQUIT
				cmn.DebugMsg(cmn.DbgLvlInfo, "SIGQUIT received, shutting down...")
				closeResources(db, &vdiInstances) // Release resources
				os.Exit(0)

			case syscall.SIGHUP:
				// Handle SIGHUP
				cmn.DebugMsg(cmn.DbgLvlInfo, "SIGHUP received, will reload configuration as soon as all pending jobs are completed...")
				configMutex.Lock()
				err := initAll(configFile, &config, &db, &vdiInstances, &GRulesEngine, &limiter)
				if err != nil {
					configMutex.Unlock()
					cmn.DebugMsg(cmn.DbgLvlFatal, "initializing the crawler: %v", err)
				}
				// Connect to the database
				err = db.Connect(config)
				if err != nil {
					configMutex.Unlock()
					closeResources(db, &vdiInstances) // Release resources
					cmn.DebugMsg(cmn.DbgLvlFatal, "connecting to the database: %v", err)
				}
				cmn.DebugMsg(cmn.DbgLvlInfo, "Database connection re-established.")
				configMutex.Unlock()
				cmn.DebugMsg(cmn.DbgLvlInfo, "Configuration reloaded.")
				//go checkSources(&db, vdiInstances)
			}
		}
	}()

	// Initialize the crawler
	err := initAll(configFile, &config, &db, &vdiInstances, &GRulesEngine, &limiter)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlFatal, "initializing the crawler: %v", err)
	}

	// Check how many VDI slots do we have:
	cmn.DebugMsg(cmn.DbgLvlInfo, "VDI slots available: %d", vdiInstances.Size())

	// Connect to the database
	err = db.Connect(config)
	if err != nil {
		closeResources(db, &vdiInstances) // Release resources
		cmn.DebugMsg(cmn.DbgLvlFatal, "connecting to the database: %v", err)
	}
	cmn.DebugMsg(cmn.DbgLvlInfo, "Database connection established.")
	defer closeResources(db, &vdiInstances)

	// Start events listener
	go cdb.ListenForEvents(&db, handleNotification)

	// Start the checkSources function in a goroutine
	cmn.DebugMsg(cmn.DbgLvlInfo, "Starting processing data (if any)...")
	go checkSources(&db, &vdiInstances, &GRulesEngine)

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

	cmn.DebugMsg(cmn.DbgLvlInfo, "System time: '%v'", time.Now())
	cmn.DebugMsg(cmn.DbgLvlInfo, "Local location: '%s'", time.Local.String())
	cmn.DebugMsg(cmn.DbgLvlInfo, "Engine Name: '%s'", cmn.GetMicroServiceName())

	cmn.DebugMsg(cmn.DbgLvlInfo, "Starting server on %s:%d", config.Crawler.Control.Host, config.Crawler.Control.Port)
	var rStatus error
	if strings.ToLower(strings.TrimSpace(config.Crawler.Control.SSLMode)) == cmn.EnableStr {
		setSysReady(2) // Indicate system is ready
		rStatus = srv.ListenAndServeTLS(config.API.CertFile, config.API.KeyFile)
	} else {
		setSysReady(2) // Indicate system is ready
		rStatus = srv.ListenAndServe()
	}
	setSysReady(0) // Indicate system is NOT ready
	statusMsg := "Server stopped."
	if rStatus != nil {
		statusMsg = fmt.Sprintf("Server stopped with error: %v", rStatus)
	}
	cmn.DebugMsg(cmn.DbgLvlFatal, statusMsg)
}

func handleNotification(payload string) {
	var event cdb.Event
	err := json.Unmarshal([]byte(payload), &event)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Failed to decode notification payload: %v", err)
		return
	}

	// Log the event for debug purposes
	cmn.DebugMsg(cmn.DbgLvlDebug, "New Event Received of type '%s': %+v", strings.ToLower(strings.TrimSpace(event.Type)), event)

	// Process the Event
	processEvent(event)
}

func processEvent(event cdb.Event) {
	eventType := strings.ToLower(strings.TrimSpace(event.Type))
	//cmn.DebugMsg(cmn.DbgLvlDebug4, "Processing event of type: %s", eventType)
	switch eventType {
	case "crowler_heartbeat":
		// Heartbeat event
		processHeartbeatEvent(event)
	case "system_event":
		// System event
		processSystemEvent(event)
	default:
		// Ignore event
		cmn.DebugMsg(cmn.DbgLvlDebug5, "Ignoring event, not interested in this type: %s", event.Type)
	}
}

func processSystemEvent(event cdb.Event) {
	// check if event.Details contains a tag "action" and process it
	if event.Details["action"] == nil {
		cmn.DebugMsg(cmn.DbgLvlDebug5, "Ignoring event, no action specified.")
		return
	}

	action := event.Details["action"].(string)
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "update_debug_level":
		// Check if there is a tag "level" and process it
		if event.Details["level"] == nil {
			cmn.DebugMsg(cmn.DbgLvlDebug5, "Ignoring event, no level specified.")
			return
		}
		// Update the debug level
		newLevel := event.Details["level"].(string)
		// Convert newLevel to a DebugLevel
		newLevel = strings.ToLower(newLevel)
		go updateDebugLevel(newLevel)
	default:
		// Ignore event
		cmn.DebugMsg(cmn.DbgLvlDebug5, "Ignoring event, not interested in this action: %s", action)
	}
}

// processHeartbeatEvent will generate a new event with a response which contains the current pipeline status
func processHeartbeatEvent(event cdb.Event) {
	//cmn.DebugMsg(cmn.DbgLvlDebug4, "Processing heartbeat event: %+v", event)

	// Prepare the response event
	now := time.Now()
	responseEvent := cdb.Event{
		Type:      "crowler_heartbeat_response",
		Severity:  "crowler_system_info",
		Timestamp: now.Format(time.RFC3339),
		ExpiresAt: now.Add(1 * time.Minute).Format(time.RFC3339),
		Details:   make(map[string]interface{}),
	}
	responseEvent.Details["parent_event_id"] = event.ID
	responseEvent.Details["origin_type"] = "crowler-engine"
	responseEvent.Details["origin_name"] = cmn.GetMicroServiceName()
	responseEvent.Details["origin_time"] = now.Format(time.RFC3339)
	responseEvent.Details["status"] = "ok"
	responseEvent.Details["type"] = "heartbeat_response"
	// Add the current pipeline status to the response event
	responseEvent.Details["pipeline_status"] = pipelineStatusJSON(sysPipelineStatus)

	responseEvent.Action = "new"

	// Send the response event to the database
	createEvent(dbHandler, responseEvent, 1)
}

func pipelineStatusJSON(PipelineStatus *[]crowler.Status) []PipelineStatusReport {
	reports := make([]PipelineStatusReport, 0, len(*PipelineStatus))

	for idx := 0; idx < len(*PipelineStatus); idx++ {
		status := &(*PipelineStatus)[idx]

		// Skip non running
		if status.PipelineRunning.Load() == 0 {
			continue
		}

		var totalRunningTime time.Duration
		if status.EndTime.IsZero() {
			totalRunningTime = time.Since(status.StartTime)
		} else {
			totalRunningTime = status.EndTime.Sub(status.StartTime)
		}

		totalLinksToGo := status.TotalLinks.Load() -
			(status.TotalPages.Load() + status.TotalSkipped.Load() + status.TotalDuplicates.Load())
		if totalLinksToGo < 0 {
			totalLinksToGo = 0
		}

		// Stale detection (same logic as in logStatus)
		if (status.PipelineRunning.Load() == 1) &&
			(totalRunningTime > 2*time.Minute) &&
			(status.CrawlingRunning.Load() == 0) &&
			(status.NetInfoRunning.Load() == 0) &&
			(status.HTTPInfoRunning.Load() == 0) {

			status.DetectedState.Store(1)
		} else {
			if (status.DetectedState.Load() & 0x01) != 0 {
				tmp := status.DetectedState.Load() & 0xfffe
				status.DetectedState.Store(tmp)
			}
		}

		reports = append(reports, PipelineStatusReport{
			PipelineID:      status.PipelineID + 1,
			Source:          status.Source,
			SourceID:        status.SourceID,
			VDIID:           status.VDIID,
			PipelineStatus:  StatusStr(int(status.PipelineRunning.Load())),
			CrawlingStatus:  StatusStr(int(status.CrawlingRunning.Load())),
			NetInfoStatus:   StatusStr(int(status.NetInfoRunning.Load())),
			HTTPInfoStatus:  StatusStr(int(status.HTTPInfoRunning.Load())),
			RunningTime:     totalRunningTime.String(),
			TotalPages:      int64(status.TotalPages.Load()),
			TotalErrors:     int64(status.TotalErrors.Load()),
			TotalLinks:      int64(status.TotalLinks.Load()),
			TotalSkipped:    int64(status.TotalSkipped.Load()),
			TotalDuplicates: int64(status.TotalDuplicates.Load()),
			TotalLinksToGo:  int64(totalLinksToGo),
			TotalScrapes:    int64(status.TotalScraped.Load()),
			TotalActions:    int64(status.TotalActions.Load()),
			LastRetry:       int64(status.LastRetry.Load()),
			LastWait:        status.LastWait,
			LastDelay:       status.LastDelay,
			CollectionState: CollectionState(int(status.DetectedState.Load())),
		})

		if status.PipelineRunning.Load() >= 2 {
			status.PipelineRunning.Store(0)
		}
	}

	return reports
}

func updateDebugLevel(newLevel string) {
	// Get configuration lock
	configMutex.Lock()
	defer configMutex.Unlock()

	var dbgLvl cmn.DbgLevel
	switch newLevel {
	case "debug":
		config.DebugLevel = 1
		cmn.SetDebugLevelFromString("debug")
	case "debug1":
		config.DebugLevel = 1
		cmn.SetDebugLevelFromString("debug1")
	case "debug2":
		config.DebugLevel = 2
		cmn.SetDebugLevelFromString("debug2")
	case "debug3":
		config.DebugLevel = 3
		cmn.SetDebugLevelFromString("debug3")
	case "debug4":
		config.DebugLevel = 4
		cmn.SetDebugLevelFromString("debug4")
	case "debug5":
		config.DebugLevel = 5
		cmn.SetDebugLevelFromString("debug5")
	case "info":
		config.DebugLevel = 0
		cmn.SetDebugLevelFromString("info")
	default:
		cmn.DebugMsg(cmn.DbgLvlDebug5, "Ignoring event, invalid debug level specified: %s", newLevel)
		return
	}

	// Update the debug level
	dbgLvl = cmn.DbgLevel(config.DebugLevel)
	cmn.SetDebugLevel(dbgLvl)
	cmn.DebugMsg(cmn.DbgLvlInfo, "Debug level updated to: %d", cmn.GetDebugLevel())
}

// initAPIv1 initializes the API v1 handlers
func initAPIv1() {
	// Health check
	healthCheckWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(healthCheckHandler)))
	readyCheckWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(readyCheckHandler)))

	http.Handle("/v1/health", healthCheckWithMiddlewares)
	http.Handle("/v1/health/", healthCheckWithMiddlewares)
	http.Handle("/v1/ready", readyCheckWithMiddlewares)
	http.Handle("/v1/ready/", readyCheckWithMiddlewares)

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

func healthCheckHandler(w http.ResponseWriter, _ *http.Request) {
	// Create a JSON document with the health status
	healthStatus := HealthCheck{
		Status: "OK",
	}

	// Respond with the health status
	handleErrorAndRespond(w, nil, healthStatus, "Error in health Check: ", http.StatusInternalServerError, http.StatusOK)
}

func readyCheckHandler(w http.ResponseWriter, _ *http.Request) {
	sysReadyMtx.RLock()
	defer sysReadyMtx.RUnlock()

	msg := ""
	switch sysReady {
	case 1: // Starting up
		msg = "STARTING UP"
	case 2: // Ready
		msg = "READY"
	default:
		msg = "NOT READY"
	}

	// Create a JSON document with the readiness status
	readyStatus := ReadyCheck{
		Status: msg,
	}

	// Respond with the readiness status
	handleErrorAndRespond(w, nil, readyStatus, "Error in ready Check: ", http.StatusInternalServerError, http.StatusOK)
}

func configCheckHandler(w http.ResponseWriter, _ *http.Request) {
	// Make a copy of the configuration and remove the sensitive data
	configCopy := config
	configCopy.Database.Password = "********"

	// Respond with the configuration
	handleErrorAndRespond(w, nil, configCopy, "Error in configuration Check: ", http.StatusInternalServerError, http.StatusOK)
}

// sel chan vdi.SeleniumInstance
func closeResources(db cdb.Handler, sel *vdi.Pool) {
	// Close the database connection
	if db != nil {
		err := db.Close()
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "closing database connection: %v", err)
		} else {
			cmn.DebugMsg(cmn.DbgLvlInfo, "Database connection closed.")
		}
	}
	// Stop the Selenium services
	(*sel).StopAll()
	/*
		if sel != nil {
			close(sel)
		}
		for seleniumInstance := range sel {
			if seleniumInstance.Service != nil {
				err := seleniumInstance.Service.Stop()
				if err != nil {
					cmn.DebugMsg(cmn.DbgLvlError, "stopping Selenium instance: %v", err)
				}
			}
		}
	*/
	cmn.DebugMsg(cmn.DbgLvlInfo, "All services stopped.")
}
