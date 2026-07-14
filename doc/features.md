# CROWler's Features

The **CROWler** is a self-hosted, event-driven Content Discovery and Intelligence development platform. It combines real-browser crawling, non-web data connectors, declarative rulesets, JavaScript plugins, agents, APIs, search, correlation, and analytics so users can build private search engines, OSINT and research pipelines, data-collection systems, monitoring solutions, and authorized security-testing workflows.

The CROWler is a development framework rather than a point-and-click scraping service. Some capabilities are disabled by default or require specific rulesets, plugins, provider credentials, external tools, or additional services. Always use crawling, discovery, and security-testing features only on systems and data you are authorized to access.

## CROWler 2.0 highlights

Release 2.0 introduced or substantially expanded several major areas:

- Redesigned Information Seed discovery, which turns a topic or organization name into an auditable inventory of crawlable Sources.
- Redesigned and open sourced Read-only email and mailbox ingestion through IMAP, POP3, Gmail, Microsoft Graph, maildir, and mbox connectors.
- Agents v2 with explicit identity, trust, capabilities, constraints, memory, contracts, and delegation controls.
- API authentication and authorization with users, roles, scopes, bearer tokens, and token revocation.
- Runtime-generated OpenAPI documentation, including schemas contributed by custom API plugins.
- WebSocket streams for General API and Events API updates.
- A more capable Events service for scheduled, custom, plugin-driven, and agent-driven workflows.
- Typed time-series observations, materialized aggregate buckets, retention, and bounded drill-down over persisted user data.
- Earlier XHR collection and direct XHR access from scraping pipelines, plugins, and agents.
- A richer source, artifact, attribute, entity, correlation, and discovery-provenance data model.

## Table of Contents

