# Information seed lifecycle

This document defines the lifecycle contract for rows in `InformationSeed` and
how discovery workers turn an information seed into linked `Sources` rows.
Workers claim work by polling the database; there are no database triggers,
notifications, or listeners that enqueue an inserted seed for processing.

## Status values

`InformationSeed.status` is a lower-case lifecycle state. The valid statuses are:

| Status | Meaning |
| --- | --- |
| `new` | A seed has been created and has not yet been claimed by a discovery worker. This is the default insertion state. |
| `pending` | A seed is intentionally queued for discovery or re-discovery, but has not yet been claimed. Use this when a caller wants to request a rerun without implying the row has never been processed. |
| `processing` | A worker has claimed the seed and is currently collecting candidates, validating them, and linking accepted sources. |
| `completed` | Processing finished without a lifecycle-blocking error. This includes successful discovery, no-op reruns, and exhausted searches that produce no accepted links. |
| `error` | Processing ended with a lifecycle-blocking provider or plugin/runtime error and should be retried only after the configured retry delay. |
| `disabled` | The seed is administratively inactive. Disabled seeds are not eligible for normal polling claims. |

A row may also have the boolean `disabled` flag. A row with `disabled = true`
must be treated as disabled even if `status` contains another value. Setting
`status = 'disabled'` is useful for human-readable state, while `disabled = true`
is the authoritative claim exclusion used by workers.

## Allowed transitions

Workers and API handlers should keep status changes within the following state
machine:

```mermaid
stateDiagram-v2
    [*] --> new: insert
    new --> processing: polled claim
    pending --> processing: polled claim
    processing --> completed: run finishes
    processing --> error: lifecycle-blocking error
    processing --> processing: stale processing row reclaimed
    error --> processing: retry delay elapsed and row reclaimed
    completed --> pending: explicit rerun request
    completed --> processing: explicit immediate rerun/claim
    error --> pending: manual retry reset
    new --> disabled: disable
    pending --> disabled: disable
    processing --> disabled: administrative stop
    completed --> disabled: disable
    error --> disabled: disable
    disabled --> pending: re-enable for rerun
    disabled --> new: re-enable as unprocessed
```

Additional rules:

- `new` and `pending` are always eligible to be claimed when `disabled = false`.
- `processing` is eligible only when it is stale: `last_processed_at` is `NULL` or
  older than the configured `processingTimeout`.
- `error` is eligible only when `last_error_at` is `NULL` or older than the
  configured retry delay.
- `completed` is terminal for automatic polling. It should move back to
  `pending` only because of an explicit rerun request or to `processing` only by
  an immediate manual claim/update path.
- `disabled` is terminal for automatic polling until the row is re-enabled and
  moved to `pending` or `new`.

## Timestamp semantics

| Column | Set when | Meaning |
| --- | --- | --- |
| `last_processed_at` | On every successful claim into `processing`; optionally updated again when the worker writes the final status. | The most recent time a worker began processing this seed. It is the stale-processing clock used to reclaim abandoned `processing` rows. It should not be used as proof that discovery completed successfully. |
| `last_error_at` | When a lifecycle-blocking provider, plugin validation, plugin timeout, or persistence error causes final status `error`. | The retry-backoff clock for `error` seeds. It should remain unchanged on successful reruns and on non-blocking partial failures that still complete. |
| `last_updated_at` | On any update to the seed row, either by DB-managed update timestamp behavior or by the application update statement. | The general audit timestamp for row mutation. It is not the claim, retry, or stale-processing clock. |

When a worker changes final status to `completed`, it should clear `last_error`
only if the previous error is no longer relevant to the current run. When a
worker changes final status to `error`, it must set both `last_error` and
`last_error_at`.

## Processing metadata fields

- `attempts` counts claim attempts, not only failed attempts. It is incremented
  when a row is claimed as `processing`, including initial runs, retries of
  `error` rows, and stale `processing` reclaims. It provides operational history
  and can be used by workers or operators to apply maximum-attempt policies.
- `engine` stores the identifier of the worker that most recently claimed the
  row. This is used for observability and to select rows claimed in a DBMS branch
  that cannot return changed rows directly. It should be overwritten on each new
  claim.
- `last_error` stores a concise, human-readable description of the latest
  lifecycle-blocking error. It should include enough context to distinguish
  provider failure, plugin rejection/timeout, validation error, or persistence
  failure, but should avoid unbounded logs or secrets.
- `priority` is a caller-defined scheduling hint. The current claim contract is
  FIFO by `created_at` and `information_seed_id`; priority is retained for APIs,
  operator filtering, and future scheduler policies. Workers that implement
  priority-aware polling must preserve the same idempotency and retry rules.

