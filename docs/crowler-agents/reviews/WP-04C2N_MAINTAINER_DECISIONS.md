# WP-04C2N Maintainer Decisions

## Completed under Phase A

- Fixed the empty `docker pull ""` dind startup failure.
- Pull/inspect/run now use the same digest-qualified dind image and fail if the
  running image ID differs.
- Added host endpoint guards before dind startup.
- Added host-scoped cleanup script and fake-Docker adversarial tests.
- Moved artifact upload after cleanup so teardown evidence is retained.
- Added workflow static tests and actionlint verification.
- Preserved manual-only trigger and `contents: read`.
- Preserved existing E2E harness, provenance, redaction and inner cleanup.
- Re-ran local isolated E2E/safety and the full Go/PostgreSQL/VDI/SQLite matrix.

## Pending decisions

### Default-branch workflow registration

The opt-in workflow is not registered because it is on `develop` and absent
from the default `main` branch. A maintainer must merge it to the default branch
through normal review before GitHub `workflow_dispatch` can be selected.

### Manual E2E dispatch

After the workflow is on the default branch and the patch is pushed, a
maintainer must run `MCP authorization E2E (opt-in)` on the exact applied SHA
and record the run URL, head SHA, attempt, artifact ID and host-dind cleanup
evidence.

### Option A simulator

No approval exists. The focused request is
`WP-04C2N_OPTION_A_APPROVAL_REQUEST.md`. Do not implement simulator code until a
tracked approval contains approver, UTC date, `WP-04C2N/Option-A-1`, exact scope
and Option B non-approval.

### Option B external execution

Still not approved. Do not implement dispatcher/worker/lease/retry/reconciliation
or remote mutating calls.

## Compatibility

- Release `v2.3.0`
- Schema `1.17`
- Writer generation `verified-ownership-v1.17`
- Execution backend `none`
- No public API/Events request or response change
- Native crawl and Information Seed unchanged

## Rollback

Revert this patch. No schema, migration, marker or production data change is
included. The manual workflow remains opt-in.
