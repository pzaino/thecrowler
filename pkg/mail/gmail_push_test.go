package mail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

type recordingEmailChangeQueue struct {
	events []EmailChangeEvent
	err    error
}

func (q *recordingEmailChangeQueue) Enqueue(_ context.Context, event EmailChangeEvent) error {
	if q.err != nil {
		return q.err
	}
	q.events = append(q.events, event)
	return nil
}

func TestDecodeGmailPushNotification(t *testing.T) {
	t.Parallel()

	payload := gmailPushTestPayload(t, "User.Name+alerts@Example.COM", "9876543210")
	event, err := DecodeGmailPushNotification(payload)
	if err != nil {
		t.Fatalf("DecodeGmailPushNotification() error = %v", err)
	}

	want := EmailChangeEvent{
		Provider:  "gmail",
		AccountID: "user.name+alerts@example.com",
		Cursor:    Cursor{HistoryID: 9876543210},
	}
	if !reflect.DeepEqual(event, want) {
		t.Fatalf("event = %#v, want %#v", event, want)
	}
}

func TestDecodeGmailPushNotificationRejectsMalformedPayloads(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload []byte
	}{
		{name: "empty"},
		{name: "invalid envelope JSON", payload: []byte(`{"message":`)},
		{name: "missing data", payload: []byte(`{"message":{}}`)},
		{name: "invalid base64", payload: []byte(`{"message":{"data":"%%%"}}`)},
		{name: "invalid notification JSON", payload: gmailPushEnvelope(t, []byte(`{"emailAddress":`))},
		{name: "missing account", payload: gmailPushTestPayload(t, "", "42")},
		{name: "display name account", payload: gmailPushTestPayload(t, "Attacker <user@example.com>", "42")},
		{name: "control character account", payload: gmailPushTestPayload(t, "user@example.com\nBcc:other@example.com", "42")},
		{name: "missing history", payload: gmailPushTestPayload(t, "user@example.com", "")},
		{name: "zero history", payload: gmailPushTestPayload(t, "user@example.com", "0")},
		{name: "negative history", payload: gmailPushTestPayload(t, "user@example.com", "-1")},
		{name: "non-numeric history", payload: gmailPushTestPayload(t, "user@example.com", "newest")},
		{name: "overflowing history", payload: gmailPushTestPayload(t, "user@example.com", "18446744073709551616")},
		{name: "trailing notification JSON", payload: gmailPushEnvelope(t, []byte(`{"emailAddress":"user@example.com","historyId":"42"}{}`))},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := DecodeGmailPushNotification(test.payload)
			if !errors.Is(err, ErrMalformedGmailPushNotification) {
				t.Fatalf("DecodeGmailPushNotification() error = %v, want ErrMalformedGmailPushNotification", err)
			}
		})
	}
}

func TestGmailPushReceiverEnqueuesNewerDeltaEvents(t *testing.T) {
	t.Parallel()

	queue := &recordingEmailChangeQueue{}
	receiver, err := NewGmailPushReceiver(queue)
	if err != nil {
		t.Fatalf("NewGmailPushReceiver() error = %v", err)
	}

	tests := []struct {
		name      string
		account   string
		historyID string
		wantQueue bool
	}{
		{name: "initial", account: "user@example.com", historyID: "100", wantQueue: true},
		{name: "duplicate", account: "USER@example.com", historyID: "100", wantQueue: false},
		{name: "out of order", account: "user@example.com", historyID: "99", wantQueue: false},
		{name: "newer", account: "user@example.com", historyID: "101", wantQueue: true},
		{name: "separate account", account: "other@example.com", historyID: "50", wantQueue: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			queued, handleErr := receiver.Handle(context.Background(), gmailPushTestPayload(t, test.account, test.historyID))
			if handleErr != nil {
				t.Fatalf("Handle() error = %v", handleErr)
			}
			if queued != test.wantQueue {
				t.Fatalf("Handle() queued = %t, want %t", queued, test.wantQueue)
			}
		})
	}

	want := []EmailChangeEvent{
		{Provider: "gmail", AccountID: "user@example.com", Cursor: Cursor{HistoryID: 100}},
		{Provider: "gmail", AccountID: "user@example.com", Cursor: Cursor{HistoryID: 101}},
		{Provider: "gmail", AccountID: "other@example.com", Cursor: Cursor{HistoryID: 50}},
	}
	if !reflect.DeepEqual(queue.events, want) {
		t.Fatalf("queued events = %#v, want %#v", queue.events, want)
	}
}

func TestGmailPushReceiverRetriesAfterQueueFailure(t *testing.T) {
	t.Parallel()

	queue := &recordingEmailChangeQueue{err: errors.New("queue unavailable")}
	receiver, err := NewGmailPushReceiver(queue)
	if err != nil {
		t.Fatalf("NewGmailPushReceiver() error = %v", err)
	}
	payload := gmailPushTestPayload(t, "user@example.com", "100")

	if queued, handleErr := receiver.Handle(context.Background(), payload); handleErr == nil || queued {
		t.Fatalf("first Handle() = (%t, %v), want enqueue error", queued, handleErr)
	}
	queue.err = nil
	if queued, handleErr := receiver.Handle(context.Background(), payload); handleErr != nil || !queued {
		t.Fatalf("retry Handle() = (%t, %v), want successful enqueue", queued, handleErr)
	}
}

func gmailPushTestPayload(t *testing.T, account, historyID string) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]string{"emailAddress": account, "historyId": historyID})
	if err != nil {
		t.Fatalf("marshal Gmail notification: %v", err)
	}
	return gmailPushEnvelope(t, data)
}

func gmailPushEnvelope(t *testing.T, data []byte) []byte {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"message": map[string]any{
			"data":      base64.StdEncoding.EncodeToString(data),
			"messageId": "pubsub-message-1",
		},
		"subscription": "projects/test/subscriptions/gmail",
	})
	if err != nil {
		t.Fatalf("marshal Pub/Sub envelope: %v", err)
	}
	return payload
}

var _ EmailChangeQueue = (*recordingEmailChangeQueue)(nil)
