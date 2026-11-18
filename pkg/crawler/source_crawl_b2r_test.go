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

// WP-05B3C3B2R execution-safety tests: terminal decision, timeout quiescence,
// VDI quarantine, authoritative pre-start validation and fenced write gates.
// These use fake coordinators and a real SQLite schema; no WebDriver or
// external service is contacted.

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	vdi "github.com/pzaino/thecrowler/pkg/vdi"
)

func TestDecideCrawlTerminalOutcome(t *testing.T) {
	cases := []struct {
		name      string
		crawlErr  error
		cancelled bool
		want      cdb.SourceCrawlTerminalOutcome
	}{
		{"success", nil, false, cdb.SourceCrawlOutcomeCompleted},
		{"failure", errors.New("index failed"), false, cdb.SourceCrawlOutcomeFailed},
		{"aborted", errors.New("anything"), true, cdb.SourceCrawlOutcomeCancelled},
		{"context cancelled", context.Canceled, false, cdb.SourceCrawlOutcomeCancelled},
		{"context deadline", context.DeadlineExceeded, false, cdb.SourceCrawlOutcomeCancelled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := decideCrawlTerminalOutcome(tc.crawlErr, tc.cancelled); got != tc.want {
				t.Fatalf("outcome = %s want %s", got, tc.want)
			}
		})
	}
}

func TestNetworkOnlyCrawlResult(t *testing.T) {
	indexErr := errors.New("index unavailable")
	if got := networkOnlyCrawlResult(nil, false); got != nil {
		t.Fatalf("successful network-only crawl = %v", got)
	}
	if got := networkOnlyCrawlResult(indexErr, false); got == nil ||
		!strings.Contains(got.Error(), "indexing network information") {
		t.Fatalf("failed network-only crawl = %v", got)
	}
	if got := networkOnlyCrawlResult(context.Canceled, false); !errors.Is(got, errCrawlFenced) {
		t.Fatalf("cancelled network-only crawl = %v", got)
	}
	if got := networkOnlyCrawlResult(indexErr, true); !errors.Is(got, errCrawlFenced) {
		t.Fatalf("aborted network-only crawl = %v", got)
	}
}

func TestNetworkInfoCollectionFailed(t *testing.T) {
	if !networkInfoCollectionFailed(3) {
		t.Fatal("collector error state 3 was not treated as failed")
	}
	for _, status := range []int32{0, 1, 2} {
		if networkInfoCollectionFailed(status) {
			t.Fatalf("collector state %d was treated as failed", status)
		}
	}
}

func TestAwaitCrawlQuiescence(t *testing.T) {
	done := make(chan struct{})
	if awaitCrawlQuiescence(done, 20*time.Millisecond) {
		t.Fatal("open worker reported as quiesced")
	}
	close(done)
	if !awaitCrawlQuiescence(done, time.Second) {
		t.Fatal("closed worker not reported as quiesced")
	}
	if awaitCrawlQuiescence(nil, time.Second) {
		t.Fatal("nil done channel reported as quiesced")
	}
}

func TestQuarantineSession(t *testing.T) {
	ctx := &ProcessContext{source: &cdb.Source{ID: 42}}
	ctx.quarantineSession("timeout")
	if !ctx.sessionIsQuarantined() {
		t.Fatal("quarantine did not mark the session as quarantined")
	}
	// The finalizer, not the quarantining goroutine, must suppress the release.
	// VDIReturned stays false so the worker's own eventual return can recover
	// the slot safely once its work has actually finished.
	if ctx.VDIReturned {
		t.Fatal("quarantine marked the session as already returned")
	}
	// A second call is idempotent.
	ctx.quarantineSession("again")
	if !ctx.sessionIsQuarantined() {
		t.Fatal("quarantine was cleared")
	}
}

