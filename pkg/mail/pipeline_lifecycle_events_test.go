package mail

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type lifecycleFakeSink struct {
	mu       sync.Mutex
	events   []LifecycleEvent
	attempts map[string]int
	emit     func(LifecycleEvent, int) error
}

func (sink *lifecycleFakeSink) EmitLifecycleEvent(_ context.Context, event LifecycleEvent) error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.attempts == nil {
		sink.attempts = make(map[string]int)
	}
	sink.attempts[event.Type]++
	attempt := sink.attempts[event.Type]
	sink.events = append(sink.events, event)
	if sink.emit != nil {
		return sink.emit(event, attempt)
	}
	return nil
}

func (sink *lifecycleFakeSink) snapshot() []LifecycleEvent {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return append([]LifecycleEvent(nil), sink.events...)
}

type lifecycleProcessorFunc func(context.Context, RawMessage) (Document, error)

func (fn lifecycleProcessorFunc) Process(ctx context.Context, message RawMessage) (Document, error) {
	return fn(ctx, message)
}

func TestPipelineEmitsLifecycleEventsInOrder(t *testing.T) {
	mailbox := Mailbox{ID: "mailbox-id", Name: "Inbox"}
	ref := pipelineTestRef(mailbox, "message-id")
	ref.Size = 123
	connector := &pipelineFakeConnector{
		mailboxes: []Mailbox{mailbox},
		pages: map[Cursor]ChangePage{
			{}: {Changes: []Change{{Kind: ChangeUpsert, Ref: ref}}, Next: Cursor{Token: "done"}},
		},
	}
	sink := &lifecycleFakeSink{}
	pipeline := NewPipeline(connector, NewMemoryStateStore(), &pipelineFakeProcessor{}, &pipelineFakeEmitter{})
	pipeline.SourceID = "source-42"
	pipeline.Provider = "fake"
	pipeline.AccountID = "account"
	pipeline.LifecycleEventSink = sink

	if err := pipeline.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	events := sink.snapshot()
	got := make([]string, len(events))
	for index := range events {
		got[index] = events[index].Type
	}
	want := []string{
		EmailEventListenerStarted,
		EmailEventMessageDiscovered,
		EmailEventMessageFetched,
		EmailEventMessageParsed,
		EmailEventMessageCompleted,
		EmailEventReconciliationCompleted,
		EmailEventListenerStopped,
	}
	if !equalStrings(got, want) {
		t.Fatalf("event order = %v, want %v", got, want)
	}

	var completed ReconciliationCompletedEventPayload
	if err := json.Unmarshal(events[5].Details, &completed); err != nil {
		t.Fatalf("json.Unmarshal(reconciliation) error = %v", err)
	}
	if completed.DiscoveredCount != 1 || completed.FetchedCount != 1 || completed.ParsedCount != 1 || completed.CompletedCount != 1 || completed.FailedCount != 0 {
		t.Fatalf("reconciliation payload = %#v, want one successful message", completed)
	}
}

func TestPipelineLifecycleSinkFailureDoesNotCorruptIngestion(t *testing.T) {
	mailbox := Mailbox{ID: "mailbox-id"}
	ref := pipelineTestRef(mailbox, "message-id")
	store := NewMemoryStateStore()
	connector := &pipelineFakeConnector{
		mailboxes: []Mailbox{mailbox},
		pages: map[Cursor]ChangePage{
			{}: {Changes: []Change{{Kind: ChangeUpsert, Ref: ref}}, Next: Cursor{Token: "committed"}},
		},
	}
	emitter := &pipelineFakeEmitter{}
	sink := &lifecycleFakeSink{emit: func(LifecycleEvent, int) error { return errors.New("event backend unavailable") }}
	pipeline := NewPipeline(connector, store, &pipelineFakeProcessor{}, emitter)
	pipeline.SourceID = "source-42"
	pipeline.Provider = "fake"
	pipeline.AccountID = "account"
	pipeline.LifecycleEventSink = sink

	if err := pipeline.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v, lifecycle failures must be observational", err)
	}
	if got, want := emitter.emittedIDs(), []string{"message-id"}; !equalStrings(got, want) {
		t.Fatalf("emitted IDs = %v, want %v", got, want)
	}
	checkpoint, err := store.LoadCheckpoint(context.Background(), MailboxKey{
		SourceID: "source-42", Provider: "fake", AccountID: "account", Mailbox: mailbox,
	})
	if err != nil {
		t.Fatalf("LoadCheckpoint() error = %v", err)
	}
	if checkpoint.Cursor.Token != "committed" || checkpoint.ErrorCount != 0 {
		t.Fatalf("checkpoint = %#v, want successful committed cursor", checkpoint)
	}
}

