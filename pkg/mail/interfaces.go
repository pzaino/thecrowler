package mail

import "context"

// ChangePage is one bounded page of provider-neutral mailbox changes. Next is
// safe to persist only after every change in Changes has been durably handled.
type ChangePage struct {
	Changes []Change `json:"changes,omitempty" yaml:"changes,omitempty"`
	Next    Cursor   `json:"next,omitempty" yaml:"next,omitempty"`
	More    bool     `json:"more,omitempty" yaml:"more,omitempty"`
}

// MailboxKey identifies durable ingestion state without exposing a provider
// SDK mailbox or session object.
type MailboxKey struct {
	SourceID  string  `json:"source_id" yaml:"source_id"`
	AccountID string  `json:"account_id" yaml:"account_id"`
	Mailbox   Mailbox `json:"mailbox" yaml:"mailbox"`
}

// Checkpoint is the durable progress for one mailbox. Cursor carries the
// provider cursor or IMAP UIDVALIDITY and last UID. The message fields record
// the outcome of the most recently handled message. Version is an opaque
// state-store value used to reject stale commits.
type Checkpoint struct {
	Cursor        Cursor        `json:"cursor" yaml:"cursor"`
	MessageStatus MessageStatus `json:"message_status,omitempty" yaml:"message_status,omitempty"`
	ContentHash   string        `json:"content_hash,omitempty" yaml:"content_hash,omitempty"`
	ErrorCount    uint32        `json:"error_count,omitempty" yaml:"error_count,omitempty"`
	LastError     string        `json:"last_error,omitempty" yaml:"last_error,omitempty"`
	Version       string        `json:"version,omitempty" yaml:"version,omitempty"`
}

// Connector exposes only the read-only mailbox operations needed by mail
// ingestion. Implementations translate protocol pagination and identifiers
// into the provider-neutral types in this package.
type Connector interface {
	ListMailboxes(ctx context.Context) ([]Mailbox, error)
	ListChanges(ctx context.Context, mailbox Mailbox, cursor Cursor, limit int) (ChangePage, error)
	OpenMessage(ctx context.Context, ref MessageRef, options FetchOptions) (RawMessage, error)
}

// StateStore owns durable mailbox progress independently of provider sessions
// and source configuration. CommitCheckpoint must reject a stale previous
// version rather than overwrite newer progress.
type StateStore interface {
	LoadCheckpoint(ctx context.Context, key MailboxKey) (Checkpoint, error)
	CommitCheckpoint(ctx context.Context, key MailboxKey, previousVersion string, next Checkpoint) error
}

// Parser decodes one RFC 5322 message and its MIME parts into provider-neutral
// values. Implementations must not expose parser-library types to callers.
type Parser interface {
	Parse(ctx context.Context, message RawMessage) (ParsedMessage, error)
}

// Processor converts one bounded raw message into a provider-neutral document.
type Processor interface {
	Process(ctx context.Context, message RawMessage) (Document, error)
}

// Emitter durably publishes a normalized document to the downstream index or
// storage boundary.
type Emitter interface {
	Emit(ctx context.Context, document Document) error
}

// Listener converts provider notifications into mailbox-scoped hints. Hints
// are advisory and must not advance a checkpoint directly.
type Listener interface {
	Listen(ctx context.Context, mailboxes []MailboxKey, sink EventSink) error
}

// EventSink accepts a provider-neutral hint that a mailbox should be
// reconciled. Implementations should coalesce or bound duplicate hints.
type EventSink interface {
	Notify(ctx context.Context, mailbox MailboxKey) error
}

// Reconciler converges one mailbox with its durable downstream state.
type Reconciler interface {
	Reconcile(ctx context.Context, mailbox MailboxKey) error
}
