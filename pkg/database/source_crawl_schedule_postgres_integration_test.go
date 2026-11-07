//go:build integration

package database

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
)

func createCrawlSchedulingSource(t *testing.T, db *Handler, priority string) uint64 {
	t.Helper()
	source := &Source{
		URL:  fmt.Sprintf("https://wp03b1-%d.example/", time.Now().UnixNano()),
		Name: "wp03b1-scheduling", Priority: priority, Restricted: 1,
	}
	id, err := CreateSourceContext(context.Background(), db, source, cfg.SourceConfig{})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	t.Cleanup(func() {
		_, _ = (*db).Exec(`DELETE FROM Status WHERE source_id=$1`, id)
		_, _ = (*db).Exec(`DELETE FROM Sources WHERE source_id=$1`, id)
	})
	return id
}

func setCrawlSchedulingSourceState(t *testing.T, db *Handler, sourceID uint64, status, engine string, disabled bool) {
	t.Helper()
	if _, err := (*db).Exec(`UPDATE Sources SET status=$1, engine=$2, disabled=$3, last_updated_at=NOW() WHERE source_id=$4`, status, engine, disabled, sourceID); err != nil {
		t.Fatalf("set source state: %v", err)
	}
}

func crawlSchedulingRequest(sourceID uint64) CrawlScheduleRequest {
	now := time.Now().UTC()
	return CrawlScheduleRequest{
		RequestID: "wp03b1-request", AgentID: "wp03b1-agent", Owner: "wp03b1-owner",
		SourceID: sourceID, AllowedSources: []uint64{sourceID}, OperationType: CrawlOperationType,
		ParametersSHA256: strings.Repeat("a", 64),
		ApprovalID:       "wp03b1-approval", ApprovalSHA256: strings.Repeat("b", 64),
		ApprovedBy: "wp03b1-operator", ApprovedAt: now.Add(-time.Minute),
		ExpiresAt: now.Add(time.Hour), Now: now,
	}
}

func TestPostgresCrawlSchedulingFoundation(t *testing.T) {
	db, sqlDB := openPostgresIntegrationTestDB(t)
	priority := fmt.Sprintf("wp03b1-%d", time.Now().UnixNano())

	sourceID := createCrawlSchedulingSource(t, db, priority)
	setCrawlSchedulingSourceState(t, db, sourceID, "completed", "", false)
	result, err := ScheduleSourceCrawlRequest(context.Background(), db, crawlSchedulingRequest(sourceID))
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if result.State != CrawlOperationAccepted || result.ReasonCode != CrawlReasonScheduled {
		t.Fatalf("unexpected accepted result: %+v", result)
	}
	var status, engine string
	if err := sqlDB.QueryRow(`SELECT status, engine FROM Sources WHERE source_id=$1`, sourceID).Scan(&status, &engine); err != nil {
		t.Fatalf("read scheduled source: %v", err)
	}
	if status != "pending" {
		t.Fatalf("source status = %q, want pending", status)
	}

	// The accepted source must be claimable by the existing engine function.
	rows, err := sqlDB.QueryContext(context.Background(),
		`SELECT source_id FROM update_sources($1,$2,$3,$4,$5,$6,$7)`,
		10, priority, "wp03b1-engine", "", "", "", "1 day")
	if err != nil {
		t.Fatalf("claim source: %v", err)
	}
	claimed := false
	for rows.Next() {
		var id uint64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			t.Fatalf("scan claim: %v", err)
		}
		if id == sourceID {
			claimed = true
		}
	}
	_ = rows.Close()
	if !claimed {
		t.Fatalf("accepted source %d was not claimed", sourceID)
	}
	if err := sqlDB.QueryRow(`SELECT status, engine FROM Sources WHERE source_id=$1`, sourceID).Scan(&status, &engine); err != nil {
		t.Fatalf("read claimed source: %v", err)
	}
	if status != "processing" || engine != "wp03b1-engine" {
		t.Fatalf("claimed source = (%q,%q), want processing/wp03b1-engine", status, engine)
	}

	// A processing source must not be reset by another request.
	processingRequest := crawlSchedulingRequest(sourceID)
	processingRequest.RequestID = "wp03b1-processing"
	processing, err := ScheduleSourceCrawlRequest(context.Background(), db, processingRequest)
	if err != nil {
		t.Fatalf("processing request: %v", err)
	}
	if processing.State != CrawlOperationProcessing || processing.SourceState != CrawlSourceProcessing {
		t.Fatalf("processing source was changed: %+v", processing)
	}
	if err := sqlDB.QueryRow(`SELECT status, engine FROM Sources WHERE source_id=$1`, sourceID).Scan(&status, &engine); err != nil {
		t.Fatalf("re-read processing source: %v", err)
	}
	if status != "processing" || engine != "wp03b1-engine" {
		t.Fatalf("processing source was reset: (%q,%q)", status, engine)
	}

	// A disabled source must not be scheduled.
	disabledID := createCrawlSchedulingSource(t, db, priority)
	setCrawlSchedulingSourceState(t, db, disabledID, "completed", "", true)
	disabled, err := ScheduleSourceCrawlRequest(context.Background(), db, crawlSchedulingRequest(disabledID))
	if err != nil {
		t.Fatalf("disabled request: %v", err)
	}
	if disabled.State != CrawlOperationDisabled || disabled.SourceState != CrawlSourceDisabled {
		t.Fatalf("disabled source was scheduled: %+v", disabled)
	}
	var disabledStatus string
	if err := sqlDB.QueryRow(`SELECT status FROM Sources WHERE source_id=$1`, disabledID).Scan(&disabledStatus); err != nil {
		t.Fatalf("read disabled source: %v", err)
	}
	if disabledStatus != "completed" {
		t.Fatalf("disabled source status changed to %q", disabledStatus)
	}

	// A missing source is distinct from a database failure.
	missing, err := ScheduleSourceCrawlRequest(context.Background(), db, crawlSchedulingRequest(sourceID+1000000))
	if err != nil {
		t.Fatalf("missing source request: %v", err)
	}
	if missing.State != CrawlOperationNotFound || missing.SourceState != CrawlSourceMissing {
		t.Fatalf("missing source was not distinguished: %+v", missing)
	}
}

