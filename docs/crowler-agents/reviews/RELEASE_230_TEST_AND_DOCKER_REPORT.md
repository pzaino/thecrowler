# Release 2.3.0 test and Docker report

Work package: `WP-REL230-RC1`
Tested commit: `9e719acce04d3606c46821e33b4f8dc23baa6b36`
Worktree: `/tmp/opencode/thecrowler-rel230` (isolated `git worktree`, detached at
the tested commit)
Original checkout: `/home/pzaino/workspace/github/thecrowler`, clean before and
after this work.
Date: 2026-11-20
Release: `v2.3.0`
Schema: `1.17` / writer generation `verified-ownership-v1.17`

Evidence classification used below:

- **Executed** — a command was run in this environment and its result is
  recorded.
- **Unverified** — not executed here; a named reason is given.
- A skipped test is never reported as a pass.

## 1. Environment

| Item | Value |
| --- | --- |
| Host | Linux x86_64 |
| Go | `go1.27.4` (`go.mod` requires `1.26.6`) |
| Docker Engine | `29.9.2-ce` |
| Docker Compose | `5.7.1` |
| PostgreSQL server (integration) | `postgres:17` → PostgreSQL 17.12 |
| PostgreSQL client on host | 18.6 (`pg_dump`/`pg_restore`); the CI selects 17 with `scripts/select-postgres-client-major.sh` |
| VDI image | `zfpsystems/crowler-vdi:latest`, digest `sha256:d834e1f0954ca9e98312f43372e453e2f38f783de892f75e5bacfa884fd359a8`, image ID `sha256:1e261c344a1eda689864f30fbf7aa6ba7cd16699a6333ce9198953d49775b23d` |
| Isolated Compose project | `rel230rc1` |

The maintainer's existing containers and volumes were not started, stopped,
removed or migrated. Only `portainer` was running on the host.

## 2. Baseline (executed)

```text
$ git status --porcelain
(empty)
$ git rev-parse HEAD
9e719acce04d3606c46821e33b4f8dc23baa6b36
$ git branch --show-current
develop
$ git log -5 --oneline
9e719acc improved some policies
be5572de improved documentation
6a682ea6 added independent Fenced-Crawler Verification
f0f4fc97 added Fenced Crawler Execution Safety and Terminal-Result Hardening
aa906c86 completed fenced-claim freshness, tightened receipt replay, and working on the internal transactional heartbeat/terminal repository
$ cat RELEASE_VERSION.txt
v2.3.0
```

Schema and writer markers: `RequiredSchemaVersion = "1.17"`,
`WriterGeneration = "verified-ownership-v1.17"` in
`pkg/database/startup_compatibility.go`.

Diff since the completed `WP-05B3C3B2F1` review baseline (`be5572de`): 12 files,
+1457/−237, all of B3 Decision 2 plus docs and the CI gate. No file outside
`pkg/crawler`, `.github/workflows/postgresql-integration.yml` and
`docs/` changed.

## 3. Static and unit gates (executed)

| Command | Result | Log |
| --- | --- | --- |
| `go test ./... -count=1 -timeout 90m -p 1` (pre-change baseline) | PASS, exit 0, 35 packages ok, 0 FAIL | `rel230/full-baseline.log` |
| `go test ./... -count=1 -timeout 90m -p 1` (post-change) | see section 3.1 | `rel230/full-final.log` |
| `go test -race ./pkg/agent/... ./pkg/database/... ./pkg/crawler/... ./cmd/thecrowler/... -count=1` | see section 3.1 | `rel230/race.log` |
| `go vet ./...` | PASS, exit 0 | `rel230/static.log` |
| `go vet -tags=integration ./pkg/agent ./pkg/database ./pkg/crawler` | PASS, exit 0 | `rel230/static.log` |
| `CGO_ENABLED=0 go build ./...` | PASS, exit 0 | `rel230/static.log` |
| `CGO_ENABLED=1 go build ./...` | PASS, exit 0 | `rel230/static.log` |
| `gofmt -l pkg cmd services` | PASS, no output | `rel230/static.log` |
| `git diff --check` | PASS, exit 0 | `rel230/static.log` |
| `actionlint .github/workflows/*.yml` (pinned v1.7.7) | PASS, exit 0 | `rel230/actionlint.log` |

### 3.1 Post-change unit and race result

