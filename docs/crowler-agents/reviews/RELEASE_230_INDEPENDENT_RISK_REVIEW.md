# Release 2.3.0 independent risk review

Work package: `WP-REL230-RC1`
Reviewed commit: `9e719acce04d3606c46821e33b4f8dc23baa6b36`
Diff under review since the completed B2F1 baseline (`be5572de`): 12 files,
+1457/−237 lines, all belonging to B3 Decision 2 (atomic fenced
`refresh_content`) plus documentation and the CI gate.
Reviewer: this agent, self-review. **No separate read-only reviewer was
available in this environment**, so this is explicitly a self-review. It is not
independent evidence in the sense the work package asks for, and that is
recorded as a limitation.

## 1. Scope and method

Read in full:

- `pkg/crawler/fenced_refresh.go` (new, 257 lines)
- `pkg/crawler/crawler.go` — `indexPageContext`, `indexPage`,
  `insertOrUpdateSearchIndex`, `insertOrUpdateWebObjects`, `deleteWebObjects`,
  worker error drain, post-commit phases, `upsertPostgresKeywords`
- `pkg/crawler/source_crawl_handoff.go` — `fencedIndexingFailure`,
  `validateIndexingFence`, `checkCrawlFence`
- `pkg/database/source_crawl_fence.go`, `source_crawl_result.go`,
  `source_crawl_attempt.go`
- `pkg/database/postgresql-setup.pgsql` — `WebObjects`, `WebObjectsIndex`,
  `SearchIndex`, `SourceSearchIndex`, `ObjectAttributes`, the cleanup triggers
  and `handle_shared_entity_deletion()`
- `pkg/database/sqlite-setup.sqlite3` — equivalent objects and triggers
- `pkg/crawler/fenced_refresh_test.go`,
  `fenced_refresh_postgres_integration_test.go`, and the new tests added by
  this package
- `cmd/thecrowler/main.go` — `isNullSource`, `handleNullSourceDeletion`,
  `drainFencedSources`

Method: trace each write, determine which transaction and lock protects it,
then attempt to falsify the documented invariant with an orchestrated
concurrent test. Everything below is either proven by a test in this package or
explicitly labelled as reasoning.

## 2. Findings, ranked

### R-01 — FAILED trigger race: a pre-existing PostgreSQL orphan trigger can silently delete another page's committed link (P0 for `refresh_content`, not reachable at default configuration)

**Where:** `pkg/database/postgresql-setup.pgsql`,
`handle_shared_entity_deletion()` and `trg_after_delete_webobjectsindex`.

**What it does:**

```sql
IF (SELECT COUNT(*) FROM WebObjectsIndex WHERE object_id = OLD.object_id) = 0 THEN
    DELETE FROM WebObjects WHERE object_id = OLD.object_id;
END IF;
```

Two separate statements, `READ COMMITTED`, no parent-row lock.

**Falsification:** `TestPostgresSharedObjectTriggerRaceCharacterization`
passes, which means it reproduces the defect:

1. object `O` is linked to page X;
2. session B inserts `WebObjectsIndex(Y, O)` and does not commit, so it holds a
   `KEY SHARE` lock on `O`;
3. session A deletes `WebObjectsIndex(X, O)`; the trigger counts references,
   sees zero because B is uncommitted, and issues `DELETE FROM WebObjects`;
4. that delete blocks on B's row lock;
5. B commits;
6. A's delete proceeds and removes `O`;
7. `WebObjectsIndex.object_id` is `ON DELETE CASCADE`, so `WebObjectsIndex(Y,O)`
   disappears with no error.

Final state observed by the test: `object_rows=0`, `page_y_links=0`. Page Y's
transaction reported success and its link is gone.

**Does the fenced code cause it?** No. It is pre-existing schema 1.17
behaviour. But the fenced path **inherits** it: `replaceFencedPageObjectsTx`
deletes `WebObjectsIndex` rows, and each delete fires the trigger before the
explicit `deleteFencedRefreshOrphanTx` can take its safer `FOR UPDATE` lock and
re-count. The explicit helper is safe in isolation
(`TestPostgresFencedRefreshOrphanLockWaitsForInFlightReference` proves it waits
for an in-flight reference and then preserves the object); the trigger simply
runs first.

**Reachability:**

- Requires `refresh_content: true` (default `false`) **or** another path that
  deletes `WebObjectsIndex` rows (`VacuumSource`, `SearchIndex` deletion), plus
  two pages whose content hashes collide, plus a concurrent link insertion in
  the race window.
- Not reachable at default configuration, because `refresh_content` is `false`
  and the default claim mode is `legacy` with no link replacement.
