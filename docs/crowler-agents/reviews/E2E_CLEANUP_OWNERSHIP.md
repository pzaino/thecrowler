# Exact-Ownership Outer Dind Cleanup

## Finding

The reviewed cleanup removed host containers from the public run-ID label alone.
Because the dind name includes the run attempt, but the label did not, a
container created by a different attempt with the same run label could be
removed. A same-name container with a different label could also be treated as
owned through the name query.

The required invariant is stricter: labels are evidence, not authentication.
A removable outer dind container must simultaneously satisfy:

1. The command is running against the host daemon, not the nested
   `DOCKER_HOST`.
2. The run ID matches exactly.
3. The run attempt matches exactly.
4. The inspected container name matches the expected dind name after removing
   Docker's leading `/`.
5. The dedicated run label and attempt label match.
6. Any recorded immutable container ID matches.
7. Inspect and removal use that exact verified container ID, not a broad filter.

## Implementation

`scripts/cleanup-e2e-dind.sh` now accepts:

```text
cleanup-e2e-dind.sh <run_id> <run_attempt> <dind_name> [metadata_json]
```

Source references:

- lines 14-20 validate the run ID, attempt and dind name;
- lines 22-25 run every Docker command through
  `env -u DOCKER_HOST -u DOCKER_CONTEXT -u DOCKER_TLS_VERIFY -u DOCKER_CERT_PATH docker`;
- lines 27-46 read the run-local `dind-container.json` and require its
  container ID, name, run ID and attempt to agree with the arguments;
- lines 48-59 verify the host daemon is reachable and that the active Docker
  context is the default host context;
- lines 61-72 build an exact anchored name filter and query the run label;
- lines 74-104 inspect every candidate and compare the actual ID, name, run
  label and attempt label before removing that exact ID;
- lines 106-131 verify that the recorded ID, exact name and run/attempt-labelled
  container are absent;
- the script never invokes `docker system prune`, a broad `docker rm`,
  `compose down -v`, or an image/volume/network deletion.

Docker treats `--filter name=` as a regular expression. The script escapes the
only regex-significant character permitted in a container name, so a lookalike
such as `name-extra` cannot match an exact name containing a dot.

## Workflow integration

`.github/workflows/mcp-authorization-e2e.yml`:

- lines 29-32 pass the run ID, run attempt and exact name to the start step;
- lines 61-68 launch dind with both `org.crowler.e2e.dind` and
  `org.crowler.e2e.attempt` labels;
- lines 74-77 record the container ID, image ID, image reference, name, run ID
  and attempt in `dind-container.json`;
- lines 140-151 run the cleanup script from the host endpoint in `always()`
  with the run ID, attempt, exact name and metadata path;
- lines 153-162 upload sanitized artifacts after cleanup, so `cleanup.log` is
  retained on failure as well as success.

The cleanup step uses `set -euo pipefail`; a cleanup failure fails the job. The
workflow still uses `workflow_dispatch` only and `contents: read`.

## Failure behavior

| Situation | Behavior |
| --- | --- |
| Exact owned container | Removed once, then verified absent |
| Cleanup repeated | Idempotent success after host checks |
| Same run label, different name | Preserved; nonzero exit |
| Same name, wrong run label | Preserved; nonzero exit |
| Same run, different attempt | Preserved; nonzero exit |
| Recorded ID mismatch | Preserved; nonzero exit |
| Name regex lookalike | Not selected; untouched |
| Inspect failure | No removal; nonzero exit |
| Remove failure | Nonzero exit; evidence retained |
| Host daemon unavailable | No query or removal; nonzero exit |
| Unexpected host context | No query or removal; nonzero exit |
| Nested `DOCKER_HOST` present | Ignored; host daemon used |
| Failure before metadata exists | Narrow exact-name plus exact run/attempt label fallback |
| Recorded container already absent | Idempotent success after verification |
| Metadata identity mismatch | Nonzero exit; no removal |

## Adversarial tests

`scripts/tests/cleanup-e2e-dind.sh` uses a fake Docker command and covers 15
cases:

1. exact owned removal;
2. repeated cleanup;
3. same run label, wrong name;
4. correct name, wrong run label;
5. same run, different attempt;
6. recorded ID mismatch;
7. name regex lookalike untouched;
8. inspect failure;
9. remove failure;
10. host daemon unavailable;
11. unexpected host context;
12. nested `DOCKER_HOST` not used;
13. partial setup without metadata;
14. recorded ID already absent;
15. metadata identity mismatch.

Observed: 15/15 PASS.

Log: `/home/pzaino/tmp/opencode/crowler-e2e-final-cleanup.log`

## Real-daemon rehearsal

A disposable local Docker daemon was used to exercise the final script with
real containers. The rehearsal created only private test containers and removed
them at the end.

| Scenario | Result |
| --- | --- |
| Exact owned container plus unlabelled regex lookalike plus unrelated foreign container, with a bogus nested `DOCKER_HOST` | Exact container removed; both foreign containers preserved; command success |
| Partial setup before metadata exists | Exact container removed through the narrow fallback |
| Same run label, different attempt | Preserved; nonzero exit |
| Same name, wrong run label | Preserved; nonzero exit |
| Recorded container ID mismatch | Preserved; nonzero exit |

Observed: 5/5 scenarios PASS, no leftovers.

Log: `/home/pzaino/tmp/opencode/crowler-e2e-final-cleanup-rehearsal.log`

## Final isolated E2E cleanup

The final local E2E run used:

- outer dind name `crowler-e2e-dind-889-1`;
- labels `org.crowler.e2e.dind=889` and `org.crowler.e2e.attempt=1`;
- a recorded container ID in the metadata file;
- the final cleanup script after the E2E completed.

Result:

```text
PASS: exact outer dind container absent (removed 1)
OUTER_ABSENT
```

The inner fixture cleanup removed only its run-labelled application
containers, volumes, networks and images. No host or nested resources remained.

## Residual limits

- Labels and the metadata file are ownership evidence, not cryptographic
  authentication. A hostile actor with host Docker access can forge them; such
  an actor already controls the daemon.
- Docker has no conditional-delete-by-ID-and-label operation. The script
  inspects the exact ID immediately before `docker rm -f`; if the ID disappears
  between those calls, removal fails closed. A recreated container cannot reuse
  the same immutable ID.
- A container created with the same run label after the candidate query is not
  removed and may remain. It is not the expected container; the script does not
  broaden its search to chase it.
- Self-hosted runners with a non-default host context or socket are rejected by
  design. Supporting them requires a separately reviewed endpoint-selection
  mechanism.
