//go:build integration

package database

// WP-REL230-RC5: deterministic integrity proof for the shared-artifact cleanup
// trigger on the real schema 1.17 layout.
//
// These tests run against an already initialised CROWler PostgreSQL database
// (the same convention as the other PostgreSQL integration tests):
//
//	THECROWLER_POSTGRES_INTEGRATION=1 \
//	DOCKER_POSTGRES_DB_HOST=127.0.0.1 DOCKER_POSTGRES_DB_PORT=... \
//	go test -tags=integration ./pkg/database -run '^TestPostgresSharedArtifact' -count=1 -v
//
// They prove the properties the cleanup trigger must keep:
//
//	A  a concurrent uncommitted link insertion is not destroyed by a last-link
//	   removal in another transaction;
//	B  two concurrent last-link removals do not leave an unreferenced parent;
//	C  a non-last removal on a widely shared parent does not disturb inserts;
//	D  the final removal deletes the parent (no permanently delayed cleanup);
//	E  contention with random parent order preserves integrity;
//	F  vacuum-shaped and cascade-shaped removals preserve other pages' links;
//	G  the mechanism relies on READ COMMITTED re-reading after the row lock.

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// rc5PGFamilies describes the real schema insert/delete statements needed to
// drive each artifact family generically.
type rc5PGFamily struct {
	name         string
	parent       string
	link         string
	parentID     string
	insertParent string
	insertLink   string
	deleteLink   string
}

func rc5PGFamilies() []rc5PGFamily {
	return []rc5PGFamily{
		{
			name: "keyword", parent: "Keywords", link: "KeywordIndex", parentID: "keyword_id",
			insertParent: `INSERT INTO Keywords (keyword) VALUES ($1) RETURNING keyword_id`,
			insertLink:   `INSERT INTO KeywordIndex (keyword_id, index_id, occurrences) VALUES ($1, $2, 1)`,
			deleteLink:   `DELETE FROM KeywordIndex WHERE keyword_id = $1 AND index_id = $2`,
		},
		{
			name: "metatag", parent: "MetaTags", link: "MetaTagsIndex", parentID: "metatag_id",
			insertParent: `INSERT INTO MetaTags (name, content) VALUES ($1, 'rc5') RETURNING metatag_id`,
			insertLink:   `INSERT INTO MetaTagsIndex (metatag_id, index_id) VALUES ($1, $2)`,
			deleteLink:   `DELETE FROM MetaTagsIndex WHERE metatag_id = $1 AND index_id = $2`,
		},
		{
			name: "webobject", parent: "WebObjects", link: "WebObjectsIndex", parentID: "object_id",
			insertParent: `INSERT INTO WebObjects (object_hash, object_content, object_html, details)
				VALUES ($1, 'rc5', '<p>rc5</p>', '{}'::jsonb) RETURNING object_id`,
			insertLink: `INSERT INTO WebObjectsIndex (object_id, index_id) VALUES ($1, $2)`,
			deleteLink: `DELETE FROM WebObjectsIndex WHERE object_id = $1 AND index_id = $2`,
		},
		{
			name: "netinfo", parent: "NetInfo", link: "NetInfoIndex", parentID: "netinfo_id",
			insertParent: `INSERT INTO NetInfo (details_hash, details) VALUES ($1, '{}'::jsonb) RETURNING netinfo_id`,
			insertLink:   `INSERT INTO NetInfoIndex (netinfo_id, index_id) VALUES ($1, $2)`,
			deleteLink:   `DELETE FROM NetInfoIndex WHERE netinfo_id = $1 AND index_id = $2`,
		},
		{
			name: "httpinfo", parent: "HTTPInfo", link: "HTTPInfoIndex", parentID: "httpinfo_id",
			insertParent: `INSERT INTO HTTPInfo (details_hash, details) VALUES ($1, '{}'::jsonb) RETURNING httpinfo_id`,
			insertLink:   `INSERT INTO HTTPInfoIndex (httpinfo_id, index_id) VALUES ($1, $2)`,
			deleteLink:   `DELETE FROM HTTPInfoIndex WHERE httpinfo_id = $1 AND index_id = $2`,
		},
	}
}

// ---------------------------------------------------------------------------
// plumbing
// ---------------------------------------------------------------------------

func rc5PGOptedIn() bool {
	return strings.TrimSpace(rc5EnvOr("THECROWLER_POSTGRES_INTEGRATION", "")) == "1"
}

func rc5EnvOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}

