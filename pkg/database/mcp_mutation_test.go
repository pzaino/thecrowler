package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func mcpMutationTestHash(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

func applyMCPMutationSQLiteMigration(t *testing.T, db *sql.DB) {
	t.Helper()
	script, err := os.ReadFile("db_migrations/sqlite-mcp-mutation-v1.17.sqlite3")
	if err != nil {
		t.Fatalf("read SQLite MCP mutation migration: %v", err)
	}
	for _, statement := range strings.Split(string(script), ";") {
		statement = strings.TrimSpace(statement)
		if statement == "" || strings.HasPrefix(strings.ToUpper(statement), "BEGIN IMMEDIATE") ||
			strings.EqualFold(statement, "COMMIT") || strings.HasPrefix(strings.ToUpper(statement), "PRAGMA") {
			continue
		}
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("apply SQLite MCP mutation statement: %v\n%s", err, statement)
		}
	}
}

func newMCPMutationSQLiteTestDB(t *testing.T) (*sql.DB, Handler) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		t.Fatalf("enable SQLite foreign keys: %v", err)
	}
	applyMCPMutationSQLiteMigration(t, db)
	return db, Handler(&SQLiteHandler{db: db, dbms: DBSQLiteStr})
}

func mcpMutationTestProposal(seed, runID, callID string, now time.Time) MCPMutationProposalRow {
	canonical := []byte(`{"resource":"fixture-resource"}`)
	return MCPMutationProposalRow{
		ProposalID:         mcpMutationTestHash("proposal:" + seed),
		ProposalSHA256:     mcpMutationTestHash("fingerprint:" + seed),
		IdentityVersion:    1,
		RunID:              runID,
		TraceID:            "trace-" + seed,
		EffectiveCallID:    callID,
		AgentID:            "agent-1",
		Owner:              "owner-1",
		PrincipalUserID:    7,
		PrincipalUsername:  "owner-1",
		PrincipalIssuer:    "test-issuer",
		ServerID:           "fixture",
		RawToolName:        "mutate_counter",
		ToolAlias:          "fixture__mutate_counter",
		Transport:          "stdio",
		TransportIdentity:  "fixture-command",
		SchemaSHA256:       mcpMutationTestHash("schema:" + seed),
		CanonicalArguments: canonical,
		ArgumentsSHA256:    mcpMutationTestHash(string(canonical)),
		PolicyID:           "test-policy",
		PolicyVersion:      "1",
		PolicySHA256:       mcpMutationTestHash("policy:" + seed),
		PolicySnapshot:     []byte(`{"policy_id":"test-policy"}`),
		SourceIDs:          []uint64{42},
		ApprovalRequired:   true,
		ExecutionBackend:   "none",
		State:              MCPMutationStateApprovalRequired,
		CreatedAt:          now.UTC(),
		LastUpdatedAt:      now.UTC(),
		ExpiresAt:          now.Add(time.Hour).UTC(),
	}
}

func TestMCPMutationCapabilityAndAdmission(t *testing.T) {
	_, handler := newMCPMutationSQLiteTestDB(t)
	ctx := context.Background()
	if err := CheckMCPMutationCapability(ctx, &handler); err != nil {
		t.Fatalf("capability check: %v", err)
	}
	now := time.Now().UTC()
	first := mcpMutationTestProposal("first", "run-1", "call-1", now)
	created, inserted, err := AdmitMCPMutationProposal(ctx, &handler, first, 2, 2)
	if err != nil || !inserted || created.ProposalID != first.ProposalID {
		t.Fatalf("first admission = %+v inserted=%v err=%v", created, inserted, err)
	}
	replay, inserted, err := AdmitMCPMutationProposal(ctx, &handler, first, 2, 2)
	if err != nil || inserted || replay.ProposalID != first.ProposalID {
		t.Fatalf("replay admission = %+v inserted=%v err=%v", replay, inserted, err)
	}
	changed := mcpMutationTestProposal("changed", "run-1", "call-1", now)
	if _, _, err := AdmitMCPMutationProposal(ctx, &handler, changed, 2, 2); !errors.Is(err, ErrMCPMutationProposalConflict) {
		t.Fatalf("changed same-call admission error = %v", err)
	}
	second := mcpMutationTestProposal("second", "run-1", "call-2", now)
	if _, _, err := AdmitMCPMutationProposal(ctx, &handler, second, 1, 2); !errors.Is(err, ErrMCPMutationQuotaReached) {
		t.Fatalf("per-run quota error = %v", err)
	}
	var active int
	if err := handler.QueryRowContext(ctx, `SELECT active_count FROM MCPMutationRunQuota WHERE run_id=?`, "run-1").Scan(&active); err != nil || active != 1 {
		t.Fatalf("run quota count = %d err=%v", active, err)
	}
	denied, transitioned, err := DenyMCPMutationProposal(ctx, &handler, first.ProposalID, 9, "admin-1", "test denial", now.Add(time.Minute))
	if err != nil || !transitioned || denied.State != MCPMutationStateDenied {
		t.Fatalf("deny = %+v transitioned=%v err=%v", denied, transitioned, err)
	}
	if err := handler.QueryRowContext(ctx, `SELECT active_count FROM MCPMutationRunQuota WHERE run_id=?`, "run-1").Scan(&active); err != nil || active != 0 {
		t.Fatalf("run quota after deny = %d err=%v", active, err)
	}
	if _, inserted, err := AdmitMCPMutationProposal(ctx, &handler, second, 1, 2); err != nil || !inserted {
		t.Fatalf("admission after deny inserted=%v err=%v", inserted, err)
	}
}

