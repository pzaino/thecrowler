package mail

import (
	"io"
	"time"
)

// HeaderMap contains decoded message headers keyed by their canonical name.
type HeaderMap map[string][]string

// Address is a provider-neutral email address.
type Address struct {
	Name       string `json:"name,omitempty" yaml:"name,omitempty"`
	Address    string `json:"address" yaml:"address"`
	Normalized string `json:"normalized,omitempty" yaml:"normalized,omitempty"`
}

// Mailbox identifies a provider mailbox without exposing a connector-specific
// mailbox object. ID is the provider's stable mailbox identifier when one is
// available; Name is the provider-visible mailbox name.
type Mailbox struct {
	ID   string `json:"id" yaml:"id"`
	Name string `json:"name" yaml:"name"`
}

// MessageRef identifies a message within a provider account and mailbox.
// It is intentionally cheap to list and pass to a message fetcher. UID and
// UIDValidity preserve IMAP identity semantics, while ProviderMessageID and
// ProviderThreadID carry stable identifiers exposed by API-based providers.
type MessageRef struct {
	Provider          string    `json:"provider" yaml:"provider"`
	AccountID         string    `json:"account_id" yaml:"account_id"`
	Mailbox           Mailbox   `json:"mailbox" yaml:"mailbox"`
	UID               uint32    `json:"uid,omitempty" yaml:"uid,omitempty"`
	UIDValidity       uint32    `json:"uid_validity,omitempty" yaml:"uid_validity,omitempty"`
	ProviderMessageID string    `json:"provider_message_id,omitempty" yaml:"provider_message_id,omitempty"`
	ProviderThreadID  string    `json:"provider_thread_id,omitempty" yaml:"provider_thread_id,omitempty"`
	Version           string    `json:"version,omitempty" yaml:"version,omitempty"`
	InternalDate      time.Time `json:"internal_date,omitempty" yaml:"internal_date,omitempty"`
	Flags             []string  `json:"flags,omitempty" yaml:"flags,omitempty"`
	Size              int64     `json:"size,omitempty" yaml:"size,omitempty"`
	Headers           HeaderMap `json:"headers,omitempty" yaml:"headers,omitempty"`
}

// RawMessage pairs provider metadata with a bounded RFC 5322 message stream.
// The caller must close RFC822. The stream is deliberately excluded from
// serialization so raw message content is not emitted accidentally.
type RawMessage struct {
	Ref    MessageRef    `json:"ref" yaml:"ref"`
	RFC822 io.ReadCloser `json:"-" yaml:"-"`
}

// Cursor records provider-neutral incremental listing progress. Token carries
// an opaque cursor or history token for API-based providers. UID and
// UIDValidity carry the equivalent IMAP checkpoint without an IMAP SDK type.
type Cursor struct {
	Token       string `json:"token,omitempty" yaml:"token,omitempty"`
	UID         uint32 `json:"uid,omitempty" yaml:"uid,omitempty"`
	UIDValidity uint32 `json:"uid_validity,omitempty" yaml:"uid_validity,omitempty"`
}

// FetchOptions controls which portions of a message a connector retrieves.
// Headers names the RFC 5322 fields requested for metadata-only operations.
// IncludeBody requests the complete RFC 5322 body, bounded by MaxBytes when it
// is greater than zero.
type FetchOptions struct {
	Headers     []string `json:"headers,omitempty" yaml:"headers,omitempty"`
	IncludeBody bool     `json:"include_body,omitempty" yaml:"include_body,omitempty"`
	MaxBytes    int64    `json:"max_bytes,omitempty" yaml:"max_bytes,omitempty"`
}

// Attachment describes a decoded MIME attachment or inline part. Content is a
// bounded stream when present, and must be closed by the consumer.
type Attachment struct {
	ID                string        `json:"id,omitempty" yaml:"id,omitempty"`
	PartID            string        `json:"part_id,omitempty" yaml:"part_id,omitempty"`
	Filename          string        `json:"filename,omitempty" yaml:"filename,omitempty"`
	MediaType         string        `json:"media_type,omitempty" yaml:"media_type,omitempty"`
	DetectedMediaType string        `json:"detected_media_type,omitempty" yaml:"detected_media_type,omitempty"`
	Disposition       string        `json:"disposition,omitempty" yaml:"disposition,omitempty"`
	ContentID         string        `json:"content_id,omitempty" yaml:"content_id,omitempty"`
	TransferEncoding  string        `json:"transfer_encoding,omitempty" yaml:"transfer_encoding,omitempty"`
	Size              int64         `json:"size,omitempty" yaml:"size,omitempty"`
	SHA256            string        `json:"sha256,omitempty" yaml:"sha256,omitempty"`
	Inline            bool          `json:"inline,omitempty" yaml:"inline,omitempty"`
	ExtractedText     string        `json:"extracted_text,omitempty" yaml:"extracted_text,omitempty"`
	Truncated         bool          `json:"truncated,omitempty" yaml:"truncated,omitempty"`
	Content           io.ReadCloser `json:"-" yaml:"-"`
}

// ParsedMessage contains decoded mail semantics without provider client
// objects or protocol-specific state.
type ParsedMessage struct {
	Ref         MessageRef      `json:"ref" yaml:"ref"`
	MessageID   string          `json:"message_id,omitempty" yaml:"message_id,omitempty"`
	ThreadID    string          `json:"thread_id,omitempty" yaml:"thread_id,omitempty"`
	Date        time.Time       `json:"date,omitempty" yaml:"date,omitempty"`
	From        []Address       `json:"from,omitempty" yaml:"from,omitempty"`
	To          []Address       `json:"to,omitempty" yaml:"to,omitempty"`
	CC          []Address       `json:"cc,omitempty" yaml:"cc,omitempty"`
	BCC         []Address       `json:"bcc,omitempty" yaml:"bcc,omitempty"`
	ReplyTo     []Address       `json:"reply_to,omitempty" yaml:"reply_to,omitempty"`
	Subject     string          `json:"subject,omitempty" yaml:"subject,omitempty"`
	Headers     HeaderMap       `json:"headers,omitempty" yaml:"headers,omitempty"`
	TextBody    string          `json:"text_body,omitempty" yaml:"text_body,omitempty"`
	HTMLBody    string          `json:"html_body,omitempty" yaml:"html_body,omitempty"`
	Attachments []Attachment    `json:"attachments,omitempty" yaml:"attachments,omitempty"`
	Warnings    []ParserWarning `json:"warnings,omitempty" yaml:"warnings,omitempty"`
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