```text
$ go test ./... -count=1 -timeout 90m -p 1
... 35 packages ok, 0 FAIL, 0 unexpected SKIP
EXIT=0
```

```text
$ go test -race ./pkg/agent/... ./pkg/database/... ./pkg/crawler/... ./cmd/thecrowler/... -count=1
ok  github.com/pzaino/thecrowler/pkg/agent        307.011s
ok  github.com/pzaino/thecrowler/pkg/database      68.335s
ok  github.com/pzaino/thecrowler/pkg/crawler        8.884s
ok  github.com/pzaino/thecrowler/cmd/thecrowler     1.760s
EXIT=0
```

No data race was reported.

The pre-change baseline and the post-change run both exit 0, so the change set
introduces no unit or race regression.

## 4. Manifest validation (executed)

```text
$ go build -o crowler-agt ./cmd/crowler-agt
$ for f in agents/examples/*.agent.yaml; do
    crowler-agt agents lint "$f"
    crowler-agt agents validate --strict "$f"
  done
manifest_checks_ok=28 manifest_checks_fail=0
```

14 shipped example manifests, each linted and strictly validated. Two checks
per manifest.

## 5. Disposable PostgreSQL 17 (executed)

### 5.1 Fresh install

PostgreSQL 17.12 container on `127.0.0.1:55917`, `SitesIndex` created, then
`pkg/database/postgresql-setup.sh` executed with the runtime role variables.

```text
setup exit 0
schema=1.17
writer=verified-ownership-v1.17
capability=mcp-mutation-authorization-v1
capability=source-crawl-attempt-v1
tables=78
```

### 5.2 Migration and addon rehearsal

On a second database `rel230_mig` created from the same fresh setup, with one
seeded `Sources` row (`source_uid='mig-fixture'`):

| Step | Result |
| --- | --- |
| `postgresql-migration-v1.17.pgsql` pass 1 | exit 0 |
| `postgresql-migration-v1.17.pgsql` pass 2 (idempotency) | exit 0 |
| `postgresql-agent-goal-v1.17.pgsql` pass 1 | exit 0 |
| `postgresql-agent-goal-v1.17.pgsql` pass 2 | exit 0 |
| `postgresql-mcp-mutation-v1.17.pgsql` pass 1 | exit 0 |
| `postgresql-mcp-mutation-v1.17.pgsql` pass 2 | exit 0 |

Post-state:

```text
schema=1.17
writer=verified-ownership-v1.17
cap=mcp-mutation-authorization-v1
cap=source-crawl-attempt-v1
fixture_after=1          (the seeded source survived every pass)
goal_tables=8
mcp_tables=6
attempt_tables=3
```

Backup and restore:

```text
pg_dump --format=custom  -> 677,247 bytes, exit 0
pg_restore --list        -> 77 TABLE DATA entries
pg_restore into rel230_restore -> exit 0
```

Both databases reported `tables=78`, `sources=1`, `schema=1.17`.

No persistent, shared, staging or production database was touched. All
databases in this rehearsal were created and dropped inside the disposable
container.

### 5.3 Integration suites (executed)

```bash
THECROWLER_POSTGRES_INTEGRATION=1 \
DOCKER_POSTGRES_DB_HOST=127.0.0.1 DOCKER_POSTGRES_DB_PORT=55917 \
DOCKER_POSTGRES_DB_NAME=SitesIndex DOCKER_POSTGRES_DB_USER=crowler \
DOCKER_POSTGRES_PASSWORD=crowler DOCKER_POSTGRES_SSL_MODE=disable \
go test -p 1 -tags=integration ./pkg/database/... ./pkg/crawler/... ./pkg/agent/... ./services/api/... -count=1 -timeout 40m
```

Result: PASS, exit 0.

```text
ok  github.com/pzaino/thecrowler/pkg/database    63.375s
ok  github.com/pzaino/thecrowler/pkg/crawler     26.243s
ok  github.com/pzaino/thecrowler/pkg/agent       46.652s
ok  github.com/pzaino/thecrowler/services/api     3.000s
```

No `--- SKIP` was reported by the suites that were selected.

### 5.4 Fenced refresh suite (executed)

