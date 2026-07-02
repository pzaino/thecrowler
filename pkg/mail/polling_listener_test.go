package mail

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type controlledPollingScheduler struct {
	waits    chan time.Duration
	advances chan error
}

func newControlledPollingScheduler() *controlledPollingScheduler {
	return &controlledPollingScheduler{
		waits:    make(chan time.Duration, 8),
		advances: make(chan error, 8),
	}
}

func (scheduler *controlledPollingScheduler) Wait(ctx context.Context, interval time.Duration) error {
	select {
	case scheduler.waits <- interval:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-scheduler.advances:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

type pollingReconcilerFunc func(context.Context, MailboxKey) error

func (fn pollingReconcilerFunc) Reconcile(ctx context.Context, mailbox MailboxKey) error {
	return fn(ctx, mailbox)
}

type pollingEventSinkFunc func(context.Context, MailboxKey) error

func (fn pollingEventSinkFunc) Notify(ctx context.Context, mailbox MailboxKey) error {
	return fn(ctx, mailbox)
}

func TestPollingListenerRunsImmediatelyAndWaitsAfterCompletedPass(t *testing.T) {
	scheduler := newControlledPollingScheduler()
	firstStarted := make(chan struct{})
	finishFirst := make(chan struct{})
	secondStarted := make(chan struct{})
	var calls atomic.Int32
	var active atomic.Int32
	var maxActive atomic.Int32

	reconciler := pollingReconcilerFunc(func(ctx context.Context, _ MailboxKey) error {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			maximum := maxActive.Load()
			if current <= maximum || maxActive.CompareAndSwap(maximum, current) {
				break
			}
		}

		switch calls.Add(1) {
		case 1:
			close(firstStarted)
			select {
			case <-finishFirst:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		case 2:
			close(secondStarted)
		}
		return nil
	})

	listener, err := NewPollingListener(reconciler, 17*time.Minute, scheduler)
	if err != nil {
		t.Fatalf("NewPollingListener() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- listener.Run(ctx, []MailboxKey{{Mailbox: Mailbox{Name: "INBOX"}}})
	}()

	awaitSignal(t, firstStarted, "first reconciliation")
	select {
	case interval := <-scheduler.waits:
		t.Fatalf("scheduler waited before reconciliation completed; interval = %v", interval)
	default:
	}
	close(finishFirst)
	if interval := awaitValue(t, scheduler.waits, "scheduler wait"); interval != 17*time.Minute {
		t.Fatalf("Wait() interval = %v, want %v", interval, 17*time.Minute)
	}

	scheduler.advances <- nil
	awaitSignal(t, secondStarted, "second reconciliation")
	if got := maxActive.Load(); got != 1 {
		t.Fatalf("maximum concurrent reconciliations = %d, want 1", got)
	}

	cancel()
	if err := awaitValue(t, done, "listener shutdown"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
}

func TestPollingListenerRejectsOverlappingRuns(t *testing.T) {
	scheduler := newControlledPollingScheduler()
	started := make(chan struct{})
	reconciler := pollingReconcilerFunc(func(ctx context.Context, _ MailboxKey) error {
		select {
		case <-started:
		default:
			close(started)
		}
		<-ctx.Done()
		return ctx.Err()
	})
	listener, err := NewPollingListener(reconciler, time.Minute, scheduler)
	if err != nil {
		t.Fatalf("NewPollingListener() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- listener.Run(ctx, []MailboxKey{{Mailbox: Mailbox{Name: "INBOX"}}})
	}()
	awaitSignal(t, started, "first listener run")

	err = listener.Run(context.Background(), []MailboxKey{{Mailbox: Mailbox{Name: "Archive"}}})
	if !errors.Is(err, ErrPollingListenerRunning) {
		t.Fatalf("overlapping Run() error = %v, want ErrPollingListenerRunning", err)
	}

	cancel()
	if err := awaitValue(t, firstDone, "first listener shutdown"); !errors.Is(err, context.Canceled) {
		t.Fatalf("first Run() error = %v, want context.Canceled", err)
	}
}

func TestPollingListenerCancellationInterruptsScheduler(t *testing.T) {
	scheduler := newControlledPollingScheduler()
	listener, err := NewPollingListener(pollingReconcilerFunc(func(context.Context, MailboxKey) error {
		return nil
	}), time.Hour, scheduler)
	if err != nil {
		t.Fatalf("NewPollingListener() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- listener.Run(ctx, []MailboxKey{{Mailbox: Mailbox{Name: "INBOX"}}})
	}()
	if interval := awaitValue(t, scheduler.waits, "scheduler wait"); interval != time.Hour {
		t.Fatalf("Wait() interval = %v, want %v", interval, time.Hour)
	}

	cancel()
	if err := awaitValue(t, done, "listener shutdown"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
}

func TestPollingListenerListenEmitsReconciliationHints(t *testing.T) {
	scheduler := newControlledPollingScheduler()
	listener := &PollingListener{Interval: time.Minute, Scheduler: scheduler}
	mailboxes := []MailboxKey{
		{SourceID: "source-1", Mailbox: Mailbox{Name: "INBOX"}},
		{SourceID: "source-1", Mailbox: Mailbox{Name: "Archive"}},
	}

	var mu sync.Mutex
	var notified []MailboxKey
	sink := pollingEventSinkFunc(func(_ context.Context, mailbox MailboxKey) error {
		mu.Lock()
		notified = append(notified, mailbox)
		mu.Unlock()
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- listener.Listen(ctx, mailboxes, sink)
	}()
	awaitValue(t, scheduler.waits, "scheduler wait")

	mu.Lock()
	got := append([]MailboxKey(nil), notified...)
	mu.Unlock()
	if len(got) != len(mailboxes) || got[0] != mailboxes[0] || got[1] != mailboxes[1] {
		t.Fatalf("notified mailboxes = %#v, want %#v", got, mailboxes)
	}

	cancel()
	if err := awaitValue(t, done, "listener shutdown"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Listen() error = %v, want context.Canceled", err)
	}
}

func TestPollingListenerStatusTracksSuccessAndFailure(t *testing.T) {
	t.Run("successful pass", func(t *testing.T) {
		scheduler := newControlledPollingScheduler()
		listener, err := NewPollingListener(pollingReconcilerFunc(func(context.Context, MailboxKey) error {
			return nil
		}), time.Minute, scheduler)
		if err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- listener.Run(ctx, []MailboxKey{{Mailbox: Mailbox{Name: "INBOX"}}}) }()
		awaitValue(t, scheduler.waits, "successful polling pass")
		status := listener.Status()
		if status.State != ListenerStatusRunning || status.LastEventAt.IsZero() || status.LastSuccessfulReconciliationAt.IsZero() || status.ErrorCategory != "" {
			t.Fatalf("successful polling status = %#v", status)
		}

		cancel()
		if err := awaitValue(t, done, "polling cancellation"); !errors.Is(err, context.Canceled) {
			t.Fatalf("Run() error = %v, want context.Canceled", err)
		}
		if status := listener.Status(); status.State != ListenerStatusStopped {
			t.Fatalf("status after cancellation = %#v, want stopped", status)
		}
	})

	t.Run("failed reconciliation", func(t *testing.T) {
		wantErr := errors.New("provider details must remain private")
		listener, err := NewPollingListener(pollingReconcilerFunc(func(context.Context, MailboxKey) error {
			return wantErr
		}), time.Minute)
		if err != nil {
			t.Fatal(err)
		}

		err = listener.Run(context.Background(), []MailboxKey{{Mailbox: Mailbox{Name: "INBOX"}}})
		if !errors.Is(err, wantErr) {
			t.Fatalf("Run() error = %v, want wrapped reconciliation error", err)
		}
		status := listener.Status()
		if status.State != ListenerStatusFailed || status.ErrorCategory != LogErrorUnknown || status.LastEventAt.IsZero() || !status.LastSuccessfulReconciliationAt.IsZero() {
			t.Fatalf("failed polling status = %#v", status)
		}
	})
}

func TestNewPollingListenerValidation(t *testing.T) {
	reconciler := pollingReconcilerFunc(func(context.Context, MailboxKey) error { return nil })
	for _, test := range []struct {
		name       string
		reconciler Reconciler
		interval   time.Duration
	}{
		{name: "missing reconciler", interval: time.Minute},
		{name: "zero interval", reconciler: reconciler},
		{name: "negative interval", reconciler: reconciler, interval: -time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewPollingListener(test.reconciler, test.interval)
			if !errors.Is(err, ErrInvalidPollingListener) {
				t.Fatalf("NewPollingListener() error = %v, want ErrInvalidPollingListener", err)
			}
		})
	}
}

func awaitSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func awaitValue[T any](t *testing.T, values <-chan T, description string) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(time.Second):
		var zero T
		t.Fatalf("timed out waiting for %s", description)
		return zero
	}
}
