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

	auth "github.com/pzaino/thecrowler/pkg/auth"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func mcpMutationAPITestHash(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
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
		statement = strings.TrimSpace(statement)
		if statement == "" || strings.HasPrefix(strings.ToUpper(statement), "BEGIN IMMEDIATE") ||
			strings.EqualFold(statement, "COMMIT") || strings.HasPrefix(strings.ToUpper(statement), "PRAGMA") {
			continue
		}
		if _, err := testHandler.db.Exec(statement); err != nil {
			t.Fatalf("apply MCP mutation statement: %v\n%s", err, statement)
		}
	}
}

func mcpMutationAPISeedProposal(t *testing.T, handler cdb.Handler, seed, runID, callID string, sourceIDs []uint64, externalResource string, now time.Time) cdb.MCPMutationProposalRow {
	t.Helper()
	var arguments []byte
	if len(sourceIDs) > 0 {
		arguments = []byte(fmt.Sprintf(`{"source_id":%d}`, sourceIDs[0]))
	} else {
		arguments = []byte(fmt.Sprintf(`{"resource":%q}`, externalResource))
	}
	row := cdb.MCPMutationProposalRow{
		ProposalID:         mcpMutationAPITestHash("proposal:" + seed),
		ProposalSHA256:     mcpMutationAPITestHash("fingerprint:" + seed),
		IdentityVersion:    1,
		RunID:              runID,
		TraceID:            "trace-" + seed,
		EffectiveCallID:    callID,
		AgentID:            "agent-1",
		Owner:              "owner-1",
		PrincipalUserID:    7,
		PrincipalUsername:  "owner-1",
		PrincipalIssuer:    "crowler",
		ServerID:           "fixture",
		RawToolName:        "mutate_counter",
		ToolAlias:          "fixture__mutate_counter",
		Transport:          "stdio",
		TransportIdentity:  "fixture-command",
		SchemaSHA256:       mcpMutationAPITestHash("schema:" + seed),
		CanonicalArguments: arguments,
		ArgumentsSHA256:    mcpMutationAPITestHash(string(arguments)),
		PolicyID:           "test-policy",
		PolicyVersion:      "1",
		PolicySHA256:       mcpMutationAPITestHash("policy:" + seed),
		PolicySnapshot:     []byte(`{"policy_id":"test-policy"}`),
		SourceIDs:          sourceIDs,
		ExternalResource:   externalResource,
		ApprovalRequired:   true,
		ExecutionBackend:   "none",
		State:              cdb.MCPMutationStateApprovalRequired,
		CreatedAt:          now.UTC(),
		LastUpdatedAt:      now.UTC(),
		ExpiresAt:          now.Add(time.Hour).UTC(),
	}
	created, _, err := cdb.AdmitMCPMutationProposal(context.Background(), &handler, row, 16, 16)
	if err != nil {
		t.Fatalf("seed MCP mutation proposal %s: %v", seed, err)
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
	active, found, err := cdb.GetActiveMCPMutationApprovalByProposal(context.Background(), &handler, second.ProposalSHA256)
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
