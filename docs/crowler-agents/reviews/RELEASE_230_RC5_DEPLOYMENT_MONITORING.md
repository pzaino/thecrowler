# Release 2.3.0 RC5 — deployment and production monitoring

Work package: `WP-REL230-RC5`
Applies to: schema `1.17`, writer generation `verified-ownership-v1.17`
Audience: the operator who deploys the RC5 build

Nothing here authorises a production deployment or migration. It records the
procedure, the evidence to collect and the rollback choices so the maintainer can
decide with data.

## 1. What the deployment changes

One PL/pgSQL function body and five trigger definitions on the link tables
(`KeywordIndex`, `MetaTagsIndex`, `WebObjectsIndex`, `NetInfoIndex`,
`HTTPInfoIndex`). No table, column, index, grant or schema marker changes.

The migration file is the same `db_migrations/postgresql-migration-v1.17.pgsql`
that already ships with 1.17; it is repeatable, and applying it again to a 1.17
database converges the trigger definitions.

## 2. Pre-deployment checks

```sql
-- must be 1.17 / verified-ownership-v1.17 before and after
SELECT version FROM DBSchemaVersion WHERE is_current;
SELECT writer_generation FROM DatabaseWriterCompatibility WHERE lock_id = 1;

-- current shape (expect five row-level triggers before the change)
SELECT tgname, (tgtype & 1) = 1 AS row_level, tgoldtable
FROM pg_trigger
WHERE NOT tgisinternal AND tgname LIKE 'trg_after_delete_%index'
ORDER BY tgname;

-- current function
SELECT md5(prosrc) FROM pg_proc WHERE proname = 'handle_shared_entity_deletion';
```

Record these values. `md5(prosrc)` is the cheapest reliable fingerprint.

## 3. Applying

1. Take a backup (`pg_dump --format=custom`).
2. Prefer a low-traffic window. The trigger replacement needs
   `ACCESS EXCLUSIVE` on each of the five link tables for the duration of a
   `DROP`/`CREATE` pair; this is a metadata operation with no table rewrite, but
   it does block reads and writes on that table while it holds the lock.
3. Bound the wait so the migration cannot queue indefinitely behind a long
   transaction, and fail rather than hang:

   ```sql
   SET lock_timeout = '5s';
   ```

4. Apply the migration with the same variables the existing procedure uses:

   ```bash
   psql -v ON_ERROR_STOP=1 -v CROWLER_DB_USER=crowler \
     -f pkg/database/db_migrations/postgresql-migration-v1.17.pgsql
   ```

   If `lock_timeout` fires, nothing is half-applied: the migration runs in a
   transaction and rolls back. Retry in a quieter window.

5. Verify:

```sql
SELECT version FROM DBSchemaVersion WHERE is_current;               -- 1.17
SELECT writer_generation FROM DatabaseWriterCompatibility WHERE lock_id = 1;

SELECT tgname, (tgtype & 1) = 1 AS row_level, tgoldtable
FROM pg_trigger
WHERE NOT tgisinternal AND tgname LIKE 'trg_after_delete_%index'
ORDER BY tgname;
-- five rows: row_level = false, tgoldtable = deleted_links

SELECT md5(prosrc) FROM pg_proc WHERE proname = 'handle_shared_entity_deletion';
-- must equal the value produced by a fresh install of the same build
```

`TestPostgresSharedArtifactTriggerDefinitionConverges` performs this exact
convergence rehearsal (row-level → statement-level → correct body) against a
disposable database.

## 4. Production monitoring

The point of this section is to distinguish "the workload changed" from "the
trigger change hurt". Use a comparison window: at least one full crawl cycle
before and after, at matched crawler concurrency and source mix.

### 4.1 Application-level (existing metrics)

| Signal | Why |
| --- | --- |
| source completions and failures per minute/hour | detects a cleanup stall surfacing as a stuck source |
| indexed pages per minute | detects crawling throughput regression |
| database transaction rate, p95/p99 latency | detects lock-driven latency growth |
| connection-pool wait count and duration | separates pool exhaustion from row-lock waits |

If a metric is not exported by the deployed build, say so in the change record
instead of substituting a proxy.

### 4.2 PostgreSQL, deadlocks and waits

```sql
-- deadlock counter (cumulative; compare deltas, not totals)
SELECT datname, deadlocks FROM pg_stat_database WHERE datname = current_database();

-- currently blocked sessions and their blockers (read-only)
SELECT a.pid,
       a.wait_event_type,
       a.wait_event,
       now() - a.query_start AS query_age,
       pg_blocking_pids(a.pid) AS blockers,
       left(regexp_replace(a.query, '\s+', ' ', 'g'), 120) AS query_prefix
FROM pg_stat_activity a
WHERE a.datname = current_database()
  AND a.pid <> pg_backend_pid()
  AND a.state <> 'idle'
ORDER BY query_age DESC;
```

`pg_blocking_pids` is available on every supported PostgreSQL version and does
not require a superuser. The query text is truncated deliberately: crawl SQL can
carry URLs and content. Do not raise `track_activity_query_size` or enable
`log_lock_waits`/`log_statement` without a separate decision; those settings
change log volume and may capture user data.

