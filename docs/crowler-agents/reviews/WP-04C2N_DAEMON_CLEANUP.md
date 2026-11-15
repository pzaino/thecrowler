# WP-04C2N Host/Nested Daemon Cleanup

## Problem

The opt-in workflow exports:

```text
DOCKER_HOST=tcp://127.0.0.1:<dynamic-port>
```

to `$GITHUB_ENV` after dind starts. Every later step inherits the nested daemon
endpoint. The outer privileged dind container exists only on the **host**
daemon. Plain `docker ps/inspect/rm` in the final step therefore inspect the
wrong daemon and can leave the outer container behind.

## Fix

`scripts/cleanup-e2e-dind.sh` is invoked by the final `always()` step. It:

- receives the exact run ID and dind container name;
- runs every Docker command through
  `env -u DOCKER_HOST -u DOCKER_CONTEXT -u DOCKER_TLS_VERIFY -u DOCKER_CERT_PATH docker ...`;
- queries only the run label `org.crowler.e2e.dind=<run_id>`;
- re-inspects each candidate and refuses to remove it unless the label matches;
- detects a same-name container without the run label, preserves it and fails;
- fails on inspect/remove errors;
- verifies no run-labelled or exact-name container remains;
- fails if the container is still inspectable;
- never runs `prune`, `down -v`, broad `rm`, or a compose teardown.

The workflow uploads artifacts after cleanup, so `cleanup.log` is retained on
failure as well as success. A cleanup failure marks the job failed; it does not
exit zero.

## Test regression

`scripts/tests/cleanup-e2e-dind.sh` uses a fake Docker command and covers:

| Case | Result |
| --- | --- |
| owned label removal | PASS |
| repeated cleanup (already absent) | PASS |
| foreign label preserved, no removal | PASS |
| same name with foreign label preserved | PASS |
| failed inspect reported | PASS |
| failed remove reported | PASS |
| nested `DOCKER_HOST` is not used | PASS |

Command:

```sh
bash scripts/tests/cleanup-e2e-dind.sh
```

Observed: 7/7 PASS.

## Host endpoint assumptions

The start step rejects nonempty `DOCKER_HOST`, `DOCKER_CONTEXT`,
`DOCKER_TLS_VERIFY` or `DOCKER_CERT_PATH` before creating dind. This is explicit
for GitHub-hosted runners. A self-hosted endpoint selector would need a separate
approved workflow because blindly unsetting a host selector would target the
default socket instead.

## Inner cleanup

The existing E2E harness continues to remove only its run-labelled inner
containers, volumes, networks and images. The outer cleanup script is separate
and does not alter that path.

## Local evidence

The full local E2E and safety suite ran on a disposable dind daemon:

- 60 PASS, 0 FAIL/BLOCKED/SKIP
- cleanup PASS
- no leftover inner or outer resources

GitHub execution remains pending because the opt-in workflow is not registered
on the default branch.
