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

// Package main (API) implements the API server for the Crowler search engine.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	plg "github.com/pzaino/thecrowler/pkg/plugin"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/push"
	"golang.org/x/time/rate"
)

const (
	errTooManyRequests = "Too Many Requests"
	errRateLimitExceed = "Rate limit exceeded"
)

// Create a rate limiter for your application. Adjust the parameters as needed.
var (
	limiter     *rate.Limiter
	configMutex sync.Mutex
	configFile  *string
	dbSemaphore chan struct{} // Semaphore for the database connection
	dbHandler   cdb.Handler

	sysReadyMtx sync.RWMutex // Mutex to protect the SysReady variable
	sysReady    int          // System readiness status variable 0 = not ready, 1 = starting up, 2 = ready

	// Counters for monitoring (atomic)
	totalRequests atomic.Int64
	totalErrors   atomic.Int64
	totalSuccess  atomic.Int64

	apiPlugins *plg.JSPluginRegister
)

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

func initAll(configFile *string, config *cfg.Config, lmt **rate.Limiter) error {
	// Reading the configuration file
	var err error
	currentSysReady := getSysReady()
	setSysReady(1) // Indicate system is starting up or being restarted

	*config, err = cfg.LoadConfig(*configFile)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlFatal, "Error reading config file: %v", err)
	}
	if cfg.IsEmpty(*config) {
		cmn.DebugMsg(cmn.DbgLvlFatal, "Config file is empty")
	}

	// Set the OS variable
	config.OS = runtime.GOOS

	// Set the rate limiter
	var rl, bl int
	if strings.TrimSpace(config.API.RateLimit) == "" {
		config.API.RateLimit = "10,10"
	}
	if !strings.Contains(config.API.RateLimit, ",") {
		config.API.RateLimit = config.API.RateLimit + ",10"
	}
	rlStr := strings.Split(config.API.RateLimit, ",")[0]
	if rlStr == "" {
		rlStr = "10"
	}
	rl, err = strconv.Atoi(rlStr)
	if err != nil {
		rl = 10
	}
	blStr := strings.Split(config.API.RateLimit, ",")[1]
	if blStr == "" {
		blStr = "10"
	}
	bl, err = strconv.Atoi(blStr)
	if err != nil {
		bl = 10
	}
	*lmt = rate.NewLimiter(rate.Limit(rl), bl)

	// Set the database semaphore
	dbSemaphore = make(chan struct{}, config.Database.MaxConns-3)

	// Initialize the database
	cmn.DebugMsg(cmn.DbgLvlInfo, "Initializing database connection...")
	connected := false
	dbHandler, err = cdb.NewHandler(*config)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error allocating database resources: %v", err)
	} else {
		for !connected {
			err = dbHandler.Connect(*config)
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "Error opening database connection: %v", err)
				time.Sleep(5 * time.Second)
				continue
			}
			connected = true
		}
		cmn.DebugMsg(cmn.DbgLvlInfo, "Database connection established")
	}

	// Reload plugins
	apiPlugins = plg.NewJSPluginRegister().
		LoadPluginsFromConfig(config, "api_plugin")

	setSysReady(currentSysReady) // Restore previous system ready state
	return nil
}

