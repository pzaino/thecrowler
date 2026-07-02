package mail

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
)

// ErrCheckpointConflict indicates that a checkpoint changed after it was
// loaded. Callers should load the current checkpoint and retry their update.
var ErrCheckpointConflict = errors.New("mail checkpoint version conflict")

// MemoryStateStore is a concurrency-safe, process-local StateStore. It is
// intended for tests and local use where state does not need to survive a
// process restart.
//
// The zero value is ready for use.
type MemoryStateStore struct {
	mu          sync.RWMutex
	checkpoints map[MailboxKey]Checkpoint
	nextVersion uint64
}

// InMemoryStateStore is an alias for MemoryStateStore.
type InMemoryStateStore = MemoryStateStore

// NewMemoryStateStore returns an empty in-memory state store.
func NewMemoryStateStore() *MemoryStateStore {
	return &MemoryStateStore{checkpoints: make(map[MailboxKey]Checkpoint)}
}

// NewInMemoryStateStore returns an empty in-memory state store.
func NewInMemoryStateStore() *InMemoryStateStore {
	return NewMemoryStateStore()
}

// LoadCheckpoint returns the checkpoint for key. An unknown key has an empty
// checkpoint and no error, allowing its first commit to use an empty previous
// version.
func (s *MemoryStateStore) LoadCheckpoint(ctx context.Context, key MailboxKey) (Checkpoint, error) {
	if err := ctx.Err(); err != nil {
		return Checkpoint{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if err := ctx.Err(); err != nil {
		return Checkpoint{}, err
	}
	return s.checkpoints[key], nil
}

// CommitCheckpoint atomically replaces the checkpoint for key when
// previousVersion matches the currently stored version. The store assigns a
// new opaque version and ignores any Version supplied in next.
func (s *MemoryStateStore) CommitCheckpoint(ctx context.Context, key MailboxKey, previousVersion string, next Checkpoint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateCheckpoint(next); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return err
	}

	current, exists := s.checkpoints[key]
	if current.Version != previousVersion {
		return fmt.Errorf(
			"%w: source %q account %q mailbox %q has version %q, not %q",
			ErrCheckpointConflict,
			key.SourceID,
			key.AccountID,
			mailboxIdentity(key.Mailbox),
			current.Version,
			previousVersion,
		)
	}
	if err := validateCheckpointStatusTransition(current.MessageStatus, next.MessageStatus, exists); err != nil {
		return err
	}

	if s.checkpoints == nil {
		s.checkpoints = make(map[MailboxKey]Checkpoint)
	}
	s.nextVersion++
	next.Version = strconv.FormatUint(s.nextVersion, 10)
	s.checkpoints[key] = next
	return nil
}

func mailboxIdentity(mailbox Mailbox) string {
	if mailbox.ID != "" {
		return mailbox.ID
	}
	return mailbox.Name
}

var _ StateStore = (*MemoryStateStore)(nil)
