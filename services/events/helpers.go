// Package main (events) implements the CROWler Events Handler engine.
package main

import (
	"context"
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

	state := &HeartbeatState{
		SentAt:    now,
		TimeoutAt: now.Add(parseDuration(config.Events.HeartbeatTimeout)),
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	uid, err := cdb.CreateEvent(ctx, db, event)
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
	now := time.Now()

	if now.Before(state.TimeoutAt) {
		time.Sleep(state.TimeoutAt.Sub(now))
	}

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

	for k, v := range state.Responses {
		names = append(names, k)
		res = append(res, v)
	}

	return HeartbeatReport{
		ParentID:   state.ParentID,
		Total:      len(res),
		Responders: names,
		Raw:        res,
	}
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
			i += 1
			continue
		}

		unit, ok := durationUnits[uStr]
		if !ok {
			i += 1
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
