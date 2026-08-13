package main

import (
	"encoding/json"
	"os"
	"testing"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// installNotificationObservers replaces the two narrow notification seams. It
// lets these tests exercise listener routing without starting production
// workers or touching the database.
func installNotificationObservers(t *testing.T) (*int, *int, *[]cdb.Event) {
	t.Helper()
	oldEnqueue := enqueueNotificationEvent
	oldResponder := replicaHeartbeatResponder
	normalCalls, heartbeatCalls := 0, 0
	seen := []cdb.Event{}
	enqueueNotificationEvent = func(event cdb.Event) bool {
		normalCalls++
		seen = append(seen, event)
		return true
	}
	replicaHeartbeatResponder = func(event cdb.Event) error {
		heartbeatCalls++
		seen = append(seen, event)
		return nil
	}
	t.Cleanup(func() {
		enqueueNotificationEvent = oldEnqueue
		replicaHeartbeatResponder = oldResponder
	})
	return &normalCalls, &heartbeatCalls, &seen
}

func eventPayload(t *testing.T, event cdb.Event) string {
	t.Helper()
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return string(payload)
}

func queueState() [4]int {
	return [4]int{len(jobQueue), len(internalQ), len(externalQ), len(apiEventQ)}
}

func TestMasterAndReplicaOrdinaryNotificationRouting(t *testing.T) {
	normalCalls, heartbeatCalls, _ := installNotificationObservers(t)
	event := cdb.Event{ID: "ordinary-id", Type: "source_created"}
	payload := eventPayload(t, event)

	handleNotification(payload)
	if *normalCalls != 1 {
		t.Fatalf("master normal-processing calls = %d, want 1", *normalCalls)
	}

	before := queueState()
	handleReplicaNotification(payload)
	if *heartbeatCalls != 0 {
		t.Errorf("replica heartbeat responses = %d, want 0", *heartbeatCalls)
	}
	if *normalCalls != 1 {
		t.Errorf("normal-processing calls after replica = %d, want unchanged", *normalCalls)
	}
	if after := queueState(); after != before {
		t.Errorf("worker queue state changed from %v to %v", before, after)
	}
}

func TestMasterHeartbeatNotificationCreatesOneCanonicalResponse(t *testing.T) {
	t.Setenv("MICROSERVICE_NAME", "crowler-events")
	oldCreate := createHeartbeatEvent
	oldEnqueue := enqueueNotificationEvent
	var responses []cdb.Event
	createHeartbeatEvent = func(_ *cdb.Handler, event cdb.Event) (string, error) {
		responses = append(responses, event)
		return "response-id", nil
	}
	enqueueNotificationEvent = func(cdb.Event) bool { return true }
	t.Cleanup(func() {
		createHeartbeatEvent = oldCreate
		enqueueNotificationEvent = oldEnqueue
	})

	handleNotification(eventPayload(t, cdb.Event{ID: "heartbeat-parent", Type: "crowler_heartbeat"}))
	assertHeartbeatResponse(t, responses, "heartbeat-parent", "crowler-events")
}

func TestReplicaHeartbeatNotificationUsesCurrentIdentity(t *testing.T) {
	t.Setenv("MICROSERVICE_NAME", "crowler-events-2")
	oldCreate := createHeartbeatEvent
	oldResponder := replicaHeartbeatResponder
	var responses []cdb.Event
	createHeartbeatEvent = func(_ *cdb.Handler, event cdb.Event) (string, error) {
		responses = append(responses, event)
		return "response-id", nil
	}
	replicaHeartbeatResponder = func(event cdb.Event) error {
		_, err := respondToHeartbeat(&dbHandler, event)
		return err
	}
	t.Cleanup(func() {
		createHeartbeatEvent = oldCreate
		replicaHeartbeatResponder = oldResponder
	})

	handleReplicaNotification(eventPayload(t, cdb.Event{ID: "heartbeat-parent", Type: "crowler_heartbeat"}))
	assertHeartbeatResponse(t, responses, "heartbeat-parent", "crowler-events-2")
}

func assertHeartbeatResponse(t *testing.T, responses []cdb.Event, parent, originName string) {
	t.Helper()
	if len(responses) != 1 {
		t.Fatalf("heartbeat responses = %d, want exactly 1", len(responses))
	}
	response := responses[0]
	if response.Type != "crowler_heartbeat_response" {
		t.Errorf("response type = %q, want crowler_heartbeat_response", response.Type)
	}
	if got := response.Details["parent_event_id"]; got != parent {
		t.Errorf("parent_event_id = %#v, want %q", got, parent)
	}
	if got := response.Details["origin_type"]; got != "crowler-events" {
		t.Errorf("origin_type = %#v, want crowler-events", got)
	}
	if got := response.Details["origin_name"]; got != originName {
		t.Errorf("origin_name = %#v, want %q", got, originName)
	}
}

func TestHeartbeatResponseIdentityVariants(t *testing.T) {
	original, existed := os.LookupEnv("MICROSERVICE_NAME")
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv("MICROSERVICE_NAME", original)
		} else {
			_ = os.Unsetenv("MICROSERVICE_NAME")
		}
	})

	for _, identity := range []string{"crowler-events", "crowler-events-1", "crowler-events-2"} {
		t.Run(identity, func(t *testing.T) {
			if err := os.Setenv("MICROSERVICE_NAME", identity); err != nil {
				t.Fatal(err)
			}
			oldCreate := createHeartbeatEvent
			var responses []cdb.Event
			createHeartbeatEvent = func(_ *cdb.Handler, event cdb.Event) (string, error) {
				responses = append(responses, event)
				return "response-id", nil
			}
			t.Cleanup(func() {
				createHeartbeatEvent = oldCreate
				if existed {
					_ = os.Setenv("MICROSERVICE_NAME", original)
				} else {
					_ = os.Unsetenv("MICROSERVICE_NAME")
				}
			})
			if _, err := respondToHeartbeat(&dbHandler, cdb.Event{ID: "parent", Type: "crowler_heartbeat"}); err != nil {
				t.Fatal(err)
			}
			assertHeartbeatResponse(t, responses, "parent", identity)
		})
	}
}