// rc5PGDB opens a pool against the initialised integration database.
func rc5PGDB(t *testing.T) *sql.DB {
	t.Helper()
	if !rc5PGOptedIn() {
		t.Skip("THECROWLER_POSTGRES_INTEGRATION=1 is required for RC5 shared-artifact integrity tests")
	}
	host := rc5EnvOr("DOCKER_POSTGRES_DB_HOST", "127.0.0.1")
	port := rc5EnvOr("DOCKER_POSTGRES_DB_PORT", "5432")
	user := rc5EnvOr("DOCKER_POSTGRES_DB_USER", rc5EnvOr("DOCKER_POSTGRES_USER", "postgres"))
	password := rc5EnvOr("DOCKER_POSTGRES_PASSWORD", "postgres")
	dbname := rc5EnvOr("DOCKER_POSTGRES_DB_NAME", "SitesIndex")
	sslmode := rc5EnvOr("DOCKER_POSTGRES_SSL_MODE", "disable")
	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		host, port, user, password, dbname, sslmode)
	db, err := sql.Open(DBPostgresStr, dsn)
	if err != nil {
		t.Fatalf("open integration database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Ping(); err != nil {
		t.Fatalf("ping integration database: %v", err)
	}
	db.SetMaxOpenConns(32)
	return db
}

// rc5PGSession returns a dedicated connection so a test can hold locks.
func rc5PGSession(t *testing.T, db *sql.DB) *sql.Conn {
	t.Helper()
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("dedicated connection: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func rc5PGBackendPID(t *testing.T, conn *sql.Conn) int {
	t.Helper()
	var pid int
	if err := conn.QueryRowContext(context.Background(), `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatalf("read backend pid: %v", err)
	}
	return pid
}

// rc5PGWaitForLockWait blocks until the given backend is observed waiting on a
// lock, so a test never depends on a sleep to establish ordering.
func rc5PGWaitForLockWait(t *testing.T, db *sql.DB, pid int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var waiting bool
		err := db.QueryRow(`
			SELECT EXISTS (
				SELECT 1 FROM pg_stat_activity
				WHERE pid = $1 AND wait_event_type = 'Lock')`, pid).Scan(&waiting)
		if err == nil && waiting {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("backend %d never blocked on a lock", pid)
}

func rc5PGNewPage(t *testing.T, db *sql.DB, label string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`INSERT INTO SearchIndex (page_url, summary) VALUES ($1, 'rc5') RETURNING index_id`,
		"https://rc5.invalid/"+label).Scan(&id); err != nil {
		t.Fatalf("create page %q: %v", label, err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM SearchIndex WHERE index_id = $1`, id) })
	return id
}

func rc5PGNewParent(t *testing.T, db *sql.DB, f rc5PGFamily, label string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(f.insertParent, "rc5-"+label).Scan(&id); err != nil {
		t.Fatalf("create %s parent %q: %v", f.name, label, err)
	}
	t.Cleanup(func() { _, _ = db.Exec(fmt.Sprintf(`DELETE FROM %s WHERE %s = $1`, f.parent, f.parentID), id) })
	return id
}

func rc5PGLink(t *testing.T, db *sql.DB, f rc5PGFamily, parentID, pageID int64) {
	t.Helper()
	if _, err := db.Exec(f.insertLink, parentID, pageID); err != nil {
		t.Fatalf("link %s %d -> page %d: %v", f.name, parentID, pageID, err)
	}
}

func rc5PGUnlink(t *testing.T, db *sql.DB, f rc5PGFamily, parentID, pageID int64) {
	t.Helper()
	if _, err := db.Exec(f.deleteLink, parentID, pageID); err != nil {
		t.Fatalf("unlink %s %d -> page %d: %v", f.name, parentID, pageID, err)
	}
}

func rc5PGParentExists(t *testing.T, db *sql.DB, f rc5PGFamily, parentID int64) bool {
	t.Helper()
	var exists bool
	if err := db.QueryRow(fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s WHERE %s = $1)`, f.parent, f.parentID),
		parentID).Scan(&exists); err != nil {
		t.Fatalf("check %s parent %d: %v", f.name, parentID, err)
	}
	return exists
}

func rc5PGLinkCount(t *testing.T, db *sql.DB, f rc5PGFamily, parentID int64) int {
	t.Helper()
	var n int
	if err := db.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE %s = $1`, f.link, f.parentID),
		parentID).Scan(&n); err != nil {
		t.Fatalf("count %s links for %d: %v", f.name, parentID, err)
	}
	return n
}

