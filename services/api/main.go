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
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"

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
)

func initAll(configFile *string, config *cfg.Config, lmt **rate.Limiter) error {
	// Reading the configuration file
	var err error
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

	return nil
}

func main() {
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
				os.Exit(0)

			case syscall.SIGQUIT:
				// Handle SIGQUIT
				cmn.DebugMsg(cmn.DbgLvlInfo, "SIGQUIT received, shutting down...")
				os.Exit(0)

			case syscall.SIGHUP:
				// Handle SIGHUP
				cmn.DebugMsg(cmn.DbgLvlInfo, "SIGHUP received, will reload configuration as soon as all pending jobs are completed...")
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

	cmn.DebugMsg(cmn.DbgLvlInfo, "Starting server on %s:%d", config.API.Host, config.API.Port)
	cmn.DebugMsg(cmn.DbgLvlInfo, "Awaiting for requests...")
	if strings.ToLower(strings.TrimSpace(config.API.SSLMode)) == "enable" {
		log.Fatal(srv.ListenAndServeTLS(config.API.CertFile, config.API.KeyFile))
	}
	log.Fatal(srv.ListenAndServe())
}

// initAPIv1 initializes the API v1 handlers
func initAPIv1() {
	// Health check
	healthCheckWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(healthCheckHandler)))

	http.Handle("/v1/health", healthCheckWithMiddlewares)

	// Query handlers
	searchHandlerWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(searchHandler)))
	scrImgSrchHandlerWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(scrImgSrchHandler)))
	netInfoHandlerWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(netInfoHandler)))
	httpInfoHandlerWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(httpInfoHandler)))
	webObjectHandlerWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(webObjectHandler)))
	webCorrelatedSitesHandlerWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(webCorrelatedSitesHandler)))
	webScrapedDataHandlerWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(webScrapedDataHandler)))

	http.Handle("/v1/search", searchHandlerWithMiddlewares)
	http.Handle("/v1/netinfo", netInfoHandlerWithMiddlewares)
	http.Handle("/v1/httpinfo", httpInfoHandlerWithMiddlewares)
	http.Handle("/v1/screenshot", scrImgSrchHandlerWithMiddlewares)
	http.Handle("/v1/webobject", webObjectHandlerWithMiddlewares)
	http.Handle("/v1/correlated_sites", webCorrelatedSitesHandlerWithMiddlewares)
	http.Handle("/v1/collected_data", webScrapedDataHandlerWithMiddlewares)

	if config.API.EnableConsole {
		addSourceHandlerWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(addSourceHandler)))
		removeSourceHandlerWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(removeSourceHandler)))
		singleURLstatusHandlerWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(singleURLstatusHandler)))
		allURLstatusHandlerWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(allURLstatusHandler)))

		http.Handle("/v1/add_source", addSourceHandlerWithMiddlewares)
		http.Handle("/v1/remove_source", removeSourceHandlerWithMiddlewares)
		http.Handle("/v1/get_source_status", singleURLstatusHandlerWithMiddlewares)
		http.Handle("/v1/get_all_source_status", allURLstatusHandlerWithMiddlewares)
	}
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

		handleErrorAndRespond(w, err, results, "Error performing search: %v", http.StatusInternalServerError, successCode)
	case <-time.After(5 * time.Second): // Wait for a connection with timeout
		healthStatus := HealthCheck{
			Status: "DB is overloaded, please try again later",
		}
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
			} else {
				retCode = successCode
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
			handleErrorAndRespond(w, err, results, "Error performing webobject search: %v", http.StatusInternalServerError, successCode)
		}
	case <-time.After(5 * time.Second): // Wait for a connection with timeout
		healthStatus := HealthCheck{
			Status: "DB is overloaded, please try again later",
		}
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
			} else {
				retCode = successCode
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
			handleErrorAndRespond(w, err, results, "Error performing correlatedsites search: %v", http.StatusInternalServerError, successCode)
		}
	case <-time.After(5 * time.Second): // Wait for a connection with timeout
		healthStatus := HealthCheck{
			Status: "DB is overloaded, please try again later",
		}
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
			} else {
				retCode = successCode
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
			handleErrorAndRespond(w, err, results, "Error performing correlatedsites search: %v", http.StatusInternalServerError, successCode)
		}
	case <-time.After(5 * time.Second): // Wait for a connection with timeout
		healthStatus := HealthCheck{
			Status: "DB is overloaded, please try again later",
		}
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
			} else {
				retCode = successCode
			}
			handleErrorAndRespond(w, err, results, "Error performing screenshot search: %v", http.StatusInternalServerError, retCode)
		} else {
			handleErrorAndRespond(w, err, results, "Error performing screenshot search: %v", http.StatusInternalServerError, successCode)
		}
	case <-time.After(5 * time.Second): // Wait for a connection with timeout
		healthStatus := HealthCheck{
			Status: "DB is overloaded, please try again later",
		}
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
			} else {
				retCode = successCode
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
			handleErrorAndRespond(w, err, results, "Error performing netinfo search: %v", http.StatusInternalServerError, successCode)
		}
	case <-time.After(5 * time.Second): // Wait for a connection with timeout
		healthStatus := HealthCheck{
			Status: "DB is overloaded, please try again later",
		}
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
			} else {
				retCode = successCode
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
			handleErrorAndRespond(w, err, results, "Error performing httpinfo search: %v", http.StatusInternalServerError, successCode)
		}
	case <-time.After(5 * time.Second): // Wait for a connection with timeout
		healthStatus := HealthCheck{
			Status: "DB is overloaded, please try again later",
		}
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
		handleErrorAndRespond(w, err, results, "Error performing addSource: %v", http.StatusInternalServerError, successCode)
	case <-time.After(5 * time.Second): // Wait for a connection with timeout
		healthStatus := HealthCheck{
			Status: "DB is overloaded, please try again later",
		}
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
		handleErrorAndRespond(w, err, results, "Error performing removeSource: %v", http.StatusInternalServerError, successCode)
	case <-time.After(5 * time.Second): // Wait for a connection with timeout
		healthStatus := HealthCheck{
			Status: "DB is overloaded, please try again later",
		}
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
		handleErrorAndRespond(w, err, results, "Error performing status: %v", http.StatusInternalServerError, successCode)
	case <-time.After(5 * time.Second): // Wait for a connection with timeout
		healthStatus := HealthCheck{
			Status: "DB is overloaded, please try again later",
		}
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
		handleErrorAndRespond(w, err, results, "Error performing status: %v", http.StatusInternalServerError, successCode)
	case <-time.After(5 * time.Second): // Wait for a connection with timeout
		healthStatus := HealthCheck{
			Status: "DB is overloaded, please try again later",
		}
		handleErrorAndRespond(w, nil, healthStatus, "", http.StatusTooManyRequests, http.StatusTooManyRequests)
	}
}
