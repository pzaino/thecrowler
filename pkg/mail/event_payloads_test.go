package mail

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestEmailLifecycleEventTypesFollowConvention(t *testing.T) {
	t.Parallel()

	got := []string{
		EmailEventMessageDiscovered,
		EmailEventMessageFetched,
		EmailEventMessageParsed,
		EmailEventMessageFailed,
		EmailEventMessageCompleted,
		EmailEventListenerStarted,
		EmailEventListenerStopped,
		EmailEventReconciliationCompleted,
	}
	want := []string{
		"email.message_discovered",
		"email.message_fetched",
		"email.message_parsed",
		"email.message_failed",
		"email.message_completed",
		"email.listener_started",
		"email.listener_stopped",
		"email.reconciliation_completed",
	}
	for index := range want {
		if got[index] != want[index] {
			t.Errorf("event type %d = %q, want %q", index, got[index], want[index])
		}
	}
}

func TestEmailLifecyclePayloadsContainOnlySafeIdentitiesAndCounts(t *testing.T) {
	t.Parallel()

	identity := lifecycleMessageIdentity()
	mailboxIdentity := identity.EmailEventIdentity
	payloads := map[string]any{
		EmailEventMessageDiscovered: MessageDiscoveredEventPayload{
			SchemaVersion: EmailEventSchemaVersion, EmailMessageEventIdentity: identity, DiscoveredCount: 1,
		},
		EmailEventMessageFetched: MessageFetchedEventPayload{
			SchemaVersion: EmailEventSchemaVersion, EmailMessageEventIdentity: identity, FetchedCount: 1, FetchedBytes: 2048,
		},
		EmailEventMessageParsed: MessageParsedEventPayload{
			SchemaVersion: EmailEventSchemaVersion, EmailMessageEventIdentity: identity, ParsedCount: 1,
			RecipientCount: 3, HeaderCount: 8, AttachmentCount: 2,
		},
		EmailEventMessageFailed: MessageFailedEventPayload{
			SchemaVersion: EmailEventSchemaVersion, EmailMessageEventIdentity: identity,
			FailedCount: 1, RetryCount: 2,
		},
		EmailEventMessageCompleted: MessageCompletedEventPayload{
			SchemaVersion: EmailEventSchemaVersion, EmailMessageEventIdentity: identity, CompletedCount: 1,
		},
		EmailEventListenerStarted: ListenerStartedEventPayload{
			SchemaVersion: EmailEventSchemaVersion, EmailEventIdentity: mailboxIdentity,
			ListenerCount: 1,
		},
		EmailEventListenerStopped: ListenerStoppedEventPayload{
			SchemaVersion: EmailEventSchemaVersion, EmailEventIdentity: mailboxIdentity,
			ListenerCount: 1,
		},
		EmailEventReconciliationCompleted: ReconciliationCompletedEventPayload{
			SchemaVersion: EmailEventSchemaVersion, EmailEventIdentity: mailboxIdentity,
			DiscoveredCount: 5, FetchedCount: 4, ParsedCount: 4, FailedCount: 1,
			CompletedCount: 3, SkippedCount: 1, QuarantinedCount: 1, RetryCount: 2,
		},
	}

	for eventType, payload := range payloads {
		eventType, payload := eventType, payload
		t.Run(eventType, func(t *testing.T) {
			t.Parallel()
			encoded, err := json.Marshal(payload)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			serialized := string(encoded)
			for _, required := range []string{`"schema_version":"email.lifecycle.v1"`, `"source_id":"source-42"`, `"account_identity"`, `"mailbox_identity"`} {
				if !strings.Contains(serialized, required) {
					t.Errorf("payload missing %s: %s", required, serialized)
				}
			}
			assertNoEmailLifecycleSecret(t, serialized)
		})
	}
}

