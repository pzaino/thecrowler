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

// PostgreSQL concurrency, contention and characterization coverage for the
// atomic fenced refresh path (WP-05B3C3B2F1 / B3 Decision 2).
//
// The original package proved the single-transaction happy path, rollback and
// cross-source refusal. The tests here add the properties a release gate needs:
// the multi-batch loop, the lock graph under concurrent refreshers plus an
// ordinary indexer, the page-lock boundary that makes the cross-source decision
// non-racy, the orphan-lock boundary that protects another page's reference,
// the pre-existing orphan-cleanup trigger that actually performs most of the
// work on PostgreSQL, and a reproducer for the legacy shared-object hazard.
//
// Every page body used here is unique to the test run. Earlier tests in this
// package reuse constant bodies such as "generation one", which means their
// pages share one globally hash-deduplicated WebObjects row; mixing unique and
// shared bodies would make those older expectations depend on test ordering.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sync"
	"testing"
	"time"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

// fencedRefreshQuerier is the minimal database surface the assertions need. It
// matches both cdb.Handler and *sql.DB so a test can assert through the runtime
// handler or through an administrative connection.
type fencedRefreshQuerier interface {
	QueryRow(query string, args ...interface{}) *sql.Row
	Exec(query string, args ...interface{}) (sql.Result, error)
}

// openFencedRefreshRuntimeConnection opens one additional pool as the runtime
// CROWler role. Concurrency tests need an independent session so they can hold
// a lock while the main handler proceeds.
func openFencedRefreshRuntimeConnection(t *testing.T) *sql.DB {
	t.Helper()
	host := fencedRefreshPGValue("DOCKER_POSTGRES_DB_HOST", "127.0.0.1")
	port := fencedRefreshPGValue("DOCKER_POSTGRES_DB_PORT", "5432")
	user := fencedRefreshPGValue("DOCKER_POSTGRES_DB_USER",
		fencedRefreshPGValue("DOCKER_POSTGRES_USER", "postgres"))
	password := fencedRefreshPGValue("DOCKER_POSTGRES_PASSWORD", "postgres")
	dbname := fencedRefreshPGValue("DOCKER_POSTGRES_DB_NAME", "SitesIndex")
	sslmode := fencedRefreshPGValue("DOCKER_POSTGRES_SSL_MODE", "disable")
	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		host, port, user, password, dbname, sslmode)
	db, err := sql.Open(cdb.DBPostgresStr, dsn)
	if err != nil {
		t.Fatalf("open runtime connection: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Ping(); err != nil {
		t.Fatalf("ping runtime connection: %v", err)
	}
	return db
}

// setFencedRefreshLockTimeout bounds a session's lock wait so a genuine
// deadlock or an unexpected wait fails the test quickly instead of hanging CI.
func setFencedRefreshLockTimeout(t *testing.T, db *sql.DB, timeout string) {
	t.Helper()
	if _, err := db.Exec(`SET lock_timeout = '` + timeout + `'`); err != nil {
		t.Fatalf("set lock_timeout: %v", err)
	}
}

func fencedRefreshSeedObject(t *testing.T, db *sql.DB, label string) uint64 {
	t.Helper()
	sum := sha256.Sum256([]byte(fencedRefreshUnique("object-" + label)))
	var objectID uint64
	if err := db.QueryRow(`
		INSERT INTO WebObjects (object_hash, object_content, object_html, details)
		VALUES ($1, $2, '<p>x</p>', '{}'::jsonb)
		RETURNING object_id`, hex.EncodeToString(sum[:]), label).Scan(&objectID); err != nil {
		t.Fatalf("seed object %q: %v", label, err)
	}
	return objectID
}

func fencedRefreshLinkObject(t *testing.T, db fencedRefreshQuerier, indexID, objectID uint64) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO WebObjectsIndex (index_id, object_id) VALUES ($1, $2)`,
		indexID, objectID); err != nil {
		t.Fatalf("link object %d to page %d: %v", objectID, indexID, err)
	}
}

func fencedRefreshCountObjects(t *testing.T, db fencedRefreshQuerier, objectIDs []uint64) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM WebObjects WHERE object_id = ANY($1::bigint[])`,
		pqUint64Array(objectIDs)).Scan(&count); err != nil {
		t.Fatalf("count objects: %v", err)
	}
	return count
}

