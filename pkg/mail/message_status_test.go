package mail

import (
	"errors"
	"testing"
)

func TestMessageStatusTransitions(t *testing.T) {
	t.Parallel()

	statuses := []MessageStatus{
		MessageStatusDiscovered,
		MessageStatusFetched,
		MessageStatusParsed,
		MessageStatusNormalized,
		MessageStatusAttachmentsProcessed,
		MessageStatusLinksEnqueued,
		MessageStatusCompleted,
		MessageStatusRetryableFailure,
		MessageStatusPermanentFailure,
	}
	allowed := map[MessageStatus]map[MessageStatus]bool{
		MessageStatusDiscovered: {
			MessageStatusFetched:          true,
			MessageStatusRetryableFailure: true,
			MessageStatusPermanentFailure: true,
		},
		MessageStatusFetched: {
			MessageStatusParsed:           true,
			MessageStatusRetryableFailure: true,
			MessageStatusPermanentFailure: true,
		},
		MessageStatusParsed: {
			MessageStatusNormalized:       true,
			MessageStatusRetryableFailure: true,
			MessageStatusPermanentFailure: true,
		},
		MessageStatusNormalized: {
			MessageStatusAttachmentsProcessed: true,
			MessageStatusRetryableFailure:     true,
			MessageStatusPermanentFailure:     true,
		},
		MessageStatusAttachmentsProcessed: {
			MessageStatusLinksEnqueued:    true,
			MessageStatusRetryableFailure: true,
			MessageStatusPermanentFailure: true,
		},
		MessageStatusLinksEnqueued: {
			MessageStatusCompleted:        true,
			MessageStatusRetryableFailure: true,
			MessageStatusPermanentFailure: true,
		},
		MessageStatusRetryableFailure: {
			MessageStatusDiscovered:       true,
			MessageStatusPermanentFailure: true,
		},
	}

	for _, current := range statuses {
		current := current
		for _, next := range statuses {
			next := next
			t.Run(string(current)+"_to_"+string(next), func(t *testing.T) {
				t.Parallel()

				wantAllowed := allowed[current][next]
				if got := current.CanTransitionTo(next); got != wantAllowed {
					t.Errorf("CanTransitionTo(%q, %q) = %t, want %t", current, next, got, wantAllowed)
				}

				err := ValidateMessageStatusTransition(current, next)
				if wantAllowed {
					if err != nil {
						t.Fatalf("ValidateMessageStatusTransition(%q, %q) error = %v", current, next, err)
					}
					return
				}
				if err == nil {
					t.Fatalf("ValidateMessageStatusTransition(%q, %q) returned nil", current, next)
				}
				if current.IsTerminal() {
					if !errors.Is(err, ErrTerminalMessageStatus) {
						t.Errorf("error = %v, want ErrTerminalMessageStatus", err)
					}
				} else if !errors.Is(err, ErrInvalidMessageStatusTransition) {
					t.Errorf("error = %v, want ErrInvalidMessageStatusTransition", err)
				}
			})
		}
	}
}

func TestMessageStatusValidityAndTerminalStates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status   MessageStatus
		valid    bool
		terminal bool
	}{
		{status: MessageStatusDiscovered, valid: true},
		{status: MessageStatusFetched, valid: true},
		{status: MessageStatusParsed, valid: true},
		{status: MessageStatusNormalized, valid: true},
		{status: MessageStatusAttachmentsProcessed, valid: true},
		{status: MessageStatusLinksEnqueued, valid: true},
		{status: MessageStatusCompleted, valid: true, terminal: true},
		{status: MessageStatusRetryableFailure, valid: true},
		{status: MessageStatusPermanentFailure, valid: true, terminal: true},
		{status: "", valid: false},
		{status: "unknown", valid: false},
	}

	for _, test := range tests {
		test := test
		t.Run(string(test.status), func(t *testing.T) {
			t.Parallel()
			if got := test.status.Valid(); got != test.valid {
				t.Errorf("Valid(%q) = %t, want %t", test.status, got, test.valid)
			}
			if got := test.status.IsTerminal(); got != test.terminal {
				t.Errorf("IsTerminal(%q) = %t, want %t", test.status, got, test.terminal)
			}
		})
	}
}

func TestMessageStatusTransitionRejectsInvalidStatuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		current MessageStatus
		next    MessageStatus
	}{
		{name: "empty current", next: MessageStatusDiscovered},
		{name: "unknown current", current: "unknown", next: MessageStatusDiscovered},
		{name: "empty next", current: MessageStatusDiscovered},
		{name: "unknown next", current: MessageStatusDiscovered, next: "unknown"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if test.current.CanTransitionTo(test.next) {
				t.Fatalf("CanTransitionTo(%q, %q) = true", test.current, test.next)
			}
			err := ValidateMessageStatusTransition(test.current, test.next)
			if !errors.Is(err, ErrInvalidMessageStatus) {
				t.Fatalf("error = %v, want ErrInvalidMessageStatus", err)
			}
		})
	}
}