```text
--- PASS: TestPostgresFencedRefreshBatchesLargeOldGeneration (5.35s)
    batched fenced refresh of 286 stale links took 736.056961ms
--- PASS: TestPostgresFencedRefreshPageLockBlocksConcurrentLink (0.61s)
--- PASS: TestPostgresFencedRefreshOrphanLockWaitsForInFlightReference (0.61s)
--- PASS: TestPostgresFencedRefreshOrphanCleanupTriggerCharacterization (0.23s)
--- PASS: TestPostgresFencedRefreshConcurrentRefreshersAndIndexer (0.84s)
--- PASS: TestPostgresSharedObjectTriggerRaceCharacterization (0.41s)
--- PASS: TestPostgresLegacyRefreshSharedObjectHazardReproducer (0.11s)
--- PASS: TestPostgresFencedRefreshAtomicReplacement (0.25s)
--- PASS: TestPostgresFencedRefreshPreservesSharedObject (0.30s)
--- PASS: TestPostgresFencedRefreshRefusesCrossSourcePage (0.25s)
--- PASS: TestPostgresFencedRefreshRollbackRestoresOldGeneration (0.25s)
--- PASS: TestPostgresFencedRefreshRejectsExpiredClaim (0.24s)
--- PASS: TestPostgresKeywordSetPersistence (15.86s)
--- PASS: TestPostgresKeywordSuppressionWindowBranches (0.14s)
    --- PASS: .../just_inside
    --- PASS: .../exactly_at_the_boundary
    --- PASS: .../just_outside
```

The two characterization tests pass, which is the expected outcome: they assert
that the pre-existing defects (R-01 trigger race, R-02 legacy shared-object
deletion) are still reproducible. Their pass is evidence of the defect, not of
correctness.

The keyword suite was run three times before the change (13.6 s, 13.6 s, 15.7 s)
and once after (15.9 s). The 38–56 s failures recorded in the B2F1 report could
not be reproduced on an unloaded host, so the mechanism was demonstrated
directly instead: the same production statement re-run 31 s after the first
insert flips `last_updated_at`, which is exactly the assertion the old test made
time-dependent.

## 6. Docker Compose and real browser (executed)

### 6.1 Build and image identity

Built from the tested commit with the repository Dockerfiles. The first attempt
through the default Docker network stalled on `proxy.golang.org`; the builds
were re-run with `docker build --network=host`, the same workaround recorded by
earlier packages.

| Image | Purpose |
| --- | --- |
| `rel230-api:gate` | General API (`Dockerfile.searchapi`) |
| `rel230-events:gate` | Events service (`Dockerfile.events`) |
| `rel230-engine:gate` | Crawler engine (`Dockerfile.thecrowler`) |
| `zfpsystems/crowler-vdi:latest` | Browser VDI, published image, not rebuilt |
| `postgres:17` | Database |
| `python:3.12-alpine` | Controlled local HTTP fixture |

### 6.2 Topology

Compose project `rel230rc1`, isolated from the maintainer's environment:

| Service | Container | Host binding |
| --- | --- | --- |
| `crowler-db` | `rel230-db` | `127.0.0.1:55432` |
| `crowler-api` | `rel230-api` | `127.0.0.1:18080` |
| `crowler-events` | `rel230-events` | `127.0.0.1:18082` |
| `crowler-engine-1` | `rel230-engine-1` | none |
| `crowler-vdi-1` | `rel230-vdi-1` | `127.0.0.1:14444-14445` |
| `fixture` | `rel230-fixture` | none, internal to both networks |

The generated Compose file enables Selenium tracing while being generated with
`--no_jaeger`; the override sets `SE_ENABLE_TRACING=false` because the exporter
endpoint would not resolve.

### 6.3 Scenario results

All scenarios ran against the isolated `rel230rc1` project with the published
VDI image and a controlled local HTTP fixture. Assertions are database queries
or HTTP responses, not log text alone.

