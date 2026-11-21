# InformationSeed Docker Compose acceptance harness

This directory contains the reproducible, isolated acceptance harness for the
InformationSeed lifecycle. It builds the current checkout's real engine, API and
events images, starts a disposable PostgreSQL database plus a deterministic HTTP
fixture, drives the public API and asserts live database state. It does not
replace unit tests and it never uses a published CROWler image.

## What it validates

The harness executes IT-01 through IT-16 from the release task:

- fresh PostgreSQL bootstrap, schema 1.17, the eight-argument claim function and
  data-preserving database restart;
- authentication and owner/admin authorization on the real HTTP routes;
- link-only discovery, new-source creation, 64-character source UIDs, verified
  ownership and idempotent reruns;
- source-config refresh policy, ownership guards and processing-state guards;
- RSS/Atom parsing, snippet preservation and malformed-feed classification;
- rate limiting outside the HTTP request timeout, including 60s/10s and a
  cancellation while queued;
- GDELT DOC parameter translation, pagination, empty and malformed responses;
- retry backoff, permanent failures, attempt exhaustion, fairness and partial
  provider results;
- lease heartbeats, a second engine, SIGTERM release and SIGKILL recovery with
  stale-token fencing;
- deterministic candidate caps across five fresh seeds;
- retention dry-run/deletion with live-lease protection and orphan checks;
- a non-mutating `EXPLAIN (ANALYZE, BUFFERS)` plan on a synthetic database;
- operator status/list/sources/candidates/events/diagnostics surfaces and log
  checks;
- API/events restart and a real 1.16-to-1.17 migration fixture built from
  repository history.

A bounded external RSS smoke check for Pitchfork and Hospitality Net runs after
the deterministic suite. It is observational only: external publisher or egress
failures do not turn a deterministic CROWler pass into a failure, and a
third-party outage is reported separately.

## Prerequisites

- Linux x86_64 with Docker Engine 29.x and Docker Compose 5.x (the versions used
  for the accepted run were Docker 29.7.2-ce and Compose 5.6.1).
- At least 4 CPUs, 8 GiB RAM and roughly 10 GiB of free Docker disk. The default
  test stack intentionally uses zero VDIs.
- Python 3.10+ with PyYAML.
- Go 1.27 available on the host for the local password-hash helper.
- Network access for the Docker base images and the two external RSS feeds.
  Deterministic scenarios never depend on external DNS or publisher uptime.

## Safety and isolation

`run.py` performs a read-only preflight inventory of containers, images, volumes
and networks before creating anything. It writes an allowlist of the exact
resource identifiers it will own. Every run gets a unique lowercase project and
resource prefix:

```text
infoseed_it_<UTC timestamp>_<random suffix>
```

The generated Compose file is rewritten so that:

- every container name and image is unique to the run;
- PostgreSQL and events have no host port;
- only the API and the fixture are published, and only on `127.0.0.1` with
  dynamically chosen high ports;
- the probe shares the disposable database container's network namespace and
  needs no host networking;
- restart policies cannot hide the deliberate SIGTERM/SIGKILL tests.

The harness only uses `docker compose -p <run-id> down --volumes` and removes
images whose repository names begin with that run ID. It never runs global
Docker prune, volume prune, `docker rm`, or an unidentified `compose down`.

## Running

From the repository root:

```bash
./tests/integration/infoseed-compose/run.sh
```

Useful environment variables:

| Variable | Meaning |
| --- | --- |
| `INFOSEED_ARTIFACT_ROOT` | Directory for timestamped artifacts. Defaults to `<repo>/artifacts`. |
| `INFOSEED_KEEP=1` | Preserve the disposable stack for inspection; no cleanup is performed. |
| `INFOSEED_RUN_ID` | Override the unique run identifier (must be lowercase and unique). |
| `INFOSEED_IMAGE_SUFFIX` | Override the local image tag suffix. |
| `INFOSEED_PREVIOUS_SCHEMA_COMMIT` | Commit whose setup SQL is the previous-schema migration fixture. Defaults to `2eeadd67`. |

The harness is noninteractive and timeout-bounded. The accepted run took about
14 minutes after the source images were built. A no-cache source build adds
another 10 to 20 minutes depending on image-pull and module-download speed.

## Artifacts

Each run writes a timestamped directory containing:

- `REPORT.md` and `results.json` with the explicit PASS/FAIL/BLOCKED conclusion;
- the sanitized resolved Compose file, run allowlist and pre/post Docker
  inventory;
- image IDs, container health, HTTP transcript, SQL snapshots, `EXPLAIN` plans,
  retention and migration evidence, timestamped service logs and external smoke
  results.

Passwords, bearer tokens, HMAC secrets and declared provider credentials are
redacted while writing. The harness also redacts JWT-shaped strings and
sensitive JSON field names defensively. The plaintext test passwords and tokens
exist only in the disposable process/container environment.

## Interpreting results

- Any `FAIL` in IT-01 through IT-16 is release-blocking.
- `BLOCKED` means a required check could not be completed safely (for example,
  the previous-schema commit is unavailable). Do not report a blocked migration
  as passing upgrade readiness.
- The external smoke result is operational information and is not part of the
  deterministic release gate.
- A green unit-test run is a prerequisite, not a substitute for this harness.

## CI

Run this harness only on a dedicated, disposable Docker host or a runner whose
Docker daemon is not shared with unrelated jobs. The job is opt-in and should
upload the timestamped artifact directory. Do not run the destructive lifecycle
stages on an untrusted shared runner. A minimal CI step looks like:

```bash
INFOSEED_ARTIFACT_ROOT="$PWD/artifacts" \
  ./tests/integration/infoseed-compose/run.sh
```

The job must fail when `results.json` has a non-PASS conclusion. It should not
be labelled as a unit test.

## Layout

- `run.sh` - operator entrypoint.
- `run.py` - orchestration, isolation, scenarios, assertions and reporting.
- `fixture-server/` - deterministic RSS, Atom, JSON, GDELT, error and slow
  endpoints with request traces.
- `probe/` - real database helper for retention and stale-token fencing checks.
- `hashpw/` - local password-hash helper used only to bootstrap test users.
- `config.example.yaml` - sanitized shape of the test-only configuration. The
  harness generates the real secrets and provider URLs at runtime; the example
  is not used directly.
