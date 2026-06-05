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

// ScheduleEventRequest represents a request to schedule an event.
type ScheduleEventRequest struct {
	Event      cdb.Event `json:"event"`       // The actual CROWler event to be scheduled
	ScheduleAt string    `json:"schedule_at"` // The time at which the event should be scheduled (RFC3339 format)
	Recurrence string    `json:"recurrence"`  // Recurrence pattern (e.g., "daily", "weekly", "monthly")
}

// InformationSeedRunDiagnosticsDetails is the stable Events.details payload for
// information_seed.* discovery diagnostics emitted by the Information Seed
// runner. Sensitive provider configuration, headers, parameters, plugin
// metadata, and error text must be redacted before a value is stored here.
type InformationSeedRunDiagnosticsDetails struct {
	SchemaVersion            string                         `json:"schema_version"`
	InformationSeedID        uint64                         `json:"information_seed_id"`
	InformationSeed          string                         `json:"information_seed"`
	RunID                    string                         `json:"run_id"`
	RunAttempt               int                            `json:"run_attempt"`
	SourceID                 uint64                         `json:"source_id"`
	ProviderCounts           map[string]int                 `json:"provider_counts"`
	ProviderMetrics          map[string]map[string]int      `json:"provider_metrics"`
	ProviderConfigs          map[string]interface{}         `json:"provider_configs"`
	CandidateCounts          InformationSeedCandidateCounts `json:"candidate_counts"`
	CandidatesFound          int                            `json:"candidates_found"`
	CandidatesAccepted       int                            `json:"candidates_accepted"`
	CandidatesRejected       int                            `json:"candidates_rejected"`
	CandidateRejectionCounts map[string]int                 `json:"candidate_rejection_counts"`
	CandidateRejectionStages map[string]map[string]int      `json:"candidate_rejection_stages"`
	SourcesCreated           int                            `json:"sources_created"`
	SourcesLinked            int                            `json:"sources_linked"`
	SourceIDsCreated         []uint64                       `json:"source_ids_created"`
	SourceIDsLinked          []uint64                       `json:"source_ids_linked"`
	ErrorSummaries           []string                       `json:"error_summaries"`
	PluginMetadata           []map[string]interface{}       `json:"plugin_metadata"`
}

// InformationSeedCandidateCounts contains stable aggregate candidate counters
// and stage totals for a single Information Seed run attempt.
type InformationSeedCandidateCounts struct {
	Found    int            `json:"found"`
	Accepted int            `json:"accepted"`
	Rejected int            `json:"rejected"`
	ByStage  map[string]int `json:"by_stage"`
}

var (
	lastDBMaintenance time.Time
)
