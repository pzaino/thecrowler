//go:build integration

package crawler

import (
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/lib/pq"

	cfg "github.com/pzaino/thecrowler/pkg/config"
	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func TestPostgresKeywordSetPersistence(t *testing.T) {
	if os.Getenv("THECROWLER_POSTGRES_INTEGRATION") != "1" {
		t.Skip("set THECROWLER_POSTGRES_INTEGRATION=1 to run PostgreSQL integration tests")
	}
	env := func(name, fallback string) string {
		if value := os.Getenv(name); value != "" {
			return value
		}
		return fallback
	}
	port, err := strconv.Atoi(env("DOCKER_POSTGRES_DB_PORT", "5432"))
	if err != nil {
		t.Fatal(err)
	}
	configuration := cfg.Config{Database: cfg.Database{
		Type: cdb.DBPostgresStr, Host: env("DOCKER_POSTGRES_DB_HOST", "127.0.0.1"), Port: port,
		User:     env("DOCKER_POSTGRES_DB_USER", env("DOCKER_POSTGRES_USER", "postgres")),
		Password: env("DOCKER_POSTGRES_PASSWORD", "postgres"), DBName: env("DOCKER_POSTGRES_DB_NAME", "SitesIndex"),
		SSLMode: env("DOCKER_POSTGRES_SSL_MODE", "disable"), MaxConns: 8, MaxIdleConns: 8,
	}}
	handler, err := cdb.NewHandler(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err = handler.Connect(configuration); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })

	suffix := fmt.Sprint(time.Now().UnixNano())
	const workers = 4
	indexIDs := make([]uint64, workers)
	for i := range indexIDs {
		if err = handler.QueryRow(
			`INSERT INTO SearchIndex(page_url, title, summary)
			VALUES($1, 'keywords', '')
			RETURNING index_id`,
			fmt.Sprintf("https://keyword-set.invalid/%s/%d", suffix, i),
		).Scan(&indexIDs[i]); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = handler.Exec(`DELETE FROM SearchIndex WHERE index_id = ANY($1::bigint[])`, pqUint64Array(indexIDs))
		_, _ = handler.Exec(`DELETE FROM Keywords WHERE keyword LIKE $1`, "kw-"+suffix+"-%")
	})

	shared := "kw-" + suffix + "-café"
	pages := make([]*PageInfo, workers)
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := range pages {
		pages[i] = &PageInfo{Keywords: []string{shared, "KW-" + suffix + "-CAFE\u0301", shared,
			fmt.Sprintf("kw-%s-page-%d", suffix, i)}}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- insertKeywords(handler, indexIDs[i], pages[i])
		}(i)
	}
	wg.Wait()
	close(errs)
	for err = range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	var keywordID int64
	var keywordRows, linkRows int
	var updatedAt time.Time
	if err = handler.QueryRow(`SELECT MIN(keyword_id), COUNT(*), MIN(last_updated_at) FROM Keywords WHERE keyword=$1`, shared).
		Scan(&keywordID, &keywordRows, &updatedAt); err != nil {
		t.Fatal(err)
	}
	if err = handler.QueryRow(`SELECT COUNT(*) FROM KeywordIndex WHERE keyword_id=$1 AND index_id=ANY($2::bigint[]) AND occurrences=3`,
		keywordID, pqUint64Array(indexIDs)).Scan(&linkRows); err != nil {
		t.Fatal(err)
	}
	if keywordRows != 1 || linkRows != workers {
		t.Fatalf("shared logical rows=%d occurrence links=%d, want 1 and %d", keywordRows, linkRows, workers)
	}
	large := &PageInfo{Keywords: make([]string, postgresKeywordBatchSize*2+17)}
	for i := range large.Keywords {
		large.Keywords[i] = fmt.Sprintf("kw-%s-large-%04d", suffix, i)
	}
	if err = insertKeywords(handler, indexIDs[0], large); err != nil {
		t.Fatal(err)
	}
	var largeLinks int
	if err = handler.QueryRow(`SELECT COUNT(*) FROM KeywordIndex ki JOIN Keywords k USING(keyword_id)
		WHERE ki.index_id=$1 AND k.keyword LIKE $2`, indexIDs[0], "kw-"+suffix+"-large-%").Scan(&largeLinks); err != nil {
		t.Fatal(err)
	}
	if largeLinks != len(large.Keywords) {
		t.Fatalf("large bounded persistence links=%d, want %d", largeLinks, len(large.Keywords))
	}
	// Re-arm the suppression window immediately before the assertion.
	//
	// The keyword upsert only refreshes Keywords.last_updated_at when the row is
	// older than thirty seconds. Anchoring the baseline here, instead of relying
	// on the timestamp captured before the large-keyword phase above, keeps the
	// assertion a statement-to-statement distance from the boundary. A slower
	// disposable database can no longer decide which branch runs by taking more
	// than thirty seconds for the whole test body. A no-op self-assignment is
	// enough: the BEFORE UPDATE trigger stamps last_updated_at from the current
	// transaction clock.
	if _, err = handler.Exec(`UPDATE Keywords SET keyword = keyword WHERE keyword_id=$1`, keywordID); err != nil {
		t.Fatal(err)
	}
	if err = handler.QueryRow(`SELECT last_updated_at FROM Keywords WHERE keyword_id=$1`, keywordID).Scan(&updatedAt); err != nil {
		t.Fatal(err)
	}

	if err = insertKeywords(handler, indexIDs[0], &PageInfo{Keywords: []string{shared, shared, shared}}); err != nil {
		t.Fatal(err)
	}
	var unchanged time.Time
	if err = handler.QueryRow(`SELECT last_updated_at FROM Keywords WHERE keyword_id=$1`, keywordID).Scan(&unchanged); err != nil {
		t.Fatal(err)
	}
	if !unchanged.Equal(updatedAt) {
		t.Fatalf("30-second suppression changed timestamp: before=%s after=%s", updatedAt, unchanged)
	}
}

