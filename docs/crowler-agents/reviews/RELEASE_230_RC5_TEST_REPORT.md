# Release 2.3.0 RC5 — test report

Work package: `WP-REL230-RC5`
Baseline / frozen code SHA: `906000f3871c988e24a65ee32e1ec452491357d6`
Worktree: `/tmp/opencode/thecrowler-rc5`
Original checkout: `/home/pzaino/workspace/github/thecrowler`, clean before and after
Date: 2026-11-20
Release: `v2.3.0` · Schema `1.17` · Writer `verified-ownership-v1.17`

Evidence classes: **executed**, **hosted**, **NOT RUN**. A skipped test is never a
pass.

## 1. Environment

| Item | Value |
| --- | --- |
| Host | Linux x86_64, 32 CPUs |
| Go | `go1.27.4` (`go.mod` requires `1.26.6`) |
| Docker Engine | `29.9.2-ce`; Compose `5.7.1` |
| Disposable PostgreSQL | `postgres:17`, container `rc5-pg`, `127.0.0.1:55977` |
| Acceptance stack | Compose project `rc5gate`, PostgreSQL 17, `rc5-api`, `rc5-events`, `rc5-engine-1`, published VDI, local fixture |
| VDI image | `zfpsystems/crowler-vdi:latest` |
| Hosted CI | §2 |

## 2. Hosted CI

The baseline `906000f3` was pushed by the maintainer and its checks were queried
directly from the GitHub API.

`GET /repos/pzaino/thecrowler/commits/906000f3.../check-runs` → `total_count: 8`,
every conclusion `success`:

| Check | Conclusion | URL |
| --- | --- | --- |
| `build` | success | https://github.com/pzaino/thecrowler/actions/runs/44819285324/job/136575662229 |
| `postgres-integration` | success | https://github.com/pzaino/thecrowler/actions/runs/44819285194/job/136575664329 |
| `SQL parser CGO=0` | success | https://github.com/pzaino/thecrowler/actions/runs/44819285473/job/136575663770 |
| `SQL parser CGO=1` | success | https://github.com/pzaino/thecrowler/actions/runs/44819285473/job/136575663612 |
| `Static binaries linux/amd64` | success | https://github.com/pzaino/thecrowler/actions/runs/44819285473/job/136575663075 |
| `Static binaries linux/arm64` | success | https://github.com/pzaino/thecrowler/actions/runs/44819285473/job/136575663517 |
| `Static binaries darwin/amd64` | success | https://github.com/pzaino/thecrowler/actions/runs/44819285473/job/136575663488 |
| `Static binaries darwin/arm64` | success | https://github.com/pzaino/thecrowler/actions/runs/44819285473/job/136575663605 |

This confirms the hosted state of the RC4F commit that RC5 builds on. It is
baseline evidence only.

The RC5 patch has **not been pushed**, so no hosted run exists for it.
`HOSTED CI: NOT RUN` for the candidate; a green baseline cannot certify code it
does not contain.

## 3. Static gates (executed)

| Command | Result | Log |
| --- | --- | --- |
| `go test ./... -count=1 -timeout 90m -p 1` | PASS, 35 packages, exit 0 | `rc5-full-unit.log` |
| `go vet ./...` | PASS | `rc5-vet.log` |
| `go vet -tags=integration ./pkg/agent ./pkg/database ./pkg/crawler ./services/events ./pkg/vdi` | PASS | `rc5-veti.log` |
| `CGO_ENABLED=0 go build ./...` | PASS | `rc5-static.log` |
| `CGO_ENABLED=1 go build ./...` | PASS | `rc5-static.log` |
| `gofmt -l pkg cmd services` | clean (no output) | `rc5-static.log` |
| `git diff --check` | PASS | `rc5-static.log` |
| `actionlint` v1.7.7 on `.github/workflows/*.yml` | PASS, exit 0 | inline |
| 14 shipped manifests × (`lint` + `validate --strict`) | 28/28 PASS, 0 failed, 0 skipped | inline |
| `go test -race ./pkg/agent/... ./pkg/database/... ./pkg/crawler/... ./cmd/thecrowler/... ./services/events/... -count=1` | PASS, exit 0, no data race (5 packages: agent, database, crawler, cmd, events) | `rc5-race.log` |

## 4. PostgreSQL migration, permissions and restore (executed)

Disposable PostgreSQL 17. Fresh install, then a second database that was
deliberately reverted to the previous row-level definition and re-migrated twice.

