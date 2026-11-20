# Release 2.3.0 RC4F implementation

Work package: `WP-REL230-RC4F`
Baseline: `b15cb8f027e896effb653622dd4977d906ab5438` (`develop`, clean)
Release: `v2.3.0` · Schema `1.17` · Writer `verified-ownership-v1.17`

This is a narrow correctness package. It fixes a defect in the redirect
comparison introduced by RC4, makes the source-configuration boolean contract
identical in the schema and on every decode path, and re-runs the release gates.
It adds no feature, no schema version and no public surface beyond the two
options RC4 already introduced.

## 1. Defect fixed: path distinctions were lost

RC4 compared navigations with:

```go
return strings.TrimRight(parsed.String(), "/")
```

That removed trailing slashes from the **whole URL**, so:

- `https://example.test/hotel` and `https://example.test/hotel/` compared equal;
- `https://example.test/a//` and `https://example.test/a` compared equal;
- `https://example.test//` compared equal to `https://example.test`.

HTTP servers may treat those as different resources. With
`follow_redirects: false`, a real `301`/`302` from `/hotel` to `/hotel/` was
therefore accepted and the redirected body was indexed as if it were the
requested page, contradicting the option's documented semantics and the release
notes.

## 2. The replacement contract

A navigation is compared as an HTTP **resource identity**, not as a string and
not with `path.Clean`:

| Component | Rule |
| --- | --- |
| Scheme | required; `http` or `https` only; compared lower-cased |
| Host | required; compared lower-cased (DNS is case-insensitive) |
| Port | the scheme's default (`80`/`443`) is dropped; any other port is preserved |
| Path | compared byte-for-byte as serialized; an empty path becomes `/` (how browsers serialize the origin root) |
| Query | compared byte-for-byte, including the bare `?` separator |
| Fragment | excluded (a browser never sends it to the server) |
| Anything else | different resource |

Consequences that are now explicit and tested:

- equal: `https://host` ≡ `https://host/`, `HTTPS://A.TEST/x` ≡ `https://a.test/x`,
  `https://a.test:443/x` ≡ `https://a.test/x`, `#a` ≡ `#b` for the same resource;
- different: `/hotel` vs `/hotel/`, `/a//b` vs `/a/b`, `%2F` vs `/`, `/Hotel` vs
  `/hotel`, `/a/./b` vs `/a/b`, any query difference or reordering,
  `https://a.test:8443/x` vs `https://a.test/x`, any host or scheme change;
- fail closed: an empty, relative or non-`http(s)` destination, including
  `file:`, `about:`, `chrome-error:` and a browser error page.

`path.Clean` is deliberately **not** used: it would collapse `..`, `.` and
duplicate separators, inventing equivalence between distinct request targets.

## 3. Code changes

| File | Change |
| --- | --- |
| `pkg/crawler/redirect_policy.go` | `normalizedNavigationURL` replaced by `navigationResource` + `parseNavigationResource` + `canonicalHost`; `redirectNotPermitted` now fails closed on unparsable or non-HTTP destinations |
| `pkg/crawler/redirect_policy_test.go` | decision matrix rebuilt around resource identity (37 cases) plus a direct identity test (10 valid, 7 fail-closed) |
| `pkg/config/types.go` | `CrawlingConfig.UnmarshalJSON` enforces omitted/`true`/`false` and rejects `null` and every other non-boolean |
| `services/api/console.go` | `extractAddSourceParams` now returns its decode error and classifies it as an invalid source configuration, instead of logging it and continuing with a zero-valued request |
| `pkg/config/source_crawling_overrides_test.go` | null is now expected to be rejected; added a stored-document reload test |
| `services/api/source_crawling_contract_test.go` | decodes the real `addSourceRequest` envelope and proves the API boundary rejects `null` |
| `tests/fixtures/rc4/serve.py` | `/hotel` → 302 → `/hotel/` and the symmetric `/motel/` → 302 → `/motel` |
| `docs/sources.md`, schema descriptions | document the strict boolean contract and the resource-identity rules |
| RC4 implementation/test reports | corrected where they described the old behaviour |