- Reachable in any deployment that enables `refresh_content`, in either claim
  mode.

**Impact:** silent loss of a derived `WebObjectsIndex` link and its
`WebObjects` row (and, via `trg_cleanup_webobject`, the object's attributes).
`SearchIndex` and `Sources` rows survive, and a re-crawl restores the artifact.
No source rows, credentials or user-owned configuration are lost.

**Why this is not fixed here:** the only sound fixes are schema changes:

- take `SELECT ... FOR UPDATE` on the parent `WebObjects` row inside
  `handle_shared_entity_deletion()` before counting, so an uncommitted FK
  insertion blocks the count; or
- drop the trigger and let lock-guarded application code own orphan deletion.

Both change schema 1.17 behaviour for every path, not just fenced refresh. The
work package explicitly forbids new DDL under this approval. The defect is
therefore recorded as a blocker with a proposed decision, not patched.

**Recommended fix (needs approval):** in the same statement as the count, lock
the parent:

```sql
PERFORM 1 FROM WebObjects WHERE object_id = OLD.object_id FOR UPDATE;
IF NOT FOUND THEN RETURN OLD; END IF;
IF (SELECT COUNT(*) FROM WebObjectsIndex WHERE object_id = OLD.object_id) = 0 THEN
    DELETE FROM WebObjects WHERE object_id = OLD.object_id;
END IF;
```

Verification would be the existing characterization test, updated to assert the
safe outcome, plus the release-gate SQLite batching test to confirm no
regression in the portable path.

### R-02 — Legacy `deleteWebObjects` destroys a hash-shared object for every page (P1 for `refresh_content`, pre-existing, unchanged)

**Where:** `pkg/crawler/crawler.go`, `deleteWebObjects`.

```sql
DELETE FROM WebObjects
WHERE object_id IN (SELECT object_id FROM WebObjectsIndex WHERE index_id = $1)
```

**Falsification:** `TestPostgresLegacyRefreshSharedObjectHazardReproducer`
passes. Deleting page X's refresh set removes the shared object, page Y's link,
and the object's attributes, with no error and no race required.

**Classification:** pre-existing, deliberately unchanged by Decision 2 ("legacy
behaviour must not silently change"). It is reachable only with
`refresh_content: true`.

**Release consequence:** the legacy path is the *default* path for any
deployment that turns on `refresh_content`, so enabling that option today gets
the strictly worse behaviour. The release note must say so, and the fix should
be a separate narrowly scoped decision (delete page-scoped links only, plus an
orphan check, exactly as the fenced path does).

### R-03 — The fenced advisory lock serialises all fenced refreshes globally (P2, documented, not a defect)

**Where:** `acquireFencedRefreshSerializationTx`, key
`hashtext('crowler:fenced-content-refresh')`.

A single transaction-scoped advisory lock is taken after the source row lock
and held for the rest of the main transaction. Consequences:

- two fenced refreshes on unrelated sources do not run concurrently;
- a waiting refresh holds its own `Sources` row lock while it waits, so an
  ordinary indexer for that source can block behind a refresh that is itself
  waiting on another refresh;
- ordinary indexers never take the lock and are not blocked by it.

**Measured:** 286 stale links refreshed in ~0.7 s
(`TestPostgresFencedRefreshBatchesLargeOldGeneration`). The serialised section
is short but the design is a global bottleneck by construction.

**Deadlock analysis:** the lock order is
`source -> advisory -> page -> object(ascending)`. Claim, heartbeat and terminal
reporting use `source -> claim -> history`; they never take the advisory lock,
so the only shared resource is the source row, which is always first. A
cross-transaction cycle would require an ordinary indexer to hold an object row
while waiting for a source row it did not lock first — not possible, because
`validateIndexingFence` takes the source row at transaction start. Five
iterations of two fenced refreshes plus one ordinary indexer on one shared
object hash completed with no deadlock or lock timeout
(`TestPostgresFencedRefreshConcurrentRefreshersAndIndexer`).

**Recommendation:** keep it. Record it as a throughput limit, not a
correctness issue, and revisit only if fenced refresh becomes a hot path.

### R-04 — The batching loop is bounded but the PostgreSQL trigger cost is not (P2, performance)

`replaceFencedPageObjectsTx` processes at most `fencedRefreshObjectBatch` (128)
links per iteration and loops until the page has no old links. The loop
terminates and a later-batch failure rolls the whole replacement back
(`TestFencedRefreshBatchesLargeOldGenerationSQLite`,
`TestFencedRefreshLaterBatchFailureRollsBackSQLite`,
`TestPostgresFencedRefreshBatchesLargeOldGeneration`).

However, on PostgreSQL the pre-existing `trg_after_delete_webobjectsindex` is
`FOR EACH ROW`, so N stale links still perform N `COUNT(*)` statements inside
the transaction regardless of the application batch size. The batching bound
therefore limits Go-side memory and round-trip size, not database work.

**Recommendation:** document, and include the measured refresh time in capacity
planning against `database.transaction_timeout` (default 300 s). No change now.

### R-05 — Page-lock boundary is correct (verified, no issue)

`lockFencedRefreshPageTx` takes `SELECT ... FOR UPDATE` on the existing
`SearchIndex` row before any write, and `ensureFencedRefreshSingleSourceTx`
counts other `SourceSearchIndex` links under that lock.
`TestPostgresFencedRefreshPageLockBlocksConcurrentLink` proves a concurrent
`INSERT INTO SourceSearchIndex` for the same page blocks for the duration and
only succeeds after the refresh releases the lock. Because
`insertOrUpdateSearchIndex` performs the `SearchIndex` upsert (which would itself
block on the same row) before linking, there is no path that links the page
without first contending for this lock.

The residual documented case is a source that links the page **after** the
refresh commits. That is a later write by another actor, not a retroactive
mutation, and it is out of scope.

### R-06 — Orphan lock is correct in isolation (verified, no issue)

`deleteFencedRefreshOrphanTx` takes `SELECT ... FOR UPDATE` on the
`WebObjects` row, then counts references, then deletes only at zero.
`TestPostgresFencedRefreshOrphanLockWaitsForInFlightReference` holds an
uncommitted link, proves the helper blocks, proves it then observes the
committed reference, and proves the object, its attributes and the other page's
link all survive.

This is the property R-01 breaks, because the trigger bypasses it.

### R-07 — Rollback semantics are correct within the main transaction (verified)

- `TestFencedRefreshRollbackRestoresOldContentSQLite` — rollback restores links
  and object.
- `TestFencedRefreshLaterBatchFailureRollsBackSQLite` — a failure in the second
  batch restores all 267 objects and 268 links.
- `TestPostgresFencedRefreshRollbackRestoresOldGeneration` — an injected delete
  failure restores the old link, object and content.
- `TestPostgresFencedRefreshRejectsExpiredClaim` — an expired claim cannot
  commit.

**Limit that must be stated:** the atomic unit is the page/object phase.
Keywords, metatags, object attributes and time-series buckets commit in
separate transactions after the main commit. A failure in one of those phases
marks the attempt `failed` while the page/object replacement remains durable.
The design document now says this explicitly (section 10.5); no code change is
warranted, but support documentation must not promise whole-page rollback.

### R-08 — Fenced indexing failure cannot be reported as `completed` (verified)

`fencedIndexingFailure()` returns true only for a fenced coordinator and an
`errIndexingFailure`-wrapped error. `indexPageContext` wraps every error with
`errIndexingFailure`, the worker drain fails a fenced crawl on it, and
`CrawlInitialURL` propagates the initial-page failure. Legacy behaviour is
preserved: non-critical worker errors are logged and the crawl continues
(`TestFencedIndexingFailureClassification`).

No path was found that turns a fenced indexing error into `completed`.

### R-09 — Unbound fenced attempts do not write source status (verified, fail-closed)

Schema 1.17 stores no operation principal for an unbound attempt. The approved
B2V decision suppresses source-status mutation unless ownership continuity can
be proven, and the earlier timestamp-based proof was shown unsound. Current code
keeps the source in `processing`; the claim lease expiry makes it claimable
again. This is the correct fail-closed behaviour and is unchanged by this
package. The consequence is a repeated crawl after lease expiry for unbound
sources, which is a known P2 limitation pending the ownership-epoch DDL
(approved as a model only).

### R-10 — Cross-source refusal is explicit and side-effect free (verified)

`ensureFencedRefreshSingleSourceTx` returns `errFencedRefreshCrossSourcePage`
wrapped with the index ID and the number of other sources. The error is returned
before any write, the transaction is rolled back, and the page's content is
unchanged (`TestPostgresFencedRefreshRefusesCrossSourcePage` asserts content is
still "generation one" and one link remains). The message contains no URL,
credential or page content.

### R-11 — Keyword suppression test was nondeterministic; production logic is correct (P1 release reliability, fixed)

**Evidence of the defect:** inserting a keyword row and re-inserting it 31
seconds later flips the production predicate and changes
`last_updated_at`. Reproduced directly against PostgreSQL 17.12 with the exact
statement. `TestPostgresKeywordSetPersistence` asserted the timestamp was
unchanged without re-anchoring the window, so any run whose body exceeded 30 s
failed. That matches the 38–56 s failures recorded in the B2F1 report and
reproduced on the base commit.

**Not a performance regression:** the test does not cover the fenced refresh
code, and measuring it on the reviewed commit gave 13.5–15.9 s of test time on
an unloaded host.

**Fix:** `upsertPostgresKeywordRowsTx` was extracted (pure refactor; the public
`upsertPostgresKeywords` still owns the transaction). The test now re-arms the
window immediately before asserting, and a new
`TestPostgresKeywordSuppressionWindowBranches` pins both branches with
unambiguous inputs plus the exact boundary at 29.999999 s / 30 s / 30.000001 s
inside one transaction. Mutation check: changing the production interval from
30 s to 29 s makes the first two boundary cases fail, so the new test is not
vacuous. Production semantics are unchanged.

### R-12 — Documentation contradictions (P3, fixed)

`ASYNC_GOAL_B3C3B2F1_REFRESH_DESIGN.md` said `Fenced refresh | refused
(unchanged)`, "it has not been implemented or exercised", "why fenced refresh
stays refused in this package", and "required tests before activation (not
run)", while also containing an "implemented behaviour" subsection. It now
carries dated `Original design (2026-11-18)` versus
`Implemented result (2026-11-20)` labels, keeps the historical text verbatim
under explicit "historical/superseded" headings, and adds section 10 with the
verified behaviour, the trigger interaction, the trigger race, the coverage
matrix, the post-commit semantics and the `null:` protocol inventory. No new
approval was invented.

### R-13 — Mutation-path inertness re-checked (verified, no issue)

Source and this gate's Docker run confirm:

- no production call to a mutating MCP `tools/call`;
- no insert into `MCPMutationDispatchAttempts`;
- no dispatcher, worker, lease, retry or reconciliation loop;
- `execution_backend` remains `none`;
- grant of `MCPMutationDispatchAttempts` privileges exists for a future
  dispatcher but nothing writes the table.

### R-14 — MySQL (verified, no issue)

`SourceCrawlAttemptSupported` excludes MySQL, fenced claim returns unsupported,
durable crawl approvals fail closed. Legacy MySQL behaviour is unchanged.

## 3. Concurrency graph, as reviewed

```text
claim allocation:      policy advisory (shared) -> Sources -> Claims -> Attempts
heartbeat:             Sources -> Claims -> Attempts
terminal report:       Sources -> Attempts -> Claims -> read proposals/approvals
                       -> attempt CAS -> Status CAS -> source status
fenced indexing:       Sources (ValidateTx) -> refresh advisory (xact)
                       -> SearchIndex page (FOR UPDATE)
                       -> WebObjects upsert (FOR UPDATE via CTE)
                       -> WebObjectsIndex deletes (fires orphan trigger)
                       -> WebObjects (FOR UPDATE) -> delete
ownership transfer:    Sources -> Users(asc) -> grants
```

The only resource reachable from two different orders is the `WebObjects` row:
the fenced helper locks it `FOR UPDATE`, while the FK insert path takes
`KEY SHARE` on it without holding a source row. `KEY SHARE` and `FOR UPDATE`
conflict, which is the intended serialisation. The trigger race (R-01) is not a
lock-order inversion; it is a check-then-act bug in a trigger that does not take
the stronger lock at all.

## 4. Residual risks not addressed by this package

- The ownership epoch is a semantic decision only. Unbound source status stays
  suppressed until DDL is approved.
- Fenced `null:` deletion stays quarantined.
- Append-only artifacts (screenshots, files, time-series rows) are not rolled
  back if a fence is lost after their transaction commits.
- In-flight WebDriver, email, plugin and agent calls are not interruptible; the
  fence stops the next operation, not the current one.
- SQLite fenced execution is test-only by documentation; its DSN lacks
  per-role privileges.
- No retention job exists for the schema-1.17 goal tables.

## 5. Review conclusion

The fenced-refresh implementation itself is defensible: the transaction
boundary, page lock, cross-source refusal, page-scoped link replacement,
orphan-only deletion and rollback all behave as documented, and the new
concurrency tests exercise them rather than a single happy path.

The release risk is not in the new code. It is in two **pre-existing** schema
behaviours that the new code makes reachable and that the design document
previously described as solved:

1. the legacy shared-object deletion (P1, no race needed), and
2. the trigger check-then-act orphan deletion (P0 for contended
   `refresh_content`, needs a race).

Neither is reachable at the 2.3.0 default configuration. Both are reachable as
soon as `refresh_content` is enabled. That is the finding the release decision
must turn on.
