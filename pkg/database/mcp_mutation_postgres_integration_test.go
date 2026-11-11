//go:build integration

package database

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

// TestPostgresMCPMutationDurableStore runs against an explicitly supplied
// disposable PostgreSQL database that already has the additive migration
// applied. It skips unless CROWLER_MCP_POSTGRES_DSN is set.
func TestPostgresMCPMutationDurableStore(t *testing.T) {
	dsn := os.Getenv("CROWLER_MCP_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set CROWLER_MCP_POSTGRES_DSN to run the disposable PostgreSQL MCP mutation test")
	}
	db, err := sql.Open(DBPostgresStr, dsn)
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	db.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping PostgreSQL: %v", err)
	}
	var handler Handler = &PostgresHandler{db: db, dbms: DBPostgresStr, connStr: dsn}
	if err := CheckMCPMutationCapability(ctx, &handler); err != nil {
		t.Fatalf("PostgreSQL MCP mutation capability: %v", err)
	}
	if _, err := db.ExecContext(ctx, `TRUNCATE MCPMutationAudit, MCPMutationDispatchAttempts,
		MCPMutationApprovals, MCPMutationProposals, MCPMutationRunQuota,
		MCPMutationPrincipalQuota RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate MCP mutation tables: %v", err)
	}
	now := time.Now().UTC()
	proposal := mcpMutationTestProposal("postgres", "pg-run", "pg-call", now)
	if _, inserted, err := AdmitMCPMutationProposal(ctx, &handler, proposal, 2, 2); err != nil || !inserted {
		t.Fatalf("PostgreSQL admission inserted=%v err=%v", inserted, err)
	}
	loaded, found, err := GetMCPMutationProposal(ctx, &handler, proposal.ProposalID)
	if err != nil || !found || loaded.SourceIDs[0] != 42 || string(loaded.CanonicalArguments) != string(proposal.CanonicalArguments) {
		t.Fatalf("PostgreSQL proposal round-trip = %+v found=%v err=%v", loaded, found, err)
	}
	external := mcpMutationTestProposal("postgres-external", "pg-external-run", "pg-external-call", now)
	external.SourceIDs = []uint64{}
	external.ExternalResource = "external-resource"
	if _, inserted, err := AdmitMCPMutationProposal(ctx, &handler, external, 2, 2); err != nil || !inserted {
		t.Fatalf("PostgreSQL external admission inserted=%v err=%v", inserted, err)
	}
	externalLoaded, found, err := GetMCPMutationProposal(ctx, &handler, external.ProposalID)
	if err != nil || !found || len(externalLoaded.SourceIDs) != 0 || externalLoaded.ExternalResource != "external-resource" {
		t.Fatalf("PostgreSQL external round-trip = %+v found=%v err=%v", externalLoaded, found, err)
	}
	approval := MCPMutationApprovalRow{
		ApprovalID:       mcpMutationTestHash("approval:postgres"),
		ProposalID:       proposal.ProposalID,
		ApprovedByUserID: 9,
		ApprovedBy:       "admin-1",
		ApprovedByIssuer: "test-issuer",
		ApprovedAt:       now.Add(time.Minute),
		ExpiresAt:        now.Add(30 * time.Minute),
		MaxExecutions:    1,
		LastUpdatedAt:    now.Add(time.Minute),
	}
	issued, inserted, err := IssueMCPMutationApproval(ctx, &handler, approval, proposal.ProposalSHA256, now.Add(time.Minute))
	if err != nil || !inserted || issued.State != "active" {
		t.Fatalf("PostgreSQL approval = %+v inserted=%v err=%v", issued, inserted, err)
	}
	revoked, transitioned, err := RevokeMCPMutationApproval(ctx, &handler, issued.ApprovalID, 9, "admin-1", "integration revoke", now.Add(2*time.Minute))
	if err != nil || !transitioned || revoked.State != "revoked" {
		t.Fatalf("PostgreSQL revocation = %+v transitioned=%v err=%v", revoked, transitioned, err)
	}
	list, err := ListMCPMutationProposals(ctx, &handler, MCPMutationProposalFilter{State: MCPMutationStateApproved, Limit: 10})
	if err != nil || len(list) != 1 {
		t.Fatalf("PostgreSQL proposal list = %+v err=%v", list, err)
	}
}
