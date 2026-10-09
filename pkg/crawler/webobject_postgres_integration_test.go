//go:build integration

package crawler

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
	detect "github.com/pzaino/thecrowler/pkg/detection"
)

// TestPostgresWebObjectRefreshUsesReplacementPath pins the crawler half of the
// history integration contract.  The database integration test owns the
// observation immutability/cascade assertions; this test prevents that fixture
// from drifting away from the actual refresh functions used by indexPage.
func TestPostgresWebObjectRefreshUsesReplacementPath(t *testing.T) {
	if os.Getenv("THECROWLER_POSTGRES_INTEGRATION") != "1" {
		t.Skip("set THECROWLER_POSTGRES_INTEGRATION=1 to run PostgreSQL integration tests")
	}

	valueOrDefault := func(name, fallback string) string {
		if value := os.Getenv(name); value != "" {
			return value
		}
		return fallback
	}

	port, err := strconv.Atoi(valueOrDefault("DOCKER_POSTGRES_DB_PORT", "5432"))
	if err != nil {
		t.Fatalf("invalid DOCKER_POSTGRES_DB_PORT: %v", err)
	}

	configuration := cfg.Config{
		Database: cfg.Database{
			Type:         cdb.DBPostgresStr,
			Host:         valueOrDefault("DOCKER_POSTGRES_DB_HOST", "127.0.0.1"),
			Port:         port,
			User:         valueOrDefault("DOCKER_POSTGRES_DB_USER", valueOrDefault("DOCKER_POSTGRES_USER", "postgres")),
			Password:     valueOrDefault("DOCKER_POSTGRES_PASSWORD", "postgres"),
			DBName:       valueOrDefault("DOCKER_POSTGRES_DB_NAME", "SitesIndex"),
			SSLMode:      valueOrDefault("DOCKER_POSTGRES_SSL_MODE", "disable"),
			MaxConns:     2,
			MaxIdleConns: 1,
		},
	}

	handler, err := cdb.NewHandler(configuration)
	if err != nil {
		t.Fatal(err)
	}

	if err = handler.Connect(configuration); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })

	var sourceID, indexID uint64
	suffix := fmt.Sprint(time.Now().UnixNano())

	page := func(count int, price float64) *PageInfo {
		return &PageInfo{
			BodyText: fmt.Sprintf("Widget %d %.2f", count, price),
			ScrapedData: []ScrapedItem{
				{
					"kind": "Widget",
					"name": "backlog-widget",
					"operational": map[string]interface{}{
						"count": count,
						"price": price,
					},
					"region": "eu",
				},
			},
			DetectedTech: map[string]detect.DetectedEntity{},
		}
	}

	//
	// First transaction: create the existing page generation and commit it.
	//
	setupTx, err := handler.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = setupTx.Rollback() }()

	sourceURL := "https://crawler-widget.invalid/" + suffix
	sourceName := "widget refresh"
	sourceUID := cdb.CalculateSourceUID(sourceName, sourceURL)

	if err = setupTx.QueryRow(`
		INSERT INTO Sources(
			source_uid,
			url,
			name,
			priority,
			category_id,
			usr_id,
			restricted,
			flags,
			config,
			disabled
		)
		VALUES(
			$1,
			$2,
			$3,
			'normal',
			0,
			0,
			0,
			0,
			'{}'::jsonb,
			false
		)
		RETURNING source_id`,
		sourceUID,
		sourceURL,
		sourceName,
	).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}

	if err = setupTx.QueryRow(`
		INSERT INTO SearchIndex(page_url, title)
		VALUES($1, 'Widget')
		RETURNING index_id`,
		"https://crawler-widget.invalid/page/"+suffix,
	).Scan(&indexID); err != nil {
		t.Fatal(err)
	}

	if _, err = setupTx.Exec(`
		INSERT INTO SourceSearchIndex(source_id, index_id)
		VALUES($1, $2)`,
		sourceID,
		indexID,
	); err != nil {
		t.Fatal(err)
	}

	oldID, _, oldHash, err := insertOrUpdateWebObjects(
		setupTx,
		indexID,
		page(7, 12.5),
	)
	if err != nil {
		t.Fatal(err)
	}

	if err = setupTx.Commit(); err != nil {
		t.Fatal(err)
	}

	//
	// Refresh cleanup: this deliberately runs in its own bounded transaction.
	//
	if err = deleteWebObjects(
		context.Background(),
		&handler,
		indexID,
	); err != nil {
		t.Fatal(err)
	}

	// Verify that the committed old generation was actually removed before
	// starting the replacement transaction.
	var oldRowsAfterCleanup int
	if err = handler.QueryRow(`
		SELECT COUNT(*)
		FROM WebObjects
		WHERE object_id = $1`,
		oldID,
	).Scan(&oldRowsAfterCleanup); err != nil {
		t.Fatal(err)
	}

	if oldRowsAfterCleanup != 0 {
		t.Fatalf(
			"cleanup left old WebObject %d behind: rows=%d",
			oldID,
			oldRowsAfterCleanup,
		)
	}

	//
	// Second transaction: create and commit the replacement generation.
	//
	replacementTx, err := handler.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = replacementTx.Rollback() }()

	newID, _, newHash, err := insertOrUpdateWebObjects(
		replacementTx,
		indexID,
		page(9, 15.75),
	)
	if err != nil {
		t.Fatal(err)
	}

	if oldID == newID || oldHash == newHash {
		t.Fatalf(
			"refresh did not replace operational WebObject: old=(%d,%s) new=(%d,%s)",
			oldID,
			oldHash,
			newID,
			newHash,
		)
	}

	if err = replacementTx.Commit(); err != nil {
		t.Fatal(err)
	}

	//
	// Verify committed replacement state.
	//
	var oldRows, newLinks int

	if err = handler.QueryRow(`
		SELECT COUNT(*)
		FROM WebObjects
		WHERE object_id = $1`,
		oldID,
	).Scan(&oldRows); err != nil {
		t.Fatal(err)
	}

	if err = handler.QueryRow(`
		SELECT COUNT(*)
		FROM WebObjectsIndex
		WHERE index_id = $1
		  AND object_id = $2`,
		indexID,
		newID,
	).Scan(&newLinks); err != nil {
		t.Fatal(err)
	}

	if oldRows != 0 || newLinks != 1 {
		t.Fatalf(
			"replacement state old rows=%d new links=%d",
			oldRows,
			newLinks,
		)
	}
}
