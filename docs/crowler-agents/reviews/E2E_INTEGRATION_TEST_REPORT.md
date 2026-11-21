# Integration and Non-Regression Test Report

## Environment

- Repository: `pzaino/thecrowler`
- Branch: `develop`
- Starting HEAD: `c5a763a64491f72f508d2768034d532c4fe3d748`
- Worktree: isolated temporary worktree outside the source checkout
- Go: `go1.27.3 linux/amd64`
- Docker: `29.7.2-ce`; Compose `5.6.1`
- Disposable PostgreSQL: 17 and 15.10-bookworm
- Release: `v2.3.0`
- Schema: `1.17`
- Writer generation: `verified-ownership-v1.17`
- Execution backend: `none`

## Go and repository

| Command | Result | Log |
| --- | --- | --- |
| `go test ./... -count=1` | PASS | `/home/pzaino/tmp/opencode/crowler-e2e-final-go-suite.log` |
| `go test -race ./pkg/agent ./pkg/database ./services/api ./services/events -count=1` | PASS, no race | `/home/pzaino/tmp/opencode/crowler-e2e-final-go-suite.log` |
| `go vet ./...` | PASS | `/home/pzaino/tmp/opencode/crowler-e2e-final-go-static.log` |
| `go vet -tags=integration ./pkg/database` | PASS | `/home/pzaino/tmp/opencode/crowler-e2e-final-go-static.log` |
| `CGO_ENABLED=0 go build ./...` | PASS | `/home/pzaino/tmp/opencode/crowler-e2e-final-go-static.log` |
| `CGO_ENABLED=1 go build ./...` | PASS | `/home/pzaino/tmp/opencode/crowler-e2e-final-go-static.log` |
| `gofmt` on changed Go files | PASS (no Go files changed) | local command |
| `git diff --check` | PASS | local command |

## SQLite and VDI

| Check | Result | Log |
| --- | --- | --- |
| SQLite FK, migration, startup, deployment and restart tests | PASS | `/home/pzaino/tmp/opencode/crowler-e2e-final-sqlite-vdi.log` |
| VDI deterministic negative controls `-count=20` | PASS | `/home/pzaino/tmp/opencode/crowler-e2e-final-sqlite-vdi.log` |

The SQLite command covered:

```text
TestSQLiteForeignKeyEnforcementRejectsOrphansAndReportsViolations
TestSQLiteRuntimeForeignKeysEnforcedOnPooledConnections
TestMCPMutationSQLiteMigrationIsRepeatable
TestSQLiteMCPMutationForeignKeysAreRuntimeEnforced
TestSQLiteV117MigrationConvergesAndPreservesHistory
TestStartupCompatibilityRejectsV116
TestStartupCompatibilityRejectsFalselyLabeledIncompleteV117
TestStartupCompatibilityAcceptsValidV117
TestStartupCompatibilityRejectsIncompatibleActiveWriterGeneration
TestStartupCompatibilityRejectsDisabledSQLiteForeignKeys
TestStartupCompatibilityRejectsSQLiteForeignKeyViolations
TestMCPMutationDeploymentSQLiteAppliesAndDisablesWithoutLosingHistory
TestMCPMutationSQLiteRestartPreservesProposalAndQuota
```

## PostgreSQL tagged integration

Disposable PostgreSQL 17 and 15.10 containers were used. No shared or
production database was touched.

| Version | Result | Log |
| --- | --- | --- |
| PostgreSQL 17 | 6/6 PASS, no SKIP | `/home/pzaino/tmp/opencode/crowler-e2e-final-postgres.log` |
| PostgreSQL 15.10 | 6/6 PASS, no SKIP | `/home/pzaino/tmp/opencode/crowler-e2e-final-postgres.log` |

Tests:

```text
TestPostgresMCPMutationDurableStore
TestPostgresMCPMutationDeploymentApplyDisable
TestPostgresMCPMutationAuditAndRetention
TestPostgresMCPMutationApprovalAuthorityLockOrder
TestPostgresMCPMutationBackupRestoreRehearsal
TestPostgresMCPMutationAdminRoleCascadeContention
```

The backup/restore rehearsal and admin-role cascade contention test passed on
both versions.

## Workflow and cleanup regressions

| Check | Result | Log |
| --- | --- | --- |
| Python workflow/provenance tests | 13/13 PASS | `/home/pzaino/tmp/opencode/crowler-e2e-final-python.log` |
| Fake-Docker exact-ownership suite | 15/15 PASS | `/home/pzaino/tmp/opencode/crowler-e2e-final-cleanup.log` |
| Pinned actionlint on three workflows | PASS, exit 0 | `/home/pzaino/tmp/opencode/crowler-e2e-final-actionlint.log` |
| Mutation checks | 8/8 mutations rejected | `/home/pzaino/tmp/opencode/crowler-e2e-final-mutations.log` |
| Real-daemon cleanup rehearsal | 5/5 scenarios PASS | `/home/pzaino/tmp/opencode/crowler-e2e-final-cleanup-rehearsal.log` |

## Local isolated E2E

- Log: `/home/pzaino/tmp/opencode/crowler-e2e-final-isolated-e2e.log`
- Result:
  `/tmp/opencode/crowler-e2e-final/db97508a78d382f448643b5defe1ad41/results.json`
- Safety log: `/home/pzaino/tmp/opencode/crowler-e2e-final-safety.log`
- Source SHA: `c5a763a64491f72f508d2768034d532c4fe3d748`
- Source tree digest at E2E time:
  `cdffa3998b0b45b2f77635ff0cd7d2d539bd77463b44535458dce0ba4fc0322d`
- Result: `PASS`
- Counts: `PASS=60`, `FAIL=0`, `BLOCKED=0`, `SKIP=0`
- Final completion marker: `fixture completed` PASS
- Inner cleanup: `PASS`
- Outer dind cleanup: `PASS`, exact container removed, `OUTER_ABSENT`
- Image roles: API, Events and model all matched the source-tree label
- Dispatch attempts: zero
- Dispatch states: none
- Secret scan: `NO_SECRET_PATTERNS`
- Leftover containers, volumes, networks or images: none

The harness safety suite passed 9/9 on the same disposable daemon.

## GitHub checks

At `c5a763a`, eight ordinary checks passed. PostgreSQL integration:
https://github.com/pzaino/thecrowler/actions/runs/43712945449/job/132808281661

These checks predate and do not exercise this uncommitted change. The new fast
workflow steps and the manual E2E workflow have not run on GitHub. No hosted E2E
URL, run ID, attempt or artifact exists.

## Not run

- GitHub `workflow_dispatch` E2E, because the workflow is not registered on the
  default branch.
- Information Seed Compose acceptance suite.
- Live Ollama.
- Real external MCP endpoint.
- Production or shared database.
- Any simulator execution. Option A is not approved.
- Any external execution. It is not approved.

## Patch applicability

The patch is generated against
`c5a763a64491f72f508d2768034d532c4fe3d748`. Its SHA-256 and the fresh-clone
`git apply --check` result are recorded in the delivery message, because a
report contained in the patch cannot contain its own final patch hash without
becoming self-referential. The applied-tree commands and outcomes are:

```text
git apply --check <patch>                 PASS
git apply <patch>                         PASS
python3 -m unittest discover ...          13/13 PASS
bash scripts/tests/cleanup-e2e-dind.sh    15/15 PASS
actionlint (three workflows)              PASS, exit 0
go test ./... -count=1                    PASS
```

These are the final applied-tree results; the exact log paths are the
descriptive paths listed above.
