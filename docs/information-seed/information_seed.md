# Information Seed discovery

Information Seed discovery turns a short topic, organization name, product name,
keyword, indicator, or other human-readable lead into one or more CROWler
Sources. Instead of manually creating every Source URL, you submit an
`information_seed` and let the built-in Information Seed runner query approved
providers, normalize and filter returned candidates, optionally run candidate
plugins, and then create or link Sources for the accepted URLs.

Use this feature when you know **what** you want to monitor, but not every URL
that should be crawled yet. Typical examples include:

- finding official, support, investor-relations, or documentation pages for an
  organization;
- discovering public news or advisory pages that mention a product or threat;
- bootstrapping a source list from a search API, RSS feed, Common Crawl index,
  federation peer, or approved browser-search provider;
- recording auditable evidence for why each candidate URL was accepted or
  rejected.

## How the discovery pipeline works

For each enabled, queued seed, the runner performs the following bounded steps:

1. **Load seed run config** from the seed's `config` JSON object. Every field is
   optional. If no query is supplied, the runner uses `{{ .Seed }}`.
2. **Render queries** from `queries` and `query_templates` using Go template
   variables such as `.Seed`, `.InformationSeed`, and `.SeedID`.
3. **Select providers** from the seed `providers` list. If the seed does not
   specify providers, the runner uses the global `information_seed.provider_allow_list`.
   Providers must exist in the global `information_seed.providers` map and must
   also be present in the global allow-list.
4. **Query providers** within global and provider-specific request limits.
5. **Normalize candidates** by canonicalizing URLs, removing default tracking
   parameters plus any seed-specific `tracking_params`, rejecting invalid URLs,
   and optionally deduplicating by host.
6. **Apply built-in filters** such as allowed or denied domains, required URL
   schemes, minimum score, per-host limits, per-domain limits, and maximum
   candidates.
7. **Run candidate plugins** listed in `candidate_plugins`, if configured.
   Plugins can reject candidates or return Source field overrides.
8. **Persist decision evidence** for accepted and rejected candidates.
9. **Create or link Sources** according to `create_sources`,
   `link_existing_sources`, and `update_existing_source_config`.
10. **Emit events and diagnostics** with redacted provider and plugin details.

## Global configuration

Information Seed discovery is disabled by default. Enable it in the main CROWler
configuration under `information_seed` and define the providers that are safe for
your deployment.

```yaml
information_seed:
  enabled: true
  query_timer: 300
  max_concurrent_seeds: 2
  max_queries_per_seed: 3
  max_candidates_per_seed: 20
  retry_interval: 300
  processing_timeout: 30 minutes

  provider_allow_list:
    - rss_public_news
    - common_crawl_latest
    - public_json_adapter

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

    public_json_adapter:
      provider: http_json
      host: https://search-adapter.example.invalid
      endpoint: /v1/search
      timeout: 30
      rate_limit: "1"
      max_requests: 3
      page_size: 10
      max_pages: 1

  plugin_limits:
    timeout: 30
    max_output_size_bytes: 1048576
```

A fuller operator-oriented example is available in
[`examples/information-seed-providers.yaml`](../examples/information-seed-providers.yaml).

### Global configuration fields

| Field | Purpose |
| --- | --- |
| `enabled` | Enables the scheduler and built-in runner. Defaults to `false`. |
| `query_timer` | Seconds between seed discovery cycles. |
| `max_concurrent_seeds` | Maximum seeds processed concurrently. |
| `max_queries_per_seed` | Global cap on rendered provider queries per seed. |
| `max_candidates_per_seed` | Global cap on accepted candidates per seed. A lower seed-level `max_candidates` wins. |
| `retry_interval` | Seconds before failed work can be retried. |
| `processing_timeout` | Wall-clock timeout for one seed run, for example `30 minutes`. |
| `provider_allow_list` | Provider names allowed to run. An empty list prevents provider execution. |
| `providers` | Provider definitions keyed by name. Seed-level `providers` entries refer to these names. |
| `plugin_limits.timeout` | JavaScript candidate plugin timeout in seconds. |
| `plugin_limits.max_output_size_bytes` | Maximum candidate plugin output size. |

### Provider types

The `provider` field selects the implementation. Supported stable provider
identifiers and aliases include:

| Provider | Use case |
| --- | --- |
| `http_json` / `json` / `generic_json` | Generic HTTP JSON search adapter. Sends the query as `q` unless `q` or `query` already exists. |
| `brave_search` / `brave` | Brave Search API web search. Requires an API key in global config. |
| `bing_web_search` / `bing` | Bing Web Search API. Requires an API key in global config. |
| `browser_search` / `browser` / `html_search` | Browser-backed HTML result page discovery. Enable only after policy, robots, terms, and rate-limit review. |
| `rss_feed` / `rss` / `atom_feed` | RSS or Atom feed discovery. |
| `common_crawl_index` / `common_crawl` | Common Crawl index discovery. |

