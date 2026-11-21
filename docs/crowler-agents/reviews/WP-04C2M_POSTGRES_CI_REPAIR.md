# WP-04C2M PostgreSQL CI Repair

## Failure

GitHub run: https://github.com/pzaino/thecrowler/actions/runs/43643362767

Failing job: https://github.com/pzaino/thecrowler/actions/runs/43643362767/job/132595567993

The job failed in `Install PostgreSQL 17 client` and skipped every schema and
database step. The public job API reports the install step as `failure`; later
steps are `skipped`. Unauthenticated job-log download is blocked by GitHub
(`Must have admin rights to Repository`), so the exact log line could not be
fetched.

The task evidence states the installed 17 client resolved to 18.6. Locally,
`/usr/bin/pg_dump` and `/usr/bin/pg_restore` are also 18.6, reproducing the PATH
precedence condition: installing `postgresql-client-17` does not guarantee that
`/usr/bin/pg_dump` stops resolving to an older/newer preinstalled client.

## Repair

`.github/workflows/postgresql-integration.yml` now:

1. installs `postgresql-client-17`;
2. runs `scripts/select-postgres-client-major.sh 17`;
3. resolves `pg_dump` from `dpkg -L postgresql-client-17`, not a guessed path;
4. verifies both `pg_dump` and `pg_restore` are executable and major 17;
5. writes the versioned bin directory to `$GITHUB_PATH`, so later steps and Go
   subprocesses inherit it;
6. exports it for the current step and runs `hash -r`;
7. logs `command -v pg_dump`, `command -v pg_restore`, both versions and the
   server version;
8. asserts both client majors and the server major are 17.

The selector script is tracked at `scripts/select-postgres-client-major.sh`.
Its fake-package regression is
`scripts/tests/select-postgres-client-major.sh` and proves:

- a 17 package is selected from a versioned path even when 18.6 is available;
- a mismatched client major is rejected;
- a missing versioned package is rejected.

## Dedicated MCP tests

The MCP step now requires visible RUN/PASS and no SKIP for:

- `TestPostgresMCPMutationDurableStore`
- `TestPostgresMCPMutationDeploymentApplyDisable`
- `TestPostgresMCPMutationAuditAndRetention`
- `TestPostgresMCPMutationApprovalAuthorityLockOrder`
- `TestPostgresMCPMutationBackupRestoreRehearsal`
- `TestPostgresMCPMutationAdminRoleCascadeContention`

The restore rehearsal uses the selected 17 client. The workflow still uses only
its disposable PostgreSQL 17 service and uniquely created databases.

## Local evidence

Selector regression:

```
PASS: selects versioned PostgreSQL 17 client
PASS: rejects mismatched client major
PASS: rejects missing versioned package
```

Disposable PostgreSQL 17 and 15.10:

- all six MCP tests PASS on both
- no SKIP
- log: `/home/pzaino/tmp/opencode/wp04c2m-pg.log`

## GitHub green gate

**Not achieved.** The patch has not been pushed and the workflow has not been
rerun on an applied commit. No commit/push was authorized. The blocking job
cannot be declared green until GitHub runs the new workflow on the applied
commit and the `Install and select PostgreSQL 17 client` step and all six MCP
tests pass.

Exact manual action required after review/push:

1. push the reviewed patch to a branch or `develop` as authorized;
2. open the PostgreSQL integration run for the applied SHA;
3. verify the client-selection step logs major-17 paths and versions;
4. verify all six RUN/PASS lines and no SKIP;
5. record the run URL in place of this blocker.

Local PostgreSQL results are additional evidence, not a substitute for the
GitHub gate.
