package agent

import (
	"context"
	"errors"
	"testing"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func sourceAdmissionProposal(t *testing.T, manager *MCPToolManager, policy MCPMutationPolicy, alias, runID, callID string) MCPMutationProposal {
	t.Helper()
	store := NewInMemoryMCPMutationProposalStore()
	tool, err := NewMCPMutationProposalToolForOperation(manager, policy, store, alias)
	if err != nil {
		t.Fatalf("source proposal tool: %v", err)
	}
	result, err := tool.Execute(mcpMutationRuntime(mcpTestIdentity(), alias, runID, callID),
		map[string]any{"source_id": float64(42)})
	if err != nil {
		t.Fatalf("source proposal: %v", err)
	}
	proposal, found, err := store.Get(context.Background(), result["proposal_id"].(string))
	if err != nil || !found {
		t.Fatalf("load source proposal: found=%v err=%v", found, err)
	}
	return proposal
}

func TestMCPAdmissionRevalidatesCurrentPrincipalAndOwnership(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name    string
		mutate  func(*mcpMutationSQLiteHandler)
		wantErr error
	}{
		{
			name: "account disabled",
			mutate: func(h *mcpMutationSQLiteHandler) {
				if _, err := h.db.Exec(`UPDATE Users SET disabled=TRUE WHERE user_id=7`); err != nil {
					t.Fatalf("disable account: %v", err)
				}
			},
			wantErr: ErrMCPMutationPrincipal,
		},
		{
			name: "account deleted",
			mutate: func(h *mcpMutationSQLiteHandler) {
				if _, err := h.db.Exec(`UPDATE Users SET deleted_at=CURRENT_TIMESTAMP WHERE user_id=7`); err != nil {
					t.Fatalf("delete account: %v", err)
				}
			},
			wantErr: ErrMCPMutationPrincipal,
		},
		{
			name: "ownership revoked",
			mutate: func(h *mcpMutationSQLiteHandler) {
				if _, err := h.db.Exec(`DELETE FROM SourceOwnership WHERE source_id=42`); err != nil {
					t.Fatalf("revoke ownership: %v", err)
				}
			},
			wantErr: ErrMCPMutationOperationUnauthorized,
		},
		{
			name: "ownership unverified",
			mutate: func(h *mcpMutationSQLiteHandler) {
				if _, err := h.db.Exec(`INSERT INTO Users(user_id, username, disabled, deleted_at) VALUES (8, 'other-owner', FALSE, NULL)`); err != nil {
					t.Fatalf("insert other owner: %v", err)
				}
				if _, err := h.db.Exec(`UPDATE SourceOwnership SET user_id=8 WHERE source_id=42`); err != nil {
					t.Fatalf("unverify ownership: %v", err)
				}
			},
			wantErr: ErrMCPMutationOperationUnauthorized,
		},
		{
			name: "ownership transferred",
			mutate: func(h *mcpMutationSQLiteHandler) {
				if _, err := h.db.Exec(`INSERT INTO Users(user_id, username, disabled, deleted_at) VALUES (8, 'other-owner', FALSE, NULL)`); err != nil {
					t.Fatalf("insert other owner: %v", err)
				}
				if _, err := h.db.Exec(`UPDATE Sources SET usr_id=8 WHERE source_id=42`); err != nil {
					t.Fatalf("transfer source: %v", err)
				}
				if _, err := h.db.Exec(`UPDATE SourceOwnership SET user_id=8 WHERE source_id=42`); err != nil {
					t.Fatalf("transfer provenance: %v", err)
				}
			},
			wantErr: ErrMCPMutationOperationUnauthorized,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler := newMCPMutationStoreTestHandler(t)
			manager, policy, aliases, _, cleanup := newMCPAuthorityManager(t)
			defer cleanup()
			proposal := sourceAdmissionProposal(t, manager, policy, aliases[1], "admission-"+tc.name, "call-"+tc.name)
			tc.mutate(handler.(*mcpMutationSQLiteHandler))
			store, err := NewDatabaseMCPMutationProposalStore(&handler)
			if err != nil {
				t.Fatalf("new durable store: %v", err)
			}
			_, err = store.Admit(ctx, proposal, 4, 8)
			if err == nil || !errors.Is(err, tc.wantErr) {
				t.Fatalf("admission error = %v, want %v", err, tc.wantErr)
			}
			var count int
			if err := handler.(*mcpMutationSQLiteHandler).db.QueryRow(`SELECT COUNT(*) FROM MCPMutationProposals`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("proposal rows after denied admission = %d err=%v", count, err)
			}
		})
	}
}

