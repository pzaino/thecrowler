//go:build integration

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
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func setSourceCrawlAttemptMode(t *testing.T, admin *sql.DB, mode string) {
	t.Helper()
	if _, err := admin.Exec(`SELECT set_source_crawl_claim_mode($1)`, mode); err != nil {
		t.Fatalf("set claim mode %q: %v", mode, err)
	}
}

func sourceCrawlAttemptRuntimeRole(t *testing.T) string {
	t.Helper()
	role := os.Getenv("CROWLER_DB_USER")
	if role == "" {
		role = os.Getenv("DOCKER_POSTGRES_DB_USER")
	}
	if role == "" {
		t.Fatal("CROWLER_DB_USER or DOCKER_POSTGRES_DB_USER is required")
	}
	return role
}

func resetSourceCrawlAttemptSource(t *testing.T, db *Handler, sourceID uint64, status string) {
	t.Helper()
	if _, err := (*db).Exec(`UPDATE Sources SET status=$1, engine='', last_updated_at=CURRENT_TIMESTAMP WHERE source_id=$2`, status, sourceID); err != nil {
		t.Fatalf("reset source %d: %v", sourceID, err)
	}
}

func expireSourceCrawlAttemptClaim(t *testing.T, db *Handler, sourceID uint64) {
	t.Helper()
	if _, err := (*db).Exec(`UPDATE SourceCrawlClaims
		SET claimed_at=CURRENT_TIMESTAMP - INTERVAL '2 seconds',
		    lease_expires_at=CURRENT_TIMESTAMP - INTERVAL '1 second'
		WHERE source_id=$1`, sourceID); err != nil {
		t.Fatalf("expire attempt claim for source %d: %v", sourceID, err)
	}
}

func cleanupSourceCrawlAttemptRows(t *testing.T, admin *sql.DB, sourceID uint64) {
	t.Helper()
	if _, err := admin.Exec(`SELECT set_source_crawl_claim_mode('legacy')`); err != nil {
		t.Errorf("reset claim mode: %v", err)
	}
	if _, err := admin.Exec(`UPDATE Sources SET status='new', engine='', last_updated_at=CURRENT_TIMESTAMP WHERE source_id=$1`, sourceID); err != nil {
		t.Errorf("reset source after attempt cleanup: %v", err)
	}
	for _, statement := range []string{
		`DELETE FROM SourceCrawlClaims WHERE source_id=$1`,
		`DELETE FROM SourceCrawlAttempts WHERE source_id=$1`,
		`DELETE FROM Status WHERE source_id=$1`,
		`DELETE FROM CrawlProposals WHERE source_id=$1`,
		`DELETE FROM CrawlApprovals WHERE source_id=$1`,
	} {
		if _, err := admin.Exec(statement, sourceID); err != nil {
			t.Errorf("cleanup attempt rows for %d: %v", sourceID, err)
		}
	}
}

var sourceCrawlAttemptTestNonce = fmt.Sprintf("%d", time.Now().UnixNano())

func sourceCrawlAttemptRequest(label string) SourceCrawlClaimRequest {
	return SourceCrawlClaimRequest{
		ClaimRequestID: sourceCrawlClaimTestID(label + "-" + sourceCrawlAttemptTestNonce),
		Limit:          5,
		EngineID:       "wp05b3c2-engine",
		Lease:          10 * time.Minute,
	}
}

func sourceCrawlAttemptRequestFor(label, priority string) SourceCrawlClaimRequest {
	req := sourceCrawlAttemptRequest(label)
	req.Priority = priority
	return req
}

