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

// Refresh-content support for both claim modes (WP-05B3C3B2F1 / B3 Decision 2,
// unified by WP-REL230-RC2 / RC2-B).
//
// The historical legacy path deleted a page's WebObjects in an independent
// transaction before the replacement page transaction. That was unsafe for two
// reasons: a failure or stale fence between the delete and the replacement
// would leave the old content deleted, and the delete removed globally
// hash-deduplicated object rows that another page may still reference. The
// fenced path replaced it with the algorithm below; RC2-B moved the legacy path
// onto the same implementation, so both modes now behave identically apart from
// the fence itself.
//
// The whole replacement runs inside the caller's already validated indexing
// transaction:
//
//  1. ValidateTx (fenced only) serializes the attempt with claim allocation,
//     heartbeat, terminal reporting and supersession.
//  2. The page row is locked, so a concurrent source cannot link the same page
//     between the ownership check and the replacement.
//  3. A page linked to more than one source is refused: WebObjectsIndex has no
//     source dimension, so a single-source refresh cannot authorize changing
//     the associations another source sees.
//  4. Only page-scoped WebObjectsIndex rows are removed.
//  5. A WebObjects row is deleted only when no WebObjectsIndex row (for any
//     page) still references it. On PostgreSQL the pre-existing
//     trg_after_delete_webobjectsindex trigger usually performs that orphan
//     delete first; on SQLite this code is the only deleter.
//
// The historical "fenced" prefix on the helper names is retained deliberately:
// it keeps this release patch focused on behaviour, and every helper is used by
// both paths now. See docs/crowler-agents/reviews/RELEASE_230_RC2_IMPLEMENTATION.md.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	cdb "github.com/pzaino/thecrowler/pkg/database"
)

var (
	// errFencedRefreshCrossSourcePage is returned when the page being refreshed
	// is also linked to another source. A source-scoped refresh cannot authorize
	// a destructive change to a page another source depends on.
	errFencedRefreshCrossSourcePage = errors.New(
		"content refresh refused: page is shared with another source")

	errFencedRefreshTransactionRequired = errors.New(
		"content refresh requires an active transaction")
)

// fencedRefreshObjectBatch bounds the number of page-scoped object links
// handled by one iteration. The loop continues until the page has no old
// links, so a page with a very large stale generation is still cleaned up
// without holding an unbounded slice in memory.
const fencedRefreshObjectBatch = 128

// fencedRefreshPlaceholder returns the parameter marker for the active backend.
// Fenced execution is supported on PostgreSQL and SQLite; MySQL remains
// legacy-only.
func fencedRefreshPlaceholder(dbms string, position int) string {
	if dbms == cdb.DBSQLiteStr {
		return "?"
	}
	return fmt.Sprintf("$%d", position)
}

// acquireFencedRefreshSerializationTx serializes fenced refresh transactions.
//
// A refresh holds the new object row (from the hash upsert) and then locks the
// page's old object rows. Two concurrent refreshes whose new content hashes
// intersect the other's old object set could otherwise acquire those rows in
// opposite orders and deadlock. A transaction-scoped advisory lock makes the
// order deterministic without blocking ordinary (non-refresh) indexing, which
// never takes it. SQLite already serializes writers, so no extra lock is
// needed there.
func acquireFencedRefreshSerializationTx(ctx context.Context, tx *sql.Tx, dbms string) error {
	if tx == nil {
		return errFencedRefreshTransactionRequired
	}
	if dbms != cdb.DBPostgresStr {
		return nil
	}
	if _, err := tx.ExecContext(ctx,
		`SELECT pg_advisory_xact_lock(hashtext('crowler:fenced-content-refresh'))`); err != nil {
		return fmt.Errorf("acquiring fenced refresh serialization lock: %w", err)
	}
	return nil
}

// lockFencedRefreshPageTx locks the existing page row (if any) for the URL and
// returns its index ID. The strong row lock blocks a concurrent
// SourceSearchIndex insert for the same page (its foreign-key check needs a
// weaker lock that conflicts with FOR UPDATE), so the cross-source decision
// below cannot race a new source linking the page.
func lockFencedRefreshPageTx(ctx context.Context, tx *sql.Tx, dbms, pageURL string) (uint64, bool, error) {
	if tx == nil {
		return 0, false, errFencedRefreshTransactionRequired
	}
	query := `SELECT index_id FROM SearchIndex WHERE page_url = ` +
		fencedRefreshPlaceholder(dbms, 1)
	if dbms == cdb.DBPostgresStr {
		query += ` FOR UPDATE`
	}
	var indexID uint64
	err := tx.QueryRowContext(ctx, query, pageURL).Scan(&indexID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("locking fenced refresh page: %w", err)
	}
	return indexID, true, nil
}

