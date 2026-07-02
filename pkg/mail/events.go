package mail

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

const maxEmailEventStringBytes = 1024

var (
	// ErrInvalidEmailChangeEvent identifies an event that is incomplete or
	// contains unsupported metadata.
	ErrInvalidEmailChangeEvent = errors.New("mail: invalid email change event")
	// ErrInvalidListenerStatus identifies an unsupported listener lifecycle
	// status.
	ErrInvalidListenerStatus = errors.New("mail: invalid listener status")
	// ErrInvalidListenerMode identifies an unsupported notification transport.
	ErrInvalidListenerMode = errors.New("mail: invalid listener mode")
)

// ListenerStatus describes the health of the listener that produced an event.
type ListenerStatus string

const (
	ListenerStatusStopped      ListenerStatus = "stopped"
	ListenerStatusStarting     ListenerStatus = "starting"
	ListenerStatusRunning      ListenerStatus = "running"
	ListenerStatusDegraded     ListenerStatus = "degraded"
	ListenerStatusReconnecting ListenerStatus = "reconnecting"
	ListenerStatusFailed       ListenerStatus = "failed"

	// ListenerStatusActive is retained as a source-compatible alias. New event
	// producers should use ListenerStatusRunning.
	ListenerStatusActive = ListenerStatusRunning
)

// Valid reports whether status is a defined listener lifecycle status.
func (status ListenerStatus) Valid() bool {
	switch status {
	case ListenerStatusStopped, ListenerStatusStarting, ListenerStatusRunning, ListenerStatusDegraded, ListenerStatusReconnecting, ListenerStatusFailed:
		return true
	default:
		return false
	}
}

var listenerStatusTransitions = map[ListenerStatus]map[ListenerStatus]struct{}{
	ListenerStatusStopped: {
		ListenerStatusStarting: {},
	},
	ListenerStatusStarting: {
		ListenerStatusRunning: {}, ListenerStatusDegraded: {}, ListenerStatusReconnecting: {},
		ListenerStatusFailed: {}, ListenerStatusStopped: {},
	},
	ListenerStatusRunning: {
		ListenerStatusDegraded: {}, ListenerStatusReconnecting: {}, ListenerStatusFailed: {}, ListenerStatusStopped: {},
	},
	ListenerStatusDegraded: {
		ListenerStatusRunning: {}, ListenerStatusReconnecting: {}, ListenerStatusFailed: {}, ListenerStatusStopped: {},
	},
	ListenerStatusReconnecting: {
		ListenerStatusRunning: {}, ListenerStatusDegraded: {}, ListenerStatusFailed: {}, ListenerStatusStopped: {},
	},
	ListenerStatusFailed: {
		ListenerStatusStarting: {}, ListenerStatusStopped: {},
	},
}

// CanTransitionTo reports whether next is an allowed lifecycle successor.
func (status ListenerStatus) CanTransitionTo(next ListenerStatus) bool {
	if !status.Valid() || !next.Valid() || status == next {
		return false
	}
	_, ok := listenerStatusTransitions[status][next]
	return ok
}

// ValidateListenerStatusTransition validates both statuses and their edge in
// the listener lifecycle graph.
func ValidateListenerStatusTransition(current, next ListenerStatus) error {
	if !current.Valid() {
		return fmt.Errorf("%w: current status %q", ErrInvalidListenerStatus, current)
	}
	if !next.Valid() {
		return fmt.Errorf("%w: next status %q", ErrInvalidListenerStatus, next)
	}
	if !current.CanTransitionTo(next) {
		return fmt.Errorf("%w: %q to %q", ErrInvalidListenerStatusTransition, current, next)
	}
	return nil
}

// ListenerMode identifies how a change hint reached the reconciler.
type ListenerMode string

const (
	ListenerModePoll    ListenerMode = "poll"
	ListenerModePush    ListenerMode = "push"
	ListenerModeWebhook ListenerMode = "webhook"
	ListenerModeIdle    ListenerMode = "idle"
)

// Valid reports whether mode is a defined listener transport.
func (mode ListenerMode) Valid() bool {
	switch mode {
	case ListenerModePoll, ListenerModePush, ListenerModeWebhook, ListenerModeIdle:
		return true
	default:
		return false
	}
}

// EmailEventMetadata carries bounded operational data about a change hint.
// It intentionally has no arbitrary map so callers cannot attach message
// content, credentials, or attachment bytes to an event.
type EmailEventMetadata struct {
	EventID        string         `json:"event_id,omitempty" yaml:"event_id,omitempty"`
	OccurredAt     time.Time      `json:"occurred_at,omitempty" yaml:"occurred_at,omitempty"`
	ReceivedAt     time.Time      `json:"received_at,omitempty" yaml:"received_at,omitempty"`
	ListenerMode   ListenerMode   `json:"listener_mode" yaml:"listener_mode"`
	ListenerStatus ListenerStatus `json:"listener_status" yaml:"listener_status"`
}

// Validate checks the listener metadata without accepting unbounded or
// provider-specific payloads.
func (metadata EmailEventMetadata) Validate() error {
	if !metadata.ListenerMode.Valid() {
		return fmt.Errorf("%w: %q", ErrInvalidListenerMode, metadata.ListenerMode)
	}
	if !metadata.ListenerStatus.Valid() {
		return fmt.Errorf("%w: %q", ErrInvalidListenerStatus, metadata.ListenerStatus)
	}
	if err := validateEventString("metadata.event_id", metadata.EventID, false); err != nil {
		return err
	}
	if !metadata.OccurredAt.IsZero() && !metadata.ReceivedAt.IsZero() && metadata.ReceivedAt.Before(metadata.OccurredAt) {
		return fmt.Errorf("%w: metadata.received_at precedes metadata.occurred_at", ErrInvalidEmailChangeEvent)
	}
	return nil
}

