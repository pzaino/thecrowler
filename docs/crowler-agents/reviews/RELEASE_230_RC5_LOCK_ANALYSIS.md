# Release 2.3.0 RC5 — shared-artifact lock analysis

Work package: `WP-REL230-RC5`
Baseline: `906000f3871c988e24a65ee32e1ec452491357d6` (`develop`, clean)
Release: `v2.3.0` · Schema `1.17` · Writer `verified-ownership-v1.17`
Date: 2026-11-20

This document answers one question with measurements instead of intuition: does
`handle_shared_entity_deletion()` serialise crawling badly enough to threaten
the release, and is there a change that reduces the cost **without** weakening
the RC2 data-integrity fix?

Short answer: the RC2 protection is sound and its cost is concentrated in
multi-link cleanup, not in normal crawling. A statement-level rewrite of the same
lock-and-re-count is safe and removes 2.8× to 4.8× of that cost, at the price of
~25% on the opt-in single-link refresh path. It is adopted.

---

## 1. The mechanism, verified in source

`pkg/database/postgresql-setup.pgsql` installs one `AFTER DELETE` trigger on each
link table:

| Link table | Parent | Parent id column | FK action |
| --- | --- | --- | --- |
| `KeywordIndex` | `Keywords` | `keyword_id` | `ON DELETE CASCADE` |
| `MetaTagsIndex` | `MetaTags` | `metatag_id` | `ON DELETE CASCADE` |
| `WebObjectsIndex` | `WebObjects` | `object_id` | `ON DELETE CASCADE` |
| `NetInfoIndex` | `NetInfo` | `netinfo_id` | `ON DELETE CASCADE` |
| `HTTPInfoIndex` | `HTTPInfo` | `httpinfo_id` | `ON DELETE CASCADE` |

Each link table also has a btree index on its parent-id column, which is what the
reference check uses.

### Lock graph

```text
INSERT link(parent_id, …)
  └─ FK check … takes FOR KEY SHARE on the parent row
       (compatible with other KEY SHARE, incompatible with FOR UPDATE)

DELETE link … (AFTER trigger)
  └─ PERFORM 1 FROM parent WHERE id = … FOR UPDATE
       (waits for every in-flight KEY SHARE on that parent)
       └─ re-count remaining links
            └─ DELETE parent   (cascades any link still there)
```

`FOR UPDATE` is not interchangeable with `FOR NO KEY UPDATE`: only `FOR UPDATE`
conflicts with `FOR KEY SHARE`, and only that conflict is what makes the re-count
trustworthy. RC2 chose it for exactly this reason, and the benchmark fixture uses
the same FK definition so the measured lock mode is the shipped one.

### Which statements actually delete links

This matters more than the lock mode, because the lock is only taken on
deletion. Verified in source at the baseline:

| Path | Statement shape | Families | Frequency |
| --- | --- | --- | --- |
| `VacuumSource` (`pkg/database/source.go`) | one `DELETE … WHERE index_id IN (SELECT …)` per family | all five | source removal/update |
| Page deletion / refresh replacement | cascade or page-scoped `DELETE` | all five / `WebObjectsIndex` | per page |
| `replaceFencedPageObjectsTx` (`pkg/crawler/fenced_refresh.go`) | one `DELETE` per link, ascending `object_id` | webobject | opt-in `refresh_content` |
| Keyword ingest (`upsertPostgresKeywordRowsTx`) | `INSERT … ON CONFLICT (keyword_id, index_id) DO UPDATE` | keyword | every indexed page |
| Metatag ingest (`insertOrUpdateMetaTags`) | `INSERT … ON CONFLICT` | metatag | every indexed page |
| Events janitor (`cleanupTooOldWebObjects`) | delete of unreferenced parents only | webobject | periodic |

Consequence: **normal crawling never deletes keyword or metatag links.** A hot
keyword therefore sees only `FOR KEY SHARE` locks, which do not conflict with one
another, and the new crawl path is not serialised by this trigger at all. The
contention lives in cleanup: vacuum, page removal, and (opt-in) content refresh.

This corrects an assumption in the earlier review text, which treated hot
keyword/metatag deletes as a production path. There is no code path that produces
them.