func TestMCPAdmissionAcceptsUnchangedPrincipalAndOwnership(t *testing.T) {
	ctx := context.Background()
	handler := newMCPMutationStoreTestHandler(t)
	manager, policy, aliases, _, cleanup := newMCPAuthorityManager(t)
	defer cleanup()
	proposal := sourceAdmissionProposal(t, manager, policy, aliases[1], "admission-valid", "call-valid")
	store, err := NewDatabaseMCPMutationProposalStore(&handler)
	if err != nil {
		t.Fatalf("new durable store: %v", err)
	}
	admitted, err := store.Admit(ctx, proposal, 4, 8)
	if err != nil {
		t.Fatalf("valid admission: %v", err)
	}
	if admitted.State != MCPProposalApprovalRequired || admitted.SourceIDs[0] != 42 {
		t.Fatalf("unexpected admitted proposal: %+v", admitted)
	}
}

func TestMCPAdmissionDBOutageFailsClosed(t *testing.T) {
	ctx := context.Background()
	handler := newMCPMutationStoreTestHandler(t)
	manager, policy, aliases, _, cleanup := newMCPAuthorityManager(t)
	defer cleanup()
	proposal := sourceAdmissionProposal(t, manager, policy, aliases[1], "admission-outage", "call-outage")
	store, err := NewDatabaseMCPMutationProposalStore(&handler)
	if err != nil {
		t.Fatalf("new durable store: %v", err)
	}
	if err := handler.(*mcpMutationSQLiteHandler).db.Close(); err != nil {
		t.Fatalf("close DB: %v", err)
	}
	if _, err := store.Admit(ctx, proposal, 4, 8); !errors.Is(err, ErrMCPMutationStoreUnavailable) {
		t.Fatalf("admission during DB outage error = %v, want store unavailable", err)
	}
}

func TestMCPAdmissionRejectsForgedPrincipalArgument(t *testing.T) {
	manager, policy, aliases, identity, cleanup := newMCPAuthorityManager(t)
	defer cleanup()
	store := NewInMemoryMCPMutationProposalStore()
	tool, err := NewMCPMutationProposalToolForOperation(manager, policy, store, aliases[0])
	if err != nil {
		t.Fatalf("proposal tool: %v", err)
	}
	ctx := mcpMutationRuntime(identity, aliases[0], "forged-principal-run", "forged-principal-call")
	_, err = tool.Execute(ctx, map[string]any{
		"resource":               "fixture-resource",
		"mcp_mutation_principal": map[string]any{"user_id": 999, "username": "forged"},
	})
	var authErr *ToolAuthError
	if err == nil || (!errors.As(err, &authErr) && !errors.Is(err, ErrMCPMutationArguments)) {
		t.Fatalf("forged principal argument error = %v", err)
	}
	if authErr != nil && authErr.Code != ToolDenyArguments {
		t.Fatalf("forged principal argument denial code = %q", authErr.Code)
	}
	if len(store.byID) != 0 {
		t.Fatalf("forged principal created %d proposals", len(store.byID))
	}
}