// ensureFencedRefreshSingleSourceTx refuses a fenced refresh when the page is
// linked to any source other than the crawling source.
func ensureFencedRefreshSingleSourceTx(ctx context.Context, tx *sql.Tx, dbms string,
	indexID, sourceID uint64) error {
	if tx == nil {
		return errFencedRefreshTransactionRequired
	}
	if sourceID == 0 {
		return errors.New("fenced refresh requires a source identity")
	}
	query := fmt.Sprintf(
		`SELECT COUNT(*) FROM SourceSearchIndex WHERE index_id = %s AND source_id <> %s`,
		fencedRefreshPlaceholder(dbms, 1), fencedRefreshPlaceholder(dbms, 2))
	var otherSources int64
	if err := tx.QueryRowContext(ctx, query, indexID, sourceID).Scan(&otherSources); err != nil {
		return fmt.Errorf("checking fenced refresh page authority: %w", err)
	}
	if otherSources > 0 {
		return fmt.Errorf("%w (index_id=%d, other_sources=%d)",
			errFencedRefreshCrossSourcePage, indexID, otherSources)
	}
	return nil
}

// replaceFencedPageObjectsTx replaces the page's object link set with
// keepObjectID and removes only objects that become unreferenced by any page.
func replaceFencedPageObjectsTx(ctx context.Context, tx *sql.Tx, dbms string,
	indexID, keepObjectID uint64) error {
	if tx == nil {
		return errFencedRefreshTransactionRequired
	}
	if indexID == 0 {
		return errors.New("fenced refresh requires an index identity")
	}
	for {
		objectIDs, err := fencedRefreshOldObjectIDsTx(ctx, tx, dbms, indexID, keepObjectID)
		if err != nil {
			return err
		}
		if len(objectIDs) == 0 {
			return nil
		}
		// Drop this page's links first, so the orphan check below sees only
		// references owned by other pages.
		for _, objectID := range objectIDs {
			query := fmt.Sprintf(
				`DELETE FROM WebObjectsIndex WHERE index_id = %s AND object_id = %s`,
				fencedRefreshPlaceholder(dbms, 1), fencedRefreshPlaceholder(dbms, 2))
			if _, err := tx.ExecContext(ctx, query, indexID, objectID); err != nil {
				return fmt.Errorf("removing fenced refresh page link %d: %w", objectID, err)
			}
		}
		for _, objectID := range objectIDs {
			if err := deleteFencedRefreshOrphanTx(ctx, tx, dbms, objectID); err != nil {
				return err
			}
		}
	}
}

// fencedRefreshOldObjectIDsTx returns the next ascending batch of page-scoped
// object IDs that are not the replacement object. Ascending order gives every
// fenced refresh the same lock order for shared objects.
func fencedRefreshOldObjectIDsTx(ctx context.Context, tx *sql.Tx, dbms string,
	indexID, keepObjectID uint64) ([]uint64, error) {
	query := fmt.Sprintf(
		`SELECT object_id FROM WebObjectsIndex
		 WHERE index_id = %s AND object_id <> %s
		 ORDER BY object_id
		 LIMIT %d`,
		fencedRefreshPlaceholder(dbms, 1),
		fencedRefreshPlaceholder(dbms, 2),
		fencedRefreshObjectBatch)
	rows, err := tx.QueryContext(ctx, query, indexID, keepObjectID)
	if err != nil {
		return nil, fmt.Errorf("listing fenced refresh page objects: %w", err)
	}
	defer rows.Close()

	objectIDs := make([]uint64, 0, fencedRefreshObjectBatch)
	for rows.Next() {
		var objectID uint64
		if err := rows.Scan(&objectID); err != nil {
			return nil, fmt.Errorf("scanning fenced refresh page object: %w", err)
		}
		objectIDs = append(objectIDs, objectID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating fenced refresh page objects: %w", err)
	}
	return objectIDs, nil
}

// deleteFencedRefreshOrphanTx deletes one WebObjects row only when no page
// references it. On PostgreSQL the row is locked first; a concurrent
// WebObjectsIndex insert needs a foreign-key lock that conflicts with the row
// lock, so the orphan decision cannot race a new reference. Deleting the row
// also fires the statement-level cleanup trigger, which is correct precisely
// because the object is no longer referenced anywhere.
func deleteFencedRefreshOrphanTx(ctx context.Context, tx *sql.Tx, dbms string, objectID uint64) error {
	lockQuery := `SELECT object_id FROM WebObjects WHERE object_id = ` +
		fencedRefreshPlaceholder(dbms, 1)
	if dbms == cdb.DBPostgresStr {
		lockQuery += ` FOR UPDATE`
	}
	var lockedID uint64
	err := tx.QueryRowContext(ctx, lockQuery, objectID).Scan(&lockedID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("locking fenced refresh object %d: %w", objectID, err)
	}

	countQuery := `SELECT COUNT(*) FROM WebObjectsIndex WHERE object_id = ` +
		fencedRefreshPlaceholder(dbms, 1)
	var references int64
	if err := tx.QueryRowContext(ctx, countQuery, objectID).Scan(&references); err != nil {
		return fmt.Errorf("counting fenced refresh object references %d: %w", objectID, err)
	}
	if references != 0 {
		return nil
	}

	deleteQuery := `DELETE FROM WebObjects WHERE object_id = ` +
		fencedRefreshPlaceholder(dbms, 1)
	if _, err := tx.ExecContext(ctx, deleteQuery, objectID); err != nil {
		return fmt.Errorf("deleting fenced refresh orphan object %d: %w", objectID, err)
	}
	return nil
}
