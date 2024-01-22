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

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime"

	"gopkg.in/yaml.v2"
)

func main() {

	configFile := flag.String("config", "./config.yaml", "Path to the configuration file")
	flag.Parse()

	// Reading the configuration file
	config = Config{}
	data, err := os.ReadFile(*configFile)
	if err != nil {
		log.Fatalf("Error reading config file: %v", err)
	}

	err = yaml.Unmarshal(data, &config)
	if err != nil {
		log.Fatalf("Error parsing config file: %v", err)
	}

	// Set the OS variable
	config.OS = runtime.GOOS

	http.HandleFunc("/search", searchHandler)
	log.Printf("Starting server on %s:%d\n", config.API.Host, config.API.Port)
	log.Fatal(http.ListenAndServe(config.API.Host+":"+fmt.Sprintf("%d", config.API.Port), nil))
}

func searchHandler(w http.ResponseWriter, r *http.Request) {
	// Extract query parameter
	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, "Query parameter 'q' is required", http.StatusBadRequest)
		return
	}

	// Perform the search
	results, err := performSearch(query)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Respond with JSON
	w.Header().Set("Content-Type", "application/json")
	err = json.NewEncoder(w).Encode(results)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}
