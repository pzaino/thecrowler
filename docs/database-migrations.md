# Database migrations

This is the operator guide for upgrading a CROWler database to the schema used by
release **2.3.0** or **2.3.1**, or for checking that an existing database
matches it. Release 2.3.1 keeps the same schema and writer generation; it adds
opt-in time-series retention and does not require a migration.

- **Schema version:** `1.17`
- **Writer generation:** `verified-ownership-v1.17`
- **Primary engine:** PostgreSQL 17 (PostgreSQL 15.10 is also used by the
  default Compose generator)
- **Other engines:** SQLite is supported. MySQL/MariaDB SQL is shipped but the
  runtime backend is **disabled** in 2.3.x (see §5).

The migration files themselves carry a short header with their engine-specific
warnings. This guide is the complete procedure.

---

## 1. Overview

A CROWler database has two version markers:

| Marker | Table | 2.3.x value |
| --- | --- | --- |
| Schema version | `DBSchemaVersion.version` where `is_current` | `1.17` |
| Writer generation | `DatabaseWriterCompatibility.writer_generation` | `verified-ownership-v1.17` |

The application refuses to start when either marker is wrong
(`CheckStartupCompatibility`), and prints the detected and required values.

**Fresh install or upgrade?**

- A **fresh** database is created from the setup script (`postgresql-setup.sh`
  or `postgresql-setup.pgsql`, `sqlite-setup.sqlite3`, `mysql-setup.mysql`). It is
  already at 1.17; do not run migrations on it.
- An **existing** database must be upgraded by applying the migration files in
  order. Running only the 1.17 file does **not** upgrade an older database.

### Migration chain

| Engine | Files, in order |
| --- | --- |
| PostgreSQL | `postgresql-migration-v1.08` … `v1.17` (all versions 1.08–1.17 exist) |
| SQLite | `sqlite-migration-v1.08`, `v1.09`, `v1.11`, `v1.13`, `v1.14`, `v1.15`, `v1.16`, `v1.17` |
| MySQL/MariaDB | `mysql-migration-v1.08`, `v1.09`, `v1.11`, `v1.13`, `v1.14`, `v1.15`, `v1.16`, `v1.17` |

MySQL and SQLite ship no 1.10 or 1.12 migration file; apply the files that
exist, in version order. Every file in `pkg/database/db_migrations/` states its
target version in its header.

### Optional 1.17 add-ons

These are **not** part of the base chain. Apply them only when the capability is
missing (typically a development database already stamped 1.17 that predates the
component):

| Add-on | PostgreSQL | SQLite |
| --- | --- | --- |
| Asynchronous agent-goal tables | `postgresql-agent-goal-v1.17.pgsql` | `sqlite-agent-goal-v1.17.sqlite3` |
| MCP mutation proposal/approval tables | `postgresql-mcp-mutation-v1.17.pgsql` | `sqlite-mcp-mutation-v1.17.sqlite3` |
| Fenced crawl-attempt tables | `postgresql-source-crawl-attempt-v1.17.pgsql` | `sqlite-source-crawl-attempt-v1.17.sqlite3` |

On PostgreSQL the MCP capability also has an operator command,
`dbDeploy -check | -apply -backup-dir … | -disable` (see
[`crowler-agents/schema-deployment.md`](crowler-agents/schema-deployment.md)).
`dbDeploy` manages the capability marker; it does not apply the base v1.17
migration.

---

## 2. Before you upgrade

1. **Stop the old writers.** The writer generation changes with 1.17, so old and
   new services must not share the database. Stop the engine, API, Events
   service and any standalone tools before migrating.
2. **Confirm the current version** so you know where you are starting:
   ```sql
   SELECT version FROM DBSchemaVersion WHERE is_current;
   ```
   If it is below 1.16, apply every intermediate migration first (§1).
3. **Check the account.** You need a role that can create and alter objects and
   grant to the CROWler runtime role: normally the database owner, or a
   superuser. The `:CROWLER_DB_USER` role (PostgreSQL) must already exist; the
   migration does not create roles.
