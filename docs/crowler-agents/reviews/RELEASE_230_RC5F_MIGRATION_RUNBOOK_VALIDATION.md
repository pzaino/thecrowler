# Release 2.3.0 RC5F — migration runbook validation

Work package: `WP-REL230-RC5F`
Baseline: `2e054c44f8ab21ca6e643fc6b6f4d97d9f276119` (`develop`, clean)
Worktree: `/tmp/opencode/thecrowler-rc5f`
Release: `v2.3.0` · Schema `1.17` · Writer `verified-ownership-v1.17`
Date: 2026-11-21

This package corrects one operational instruction and one explanation in
`RELEASE_230_RC5_DEPLOYMENT_MONITORING.md`, and demonstrates the corrected
semantics on disposable PostgreSQL 17. It changes no SQL, no trigger, no schema
version, no application code, no CI workflow and no version identifier.

## 1. What was wrong

Two statements in the RC5 runbook were unsafe to follow literally.

### 1.1 A detached `SET lock_timeout` controls the wrong session

§3 previously said:

```sql
SET lock_timeout = '5s';
```

and then, as a separate step, launched:

```bash
psql -v ON_ERROR_STOP=1 -v CROWLER_DB_USER=crowler \
  -f pkg/database/db_migrations/postgresql-migration-v1.17.pgsql
```

`SET` is session-local. Executed on its own, it configured a `psql` session that
exited immediately; the migration then ran on a **different** connection with
`lock_timeout = 0` (wait forever). The advertised 5-second bound did not exist.

### 1.2 "For the duration of a DROP/CREATE pair" understated the window

§3 also described the trigger replacement as holding `ACCESS EXCLUSIVE`
"for the duration of a `DROP`/`CREATE` pair". PostgreSQL releases table locks at
**transaction end**, not statement end. The migration is a single transaction, so
those locks are held until its `COMMIT` or rollback — and every other lock the
transaction takes is held equally long.

## 2. Correction

§3 now:

- states that the file is one transaction, so locks are held to commit;
- documents which statements take which locks and what they block, per
  statement class, in a table;
- replaces the detached `SET` with a single invocation that sets the timeout on
  the connection that runs the migration:

  ```bash
  PGOPTIONS='-c lock_timeout=5s' \
  psql -X -v ON_ERROR_STOP=1 \
    -v CROWLER_DB_USER="$CROWLER_DB_USER" \
    -f pkg/database/db_migrations/postgresql-migration-v1.17.pgsql
  ```

  with the reasons: `PGOPTIONS` is applied by libpq to that session, `-X` ignores
  `~/.psqlrc`, `ON_ERROR_STOP=1` gives a non-zero exit, and connection
  parameters come from the standard libpq environment rather than the command
  line;
- adds a read-only propagation check
  (`PGOPTIONS='-c lock_timeout=5s' psql -X -At -c 'SHOW lock_timeout;'` → `5s`);
- explains that `lock_timeout` bounds **acquisition only**, and offers
  `statement_timeout` as an optional, independently justified per-statement
  ceiling — with its scope, its interaction with a long legitimate `CREATE INDEX`
  and its rollback behaviour spelled out, and a recommendation to leave it unset
  for this migration;
- records the measured failure mode (exit `3`, full rollback, unchanged state).

§6 now states that the same-session rule applies to rollback DDL, that no
executable rollback script ships, and that the shown block is a **template**
requiring the RC4F definitions to be extracted and rehearsed first.

`RELEASE_230_PRE_RELEASE_RUNBOOK.md` was cross-referenced: the migration
rehearsal step shows the bounded single-session invocation, and the deployment
rollback note points at §3/§6.

## 3. Lock wait vs lock hold

| | Lock **wait** | Lock **hold** |
| --- | --- | --- |
| What it is | time spent queuing for a lock a statement needs | time a granted lock is retained |
| Bounded by | `lock_timeout` (fails with SQLSTATE `55P03`) | the end of the enclosing transaction |
| In this migration | each lock acquisition, individually | from the statement that took it to `COMMIT`/rollback |
| Practical effect | a blocked migration gives up after 5 s instead of hanging | blocked readers/writers on the affected tables stay blocked while the migration is still working |

