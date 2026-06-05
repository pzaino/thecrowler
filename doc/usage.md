# Usage

If you have installed The CROWler using the docker compose file, then you can
just use the API to control it. If you have built the CROWler from source, then
you can use the API or the command line tools.

## Crawling

TheCROWler uses the concept of "sources" to define a website "entry-point" from
where to start the crawling, scrapping and interaction process. More information
on sources can be found [here](./sources.md).

To crawl a site, you'll need to add it to the Sources list in the database. You
can do this by running the following command:

```bash
./addSource -url <url> -restrict <restrict>
```

This will add the site to the Sources list and the crawler will start crawling
it. The crawler will crawl the site using the specified restriction level and
then stop.

**Please note**: When adding complex URLs (or URLs with special characters),
I recommend to put the entire URL between double quotes, to avoid issues with
your OS CLI arguments interpretation.

The restriction level is a number between 0 and 3. The higher the number, the
more the crawler will crawl. The restriction level is used to limit the depth
of the crawling. For example, set the restriction level to:

* 0 = for "fully restricted" crawling (just this URL, nothing else)
* 1 = for restrict crawling to whatever contains this URL (so the URL and its
  subdirectories)
* 2 = for domain restricted (so the URL and and every link containing the
  same SLD)
* 3 = for TLD restricted (so the URL and every link containing the same TLD)
* 4 = for no restrictions, basically crawl the URL and everything on the site
 and all sites linked to it and so on and so forth.

For more info on the addSOurce syntax, type:

```bash
./addSource --help
```

For the actual crawling to take place ensure you have the CROWler VDI running,
the CROWler db container running and you have created a config.yaml
configuration to allow The CROWler to access both.

Finally, make sure that The CROWler engine is running.

### Bulk inserting sites

You can also bulk insert sites by providing a CSV file with a list of URLs.
The file should have the following format:

```csv
URL, Category ID, UsrID, Restricted, Flags, ConfigFileName
```

Where:

* URL: The URL of the site.
* Category ID: The ID of the category the site belongs to. (optional)
* UsrID: The ID of the user that added the site. (optional)
* Restricted: The restriction level of the site. (optional)
* Flags: The flags of the site. (optional)
* ConfigFileName: The name of the Source configuration file to use
  for the site. (optional)

## Removing a site

To remove a site from the Sources list, run the following command:

```bash
./removeSource -url <url>
```

Where URL is the URL of the site you want to remove and it's listed in the
Sources list.

For more info on the removeSource syntax, type:

```bash
./removeSource --help
```

## API

The CROWler provides an API to query the database. The API is a REST API and is
documented using Swagger. To access the Swagger documentation, go to
`http://localhost:8080/v1/search?q=<your query>` with a RESTFUL client.

If you have built The CROWler from source, you can run the API by running the
API with the following command:

```bash
./bin/api
```

If you used docker compose to install The CROWler, then the API is already
up and running.

Check [this](./api.md) page for details on the API end-points and how to use them.

## Information seed discovery

Information seeds let operators submit a high-level topic or organization name
and have The CROWler discover candidate Sources through configured search
providers and candidate plugins. Production discovery is disabled until the
global `information_seed` configuration is enabled, provider credentials are
configured, and provider names are explicitly allow-listed.

### Production enablement checklist

1. Configure only the providers you intend to use under
   `information_seed.providers`. Supported adapters are `http_json`,
   `brave_search`, `bing_web_search`, and the opt-in `browser_search` HTML
   adapter.
2. Use environment-variable placeholders for credentials, such as
   `${INFORMATION_SEED_BRAVE_SEARCH_API_KEY}`; do not commit real secrets.
3. Keep safe request limits in place: start with `max_concurrent_seeds: 2`,
   `max_queries_per_seed: 3`, provider `max_requests: 3`, `page_size: 10`,
   `max_pages: 1`, and `rate_limit: 1/s` or slower.
4. Add provider names to `provider_allow_list`. A configured provider is ignored
   unless it is also allow-listed.
5. Keep `browser_search` disabled unless a target-specific robots.txt, terms of
   service, consent-flow, and rate-limit review has approved it.
6. Restart or reload the worker process after changing provider configuration.

### Provider snippets

Generic JSON gateway:

```yaml
information_seed:
  enabled: true
  provider_allow_list: [public_json]
  providers:
    public_json:
      provider: http_json
      host: https://search-adapter.example.invalid
      endpoint: /v1/search
      api_key_label: api_key
      api_key: ${INFORMATION_SEED_PUBLIC_JSON_API_KEY}
      timeout: 30
      rate_limit: 1/s
      max_requests: 3
      page_size: 10
      max_pages: 1
```

Brave Search:

```yaml
information_seed:
  enabled: true
  provider_allow_list: [brave_search]
  providers:
    brave_search:
      provider: brave_search
      host: https://api.search.brave.com
      endpoint: /res/v1/web/search
      api_key: ${INFORMATION_SEED_BRAVE_SEARCH_API_KEY}
      timeout: 30
      rate_limit: 1/s
      max_requests: 3
      page_size: 10
      max_pages: 1
```

Bing Web Search:

```yaml
information_seed:
  enabled: true
  provider_allow_list: [bing_web_search]
  providers:
    bing_web_search:
      provider: bing_web_search
      host: https://api.bing.microsoft.com
      endpoint: /v7.0/search
      api_key: ${INFORMATION_SEED_BING_WEB_SEARCH_API_KEY}
      timeout: 30
      rate_limit: 1/s
      max_requests: 3
      page_size: 10
      max_pages: 1
```

Approved browser fixture or policy-reviewed HTML search page:

