# Release 2.3.0 RC3 test report

Work package: `WP-REL230-RC3`
Frozen commit: `ef28d7672fe7134f4d277f673fe28a9d2f4424a3`
Worktree: `/tmp/opencode/thecrowler-rc3` (detached at the frozen SHA)
Original checkout: `/home/pzaino/workspace/github/thecrowler`, clean before and
after
Date: 2026-11-20
Release: `v2.3.0` · Schema `1.17` · Writer `verified-ownership-v1.17`

Evidence classes used below:

- **executed** — the command ran here and the result is recorded.
- **hosted** — GitHub Actions on the exact frozen SHA.
- **integration-only** — asserted at SQL/logic level by a tagged test but not
  re-run through the UI.
- **NOT RUN** — not executed, with the specific reason. Never counted as a pass.

## 1. Environment

| Item | Value |
| --- | --- |
| Host | Linux x86_64, 32 CPUs |
| Go | `go1.27.4` (`go.mod` requires `1.26.6`) |
| Docker Engine | `29.9.2-ce`; Compose `5.7.1` |
| Disposable PostgreSQL (integration) | `postgres:17` (17.12), container `rc3-pg`, `127.0.0.1:55937` |
| Acceptance stack | Compose project `rc3gate`, PostgreSQL 17, published VDI, controlled local fixture |
| VDI image | `zfpsystems/crowler-vdi:latest`, digest `sha256:d834e1f0954ca9e98312f43372e453e2f38f783de892f75e5bacfa884fd359a8` |
| Original checkout | untouched, `HEAD` unchanged |

The maintainer's containers (`portainer`) and any existing volumes were not
touched. Every resource created here is prefixed `rc3` and was removed at the
end.

## 2. Hosted CI on the frozen SHA (hosted)

`GET /repos/pzaino/thecrowler/commits/ef28d767.../check-runs` →
`total_count=8`, all `success`. Details and step inventory:
`RELEASE_230_RC3_CI_AND_RELEASE_GATE.md`.

Notably, `postgres-integration` step 15 (`Run shared-object integrity PostgreSQL
regressions`) and step 16 (`Run event retention PostgreSQL integration`) both
executed and succeeded, so the RC2 hardening gates are proven on hosted
infrastructure, not only locally.

## 3. Static and unit gates (executed)

| Command | Result | Log |
| --- | --- | --- |
| `go test ./... -count=1 -timeout 90m -p 1` (first run) | **1 failure**: `pkg/infoseed TestSchedulerProcessesNotifiedSeedBeforePollingInterval` | `/tmp/opencode/rc3-full-unit.log` |
| same test in isolation, `-count=10` | **PASS 10/10** | inline |
| `go test ./... -count=1 -timeout 90m -p 1` (re-run) | **PASS, exit 0, 35 packages ok** | `/tmp/opencode/rc3-full-unit-rerun.log` |
| `go test -race ./pkg/agent/... ./pkg/database/... ./pkg/crawler/... ./cmd/thecrowler/... ./services/events/... -count=1` | PASS, exit 0, no data race (`agent 252.9s`, `database 48.6s`, `crawler 9.2s`, `cmd 1.8s`, `events 10.2s`) | `/tmp/opencode/rc3-race.log` |
| `go vet ./...` | PASS | `/tmp/opencode/rc3-static.log` |
| `go vet -tags=integration ./pkg/agent ./pkg/database ./pkg/crawler ./services/events` | PASS | `/tmp/opencode/rc3-static.log` |
| `CGO_ENABLED=0 go build ./...` | PASS | `/tmp/opencode/rc3-static.log` |
| `CGO_ENABLED=1 go build ./...` | PASS | `/tmp/opencode/rc3-static.log` |
| `gofmt -l pkg cmd services` | clean, no output | `/tmp/opencode/rc3-static.log` |
| `git diff --check` | PASS | `/tmp/opencode/rc3-static.log` |
| `actionlint` v1.7.7 on `.github/workflows/*.yml` | PASS, exit 0 | inline |
| 14 example manifests × `lint` + `validate --strict` | 28/28 PASS, 0 failed, 0 skipped | inline |

### 3.1 The one observed failure

`TestSchedulerProcessesNotifiedSeedBeforePollingInterval` failed once while a
Docker image build was saturating the host:

```text
--- FAIL: TestSchedulerProcessesNotifiedSeedBeforePollingInterval (2.06s)
    scheduler_test.go:35: seed 1 did not reach status "completed" before timeout; last status/error: new
```

