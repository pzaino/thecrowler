# Migrating CROWler's DB

> **Authoritative operator guide:** [Database migrations](../../../docs/database-migrations.md).
> It covers prerequisites, backups, the CLI route, the ready-made GUI file for
> the standard `crowler` role, custom-role generation, postchecks, rollback and
> the engine support matrix. Read it before running anything here.
>
> The rest of this file is developer-facing migration detail and change history.

This directory contains the SQL migration scripts. Each file is named after the
schema version it applies to, for example `postgresql-migration-v1.14.pgsql`.
Back up the database before running one, and use an account with the privileges
described below.

The PostgreSQL v1.17 directory also ships
`postgresql-migration-v1.17.gui-crowler.sql`, the mechanical GUI copy for the
standard `crowler` role. It is regenerated from the canonical migration and
guarded by `TestPostgresV117GUICrowlerArtifactMatchesCanonical`; do not edit it
by hand.

## Engine support in 2.3.0

| Engine | 2.3.0 runtime | Migration SQL | Notes |
| --- | --- | --- | --- |
| PostgreSQL | **supported (primary)** | `postgresql-migration-*.pgsql` | release target |
| SQLite | **supported** | `sqlite-migration-*.sqlite3` | the runtime repairs the Information Seed claim columns after the SQL runs |
| MySQL / MariaDB | **not available** | `mysql-migration-*.mysql` | `pkg/database.NewHandler()` rejects `database.type: mysql` and the handler is commented out for licensing reasons; the SQL is retained for reference and future work |

The presence of MySQL SQL files is not runtime support. Separately, the shipped
`mysql-setup.mysql` is not loadable as-is on MySQL 8.0 (reserved word `rank`) or
MariaDB 11 (functional index), and `mysql-setup.mysql` plus the v1.09/v1.11
migrations contain bare dash separator lines that MySQL parses as errors rather
than comments. Those are recorded limitations, not passing paths.

## Privileges

There is no single required login name. A migration needs an account that can
perform the operations in that engine's script:

- PostgreSQL: create and alter tables and indexes, replace functions and
  triggers, change table ownership and `GRANT` to the runtime role. In practice
  this is the database owner or an administrator. The runtime role named by
  `CROWLER_DB_USER` must already exist; the migration does not create roles.
- SQLite: write access to the database file, with no other writer active.
- MySQL/MariaDB: DDL and routine privileges, if the scripts are used outside a
  supported 2.3.0 runtime.

## Version chain and markers

Apply the files that exist, in version order. PostgreSQL ships 1.08 through 1.17.
SQLite and MySQL/MariaDB ship no 1.10 or 1.12 file; do not skip an existing file
to compensate.

A script only applies the change for the version it names. Running the v1.17 file
alone does not upgrade an older database.

The v1.17 migration finishes with:

- `DBSchemaVersion.version = '1.17'`;
- `DatabaseWriterCompatibility.writer_generation = 'verified-ownership-v1.17'`.

The writer generation is part of the upgrade. A 1.16 database used
`reservation-v1.15`; v1.17 writes `verified-ownership-v1.17`. Re-running v1.17 is
idempotent, but it does not preserve the pre-1.17 writer marker. Stop every old
writer before applying it, then start only v1.17-compatible services.

Later sections describe the 1.17 contents and the optional add-ons.

Current schema target: **1.17**. The v1.17 migration adds verified source
ownership, durable approvals, and Information Seed claim fencing, lease
heartbeats, explicit retry metadata and a bounded terminal exhausted state. The
PostgreSQL migration is transactional and safe to re-run. SQLite uses runtime
guarded column repair because its supported versions cannot conditionally add
columns in SQL; the SQL ledger is safe to re-run. The migration does not
bulk-verify historical `usr_id` values.

The unreleased 1.17 baseline also contains the asynchronous goal persistence
tables (`AgentGoalRuns`, `AgentGoalRunTools`, `AgentGoalRunSources`,
`AgentGoalRunWork`, `AgentGoalRunEvidence`,
`AgentGoalRunVerificationEvidence`, `AgentGoalRunAuditRefs` and
`AgentGoalRunTransitions`). A fresh install and the v1.16 -> v1.17 migration
create them. The database release stays **1.17** and the writer generation stays
**`verified-ownership-v1.17`**.

Development databases already stamped 1.17 from an earlier in-development
baseline can add the missing goal objects with:

- `postgresql-agent-goal-v1.17.pgsql`
- `sqlite-agent-goal-v1.17.sqlite3`

These are manual, opt-in, administrator scripts. For PostgreSQL run them with
an account that can create objects and transfer ownership (normally the
database owner or an administrator) and `-v CROWLER_DB_USER=<runtime role>`; the
script creates the objects and transfers ownership and grants to the runtime
role. For
SQLite, run the script with the `sqlite3` command-line tool while no service is
writing. The scripts do not change `DBSchemaVersion` or
`DatabaseWriterCompatibility`, are safe to re-run, preserve goal history, and
fail closed when a same-named table, column, index, foreign key or check
constraint is structurally incompatible. They are not applied automatically at
startup.

