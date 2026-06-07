# The CROWler API

The CROWler Search API exposes public search endpoints, optional console
administration endpoints, API documentation endpoints, and optional plugin
routes. The tables below intentionally list only routes registered by
`initAPIv1`; every route documented here is covered by a route/handler test so
future documentation changes cannot describe endpoints that are not mounted.

## Operational and documentation endpoints

| Method | Endpoint | Purpose |
| --- | --- | --- |
| GET | `/v1/health` | Liveness check. The `/v1/health/` form is also registered for compatibility. |
| GET | `/v1/ready` | Readiness check. The `/v1/ready/` form is also registered for compatibility. |
| GET | `/v1/openapi.json` | Runtime-generated OpenAPI 3.0.3 document when API docs are enabled. |
| GET | `/v1/docs` | JSON endpoint index when API docs are enabled. |

## Search endpoints

Search endpoints are enabled unless `api.disable_default` is true. They accept a
`q` query parameter for GET requests and the equivalent JSON body for POST
requests through the shared search handler. Use `limit` and `offset` for
pagination where the backing handler supports them. The `q` parameter supports
CROWler dorking operators such as `title:admin` and logical OR expressions such
as `title:admin||administrator`.

| Method | Endpoint | Result focus |
| --- | --- | --- |
| GET/POST | `/v1/search/general` | General search results from the CROWler index. |
| GET/POST | `/v1/search/netinfo` | Network information for matching sites. |
| GET/POST | `/v1/search/httpinfo` | HTTP information, detected technologies, and SSL information. |
| GET/POST | `/v1/search/screenshot` | Screenshot search results. |
| GET/POST | `/v1/search/webobject` | Web object search results. |
| GET/POST | `/v1/search/correlated_sites` | Sites correlated with the requested terms. |
| GET/POST | `/v1/search/collected_data` | Collected/scraped data related to the requested terms. |
| GET | `/v1/search/correlated_sources` | Typed PostgreSQL correlated-source search. |
| GET | `/v1/search/pages` | Typed PostgreSQL page search. |
| GET | `/v1/search/scraped_data` | Typed PostgreSQL scraped-data search. |
| GET | `/v1/search/scraped_data_field` | Typed PostgreSQL scraped-data field search. |
| GET | `/v1/search/artifacts` | Typed PostgreSQL artifact search. |
| GET | `/v1/search/artifacts_field` | Typed PostgreSQL artifact field search. |
| GET | `/v1/search/artifacts_fields` | Typed PostgreSQL multi-field artifact search. |
| GET | `/v1/search/artifacts_attribute` | Typed PostgreSQL artifact-attribute search. |
| GET | `/v1/search/objects_attribute` | Typed PostgreSQL object-attribute search. |
| GET | `/v1/search/objects_attributes` | Typed PostgreSQL multi-attribute object search. |

Example:

```text
/v1/search/webobject?q=example.com&offset=1
```

## Console source administration

Console endpoints are registered only when `api.enable_console` is true. Treat
all console endpoints as privileged operations: authenticate them at the edge,
restrict network access, and avoid exposing them directly to the public
Internet.

| Method | Endpoint | Purpose |
| --- | --- | --- |
| GET/POST | `/v1/source/add` | Add a new source. See [addsource](./api/addsource.md). |
| GET | `/v1/source/remove` | Remove a source and related crawled data. |
| POST | `/v1/source/update` | Update a source. |
| GET | `/v1/source/vacuum` | Delete crawled/collected data for a source without removing the source row. |
| GET | `/v1/source/status` | Return status for one URL/source. |
| GET | `/v1/source/statuses` | Return statuses for all known URLs/sources. |

## Information seed administration

The canonical namespace is `/v1/information_seed/*` (underscore). The
hyphenated `/v1/information-seed/list` route is a deprecated alias for
`/v1/information_seed/list` only.

All information seed responses include seed identity and lifecycle fields such
as `information_seed_id`, `status`, `has_error`, `last_error`,
`last_error_at`, `attempts`, `disabled`, `priority`, `engine`, timestamps,
`config`, and `discovered_source_count` where source relationship counts are
applicable.