## End-to-end discovery contract

An enabled seed reaches a terminal lifecycle state through the same deterministic
phase order on every run:

1. A caller inserts an `InformationSeed` row with `status = 'new'` or
   `status = 'pending'`, `disabled = false`, and optional JSON in
   `InformationSeed.config`.
2. API and database package creation helpers wake the in-process scheduler for
   enabled `new` or `pending` seeds. PostgreSQL deployments also emit a
   `LISTEN/NOTIFY` message on `information_seed_created` so other processes can
   wake promptly.
3. The scheduler coalesces duplicate wake-ups and still polls on every
   `information_seed.query_timer` interval, so missed notifications, unsupported
   database backends, and process restarts recover through the normal polling
   path.
4. The claim helper atomically moves eligible rows to `processing`, records the
   claiming engine, increments `attempts`, and stamps `last_processed_at`.
5. The runner parses and validates `InformationSeed.config` into the per-run
   contract shown below.
6. Candidate processing always uses this canonical order: URL normalization and
   URL/host de-duplication, built-in filters, user candidate plugins, source
   override validation, then persistence/linking. Built-in filters enforce
   allowed domains, denied domains, required URL schemes, minimum score, maximum
   candidates per host/domain, and the seed/global candidate limit before any
   user plugin can run.
7. Custom candidate plugin phases run only after built-in provider discovery,
   URL normalization/de-duplication, and built-in filters. `candidate_plugins`
   is both the ordered execution list and the allow-list: when omitted, all
   registered candidate processors run in registration order; when present, only
   matching named processors run, in the seed-provided order, with duplicate or
   unknown names ignored. Plugins may reject candidates or apply the documented
   safe source overrides, but plugin output and source overrides are validated
   before any decision or override is applied. Plugins do not replace built-in
   source persistence, source/seed linking, lifecycle finalization, or event
   emission phases.
8. The runner writes `completed`, `error`, or `disabled` as the terminal status
   for the current attempt according to the final-status table.

## `InformationSeed.config` example

`InformationSeed.config` is seed-specific JSON. It selects from providers that
are already present in the global `information_seed.providers` map and allowed
by `information_seed.provider_allow_list`; it does not define provider
credentials. API-based providers (for example Brave Search, Bing Web Search, or a
custom `http_json` gateway) are the recommended production path. The
`browser_search` HTML adapter is disabled unless it is explicitly configured and
allow-listed; use it only for local fixtures or after reviewing the target site's
robots.txt, terms of service, consent flow, and rate-limit expectations. Its CSS
selectors are site-specific, credentials are stripped rather than sent, and strict
page/request/timeout/debug-output caps apply. All fields are optional, but this
example shows the complete production contract for the current runner:

```json
{
  "query_templates": [
    "{{ .Seed }} official site",
    "{{ .Seed }} research portal",
    "{{ .Seed }} annual report"
  ],
  "providers": ["public_json", "partner_search"],
  "tracking_params": ["utm_source", "utm_medium", "utm_campaign"],
  "deduplicate_host": true,
  "max_candidates": 25,
  "allowed_domains": ["example.invalid"],
  "denied_domains": ["ads.example.invalid"],
  "required_url_schemes": ["https"],
  "min_score": 0.2,
  "max_candidates_per_host": 3,
  "max_candidates_per_domain": 10,
  "source_name_template": "{{ .Seed }} — {{ .Candidate.Title }}",
  "source_priority": "normal",
  "restricted": 1,
  "flags": 0,
  "source_config": {
    "version": "1.0",
    "format_version": "1.0",
    "source_name": "information-seed-default",
    "crawling_config": {
      "site": "https://example.invalid/placeholder",
      "source_type": "website"
    },
    "custom": {
      "created_by": "information_seed"
    }
  },
  "candidate_plugins": ["domain-policy", "source-overrides"]
}
```

Configuration behavior:

- `query_templates` are rendered with the seed text and produce provider query
  strings. Literal `queries` may also be supplied; rendered and literal queries
  are bounded by `information_seed.max_queries_per_seed`.
- `providers` is an ordered selection of global provider names for this seed. If
  omitted, the runner uses the global provider allow-list order.
- `tracking_params` are removed during URL normalization before de-duplication
  and source lookup.
- `deduplicate_host` keeps at most one normalized candidate per host; otherwise
  duplicate filtering is by normalized URL.
- `max_candidates` is capped by `information_seed.max_candidates_per_seed` and is
  enforced during the built-in filter phase.
- `allowed_domains` keeps only candidates whose host is the listed domain or a
  subdomain. `denied_domains` rejects listed domains/subdomains after the allow
  check and before scoring or cardinality limits.