A short acquisition time therefore does **not** imply a short blocking window.

## 4. What the migration actually does

Reviewed in full (`pkg/database/db_migrations/postgresql-migration-v1.17.pgsql`,
1,080 lines):

- `BEGIN` at line 8 and `COMMIT` at line 1080: one transaction.
- `LOCK TABLE DBSchemaVersion IN EXCLUSIVE MODE` immediately after `BEGIN`.
- Creates `SourceOwnership`, `CrawlApprovals`, `CrawlProposals`, the eight
  `AgentGoalRun*` tables, `SchemaCapabilities`, `SourceCrawlAttempts`,
  `SourceCrawlClaims`, `SourceCrawlClaimPolicy` (`CREATE TABLE IF NOT EXISTS`),
  plus indexes, owners and grants.
- `ALTER TABLE InformationSeed ADD COLUMN IF NOT EXISTS …` for five columns and
  three indexes on that table.
- Replaces `update_informationseed(...)`, `set_source_crawl_claim_mode(...)`,
  `update_sources(...)` and `handle_shared_entity_deletion()`.
- Replaces the five link-table triggers (lines 1034-1067) — the RC5 change.
- Updates `DBSchemaVersion`, `DatabaseWriterCompatibility` and the unique-current
  index, then commits.

Consequence for the operator: the trigger DDL is near the end, but the
`ALTER TABLE InformationSeed` statements earlier in the same transaction already
hold `ACCESS EXCLUSIVE` on `InformationSeed` until commit. The migration blocks
that table (reads and writes) for its whole duration, not only the five link
tables at the end.

## 5. Disposable PostgreSQL 17 verification

Environment: `postgres:17` container `rc5f-pg` on `127.0.0.1:55987`, fresh
`SitesIndex` created by `pkg/database/postgresql-setup.sh`. No persistent,
shared, staging or production database was contacted.

Pre-migration state was simulated by restoring the RC4F definitions from commit
`906000f3` (the commit RC5 builds on):

```text
fn=6d33f3492e828ea1334de93baeba5c95   (RC4F, FOR EACH ROW body)
row-level link triggers = 6           (5 link triggers + unrelated source_searchindex)
schema=1.17  writer=verified-ownership-v1.17
```

### Test A — the timeout reaches the migration connection

```bash
PGOPTIONS='-c lock_timeout=5s' psql -X -At -c 'SHOW lock_timeout;'
PGOPTIONS='-c lock_timeout=5s' psql -X -At -c "select current_setting('lock_timeout');"
psql -X -At -c 'show lock_timeout;'
```

```text
5s
5s
0          <- without PGOPTIONS
```

`SHOW` proves the option is applied to that session. Test B proves it is applied
to the actual migration, because the migration is what times out.

### Test B — a blocked migration fails safely

Session A held `LOCK TABLE KeywordIndex IN ACCESS EXCLUSIVE MODE` inside an open
transaction. Session B ran the documented command:

```bash
PGOPTIONS='-c lock_timeout=5s' psql -X -v ON_ERROR_STOP=1 \
  -v CROWLER_DB_USER=crowler \
  -f pkg/database/db_migrations/postgresql-migration-v1.17.pgsql
```

```text
exit=3   elapsed=5991 ms
CREATE TRIGGER psql:pkg/database/db_migrations/postgresql-migration-v1.17.pgsql:1048:
  ERROR: canceling statement due to lock timeout
```

The SQLSTATE was confirmed on the same blocked scenario with
`\set VERBOSITY verbose`: `ERROR: 55P03: canceling statement due to lock timeout`.

State after the failed migration (must be unchanged — and was):

```text
fn=6d33f3492e828ea1334de93baeba5c95
row-level triggers on the link-trigger pattern = 6   (5 link tables + unrelated source_searchindex)
statement-level triggers on that pattern        = 0
schema=1.17  writer=verified-ownership-v1.17
```

No mixture of row-level and statement-level triggers, no partial function body,
no marker drift.

### Test C — DDL locks persist to commit

Session A, inside one transaction:

```sql
BEGIN;
DROP TRIGGER IF EXISTS trg_after_delete_keywordindex ON KeywordIndex;
CREATE TRIGGER trg_after_delete_keywordindex
AFTER DELETE ON KeywordIndex
REFERENCING OLD TABLE AS deleted_links
FOR EACH STATEMENT
EXECUTE FUNCTION handle_shared_entity_deletion();
SELECT pg_sleep(20);   -- hold the transaction open
COMMIT;
```

Lock modes observed on `KeywordIndex` from `pg_locks` while the transaction was
open:

```text
AccessExclusiveLock      <- DROP TRIGGER
AccessShareLock          <- catalog lookup
ShareRowExclusiveLock    <- CREATE TRIGGER
```

Conflicting sessions with `lock_timeout = '1s'`:

```text
SELECT count(*) FROM KeywordIndex;                      -> ERROR 55P03
INSERT INTO KeywordIndex (keyword_id, index_id) VALUES … -> ERROR 55P03
```

After session A committed, the same `SELECT` returned normally. This is the
statement-level lock behaviour the corrected runbook describes: reads are
blocked too, and release happens at commit, not at the end of the `CREATE`.

The same probe was repeated for `ALTER TABLE InformationSeed ADD COLUMN IF NOT
EXISTS claim_token VARCHAR(64)`:

```text
lock mode on InformationSeed = AccessExclusiveLock
SELECT count(*) FROM InformationSeed;  -> ERROR 55P03 (while uncommitted)
```

### Test D — successful and repeatable migration

With the blocker released:

| Pass | Exit | Elapsed |
| --- | --- | --- |
| 1 | 0 | 1,029 ms |
| 2 (repeat) | 0 | 964 ms |

Verification after both passes:

```text
schema                    = 1.17
writer                    = verified-ownership-v1.17
fn(prosrc)                = 5bdd88efd2e200043c2fb1502be5ba3b
statement-level triggers  = 5
row-level link triggers   = 1   (trg_after_delete_source_searchindex, unrelated)
transition relations      = deleted_links on all five link triggers
fixture source row        = preserved
```

`5bdd88ef…` is the same digest a fresh RC5 setup produces, so fresh installs and
upgraded databases converge.

### Rollback rehearsal (disposable)

The template in §6.1 was assembled from the RC4F definitions and executed as a
single transaction on the disposable database:

```text
exit=0  elapsed=845 ms
fn=6d33f3492e828ea1334de93baeba5c95
row-level triggers on the link-trigger pattern = 6   (5 link tables + unrelated source_searchindex)
statement-level triggers on that pattern        = 0
```

Re-applying the v1.17 migration returned the database to `5bdd88ef…` with five
statement triggers, so the rollback is reversible. This was a rehearsal of a
template, not a shipped rollback artifact, and it was executed only on disposable
PostgreSQL.

### Focused regressions

```text
go test ./pkg/database -run '^TestPostgresSharedEntityDeletionConcurrencyContract$'   ok
THECROWLER_POSTGRES_INTEGRATION=1 go test -tags=integration ./pkg/database \
  -run '^TestPostgresSharedArtifact' -skip '…ContentionBenchmark$'                   ok (22.8 s)
```

## 6. Documentation checks

- `git diff --check`: pass.
- Markdown fences: balanced; the corrected command blocks were copy-pasted into a
  shell and executed for tests A–D.
- No SQL, Go, CI, schema-version or writer-generation change is part of this
  package.
- The previous RC5 evidence (benchmark, Docker suite, Information Seed) is
  unchanged and remains the historical record for the code; it was not re-run for
  a documentation-only change.

## 7. Not run

| Item | Status | Reason |
| --- | --- | --- |
| Production or shared database migration | **NOT RUN** | Forbidden by the work package |
| Hosted CI on the RC5F docs SHA | **NOT RUN** | Patch not pushed |
| Full RC5 benchmark / Docker stack / Information Seed suite | **NOT RUN** | Documentation-only change; RC5 evidence retained |
| `statement_timeout` value selection | **NOT RUN** | Environment-specific; the runbook recommends leaving it unset and explains how to justify it |

## 8. Production state

Unchanged by this package: schema `1.17`, writer
`verified-ownership-v1.17`, the `handle_shared_entity_deletion()` body, its five
triggers, and all application code. The only deliverables are the corrected
runbook, the cross-reference in the pre-release runbook, and this record.
