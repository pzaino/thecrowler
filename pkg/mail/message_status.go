package mail

import (
	"errors"
	"fmt"
)

// MessageStatus identifies a durable stage in the ingestion lifecycle for one
// message.
type MessageStatus string

const (
	MessageStatusDiscovered           MessageStatus = "discovered"
	MessageStatusFetched              MessageStatus = "fetched"
	MessageStatusParsed               MessageStatus = "parsed"
	MessageStatusNormalized           MessageStatus = "normalized"
	MessageStatusAttachmentsProcessed MessageStatus = "attachments_processed"
	MessageStatusLinksEnqueued        MessageStatus = "links_enqueued"
	MessageStatusCompleted            MessageStatus = "completed"
	MessageStatusRetryableFailure     MessageStatus = "retryable_failure"
	MessageStatusPermanentFailure     MessageStatus = "permanent_failure"
)

var (
	// ErrInvalidMessageStatus indicates that a transition contains an unknown
	// or empty message status.
	ErrInvalidMessageStatus = errors.New("invalid mail message status")
	// ErrInvalidMessageStatusTransition indicates that two valid statuses do
	// not form an allowed lifecycle transition.
	ErrInvalidMessageStatusTransition = errors.New("invalid mail message status transition")
	// ErrTerminalMessageStatus indicates an attempt to leave a terminal state.
	ErrTerminalMessageStatus = errors.New("mail message status is terminal")
)

var messageStatusTransitions = map[MessageStatus]map[MessageStatus]struct{}{
	MessageStatusDiscovered: {
		MessageStatusFetched:          {},
		MessageStatusRetryableFailure: {},
		MessageStatusPermanentFailure: {},
	},
	MessageStatusFetched: {
		MessageStatusParsed:           {},
		MessageStatusRetryableFailure: {},
		MessageStatusPermanentFailure: {},
	},
	MessageStatusParsed: {
		MessageStatusNormalized:       {},
		MessageStatusRetryableFailure: {},
		MessageStatusPermanentFailure: {},
	},
	MessageStatusNormalized: {
		MessageStatusAttachmentsProcessed: {},
		MessageStatusRetryableFailure:     {},
		MessageStatusPermanentFailure:     {},
	},
	MessageStatusAttachmentsProcessed: {
		MessageStatusLinksEnqueued:    {},
		MessageStatusRetryableFailure: {},
		MessageStatusPermanentFailure: {},
	},
	MessageStatusLinksEnqueued: {
		MessageStatusCompleted:        {},
		MessageStatusRetryableFailure: {},
		MessageStatusPermanentFailure: {},
	},
	MessageStatusRetryableFailure: {
		MessageStatusDiscovered:       {},
		MessageStatusPermanentFailure: {},
	},
}

// Valid reports whether status is one of the defined lifecycle statuses.
func (status MessageStatus) Valid() bool {
	switch status {
	case MessageStatusDiscovered,
		MessageStatusFetched,
		MessageStatusParsed,
		MessageStatusNormalized,
		MessageStatusAttachmentsProcessed,
		MessageStatusLinksEnqueued,
		MessageStatusCompleted,
		MessageStatusRetryableFailure,
		MessageStatusPermanentFailure:
		return true
	default:
		return false
	}
}

// IsTerminal reports whether no further transition is permitted. A retryable
// failure is deliberately non-terminal: it may restart at discovered or be
// promoted to a permanent failure when its retry budget is exhausted.
func (status MessageStatus) IsTerminal() bool {
	return status == MessageStatusCompleted || status == MessageStatusPermanentFailure
}

// CanTransitionTo reports whether transitioning from status to next is
// permitted by the message ingestion lifecycle.
func (status MessageStatus) CanTransitionTo(next MessageStatus) bool {
	if !status.Valid() || !next.Valid() || status.IsTerminal() {
		return false
	}
	_, ok := messageStatusTransitions[status][next]
	return ok
}

// ValidateMessageStatusTransition validates one lifecycle transition. Both
// statuses must be defined, terminal statuses cannot be left, and active
// statuses may only advance along the explicit ingestion path or into a
// failure state.
func ValidateMessageStatusTransition(current, next MessageStatus) error {
	if !current.Valid() {
		return fmt.Errorf("%w: current status %q", ErrInvalidMessageStatus, current)
	}
	if !next.Valid() {
		return fmt.Errorf("%w: next status %q", ErrInvalidMessageStatus, next)
	}
	if current.IsTerminal() {
		return fmt.Errorf("%w: %q cannot transition to %q", ErrTerminalMessageStatus, current, next)
	}
	if !current.CanTransitionTo(next) {
		return fmt.Errorf("%w: %q to %q", ErrInvalidMessageStatusTransition, current, next)
	}
	return nil
}