func rc5PGLinkExists(t *testing.T, db *sql.DB, f rc5PGFamily, parentID, pageID int64) bool {
	t.Helper()
	var exists bool
	if err := db.QueryRow(fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s WHERE %s = $1 AND index_id = $2)`,
		f.link, f.parentID), parentID, pageID).Scan(&exists); err != nil {
		t.Fatalf("check %s link: %v", f.name, err)
	}
	return exists
}

// ---------------------------------------------------------------------------
// case A: concurrent insert must survive a last-link removal
// ---------------------------------------------------------------------------

func TestPostgresSharedArtifactConcurrentInsertSurvivesLastLinkDelete(t *testing.T) {
	db := rc5PGDB(t)
	for _, f := range rc5PGFamilies() {
		f := f
		t.Run(f.name, func(t *testing.T) {
			parentID := rc5PGNewParent(t, db, f, "a-"+f.name+"-"+rc5RandomSuffix(t))
			pageX := rc5PGNewPage(t, db, "a-x-"+f.name+"-"+rc5RandomSuffix(t))
			pageY := rc5PGNewPage(t, db, "a-y-"+f.name+"-"+rc5RandomSuffix(t))
			rc5PGLink(t, db, f, parentID, pageX)

			// B inserts a new link and keeps the transaction open, holding the
			// foreign-key KEY SHARE lock on the parent row.
			inserter := rc5PGSession(t, db)
			ctx := context.Background()
			tx, err := inserter.BeginTx(ctx, nil)
			if err != nil {
				t.Fatalf("begin inserter: %v", err)
			}
			if _, err := tx.ExecContext(ctx, f.insertLink, parentID, pageY); err != nil {
				_ = tx.Rollback()
				t.Fatalf("insert concurrent link: %v", err)
			}

			// A removes the only visible link. Its trigger must block on the
			// parent row lock, so this runs on its own connection.
			remover := rc5PGSession(t, db)
			removerPID := rc5PGBackendPID(t, remover)
			removeCtx := context.Background()
			rmTx, err := remover.BeginTx(removeCtx, nil)
			if err != nil {
				t.Fatalf("begin remover: %v", err)
			}
			done := make(chan error, 1)
			go func() {
				_, execErr := rmTx.ExecContext(removeCtx, f.deleteLink, parentID, pageX)
				done <- execErr
			}()

			rc5PGWaitForLockWait(t, db, removerPID)

			if err := tx.Commit(); err != nil {
				t.Fatalf("commit inserter: %v", err)
			}
			if err := <-done; err != nil {
				_ = rmTx.Rollback()
				t.Fatalf("delete last visible link: %v", err)
			}
			if err := rmTx.Commit(); err != nil {
				t.Fatalf("commit remover: %v", err)
			}

			if !rc5PGParentExists(t, db, f, parentID) {
				t.Fatalf("%s parent was deleted although a concurrent link was committed", f.name)
			}
			if !rc5PGLinkExists(t, db, f, parentID, pageY) {
				t.Fatalf("%s concurrent link was silently removed by ON DELETE CASCADE", f.name)
			}
			if rc5PGLinkExists(t, db, f, parentID, pageX) {
				t.Fatalf("%s removed link still present", f.name)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// case B: two concurrent last-link removals must not orphan the parent
// ---------------------------------------------------------------------------

func TestPostgresSharedArtifactConcurrentLastLinkRemovals(t *testing.T) {
	db := rc5PGDB(t)
	for _, f := range rc5PGFamilies() {
		f := f
		t.Run(f.name, func(t *testing.T) {
			for i := 0; i < 10; i++ {
				label := fmt.Sprintf("b-%s-%d-%s", f.name, i, rc5RandomSuffix(t))
				parentID := rc5PGNewParent(t, db, f, label)
				pageX := rc5PGNewPage(t, db, "b-x-"+label)
				pageY := rc5PGNewPage(t, db, "b-y-"+label)
				rc5PGLink(t, db, f, parentID, pageX)
				rc5PGLink(t, db, f, parentID, pageY)

				connX := rc5PGSession(t, db)
				connY := rc5PGSession(t, db)
				start := make(chan struct{})
				var wg sync.WaitGroup
				var failures int64
				run := func(conn *sql.Conn, pageID int64) {
					defer wg.Done()
					<-start
					ctx := context.Background()
					tx, err := conn.BeginTx(ctx, nil)
					if err != nil {
						atomic.AddInt64(&failures, 1)
						return
					}
					if _, err := tx.ExecContext(ctx, f.deleteLink, parentID, pageID); err != nil {
						_ = tx.Rollback()
						atomic.AddInt64(&failures, 1)
						return
					}
					if err := tx.Commit(); err != nil {
						atomic.AddInt64(&failures, 1)
					}
				}
				wg.Add(2)
				go run(connX, pageX)
				go run(connY, pageY)
				close(start)
				wg.Wait()
				if failures != 0 {
					t.Fatalf("iteration %d: %d concurrent removals failed", i, failures)
				}

				exists := rc5PGParentExists(t, db, f, parentID)
				links := rc5PGLinkCount(t, db, f, parentID)
				if exists && links == 0 {
					t.Fatalf("%s iteration %d left parent %d with zero links (orphan leak)", f.name, i, parentID)
				}
				if !exists && links != 0 {
					t.Fatalf("%s iteration %d deleted parent %d but left %d links", f.name, i, parentID, links)
				}
				if exists {
					_, _ = db.Exec(fmt.Sprintf(`DELETE FROM %s WHERE %s = $1`, f.parent, f.parentID), parentID)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// case C: a non-last removal must not disturb concurrent inserts
// ---------------------------------------------------------------------------

func TestPostgresSharedArtifactWideParentRemovalKeepsInserts(t *testing.T) {
	db := rc5PGDB(t)
	const fanOut = 40
	for _, f := range rc5PGFamilies() {
		f := f
		t.Run(f.name, func(t *testing.T) {
			label := "c-" + f.name + "-" + rc5RandomSuffix(t)
			parentID := rc5PGNewParent(t, db, f, label)
			pages := make([]int64, 0, fanOut)
			for i := 0; i < fanOut; i++ {
				page := rc5PGNewPage(t, db, fmt.Sprintf("%s-p%d", label, i))
				pages = append(pages, page)
				rc5PGLink(t, db, f, parentID, page)
			}

			// Concurrently add links for pages that do not exist in the parent yet.
			newPages := make([]int64, 0, 8)
			for i := 0; i < 8; i++ {
				page := rc5PGNewPage(t, db, fmt.Sprintf("%s-n%d", label, i))
				newPages = append(newPages, page)
			}
			start := make(chan struct{})
			var wg sync.WaitGroup
			var insertFailures int64
			for _, page := range newPages {
				page := page
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					if _, err := db.Exec(f.insertLink, parentID, page); err != nil {
						atomic.AddInt64(&insertFailures, 1)
					}
				}()
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if _, err := db.Exec(f.deleteLink, parentID, pages[0]); err != nil {
					atomic.AddInt64(&insertFailures, 1)
				}
			}()
			close(start)
			wg.Wait()
			if insertFailures != 0 {
				t.Fatalf("%s: %d operations failed under non-last removal contention", f.name, insertFailures)
			}
			if !rc5PGParentExists(t, db, f, parentID) {
				t.Fatalf("%s parent deleted although %d links remained", f.name, fanOut-1+len(newPages))
			}
			for _, page := range newPages {
				if !rc5PGLinkExists(t, db, f, parentID, page) {
					t.Fatalf("%s lost a concurrently inserted link to page %d", f.name, page)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// case D: the final removal must delete the parent (no delayed cleanup)
// ---------------------------------------------------------------------------

func TestPostgresSharedArtifactFinalRemovalDeletesParent(t *testing.T) {
	db := rc5PGDB(t)
	for _, f := range rc5PGFamilies() {
		f := f
		t.Run(f.name, func(t *testing.T) {
			label := "d-" + f.name + "-" + rc5RandomSuffix(t)
			parentID := rc5PGNewParent(t, db, f, label)
			pageX := rc5PGNewPage(t, db, label+"-x")
			pageY := rc5PGNewPage(t, db, label+"-y")
			rc5PGLink(t, db, f, parentID, pageX)
			rc5PGLink(t, db, f, parentID, pageY)

			rc5PGUnlink(t, db, f, parentID, pageX)
			if !rc5PGParentExists(t, db, f, parentID) {
				t.Fatalf("%s parent deleted while one link remained", f.name)
			}
			rc5PGUnlink(t, db, f, parentID, pageY)
			if rc5PGParentExists(t, db, f, parentID) {
				t.Fatalf("%s parent survived its final link removal (orphan retention)", f.name)
			}
			if rc5PGLinkCount(t, db, f, parentID) != 0 {
				t.Fatalf("%s links survived parent deletion", f.name)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// case E: random parent order under contention preserves integrity
// ---------------------------------------------------------------------------

func TestPostgresSharedArtifactRandomOrderContention(t *testing.T) {
	db := rc5PGDB(t)
	for _, f := range rc5PGFamilies() {
		f := f
		t.Run(f.name, func(t *testing.T) {
			label := "e-" + f.name + "-" + rc5RandomSuffix(t)
			const parents = 4
			const pagesPerParent = 6
			parentIDs := make([]int64, 0, parents)
			pageIDs := make([][]int64, parents)
			for p := 0; p < parents; p++ {
				parentIDs = append(parentIDs, rc5PGNewParent(t, db, f, fmt.Sprintf("%s-p%d", label, p)))
				for i := 0; i < pagesPerParent; i++ {
					page := rc5PGNewPage(t, db, fmt.Sprintf("%s-%d-%d", label, p, i))
					pageIDs[p] = append(pageIDs[p], page)
					rc5PGLink(t, db, f, parentIDs[p], page)
				}
			}

			const workers = 6
			const iterations = 20
			start := make(chan struct{})
			var wg sync.WaitGroup
			var failures, deadlocks int64
			for w := 0; w < workers; w++ {
				w := w
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					conn := rc5PGSession(t, db)
					if _, err := conn.ExecContext(context.Background(), `SET lock_timeout = '2000ms'`); err != nil {
						atomic.AddInt64(&failures, 1)
						return
					}
					for i := 0; i < iterations; i++ {
						// Each worker removes one link from a randomly chosen
						// parent, so transactions acquire parent locks in
						// different orders. A deadlock here is a caller-visible
						// error (SQLSTATE 40P01), not data loss.
						p := (w + i) % parents
						page := pageIDs[p][(w+i)%pagesPerParent]
						ctx := context.Background()
						tx, err := conn.BeginTx(ctx, nil)
						if err != nil {
							atomic.AddInt64(&failures, 1)
							continue
						}
						if _, err := tx.ExecContext(ctx, f.deleteLink, parentIDs[p], page); err != nil {
							_ = tx.Rollback()
							if rc5IsDeadlock(err) {
								atomic.AddInt64(&deadlocks, 1)
								continue
							}
							atomic.AddInt64(&failures, 1)
							continue
						}
						// Re-link so the workload stays bounded and repeatable.
						// Two workers may target the same link; ON CONFLICT keeps
						// the workload a delete/insert churn instead of a unique
						// violation race.
						if _, err := tx.ExecContext(ctx, f.insertLink+" ON CONFLICT DO NOTHING", parentIDs[p], page); err != nil {
							_ = tx.Rollback()
							if rc5IsDeadlock(err) {
								atomic.AddInt64(&deadlocks, 1)
								continue
							}
							atomic.AddInt64(&failures, 1)
							continue
						}
						if err := tx.Commit(); err != nil {
							if rc5IsDeadlock(err) {
								atomic.AddInt64(&deadlocks, 1)
								continue
							}
							atomic.AddInt64(&failures, 1)
						}
					}
				}()
			}
			close(start)
			wg.Wait()
			if failures != 0 {
				t.Fatalf("%s: %d non-deadlock failures under contention", f.name, failures)
			}
			t.Logf("%s: %d deadlock aborts under %d workers x %d iterations", f.name, deadlocks, workers, iterations)

			for p, parentID := range parentIDs {
				links := rc5PGLinkCount(t, db, f, parentID)
				if links != pagesPerParent {
					t.Fatalf("%s parent %d ended with %d links, want %d (lost or duplicated links)",
						f.name, p, links, pagesPerParent)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// case F: vacuum-shaped and cascade-shaped removals preserve other pages
// ---------------------------------------------------------------------------

func TestPostgresSharedArtifactVacuumAndCascadeKeepOtherPages(t *testing.T) {
	db := rc5PGDB(t)
	for _, f := range rc5PGFamilies() {
		f := f
		t.Run(f.name, func(t *testing.T) {
			label := "f-" + f.name + "-" + rc5RandomSuffix(t)
			parentID := rc5PGNewParent(t, db, f, label)
			pageA := rc5PGNewPage(t, db, label+"-a")
			pageB := rc5PGNewPage(t, db, label+"-b")
			rc5PGLink(t, db, f, parentID, pageA)
			rc5PGLink(t, db, f, parentID, pageB)

			// VacuumSource shape: one statement deleting this page's links.
			if _, err := db.Exec(fmt.Sprintf(`DELETE FROM %s WHERE index_id = $1`, f.link), pageA); err != nil {
				t.Fatalf("vacuum-shaped delete: %v", err)
			}
			if !rc5PGParentExists(t, db, f, parentID) {
				t.Fatalf("%s parent deleted while page B still referenced it", f.name)
			}
			if !rc5PGLinkExists(t, db, f, parentID, pageB) {
				t.Fatalf("%s page B link lost by vacuum-shaped delete", f.name)
			}

			// Cascade shape: deleting the page row cascades its links.
			if _, err := db.Exec(`DELETE FROM SearchIndex WHERE index_id = $1`, pageB); err != nil {
				t.Fatalf("delete page B: %v", err)
			}
			if rc5PGParentExists(t, db, f, parentID) {
				t.Fatalf("%s parent survived removal of its final link through cascade", f.name)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// case G: READ COMMITTED re-read after the row lock is the mechanism
// ---------------------------------------------------------------------------

func TestPostgresSharedArtifactIsolationLevelIsReadCommitted(t *testing.T) {
	db := rc5PGDB(t)
	var level string
	if err := db.QueryRow(`SHOW default_transaction_isolation`).Scan(&level); err != nil {
		t.Fatalf("read default isolation: %v", err)
	}
	if strings.ToLower(strings.TrimSpace(level)) != "read committed" {
		t.Fatalf("the cleanup trigger's correctness argument assumes READ COMMITTED, found %q", level)
	}
	if err := db.QueryRow(`SELECT current_setting('transaction_isolation')`).Scan(&level); err != nil {
		t.Fatalf("read transaction isolation: %v", err)
	}
	if strings.ToLower(strings.TrimSpace(level)) != "read committed" {
		t.Fatalf("active transaction isolation must be READ COMMITTED, found %q", level)
	}
}

// ---------------------------------------------------------------------------
// characterization: the pre-RC2 body still loses a concurrent link
// ---------------------------------------------------------------------------

// TestPostgresSharedArtifactLegacyBodyLosesConcurrentLink keeps the historical
// data-loss race reproducible. It uses an isolated fixture database because it
// installs a different function body; it never runs against the shipped schema.
func TestPostgresSharedArtifactLegacyBodyLosesConcurrentLink(t *testing.T) {
	if !rc5PGOptedIn() {
		t.Skip("THECROWLER_POSTGRES_INTEGRATION=1 is required")
	}
	db, cleanup := rc5NewDatabase(t)
	defer cleanup()
	db.SetMaxOpenConns(8)
	rc5InstallVariant(t, db, rc5Variant{name: "v228", body: rc5LegacyFunctionBody})

	f := rc5Families()[0] // keyword fixture
	parentID := rc5SeedHotParent(t, db, f, "legacy-loss", 1)
	pageX := int64(1)
	pageY := int64(2)

	inserter, err := db.Begin()
	if err != nil {
		t.Fatalf("begin inserter: %v", err)
	}
	if _, err := inserter.Exec(fmt.Sprintf(`INSERT INTO %s (%s, index_id) VALUES ($1, $2)`, f.link, f.parentID),
		parentID, pageY); err != nil {
		_ = inserter.Rollback()
		t.Fatalf("insert concurrent link: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, execErr := db.Exec(fmt.Sprintf(`DELETE FROM %s WHERE %s = $1 AND index_id = $2`, f.link, f.parentID),
			parentID, pageX)
		done <- execErr
	}()
	time.Sleep(200 * time.Millisecond)
	if err := inserter.Commit(); err != nil {
		t.Fatalf("commit inserter: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("legacy delete: %v", err)
	}

	var parentRows, linkRows int
	_ = db.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE %s = $1`, f.parent, f.parentID), parentID).Scan(&parentRows)
	_ = db.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE %s = $1`, f.link, f.parentID), parentID).Scan(&linkRows)
	if parentRows != 0 || linkRows != 0 {
		t.Fatalf("the pre-RC2 body no longer reproduces the data-loss race: parent_rows=%d link_rows=%d", parentRows, linkRows)
	}
	t.Logf("characterization confirmed: the pre-RC2 body removed the parent and the concurrently committed link")
}

// rc5MigrationTriggerStatements extracts the DROP/CREATE TRIGGER statements the
// v1.17 migration uses to replace the link-table triggers.
func rc5MigrationTriggerStatements(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile("db_migrations/postgresql-migration-v1.17.pgsql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	text := string(raw)
	start := strings.Index(text, "DROP TRIGGER IF EXISTS trg_after_delete_metatagsindex ON MetaTagsIndex;")
	if start < 0 {
		t.Fatal("migration does not replace trg_after_delete_metatagsindex")
	}
	end := strings.Index(text[start:], "UPDATE DBSchemaVersion")
	if end < 0 {
		t.Fatal("migration trigger block is not terminated")
	}
	block := text[start : start+end]
	parts := strings.Split(block, ";")
	stmts := make([]string, 0, len(parts))
	for _, part := range parts {
		stmt := strings.TrimSpace(part)
		if stmt == "" || strings.HasPrefix(stmt, "--") {
			continue
		}
		stmts = append(stmts, stmt)
	}
	if len(stmts) != 10 {
		t.Fatalf("expected 5 DROP + 5 CREATE trigger statements, got %d", len(stmts))
	}
	return stmts
}

// TestPostgresSharedArtifactTriggerDefinitionConverges proves that a database
// still carrying the previous row-level trigger definition converges to the
// shipped statement-level definition when the v1.17 migration is re-applied.
func TestPostgresSharedArtifactTriggerDefinitionConverges(t *testing.T) {
	db := rc5PGDB(t)
	ctx := context.Background()

	linkTables := map[string]string{
		"trg_after_delete_metatagsindex":   "MetaTagsIndex",
		"trg_after_delete_webobjectsindex": "WebObjectsIndex",
		"trg_after_delete_keywordindex":    "KeywordIndex",
		"trg_after_delete_netinfoindex":    "NetInfoIndex",
		"trg_after_delete_httpinfoindex":   "HTTPInfoIndex",
	}

	assertShape := func(label string, wantRowLevel bool) {
		t.Helper()
		for trigger, table := range linkTables {
			var rowLevel bool
			var oldTable sql.NullString
			err := db.QueryRow(`
				SELECT (tgtype & 1) = 1, tgoldtable
				FROM pg_trigger
				WHERE tgrelid = $1::regclass AND tgname = $2`,
				table, trigger).Scan(&rowLevel, &oldTable)
			if err != nil {
				t.Fatalf("%s: read trigger %s: %v", label, trigger, err)
			}
			if rowLevel != wantRowLevel {
				t.Fatalf("%s: %s row_level=%v, want %v", label, trigger, rowLevel, wantRowLevel)
			}
			if wantRowLevel && oldTable.Valid {
				t.Fatalf("%s: %s unexpectedly has a transition table", label, trigger)
			}
			if !wantRowLevel && (!oldTable.Valid || oldTable.String != "deleted_links") {
				t.Fatalf("%s: %s transition table = %q, want deleted_links", label, trigger, oldTable.String)
			}
		}
	}

	stmts := rc5MigrationTriggerStatements(t)

	// Simulate a database migrated with the previous row-level definition.
	for trigger, table := range linkTables {
		if _, err := db.ExecContext(ctx, fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON %s`, trigger, table)); err != nil {
			t.Fatalf("drop %s: %v", trigger, err)
		}
		if _, err := db.ExecContext(ctx, fmt.Sprintf(
			`CREATE TRIGGER %s AFTER DELETE ON %s FOR EACH ROW EXECUTE FUNCTION handle_shared_entity_deletion()`,
			trigger, table)); err != nil {
			t.Fatalf("create legacy %s: %v", trigger, err)
		}
	}
	assertShape("legacy row-level", true)

	// Re-apply the shipped migration trigger block.
	for _, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("apply migration statement %q: %v", stmt, err)
		}
	}
	assertShape("after migration", false)

	// The function body must be the shipped statement-level one.
	var prosrc string
	if err := db.QueryRow(`SELECT prosrc FROM pg_proc WHERE proname = 'handle_shared_entity_deletion'`).Scan(&prosrc); err != nil {
		t.Fatalf("read function body: %v", err)
	}
	if !strings.Contains(prosrc, "SELECT DISTINCT keyword_id FROM deleted_links ORDER BY keyword_id") {
		t.Error("migrated function does not de-duplicate and order keyword parents")
	}
	if strings.Contains(prosrc, "OLD.") {
		t.Error("migrated function still references OLD.<column>")
	}

	// Leave the shared integration database on the shipped definition.
	t.Cleanup(func() {
		for _, stmt := range stmts {
			_, _ = db.ExecContext(context.Background(), stmt)
		}
	})
}