| Method | Endpoint | Purpose |
| --- | --- | --- |
| POST | `/v1/information_seed/add` | Add an information seed. The request body accepts `information_seed`, `category_id`, `usr_id`/`user_id`, `status`, `priority`, `engine`, `disabled`, and seed-level `config`. Provider credentials belong in global `information_seed.providers`, not in this request. |
| GET | `/v1/information_seed/status` | Return status for one seed. Supply `information_seed_id`, `id`, or `q`. |
| GET | `/v1/information_seed/list` | List seeds with filters (`status`, `priority`, `disabled`, `category`/`category_id`, `user`/`user_id`/`usr_id`) plus `limit` and `offset`. |
| GET | `/v1/information_seed/sources` | List sources linked to one seed. Supply `information_seed_id`; response items include `source_information_seed_index` provenance. |
| GET | `/v1/information_seed/candidates` | List persisted accepted/rejected candidate decisions for one seed. Supply `information_seed_id`, plus optional pagination. |
| POST | `/v1/information_seed/retry` | Reset a seed for retry after correcting credentials/configuration. Body: `{"information_seed_id":123}`. |
| POST | `/v1/information_seed/disable` | Disable a seed by request body. Body: `{"information_seed_id":123}`. |
| POST | `/v1/information_seed/{id}/rerun` | Path-ID rerun helper for a seed. |
| POST | `/v1/information_seed/{id}/disable` | Path-ID disable helper for a seed. |
| POST | `/v1/information_seed/{id}/enable` | Re-enable a seed; optional body can include `queue_pending`. |
| GET | `/v1/information_seed/{id}/events` | List information-seed discovery events. |
| GET | `/v1/information_seed/{id}/diagnostics` | Return the latest redacted run diagnostics payload. |
| GET | `/v1/information-seed/list` | Deprecated alias for `/v1/information_seed/list`. |

