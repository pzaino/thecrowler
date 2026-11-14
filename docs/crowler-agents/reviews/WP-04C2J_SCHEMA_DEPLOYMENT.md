# WP-04C2J Schema Deployment and Rollback

## Implementation

- `pkg/database/mcp_mutation_deploy.go` embeds both additive v1.17 MCP
  migration scripts and provides the deployment, rollback, backup, privilege
  and SQLite foreign-key helpers.
- `cmd/dbDeploy` is the operator entry point and is included in the Events, API
  and Engine images.
- The migration is applied inside a transaction. PostgreSQL receives the
  quoted `-db-user` in place of `:CROWLER_DB_USER`; SQLite runs on a dedicated
  connection with foreign keys already enabled by the runtime DSN.
- `CheckMCPMutationCapability` remains the substantive verifier: tables,
  columns, named indexes, exact unique columns, foreign keys, PostgreSQL
  critical types and the marker checksum.
- `CheckStartupCompatibility` remains the base-schema verifier and now also
  fails closed on disabled SQLite foreign keys or existing FK violations.

## Apply

1. Verify the base schema is exactly `1.17`.
2. Verify startup compatibility.
3. Require `-backup-dir`; refuse to overwrite an existing backup path.
4. Back up the full database:
   - SQLite: `VACUUM INTO`.
   - PostgreSQL: `pg_dump --format=custom`, with `PGPASSWORD` in the child
     environment.
5. Apply the embedded migration in a transaction.
6. Verify capability marker/checksum and object probes.
7. Verify the runtime application user's table and sequence privileges.
8. Re-run startup compatibility.

The command is idempotent. Re-applying the capability is safe and is used in
the live test after a disable.

## Rollback

`dbDeploy -disable` deletes only:

```sql
DELETE FROM SchemaCapabilities
WHERE capability = 'mcp-mutation-authorization-v1'
```

It counts `MCPMutationProposals`, `MCPMutationApprovals`,
`MCPMutationDispatchAttempts` and `MCPMutationAudit` before and after and fails
if any count changed. The API and agent stores then fail closed because the
capability marker is absent. No table is dropped and no history is rewritten.

A full restore is an operator action using the backup. Dropping the MCP tables
is explicitly not a normal rollback.

## Privileges

- `-apply` uses an administrative connection for DDL, ownership and grants
  across the base schema.
- `-db-user` is the runtime application user. The migration sets ownership and
  grants to this user.
- `-check` verifies `has_table_privilege` and the audit sequence for `-db-user`.
- The application user receives `SELECT,INSERT,UPDATE` on proposal, approval,
  dispatch-attempt and quota tables, `SELECT,INSERT` on the audit table and
  `SELECT,INSERT` on `SchemaCapabilities`. The existing 1.17 migration also
  makes the application user the owner of those tables, so `-disable` can run
  as the marker-table owner; using an administrative connection is still the
  recommended maintenance pattern.

## Backup verification

- PostgreSQL: `pg_restore --list` shows the six MCP tables, the audit sequence
  and the MCP table data; `pg_restore --data-only --table=mcpmutationaudit`
  contains the live audit marker.
- SQLite: the backup is a standalone database file; querying
  `MCPMutationAudit` from the backup returned the live audit row.

## Schema contract

`RequiredSchemaVersion` remains `1.17`, `WriterGeneration` remains
`verified-ownership-v1.17`, and the capability checksum is unchanged. No new
migration file and no new schema version were added.

## Live evidence

- Disposable PostgreSQL 15.10: pre-check failed, apply/backup/check passed,
  history survived disable, post-disable check failed, re-apply restored the
  capability and preserved history. Container `dbDeploy` produced a valid
  custom-format `pg_dump`.
- SQLite: pre-check failed, apply/backup/check passed, history survived
  disable, post-disable check failed, re-apply restored the capability. The
  built `crowler-events` image ran the same check/apply against a mounted
  SQLite database.

See `docs/crowler-agents/schema-deployment.md` for the operator procedure and
`WP-04C2J_TEST_REPORT.md` for command results and log names.