func sourceCrawlAttemptInsertOperation(t *testing.T, db *Handler, sourceID, ownerID uint64, label, operationState string, approvedAt, acceptedAt time.Time) string {
	t.Helper()
	unique := fmt.Sprintf("%s-%d", label, time.Now().UnixNano())
	requestID := sourceCrawlClaimTestID("pg-request-" + unique)
	operationID := CrawlOperationKey("wp03b1-owner", "wp05b3c2-agent", sourceID, string(CrawlOperationType), requestID)
	proposalSHA := sourceCrawlClaimTestID("pg-proposal-" + unique)
	approvalID := sourceCrawlClaimTestID("pg-approval-" + unique)
	approvalSHA := sourceCrawlClaimTestID("pg-approval-sha-" + unique)
	proposalExpires := acceptedAt.Add(2 * time.Hour)
	approvalExpires := acceptedAt.Add(time.Hour)
	if _, err := (*db).Exec(`INSERT INTO CrawlProposals
		(request_id, run_id, tool_call_id, agent_id, owner, principal_user_id, source_id,
		 operation_type, proposal_sha256, trace_id, max_requests, budget_window_seconds,
		 policy_json, state, expires_at, submitted_at, operation_id, created_at, last_updated_at)
		VALUES ($1, 'wp05b3c2-run', $2, 'wp05b3c2-agent', 'wp03b1-owner', $3, $4,
		 'crawl', $5, 'wp05b3c2-trace', 1, 3600, '{}', 'submitted', $6, $7, $8, $7, $7)`,
		requestID, "wp05b3c2-tool-"+label, ownerID, sourceID, proposalSHA,
		proposalExpires, acceptedAt, operationID); err != nil {
		t.Fatalf("insert PostgreSQL claim proposal: %v", err)
	}
	if _, err := (*db).Exec(`INSERT INTO CrawlApprovals
		(approval_id, proposal_sha256, request_id, run_id, agent_id, owner,
		 principal_user_id, source_id, operation_type, issuer, issued_by_user_id,
		 issued_by, issued_by_issuer, approved_by_user_id, approved_by,
		 approved_by_issuer, approval_sha256, proposal_expires_at, approved_at,
		 expires_at, created_at, last_updated_at)
		VALUES ($1, $2, $3, 'wp05b3c2-run', 'wp05b3c2-agent', 'wp03b1-owner', $4, $5,
		 'crawl', 'wp05b3c2', 9, 'wp03b1-operator', 'crowler', 9,
		 'wp03b1-operator', 'crowler', $6, $7, $8, $9, $10, $10)`,
		approvalID, proposalSHA, requestID, ownerID, sourceID, approvalSHA,
		proposalExpires, approvedAt, approvalExpires, acceptedAt); err != nil {
		t.Fatalf("insert PostgreSQL claim approval: %v", err)
	}
	details, err := json.Marshal(crawlOperationDetails{
		RequestID: requestID, RunID: "wp05b3c2-run", PrincipalUserID: ownerID,
		AgentID: "wp05b3c2-agent", Owner: "wp03b1-owner", SourceID: sourceID,
		OperationType: "crawl", ParametersSHA256: proposalSHA,
		ApprovalID: approvalID, ApprovalSHA256: approvalSHA,
		ApprovedBy: "wp03b1-operator", ApprovedByUserID: 9,
		ApprovedAt: approvedAt.Format(time.RFC3339Nano),
		ExpiresAt:  approvalExpires.Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("marshal PostgreSQL claim details: %v", err)
	}
	if _, err := (*db).Exec(`INSERT INTO Status
		(status_sha256, agent_id, source_id, operation_type, status, details, created_at, last_updated_at)
		VALUES ($1, 'wp05b3c2-agent', $2, 'crawl', $3, $4, $5, $5)`,
		operationID, sourceID, operationState, string(details), acceptedAt); err != nil {
		t.Fatalf("insert PostgreSQL claim status: %v", err)
	}
	return operationID
}

func openSourceCrawlAttemptPostgresSecondHandler(t *testing.T, db *Handler) *Handler {
	t.Helper()
	pg, ok := (*db).(*PostgresHandler)
	if !ok || pg.connStr == "" {
		t.Skip("PostgreSQL handler does not expose a reusable connection string")
	}
	second, err := sql.Open(DBPostgresStr, pg.connStr)
	if err != nil {
		t.Fatalf("open second PostgreSQL pool: %v", err)
	}
	second.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = second.Close() })
	if err := second.Ping(); err != nil {
		t.Fatalf("ping second PostgreSQL pool: %v", err)
	}
	var handler Handler = &PostgresHandler{db: second, dbms: DBPostgresStr, connStr: pg.connStr}
	return &handler
}

