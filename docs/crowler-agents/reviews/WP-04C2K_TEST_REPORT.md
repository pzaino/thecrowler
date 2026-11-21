# WP-04C2K Test Report

## Environment

- Repository: `pzaino/thecrowler`
- Branch: `develop`
- Reviewed HEAD: `180bd5f9b69fbb1806366a915435a5f96e514276` (no commit created)
- Worktree: `/home/pzaino/tmp/opencode/thecrowler-wp04c2k`
- Go: `go1.27.3 linux/amd64`
- Node: `v24.22.1`
- Docker: `29.7.2-ce`
- Docker Compose: `v5.6.1`
- Disposable PostgreSQL versions: `17` and `15.10-bookworm`
- Release: `v2.3.0`
- Schema: `1.17`
- Writer generation: `verified-ownership-v1.17`
- Execution backend: `none`

All logs are outside the repository under `/home/pzaino/tmp/opencode/`.

## Required Go matrix

| Command | Result | Log |
| --- | --- | --- |
| `go test ./... -count=1` | PASS | `wp04c2k-final-suite2.log` |
| `go test -race -count=1 ./pkg/agent ./pkg/database ./services/api ./services/events` | PASS, no data race | `wp04c2k-final-suite2.log` |
| `go vet ./...` | PASS | `wp04c2k-final-static3.log` |
| `go vet -tags=integration ./pkg/database` | PASS | `wp04c2k-final-static3.log` |
| `gofmt` over all changed Go files | PASS, no output | `wp04c2k-final-static3.log` |
| `git diff --check` | PASS | `wp04c2k-final-static3.log` |
| `CGO_ENABLED=0 go build ./...` | PASS | `wp04c2k-final-static3.log` |
| `CGO_ENABLED=1 go build ./...` | PASS | `wp04c2k-final-static3.log` |

The full suite includes the new fixture packages under
`tests/integration/mcp-authorization/`; both report `[no test files]` as
expected for command fixtures.

## PostgreSQL integration

### PostgreSQL 17

```sh
CROWLER_MCP_POSTGRES_TEST_DISPOSABLE=1 \
CROWLER_MCP_POSTGRES_DSN='postgres://postgres:<redacted>@127.0.0.1:55484/SitesIndex?sslmode=disable' \
go test -tags integration ./pkg/database \
  -run '^TestPostgresMCPMutation(DurableStore|DeploymentApplyDisable|AuditAndRetention|BackupRestoreRehearsal)$' \
  -count=1 -v
```

PASS:

- `TestPostgresMCPMutationAuditAndRetention`
- `TestPostgresMCPMutationDeploymentApplyDisable`
- `TestPostgresMCPMutationDurableStore`
- `TestPostgresMCPMutationBackupRestoreRehearsal`

Log: `wp04c2k-pg-final3.log`. No SKIP.

### PostgreSQL 15.10

The same suite was run against a disposable
`postgres:15.10-bookworm` instance on port `55486`. All four tests PASS.
Log: `wp04c2k-pg-final3.log`.
The restore rehearsal tolerates only the known client artifact
`SET transaction_timeout = 0` emitted by the newer host `pg_restore`; it still
verifies the restored audit row count. Any other restore error fails.

## SQLite and startup compatibility

```sh
go test ./pkg/database -run '^(TestSQLiteForeignKeyEnforcement...|TestStartupCompatibility...)$' -count=1 -v
```

PASS:

- SQLite foreign-key orphan rejection and violation reporting
- SQLite runtime foreign keys on pooled connections
- MCP mutation SQLite migration repeatability
- SQLite MCP mutation runtime foreign-key enforcement
- SQLite v1.17 migration convergence with history preservation
- startup rejection of v1.16
- startup rejection of falsely labeled incomplete v1.17
- startup acceptance of valid v1.17
- startup rejection of incompatible active writer generation
- startup rejection of disabled SQLite foreign keys
- startup rejection of SQLite foreign-key violations
- SQLite deployment apply/disable without losing history
- SQLite restart preserving proposal and quota

Log: `wp04c2k-sqlite-vdi-final2.log`.

## VDI deterministic negative controls

```sh
go test ./pkg/vdi \
  -run '^(TestCamouflageControlsDetectLegacyDefects|TestCamouflageLegacyAudioControlIsDeterministic)$' \
  -count=20
```

PASS on Node `v24.22.1`. Log: `wp04c2k-sqlite-vdi-final2.log`.

## Live end-to-end authorization fixture

Command:

```sh
bash ./tests/integration/mcp-authorization/run.sh
```

