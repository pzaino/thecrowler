# WP-04C2L E2E Isolation

## Run identity

Every run creates:

- a 128-bit hex `RUN_ID` from `os.urandom`;
- a Compose project `crowler-e2e-<12 hex>` unless
  `CROWLER_E2E_PROJECT` is supplied;
- a unique work directory under `TMPDIR` or under a unique child of
  `CROWLER_E2E_WORK`;
- a unique artifact directory outside the work directory;
- run-scoped image tags `${PROJECT}-api:${RUN_ID}`,
  `${PROJECT}-events:${RUN_ID}`, `${PROJECT}-model:${RUN_ID}`.

Every Docker resource carries:

```
org.crowler.e2e.run=<RUN_ID>
org.crowler.e2e.project=<PROJECT>
org.crowler.e2e.source.sha=<HEAD>
org.crowler.e2e.source.tree=<source digest>
org.crowler.e2e.role=<resource role>
```

The implementation is `tests/integration/mcp-authorization/e2e_support.py`
and the shell driver is `tests/integration/mcp-authorization/run.sh`.

## Collision preflight

`e2e_support.py prepare` performs a read-only inventory before creating any
Docker resource:

- validates the project grammar;
- checks exact container, volume and network names;
- checks `com.docker.compose.project=<PROJECT>` labels;
- checks `org.crowler.e2e.run=<RUN_ID>` labels;
- refuses existing image tags for builds;
- rejects fixed `CROWLER_E2E_API_PORT`, `CROWLER_E2E_EVENTS_PORT`,
  `CROWLER_E2E_DB_PORT` and `CROWLER_E2E_MODEL_PORT`;
- rejects `/`, the home directory, repository paths and symlinked work roots.

A collision produces BLOCKED. It never runs `down`, `rm`, `prune` or a
fallback deletion.

## Work root

- `CROWLER_E2E_WORK` is a root, never the work directory itself.
- The harness creates `<root>/crowler-e2e-<RUN_ID>` with mode `0700`.
- The directory contains `.crowler-e2e-owner` with the exact run ID.
- Cleanup removes the work directory only when that marker matches.
- A populated root survives; only the run-owned child is removed.

## Ports and network

- PostgreSQL publishes **no** host port. All SQL uses `docker exec`.
- API and Events publish `127.0.0.1::8080` and `127.0.0.1::8082`; Docker assigns
  dynamic ephemeral host ports.
- The harness queries `docker compose port` and refreshes the API URL after each
  force-recreate.
- Fake models are private Compose services on `crowler-net`; they expose only
  container port `18095`.
- `docker compose config --format json` is validated before startup:
  - DB has no published port;
  - API/Events have exactly one `127.0.0.1` binding with no fixed published
    port;
  - model services publish no ports;
  - no service uses `network_mode: host`;
  - every service/network carries the run label.
- Runtime inspection verifies `HostConfig.PortBindings` is loopback-only.

## Models

- `crowler-e2e-model` and `crowler-e2e-slow-model` run from the same run-scoped
  image.
- Events reaches them by Compose service DNS:
  `http://crowler-e2e-model:18095` and
  `http://crowler-e2e-slow-model:18095`.
- No `host.docker.internal`, host port or `0.0.0.0` listener is used.
- Model health is checked before API/Events startup.
- The slow model delays only the first model round, keeping the cancellation
  window deterministic.

## Cleanup

The harness never runs `docker compose down -v --remove-orphans`.

Cleanup queries resources by the exact run label, verifies the label again on
each resource, and removes only exact IDs/names:

1. containers;
2. volumes;
3. networks;
4. run-owned images discovered by label, plus any recorded image IDs;
5. the work child after its owner marker matches.

A resource matching an expected name but lacking the run label is preserved and
reported as a cleanup failure. A work directory that exists without the matching
owner marker is a cleanup failure, not a success. An already-absent image is
treated as clean for idempotent cleanup. A cleanup failure keeps the work
directory and returns nonzero. No broad fallback deletion exists. Generated
`cleanup.sh` commands are shell-quoted.

`CROWLER_E2E_KEEP=1` preserves run-owned resources, prints an operator cleanup
command and writes `artifacts/cleanup.sh`.

## Signals and partial setup

- `trap on_exit EXIT INT TERM` is guarded by an idempotent `CLEANED` flag.
- Cleanup collects Docker resources by label even if setup failed part-way.
- Container logs are collected and sanitized before removal.
- Artifacts live outside the work directory, so successful cleanup does not
  destroy evidence.

## Artifacts

Each run writes under a unique artifact directory:

- `preflight.json`;
- `images.json` and `meta/*-image.json`;
- `meta/runtime.json`;
- `results.ndjson` and `results.json`;
- `REPORT.md`;
- `cleanup.json` and, when kept, `cleanup.sh`;
- sanitized `logs/*.log`;
- safety-test artifacts when the safety suite is run.

Passwords, JWTs, DSNs, bearer tokens and raw fixture arguments are redacted by
`e2e_support.py sanitize`.

## Verified isolation evidence

- Preflight-only run:
  `/home/pzaino/tmp/opencode/wp04c2l-preflight3.log`
- Internal DB/model startup and exact cleanup:
  `/home/pzaino/tmp/opencode/wp04c2l-partial-models2.log`
- Full fixture on disposable Docker-in-Docker:
  `/home/pzaino/tmp/opencode/wp04c2l-e2e-dind7.log`
- Structured result:
  `/tmp/opencode/wp04c2l-e2e-dind7/a5384e2cdbeac8ee8a7ace7388e66ddc/results.json`

The final isolated run reported 60 PASS, 0 FAIL, 0 BLOCKED, 0 SKIP and
`cleanup.status=PASS`. No `crowler-e2e-*` container, volume, network, image or
work directory remained.
