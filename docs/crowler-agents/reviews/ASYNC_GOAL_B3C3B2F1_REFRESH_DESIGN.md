# WP-05B3C3B2F1 atomic fenced refresh — design review

## Status

**APPROVED AND IMPLEMENTED (WP-05B3C3B2F1 / B3 Decision 2, 2026-11-18).
RELEASE-GATE RE-REVIEWED (WP-REL230-RC1, 2026-11-20): one pre-existing
PostgreSQL concurrency defect found, see section 10.**

The maintainer recorded Decision 2 approval in
[`ASYNC_GOAL_MAINTAINER_DECISIONS.md`](ASYNC_GOAL_MAINTAINER_DECISIONS.md)
(Decision 1m).

This document has two halves and they are deliberately labelled:

- **Sections 1-9 are the original pre-approval design record**, written on
  2026-11-18 while Decision 2 was still blocked. They explain why the naive
  "move the delete into the main transaction" approach was unsafe, and they
  still describe the package as refusing fenced refresh. That was true when they
  were written and is retained unchanged so the evidence chain stays auditable.
- **Section 10 is the implemented result**, re-verified on 2026-11-20 against
  `9e719acce04d3606c46821e33b4f8dc23baa6b36`. Where sections 1-9 conflict with
  section 10, section 10 describes the shipped behaviour.

The implementation keeps legacy refresh unchanged, refuses a page shared with
another source, replaces only page-scoped object links, deletes only orphaned
objects, and runs every write inside the already validated indexing
transaction. Schema stays 1.17.

| Item | Original design (2026-11-18) | Implemented result (2026-11-20) |
| --- | --- | --- |
| Repository | `pzaino/thecrowler` | `pzaino/thecrowler` |
| Branch | `develop` | `develop` |
| Reviewed HEAD | `6a682ea6091fdb0a43b76e57616a2bc8a2c2edaa` | `9e719acce04d3606c46821e33b4f8dc23baa6b36` |
| Predecessor baseline | `f0f4fc97da6e2e5975b4cf26e5b8b4610324d4e2` (B2R) | `be5572de9064c4e4020d5b8f3adf478de4952aad` |
| Release | `v2.3.0` | `v2.3.0` |
| Schema | `1.17` | `1.17` |
| Writer generation | `verified-ownership-v1.17` | `verified-ownership-v1.17` |
| Fenced refresh | refused (unchanged) | implemented; only reachable in operator-selected `fenced` mode |
| Default claim mode | `legacy` (unchanged) | `legacy` (unchanged) |
| `refresh_content` default | `false` | `false` (unchanged) |

## 1. Why this document exists

B2V handed the maintainer a set of independent B3 decisions. Decision 2 asks
whether to implement an atomic `refresh_content` for fenced crawls using
existing schema 1.17. The B2V proposal summarized the intent as "move the
delete inside the already-fenced main indexing transaction".

That summary is necessary but **not sufficient**. A source-level audit of the
actual schema and delete SQL shows two structural sharing properties that a
source-scoped crawl fence does not authorize:

1. `WebObjects` rows are globally deduplicated by `object_hash`, and the
   refresh delete removes the **object row**, not just the page link.
2. `SearchIndex.page_url` is globally unique, while one page row can be linked
   to several `Sources` through `SourceSearchIndex`.

Both are demonstrated below on a disposable PostgreSQL 17 instance using the
repository's real `postgresql-setup.pgsql`.

## 2. Source-grounded call graph

```text
CrawlWebsite (initial page and worker pages)
  -> indexPage / indexPageContext                pkg/crawler/crawler.go:1150,1166
       -> checkCrawlFence                        (pre-work local fence check)
       -> pageIndexingAdmissions.acquire
       -> RefreshContent branch                  :1227
            -> getIndexIDForURL                  :1154   (read outside any tx)
            -> checkCrawlFence                   :1244
            -> deleteWebObjects                  :1247 -> :2055  (OWN transaction)
       -> Tx1 BEGIN                              :1280
            -> validateIndexingFence(tx)         :1284   (source row FOR UPDATE)
            -> insertOrUpdateSearchIndex(tx)     :1336 -> :1981
                 SearchIndex upsert + SourceSearchIndex link
            -> insertOrUpdateWebObjects(tx)      :1347 -> :2124
                 WebObjects hash upsert + WebObjectsIndex link
            -> validateIndexingFence(tx)         :1357   (pre-commit)
            -> COMMIT                            :1361
       -> post-commit keywords                   :1378
       -> post-commit MetaTags (own tx)          :1401
       -> post-commit ObjectAttributes (own tx)  :1476
       -> post-commit Time Series (own tx)       :1551
```

