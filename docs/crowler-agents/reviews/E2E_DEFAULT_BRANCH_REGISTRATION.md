# Default-Branch Registration and Publication Plan

## Current state

- Repository default branch: `main`
- `main` SHA: `6c06a9a5ff2b2cd954e444e82f4045f65170ddb1`
- `main` is protected.
- The opt-in workflow `.github/workflows/mcp-authorization-e2e.yml` is absent
  from `main`.
- The E2E harness under `tests/integration/mcp-authorization/` is absent from
  `main`.
- `scripts/cleanup-e2e-dind.sh` and `scripts/tests/cleanup-e2e-dind.sh` are
  absent from `main`.
- The MCP mutation Go packages (`pkg/agent/mcp_mutation.go`,
  `pkg/database/mcp_mutation.go`, `services/api/mcp_mutation.go`) are absent
  from `main`.
- `develop` is 2629 commits ahead of `main`.

GitHub only registers `workflow_dispatch` workflows that exist on the default
branch. The repository workflow list contains no
`mcp-authorization-e2e.yml` entry, the workflow-runs API returns 404 for that
path, and no `workflow_dispatch` run named `MCP authorization E2E` exists.

## Consequence

The workflow cannot be selected in the Actions UI today. This is a repository
publication gate, not a test failure.

A workflow-only file on `main` would make the workflow visible, but it would not
be self-contained there: the harness, cleanup script and MCP code are absent.
Dispatching it against `main` would fail immediately.

## Publication options

### Option 1: minimal registration, run on `develop`

The smallest change that registers the workflow is a one-file pull request to
`main` adding only the reviewed
`.github/workflows/mcp-authorization-e2e.yml`.

After that PR is merged:

1. GitHub registers the workflow path.
2. Open **Actions > MCP authorization E2E (opt-in) > Run workflow**.
3. Select branch `develop` or the exact applied review branch. Do not select
   `main`.
4. Confirm the displayed commit SHA is the intended reviewed SHA before
   starting the run.

GitHub uses the workflow definition from the selected ref, so the `develop`
version with its harness, cleanup script and source code is used. The workflow
file on `main` is only the registration anchor.

This option is minimal but leaves a workflow on `main` that cannot run
self-contained there. The workflow description and the run instructions should
make that explicit.

### Option 2: full feature or release merge to `main`

The only way the workflow can run self-contained on `main` is to publish the
reviewed development line that contains:

- the workflow;
- the E2E harness and fixtures;
- the cleanup scripts and tests;
- the MCP mutation Go packages and their database/API wiring;
- the migrations and schema 1.17 that the fixture expects.

That is a normal product release decision, not a workflow-registration
workaround. Do not merge unrelated development changes solely to make the
workflow appear. Do not cherry-pick an arbitrary subset without verifying that
its dependencies are present on `main`.

## Required ref contents

Whichever option is used, the selected ref must contain all of:

- `.github/workflows/mcp-authorization-e2e.yml`;
- `scripts/cleanup-e2e-dind.sh`;
- `scripts/tests/cleanup-e2e-dind.sh`;
- `tests/integration/mcp-authorization/` including `run.sh`,
  `e2e_support.py`, `safety_tests.py`, the fixtures and the Python tests;
- the Go packages and schema used by the fixture.

`develop` at `c5a763a64491f72f508d2768034d532c4fe3d748` contains them. `main`
does not.

## Exact maintainer steps

For Option 1:

1. Create a branch from `main`.
2. Add only `.github/workflows/mcp-authorization-e2e.yml` from the reviewed
   change.
3. Open a pull request. Do not bypass protection, required reviews or required
   checks.
4. After merge, open **Actions > MCP authorization E2E (opt-in) > Run
   workflow**.
5. Select the `develop` branch or the exact applied review branch.
6. Confirm the run's head SHA and workflow path.
7. Capture the run URL, run ID, attempt, head SHA, start/finish timestamps and
   artifact ID.
8. Verify the pinned dind pull, image-ID comparison, host/nested endpoint
   provenance, safety suite, `results.json`, cleanup log, zero mutation
   dispatch attempts and zero surviving run-owned resources.

For Option 2:

1. Prepare a normal release pull request containing the complete reviewed
   feature and its dependencies.
2. Let protection, review and required checks complete.
3. Dispatch the workflow against the merged `main` SHA.
4. Capture the same evidence.

## Prohibited shortcuts

- Do not change the default branch.
- Do not bypass `main` protection.
- Do not add a `push` or `pull_request` trigger to the heavy E2E workflow.
- Do not merge unrelated changes solely to register the workflow.
- Do not dispatch a workflow without separate maintainer authorization.
- Do not report a local run as a GitHub run.

## Gate state

**BLOCKED.** Registration and dispatch require maintainer action under the
protected-branch process. No publication, push, merge or dispatch was performed
by this change.
