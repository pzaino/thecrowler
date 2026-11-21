# Release 2.3.0 — final migration documentation validation

Work package: `WP-REL230-DOC1`
Baseline: `15e8684072a68aa231f6071b86a9ae2afd563c2a` (`develop`, clean)
Date: 2026-11-21
Release: `v2.3.0` · Schema `1.17` · Writer generation `verified-ownership-v1.17`

This package publishes the operator migration instructions for 2.3.0 and a
validated helper for SQL-GUI execution. It changes no schema semantics, no
trigger logic, no version marker and no executable SQL.

## 1. Deliverables

| Item | Path |
| --- | --- |
| Operator guide | `docs/database-migrations.md` |
| SQL-GUI preparation helper | `scripts/prepare-postgresql-migration.sh` |
| Helper regression test | `scripts/tests/prepare-postgresql-migration.sh` |
| Migration headers | all files in `pkg/database/db_migrations/` |
| Links | `docs/README.md`, `docs/installation.md`, `docs/database/database_architecture.md`, `pkg/database/db_migrations/README.md` |

The three v1.17 base migrations carry a full header: target and source version,
prerequisite chain, engine, account/privileges, backup, CLI command, GUI
path, transaction/session rules, failure behaviour, postchecks and a link to the
guide. Earlier chain files carry a two-line pointer header. The optional 1.17
add-ons say explicitly that they are add-ons and apply only when the capability
is absent.

## 2. Comment-only boundary

All 32 modified SQL files were checked by removing every line whose first
non-space characters are `--` from both the committed file and the working copy,
then comparing the remaining bytes:

```text
files_checked=32 executable_diffs=0
git diff --stat  ->  36 files changed, 250 insertions(+), 0 deletions(-)
```

Every change in `pkg/database/db_migrations/` is therefore an added comment.
The transaction fences (`BEGIN`/`COMMIT`), version guards, token positions,
`DROP`/`CREATE TRIGGER` pairs and marker updates are untouched.

Two self-inflicted defects were found and corrected while testing:

- the first helper banner echoed the token it had just replaced, which made the
  "no token survives" assertion vacuous; the banner is now static;
- the MCP test harness splits the SQLite MCP add-on on `;` before stripping
  comment lines, so an added comment containing a semicolon became executable
  text (`near "stop": syntax error`). The header uses a full stop instead. This
  is a reminder that comments inside SQL are still parsed by naive splitters and
  must not contain semicolons.

## 3. PostgreSQL

Environment: disposable `postgres:17` (17.12), runtime role `crowler`,
administrative role `postgres`. No shared or production database was touched.

A 1.16 state was simulated from a fresh install by dropping the 1.17 objects
(`SourceOwnership`, `CrawlProposals`, `CrawlApprovals`, the attempt tables, the
agent-goal tables, the capability table), dropping the five Information Seed
claim columns, setting `DBSchemaVersion` to 1.16 and the writer generation to
`reservation-v1.15`. Result: 63 tables, 0 claim columns, no `SourceOwnership`.

| Case | Command shape | Result |
| --- | --- | --- |
| CLI upgrade | `PGOPTIONS='-c lock_timeout=5s' psql -X -v ON_ERROR_STOP=1 -v CROWLER_DB_USER=crowler -f postgresql-migration-v1.17.pgsql` | exit 0 in 418 ms; 78 tables; schema 1.17; writer `verified-ownership-v1.17`; 5 claim columns; `SourceOwnership` present; function digest `5bdd88efd2e200043c2fb1502be5ba3b`; 5 statement triggers with `deleted_links` |
| Repeat run on 1.17 | same command | exit 0 (idempotent) |
| Wrong prerequisite | 1.15 marker | exit 3 at the guard; `v1.17 requires schema version 1.16`; version stayed 1.15 |
| Wrong role | prepared with `no_such_role` | exit 3; `role "no_such_role" does not exist`; version stayed 1.16; no `SourceOwnership` |
| Lock bound | prepared file with `SET lock_timeout = '5s'` prepended, run while another session held `KeywordIndex` | exit 3 after 5,302 ms with `55P03`; version stayed 1.16; no `SourceOwnership` |
| Prepared GUI file as one raw batch | Go `database/sql` `ExecContext` of the whole prepared file on one `*sql.Conn` (not `psql`) | exit 0; schema 1.17; 78 tables; same function digest; 5 triggers |

The last row is the important one: it shows the prepared file works as a single
server-side batch, which is the closest reproducible proxy for a SQL GUI that
can run a whole script on one connection. It does not prove any specific GUI
product works; see §7.

## 4. SQLite

Environment: `sqlite3` 3.54.1, disposable files.

| Case | Result |
| --- | --- |
| Fresh install | 69 tables, schema 1.17 |
| Simulated 1.16 state | `SourceOwnership`/`CrawlApprovals` dropped, five claim columns dropped, markers rolled back |
| CLI migration `sqlite3 -bail < sqlite-migration-v1.17.sqlite3` | exit 0; schema 1.17; `SourceOwnership` and `CrawlApprovals` present; `PRAGMA foreign_key_check` empty |
| Claim columns after SQL only | **0 of 5** — the SQL file does not create them |
| Runtime repair | `ensureSQLiteInformationSeedSchema` installed all 5 |
| Repo regression | `TestSQLiteV117MigrationConvergesAndPreservesHistory` PASS |
| Busy database | a second connection held `BEGIN IMMEDIATE`; migration exited 1 with `database is locked`; version stayed 1.16; retry after release exited 0 and reached 1.17 |
| Consistent backup | `.backup` exit 0; `PRAGMA integrity_check` = `ok` on both; same table count and version |