func TestEmailLifecycleIdentityConstructorsAreOpaqueAndDeterministic(t *testing.T) {
	t.Parallel()

	first := lifecycleMessageIdentity()
	second := lifecycleMessageIdentity()
	if first != second {
		t.Fatalf("identity constructors are not deterministic: %#v != %#v", first, second)
	}
	for name, identity := range map[string]string{
		"account": first.AccountIdentity,
		"mailbox": first.MailboxIdentity,
		"message": first.MessageIdentity,
	} {
		if !validSafeIdentity(identity) {
			t.Errorf("%s identity = %q, want lowercase SHA-256", name, identity)
		}
	}
	if first.MessageIdentity != SafeEmailMessageIdentity(MessageRef{
		Provider: "gmail", AccountID: "sensitive.user@example.com",
		Mailbox:           Mailbox{ID: "provider-mailbox-id", Name: "Private Inbox"},
		ProviderMessageID: "provider-message-id", ProviderThreadID: "provider-thread-id", Version: "provider-version",
	}) {
		t.Fatal("NewEmailMessageEventIdentity() did not use the public safe message identity")
	}
	assertNoEmailLifecycleSecret(t, first.AccountIdentity+first.MailboxIdentity+first.MessageIdentity)
}

func TestEmailLifecyclePayloadValidationRejectsUnsafeIdentityAndSchema(t *testing.T) {
	t.Parallel()

	identity := lifecycleMessageIdentity()
	tests := []struct {
		name    string
		payload any
		want    error
	}{
		{
			name: "raw account identity",
			payload: MessageDiscoveredEventPayload{
				SchemaVersion: EmailEventSchemaVersion,
				EmailMessageEventIdentity: EmailMessageEventIdentity{
					EmailEventIdentity: EmailEventIdentity{SourceID: "source-42", AccountIdentity: "sensitive.user@example.com", MailboxIdentity: identity.MailboxIdentity},
					MessageIdentity:    identity.MessageIdentity,
				},
				DiscoveredCount: 1,
			},
			want: ErrInvalidEmailChangeEvent,
		},
		{
			name: "raw message identity",
			payload: MessageCompletedEventPayload{
				SchemaVersion: EmailEventSchemaVersion, EmailMessageEventIdentity: identity, CompletedCount: 1,
			},
			want: ErrInvalidEmailChangeEvent,
		},
		{
			name: "wrong schema",
			payload: ReconciliationCompletedEventPayload{
				SchemaVersion: "email.lifecycle.v2", EmailEventIdentity: identity.EmailEventIdentity,
			},
			want: ErrInvalidEmailChangeEvent,
		},
	}
	unsafeCompleted := tests[1].payload.(MessageCompletedEventPayload)
	unsafeCompleted.MessageIdentity = "provider-message-id"
	tests[1].payload = unsafeCompleted

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := json.Marshal(test.payload); !errors.Is(err, test.want) {
				t.Fatalf("json.Marshal() error = %v, want %v", err, test.want)
			}
		})
	}
}

func lifecycleMessageIdentity() EmailMessageEventIdentity {
	return NewEmailMessageEventIdentity("source-42", MessageRef{
		Provider:          "gmail",
		AccountID:         "sensitive.user@example.com",
		Mailbox:           Mailbox{ID: "provider-mailbox-id", Name: "Private Inbox"},
		ProviderMessageID: "provider-message-id",
		ProviderThreadID:  "provider-thread-id",
		Version:           "provider-version",
	})
}

func assertNoEmailLifecycleSecret(t *testing.T, serialized string) {
	t.Helper()
	for _, forbidden := range []string{
		"sensitive.user@example.com",
		"provider-mailbox-id",
		"Private Inbox",
		"provider-message-id",
		"provider-thread-id",
		"provider-version",
		"message body secret",
		"oauth-access-token",
		"recipient@example.com",
		"authorization",
		"raw_headers",
		"attachment_content",
		`"body"`,
		`"credentials"`,
		`"recipients"`,
		`"headers"`,
		`"attachments"`,
	} {
		if strings.Contains(serialized, forbidden) {
			t.Errorf("serialized payload contains forbidden value or field %q: %s", forbidden, serialized)
		}
	}
}
