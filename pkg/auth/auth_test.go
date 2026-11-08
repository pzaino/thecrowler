package auth

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"

	_ "github.com/mattn/go-sqlite3"
)

func testConfig() cfg.AuthConfig {
	return cfg.AuthConfig{Enabled: true, Mode: "local", Issuer: "crowler", HMACSecret: "test-secret", TokenTTL: 60}
}

func TestHashAndVerifyPassword(t *testing.T) {
	h, err := HashPassword("s3cr3t")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if !VerifyPassword("s3cr3t", h) {
		t.Fatal("expected password to verify")
	}
	if VerifyPassword("wrong", h) {
		t.Fatal("wrong password verified")
	}
}

func TestIssueValidateTokenWithRolesScopes(t *testing.T) {
	tok, err := IssueToken(testConfig(), Identity{Subject: "1", Username: "alice", Roles: []string{"admin"}, Scopes: []string{"events:write"}}, "tid")
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	id, err := ValidateToken(nil, nil, testConfig(), tok)
	if err != nil {
		t.Fatalf("validate token: %v", err)
	}
	if id.Username != "alice" || len(id.Roles) != 1 || id.Roles[0] != "admin" || len(id.Scopes) != 1 || id.Scopes[0] != "events:write" {
		t.Fatalf("unexpected identity: %#v", id)
	}
}

func TestExpiredTokenRejected(t *testing.T) {
	c := testConfig()
	c.TokenTTL = 1
	tok, err := IssueToken(c, Identity{Subject: "1"}, "tid")
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	time.Sleep(1100 * time.Millisecond)
	if _, err := ValidateToken(nil, nil, testConfig(), tok); err == nil {
		t.Fatal("expected expired token to be rejected")
	}
}

func TestMiddlewareDisabledIsBackwardCompatible(t *testing.T) {
	mw := Middleware(cfg.AuthConfig{Enabled: false}, nil)
	rec := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("got status %d", rec.Code)
	}
}

func authIdentityTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
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
		(8, 'disabled-user', TRUE, NULL),
		(9, 'deleted-user', FALSE, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("insert users: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO AuthRoles(role_id, name) VALUES (1, 'admin');
		INSERT INTO AuthScopes(scope_id, name) VALUES (1, 'api:read');
		INSERT INTO UserRoles(user_id, role_id) VALUES (7, 1);
		INSERT INTO RoleScopes(role_id, scope_id) VALUES (1, 1);`); err != nil {
		t.Fatalf("insert grants: %v", err)
	}
	return db
}

func TestResolveActiveIdentity(t *testing.T) {
	db := authIdentityTestDB(t)
	id, err := ResolveActiveIdentity(context.Background(), db, Identity{Subject: "7", Username: "stale", Issuer: "crowler"})
	if err != nil {
		t.Fatalf("resolve active identity: %v", err)
	}
	if id.Username != "owner-1" || id.Subject != "7" || id.Issuer != "crowler" {
		t.Fatalf("unexpected identity: %#v", id)
	}
	if len(id.Roles) != 1 || id.Roles[0] != "admin" || len(id.Scopes) != 1 || id.Scopes[0] != "api:read" {
		t.Fatalf("grants were not reloaded: %#v", id)
	}

	for _, subject := range []string{"8", "9", "missing", "external-subject", "0"} {
		if _, err := ResolveActiveIdentity(context.Background(), db, Identity{Subject: subject, Username: "x", Issuer: "crowler"}); err == nil {
			t.Fatalf("subject %q was accepted", subject)
		}
	}

	_ = db.Close()
	if _, err := ResolveActiveIdentity(context.Background(), db, Identity{Subject: "7", Username: "owner-1", Issuer: "crowler"}); err == nil {
		t.Fatal("closed database was accepted")
	}
}
