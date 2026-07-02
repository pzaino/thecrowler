package mail

import (
	"context"
	"testing"
)

type fakeConnector struct {
	listMailboxes func(context.Context) ([]Mailbox, error)
	listChanges   func(context.Context, Mailbox, Cursor, int) (ChangePage, error)
	openMessage   func(context.Context, MessageRef, FetchOptions) (RawMessage, error)
}

func (f fakeConnector) ListMailboxes(ctx context.Context) ([]Mailbox, error) {
	return f.listMailboxes(ctx)
}

func (f fakeConnector) ListChanges(ctx context.Context, mailbox Mailbox, cursor Cursor, limit int) (ChangePage, error) {
	return f.listChanges(ctx, mailbox, cursor, limit)
}

func (f fakeConnector) OpenMessage(ctx context.Context, ref MessageRef, options FetchOptions) (RawMessage, error) {
	return f.openMessage(ctx, ref, options)
}

type fakeStateStore struct {
	loadCheckpoint   func(context.Context, MailboxKey) (Checkpoint, error)
	commitCheckpoint func(context.Context, MailboxKey, string, Checkpoint) error
}

func (f fakeStateStore) LoadCheckpoint(ctx context.Context, key MailboxKey) (Checkpoint, error) {
	return f.loadCheckpoint(ctx, key)
}

func (f fakeStateStore) CommitCheckpoint(ctx context.Context, key MailboxKey, previousVersion string, next Checkpoint) error {
	return f.commitCheckpoint(ctx, key, previousVersion, next)
}

type fakeProcessor struct {
	process func(context.Context, RawMessage) (Document, error)
}

func (f fakeProcessor) Process(ctx context.Context, message RawMessage) (Document, error) {
	return f.process(ctx, message)
}

type fakeEmitter struct {
	emit func(context.Context, Document) error
}

func (f fakeEmitter) Emit(ctx context.Context, document Document) error {
	return f.emit(ctx, document)
}

type fakeListener struct {
	listen func(context.Context, []MailboxKey, EventSink) error
}

func (f fakeListener) Listen(ctx context.Context, mailboxes []MailboxKey, sink EventSink) error {
	return f.listen(ctx, mailboxes, sink)
}

type fakeEventSink struct {
	notify func(context.Context, MailboxKey) error
}

func (f fakeEventSink) Notify(ctx context.Context, mailbox MailboxKey) error {
	return f.notify(ctx, mailbox)
}

type fakeReconciler struct {
	reconcile func(context.Context, MailboxKey) error
}

func (f fakeReconciler) Reconcile(ctx context.Context, mailbox MailboxKey) error {
	return f.reconcile(ctx, mailbox)
}

var (
	_ Connector  = fakeConnector{}
	_ StateStore = fakeStateStore{}
	_ Processor  = fakeProcessor{}
	_ Emitter    = fakeEmitter{}
	_ Listener   = fakeListener{}
	_ EventSink  = fakeEventSink{}
	_ Reconciler = fakeReconciler{}
)

func TestInterfacesPassContextAndProviderNeutralValues(t *testing.T) {
	t.Parallel()

	type contextKey string
	const key contextKey = "request"
	ctx := context.WithValue(context.Background(), key, "mail-crawl")
	mailboxKey := MailboxKey{
		SourceID:  "source-1",
		AccountID: "account-1",
		Mailbox:   Mailbox{ID: "inbox", Name: "Inbox"},
	}

	assertContext := func(got context.Context) {
		t.Helper()
		if value := got.Value(key); value != "mail-crawl" {
			t.Fatalf("context value = %v, want mail-crawl", value)
		}
	}

	sink := fakeEventSink{notify: func(got context.Context, gotKey MailboxKey) error {
		assertContext(got)
		if gotKey != mailboxKey {
			t.Fatalf("mailbox key = %#v, want %#v", gotKey, mailboxKey)
		}
		return nil
	}}
	listener := fakeListener{listen: func(got context.Context, mailboxes []MailboxKey, gotSink EventSink) error {
		assertContext(got)
		if len(mailboxes) != 1 || mailboxes[0] != mailboxKey {
			t.Fatalf("mailboxes = %#v, want [%#v]", mailboxes, mailboxKey)
		}
		return gotSink.Notify(got, mailboxes[0])
	}}
	reconciler := fakeReconciler{reconcile: func(got context.Context, gotKey MailboxKey) error {
		assertContext(got)
		if gotKey != mailboxKey {
			t.Fatalf("mailbox key = %#v, want %#v", gotKey, mailboxKey)
		}
		return nil
	}}

	if err := listener.Listen(ctx, []MailboxKey{mailboxKey}, sink); err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	if err := reconciler.Reconcile(ctx, mailboxKey); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
}