Provider credentials belong only in the global configuration. The API rejects
seed submission or update payloads that attempt to place credential-like fields
such as API keys, tokens, passwords, or secrets inside the per-seed `config`.

## Submitting an Information Seed

Submit a seed with `POST /v1/information_seed/add`. The top-level request fields
identify the seed and default Source ownership. The nested `config` object
controls discovery for that one seed.

```bash
curl -X POST http://localhost:8080/v1/information_seed/add \
  -H 'Content-Type: application/json' \
  -d @examples/tyrell-information-seed.json
```

Minimal request:

```json
{
  "information_seed": "Tyrell Corporation",
  "category_id": 42,
  "usr_id": 7,
  "priority": "normal",
  "config": {
    "query_templates": ["{{ .Seed }} official website"],
    "providers": ["rss_public_news"],
    "max_candidates": 5,
    "required_url_schemes": ["https"],
    "create_sources": true,
    "link_existing_sources": true
  }
}
```

Complete request example:

```json
{
  "information_seed": "Tyrell Corporation",
  "category_id": 42,
  "usr_id": 7,
  "priority": "normal",
  "status": "new",
  "disabled": false,
  "config": {
    "query_templates": [
      "{{ .Seed }} official website",
      "{{ .Seed }} investor relations",
      "{{ .Seed }} contact support"
    ],
    "providers": [
      "rss_public_news",
      "common_crawl_latest",
      "public_json_adapter"
    ],
    "tracking_params": ["utm_source", "utm_medium", "utm_campaign", "fbclid"],
    "deduplicate_host": true,
    "max_candidates": 10,
    "allowed_domains": ["tyrell.example", "replicants.tyrell.example"],
    "denied_domains": ["ads.tyrell.example"],
    "required_url_schemes": ["https"],
    "min_score": 0.2,
    "max_candidates_per_host": 1,
    "max_candidates_per_domain": 3,
    "source_name_template": "{{ .Seed }} — {{ .Candidate.Title }}",
    "source_priority": "normal",
    "create_sources": true,
    "link_existing_sources": true,
    "update_existing_source_config": false,
    "restricted": 1,
    "flags": 0,
    "candidate_plugins": ["tyrell_source_config"],
    "source_config": {
      "version": "1.0",
      "format_version": "1.0",
      "source_name": "tyrell-information-seed-default",
      "crawling_config": {
        "site": "https://www.tyrell.example/",
        "source_type": "website"
      },
      "custom": {
        "created_by": "information_seed",
        "seed_label": "tyrell-corporation",
        "default_source_config": true
      }
    }
  }
}
```

The complete checked-in version is
[`examples/tyrell-information-seed.json`](../examples/tyrell-information-seed.json).

### Top-level seed fields

| Field | Purpose |
| --- | --- |
| `information_seed` | Required seed text used by default query templates. |
| `category_id` | Source category applied to created Sources. |
| `usr_id` / `user_id` | Source owner/user ID. `user_id` is accepted as an API alias. |
| `priority` | Seed and default Source priority, such as `normal` or `high`. |
| `status` | Initial status. Omit or use `new` for normal queue processing. |
| `engine` | Stored seed engine value when supplied. |
| `disabled` | If `true`, the seed is stored but not processed until enabled. |
| `config` | Per-seed discovery configuration. Must be a JSON object. |

### Per-seed `config` fields

| Field | Default | Purpose |
| --- | --- | --- |
| `queries` | none | Literal Go-template query strings. Processed before `query_templates`. |
| `query_templates` | `{{ .Seed }}` when no queries are supplied | Go-template query strings rendered with `.Seed`, `.InformationSeed`, and `.SeedID`. |
| `providers` | global `provider_allow_list` | Provider names to use for this seed. Names must exist globally and be allowed. |
| `tracking_params` | default tracking params only | Additional query-string parameters removed during URL normalization. |
| `deduplicate_host` | `false` | Reject all but the first candidate from the same host during normalization. |
| `max_candidates` | global `max_candidates_per_seed` | Seed-level accepted-candidate cap. If lower than the global cap, this wins. |
| `allowed_domains` | unrestricted | Accept only matching hosts or registrable domains. |
| `denied_domains` | none | Reject matching hosts or registrable domains. |
| `required_url_schemes` | any valid scheme | Accept only schemes such as `https`. |
| `min_score` | unset | Reject candidates with provider score below this value. |
| `max_candidates_per_host` | unlimited | Accepted candidate cap per host. |
| `max_candidates_per_domain` | unlimited | Accepted candidate cap per registrable domain. |
| `source_name_template` | candidate URL | Template for created Source names. Variables include `.Seed`, `.Candidate`, `.URL`, and `.Host`. |
| `source_priority` | seed priority or normal Source defaults | Priority for created Sources. |
| `create_sources` | `true` | Create Sources for accepted candidates when no matching Source exists. |
| `link_existing_sources` | `true` | Link accepted candidates to existing Sources when the URL already exists. |
| `update_existing_source_config` | `true` | Update an existing Source's config during upsert. |
| `disabled` | `false` | Per-run disable flag. |
| `status` | `new` | Per-run status hint stored in config. |
| `restricted` | `0` | Restricted flag copied to created Sources. |
| `flags` | `0` | Source flags copied to created Sources. |
| `candidate_plugins` | none | Named candidate processors/plugins to run after built-in filters. |
| `source_config` | empty | Source configuration JSON applied to created Sources. Must match Source config expectations for your deployment. |

