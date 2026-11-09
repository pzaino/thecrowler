package agent

import (
	"context"
	"testing"
	"time"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// TestCrawlRequestToolLiveOllamaApprovalWorkflowOptional exercises the full
// operator-controlled chain with a real local model: the model proposes one
// source, the proposal is persisted without scheduling, a test-controlled
// operator issues the production approval, and the trusted submission path
// schedules exactly one crawl.
func TestCrawlRequestToolLiveOllamaApprovalWorkflowOptional(t *testing.T) {
	baseURL, model := ollamaSmokeConfig(t)
	handler := newCrawlSchedulingTestDB(t)
	now := time.Now().UTC()
	runtime := newCrawlToolTestRuntime(t, handler, []uint64{42}, now)
	registry := NewAgentToolRegistry()
	if err := RegisterCrawlRequestTool(registry, runtime); err != nil {
		t.Fatalf("register crawl request tool: %v", err)
	}
	provider := NewOllamaProvider([]string{model})
	config := loopTestConfig(provider, registry)
	config.ProviderName = "ollama"
	config.Model = model
	config.URL = baseURL
	config.Allowlist = []string{crawlRequestToolName}
	config.AllowedSources = []uint64{42}
	config.AllowedOperations = map[string]bool{crawlRequestToolName: true}
	config.MutationAllowed = true
	config.CrawlRuntime = runtime
	config.DB = *handler
	config.Identity = runtime.Identity
	config.RunID = "run-live-crawl"
	config.TraceID = "trace-live-crawl"
	config.Prompt = `Call the request_source_crawl tool exactly once with source_id 42. Do not call any other tool. After the tool result, reply with: done. An operator will review the proposal separately.`

	result, err := RunToolLoop(config)
	if err != nil {
		t.Fatalf("live crawl request loop: %v", err)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Name != crawlRequestToolName ||
		result.ToolCalls[0].Status != "executed" {
		t.Fatalf("model did not execute the crawl request tool: %+v", result.ToolCalls)
	}
	requestID, err := CrawlToolRequestID(result.RunID, result.ToolCalls[0].ID, 42)
	if err != nil {
		t.Fatalf("derive live request ID: %v", err)
	}
	row, found, err := cdb.GetCrawlProposalByRequest(context.Background(), handler, requestID)
	if err != nil || !found || row.State != cdb.CrawlProposalStatePending {
		t.Fatalf("live proposal not persisted: %+v found=%v err=%v", row, found, err)
	}
	status, operations := crawlSchedulingTestSourceState(t, handler, 42)
	if status != "completed" || operations != 0 {
		t.Fatalf("unapproved live proposal mutated source: status=%q operations=%d", status, operations)
	}

	store := runtime.ApprovalStore.(*DatabaseCrawlApprovalStore)
	approval, err := store.IssueCrawlApproval(context.Background(), CrawlApprovalIssuanceRequest{
		Proposal: CrawlRequestProposal{
			RequestID: row.RequestID, SourceID: row.SourceID,
			OperationType: row.OperationType, ExpiresAt: row.ExpiresAt,
		},
		Identity: runtime.Identity, Principal: runtime.Policy.Principal,
		RunID: row.RunID, IssuerPrincipal: runtime.Policy.Principal,
		Approver: runtime.Policy.Principal, Issuer: "crowler-api",
		ApprovedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatalf("issue live operator approval: %v", err)
	}
	submitted, err := runtime.SubmitApprovedProposal(context.Background(), row.RequestID, approval.ApprovalID, runtime.Policy.Principal)
	if err != nil {
		t.Fatalf("submit live approved proposal: %v", err)
	}
	if submitted.State != cdb.CrawlOperationAccepted {
		t.Fatalf("live submission state = %q: %+v", submitted.State, submitted)
	}
	status, operations = crawlSchedulingTestSourceState(t, handler, 42)
	if status != "pending" || operations != 1 {
		t.Fatalf("live approved submission status=%q operations=%d", status, operations)
	}
	repeat, err := runtime.SubmitApprovedProposal(context.Background(), row.RequestID, approval.ApprovalID, runtime.Policy.Principal)
	if err != nil || !repeat.Reused {
		t.Fatalf("live repeat submission was not idempotent: %+v err=%v", repeat, err)
	}
	status, operations = crawlSchedulingTestSourceState(t, handler, 42)
	if status != "pending" || operations != 1 {
		t.Fatalf("live repeat created duplicate work: status=%q operations=%d", status, operations)
	}

	outOfScope := &crawlRequestTool{runtime: runtime}
	if _, err := outOfScope.Execute(crawlToolContext(t, handler, runtime, "call-live-out-of-scope"),
		map[string]any{"source_id": float64(43)}); err == nil {
		t.Fatalf("out-of-scope live call was accepted")
	}
	t.Logf("live crawl request chain: request=%s approval=%s operation=%s", row.RequestID, approval.ApprovalID, submitted.OperationID)
}