`validateIndexingFence` is `SourceCrawlHandoff.ValidateTx`
(`pkg/database/source_crawl_fence.go:147`): it locks the `Sources` row with
`FOR UPDATE` and verifies policy mode, source liveness/enablement/`processing`,
current claim, attempt history and lease. It does **not** check ownership and
does **not** lock or validate the page or object rows.

`deleteWebObjects` (`pkg/crawler/crawler.go:2055`) runs in its own bounded
transaction (`webObjectCleanupTimeout = 15m`) and executes exactly:

```sql
DELETE FROM WebObjects
WHERE object_id IN (
    SELECT object_id FROM WebObjectsIndex WHERE index_id = $1
);
```

No check of other `WebObjectsIndex` rows, no page lock, no fence.

## 3. Data dependency of one page refresh

For a page `P` (`index_id = I`) currently linked to object `O1`, refreshed to
new object `O2`:

| Object | Current refresh behaviour | Atomicity today | Proposed fenced semantics |
| --- | --- | --- | --- |
| `SearchIndex` row `I` | updated in place by `ON CONFLICT (page_url) DO UPDATE`; empty new title/summary/language/type keep the old value (`COALESCE(NULLIF(...), old)`) | same tx as the object write | unchanged; not a replacement of the row |
| `SourceSearchIndex (S, I)` | insert-if-missing, never removed | same tx | unchanged |
| `WebObjects` row `O1` | **deleted globally** by `deleteWebObjects` | **separate prior tx** | delete only if no other page references it |
| `WebObjectsIndex (I, O1)` | removed by FK cascade from the object delete | **separate prior tx** | delete the page-scoped link explicitly |
| `ObjectAttributes(O1)` | removed by `trg_cleanup_webobject` (statement-level) | follows the object delete | only for orphaned objects |
| `EntityMemberships(O1)` | removed by the same trigger | follows the object delete | only for orphaned objects |
| `ObjectCorrelations(O1)` | removed by the same trigger | follows the object delete | only for orphaned objects |
| `WebObjects` row `O2` | hash upsert (`ON CONFLICT (object_hash) DO UPDATE`) | main tx | unchanged, inside the same tx |
| `WebObjectsIndex (I, O2)` | insert `ON CONFLICT DO NOTHING` | main tx | unchanged |
| `Keywords` / `KeywordIndex` | upsert / append; **old entries are never removed** | post-commit | unchanged; refresh is not a keyword replacement |
| `MetaTags` / `MetaTagsIndex` | upsert / append; old entries retained | post-commit tx | unchanged |
| `Screenshots`, files | append-only new rows | outside refresh | unchanged |
| `TimeSeriesObservations` | append-only | post-commit tx | unchanged, never a completion proof |
| `Events` (`crawl_completed`) | only after an applied `completed` terminal result | terminal tx | unchanged |

Two facts follow directly:

- Legacy `refresh_content` is **object-link replacement for the current page
  object**, not a full page replacement. It leaves keywords, metatags,
  screenshots and historical observations from the previous crawl in place.
- A fenced "atomic" refresh can at best make the page/object phase atomic. It
  cannot make the post-commit phases atomic without a much larger redesign.

## 4. Global sharing analysis

### 4.1 Schema facts

