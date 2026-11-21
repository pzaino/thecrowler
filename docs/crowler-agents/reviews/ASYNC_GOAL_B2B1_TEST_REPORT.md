# WP-05B2B1 Test Report

## Environment

| Item | Value |
| --- | --- |
| Worktree | `/home/pzaino/tmp/opencode/thecrowler-wp05b2b1` |
| Base | `c505cf28d380535ec2057497bc8f3f0b42765158` |
| Go | `go1.27.3 linux/amd64` |
| PostgreSQL | 17.12, disposable Docker container `crowler-b2b1-pg`, port `55495` |
| SQLite | system `sqlite3` and the repository's cgo SQLite driver |

All database tests used disposable databases. No persistent, shared, staging or
production database was touched.

## Repository-wide gates

| Command | Result |
| --- | --- |
| `go test ./... -count=1` | PASS |
| `go vet ./...` | PASS |
| `CGO_ENABLED=0 go build ./...` | PASS |
| `CGO_ENABLED=1 go build ./...` | PASS |
| `go test -race ./pkg/database -run 'TestAsyncGoalPersistence' -count=1` | PASS |
| `git diff --check` | PASS |

The full `go test ./...` run includes the existing `pkg/agent`, `pkg/config`,
`pkg/database`, `services/api` and `services/events` tests. It does not include
files guarded by the `integration` build tag.

## SQLite tests

Command:

```bash
go test ./pkg/database -run 'TestAsyncGoalPersistence' -count=1 -v
```

Result: PASS. Tests:

- `TestAsyncGoalPersistenceSQLiteReadiness`
- `TestAsyncGoalPersistenceSQLiteAddonPreservesData`
- `TestAsyncGoalPersistenceSQLiteMigrationConverges`
- `TestAsyncGoalPersistenceSQLiteAddonRejectsIncompatible`
- `TestAsyncGoalPersistenceSQLCoverage`
- `TestAsyncGoalPersistenceCanonicalDDLConverges`
- `TestAsyncGoalPersistenceKeepsSchema117`
- `TestAsyncGoalPersistenceMySQLIsUnsupported`
- `TestAsyncGoalPersistenceReadinessDoesNotAffectStartup`
- `TestAsyncGoalPersistenceSQLiteRejectsInvalidTimestamps`
- `TestAsyncGoalPersistenceSQLiteAddonRejectsWrongForeignKey`
- `TestAsyncGoalPersistenceSQLiteRejectsOversizedValues`

The tests cover:

- fresh setup readiness;
- missing unique index and forbidden payload column refusal;
- missing named CHECK constraint refusal;
- wrong foreign-key parent refusal;
- addon re-run with preserved goal rows and markers;
- v1.16 -> v1.17 migration convergence;
- byte-identical PostgreSQL and SQLite DDL blocks across fresh, migration and
  addon files;
- no schema value beyond 1.17 and no MySQL goal tables;
- MySQL typed unsupported result;
- ordinary 1.17 startup succeeding while goal readiness fails closed;
- invalid timestamp text rejection for root, work, evidence, audit and
  transition rows;
- oversized owner, tool-name and audit-ref rejection.

## PostgreSQL integration tests

Command shape (disposable database `crowler_b2b1_test`, runtime role
`crowler`, administrative role `postgres`):

```bash
THECROWLER_POSTGRES_INTEGRATION=1 \
POSTGRES_USER=postgres POSTGRES_PASSWORD=... \
DOCKER_POSTGRES_DB_HOST=127.0.0.1 DOCKER_POSTGRES_DB_PORT=55495 \
DOCKER_POSTGRES_DB_NAME=crowler_b2b1_test \
DOCKER_POSTGRES_DB_USER=crowler DOCKER_POSTGRES_PASSWORD=... \
DOCKER_POSTGRES_SSL_MODE=disable \
go test -tags=integration ./pkg/database \
  -run 'TestPostgresAsyncGoalPersistence' -count=1 -v
```

Result: PASS.

