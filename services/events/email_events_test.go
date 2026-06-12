package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cdb "github.com/pzaino/thecrowler/pkg/database"
	mail "github.com/pzaino/thecrowler/pkg/mail"
)

const testEmailIdentity = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestCreateEventHandlerAcceptsEmailLifecycleEventTypes(t *testing.T) {
	eventTypes := []string{
		mail.EmailEventMessageDiscovered,
		mail.EmailEventMessageFetched,
		mail.EmailEventMessageParsed,
		mail.EmailEventMessageFailed,
		mail.EmailEventMessageCompleted,
		mail.EmailEventListenerStarted,
		mail.EmailEventListenerStopped,
		mail.EmailEventReconciliationCompleted,
	}

	for _, eventType := range eventTypes {
		t.Run(eventType, func(t *testing.T) {
			queued := withTestEventQueue(t)
			body := `{
				"event_type": "` + eventType + `",
				"details": {
					"schema_version": "email.lifecycle.v1",
					"source_id": "42",
					"account_identity": "` + testEmailIdentity + `",
					"mailbox_identity": "` + testEmailIdentity + `",
					"message_identity": "` + testEmailIdentity + `"
				}
			}`

			response := httptest.NewRecorder()
			createEventHandler(response, httptest.NewRequest(http.MethodPost, "/v1/event/create", strings.NewReader(body)))

			if response.Code != http.StatusCreated {
				t.Fatalf("status = %d, want %d; body = %q", response.Code, http.StatusCreated, response.Body.String())
			}
			select {
			case event := <-queued:
				if event.Type != eventType {
					t.Fatalf("queued event type = %q, want %q", event.Type, eventType)
				}
				if event.Action != actionInsert {
					t.Fatalf("queued action = %q, want %q", event.Action, actionInsert)
				}
			default:
				t.Fatal("accepted event was not queued")
			}
		})
	}
}

func TestCreateEventHandlerRejectsMalformedEmailLifecyclePayloads(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "missing required lifecycle fields",
			body: `{"event_type":"email.message_discovered","details":{"schema_version":"email.lifecycle.v1"}}`,
		},
		{
			name: "wrong schema version",
			body: `{"event_type":"email.listener_started","details":{"schema_version":"v0","source_id":"42","account_identity":"` + testEmailIdentity + `","mailbox_identity":"` + testEmailIdentity + `"}}`,
		},
		{
			name: "invalid field type",
			body: `{"event_type":"email.message_fetched","details":{"schema_version":"email.lifecycle.v1","source_id":"42","account_identity":"` + testEmailIdentity + `","mailbox_identity":"` + testEmailIdentity + `","message_identity":"` + testEmailIdentity + `","fetched_count":"one"}}`,
		},
		{
			name: "malformed JSON",
			body: `{"event_type":"email.message_completed","details":`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			queued := withTestEventQueue(t)
			response := httptest.NewRecorder()
			createEventHandler(response, httptest.NewRequest(http.MethodPost, "/v1/event/create", strings.NewReader(test.body)))

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body = %q", response.Code, http.StatusBadRequest, response.Body.String())
			}
			if len(queued) != 0 {
				t.Fatalf("queue length = %d, want 0", len(queued))
			}
		})
	}
}

func TestCreateEventHandlerPreservesGenericEventCompatibility(t *testing.T) {
	for _, eventType := range []string{"legacy.custom_event", "email.custom_event"} {
		t.Run(eventType, func(t *testing.T) {
			queued := withTestEventQueue(t)
			body := `{"event_type":"` + eventType + `","details":{"schema_version":"legacy","payload":"unchanged"}}`
			response := httptest.NewRecorder()
			createEventHandler(response, httptest.NewRequest(http.MethodPost, "/v1/event/create", strings.NewReader(body)))

			if response.Code != http.StatusCreated {
				t.Fatalf("status = %d, want %d; body = %q", response.Code, http.StatusCreated, response.Body.String())
			}
			if len(queued) != 1 {
				t.Fatalf("queue length = %d, want 1", len(queued))
			}
		})
	}
}

func withTestEventQueue(t *testing.T) chan cdb.Event {
	t.Helper()
	previous := jobQueue
	queue := make(chan cdb.Event, 1)
	jobQueue = queue
	t.Cleanup(func() {
		jobQueue = previous
	})
	return queue
}
