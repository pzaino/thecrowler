package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	agentpkg "github.com/pzaino/thecrowler/pkg/agent"
	auth "github.com/pzaino/thecrowler/pkg/auth"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

var (
	mcpAPITestPolicy    agentpkg.MCPMutationPolicy
	mcpAPITestInventory cfg.MCPConfig
	mcpAPITestAgent     agentpkg.AgentIdentity
)

func setupMCPMutationApprovalFactory(t *testing.T) {
	t.Helper()
	previous := mcpMutationApprovalStoreFactory
	mcpMutationApprovalStoreFactory = func(db *cdb.Handler, maxLifetime time.Duration) (*agentpkg.DatabaseMCPMutationApprovalStore, error) {
		store, err := agentpkg.NewDatabaseMCPMutationApprovalStore(db, maxLifetime)
		if err != nil {
			return nil, err
		}
		store.Policy = func() agentpkg.MCPMutationPolicy { return mcpAPITestPolicy }
		store.Inventory = func() cfg.MCPConfig { return mcpAPITestInventory }
		store.Agent = func(agentID string) (agentpkg.AgentIdentity, bool) {
			if agentID != mcpAPITestAgent.AgentID {
				return agentpkg.AgentIdentity{}, false
			}
			return mcpAPITestAgent, true
		}
		return store, nil
	}
	t.Cleanup(func() { mcpMutationApprovalStoreFactory = previous })
}