| Definition | Location | Consequence |
| --- | --- | --- |
| `SearchIndex.page_url TEXT NOT NULL UNIQUE` | `pkg/database/postgresql-setup.pgsql:596` | one page row per URL |
| `SourceSearchIndex UNIQUE(source_id, index_id)` | `:1215` | many sources can link the same page row |
| `WebObjects.object_hash VARCHAR(64) UNIQUE` | `:692` | one object row per content hash, globally |
| `WebObjectsIndex UNIQUE(index_id, object_id)` + FKs with `ON DELETE CASCADE` | `:1256` | links are page-scoped, but the object row is global |
| `trg_cleanup_webobject AFTER DELETE ... FOR EACH STATEMENT` | `:984` | deleting an object row removes its attributes/memberships/correlations |

### 4.2 Reproducible proof (disposable PostgreSQL 17.12)

Loaded the repository's `postgresql-setup.pgsql`, then:

1. created two sources (`https://a.example/`, `https://b.example/`);
2. inserted the global page row `https://b.example/` and linked **both**
   sources to its `index_id = 1`;
3. created a second unrelated page `https://other.example/dup` (`index_id = 2`);
4. inserted one `WebObjects` row with hash `aaaa…` and linked it to **both**
   pages; inserted one `ObjectAttributes` row for it;
5. executed the exact `deleteWebObjects` SQL for `index_id = 1`.

Observed:

```text
--- BEFORE
 source_id |        url         | index_id | object_id
-----------+--------------------+----------+-----------
         3 | https://a.example/ |        1 |         1
         4 | https://b.example/ |        1 |         1

--- multi-source linkage of the shared page row
 index_id | linked_sources
----------+----------------
        1 |              2

DELETE 1

--- AFTER the exact deleteWebObjects SQL for source A page
 other_page | other_page_links
------------+------------------
          2 |                0

 shared_object_rows
--------------------
                  0

 shared_object_attributes
--------------------------
                        0
```

The unrelated page lost its link, its object and its attributes. No error was
raised. This is a genuine cross-page data-loss path in the current
`deleteWebObjects` implementation, reachable whenever two pages hash to the
same content (mirrored pages, identical error/consent pages, paginated
duplicates).

A second, pre-existing consequence of the same model: `VacuumSource`
(`pkg/database/source.go:611`) deletes `WebObjectsIndex`, `KeywordIndex` and
`MetaTagsIndex` rows for every index linked to the source, so vacuuming source
A also affects a shared page's associations. This predates B2V and is out of
scope; it documents that page associations are not source-isolated.

### 4.3 What this means for Decision 2

A source-scoped fenced attempt cannot authorize deleting an object row that
another page or source still reaches. The delete must be reduced to:

- page-scoped link removal for the exact `index_id`, and
- object deletion only when no other `WebObjectsIndex` row references the
  object.

Additionally, a page row linked to more than one source must be **refused**:
`WebObjectsIndex` has no source dimension, so removing the page's link set
changes what the other source sees.

## 5. Proposed transaction design (original design, 2026-11-18, pre-approval)

> **Historical status note (added 2026-11-20).** This section is the design as
> written before Decision 2 was approved. It says "it has not been implemented
> or exercised"; that was true on 2026-11-18. The implemented transaction and
> the tests that exercise it are recorded in section 10.

This is the smallest design that appears sound on schema 1.17. It is a
proposal; it has not been implemented or exercised.

### 5.1 Preconditions

- Fenced mode is active and `ValidateTx` succeeds.
- `ctx.sourceCrawl != nil` (fenced attempt). Legacy paths keep the current
  implementation unchanged.
- `RefreshContent` is true for the source.

### 5.2 Transaction body

```text
BEGIN
 1. ValidateTx(tx)                       -- source row FOR UPDATE
 2. SELECT index_id FROM SearchIndex
      WHERE page_url = $1 FOR UPDATE      -- page row lock (blocks concurrent
                                          -- SourceSearchIndex linking)
 3. if the page existed:
      a. count SourceSearchIndex links for that index_id
         excluding the current source
      b. if count > 0 -> rollback, errFencedRefreshCrossSourcePage
 4. upsert SearchIndex row; insert SourceSearchIndex link
 5. upsert WebObjects for the new content -> objID
 6. SELECT object_id FROM WebObjectsIndex
      WHERE index_id = $I AND object_id <> $objID
      ORDER BY object_id FOR UPDATE        -- deterministic lock order
 7. DELETE FROM WebObjectsIndex
      WHERE index_id = $I AND object_id <> $objID
 8. DELETE FROM WebObjects w
      WHERE w.object_id IN (<locked old ids>)
        AND NOT EXISTS (SELECT 1 FROM WebObjectsIndex wi
                        WHERE wi.object_id = w.object_id)
      -- orphan-only; the statement trigger is then correct by construction
 9. insert WebObjectsIndex (I, objID)
10. ValidateTx(tx)                        -- revalidation before commit
11. COMMIT
```

