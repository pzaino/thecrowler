//go:build integration

package database

// WP-REL230-RC5: shared-artifact parent-row locking benchmark.
//
// The RC2 fix for silent shared-object loss made handle_shared_entity_deletion()
// take a FOR UPDATE row lock on the parent artifact before counting remaining
// links. FOR UPDATE conflicts with the KEY SHARE lock a concurrent link INSERT
// needs for its foreign key, so every link deletion now serialises against link
// insertions for the same parent.
//
// This harness measures that cost on a disposable PostgreSQL instance. It is
// opt-in and never touches a caller-selected database:
//
//	CROWLER_RC5_POSTGRES_TEST_DISPOSABLE=1 \
//	CROWLER_RC5_POSTGRES_DSN='postgres://postgres:...@127.0.0.1:PORT/SitesIndex?sslmode=disable' \
//	CROWLER_RC5_BENCH=1 \
//	go test -tags=integration ./pkg/database -run '^TestPostgresSharedArtifactContentionBenchmark$' -count=1 -v
//
// The test creates a uniquely named child database, builds a minimal but
// FK-faithful fixture (same columns, foreign keys, ON DELETE CASCADE and
// parent-id indexes as the shipped schema), and drops only that database.
//
// Variants:
//
//	rc4f      - the RC4F body: AFTER DELETE FOR EACH ROW, FOR UPDATE before
//	            counting links. Frozen historical baseline, embedded below.
//	current   - the body read from postgresql-setup.pgsql, i.e. exactly what
//	            ships. It is the candidate being evaluated (statement-level,
//	            transition table, ascending parent order, one lock per parent).
//	v228      - characterization only: the pre-RC2 body with no parent lock.
//	            It is NOT a candidate. It is measured so the historical
//	            throughput number is visible next to its known data-loss race,
//	            which TestPostgresSharedArtifactLegacyBodyLosesConcurrentLink
//	            keeps reproducible.
//
// Results are written as CSV plus a human-readable summary to RC5_BENCH_OUT
// (default: the test's temporary directory).

import (
	cryptorand "crypto/rand"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	mrand "math/rand/v2"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lib/pq"
)

const (
	rc5DisposableEnv = "CROWLER_RC5_POSTGRES_TEST_DISPOSABLE"
	rc5DSNEnv        = "CROWLER_RC5_POSTGRES_DSN"
	rc5BenchEnv      = "CROWLER_RC5_BENCH"
	rc5BenchOutEnv   = "RC5_BENCH_OUT"
)

// rc5RC4FFunctionBody is the RC4F/current shipped body, kept verbatim so the
// baseline can be measured on the same fixture as the candidate. A static test
// (TestSharedArtifactBenchmarkBaselineMatchesShippedSQL) fails if it ever drifts
// from postgresql-setup.pgsql.
const rc5RC4FFunctionBody = `CREATE OR REPLACE FUNCTION handle_shared_entity_deletion()
RETURNS TRIGGER AS $$
BEGIN
    IF TG_TABLE_NAME = 'metatagsindex' THEN
        PERFORM 1 FROM MetaTags WHERE metatag_id = OLD.metatag_id FOR UPDATE;
        IF FOUND THEN
            IF (SELECT COUNT(*) FROM MetaTagsIndex WHERE metatag_id = OLD.metatag_id) = 0 THEN
                DELETE FROM MetaTags WHERE metatag_id = OLD.metatag_id;
            END IF;
        END IF;
    ELSIF TG_TABLE_NAME = 'webobjectsindex' THEN
        PERFORM 1 FROM WebObjects WHERE object_id = OLD.object_id FOR UPDATE;
        IF FOUND THEN
            IF (SELECT COUNT(*) FROM WebObjectsIndex WHERE object_id = OLD.object_id) = 0 THEN
                DELETE FROM WebObjects WHERE object_id = OLD.object_id;
            END IF;
        END IF;
    ELSIF TG_TABLE_NAME = 'keywordindex' THEN
        PERFORM 1 FROM Keywords WHERE keyword_id = OLD.keyword_id FOR UPDATE;
        IF FOUND THEN
            IF (SELECT COUNT(*) FROM KeywordIndex WHERE keyword_id = OLD.keyword_id) = 0 THEN
                DELETE FROM Keywords WHERE keyword_id = OLD.keyword_id;
            END IF;
        END IF;
    ELSIF TG_TABLE_NAME = 'netinfoindex' THEN
        PERFORM 1 FROM NetInfo WHERE netinfo_id = OLD.netinfo_id FOR UPDATE;
        IF FOUND THEN
            IF (SELECT COUNT(*) FROM NetInfoIndex WHERE netinfo_id = OLD.netinfo_id) = 0 THEN
                DELETE FROM NetInfo WHERE netinfo_id = OLD.netinfo_id;
            END IF;
        END IF;
    ELSIF TG_TABLE_NAME = 'httpinfoindex' THEN
        PERFORM 1 FROM HTTPInfo WHERE httpinfo_id = OLD.httpinfo_id FOR UPDATE;
        IF FOUND THEN
            IF (SELECT COUNT(*) FROM HTTPInfoIndex WHERE httpinfo_id = OLD.httpinfo_id) = 0 THEN
                DELETE FROM HTTPInfo WHERE httpinfo_id = OLD.httpinfo_id;
            END IF;
        END IF;
    END IF;
    RETURN OLD;
END;
$$ LANGUAGE plpgsql`