func TestReplicaSuppressesExecutionOrientedNotifications(t *testing.T) {
	normalCalls, heartbeatCalls, _ := installNotificationObservers(t)
	before := queueState()
	for _, eventType := range []string{"system_event", "plugin_event", "agent_event", "crowler_heartbeat_response"} {
		handleReplicaNotification(eventPayload(t, cdb.Event{
			ID: eventType, Type: eventType, Details: map[string]interface{}{"action": "update_debug_level", "level": "debug"},
		}))
	}
	if *normalCalls != 0 {
		t.Errorf("normal/plugin/agent/system dispatch calls = %d, want 0", *normalCalls)
	}
	if *heartbeatCalls != 0 {
		t.Errorf("heartbeat responder calls = %d, want 0", *heartbeatCalls)
	}
	if after := queueState(); after != before {
		t.Errorf("worker queue state changed from %v to %v", before, after)
	}
}

func TestMasterPluginAndAgentNotificationsReachNormalDispatcher(t *testing.T) {
	normalCalls, heartbeatCalls, seen := installNotificationObservers(t)
	for _, eventType := range []string{"plugin_event", "agent_event"} {
		handleNotification(eventPayload(t, cdb.Event{ID: eventType, Type: eventType}))
	}
	if *normalCalls != 2 || len(*seen) != 2 {
		t.Fatalf("master dispatch calls = %d, events = %d; want 2 and 2", *normalCalls, len(*seen))
	}
	if *heartbeatCalls != 0 {
		t.Errorf("heartbeat responses = %d, want 0", *heartbeatCalls)
	}
}

func TestMalformedNotificationsHaveNoSideEffects(t *testing.T) {
	normalCalls, heartbeatCalls, _ := installNotificationObservers(t)
	before := queueState()
	for name, handler := range map[string]func(string){"master": handleNotification, "replica": handleReplicaNotification} {
		t.Run(name, func(t *testing.T) { handler(`{"event_type":`) })
	}
	if *normalCalls != 0 || *heartbeatCalls != 0 {
		t.Errorf("malformed input calls: normal=%d heartbeat=%d, want zero", *normalCalls, *heartbeatCalls)
	}
	if after := queueState(); after != before {
		t.Errorf("worker queue state changed from %v to %v", before, after)
	}
}

func TestMasterHeartbeatResponseRetainsAggregationInterception(t *testing.T) {
	oldEnqueue := enqueueNotificationEvent
	var dispatched []cdb.Event
	enqueueNotificationEvent = func(event cdb.Event) bool {
		dispatched = append(dispatched, event)
		return true
	}
	t.Cleanup(func() { enqueueNotificationEvent = oldEnqueue })

	heartbeatMu.Lock()
	oldActive := activeHeartbeat
	activeHeartbeat = &HeartbeatState{ParentID: "active-parent", Responses: make(map[cdb.FleetMember]cdb.Event)}
	heartbeatMu.Unlock()
	t.Cleanup(func() {
		heartbeatMu.Lock()
		activeHeartbeat = oldActive
		heartbeatMu.Unlock()
	})

	event := cdb.Event{ID: "response-id", Type: "crowler_heartbeat_response", Details: map[string]interface{}{
		"parent_event_id": "active-parent", "origin_type": "crowler-events", "origin_name": "crowler-events-1",
	}}
	handleNotification(eventPayload(t, event))
	if len(dispatched) != 1 {
		t.Fatalf("master dispatcher calls = %d, want 1", len(dispatched))
	}
	if !maybeHandleHeartbeatResponse(dispatched[0]) {
		t.Fatal("active heartbeat response was not intercepted")
	}
	heartbeatMu.Lock()
	got := activeHeartbeat.Responses[cdb.FleetMember{OriginType: "crowler-events", OriginName: "crowler-events-1"}]
	heartbeatMu.Unlock()
	if got.ID != event.ID {
		t.Errorf("aggregated response ID = %q, want %q", got.ID, event.ID)
	}
}
