package mail

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestEmailChangeEventValidation(t *testing.T) {
	t.Parallel()

	valid := validEmailChangeEvent()
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*EmailChangeEvent)
		is     error
	}{
		{name: "missing provider", mutate: func(event *EmailChangeEvent) { event.Provider = "" }, is: ErrInvalidEmailChangeEvent},
		{name: "missing account", mutate: func(event *EmailChangeEvent) { event.AccountID = "" }, is: ErrInvalidEmailChangeEvent},
		{name: "missing mailbox", mutate: func(event *EmailChangeEvent) { event.Mailbox = Mailbox{} }, is: ErrInvalidEmailChangeEvent},
		{name: "empty cursor", mutate: func(event *EmailChangeEvent) { event.Cursor = Cursor{} }, is: ErrInvalidEmailChangeEvent},
		{name: "raw identity", mutate: func(event *EmailChangeEvent) { event.SafeIdentity = "message@example.com" }, is: ErrInvalidEmailChangeEvent},
		{name: "unknown change", mutate: func(event *EmailChangeEvent) { event.ChangeType = "body_changed" }, is: ErrInvalidEmailChangeEvent},
		{name: "unknown listener mode", mutate: func(event *EmailChangeEvent) { event.Metadata.ListenerMode = "socket" }, is: ErrInvalidListenerMode},
		{name: "unknown listener status", mutate: func(event *EmailChangeEvent) { event.Metadata.ListenerStatus = "healthy" }, is: ErrInvalidListenerStatus},
		{name: "received before occurred", mutate: func(event *EmailChangeEvent) { event.Metadata.ReceivedAt = event.Metadata.OccurredAt.Add(-time.Second) }, is: ErrInvalidEmailChangeEvent},
		{name: "control character", mutate: func(event *EmailChangeEvent) { event.AccountID = "account\ncredential" }, is: ErrInvalidEmailChangeEvent},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			event := valid
			test.mutate(&event)
			if err := event.Validate(); !errors.Is(err, test.is) {
				t.Fatalf("Validate() error = %v, want %v", err, test.is)
			}
			if _, err := json.Marshal(event); !errors.Is(err, test.is) {
				t.Fatalf("json.Marshal() error = %v, want %v", err, test.is)
			}
		})
	}
}

func TestEmailChangeEventJSONRoundTripHasClosedSafeSchema(t *testing.T) {
	t.Parallel()

	event := validEmailChangeEvent()
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	serialized := string(encoded)
	for _, forbidden := range []string{
		"message body secret",
		"oauth-access-token",
		"attachment bytes",
		`"body"`,
		`"credentials"`,
		`"attachments"`,
		`"headers"`,
	} {
		if strings.Contains(serialized, forbidden) {
			t.Errorf("serialized event contains forbidden value or field %q: %s", forbidden, serialized)
		}
	}
	for _, required := range []string{`"account_id"`, `"mailbox"`, `"cursor"`, `"safe_identity"`, `"change_type"`, `"metadata"`} {
		if !strings.Contains(serialized, required) {
			t.Errorf("serialized event is missing %s: %s", required, serialized)
		}
	}

	var decoded EmailChangeEvent
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if !reflect.DeepEqual(decoded, event) {
		t.Fatalf("round trip event = %#v, want %#v", decoded, event)
	}
}

func TestEmailChangeEventJSONRejectsSensitiveOrUnknownFields(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(validEmailChangeEvent())
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	tests := []string{
		`"body":"message body secret"`,
		`"credentials":"oauth-access-token"`,
		`"attachments":[{"content":"attachment bytes"}]`,
		`"headers":{"authorization":["bearer secret"]}`,
	}
	for _, field := range tests {
		field := field
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			payload := append([]byte(nil), encoded[:len(encoded)-1]...)
			payload = append(payload, []byte(","+field+"}")...)
			var event EmailChangeEvent
			if err := json.Unmarshal(payload, &event); !errors.Is(err, ErrInvalidEmailChangeEvent) {
				t.Fatalf("json.Unmarshal() error = %v, want ErrInvalidEmailChangeEvent", err)
			}
		})
	}
}

func TestSafeEmailEventIdentityIsDeterministicAndOpaque(t *testing.T) {
	t.Parallel()

	provider := "gmail"
	account := "sensitive.user@example.com"
	mailbox := Mailbox{ID: "provider-secret-label", Name: "Private Mail"}
	identity := SafeEmailEventIdentity(provider, account, mailbox)
	if identity != SafeEmailEventIdentity(provider, account, mailbox) {
		t.Fatal("SafeEmailEventIdentity() is not deterministic")
	}
	if !validSafeIdentity(identity) {
		t.Fatalf("SafeEmailEventIdentity() = %q, want lowercase SHA-256", identity)
	}
	for _, raw := range []string{provider, account, mailbox.ID, mailbox.Name} {
		if strings.Contains(identity, raw) {
			t.Errorf("safe identity %q exposes %q", identity, raw)
		}
	}
}

func TestListenerEnums(t *testing.T) {
	t.Parallel()

	for _, status := range []ListenerStatus{ListenerStatusStopped, ListenerStatusStarting, ListenerStatusRunning, ListenerStatusDegraded, ListenerStatusReconnecting, ListenerStatusFailed} {
		if !status.Valid() {
			t.Errorf("ListenerStatus(%q).Valid() = false", status)
		}
	}
	if ListenerStatus("unknown").Valid() {
		t.Error("unknown ListenerStatus.Valid() = true")
	}
	for _, mode := range []ListenerMode{ListenerModePoll, ListenerModePush, ListenerModeWebhook, ListenerModeIdle} {
		if !mode.Valid() {
			t.Errorf("ListenerMode(%q).Valid() = false", mode)
		}
	}
	if ListenerMode("unknown").Valid() {
		t.Error("unknown ListenerMode.Valid() = true")
	}
}

func validEmailChangeEvent() EmailChangeEvent {
	mailbox := Mailbox{ID: "INBOX", Name: "Inbox"}
	return EmailChangeEvent{
		Provider:     "gmail",
		AccountID:    "account-123",
		Mailbox:      mailbox,
		Cursor:       Cursor{HistoryID: 42},
		SafeIdentity: SafeEmailEventIdentity("gmail", "account-123", mailbox),
		ChangeType:   ChangeUpsert,
		Metadata: EmailEventMetadata{
			EventID:        "notification-123",
			OccurredAt:     time.Date(2026, time.June, 11, 12, 0, 0, 0, time.UTC),
			ReceivedAt:     time.Date(2026, time.June, 11, 12, 0, 1, 0, time.UTC),
			ListenerMode:   ListenerModePush,
			ListenerStatus: ListenerStatusActive,
		},
	}
}
