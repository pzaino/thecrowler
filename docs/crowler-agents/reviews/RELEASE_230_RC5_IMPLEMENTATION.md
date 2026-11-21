# Release 2.3.0 RC5 — implementation

Work package: `WP-REL230-RC5`
Baseline: `906000f3871c988e24a65ee32e1ec452491357d6`
Release: `v2.3.0` · Schema `1.17` · Writer `verified-ownership-v1.17`

This package changes one PostgreSQL trigger function and its five trigger
definitions, and adds the tests, benchmark and documentation that justify the
change. It adds no schema version, no public configuration, no API, no
background subsystem and no application-level behaviour change.

## 1. What changed

`handle_shared_entity_deletion()` now runs once per `DELETE` statement instead of
once per deleted row:

| Before (RC4F) | After (RC5) |
| --- | --- |
| `AFTER DELETE … FOR EACH ROW` on each link table | `AFTER DELETE … FOR EACH STATEMENT` with `REFERENCING OLD TABLE AS deleted_links` |
| parent id taken from `OLD.<column>` | parent ids taken from `SELECT DISTINCT <column> FROM deleted_links ORDER BY <column>` |
| one `FOR UPDATE` + count per deleted link | one `FOR UPDATE` + re-check per distinct parent |
| lock order determined by the deletion plan | ascending parent-id order |
| `COUNT(*) = 0` re-check | `NOT EXISTS (…)` re-check |

Unchanged, deliberately:

- the `FOR UPDATE` parent lock **before** the re-check, which conflicts with the
  `FOR KEY SHARE` lock a concurrent link insert needs (the RC2 integrity fix);
- the parent is deleted only when no link remains, inside the same transaction;
- the function name, signature, trigger names and the five affected families;
- schema `1.17`, writer generation `verified-ownership-v1.17`, grants and
  privileges.

`NOT EXISTS` is used instead of `COUNT(*)` because it is index-only and stops at
the first reference, which matters on a hot parent. Both are evaluated after the
row lock is granted, so they are equivalent for correctness.

Cost profile, stated plainly: the row-level form did no per-statement buffering
but repeated the lock and re-check for every deleted link. The statement-level
form adds a transition table for the deleted rows of each statement and does one
lock plus one re-check per distinct parent. For the common case (a handful of
links per statement) the difference is negligible; for a large vacuum the saving
is large but the transition table is a new, bounded temporary-memory cost. Both
are documented in `RELEASE_230_RC5_LOCK_ANALYSIS.md` and
`RELEASE_230_RC5_DEPLOYMENT_MONITORING.md`.

## 2. Why not a cheaper shortcut

`RELEASE_230_RC5_LOCK_ANALYSIS.md` records the rejected candidates. Two are worth
repeating because they look like improvements:

- **Single-statement `DELETE … AND NOT EXISTS`** — measured to lose a
  concurrently committed link
  (`TestPostgresSharedArtifactSingleStatementDeleteIsUnsafe`). PostgreSQL
  evaluates the qualification before waiting for the row lock, and a concurrent
  child insert does not create a new parent tuple version, so nothing is
  re-evaluated.
- **Fast existence check before the lock** — two racing last-link removals each
  observe the other's link, both skip, and the parent is orphaned. The
  deterministic `TestPostgresSharedArtifactConcurrentLastLinkRemovals` covers
  exactly that interleaving.

The chosen change is the only candidate that keeps the RC2 guarantee *and*
removes work, because it reduces the number of lock/re-check cycles without
moving the re-check outside the lock.

## 3. SQL changes

`pkg/database/postgresql-setup.pgsql` (fresh installs) and
`pkg/database/db_migrations/postgresql-migration-v1.17.pgsql` (v1.16 → 1.17 and
repeatable re-run) carry the identical function body and the identical five
trigger definitions.

The trigger section replaces conditional creation with unconditional replacement:

```sql
DROP TRIGGER IF EXISTS trg_after_delete_keywordindex ON KeywordIndex;
CREATE TRIGGER trg_after_delete_keywordindex
AFTER DELETE ON KeywordIndex
REFERENCING OLD TABLE AS deleted_links
FOR EACH STATEMENT
EXECUTE FUNCTION handle_shared_entity_deletion();
```

`DROP` + `CREATE` is what makes a *repeatable* migration converge: a database
that already ran the previous migration owns a row-level trigger, and a
`CREATE TRIGGER … IF NOT EXISTS`-style guard would silently keep it. All five
statements are idempotent and safe to re-run.

### Deployment cost

`CREATE TRIGGER` / `DROP TRIGGER` take a brief `ACCESS EXCLUSIVE` lock on the
link table. There is no table rewrite and no data movement. Five tables are
touched for milliseconds each. Rollout prerequisites and the rollback path are in
`RELEASE_230_RC5_DEPLOYMENT_MONITORING.md`.

## 4. Tests added

New files, all opt-in and disposable-database only:

| File | Purpose |
| --- | --- |
| `pkg/database/shared_artifact_bench_postgres_integration_test.go` | reproducible contention benchmark; compares the frozen RC4F body, the shipped body and the pre-RC2 characterization body |
| `pkg/database/shared_artifact_integrity_postgres_integration_test.go` | family-generic integrity proof on the real schema (cases A–G), plus migration convergence and two data-loss characterizations |

Changed:

| File | Change |
| --- | --- |
| `pkg/database/schema_sql_test.go` | `TestPostgresSharedEntityDeletionConcurrencyContract` now pins the statement-level shape, the transition relation, the ascending parent order, the identification of every family and the `DROP`/`CREATE` convergence, in addition to the RC2 `FOR UPDATE`-before-count rule |
| `pkg/crawler/fenced_refresh_concurrency_postgres_integration_test.go` | comment corrected: the orphan trigger fires once per statement, not per row (no assertion changed) |

The benchmark reads the `current` variant out of `postgresql-setup.pgsql` rather
than carrying a copy, so it always measures the shipped SQL.

## 5. Compatibility

- Behaviour of `VacuumSource`, source/page deletion, refresh, the events janitor
  and the post-commit keyword/metatag phases is unchanged.
- The trigger contract an application could observe (parent deleted only when
  unreferenced) is unchanged.
- Schema version, writer generation, grants, table definitions, indexes and
  migrations' names are unchanged.
- MySQL and SQLite are untouched. SQLite never had this stored function.
- Serialised output of `pg_dump` differs only in the function body and trigger
  definitions, which is expected.

## 6. Explicitly not done

Ownership-epoch DDL, asynchronous goal resumption, MCP mutation dispatch, fenced
`null:` deletion, schema 1.18, new APIs, replacement of the global content-refresh
advisory lock, browser error-page heuristics and any application-level retry or
ordering change. The v2.2.8 pre-RC2 trigger was not restored, and the break-glass
rollback file was neither created nor applied.
