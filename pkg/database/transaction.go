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

// Package database is responsible for handling the database setup, configuration and abstraction.
package database

// File: /pkg/database/transaction.go

import (
	"context"
	"errors"
	"fmt"
	"time"

	cmn "github.com/pzaino/thecrowler/pkg/common"
	cfg "github.com/pzaino/thecrowler/pkg/config"
)

// minTransactionTimeoutSeconds and maxTransactionTimeoutSeconds mirror the
// bounds declared in schemas/crowler-config-schema.json for
// database.transaction_timeout.
const (
	minTransactionTimeoutSeconds = 30
	maxTransactionTimeoutSeconds = 86400
)

// ErrTransactionTimeout identifies a database transaction that was aborted
// because the deadline of its transaction context expired. Callers should
// classify transaction failures with IsTransactionTimeout instead of
// inspecting driver-specific error strings.
var ErrTransactionTimeout = errors.New("database transaction timed out")

// TransactionTimeoutProvider is optionally implemented by handlers which
// retain the configured database.transaction_timeout so that code holding only
// a Handler can derive a correctly bounded transaction context.
type TransactionTimeoutProvider interface {
	TransactionTimeoutSeconds() int
}

// normalizedTransactionTimeoutSeconds returns the effective timeout for a
// configured value, falling back to the documented default when the value is
// outside the bounds declared for database.transaction_timeout.
func normalizedTransactionTimeoutSeconds(seconds int) int {
	if seconds >= minTransactionTimeoutSeconds && seconds <= maxTransactionTimeoutSeconds {
		return seconds
	}
	return cfg.DefaultTransactionTimeoutSeconds
}

// TransactionContext derives the context that bounds a single database
// transaction from the caller's context and the configured
// database.transaction_timeout. The returned cancel function must always be
// called (typically via defer) after the transaction has been committed or
// rolled back: when the deadline is reached the driver cancels any in-flight
// query and the transaction is no longer usable, which prevents abandoned
// transactions from holding locks and their MVCC snapshot indefinitely.
//
// A nil Config or an invalid configured timeout falls back to
// cfg.DefaultTransactionTimeoutSeconds so callers that have no Config
// available still get a bounded transaction.
func TransactionContext(parent context.Context, c *cfg.Config) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	timeout := cfg.DefaultTransactionTimeoutSeconds
	if c != nil {
		timeout = normalizedTransactionTimeoutSeconds(c.Database.TransactionTimeout)
	}
	return context.WithTimeout(parent, time.Duration(timeout)*time.Second)
}

// TransactionContextForHandler derives a transaction context from parent and
// the timeout retained by db. Handlers that do not implement
// TransactionTimeoutProvider, or that report an invalid timeout, fall back to
// cfg.DefaultTransactionTimeoutSeconds so callers always get a bounded
// transaction. A nil parent is treated as context.Background().
func TransactionContextForHandler(parent context.Context, db *Handler) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	timeout := cfg.DefaultTransactionTimeoutSeconds
	if db != nil && *db != nil {
		if provider, ok := (*db).(TransactionTimeoutProvider); ok {
			timeout = normalizedTransactionTimeoutSeconds(provider.TransactionTimeoutSeconds())
		}
	}
	return context.WithTimeout(parent, time.Duration(timeout)*time.Second)
}

// IsTransactionTimeout reports whether err was caused by the deadline of a
// transaction context expiring (see TransactionContext). It recognizes both
// the ErrTransactionTimeout sentinel produced by NormalizeTransactionError and
// raw context deadline errors returned by a driver.
func IsTransactionTimeout(err error) bool {
	return errors.Is(err, ErrTransactionTimeout) || errors.Is(err, context.DeadlineExceeded)
}

// NormalizeTransactionError translates a raw error observed while a
// context-bounded transaction was running into a stable classification. The
// transaction context is authoritative: when its deadline expired the returned
// error identifies ErrTransactionTimeout and preserves the original cause;
// when it was cancelled the cancellation is preserved and must not be
// reported as a timeout; otherwise the original error is returned unchanged.
// A successful transaction (nil error) always stays successful.
func NormalizeTransactionError(txCtx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if txCtx == nil {
		return err
	}
	switch ctxErr := txCtx.Err(); {
	case errors.Is(ctxErr, context.DeadlineExceeded):
		if IsTransactionTimeout(err) {
			return err
		}
		return fmt.Errorf("%w: %v", ErrTransactionTimeout, err)
	case errors.Is(ctxErr, context.Canceled):
		// An explicit cancellation (for example during shutdown) must stay a
		// cancellation and must never be misclassified as a timeout.
		return err
	default:
		return err
	}
}

// LogTransactionOutcome emits a single aggregated log line for a finished
// transaction: successful transactions are logged at debug level 4 with their
// elapsed time (no per-row or per-statement logging), while failures are
// logged once at error level, distinguishing deadline expiries from other
// errors. op identifies the logical operation (e.g. "indexPage").
func LogTransactionOutcome(op string, started time.Time, err error) {
	elapsed := time.Since(started)
	if err == nil {
		cmn.DebugMsg(cmn.DbgLvlDebug4, "transaction %q completed in %s", op, elapsed)
		return
	}
	if IsTransactionTimeout(err) {
		cmn.DebugMsg(cmn.DbgLvlError, "transaction %q timed out after %s: %v", op, elapsed, err)
		return
	}
	cmn.DebugMsg(cmn.DbgLvlError, "transaction %q failed after %s: %v", op, elapsed, err)
}
