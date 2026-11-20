# Release 2.3.0 RC4 test report

Work package: `WP-REL230-RC4`
Baseline: `d1c74666a9df58105816c1ebbccb4a2ba78ba76f`
Worktree: `/tmp/opencode/thecrowler-rc4`
Original checkout: `/home/pzaino/workspace/github/thecrowler`, clean before and
after
Date: 2026-11-20
Release: `v2.3.0` · Schema `1.17` · Writer `verified-ownership-v1.17`

Evidence classes: **executed**, **live browser**, **integration-only**,
**NOT RUN**. A skipped test is never counted as a pass.

## 1. Environment

| Item | Value |
| --- | --- |
| Host | Linux x86_64, 32 CPUs |
| Go | `go1.27.4` (`go.mod` requires `1.26.6`) |
| Docker Engine | `29.9.2-ce`; Compose `5.7.1` |
| Disposable PostgreSQL | `postgres:17` (17.12), container `rc4-pg`, `127.0.0.1:55957` |
| Acceptance stack | Compose project `rc4gate`, PostgreSQL 17, `rc4-api`, `rc4-events`, `rc4-engine-1`, published VDI, local fixture |
| VDI image | `zfpsystems/crowler-vdi:latest` |
| Original checkout | untouched, `HEAD` unchanged |

No maintainer container, volume or database was touched. Every resource created
here is prefixed `rc4` and was removed at the end.

## 2. Docker image build (executed)

All three shipping images were built from the repository Dockerfiles with the
**default module path**: no `GOPROXY` build argument and no proxy workaround.

| Image | Dockerfile | Build exit | Elapsed | Image ID |
| --- | --- | --- | --- | --- |
| `rc4-api:ship` | `Dockerfile.searchapi` | 0 | 248 s | `sha256:d53abf7b0d4895f93488d272102b722b432985c452fd1414d678c450807f85ce` |
| `rc4-events:ship` | `Dockerfile.events` | 0 | 272 s | `sha256:a2b32d72b8fb2540178aeca3b5b589597165caef26e43ecbf01f7098544fe159` |
| `rc4-engine:ship` | `Dockerfile.thecrowler` | 0 | 322 s | `sha256:e7bbdd7b2f34b389374abd95e5a84b97d77818b65283135d0927792885828b35` |

Logs: `/tmp/opencode/rc4-build-normal.log`,
`/tmp/opencode/rc4-build-rc4-events:ship.log`,
`/tmp/opencode/rc4-build-rc4-engine:ship.log`.

### 2.1 Correction of the RC3 finding

RC3 recorded that a plain `docker build` "stalls during Go module download". That
was a false negative. With the default module path and a realistic timeout the
same Dockerfile completed in 248 s. Contributing factors in RC3: a 7-minute
bound, a missing local `config.yaml` on the first attempt, and heavy unrelated
host CPU load. The silence in the build log is normal compilation after the
download phase, not a hang. `RELEASE_230_RC3_TEST_REPORT.md` and
`RELEASE_230_RC3_CI_AND_RELEASE_GATE.md` were corrected in this package rather
than left standing.

Module integrity is unchanged in every build: `GOSUMDB` untouched, `go.sum`
unmodified, no `GOINSECURE`/`GOPRIVATE`/`GONOSUMDB`.

## 3. Local regression gates (executed)

| Command | Result | Log |
| --- | --- | --- |
| `go test ./... -count=1 -timeout 90m -p 1` | **PASS, exit 0, 35 packages ok** | `/tmp/opencode/rc4-full-unit.log` |
| `go test -race ./pkg/agent/... ./pkg/database/... ./pkg/crawler/... ./cmd/thecrowler/... ./services/events/... -count=1` | PASS, exit 0, no data race (`agent 195.6s`, `database 27.2s`, `crawler 5.4s`, `cmd 1.6s`, `events 6.3s`) | `/tmp/opencode/rc4-race.log` |
| `go vet ./...` | PASS | `/tmp/opencode/rc4-static.log` |
| `go vet -tags=integration ./pkg/agent ./pkg/database ./pkg/crawler ./services/events` | PASS | `/tmp/opencode/rc4-static.log` |
| `CGO_ENABLED=0 go build ./...` | PASS | `/tmp/opencode/rc4-static.log` |
| `CGO_ENABLED=1 go build ./...` | PASS | `/tmp/opencode/rc4-static.log` |
| `gofmt -l pkg cmd services` | clean, no output | `/tmp/opencode/rc4-static.log` |
| `git diff --check` | PASS | `/tmp/opencode/rc4-static.log` |
| `actionlint` v1.7.7 on `.github/workflows/*.yml` | PASS, exit 0 | inline |
| 14 shipped manifests × (`lint` + `validate --strict`) | 28/28 PASS, 0 failed, 0 skipped | inline |

