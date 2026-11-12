package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	auth "github.com/pzaino/thecrowler/pkg/auth"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	"golang.org/x/time/rate"
)

func setupInformationSeedAuthTest(t *testing.T) (cdb.Handler, func()) {
	t.Helper()
	handler, cleanup := setupInformationSeedAPITestDB(t)
	oldHandler := dbHandler
	oldGate := dbAdmission
	oldAuth := config.API.Auth
	dbHandler = handler
	dbAdmission = newDBAdmissionGate(1)
	config.API.Auth = cfg.AuthConfig{Enabled: true, Mode: "local", Issuer: "crowler", HMACSecret: "test-secret", TokenTTL: 60}
	t.Cleanup(func() {
		dbHandler = oldHandler
		dbAdmission = oldGate
		config.API.Auth = oldAuth
		cleanup()
	})
	db := handler.(*informationSeedAPITestHandler).db
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
		(9, 'admin-1', FALSE, NULL)`); err != nil {
		t.Fatalf("insert users: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO AuthRoles(role_id, name) VALUES (1, 'admin'), (2, 'user');
		INSERT INTO UserRoles(user_id, role_id) VALUES (7, 2), (8, 2), (9, 1);`); err != nil {
		t.Fatalf("insert roles: %v", err)
	}
	return handler, func() {}
}

func createInformationSeedForAuthTest(t *testing.T, handler cdb.Handler, owner uint64, text string) uint64 {
	t.Helper()
	id, err := cdb.CreateInformationSeed(&handler, &cdb.InformationSeed{InformationSeed: text, UsrID: owner, Status: "new"})
	if err != nil {
		t.Fatalf("create information seed: %v", err)
	}
	return id
}

func TestInformationSeedMutationAuthorization(t *testing.T) {
	handler, _ := setupInformationSeedAuthTest(t)
	seedID := createInformationSeedForAuthTest(t, handler, 7, "owned seed")
	owner := sourceOwnershipIdentity(7, "owner-1", "user")
	other := sourceOwnershipIdentity(8, "owner-2", "user")
	admin := sourceOwnershipIdentity(9, "admin-1", "admin")

	for _, test := range []struct {
		name     string
		identity *auth.Identity
		want     int
	}{
		{name: "unauthenticated", identity: nil, want: http.StatusUnauthorized},
		{name: "cross owner", identity: &other, want: http.StatusForbidden},
		{name: "owner", identity: &owner, want: http.StatusOK},
		{name: "admin", identity: &admin, want: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := sourceMutationRequest(http.MethodPost, "/v1/information_seed/"+strconvFormat(seedID)+"/disable", "", test.identity)
			recorder := httptest.NewRecorder()
			informationSeedPathDisableHandler(recorder, request)
			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, test.want, recorder.Body.String())
			}
			if test.name == "cross owner" || test.name == "unauthenticated" {
				seed, err := cdb.GetInformationSeedByID(&handler, seedID)
				if err != nil {
					t.Fatalf("load seed: %v", err)
				}
				if seed.Disabled {
					t.Fatal("unauthorized request changed the seed")
				}
			}
		})
	}
}

func TestInformationSeedAddBindsAuthenticatedOwner(t *testing.T) {
	_, _ = setupInformationSeedAuthTest(t)
	owner := sourceOwnershipIdentity(7, "owner-1", "user")
	request := sourceMutationRequest(http.MethodPost, "/v1/information_seed/add",
		`{"information_seed":"bound seed","usr_id":8}`, &owner)
	recorder := httptest.NewRecorder()
	informationSeedAddHandler(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-owner add got %d, want 403: %s", recorder.Code, recorder.Body.String())
	}

	request = sourceMutationRequest(http.MethodPost, "/v1/information_seed/add",
		`{"information_seed":"bound seed"}`, &owner)
	recorder = httptest.NewRecorder()
	informationSeedAddHandler(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("owner add got %d, want 201: %s", recorder.Code, recorder.Body.String())
	}
	var response InformationSeedResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode add response: %v", err)
	}
	if response.Item.UsrID != 7 {
		t.Fatalf("seed owner = %d, want authenticated user 7", response.Item.UsrID)
	}
}

func TestInformationSeedListForcesOwnerFilter(t *testing.T) {
	handler, _ := setupInformationSeedAuthTest(t)
	createInformationSeedForAuthTest(t, handler, 7, "owner seed")
	createInformationSeedForAuthTest(t, handler, 8, "other seed")
	owner := sourceOwnershipIdentity(7, "owner-1", "user")
	request := sourceMutationRequest(http.MethodGet, "/v1/information_seed/list?usr_id=8", "", &owner)
	recorder := httptest.NewRecorder()
	informationSeedListHandler(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list got %d: %s", recorder.Code, recorder.Body.String())
	}
	var response InformationSeedListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(response.Items) != 1 || response.Items[0].UsrID != 7 {
		t.Fatalf("cross-owner list leaked rows: %#v", response.Items)
	}
}

