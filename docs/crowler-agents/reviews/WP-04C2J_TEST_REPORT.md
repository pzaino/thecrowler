# WP-04C2J G3 Test Report

## Environment

- Repository: `pzaino/thecrowler`
- Branch: `develop`
- HEAD: `479803aa9c74e6b5f0c09ced881a7191c6574afd` (unchanged; no commit)
- Go: `go1.27.3 linux/amd64`
- Docker: `29.7.2-ce`; Compose `v5.6.1`
- PostgreSQL: `15.10`
- Release: `v2.3.0`
- Schema: `1.17`, writer generation `verified-ownership-v1.17`

Commands ran in `/home/pzaino/tmp/opencode/thecrowler-wp04c2j`. Logs are under
`/home/pzaino/tmp/opencode/`.

## Go verification

| Command | Result | Log |
| --- | --- | --- |
| `CGO_ENABLED=1 go test ./... -count=1` | PASS | `wp04c2j-full-final.log` |
| `CGO_ENABLED=1 go test ./pkg/database ./cmd/dbDeploy ./services/events ./services/api ./pkg/agent -count=1` | PASS | `wp04c2j-static.log` |
| `CGO_ENABLED=1 go test -race ./pkg/config ./pkg/agent ./pkg/database ./services/events ./services/api ./cmd/dbDeploy -count=1` | PASS, no data race | `wp04c2j-race.log` |
| `gofmt -l pkg services cmd` | PASS (no output) | `wp04c2j-static.log` |
| `CGO_ENABLED=1 go vet ./...` | PASS | `wp04c2j-static.log` |
| `CGO_ENABLED=1 go vet -tags integration ./pkg/database` | PASS | `wp04c2j-static.log` |
| `CGO_ENABLED=0 go build ./...` | PASS | `wp04c2j-static.log` |
| `CGO_ENABLED=1 go build ./...` | PASS | `wp04c2j-static.log` |
| PostgreSQL deployment integration | PASS | `wp04c2j-pg-deploy.log` |

A first full-suite run hit the known transient
`pkg/ws.TestWebSocketConnectionMessageDeliveryAndCleanup` failure. It passed in
isolation and the full suite passed on immediate rerun
(`wp04c2j-full-fk-retry.log`). The final full suite also passed.

## G3 test coverage

`pkg/database/mcp_mutation_deploy_test.go`:

- applies the additive MCP migration to a schema-1.17 SQLite database whose
  MCP objects/marker were removed;
- verifies the capability marker/checksum and startup compatibility after
  deployment;
- inserts audit history, disables the marker, proves the history row count is
  unchanged and the capability check now fails closed;
- creates a SQLite backup and reads the audit history from the backup;
- rejects an old schema (`1.16`);
- verifies both embedded migration scripts advertise schema 1.17 and the
  unchanged capability checksum.

`pkg/database/startup_compatibility_test.go`:

- validates a complete 1.17 fixture with foreign keys enabled;
- rejects `foreign_keys=0`;
- rejects a database with a pre-existing orphan row reported by
  `PRAGMA foreign_key_check`;
- keeps the existing v1.16/incomplete-v1.17/writer-generation negative tests.

`pkg/database/mcp_mutation_test.go`:

- `TestSQLiteMCPMutationForeignKeysAreRuntimeEnforced` proves two pooled
  connections report `foreign_keys=1`, valid parent/child inserts succeed and
  orphan inserts fail on both connections;
- the MCP approval table still declares its foreign keys.

`pkg/database/mcp_mutation_deploy_postgres_integration_test.go` (build tag
`integration`):

- creates a disposable PostgreSQL database;
- applies the migration, verifies capability and privileges;
- inserts an audit row;
- applies the migration a second time;
- disables the marker, verifies the history row count is unchanged and the
  capability fails closed;
- re-applies the capability and verifies it again.

`cmd/dbDeploy/main_test.go`:

- rejects `-apply` without `-backup-dir` and mutually exclusive actions;
- sanitizes backup filenames and chooses `.db`/`.dump` suffixes;
- runs the full SQLite `-check` (fails before deployment), `-apply`,
  `-check` (passes), history insert, `-disable`, `-check` (fails), and verifies
  the history row survives.

## Live Docker acceptance