// EmailChangeEvent is a provider-neutral request for incremental
// reconciliation. Cursor is an advisory provider position; a consumer must
// reconcile from its durable checkpoint and must never commit Cursor solely
// because this event was received.
//
// SafeIdentity is a SHA-256 digest derived from stable provider/account/mailbox
// identifiers. The closed event schema deliberately cannot carry message
// bodies, credentials, arbitrary headers, or attachment content.
type EmailChangeEvent struct {
	Provider     string             `json:"provider" yaml:"provider"`
	AccountID    string             `json:"account_id" yaml:"account_id"`
	Mailbox      Mailbox            `json:"mailbox" yaml:"mailbox"`
	Cursor       Cursor             `json:"cursor" yaml:"cursor"`
	SafeIdentity string             `json:"safe_identity" yaml:"safe_identity"`
	ChangeType   ChangeKind         `json:"change_type" yaml:"change_type"`
	Metadata     EmailEventMetadata `json:"metadata" yaml:"metadata"`
}

// Validate verifies that an event is safe and sufficiently scoped for
// reconciliation.
func (event EmailChangeEvent) Validate() error {
	if err := validateEventString("provider", event.Provider, true); err != nil {
		return err
	}
	if err := validateEventString("account_id", event.AccountID, true); err != nil {
		return err
	}
	if strings.TrimSpace(event.Mailbox.ID) == "" && strings.TrimSpace(event.Mailbox.Name) == "" {
		return fmt.Errorf("%w: mailbox requires an id or name", ErrInvalidEmailChangeEvent)
	}
	if err := validateEventString("mailbox.id", event.Mailbox.ID, false); err != nil {
		return err
	}
	if err := validateEventString("mailbox.name", event.Mailbox.Name, false); err != nil {
		return err
	}
	if !cursorPresent(event.Cursor) {
		return fmt.Errorf("%w: cursor is empty", ErrInvalidEmailChangeEvent)
	}
	if err := validateEventString("cursor.token", event.Cursor.Token, false); err != nil {
		return err
	}
	if !validSafeIdentity(event.SafeIdentity) {
		return fmt.Errorf("%w: safe_identity must be a lowercase SHA-256 digest", ErrInvalidEmailChangeEvent)
	}
	if !event.ChangeType.Valid() {
		return fmt.Errorf("%w: unsupported change_type %q", ErrInvalidEmailChangeEvent, event.ChangeType)
	}
	if err := event.Metadata.Validate(); err != nil {
		return err
	}
	return nil
}

// MarshalJSON prevents invalid events from crossing a serialization boundary.
func (event EmailChangeEvent) MarshalJSON() ([]byte, error) {
	if err := event.Validate(); err != nil {
		return nil, err
	}
	type wireEvent EmailChangeEvent
	return json.Marshal(wireEvent(event))
}

// UnmarshalJSON accepts only the closed event schema and validates it. Unknown
// fields are rejected so sensitive provider payloads cannot be smuggled into
// an event as body, credential, header, or attachment fields.
func (event *EmailChangeEvent) UnmarshalJSON(data []byte) error {
	if event == nil {
		return fmt.Errorf("%w: nil destination", ErrInvalidEmailChangeEvent)
	}
	type wireEvent EmailChangeEvent
	var decoded wireEvent
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return fmt.Errorf("%w: decode: %v", ErrInvalidEmailChangeEvent, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: multiple JSON values", ErrInvalidEmailChangeEvent)
	}
	candidate := EmailChangeEvent(decoded)
	if err := candidate.Validate(); err != nil {
		return err
	}
	*event = candidate
	return nil
}

// SafeEmailEventIdentity returns an opaque, deterministic mailbox identity for
// event correlation. It does not expose the account or mailbox value used to
// derive it.
func SafeEmailEventIdentity(provider, accountID string, mailbox Mailbox) string {
	hash := sha256.New()
	for _, value := range []string{
		strings.TrimSpace(provider),
		strings.TrimSpace(accountID),
		strings.TrimSpace(mailbox.ID),
		strings.TrimSpace(mailbox.Name),
	} {
		_, _ = hash.Write([]byte(strconv.Itoa(len(value))))
		_, _ = hash.Write([]byte{':'})
		_, _ = hash.Write([]byte(value))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func (kind ChangeKind) Valid() bool {
	switch kind {
	case ChangeUpsert, ChangeDelete, ChangeReset:
		return true
	default:
		return false
	}
}

func cursorPresent(cursor Cursor) bool {
	return strings.TrimSpace(cursor.Token) != "" || cursor.HistoryID != 0 || cursor.UID != 0 || cursor.UIDValidity != 0
}

func validSafeIdentity(identity string) bool {
	if len(identity) != sha256.Size*2 || identity != strings.ToLower(identity) {
		return false
	}
	_, err := hex.DecodeString(identity)
	return err == nil
}

func validateEventString(field, value string, required bool) error {
	trimmed := strings.TrimSpace(value)
	if required && trimmed == "" {
		return fmt.Errorf("%w: %s is required", ErrInvalidEmailChangeEvent, field)
	}
	if len(value) > maxEmailEventStringBytes {
		return fmt.Errorf("%w: %s exceeds %d bytes", ErrInvalidEmailChangeEvent, field, maxEmailEventStringBytes)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: %s contains control characters", ErrInvalidEmailChangeEvent, field)
		}
	}
	return nil
}