New page (no existing row): steps 3a/3b/6/7/8 are no-ops.

### 5.3 Invariants

- Old page content is deleted only if the transaction commits.
- Any failure or stale fence rolls the whole replacement back.
- An object still referenced by another page is never deleted.
- A page linked to another source is never refreshed under a single-source
  fence.
- The object delete order is ascending `object_id`, so two concurrent refreshes
  that share old objects cannot form a lock cycle.
- No legacy fallback: a fenced attempt that cannot prove these preconditions
  fails with `errIndexingFailure` and changes nothing.

### 5.4 Lock order and deadlock analysis

```text
source row (ValidateTx)
  -> SearchIndex page row
      -> WebObjectsIndex / WebObjects rows in ascending object_id
```

Claim, heartbeat and terminal reporting use `source -> claim -> history`.
Indexing now adds `page -> object` after the source row. The only cross-path
resource is the source row, always acquired first. Two refreshes on different
sources never share a source row, and their page/object locks are ordered.
Two crawls that hash to one object serialize on the object row; PostgreSQL FK
insertion into `WebObjectsIndex` takes a `KEY SHARE` lock on the object, which
conflicts with the `FOR UPDATE`/delete of an orphan check, so a concurrent
linker either commits first (and the object is no longer an orphan) or waits.

### 5.5 Post-commit semantics

After the main transaction commits, keywords, metatags, object attributes and
time-series emission still run in separate transactions. Their failures are
currently non-fatal except for PostgreSQL keywords, which now propagate through
`errIndexingFailure` and fail the fenced crawl (B2V-05). Therefore:

- `completed` means: the page row was updated, the page's object-link set was
  replaced, and every phase that the code treats as fatal succeeded.
- `failed` after the main commit does **not** imply the page was unchanged.
  The old object links were already replaced. This must be stated in the
  terminal reason and in operator documentation.
- Keywords and metatags from the previous crawl are retained. A refresh is not
  a search-index purge. Any future requirement for full replacement needs a
  separate design and approval.

### 5.6 Cost and timeout risk

The v1.16 hardening split the cleanup into its own transaction specifically so
a large `refresh_content` cleanup does not consume the `indexPage` transaction
budget (`database.transaction_timeout`, default 300 s). Moving it inside the
main transaction trades that for atomicity: a large refresh can now roll back
on timeout. That is safe (old content survives) but operationally visible and
must be measured on representative page sizes before activation.

### 5.7 Implemented behaviour (this work package)

> **Date note.** Written on 2026-11-18 as the "implemented" subsection of a
> document whose surrounding sections had not yet been updated. The authoritative
> implemented description, including the release-gate re-verification and the
> PostgreSQL trigger interaction, is section 10.

`pkg/crawler/fenced_refresh.go` implements the design:

1. `acquireFencedRefreshSerializationTx` takes a transaction-scoped PostgreSQL
   advisory lock (`hashtext('crowler:fenced-content-refresh')`) so two refreshes
   whose content hashes cross-reference each other cannot deadlock. SQLite
   writers are already serialized, so no extra lock is taken there.
2. `lockFencedRefreshPageTx` locks the existing `SearchIndex` row with
   `FOR UPDATE` (PostgreSQL) before any write.
3. `ensureFencedRefreshSingleSourceTx` refuses a page linked to any other
   source (`errFencedRefreshCrossSourcePage`).
4. After the new page/object write, `replaceFencedPageObjectsTx` deletes only
   `WebObjectsIndex` rows for this page and keeps the replacement object.
