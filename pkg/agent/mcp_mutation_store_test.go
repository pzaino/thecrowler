package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"

	_ "github.com/mattn/go-sqlite3"
)

type mcpMutationSQLiteHandler struct {
	db *sql.DB
}

func (h *mcpMutationSQLiteHandler) Connect(cfg.Config) error { return nil }
func (h *mcpMutationSQLiteHandler) Close() error             { return h.db.Close() }
func (h *mcpMutationSQLiteHandler) Ping() error              { return h.db.Ping() }
func (h *mcpMutationSQLiteHandler) ExecuteQuery(query string, args ...interface{}) (*sql.Rows, error) {
	return h.db.Query(query, args...)
}
func (h *mcpMutationSQLiteHandler) Exec(query string, args ...interface{}) (sql.Result, error) {
	return h.db.Exec(query, args...)
}
func (h *mcpMutationSQLiteHandler) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	return h.db.ExecContext(ctx, query, args...)
}
func (h *mcpMutationSQLiteHandler) DBMS() string { return cdb.DBSQLiteStr }
func (h *mcpMutationSQLiteHandler) Begin() (*sql.Tx, error) {
	return h.db.Begin()
}
func (h *mcpMutationSQLiteHandler) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return h.db.BeginTx(ctx, opts)
}
func (h *mcpMutationSQLiteHandler) Commit(tx *sql.Tx) error   { return tx.Commit() }
func (h *mcpMutationSQLiteHandler) Rollback(tx *sql.Tx) error { return tx.Rollback() }
func (h *mcpMutationSQLiteHandler) QueryRow(query string, args ...interface{}) *sql.Row {
	return h.db.QueryRow(query, args...)
}
func (h *mcpMutationSQLiteHandler) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	return h.db.QueryRowContext(ctx, query, args...)
}
func (h *mcpMutationSQLiteHandler) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	return h.db.QueryContext(ctx, query, args...)
}
func (h *mcpMutationSQLiteHandler) CheckConnection(cfg.Config) error { return nil }
func (h *mcpMutationSQLiteHandler) WaitForConnection(cfg.Config, time.Duration) error {
	return nil
}
func (h *mcpMutationSQLiteHandler) NewListener() cdb.Listener { return nil }
func (h *mcpMutationSQLiteHandler) TransactionTimeoutSeconds() int {
	return 30
}

func stripMCPMutationStoreSQLComments(statement string) string {
	lines := strings.Split(statement, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func newMCPMutationStoreTestHandler(t *testing.T) cdb.Handler {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		t.Fatalf("enable SQLite foreign keys: %v", err)
	}
	script, err := os.ReadFile("../database/db_migrations/sqlite-mcp-mutation-v1.17.sqlite3")
	if err != nil {
		t.Fatalf("read MCP mutation migration: %v", err)
	}
	for _, statement := range strings.Split(string(script), ";") {
		statement = stripMCPMutationStoreSQLComments(statement)
		if statement == "" || strings.HasPrefix(strings.ToUpper(statement), "BEGIN IMMEDIATE") ||
			strings.EqualFold(statement, "COMMIT") || strings.HasPrefix(strings.ToUpper(statement), "PRAGMA") {
			continue
		}
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("apply MCP mutation statement: %v\n%s", err, statement)
		}
	}
	handler := cdb.Handler(&mcpMutationSQLiteHandler{db: db})
	t.Cleanup(func() { _ = db.Close() })
	return handler
}

func TestDatabaseMCPMutationStoreAdmitAndApproval(t *testing.T) {
	ctx := context.Background()
	handler := newMCPMutationStoreTestHandler(t)
	_, tool, memStore, identity, _, alias := newMCPMutationHarness(t)
	result, err := tool.Execute(mcpMutationRuntime(identity, alias, "db-run", "db-call"),
		map[string]any{"resource": "fixture-resource"})
	if err != nil {
		t.Fatalf("proposal tool: %v", err)
	}
	proposal, found, err := memStore.Get(ctx, result["proposal_id"].(string))
	if err != nil || !found {
		t.Fatalf("load proposal: found=%v err=%v", found, err)
	}
	store, err := NewDatabaseMCPMutationProposalStore(&handler)
	if err != nil {
		t.Fatalf("new database proposal store: %v", err)
	}
	admitted, err := store.Admit(ctx, proposal, 4, 8)
	if err != nil {
		t.Fatalf("durable admission: %v", err)
	}
	if admitted.ProposalID != proposal.ProposalID || admitted.ProposalSHA256 != proposal.ProposalSHA256 {
		t.Fatalf("durable proposal identity changed: %+v vs %+v", admitted, proposal)
	}
	replay, err := store.Admit(ctx, proposal, 4, 8)
	if err != nil || replay.ProposalID != proposal.ProposalID {
		t.Fatalf("durable replay: %+v err=%v", replay, err)
	}
	loaded, found, err := store.Get(ctx, proposal.ProposalID)
	if err != nil || !found || loaded.IdentityVersion != proposal.IdentityVersion {
		t.Fatalf("durable get: %+v found=%v err=%v", loaded, found, err)
	}
	approvalStore, err := NewDatabaseMCPMutationApprovalStore(&handler, time.Hour)
	if err != nil {
		t.Fatalf("new database approval store: %v", err)
	}
	approver := MCPMutationPrincipal{UserID: 9, Username: "admin-1", Issuer: "test-issuer"}
	approval, inserted, err := approvalStore.Issue(ctx, MCPMutationApprovalRequest{
		ProposalID: proposal.ProposalID, ProposalSHA256: proposal.ProposalSHA256,
		Approver: approver, MaxExecutions: 1,
	})
	if err != nil || !inserted || approval.State != "active" {
		t.Fatalf("durable approval: %+v inserted=%v err=%v", approval, inserted, err)
	}
	verifier := MCPMutationApprovalVerifier{Store: approvalStore}
	if _, err := verifier.Verify(ctx, proposal, approval.ApprovalID, approver); err != nil {
		t.Fatalf("durable approval verification: %v", err)
	}
	revoked, transitioned, err := approvalStore.Revoke(ctx, approval.ApprovalID, approver, "test revocation")
	if err != nil || !transitioned || revoked.State != "revoked" {
		t.Fatalf("durable revocation: %+v transitioned=%v err=%v", revoked, transitioned, err)
	}
	if _, err := verifier.Verify(ctx, proposal, approval.ApprovalID, approver); err == nil {
		t.Fatalf("revoked durable approval verified")
	}
}