func TestPipelineRetriesTransientLifecycleSinkFailuresOnly(t *testing.T) {
	mailbox := Mailbox{ID: "mailbox-id"}
	ref := pipelineTestRef(mailbox, "message-id")
	connector := &pipelineFakeConnector{
		mailboxes: []Mailbox{mailbox},
		pages: map[Cursor]ChangePage{
			{}: {Changes: []Change{{Kind: ChangeUpsert, Ref: ref}}, Next: Cursor{Token: "done"}},
		},
	}
	sink := &lifecycleFakeSink{emit: func(event LifecycleEvent, attempt int) error {
		if event.Type == EmailEventMessageDiscovered && attempt < 3 {
			return &Error{Kind: ErrorTransient, Message: "temporary event failure"}
		}
		return nil
	}}
	var delays []time.Duration
	pipeline := NewPipeline(connector, NewMemoryStateStore(), &pipelineFakeProcessor{}, &pipelineFakeEmitter{})
	pipeline.SourceID = "source-42"
	pipeline.Provider = "fake"
	pipeline.AccountID = "account"
	pipeline.LifecycleEventSink = sink
	pipeline.LifecycleEventRetryPolicy = RetryPolicy{MaxAttempts: 3, InitialBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond}
	pipeline.Sleep = func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		return nil
	}

	if err := pipeline.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := sink.attempts[EmailEventMessageDiscovered]; got != 3 {
		t.Fatalf("message_discovered attempts = %d, want 3", got)
	}
	if got, want := delays, []time.Duration{time.Millisecond, 2 * time.Millisecond}; !equalDurations(got, want) {
		t.Fatalf("event retry delays = %v, want %v", got, want)
	}
	if got := len(connector.openedIDs()); got != 1 {
		t.Fatalf("OpenMessage() calls = %d, want 1; event retries must not retry ingestion", got)
	}
}

func TestPipelineLifecycleEventsRedactSensitiveMailData(t *testing.T) {
	mailbox := Mailbox{ID: "private-mailbox-id", Name: "Private Inbox"}
	ref := MessageRef{
		Provider: "gmail", AccountID: "sensitive.user@example.com", Mailbox: mailbox,
		ProviderMessageID: "provider-message-id", ProviderThreadID: "provider-thread-id", Version: "provider-version",
		Headers:  HeaderMap{"Authorization": {"Bearer oauth-access-token"}},
		Envelope: &MessageEnvelope{Subject: "message body secret", To: []Address{{Address: "recipient@example.com"}}},
	}
	connector := &pipelineFakeConnector{
		mailboxes: []Mailbox{mailbox},
		pages: map[Cursor]ChangePage{
			{}: {Changes: []Change{{Kind: ChangeUpsert, Ref: ref}}, Next: Cursor{Token: "secret-cursor"}},
		},
	}
	processor := lifecycleProcessorFunc(func(_ context.Context, raw RawMessage) (Document, error) {
		return Document{
			ID: "document-id", Ref: raw.Ref, Subject: "message body secret",
			TextBody: "oauth-access-token", To: []Address{{Address: "recipient@example.com"}},
			Headers:     HeaderSet{Values: HeaderMap{"Authorization": {"Bearer oauth-access-token"}}},
			Attachments: []Attachment{{Filename: "secret.txt", ExtractedText: "attachment_content"}},
		}, nil
	})
	sink := &lifecycleFakeSink{}
	pipeline := NewPipeline(connector, NewMemoryStateStore(), processor, &pipelineFakeEmitter{})
	pipeline.SourceID = "source-42"
	pipeline.Provider = "gmail"
	pipeline.AccountID = "sensitive.user@example.com"
	pipeline.LifecycleEventSink = sink

	if err := pipeline.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	encoded, err := json.Marshal(sink.snapshot())
	if err != nil {
		t.Fatalf("json.Marshal(events) error = %v", err)
	}
	serialized := string(encoded)
	for _, forbidden := range []string{
		"sensitive.user@example.com", "private-mailbox-id", "Private Inbox", "provider-message-id",
		"provider-thread-id", "provider-version", "message body secret", "oauth-access-token",
		"recipient@example.com", "secret-cursor", "secret.txt", "attachment_content", "Authorization",
	} {
		if strings.Contains(serialized, forbidden) {
			t.Errorf("lifecycle events contain sensitive value %q: %s", forbidden, serialized)
		}
	}
}
