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

package agent

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func openHandoffPostgresAdminDB(t *testing.T) *sql.DB {
	t.Helper()
	user := os.Getenv("POSTGRES_USER")
	password := os.Getenv("POSTGRES_PASSWORD")
	if user == "" {
		t.Skip("POSTGRES_USER is not set; administrative PostgreSQL rehearsal is unavailable")
	}
	port, err := strconv.Atoi(envOrCrawlApproval("DOCKER_POSTGRES_DB_PORT", "5432"))
	if err != nil {
		t.Fatalf("invalid DOCKER_POSTGRES_DB_PORT: %v", err)
	}
	dsn := &url.URL{
		Scheme: "postgres",
		Host:   fmt.Sprintf("%s:%d", envOrCrawlApproval("DOCKER_POSTGRES_DB_HOST", "127.0.0.1"), port),
		Path:   envOrCrawlApproval("DOCKER_POSTGRES_DB_NAME", "SitesIndex"),
		User:   url.UserPassword(user, password),
	}
	query := dsn.Query()
	query.Set("sslmode", envOrCrawlApproval("DOCKER_POSTGRES_SSL_MODE", "disable"))
	dsn.RawQuery = query.Encode()
	database, err := sql.Open(cdb.DBPostgresStr, dsn.String())
	if err != nil {
		t.Fatalf("open administrative PostgreSQL connection: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Ping(); err != nil {
		t.Skipf("administrative PostgreSQL connection is unavailable: %v", err)
	}
	return database
}

func handoffClaimRequestID(t *testing.T) string {
	t.Helper()
	sum := sha256.Sum256([]byte(fmt.Sprintf("wp05b3c3a-claim-%d", time.Now().UnixNano())))
	return hex.EncodeToString(sum[:])
}

func TestPostgresFullProposalApprovalSchedulerClaimAndObservation(t *testing.T) {
	db := openCrawlApprovalPostgresDB(t)
	adminDB := openHandoffPostgresAdminDB(t)
	ownerID, _ := setupCrawlApprovalPostgresUsers(t, db)
	sourceID := createCrawlApprovalPostgresSource(t, db, ownerID)
	ownerName := fmt.Sprintf("wp03b2b-owner-%d", ownerID)
	now := time.Now().UTC()

	identity := AgentIdentity{
		AgentID: "wp05b3c3-agent", Owner: ownerName, TrustLevel: "trusted",
		Capabilities: []string{"tool_execution", "db_write"},
	}
	runtime, err := BuildCrawlRequestToolRuntime(context.Background(), CrawlRequestToolRuntimeConfig{
		Enabled: true, DB: *db, Identity: identity,
		AllowedOperations: []string{crawlRequestToolName},
		AllowedSources:    []uint64{sourceID},
		MaxRequests:       2, BudgetWindow: time.Hour,
		Issuer: "crowler-api", ProposalTTL: 2 * time.Minute,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("build crawl runtime: %v", err)
	}
	tool := &crawlRequestTool{runtime: runtime}
	toolCtx := ContextWithToolRuntime(context.Background(), ToolRuntime{
		DB: *db, CallID: "wp05b3c3a-call-1", Crawl: runtime,
		Auth: ToolAuthContext{
			Identity: identity, Enforcement: true,
			Allowlist:         map[string]bool{crawlRequestToolName: true},
			AllowedSources:    map[uint64]bool{sourceID: true},
			AllowedOperations: map[string]bool{crawlRequestToolName: true},
			MutationAllowed:   true, RunID: "wp05b3c3a-run", TraceID: "wp05b3c3a-trace",
		},
	})
	proposalResult, err := tool.Execute(toolCtx, map[string]any{"source_id": float64(sourceID)})
	if err != nil {
		t.Fatalf("create crawl proposal: %v", err)
	}
	requestID, _ := proposalResult["request_id"].(string)
	proposal, found, err := cdb.GetCrawlProposalByRequest(context.Background(), db, requestID)
	if err != nil || !found || proposal.State != cdb.CrawlProposalStatePending {
		t.Fatalf("durable proposal = %+v found=%v err=%v", proposal, found, err)
	}

	store := runtime.ApprovalStore.(*DatabaseCrawlApprovalStore)
	approval, err := store.IssueCrawlApproval(context.Background(), CrawlApprovalIssuanceRequest{
		Proposal: CrawlRequestProposal{
			RequestID: proposal.RequestID, SourceID: proposal.SourceID,
			OperationType: proposal.OperationType, ExpiresAt: proposal.ExpiresAt,
		},
		Identity: identity, Principal: runtime.Policy.Principal,
		RunID: proposal.RunID, IssuerPrincipal: runtime.Policy.Principal,
		Approver: runtime.Policy.Principal, Issuer: "crowler-api",
		ApprovedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatalf("issue crawl approval: %v", err)
	}
	scheduled, err := runtime.SubmitApprovedProposal(context.Background(), requestID, approval.ApprovalID,
		runtime.Policy.Principal)
	if err != nil || scheduled.State != cdb.CrawlOperationAccepted {
		t.Fatalf("submit approved proposal = %+v err=%v", scheduled, err)
	}

	var acceptedAt time.Time
	var rawDetails []byte
	if err := (*db).QueryRow(`SELECT created_at, details FROM Status WHERE status_sha256=$1`,
		scheduled.OperationID).Scan(&acceptedAt, &rawDetails); err != nil {
		t.Fatalf("read scheduled operation: %v", err)
	}
	var details struct {
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(rawDetails, &details); err != nil {
		t.Fatalf("decode scheduled operation details: %v", err)
	}
	submitBy, err := time.Parse(time.RFC3339Nano, details.ExpiresAt)
	if err != nil {
		t.Fatalf("parse scheduled submit-by: %v", err)
	}

	if _, err := adminDB.Exec(`SELECT set_source_crawl_claim_mode('fenced')`); err != nil {
		t.Fatalf("enable fenced mode: %v", err)
	}
	t.Cleanup(func() {
		_, _ = adminDB.Exec(`DELETE FROM SourceCrawlClaims WHERE source_id=$1`, sourceID)
		_, _ = adminDB.Exec(`DELETE FROM SourceCrawlAttempts WHERE source_id=$1`, sourceID)
		_, _ = adminDB.Exec(`SELECT set_source_crawl_claim_mode('legacy')`)
	})

	claims, err := cdb.ClaimSourceCrawlAttempts(context.Background(), db, cdb.SourceCrawlClaimRequest{
		ClaimRequestID: handoffClaimRequestID(t),
		Limit:          5,
		Priority:       "high",
		EngineID:       "wp05b3c3a-engine",
		Lease:          10 * time.Minute,
	})
	if err != nil || len(claims) != 1 {
		t.Fatalf("claim scheduled source = %+v err=%v", claims, err)
	}
	claim := claims[0]
	if claim.Outcome != cdb.SourceCrawlClaimed || claim.SourceID != sourceID ||
		claim.OperationID != scheduled.OperationID || claim.PrincipalUserID != ownerID ||
		claim.ApprovalID != approval.ApprovalID || claim.RequestID != proposal.RequestID ||
		claim.RunID != proposal.RunID || claim.AgentID != proposal.AgentID ||
		claim.Owner != proposal.Owner {
		t.Fatalf("fenced claim does not match the real proposal/approval/operation: %+v", claim)
	}

	var storedOperation, storedRequest, storedRun, storedAgent, storedOwner sql.NullString
	var storedPrincipal sql.NullInt64
	var storedApproval, storedApprovalSHA sql.NullString
	if err := adminDB.QueryRow(`SELECT operation_id, request_id, run_id, agent_id, owner,
		principal_user_id, approval_id, approval_sha256
		FROM SourceCrawlAttempts WHERE source_id=$1 AND attempt_id=$2`,
		sourceID, claim.AttemptID).Scan(&storedOperation, &storedRequest, &storedRun,
		&storedAgent, &storedOwner, &storedPrincipal, &storedApproval, &storedApprovalSHA); err != nil {
		t.Fatalf("read persisted attempt binding: %v", err)
	}
	if storedOperation.String != scheduled.OperationID ||
		storedRequest.String != proposal.RequestID ||
		storedRun.String != proposal.RunID ||
		storedAgent.String != proposal.AgentID ||
		storedOwner.String != proposal.Owner ||
		uint64(storedPrincipal.Int64) != proposal.PrincipalUserID ||
		storedApproval.String != approval.ApprovalID ||
		storedApprovalSHA.String != approval.Fingerprint() {
		t.Fatalf("persisted attempt binding does not match authoritative rows: %+v", claim)
	}

	spec := testAsyncGoalSpec()
	spec.AllowedSources = []uint64{sourceID}
	spec.SpecHash = computeGoalSpecHash(spec)
	principal := goalAsyncPrincipal{
		Kind: goalAsyncPrincipalUser, UserID: ownerID,
		Username: ownerName, Issuer: "crowler",
	}
	createdAt := acceptedAt.Add(-time.Minute)
	checkpoint, err := newGoalAsyncCheckpoint(spec,
		AgentIdentity{AgentID: identity.AgentID, Owner: ownerName}, principal,
		proposal.RunID, "wp05b3c3a-trace", "", "wp05b3c3a-ledger",
		strings.Repeat("b", 64), createdAt, createdAt.Add(20*time.Minute),
		goalAsyncLimits{MaxSlices: 8, MaxRetries: 2, MaxScheduledWorkRequests: 4, LogicalLifetime: 20 * time.Minute})
	if err != nil {
		t.Fatalf("build goal checkpoint: %v", err)
	}
	checkpoint, _ = testAsyncGoalTransition(t, checkpoint, goalAsyncTransitionRequest{
		OperationID: "wp05b3c3a-start", ExpectedRevision: checkpoint.Revision,
		Kind: goalAsyncTransitionStart, Now: createdAt.Add(time.Second),
		ReasonCode: goalAsyncReasonStarted,
	})
	proposalWork := goalAsyncWorkRef{
		Kind: goalAsyncWorkCrawlProposal, State: goalAsyncWorkAwaitingApproval,
		RequestID: proposal.RequestID, ProposalSHA256: proposal.ProposalSHA256,
		SourceID: sourceID, ProposalExpiresAt: proposal.ExpiresAt,
	}
	checkpoint, _ = testAsyncGoalTransition(t, checkpoint, goalAsyncTransitionRequest{
		OperationID: "wp05b3c3a-await-approval", ExpectedRevision: checkpoint.Revision,
		Kind: goalAsyncTransitionAwaitApproval, Now: createdAt.Add(2 * time.Second),
		ReasonCode: goalAsyncReasonAwaitingApproval,
		Used:       &goalAsyncUsed{ScheduledWorkRequests: 1}, Work: &proposalWork,
	})
	operationWork := goalAsyncWorkRef{
		Kind: goalAsyncWorkCrawlOperation, State: goalAsyncWorkScheduled,
		RequestID: proposal.RequestID, ProposalSHA256: proposal.ProposalSHA256,
		ApprovalID: approval.ApprovalID, ApprovalSHA256: approval.Fingerprint(),
		OperationID: scheduled.OperationID, SourceID: sourceID,
		ProposalExpiresAt: proposal.ExpiresAt, ApprovalExpiresAt: approval.ExpiresAt,
		SubmitBy: submitBy, AcceptedAt: acceptedAt,
	}
	scheduling, err := newGoalAsyncSchedulingPrerequisite(checkpoint, goalAsyncSchedulingObservation{
		RunID: checkpoint.RunID, GoalID: checkpoint.GoalID, GoalSpecHash: checkpoint.GoalSpecHash,
		AgentID: checkpoint.AgentID, Owner: checkpoint.Owner, Principal: principal,
		Work: operationWork, ApprovalValid: true, ObservedAt: acceptedAt,
	})
	if err != nil {
		t.Fatalf("build scheduling prerequisite: %v", err)
	}
	checkpoint, _ = testAsyncGoalTransition(t, checkpoint, goalAsyncTransitionRequest{
		OperationID: "wp05b3c3a-await-external", ExpectedRevision: checkpoint.Revision,
		Kind: goalAsyncTransitionAwaitExternalWork, Now: acceptedAt.Add(time.Second),
		ReasonCode: goalAsyncReasonAwaitingExternalWork,
		Used:       &goalAsyncUsed{ScheduledWorkRequests: 1}, Scheduling: &scheduling,
	})

	storeObservation := &goalAsyncStore{db: db, postgres: true}
	observation, err := storeObservation.observeCrawlOperationAt(context.Background(), checkpoint,
		scheduled.OperationID, time.Now().UTC())
	if err != nil {
		t.Fatalf("observe real scheduled operation: %v", err)
	}
	if observation.Class != goalAsyncObservationPending ||
		observation.OperationState != string(cdb.CrawlOperationAccepted) ||
		observation.ProposalState != cdb.CrawlProposalStateSubmitted ||
		observation.ApprovalState != "active" ||
		observation.OperationID != scheduled.OperationID ||
		observation.SourceID != sourceID {
		t.Fatalf("real scheduler observation = %+v", observation)
	}

	var sourceStatus string
	if err := (*db).QueryRow(`SELECT status FROM Sources WHERE source_id=$1`, sourceID).Scan(&sourceStatus); err != nil {
		t.Fatalf("read claimed source status: %v", err)
	}
	if sourceStatus != "processing" {
		t.Fatalf("source status after fenced claim = %q, want processing", sourceStatus)
	}

	// The operation record remains accepted after the claim. A claim is not
	// completion evidence and the observer must not report completion.
	var operationStatus string
	if err := (*db).QueryRow(`SELECT status FROM Status WHERE status_sha256=$1`,
		scheduled.OperationID).Scan(&operationStatus); err != nil {
		t.Fatalf("read operation status: %v", err)
	}
	if operationStatus != string(cdb.CrawlOperationAccepted) {
		t.Fatalf("operation status after claim = %q, want accepted", operationStatus)
	}
}
