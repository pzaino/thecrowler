# WP-05B2B1 Migration Review

## Paths that must converge

| Path | PostgreSQL | SQLite |
| --- | --- | --- |
| Fresh 1.17 install | `pkg/database/postgresql-setup.pgsql:4277` | `pkg/database/sqlite-setup.sqlite3:1751` |
| v1.16 -> v1.17 | `pkg/database/db_migrations/postgresql-migration-v1.17.pgsql:364` | `pkg/database/db_migrations/sqlite-migration-v1.17.sqlite3:129` |
| Development DB already stamped 1.17 | `pkg/database/db_migrations/postgresql-agent-goal-v1.17.pgsql:10` | `pkg/database/db_migrations/sqlite-agent-goal-v1.17.sqlite3:7` |

All three paths embed the same canonical goal block. The drift test
`TestAsyncGoalPersistenceCanonicalDDLConverges`
(`pkg/database/async_goal_schema_test.go:305`) extracts the block from every
file and compares it byte-for-byte.

## PostgreSQL

The fresh install and the v1.16 -> v1.17 migration create the goal tables,
indexes and constraints before the final schema/writer marker update. The
migration remains wrapped in `BEGIN`/`COMMIT` and retains its existing
transactional behavior. The goal block is additive and uses `IF NOT EXISTS` for
tables and indexes.

The same-version addon:

1. opens a transaction;
2. takes `pg_advisory_xact_lock(hashtext('crowler:async-goal-persistence-v1'))`;
3. creates the canonical block;
4. runs a `DO` guard that verifies the schema and writer markers, required
   columns, forbidden raw-content columns, required indexes, unique-column
   sets, primary keys, named foreign keys and their parent/column/cascade
   targets, and named CHECK constraints;
5. transfers table ownership to `:CROWLER_DB_USER` and grants the runtime
   privileges;
6. commits.

The addon is an administrator operation. The runtime role does not have
`CREATE` on the database or schema, so it cannot create the missing tables
itself. Run it as `postgres` (or another role with the required DDL rights) and
pass the runtime role with `-v CROWLER_DB_USER=<role>`.

The fail-closed guard is at
`pkg/database/db_migrations/postgresql-agent-goal-v1.17.pgsql:340`. It checks
constraints by schema and table before joining names, so an identical
constraint name in another schema does not create a false positive.

## SQLite

The fresh install and v1.16 -> v1.17 migration use the same canonical block.
The addon uses:

```sql
PRAGMA foreign_keys = ON;
BEGIN IMMEDIATE;
```

The canonical block is followed by a temporary guard table and trigger. The
trigger's `WHEN` clause fails the insert if:

- the schema or writer marker is wrong;
- a required column is missing;
- a forbidden raw-content column exists;
- a required index or unique-column set is missing;
- a primary key or foreign key is missing;
- a foreign key points at the wrong parent, columns or delete action;
- a named CHECK constraint is missing.

The guard is at
`pkg/database/db_migrations/sqlite-agent-goal-v1.17.sqlite3:326`. SQLite has no
conditional table alteration here, so a structurally incompatible same-named
object is rejected rather than repaired. The addon is manual and is not called
at startup.

## Failure and re-run behavior

| Scenario | PostgreSQL | SQLite |
| --- | --- | --- |
| Apply twice | Tables and indexes use `IF NOT EXISTS`; guard re-validates; ownership/grants are reapplied. | `IF NOT EXISTS` and `BEGIN IMMEDIATE`; guard re-validates. |
| Existing goal rows | Preserved; no `DROP`, `TRUNCATE` or data update in the block. | Preserved; no destructive statement in the block. |
| Missing object | Created. | Created. |
| Incompatible same-named object | Guard raises an exception and the transaction rolls back. | Guard trigger raises `ABORT` and the transaction rolls back. |
| Interrupted transaction | PostgreSQL rolls back the whole DDL transaction. | SQLite rolls back the `BEGIN IMMEDIATE` transaction. |
| Markers | No `UPDATE DBSchemaVersion` or `UPDATE DatabaseWriterCompatibility` in the addon. | Same. |

## Rehearsal evidence

The following disposable rehearsals were executed. They are not production
migration evidence.

- PostgreSQL 17.12: fresh setup; v1.16 -> v1.17 migration; same-version addon
  from a database with the goal tables removed; addon re-run with a goal row
  present; ownership/grants applied to the `crowler` runtime role.
- PostgreSQL 17.12: addon applied inside a transaction and rolled back; no goal
  tables remained.
- PostgreSQL 17.12: addon applied to a temporary schema, one named CHECK
  constraint dropped, and the addon re-run; the guard rejected the schema.
- SQLite: fresh setup; v1.16 -> v1.17 migration; addon from a database with the
  goal tables removed; addon re-run with a goal row present; invalid timestamp
  and missing-CHECK cases rejected.
- `psql` full addon script applied twice with
  `-v CROWLER_DB_USER=crowler`; one goal row and one transition were preserved,
  and the markers stayed `1.17` / `verified-ownership-v1.17`.
- `sqlite3` full addon script applied twice; one goal row and one transition
  were preserved, and the markers stayed `1.17` / `verified-ownership-v1.17`.

## Deployment boundary

The migration and addon files are source artifacts. They are not approval to
apply DDL to a persistent, shared, staging or production database. No automatic
startup migration exists. A deployment must take a backup, rehearse on a
disposable database and obtain separate authorization.

## Known limitations

- The PostgreSQL addon requires an administrative role; this is intentional.
- Existing development databases containing an earlier, differently named
  goal-table variant will fail closed and require a deliberate drop/recreate or
  a separately reviewed repair.
- The migration does not prune or archive existing data.
- The migration does not create MySQL goal tables; MySQL goal persistence is
  unsupported.
