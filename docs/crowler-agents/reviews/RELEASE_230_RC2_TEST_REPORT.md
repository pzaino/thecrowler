# Release 2.3.0 RC2 test report

Work package: `WP-REL230-RC2`
Tested commit: `e6a41d317835356b1ed7ebd3fd31c8aa781f272c` plus the RC2 patch
(see the delivery message for the patched tree digest)
Worktree: `/tmp/opencode/thecrowler-rc2`
Original checkout: `/home/pzaino/workspace/github/thecrowler`, clean before and
after
Date: 2026-11-20
Release: `v2.3.0`
Schema: `1.17` / writer generation `verified-ownership-v1.17`

Evidence classes: **executed** (command run here, result recorded), **covered by
integration test** (asserted at SQL level but not through the UI), **NOT RUN**
(named reason). A skipped test is never a pass.

## 1. Environment

| Item | Value |
| --- | --- |
| Host | Linux x86_64, 32 CPUs |
| Go | `go1.27.4` (`go.mod` 1.26.6) |
| Docker Engine | `29.9.2-ce`, Compose `5.7.1` |
| PostgreSQL | `postgres:17` → 17.12, disposable container `rc2-pg` on `127.0.0.1:55927` |
| VDI | `zfpsystems/crowler-vdi:latest`, digest `sha256:d834e1f0954ca9e98312f43372e453e2f38f783de892f75e5bacfa884fd359a8`, image ID `sha256:1e261c344a1eda689864f30fbf7aa6ba7cd16699a6333ce9198953d49775b23d` |
| Compose project | `rc2gate` (isolated, removed after the run) |

The maintainer's containers were not touched. Only `portainer` was running at
the start.

## 2. Pre-fix reproduction (before any edit)

```text
go test -tags=integration ./pkg/crawler \
  -run '^(TestPostgresSharedObjectTriggerRaceCharacterization|TestPostgresLegacyRefreshSharedObjectHazardReproducer)$'
--- PASS: TestPostgresSharedObjectTriggerRaceCharacterization
--- PASS: TestPostgresLegacyRefreshSharedObjectHazardReproducer
EXIT=0
```

Both tests passed by asserting the defective outcome (RC1 characterization), so
both defects were present before the fix. The first asserted
`object_rows=0`, `page_y_links=0`; the second asserted the shared object and the
other page's link destroyed by a legacy refresh.

## 3. Static and unit gates (executed)

| Command | Result | Log |
| --- | --- | --- |
| `go test ./... -count=1 -timeout 90m -p 1` | PASS, 35 packages, exit 0 | `/tmp/opencode/rc2-full-unit.log` |
| `go test -race ./pkg/agent/... ./pkg/database/... ./pkg/crawler/... ./cmd/thecrowler/... ./services/events/...` | PASS, exit 0, no data race | `/tmp/opencode/rc2-race.log` |
| `go vet ./...` | PASS | `/tmp/opencode/rc2-static.log` |
| `go vet -tags=integration ./pkg/agent ./pkg/database ./pkg/crawler ./services/events` | PASS | `/tmp/opencode/rc2-static.log` |
| `CGO_ENABLED=0 go build ./...` | PASS | `/tmp/opencode/rc2-static.log` |
| `CGO_ENABLED=1 go build ./...` | PASS | `/tmp/opencode/rc2-static.log` |
| `gofmt -l pkg cmd services` | clean (no output) | `/tmp/opencode/rc2-static.log` |
| `git diff --check` | PASS | `/tmp/opencode/rc2-static.log` |
| `actionlint` (pinned v1.7.7) on every workflow | PASS, exit 0 | inline |
| 14 example manifests × `lint` + `validate --strict` | PASS, 28/28 | see section 8 |

## 4. Database gates (executed)

### 4.1 Fresh install

`postgresql-setup.sh` on an empty `SitesIndex`:

```text
setup exit 0
schema=1.17
writer=verified-ownership-v1.17
handle_shared_entity_deletion has FOR UPDATE = true
```

### 4.2 Migration / addon convergence

Simulated a pre-fix 1.16-era function (no lock), then applied
`postgresql-migration-v1.17.pgsql` twice:

```text
pre-fix body has FOR UPDATE      = false
migration pass 1                 = exit 0
migration pass 2 (repeatable)    = exit 0
post-migration body has lock     = true
DBSchemaVersion                  = 1.17
DatabaseWriterCompatibility      = verified-ownership-v1.17
trg_after_delete_* triggers      = 6
```

### 4.3 Backup, restore and data comparison

On `rc2_mig` with a seeded `Sources` row:

```text
pg_dump --format=custom        -> 677,044 bytes, exit 0
pg_restore --list              -> 77 TABLE DATA entries
pg_restore into rc2_restore    -> exit 0
rc2_mig:     tables=78 sources=1 function_locked=true
rc2_restore: tables=78 sources=1 function_locked=true
```

### 4.4 Full PostgreSQL integration suite

```bash
go test -p 1 -tags=integration ./pkg/database/... ./pkg/crawler/... ./pkg/agent/... ./services/api/... -count=1 -timeout 40m
```

PASS, exit 0:

```text
ok pkg/database   60.989s
ok pkg/crawler    49.285s
ok pkg/agent      48s
ok services/api    3s
```

Log: `/tmp/opencode/rc2-pg-full.log`. No `--- SKIP` in the selected packages.

## 5. New regressions (executed)

| Test | Result | What it proves |
| --- | --- | --- |
| `TestPostgresSharedObjectTriggerPreservesConcurrentReference` | PASS | uncommitted concurrent link makes the trigger wait; after commit the object, its attributes and page Y's link survive (`object_rows=1`, `page_y_links=1`) |
| `TestPostgresSharedObjectTriggerDeletesZeroReferenceObject` | PASS | a genuinely unreferenced object is still deleted, three times, idempotently |
| `TestPostgresSharedObjectTriggerStress` | PASS | 25 iterations of the interleaving, no loss, bounded by a 90 s budget |
| `TestPostgresRefreshAndVacuumRemainConsistent` | PASS | concurrent legacy refresh and `VacuumSource` on a shared object: no deadlock, object exists iff a link remains |
| `TestPostgresLegacyRefreshPreservesSharedObject` | PASS | refreshing page X keeps page Y's link, the shared object and its attributes |
| `TestPostgresLegacyRefreshRefusesCrossSourcePage` | PASS | cross-source page refused, one link, content unchanged |
| `TestPostgresLegacyRefreshRollbackRestoresOldGeneration` | PASS | injected delete failure restores the old link, object and content |
| `TestPostgresLegacyRefreshReplacesPageGeneration` | PASS | real `indexPageContext` legacy path replaces one generation with the next |
| `TestPostgresSharedEntityDeletionConcurrencyContract` (static) | PASS | setup and v1.17 migration bodies identical, all five families lock, SQLite keeps its documented omission |
| `TestEventRemovalActionPolicy` | PASS | all `automatic_events_removal` values × success/failure |
| `TestPostgresEventJanitorDeletesOnlyExpiredRows` | PASS | expired deleted, future kept, no-expiry kept |

Logs: `/tmp/opencode/rc2-crawler-pg1.log`, `/tmp/opencode/rc2-pg-full.log`.

## 6. Shipping images (executed)

Built with the repository Dockerfiles, not a temporary packaging image:

```bash
docker build --network=host --build-arg GOPROXY=http://127.0.0.1:18734 \
  -t rc2-api:ship   -f Dockerfile.searchapi .
docker build --network=host --build-arg GOPROXY=http://127.0.0.1:18734 \
  -t rc2-events:ship -f Dockerfile.events .
docker build --network=host --build-arg GOPROXY=http://127.0.0.1:18734 \
  -t rc2-engine:ship -f Dockerfile.thecrowler .
```

| Image | ID | Size | Build exit |
| --- | --- | --- | --- |
| `rc2-api:ship` | `sha256:38bcb0755e19…` | 536 MB | 0 |
| `rc2-events:ship` | `sha256:a7f956f9b73b…` | 897 MB | 0 |
| `rc2-engine:ship` | `sha256:febc5f20713f11d7332c32190a0c5a413e9a8a121f6b08685fe69fff217ba49b` | 542 MB | 0 |

`Dockerfile.thecrowler` builds the binaries with `autobuild.sh` inside the
pinned `golang@sha256:3889b425…` builder, so the tested binaries are compiled
from this commit's source. Runtime base is the pinned `alpine@sha256:a8560b36…`.

Advertised tools verified inside `rc2-engine:ship`:

```text
nmap 7.95 (alpine), file caps cap_net_admin,cap_net_raw=eip
java openjdk 11.0.30
dig 9.18.50
/app/healthCheck, /app/dbDeploy present and executable
/app/config.yaml 900 lines
user: crowler (uid 1000)
```

**Build-process fix.** The original Dockerfiles stalled on Docker-internal Go
module downloads (reproducible; the container had network reachability but the
build hung at the download step). The fix adds `ARG GOPROXY` with the public
default and `ENV GOPROXY=${GOPROXY}` to the three builder stages. A constrained
build network can pass an internal proxy; `go.sum` and `GOSUMDB` verification
are unchanged, and a normal build without the argument behaves as before. The
gate used a loopback proxy served from the host's verified module cache.

