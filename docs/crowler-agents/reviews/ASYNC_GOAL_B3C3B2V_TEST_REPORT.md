# WP-05B3C3B2V test report

## 1. Environment

| Item | Value |
| --- | --- |
| Repository | `pzaino/thecrowler` |
| Branch | `develop` |
| Reviewed commit | `f0f4fc97da6e2e5975b4cf26e5b8b4610324d4e2` |
| Release | `v2.3.0` |
| Schema | `1.17` |
| Writer generation | `verified-ownership-v1.17` |
| Go | `go1.27.3 linux/amd64` |
| Host | Linux, Docker 29.7.2-ce, Compose 5.6.1 |
| Disposable PostgreSQL | `postgres:17` (PostgreSQL 17.12), host port 55504 and Compose port 15432 |
| Runtime role (integration) | `crowler`; administrative role `postgres` |
| Isolated Compose project | `b2v-e2e` |
| Fixture | `python:3.12-slim` serving `b2v-fixture` / `b2v-fixture-alt` |
| VDI | `zfpsystems/crowler-vdi:latest` (Chromium 132) |

The maintainer's existing `crowler-*` containers, volumes and database were not
used. The isolated project uses its own volumes (`b2v-e2e_*`), names
(`b2v-e2e-*`) and host ports.

Hosted CI at the reviewed commit: 8/8 success, including
`postgres-integration`
(https://github.com/pzaino/thecrowler/actions/runs/44305245883/job/134804995471).
When this report was written the patch had not been pushed. Post-publication
status: the B2V change set is commit
`6a682ea6091fdb0a43b76e57616a2bc8a2c2edaa` on `develop`, and hosted CI on that
commit is 8/8 successful.

## 2. Code review and fixes

The full findings are in `ASYNC_GOAL_B3C3B2V_INDEPENDENT_REVIEW.md`. Five
correctness defects were demonstrated and fixed, all within schema 1.17:

| ID | Severity | Defect | Fix |
| --- | --- | --- | --- |
| B2V-01 | Blocker | Unbound owner continuity based on `SourceOwnership.last_updated_at` is unsound (transaction-start `CURRENT_TIMESTAMP`); a transfer beginning before the claim and committing after it could authorize an old worker. | Unbound source-status mutation suppressed; lease-expiry reclaim retained. |
| B2V-02 | High | Quarantine set `VDIReturned` and `closeSession` cleared shared maps/status while a worker could still run; `CrawlInitialURL` writes `visitedLinks` unconditionally. | Quarantine only suppresses the finalizer release and cleanup; worker's later return recovers the slot. |
| B2V-03 | Medium | A failed network-information *collection* was not reflected in the terminal outcome. | Network-only branch treats collector state 3 as failed. |
| B2V-04 | High | The claim binder accepted only `Status='accepted'`, so a scheduled `processing` operation was never bound or terminated. | Binder accepts `accepted` and `processing`; `already_pending` remains unbound. |
| B2V-05 | High | Fenced page-indexing failures were logged but did not fail the crawl (only legacy `errCriticalError` did), so a crawl with no indexed page could report `completed`. | `errIndexingFailure` sentinel; fenced worker drain and initial page fail the crawl. |

No schema, migration, capability, default-mode, public API or configuration
change was made.

## 3. Local and integration checks (final patched tree)

All commands were executed from `/home/pzaino/workspace/github/thecrowler`.

| Command | Result | Log |
| --- | --- | --- |
| `go test ./... -count=1 -timeout 90m -p 1` | PASS | `b2v-full-final.log` |
| `go test -race ./pkg/database/... ./pkg/crawler/... ./cmd/thecrowler/... -count=1` | PASS, no race | `b2v-race-final.log` |
| `go vet ./...` | PASS | `b2v-vet-final.log` |
| `go vet -tags=integration ./pkg/database ./pkg/agent` | PASS | `b2v-veti-final.log` |
| `CGO_ENABLED=0 go build ./...` | PASS | local |
| `CGO_ENABLED=1 go build ./...` | PASS | local |
| `gofmt -l pkg cmd services` | PASS (no output) | local |
| `git diff --check` | PASS | local |
| `actionlint .github/workflows/*.yml` | PASS | `b2v-actionlint-final.log` |

Full suite used `TMPDIR=/dev/shm/opencode`.

### 3.1 Disposable PostgreSQL 17

Database initialized from `postgresql-setup.pgsql`; application operations used
the restricted `crowler` role.

```text
go test -tags=integration ./pkg/database \
  -run '^TestPostgresSourceCrawl(Attempt|Result|B2R)' -count=1 -v
```

Result: **36 PASS, 0 FAIL, 0 SKIP** (`b2v-pg-all-final.log`).

New/expanded B2R/B2V coverage:

- `TestPostgresSourceCrawlB2RFenceBoundary`
- `TestPostgresSourceCrawlB2RValidateTxSupersession`
- `TestPostgresSourceCrawlB2RUnboundOwnershipTransfer`
- `TestPostgresSourceCrawlB2RUnboundSuppressionThenTransfer`
- `TestPostgresSourceCrawlB2RProvenanceTimestampIsNotContinuity` — proves the
  old continuity rule was unsound and that the mutation is suppressed.
- `TestPostgresSourceCrawlB2RClaimBindsProcessingOperation` — accepted and
  processing operations bind and terminate.

The real scheduler/approval/claim/observation test also passed:

```text
go test -tags=integration ./pkg/agent \
  -run '^TestPostgresFullProposalApprovalSchedulerClaimAndObservation$' -count=1
```

Result: PASS (`b2v-pg-handoff-final.log`).

### 3.2 SQLite and unit coverage

- `TestSourceCrawlB2RUnboundSourceStatusSQLite` — suppression, transfer,
  changed status, disabled/deleted source, lease-expiry reclaim.
- `TestSourceCrawlB2RClaimBindsProcessingOperationSQLite`.
- `TestSourceCrawlB2RFenceValidationSQLite` — active / ±expiry / superseded /
  disabled / deleted / legacy / transaction-held validation.
- `TestSourceCrawlB2ROperationStateCompatibilitySQLite`.
- `TestSourceCrawlB2RLateWorkerCannotReplaceTerminalSQLite`.
- `TestQuarantineSession`, `TestCloseSessionDoesNotReturnQuarantinedVDI` —
  release channel empty, `VDIReturned` unchanged, shared maps preserved, late
  map write does not panic.
- `TestNetworkInfoCollectionFailed`, `TestNetworkOnlyCrawlResult`,
  `TestDecideCrawlTerminalOutcome`.
- `TestFencedRefreshContentIsRejected`.

## 4. Docker build and stack startup

The repository's `scripts/generate-docker-compose.sh -e=1 -v=1 --postgres=yes
--prometheus=no --no_jaeger` generated the Compose file in a disposable clone.
The isolated deployment used:

- `crowler-db` → `postgres:17` (overridden from the generator's 15.10 default
  for this disposable test only), host `127.0.0.1:15432`
- `crowler-api` → image `sha256:d5ec09bc…`, host `127.0.0.1:18080`
- `crowler-events` → image `sha256:f9d9c175…`, host `127.0.0.1:18082`
- `crowler-engine-1` → image `sha256:ea083049…`
- `crowler-vdi-1` → `zfpsystems/crowler-vdi:latest`
  (`sha256:1e261c34…`), host `127.0.0.1:14444-14445`
- `b2v-fixture` → `python:3.12-slim`, internal DNS `b2v-fixture` and
  `b2v-fixture-alt`, attached to the engine and VDI networks

Builds:

```text
docker compose -p b2v-e2e build crowler-api crowler-events crowler-engine-1
```

PASS (`b2v-build.log`, `b2v-build2.log`, `b2v-build3.log`; the final rebuild
includes every code fix).

Startup and readiness:

- `docker compose -p b2v-e2e up -d` — all six services running
- `GET /v1/health` and `/v1/ready` → 200 / `{"status":"READY"}`
- Events `/v1/health` → `{"status":"OK"}`
- Selenium `/status` → ready, one node
- DB: schema `1.17`, writer `verified-ownership-v1.17`,
  `source-crawl-attempt-v1` capability present
- default mode `legacy` verified before any test changed it
- engine loaded config, connected to PostgreSQL, initialized the VDI pool and
  entered its claim loop

## 5. Live crawler scenarios

Fixtures: deterministic HTTP pages with stable content, `/page2`, `/changing`,
`/delay/N` and `/slow`. All crawls used the real engine, PostgreSQL 17, the
real VDI browser and the real API/Events services. Sources were isolated by
unique URLs and, where a scenario required it, by disposable SQL fixtures.

| ID | Scenario | Result | Evidence |
| --- | --- | --- | --- |
| PREPARE | Health, readiness, schema, writer, capability, default `legacy`, service inventory | PASS | 13 assertions; DB 1.17 / `verified-ownership-v1.17`; mode `legacy` |
| LIVE-01 | Legacy crawl of the fixture | PASS | source 2 `completed`, no fenced attempt, indexed fixture page |
| LIVE-02 | Fenced crawl through real VDI | PASS | attempt `completed`, indexed `/`, `/page2`, `/changing`; source `processing` (unbound suppression); current claim present |
| LIVE-03-ENABLED | Fenced crawl with `create_event_when_done: true` | PASS | one `crawl_completed` event |
| LIVE-03-DISABLED | Same with the flag false (engine restart with mounted config) | PASS | no `crawl_completed` event |
| LIVE-04 | Network-only source, success and forced `NetInfo` write failure | PASS | success attempt `completed` and NetInfo rows grew 4→5; failed attempt `failed` with no page after `netinfo` was renamed; table restored |
| LIVE-05 | Crawl timeout and VDI cleanup | PASS | attempt `cancelled`, no indexed page, engine logged `Crawling timed out` and `quarantining VDI session`; stack restarted afterward |
| LIVE-06 | Queued claim past lease expiry | **BLOCKED** | Not reproducible through the engine's supported scheduling: `retrieveAvailableSources` claims only when a pipeline slot is free, so a claim never waits behind a VDI. Manual lease mutation after validation correctly produced `conflict (receipt_identity_mismatch)`, not a stale admission. Deterministic coverage: `TestSourceCrawlB2RFenceValidationSQLite`, `TestPostgresSourceCrawlB2RFenceBoundary`, `TestPostgresSourceCrawlB2RValidateTxSupersession`. |
| LIVE-07 | Disable source during an active fenced crawl | PASS | attempt stayed `claimed` (terminal `stale_fence: source_disabled`), `Error indexing page: source crawl fence is not current: source_disabled`, zero indexed pages |
| LIVE-08 | Bound operation from `accepted` and from `processing` | PASS | both attempts `completed`, operation IDs match, `Status` `accepted→completed` and `processing→completed`, source `completed` |
| LIVE-09 | Unbound crawl with ownership transfer mid-crawl | PASS | attempt `completed`, new owner retained (`usr_id=8`), source status not overwritten (`processing`) |
| LIVE-10-NULL | Fenced `null:` quarantine vs legacy deletion | PASS | fenced source retained with attempt `failed`; legacy source deleted |
| LIVE-10-REFRESH | Fenced `refresh_content` refusal | PASS | attempt `failed`, zero indexed pages, `refresh_content is not supported` logged |
| LIVE-11 | Two fenced sources claimed together | PASS | distinct attempts, both `completed`, exactly one attempt per source |
| LIVE-12 | Engine/VDI restart during a crawl | PASS | interrupted attempt remained `claimed` and never completed; expired lease reclaimed as generation 2; old attempt `superseded` |

Structured evidence:
`/tmp/opencode/b2v-e2e/results.ndjson`. Raw engine/API/Events/DB logs:
`/tmp/opencode/b2v-e2e/logs/`.

### 5.1 Iteration failures and their cause

The following live attempts failed while the harness or the product was being
corrected. They are recorded so the PASS results are not mistaken for
first-attempt success:

- LIVE-01 initially failed with `ERR_CONNECTION_REFUSED`: the fixture listened
  on 8080 while the URL used port 80. Fixture fixed; not a product defect.
- LIVE-02 initially indexed only the root page: the source config had
  `max_depth: 1` and `restricted: 1`, so the crawler skipped `/page2` as
  external. Harness config corrected.
- LIVE-04 initially failed on a missing `source_uid` and on quoted
  `"NetInfo"` naming; corrected fixture SQL.
- LIVE-07 initially raced the log assertion; the harness now waits for the
  fence log. The product behavior was correct.
- LIVE-08 initially failed because the source was claimed before its proposal
  fixture was inserted, and then because the binder rejected `processing`
  (B2V-04, fixed).
- LIVE-10-REFRESH initially failed because the engine restart raced the source
  claim, then reproduced B2V-05 (false completion). Both fixed.

## 6. Shutdown and isolation

- Only the `b2v-e2e` Compose project was created.
- The maintainer's `crowler-*` containers and volumes were never started,
  stopped, removed or migrated.
- The isolated database remains disposable; no production or shared database
  was touched.
- The isolated stack was left running for artifact collection and will be shut
  down with `docker compose -p b2v-e2e down` (without `-v`) unless the
  maintainer asks to retain it. Named volumes are retained by that command.

## 7. Not run

- Hosted CI for the patched tree (not pushed).
- A live two-VDI concurrency matrix; the configured environment has one VDI.
  LIVE-11 exercised two sources with the supported single-VDI topology.
- MySQL fenced mode (legacy-only by contract).
- Live email, plugin, agent, MCP or Ollama execution (unchanged by this work).
- Production or shared database migration/activation.

## 8. Evidence classification

Everything in sections 3–5 was executed locally on the patched working tree.
The reviewed commit's hosted checks are baseline evidence, not this patch.
LIVE-06 is explicitly **BLOCKED**, not passed. The fenced path is a candidate
for controlled integration testing after independent review; it is not
production-ready.