func mcpMutationAPITestHash(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

func stripMCPMutationAPISQLComments(statement string) string {
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

func applyMCPMutationAPIMigration(t *testing.T, handler cdb.Handler) {
	t.Helper()
	testHandler, ok := handler.(*sourceAPITestHandler)
	if !ok {
		t.Fatalf("unexpected test handler type %T", handler)
	}
	script, err := os.ReadFile("../../pkg/database/db_migrations/sqlite-mcp-mutation-v1.17.sqlite3")
	if err != nil {
		t.Fatalf("read MCP mutation migration: %v", err)
	}
	for _, statement := range strings.Split(string(script), ";") {
		statement = stripMCPMutationAPISQLComments(statement)
		if statement == "" || strings.HasPrefix(strings.ToUpper(statement), "BEGIN IMMEDIATE") ||
			strings.EqualFold(statement, "COMMIT") || strings.HasPrefix(strings.ToUpper(statement), "PRAGMA") {
			continue
		}
		if _, err := testHandler.db.Exec(statement); err != nil {
			t.Fatalf("apply MCP mutation statement: %v\n%s", err, statement)
		}
	}
}

func mcpMutationAPISeedProposal(t *testing.T, handler cdb.Handler, seed, runID, callID string, sourceIDs []uint64, externalResource string, now time.Time) agentpkg.MCPMutationProposal {
	t.Helper()
	return mcpMutationAPISeedProposalForOwner(t, handler, seed, runID, callID, sourceIDs, externalResource, 7, "owner-1", now)
}

func mcpMutationAPISeedProposalForOwner(t *testing.T, handler cdb.Handler, seed, runID, callID string, sourceIDs []uint64, externalResource string, ownerID uint64, ownerName string, now time.Time) agentpkg.MCPMutationProposal {
	t.Helper()
	var arguments []byte
	operation := agentpkg.MCPMutationOperationPolicy{
		ServerID:             "fixture",
		ToolName:             "mutate_counter",
		ToolAlias:            agentpkg.MCPToolAlias("fixture", "mutate_counter"),
		AllowedAgents:        []string{"agent-1"},
		RequiredCapabilities: []string{"tool_execution"},
		MinTrustLevel:        "trusted",
		ProposalTTL:          time.Hour,
		MaxProposalsPerRun:   16,
		MaxPendingProposals:  100,
		MaxInputBytes:        4096,
		MaxOutputBytes:       4096,
		RequireApproval:      true,
		ExecutionBackend:     "none",
		AllowedTransports:    []string{"stdio"},
		AuditRequired:        true,
	}
	sourceArguments := map[string]any(nil)
	if len(sourceIDs) > 0 {
		arguments = []byte(fmt.Sprintf(`{"source_id":%d}`, sourceIDs[0]))
		sourceArguments = map[string]any{"source_id": sourceIDs[0]}
		operation.AllowedSources = append([]uint64(nil), sourceIDs...)
		operation.RequiredArgumentFields = []string{"source_id"}
		operation.AllowedArgumentFields = []string{"source_id"}
	} else {
		arguments = []byte(fmt.Sprintf(`{"resource":%q}`, externalResource))
		sourceArguments = map[string]any{"resource": externalResource}
		operation.AllowedResources = []string{externalResource}
		operation.RequiredArgumentFields = []string{"resource"}
		operation.AllowedArgumentFields = []string{"resource"}
	}
	policy := agentpkg.MCPMutationPolicy{
		Enabled: true, PolicyID: "test-policy", Version: "1",
		Operations: []agentpkg.MCPMutationOperationPolicy{operation},
	}
	policySnapshot, err := json.Marshal(policy)
	if err != nil {
		t.Fatalf("marshal API test policy: %v", err)
	}
	proposal := agentpkg.MCPMutationProposal{
		ProposalID:         mcpMutationAPITestHash("proposal:" + seed),
		IdentityVersion:    agentpkg.MCPMutationIdentityVersion,
		State:              agentpkg.MCPProposalApprovalRequired,
		RunID:              runID,
		TraceID:            "trace-" + seed,
		EffectiveCallID:    callID,
		AgentID:            "agent-1",
		Owner:              ownerName,
		Principal:          agentpkg.MCPMutationPrincipal{UserID: ownerID, Username: ownerName, Issuer: "crowler"},
		ServerID:           "fixture",
		RawToolName:        "mutate_counter",
		ToolAlias:          agentpkg.MCPToolAlias("fixture", "mutate_counter"),
		Transport:          "stdio",
		TransportIdentity:  "fixture-command",
		SchemaSHA256:       mcpMutationAPITestHash("schema:" + seed),
		Arguments:          sourceArguments,
		CanonicalArguments: arguments,
		ArgumentsSHA256:    mcpMutationAPITestHash(string(arguments)),
		PolicyID:           policy.PolicyID,
		PolicyVersion:      policy.Version,
		PolicySHA256:       policy.SHA256(),
		PolicySnapshot:     policySnapshot,
		SourceIDs:          append([]uint64(nil), sourceIDs...),
		ExternalResource:   externalResource,
		ApprovalRequired:   true,
		ExecutionBackend:   "none",
		CreatedAt:          now.UTC(),
		LastUpdatedAt:      now.UTC(),
		ExpiresAt:          now.Add(time.Hour).UTC(),
	}
	mcpAPITestPolicy = policy
	mcpAPITestInventory = cfg.MCPConfig{Servers: []cfg.MCPServerConfig{{
		ID: "fixture", Enabled: true, Transport: "stdio", Command: "fixture-command",
		AllowedAgents: []string{"agent-1"},
		ApprovedTools: []cfg.MCPApprovedTool{{
			Name: "mutate_counter", Effect: "mutation", AllowMutationProposal: true,
			RequiredCapabilities: []string{"tool_execution"}, AllowedSources: []uint64{42, 43},
		}},
	}}}
	mcpAPITestAgent = agentpkg.AgentIdentity{
		AgentID: "agent-1", Owner: ownerName, TrustLevel: "trusted",
		Capabilities: []string{"tool_execution"},
	}
	proposal.ProposalSHA256 = agentpkg.MCPMutationProposalFingerprint(proposal)
	store, err := agentpkg.NewDatabaseMCPMutationProposalStore(&handler)
	if err != nil {
		t.Fatalf("create durable API proposal store: %v", err)
	}
	created, err := store.Admit(context.Background(), proposal, 16, 16)
	if err != nil {
		t.Fatalf("seed durable MCP mutation proposal %s: %v", seed, err)
	}
	return created
}

func callMCPMutationHandler(t *testing.T, handler http.HandlerFunc, method, path, body string, identity *auth.Identity) *httptest.ResponseRecorder {
	t.Helper()
	request := sourceMutationRequest(method, path, body, identity)
	if marker := "/proposals/"; strings.Contains(path, marker) {
		rest := path[strings.Index(path, marker)+len(marker):]
		proposalID := strings.SplitN(rest, "/", 2)[0]
		request.SetPathValue("proposal_id", proposalID)
	}
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	return recorder
}

func mcpMutationApproveBody(fingerprint string, expiresAt time.Time) string {
	return fmt.Sprintf(`{"proposal_sha256":%q,"identity_version":1,"max_executions":1,
		"requested_expiry":%q,"reason":"operator review"}`, fingerprint, expiresAt.Format(time.RFC3339Nano))
}

func TestMCPMutationApprovalAPIApproveRevokeDeny(t *testing.T) {
	handler := setupSourceOwnershipTest(t)
	applyMCPMutationAPIMigration(t, handler)
	insertOwnedSource(t, handler, 42, "https://mcp-owned.example/", 7)
	now := time.Now().UTC()
	owner := sourceOwnershipIdentity(7, "owner-1", "user")
	other := sourceOwnershipIdentity(8, "owner-2", "user")
	admin := sourceOwnershipIdentity(9, "admin-1", "admin")

	first := mcpMutationAPISeedProposal(t, handler, "api-first", "run-api-1", "call-api-1", []uint64{42}, "", now)
	approve := callMCPMutationHandler(t, mcpMutationApproveHandler, http.MethodPost,
		"/v1/console/mcp/mutations/proposals/"+first.ProposalID+"/approve",
		mcpMutationApproveBody(first.ProposalSHA256, now.Add(30*time.Minute)), &owner)
	if approve.Code != http.StatusOK {
		t.Fatalf("approve status %d: %s", approve.Code, approve.Body.String())
	}
	var approved map[string]any
	if err := json.Unmarshal(approve.Body.Bytes(), &approved); err != nil {
		t.Fatalf("decode approve response: %v", err)
	}
	approval, _ := approved["approval"].(map[string]any)
	approvalID, _ := approval["approval_id"].(string)
	if len(approvalID) != 64 {
		t.Fatalf("unexpected approval response: %s", approve.Body.String())
	}
	duplicate := callMCPMutationHandler(t, mcpMutationApproveHandler, http.MethodPost,
		"/v1/console/mcp/mutations/proposals/"+first.ProposalID+"/approve",
		mcpMutationApproveBody(first.ProposalSHA256, now.Add(30*time.Minute)), &owner)
	if duplicate.Code != http.StatusOK {
		t.Fatalf("duplicate approve status %d: %s", duplicate.Code, duplicate.Body.String())
	}
	var duplicateResponse map[string]any
	if err := json.Unmarshal(duplicate.Body.Bytes(), &duplicateResponse); err != nil {
		t.Fatalf("decode duplicate approval: %v", err)
	}
	duplicateApproval, _ := duplicateResponse["approval"].(map[string]any)
	if duplicateResponse["reused"] != true || duplicateApproval["approval_id"] != approvalID {
		t.Fatalf("duplicate approval was not idempotent: %s", duplicate.Body.String())
	}
	var approvalCount int
	if err := handler.(*sourceAPITestHandler).db.QueryRow(
		`SELECT COUNT(*) FROM MCPMutationApprovals WHERE proposal_id=?`, first.ProposalID).Scan(&approvalCount); err != nil || approvalCount != 1 {
		t.Fatalf("approval rows after duplicate = %d err=%v", approvalCount, err)
	}
	stored, found, err := cdb.GetMCPMutationProposal(context.Background(), &handler, first.ProposalID)
	if err != nil || !found || stored.State != cdb.MCPMutationStateApproved {
		t.Fatalf("proposal after approve = %+v found=%v err=%v", stored, found, err)
	}

	crossTenant := callMCPMutationHandler(t, mcpMutationApproveHandler, http.MethodPost,
		"/v1/console/mcp/mutations/proposals/"+first.ProposalID+"/approve",
		mcpMutationApproveBody(first.ProposalSHA256, now.Add(30*time.Minute)), &other)
	if crossTenant.Code != http.StatusForbidden {
		t.Fatalf("cross-tenant approve status %d: %s", crossTenant.Code, crossTenant.Body.String())
	}
	if strings.Contains(crossTenant.Body.String(), "source 42") || strings.Contains(crossTenant.Body.String(), "owned by another") {
		t.Fatalf("approval denial leaked source ownership detail: %s", crossTenant.Body.String())
	}

	revoke := callMCPMutationHandler(t, mcpMutationRevokeHandler, http.MethodPost,
		"/v1/console/mcp/mutations/proposals/"+first.ProposalID+"/revoke",
		fmt.Sprintf(`{"approval_id":%q,"proposal_sha256":%q,"reason":"owner revoke"}`, approvalID, first.ProposalSHA256), &owner)
	if revoke.Code != http.StatusOK {
		t.Fatalf("revoke status %d: %s", revoke.Code, revoke.Body.String())
	}
	approvalRow, found, err := cdb.GetMCPMutationApproval(context.Background(), &handler, approvalID)
	if err != nil || !found || approvalRow.State != "revoked" {
		t.Fatalf("approval after revoke = %+v found=%v err=%v", approvalRow, found, err)
	}

	second := mcpMutationAPISeedProposal(t, handler, "api-second", "run-api-2", "call-api-2", []uint64{42}, "", now)
	approveSecond := callMCPMutationHandler(t, mcpMutationApproveHandler, http.MethodPost,
		"/v1/console/mcp/mutations/proposals/"+second.ProposalID+"/approve",
		mcpMutationApproveBody(second.ProposalSHA256, now.Add(30*time.Minute)), &owner)
	if approveSecond.Code != http.StatusOK {
		t.Fatalf("approve second status %d: %s", approveSecond.Code, approveSecond.Body.String())
	}
	deny := callMCPMutationHandler(t, mcpMutationDenyHandler, http.MethodPost,
		"/v1/console/mcp/mutations/proposals/"+second.ProposalID+"/deny",
		fmt.Sprintf(`{"proposal_sha256":%q,"reason":"owner denied"}`, second.ProposalSHA256), &owner)
	if deny.Code != http.StatusOK {
		t.Fatalf("deny status %d: %s", deny.Code, deny.Body.String())
	}
	denied, found, err := cdb.GetMCPMutationProposal(context.Background(), &handler, second.ProposalID)
	if err != nil || !found || denied.State != cdb.MCPMutationStateDenied {
		t.Fatalf("proposal after deny = %+v found=%v err=%v", denied, found, err)
	}
	active, found, err := cdb.GetActiveMCPMutationApprovalByProposal(context.Background(), &handler, second.ProposalSHA256, time.Now().UTC())
	if err != nil || found {
		t.Fatalf("active approval after deny = %+v found=%v err=%v", active, found, err)
	}

	external := mcpMutationAPISeedProposal(t, handler, "api-external", "run-api-3", "call-api-3", nil, "external-resource", now)
	externalOwner := callMCPMutationHandler(t, mcpMutationApproveHandler, http.MethodPost,
		"/v1/console/mcp/mutations/proposals/"+external.ProposalID+"/approve",
		mcpMutationApproveBody(external.ProposalSHA256, now.Add(30*time.Minute)), &owner)
	if externalOwner.Code != http.StatusForbidden {
		t.Fatalf("external owner approve status %d: %s", externalOwner.Code, externalOwner.Body.String())
	}
	externalAdmin := callMCPMutationHandler(t, mcpMutationApproveHandler, http.MethodPost,
		"/v1/console/mcp/mutations/proposals/"+external.ProposalID+"/approve",
		mcpMutationApproveBody(external.ProposalSHA256, now.Add(30*time.Minute)), &admin)
	if externalAdmin.Code != http.StatusOK {
		t.Fatalf("external admin approve status %d: %s", externalAdmin.Code, externalAdmin.Body.String())
	}

	config.API.Auth = cfg.AuthConfig{Enabled: false}
	unauthenticated := callMCPMutationHandler(t, mcpMutationListHandler, http.MethodGet,
		"/v1/console/mcp/mutations/proposals", "", &owner)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("auth-disabled list status %d: %s", unauthenticated.Code, unauthenticated.Body.String())
	}
}

func TestMCPMutationApprovalAPIListDetailAndAttempts(t *testing.T) {
	handler := setupSourceOwnershipTest(t)
	applyMCPMutationAPIMigration(t, handler)
	insertOwnedSource(t, handler, 42, "https://mcp-list.example/", 7)
	now := time.Now().UTC()
	owner := sourceOwnershipIdentity(7, "owner-1", "user")
	admin := sourceOwnershipIdentity(9, "admin-1", "admin")
	first := mcpMutationAPISeedProposal(t, handler, "api-list-1", "run-list-api", "call-list-1", []uint64{42}, "", now)
	mcpMutationAPISeedProposal(t, handler, "api-list-2", "run-list-api", "call-list-2", []uint64{42}, "", now.Add(-time.Minute))
	third := mcpMutationAPISeedProposal(t, handler, "api-list-3", "run-list-api", "call-list-3", []uint64{42}, "", now.Add(-2*time.Minute))

	list := callMCPMutationHandler(t, mcpMutationListHandler, http.MethodGet,
		"/v1/console/mcp/mutations/proposals?limit=2", "", &admin)
	if list.Code != http.StatusOK {
		t.Fatalf("list status %d: %s", list.Code, list.Body.String())
	}
	var page mcpMutationListResponse
	if err := json.Unmarshal(list.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(page.Items) != 2 || page.NextCursor == "" {
		t.Fatalf("unexpected list page: %+v", page)
	}
	if page.Items[0].ProposalID == "" || len(page.Items[0].ArgumentKeys) == 0 {
		t.Fatalf("unexpected list item: %+v", page.Items[0])
	}
	if strings.Contains(list.Body.String(), "fixture-resource") {
		t.Fatalf("list response leaked raw argument values: %s", list.Body.String())
	}
	next := callMCPMutationHandler(t, mcpMutationListHandler, http.MethodGet,
		"/v1/console/mcp/mutations/proposals?limit=2&cursor="+page.NextCursor, "", &admin)
	if next.Code != http.StatusOK {
		t.Fatalf("next page status %d: %s", next.Code, next.Body.String())
	}
	var page2 mcpMutationListResponse
	if err := json.Unmarshal(next.Body.Bytes(), &page2); err != nil {
		t.Fatalf("decode next page: %v", err)
	}
	if len(page2.Items) != 1 || page2.Items[0].ProposalID != third.ProposalID {
		t.Fatalf("unexpected next page: %+v", page2)
	}

	detail := callMCPMutationHandler(t, mcpMutationDetailHandler, http.MethodGet,
		"/v1/console/mcp/mutations/proposals/"+first.ProposalID, "", &owner)
	if detail.Code != http.StatusOK {
		t.Fatalf("detail status %d: %s", detail.Code, detail.Body.String())
	}
	var detailResponse mcpMutationProposalResponse
	if err := json.Unmarshal(detail.Body.Bytes(), &detailResponse); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detailResponse.ProposalID != first.ProposalID || detailResponse.ArgumentKeys == nil {
		t.Fatalf("unexpected detail: %+v", detailResponse)
	}
	attempts := callMCPMutationHandler(t, mcpMutationAttemptsHandler, http.MethodGet,
		"/v1/console/mcp/mutations/proposals/"+first.ProposalID+"/attempts", "", &owner)
	if attempts.Code != http.StatusOK || !strings.Contains(attempts.Body.String(), `"dispatch_enabled":false`) {
		t.Fatalf("attempts response %d: %s", attempts.Code, attempts.Body.String())
	}
}

func TestMCPMutationApprovalAPIDisabledProposalPrincipal(t *testing.T) {
	handler := setupSourceOwnershipTest(t)
	applyMCPMutationAPIMigration(t, handler)
	insertOwnedSource(t, handler, 42, "https://mcp-disabled-principal.example/", 7)
	now := time.Now().UTC()
	admin := sourceOwnershipIdentity(9, "admin-1", "admin")
	proposal := mcpMutationAPISeedProposal(t, handler, "api-disabled-principal", "run-disabled-principal", "call-disabled-principal", []uint64{42}, "", now)
	db := handler.(*sourceAPITestHandler).db
	if _, err := db.Exec(`UPDATE Users SET disabled=TRUE WHERE user_id=7`); err != nil {
		t.Fatalf("disable proposal principal: %v", err)
	}
	approved := callMCPMutationHandler(t, mcpMutationApproveHandler, http.MethodPost,
		"/v1/console/mcp/mutations/proposals/"+proposal.ProposalID+"/approve",
		mcpMutationApproveBody(proposal.ProposalSHA256, now.Add(30*time.Minute)), &admin)
	if approved.Code != http.StatusForbidden {
		t.Fatalf("disabled proposal principal approve status %d: %s", approved.Code, approved.Body.String())
	}
}

func TestMCPMutationExternalResourceRedaction(t *testing.T) {
	got := mcpMutationSafeResource("https://user:secret@example.com/path?token=secret#fragment")
	if strings.Contains(got, "secret") || strings.Contains(got, "token") || strings.Contains(got, "fragment") {
		t.Fatalf("external resource redaction leaked sensitive components: %q", got)
	}
	if got != "https://example.com/path" {
		t.Fatalf("unexpected redacted resource: %q", got)
	}
}

func TestMCPMutationApprovalAPIRejectsCorruptProposal(t *testing.T) {
	handler := setupSourceOwnershipTest(t)
	applyMCPMutationAPIMigration(t, handler)
	insertOwnedSource(t, handler, 42, "https://mcp-corrupt.example/", 7)
	now := time.Now().UTC()
	owner := sourceOwnershipIdentity(7, "owner-1", "user")
	proposal := mcpMutationAPISeedProposal(t, handler, "api-corrupt", "run-corrupt", "call-corrupt", []uint64{42}, "", now)
	db := handler.(*sourceAPITestHandler).db
	bad := []byte(`{"source_id":`)
	if _, err := db.Exec(`UPDATE MCPMutationProposals SET canonical_arguments=?, arguments_sha256=? WHERE proposal_id=?`,
		bad, mcpMutationAPITestHash(string(bad)), proposal.ProposalID); err != nil {
		t.Fatalf("corrupt durable proposal: %v", err)
	}
	approved := callMCPMutationHandler(t, mcpMutationApproveHandler, http.MethodPost,
		"/v1/console/mcp/mutations/proposals/"+proposal.ProposalID+"/approve",
		mcpMutationApproveBody(proposal.ProposalSHA256, now.Add(30*time.Minute)), &owner)
	if approved.Code != http.StatusServiceUnavailable {
		t.Fatalf("corrupt proposal approve status %d: %s", approved.Code, approved.Body.String())
	}
	var approvals int
	if err := db.QueryRow(`SELECT COUNT(*) FROM MCPMutationApprovals WHERE proposal_id=?`, proposal.ProposalID).Scan(&approvals); err != nil || approvals != 0 {
		t.Fatalf("approvals after corrupt refusal = %d err=%v", approvals, err)
	}
}

func TestMCPMutationApprovalAPIFailsClosedWithoutSchema(t *testing.T) {
	_ = setupSourceOwnershipTest(t)
	owner := sourceOwnershipIdentity(7, "owner-1", "user")
	list := callMCPMutationHandler(t, mcpMutationListHandler, http.MethodGet,
		"/v1/console/mcp/mutations/proposals", "", &owner)
	if list.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing schema list status %d: %s", list.Code, list.Body.String())
	}
}

func TestMCPMutationApprovalAPIMethodAndPrecondition(t *testing.T) {
	handler := setupSourceOwnershipTest(t)
	applyMCPMutationAPIMigration(t, handler)
	insertOwnedSource(t, handler, 42, "https://mcp-precondition.example/", 7)
	now := time.Now().UTC()
	owner := sourceOwnershipIdentity(7, "owner-1", "user")
	proposal := mcpMutationAPISeedProposal(t, handler, "api-precondition", "run-precondition", "call-precondition", []uint64{42}, "", now)
	badMethod := callMCPMutationHandler(t, mcpMutationApproveHandler, http.MethodGet,
		"/v1/console/mcp/mutations/proposals/"+proposal.ProposalID+"/approve", "", &owner)
	if badMethod.Code != http.StatusMethodNotAllowed {
		t.Fatalf("wrong method status %d", badMethod.Code)
	}
	stale := callMCPMutationHandler(t, mcpMutationApproveHandler, http.MethodPost,
		"/v1/console/mcp/mutations/proposals/"+proposal.ProposalID+"/approve",
		`{"proposal_sha256":"`+strings.Repeat("0", 64)+`","identity_version":1,"max_executions":1}`, &owner)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale fingerprint status %d: %s", stale.Code, stale.Body.String())
	}
}