func TestMCPMutationApprovalLifecycle(t *testing.T) {
	_, handler := newMCPMutationSQLiteTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	proposal := mcpMutationTestProposal("approval", "run-approval", "call-approval", now)
	if _, _, err := AdmitMCPMutationProposal(ctx, &handler, proposal, 2, 2); err != nil {
		t.Fatalf("admit approval proposal: %v", err)
	}
	approval := MCPMutationApprovalRow{
		ApprovalID:       mcpMutationTestHash("approval:1"),
		ProposalID:       proposal.ProposalID,
		ProposalSHA256:   proposal.ProposalSHA256,
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
		t.Fatalf("issue approval = %+v inserted=%v err=%v", issued, inserted, err)
	}
	stored, found, err := GetMCPMutationProposal(ctx, &handler, proposal.ProposalID)
	if err != nil || !found || stored.State != MCPMutationStateApproved || !stored.ApprovedAt.Valid {
		t.Fatalf("proposal after approval = %+v found=%v err=%v", stored, found, err)
	}
	duplicate := approval
	duplicate.ApprovalID = mcpMutationTestHash("approval:duplicate")
	existing, inserted, err := IssueMCPMutationApproval(ctx, &handler, duplicate, proposal.ProposalSHA256, now.Add(2*time.Minute))
	if err != nil || inserted || existing.ApprovalID != issued.ApprovalID {
		t.Fatalf("duplicate approval = %+v inserted=%v err=%v", existing, inserted, err)
	}
	revoked, transitioned, err := RevokeMCPMutationApproval(ctx, &handler, issued.ApprovalID, 9, "admin-1", "test revoke", now.Add(3*time.Minute))
	if err != nil || !transitioned || revoked.State != "revoked" {
		t.Fatalf("revoke approval = %+v transitioned=%v err=%v", revoked, transitioned, err)
	}
	replacement := approval
	replacement.ApprovalID = mcpMutationTestHash("approval:replacement")
	replacement.ApprovedAt = now.Add(4 * time.Minute)
	replacement.LastUpdatedAt = replacement.ApprovedAt
	reissued, inserted, err := IssueMCPMutationApproval(ctx, &handler, replacement, proposal.ProposalSHA256, now.Add(4*time.Minute))
	if err != nil || !inserted || reissued.ApprovalID != replacement.ApprovalID {
		t.Fatalf("reissue approval = %+v inserted=%v err=%v", reissued, inserted, err)
	}
	denied, transitioned, err := DenyMCPMutationProposal(ctx, &handler, proposal.ProposalID, 9, "admin-1", "deny after reapproval", now.Add(5*time.Minute))
	if err != nil || !transitioned || denied.State != MCPMutationStateDenied {
		t.Fatalf("deny after reapproval = %+v transitioned=%v err=%v", denied, transitioned, err)
	}
	activeApproval, found, err := GetMCPMutationApproval(ctx, &handler, reissued.ApprovalID)
	if err != nil || !found || activeApproval.State != "revoked" {
		t.Fatalf("approval after denial = %+v found=%v err=%v", activeApproval, found, err)
	}
}