5. `deleteFencedRefreshOrphanTx` locks each old object `FOR UPDATE` (PostgreSQL)
   and deletes it only when no `WebObjectsIndex` row references it.
6. The whole replacement remains inside the main transaction started after
   `ValidateTx`, with a second `ValidateTx` before commit. Any error, stale
   fence, expiry or supersession rolls the page, links and orphan deletions
   back together.

During live verification a pre-existing propagation gap was found: `worker`
returned a page-indexing error only when the message contained the legacy
`[critical]` marker, so a non-critical fenced indexing failure was logged and
swallowed before the terminal reporter. `fencedIndexingFailure` now classifies
the error at the worker boundary and returns it, so the drain sets
`Status.LastError` and the crawl reports `failed`. Legacy non-critical worker
errors keep their historical log-and-continue behaviour.

The legacy branch still calls `getIndexIDForURL` and `deleteWebObjects` exactly
as before. The obsolete "refresh_content is not supported for fenced source
crawls" refusal is gone from the fenced path. Post-commit keyword/metatag/
attribute/time-series phases are unchanged and section 5.5 still applies:
`completed` means the page/object replacement committed, not whole-page
replacement.

## 6. Risk matrix

> **Status note (2026-11-20).** This matrix was written before approval. The
> "Mitigation in the proposal" column describes the design, and the "Residual"
> column assumed the design would be sufficient. Release-gate re-verification
> showed that the last row of the shared-object risk is **not** fully closed on
> PostgreSQL, because a pre-existing trigger performs its own unlocked
> check-then-delete before the fenced helper runs. See section 10.3 and
> `RELEASE_230_GO_NO_GO.md` B-01.

| Risk | Level | Mitigation in the proposal | Residual |
| --- | --- | --- | --- |
| Shared `WebObjects` deleted for another page | **Blocker today** | orphan-only delete under object lock | none if implemented and tested |
| Shared page row refreshed by one source | **Blocker today** | cross-source page refusal under page row lock | a source that links the page after commit sees the refreshed content; that is a later write, not retroactive deletion |
| Replacement failure loses old content | High today (separate tx) | delete and replace in one transaction | none if implemented |
| Deadlock between concurrent refreshes | Medium | ascending object order, page row before objects | must be proven by PostgreSQL concurrency tests |
| Transaction timeout on large refresh | Medium | rollback is safe | refresh fails; may need batching or a larger budget |
| Stale keywords/metatags after refresh | Medium | documented partial-phase semantics | search result staleness |
| `failed` with committed page replacement | Medium | truthful terminal error, documented | operator expectation |
| Trigger deletes attributes of a shared object | High today | orphan-only delete | none if implemented |
| SQLite portable path parity | Low | SQLite fenced execution is test-only | must still be covered by unit tests |
| MySQL | Low | legacy-only, unaffected | no fenced refresh |

## 7. Why fenced refresh stayed refused in this package (historical, superseded)

> **Superseded on 2026-11-18 by Decision 1m and implemented on 2026-11-18/19.**
> This section records the reasoning that was correct *before* approval. It is
> retained as the audit trail for why the naive design was rejected. The current
> behaviour is section 10.

1. Decision 2 is not approved.
2. Even with approval, the naive "move the delete into the main transaction"
   design is unsafe: it preserves the shared-object deletion and the
   cross-source page mutation proven in section 4.2.
3. A correct implementation requires the page-lock, cross-source check,
   link-only deletion and orphan-only object deletion described above, plus
   deterministic and real PostgreSQL concurrency proof, before it can be
   enabled.
4. The post-commit phase semantics cannot be made fully atomic without a much
   larger redesign; enabling refresh must therefore document partial
   replacement rather than claim whole-page atomicity.

## 8. Required tests before activation (historical checklist, 2026-11-18)

> **Status note (2026-11-20).** This was the pre-activation checklist. Section
> 10.4 records which items are now covered, which are covered by the release-gate
> suite added in WP-REL230-RC1, and which remain uncovered.

Deterministic SQLite and disposable PostgreSQL 17:

- refresh replaces the page link set; no stale link remains;
- forced failure after link deletion and before commit restores the old links,
  object and attributes;