4. **Take a backup.** See §4, §5 and §6 for the engine-specific command. Keep it
   until the new version has run successfully for a full crawl cycle.
5. **Reserve a quiet window.** The PostgreSQL migration holds
   `ACCESS EXCLUSIVE` locks until it commits (§4.3). On SQLite it needs
   exclusive access to the file.
6. **Decide the rollback you would use** before you start (§9). A backup restore
   is the universal answer.

---

## 3. Choose your method

| Method | PostgreSQL | SQLite | MySQL |
| --- | --- | --- | --- |
| CLI client (`psql`, `sqlite3`, `mysql`) | **Recommended** | **Recommended** | SQL only, runtime unsupported (§5) |
| Desktop SQL editor / GUI | Ready-made file for the `crowler` role (§4.4); custom roles need preparation | Works if it runs one script on one connection | Only with DELIMITER support (§5) |
| Hosted admin console (phpMyAdmin, Adminer, cloud consoles) | **Not verified.** Use only if it can run a whole script as one transaction on one connection | **Not verified** | **Not verified** |
| Container/Compose "automatic" upgrade | No automatic in-place upgrade exists | No | No |

The container entrypoint runs a setup script **only when the database volume is
empty**. It never upgrades an existing database, so `docker compose up` will not
migrate your data.

"Not verified" means exactly that: it may work, but it has not been tested for
this release, and a console that splits statements or commits each one silently
breaks the PostgreSQL migration.

---

## 4. PostgreSQL (primary)

### 4.1 Backup

```bash
pg_dump --format=custom --file=crowler-before-1.17.dump "$PGDATABASE"
```

### 4.2 Command line

`psql` substitutes the `:CROWLER_DB_USER` token used by the migration. Set the
lock bound on the same connection:

```bash
PGOPTIONS='-c lock_timeout=5s' \
psql -X -v ON_ERROR_STOP=1 \
  -v CROWLER_DB_USER="$CROWLER_DB_USER" \
  -f pkg/database/db_migrations/postgresql-migration-v1.17.pgsql
```

- Connection settings come from `PGHOST`, `PGPORT`, `PGUSER`, `PGDATABASE`,
  `PGPASSWORD` or `~/.pgpass`. Do not put credentials on the command line.
- `-X` ignores `~/.psqlrc`.
- `ON_ERROR_STOP=1` makes any SQL error a non-zero exit (`3`).
- `lock_timeout` bounds **each lock wait**, not the run time, and it does not
  release locks that are already held.

Check that the option reaches a session before relying on it:

```bash
PGOPTIONS='-c lock_timeout=5s' psql -X -At -c 'SHOW lock_timeout;'   # 5s
```

### 4.3 What the migration locks

The file is one transaction (`BEGIN` … `COMMIT`). Locks are held until that
commit, not until the end of the statement that took them:

| Statement | Lock | Blocks |
| --- | --- | --- |
| `LOCK TABLE DBSchemaVersion IN EXCLUSIVE MODE` | `EXCLUSIVE` | writes to `DBSchemaVersion` |
| `ALTER TABLE InformationSeed ADD COLUMN …` | `ACCESS EXCLUSIVE` | **reads and writes** on `InformationSeed` |
| `CREATE INDEX …` | `SHARE` | writes to that table |
| `DROP TRIGGER …` / `CREATE TRIGGER …` | `ACCESS EXCLUSIVE` / `SHARE ROW EXCLUSIVE` | reads and writes on the five link tables |

So the migration blocks `InformationSeed` and the link tables for its whole
duration. Look for long-running transactions before starting:

```sql
SELECT pid, now() - xact_start AS xact_age, state,
       left(regexp_replace(query, '\s+', ' ', 'g'), 100) AS query_prefix
FROM pg_stat_activity
WHERE datname = current_database() AND xact_start IS NOT NULL
ORDER BY xact_age DESC;
```

`statement_timeout` is a *per-statement* ceiling, not a transaction budget, and a
legitimate `CREATE INDEX` can exceed a small value. Leave it unset unless your
change policy requires one.

