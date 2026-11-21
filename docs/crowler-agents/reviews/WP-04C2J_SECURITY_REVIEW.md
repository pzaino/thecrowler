# WP-04C2J G3 Security Review

## Scope

SQLite foreign-key enforcement, additive schema-1.17 MCP capability
deployment, mandatory backup, privilege verification, non-destructive rollback
and the CGO SQLite production build. No new network service, dispatcher or
external mutation is added.

## Threats and controls

| Threat | Control |
| --- | --- |
| SQLite connections silently bypass declared foreign keys | Runtime DSN always includes `_foreign_keys=on`; startup compatibility verifies `PRAGMA foreign_keys=1` on a pooled connection |
| Existing orphan rows are hidden by enabling enforcement | Startup runs `PRAGMA foreign_key_check` and fails closed with table/rowid/parent details; no automatic deletion or repair |
| Migration is applied to the wrong schema version | `dbDeploy` refuses anything other than base schema 1.17 and tells the operator to apply the base migration first |
| Capability marker alone certifies a broken schema | `CheckMCPMutationCapability` still probes tables, columns, indexes, unique columns, foreign keys, PostgreSQL types and the checksum; the marker is not the only guard |
| Deployment runs without a recoverable copy | `-apply` requires `-backup-dir`; the command creates a full database backup and refuses to overwrite an existing path |
| Backup path injection or overwrite | Backup paths are derived from a sanitized database name plus a UTC timestamp; existing paths are rejected; SQLite `VACUUM INTO` quotes the path; PostgreSQL uses `exec.CommandContext` with argument arrays and no shell |
| Password appears in process arguments or logs | PostgreSQL `PGPASSWORD` is passed via the child process environment, not the `pg_dump` command line; the command reports the backup path, not credentials |
| Partial migration leaves a half-applied capability | PostgreSQL migration runs in one transaction; SQLite uses a dedicated connection and one transaction; all statements are idempotent and re-runnable |
| Application user lacks runtime privileges after deployment | `CheckMCPMutationPrivileges` checks every MCP table and the audit sequence for the configured `-db-user` |
| Rollback destroys proposal/approval/audit history | `-disable` deletes only the capability marker and refuses success if any MCP history row count changed; table drops are explicitly out of scope |
| Marker deletion requires excessive application privileges | `-apply` uses an administrative connection for base-schema and cross-role DDL; the existing 1.17 migration makes the application user the owner of `SchemaCapabilities`, so marker maintenance does not require an additional grant boundary |
| CGO-disabled production binary silently loses SQLite support | `sqlite_driver.go` imports the driver under the `cgo` build tag; the three Dockerfiles set `CGO_ENABLED=1` and install `gcc musl-dev`; `autobuild.sh` honors the caller's CGO setting so static cross-builds remain CGO=0; SQLite checks fail closed if the driver is absent |
| Container lacks the backup tool | Runtime images install `postgresql-client`; live container testing confirmed `pg_dump` and `dbDeploy` work from the Events image |
| Startup foreign-key scan causes unbounded work | The scan is a deliberate one-time startup integrity gate; the maintenance command has a 30-second SQLite cleanup timeout and a 10-minute overall command timeout |
| Operator accidentally runs against production | The command is explicit, requires `-apply`/`-disable`, provides `-check`, and never runs automatically from the services |

## Live evidence

### PostgreSQL

A disposable PostgreSQL 15.10 database was installed with the full
`postgresql-setup.pgsql` schema, then the MCP objects and marker were removed to
simulate an existing 1.17 database without the additive capability.

- `dbDeploy -check` failed with `MCP mutation capability is not deployed` and a
  precise missing-object list.
- `dbDeploy -apply -backup-dir ... -db-user crowler` created a 518 KB custom
  `pg_dump` backup, applied the migration, verified the capability/checksum,
  verified `crowler` table/sequence privileges and passed post-deployment
  compatibility.
- A history row inserted as `crowler` survived.
- `dbDeploy -disable` removed exactly one marker, reported
  `history_preserved=true`, and left the audit row intact. `-check` then failed
  with the expected missing-marker error.
- Re-applying the capability succeeded and the history row remained.

### SQLite

A SQLite database was created from `sqlite-setup.sqlite3`, then the MCP objects
and marker were removed.

- `dbDeploy -check` failed with the expected missing capability.
- `dbDeploy -apply` created a `VACUUM INTO` backup and deployed the capability.
- `dbDeploy -check` passed, including the startup foreign-key enforcement and
  `PRAGMA foreign_key_check` gate.
- A history row inserted after deployment survived `dbDeploy -disable`;
  `-check` then failed as expected.
- The same `-check`/`-apply`/`-check` sequence was run from inside the built
  `crowler-events` image, proving the image includes `dbDeploy`, the CGO SQLite
  driver and the runtime DSN.

### Container PostgreSQL

The built `crowler-events` image ran `dbDeploy -check` and
`dbDeploy -apply -backup-dir /g3/...` against the disposable PostgreSQL server.
The image's `pg_dump` produced a valid PostgreSQL custom-format dump.

## Residual risks

- Enabling SQLite foreign keys on an existing database can expose orphan rows
  that previously went unnoticed. The startup gate fails closed and the
  operator must repair or restore data; G3 does not delete or rewrite it.
- `PRAGMA foreign_key_check` is a full scan. Very large SQLite databases will
  have a longer startup compatibility check.
- `-apply`/`-disable` require an administrative connection for DDL/owner and
  marker deletion. Running them with the runtime application account may fail
  closed; that is intentional least-privilege behaviour.
- The full database backup can be large. Operators should size the backup mount
  and maintenance window accordingly.
- Full restore is an operator procedure using the backup; `dbDeploy` does not
  run `pg_restore` or overwrite an existing database automatically.
- SQLite support now requires a CGO build. CGO-disabled builds remain
  compilable but cannot open SQLite; the production images use CGO.
- The existing `pkg/ws` test flake was observed once during the full suite and
  passed in isolation and on rerun; it is unrelated to G3 but remains a
  test-stability risk.
