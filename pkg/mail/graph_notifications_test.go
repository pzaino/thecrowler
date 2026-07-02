package mail

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestDecodeGraphChangeNotifications(t *testing.T) {
	t.Parallel()

	payload := graphNotificationPayload(t, graphNotificationValue{
		ID: "notification-1", SubscriptionID: "subscription-1", ClientState: "configured-secret",
		ChangeType: "created", Resource: "users/user/messages/message-1",
		ResourceData: graphNotificationResourceData{ID: "message-1", ETag: `W/"version-1"`},
	})
	events, err := DecodeGraphChangeNotifications(payload, validGraphNotificationConfig())
	if err != nil {
		t.Fatalf("DecodeGraphChangeNotifications() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %#v, want one event", events)
	}
	event := events[0]
	if event.Provider != graphProvider || event.AccountID != "account-1" || event.Mailbox != (Mailbox{ID: "inbox", Name: "Inbox"}) {
		t.Fatalf("event routing = %#v, want configured Graph account and mailbox", event)
	}
	if event.ChangeType != ChangeUpsert || event.Metadata.ListenerMode != ListenerModeWebhook || event.Metadata.ListenerStatus != ListenerStatusActive {
		t.Fatalf("event change metadata = %#v, want active webhook upsert", event)
	}
	if event.Metadata.EventID == "" || event.Cursor.Token != event.Metadata.EventID {
		t.Fatalf("event identity/cursor = %#v, want stable notification identity", event)
	}
	if err := event.Validate(); err != nil {
		t.Fatalf("event.Validate() error = %v", err)
	}
}

func TestDecodeGraphChangeNotificationsRejectsInvalidPayloads(t *testing.T) {
	t.Parallel()

	valid := graphNotificationValue{
		ID: "notification-1", SubscriptionID: "subscription-1", ClientState: "configured-secret",
		ChangeType: "updated", Resource: "users/user/messages/message-1",
	}
	tests := []struct {
		name    string
		payload []byte
		config  GraphNotificationConfig
	}{
		{name: "empty payload", payload: nil, config: validGraphNotificationConfig()},
		{name: "malformed JSON", payload: []byte(`{"value":`), config: validGraphNotificationConfig()},
		{name: "multiple JSON values", payload: []byte(`{"value":[]} {}`), config: validGraphNotificationConfig()},
		{name: "empty collection", payload: []byte(`{"value":[]}`), config: validGraphNotificationConfig()},
		{name: "missing client state", payload: graphNotificationPayload(t, withGraphNotification(valid, func(value *graphNotificationValue) { value.ClientState = "" })), config: validGraphNotificationConfig()},
		{name: "wrong client state", payload: graphNotificationPayload(t, withGraphNotification(valid, func(value *graphNotificationValue) { value.ClientState = "attacker" })), config: validGraphNotificationConfig()},
		{name: "wrong subscription", payload: graphNotificationPayload(t, withGraphNotification(valid, func(value *graphNotificationValue) { value.SubscriptionID = "other" })), config: validGraphNotificationConfig()},
		{name: "missing event kind", payload: graphNotificationPayload(t, withGraphNotification(valid, func(value *graphNotificationValue) { value.ChangeType = "" })), config: validGraphNotificationConfig()},
		{name: "conflicting event kinds", payload: graphNotificationPayload(t, withGraphNotification(valid, func(value *graphNotificationValue) { value.LifecycleEvent = "missed" })), config: validGraphNotificationConfig()},
		{name: "unsupported change", payload: graphNotificationPayload(t, withGraphNotification(valid, func(value *graphNotificationValue) { value.ChangeType = "moved" })), config: validGraphNotificationConfig()},
		{name: "missing resource", payload: graphNotificationPayload(t, withGraphNotification(valid, func(value *graphNotificationValue) { value.Resource = "" })), config: validGraphNotificationConfig()},
		{name: "missing configured secret", payload: graphNotificationPayload(t, valid), config: GraphNotificationConfig{AccountID: "account-1", Mailbox: Mailbox{ID: "inbox"}}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := DecodeGraphChangeNotifications(test.payload, test.config)
			if !errors.Is(err, ErrMalformedGraphChangeNotification) {
				t.Fatalf("DecodeGraphChangeNotifications() error = %v, want ErrMalformedGraphChangeNotification", err)
			}
		})
	}
}

func TestDecodeGraphChangeNotificationsMapsLifecycleEvents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		lifecycle string
		status    ListenerStatus
	}{
		{lifecycle: "reauthorizationRequired", status: ListenerStatusDegraded},
		{lifecycle: "missed", status: ListenerStatusDegraded},
		{lifecycle: "subscriptionRemoved", status: ListenerStatusStopped},
	}
	for _, test := range tests {
		test := test
		t.Run(test.lifecycle, func(t *testing.T) {
			t.Parallel()
			payload := graphNotificationPayload(t, graphNotificationValue{
				SubscriptionID: "subscription-1", ClientState: "configured-secret", LifecycleEvent: test.lifecycle,
			})
			events, err := DecodeGraphChangeNotifications(payload, validGraphNotificationConfig())
			if err != nil {
				t.Fatalf("DecodeGraphChangeNotifications() error = %v", err)
			}
			if len(events) != 1 || events[0].ChangeType != ChangeReset || events[0].Metadata.ListenerStatus != test.status {
				t.Fatalf("events = %#v, want reset with status %q", events, test.status)
			}
		})
	}
}

