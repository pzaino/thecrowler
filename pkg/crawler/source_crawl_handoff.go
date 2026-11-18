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

// Stage B2 crawler-side handoff, heartbeat, cancellation and terminal reporting.
// A nil SourceCrawlCoordinator keeps the legacy path unchanged.

import (
	"context"
	"errors"
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
	UpdateUnboundSourceStatus(ctx context.Context, db *cdb.Handler, outcome cdb.SourceCrawlTerminalOutcome, observedAt time.Time) (cdb.SourceCrawlTerminalResult, error)
}

var (
	sourceCrawlHeartbeatInterval  = 5 * time.Minute
	sourceCrawlHeartbeatExtension = 5 * time.Minute
	sourceCrawlReportTimeout      = 10 * time.Second
)

var errCrawlFenced = errors.New("source crawl fence lost")

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
// terminal reporter.
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
	outcome := cdb.SourceCrawlOutcomeCompleted
	switch {
	case ctx.crawlAborted():
		outcome = cdb.SourceCrawlOutcomeCancelled
	case crawlErr != nil:
		outcome = cdb.SourceCrawlOutcomeFailed
	}
	reportCtx, cancel := context.WithTimeout(context.Background(), sourceCrawlReportTimeout)
	defer cancel()
	observedAt := time.Now().UTC().Truncate(time.Microsecond)
	result, err := ctx.sourceCrawl.ReportTerminal(
		reportCtx, ctx.db, outcome, observedAt)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError,
			"reporting fenced terminal result for source %d attempt %s: %v",
			ctx.sourceCrawl.SourceID(), ctx.sourceCrawl.AttemptID(), err)
	}
	if !ctx.sourceCrawl.Bound() &&
		(result.Result == cdb.SourceCrawlResultApplied ||
			result.Result == cdb.SourceCrawlResultIdempotentReplay) {
		statusCtx, statusCancel := context.WithTimeout(context.Background(), sourceCrawlReportTimeout)
		defer statusCancel()
		statusResult, statusErr := ctx.sourceCrawl.UpdateUnboundSourceStatus(
			statusCtx, ctx.db, outcome, observedAt)
		if statusErr != nil {
			cmn.DebugMsg(cmn.DbgLvlError,
				"updating fenced unbound source status for source %d attempt %s: %v",
				ctx.sourceCrawl.SourceID(), ctx.sourceCrawl.AttemptID(), statusErr)
		}
		if statusResult.Result == cdb.SourceCrawlResultApplied ||
			statusResult.Result == cdb.SourceCrawlResultIdempotentReplay {
			result.SourceStatusChanged = statusResult.SourceStatusChanged
		}
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
	reportCtx, cancel := context.WithTimeout(context.Background(), sourceCrawlReportTimeout)
	defer cancel()
	observedAt := time.Now().UTC().Truncate(time.Microsecond)
	result, err := args.SourceCrawl.ReportTerminal(
		reportCtx, &args.DB, cdb.SourceCrawlOutcomeFailed, observedAt)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError,
			"reporting fenced failure for source %d attempt %s: %v",
			args.SourceCrawl.SourceID(), args.SourceCrawl.AttemptID(), err)
	}
	if !args.SourceCrawl.Bound() &&
		(result.Result == cdb.SourceCrawlResultApplied ||
			result.Result == cdb.SourceCrawlResultIdempotentReplay) {
		statusCtx, statusCancel := context.WithTimeout(context.Background(), sourceCrawlReportTimeout)
		defer statusCancel()
		statusResult, statusErr := args.SourceCrawl.UpdateUnboundSourceStatus(
			statusCtx, &args.DB, cdb.SourceCrawlOutcomeFailed, observedAt)
		if statusErr != nil {
			cmn.DebugMsg(cmn.DbgLvlError,
				"updating fenced unbound source status for source %d attempt %s: %v",
				args.SourceCrawl.SourceID(), args.SourceCrawl.AttemptID(), statusErr)
		}
		if statusResult.Result == cdb.SourceCrawlResultApplied ||
			statusResult.Result == cdb.SourceCrawlResultIdempotentReplay {
			result.SourceStatusChanged = statusResult.SourceStatusChanged
		}
	}
	cmn.DebugMsg(cmn.DbgLvlInfo,
		"fenced failure result for source %d attempt %s: %s (%s)",
		args.SourceCrawl.SourceID(), args.SourceCrawl.AttemptID(),
		result.Result, result.Reason)
}

// ReportFencedCrawlFailure is the cmd-level equivalent for failures that occur
// before CrawlWebsite starts, such as VDI acquisition failure.
func ReportFencedCrawlFailure(db *cdb.Handler, coordinator SourceCrawlCoordinator, timeout time.Duration) {
	ReportFencedCrawlOutcome(db, coordinator, cdb.SourceCrawlOutcomeFailed, timeout)
}

// ReportFencedCrawlOutcome reports a terminal result for a source that was
// claimed but never entered CrawlWebsite, such as a queued source dropped at
// batch shutdown or a VDI acquisition failure.
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
	observedAt := time.Now().UTC().Truncate(time.Microsecond)
	result, err := coordinator.ReportTerminal(reportCtx, db, outcome, observedAt)
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError,
			"reporting fenced terminal outcome for source %d attempt %s: %v",
			coordinator.SourceID(), coordinator.AttemptID(), err)
	}
	if !coordinator.Bound() &&
		(result.Result == cdb.SourceCrawlResultApplied ||
			result.Result == cdb.SourceCrawlResultIdempotentReplay) {
		statusCtx, statusCancel := context.WithTimeout(context.Background(), sourceCrawlReportTimeout)
		defer statusCancel()
		statusResult, statusErr := coordinator.UpdateUnboundSourceStatus(
			statusCtx, db, outcome, observedAt)
		if statusErr != nil {
			cmn.DebugMsg(cmn.DbgLvlError,
				"updating fenced unbound source status for source %d attempt %s: %v",
				coordinator.SourceID(), coordinator.AttemptID(), statusErr)
		}
		if statusResult.Result == cdb.SourceCrawlResultApplied ||
			statusResult.Result == cdb.SourceCrawlResultIdempotentReplay {
			result.SourceStatusChanged = statusResult.SourceStatusChanged
		}
	}
	cmn.DebugMsg(cmn.DbgLvlInfo,
		"fenced terminal outcome for source %d attempt %s: %s (%s)",
		coordinator.SourceID(), coordinator.AttemptID(), result.Result, result.Reason)
}
