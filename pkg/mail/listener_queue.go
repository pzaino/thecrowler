package mail

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultListenerDedupCapacity = 4096
	defaultListenerDedupTTL      = 5 * time.Minute
)

var (
	// ErrInvalidListenerChange identifies a provider-neutral change that cannot
	// be safely scoped to a mailbox or converted into a queue job.
	ErrInvalidListenerChange = errors.New("mail: invalid listener change")
	// ErrInvalidListenerQueueOptions identifies invalid bridge retention
	// settings.
	ErrInvalidListenerQueueOptions = errors.New("mail: invalid listener queue options")
)

// ListenerJobKind identifies the bounded work requested by a listener hint.
type ListenerJobKind string

const (
	// ListenerJobReconcile asks a worker to converge a mailbox from its durable
	// checkpoint. It is used for coarse hints, deletes, and reset signals.
	ListenerJobReconcile ListenerJobKind = "reconcile"
	// ListenerJobFetchMessage asks a worker to fetch one provider-neutral
	// message reference. The worker remains responsible for durable ordering.
	ListenerJobFetchMessage ListenerJobKind = "fetch_message"
)

// Valid reports whether kind is a defined listener job kind.
func (kind ListenerJobKind) Valid() bool {
	return kind == ListenerJobReconcile || kind == ListenerJobFetchMessage
}

// ListenerChange is the provider-neutral input accepted from listeners that
// can identify an individual message. A zero Change is a coarse mailbox hint.
type ListenerChange struct {
	Mailbox MailboxKey `json:"mailbox" yaml:"mailbox"`
	Cursor  Cursor     `json:"cursor,omitempty" yaml:"cursor,omitempty"`
	Change  Change     `json:"change,omitempty" yaml:"change,omitempty"`
}

// ListenerJob is the closed queue payload produced by ListenerQueueBridge.
// Reconciliation jobs carry no message reference. Message-fetch jobs are
// emitted only for an upsert with a stable provider ID or IMAP UID tuple.
type ListenerJob struct {
	Kind    ListenerJobKind `json:"kind" yaml:"kind"`
	Mailbox MailboxKey      `json:"mailbox" yaml:"mailbox"`
	Cursor  Cursor          `json:"cursor,omitempty" yaml:"cursor,omitempty"`
	Change  Change          `json:"change,omitempty" yaml:"change,omitempty"`
}