```yaml
information_seed:
  enabled: true
  provider_allow_list:
    - approved_fixture_search
  providers:
    approved_fixture_search:
      provider: browser_search
      host: https://search.example.invalid
      endpoint: /search
      parameters:
        result_container_selector: article.result
        url_selector: a.result-url
        title_selector: h2.result-title
        snippet_selector: p.result-snippet
        next_page_selector: a[rel='next']
        consent_page_selector: '#consent-wall'
        safe_search: strict
      timeout: 5
      rate_limit: 1s
      max_requests: 1
      page_size: 10
      max_pages: 1
```

### Tyrell Corporation API example

Create `tyrell-information-seed.json` with this request body:

```json
{
  "information_seed": "Tyrell Corporation",
  "category_id": 42,
  "usr_id": 7,
  "priority": 10,
  "status": "new",
  "disabled": false,
  "config": {
    "query_templates": [
      "{{ .Seed }} official website",
      "{{ .Seed }} investor relations",
      "{{ .Seed }} contact support"
    ],
    "providers": ["brave_search", "public_json"],
    "tracking_params": ["utm_source", "utm_medium", "utm_campaign", "fbclid"],
    "deduplicate_host": true,
    "max_candidates": 10,
    "required_url_schemes": ["https"],
    "min_score": 0.2,
    "max_candidates_per_host": 1,
    "max_candidates_per_domain": 3,
    "source_name_template": "{{ .Seed }} — {{ .Candidate.Title }}",
    "source_priority": "normal",
    "create_sources": true,
    "link_existing_sources": true,
    "update_existing_source_config": false,
    "disabled": false,
    "status": "new",
    "restricted": 1,
    "flags": 0,
    "source_config": {
      "version": "1.0",
      "format_version": "1.0",
      "source_name": "tyrell-information-seed",
      "crawling_config": {
        "site": "https://www.tyrell.example/",
        "source_type": "website"
      },
      "custom": {
        "created_by": "information_seed",
        "seed_label": "tyrell-corporation"
      }
    },
    "candidate_plugins": ["domain-policy", "source-overrides"]
  }
}
```

The rendered queries are:

```text
Tyrell Corporation official website
Tyrell Corporation investor relations
Tyrell Corporation contact support
```

The selected providers are `brave_search` and `public_json`; both must be present
in `information_seed.providers` and `provider_allow_list`. Candidate plugins are
`domain-policy` and `source-overrides`, in that order. Accepted new sources use
the request's source defaults, including `restricted: 1`, `status: "new"`, and
the `source_config` object shown in the request. Because
`update_existing_source_config` is `false`, already-existing sources are linked
without replacing their stored source config.

Add the seed:

```bash
curl -sS -X POST 'http://localhost:8080/v1/information_seed/add' \
  -H 'Content-Type: application/json' \
  -d @tyrell-information-seed.json
```

Check seed status:

```bash
curl -sS 'http://localhost:8080/v1/information_seed/status?information_seed_id=123'
```

List linked sources and inspect source provenance:

```bash
curl -sS 'http://localhost:8080/v1/information_seed/sources?information_seed_id=123&limit=50&offset=0'
```

List accepted and rejected candidate decisions, when available:

```bash
curl -sS 'http://localhost:8080/v1/information_seed/candidates?information_seed_id=123&limit=100&offset=0'
```

Queue a rerun after changing providers, credentials, filters, or plugins. Completed
and errored seeds move back to `pending`; already claimable seeds wake the
scheduler without changing status:

```bash
curl -sS -X POST 'http://localhost:8080/v1/information_seed/123/rerun'
```

The legacy retry endpoint remains available:

```bash
curl -sS -X POST 'http://localhost:8080/v1/information_seed/retry' \
  -H 'Content-Type: application/json' \
  -d '{"information_seed_id":123}'
```

Disable or enable a seed:

```bash
curl -sS -X POST 'http://localhost:8080/v1/information_seed/123/disable'
curl -sS -X POST 'http://localhost:8080/v1/information_seed/123/enable' \
  -H 'Content-Type: application/json' \
  -d '{"queue_pending":true}'
```

List information-seed discovery events:

```bash
curl -sS 'http://localhost:8080/v1/information_seed/123/events?limit=100&offset=0'
```

### Provenance fields

`/v1/information_seed/sources` returns each linked Source plus a
`source_information_seed_index` object with discovery provenance such as provider
name, query, rank, candidate score, reason, metadata, and link timestamps.
`/v1/information_seed/candidates` returns persisted accepted and rejected
candidate decisions, including rejection reasons and run attempts. Use both
endpoints together to explain what was discovered, which provider/query found it,
which plugins or filters accepted or rejected it, and whether an existing source
was reused.

### Troubleshooting

| Problem | What to check |
| --- | --- |
| Missing providers | Confirm global discovery is enabled and provider names match between seed config, global providers, and the allow-list. |
| Rejected credentials | Replace placeholders, verify provider subscription status, and use the adapter's expected credential field. |
| Rate limits | Lower concurrency and request/page limits; slow `rate_limit`; retry after quota reset. |
| Plugins reject all candidates | Inspect candidate rejection reasons, confirm plugin registration/order, then adjust filters or plugin policy. |
| Source config validation failures | Validate seed-level and plugin-provided `source_config`; remove unsafe plugin overrides before retrying. |
| Duplicate or existing sources | Existing normalized URLs are reused and linked idempotently; inspect `source_information_seed_index`. |
| Disabled seeds | Ensure `disabled` is false and status is `new` or `pending`; use the retry endpoint after re-enabling. |