- [Platform and Source Model](#features-group-1-platform-and-source-model)
- [Web Crawling and Real-Browser Rendering](#features-group-2-web-crawling-and-real-browser-rendering)
- [Information Seed Discovery](#features-group-3-information-seed-discovery)
- [Email and Mailbox Crawling](#features-group-4-email-and-mailbox-crawling)
- [Search and Data Retrieval](#features-group-5-search-and-data-retrieval)
- [Scraping and Structured Extraction](#features-group-6-scraping-and-structured-extraction)
- [XHR, Page Events, and Performance Collection](#features-group-7-xhr-page-events-and-performance-collection)
- [Action Execution and Human Behaviour Simulation](#features-group-8-action-execution-and-human-behaviour-simulation)
- [Comprehensive Ruleset System](#features-group-9-comprehensive-ruleset-system)
- [JavaScript Plugins and Extension APIs](#features-group-10-javascript-plugins-and-extension-apis)
- [AI and Traditional Agents](#features-group-11-ai-and-traditional-agents)
- [Event-Driven Automation](#features-group-12-event-driven-automation)
- [REST, OpenAPI, Streaming, and WebSocket APIs](#features-group-13-rest-openapi-streaming-and-websocket-apis)
- [Authentication, Authorization, and API Protection](#features-group-14-authentication-authorization-and-api-protection)
- [Technology, Vulnerability, and Security Detection](#features-group-15-technology-vulnerability-and-security-detection)
- [Network, Service, Geolocation, and TLS Intelligence](#features-group-16-network-service-geolocation-and-tls-intelligence)
- [Data Storage, Indexing, Entities, and Correlation](#features-group-17-data-storage-indexing-entities-and-correlation)
- [Time-Series Analytics](#features-group-18-time-series-analytics)
- [Images, Files, Scripts, and Screenshots](#features-group-19-images-files-scripts-and-screenshots)
- [Configuration, Scheduling, and Scalability](#features-group-20-configuration-scheduling-and-scalability)
- [Deployment and VDI Operations](#features-group-21-deployment-and-vdi-operations)
- [Reliability, Observability, and Maintenance](#features-group-22-reliability-observability-and-maintenance)
- [Third-Party Services and Data Integration](#features-group-23-third-party-services-and-data-integration)
- [Developer Tooling and Validation](#features-group-24-developer-tooling-and-validation)

## (Features Group 1) Platform and Source Model

- **Self-Hosted Content Discovery Platform**: Runs on infrastructure controlled by the user and exposes its capabilities through services, APIs, rulesets, plugins, agents, and a structured database.
  - *Benefits*: Keeps data, crawling policy, credentials, and operational control inside the user's environment.

- **Multiple Source Families**: Built-in dispatch supports web sources, email sources, and network-only targets. Rules and plugins can also work in website, API, file, database, and generic data contexts.
  - *Benefits*: Allows one platform to process browser-rendered content, mailboxes, local mail archives, APIs, stored documents, and non-web hosts.

- **Web and Transfer Protocols**: Supports HTTP, HTTPS, FTP, and FTPS sources for web-oriented collection.
  - *Benefits*: Extends discovery beyond standard public websites.

- **Email Protocols and Local Mail Archives**: Recognizes IMAP, IMAPS, POP3, POP3S, Gmail, Microsoft Graph Mail, maildir, and mbox source schemes.
  - *Benefits*: Makes newsletters, alerts, mailing lists, reports, and archived mail searchable and available to downstream processing.

- **Per-Source Configuration**: Each Source can carry its own crawl policy, rulesets, restrictions, browser settings, scheduling hints, and connector configuration.
  - *Benefits*: Provides fine-grained control for sites and data sources with different requirements.

- **Categories, Owners, Priorities, Flags, and Restrictions**: Sources can be organized and filtered through categories, ownership relationships, priorities, flags, status, and crawl-boundary settings.
  - *Benefits*: Supports multi-tenant, multi-project, and use-case-specific data organization.

- **Stable Source Metadata**: Search and collection records include CROWler metadata that links results back to their originating Source and Source UID.
  - *Benefits*: Improves provenance, deduplication, correlation, and downstream traceability.

## (Features Group 2) Web Crawling and Real-Browser Rendering

- **Real Browsers**: Uses Chromium, Chrome, or Firefox through VDIs instead of relying only on basic HTTP clients.
  - *Benefits*: Renders JavaScript applications and captures content that exists only after browser execution.

- **Recursive and Scoped Crawling**: Supports recursive, right-click recursive, human, and rules-driven fuzzing modes, with configurable URL and domain restrictions.
  - *Benefits*: Lets users choose between a single page, a bounded property, or broader discovery.

- **Depth, Link, Request, Redirect, Retry, and Timeout Limits**: Bounds work globally and per Source.
  - *Benefits*: Controls resource use and reduces the risk of unbounded crawls.

- **Page-Load Validation**: Source configuration can validate the current URL and DOM state, then retry, mark invalid, skip, or log when the page does not load as expected.
  - *Benefits*: Distinguishes successful rendering from login redirects, block pages, partial loads, and invalid content.

- **Alternative Links and Redirect Recovery**: Supports fallback links, unwanted redirect patterns, and configurable retries of the original URL.
  - *Benefits*: Helps recover from transient or user-agent-dependent redirections.

- **Request Filtering and Bandwidth Control**: Can enable or disable browser requests for images, CSS, scripts, plugins, and frames.
  - *Benefits*: Reduces proxy bandwidth and speeds up crawls when selected resource types are unnecessary.

- **User-Agent, Cookie, Referrer, and Browser Policies**: Supports configurable browser platform, User-Agent rotation, cookie reset policies, third-party-cookie blocking, referrers, and same-origin fetch behavior.
  - *Benefits*: Adapts sessions to site requirements and privacy constraints.

- **Robots and Low-Noise Controls**: Includes robots.txt checks, configurable request pacing, warm-up ramping, intervals, and delays.
  - *Benefits*: Helps operators build respectful and predictable collection policies.

## (Features Group 3) Information Seed Discovery

- **Concept-to-Source Discovery**: Converts a topic, organization, product, threat, location, or other human-readable seed into candidate URLs and accepted CROWler Sources.
  - *Benefits*: Lets users start with what they want to monitor instead of manually assembling every URL.

- **Multiple Discovery Providers**: Supports generic JSON search adapters, Brave Search, Bing Web Search, approved browser-search providers, RSS or Atom feeds, Common Crawl indexes, and trusted CROWler federation peers.
  - *Benefits*: Combines public indexes, feeds, commercial APIs, internal services, and peer nodes.

- **Bounded Query and Candidate Processing**: Applies explicit concurrency, query, page, request, candidate, timeout, and plugin-output limits.
  - *Benefits*: Makes source discovery controllable and suitable for production operations.

- **URL Normalization, Deduplication, and Filtering**: Removes configured tracking parameters, validates URL schemes, applies allowed or denied domains, minimum scores, and per-host or per-domain limits.
  - *Benefits*: Reduces duplicate, invalid, low-quality, and out-of-policy candidates before Source creation.

- **Candidate Quality Plugins**: JavaScript plugins can accept or reject candidates, adjust scores, add metadata, and provide safe Source overrides.
  - *Benefits*: Adds deployment-specific policy without replacing built-in validation, persistence, provenance, or lifecycle handling.

- **Auditable Decisions and Provenance**: Persists accepted and rejected candidates, provider and query evidence, rankings, scores, reasons, Source links, events, and redacted diagnostics.
  - *Benefits*: Explains why a Source was created, rejected, reused, or linked to a seed.

- **Idempotent Source Creation and Linking**: Can create new Sources, reuse existing Sources, and link the same URL to multiple seeds without duplicating the Source.
  - *Benefits*: Builds a reusable source inventory while preserving seed-specific discovery evidence.

See [Information Seed discovery](information_seed.md) and [Information Seed lifecycle](information_seed_lifecycle.md).

## (Features Group 4) Email and Mailbox Crawling

- **Read-Only Mailbox Ingestion**: Collects and normalizes email without allocating a browser or modifying mailbox state.
  - *Benefits*: Safely turns newsletters, alerts, spam, reports, and mailing-list traffic into searchable data.

- **Multiple Connectors**: Supports IMAP or IMAPS, POP3 or POP3S, Gmail API, Microsoft Graph Mail, local maildir, and local mbox sources.
  - *Benefits*: Covers common hosted, standards-based, cloud, and archive-based mailbox deployments.

- **Polling and Listener Modes**: Polling works across providers; listener mode supports IMAP IDLE, Gmail push notifications, and Microsoft Graph webhooks while periodic reconciliation remains authoritative.
  - *Benefits*: Provides timely updates without sacrificing durable recovery and consistency.

- **MIME, Charset, and Multipart Processing**: Decodes common transfer encodings and character sets, chooses normalized text or HTML bodies, and bounds MIME depth and part counts.
  - *Benefits*: Produces deterministic, searchable documents from complex or malformed messages.

- **Attachment and Link Policies**: Supports bounded attachment extraction, media-type filtering, inline-attachment policy, link extraction, and disabled remote-link following by default.
  - *Benefits*: Collects useful evidence while limiting exposure to untrusted content and tracking resources.

- **Checkpoints, Leases, and Idempotent Message Identity**: Advances provider state only after durable output and converges repeated processing on stable document identities.
  - *Benefits*: Prevents gaps and duplicate records during retries, restarts, and notification bursts.

- **Proxy, TLS, and Secret References**: Network connectors support HTTP, HTTPS, and SOCKS5 proxies, TLS verification, and centrally configured credential references with redaction.
  - *Benefits*: Integrates mailbox collection with enterprise networking and secret-management practices.

See [Email source API payloads](api/email-sources.md) and [Email crawling architecture](email-crawling-design.md).

## (Features Group 5) Search and Data Retrieval

- **High-Performance Search API**: Exposes indexed data through versioned General API search endpoints.
  - *Benefits*: Makes collected data available to applications, dashboards, analysts, agents, and integration pipelines.

- **Advanced Query Syntax**: Supports field-oriented dorking, exact phrases, logical expressions, pagination, and content-aware queries.
  - *Benefits*: Enables precise searches across large collections.

- **Specialized Search Views**: Includes searches for general pages, network information, HTTP information, screenshots, web objects, correlated sites, and collected data.
  - *Benefits*: Lets clients query the most relevant representation instead of parsing one generic response.

- **Typed Artifact and Attribute Search**: Provides typed endpoints for pages, scraped data, artifact fields, object attributes, and multi-attribute queries.
  - *Benefits*: Supports structured application queries over user-defined fields and normalized attributes.

- **Entity and Source Correlation**: Relates Sources, pages, objects, attributes, owners, entities, and other evidence.
  - *Benefits*: Reveals connections that are difficult to find in isolated crawl results.

- **Self-Hosted Search Engine Foundation**: The database, index, API, and extensible data model can be used to build private or organization-specific search services.
  - *Benefits*: Gives operators control over indexing policy, ranking inputs, retention, and result presentation.

## (Features Group 6) Scraping and Structured Extraction

- **Declarative Scraping Rules**: Defines extraction with CSS, XPath, ID, class, name, tag, link text, partial link text, JavaScript path, regular expression, plugin, and agent selectors.
  - *Benefits*: Supports both straightforward pages and complex site-specific extraction logic.

- **Selector Fallbacks and Multi-Match Extraction**: A field can use multiple selectors, extract one or all occurrences, and mark critical fields.
  - *Benefits*: Makes scrapers more resilient to layout changes and alternate page variants.

- **Text, HTML, and Attribute Extraction**: Extracts visible text, HTML fragments, and element attributes, with optional HTML-to-JSON transformation.
  - *Benefits*: Produces structured output from both semantic content and raw page fragments.

- **Page Readiness and Wait Conditions**: Rules can wait for element presence, visibility, delays, plugins, or agents before extraction.
  - *Benefits*: Avoids scraping a dynamic page before its relevant content is available.

- **Post-Processing Pipelines**: Supports replacement, removal, transformation, validation, cleaning, environment setting, plugin calls, agent calls, and external API steps.
  - *Benefits*: Cleans, validates, enriches, and reconciles data before persistence.

- **JSON Field Renaming and Extensible Output**: Scraped fields can be renamed and stored as structured JSON associated with the originating page and Source.
  - *Benefits*: Lets users extend the data model without changing the Go database schema for every new field.

- **Keywords, Metadata, Language, and Content Extraction**: Can collect page text, keywords, meta tags, language, links, and other searchable metadata.
  - *Benefits*: Supports classification, discovery, knowledge-base construction, and search enrichment.

## (Features Group 7) XHR, Page Events, and Performance Collection

- **XHR Capture**: Collects browser XHR and fetch traffic, with MIME-type filtering.
  - *Benefits*: Exposes structured data returned by application APIs even when it is not present directly in the rendered DOM.

- **XHR-Aware Scraping Order**: XHR collection occurs early enough for scraping and post-processing logic to use it for extraction and data reconciliation.
  - *Benefits*: Combines DOM evidence and backend JSON responses in one pipeline.

- **Plugin and Agent XHR Access**: Engine plugins and agents receive captured XHR entries separately through `params.xhr` while `params.json_data` remains the scraped document.
  - *Benefits*: Lets extensions inspect network responses without accidentally persisting the entire XHR cache as modified scraped output.

- **JSON Query Library**: The built-in `json_query` library plugin supports objects, arrays, scalar roots, wildcards, recursive descent, slices, unions, filters, projections, compiled paths, and safety limits using paths compatible with attribute indexing.
  - *Benefits*: Makes deeply nested JSON and XHR payloads easier to extract and transform from plugins.

- **Page Event Collection**: Can collect browser and JavaScript page events for analysis and event generation.
  - *Benefits*: Helps identify callbacks, dynamic behavior, and security-relevant activity.

- **Page Performance Metrics**: Can persist page-level performance measurements during crawling.
  - *Benefits*: Supports content-performance analysis alongside the collected page data.

See [Using the `json_query` library plugin](plugins_api/UsingJSONQuery.md).

## (Features Group 8) Action Execution and Human Behaviour Simulation

- **Declarative Browser Actions**: Supports click, input, clear, drag and drop, hover, right click, double click, hold and release, keyboard actions, navigation, history, refresh, window and frame switching, alert handling, scrolling, screenshots, and custom plugin actions.
  - *Benefits*: Automates multi-step interactions required before data can be collected.

- **Human Behaviour Simulation (HBS)**: Can use low-level mouse and keyboard control, randomized timing, and human-like interaction sequences where the HBS runtime is available.
  - *Benefits*: Produces more realistic interaction patterns than direct DOM-only automation.

- **Selenium Execution and Fallback Paths**: Browser actions can run through Selenium, and selected workflows can fall back from HBS to Selenium when configured.
  - *Benefits*: Balances realistic interaction with operational reliability.

- **Randomized Timing Expressions**: Delays and intervals can use the CROWler expression interpreter, including nested random ranges.
  - *Benefits*: Avoids rigid, identical request timing across sessions.

- **Conditional and Wait-Aware Actions**: Actions can wait for readiness and evaluate element, language, plugin, or agent conditions.
  - *Benefits*: Makes interaction flows responsive to the actual page state.

HBS can reduce automation fingerprints, but no automation framework can guarantee that a site will not detect or block automated activity.

## (Features Group 9) Comprehensive Ruleset System

- **Four Core Rule Families**:
  - **Crawling Rules** define navigation, recursion, and fuzzing behavior.
  - **Scraping Rules** define structured extraction and post-processing.
  - **Action Rules** define browser and system interactions.
  - **Detection Rules** define technologies, objects, patterns, and vulnerabilities to identify.
  - *Benefits*: Separates policy and site logic from the Go core.

- **YAML and JSON Authoring**: Rulesets are declarative, versioned, and schema validated.
  - *Benefits*: Makes rules reviewable, portable, and suitable for source control.

- **Rule Groups and Applicability**: Supports named groups, enablement, validity windows, URL matching, scopes, preconditions, wait conditions, and critical outcomes.
  - *Benefits*: Organizes complex behavior and limits rules to the intended context.

- **Plugin and Agent Calls from Rules**: Rules can delegate selectors, waits, transformations, decisions, and custom behavior to JavaScript plugins or CROWler Agents.
  - *Benefits*: Keeps common logic declarative while allowing advanced extension points.

- **Local or Remote Distribution**: Rulesets can be loaded locally or from configured remote locations such as HTTP, S3-compatible storage, or FTP.
  - *Benefits*: Supports centralized rule distribution across a fleet.

- **Runtime Reloading**: Configuration and rules can be reloaded without stopping active deployments, including through SIGHUP-based reload flows.
  - *Benefits*: Reduces downtime when updating collection logic.

See [Ruleset architecture](ruleset_architecture.md).

## (Features Group 10) JavaScript Plugins and Extension APIs

- **Multiple Plugin Types**: Supports engine, VDI, API, event, library, and test plugins.
  - *Benefits*: Extends data processing, browser behavior, APIs, events, reusable libraries, and test coverage without modifying the Go core.

- **Engine and Event ETL APIs**: Plugins can log, hash data, make HTTP requests, query internal or external databases, convert JSON, CSV, and XML, and run reusable JSON transformations.
  - *Benefits*: Enables custom enrichment and integration pipelines close to the collected data.

- **VDI Plugins**: Run inside the browser context with access to the DOM and browser APIs.
  - *Benefits*: Handles page-specific behavior that cannot be represented cleanly by built-in selectors or actions.

- **Custom API Endpoints**: API plugins can add routes to the General API.
  - *Benefits*: Lets developers expose domain-specific services without maintaining a separate API process.

- **OpenAPI-Aware Plugin Routes**: API plugins can provide query, request, and response schemas that are included in runtime-generated OpenAPI documentation.
  - *Benefits*: Makes custom endpoints discoverable and easier for clients to integrate correctly.

- **Reusable Library Plugins**: Plugins can include shared libraries, including the built-in `json_query` library.
  - *Benefits*: Reduces duplicated code and standardizes common extraction logic.

- **Plugin Controls**: Supports configured locations, refresh intervals, allow-lists, execution timeouts, and specialized output-size limits.
  - *Benefits*: Gives operators control over which extensions can run and how much work they may perform.

See [Plugin documentation](plugins.md).

## (Features Group 11) AI and Traditional Agents

- **Deterministic and AI-Assisted Workflows**: Agents may contain only fixed steps, AI interactions, or a combination of both.
  - *Benefits*: Lets users choose predictable automation, model-assisted reasoning, or hybrid orchestration.

- **Agents v1 and v2**: Retains legacy jobs-only manifests while adding identity-enabled v2 manifests for new deployments.
  - *Benefits*: Preserves compatibility while enabling stronger governance.

- **Explicit Agent Identity and Governance**: v2 supports stable IDs, versions, types, owners, trust levels, capabilities, constraints, resource limits, audit tags, goals, reasoning modes, and agent contracts.
  - *Benefits*: Makes agent permissions and operational boundaries reviewable and enforceable.

- **Agent Memory Policies**: Supports none, ephemeral, or persistent memory scopes with namespaces, TTLs, and retention limits.
  - *Benefits*: Controls how much state an agent can carry across executions.

- **Self-Model and Delegation Controls**: Manifests can state whether an agent may modify its identity or jobs, spawn agents, or delegate work.
  - *Benefits*: Limits autonomy explicitly instead of relying on undocumented behavior.

- **Multiple Triggers**: Jobs can run manually, on intervals, on events, on signals, or when called by another agent or ruleset.
  - *Benefits*: Connects agents to crawl lifecycle, schedules, application workflows, and reusable subflows.

- **Workflow Actions**: Steps can perform API requests, AI interactions, database queries, command execution, plugin execution, event creation, and decision branching.
  - *Benefits*: Orchestrates CROWler and external systems through one manifest.

- **Agent CLI Tooling**: `crowler-agt` supports linting, strict validation, conversion between YAML and JSON, and other agent-management workflows.
  - *Benefits*: Improves authoring quality and CI integration.

See [CROWler Agents User Guide](crowler_agents_reference.md).

## (Features Group 12) Event-Driven Automation

- **Dedicated Events Service**: The Events Manager operates as a separate service with its own API and runtime.
  - *Benefits*: Decouples asynchronous workflows from crawling and search traffic.

- **Custom Event Types**: Event types are user-extensible strings with structured details and severity.
  - *Benefits*: Supports domain-specific automation without changing the event schema for every workflow.

- **Scheduled Events**: Events and event-driven jobs can be scheduled for later execution or recurring intervals.
  - *Benefits*: Enables periodic analysis, delayed work, and maintenance workflows.

- **Plugin and Agent Triggers**: Events can invoke event plugins, engine plugins, or matching agent jobs.
  - *Benefits*: Connects data collection to enrichment, notifications, analysis, and downstream actions.

- **Lifecycle and Diagnostic Events**: Crawling, batches, Information Seed runs, email listeners, and other subsystems can emit structured operational and business events.
  - *Benefits*: Provides a consistent automation and audit channel across the platform.

- **Heartbeat and Event Management**: Supports service heartbeats, event polling, automatic event removal policies, and rate-limited event handling.
  - *Benefits*: Helps coordinate distributed services and control event retention.

## (Features Group 13) REST, OpenAPI, Streaming, and WebSocket APIs

- **General API**: Provides search, Source administration, Information Seed administration, configuration inspection, time-series queries, authentication, and custom plugin endpoints.
  - *Benefits*: Offers one programmable interface for applications and operators.

- **Events API**: Provides asynchronous event creation, retrieval, scheduling, and workflow integration.
  - *Benefits*: Separates event-driven automation from synchronous search and administration calls.

- **Runtime-Generated OpenAPI 3.0.3**: Exposes `/v1/openapi.json` and a documentation index when API documentation is enabled.
  - *Benefits*: Keeps API clients aligned with the routes available in the running build.

- **REST Event Streaming**: The General API supports event-streaming responses for suitable long-running or incremental operations.
  - *Benefits*: Lets clients receive progress without repeatedly polling.

- **WebSocket Streams**: Optional `/v1/ws` and `/v1/event/ws` endpoints publish JSON updates from the General and Events services.
  - *Benefits*: Enables responsive dashboards and real-time integrations.

- **Health and Readiness Endpoints**: Exposes liveness and readiness checks.
  - *Benefits*: Integrates with orchestrators, load balancers, and monitoring systems.

- **Console and Administrative Routes**: Optional privileged routes manage Sources, Information Seeds, categories, owners, authentication records, and selected configuration data.
  - *Benefits*: Enables operational tooling without direct database access.

See [API documentation](api.md) and [WebSocket API endpoints](api/websockets.md).

## (Features Group 14) Authentication, Authorization, and API Protection

- **Shared API Authentication Model**: The General API and Events API use the same authentication configuration and bearer-token model.
  - *Benefits*: Keeps access control consistent across horizontally scaled services.

- **Local Authentication**: Supports local users with Argon2id password hashes and HMAC-signed access tokens.
  - *Benefits*: Provides a self-contained authentication option for private deployments.

- **OIDC and Hybrid Configuration**: Supports local, OIDC-configured, hybrid, or disabled modes, with issuer, audience, and JWKS settings for external identity-provider integration.
  - *Benefits*: Fits both standalone and enterprise identity environments.

- **Roles and Scopes**: Stores roles, scopes, role-to-scope grants, user roles, and user-specific scopes.
  - *Benefits*: Allows endpoints and administrative operations to require explicit privileges.

- **Token Expiry and Revocation**: Tokens carry expiry and unique IDs, and revocations can be shared through the database.
  - *Benefits*: Supports logout, credential invalidation, and fleet-wide token rejection.

- **Authentication Administration API**: Privileged endpoints can manage users, roles, scopes, and grants.
  - *Benefits*: Avoids manual database editing for routine identity administration.

- **Transport and Request Protection**: APIs support TLS, rate limits, CORS, allowed IP or CIDR lists, host controls, read and write timeouts, and WebSocket origin allow-lists.
  - *Benefits*: Adds multiple layers of service exposure control.

## (Features Group 15) Technology, Vulnerability, and Security Detection

- **Technology and Framework Detection**: Detection rules can identify servers, frameworks, content-management systems, JavaScript libraries, plugins, and other technologies.
  - *Benefits*: Builds a structured view of the technology stack behind a Source.

- **Multiple Fingerprinting Inputs**: Detection can use page content, DOM elements, scripts, HTTP headers, cookies, network evidence, and JavaScript-visible objects.
  - *Benefits*: Improves coverage beyond simple header matching.

- **Vulnerability and Pattern Detection**: Rules can identify known vulnerable versions, insecure patterns, exposed artifacts, and user-defined indicators.
  - *Benefits*: Supports authorized external assessments and continuous security discovery.

- **Security Header Analysis**: Collects and evaluates headers such as CSP, HSTS, and related controls.
  - *Benefits*: Helps assess web security posture and configuration drift.

- **JavaScript Collection for External Analysis**: Scraping rules can persist page scripts as web objects.
  - *Benefits*: Enables later linting, software-composition analysis, and specialized JavaScript security scanning.

- **Rule-Driven Security Intelligence**: Security logic remains configurable through detection, crawling, scraping, action, plugin, event, and agent layers.
  - *Benefits*: Allows organizations to encode their own indicators, policies, and response workflows.

## (Features Group 16) Network, Service, Geolocation, and TLS Intelligence

- **DNS and WHOIS Collection**: Resolves DNS data and retrieves domain-registration information.
  - *Benefits*: Adds ownership and infrastructure context to web content.

- **Network Lookup and Non-Web Targets**: Can collect network intelligence for hosts even when they do not expose a supported web or email service.
  - *Benefits*: Extends discovery to infrastructure that would otherwise be invisible to a browser crawler.

- **Service Scout**: Integrates Nmap-compatible service discovery and scan options for ports, services, versions, scripts, and authorized vulnerability checks.
  - *Benefits*: Correlates application content with the services exposed by the underlying host.

- **Geolocation**: Supports MaxMind-style databases, IP2Location-style services, and registration-derived location data where configured.
  - *Benefits*: Adds geographic context to infrastructure and Source analysis.

- **TLS and Certificate Analysis**: Collects certificate, chain, expiry, key, signature, protocol, and cipher information.
  - *Benefits*: Supports certificate inventory, expiry tracking, and TLS posture assessment.

- **TLS and Content Fingerprints**: Includes fingerprinting components such as JA3, JA4, JARM, and MinHash where enabled and applicable.
  - *Benefits*: Helps correlate services and detect infrastructure or content similarities.

## (Features Group 17) Data Storage, Indexing, Entities, and Correlation

- **Structured Search Database**: Stores Sources, pages, owners, sessions, HTTP information, network information, screenshots, web objects, meta tags, keywords, events, and their relationships.
  - *Benefits*: Preserves provenance and avoids flattening every result into an isolated document.

- **Graph-Like Relationships**: Links Sources to owners, categories, seeds, sessions, pages, artifacts, entities, and correlated objects.
  - *Benefits*: Supports relationship-oriented intelligence and exploration.

- **Extensible JSON Data**: Uses structured JSON fields for Source configuration, collected details, scraped data, metadata, diagnostics, and extension output.
  - *Benefits*: Accommodates new use cases without requiring a new table for every custom field.

- **Configurable Attribute Indexing**: Extracts selected paths from web objects, HTTP information, and network information, applies normalizers, and stores typed attributes.
  - *Benefits*: Makes important nested JSON fields searchable and correlatable.

- **Entity Membership and Object Correlation**: Persists entity memberships, object relationships, rule provenance, and confidence values.
  - *Benefits*: Builds higher-level intelligence from repeated observations across Sources.

- **Current-State or Historical Collection Policies**: `refresh_content` can replace current page data, while disabled refresh preserves repeated collections for history and change analysis.
  - *Benefits*: Supports both snapshot-style indexes and longitudinal datasets.

- **Primary and Portable Database Backends**: PostgreSQL is the primary production backend; database setup and selected subsystems also support MySQL or MariaDB and SQLite.
  - *Benefits*: Provides a scalable default and lighter-weight options for development, testing, or constrained deployments.

## (Features Group 18) Time-Series Analytics

- **Typed Observations**: Converts selected persisted crawler, discovery, entity, and correlation facts into integer, decimal, duration, boolean, string, JSON, count, or timestamp observations.
  - *Benefits*: Measures how user data changes over time without replacing the authoritative search tables.

- **Multiple Source Kinds**: Can observe keywords, meta tags, object attributes, web objects, HTTP information, network information, screenshots, files, Information Seeds, candidate decisions, Source discovery, entity membership, and object correlation.
  - *Benefits*: Applies one analytics model across many CROWler data families.

- **Materialized Aggregate Buckets**: Supports count, sum, average, min, max, distinct count, first, last, and percentile aggregates over fixed intervals.
  - *Benefits*: Serves charts and trend queries efficiently.

- **Dimensions and Cardinality Controls**: Metrics can define bounded comparison dimensions and overflow policy.
  - *Benefits*: Enables segmented analysis without uncontrolled series growth.

- **Deduplication, Change Detection, and Late-Data Overlap**: Supports configurable dedupe scopes, retry-safe observation identity, change metadata, and overlap windows for late arrivals.
  - *Benefits*: Improves correctness under retries and asynchronous processing.

- **Retention, Privacy, and Redaction**: Supports separate raw and aggregate retention, hash-only storage, text-storage controls, maximum value length, and redaction patterns.
  - *Benefits*: Balances analytical value with data-minimization requirements.

- **Aggregate-First Queries and Bounded Drill-Down**: API clients can query buckets, dimensions, supporting observations, and explicit reaggregation ranges.
  - *Benefits*: Provides fast summaries while retaining controlled access to evidence.

Time Series measures persisted user data. Worker health, API latency, queue depth, scheduler health, and other infrastructure telemetry belong in Prometheus, logs, traces, or administrative monitoring.

See [Time-Series configuration and results guide](timeseries.md) and [Time-Series API](api/timeseries.md).

## (Features Group 19) Images, Files, Scripts, and Screenshots

- **Image and File Collection**: Can collect images and linked files during crawling.
  - *Benefits*: Preserves non-text evidence alongside indexed content.

- **HTML and Text Collection**: Can store page HTML and normalized text content independently.
  - *Benefits*: Supports debugging, archival, search, AI datasets, and reprocessing.

- **Web Object Storage**: Stores scripts, styles, images, iframes, HTML, and other discovered objects with hashes and structured details.
  - *Benefits*: Deduplicates reusable artifacts and enables object-level analysis.

- **Source and Full-Site Screenshots**: Captures initial-page and full-page screenshots, including long or infinite-scroll pages with configured height and wait controls.
  - *Benefits*: Provides visual evidence for review, comparison, and reporting.

- **Email Attachments**: Mail crawling can extract attachments under explicit size, count, type, and inline-content policies.
  - *Benefits*: Makes documents delivered by email available to the same downstream storage and analysis workflows.

- **Configurable Storage Targets**: Images and files can use local, remote HTTP, S3-compatible, FTP, or other configured storage adapters.
  - *Benefits*: Separates large binary storage from the search database when required.

## (Features Group 20) Configuration, Scheduling, and Scalability

- **Global and Per-Source Configuration**: Combines fleet-wide defaults with Source-specific overrides.
  - *Benefits*: Provides consistent operations without losing site-specific control.

- **Local or Remote Configuration Distribution**: Can load configuration locally or from HTTP, S3-compatible storage, FTP, and other supported remote modes.
  - *Benefits*: Centralizes configuration for distributed deployments.

- **Engine Schedules**: Individual engines can be restricted by date ranges, weekdays, time windows, and IANA time zones.
  - *Benefits*: Aligns collection with business hours, maintenance windows, regional requirements, or provider quotas.

- **Source Priorities and VDI Assignment**: Engines can claim selected priority classes and use selected VDI pools.
  - *Benefits*: Separates workloads by urgency, geography, browser profile, or customer.

- **Parallel Workers and Atomic Source Claims**: Multiple workers and engine instances can claim work without processing the same Source concurrently.
  - *Benefits*: Supports horizontal and vertical scaling.

- **Independent Service Scaling**: Engines, VDIs, General APIs, Events services, and databases can be scaled separately.
  - *Benefits*: Adds capacity where a deployment actually needs it.

- **Hot Reloading**: Configuration, rulesets, and selected runtime registries can be refreshed without a full platform shutdown.
  - *Benefits*: Reduces operational disruption.

## (Features Group 21) Deployment and VDI Operations

- **Microservice Architecture**: Separates crawling engines, General API, Events service, database, and browser VDIs.
  - *Benefits*: Supports isolated upgrades, independent scaling, and distributed deployments.

- **Docker and Docker Compose**: Provides container build files, deployment scripts, and compose generation tools.
  - *Benefits*: Simplifies reproducible local, server, and cloud installation.

- **Static and Orchestrated Scaling**: Supports generated multi-engine and multi-VDI compose deployments as well as external orchestrators such as Kubernetes.
  - *Benefits*: Fits both simple installations and large fleets.

- **Remote VDIs**: Engines can connect to browser VDIs on remote hosts or in different regions.
  - *Benefits*: Places browser execution near target regions, proxies, or specialized infrastructure.

- **VNC and noVNC Access**: VDIs can be observed and interacted with remotely.
  - *Benefits*: Helps debug page state, consent flows, browser behavior, and rule execution.

- **Browser and Platform Profiles**: Supports Chrome, Chromium, and Firefox, headless or visible operation, desktop or experimental mobile mode, language settings, proxies, and VDI system-manager integration.
  - *Benefits*: Adapts browser execution to different collection scenarios.

From release 2.0, the main configuration section for browser environments is named `vdi`; the former `selenium` section is no longer the canonical configuration name.

## (Features Group 22) Reliability, Observability, and Maintenance

- **Retries and Recovery Policies**: Supports request retries, redirect retries, crawl-after-error intervals, processing timeouts, stale-work recovery, and connector reconciliation.
  - *Benefits*: Recovers from transient failures without manual intervention.

- **Detailed Logging and Debug Levels**: Emits structured operational messages with configurable verbosity and syslog-compatible deployment patterns.
  - *Benefits*: Supports troubleshooting and integration with centralized logging.

- **Prometheus Integration**: Provides configurable Prometheus metrics for supported operational components.
  - *Benefits*: Integrates CROWler with standard monitoring stacks.

- **Status and Error Tracking**: Sources, seeds, events, and connectors persist lifecycle states, attempts, timestamps, and redacted error information.
  - *Benefits*: Makes failures inspectable through APIs and database queries.

- **Health Checks and Service Readiness**: Includes API endpoints and CLI tooling for health and readiness verification.
  - *Benefits*: Supports automated deployment checks and failover decisions.

- **Database Maintenance**: Can run scheduled cleanup and optimization work, with configurable maintenance intervals and retention policies.
  - *Benefits*: Helps long-running deployments control storage and index health.

- **Resource and Output Bounds**: Applies size, count, depth, timeout, request, candidate, event, and plugin limits across high-risk inputs.
  - *Benefits*: Reduces denial-of-service and out-of-memory risk from untrusted or unexpectedly large data.

## (Features Group 23) Third-Party Services and Data Integration

- **External Security Intelligence**: Supports native or plugin-based integrations with services such as Shodan, Censys, VirusTotal, AbuseIPDB, URLHaus, PhishTank, Google Safe Browsing, Hybrid Analysis, AlienVault, and related providers.
  - *Benefits*: Enriches locally collected evidence with external reputation and threat intelligence.

- **External API Enrichment**: Scraping post-processing, plugins, and agents can call third-party APIs.
  - *Benefits*: Adds classification, normalization, translation, sentiment, geocoding, or business-specific enrichment.

- **External Database Access**: Plugin helpers can query PostgreSQL, MySQL, SQLite, MongoDB, and Neo4j databases.
  - *Benefits*: Joins CROWler data with existing enterprise and analytical stores.

- **Discovery Provider Integrations**: Information Seed can use public feeds, Common Crawl, commercial search APIs, internal JSON services, and trusted CROWler peers.
  - *Benefits*: Expands Source discovery while keeping provider budgets and credentials centrally controlled.

- **Outbound Authentication**: Integration clients can use static bearer tokens and Google Cloud ID tokens where configured.
  - *Benefits*: Connects securely to protected internal or cloud-hosted services.

- **Plugin-Based Adapters**: Custom JavaScript adapters can add unsupported APIs, transformations, and export destinations.
  - *Benefits*: Avoids waiting for every integration to become part of the Go core.

## (Features Group 24) Developer Tooling and Validation

- **JSON Schemas**: Provides schemas for global configuration, Source configuration, categories, rulesets, agents, events, and specialized plugin contracts.
  - *Benefits*: Enables strict validation and editor assistance.

- **VS Code and Editor Integration**: Schemas can be associated with YAML and JSON files in modern editors.
  - *Benefits*: Provides completion, inline validation, enum hints, and documentation while authoring.

- **Command-Line Tools**: Includes tools for adding, removing, exporting, and updating Sources, adding categories, health checks, the main engine, Events service, API service, and agent management.
  - *Benefits*: Supports administration, automation, and CI without a graphical interface.

- **Validation and CI Integrations**: The CROWler ecosystem includes syntax-validation and plugin-test tooling suitable for GitHub Actions and other CI pipelines.
  - *Benefits*: Detects invalid configuration, rules, agents, and plugins before deployment.

- **OpenAPI for Client Generation**: Runtime API documentation can be used by client generators and integration tooling.
  - *Benefits*: Reduces manual API-client maintenance.

- **Go Core, JavaScript Extensions, YAML or JSON Rules**: The architecture uses Go for the core, JavaScript for plugins, and YAML or JSON for declarative configuration and rules.
  - *Benefits*: Combines a compiled runtime with accessible extension and policy languages.

- **Extensive Tests and Security Automation**: The repository includes unit, integration, fuzz, schema, database, connector, and workflow tests together with code-quality and security checks.
  - *Benefits*: Improves confidence when extending or upgrading the platform.

---

For installation, architecture, configuration, API, and feature-specific guides, see the [CROWler documentation index](README.md).