func TestMCPAdmissionRejectsBackgroundRunWithoutPrincipal(t *testing.T) {
	manager, policy, aliases, identity, cleanup := newMCPAuthorityManager(t)
	defer cleanup()
	store := NewInMemoryMCPMutationProposalStore()
	tool, err := NewMCPMutationProposalToolForOperation(manager, policy, store, aliases[0])
	if err != nil {
		t.Fatalf("proposal tool: %v", err)
	}
	ctx := ContextWithToolRuntime(context.Background(), ToolRuntime{
		Auth: ToolAuthContext{
			Identity: identity, Enforcement: true,
			Allowlist:          map[string]bool{aliases[0]: true},
			MCPMutationAllowed: true, MCPAllowedOperations: map[string]bool{aliases[0]: true},
			RunID: "background-principal-run", TraceID: "background-principal-trace",
		},
		CallID: "background-principal-call",
	})
	if _, err := tool.Execute(ctx, map[string]any{"resource": "fixture-resource"}); !errors.Is(err, ErrMCPMutationPrincipal) {
		t.Fatalf("background principal error = %v", err)
	}
	if len(store.byID) != 0 {
		t.Fatalf("background run created %d proposals", len(store.byID))
	}
}

var _ cdb.Handler = (*mcpMutationSQLiteHandler)(nil)

func TestMCPAdmissionReplayRevalidatesAuthority(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*mcpMutationSQLiteHandler)
		wantErr error
	}{
		{
			name: "account disabled before replay",
			mutate: func(h *mcpMutationSQLiteHandler) {
				if _, err := h.db.Exec(`UPDATE Users SET disabled=TRUE WHERE user_id=7`); err != nil {
					t.Fatalf("disable account: %v", err)
				}
			},
			wantErr: ErrMCPMutationPrincipal,
		},
		{
			name: "ownership transferred before replay",
			mutate: func(h *mcpMutationSQLiteHandler) {
				if _, err := h.db.Exec(`INSERT INTO Users(user_id, username, disabled, deleted_at) VALUES (8, 'other-owner', FALSE, NULL)`); err != nil {
					t.Fatalf("insert other owner: %v", err)
				}
				if _, err := h.db.Exec(`UPDATE Sources SET usr_id=8 WHERE source_id=42`); err != nil {
					t.Fatalf("transfer source: %v", err)
				}
				if _, err := h.db.Exec(`UPDATE SourceOwnership SET user_id=8 WHERE source_id=42`); err != nil {
					t.Fatalf("transfer provenance: %v", err)
				}
			},
			wantErr: ErrMCPMutationOperationUnauthorized,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			handler := newMCPMutationStoreTestHandler(t)
			manager, policy, aliases, _, cleanup := newMCPAuthorityManager(t)
			defer cleanup()
			proposal := sourceAdmissionProposal(t, manager, policy, aliases[1], "replay-"+tc.name, "call-"+tc.name)
			store, err := NewDatabaseMCPMutationProposalStore(&handler)
			if err != nil {
				t.Fatalf("new durable store: %v", err)
			}
			if _, err := store.Admit(ctx, proposal, 4, 8); err != nil {
				t.Fatalf("initial admission: %v", err)
			}
			tc.mutate(handler.(*mcpMutationSQLiteHandler))
			if _, err := store.Admit(ctx, proposal, 4, 8); !errors.Is(err, tc.wantErr) {
				t.Fatalf("replay error = %v, want %v", err, tc.wantErr)
			}
			var count int
			if err := handler.(*mcpMutationSQLiteHandler).db.QueryRow(`SELECT COUNT(*) FROM MCPMutationProposals`).Scan(&count); err != nil || count != 1 {
				t.Fatalf("proposal rows after replay = %d err=%v", count, err)
			}
		})
	}
}

func TestMCPAdmissionAuthorizerPreservesCancellation(t *testing.T) {
	handler := newMCPMutationStoreTestHandler(t)
	manager, policy, aliases, _, cleanup := newMCPAuthorityManager(t)
	defer cleanup()
	proposal := sourceAdmissionProposal(t, manager, policy, aliases[1], "authorizer-cancel", "authorizer-cancel-call")
	raw := handler.(*mcpMutationSQLiteHandler).db
	tx, err := raw.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin admission transaction: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	authorizer := DatabaseMCPMutationAdmissionAuthorizer{DB: &handler}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := authorizer.AuthorizeMCPMutationAdmission(ctx, tx, proposal); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled authorizer error = %v, want context.Canceled", err)
	}
}