- `required_url_schemes` rejects candidates whose normalized URL scheme is not in
  the list. Normalization already limits candidates to HTTP(S), so this is most
  often used to require `https`.
- `min_score` rejects candidates below the configured score.
- `max_candidates_per_host` and `max_candidates_per_domain` keep the first N
  candidates in deterministic candidate order for each host or registrable
  domain.
- `source_name_template`, `source_priority`, `restricted`, `flags`, and
  `source_config` provide defaults for accepted candidates. Candidate plugins may
  override only the safe subset documented in the plugin contract.
- `candidate_plugins` is both a seed-level ordered execution list and an
  allow-list. When omitted, all registered information-seed candidate processors
  are eligible in registration order. When present, only named processors
  matching the list are selected, they run in the list order, and duplicate or
  unknown names are ignored.

## Final status behavior

Discovery runs often involve multiple providers followed by plugin validation.
The final status is determined by whether the run completed its lifecycle and
whether any accepted source links remain after validation.

| Outcome | Final status | Timestamp/error behavior |
| --- | --- | --- |
| All providers succeed and accepted sources are created or already exist, with `SourceInformationSeedIndex` links present. | `completed` | `last_processed_at` reflects the claim/run. Clear stale `last_error` if appropriate; do not update `last_error_at`. |
| One or more providers fail, but at least one candidate from another provider is accepted and linked. | `completed` | Treat provider failures as non-blocking partial failures. Record details in discovery metadata or logs rather than `last_error`, unless policy requires surfacing the warning. |
| Providers return zero results. | `completed` | This is a successful no-result run. Do not set `last_error`/`last_error_at`. |
| Providers return candidates, but all candidates are rejected by plugins. | `completed` | This is a successful filtered run. Do not set `last_error`/`last_error_at`; rejection reasons should be recorded in logs or per-candidate metadata when available. |
| Every provider fails before producing usable candidates. | `error` | Set `last_error` to the provider failure summary and set `last_error_at` to the finalization time. |
| A provider error prevents the worker from completing the discovery lifecycle, even if the provider set is not exhausted. | `error` | Set `last_error` and `last_error_at`. Retry is controlled by the `error` retry delay. |
| Plugin validation or plugin timeout rejects only some candidates and at least one accepted candidate is linked. | `completed` | Treat as partial validation failure. Keep the run completed and record validation details outside the lifecycle error fields. |
| Plugin validation or plugin timeout prevents all validation from completing, or policy requires plugin runtime errors to fail the whole run. | `error` | Set `last_error` with the plugin name/reason and set `last_error_at`. |
| Linking or source persistence fails for otherwise accepted candidates. | `error` | Set `last_error` and `last_error_at` because accepted work could not be made durable. |

## Candidate edge cases

| Edge case | Contract |
| --- | --- |
| Duplicate provider URLs after normalization. | Keep one candidate for the normalized URL and reject duplicate URLs before plugin processing. The final status remains governed by the remaining candidates. |
| Multiple URLs on the same host with `deduplicate_host = true`. | Keep one candidate for that host and reject later same-host candidates before plugin processing. |
| Existing `Sources.url` already matches an accepted normalized URL. | Reuse the existing `Sources` row and ensure the `SourceInformationSeedIndex` relationship exists. Do not create a duplicate source row. |
| Existing source/seed link already exists. | Treat the link as idempotent and update or preserve relationship discovery metadata according to database helper semantics. |
| A rerun discovers only already-linked sources. | Finish as `completed`; no new source rows are required for a successful rerun. |
| A rerun repairs missing source/seed links for existing sources. | Finish as `completed` after durable link repair. |
| A candidate plugin rejects every candidate without a lifecycle-blocking runtime error. | Finish as `completed` with rejection summaries rather than source rows. |
| A candidate plugin returns invalid source overrides or invalid `source_config`. | Treat the affected validation as plugin/persistence failure. If validation prevents durable completion, finish as `error`. |

In short: `completed` means the discovery lifecycle ran to a deterministic end,
not necessarily that new sources were found. `error` means the lifecycle could
not complete durably and automatic retry is appropriate.


## Discovery events

The seed runner emits operational events during each discovery run. Events that
belong to the seed as a whole use `source_id = 0` so they can be stored in the
existing `Events` table even when no `Sources` row exists yet. Events that refer
to a persisted candidate source use that source's `source_id`.

