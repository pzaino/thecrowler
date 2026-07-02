package config

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

// SourceConfig describes a complete, provider-neutral mail source. Provider
// adapters may accept additional settings through Extensions, but portable
// settings belong in one of the typed sections below.
type SourceConfig struct {
	Connector      ConnectorConfig      `json:"connector" yaml:"connector"`
	Auth           AuthConfig           `json:"auth,omitempty" yaml:"auth,omitempty"`
	Mailboxes      MailboxConfig        `json:"mailboxes,omitempty" yaml:"mailboxes,omitempty"`
	Crawl          CrawlConfig          `json:"crawl,omitempty" yaml:"crawl,omitempty"`
	Extraction     ExtractionConfig     `json:"extraction,omitempty" yaml:"extraction,omitempty"`
	Listener       ListenerConfig       `json:"listener,omitempty" yaml:"listener,omitempty"`
	Reconciliation ReconciliationConfig `json:"reconciliation,omitempty" yaml:"reconciliation,omitempty"`
	Safety         SafetyConfig         `json:"safety,omitempty" yaml:"safety,omitempty"`
	Extensions     map[string]any       `json:"extensions,omitempty" yaml:"extensions,omitempty"`
}

// SafetyConfig contains explicit opt-ins for behavior that can cross the mail
// source's trust boundary. Unsupported active-content and mutation capabilities
// remain invalid even when requested; unrestricted remote link following must
// be acknowledged separately from enabling remote link following itself.
type SafetyConfig struct {
	AllowRemoteResources   bool `json:"allow_remote_resources,omitempty" yaml:"allow_remote_resources,omitempty"`
	AllowJavaScript        bool `json:"allow_javascript,omitempty" yaml:"allow_javascript,omitempty"`
	AllowMailboxMutation   bool `json:"allow_mailbox_mutation,omitempty" yaml:"allow_mailbox_mutation,omitempty"`
	AllowUnrestrictedLinks bool `json:"allow_unrestricted_links,omitempty" yaml:"allow_unrestricted_links,omitempty"`
}

// ConnectorConfig identifies the connector and its network endpoint. Endpoint
// must not contain credentials. Connector-specific options belong in
// Extensions rather than in provider-specific fields on this type.
type ConnectorConfig struct {
	Provider   string         `json:"provider" yaml:"provider"`
	Endpoint   string         `json:"endpoint" yaml:"endpoint"`
	ProxyURL   string         `json:"proxy_url,omitempty" yaml:"proxy_url,omitempty"`
	TLS        TLSConfig      `json:"tls,omitempty" yaml:"tls,omitempty"`
	Timeout    time.Duration  `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	Extensions map[string]any `json:"extensions,omitempty" yaml:"extensions,omitempty"`
}

// AuthConfig refers to authentication material held by an external secret
// store. It must never contain passwords, access tokens, or client secrets.
type AuthConfig struct {
	Method        string         `json:"method,omitempty" yaml:"method,omitempty"`
	CredentialRef string         `json:"credential_ref" yaml:"credential_ref"`
	Identity      string         `json:"identity,omitempty" yaml:"identity,omitempty"`
	Extensions    map[string]any `json:"extensions,omitempty" yaml:"extensions,omitempty"`
}

// MailboxConfig selects mailboxes for ingestion. Exclusions take precedence
// when a mailbox matches both lists.
type MailboxConfig struct {
	Include    []string       `json:"include,omitempty" yaml:"include,omitempty"`
	Exclude    []string       `json:"exclude,omitempty" yaml:"exclude,omitempty"`
	Extensions map[string]any `json:"extensions,omitempty" yaml:"extensions,omitempty"`
}

// CrawlConfig controls bounded message retrieval.
type CrawlConfig struct {
	Mode        Mode           `json:"mode,omitempty" yaml:"mode,omitempty"`
	BatchSize   int            `json:"batch_size,omitempty" yaml:"batch_size,omitempty"`
	MaxMessages int            `json:"max_messages,omitempty" yaml:"max_messages,omitempty"`
	Timeout     time.Duration  `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	Limits      Limits         `json:"limits,omitempty" yaml:"limits,omitempty"`
	Extensions  map[string]any `json:"extensions,omitempty" yaml:"extensions,omitempty"`
}

// ExtractionConfig controls which message content is emitted by the mail
// normalizer. It does not authorize dereferencing remote content.
type ExtractionConfig struct {
	IncludeHeaders []string `json:"include_headers,omitempty" yaml:"include_headers,omitempty"`
	ExcludeHeaders []string `json:"exclude_headers,omitempty" yaml:"exclude_headers,omitempty"`
	PreferHTML     bool     `json:"prefer_html,omitempty" yaml:"prefer_html,omitempty"`
	// CleanupHTML removes conservative email-only artifacts from the temporary
	// DOM used for text and link extraction while preserving HTMLBody verbatim.
	CleanupHTML bool             `json:"cleanup_html,omitempty" yaml:"cleanup_html,omitempty"`
	Links       LinkPolicy       `json:"links,omitempty" yaml:"links,omitempty"`
	Attachments AttachmentPolicy `json:"attachments,omitempty" yaml:"attachments,omitempty"`
	Extensions  map[string]any   `json:"extensions,omitempty" yaml:"extensions,omitempty"`
}