This confirms the guide's SQLite caution: a SQL-only or GUI-only run stops at
1.17 markers but is not fully converged until the 2.3.0 runtime starts once.

## 5. MySQL / MariaDB

Environment: disposable `mysql:8.0` and `mariadb:11` (11.8.9). The repository's
own `mysql-setup.mysql` could not be used to build the fixture, so the migration
was validated against a minimal 1.17-compatible schema (version/writer tables,
`InformationSeed`, and the tables the migration touches).

| Case | Result |
| --- | --- |
| Migration via `mysql < mysql-migration-v1.17.mysql` (DELIMITER path) | exit 0; schema 1.17; writer marker; 5 columns; 3 indexes |
| Repeat run | exit 0; same state |
| Per-routine execution (`mysql --delimiter='//'` for each procedure, then the remaining statements in order) | identical state; proves the documented GUI workaround |
| Partial application (first 15 statements only) | `SourceOwnership` present and 5 columns installed while `DBSchemaVersion` stayed 1.16 — concrete proof that MySQL cannot roll back DDL |
| Replay of the complete file | exit 0; converged to 1.17 |

Pre-existing blockers found in other MySQL files (reported, not changed):

- `mysql-setup.mysql` fails on MySQL 8.0 because `rank` is a reserved word
  (line 93).
- The same file fails on MariaDB 11 on the functional index
  `CREATE INDEX … (LOWER(page_url))`.
- `mysql-setup.mysql` and the `v1.09`/`v1.11` migrations contain bare dash
  separator lines, which MySQL parses as an error rather than a comment (the
  client error is `near '----…'`). The v1.17 files have none.

Separately, `pkg/database.NewHandler()` has no MySQL case and
`pkg/database/mysql_db.go` is commented out for licensing reasons, so the 2.3.0
runtime rejects `database.type: mysql` regardless of schema state. This
contradicts older text that calls MySQL "legacy-only"; the guide states the
runtime limitation plainly. Resolving it is outside DOC1.

## 6. Helper

`scripts/prepare-postgresql-migration.sh`:

- validates the role as an unquoted identifier (`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)
  and rejects quotes, spaces, dots, semicolons, leading digits and anything
  longer than 63 characters;
- replaces only `:CROWLER_DB_USER`, with a double-quoted identifier;
- verifies the replacement count, that zero tokens remain, and that the file
  outside the banner is byte-identical to the substituted input;
- requires `--output`, refuses to overwrite without `--force`, refuses a
  directory as the output path, and refuses to rewrite the input in place;
- prints a 7-line static banner (no timestamp), so repeated runs are
  reproducible.

Regression test: 7/7 PASS — real v1.17 file (29 replacements, body identical),
optional add-on, quoting, six invalid-role classes, overwrite behaviour,
in-place refusal, missing input and input without the token.

## 7. GUI coverage classification

| Path | Status |
| --- | --- |
| PostgreSQL prepared file executed as one server-side batch on one connection | **tested** |
| PostgreSQL file pasted raw | **fails as documented** (server does not understand `:CROWLER_DB_USER`) |
| MySQL file through a DELIMITER-capable client | **tested** |
| MySQL per-routine execution without DELIMITER lines | **tested** |
| Specific desktop GUI products (pgAdmin, DBeaver, SQLiteStudio, …) | **NOT RUN** |
| Hosted admin consoles (phpMyAdmin, Adminer, cloud SQL editors) | **NOT RUN** |
| MySQL/MariaDB runtime backend | **NOT AVAILABLE** in this tree |

The guide labels unverified paths as not verified rather than claiming them.

## 8. Verification commands actually run

```text
git diff --stat                                 36 files, +250/-0
comment-strip comparison (32 files)             0 executable diffs
scripts/tests/prepare-postgresql-migration.sh   7/7 PASS
go test ./pkg/database -count=1                 PASS (20.6s)
go test ./pkg/agent -run 'TestMCPMutation'      PASS
sqlite3 migration + busy + backup               as above
psql upgrade/repeat/wrong-version/wrong-role    as above
lock-timeout failure under a held lock          exit 3, 55P03, rollback
prepared file as a raw batch via database/sql   exit 0, converged
mysql 8.0 full/per-routine/partial/replay       as above
mariadb 11 fresh-install attempt                blocked by functional index (reported)
```

## 9. Not run

- Hosted GitHub CI for the DOC1 patch: no push was performed.
- Any specific GUI application or hosted SQL console.
- MySQL/MariaDB as a running CROWler backend.
- A production or shared database migration.
- The full repository suite; only the packages that read the modified SQL were
  run, plus the helper's own regression test.

## 10. Residual risks and follow-ups

1. The MySQL setup and 1.09/1.11 migration files remain unloadable because of
   bare dash separators, and the setup file additionally uses a reserved word
   and a functional index. Fixing them is an executable-SQL change and needs its
   own maintainer decision.
2. MySQL runtime support is absent from `NewHandler()` despite documentation
   that still describes it as legacy-only. That documentation should be aligned
   in a separate change.
3. The guide cannot promise that a GUI runs a whole file on one connection.
   Operators whose console cannot do so must use `psql` or a migration job; the
   guide says this in the PostgreSQL section.
4. The PostgreSQL migration still blocks `InformationSeed` and the five link
   tables for its duration. The guide documents this and the bounded-lock
   invocation; it does not shorten the window.
5. Hosted CI and real GUI testing remain the two evidence gaps. Neither is
   claimed as passed.
