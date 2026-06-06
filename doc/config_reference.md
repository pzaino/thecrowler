# The CROWler config.yaml reference

## Properties

- **`remote`** *(object)*: (optional) This is the configuration section to tell the CROWler its actual configuration has to be fetched remotely from a distribution server. If you use this section, then do not populate the other configuration sections as they will be ignored. The CROWler will fetch its configuration from the remote server and use it to start the engine.
  - **`host`** *(string)*: This is the host that the CROWler will use to fetch its configuration.
  - **`path`** *(string)*: This is the path that the CROWler will use to fetch its configuration.
  - **`port`** *(integer)*: This is the port that the CROWler will use to fetch its configuration.
  - **`region`** *(string)*: This is the region that the CROWler will use to fetch its configuration. For example in case the distribution server is on an AWS S3 bucket, you can specify the region here.
  - **`token`** *(string)*: This is the token that the CROWler will use to connect to the distribution server to fetch its configuration.
  - **`secret`** *(string)*: This is the secret that the CROWler will use to connect to the distribution server to fetch its configuration.
  - **`timeout`** *(integer)*: This is the timeout for the CROWler to fetch its configuration.
  - **`type`** *(string)*: This is the type of the distribution server that the CROWler will use to fetch its configuration. For example, s3 or http.
  - **`sslmode`** *(string)*: This is the sslmode that the CROWler will use to connect to the distribution server to fetch its configuration.
- **`database`** *(object)*: This is the configuration for the database that the CROWler will use to store data.
  - **`type`** *(string)*
  - **`host`** *(string)*
  - **`port`** *(integer)*
  - **`user`** *(string)*
  - **`password`** *(string)*
  - **`dbname`** *(string)*
  - **`retry_time`** *(integer)*
  - **`ping_time`** *(integer)*
  - **`sslmode`** *(string)*
  - **`optimize_for`** *(string)*: This option allows the user to optimize the database for a specific use case. For example, if the user is doing more write operations than query, then use the value "write". If the user is doing more query operations than write, then use the value "query". If unsure leave it empty.
- **`crawler`** *(object)*
  - **`workers`** *(integer)*: This is the number of workers that the CROWler will use to crawl websites. Minimum number is 3 per each Source if you have network discovery enabled or 1 per each source if you are doing crawling only. Increase the number of workers to scale up the CROWler engine vertically.
  - **`interval`** *(string)*: This is the interval at which the CROWler will crawl websites. It is the interval at which the CROWler will crawl websites, values are in seconds, e.g. '3' means 3 seconds. For the interval you can also use the CROWler exprterpreter to generate delay values at runtime, e.g., 'random(1, 3)' or 'random(random(1,3), random(5,8))'.
  - **`timeout`** *(integer)*: This is the timeout for the CROWler. It is the maximum amount of time that the CROWler will wait for a website to respond.
  - **`maintenance`** *(integer)*: This is the maintenance interval for the CROWler. It is the interval at which the CROWler will perform automatic maintenance tasks.
  - **`source_screenshot`** *(boolean)*: This is a flag that tells the CROWler to take a screenshot of the source website. This is useful for debugging purposes.
  - **`full_site_screenshot`** *(boolean)*: This is a flag that tells the CROWler to take a screenshot of the full website. This is useful for debugging purposes.
  - **`max_depth`** *(integer)*: This is the maximum depth that the CROWler will crawl websites.
  - **`max_sources`** *(integer)*: This is the maximum number of sources that a single instance of the CROWler's engine will fetch atomically to enqueue and crawl.
  - **`delay`** *(string)*: This is the delay between requests that the CROWler will use to crawl websites. It is the delay between requests that the CROWler will use to crawl websites. For delay you can also use the CROWler exprterpreter to generate delay values at runtime, e.g., 'random(1, 3)' or 'random(random(1,3), random(5,8))'.
  - **`cdp_delay`** *(integer)*: Delay in milliseconds applied before each CDP request. Useful on low-spec VDIs where very frequent CDP commands can trigger transient Selenium/CDP errors.
  - **`browsing_mode`** *(string)*: This is the browsing mode that the CROWler will use to crawl websites. For example, recursive, human, or fuzzing.
  - **`max_retries`** *(integer)*: This is the maximum number of times that the CROWler will retry a request to a website. If the CROWler is unable to fetch a website after this number of retries, it will move on to the next website.
  - **`max_requests`** *(integer)*: This is the maximum number of requests that the CROWler will send to a website. If the CROWler sends this number of requests to a website and is unable to fetch the website, it will move on to the next website.
  - **`collect_html`** *(boolean)*: This is a flag that tells the CROWler to collect the HTML of a website. This is useful for debugging purposes.
  - **`collect_images`** *(boolean)*: This is a flag that tells the CROWler to collect images from a website. This is useful for debugging purposes.
  - **`collect_files`** *(boolean)*: This is a flag that tells the CROWler to collect files from a website. This is useful for debugging purposes.
  - **`collect_content`** *(boolean)*: This is a flag that tells the CROWler to collect the text content of a website. This is useful for AI datasets creation and knowledge bases.
  - **`collect_keywords`** *(boolean)*: This is a flag that tells the CROWler to collect the keywords of a website. This is useful for AI datasets creation and knowledge bases.
  - **`collect_metatags`** *(boolean)*: This is a flag that tells the CROWler to collect the metatags of a website. This is useful for AI datasets creation and knowledge bases.
