package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pzaino/thecrowler/pkg/auth"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func sourceApprovalFixture(t *testing.T) (MCPMutationProposal, MCPMutationPolicy, AgentIdentity) {
	t.Helper()
	manager, policy, aliases, identity, cleanup := newMCPAuthorityManager(t)
	t.Cleanup(cleanup)
	store := NewInMemoryMCPMutationProposalStore()
	tool, err := NewMCPMutationProposalToolForOperation(manager, policy, store, aliases[1])
	if err != nil {
		t.Fatalf("source approval tool: %v", err)
	}
	result, err := tool.Execute(mcpMutationRuntime(identity, aliases[1], "approval-authority-run", "approval-authority-call"),
		map[string]any{"source_id": float64(42)})
	if err != nil {
		t.Fatalf("source approval proposal: %v", err)
	}
	proposal, found, err := store.Get(context.Background(), result["proposal_id"].(string))
	if err != nil || !found {
		t.Fatalf("load source approval proposal: found=%v err=%v", found, err)
	}
	return proposal, policy, identity
}

func TestMCPMutationApprovalAuthorityRejectsChangedAuthority(t *testing.T) {
	cases := []struct {
		name     string
		mutate   func(*mcpMutationSQLiteHandler)
		approver MCPMutationPrincipal
		wantErr  error
	}{
		{
			name: "approver disabled",
			mutate: func(h *mcpMutationSQLiteHandler) {
				if _, err := h.db.Exec(`UPDATE Users SET disabled=TRUE WHERE user_id=7`); err != nil {
					t.Fatalf("disable approver: %v", err)
				}
			},
			approver: MCPMutationPrincipal{UserID: 7, Username: "mcp-owner", Issuer: "test-issuer"},
			wantErr:  ErrMCPMutationApprovalForbidden,
		},
		{
			name: "approver deleted",
			mutate: func(h *mcpMutationSQLiteHandler) {
				if _, err := h.db.Exec(`UPDATE Users SET deleted_at=CURRENT_TIMESTAMP WHERE user_id=7`); err != nil {
					t.Fatalf("delete approver: %v", err)
				}
			},
			approver: MCPMutationPrincipal{UserID: 7, Username: "mcp-owner", Issuer: "test-issuer"},
			wantErr:  ErrMCPMutationApprovalForbidden,
		},
		{
			name: "proposal principal disabled",
			mutate: func(h *mcpMutationSQLiteHandler) {
				if _, err := h.db.Exec(`UPDATE Users SET disabled=TRUE WHERE user_id=7`); err != nil {
					t.Fatalf("disable principal: %v", err)
				}
			},
			approver: MCPMutationPrincipal{UserID: 9, Username: "admin-1", Issuer: "test-issuer"},
			wantErr:  ErrMCPMutationApprovalForbidden,
		},
		{
			name: "source ownership transferred",
			mutate: func(h *mcpMutationSQLiteHandler) {
				if _, err := h.db.Exec(`UPDATE Sources SET usr_id=8 WHERE source_id=42`); err != nil {
					t.Fatalf("transfer source: %v", err)
				}
				if _, err := h.db.Exec(`UPDATE SourceOwnership SET user_id=8 WHERE source_id=42`); err != nil {
					t.Fatalf("transfer provenance: %v", err)
				}
			},
			approver: MCPMutationPrincipal{UserID: 7, Username: "mcp-owner", Issuer: "test-issuer"},
			wantErr:  ErrMCPMutationApprovalForbidden,
		},
		{
			name: "source ownership revoked",
			mutate: func(h *mcpMutationSQLiteHandler) {
				if _, err := h.db.Exec(`DELETE FROM SourceOwnership WHERE source_id=42`); err != nil {
					t.Fatalf("revoke provenance: %v", err)
				}
			},
			approver: MCPMutationPrincipal{UserID: 9, Username: "admin-1", Issuer: "test-issuer"},
			wantErr:  ErrMCPMutationApprovalForbidden,
		},
		{
			name:     "forged approver username",
			mutate:   func(*mcpMutationSQLiteHandler) {},
			approver: MCPMutationPrincipal{UserID: 7, Username: "forged-name", Issuer: "test-issuer"},
			wantErr:  ErrMCPMutationApprovalForbidden,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler := newMCPMutationStoreTestHandler(t)
			proposal, policy, identity := sourceApprovalFixture(t)
			proposalStore, err := NewDatabaseMCPMutationProposalStore(&handler)
			if err != nil {
				t.Fatalf("new proposal store: %v", err)
			}
			_, err = proposalStore.Admit(context.Background(), proposal, 4, 8)
			if err != nil {
				t.Fatalf("admit proposal: %v", err)
			}
			store, err := NewDatabaseMCPMutationApprovalStore(&handler, time.Hour)
			if err != nil {
				t.Fatalf("new approval store: %v", err)
			}
			store.Policy = func() MCPMutationPolicy { return policy }
			store.Inventory = func() cfg.MCPConfig { return CurrentMCPOperatorConfig() }
			store.Agent = func(agentID string) (AgentIdentity, bool) {
				if agentID != identity.AgentID {
					return AgentIdentity{}, false
				}
				return identity, true
			}
			tc.mutate(handler.(*mcpMutationSQLiteHandler))
			_, inserted, err := store.Issue(context.Background(), MCPMutationApprovalRequest{
				ProposalID: proposal.ProposalID, ProposalSHA256: proposal.ProposalSHA256,
				Approver: tc.approver, MaxExecutions: 1,
			})
			if err == nil || inserted || !errors.Is(err, tc.wantErr) {
				t.Fatalf("approval inserted=%v err=%v, want %v", inserted, err, tc.wantErr)
			}
			var approvals int
			if err := handler.(*mcpMutationSQLiteHandler).db.QueryRow(
				`SELECT COUNT(*) FROM MCPMutationApprovals WHERE proposal_id=?`, proposal.ProposalID).Scan(&approvals); err != nil || approvals != 0 {
				t.Fatalf("approval rows after denied authority = %d err=%v", approvals, err)
			}
		})
	}
}

