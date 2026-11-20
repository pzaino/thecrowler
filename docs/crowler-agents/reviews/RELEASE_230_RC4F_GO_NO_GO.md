# Release 2.3.0 RC4F go / no-go

Work package: `WP-REL230-RC4F`
Baseline: `b15cb8f027e896effb653622dd4977d906ab5438`
Decision owner: the repository maintainer
Prepared: 2026-11-20
**This document does not tag, merge, publish, deploy or approve anything.**

## 1. Decision

# **GO FOR PRE-RELEASE TESTING**

# **TAG / PUBLISH: BLOCKED**

RC4F is a required correctness fix. The RC4 commit `b15cb8f0` is hosted-green
(8/8) but contains the redirect-comparison defect described below, so it must not
be tagged. RC4F fixes it and passes every local gate, including a real browser
regression for the exact `/hotel` → `/hotel/` case.

The tag remains blocked on one thing only: **hosted CI on the RC4F commit**. The
RC4F patch was not pushed, and a green run on `b15cb8f0` cannot certify code it
does not contain.

## 2. What RC4F fixes

| Defect | Severity | Status |
| --- | --- | --- |
| `normalizedNavigationURL` stripped trailing slashes from the whole URL, so `https://host/hotel` and `https://host/hotel/` compared equal and a real 301/302 between them was accepted under `follow_redirects: false` | **High** for the new option's advertised semantics | **FIXED** |
| The JSON schema declares the two new properties as `type: boolean`, but the decoder accepted an explicit `null` as "inherit" | **Medium**: schema and runtime disagreed | **FIXED**: `null` is now rejected everywhere |
| `POST /v1/source/add` logged a body decode error and continued with a zero-valued request, so a malformed document (including `null` on the new fields) created a source with an empty config and answered **201** | **High**: silent misconfiguration, pre-existing | **FIXED**: the decode error is returned and mapped to HTTP 400 |

Both fixes are covered by unit tests, an API-boundary test, and (for the
redirect case) a real Selenium/VDI regression against a dedicated fixture.

## 3. The redirect contract now

Two navigations are the same resource only when all of the following match:

- scheme, lower-cased, `http` or `https`;
- host, lower-cased;
- port, with the scheme's default (`80`/`443`) normalized away;
- path, byte-for-byte, with an empty path treated as `/` (origin root only);
- query, byte-for-byte, including a bare `?`.

The fragment is ignored because the browser never sends it. Everything else is a
redirect: `/hotel` vs `/hotel/`, `/a//b` vs `/a/b`, `%2F` vs `/`, path case,
`/a/./b`, query changes, non-default ports, host or scheme changes. A destination
that is not an absolute `http(s)` URL — `file:`, `about:`, `chrome-error:`, a
relative URL, a hostless URL — fails closed.

`path.Clean` is deliberately not used, because it would invent equivalence
between distinct request targets.

## 4. What was re-validated

The full RC4 release matrix was re-run on the fixed tree, not just the new unit
tests: local unit/race/vet/builds, disposable PostgreSQL 17 with the 1.17
migration and backup/restore, authentic shipping images, and the Docker/VDI
matrix covering defaults, both incognito modes, session isolation, the redirect
precedence matrix including the new path-slash cases, cross-origin, loops,
forbidden destinations, `unwanted_urls`, legacy and fenced `null:`, and the RC2
shared-object refresh integrity in both claim modes. The Information Seed
Compose acceptance suite was also executed for real (16/16 PASS), closing a gap
carried since RC3.

Exact results are in `RELEASE_230_RC4F_TEST_REPORT.md`.

## 5. Remaining issues and known limitations

| Item | Severity | Disposition |
| --- | --- | --- |
| Browser error documents can be indexed under the requested URL for a navigation Chromium cannot complete (redirect loop, `file:` destination) | P2, pre-existing | **Not changed.** Chromium exposes no stable machine-readable navigation-failure flag through the results the crawler consumes; the remaining signals are localized DOM text. A heuristic would be unreliable and could discard legitimate pages. Recorded as a release limitation: no target content and no credential leak, but the error document may appear in the index |
| `follow_redirects: false` cannot detect a redirect loop | P3, by design | A loop ends on the requested URL, not on a different resource; only the browser can abort it. Documented |
| `refresh_content: true` serialises page indexing | P2-1 from RC3 | Unchanged, accepted/documented |
| Legacy page-indexing failure can leave `completed` with `last_error` | P2-2 from RC3 | Unchanged, accept/document recommended |
| SQLite Events janitor `now()` | P3-1 | Unchanged, SQLite not a supported Events deployment |
| Cosmetic engine startup warnings | P3-2 | Unchanged |
| Finite event TTL | P3-3 | Unchanged, documented |

No P0 or P1 issue is open.

## 6. Evidence gaps the maintainer must accept or close

| Gap | Status | Release effect |
| --- | --- | --- |
| Hosted CI on the RC4F commit | **BLOCKED** | Must be green before tagging, or explicitly waived |
| Information Seed Compose acceptance (`--fixture-only`) | **PASS, 16/16** | Closed: the RC3/RC4 "not run" gap no longer applies |
| Live Ollama inference / tool calling | **NOT RUN** | No endpoint/model; hermetic coverage passes |
| Live external MCP server | **NOT RUN** | Local fixtures only |
| MySQL legacy runtime | **NOT RUN** | Legacy-only by contract, no server |
| Hosted `mcp-authorization-e2e` | **BLOCKED** | Dispatch-only, absent from the protected default branch |
| Authorized live hotel target | **NOT RUN** | No authorized target; the local fixture covers the same code path |

## 7. Suggested release-note wording

> Two source-scoped options are available in 2.3.0:
> `crawling_config.incognito` and `crawling_config.follow_redirects`. Both are
> optional strict booleans; omit the key to inherit the default. An explicit
> `null` is rejected.
>
> `follow_redirects: false` refuses a navigation that lands on a different
> resource. Only standards-equivalent spellings are treated as the same
> resource: scheme/host case, the default port, and an empty path versus `/` at
> the origin root. A slash difference on a real path (`/hotel` versus
> `/hotel/`), repeated slashes, escaped separators, path case and any query
> difference count as redirects. The browser still performs the redirect on the
> network before the crawler refuses the result; this option controls
> acceptance, not interception.
>
> Known limitation: a navigation Chromium cannot complete (for example a
> redirect loop) may leave the browser's error document indexed under the
> requested URL. No target content is indexed and no credentials leak.

## 8. Maintainer sign-off items

1. Accept the RC4F fixes and the strict `null` contract.
2. Accept the error-document limitation, or schedule a separate change.
3. Push RC4F and confirm `Go` and `postgres-integration` green on that exact
   SHA; or record an explicit waiver.
4. Accept or close the evidence gaps in §6.
5. Confirm the release scope and deferrals: no asynchronous goal resumption, no
   MCP mutation dispatch, no ownership-epoch DDL, no fenced `null:` deletion,
   production `fenced` mode still operator-gated.
