// Copyright 2023 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package database

import (
	"context"
	"errors"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func TestTransactionContextDefaultAndBounds(t *testing.T) {
	deadlineWithin := func(t *testing.T, ctx context.Context, want time.Duration) {
		t.Helper()
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("expected context to carry a deadline")
		}
		remaining := time.Until(deadline)
		if remaining <= 0 || remaining > want {
			t.Fatalf("deadline remaining = %s, want <= %s and > 0", remaining, want)
		}
	}

	t.Run("nil config uses default", func(t *testing.T) {
		ctx, cancel := TransactionContext(context.Background(), nil)
		defer cancel()
		deadlineWithin(t, ctx, cfg.DefaultTransactionTimeoutSeconds*time.Second)
	})

	t.Run("nil parent uses background", func(t *testing.T) {
		ctx, cancel := TransactionContext(nil, nil)
		defer cancel()
		deadlineWithin(t, ctx, cfg.DefaultTransactionTimeoutSeconds*time.Second)
	})

	t.Run("zero timeout uses default", func(t *testing.T) {
		ctx, cancel := TransactionContext(context.Background(), &cfg.Config{})
		defer cancel()
		deadlineWithin(t, ctx, cfg.DefaultTransactionTimeoutSeconds*time.Second)
	})

	t.Run("configured timeout is applied", func(t *testing.T) {
		c := &cfg.Config{Database: cfg.Database{TransactionTimeout: 60}}
		ctx, cancel := TransactionContext(context.Background(), c)
		defer cancel()
		deadlineWithin(t, ctx, 60*time.Second)
	})

	t.Run("invalid timeout uses default", func(t *testing.T) {
		c := &cfg.Config{Database: cfg.Database{TransactionTimeout: 5}}
		ctx, cancel := TransactionContext(context.Background(), c)
		defer cancel()
		deadlineWithin(t, ctx, cfg.DefaultTransactionTimeoutSeconds*time.Second)
	})

	t.Run("parent deadline can expire sooner", func(t *testing.T) {
		parent, parentCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer parentCancel()
		ctx, cancel := TransactionContext(parent, nil)
		defer cancel()
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("expected context to carry a deadline")
		}
		parentDeadline, _ := parent.Deadline()
		if deadline.After(parentDeadline) {
			t.Fatalf("child deadline %s must not exceed parent deadline %s", deadline, parentDeadline)
		}
	})
}

func TestTransactionContextCancelStopsQueries(t *testing.T) {
	ctx, cancel := TransactionContext(context.Background(), &cfg.Config{
		Database: cfg.Database{TransactionTimeout: 30},
	})
	cancel()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("context not cancelled")
	}
	if !IsTransactionTimeout(ctx.Err()) {
		// cancel() before the deadline yields context.Canceled, not DeadlineExceeded;
		// both must be observed as context errors by callers.
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatalf("unexpected error after cancel: %v", ctx.Err())
		}
	}
}

func TestIsTransactionTimeout(t *testing.T) {
	if !IsTransactionTimeout(context.DeadlineExceeded) {
		t.Fatal("expected DeadlineExceeded to classify as transaction timeout")
	}
	if !IsTransactionTimeout(wrappedTimeoutErr()) {
		t.Fatal("expected wrapped DeadlineExceeded to classify as transaction timeout")
	}
	if IsTransactionTimeout(errors.New("some other error")) {
		t.Fatal("unexpected classification for unrelated error")
	}
	if IsTransactionTimeout(nil) {
		t.Fatal("nil is not a timeout")
	}
}

func wrappedTimeoutErr() error {
	return &timeoutWrap{err: context.DeadlineExceeded}
}

type timeoutWrap struct{ err error }

func (w *timeoutWrap) Error() string { return "wrapped: " + w.err.Error() }
func (w *timeoutWrap) Unwrap() error { return w.err }

func TestLogTransactionOutcomeSmoke(t *testing.T) {
	// Success is logged at debug level 4 (silent unless debug is enabled);
	// failures log once at error level. Neither must panic.
	started := time.Now().Add(-time.Second)
	LogTransactionOutcome("unit-test-success", started, nil)
	LogTransactionOutcome("unit-test-timeout", started, context.DeadlineExceeded)
	LogTransactionOutcome("unit-test-failure", started, errors.New("boom"))
}