---

## 2. Isolation and snapshot reasoning

The application runs at PostgreSQL's default `READ COMMITTED`.
`TestPostgresSharedArtifactIsolationLevelIsReadCommitted` asserts both the
default and the active transaction setting, so the argument is not silently
dependent on a server configuration change.

Under `READ COMMITTED` each statement takes a fresh snapshot. The trigger's
`SELECT … FOR UPDATE` and its `NOT EXISTS` re-check are **separate statements**,
and the re-check runs after the lock is granted, so it sees every reference that
committed while the trigger was waiting. That is the whole correctness argument,
and it is why the shipped function is safe.

### Why the "one statement" simplification is unsafe

The obvious simplification is:

```sql
DELETE FROM Keywords k
 WHERE k.keyword_id = $1
   AND NOT EXISTS (SELECT 1 FROM KeywordIndex i WHERE i.keyword_id = k.keyword_id);
```

It is **not** equivalent. PostgreSQL evaluates the `DELETE` qualification during
the scan, *before* it waits for the row lock. A concurrent child `INSERT` does not
create a new version of the parent tuple, so there is nothing for EvalPlanQual to
re-check when the lock is finally granted: the qualification is not re-evaluated
and the delete proceeds, cascading the just-committed link away.

`TestPostgresSharedArtifactSingleStatementDeleteIsUnsafe` reproduces it on a
disposable database:

```text
characterization confirmed: the one-statement DELETE form removes the parent
and the concurrently committed link
```

So "fold the lock into the DELETE" is a data-loss shape, not an optimisation. It
is recorded here so it is not proposed again.

---

## 3. Candidates evaluated

| # | Candidate | Verdict |
| --- | --- | --- |
| 1 | Keep RC4F `FOR EACH ROW` + `FOR UPDATE` + `COUNT` | Correct; baseline |
| 2 | Fast existence check before the lock, lock only on suspected last link | **Rejected.** Two racing last-link removals each see the other's link, both skip, and the parent is orphaned. `TestPostgresSharedArtifactConcurrentLastLinkRemovals` is exactly that interleaving; the fast path cannot satisfy it without a background reaper. |
| 3 | One-statement `DELETE … AND NOT EXISTS` | **Rejected, measured data loss** (§2) |
| 4 | `FOR NO KEY UPDATE` | **Rejected.** Does not conflict with `KEY SHARE`, so it reopens the RC2 race |
| 5 | Application-level suppression of unchanged link deletes | Already implemented: keyword/metatag links are upserted, never deleted; refresh replacement already skips the replacement object. No further change available |
| 6 | Separate orphan garbage collector | Not required: synchronous deletion is correct and the measured cost is acceptable; a new background subsystem is a larger release risk |
| 7 | `AFTER DELETE FOR EACH STATEMENT` + `OLD TABLE` transition table, distinct parents in ascending id order, same `FOR UPDATE` + re-check | **Adopted** |

Candidate 7 keeps the RC2 semantics exactly (same lock, same re-check, same
ordering of lock-before-count), and changes only *how many times* the per-parent
work is done and *in what order*:

- a statement deleting N links for P distinct parents performs P lock/re-check
  cycles instead of N;
- parents are processed in ascending id order, so two concurrent multi-parent
  deletions acquire the same locks in the same order.

It does not make orphan cleanup lazy: the parent is still deleted inside the same
transaction.

---

## 4. Measurements

Harness: `pkg/database/shared_artifact_bench_postgres_integration_test.go`
(opt-in, disposable PostgreSQL 17 child database, FK-faithful fixture). Three
variants on identical fixtures: `rc4f` (frozen row-level baseline), `current`
(the body read from `postgresql-setup.pgsql`), `v228` (the pre-RC2 body, kept
only to show the historical throughput next to its known data loss).

Fixture per family: 8 parents × 200 links (1,600 links). `mass_delete` removes
the first 100 links of every parent in one statement while 8 workers insert new
links for the same parents. `single_link_churn` runs 8 workers × 120
delete+reinsert cycles on one parent (the refresh shape). `overlapping_vacua`
runs two concurrent single-statement vacua over the same parents, 3 iterations.
One measurement per family; the table is the mean across the five families:

