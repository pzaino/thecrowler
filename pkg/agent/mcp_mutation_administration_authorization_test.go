package agent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func newAdministrationStore(t *testing.T, handler cdb.Handler, policy MCPMutationPolicy, identity AgentIdentity) *DatabaseMCPMutationApprovalStore {
	t.Helper()
	store, err := NewDatabaseMCPMutationApprovalStore(&handler, time.Hour)
	if err != nil {
		t.Fatalf("new administration store: %v", err)
	}
	store.Policy = func() MCPMutationPolicy { return policy }
	store.Inventory = func() cfg.MCPConfig { return CurrentMCPOperatorConfig() }
	store.Agent = func(agentID string) (AgentIdentity, bool) {
		if agentID != identity.AgentID {
			return AgentIdentity{}, false
		}
		return identity, true
	}
	return store
}

func admittedSourceProposal(t *testing.T, handler cdb.Handler) (MCPMutationProposal, MCPMutationPolicy, AgentIdentity) {
	t.Helper()
	proposal, policy, identity := sourceApprovalFixture(t)
	proposalStore, err := NewDatabaseMCPMutationProposalStore(&handler)
	if err != nil {
		t.Fatalf("new proposal store: %v", err)
	}
	admitted, err := proposalStore.Admit(context.Background(), proposal, 4, 8)
	if err != nil {
		t.Fatalf("admit source proposal: %v", err)
	}
	return admitted, policy, identity
}

func TestMCPMutationAdministrationDenyAuthority(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*mcpMutationSQLiteHandler)
		actor  MCPMutationPrincipal
		want   error
	}{
		{
			name:   "current owner may deny",
			mutate: func(*mcpMutationSQLiteHandler) {},
			actor:  MCPMutationPrincipal{UserID: 7, Username: "mcp-owner", Issuer: "test-issuer"},
			want:   nil,
		},
		{
			name:   "unrelated user may not deny",
			mutate: func(*mcpMutationSQLiteHandler) {},
			actor:  MCPMutationPrincipal{UserID: 8, Username: "other-owner", Issuer: "test-issuer"},
			want:   ErrMCPMutationAdministrationForbidden,
		},
		{
			name: "admin may deny after transfer",
			mutate: func(h *mcpMutationSQLiteHandler) {
				if _, err := h.db.Exec(`UPDATE Sources SET usr_id=8 WHERE source_id=42`); err != nil {
					t.Fatalf("transfer source: %v", err)
				}
				if _, err := h.db.Exec(`UPDATE SourceOwnership SET user_id=8 WHERE source_id=42`); err != nil {
					t.Fatalf("transfer provenance: %v", err)
				}
			},
			actor: MCPMutationPrincipal{UserID: 9, Username: "admin-1", Issuer: "test-issuer"},
			want:  nil,
		},
		{
			name: "disabled actor may not deny",
			mutate: func(h *mcpMutationSQLiteHandler) {
				if _, err := h.db.Exec(`UPDATE Users SET disabled=TRUE WHERE user_id=7`); err != nil {
					t.Fatalf("disable actor: %v", err)
				}
			},
			actor: MCPMutationPrincipal{UserID: 7, Username: "mcp-owner", Issuer: "test-issuer"},
			want:  ErrMCPMutationAdministrationForbidden,
		},
		{
			name: "revoked admin role may not deny",
			mutate: func(h *mcpMutationSQLiteHandler) {
				if _, err := h.db.Exec(`DELETE FROM UserRoles WHERE user_id=9 AND role_id=1`); err != nil {
					t.Fatalf("revoke admin role: %v", err)
				}
			},
			actor: MCPMutationPrincipal{UserID: 9, Username: "admin-1", Issuer: "test-issuer"},
			want:  ErrMCPMutationAdministrationForbidden,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler := newMCPMutationStoreTestHandler(t)
			proposal, policy, identity := admittedSourceProposal(t, handler)
			store := newAdministrationStore(t, handler, policy, identity)
			tc.mutate(handler.(*mcpMutationSQLiteHandler))
			row, transitioned, err := store.Deny(context.Background(), proposal.ProposalID, tc.actor, "authority test")
			if tc.want != nil {
				if err == nil || transitioned || !errors.Is(err, tc.want) {
					t.Fatalf("deny transitioned=%v err=%v, want %v", transitioned, err, tc.want)
				}
			} else if err != nil || !transitioned || row.State != cdb.MCPMutationStateDenied {
				t.Fatalf("deny row=%+v transitioned=%v err=%v", row, transitioned, err)
			}
			var attempts int
			if err := handler.(*mcpMutationSQLiteHandler).db.QueryRow(`SELECT COUNT(*) FROM MCPMutationDispatchAttempts`).Scan(&attempts); err != nil || attempts != 0 {
				t.Fatalf("dispatch attempts after deny = %d err=%v", attempts, err)
			}
		})
	}
}

