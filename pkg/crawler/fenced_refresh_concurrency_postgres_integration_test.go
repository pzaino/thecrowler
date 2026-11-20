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
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/lib/pq"

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
// delete (once per statement since RC5, which does not change which component
// performs the delete). The fenced helper deleteFencedRefreshOrphanTx therefore
// usually finds the row already gone. SQLite has no equivalent trigger, so there
// the helper performs the delete. Both converge on the same observable state,
// but the release record must not claim the explicit helper is the authority on
// PostgreSQL when a pre-existing trigger runs first.
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

// TestPostgresSharedObjectTriggerPreservesConcurrentReference is the positive
// regression for the RC1 trigger race (WP-REL230-RC2 / RC2-A).
//
// Schema 1.17 installs trg_after_delete_webobjectsindex. For every deleted
// WebObjectsIndex row it runs handle_shared_entity_deletion(). Before RC2-A that
// function counted references and then deleted the parent as two separate READ
// COMMITTED statements with no parent-row lock, so an uncommitted concurrent
// link was invisible to the count, the delete waited and then proceeded, and
// WebObjectsIndex.object_id ON DELETE CASCADE silently removed the other page's
// committed link.
//
// RC2-A makes the function lock the parent row FOR UPDATE before counting. This
// test performs exactly that interleaving:
//
//  1. object O is linked to page X;
//  2. session B inserts WebObjectsIndex(Y, O) and does not commit, so it holds a
//     KEY SHARE lock on O;
//  3. session A deletes WebObjectsIndex(X, O); the trigger must block on O;
//  4. B commits;
//  5. A's trigger re-counts, sees B's committed reference, and preserves O.
//
// The assertion is the opposite of the RC1 characterization test: object_rows=1,
// page_y_links=1, and the object keeps its attributes.
func TestPostgresSharedObjectTriggerPreservesConcurrentReference(t *testing.T) {
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
	if _, err := admin.Exec(`
		INSERT INTO ObjectAttributes
			(object_id, object_type, attribute_key, attribute_value,
			 normalized_value, value_hash)
		VALUES ($1, 'webobject', 'price', '20', '20', repeat('0', 64))`,
		objectID); err != nil {
		t.Fatalf("seed attribute: %v", err)
	}

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
		// Expected: the orphan-cleanup trigger is waiting on the parent row.
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

	if got := fencedRefreshCountObjects(t, handler, []uint64{objectID}); got != 1 {
		t.Fatalf("concurrent reference lost the shared object: object_rows=%d", got)
	}
	if got := fencedRefreshCountLinks(t, handler, pageY, objectID); got != 1 {
		t.Fatalf("concurrent reference lost page Y's link: page_y_links=%d", got)
	}
	if got := fencedRefreshCountAttributes(t, handler, objectID); got != 1 {
		t.Fatalf("shared object lost attributes: %d", got)
	}
}

// TestPostgresSharedObjectTriggerDeletesZeroReferenceObject confirms the other
// side of the RC2-A contract: when no other page references the object, the
// trigger still deletes it, and a repeated delete statement is idempotent.
func TestPostgresSharedObjectTriggerDeletesZeroReferenceObject(t *testing.T) {
	handler, admin := openFencedRefreshPostgres(t)
	unique := fencedRefreshUnique("trigger-zero")

	pageURL := "https://trigger-zero.invalid/" + unique
	var pageID uint64
	if err := admin.QueryRow(`INSERT INTO SearchIndex (page_url, title, summary)
		VALUES ($1, 'zero', '') RETURNING index_id`, pageURL).Scan(&pageID); err != nil {
		t.Fatalf("insert page: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(`DELETE FROM WebObjectsIndex WHERE index_id = $1`, pageID)
		_, _ = admin.Exec(`DELETE FROM SearchIndex WHERE index_id = $1`, pageID)
	})

	objectID := fencedRefreshSeedObject(t, admin, "trigger-zero-"+unique)
	fencedRefreshLinkObject(t, admin, pageID, objectID)

	for attempt := 0; attempt < 3; attempt++ {
		if _, err := admin.Exec(`DELETE FROM WebObjectsIndex WHERE index_id = $1 AND object_id = $2`,
			pageID, objectID); err != nil {
			t.Fatalf("attempt %d delete: %v", attempt, err)
		}
		if got := fencedRefreshCountObjects(t, handler, []uint64{objectID}); got != 0 {
			t.Fatalf("attempt %d: zero-reference object survived: %d", attempt, got)
		}
	}
}