func TestMCPMutationApprovalAuthorityAcceptsOwnerAndAdmin(t *testing.T) {
	// Source-scoped owner approval.
	handler := newMCPMutationStoreTestHandler(t)
	proposal, policy, identity := sourceApprovalFixture(t)
	proposalStore, err := NewDatabaseMCPMutationProposalStore(&handler)
	if err != nil {
		t.Fatalf("new proposal store: %v", err)
	}
	admitted, err := proposalStore.Admit(context.Background(), proposal, 4, 8)
	if err != nil {
		t.Fatalf("admit source proposal: %v", err)
	}
	store, err := NewDatabaseMCPMutationApprovalStore(&handler, time.Hour)
	if err != nil {
		t.Fatalf("new approval store: %v", err)
	}
	store.Policy = func() MCPMutationPolicy { return policy }
	store.Inventory = func() cfg.MCPConfig { return CurrentMCPOperatorConfig() }
	store.Agent = func(agentID string) (AgentIdentity, bool) {
		if agentID != identity.AgentID {
			return AgentIdentity{}, false
		}
		return identity, true
	}
	owner := MCPMutationPrincipal{UserID: 7, Username: "mcp-owner", Issuer: "test-issuer"}
	approval, inserted, err := store.Issue(context.Background(), MCPMutationApprovalRequest{
		ProposalID: admitted.ProposalID, ProposalSHA256: admitted.ProposalSHA256,
		Approver: owner, MaxExecutions: 1,
	})
	if err != nil || !inserted || approval.State != "active" {
		t.Fatalf("owner source approval = %+v inserted=%v err=%v", approval, inserted, err)
	}

	// External-resource approval requires an administrator.
	externalHandler := newMCPMutationStoreTestHandler(t)
	externalProposal, current, _ := approvalPolicyFixture(t)
	externalStore, err := NewDatabaseMCPMutationProposalStore(&externalHandler)
	if err != nil {
		t.Fatalf("new external proposal store: %v", err)
	}
	externalAdmitted, err := externalStore.Admit(context.Background(), externalProposal, 4, 8)
	if err != nil {
		t.Fatalf("admit external proposal: %v", err)
	}
	approvalStore, err := NewDatabaseMCPMutationApprovalStore(&externalHandler, time.Hour)
	if err != nil {
		t.Fatalf("new external approval store: %v", err)
	}
	approvalStore.Policy = func() MCPMutationPolicy { return current.Policy }
	approvalStore.Inventory = func() cfg.MCPConfig { return current.Inventory }
	approvalStore.Agent = func(agentID string) (AgentIdentity, bool) {
		if current.Agent == nil || agentID != current.Agent.AgentID {
			return AgentIdentity{}, false
		}
		return *current.Agent, true
	}
	if _, _, err := approvalStore.Issue(context.Background(), MCPMutationApprovalRequest{
		ProposalID: externalAdmitted.ProposalID, ProposalSHA256: externalAdmitted.ProposalSHA256,
		Approver: owner, MaxExecutions: 1,
	}); !errors.Is(err, ErrMCPMutationApprovalForbidden) {
		t.Fatalf("nonadmin external approval error = %v", err)
	}
	admin := MCPMutationPrincipal{UserID: 9, Username: "admin-1", Issuer: "test-issuer"}
	approved, inserted, err := approvalStore.Issue(context.Background(), MCPMutationApprovalRequest{
		ProposalID: externalAdmitted.ProposalID, ProposalSHA256: externalAdmitted.ProposalSHA256,
		Approver: admin, MaxExecutions: 1,
	})
	if err != nil || !inserted || approved.State != "active" {
		t.Fatalf("admin external approval = %+v inserted=%v err=%v", approved, inserted, err)
	}
}

