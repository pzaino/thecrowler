# Release 2.3.0 RC4 implementation

Work package: `WP-REL230-RC4`
Baseline: `d1c74666a9df58105816c1ebbccb4a2ba78ba76f` (`develop`, clean)
Release: `v2.3.0` · Schema `1.17` · Writer `verified-ownership-v1.17`

This document describes two new source-scoped crawling options, the exact
precedence and failure behaviour of each, and the compatibility implications. It
also records the release-acceptance infrastructure repairs delivered in the same
package.

## 1. Scope

Implemented:

- `crawling_config.incognito` — per-source private-browsing control.
- `crawling_config.follow_redirects` — per-source redirect-policy override.
- Schema, Go types, merge path, documentation and examples for both.
- Release-acceptance infrastructure: documented module-proxy support in the
  Information Seed harness, a fixture-only acceptance mode, and corrected
  guidance for the Docker image build time.

Deliberately unchanged:

- release `v2.3.0`, schema `1.17`, writer generation
  `verified-ownership-v1.17`, default claim mode `legacy`, global
  `refresh_content: false`;
- the global `http_headers.follow_redirects` default;
- the historical default private-browsing behaviour;
- MCP Streamable HTTP, which keeps its own stricter rule and never follows
  redirects;
- `null:` semantics, MySQL legacy-only behaviour, MCP mutation inertness and the
  absent asynchronous goal resumption.

## 2. Configuration contract

Both options are optional booleans under the existing `crawling_config` object of
the source configuration document:

```json
{
  "crawling_config": {
    "site": "https://example.com/",
    "source_type": "website",
    "incognito": false,
    "follow_redirects": true
  }
}
```

They are stored inside the existing source configuration document. There is no
database schema change, no migration and no new table or column.

### 2.1 Type strictness

| Input | Result |
| --- | --- |
| key omitted | default behaviour preserved |
| `true` / `false` | honoured |
| `null` | treated as absent; the default is preserved |
| `"false"`, `0`, `{}`, `[]` | source document rejected at decode time |

A malformed value can therefore never silently switch a site out of private
browsing or silently enable redirect following. `null` is explicitly documented
as "not set" rather than "false", because treating it as `false` would change
browsing behaviour for a document that never expressed an intent.

### 2.2 Incognito precedence

| Source value | Effective |
| --- | --- |
| omitted (`nil`) | private (historical default) |
| `true` | private |
| `false` | normal session |

There is intentionally **no global `crawler.incognito` key**: the effective value
lives on an unexported-to-serialisation field (`json:"-"`) that can only be
populated by the per-source merge. A single source therefore cannot change the
process-wide default for any other source, by construction rather than by
convention.

### 2.3 Redirect precedence

| Global `http_headers.follow_redirects` | Source value | Effective |
| --- | --- | --- |
| `false` | omitted | `false` |
| `false` | `true` | `true` |
| `true` | `false` | `false` |
| `true` | omitted | `true` |

The override is folded into the per-source merged configuration
(`HTTPHeaders.FollowRedirects`), so every redirect-aware consumer of that source
reads one value. The global configuration object is never mutated.

## 3. Browser launch change

`pkg/vdi/vdi.go` previously appended `--incognito` unconditionally for Chrome and
Chromium. It now appends the flag only when the merged configuration asks for a
private session:

```go
if crawler.EffectiveIncognito() {
    args = append(args, "--incognito")
}
```

Details that matter:

- The decision is read from the per-source merged `cfg.Crawler` that
  `ConnectVDI` already receives, so it flows through
  `ProcessContext.LoadSourceConfiguration` → `cfg.CombineConfig` →
  `ConnectToVDI` → `vdi.ConnectVDI` → `buildLaunchArgs` → Chrome capabilities.
  No new plumbing and no new global state were introduced.
- Firefox is untouched: the flag lives inside the Chrome/Chromium branch and the
  Firefox session never receives it.
- The remaining Chrome hardening flags (`--no-first-run`,
  `--disable-client-side-phishing-detection`, `--disable-component-update`,
  media/geolocation/notification flags) are unchanged when the flag is removed,
  so a normal session is not a *less* hardened session.
- Reconnection and reset paths call the same `ConnectVDI`/`buildLaunchArgs`, so a
  session recreated after a WebDriver failure keeps the same per-source value.

### 3.1 Session isolation in normal mode

Disabling private browsing must not let one source read another source's state.
The VDI already creates an isolated browser profile per session; the release
gate verifies it rather than assuming it: a session writes a cookie and a
`localStorage` entry, quits, and a second normal session on the same VDI must
observe neither. The observed result is in the RC4 test report.

