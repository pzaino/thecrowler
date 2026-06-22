package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
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