A disposable PostgreSQL container on host port `55483` was loaded with the full
`postgresql-setup.pgsql` schema, then the MCP objects and capability marker were
removed to simulate an existing 1.17 database without the optional capability.

### PostgreSQL deployment

- `dbDeploy -check` failed with
  `MCP mutation capability is not deployed` and a detailed missing-object list.
- `dbDeploy -apply -backup-dir ... -db-user crowler` created a 518 KB custom
  `pg_dump` backup, applied the migration, verified the capability/checksum and
  verified `crowler` table/sequence privileges.
- `dbDeploy -check -db-user crowler` passed with schema `1.17`, writer
  generation `verified-ownership-v1.17` and capability
  `mcp-mutation-authorization-v1`.
- An audit row inserted as `crowler` survived `-disable`; the marker count went
  to zero and `-check` failed closed.
- Re-applying created a new backup and restored the capability; the audit row
  remained.
- `pg_restore --list` showed the six MCP tables, the audit sequence and the
  MCP data entries; `pg_restore --data-only --table=mcpmutationaudit` showed the
  deployed audit row.

### SQLite deployment

- A SQLite database created from `sqlite-setup.sqlite3` had its MCP objects and
  marker removed.
- `dbDeploy -check` failed with the expected missing capability.
- `dbDeploy -apply -backup-dir ...` created a `VACUUM INTO` backup and deployed
  the capability.
- `dbDeploy -check` passed, including `PRAGMA foreign_keys=1` and
  `PRAGMA foreign_key_check`.
- An audit row survived `-disable`; the marker count went to zero and `-check`
  failed closed.
- Re-applying restored the capability and preserved the audit row.
- The latest SQLite backup contained the audit row.

### Container checks

The built `crowler-events` image (CGO SQLite driver, `dbDeploy` and
`postgresql-client`) ran:

- `./dbDeploy -config /g3/sqlite-container-config.yaml -check` -> PASS;
- `./dbDeploy -config /g3/sqlite-container-config.yaml -apply -backup-dir
  /g3/container-backups` -> PASS, backup created;
- `./dbDeploy -config /g3/pg-container-config.yaml -check -db-user crowler` ->
  PASS;
- `./dbDeploy -config /g3/pg-container-config.yaml -apply -backup-dir
  /g3/container-pg-backups -db-user crowler` -> PASS, valid custom-format dump
  created.

Live evidence: `wp04c2j-live-evidence.log`, `wp04c2j-pg-deploy.log`,
`wp04c2j-g3/sqlite-check-before.out`,
`wp04c2j-g3/sqlite-check-disabled.out`,
`wp04c2j-g3/container-check-before.out`.

## Safety evidence

- No shared or production database was touched.
- The disposable PostgreSQL container and databases were created only for this
  test; no migration was applied to the host PostgreSQL instance.
- The SQLite databases and backups are temporary files under
  `/tmp/opencode/`.
- `RequiredSchemaVersion` stayed `1.17`; `WriterGeneration` stayed
  `verified-ownership-v1.17`; `RELEASE_VERSION.txt` stayed `v2.3.0`.
- No dispatcher, external mutation, retry, reconciliation or execution endpoint
  was added.

## Skips and limitations

- Full `pg_restore` into a new database was not run; backup validity was checked
  with `pg_restore --list` and a data-only extraction of the audit table.
- The live PostgreSQL server was a disposable container, not the host's
  service.
- `PRAGMA foreign_key_check` is a full scan; large SQLite databases will take
  longer at startup.
- Existing SQLite orphan rows are reported, not automatically repaired.
- `-apply` requires an administrative database connection for DDL and
  ownership/grants; `-db-user` selects the runtime application user for grants
  and privilege checks. `-disable` can run as the capability-table owner.
- The existing `pkg/ws` flake was observed once and passed on isolation and
  rerun; it is outside G3.
- CGO-disabled builds remain compilable but do not include SQLite support.

## Patch validation

The patch is generated with:

```sh
git diff --binary --full-index --no-ext-diff 479803aa
```

It is applied to a clean `git archive 479803aa` tree. The final patch hash,
application check and applied-tree results are reported in the handoff. The
tracked report does not embed the hash because adding it would change the patch
it describes.