func TestCloseSessionDoesNotReturnQuarantinedVDI(t *testing.T) {
	ctx := &ProcessContext{
		source: &cdb.Source{ID: 42, URL: "https://quarantine.example/"},
		Status: &Status{},
		sourceCrawl: &fakeSourceCrawlCoordinator{
			sourceID: 42, attemptID: "attempt", generation: 1,
		},
	}
	ctx.sessionFinalized = false
	// Shared state the still-running worker may use.
	ctx.newLinks = []LinkItem{{Link: "https://still-running.example/"}}
	ctx.visitedLinks = map[string]bool{"https://still-running.example/": true}
	ctx.CollectedCookies = map[string]interface{}{"cookie": "still-running"}
	ctx.pStatus = 1
	ctx.quarantineSession("timeout")
	release := make(chan vdi.SeleniumInstance, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	ctx.WG = &wg
	var sel vdi.SeleniumInstance
	closeSession(ctx, &Pars{}, &sel, release, errCrawlTimeout)
	select {
	case <-release:
		t.Fatal("quarantined VDI was returned to the pool")
	default:
	}
	if ctx.VDIReturned {
		t.Fatal("quarantined session was marked as returned")
	}
	if len(ctx.newLinks) != 1 || ctx.visitedLinks == nil || ctx.CollectedCookies == nil {
		t.Fatal("closeSession cleared shared state still owned by the worker")
	}
	if ctx.pStatus == 10 {
		t.Fatal("closeSession finalized the pipeline state while quarantined")
	}
	// The worker's late updates must remain safe: this unconditional map write
	// is the shape that would panic on a cleared map.
	ctx.visitedLinks["https://late.example/"] = true
	waitCh := make(chan struct{})
	go func() {
		wg.Wait()
		close(waitCh)
	}()
	select {
	case <-waitCh:
	case <-time.After(time.Second):
		t.Fatal("closeSession did not complete the wait group")
	}
}

func TestValidateFencedClaimStartupBlocksStaleClaim(t *testing.T) {
	fake := &fakeSourceCrawlCoordinator{
		sourceID: 42, attemptID: "attempt", generation: 1,
		claimedAt: time.Now().Add(-time.Minute), lease: time.Now().Add(-time.Second),
		validateResult: cdb.SourceCrawlFenceStatus{
			Result: cdb.SourceCrawlFenceStale, Reason: "lease_expired",
		},
	}
	var db cdb.Handler
	if err := validateFencedClaimStartup(&db, fake, time.Second); err == nil {
		t.Fatal("stale claim was admitted")
	}
	if fake.validateCalls != 1 {
		t.Fatalf("validate calls = %d want 1", fake.validateCalls)
	}
	// No coordinator means legacy behavior and no validation.
	if err := validateFencedClaimStartup(&db, nil, time.Second); err != nil {
		t.Fatalf("legacy startup = %v", err)
	}
}

func TestValidateFencedClaimStartupAdmitsCurrentClaim(t *testing.T) {
	fake := &fakeSourceCrawlCoordinator{
		sourceID: 42, attemptID: "attempt", generation: 1,
		claimedAt: time.Now().Add(-time.Minute), lease: time.Now().Add(time.Hour),
		validateResult: cdb.SourceCrawlFenceStatus{Result: cdb.SourceCrawlFenceActive},
	}
	var db cdb.Handler
	if err := validateFencedClaimStartup(&db, fake, time.Second); err != nil {
		t.Fatalf("current claim rejected: %v", err)
	}
}

func TestFencedIndexingFencePropagatesFailure(t *testing.T) {
	fake := &fakeSourceCrawlCoordinator{sourceID: 42, attemptID: "attempt",
		validateTxErr: errors.New("stale")}
	ctx := &ProcessContext{sourceCrawl: fake}
	if err := ctx.validateIndexingFence(nil); err != nil {
		t.Fatalf("nil tx = %v", err)
	}
	ctx.db = new(cdb.Handler)
	tx := new(sql.Tx)
	if err := ctx.validateIndexingFence(tx); err == nil {
		t.Fatal("fenced-out indexing transaction was not rejected")
	}
	if fake.validateTxCall != 1 {
		t.Fatalf("ValidateTx calls = %d want 1", fake.validateTxCall)
	}
}

func TestFencedRefreshContentIsRejected(t *testing.T) {
	handler := openB2RSQLiteHandler(t)
	fake := &fakeSourceCrawlCoordinator{sourceID: 42, attemptID: "attempt"}
	ctx := &ProcessContext{
		source:      &cdb.Source{ID: 42, URL: "https://refresh.example/"},
		db:          &handler,
		sourceCrawl: fake,
		Status:      &Status{},
	}
	ctx.config.Crawler.RefreshContent = true
	ctx.config.Crawler.IndexingConcurrency = 1
	pageInfo := &PageInfo{}
	if _, err := indexPageContext(context.Background(), ctx, "https://refresh.example/",
		pageInfo); err == nil || !strings.Contains(err.Error(), "refresh_content") {
		t.Fatalf("fenced refresh_content error = %v", err)
	}
	var count int
	if err := handler.QueryRow(`SELECT COUNT(*) FROM WebObjects`).Scan(&count); err != nil {
		t.Fatalf("read WebObjects: %v", err)
	}
	if count != 0 {
		t.Fatalf("refresh_content wrote %d web objects", count)
	}
}

func TestShouldEmitCrawlCompletedEvent(t *testing.T) {
	cases := []struct {
		configured, fenced, applied, want bool
	}{
		{false, false, false, false},
		{true, false, false, true},
		{true, true, false, false},
		{true, true, true, true},
		{false, true, true, false},
	}
	for _, tc := range cases {
		if got := shouldEmitCrawlCompletedEvent(tc.configured, tc.fenced, tc.applied); got != tc.want {
			t.Fatalf("shouldEmit(%v,%v,%v) = %v want %v",
				tc.configured, tc.fenced, tc.applied, got, tc.want)
		}
	}
}

func openB2RSQLiteHandler(t *testing.T) cdb.Handler {
	t.Helper()
	path := t.TempDir() + "/b2r.db"
	c := cfg.Config{Database: cfg.Database{
		Type: "sqlite", DBName: path, TransactionTimeout: 30,
	}}
	handler, err := cdb.NewHandler(c)
	if err != nil {
		t.Fatalf("new SQLite handler: %v", err)
	}
	if err := handler.Connect(c); err != nil {
		t.Fatalf("connect SQLite handler: %v", err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	setup, err := os.ReadFile("../database/sqlite-setup.sqlite3")
	if err != nil {
		t.Fatalf("read SQLite setup: %v", err)
	}
	if _, err := handler.Exec(string(setup)); err != nil {
		t.Fatalf("apply SQLite setup: %v", err)
	}
	return handler
}
