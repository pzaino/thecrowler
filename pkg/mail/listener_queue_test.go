package mail

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

type fakeListenerJobQueue struct {
	mu       sync.Mutex
	jobs     []ListenerJob
	failures []error
}

func (queue *fakeListenerJobQueue) EnqueueListenerJob(ctx context.Context, job ListenerJob) error {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(queue.failures) > 0 {
		err := queue.failures[0]
		queue.failures = queue.failures[1:]
		if err != nil {
			return err
		}
	}
	queue.jobs = append(queue.jobs, job)
	return nil
}

func (queue *fakeListenerJobQueue) snapshot() []ListenerJob {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	return append([]ListenerJob(nil), queue.jobs...)
}

func TestListenerQueueBridgeDeduplicatesStableEvents(t *testing.T) {
	t.Parallel()

	queue := &fakeListenerJobQueue{}
	bridge := newTestListenerQueueBridge(t, queue, ListenerQueueOptions{})
	event := validEmailChangeEvent()

	queued, err := bridge.SubmitEvent(context.Background(), event)
	if err != nil || !queued {
		t.Fatalf("first SubmitEvent() = (%t, %v), want queued", queued, err)
	}
	duplicate := event
	duplicate.Metadata.EventID = "redelivery-456"
	duplicate.Metadata.ReceivedAt = duplicate.Metadata.ReceivedAt.Add(time.Minute)
	queued, err = bridge.SubmitEvent(context.Background(), duplicate)
	if err != nil || queued {
		t.Fatalf("duplicate SubmitEvent() = (%t, %v), want coalesced", queued, err)
	}

	jobs := queue.snapshot()
	if len(jobs) != 1 {
		t.Fatalf("queued jobs = %d, want 1", len(jobs))
	}
	want := ListenerJob{
		Kind: ListenerJobReconcile,
		Mailbox: MailboxKey{
			SourceID:  "source-1",
			Provider:  event.Provider,
			AccountID: event.AccountID,
			Mailbox:   event.Mailbox,
		},
		Cursor: event.Cursor,
		Change: Change{Kind: ChangeUpsert},
	}
	if !reflect.DeepEqual(jobs[0], want) {
		t.Fatalf("queued job = %#v, want %#v", jobs[0], want)
	}
}

func TestListenerQueueBridgePreservesAcceptedOrderAndSelectsJobKind(t *testing.T) {
	t.Parallel()

	queue := &fakeListenerJobQueue{}
	bridge := newTestListenerQueueBridge(t, queue, ListenerQueueOptions{})
	mailbox := MailboxKey{
		Provider:  "imap",
		AccountID: "account-1",
		Mailbox:   Mailbox{ID: "INBOX", Name: "Inbox"},
	}
	inputs := []ListenerChange{
		{
			Mailbox: mailbox,
			Cursor:  Cursor{UIDValidity: 9, UID: 40},
			Change: Change{Kind: ChangeUpsert, Ref: MessageRef{
				Provider: "imap", AccountID: "account-1", Mailbox: mailbox.Mailbox,
				UIDValidity: 9, UID: 41,
			}},
		},
		{
			Mailbox: mailbox,
			Cursor:  Cursor{UIDValidity: 9, UID: 41},
			Change:  Change{Kind: ChangeDelete},
		},
		{
			Mailbox: mailbox,
			Cursor:  Cursor{UIDValidity: 10},
			Change:  Change{Kind: ChangeReset},
		},
	}
	for index, input := range inputs {
		queued, err := bridge.Submit(context.Background(), input)
		if err != nil || !queued {
			t.Fatalf("Submit(%d) = (%t, %v), want queued", index, queued, err)
		}
	}

	jobs := queue.snapshot()
	if len(jobs) != len(inputs) {
		t.Fatalf("queued jobs = %d, want %d", len(jobs), len(inputs))
	}
	wantKinds := []ListenerJobKind{ListenerJobFetchMessage, ListenerJobReconcile, ListenerJobReconcile}
	wantChanges := []ChangeKind{ChangeUpsert, ChangeDelete, ChangeReset}
	for index := range jobs {
		if jobs[index].Kind != wantKinds[index] || jobs[index].Change.Kind != wantChanges[index] {
			t.Errorf("job %d = (%q, %q), want (%q, %q)", index, jobs[index].Kind, jobs[index].Change.Kind, wantKinds[index], wantChanges[index])
		}
	}
}

