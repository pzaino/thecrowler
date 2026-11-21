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

package crawler

// Stage B2R crawler-side handoff, authoritative fence validation, heartbeat,
// cancellation and terminal reporting. A nil SourceCrawlCoordinator keeps the
// legacy path unchanged.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	cdb "github.com/pzaino/thecrowler/pkg/database"

	cmn "github.com/pzaino/thecrowler/pkg/common"
)

// SourceCrawlCoordinator is the narrow crawler-facing fenced attempt API.
// *cdb.SourceCrawlHandoff implements it in production; tests use a fake.
type SourceCrawlCoordinator interface {
	SourceID() uint64
	AttemptID() string
	Generation() uint64
	ClaimedAt() time.Time
	LeaseExpiresAt() time.Time
	Bound() bool
	RenewLease(ctx context.Context, db *cdb.Handler, extension time.Duration) (cdb.SourceCrawlLeaseResult, error)
	ReportTerminal(ctx context.Context, db *cdb.Handler, outcome cdb.SourceCrawlTerminalOutcome, observedAt time.Time) (cdb.SourceCrawlTerminalResult, error)
	ValidateCurrent(ctx context.Context, db *cdb.Handler) (cdb.SourceCrawlFenceStatus, error)
	ValidateTx(ctx context.Context, tx *sql.Tx, db *cdb.Handler) error
}

var (
	sourceCrawlHeartbeatInterval  = 5 * time.Minute
	sourceCrawlHeartbeatExtension = 5 * time.Minute
	sourceCrawlReportTimeout      = 10 * time.Second
	sourceCrawlValidateTimeout    = 10 * time.Second
	// crawlShutdownGrace bounds how long the timeout path waits for the crawl
	// worker and its indexing transactions to quiesce before the VDI session is
	// quarantined. A WebDriver call cannot be interrupted, so this is a bound,
	// not a guarantee.
	crawlShutdownGrace = 30 * time.Second
)

var (
	errCrawlFenced  = errors.New("source crawl fence lost")
	errCrawlTimeout = errors.New("timeout during crawling")
	// errIndexingFailure marks a page-indexing error. Fenced crawls treat it
	// as a failed crawl because a page that was not persisted cannot support a
	// truthful completed result. Legacy behaviour is unchanged.
	errIndexingFailure = errors.New("page indexing failed")
)

func (ctx *ProcessContext) crawlContext() context.Context {
	if ctx == nil || ctx.crawlCtx == nil {
		return context.Background()
	}
	return ctx.crawlCtx
}

func (ctx *ProcessContext) crawlAborted() bool {
	if ctx == nil || ctx.crawlCtx == nil {
		return false
	}
	return ctx.crawlCtx.Err() != nil
}

func (ctx *ProcessContext) abortCrawl(reason string) {
	if ctx == nil {
		return
	}
	ctx.crawlAbortOnce.Do(func() {
		if strings.TrimSpace(reason) == "" {
			reason = "crawl_cancelled"
		}
		ctx.crawlAbortReason.Store(reason)
		if ctx.crawlCancel != nil {
			ctx.crawlCancel()
		}
	})
}

func (ctx *ProcessContext) crawlAbortReasonString() string {
	if ctx == nil {
		return ""
	}
	value := ctx.crawlAbortReason.Load()
	if value == nil {
		return ""
	}
	reason, _ := value.(string)
	return reason
}

func (ctx *ProcessContext) checkCrawlFence() error {
	if ctx != nil && ctx.crawlAborted() {
		return errCrawlFenced
	}
	return nil
}

// quarantineSession marks the crawl as having failed to quiesce. The deferred
// finalizer then neither returns nor closes the session, and it does not clear
// per-crawl shared state that the still-running worker may use. It deliberately
// does not set VDIReturned: the worker's own eventual return is safe once its
// work has actually finished, and it recovers the pool slot. If the worker
// never finishes, the slot stays out of the pool by design.
func (ctx *ProcessContext) quarantineSession(reason string) {
	if ctx == nil {
		return
	}
	ctx.quarantineOnce.Do(func() {
		ctx.sessionQuarantined.Store(true)
		cmn.DebugMsg(cmn.DbgLvlError,
			"quarantining VDI session for source %d: %s", ctx.source.ID, reason)
	})
}

