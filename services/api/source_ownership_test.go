package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	auth "github.com/pzaino/thecrowler/pkg/auth"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"

	_ "github.com/mattn/go-sqlite3"
)

func setupSourceOwnershipTest(t *testing.T) cdb.Handler {
	t.Helper()
	oldHandler := dbHandler
	oldGate := dbAdmission
	oldAuth := config.API.Auth
	t.Cleanup(func() {
		dbHandler = oldHandler
		dbAdmission = oldGate
		config.API.Auth = oldAuth
	})
	handler, cleanup := setupSourceAPITestDB(t)
	t.Cleanup(cleanup)
	dbHandler = handler
	dbAdmission = newDBAdmissionGate(1)
	config.API.Auth = cfg.AuthConfig{Enabled: true, Mode: "local", Issuer: "crowler", HMACSecret: "test-secret", TokenTTL: 60}
	setupMCPMutationApprovalFactory(t)

	db := handler.(*sourceAPITestHandler).db
	for _, statement := range []string{
		`CREATE TABLE Users (user_id INTEGER PRIMARY KEY, username TEXT NOT NULL UNIQUE, disabled BOOLEAN NOT NULL DEFAULT FALSE, deleted_at TIMESTAMP)`,
		`CREATE TABLE AuthRoles (role_id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, description TEXT)`,
		`CREATE TABLE AuthScopes (scope_id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, description TEXT)`,
		`CREATE TABLE UserRoles (user_id INTEGER NOT NULL, role_id INTEGER NOT NULL, PRIMARY KEY (user_id, role_id))`,
		`CREATE TABLE UserScopes (user_id INTEGER NOT NULL, scope_id INTEGER NOT NULL, PRIMARY KEY (user_id, scope_id))`,
		`CREATE TABLE RoleScopes (role_id INTEGER NOT NULL, scope_id INTEGER NOT NULL, PRIMARY KEY (role_id, scope_id))`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("create auth table: %v", err)
		}
	}
	if _, err := db.Exec(`INSERT INTO Users(user_id, username, disabled, deleted_at) VALUES
		(7, 'owner-1', FALSE, NULL),
		(8, 'owner-2', FALSE, NULL),
		(9, 'admin-1', FALSE, NULL),
		(10, 'disabled-1', TRUE, NULL),
		(11, 'deleted-1', FALSE, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("insert users: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO AuthRoles(role_id, name) VALUES (1, 'admin'), (2, 'user');
		INSERT INTO UserRoles(user_id, role_id) VALUES (7, 2), (8, 2), (9, 1);`); err != nil {
		t.Fatalf("insert roles: %v", err)
	}
	return handler
}

func sourceOwnershipIdentity(userID uint64, username string, roles ...string) auth.Identity {
	return auth.Identity{Subject: fmt.Sprintf("%d", userID), Username: username, Issuer: "crowler", Roles: roles}
}

func sourceMutationRequest(method, path, body string, identity *auth.Identity) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if identity != nil {
		request = request.WithContext(auth.ContextWithIdentity(context.Background(), *identity))
	}
	return request
}

func insertOwnedSource(t *testing.T, handler cdb.Handler, sourceID int64, sourceURL string, ownerID uint64) {
	t.Helper()
	db := handler.(*sourceAPITestHandler).db
	if _, err := db.Exec(`INSERT INTO Sources(source_id, source_uid, url, name, status, disabled, usr_id)
		VALUES (?, ?, ?, 'owned', 'new', FALSE, ?)`, sourceID, fmt.Sprintf("uid-%d", sourceID), sourceURL, ownerID); err != nil {
		t.Fatalf("insert owned source: %v", err)
	}
	if ownerID != 0 {
		if _, err := db.Exec(`INSERT INTO SourceOwnership(source_id, user_id, verified_at, verified_by_user_id, verified_by, verification_method)
			VALUES (?, ?, CURRENT_TIMESTAMP, ?, 'test-fixture', 'admin_verification')`, sourceID, ownerID, ownerID); err != nil {
			t.Fatalf("insert source ownership: %v", err)
		}
	}
}