Seed-level `config` can include `query_templates`, literal `queries`, selected
provider names, request-bounding candidate filters, source defaults, and
`candidate_plugins`. The built-in runner executes provider discovery,
normalization/de-duplication, built-in filters, plugin processing, source
override validation, source persistence/linking, event emission, and lifecycle
finalization in a deterministic order. Custom candidate plugins/agents can only
participate in the documented user/plugin phases; they do not replace built-in
persistence, linking, final status, or redaction behavior. See
[information_seed_lifecycle.md](information_seed_lifecycle.md) and
[plugins.md](plugins.md#information-seed-candidate-plugins).

## Owner and category console endpoints

| Method | Endpoint | Purpose |
| --- | --- | --- |
| POST | `/v1/owner/add` | Add an owner. |
| POST | `/v1/owner/update` | Update an owner. |
| POST | `/v1/owner/remove` | Remove an owner. |
| POST | `/v1/category/add` | Add a category. |
| POST | `/v1/category/update` | Update a category. |
| POST | `/v1/category/remove` | Remove a category. |

## Provider recipes for information seeds

Prefer lower-risk and lower-friction providers first, then add paid or custom
providers only when operator review is complete. Every provider example in this
repository is either backed by deterministic fixtures in
`pkg/infoseed/searchproviders/testdata` or explicitly marked as a template that
requires operator validation before production use.

### Free/public providers first

Use free/public providers before paid integrations and keep their request budgets
small. RSS/Atom and Common Crawl examples are fixture-backed by provider tests;
live hosts and collection IDs are still examples and should be validated by the
operator.

```yaml
information_seed:
  provider_allow_list:
    - rss_public_news
    - common_crawl_latest
  providers:
    rss_public_news:
      provider: rss_feed
      host: https://www.cisa.gov
      endpoint: /news.xml
      timeout: 10
      rate_limit: 30s
      max_requests: 1
      page_size: 10
      max_pages: 1
    common_crawl_latest:
      provider: common_crawl_index
      host: https://index.commoncrawl.org
      endpoint: /CC-MAIN-2026-18-index
      parameters:
        output: json
        filter: status:200
        collapse: urlkey
      timeout: 15
      rate_limit: 10s
      max_requests: 1
      page_size: 10
      max_pages: 1
```

### Paid/provider API integrations

Official provider APIs are preferred over scraping public result pages when the
provider offers a compliant API. The adapter behavior for Brave, Bing, and
generic `http_json` is fixture-backed, but the following live credentials,
subscriptions, quotas, and contractual terms are templates requiring operator
validation.

```yaml
information_seed:
  provider_allow_list:
    - brave_search_api
    - bing_web_search_api
  providers:
    brave_search_api:
      provider: brave_search
      host: https://api.search.brave.com
      endpoint: /res/v1/web/search
      api_key: ${INFORMATION_SEED_BRAVE_SEARCH_API_KEY}
      timeout: 30
      rate_limit: "1"
      max_requests: 3
      page_size: 10
      max_pages: 1
    bing_web_search_api:
      provider: bing_web_search
      host: https://api.bing.microsoft.com
      endpoint: /v7.0/search
      api_key: ${INFORMATION_SEED_BING_WEB_SEARCH_API_KEY}
      timeout: 30
      rate_limit: "1"
      max_requests: 3
      page_size: 10
      max_pages: 1
```

### CROWler federation provider

A CROWler node can query another CROWler Search API through the generic
`http_json` adapter because `/v1/search/general` returns an `items` array with
`link`, `title`, and `summary` fields. The parser shape is fixture-backed; the
remote deployment, trust relationship, authentication header, and data-sharing
policy are templates requiring operator validation.

```yaml
information_seed:
  provider_allow_list:
    - crowler_federation_peer
  providers:
    crowler_federation_peer:
      provider: http_json
      host: https://peer-crowler.example.invalid
      endpoint: /v1/search/general
      token: ${INFORMATION_SEED_CROWLER_FEDERATION_TOKEN}
      headers:
        User-Agent: CROWler federation information-seed example (+https://example.invalid/contact)
      parameters:
        federation_scope: public-index
      timeout: 15
      rate_limit: "0.2" # one request every five seconds
      max_requests: 2
      page_size: 10
      max_pages: 1
```

### `browser_search` HTTP and WebDriver transports

`browser_search` is an explicit opt-in HTML adapter for controlled fixtures or
policy-approved pages. Its default `transport: http` performs bounded HTTP GETs,
adds the query as `q` when the endpoint does not already contain `q` or `query`,
parses result/title/snippet selectors from `parameters`, follows the configured
next-page selector, and fails closed when the consent-page selector is present.
It does not execute JavaScript or click consent, query, or pagination controls.

`transport: webdriver` instead leases one application-owned VDI session for the
provider/query operation. It resolves read-only action-rule references for the
`initial`, `consent`, `query`, and `pagination` phases, and scraping-rule
references for candidate extraction. Navigation and every action phase, scrape,
and HBS-to-Selenium retry consume the shared `browser.max_requests` operation
budget. The session is closed and its VDI lease is released on success, error,
timeout, or cancellation.

VDI capacity is shared with subsequent crawling: `max_concurrent_seeds` is not a
VDI reservation, and each active WebDriver discovery can wait for one matching
entry from `browser.vdi_allow_list`. Operators should size the pool for discovery
plus normal crawler demand, or isolate fixture/discovery VDIs by name. An empty
pool leaves WebDriver dependencies unavailable; an exhausted pool can consume
the provider timeout while waiting for a lease. HTTP transport does not require
VDI capacity.

When `browser.hbs_enabled` is true, action phases first request HBS (the Rbee
path used by the browser action runtime). If Rbee/HBS is unavailable or an HBS
phase fails, `browser.selenium_fallback: true` permits one retry of the same
phase with Selenium; that retry consumes another operation-budget unit. With
fallback disabled, the HBS failure ends the provider call. The built-in
Information Seed action adapter does not configure a provider-specific Rbee URL,
so keep HBS disabled unless an HBS-capable action dependency is injected; when
using the built-in adapter, enable Selenium fallback if HBS is selected.

The disabled, credential-free companion examples show consent handling, literal
fixture-query submission, result extraction, and pagination without naming a
live public search engine:

- [`examples/information-seed-browser-search-fixture.yaml`](../examples/information-seed-browser-search-fixture.yaml)
- [`examples/information-seed-browser-search-fixture-ruleset.yaml`](../examples/information-seed-browser-search-fixture-ruleset.yaml)

The HTTP adapter caps each request at 10 seconds and caps a provider call at 3
requests, 2 pages, and 20 results per page. WebDriver is additionally bounded by
the provider timeout, `navigation_timeout`, `page_readiness_timeout`, the global
processing timeout, 2 pages, 64 browser operations at runtime, and 40 returned
candidates; config
normalization may impose lower global/provider caps. Diagnostics expose only
bounded counts and durations for lease wait, navigation, actions, scraping,
pages, raw records, accepted candidates, URL rejections, HBS fallbacks, cleanup
failures, and budget exhaustion. They never include URLs, headers, page HTML,
screenshots, credentials, or error text. `screenshot_on_error` is a validated,
redacted configuration flag, but the Information Seed diagnostic payload remains
count-and-duration only.

Browser results are *discovery candidates*, not crawled content. The built-in
runner normalizes, filters, de-duplicates, scores, persists, and links accepted
candidate URLs as Sources. The crawler later schedules those Sources under their
own source configuration, robots policy, crawl budget, and VDI capacity; a
successful search-page interaction does not authorize or immediately crawl a
candidate destination.

`browser_search` is not a mechanism for bypassing consent flows, robots
directives, anti-abuse controls, access restrictions, or provider terms. Before
enabling any target, the operator is responsible for confirming that provider
terms permit the use, honoring robots directives for both discovery and later
crawling, setting sustainable rate/request/page limits, minimizing and protecting
personal data, restricting API/console and diagnostic access, and complying with
applicable contracts and law. Prefer official APIs. Automated tests must remain
credential-free and use controlled fixtures rather than live public search
engines.

## Security, rate-limit, robots, and terms considerations

- Store provider secrets in environment variables or a secret manager; never put
  API keys in seed request bodies or committed examples.
- Console endpoints can create, update, delete, or disable sources and seeds.
  Put them behind authentication, network policy, and audit logging.
- Configure `rate_limit`, `max_requests`, `max_pages`, `page_size`, `timeout`,
  global `max_concurrent_seeds`, and `max_queries_per_seed` before enabling a
  provider. Treat provider `429`, `Retry-After`, and quota errors as signals to
  slow down.
- Review robots.txt, published API policies, acceptable-use policies, and terms
  of service before crawling or scraping. Do not bypass login walls, consent
  prompts, CAPTCHAs, or anti-automation controls.
- Use a clear User-Agent and contact URL where the provider permits automated
  access. Avoid sending credentials to HTML result pages; `browser_search`
  strips credential fields by design.
- Redaction is defense-in-depth, not a substitute for secret hygiene. Events and
  diagnostics redact configured keys, tokens, sensitive headers, and common
  sensitive parameter names before persistence.

## Aggregate-first time-series API

The versioned time-series API reads materialized aggregate buckets by default. It never silently falls back to raw observations.

* `GET /v1/timeseries/metrics` lists public metric definitions.
* `GET /v1/timeseries` returns aggregate chart buckets.
* `GET /v1/timeseries/observations` returns explicitly requested, bounded raw observations.
* `GET /v1/timeseries/drilldown` resolves an aggregate hash (preferred) or a complete aggregate scope to matching observations.
* `GET /v1/timeseries/dimensions` compares aggregate buckets grouped by one configured dimension.

See [Time-series observations and aggregates](timeseries.md#aggregate-first-http-api) for exact date formats, filters, response fields, pagination, privacy limits, aggregate-first queries, and drill-down examples.