func (ctx *ProcessContext) sessionIsQuarantined() bool {
	return ctx != nil && ctx.sessionQuarantined.Load()
}

// validateFencedClaimStartup is the exported cmd-level pre-start check. It
// establishes the operator policy, claim identity, generation, lease and
// source state before any external work begins.
func validateFencedClaimStartup(db *cdb.Handler, coordinator SourceCrawlCoordinator,
	timeout time.Duration) error {
	if coordinator == nil {
		return nil
	}
	if timeout <= 0 {
		timeout = sourceCrawlValidateTimeout
	}
	checkCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	status, err := coordinator.ValidateCurrent(checkCtx, db)
	if err != nil && status.Result == cdb.SourceCrawlFenceUnavailable {
		return err
	}
	if status.Result != cdb.SourceCrawlFenceActive {
		if err != nil {
			return err
		}
		return &sourceCrawlFenceError{Status: status}
	}
	return nil
}

// ValidateFencedClaimStartup is the cmd-facing wrapper for the pre-start check.
func ValidateFencedClaimStartup(db *cdb.Handler, coordinator SourceCrawlCoordinator,
	timeout time.Duration) error {
	return validateFencedClaimStartup(db, coordinator, timeout)
}

type sourceCrawlFenceError struct {
	Status cdb.SourceCrawlFenceStatus
}

func (e *sourceCrawlFenceError) Error() string {
	if e == nil {
		return "source crawl fence is not current"
	}
	reason := e.Status.Reason
	if reason == "" {
		reason = string(e.Status.Result)
	}
	return "source crawl fence is not current: " + reason
}

// validateFencedClaim performs the authoritative in-process pre-work check.
// It is called before the protocol branch and again after a blocking VDI
// acquisition, immediately before work starts.
func (ctx *ProcessContext) validateFencedClaim() error {
	if ctx == nil || ctx.sourceCrawl == nil || ctx.db == nil {
		return nil
	}
	return validateFencedClaimStartup(ctx.db, ctx.sourceCrawl, sourceCrawlValidateTimeout)
}

// validateIndexingFence is the transaction-held check used by current-state
// database writes. It takes the source row lock and therefore serializes the
// write with terminal reporting, supersession and ownership transfer.
func (ctx *ProcessContext) validateIndexingFence(tx *sql.Tx) error {
	if ctx == nil || ctx.sourceCrawl == nil || tx == nil || ctx.db == nil {
		return nil
	}
	return ctx.sourceCrawl.ValidateTx(ctx.crawlContext(), tx, ctx.db)
}

// decideCrawlTerminalOutcome is the single terminal-outcome decision function.
// A cancellation (fence loss, timeout or explicit shutdown) is classified
// before an ordinary error so a timed-out or cancelled crawl cannot be reported
// as completed.
func decideCrawlTerminalOutcome(crawlErr error, cancelled bool) cdb.SourceCrawlTerminalOutcome {
	switch {
	case cancelled:
		return cdb.SourceCrawlOutcomeCancelled
	case errors.Is(crawlErr, context.Canceled), errors.Is(crawlErr, context.DeadlineExceeded):
		return cdb.SourceCrawlOutcomeCancelled
	case crawlErr != nil:
		return cdb.SourceCrawlOutcomeFailed
	default:
		return cdb.SourceCrawlOutcomeCompleted
	}
}

