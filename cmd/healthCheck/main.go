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
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
)

var (
	config     cfg.Config
	loadConfig = cfg.LoadConfig
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

func parseService(service string) (serviceType, error) {
	switch strings.ToLower(strings.TrimSpace(service)) {
	case "crowler":
		return crowler, nil
	case "vdi":
		return vdi, nil
	case "api":
		return api, nil
	case "events":
		return events, nil
	default:
		return crowler, fmt.Errorf("unknown service: %s", service)
	}
}

func run(args []string, client *http.Client) error {
	flags := flag.NewFlagSet("healthCheck", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	configFile := flags.String("config", "config.yaml", "Path to the configuration file")
	service := flags.String("service", "crowler", "Service to check (crowler, vdi, api, events)")
	checkType := flags.String("type", healthEndpoint, "Type of check to perform (health, ready)")

	if err := flags.Parse(args); err != nil {
		return err
	}

	var err error
	config, err = loadConfig(*configFile)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	serviceToCheck, err := parseService(*service)
	if err != nil {
		return err
	}

	healthURL := genHealthURL(serviceToCheck, *checkType)
	if healthURL == "" {
		return nil
	}

	resp, err := client.Get(healthURL) //nolint:gosec // This is usually a localhost connection
	if err != nil {
		return fmt.Errorf("health check request for %s: %w", *service, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check for %s returned %s", *service, resp.Status)
	}
	return nil
}

func main() {
	cmn.InitLogger("healthCheck")
	if err := run(os.Args[1:], http.DefaultClient); err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			cmn.DebugMsg(cmn.DbgLvlError, "Health check failed: %v", err)
		}
		os.Exit(1)
	}
}