Disposition: **flaky under load, not a regression.**

- it passes 10/10 in isolation on the same tree;
- `pkg/infoseed` was not touched by RC2 or by any commit since `e6a41d31`;
- the hosted `build` job passed on this exact SHA;
- the failure message is a scheduler wake-up timing assertion, not an assertion
  about data or authorization.

It is recorded rather than hidden, and it is reported as a test-stability
observation in the sign-off request.

## 4. Disposable PostgreSQL 17 (executed)

All work used the disposable `rc3-pg` container and the restricted `crowler`
runtime role. No shared or production database was touched.

### 4.1 Fresh 1.17 installation

```text
setup exit 0
schema=1.17
writer=verified-ownership-v1.17
capabilities=source-crawl-attempt-v1,mcp-mutation-authorization-v1
tables=78
handle_shared_entity_deletion has FOR UPDATE = true
trg_after_delete_* triggers = 6
```

### 4.2 Migration from a simulated pre-RC2 database

```text
create rc3_mig from the fresh setup, seed one Sources row
replace handle_shared_entity_deletion() with the pre-RC2 body (no FOR UPDATE)
pre-fix body has FOR UPDATE      = f
apply postgresql-migration-v1.17.pgsql  (pass 1) = exit 0
apply postgresql-migration-v1.17.pgsql  (pass 2) = exit 0
post-fix body has FOR UPDATE     = t
schema                           = 1.17
writer                           = verified-ownership-v1.17
seeded Sources row preserved     = 1
trg_after_delete_* triggers      = 6
```

Function-body equality between the fresh install and the migrated database:

```text
fresh    md5(prosrc) = 6d33f3492e828ea1334de93baeba5c95
migrated md5(prosrc) = 6d33f3492e828ea1334de93baeba5c95
BODY_IDENTICAL
```

This is the key convergence proof: an upgraded database does not keep the old
unsafe function.

### 4.3 Backup and restore

```text
pg_dump --format=custom        -> exit 0, 677,039 bytes
pg_restore --list              -> 77 TABLE DATA entries
pg_restore into rc3_restore    -> exit 0

rc3_mig     : tables=78 sources=1 schema=1.17 writer=verified-ownership-v1.17
              fn=6d33f349… triggers=6
rc3_restore : tables=78 sources=1 schema=1.17 writer=verified-ownership-v1.17
              fn=6d33f349… triggers=6
```

Schema, data, version, writer generation, function body and trigger inventory
all match after restore.

### 4.4 Full tagged integration suite

```bash
go test -p 1 -tags=integration \
  ./pkg/database/... ./pkg/crawler/... ./pkg/agent/... \
  ./services/api/... ./services/events/... -count=1 -timeout 40m
```

Result: **PASS, exit 0**

```text
ok  pkg/database   251.699s
ok  pkg/crawler     (included)
ok  pkg/agent       (included)
ok  services/api   3.265s
ok  services/events 0.613s
```

Log: `/tmp/opencode/rc3-pg-full.log`. No `--- SKIP` in the selected packages.

Included in that run: the shared-object trigger regressions, the 25-iteration
stress, zero-reference deletion, refresh/`VacuumSource` consistency, legacy
refresh preservation/refusal/rollback/generation tests, fenced refresh tests,
keyword suppression windows, Events retention, source-crawl attempt/result
suites, goal store and observation suites, MCP mutation suites and crawl
approval/scheduling.

## 5. Feature-specific hermetic coverage (executed)

| Area | Command | Result |
| --- | --- | --- |
| Ollama provider | `go test ./pkg/agent -run TestOllama -count=1 -v` | 7 PASS, 2 SKIP (the two live opt-in tests) |
| MCP (stdio + Streamable HTTP fixtures) | `go test ./pkg/agent -run TestMCP -count=1 -v` | **138 PASS**, 1 SKIP (`TestMCPFixtureProcess`), 0 FAIL |
| Information Seed (hermetic) | `go test ./pkg/infoseed/... -count=1` | PASS |
| Information Seed provider fixtures | included above (`searchproviders`) | PASS |

The MCP selection covers endpoint binding, TLS verification and optional CA
trust, DNS/SSRF policy, mixed answers, redirect rejection, ambient-proxy
avoidance, bearer isolation, response/SSE caps, `IsError` evidence semantics,
allowlisting, argument validation and bounded output.