func TestListenerQueueBridgeCancellationDoesNotPoisonDeduplication(t *testing.T) {
	t.Parallel()

	queue := &fakeListenerJobQueue{}
	bridge := newTestListenerQueueBridge(t, queue, ListenerQueueOptions{})
	mailbox := MailboxKey{Provider: "gmail", AccountID: "account-1", Mailbox: Mailbox{ID: "INBOX"}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	queued, err := bridge.Submit(ctx, ListenerChange{Mailbox: mailbox})
	if !errors.Is(err, context.Canceled) || queued {
		t.Fatalf("cancelled Submit() = (%t, %v), want context.Canceled", queued, err)
	}
	if jobs := queue.snapshot(); len(jobs) != 0 {
		t.Fatalf("cancelled submission queued %d jobs, want 0", len(jobs))
	}
	queued, err = bridge.Submit(context.Background(), ListenerChange{Mailbox: mailbox})
	if err != nil || !queued {
		t.Fatalf("retry Submit() = (%t, %v), want queued", queued, err)
	}
}

func TestListenerQueueBridgeRetriesAfterQueueFailure(t *testing.T) {
	t.Parallel()

	queueFailure := errors.New("queue unavailable")
	queue := &fakeListenerJobQueue{failures: []error{queueFailure}}
	bridge := newTestListenerQueueBridge(t, queue, ListenerQueueOptions{})
	change := ListenerChange{Mailbox: MailboxKey{
		Provider: "graph", AccountID: "account-1", Mailbox: Mailbox{ID: "inbox"},
	}}

	queued, err := bridge.Submit(context.Background(), change)
	if !errors.Is(err, queueFailure) || queued {
		t.Fatalf("failed Submit() = (%t, %v), want queue failure", queued, err)
	}
	queued, err = bridge.Submit(context.Background(), change)
	if err != nil || !queued {
		t.Fatalf("retry Submit() = (%t, %v), want queued", queued, err)
	}
	if jobs := queue.snapshot(); len(jobs) != 1 {
		t.Fatalf("queued jobs after retry = %d, want 1", len(jobs))
	}
}

func TestListenerQueueBridgeDedupRetentionIsBoundedAndExpires(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.June, 11, 15, 0, 0, 0, time.UTC)
	queue := &fakeListenerJobQueue{}
	bridge := newTestListenerQueueBridge(t, queue, ListenerQueueOptions{
		DedupCapacity: 2,
		DedupTTL:      time.Minute,
		Now:           func() time.Time { return now },
	})
	change := func(mailbox string) ListenerChange {
		return ListenerChange{Mailbox: MailboxKey{
			Provider: "imap", AccountID: "account-1", Mailbox: Mailbox{ID: mailbox},
		}}
	}

	for _, mailbox := range []string{"one", "two", "three"} {
		if queued, err := bridge.Submit(context.Background(), change(mailbox)); err != nil || !queued {
			t.Fatalf("Submit(%q) = (%t, %v), want queued", mailbox, queued, err)
		}
	}
	// The oldest key was evicted when the third key was accepted.
	if queued, err := bridge.Submit(context.Background(), change("one")); err != nil || !queued {
		t.Fatalf("evicted Submit() = (%t, %v), want queued", queued, err)
	}
	if queued, err := bridge.Submit(context.Background(), change("one")); err != nil || queued {
		t.Fatalf("immediate duplicate Submit() = (%t, %v), want coalesced", queued, err)
	}
	now = now.Add(time.Minute)
	if queued, err := bridge.Submit(context.Background(), change("one")); err != nil || !queued {
		t.Fatalf("expired Submit() = (%t, %v), want queued", queued, err)
	}
}

func TestListenerQueueBridgeRejectsMismatchedFetchScope(t *testing.T) {
	t.Parallel()

	queue := &fakeListenerJobQueue{}
	bridge := newTestListenerQueueBridge(t, queue, ListenerQueueOptions{})
	mailbox := MailboxKey{Provider: "gmail", AccountID: "account-1", Mailbox: Mailbox{ID: "INBOX"}}
	_, err := bridge.Submit(context.Background(), ListenerChange{
		Mailbox: mailbox,
		Change: Change{Kind: ChangeUpsert, Ref: MessageRef{
			Provider: "gmail", AccountID: "other-account", Mailbox: mailbox.Mailbox,
			ProviderMessageID: "message-1",
		}},
	})
	if !errors.Is(err, ErrInvalidListenerChange) {
		t.Fatalf("Submit() error = %v, want ErrInvalidListenerChange", err)
	}
	if jobs := queue.snapshot(); len(jobs) != 0 {
		t.Fatalf("invalid submission queued %d jobs, want 0", len(jobs))
	}
}

func newTestListenerQueueBridge(t *testing.T, queue ListenerJobQueue, options ListenerQueueOptions) *ListenerQueueBridge {
	t.Helper()
	bridge, err := NewListenerQueueBridge("source-1", queue, options)
	if err != nil {
		t.Fatalf("NewListenerQueueBridge() error = %v", err)
	}
	return bridge
}