// rc5SingleStatementFunctionBody is the "obvious" simplification: replace the
// lock-and-count with one DELETE whose NOT EXISTS predicate re-checks the
// reference. It is kept only to demonstrate that the simplification is unsafe.
const rc5SingleStatementFunctionBody = `CREATE OR REPLACE FUNCTION handle_shared_entity_deletion()
RETURNS TRIGGER AS $$
BEGIN
    IF TG_TABLE_NAME = 'metatagsindex' THEN
        DELETE FROM MetaTags m WHERE m.metatag_id = OLD.metatag_id
            AND NOT EXISTS (SELECT 1 FROM MetaTagsIndex i WHERE i.metatag_id = OLD.metatag_id);
    ELSIF TG_TABLE_NAME = 'webobjectsindex' THEN
        DELETE FROM WebObjects w WHERE w.object_id = OLD.object_id
            AND NOT EXISTS (SELECT 1 FROM WebObjectsIndex i WHERE i.object_id = OLD.object_id);
    ELSIF TG_TABLE_NAME = 'keywordindex' THEN
        DELETE FROM Keywords k WHERE k.keyword_id = OLD.keyword_id
            AND NOT EXISTS (SELECT 1 FROM KeywordIndex i WHERE i.keyword_id = OLD.keyword_id);
    ELSIF TG_TABLE_NAME = 'netinfoindex' THEN
        DELETE FROM NetInfo n WHERE n.netinfo_id = OLD.netinfo_id
            AND NOT EXISTS (SELECT 1 FROM NetInfoIndex i WHERE i.netinfo_id = OLD.netinfo_id);
    ELSIF TG_TABLE_NAME = 'httpinfoindex' THEN
        DELETE FROM HTTPInfo h WHERE h.httpinfo_id = OLD.httpinfo_id
            AND NOT EXISTS (SELECT 1 FROM HTTPInfoIndex i WHERE i.httpinfo_id = OLD.httpinfo_id);
    END IF;
    RETURN OLD;
END;
$$ LANGUAGE plpgsql`