// rc5LegacyFunctionBody is the pre-RC2 body. It is a characterization target
// only: it loses a concurrently inserted reference. Never ship it.
const rc5LegacyFunctionBody = `CREATE OR REPLACE FUNCTION handle_shared_entity_deletion()
RETURNS TRIGGER AS $$
BEGIN
    IF TG_TABLE_NAME = 'metatagsindex' THEN
        IF (SELECT COUNT(*) FROM MetaTagsIndex WHERE metatag_id = OLD.metatag_id) = 0 THEN
            DELETE FROM MetaTags WHERE metatag_id = OLD.metatag_id;
        END IF;
    ELSIF TG_TABLE_NAME = 'webobjectsindex' THEN
        IF (SELECT COUNT(*) FROM WebObjectsIndex WHERE object_id = OLD.object_id) = 0 THEN
            DELETE FROM WebObjects WHERE object_id = OLD.object_id;
        END IF;
    ELSIF TG_TABLE_NAME = 'keywordindex' THEN
        IF (SELECT COUNT(*) FROM KeywordIndex WHERE keyword_id = OLD.keyword_id) = 0 THEN
            DELETE FROM Keywords WHERE keyword_id = OLD.keyword_id;
        END IF;
    ELSIF TG_TABLE_NAME = 'netinfoindex' THEN
        IF (SELECT COUNT(*) FROM NetInfoIndex WHERE netinfo_id = OLD.netinfo_id) = 0 THEN
            DELETE FROM NetInfo WHERE netinfo_id = OLD.netinfo_id;
        END IF;
    ELSIF TG_TABLE_NAME = 'httpinfoindex' THEN
        IF (SELECT COUNT(*) FROM HTTPInfoIndex WHERE httpinfo_id = OLD.httpinfo_id) = 0 THEN
            DELETE FROM HTTPInfo WHERE httpinfo_id = OLD.httpinfo_id;
        END IF;
    END IF;
    RETURN OLD;
END;
$$ LANGUAGE plpgsql`

// rc5Variant is one comparable trigger implementation.
// rc5ShippedTriggersAreStatementLevel reports whether the shipped setup creates
// the link-table triggers FOR EACH STATEMENT with a transition table.
func rc5ShippedTriggersAreStatementLevel(t *testing.T) bool {
	t.Helper()
	raw, err := os.ReadFile("postgresql-setup.pgsql")
	if err != nil {
		t.Fatalf("read postgresql-setup.pgsql: %v", err)
	}
	text := string(raw)
	statement := strings.Count(text, "FOR EACH STATEMENT\nEXECUTE FUNCTION handle_shared_entity_deletion();")
	row := strings.Count(text, "FOR EACH ROW\nEXECUTE FUNCTION handle_shared_entity_deletion();")
	if statement == 5 && row == 0 {
		return true
	}
	if row == 5 && statement == 0 {
		return false
	}
	t.Fatalf("unexpected shipped trigger mix: statement=%d row=%d", statement, row)
	return false
}

type rc5Variant struct {
	name           string
	body           string
	statementLevel bool
	trusted        bool // false for the historical data-loss body
}

