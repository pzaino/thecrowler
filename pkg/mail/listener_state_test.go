package mail

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestListenerStatusTransitions(t *testing.T) {
	t.Parallel()

	statuses := []ListenerStatus{
		ListenerStatusStopped,
		ListenerStatusStarting,
		ListenerStatusRunning,
		ListenerStatusDegraded,
		ListenerStatusReconnecting,
		ListenerStatusFailed,
	}
	allowed := map[ListenerStatus]map[ListenerStatus]bool{
		ListenerStatusStopped: {ListenerStatusStarting: true},
		ListenerStatusStarting: {
			ListenerStatusRunning: true, ListenerStatusDegraded: true, ListenerStatusReconnecting: true,
			ListenerStatusFailed: true, ListenerStatusStopped: true,
		},
		ListenerStatusRunning: {
			ListenerStatusDegraded: true, ListenerStatusReconnecting: true,
			ListenerStatusFailed: true, ListenerStatusStopped: true,
		},
		ListenerStatusDegraded: {
			ListenerStatusRunning: true, ListenerStatusReconnecting: true,
			ListenerStatusFailed: true, ListenerStatusStopped: true,
		},
		ListenerStatusReconnecting: {
			ListenerStatusRunning: true, ListenerStatusDegraded: true,
			ListenerStatusFailed: true, ListenerStatusStopped: true,
		},
		ListenerStatusFailed: {ListenerStatusStarting: true, ListenerStatusStopped: true},
	}

	for _, current := range statuses {
		current := current
		for _, next := range statuses {
			next := next
			t.Run(string(current)+"_to_"+string(next), func(t *testing.T) {
				t.Parallel()
				want := allowed[current][next]
				if got := current.CanTransitionTo(next); got != want {
					t.Fatalf("CanTransitionTo(%q, %q) = %t, want %t", current, next, got, want)
				}
				err := ValidateListenerStatusTransition(current, next)
				if want && err != nil {
					t.Fatalf("ValidateListenerStatusTransition(%q, %q) error = %v", current, next, err)
				}
				if !want && !errors.Is(err, ErrInvalidListenerStatusTransition) {
					t.Fatalf("ValidateListenerStatusTransition(%q, %q) error = %v, want ErrInvalidListenerStatusTransition", current, next, err)
				}
			})
		}
	}
}

func TestListenerStatusTransitionRejectsUnknownStatus(t *testing.T) {
	t.Parallel()
	if err := ValidateListenerStatusTransition("unknown", ListenerStatusStarting); !errors.Is(err, ErrInvalidListenerStatus) {
		t.Fatalf("invalid current status error = %v, want ErrInvalidListenerStatus", err)
	}
	if err := ValidateListenerStatusTransition(ListenerStatusStopped, "unknown"); !errors.Is(err, ErrInvalidListenerStatus) {
		t.Fatalf("invalid next status error = %v, want ErrInvalidListenerStatus", err)
	}
}

func TestListenerStateTrackerRecordsFailureAndRecovery(t *testing.T) {
	base := time.Date(2026, time.June, 12, 10, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	now := base
	tracker := newListenerStateTracker(func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		current := now
		now = now.Add(time.Second)
		return current
	})

	if err := tracker.Transition(ListenerStatusStarting, nil); err != nil {
		t.Fatalf("starting transition error = %v", err)
	}
	if err := tracker.Transition(ListenerStatusRunning, nil); err != nil {
		t.Fatalf("running transition error = %v", err)
	}
	tracker.RecordEvent()
	tracker.RecordSuccessfulReconciliation()
	beforeFailure := tracker.Snapshot()

	if err := tracker.Transition(ListenerStatusReconnecting, errors.New("provider secret must not be retained")); err != nil {
		t.Fatalf("reconnecting transition error = %v", err)
	}
	failed := tracker.Snapshot()
	if failed.ErrorCategory != LogErrorUnknown || failed.TransitionedAt.Before(beforeFailure.LastSuccessfulReconciliationAt) {
		t.Fatalf("failure snapshot = %#v, want safe category and later transition", failed)
	}
	if err := tracker.Transition(ListenerStatusRunning, nil); err != nil {
		t.Fatalf("recovery transition error = %v", err)
	}
	recovered := tracker.Snapshot()
	if recovered.State != ListenerStatusRunning || recovered.ErrorCategory != "" {
		t.Fatalf("recovered snapshot = %#v, want running without an error category", recovered)
	}
	if recovered.LastEventAt != beforeFailure.LastEventAt || recovered.LastSuccessfulReconciliationAt != beforeFailure.LastSuccessfulReconciliationAt {
		t.Fatalf("recovery lost activity timestamps: before=%#v after=%#v", beforeFailure, recovered)
	}
}

func TestListenerStateTrackerSnapshotsAreConcurrencySafe(t *testing.T) {
	tracker := NewListenerStateTracker()
	if err := tracker.Transition(ListenerStatusStarting, nil); err != nil {
		t.Fatal(err)
	}
	if err := tracker.Transition(ListenerStatusRunning, nil); err != nil {
		t.Fatal(err)
	}

	var workers sync.WaitGroup
	for i := 0; i < 16; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 100; j++ {
				tracker.RecordEvent()
				tracker.RecordSuccessfulReconciliation()
				_ = tracker.Snapshot()
			}
		}()
	}
	workers.Wait()

	snapshot := tracker.Snapshot()
	if snapshot.State != ListenerStatusRunning || snapshot.LastEventAt.IsZero() || snapshot.LastSuccessfulReconciliationAt.IsZero() {
		t.Fatalf("snapshot after concurrent updates = %#v", snapshot)
	}
}
