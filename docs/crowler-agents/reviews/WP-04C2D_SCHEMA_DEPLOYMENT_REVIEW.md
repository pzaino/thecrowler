# WP-04C2D Schema Deployment and SQLite Integrity Review

**Deployment status: NOT APPROVED.** No DDL was executed against a shared or
production database. The schema remains 1.17 with writer generation
`verified-ownership-v1.17`.

## 1. Schema inventory

No schema change was made in WP-04C2D. The additive 1.17 MCP capability from
WP-04C2B/C remains:

- `MCPMutationProposals`
- `MCPMutationApprovals`
- `MCPMutationDispatchAttempts` (unused)
- `MCPMutationAudit`
- `MCPMutationRunQuota`
- `MCPMutationPrincipalQuota`
- `SchemaCapabilities`

Migration files:

- `pkg/database/db_migrations/postgresql-mcp-mutation-v1.17.pgsql`
- `pkg/database/db_migrations/sqlite-mcp-mutation-v1.17.sqlite3`

Clean-install blocks are present in `postgresql-setup.pgsql` and
`sqlite-setup.sqlite3`.

## 2. PostgreSQL test isolation

`pkg/database/mcp_mutation_postgres_integration_test.go` now:

- requires `CROWLER_MCP_POSTGRES_TEST_DISPOSABLE=1` and
  `CROWLER_MCP_POSTGRES_DSN`;
- creates a randomly named throwaway database through a maintenance
  connection;
- applies the additive migration twice and the clean-install MCP block;
- creates its sentinel table **inside the disposable test database**, never in
  the maintenance database;
- verifies pre-existing rows in that disposable database survive proposal and
  approval operations;
- closes test-created pools before cleanup;
- drops only the database it created, with `DROP DATABASE ... WITH (FORCE)`;
- reports cleanup failure with `t.Errorf`, so failed cleanup is visible.

There is no `TRUNCATE`, no sentinel DDL on `postgres`/caller databases, and no
fallback that mutates an arbitrary existing database. Missing CREATEDB
permission or connection failure fails closed with a precise diagnostic.

## 3. SQLite foreign-key behavior

Added deterministic evidence:
`TestSQLiteMCPMutationForeignKeysAreDeclaredButNotRuntimeEnforced`.

Observed current behavior:

- `SQLiteHandler.Connect` uses
  `file:%s?cache=shared&mode=rwc` and does not pass `_foreign_keys=on`.
- Two pooled connections both report `PRAGMA foreign_keys = 0`.
- A child row referencing a missing parent inserts successfully with FK off.
- Enabling `PRAGMA foreign_keys=ON` on connection 1 makes connection 1 reject
  the orphan, while connection 2 remains FK-off and still accepts it.
- The MCP approval table declares foreign keys, but the runtime pool does not
  enforce them by default.

This is a **decision blocker**, not silently fixed. Options:

1. enable `_foreign_keys=on` in the runtime DSN and run the full regression
   suite, including existing data with possible violations;
2. enforce MCP relationships in application transactions and document the
   divergence;
3. retain current behavior with an explicit operational caveat.

WP-04C2D does not change global SQLite DSN semantics.

## 4. Upgrade and rollback

Two distinct cases:

- base schema 1.16 -> 1.17: run the normal v1.17 base migration;
- already 1.17 without optional MCP capability: run the additive
  `*-mcp-mutation-v1.17.*` script. It does not change the schema version or
  writer generation.

Deployment prerequisites:

- capability marker/checksum verification;
- backup of proposal/approval/audit history;
- sufficient DDL privileges and a maintenance window;
- object-presence/capability checks before enabling routes;
- fail-closed behavior if any required object or marker is missing.

Rollback:

- removing/disabling the capability must not delete historical rows; the
  routes/stores fail closed if the marker or objects are absent;
- dropping the MCP tables would destroy proposal/approval/audit history and is
  not a normal rollback; a backup and explicit maintainer decision are
  required;
- no runtime DDL or destructive repair exists.

## 5. Test evidence

- PostgreSQL disposable integration (unique DB, no maintenance sentinel):
  PASS.
- Repeated migration and clean-install MCP block: PASS.
- Capability negative tests (missing index/constraint/type/marker): PASS.
- SQLite clean install/migration/restart/quota tests: PASS.
- SQLite FK runtime test: PASS and demonstrates FK-off pooled connections.

## 6. Open decision

Schema 1.17 MCP capability deployment and SQLite FK enforcement require separate
maintainer approval. A green test is not deployment permission.