// startSourceCrawlHeartbeat renews the exact attempt until the crawl context
// ends or the database reports a lost/conflicting fence. A lost fence cancels
// the crawl context so the crawler stops new external work and indexing.
func startSourceCrawlHeartbeat(ctx *ProcessContext, db *cdb.Handler) func() {
	if ctx == nil || ctx.sourceCrawl == nil || db == nil {
		return func() {}
	}
	done := make(chan struct{})
	var once sync.Once
	stop := func() {
		once.Do(func() { close(done) })
	}
	go func() {
		ticker := time.NewTicker(sourceCrawlHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.crawlContext().Done():
				return
			case <-ticker.C:
				result, err := ctx.sourceCrawl.RenewLease(
					ctx.crawlContext(), db, sourceCrawlHeartbeatExtension)
				if err != nil {
					// A database outage is not itself proof that the fence was
					// lost. Keep trying, but never run past the last known lease.
					if !ctx.sourceCrawl.LeaseExpiresAt().IsZero() &&
						!time.Now().Before(ctx.sourceCrawl.LeaseExpiresAt()) {
						ctx.abortCrawl("lease_expired")
						return
					}
					cmn.DebugMsg(cmn.DbgLvlWarn,
						"renewing source crawl lease for source %d: %v",
						ctx.sourceCrawl.SourceID(), err)
					continue
				}
				switch result.Result {
				case cdb.SourceCrawlResultApplied, cdb.SourceCrawlResultIdempotentReplay:
					continue
				default:
					reason := string(result.Result)
					if result.Reason != "" {
						reason += ":" + result.Reason
					}
					cmn.DebugMsg(cmn.DbgLvlWarn,
						"source crawl lease lost for source %d attempt %s: %s",
						ctx.sourceCrawl.SourceID(), ctx.sourceCrawl.AttemptID(), reason)
					ctx.abortCrawl("lease_lost:" + reason)
					return
				}
			}
		}
	}()
	return stop
}

// UpdateSourceStateForCrawl keeps the legacy source-state behavior when no
// fenced handoff exists. A fenced crawl's source status is written only by the
// terminal reporter inside its own transaction.
func UpdateSourceStateForCrawl(ctx *ProcessContext, args *Pars, crawlErr error) {
	if args == nil || args.SourceCrawl != nil || (ctx != nil && ctx.sourceCrawl != nil) {
		return
	}
	UpdateSourceState(args.DB, args.Src.URL, crawlErr)
}

func (ctx *ProcessContext) updateSourceState(crawlErr error) {
	if ctx == nil || ctx.sourceCrawl != nil {
		return
	}
	if ctx.db == nil {
		return
	}
	UpdateSourceState(*ctx.db, ctx.source.URL, crawlErr)
}

func (ctx *ProcessContext) reportSourceCrawlTerminal(crawlErr error) cdb.SourceCrawlTerminalResult {
	if ctx == nil || ctx.sourceCrawl == nil || ctx.db == nil {
		return cdb.SourceCrawlTerminalResult{Result: cdb.SourceCrawlResultUnsupported}
	}
	outcome := decideCrawlTerminalOutcome(crawlErr, ctx.crawlAborted())
	reportCtx, cancel := context.WithTimeout(context.Background(), sourceCrawlReportTimeout)
	defer cancel()
	result, err := ctx.sourceCrawl.ReportTerminal(
		reportCtx, ctx.db, outcome, time.Now().UTC().Truncate(time.Microsecond))
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError,
			"reporting fenced terminal result for source %d attempt %s: %v",
			ctx.sourceCrawl.SourceID(), ctx.sourceCrawl.AttemptID(), err)
	}
	cmn.DebugMsg(cmn.DbgLvlInfo,
		"fenced terminal result for source %d attempt %s: %s (%s)",
		ctx.sourceCrawl.SourceID(), ctx.sourceCrawl.AttemptID(),
		result.Result, result.Reason)
	return result
}