func insertLegacySource(t *testing.T, handler cdb.Handler, sourceID int64, sourceURL string, ownerID uint64) {
	t.Helper()
	db := handler.(*sourceAPITestHandler).db
	if _, err := db.Exec(`INSERT INTO Sources(source_id, source_uid, url, name, status, disabled, usr_id)
		VALUES (?, ?, ?, 'legacy', 'new', FALSE, ?)`, sourceID, fmt.Sprintf("uid-%d", sourceID), sourceURL, ownerID); err != nil {
		t.Fatalf("insert legacy source: %v", err)
	}
}

func sourceHasVerifiedOwnership(t *testing.T, handler cdb.Handler, sourceID int64) bool {
	t.Helper()
	var count int
	if err := handler.(*sourceAPITestHandler).db.QueryRow(
		`SELECT COUNT(*) FROM SourceOwnership WHERE source_id = ?`, sourceID).Scan(&count); err != nil {
		t.Fatalf("read source ownership: %v", err)
	}
	return count > 0
}

func readSourceOwner(t *testing.T, handler cdb.Handler, sourceID int64) uint64 {
	t.Helper()
	var owner uint64
	if err := handler.(*sourceAPITestHandler).db.QueryRow(`SELECT usr_id FROM Sources WHERE source_id = ?`, sourceID).Scan(&owner); err != nil {
		t.Fatalf("read source owner: %v", err)
	}
	return owner
}

func readSourceStatus(t *testing.T, handler cdb.Handler, sourceID int64) string {
	t.Helper()
	var status string
	if err := handler.(*sourceAPITestHandler).db.QueryRow(`SELECT status FROM Sources WHERE source_id = ?`, sourceID).Scan(&status); err != nil {
		t.Fatalf("read source status: %v", err)
	}
	return status
}