func TestInformationSeedProviderConfigRequiresAdmin(t *testing.T) {
	_, _ = setupInformationSeedAuthTest(t)
	owner := sourceOwnershipIdentity(7, "owner-1", "user")
	admin := sourceOwnershipIdentity(9, "admin-1", "admin")

	request := sourceMutationRequest(http.MethodGet, "/v1/config/information_seed/providers", "", &owner)
	recorder := httptest.NewRecorder()
	informationSeedConfigProvidersHandler(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("ordinary user provider config got %d, want 403", recorder.Code)
	}

	request = sourceMutationRequest(http.MethodGet, "/v1/config/information_seed/providers", "", &admin)
	recorder = httptest.NewRecorder()
	informationSeedConfigProvidersHandler(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("admin provider config got %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
}

func strconvFormat(value uint64) string {
	return strconv.FormatUint(value, 10)
}

func TestInformationSeedReadRoutesRejectCrossOwner(t *testing.T) {
	handler, _ := setupInformationSeedAuthTest(t)
	seedID := createInformationSeedForAuthTest(t, handler, 7, "read-owned")
	other := sourceOwnershipIdentity(8, "owner-2", "user")
	paths := []struct {
		name    string
		handler http.HandlerFunc
		path    string
	}{
		{name: "status", handler: informationSeedStatusHandler, path: "/v1/information_seed/status?information_seed_id=" + strconvFormat(seedID)},
		{name: "sources", handler: informationSeedSourcesHandler, path: "/v1/information_seed/sources?information_seed_id=" + strconvFormat(seedID)},
		{name: "candidates", handler: informationSeedCandidateDecisionsHandler, path: "/v1/information_seed/candidates?information_seed_id=" + strconvFormat(seedID)},
		{name: "events", handler: informationSeedEventsHandler, path: "/v1/information_seed/" + strconvFormat(seedID) + "/events"},
		{name: "diagnostics", handler: informationSeedDiagnosticsHandler, path: "/v1/information_seed/" + strconvFormat(seedID) + "/diagnostics"},
	}
	for _, test := range paths {
		t.Run(test.name, func(t *testing.T) {
			request := sourceMutationRequest(http.MethodGet, test.path, "", &other)
			recorder := httptest.NewRecorder()
			test.handler(recorder, request)
			if recorder.Code != http.StatusForbidden {
				t.Fatalf("%s cross-owner status = %d, want 403: %s", test.name, recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestInformationSeedUpdateCannotTransferOwnership(t *testing.T) {
	handler, _ := setupInformationSeedAuthTest(t)
	seedID := createInformationSeedForAuthTest(t, handler, 7, "transfer-protected")
	owner := sourceOwnershipIdentity(7, "owner-1", "user")
	request := sourceMutationRequest(http.MethodPost, "/v1/information_seed/update",
		`{"information_seed_id":`+strconvFormat(seedID)+`,"usr_id":8}`, &owner)
	recorder := httptest.NewRecorder()
	informationSeedUpdateHandler(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("ownership transfer got %d, want 403: %s", recorder.Code, recorder.Body.String())
	}
	seed, err := cdb.GetInformationSeedByID(&handler, seedID)
	if err != nil {
		t.Fatalf("load seed: %v", err)
	}
	if seed.UsrID != 7 {
		t.Fatalf("seed owner changed to %d", seed.UsrID)
	}
}

func TestInformationSeedRoutesRequireAuthenticationMiddleware(t *testing.T) {
	_, _ = setupInformationSeedAuthTest(t)
	oldMux := http.DefaultServeMux
	oldLimiter := limiter
	oldConfig := config
	http.DefaultServeMux = http.NewServeMux()
	limiter = rate.NewLimiter(rate.Inf, 0)
	config.API.EnableConsole = true
	config.API.EnableAPIDocs = false
	config.API.Plugins.Enabled = false
	t.Cleanup(func() {
		http.DefaultServeMux = oldMux
		limiter = oldLimiter
		config = oldConfig
	})
	initAPIv1()
	for _, test := range []struct{ method, path string }{
		{method: http.MethodPost, path: "/v1/information_seed/add"},
		{method: http.MethodPost, path: "/v1/information_seed/1/disable"},
		{method: http.MethodPost, path: "/v1/information_seed/1/enable"},
		{method: http.MethodPost, path: "/v1/information_seed/1/rerun"},
	} {
		request := httptest.NewRequest(test.method, test.path, strings.NewReader(`{}`))
		recorder := httptest.NewRecorder()
		http.DefaultServeMux.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s without token = %d, want 401", test.method, test.path, recorder.Code)
		}
	}
}