func TestMCPMutationApprovalReplayRevalidatesApprover(t *testing.T) {
	handler := newMCPMutationStoreTestHandler(t)
	proposal, current, _ := approvalPolicyFixture(t)
	proposalStore, err := NewDatabaseMCPMutationProposalStore(&handler)
	if err != nil {
		t.Fatalf("new proposal store: %v", err)
	}
	admitted, err := proposalStore.Admit(context.Background(), proposal, 4, 8)
	if err != nil {
		t.Fatalf("admit proposal: %v", err)
	}
	store, err := NewDatabaseMCPMutationApprovalStore(&handler, time.Hour)
	if err != nil {
		t.Fatalf("new approval store: %v", err)
	}
	store.Policy = func() MCPMutationPolicy { return current.Policy }
	store.Inventory = func() cfg.MCPConfig { return current.Inventory }
	store.Agent = func(agentID string) (AgentIdentity, bool) {
		if current.Agent == nil || agentID != current.Agent.AgentID {
			return AgentIdentity{}, false
		}
		return *current.Agent, true
	}
	admin := MCPMutationPrincipal{UserID: 9, Username: "admin-1", Issuer: "test-issuer"}
	if _, inserted, err := store.Issue(context.Background(), MCPMutationApprovalRequest{
		ProposalID: admitted.ProposalID, ProposalSHA256: admitted.ProposalSHA256,
		Approver: admin, MaxExecutions: 1,
	}); err != nil || !inserted {
		t.Fatalf("initial approval inserted=%v err=%v", inserted, err)
	}
	if _, err := handler.(*mcpMutationSQLiteHandler).db.Exec(`DELETE FROM UserRoles WHERE user_id=9 AND role_id=1`); err != nil {
		t.Fatalf("revoke admin role: %v", err)
	}
	if _, inserted, err := store.Issue(context.Background(), MCPMutationApprovalRequest{
		ProposalID: admitted.ProposalID, ProposalSHA256: admitted.ProposalSHA256,
		Approver: admin, MaxExecutions: 1,
	}); err == nil || inserted || !errors.Is(err, ErrMCPMutationApprovalForbidden) {
		t.Fatalf("replay after admin revocation inserted=%v err=%v", inserted, err)
	}
	var approvals int
	if err := handler.(*mcpMutationSQLiteHandler).db.QueryRow(
		`SELECT COUNT(*) FROM MCPMutationApprovals WHERE proposal_id=?`, admitted.ProposalID).Scan(&approvals); err != nil || approvals != 1 {
		t.Fatalf("approval rows after replay = %d err=%v", approvals, err)
	}
}