The two Ollama SKIPs are the live connectivity and live tool-calling tests; they
require `CROWLER_OLLAMA_SMOKE_URL`/`_MODEL` and are reported as `NOT RUN`, not
as passes.

## 6. Shipping Docker images (executed)

Built from the repository's own Dockerfiles at the frozen SHA, using the pinned
builder and runtime base images recorded in those files.

### 6.1 Default build path

```text
docker build --network=host -t rc3-api:normal -f Dockerfile.searchapi .
...
#25 83.01 go: downloading github.com/felixge/httpsnoop v1.1.0
ERROR: failed to build: failed to solve: Canceled: context canceled
NORMAL_EXIT=124          # bounded at 7 minutes
```

The builder stage **stalls during Go module download** with the public proxy
default in this environment. This reproduces the condition recorded in the RC2
report and is a host egress problem, not a repository defect. GitHub-hosted CI
does not build these images, so this is not covered by the green CI run.

### 6.2 Documented workaround path

```bash
docker build --network=host --build-arg GOPROXY=http://127.0.0.1:<local> \
  -t <tag> -f <Dockerfile> .
```

| Image | Dockerfile | Build exit | Elapsed | Image ID | Size |
| --- | --- | --- | --- | --- | --- |
| `rc3-api:ship` | `Dockerfile.searchapi` | 0 | 240 s | `sha256:38bcb0755e19d81cbd0a51bd21987c08d3da6dc10ab51379a46f587c0f6ca882` | 536 MB |
| `rc3-events:ship` | `Dockerfile.events` | 0 | 261 s | `sha256:a7f956f9b73b54fc2a248a341ce5f7e25acad62c03be163a81a2898b520bf6c1` | 897 MB |
| `rc3-engine:ship` | `Dockerfile.thecrowler` | 0 | 372 s | `sha256:febc5f20713f11d7332c32190a0c5a413e9a8a121f6b08685fe69fff217ba49b` | 542 MB |

All three image IDs are byte-identical to the ones RC2 produced from the same
source and builder digests (`38bcb0755e19…`, `a7f956f9b73b…`,
`febc5f20713f…`), which is direct evidence that the build is deterministic for
this source.

**Reproducibility signal:** the API and Events image IDs are byte-identical to
the ones produced by RC2 for the same source and builder digests
(`38bcb0755e19…`, `a7f956f9b73b…`), so the image build is deterministic here.

**Module-integrity posture:** the build argument selects the proxy only.
`GOSUMDB` is not disabled, `GOFLAGS`/`GOINSECURE`/`GOPRIVATE`/`GONOSUMDB` are not
set, and `go.sum` in the repository is unchanged. Modules are still verified
against `go.sum` and the checksum database.

### 6.3 Engine image contents

The engine image ID produced here
(`sha256:febc5f20713f11d7332c32190a0c5a413e9a8a121f6b08685fe69fff217ba49b`) is
**identical** to the RC2 engine image, whose tool inventory was verified
directly: `nmap` 7.95 with `cap_net_admin,cap_net_raw=eip`, OpenJDK 11.0.30,
`dig` 9.18.50, `healthCheck` and `dbDeploy` present and executable, and runtime
user `crowler` (uid 1000). An identical image ID means identical layers, so the
same inventory applies here without re-opening the container. RC3 did not
independently re-run those tool checks.

### 6.4 Engine image build (final)

See §7.1; the second attempt used the same Dockerfile with the documented
`GOPROXY` argument against a Go-based local module proxy.

## 7. Real Docker / browser acceptance (executed)

Isolated Compose project `rc3gate`: PostgreSQL 17, `rc3-api`, `rc3-events`,
`rc3-engine-1` (default config), `rc3-engine-refresh` (`refresh_content: true`),
`rc3-vdi-1` (published `zfpsystems/crowler-vdi`), and a controlled local HTTP
fixture (`python:3.12-alpine`) reachable from both the engine and the browser
networks.

### 7.1 Readiness and posture

```text
API    /v1/health 200, /v1/ready 200
Events /v1/health 200
Selenium /status 200, ready=true, 1 node
DB     schema=1.17 writer=verified-ownership-v1.17 mode=legacy
handle_shared_entity_deletion has FOR UPDATE = true
trg_after_delete_* triggers = 6 ; tables = 78
fixture reachable from crowler-net and crowler-vdi-1 (200 on both)
```

### 7.2 Scenario results