func main() {
	setSysReady(1) // Indicate system is starting

	// Parse the command line arguments
	configFile = flag.String("config", "./config.yaml", "Path to the configuration file")
	flag.Parse()

	// Initialize the logger
	cmn.InitLogger("TheCROWlerAPI")
	cmn.DebugMsg(cmn.DbgLvlInfo, "The CROWler API is starting...")

	// Setting up a channel to listen for termination signals
	cmn.DebugMsg(cmn.DbgLvlInfo, "Setting up termination signals listener...")
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT, syscall.SIGHUP)

	// Define signal handling
	go func() {
		for {
			sig := <-signals
			switch sig {
			case syscall.SIGINT:
				// Handle SIGINT (Ctrl+C)
				cmn.DebugMsg(cmn.DbgLvlInfo, "SIGINT received, shutting down...")
				os.Exit(0)

			case syscall.SIGTERM:
				// Handle SIGTERM
				cmn.DebugMsg(cmn.DbgLvlInfo, "SIGTERM received, shutting down...")
				updateMetrics()
				os.Exit(0)

			case syscall.SIGQUIT:
				// Handle SIGQUIT
				cmn.DebugMsg(cmn.DbgLvlInfo, "SIGQUIT received, shutting down...")
				updateMetrics()
				os.Exit(0)

			case syscall.SIGHUP:
				// Handle SIGHUP
				cmn.DebugMsg(cmn.DbgLvlInfo, "SIGHUP received, will reload configuration as soon as all pending jobs are completed...")
				updateMetrics()
				configMutex.Lock()
				err := initAll(configFile, &config, &limiter)
				if err != nil {
					configMutex.Unlock()
					cmn.DebugMsg(cmn.DbgLvlFatal, "Error initializing the crawler: %v", err)
				}
				configMutex.Unlock()
			}
		}
	}()

	// Initialize the configuration
	err := initAll(configFile, &config, &limiter)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlFatal, "Error initializing the crawler: %v", err)
		os.Exit(-1)
	}

	srv := &http.Server{
		Addr: config.API.Host + ":" + fmt.Sprintf("%d", config.API.Port),

		// ReadHeaderTimeout is the amount of time allowed to read
		// request headers. The connection's read deadline is reset
		// after reading the headers and the Handler can decide what
		// is considered too slow for the body. If ReadHeaderTimeout
		// is zero, the value of ReadTimeout is used. If both are
		// zero, there is no timeout.
		ReadHeaderTimeout: time.Duration(config.API.ReadHeaderTimeout) * time.Second,

		// ReadTimeout is the maximum duration for reading the entire
		// request, including the body. A zero or negative value means
		// there will be no timeout.
		//
		// Because ReadTimeout does not let Handlers make per-request
		// decisions on each request body's acceptable deadline or
		// upload rate, most users will prefer to use
		// ReadHeaderTimeout. It is valid to use them both.
		ReadTimeout: time.Duration(config.API.ReadTimeout) * time.Second,

		// WriteTimeout is the maximum duration before timing out
		// writes of the response. It is reset whenever a new
		// request's header is read. Like ReadTimeout, it does not
		// let Handlers make decisions on a per-request basis.
		// A zero or negative value means there will be no timeout.
		WriteTimeout: time.Duration(config.API.WriteTimeout) * time.Second,

		// IdleTimeout is the maximum amount of time to wait for the
		// next request when keep-alive are enabled. If IdleTimeout
		// is zero, the value of ReadTimeout is used. If both are
		// zero, there is no timeout.
		IdleTimeout: time.Duration(config.API.Timeout) * time.Second,
	}

	runtime.GOMAXPROCS(runtime.NumCPU())

	// Set the handlers
	initAPIv1()

	// ---------------------------------------------------------
	// Start Event Listener (Search API participates in heartbeats)
	// ---------------------------------------------------------
	go cdb.ListenForEvents(&dbHandler, handleNotification)

	// ---------------------------------------------------------
	// Start Prometheus metrics updater
	// ---------------------------------------------------------
	if config.Prometheus.Enabled {
		// Init immediate metrics update
		updateMetrics()
		// Start periodic metrics update
		go func() {
			ticker := time.NewTicker(15 * time.Second)
			defer ticker.Stop()

			for range ticker.C {
				updateMetrics()
			}
		}()
	}

	cmn.DebugMsg(cmn.DbgLvlInfo, "System time: '%v'", time.Now())
	cmn.DebugMsg(cmn.DbgLvlInfo, "Local location: '%v'", time.Local.String())

	cmn.DebugMsg(cmn.DbgLvlInfo, "Starting server on %s:%d", config.API.Host, config.API.Port)
	cmn.DebugMsg(cmn.DbgLvlInfo, "Awaiting for requests...")
	if strings.ToLower(strings.TrimSpace(config.API.SSLMode)) == cmn.EnableStr {
		setSysReady(2) // Indicate system is ready
		cmn.DebugMsg(cmn.DbgLvlFatal, "Server return: %v", srv.ListenAndServeTLS(config.API.CertFile, config.API.KeyFile))
	} else {
		setSysReady(2) // Indicate system is ready
		cmn.DebugMsg(cmn.DbgLvlFatal, "Server return: %v", srv.ListenAndServe())
	}
	setSysReady(0) // Indicate system is NOT ready
}

// ---------------------------------------------------------
// Event Listener for the Search API
// ---------------------------------------------------------

func handleNotification(payload string) {
	var event cdb.Event
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "API: Failed to decode incoming event: %v", err)
		return
	}

	eventType := strings.ToLower(strings.TrimSpace(event.Type))
	cmn.DebugMsg(cmn.DbgLvlDebug3, "API: Received event of type '%s'", eventType)

	switch eventType {
	case "system_event":
		processSystemEvent(event)
	case "crowler_heartbeat":
		processHeartbeatEvent(event)
	default:
		// Ignore all other events
		cmn.DebugMsg(cmn.DbgLvlDebug5, "API: Ignoring event type '%s'", eventType)
	}
}

// ---------------------------------------------------------
// Heartbeat response logic for The CROWler Search API
// ---------------------------------------------------------