- `TestPostgresAsyncGoalPersistenceSchema`
- `TestPostgresAsyncGoalPersistenceAddonRollsBack`
- `TestPostgresAsyncGoalPersistenceAddonRejectsIncompatible`
- `TestPostgresAsyncGoalPersistenceAddonRejectsWrongForeignKey`

Coverage includes:

- fresh 1.17 setup readiness as the runtime role;
- full addon application by the administrator and privilege transfer to the
  runtime role;
- addon recreation after dropping the goal tables;
- addon re-run after inserting goal root, tool, source, audit and transition
  rows, with row counts preserved and application-table counts unchanged;
- completion after approval expiry;
- duplicate operation identity rejection;
- completion observed before acceptance rejection;
- acceptance before the work-reference row insertion;
- unique-index removal and addon repair;
- transactional DDL rollback leaving no goal tables;
- temporary-schema fail-closed rehearsal after dropping a named CHECK
  constraint;
- temporary-schema fail-closed rehearsal after replacing a named foreign key
  with one pointing at the wrong parent.

Command:

```bash
go test -tags=integration ./pkg/database \
  -run '^TestPostgresV117RuntimeCompatibility$' -count=1 -v
```

Result: PASS against the same disposable database. This confirms the ordinary
1.17 startup contract is unchanged.

## Full-script rehearsals

### `psql`

The final PostgreSQL addon was applied twice with:

```bash
psql -v ON_ERROR_STOP=1 -v CROWLER_DB_USER=crowler \
  -f pkg/database/db_migrations/postgresql-agent-goal-v1.17.pgsql
```

A goal root, tool, source, audit ref and transition were inserted between the
two runs. Results:

- first addon application: success;
- application as the runtime role: correctly unable to create the tables (the
  script is an administrator operation);
- second addon application: success;
- goal root count: 1;
- transition count: 1;
- `DBSchemaVersion`: `1.17`;
- writer generation: `verified-ownership-v1.17`;
- goal table count: 8.

### `sqlite3`

The final SQLite addon was applied twice with:

```bash
sqlite3 -bail <db> < pkg/database/db_migrations/sqlite-agent-goal-v1.17.sqlite3
```

A goal root, tool, source, audit ref and transition were inserted between the
two runs. Results:

- first addon application: success;
- second addon application: success;
- goal root count: 1;
- transition count: 1;
- `DBSchemaVersion`: `1.17`;
- writer generation: `verified-ownership-v1.17`;
- goal table count: 8.

A table with the same name but no `chk_agent_goal_tool_name` constraint was then
substituted; the addon exited non-zero with:

```text
Error near line 501: async goal schema is incompatible
```

## Patch applicability

The generated patch was checked against a fresh shared clone at
`c505cf28d380535ec2057497bc8f3f0b42765158`:

```bash
git apply --check /home/pzaino/tmp/opencode/thecrowler-async-b2b1.patch
```

Result: PASS. The patch was then applied to the clone, every changed file's
SHA-256 matched the isolated worktree, and
`go test ./pkg/database -run 'TestAsyncGoalPersistence' -count=1` passed in the
clone.

## Not run

- GitHub Actions CI for this patch. The patch has not been pushed.
- PostgreSQL 15.10. Local rehearsal used PostgreSQL 17.12 only.
- A live MySQL server test. MySQL non-support is covered by the backend policy,
  the static schema check and the typed unsupported result.
- The complete `-tags=integration` repository suite. Only the new
  asynchronous-goal PostgreSQL tests and the existing
  `TestPostgresV117RuntimeCompatibility` were run against the disposable
  database.
- Any production, staging or shared-database migration.
- Any asynchronous goal execution, worker, claim, lease or event consumer.

## Evidence limitations

A green suite is evidence for the tested properties, not proof of general
correctness. The PostgreSQL rehearsal used a disposable local container, not
GitHub CI. The structural check verifies names and shapes, not full CHECK
expression equivalence or cross-table kernel invariants.