// StableKey returns an opaque, deterministic key for successful-enqueue
// deduplication. Operational metadata and delivery timestamps are excluded.
func (job ListenerJob) StableKey() string {
	hash := sha256.New()
	writeStablePart := func(value string) {
		_, _ = hash.Write([]byte(strconv.Itoa(len(value))))
		_, _ = hash.Write([]byte{':'})
		_, _ = hash.Write([]byte(value))
	}

	writeStablePart(string(job.Kind))
	writeStablePart(strings.TrimSpace(job.Mailbox.SourceID))
	writeStablePart(strings.ToLower(strings.TrimSpace(job.Mailbox.Provider)))
	writeStablePart(strings.TrimSpace(job.Mailbox.AccountID))
	writeStablePart(stableMailboxID(job.Mailbox.Mailbox))
	writeStablePart(job.Cursor.Token)
	writeStablePart(strconv.FormatUint(job.Cursor.HistoryID, 10))
	writeStablePart(strconv.FormatUint(uint64(job.Cursor.UIDValidity), 10))
	writeStablePart(strconv.FormatUint(uint64(job.Cursor.UID), 10))
	writeStablePart(string(job.Change.Kind))
	if job.Kind == ListenerJobFetchMessage {
		ref := job.Change.Ref
		writeStablePart(strings.ToLower(strings.TrimSpace(ref.Provider)))
		writeStablePart(strings.TrimSpace(ref.AccountID))
		writeStablePart(stableMailboxID(ref.Mailbox))
		writeStablePart(strings.TrimSpace(ref.ProviderMessageID))
		writeStablePart(strconv.FormatUint(uint64(ref.UIDValidity), 10))
		writeStablePart(strconv.FormatUint(uint64(ref.UID), 10))
		writeStablePart(strings.TrimSpace(ref.Version))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// ListenerJobQueue is the minimal queue boundary used by the bridge.
type ListenerJobQueue interface {
	EnqueueListenerJob(ctx context.Context, job ListenerJob) error
}

// ListenerQueueOptions bounds successful-enqueue deduplication. Zero values
// select conservative defaults. Now exists for deterministic tests.
type ListenerQueueOptions struct {
	DedupCapacity int
	DedupTTL      time.Duration
	Now           func() time.Time
}

type listenerDedupEntry struct {
	key       string
	expiresAt time.Time
}

// ListenerQueueBridge converts listener hints into queue jobs. It serializes
// enqueue calls so accepted jobs preserve input order, and records a stable key
// only after the queue accepts a job so failed or cancelled submissions remain
// retryable.
type ListenerQueueBridge struct {
	sourceID string
	queue    ListenerJobQueue
	capacity int
	ttl      time.Duration
	now      func() time.Time

	mu      sync.Mutex
	entries map[string]*list.Element
	order   *list.List
}

// NewListenerQueueBridge constructs a bridge for one source. The source ID is
// supplied by configuration rather than trusted from a provider notification.
func NewListenerQueueBridge(sourceID string, queue ListenerJobQueue, options ListenerQueueOptions) (*ListenerQueueBridge, error) {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return nil, fmt.Errorf("%w: source ID is required", ErrInvalidListenerQueueOptions)
	}
	if queue == nil {
		return nil, fmt.Errorf("%w: queue is required", ErrInvalidListenerQueueOptions)
	}
	if options.DedupCapacity < 0 {
		return nil, fmt.Errorf("%w: dedup capacity cannot be negative", ErrInvalidListenerQueueOptions)
	}
	if options.DedupTTL < 0 {
		return nil, fmt.Errorf("%w: dedup TTL cannot be negative", ErrInvalidListenerQueueOptions)
	}
	capacity := options.DedupCapacity
	if capacity == 0 {
		capacity = defaultListenerDedupCapacity
	}
	ttl := options.DedupTTL
	if ttl == 0 {
		ttl = defaultListenerDedupTTL
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &ListenerQueueBridge{
		sourceID: sourceID,
		queue:    queue,
		capacity: capacity,
		ttl:      ttl,
		now:      now,
		entries:  make(map[string]*list.Element, capacity),
		order:    list.New(),
	}, nil
}

// Notify implements EventSink by converting a coarse mailbox hint into a
// reconciliation job. Duplicate accepted hints are silently coalesced.
func (bridge *ListenerQueueBridge) Notify(ctx context.Context, mailbox MailboxKey) error {
	mailbox.SourceID = bridge.sourceID
	_, err := bridge.Submit(ctx, ListenerChange{Mailbox: mailbox})
	return err
}

// Enqueue implements EmailChangeQueue, allowing verified webhook receivers to
// send their closed provider-neutral events directly through the bridge.
func (bridge *ListenerQueueBridge) Enqueue(ctx context.Context, event EmailChangeEvent) error {
	_, err := bridge.SubmitEvent(ctx, event)
	return err
}

// SubmitEvent converts a validated EmailChangeEvent into a reconciliation job.
// Notification cursors remain advisory and are never persisted by the bridge.
func (bridge *ListenerQueueBridge) SubmitEvent(ctx context.Context, event EmailChangeEvent) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := event.Validate(); err != nil {
		return false, err
	}
	return bridge.Submit(ctx, ListenerChange{
		Mailbox: MailboxKey{
			SourceID:  bridge.sourceID,
			Provider:  event.Provider,
			AccountID: event.AccountID,
			Mailbox:   event.Mailbox,
		},
		Cursor: event.Cursor,
		Change: Change{Kind: event.ChangeType},
	})
}

// Submit converts a provider-neutral change into one queue job. It returns
// false with no error when a successful, unexpired duplicate was coalesced.
func (bridge *ListenerQueueBridge) Submit(ctx context.Context, change ListenerChange) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	job, err := bridge.jobFor(change)
	if err != nil {
		return false, err
	}
	key := job.StableKey()

	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	now := bridge.now().UTC()
	bridge.purgeExpired(now)
	if _, duplicate := bridge.entries[key]; duplicate {
		return false, nil
	}
	if err := bridge.queue.EnqueueListenerJob(ctx, job); err != nil {
		return false, fmt.Errorf("mail: enqueue listener job: %w", err)
	}
	bridge.remember(key, now.Add(bridge.ttl))
	return true, nil
}

