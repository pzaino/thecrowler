package mail

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	// EmailEventSchemaVersion is the stable schema version shared by email.*
	// Events.details payloads.
	EmailEventSchemaVersion = "email.lifecycle.v1"

	EmailEventMessageDiscovered       = "email.message_discovered"
	EmailEventMessageFetched          = "email.message_fetched"
	EmailEventMessageParsed           = "email.message_parsed"
	EmailEventMessageFailed           = "email.message_failed"
	EmailEventMessageCompleted        = "email.message_completed"
	EmailEventListenerStarted         = "email.listener_started"
	EmailEventListenerStopped         = "email.listener_stopped"
	EmailEventReconciliationCompleted = "email.reconciliation_completed"
)

// EmailEventIdentity contains only application-owned source identity and opaque
// SHA-256 correlation identities. AccountIdentity and MailboxIdentity must never
// contain provider-visible account names, addresses, mailbox names, or IDs.
type EmailEventIdentity struct {
	SourceID        string `json:"source_id"`
	AccountIdentity string `json:"account_identity"`
	MailboxIdentity string `json:"mailbox_identity"`
}

// EmailMessageEventIdentity scopes a lifecycle event to one message without
// exposing its Message-ID, provider ID, subject, senders, or recipients.
type EmailMessageEventIdentity struct {
	EmailEventIdentity
	MessageIdentity string `json:"message_identity"`
}

// MessageDiscoveredEventPayload is the Events.details contract for
// email.message_discovered.
type MessageDiscoveredEventPayload struct {
	SchemaVersion string `json:"schema_version"`
	EmailMessageEventIdentity
	DiscoveredCount uint64 `json:"discovered_count"`
}

// MessageFetchedEventPayload is the Events.details contract for
// email.message_fetched. Byte totals are counts; no fetched content is retained.
type MessageFetchedEventPayload struct {
	SchemaVersion string `json:"schema_version"`
	EmailMessageEventIdentity
	FetchedCount uint64 `json:"fetched_count"`
	FetchedBytes uint64 `json:"fetched_bytes"`
}

// MessageParsedEventPayload is the Events.details contract for
// email.message_parsed. Recipient, header, and attachment details are represented
// only by aggregate counts.
type MessageParsedEventPayload struct {
	SchemaVersion string `json:"schema_version"`
	EmailMessageEventIdentity
	ParsedCount     uint64 `json:"parsed_count"`
	RecipientCount  uint64 `json:"recipient_count"`
	HeaderCount     uint64 `json:"header_count"`
	AttachmentCount uint64 `json:"attachment_count"`
}

// MessageFailedEventPayload is the Events.details contract for
// email.message_failed. It carries failure and retry totals, never an error
// message, category derived from provider text, or raw provider response.
type MessageFailedEventPayload struct {
	SchemaVersion string `json:"schema_version"`
	EmailMessageEventIdentity
	FailedCount uint64 `json:"failed_count"`
	RetryCount  uint64 `json:"retry_count"`
}

// MessageCompletedEventPayload is the Events.details contract for
// email.message_completed.
type MessageCompletedEventPayload struct {
	SchemaVersion string `json:"schema_version"`
	EmailMessageEventIdentity
	CompletedCount uint64 `json:"completed_count"`
}

// ListenerStartedEventPayload is the Events.details contract for
// email.listener_started.
type ListenerStartedEventPayload struct {
	SchemaVersion string `json:"schema_version"`
	EmailEventIdentity
	ListenerCount uint64 `json:"listener_count"`
}

// ListenerStoppedEventPayload is the Events.details contract for
// email.listener_stopped.
type ListenerStoppedEventPayload struct {
	SchemaVersion string `json:"schema_version"`
	EmailEventIdentity
	ListenerCount uint64 `json:"listener_count"`
}

// ReconciliationCompletedEventPayload is the Events.details contract for
// email.reconciliation_completed. It deliberately reports only opaque run scope
// and aggregate outcomes.
type ReconciliationCompletedEventPayload struct {
	SchemaVersion string `json:"schema_version"`
	EmailEventIdentity
	DiscoveredCount  uint64 `json:"discovered_count"`
	FetchedCount     uint64 `json:"fetched_count"`
	ParsedCount      uint64 `json:"parsed_count"`
	FailedCount      uint64 `json:"failed_count"`
	CompletedCount   uint64 `json:"completed_count"`
	SkippedCount     uint64 `json:"skipped_count"`
	QuarantinedCount uint64 `json:"quarantined_count"`
	RetryCount       uint64 `json:"retry_count"`
}

// NewEmailEventIdentity derives the opaque account and mailbox identities used
// by email lifecycle payloads.
func NewEmailEventIdentity(sourceID, provider, accountID string, mailbox Mailbox) EmailEventIdentity {
	return EmailEventIdentity{
		SourceID:        strings.TrimSpace(sourceID),
		AccountIdentity: SafeEmailAccountIdentity(provider, accountID),
		MailboxIdentity: SafeEmailEventIdentity(provider, accountID, mailbox),
	}
}

// NewEmailMessageEventIdentity derives a closed, content-free identity scope for
// one message lifecycle.
func NewEmailMessageEventIdentity(sourceID string, ref MessageRef) EmailMessageEventIdentity {
	return EmailMessageEventIdentity{
		EmailEventIdentity: NewEmailEventIdentity(sourceID, ref.Provider, ref.AccountID, ref.Mailbox),
		MessageIdentity:    SafeEmailMessageIdentity(ref),
	}
}

