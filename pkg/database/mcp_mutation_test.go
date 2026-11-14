package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"

	_ "github.com/mattn/go-sqlite3"
)

func mcpMutationTestHash(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

func stripMCPMutationSQLComments(statement string) string {
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

func applyMCPMutationSQLiteMigration(t *testing.T, db *sql.DB) {
	t.Helper()
	script, err := os.ReadFile("db_migrations/sqlite-mcp-mutation-v1.17.sqlite3")
	if err != nil {
		t.Fatalf("read SQLite MCP mutation migration: %v", err)
	}
	for _, statement := range strings.Split(string(script), ";") {
		statement = stripMCPMutationSQLComments(statement)
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

func issueMCPMutationApprovalForTest(ctx context.Context, db *Handler, approval MCPMutationApprovalRow, expectedProposalSHA256 string, now time.Time) (MCPMutationApprovalRow, bool, error) {
	return IssueMCPMutationApprovalAuthorized(ctx, db, approval, expectedProposalSHA256, now,
		func(context.Context, *sql.Tx, MCPMutationProposalRow) error { return nil })
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
	issued, inserted, err := issueMCPMutationApprovalForTest(ctx, &handler, approval, proposal.ProposalSHA256, now.Add(time.Minute))
	if err != nil || !inserted || issued.State != "active" {
		t.Fatalf("issue approval = %+v inserted=%v err=%v", issued, inserted, err)
	}
	stored, found, err := GetMCPMutationProposal(ctx, &handler, proposal.ProposalID)
	if err != nil || !found || stored.State != MCPMutationStateApproved || !stored.ApprovedAt.Valid {
		t.Fatalf("proposal after approval = %+v found=%v err=%v", stored, found, err)
	}
	duplicate := approval
	duplicate.ApprovalID = mcpMutationTestHash("approval:duplicate")
	existing, inserted, err := issueMCPMutationApprovalForTest(ctx, &handler, duplicate, proposal.ProposalSHA256, now.Add(2*time.Minute))
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
	reissued, inserted, err := issueMCPMutationApprovalForTest(ctx, &handler, replacement, proposal.ProposalSHA256, now.Add(4*time.Minute))
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
	if got := mcpMutationSQLiteAuditCount(t, dbFromHandler(t, handler), "mcp_mutation_approval_revoked"); got < 2 {
		t.Fatalf("approval revocation audit count = %d, want at least 2", got)
	}
}

func dbFromHandler(t *testing.T, handler Handler) *sql.DB {
	t.Helper()
	sqlite, ok := handler.(*SQLiteHandler)
	if !ok || sqlite == nil || sqlite.db == nil {
		t.Fatalf("handler is not a SQLite handler: %T", handler)
	}
	return sqlite.db
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
		State: MCPMutationStateApprovalRequired, Limit: 1, ViewerAdmin: true,
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
	if _, _, err := issueMCPMutationApprovalForTest(ctx, &handler, approval, first.ProposalSHA256, now); !errors.Is(err, ErrMCPMutationProposalExpired) {
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

func mcpMutationSQLiteApproval(t *testing.T, handler Handler, approvalID string) (MCPMutationApprovalRow, bool) {
	t.Helper()
	row, found, err := GetMCPMutationApproval(context.Background(), &handler, approvalID)
	if err != nil {
		t.Fatalf("get approval %s: %v", approvalID, err)
	}
	return row, found
}

func mcpMutationSQLiteAuditCount(t *testing.T, db *sql.DB, eventType string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM MCPMutationAudit WHERE event_type=?`, eventType).Scan(&count); err != nil {
		t.Fatalf("count audit %s: %v", eventType, err)
	}
	return count
}

func TestMCPMutationExpiredApprovalReapprovalReopensProposal(t *testing.T) {
	db, handler := newMCPMutationSQLiteTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	proposal := mcpMutationTestProposal("expiry-reapprove", "run-expiry", "call-expiry", now)
	if _, _, err := AdmitMCPMutationProposal(ctx, &handler, proposal, 2, 2); err != nil {
		t.Fatalf("admit expiry proposal: %v", err)
	}
	first := MCPMutationApprovalRow{
		ApprovalID: mcpMutationTestHash("approval:expiry:first"), ProposalID: proposal.ProposalID,
		ApprovedByUserID: 9, ApprovedBy: "admin-1", ApprovedByIssuer: "test-issuer",
		ApprovedAt: now.Add(time.Minute), ExpiresAt: now.Add(5 * time.Minute),
		MaxExecutions: 1, LastUpdatedAt: now.Add(time.Minute),
	}
	if _, inserted, err := issueMCPMutationApprovalForTest(ctx, &handler, first, proposal.ProposalSHA256, now.Add(time.Minute)); err != nil || !inserted {
		t.Fatalf("first approval inserted=%v err=%v", inserted, err)
	}
	active, found, err := GetActiveMCPMutationApprovalByProposal(ctx, &handler, proposal.ProposalSHA256, now.Add(2*time.Minute))
	if err != nil || !found || active.ApprovalID != first.ApprovalID {
		t.Fatalf("active approval before expiry = %+v found=%v err=%v", active, found, err)
	}
	if expiredActive, found, err := GetActiveMCPMutationApprovalByProposal(ctx, &handler, proposal.ProposalSHA256, now.Add(6*time.Minute)); err != nil || found {
		t.Fatalf("expired approval returned as active = %+v found=%v err=%v", expiredActive, found, err)
	}
	second := MCPMutationApprovalRow{
		ApprovalID: mcpMutationTestHash("approval:expiry:second"), ProposalID: proposal.ProposalID,
		ApprovedByUserID: 9, ApprovedBy: "admin-1", ApprovedByIssuer: "test-issuer",
		ApprovedAt: now.Add(6 * time.Minute), ExpiresAt: now.Add(20 * time.Minute),
		MaxExecutions: 1, LastUpdatedAt: now.Add(6 * time.Minute),
	}
	issued, inserted, err := issueMCPMutationApprovalForTest(ctx, &handler, second, proposal.ProposalSHA256, now.Add(6*time.Minute))
	if err != nil || !inserted || issued.ApprovalID != second.ApprovalID {
		t.Fatalf("reapproval after expiry = %+v inserted=%v err=%v", issued, inserted, err)
	}
	expired, found := mcpMutationSQLiteApproval(t, handler, first.ApprovalID)
	if !found || expired.State != "expired" {
		t.Fatalf("old approval after expiry = %+v found=%v", expired, found)
	}
	stored, found, err := GetMCPMutationProposal(ctx, &handler, proposal.ProposalID)
	if err != nil || !found || stored.State != MCPMutationStateApproved || !stored.ApprovedAt.Valid {
		t.Fatalf("proposal after reapproval = %+v found=%v err=%v", stored, found, err)
	}
	if got := mcpMutationSQLiteAuditCount(t, db, "mcp_mutation_approval_expired"); got != 1 {
		t.Fatalf("approval expiry audit count = %d", got)
	}
	if got := mcpMutationSQLiteAuditCount(t, db, "mcp_mutation_proposal_reopened"); got != 1 {
		t.Fatalf("proposal reopened audit count = %d", got)
	}
	active, found, err = GetActiveMCPMutationApprovalByProposal(ctx, &handler, proposal.ProposalSHA256, now.Add(7*time.Minute))
	if err != nil || !found || active.ApprovalID != second.ApprovalID {
		t.Fatalf("new active approval = %+v found=%v err=%v", active, found, err)
	}
}

func TestMCPMutationRevocationReopensProposal(t *testing.T) {
	_, handler := newMCPMutationSQLiteTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	proposal := mcpMutationTestProposal("revoke-reopen", "run-revoke", "call-revoke", now)
	if _, _, err := AdmitMCPMutationProposal(ctx, &handler, proposal, 2, 2); err != nil {
		t.Fatalf("admit revoke proposal: %v", err)
	}
	approval := MCPMutationApprovalRow{
		ApprovalID: mcpMutationTestHash("approval:revoke"), ProposalID: proposal.ProposalID,
		ApprovedByUserID: 9, ApprovedBy: "admin-1", ApprovedByIssuer: "test-issuer",
		ApprovedAt: now.Add(time.Minute), ExpiresAt: now.Add(30 * time.Minute),
		MaxExecutions: 1, LastUpdatedAt: now.Add(time.Minute),
	}
	if _, inserted, err := issueMCPMutationApprovalForTest(ctx, &handler, approval, proposal.ProposalSHA256, now.Add(time.Minute)); err != nil || !inserted {
		t.Fatalf("approve for revoke inserted=%v err=%v", inserted, err)
	}
	if _, transitioned, err := RevokeMCPMutationApproval(ctx, &handler, approval.ApprovalID, 9, "admin-1", "revoke", now.Add(2*time.Minute)); err != nil || !transitioned {
		t.Fatalf("revoke transitioned=%v err=%v", transitioned, err)
	}
	stored, found, err := GetMCPMutationProposal(ctx, &handler, proposal.ProposalID)
	if err != nil || !found || stored.State != MCPMutationStateApprovalRequired || stored.ApprovedAt.Valid {
		t.Fatalf("proposal after revoke = %+v found=%v err=%v", stored, found, err)
	}
	if _, found, err := GetActiveMCPMutationApprovalByProposal(ctx, &handler, proposal.ProposalSHA256, now.Add(2*time.Minute)); err != nil || found {
		t.Fatalf("active approval after revoke found=%v err=%v", found, err)
	}
	replacement := approval
	replacement.ApprovalID = mcpMutationTestHash("approval:revoke:replacement")
	replacement.ApprovedAt = now.Add(3 * time.Minute)
	replacement.LastUpdatedAt = replacement.ApprovedAt
	if _, inserted, err := issueMCPMutationApprovalForTest(ctx, &handler, replacement, proposal.ProposalSHA256, now.Add(3*time.Minute)); err != nil || !inserted {
		t.Fatalf("reapproval after revoke inserted=%v err=%v", inserted, err)
	}
}

func TestMCPMutationDenyClassifiesExpiredApproval(t *testing.T) {
	db, handler := newMCPMutationSQLiteTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	proposal := mcpMutationTestProposal("deny-expired-approval", "run-deny-expiry", "call-deny-expiry", now)
	if _, _, err := AdmitMCPMutationProposal(ctx, &handler, proposal, 2, 2); err != nil {
		t.Fatalf("admit deny-expiry proposal: %v", err)
	}
	approval := MCPMutationApprovalRow{
		ApprovalID: mcpMutationTestHash("approval:deny-expired"), ProposalID: proposal.ProposalID,
		ApprovedByUserID: 9, ApprovedBy: "admin-1", ApprovedByIssuer: "test-issuer",
		ApprovedAt: now.Add(time.Minute), ExpiresAt: now.Add(2 * time.Minute),
		MaxExecutions: 1, LastUpdatedAt: now.Add(time.Minute),
	}
	if _, inserted, err := issueMCPMutationApprovalForTest(ctx, &handler, approval, proposal.ProposalSHA256, now.Add(time.Minute)); err != nil || !inserted {
		t.Fatalf("approve before deny inserted=%v err=%v", inserted, err)
	}
	denied, transitioned, err := DenyMCPMutationProposal(ctx, &handler, proposal.ProposalID, 9, "admin-1", "deny after approval expiry", now.Add(3*time.Minute))
	if err != nil || !transitioned || denied.State != MCPMutationStateDenied {
		t.Fatalf("deny after approval expiry = %+v transitioned=%v err=%v", denied, transitioned, err)
	}
	storedApproval, found := mcpMutationSQLiteApproval(t, handler, approval.ApprovalID)
	if !found || storedApproval.State != "expired" {
		t.Fatalf("approval after deny = %+v found=%v", storedApproval, found)
	}
	var revoked int
	if err := db.QueryRow(`SELECT COUNT(*) FROM MCPMutationApprovals WHERE proposal_sha256=? AND state='revoked'`, proposal.ProposalSHA256).Scan(&revoked); err != nil || revoked != 0 {
		t.Fatalf("revoked approvals after expired-approval deny = %d err=%v", revoked, err)
	}
}

func assertMCPMutationQuotaConsistency(t *testing.T, db *sql.DB) {
	t.Helper()
	type runQuota struct {
		runID  string
		stored int
	}
	runRows, err := db.Query(`SELECT run_id, active_count FROM MCPMutationRunQuota`)
	if err != nil {
		t.Fatalf("read run quota rows: %v", err)
	}
	var runs []runQuota
	for runRows.Next() {
		var row runQuota
		if err := runRows.Scan(&row.runID, &row.stored); err != nil {
			_ = runRows.Close()
			t.Fatalf("scan run quota: %v", err)
		}
		runs = append(runs, row)
	}
	if err := runRows.Err(); err != nil {
		_ = runRows.Close()
		t.Fatalf("iterate run quota: %v", err)
	}
	_ = runRows.Close()
	for _, row := range runs {
		var actual int
		if err := db.QueryRow(`SELECT COUNT(*) FROM MCPMutationProposals
			WHERE run_id=? AND state IN ('proposed','approval_required','approved','dispatching','uncertain','reconciliation_required')`, row.runID).Scan(&actual); err != nil {
			t.Fatalf("count run quota %s: %v", row.runID, err)
		}
		if row.stored != actual {
			t.Fatalf("run %s quota stored=%d actual=%d", row.runID, row.stored, actual)
		}
	}
	type principalQuota struct {
		principal uint64
		stored    int
	}
	principalRows, err := db.Query(`SELECT principal_user_id, pending_count FROM MCPMutationPrincipalQuota`)
	if err != nil {
		t.Fatalf("read principal quota rows: %v", err)
	}
	var principals []principalQuota
	for principalRows.Next() {
		var row principalQuota
		if err := principalRows.Scan(&row.principal, &row.stored); err != nil {
			_ = principalRows.Close()
			t.Fatalf("scan principal quota: %v", err)
		}
		principals = append(principals, row)
	}
	if err := principalRows.Err(); err != nil {
		_ = principalRows.Close()
		t.Fatalf("iterate principal quota: %v", err)
	}
	_ = principalRows.Close()
	for _, row := range principals {
		var actual int
		if err := db.QueryRow(`SELECT COUNT(*) FROM MCPMutationProposals
			WHERE principal_user_id=? AND state IN ('proposed','approval_required','approved','dispatching')`, row.principal).Scan(&actual); err != nil {
			t.Fatalf("count principal quota %d: %v", row.principal, err)
		}
		if row.stored != actual {
			t.Fatalf("principal %d quota stored=%d actual=%d", row.principal, row.stored, actual)
		}
	}
}

func TestMCPMutationQuotaConsistencyAfterTransitions(t *testing.T) {
	db, handler := newMCPMutationSQLiteTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	first := mcpMutationTestProposal("consistency-1", "run-consistency-1", "call-consistency-1", now)
	second := mcpMutationTestProposal("consistency-2", "run-consistency-2", "call-consistency-2", now)
	if _, _, err := AdmitMCPMutationProposalAt(ctx, &handler, first, 2, 2, now); err != nil {
		t.Fatalf("admit consistency first: %v", err)
	}
	if _, _, err := AdmitMCPMutationProposalAt(ctx, &handler, second, 2, 2, now); err != nil {
		t.Fatalf("admit consistency second: %v", err)
	}
	assertMCPMutationQuotaConsistency(t, db)
	if _, _, err := DenyMCPMutationProposal(ctx, &handler, first.ProposalID, 9, "admin-1", "consistency deny", now.Add(time.Minute)); err != nil {
		t.Fatalf("deny consistency first: %v", err)
	}
	assertMCPMutationQuotaConsistency(t, db)
	if _, err := db.Exec(`UPDATE MCPMutationProposals SET created_at=?, expires_at=? WHERE proposal_id=?`,
		now.Add(-2*time.Minute), now.Add(-time.Minute), second.ProposalID); err != nil {
		t.Fatalf("expire consistency second: %v", err)
	}
	third := mcpMutationTestProposal("consistency-3", "run-consistency-2", "call-consistency-3", now.Add(2*time.Minute))
	if _, _, err := AdmitMCPMutationProposalAt(ctx, &handler, third, 2, 2, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("admit consistency third: %v", err)
	}
	assertMCPMutationQuotaConsistency(t, db)
}

func TestMCPMutationSQLiteRestartPreservesProposalAndQuota(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "mcp-mutation-restart.db")
	open := func() (*sql.DB, Handler) {
		db, err := sql.Open("sqlite3", dbPath)
		if err != nil {
			t.Fatalf("open restart SQLite: %v", err)
		}
		db.SetMaxOpenConns(1)
		if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
			t.Fatalf("enable restart foreign keys: %v", err)
		}
		return db, Handler(&SQLiteHandler{db: db, dbms: DBSQLiteStr})
	}
	db, handler := open()
	applyMCPMutationSQLiteMigration(t, db)
	now := time.Now().UTC()
	proposal := mcpMutationTestProposal("restart", "run-restart", "call-restart", now)
	if _, inserted, err := AdmitMCPMutationProposalAt(context.Background(), &handler, proposal, 2, 2, now); err != nil || !inserted {
		t.Fatalf("restart admission inserted=%v err=%v", inserted, err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close restart database: %v", err)
	}

	db, handler = open()
	defer db.Close()
	loaded, found, err := GetMCPMutationProposal(context.Background(), &handler, proposal.ProposalID)
	if err != nil || !found || loaded.ProposalSHA256 != proposal.ProposalSHA256 {
		t.Fatalf("restart proposal = %+v found=%v err=%v", loaded, found, err)
	}
	assertMCPMutationQuotaConsistency(t, db)
}

func TestMCPMutationCapabilityDetectsMissingIndex(t *testing.T) {
	db, handler := newMCPMutationSQLiteTestDB(t)
	if _, err := db.Exec(`DROP INDEX idx_mcp_proposals_pending`); err != nil {
		t.Fatalf("drop capability index: %v", err)
	}
	if err := CheckMCPMutationCapability(context.Background(), &handler); !errors.Is(err, ErrMCPMutationSchemaUnavailable) {
		t.Fatalf("missing index capability error = %v", err)
	}
}

func TestMCPMutationCapabilityDetectsMissingTable(t *testing.T) {
	db, handler := newMCPMutationSQLiteTestDB(t)
	if _, err := db.Exec(`DROP TABLE MCPMutationRunQuota`); err != nil {
		t.Fatalf("drop capability table: %v", err)
	}
	if err := CheckMCPMutationCapability(context.Background(), &handler); !errors.Is(err, ErrMCPMutationSchemaUnavailable) {
		t.Fatalf("missing table capability error = %v", err)
	}
}

func TestMCPMutationCapabilityDetectsMissingColumn(t *testing.T) {
	db, handler := newMCPMutationSQLiteTestDB(t)
	if _, err := db.Exec(`ALTER TABLE MCPMutationProposals RENAME COLUMN trace_id TO trace_id_old`); err != nil {
		t.Fatalf("rename capability column: %v", err)
	}
	if err := CheckMCPMutationCapability(context.Background(), &handler); !errors.Is(err, ErrMCPMutationSchemaUnavailable) {
		t.Fatalf("missing column capability error = %v", err)
	}
}

func TestMCPMutationCapabilityDetectsStaleMarker(t *testing.T) {
	db, handler := newMCPMutationSQLiteTestDB(t)
	if _, err := db.Exec(`UPDATE SchemaCapabilities SET checksum=? WHERE capability=?`,
		strings.Repeat("0", 64), MCPMutationCapability); err != nil {
		t.Fatalf("stale capability marker: %v", err)
	}
	if err := CheckMCPMutationCapability(context.Background(), &handler); !errors.Is(err, ErrMCPMutationSchemaUnavailable) {
		t.Fatalf("stale marker capability error = %v", err)
	}
}

func TestMCPMutationUniqueColumnsProbe(t *testing.T) {
	db, handler := newMCPMutationSQLiteTestDB(t)
	if _, err := db.Exec(`CREATE TABLE MCPMutationUniqueProbe (a TEXT, b TEXT)`); err != nil {
		t.Fatalf("create unique probe table: %v", err)
	}
	if tableUniqueColumnsExist(context.Background(), handler, "MCPMutationUniqueProbe", []string{"a", "b"}) {
		t.Fatalf("non-unique table reported a unique column set")
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX idx_mcp_unique_probe ON MCPMutationUniqueProbe(a,b)`); err != nil {
		t.Fatalf("create unique probe index: %v", err)
	}
	if !tableUniqueColumnsExist(context.Background(), handler, "MCPMutationUniqueProbe", []string{"a", "b"}) {
		t.Fatalf("unique column set was not detected")
	}
	if tableUniqueColumnsExist(context.Background(), handler, "MCPMutationUniqueProbe", []string{"a"}) {
		t.Fatalf("partial column set was accepted as exact")
	}
}

func TestMCPMutationCapabilityFailsOnSchema117WithoutCapability(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE DBSchemaVersion (
		schema_version_id INTEGER PRIMARY KEY AUTOINCREMENT,
		version TEXT NOT NULL UNIQUE,
		is_current INTEGER NOT NULL DEFAULT 1
	)`); err != nil {
		t.Fatalf("create schema version table: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO DBSchemaVersion (version, is_current) VALUES ('1.17', 1)`); err != nil {
		t.Fatalf("mark schema 1.17: %v", err)
	}
	handler := Handler(&SQLiteHandler{db: db, dbms: DBSQLiteStr})
	if err := CheckMCPMutationCapability(context.Background(), &handler); !errors.Is(err, ErrMCPMutationSchemaUnavailable) {
		t.Fatalf("1.17 without capability error = %v", err)
	}
}

func TestSQLiteMCPMutationForeignKeysAreRuntimeEnforced(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mcp-fk-runtime.db")
	var handler Handler = &SQLiteHandler{dbms: DBSQLiteStr}
	if err := handler.Connect(cfg.Config{Database: cfg.Database{DBName: path, TransactionTimeout: 30}}); err != nil {
		t.Fatalf("connect SQLite handler: %v", err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	sqlite := handler.(*SQLiteHandler)
	sqlite.db.SetMaxOpenConns(2)
	conn1, err := sqlite.db.Conn(ctx)
	if err != nil {
		t.Fatalf("open SQLite connection 1: %v", err)
	}
	defer conn1.Close()
	conn2, err := sqlite.db.Conn(ctx)
	if err != nil {
		t.Fatalf("open SQLite connection 2: %v", err)
	}
	defer conn2.Close()
	var fk1, fk2 int
	if err := conn1.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk1); err != nil {
		t.Fatalf("read connection 1 foreign_keys: %v", err)
	}
	if err := conn2.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk2); err != nil {
		t.Fatalf("read connection 2 foreign_keys: %v", err)
	}
	if fk1 != 1 || fk2 != 1 {
		t.Fatalf("runtime SQLite foreign_keys = %d/%d, want 1/1", fk1, fk2)
	}
	for _, statement := range []string{
		`CREATE TABLE fk_parent (id INTEGER PRIMARY KEY)`,
		`CREATE TABLE fk_child (id INTEGER PRIMARY KEY, parent_id INTEGER REFERENCES fk_parent(id))`,
		`INSERT INTO fk_parent (id) VALUES (1)`,
		`INSERT INTO fk_child (id, parent_id) VALUES (1, 1)`,
	} {
		if _, err := conn1.ExecContext(ctx, statement); err != nil {
			t.Fatalf("create/insert valid FK fixture: %v", err)
		}
	}
	if _, err := conn1.ExecContext(ctx, `INSERT INTO fk_child (id, parent_id) VALUES (2, 999)`); err == nil {
		t.Fatalf("orphan insert on connection 1 succeeded with runtime FK on")
	}
	if _, err := conn2.ExecContext(ctx, `INSERT INTO fk_child (id, parent_id) VALUES (3, 999)`); err == nil {
		t.Fatalf("orphan insert on connection 2 succeeded with runtime FK on")
	}
	_ = conn1.Close()
	_ = conn2.Close()
	applyMCPMutationSQLiteMigration(t, sqlite.db)
	var declaredFKs int
	if err := sqlite.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_foreign_key_list('MCPMutationApprovals')`).Scan(&declaredFKs); err != nil {
		t.Fatalf("read MCP approval foreign keys: %v", err)
	}
	if declaredFKs == 0 {
		t.Fatalf("MCP approval table declares no foreign keys")
	}
}

// TestMCPMutationLifecycleNeverCreatesDispatchAttempts proves that the inert
// proposal and approval lifecycle does not create a dispatch record. No
// executor, claim, retry or reconciliation path is wired to these tables.
func TestMCPMutationLifecycleNeverCreatesDispatchAttempts(t *testing.T) {
	db, handler := newMCPMutationSQLiteTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	proposal := mcpMutationTestProposal("dispatch-inert", "run-dispatch-inert", "call-dispatch-inert", now)
	if _, _, err := AdmitMCPMutationProposal(ctx, &handler, proposal, 2, 2); err != nil {
		t.Fatalf("admit inert proposal: %v", err)
	}
	approval := MCPMutationApprovalRow{
		ApprovalID:       mcpMutationTestHash("approval:dispatch-inert"),
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
	issued, inserted, err := issueMCPMutationApprovalForTest(ctx, &handler, approval, proposal.ProposalSHA256, now.Add(time.Minute))
	if err != nil || !inserted {
		t.Fatalf("issue inert approval = %+v inserted=%v err=%v", issued, inserted, err)
	}
	if _, _, err := DenyMCPMutationProposal(ctx, &handler, proposal.ProposalID, 9, "admin-1", "inert denial", now.Add(2*time.Minute)); err != nil {
		t.Fatalf("deny inert proposal: %v", err)
	}
	var attempts int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM MCPMutationDispatchAttempts`).Scan(&attempts); err != nil {
		t.Fatalf("count dispatch attempts: %v", err)
	}
	if attempts != 0 {
		t.Fatalf("dispatch attempts = %d, want 0", attempts)
	}
	var dispatching int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM MCPMutationProposals WHERE state IN ('dispatching','uncertain','reconciliation_required')`).Scan(&dispatching); err != nil {
		t.Fatalf("count active dispatch states: %v", err)
	}
	if dispatching != 0 {
		t.Fatalf("proposals entered a dispatch state: %d", dispatching)
	}
}