## Candidate plugins

Candidate plugins receive seed details, the current candidate, provider/query/rank
metadata, and proposed Source defaults. A plugin can:

- keep or reject the candidate;
- set a rejection reason;
- override candidate fields;
- provide a `source_override` such as a custom Source name, priority,
  restricted flag, flags, or Source config.

Select plugins with `config.candidate_plugins`:

```json
{
  "config": {
    "candidate_plugins": ["tyrell_source_config"]
  }
}
```

See [`examples/tyrell-information-seed-candidate-plugin.js`](../examples/tyrell-information-seed-candidate-plugin.js)
for a runnable example.

## Managing seeds through the API

Canonical endpoints use the `/v1/information_seed` namespace:

| Endpoint | Method | Purpose |
| --- | --- | --- |
| `/v1/config/information_seed/providers` | `GET` | List configured provider names. |
| `/v1/config/information_seed/providers/{name}` | `GET` | Return one redacted global provider config. |
| `/v1/information_seed/add` | `POST` | Create a seed. |
| `/v1/information_seed/status?information_seed_id=ID` | `GET` | Get one seed status. |
| `/v1/information_seed/list` | `GET` | List seeds with filters and discovered Source counts. |
| `/v1/information_seed/sources?information_seed_id=ID` | `GET` | List Sources linked to a seed, including provenance metadata. |
| `/v1/information_seed/candidates?information_seed_id=ID` | `GET` | List accepted and rejected candidate decision evidence. |
| `/v1/information_seed/retry` | `POST` | Queue a retry using `{"information_seed_id": ID}`. |
| `/v1/information_seed/update` | `POST` | Update mutable seed fields and config. |
| `/v1/information_seed/remove` | `POST` | Remove a seed using `{"information_seed_id": ID}`. |
| `/v1/information_seed/{id}/rerun` | `POST` | Queue a path-ID rerun. |
| `/v1/information_seed/{id}/disable` | `POST` | Disable a seed by path ID. |
| `/v1/information_seed/{id}/enable` | `POST` | Enable a seed by path ID. Body can include `{"queue_pending": true}`. |
| `/v1/information_seed/{id}/events` | `GET` | List discovery events for a seed. |
| `/v1/information_seed/{id}/diagnostics` | `GET` | Return latest redacted run diagnostics. |

Useful list filters include `status`, `priority`, `disabled`, `category`, `user`,
`limit`, and `offset`.

## Reviewing results

After a run completes:

1. Check status:

   ```bash
   curl 'http://localhost:8080/v1/information_seed/status?information_seed_id=123'
   ```

2. Review linked Sources and provenance:

   ```bash
   curl 'http://localhost:8080/v1/information_seed/sources?information_seed_id=123'
   ```

3. Review accepted and rejected candidates:

   ```bash
   curl 'http://localhost:8080/v1/information_seed/candidates?information_seed_id=123'
   ```

4. Inspect redacted diagnostics for provider failures, plugin failures, counts,
   and browser-operation metrics:

   ```bash
   curl 'http://localhost:8080/v1/information_seed/123/diagnostics'
   ```

## Safety and operational guidance

- Keep credentials in global configuration or environment-substituted global
  configuration, never in seed API requests.
- Keep `provider_allow_list` explicit and short. A provider name in a seed does
  not run unless the same name is globally configured and allow-listed.
- Start with low `max_requests`, low `max_pages`, and conservative `rate_limit`
  values. Increase only after provider policy and capacity review.
- Treat `browser_search` providers as high-touch integrations. Confirm target
  terms, robots/consent expectations, legal basis, and expected traffic before
  enabling them.
- Use `allowed_domains`, `denied_domains`, and `required_url_schemes` to keep
  discovery bounded.
- Use `/candidates`, `/events`, and `/diagnostics` during rollout to understand
  rejection reasons and provider behavior before creating large numbers of
  Sources.