func fencedRefreshCountAttributes(t *testing.T, db fencedRefreshQuerier, objectID uint64) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM ObjectAttributes
		WHERE object_type = 'webobject' AND object_id = $1`, objectID).Scan(&count); err != nil {
		t.Fatalf("count attributes: %v", err)
	}
	return count
}

func fencedRefreshCountLinks(t *testing.T, db fencedRefreshQuerier, indexID, objectID uint64) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM WebObjectsIndex WHERE index_id = $1 AND object_id = $2`,
		indexID, objectID).Scan(&count); err != nil {
		t.Fatalf("count links: %v", err)
	}
	return count
}

// TestPostgresFencedRefreshBatchesLargeOldGeneration exercises the bounded loop
// with more than two batches on a real transaction and records the wall time so
// the transaction budget can be compared with the documented default.
func TestPostgresFencedRefreshBatchesLargeOldGeneration(t *testing.T) {
	handler, admin := openFencedRefreshPostgres(t)
	_, sourceID, ctx, _, _ := fencedRefreshEnvironment(t, handler, admin, "batch")
	unique := fencedRefreshUnique("batch-body")
	pageURL := ctx.source.URL + "page"
	if _, err := indexPageContext(context.Background(), ctx, pageURL,
		fencedRefreshPageInfo(ctx, sourceID, "generation one "+unique)); err != nil {
		t.Fatalf("initial index: %v", err)
	}
	indexID := fencedRefreshPageIndex(t, handler, pageURL)

	// Seed a stale generation larger than two batches directly, plus one shared
	// object that another page still references.
	const oldCount = fencedRefreshObjectBatch*2 + 29
	oldIDs := make([]uint64, 0, oldCount)
	for i := 0; i < oldCount; i++ {
		objectID := fencedRefreshSeedObject(t, admin, fmt.Sprintf("batch-%d-%s", i, unique))
		fencedRefreshLinkObject(t, admin, indexID, objectID)
		oldIDs = append(oldIDs, objectID)
	}
	sharedID := fencedRefreshSeedObject(t, admin, "batch-shared-"+unique)
	fencedRefreshLinkObject(t, admin, indexID, sharedID)
	sharedPageURL := ctx.source.URL + "shared"
	if _, err := indexPageContext(context.Background(), ctx, sharedPageURL,
		fencedRefreshPageInfo(ctx, sourceID, "shared page "+unique)); err != nil {
		t.Fatalf("shared page index: %v", err)
	}
	sharedPageIndex := fencedRefreshPageIndex(t, handler, sharedPageURL)
	fencedRefreshLinkObject(t, admin, sharedPageIndex, sharedID)

	started := time.Now()
	if _, err := indexPageContext(context.Background(), ctx, pageURL,
		fencedRefreshPageInfo(ctx, sourceID, "generation two "+unique)); err != nil {
		t.Fatalf("fenced refresh: %v", err)
	}
	t.Logf("batched fenced refresh of %d stale links took %s",
		oldCount+1, time.Since(started))

	if got := fencedRefreshCountObjects(t, handler, oldIDs); got != 0 {
		t.Fatalf("%d stale objects survived the batched cleanup", got)
	}
	var pageLinks int
	if err := handler.QueryRow(`SELECT COUNT(*) FROM WebObjectsIndex WHERE index_id = $1`,
		indexID).Scan(&pageLinks); err != nil {
		t.Fatalf("count page links: %v", err)
	}
	if pageLinks != 1 {
		t.Fatalf("refreshed page kept %d links, want the replacement only", pageLinks)
	}
	if got := fencedRefreshCountObjects(t, handler, []uint64{sharedID}); got != 1 {
		t.Fatalf("batched cleanup removed a shared object: %d", got)
	}
	if got := fencedRefreshCountLinks(t, handler, sharedPageIndex, sharedID); got != 1 {
		t.Fatalf("another page lost its shared reference: %d", got)
	}
}