// rc5ShippedFunctionBody extracts handle_shared_entity_deletion() from
// postgresql-setup.pgsql so the benchmark always measures the shipped SQL.
func rc5ShippedFunctionBody(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("postgresql-setup.pgsql")
	if err != nil {
		t.Fatalf("read postgresql-setup.pgsql: %v", err)
	}
	text := string(raw)
	const header = "CREATE OR REPLACE FUNCTION handle_shared_entity_deletion()"
	start := strings.Index(text, header)
	if start < 0 {
		t.Fatal("postgresql-setup.pgsql has no handle_shared_entity_deletion()")
	}
	end := strings.Index(text[start:], "$$ LANGUAGE plpgsql;")
	if end < 0 {
		t.Fatal("handle_shared_entity_deletion() body is not terminated")
	}
	return text[start : start+end+len("$$ LANGUAGE plpgsql;")]
}

func rc5Variants(t *testing.T) []rc5Variant {
	t.Helper()
	shipped := rc5ShippedFunctionBody(t)
	return []rc5Variant{
		{name: "rc4f", body: rc5RC4FFunctionBody, trusted: true},
		{name: "current", body: shipped, statementLevel: rc5ShippedTriggersAreStatementLevel(t), trusted: true},
		{name: "v228", body: rc5LegacyFunctionBody},
	}
}

// rc5ArtifactFamilies maps a family name to its parent table, link table and
// the parent-id column shared by both.
type rc5ArtifactFamily struct {
	name       string
	parent     string
	link       string
	parentID   string
	linkID     string
	parentCols string // extra parent columns
	linkCols   string // extra link columns
}

func rc5Families() []rc5ArtifactFamily {
	return []rc5ArtifactFamily{
		{name: "keyword", parent: "Keywords", link: "KeywordIndex", parentID: "keyword_id", linkID: "keyword_index_id"},
		{name: "metatag", parent: "MetaTags", link: "MetaTagsIndex", parentID: "metatag_id", linkID: "sim_id"},
		{name: "webobject", parent: "WebObjects", link: "WebObjectsIndex", parentID: "object_id", linkID: "page_object_id"},
		{name: "netinfo", parent: "NetInfo", link: "NetInfoIndex", parentID: "netinfo_id", linkID: "netinfo_index_id"},
		{name: "httpinfo", parent: "HTTPInfo", link: "HTTPInfoIndex", parentID: "httpinfo_id", linkID: "httpinfo_index_id"},
	}
}

// rc5FixtureDDL builds a minimal but lock-faithful schema: the same foreign
// keys with ON DELETE CASCADE that make a link insert take KEY SHARE on the
// parent, and an index on the link table's parent-id column, which is what the
// shipped cleanup count/anti-join uses.
func rc5FixtureDDL() []string {
	return []string{
		`CREATE TABLE Keywords (
			keyword_id BIGSERIAL PRIMARY KEY,
			keyword TEXT NOT NULL UNIQUE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			last_updated_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE TABLE KeywordIndex (
			keyword_index_id BIGSERIAL PRIMARY KEY,
			keyword_id BIGINT NOT NULL REFERENCES Keywords(keyword_id) ON DELETE CASCADE,
			index_id BIGINT NOT NULL,
			occurrences BIGINT NOT NULL DEFAULT 1,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			UNIQUE (keyword_id, index_id))`,
		`CREATE INDEX idx_fixture_keywordindex_parent ON KeywordIndex (keyword_id)`,
		`CREATE TABLE MetaTags (
			metatag_id BIGSERIAL PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			content TEXT,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			last_updated_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE TABLE MetaTagsIndex (
			sim_id BIGSERIAL PRIMARY KEY,
			metatag_id BIGINT NOT NULL REFERENCES MetaTags(metatag_id) ON DELETE CASCADE,
			index_id BIGINT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			UNIQUE (metatag_id, index_id))`,
		`CREATE INDEX idx_fixture_metatagsindex_parent ON MetaTagsIndex (metatag_id)`,
		`CREATE TABLE WebObjects (
			object_id BIGSERIAL PRIMARY KEY,
			object_hash TEXT NOT NULL UNIQUE,
			object_content TEXT,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			last_updated_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE TABLE WebObjectsIndex (
			page_object_id BIGSERIAL PRIMARY KEY,
			object_id BIGINT NOT NULL REFERENCES WebObjects(object_id) ON DELETE CASCADE,
			index_id BIGINT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			UNIQUE (index_id, object_id))`,
		`CREATE INDEX idx_fixture_webobjectsindex_parent ON WebObjectsIndex (object_id)`,
		`CREATE TABLE NetInfo (
			netinfo_id BIGSERIAL PRIMARY KEY,
			details_hash TEXT NOT NULL UNIQUE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			last_updated_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE TABLE NetInfoIndex (
			netinfo_index_id BIGSERIAL PRIMARY KEY,
			netinfo_id BIGINT NOT NULL REFERENCES NetInfo(netinfo_id) ON DELETE CASCADE,
			index_id BIGINT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			UNIQUE (netinfo_id, index_id))`,
		`CREATE INDEX idx_fixture_netinfoindex_parent ON NetInfoIndex (netinfo_id)`,
		`CREATE TABLE HTTPInfo (
			httpinfo_id BIGSERIAL PRIMARY KEY,
			details_hash TEXT NOT NULL UNIQUE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			last_updated_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE TABLE HTTPInfoIndex (
			httpinfo_index_id BIGSERIAL PRIMARY KEY,
			httpinfo_id BIGINT NOT NULL REFERENCES HTTPInfo(httpinfo_id) ON DELETE CASCADE,
			index_id BIGINT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			UNIQUE (httpinfo_id, index_id))`,
		`CREATE INDEX idx_fixture_httpinfoindex_parent ON HTTPInfoIndex (httpinfo_id)`,
	}
}