## 4. Source-option unit coverage (executed)

### 4.1 Configuration (`pkg/config/source_crawling_overrides_test.go`)

| Test | Proves |
| --- | --- |
| `TestSourceCrawlingOverrideDefaultsPreserved` | omitted source value keeps the private default and the global redirect value |
| `TestSourceCrawlingOverrideIncognito` | explicit `true` and explicit `false`, with `false` distinguishable from absent |
| `TestSourceCrawlingOverrideFollowRedirectsMatrix` | the four-row precedence table; the base configuration is never mutated |
| `TestSourceCrawlingOverrideIsolation` | two sources merged from one base keep independent values and do not share a pointer |
| `TestCrawlingConfigStrictBooleans` | omitted/true/false/null/types: strings, numbers, objects and arrays are rejected |
| `TestCrawlingConfigNullKeepsPrivateBrowsing` | JSON `null` is "not set" and preserves the private default |
| `TestCrawlingConfigRoundTripPreservesOverrides` | marshal→unmarshal→merge keeps an explicit `false` |

### 4.2 Browser launch (`pkg/vdi/incognito_launch_test.go`)

| Test | Proves |
| --- | --- |
| `TestBuildLaunchArgsIncognitoDefaultPreserved` | `--incognito` present exactly once by default for Chrome and Chromium |
| `TestBuildLaunchArgsIncognitoExplicitTrue` | present for explicit `true` |
| `TestBuildLaunchArgsIncognitoExplicitFalse` | absent for explicit `false`, and the other Chrome hardening flags survive |
| `TestBuildLaunchArgsIncognitoNeverForFirefox` | Firefox never receives the Chrome-only flag, for any value |
| `TestBuildLaunchArgsConcurrentSourcesIsolated` | 400 concurrent builder calls with opposite values never cross-contaminate |
| `TestBuildLaunchArgsNonIncognitoStillHardened` | a normal session keeps the automation-hygiene flags and never forces a shared `--user-data-dir` |

### 4.3 Redirect policy (`pkg/crawler/redirect_policy_test.go`)

| Test | Proves |
| --- | --- |
| `TestRedirectNotPermittedDecisionMatrix` | same URL, fragment, trailing slash and root slash are not redirects; same-origin, cross-origin and scheme-change redirects are refused; empty/unparsable destinations fail closed |
| `TestNormalizedNavigationURL` | the cosmetic-equivalence rules used by the comparison |
| `TestRedirectsFollowingPermittedReadsMergedConfig` | the browser path reads the same effective value for all four precedence rows |
| `TestRedirectsFollowingPermittedNilContext` | defensive nil-context guard |

## 5. Live browser evidence (executed)

Against the published VDI over the WebDriver endpoint
(`THECROWLER_E2E_WEBDRIVER=1`, `pkg/vdi`, `-tags integration`):

| Test | Result | Meaning |
| --- | --- | --- |
| `TestIncognitoBrowserIntegration/explicit_true_launches_a_working_private_session` | **PASS** | the production capability builder produces a real private session that navigates and renders the fixture |
| `TestIncognitoBrowserIntegration/explicit_false_launches_a_working_normal_session` | **PASS** | the same builder produces a working normal session |
| `TestSessionIsolationIntegration` | **PASS** | a normal session writes a cookie and a `localStorage` entry, quits, and a second normal session on the same VDI observes neither |

`TestSessionIsolationIntegration` is the compensating control for disabling
private browsing: sessions are isolated by the VDI's per-session profile, not by
the incognito flag.

Limitation recorded honestly: the W3C WebDriver endpoint does not expose
per-session command-line arguments, so the exact `--incognito` presence is
asserted by the unit tests on the real builder, and the live test asserts working
sessions in both modes.

## 6. Docker / browser acceptance matrix (executed)

Isolated `rc4gate` stack: PostgreSQL 17, `rc4-api`, `rc4-events`, `rc4-engine-1`,
published VDI, local fixture with deterministic redirect endpoints. Every
assertion below is a PostgreSQL query or a fixture/HTTP observation.

