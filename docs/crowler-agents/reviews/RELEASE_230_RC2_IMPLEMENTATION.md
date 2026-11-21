# Release 2.3.0 RC2 implementation

Work package: `WP-REL230-RC2`
Repository: `pzaino/thecrowler`
Baseline: `e6a41d317835356b1ed7ebd3fd31c8aa781f272c` (`develop`, clean)
Release: `v2.3.0`
Schema: `1.17` (unchanged; no new release)
Writer generation: `verified-ownership-v1.17` (unchanged)
Date: 2026-11-20

## 1. Approvals

Recorded in [`ASYNC_GOAL_MAINTAINER_DECISIONS.md`](ASYNC_GOAL_MAINTAINER_DECISIONS.md)
as Decision 1o, with approver, UTC date, scope, rollback, schema compatibility
and the attached test gates.

| Decision | Answer | Scope |
| --- | --- | --- |
| RC2-A | APPROVE | Lock the parent artifact row before the orphan count in `handle_shared_entity_deletion()`, in the fresh install and the v1.17 migration. No unrelated DDL. |
| RC2-B | APPROVE | Replace the legacy global object delete with page-scoped unlink plus orphan-only deletion, sharing the fenced path's page lock, cross-source refusal and ordering. |

No other schema change, activation or production migration was authorised or
performed.

## 2. RC2-A: orphan-deletion race

### 2.1 The defect, precisely

Schema 1.17 installs `trg_after_delete_webobjectsindex`, an
`AFTER DELETE ... FOR EACH ROW` trigger on `WebObjectsIndex` that calls
`handle_shared_entity_deletion()`. The old body was:

```sql
IF (SELECT COUNT(*) FROM WebObjectsIndex WHERE object_id = OLD.object_id) = 0 THEN
    DELETE FROM WebObjects WHERE object_id = OLD.object_id;
END IF;
```

Under `READ COMMITTED` the count and the delete are two statements with no
parent row lock.

### 2.2 Before/after interleaving

Before RC2-A, with object `O` linked to page X and page Y about to link it:

| Step | Session A (deletes X's link) | Session B (adds Y's link) | Database state |
| --- | --- | --- | --- |
| 1 | | `BEGIN`; `INSERT WebObjectsIndex(Y,O)` | B holds `KEY SHARE` on `O` |
| 2 | `DELETE WebObjectsIndex(X,O)` | | A's trigger counts references |
| 3 | count sees only committed rows → 0 | | B's row is invisible |
| 4 | `DELETE WebObjects` | | A blocks on B's row lock |
| 5 | | `COMMIT` | `WebObjectsIndex(Y,O)` is now committed |
| 6 | delete proceeds (no re-check) | | `O` deleted; `ON DELETE CASCADE` removes `(Y,O)` |
| 7 | `COMMIT` | | Y's transaction reported success and its link is gone |

The RC1 characterization test recorded the final state
`object_rows=0`, `page_y_links=0`, `attributes=0`.

After RC2-A:

| Step | Session A | Session B | Database state |
| --- | --- | --- | --- |
| 1 | | `BEGIN`; insert `(Y,O)` | B holds `KEY SHARE` on `O` |
| 2 | `DELETE (X,O)` | | trigger runs `PERFORM 1 FROM WebObjects WHERE object_id = OLD.object_id FOR UPDATE` |
| 3 | blocks | | `FOR UPDATE` conflicts with `KEY SHARE` |
| 4 | | `COMMIT` | `(Y,O)` committed |
| 5 | lock acquired, count = 1 | | object preserved, no delete |
| 6 | `COMMIT` | | `object_rows=1`, `page_y_links=1`, attributes intact |

The `FOR UPDATE` is what makes the check-and-delete atomic: the concurrent
inserter cannot commit between the count and the delete, because it must first
acquire a conflicting lock on the same parent row.

### 2.3 Why `FOR UPDATE` and not `FOR NO KEY UPDATE`