// TestPostgresLegacyRefreshPreservesSharedObject is the positive regression for
// RC2-B. Two pages share one hash-deduplicated object. Refreshing page X must
// replace only X's link and must leave page Y's link, the shared object and its
// attributes intact.
func TestPostgresLegacyRefreshPreservesSharedObject(t *testing.T) {
	handler, admin := openFencedRefreshPostgres(t)
	unique := fencedRefreshUnique("legacy-shared")
	sourceID := fencedRefreshSeedSource(t, admin, unique)
	pageXURL := "https://" + unique + ".invalid/x"
	pageYURL := "https://" + unique + ".invalid/y"

	ctx := openLegacyRefreshContext(t, handler, sourceID, pageXURL)
	t.Cleanup(func() {
		_, _ = admin.Exec(`DELETE FROM WebObjectsIndex WHERE index_id IN
			(SELECT index_id FROM SearchIndex WHERE page_url = ANY($1::text[]))`,
			pq.Array([]string{pageXURL, pageYURL}))
		_, _ = admin.Exec(`DELETE FROM SearchIndex WHERE page_url = ANY($1::text[])`,
			pq.Array([]string{pageXURL, pageYURL}))
	})

	sharedBody := "legacy shared body " + unique
	if _, err := indexPageContext(context.Background(), ctx, pageXURL,
		legacyRefreshPageInfo(ctx, sourceID, sharedBody)); err != nil {
		t.Fatalf("index X: %v", err)
	}
	if _, err := indexPageContext(context.Background(), ctx, pageYURL,
		legacyRefreshPageInfo(ctx, sourceID, sharedBody)); err != nil {
		t.Fatalf("index Y: %v", err)
	}

	pageXID := fencedRefreshPageIndex(t, handler, pageXURL)
	pageYID := fencedRefreshPageIndex(t, handler, pageYURL)
	sharedHash := fencedRefreshPageObjects(t, handler, pageXID)[0]
	if got := fencedRefreshPageObjects(t, handler, pageYID); len(got) != 1 || got[0] != sharedHash {
		t.Fatalf("pages did not converge on one object: x=%s y=%v", sharedHash, got)
	}
	var sharedObjectID uint64
	if err := handler.QueryRow(`SELECT object_id FROM WebObjects WHERE object_hash = $1`,
		sharedHash).Scan(&sharedObjectID); err != nil {
		t.Fatalf("read shared object: %v", err)
	}
	if _, err := admin.Exec(`
		INSERT INTO ObjectAttributes
			(object_id, object_type, attribute_key, attribute_value,
			 normalized_value, value_hash)
		VALUES ($1, 'webobject', 'price', '20', '20', repeat('0', 64))`,
		sharedObjectID); err != nil {
		t.Fatalf("seed attribute: %v", err)
	}

	if _, err := indexPageContext(context.Background(), ctx, pageXURL,
		legacyRefreshPageInfo(ctx, sourceID, "legacy changed body "+unique)); err != nil {
		t.Fatalf("legacy refresh X: %v", err)
	}

	if got := fencedRefreshPageObjects(t, handler, pageYID); len(got) != 1 || got[0] != sharedHash {
		t.Fatalf("refreshing X changed page Y's object: %v", got)
	}
	if got := fencedRefreshCountObjects(t, handler, []uint64{sharedObjectID}); got != 1 {
		t.Fatalf("refreshing X deleted the shared object: %d", got)
	}
	if got := fencedRefreshCountAttributes(t, handler, sharedObjectID); got != 1 {
		t.Fatalf("refreshing X removed the shared object's attributes: %d", got)
	}
	if got := fencedRefreshCountLinks(t, handler, pageYID, sharedObjectID); got != 1 {
		t.Fatalf("refreshing X removed page Y's link: %d", got)
	}
	if got := fencedRefreshPageObjects(t, handler, pageXID); len(got) != 1 || got[0] == sharedHash {
		t.Fatalf("refreshing X did not replace X's object: %v", got)
	}
}

