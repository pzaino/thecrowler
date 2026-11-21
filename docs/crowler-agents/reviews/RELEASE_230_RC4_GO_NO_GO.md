# Release 2.3.0 RC4 go / no-go

Work package: `WP-REL230-RC4`
Baseline: `d1c74666a9df58105816c1ebbccb4a2ba78ba76f`
Decision owner: the repository maintainer
Prepared: 2026-11-20
**This document does not tag, merge, publish, deploy or approve anything.**

## 1. Decision

# **GO FOR PRE-RELEASE TESTING**

Both requested source-scoped options are implemented, schema-validated, and
demonstrated against a real browser and a real PostgreSQL/VDI stack. No new P0 or
P1 defect is open, and the RC2 shared-object integrity guarantees still hold
after the change.

**Not granted: GO TO TAG / PUBLISH.** Hosted CI on the RC4 candidate SHA has not
run (the patch was not pushed), and that is the one gate the maintainer must
close before tagging. See §5.

## 2. What was delivered

| Item | Status |
| --- | --- |
| `crawling_config.incognito` in schema, types, merge, browser launch | **DONE** |
| `crawling_config.follow_redirects` in schema, types, merge, HTTP and browser paths | **DONE** |
| Defaults preserved (private browsing, global redirect policy) | **DONE** |
| Malformed values rejected; explicit `false` distinguished from absent | **DONE** |
| Deterministic unit tests (precedence, isolation, strict types, launch args, redirect matrix) | **DONE** |
| Live browser capabilities and cookie/localStorage isolation | **DONE** |
| Legacy + fenced Docker matrix, including RC2 shared-object refresh integrity | **DONE** |
| Source documentation, example document, test policy, runbook note | **DONE** |
| Information Seed harness module-proxy support + `--fixture-only` | **DONE** |
| Corrected the RC3 "Docker build stalls" finding | **DONE** (see §4) |

## 3. Residual defects

None new. The pre-existing items from RC3 keep their recorded disposition:

| ID | Item | Status |
| --- | --- | --- |
| P2-1 | `refresh_content: true` serialises page indexing | Unchanged, accepted/documented |
| P2-2 | Legacy page-indexing failure can leave `completed` with `last_error` | Unchanged, accept/document recommended |
| P3-1 | SQLite Events janitor `now()` | Unchanged, SQLite not a supported Events deployment |
| P3-2 | Cosmetic engine startup warnings | Unchanged |
| P3-3 | Finite event TTL | Unchanged, documented |

New observations from RC4, none of them blocking the two features:

- **Browser error pages are indexed.** A redirect loop and an unreachable
  `file:///` destination each produced a Chrome error page indexed under the
  requested URL. No target content and no `/etc/passwd` content were indexed
  (`a8_passwd_leak=0`). This is pre-existing navigation-failure behaviour, not a
  regression, and it is safe; it does affect index quality for broken sources.
- **The indexed URL stays the requested URL after a followed redirect.** With
  `follow_redirects: true`, the page content comes from the destination while
  `SearchIndex.page_url` remains the requested URL. Pre-existing indexing
  semantics; documented so no consumer assumes otherwise.

## 4. Release-acceptance infrastructure

- The default Docker module path is **not broken**. A plain
  `docker build --network=host` with no `GOPROXY` argument built the API image in
  248 s, the Events image in 272 s and the engine image in 322 s. RC3's
  "stalls" conclusion was a false negative caused by a 7-minute bound, a missing
  local `config.yaml` on the first attempt, and host CPU load. The RC3 documents
  were corrected rather than left standing.
- The Information Seed harness now honours `INFOSEED_GOPROXY` (falling back to
  `GOPROXY`) on every build block and supports `--fixture-only`, so it no longer
  requires an undocumented loopback proxy or public providers.
- `mcp-authorization-e2e` remains dispatch-only and unregistered on the default
  branch; the local procedure is unchanged and hosted execution is still
  classified separately.

## 5. The one remaining gate: hosted CI on the RC4 SHA

The RC4 patch contains Go code, tests, schema and documentation changes. The
baseline `d1c74666` is hosted-CI green (8/8), but **historical checks cannot
substitute for checks on the new code**.

Before tagging, the maintainer must:

1. push the reviewed RC4 patch through the normal branch/PR process;
2. confirm the `Go` and `postgres-integration` workflows pass on the resulting
   commit, including the existing shared-object integrity and event-retention
   steps;
3. record the run URLs for that exact SHA.

If the maintainer decides to tag without a hosted run, that must be an explicit,
recorded waiver — not an inference.

## 6. Deferred tests (explicitly not run)

| Item | Status | Reason |
| --- | --- | --- |
| Live Ollama inference / tool calling | **NOT RUN** | No Ollama endpoint or tool-capable model is configured in this environment; the hermetic provider and tool-calling tests pass |
| Live external MCP server | **NOT RUN** | Only local stdio and local TLS Streamable HTTP fixtures; no approved external endpoint |
| MySQL runtime | **NOT RUN** | No server; MySQL remains legacy-only by contract |
| `mcp-authorization-e2e` (hosted) | **BLOCKED** | Dispatch-only workflow absent from the protected default branch |
| Live hotel-site crawl | **NOT RUN** | No authorized live target; the controlled fixture exercises the same redirect/incognito paths |
| Information Seed Compose acceptance | see test report | The harness supports the documented module proxy and `--fixture-only`; the RC4 run is reported as executed or bounded in the test report |

These are evidence gaps, not defects. None of them blocks the two features: both
are covered by deterministic tests and by the real-browser Docker matrix.

## 7. Recommended next step

Push the patch, observe the two workflows on the new SHA, then run the
maintainer pre-release procedure with the two new source-option checks added to
the scenario list (see `RELEASE_230_PRE_RELEASE_RUNBOOK.md`).