func TestMCPMutationApprovalServiceRequiresTrustedIdentity(t *testing.T) {
	handler := newMCPMutationStoreTestHandler(t)
	proposal, current, _ := approvalPolicyFixture(t)
	proposalStore, err := NewDatabaseMCPMutationProposalStore(&handler)
	if err != nil {
		t.Fatalf("new proposal store: %v", err)
	}
	admitted, err := proposalStore.Admit(context.Background(), proposal, 4, 8)
	if err != nil {
		t.Fatalf("admit proposal: %v", err)
	}
	store, err := NewDatabaseMCPMutationApprovalStore(&handler, time.Hour)
	if err != nil {
		t.Fatalf("new approval store: %v", err)
	}
	store.Policy = func() MCPMutationPolicy { return current.Policy }
	store.Inventory = func() cfg.MCPConfig { return current.Inventory }
	store.Agent = func(agentID string) (AgentIdentity, bool) {
		if current.Agent == nil || agentID != current.Agent.AgentID {
			return AgentIdentity{}, false
		}
		return *current.Agent, true
	}
	if _, _, err := store.Approve(context.Background(), MCPMutationApprovalCommand{
		Approver:        auth.Identity{Subject: "not-a-user", Username: "admin-1", Issuer: "test-issuer"},
		ProposalID:      admitted.ProposalID,
		ProposalSHA256:  admitted.ProposalSHA256,
		IdentityVersion: 1,
		MaxExecutions:   1,
	}); !errors.Is(err, ErrMCPMutationPrincipal) {
		t.Fatalf("forged typed identity error = %v", err)
	}
}

func TestMCPMutationApprovalAuthorityPreservesCancellation(t *testing.T) {
	handler := newMCPMutationStoreTestHandler(t)
	proposal, _, _ := sourceApprovalFixture(t)
	proposalStore, err := NewDatabaseMCPMutationProposalStore(&handler)
	if err != nil {
		t.Fatalf("new proposal store: %v", err)
	}
	if _, err := proposalStore.Admit(context.Background(), proposal, 4, 8); err != nil {
		t.Fatalf("admit proposal: %v", err)
	}
	raw := handler.(*mcpMutationSQLiteHandler).db
	tx, err := raw.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin approval transaction: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	authorizer := DatabaseMCPMutationApprovalAuthorizer{DB: &handler}
	approver := MCPMutationPrincipal{UserID: 7, Username: "mcp-owner", Issuer: "test-issuer"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	row := cdb.MCPMutationProposalRow{
		ProposalID: proposal.ProposalID, PrincipalUserID: 7, PrincipalUsername: "mcp-owner",
		PrincipalIssuer: "test-issuer", SourceIDs: []uint64{42},
	}
	if err := authorizer.AuthorizeMCPMutationApproval(ctx, tx, row, approver); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled approval authority error = %v, want context.Canceled", err)
	}
}

func TestMCPMutationApprovalAuthorityRequiresVerifiedApprover(t *testing.T) {
	handler := newMCPMutationStoreTestHandler(t)
	db := handler.(*mcpMutationSQLiteHandler).db
	if _, err := db.Exec(`DELETE FROM SourceOwnership WHERE source_id=42`); err != nil {
		t.Fatalf("remove approver provenance: %v", err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin authority transaction: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	err = cdb.AuthorizeMCPMutationApprovalTx(context.Background(), tx, handler, cdb.MCPMutationApprovalAuthorityRequest{
		SourceIDs: []uint64{42}, ApproverUserID: 7, ProposalPrincipalID: 9,
	})
	if !errors.Is(err, cdb.ErrMCPMutationApproverForbidden) {
		t.Fatalf("unverified approver error = %v, want approver forbidden", err)
	}
}