// TestPostgresLegacyRefreshRefusesCrossSourcePage covers the deliberate RC2-B
// behaviour change: a legacy refresh of a page linked to more than one source is
// refused with an explicit error and zero writes, instead of silently rewriting
// associations another source depends on.
func TestPostgresLegacyRefreshRefusesCrossSourcePage(t *testing.T) {
	handler, admin := openFencedRefreshPostgres(t)
	unique := fencedRefreshUnique("legacy-cross")
	sourceID := fencedRefreshSeedSource(t, admin, unique)
	otherSourceID := fencedRefreshSeedSource(t, admin, "xsrc")
	pageURL := "https://" + unique + ".invalid/page"

	ctx := openLegacyRefreshContext(t, handler, sourceID, pageURL)
	t.Cleanup(func() {
		_, _ = admin.Exec(`DELETE FROM WebObjectsIndex WHERE index_id IN
			(SELECT index_id FROM SearchIndex WHERE page_url = $1)`, pageURL)
		_, _ = admin.Exec(`DELETE FROM SourceSearchIndex WHERE index_id IN
			(SELECT index_id FROM SearchIndex WHERE page_url = $1)`, pageURL)
		_, _ = admin.Exec(`DELETE FROM SearchIndex WHERE page_url = $1`, pageURL)
	})

	if _, err := indexPageContext(context.Background(), ctx, pageURL,
		legacyRefreshPageInfo(ctx, sourceID, "cross generation one "+unique)); err != nil {
		t.Fatalf("initial index: %v", err)
	}
	indexID := fencedRefreshPageIndex(t, handler, pageURL)
	if _, err := admin.Exec(`INSERT INTO SourceSearchIndex (source_id, index_id)
		VALUES ($1, $2) ON CONFLICT DO NOTHING`, otherSourceID, indexID); err != nil {
		t.Fatalf("link other source: %v", err)
	}
	oldObjects := fencedRefreshPageObjects(t, handler, indexID)

	_, err := indexPageContext(context.Background(), ctx, pageURL,
		legacyRefreshPageInfo(ctx, sourceID, "cross generation two "+unique))
	if !errors.Is(err, errFencedRefreshCrossSourcePage) {
		t.Fatalf("cross-source legacy refresh error = %v", err)
	}
	if got := fencedRefreshPageObjects(t, handler, indexID); len(got) != 1 || got[0] != oldObjects[0] {
		t.Fatalf("cross-source refusal changed page objects: %v", got)
	}
	var pageLinks int
	if err := handler.QueryRow(`SELECT COUNT(*) FROM WebObjectsIndex WHERE index_id = $1`,
		indexID).Scan(&pageLinks); err != nil {
		t.Fatalf("count page links: %v", err)
	}
	if pageLinks != 1 {
		t.Fatalf("refusal changed the page link count: %d", pageLinks)
	}
}