func TestAuthenticatedSourceCreationIgnoresBodyUsrID(t *testing.T) {
	handler := setupSourceOwnershipTest(t)
	identity := sourceOwnershipIdentity(7, "owner-1", "user")
	request := sourceMutationRequest(http.MethodPost, "/v1/source/add",
		`{"url":"https://owned.example/","usr_id":999}`, &identity)
	recorder := httptest.NewRecorder()
	addSourceHandler(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var sourceID int64
	if err := handler.(*sourceAPITestHandler).db.QueryRow(`SELECT source_id FROM Sources WHERE url = 'https://owned.example/'`).Scan(&sourceID); err != nil {
		t.Fatalf("read created source: %v", err)
	}
	if owner := readSourceOwner(t, handler, sourceID); owner != 7 {
		t.Fatalf("owner = %d, want authenticated user 7", owner)
	}
}

func TestAuthenticatedSourceCreationRejectsExistingOtherOwner(t *testing.T) {
	handler := setupSourceOwnershipTest(t)
	insertOwnedSource(t, handler, 42, "https://other.example/", 8)
	identity := sourceOwnershipIdentity(7, "owner-1", "user")
	request := sourceMutationRequest(http.MethodPost, "/v1/source/add",
		`{"url":"https://other.example/","usr_id":7}`, &identity)
	recorder := httptest.NewRecorder()
	addSourceHandler(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if owner := readSourceOwner(t, handler, 42); owner != 8 {
		t.Fatalf("cross-user add transferred ownership to %d", owner)
	}
}

func TestCrossUserSourceUpdateRejected(t *testing.T) {
	handler := setupSourceOwnershipTest(t)
	insertOwnedSource(t, handler, 42, "https://other.example/", 8)
	identity := sourceOwnershipIdentity(7, "owner-1", "user")
	request := sourceMutationRequest(http.MethodPost, "/v1/source/update",
		`{"source_id":42,"status":"pending"}`, &identity)
	recorder := httptest.NewRecorder()
	updateSourceHandler(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if status := readSourceStatus(t, handler, 42); status != "new" {
		t.Fatalf("cross-user update changed status to %q", status)
	}
}

func TestOwnerAndAdminSourceUpdateAllowed(t *testing.T) {
	handler := setupSourceOwnershipTest(t)
	insertOwnedSource(t, handler, 42, "https://owner.example/", 7)

	owner := sourceOwnershipIdentity(7, "owner-1", "user")
	request := sourceMutationRequest(http.MethodPost, "/v1/source/update",
		`{"source_id":42,"status":"pending"}`, &owner)
	recorder := httptest.NewRecorder()
	updateSourceHandler(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("owner update failed: %d: %s", recorder.Code, recorder.Body.String())
	}
	if status := readSourceStatus(t, handler, 42); status != "pending" {
		t.Fatalf("owner update status = %q", status)
	}

	insertOwnedSource(t, handler, 43, "https://admin.example/", 8)
	admin := sourceOwnershipIdentity(9, "admin-1", "admin")
	request = sourceMutationRequest(http.MethodPost, "/v1/source/update",
		`{"source_id":43,"disabled":true}`, &admin)
	recorder = httptest.NewRecorder()
	updateSourceHandler(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("admin update failed: %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestSourceMutationRejectsMissingOrInactiveIdentity(t *testing.T) {
	handler := setupSourceOwnershipTest(t)
	insertOwnedSource(t, handler, 42, "https://owner.example/", 7)

	request := sourceMutationRequest(http.MethodPost, "/v1/source/update",
		`{"source_id":42,"status":"pending"}`, nil)
	recorder := httptest.NewRecorder()
	updateSourceHandler(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("missing identity got %d, want 401", recorder.Code)
	}

	for _, identity := range []auth.Identity{
		sourceOwnershipIdentity(10, "disabled-1", "user"),
		sourceOwnershipIdentity(11, "deleted-1", "user"),
		{Subject: "external-subject", Username: "external", Issuer: "oidc", Roles: []string{"user"}},
	} {
		request := sourceMutationRequest(http.MethodPost, "/v1/source/update",
			`{"source_id":42,"status":"pending"}`, &identity)
		recorder := httptest.NewRecorder()
		updateSourceHandler(recorder, request)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("identity %+v got %d, want 401", identity, recorder.Code)
		}
	}
	if status := readSourceStatus(t, handler, 42); status != "new" {
		t.Fatalf("unauthorized mutation changed status to %q", status)
	}
}

func TestSourceOwnershipZeroAndTransferAttempts(t *testing.T) {
	handler := setupSourceOwnershipTest(t)
	insertOwnedSource(t, handler, 42, "https://unowned.example/", 0)
	identity := sourceOwnershipIdentity(7, "owner-1", "user")
	request := sourceMutationRequest(http.MethodPost, "/v1/source/update",
		`{"source_id":42,"status":"pending"}`, &identity)
	recorder := httptest.NewRecorder()
	updateSourceHandler(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("zero-owner update got %d, want 403", recorder.Code)
	}

	insertOwnedSource(t, handler, 43, "https://transfer.example/", 7)
	request = sourceMutationRequest(http.MethodPost, "/v1/source/update",
		`{"source_id":43,"usr_id":999,"status":"pending"}`, &identity)
	recorder = httptest.NewRecorder()
	updateSourceHandler(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("owner update failed: %d: %s", recorder.Code, recorder.Body.String())
	}
	if owner := readSourceOwner(t, handler, 43); owner != 7 {
		t.Fatalf("source update transferred ownership to %d", owner)
	}
}

func TestAuthDisabledPreservesLegacySourceWorkflow(t *testing.T) {
	handler := setupSourceOwnershipTest(t)
	config.API.Auth = cfg.AuthConfig{Enabled: false}
	request := sourceMutationRequest(http.MethodPost, "/v1/source/add",
		`{"url":"https://legacy.example/","usr_id":5}`, nil)
	recorder := httptest.NewRecorder()
	addSourceHandler(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("legacy add failed: %d: %s", recorder.Code, recorder.Body.String())
	}
	var sourceID int64
	if err := handler.(*sourceAPITestHandler).db.QueryRow(`SELECT source_id FROM Sources WHERE url = 'https://legacy.example/'`).Scan(&sourceID); err != nil {
		t.Fatalf("read legacy source: %v", err)
	}
	if owner := readSourceOwner(t, handler, sourceID); owner != 5 {
		t.Fatalf("legacy owner = %d, want 5", owner)
	}
	if sourceHasVerifiedOwnership(t, handler, sourceID) {
		t.Fatal("legacy unauthenticated creation claimed verified ownership")
	}
}

func TestSourceRemoveCrossUserRejected(t *testing.T) {
	handler := setupSourceOwnershipTest(t)
	insertOwnedSource(t, handler, 42, "https://remove.example/", 8)
	identity := sourceOwnershipIdentity(7, "owner-1", "user")
	request := sourceMutationRequest(http.MethodGet, "/v1/source/remove?q=https://remove.example/", "", &identity)
	recorder := httptest.NewRecorder()
	removeSourceHandler(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-user remove got %d, want 403: %s", recorder.Code, recorder.Body.String())
	}
	var count int
	if err := handler.(*sourceAPITestHandler).db.QueryRow(`SELECT COUNT(*) FROM Sources WHERE source_id = 42`).Scan(&count); err != nil {
		t.Fatalf("count source: %v", err)
	}
	if count != 1 {
		t.Fatalf("cross-user remove deleted the source")
	}
}

func TestSourceIdentityDatabaseFailureRejected(t *testing.T) {
	handler := setupSourceOwnershipTest(t)
	insertOwnedSource(t, handler, 42, "https://failure.example/", 7)
	if err := handler.(*sourceAPITestHandler).db.Close(); err != nil {
		t.Fatalf("close test database: %v", err)
	}
	identity := sourceOwnershipIdentity(7, "owner-1", "user")
	request := sourceMutationRequest(http.MethodPost, "/v1/source/update",
		`{"source_id":42,"status":"pending"}`, &identity)
	recorder := httptest.NewRecorder()
	updateSourceHandler(recorder, request)
	if recorder.Code != http.StatusUnauthorized && recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("database failure got %d, want 401/503: %s", recorder.Code, recorder.Body.String())
	}
}

func TestAuthenticatedSourceMutationMiddleware(t *testing.T) {
	handler := setupSourceOwnershipTest(t)
	_ = handler
	called := false
	wrapped := auth.Middleware(config.API.Auth, dbHandler)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodPost, "/v1/source/add", strings.NewReader(`{}`))
	recorder := httptest.NewRecorder()
	wrapped.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("missing token got %d, want 401", recorder.Code)
	}
	if called {
		t.Fatal("unauthenticated request reached the source handler")
	}

	token, err := auth.IssueToken(config.API.Auth, sourceOwnershipIdentity(7, "owner-1", "user"), "source-ownership-test")
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	request = httptest.NewRequest(http.MethodPost, "/v1/source/add", strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer "+token)
	recorder = httptest.NewRecorder()
	wrapped.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent || !called {
		t.Fatalf("authenticated request got %d, called=%v: %s", recorder.Code, called, recorder.Body.String())
	}
}

func TestLegacyUnverifiedSourceRequiresAdministrativeVerification(t *testing.T) {
	handler := setupSourceOwnershipTest(t)
	insertLegacySource(t, handler, 42, "https://legacy.example/", 7)
	owner := sourceOwnershipIdentity(7, "owner-1", "user")
	admin := sourceOwnershipIdentity(9, "admin-1", "admin")

	request := sourceMutationRequest(http.MethodPost, "/v1/source/update",
		`{"source_id":42,"status":"pending"}`, &owner)
	recorder := httptest.NewRecorder()
	updateSourceHandler(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("legacy owner update got %d, want 403: %s", recorder.Code, recorder.Body.String())
	}
	if status := readSourceStatus(t, handler, 42); status != "new" {
		t.Fatalf("legacy owner update changed status to %q", status)
	}
	if sourceHasVerifiedOwnership(t, handler, 42) {
		t.Fatal("legacy source unexpectedly has verified provenance")
	}

	// Administrators may manage a legacy source without silently claiming it.
	request = sourceMutationRequest(http.MethodPost, "/v1/source/update",
		`{"source_id":42,"disabled":true}`, &admin)
	recorder = httptest.NewRecorder()
	updateSourceHandler(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("admin legacy update got %d: %s", recorder.Code, recorder.Body.String())
	}
	if ownerID := readSourceOwner(t, handler, 42); ownerID != 7 {
		t.Fatalf("admin update transferred ownership to %d", ownerID)
	}

	// Explicit administrative verification makes the existing claim
	// authoritative and records the verifier.
	request = sourceMutationRequest(http.MethodPost, "/v1/console/source/ownership/verify",
		`{"source_id":42,"user_id":7,"reason":"historical ownership audit"}`, &admin)
	recorder = httptest.NewRecorder()
	sourceOwnershipVerifyHandler(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("admin verification got %d: %s", recorder.Code, recorder.Body.String())
	}
	if !sourceHasVerifiedOwnership(t, handler, 42) {
		t.Fatal("admin verification did not persist provenance")
	}
	var verifier uint64
	var method string
	if err := handler.(*sourceAPITestHandler).db.QueryRow(
		`SELECT verified_by_user_id, verification_method FROM SourceOwnership WHERE source_id = 42`).
		Scan(&verifier, &method); err != nil {
		t.Fatalf("read provenance: %v", err)
	}
	if verifier != 9 || method != "admin_verification" {
		t.Fatalf("unexpected provenance verifier=%d method=%q", verifier, method)
	}

	request = sourceMutationRequest(http.MethodPost, "/v1/source/update",
		`{"source_id":42,"status":"pending"}`, &owner)
	recorder = httptest.NewRecorder()
	updateSourceHandler(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("verified owner update got %d: %s", recorder.Code, recorder.Body.String())
	}
	if status := readSourceStatus(t, handler, 42); status != "pending" {
		t.Fatalf("verified owner update status = %q", status)
	}
}

func TestSourceOwnershipVerificationAuthorizationFailures(t *testing.T) {
	handler := setupSourceOwnershipTest(t)
	insertLegacySource(t, handler, 42, "https://verify.example/", 7)
	owner := sourceOwnershipIdentity(7, "owner-1", "user")
	admin := sourceOwnershipIdentity(9, "admin-1", "admin")

	for name, test := range map[string]struct {
		identity *auth.Identity
		body     string
		want     int
	}{
		"missing auth":        {nil, `{"source_id":42,"user_id":7}`, http.StatusUnauthorized},
		"ordinary user":       {&owner, `{"source_id":42,"user_id":7}`, http.StatusForbidden},
		"unknown source":      {&admin, `{"source_id":999,"user_id":7}`, http.StatusNotFound},
		"unknown target user": {&admin, `{"source_id":42,"user_id":999}`, http.StatusForbidden},
	} {
		t.Run(name, func(t *testing.T) {
			request := sourceMutationRequest(http.MethodPost, "/v1/console/source/ownership/verify", test.body, test.identity)
			recorder := httptest.NewRecorder()
			sourceOwnershipVerifyHandler(recorder, request)
			if recorder.Code != test.want {
				t.Fatalf("got %d, want %d: %s", recorder.Code, test.want, recorder.Body.String())
			}
			if sourceHasVerifiedOwnership(t, handler, 42) {
				t.Fatal("failed verification persisted provenance")
			}
		})
	}

	oldAuth := config.API.Auth
	config.API.Auth = cfg.AuthConfig{Enabled: false}
	request := sourceMutationRequest(http.MethodPost, "/v1/console/source/ownership/verify",
		`{"source_id":42,"user_id":7}`, &admin)
	recorder := httptest.NewRecorder()
	sourceOwnershipVerifyHandler(recorder, request)
	config.API.Auth = oldAuth
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("auth-disabled verification got %d, want 401", recorder.Code)
	}
}

func TestConcurrentSameURLCreationByTwoUsers(t *testing.T) {
	handler := setupSourceOwnershipTest(t)
	ownerOne := sourceOwnershipIdentity(7, "owner-1", "user")
	ownerTwo := sourceOwnershipIdentity(8, "owner-2", "user")
	url := "https://concurrent.example/"
	type outcome struct {
		code int
		body string
	}
	results := make(chan outcome, 2)
	var wg sync.WaitGroup
	for _, identity := range []auth.Identity{ownerOne, ownerTwo} {
		identity := identity
		wg.Add(1)
		go func() {
			defer wg.Done()
			request := sourceMutationRequest(http.MethodPost, "/v1/source/add",
				`{"url":"`+url+`","usr_id":999}`, &identity)
			recorder := httptest.NewRecorder()
			addSourceHandler(recorder, request)
			results <- outcome{code: recorder.Code, body: recorder.Body.String()}
		}()
	}
	wg.Wait()
	close(results)

	successes, conflicts := 0, 0
	for result := range results {
		switch result.code {
		case http.StatusCreated:
			successes++
		case http.StatusForbidden:
			conflicts++
		default:
			t.Fatalf("unexpected concurrent add status %d: %s", result.code, result.body)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent add results successes=%d conflicts=%d", successes, conflicts)
	}
	var owner uint64
	if err := handler.(*sourceAPITestHandler).db.QueryRow(
		`SELECT usr_id FROM Sources WHERE url = ?`, url).Scan(&owner); err != nil {
		t.Fatalf("read concurrent source owner: %v", err)
	}
	if owner != 7 && owner != 8 {
		t.Fatalf("unexpected concurrent source owner %d", owner)
	}
	var sourceID int64
	if err := handler.(*sourceAPITestHandler).db.QueryRow(`SELECT source_id FROM Sources WHERE url = ?`, url).Scan(&sourceID); err != nil {
		t.Fatalf("read concurrent source ID: %v", err)
	}
	if !sourceHasVerifiedOwnership(t, handler, sourceID) {
		t.Fatal("concurrent creation did not persist verified provenance")
	}
}

func TestSourceVacuumCrossUserRejected(t *testing.T) {
	handler := setupSourceOwnershipTest(t)
	insertOwnedSource(t, handler, 42, "https://vacuum.example/", 8)
	identity := sourceOwnershipIdentity(7, "owner-1", "user")
	request := sourceMutationRequest(http.MethodGet, "/v1/source/vacuum?q=https://vacuum.example/", "", &identity)
	recorder := httptest.NewRecorder()
	vacuumSourceHandler(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-user vacuum got %d, want 403: %s", recorder.Code, recorder.Body.String())
	}
	var count int
	if err := handler.(*sourceAPITestHandler).db.QueryRow(`SELECT COUNT(*) FROM Sources WHERE source_id = 42`).Scan(&count); err != nil {
		t.Fatalf("count source: %v", err)
	}
	if count != 1 {
		t.Fatal("cross-user vacuum removed the source")
	}
}

func TestAuthenticatedSameOwnerSourceUpsertRemainsValid(t *testing.T) {
	handler := setupSourceOwnershipTest(t)
	owner := sourceOwnershipIdentity(7, "owner-1", "user")
	sourceURL := "https://same-owner.example/"
	for i := 0; i < 2; i++ {
		request := sourceMutationRequest(http.MethodPost, "/v1/source/add",
			`{"url":"`+sourceURL+`","usr_id":999,"disabled":false}`, &owner)
		recorder := httptest.NewRecorder()
		addSourceHandler(recorder, request)
		if recorder.Code != http.StatusCreated {
			t.Fatalf("same-owner upsert %d got %d: %s", i, recorder.Code, recorder.Body.String())
		}
	}
	var sourceID int64
	var ownerID uint64
	if err := handler.(*sourceAPITestHandler).db.QueryRow(
		`SELECT source_id, usr_id FROM Sources WHERE url = ?`, sourceURL).Scan(&sourceID, &ownerID); err != nil {
		t.Fatalf("read same-owner source: %v", err)
	}
	if ownerID != 7 {
		t.Fatalf("same-owner upsert changed owner to %d", ownerID)
	}
	var provenanceRows int
	if err := handler.(*sourceAPITestHandler).db.QueryRow(
		`SELECT COUNT(*) FROM SourceOwnership WHERE source_id = ? AND user_id = 7`, sourceID).Scan(&provenanceRows); err != nil {
		t.Fatalf("count same-owner provenance: %v", err)
	}
	if provenanceRows != 1 {
		t.Fatalf("same-owner provenance rows = %d, want 1", provenanceRows)
	}
}

func TestAdministratorCanAddExistingOtherOwnerURLWithoutTransfer(t *testing.T) {
	handler := setupSourceOwnershipTest(t)
	insertOwnedSource(t, handler, 42, "https://admin-add.example/", 8)
	admin := sourceOwnershipIdentity(9, "admin-1", "admin")
	request := sourceMutationRequest(http.MethodPost, "/v1/source/add",
		`{"url":"https://admin-add.example/","usr_id":7,"disabled":true}`, &admin)
	recorder := httptest.NewRecorder()
	addSourceHandler(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("admin add got %d: %s", recorder.Code, recorder.Body.String())
	}
	if owner := readSourceOwner(t, handler, 42); owner != 8 {
		t.Fatalf("admin add transferred ownership to %d", owner)
	}
}