func processHeartbeatEvent(event cdb.Event) {
	// SearchAPI has a single pipeline with info, so let's respond accordingly
	pipelineResults := make([]any, 0)
	pipelineResults = append(pipelineResults, map[string]any{
		"pipeline_name":   "search_api_pipeline",
		"pipeline_status": "ready",
		"Total Requests":  totalRequests.Load(),
		"Total Errors":    totalErrors.Load(),
		"Total Success":   totalSuccess.Load(),
	})
	resp := cdb.Event{
		Type:      "crowler_heartbeat_response",
		Severity:  "crowler_system_info",
		Timestamp: time.Now().Format(time.RFC3339),
		Details: map[string]any{
			"parent_event_id": event.ID,
			"type":            "heartbeat_response",
			"status":          "ok",
			"origin_name":     cmn.GetMicroServiceName(),
			"origin_type":     "crowler-api",
			"origin_time":     time.Now().Format(time.RFC3339),
			"pipeline_status": pipelineResults,
		},
	}

	_, err := cdb.CreateEventWithRetries(&dbHandler, resp)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "API: Failed to send heartbeat response: %v", err)
		return
	}

	cmn.DebugMsg(cmn.DbgLvlDebug3, "API: Heartbeat response sent")
}

func processSystemEvent(event cdb.Event) {
	action, _ := event.Details["action"].(string)
	action = strings.ToLower(strings.TrimSpace(action))

	switch action {
	case "update_debug_level":
		if lv, ok := event.Details["level"].(string); ok {
			newLevel := strings.ToLower(strings.TrimSpace(lv))
			go updateDebugLevel(newLevel)
		}
	default:
		cmn.DebugMsg(cmn.DbgLvlDebug5, "API: Ignoring system_event action '%s'", action)
	}
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

// -------------------------------------------
// Handle Prometheus Push-Gateway Metrics
//--------------------------------------------

var (
	gaugeSearchTotalRequests = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "crowler_search_total_requests",
			Help: "Total number of search requests",
		},
		[]string{"engine"},
	)

	gaugeSearchTotalErrors = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "crowler_search_total_errors",
			Help: "Total number of search errors",
		},
		[]string{"engine"},
	)

	gaugeSearchTotalSuccess = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "crowler_search_total_success",
			Help: "Total number of successful searches",
		},
		[]string{"engine"},
	)
)

func init() {
	prometheus.MustRegister(
		gaugeSearchTotalRequests,
		gaugeSearchTotalErrors,
		gaugeSearchTotalSuccess,
	)
}

func updateMetrics() {
	if !config.Prometheus.Enabled {
		return
	}

	engine := cmn.GetMicroServiceName()
	url := "http://" + config.Prometheus.Host + ":" + strconv.Itoa(config.Prometheus.Port)

	labels := prometheus.Labels{
		"engine": engine,
	}

	gaugeSearchTotalRequests.With(labels).Set(float64(totalRequests.Load()))
	gaugeSearchTotalErrors.With(labels).Set(float64(totalErrors.Load()))
	gaugeSearchTotalSuccess.With(labels).Set(float64(totalSuccess.Load()))

	p := push.New(url, "crowler_search_api").
		Collector(gaugeSearchTotalRequests).
		Collector(gaugeSearchTotalErrors).
		Collector(gaugeSearchTotalSuccess)

	if err := p.Push(); err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "SearchAPI: Could not push metrics: %v", err)
	} else {
		cmn.DebugMsg(cmn.DbgLvlDebug3, "SearchAPI: Metrics pushed for engine=%s", engine)
	}
}

// -------------------------------------------
// API v1 Handlers and Middlewares
//--------------------------------------------