```text
fresh install:      schema=1.17  writer=verified-ownership-v1.17  tables=78
function digest:    5bdd88efd2e200043c2fb1502be5ba3b (fresh)

simulated previous definition:
  function md5        6d33f3492e828ea1334de93baeba5c95  (RC4F, FOR EACH ROW)
  row-level triggers  6  (5 link triggers + the unrelated source_searchindex)

migration pass 1:   exit 0
migration pass 2:   exit 0
migrated digest:    5bdd88efd2e200043c2fb1502be5ba3b  (identical to fresh)
statement triggers: 5  (row_level=false, tgoldtable=deleted_links)
schema/writer:      1.17 / verified-ownership-v1.17  (unchanged)
seeded fixture row: preserved

pg_dump --format=custom -> 678,741 bytes, 77 TABLE DATA entries
pg_restore             -> exit 0
rc5_mig     : tables=78 sources=1 fn=5bdd88ef… statement_triggers=5
rc5_restore : tables=78 sources=1 fn=5bdd88ef… statement_triggers=5
```

`TestPostgresSharedArtifactTriggerDefinitionConverges` performs the same
convergence rehearsal inside the test suite: it restores the row-level
definition, re-applies the migration trigger block, and asserts the five
statement-level triggers, the transition relation and the shipped function body.

## 5. RC5 integrity suite (executed)

`go test -tags=integration ./pkg/database -run '^TestPostgresSharedArtifact' -count=1 -v`
against the real schema 1.17 database, all five families:

| Test | Families | Result |
| --- | --- | --- |
| `…ConcurrentInsertSurvivesLastLinkDelete` | 5 | PASS |
| `…ConcurrentLastLinkRemovals` (10 iterations each) | 5 | PASS |
| `…WideParentRemovalKeepsInserts` (40 links, 8 concurrent inserts) | 5 | PASS |
| `…FinalRemovalDeletesParent` | 5 | PASS |
| `…RandomOrderContention` (6 workers × 20 iterations) | 5 | PASS, 0 deadlocks |
| `…VacuumAndCascadeKeepOtherPages` | 5 | PASS |
| `…IsolationLevelIsReadCommitted` | — | PASS |
| `…LegacyBodyLosesConcurrentLink` (characterization) | keyword | PASS (loss reproduced) |
| `…SingleStatementDeleteIsUnsafe` (characterization) | keyword | PASS (loss reproduced) |
| `…TriggerDefinitionConverges` | — | PASS |

The two characterization tests assert that the *unsafe* bodies lose data. They
are not expectations about the shipped function.

The same set is now a mandatory CI gate. `.github/workflows/postgresql-integration.yml`
gains `Run shared-artifact locking PostgreSQL integration`, which exports the
disposable opt-in, runs `-run '^TestPostgresSharedArtifact' -skip
'^TestPostgresSharedArtifactContentionBenchmark$'`, requires a visible `=== RUN`
and `--- PASS` for each of the ten tests, and fails on `--- SKIP`. The exact
command and assertions were executed locally against the disposable database:
10/10 RUN and PASS, 0 skips.

The existing RC2 crawler regressions were re-run against the new trigger and
still pass:

```text
go test -tags=integration ./pkg/crawler \
  -run 'TestPostgresFencedRefresh|TestPostgresSharedObject|TestPostgresLegacyRefresh|TestPostgresRefreshAndVacuum|TestPostgresKeyword'
ok  github.com/pzaino/thecrowler/pkg/crawler  63.3s
```

## 6. Contention benchmark (executed)

`CROWLER_RC5_BENCH=1 CROWLER_RC5_POSTGRES_TEST_DISPOSABLE=1 … go test -tags=integration ./pkg/database -run '^TestPostgresSharedArtifactContentionBenchmark$'`

CSV: `shared-artifact-contention.csv` (written to `RC5_BENCH_OUT`). One
measurement per family; the deadlock probe runs three iterations; the table is the
mean across the five families:

| Workload | Variant | Throughput | p95 | p99 | Lock-waiting | Failures | Deadlocks |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| mass_delete | rc4f | 4,954 links/s | 82.4 ms | 118.1 ms | 42.3% | 0 | 0 |
| mass_delete | **current** | **13,700 links/s** | **48.3 ms** | **59.8 ms** | **34.7%** | 0 | 0 |
| mass_delete | v228 | 8,871 links/s | 34.3 ms | 37.0 ms | 23.8% | 0 | 0 |
| single_link_churn | rc4f | 85.2 ops/s | 142.8 ms | 169.5 ms | 85.6% | 0 | 0 |
| single_link_churn | **current** | 64.3 ops/s | 157.0 ms | 210.3 ms | 86.1% | 0 | 0 |
| single_link_churn | v228 | 396.8 ops/s | 31.7 ms | 44.0 ms | 1.4% | 0 | 0 |
| overlapping_vacua | rc4f | 34.9 it/s | — | — | — | 0 | 0 |
| overlapping_vacua | **current** | **166.0 it/s** | — | — | — | 0 | 0 |
| overlapping_vacua | v228 | 57.5 it/s | — | — | — | 0 | 0 |

`current` is read from `postgresql-setup.pgsql`, so the benchmark measures the
shipped SQL. `v228` is the pre-RC2 body and is not an acceptable candidate: it
loses a concurrently committed reference.

Run-to-run spread, from two independent full runs of the matrix on the same
host: `rc4f` mass_delete 4,954 vs 5,179 links/s (4.3%), `rc4f` single-link churn
85.2 vs 87.2 ops/s (2.3%), `current` mass_delete 13,700 vs 15,427 links/s (12.6%).
The effect being reported (2.8×–4.8×) is far larger than that spread; the
single-link regression (~25%) is larger than the churn spread but not by a wide
margin, which is why it is reported as approximately 25% rather than a precise
figure.

Limitations: `lock_wait_ratio` is a 5 ms `pg_stat_activity` sample of the fraction
of active backends waiting on a lock, not exact lock-wait time.
`pg_stat_statements` and `log_lock_waits` were not enabled. Deadlock counts are
zero in every cell, which is consistent with the deterministic ordering but is
not proof that application-level deadlocks cannot occur.

## 6a. Full tagged PostgreSQL suite (executed)

```bash
go test -p 1 -tags=integration \
  ./pkg/database/... ./pkg/crawler/... ./pkg/agent/... \
  ./services/api/... ./services/events/... -count=1 -timeout 40m
```

**PASS, exit 0**, 5 packages `ok` (`database 122.5s`, `crawler 43.5s`, `agent`,
`api 4.5s`, `events 3.1s`), no `--- SKIP` in the selected packages. Log:
`/tmp/opencode/rc5-pg-full.log`.

## 7. Docker and real-browser acceptance

### 7.1 Shipping images

Built from the repository Dockerfiles at the RC5 tree, default module path, no
`GOPROXY` build argument, `--network=host` only for registry reachability.

| Image | Dockerfile | Exit | Elapsed | Image ID |
| --- | --- | --- | --- | --- |
| `rc5-api:ship` | `Dockerfile.searchapi` | 0 | 455 s | `sha256:16b2482deab0…` |
| `rc5-events:ship` | `Dockerfile.events` | 0 | 430 s | `sha256:486bace2cf1a…` |
| `rc5-engine:ship` | `Dockerfile.thecrowler` | 0 | 467 s | `sha256:84e0883c3c80…` |

Logs: `/tmp/opencode/rc5-build-rc5-api:ship.log`, `…-rc5-events:ship.log`,
`…-rc5-engine:ship.log`.

### 7.2 Stack

Isolated Compose project `rc5gate` (never the maintainer's `crowler-*`
project): PostgreSQL 17 initialised from the RC5 `postgresql-setup.pgsql`,
`rc5-api` on `127.0.0.1:18120`, `rc5-events` on `18122`, one `rc5-engine-1`,
the published `zfpsystems/crowler-vdi` image, and a fixture container reachable
from both the crawler and browser networks.

The first database initialisation is what installs the RC5 trigger, so the
acceptance run is also an end-to-end check of the migration path a fresh
deployment takes.

The Compose override, `config.yaml` and `.env` are local gate scaffolding and are
**not** part of the patch, consistent with the RC4/RC4F packages. They contain no
product behaviour.

### 7.3 Scenario results

Every assertion is a PostgreSQL query against the running stack, not a log line.

