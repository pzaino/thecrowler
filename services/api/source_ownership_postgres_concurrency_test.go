//go:build integration

package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	auth "github.com/pzaino/thecrowler/pkg/auth"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func setupSourceOwnershipPostgresAPI(t *testing.T) (cdb.Handler, uint64, uint64, uint64) {
	t.Helper()
	handler := openSourceOwnershipPostgresHandler(t)
	oldHandler := dbHandler
	oldGate := dbAdmission
	oldAuth := config.API.Auth
	t.Cleanup(func() {
		dbHandler = oldHandler
		dbAdmission = oldGate
		config.API.Auth = oldAuth
	})
	dbHandler = handler
	dbAdmission = newDBAdmissionGate(1)
	config.API.Auth = cfg.AuthConfig{Enabled: true, Mode: "local", Issuer: "crowler", HMACSecret: "test-secret", TokenTTL: 60}

	base := uint64(770000 + time.Now().UnixNano()%10000)
	ownerID, otherID, adminID := base, base+1, base+2
	users := []struct {
		id   uint64
		name string
	}{
		{ownerID, fmt.Sprintf("pg-race-owner-%d", ownerID)},
		{otherID, fmt.Sprintf("pg-race-other-%d", otherID)},
		{adminID, fmt.Sprintf("pg-race-admin-%d", adminID)},
	}
	if _, err := handler.Exec(`INSERT INTO AuthRoles(name, description) VALUES
		('admin', 'test administrator'), ('user', 'test user')
		ON CONFLICT (name) DO NOTHING`); err != nil {
		t.Fatalf("ensure race roles: %v", err)
	}
	for _, user := range users {
		if _, err := handler.Exec(`INSERT INTO Users(user_id, username, disabled, deleted_at) VALUES ($1,$2,FALSE,NULL)
			ON CONFLICT (user_id) DO UPDATE SET username=EXCLUDED.username, disabled=FALSE, deleted_at=NULL`, user.id, user.name); err != nil {
			t.Fatalf("insert race user: %v", err)
		}
	}
	for _, grant := range []struct {
		id   uint64
		role string
	}{{ownerID, "user"}, {otherID, "user"}, {adminID, "admin"}} {
		if _, err := handler.Exec(`INSERT INTO UserRoles(user_id, role_id)
			SELECT $1, role_id FROM AuthRoles WHERE name=$2
			ON CONFLICT DO NOTHING`, grant.id, grant.role); err != nil {
			t.Fatalf("grant race role: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = handler.Exec(`DELETE FROM UserRoles WHERE user_id IN ($1,$2,$3)`, ownerID, otherID, adminID)
		_, _ = handler.Exec(`DELETE FROM UserScopes WHERE user_id IN ($1,$2,$3)`, ownerID, otherID, adminID)
		_, _ = handler.Exec(`DELETE FROM Users WHERE user_id IN ($1,$2,$3)`, ownerID, otherID, adminID)
	})
	return handler, ownerID, otherID, adminID
}

func pgIdentity(id uint64, name string, roles ...string) auth.Identity {
	return auth.Identity{Subject: strconv.FormatUint(id, 10), Username: name, Issuer: "crowler", Roles: roles}
}

func pgAddSource(t *testing.T, identity auth.Identity, sourceURL string) (int, string) {
	t.Helper()
	request := sourceMutationRequest(http.MethodPost, "/v1/source/add",
		fmt.Sprintf(`{"url":%q,"usr_id":999999}`, sourceURL), &identity)
	recorder := httptest.NewRecorder()
	addSourceHandler(recorder, request)
	return recorder.Code, recorder.Body.String()
}

func pgSourceCleanup(t *testing.T, handler cdb.Handler, sourceID uint64) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = handler.Exec(`DELETE FROM Status WHERE source_id=$1`, sourceID)
		_, _ = handler.Exec(`DELETE FROM CrawlProposals WHERE source_id=$1`, sourceID)
		_, _ = handler.Exec(`DELETE FROM CrawlApprovals WHERE source_id=$1`, sourceID)
		_, _ = handler.Exec(`DELETE FROM SourceOwnership WHERE source_id=$1`, sourceID)
		_, _ = handler.Exec(`DELETE FROM Sources WHERE source_id=$1`, sourceID)
	})
}

