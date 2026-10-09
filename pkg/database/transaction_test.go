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
	"strings"
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
	if !IsTransactionTimeout(ErrTransactionTimeout) {
		t.Fatal("expected ErrTransactionTimeout to classify as transaction timeout")
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

func TestNormalizeTransactionError(t *testing.T) {
	expired, cancelExpired := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancelExpired()
	<-expired.Done()

	canceled, cancelCanceled := context.WithCancel(context.Background())
	cancelCanceled()

	live := context.Background()
	secondary := errors.New("sql: transaction has already been committed or rolled back")

	t.Run("nil error stays successful", func(t *testing.T) {
		if err := NormalizeTransactionError(expired, nil); err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
	})

	t.Run("expired deadline classifies as timeout and preserves cause", func(t *testing.T) {
		err := NormalizeTransactionError(expired, secondary)
		if !IsTransactionTimeout(err) {
			t.Fatalf("expected timeout classification, got %v", err)
		}
		if !errors.Is(err, ErrTransactionTimeout) {
			t.Fatalf("expected ErrTransactionTimeout in chain, got %v", err)
		}
		if !strings.Contains(err.Error(), secondary.Error()) {
			t.Fatalf("expected original cause preserved, got %q", err.Error())
		}
	})

	t.Run("expired deadline keeps raw deadline error", func(t *testing.T) {
		if err := NormalizeTransactionError(expired, context.DeadlineExceeded); !IsTransactionTimeout(err) {
			t.Fatalf("expected timeout classification, got %v", err)
		}
	})

	t.Run("cancellation is not a timeout", func(t *testing.T) {
		err := NormalizeTransactionError(canceled, secondary)
		if IsTransactionTimeout(err) {
			t.Fatalf("cancellation must not classify as timeout: %v", err)
		}
		if !errors.Is(err, secondary) {
			t.Fatalf("expected original error preserved, got %v", err)
		}
	})

	t.Run("live context preserves original error", func(t *testing.T) {
		err := NormalizeTransactionError(live, secondary)
		if IsTransactionTimeout(err) {
			t.Fatalf("unexpected timeout classification: %v", err)
		}
		if !errors.Is(err, secondary) {
			t.Fatalf("expected original error preserved, got %v", err)
		}
	})

	t.Run("nil context preserves original error", func(t *testing.T) {
		if err := NormalizeTransactionError(nil, secondary); !errors.Is(err, secondary) {
			t.Fatalf("expected original error preserved, got %v", err)
		}
	})
}

func TestTransactionContextForHandler(t *testing.T) {
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

	t.Run("handler timeout provider is honored", func(t *testing.T) {
		db := Handler(&timeoutProviderHandler{
			searchFunctionTestHandler: &searchFunctionTestHandler{dbms: DBSQLiteStr},
			timeout:                   120,
		})
		ctx, cancel := TransactionContextForHandler(context.Background(), &db)
		defer cancel()
		deadlineWithin(t, ctx, 120*time.Second)
	})

	t.Run("invalid provider timeout falls back to default", func(t *testing.T) {
		db := Handler(&timeoutProviderHandler{
			searchFunctionTestHandler: &searchFunctionTestHandler{dbms: DBSQLiteStr},
			timeout:                   5,
		})
		ctx, cancel := TransactionContextForHandler(context.Background(), &db)
		defer cancel()
		deadlineWithin(t, ctx, cfg.DefaultTransactionTimeoutSeconds*time.Second)
	})

	t.Run("handler without provider falls back to default", func(t *testing.T) {
		db := Handler(&searchFunctionTestHandler{dbms: DBSQLiteStr})
		ctx, cancel := TransactionContextForHandler(context.Background(), &db)
		defer cancel()
		deadlineWithin(t, ctx, cfg.DefaultTransactionTimeoutSeconds*time.Second)
	})

	t.Run("nil handler and parent fall back safely", func(t *testing.T) {
		ctx, cancel := TransactionContextForHandler(nil, nil)
		defer cancel()
		deadlineWithin(t, ctx, cfg.DefaultTransactionTimeoutSeconds*time.Second)
	})
}

func TestConcreteHandlerTransactionTimeoutSeconds(t *testing.T) {
	if got := (&PostgresHandler{transactionTimeout: 90}).TransactionTimeoutSeconds(); got != 90 {
		t.Fatalf("postgres timeout = %d, want 90", got)
	}
	if got := (&SQLiteHandler{transactionTimeout: 120}).TransactionTimeoutSeconds(); got != 120 {
		t.Fatalf("sqlite timeout = %d, want 120", got)
	}
	if got := (&PostgresHandler{}).TransactionTimeoutSeconds(); got != cfg.DefaultTransactionTimeoutSeconds {
		t.Fatalf("unset timeout = %d, want default %d", got, cfg.DefaultTransactionTimeoutSeconds)
	}
	if got := (*PostgresHandler)(nil).TransactionTimeoutSeconds(); got != cfg.DefaultTransactionTimeoutSeconds {
		t.Fatalf("nil handler timeout = %d, want default %d", got, cfg.DefaultTransactionTimeoutSeconds)
	}
}

type timeoutProviderHandler struct {
	*searchFunctionTestHandler
	timeout int
}

func (h *timeoutProviderHandler) TransactionTimeoutSeconds() int { return h.timeout }

func TestLogTransactionOutcomeSmoke(t *testing.T) {
	// Success is logged at debug level 4 (silent unless debug is enabled);
	// failures log once at error level. Neither must panic.
	started := time.Now().Add(-time.Second)
	LogTransactionOutcome("unit-test-success", started, nil)
	LogTransactionOutcome("unit-test-timeout", started, context.DeadlineExceeded)
	LogTransactionOutcome("unit-test-failure", started, errors.New("boom"))
}