// ---------------------------------------------------------------------------
// disposable database plumbing
// ---------------------------------------------------------------------------

func rc5Target(t *testing.T) *url.URL {
	t.Helper()
	if strings.TrimSpace(os.Getenv(rc5DisposableEnv)) != "1" {
		t.Skipf("%s=1 is required for the RC5 shared-artifact benchmark", rc5DisposableEnv)
	}
	raw := strings.TrimSpace(os.Getenv(rc5DSNEnv))
	if raw == "" {
		t.Skipf("%s is required for the RC5 shared-artifact benchmark", rc5DSNEnv)
	}
	target, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("invalid %s: %v", rc5DSNEnv, err)
	}
	if target.Scheme != "postgres" && target.Scheme != "postgresql" {
		t.Fatalf("%s must be a postgres:// URL", rc5DSNEnv)
	}
	return target
}

func rc5RandomSuffix(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 6)
	if _, err := cryptorand.Read(raw); err != nil {
		t.Fatalf("random: %v", err)
	}
	return hex.EncodeToString(raw)
}

// rc5NewDatabase creates a uniquely named database from the opt-in DSN and
// returns a DSN for it. The caller gets a cleanup that drops only this database.
func rc5NewDatabase(t *testing.T) (*sql.DB, func()) {
	t.Helper()
	target := rc5Target(t)
	name := "rc5_bench_" + rc5RandomSuffix(t)

	adminDSN := *target
	adminDB, err := sql.Open("postgres", adminDSN.String())
	if err != nil {
		t.Fatalf("open admin connection: %v", err)
	}
	adminDB.SetMaxOpenConns(2)
	if err := adminDB.Ping(); err != nil {
		_ = adminDB.Close()
		t.Fatalf("admin connection: %v", err)
	}
	if _, err := adminDB.Exec(`CREATE DATABASE ` + pq.QuoteIdentifier(name)); err != nil {
		_ = adminDB.Close()
		t.Fatalf("create test database: %v", err)
	}

	child := *target
	child.Path = "/" + name
	db, err := sql.Open("postgres", child.String())
	if err != nil {
		_, _ = adminDB.Exec(`DROP DATABASE IF EXISTS ` + pq.QuoteIdentifier(name) + ` WITH (FORCE)`)
		_ = adminDB.Close()
		t.Fatalf("open test database: %v", err)
	}
	cleanup := func() {
		_ = db.Close()
		_, _ = adminDB.Exec(`DROP DATABASE IF EXISTS ` + pq.QuoteIdentifier(name) + ` WITH (FORCE)`)
		_ = adminDB.Close()
	}
	return db, cleanup
}

func rc5MustExec(t *testing.T, db *sql.DB, stmt string, args ...any) {
	t.Helper()
	if _, err := db.Exec(stmt, args...); err != nil {
		t.Fatalf("exec failed: %v\nstatement: %s", err, stmt)
	}
}

// rc5InstallVariant creates the fixture and installs one variant.
func rc5InstallVariant(t *testing.T, db *sql.DB, v rc5Variant) {
	t.Helper()
	for _, stmt := range rc5FixtureDDL() {
		rc5MustExec(t, db, stmt)
	}
	rc5MustExec(t, db, v.body)
	for _, f := range rc5Families() {
		mode := "FOR EACH ROW"
		transition := ""
		if v.statementLevel {
			mode = "FOR EACH STATEMENT"
			transition = " REFERENCING OLD TABLE AS deleted_links"
		}
		trigger := fmt.Sprintf(
			`CREATE TRIGGER trg_after_delete_%s AFTER DELETE ON %s%s %s EXECUTE FUNCTION handle_shared_entity_deletion()`,
			strings.ToLower(f.link), f.link, transition, mode)
		rc5MustExec(t, db, trigger)
	}
}