func TestDecodeGraphChangeNotificationsHandlesMultipleValuesAndDuplicates(t *testing.T) {
	t.Parallel()

	first := graphNotificationValue{
		ID: "notification-1", SubscriptionID: "subscription-1", ClientState: "configured-secret",
		ChangeType: "created", Resource: "users/user/messages/message-1",
	}
	second := graphNotificationValue{
		ID: "notification-2", SubscriptionID: "subscription-1", ClientState: "configured-secret",
		ChangeType: "deleted", Resource: "users/user/messages/message-2",
	}
	payload := graphNotificationPayload(t, first, first, second)
	events, err := DecodeGraphChangeNotifications(payload, validGraphNotificationConfig())
	if err != nil {
		t.Fatalf("DecodeGraphChangeNotifications() error = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want duplicate coalesced to two events", len(events))
	}
	if got := []ChangeKind{events[0].ChangeType, events[1].ChangeType}; !reflect.DeepEqual(got, []ChangeKind{ChangeUpsert, ChangeDelete}) {
		t.Fatalf("change kinds = %v, want [upsert delete]", got)
	}
}

func TestGraphChangeNotificationReceiverDeduplicatesAcceptedDeliveries(t *testing.T) {
	t.Parallel()

	queue := &recordingEmailChangeQueue{}
	receiver, err := NewGraphChangeNotificationReceiver(queue, validGraphNotificationConfig())
	if err != nil {
		t.Fatalf("NewGraphChangeNotificationReceiver() error = %v", err)
	}
	payload := graphNotificationPayload(t, graphNotificationValue{
		ID: "notification-1", SubscriptionID: "subscription-1", ClientState: "configured-secret",
		ChangeType: "updated", Resource: "users/user/messages/message-1",
	})
	if accepted, err := receiver.Handle(context.Background(), payload); err != nil || accepted != 1 {
		t.Fatalf("first Handle() = (%d, %v), want (1, nil)", accepted, err)
	}
	if accepted, err := receiver.Handle(context.Background(), payload); err != nil || accepted != 0 {
		t.Fatalf("duplicate Handle() = (%d, %v), want (0, nil)", accepted, err)
	}
	if len(queue.events) != 1 {
		t.Fatalf("queued events = %d, want 1", len(queue.events))
	}
}

func TestGraphChangeNotificationReceiverRetriesFailedEnqueue(t *testing.T) {
	t.Parallel()

	queue := &recordingEmailChangeQueue{err: errors.New("queue unavailable")}
	receiver, err := NewGraphChangeNotificationReceiver(queue, validGraphNotificationConfig())
	if err != nil {
		t.Fatalf("NewGraphChangeNotificationReceiver() error = %v", err)
	}
	payload := graphNotificationPayload(t, graphNotificationValue{
		ID: "notification-1", SubscriptionID: "subscription-1", ClientState: "configured-secret",
		ChangeType: "created", Resource: "users/user/messages/message-1",
	})
	if accepted, err := receiver.Handle(context.Background(), payload); err == nil || accepted != 0 {
		t.Fatalf("failed Handle() = (%d, %v), want queue error", accepted, err)
	}
	queue.err = nil
	if accepted, err := receiver.Handle(context.Background(), payload); err != nil || accepted != 1 {
		t.Fatalf("retry Handle() = (%d, %v), want (1, nil)", accepted, err)
	}
}

type graphNotificationValue struct {
	ID             string                        `json:"id,omitempty"`
	SubscriptionID string                        `json:"subscriptionId,omitempty"`
	ClientState    string                        `json:"clientState,omitempty"`
	ChangeType     string                        `json:"changeType,omitempty"`
	LifecycleEvent string                        `json:"lifecycleEvent,omitempty"`
	Resource       string                        `json:"resource,omitempty"`
	ResourceData   graphNotificationResourceData `json:"resourceData,omitempty"`
}

type graphNotificationResourceData struct {
	ID   string `json:"id,omitempty"`
	ETag string `json:"@odata.etag,omitempty"`
}

func validGraphNotificationConfig() GraphNotificationConfig {
	return GraphNotificationConfig{
		AccountID: "account-1", Mailbox: Mailbox{ID: "inbox", Name: "Inbox"},
		ClientState: "configured-secret", SubscriptionID: "subscription-1",
	}
}

func graphNotificationPayload(t *testing.T, values ...graphNotificationValue) []byte {
	t.Helper()
	payload, err := json.Marshal(struct {
		Value []graphNotificationValue `json:"value"`
	}{Value: values})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return payload
}

func withGraphNotification(value graphNotificationValue, mutate func(*graphNotificationValue)) graphNotificationValue {
	mutate(&value)
	return value
}
