package database

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
)

var (
	// GlobalEventBus is the global event bus instance.
	GlobalEventBus     *EventBus
	globalEventBusOnce sync.Once
	globalEventBusStop func()

	ingestionMu      sync.Mutex
	ingestionRunning bool
)

// EventFilter defines the filtering criteria for subscribing to events.
type EventFilter struct {
	// Empty means "match all"
	Types      []string // exact match
	TypePrefix string   // prefix match, case-insensitive
	SourceID   *uint64  // optional
	Severities []string // exact match, case-insensitive
}

type subscription struct {
	id     string
	filter EventFilter
	ch     chan Event
	closed atomic.Bool
}

// EventBus is a simple in-memory event bus with filtering capabilities.
type EventBus struct {
	mu       sync.RWMutex
	subs     map[string]*subscription
	seq      uint64
	subCount atomic.Int32
}

// NewEventBus creates a new EventBus instance.
func NewEventBus() *EventBus {
	return &EventBus{
		subs: make(map[string]*subscription),
	}
}

// InitGlobalEventBus starts the global event bus.
func InitGlobalEventBus(_ *Handler) {
	globalEventBusOnce.Do(func() {
		GlobalEventBus, globalEventBusStop = StartEventBus()
	})
}

// StopGlobalEventBus stops the global event bus.
func StopGlobalEventBus() {
	if globalEventBusStop != nil {
		globalEventBusStop()
	}
}

// Subscribe adds a new subscriber with the given filter and buffer size.
func (b *EventBus) Subscribe(
	filter EventFilter,
	buffer int,
) (string, <-chan Event, func()) {

	if buffer <= 0 {
		buffer = 16
	}

	id := b.nextID()

	sub := &subscription{
		id:     id,
		filter: normalizeFilter(filter),
		ch:     make(chan Event, buffer),
	}

	b.mu.Lock()
	b.subs[id] = sub
	b.mu.Unlock()

	// Transition 0 -> 1
	if b.subCount.Add(1) == 1 {
		startGlobalEventIngestion()
	}

	cancel := func() {
		b.Unsubscribe(id)
	}

	return id, sub.ch, cancel
}

// Unsubscribe removes the subscriber with the given ID.
func (b *EventBus) Unsubscribe(id string) {
	b.mu.Lock()
	sub := b.subs[id]
	if sub != nil {
		delete(b.subs, id)
	}
	b.mu.Unlock()

	if sub == nil {
		return
	}

	if sub.closed.CompareAndSwap(false, true) {
		close(sub.ch)
	}

	// Transition 1 -> 0
	if b.subCount.Add(-1) == 0 {
		stopGlobalEventIngestion()
	}
}

func startGlobalEventIngestion() {
	ingestionMu.Lock()
	defer ingestionMu.Unlock()

	if ingestionRunning {
		return
	}

	GlobalEventBus, globalEventBusStop = StartEventBus()
	ingestionRunning = true

	cmn.DebugMsg(cmn.DbgLvlInfo, "Event ingestion started")
}

func stopGlobalEventIngestion() {
	ingestionMu.Lock()
	defer ingestionMu.Unlock()

	if !ingestionRunning {
		return
	}

	if globalEventBusStop != nil {
		globalEventBusStop()
	}

	ingestionRunning = false

	cmn.DebugMsg(cmn.DbgLvlInfo, "Event ingestion stopped")
}

// Publish fans out the event to all matching subscribers.
// Non-blocking: if a subscriber is slow and its buffer is full, the event is dropped for that subscriber.
func (b *EventBus) Publish(e Event) {
	// DO not publish if there aren't subscribers, to avoid unnecessary work (e.g. filter matching).
	if b.subCount.Load() == 0 {
		return
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, sub := range b.subs {
		if !match(sub.filter, e) {
			continue
		}
		select {
		case sub.ch <- e:
		default:
			// Drop for this subscriber (plugin may be busy).
		}
	}
}

func (b *EventBus) nextID() string {
	n := atomic.AddUint64(&b.seq, 1)
	// tiny unique-enough ID: time + counter (no deps)
	return strings.Join([]string{time.Now().Format(time.RFC3339Nano), "sub", itoaU64(n)}, ":")
}

func normalizeFilter(f EventFilter) EventFilter {
	f.TypePrefix = strings.ToLower(strings.TrimSpace(f.TypePrefix))
	for i := range f.Types {
		f.Types[i] = strings.TrimSpace(f.Types[i])
	}
	for i := range f.Severities {
		f.Severities[i] = strings.ToLower(strings.TrimSpace(f.Severities[i]))
	}
	return f
}

func match(f EventFilter, e Event) bool {
	if f.SourceID != nil && e.SourceID != *f.SourceID {
		return false
	}

	if len(f.Severities) > 0 {
		es := strings.ToLower(strings.TrimSpace(e.Severity))
		ok := false
		for _, s := range f.Severities {
			if s == es {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}

	et := strings.TrimSpace(e.Type)

	if f.TypePrefix != "" {
		if !strings.HasPrefix(strings.ToLower(et), f.TypePrefix) {
			return false
		}
	}

	if len(f.Types) > 0 {
		ok := false
		for _, t := range f.Types {
			if t == et {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}

	return true
}

// tiny helper to avoid fmt in hot path
func itoaU64(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + (v % 10))
		v /= 10
	}
	return string(buf[i:])
}
