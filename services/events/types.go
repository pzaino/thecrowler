// Package main (events) implements the CROWler Events Handler engine.
package main

import (
	"time"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// HealthCheck is a struct that holds the health status of the application.
type HealthCheck struct {
	Status string `json:"status"`
}

// ReadyCheck is a struct that holds the readiness status of the application.
type ReadyCheck struct {
	Status string `json:"status"`
}

// PluginResponse is a struct that holds the response from a plugin.
type PluginResponse struct {
	Success     bool        `json:"success"`
	Message     string      `json:"message"`
	APIResponse interface{} `json:"apiResponse,omitempty"` // Use `interface{}` to allow flexibility in the API response structure
}

// HeartbeatState tracks an active heartbeat round.
type HeartbeatState struct {
	ParentID  string
	SentAt    time.Time
	Timeout   time.Duration
	Responses map[string]cdb.Event // keyed by agent or source
	DoneChan  chan struct{}
}

// HeartbeatReport is the structure of the heartbeat report.
type HeartbeatReport struct {
	ParentID   string      `json:"parent_id"`
	Total      int         `json:"total"`
	Responders []string    `json:"responders"`
	Raw        []cdb.Event `json:"raw_responses"`
}

var (
	lastDBMaintenance time.Time
)
