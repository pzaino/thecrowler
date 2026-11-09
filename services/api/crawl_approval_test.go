package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	auth "github.com/pzaino/thecrowler/pkg/auth"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func setupCrawlApprovalAPITest(t *testing.T) cdb.Handler {
	t.Helper()
	handler := setupSourceOwnershipTest(t)
	db := handler.(*sourceAPITestHandler).db
	if _, err := db.Exec(`CREATE TABLE CrawlApprovals (
		approval_id CHAR(64) PRIMARY KEY,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		last_updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		proposal_sha256 CHAR(64) NOT NULL,
		request_id TEXT NOT NULL,
		run_id TEXT NOT NULL,
		agent_id TEXT NOT NULL,
		owner TEXT NOT NULL,
		principal_user_id INTEGER NOT NULL,
		source_id INTEGER NOT NULL,
		operation_type TEXT NOT NULL,
		issuer TEXT NOT NULL,
		issued_by_user_id INTEGER NOT NULL,
		issued_by TEXT NOT NULL,
		issued_by_issuer TEXT NOT NULL,
		approved_by_user_id INTEGER NOT NULL,
		approved_by TEXT NOT NULL,
		approved_by_issuer TEXT NOT NULL,
		approval_sha256 CHAR(64) NOT NULL,
		proposal_expires_at TIMESTAMP NOT NULL,
		approved_at TIMESTAMP NOT NULL,
		expires_at TIMESTAMP NOT NULL,
		revoked_at TIMESTAMP,
		revoked_by_user_id INTEGER,
		revoked_by TEXT,
		revocation_reason TEXT,
		UNIQUE (request_id, run_id, principal_user_id, source_id, operation_type),
		CHECK (expires_at > approved_at),
		CHECK (julianday(expires_at) - julianday(approved_at) <= 1.0)
	)`); err != nil {
		t.Fatalf("create CrawlApprovals: %v", err)
	}
	insertOwnedSource(t, handler, 42, "https://approval-owned.example/", 7)
	insertOwnedSource(t, handler, 43, "https://approval-other.example/", 8)
	return handler
}

func crawlApprovalIssueBody(now time.Time) string {
	return `{"request_id":"approval-request-1","source_id":42,"agent_id":"agent-1","run_id":"run-1","operation_type":"crawl","proposal_expires_at":"` +
		now.Add(time.Hour).Format(time.RFC3339Nano) + `","approval_expires_at":"` + now.Add(30*time.Minute).Format(time.RFC3339Nano) + `"}`
}

func callCrawlApprovalHandler(t *testing.T, handler http.HandlerFunc, method, path, body string, identity *auth.Identity) *httptest.ResponseRecorder {
	t.Helper()
	request := sourceMutationRequest(method, path, body, identity)
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	return recorder
}

func decodeCrawlApprovalResponse(t *testing.T, recorder *httptest.ResponseRecorder) crawlApprovalResponse {
	t.Helper()
	var response crawlApprovalResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode approval response: %v: %s", err, recorder.Body.String())
	}
	return response
}

func TestCrawlApprovalIssueAndStatus(t *testing.T) {
	handler := setupCrawlApprovalAPITest(t)
	now := time.Now().UTC()
	owner := sourceOwnershipIdentity(7, "owner-1", "user")
	recorder := callCrawlApprovalHandler(t, crawlApprovalIssueHandler, http.MethodPost,
		"/v1/console/crawl/approvals/issue", crawlApprovalIssueBody(now), &owner)
	if recorder.Code != http.StatusOK {
		t.Fatalf("issue status %d: %s", recorder.Code, recorder.Body.String())
	}
	issued := decodeCrawlApprovalResponse(t, recorder)
	if len(issued.ApprovalID) != 64 || issued.RequestID != "approval-request-1" ||
		issued.SourceID != 42 || issued.AgentID != "agent-1" || issued.RunID != "run-1" ||
		issued.ApprovedBy != "owner-1" || issued.Revoked {
		t.Fatalf("unexpected issued approval: %+v", issued)
	}

	duplicate := callCrawlApprovalHandler(t, crawlApprovalIssueHandler, http.MethodPost,
		"/v1/console/crawl/approvals/issue", crawlApprovalIssueBody(now), &owner)
	if duplicate.Code != http.StatusOK {
		t.Fatalf("duplicate issue status %d: %s", duplicate.Code, duplicate.Body.String())
	}
	if got := decodeCrawlApprovalResponse(t, duplicate); got.ApprovalID != issued.ApprovalID {
		t.Fatalf("duplicate issuance created a new approval: %s vs %s", got.ApprovalID, issued.ApprovalID)
	}

	var rows int
	if err := handler.(*sourceAPITestHandler).db.QueryRow(`SELECT COUNT(*) FROM CrawlApprovals`).Scan(&rows); err != nil {
		t.Fatalf("count approvals: %v", err)
	}
	if rows != 1 {
		t.Fatalf("approval rows = %d, want 1", rows)
	}

	status := callCrawlApprovalHandler(t, crawlApprovalStatusHandler, http.MethodGet,
		"/v1/console/crawl/approvals/status?approval_id="+issued.ApprovalID, "", &owner)
	if status.Code != http.StatusOK {
		t.Fatalf("status %d: %s", status.Code, status.Body.String())
	}
	if got := decodeCrawlApprovalResponse(t, status); got.ApprovalID != issued.ApprovalID || got.Revoked {
		t.Fatalf("unexpected status response: %+v", got)
	}
}

