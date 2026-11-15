# GitHub E2E Evidence

## Summary

| Item | State |
| --- | --- |
| Ordinary GitHub checks at reviewed SHA | 8/8 success |
| Manual opt-in E2E workflow | **BLOCKED / not run** |
| Local isolated E2E | PASS, 60/60 |
| Local safety suite | PASS, 9/9 |
| Exact host cleanup after local E2E | PASS |
| GitHub E2E URL, run ID or artifact | None |

## Ordinary GitHub checks

At `c5a763a64491f72f508d2768034d532c4fe3d748`:

| Check | URL |
| --- | --- |
| PostgreSQL integration | https://github.com/pzaino/thecrowler/actions/runs/43712945449/job/132808281661 |
| build | https://github.com/pzaino/thecrowler/actions/runs/43712945419/job/132808281481 |
| SQL parser CGO=0 | https://github.com/pzaino/thecrowler/actions/runs/43712945421/job/132808281585 |
| SQL parser CGO=1 | https://github.com/pzaino/thecrowler/actions/runs/43712945421/job/132808281387 |
| Static binaries linux/amd64 | https://github.com/pzaino/thecrowler/actions/runs/43712945421/job/132808281476 |
| Static binaries linux/arm64 | https://github.com/pzaino/thecrowler/actions/runs/43712945421/job/132808281629 |
| Static binaries darwin/amd64 | https://github.com/pzaino/thecrowler/actions/runs/43712945421/job/132808281589 |
| Static binaries darwin/arm64 | https://github.com/pzaino/thecrowler/actions/runs/43712945421/job/132808281563 |

These checks cover the reviewed baseline, not the uncommitted workflow and
cleanup change. The new fast static step has not run on GitHub.

## Manual E2E status

**BLOCKED — not registered.**

Observed evidence:

- the repository workflow list has no `mcp-authorization-e2e.yml` entry;
- `GET /repos/pzaino/thecrowler/actions/workflows/mcp-authorization-e2e.yml/runs`
  returns 404;
- no `workflow_dispatch` run named `MCP authorization E2E` exists;
- the repository default branch is `main`;
- `main` does not contain the workflow, the E2E harness, the cleanup script or
  the MCP mutation packages.

The publication and dispatch steps are in
`E2E_DEFAULT_BRANCH_REGISTRATION.md`. Do not treat the ordinary 8/8 checks as
E2E evidence.

## Local isolated E2E

The final E2E ran on a disposable Docker-in-Docker daemon after the code and
tests were frozen.

- Log: `/home/pzaino/tmp/opencode/crowler-e2e-final-isolated-e2e.log`
- Result:
  `/tmp/opencode/crowler-e2e-final/db97508a78d382f448643b5defe1ad41/results.json`
- Source SHA: `c5a763a64491f72f508d2768034d532c4fe3d748`
- Source tree digest at E2E time:
  `cdffa3998b0b45b2f77635ff0cd7d2d539bd77463b44535458dce0ba4fc0322d`
  (the review documents in this directory were added afterward and do not
  affect runtime)
- Result: `PASS`
- Counts: `PASS=60`, `FAIL=0`, `BLOCKED=0`, `SKIP=0`
- Final check: `fixture completed` PASS
- Cleanup: `PASS`
- Image roles: API, Events and model images all carried the source-tree label
  `cdffa3998b0b45b2f77635ff0cd7d2d539bd77463b44535458dce0ba4fc0322d`.

Zero-mutation evidence includes explicit PASS checks for:

- positive admission: zero dispatch attempts;
- positive admission: no dispatch states;
- positive approval: zero dispatch attempts;
- positive approval: no dispatch states;
- negative matrix: zero dispatch attempts;
- negative matrix: no dispatch states;
- database outage: zero dispatch attempts;
- database outage: no dispatch states.

No `MCPMutationDispatchAttempts` write, `dispatching`, `uncertain` or
`reconciliation_required` state appears in the structured result.

## Local safety suite

The same disposable daemon ran the harness safety suite before the E2E:

- Log: `/home/pzaino/tmp/opencode/crowler-e2e-final-safety.log`
- Result: 9/9 PASS
- Cases include stale image provenance rejection, stale PID handling, artifact
  redaction, foreign-resource preservation and exact run cleanup.

## Outer dind cleanup

The outer dind was named `crowler-e2e-dind-889-1` with run/attempt labels and a
recorded container ID. The final cleanup script reported:

```text
PASS: exact outer dind container absent (removed 1)
OUTER_ABSENT
```

No run-owned host or nested containers, volumes, networks or images remained.

## Artifact hygiene

A scan of the result directory, safety log and E2E log found no:

- PostgreSQL DSNs or passwords;
- bearer tokens or JWT-shaped strings;
- `fixture-resource` or raw `source_id` values;
- private-key markers.

The scan result was `NO_SECRET_PATTERNS`.

## What is still required

A real GitHub run on the exact applied SHA. Required evidence:

1. workflow run URL, run ID, attempt and head SHA;
2. pinned dind pull and image-ID equality;
3. host versus nested endpoint provenance;
4. safety suite PASS;
5. `results.json` with at least 50 PASS, zero FAIL/BLOCKED/SKIP and the
   `fixture completed` marker;
6. inner `cleanup.json` PASS and host dind cleanup PASS;
7. zero dispatch attempts and zero mutation fixture calls;
8. no `dispatching`, `uncertain` or `reconciliation_required` states;
9. image source SHA/tree labels matching the tested revision;
10. sanitized artifact ID/URL and no surviving resources.

Until that exists, the hosted E2E gate is **BLOCKED**, not passed.