// reportFencedCrawlFailure handles a failure before a ProcessContext exists.
// It reports the terminal result when a handoff is present and otherwise keeps
// the legacy URL-based source update.
func reportFencedCrawlFailure(args *Pars, crawlErr error) {
	if args == nil {
		return
	}
	if args.SourceCrawl == nil {
		UpdateSourceState(args.DB, args.Src.URL, crawlErr)
		return
	}
	ReportFencedCrawlOutcome(&args.DB, args.SourceCrawl, cdb.SourceCrawlOutcomeFailed,
		sourceCrawlReportTimeout)
}

// ReportFencedCrawlFailure is the cmd-level equivalent for failures that occur
// before CrawlWebsite starts, such as VDI acquisition failure.
func ReportFencedCrawlFailure(db *cdb.Handler, coordinator SourceCrawlCoordinator, timeout time.Duration) {
	ReportFencedCrawlOutcome(db, coordinator, cdb.SourceCrawlOutcomeFailed, timeout)
}

// ReportFencedCrawlOutcome reports a terminal result for a source that was
// claimed but never entered CrawlWebsite, such as a queued source dropped at
// batch shutdown or a VDI acquisition failure. Source status is written only
// by the terminal transaction for bound attempts; unbound attempts are
// suppressed because schema 1.17 has no durable claim-time owner.
func ReportFencedCrawlOutcome(db *cdb.Handler, coordinator SourceCrawlCoordinator,
	outcome cdb.SourceCrawlTerminalOutcome, timeout time.Duration) {
	if db == nil || coordinator == nil {
		return
	}
	if timeout <= 0 {
		timeout = sourceCrawlReportTimeout
	}
	reportCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	result, err := coordinator.ReportTerminal(
		reportCtx, db, outcome, time.Now().UTC().Truncate(time.Microsecond))
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError,
			"reporting fenced terminal outcome for source %d attempt %s: %v",
			coordinator.SourceID(), coordinator.AttemptID(), err)
	}
	cmn.DebugMsg(cmn.DbgLvlInfo,
		"fenced terminal outcome for source %d attempt %s: %s (%s)",
		coordinator.SourceID(), coordinator.AttemptID(), result.Result, result.Reason)
}

// networkInfoCollectionFailed reports whether the network-information
// collector recorded a terminal error. The collector state uses 3 for error,
// matching the Status flag convention.
// fencedIndexingFailure reports whether an indexing error must fail a fenced
// crawl. Legacy crawls keep the historical behaviour of logging non-critical
// worker errors and continuing.
func fencedIndexingFailure(coordinator SourceCrawlCoordinator, err error) bool {
	return coordinator != nil && errors.Is(err, errIndexingFailure)
}

func networkInfoCollectionFailed(status int32) bool {
	return status == 3
}

// awaitCrawlQuiescence waits for the crawl worker to finish within the bounded
// shutdown grace. It returns false when the worker is still running and the
// caller must quarantine the session instead of releasing it.
func awaitCrawlQuiescence(done <-chan struct{}, grace time.Duration) bool {
	if done == nil {
		return false
	}
	if grace <= 0 {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

// networkOnlyCrawlResult maps the network-information index outcome to the
// crawl error contract. A failed index is a failed crawl; an aborted or
// context-cancelled index is classified as cancellation. Non-fatal
// configuration handling around the collectors does not reach this function.
func networkOnlyCrawlResult(indexErr error, aborted bool) error {
	if indexErr == nil {
		return nil
	}
	if aborted || errors.Is(indexErr, context.Canceled) ||
		errors.Is(indexErr, context.DeadlineExceeded) {
		return fmt.Errorf("%w: network information indexing: %w", errCrawlFenced, indexErr)
	}
	return fmt.Errorf("indexing network information: %w", indexErr)
}

// shouldEmitCrawlCompletedEvent preserves the configured completion-event
// behavior. A fenced crawl may emit the event only for an applied, completed
// terminal result (never a failed, cancelled or replayed one); the event is an
// audit hint, not completion proof.
func shouldEmitCrawlCompletedEvent(configured, fenced, eligible bool) bool {
	if !configured {
		return false
	}
	if !fenced {
		return true
	}
	return eligible
}
