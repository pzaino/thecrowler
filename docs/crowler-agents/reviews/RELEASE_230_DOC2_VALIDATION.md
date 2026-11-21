# Release 2.3.0 — DOC2 migration documentation corrections

Work package: `WP-REL230-DOC2`
Baseline / exact tested SHA: `78d2b0b87daa309ce53f86e20cc2861a238899f3`
Branch: `develop`
Date: 2026-11-21
Release: `v2.3.0` · Schema `1.17` · Writer `verified-ownership-v1.17`

This package closes the three documentation gaps left by `WP-REL230-DOC1`:

1. a genuinely shell-free PostgreSQL v1.17 route for the standard `crowler`
   role, shipped as a release-matched SQL artifact;
2. corrected and reconciled `pkg/database/db_migrations/README.md`;
3. a targeted audit of MySQL/MariaDB support claims against the real runtime
   dispatch.

No canonical migration body, schema object, trigger, version marker or runtime
code was changed. The only executable SQL added is the generated GUI copy, which
is byte-identical to the canonical migration apart from the fixed banner and the
role substitution.

## 1. Deliverables

| Item | Path |
| --- | --- |
| GUI-ready PostgreSQL v1.17 artifact (role `crowler`) | `pkg/database/db_migrations/postgresql-migration-v1.17.gui-crowler.sql` |
| Artifact parity test | `pkg/database/postgresql_gui_artifact_test.go` |
| Updated operator guide | `docs/database-migrations.md` |
| Corrected migration README | `pkg/database/db_migrations/README.md` |
| Corrected entry points | `docs/database/README.md`, `docs/database/database_architecture.md`, `docs/installation.md`, `docs/features.md`, `docs/information-seed/information_seed_lifecycle.md`, `docs/time-series/timeseries.md`, `docs/crowler-agents/crawl_scheduling_foundation.md`, `docs/crowler-agents/crawl_request_authorization.md`, `docs/email-crawling/email-crawling-design.md`, `config.default` |
| Helper banner hardening | `scripts/prepare-postgresql-migration.sh`, `scripts/tests/prepare-postgresql-migration.sh` |

## 2. GUI artifact

### 2.1 What it is

`postgresql-migration-v1.17.gui-crowler.sql` is the canonical
`postgresql-migration-v1.17.pgsql` with:

- a fixed nine-line generated banner (no timestamp, no environment data);
- each of the 29 `:CROWLER_DB_USER` tokens replaced by `"crowler"`.

Everything else is byte-identical: `BEGIN`/`COMMIT`, the version guard, the
`InformationSeed` alterations, the shared-artifact trigger replacement, grants,
ownership statements and the writer marker.

The artifact is **only for installations whose runtime role is exactly
`crowler`**. The banner says so, the guide repeats it in a labelled warning, and
the filename makes the binding explicit.

### 2.2 Anti-drift test

`TestPostgresV117GUICrowlerArtifactMatchesCanonical` reconstructs the expected
file as:

```text
fixed banner + canonical body with :CROWLER_DB_USER -> "crowler"
```

and compares it byte-for-byte with the shipped artifact. It also requires exactly
29 tokens in the canonical file and zero in the artifact. A changed migration, a
changed banner, a hand edit, a missing substitution or a stale artifact fails the
test.

### 2.3 Helper banner

The helper now adds two lines to every generated file:

```text
-- This file is bound to that role. If your runtime role differs, regenerate it.
-- Do not reuse or hand-edit this file for another role.
```

The helper regression test was updated for the nine-line banner. The helper's
role grammar, substitution, body-equality check, overwrite guard, directory
guard and in-place refusal are unchanged.

## 3. Operator guide changes

`docs/database-migrations.md` §4.4 now contains a complete shell-free path:

- where the artifact lives and how to obtain it without a shell;
- `SELECT 1 FROM pg_roles WHERE rolname = 'crowler'` role verification;
- backup options, with an explicit statement that a CSV/grid export is not a
  backup;
- required client settings: script mode, one connection, no client-managed
  transaction, no statement splitting, server-side batch;
- the per-session `SET lock_timeout = '5s'` prepended in the same submission;
- the expected function digest for this build
  (`5bdd88efd2e200043c2fb1502be5ba3b`);
- a stop rule when the client cannot guarantee one persistent connection;
- the custom-role alternative through the helper, with no promise of a no-shell
  custom-role route;
- product-specific clients remain `NOT RUN`; the guide does not claim any GUI
  product was tested.

§3 (method matrix) and §10 (tested scope) were aligned with the artifact and its
limits.

## 4. Migration README reconciliation

`pkg/database/db_migrations/README.md` now leads with the authoritative guide and
an engine support matrix, then states:

- **Privileges:** no required login name. PostgreSQL needs an account that can
  create/alter objects, replace functions/triggers, change ownership and grant to
  the runtime role (normally the database owner or an administrator). The runtime
  role named by `CROWLER_DB_USER` must already exist.
