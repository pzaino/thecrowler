# Migrating CROWler's DB

> **Start here:** [Database migrations](../../../docs/database-migrations.md) is
> the operator guide. It covers the full chain, prerequisites, backups, the
> PostgreSQL `:CROWLER_DB_USER` token and the `scripts/prepare-postgresql-migration.sh`
> helper, GUI/SQL-console execution, MySQL `DELIMITER` handling, SQLite runtime
> repair, postchecks and rollback. The notes below are developer-facing detail.

This directory contains SQL migration scripts for updating the CROWler database schema. Each migration script is named according to the version it applies to, e.g., `postgresql-migration-v1.14.pgsql`. Before running a migration, ensure that you have backed up your database and that you have the necessary permissions to alter the schema.

A migration script only applies changes from the previous release to the one it declares it updates. In other words you cannot migrate directly from version 1.12 to 1.14 without first applying the 1.13 migration.

To migrate you must use the `postgres` admin account, not the `crowler` one.

Each migration script should be executed in the order of their version numbers to ensure the database schema remains consistent. After running a migration, verify that the changes have been applied correctly before proceeding to the next one.

If you encounter any issues during the migration process, consult the migration script for details on the changes being applied and check the database logs for any errors. It is recommended to test the migration on a staging environment before applying it to the production database.

BEFORE you run a migration script open it and read it, sometimes I have to leave special instructions or notes that are crucial for the migration to succeed.

Current schema target: **1.17**. The v1.17 migration adds verified source
ownership, durable approvals, and Information Seed claim fencing, lease
heartbeats, explicit retry metadata and a bounded terminal exhausted state. It is
additive and keeps the existing writer-generation marker. Stop every old writer
before applying it, then start only v1.17-compatible services. The PostgreSQL
migration is transactional and safe to re-run. SQLite uses runtime guarded
column repair because its supported versions cannot conditionally add columns in
SQL; the SQL ledger is safe to re-run. MySQL uses guarded column/index procedures
and is safe to re-run. The migration does not bulk-verify historical `usr_id`
values.

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
the `postgres` admin account and `-v CROWLER_DB_USER=<runtime role>`; the script
creates the objects and transfers ownership and grants to the runtime role. For
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
with the `postgres` account and `-v CROWLER_DB_USER=<runtime role>`; it creates
the objects, installs the fenced-mode-aware `update_sources` function and
transfers ownership/grants. Run the SQLite addon with `sqlite3` while no service
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
optional attempt objects, and MySQL remains legacy-only with no attempt tables.

MySQL keeps its existing 1.17 startup and migration behavior. Asynchronous goal
persistence is deliberately unsupported on MySQL in this phase; the MySQL
scripts contain no goal tables.

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