| Workload | Variant | Throughput | p95 | p99 | Lock-waiting | Failures | Deadlocks |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| mass_delete | rc4f | 4,954 links/s | 82.4 ms | 118.1 ms | 42.3% | 0 | 0 |
| mass_delete | **current** | **13,700 links/s** | **48.3 ms** | **59.8 ms** | **34.7%** | 0 | 0 |
| mass_delete | v228 (unsafe) | 8,871 links/s | 34.3 ms | 37.0 ms | 23.8% | 0 | 0 |
| single_link_churn | rc4f | 85.2 ops/s | 142.8 ms | 169.5 ms | 85.6% | 0 | 0 |
| single_link_churn | **current** | 64.3 ops/s | 157.0 ms | 210.3 ms | 86.1% | 0 | 0 |
| single_link_churn | v228 (unsafe) | 396.8 ops/s | 31.7 ms | 44.0 ms | 1.4% | 0 | 0 |
| overlapping_vacua | rc4f | 34.9 iterations/s | — | — | — | 0 | 0 |
| overlapping_vacua | **current** | **166.0 iterations/s** | — | — | — | 0 | 0 |
| overlapping_vacua | v228 (unsafe) | 57.5 iterations/s | — | — | — | 0 | 0 |

Ratios: mass_delete **2.77×**, overlapping_vacua **4.75×**, single_link_churn
**0.75×** (current vs rc4f). No deadlock was observed in any variant, which is
consistent with the documented `lock_timeout`-bounded probe but is not evidence
that deadlocks are impossible; the ordering guarantee is a design property, not
an empirical one.

Two observations worth keeping:

1. The unsafe body is **slower** than the shipped statement-level body on both
   multi-link workloads. The expensive part of the old trigger was the per-row
   invocation, not the row lock. Removing the lock would buy nothing and lose
   data.
2. The protection is genuinely expensive only for delete-per-link statements
   (refresh), where the shipped form cannot be improved by batching because each
   statement deletes exactly one row. That path is opt-in
   (`refresh_content: false` by default) and is already serialised by the
   `crowler:fenced-content-refresh` advisory lock, so its 25% regression does not
   affect default operation.

---

## 5. Severity assessment

| Property | Assessment |
| --- | --- |
| Data integrity | RC2 guarantee preserved; re-proved per family by `TestPostgresSharedArtifactConcurrentInsertSurvivesLastLinkDelete`, `…ConcurrentLastLinkRemovals`, `…FinalRemovalDeletesParent`, `…VacuumAndCascadeKeepOtherPages` |
| Orphan accumulation | None: the parent is deleted in the same transaction as its last link |
| Deadlock exposure | Reduced: deterministic ascending parent order inside a statement. Application-level ordering (two separate statements in opposite order) remains the caller's responsibility |
| Normal crawl throughput | Unaffected: no link deletion on the keyword/metatag ingest paths |
| Cleanup throughput | Improved 2.8× (vacuum-shaped) and 4.8× (concurrent vacua) |
| Refresh throughput | ~25% lower for the opt-in single-link delete path |
| Rollout risk | Five `DROP`/`CREATE TRIGGER` pairs take a brief `ACCESS EXCLUSIVE` lock on the link tables; no table rewrite, no schema version change |

Residual risks:

- A statement-level trigger makes PostgreSQL materialise the statement's deleted
  rows in a transition table. The old row-level trigger had no such store. For a
  vacuum of a very large source the transition table holds every deleted link of
  that statement (spilling from `work_mem` to a temporary file when large), and
  the function then reduces it with `SELECT DISTINCT … ORDER BY`. This is a
  bounded, per-statement cost that the same transaction already paid in I/O and
  row locks, but it is new memory/disk pressure that must be watched on
  multi-million-link sources; batching a very large `VacuumSource` by page range
  is the mitigation if it ever matters.
- Application-level deadlocks between *separate* statements issued in opposite
  orders are still possible; the trigger cannot order work it does not see.
- The `lock_wait_ratio` metric is a sampled `pg_stat_activity` proxy, not an
  exact per-lock wait time. `pg_stat_statements`/`log_lock_waits` would be
  needed for that and were not available.
