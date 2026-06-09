# Email crawling dispatch design

## Scope

This document records the source-dispatch path in the current CROWler tree and
identifies the integration points for a future email crawler. It is a design
note only: no email transport, parsing, indexing, or dispatch behavior is
implemented by this change.

## Current source path

The runtime path from a database row to a crawler is:

1. `retrieveAvailableSources` in `main.go` calls the PostgreSQL
   `update_sources(...)` function and scans `source_id`, `url`, `restricted`,
   `flags`, and `config` into `database.Source`. The query does not filter or
   branch by URL scheme or `crawling_config.source_type`.
2. `crawlSources` places each `database.Source` on `sourceChan`. A VDI-slot
   worker receives it and calls `startCrawling`.
3. `startCrawling` constructs `crawler.Pars`, acquires a Selenium/VDI instance,
   and only then invokes `crawler.CrawlWebsite`.
4. `CrawlWebsite` constructs a `ProcessContext`, combines the source config
   with the global config, initializes timeout/session cleanup, and calls
   `classifySourceProtocol(args.Src.URL)`.
5. `classifySourceProtocol` in `pkg/crawler/protocol.go` recognizes only the
   literal, case-sensitive prefixes `http://`, `https://`, `ftp://`, and
   `ftps://` (after trimming surrounding whitespace). These map to
   `SourceProtocolWeb`; every other value maps to `SourceProtocolNetwork`.
6. `CrawlWebsite` sends `SourceProtocolWeb` through the Selenium-backed web
   path. Every other source is handled by the early network-only branch, which
   calls `GetNetInfo`, calls `IndexNetInfo`, updates the pipeline status and
   source state, and returns before `ConnectToVDI`.

Although the network-only branch returns before connecting the supplied
Selenium instance, the scheduler has already reserved that VDI in
`startCrawling`. Protocol dispatch therefore occurs too late to prevent a
non-web source from consuming a VDI slot for the duration of its run.

`pkg/crawler/process.go` is not currently a dispatch layer. `ProcessContext`
holds web/VDI state and the selected `database.Source`; its
`LoadSourceConfiguration` method unmarshals generic source JSON and prepares
web URL-pattern fields, but it does not select a processor.

`pkg/crawler/types.go` is also transport-neutral at the handoff boundary:
`Pars` carries the database source, status, database handle, rules engine,
source list, VDI pool, and wait-group information used by `CrawlWebsite`. The
status structure is oriented around the existing pipeline, network, HTTP, and
web-crawling stages and has no email-specific stage counter.

## Current result for email URLs

There is no email protocol family or email worker in the inspected crawler
code. Consequently:

- `imap://...`, `imaps://...`, `pop3://...`, `pop3s://...`, and any other
  unrecognized scheme classify as `SourceProtocolNetwork`.
- The classifier test explicitly fixes the current behavior for
  `imap://mail.example.com`: it expects `SourceProtocolNetwork`.
- Such a source is not routed to mailbox retrieval. It enters the existing
  NETInfo-only fallback after a VDI has already been acquired.
- `smtp` should not be treated as a crawl transport. SMTP is an outbound mail
  delivery protocol, while mailbox crawling should be based on retrieval
  protocols such as IMAP (and POP3 only if intentionally supported).

## Database and source-configuration findings

The `Sources` storage path is mostly protocol-agnostic:

- `database.Source.URL` is a string and `Config` is raw JSON.
- `CreateSource`, the policy-based upsert path, source lookup/list helpers, and
  status helpers persist and retrieve the URL/config without protocol
  dispatch.
- `validateURL` accepts any parseable URL with a non-empty scheme and host, so
  it does not itself restrict a source to HTTP(S).
- Queue selection in `retrieveAvailableSources` does not inspect the protocol
  or source type.

The typed configuration vocabulary is not yet email-ready:

- `pkg/database/source_types.go` defines source types for `website`, `api`,
  `file`, and `db`, but not `email`.
- `pkg/config/types.go` describes the same current set in `CrawlingConfig`.
- `schemas/crowler-source-config-schema.json` permits only `website`, `api`,
  `file`, and `db` for `crawling_config.source_type`.
- `database.Source.GetSourceType` defaults a missing or malformed source type
  to `website`; `IsAPI` only special-cases `api` and `data`. Neither helper is
  used by the current protocol dispatch in `CrawlWebsite`.

Email credentials and secrets should not be embedded in `Sources.url`. A
future email configuration should keep the URL as a non-secret endpoint and
mailbox selector, while referring to credentials through the project's secret
or credential mechanism. The exact credential schema is outside this
inspection.

## Proposed dispatch boundary

Introduce a source-level dispatcher **before VDI acquisition**, rather than
adding email handling inside the existing network fallback. Conceptually, the
scheduler path should become:

```text
Sources row
  -> source queue
  -> classify source protocol/type
       -> web     -> acquire VDI -> CrawlWebsite
       -> email   -> no VDI      -> CrawlEmail
       -> network -> no VDI      -> existing NETInfo-only processor
```

The best current integration point is the boundary represented by
`startCrawling`, because it has the complete `database.Source` and constructs
`crawler.Pars`, but has not yet acquired a VDI. In a future implementation,
prefer moving the decision into a crawler-package entry point (for example,
`ProcessSource` or `DispatchSource`) and having `startCrawling` call that entry
point. This keeps protocol policy in `pkg/crawler` instead of duplicating it in
`main.go`, while allowing only the web branch to acquire Selenium.

Do not route email from the current `SourceProtocolNetwork` branch in
`CrawlWebsite`. That branch has NETInfo semantics, uses web-oriented cleanup,
and is reached after the expensive scheduler resource decision has already
been made.

## Proposed integration points

### 1. Protocol classification (`pkg/crawler/protocol.go`)