func TestMCPMutationApprovalAPIListTenantPagination(t *testing.T) {
	handler := setupSourceOwnershipTest(t)
	applyMCPMutationAPIMigration(t, handler)
	insertOwnedSource(t, handler, 42, "https://mcp-owner-7.example/", 7)
	insertOwnedSource(t, handler, 43, "https://mcp-owner-8.example/", 8)
	now := time.Now().UTC()
	owner := sourceOwnershipIdentity(7, "owner-1", "user")
	admin := sourceOwnershipIdentity(9, "admin-1", "admin")

	otherNew := mcpMutationAPISeedProposalForOwner(t, handler, "tenant-other-new", "run-tenant", "call-other-new", []uint64{43}, "", 8, "owner-2", now)
	ownOne := mcpMutationAPISeedProposalForOwner(t, handler, "tenant-own-1", "run-tenant", "call-own-1", []uint64{42}, "", 7, "owner-1", now.Add(-time.Minute))
	otherMid := mcpMutationAPISeedProposalForOwner(t, handler, "tenant-other-mid", "run-tenant", "call-other-mid", []uint64{43}, "", 8, "owner-2", now.Add(-2*time.Minute))
	ownTwo := mcpMutationAPISeedProposalForOwner(t, handler, "tenant-own-2", "run-tenant", "call-own-2", []uint64{42}, "", 7, "owner-1", now.Add(-3*time.Minute))
	otherOld := mcpMutationAPISeedProposalForOwner(t, handler, "tenant-other-old", "run-tenant", "call-other-old", []uint64{43}, "", 8, "owner-2", now.Add(-4*time.Minute))
	external := mcpMutationAPISeedProposalForOwner(t, handler, "tenant-external", "run-tenant", "call-external", nil, "external-resource", 7, "owner-1", now.Add(-5*time.Minute))

	first := callMCPMutationHandler(t, mcpMutationListHandler, http.MethodGet,
		"/v1/console/mcp/mutations/proposals?limit=1", "", &owner)
	if first.Code != http.StatusOK {
		t.Fatalf("tenant first page status %d: %s", first.Code, first.Body.String())
	}
	var page1 mcpMutationListResponse
	if err := json.Unmarshal(first.Body.Bytes(), &page1); err != nil {
		t.Fatalf("decode tenant first page: %v", err)
	}
	if len(page1.Items) != 1 || page1.Items[0].ProposalID != ownOne.ProposalID || page1.NextCursor == "" {
		t.Fatalf("tenant first page = %+v", page1)
	}
	second := callMCPMutationHandler(t, mcpMutationListHandler, http.MethodGet,
		"/v1/console/mcp/mutations/proposals?limit=1&cursor="+page1.NextCursor, "", &owner)
	if second.Code != http.StatusOK {
		t.Fatalf("tenant second page status %d: %s", second.Code, second.Body.String())
	}
	var page2 mcpMutationListResponse
	if err := json.Unmarshal(second.Body.Bytes(), &page2); err != nil {
		t.Fatalf("decode tenant second page: %v", err)
	}
	if len(page2.Items) != 1 || page2.Items[0].ProposalID != ownTwo.ProposalID {
		t.Fatalf("tenant second page = %+v", page2)
	}
	third := callMCPMutationHandler(t, mcpMutationListHandler, http.MethodGet,
		"/v1/console/mcp/mutations/proposals?limit=1", "", &owner)
	if third.Code != http.StatusOK {
		t.Fatalf("tenant reset page status %d", third.Code)
	}
	for _, item := range append(page1.Items, page2.Items...) {
		if item.ProposalID == otherNew.ProposalID || item.ProposalID == otherMid.ProposalID ||
			item.ProposalID == otherOld.ProposalID || item.ProposalID == external.ProposalID {
			t.Fatalf("owner saw unauthorized proposal %+v", item)
		}
	}
	adminList := callMCPMutationHandler(t, mcpMutationListHandler, http.MethodGet,
		"/v1/console/mcp/mutations/proposals?limit=20", "", &admin)
	if adminList.Code != http.StatusOK {
		t.Fatalf("admin tenant list status %d: %s", adminList.Code, adminList.Body.String())
	}
	var adminPage mcpMutationListResponse
	if err := json.Unmarshal(adminList.Body.Bytes(), &adminPage); err != nil {
		t.Fatalf("decode admin tenant list: %v", err)
	}
	if len(adminPage.Items) != 6 {
		t.Fatalf("admin tenant list count = %d, want 6", len(adminPage.Items))
	}
	seenExternal := false
	for _, item := range adminPage.Items {
		if item.ProposalID == external.ProposalID {
			seenExternal = true
		}
	}
	if !seenExternal {
		t.Fatalf("admin tenant list omitted external-resource proposal")
	}

	// Revoking verified ownership must remove the owner's visibility without
	// changing the administrator's view.
	db := handler.(*sourceAPITestHandler).db
	if _, err := db.Exec(`DELETE FROM SourceOwnership WHERE source_id=42 AND user_id=7`); err != nil {
		t.Fatalf("revoke source ownership: %v", err)
	}
	afterRevoke := callMCPMutationHandler(t, mcpMutationListHandler, http.MethodGet,
		"/v1/console/mcp/mutations/proposals?limit=20", "", &owner)
	if afterRevoke.Code != http.StatusOK {
		t.Fatalf("owner list after ownership revoke status %d", afterRevoke.Code)
	}
	var emptyPage mcpMutationListResponse
	if err := json.Unmarshal(afterRevoke.Body.Bytes(), &emptyPage); err != nil {
		t.Fatalf("decode owner list after ownership revoke: %v", err)
	}
	if len(emptyPage.Items) != 0 {
		t.Fatalf("owner saw proposals after ownership revoke: %+v", emptyPage.Items)
	}
	adminAfter := callMCPMutationHandler(t, mcpMutationListHandler, http.MethodGet,
		"/v1/console/mcp/mutations/proposals?limit=20", "", &admin)
	if adminAfter.Code != http.StatusOK {
		t.Fatalf("admin list after ownership revoke status %d", adminAfter.Code)
	}
	var adminAfterPage mcpMutationListResponse
	if err := json.Unmarshal(adminAfter.Body.Bytes(), &adminAfterPage); err != nil {
		t.Fatalf("decode admin list after ownership revoke: %v", err)
	}
	if len(adminAfterPage.Items) != 6 {
		t.Fatalf("admin list after ownership revoke count = %d, want 6", len(adminAfterPage.Items))
	}

	longCursor := strings.Repeat("a", 513)
	badCursor := callMCPMutationHandler(t, mcpMutationListHandler, http.MethodGet,
		"/v1/console/mcp/mutations/proposals?cursor="+longCursor, "", &owner)
	if badCursor.Code != http.StatusBadRequest {
		t.Fatalf("oversized cursor status %d: %s", badCursor.Code, badCursor.Body.String())
	}

	// Administrators retain emergency deny/revoke authority after source
	// ownership provenance changes; the former owner does not.
	adminDeny := callMCPMutationHandler(t, mcpMutationDenyHandler, http.MethodPost,
		"/v1/console/mcp/mutations/proposals/"+ownOne.ProposalID+"/deny",
		fmt.Sprintf(`{"proposal_sha256":%q,"reason":"admin emergency deny"}`, ownOne.ProposalSHA256), &admin)
	if adminDeny.Code != http.StatusOK {
		t.Fatalf("admin deny after ownership revoke status %d: %s", adminDeny.Code, adminDeny.Body.String())
	}
	ownerDeny := callMCPMutationHandler(t, mcpMutationDenyHandler, http.MethodPost,
		"/v1/console/mcp/mutations/proposals/"+ownTwo.ProposalID+"/deny",
		fmt.Sprintf(`{"proposal_sha256":%q,"reason":"former owner deny"}`, ownTwo.ProposalSHA256), &owner)
	if ownerDeny.Code != http.StatusForbidden {
		t.Fatalf("former owner deny status %d: %s", ownerDeny.Code, ownerDeny.Body.String())
	}
}