// ---------------------------------------------------------------------------
// fixtures
// ---------------------------------------------------------------------------

// rc5SeedHotParent inserts one parent with fanOut links.
func rc5SeedHotParent(t *testing.T, db *sql.DB, f rc5ArtifactFamily, name string, fanOut int) int64 {
	t.Helper()
	insertParent := fmt.Sprintf(`INSERT INTO %s (%s) VALUES ($1) RETURNING %s`, f.parent, rc5ParentInsertColumn(f), f.parentID)
	var parentID int64
	if err := db.QueryRow(insertParent, name).Scan(&parentID); err != nil {
		t.Fatalf("insert %s parent: %v", f.name, err)
	}
	insertLink := fmt.Sprintf(`INSERT INTO %s (%s, index_id) SELECT $1, g FROM generate_series(1, $2) AS g`, f.link, f.parentID)
	if _, err := db.Exec(insertLink, parentID, fanOut); err != nil {
		t.Fatalf("insert %s links: %v", f.name, err)
	}
	return parentID
}

func rc5ParentInsertColumn(f rc5ArtifactFamily) string {
	switch f.name {
	case "keyword":
		return "keyword"
	case "metatag":
		return "name"
	case "webobject":
		return "object_hash"
	default:
		return "details_hash"
	}
}

// rc5SeedSharedParents inserts parents with fanOut links each and returns their IDs.
func rc5SeedSharedParents(t *testing.T, db *sql.DB, f rc5ArtifactFamily, prefix string, parents, fanOut int) []int64 {
	t.Helper()
	ids := make([]int64, 0, parents)
	for i := 0; i < parents; i++ {
		ids = append(ids, rc5SeedHotParent(t, db, f, fmt.Sprintf("%s-%d", prefix, i), fanOut))
	}
	return ids
}

// ---------------------------------------------------------------------------
// measurement
// ---------------------------------------------------------------------------

type rc5Sample struct {
	variant       string
	workload      string
	family        string
	concurrency   int
	ops           int64
	failures      int64
	deadlocks     int64
	lockWaitRatio float64
	p50           time.Duration
	p95           time.Duration
	p99           time.Duration
	opsPerSec     float64
	duration      time.Duration
}

// rc5Latencies collects durations without holding a lock per sample.
type rc5Latencies struct {
	mu   sync.Mutex
	data []time.Duration
}

func (l *rc5Latencies) add(d time.Duration) {
	l.mu.Lock()
	l.data = append(l.data, d)
	l.mu.Unlock()
}

func (l *rc5Latencies) percentile(p float64) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.data) == 0 {
		return 0
	}
	cp := make([]time.Duration, len(l.data))
	copy(cp, l.data)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	idx := int(math.Ceil(p*float64(len(cp)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(cp) {
		idx = len(cp) - 1
	}
	return cp[idx]
}

// rc5LockSampler samples how many backends are waiting on a lock.
type rc5LockSampler struct {
	db      *sql.DB
	stop    chan struct{}
	done    chan struct{}
	samples int64
	waiting int64
	active  int64
}

func rc5StartLockSampler(db *sql.DB) *rc5LockSampler {
	s := &rc5LockSampler{db: db, stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-ticker.C:
				var waiting, total int
				err := db.QueryRow(`
					SELECT COUNT(*) FILTER (WHERE wait_event_type = 'Lock'), COUNT(*)
					FROM pg_stat_activity
					WHERE datname = current_database() AND pid <> pg_backend_pid() AND state <> 'idle'`).
					Scan(&waiting, &total)
				if err != nil {
					continue
				}
				atomic.AddInt64(&s.samples, 1)
				atomic.AddInt64(&s.waiting, int64(waiting))
				atomic.AddInt64(&s.active, int64(total))
			}
		}
	}()
	return s
}

func (s *rc5LockSampler) stopAndRatio() float64 {
	if s == nil {
		return 0
	}
	close(s.stop)
	<-s.done
	samples := atomic.LoadInt64(&s.samples)
	active := atomic.LoadInt64(&s.active)
	if samples == 0 || active == 0 {
		return 0
	}
	// Fraction of observed backend-samples that were blocked on a lock.
	return float64(atomic.LoadInt64(&s.waiting)) / float64(active)
}