- **`information_seed`** *(object)*: Bounded information seed discovery configuration. The section is disabled by default and is designed to keep seed expansion constrained by explicit concurrency, query, candidate, timeout, provider allow-list, plugin timeout, and plugin output-size limits. The scheduler treats `infoseed.Runner` as the built-in Information Seed Agent (`system.infoseed.runner`), so every seed-run event includes a system-agent identity and a phase catalog that distinguishes built-in platform phases from user/plugin phases.
  Per-seed `InformationSeed.config` may also control Source persistence with safe defaults: `create_sources: true`, `link_existing_sources: true`, `update_existing_source_config: true`, `disabled: false`, and `status: "new"`. These policy fields are documented in the [Information seed lifecycle](information_seed_lifecycle.md#informationseedconfig-example).
  - **`enabled`** *(boolean)*: Enables or disables information seed discovery.
  - **`query_timer`** *(integer)*: Seconds between seed discovery cycles. Values are clamped to the safe range `1..86400`; invalid or missing values default to `300`.
  - **`max_concurrent_seeds`** *(integer)*: Maximum seed discovery jobs processed concurrently. Values are clamped to `1..32`; invalid or missing values default to `2`.
  - **`max_queries_per_seed`** *(integer)*: Maximum provider queries generated from each seed. Values are clamped to `1..100`; invalid or missing values default to `5`.
  - **`max_candidates_per_seed`** *(integer)*: Maximum candidate Sources accepted from each seed. Values are clamped to `1..1000`; invalid or missing values default to `50`.
  - **`retry_interval`** *(integer)*: Seconds to wait before retrying failed seed discovery work. Values are clamped to `1..86400`; invalid or missing values default to `60`.
  - **`processing_timeout`** *(string)*: Maximum wall-clock time to process one seed. Uses duration strings such as `30 minutes` or `1 hour`; empty values default to `30 minutes`.
  - **`providers`** *(object)*: Provider settings and credentials keyed by provider name. Each provider may define `provider`, `host`, `endpoint`, `api_key_label`, `api_key`, `api_id`, `api_secret`, `api_token`, `token`, `secret`, `username`, `password`, `timeout`, `rate_limit`, `max_requests`, `parameters`, `headers`, `page_size`, and `max_pages`. Provider `timeout` is clamped to `1..300`; provider `max_requests` is capped by `max_queries_per_seed`; `page_size` is clamped to `1..100`; and `max_pages` is clamped to `1..10` and cannot exceed `max_requests`. Use placeholders for sensitive values, list free/public provider blocks before paid/API-key integrations, and label commercial providers clearly. The `rss_feed` provider reads RSS/Atom documents, `common_crawl_index` reads Common Crawl CDX index JSON/JSONL responses, `http_json` can front custom gateways or trusted CROWler federation peers, and the explicit opt-in `browser_search` provider reads its CSS selectors from `parameters`: `result_container_selector`, `url_selector`, `title_selector`, `snippet_selector`, `next_page_selector`, and `consent_page_selector`. Scraping public search result pages requires site-specific review of robots.txt, terms, consent flows, and anti-abuse policies; prefer official APIs, avoid bypassing controls, keep tests on fixtures rather than live search engines, and configure explicit conservative limits such as `rate_limit: 30s`, `max_requests: 1`, `max_pages: 1`, and short `timeout` values.
  - **`provider_allow_list`** *(array of strings)*: Explicit provider allow-list. Entries are trimmed, lower-cased, and de-duplicated; configured providers are ignored unless their normalized key is present in the allow-list, so an empty allow-list prevents provider execution.
  - **`plugin_limits`** *(object)*: Hard limits for plugins used by seed discovery.
    - **`timeout`** *(integer)*: Plugin execution timeout in seconds. Values are clamped to `1..300`; invalid or missing values default to `30`.
    - **`max_output_size_bytes`** *(integer)*: Maximum plugin output size in bytes. Values are clamped to `1..10485760`; invalid or missing values default to `1048576`.

  Example global provider configuration with placeholder credentials:

  ```yaml
  information_seed:
    enabled: true
    query_timer: 300
    max_concurrent_seeds: 2
    max_queries_per_seed: 5
    max_candidates_per_seed: 50
    retry_interval: 60
    processing_timeout: 30 minutes
    provider_allow_list:
      - rss_public_news
      - common_crawl_latest
      - public_json
      # Paid/API-key providers; enable only with valid subscriptions.
      # - brave_search
      # - bing_web_search
      # CROWler federation peers are templates requiring operator validation.
      # - crowler_federation_peer
      # Add browser_search only after explicit policy review; otherwise the
      # configured browser_search block below remains disabled.
      # - browser_search
    providers:
      # Free/public RSS or Atom feed adapter.
      rss_public_news:
        provider: rss_feed
        host: https://www.cisa.gov
        endpoint: /news.xml
        timeout: 10
        rate_limit: 30s
        max_requests: 1
        page_size: 10
        max_pages: 1

      # Free/public Common Crawl index adapter.
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

      # Generic JSON adapter support is preserved for custom search gateways.
      # Fixture-backed adapter shape; live host is a template requiring operator validation.
      public_json:
        provider: http_json
        host: https://search-adapter.example.invalid
        endpoint: /v1/search
        api_key_label: api_key
        api_key: ${INFORMATION_SEED_PUBLIC_JSON_API_KEY}
        parameters:
          locale: ${INFORMATION_SEED_SEARCH_LOCALE}
          safe_search: ${INFORMATION_SEED_SAFE_SEARCH}
        headers:
          X-Request-ID: ${INFORMATION_SEED_REQUEST_ID}
          X-Client-Token: ${INFORMATION_SEED_PUBLIC_JSON_CLIENT_TOKEN}
        timeout: 30
        rate_limit: "1"
        max_requests: 5
        page_size: 10
        max_pages: 1

      # Paid/API-key Brave Search API adapter. The adapter sends q=<query> and
      # X-Subscription-Token: <api_key>. Host and endpoint are optional when
      # using the public Brave API defaults shown here.
      # Paid/API-key provider; template requiring operator validation.
      brave_search:
        provider: brave_search
        host: https://api.search.brave.com
        endpoint: /res/v1/web/search
        api_key: ${INFORMATION_SEED_BRAVE_SEARCH_API_KEY}
        parameters:
          country: ${INFORMATION_SEED_BRAVE_COUNTRY}
        timeout: 30
        rate_limit: "1"
        max_requests: 5
        page_size: 10
        max_pages: 1

      # Paid/API-key Bing Web Search API adapter. The adapter sends q=<query> and
      # Ocp-Apim-Subscription-Key: <api_key>. Host and endpoint are optional
      # when using the public Bing Web Search API defaults shown here.
      # Paid/API-key provider; template requiring operator validation.
      bing_web_search:
        provider: bing_web_search
        host: https://api.bing.microsoft.com
        endpoint: /v7.0/search
        api_key: ${INFORMATION_SEED_BING_WEB_SEARCH_API_KEY}
        headers:
          X-Search-Trace: ${INFORMATION_SEED_BING_TRACE_ID}
        timeout: 30
        rate_limit: "1"
        max_requests: 5
        page_size: 10
        max_pages: 1

      # CROWler federation provider. The generic http_json parser shape is
      # fixture-backed against CROWler SearchResult JSON, but the peer trust
      # boundary, authentication token, data-sharing scope, and retention policy
      # are templates requiring operator validation before enabling.
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
        rate_limit: "0.2"
        max_requests: 2
        page_size: 10
        max_pages: 1

      # Explicit opt-in browser_search HTML adapter. Prefer Brave, Bing, or a
      # custom http_json API gateway for production. Enable this only after a
      # site-specific robots.txt, terms-of-service, consent, and rate-limit
      # review. It is intended for controlled fixtures or policy-approved pages,
      # not for bypassing search-engine access rules. Credentials are ignored by
      # this adapter, sensitive headers/parameters are stripped, screenshots and
      # debug output stay disabled, and runtime caps are stricter than generic
      # providers (defaults: max_pages=1, max_requests=1, timeout=5s; hard caps:
      # max_pages=2, max_requests=3, timeout=10s, page_size=20).
      browser_search:
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
    plugin_limits:
      timeout: 30
      max_output_size_bytes: 1048576
  ```

  Per-seed query templates, selected provider names, source defaults, and
  `candidate_plugins` live in `InformationSeed.config`; see
  [Information seed lifecycle](information_seed_lifecycle.md#informationseedconfig-example)
  for the complete seed-level contract.
  Supported production provider adapters are selected by the provider block's
  `provider` value. The runner currently recognizes `http_json` (also `json` or
  `generic_json`), `rss_feed`, `common_crawl_index`, `brave_search`
  (including Brave aliases), `bing_web_search` (including Bing aliases), and the
  explicit opt-in `browser_search` HTML adapter. Unknown provider values intentionally fall back
  to the generic JSON adapter so deployments can front additional search systems
  with an internal gateway. Keep request limits conservative in production: set
  `max_requests` no higher than the global `max_queries_per_seed`, keep
  `max_pages` at `1` unless the provider quota has been reviewed, and prefer
  `rate_limit: "1"` (one request per second), duration-based limits such as
  `rate_limit: 30s`, or slower values for external APIs.

  Minimal snippets for each supported provider, using placeholder credentials,
  are shown below. Copy only the provider blocks you intend to enable and add the
  corresponding provider names to `provider_allow_list`.

  ```yaml
  information_seed:
    enabled: true
    max_queries_per_seed: 5
    max_candidates_per_seed: 50
    provider_allow_list:
      - rss_public_news
      - common_crawl_latest
      - public_json
      # Paid/API-key providers; enable only with valid subscriptions.
      # - brave_search
      # - bing_web_search
      # CROWler federation peers are templates requiring operator validation.
      # - crowler_federation_peer
      # browser_search requires an explicit site policy review before enabling.
      # - approved_fixture_search
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
        timeout: 15
        rate_limit: 10s
        max_requests: 1
        page_size: 10
        max_pages: 1

      public_json:
        provider: http_json
        host: https://search-adapter.example.invalid
        endpoint: /v1/search
        api_key_label: api_key
        api_key: ${INFORMATION_SEED_PUBLIC_JSON_API_KEY}
        timeout: 30
        rate_limit: "1"
        max_requests: 5
        page_size: 10
        max_pages: 1

      # Paid/API-key provider; template requiring operator validation.
      brave_search:
        provider: brave_search
        host: https://api.search.brave.com
        endpoint: /res/v1/web/search
        api_key: ${INFORMATION_SEED_BRAVE_SEARCH_API_KEY}
        timeout: 30
        rate_limit: "1"
        max_requests: 5
        page_size: 10
        max_pages: 1

      # Paid/API-key provider; template requiring operator validation.
      bing_web_search:
        provider: bing_web_search
        host: https://api.bing.microsoft.com
        endpoint: /v7.0/search
        api_key: ${INFORMATION_SEED_BING_WEB_SEARCH_API_KEY}
        timeout: 30
        rate_limit: "1"
        max_requests: 5
        page_size: 10
        max_pages: 1

      crowler_federation_peer:
        provider: http_json
        host: https://peer-crowler.example.invalid
        endpoint: /v1/search/general
        token: ${INFORMATION_SEED_CROWLER_FEDERATION_TOKEN}
        timeout: 15
        rate_limit: "0.2"
        max_requests: 2
        page_size: 10
        max_pages: 1

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

- **`api`** *(object)*: This is the configuration for the API (has no effect on the engine). It is the configuration for the API that the CROWler will use to communicate with the outside world.
  - **`host`** *(string)*: This is the host that the API will use to communicate with the outside world. Use 0.0.0.0 to make the API accessible from any IP address.
  - **`port`** *(integer)*: This is the port that the API will use to communicate with the outside world.
  - **`timeout`** *(integer)*: This is the timeout for the API. It is the maximum amount of time that the CROWler will wait for the API to respond.
  - **`content_search`** *(boolean)*: This is a flag that tells the CROWler to search also in the content field of a web object in the search results. This is useful for searching for every possible details of a web object, however will reduce performance quite a bit.
  - **`return_content`** *(boolean)*: This is a flag that tells the CROWler to return the web object content of a page in the search results. To improve performance, you can disable this option.
  - **`sslmode`** *(string)*: This is the sslmode switch for the API. Use 'enable' to make the API use HTTPS.
  - **`cert_file`** *(string)*: This is the certificate file for the API HTTPS protocol.
  - **`key_file`** *(string)*: This is the key file for the API HTTPS certificates.
  - **`rate_limit`** *(string)*: This is the rate limit for the API. It is the maximum number of requests that the CROWler will accept per second. You can use the ExprTerpreter language to set the rate limit.
  - **`enable_console`** *(boolean)*: This is a flag that tells the CROWler to enable the admin console via the API. In other words, you'll get more endpoints to manage the CROWler via the Search API instead of local commands.
  - **`return_404`** *(boolean)*: This is a flag that tells the CROWler to return 404 status code if a query has no results.
- **`selenium`** *(array)*
  - **Items** *(object)*: This is the configuration for the selenium driver. It is the configuration for the selenium driver that the CROWler will use to crawl websites. To scale the CROWler web crawling capabilities, you can add multiple selenium drivers in the array. Cannot contain additional properties.
    - **`name`** *(string)*: This is the name of the VDI image.
    - **`location`** *(string)*: This is the location of the VDI image.
    - **`path`** *(string)*: This is the path to the selenium driver (IF LOCAL). It is the path to the selenium driver that the CROWler will use to crawl websites.
    - **`driver_path`** *(string)*: This is the path to the selenium driver (IF REMOTE). It is the path to the selenium driver that the CROWler will use to crawl websites.
    - **`type`** *(string)*: This is the type of selenium driver that the CROWler will use to crawl websites. For example, chrome or firefox.
    - **`port`** *(integer)*: This is the port that the selenium driver will use to connect to the CROWler. It is the port that the selenium driver will use to connect to the CROWler.
    - **`host`** *(string)*: This is the host that the selenium driver will use to connect to the CROWler. It is the host that the selenium driver will use to connect to the CROWler. For example, localhost. This is also the recommended way to use the Selenium driver with the CROWler.
    - **`headless`** *(boolean)*: This is a flag that tells the selenium driver to run in headless mode. This is useful for running the selenium driver in a headless environment. It's generally NOT recommended to enable headless mode for the selenium driver.
    - **`use_service`** *(boolean)*: This is a flag that tells the CROWler to access Selenium as service.
    - **`sslmode`** *(string)*: This is the sslmode that the selenium driver will use to connect to the CROWler. It is the sslmode that the selenium driver will use to connect to the CROWler.
    - **`download_path`** *(string)*: This is the download path for the selenium driver. It is the path where the selenium driver will download files. This is useful for downloading files from websites. The CROWler will use this path to store the downloaded files.
- **`image_storage`** *(object)*: This is the configuration for the image storage. It is the configuration for the storage that the CROWler will use to store images.
  - **`host`** *(string)*
  - **`path`** *(string)*
  - **`port`** *(integer)*
  - **`region`** *(string)*
  - **`token`** *(string)*
  - **`secret`** *(string)*
  - **`timeout`** *(integer)*
  - **`type`** *(string)*
  - **`sslmode`** *(string)*
- **`file_storage`** *(object)*: This is the configuration for the file storage. File storage will be used for web object content storage.
  - **`host`** *(string)*
  - **`path`** *(string)*
  - **`port`** *(integer)*
  - **`region`** *(string)*
  - **`token`** *(string)*
  - **`secret`** *(string)*
  - **`timeout`** *(integer)*
  - **`type`** *(string)*
  - **`sslmode`** *(string)*
- **`network_info`** *(object)*: This is the configuration for the network information collection.
  - **`dns`** *(object)*
    - **`enabled`** *(boolean)*: This is a flag that tells the CROWler to use DNS techniques. This is useful for detecting the IP address of a domain.
    - **`timeout`** *(integer)*: This is the timeout for the DNS database. It is the maximum amount of time that the CROWler will wait for the DNS database to respond.
    - **`rate_limit`** *(string)*: This is the rate limit for the DNS database. It is the maximum number of requests that the CROWler will send to the DNS database per second. You can use the ExprTerpreter language to set the rate limit.
  - **`whois`** *(object)*
    - **`enabled`** *(boolean)*: This is a flag that tells the CROWler to use whois techniques. This is useful for detecting the owner of a domain.
    - **`timeout`** *(integer)*: This is the timeout for the whois database. It is the maximum amount of time that the CROWler will wait for the whois database to respond.
    - **`rate_limit`** *(string)*: This is the rate limit for the whois database. It is the maximum number of requests that the CROWler will send to the whois database per second. You can use the ExprTerpreter language to set the rate limit.
  - **`netlookup`** *(object)*
    - **`enabled`** *(boolean)*: This is a flag that tells the CROWler to use netlookup techniques. This is useful for detecting the network information of a host.
    - **`timeout`** *(integer)*: This is the timeout for the netlookup database. It is the maximum amount of time that the CROWler will wait for the netlookup database to respond.
    - **`rate_limit`** *(string)*: This is the rate limit for the netlookup database. It is the maximum number of requests that the CROWler will send to the netlookup database per second. You can use the ExprTerpreter language to set the rate limit.
  - **`geo_localization`** *(object)*
    - **`enabled`** *(boolean)*: This is a flag that tells the CROWler to use geolocation techniques. This is useful for detecting the location of a host.
    - **`path`** *(string)*: This is the path to the geolocation database. It is the path to the database that the CROWler will use to determine the location of a host.
    - **`type`** *(string)*: This is the type of geolocation database that the CROWler will use. It is the type of database that the CROWler will use to determine the location of a host. For example maxmind or ip2location.
    - **`timeout`** *(integer)*: This is the timeout for the geolocation database. It is the maximum amount of time that the CROWler will wait for the geolocation database to respond.
    - **`api_key`** *(string)*: This is the API key for the geolocation database. It is the API key that the CROWler will use to connect to the geolocation database.
    - **`sslmode`** *(string)*: This is the sslmode for the geolocation database. It is the sslmode that the CROWler will use to connect to the geolocation database.
  - **`service_scout`** *(object)*
    - **`enabled`** *(boolean)*: This is a flag that tells the CROWler to use service scanning techniques. This is useful for detecting services that are running on a host.
    - **`timeout`** *(integer)*: This is the timeout for the scan. It is the maximum amount of time that the CROWler will wait for a host to respond to a scan.
    - **`idle_scan`** *(object)*: This is the configuration for the idle scan.
      - **`host`** *(string)*: Host FQDN or IP address.
      - **`port`** *(integer)*: Port number.
    - **`ping_scan`** *(boolean)*: This is a flag that tells the CROWler to use ping scanning techniques. This is useful for detecting hosts that are alive.
    - **`connect_scan`** *(boolean)*: This is a flag that tells the CROWler to use connect scanning techniques. This is useful for detecting services that are running on a host.
    - **`syn_scan`** *(boolean)*: This is a flag that tells the CROWler to use SYN scanning techniques. This is useful for detecting services that are running on a host.
    - **`udp_scan`** *(boolean)*: This is a flag that tells the CROWler to use UDP scanning techniques. This is useful for detecting services that are running on a host.
    - **`no_dns_resolution`** *(boolean)*: This is a flag that tells the CROWler not to resolve hostnames to IP addresses. This is useful for avoiding detection by intrusion detection systems.
    - **`service_detection`** *(boolean)*: This is a flag that tells the CROWler to use service detection techniques. This is useful for detecting services that are running on a host.
    - **`service_db`** *(string)*: This is the service detection database.
    - **`os_finger_print`** *(boolean)*: This is a flag that tells the CROWler to use OS fingerprinting techniques. This is useful for detecting the operating system that is running on a host.
    - **`aggressive_scan`** *(boolean)*: This is a flag that tells the CROWler to use aggressive scanning techniques. This is useful for detecting services that are running on a host.
    - **`script_scan`** *(array)*: This is a list of nmap scripts to run. This is particularly important when a user wants to do vulnerability scanning.
      - **Items** *(string)*
    - **`excluded_hosts`** *(array)*: This is a list of hosts to exclude from the scan. The CROWler may encounter such hosts during its crawling activities, so this field makes it easy to define a list of hosts that it should always avoid scanning.
      - **Items** *(string)*
    - **`timing_template`** *(string)*: This allows the user to set the timing template for the scan. The timing template is a string that is passed to nmap to set the timing of the scan. DO not specify values using Tx, where x is a number. Instead, use just the number, e.g., '3'.
    - **`host_timeout`** *(string)*: This is the timeout for the scan. It is the maximum amount of time that the CROWler will wait for a host to respond to a scan.
    - **`min_rate`** *(string)*: This is the minimum rate at which the CROWler will scan hosts. It is the minimum number of packets that the CROWler will send to a host per second.
    - **`max_retries`** *(integer)*: This is the maximum number of times that the CROWler will retry a scan on a host. If the CROWler is unable to scan a host after this number of retries, it will move on to the next host.
    - **`source_port`** *(integer)*: This is the source port that the CROWler will use for scanning. It is the port that the CROWler will use to send packets to hosts.
    - **`interface`** *(string)*: This is the interface that the CROWler will use for scanning. It is the network interface that the CROWler will use to send packets to hosts. Use this option with a port that is behind a VPN or a proxy for better results.
    - **`spoof_ip`** *(string)*: This is the IP address that the CROWler will use to spoof its identity. It is the IP address that the CROWler will use to send packets to hosts. Use this option with a port that is behind a VPN or a proxy for better results.
    - **`randomize_hosts`** *(boolean)*: This is a flag that tells the CROWler to randomize the order in which it scans hosts. This is useful for avoiding detection by intrusion detection systems.
    - **`data_length`** *(integer)*: This is the length of the data that the CROWler will send to hosts. It is the length of the data that the CROWler will use to send packets to hosts. Use this option with a port that is behind a VPN or a proxy for better results.
    - **`delay`** *(string)*: This is the delay between packets that the CROWler will use for scanning. It is the delay between packets that the CROWler will use to send packets to hosts. Use this option with a port that is behind a VPN or a proxy for better results. For the delay you can also use the CROWler exprterpreter to generate delay values at runtime, e.g., 'random(1, 3)' or 'random(random(1,3), random(5,8))'.
    - **`mtu_discovery`** *(boolean)*: This is a flag that tells the CROWler to use MTU discovery when scanning hosts. This is useful for avoiding detection by intrusion detection systems.
    - **`scan_flags`** *(string)*: This is the flags that the CROWler will use for scanning. It is the flags that the CROWler will use to send packets to hosts. Use this option with a port that is behind a VPN or a proxy for better results.
    - **`ip_fragment`** *(boolean)*: This is a flag that tells the CROWler to fragment IP packets. This is useful for avoiding detection by intrusion detection systems.
    - **`max_port_number`** *(integer)*: This is the maximum port number to scan (default is 9000).
    - **`max_parallelism`** *(integer)*: This is the maximum number of parallelism.
    - **`dns_servers`** *(array)*: This is a list of custom DNS servers.
      - **Items** *(string)*
    - **`proxies`** *(array)*: Proxies for the database connection.
      - **Items** *(string)*
- **`rulesets`** *(array)*: This is the configuration for the rulesets that the CROWler will use to crawl, interact, scrape info and detect stuff on the provided Sources to crawl.
  - **Items** *(object)*
    - **`path`** *(array)*
      - **Items** *(string)*
    - **`host`** *(string)*
    - **`port`** *(string)*
    - **`region`** *(string)*
    - **`token`** *(string)*
    - **`secret`** *(string)*
    - **`timeout`** *(integer)*
    - **`type`** *(string)*
    - **`sslmode`** *(string)*
    - **`refresh`** *(integer)*

- **`debug_level`** *(integer)*

## `timeseries`

The optional top-level `timeseries` section declares metric extraction, retention,
aggregation, storage, cardinality, and privacy policy. It is disabled by default;
configuration alone does **not** install or invoke a database/crawler emitter.
Configurations that omit the section continue to receive the safe defaults below.

```yaml
timeseries:
  enabled: false
  defaults:
    value_type: integer          # integer|decimal|duration|count|timestamp|boolean|string|json
    aggregates: [count]          # count|sum|average|min|max|distinct_count|first|last|p50|p75|p90|p95|p99
    bucket_interval: 1h          # none|1m|5m|15m|1h|1d|1w|1mo
    time_basis: observed_at      # observed_at|event_at|source_timestamp
    dedupe_scope: none           # none|source|object|global
    failure_policy: log_skip     # log_skip|log|skip|retry|fail_indexing
  retention:
    raw: 30d
    aggregated: 365d
  aggregation:
    enabled: false
    schedule: 5m
    batch_size: 1000
    max_batches: 10
    overlap: 15m
  storage:
    backend: postgres
    table_prefix: timeseries
    partitioning:
      enabled: false
      interval: 1d
      precreate: 7
  cardinality:
    max_series_per_metric: 100000
    max_dimensions: 10
    max_values_per_dimension: 10000
    overflow: drop              # drop|hash|overflow_bucket
  privacy:
    hash_only: false
    store_value_text: false
    max_value_length: 2048
    redact_patterns: []         # Go-compatible regular expressions
  metrics: []
```

Metric `source_kind` accepts `keyword`, `metatag`, `object_attribute`,
`webobject`, `httpinfo`, `netinfo`, `screenshot`, `file`, `information_seed`,
`information_seed_candidate`, `source_discovery`, `entity_membership`,
`object_correlation`, `correlation_rule`, or `custom`. A metatag selector must
contain `metatag_name`. An `object_attribute` selector must contain
`attribute_key` and its `object_type` must be `webobject`, `httpinfo`, or
`netinfo`; `object_type` is rejected for other source kinds.

Selectors, timestamp selectors, and dimension selectors are open JSON/YAML
objects. This keeps extraction paths declarative rather than embedding crawler
domain fields in the configuration model. Every dimension requires a unique,
non-empty `key` and a non-empty `selector`.

A metric may override any default. `event_at` and `source_timestamp` metrics
must provide `timestamp_selector`. Numeric aggregates (`sum`, `average`, `min`,
`max`, and percentiles) require `integer`, `decimal`, `duration`, or `count` values.
Boolean and JSON values support counting/distinct counting only; strings also
support first/last. Metric keys must be non-empty and unique.

Privacy and cardinality can be overridden per metric. `hash_only: true` cannot
be combined with `store_value_text: true`; regular expressions are compiled at
configuration validation time. Limits are bounded to prevent unbounded series,
dimension, or value growth.