- supersession before `ValidateTx`; supersession blocked behind the source row
  while the transaction is open; the stale worker cannot commit;
- lease expiry before, at and after the database clock boundary;
- operator switch to `legacy`, source delete, disable and ownership transfer
  while the transaction waits;
- two concurrent generations for the same page: no stale overwrite;
- shared object referenced by two pages: the other page keeps its link, object
  and attributes;
- orphan-only delete still lets the trigger clean attributes;
- cross-source page: refused with zero writes;
- new page: no delete path executed;
- empty content and malformed payload;
- keyword/metatag/attribute/time-series post-commit failure classification;
- repeated refresh idempotency, terminal replay, cancellation;
- legacy refresh regression and fenced `null:` quarantine unchanged.

Real Docker/Selenium (REF-01..REF-10 in the work package) only after approval;
none was executed here.

## 9. Approval required (historical, satisfied 2026-11-18)

> **Status note (2026-11-20).** This section was the approval gate. It was
> satisfied by Decision 1m in
> [`ASYNC_GOAL_MAINTAINER_DECISIONS.md`](ASYNC_GOAL_MAINTAINER_DECISIONS.md).
> The text below is retained as the record of what was requested.

No implementation may start until a tracked decision states the exact scope:

> Approve B3 Decision 2 only: implement atomic `refresh_content` for fenced
> crawls using existing schema 1.17, with cross-source page refusal,
> page-scoped link replacement, orphan-only object deletion, and all rollback,
> concurrency, safety and live-integration gates in WP-05B3C3B2F1. Do not
> approve B3 DDL, Decision 1, Decision 3 or production activation.

The decision must record approver, UTC date, work-package identifier and scope.
A chat request or the existence of a work package is not approval.

## 10. Implemented result and release-gate re-verification (2026-11-20)

This section is authoritative for the shipped behaviour.

### 10.1 What the code does

`pkg/crawler/indexPageContext` chooses the refresh strategy per page:

```text
fencedRefresh := ctx.config.Crawler.RefreshContent && ctx.sourceCrawl != nil
```

- `RefreshContent == false` (the `config.default` value): no refresh path runs.
- `RefreshContent == true` and no fenced handoff (legacy, and every MySQL
  deployment): `getIndexIDForURL` + `deleteWebObjects` in their own bounded
  transaction, unchanged from before Decision 2.
- `RefreshContent == true` and a fenced handoff: the fenced path in
  `pkg/crawler/fenced_refresh.go`, inside the main validated transaction.

The fenced transaction body is:

```text
BEGIN
  ValidateTx(tx)                          -- Sources row FOR UPDATE
  pg_advisory_xact_lock(hashtext('crowler:fenced-content-refresh'))
  SELECT index_id FROM SearchIndex
    WHERE page_url = $1 FOR UPDATE        -- page row lock
  if the page exists:
      count SourceSearchIndex links for index_id excluding this source
      if > 0 -> rollback, errFencedRefreshCrossSourcePage
  upsert SearchIndex; insert SourceSearchIndex link
  upsert WebObjects for the new content -> objID; insert WebObjectsIndex (I, objID)
  replaceFencedPageObjectsTx(I, objID)    -- bounded 128-row loop
  ValidateTx(tx)                          -- revalidation before commit
COMMIT
```

`replaceFencedPageObjectsTx` repeatedly selects up to `fencedRefreshObjectBatch`
(128) page-scoped object IDs in ascending `object_id` order, excludes the
replacement object, deletes those `WebObjectsIndex` rows, then calls
`deleteFencedRefreshOrphanTx` for each. The orphan helper takes
`SELECT ... FOR UPDATE` on the `WebObjects` row and deletes it only when
`COUNT(*) FROM WebObjectsIndex WHERE object_id = $1` is zero.

### 10.2 Two facts the original design did not state

Both were established during the release gate and both matter operationally.

