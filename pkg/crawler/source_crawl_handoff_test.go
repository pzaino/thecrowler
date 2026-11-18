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

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

type fakeSourceCrawlCoordinator struct {
	mu            sync.Mutex
	sourceID      uint64
	attemptID     string
	generation    uint64
	claimedAt     time.Time
	lease         time.Time
	bound         bool
	renewResults  []cdb.SourceCrawlLeaseResult
	renewCalls    int
	renewCh       chan struct{}
	reportOutcome cdb.SourceCrawlTerminalOutcome
	reportAt      time.Time
	reportCalls   int
	reportResult  cdb.SourceCrawlTerminalResult
	statusOutcome cdb.SourceCrawlTerminalOutcome
	statusAt      time.Time
	statusCalls   int
	statusResult  cdb.SourceCrawlTerminalResult
}

func (f *fakeSourceCrawlCoordinator) SourceID() uint64     { return f.sourceID }
func (f *fakeSourceCrawlCoordinator) AttemptID() string    { return f.attemptID }
func (f *fakeSourceCrawlCoordinator) Generation() uint64   { return f.generation }
func (f *fakeSourceCrawlCoordinator) ClaimedAt() time.Time { return f.claimedAt }
func (f *fakeSourceCrawlCoordinator) Bound() bool          { return f.bound }
func (f *fakeSourceCrawlCoordinator) LeaseExpiresAt() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lease
}

func (f *fakeSourceCrawlCoordinator) RenewLease(_ context.Context, _ *cdb.Handler,
	_ time.Duration) (cdb.SourceCrawlLeaseResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renewCalls++
	if f.renewCh != nil {
		select {
		case f.renewCh <- struct{}{}:
		default:
		}
	}
	if len(f.renewResults) == 0 {
		return cdb.SourceCrawlLeaseResult{Result: cdb.SourceCrawlResultApplied, LeaseExpiresAt: f.lease}, nil
	}
	result := f.renewResults[0]
	if len(f.renewResults) > 1 {
		f.renewResults = f.renewResults[1:]
	}
	if result.Result == cdb.SourceCrawlResultApplied {
		f.lease = result.LeaseExpiresAt
	}
	return result, nil
}

func (f *fakeSourceCrawlCoordinator) ReportTerminal(_ context.Context, _ *cdb.Handler,
	outcome cdb.SourceCrawlTerminalOutcome, observedAt time.Time) (cdb.SourceCrawlTerminalResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reportCalls++
	f.reportOutcome = outcome
	f.reportAt = observedAt
	return f.reportResult, nil
}

func (f *fakeSourceCrawlCoordinator) UpdateUnboundSourceStatus(_ context.Context, _ *cdb.Handler,
	outcome cdb.SourceCrawlTerminalOutcome, observedAt time.Time) (cdb.SourceCrawlTerminalResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statusCalls++
	f.statusOutcome = outcome
	f.statusAt = observedAt
	if f.statusResult.Result == "" {
		return cdb.SourceCrawlTerminalResult{
			Result: cdb.SourceCrawlResultApplied, SourceStatusChanged: true,
		}, nil
	}
	return f.statusResult, nil
}

func TestStartSourceCrawlHeartbeatAbortsOnLostFence(t *testing.T) {
	oldInterval, oldExtension := sourceCrawlHeartbeatInterval, sourceCrawlHeartbeatExtension
	sourceCrawlHeartbeatInterval = time.Millisecond
	sourceCrawlHeartbeatExtension = time.Second
	t.Cleanup(func() {
		sourceCrawlHeartbeatInterval = oldInterval
		sourceCrawlHeartbeatExtension = oldExtension
	})
	fake := &fakeSourceCrawlCoordinator{
		sourceID: 42, attemptID: "attempt", generation: 1,
		claimedAt: time.Now().Add(-time.Minute), lease: time.Now().Add(time.Hour),
		renewResults: []cdb.SourceCrawlLeaseResult{{
			Result: cdb.SourceCrawlResultStaleFence, Reason: "lease_expired",
		}},
		renewCh: make(chan struct{}, 1),
	}
	parent, cancel := context.WithCancel(context.Background())
	ctx := &ProcessContext{crawlCtx: parent, crawlCancel: cancel, sourceCrawl: fake}
	var db cdb.Handler
	stop := startSourceCrawlHeartbeat(ctx, &db)
	defer stop()
	select {
	case <-ctx.crawlCtx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("heartbeat did not cancel the crawl after a lost fence")
	}
	reason := ctx.crawlAbortReasonString()
	if !strings.Contains(reason, "lease_lost") || !strings.Contains(reason, "stale_fence") {
		t.Fatalf("abort reason = %q", reason)
	}
}

