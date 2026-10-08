# High-churn refresh_content hardening

Schema version **1.16** hardens CROWler's PostgreSQL behaviour for high-churn
`refresh_content` workloads, where whole WebObject replacement cycles
continuously delete and re-insert large artifact sets. Three independent
workstreams address the three costs such workloads create: long-lived
transactions, dead-tuple accumulation, and per-row cleanup triggers.

Each workstream is a separately reviewable change; together they ship as
schema v1.16 (`pkg/database/db_migrations/postgresql-migration-v1.16.pgsql`).

## 1. Bounded application transactions

**Problem.** Many write paths opened transactions with `Begin()` (background
context) and no deadline, so a stalled statement, lock wait, or operator pause
could hold a transaction open indefinitely. Open transactions pin the MVCC
snapshot horizon, which prevents vacuum from removing dead tuples anywhere in
the database.

**Change.**

- New configuration field `database.transaction_timeout` (seconds):
  default **300**, minimum 30, maximum 86400 (values outside the bounds are
  silently defaulted/clamped, matching the existing validation style). Declared
  in `schemas/crowler-config-schema.json` and `config.default`.
- `pkg/database/transaction.go` provides:
  - `TransactionContext(parent, *cfg.Config)` — derives a context carrying a
    deadline for `BeginTx`. It is nil-safe: a nil config or invalid timeout
    falls back to the fixed default (300s), so sites without configuration
    access are still bounded.
  - `IsTransactionTimeout(err)` — classifies deadline failures.
  - `LogTransactionOutcome(op, started, err)` — one log line per transaction
    outcome (debug level 4 on success, error level on failure, timeout-aware).
    Sites with fallible post-commit work log through a `txCommitted` flag so a
    late failure is not misreported as a transaction failure.
- All previously unbounded `Begin()`/`BeginTx(ctx)` sites in `pkg/crawler`,
  `cmd/`, `services/api`, and `pkg/database` now derive their transaction
  context through `TransactionContext` and roll back via `defer` (panic-safe).

**Explicitly not converted.** Sites that are already bounded by their caller
or whose semantics would be harmed by a default cap: event insertion (5s
attempt context), cardinality rebuild and aggregation retention (long
maintenance jobs that must exceed the per-transaction default), retry loops
that pass caller contexts, and short serializable mail-queue transactions.

**Role-level safeguards.** The application timeout complements — it does not
replace — server-side limits. For production roles, PostgreSQL operators
should still set:

- `idle_in_transaction_session_timeout` — backstop against forgotten idle
  transactions;
- `lock_timeout` — bound lock waits (deadlock victims surface quickly);
- `statement_timeout` — bound individual statements;
- PostgreSQL 17: `transaction_timeout` — server-side total transaction time;
  on PostgreSQL 15 only the client-side `transaction_timeout` config above is
  available.

## 2. Table-specific autovacuum for ObjectAttributes (and its TOAST)

**Problem.** `refresh_content` constantly replaces rows in `ObjectAttributes`.
With PostgreSQL's default `autovacuum_vacuum_scale_factor = 0.2`, a table
below ~50k rows accumulates 10k+ dead tuples before autovacuum acts, pinning
the MVCC horizon and delaying cleanup everywhere.

**Change** (both in `postgresql-setup.pgsql` and the v1.16 migration, applied
via `ALTER TABLE ... SET (...)`):

| Parameter | Value | Rationale |
| --- | --- | --- |
| `autovacuum_vacuum_scale_factor` | `0.005` | Vacuum reacts after ~0.5% growth instead of 20% |
| `autovacuum_vacuum_threshold` | `25000` | Floor so tiny tables are not vacuumed constantly |
| `autovacuum_analyze_scale_factor` | `0.002` | Keep planner statistics fresh under churn |
| `autovacuum_analyze_threshold` | `5000` | Analyze floor |
| `toast.autovacuum_vacuum_scale_factor` | `0.005` | Large TOASTed attribute values cleaned on the same schedule |
| `toast.autovacuum_vacuum_threshold` | `10000` | TOAST vacuum floor |