**The advisory lock serialises every fenced refresh globally.** It is a single
key (`hashtext('crowler:fenced-content-refresh')`), so two fenced refreshes on
unrelated sources do not run concurrently: the second waits for the first's
main transaction. Ordinary (non-fenced) indexing never takes the lock and is not
blocked by it. The lock is acquired *after* the source row lock, so a waiting
refresh holds its own source row while it waits. A measured 286-link refresh
took about 0.7 s of wall time in the release-gate environment, so the serialised
section is short, but the design is a global bottleneck by construction and must
not be described as per-source concurrency.

**PostgreSQL already deletes orphaned objects from a pre-existing trigger.**
Schema 1.17 installs `trg_after_delete_webobjectsindex`, an `AFTER DELETE FOR
EACH ROW` trigger on `WebObjectsIndex` that calls
`handle_shared_entity_deletion()`. That function counts the remaining rows and
deletes the `WebObjects` row when the count is zero. Because it fires during the
link delete, it normally removes the object *before*
`deleteFencedRefreshOrphanTx` runs; the explicit helper then finds no row and
returns. SQLite has no equivalent trigger, so there the explicit helper performs
the delete. Both backends converge on the same observable state, but the design
must not claim the explicit helper is the deleting authority on PostgreSQL.

The trigger also imposes a cost the batching loop does not avoid: it is
`FOR EACH ROW`, so a page with N stale links performs N `COUNT(*)` statements
even though the application loop is batched at 128.

### 10.3 A pre-existing concurrency defect the fenced path inherits

`handle_shared_entity_deletion()` performs a `COUNT(*)` and then a separate
`DELETE`, under `READ COMMITTED`, without locking the `WebObjects` row first.
A concurrent transaction that has inserted its own `WebObjectsIndex` row for the
same object but has not committed is invisible to the count. The `DELETE` then
waits on that transaction's foreign-key row lock and, once it commits, proceeds
anyway. The `WebObjectsIndex.object_id` foreign key is `ON DELETE CASCADE`, so
the other page's just-committed link disappears with no error.

`TestPostgresSharedObjectTriggerRaceCharacterization`
(`pkg/crawler/fenced_refresh_concurrency_postgres_integration_test.go`)
reproduces this deterministically: page Y links the object inside an uncommitted
transaction, page X deletes its link, the delete blocks, page Y commits, and the
final state is zero object rows and zero page-Y links.

The fenced helper's `FOR UPDATE` plus re-count would be safe on its own, but the
trigger fires first, so the fenced path does not fix this. The legacy path is
worse in a different way: it deletes the object row outright, so it destroys a
shared object without needing a race (see section 4.2 and
`TestPostgresLegacyRefreshSharedObjectHazardReproducer`).

Closing the race requires changing the schema function (for example locking the
parent row before the count) or retiring the trigger in favour of lock-guarded
application logic. Both are schema changes and are outside Decision 2. The
release gate therefore records this as an open blocker rather than shipping a
schema change under a fenced-refresh approval. See
[`RELEASE_230_GO_NO_GO.md`](RELEASE_230_GO_NO_GO.md).

### 10.4 Test coverage actually executed

Deterministic SQLite (`go test ./pkg/crawler`):

| Property | Test |
| --- | --- |
| Page link set replaced; orphan removed; shared object kept | `TestFencedRefreshReplacesPageLinksSQLite` |
| Replacement object never deleted | `TestFencedRefreshKeepsReplacementObjectSQLite` |
| Rollback restores links and object | `TestFencedRefreshRollbackRestoresOldContentSQLite` |
| Cross-source page refused, then accepted after the second link is removed | `TestFencedRefreshRefusesCrossSourcePageSQLite` |
| Missing page performs no cleanup | `TestFencedRefreshLocksOnlyExistingPageSQLite` |
| Shared object keeps its attributes; orphaned object loses them | `TestFencedRefreshKeepsSharedObjectAttributesSQLite` |
| More than two batches terminate and a shared object inside the range survives | `TestFencedRefreshBatchesLargeOldGenerationSQLite` |
| A failure in the second batch rolls the whole replacement back | `TestFencedRefreshLaterBatchFailureRollsBackSQLite` |
| Fenced indexing errors are fatal; legacy non-critical errors are not | `TestFencedIndexingFailureClassification` |

