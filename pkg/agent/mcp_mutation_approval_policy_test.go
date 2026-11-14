package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func approvalPolicyFixture(t *testing.T) (MCPMutationProposal, MCPMutationApprovalPolicy, string) {
	t.Helper()
	_, tool, store, identity, _, alias := newMCPMutationHarness(t)
	result, err := tool.Execute(mcpMutationRuntime(identity, alias, "approval-policy-run", "approval-policy-call"),
		map[string]any{"resource": "fixture-resource"})
	if err != nil {
		t.Fatalf("proposal: %v", err)
	}
	proposal, found, err := store.Get(context.Background(), result["proposal_id"].(string))
	if err != nil || !found {
		t.Fatalf("load proposal: found=%v err=%v", found, err)
	}
	policy := mcpMutationTestPolicy(alias)
	return proposal, MCPMutationApprovalPolicy{
		Policy: policy, Inventory: CurrentMCPOperatorConfig(), Agent: &identity,
	}, alias
}

func TestEvaluateMCPMutationApprovalAcceptsCurrentPolicy(t *testing.T) {
	proposal, current, _ := approvalPolicyFixture(t)
	if err := EvaluateMCPMutationApproval(proposal, current); err != nil {
		t.Fatalf("valid current policy rejected: %v", err)
	}
}