func TestPostgresSourceCrawlAttemptRollback(t *testing.T) {
	db, sqlDB := openPostgresIntegrationTestDB(t)
	adminDB := openAsyncGoalPostgresAdminDB(t)
	ctx := context.Background()
	priority := fmt.Sprintf("wp05b3c2-rollback-%d", time.Now().UnixNano())
	sourceID := createCrawlSchedulingSource(t, db, priority)
	t.Cleanup(func() {
		_, _ = adminDB.Exec(`DROP TRIGGER IF EXISTS wp05b3c2_fail_claim ON SourceCrawlAttempts`)
		_, _ = adminDB.Exec(`DROP FUNCTION IF EXISTS wp05b3c2_fail_claim()`)
		cleanupSourceCrawlAttemptRows(t, adminDB, sourceID)
	})
	setSourceCrawlAttemptMode(t, adminDB, "fenced")
	if _, err := adminDB.Exec(`CREATE FUNCTION wp05b3c2_fail_claim() RETURNS trigger AS $$
		BEGIN RAISE EXCEPTION 'injected claim failure'; END; $$ LANGUAGE plpgsql`); err != nil {
		t.Fatalf("create failing claim function: %v", err)
	}
	if _, err := adminDB.Exec(`CREATE TRIGGER wp05b3c2_fail_claim BEFORE INSERT ON SourceCrawlAttempts
		FOR EACH ROW EXECUTE FUNCTION wp05b3c2_fail_claim()`); err != nil {
		t.Fatalf("create failing claim trigger: %v", err)
	}
	if _, err := ClaimSourceCrawlAttempts(ctx, db, sourceCrawlAttemptRequestFor("pg-rollback", priority)); err == nil {
		t.Fatal("PostgreSQL claim unexpectedly succeeded")
	}
	var status string
	if err := sqlDB.QueryRow(`SELECT status FROM Sources WHERE source_id=$1`, sourceID).Scan(&status); err != nil {
		t.Fatalf("read source after rollback: %v", err)
	}
	if status != "new" {
		t.Fatalf("PostgreSQL rollback left source status %q", status)
	}
	var claims, attempts int
	if err := sqlDB.QueryRow(`SELECT
		(SELECT COUNT(*) FROM SourceCrawlClaims WHERE source_id=$1),
		(SELECT COUNT(*) FROM SourceCrawlAttempts WHERE source_id=$1)`, sourceID).Scan(&claims, &attempts); err != nil {
		t.Fatalf("count rows after rollback: %v", err)
	}
	if claims != 0 || attempts != 0 {
		t.Fatalf("PostgreSQL rollback left claims=%d attempts=%d", claims, attempts)
	}
}