func TestMCPMutationApprovalExpiredProposalAndList(t *testing.T) {
	db, handler := newMCPMutationSQLiteTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	first := mcpMutationTestProposal("list-1", "run-list", "call-list-1", now)
	second := mcpMutationTestProposal("list-2", "run-list", "call-list-2", now.Add(-time.Minute))
	if _, _, err := AdmitMCPMutationProposal(ctx, &handler, first, 4, 4); err != nil {
		t.Fatalf("admit first list proposal: %v", err)
	}
	if _, _, err := AdmitMCPMutationProposal(ctx, &handler, second, 4, 4); err != nil {
		t.Fatalf("admit second list proposal: %v", err)
	}
	list, err := ListMCPMutationProposals(ctx, &handler, MCPMutationProposalFilter{
		State: MCPMutationStateApprovalRequired, Limit: 1,
	})
	if err != nil || len(list) != 1 || list[0].ProposalID != first.ProposalID {
		t.Fatalf("bounded list = %+v err=%v", list, err)
	}
	if _, err := db.Exec(`UPDATE MCPMutationProposals SET created_at=?, expires_at=? WHERE proposal_id=?`,
		now.Add(-2*time.Minute), now.Add(-time.Minute), first.ProposalID); err != nil {
		t.Fatalf("expire test proposal: %v", err)
	}
	approval := MCPMutationApprovalRow{
		ApprovalID: mcpMutationTestHash("approval:expired-proposal"),
		ProposalID: first.ProposalID, ApprovedByUserID: 9, ApprovedBy: "admin-1",
		ApprovedByIssuer: "test-issuer", ApprovedAt: now, ExpiresAt: now.Add(10 * time.Minute),
		MaxExecutions: 1, LastUpdatedAt: now,
	}
	if _, _, err := IssueMCPMutationApproval(ctx, &handler, approval, first.ProposalSHA256, now); !errors.Is(err, ErrMCPMutationProposalExpired) {
		t.Fatalf("expired proposal approval error = %v", err)
	}
	expired, found, err := GetMCPMutationProposal(ctx, &handler, first.ProposalID)
	if err != nil || !found || expired.State != MCPMutationStateExpired {
		t.Fatalf("expired proposal = %+v found=%v err=%v", expired, found, err)
	}
	var active int
	if err := db.QueryRow(`SELECT active_count FROM MCPMutationRunQuota WHERE run_id=?`, "run-list").Scan(&active); err != nil || active != 1 {
		t.Fatalf("run quota after expiry = %d err=%v", active, err)
	}
}

func TestMCPMutationCapabilityFailsClosedWithoutObjects(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	defer db.Close()
	handler := Handler(&SQLiteHandler{db: db, dbms: DBSQLiteStr})
	if err := CheckMCPMutationCapability(context.Background(), &handler); !errors.Is(err, ErrMCPMutationSchemaUnavailable) {
		t.Fatalf("empty schema capability error = %v", err)
	}
}

func TestMCPMutationSQLiteMigrationIsRepeatable(t *testing.T) {
	db, _ := newMCPMutationSQLiteTestDB(t)
	applyMCPMutationSQLiteMigration(t, db)
	var capabilities int
	if err := db.QueryRow(`SELECT COUNT(*) FROM SchemaCapabilities WHERE capability=?`, MCPMutationCapability).Scan(&capabilities); err != nil || capabilities != 1 {
		t.Fatalf("capability count = %d err=%v", capabilities, err)
	}
}

func TestMCPMutationAdmissionReleasesExpiredQuota(t *testing.T) {
	db, handler := newMCPMutationSQLiteTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	first := mcpMutationTestProposal("sweep-first", "run-sweep", "call-sweep-1", now)
	if _, inserted, err := AdmitMCPMutationProposal(ctx, &handler, first, 1, 1); err != nil || !inserted {
		t.Fatalf("admit first sweep proposal inserted=%v err=%v", inserted, err)
	}
	if _, err := db.Exec(`UPDATE MCPMutationProposals SET created_at=?, expires_at=? WHERE proposal_id=?`,
		now.Add(-2*time.Minute), now.Add(-time.Minute), first.ProposalID); err != nil {
		t.Fatalf("expire sweep proposal: %v", err)
	}
	second := mcpMutationTestProposal("sweep-second", "run-sweep", "call-sweep-2", now)
	if _, inserted, err := AdmitMCPMutationProposal(ctx, &handler, second, 1, 1); err != nil || !inserted {
		t.Fatalf("admit after expiry sweep inserted=%v err=%v", inserted, err)
	}
	expired, found, err := GetMCPMutationProposal(ctx, &handler, first.ProposalID)
	if err != nil || !found || expired.State != MCPMutationStateExpired {
		t.Fatalf("swept proposal = %+v found=%v err=%v", expired, found, err)
	}
	var active int
	if err := db.QueryRow(`SELECT active_count FROM MCPMutationRunQuota WHERE run_id=?`, "run-sweep").Scan(&active); err != nil || active != 1 {
		t.Fatalf("run quota after sweep = %d err=%v", active, err)
	}
}
