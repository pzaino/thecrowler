# E2E Cleanup and Workflow Security Review

## Scope

This review covers the uncommitted change to:

- `.github/workflows/mcp-authorization-e2e.yml`
- `.github/workflows/go.yml`
- `scripts/cleanup-e2e-dind.sh`
- `scripts/tests/cleanup-e2e-dind.sh`
- `tests/integration/mcp-authorization/test_workflow.py`

No production Go code, schema, migration, API/Events contract, MCP mutation
state transition or execution backend was changed.

## Source-verified guarantees

### Host daemon selection

- The start step rejects pre-set `DOCKER_HOST`, `DOCKER_CONTEXT`,
  `DOCKER_TLS_VERIFY` and `DOCKER_CERT_PATH` before creating the outer dind
  (`.github/workflows/mcp-authorization-e2e.yml` lines 39-45).
- It requires the active host context to be `default` (lines 46-52).
- The cleanup script unsets the same selectors for every Docker command
  (`scripts/cleanup-e2e-dind.sh` lines 22-25).
- It requires `docker info` and `docker context show` to succeed, and requires
  the context to be `default` (lines 48-59).
- The final workflow cleanup step uses the host-scoped script in `always()`
  (lines 140-151), not plain `docker` commands inherited from `$GITHUB_ENV`.

### Exact ownership

- Run ID and run attempt are numeric-validated (lines 18-19).
- The dind name is character-validated (line 20).
- The run-local metadata is checked for container ID, name, run ID and attempt
  before it is trusted (lines 27-46).
- Candidates are discovered by exact anchored name and by the public run label
  (lines 61-72).
- Every candidate is inspected for its actual ID, name, run label and attempt
  label (lines 74-94).
- The recorded ID, when present, must match (lines 95-98).
- Removal uses the verified immutable ID (line 99).
- The script verifies the recorded ID, exact name and run/attempt labels are
  absent after removal (lines 106-131).
- No broad label-only removal exists.
- No `docker system prune`, broad `docker rm`, `compose down -v` or
  image/volume/network deletion exists.

### Dind image identity

- The workflow uses the digest-qualified
  `docker:dind@sha256:7bf70dcb85c822e3fa6f2d332143e474b99147b9d7b39102b0cfe8b04a7dd61b`
  (line 54).
- Pull, image inspect and run all use `"$dind_image"` (lines 56-68).
- The image ID is captured before launch and compared with the container's
  image ID after launch; a mismatch is `BLOCKED` (lines 57-73).
- There is no mutable tag fallback.
- The container is published only on a dynamic `127.0.0.1` port (line 65).

### Workflow privilege and triggers

- Trigger remains `workflow_dispatch` only.
- Permissions remain `contents: read`.
- No new service credential, token scope or secret is introduced.
- Ordinary CI gains only a runner-temporary Python virtual environment and
  pinned `PyYAML==6.0.2` plus pinned `actionlint@v1.7.7`.
- The heavy dind job is not attached to push or pull request.

### Evidence retention

- The cleanup step writes `cleanup.log`.
- Artifact upload runs after cleanup and uses `always()`.
- The cleanup step uses `set -euo pipefail`; a cleanup failure still fails the
  job and does not report success.
- The E2E result gate requires `PASS`, at least 50 checks, zero
  FAIL/BLOCKED/SKIP, cleanup `PASS` and the `fixture completed` marker.

### Mutation inertness

The local E2E result contains explicit PASS checks for zero dispatch attempts
and no dispatch states across positive admission, positive approval, the
negative matrix and database outage. No `MCPMutationDispatchAttempts` write,
`dispatching`, `uncertain` or `reconciliation_required` state appeared.

## Adversarial evidence

- 15/15 fake-Docker cleanup cases pass, including foreign label, foreign name,
  different attempt, recorded-ID mismatch, regex lookalike, inspect failure,
  remove failure, host unavailable, unexpected context, nested `DOCKER_HOST`,
  partial setup and repeated cleanup.
- 5/5 real-daemon rehearsal scenarios pass with no foreign removal and no
  leftovers.
- Mutation checks reject an empty pull, removed host isolation, removed context
  guard, added trigger and removed attempt comparison.
- The final local E2E passes 60/60 with cleanup `PASS` and outer dind absent.

## Residual risks

- **Workflow not registered.** The workflow is absent from the protected
  default branch. The GitHub E2E gate is blocked; the local E2E is not a hosted
  run.
- **Fast CI not observed on GitHub.** The new `go.yml` steps have not run on
  GitHub for this change. Their local behaviour is verified; the hosted job URL
  is pending.
- **Labels are not authentication.** Exact name, run, attempt, label and ID are
  operational ownership evidence. A host-Docker-privileged actor can forge
  them; such an actor already controls the daemon.
- **Inspect/remove race.** Docker has no conditional delete by ID and label.
  The script inspects the exact ID immediately before removal; a vanished ID
  fails closed. A recreated container cannot reuse the same immutable ID.
- **Post-query foreign container.** A container created with the same public
  run label after the candidate query is not removed and may remain. The script
  does not broaden the search to chase it.
- **Self-hosted endpoint selectors.** A non-default host context or socket is
  rejected by design. Supporting one requires a separately reviewed endpoint
  mechanism.
- **User Docker configuration.** The default context is required, but a
  manipulated default-context endpoint in local Docker configuration is outside
  the threat model for GitHub-hosted runners.
- **Supply chain.** PyYAML and actionlint are version-pinned, not
  hash-pinned. CI must reach PyPI and the Go module proxy.
- **Existing product risks.** Out-of-band `SourceOwnership` writes can bypass
  the source lock protocol; agent and policy snapshots are frozen per process;
  record-only approval binds configured transport identity, not a fully
  verified live execution profile.
- **Simulator gate.** No Option A approval exists; no simulator code exists.
  External execution remains unapproved.

## Conclusion

The change reduces cleanup authority from a public run label to an exact
host/name/run/attempt/ID match and keeps the heavy E2E manual, least-privilege
and digest-pinned. It introduces no production execution path. The remaining
blockers are publication, hosted E2E observation and the separate simulator
decision.