func TestPostgresSourceCrawlAttemptSchemaAndFencedCutover(t *testing.T) {
	db, sqlDB := openPostgresIntegrationTestDB(t)
	adminDB := openAsyncGoalPostgresAdminDB(t)
	ctx := context.Background()
	if err := CheckSourceCrawlAttemptSchema(ctx, db); err != nil {
		t.Fatalf("PostgreSQL attempt readiness: %v", err)
	}
	priority := fmt.Sprintf("wp05b3c2-%d", time.Now().UnixNano())
	sourceID := createCrawlSchedulingSource(t, db, priority)
	t.Cleanup(func() {
		cleanupSourceCrawlAttemptRows(t, adminDB, sourceID)
	})
	setSourceCrawlAttemptMode(t, adminDB, "legacy")

	rows, err := sqlDB.QueryContext(ctx,
		`SELECT source_id, source_uid, url, restricted, flags, config, last_updated_at, sub_priority
		 FROM update_sources($1,$2,$3,$4,$5,$6,$7)`,
		10, priority, "wp05b3c2-legacy-engine", "", "", "", "1 day")
	if err != nil {
		t.Fatalf("legacy update_sources: %v", err)
	}
	found := false
	for rows.Next() {
		var sourceIDValue uint64
		var sourceUID, url string
		var restricted, flags, subPriority int
		var config sql.NullString
		var lastUpdated sql.NullTime
		if err := rows.Scan(&sourceIDValue, &sourceUID, &url, &restricted, &flags, &config, &lastUpdated, &subPriority); err != nil {
			_ = rows.Close()
			t.Fatalf("scan legacy update_sources: %v", err)
		}
		if sourceIDValue == sourceID {
			found = true
		}
	}
	_ = rows.Close()
	if !found {
		t.Fatalf("legacy update_sources did not claim source %d", sourceID)
	}
	var status, engine string
	if err := sqlDB.QueryRow(`SELECT status, engine FROM Sources WHERE source_id=$1`, sourceID).Scan(&status, &engine); err != nil {
		t.Fatalf("read legacy claimed source: %v", err)
	}
	if status != "processing" || engine != "wp05b3c2-legacy-engine" {
		t.Fatalf("legacy claim state = %q/%q", status, engine)
	}

	resetSourceCrawlAttemptSource(t, db, sourceID, "new")
	setSourceCrawlAttemptMode(t, adminDB, "fenced")
	rows, err = sqlDB.QueryContext(ctx,
		`SELECT source_id FROM update_sources($1,$2,$3,$4,$5,$6,$7)`,
		10, priority, "wp05b3c2-old-worker", "", "", "", "1 day")
	if err != nil {
		t.Fatalf("fenced update_sources: %v", err)
	}
	count := 0
	for rows.Next() {
		count++
	}
	_ = rows.Close()
	if count != 0 {
		t.Fatalf("fenced update_sources returned %d rows", count)
	}

	claims, err := ClaimSourceCrawlAttempts(ctx, db, sourceCrawlAttemptRequestFor("pg-first", priority))
	if err != nil {
		t.Fatalf("first PostgreSQL claim: %v", err)
	}
	if len(claims) != 1 || claims[0].SourceID != sourceID || claims[0].Generation != 1 ||
		claims[0].AttemptID == "" || claims[0].Outcome != SourceCrawlUnattributed {
		t.Fatalf("first PostgreSQL claim = %+v", claims)
	}
	firstAttempt := claims[0].AttemptID
	replay, err := ClaimSourceCrawlAttempts(ctx, db, sourceCrawlAttemptRequestFor("pg-first", priority))
	if err != nil || len(replay) != 1 || !replay[0].Reused ||
		replay[0].AttemptID != firstAttempt || replay[0].Generation != 1 {
		t.Fatalf("PostgreSQL idempotent replay = %+v err=%v", replay, err)
	}
	resetSourceCrawlAttemptSource(t, db, sourceID, "pending")
	blocked, err := ClaimSourceCrawlAttempts(ctx, db, sourceCrawlAttemptRequestFor("pg-blocked", priority))
	if err != nil || len(blocked) != 0 {
		t.Fatalf("active PostgreSQL lease did not fence a second claim: %+v err=%v", blocked, err)
	}
	expireSourceCrawlAttemptClaim(t, db, sourceID)
	second, err := ClaimSourceCrawlAttempts(ctx, db, sourceCrawlAttemptRequestFor("pg-second", priority))
	if err != nil || len(second) != 1 || second[0].Generation != 2 ||
		second[0].AttemptID == firstAttempt {
		t.Fatalf("second PostgreSQL claim = %+v err=%v", second, err)
	}
	var oldState string
	var supersededBy int64
	if err := sqlDB.QueryRow(`SELECT state, superseded_by_generation FROM SourceCrawlAttempts WHERE attempt_id=$1`, firstAttempt).Scan(&oldState, &supersededBy); err != nil {
		t.Fatalf("read superseded PostgreSQL attempt: %v", err)
	}
	if oldState != "superseded" || supersededBy != 2 {
		t.Fatalf("old PostgreSQL attempt = %q/%d", oldState, supersededBy)
	}
}