// TestPostgresLegacyRefreshRollbackRestoresOldGeneration injects a delete
// failure and confirms the unified legacy path rolls the whole replacement back:
// the old link, object and content survive.
func TestPostgresLegacyRefreshRollbackRestoresOldGeneration(t *testing.T) {
	handler, admin := openFencedRefreshPostgres(t)
	unique := fencedRefreshUnique("legacy-rollback")
	sourceID := fencedRefreshSeedSource(t, admin, unique)
	pageURL := "https://" + unique + ".invalid/page"

	ctx := openLegacyRefreshContext(t, handler, sourceID, pageURL)
	t.Cleanup(func() {
		_, _ = admin.Exec(`DROP TRIGGER IF EXISTS rc2_fail_legacy_refresh ON WebObjects`)
		_, _ = admin.Exec(`DROP FUNCTION IF EXISTS rc2_fail_legacy_refresh()`)
		_, _ = admin.Exec(`DELETE FROM WebObjectsIndex WHERE index_id IN
			(SELECT index_id FROM SearchIndex WHERE page_url = $1)`, pageURL)
		_, _ = admin.Exec(`DELETE FROM SearchIndex WHERE page_url = $1`, pageURL)
	})

	if _, err := indexPageContext(context.Background(), ctx, pageURL,
		legacyRefreshPageInfo(ctx, sourceID, "legacy rollback one "+unique)); err != nil {
		t.Fatalf("initial index: %v", err)
	}
	indexID := fencedRefreshPageIndex(t, handler, pageURL)
	oldHash := fencedRefreshPageObjects(t, handler, indexID)[0]

	if _, err := admin.Exec(`
		CREATE OR REPLACE FUNCTION rc2_fail_legacy_refresh() RETURNS trigger AS $$
		BEGIN
			RAISE EXCEPTION 'rc2 injected legacy refresh failure';
		END;
		$$ LANGUAGE plpgsql`); err != nil {
		t.Fatalf("create failure function: %v", err)
	}
	if _, err := admin.Exec(`
		CREATE TRIGGER rc2_fail_legacy_refresh BEFORE DELETE ON WebObjects
		FOR EACH ROW WHEN (OLD.object_hash = '` + oldHash + `')
		EXECUTE FUNCTION rc2_fail_legacy_refresh()`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	if _, err := indexPageContext(context.Background(), ctx, pageURL,
		legacyRefreshPageInfo(ctx, sourceID, "legacy rollback two "+unique)); err == nil {
		t.Fatal("injected failure did not fail the legacy refresh")
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
	if content != "legacy rollback one "+unique {
		t.Fatalf("rollback content = %q", content)
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

// TestPostgresSharedObjectTriggerStress repeats the RC2-A interleaving to catch
// scheduling-dependent regressions. Each iteration uses fresh pages and a fresh
// object, and the whole test is bounded so a lock-order regression fails fast
// instead of hanging CI.
func TestPostgresSharedObjectTriggerStress(t *testing.T) {
	handler, admin := openFencedRefreshPostgres(t)
	connA := openFencedRefreshRuntimeConnection(t)
	connB := openFencedRefreshRuntimeConnection(t)
	setFencedRefreshLockTimeout(t, connA, "10s")
	setFencedRefreshLockTimeout(t, connB, "10s")

	unique := fencedRefreshUnique("trigger-stress")
	deadline := time.Now().Add(90 * time.Second)

	for iteration := 0; iteration < 25; iteration++ {
		if time.Now().After(deadline) {
			t.Fatalf("stress iterations exceeded their budget at %d", iteration)
		}
		pageXURL := fmt.Sprintf("https://trigger-stress.invalid/%s/%d/x", unique, iteration)
		pageYURL := fmt.Sprintf("https://trigger-stress.invalid/%s/%d/y", unique, iteration)
		var pageX, pageY uint64
		if err := admin.QueryRow(`INSERT INTO SearchIndex (page_url, title, summary)
			VALUES ($1, 'x', '') RETURNING index_id`, pageXURL).Scan(&pageX); err != nil {
			t.Fatalf("iteration %d insert page X: %v", iteration, err)
		}
		if err := admin.QueryRow(`INSERT INTO SearchIndex (page_url, title, summary)
			VALUES ($1, 'y', '') RETURNING index_id`, pageYURL).Scan(&pageY); err != nil {
			t.Fatalf("iteration %d insert page Y: %v", iteration, err)
		}
		t.Cleanup(func() {
			_, _ = admin.Exec(`DELETE FROM WebObjectsIndex WHERE index_id = ANY($1::bigint[])`,
				pqUint64Array([]uint64{pageX, pageY}))
			_, _ = admin.Exec(`DELETE FROM SearchIndex WHERE index_id = ANY($1::bigint[])`,
				pqUint64Array([]uint64{pageX, pageY}))
		})

		objectID := fencedRefreshSeedObject(t, admin,
			fmt.Sprintf("trigger-stress-%s-%d", unique, iteration))
		fencedRefreshLinkObject(t, admin, pageX, objectID)

		txB, err := connB.Begin()
		if err != nil {
			t.Fatalf("iteration %d begin B: %v", iteration, err)
		}
		if _, err = txB.Exec(`INSERT INTO WebObjectsIndex (index_id, object_id) VALUES ($1, $2)`,
			pageY, objectID); err != nil {
			_ = txB.Rollback()
			t.Fatalf("iteration %d link Y: %v", iteration, err)
		}
		txA, err := connA.Begin()
		if err != nil {
			_ = txB.Rollback()
			t.Fatalf("iteration %d begin A: %v", iteration, err)
		}
		done := make(chan error, 1)
		go func() {
			_, err := txA.Exec(`DELETE FROM WebObjectsIndex WHERE index_id = $1 AND object_id = $2`,
				pageX, objectID)
			done <- err
		}()
		time.Sleep(5 * time.Millisecond)
		if err := txB.Commit(); err != nil {
			_ = txA.Rollback()
			t.Fatalf("iteration %d commit B: %v", iteration, err)
		}
		select {
		case err := <-done:
			if err != nil {
				_ = txA.Rollback()
				t.Fatalf("iteration %d delete A: %v", iteration, err)
			}
		case <-time.After(10 * time.Second):
			_ = txA.Rollback()
			t.Fatalf("iteration %d delete A did not finish", iteration)
		}
		if err := txA.Commit(); err != nil {
			t.Fatalf("iteration %d commit A: %v", iteration, err)
		}

		if got := fencedRefreshCountObjects(t, handler, []uint64{objectID}); got != 1 {
			t.Fatalf("iteration %d lost the shared object: %d", iteration, got)
		}
		if got := fencedRefreshCountLinks(t, handler, pageY, objectID); got != 1 {
			t.Fatalf("iteration %d lost page Y's link: %d", iteration, got)
		}
	}
}

// TestPostgresRefreshAndVacuumRemainConsistent runs a legacy refresh of source A
// while VacuumSource removes source B's links to the same shared object. Both
// paths now take the parent row lock before an orphan decision. The test asserts
// that both complete without a deadlock or silent inconsistency: the object
// exists exactly when at least one page still links it.
func TestPostgresRefreshAndVacuumRemainConsistent(t *testing.T) {
	handler, admin := openFencedRefreshPostgres(t)
	unique := fencedRefreshUnique("vacuum-shared")
	sourceA := fencedRefreshSeedSource(t, admin, "vsrc-a")
	sourceB := fencedRefreshSeedSource(t, admin, "vsrc-b")
	pageAURL := "https://" + unique + ".invalid/a"
	pageBURL := "https://" + unique + ".invalid/b"

	ctxA := openLegacyRefreshContext(t, handler, sourceA, pageAURL)
	t.Cleanup(func() {
		_, _ = admin.Exec(`DELETE FROM WebObjectsIndex WHERE index_id IN
			(SELECT index_id FROM SearchIndex WHERE page_url = ANY($1::text[]))`,
			pq.Array([]string{pageAURL, pageBURL}))
		_, _ = admin.Exec(`DELETE FROM SourceSearchIndex WHERE source_id = ANY($1::bigint[])`,
			pqUint64Array([]uint64{sourceA, sourceB}))
		_, _ = admin.Exec(`DELETE FROM SearchIndex WHERE page_url = ANY($1::text[])`,
			pq.Array([]string{pageAURL, pageBURL}))
	})

	sharedBody := "vacuum shared body " + unique
	if _, err := indexPageContext(context.Background(), ctxA, pageAURL,
		legacyRefreshPageInfo(ctxA, sourceA, sharedBody)); err != nil {
		t.Fatalf("index A: %v", err)
	}
	pageBInfo := legacyRefreshPageInfo(ctxA, sourceB, sharedBody)
	// Index page B directly so it belongs to source B only.
	pageBInfo.Config = &ctxA.config
	tx, err := handler.Begin()
	if err != nil {
		t.Fatalf("begin page B: %v", err)
	}
	indexB, err := insertOrUpdateSearchIndex(tx, pageBURL, pageBInfo)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("index B search index: %v", err)
	}
	if _, err = tx.Exec(`INSERT INTO SourceSearchIndex (source_id, index_id)
		VALUES ($1, $2) ON CONFLICT DO NOTHING`, sourceB, indexB); err != nil {
		_ = tx.Rollback()
		t.Fatalf("link B: %v", err)
	}
	if _, _, _, err = insertOrUpdateWebObjects(tx, indexB, pageBInfo); err != nil {
		_ = tx.Rollback()
		t.Fatalf("index B object: %v", err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatalf("commit page B: %v", err)
	}

	pageAID := fencedRefreshPageIndex(t, handler, pageAURL)
	sharedHash := fencedRefreshPageObjects(t, handler, pageAID)[0]
	var sharedID uint64
	if err := handler.QueryRow(`SELECT object_id FROM WebObjects WHERE object_hash = $1`,
		sharedHash).Scan(&sharedID); err != nil {
		t.Fatalf("read shared object: %v", err)
	}

	deadline := time.Now().Add(30 * time.Second)
	refreshDone := make(chan error, 1)
	vacuumDone := make(chan error, 1)
	go func() {
		_, err := indexPageContext(context.Background(), ctxA, pageAURL,
			legacyRefreshPageInfo(ctxA, sourceA, "vacuum changed body "+unique))
		refreshDone <- err
	}()
	go func() {
		vacuumDone <- cdb.VacuumSource(&handler, sourceB)
	}()

	for _, ch := range []chan error{refreshDone, vacuumDone} {
		select {
		case err := <-ch:
			if err != nil {
				t.Fatalf("concurrent operation failed: %v", err)
			}
		case <-time.After(time.Until(deadline)):
			t.Fatal("concurrent refresh/vacuum did not finish within the budget")
		}
	}

	var survivingLinks int
	if err := handler.QueryRow(`SELECT COUNT(*) FROM WebObjectsIndex WHERE object_id = $1`,
		sharedID).Scan(&survivingLinks); err != nil {
		t.Fatalf("count surviving links: %v", err)
	}
	objectRows := fencedRefreshCountObjects(t, handler, []uint64{sharedID})
	if survivingLinks > 0 && objectRows != 1 {
		t.Fatalf("object deleted while %d links remain", survivingLinks)
	}
	if survivingLinks == 0 && objectRows != 0 {
		t.Fatalf("orphaned object survived: links=%d object_rows=%d", survivingLinks, objectRows)
	}
}
