# WP-04C2J SQLite Foreign-Key Enforcement

## Decision

G3 enables `_foreign_keys=on` for every SQLite runtime connection and fails
closed when the database has pre-existing orphan rows. It does not silently
delete or rewrite existing data.

## Runtime change

`SQLiteHandler.Connect` now opens:

```text
file:<dbname>?cache=shared&mode=rwc&_foreign_keys=on
```

The DSN applies to every connection created by `database/sql`, not only the
setup or migration connection. `SQLiteHandler` remains the same handler type;
no query API or schema contract changes.

## Startup integrity gate

`CheckStartupCompatibility` now calls
`CheckSQLiteForeignKeyEnforcement` after the existing table/index/constraint
probes. The helper:

1. verifies `PRAGMA foreign_keys = 1` on the runtime connection;
2. runs `PRAGMA foreign_key_check`;
3. reports up to ten violations with table, rowid, parent table and FK id;
4. fails the startup compatibility check when either condition fails.

This makes an existing database with orphan rows fail closed before the service
starts writers. The operator must repair the data (or restore a consistent
backup) before retrying. There is no automatic destructive repair.

## Existing-data regression

- The full `sqlite-setup.sqlite3` schema was loaded into a fresh SQLite
  database with foreign keys enabled; `PRAGMA foreign_key_check` returned no
  rows.
- The full Go suite passed with the new DSN, including MCP, crawl, ownership,
  information seed, time-series, API and Events tests.
- `TestSQLiteRuntimeForeignKeysEnforcedOnPooledConnections` proves two pooled
  connections both report `foreign_keys=1` and both reject orphan inserts.
- `TestSQLiteMCPMutationForeignKeysAreRuntimeEnforced` proves the MCP approval
  table declares foreign keys and the runtime rejects orphan relationships.
- `TestStartupCompatibilityRejectsDisabledSQLiteForeignKeys` and
  `TestStartupCompatibilityRejectsSQLiteForeignKeyViolations` prove the
  fail-closed startup diagnostics.
- `TestSQLiteForeignKeyEnforcementRejectsOrphansAndReportsViolations` proves a
  database created with foreign keys off and containing an orphan is reported
  as a violation when reopened through the runtime handler.

## Build requirement

The SQLite driver is provided by `github.com/mattn/go-sqlite3`, which requires
CGO. `pkg/database/sqlite_driver.go` imports it under the `cgo` build tag.
The three production Dockerfiles set `CGO_ENABLED=1` and install `gcc
musl-dev` in the builder stage. `autobuild.sh` honors the caller's
`CGO_ENABLED`, so the static cross-build workflow can remain `CGO_ENABLED=0`
while the images build with CGO. CGO-disabled builds still compile but do not
include SQLite support; the Docker images use CGO so the runtime and
`dbDeploy` can open SQLite databases.

## Operational caveat

`PRAGMA foreign_key_check` scans the database at startup. On a very large
SQLite database this is an intentional one-time integrity cost. It is the
fail-closed gate that prevents enabling enforcement on inconsistent data.