### 4.4 No-shell GUI path (standard `crowler` role)

The PostgreSQL server does not understand `:CROWLER_DB_USER`; pasting the
canonical file into a GUI fails with a syntax error at `:`. For installations
whose CROWler runtime role is exactly `crowler`, the release ships a ready-made
copy:

```text
pkg/database/db_migrations/postgresql-migration-v1.17.gui-crowler.sql
```

It is produced mechanically from
`postgresql-migration-v1.17.pgsql`: only the 29 `:CROWLER_DB_USER` tokens are
replaced with the quoted identifier `"crowler"`, plus a fixed generated banner.
Its transaction boundaries, version guards, trigger definitions, grants and
marker updates are identical to the canonical migration. A repository test
(`TestPostgresV117GUICrowlerArtifactMatchesCanonical`) fails if the copy drifts.

> **Role warning.** Do not run this file if your runtime role is not exactly
> `crowler`. It grants and transfers ownership to `"crowler"` and has no way to
> discover your actual role. Installations with a custom role must prepare their
> own copy with `scripts/prepare-postgresql-migration.sh` (administrator or
> `psql`), as described below.

#### Obtain the file without a shell

Take the file from the same place you obtained this release: the 2.3.0 source
archive or the repository file tree at the 2.3.0 commit. Download it unchanged.
Do not copy it from another version, and do not edit it.

#### Verify the target role

In the SQL client, run:

```sql
SELECT 1 FROM pg_roles WHERE rolname = 'crowler';
```

One row means the role exists. No rows means the file is not applicable: create
the runtime role first, or use a custom-role prepared copy. The migration does
not create roles.

#### Back up first

Use one of:

- your organisation's DBA-managed PostgreSQL backup;
- the client's own PostgreSQL backup feature, **only if** it produces a real
  `pg_dump`-equivalent backup;
- `pg_dump --format=custom --file=crowler-before-1.17.dump "$PGDATABASE"` from a
  machine that has the client tools.

A CSV, spreadsheet or result-grid export is **not** a database backup. If you
cannot obtain a verified consistent backup from the GUI, stop and ask a DBA;
do not migrate on the strength of an export.

#### Configure the client

These settings matter more than the product name:

- **Script execution, not "execute current statement".** The whole file must be
  submitted as one script/batch.
- **One connection.** The file's `BEGIN` … `COMMIT` and any `SET` must run on the
  same session. A console that opens a new pooled connection per statement
  cannot run this migration.
- **No client-managed transaction.** If the client has "wrap script in a
  transaction" or "autocommit off" automation, disable it. The file already has
  its own `BEGIN` and `COMMIT`; a second `BEGIN` produces a warning and makes the
  commit boundary ambiguous.
- **No statement splitting.** Some consoles split on `;`. The migration contains
  `DO $$ … $$` blocks; splitting them breaks the script.
- **Server-side batch execution.** The client must send the file as a script to
  the server, not replay it statement by statement through an ORM or query
  builder.

#### Add the lock bound in the same submission

`lock_timeout` is per session. Putting it in a separate UI submission can land on
a different pooled connection. Prepend it to the *same* script, above the file's
`BEGIN`:

```sql
SET lock_timeout = '5s';
```

then the entire file contents. Submit once. This bounds each lock wait; it does
not shorten locks already held and it does not release them early (§4.3). If
your client cannot submit both as one script on one connection, do not use that
client for the migration.

#### Execute and verify

1. Confirm the role (§ above) and the backup.
2. Run the whole script on one connection, with the optional `SET` first.
3. If the client reports an error, stop. The transaction should have rolled
   back; confirm with the postchecks. Do not "fix" it by committing partial
   results.
4. Run the postchecks (§4.5).

The shipped GUI copy has been executed as one raw SQL batch on a single
PostgreSQL 17 connection in this release's validation. Specific desktop or
hosted GUI products were **not** tested; see §10.

#### If the client cannot guarantee one connection