func crawlApprovalRevocationMetadata(t *testing.T, handler cdb.Handler, approvalID string) (string, uint64, string) {
	t.Helper()
	var revokedAt, reason string
	var revokedBy uint64
	if err := handler.(*sourceAPITestHandler).db.QueryRow(
		`SELECT COALESCE(CAST(revoked_at AS TEXT),''), COALESCE(revoked_by_user_id,0), COALESCE(revocation_reason,'')
		 FROM CrawlApprovals WHERE approval_id = ?`, approvalID).
		Scan(&revokedAt, &revokedBy, &reason); err != nil {
		t.Fatalf("read approval revocation metadata: %v", err)
	}
	return revokedAt, revokedBy, reason
}

func TestCrawlApprovalRevocation(t *testing.T) {
	handler := setupCrawlApprovalAPITest(t)
	now := time.Now().UTC()
	owner := sourceOwnershipIdentity(7, "owner-1", "user")
	other := sourceOwnershipIdentity(8, "owner-2", "user")
	admin := sourceOwnershipIdentity(9, "admin-1", "admin")
	recorder := callCrawlApprovalHandler(t, crawlApprovalIssueHandler, http.MethodPost,
		"/v1/console/crawl/approvals/issue", crawlApprovalIssueBody(now), &owner)
	if recorder.Code != http.StatusOK {
		t.Fatalf("issue status %d: %s", recorder.Code, recorder.Body.String())
	}
	issued := decodeCrawlApprovalResponse(t, recorder)

	denied := callCrawlApprovalHandler(t, crawlApprovalRevokeHandler, http.MethodPost,
		"/v1/console/crawl/approvals/revoke",
		`{"approval_id":"`+issued.ApprovalID+`","reason":"cross-user"}`, &other)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("cross-user revoke status %d: %s", denied.Code, denied.Body.String())
	}

	revoked := callCrawlApprovalHandler(t, crawlApprovalRevokeHandler, http.MethodPost,
		"/v1/console/crawl/approvals/revoke",
		`{"approval_id":"`+issued.ApprovalID+`","reason":"owner request"}`, &owner)
	if revoked.Code != http.StatusOK {
		t.Fatalf("owner revoke status %d: %s", revoked.Code, revoked.Body.String())
	}
	status := callCrawlApprovalHandler(t, crawlApprovalStatusHandler, http.MethodGet,
		"/v1/console/crawl/approvals/status?approval_id="+issued.ApprovalID, "", &owner)
	if status.Code != http.StatusOK {
		t.Fatalf("status after revoke %d: %s", status.Code, status.Body.String())
	}
	if got := decodeCrawlApprovalResponse(t, status); !got.Revoked {
		t.Fatalf("revocation was not persisted: %+v", got)
	}

	repeat := callCrawlApprovalHandler(t, crawlApprovalRevokeHandler, http.MethodPost,
		"/v1/console/crawl/approvals/revoke",
		`{"approval_id":"`+issued.ApprovalID+`","reason":"repeat"}`, &owner)
	if repeat.Code != http.StatusOK {
		t.Fatalf("repeat revoke status %d: %s", repeat.Code, repeat.Body.String())
	}
	revokedAt, revokedBy, reason := crawlApprovalRevocationMetadata(t, handler, issued.ApprovalID)

	otherRepeat := callCrawlApprovalHandler(t, crawlApprovalRevokeHandler, http.MethodPost,
		"/v1/console/crawl/approvals/revoke",
		`{"approval_id":"`+issued.ApprovalID+`","reason":"other repeat"}`, &other)
	if otherRepeat.Code != http.StatusForbidden {
		t.Fatalf("other repeat revoke status %d: %s", otherRepeat.Code, otherRepeat.Body.String())
	}
	afterOtherAt, afterOtherBy, afterOtherReason := crawlApprovalRevocationMetadata(t, handler, issued.ApprovalID)
	if afterOtherAt != revokedAt || afterOtherBy != revokedBy || afterOtherReason != reason {
		t.Fatalf("unauthorized repeat changed revocation metadata")
	}

	if _, err := handler.(*sourceAPITestHandler).db.Exec(`DELETE FROM UserRoles WHERE user_id=9`); err != nil {
		t.Fatalf("revoke admin grant: %v", err)
	}
	adminRepeat := callCrawlApprovalHandler(t, crawlApprovalRevokeHandler, http.MethodPost,
		"/v1/console/crawl/approvals/revoke",
		`{"approval_id":"`+issued.ApprovalID+`","reason":"demoted admin repeat"}`, &admin)
	if adminRepeat.Code != http.StatusForbidden {
		t.Fatalf("demoted admin repeat status %d: %s", adminRepeat.Code, adminRepeat.Body.String())
	}
	afterAdminAt, afterAdminBy, afterAdminReason := crawlApprovalRevocationMetadata(t, handler, issued.ApprovalID)
	if afterAdminAt != revokedAt || afterAdminBy != revokedBy || afterAdminReason != reason {
		t.Fatalf("demoted administrator changed revocation metadata")
	}

	if _, err := handler.(*sourceAPITestHandler).db.Exec(`UPDATE Users SET disabled=TRUE WHERE user_id=7`); err != nil {
		t.Fatalf("disable owner: %v", err)
	}
	disabledRepeat := callCrawlApprovalHandler(t, crawlApprovalRevokeHandler, http.MethodPost,
		"/v1/console/crawl/approvals/revoke",
		`{"approval_id":"`+issued.ApprovalID+`","reason":"disabled repeat"}`, &owner)
	if disabledRepeat.Code != http.StatusUnauthorized {
		t.Fatalf("disabled owner repeat status %d: %s", disabledRepeat.Code, disabledRepeat.Body.String())
	}
	afterDisabledAt, afterDisabledBy, afterDisabledReason := crawlApprovalRevocationMetadata(t, handler, issued.ApprovalID)
	if afterDisabledAt != revokedAt || afterDisabledBy != revokedBy || afterDisabledReason != reason {
		t.Fatalf("disabled owner changed revocation metadata")
	}
}