func rc5Deadlocks(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRow(`SELECT deadlocks FROM pg_stat_database WHERE datname = current_database()`).Scan(&n); err != nil {
		return 0
	}
	return n
}

func rc5IsDeadlock(err error) bool {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		return string(pqErr.Code) == "40P01"
	}
	return false
}

func rc5IsLockTimeout(err error) bool {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		return string(pqErr.Code) == "55P03"
	}
	return false
}

// ---------------------------------------------------------------------------
// workloads
// ---------------------------------------------------------------------------

// rc5ResetFamily removes all rows for one family and returns fresh parent IDs.
func rc5ResetFamily(t *testing.T, db *sql.DB, f rc5ArtifactFamily, parents, fanOut int) []int64 {
	t.Helper()
	rc5MustExec(t, db, fmt.Sprintf(`TRUNCATE %s, %s RESTART IDENTITY CASCADE`, f.parent, f.link))
	return rc5SeedSharedParents(t, db, f, f.name, parents, fanOut)
}

// rc5RunSingleLinkChurn measures the per-link delete+reinsert shape used by the
// refresh path (one DELETE per link, one transaction per page).
func rc5RunSingleLinkChurn(t *testing.T, db *sql.DB, f rc5ArtifactFamily, parentID int64, fanOut, workers, opsPerWorker int) (*rc5Latencies, int64, int64, time.Duration) {
	t.Helper()
	var failures, deadlocks int64
	startedAt := time.Now()
	lat := &rc5Latencies{}
	deleteStmt := fmt.Sprintf(`DELETE FROM %s WHERE %s = $1 AND index_id = $2`, f.link, f.parentID)
	insertStmt := fmt.Sprintf(`INSERT INTO %s (%s, index_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, f.link, f.parentID)

	var wg sync.WaitGroup
	start := make(chan struct{})
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed uint64) {
			defer wg.Done()
			rng := mrand.New(mrand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
			<-start
			for i := 0; i < opsPerWorker; i++ {
				indexID := rng.IntN(fanOut) + 1
				opStart := time.Now()
				tx, err := db.Begin()
				if err != nil {
					atomic.AddInt64(&failures, 1)
					continue
				}
				_, err = tx.Exec(deleteStmt, parentID, indexID)
				if err == nil {
					_, err = tx.Exec(insertStmt, parentID, indexID)
				}
				if err != nil {
					_ = tx.Rollback()
					if rc5IsDeadlock(err) {
						atomic.AddInt64(&deadlocks, 1)
					}
					atomic.AddInt64(&failures, 1)
					continue
				}
				if err := tx.Commit(); err != nil {
					if rc5IsDeadlock(err) {
						atomic.AddInt64(&deadlocks, 1)
					}
					atomic.AddInt64(&failures, 1)
					continue
				}
				lat.add(time.Since(opStart))
			}
		}(uint64(w) + 1)
	}
	close(start)
	wg.Wait()
	return lat, failures, deadlocks, time.Since(startedAt)
}

// rc5RunMassDelete measures the VacuumSource shape: one DELETE statement that
// removes many links across shared parents while concurrent inserters add new
// links for the same parents.
func rc5RunMassDelete(t *testing.T, db *sql.DB, f rc5ArtifactFamily, parentIDs []int64, fanOut int, keep int, inserters int) (time.Duration, int64, int64, int64, *rc5Latencies) {
	t.Helper()
	stop := make(chan struct{})
	var insertFailures, insertDeadlocks int64
	insertLat := &rc5Latencies{}
	var wg sync.WaitGroup

	for w := 0; w < inserters; w++ {
		wg.Add(1)
		go func(seed uint64) {
			defer wg.Done()
			rng := mrand.New(mrand.NewPCG(seed, seed*31+7))
			insertStmt := fmt.Sprintf(`INSERT INTO %s (%s, index_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, f.link, f.parentID)
			next := fanOut + 1
			for {
				select {
				case <-stop:
					return
				default:
				}
				parent := parentIDs[rng.IntN(len(parentIDs))]
				opStart := time.Now()
				if _, err := db.Exec(insertStmt, parent, next); err != nil {
					if rc5IsDeadlock(err) {
						atomic.AddInt64(&insertDeadlocks, 1)
					}
					atomic.AddInt64(&insertFailures, 1)
					continue
				}
				next++
				insertLat.add(time.Since(opStart))
			}
		}(uint64(w) + 1)
	}

	// Give the inserters time to be genuinely concurrent with the delete.
	time.Sleep(150 * time.Millisecond)

	deleteStmt := fmt.Sprintf(`DELETE FROM %s WHERE %s = ANY($1) AND index_id <= $2`, f.link, f.parentID)
	start := time.Now()
	res, err := db.Exec(deleteStmt, pq.Array(parentIDs), keep)
	elapsed := time.Since(start)
	close(stop)
	wg.Wait()
	if err != nil {
		t.Fatalf("mass delete failed: %v", err)
	}
	deleted, _ := res.RowsAffected()
	return elapsed, deleted, insertFailures, insertDeadlocks, insertLat
}

