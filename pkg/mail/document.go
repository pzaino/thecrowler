package mail

import "time"

// HeaderSet contains normalized headers retained for indexing and audit.
// Values contains bounded, decoded UTF-8 values. Raw contains bounded values
// before RFC 2047 decoding; unsafe controls are replaced and authentication
// signatures are redacted. Repeated fields retain their original order.
type HeaderSet struct {
	MessageID    string    `json:"message_id,omitempty" yaml:"message_id,omitempty"`
	InReplyTo    string    `json:"in_reply_to,omitempty" yaml:"in_reply_to,omitempty"`
	References   []string  `json:"references,omitempty" yaml:"references,omitempty"`
	ListID       string    `json:"list_id,omitempty" yaml:"list_id,omitempty"`
	OriginalDate string    `json:"original_date,omitempty" yaml:"original_date,omitempty"`
	Values       HeaderMap `json:"values,omitempty" yaml:"values,omitempty"`
	Raw          HeaderMap `json:"raw,omitempty" yaml:"raw,omitempty"`
}

// Link records a static link discovered in a message body. It is metadata
// only: retaining a link never authorizes the crawler to dereference it.
type Link struct {
	URL            string    `json:"url" yaml:"url"`
	Text           string    `json:"text,omitempty" yaml:"text,omitempty"`
	Title          string    `json:"title,omitempty" yaml:"title,omitempty"`
	Source         string    `json:"source,omitempty" yaml:"source,omitempty"`
	Classification LinkClass `json:"classification" yaml:"classification"`
}

// SecuritySignals preserves authentication and transport observations parsed
// from message headers. Values are normalized result tokens (for example,
// "pass", "fail", or "unknown") rather than security decisions made by the
// crawler. AuthenticationResults retains the sanitized source fields used to
// derive those tokens.
type SecuritySignals struct {
	SPF                   string   `json:"spf,omitempty" yaml:"spf,omitempty"`
	DKIM                  string   `json:"dkim,omitempty" yaml:"dkim,omitempty"`
	DMARC                 string   `json:"dmarc,omitempty" yaml:"dmarc,omitempty"`
	ARC                   string   `json:"arc,omitempty" yaml:"arc,omitempty"`
	TLS                   bool     `json:"tls,omitempty" yaml:"tls,omitempty"`
	AuthenticationResults []string `json:"authentication_results,omitempty" yaml:"authentication_results,omitempty"`
}

// WarningCategory groups recoverable parser problems into stable classes that
// callers can use for policy and telemetry without depending on parser-specific
// warning codes.
type WarningCategory string

const (
	WarningUnknownCharset    WarningCategory = "unknown_charset"
	WarningMalformedHeader   WarningCategory = "malformed_header"
	WarningUnsupportedPart   WarningCategory = "unsupported_part"
	WarningOversizedPart     WarningCategory = "oversized_part"
	WarningProtectedContent  WarningCategory = "protected_content"
	WarningAttachmentSkipped WarningCategory = "attachment_skipped"
)

// ParserWarning describes a recoverable parser or normalization problem.
// Category is a stable high-level class and Code is a more specific,
// machine-readable reason. Message is a safe human-readable summary. PartID,
// Header, and Offset locate the affected input without retaining raw content.
type ParserWarning struct {
	Category WarningCategory `json:"category,omitempty" yaml:"category,omitempty"`
	Code     string          `json:"code" yaml:"code"`
	Message  string          `json:"message,omitempty" yaml:"message,omitempty"`
	PartID   string          `json:"part_id,omitempty" yaml:"part_id,omitempty"`
	Header   string          `json:"header,omitempty" yaml:"header,omitempty"`
	Offset   int64           `json:"offset,omitempty" yaml:"offset,omitempty"`
}

// Document is the normalized, transport-neutral representation of an email
// message. Ref preserves provider provenance while the remaining fields retain
// decoded semantics and deterministic extraction output suitable for indexing.
type Document struct {
	ID            string       `json:"id" yaml:"id"`
	SourceID      string       `json:"source_id" yaml:"source_id"`
	Ref           MessageRef   `json:"ref" yaml:"ref"`
	MessageID     string       `json:"message_id,omitempty" yaml:"message_id,omitempty"`
	ThreadID      string       `json:"thread_id,omitempty" yaml:"thread_id,omitempty"`
	Date          time.Time    `json:"date,omitempty" yaml:"date,omitempty"`
	From          []Address    `json:"from,omitempty" yaml:"from,omitempty"`
	To            []Address    `json:"to,omitempty" yaml:"to,omitempty"`
	CC            []Address    `json:"cc,omitempty" yaml:"cc,omitempty"`
	BCC           []Address    `json:"bcc,omitempty" yaml:"bcc,omitempty"`
	ReplyTo       []Address    `json:"reply_to,omitempty" yaml:"reply_to,omitempty"`
	Subject       string       `json:"subject,omitempty" yaml:"subject,omitempty"`
	Headers       HeaderSet    `json:"headers,omitempty" yaml:"headers,omitempty"`
	TextBody      string       `json:"text_body,omitempty" yaml:"text_body,omitempty"`
	HTMLBody      string       `json:"html_body,omitempty" yaml:"html_body,omitempty"`
	ExtractedText string       `json:"extracted_text,omitempty" yaml:"extracted_text,omitempty"`
	Links         []Link       `json:"links,omitempty" yaml:"links,omitempty"`
	Attachments   []Attachment `json:"attachments,omitempty" yaml:"attachments,omitempty"`
	// ChildDocuments contains normalized email documents parsed from permitted
	// attached messages, preserving their recursive parent-child structure.
	ChildDocuments []Document      `json:"child_documents,omitempty" yaml:"child_documents,omitempty"`
	Security       SecuritySignals `json:"security,omitempty" yaml:"security,omitempty"`
	Warnings       []ParserWarning `json:"warnings,omitempty" yaml:"warnings,omitempty"`
}