- **Writer generation:** v1.17 writes `verified-ownership-v1.17`; a 1.16 database
  used `reservation-v1.15`. This is a change, not preservation. Re-running v1.17
  is idempotent.
- **Version chain:** apply the files that exist, in order. SQLite and MySQL ship
  no 1.10/1.12 file.
- **Runtime matrix:** PostgreSQL supported (primary); SQLite supported with its
  documented runtime repair; MySQL/MariaDB **not available** because
  `NewHandler()` rejects `database.type: mysql` and the handler is commented out.
- The two add-on instruction paragraphs no longer require the `postgres` login;
  they require an account with the actual DDL/ownership/grant privileges.
- The historical “MySQL remains legacy-only” and “existing startup behavior”
  statements were corrected.

The historical technical sections below the new overview were retained, because
they describe schema evolution rather than current runtime support.

## 5. MySQL/MariaDB claim audit

Runtime evidence: `pkg/database/sqlite_db.go` `NewHandler()` switches only on
`postgres` and `sqlite`/`sqlite3`; the default returns
`unsupported database type`. `pkg/database/mysql_db.go` is inside a block comment
(“Commented out to avoid issues with the Licensing”). The config schema still
lists `mysql` in its enum, and `pkg/config` does not reject the string; the
failure is at handler construction.

| File | Previous claim | New wording | Status |
| --- | --- | --- | --- |
| `pkg/database/db_migrations/README.md` | “postgres admin account only”, “keeps the existing writer-generation marker”, “MySQL remains legacy-only”, “existing 1.17 startup behavior” | support matrix; privilege-based account rule; writer change stated; MySQL runtime unavailable | fixed |
| `docs/database/README.md` | “PostgreSQL, MySQL/MariaDB, and SQLite are expected to use …” | PostgreSQL primary, SQLite supported, MySQL SQL reference-only; `NewHandler` reference | fixed |
| `docs/database/database_architecture.md` | “setup/migration coverage … across supported DBMS backends”; MySQL claim branch/approvals described as operational | runtime matrix at the top; MySQL branch marked unreachable; approval table described as schema-only | fixed |
| `docs/features.md` | “selected subsystems also support MySQL or MariaDB and SQLite” | PostgreSQL primary, SQLite supported, MySQL SQL retained but runtime disabled | fixed |
| `docs/installation.md` | “command-line and SQL-GUI paths for PostgreSQL, SQLite and MySQL” | PostgreSQL CLI/GUI, SQLite, and retained MySQL SQL notes | fixed |
| `docs/information-seed/information_seed_lifecycle.md` | MySQL wake-up and claim paths described as operational | noted as retained SQL, not reachable in the 2.3.0 runtime | fixed |
| `docs/time-series/timeseries.md` | MySQL CRUD/table support described alongside PostgreSQL/SQLite | MySQL paths marked reference-only in 2.3.0 | fixed |
| `docs/email-crawling/email-crawling-design.md` | “PostgreSQL is the currently supported database”; MySQL schema assumed | PostgreSQL + SQLite are the 2.3.0 backends; MySQL retained for future work | fixed |
| `docs/crowler-agents/crawl_scheduling_foundation.md` | `ScheduleSourceCrawl()` “remains available for legacy MySQL callers” | scheduling API not reachable because the backend is not enabled | fixed |
| `docs/crowler-agents/crawl_request_authorization.md` | MySQL “keeps the ownership table” / “retains ownership provenance” as a backend | schema-only statement; runtime does not enable MySQL | fixed |
| `config.default` | `postgres | mysql | sqlite` | `postgres | sqlite`, with a note that MySQL SQL is reference-only | fixed (comment only) |
| `schemas/crowler-config-schema.json` | enum still includes `mysql` | untouched | follow-up: contract change, not documentation |
| `pkg/config/types.go` | comment mentions `"mysql"` as an example | untouched | follow-up: code comment / configuration contract |
| `docs/crowler-agents/reviews/**` | historical records mention “legacy-only” | untouched | historical review evidence; not current operator guidance |
| `docs/database/high_churn_hardening.md` | historical v1.16 MySQL ledger notes | untouched | explicitly historical section |
| `docs/crowler-agents/mcp-mutation-authorization.md` | “MySQL is not supported for durable MCP mutation persistence” | untouched | already accurate |
| `docs/features.md` plugin/Neo4j/MySQL lines | external database access by plugins | untouched | not a storage-backend claim |

## 6. Static and artifact verification

Executed in the isolated worktree at `78d2b0b8`:

```text
comment-strip comparison, canonical v1.17 files (PG/MySQL/SQLite):
  PostgreSQL OK executable-identical
  MySQL      OK executable-identical
  SQLite     OK executable-identical

independent artifact body check:
  body_matches=True
  tokens_in_artifact=0
  banner_lines=9
  BEGIN=1 COMMIT=1

gofmt -l pkg/database/postgresql_gui_artifact_test.go   (no output)
go vet ./pkg/database                                   PASS
go test ./pkg/database -count=1                         PASS (17.6s)
go test ./pkg/agent -run 'TestMCPMutation' -count=1     PASS (2.8s)
go test ./pkg/mail ./services/api -count=1              PASS (0.1s / 0.5s)
go test ./pkg/database -run '^TestPostgresV117GUICrowlerArtifactMatchesCanonical$' -v   PASS
scripts/tests/prepare-postgresql-migration.sh           7/7 PASS
bash -n (helper and test)                               PASS
shellcheck                                              NOT AVAILABLE
git diff --check                                        PASS
```

The full repository suite was not run. The packages that consume the changed
SQL and the added test were run directly (`pkg/database`, plus the MCP store,
mail and API packages); all passed. The canonical migration bodies are
executable-identical to the baseline.

## 7. Disposable PostgreSQL 17 verification

Environment: `postgres:17` (17.12) on `127.0.0.1:55996`, runtime role `crowler`,
administrative `postgres`. Each test database was cloned from a fresh 1.17
install and then reduced to a simulated 1.16 state (63 tables, no
`SourceOwnership`, no claim columns, schema marker 1.16, writer
`reservation-v1.15`).

A temporary Go harness executed a whole file through one `database/sql`
connection (not `psql`), reproducing a script-capable GUI submission. It was
deleted after the evidence run.

| Case | Result |
| --- | --- |
| Shipped GUI artifact, one raw batch | exit 0; 78 tables; schema 1.17; writer `verified-ownership-v1.17`; 5 claim columns; `SourceOwnership` present; `md5(prosrc)=5bdd88efd2e200043c2fb1502be5ba3b`; 5 statement triggers with `deleted_links`; 63 `crowler`-owned tables |
| Reapply the same artifact | exit 0; same markers and digest (idempotent) |
| Canonical `psql -v CROWLER_DB_USER=crowler` route on a separate 1.16 clone | exit 0; identical markers, 78 tables, digest, 5 triggers and 63 `crowler`-owned tables |
| Custom role prepared copy (`crowler_ops`) | exit 0; schema 1.17, writer marker, digest; 11 tables owned by `crowler_ops` (the migration has 11 explicit `OWNER TO` statements) |
| `SET lock_timeout = '5s';` prepended to the artifact, executed as one batch while another session held `KeywordIndex` | batch failed after ~5.1 s with `canceling statement due to lock timeout` (`55P03`); schema stayed 1.16; `SourceOwnership` absent |

This proves the artifact, the canonical CLI route and the custom-role variant
converge to the same schema, and that a same-session lock bound aborts the whole
transaction without partial state.

## 8. Not run

- **Hosted GitHub CI on this SHA: NOT RUN.** No push was performed; this must be
  checked by the maintainer on the applied commit.
- **Any specific desktop GUI product: NOT RUN.** pgAdmin, DBeaver, SQLiteStudio
  and similar were not available; the guide states the requirements and marks
  products unverified.
- **Any hosted SQL console: NOT RUN.** Statement-splitting and
  transaction-pooling consoles remain explicitly unsupported.
- **MySQL/MariaDB runtime:** not available in this tree; not tested and not
  claimed.
- **A production or shared database migration:** not run.

## 9. Residual risks and follow-ups

1. `schemas/crowler-config-schema.json` still allows `database.type: mysql` while
   `NewHandler()` rejects it. Aligning the schema and the configuration
   validator is a contract change and needs its own decision.
2. The config type comment in `pkg/config/types.go` still lists `"mysql"`.
3. The shipped `mysql-setup.mysql` remains unloadable (reserved `rank`, MariaDB
   functional index, bare dash separators). DOC2 does not fix executable MySQL
   SQL.
4. The GUI artifact is bound to `"crowler"`. A custom-role administrator still
   needs the helper or `psql`; no no-shell custom-role route is claimed.
5. Hosted CI and real GUI-product testing remain evidence gaps. The raw-batch
   harness does not prove that a particular client configures its script mode
   correctly.

## 10. Documentation GO / NO-GO

**GO for documentation completeness**, on `78d2b0b8` plus this patch, subject to
the maintainer running hosted CI on the applied commit.

The default-role no-shell route exists, is discoverable, is mechanically bound
to the canonical migration and is tested against PostgreSQL 17. The CLI and GUI
routes produce identical schema outcomes. Conflicting README claims and the
material MySQL/MariaDB support claims on the main documentation path have been
corrected. The remaining gaps are explicitly named above and do not block the
documentation deliverable.

This statement is about documentation completeness only. It is not a claim that
2.3.0 has been tagged, published or deployed, and it is not a substitute for
hosted CI.