// TestPostgresFencedRefreshPageLockBlocksConcurrentLink proves the page row
// lock is what makes the cross-source decision sound. A concurrent
// SourceSearchIndex insert for the page must wait for the refresh's FOR UPDATE
// lock, so it cannot slip between the ownership check and the replacement.
func TestPostgresFencedRefreshPageLockBlocksConcurrentLink(t *testing.T) {
	handler, admin := openFencedRefreshPostgres(t)
	sourceURL, sourceID, ctx, _, _ := fencedRefreshEnvironment(t, handler, admin, "pagelock")
	unique := fencedRefreshUnique("pagelock-body")
	pageURL := sourceURL + "page"
	if _, err := indexPageContext(context.Background(), ctx, pageURL,
		fencedRefreshPageInfo(ctx, sourceID, "generation one "+unique)); err != nil {
		t.Fatalf("initial index: %v", err)
	}
	indexID := fencedRefreshPageIndex(t, handler, pageURL)

	otherSourceID := fencedRefreshSeedSource(t, admin, "pagelock-other")
	linker := openFencedRefreshRuntimeConnection(t)
	// A generous lock timeout keeps the waiting insert from timing out before
	// the test releases the page lock; the assertion below is about blocking,
	// not about the timeout path.
	setFencedRefreshLockTimeout(t, linker, "5s")

	refresh := openFencedRefreshRuntimeConnection(t)
	tx, err := refresh.Begin()
	if err != nil {
		t.Fatalf("begin refresh transaction: %v", err)
	}
	if _, exists, err := lockFencedRefreshPageTx(context.Background(), tx,
		cdb.DBPostgresStr, pageURL); err != nil || !exists {
		_ = tx.Rollback()
		t.Fatalf("lock page = exists:%v err=%v", exists, err)
	}

	// A concurrent source trying to link the page must be blocked by the lock.
	linkErr := make(chan error, 1)
	go func() {
		_, err := linker.Exec(
			`INSERT INTO SourceSearchIndex (source_id, index_id) VALUES ($1, $2)`,
			otherSourceID, indexID)
		linkErr <- err
	}()
	select {
	case err := <-linkErr:
		_ = tx.Rollback()
		t.Fatalf("concurrent link completed through the page lock: err=%v", err)
	case <-time.After(300 * time.Millisecond):
		// Expected: the insert is waiting for the page row lock.
	}

	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback refresh transaction: %v", err)
	}
	select {
	case err := <-linkErr:
		if err != nil {
			t.Fatalf("concurrent link failed after the lock was released: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent link did not proceed after the page lock was released")
	}

	var linked int
	if err := handler.QueryRow(`SELECT COUNT(*) FROM SourceSearchIndex WHERE source_id = $1 AND index_id = $2`,
		otherSourceID, indexID).Scan(&linked); err != nil {
		t.Fatalf("count link: %v", err)
	}
	if linked != 1 {
		t.Fatalf("concurrent link was not persisted: %d", linked)
	}
}

