//go:build integration

package crawler

// Legacy refresh_content integration coverage.
//
// RC2-B moved the legacy refresh onto the same in-transaction, page-scoped
// implementation the fenced path uses. This test pins the replacement path
// through the real indexPageContext entry point (no fenced handoff) so the
// database fixture cannot drift away from the production refresh code.

import (
	"context"
	"testing"

	cdb "github.com/pzaino/thecrowler/pkg/database"
	detect "github.com/pzaino/thecrowler/pkg/detection"
)

// openLegacyRefreshContext builds a ProcessContext that takes the legacy path:
// RefreshContent enabled and no fenced handoff.
func openLegacyRefreshContext(t *testing.T, handler cdb.Handler,
	sourceID uint64, pageURL string) *ProcessContext {
	t.Helper()
	ctx := &ProcessContext{
		source: &cdb.Source{ID: sourceID, URL: pageURL},
		db:     &handler,
		Status: &Status{},
	}
	ctx.config.Crawler.RefreshContent = true
	ctx.config.Crawler.IndexingConcurrency = 0
	return ctx
}

func legacyRefreshPageInfo(ctx *ProcessContext, sourceID uint64, body string) *PageInfo {
	return &PageInfo{
		Config:       &ctx.config,
		sourceID:     sourceID,
		BodyText:     body,
		DetectedTech: map[string]detect.DetectedEntity{},
	}
}

// TestPostgresLegacyRefreshReplacesPageGeneration confirms that a legacy
// refresh (RefreshContent on, no fenced claim) replaces one page generation
// with the next: the old page-scoped link and its orphaned object are removed
// and exactly one link to the new object remains.
func TestPostgresLegacyRefreshReplacesPageGeneration(t *testing.T) {
	handler, admin := openFencedRefreshPostgres(t)
	unique := fencedRefreshUnique("legacy-gen")
	sourceID := fencedRefreshSeedSource(t, admin, unique)
	pageURL := "https://" + unique + ".invalid/page"

	ctx := openLegacyRefreshContext(t, handler, sourceID, pageURL)
	t.Cleanup(func() {
		_, _ = admin.Exec(`DELETE FROM WebObjectsIndex WHERE index_id IN
			(SELECT index_id FROM SearchIndex WHERE page_url = $1)`, pageURL)
		_, _ = admin.Exec(`DELETE FROM SearchIndex WHERE page_url = $1`, pageURL)
	})

	if _, err := indexPageContext(context.Background(), ctx, pageURL,
		legacyRefreshPageInfo(ctx, sourceID, "legacy generation one "+unique)); err != nil {
		t.Fatalf("initial index: %v", err)
	}
	indexID := fencedRefreshPageIndex(t, handler, pageURL)
	oldObjects := fencedRefreshPageObjects(t, handler, indexID)
	if len(oldObjects) != 1 {
		t.Fatalf("initial page objects = %v", oldObjects)
	}
	oldHash := oldObjects[0]

	if _, err := indexPageContext(context.Background(), ctx, pageURL,
		legacyRefreshPageInfo(ctx, sourceID, "legacy generation two "+unique)); err != nil {
		t.Fatalf("legacy refresh: %v", err)
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
		t.Fatalf("old orphan object survived the legacy refresh: %d", oldRows)
	}
}