// rc5RunOverlappingVacua issues two concurrent single-statement mass deletes
// over overlapping parents through different page subsets, mirroring two
// VacuumSource calls for two sources that share artifacts. It returns the
// observed deadlock and lock-timeout counts.
func rc5RunOverlappingVacua(t *testing.T, db *sql.DB, f rc5ArtifactFamily, parentIDs []int64, fanOut, iterations int) (deadlocks, timeouts, failures int64) {
	t.Helper()
	odd := fmt.Sprintf(`DELETE FROM %s WHERE %s = ANY($1) AND index_id %% 2 = 1 AND index_id > $2`, f.link, f.parentID)
	even := fmt.Sprintf(`DELETE FROM %s WHERE %s = ANY($1) AND index_id %% 2 = 0 AND index_id > $2`, f.link, f.parentID)

	for i := 0; i < iterations; i++ {
		start := make(chan struct{})
		var wg sync.WaitGroup
		run := func(stmt string) {
			defer wg.Done()
			<-start
			if _, err := db.Exec(stmt, pq.Array(parentIDs), 0); err != nil {
				switch {
				case rc5IsDeadlock(err):
					atomic.AddInt64(&deadlocks, 1)
				case rc5IsLockTimeout(err):
					atomic.AddInt64(&timeouts, 1)
				default:
					atomic.AddInt64(&failures, 1)
				}
			}
		}
		wg.Add(2)
		go run(odd)
		go run(even)
		close(start)
		wg.Wait()
	}
	return
}

// rc5WriteResults appends samples to the CSV and prints a summary table.
func rc5WriteResults(t *testing.T, samples []rc5Sample) {
	t.Helper()
	outDir := strings.TrimSpace(os.Getenv(rc5BenchOutEnv))
	if outDir == "" {
		outDir = t.TempDir()
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("create output dir: %v", err)
	}
	path := filepath.Join(outDir, "shared-artifact-contention.csv")
	fh, err := os.Create(path)
	if err != nil {
		t.Fatalf("create CSV: %v", err)
	}
	defer fh.Close()
	w := csv.NewWriter(fh)
	_ = w.Write([]string{
		"variant", "workload", "family", "concurrency", "ops", "failures", "deadlocks",
		"lock_wait_ratio", "p50_ms", "p95_ms", "p99_ms", "ops_per_sec", "duration_ms",
	})
	for _, s := range samples {
		_ = w.Write([]string{
			s.variant, s.workload, s.family, fmt.Sprint(s.concurrency),
			fmt.Sprint(s.ops), fmt.Sprint(s.failures), fmt.Sprint(s.deadlocks),
			fmt.Sprintf("%.4f", s.lockWaitRatio),
			fmt.Sprintf("%.3f", float64(s.p50.Microseconds())/1000),
			fmt.Sprintf("%.3f", float64(s.p95.Microseconds())/1000),
			fmt.Sprintf("%.3f", float64(s.p99.Microseconds())/1000),
			fmt.Sprintf("%.2f", s.opsPerSec),
			fmt.Sprintf("%.1f", float64(s.duration.Microseconds())/1000),
		})
	}
	w.Flush()
	if err := w.Error(); err != nil {
		t.Fatalf("write CSV: %v", err)
	}

	t.Logf("RC5 bench results: %s", path)
	t.Logf("%-10s %-14s %-10s %5s %9s %8s %9s %8s %8s", "variant", "workload", "family", "conc", "ops/s", "p95ms", "p99ms", "fail", "lock%")
	for _, s := range samples {
		t.Logf("%-10s %-14s %-10s %5d %9.1f %8.2f %9.2f %8d %7.1f%%",
			s.variant, s.workload, s.family, s.concurrency, s.opsPerSec,
			float64(s.p95.Microseconds())/1000, float64(s.p99.Microseconds())/1000,
			s.failures, s.lockWaitRatio*100)
	}
}