// TestPostgresFencedRefreshOrphanLockWaitsForInFlightReference proves the orphan
// decision cannot race a reference created by another page.
//
// A second session inserts a WebObjectsIndex row for the candidate orphan and
// holds it uncommitted, so it owns a KEY SHARE lock on the object row. The
// refresh's FOR UPDATE lock must wait, then observe the committed reference and
// keep the object and its attributes.
func TestPostgresFencedRefreshOrphanLockWaitsForInFlightReference(t *testing.T) {
	handler, admin := openFencedRefreshPostgres(t)
	sourceURL, sourceID, ctx, _, _ := fencedRefreshEnvironment(t, handler, admin, "orphanlock")
	unique := fencedRefreshUnique("orphanlock-body")
	pageURL := sourceURL + "page"
	otherPageURL := sourceURL + "other"
	if _, err := indexPageContext(context.Background(), ctx, pageURL,
		fencedRefreshPageInfo(ctx, sourceID, "generation one "+unique)); err != nil {
		t.Fatalf("initial index: %v", err)
	}
	if _, err := indexPageContext(context.Background(), ctx, otherPageURL,
		fencedRefreshPageInfo(ctx, sourceID, "other page "+unique)); err != nil {
		t.Fatalf("other page index: %v", err)
	}
	otherIndexID := fencedRefreshPageIndex(t, handler, otherPageURL)

	candidateID := fencedRefreshSeedObject(t, admin, "orphan-candidate-"+unique)
	if _, err := admin.Exec(`
		INSERT INTO ObjectAttributes
			(object_id, object_type, attribute_key, attribute_value,
			 normalized_value, value_hash)
		VALUES ($1, 'webobject', 'price', '20', '20', repeat('0', 64))`,
		candidateID); err != nil {
		t.Fatalf("seed attribute: %v", err)
	}

	linker := openFencedRefreshRuntimeConnection(t)
	linkTx, err := linker.Begin()
	if err != nil {
		t.Fatalf("begin linker transaction: %v", err)
	}
	if _, err = linkTx.Exec(`INSERT INTO WebObjectsIndex (index_id, object_id) VALUES ($1, $2)`,
		otherIndexID, candidateID); err != nil {
		_ = linkTx.Rollback()
		t.Fatalf("link other page: %v", err)
	}

	refresh := openFencedRefreshRuntimeConnection(t)
	tx, err := refresh.Begin()
	if err != nil {
		_ = linkTx.Rollback()
		t.Fatalf("begin refresh transaction: %v", err)
	}

	deleteDone := make(chan error, 1)
	go func() {
		deleteDone <- deleteFencedRefreshOrphanTx(context.Background(), tx,
			cdb.DBPostgresStr, candidateID)
	}()
	select {
	case err := <-deleteDone:
		_ = tx.Rollback()
		_ = linkTx.Rollback()
		t.Fatalf("orphan delete did not wait for the in-flight reference: err=%v", err)
	case <-time.After(300 * time.Millisecond):
		// Expected: the object row lock is held by the uncommitted linker.
	}

	if err := linkTx.Commit(); err != nil {
		_ = tx.Rollback()
		t.Fatalf("commit linker: %v", err)
	}
	select {
	case err := <-deleteDone:
		if err != nil {
			_ = tx.Rollback()
			t.Fatalf("orphan delete failed after the linker committed: %v", err)
		}
	case <-time.After(5 * time.Second):
		_ = tx.Rollback()
		t.Fatal("orphan delete did not finish after the linker committed")
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit refresh: %v", err)
	}

	if got := fencedRefreshCountObjects(t, handler, []uint64{candidateID}); got != 1 {
		t.Fatalf("object referenced by another page was deleted: %d", got)
	}
	if got := fencedRefreshCountAttributes(t, handler, candidateID); got != 1 {
		t.Fatalf("shared object lost attributes: %d", got)
	}
	if got := fencedRefreshCountLinks(t, handler, otherIndexID, candidateID); got != 1 {
		t.Fatalf("other page lost its reference: %d", got)
	}
}

