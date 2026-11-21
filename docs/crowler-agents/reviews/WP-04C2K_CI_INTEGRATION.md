# WP-04C2K CI Integration

## Changed workflow step

`.github/workflows/postgresql-integration.yml:284` is the dedicated disposable
MCP mutation PostgreSQL step. The test regex now includes the audit/retention
test:

```sh
go test -tags=integration ./pkg/database \
  -run '^TestPostgresMCPMutation(DurableStore|DeploymentApplyDisable|AuditAndRetention)$' \
  -count=1 -v
```

The step then requires visible `RUN` and `PASS` lines for all three tests:

- `TestPostgresMCPMutationDurableStore`
- `TestPostgresMCPMutationDeploymentApplyDisable`
- `TestPostgresMCPMutationAuditAndRetention`

It fails if the log contains `--- SKIP`, so a missing opt-in or unavailable
service cannot silently pass. The workflow uses only its disposable
`postgres:17` service, port `5432`, and uniquely created test databases.

## PostgreSQL 17 evidence

Disposable container: `postgres:17`.

```sh
CROWLER_MCP_POSTGRES_TEST_DISPOSABLE=1 \
CROWLER_MCP_POSTGRES_DSN='postgres://postgres:<redacted>@127.0.0.1:55484/SitesIndex?sslmode=disable' \
go test -tags integration ./pkg/database \
  -run '^TestPostgresMCPMutation(DurableStore|DeploymentApplyDisable|AuditAndRetention|BackupRestoreRehearsal)$' \
  -count=1 -v
```

Observed:

```
=== RUN   TestPostgresMCPMutationAuditAndRetention
--- PASS: TestPostgresMCPMutationAuditAndRetention
=== RUN   TestPostgresMCPMutationDeploymentApplyDisable
--- PASS: TestPostgresMCPMutationDeploymentApplyDisable
=== RUN   TestPostgresMCPMutationDurableStore
--- PASS: TestPostgresMCPMutationDurableStore
=== RUN   TestPostgresMCPMutationBackupRestoreRehearsal
--- PASS: TestPostgresMCPMutationBackupRestoreRehearsal
```

No test was skipped.

## PostgreSQL 15 compatibility evidence

The project's Compose stack still uses `postgres:15.10-bookworm`. A second
disposable `postgres:15.10-bookworm` instance was run on port `55486`:

```
=== RUN   TestPostgresMCPMutationAuditAndRetention
--- PASS
=== RUN   TestPostgresMCPMutationDeploymentApplyDisable
--- PASS
=== RUN   TestPostgresMCPMutationDurableStore
--- PASS
=== RUN   TestPostgresMCPMutationBackupRestoreRehearsal
--- PASS
```

The restore rehearsal initially hit a client/server version artifact: the host
`pg_restore` emits `SET transaction_timeout = 0`, which PostgreSQL 15 does not
recognize. `pg_restore` reported one ignored error and still restored the data.
The integration test now tolerates only that exact single-error signature and
then verifies the restored `MCPMutationAudit` row count. It does not tolerate
other restore errors.

The E2E authorization fixture also ran against the project's
`postgres:15.10-bookworm` service, providing live schema-1.17 evidence on 15.

`TestPostgresMCPMutationBackupRestoreRehearsal` is local evidence only. It is
not in the dedicated CI regex and the broad integration job does not opt into
the disposable MCP environment, so it skips there. Adding it to CI would need a
separate pg_dump/pg_restore tooling decision.

## What CI does not run

The tracked E2E authorization fixture
(`tests/integration/mcp-authorization/run.sh`) is not wired into a CI workflow.
It requires Docker Compose, the API/Events images, a local fake model and a
mutable test schema. It was executed locally with both PostgreSQL 17 integration
tests and the Compose PostgreSQL 15 stack.

The Information Seed Compose acceptance suite was **not run** in this work
package. No result in this report should be read as evidence that it ran.

## Artifact handling

The CI step logs only test output. The DSN is assembled from the workflow
service credentials and is not copied into artifacts. Local commands in this
report redact the password. No database credential is added to a tracked file.
