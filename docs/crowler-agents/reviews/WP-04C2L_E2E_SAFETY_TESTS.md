# WP-04C2L E2E Safety Tests

## Scope

`tests/integration/mcp-authorization/safety_tests.py` exercises the harness's
destructive and collision-sensitive behavior without running the full fixture.
It creates only uniquely named test resources and removes them by exact name or
label.

## Tests and results

Run on the host daemon:
`/home/pzaino/tmp/opencode/wp04c2l-safety-host2.log`

Run on the disposable Docker-in-Docker daemon:
`/home/pzaino/tmp/opencode/wp04c2l-safety-dind3.log`

Structured result:
`/home/pzaino/tmp/opencode/wp04c2l-safety-dind3/safety-results.json`

| # | Test | Result | What it proves |
| --- | --- | --- | --- |
| 1 | Existing Compose project collision | PASS | A foreign project with the requested name is not stopped or removed; prepare refuses it |
| 2 | Populated work root | PASS | Root sentinel survives; only the run-owned child is removed |
| 3 | Concurrent runs | PASS | Two runs receive disjoint work roots and independent cleanup |
| 4 | Unsafe bindings | PASS | Wildcard, fixed, DB-published and host-network configurations are rejected; loopback-only config is accepted |
| 5 | Invalid project name | PASS | Unsafe project names fail before Docker mutation |
| 6 | Exact-label cleanup | PASS | Owned container, volume and network are removed; cleanup is idempotent |
| 7 | Stale PID file | PASS | An arbitrary PID file cannot cause an unrelated process to be killed |
| 8 | Artifact redaction | PASS | Secrets, DSNs, JWTs, `source_id` values and `fixture-resource` are redacted |
| 9 | Stale image provenance | PASS | An image with wrong source labels is rejected for reuse |

All nine passed on both daemons. No test-owned container, volume, network or
image remained afterward.

## Full-fixture checks

The full isolated E2E run additionally proves:

- positive proposal and approval control;
- current-policy identity mismatch;
- same-version canonical hash change;
- disabled policy;
- source ownership transfer;
- disabled proposal principal;
- incorrect proposal fingerprint;
- expired proposal;
- corrupted stored policy snapshot;
- request cancellation during a model call, plus an uncancelled positive
  control;
- database outage;
- zero mutation fixture execution;
- zero `MCPMutationDispatchAttempts`;
- no dispatch/uncertain/reconciliation state;
- audit redaction of source/resource arguments.

Final isolated run:
`/home/pzaino/tmp/opencode/wp04c2l-e2e-dind7.log`

Result:
`/tmp/opencode/wp04c2l-e2e-dind7/a5384e2cdbeac8ee8a7ace7388e66ddc/results.json`

60 PASS, 0 FAIL, 0 BLOCKED, 0 SKIP; cleanup PASS.

## Determinism notes

- Project, work, image tags, ports and run labels are generated per run.
- The cancellation slow model delays only the first model round.
- The positive slow-agent control proves the cancellation assertion is not
  vacuous.
- The harness waits for PostgreSQL's init-complete marker and post-restart
  readiness before seeding.
- API dynamic ports are refreshed after each force-recreate.

## Residual limits

- Safety tests use an Alpine helper image; pulling it requires network access.
- The disposable Docker-in-Docker daemon must be privileged.
- A successful safety suite does not prove Docker Engine itself is free of
  unrelated defects; it proves the harness refuses unsafe operations.