func TestPostgresConcurrentSameURLCreationByTwoUsers(t *testing.T) {
	handler, ownerID, otherID, _ := setupSourceOwnershipPostgresAPI(t)
	ownerName := fmt.Sprintf("pg-race-owner-%d", ownerID)
	otherName := fmt.Sprintf("pg-race-other-%d", otherID)
	sourceURL := fmt.Sprintf("https://pg-race-%d.example/", time.Now().UnixNano())
	identities := []auth.Identity{
		pgIdentity(ownerID, ownerName, "user"),
		pgIdentity(otherID, otherName, "user"),
	}
	type result struct {
		code int
		body string
	}
	results := make(chan result, len(identities))
	var wg sync.WaitGroup
	for _, identity := range identities {
		identity := identity
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, body := pgAddSource(t, identity, sourceURL)
			results <- result{code: code, body: body}
		}()
	}
	wg.Wait()
	close(results)

	created, forbidden := 0, 0
	for result := range results {
		switch result.code {
		case http.StatusCreated:
			created++
		case http.StatusForbidden:
			forbidden++
		default:
			t.Fatalf("unexpected concurrent add status %d: %s", result.code, result.body)
		}
	}
	if created != 1 || forbidden != 1 {
		t.Fatalf("concurrent creation created=%d forbidden=%d", created, forbidden)
	}
	var sourceID, owner uint64
	if err := handler.QueryRow(`SELECT source_id, usr_id FROM Sources WHERE url=$1`, cdb.NormalizeSourceURL(sourceURL)).Scan(&sourceID, &owner); err != nil {
		t.Fatalf("read concurrent source: %v", err)
	}
	pgSourceCleanup(t, handler, sourceID)
	if owner != ownerID && owner != otherID {
		t.Fatalf("unexpected source owner %d", owner)
	}
	var verifiedUser, verifier uint64
	if err := handler.QueryRow(`SELECT user_id, verified_by_user_id FROM SourceOwnership WHERE source_id=$1`, sourceID).Scan(&verifiedUser, &verifier); err != nil {
		t.Fatalf("read concurrent provenance: %v", err)
	}
	if verifiedUser != owner || verifier != owner {
		t.Fatalf("provenance user=%d verifier=%d owner=%d", verifiedUser, verifier, owner)
	}
}

func TestPostgresOwnershipChangeDuringSourceUpdate(t *testing.T) {
	handler, ownerID, otherID, _ := setupSourceOwnershipPostgresAPI(t)
	ownerName := fmt.Sprintf("pg-race-owner-%d", ownerID)
	sourceURL := fmt.Sprintf("https://pg-transfer-%d.example/", time.Now().UnixNano())
	owner := pgIdentity(ownerID, ownerName, "user")
	if code, body := pgAddSource(t, owner, sourceURL); code != http.StatusCreated {
		t.Fatalf("create source status %d: %s", code, body)
	}
	var sourceID uint64
	if err := handler.QueryRow(`SELECT source_id FROM Sources WHERE url=$1`, cdb.NormalizeSourceURL(sourceURL)).Scan(&sourceID); err != nil {
		t.Fatalf("read source: %v", err)
	}
	pgSourceCleanup(t, handler, sourceID)

	// Hold the source row lock while an update request starts, then transfer
	// ownership and commit. The update must observe the transfer when it
	// acquires the lock and must not write with the stale authorization.
	tx, err := handler.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin transfer transaction: %v", err)
	}
	if _, err := tx.Exec(`SELECT source_id FROM Sources WHERE source_id=$1 FOR UPDATE`, sourceID); err != nil {
		_ = tx.Rollback()
		t.Fatalf("lock source: %v", err)
	}
	updateDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := sourceMutationRequest(http.MethodPost, "/v1/source/update",
			fmt.Sprintf(`{"source_id":%d,"status":"pending"}`, sourceID), &owner)
		recorder := httptest.NewRecorder()
		updateSourceHandler(recorder, request)
		updateDone <- recorder
	}()
	time.Sleep(250 * time.Millisecond)
	if _, err := tx.Exec(`UPDATE Sources SET usr_id=$1 WHERE source_id=$2`, otherID, sourceID); err != nil {
		_ = tx.Rollback()
		t.Fatalf("transfer source: %v", err)
	}
	if _, err := tx.Exec(`UPDATE SourceOwnership SET user_id=$1 WHERE source_id=$2`, otherID, sourceID); err != nil {
		_ = tx.Rollback()
		t.Fatalf("transfer provenance: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit transfer: %v", err)
	}
	recorder := <-updateDone
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("stale update status %d: %s", recorder.Code, recorder.Body.String())
	}
	var status string
	var ownerAfter uint64
	if err := handler.QueryRow(`SELECT status, usr_id FROM Sources WHERE source_id=$1`, sourceID).Scan(&status, &ownerAfter); err != nil {
		t.Fatalf("read source after stale update: %v", err)
	}
	if status != "new" || ownerAfter != otherID {
		t.Fatalf("stale update result status=%q owner=%d", status, ownerAfter)
	}
}

