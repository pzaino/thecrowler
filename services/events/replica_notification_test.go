package main

import (
	"encoding/json"
	"testing"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func TestHandleReplicaNotificationRespondsToHeartbeat(t *testing.T) {
	previousResponder := replicaHeartbeatResponder
	var received []cdb.Event
	replicaHeartbeatResponder = func(event cdb.Event) error {
		received = append(received, event)
		return nil
	}
	t.Cleanup(func() { replicaHeartbeatResponder = previousResponder })

	event := cdb.Event{ID: "heartbeat-id", Type: "crowler_heartbeat"}
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	handleReplicaNotification(string(payload))

	if len(received) != 1 {
		t.Fatalf("heartbeat responder calls = %d, want 1", len(received))
	}
	if received[0].ID != event.ID || received[0].Type != event.Type {
		t.Errorf("heartbeat responder event = %#v, want %#v", received[0], event)
	}
}

func TestHandleReplicaNotificationIgnoresAllOtherEventCategories(t *testing.T) {
	previousResponder := replicaHeartbeatResponder
	responderCalls := 0
	replicaHeartbeatResponder = func(cdb.Event) error {
		responderCalls++
		return nil
	}
	t.Cleanup(func() { replicaHeartbeatResponder = previousResponder })

	queueLengths := func() [4]int {
		return [4]int{len(jobQueue), len(internalQ), len(externalQ), len(apiEventQ)}
	}
	before := queueLengths()

	tests := []struct {
		name      string
		eventType string
	}{
		{name: "heartbeat response aggregation", eventType: "crowler_heartbeat_response"},
		{name: "system action", eventType: "system_event"},
		{name: "application dispatch", eventType: "source_created"},
		{name: "plugin execution", eventType: "plugin_event"},
		{name: "agent execution", eventType: "agent_event"},
		{name: "future heartbeat report", eventType: "crowler_heartbeat_report"},
		{name: "unknown", eventType: "unexpected_event_type"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload, err := json.Marshal(cdb.Event{ID: "ignored-id", Type: test.eventType})
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			handleReplicaNotification(string(payload))
		})
	}

	if responderCalls != 0 {
		t.Errorf("heartbeat responder calls = %d, want 0", responderCalls)
	}
	if after := queueLengths(); after != before {
		t.Errorf("queue lengths changed from %v to %v; ignored events reached downstream processing", before, after)
	}
}

func TestHandleReplicaNotificationRejectsMalformedJSON(t *testing.T) {
	previousResponder := replicaHeartbeatResponder
	responderCalls := 0
	replicaHeartbeatResponder = func(cdb.Event) error {
		responderCalls++
		return nil
	}
	t.Cleanup(func() { replicaHeartbeatResponder = previousResponder })

	before := [4]int{len(jobQueue), len(internalQ), len(externalQ), len(apiEventQ)}
	handleReplicaNotification(`{"event_type":`)
	after := [4]int{len(jobQueue), len(internalQ), len(externalQ), len(apiEventQ)}

	if responderCalls != 0 {
		t.Errorf("heartbeat responder calls = %d, want 0", responderCalls)
	}
	if after != before {
		t.Errorf("queue lengths changed from %v to %v; malformed JSON reached downstream processing", before, after)
	}
}
