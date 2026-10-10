//go:build integration

package database

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestPostgresSearchPagesSourceFilterScoped verifies source scoping at the
// query layer: the UID filter applies inside the database before LIMIT, so
// scoped matches surfacing beyond the global top-N are still returned.
//
// Requires a live PostgreSQL instance provisioned from the project schema
// (postgresql-setup.pgsql through the current migration), e.g.:
//
//	THECROWLER_POSTGRES_INTEGRATION=1 \
//	go test -tags=integration ./pkg/database/ -run TestPostgresSearchPagesSourceFilterScoped -v
func TestPostgresSearchPagesSourceFilterScoped(t *testing.T) {
	db, sqlDB := openPostgresIntegrationTestDB(t)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	uidA := "itest-uid-a-" + suffix
	uidB := "itest-uid-b-" + suffix
	urlA := fmt.Sprintf("https://itest-a-%s.example/page", suffix)
	urlB1 := fmt.Sprintf("https://itest-b-%s.example/one", suffix)
	urlB2 := fmt.Sprintf("https://itest-b-%s.example/two", suffix)

	var sourceAID, sourceBID uint64
	if err := sqlDB.QueryRowContext(ctx,
		`INSERT INTO Sources (source_uid, name, url) VALUES ($1, $2, $3) RETURNING source_id`,
		uidA, "itest-a", urlA).Scan(&sourceAID); err != nil {
		t.Fatalf("insert source A: %v", err)
	}
	if err := sqlDB.QueryRowContext(ctx,
		`INSERT INTO Sources (source_uid, name, url) VALUES ($1, $2, $3) RETURNING source_id`,
		uidB, "itest-b", urlB1).Scan(&sourceBID); err != nil {
		t.Fatalf("insert source B: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = sqlDB.ExecContext(cleanupCtx, `DELETE FROM SourceSearchIndex WHERE source_id IN ($1, $2)`, sourceAID, sourceBID)
		_, _ = sqlDB.ExecContext(cleanupCtx, `DELETE FROM SearchIndex WHERE page_url IN ($1, $2, $3)`, urlA, urlB1, urlB2)
		_, _ = sqlDB.ExecContext(cleanupCtx, `DELETE FROM Sources WHERE source_id IN ($1, $2)`, sourceAID, sourceBID)
	})

	insertPage := func(url, title, summary string, sourceID uint64) {
		var indexID uint64
		if err := sqlDB.QueryRowContext(ctx,
			`INSERT INTO SearchIndex (page_url, title, summary) VALUES ($1, $2, $3) RETURNING index_id`,
			url, title, summary).Scan(&indexID); err != nil {
			t.Fatalf("insert page %s: %v", url, err)
		}
		if _, err := sqlDB.ExecContext(ctx,
			`INSERT INTO SourceSearchIndex (source_id, index_id) VALUES ($1, $2)`,
			sourceID, indexID); err != nil {
			t.Fatalf("link page %s: %v", url, err)
		}
	}
	// Both B pages match strongly; the A page matches but ranks lower.
	insertPage(urlB1, "forecast storm surge warning", "severe forecast storm surge expected tonight", sourceBID)
	insertPage(urlB2, "forecast storm surge watch", "forecast storm surge watch continues", sourceBID)
	insertPage(urlA, "weekly outlook", "a mild forecast outlook for the region", sourceAID)

	// Scoped to A with a limit that the global top-N would fill with B rows
	// under limit-before-filter semantics: A must still surface, bounded.
	scoped, err := SearchPages(ctx, db, "forecast", "english", SearchFunctionOptions{Limit: 2, SourceUID: uidA})
	if err != nil {
		t.Fatalf("scoped search: %v", err)
	}
	if len(scoped) != 1 {
		t.Fatalf("expected exactly the A match, got %d rows: %+v", len(scoped), scoped)
	}
	if scoped[0].SourceUID != uidA {
		t.Fatalf("B row leaked into scoped results: %+v", scoped[0])
	}

	// Source A has fewer results than requested: no padding, no B rows.
	wide, err := SearchPages(ctx, db, "forecast", "english", SearchFunctionOptions{Limit: 10, SourceUID: uidA})
	if err != nil {
		t.Fatalf("wide scoped search: %v", err)
	}
	if len(wide) != 1 || wide[0].SourceUID != uidA {
		t.Fatalf("unexpected wide scoped results: %+v", wide)
	}

	// Unscoped requests keep prior semantics: bounded, unfiltered.
	open, err := SearchPages(ctx, db, "forecast", "english", SearchFunctionOptions{Limit: 2})
	if err != nil {
		t.Fatalf("unscoped search: %v", err)
	}
	if len(open) != 2 {
		t.Fatalf("expected 2 unscoped rows, got %d", len(open))
	}
}