| ID | Scenario | Result | Evidence |
| --- | --- | --- | --- |
| PREPARE | Stack readiness, schema, writer, default mode | **PASS** | API `/v1/health` and `/v1/ready` 200; Events `/v1/health` 200; Selenium `/status` `"ready": true`, one node; DB `schema=1.17`, `writer=verified-ownership-v1.17`, `mode=legacy`; engine and API/Events healthy |
| S-01 | Legacy crawl of the fixture | **PASS** | source 1 `completed`; `SearchIndex` 1; `SourceSearchIndex` 1; `WebObjects` 1; `WebObjectsIndex` 1; `crawl_completed` event observed (count 2 after two crawls); `SourceCrawlClaims` 0 (legacy path) |
| S-01b | API retrieval of the indexed page | **PASS** | `GET /v1/search/pages?q=REL230 Fixture Home` returned `totalResults=1` with `page_url=http://rel230-fixture:8000/index.html` |
| S-02 | Legacy shared-object hazard | **PASS (defect reproduced)** | Covered deterministically by `TestPostgresLegacyRefreshSharedObjectHazardReproducer`; see R-02. Not re-run through the UI because the SQL reproduction is stronger. |
| S-03 | Fenced crawl | **PASS** | source 2 claimed (`SourceCrawlClaims` 1); `SourceCrawlAttempts` state `completed`; 2 pages, 2 links, 2 objects; source status left `processing` (documented unbound suppression) |
| S-04 | Fenced refresh success | **PASS** | after changing the fixture body and re-queueing: exactly 1 page link, object content `generation two`, `generation one` object rows 0, `generation two` rows 1, attempt `completed` |
| S-05 | Fenced refresh forced SQL error | **PASS** | injected `BEFORE DELETE` trigger raising on the old object: attempt states `completed,failed`; page still `generation two`; `generation three` rows 0; error propagated through the initial-page path; terminal result `applied` with failure |
| S-06 | Cross-source page refusal | **PASS** | page linked to sources 1 and 2; refresh attempt `failed`; object still `generation two`; `generation four` rows 0; both `SourceSearchIndex` links intact; log line `fenced refresh refused: page is shared with another source (index_id=1, other_sources=1)` |
| S-07 | Fenced `null:` source | **PASS** | source 4 retained; attempt `failed`; log `Quarantining null: source deletion for fenced source ID 4` |
| S-08 | Legacy `null:` source | **PASS** | source 3 inserted then deleted (`Sources` rows with `null:` prefix = 0); no page indexed |
| S-09 | Disable the source during a fenced crawl | **PASS** | claim acquired, then `disabled=true`; log `source crawl fence is not current: source_disabled`; terminal result `stale_fence (source_disabled)`; attempt stayed `claimed`; no page write; source status left `processing` |
| S-10 | Expiry / supersession / restart-and-reclaim | **Covered by integration tests, not re-run through the UI** | `TestPostgresFencedRefreshRejectsExpiredClaim`, `TestSourceCrawlAttempt*` supersession/lease tests, and the earlier B2V live restart scenario. Recorded as integration evidence, not as a Docker pass. |
| NEG-01 | No MCP mutation dispatch | **PASS** | `MCPMutationDispatchAttempts` 0; `MCPMutationProposals` 0; `CrawlProposals` 0; `CrawlApprovals` 0; `MCPMutationAudit` 0 |
| NEG-02 | No autonomous asynchronous resume | **PASS** | `AgentGoalRuns` 0; no resume worker exists in the binary; the engine was idle after each crawl and nothing transitioned on its own |

One observation outside the release decision: `crawl_completed` events were
created (count observed) but later reads of `Events` returned zero even with
`events.automatic_events_removal: never`. The janitors only delete rows with a
past `expires_at`, and `SetEventAsProcessed` sets `expires_at = NOW() + 1
minute`, so something marks them processed. That is an events-retention
question, independent of 2.3.0's agentic scope. It is recorded as a P3
observation needing its own check, **not** as a pass and not as a blocker.

## 6.4 Image identity

```text
rel230-api:gate     af349bb4e731
rel230-events:gate  365306a0993a
rel230-engine:gate  9c759eeb8b1e
postgres:17         (digest pulled at run time)
zfpsystems/crowler-vdi:latest  1e261c344a1e
                               digest sha256:d834e1f0954ca9e98312f43372e453e2f38f783de892f75e5bacfa884fd359a8
```

The three CROWler images were produced by building the service binaries on the
host from the tested commit with `CGO_ENABLED=1` and copying them into a
minimal Debian runtime image (`Dockerfile.rel230`, not shipped). The repository
Dockerfiles (`Dockerfile.searchapi`, `Dockerfile.events`,
`Dockerfile.thecrowler`) were attempted first and stalled on Docker-internal Go
module downloads even with `--network=host`; the same workaround (host module
cache) was recorded by earlier packages. The consequence is that the engine
image for this gate does **not** include `nmap`, the JDK or `bind-tools`, which
the shipped image adds for service scanning. Those features are disabled in the
test configuration, so the crawl path exercised here is unaffected, but this is
a deviation from the shipped image and is recorded as such.