func TestPostgresSourceCrawlAttemptBindingAndRejections(t *testing.T) {
	db, sqlDB := openPostgresIntegrationTestDB(t)
	adminDB := openAsyncGoalPostgresAdminDB(t)
	ctx := context.Background()
	priority := fmt.Sprintf("wp05b3c2-bind-%d", time.Now().UnixNano())
	sourceID := createCrawlSchedulingSource(t, db, priority)
	t.Cleanup(func() {
		cleanupSourceCrawlAttemptRows(t, adminDB, sourceID)
	})
	setSourceCrawlAttemptMode(t, adminDB, "fenced")
	now := time.Now().UTC().Truncate(time.Microsecond)
	operationID := sourceCrawlAttemptInsertOperation(t, db, sourceID, 7, "bound", "accepted", now.Add(-time.Minute), now)

	claims, err := ClaimSourceCrawlAttempts(ctx, db, sourceCrawlAttemptRequestFor("pg-bound", priority))
	if err != nil || len(claims) != 1 {
		t.Fatalf("bound PostgreSQL claim = %+v err=%v", claims, err)
	}
	if claims[0].Outcome != SourceCrawlClaimed || claims[0].OperationID != operationID ||
		claims[0].PrincipalUserID != 7 || claims[0].ApprovalID == "" {
		t.Fatalf("bound PostgreSQL claim details = %+v", claims[0])
	}
	var boundOperation string
	if err := sqlDB.QueryRow(`SELECT operation_id FROM SourceCrawlAttempts WHERE attempt_id=$1`, claims[0].AttemptID).Scan(&boundOperation); err != nil {
		t.Fatalf("read PostgreSQL binding: %v", err)
	}
	if boundOperation != operationID {
		t.Fatalf("stored PostgreSQL binding = %q, want %q", boundOperation, operationID)
	}

	// A later attempt may rebind the same operation only after the previous
	// attempt is superseded; the partial unique index must permit that.
	resetSourceCrawlAttemptSource(t, db, sourceID, "pending")
	expireSourceCrawlAttemptClaim(t, db, sourceID)
	rebound, err := ClaimSourceCrawlAttempts(ctx, db, sourceCrawlAttemptRequestFor("pg-rebound", priority))
	if err != nil || len(rebound) != 1 || rebound[0].Outcome != SourceCrawlClaimed ||
		rebound[0].Generation != 2 || rebound[0].OperationID != operationID {
		t.Fatalf("rebound PostgreSQL claim = %+v err=%v", rebound, err)
	}
	var oldState string
	if err := sqlDB.QueryRow(`SELECT state FROM SourceCrawlAttempts WHERE attempt_id=$1`, claims[0].AttemptID).Scan(&oldState); err != nil {
		t.Fatalf("read rebound previous attempt: %v", err)
	}
	if oldState != "superseded" {
		t.Fatalf("previous bound attempt state = %q, want superseded", oldState)
	}

	// Transferring ownership must make the same candidate not attributable.
	if _, err := (*db).Exec(`UPDATE Sources SET usr_id=8 WHERE source_id=$1`, sourceID); err != nil {
		t.Fatalf("transfer PostgreSQL source: %v", err)
	}
	if _, err := (*db).Exec(`UPDATE SourceOwnership SET user_id=8 WHERE source_id=$1`, sourceID); err != nil {
		t.Fatalf("transfer PostgreSQL provenance: %v", err)
	}
	resetSourceCrawlAttemptSource(t, db, sourceID, "new")
	expireSourceCrawlAttemptClaim(t, db, sourceID)
	transferred, err := ClaimSourceCrawlAttempts(ctx, db, sourceCrawlAttemptRequestFor("pg-transferred", priority))
	if err != nil || len(transferred) != 1 || transferred[0].Outcome != SourceCrawlNotAttributable ||
		transferred[0].OperationID != "" {
		t.Fatalf("transferred PostgreSQL claim = %+v err=%v", transferred, err)
	}

	// A second accepted operation for the same source is ambiguous.
	if _, err := (*db).Exec(`UPDATE Sources SET usr_id=7 WHERE source_id=$1`, sourceID); err != nil {
		t.Fatalf("restore PostgreSQL source owner: %v", err)
	}
	if _, err := (*db).Exec(`UPDATE SourceOwnership SET user_id=7 WHERE source_id=$1`, sourceID); err != nil {
		t.Fatalf("restore PostgreSQL provenance: %v", err)
	}
	sourceCrawlAttemptInsertOperation(t, db, sourceID, 7, "second", "accepted", now.Add(-time.Minute), now)
	resetSourceCrawlAttemptSource(t, db, sourceID, "new")
	expireSourceCrawlAttemptClaim(t, db, sourceID)
	ambiguous, err := ClaimSourceCrawlAttempts(ctx, db, sourceCrawlAttemptRequestFor("pg-ambiguous", priority))
	if err != nil || len(ambiguous) != 1 || ambiguous[0].Outcome != SourceCrawlAmbiguous ||
		ambiguous[0].OperationID != "" {
		t.Fatalf("ambiguous PostgreSQL claim = %+v err=%v", ambiguous, err)
	}
}

