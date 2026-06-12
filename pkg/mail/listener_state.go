package mail

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	// ErrInvalidListenerStatusTransition identifies a transition that is not
	// permitted by the listener lifecycle.
	ErrInvalidListenerStatusTransition = errors.New("mail: invalid listener status transition")
)

// ListenerStateSnapshot is an immutable, concurrency-safe view of a listener's
// lifecycle and recovery metadata. ErrorCategory is deliberately bounded and
// never contains provider error text, credentials, or mailbox identifiers.
type ListenerStateSnapshot struct {
	State                          ListenerStatus   `json:"state" yaml:"state"`
	LastEventAt                    time.Time        `json:"last_event_at,omitempty" yaml:"last_event_at,omitempty"`
	LastSuccessfulReconciliationAt time.Time        `json:"last_successful_reconciliation_at,omitempty" yaml:"last_successful_reconciliation_at,omitempty"`
	ErrorCategory                  LogErrorCategory `json:"error_category,omitempty" yaml:"error_category,omitempty"`
	TransitionedAt                 time.Time        `json:"transitioned_at,omitempty" yaml:"transitioned_at,omitempty"`
}

// ListenerStateTracker owns listener lifecycle metadata and serializes all
// updates. Callers receive values from Snapshot, never references to mutable
// internal state.
type ListenerStateTracker struct {
	mu       sync.RWMutex
	snapshot ListenerStateSnapshot
	now      func() time.Time
}

// NewListenerStateTracker creates a tracker in the stopped state.
func NewListenerStateTracker() *ListenerStateTracker {
	return newListenerStateTracker(time.Now)
}

func newListenerStateTracker(now func() time.Time) *ListenerStateTracker {
	if now == nil {
		now = time.Now
	}
	at := now()
	return &ListenerStateTracker{
		snapshot: ListenerStateSnapshot{State: ListenerStatusStopped, TransitionedAt: at},
		now:      now,
	}
}

// Snapshot returns a point-in-time copy that remains safe to read while the
// listener continues updating its state.
func (tracker *ListenerStateTracker) Snapshot() ListenerStateSnapshot {
	if tracker == nil {
		return ListenerStateSnapshot{State: ListenerStatusStopped}
	}
	tracker.mu.RLock()
	defer tracker.mu.RUnlock()
	return tracker.snapshot
}

// Transition validates and records a lifecycle transition. Successful states
// clear the previous safe error category; unhealthy states classify err into a
// bounded provider-neutral category.
func (tracker *ListenerStateTracker) Transition(next ListenerStatus, err error) error {
	if tracker == nil {
		return fmt.Errorf("%w: tracker is nil", ErrInvalidListenerStatusTransition)
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()

	current := tracker.snapshot.State
	if err := ValidateListenerStatusTransition(current, next); err != nil {
		return err
	}
	tracker.snapshot.State = next
	tracker.snapshot.TransitionedAt = tracker.now()
	if next == ListenerStatusStarting || next == ListenerStatusRunning || next == ListenerStatusStopped {
		tracker.snapshot.ErrorCategory = ""
	} else {
		tracker.snapshot.ErrorCategory = logErrorCategory(err)
	}
	return nil
}

// RecordEvent records when the listener most recently accepted a provider or
// protocol change hint.
func (tracker *ListenerStateTracker) RecordEvent() {
	if tracker == nil {
		return
	}
	tracker.mu.Lock()
	tracker.snapshot.LastEventAt = tracker.now()
	tracker.mu.Unlock()
}

// RecordSuccessfulReconciliation records completed delivery/reconciliation and
// clears a stale error category. Lifecycle recovery remains explicit through
// Transition so callers cannot accidentally hide a degraded state.
func (tracker *ListenerStateTracker) RecordSuccessfulReconciliation() {
	if tracker == nil {
		return
	}
	tracker.mu.Lock()
	tracker.snapshot.LastSuccessfulReconciliationAt = tracker.now()
	if tracker.snapshot.State == ListenerStatusRunning {
		tracker.snapshot.ErrorCategory = ""
	}
	tracker.mu.Unlock()
}
