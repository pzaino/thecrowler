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

// Portable coverage for the parts of the fenced refresh path that the original
// WP-05B3C3B2F1 package did not exercise: the bounded multi-batch loop, a
// failure that occurs after an earlier batch already committed its deletes
// inside the same transaction, and attribute cleanup for hash-shared objects.
//
// These run on SQLite because the link/orphan logic is backend-neutral. The
// PostgreSQL suite covers the lock and transaction behaviour.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

func seedFencedRefreshAttribute(t *testing.T, handler cdb.Handler,
	objectID uint64, key, value string) {
	t.Helper()
	if _, err := handler.Exec(`
		INSERT INTO ObjectAttributes
			(object_id, object_type, attribute_key, attribute_value,
			 normalized_value, value_hash)
		VALUES (?, 'webobject', ?, ?, ?, ?)`,
		objectID, key, value, value, strings.Repeat("0", 64)); err != nil {
		t.Fatalf("insert attribute: %v", err)
	}
}

func countFencedRefreshAttributes(t *testing.T, handler cdb.Handler, objectID uint64) int {
	t.Helper()
	return countFencedRefreshRows(t, handler,
		`SELECT COUNT(*) FROM ObjectAttributes WHERE object_type = 'webobject' AND object_id = ?`,
		objectID)
}

func countFencedRefreshObjectsIn(t *testing.T, handler cdb.Handler, objectIDs []uint64) int {
	t.Helper()
	if len(objectIDs) == 0 {
		return 0
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(objectIDs)), ",")
	args := make([]any, len(objectIDs))
	for i, id := range objectIDs {
		args[i] = id
	}
	return countFencedRefreshRows(t, handler,
		`SELECT COUNT(*) FROM WebObjects WHERE object_id IN (`+placeholders+`)`, args...)
}

// TestFencedRefreshKeepsSharedObjectAttributesSQLite proves that replacing a
// page's object links removes attributes only for objects that became
// unreferenced. A hash-shared object that another page still links must keep
// both its row and its attributes.
func TestFencedRefreshKeepsSharedObjectAttributesSQLite(t *testing.T) {
	handler := openB2RSQLiteHandler(t)
	dbms := handler.DBMS()
	sourceID := seedFencedRefreshSource(t, handler, "uid-attr", "https://attr.example/", "attr")
	pageID := seedFencedRefreshPage(t, handler, "https://attr.example/page")
	otherPageID := seedFencedRefreshPage(t, handler, "https://attr-other.example/")
	linkFencedRefreshSourcePage(t, handler, sourceID, pageID)

	orphanID := seedFencedRefreshObject(t, handler, strings.Repeat("1", 64), "orphan")
	sharedID := seedFencedRefreshObject(t, handler, strings.Repeat("2", 64), "shared")
	linkFencedRefreshObject(t, handler, pageID, orphanID)
	linkFencedRefreshObject(t, handler, pageID, sharedID)
	linkFencedRefreshObject(t, handler, otherPageID, sharedID)
	seedFencedRefreshAttribute(t, handler, orphanID, "price", "10")
	seedFencedRefreshAttribute(t, handler, sharedID, "price", "20")

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

	if got := countFencedRefreshAttributes(t, handler, orphanID); got != 0 {
		t.Fatalf("orphaned object kept %d attributes", got)
	}
	if got := countFencedRefreshAttributes(t, handler, sharedID); got != 1 {
		t.Fatalf("shared object lost its attributes: %d", got)
	}
	if got := countFencedRefreshRows(t, handler,
		`SELECT COUNT(*) FROM WebObjectsIndex WHERE index_id = ? AND object_id = ?`,
		otherPageID, sharedID); got != 1 {
		t.Fatalf("other page lost its shared link: %d", got)
	}
}

