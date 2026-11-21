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

The migration file is **one transaction**: `BEGIN` near the top and `COMMIT` at
the end. That has two consequences which the rest of this section depends on:

- every table lock the file takes is held until that single commit (or until the
  rollback), **not** only for the statement that took it;
- `lock_timeout` bounds the **wait to acquire** each lock. It does not shorten a
  lock that is already held, and it does not bound the total run time.

### 3.1 What is locked, and for how long

Measured on disposable PostgreSQL 17; the commands and results are in
`RELEASE_230_RC5F_MIGRATION_RUNBOOK_VALIDATION.md`.

| Statement in the migration | Strongest lock | Held until | Blocks |
| --- | --- | --- | --- |
| `LOCK TABLE DBSchemaVersion IN EXCLUSIVE MODE` | `EXCLUSIVE` on `DBSchemaVersion` | commit | writes; plain reads still allowed |
| `ALTER TABLE InformationSeed ADD COLUMN …` | **`ACCESS EXCLUSIVE`** on `InformationSeed` | commit | reads **and** writes on `InformationSeed` |
| `CREATE INDEX …` when it actually builds the index | `SHARE` | commit | writes; reads still allowed |
| `DROP TRIGGER … ON <link table>` | **`ACCESS EXCLUSIVE`** on that link table | commit | reads **and** writes |
| `CREATE TRIGGER …` | `SHARE ROW EXCLUSIVE` | commit | writes |

The trigger replacements are near the end of the file, but the
`ALTER TABLE InformationSeed ADD COLUMN` statements earlier in the same
transaction already hold `ACCESS EXCLUSIVE` on `InformationSeed` for the whole
migration. "Metadata-only, no table rewrite" is therefore **not** the same as
"no availability impact": those tables are blocked for as long as the transaction
stays open, and anything added after the trigger block would extend that window.

Measured facts (disposable PostgreSQL 17):

- while a transaction holds the trigger `DROP`/`CREATE`, another session's
  `SELECT` on that link table **and** its `INSERT` both fail with SQLSTATE
  `55P03` under `lock_timeout = '1s'`; both succeed after the holder commits;
- the same holds for `InformationSeed` while the `ALTER TABLE` is uncommitted.

### 3.2 Procedure

1. Take a backup (`pg_dump --format=custom`).
2. Look for long-running transactions before starting, and reserve a quiet
   window. A migration that queues behind an open transaction holds its own
   locks for the whole wait.

   ```sql
   SELECT pid, state, now() - xact_start AS xact_age,
          left(regexp_replace(query, '\s+', ' ', 'g'), 100) AS query_prefix
   FROM pg_stat_activity
   WHERE datname = current_database()
     AND xact_start IS NOT NULL
     AND pid <> pg_backend_pid()
   ORDER BY xact_age DESC;
   ```

3. Bound lock **acquisition** on the connection that actually runs the
   migration. `lock_timeout` is session-local, so it must be set for that
   session; a `SET lock_timeout` executed earlier, in another `psql` invocation,
   has no effect on the migration.

   ```bash
   PGOPTIONS='-c lock_timeout=5s' \
   psql -X -v ON_ERROR_STOP=1 \
     -v CROWLER_DB_USER="$CROWLER_DB_USER" \
     -f pkg/database/db_migrations/postgresql-migration-v1.17.pgsql
   ```

   - `PGOPTIONS` is read by libpq when this `psql` process connects, and the
     settings are applied to **that** PostgreSQL session.
   - `-X` ignores any `~/.psqlrc`, so a local alias or setting cannot silently
     change the migration environment.
   - `ON_ERROR_STOP=1` makes any SQL error a non-zero `psql` exit.
   - Connection parameters come from the standard libpq environment
     (`PGHOST`, `PGPORT`, `PGUSER`, `PGDATABASE`, `PGPASSWORD`) or `~/.pgpass`.
     Do not put credentials on the command line.

   Confirm the setting reaches a session before you rely on it:

   ```bash
   PGOPTIONS='-c lock_timeout=5s' psql -X -At -c 'SHOW lock_timeout;'
   # 5s
   ```

4. *(Optional)* A total bound. `lock_timeout` cannot stop a migration that keeps
   holding locks after acquiring them; only the transaction ending does that. If
   your change policy requires a wall-clock ceiling, `statement_timeout` is the
   available knob, but be aware of its scope:

   - it aborts a **single statement** that runs longer than the value, not the
     transaction as a whole, and it does not abort an idle transaction (use
     `idle_in_transaction_session_timeout` for that);
   - a legitimate `CREATE INDEX` on a large table can exceed a small value, so a
     value below several minutes would abort a healthy migration;
   - when it fires, the migration transaction rolls back exactly like a
     `lock_timeout` failure.

   Default recommendation: leave `statement_timeout` unset (`0`) for this
   migration and rely on `lock_timeout` plus the quiet window. If your operator
   policy requires one, set it well above the expected duration and record why.

5. If `lock_timeout` fires, the migration transaction rolls back and the exit
   code is non-zero (`3` from `psql` with `ON_ERROR_STOP`). Nothing is
   half-applied: the whole file runs in one transaction. Verified on a
   disposable database: a blocked migration exited `3` after ~6 s and left the
   function body, the trigger shape and both markers unchanged. Retry in a
   quieter window.

6. Verify:

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

### 6.1 Revert to the RC4F trigger (supported shape, no shipped script)

**No executable rollback artifact ships in this repository.** What follows is a
*template*, not a ready-to-run script: it has no function body and no trigger
definitions of its own.

Prerequisites:

1. obtain the RC4F release's `pkg/database/postgresql-setup.pgsql` (the commit
   that shipped the `FOR EACH ROW` triggers) and extract from it:
   the `handle_shared_entity_deletion()` body and the five
   `trg_after_delete_*` definitions;
2. rehearse the assembled script on a disposable copy of the database, then
   verify the resulting shape with the queries in §3.2 step 6;
3. only then apply it to the deployment.

The same-session timeout rule applies to this DDL too, and the rollback is also
one transaction, so its `ACCESS EXCLUSIVE` locks on the five link tables are held
until it commits:

```bash
PGOPTIONS='-c lock_timeout=5s' psql -X -v ON_ERROR_STOP=1 <<'SQL'
BEGIN;
-- CREATE OR REPLACE FUNCTION handle_shared_entity_deletion() ... ;   (RC4F body)
-- DROP TRIGGER + CREATE TRIGGER ... FOR EACH ROW ... ;                 (x5)
COMMIT;
SQL
```

Do **not** paste the two comment lines as if they were the rollback. The template
is only useful once the RC4F definitions are inserted and validated. A disposable
rehearsal of the assembled script (RC5 → RC4F) took ~0.85 s, put the five link
triggers back to `FOR EACH ROW` and restored the RC4F function digest; re-applying
the v1.17 migration returned the database to the RC5 shape. Details are in
`RELEASE_230_RC5F_MIGRATION_RUNBOOK_VALIDATION.md`.

Semantics of the rollback are the RC2-protected ones, so it is safe; the cost is
the measured 2.8×–4.8× cleanup penalty described in
`RELEASE_230_RC5_LOCK_ANALYSIS.md`.

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