// TestPostgresFencedRefreshOrphanCleanupTriggerCharacterization documents which
// component actually deletes an orphaned WebObjects row on PostgreSQL.
//
// PostgreSQL schema 1.17 already installs trg_after_delete_webobjectsindex,
// which calls handle_shared_entity_deletion() after every WebObjectsIndex
// delete. The fenced helper deleteFencedRefreshOrphanTx therefore usually finds
// the row already gone. SQLite has no equivalent trigger, so there the helper
// performs the delete. Both converge on the same observable state, but the
// release record must not claim the explicit helper is the authority on
// PostgreSQL when a pre-existing per-row trigger runs first.
func TestPostgresFencedRefreshOrphanCleanupTriggerCharacterization(t *testing.T) {
	handler, admin := openFencedRefreshPostgres(t)
	_, _, _, _, _ = fencedRefreshEnvironment(t, handler, admin, "trigger")
	unique := fencedRefreshUnique("trigger-body")

	pageAURL := "https://trigger-characterization.invalid/" + unique + "/a"
	pageBURL := "https://trigger-characterization.invalid/" + unique + "/b"
	var pageAID, pageBID uint64
	if err := admin.QueryRow(`INSERT INTO SearchIndex (page_url, title, summary)
		VALUES ($1, 'a', '') RETURNING index_id`, pageAURL).Scan(&pageAID); err != nil {
		t.Fatalf("insert page A: %v", err)
	}
	if err := admin.QueryRow(`INSERT INTO SearchIndex (page_url, title, summary)
		VALUES ($1, 'b', '') RETURNING index_id`, pageBURL).Scan(&pageBID); err != nil {
		t.Fatalf("insert page B: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(`DELETE FROM WebObjectsIndex WHERE index_id = ANY($1::bigint[])`,
			pqUint64Array([]uint64{pageAID, pageBID}))
		_, _ = admin.Exec(`DELETE FROM SearchIndex WHERE index_id = ANY($1::bigint[])`,
			pqUint64Array([]uint64{pageAID, pageBID}))
	})

	sharedID := fencedRefreshSeedObject(t, admin, "trigger-shared-"+unique)
	fencedRefreshLinkObject(t, admin, pageAID, sharedID)
	fencedRefreshLinkObject(t, admin, pageBID, sharedID)

	// Removing one page's link must not delete a referenced object.
	if _, err := admin.Exec(`DELETE FROM WebObjectsIndex WHERE index_id = $1 AND object_id = $2`,
		pageAID, sharedID); err != nil {
		t.Fatalf("delete page A link: %v", err)
	}
	if got := fencedRefreshCountObjects(t, handler, []uint64{sharedID}); got != 1 {
		t.Fatalf("trigger deleted an object still referenced by page B: %d", got)
	}

	// Removing the last link must delete the object without any explicit helper
	// call. This is the pre-existing trigger, not the fenced refresh code.
	if _, err := admin.Exec(`DELETE FROM WebObjectsIndex WHERE index_id = $1 AND object_id = $2`,
		pageBID, sharedID); err != nil {
		t.Fatalf("delete page B link: %v", err)
	}
	if got := fencedRefreshCountObjects(t, handler, []uint64{sharedID}); got != 0 {
		t.Fatalf("orphan-cleanup trigger did not delete the unreferenced object: %d", got)
	}
}