// TestFencedRefreshBatchesLargeOldGenerationSQLite proves the bounded loop
// terminates and cleans a generation larger than one batch while preserving a
// hash-shared object that another page still references.
func TestFencedRefreshBatchesLargeOldGenerationSQLite(t *testing.T) {
	handler := openB2RSQLiteHandler(t)
	dbms := handler.DBMS()
	sourceID := seedFencedRefreshSource(t, handler, "uid-batch", "https://batch.example/", "batch")
	pageID := seedFencedRefreshPage(t, handler, "https://batch.example/page")
	otherPageID := seedFencedRefreshPage(t, handler, "https://batch-other.example/")
	linkFencedRefreshSourcePage(t, handler, sourceID, pageID)

	const oldCount = fencedRefreshObjectBatch*2 + 37
	oldIDs := make([]uint64, 0, oldCount)
	for i := 0; i < oldCount; i++ {
		objectID := seedFencedRefreshObject(t, handler,
			fmt.Sprintf("%064x", i+1), fmt.Sprintf("old-%d", i))
		linkFencedRefreshObject(t, handler, pageID, objectID)
		oldIDs = append(oldIDs, objectID)
	}
	// A shared object whose ID sorts after the first batch: it must survive
	// even though it is inside the range the loop walks.
	sharedID := seedFencedRefreshObject(t, handler, strings.Repeat("f", 64), "shared")
	linkFencedRefreshObject(t, handler, pageID, sharedID)
	linkFencedRefreshObject(t, handler, otherPageID, sharedID)
	keepID := seedFencedRefreshObject(t, handler, strings.Repeat("9", 64), "replacement")
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
		`SELECT COUNT(*) FROM WebObjectsIndex WHERE index_id = ?`, pageID); got != 1 {
		t.Fatalf("page kept %d links, want only the replacement", got)
	}
	if got := countFencedRefreshRows(t, handler,
		`SELECT COUNT(*) FROM WebObjectsIndex WHERE index_id = ? AND object_id = ?`,
		pageID, keepID); got != 1 {
		t.Fatalf("replacement link missing: %d", got)
	}
	if got := countFencedRefreshObjectsIn(t, handler, oldIDs); got != 0 {
		t.Fatalf("%d old objects survived the batched cleanup", got)
	}
	if got := countFencedRefreshRows(t, handler,
		`SELECT COUNT(*) FROM WebObjects WHERE object_id = ?`, sharedID); got != 1 {
		t.Fatalf("batched cleanup removed a shared object: %d", got)
	}
	if got := countFencedRefreshRows(t, handler,
		`SELECT COUNT(*) FROM WebObjectsIndex WHERE index_id = ? AND object_id = ?`,
		otherPageID, sharedID); got != 1 {
		t.Fatalf("batched cleanup removed another page's link: %d", got)
	}
}

// TestFencedRefreshLaterBatchFailureRollsBackSQLite fails the delete inside the
// second batch. The first batch has already removed links and objects at that
// point, so this proves the whole replacement, not only the last statement, is
// rolled back.
func TestFencedRefreshLaterBatchFailureRollsBackSQLite(t *testing.T) {
	handler := openB2RSQLiteHandler(t)
	dbms := handler.DBMS()
	sourceID := seedFencedRefreshSource(t, handler, "uid-latefail", "https://latefail.example/", "latefail")
	pageID := seedFencedRefreshPage(t, handler, "https://latefail.example/page")
	linkFencedRefreshSourcePage(t, handler, sourceID, pageID)

	const oldCount = fencedRefreshObjectBatch*2 + 11
	oldIDs := make([]uint64, 0, oldCount)
	for i := 0; i < oldCount; i++ {
		objectID := seedFencedRefreshObject(t, handler,
			fmt.Sprintf("%064x", i+500), fmt.Sprintf("old-%d", i))
		linkFencedRefreshObject(t, handler, pageID, objectID)
		oldIDs = append(oldIDs, objectID)
	}
	keepID := seedFencedRefreshObject(t, handler, strings.Repeat("7", 64), "replacement")
	linkFencedRefreshObject(t, handler, pageID, keepID)

	// Fail on an object in the second batch. Objects are walked in ascending
	// object_id order, so the first batch must already have completed before
	// the injected error fires.
	failID := oldIDs[fencedRefreshObjectBatch+60]
	trigger := fmt.Sprintf(`
		CREATE TRIGGER b2rsqlite_fail_late_batch
		BEFORE DELETE ON WebObjects
		WHEN OLD.object_id = %d
		BEGIN
			SELECT RAISE(ABORT, 'b2rsqlite injected late batch failure');
		END`, failID)
	if _, err := handler.Exec(trigger); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = handler.Exec(`DROP TRIGGER IF EXISTS b2rsqlite_fail_late_batch`)
	})

	tx, err := handler.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	replaceErr := replaceFencedPageObjectsTx(context.Background(), tx, dbms, pageID, keepID)
	if replaceErr == nil {
		_ = tx.Rollback()
		t.Fatal("injected late batch failure did not stop the replacement")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	if got := countFencedRefreshRows(t, handler,
		`SELECT COUNT(*) FROM WebObjectsIndex WHERE index_id = ?`, pageID); got != oldCount+1 {
		t.Fatalf("rollback left %d page links, want %d", got, oldCount+1)
	}
	if got := countFencedRefreshObjectsIn(t, handler, oldIDs); got != oldCount {
		t.Fatalf("rollback restored %d of %d old objects", got, oldCount)
	}
}