func TestPostgresOwnershipChangeDuringSourceRemove(t *testing.T) {
	handler, ownerID, otherID, _ := setupSourceOwnershipPostgresAPI(t)
	ownerName := fmt.Sprintf("pg-race-owner-%d", ownerID)
	sourceURL := fmt.Sprintf("https://pg-remove-%d.example/", time.Now().UnixNano())
	owner := pgIdentity(ownerID, ownerName, "user")
	if code, body := pgAddSource(t, owner, sourceURL); code != http.StatusCreated {
		t.Fatalf("create source status %d: %s", code, body)
	}
	var sourceID uint64
	if err := handler.QueryRow(`SELECT source_id FROM Sources WHERE url=$1`, cdb.NormalizeSourceURL(sourceURL)).Scan(&sourceID); err != nil {
		t.Fatalf("read source: %v", err)
	}
	pgSourceCleanup(t, handler, sourceID)

	tx, err := handler.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin transfer transaction: %v", err)
	}
	if _, err := tx.Exec(`SELECT source_id FROM Sources WHERE source_id=$1 FOR UPDATE`, sourceID); err != nil {
		_ = tx.Rollback()
		t.Fatalf("lock source: %v", err)
	}
	removeDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := sourceMutationRequest(http.MethodGet, "/v1/source/remove?q="+sourceURL, "", &owner)
		recorder := httptest.NewRecorder()
		removeSourceHandler(recorder, request)
		removeDone <- recorder
	}()
	time.Sleep(250 * time.Millisecond)
	if _, err := tx.Exec(`UPDATE Sources SET usr_id=$1 WHERE source_id=$2`, otherID, sourceID); err != nil {
		_ = tx.Rollback()
		t.Fatalf("transfer source: %v", err)
	}
	if _, err := tx.Exec(`UPDATE SourceOwnership SET user_id=$1 WHERE source_id=$2`, otherID, sourceID); err != nil {
		_ = tx.Rollback()
		t.Fatalf("transfer provenance: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit transfer: %v", err)
	}
	recorder := <-removeDone
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("stale remove status %d: %s", recorder.Code, recorder.Body.String())
	}
	var count int
	if err := handler.QueryRow(`SELECT COUNT(*) FROM Sources WHERE source_id=$1`, sourceID).Scan(&count); err != nil {
		t.Fatalf("count source: %v", err)
	}
	if count != 1 {
		t.Fatalf("stale remove deleted the source")
	}
}

