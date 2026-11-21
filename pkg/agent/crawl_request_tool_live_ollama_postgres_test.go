//go:build integration

package agent

import (
	"context"
	"fmt"
	"testing"
	"time"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func TestCrawlRequestToolLiveOllamaPostgresApprovalWorkflowOptional(t *testing.T) {
	baseURL, model := ollamaSmokeConfig(t)
	db := openCrawlApprovalPostgresDB(t)
	ownerID, _ := setupCrawlApprovalPostgresUsers(t, db)
	sourceID := createCrawlApprovalPostgresSource(t, db, ownerID)
	ownerName := fmt.Sprintf("wp03b2b-owner-%d", ownerID)
	now := time.Now().UTC()
	identity := AgentIdentity{
		AgentID: "wp03b2c-pg-live-agent", Owner: ownerName, TrustLevel: "trusted",
		Capabilities: []string{"tool_execution", "db_write"},
	}
	runtime, err := BuildCrawlRequestToolRuntime(context.Background(), CrawlRequestToolRuntimeConfig{
		Enabled: true, DB: *db, Identity: identity,
		AllowedOperations: []string{crawlRequestToolName},
		AllowedSources:    []uint64{sourceID},
		MaxRequests:       2, BudgetWindow: time.Hour,
		Issuer: "crowler-api", ProposalTTL: 10 * time.Minute,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("build PostgreSQL live crawl runtime: %v", err)
	}
	registry := NewAgentToolRegistry()
	if err := RegisterCrawlRequestTool(registry, runtime); err != nil {
		t.Fatalf("register PostgreSQL live crawl tool: %v", err)
	}
	provider := NewOllamaProvider([]string{model})
	config := loopTestConfig(provider, registry)
	config.ProviderName = "ollama"
	config.Model = model
	config.URL = baseURL
	config.Allowlist = []string{crawlRequestToolName}
	config.AllowedSources = []uint64{sourceID}
	config.AllowedOperations = map[string]bool{crawlRequestToolName: true}
	config.MutationAllowed = true
	config.CrawlRuntime = runtime
	config.DB = *db
	config.Identity = identity
	config.RunID = "pg-live-crawl-run"
	config.TraceID = "pg-live-crawl-trace"
	config.Prompt = fmt.Sprintf("Call the request_source_crawl tool exactly once with source_id %d. Do not call any other tool. After the tool result, reply with: done. An operator will review the proposal separately.", sourceID)

	result, err := RunToolLoop(config)
	if err != nil {
		t.Fatalf("PostgreSQL live crawl loop: %v", err)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Name != crawlRequestToolName ||
		result.ToolCalls[0].Status != "executed" {
		t.Fatalf("model did not execute PostgreSQL crawl request tool: %+v", result.ToolCalls)
	}
	requestID, err := CrawlToolRequestID(result.RunID, result.ToolCalls[0].ID, sourceID)
	if err != nil {
		t.Fatalf("derive PostgreSQL live request ID: %v", err)
	}
	row, found, err := cdb.GetCrawlProposalByRequest(context.Background(), db, requestID)
	if err != nil || !found || row.State != cdb.CrawlProposalStatePending {
		t.Fatalf("PostgreSQL live proposal not persisted: %+v found=%v err=%v", row, found, err)
	}
	var status string
	var operations int
	if err := (*db).QueryRow(`SELECT status FROM Sources WHERE source_id=$1`, sourceID).Scan(&status); err != nil {
		t.Fatalf("read PostgreSQL live source before approval: %v", err)
	}
	if err := (*db).QueryRow(`SELECT COUNT(*) FROM Status WHERE source_id=$1`, sourceID).Scan(&operations); err != nil {
		t.Fatalf("count PostgreSQL live operations before approval: %v", err)
	}
	if status != "completed" || operations != 0 {
		t.Fatalf("unapproved PostgreSQL live proposal mutated source: status=%q operations=%d", status, operations)
	}
	store := runtime.ApprovalStore.(*DatabaseCrawlApprovalStore)
	approval, err := store.IssueCrawlApproval(context.Background(), CrawlApprovalIssuanceRequest{
		Proposal: CrawlRequestProposal{
			RequestID: row.RequestID, SourceID: row.SourceID,
			OperationType: row.OperationType, ExpiresAt: row.ExpiresAt,
		},
		Identity: identity, Principal: runtime.Policy.Principal,
		RunID: row.RunID, IssuerPrincipal: runtime.Policy.Principal,
		Approver: runtime.Policy.Principal, Issuer: "crowler-api",
		ApprovedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatalf("issue PostgreSQL live operator approval: %v", err)
	}
	submitted, err := runtime.SubmitApprovedProposal(context.Background(), row.RequestID, approval.ApprovalID, runtime.Policy.Principal)
	if err != nil {
		t.Fatalf("submit PostgreSQL live approved proposal: %v", err)
	}
	if submitted.State != cdb.CrawlOperationAccepted {
		t.Fatalf("PostgreSQL live submission state = %q: %+v", submitted.State, submitted)
	}
	if err := (*db).QueryRow(`SELECT status FROM Sources WHERE source_id=$1`, sourceID).Scan(&status); err != nil {
		t.Fatalf("read PostgreSQL live source after approval: %v", err)
	}
	if err := (*db).QueryRow(`SELECT COUNT(*) FROM Status WHERE source_id=$1`, sourceID).Scan(&operations); err != nil {
		t.Fatalf("count PostgreSQL live operations after approval: %v", err)
	}
	if status != "pending" || operations != 1 {
		t.Fatalf("PostgreSQL live approved submission status=%q operations=%d", status, operations)
	}
	stored, found, err := cdb.GetCrawlProposalByRequest(context.Background(), db, requestID)
	if err != nil || !found || stored.State != cdb.CrawlProposalStateSubmitted ||
		stored.OperationID.String != submitted.OperationID {
		t.Fatalf("PostgreSQL live proposal/operation mismatch: %+v found=%v err=%v", stored, found, err)
	}
	repeat, err := runtime.SubmitApprovedProposal(context.Background(), row.RequestID, approval.ApprovalID, runtime.Policy.Principal)
	if err != nil || !repeat.Reused || repeat.OperationID != submitted.OperationID {
		t.Fatalf("PostgreSQL live repeat submission: %+v err=%v", repeat, err)
	}
	if err := (*db).QueryRow(`SELECT COUNT(*) FROM Status WHERE source_id=$1`, sourceID).Scan(&operations); err != nil {
		t.Fatalf("count PostgreSQL live repeat operations: %v", err)
	}
	if operations != 1 {
		t.Fatalf("PostgreSQL live repeat created %d operations", operations)
	}
	t.Logf("PostgreSQL live crawl request chain: request=%s approval=%s operation=%s", row.RequestID, approval.ApprovalID, submitted.OperationID)
}