The builder deliberately does **not** force a `--user-data-dir`: Selenium manages
the per-session profile, and overriding it would risk sharing a directory between
concurrent sources. Isolation is proven by behaviour, and a `--user-data-dir`
regression test fails the build if one is ever introduced.

## 4. Redirect handling

### 4.1 What each path actually does

| Path | Mechanism | Effect of the override |
| --- | --- | --- |
| HTTP metadata collection (`httpinfo`) | Go `http.Client` with `FollowRedirects` | Follows or records the `3xx` according to the effective value |
| Browser navigation (initial page, recursive links, fenced crawls) | Selenium `Get` + `wd.CurrentURL()` | The browser always performs the HTTP redirect; the crawler **accepts or refuses** the result |
| MCP Streamable HTTP | SDK transport | Unchanged: redirects are still rejected outright |

**Investigation result.** Before this package the global
`http_headers.follow_redirects` value reached only the HTTP metadata client. It
had no effect on Selenium navigation, because the browser follows redirects
itself and nothing compared the requested URL with the final one except the
`unwanted_urls` check. A source that "did not load" because it redirected was
therefore not being governed by the global flag at all.

### 4.2 Acceptance, not interception

`follow_redirects: false` cannot stop the browser from issuing the follow. The
implementation is explicit about this and enforces the part it can:

1. Navigate to the requested URL.
2. Read the final URL from the browser.
3. Compare the requested and final URLs after collapsing only cosmetic
   differences (URL fragment, trailing slash, surrounding whitespace — a browser
   reports `https://host` as `https://host/` with no HTTP redirect involved).
4. If the policy forbids redirects and the destination differs, treat the
   attempt as a failed navigation: retry up to `retries_on_redirect` times, then
   fail the page with `redirect not permitted: <requested> landed on <final>`.

The page is never indexed under the requested URL when it actually delivered a
different resource, and the redirect destination is never used to bypass the
source's own restrictions, `unwanted_urls` patterns, block patterns, or
ownership checks — all of those still run against the final URL.

### 4.3 Safety properties

- Bounded retries: the existing `retries_on_redirect` budget, default 5, is the
  only retry loop; there is no new unbounded loop.
- Redirect loops: the browser terminates the loop and reports a navigation
  error, which is handled by the existing navigation-failure path. The Go HTTP
  client used for metadata collection also bounds the chain (10 by default) and
  returns `stopped after too many redirects`.
- Unsupported schemes: an `ftp://`, `file://` or otherwise unsupported redirect
  target surfaces as a browser navigation or fetch failure and never produces an
  indexed page.
- Recovery: a refused redirect retries the **original** URL, exactly like the
  existing unwanted-URL handling, so a site that redirects only intermittently
  can still succeed.
- Cross-origin credentials: browser navigation is a top-level GET; no
  authorisation header is forwarded by the crawler. The HTTP metadata client
  keeps the existing `DropAuthOnRedirect` behaviour, dropping `Authorization` and
  `Cookie` when the host changes.

### 4.4 Known limitation

Redirect interception before the request is not possible with Selenium
navigation. A deployment that must guarantee the browser never contacts a
redirect destination needs network-level egress control; the crawler guarantees
only that the destination is not accepted, indexed or treated as the requested
page. This limitation is also stated in `docs/sources.md`.

## 5. Schema and compatibility

- `schemas/crowler-source-config-schema.json`: two new `"type": "boolean"`
  properties under `crawling_config`, whose `additionalProperties` remains
  `false`.
- `pkg/config.CrawlingConfig`: `*bool` fields, so absent and explicit `false`
  are distinguishable.
- `pkg/database.SourceCrawlingConfig`: mirror fields for the database-side
  parser.
- `pkg/config.Crawler.Incognito`: effective value, `json:"-"` so it cannot be
  set from `config.yaml`.
- `examples/source-crawling-options.json`: a validated example document.

Backward compatibility: every existing source configuration continues to parse
and behaves exactly as before, because both options default to the previous
behaviour. No global default changed.

## 6. Acceptance infrastructure repairs

- The Information Seed Compose harness now honours `INFOSEED_GOPROXY` (falling
  back to `GOPROXY`) and injects it as the documented `GOPROXY` build argument on
  every build block, including the fixture and probe images. No checksum
  verification is disabled.
- The same harness gained `--fixture-only`, which runs the 16 deterministic
  cases and skips the optional external publisher smoke check, so the suite no
  longer depends on public providers being reachable.
- The Docker image build guidance was corrected: the default module path is not
  broken in this environment, it simply needs more than a few minutes. The
  `GOPROXY` argument remains the documented option for restricted networks.
