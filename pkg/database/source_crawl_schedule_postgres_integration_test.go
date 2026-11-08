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
		RequestID: "wp03b1-request", RunID: "wp03b1-run", PrincipalUserID: 7, AgentID: "wp03b1-agent", Owner: "wp03b1-owner",
		SourceID: sourceID, AllowedSources: []uint64{sourceID}, OperationType: CrawlOperationType,
		ParametersSHA256: strings.Repeat("a", 64),
		ApprovalID:       "wp03b1-approval", ApprovalSHA256: strings.Repeat("b", 64),
		ApprovedBy: "wp03b1-operator", ApprovedAt: now.Add(-time.Minute),
		ExpiresAt: now.Add(time.Hour), MaxRequests: 10, BudgetWindow: time.Hour,
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

func TestPostgresCrawlSchedulingQuotaIsOwnerIsolated(t *testing.T) {
	db, sqlDB := openPostgresIntegrationTestDB(t)
	sourceID := createCrawlSchedulingSource(t, db, fmt.Sprintf("wp03b1-owner-quota-%d", time.Now().UnixNano()))
	setCrawlSchedulingSourceState(t, db, sourceID, "completed", "", false)

	base := crawlSchedulingRequest(sourceID)
	base.MaxRequests = 1
	base.BudgetWindow = time.Hour
	ownerA, err := ScheduleSourceCrawlRequest(context.Background(), db, base)
	if err != nil || ownerA.State != CrawlOperationAccepted {
		t.Fatalf("owner A first request: %+v, %v", ownerA, err)
	}
	ownerB := base
	ownerB.RequestID, ownerB.Owner, ownerB.MaxRequests = "owner-b-request", "owner-b", 10
	ownerBResult, err := ScheduleSourceCrawlRequest(context.Background(), db, ownerB)
	if err != nil {
		t.Fatalf("owner B request: %v", err)
	}
	ownerC := base
	ownerC.RequestID, ownerC.Owner, ownerC.MaxRequests = "owner-c-request", "owner-c", 10
	ownerCResult, err := ScheduleSourceCrawlRequest(context.Background(), db, ownerC)
	if err != nil {
		t.Fatalf("owner C request: %v", err)
	}
	if _, err := sqlDB.Exec(`UPDATE Status SET created_at=NOW()-INTERVAL '1 minute' WHERE status_sha256=$1`, ownerA.OperationID); err != nil {
		t.Fatalf("age owner A operation: %v", err)
	}
	for _, operationID := range []string{ownerBResult.OperationID, ownerCResult.OperationID} {
		if _, err := sqlDB.Exec(`UPDATE Status SET created_at=NOW() WHERE status_sha256=$1`, operationID); err != nil {
			t.Fatalf("newer owner operation: %v", err)
		}
	}
	secondA := base
	secondA.RequestID = "owner-a-request-2"
	result, err := ScheduleSourceCrawlRequest(context.Background(), db, secondA)
	if err != nil {
		t.Fatalf("owner A second request: %v", err)
	}
	if result.State != CrawlOperationDenied || result.ReasonCode != CrawlReasonBudgetExhausted {
		t.Fatalf("owner A quota was bypassed by newer other-owner rows: %+v", result)
	}
	var rows int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM Status WHERE source_id=$1`, sourceID).Scan(&rows); err != nil {
		t.Fatalf("count owner operations: %v", err)
	}
	if rows != 3 {
		t.Fatalf("operation rows = %d, want 3", rows)
	}
}

func TestPostgresCrawlSchedulingConcurrentOwners(t *testing.T) {
	db, sqlDB := openPostgresIntegrationTestDB(t)
	sourceID := createCrawlSchedulingSource(t, db, fmt.Sprintf("wp03b1-concurrent-owners-%d", time.Now().UnixNano()))
	setCrawlSchedulingSourceState(t, db, sourceID, "completed", "", false)
	base := crawlSchedulingRequest(sourceID)
	owners := []string{"concurrent-a", "concurrent-b", "concurrent-c"}
	start := make(chan struct{})
	outcomes := make(chan CrawlScheduleResult, len(owners))
	errs := make(chan error, len(owners))
	var wg sync.WaitGroup
	for index, owner := range owners {
		request := base
		request.RequestID = fmt.Sprintf("concurrent-request-%d", index)
		request.Owner = owner
		request.MaxRequests = 1
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := ScheduleSourceCrawlRequest(context.Background(), db, request)
			if err != nil {
				errs <- err
				return
			}
			outcomes <- result
		}()
	}
	close(start)
	wg.Wait()
	close(outcomes)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent owner request: %v", err)
	}
	accepted := 0
	for result := range outcomes {
		if result.State == CrawlOperationAccepted {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("concurrent source accepted %d owners, want 1", accepted)
	}
	var rows int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM Status WHERE source_id=$1`, sourceID).Scan(&rows); err != nil {
		t.Fatalf("count concurrent owner operations: %v", err)
	}
	if rows != len(owners) {
		t.Fatalf("operation rows = %d, want %d", rows, len(owners))
	}
	var status string
	if err := sqlDB.QueryRow(`SELECT status FROM Sources WHERE source_id=$1`, sourceID).Scan(&status); err != nil {
		t.Fatalf("read concurrent owner source: %v", err)
	}
	if status != "pending" {
		t.Fatalf("concurrent owner source status = %q, want pending", status)
	}
}