func TestMCPMutationApprovalAPIExpiredApprovalReapproval(t *testing.T) {
	handler := setupSourceOwnershipTest(t)
	applyMCPMutationAPIMigration(t, handler)
	insertOwnedSource(t, handler, 42, "https://mcp-reapproval.example/", 7)
	now := time.Now().UTC()
	owner := sourceOwnershipIdentity(7, "owner-1", "user")
	proposal := mcpMutationAPISeedProposal(t, handler, "api-reapproval", "run-reapproval", "call-reapproval", []uint64{42}, "", now)
	first := callMCPMutationHandler(t, mcpMutationApproveHandler, http.MethodPost,
		"/v1/console/mcp/mutations/proposals/"+proposal.ProposalID+"/approve",
		mcpMutationApproveBody(proposal.ProposalSHA256, now.Add(20*time.Minute)), &owner)
	if first.Code != http.StatusOK {
		t.Fatalf("initial approval status %d: %s", first.Code, first.Body.String())
	}
	var firstResponse map[string]any
	if err := json.Unmarshal(first.Body.Bytes(), &firstResponse); err != nil {
		t.Fatalf("decode initial approval: %v", err)
	}
	firstApproval, _ := firstResponse["approval"].(map[string]any)
	firstApprovalID, _ := firstApproval["approval_id"].(string)
	if firstApprovalID == "" {
		t.Fatalf("initial approval ID missing: %s", first.Body.String())
	}
	db := handler.(*sourceAPITestHandler).db
	if _, err := db.Exec(`UPDATE MCPMutationApprovals SET approved_at=?, expires_at=? WHERE approval_id=?`,
		now.Add(-2*time.Minute), now.Add(-time.Minute), firstApprovalID); err != nil {
		t.Fatalf("expire initial approval: %v", err)
	}
	second := callMCPMutationHandler(t, mcpMutationApproveHandler, http.MethodPost,
		"/v1/console/mcp/mutations/proposals/"+proposal.ProposalID+"/approve",
		mcpMutationApproveBody(proposal.ProposalSHA256, now.Add(20*time.Minute)), &owner)
	if second.Code != http.StatusOK {
		t.Fatalf("reapproval status %d: %s", second.Code, second.Body.String())
	}
	var secondResponse map[string]any
	if err := json.Unmarshal(second.Body.Bytes(), &secondResponse); err != nil {
		t.Fatalf("decode reapproval: %v", err)
	}
	secondApproval, _ := secondResponse["approval"].(map[string]any)
	secondApprovalID, _ := secondApproval["approval_id"].(string)
	if secondApprovalID == "" || secondApprovalID == firstApprovalID {
		t.Fatalf("reapproval did not create a new approval: %s", second.Body.String())
	}
	oldApproval, found, err := cdb.GetMCPMutationApproval(context.Background(), &handler, firstApprovalID)
	if err != nil || !found || oldApproval.State != "expired" {
		t.Fatalf("old approval after reapproval = %+v found=%v err=%v", oldApproval, found, err)
	}
	stored, found, err := cdb.GetMCPMutationProposal(context.Background(), &handler, proposal.ProposalID)
	if err != nil || !found || stored.State != cdb.MCPMutationStateApproved {
		t.Fatalf("proposal after reapproval = %+v found=%v err=%v", stored, found, err)
	}
	// Expire the replacement without issuing another approval; the read model
	// must not present an approved proposal without a usable approval.
	if _, err := db.Exec(`UPDATE MCPMutationApprovals SET approved_at=?, expires_at=? WHERE approval_id=?`,
		now.Add(-2*time.Minute), now.Add(-time.Minute), secondApprovalID); err != nil {
		t.Fatalf("expire replacement approval: %v", err)
	}
	detail := callMCPMutationHandler(t, mcpMutationDetailHandler, http.MethodGet,
		"/v1/console/mcp/mutations/proposals/"+proposal.ProposalID, "", &owner)
	if detail.Code != http.StatusOK {
		t.Fatalf("detail after replacement expiry status %d: %s", detail.Code, detail.Body.String())
	}
	var detailResponse mcpMutationProposalResponse
	if err := json.Unmarshal(detail.Body.Bytes(), &detailResponse); err != nil {
		t.Fatalf("decode detail after replacement expiry: %v", err)
	}
	if detailResponse.State != cdb.MCPMutationStateApprovalRequired || detailResponse.ActiveApproval != nil {
		t.Fatalf("detail after replacement expiry = %+v", detailResponse)
	}
}
