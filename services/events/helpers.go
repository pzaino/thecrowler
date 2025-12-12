// Package main (events) implements the CROWler Events Handler engine.
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// Heartbeat Coordinator: one active heartbeat at a time.
var heartbeatMu sync.Mutex
var activeHeartbeat *HeartbeatState

var durationUnits = map[string]time.Duration{
	"s":       time.Second,
	"sec":     time.Second,
	"secs":    time.Second,
	"second":  time.Second,
	"seconds": time.Second,
	"m":       time.Minute,
	"min":     time.Minute,
	"mins":    time.Minute,
	"minute":  time.Minute,
	"minutes": time.Minute,
	"h":       time.Hour,
	"hr":      time.Hour,
	"hrs":     time.Hour,
	"hour":    time.Hour,
	"hours":   time.Hour,
	"d":       24 * time.Hour,
	"day":     24 * time.Hour,
	"days":    24 * time.Hour,
}

// handleErrorAndRespond encapsulates common error handling and JSON response logic.
func handleErrorAndRespond(w http.ResponseWriter, err error, results interface{}, errMsg string, errCode int, successCode int) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlDebug3, errMsg, err)
		// escape " in the error message
		errMsg = strings.ReplaceAll(err.Error(), "\"", "\\\"")
		// Encapsulate the error message in a JSON string
		results := "{\n  \"error\": \"" + errMsg + "\"\n}"
		http.Error(w, results, errCode)
		return
	}
	w.WriteHeader(successCode) // Explicitly set the success status code
	if err := json.NewEncoder(w).Encode(results); err != nil {
		// Log the error and send a generic error message to the client
		cmn.DebugMsg(cmn.DbgLvlDebug3, "Error encoding JSON response: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// Heartbeat helpers

func startHeartbeatLoop(db *cdb.Handler, config cfg.Config) {
	interval := parseDuration(config.Events.HeartbeatInterval)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		<-ticker.C
		startHeartbeat(db, config)
	}
}

func startHeartbeat(db *cdb.Handler, config cfg.Config) {
	heartbeatMu.Lock()
	if activeHeartbeat != nil {
		heartbeatMu.Unlock()
		return // still waiting for previous heartbeat
	}

	hbType := "crowler_heartbeat"
	now := time.Now()

	eventResponseTimeout := parseDuration(config.Events.HeartbeatTimeout)
	// Empty or invalid → 0 or negative
	if eventResponseTimeout <= 0 {
		eventResponseTimeout = 15 * time.Second
	}

	// Minimum 5 seconds
	if eventResponseTimeout < time.Second*5 {
		eventResponseTimeout = time.Second * 15
	}

	state := &HeartbeatState{
		SentAt:    now,
		Timeout:   eventResponseTimeout,
		Responses: make(map[string]cdb.Event),
		DoneChan:  make(chan struct{}),
	}
	activeHeartbeat = state
	heartbeatMu.Unlock()

	// Create heartbeat event
	event := cdb.Event{
		Action:        "",
		SourceID:      0,
		Type:          hbType,
		Severity:      "crowler_system_info",
		Timestamp:     now.Format(time.RFC3339),
		CreatedAt:     now.Format(time.RFC3339),
		LastUpdatedAt: now.Format(time.RFC3339),
		Details: map[string]interface{}{
			"origin_type": "events-manager",
			"origin_name": cmn.GetMicroServiceName(),
			"origin_time": now.Format(time.RFC3339),
			"type":        "heartbeat_request",
		},
	}

	uid, err := cdb.CreateEventWithRetries(db, event)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "HEARTBEAT: failed to create event: %v", err)
		heartbeatMu.Lock()
		activeHeartbeat = nil
		heartbeatMu.Unlock()
		return
	}

	state.ParentID = uid

	// Start timeout watcher
	go heartbeatTimeoutWatcher(db, state)
}

func heartbeatTimeoutWatcher(db *cdb.Handler, state *HeartbeatState) {
	time.Sleep(state.Timeout)

	heartbeatMu.Lock()
	defer heartbeatMu.Unlock()

	if activeHeartbeat != state {
		return
	}

	// Produce report and cleanup now
	report := finishHeartbeatState(state)
	if config.Events.HeartbeatLog {
		logHeartbeatReport(report)
	}

	// Delete events (parent + responses)
	cleanupHeartbeatEvents(db, state.ParentID)

	activeHeartbeat = nil
}

// Called from processEvent() BEFORE plugin/agent routing.
func maybeHandleHeartbeatResponse(event cdb.Event) bool {
	heartbeatMu.Lock()
	defer heartbeatMu.Unlock()

	if activeHeartbeat == nil {
		return false
	}

	// Only capture "crowler_heartbeat_response"
	if strings.ToLower(strings.TrimSpace(event.Type)) != "crowler_heartbeat_response" {
		return false
	}

	// Must contain correct parent_event_id
	parent, ok := event.Details["parent_event_id"].(string)
	if !ok {
		return false
	}

	if parent != activeHeartbeat.ParentID {
		return false
	}

	// Identify responder
	responder := fmt.Sprintf("src-%s", event.ID)
	if name, ok := event.Details["origin_name"].(string); ok {
		responder = name
	}

	activeHeartbeat.Responses[responder] = event

	return true // consumed
}