// TestPostgresFencedRefreshConcurrentRefreshersAndIndexer runs two fenced
// refreshes plus an ordinary indexer that all converge on one shared content
// hash, so they contend on the same WebObjects row while consuming the advisory
// lock and their own page and source locks.
//
// The shared object is already referenced by two untouched pages, so no
// transaction is allowed to delete it. The test asserts all three complete
// without a deadlock or lock timeout and that every page keeps exactly one
// link. The object-orphan race that the pre-existing trigger exposes is
// covered separately by TestPostgresSharedObjectTriggerRaceCharacterization,
// because it is a schema-level defect and not a property of this lock graph.
func TestPostgresFencedRefreshConcurrentRefreshersAndIndexer(t *testing.T) {
	handler, admin := openFencedRefreshPostgres(t)
	_, sourceA, ctxA, _, _ := fencedRefreshEnvironment(t, handler, admin, "conc-a")
	_, sourceB, ctxB, _, _ := fencedRefreshEnvironment(t, handler, admin, "conc-b")
	unique := fencedRefreshUnique("conc-body")
	pageA := ctxA.source.URL + "page"
	pageB := ctxB.source.URL + "page"

	// One shared body, pre-linked by two pages that are never refreshed, so the
	// object must survive every concurrent transaction.
	sharedBody := "concurrent shared body " + unique
	holderA := ctxA.source.URL + "holder"
	holderB := ctxB.source.URL + "holder"
	if _, err := indexPageContext(context.Background(), ctxA, holderA,
		fencedRefreshPageInfo(ctxA, sourceA, sharedBody)); err != nil {
		t.Fatalf("holder A index: %v", err)
	}
	if _, err := indexPageContext(context.Background(), ctxB, holderB,
		fencedRefreshPageInfo(ctxB, sourceB, sharedBody)); err != nil {
		t.Fatalf("holder B index: %v", err)
	}

	contentA := "concurrent body alpha " + unique
	contentB := "concurrent body beta " + unique
	if _, err := indexPageContext(context.Background(), ctxA, pageA,
		fencedRefreshPageInfo(ctxA, sourceA, contentA)); err != nil {
		t.Fatalf("index A: %v", err)
	}
	if _, err := indexPageContext(context.Background(), ctxB, pageB,
		fencedRefreshPageInfo(ctxB, sourceB, contentB)); err != nil {
		t.Fatalf("index B: %v", err)
	}

	// Ordinary (non-fenced) indexing of the shared body, attached to a third
	// page of source A. A nil sourceCrawl makes validateIndexingFence a no-op,
	// which is exactly the legacy indexing path: no source-row lock and no
	// advisory lock.
	ordinaryCtx := &ProcessContext{
		source: ctxA.source,
		db:     ctxA.db,
		Status: &Status{},
	}
	ordinaryCtx.config.Crawler.RefreshContent = false
	ordinaryCtx.config.Crawler.IndexingConcurrency = 0
	ordinaryURL := ctxA.source.URL + "ordinary"
	if _, err := indexPageContext(context.Background(), ordinaryCtx, ordinaryURL,
		fencedRefreshPageInfo(ordinaryCtx, sourceA, sharedBody)); err != nil {
		t.Fatalf("ordinary index: %v", err)
	}

	// Bound each refresh with a context deadline so a genuine deadlock fails
	// fast instead of stalling the suite. The production handlers do not set a
	// lock_timeout, so the context is the practical guard.
	deadline := time.Now().Add(45 * time.Second)
	iterations := 5
	for iteration := 0; iteration < iterations; iteration++ {
		if time.Now().After(deadline) {
			t.Fatal("concurrent refresh iterations exceeded their time budget")
		}

		iterationCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		var wg sync.WaitGroup
		errs := make(chan error, 3)
		wg.Add(3)
		go func() {
			defer wg.Done()
			_, err := indexPageContext(iterationCtx, ctxA, pageA,
				fencedRefreshPageInfo(ctxA, sourceA, sharedBody))
			errs <- err
		}()
		go func() {
			defer wg.Done()
			_, err := indexPageContext(iterationCtx, ctxB, pageB,
				fencedRefreshPageInfo(ctxB, sourceB, sharedBody))
			errs <- err
		}()
		go func() {
			defer wg.Done()
			_, err := indexPageContext(iterationCtx, ordinaryCtx, ordinaryURL,
				fencedRefreshPageInfo(ordinaryCtx, sourceA, sharedBody))
			errs <- err
		}()
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				cancel()
				t.Fatalf("iteration %d: %v", iteration, err)
			}
		}
		cancel()

		// Every page must still own exactly one object link.
		for _, pageURL := range []string{pageA, pageB, ordinaryURL, holderA, holderB} {
			indexID := fencedRefreshPageIndex(t, handler, pageURL)
			var links int
			if err := handler.QueryRow(`SELECT COUNT(*) FROM WebObjectsIndex WHERE index_id = $1`,
				indexID).Scan(&links); err != nil {
				t.Fatalf("iteration %d: count links for %s: %v", iteration, pageURL, err)
			}
			if links != 1 {
				t.Fatalf("iteration %d: page %s kept %d links", iteration, pageURL, links)
			}
		}
	}
}

