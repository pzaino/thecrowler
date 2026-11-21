# WP-04C2J Schema 1.17 Capability Deployment

## Contract

Schema remains `1.17`; writer generation remains
`verified-ownership-v1.17`. G3 does not add a schema version. It deploys the
already-defined additive MCP capability on an existing 1.17 database and
provides a non-destructive rollback.

Capability marker:

```text
mcp-mutation-authorization-v1
00cb16b3efc2a3a619335c4e3bc2b1c470bb34b2378d249a81e746cddc704eed
```

## Command

`cmd/dbDeploy` is included in the Events, API and Engine images.

```sh
# Read-only readiness check
dbDeploy -config config.yaml -check -db-user crowler

# Apply the additive capability, after a mandatory full backup
dbDeploy -config config.yaml -apply \
  -backup-dir /var/backups/crowler \
  -db-user crowler

# Disable only the capability marker; preserve every row
dbDeploy -config config.yaml -disable -db-user crowler
```

Flags:

| Flag | Meaning |
| --- | --- |
| `-config` | Configuration file. Default `config.yaml`. |
| `-check` | Verify schema 1.17, startup compatibility, capability marker/checksum, SQLite FK enforcement and application privileges. |
| `-apply` | Create a backup, apply the embedded additive migration, then verify it. Requires `-backup-dir`. |
| `-disable` | Delete only the `SchemaCapabilities` marker. Tables and history remain. |
| `-backup-dir` | Required for `-apply`; optional for `-disable`. |
| `-db-user` | Application user for ownership, grants and `has_table_privilege` checks. Defaults to `database.user`. |
| `-pg-dump` | PostgreSQL backup binary. Default `pg_dump`. |
| `-timeout` | Overall command timeout. Default 10 minutes. |

`-check`, `-apply` and `-disable` are mutually exclusive. No action defaults to
`-check`.

## Preconditions

- The base schema is already `1.17`. The command refuses `1.16` or earlier and
  tells the operator to apply the base v1.17 migration first.
- For PostgreSQL, the connection user must be able to create/alter/own the MCP
  objects and grant privileges to the application user. Use an administrative
  config for `-apply`; pass the runtime application user with `-db-user`.
  `-disable` can run as the capability-table owner (the application user under
  the existing 1.17 migration) or as an administrative user.
- For SQLite, the database file must already exist. Use the clean-install setup
  to create a new database.
- A maintenance window is recommended. The command does not stop running
  writers by itself.

## Apply sequence

1. `CheckSchemaVersion117`.
2. `CheckStartupCompatibility` (base schema, indexes, constraints, writer
   generation; SQLite foreign-key enforcement and integrity).
3. Create the backup directory and refuse to overwrite an existing backup.
4. Create a full database backup:
   - SQLite: `VACUUM INTO`.
   - PostgreSQL: `pg_dump --format=custom` with `PGPASSWORD` supplied via the
     process environment.
5. Apply the embedded additive migration in a transaction. PostgreSQL
   substitutes `:CROWLER_DB_USER` with the quoted `-db-user`; SQLite runs the
   migration on a dedicated connection.
6. Verify the capability marker/checksum and all object probes with
   `CheckMCPMutationCapability`.
7. Verify PostgreSQL table/sequence privileges for `-db-user`.
8. Re-run `CheckStartupCompatibility`.

The migration is idempotent and can be applied repeatedly. It does not change
the schema version or writer generation.

## Rollback

The normal rollback is:

```sh
dbDeploy -config config.yaml -disable -db-user crowler
```

`-disable` deletes only the capability marker. It counts the proposal, approval,
dispatch-attempt and audit rows before and after and refuses to report success if
any count changed. The API/agent code then fails closed because
`CheckMCPMutationCapability` cannot find the marker.

Dropping the MCP tables would destroy proposal/approval/audit history. That is
not a normal rollback. Use the full backup if a full restore is required, and
treat a table drop as an explicit maintainer decision with a tested restore.

## Backup and restore

- SQLite backup: `VACUUM INTO` produces a standalone database file.
- PostgreSQL backup: `pg_dump --format=custom` produces a `.dump` file
  restorable with `pg_restore`.
- The command refuses to overwrite an existing backup path.
- The backup is a full database backup, not only the MCP tables, so it also
  covers the base schema and unrelated history.
- The supplied images include `postgresql-client`; operators running the
  command outside the images need `pg_dump` available.

## Clean install

A clean install via `postgresql-setup.pgsql` or `sqlite-setup.sqlite3` already
contains the capability marker and tables. Run `dbDeploy -check` to verify the
installed contract. No `-apply` is required.

## Existing 1.17 without the capability

Run `dbDeploy -check`; it fails with a specific
`MCP mutation capability is not deployed` error and lists the missing objects.
Then run `dbDeploy -apply -backup-dir ...`. If the base schema is incomplete,
fix the base 1.17 migration first; the command will not apply the additive
capability on an incomplete base.

## Container behaviour

The Events, API and Engine images include the `dbDeploy` binary and
`postgresql-client`. The production binaries are built with `CGO_ENABLED=1` so
the SQLite driver is present. Examples:

```sh
docker run --rm -v /data:/data -v /backups:/backups \
  -v "$PWD/config.yaml:/app/config.yaml:ro" crowler-events \
  ./dbDeploy -config /app/config.yaml -check
```

The container user must be able to read/write the SQLite file and the backup
directory. For PostgreSQL, mount a config whose `database.host` resolves from
inside the container and provide a writable backup mount.