func TestMCPMutationAdministrationRevokeAuthority(t *testing.T) {
	handler := newMCPMutationStoreTestHandler(t)
	proposal, policy, identity := admittedSourceProposal(t, handler)
	store := newAdministrationStore(t, handler, policy, identity)
	owner := MCPMutationPrincipal{UserID: 7, Username: "mcp-owner", Issuer: "test-issuer"}
	approval, inserted, err := store.Issue(context.Background(), MCPMutationApprovalRequest{
		ProposalID: proposal.ProposalID, ProposalSHA256: proposal.ProposalSHA256,
		Approver: owner, MaxExecutions: 1,
	})
	if err != nil || !inserted {
		t.Fatalf("approve before revoke inserted=%v err=%v", inserted, err)
	}
	if _, err := handler.(*mcpMutationSQLiteHandler).db.Exec(`UPDATE Users SET disabled=TRUE WHERE user_id=7`); err != nil {
		t.Fatalf("disable proposal principal: %v", err)
	}
	admin := MCPMutationPrincipal{UserID: 9, Username: "admin-1", Issuer: "test-issuer"}
	revoked, transitioned, err := store.Revoke(context.Background(), approval.ApprovalID, admin, "emergency revoke")
	if err != nil || !transitioned || revoked.State != "revoked" {
		t.Fatalf("admin revoke after principal disable = %+v transitioned=%v err=%v", revoked, transitioned, err)
	}
	var attempts int
	if err := handler.(*mcpMutationSQLiteHandler).db.QueryRow(`SELECT COUNT(*) FROM MCPMutationDispatchAttempts`).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("dispatch attempts after revoke = %d err=%v", attempts, err)
	}
}

func TestMCPMutationAdministrationConcurrentDenyIsIdempotent(t *testing.T) {
	handler := newMCPMutationStoreTestHandler(t)
	proposal, policy, identity := admittedSourceProposal(t, handler)
	store := newAdministrationStore(t, handler, policy, identity)
	owner := MCPMutationPrincipal{UserID: 7, Username: "mcp-owner", Issuer: "test-issuer"}
	type outcome struct {
		transitioned bool
		err          error
	}
	results := make(chan outcome, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, transitioned, err := store.Deny(context.Background(), proposal.ProposalID, owner, "concurrent deny")
			results <- outcome{transitioned: transitioned, err: err}
		}()
	}
	wg.Wait()
	close(results)
	transitions := 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent deny: %v", result.err)
		}
		if result.transitioned {
			transitions++
		}
	}
	if transitions != 1 {
		t.Fatalf("deny transitions = %d, want 1", transitions)
	}
	var state string
	if err := handler.(*mcpMutationSQLiteHandler).db.QueryRow(
		`SELECT state FROM MCPMutationProposals WHERE proposal_id=?`, proposal.ProposalID).Scan(&state); err != nil || state != cdb.MCPMutationStateDenied {
		t.Fatalf("final proposal state = %q err=%v", state, err)
	}
}

func TestMCPMutationAdministrationPreservesCancellationAndOutage(t *testing.T) {
	handler := newMCPMutationStoreTestHandler(t)
	proposal, policy, identity := admittedSourceProposal(t, handler)
	store := newAdministrationStore(t, handler, policy, identity)
	owner := MCPMutationPrincipal{UserID: 7, Username: "mcp-owner", Issuer: "test-issuer"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := store.Deny(ctx, proposal.ProposalID, owner, "cancelled"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled deny error = %v", err)
	}
	if err := handler.(*mcpMutationSQLiteHandler).db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}
	if _, _, err := store.Deny(context.Background(), proposal.ProposalID, owner, "outage"); !errors.Is(err, ErrMCPMutationStoreUnavailable) {
		t.Fatalf("outage deny error = %v, want store unavailable", err)
	}
}
