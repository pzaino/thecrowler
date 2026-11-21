# Release 2.3.0 RC4F test report

Work package: `WP-REL230-RC4F`
Baseline / frozen code SHA: `b15cb8f027e896effb653622dd4977d906ab5438`
Worktree: `/tmp/opencode/thecrowler-rc4f`
Original checkout: `/home/pzaino/workspace/github/thecrowler`, clean before and
after
Date: 2026-11-20
Release: `v2.3.0` · Schema `1.17` · Writer `verified-ownership-v1.17`

Evidence classes: **executed**, **live browser**, **hosted**, **NOT RUN**. A
skipped test is never counted as a pass.

## 1. Environment

| Item | Value |
| --- | --- |
| Host | Linux x86_64, 32 CPUs |
| Go | `go1.27.4` (`go.mod` requires `1.26.6`) |
| Docker Engine | `29.9.2-ce`; Compose `5.7.1` |
| Disposable PostgreSQL | `postgres:17`, container `rc4f-pg`, `127.0.0.1:55967` |
| Acceptance stack | Compose project `rc4fgate`, PostgreSQL 17, `rc4f-api`, `rc4f-events`, `rc4f-engine-1`, published VDI, local fixture |
| VDI image | `zfpsystems/crowler-vdi:latest` |
| Hosted CI | see §2 |

## 2. Hosted CI on the baseline and on the candidate