No change to `pkg/crawler/web.go`: the fix is entirely inside the comparison the
navigation path already calls, so the retry, `unwanted_urls`, restriction and
ownership behaviour is untouched.

### 3.1 A second defect found by the live gate: swallowed decode errors

The strict decoder only helps if somebody looks at its error. The acceptance run
showed `POST /v1/source/add` answering **201** for a body containing
`"incognito": null`, creating a source whose stored `config` was `NULL`:

```text
a0_null_incognito_http=201 body={"message":"Website inserted successfully with ID: 1"}
source 1 url=.../null-incognito-14702 config=NULL
```

`extractAddSourceParams` called `json.Unmarshal` and, on failure, only wrote a
debug line; the handler then proceeded with the zero value. The error was
visible in the log and nowhere else. This predates RC4F (it affects any malformed
add-source body, not just the new fields), but it defeated the strict contract,
so it is fixed here:

- `extractAddSourceParams` returns an `error`;
- a decode failure is wrapped with `errInvalidSourceConfig`, which the handler
  already maps to HTTP 400;
- the same treatment is applied to the two secondary re-unmarshal steps in the
  function, which had the identical log-and-continue shape.

The live run now answers 400 with a field-naming message, and no source row is
created.

## 4. Schema and runtime now agree

| Input | Schema | Decoder | API request | Stored reload |
| --- | --- | --- | --- | --- |
| key omitted | valid | nil (inherit) | accepted | accepted |
| `true` / `false` | valid | pointer | accepted | accepted |
| `null` | **invalid** (`type: boolean`) | **error** | **rejected** | **error** |
| `"false"`, `0`, `{}`, `[]` | invalid | error | rejected | error |

Only omission means "inherit". The rejection error names the field
(`crawling_config.incognito` or `crawling_config.follow_redirects`) so an
operator can fix the document. The decoder is implemented on
`CrawlingConfig.UnmarshalJSON`, which means every path that decodes a source
configuration — API create/update, stored-document reload, tests and any future
caller — gets the same contract; there is no second validation to drift.

## 5. Compatibility

- Behaviour of the two options for omitted/`true`/`false` is unchanged.
- The global `http_headers.follow_redirects` default, the private-browsing
  default, the claim mode, `refresh_content`, `null:` semantics and the MCP
  transport are untouched.
- The only intentional behaviour change is that a source document containing an
  explicit `null` for either option is now rejected instead of silently
  inheriting. No shipped example or test fixture used `null`; the RC4 example
  uses explicit values.
- Redirect acceptance becomes stricter in exactly the direction the option
  promises: a path-changing redirect is no longer accepted when the policy
  forbids following.

## 6. Browser error pages (investigated, not changed)

A navigation that Chromium cannot complete (`ERR_TOO_MANY_REDIRECTS`, a `file:`
destination) can leave Chromium's own error document loaded under the requested
URL. The crawler then indexes that document.

Trace: `getURLContent` calls `getWithTimeout`, which returns no error for these
cases (the WebDriver command succeeds and the error page is a normal document).
`wd.CurrentURL()` reports the requested URL, so the resource comparison correctly
sees "no redirect". The only remaining signals are the DOM and the localized
error text (`"This page isn't working"`, `"This site can't be reached"`), and
Chromium exposes no stable machine-readable navigation-failure flag through the
WebDriver results the crawler already consumes.

A text-based heuristic would be locale-dependent and would risk discarding
legitimate pages, and a CDP-based classification would be a wider change than
this correction package. **No change was made.** The behaviour is recorded as a
release limitation: a broken navigation may index the browser's error document
under the requested URL, with no target content and no credential leak. This is
pre-existing and independent of the redirect option; `follow_redirects: false`
does not and cannot detect a loop, because the loop ends on the requested URL
rather than on a different resource.

## 7. Scope boundaries

Not implemented, by design: ownership-epoch DDL, automatic goal resumption, MCP
mutation dispatch, fenced `null:` deletion, schema 1.18, new APIs and unrelated
optimisations.
