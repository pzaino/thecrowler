package netinfo

import (
	"net"

	"github.com/oschwald/maxminddb-golang"
)

// DetectLocation detects the geolocation for the given IP address using the provided GeoLite2 database.
func DetectLocation(ipAddress string, dbPath string) (*DetectedLocation, error) {
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
