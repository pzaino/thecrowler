package mail

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPipelineLogHookEmitsStructuredRedactedEvents(t *testing.T) {
	t.Parallel()

	const (
		sourceID    = "source-42"
		mailboxID   = "provider-mailbox-secret"
		mailboxName = "Executive Inbox"
		providerID  = "provider-message-secret"
		accountID   = "account-secret"
		bodySecret  = "confidential body text"
		tokenSecret = "Bearer token-secret"
		recipients  = "alice@example.test,bob@example.test"
	)
	mailbox := Mailbox{ID: mailboxID, Name: mailboxName}
	ref := MessageRef{
		Provider:          "fake",
		AccountID:         accountID,
		Mailbox:           mailbox,
		ProviderMessageID: providerID,
		Version:           "version-secret",
		Headers: HeaderMap{
			"Authorization": {tokenSecret},
			"To":            {recipients},
		},
	}
	connector := &pipelineFakeConnector{
		mailboxes: []Mailbox{mailbox},
		pages: map[Cursor]ChangePage{
			{}: {Changes: []Change{{Kind: ChangeUpsert, Ref: ref}}, Next: Cursor{Token: "cursor-secret"}},
		},
	}
	processor := &pipelineFakeProcessor{failures: map[string]error{
		providerID: &Error{Kind: ErrorMalformed, Message: bodySecret + " " + tokenSecret + " " + recipients},
	}}
	pipeline := NewPipeline(connector, NewMemoryStateStore(), processor, &pipelineFakeEmitter{})
	pipeline.SourceID = sourceID
	pipeline.RetryPolicy = RetryPolicy{MaxAttempts: 1}

	var nowCalls int
	pipeline.Now = func() time.Time {
		nowCalls++
		return time.Unix(int64(nowCalls), 0)
	}
	var events []LogEvent
	pipeline.LogHook = func(_ context.Context, event LogEvent) {
		events = append(events, event)
	}

	if err := pipeline.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("log event count = %d, want 4: %#v", len(events), events)
	}

	messageStarted := events[1]
	messageFinished := events[2]
	if messageStarted.Operation != logOperationMessage || messageStarted.State != LogStateStarted || messageStarted.Duration != 0 {
		t.Fatalf("message started event = %#v", messageStarted)
	}
	if messageFinished.State != LogStateDiscarded || messageFinished.ErrorCategory != LogErrorMalformed {
		t.Fatalf("message finished event = %#v, want discarded/malformed", messageFinished)
	}
	if messageFinished.SourceID != sourceID || messageFinished.MailboxID == "" || messageFinished.MessageID == "" {
		t.Fatalf("message identifiers missing from event: %#v", messageFinished)
	}
	if messageFinished.Duration <= 0 {
		t.Fatalf("message duration = %v, want positive", messageFinished.Duration)
	}
	if events[3].Operation != logOperationMailbox || events[3].State != LogStateSucceeded || events[3].Duration <= 0 {
		t.Fatalf("mailbox finished event = %#v", events[3])
	}

	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatalf("json.Marshal(events) error = %v", err)
	}
	logOutput := string(encoded)
	for _, secret := range []string{
		mailboxID, mailboxName, providerID, accountID, "version-secret", "cursor-secret",
		bodySecret, tokenSecret, recipients, "alice@example.test", "bob@example.test", "Authorization",
	} {
		if strings.Contains(logOutput, secret) {
			t.Errorf("structured log contains secret %q: %s", secret, logOutput)
		}
	}
}

func TestLogEventJSONContainsOnlyAllowlistedFields(t *testing.T) {
	t.Parallel()

	event := LogEvent{
		Operation:     logOperationMessage,
		SourceID:      "source",
		MailboxID:     strings.Repeat("a", 64),
		MessageID:     strings.Repeat("b", 64),
		State:         LogStateFailed,
		Duration:      time.Second,
		ErrorCategory: LogErrorAuthentication,
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("json.Marshal(event) error = %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("json.Unmarshal(event) error = %v", err)
	}
	allowed := map[string]bool{
		"operation": true, "source_id": true, "mailbox_id": true, "message_id": true,
		"state": true, "duration": true, "error_category": true,
	}
	for field := range fields {
		if !allowed[field] {
			t.Errorf("unexpected structured log field %q", field)
		}
	}
	if len(fields) != len(allowed) {
		t.Fatalf("structured log fields = %v, want %v", fields, allowed)
	}
}

func TestLogHookPanicDoesNotInterruptPipeline(t *testing.T) {
	t.Parallel()

	mailbox := Mailbox{ID: "inbox"}
	connector := &pipelineFakeConnector{
		mailboxes: []Mailbox{mailbox},
		pages:     map[Cursor]ChangePage{{}: {Next: Cursor{Token: "done"}}},
	}
	pipeline := NewPipeline(connector, NewMemoryStateStore(), &pipelineFakeProcessor{}, &pipelineFakeEmitter{})
	pipeline.LogHook = func(context.Context, LogEvent) { panic(errors.New("logger unavailable")) }

	if err := pipeline.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v, want logging panic isolated", err)
	}
}

func TestSafeLogIdentitiesAreDeterministicAndOpaque(t *testing.T) {
	t.Parallel()

	mailbox := Mailbox{ID: "secret-mailbox"}
	ref := MessageRef{Provider: "provider", AccountID: "secret-account", Mailbox: mailbox, ProviderMessageID: "secret-message", Version: "secret-version"}

	mailboxIdentity := safeMailboxIdentity(mailbox)
	messageIdentity := safeMessageIdentity(ref)
	if len(mailboxIdentity) != 64 || len(messageIdentity) != 64 {
		t.Fatalf("safe identity lengths = %d, %d; want SHA-256 hex", len(mailboxIdentity), len(messageIdentity))
	}
	if mailboxIdentity != safeMailboxIdentity(mailbox) || messageIdentity != safeMessageIdentity(ref) {
		t.Fatal("safe log identities are not deterministic")
	}
	for _, identity := range []string{mailboxIdentity, messageIdentity} {
		for _, secret := range []string{"secret-mailbox", "secret-account", "secret-message", "secret-version"} {
			if strings.Contains(identity, secret) {
				t.Errorf("safe identity %q contains raw value %q", identity, secret)
			}
		}
	}
}
