# WP-04C2N Security Review

## Source-verified guarantees

- No production mutating MCP `tools/call`.
- No `MCPMutationDispatchAttempts` insert.
- No dispatcher, worker, lease claim, retry or reconciliation.
- Approval remains record-only; no automatic execution.
- Deny/revoke remain terminal and record-only.
- Existing in-transaction approval and administration authority remains in
  place.
- E2E inner cleanup removes only run-labelled application resources.
- New outer cleanup runs against the host daemon, rechecks labels and refuses
  foreign resources.
- Dind image is digest-qualified; pull, inspect and run use the same reference;
  container image ID is compared before launch.
- Runtime image provenance compares the container image ID and image labels with
  the tested source tree.
- Source digest/copy excludes bytecode, `.pyc`, `.pyo` and ignored
  `config.yaml`.
- Artifacts are sanitized.

## Host/nested endpoint isolation

The workflow rejects pre-set host endpoint selectors before dind startup. The
cleanup script unsets nested selectors and uses the default host socket. This is
correct for GitHub-hosted runners. A self-hosted runner with a non-default host
socket is intentionally unsupported without a separate review.

## Cleanup safety

The cleanup script:

- validates run ID and container name;
- queries only the exact run label;
- re-inspects before removal;
- preserves foreign labels/names;
- fails on inspect/remove/query errors;
- verifies absence;
- never prunes or runs compose teardown.

Fake-Docker tests cover success, absence, foreign label, foreign name, inspect
failure, remove failure, repeated cleanup and nested `DOCKER_HOST`.

## Docker-in-Docker

The dind runtime is privileged by design. It is:

- pinned by immutable digest;
- given a unique run name/label;
- published only on a dynamic loopback port;
- mounted only with workspace and runner temp;
- removed from the host endpoint in `always()`;
- covered by artifact upload after cleanup.

## Residual risks

- The opt-in workflow is not registered on GitHub because it is absent from the
  default `main` branch; dispatch is pending.
- The E2E workflow patch has not run on GitHub.
- A self-hosted runner with custom Docker endpoint selectors is rejected by
  design rather than supported.
- Direct out-of-band `SourceOwnership` writes bypass application locks.
- Agent/policy snapshots are frozen per process; agent uploads may require API
  restart.
- Record-only approval binds configured transport identity, not a completely
  verified live execution profile.
- Option A simulator is not approved; no simulator code exists.
- Option B external execution is not approved.
- Base/helper images other than dind remain tag-pinned.