| Event type | Source ID | Meaning |
| --- | --- | --- |
| `information_seed.discovery_started` | `0` | The runner parsed the seed configuration, rendered queries, and began provider discovery. |
| `information_seed.candidate_found` | `0` | Provider discovery completed and the runner recorded the aggregate number of raw candidates found. |
| `information_seed.candidate_rejected` | `0` | One batched rejection event for the run when normalization, de-duplication, limits, or candidate processors reject candidates. Per-candidate rejection events are intentionally avoided to reduce noise. |
| `information_seed.source_created` | Created source ID | A candidate was persisted through `CreateSource`; the payload includes the current run counters and the source ID is stored in the event row. |
| `information_seed.discovery_completed` | `0` | Discovery reached a durable terminal `completed` status. This may include no-result runs and runs with non-blocking provider or processor warnings. |
| `information_seed.discovery_failed` | `0` | Discovery hit a lifecycle-blocking parse, query, provider, processor, persistence, or final status error and the seed is marked `error` when possible. |

Every information-seed event payload includes the same aggregate keys so Agents
and plugins can subscribe to any individual phase without needing a different
schema per event:

```json
{
  "information_seed_id": 123,
  "information_seed": "renewable energy market signals",
  "source_id": 0,
  "provider_counts": { "example_provider": 12 },
  "candidates_found": 12,
  "candidates_accepted": 8,
  "candidates_rejected": 4,
  "candidate_rejection_counts": {
    "duplicate_url": 2,
    "candidate_limit": 1,
    "candidate_processor": 1
  },
  "candidate_rejection_stages": {
    "normalization": { "duplicate_url": 2 },
    "built_in_filters": { "candidate_limit": 1 },
    "user_candidate_plugins": { "candidate_processor": 1 }
  },
  "sources_created": 8,
  "sources_linked": 8,
  "error_summaries": []
}
```

`candidate_rejection_stages` groups the same stable reason constants by the
phase that rejected them, including `normalization`, `built_in_filters`,
`user_candidate_plugins`, and `source_override_validation` when those phases
reject at least one candidate. `error_summaries` contains concise messages only;
provider, plugin, or database errors should be summarized rather than storing
unbounded logs or secrets in `Events.details`.

## Idempotency and reruns

Information seed processing must be safe to rerun after manual resets, stale
claim recovery, process crashes, and retry of `error` rows.

- `Sources.url` must not duplicate. Workers must normalize candidate URLs before
  lookup/insert and must use existing `Sources` rows for URLs that are already
  present.
- If a source already exists but the `(source_id, information_seed_id)`
  relationship is missing, the rerun must add the missing
  `SourceInformationSeedIndex` link.
- Link creation is idempotent. Duplicate source/seed pairs must be ignored or
  upserted without creating extra rows.
- Discovery metadata belongs on `SourceInformationSeedIndex`, not on
  `Sources.config`, because the same source may be discovered by multiple seeds.
  Reruns may merge or fill missing relationship metadata without clearing fields
  that were supplied by previous runs.
- A rerun that discovers only existing sources and repairs missing links should
  still finish as `completed`.
- A rerun that has no new candidates and no missing links to repair should also
  finish as `completed`.

## Creation notifications and polling fallback

Use exported database helpers such as `CreateInformationSeedAndNotify` for
application seed creation. They insert the row inside `pkg/database` and, when
the new row is enabled and immediately claimable (`status = 'new'` or
`status = 'pending'`), trigger scheduler wake-up behavior:

1. Every supported database wakes registered in-process schedulers through a
   coalesced channel. Multiple creation events that arrive while a scheduler is
   already awake collapse into one immediate claim cycle.
2. PostgreSQL additionally sends `pg_notify('information_seed_created', seed_id)`.
   Other CROWler processes that are listening on that channel wake without
   waiting for their next polling tick.
3. SQLite and MySQL rely on the in-process wake-up when the creator and
   scheduler share a process. When no local scheduler is registered, or a wake-up
   is missed, periodic polling remains the recovery mechanism.

Direct insertion into `InformationSeed` is still supported for integrations that
cannot call the helper, as long as the row uses a valid status, normally `new` or
`pending`, and `disabled = false`. Direct inserts do not emit the helper
notification, so they are discovered by the normal polling claim path:

1. A worker periodically calls the claim operation with a batch limit, engine
   identifier, `processingTimeout`, and retry delay.
2. The claim operation atomically changes eligible rows to `processing`, stamps
   `engine`, stamps `last_processed_at`, and increments `attempts`.
3. The worker processes claimed seeds and writes final `completed` or `error`
   state.

The scheduler always preserves stale processing recovery and retry delay
behavior because notifications only advance the next claim attempt; they do not
change the eligibility predicates used by `ClaimInformationSeeds`. Database
triggers may still exist for audit fields such as `last_updated_at`; those audit
triggers do not drive seed discovery.
