# WP-04C2L CI Integration

## Dedicated PostgreSQL MCP step

`.github/workflows/postgresql-integration.yml:92` now installs
`postgresql-client-17` from the PGDG repository and verifies
`pg_dump`/`pg_restore` major version 17.

The MCP step (`.github/workflows/postgresql-integration.yml:285`) runs:

- `TestPostgresMCPMutationDurableStore`
- `TestPostgresMCPMutationDeploymentApplyDisable`
- `TestPostgresMCPMutationAuditAndRetention`
- `TestPostgresMCPMutationApprovalAuthorityLockOrder`
- `TestPostgresMCPMutationBackupRestoreRehearsal`

It requires a visible `=== RUN` and `--- PASS` line for each test and fails on
any `--- SKIP`. It uses only the workflow's disposable PostgreSQL 17 service and
uniquely created child databases. No credentials are copied to artifacts.

The restore rehearsal creates a dump with `pg_dump --format=custom`, restores it
into a newly created child database with `pg_restore`, and verifies restored
audit data. The PostgreSQL 15 client/server `transaction_timeout` allowance is
narrowly limited to that one known statement and still verifies rows.

## Opt-in isolated E2E workflow

New file: `.github/workflows/mcp-authorization-e2e.yml`

- Trigger: `workflow_dispatch` only.
- Permissions: `contents: read`.
- Runner: fresh `ubuntu-latest`.
- Timeout: 45 minutes.
- Docker daemon: disposable privileged `docker:dind` reached only on
  `127.0.0.1:2375`.
- Source/workspace and runner temp are bind-mounted into the dind container so
  the harness work tree is visible to nested containers.
- Runs `safety_tests.py` before the full fixture.
- Runs `run.sh` with `CROWLER_E2E_WORK` and
  `CROWLER_E2E_ARTIFACT_ROOT` under `RUNNER_TEMP`.
- Verifies `results.json` conclusion is `PASS` and no FAIL/BLOCKED checks exist.
- Uploads sanitized artifacts even on failure.
- Removes the disposable dind container in `always()` cleanup.

The workflow is intentionally separate from quick build/PR workflows until run
time, cost and reliability are measured and approved.

## What the workflow does not do

- It does not run on ordinary pull requests.
- It does not use production credentials or a shared Docker daemon.
- It does not enable dispatch or a remote mutating MCP call.
- It does not infer that a green unit build means the E2E ran.

## Local evidence

PostgreSQL 17 and 15.10:

`/home/pzaino/tmp/opencode/wp04c2l-pg-final2.log`

Safety tests on host and dind:

- `/home/pzaino/tmp/opencode/wp04c2l-safety-host2.log`
- `/home/pzaino/tmp/opencode/wp04c2l-safety-dind3.log`

Full E2E on dind:

`/home/pzaino/tmp/opencode/wp04c2l-e2e-dind7.log`

## Not run

- GitHub Actions itself was not executed.
- The Information Seed Compose suite was not run.
- No live Ollama or production MCP endpoint was contacted.