`WebObjectsIndex.object_id` references `WebObjects(object_id)`, so an insertion
takes `FOR KEY SHARE` on the parent. Only `FOR UPDATE` conflicts with
`FOR KEY SHARE`. `FOR NO KEY UPDATE` deliberately does not, and would leave the
race open. This was verified by the positive regression, not assumed.

### 2.4 Deterministic order

The trigger is `FOR EACH ROW`, so one invocation always has exactly one `OLD`
row; there is no multi-row transition table to order. A statement that deletes
many links invokes the function once per link in the order PostgreSQL produces
the deleted rows. Each invocation takes one parent lock and releases it at
transaction end, so two transactions that delete overlapping link sets can still
deadlock if they touch the same parent objects in different orders.

The application paths that delete links already order their work:

- the refresh path walks page-scoped object IDs in ascending `object_id` order
  and is additionally serialised by the `crowler:fenced-content-refresh`
  advisory lock, now taken by both modes;
- `VacuumSource` deletes by `index_id IN (SELECT ...)`, whose order is chosen
  by the planner, and does not take the advisory lock.

`TestPostgresRefreshAndVacuumRemainConsistent` runs a refresh and a
`VacuumSource` against the same shared object and asserts both complete without
a deadlock and that the object exists exactly when a link remains. A deadlock
between two vacuum operations would be reported by PostgreSQL as an error and
retried by the caller; it cannot silently lose a link, because the loser rolls
back entirely.

### 2.5 All five artifact families

`handle_shared_entity_deletion()` is shared by five triggers. The same
check-then-act pattern existed for `MetaTags`, `Keywords`, `NetInfo` and
`HTTPInfo`, so all five branches now lock their parent before counting. The
demonstrated cross-page data loss was in the `WebObjects` branch; the others are
fixed for the same reason and covered by the static convergence test.

SQLite defines no stored function and no equivalent trigger (the setup file
documents the omission), so RC2-A is PostgreSQL-only and SQLite behaviour is
unchanged.

### 2.6 Schema and migration convergence

Release remains `1.17`, writer generation `verified-ownership-v1.17`.

- `pkg/database/postgresql-setup.pgsql` carries the new body for fresh installs.
- `pkg/database/db_migrations/postgresql-migration-v1.17.pgsql` now defines the
  same body and creates the five triggers if they are missing. A database
  upgraded from 1.16 previously kept whatever function it already had, because
  1.17 only defined it in the fresh-install script. Re-running the v1.17
  migration is the sanctioned update path for a development database already
  stamped 1.17, and CI already proves that migration is repeatable.

Convergence was rehearsed on a disposable PostgreSQL 17 instance:

```text
pre-fix function body (no FOR UPDATE)      -> has_lock=f
apply v1.17 migration (pass 1)             -> exit 0
apply v1.17 migration (pass 2)             -> exit 0
post-migration function body               -> has_lock=t
DBSchemaVersion                            -> 1.17
DatabaseWriterCompatibility                -> verified-ownership-v1.17
trg_after_delete_* triggers                -> 6
```

A static test (`TestPostgresSharedEntityDeletionConcurrencyContract`) asserts
that the setup and migration bodies are byte-identical, that each of the five
families locks its parent, and that SQLite keeps its documented omission.

## 3. RC2-B: legacy global object delete

### 3.1 The defect

`deleteWebObjects` ran in its own transaction before the replacement:

```sql
DELETE FROM WebObjects
WHERE object_id IN (SELECT object_id FROM WebObjectsIndex WHERE index_id = $1);
```

Two problems:

1. `WebObjects` is deduplicated by `object_hash`, so the row may be shared by
   other pages. Deleting it cascades their `WebObjectsIndex` links away and the
   statement-level trigger removes the object's attributes.
2. The delete committed before the replacement transaction, so a failure in
   between left the old content deleted.

Unlike the RC2-A race, this needed no concurrency: a single legacy refresh of
one of two identical pages destroyed the other page's artifact.

### 3.2 Placement decision

The legacy refresh now uses the same implementation as the fenced path, inside
the main indexing transaction:

```text
BEGIN
  ValidateTx(tx)                          -- fenced only; no-op for legacy
  advisory lock (refresh paths only)
  lock the page row FOR UPDATE
  refuse if the page is linked to another source
  upsert SearchIndex + SourceSearchIndex
  upsert WebObjects + WebObjectsIndex for the new content
  replace this page's WebObjectsIndex links, deleting only orphans
  ValidateTx(tx)                          -- fenced only
COMMIT
```

Rationale for moving the delete into the main transaction rather than keeping a
separate cleanup transaction:

- **Rollback safety.** A failure anywhere in the replacement now restores the
  old links, object and attributes. The separate-transaction design could not
  offer that.
- **One code path.** Two implementations of the same destructive operation was
  the source of the divergence in the first place.
- **Bounded cost.** The measured cost of replacing 286 links is ~0.7 s
  (`TestPostgresFencedRefreshBatchesLargeOldGeneration`), far below the default
  `database.transaction_timeout` of 300 s. A pathological page that exceeds the
  timeout now rolls back and preserves the old generation, which is the safe
  failure.
- **No external callers.** `deleteWebObjects` was unexported and only called
  from `indexPageContext`.

What did **not** become atomic: keywords, metatags, object attributes and
time-series buckets still commit in separate transactions after the main commit.
`completed` therefore still means "the page/object replacement committed and
every phase the code treats as fatal succeeded", not "the whole page is a new
generation". The implementation docs and the operator notes say so.

### 3.3 Observable behaviour change

| Case | Before | After |
| --- | --- | --- |
| Two pages share one content hash; one refreshes | the shared object, the other page's link and its attributes are destroyed | only the refreshing page's link is replaced; the other page keeps its object, link and attributes |
| Refreshing page is linked to more than one source | silently rewrites associations another source depends on | refused with `content refresh refused: page is shared with another source`, zero writes |
| Refresh fails part-way | old content already deleted (separate transaction) | whole replacement rolled back, old generation intact |
| `refresh_content: false` | no refresh | no refresh (unchanged) |
| New page, no existing row | no cleanup | no cleanup (unchanged) |

The cross-source refusal is a deliberate behaviour change: `WebObjectsIndex` has
no source dimension, so a single-source refresh cannot prove authority over the
associations another source sees. Failing closed is the only safe option that
preserves the shared-page model.

### 3.4 Removed code

- `deleteWebObjects` — replaced by the shared path.
- `getIndexIDForURL` — only used by the removed pre-transaction lookup.
- `webObjectCleanupTimeout` — only used by `deleteWebObjects`.

No exported API changed.

## 4. Event retention (RC2 section 4D)

RC1 observed `crawl_completed` rows disappearing while
`events.automatic_events_removal` was `never`.

Trace result: two independent mechanisms act on `Events`.

1. **Consumer acknowledgement.** `eventRemovalAction(policy, success)` decides
   between keeping the row, expiring it (`expires_at = now + 1 minute`) and
   deleting it. `never` and any unknown value keep the row.
2. **Janitor.** `cleanupExpiredEvents` and `runDailyEventJanitor` delete rows
   whose `expires_at` has passed. Every event is created with a finite
   `expires_at` — two minutes for `crawl_completed`, five minutes by default in
   `CreateEvent`.

So the RC1 observation was **TTL expiry by the janitor**, not a defect:
`never` means "the consumer never removes or expires the row", not "the row is
permanent". This is now documented in code and covered by
`TestEventRemovalActionPolicy` (all policy values, both outcomes) and
`TestPostgresEventJanitorDeletesOnlyExpiredRows` (expired deleted, future kept,
no-expiry kept).

One **pre-existing portability gap** was found and is *not* fixed here: the
janitor uses SQL `now()`, which PostgreSQL and MySQL accept but SQLite does not.
On SQLite the query fails with `no such function: now`, is logged, and nothing
is deleted. SQLite is not a supported Events deployment in this release, and
changing the janitor SQL is outside the RC2 approvals. It is recorded as a P3
limitation in the RC2 go/no-go.