### 6.1 Defaults and incognito (global redirects `true`, legacy claims)

| ID | Scenario | Result | Evidence |
| --- | --- | --- | --- |
| A1 | No source overrides | **PASS** | source `completed`, 1 page indexed, home marker present |
| A2 | `incognito: true` | **PASS** | `completed`, 1 page |
| A3 | `incognito: false` | **PASS** | `completed`, 1 page |
| A4 | State isolation between sources | **live browser test (§5)** | cookie/`localStorage` do not cross sessions |

### 6.2 Redirect policy

| ID | Scenario | Result | Evidence |
| --- | --- | --- | --- |
| A5 | Global `true`, source `follow_redirects: false` | **PASS** | `status=error`, 0 pages, `last_error = redirect not permitted: …/r/302?x=3 landed on …/target.html` |
| A6 | Same-origin 301, 302, 303, 307, 308 | **PASS** | all five `completed` with 1 page each; indexed content is `RC4 Target / marker rc4-target-v1`, proving the redirect was followed |
| A7 | Redirect loop | **PASS (bounded)** | navigation failed; Chrome's `ERR_TOO_MANY_REDIRECTS` page was indexed under the requested URL; no loop and no unbounded retry |
| A8 | `file:///etc/passwd` destination | **PASS (safe)** | no page content from `/etc/passwd` (`a8_passwd_leak=0`); Chrome's "can't be reached" page was indexed |
| A9 | `unwanted_urls` with `retries_on_redirect` | **PASS** | `status=error`, 0 pages, `last_error = failed to navigate … after 2 retries` |
| B1 | Global `false`, source `follow_redirects: true` | **PASS** | `completed`, 1 page — the override permits the redirect |
| B2 | Global `false`, source omitted | **PASS** | `status=error`, 0 pages, `redirect not permitted` |
| B3 | Cross-origin redirect, source omitted under global `false` | **PASS** | `status=error`, 0 pages |

### 6.3 `null:` protocol

| ID | Scenario | Result | Evidence |
| --- | --- | --- | --- |
| A10 | Legacy `null:` | **PASS** | source created (201), then deleted by the pipeline; 0 `null:` sources left; 0 `null:` pages |
| C3 | Fenced `null:` | **PASS** | source retained (1), newest attempt `failed`, 0 `null:` pages |

### 6.4 Fenced claims and RC2 refresh integrity