func TestPostgresSourceCrawlAttemptConcurrentClaimers(t *testing.T) {
	db, sqlDB := openPostgresIntegrationTestDB(t)
	adminDB := openAsyncGoalPostgresAdminDB(t)
	ctx := context.Background()
	priority := fmt.Sprintf("wp05b3c2-concurrent-%d", time.Now().UnixNano())
	sourceID := createCrawlSchedulingSource(t, db, priority)
	t.Cleanup(func() {
		cleanupSourceCrawlAttemptRows(t, adminDB, sourceID)
	})
	setSourceCrawlAttemptMode(t, adminDB, "fenced")
	resetSourceCrawlAttemptSource(t, db, sourceID, "new")

	secondHandler := openSourceCrawlAttemptPostgresSecondHandler(t, db)
	type outcome struct {
		claims []SourceCrawlClaim
		err    error
	}
	const workers = 2
	start := make(chan struct{})
	results := make(chan outcome, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		label := fmt.Sprintf("pg-concurrent-%d", i)
		wg.Add(1)
		handler := db
		if i == 1 {
			handler = secondHandler
		}
		go func(label string, handler *Handler) {
			defer wg.Done()
			<-start
			claims, err := ClaimSourceCrawlAttempts(ctx, handler, sourceCrawlAttemptRequest(label))
			results <- outcome{claims: claims, err: err}
		}(label, handler)
	}
	close(start)
	wg.Wait()
	close(results)

	total := 0
	attempts := map[string]bool{}
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent PostgreSQL claim error: %v", result.err)
		}
		for _, claim := range result.claims {
			if claim.SourceID == sourceID {
				total++
				attempts[claim.AttemptID] = true
			}
		}
	}
	if total != 1 || len(attempts) != 1 {
		t.Fatalf("concurrent PostgreSQL claims for source %d = %d distinct=%d", sourceID, total, len(attempts))
	}
	var stored int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM SourceCrawlAttempts WHERE source_id=$1`, sourceID).Scan(&stored); err != nil {
		t.Fatalf("count concurrent PostgreSQL attempts: %v", err)
	}
	if stored != 1 {
		t.Fatalf("concurrent PostgreSQL attempts stored = %d, want 1", stored)
	}
}

func TestPostgresSourceCrawlAttemptAddonRepeatable(t *testing.T) {
	db, _ := openPostgresIntegrationTestDB(t)
	adminDB := openAsyncGoalPostgresAdminDB(t)
	ctx := context.Background()
	priority := fmt.Sprintf("wp05b3c2-addon-%d", time.Now().UnixNano())
	sourceID := createCrawlSchedulingSource(t, db, priority)
	t.Cleanup(func() {
		cleanupSourceCrawlAttemptRows(t, adminDB, sourceID)
	})
	setSourceCrawlAttemptMode(t, adminDB, "fenced")
	claims, err := ClaimSourceCrawlAttempts(ctx, db, sourceCrawlAttemptRequestFor("pg-addon", priority))
	if err != nil || len(claims) != 1 {
		t.Fatalf("seed PostgreSQL attempt = %+v err=%v", claims, err)
	}
	addon, err := os.ReadFile("db_migrations/postgresql-source-crawl-attempt-v1.17.pgsql")
	if err != nil {
		t.Fatalf("read PostgreSQL attempt addon: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := applyAsyncGoalPostgresScript(adminDB, string(addon), sourceCrawlAttemptRuntimeRole(t)); err != nil {
			t.Fatalf("apply PostgreSQL attempt addon pass %d: %v", i+1, err)
		}
	}
	if err := CheckSourceCrawlAttemptSchema(ctx, db); err != nil {
		t.Fatalf("PostgreSQL readiness after addon: %v", err)
	}
	var mode string
	if err := adminDB.QueryRow(`SELECT mode FROM SourceCrawlClaimPolicy WHERE policy_id=1`).Scan(&mode); err != nil {
		t.Fatalf("read PostgreSQL policy mode: %v", err)
	}
	if mode != "fenced" {
		t.Fatalf("PostgreSQL addon reset policy mode to %q", mode)
	}
	var attempts, current int
	if err := adminDB.QueryRow(`SELECT
		(SELECT COUNT(*) FROM SourceCrawlAttempts WHERE claim_request_id=$1),
		(SELECT COUNT(*) FROM SourceCrawlClaims WHERE source_id=$2)`,
		sourceCrawlAttemptRequestFor("pg-addon", priority).ClaimRequestID, sourceID).Scan(&attempts, &current); err != nil {
		t.Fatalf("count PostgreSQL attempt rows: %v", err)
	}
	if attempts != 1 || current != 1 {
		t.Fatalf("PostgreSQL addon changed rows attempts=%d current=%d", attempts, current)
	}
	var version, generation string
	if err := adminDB.QueryRow(`SELECT version FROM DBSchemaVersion WHERE is_current`).Scan(&version); err != nil {
		t.Fatalf("read PostgreSQL version: %v", err)
	}
	if err := adminDB.QueryRow(`SELECT writer_generation FROM DatabaseWriterCompatibility WHERE lock_id=1`).Scan(&generation); err != nil {
		t.Fatalf("read PostgreSQL writer generation: %v", err)
	}
	if version != "1.17" || generation != WriterGeneration {
		t.Fatalf("PostgreSQL addon changed markers version=%q generation=%q", version, generation)
	}
}

func TestPostgresSourceCrawlAttemptMySQLAndLegacyStartupCompatibility(t *testing.T) {
	if SourceCrawlAttemptSupported(DBMySQLStr) {
		t.Fatal("MySQL must not advertise fenced attempt persistence")
	}
	if !strings.Contains(RequiredSchemaVersion, "1.17") {
		t.Fatalf("schema version = %q", RequiredSchemaVersion)
	}
}