// TestPostgresSharedArtifactContentionBenchmark is the opt-in measurement.
func TestPostgresSharedArtifactContentionBenchmark(t *testing.T) {
	if strings.TrimSpace(os.Getenv(rc5BenchEnv)) != "1" {
		t.Skipf("%s=1 is required to run the shared-artifact benchmark", rc5BenchEnv)
	}
	quick := strings.TrimSpace(os.Getenv("RC5_BENCH_QUICK")) == "1"

	// Matrix sizes. Keep the default run bounded (~5-8 minutes on a laptop).
	massParents := 8
	massFanOut := 200
	inserters := 8
	iterations := 3
	churnWorkers := 8
	churnOps := 120
	if quick {
		massParents, massFanOut, inserters, iterations, churnWorkers, churnOps = 4, 50, 4, 1, 4, 40
	}

	variants := rc5Variants(t)
	samples := make([]rc5Sample, 0, len(variants)*4)
	for _, variant := range variants {
		variant := variant
		t.Run(variant.name, func(t *testing.T) {
			db, cleanup := rc5NewDatabase(t)
			defer cleanup()
			db.SetMaxOpenConns(64)
			rc5InstallVariant(t, db, variant)

			for _, f := range rc5Families() {
				f := f

				// W1: mass delete (VacuumSource shape) with concurrent inserts.
				parentIDs := rc5ResetFamily(t, db, f, massParents, massFanOut)
				deadBefore := rc5Deadlocks(t, db)
				sampler := rc5StartLockSampler(db)
				elapsed, deleted, insertFailures, insertDeadlocks, insertLat :=
					rc5RunMassDelete(t, db, f, parentIDs, massFanOut, massFanOut/2, inserters)
				ratio := sampler.stopAndRatio()
				dead := rc5Deadlocks(t, db) - deadBefore
				samples = append(samples, rc5Sample{
					variant: variant.name, workload: "mass_delete", family: f.name,
					concurrency: inserters + 1, ops: deleted, failures: insertFailures + insertDeadlocks,
					deadlocks: dead + insertDeadlocks, lockWaitRatio: ratio,
					p50: insertLat.percentile(0.50), p95: insertLat.percentile(0.95), p99: insertLat.percentile(0.99),
					opsPerSec: float64(deleted) / elapsed.Seconds(), duration: elapsed,
				})

				// W2: single-link churn (refresh shape: one DELETE per link).
				single := rc5ResetFamily(t, db, f, 1, massFanOut)
				churnDeadBefore := rc5Deadlocks(t, db)
				churnSampler := rc5StartLockSampler(db)
				lat, failures, churnDeadlocks, churnElapsed := rc5RunSingleLinkChurn(t, db, f, single[0], massFanOut, churnWorkers, churnOps)
				churnRatio := churnSampler.stopAndRatio()
				churnDead := rc5Deadlocks(t, db) - churnDeadBefore
				totalOps := int64(churnWorkers * churnOps)
				samples = append(samples, rc5Sample{
					variant: variant.name, workload: "single_link_churn", family: f.name,
					concurrency: churnWorkers, ops: totalOps, failures: failures,
					deadlocks: churnDead + churnDeadlocks, lockWaitRatio: churnRatio,
					p50: lat.percentile(0.50), p95: lat.percentile(0.95), p99: lat.percentile(0.99),
					opsPerSec: float64(totalOps) / churnElapsed.Seconds(), duration: churnElapsed,
				})

				// W3: overlapping vacua (deadlock probe).
				probeIDs := rc5ResetFamily(t, db, f, massParents, massFanOut)
				probeStart := time.Now()
				dl, to, fl := rc5RunOverlappingVacua(t, db, f, probeIDs, massFanOut, iterations)
				probeElapsed := time.Since(probeStart)
				samples = append(samples, rc5Sample{
					variant: variant.name, workload: "overlapping_vacua", family: f.name,
					concurrency: 2, ops: int64(iterations * 2), failures: fl + to,
					deadlocks: dl, lockWaitRatio: 0,
					opsPerSec: float64(iterations*2) / probeElapsed.Seconds(), duration: probeElapsed,
				})
			}
		})
	}
	rc5WriteResults(t, samples)
}
