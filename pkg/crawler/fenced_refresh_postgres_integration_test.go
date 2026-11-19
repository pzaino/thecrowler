//go:build integration

// Copyright 2023 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package crawler

// PostgreSQL integration coverage for the atomic fenced refresh path
// (WP-05B3C3B2F1 / B3 Decision 2). These tests use a disposable database
// initialised from pkg/database/postgresql-setup.pgsql and a restricted runtime
// role; they are skipped unless THECROWLER_POSTGRES_INTEGRATION=1.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	detect "github.com/pzaino/thecrowler/pkg/detection"
)

func fencedRefreshPGValue(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func openFencedRefreshPostgres(t *testing.T) (cdb.Handler, *sql.DB) {
	t.Helper()
	if os.Getenv("THECROWLER_POSTGRES_INTEGRATION") != "1" {
		t.Skip("set THECROWLER_POSTGRES_INTEGRATION=1 to run PostgreSQL integration tests")
	}
	port, err := strconv.Atoi(fencedRefreshPGValue("DOCKER_POSTGRES_DB_PORT", "5432"))
	if err != nil {
		t.Fatalf("invalid DOCKER_POSTGRES_DB_PORT: %v", err)
	}
	host := fencedRefreshPGValue("DOCKER_POSTGRES_DB_HOST", "127.0.0.1")
	user := fencedRefreshPGValue("DOCKER_POSTGRES_DB_USER",
		fencedRefreshPGValue("DOCKER_POSTGRES_USER", "postgres"))
	password := fencedRefreshPGValue("DOCKER_POSTGRES_PASSWORD", "postgres")
	dbname := fencedRefreshPGValue("DOCKER_POSTGRES_DB_NAME", "SitesIndex")
	sslmode := fencedRefreshPGValue("DOCKER_POSTGRES_SSL_MODE", "disable")

	configuration := cfg.Config{
		Database: cfg.Database{
			Type: cdb.DBPostgresStr, Host: host, Port: port,
			User: user, Password: password, DBName: dbname, SSLMode: sslmode,
			MaxConns: 4, MaxIdleConns: 2,
		},
	}
	handler, err := cdb.NewHandler(configuration)
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	if err := handler.Connect(configuration); err != nil {
		t.Fatalf("connect handler: %v", err)
	}
	t.Cleanup(func() { _ = handler.Close() })

	adminPassword := fencedRefreshPGValue("POSTGRES_PASSWORD", password)
	adminUser := fencedRefreshPGValue("POSTGRES_USER", "postgres")
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		host, port, adminUser, adminPassword, dbname, sslmode)
	admin, err := sql.Open(cdb.DBPostgresStr, dsn)
	if err != nil {
		t.Fatalf("open admin DB: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	return handler, admin
}

func fencedRefreshHash(label string) string {
	sum := sha256.Sum256([]byte(label))
	return hex.EncodeToString(sum[:])
}

func fencedRefreshUnique(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func fencedRefreshSetMode(t *testing.T, admin *sql.DB, mode string) {
	t.Helper()
	if _, err := admin.Exec(`SELECT set_source_crawl_claim_mode($1)`, mode); err != nil {
		t.Fatalf("set claim mode %q: %v", mode, err)
	}
}

// fencedRefreshEnvironment creates a source and a real fenced claim, returns
// the source URL, the claimed source ID and a ProcessContext wired to the real
// handoff. Cleanup restores the previous claim mode.
func fencedRefreshEnvironment(t *testing.T, handler cdb.Handler, admin *sql.DB,
	label string) (string, uint64, *ProcessContext, *cdb.SourceCrawlHandoff, cdb.SourceCrawlClaim) {
	t.Helper()

	var previousMode string
	if err := admin.QueryRow(`SELECT mode FROM SourceCrawlClaimPolicy WHERE policy_id = 1`).
		Scan(&previousMode); err != nil {
		t.Fatalf("read claim mode: %v", err)
	}
	fencedRefreshSetMode(t, admin, "fenced")
	t.Cleanup(func() { fencedRefreshSetMode(t, admin, previousMode) })

	unique := fencedRefreshUnique(label)
	priority := "b2f1-" + unique
	sourceURL := "https://fenced-refresh.invalid/" + unique + "/"
	sourceName := "fenced refresh " + unique
	sourceUID := cdb.CalculateSourceUID(sourceName, sourceURL)

	var sourceID uint64
	if err := handler.QueryRow(`
		INSERT INTO Sources
			(source_uid, url, name, status, restricted, disabled, usr_id,
			 priority, sub_priority, config)
		VALUES ($1, $2, $3, 'new', 0, FALSE, 7, $4, 0, '{}'::jsonb)
		RETURNING source_id`,
		sourceUID, sourceURL, sourceName, priority).Scan(&sourceID); err != nil {
		t.Fatalf("insert source: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(`DELETE FROM SourceCrawlClaims WHERE source_id = $1`, sourceID)
		_, _ = admin.Exec(`DELETE FROM SourceCrawlAttempts WHERE source_id = $1`, sourceID)
		_, _ = admin.Exec(`DELETE FROM SearchIndex WHERE page_url LIKE $1`,
			"https://fenced-refresh.invalid/"+unique+"/%")
		_, _ = admin.Exec(`DELETE FROM Sources WHERE source_id = $1`, sourceID)
	})

	request := cdb.SourceCrawlClaimRequest{
		ClaimRequestID:    fencedRefreshHash("claim-" + unique),
		Limit:             1,
		Priority:          priority,
		EngineID:          "b2f1-test-engine",
		Lease:             30 * time.Minute,
		ProcessingTimeout: 5 * time.Minute,
	}
	claims, err := cdb.ClaimSourceCrawlAttempts(context.Background(), &handler, request)
	if err != nil || len(claims) != 1 || claims[0].SourceID != sourceID {
		t.Fatalf("claim = %+v err=%v", claims, err)
	}
	handoff, err := cdb.NewSourceCrawlHandoff(claims[0])
	if err != nil {
		t.Fatalf("handoff: %v", err)
	}

	ctx := &ProcessContext{
		source:      &cdb.Source{ID: sourceID, URL: sourceURL},
		db:          &handler,
		sourceCrawl: handoff,
		Status:      &Status{},
	}
	ctx.config.Crawler.RefreshContent = true
	ctx.config.Crawler.IndexingConcurrency = 0
	return sourceURL, sourceID, ctx, handoff, claims[0]
}

func fencedRefreshPageInfo(ctx *ProcessContext, sourceID uint64, body string) *PageInfo {
	return &PageInfo{
		Config:       &ctx.config,
		sourceID:     sourceID,
		BodyText:     body,
		DetectedTech: map[string]detect.DetectedEntity{},
	}
}

func fencedRefreshPageIndex(t *testing.T, handler cdb.Handler, pageURL string) uint64 {
	t.Helper()
	var indexID uint64
	if err := handler.QueryRow(`SELECT index_id FROM SearchIndex WHERE page_url = $1`,
		pageURL).Scan(&indexID); err != nil {
		t.Fatalf("read page index: %v", err)
	}
	return indexID
}

func fencedRefreshPageObjects(t *testing.T, handler cdb.Handler, indexID uint64) []string {
	t.Helper()
	var joined string
	if err := handler.QueryRow(`
		SELECT COALESCE(string_agg(w.object_hash, ',' ORDER BY w.object_hash), '')
		FROM WebObjectsIndex wi
		JOIN WebObjects w ON w.object_id = wi.object_id
		WHERE wi.index_id = $1`, indexID).Scan(&joined); err != nil {
		t.Fatalf("read page objects: %v", err)
	}
	if joined == "" {
		return nil
	}
	return strings.Split(joined, ",")
}

func TestPostgresFencedRefreshAtomicReplacement(t *testing.T) {
	handler, admin := openFencedRefreshPostgres(t)
	_, sourceID, ctx, _, _ := fencedRefreshEnvironment(t, handler, admin, "atomic")
	pageURL := ctx.source.URL + "page"

	if _, err := indexPageContext(context.Background(), ctx, pageURL,
		fencedRefreshPageInfo(ctx, sourceID, "generation one")); err != nil {
		t.Fatalf("initial index: %v", err)
	}
	indexID := fencedRefreshPageIndex(t, handler, pageURL)
	if objects := fencedRefreshPageObjects(t, handler, indexID); len(objects) != 1 {
		t.Fatalf("initial page objects = %v", objects)
	}
	var oldHash string
	if err := handler.QueryRow(`
		SELECT w.object_hash FROM WebObjectsIndex wi
		JOIN WebObjects w ON w.object_id = wi.object_id
		WHERE wi.index_id = $1`, indexID).Scan(&oldHash); err != nil {
		t.Fatalf("read old hash: %v", err)
	}

	if _, err := indexPageContext(context.Background(), ctx, pageURL,
		fencedRefreshPageInfo(ctx, sourceID, "generation two")); err != nil {
		t.Fatalf("fenced refresh: %v", err)
	}

	objects := fencedRefreshPageObjects(t, handler, indexID)
	if len(objects) != 1 || objects[0] == oldHash {
		t.Fatalf("refresh page objects = %v (old %s)", objects, oldHash)
	}
	var oldRows int
	if err := handler.QueryRow(`SELECT COUNT(*) FROM WebObjects WHERE object_hash = $1`,
		oldHash).Scan(&oldRows); err != nil {
		t.Fatalf("count old object: %v", err)
	}
	if oldRows != 0 {
		t.Fatalf("old orphan object survived: %d", oldRows)
	}
}

func TestPostgresFencedRefreshPreservesSharedObject(t *testing.T) {
	handler, admin := openFencedRefreshPostgres(t)
	_, sourceID, ctx, _, _ := fencedRefreshEnvironment(t, handler, admin, "shared")
	pageURL := ctx.source.URL + "page"
	otherURL := ctx.source.URL + "duplicate"

	const sharedContent = "identical shared content"
	if _, err := indexPageContext(context.Background(), ctx, pageURL,
		fencedRefreshPageInfo(ctx, sourceID, sharedContent)); err != nil {
		t.Fatalf("initial index: %v", err)
	}
	if _, err := indexPageContext(context.Background(), ctx, otherURL,
		fencedRefreshPageInfo(ctx, sourceID, sharedContent)); err != nil {
		t.Fatalf("duplicate index: %v", err)
	}
	pageIndex := fencedRefreshPageIndex(t, handler, pageURL)
	otherIndex := fencedRefreshPageIndex(t, handler, otherURL)
	if len(fencedRefreshPageObjects(t, handler, pageIndex)) != 1 ||
		len(fencedRefreshPageObjects(t, handler, otherIndex)) != 1 {
		t.Fatal("shared pages did not converge on one object each")
	}

	if _, err := indexPageContext(context.Background(), ctx, pageURL,
		fencedRefreshPageInfo(ctx, sourceID, "changed content")); err != nil {
		t.Fatalf("fenced refresh: %v", err)
	}

	if objects := fencedRefreshPageObjects(t, handler, pageIndex); len(objects) != 1 {
		t.Fatalf("refreshed page objects = %v", objects)
	}
	if objects := fencedRefreshPageObjects(t, handler, otherIndex); len(objects) != 1 {
		t.Fatalf("unrelated page lost its shared object: %v", objects)
	}
}

func TestPostgresFencedRefreshRefusesCrossSourcePage(t *testing.T) {
	handler, admin := openFencedRefreshPostgres(t)
	sourceURL, sourceID, ctx, _, _ := fencedRefreshEnvironment(t, handler, admin, "crosssource")
	pageURL := sourceURL + "page"
	if _, err := indexPageContext(context.Background(), ctx, pageURL,
		fencedRefreshPageInfo(ctx, sourceID, "generation one")); err != nil {
		t.Fatalf("initial index: %v", err)
	}
	indexID := fencedRefreshPageIndex(t, handler, pageURL)

	unique := fencedRefreshUnique("other-source")
	var otherID uint64
	if err := admin.QueryRow(`
		INSERT INTO Sources
			(source_uid, url, name, status, restricted, disabled, usr_id,
			 priority, sub_priority, config)
		VALUES ($1, $2, 'other source', 'new', 0, FALSE, 8, $3, 0, '{}'::jsonb)
		RETURNING source_id`,
		cdb.CalculateSourceUID("other source", "https://other-source.invalid/"+unique),
		"https://other-source.invalid/"+unique,
		"b2f1-other-"+unique).Scan(&otherID); err != nil {
		t.Fatalf("insert other source: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(`DELETE FROM SourceSearchIndex WHERE source_id = $1`, otherID)
		_, _ = admin.Exec(`DELETE FROM Sources WHERE source_id = $1`, otherID)
	})
	if _, err := admin.Exec(`INSERT INTO SourceSearchIndex (source_id, index_id) VALUES ($1, $2)`,
		otherID, indexID); err != nil {
		t.Fatalf("link other source: %v", err)
	}

	_, err := indexPageContext(context.Background(), ctx, pageURL,
		fencedRefreshPageInfo(ctx, sourceID, "generation two"))
	if !errors.Is(err, errFencedRefreshCrossSourcePage) {
		t.Fatalf("cross-source refresh error = %v", err)
	}
	objects := fencedRefreshPageObjects(t, handler, indexID)
	if len(objects) != 1 {
		t.Fatalf("cross-source refusal changed page objects: %v", objects)
	}
	var content string
	if err := handler.QueryRow(`
		SELECT w.object_content FROM WebObjectsIndex wi
		JOIN WebObjects w ON w.object_id = wi.object_id
		WHERE wi.index_id = $1`, indexID).Scan(&content); err != nil {
		t.Fatalf("read page content: %v", err)
	}
	if content != "generation one" {
		t.Fatalf("cross-source refusal changed content: %q", content)
	}
}

func TestPostgresFencedRefreshRollbackRestoresOldGeneration(t *testing.T) {
	handler, admin := openFencedRefreshPostgres(t)
	_, sourceID, ctx, _, _ := fencedRefreshEnvironment(t, handler, admin, "rollback")
	pageURL := ctx.source.URL + "page"
	if _, err := indexPageContext(context.Background(), ctx, pageURL,
		fencedRefreshPageInfo(ctx, sourceID, "generation one")); err != nil {
		t.Fatalf("initial index: %v", err)
	}
	indexID := fencedRefreshPageIndex(t, handler, pageURL)
	oldHash := fencedRefreshPageObjects(t, handler, indexID)[0]

	if _, err := admin.Exec(`
		CREATE OR REPLACE FUNCTION b2f1_fail_refresh() RETURNS trigger AS $$
		BEGIN
			RAISE EXCEPTION 'b2f1 injected refresh failure';
		END;
		$$ LANGUAGE plpgsql`); err != nil {
		t.Fatalf("create failure function: %v", err)
	}
	triggerName := "b2f1_fail_refresh_" + strings.ReplaceAll(fmt.Sprint(time.Now().UnixNano()), "-", "")
	if _, err := admin.Exec(fmt.Sprintf(`
		CREATE TRIGGER %s BEFORE DELETE ON WebObjects
		FOR EACH ROW WHEN (OLD.object_hash = '%s')
		EXECUTE FUNCTION b2f1_fail_refresh()`, triggerName, oldHash)); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON WebObjects`, triggerName))
		_, _ = admin.Exec(`DROP FUNCTION IF EXISTS b2f1_fail_refresh()`)
	})

	if _, err := indexPageContext(context.Background(), ctx, pageURL,
		fencedRefreshPageInfo(ctx, sourceID, "generation two")); err == nil {
		t.Fatal("injected failure did not fail the refresh")
	}

	objects := fencedRefreshPageObjects(t, handler, indexID)
	if len(objects) != 1 || objects[0] != oldHash {
		t.Fatalf("rollback page objects = %v want %s", objects, oldHash)
	}
	var content string
	if err := handler.QueryRow(`
		SELECT w.object_content FROM WebObjectsIndex wi
		JOIN WebObjects w ON w.object_id = wi.object_id
		WHERE wi.index_id = $1`, indexID).Scan(&content); err != nil {
		t.Fatalf("read content: %v", err)
	}
	if content != "generation one" {
		t.Fatalf("rollback content = %q", content)
	}
}

func TestPostgresFencedRefreshRejectsExpiredClaim(t *testing.T) {
	handler, admin := openFencedRefreshPostgres(t)
	_, sourceID, ctx, handoff, claim := fencedRefreshEnvironment(t, handler, admin, "expired")
	pageURL := ctx.source.URL + "page"
	if _, err := indexPageContext(context.Background(), ctx, pageURL,
		fencedRefreshPageInfo(ctx, sourceID, "generation one")); err != nil {
		t.Fatalf("initial index: %v", err)
	}
	indexID := fencedRefreshPageIndex(t, handler, pageURL)

	now := time.Now().UTC().Truncate(time.Microsecond)
	claimedAt := now.Add(-2 * time.Hour)
	leaseExpired := now.Add(-time.Hour)
	if _, err := admin.Exec(`
		UPDATE SourceCrawlClaims SET claimed_at = $1, lease_expires_at = $2
		WHERE source_id = $3`, claimedAt, leaseExpired, sourceID); err != nil {
		t.Fatalf("expire current claim: %v", err)
	}
	if _, err := admin.Exec(`
		UPDATE SourceCrawlAttempts SET claimed_at = $1, lease_expires_at = $2
		WHERE source_id = $3 AND attempt_id = $4`,
		claimedAt, leaseExpired, sourceID, handoff.AttemptID()); err != nil {
		t.Fatalf("expire attempt: %v", err)
	}
	claim.ClaimedAt = claimedAt
	claim.LeaseExpiresAt = leaseExpired
	expiredHandoff, err := cdb.NewSourceCrawlHandoff(claim)
	if err != nil {
		t.Fatalf("expired handoff: %v", err)
	}
	ctx.sourceCrawl = expiredHandoff

	_, err = indexPageContext(context.Background(), ctx, pageURL,
		fencedRefreshPageInfo(ctx, sourceID, "generation two"))
	if err == nil {
		t.Fatal("expired claim performed a refresh")
	}
	objects := fencedRefreshPageObjects(t, handler, indexID)
	if len(objects) != 1 {
		t.Fatalf("expired claim changed page objects: %v", objects)
	}
	var content string
	if err := handler.QueryRow(`
		SELECT w.object_content FROM WebObjectsIndex wi
		JOIN WebObjects w ON w.object_id = wi.object_id
		WHERE wi.index_id = $1`, indexID).Scan(&content); err != nil {
		t.Fatalf("read content: %v", err)
	}
	if content != "generation one" {
		t.Fatalf("expired claim changed content: %q", content)
	}
}