The fixture starts a controlled stdio MCP mutation server, a fake model, a slow
model for cancellation, PostgreSQL 15.10 schema 1.17, the real API approval
endpoint and the real Events manual-run endpoint. The API image does **not**
mount the MCP binary; only Events does. The API still starts because approval
normalization does not require local MCP executables.

Final result: PASS, `wp04c2k-e2e-final3.log` (cleanup enabled, no leftover
containers, volumes, workspaces or fixture model processes). The inspection run
with `CROWLER_E2E_KEEP=1` is `wp04c2k-e2e-13.log`.

Verified positive path:

- authenticated owner starts a manual run;
- real agent tool loop calls the fake model;
- exactly one inert proposal is admitted;
- proposal state is `approval_required`;
- no approval exists before operator action;
- correct principal and source ownership;
- current policy identity and proposal fingerprint accepted;
- one `mcp_mutation_proposal_created` audit row;
- operator approval creates one active approval and one
  `mcp_mutation_approval_issued` audit row;
- no mutation counter entry;
- zero `MCPMutationDispatchAttempts`;
- no dispatch/uncertain/reconciliation state.

Verified negative paths:

- request cancellation during a model call: cancelled provider request observed,
  no proposal inserted; the same slow agent run without cancellation admits
  exactly one inert control proposal;
- API/Events policy ID mismatch: HTTP 409, no approval;
- same policy ID/version but changed canonical hash: HTTP 409, no approval;
- disabled API policy: HTTP 503, no approval;
- source ownership transferred after proposal creation: HTTP 403, no approval;
- new manual run after transfer: denied at durable admission, no proposal,
  `mcp_mutation_admission_denied` audit written;
- incorrect proposal fingerprint: HTTP 409, no approval;
- expired proposal: HTTP 410, no approval;
- corrupted stored policy snapshot: HTTP 503, no approval;
- disabled proposal principal: HTTP 403, no approval;
- database outage: non-success approval response; after restart no approval row;
- audit detail contains no `source_id` or `fixture-resource` values.

Every case asserts zero mutation fixture execution, zero dispatch attempt rows,
and no `dispatching`, `uncertain` or `reconciliation_required` state.

The fixture cleans up its containers, volumes and workspace when
`CROWLER_E2E_KEEP` is not `1`. The final run left no `wp04c2k-e2e` containers,
volumes or work directories.

## Repository-level and focused tests

The full suite and race suite cover the focused packages. The new coverage is
organized as:

- `pkg/agent/mcp_mutation_approval_policy_test.go`
- `pkg/agent/mcp_admission_authorization_test.go`
- `pkg/agent/context_propagation_test.go`
- `pkg/agent/mcp_mutation_test.go` (scope-selector ambiguity)
- `pkg/agent/mcp_config_test.go`
- `pkg/agent/mcp_mutation_store_test.go`
- `services/api/mcp_mutation_test.go`
- `pkg/database/mcp_mutation_restore_postgres_integration_test.go`

## Tests not run

- The Information Seed Compose acceptance suite was **not run** in this work
  package. This report does not claim it passed.
- The live Ollama mutation test was not run. The E2E fake model replaced it for
  deterministic authorization testing.
- No test ran against a shared or production database.
- No remote MCP endpoint was contacted for a mutating call.

## Distinction between evidence classes

Executed and observed: all commands and test names above.

Not executed: Information Seed acceptance, live Ollama, production deployment,
CI itself (the workflow file was updated and the same commands were run locally
against PostgreSQL 17).

Inferred: deployment restart ordering for API and Events follows from the
process-frozen policy design; the live mismatch tests demonstrate the
fail-closed result.

## Patch applicability

The final patch was generated from the isolated worktree against
`180bd5f9b69fbb1806366a915435a5f96e514276`:

```sh
git diff --binary --full-index --no-ext-diff 180bd5f9 > thecrowler-wp04c2k.patch
```

A fresh shared clone of the base commit passed `git apply --check`, then applied
the patch cleanly. The applied tree passed:

- `go test ./... -count=1` (`wp04c2k-patchcheck-final2.log`)
- `go test -race -count=1 ./pkg/agent ./pkg/database ./services/api ./services/events`
  (`wp04c2k-patchcheck-final2.log`)
- `go vet ./...`, `go vet -tags=integration ./pkg/database`, `gofmt`,
  `git diff --check`, CGO 0/1 builds (`wp04c2k-patchcheck-static2.log`)

The final patch hash is reported in the delivery message, after this report is
included in the patch.
