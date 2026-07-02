package mail

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	// ErrInvalidPollingListener identifies a polling listener that is missing a
	// reconciliation target or has an invalid interval or scheduler.
	ErrInvalidPollingListener = errors.New("mail: invalid polling listener")
	// ErrPollingListenerRunning is returned when the same listener instance is
	// started more than once. This prevents independent lifecycle owners from
	// running overlapping reconciliation loops.
	ErrPollingListenerRunning = errors.New("mail: polling listener is already running")
)

// PollingScheduler waits between completed polling passes. Implementations must
// unblock when ctx is cancelled. The interface is intentionally small so tests
// can advance polling deterministically without sleeping.
type PollingScheduler interface {
	Wait(ctx context.Context, interval time.Duration) error
}

// TimerPollingScheduler implements PollingScheduler with one-shot timers. A
// one-shot timer, rather than a ticker, ensures a slow reconciliation pass does
// not accumulate ticks and overlap a later pass.
type TimerPollingScheduler struct{}

// Wait blocks for interval or until ctx is cancelled.
func (TimerPollingScheduler) Wait(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// PollingListener periodically requests authoritative reconciliation for a set
// of mailboxes. The first pass starts immediately; each later interval begins
// only after the preceding pass completes, so slow connector calls cannot
// overlap. A PollingListener instance supports one active Run or Listen call.
type PollingListener struct {
	Reconciler Reconciler
	Interval   time.Duration
	Scheduler  PollingScheduler

	mu      sync.Mutex
	running bool
	state   *ListenerStateTracker
}

// NewPollingListener constructs a polling listener. scheduler is optional; if
// omitted or nil, TimerPollingScheduler is used. At most one scheduler may be
// supplied.
func NewPollingListener(reconciler Reconciler, interval time.Duration, scheduler ...PollingScheduler) (*PollingListener, error) {
	if reconciler == nil {
		return nil, fmt.Errorf("%w: reconciler is required", ErrInvalidPollingListener)
	}
	if interval <= 0 {
		return nil, fmt.Errorf("%w: interval must be greater than zero", ErrInvalidPollingListener)
	}
	if len(scheduler) > 1 {
		return nil, fmt.Errorf("%w: at most one scheduler may be supplied", ErrInvalidPollingListener)
	}

	var selected PollingScheduler = TimerPollingScheduler{}
	if len(scheduler) == 1 && scheduler[0] != nil {
		selected = scheduler[0]
	}
	return &PollingListener{
		Reconciler: reconciler,
		Interval:   interval,
		Scheduler:  selected,
		state:      NewListenerStateTracker(),
	}, nil
}

// Run reconciles every mailbox immediately and then repeats after each
// configured interval. Mailboxes and passes are processed serially. Run stops
// on the first reconciliation or scheduler error, or promptly when ctx is
// cancelled.
func (listener *PollingListener) Run(ctx context.Context, mailboxes []MailboxKey) error {
	if listener == nil || listener.Reconciler == nil {
		return fmt.Errorf("%w: reconciler is required", ErrInvalidPollingListener)
	}
	return listener.run(ctx, mailboxes, listener.Reconciler.Reconcile, true)
}

// Listen implements Listener for polling-only connectors. Each polling pass
// emits coarse reconciliation hints through sink; durable progress remains the
// reconciler's responsibility. Use Run when the lifecycle owner has a
// Reconciler directly.
func (listener *PollingListener) Listen(ctx context.Context, mailboxes []MailboxKey, sink EventSink) error {
	if sink == nil {
		return fmt.Errorf("%w: event sink is required", ErrInvalidPollingListener)
	}
	return listener.run(ctx, mailboxes, sink.Notify, false)
}

func (listener *PollingListener) run(ctx context.Context, mailboxes []MailboxKey, reconcile func(context.Context, MailboxKey) error, recordsReconciliation bool) (result error) {
	if listener == nil {
		return fmt.Errorf("%w: listener is required", ErrInvalidPollingListener)
	}
	if listener.Interval <= 0 {
		return fmt.Errorf("%w: interval must be greater than zero", ErrInvalidPollingListener)
	}
	if listener.Scheduler == nil {
		return fmt.Errorf("%w: scheduler is required", ErrInvalidPollingListener)
	}
	if reconcile == nil {
		return fmt.Errorf("%w: reconciliation target is required", ErrInvalidPollingListener)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !listener.start() {
		return ErrPollingListenerRunning
	}
	defer func() { listener.stop(result) }()

	// Own a stable snapshot for the lifetime of this long-running loop so a
	// caller cannot race polling by reusing or modifying the input slice.
	mailboxes = append([]MailboxKey(nil), mailboxes...)
	for {
		for _, mailbox := range mailboxes {
			if err := ctx.Err(); err != nil {
				return err
			}
			listener.listenerState().RecordEvent()
			if err := reconcile(ctx, mailbox); err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return ctxErr
				}
				return fmt.Errorf("mail: poll reconcile mailbox %q: %w", mailboxIdentity(mailbox.Mailbox), err)
			}
			if recordsReconciliation {
				listener.listenerState().RecordSuccessfulReconciliation()
			}
		}
		listener.transitionState(ListenerStatusRunning, nil)

		if err := listener.Scheduler.Wait(ctx, listener.Interval); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return fmt.Errorf("mail: wait for next reconciliation poll: %w", err)
		}
	}
}

func (listener *PollingListener) start() bool {
	listener.mu.Lock()
	if listener.running {
		listener.mu.Unlock()
		return false
	}
	listener.running = true
	listener.mu.Unlock()
	listener.transitionState(ListenerStatusStarting, nil)
	return true
}

func (listener *PollingListener) stop(result error) {
	listener.mu.Lock()
	listener.running = false
	listener.mu.Unlock()
	if result != nil && !errors.Is(result, context.Canceled) {
		listener.transitionState(ListenerStatusFailed, result)
		return
	}
	listener.transitionState(ListenerStatusStopped, nil)
}

// Status returns a concurrency-safe listener lifecycle snapshot.
func (listener *PollingListener) Status() ListenerStateSnapshot {
	if listener == nil {
		return ListenerStateSnapshot{State: ListenerStatusStopped}
	}
	return listener.listenerState().Snapshot()
}

func (listener *PollingListener) listenerState() *ListenerStateTracker {
	listener.mu.Lock()
	defer listener.mu.Unlock()
	if listener.state == nil {
		listener.state = NewListenerStateTracker()
	}
	return listener.state
}

func (listener *PollingListener) transitionState(next ListenerStatus, err error) {
	state := listener.listenerState()
	if state.Snapshot().State == next {
		return
	}
	_ = state.Transition(next, err)
}

var _ Listener = (*PollingListener)(nil)
