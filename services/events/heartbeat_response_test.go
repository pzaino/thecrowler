package main

import (
	"testing"
	"time"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func TestRespondToHeartbeatPersistsExpectedResponse(t *testing.T) {
	t.Setenv("MICROSERVICE_NAME", "crowler-events-replica-2")
	previousCreate := createHeartbeatEvent
	var persisted []cdb.Event
	createHeartbeatEvent = func(_ *cdb.Handler, event cdb.Event) (string, error) {
		persisted = append(persisted, event)
		return "response-id", nil
	}
	t.Cleanup(func() { createHeartbeatEvent = previousCreate })

	before := time.Now().Add(-time.Second)
	id, err := respondToHeartbeat(new(cdb.Handler), cdb.Event{ID: "heartbeat-id", Type: "crowler_heartbeat"})
	after := time.Now().Add(time.Second)
	if err != nil {
		t.Fatalf("respondToHeartbeat() error = %v", err)
	}
	if id != "response-id" {
		t.Fatalf("response ID = %q, want response-id", id)
	}
	if len(persisted) != 1 {
		t.Fatalf("persisted responses = %d, want 1", len(persisted))
	}

	response := persisted[0]
	if response.Type != "crowler_heartbeat_response" {
		t.Errorf("outer event type = %q", response.Type)
	}
	wantDetails := map[string]string{
		"parent_event_id": "heartbeat-id",
		"origin_type":     "crowler-events",
		"origin_name":     "crowler-events-replica-2",
		"status":          "ok",
		"type":            "heartbeat_response",
	}
	for key, want := range wantDetails {
		if got := response.Details[key]; got != want {
			t.Errorf("details[%q] = %#v, want %q", key, got, want)
		}
	}

	originTime, ok := response.Details["origin_time"].(string)
	if !ok {
		t.Fatalf("origin_time = %#v, want RFC3339 string", response.Details["origin_time"])
	}
	parsedOriginTime, err := time.Parse(time.RFC3339, originTime)
	if err != nil {
		t.Fatalf("origin_time %q is not RFC3339: %v", originTime, err)
	}
	if parsedOriginTime.Before(before) || parsedOriginTime.After(after) {
		t.Errorf("origin_time %v outside test interval [%v, %v]", parsedOriginTime, before, after)
	}
	if response.Timestamp != originTime || response.CreatedAt != originTime || response.LastUpdatedAt != originTime {
		t.Errorf("standard timestamps do not match origin_time: %#v", response)
	}
}

func TestRespondToHeartbeatDoesNotDependOnMasterSelection(t *testing.T) {
	t.Setenv("MICROSERVICE_NAME", "crowler-events-replica")
	previousMaster := config.Events.MasterEventsManager
	config.Events.MasterEventsManager = "a-different-master"
	t.Cleanup(func() { config.Events.MasterEventsManager = previousMaster })

	previousCreate := createHeartbeatEvent
	calls := 0
	createHeartbeatEvent = func(_ *cdb.Handler, _ cdb.Event) (string, error) {
		calls++
		return "response-id", nil
	}
	t.Cleanup(func() { createHeartbeatEvent = previousCreate })

	if _, err := respondToHeartbeat(new(cdb.Handler), cdb.Event{ID: "heartbeat-id", Type: "crowler_heartbeat"}); err != nil {
		t.Fatalf("replica respondToHeartbeat() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("persistence calls = %d, want 1", calls)
	}
}
