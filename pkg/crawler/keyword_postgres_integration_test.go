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
		if err = handler.QueryRow(`INSERT INTO SearchIndex(page_url,title) VALUES($1,'keywords') RETURNING index_id`,
			fmt.Sprintf("https://keyword-set.invalid/%s/%d", suffix, i)).Scan(&indexIDs[i]); err != nil {
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

// pqUint64Array uses pq's Valuer without making the production helper's API
// depend on a test-only conversion.
func pqUint64Array(values []uint64) interface{} {
	converted := make([]int64, len(values))
	for i, value := range values {
		converted[i] = int64(value)
	}
	return pq.Array(converted)
}
