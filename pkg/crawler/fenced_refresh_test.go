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

import (
	"context"
	"errors"
	"strings"
	"testing"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func seedFencedRefreshSource(t *testing.T, handler cdb.Handler, uid, url, name string) uint64 {
	t.Helper()
	result, err := handler.Exec(`
		INSERT INTO Sources
			(source_uid, url, name, status, restricted, disabled, usr_id,
			 priority, sub_priority, config)
		VALUES (?, ?, ?, 'new', 0, 0, 0, 'normal', 0, '{}')`,
		uid, url, name)
	if err != nil {
		t.Fatalf("insert source: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("source id: %v", err)
	}
	return uint64(id)
}

func seedFencedRefreshPage(t *testing.T, handler cdb.Handler, pageURL string) uint64 {
	t.Helper()
	result, err := handler.Exec(`
		INSERT INTO SearchIndex (page_url, title, summary)
		VALUES (?, 'title', 'summary')`, pageURL)
	if err != nil {
		t.Fatalf("insert page: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("page id: %v", err)
	}
	return uint64(id)
}

func seedFencedRefreshObject(t *testing.T, handler cdb.Handler, hash, content string) uint64 {
	t.Helper()
	result, err := handler.Exec(`
		INSERT INTO WebObjects
			(object_hash, object_content, object_html, details)
		VALUES (?, ?, '<p>content</p>', '{}')`, hash, content)
	if err != nil {
		t.Fatalf("insert object: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("object id: %v", err)
	}
	return uint64(id)
}

func linkFencedRefreshSourcePage(t *testing.T, handler cdb.Handler, sourceID, indexID uint64) {
	t.Helper()
	if _, err := handler.Exec(`
		INSERT INTO SourceSearchIndex (source_id, index_id) VALUES (?, ?)`,
		sourceID, indexID); err != nil {
		t.Fatalf("link source page: %v", err)
	}
}

func linkFencedRefreshObject(t *testing.T, handler cdb.Handler, indexID, objectID uint64) {
	t.Helper()
	if _, err := handler.Exec(`
		INSERT INTO WebObjectsIndex (index_id, object_id) VALUES (?, ?)`,
		indexID, objectID); err != nil {
		t.Fatalf("link object: %v", err)
	}
}

func countFencedRefreshRows(t *testing.T, handler cdb.Handler, query string, args ...any) int {
	t.Helper()
	var count int
	if err := handler.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return count
}

func TestFencedRefreshReplacesPageLinksSQLite(t *testing.T) {
	handler := openB2RSQLiteHandler(t)
	dbms := handler.DBMS()
	sourceID := seedFencedRefreshSource(t, handler, "uid-refresh", "https://refresh.example/", "refresh")
	pageID := seedFencedRefreshPage(t, handler, "https://refresh.example/page")
	otherPageID := seedFencedRefreshPage(t, handler, "https://other.example/dup")
	linkFencedRefreshSourcePage(t, handler, sourceID, pageID)
	linkFencedRefreshSourcePage(t, handler, sourceID, otherPageID)

	orphanID := seedFencedRefreshObject(t, handler, strings.Repeat("a", 64), "orphan")
	sharedID := seedFencedRefreshObject(t, handler, strings.Repeat("b", 64), "shared")
	linkFencedRefreshObject(t, handler, pageID, orphanID)
	linkFencedRefreshObject(t, handler, pageID, sharedID)
	linkFencedRefreshObject(t, handler, otherPageID, sharedID)

	tx, err := handler.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := replaceFencedPageObjectsTx(context.Background(), tx, dbms, pageID, 0); err != nil {
		_ = tx.Rollback()
		t.Fatalf("replace: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if got := countFencedRefreshRows(t, handler,
		`SELECT COUNT(*) FROM WebObjectsIndex WHERE index_id = ?`, pageID); got != 0 {
		t.Fatalf("refreshed page still has %d object links", got)
	}
	if got := countFencedRefreshRows(t, handler,
		`SELECT COUNT(*) FROM WebObjects WHERE object_id = ?`, orphanID); got != 0 {
		t.Fatalf("orphan object was not removed: %d rows", got)
	}
	if got := countFencedRefreshRows(t, handler,
		`SELECT COUNT(*) FROM WebObjects WHERE object_id = ?`, sharedID); got != 1 {
		t.Fatalf("shared object was removed: %d rows", got)
	}
	if got := countFencedRefreshRows(t, handler,
		`SELECT COUNT(*) FROM WebObjectsIndex WHERE index_id = ? AND object_id = ?`,
		otherPageID, sharedID); got != 1 {
		t.Fatalf("other page lost its shared object link: %d", got)
	}
}

func TestFencedRefreshKeepsReplacementObjectSQLite(t *testing.T) {
	handler := openB2RSQLiteHandler(t)
	dbms := handler.DBMS()
	sourceID := seedFencedRefreshSource(t, handler, "uid-keep", "https://keep.example/", "keep")
	pageID := seedFencedRefreshPage(t, handler, "https://keep.example/page")
	linkFencedRefreshSourcePage(t, handler, sourceID, pageID)

	oldID := seedFencedRefreshObject(t, handler, strings.Repeat("c", 64), "old")
	keepID := seedFencedRefreshObject(t, handler, strings.Repeat("d", 64), "new")
	linkFencedRefreshObject(t, handler, pageID, oldID)
	linkFencedRefreshObject(t, handler, pageID, keepID)

	tx, err := handler.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := replaceFencedPageObjectsTx(context.Background(), tx, dbms, pageID, keepID); err != nil {
		_ = tx.Rollback()
		t.Fatalf("replace: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if got := countFencedRefreshRows(t, handler,
		`SELECT COUNT(*) FROM WebObjects WHERE object_id = ?`, oldID); got != 0 {
		t.Fatalf("old object survived: %d", got)
	}
	if got := countFencedRefreshRows(t, handler,
		`SELECT COUNT(*) FROM WebObjectsIndex WHERE index_id = ? AND object_id = ?`,
		pageID, keepID); got != 1 {
		t.Fatalf("replacement link missing: %d", got)
	}
}

func TestFencedRefreshRollbackRestoresOldContentSQLite(t *testing.T) {
	handler := openB2RSQLiteHandler(t)
	dbms := handler.DBMS()
	sourceID := seedFencedRefreshSource(t, handler, "uid-rollback", "https://rollback.example/", "rollback")
	pageID := seedFencedRefreshPage(t, handler, "https://rollback.example/page")
	linkFencedRefreshSourcePage(t, handler, sourceID, pageID)
	objectID := seedFencedRefreshObject(t, handler, strings.Repeat("e", 64), "old")
	linkFencedRefreshObject(t, handler, pageID, objectID)

	tx, err := handler.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := replaceFencedPageObjectsTx(context.Background(), tx, dbms, pageID, 0); err != nil {
		_ = tx.Rollback()
		t.Fatalf("replace: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	if got := countFencedRefreshRows(t, handler,
		`SELECT COUNT(*) FROM WebObjectsIndex WHERE index_id = ? AND object_id = ?`,
		pageID, objectID); got != 1 {
		t.Fatalf("rollback did not restore the page link: %d", got)
	}
	if got := countFencedRefreshRows(t, handler,
		`SELECT COUNT(*) FROM WebObjects WHERE object_id = ?`, objectID); got != 1 {
		t.Fatalf("rollback did not restore the object: %d", got)
	}
}

func TestFencedRefreshRefusesCrossSourcePageSQLite(t *testing.T) {
	handler := openB2RSQLiteHandler(t)
	dbms := handler.DBMS()
	owner := seedFencedRefreshSource(t, handler, "uid-owner", "https://owner.example/", "owner")
	other := seedFencedRefreshSource(t, handler, "uid-other", "https://other.example/", "other")
	pageID := seedFencedRefreshPage(t, handler, "https://shared.example/page")
	linkFencedRefreshSourcePage(t, handler, owner, pageID)
	linkFencedRefreshSourcePage(t, handler, other, pageID)

	tx, err := handler.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	err = ensureFencedRefreshSingleSourceTx(context.Background(), tx, dbms, pageID, owner)
	if !errors.Is(err, errFencedRefreshCrossSourcePage) {
		t.Fatalf("cross-source page error = %v", err)
	}

	if _, err := tx.Exec(`DELETE FROM SourceSearchIndex WHERE source_id = ? AND index_id = ?`,
		other, pageID); err != nil {
		t.Fatalf("remove second link: %v", err)
	}
	if err := ensureFencedRefreshSingleSourceTx(context.Background(), tx, dbms, pageID, owner); err != nil {
		t.Fatalf("single-source page rejected: %v", err)
	}
}

func TestFencedRefreshLocksOnlyExistingPageSQLite(t *testing.T) {
	handler := openB2RSQLiteHandler(t)
	dbms := handler.DBMS()
	pageID := seedFencedRefreshPage(t, handler, "https://locked.example/page")

	tx, err := handler.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	indexID, exists, err := lockFencedRefreshPageTx(context.Background(), tx, dbms,
		"https://missing.example/page")
	if err != nil || exists || indexID != 0 {
		t.Fatalf("missing page = id=%d exists=%v err=%v", indexID, exists, err)
	}
	indexID, exists, err = lockFencedRefreshPageTx(context.Background(), tx, dbms,
		"https://locked.example/page")
	if err != nil || !exists || indexID != pageID {
		t.Fatalf("existing page = id=%d exists=%v err=%v", indexID, exists, err)
	}
}

func TestFencedIndexingFailureClassification(t *testing.T) {
	if fencedIndexingFailure(nil, errIndexingFailure) {
		t.Fatal("legacy crawl must keep non-critical indexing errors non-fatal")
	}
	fake := &fakeSourceCrawlCoordinator{sourceID: 42, attemptID: "attempt"}
	if !fencedIndexingFailure(fake, errors.Join(errIndexingFailure, errors.New("detail"))) {
		t.Fatal("fenced indexing failure was not classified as fatal")
	}
	if fencedIndexingFailure(fake, errors.New("unrelated error")) {
		t.Fatal("non-indexing error was classified as an indexing failure")
	}
}