## 7. Full stack on the shipping images (executed)

Isolated Compose project `rc2gate`, shipping images, published VDI, controlled
local HTTP fixture (`python:3.12-alpine` serving `tests/fixtures/rc2`).

### 7.1 Readiness

```text
API  /v1/health 200, /v1/ready 200
Events /v1/health 200
Selenium /status 200
DB schema=1.17 writer=verified-ownership-v1.17 mode=legacy function_locked=true
engine and API/Events healthy
```

### 7.2 Scenarios

| ID | Scenario | Result | Evidence |
| --- | --- | --- | --- |
| PREPARE | Readiness, schema, writer, default mode, function body | **PASS** | as above |
| S-01 | Legacy crawl (`refresh_content: false`) | **PASS** | source 1 `completed`; pages=1 links=1 objects=1 woi=1; claims=0 (legacy path); `crawl_completed` events=1 |
| S-02 | Legacy refresh with a hash-shared page | **PASS** | after refreshing page A: pageA_objects=1, pageB_objects=1, shared_object_rows=1, shared_object_attrs=1, pageB_keeps_shared=1 |
| S-03 | Fenced crawl | **PASS** | claims=1, attempts=1 state `completed`, pages=2, objects=2 |
| S-04 | Fenced refresh | **PASS** | attempts=1 state `completed`; pageA_objects=1; v6 object present, v5 object absent; page B still linked |
| S-05 | Legacy refresh with injected `BEFORE DELETE` failure | **PASS (data) / note** | old object row=1, new content rows=0, page link intact, `last_error` = `page indexing failed: removing fenced refresh page link 6: pq: rc2 injected refresh failure`. Source status was `completed` with `last_error` set — unchanged legacy error classification, see P3-2 |
| S-06 | Legacy refresh of a cross-source page | **PASS** | source status `error`; `content refresh refused: page is shared with another source (index_id=5, other_sources=1)`; page keeps 1 object and 2 source links |
| S-07 | Fenced `null:` source | **PASS** | source retained (1), attempt `failed`, no deletion |
| S-08 | Legacy `null:` source | **PASS** | source deleted; only the fenced `null:` row remained |
| NEG-01 | No MCP mutation dispatch | **PASS** | `MCPMutationDispatchAttempts`=0, `MCPMutationProposals`=0, `CrawlProposals`=0 |
| NEG-02 | No autonomous async resume | **PASS** | `AgentGoalRuns`=0; no resume worker in the binary; no self-transition observed |

Mode was restored to `legacy` before shutdown.

Stack log: `/tmp/opencode/rc2-stack.log`.

## 8. Manifest validation (executed)

```text
crowler-agt agents lint <file>            x14  PASS
crowler-agt agents validate --strict <file> x14  PASS
manifest_checks_ok=28 manifest_checks_fail=0
```

## 9. NOT RUN

| Item | Status | Reason |
| --- | --- | --- |
| Hosted GitHub CI on the patched SHA | **NOT RUN** | No push was performed and no GitHub token is available. Local results do not imply hosted green. |
| Live Ollama inference and tool calling | **NOT RUN** | No Ollama endpoint or tool-capable model configured. |
| Live external MCP endpoint | **NOT RUN** | Only local fixtures are used by the repository. |
| Information Seed Compose acceptance suite | **NOT RUN** | Separate opt-in suite, out of scope. |
| MySQL runtime | **NOT RUN** | MySQL remains legacy-only; no server configured. |
| `mcp-authorization-e2e` workflow | **NOT RUN** | `workflow_dispatch`-only and not registered on the default branch. |
| Production or shared database migration | **NOT RUN** | Forbidden by the work package. |

## 10. Log index

| Log | Content |
| --- | --- |
| `/tmp/opencode/rc2-prefix-repro.log` | pre-fix defect reproduction |
| `/tmp/opencode/rc2-full-unit.log` | full unit suite |
| `/tmp/opencode/rc2-race.log` | race suite |
| `/tmp/opencode/rc2-static.log` | vet/builds/gofmt/diff |
| `/tmp/opencode/rc2-pg-full.log` | full PostgreSQL integration suite |
| `/tmp/opencode/rc2-crawler-pg1.log` | crawler refresh regressions |
| `/tmp/opencode/rc2-mig-apply1.log`, `rc2-mig-apply2.log` | migration convergence |
| `/tmp/opencode/rc2-mig.dump`, `/tmp/opencode/rc2-restore.log` | backup/restore |
| `/tmp/opencode/rc2-build-api2.log`, `rc2-build-events2.log`, `rc2-build-engine3.log` | shipping image builds |
| `/tmp/opencode/rc2-up.log`, `rc2-stack.log`, `rc2-down.log` | full-stack run |