// TestPostgresSharedObjectTriggerRaceCharacterization pins a pre-existing
// schema-1.17 concurrency defect that Decision 2 does not introduce and does not
// fix.
//
// Schema 1.17 installs trg_after_delete_webobjectsindex. For every deleted
// WebObjectsIndex row it runs handle_shared_entity_deletion(), which does
//
//	IF (SELECT COUNT(*) FROM WebObjectsIndex WHERE object_id = OLD.object_id) = 0
//	THEN DELETE FROM WebObjects WHERE object_id = OLD.object_id;
//
// The count and the delete are separate READ COMMITTED statements and no row
// lock is taken first. A concurrent transaction that has inserted its own link
// to the same object but not yet committed is invisible to the count. The
// delete then blocks on that transaction's foreign-key row lock, and after the
// commit proceeds anyway. WebObjectsIndex.object_id is ON DELETE CASCADE, so the
// other page's link disappears without any error.
//
// The fenced helper deleteFencedRefreshOrphanTx avoids this pattern by locking
// the object FOR UPDATE and re-counting, but the trigger fires first, during
// the link deletion, so the fenced path inherits the defect. The legacy path is
// worse: it deletes the object row outright.
//
// This is a characterization test. It asserts the current defective outcome so
// the release record carries reproducible evidence. An approved fix (for
// example, locking the WebObjects row FOR UPDATE before the count, which
// requires a schema-function change) must update this expectation deliberately.
func TestPostgresSharedObjectTriggerRaceCharacterization(t *testing.T) {
	handler, admin := openFencedRefreshPostgres(t)
	unique := fencedRefreshUnique("trigger-race")

	pageXURL := "https://trigger-race.invalid/" + unique + "/x"
	pageYURL := "https://trigger-race.invalid/" + unique + "/y"
	var pageX, pageY uint64
	if err := admin.QueryRow(`INSERT INTO SearchIndex (page_url, title, summary)
		VALUES ($1, 'x', '') RETURNING index_id`, pageXURL).Scan(&pageX); err != nil {
		t.Fatalf("insert page X: %v", err)
	}
	if err := admin.QueryRow(`INSERT INTO SearchIndex (page_url, title, summary)
		VALUES ($1, 'y', '') RETURNING index_id`, pageYURL).Scan(&pageY); err != nil {
		t.Fatalf("insert page Y: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(`DELETE FROM WebObjectsIndex WHERE index_id = ANY($1::bigint[])`,
			pqUint64Array([]uint64{pageX, pageY}))
		_, _ = admin.Exec(`DELETE FROM SearchIndex WHERE index_id = ANY($1::bigint[])`,
			pqUint64Array([]uint64{pageX, pageY}))
	})

	objectID := fencedRefreshSeedObject(t, admin, "trigger-race-"+unique)
	fencedRefreshLinkObject(t, admin, pageX, objectID)

	connA := openFencedRefreshRuntimeConnection(t)
	connB := openFencedRefreshRuntimeConnection(t)

	txB, err := connB.Begin()
	if err != nil {
		t.Fatalf("begin linker: %v", err)
	}
	if _, err = txB.Exec(`INSERT INTO WebObjectsIndex (index_id, object_id) VALUES ($1, $2)`,
		pageY, objectID); err != nil {
		_ = txB.Rollback()
		t.Fatalf("link page Y: %v", err)
	}

	txA, err := connA.Begin()
	if err != nil {
		_ = txB.Rollback()
		t.Fatalf("begin deleter: %v", err)
	}
	deleteDone := make(chan error, 1)
	go func() {
		_, err := txA.ExecContext(context.Background(),
			`DELETE FROM WebObjectsIndex WHERE index_id = $1 AND object_id = $2`,
			pageX, objectID)
		deleteDone <- err
	}()
	select {
	case err := <-deleteDone:
		_ = txA.Rollback()
		_ = txB.Rollback()
		t.Fatalf("link delete did not wait for the in-flight reference: err=%v", err)
	case <-time.After(300 * time.Millisecond):
		// Expected: the orphan-cleanup DELETE is waiting for the linker.
	}

	if err := txB.Commit(); err != nil {
		_ = txA.Rollback()
		t.Fatalf("commit linker: %v", err)
	}
	select {
	case err := <-deleteDone:
		if err != nil {
			_ = txA.Rollback()
			t.Fatalf("link delete failed after the linker committed: %v", err)
		}
	case <-time.After(5 * time.Second):
		_ = txA.Rollback()
		t.Fatal("link delete did not finish after the linker committed")
	}
	if err := txA.Commit(); err != nil {
		t.Fatalf("commit deleter: %v", err)
	}

	objectRows := fencedRefreshCountObjects(t, handler, []uint64{objectID})
	pageYLinks := fencedRefreshCountLinks(t, handler, pageY, objectID)
	if objectRows != 0 || pageYLinks != 0 {
		t.Fatalf("shared-object trigger race is no longer reproducible: "+
			"object_rows=%d page_y_links=%d "+
			"(if this changed because the orphan trigger was fixed, update this "+
			"characterization test and the release record)",
			objectRows, pageYLinks)
	}
}