### 4.3 Cleanup work

```sql
-- link rows per family (cheap on the parent-id indexes)
SELECT 'keyword' AS family, count(*) FROM KeywordIndex
UNION ALL SELECT 'metatag', count(*) FROM MetaTagsIndex
UNION ALL SELECT 'webobject', count(*) FROM WebObjectsIndex
UNION ALL SELECT 'netinfo', count(*) FROM NetInfoIndex
UNION ALL SELECT 'httpinfo', count(*) FROM HTTPInfoIndex;

-- parents that lost all references but still exist (integrity invariant)
SELECT 'keyword' AS family, count(*) FROM Keywords k
 WHERE NOT EXISTS (SELECT 1 FROM KeywordIndex i WHERE i.keyword_id = k.keyword_id)
UNION ALL
SELECT 'metatag', count(*) FROM MetaTags m
 WHERE NOT EXISTS (SELECT 1 FROM MetaTagsIndex i WHERE i.metatag_id = m.metatag_id)
UNION ALL
SELECT 'webobject', count(*) FROM WebObjects w
 WHERE NOT EXISTS (SELECT 1 FROM WebObjectsIndex i WHERE i.object_id = w.object_id)
UNION ALL
SELECT 'netinfo', count(*) FROM NetInfo n
 WHERE NOT EXISTS (SELECT 1 FROM NetInfoIndex i WHERE i.netinfo_id = n.netinfo_id)
UNION ALL
SELECT 'httpinfo', count(*) FROM HTTPInfo h
 WHERE NOT EXISTS (SELECT 1 FROM HTTPInfoIndex i WHERE i.httpinfo_id = h.httpinfo_id);
```

The second query is the orphan check. Non-zero is not automatically a fault: the
RC2 design deletes parents synchronously, so a stable non-zero count would mean
either a row is referenced by another table, or cleanup is being skipped. Record
the value before and after, and treat **growth proportional to vacuum/refresh
activity** as a regression signal. (The events janitor also creates short-lived
unreferenced `WebObjects` until its next pass; do not measure immediately after a
crawl.)

## 5. Proposed thresholds (not production guarantees)

Roll back the trigger change (not the release) if any of these holds in the
post-change window, at matched load:

| Condition | Proposed threshold |
| --- | --- |
| New deadlocks attributable to cleanup | any sustained increase over the pre-change baseline across two windows |
| Cleanup duration | a `VacuumSource` for a comparable source takes > 2× its pre-change duration |
| Crawl throughput | indexed pages/minute falls > 10% with unchanged source mix and concurrency |
| Row-lock wait | the fraction of active backends waiting on `Lock` stays materially above the pre-change sample for a full cycle |
| Integrity | the orphan query grows in proportion to vacuum/refresh activity, or a duplicate/lost link is reported |

These are starting points for the maintainer, chosen to be visible before they
are severe. They are not measured production values.

One RC5-specific operational note: a statement-level trigger materialises the
statement's deleted rows in a transition table. If a single `VacuumSource` covers
a very large source, that statement now has a temporary tuplestore proportional
to its deleted links. Watch `pg_stat_database.temp_bytes` /
`log_temp_files` (if enabled) around large vacua, and batch the vacuum by page
range if it becomes significant. It is not a correctness risk.

## 6. Rollback

Two distinct operations, do not confuse them.

### 6.1 Revert to the RC4F trigger (supported, safe)

Re-apply the previous function body and the row-level trigger definitions from the
RC4F release's `postgresql-setup.pgsql`. Semantics are the RC2-protected ones, so
this is a safe rollback; the cost is the measured 2.8×–4.8× cleanup penalty
described in `RELEASE_230_RC5_LOCK_ANALYSIS.md`.

```sql
SET lock_timeout = '5s';
-- from the RC4F release:
--   1. CREATE OR REPLACE the RC4F function body
--   2. DROP + CREATE the five triggers FOR EACH ROW
```

Verify afterwards with the same function digest and trigger-shape queries as
§3.

### 6.2 The pre-RC2 / v2.2.8 body is **not** a rollback

The pre-RC2 trigger (count, then delete, no parent lock) has a reproducible
silent data-loss race: a link committed while the trigger waits is cascaded away.
It must not be used as an ordinary rollback, and the emergency
`revert_shared_entity_trigger_to_v2.2.8.sql` variant is a break-glass trade-off
that requires an explicit incident decision and a recorded integrity risk. This
package neither creates nor applies it.

### 6.3 Application rollback

The trigger change is independent of the service binaries. Rolling back the
application images without touching the database is safe: both the RC4F and RC5
function bodies are compatible with all 2.3.0 services.

## 7. What cannot be verified from here

- No production telemetry was available, and none was assumed. The 24-hour
  "zero deadlocks" observation carried from earlier reviews is **not** evidence
  that no waits occur; use §4 deltas.
- `pg_stat_statements`, `log_lock_waits` and server-side statement logging were
  not enabled; enabling them requires the database administrator's authorization
  and a data-handling review.
- Deployment window timing depends on the environment's write rate and longest
  open transaction; it cannot be predicted from this repository.