func TestPostgresAdministrativeOwnershipVerification(t *testing.T) {
	handler, ownerID, _, adminID := setupSourceOwnershipPostgresAPI(t)
	ownerName := fmt.Sprintf("pg-race-owner-%d", ownerID)
	adminName := fmt.Sprintf("pg-race-admin-%d", adminID)
	sourceURL := fmt.Sprintf("https://pg-legacy-%d.example/", time.Now().UnixNano())
	if _, err := handler.Exec(`INSERT INTO Sources(source_uid, url, name, status, disabled, usr_id)
		VALUES ('legacy-uid', $1, 'legacy', 'new', FALSE, $2)`, cdb.NormalizeSourceURL(sourceURL), ownerID); err != nil {
		t.Fatalf("insert legacy source: %v", err)
	}
	var sourceID uint64
	if err := handler.QueryRow(`SELECT source_id FROM Sources WHERE url=$1`, cdb.NormalizeSourceURL(sourceURL)).Scan(&sourceID); err != nil {
		t.Fatalf("read legacy source: %v", err)
	}
	pgSourceCleanup(t, handler, sourceID)

	owner := pgIdentity(ownerID, ownerName, "user")
	admin := pgIdentity(adminID, adminName, "admin")
	request := sourceMutationRequest(http.MethodPost, "/v1/source/update",
		fmt.Sprintf(`{"source_id":%d,"status":"pending"}`, sourceID), &owner)
	recorder := httptest.NewRecorder()
	updateSourceHandler(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("legacy owner update status %d: %s", recorder.Code, recorder.Body.String())
	}

	request = sourceMutationRequest(http.MethodPost, "/v1/console/source/ownership/verify",
		fmt.Sprintf(`{"source_id":%d,"user_id":%d,"reason":"test migration"}`, sourceID, ownerID), &admin)
	recorder = httptest.NewRecorder()
	sourceOwnershipVerifyHandler(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("admin verification status %d: %s", recorder.Code, recorder.Body.String())
	}
	request = sourceMutationRequest(http.MethodPost, "/v1/source/update",
		fmt.Sprintf(`{"source_id":%d,"status":"pending"}`, sourceID), &owner)
	recorder = httptest.NewRecorder()
	updateSourceHandler(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("verified owner update status %d: %s", recorder.Code, recorder.Body.String())
	}
	var verifiedBy uint64
	if err := handler.QueryRow(`SELECT verified_by_user_id FROM SourceOwnership WHERE source_id=$1`, sourceID).Scan(&verifiedBy); err != nil {
		t.Fatalf("read verification actor: %v", err)
	}
	if verifiedBy != adminID {
		t.Fatalf("verification actor = %d, want admin %d", verifiedBy, adminID)
	}
}

func TestPostgresUserDisabledDuringSourceUpdate(t *testing.T) {
	handler, ownerID, _, _ := setupSourceOwnershipPostgresAPI(t)
	ownerName := fmt.Sprintf("pg-race-owner-%d", ownerID)
	sourceURL := fmt.Sprintf("https://pg-disabled-%d.example/", time.Now().UnixNano())
	owner := pgIdentity(ownerID, ownerName, "user")
	if code, body := pgAddSource(t, owner, sourceURL); code != http.StatusCreated {
		t.Fatalf("create source status %d: %s", code, body)
	}
	var sourceID uint64
	if err := handler.QueryRow(`SELECT source_id FROM Sources WHERE url=$1`, cdb.NormalizeSourceURL(sourceURL)).Scan(&sourceID); err != nil {
		t.Fatalf("read source: %v", err)
	}
	pgSourceCleanup(t, handler, sourceID)

	tx, err := handler.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin disable transaction: %v", err)
	}
	if _, err := tx.Exec(`SELECT source_id FROM Sources WHERE source_id=$1 FOR UPDATE`, sourceID); err != nil {
		_ = tx.Rollback()
		t.Fatalf("lock source: %v", err)
	}
	updateDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := sourceMutationRequest(http.MethodPost, "/v1/source/update",
			fmt.Sprintf(`{"source_id":%d,"status":"pending"}`, sourceID), &owner)
		recorder := httptest.NewRecorder()
		updateSourceHandler(recorder, request)
		updateDone <- recorder
	}()
	time.Sleep(250 * time.Millisecond)
	if _, err := tx.Exec(`UPDATE Users SET disabled=TRUE WHERE user_id=$1`, ownerID); err != nil {
		_ = tx.Rollback()
		t.Fatalf("disable owner: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit disable: %v", err)
	}
	recorder := <-updateDone
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("disabled-user update status %d: %s", recorder.Code, recorder.Body.String())
	}
	var status string
	if err := handler.QueryRow(`SELECT status FROM Sources WHERE source_id=$1`, sourceID).Scan(&status); err != nil {
		t.Fatalf("read source after disabled-user update: %v", err)
	}
	if status != "new" {
		t.Fatalf("disabled-user update changed status to %q", status)
	}
}