func TestEvaluateMCPMutationApprovalFailsClosed(t *testing.T) {
	proposal, current, _ := approvalPolicyFixture(t)
	cases := []struct {
		name    string
		mutate  func(*MCPMutationProposal, *MCPMutationApprovalPolicy)
		wantErr error
	}{
		{"disabled policy", func(_ *MCPMutationProposal, c *MCPMutationApprovalPolicy) {
			c.Policy.Enabled = false
		}, ErrMCPMutationPolicyUnavailable},
		{"policy id", func(_ *MCPMutationProposal, c *MCPMutationApprovalPolicy) {
			c.Policy.PolicyID = "different-policy"
		}, ErrMCPMutationPolicyMismatch},
		{"policy version", func(_ *MCPMutationProposal, c *MCPMutationApprovalPolicy) {
			c.Policy.Version = "2"
		}, ErrMCPMutationPolicyMismatch},
		{"policy hash", func(_ *MCPMutationProposal, c *MCPMutationApprovalPolicy) {
			c.Policy.Operations[0].AllowedResources = []string{"fixture-resource", "other-resource"}
		}, ErrMCPMutationPolicyMismatch},
		{"operation removed", func(_ *MCPMutationProposal, c *MCPMutationApprovalPolicy) {
			c.Policy.Operations[0].ToolName = "mutate_other"
			c.Policy.Operations[0].ToolAlias = MCPToolAlias("fixture", "mutate_other")
		}, ErrMCPMutationPolicyMismatch},
		{"agent removed", func(_ *MCPMutationProposal, c *MCPMutationApprovalPolicy) {
			c.Policy.Operations[0].AllowedAgents = []string{"other-agent"}
		}, ErrMCPMutationPolicyMismatch},
		{"missing agent identity", func(_ *MCPMutationProposal, c *MCPMutationApprovalPolicy) {
			c.Agent = nil
		}, ErrMCPMutationPolicyUnavailable},
		{"agent capability", func(_ *MCPMutationProposal, c *MCPMutationApprovalPolicy) {
			c.Agent.Capabilities = []string{"db_read"}
		}, ErrMCPMutationOperationUnauthorized},
		{"agent trust", func(_ *MCPMutationProposal, c *MCPMutationApprovalPolicy) {
			c.Agent.TrustLevel = "restricted"
		}, ErrMCPMutationOperationUnauthorized},
		{"agent contract", func(_ *MCPMutationProposal, c *MCPMutationApprovalPolicy) {
			c.Agent.Contract = &AgentContract{ForbiddenActions: []string{"tool:" + c.Policy.Operations[0].ToolAlias}}
		}, ErrMCPMutationOperationUnauthorized},
		{"inventory tool removed", func(_ *MCPMutationProposal, c *MCPMutationApprovalPolicy) {
			c.Inventory.Servers[0].ApprovedTools = nil
		}, ErrMCPMutationOperationUnauthorized},
		{"inventory transport", func(_ *MCPMutationProposal, c *MCPMutationApprovalPolicy) {
			c.Inventory.Servers[0].Transport = "streamable_http"
		}, ErrMCPMutationOperationUnauthorized},
		{"proposal transport", func(p *MCPMutationProposal, _ *MCPMutationApprovalPolicy) {
			p.Transport = "streamable_http"
		}, ErrMCPMutationOperationUnauthorized},
		{"proposal agent", func(p *MCPMutationProposal, _ *MCPMutationApprovalPolicy) {
			p.AgentID = "other-agent"
		}, ErrMCPMutationOperationUnauthorized},
		{"proposal resource", func(p *MCPMutationProposal, _ *MCPMutationApprovalPolicy) {
			p.Arguments = map[string]any{"resource": "other-resource"}
			canonical, err := json.Marshal(p.Arguments)
			if err != nil {
				t.Fatal(err)
			}
			p.CanonicalArguments = canonical
			p.ArgumentsSHA256 = sha256Hex(canonical)
			p.ProposalSHA256 = proposalFingerprint(*p)
		}, ErrMCPMutationOperationUnauthorized},
		{"proposal source scope", func(p *MCPMutationProposal, _ *MCPMutationApprovalPolicy) {
			p.SourceIDs = []uint64{42}
			p.ExternalResource = ""
			p.Arguments = map[string]any{"source_id": uint64(42)}
			canonical, err := json.Marshal(p.Arguments)
			if err != nil {
				t.Fatal(err)
			}
			p.CanonicalArguments = canonical
			p.ArgumentsSHA256 = sha256Hex(canonical)
			p.ProposalSHA256 = proposalFingerprint(*p)
		}, ErrMCPMutationOperationUnauthorized},
		{"corrupt canonical arguments", func(p *MCPMutationProposal, _ *MCPMutationApprovalPolicy) {
			p.CanonicalArguments = []byte(`{"resource":`)
		}, ErrMCPMutationOperationUnauthorized},
		{"proposal fingerprint", func(p *MCPMutationProposal, _ *MCPMutationApprovalPolicy) {
			p.ProposalSHA256 = strings.Repeat("0", 64)
		}, ErrMCPMutationOperationUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			proposalCopy := proposal
			currentCopy := current
			policyCopy := current.Policy
			policyCopy.Operations = append([]MCPMutationOperationPolicy(nil), current.Policy.Operations...)
			currentCopy.Policy = policyCopy
			inventoryCopy := current.Inventory
			inventoryCopy.Servers = append([]cfg.MCPServerConfig(nil), current.Inventory.Servers...)
			if len(inventoryCopy.Servers) > 0 {
				inventoryCopy.Servers[0].ApprovedTools = append([]cfg.MCPApprovedTool(nil), current.Inventory.Servers[0].ApprovedTools...)
			}
			currentCopy.Inventory = inventoryCopy
			if current.Agent != nil {
				agentCopy := *current.Agent
				currentCopy.Agent = &agentCopy
			}
			tc.mutate(&proposalCopy, &currentCopy)
			err := EvaluateMCPMutationApproval(proposalCopy, currentCopy)
			if err == nil || !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestApprovalStoreFailsClosedWhenPolicyChanges(t *testing.T) {
	ctx := context.Background()
	handler := newMCPMutationStoreTestHandler(t)
	proposal, current, _ := approvalPolicyFixture(t)
	store, err := NewDatabaseMCPMutationProposalStore(&handler)
	if err != nil {
		t.Fatalf("new durable store: %v", err)
	}
	admitted, err := store.Admit(ctx, proposal, 4, 8)
	if err != nil {
		t.Fatalf("admit proposal: %v", err)
	}
	approvalStore, err := NewDatabaseMCPMutationApprovalStore(&handler, time.Hour)
	if err != nil {
		t.Fatalf("new approval store: %v", err)
	}
	approvalStore.Policy = func() MCPMutationPolicy {
		changed := current.Policy
		changed.Operations = append([]MCPMutationOperationPolicy(nil), current.Policy.Operations...)
		changed.Operations[0].AllowedResources = []string{"fixture-resource", "changed-resource"}
		return changed
	}
	approvalStore.Inventory = func() cfg.MCPConfig { return current.Inventory }
	approvalStore.Agent = func(agentID string) (AgentIdentity, bool) {
		if current.Agent == nil || agentID != current.Agent.AgentID {
			return AgentIdentity{}, false
		}
		return *current.Agent, true
	}
	_, inserted, err := approvalStore.Issue(ctx, MCPMutationApprovalRequest{
		ProposalID: admitted.ProposalID, ProposalSHA256: admitted.ProposalSHA256,
		Approver:      MCPMutationPrincipal{UserID: 9, Username: "admin-1", Issuer: "test-issuer"},
		MaxExecutions: 1,
	})
	if err == nil || inserted || !errors.Is(err, ErrMCPMutationPolicyMismatch) {
		t.Fatalf("changed policy approval = inserted=%v err=%v", inserted, err)
	}
	row, found, err := cdb.GetMCPMutationProposal(ctx, &handler, admitted.ProposalID)
	if err != nil || !found || row.State != cdb.MCPMutationStateApprovalRequired {
		t.Fatalf("proposal after refused approval = %+v found=%v err=%v", row, found, err)
	}
	var approvals int
	if err := handler.(*mcpMutationSQLiteHandler).db.QueryRow(`SELECT COUNT(*) FROM MCPMutationApprovals`).Scan(&approvals); err != nil || approvals != 0 {
		t.Fatalf("approval rows after refusal = %d err=%v", approvals, err)
	}
}

func TestApprovalStoreConcurrentIssueIsIdempotent(t *testing.T) {
	ctx := context.Background()
	handler := newMCPMutationStoreTestHandler(t)
	proposal, current, _ := approvalPolicyFixture(t)
	proposalStore, err := NewDatabaseMCPMutationProposalStore(&handler)
	if err != nil {
		t.Fatalf("new durable proposal store: %v", err)
	}
	admitted, err := proposalStore.Admit(ctx, proposal, 4, 8)
	if err != nil {
		t.Fatalf("admit proposal: %v", err)
	}
	approvalStore, err := NewDatabaseMCPMutationApprovalStore(&handler, time.Hour)
	if err != nil {
		t.Fatalf("new approval store: %v", err)
	}
	approvalStore.Policy = func() MCPMutationPolicy { return current.Policy }
	approvalStore.Inventory = func() cfg.MCPConfig { return current.Inventory }
	approvalStore.Agent = func(agentID string) (AgentIdentity, bool) {
		if current.Agent == nil || agentID != current.Agent.AgentID {
			return AgentIdentity{}, false
		}
		return *current.Agent, true
	}
	approver := MCPMutationPrincipal{UserID: 9, Username: "admin-1", Issuer: "test-issuer"}
	type outcome struct {
		approval MCPMutationApproval
		inserted bool
		err      error
	}
	results := make(chan outcome, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			approval, inserted, err := approvalStore.Issue(ctx, MCPMutationApprovalRequest{
				ProposalID: admitted.ProposalID, ProposalSHA256: admitted.ProposalSHA256,
				Approver: approver, MaxExecutions: 1,
			})
			results <- outcome{approval: approval, inserted: inserted, err: err}
		}()
	}
	wg.Wait()
	close(results)
	insertedCount := 0
	var approvalID string
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent approval: %v", result.err)
		}
		if result.inserted {
			insertedCount++
		}
		if approvalID == "" {
			approvalID = result.approval.ApprovalID
		} else if approvalID != result.approval.ApprovalID {
			t.Fatalf("concurrent approvals returned different IDs: %s %s", approvalID, result.approval.ApprovalID)
		}
	}
	if insertedCount != 1 {
		t.Fatalf("inserted approvals = %d, want 1", insertedCount)
	}
	var rows int
	if err := handler.(*mcpMutationSQLiteHandler).db.QueryRow(
		`SELECT COUNT(*) FROM MCPMutationApprovals WHERE proposal_id=?`, admitted.ProposalID).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("approval rows after concurrency = %d err=%v", rows, err)
	}
}
