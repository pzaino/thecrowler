# WP-04C2M Test Report

## Environment

- Repository: `pzaino/thecrowler`
- Branch: `develop`
- Starting HEAD: `3247de3d0a4a90b16d3fa6980bee92ea4695a709`
- Worktree: `/home/pzaino/tmp/opencode/thecrowler-wp04c2m`
- Go: `go1.27.3 linux/amd64`
- Node: `v24.22.1`
- Docker: `29.7.2-ce`; Compose `v5.6.1`
- Host `pg_dump`/`pg_restore`: `18.6`
- Disposable PostgreSQL: 17 and 15.10-bookworm
- Release: `v2.3.0`
- Schema: `1.17`
- Writer generation: `verified-ownership-v1.17`
- Execution backend: `none`

## Go verification

| Command | Result | Log |
| --- | --- | --- |
| `go test ./... -count=1` | PASS | `wp04c2m-final-suite2.log` |
| `go test -race ./pkg/agent ./pkg/database ./services/api ./services/events -count=1` | PASS, no data race | `wp04c2m-final-suite2.log` |
| `go vet ./...` | PASS | `wp04c2m-final-static2.log` |
| `go vet -tags=integration ./pkg/database` | PASS | `wp04c2m-final-static2.log` |
| `gofmt` changed Go files | PASS | `wp04c2m-final-static2.log` |
| `git diff --check` | PASS | `wp04c2m-final-static2.log` |
| `CGO_ENABLED=0 go build ./...` | PASS | `wp04c2m-final-static2.log` |
| `CGO_ENABLED=1 go build ./...` | PASS | `wp04c2m-final-static2.log` |

## PostgreSQL tagged integration

Six tests on both versions:

- `TestPostgresMCPMutationDurableStore`
- `TestPostgresMCPMutationDeploymentApplyDisable`
- `TestPostgresMCPMutationAuditAndRetention`
- `TestPostgresMCPMutationApprovalAuthorityLockOrder`
- `TestPostgresMCPMutationBackupRestoreRehearsal`
- `TestPostgresMCPMutationAdminRoleCascadeContention`

| Version | Result | Log |
| --- | --- | --- |
| PostgreSQL 17 | 6/6 PASS | `wp04c2m-pg.log` |
| PostgreSQL 15.10 | 6/6 PASS | `wp04c2m-pg.log` |

No SKIP. The cascade contention test observed:
`delete=<nil>, lock=source mutation is forbidden` on both versions.

## Deny/revoke authority tests

`pkg/agent/mcp_mutation_administration_authorization_test.go`:

- current owner may deny;
- unrelated user cannot deny;
- admin may deny after source transfer;
- disabled actor and revoked admin role cannot deny;
- admin may revoke after proposal principal is disabled;
- concurrent deny produces one denied state;
- cancellation and database outage fail closed;
- zero dispatch attempts.

The PostgreSQL lock-order test additionally covers admin deny after transfer,
non-admin deny rejection, admin revoke after principal disable, and role
revocation contending with deny.

## PostgreSQL client selection

`scripts/tests/select-postgres-client-major.sh`:

- selects versioned 17 client when 18.6 is also present;
- rejects mismatched client major;
- rejects missing versioned package.

PASS locally. This tests the workflow PATH/version repair, but not GitHub.

## SQLite and startup

Focused SQLite FK/migration/startup/deployment tests: PASS.
Log: `wp04c2m-sqlite-vdi.log`.

## VDI

`go test ./pkg/vdi -run 'TestCamouflage...|TestCamouflageLegacy...' -count=20`:
PASS on Node `v24.22.1`. Log: `wp04c2m-sqlite-vdi.log`.

## Provenance tests

`python3 -m unittest discover -s tests/integration/mcp-authorization -p 'test_*.py' -v`:
4/4 PASS.

- identical source digest;
- bytecode/pycache does not change digest;
- real source change changes digest;
- symlink target is recorded without being followed.

## Harness safety

Host: 9/9 PASS (`wp04c2m-safety-host.log`).

Disposable dind: 9/9 PASS (`wp04c2m-safety-dind.log`).

## Full isolated E2E

Disposable dind, source SHA `3247de3d`, source tree
`ded39c819c1c3494cf9bc85b5b297d57d6ba9a71ecfff2385991854154d2ecac`:

- 60 PASS
- 0 FAIL
- 0 BLOCKED
- 0 SKIP
- cleanup PASS
- zero mutation fixture execution
- zero dispatch attempts
- no dispatch/uncertain/reconciliation state

Log: `/home/pzaino/tmp/opencode/wp04c2m-e2e-dind.log`

Result:
`/tmp/opencode/wp04c2m-e2e/b4fecdf563b8a982a7685d0168d563d6/results.json`

Artifact scan found no passwords, DSNs, bearer tokens, JWTs, raw `source_id`
values or `fixture-resource`.

## GitHub CI

Not run on the patched tree. The starting HEAD has 7/8 checks: PostgreSQL
integration failed at client installation and skipped all database steps. The
repair is not green until GitHub runs the applied commit. See
`WP-04C2M_POSTGRES_CI_REPAIR.md`.

## GitHub E2E

Not run. `workflow_dispatch` requires authenticated repository permission; no
token/`gh` is available and no push was authorized. See
`WP-04C2M_E2E_GITHUB_EVIDENCE.md`.

## Simulator

Not implemented. Option A approval is absent. See
`WP-04C2M_SIMULATOR_GATE.md`.

## Not run

- GitHub Actions after the patch.
- Opt-in GitHub E2E.
- Information Seed Compose acceptance suite.
- Live Ollama.
- Production/shared database.
- Option B execution.

## Patch validation

Final patch apply-check and applied-tree results are recorded in the delivery
message once the patch is generated.

## Patch applicability

The final patch was generated against
`3247de3d0a4a90b16d3fa6980bee92ea4695a709`. A fresh clone passed
`git apply --check`, applied cleanly, and contained all twelve new WP-04C2M
reports plus the existing WP-04C2M dispatch request.

Applied-tree verification:

- `go test ./... -count=1` PASS (`wp04c2m-patchcheck.log`)
- `go test -race ./pkg/agent ./pkg/database ./services/api ./services/events -count=1`
  PASS (`wp04c2m-patchcheck.log`)
- `go vet ./...`, `go vet -tags=integration ./pkg/database`, `gofmt`,
  `git diff --check`, CGO 0/1 builds PASS (`wp04c2m-patchcheck-static.log`)

The final patch SHA-256 and file list are reported in the delivery message.
