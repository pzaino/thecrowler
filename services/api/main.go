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
	"io"
	"log"
	"net/http"
	"runtime"
	"strconv"
	"strings"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"

	"golang.org/x/time/rate"
)

const (
	jsonErrorPrefix  = "Error encoding JSON: %v"
	contentTypeLabel = "Content-Type"
	contentTypeJSON  = "application/json"
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

func initAPIv1() {
	searchHandlerWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(searchHandler)))
	scrImgSrchHandlerWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(scrImgSrchHandler)))
	netInfoHandlerWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(netInfoHandler)))
	addSourceHandlerWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(addSourceHandler)))
	removeSourceHandlerWithMiddlewares := SecurityHeadersMiddleware(RateLimitMiddleware(http.HandlerFunc(removeSourceHandler)))

	http.Handle("/v1/search", searchHandlerWithMiddlewares)
	http.Handle("/v1/netinfo", netInfoHandlerWithMiddlewares)
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
	// Extract query parameter
	query := r.URL.Query().Get("q")
	if query == "" {
		cmn.DebugMsg(cmn.DbgLvlDebug3, "Missing parameter 'q' in search get request")
		http.Error(w, "Query parameter 'q' is required for search get request", http.StatusBadRequest)
		return
	}

	// Perform the search
	results, err := performSearch(query)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug3, "Error performing search: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Respond with JSON
	w.Header().Set(contentTypeLabel, contentTypeJSON)
	err = json.NewEncoder(w).Encode(results)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug3, jsonErrorPrefix, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

// scrImgSrchHandler handles the search requests for screenshot images
func scrImgSrchHandler(w http.ResponseWriter, r *http.Request) {
	var results ScreenshotResponse
	var query string
	var err error
	if r.Method == "POST" {
		// Read the JSON document from the request body
		body, err := io.ReadAll(r.Body)
		defer r.Body.Close()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		query = string(body)
		results, err = performScreenshotSearch(query, 0)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlDebug3, "Error performing screenshot search: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		// Extract query parameter
		query := r.URL.Query().Get("q")
		if query == "" {
			cmn.DebugMsg(cmn.DbgLvlDebug3, "Missing parameter 'q' in screenshot search get request")
			http.Error(w, "Query parameter 'q' is required for screenshot get request", http.StatusBadRequest)
			return
		}
		results, err = performScreenshotSearch(query, 1)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlDebug3, "Error on screenshot search: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// Respond with JSON
	w.Header().Set(contentTypeLabel, contentTypeJSON)
	err = json.NewEncoder(w).Encode(results)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug3, jsonErrorPrefix, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

}

// netInfoHandler handles the network information requests
func netInfoHandler(w http.ResponseWriter, r *http.Request) {
	var results NetInfoResponse
	var query string
	var err error
	if r.Method == "POST" {
		// Read the JSON document from the request body
		body, err := io.ReadAll(r.Body)
		defer r.Body.Close()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		query = string(body)
		results, err = performNetInfoSearch(query, 0)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlDebug3, "Error performing netinfo search: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		// Extract query parameter
		query := r.URL.Query().Get("q")
		if query == "" {
			cmn.DebugMsg(cmn.DbgLvlDebug3, "Missing parameter 'q' in netinfo search get request")
			http.Error(w, "Query parameter 'q' is required for netinfo get request", http.StatusBadRequest)
			return
		}
		results, err = performNetInfoSearch(query, 1)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlDebug3, "Error on netinfo search: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// Respond with JSON
	w.Header().Set(contentTypeLabel, contentTypeJSON)
	err = json.NewEncoder(w).Encode(results)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug3, jsonErrorPrefix, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

// addSourceHandler handles the addition of new sources
func addSourceHandler(w http.ResponseWriter, r *http.Request) {
	var results ConsoleResponse
	var query string
	var err error
	if r.Method == "POST" {
		// Read the JSON document from the request body
		body, err := io.ReadAll(r.Body)
		defer r.Body.Close()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		query = string(body)
		results, err = performAddSource(query, 0)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlDebug3, "Error performing addSource: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		// Extract query parameter
		query := r.URL.Query().Get("q")
		if query == "" {
			cmn.DebugMsg(cmn.DbgLvlDebug3, "Missing parameter 'q' in addSource get request")
			http.Error(w, "Query parameter 'q' is required for addSource get request", http.StatusBadRequest)
			return
		}
		results, err = performAddSource(query, 1)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlDebug3, "Error on addSource request: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// Respond with JSON
	w.Header().Set(contentTypeLabel, contentTypeJSON)
	err = json.NewEncoder(w).Encode(results)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug3, jsonErrorPrefix, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

// removeSourceHandler handles the removal of sources
func removeSourceHandler(w http.ResponseWriter, r *http.Request) {
	var results ConsoleResponse
	var query string
	var err error
	if r.Method == "POST" {
		// Read the JSON document from the request body
		body, err := io.ReadAll(r.Body)
		defer r.Body.Close()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		query = string(body)
		results, err = performRemoveSource(query, 0)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlDebug3, "Error performing removeSource: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		// Extract query parameter
		query := r.URL.Query().Get("q")
		if query == "" {
			cmn.DebugMsg(cmn.DbgLvlDebug3, "Missing parameter 'q' in removeSource get request")
			http.Error(w, "Query parameter 'q' is required for removeSource get request", http.StatusBadRequest)
			return
		}
		results, err = performRemoveSource(query, 1)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlDebug3, "Error on removeSource search: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// Respond with JSON
	w.Header().Set(contentTypeLabel, contentTypeJSON)
	err = json.NewEncoder(w).Encode(results)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug3, jsonErrorPrefix, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}