func TestCrawlApprovalAuthorizationFailures(t *testing.T) {
	_ = setupCrawlApprovalAPITest(t)
	now := time.Now().UTC()
	owner := sourceOwnershipIdentity(7, "owner-1", "user")
	other := sourceOwnershipIdentity(8, "owner-2", "user")
	admin := sourceOwnershipIdentity(9, "admin-1", "admin")

	crossUser := callCrawlApprovalHandler(t, crawlApprovalIssueHandler, http.MethodPost,
		"/v1/console/crawl/approvals/issue", crawlApprovalIssueBody(now), &other)
	if crossUser.Code != http.StatusForbidden {
		t.Fatalf("cross-user issue status %d: %s", crossUser.Code, crossUser.Body.String())
	}

	adminBody := strings.Replace(crawlApprovalIssueBody(now), `"source_id":42`, `"source_id":43`, 1)
	adminBody = strings.Replace(adminBody, `"request_id":"approval-request-1"`, `"request_id":"admin-request-1"`, 1)
	adminResult := callCrawlApprovalHandler(t, crawlApprovalIssueHandler, http.MethodPost,
		"/v1/console/crawl/approvals/issue", adminBody, &admin)
	if adminResult.Code != http.StatusOK {
		t.Fatalf("admin issue status %d: %s", adminResult.Code, adminResult.Body.String())
	}

	missing := callCrawlApprovalHandler(t, crawlApprovalIssueHandler, http.MethodPost,
		"/v1/console/crawl/approvals/issue", crawlApprovalIssueBody(now), nil)
	if missing.Code != http.StatusUnauthorized {
		t.Fatalf("missing auth status %d: %s", missing.Code, missing.Body.String())
	}

	disabled := sourceOwnershipIdentity(10, "disabled-1", "user")
	disabledResult := callCrawlApprovalHandler(t, crawlApprovalIssueHandler, http.MethodPost,
		"/v1/console/crawl/approvals/issue", crawlApprovalIssueBody(now), &disabled)
	if disabledResult.Code != http.StatusUnauthorized {
		t.Fatalf("disabled user status %d: %s", disabledResult.Code, disabledResult.Body.String())
	}

	expiredBody := strings.Replace(crawlApprovalIssueBody(now), now.Add(30*time.Minute).Format(time.RFC3339Nano), now.Add(-time.Minute).Format(time.RFC3339Nano), 1)
	expired := callCrawlApprovalHandler(t, crawlApprovalIssueHandler, http.MethodPost,
		"/v1/console/crawl/approvals/issue", expiredBody, &owner)
	if expired.Code != http.StatusBadRequest {
		t.Fatalf("expired approval status %d: %s", expired.Code, expired.Body.String())
	}

	config.API.Auth = cfg.AuthConfig{Enabled: false}
	noAuth := callCrawlApprovalHandler(t, crawlApprovalIssueHandler, http.MethodPost,
		"/v1/console/crawl/approvals/issue", crawlApprovalIssueBody(now), &owner)
	if noAuth.Code != http.StatusUnauthorized {
		t.Fatalf("auth-disabled status %d: %s", noAuth.Code, noAuth.Body.String())
	}
}
