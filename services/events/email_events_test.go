package main

import (
	"context"
	"errors"
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
			queued := withTestAPIEventQueue(t, 1)
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
				completeTestAPIEvent()
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
			queued := withTestAPIEventQueue(t, 1)
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
			queued := withTestAPIEventQueue(t, 1)
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

func TestCreateEventHandlerRepliesImmediatelyWhenQueueIsFull(t *testing.T) {
	withTestAPIEventQueue(t, 0)

	response := httptest.NewRecorder()
	createEventHandler(response, httptest.NewRequest(http.MethodPost, "/v1/event/create", strings.NewReader(`{"event_type":"new"}`)))

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body = %q", response.Code, http.StatusServiceUnavailable, response.Body.String())
	}
	if response.Header().Get("Retry-After") == "" {
		t.Fatal("missing Retry-After header")
	}
}

func withTestAPIEventQueue(t *testing.T, capacity int) chan cdb.Event {
	t.Helper()
	previousQueue := apiEventQ
	previousAccepting := apiEventsAccepting.Load()
	queue := make(chan cdb.Event, capacity)
	apiEventQ = queue
	apiEventsAccepting.Store(true)
	t.Cleanup(func() {
		for len(queue) > 0 {
			<-queue
			completeTestAPIEvent()
		}
		apiEventQ = previousQueue
		apiEventsAccepting.Store(previousAccepting)
	})
	return queue
}

func completeTestAPIEvent() {
	apiEventJobs.Done()
}

func TestPersistQueuedAPIEventRetriesUntilSuccess(t *testing.T) {
	previous := createQueuedAPIEvent
	attempts := 0
	createQueuedAPIEvent = func(context.Context, cdb.Event) error {
		attempts++
		if attempts < 3 {
			return errors.New("temporary database failure")
		}
		return nil
	}
	t.Cleanup(func() { createQueuedAPIEvent = previous })

	if !persistQueuedAPIEventUntilSuccess(cdb.Event{ID: "event-id", Type: "test"}) {
		t.Fatal("event was not persisted")
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}