func (bridge *ListenerQueueBridge) jobFor(input ListenerChange) (ListenerJob, error) {
	key := input.Mailbox
	key.SourceID = bridge.sourceID
	key.Provider = strings.TrimSpace(key.Provider)
	key.AccountID = strings.TrimSpace(key.AccountID)
	if key.Provider == "" || key.AccountID == "" || stableMailboxID(key.Mailbox) == "" {
		return ListenerJob{}, fmt.Errorf("%w: provider, account, and mailbox are required", ErrInvalidListenerChange)
	}

	job := ListenerJob{Kind: ListenerJobReconcile, Mailbox: key, Cursor: input.Cursor}
	kind := input.Change.Kind
	if kind == "" {
		return job, nil
	}
	if !kind.Valid() {
		return ListenerJob{}, fmt.Errorf("%w: unsupported change kind %q", ErrInvalidListenerChange, kind)
	}
	if kind != ChangeUpsert {
		job.Change.Kind = kind
		return job, nil
	}
	if !fetchableMessageRef(input.Change.Ref) {
		job.Change.Kind = kind
		return job, nil
	}
	if err := matchingMessageScope(key, input.Change.Ref); err != nil {
		return ListenerJob{}, err
	}
	job.Kind = ListenerJobFetchMessage
	job.Change = input.Change
	return job, nil
}

func fetchableMessageRef(ref MessageRef) bool {
	return strings.TrimSpace(ref.ProviderMessageID) != "" || (ref.UID != 0 && ref.UIDValidity != 0)
}

func matchingMessageScope(key MailboxKey, ref MessageRef) error {
	if !strings.EqualFold(strings.TrimSpace(ref.Provider), key.Provider) ||
		strings.TrimSpace(ref.AccountID) != key.AccountID ||
		stableMailboxID(ref.Mailbox) != stableMailboxID(key.Mailbox) {
		return fmt.Errorf("%w: message reference does not match mailbox scope", ErrInvalidListenerChange)
	}
	return nil
}

func (bridge *ListenerQueueBridge) purgeExpired(now time.Time) {
	for element := bridge.order.Front(); element != nil; {
		next := element.Next()
		entry := element.Value.(listenerDedupEntry)
		if entry.expiresAt.After(now) {
			break
		}
		delete(bridge.entries, entry.key)
		bridge.order.Remove(element)
		element = next
	}
}

func (bridge *ListenerQueueBridge) remember(key string, expiresAt time.Time) {
	for bridge.order.Len() >= bridge.capacity {
		oldest := bridge.order.Front()
		entry := oldest.Value.(listenerDedupEntry)
		delete(bridge.entries, entry.key)
		bridge.order.Remove(oldest)
	}
	element := bridge.order.PushBack(listenerDedupEntry{key: key, expiresAt: expiresAt})
	bridge.entries[key] = element
}

var (
	_ EventSink        = (*ListenerQueueBridge)(nil)
	_ EmailChangeQueue = (*ListenerQueueBridge)(nil)
)
