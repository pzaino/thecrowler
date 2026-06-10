package mail

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestMemoryStateStoreIsolatesMailboxState(t *testing.T) {
	t.Parallel()

	store := NewMemoryStateStore()
	ctx := context.Background()
	keys := []MailboxKey{
		{SourceID: "source-a", AccountID: "account", Mailbox: Mailbox{ID: "inbox", Name: "Inbox"}},
		{SourceID: "source-b", AccountID: "account", Mailbox: Mailbox{ID: "inbox", Name: "Inbox"}},
		{SourceID: "source-a", AccountID: "other-account", Mailbox: Mailbox{ID: "inbox", Name: "Inbox"}},
		{SourceID: "source-a", AccountID: "account", Mailbox: Mailbox{ID: "archive", Name: "Archive"}},
	}

	for i, key := range keys {
		next := Checkpoint{
			Cursor:        Cursor{Token: fmt.Sprintf("cursor-%d", i), UID: uint32(i + 10), UIDValidity: uint32(i + 100)},
			MessageStatus: fmt.Sprintf("status-%d", i),
			ContentHash:   fmt.Sprintf("hash-%d", i),
			ErrorCount:    uint32(i),
			LastError:     fmt.Sprintf("error-%d", i),
		}
		if err := store.CommitCheckpoint(ctx, key, "", next); err != nil {
			t.Fatalf("CommitCheckpoint(%d) error = %v", i, err)
		}
	}

	versions := make(map[string]struct{}, len(keys))
	for i, key := range keys {
		got, err := store.LoadCheckpoint(ctx, key)
		if err != nil {
			t.Fatalf("LoadCheckpoint(%d) error = %v", i, err)
		}
		if got.Cursor.Token != fmt.Sprintf("cursor-%d", i) ||
			got.Cursor.UID != uint32(i+10) ||
			got.Cursor.UIDValidity != uint32(i+100) ||
			got.MessageStatus != fmt.Sprintf("status-%d", i) ||
			got.ContentHash != fmt.Sprintf("hash-%d", i) ||
			got.ErrorCount != uint32(i) ||
			got.LastError != fmt.Sprintf("error-%d", i) {
			t.Errorf("LoadCheckpoint(%d) = %#v, state was not isolated", i, got)
		}
		if got.Version == "" {
			t.Errorf("LoadCheckpoint(%d) returned an empty version", i)
		}
		if _, exists := versions[got.Version]; exists {
			t.Errorf("LoadCheckpoint(%d) reused version %q", i, got.Version)
		}
		versions[got.Version] = struct{}{}
	}
}

func TestMemoryStateStoreUpdatesAndRejectsStaleVersion(t *testing.T) {
	t.Parallel()

	var store MemoryStateStore
	ctx := context.Background()
	key := MailboxKey{SourceID: "source", AccountID: "account", Mailbox: Mailbox{Name: "Inbox"}}

	missing, err := store.LoadCheckpoint(ctx, key)
	if err != nil {
		t.Fatalf("LoadCheckpoint(missing) error = %v", err)
	}
	if missing != (Checkpoint{}) {
		t.Fatalf("LoadCheckpoint(missing) = %#v, want zero checkpoint", missing)
	}

	first := Checkpoint{
		Cursor:        Cursor{Token: "page-1", UID: 41, UIDValidity: 7},
		MessageStatus: "failed",
		ContentHash:   "sha256:first",
		ErrorCount:    1,
		LastError:     "temporary failure",
		Version:       "caller-supplied-version",
	}
	if err := store.CommitCheckpoint(ctx, key, "", first); err != nil {
		t.Fatalf("first CommitCheckpoint() error = %v", err)
	}

	stored, err := store.LoadCheckpoint(ctx, key)
	if err != nil {
		t.Fatalf("LoadCheckpoint(first) error = %v", err)
	}
	if stored.Version == "" || stored.Version == first.Version {
		t.Fatalf("stored version = %q, want a store-assigned version", stored.Version)
	}

	second := Checkpoint{
		Cursor:        Cursor{Token: "page-2", UID: 42, UIDValidity: 7},
		MessageStatus: "processed",
		ContentHash:   "sha256:second",
	}
	if err := store.CommitCheckpoint(ctx, key, stored.Version, second); err != nil {
		t.Fatalf("second CommitCheckpoint() error = %v", err)
	}
	if err := store.CommitCheckpoint(ctx, key, stored.Version, first); !errors.Is(err, ErrCheckpointConflict) {
		t.Fatalf("stale CommitCheckpoint() error = %v, want ErrCheckpointConflict", err)
	}

	updated, err := store.LoadCheckpoint(ctx, key)
	if err != nil {
		t.Fatalf("LoadCheckpoint(updated) error = %v", err)
	}
	if updated.Cursor != second.Cursor || updated.MessageStatus != second.MessageStatus ||
		updated.ContentHash != second.ContentHash || updated.ErrorCount != 0 || updated.LastError != "" {
		t.Fatalf("LoadCheckpoint(updated) = %#v, want second checkpoint fields", updated)
	}
	if updated.Version == stored.Version {
		t.Fatalf("updated version = %q, want a new version", updated.Version)
	}
}

func TestMemoryStateStoreConcurrentUpdates(t *testing.T) {
	const (
		workers    = 16
		increments = 50
	)

	store := NewInMemoryStateStore()
	ctx := context.Background()
	key := MailboxKey{SourceID: "source", AccountID: "account", Mailbox: Mailbox{ID: "inbox"}}

	var wg sync.WaitGroup
	errorsFound := make(chan error, workers)
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for increment := 0; increment < increments; increment++ {
				for {
					current, err := store.LoadCheckpoint(ctx, key)
					if err != nil {
						errorsFound <- err
						return
					}
					next := current
					next.ErrorCount++
					next.Cursor.UID++
					next.MessageStatus = "processed"
					if err := store.CommitCheckpoint(ctx, key, current.Version, next); err == nil {
						break
					} else if !errors.Is(err, ErrCheckpointConflict) {
						errorsFound <- err
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	close(errorsFound)

	for err := range errorsFound {
		t.Errorf("concurrent state update error = %v", err)
	}

	got, err := store.LoadCheckpoint(ctx, key)
	if err != nil {
		t.Fatalf("LoadCheckpoint() error = %v", err)
	}
	want := uint32(workers * increments)
	if got.ErrorCount != want || got.Cursor.UID != want {
		t.Fatalf("concurrent state = %#v, want error count and UID %d", got, want)
	}
}

func TestMemoryStateStoreHonorsCanceledContext(t *testing.T) {
	t.Parallel()

	store := NewMemoryStateStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	key := MailboxKey{SourceID: "source", AccountID: "account", Mailbox: Mailbox{ID: "inbox"}}

	if _, err := store.LoadCheckpoint(ctx, key); !errors.Is(err, context.Canceled) {
		t.Fatalf("LoadCheckpoint() error = %v, want context.Canceled", err)
	}
	if err := store.CommitCheckpoint(ctx, key, "", Checkpoint{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("CommitCheckpoint() error = %v, want context.Canceled", err)
	}
}