func TestPostgresCrawlSchedulingConcurrentIdempotency(t *testing.T) {
	db, sqlDB := openPostgresIntegrationTestDB(t)
	priority := fmt.Sprintf("wp03b1-concurrent-%d", time.Now().UnixNano())
	sourceID := createCrawlSchedulingSource(t, db, priority)
	setCrawlSchedulingSourceState(t, db, sourceID, "completed", "", false)
	req := crawlSchedulingRequest(sourceID)

	type outcome struct {
		result CrawlScheduleResult
		err    error
	}
	const workers = 2
	start := make(chan struct{})
	outcomes := make(chan outcome, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := ScheduleSourceCrawlRequest(context.Background(), db, req)
			outcomes <- outcome{result: result, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(outcomes)

	accepted := 0
	reused := 0
	var operationID string
	for item := range outcomes {
		if item.err != nil {
			t.Fatalf("concurrent schedule: %v", item.err)
		}
		if item.result.State == CrawlOperationAccepted && !item.result.Reused {
			accepted++
			operationID = item.result.OperationID
		}
		if item.result.Reused {
			reused++
		}
		if operationID == "" {
			operationID = item.result.OperationID
		}
	}
	if accepted != 1 || reused != 1 {
		t.Fatalf("concurrent results: accepted=%d reused=%d", accepted, reused)
	}
	var rows int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM Status WHERE status_sha256=$1`, operationID).Scan(&rows); err != nil {
		t.Fatalf("count operation rows: %v", err)
	}
	if rows != 1 {
		t.Fatalf("operation rows = %d, want 1", rows)
	}
	var status string
	if err := sqlDB.QueryRow(`SELECT status FROM Sources WHERE source_id=$1`, sourceID).Scan(&status); err != nil {
		t.Fatalf("read concurrent source: %v", err)
	}
	if status != "pending" {
		t.Fatalf("concurrent source status = %q, want pending", status)
	}
}

func TestPostgresLegacyScheduleSourceCrawl(t *testing.T) {
	db, sqlDB := openPostgresIntegrationTestDB(t)
	sourceID := createCrawlSchedulingSource(t, db, fmt.Sprintf("wp03b1-legacy-%d", time.Now().UnixNano()))
	setCrawlSchedulingSourceState(t, db, sourceID, "completed", "", false)
	if err := ScheduleSourceCrawl(context.Background(), db, sourceID); err != nil {
		t.Fatalf("legacy schedule: %v", err)
	}
	var status string
	if err := sqlDB.QueryRow(`SELECT status FROM Sources WHERE source_id=$1`, sourceID).Scan(&status); err != nil {
		t.Fatalf("read legacy source: %v", err)
	}
	if status != "pending" {
		t.Fatalf("legacy source status = %q, want pending", status)
	}
}

func TestPostgresCrawlSchedulingBudgetConcurrency(t *testing.T) {
	db, sqlDB := openPostgresIntegrationTestDB(t)
	priority := fmt.Sprintf("wp03b1-budget-%d", time.Now().UnixNano())
	sourceID := createCrawlSchedulingSource(t, db, priority)
	setCrawlSchedulingSourceState(t, db, sourceID, "completed", "", false)
	base := crawlSchedulingRequest(sourceID)
	base.MaxRequests = 1
	base.BudgetWindow = time.Hour

	type outcome struct {
		result CrawlScheduleResult
		err    error
	}
	start := make(chan struct{})
	outcomes := make(chan outcome, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		request := base
		request.RequestID = fmt.Sprintf("wp03b1-budget-request-%d", i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := ScheduleSourceCrawlRequest(context.Background(), db, request)
			outcomes <- outcome{result: result, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(outcomes)
	accepted, denied := 0, 0
	for item := range outcomes {
		if item.err != nil {
			t.Fatalf("budget schedule: %v", item.err)
		}
		if item.result.State == CrawlOperationAccepted {
			accepted++
		}
		if item.result.State == CrawlOperationDenied && item.result.ReasonCode == CrawlReasonBudgetExhausted {
			denied++
		}
	}
	if accepted != 1 || denied != 1 {
		t.Fatalf("budget concurrency accepted=%d denied=%d", accepted, denied)
	}
	var rows int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM Status WHERE source_id=$1`, sourceID).Scan(&rows); err != nil {
		t.Fatalf("count budget operations: %v", err)
	}
	if rows != 1 {
		t.Fatalf("budget operation rows = %d, want 1", rows)
	}
}