Notes:

- Table-level autovacuum parameters propagate to the TOAST table unless
  overridden; `toast.*` values are set explicitly so the behaviour is
  deterministic and visible in `pg_class.reloptions` instead of implied.
- PostgreSQL has no `toast.autovacuum_analyze_*` variants — analyze settings
  always propagate from the main table.
- Autovacuum stays enabled; no `VACUUM FULL` and no unrelated table tuning.

## 3. Statement-level artifact cleanup triggers

**Problem.** Deleting N WebObjects fired N per-row trigger invocations, each
re-running three cleanup statements — the cost scaled with batch size and
dominated bulk `refresh_content` deletes.

**Change.** `cleanup_artifact_data_set()` is one shared PL/pgSQL function
attached to `WebObjects`, `NetInfo`, and `HTTPInfo` as an `AFTER DELETE ...
FOR EACH STATEMENT` trigger with an `OLD TABLE` transition alias
(`REFERENCING OLD TABLE AS deleted_artifacts`):

- each `DELETE` statement is processed once, regardless of row count;
- `ObjectAttributes`, `EntityMemberships`, and `ObjectCorrelations` cleanup
  uses set-based `DELETE ... USING` joins, all covered by the existing
  `(object_type, object_id)` and `idx_objectcorrelations_obj1/_obj2` indexes;
- correlations are cleaned with two statements (one per correlation side) so
  each can use its own composite index instead of an `OR` predicate;
- the identifier column (`object_id`/`netinfo_id`/`httpinfo_id`) is resolved
  dynamically (`format(... %I ...)`), because a transition table exposes only
  its source table's columns — a static `CASE` referencing another table's
  column fails to parse when the trigger fires on `WebObjects`;
- `TRUNCATE` behaviour is unchanged (statement `DELETE` triggers do not fire
  for truncation, matching the previous per-row behaviour);
- rollback semantics are unchanged: cleanup participates in the deleting
  transaction (verified by integration test).

Legacy per-row triggers and `cleanup_artifact_data()` are dropped by both the
setup script (converging re-runs) and the migration. The setup and migration
copies of the function body are asserted byte-identical by
`TestPostgresStatementLevelCleanupTriggerContract`, guaranteeing fresh
install ≡ upgraded database.

SQLite keeps its per-row `trg_cleanup_*` triggers (no transition-table
support); MySQL/MariaDB previously had no cleanup triggers and are unchanged.

## Schema version 1.16 and upgrade procedure

- `RequiredSchemaVersion` is now `1.16`; writers refuse any other current
  version at startup (`CheckStartupCompatibility`).
- `db_migrations/postgresql-migration-v1.16.pgsql` is one atomic,
  re-runnable transaction: autovacuum parameters, trigger swap, version
  stamp.
- SQLite and MySQL receive ledger-only v1.16 migration files so upgraded
  databases reach the same version as fresh installs.
- Upgrade procedure (unchanged fleet discipline): stop all writers, apply
  migrations through v1.16, restart only v1.16-compatible services.

## Verification

```sh
go test ./pkg/config/ ./pkg/database/ ./pkg/crawler/ ./cmd/... ./services/...

THECROWLER_POSTGRES_INTEGRATION=1 DOCKER_POSTGRES_DB_PORT=<port> \
  go test -tags=integration -count=1 ./pkg/database/...
```

The integration suite includes `TestPostgresStatementLevelArtifactCleanup`
(bulk/single deletes, unrelated-row preservation, rollback restoration,
statement-level `pg_trigger` shape, version stamp) and
`TestPostgresCurrentVersionUniquenessAndCardinalityPermissions`.
Static contracts live in `schema_sql_test.go`. CI applies the v1.16
migration twice (fresh + repeat) and asserts the current version before
running the integration suite.