// TestPostgresSharedArtifactSingleStatementDeleteIsUnsafe proves that folding
// the lock and the count into one DELETE does not preserve the RC2 guarantee.
//
// PostgreSQL evaluates the DELETE qualification before it waits for the row
// lock, and a concurrent child INSERT does not create a new version of the
// parent tuple, so the qualification is not re-evaluated when the lock is
// finally granted. The uncommitted reference is invisible, the delete proceeds
// and ON DELETE CASCADE removes the committed link. The shipped function
// therefore keeps the explicit FOR UPDATE plus a separate re-check.
func TestPostgresSharedArtifactSingleStatementDeleteIsUnsafe(t *testing.T) {
	if !rc5PGOptedIn() {
		t.Skip("THECROWLER_POSTGRES_INTEGRATION=1 is required")
	}
	db, cleanup := rc5NewDatabase(t)
	defer cleanup()
	db.SetMaxOpenConns(8)
	rc5InstallVariant(t, db, rc5Variant{name: "one-statement", body: rc5SingleStatementFunctionBody})

	f := rc5Families()[0]
	parentID := rc5SeedHotParent(t, db, f, "single-statement-loss", 1)
	pageX := int64(1)
	pageY := int64(2)

	inserter, err := db.Begin()
	if err != nil {
		t.Fatalf("begin inserter: %v", err)
	}
	if _, err := inserter.Exec(fmt.Sprintf(`INSERT INTO %s (%s, index_id) VALUES ($1, $2)`, f.link, f.parentID),
		parentID, pageY); err != nil {
		_ = inserter.Rollback()
		t.Fatalf("insert concurrent link: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, execErr := db.Exec(fmt.Sprintf(`DELETE FROM %s WHERE %s = $1 AND index_id = $2`, f.link, f.parentID),
			parentID, pageX)
		done <- execErr
	}()
	time.Sleep(200 * time.Millisecond)
	if err := inserter.Commit(); err != nil {
		t.Fatalf("commit inserter: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("delete: %v", err)
	}

	var parentRows, linkRows int
	_ = db.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE %s = $1`, f.parent, f.parentID), parentID).Scan(&parentRows)
	_ = db.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE %s = $1`, f.link, f.parentID), parentID).Scan(&linkRows)
	if parentRows != 0 || linkRows != 0 {
		t.Fatalf("the one-statement form no longer reproduces the race (parent_rows=%d link_rows=%d); "+
			"re-verify the rejection argument before reconsidering it", parentRows, linkRows)
	}
	t.Logf("characterization confirmed: the one-statement DELETE form removes the parent and the concurrently committed link")
}