func mcpMutationDurableTestProposal(t *testing.T, handler cdb.Handler, seed string) MCPMutationProposal {
	t.Helper()
	_, tool, memStore, identity, _, alias := newMCPMutationHarness(t)
	result, err := tool.Execute(mcpMutationRuntime(identity, alias, "durable-"+seed, "call-"+seed),
		map[string]any{"resource": "fixture-resource"})
	if err != nil {
		t.Fatalf("proposal tool %s: %v", seed, err)
	}
	proposal, found, err := memStore.Get(context.Background(), result["proposal_id"].(string))
	if err != nil || !found {
		t.Fatalf("load proposal %s: found=%v err=%v", seed, found, err)
	}
	store, err := NewDatabaseMCPMutationProposalStore(&handler)
	if err != nil {
		t.Fatalf("new durable store %s: %v", seed, err)
	}
	admitted, err := store.Admit(context.Background(), proposal, 4, 8)
	if err != nil {
		t.Fatalf("durable admission %s: %v", seed, err)
	}
	return admitted
}

func TestDatabaseMCPMutationStoreRejectsCorruptProposal(t *testing.T) {
	handler := newMCPMutationStoreTestHandler(t)
	db := handler.(*mcpMutationSQLiteHandler).db
	proposal := mcpMutationDurableTestProposal(t, handler, "corrupt")
	store, err := NewDatabaseMCPMutationProposalStore(&handler)
	if err != nil {
		t.Fatalf("new durable store: %v", err)
	}
	cases := []struct {
		name string
		args []byte
	}{
		{"truncated", []byte(`{"resource":`)},
		{"noncanonical-whitespace", []byte(`{ "resource" : "fixture-resource" }`)},
		{"trailing-json", append(append([]byte(nil), proposal.CanonicalArguments...), []byte(`{}`)...)},
		{"duplicate-keys", []byte(`{"resource":"fixture-resource","resource":"fixture-resource"}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := db.Exec(`UPDATE MCPMutationProposals SET canonical_arguments=?, arguments_sha256=? WHERE proposal_id=?`,
				tc.args, sha256Hex(tc.args), proposal.ProposalID); err != nil {
				t.Fatalf("corrupt canonical arguments: %v", err)
			}
			if _, found, err := store.Get(context.Background(), proposal.ProposalID); !found || !errors.Is(err, ErrMCPMutationProposalCorrupt) {
				t.Fatalf("corrupt get found=%v err=%v", found, err)
			}
		})
	}
	t.Run("missing owner identity", func(t *testing.T) {
		if _, err := db.Exec(`UPDATE MCPMutationProposals SET canonical_arguments=?, arguments_sha256=?, owner='' WHERE proposal_id=?`,
			proposal.CanonicalArguments, proposal.ArgumentsSHA256, proposal.ProposalID); err != nil {
			t.Fatalf("corrupt owner: %v", err)
		}
		if _, found, err := store.Get(context.Background(), proposal.ProposalID); !found || !errors.Is(err, ErrMCPMutationProposalCorrupt) {
			t.Fatalf("corrupt owner get found=%v err=%v", found, err)
		}
	})
	t.Run("policy snapshot", func(t *testing.T) {
		if _, err := db.Exec(`UPDATE MCPMutationProposals SET canonical_arguments=?, arguments_sha256=?, policy_snapshot=? WHERE proposal_id=?`,
			proposal.CanonicalArguments, proposal.ArgumentsSHA256, []byte(`{"tampered":true}`), proposal.ProposalID); err != nil {
			t.Fatalf("corrupt policy snapshot: %v", err)
		}
		if _, found, err := store.Get(context.Background(), proposal.ProposalID); !found || !errors.Is(err, ErrMCPMutationProposalCorrupt) {
			t.Fatalf("corrupt policy get found=%v err=%v", found, err)
		}
	})
}

func TestDatabaseMCPMutationApprovalRejectsCorruptProposal(t *testing.T) {
	handler := newMCPMutationStoreTestHandler(t)
	db := handler.(*mcpMutationSQLiteHandler).db
	proposal := mcpMutationDurableTestProposal(t, handler, "corrupt-approval")
	bad := []byte(`{"resource":`)
	if _, err := db.Exec(`UPDATE MCPMutationProposals SET canonical_arguments=?, arguments_sha256=? WHERE proposal_id=?`,
		bad, sha256Hex(bad), proposal.ProposalID); err != nil {
		t.Fatalf("corrupt proposal before approval: %v", err)
	}
	approvalStore, err := NewDatabaseMCPMutationApprovalStore(&handler, time.Hour)
	if err != nil {
		t.Fatalf("new durable approval store: %v", err)
	}
	approver := MCPMutationPrincipal{UserID: 9, Username: "admin-1", Issuer: "test-issuer"}
	if _, _, err := approvalStore.Issue(context.Background(), MCPMutationApprovalRequest{
		ProposalID: proposal.ProposalID, ProposalSHA256: proposal.ProposalSHA256,
		Approver: approver, MaxExecutions: 1,
	}); !errors.Is(err, ErrMCPMutationProposalCorrupt) {
		t.Fatalf("approval of corrupt proposal error = %v", err)
	}
	var approvals int
	if err := db.QueryRow(`SELECT COUNT(*) FROM MCPMutationApprovals WHERE proposal_id=?`, proposal.ProposalID).Scan(&approvals); err != nil || approvals != 0 {
		t.Fatalf("approvals after corrupt refusal = %d err=%v", approvals, err)
	}
}

func TestDatabaseMCPMutationStoreAcceptsNormalizedPolicySnapshot(t *testing.T) {
	handler := newMCPMutationStoreTestHandler(t)
	db := handler.(*mcpMutationSQLiteHandler).db
	proposal := mcpMutationDurableTestProposal(t, handler, "normalized-policy")
	var decoded any
	if err := json.Unmarshal(proposal.PolicySnapshot, &decoded); err != nil {
		t.Fatalf("decode policy snapshot: %v", err)
	}
	normalized, err := json.Marshal(decoded)
	if err != nil {
		t.Fatalf("normalize policy snapshot: %v", err)
	}
	if _, err := db.Exec(`UPDATE MCPMutationProposals SET policy_snapshot=? WHERE proposal_id=?`,
		normalized, proposal.ProposalID); err != nil {
		t.Fatalf("write normalized policy snapshot: %v", err)
	}
	store, err := NewDatabaseMCPMutationProposalStore(&handler)
	if err != nil {
		t.Fatalf("new durable store: %v", err)
	}
	loaded, found, err := store.Get(context.Background(), proposal.ProposalID)
	if err != nil || !found || loaded.Arguments == nil {
		t.Fatalf("normalized policy proposal = %+v found=%v err=%v", loaded, found, err)
	}
}

func TestMCPMutationProposalToolUsesDurableStoreWithoutExecution(t *testing.T) {
	ctx := context.Background()
	handler := newMCPMutationStoreTestHandler(t)
	_, tool, _, identity, counterPath, alias := newMCPMutationHarness(t)
	store, err := NewDatabaseMCPMutationProposalStore(&handler)
	if err != nil {
		t.Fatalf("new durable proposal store: %v", err)
	}
	durableTool, err := NewMCPMutationProposalTool(tool.descriptor.manager, mcpMutationTestPolicy(alias), store)
	if err != nil {
		t.Fatalf("new durable proposal tool: %v", err)
	}
	args := map[string]any{"resource": "fixture-resource"}
	result, err := durableTool.Execute(mcpMutationRuntime(identity, alias, "durable-producer-run", "durable-producer-call"), args)
	if err != nil {
		t.Fatalf("durable proposal tool: %v", err)
	}
	proposalID, _ := result["proposal_id"].(string)
	if proposalID == "" {
		t.Fatalf("durable proposal reference missing: %+v", result)
	}
	stored, found, err := cdb.GetMCPMutationProposal(ctx, &handler, proposalID)
	if err != nil || !found || stored.State != cdb.MCPMutationStateApprovalRequired {
		t.Fatalf("durable proposal = %+v found=%v err=%v", stored, found, err)
	}
	replay, err := durableTool.Execute(mcpMutationRuntime(identity, alias, "durable-producer-run", "durable-producer-call"), args)
	if err != nil || replay["proposal_id"] != proposalID {
		t.Fatalf("durable replay = %+v err=%v", replay, err)
	}
	if data, err := os.ReadFile(counterPath); err == nil && strings.TrimSpace(string(data)) != "" {
		t.Fatalf("durable proposal path executed the mutation fixture: %s", data)
	}
	var attempts int
	if err := handler.QueryRowContext(ctx, `SELECT COUNT(*) FROM MCPMutationDispatchAttempts`).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("dispatch attempts after durable proposal = %d err=%v", attempts, err)
	}
}