// LinkPolicy controls link extraction from message bodies. FollowRemote is
// deliberately explicit because fetching remote mail content is unsafe by
// default.
type LinkPolicy struct {
	Extract        bool     `json:"extract,omitempty" yaml:"extract,omitempty"`
	FollowRemote   bool     `json:"follow_remote,omitempty" yaml:"follow_remote,omitempty"`
	AllowedSchemes []string `json:"allowed_schemes,omitempty" yaml:"allowed_schemes,omitempty"`
	// Allowlist and Denylist contain exact hosts or wildcard subdomains such as
	// *.example.com. A denylist match always wins.
	Allowlist           []string       `json:"allowlist,omitempty" yaml:"allowlist,omitempty"`
	Denylist            []string       `json:"denylist,omitempty" yaml:"denylist,omitempty"`
	MaxLinksPerMessage  int            `json:"max_links_per_message,omitempty" yaml:"max_links_per_message,omitempty"`
	SuppressUnsubscribe bool           `json:"suppress_unsubscribe,omitempty" yaml:"suppress_unsubscribe,omitempty"`
	Extensions          map[string]any `json:"extensions,omitempty" yaml:"extensions,omitempty"`
}

// AttachmentPolicy controls attachment emission and optional downstream text
// extraction. MIME types and filename extensions remain untrusted input.
type AttachmentPolicy struct {
	Include       bool `json:"include,omitempty" yaml:"include,omitempty"`
	IncludeInline bool `json:"include_inline,omitempty" yaml:"include_inline,omitempty"`
	// Download exposes policy-approved attachment bytes to crawler artifacts as
	// base64. It is opt-in because binary payloads increase index size and may
	// contain malicious content.
	Download          bool           `json:"download,omitempty" yaml:"download,omitempty"`
	ExtractText       bool           `json:"extract_text,omitempty" yaml:"extract_text,omitempty"`
	AllowedMediaTypes []string       `json:"allowed_media_types,omitempty" yaml:"allowed_media_types,omitempty"`
	BlockedMediaTypes []string       `json:"blocked_media_types,omitempty" yaml:"blocked_media_types,omitempty"`
	Extensions        map[string]any `json:"extensions,omitempty" yaml:"extensions,omitempty"`
}

// ListenerConfig controls optional change-hint listening. Notifications only
// request reconciliation and never advance durable progress.
type ListenerConfig struct {
	Enabled             bool           `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	BufferSize          int            `json:"buffer_size,omitempty" yaml:"buffer_size,omitempty"`
	CoalesceWindow      time.Duration  `json:"coalesce_window,omitempty" yaml:"coalesce_window,omitempty"`
	ReconnectBackoff    time.Duration  `json:"reconnect_backoff,omitempty" yaml:"reconnect_backoff,omitempty"`
	MaxReconnectBackoff time.Duration  `json:"max_reconnect_backoff,omitempty" yaml:"max_reconnect_backoff,omitempty"`
	IdleReissueInterval time.Duration  `json:"idle_reissue_interval,omitempty" yaml:"idle_reissue_interval,omitempty"`
	Extensions          map[string]any `json:"extensions,omitempty" yaml:"extensions,omitempty"`
}

// ReconciliationConfig controls the authoritative convergence loop.
type ReconciliationConfig struct {
	PollInterval     time.Duration  `json:"poll_interval,omitempty" yaml:"poll_interval,omitempty"`
	FullSyncInterval time.Duration  `json:"full_sync_interval,omitempty" yaml:"full_sync_interval,omitempty"`
	PageSize         int            `json:"page_size,omitempty" yaml:"page_size,omitempty"`
	MaxPages         int            `json:"max_pages,omitempty" yaml:"max_pages,omitempty"`
	LeaseTTL         time.Duration  `json:"lease_ttl,omitempty" yaml:"lease_ttl,omitempty"`
	Extensions       map[string]any `json:"extensions,omitempty" yaml:"extensions,omitempty"`
}

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
	MaxMessageBytes         int64 `json:"max_message_bytes,omitempty" yaml:"max_message_bytes,omitempty"`
	MaxAttachmentBytes      int64 `json:"max_attachment_bytes,omitempty" yaml:"max_attachment_bytes,omitempty"`
	MaxTotalAttachmentBytes int64 `json:"max_total_attachment_bytes,omitempty" yaml:"max_total_attachment_bytes,omitempty"`
	MaxAttachments          int   `json:"max_attachments,omitempty" yaml:"max_attachments,omitempty"`
	MaxHeaderBytes          int64 `json:"max_header_bytes,omitempty" yaml:"max_header_bytes,omitempty"`
	MaxEmbeddedMessageDepth int   `json:"max_embedded_message_depth,omitempty" yaml:"max_embedded_message_depth,omitempty"`
	MaxMIMEDepth            int   `json:"max_mime_depth,omitempty" yaml:"max_mime_depth,omitempty"`
	MaxMIMEParts            int   `json:"max_mime_parts,omitempty" yaml:"max_mime_parts,omitempty"`
}