- Add an explicit `SourceProtocolEmail` family.
- Parse the URL scheme rather than extending the current prefix list. Scheme
  parsing avoids prefix collisions and makes case normalization explicit.
- Initially recognize only retrieval schemes selected by product policy. A
  conservative first set is `imap` and `imaps`; add `pop3`/`pop3s` only if the
  email crawler supports their different mailbox and state semantics.
- Keep SMTP schemes unsupported rather than routing them as mailbox sources.
- Decide precedence between URL scheme and `crawling_config.source_type` in one
  place. A recommended rule is that a recognized scheme determines the
  transport, while `source_type: email` validates intent and permits future
  provider-specific endpoint forms. Conflicts should fail clearly rather than
  silently falling back to NETInfo.

### 2. Top-level source dispatch (`main.go` and `pkg/crawler`)

- Dispatch before `vdiPool.Acquire`.
- Retain the existing `Pars` handoff fields that are transport-neutral
  (`DB`, `Src`, `Status`, rules, refresh callback, and wait group).
- Make VDI acquisition and release exclusively owned by the web branch.
- Give the email and network-only branches completion paths that always finish
  the wait group and update pipeline/source state without pretending to return
  a Selenium session.
- Consider separating queue concurrency from VDI capacity. The current worker
  count and lifecycle are organized around VDI slots; email work may need a
  distinct bounded worker pool so mailbox I/O cannot starve web crawling or
  vice versa.

### 3. Email processor boundary (new crawler package code)

A future `CrawlEmail`/email processor should receive the selected source and
shared runtime dependencies, then own:

- endpoint and mailbox configuration validation;
- credential lookup;
- connection, TLS, and authentication;
- incremental state (for example IMAP UID validity and last processed UID);
- message and attachment limits;
- MIME parsing and normalization;
- indexing and deduplication;
- source status, counters, errors, timeout, and completion event handling.

It should not require `ProcessContext` as currently shaped. That type owns a
large amount of Selenium, page-link, cookie, HTTP, and DOM state. Extracting a
small transport-neutral lifecycle context is preferable to populating unused
web fields for email runs. `ProcessContext.LoadSourceConfiguration` is useful
as evidence that source JSON is available at runtime, but email-specific
configuration should be decoded into a typed email configuration at the email
processor boundary.

### 4. Source configuration and validation

When behavior is implemented, update all representations together:

- add `email` to `pkg/database/source_types.go`;
- add/describe `email` in `pkg/config/types.go`;
- add `email` to `schemas/crowler-source-config-schema.json`;
- add typed email settings rather than placing arbitrary credentials in
  `custom`;
- update source validation so required fields depend on the source type and
  protocol instead of assuming web-oriented `site` semantics everywhere;
- preserve backward compatibility: missing `source_type` should continue to
  behave as `website` for existing records unless a recognized non-web scheme
  gives an unambiguous transport.

No database column is required merely to dispatch email: the existing URL and
JSON config can represent it. A migration may still be needed for incremental
mailbox checkpoints or message identity if those values should be queryable,
transactional state rather than opaque source config.

### 5. Lifecycle, observability, and indexing

- Reuse the `Sources.status` lifecycle selected by `update_sources(...)` and
  completed through `UpdateSourceState`; otherwise email rows could remain in
  `processing` and be delayed or retried incorrectly.
- Preserve `Status.PipelineRunning`, timestamps, error count, and `LastError`.
  Add email-specific counters/stage state only if consumers require them;
  avoid overloading `CrawlingRunning`, `HTTPInfoRunning`, or
  `NetInfoRunning` with mailbox meanings.
- Decide whether the existing `crawl_completed` event is transport-neutral.
  If retained, include the source protocol/type in its details. Otherwise add
  a separate event type without breaking existing web consumers.
- Route normalized messages and attachments through explicit indexing APIs;
  do not force them through page/DOM methods merely to reuse web code.

## Tests to change when implementation begins

The current tests document only the two-family classifier. Future behavior
should be driven by tests at these seams:

1. **Classifier tests (`pkg/crawler/crawler_test.go`)**
   - change the existing IMAP expectation from network to email;
   - cover IMAPS and any intentionally supported POP schemes;
   - cover mixed-case schemes and surrounding whitespace;
   - verify SMTP and unknown schemes are rejected or remain in the explicitly
     chosen unsupported/network policy;
   - retain HTTP(S)/FTP(S) and bare-host behavior.
2. **Dispatcher tests**
   - verify web sources acquire/release a VDI and call the web processor;
   - verify email and network-only sources never acquire a VDI;
   - verify every branch completes the wait group and source lifecycle on
     success, validation failure, timeout, and panic/error boundaries.
3. **Database/config tests (`pkg/database/source_test.go` and config/schema
   tests)**
   - create and retrieve an email URL/config without losing scheme or config;
   - validate `source_type: email` consistently across Go types and JSON
     schema;
   - preserve the processing-row upsert protections and status-helper behavior;
   - test redaction so credentials never appear in status, events, or logs.
4. **Email processor tests**
   - use a fake protocol client; do not require a live mailbox;
   - cover incremental checkpoints, duplicate messages, UID validity changes,
     MIME alternatives, malformed messages, attachment limits, and partial
     failures.

## Recommended implementation sequence

1. Add tests for a three-family classifier and a pre-VDI dispatcher.
2. Refactor the current web and network-only paths behind that dispatcher
   without changing their behavior.
3. Extend the source-type schema and typed configuration with `email`.
4. Add an email processor using a fakeable client interface.
5. Add persistence for incremental mailbox state if config storage is not
   sufficient.
6. Add operational documentation for endpoint syntax, secrets, limits, and
   retry behavior.

This ordering first fixes the routing/resource boundary, then adds email
behavior behind a testable seam.
