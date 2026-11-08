package agent

import (
	"context"
	"fmt"
	"testing"
	"time"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// crawlProposalProbe is a test-only, read-only tool. It turns a model proposal
// into bounded proposal data and deliberately never calls the scheduling API.
type crawlProposalProbe struct {
	proposal CrawlRequestProposal
	executed bool
}

func (p *crawlProposalProbe) Name() string { return "crawl_proposal_probe" }

func (p *crawlProposalProbe) Description() string {
	return "Return a bounded crawl-request proposal. This tool never schedules work."
}

func (p *crawlProposalProbe) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"source_id":  map[string]any{"type": "integer", "minimum": 1},
			"request_id": map[string]any{"type": "string", "minLength": 1, "maxLength": 128},
		},
		"required":             []any{"source_id", "request_id"},
		"additionalProperties": false,
	}
}

func (p *crawlProposalProbe) RequiredCapabilities() []string { return []string{"tool_execution"} }

func (p *crawlProposalProbe) Execute(ctx context.Context, args map[string]any) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sourceID, err := toSourceIDArg(args["source_id"])
	if err != nil {
		return nil, err
	}
	requestID, _ := args["request_id"].(string)
	if requestID == "" {
		return nil, fmt.Errorf("request_id is required")
	}
	p.proposal = CrawlRequestProposal{
		RequestID: requestID, SourceID: sourceID, OperationType: cdb.CrawlOperationType,
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	p.executed = true
	return map[string]any{
		"proposal": map[string]any{
			"request_id": requestID, "source_id": sourceID,
			"operation_type": cdb.CrawlOperationType,
			"expires_at":     p.proposal.ExpiresAt,
		},
	}, nil
}

// TestCrawlProposalLiveOllamaInertOptional proves a real model can produce a
// proposal but cannot turn it into a mutation without the trusted approval
// boundary. The probe tool is test-only and is not in the production registry.
func TestCrawlProposalLiveOllamaInertOptional(t *testing.T) {
	baseURL, model := ollamaSmokeConfig(t)
	probe := &crawlProposalProbe{}
	registry := NewAgentToolRegistry()
	if err := registry.Register(probe); err != nil {
		t.Fatalf("register proposal probe: %v", err)
	}
	provider := NewOllamaProvider([]string{model})
	config := loopTestConfig(provider, registry)
	config.ProviderName = "ollama"
	config.Model = model
	config.URL = baseURL
	config.Allowlist = []string{"crawl_proposal_probe"}
	config.AllowedSources = []uint64{42}
	config.Identity = AgentIdentity{
		AgentID: "proposal-agent", Owner: "proposal-owner", TrustLevel: "trusted",
		Capabilities: []string{"tool_execution"},
	}
	config.Prompt = `Call the crawl_proposal_probe tool exactly once with source_id 42 and request_id req-live-1. Do not call any other tool. After the tool result, reply with the single word done.`
	config.DB = &panicOnReadDBHandler{}

	result, err := RunToolLoop(config)
	if err != nil {
		t.Fatalf("live proposal loop: %v", err)
	}
	if !probe.executed {
		t.Fatalf("model did not call the inert proposal tool: %+v", result.ToolCalls)
	}
	if result.ToolCalls[0].Name != "crawl_proposal_probe" || result.ToolCalls[0].Status != "executed" {
		t.Fatalf("proposal tool was not executed: %+v", result.ToolCalls)
	}

	identity := AgentIdentity{
		AgentID: "proposal-agent", Owner: "proposal-owner", TrustLevel: "trusted",
		Capabilities: []string{"tool_execution", "db_write"},
	}
	policy := CrawlRequestPolicy{
		AllowedOperations: map[string]bool{crawlRequestToolName: true},
		AllowedSources:    map[uint64]bool{42: true},
		MaxRequests:       1,
		BudgetWindow:      time.Hour,
	}
	decision := AuthorizeCrawlRequest(context.Background(), identity, policy, probe.proposal, nil, "run-live", "trace-live")
	if decision.Allowed || decision.ReasonCode != CrawlReasonApprovalRequired {
		t.Fatalf("unapproved live proposal was not denied: %+v", decision)
	}
	if _, err := BuildCrawlScheduleRequest(identity, probe.proposal, CrawlRequestApproval{}, decision, "run-live"); err == nil {
		t.Fatal("unapproved live proposal built a scheduling request")
	}
	t.Logf("live inert proposal: decision=%s proposal=%s", decision.ReasonCode, decision.ProposalSHA256)
}

var _ AgentTool = (*crawlProposalProbe)(nil)