Do not migrate with it. Use `psql` (§4.2), a DBA-run migration, or a managed
database migration job that provides a single session and a bounded lock wait.
A console that silently reconnects between statements can leave the database in
a state that is neither the old schema nor the new one.

#### Custom runtime roles

The GUI copy is only for `crowler`. For any other role, have an administrator
generate a validated copy with the helper (requires a shell on the
administrator's side):

```bash
scripts/prepare-postgresql-migration.sh \
  --role "<actual-runtime-role>" \
  --output /tmp/postgresql-migration-v1.17.gui.sql
```

The helper:

- accepts only an unquoted identifier matching `^[A-Za-z_][A-Za-z0-9_]{0,62}$`
  and rejects quotes, spaces, dots, semicolons and every other injection vector;
- replaces **only** the exact `:CROWLER_DB_USER` token with a double-quoted
  identifier, preserving case;
- verifies that no token survives and that the rest of the file is byte-identical;
- refuses to overwrite an existing file without `--force`, refuses a directory
  as the output, and refuses to rewrite the source in place.

It works for the optional add-ons too (`--input <file>`). There is no validated
no-shell custom-role route in this release: do not hand-edit 29 tokens and do not
promise that a client-side search-and-replace is equivalent.

### 4.5 Postchecks

```sql
SELECT version FROM DBSchemaVersion WHERE is_current;                    -- 1.17
SELECT writer_generation FROM DatabaseWriterCompatibility WHERE lock_id = 1;

SELECT count(*) FROM pg_trigger
WHERE NOT tgisinternal AND tgname LIKE 'trg_after_delete_%index'
  AND (tgtype & 1) = 0 AND tgoldtable = 'deleted_links';                 -- 5

SELECT md5(prosrc) FROM pg_proc WHERE proname = 'handle_shared_entity_deletion';
```

For the 2.3.0 build validated for this guide the function digest is
`5bdd88efd2e200043c2fb1502be5ba3b`. A fresh 1.17 install of the same build must
produce the same value. If it differs, stop and compare the deployed build with
the one you are migrating from; do not assume the migration succeeded. A blocked
or failed run exits non-zero and changes nothing (verified: a lock-timeout
failure left the function body, triggers and both markers intact).

### 4.6 Retry and repeatability

The file is additive and safe to re-run. If it fails on a lock timeout, fix the
blocking transaction and run it again; if it fails on a privilege or role error,
fix that and re-run. There is no partial state to clean up.

---

## 5. MySQL / MariaDB (SQL shipped, runtime unsupported)

> **Do not deploy 2.3.0 on MySQL or MariaDB.** `pkg/database.NewHandler()`
> rejects `database.type: mysql`, and the MySQL handler is commented out in this
> source tree for licensing reasons. The SQL below is shipped for reference and
> future work.

The **v1.17 migration file itself** was executed successfully on MySQL 8.0
against a minimal 1.17-compatible schema. Pre-existing blockers in other MySQL
files prevent an end-to-end path today:

- `mysql-setup.mysql` cannot be loaded by MySQL 8.0: it uses `rank` as a column
  name, which is reserved since 8.0.2.
- On MariaDB 11 the same file fails on the functional index
  `CREATE INDEX … (LOWER(page_url))`.
- `mysql-setup.mysql` and the `v1.09`/`v1.11` migration files contain bare
  sequences of dashes as visual separators. MySQL only treats `--` as a comment
  when whitespace follows, so those lines are a syntax error rather than a
  comment. The v1.17 file has none.

All three are executable-SQL issues in files this documentation does not change;
each needs its own maintainer decision. `docs/database-migrations.md` cannot make
those files loadable, and this release does not claim they are.

### 5.1 Delimiters

`DELIMITER //` and `DELIMITER ;` are **mysql client** instructions, not SQL. A
raw SQL panel usually does not understand them.

- **With a script runner:** `mysql … < mysql-migration-v1.17.mysql` (the client
  understands `DELIMITER`). Verified: exit 0, converges, idempotent.
- **Without delimiter support:** execute each
  `CREATE PROCEDURE … BEGIN … END` block as **one statement** (without the
  `DELIMITER` lines), keeping the original order of the `DROP PROCEDURE`, `CALL`
  and marker statements. Verified with `mysql --delimiter='//'` per routine:
  identical result.

### 5.2 Partial failure

MySQL commits DDL implicitly, so there is no transactional rollback. Verified:
an interrupted run left `SourceOwnership` and the five `InformationSeed` columns
in place while `DBSchemaVersion` stayed `1.16`.

Recovery is a deliberate replay: fix the cause, run the **complete** file again,
and verify. The `IF NOT EXISTS` guards and the `INFORMATION_SCHEMA` checks make
the replay converge.

### 5.3 Postchecks

```sql
SELECT version FROM DBSchemaVersion WHERE is_current = 1;                -- 1.17
SELECT writer_generation FROM DatabaseWriterCompatibility WHERE lock_id = 1;

SELECT COUNT(DISTINCT index_name) FROM information_schema.statistics
WHERE table_schema = DATABASE() AND table_name = 'InformationSeed'
  AND index_name IN ('idx_informationseed_lease_expiry',
                     'idx_informationseed_next_retry',
                     'idx_informationseed_claim_token');                 -- 3
```

---

## 6. SQLite

### 6.1 Stop the writers and back up

The SQLite database is a single file. Do not migrate it while a CROWler process
has it open.

```bash
sqlite3 SitesIndex.db ".backup 'SitesIndex-before-1.17.db'"
# or: sqlite3 SitesIndex.db "VACUUM INTO 'SitesIndex-before-1.17.db'"
```

### 6.2 Run the migration

```bash
sqlite3 -bail SitesIndex.db < pkg/database/db_migrations/sqlite-migration-v1.17.sqlite3
```

`-bail` stops at the first error. The file has its own `BEGIN`/`COMMIT`; do not
wrap another transaction around it. In a GUI, open the file and execute the
script as one unit on one connection.

### 6.3 Runtime convergence is required

The file does **not** create the Information Seed claim columns
(`claim_token`, `fencing_token`, `lease_expires_at`, `heartbeat_at`,
`next_retry_at`). The runtime installs them through
`ensureSQLiteInformationSeedSchema` before the startup compatibility check.
Verified: after a CLI-only migration the columns were absent; starting the 2.3.0
runtime installed all five.

Practical sequence:

1. stop the old writers;
2. back up;
3. run the SQL migration with `sqlite3 -bail`;
4. start the 2.3.0 service once and let it complete its schema repair;
5. confirm the columns and markers are present;
6. run your normal crawl.

A GUI-only migration that never starts the new runtime does **not** converge.

### 6.4 Postchecks

```bash
sqlite3 SitesIndex.db "SELECT version FROM DBSchemaVersion WHERE is_current=1;"
sqlite3 SitesIndex.db "SELECT writer_generation FROM DatabaseWriterCompatibility WHERE lock_id=1;"
sqlite3 SitesIndex.db "SELECT count(*) FROM pragma_table_info('InformationSeed')
  WHERE name IN ('claim_token','fencing_token','lease_expires_at','heartbeat_at','next_retry_at');"   # 5
sqlite3 SitesIndex.db "PRAGMA foreign_key_check;"   # no rows
```

### 6.5 Busy or locked database

If another connection holds a write lock, the migration fails with
`database is locked` and rolls back. Stop the writers and retry. `PRAGMA
busy_timeout` is a per-connection busy-wait policy; it is not a PostgreSQL-style
lock timeout and is optional.

---

## 7. Troubleshooting

| Symptom | Cause | Fix |
| --- | --- | --- |
| PostgreSQL: `syntax error at or near ":"` | the raw file was pasted into a GUI/server | prepare the file with `scripts/prepare-postgresql-migration.sh` |
| `role "crowler" does not exist` | `:CROWLER_DB_USER` names a missing role | create the role, or prepare the file with the correct existing role |
| `must be owner of table …` / `permission denied` | account lacks owner/superuser rights | use the database owner or an administrator |
| `canceling statement due to lock timeout` (`55P03`) | another transaction holds a conflicting lock | find the blocker (§4.3), wait or stop it, re-run |
| `requires schema version 1.16` | prerequisite chain not applied | apply every earlier migration first |
| PostgreSQL startup: `fatal database compatibility check failed` | migration incomplete or wrong markers | re-run the migration and the postchecks |
| MySQL: `1064 … near 'DELIMITER'` | the client/console does not support DELIMITER | use a script runner, or execute each routine as one statement |
| MySQL: `PROCEDURE … does not exist` | routines were dropped or never created in the same run | re-run the complete file; keep the original statement order |
| SQLite: `database is locked` | another connection holds a write lock | stop the writers, retry |
| SQLite: claim columns missing after a GUI run | runtime repair has not run | start the 2.3.0 service once |
| GitHub/CI: migrations skipped | not a migration error | see the workflow logs; do not assume success |

---

## 8. Validate and restart

1. Start the new services.
2. Confirm readiness:
   ```bash
   curl -fsS http://127.0.0.1:8080/v1/health
   curl -fsS http://127.0.0.1:8080/v1/ready
   ```
3. Check the engine log for the compatibility message; a failure names the
   detected schema version and writer generation.
4. Crawl one known source and confirm the page is indexed and searchable.
5. If you applied an optional capability (agent-goal, MCP, crawl attempts), run
   its own postcheck (`dbDeploy -check` on PostgreSQL).

---

## 9. Safe rollback

There is no universal "down" migration. Choose according to the engine:

| Engine | Safe rollback |
| --- | --- |
| PostgreSQL | the migration is one transaction: on failure it rolls back by itself. After a successful upgrade, restore the `pg_dump` backup; the writer generation has changed, so do not point an old build at the upgraded data. |
| MySQL | restore the backup. There is no transactional rollback; a partial state must be replayed or restored. |
| SQLite | stop the writers and restore the `.backup`/`VACUUM INTO` copy over the database file. |

**Never** use the pre-RC2/v2.2.8 shared-entity cleanup trigger as a rollback. It
can silently lose a concurrently committed shared reference. If you need the
previous trigger shape, use the RC4F row-level definition described in
[`crowler-agents/reviews/RELEASE_230_RC5_DEPLOYMENT_MONITORING.md`](crowler-agents/reviews/RELEASE_230_RC5_DEPLOYMENT_MONITORING.md)
§6.

---

## 10. Tested scope and limitations

Verified on disposable instances for this release:

| Engine / client | What was verified |
| --- | --- |
| PostgreSQL 17 + `psql` 17 | CLI upgrade from a simulated 1.16 database; repeat run; wrong prerequisite; missing role; lock-timeout failure and rollback |
| PostgreSQL 17 + a raw-SQL driver on one connection | the shipped `gui-crowler` file and a prepared custom copy executed as one batch; identical schema, markers, trigger count and function digest; reapply is idempotent |
| PostgreSQL 17 + shipped GUI file + `SET lock_timeout` in the batch | a deliberately held conflicting lock aborted the batch with `55P03` and the whole transaction rolled back |
| SQLite 3.54 + `sqlite3` CLI | CLI migration, busy/locked behaviour, `.backup` integrity, runtime convergence of the claim columns |
| MySQL 8.0 + `mysql` client | migration SQL only, on a minimal compatible schema: delimiter path, per-routine path, idempotency, partial-application behaviour |
| MariaDB 11.8 | fresh-install script only: blocked by a functional index (reported, not fixed) |

**Not verified:** any specific desktop GUI product or hosted admin console
(pgAdmin, DBeaver, phpMyAdmin, Adminer, cloud SQL consoles), and MySQL/MariaDB
as a runtime backend (disabled in 2.3.0). Treat those paths as unsupported until
someone tests them and records the result. A raw SQL batch executed through a
driver is not evidence that a particular GUI product configures its script mode
correctly.

The supporting evidence is in
[`crowler-agents/reviews/RELEASE_230_DOC2_VALIDATION.md`](crowler-agents/reviews/RELEASE_230_DOC2_VALIDATION.md).