// SafeEmailAccountIdentity returns an opaque account correlation digest.
func SafeEmailAccountIdentity(provider, accountID string) string {
	return safeEmailLifecycleDigest("account", provider, accountID)
}

// SafeEmailMessageIdentity returns an opaque message correlation digest using
// provider identity when available and IMAP UID identity otherwise.
func SafeEmailMessageIdentity(ref MessageRef) string {
	return safeMessageIdentity(ref)
}

func safeEmailLifecycleDigest(kind string, values ...string) string {
	hash := sha256.New()
	for _, value := range append([]string{kind}, values...) {
		value = strings.TrimSpace(value)
		_, _ = fmt.Fprintf(hash, "%d:%s", len(value), value)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func validateEmailEventSchema(schema string) error {
	if schema != EmailEventSchemaVersion {
		return fmt.Errorf("%w: schema_version must be %q", ErrInvalidEmailChangeEvent, EmailEventSchemaVersion)
	}
	return nil
}

func (identity EmailEventIdentity) validate() error {
	if err := validateEventString("source_id", identity.SourceID, true); err != nil {
		return err
	}
	if !validSafeIdentity(identity.AccountIdentity) {
		return fmt.Errorf("%w: account_identity must be a lowercase SHA-256 digest", ErrInvalidEmailChangeEvent)
	}
	if !validSafeIdentity(identity.MailboxIdentity) {
		return fmt.Errorf("%w: mailbox_identity must be a lowercase SHA-256 digest", ErrInvalidEmailChangeEvent)
	}
	return nil
}

func (identity EmailMessageEventIdentity) validate() error {
	if err := identity.EmailEventIdentity.validate(); err != nil {
		return err
	}
	if !validSafeIdentity(identity.MessageIdentity) {
		return fmt.Errorf("%w: message_identity must be a lowercase SHA-256 digest", ErrInvalidEmailChangeEvent)
	}
	return nil
}

func marshalLifecyclePayload(value any, validate func() error) ([]byte, error) {
	if err := validate(); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func (payload MessageDiscoveredEventPayload) Validate() error {
	if err := validateEmailEventSchema(payload.SchemaVersion); err != nil {
		return err
	}
	return payload.EmailMessageEventIdentity.validate()
}
func (payload MessageDiscoveredEventPayload) MarshalJSON() ([]byte, error) {
	type wire MessageDiscoveredEventPayload
	return marshalLifecyclePayload(wire(payload), payload.Validate)
}

func (payload MessageFetchedEventPayload) Validate() error {
	if err := validateEmailEventSchema(payload.SchemaVersion); err != nil {
		return err
	}
	return payload.EmailMessageEventIdentity.validate()
}
func (payload MessageFetchedEventPayload) MarshalJSON() ([]byte, error) {
	type wire MessageFetchedEventPayload
	return marshalLifecyclePayload(wire(payload), payload.Validate)
}

func (payload MessageParsedEventPayload) Validate() error {
	if err := validateEmailEventSchema(payload.SchemaVersion); err != nil {
		return err
	}
	return payload.EmailMessageEventIdentity.validate()
}
func (payload MessageParsedEventPayload) MarshalJSON() ([]byte, error) {
	type wire MessageParsedEventPayload
	return marshalLifecyclePayload(wire(payload), payload.Validate)
}

func (payload MessageFailedEventPayload) Validate() error {
	if err := validateEmailEventSchema(payload.SchemaVersion); err != nil {
		return err
	}
	return payload.EmailMessageEventIdentity.validate()
}
func (payload MessageFailedEventPayload) MarshalJSON() ([]byte, error) {
	type wire MessageFailedEventPayload
	return marshalLifecyclePayload(wire(payload), payload.Validate)
}

func (payload MessageCompletedEventPayload) Validate() error {
	if err := validateEmailEventSchema(payload.SchemaVersion); err != nil {
		return err
	}
	return payload.EmailMessageEventIdentity.validate()
}
func (payload MessageCompletedEventPayload) MarshalJSON() ([]byte, error) {
	type wire MessageCompletedEventPayload
	return marshalLifecyclePayload(wire(payload), payload.Validate)
}

func (payload ListenerStartedEventPayload) Validate() error {
	if err := validateEmailEventSchema(payload.SchemaVersion); err != nil {
		return err
	}
	if err := payload.EmailEventIdentity.validate(); err != nil {
		return err
	}
	return nil
}
func (payload ListenerStartedEventPayload) MarshalJSON() ([]byte, error) {
	type wire ListenerStartedEventPayload
	return marshalLifecyclePayload(wire(payload), payload.Validate)
}

func (payload ListenerStoppedEventPayload) Validate() error {
	if err := validateEmailEventSchema(payload.SchemaVersion); err != nil {
		return err
	}
	if err := payload.EmailEventIdentity.validate(); err != nil {
		return err
	}
	return nil
}
func (payload ListenerStoppedEventPayload) MarshalJSON() ([]byte, error) {
	type wire ListenerStoppedEventPayload
	return marshalLifecyclePayload(wire(payload), payload.Validate)
}

func (payload ReconciliationCompletedEventPayload) Validate() error {
	if err := validateEmailEventSchema(payload.SchemaVersion); err != nil {
		return err
	}
	return payload.EmailEventIdentity.validate()
}
func (payload ReconciliationCompletedEventPayload) MarshalJSON() ([]byte, error) {
	type wire ReconciliationCompletedEventPayload
	return marshalLifecyclePayload(wire(payload), payload.Validate)
}
