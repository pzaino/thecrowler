//go:build integration

package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	auth "github.com/pzaino/thecrowler/pkg/auth"
	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func openSourceOwnershipPostgresHandler(t *testing.T) cdb.Handler {
	t.Helper()
	if os.Getenv("THECROWLER_POSTGRES_INTEGRATION") != "1" {
		t.Skip("set THECROWLER_POSTGRES_INTEGRATION=1 to run PostgreSQL integration tests")
	}
	port, _ := strconv.Atoi(func() string {
		if v := os.Getenv("DOCKER_POSTGRES_DB_PORT"); v != "" {
			return v
		}
		return "5432"
	}())
	databaseConfig := cfg.Config{Database: cfg.Database{
		Type:               "postgres",
		Host:               envSourceOwnership("DOCKER_POSTGRES_DB_HOST", "127.0.0.1"),
		Port:               port,
		User:               envSourceOwnership("DOCKER_POSTGRES_DB_USER", "postgres"),
		Password:           envSourceOwnership("DOCKER_POSTGRES_PASSWORD", "postgres"),
		DBName:             envSourceOwnership("DOCKER_POSTGRES_DB_NAME", "SitesIndex"),
		SSLMode:            envSourceOwnership("DOCKER_POSTGRES_SSL_MODE", "disable"),
		TransactionTimeout: 30,
	}}
	handler, err := cdb.NewHandler(databaseConfig)
	if err != nil {
		t.Fatalf("create postgres handler: %v", err)
	}
	if err := handler.Connect(databaseConfig); err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	if err := cdb.CheckStartupCompatibility(context.Background(), handler); err != nil {
		t.Fatalf("postgres v1.17 runtime compatibility: %v", err)
	}
	return handler
}

