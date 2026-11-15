# WP-05B2B1 Compatibility Review

## Global startup contract

The existing startup contract is unchanged:

- `RequiredSchemaVersion = "1.17"` at
  `pkg/database/startup_compatibility.go:12`;
- `WriterGeneration = "verified-ownership-v1.17"` at
  `pkg/database/startup_compatibility.go:14`.

`CheckStartupCompatibility` still validates the existing reservation schema,
the active schema row, the writer generation and SQLite foreign-key
enforcement. It does **not** require the asynchronous-goal tables.

`TestAsyncGoalPersistenceReadinessDoesNotAffectStartup`
(`pkg/database/async_goal_schema_test.go:389`) builds a valid 1.17 startup
fixture with no goal tables, verifies that ordinary startup succeeds, and then
verifies that the separate goal readiness check fails closed.

## Separate goal readiness check

`CheckAsyncGoalPersistenceSchema` is defined at
`pkg/database/async_goal_schema.go:138`. It is read-only and verifies:

- active schema `1.17`;
- writer generation `verified-ownership-v1.17`;
- required columns and absence of forbidden raw-content columns;
- required index names;
- unique-column sets;
- primary keys;
- named foreign keys;
- named CHECK constraints;
- PostgreSQL table privileges for the current user;
- SQLite `PRAGMA foreign_keys` and `PRAGMA foreign_key_check`.

It is deliberately not called by `CheckStartupCompatibility`, the agent,
crawler or Events startup paths. The future transactional store must call it
before opening a write path.

## Backend policy

`AsyncGoalPersistenceSupported` (`pkg/database/async_goal_schema.go:22`)
returns true for PostgreSQL and SQLite and false for every other backend.
`CheckAsyncGoalPersistenceSchema` returns the typed
`ErrAsyncGoalPersistenceUnsupported` before querying a MySQL database.

MySQL behavior is unchanged:

- `pkg/database/mysql-setup.mysql` still stamps 1.17 and
  `verified-ownership-v1.17`;
- `pkg/database/db_migrations/mysql-migration-v1.17.mysql` still stamps the
  same values;
- neither MySQL file contains an `AgentGoal` table;
- `TestAsyncGoalPersistenceMySQLIsUnsupported`
  (`pkg/database/async_goal_schema_test.go:377`) verifies the typed unsupported
  result without opening a MySQL connection.

## Existing schema protection

The following protections remain intact and pass:

- `TestSchemaVersion117LedgerCoverage` checks the 1.17 stamp in every active
  setup and v1.17 migration file.
- `TestNoSchemaVersionAfter117` rejects migration filenames or setup markers
  for 1.18, 1.19 or 2.x.
- Existing writer-generation checks and historic migration tests remain in the
  suite.
- `TestAsyncGoalPersistenceKeepsSchema117`
  (`pkg/database/async_goal_schema_test.go:328`) adds a targeted check over the
  six goal-related SQL files for forbidden version literals and MySQL goal
  tables.

No existing assertion was weakened or deleted.

## Runtime compatibility

No new runtime path calls the asynchronous-goal kernel or enables execution:

- no store, CAS adapter, worker, claim, lease, heartbeat, resume, retry or
  reconciliation loop;
- no event consumer, API endpoint, Events payload or manifest field;
- no MCP mutation dispatch;
- no automatic migration at startup.

The only runtime addition is the exported read-only readiness function and the
backend support predicate.

## Development-database compatibility

A development database already stamped 1.17 can acquire the goal objects with
the same-version addon and remains stamped 1.17 with writer generation
`verified-ownership-v1.17`. The addon is manual, administrator-run and
re-runnable.

Because ordinary startup does not require the goal tables, a 1.17 development
database without the addon continues to serve non-async workloads. This avoids
stranding existing services while async execution remains disabled.

## Residual compatibility risks

- A future store that writes goal rows must also enforce cross-table kernel
  invariants that SQL cannot express.
- A deployment that applies the addon while old writers are active is outside
  the approved procedure; stop writers first.
- MySQL goal persistence remains unavailable by design.
- GitHub CI has not yet run this patch. Local PostgreSQL 17.12 and SQLite
  rehearsals are reported in `ASYNC_GOAL_B2B1_TEST_REPORT.md`.
