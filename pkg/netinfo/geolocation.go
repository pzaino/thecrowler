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
	"fmt"
	"net"

	cmn "github.com/pzaino/thecrowler/pkg/common"

	"github.com/oschwald/maxminddb-golang"
)

// DetectLocation detects the geolocation for the given IP address using the provided GeoLite2 database.
func DetectLocation(ipAddress string, dbPath string) (*DetectedLocation, error) {
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
	defer db.Close()

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

// InvalidIPAddressError represents an error for an invalid IP address.
type InvalidIPAddressError struct {
	IPAddress string
}

func (e *InvalidIPAddressError) Error() string {
	return "Invalid IP address: " + e.IPAddress
}
