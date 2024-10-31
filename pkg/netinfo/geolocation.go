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

// Package netinfo provides functionality to extract network information
package netinfo

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	config "github.com/pzaino/thecrowler/pkg/config"

	"github.com/oschwald/maxminddb-golang"
)

// DetectLocation detects the geolocation for the given IP address using the provided GeoLite2 database.
func DetectLocation(ipAddress string, cfg config.GeoLookupConfig) (*DetectedLocation, error) {
	if !cfg.Enabled {
		return nil, fmt.Errorf("geolocation is disabled")
	}
	glType := strings.ToLower(strings.TrimSpace(cfg.Type))
	switch glType {
	case "maxmind", "local":
		return detectLocationMaxMind(ipAddress, cfg.DBPath)
	case "ip2location", "remote":
		return detectLocationIP2Location(ipAddress, cfg.APIKey, cfg.Timeout, cfg.SSLMode)
	default:
		return nil, fmt.Errorf("unsupported geolocation type: %s", cfg.Type)
	}
}

func detectLocationMaxMind(ipAddress string, dbPath string) (*DetectedLocation, error) {
	// Check if the DB path is valid and the file exists
	if dbPath == "" {
		return nil, fmt.Errorf("GeoLite2 database path is empty")
	}
	if !cmn.IsPathCorrect(dbPath) {
		return nil, fmt.Errorf("GeoLite2 database path is incorrect or the file does not exist")
	}

	// Load the MaxMind GeoLite2 database
	db, err := maxminddb.Open(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close() //nolint:errcheck // Don't lint for error not checked, this is a defer statement

	// Parse the IP address
	netIP := net.ParseIP(ipAddress)
	if netIP == nil {
		return nil, &InvalidIPAddressError{IPAddress: ipAddress}
	}

	// Query geolocation
	var location DetectedLocation
	err = db.Lookup(netIP, &location)
	if err != nil {
		return nil, err
	}

	return &location, nil
}

func detectLocationIP2Location(ipAddress, apiKey string, timeout int, sslmode string) (*DetectedLocation, error) {
	url := fmt.Sprintf("https://api.ip2location.com/v2/?ip=%s&key=%s&format=json", ipAddress, apiKey)

	httpClient := &http.Client{
		Transport: cmn.SafeTransport(timeout, sslmode),
	}
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck // Don't lint for error not checked, this is a defer statement

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("IP2Location API returned non-OK status: %d", resp.StatusCode)
	}

	var result IP2LocationResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	// Convert IP2LocationResult to DetectedLocation as needed
	location := DetectedLocation{
		CountryCode: result.CountryCode,
		CountryName: result.CountryName,
		City:        result.CityName,
		Latitude:    result.Latitude,
		Longitude:   result.Longitude,
	}

	return &location, nil
}

// InvalidIPAddressError represents an error for an invalid IP address.
type InvalidIPAddressError struct {
	IPAddress string
}

func (e *InvalidIPAddressError) Error() string {
	return "Invalid IP address: " + e.IPAddress
}
