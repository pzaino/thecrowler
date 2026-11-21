# WP-04C2M Baseline

## Repository state

- Repository: `pzaino/thecrowler`
- Branch: `develop`
- Actual checked HEAD: `3247de3d0a4a90b16d3fa6980bee92ea4695a709`
- Previous WP-04C2L baseline: `904f5684ccd2fbc2650e5a3ffa1ff41f51d35757`
- Working tree at inspection: clean
- Review artifact: `The_CROWler_WP-04C2L_Review_2026-11-14.md` was not present in the repository or the supplied workspace paths. Its findings were taken from the task description and independently checked.
- Release: `v2.3.0`
- Required schema: `1.17`
- Writer generation: `verified-ownership-v1.17`
- Execution backend: `none`
- Remote mutating MCP call: absent
- Dispatcher/worker/dispatch claim: absent

There is one commit between the prior baseline and this HEAD:

```
3247de3d added Approval Boundary Closure, Hermetic Authorization E2E, and Pre-Dispatch Security Design
```

The working tree was not modified before the baseline checks.

## Changed-file inventory at HEAD

Production Go:

- `pkg/agent/mcp_admission_authorization.go`
- `pkg/agent/mcp_mutation_approval_authorization.go`
- `pkg/agent/mcp_mutation_store.go`
- `pkg/database/mcp_mutation.go`
- `services/api/mcp_mutation.go`

Workflow:

- `.github/workflows/postgresql-integration.yml`
- `.github/workflows/mcp-authorization-e2e.yml`

Tests:

- `pkg/agent/mcp_admission_authorization_test.go`
- `pkg/agent/mcp_mutation_approval_authorization_test.go`
- `pkg/agent/mcp_mutation_store_test.go`
- `pkg/database/mcp_mutation_test.go`
- `pkg/database/mcp_mutation_postgres_integration_test.go`
- `services/api/mcp_mutation_test.go`
- `tests/integration/mcp-authorization/*`

Docs:

- `docs/crowler-agents/mcp-mutation-authorization.md`
- `docs/crowler-agents/reviews/WP-04C2L_*`
- `docs/crowler-agents/reviews/WP-04C2M_DISPATCH_APPROVAL_REQUEST.md`

Tracked generated junk at HEAD:

- `tests/integration/mcp-authorization/__pycache__/e2e_support.cpython-313.pyc`
- `tests/integration/mcp-authorization/__pycache__/safety_tests.cpython-313.pyc`

## GitHub checks at HEAD

Eight check runs were reported for `3247de3d`:

| Check | Result | Link |
| --- | --- | --- |
| PostgreSQL cardinality integration | **FAILED** | https://github.com/pzaino/thecrowler/actions/runs/43643362767/job/132595567993 |
| build | success | https://github.com/pzaino/thecrowler/actions/runs/43643362707/job/132595567649 |
| SQL parser CGO=0 | success | https://github.com/pzaino/thecrowler/actions/runs/43643362792/job/132595567926 |
| SQL parser CGO=1 | success | https://github.com/pzaino/thecrowler/actions/runs/43643362792/job/132595567660 |
| Static binaries linux/amd64 | success | https://github.com/pzaino/thecrowler/actions/runs/43643362792/job/132595567851 |
| Static binaries linux/arm64 | success | https://github.com/pzaino/thecrowler/actions/runs/43643362792/job/132595567859 |
| Static binaries darwin/amd64 | success | https://github.com/pzaino/thecrowler/actions/runs/43643362792/job/132595567875 |
| Static binaries darwin/arm64 | success | https://github.com/pzaino/thecrowler/actions/runs/43643362792/job/132595567830 |

This is **7/8**, not green.

The PostgreSQL job failed at `Install PostgreSQL 17 client` and skipped:

- `Create current schema`
- `Verify CROWler runtime database role`
- `Verify repeatable v1.17 migration`
- `Verify v1.17 crawl-approval migration`
- `Run MCP mutation PostgreSQL integration`
- `Run PostgreSQL integration suite`

GitHub's public job-log download requires repository admin rights, so the exact log could not be fetched through the unauthenticated API. The job step status and annotations were read from the public API. The task-provided evidence says the installed 17 client resolved to 18.6; this was independently consistent with the local runner, where `/usr/bin/pg_dump` and `/usr/bin/pg_restore` are 18.6.

## Existing gate states

| Gate | State |
| --- | --- |
| B1 operator policy | Approved, implemented |
| B2 principal provenance | Approved, implemented |
| B3 proposal-only registration | Approved, implemented |
| B4 current-policy revalidation | Recorded approval, implemented in WP-04C2K |
| B5 denied-admission audit/retention | Approved, implemented |
| G3 schema deployment/SQLite FK | Approved, implemented |
| External execution backend | **Not approved** |
| Dispatcher/worker/claim/retry/reconciliation | **Not approved** |
| Read-only Option A simulator | **Not approved** |
| Option B limited execution | **Not approved** |

## Source-verified inertness

At baseline:

- no production `tools/call` for a mutation;
- no `MCPMutationDispatchAttempts` insert;
- no dispatch worker, claim, retry or reconciliation loop;
- no automatic execution after approval;
- approval remains record-only;
- schema and writer generation unchanged;
- native crawl authorization and Information Seed remain separate.

## Local toolchain

- Go: `go1.27.3 linux/amd64`
- Node: `v24.22.1`
- Docker: `29.7.2-ce`
- Docker Compose: `v5.6.1`
- Host `pg_dump`/`pg_restore`: `18.6`
- Disposable PostgreSQL: `17` and `15.10-bookworm`

## Baseline tests

Commands run before edits:

```sh
go test ./pkg/agent ./pkg/database ./services/api ./services/events -count=1
go test ./... -count=1
go test -race ./pkg/agent ./pkg/database ./services/api ./services/events -count=1
go vet ./...
go vet -tags=integration ./pkg/database
CGO_ENABLED=0 go build ./...
CGO_ENABLED=1 go build ./...
```

All passed:

- `/home/pzaino/tmp/opencode/wp04c2m-baseline-tests.log`
- `/home/pzaino/tmp/opencode/wp04c2m-baseline-static.log`

## Not run at baseline

- GitHub Actions itself: not triggered.
- Opt-in GitHub E2E: no run.
- Information Seed Compose suite: not run.
- Live Ollama: not run.
- Production/shared database: not contacted.