| ID | Scenario | Result | Evidence |
| --- | --- | --- | --- |
| C1 | Fenced crawl with `incognito: false` | **PASS** | attempt `completed`, 1 page indexed |
| C2 | Fenced crawl of a redirecting source with `follow_redirects: true` | **PASS** | attempt `completed` |
| D | Legacy refresh of one of two hash-shared pages | **PASS** | before: 1 object with 2 links, 1 object per page; after refreshing page A: new object present, old object still present with exactly 1 link (page B), each page still exactly 1 object |
| E | Fenced refresh of the same page | **PASS** | new object present, the intermediate object removed (its only link was A's), the page-B object preserved with its link, each page exactly 1 object |

Scenario D/E is the RC2 shared-object guarantee re-proven after the RC4 change:
refreshing one page never deletes an object another page still references.

## 7. PostgreSQL gates (executed, disposable instance)

```text
fresh setup exit 0
schema=1.17  writer=verified-ownership-v1.17
capabilities=mcp-mutation-authorization-v1,source-crawl-attempt-v1
tables=78  handle_shared_entity_deletion FOR UPDATE=true  triggers=6

migration rehearsal from a simulated pre-RC2 function body:
pre_fix_lock=f  migration pass1=0  pass2=0  post_fix_lock=t
schema=1.17  writer=verified-ownership-v1.17  fixture row preserved=1  triggers=6
fresh md5 = migrated md5 = 6d33f3492e828ea1334de93baeba5c95  (BODY_IDENTICAL)

pg_dump --format=custom -> 677,040 bytes, 77 TABLE DATA entries
pg_restore -> rc4_restore exit 0
rc4_mig     : tables=78 sources=1 schema=1.17 fn=6d33f349… triggers=6
rc4_restore : tables=78 sources=1 schema=1.17 fn=6d33f349… triggers=6
```

Full tagged integration suite:

```bash
go test -p 1 -tags=integration \
  ./pkg/database/... ./pkg/crawler/... ./pkg/agent/... \
  ./services/api/... ./services/events/... -count=1 -timeout 40m
```

**PASS, exit 0**, 5 packages ok. Log: `/tmp/opencode/rc4-pg-full.log`.

## 8. Information Seed harness (executed, deterministic)

The harness now honours `INFOSEED_GOPROXY` (falling back to `GOPROXY`) and
supports `--fixture-only`. Verified by importing the harness module and
exercising the helper:

```text
unset                          -> {}
INFOSEED_GOPROXY=<url>         -> {'GOPROXY': '<url>'}
GOPROXY=<url> fallback         -> {'GOPROXY': '<url>'}
attach to a dict build block   -> build.args['GOPROXY'] set
attach to a string build block -> normalized to {context, args}
```

The same module parse confirms `--fixture-only` is registered. A full Compose
acceptance run (which builds five images) was **NOT RUN** in this package; see
§9.

## 9. Named skips and NOT RUN

| Item | Status | Reason |
| --- | --- | --- |
| Hosted CI on the RC4 candidate SHA | **NOT RUN** | The patch was not pushed; the baseline `d1c74666` is hosted-green (8/8) but historical checks cannot substitute for the new code |
| Live Ollama inference / tool calling | **NOT RUN** | No Ollama endpoint or tool-capable model configured; hermetic provider tests pass |
| Live external MCP server | **NOT RUN** | Only local stdio and local TLS Streamable HTTP fixtures |
| MySQL runtime | **NOT RUN** | No server; MySQL remains legacy-only by contract |
| `mcp-authorization-e2e` (hosted) | **BLOCKED** | Dispatch-only workflow absent from the protected default branch |
| Information Seed Compose acceptance (full run) | **NOT RUN** | Harness capability validated deterministically (§8); a full run builds five images and was out of the remaining time budget. It is no longer blocked by the module-download workaround |
| Live hotel-site crawl | **NOT RUN** | No authorized live target; the controlled fixture exercises the same incognito and redirect paths |

## 10. Acceptance fixtures (in the repository)

The redirect endpoints and the two pages used by the refresh scenarios are
repository-controlled, so the matrix is reproducible without any external
target:

- `tests/fixtures/rc4/serve.py` — deterministic server with `Cache-Control:
  no-store`, same-origin redirects for 301/302/303/307/308, a cross-host
  redirect, a loop, an unsupported `file://` and `ftp://` destination, a bounded
  chain, plus a `/page` endpoint whose body is read from `a.body`/`b.body` so a
  refresh scenario can change content between crawls.
- `tests/fixtures/rc4/index.html`, `target.html`, `a.body`, `b.body`.
- `docker-compose.rc4.yml` — the isolated acceptance override (container names
  `rc4-*`, loopback ports, `rc4-*:ship` images, fixture attached to both the
  crawler and VDI networks with the two host aliases).

The matrix also needs a second engine configuration with the global policy
flipped. It is produced from the first with a one-line change:

```bash
sed 's/^  follow_redirects: true/  follow_redirects: false/' config.yaml > config-redirectoff.yaml
```

`config.yaml`, `.env`, `docker-compose.yml` and `config-redirectoff.yaml` are
local, git-ignored deployment inputs and are not part of the patch.

## 11. Log index

| Log | Content |
| --- | --- |
| `/tmp/opencode/rc4-build-normal.log` | default-path API image build (248 s) |
| `/tmp/opencode/rc4-build-rc4-events:ship.log`, `rc4-build-rc4-engine:ship.log` | Events and engine builds |
| `/tmp/opencode/rc4-full-unit.log` | full unit suite |
| `/tmp/opencode/rc4-race.log` | race suite |
| `/tmp/opencode/rc4-static.log` | vet, builds, gofmt, diff-check |
| `/tmp/opencode/rc4-pg-full.log` | full tagged PostgreSQL suite |
| `/tmp/opencode/rc4-mig1.log`, `rc4-mig2.log`, `rc4-mig.dump`, `rc4-restore.log` | migration and backup/restore |
| `/tmp/opencode/rc4-phase-a.out`, `rc4-phase-b.out`, `rc4-phase-c.out`, `rc4-phase-d.out` | acceptance phases |
| `/tmp/opencode/rc4-up-core.log`, `rc4-down.log` | stack lifecycle |