func TestPostgresConcurrentApprovalRevocationAuthorization(t *testing.T) {
	handler, ownerID, otherID, _ := setupSourceOwnershipPostgresAPI(t)
	ownerName := fmt.Sprintf("pg-race-owner-%d", ownerID)
	otherName := fmt.Sprintf("pg-race-other-%d", otherID)
	sourceURL := fmt.Sprintf("https://pg-approval-race-%d.example/", time.Now().UnixNano())
	owner := pgIdentity(ownerID, ownerName, "user")
	other := pgIdentity(otherID, otherName, "user")
	if code, body := pgAddSource(t, owner, sourceURL); code != http.StatusCreated {
		t.Fatalf("create source status %d: %s", code, body)
	}
	var sourceID uint64
	if err := handler.QueryRow(`SELECT source_id FROM Sources WHERE url=$1`, cdb.NormalizeSourceURL(sourceURL)).Scan(&sourceID); err != nil {
		t.Fatalf("read approval source: %v", err)
	}
	pgSourceCleanup(t, handler, sourceID)
	now := time.Now().UTC()
	requestID := fmt.Sprintf("%064x", now.UnixNano())
	seedSubmittableCrawlProposal(t, handler, requestID, "race-run", sourceID, ownerID, ownerName, now.Add(time.Hour))
	issueBody := fmt.Sprintf(`{"request_id":%q,"source_id":%d,"operation_type":"crawl","proposal_expires_at":%q,"approval_expires_at":%q}`,
		requestID, sourceID, now.Add(time.Hour).Format(time.RFC3339Nano), now.Add(30*time.Minute).Format(time.RFC3339Nano))
	issue := callCrawlApprovalHandler(t, crawlApprovalIssueHandler, http.MethodPost,
		"/v1/console/crawl/approvals/issue", issueBody, &owner)
	if issue.Code != http.StatusOK {
		t.Fatalf("issue approval status %d: %s", issue.Code, issue.Body.String())
	}
	approval := decodeCrawlApprovalResponse(t, issue)

	type outcome struct {
		code int
	}
	results := make(chan outcome, 2)
	var wg sync.WaitGroup
	for _, identity := range []auth.Identity{owner, other} {
		identity := identity
		wg.Add(1)
		go func() {
			defer wg.Done()
			recorder := callCrawlApprovalHandler(t, crawlApprovalRevokeHandler, http.MethodPost,
				"/v1/console/crawl/approvals/revoke",
				`{"approval_id":"`+approval.ApprovalID+`","reason":"concurrent"}`, &identity)
			results <- outcome{code: recorder.Code}
		}()
	}
	wg.Wait()
	close(results)
	ownerSuccess, otherDenied := false, false
	for result := range results {
		switch result.code {
		case http.StatusOK:
			ownerSuccess = true
		case http.StatusForbidden:
			otherDenied = true
		}
	}
	if !ownerSuccess || !otherDenied {
		t.Fatalf("concurrent HTTP revocation ownerSuccess=%v otherDenied=%v", ownerSuccess, otherDenied)
	}
	var revokedBy uint64
	if err := handler.QueryRow(`SELECT revoked_by_user_id FROM CrawlApprovals WHERE approval_id=$1`, approval.ApprovalID).Scan(&revokedBy); err != nil {
		t.Fatalf("read HTTP revocation owner: %v", err)
	}
	if revokedBy != ownerID {
		t.Fatalf("concurrent HTTP revocation actor = %d, want owner %d", revokedBy, ownerID)
	}
}