func finishHeartbeatState(state *HeartbeatState) HeartbeatReport {
	res := make([]cdb.Event, 0, len(state.Responses))
	names := make([]string, 0, len(state.Responses))

	const running = "running"

	for k, v := range state.Responses {
		names = append(names, k)
		res = append(res, v)
	}

	// Update the active_fleet_nodes metric
	mActiveFleetNodes.Set(float64(len(state.Responses)))

	// Determine whether all nodes are idle
	allIdle := true

	for _, evt := range state.Responses {
		statusRaw, ok := evt.Details["pipeline_status"]
		if !ok {
			continue
		}

		arr, ok := statusRaw.([]interface{})
		if !ok {
			continue
		}

		if len(arr) == 0 {
			// empty = idle
			continue
		}

		// If any pipeline entry is active, the fleet is NOT idle
		for _, elem := range arr {
			obj, ok := elem.(map[string]interface{})
			if !ok {
				continue
			}

			ps, _ := obj["PipelineStatus"].(string)
			ps = strings.ToLower(strings.TrimSpace(ps))
			crawling, _ := obj["CrawlingStatus"].(string)
			crawling = strings.ToLower(strings.TrimSpace(crawling))
			netinfo, _ := obj["NetInfoStatus"].(string)
			netinfo = strings.ToLower(strings.TrimSpace(netinfo))
			httpinfo, _ := obj["HTTPInfoStatus"].(string)
			httpinfo = strings.ToLower(strings.TrimSpace(httpinfo))

			// If any subsystem is running we are not idle
			if ps == running || crawling == running || netinfo == running || httpinfo == running {
				allIdle = false
				break
			}
		}
	}

	if allIdle {
		if canScheduleDBMaintenance() {
			cmn.DebugMsg(cmn.DbgLvlInfo, "HEARTBEAT ANALYSIS: Entire fleet appears idle, scheduling DB optimization...")

			// build an internal event to request DB maintenance
			maintenanceEvent := cdb.Event{
				Action:    "db_maintenance",
				Type:      "system_event",
				Severity:  "low",
				Timestamp: time.Now().Format(time.RFC3339),
				Details: map[string]interface{}{
					"action": "db_maintenance",
					"reason": "all_fleet_idle",
					"time":   time.Now().Format(time.RFC3339),
				},
			}

			// schedule internal event asynchronously
			go func(ev cdb.Event) {
				_, err := cdb.CreateEventWithRetries(&dbHandler, ev)
				if err != nil {
					cmn.DebugMsg(cmn.DbgLvlError, "Failed to create DB maintenance event: %v", err)
				}
			}(maintenanceEvent)
		}
	}

	return HeartbeatReport{
		ParentID:   state.ParentID,
		Total:      len(res),
		Responders: names,
		Raw:        res,
	}
}

func canScheduleDBMaintenance() bool {
	// Check if we have an interval
	if strings.TrimSpace(config.Events.SysDBMaintenance) == "" {
		return false
	}

	// Get schedule time from config
	interval := parseDuration(config.Events.SysDBMaintenance)

	// Never schedule if last run was less than 1 hour ago
	if !lastDBMaintenance.IsZero() {
		if time.Since(lastDBMaintenance) < interval {
			return false
		}
	}

	// OK to schedule
	lastDBMaintenance = time.Now()
	return true
}

func logHeartbeatReport(r HeartbeatReport) {
	b, _ := json.MarshalIndent(r, "", "  ")
	cmn.DebugMsg(cmn.DbgLvlInfo, "HEARTBEAT REPORT:\n%s\n", string(b))
}

func cleanupHeartbeatEvents(db *cdb.Handler, parentID string) {
	_, err := (*db).Exec(`
		DELETE FROM Events
		WHERE event_sha256 = $1
		   OR details->>'parent_event_id' = $1
	`, parentID)

	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "HEARTBEAT cleanup error: %v", err)
	}
}

func parseDuration(s string) time.Duration {
	s = strings.TrimSpace(strings.ToLower(s))

	// First try the native Go parser ("1h30m")
	if d, err := time.ParseDuration(s); err == nil {
		return d
	}

	parts := strings.Fields(s)
	if len(parts) == 0 {
		return time.Minute
	}

	var total time.Duration

	i := 0
	for i < len(parts) {
		// Expect "<number> <unit>"
		if i+1 >= len(parts) {
			break
		}

		nStr := parts[i]
		uStr := parts[i+1]

		n, err := strconv.ParseFloat(nStr, 64)
		if err != nil {
			i++
			continue
		}

		unit, ok := durationUnits[uStr]
		if !ok {
			i++
			continue
		}

		total += time.Duration(n * float64(unit))
		i += 2
	}

	if total == 0 {
		return time.Minute
	}

	return total
}
