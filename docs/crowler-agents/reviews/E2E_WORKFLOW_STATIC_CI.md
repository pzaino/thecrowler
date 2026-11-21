# Fast Workflow and Cleanup Static CI

## Why ordinary CI

The workflow static tests and the fake-Docker cleanup tests previously ran only
on a developer machine. A green `build` or `postgres-integration` check did not
prove that the workflow YAML, the exact-ownership cleanup rules or the
nested/host endpoint separation were exercised.

The ordinary `Go` workflow is the correct place for a cheap regression: it
already runs on every push and pull request to `main` and `develop`, has
`permissions: read-all`, and does not require a privileged Docker daemon.

## Changes

`.github/workflows/go.yml` now has two additional steps after the Go tests:

- `Set up workflow test dependencies` (lines 32-37) creates a runner-temporary
  virtual environment and installs exactly `PyYAML==6.0.2` with pip's version
  check disabled and input disabled. A missing package or a failed install
  fails the step; there is no fallback to whatever happens to be installed.
- `Workflow and exact-cleanup static regressions` (lines 39-47) runs:
  - `python -m unittest discover -s tests/integration/mcp-authorization -p 'test_*.py' -v`;
  - `bash scripts/tests/cleanup-e2e-dind.sh`;
  - `go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.7` against
    `.github/workflows/mcp-authorization-e2e.yml` and
    `.github/workflows/postgresql-integration.yml`.

The workflow keeps its existing `push`/`pull_request` triggers and its
`read-all` permissions. No service credentials, Docker socket access or
privileged container is added to ordinary CI.

The heavy E2E workflow remains `workflow_dispatch` only. It is not called from
`go.yml` and is not attached to pull requests.

## Static coverage

`tests/integration/mcp-authorization/test_workflow.py` now has 13 tests:

- manual-only `workflow_dispatch` trigger and `contents: read`;
- nonempty, digest-qualified dind reference shared by pull, inspect and run;
- absence of the empty `docker pull ""` form;
- pre-launch image ID comparison;
- attempt label, run attempt and metadata binding;
- exact-ownership comparisons for name, run label, attempt and recorded ID;
- host-daemon-only cleanup through the unset selectors, default context check
  and exact name pattern;
- cleanup step uses the host-scoped script with run ID, attempt, name and
  metadata;
- no broad `rm`, `prune` or `down -v` in the cleanup step;
- artifact upload happens after cleanup and uses `always()`;
- the fast `Go` workflow actually invokes the Python tests, the cleanup suite
  and the pinned actionlint;
- ordinary `Go` CI retains push and pull-request triggers.

## Cleanup coverage

`scripts/tests/cleanup-e2e-dind.sh` has 15 deterministic fake-Docker cases,
listed in `E2E_CLEANUP_OWNERSHIP.md`. The fake daemon records removal calls and
rejects any leaked `DOCKER_HOST`, so a successful case proves both the expected
removal count and host scoping.

## Observed local results

```text
python3 -m unittest discover -s tests/integration/mcp-authorization -p 'test_*.py'
Ran 13 tests ... OK

bash scripts/tests/cleanup-e2e-dind.sh
15/15 PASS

go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.7 \
  .github/workflows/mcp-authorization-e2e.yml \
  .github/workflows/go.yml \
  .github/workflows/postgresql-integration.yml
exit 0
```

Logs:

- `/home/pzaino/tmp/opencode/crowler-e2e-final-actionlint.log`
- `/home/pzaino/tmp/opencode/crowler-e2e-final-cleanup.log`
- `/home/pzaino/tmp/opencode/crowler-e2e-final-python.log`

## Mutation checks

A disposable copy of the relevant workflow, scripts and tests was mutated five
ways. Each mutation had to make the corresponding regression fail:

| Mutation | Regression that rejected it |
| --- | --- |
| `docker pull ""` | digest/argument static test |
| Remove host endpoint isolation | host static test and nested-DOCKER_HOST fake case |
| Remove host-context guard | host static test and unexpected-context fake case |
| Add a `push` trigger | manual-only static test |
| Remove the attempt comparison | ownership static test and different-attempt fake case |

Observed: all mutations rejected as expected.

Log: `/home/pzaino/tmp/opencode/crowler-e2e-final-mutations.log`

## GitHub status

**PENDING.** This change has not been pushed. No GitHub job URL exists for the
new fast steps, and the 8/8 ordinary checks at `c5a763a` are baseline evidence,
not proof that these steps ran.

After the patch is applied and pushed through the normal review process, the
`build` job of the `Go` workflow should show:

1. `Set up workflow test dependencies`;
2. `Workflow and exact-cleanup static regressions`.

Do not mark this gate green until those steps have actually run and their job
URL is recorded. A missing `PyYAML` or actionlint download fails the job; it
does not skip the checks.

## Residual limits

- PyYAML is pinned by version, not by artifact hash.
- actionlint is pinned by module version, not by source or binary hash.
- The runner must reach PyPI and the Go module proxy. The steps fail closed if
  those are unavailable.
- The static tests are behavioural guards for the checked-in workflow; they do
  not replace the manual isolated E2E.
