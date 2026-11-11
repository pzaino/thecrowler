package agent

import (
	"context"
	"database/sql"
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
		statement = strings.TrimSpace(statement)
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