Disposable PostgreSQL 17 (`go test -tags=integration ./pkg/crawler`):

| Property | Test |
| --- | --- |
| Single-source replacement, old orphan removed | `TestPostgresFencedRefreshAtomicReplacement` |
| Shared object and the other page's link survive | `TestPostgresFencedRefreshPreservesSharedObject` |
| Cross-source page refused with zero writes | `TestPostgresFencedRefreshRefusesCrossSourcePage` |
| Injected delete failure restores the old generation | `TestPostgresFencedRefreshRollbackRestoresOldGeneration` |
| Expired claim cannot commit a refresh | `TestPostgresFencedRefreshRejectsExpiredClaim` |
| 286 stale links across three batches, shared object inside the range preserved, measured wall time | `TestPostgresFencedRefreshBatchesLargeOldGeneration` |
| The page `FOR UPDATE` lock blocks a concurrent `SourceSearchIndex` insert | `TestPostgresFencedRefreshPageLockBlocksConcurrentLink` |
| The orphan `FOR UPDATE` lock waits for an in-flight reference and then preserves the object and its attributes | `TestPostgresFencedRefreshOrphanLockWaitsForInFlightReference` |
| Which component deletes an orphan on PostgreSQL, and that a referenced object is preserved | `TestPostgresFencedRefreshOrphanCleanupTriggerCharacterization` |
| Two fenced refreshes plus an ordinary indexer contend on one shared object; no deadlock, every page keeps one link | `TestPostgresFencedRefreshConcurrentRefreshersAndIndexer` |
| The pre-existing orphan trigger race, reproduced deterministically | `TestPostgresSharedObjectTriggerRaceCharacterization` |
| The legacy shared-object data-loss hazard, reproduced deterministically | `TestPostgresLegacyRefreshSharedObjectHazardReproducer` |

### 10.5 Semantics operators must understand

- **Atomic means the page/object phase.** The main transaction covers the
  `SearchIndex` upsert, the `SourceSearchIndex` link, the `WebObjects` upsert,
  the `WebObjectsIndex` replacement and the orphan deletion. Keywords,
  metatags, object attributes and time-series buckets are separate
  transactions.
- **`completed` therefore means "the page/object replacement committed and
  every phase the code treats as fatal succeeded"**, not "the whole page is a
  new generation". Keywords and metatags from the previous crawl are retained.
- **`failed` after the main commit does not mean the page is unchanged.** The
  replacement is already durable; the terminal reason reports the later phase
  that failed.
- **A fenced `null:` source is quarantined.** `null:` is the pipeline-testing
  protocol described in section 10.6, not a deletion request. Legacy `null:`
  deletion is unchanged.
- **Unbound fenced attempts do not write source status.** Schema 1.17 stores no
  operation principal for an unbound attempt, so the source stays `processing`
  and becomes claimable again when the claim lease expires. This is the
  fail-closed behaviour approved in WP-05B3C3B2V; the owner-epoch DDL that would
  lift it is approved only as a semantic model, not implemented.

### 10.6 What `null:` actually is

`null:` is an intentional pipeline-testing protocol: a source URL prefixed with
`null:` exercises the claim, queue, VDI-slot and lifecycle stages while
explicitly skipping content collection. It is not an invalid source and it is
not a synonym for "delete this source".

What actually executes in legacy mode:

| Stage | Executes? |
| --- | --- |
| Source claim and queue dispatch | Yes |
| VDI slot acquisition in the worker loop | Yes (the slot is taken, then released without navigation) |
| Browser navigation, action rules, scraping | No |
| Page indexing, web objects, keywords, metatags | No |
| Source row deletion by `deleteNullSources` | Yes, asynchronously by source ID |

What executes in fenced mode: claim and queue dispatch only. The worker logs the
quarantine and reports a terminal failure through the handoff. No deletion is
performed, because a deletion queued by source ID cannot prove that the claim is
still current, and schema 1.17 has no durable claim-time owner to prove
continuity. This is Decision 3, deferred.

Treating `null:` as "all pipeline stages execute" is wrong: the point of the
protocol is that collection is skipped while lifecycle handling is exercised.