## 5. Changed paths

| Path | Change |
| --- | --- |
| `pkg/database/postgresql-setup.pgsql` | `handle_shared_entity_deletion()` locks each parent row `FOR UPDATE` before counting |
| `pkg/database/db_migrations/postgresql-migration-v1.17.pgsql` | Same function body plus idempotent creation of the five `trg_after_delete_*` triggers, so 1.16 upgrades converge |
| `pkg/crawler/crawler.go` | Removed the pre-transaction legacy cleanup; both modes now take the page lock, cross-source check and in-transaction replacement. Removed `deleteWebObjects`, `getIndexIDForURL`, `webObjectCleanupTimeout` |
| `pkg/crawler/fenced_refresh.go` | Header and error text reflect that the helpers serve both modes; no algorithm change |
| `pkg/database/schema_sql_test.go` | `TestPostgresSharedEntityDeletionConcurrencyContract` |
| `pkg/crawler/webobject_postgres_integration_test.go` | Rewritten to drive the real legacy `indexPageContext` refresh instead of the removed helper |
| `pkg/crawler/fenced_refresh_concurrency_postgres_integration_test.go` | RC1 characterizations replaced by positive regressions; added zero-reference, 25-iteration stress and refresh/VacuumSource consistency tests |
| `services/events/main.go` | Consumer removal policy extracted into the pure `eventRemovalAction`; behaviour unchanged |
| `services/events/event_retention_test.go` (new) | Policy table test |
| `services/events/event_retention_postgres_integration_test.go` (new) | Janitor DB test |
| `Dockerfile.searchapi`, `Dockerfile.events`, `Dockerfile.thecrowler` | `ARG GOPROXY` with the public default, so a constrained build network can point at an internal proxy without disabling `go.sum` or `GOSUMDB` |
| `.github/workflows/postgresql-integration.yml` | Mandatory gates for the new regressions |
| `docs/test_policy.md`, `docs/crowler-agents/reviews/*` | Updated expectations and release records |

## 6. Residual risk

- **Legacy refresh refuses shared pages.** A deployment that relied on
  refreshing a page linked to several sources will now see the refresh fail.
  That is intentional and documented, but it is a behaviour change operators
  must know about.
- **Global refresh serialisation.** Both modes take the single
  `crowler:fenced-content-refresh` advisory lock for every page while
  `refresh_content` is enabled, because page creation must serialise with the
  existence check. With `refresh_content: false` (the default) ordinary indexing
  is unaffected. With it enabled, page indexing throughput is effectively
  serialised across sources, and a very large stale generation holds the lock
  for the whole replacement. This was already true of the fenced path before
  RC2; RC2 extends it to legacy so both modes behave the same. It is a
  throughput limitation, not a correctness one, and it is recorded in the RC2
  go/no-go as a reason to keep `refresh_content` opt-in.
- **Vendor-planner link order.** `VacuumSource` does not order its deletes and
  does not take the advisory lock. A concurrent vacuum and refresh are proven
  consistent, but two vacuums that lock the same parents in opposite orders can
  deadlock; PostgreSQL aborts one and the caller retries. No silent loss is
  possible because the loser rolls back.
- **Post-commit phases.** Keywords, metatags, attributes and time-series remain
  separate transactions; a failure there still leaves the page/object
  replacement durable.
- **SQLite Events janitor.** `now()` is not SQLite-compatible; documented, not
  fixed.
- **RC2-A protects the trigger path.** A future code path that deletes
  `WebObjects` rows directly, bypassing both the trigger and the explicit
  helper, still needs its own orphan check. No such path exists today.

## 7. Schema compatibility statement

No schema version, writer generation, release version, table, column, index or
public contract changed. The modification is inside the unreleased 1.17
function definition. A 1.17 database created before this change is updated by
re-running `postgresql-migration-v1.17.pgsql`, which is repeatable and already
part of CI. No production or shared database was migrated.
