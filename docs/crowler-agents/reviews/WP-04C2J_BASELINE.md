# WP-04C2J Baseline

## Repository state

- Repository: `pzaino/thecrowler`
- Branch: `develop`
- Starting HEAD: `479803aa9c74e6b5f0c09ced881a7191c6574afd`
- Working tree at start: clean.
- Go: `go1.27.3 linux/amd64`.
- Docker: `29.7.2-ce`; Compose `v5.6.1`.
- Release: `v2.3.0`.
- Schema: `1.17`, writer generation `verified-ownership-v1.17`.

B5 is present at this HEAD: durable denied-admission audit, bounded reason
codes, retention and the B5 review reports.

## G3 findings before the change

1. **SQLite runtime foreign keys were off.** `SQLiteHandler.Connect` used
   `file:%s?cache=shared&mode=rwc`; `database/sql` pools multiple connections,
   and only the setup/migration connection executed `PRAGMA foreign_keys=ON`.
   The schema declared foreign keys, but runtime connections did not enforce
   them.
2. **No additive capability deployment command existed.** The 1.17 MCP
   capability could be applied manually with `psql`/`sqlite3` or via a clean
   install. There was no command that required a backup, verified the
   capability marker/checksum and privileges, upgraded an existing 1.17
   database, or disabled the capability without deleting history.
3. **Production binaries did not include the SQLite driver.** The CGO-backed
   `github.com/mattn/go-sqlite3` driver was imported only by tests, and
   `autobuild.sh` used `CGO_ENABLED=0`. A production SQLite connection or
   `dbDeploy -apply` would fail with `unknown driver "sqlite3"`.
4. **The runtime images did not contain `pg_dump`.** A PostgreSQL deployment
   backup could not be created from inside the supplied images.

## Existing schema-1.17 contract

The additive MCP capability already exists in schema 1.17:

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
`sqlite-setup.sqlite3`. The capability marker is
`mcp-mutation-authorization-v1` with checksum
`00cb16b3efc2a3a619335c4e3bc2b1c470bb34b2378d249a81e746cddc704eed`.

No schema SQL change is required for G3: the declarations already exist and the
full SQLite setup has no `PRAGMA foreign_key_check` violations. The deployment
command applies the existing additive migration; SQLite enforcement is a
runtime DSN change.

## Source boundaries inspected

- `pkg/database/sqlite_db.go`, `startup_compatibility.go`,
  `mcp_mutation.go`, the schema-1.17 setup/migration files and the database
  tests.
- `pkg/database/db_migrations/*mcp-mutation-v1.17*`.
- `pkg/database/mcp_mutation_postgres_integration_test.go`.
- `cmd/`, `autobuild.sh`, `Dockerfile.events`, `Dockerfile.searchapi`,
  `Dockerfile.thecrowler`.
- `docs/crowler-agents/reviews/WP-04C2C_*`, `WP-04C2D_*`, `WP-04C2E_*`,
  `WP-04C2I_*` and `docs/crowler-agents/mcp-mutation-authorization.md`.
- `TESTING.md` and the disposable Compose workflow.

## Deployment boundaries

No shared or production database was touched. The live Postgres tests used a
disposable container; the live SQLite tests used temporary files. No schema
version, writer generation, release version or `.github` workflow was changed.