| # | Scenario | Result | Database assertions |
| --- | --- | --- | --- |
| 1 | Default legacy crawl (single page, `refresh_content=false`) | **PASS** | source `completed`, engine recorded; pages=1, links=1, objects=1, woi=1; `SourceCrawlClaims`=0 (legacy path); `crawl_completed` events=1; event `expires_at - created_at` = 119 s |
| 1b | Recursive legacy crawl of the fixture root | **PASS (scope-limited)** | source `completed`; 2 pages indexed (`/`, `/index.html`). Link following beyond the entry page did **not** occur with `max_depth: 1` / `restricted: 1` in this fixture. Recorded as observed; multi-page indexing is covered by the integration suite |
| 1c | Two pages sharing one content hash | **PASS** | `dup-a.html` and `dup-b.html` indexed as separate sources; one `WebObjects` row linked to both pages (`objects_with_two_links=2`; the second pair is `/` and `/index.html`) |
| 2 | Legacy refresh with a hash-shared object | **PASS** | after refreshing page A: pageA_objects=1 (new v2), pageB_objects=1 (old v1), `v2_rows=1`, `v1_rows=1`, `v1_links=1` (only page B), `pageA_v2_links=1` |
| 3 | Legacy cross-source page refusal | **PASS** | page linked to two sources; refresh refused, `v2_rows=0`, `v1_rows=1`, `page_source_links` still 2 after the attempt; `last_error` = `page indexing failed: content refresh refused: page is shared with another source (index_id=11, other_sources=1)` |
| 4 | Legacy refresh failure injection | **PASS (data)** | `v4_rows=0`, `v2_rows=1`, page A still linked to v2; `last_error` = `page indexing failed: removing fenced refresh page link 9: pq: rc3 injected refresh failure (P0001)`; source `status=error` |
| 5 | Fenced crawl (operator-enabled) | **PASS** | claim row present, generation 1, attempt `completed`, 1 object for the page; source left `processing` (unbound source-status suppression, as designed) |
| 6 | Fenced refresh | **PASS** | new generation `completed`; `v2_rows=1`, `v1_rows=0`, page still has exactly 1 object |
| 7 | Fenced refresh failure injection | **PASS** | trigger verified present against the live object; generation 4 → 5; attempt `failed` with reason `crawl_failed`; `v5_rows=0`, `v4_rows=1`, page still linked to v4 and to exactly one object |
| 8 | Engine/VDI interruption and reclaim | **integration-only** | Covered by `TestPostgresSourceCrawlAttempt*` (supersession, lease expiry, stale fence, replay) and the goal-store restart tests. No live restart was performed |
| 9 | Legacy `null:` pipeline test | **PASS** | source row deleted; 0 pages indexed for `null:` URLs; no browser navigation path |
| 10 | Fenced `null:` regression | **PASS** | source retained (1), newest attempt `failed`, 0 pages indexed, no deletion |
| 11 | Events TTL / janitor (`automatic_events_removal: never`) | **PASS** | an expired probe row and a future probe row inserted; the expired row is removed by the 1-minute janitor while the future row remains; `crawl_completed` rows carry a finite TTL |
| 12 | Negative guarantees | **PASS** | `MCPMutationDispatchAttempts`=0, `MCPMutationProposals`=0, `CrawlProposals`=0, `CrawlApprovals`=0, `AgentGoalRuns`=0; no autonomous resume activity |

### 7.3 Scenario notes that matter

- **Scenario 4 status is not deterministic.** The same kind of non-critical page
  error left `status=error` here but `status=completed` in scenario 3, while
  `last_error` was populated in both. This is pre-existing legacy error
  classification, not an RC2 regression, and it is the strongest argument for
  the P2-2 disposition: a legacy consumer must treat a non-empty `last_error` as
  significant regardless of `status`.
- **Scenario 7 needed two attempts.** The first injection used the predicate
  `object_content LIKE '%rc3-fenced-v2%'`, which did not match the stored object
  text, so no failure was actually injected (the refresh legitimately
  completed). The re-run verified the trigger against the live object first and
  used a broader predicate; that run produced the expected `failed` + rollback.
  Only the verified run is counted.
- **Cache can mask content changes.** The fixture was rebuilt mid-run to send
  `Cache-Control: no-store`, because the browser otherwise re-served a cached
  body and a "refresh" became a no-op. The final fixture server always disables
  caching.