// TestPostgresKeywordSuppressionWindowBranches pins both sides of the
// documented thirty-second keyword suppression window without depending on how
// long the test takes.
//
// TestPostgresKeywordSetPersistence used to fail whenever a slow disposable
// database let more than thirty seconds elapse between the initial insert and
// the re-insert: the production predicate then selected the refresh branch and
// the timestamp legitimately changed. That was a test-arrangement defect, not
// an indexing regression. The cases below give each branch an unambiguous
// input instead of racing a wall clock.
func TestPostgresKeywordSuppressionWindowBranches(t *testing.T) {
	if os.Getenv("THECROWLER_POSTGRES_INTEGRATION") != "1" {
		t.Skip("set THECROWLER_POSTGRES_INTEGRATION=1 to run PostgreSQL integration tests")
	}
	handler := openKeywordTestPostgres(t)
	suffix := fmt.Sprint(time.Now().UnixNano())

	var indexID uint64
	pageURL := fmt.Sprintf("https://keyword-window.invalid/%s", suffix)
	if err := handler.QueryRow(
		`INSERT INTO SearchIndex(page_url, title, summary)
		VALUES($1, 'keywords', '') RETURNING index_id`, pageURL).Scan(&indexID); err != nil {
		t.Fatal(err)
	}

	// "Recent" is expressed as a future timestamp so the row stays inside the
	// suppression window no matter how long the test takes. This also documents
	// the clock-skew behaviour: a row stamped ahead of the database clock is
	// treated as recent and is never refreshed.
	recent := "kw-" + suffix + "-window-recent"
	// "Stale" is an hour old, which keeps the refresh branch selected with a
	// wide margin regardless of scheduling delay.
	stale := "kw-" + suffix + "-window-stale"
	t.Cleanup(func() {
		_, _ = handler.Exec(`DELETE FROM SearchIndex WHERE index_id = $1`, indexID)
		_, _ = handler.Exec(`DELETE FROM Keywords WHERE keyword IN ($1, $2)`, recent, stale)
	})

	if _, err := handler.Exec(
		`INSERT INTO Keywords (keyword, last_updated_at)
		 VALUES ($1, CURRENT_TIMESTAMP + INTERVAL '1 hour')`,
		recent); err != nil {
		t.Fatal(err)
	}
	var recentBefore time.Time
	if err := handler.QueryRow(`SELECT last_updated_at FROM Keywords WHERE keyword=$1`, recent).
		Scan(&recentBefore); err != nil {
		t.Fatal(err)
	}
	if err := insertKeywords(handler, indexID, &PageInfo{Keywords: []string{recent}}); err != nil {
		t.Fatal(err)
	}
	var recentAfter time.Time
	if err := handler.QueryRow(`SELECT last_updated_at FROM Keywords WHERE keyword=$1`, recent).
		Scan(&recentAfter); err != nil {
		t.Fatal(err)
	}
	if !recentAfter.Equal(recentBefore) {
		t.Fatalf("row inside the suppression window was refreshed: before=%s after=%s",
			recentBefore, recentAfter)
	}

	if _, err := handler.Exec(
		`INSERT INTO Keywords (keyword, last_updated_at)
		 VALUES ($1, CURRENT_TIMESTAMP - INTERVAL '1 hour')`,
		stale); err != nil {
		t.Fatal(err)
	}
	var staleBefore time.Time
	if err := handler.QueryRow(`SELECT last_updated_at FROM Keywords WHERE keyword=$1`, stale).
		Scan(&staleBefore); err != nil {
		t.Fatal(err)
	}
	if err := insertKeywords(handler, indexID, &PageInfo{Keywords: []string{stale}}); err != nil {
		t.Fatal(err)
	}
	var staleAfter time.Time
	if err := handler.QueryRow(`SELECT last_updated_at FROM Keywords WHERE keyword=$1`, stale).
		Scan(&staleAfter); err != nil {
		t.Fatal(err)
	}
	if !staleAfter.After(staleBefore) {
		t.Fatalf("row outside the suppression window was not refreshed: before=%s after=%s",
			staleBefore, staleAfter)
	}

	// Exact boundary. Running the baseline insert and the production upsert in
	// one transaction fixes CURRENT_TIMESTAMP, so "just inside", "exactly at"
	// and "just outside" are decided by the predicate alone. The documented
	// comparison is strictly less-than, therefore a row exactly thirty seconds
	// old is still suppressed.
	boundary := "kw-" + suffix + "-window-boundary"
	t.Cleanup(func() {
		_, _ = handler.Exec(`DELETE FROM Keywords WHERE keyword = $1`, boundary)
	})
	for _, tc := range []struct {
		name       string
		age        string
		wantChange bool
	}{
		{name: "just inside", age: "29.999999 seconds", wantChange: false},
		{name: "exactly at the boundary", age: "30 seconds", wantChange: false},
		{name: "just outside", age: "30.000001 seconds", wantChange: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := handler.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()

			if _, err = tx.Exec(`DELETE FROM Keywords WHERE keyword = $1`, boundary); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(
				`INSERT INTO Keywords (keyword, last_updated_at)
				 VALUES ($1, CURRENT_TIMESTAMP - $2::interval)`,
				boundary, tc.age); err != nil {
				t.Fatal(err)
			}
			var before time.Time
			if err = tx.QueryRow(`SELECT last_updated_at FROM Keywords WHERE keyword=$1`, boundary).
				Scan(&before); err != nil {
				t.Fatal(err)
			}
			if _, err = upsertPostgresKeywordRowsTx(tx, indexID,
				[]string{boundary}, map[string]int64{boundary: 1}); err != nil {
				t.Fatal(err)
			}
			var after time.Time
			if err = tx.QueryRow(`SELECT last_updated_at FROM Keywords WHERE keyword=$1`, boundary).
				Scan(&after); err != nil {
				t.Fatal(err)
			}
			if changed := !after.Equal(before); changed != tc.wantChange {
				t.Fatalf("age=%s refreshed=%v want %v (before=%s after=%s)",
					tc.age, changed, tc.wantChange, before, after)
			}
		})
	}
}