// initAPIv1 initializes the API v1 handlers
func initAPIv1() {
	// Health check
	healthCheckWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(healthCheckHandler)))
	readyCheckWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(readyCheckHandler)))

	http.Handle("/v1/health", healthCheckWithMiddlewares)
	http.Handle("/v1/health/", healthCheckWithMiddlewares)
	http.Handle("/v1/ready", readyCheckWithMiddlewares)
	http.Handle("/v1/ready/", readyCheckWithMiddlewares)

	// Query handlers
	http.Handle("/v1/search/general", withPublicMiddlewares(searchHandler))
	http.Handle("/v1/search/netinfo", withPublicMiddlewares(netInfoHandler))
	http.Handle("/v1/search/httpinfo", withPublicMiddlewares(httpInfoHandler))
	http.Handle("/v1/search/screenshot", withPublicMiddlewares(scrImgSrchHandler))
	http.Handle("/v1/search/webobject", withPublicMiddlewares(webObjectHandler))
	http.Handle("/v1/search/correlated_sites", withPublicMiddlewares(webCorrelatedSitesHandler))
	http.Handle("/v1/search/collected_data", withPublicMiddlewares(webScrapedDataHandler))

	if config.API.EnableConsole {
		addSourceHandlerWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(addSourceHandler)))
		removeSourceHandlerWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(removeSourceHandler)))
		updateSourceHandlerWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(updateSourceHandler)))
		vacuumSourceHandlerWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(vacuumSourceHandler)))
		singleURLstatusHandlerWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(singleURLstatusHandler)))
		allURLstatusHandlerWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(allURLstatusHandler)))

		http.Handle("/v1/source/add", addSourceHandlerWithMiddlewares)
		http.Handle("/v1/source/remove", removeSourceHandlerWithMiddlewares)
		http.Handle("/v1/source/update", updateSourceHandlerWithMiddlewares)
		http.Handle("/v1/source/vacuum", vacuumSourceHandlerWithMiddlewares)
		http.Handle("/v1/source/status", singleURLstatusHandlerWithMiddlewares)
		http.Handle("/v1/source/statuses", allURLstatusHandlerWithMiddlewares)

		// Owner endpoints
		http.Handle("/v1/owner/add", SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(addOwnerHandler))))
		http.Handle("/v1/owner/update", SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(updateOwnerHandler))))
		http.Handle("/v1/owner/remove", SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(removeOwnerHandler))))

		// Category endpoints
		http.Handle("/v1/category/add", SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(addCategoryHandler))))
		http.Handle("/v1/category/update", SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(updateCategoryHandler))))
		http.Handle("/v1/category/remove", SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(removeCategoryHandler))))
	}

	// Register API plugin routes
	registerAPIPluginRoutes(http.DefaultServeMux)
}

func registerAPIPluginRoutes(mux *http.ServeMux) {
	for _, name := range apiPlugins.Order {
		plugin := apiPlugins.Registry[name]
		api := plugin.API
		if api == nil {
			continue
		}

		for _, method := range api.Methods {
			handler := withAPIPluginMiddlewares(
				makeAPIPluginHandler(plugin, method),
			)

			mux.Handle(api.EndPoint, handler)
		}
	}
}

func withAPIPluginMiddlewares(h http.HandlerFunc) http.Handler {
	return RecoverMiddleware(
		SecurityHeadersMiddleware(
			RateLimitMiddleware(h),
		),
	)
}

func withPublicMiddlewares(h http.HandlerFunc) http.Handler {
	return RecoverMiddleware(
		CORSHeadersMiddleware(
			SecurityHeadersMiddleware(
				RateLimitMiddleware(h),
			),
		),
	)
}

