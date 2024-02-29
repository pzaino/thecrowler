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
	"runtime"
	"strconv"
	"strings"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"

	"golang.org/x/time/rate"
)

// Create a rate limiter for your application. Adjust the parameters as needed.
var limiter *rate.Limiter

func main() {

	configFile := flag.String("config", "./config.yaml", "Path to the configuration file")
	flag.Parse()

	// Reading the configuration file
	var err error
	config, err = cfg.LoadConfig(*configFile)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlFatal, "Error reading config file: %v", err)
	}
	if cfg.IsEmpty(config) {
		cmn.DebugMsg(cmn.DbgLvlFatal, "Config file is empty")
	}

	// Set the OS variable
	config.OS = runtime.GOOS

	// Set the rate limiter
	var rl, bl int
	if config.API.RateLimit == "" {
		config.API.RateLimit = "10,10"
	}
	rl, err = strconv.Atoi(strings.Split(config.API.RateLimit, ",")[0])
	if err != nil {
		rl = 10
	}
	bl, err = strconv.Atoi(strings.Split(config.API.RateLimit, ",")[1])
	if err != nil {
		bl = 10
	}
	limiter = rate.NewLimiter(rate.Limit(rl), bl)

	// Set the handlers
	initAPIv1()

	cmn.DebugMsg(cmn.DbgLvlInfo, "Starting server on %s:%d", config.API.Host, config.API.Port)
	if strings.ToLower(strings.TrimSpace(config.API.SSLMode)) == "enable" {
		log.Fatal(http.ListenAndServeTLS(config.API.Host+":"+fmt.Sprintf("%d", config.API.Port), config.API.CertFile, config.API.KeyFile, nil))
	} else {
		log.Fatal(http.ListenAndServe(config.API.Host+":"+fmt.Sprintf("%d", config.API.Port), nil))
	}
}

// initAPIv1 initializes the API v1 handlers
func initAPIv1() {
	searchHandlerWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(searchHandler)))
	scrImgSrchHandlerWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(scrImgSrchHandler)))
	netInfoHandlerWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(netInfoHandler)))
	httpInfoHandlerWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(httpInfoHandler)))
	addSourceHandlerWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(addSourceHandler)))
	removeSourceHandlerWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(removeSourceHandler)))

	http.Handle("/v1/search", searchHandlerWithMiddlewares)
	http.Handle("/v1/netinfo", netInfoHandlerWithMiddlewares)
	http.Handle("/v1/httpinfo", httpInfoHandlerWithMiddlewares)
	http.Handle("/v1/screenshot", scrImgSrchHandlerWithMiddlewares)

	if config.API.EnableConsole {
		http.Handle("/v1/add_source", addSourceHandlerWithMiddlewares)
		http.Handle("/v1/remove_source", removeSourceHandlerWithMiddlewares)
	}
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

// searchHandler handles the traditional search requests
func searchHandler(w http.ResponseWriter, r *http.Request) {
	successCode := http.StatusOK
	query, err := extractQueryOrBody(r)
	if err != nil {
		handleErrorAndRespond(w, err, nil, "Missing parameter 'q' in search request", http.StatusBadRequest, successCode)
		return
	}

	results, err := performSearch(query)
	handleErrorAndRespond(w, err, results, "Error performing search: %v", http.StatusInternalServerError, successCode)
}

// scrImgSrchHandler handles the search requests for screenshot images
func scrImgSrchHandler(w http.ResponseWriter, r *http.Request) {
	successCode := http.StatusOK
	query, err := extractQueryOrBody(r)
	if err != nil {
		handleErrorAndRespond(w, err, nil, "Missing parameter 'q' in screenshot search request", http.StatusBadRequest, successCode)
		return
	}

	results, err := performScreenshotSearch(query, getQType(r.Method != "POST"))

	handleErrorAndRespond(w, err, results, "Error performing screenshot search: %v", http.StatusInternalServerError, successCode)
}

// netInfoHandler handles the network information requests
func netInfoHandler(w http.ResponseWriter, r *http.Request) {
	successCode := http.StatusOK
	query, err := extractQueryOrBody(r)
	if err != nil {
		handleErrorAndRespond(w, err, nil, "Missing parameter 'q' in netinfo search request", http.StatusBadRequest, successCode)
		return
	}

	results, err := performNetInfoSearch(query, getQType(r.Method != "POST"))
	handleErrorAndRespond(w, err, results, "Error performing netinfo search: %v", http.StatusInternalServerError, successCode)
}

// httpInfoHandler handles the http information requests
func httpInfoHandler(w http.ResponseWriter, r *http.Request) {
	successCode := http.StatusOK
	query, err := extractQueryOrBody(r)
	if err != nil {
		handleErrorAndRespond(w, err, nil, "Missing parameter 'q' in httpinfo search request", http.StatusBadRequest, successCode)
		return
	}

	results, err := performHTTPInfoSearch(query, getQType(r.Method != "POST"))
	handleErrorAndRespond(w, err, results, "Error performing httpinfo search: %v", http.StatusInternalServerError, successCode)
}

// addSourceHandler handles the addition of new sources
func addSourceHandler(w http.ResponseWriter, r *http.Request) {
	successCode := http.StatusCreated
	query, err := extractQueryOrBody(r)
	if err != nil {
		handleErrorAndRespond(w, err, nil, "Missing parameter 'q' in addSource request", http.StatusBadRequest, successCode)
		return
	}

	results, err := performAddSource(query, getQType(r.Method != "POST"))
	handleErrorAndRespond(w, err, results, "Error performing addSource: %v", http.StatusInternalServerError, successCode)
}

// removeSourceHandler handles the removal of sources
func removeSourceHandler(w http.ResponseWriter, r *http.Request) {
	successCode := http.StatusNoContent
	query, err := extractQueryOrBody(r)
	if err != nil {
		handleErrorAndRespond(w, err, nil, "Missing parameter 'q' in removeSource request", http.StatusBadRequest, successCode)
		return
	}

	results, err := performRemoveSource(query, getQType(r.Method != "POST"))
	handleErrorAndRespond(w, err, results, "Error performing removeSource: %v", http.StatusInternalServerError, successCode)
}