// openKeywordTestPostgres opens the runtime CROWler handler used by the keyword
// integration tests. It shares the connection contract with the fenced-refresh
// helper but is kept separate so the keyword tests do not depend on a fenced
// claim.
func openKeywordTestPostgres(t *testing.T) cdb.Handler {
	t.Helper()
	env := func(name, fallback string) string {
		if value := os.Getenv(name); value != "" {
			return value
		}
		return fallback
	}
	port, err := strconv.Atoi(env("DOCKER_POSTGRES_DB_PORT", "5432"))
	if err != nil {
		t.Fatal(err)
	}
	configuration := cfg.Config{Database: cfg.Database{
		Type: cdb.DBPostgresStr, Host: env("DOCKER_POSTGRES_DB_HOST", "127.0.0.1"), Port: port,
		User:     env("DOCKER_POSTGRES_DB_USER", env("DOCKER_POSTGRES_USER", "postgres")),
		Password: env("DOCKER_POSTGRES_PASSWORD", "postgres"), DBName: env("DOCKER_POSTGRES_DB_NAME", "SitesIndex"),
		SSLMode: env("DOCKER_POSTGRES_SSL_MODE", "disable"), MaxConns: 8, MaxIdleConns: 8,
	}}
	handler, err := cdb.NewHandler(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err = handler.Connect(configuration); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	return handler
}

// pqUint64Array uses pq's Valuer without making the production helper's API
// depend on a test-only conversion.
func pqUint64Array(values []uint64) interface{} {
	converted := make([]int64, len(values))
	for i, value := range values {
		converted[i] = int64(value)
	}
	return pq.Array(converted)
}