The unreleased 1.17 baseline also contains the optional fenced source-crawl
attempt objects (`SourceCrawlClaims`, `SourceCrawlAttempts` and
`SourceCrawlClaimPolicy`) and the `source-crawl-attempt-v1` capability marker.
Fresh installs and the v1.16 -> v1.17 migration create them. The default policy
mode is `legacy`; while `fenced`, the PostgreSQL `update_sources(...)` function
returns no rows to old workers. Its seven-argument signature and eight-column
result are unchanged.

Development databases already stamped 1.17 can add the missing attempt objects
with:

- `postgresql-source-crawl-attempt-v1.17.pgsql`
- `sqlite-source-crawl-attempt-v1.17.sqlite3`

These addons are manual, opt-in, administrator scripts. Run the PostgreSQL addon
with an account that can create objects and transfer ownership (normally the
database owner or an administrator) and `-v CROWLER_DB_USER=<runtime role>`; it
creates the objects, installs the fenced-mode-aware `update_sources` function
and transfers ownership/grants. Run the SQLite addon with `sqlite3` while no service
is writing. Both scripts are safe to re-run, preserve existing attempt rows and
an existing `fenced` policy mode, and fail closed on incompatible same-named
objects. They do not change `DBSchemaVersion` or
`DatabaseWriterCompatibility` and are not applied automatically at startup.

The current claim references exactly one history row through the composite
`(source_id, generation, attempt_id)` foreign key; the split attempt-ID and
source/generation references from the first Stage B draft are upgraded away.
The PostgreSQL addon drops only those old foreign keys, adds the exact unique
key and reference, and fails the transaction if existing rows are inconsistent.
The SQLite addon performs a transactional, data-preserving rebuild of the two
attempt tables and aborts if a current claim does not match its history row.

`SourceCrawlClaimPolicy` is owned by the deploying administrator and the runtime
claim role receives SELECT only. Operators change `legacy`/`fenced` with:

```sql
SELECT set_source_crawl_claim_mode('fenced');
SELECT set_source_crawl_claim_mode('legacy');
```

That function takes the exclusive advisory lock; claim transactions and
`update_sources` take the shared lock. A direct table UPDATE bypasses the
protocol and is not a supported cutover operation. The runtime role cannot
execute the operator function or update the policy table directly.

The claim API remains internal and opt-in. Ordinary startup does not require the
optional attempt objects. MySQL has no attempt tables, and the 2.3.0 runtime
does not enable MySQL at all (see the engine support matrix above).

The MySQL scripts still stamp 1.17, but the 2.3.0 runtime cannot start on
MySQL/MariaDB. Asynchronous goal persistence is also unsupported there; the
MySQL scripts contain no goal tables.

The migration files are source artifacts. They are not approval to apply DDL to
a persistent, shared, staging or production database. Take a backup, rehearse on
a disposable database, and obtain separate deployment authorization first.

WP-05B3C3A changes no schema object. The replay fix uses the existing attempt
and policy columns. The authenticated crawler handoff and fenced terminal-result
protocol is design-only; if a future implementation needs a durable request
fingerprint, an empty-batch acknowledgement or a terminal-receipt ledger, that
must be an additive migration or same-version addon approved separately. Keep
`RequiredSchemaVersion='1.17'` and
`WriterGeneration='verified-ownership-v1.17'`.

WP-05B3C3B1 adds no DDL. Its authority, clock and replay corrections use the
existing 1.17 objects. An optional canonical receipt digest, persisted
`ExpectedState`, original unbound class, request fingerprint or empty-batch
acknowledgement is a separately requested additive 1.17 extension and is **not
approved or implemented**. Keep `RequiredSchemaVersion='1.17'`,
`WriterGeneration='verified-ownership-v1.17'` and the current
`source-crawl-attempt-v1` checksum.

WP-05B3C3B1R adds no DDL. Its source-status, duplicate current/history field,
per-source claim-clock and replay-structure corrections use existing 1.17
objects. B1 Option A remains unapproved; any stronger request/receipt or
owner-continuity storage remains a separate B3 design-only request.

WP-05B3C3B1S adds no DDL. Its all-class final-freshness and terminal
observation-time corrections also use existing 1.17 objects.

WP-05B3C3B1 B1 adds no DDL. The internal heartbeat and terminal-result
repository uses the existing 1.17 `SourceCrawlClaims`, `SourceCrawlAttempts`,
`SourceCrawlClaimPolicy`, `Sources`, `SourceOwnership`, `CrawlProposals`,
`CrawlApprovals` and `Status` objects. B2 is a separate pending crawler handoff;
B3 additive schema remains unapproved.

WP-05B3C3B2 adds no DDL. The live crawler handoff, heartbeat, cancellation and
unbound operational source-status update use the existing 1.17 objects and the
B1 repository. B3 additive schema remains unapproved.

WP-05B3C3B2R adds no DDL. The authoritative pre-start/transaction-held fence
validation, unbound owner-continuity rule and operation-state compatibility all
use the existing schema-1.17 objects. A durable unbound claim-owner field or an
atomic `refresh_content` replacement remains a separately gated B3 decision.