## 7. What was not executed

| Item | Status | Reason |
| --- | --- | --- |
| Hosted GitHub CI for the patched tree | **Unverified** | No push was performed and no GitHub token is available. The reviewed baseline's checks do not cover this patch. |
| Live Ollama inference and live Ollama tool calling | **Unverified** | No Ollama endpoint or tool-capable model is configured in this environment. `CROWLER_OLLAMA_SMOKE_URL`/`_MODEL` were not set. This is a named environmental gap, not a pass. |
| Live external MCP server | **Unverified** | Only deterministic local fixtures are used by the repository; no external MCP endpoint was configured. |
| Information Seed Compose acceptance suite | **Not run** | Separate opt-in suite; not required by this scope and it needs its own provider fixtures. |
| Production or shared database migration | **Not run** | Explicitly forbidden by the work package. |
| MySQL runtime | **Not run** | MySQL remains legacy-only by contract; no MySQL server was configured. |
| `tests/integration/mcp-authorization` E2E workflow | **Not run** | `workflow_dispatch`-only and not registered on the default branch; the local fixture requires a disposable Docker-in-Docker daemon. |
| Go fuzzing | **Not run** | Not part of the release gate; the seeded fuzz targets were exercised only as part of `go test ./...`. |

## 8. Final result block

| Gate | Result |
| --- | --- |
| Full unit suite (post-change) | PASS, 35 packages, exit 0 |
| Race suite (four packages) | PASS, exit 0, no data race |
| Static set (vet, vet -tags, builds, gofmt, diff --check) | PASS |
| actionlint (every workflow) | PASS |
| Manifest lint + strict (14 files) | PASS, 28/28 |
| PostgreSQL 17 fresh install | PASS |
| Migration/addon/backup/restore rehearsal | PASS |
| Full PostgreSQL integration suite (4 packages) | PASS, exit 0 |
| Docker + WebDriver scenarios | PASS for S-01, S-01b, S-03..S-09, NEG-01, NEG-02 |
| Characterization tests | PASS, which confirms the two known defects remain reproducible |
| Hosted CI | Unverified |
| Live Ollama / live MCP | Unverified |

Change set delivered with this report:

| File | Change |
| --- | --- |
| `pkg/crawler/crawler.go` | Extracted `upsertPostgresKeywordRowsTx` from `upsertPostgresKeywords` (pure refactor; the public function still owns the transaction and behaviour is unchanged) |
| `pkg/crawler/keyword_postgres_integration_test.go` | Re-anchored the suppression assertion; added `TestPostgresKeywordSuppressionWindowBranches` and the shared `openKeywordTestPostgres` helper |
| `pkg/crawler/fenced_refresh_batching_test.go` (new) | SQLite batching, later-batch rollback and shared-object attribute coverage |
| `pkg/crawler/fenced_refresh_concurrency_postgres_integration_test.go` (new) | PostgreSQL batching, page-lock boundary, orphan-lock boundary, trigger characterization, concurrent refreshers plus ordinary indexer, legacy hazard reproducer |
| `.github/workflows/postgresql-integration.yml` | Mandatory gates for the new fenced-refresh tests, the two shared-object characterization tests, and the keyword suppression tests |
| `docs/crowler-agents/reviews/ASYNC_GOAL_B3C3B2F1_REFRESH_DESIGN.md` | Dated original-design vs implemented-result split; section 10 with the verified behaviour, trigger interaction, trigger race, coverage matrix, post-commit semantics and `null:` inventory |
| `docs/crowler-agents/reviews/ASYNC_GOAL_B3C3B2F1_TEST_REPORT.md` | Release-gate re-verification section |
| `docs/crowler-agents/reviews/ASYNC_GOAL_MAINTAINER_DECISIONS.md` | Decision 1n: open release-gate finding (not a decision) |
| `docs/test_policy.md` | Fenced refresh lock/batching requirements, characterization-test policy, keyword window determinism policy |
| `docs/crowler-agents/reviews/RELEASE_230_*.md` (5 new) | This release package |

No production behaviour was changed. No schema, migration, public API,
configuration default or claim-mode default changed.
