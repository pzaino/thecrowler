//go:build integration

package agent

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func integrationToolFixture(t *testing.T, db cdb.Handler, sourceID uint64, indexURL string) (uint64, uint64) {
	t.Helper()
	valueHash := fmt.Sprintf("%x", sha256.Sum256([]byte("42")))
	var indexID uint64
	if err := db.QueryRowContext(context.Background(),
		`INSERT INTO SearchIndex (page_url, title, summary, last_updated_at) VALUES ($1,$2,$3,NOW()) RETURNING index_id`,
		indexURL, "WP03A fixture", "WP03A metadata fixture",
	).Scan(&indexID); err != nil {
		t.Fatalf("create search index: %v", err)
	}
	var objectID uint64
	if err := db.QueryRowContext(context.Background(),
		`INSERT INTO WebObjects (object_link, object_type, object_hash, details, last_updated_at)
		 VALUES ('db','text/html',$1,'{}'::jsonb,NOW()) RETURNING object_id`,
		fmt.Sprintf("%x", sha256.Sum256([]byte(indexURL))),
	).Scan(&objectID); err != nil {
		t.Fatalf("create web object: %v", err)
	}
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO WebObjectsIndex (index_id, object_id) VALUES ($1,$2)`, indexID, objectID); err != nil {
		t.Fatalf("link web object index: %v", err)
	}
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO SourceSearchIndex (source_id, index_id) VALUES ($1,$2)`, sourceID, indexID); err != nil {
		t.Fatalf("link source index: %v", err)
	}
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO ObjectAttributes (object_id, object_type, attribute_key, attribute_value, normalized_value, value_hash, last_updated_at)
		 VALUES ($1,'webobject','price','42','42',$2,NOW())`, objectID, valueHash); err != nil {
		t.Fatalf("create object attribute: %v", err)
	}
	return indexID, objectID
}

func integrationToolAuth(sourceID uint64) ToolAuthContext {
	return ToolAuthContext{
		Identity: AgentIdentity{
			AgentID: "wp03a-integration", TrustLevel: "trusted",
			Capabilities: []string{"db_read"},
		},
		Enforcement: true,
		Allowlist: map[string]bool{
			"get_source_crawl_status":     true,
			"find_scraped_field_metadata": true,
			"find_artifact_metadata":      true,
		},
		AllowedSources: map[uint64]bool{sourceID: true},
		RunID:          "wp03a-integration-run",
		TraceID:        "wp03a-integration-trace",
	}
}

// TestPostgresReadOnlyMetadataTools exercises the new native tools against a
// disposable schema and verifies source filtering happens before LIMIT.
func TestPostgresReadOnlyMetadataTools(t *testing.T) {
	if os.Getenv("THECROWLER_POSTGRES_INTEGRATION") != "1" {
		t.Skip("set THECROWLER_POSTGRES_INTEGRATION=1 to run PostgreSQL integration tests")
	}
	config := liveOllamaDBConfig(t)
	preflightTestDatabase(t, config)
	db, err := cdb.NewHandler(config)
	if err != nil {
		t.Fatalf("create database handler: %v", err)
	}
	if err := db.Connect(config); err != nil {
		t.Fatalf("connect database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	suffix := time.Now().UnixNano()
	newSource := func(name string) uint64 {
		source := &cdb.Source{
			URL:  fmt.Sprintf("https://wp03a-tools-%s-%d.example/", name, suffix),
			Name: fmt.Sprintf("wp03a-tools-%s-%d", name, suffix), Priority: "high", Restricted: 1,
		}
		id, err := cdb.CreateSourceContext(context.Background(), &db, source, cfg.SourceConfig{})
		if err != nil {
			t.Fatalf("create source %s: %v", name, err)
		}
		return id
	}
	sourceID := newSource("first")
	otherSourceID := newSource("second")
	firstPageURL := fmt.Sprintf("https://wp03a-tools-first-%d.example/page", suffix)
	secondPageURL := fmt.Sprintf("https://wp03a-tools-second-%d.example/page", suffix)
	indexID, objectID := integrationToolFixture(t, db, sourceID, firstPageURL)
	otherIndexID, otherObjectID := integrationToolFixture(t, db, otherSourceID, secondPageURL)
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM ObjectAttributes WHERE object_id IN ($1,$2) AND object_type='webobject'`, objectID, otherObjectID)
		_, _ = db.Exec(`DELETE FROM WebObjectsIndex WHERE object_id IN ($1,$2)`, objectID, otherObjectID)
		_, _ = db.Exec(`DELETE FROM WebObjects WHERE object_id IN ($1,$2)`, objectID, otherObjectID)
		_, _ = db.Exec(`DELETE FROM Sources WHERE source_id IN ($1,$2)`, sourceID, otherSourceID)
		_, _ = db.Exec(`DELETE FROM SearchIndex WHERE index_id IN ($1,$2)`, indexID, otherIndexID)
	})

	ctx := ContextWithToolRuntime(context.Background(), ToolRuntime{
		DB: db, Auth: integrationToolAuth(sourceID), Deadline: time.Now().Add(time.Minute),
	})
	auth := integrationToolAuth(sourceID)
	if err := AuthorizeToolCall(auth, &sourceCrawlStatusTool{}, map[string]any{"source_id": float64(sourceID)}); err != nil {
		t.Fatalf("authorize crawl status: %v", err)
	}
	status, err := (&sourceCrawlStatusTool{}).Execute(ctx, map[string]any{"source_id": float64(sourceID)})
	if err != nil {
		t.Fatalf("crawl status: %v", err)
	}
	if status["status"] != "new" || status["status_records"] != 1 || status["source_uid"] == "" {
		t.Fatalf("unexpected crawl status: %v", status)
	}

	args := map[string]any{
		"source_id": float64(sourceID), "field": "price", "value": "42", "limit": float64(5),
	}
	if err := AuthorizeToolCall(auth, &scrapedFieldMetadataTool{}, args); err != nil {
		t.Fatalf("authorize scraped metadata: %v", err)
	}
	scraped, err := (&scrapedFieldMetadataTool{}).Execute(ctx, args)
	if err != nil {
		t.Fatalf("scraped metadata: %v", err)
	}
	if scraped["count"] != 1 {
		t.Fatalf("cross-source scraped rows leaked: %v", scraped)
	}
	scrapedMatches := scraped["matches"].([]any)
	scrapedMatch := scrapedMatches[0].(map[string]any)
	if scrapedMatch["index_id"] != indexID || scrapedMatch["page_url"] != firstPageURL {
		t.Fatalf("unexpected scraped metadata: %v", scrapedMatch)
	}
	if strings.Contains(fmt.Sprint(scraped), "json_val") || strings.Contains(fmt.Sprint(scraped), "attribute_value") {
		t.Fatalf("raw scraped field leaked: %v", scraped)
	}

	if err := AuthorizeToolCall(auth, &artifactMetadataTool{}, args); err != nil {
		t.Fatalf("authorize artifact metadata: %v", err)
	}
	artifacts, err := (&artifactMetadataTool{}).Execute(ctx, args)
	if err != nil {
		t.Fatalf("artifact metadata: %v", err)
	}
	if artifacts["count"] != 1 {
		t.Fatalf("cross-source artifact rows leaked: %v", artifacts)
	}
	artifactItems := artifacts["artifacts"].([]any)
	artifact := artifactItems[0].(map[string]any)
	if artifact["artifact_id"] != objectID || artifact["source_type"] != "webobject" {
		t.Fatalf("unexpected artifact metadata: %v", artifact)
	}
	if strings.Contains(fmt.Sprint(artifacts), "json_val") || strings.Contains(fmt.Sprint(artifacts), "attribute_value") {
		t.Fatalf("raw artifact field leaked: %v", artifacts)
	}
}