func TestStartSourceCrawlHeartbeatStopsWithContext(t *testing.T) {
	oldInterval, oldExtension := sourceCrawlHeartbeatInterval, sourceCrawlHeartbeatExtension
	sourceCrawlHeartbeatInterval = time.Millisecond
	sourceCrawlHeartbeatExtension = time.Second
	t.Cleanup(func() {
		sourceCrawlHeartbeatInterval = oldInterval
		sourceCrawlHeartbeatExtension = oldExtension
	})
	fake := &fakeSourceCrawlCoordinator{
		sourceID: 42, attemptID: "attempt", generation: 1,
		claimedAt: time.Now().Add(-time.Minute), lease: time.Now().Add(time.Hour),
		renewResults: []cdb.SourceCrawlLeaseResult{{
			Result: cdb.SourceCrawlResultApplied, LeaseExpiresAt: time.Now().Add(time.Hour),
		}},
		renewCh: make(chan struct{}, 1),
	}
	parent, cancel := context.WithCancel(context.Background())
	ctx := &ProcessContext{crawlCtx: parent, crawlCancel: cancel, sourceCrawl: fake}
	var db cdb.Handler
	stop := startSourceCrawlHeartbeat(ctx, &db)
	defer stop()
	select {
	case <-fake.renewCh:
	case <-time.After(2 * time.Second):
		t.Fatal("heartbeat did not renew the lease")
	}
	cancel()
	select {
	case <-ctx.crawlCtx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("crawl context did not cancel")
	}
	if ctx.crawlAborted() != true {
		t.Fatal("canceled crawl context is not reported as aborted")
	}
}

func TestReportSourceCrawlTerminalOutcomes(t *testing.T) {
	cases := []struct {
		name     string
		crawlErr error
		aborted  bool
		want     cdb.SourceCrawlTerminalOutcome
	}{
		{"completed", nil, false, cdb.SourceCrawlOutcomeCompleted},
		{"failed", errors.New("crawl failed"), false, cdb.SourceCrawlOutcomeFailed},
		{"cancelled", errors.New("cancelled"), true, cdb.SourceCrawlOutcomeCancelled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeSourceCrawlCoordinator{
				sourceID: 42, attemptID: "attempt", generation: 1,
				reportResult: cdb.SourceCrawlTerminalResult{Result: cdb.SourceCrawlResultApplied},
			}
			crawlCtx, cancel := context.WithCancel(context.Background())
			if tc.aborted {
				cancel()
			}
			ctx := &ProcessContext{crawlCtx: crawlCtx, crawlCancel: cancel, sourceCrawl: fake}
			var db cdb.Handler
			ctx.db = &db
			result := ctx.reportSourceCrawlTerminal(tc.crawlErr)
			if result.Result != cdb.SourceCrawlResultApplied || fake.reportCalls != 1 {
				t.Fatalf("terminal result = %+v calls=%d", result, fake.reportCalls)
			}
			if fake.reportOutcome != tc.want {
				t.Fatalf("terminal outcome = %s want %s", fake.reportOutcome, tc.want)
			}
			if fake.reportAt.IsZero() {
				t.Fatal("terminal observation time is zero")
			}
			if fake.statusCalls != 1 || fake.statusOutcome != tc.want {
				t.Fatalf("unbound status outcome = %s calls=%d want %s",
					fake.statusOutcome, fake.statusCalls, tc.want)
			}
			if !result.SourceStatusChanged {
				t.Fatal("unbound source status change was not reported")
			}
		})
	}
}

func TestUpdateSourceStateForCrawlSkipsFenced(t *testing.T) {
	fake := &fakeSourceCrawlCoordinator{sourceID: 42, attemptID: "attempt"}
	args := &Pars{
		Src:         cdb.Source{URL: "https://example.invalid/"},
		SourceCrawl: fake,
	}
	// A nil DB would panic in the legacy path; the fenced branch must return
	// before touching it.
	UpdateSourceStateForCrawl(nil, args, errors.New("test"))
	ctx := &ProcessContext{sourceCrawl: fake}
	ctx.updateSourceState(errors.New("test"))
}

func TestCheckCrawlFence(t *testing.T) {
	ctx := &ProcessContext{crawlCtx: context.Background()}
	if err := ctx.checkCrawlFence(); err != nil {
		t.Fatalf("background context fence error = %v", err)
	}
	crawlCtx, cancel := context.WithCancel(context.Background())
	cancel()
	ctx.crawlCtx = crawlCtx
	ctx.crawlCancel = cancel
	if err := ctx.checkCrawlFence(); !errors.Is(err, errCrawlFenced) {
		t.Fatalf("canceled context fence error = %v", err)
	}
}
