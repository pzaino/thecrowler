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
		if t := c.Database.TransactionTimeout; t >= minTransactionTimeoutSeconds && t <= maxTransactionTimeoutSeconds {
			timeout = t
		}
	}
	return context.WithTimeout(parent, time.Duration(timeout)*time.Second)
}

// IsTransactionTimeout reports whether err was caused by the deadline of a
// transaction context expiring (see TransactionContext).
func IsTransactionTimeout(err error) bool {
	return errors.Is(err, context.DeadlineExceeded)
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