| ID | Scenario | Result | Evidence |
| --- | --- | --- | --- |
| P0 | Installed trigger shape on a **fresh** database (container init script) | **PASS** | schema `1.17`, writer `verified-ownership-v1.17`, `statement_triggers=5`, `row_triggers_other=1` (`trg_after_delete_source_searchindex`, unrelated), `fn=5bdd88ef…`, `mode=legacy` |
| P1 | Default legacy crawl | **PASS** | `completed`, 1 page, fixture marker present |
| P2 | `/hotel` → 302 `/hotel/` with source `follow_redirects: false` (global `true`) | **PASS** | `error`, 0 pages, trailing-slash body absent, `redirect not permitted: …/hotel?src=p2 landed on …/hotel/?src=p2` |
| P3 | Same URL with the global `true` default | **PASS** | `completed`, trailing-slash body indexed |
| P4 | `incognito: false` | **PASS** | `completed`, 1 page |
| P5 | Legacy `null:` pipeline source | **PASS** | created, then deleted; 0 `null:` sources, 0 `null:` pages |
| P6 | **Vacuum one of two sources sharing an artifact** | **PASS** | shared object had 3 page links, survives with 2 after the vacuum; keyword links 19 → 16, keyword rows 8 → 8; `orphan_keywords=0`, `orphan_objects=0`; vacuumed source's `SourceSearchIndex` links 1 → 0; the other source keeps its link |
| P7 | Fenced crawl, then fenced `null:` | **PASS** | attempt `completed` with 1 page; fenced `null:` attempt `failed` and the source was retained; claim mode restored to `legacy` |

P6 is the end-to-end exercise of the RC5 change: `VacuumSource` issues one
`DELETE` per family over every page of the source, so the statement-level trigger
locks each distinct shared parent once, keeps the object that another source still
references, and leaves no orphan behind.

The first P6 attempt used the wrong query parameter (`url=` instead of `q=`) and
the API correctly answered `400 query parameter 'q' is required`; nothing was
deleted. The run was repeated with `q=` and the table above reports that run.
This was a harness mistake, not a product defect.

Screenshot and full-site capture are disabled in the acceptance configuration,
so no screenshot rows are part of these assertions.

## 8. Information Seed Compose acceptance

**Executed for real.** The repository's own Compose acceptance harness was run
against the RC5 tree:

```bash
python3 tests/integration/infoseed-compose/run.py --fixture-only
```

Result: **exit 0, 16/16 PASS, 0 FAIL** (`/tmp/opencode/rc5-infoseed.log`). The
harness built its own service images with `--no-cache` from the RC5 source, used a
disposable Compose project, and removed its containers, volumes and images
afterwards.

| Case | Scenario | Result |
| --- | --- | --- |
| IT-01 | Fresh database bootstrap and schema correctness | PASS |
| IT-02 | AuthN/AuthZ gates on the actual HTTP routes | PASS |
| IT-03 | Link-only seed preserves existing Sources | PASS |
| IT-04 | Create new Sources with verified ownership | PASS |
| IT-05 | Existing config refresh and processing-state guard | PASS |
| IT-06 | RSS/Atom parsing, filtering and snippet preservation | PASS |
| IT-07 | Provider rate limiter outside the HTTP timeout | PASS |
| IT-08 | Dedicated GDELT DOC adapter | PASS |
| IT-09 | Retry scheduling, backoff, permanent failures, fairness | PASS |
| IT-10 | Heartbeats maintain exclusive ownership | PASS |
| IT-11 | SIGTERM release and SIGKILL lease recovery | PASS |
| IT-12 | Candidate cap and ordering determinism | PASS |
| IT-13 | Retention and live-lease protection | PASS |
| IT-14 | PostgreSQL claim index and query plan | PASS |
| IT-15 | Operator API, events and end-to-end observability | PASS |
| IT-16 | Controlled restart and migration posture | PASS |

IT-01 also re-initialises schema 1.17 from the RC5 setup SQL, so the trigger
change is exercised by the Information Seed stack's own bootstrap.

## 9. Named skips and NOT RUN

| Item | Status | Reason |
| --- | --- | --- |
| Hosted CI on the RC5 SHA | **NOT RUN** | Patch not pushed |
| Live Ollama / live external MCP | **NOT RUN** | No endpoint/model configured |
| MySQL runtime | **NOT RUN** | Legacy-only by contract |
| Hosted `mcp-authorization-e2e` | **BLOCKED** | Dispatch-only, absent from the protected default branch |
| Production telemetry / `pg_stat_statements` | **NOT RUN** | No production access; not assumed |
| Production or shared database migration | **NOT RUN** | Forbidden by the work package |
