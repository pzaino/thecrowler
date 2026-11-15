# WP-04C2N Test Report

## Environment

- Repository: `pzaino/thecrowler`
- Branch: `develop`
- Starting HEAD: `d5314ce2272c7136352e772b2e3b394b17ee319e`
- Worktree: `/home/pzaino/tmp/opencode/thecrowler-wp04c2n`
- Go: `go1.27.3 linux/amd64`
- Node: `v24.22.1`
- Docker: `29.7.2-ce`; Compose `v5.6.1`
- Disposable PostgreSQL: 17 and 15.10-bookworm
- Release: `v2.3.0`
- Schema: `1.17`
- Writer generation: `verified-ownership-v1.17`
- Execution backend: `none`

## Go verification

| Command | Result | Log |
| --- | --- | --- |
| `go test ./... -count=1` | PASS | `wp04c2n-final-suite.log` |
| `go test -race ./pkg/agent ./pkg/database ./services/api ./services/events -count=1` | PASS, no race | `wp04c2n-final-suite.log` |
| `go vet ./...` | PASS | `wp04c2n-final-static.log` |
| `go vet -tags=integration ./pkg/database` | PASS | `wp04c2n-final-static.log` |
| `gofmt` changed Go files | PASS | `wp04c2n-final-static.log` |
| `git diff --check` | PASS | `wp04c2n-final-static.log` |
| `CGO_ENABLED=0 go build ./...` | PASS | `wp04c2n-final-static.log` |
| `CGO_ENABLED=1 go build ./...` | PASS | `wp04c2n-final-static.log` |

Baseline logs before workflow edits:

- `wp04c2n-baseline.log`
- `wp04c2n-baseline-static.log`

## SQLite and VDI

- SQLite FK/migration/startup/deployment focused tests: PASS
- VDI deterministic controls `-count=20`: PASS
- log: `/home/pzaino/tmp/opencode/wp04c2n-sqlite-vdi.log`

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
| PostgreSQL 17 | 6/6 PASS | `wp04c2n-pg.log` |
| PostgreSQL 15.10 | 6/6 PASS | `wp04c2n-pg.log` |

No SKIP. Cascade contention observed `delete=<nil>, lock=source mutation is forbidden` on both.

## Workflow and cleanup regressions

- Actionlint on both workflows: PASS
  (`wp04c2n-actionlint.log`).
- `test_workflow.py`: 5/5 PASS
  - manual-only trigger;
  - least privilege;
  - digest pull/inspect/run identity;
  - no empty pull;
  - host endpoint guard;
  - host-scoped cleanup;
  - upload after cleanup.
- `scripts/tests/cleanup-e2e-dind.sh`: 7/7 PASS
  - owned removal;
  - repeated cleanup;
  - foreign label/name preservation;
  - inspect failure;
  - remove failure;
  - nested `DOCKER_HOST` isolation.
- `test_provenance.py`: 4/4 PASS.

## Local isolated E2E

Disposable dind, final harness:

- log: `/home/pzaino/tmp/opencode/wp04c2n-e2e.log`
- result:
  `/tmp/opencode/wp04c2n-e2e/48edce50ec854b9966b32d00bc3d4677/results.json`
- 60 PASS, 0 FAIL/BLOCKED/SKIP
- cleanup PASS
- source SHA `d5314ce2272c7136352e772b2e3b394b17ee319e`
- source tree `387bd2b31cb2f6c200d16fce39084576dbfc886dafb9b14391ab4208ef8b492c`
- image IDs matched the tree label
- zero mutation fixture execution
- zero dispatch attempts
- no dispatch/uncertain/reconciliation state
- artifact secret scan clean

Harness safety suite: 9/9 PASS
(`wp04c2n-safety.log`).

## GitHub checks

At `d5314ce`, eight GitHub checks passed. PostgreSQL integration job:
https://github.com/pzaino/thecrowler/actions/runs/43654921200/job/132631232007

The opt-in E2E workflow has **not run** on GitHub. It is not registered because
the workflow file is on `develop` while the repository default branch `main`
does not contain it. Workflow-runs API returns 404 for this workflow.

This is recorded as PENDING, not as a pass. No GitHub E2E URL or artifact exists.

## Not run

- GitHub workflow_dispatch E2E (registration/default-branch blocker).
- Information Seed Compose acceptance suite.
- Live Ollama.
- Option A simulator (no approval).
- Option B execution (not approved).
- Production/shared database.

## Patch applicability

Generated against `d5314ce`. Final patch hash and applied-tree results are
reported in the delivery message.

## Patch applicability

The patch was generated against `d5314ce2272c7136352e772b2e3b394b17ee319e`. A
fresh clone passed `git apply --check`, applied cleanly, and contained all nine
tracked WP-04C2N reports (including the Option A approval request).

Applied-tree verification:

- `go test ./... -count=1` PASS (`wp04c2n-patchcheck.log`)
- `python3 -m unittest discover ...` 9/9 PASS (`wp04c2n-patchcheck-python.log`)
- `scripts/tests/cleanup-e2e-dind.sh` 7/7 PASS (`wp04c2n-patchcheck-cleanup.log`)
- actionlint on both workflows: PASS (`wp04c2n-patchcheck-actionlint.log`)

The final patch SHA-256 and file list are in the delivery message.