// TestPostgresLegacyRefreshSharedObjectHazardReproducer pins the pre-existing
// legacy refresh behaviour that Decision 2 deliberately does not change.
//
// The legacy path deletes the object row rather than the page link. When two
// pages hash to identical content they share one WebObjects row, so refreshing
// either page removes the object and its attributes for both. This test exists
// so the release record has a reproducible demonstration instead of an
// assertion in prose. It is a characterization test, not an endorsement: a
// future approved fix must update this expectation deliberately.
func TestPostgresLegacyRefreshSharedObjectHazardReproducer(t *testing.T) {
	handler, admin := openFencedRefreshPostgres(t)
	unique := fencedRefreshUnique("legacy-hazard")
	sourceID := fencedRefreshSeedSource(t, admin, unique)

	pageAURL := "https://legacy-hazard.invalid/" + unique + "/a"
	pageBURL := "https://legacy-hazard.invalid/" + unique + "/b"
	var pageAID, pageBID uint64
	if err := admin.QueryRow(`INSERT INTO SearchIndex (page_url, title, summary)
		VALUES ($1, 'a', '') RETURNING index_id`, pageAURL).Scan(&pageAID); err != nil {
		t.Fatalf("insert page A: %v", err)
	}
	if err := admin.QueryRow(`INSERT INTO SearchIndex (page_url, title, summary)
		VALUES ($1, 'b', '') RETURNING index_id`, pageBURL).Scan(&pageBID); err != nil {
		t.Fatalf("insert page B: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(`DELETE FROM WebObjectsIndex WHERE index_id = ANY($1::bigint[])`,
			pqUint64Array([]uint64{pageAID, pageBID}))
		_, _ = admin.Exec(`DELETE FROM SourceSearchIndex WHERE source_id = $1`, sourceID)
		_, _ = admin.Exec(`DELETE FROM SearchIndex WHERE index_id = ANY($1::bigint[])`,
			pqUint64Array([]uint64{pageAID, pageBID}))
	})

	sharedID := fencedRefreshSeedObject(t, admin, "legacy-shared")
	fencedRefreshLinkObject(t, admin, pageAID, sharedID)
	fencedRefreshLinkObject(t, admin, pageBID, sharedID)
	if _, err := admin.Exec(`
		INSERT INTO ObjectAttributes
			(object_id, object_type, attribute_key, attribute_value,
			 normalized_value, value_hash)
		VALUES ($1, 'webobject', 'price', '20', '20', repeat('0', 64))`,
		sharedID); err != nil {
		t.Fatalf("seed attribute: %v", err)
	}

	// This is exactly what the legacy refresh branch calls before the
	// replacement page transaction.
	if err := deleteWebObjects(context.Background(), &handler, pageAID); err != nil {
		t.Fatalf("legacy deleteWebObjects: %v", err)
	}

	objectRows := fencedRefreshCountObjects(t, handler, []uint64{sharedID})
	pageBLinks := fencedRefreshCountLinks(t, handler, pageBID, sharedID)
	attributes := fencedRefreshCountAttributes(t, handler, sharedID)

	if objectRows != 0 || pageBLinks != 0 || attributes != 0 {
		t.Fatalf("legacy shared-object hazard is not reproducible: "+
			"object_rows=%d page_b_links=%d attributes=%d "+
			"(if this changed because the legacy delete was fixed, update this "+
			"characterization test and the release record)",
			objectRows, pageBLinks, attributes)
	}
}

// fencedRefreshSeedSource inserts a plain source row owned by user 7 and
// schedules its removal.
func fencedRefreshSeedSource(t *testing.T, admin *sql.DB, label string) uint64 {
	t.Helper()
	unique := fencedRefreshUnique(label)
	sourceURL := "https://" + unique + ".invalid/"
	var sourceID uint64
	if err := admin.QueryRow(`
		INSERT INTO Sources
			(source_uid, url, name, status, restricted, disabled, usr_id,
			 priority, sub_priority, config)
		VALUES ($1, $2, $3, 'new', 0, FALSE, 7, $4, 0, '{}'::jsonb)
		RETURNING source_id`,
		cdb.CalculateSourceUID(unique, sourceURL), sourceURL, unique,
		"rel230-"+unique).Scan(&sourceID); err != nil {
		t.Fatalf("insert source %q: %v", label, err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(`DELETE FROM SourceCrawlClaims WHERE source_id = $1`, sourceID)
		_, _ = admin.Exec(`DELETE FROM SourceCrawlAttempts WHERE source_id = $1`, sourceID)
		_, _ = admin.Exec(`DELETE FROM SourceSearchIndex WHERE source_id = $1`, sourceID)
		_, _ = admin.Exec(`DELETE FROM Sources WHERE source_id = $1`, sourceID)
	})
	return sourceID
}
