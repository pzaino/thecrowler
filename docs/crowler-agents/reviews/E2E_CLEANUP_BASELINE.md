# E2E Cleanup and Workflow Baseline

## Repository state at inspection

- Repository: `pzaino/thecrowler`
- Branch: `develop`
- Checked HEAD: `c5a763a64491f72f508d2768034d532c4fe3d748`
- Previous reviewed SHA: `d5314ce2272c7136352e772b2e3b394b17ee319e`
- Comparison:
  https://github.com/pzaino/thecrowler/compare/d5314ce2272c7136352e772b2e3b394b17ee319e...c5a763a64491f72f508d2768034d532c4fe3d748
- Working tree at inspection: clean
- Default branch: `main` at `6c06a9a5ff2b2cd954e444e82f4045f65170ddb1`
- Release: `v2.3.0`
- Schema: `1.17`
- Writer generation: `verified-ownership-v1.17`
- Operator mutation policy: default disabled
- Execution backend: `none`

The comparison contains the single reviewed change:

```text
c5a763a6 added support for Opt-in E2E Workflow Recovery and Gated Read-Only MCP Execution-Profile Simulation
```

## Reviewed GitHub checks

Eight ordinary checks passed at `c5a763a`:

| Check | Result | URL |
| --- | --- | --- |
| PostgreSQL integration | success | https://github.com/pzaino/thecrowler/actions/runs/43712945449/job/132808281661 |
| build | success | https://github.com/pzaino/thecrowler/actions/runs/43712945419/job/132808281481 |
| SQL parser CGO=0 | success | https://github.com/pzaino/thecrowler/actions/runs/43712945421/job/132808281585 |
| SQL parser CGO=1 | success | https://github.com/pzaino/thecrowler/actions/runs/43712945421/job/132808281387 |
| Static binaries linux/amd64 | success | https://github.com/pzaino/thecrowler/actions/runs/43712945421/job/132808281476 |
| Static binaries linux/arm64 | success | https://github.com/pzaino/thecrowler/actions/runs/43712945421/job/132808281629 |
| Static binaries darwin/amd64 | success | https://github.com/pzaino/thecrowler/actions/runs/43712945421/job/132808281589 |
| Static binaries darwin/arm64 | success | https://github.com/pzaino/thecrowler/actions/runs/43712945421/job/132808281563 |

The PostgreSQL job steps all succeeded, including client selection, schema and
grants, repeatable migrations, the six dedicated MCP tagged tests and the broad
integration suite.

These are historical checks for `c5a763a`, not for the uncommitted change
reviewed here. The new fast workflow checks and the manual E2E workflow have not
run on GitHub for this change.

## Source inventory

The reviewed change is limited to workflow, cleanup, static-test and review
documentation:

Modified:

- `.github/workflows/go.yml`
- `.github/workflows/mcp-authorization-e2e.yml`
- `scripts/cleanup-e2e-dind.sh`
- `scripts/tests/cleanup-e2e-dind.sh`
- `tests/integration/mcp-authorization/test_workflow.py`

Added review documents:

- `docs/crowler-agents/reviews/E2E_CLEANUP_BASELINE.md`
- `docs/crowler-agents/reviews/E2E_CLEANUP_OWNERSHIP.md`
- `docs/crowler-agents/reviews/E2E_WORKFLOW_STATIC_CI.md`
- `docs/crowler-agents/reviews/E2E_DEFAULT_BRANCH_REGISTRATION.md`
- `docs/crowler-agents/reviews/E2E_GITHUB_EVIDENCE.md`
- `docs/crowler-agents/reviews/EXECUTION_PROFILE_OPTION_A_GATE.md`
- `docs/crowler-agents/reviews/EXECUTION_PROFILE_OPTION_A_APPROVAL_REQUEST.md`
- `docs/crowler-agents/reviews/EXECUTION_PROFILE_CONTRACT.md`
- `docs/crowler-agents/reviews/E2E_CLEANUP_SECURITY_REVIEW.md`
- `docs/crowler-agents/reviews/E2E_INTEGRATION_TEST_REPORT.md`
- `docs/crowler-agents/reviews/E2E_MAINTAINER_DECISIONS.md`

No production Go source, schema, migration, API/Events contract or execution
backend was changed.

## Defect at the reviewed baseline

The outer dind container name included both the GitHub run ID and run attempt.
At `c5a763a`, `scripts/cleanup-e2e-dind.sh` lines 39-45 enumerated every host
container carrying only:

```text
label=org.crowler.e2e.dind=<run_id>
```

and removed each one after checking only that run label. The label did not
include the attempt, the exact name or an immutable container ID. A container
with the same public run label but a different name or attempt could therefore
be deleted. The same weakness existed in the name-filter path at lines 48-54
and in the post-removal verification at lines 56-63.

The final `always()` workflow step at
`.github/workflows/mcp-authorization-e2e.yml` lines 132-141 passed only
`RUN_ID` and `DIND_NAME`; it did not carry the attempt or the recorded container
identity.

The empty `docker pull ""` defect and the host/nested endpoint guard from the
previous change were already repaired at `c5a763a`:

- `.github/workflows/mcp-authorization-e2e.yml` lines 29-47 guard endpoint
  selectors and log the host context.
- lines 49-63 pull, inspect and run the same digest-qualified dind image.
- `scripts/cleanup-e2e-dind.sh` unsets nested selectors before every host
  Docker command.

This change preserves those repairs and adds exact ownership.

## Gate states

| Gate | State |
| --- | --- |
| B1 operator policy | Approved, implemented |
| B2 principal provenance | Approved, implemented |
| B3 proposal-only registration | Approved, implemented |
| B4 current-policy revalidation | Recorded approval, implemented |
| B5 denied-admission audit/retention | Approved, implemented |
| G3 schema/SQLite FK | Approved, implemented |
| Deny/revoke transactional administration | Implemented previously |
| Read-only execution-profile simulator | **Not approved** |
| External execution | **Not approved** |

## Baseline verification

The final tree passed the full Go matrix. No production Go source changed, so
the same results characterize the reviewed baseline behavior:

```text
go test ./... -count=1
go test -race ./pkg/agent ./pkg/database ./services/api ./services/events -count=1
go vet ./...
go vet -tags=integration ./pkg/database
CGO_ENABLED=0 go build ./...
CGO_ENABLED=1 go build ./...
```

Logs:

- `/home/pzaino/tmp/opencode/crowler-e2e-final-go-suite.log`
- `/home/pzaino/tmp/opencode/crowler-e2e-final-go-static.log`

## Not run

- GitHub `workflow_dispatch` E2E. The workflow is not registered because it is
  absent from the protected default branch.
- Information Seed Compose acceptance suite.
- Live Ollama or a real external MCP endpoint.
- Any simulator execution. The simulator gate remains blocked.
- Production or shared database operations.
