# WP-05B3C3B2 implementation: live fenced crawler handoff

## Status

**B2 implemented in the working tree. No deployment or production activation.**
**B3 remains not approved.**

The attributable approval is recorded in
[`ASYNC_GOAL_B3C3_STAGE_B_APPROVAL_REQUEST.md`](ASYNC_GOAL_B3C3_STAGE_B_APPROVAL_REQUEST.md):
repository maintainer, 2026-11-18, `WP-05B3C3/Stage-B-2`, B1 and B2 approved,
B3 not requested.

B2 uses the existing schema-1.17 attempt objects and the B1 repository. It adds
no table, column, index, capability marker, migration, default-mode change or
runtime grant.

## Claim acquisition

`cmd/thecrowler` reads the operator-owned `SourceCrawlClaimMode` before each
batch:

- `legacy` (or an unavailable attempt schema) keeps the existing
  `update_sources(...)` path unchanged.
- `fenced` calls `ClaimSourceCrawlAttempts` with a fresh 256-bit claim request
  ID, a bounded 30-minute lease, the configured priority, engine identity and
  selection intervals.
- If the mode changes to `legacy` between the mode read and the claim, the
  batch falls back to the legacy query. Any other claim error fails closed; the
  engine does not silently mix modes.

Each allocated claim is converted with `NewSourceCrawlHandoff`. The source row
is loaded by ID and queued with its handoff. Disabled or unloadable sources are
reported failed/cancelled rather than crawled. The queue type is internal to
`cmd/thecrowler`; the legacy `cdb.Source` queue behavior is unchanged when no
handoff is present.

## Handoff transport

The handoff is carried as:

```text
ClaimSourceCrawlAttempts
  -> claimedSource (cmd/thecrowler queue)
  -> crawler.Pars.SourceCrawl
  -> ProcessContext.sourceCrawl
  -> heartbeat and terminal reporter
```

It is never reconstructed from a URL, event, plugin, model, agent or source
configuration.

## Heartbeat and cancellation

`CrawlWebsite` starts one heartbeat per fenced source. It renews the exact
attempt every five minutes with a five-minute extension. A lost or conflicting
fence cancels the crawl context. A database outage is not treated as a lost
fence by itself, but the crawler aborts once the last known lease expires.

The crawl context is checked before:

- starting a protocol branch;
- connecting or reconnecting the VDI;
- every navigation and alternative-link retry;
- page and network-info indexing, including the main indexing commit and the
  destructive `refresh_content` delete;
- each action rule and each plugin/agent rule call;
- the email phase and its extracted web links;
- the crawl completion path.

WebDriver, email, plugin and agent calls that are already in flight cannot be
interrupted by the context; the check stops the next external operation. This
is a bounded, best-effort cancellation boundary, not a claim of transactional
external-effect rollback.

## Terminal reporting

`closeSession` reports the terminal result through the handoff and skips the
legacy URL-based `UpdateSourceState` for fenced crawls. The legacy source-state
event is emitted only when the terminal result is `applied`; it remains an audit
hint, not completion proof.

Early failures are covered:

- a `NewProcessContext` failure reports failed, releases the VDI slot and
  completes the wait group;
- VDI acquisition failure reports failed and completes the wait group;
- queued fenced sources that are never dequeued are reported cancelled by a
  bounded background drain at batch shutdown.

## Unbound source lifecycle

B1 deliberately leaves source status unchanged for an unbound terminal report
because schema 1.17 stores no unbound operation principal. Leaving ordinary
fenced sources in `processing` would make them ineligible for normal progress.

B2 therefore adds a **separate** fenced operational source-status update for
unbound attempts. It:

- is keyed by the exact current claim, attempt ID, generation, engine, claim
  request, claim time and lease;
- requires the exact terminal history state and the same unbound attempt;
- updates only `Sources.status`, `last_crawled_at`, `last_error`,
  `last_error_at` and `last_updated_at`;
- requires `Sources.status = 'processing'`, a live/non-deleted source and a
  current fence;
- is idempotent after the target status is already present;
- never creates a `Status` operation, operation binding or goal evidence.

Bound attempts continue to use the B1 bound-owner source policy only. The B2
unbound update does not relax B1's bound authorization, ownership verification
or operation identity checks.

## Fencing matrix

| Write or effect | Fenced behavior |
| --- | --- |
| Legacy source claim | Fenced mode returns no rows from `update_sources`; the claim API is used. |
| Early source state | Legacy URL update is suppressed; failed terminal report is used. |
| Crawl completion source state | Legacy URL update is suppressed; terminal report and unbound operational update are used. |
| Page index main transaction | Checked before the transaction and again before commit. |
| `refresh_content` delete | Checked immediately before the destructive delete. |
| Network-info index | Checked before indexing. |
| Screenshots/files/time series | Existing append-only behavior is retained; they are not rolled back. |
| Action rules | Checked before each rule. |
| Plugins/agents | Checked before each rule call. |
| Browser navigation | Checked before connect, navigation, reconnect and alternative-link retry. |
| Email retrieval | Crawl context is passed to the mail runner; a fence check precedes extracted web links. |
| Queued sources | Bounded drain reports cancelled at batch shutdown. |

## Schema and compatibility

- `RequiredSchemaVersion`: `1.17` (unchanged)
- `WriterGeneration`: `verified-ownership-v1.17` (unchanged)
- Attempt capability checksum: unchanged
- MySQL remains legacy-only; no attempt store
- No automatic mode switch, migration, deployment or runtime grant

## Legacy path

When no handoff is present, `Pars.SourceCrawl` and
`ProcessContext.sourceCrawl` are nil. Claim acquisition, source-state updates,
events, action execution and the VDI flow keep their existing behavior. The
fenced branch is only reachable when the operator has explicitly selected
`fenced` and the attempt schema is available.

## Limitations

- The heartbeat starts when `CrawlWebsite` starts. A queued source is protected
  by the 30-minute claim lease, not by an active heartbeat while it waits.
- WebDriver, email, plugin and agent calls that are already in flight are not
  interrupted mid-call.
- Indexing rows do not carry attempt provenance. The pre-commit fence check
  prevents most post-loss writes, but a narrow race remains between the final
  check and commit. Attempt-provenance indexing would require a later approved
  schema or indexing contract.
- A database outage can delay terminal reporting; the attempt lease eventually
  makes the source reclaimable.
- No live VDI/browser end-to-end run was performed in this environment. The
  tests cover the database adapter, claim branch, heartbeat, cancellation and
  terminal outcomes with SQLite and disposable PostgreSQL.

## Tests

See [`ASYNC_GOAL_B3C3B2_TEST_REPORT.md`](ASYNC_GOAL_B3C3B2_TEST_REPORT.md).
