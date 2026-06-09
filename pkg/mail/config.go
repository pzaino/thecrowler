package mail

import "time"

// Mode controls how mailbox changes trigger reconciliation.
type Mode string

const (
	// ModePoll discovers changes on a schedule.
	ModePoll Mode = "poll"
	// ModeListen uses provider notifications as reconciliation hints while
	// retaining polling as a safety net.
	ModeListen Mode = "listen"
)

// Config contains provider-neutral settings for a read-only email source.
// Endpoint must not contain credentials; CredentialRef identifies secret
// material managed outside this package.
type Config struct {
	Provider      string          `json:"provider" yaml:"provider"`
	Endpoint      string          `json:"endpoint" yaml:"endpoint"`
	CredentialRef string          `json:"credential_ref" yaml:"credential_ref"`
	Mailboxes     MailboxSelector `json:"mailboxes,omitempty" yaml:"mailboxes,omitempty"`
	Mode          Mode            `json:"mode,omitempty" yaml:"mode,omitempty"`
	TLS           TLSConfig       `json:"tls,omitempty" yaml:"tls,omitempty"`
	Timeout       time.Duration   `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	Limits        Limits          `json:"limits,omitempty" yaml:"limits,omitempty"`
}

// MailboxSelector controls which provider mailboxes are ingested. Exclusions
// take precedence when a mailbox matches both lists.
type MailboxSelector struct {
	Include []string `json:"include,omitempty" yaml:"include,omitempty"`
	Exclude []string `json:"exclude,omitempty" yaml:"exclude,omitempty"`
}

// TLSConfig controls transport security. TLS certificate verification is
// enabled unless InsecureSkipVerify is explicitly set for development use.
type TLSConfig struct {
	InsecureSkipVerify bool   `json:"insecure_skip_verify,omitempty" yaml:"insecure_skip_verify,omitempty"`
	ServerName         string `json:"server_name,omitempty" yaml:"server_name,omitempty"`
}

// Limits bounds resource consumption while retrieving and parsing untrusted
// messages.
type Limits struct {
	MaxMessageBytes    int64 `json:"max_message_bytes,omitempty" yaml:"max_message_bytes,omitempty"`
	MaxAttachmentBytes int64 `json:"max_attachment_bytes,omitempty" yaml:"max_attachment_bytes,omitempty"`
	MaxAttachments     int   `json:"max_attachments,omitempty" yaml:"max_attachments,omitempty"`
	MaxHeaderBytes     int64 `json:"max_header_bytes,omitempty" yaml:"max_header_bytes,omitempty"`
	MaxMIMEDepth       int   `json:"max_mime_depth,omitempty" yaml:"max_mime_depth,omitempty"`
	MaxMIMEParts       int   `json:"max_mime_parts,omitempty" yaml:"max_mime_parts,omitempty"`
}