// CORSHeadersMiddleware enables CORS for requests
func CORSHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// You can restrict this to specific domains in production
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		// For preflight requests
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// RecoverMiddleware recovers from panics and returns a 500 error
func RecoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "Recovered from panic: %v", rec)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// RateLimitMiddleware is a middleware for rate limiting
func RateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !limiter.Allow() {
			cmn.DebugMsg(cmn.DbgLvlDebug, errRateLimitExceed)
			http.Error(w, errTooManyRequests, http.StatusTooManyRequests)
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

// searchHandler handles the traditional search requests
func searchHandler(w http.ResponseWriter, r *http.Request) {
	select {
	case dbSemaphore <- struct{}{}: // Try to acquire a DB connection
		defer func() { <-dbSemaphore }() // Release the connection after the work is done

		successCode := http.StatusOK
		query, err := extractQueryOrBody(r)
		if err != nil {
			handleErrorAndRespond(w, err, nil, "Missing parameter 'q' in search request", http.StatusBadRequest, successCode)
			return
		}

		results, err := performSearch(query, &dbHandler)
		results.SetHeaderFields(
			"customsearch#search",
			jsonResponse,
			GetQueryTemplate("search", "v1", r.Method),
			[]QueryRequest{
				{
					"search",
					len(results.Items),
					query,
					len(results.Items),
					results.Queries.Offset,
					"utf8",
					"utf8",
					"off",
					"0",
				},
			},
		)
		if err != nil {
			totalErrors.Add(1)
		} else {
			totalSuccess.Add(1)
		}

		handleErrorAndRespond(w, err, results, "Error performing search: %v", http.StatusInternalServerError, successCode)
	case <-time.After(5 * time.Second): // Wait for a connection with timeout
		healthStatus := HealthCheck{
			Status: "DB is overloaded, please try again later",
		}
		totalErrors.Add(1)
		handleErrorAndRespond(w, nil, healthStatus, "", http.StatusTooManyRequests, http.StatusTooManyRequests)
	}
}

func webObjectHandler(w http.ResponseWriter, r *http.Request) {
	select {
	case dbSemaphore <- struct{}{}:
		defer func() { <-dbSemaphore }()

		successCode := http.StatusOK
		query, err := extractQueryOrBody(r)
		if err != nil {
			handleErrorAndRespond(w, err, nil, "Missing parameter 'q' in webobject search request", http.StatusBadRequest, successCode)
			return
		}

		results, err := performWebObjectSearch(query, getQTypeFromName(r.Method), &dbHandler)
		if results.IsEmpty() {
			var retCode int
			if config.API.Return404 {
				retCode = http.StatusNotFound
				totalErrors.Add(1)
			} else {
				retCode = successCode
				totalSuccess.Add(1)
			}
			handleErrorAndRespond(w, err, results, "Error performing webobject search: %v", http.StatusNotFound, retCode)
		} else {
			results.SetHeaderFields(
				"webobject#search",
				jsonResponse,
				GetQueryTemplate("webobject", "v1", r.Method),
				[]QueryRequest{
					{
						"search",
						len(results.Items),
						query,
						len(results.Items),
						results.Queries.Offset,
						"utf8",
						"utf8",
						"off",
						"0",
					},
				},
			)
			if err != nil {
				totalErrors.Add(1)
			} else {
				totalSuccess.Add(1)
			}
			handleErrorAndRespond(w, err, results, "Error performing webobject search: %v", http.StatusInternalServerError, successCode)
		}
	case <-time.After(5 * time.Second): // Wait for a connection with timeout
		healthStatus := HealthCheck{
			Status: "DB is overloaded, please try again later",
		}
		totalErrors.Add(1)
		handleErrorAndRespond(w, nil, healthStatus, "", http.StatusTooManyRequests, http.StatusTooManyRequests)
	}
}

func webCorrelatedSitesHandler(w http.ResponseWriter, r *http.Request) {
	select {
	case dbSemaphore <- struct{}{}:
		defer func() { <-dbSemaphore }()

		successCode := http.StatusOK
		query, err := extractQueryOrBody(r)
		if err != nil {
			handleErrorAndRespond(w, err, nil, "Missing parameter 'q' in correlatedsites search request", http.StatusBadRequest, successCode)
			return
		}

		results, err := performCorrelatedSitesSearch(query, getQTypeFromName(r.Method), &dbHandler)
		if results.IsEmpty() {
			var retCode int
			if config.API.Return404 {
				retCode = http.StatusNotFound
				totalErrors.Add(1)
			} else {
				retCode = successCode
				totalSuccess.Add(1)
			}
			handleErrorAndRespond(w, err, results, "Error performing correlatedsites search: %v", http.StatusNotFound, retCode)
		} else {
			results.SetHeaderFields(
				"correlatedsites#search",
				jsonResponse,
				GetQueryTemplate("correlatedsites", "v1", r.Method),
				[]QueryRequest{
					{
						"search",
						len(results.Items),
						query,
						len(results.Items),
						results.Queries.Offset,
						"utf8",
						"utf8",
						"off",
						"0",
					},
				},
			)
			if err != nil {
				totalErrors.Add(1)
			} else {
				totalSuccess.Add(1)
			}
			handleErrorAndRespond(w, err, results, "Error performing correlatedsites search: %v", http.StatusInternalServerError, successCode)
		}
	case <-time.After(5 * time.Second): // Wait for a connection with timeout
		healthStatus := HealthCheck{
			Status: "DB is overloaded, please try again later",
		}
		totalErrors.Add(1)
		handleErrorAndRespond(w, nil, healthStatus, "", http.StatusTooManyRequests, http.StatusTooManyRequests)
	}
}

// webScrapedDataHandler handles the search requests for scraped data
func webScrapedDataHandler(w http.ResponseWriter, r *http.Request) {
	select {
	case dbSemaphore <- struct{}{}:
		defer func() { <-dbSemaphore }()

		successCode := http.StatusOK
		query, err := extractQueryOrBody(r)
		if err != nil {
			handleErrorAndRespond(w, err, nil, "Missing parameter 'q' in scraped_data search request", http.StatusBadRequest, successCode)
			return
		}

		results, err := performScrapedDataSearch(query, getQTypeFromName(r.Method), &dbHandler)
		if results.IsEmpty() {
			var retCode int
			if config.API.Return404 {
				retCode = http.StatusNotFound
				totalErrors.Add(1)
			} else {
				retCode = successCode
				totalSuccess.Add(1)
			}
			handleErrorAndRespond(w, err, results, "Error performing scraped_data search: %v", http.StatusNotFound, retCode)
		} else {
			results.SetHeaderFields(
				"scraped_data#search",
				jsonResponse,
				GetQueryTemplate("scraped_data", "v1", r.Method),
				[]QueryRequest{
					{
						"search",
						len(results.Items),
						query,
						len(results.Items),
						results.Queries.Offset,
						"utf8",
						"utf8",
						"off",
						"0",
					},
				},
			)
			if err != nil {
				totalErrors.Add(1)
			} else {
				totalSuccess.Add(1)
			}
			handleErrorAndRespond(w, err, results, "Error performing correlatedsites search: %v", http.StatusInternalServerError, successCode)
		}
	case <-time.After(5 * time.Second): // Wait for a connection with timeout
		healthStatus := HealthCheck{
			Status: "DB is overloaded, please try again later",
		}
		totalErrors.Add(1)
		handleErrorAndRespond(w, nil, healthStatus, "", http.StatusTooManyRequests, http.StatusTooManyRequests)
	}
}

// scrImgSrchHandler handles the search requests for screenshot images
func scrImgSrchHandler(w http.ResponseWriter, r *http.Request) {
	select {
	case dbSemaphore <- struct{}{}:
		defer func() { <-dbSemaphore }()

		successCode := http.StatusOK
		query, err := extractQueryOrBody(r)
		if err != nil {
			handleErrorAndRespond(w, err, nil, "Missing parameter 'q' in screenshot search request", http.StatusBadRequest, successCode)
			return
		}

		results, err := performScreenshotSearch(query, getQTypeFromName(r.Method), &dbHandler)
		if results == (ScreenshotResponse{}) {
			var retCode int
			if config.API.Return404 {
				retCode = http.StatusNotFound
				totalErrors.Add(1)
			} else {
				retCode = successCode
				totalSuccess.Add(1)
			}
			handleErrorAndRespond(w, err, results, "Error performing screenshot search: %v", http.StatusInternalServerError, retCode)
		} else {
			if err != nil {
				totalErrors.Add(1)
			} else {
				totalSuccess.Add(1)
			}
			handleErrorAndRespond(w, err, results, "Error performing screenshot search: %v", http.StatusInternalServerError, successCode)
		}
	case <-time.After(5 * time.Second): // Wait for a connection with timeout
		healthStatus := HealthCheck{
			Status: "DB is overloaded, please try again later",
		}
		totalErrors.Add(1)
		handleErrorAndRespond(w, nil, healthStatus, "", http.StatusTooManyRequests, http.StatusTooManyRequests)
	}
}

// netInfoHandler handles the network information requests
func netInfoHandler(w http.ResponseWriter, r *http.Request) {
	select {
	case dbSemaphore <- struct{}{}:
		defer func() { <-dbSemaphore }()

		successCode := http.StatusOK
		query, err := extractQueryOrBody(r)
		if err != nil {
			handleErrorAndRespond(w, err, nil, "Missing parameter 'q' in netinfo search request", http.StatusBadRequest, successCode)
			return
		}

		results, err := performNetInfoSearch(query, getQTypeFromName(r.Method), &dbHandler)
		if results.isEmpty() {
			var retCode int
			if config.API.Return404 {
				retCode = http.StatusNotFound
				totalErrors.Add(1)
			} else {
				retCode = successCode
				totalSuccess.Add(1)
			}
			handleErrorAndRespond(w, err, results, "Error performing netinfo search: %v", http.StatusNotFound, retCode)
		} else {
			results.SetHeaderFields(
				"netinfo#search",
				jsonResponse,
				GetQueryTemplate("netinfo", "v1", r.Method),
				[]QueryRequest{
					{
						"search",
						len(results.Items),
						query,
						len(results.Items),
						results.Queries.Offset,
						"utf8",
						"utf8",
						"off",
						"0",
					},
				},
			)
			if err != nil {
				totalErrors.Add(1)
			} else {
				totalSuccess.Add(1)
			}
			handleErrorAndRespond(w, err, results, "Error performing netinfo search: %v", http.StatusInternalServerError, successCode)
		}
	case <-time.After(5 * time.Second): // Wait for a connection with timeout
		healthStatus := HealthCheck{
			Status: "DB is overloaded, please try again later",
		}
		totalErrors.Add(1)
		handleErrorAndRespond(w, nil, healthStatus, "", http.StatusTooManyRequests, http.StatusTooManyRequests)
	}
}

// httpInfoHandler handles the http information requests
func httpInfoHandler(w http.ResponseWriter, r *http.Request) {
	select {
	case dbSemaphore <- struct{}{}:
		defer func() { <-dbSemaphore }()

		successCode := http.StatusOK
		query, err := extractQueryOrBody(r)
		if err != nil {
			handleErrorAndRespond(w, err, nil, "Missing parameter 'q' in httpinfo search request", http.StatusBadRequest, successCode)
			return
		}

		results, err := performHTTPInfoSearch(query, getQTypeFromName(r.Method), &dbHandler)
		if results.IsEmpty() {
			var retCode int
			if config.API.Return404 {
				retCode = http.StatusNotFound
				totalErrors.Add(1)
			} else {
				retCode = successCode
				totalSuccess.Add(1)
			}
			handleErrorAndRespond(w, err, results, "Error performing httpinfo search: %v", http.StatusNotFound, retCode)
		} else {
			results.SetHeaderFields(
				"httpinfo#search",
				jsonResponse,
				GetQueryTemplate("httpinfo", "v1", r.Method),
				[]QueryRequest{
					{
						"search",
						len(results.Items),
						query,
						len(results.Items),
						results.Queries.Offset,
						"utf8",
						"utf8",
						"off",
						"0",
					},
				},
			)
			if err != nil {
				totalErrors.Add(1)
			} else {
				totalSuccess.Add(1)
			}
			handleErrorAndRespond(w, err, results, "Error performing httpinfo search: %v", http.StatusInternalServerError, successCode)
		}
	case <-time.After(5 * time.Second): // Wait for a connection with timeout
		healthStatus := HealthCheck{
			Status: "DB is overloaded, please try again later",
		}
		totalErrors.Add(1)
		handleErrorAndRespond(w, nil, healthStatus, "", http.StatusTooManyRequests, http.StatusTooManyRequests)
	}
}

// addSourceHandler handles the addition of new sources
func addSourceHandler(w http.ResponseWriter, r *http.Request) {
	select {
	case dbSemaphore <- struct{}{}:
		defer func() { <-dbSemaphore }()

		successCode := http.StatusCreated
		query, err := extractQueryOrBody(r)
		if err != nil {
			handleErrorAndRespond(w, err, nil, "Missing parameter 'q' in addSource request", http.StatusBadRequest, successCode)
			return
		}

		results, err := performAddSource(query, getQTypeFromName(r.Method), &dbHandler)
		if err != nil {
			totalErrors.Add(1)
		} else {
			totalSuccess.Add(1)
		}
		handleErrorAndRespond(w, err, results, "Error performing addSource: %v", http.StatusInternalServerError, successCode)
	case <-time.After(5 * time.Second): // Wait for a connection with timeout
		healthStatus := HealthCheck{
			Status: "DB is overloaded, please try again later",
		}
		totalErrors.Add(1)
		handleErrorAndRespond(w, nil, healthStatus, "", http.StatusTooManyRequests, http.StatusTooManyRequests)
	}
}

// removeSourceHandler handles the removal of sources
func removeSourceHandler(w http.ResponseWriter, r *http.Request) {
	select {
	case dbSemaphore <- struct{}{}:
		defer func() { <-dbSemaphore }()

		successCode := http.StatusNoContent
		query, err := extractQueryOrBody(r)
		if err != nil {
			handleErrorAndRespond(w, err, nil, "Missing parameter 'q' in removeSource request", http.StatusBadRequest, successCode)
			return
		}

		results, err := performRemoveSource(query, getQTypeFromName(r.Method), &dbHandler)
		if err != nil {
			totalErrors.Add(1)
		} else {
			totalSuccess.Add(1)
		}
		handleErrorAndRespond(w, err, results, "Error performing removeSource: %v", http.StatusInternalServerError, successCode)
	case <-time.After(5 * time.Second): // Wait for a connection with timeout
		healthStatus := HealthCheck{
			Status: "DB is overloaded, please try again later",
		}
		totalErrors.Add(1)
		handleErrorAndRespond(w, nil, healthStatus, "", http.StatusTooManyRequests, http.StatusTooManyRequests)
	}
}

// updateSourceHandler handles the update of sources
func updateSourceHandler(w http.ResponseWriter, r *http.Request) {
	select {
	case dbSemaphore <- struct{}{}:
		defer func() { <-dbSemaphore }()

		successCode := http.StatusNoContent
		query, err := extractQueryOrBody(r)
		if err != nil {
			handleErrorAndRespond(w, err, nil, "Missing parameter 'q' in removeSource request", http.StatusBadRequest, successCode)
			return
		}

		results, err := performUpdateSource(query, getQTypeFromName(r.Method), &dbHandler)
		if err != nil {
			totalErrors.Add(1)
		} else {
			totalSuccess.Add(1)
		}
		handleErrorAndRespond(w, err, results, "Error performing update Source: %v", http.StatusInternalServerError, successCode)
	case <-time.After(5 * time.Second): // Wait for a connection with timeout
		healthStatus := HealthCheck{
			Status: "DB is overloaded, please try again later",
		}
		totalErrors.Add(1)
		handleErrorAndRespond(w, nil, healthStatus, "", http.StatusTooManyRequests, http.StatusTooManyRequests)
	}
}

// vacuumSourceHandler handles the vacuum of sources
func vacuumSourceHandler(w http.ResponseWriter, r *http.Request) {
	select {
	case dbSemaphore <- struct{}{}:
		defer func() { <-dbSemaphore }()

		successCode := http.StatusNoContent
		query, err := extractQueryOrBody(r)
		if err != nil {
			handleErrorAndRespond(w, err, nil, "Missing parameter 'q' in removeSource request", http.StatusBadRequest, successCode)
			return
		}

		results, err := performVacuumSource(query, getQTypeFromName(r.Method), &dbHandler)
		if err != nil {
			totalErrors.Add(1)
		} else {
			totalSuccess.Add(1)
		}
		handleErrorAndRespond(w, err, results, "Error performing removeSource: %v", http.StatusInternalServerError, successCode)
	case <-time.After(5 * time.Second): // Wait for a connection with timeout
		healthStatus := HealthCheck{
			Status: "DB is overloaded, please try again later",
		}
		totalErrors.Add(1)
		handleErrorAndRespond(w, nil, healthStatus, "", http.StatusTooManyRequests, http.StatusTooManyRequests)
	}
}

// singleURLstatusHandler handles the status requests
func singleURLstatusHandler(w http.ResponseWriter, r *http.Request) {
	select {
	case dbSemaphore <- struct{}{}:
		defer func() { <-dbSemaphore }()

		successCode := http.StatusOK
		query, err := extractQueryOrBody(r)
		if err != nil {
			handleErrorAndRespond(w, err, nil, "Missing parameter 'q' in status request", http.StatusBadRequest, successCode)
			return
		}

		results, err := performGetURLStatus(query, getQTypeFromName(r.Method), &dbHandler)
		if err != nil {
			totalErrors.Add(1)
		} else {
			totalSuccess.Add(1)
		}
		handleErrorAndRespond(w, err, results, "Error performing status: %v", http.StatusInternalServerError, successCode)
	case <-time.After(5 * time.Second): // Wait for a connection with timeout
		healthStatus := HealthCheck{
			Status: "DB is overloaded, please try again later",
		}
		totalErrors.Add(1)
		handleErrorAndRespond(w, nil, healthStatus, "", http.StatusTooManyRequests, http.StatusTooManyRequests)
	}
}

// allURLstatusHandler handles the status requests for all sources
func allURLstatusHandler(w http.ResponseWriter, r *http.Request) {
	select {
	case dbSemaphore <- struct{}{}:
		defer func() { <-dbSemaphore }()

		successCode := http.StatusOK

		results, err := performGetAllURLStatus(getQTypeFromName(r.Method), &dbHandler)
		if err != nil {
			totalErrors.Add(1)
		} else {
			totalSuccess.Add(1)
		}
		handleErrorAndRespond(w, err, results, "Error performing status: %v", http.StatusInternalServerError, successCode)
	case <-time.After(5 * time.Second): // Wait for a connection with timeout
		healthStatus := HealthCheck{
			Status: "DB is overloaded, please try again later",
		}
		totalErrors.Add(1)
		handleErrorAndRespond(w, nil, healthStatus, "", http.StatusTooManyRequests, http.StatusTooManyRequests)
	}
}

func addOwnerHandler(w http.ResponseWriter, r *http.Request) {
	handleRequestWithDB(w, r, http.StatusCreated, func(query string, qType int, db *cdb.Handler) (interface{}, error) {
		return performAddOwner(query, qType, db)
	})
}

func updateOwnerHandler(w http.ResponseWriter, r *http.Request) {
	handleRequestWithDB(w, r, http.StatusNoContent, func(query string, qType int, db *cdb.Handler) (interface{}, error) {
		return performUpdateOwner(query, qType, db)
	})
}

func removeOwnerHandler(w http.ResponseWriter, r *http.Request) {
	handleRequestWithDB(w, r, http.StatusNoContent, func(query string, qType int, db *cdb.Handler) (interface{}, error) {
		return performRemoveOwner(query, qType, db)
	})
}

func addCategoryHandler(w http.ResponseWriter, r *http.Request) {
	handleRequestWithDB(w, r, http.StatusCreated, func(query string, qType int, db *cdb.Handler) (interface{}, error) {
		return performAddCategory(query, qType, db)
	})
}

func updateCategoryHandler(w http.ResponseWriter, r *http.Request) {
	handleRequestWithDB(w, r, http.StatusNoContent, func(query string, qType int, db *cdb.Handler) (interface{}, error) {
		return performUpdateCategory(query, qType, db)
	})
}

func removeCategoryHandler(w http.ResponseWriter, r *http.Request) {
	handleRequestWithDB(w, r, http.StatusNoContent, func(query string, qType int, db *cdb.Handler) (interface{}, error) {
		return performRemoveCategory(query, qType, db)
	})
}