func TestPostgresCrawlSchedulingConcurrentRetryWithQuota(t *testing.T) {
	db, sqlDB := openPostgresIntegrationTestDB(t)
	sourceID := createCrawlSchedulingSource(t, db, fmt.Sprintf("wp03b1-retry-quota-%d", time.Now().UnixNano()))
	setCrawlSchedulingSourceState(t, db, sourceID, "completed", "", false)
	request := crawlSchedulingRequest(sourceID)
	request.MaxRequests = 1
	request.BudgetWindow = time.Hour

	type outcome struct {
		result CrawlScheduleResult
		err    error
	}
	start := make(chan struct{})
	outcomes := make(chan outcome, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
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

	accepted, reused, denied := 0, 0, 0
	for item := range outcomes {
		if item.err != nil {
			t.Fatalf("concurrent retry: %v", item.err)
		}
		if item.result.Reused {
			reused++
		}
		switch item.result.State {
		case CrawlOperationAccepted:
			if !item.result.Reused {
				accepted++
			}
		case CrawlOperationDenied:
			if item.result.ReasonCode == CrawlReasonBudgetExhausted {
				denied++
			}
		}
	}
	if accepted != 1 || reused != 1 || denied != 0 {
		t.Fatalf("concurrent retry accepted=%d reused=%d denied=%d", accepted, reused, denied)
	}
	var rows int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM Status WHERE source_id=$1`, sourceID).Scan(&rows); err != nil {
		t.Fatalf("count retry operations: %v", err)
	}
	if rows != 1 {
		t.Fatalf("retry operation rows = %d, want 1", rows)
	}
}

func TestPostgresCrawlSchedulingMalformedQuotaRecordFailsClosed(t *testing.T) {
	db, sqlDB := openPostgresIntegrationTestDB(t)
	sourceID := createCrawlSchedulingSource(t, db, fmt.Sprintf("wp03b1-malformed-%d", time.Now().UnixNano()))
	setCrawlSchedulingSourceState(t, db, sourceID, "completed", "", false)
	if _, err := sqlDB.Exec(`INSERT INTO Status
		(status_sha256, agent_id, source_id, operation_type, status, details)
		VALUES ($1, $2, $3, $4, 'accepted', $5::jsonb)`,
		strings.Repeat("f", 64), "wp03b1-agent", sourceID, CrawlOperationType,
		`{"request_id":"malformed","owner":7,"operation_type":"crawl"}`); err != nil {
		t.Fatalf("insert malformed operation: %v", err)
	}
	result, err := ScheduleSourceCrawlRequest(context.Background(), db, crawlSchedulingRequest(sourceID))
	if err == nil {
		t.Fatal("malformed quota record did not fail closed")
	}
	if result.State != CrawlOperationFailed || result.ReasonCode != CrawlReasonDatabase {
		t.Fatalf("unexpected malformed result: %+v", result)
	}
	var status string
	if err := sqlDB.QueryRow(`SELECT status FROM Sources WHERE source_id=$1`, sourceID).Scan(&status); err != nil {
		t.Fatalf("read source after malformed quota: %v", err)
	}
	if status != "completed" {
		t.Fatalf("malformed quota changed source to %q", status)
	}
	var rows int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM Status WHERE source_id=$1`, sourceID).Scan(&rows); err != nil {
		t.Fatalf("count malformed operations: %v", err)
	}
	if rows != 1 {
		t.Fatalf("malformed quota created new operations: %d rows", rows)
	}
}

func TestPostgresSourceOwnerLookup(t *testing.T) {
	db, sqlDB := openPostgresIntegrationTestDB(t)
	sourceID := createCrawlSchedulingSource(t, db, fmt.Sprintf("wp03b2a-owner-%d", time.Now().UnixNano()))

	owner, err := GetSourceOwner(context.Background(), db, sourceID)
	if err != nil {
		t.Fatalf("default owner lookup: %v", err)
	}
	if !owner.Found || owner.UserID != 0 {
		t.Fatalf("unexpected default owner: %+v", owner)
	}

	if _, err := sqlDB.Exec(`UPDATE Sources SET usr_id=$1 WHERE source_id=$2`, 7, sourceID); err != nil {
		t.Fatalf("set source owner: %v", err)
	}
	owner, err = GetSourceOwner(context.Background(), db, sourceID)
	if err != nil {
		t.Fatalf("owned lookup: %v", err)
	}
	if !owner.Found || owner.SourceID != sourceID || owner.UserID != 7 {
		t.Fatalf("unexpected owned source: %+v", owner)
	}

	missing, err := GetSourceOwner(context.Background(), db, sourceID+1000000)
	if err != nil {
		t.Fatalf("missing owner lookup: %v", err)
	}
	if missing.Found {
		t.Fatalf("missing source reported as found: %+v", missing)
	}

	if _, err := sqlDB.Exec(`UPDATE Sources SET deleted_at=NOW() WHERE source_id=$1`, sourceID); err != nil {
		t.Fatalf("delete source: %v", err)
	}
	deleted, err := GetSourceOwner(context.Background(), db, sourceID)
	if err != nil {
		t.Fatalf("deleted owner lookup: %v", err)
	}
	if deleted.Found {
		t.Fatalf("deleted source reported as found: %+v", deleted)
	}
}