At the frozen baseline `b15cb8f0` the repository reports eight successful
checks, including `postgres-integration`
(https://github.com/pzaino/thecrowler/actions/runs/44798833298/job/136505337964).

The RC4F patch has **not been pushed**, so **no hosted run exists for it**.
`HOSTED: BLOCKED / NOT RUN` for the candidate. Baseline green is evidence about
the parent commit only; it cannot certify code it does not contain.

## 3. Docker image build (executed)

All three images were built from the repository Dockerfiles at `b15cb8f0` plus
the RC4F patch, with the **default module path** and no `GOPROXY` build argument.

| Image | Dockerfile | Exit | Elapsed | Image ID |
| --- | --- | --- | --- | --- |
| `rc4f-api:ship` (first) | `Dockerfile.searchapi` | 0 | 405 s | `sha256:06a5ae04…` |
| `rc4f-events:ship` | `Dockerfile.events` | 0 | 422 s | `sha256:63678ab5…` |
| `rc4f-engine:ship` | `Dockerfile.thecrowler` | 0 | 409 s | `sha256:56d8d342…` |
| `rc4f-api:ship` (after the §5.3 fix) | `Dockerfile.searchapi` | 0 | 526 s | `sha256:444ddb65…` |

The API image was rebuilt after the swallowed-decode-error fix discovered by the
first live pass, and the whole acceptance matrix below was then re-run from an
empty database against that rebuilt image. This confirms RC3's conclusion that
the Docker module download was a false negative: the default path builds in
roughly seven to nine minutes here.

Logs: `/tmp/opencode/rc4f-build-rc4f-api:ship.log`,
`rc4f-build-rc4f-events:ship.log`, `rc4f-build-rc4f-engine:ship.log`,
`rc4f-build-api2.log`.

## 4. Local regression gates (executed)

| Command | Result | Log |
| --- | --- | --- |
| `go test ./... -count=1 -timeout 90m -p 1` | PASS, 35 packages, exit 0 | `rc4f-full-unit-final.log` |
| `go test -race ./pkg/agent/... ./pkg/database/... ./pkg/crawler/... ./cmd/thecrowler/... ./services/events/... -count=1` | PASS, exit 0, no data race (`agent 264.8s`, `database 54.5s`, `crawler 10.4s`, `cmd 2.0s`, `events 11.9s`) | `rc4f-race.log` |
| `go vet ./...` | PASS | `rc4f-vet-final.log` |
| `go vet -tags=integration ./pkg/agent ./pkg/database ./pkg/crawler ./services/events ./pkg/vdi` | PASS | `rc4f-veti-final.log` |
| `CGO_ENABLED=0 go build ./...` | PASS | `rc4f-static-final.log` |
| `CGO_ENABLED=1 go build ./...` | PASS | `rc4f-static-final.log` |
| `gofmt -l pkg cmd services` | clean (no output) | `rc4f-static-final.log` |
| `git diff --check` | PASS | `rc4f-static-final.log` |
| `actionlint` v1.7.7 on `.github/workflows/*.yml` | PASS, exit 0 | inline |
| 14 shipped manifests × (`lint` + `validate --strict`) | 28/28 PASS, 0 failed, 0 skipped | inline |

The full unit suite was re-run after the final API change so the numbers above
describe the delivered tree, not an intermediate one.

## 5. Correctness regressions (executed)

### 5.1 Redirect resource identity (`pkg/crawler/redirect_policy_test.go`)

`TestRedirectNotPermittedDecisionMatrix` now has 37 cases. The cases that pin the
RC4F fix:

| Case | Requested | Final | Expected |
| --- | --- | --- | --- |
| trailing slash on a real path | `https://a.test/hotel` | `https://a.test/hotel/` | **redirect** |
| trailing slash removed | `https://a.test/hotel/` | `https://a.test/hotel` | **redirect** |
| repeated trailing slashes | `https://a.test/a//` | `https://a.test/a/` | **redirect** |
| double slash | `https://a.test/a//b` | `https://a.test/a/b` | **redirect** |
| escaped separator | `https://a.test/a%2Fb` | `https://a.test/a/b` | **redirect** |
| path case | `https://a.test/Hotel` | `https://a.test/hotel` | **redirect** |
| parent traversal | `https://a.test/a/./b` | `https://a.test/a/b` | **redirect** |
| origin root | `https://a.test` | `https://a.test/` | **not a redirect** |
| host/scheme case | `HTTPS://A.TEST/x` | `https://a.test/x` | **not a redirect** |
| default port | `https://a.test:443/x` | `https://a.test/x` | **not a redirect** |
| fragment | `https://a.test/x#top` | `https://a.test/x` | **not a redirect** |
| non-default port | `https://a.test:8443/x` | `https://a.test/x` | **redirect** |
| query added/value/order/bare `?` | — | — | **redirect** |
| `file:`, `about:`, `chrome-error:`, relative, hostless | — | — | **fail closed** |

`TestNavigationResourceIdentity` pins the parsed identity fields (10 cases) and
the fail-closed set (7 inputs).

### 5.2 Strict boolean contract

| Test | Proves |
| --- | --- |
| `TestCrawlingConfigStrictBooleans` | omitted/`true`/`false` accepted; explicit `null`, string, number, object and array rejected |
| `TestCrawlingConfigRejectsExplicitNull` | rejection names the field and leaves no partially populated struct; omission still preserves the defaults |
| `TestCrawlingConfigStoredDocumentReload` | a persisted document with explicit values re-reads correctly; a persisted `null` is rejected |
| `TestCrawlingConfigRoundTripPreservesOverrides` | marshal→unmarshal keeps explicit `false` |
| `TestAddSourceRequestRejectsNullSourceCrawlingBooleans` (`services/api`) | the real `addSourceRequest` decoder rejects `null` and non-booleans, accepts omitted/`true`/`false` |

## 6. PostgreSQL gates (executed, disposable instance)

```text
fresh setup exit 0
schema=1.17  writer=verified-ownership-v1.17
capabilities=mcp-mutation-authorization-v1,source-crawl-attempt-v1
tables=78  handle_shared_entity_deletion FOR UPDATE=true  triggers=6

migration rehearsal from a simulated pre-RC2 function body:
pre_fix_lock=f  pass1=0  pass2=0  post_fix_lock=t
schema=1.17  writer=verified-ownership-v1.17  fixture row preserved=1  triggers=6
fresh md5 = migrated md5 = 6d33f3492e828ea1334de93baeba5c95  (BODY_IDENTICAL)

pg_dump --format=custom -> 677,047 bytes, 77 TABLE DATA entries
pg_restore -> rc4f_restore exit 0
rc4f_mig     : tables=78 sources=1 schema=1.17 fn=6d33f349…
rc4f_restore : tables=78 sources=1 schema=1.17 fn=6d33f349…
```

Full tagged integration suite (disposable PostgreSQL 17, runtime `crowler` role):

```bash
go test -p 1 -tags=integration \
  ./pkg/database/... ./pkg/crawler/... ./pkg/agent/... \
  ./services/api/... ./services/events/... -count=1 -timeout 40m
```

**PASS, exit 0**, 5 packages `ok`, no `--- SKIP` in the selected packages. This
includes the shared-object trigger regressions, the 25-iteration stress, the
legacy and fenced refresh suites, the keyword suppression windows, the event
retention janitor, the source-crawl attempt/result suites, the goal store and
observation suites, and MCP mutation persistence. Log:
`/tmp/opencode/rc4f-pg-full.log`.

## 7. Live Docker / browser acceptance

The acceptance stack was the repository's generated Compose file plus a local
override: PostgreSQL 17, `rc4f-api`, `rc4f-events`, `rc4f-engine-1`, the
published `zfpsystems/crowler-vdi` image and a controlled local HTTP fixture
reachable from both the crawler and the browser networks. Every assertion below
is a PostgreSQL query, not a log line.

### 7.1 Posture

```text
db=healthy api=200 events=200 selenium=200
schema=1.17  mode=legacy  handle_shared_entity_deletion FOR UPDATE=true
```

### 7.2 Phase A — strict booleans and path distinctions (global `true`, legacy)

| ID | Scenario | Result | Evidence |
| --- | --- | --- | --- |
| A0 | `POST /v1/source/add` with `"incognito": null` | **PASS** | HTTP **400**, `invalid source configuration: crawling_config.incognito must be a JSON boolean; null is not accepted (omit the key to inherit the default)`; no source row |
| A0 | same with `"follow_redirects": null` | **PASS** | HTTP **400**, same shape for the other field |
| A0 | explicit `true` / explicit `false` on both fields | **PASS** | HTTP 201 |
| A1 | default source, no overrides | **PASS** | `completed`, 1 page, fixture marker present |
| A2 | `incognito: true` | **PASS** | `completed`, 1 page |
| A3 | `incognito: false` | **PASS** | `completed`, 1 page |
| A4 | `/hotel` → 302 `/hotel/`, global `true`, source `follow_redirects: false` | **PASS** | `error`, 0 pages, trailing-slash body absent, `last_error = redirect not permitted: …/hotel?src=a4 landed on …/hotel/?src=a4` |
| A5 | `/hotel` → 302 `/hotel/`, global `true`, override omitted | **PASS** | `completed`, 1 page, trailing-slash body indexed |
| A6 | reverse `/motel/` → 302 `/motel`, source `false` | **PASS** | `error`, 0 pages, bare body absent |
| A7 | cross-origin redirect, source `false` | **PASS** | `error`, 0 pages |
| A8 | `unwanted_urls` with `retries_on_redirect: 2` | **PASS** | `error`, 0 pages, `failed to navigate … after 2 retries` |
| A9 | legacy `null:` pipeline source | **PASS** | created, then deleted by the pipeline; 0 `null:` sources, 0 `null:` pages |

### 7.3 Phase B — global `follow_redirects: false`

| ID | Scenario | Result | Evidence |
| --- | --- | --- | --- |
| B1 | `/hotel` → 302 `/hotel/`, override omitted (**the required regression**) | **PASS** | `error`, 0 pages, trailing-slash body absent, `redirect not permitted` |
| B2 | same source with explicit `follow_redirects: true` | **PASS** | `completed`, 1 page, trailing-slash body indexed |
| B3 | reverse `/motel/` → 302 `/motel`, explicit `false` | **PASS** | `error`, 0 pages |

B1 and B2 together prove the assertion is not vacuous: the same URL is refused
under the inherited policy and accepted under an explicit override.

### 7.4 Phase C — fenced claims

| ID | Scenario | Result | Evidence |
| --- | --- | --- | --- |
| C1 | fenced crawl with `incognito: false` | **PASS** | attempt `completed`, 1 page |
| C2 | fenced source with `follow_redirects: false` against `/hotel` → `/hotel/` | **PASS** | attempt `failed`, 0 pages |
| C3 | fenced `null:` source | **PASS** | source retained, attempt `failed`, 0 `null:` pages |

Mode was returned to `legacy` afterwards and the database confirmed it.

### 7.5 Phase D/E — RC2 shared-object integrity under `refresh_content: true`

Two sources crawl the same content hash, producing one `WebObjects` row linked
to both pages (`v1_objects=1`, `v1_links=2`).

| Step | Result | Evidence |
| --- | --- | --- |
| Legacy refresh of page A with new content | **PASS** | new object present, v1 object still present, v1 links reduced from 2 to **1**, page B keeps its link |
| Fenced refresh of page A with newer content | **PASS** | v3 object present, v2 (A's intermediate) removed, v1 preserved for page B; A has 1 link, B has 1 link |

Refreshing one page never deletes an object another page still references, in
either claim mode. This is the RC2-A/RC2-B guarantee re-proven after the RC4F
change.

### 7.6 Live browser tests with the real VDI

```bash
THECROWLER_E2E_WEBDRIVER=1 THECROWLER_E2E_WEBDRIVER_URL=http://127.0.0.1:14474/wd/hub THECROWLER_E2E_ISOLATION_FIXTURE_URL=http://rc4f-fixture:8000/target.html go test -tags integration ./pkg/vdi \
  -run 'TestIncognitoBrowserIntegration|TestSessionIsolationIntegration' -count=1
```

Result: **PASS** (5.3 s). Explicit `true` and explicit `false` both launch a
working session, and a normal session does not see a previous session's cookie
or `localStorage`.

The first attempt failed with `net::ERR_NAME_NOT_RESOLVED` because
`TestIncognitoBrowserIntegration` hard-coded the RC4 fixture alias
(`rc4-fixture`) and created an unused loopback listener, while
`TestSessionIsolationIntegration` honoured the documented environment override.
The test now uses one `liveFixtureURL()` helper for both paths and drops the
unused listener; the default (`rc4-fixture`) is unchanged, so the documented RC4
procedure still works. This is a test-harness fix, not a product change.

## 8. Information Seed Compose acceptance

**Executed for real, not deferred.** The repository's own Compose acceptance
harness was run against the RC4F tree:

```bash
python3 tests/integration/infoseed-compose/run.py --fixture-only
```

Result: **exit 0, 16/16 PASS, 0 FAIL** (`/tmp/opencode/rc4f-infoseed.log`). The
harness built its own service images with `--no-cache` from the RC4F source, used
a disposable Compose project, and tore down its containers, volumes and images
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

This closes the RC3/RC4 gap where the suite was reported as not run. `--fixture-only`
skips the optional live public-publisher smoke check, so the run is fully
deterministic and needs no external provider.

## 9. Named skips and NOT RUN

| Item | Status | Reason |
| --- | --- | --- |
| Hosted CI on the RC4F SHA | **BLOCKED** | The patch was not pushed; no `gh`/token is available and no push was authorised. The baseline run on `b15cb8f0` cannot certify RC4F code |
| Live Ollama inference / tool calling | **NOT RUN** | No Ollama endpoint or tool-capable model configured. Hermetic provider, capability and tool-round-trip tests pass |
| Live external MCP server | **NOT RUN** | Only local stdio and local TLS Streamable HTTP fixtures |
| MySQL runtime | **NOT RUN** | MySQL remains legacy-only by contract; no server configured |
| Hosted `mcp-authorization-e2e` | **BLOCKED** | `workflow_dispatch` only, absent from the protected default branch |
| Authorised live hotel target | **NOT RUN** | No authorised target; the controlled fixture exercises the same redirect code path |
| Production / shared database migration | **NOT RUN** | Forbidden by the work package |

## 10. Log index

| Log | Content |
| --- | --- |
| `/tmp/opencode/rc4f-full-unit-final.log`, `rc4f-race.log` | final unit and race suites |
| `/tmp/opencode/rc4f-vet-final.log`, `rc4f-veti-final.log` | vet, both tag sets |
| `/tmp/opencode/rc4f-static-final.log` | final builds, gofmt, `git diff --check` |
| `/tmp/opencode/rc4f-setup.log` | fresh PostgreSQL install |
| `/tmp/opencode/rc4f-mig1.log`, `rc4f-mig2.log` | migration passes |
| `/tmp/opencode/rc4f-mig.dump`, `rc4f-restore.log` | backup/restore |
| `/tmp/opencode/rc4f-build-*.log`, `rc4f-build-api2.log` | shipping image builds |
| `/tmp/opencode/rc4f-phase-a.out` … `rc4f-phase-d.out` | acceptance phases |
