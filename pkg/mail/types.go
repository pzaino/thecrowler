package mail

import (
	"io"
	"time"
)

// HeaderMap contains decoded message headers keyed by their canonical name.
type HeaderMap map[string][]string

// Address is a provider-neutral email address.
type Address struct {
	Name    string `json:"name,omitempty" yaml:"name,omitempty"`
	Address string `json:"address" yaml:"address"`
}

// MessageRef identifies a message within a provider account and mailbox.
// It is intentionally cheap to list and pass to a message fetcher.
type MessageRef struct {
	Provider     string    `json:"provider" yaml:"provider"`
	AccountID    string    `json:"account_id" yaml:"account_id"`
	MailboxID    string    `json:"mailbox_id" yaml:"mailbox_id"`
	ProviderID   string    `json:"provider_id" yaml:"provider_id"`
	Version      string    `json:"version,omitempty" yaml:"version,omitempty"`
	InternalDate time.Time `json:"internal_date,omitempty" yaml:"internal_date,omitempty"`
	Size         int64     `json:"size,omitempty" yaml:"size,omitempty"`
}

// RawMessage pairs provider metadata with a bounded RFC 5322 message stream.
// The caller must close RFC822.
type RawMessage struct {
	Ref    MessageRef
	RFC822 io.ReadCloser
}

// Attachment describes a decoded MIME attachment or inline part. Content is a
// bounded stream when present, and must be closed by the consumer.
type Attachment struct {
	ID          string        `json:"id,omitempty" yaml:"id,omitempty"`
	Filename    string        `json:"filename,omitempty" yaml:"filename,omitempty"`
	MediaType   string        `json:"media_type,omitempty" yaml:"media_type,omitempty"`
	Disposition string        `json:"disposition,omitempty" yaml:"disposition,omitempty"`
	ContentID   string        `json:"content_id,omitempty" yaml:"content_id,omitempty"`
	Size        int64         `json:"size,omitempty" yaml:"size,omitempty"`
	Content     io.ReadCloser `json:"-" yaml:"-"`
}

// ParsedMessage contains decoded mail semantics without provider client
// objects or protocol-specific state.
type ParsedMessage struct {
	Ref         MessageRef   `json:"ref" yaml:"ref"`
	MessageID   string       `json:"message_id,omitempty" yaml:"message_id,omitempty"`
	ThreadID    string       `json:"thread_id,omitempty" yaml:"thread_id,omitempty"`
	Date        time.Time    `json:"date,omitempty" yaml:"date,omitempty"`
	From        []Address    `json:"from,omitempty" yaml:"from,omitempty"`
	To          []Address    `json:"to,omitempty" yaml:"to,omitempty"`
	CC          []Address    `json:"cc,omitempty" yaml:"cc,omitempty"`
	BCC         []Address    `json:"bcc,omitempty" yaml:"bcc,omitempty"`
	ReplyTo     []Address    `json:"reply_to,omitempty" yaml:"reply_to,omitempty"`
	Subject     string       `json:"subject,omitempty" yaml:"subject,omitempty"`
	Headers     HeaderMap    `json:"headers,omitempty" yaml:"headers,omitempty"`
	TextBody    string       `json:"text_body,omitempty" yaml:"text_body,omitempty"`
	HTMLBody    string       `json:"html_body,omitempty" yaml:"html_body,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty" yaml:"attachments,omitempty"`
}

// ChangeKind identifies a provider-neutral mailbox change.
type ChangeKind string

const (
	// ChangeUpsert indicates that a message was added or changed.
	ChangeUpsert ChangeKind = "upsert"
	// ChangeDelete indicates that a message was removed.
	ChangeDelete ChangeKind = "delete"
	// ChangeReset indicates that the current checkpoint can no longer be used.
	ChangeReset ChangeKind = "reset"
)

// Change represents a message mutation or a request for full reconciliation.
type Change struct {
	Kind ChangeKind `json:"kind" yaml:"kind"`
	Ref  MessageRef `json:"ref" yaml:"ref"`
}
