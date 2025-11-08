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

// Package main (healthCheck) is a command line that allows to check if
// both the CROWler, the general API and the VDI are reachable and working.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
)

var (
	config cfg.Config
)

// enum for the different types of services crowler, vdi and api
type serviceType int

const (
	crowler serviceType = iota
	vdi
	api
	events
)

const (
	healthEndpoint = "health"
	readyEndpoint  = "ready"
)

func genHealthURL(t serviceType, st string) string {
	// Define the health check endpoint
	rval := ""

	st = strings.ToLower(strings.TrimSpace(st))
	checkType := ""
	switch st {
	case healthEndpoint:
		checkType = healthEndpoint
	case readyEndpoint:
		checkType = readyEndpoint
	default:
		checkType = healthEndpoint
	}

	switch t {
	case crowler:
		rval = fmt.Sprintf("%s:%d/v1/%s", config.Crawler.Control.Host, config.Crawler.Control.Port, checkType)
		if config.Crawler.Control.SSLMode == cmn.EnableStr {
			rval = fmt.Sprintf("https://%s", rval)
		} else {
			rval = fmt.Sprintf("http://%s", rval)
		}
	case vdi:
		//rval, err = fmt.Sprintf("%s:%d/v1/health", config.VDI.Host, config.VDI.Port)
	case api:
		rval = fmt.Sprintf("%s:%d/v1/%s", config.API.Host, config.API.Port, checkType)
		if config.API.SSLMode == cmn.EnableStr {
			rval = fmt.Sprintf("https://%s", rval)
		} else {
			rval = fmt.Sprintf("http://%s", rval)
		}
	case events:
		rval = fmt.Sprintf("%s:%d/v1/%s", config.Events.Host, config.Events.Port, checkType)
		if config.Events.SSLMode == cmn.EnableStr {
			rval = fmt.Sprintf("https://%s", rval)
		} else {
			rval = fmt.Sprintf("http://%s", rval)
		}
	}
	return rval
}

func main() {
	configFile := flag.String("config", "config.yaml", "Path to the configuration file")
	service := flag.String("service", "crowler", "Service to check (crowler, vdi, api)")
	checkType := flag.String("type", healthEndpoint, "Type of check to perform (health, ready)")

	cmn.InitLogger("healthCheck")

	// Parse the command line arguments
	flag.Parse()

	// Load the configuration file
	var err error
	config, err = cfg.LoadConfig(*configFile)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, fmt.Sprintf("Health check failed to load config.yaml: %v", err))
		os.Exit(1)
	}

	// Check the service
	var serviceToCheck serviceType
	switch *service {
	case "crowler":
		serviceToCheck = crowler
	case "vdi":
		serviceToCheck = vdi
	case "api":
		serviceToCheck = api
	case "events":
		serviceToCheck = events
	default:
		cmn.DebugMsg(cmn.DbgLvlError, "Unknown service: %s", *service)
		os.Exit(1)
	}

	// Define the health check endpoint
	healthURL := genHealthURL(serviceToCheck, *checkType)
	if healthURL == "" {
		os.Exit(0)
	}

	// Perform the GET request
	resp, err := http.Get(healthURL) //nolint:gosec // This is usually a localhost connection
	if err != nil || resp.StatusCode != http.StatusOK {
		cmn.DebugMsg(cmn.DbgLvlDebug, fmt.Sprintf("Health check failed for %s: %v", *service, err))
		// If there's an error or the status is not 200, exit with a non-zero status
		os.Exit(1)
	}

	// If successful, exit with zero (healthy)
	os.Exit(0)
}