func envSourceOwnership(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func TestPostgresAuthenticatedSourceCreateAndUpdate(t *testing.T) {
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

	ownerID := uint64(880000 + time.Now().UnixNano()%10000)
	otherID := ownerID + 1
	ownerName := fmt.Sprintf("pg-owner-%d", ownerID)
	otherName := fmt.Sprintf("pg-other-%d", otherID)
	if _, err := handler.Exec(`INSERT INTO Users(user_id, username, disabled, deleted_at) VALUES
		($1, $2, FALSE, NULL), ($3, $4, FALSE, NULL)
		ON CONFLICT (user_id) DO UPDATE SET username=EXCLUDED.username, disabled=FALSE, deleted_at=NULL`,
		ownerID, ownerName, otherID, otherName); err != nil {
		t.Fatalf("insert users: %v", err)
	}
	if _, err := handler.Exec(`INSERT INTO UserRoles(user_id, role_id)
		SELECT $1, role_id FROM AuthRoles WHERE name='user'
		ON CONFLICT DO NOTHING`, ownerID); err != nil {
		t.Fatalf("grant owner role: %v", err)
	}
	if _, err := handler.Exec(`INSERT INTO UserRoles(user_id, role_id)
		SELECT $1, role_id FROM AuthRoles WHERE name='user'
		ON CONFLICT DO NOTHING`, otherID); err != nil {
		t.Fatalf("grant other role: %v", err)
	}
	t.Cleanup(func() {
		_, _ = handler.Exec(`DELETE FROM UserRoles WHERE user_id IN ($1,$2)`, ownerID, otherID)
		_, _ = handler.Exec(`DELETE FROM UserScopes WHERE user_id IN ($1,$2)`, ownerID, otherID)
		_, _ = handler.Exec(`DELETE FROM Users WHERE user_id IN ($1,$2)`, ownerID, otherID)
	})

	sourceURL := fmt.Sprintf("https://pg-ownership-%d.example/", time.Now().UnixNano())
	ownerIdentity := auth.Identity{Subject: strconv.FormatUint(ownerID, 10), Username: ownerName, Issuer: "crowler", Roles: []string{"user"}}
	otherIdentity := auth.Identity{Subject: strconv.FormatUint(otherID, 10), Username: otherName, Issuer: "crowler", Roles: []string{"user"}}
	addBody := fmt.Sprintf(`{"url":%q,"usr_id":999999}`, sourceURL)
	addRequest := httptest.NewRequest(http.MethodPost, "/v1/source/add", strings.NewReader(addBody))
	addRequest = addRequest.WithContext(auth.ContextWithIdentity(context.Background(), ownerIdentity))
	addRecorder := httptest.NewRecorder()
	addSourceHandler(addRecorder, addRequest)
	if addRecorder.Code != http.StatusCreated {
		t.Fatalf("add source status %d: %s", addRecorder.Code, addRecorder.Body.String())
	}
	var sourceID uint64
	if err := handler.QueryRow(`SELECT source_id FROM Sources WHERE url=$1`, cdb.NormalizeSourceURL(sourceURL)).Scan(&sourceID); err != nil {
		t.Fatalf("read created source: %v", err)
	}
	var owner uint64
	if err := handler.QueryRow(`SELECT usr_id FROM Sources WHERE source_id=$1`, sourceID).Scan(&owner); err != nil {
		t.Fatalf("read source owner: %v", err)
	}
	if owner != ownerID {
		t.Fatalf("source owner = %d, want authenticated user %d", owner, ownerID)
	}
	t.Cleanup(func() {
		_, _ = handler.Exec(`DELETE FROM Status WHERE source_id=$1`, sourceID)
		_, _ = handler.Exec(`DELETE FROM SourceOwnership WHERE source_id=$1`, sourceID)
		_, _ = handler.Exec(`DELETE FROM Sources WHERE source_id=$1`, sourceID)
	})

	crossUpdate := httptest.NewRequest(http.MethodPost, "/v1/source/update",
		strings.NewReader(fmt.Sprintf(`{"source_id":%d,"status":"pending"}`, sourceID)))
	crossUpdate = crossUpdate.WithContext(auth.ContextWithIdentity(context.Background(), otherIdentity))
	crossRecorder := httptest.NewRecorder()
	updateSourceHandler(crossRecorder, crossUpdate)
	if crossRecorder.Code != http.StatusForbidden {
		t.Fatalf("cross-user update status %d: %s", crossRecorder.Code, crossRecorder.Body.String())
	}
	var status string
	if err := handler.QueryRow(`SELECT status FROM Sources WHERE source_id=$1`, sourceID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "new" {
		t.Fatalf("cross-user update changed status to %q", status)
	}

	ownerUpdate := httptest.NewRequest(http.MethodPost, "/v1/source/update",
		strings.NewReader(fmt.Sprintf(`{"source_id":%d,"status":"pending","usr_id":999999}`, sourceID)))
	ownerUpdate = ownerUpdate.WithContext(auth.ContextWithIdentity(context.Background(), ownerIdentity))
	ownerRecorder := httptest.NewRecorder()
	updateSourceHandler(ownerRecorder, ownerUpdate)
	if ownerRecorder.Code != http.StatusOK {
		t.Fatalf("owner update status %d: %s", ownerRecorder.Code, ownerRecorder.Body.String())
	}
	if err := handler.QueryRow(`SELECT status, usr_id FROM Sources WHERE source_id=$1`, sourceID).Scan(&status, &owner); err != nil {
		t.Fatalf("read updated source: %v", err)
	}
	if status != "pending" || owner != ownerID {
		t.Fatalf("owner update result status=%q owner=%d", status, owner)
	}

	// A same-owner authenticated add of the existing URL remains valid and
	// exercises the PostgreSQL ownership-aware update path.
	sameOwnerBody := fmt.Sprintf(`{"url":%q,"usr_id":999999,"disabled":true}`, sourceURL)
	sameOwnerRequest := httptest.NewRequest(http.MethodPost, "/v1/source/add", strings.NewReader(sameOwnerBody))
	sameOwnerRequest = sameOwnerRequest.WithContext(auth.ContextWithIdentity(context.Background(), ownerIdentity))
	sameOwnerRecorder := httptest.NewRecorder()
	addSourceHandler(sameOwnerRecorder, sameOwnerRequest)
	if sameOwnerRecorder.Code != http.StatusCreated {
		t.Fatalf("same-owner add status %d: %s", sameOwnerRecorder.Code, sameOwnerRecorder.Body.String())
	}
	var sameOwnerDisabled bool
	if err := handler.QueryRow(`SELECT usr_id, disabled FROM Sources WHERE source_id=$1`, sourceID).Scan(&owner, &sameOwnerDisabled); err != nil {
		t.Fatalf("read same-owner add result: %v", err)
	}
	if owner != ownerID || !sameOwnerDisabled {
		t.Fatalf("same-owner add returned owner=%d disabled=%v", owner, sameOwnerDisabled)
	}
}