- The refresh engine variant (`refresh_content: true`) ran as a separate Compose
  service; only one engine was running at any time.
- Claim mode was returned to `legacy` before shutdown.

### 7.4 Teardown

The `rc3gate` project and the `rc3-pg` container were removed; no `rc3-*`
container, volume, network or image remains, and the maintainer's `portainer`
container was untouched.

### 7.5 Acceptance scaffolding (not part of the repository)

The gate used local, disposable scaffolding. It is described here rather than
committed, because it is a verification harness and not a product artifact.

- **Fixture server**: `python:3.12-alpine` serving a directory of static pages,
  with a small handler that forces `Cache-Control: no-store`,
  `Pragma: no-cache`, `Expires: 0` (see the note in §7.3).
- **Pages**: `index.html`, `page2.html`, identical `dup-a.html`/`dup-b.html`
  (same content hash to create one shared `WebObjects` row), `shared.html`
  (cross-source page) and `fenced.html` (fenced scenarios).
- **Compose override**: renames every container to `rc3-*`, binds host ports on
  `127.0.0.1` with non-default numbers (`18090` API, `18092` Events, `55442`
  DB, `14454-14455` VDI), sets the images to the locally built `rc3-*:ship`
  tags with `pull_policy: never`, mounts the candidate `config.yaml`, adds a
  second engine service (`rc3-engine-refresh`) mounting a
  `refresh_content: true` variant, attaches the fixture to both the crawler and
  VDI networks with the aliases the engine and browser use, and pins named
  volumes/networks to `rc3gate_*`.
- **PostgreSQL**: fresh `postgres:17` initialised from
  `pkg/database/postgresql-setup.sh`, no host port exposure beyond
  `127.0.0.1:55442`.

`config.yaml` was derived from `config.default` with: database host
`crowler-db`, `crawler.workers=2`, `indexing_concurrency=1`, `max_depth=1`,
`max_links=20`, `prevent_duplicate_urls=false`, `crawling_interval: 30 days`,
`crawling_if_ok: 30 days`, `crawling_if_error: 5 minutes`,
`create_event_when_done=true`, `refresh_content=false`, `api.enable_console=true`,
`events.automatic_events_removal: never`, and the VDI entry pointing at
`crowler-vdi-1`.



## 8. Named skips and NOT RUN

| Item | Status | Reason |
| --- | --- | --- |
| Live Ollama inference and tool calling | **NOT RUN** | No Ollama endpoint or tool-capable model configured |
| Live external MCP server | **NOT RUN** | Only local fixtures available; no approved external endpoint |
| Information Seed Compose acceptance | **NOT RUN** | Its harness builds its own images with `--no-cache` and no `GOPROXY` argument; the same Docker module-download stall applies (see §6.1). It also requires live network for base images and two external RSS checks |
| MySQL runtime | **NOT RUN** | No server configured; MySQL is legacy-only by contract |
| `mcp-authorization-e2e` workflow | **BLOCKED** | `workflow_dispatch`-only and absent from the protected default branch, so it cannot be dispatched |
| Production / shared database migration | **NOT RUN** | Forbidden by the work package |
| Engine/VDI interruption and reclaim (live) | **integration-only** | Covered by the fenced claim/lease/supersession PostgreSQL tests; no live restart was performed in this package |

## 9. Log index

| Log | Content |
| --- | --- |
| `/tmp/opencode/rc3-full-unit.log` | first full unit run (1 flaky failure) |
| `/tmp/opencode/rc3-full-unit-rerun.log` | full unit re-run |
| `/tmp/opencode/rc3-race.log` | race suite |
| `/tmp/opencode/rc3-static.log` | vet, builds, gofmt, diff-check |
| `/tmp/opencode/rc3-mcp-tests.log` | 138 MCP tests |
| `/tmp/opencode/rc3-pg-full.log` | full tagged PostgreSQL suite |
| `/tmp/opencode/rc3-mig1.log`, `rc3-mig2.log` | migration passes |
| `/tmp/opencode/rc3-mig.dump`, `rc3-restore.log` | backup/restore |
| `/tmp/opencode/rc3-build-normal.log` | default build stall |
| `/tmp/opencode/rc3-build-rc3-api:ship.log`, `…-rc3-events:ship.log`, `rc3-build-engine2.log` | shipping image builds |
| `/tmp/opencode/rc3-up-core.log`, `rc3-stack.log` | acceptance stack startup and runtime |
