# Information Seed Provider Health and Operator Checklist

This page records provider observations and the safe operating policy for
Information Seed discovery. It does **not** change any deployment allow-list or
re-enable disabled seeds automatically.

## Observed provider matrix

The matrix below reflects the reporter-supplied observations from the
2026-11-10 development rig and 2026-11-11 production comparison. Endpoints and
credentials are intentionally omitted; use the deployment configuration as the
source of truth. Revalidate before changing production policy.

| Provider | Type | Observed outcome | Recommended state |
| --- | --- | --- | --- |
| `hospitalitynet_all_news_rss` | RSS/Atom | Successful response | Keep enabled if current URL remains valid |
| `hospitalitynet_europe_news_rss` | RSS/Atom | Successful response | Keep enabled if current URL remains valid |
| `hyatt_news_releases_rss` | RSS/Atom | Successful response | Keep enabled if current URL remains valid |
| `pitchfork_news_rss` | RSS/Atom | Successful response | Keep enabled if current URL remains valid |
| `mubutv_music_business_rss` | RSS/Atom | Successful response | Keep enabled if current URL remains valid |
| `gdelt_doc_search` | GDELT DOC | Timeout from the observed egress | Use the dedicated `gdelt_doc` adapter; keep bounded retries and investigate egress latency |
| `common_crawl_latest` | Common Crawl CDX | HTTP 504 observed | Treat as upstream/transport failure; use bounded backoff and a verified current index |
| `music_news_rss` | RSS/Atom | HTTP 404 | Verify the publisher's current official feed before replacing the URL |
| `hospitalitynet_usa_canada_news_rss` | RSS/Atom | HTTP 404 | Verify the publisher's current official feed before replacing the URL |
| `ein_music_industry_rss` | RSS/Atom | HTTP 403 | Treat as forbidden/access-controlled; do not bypass controls |
| `ihg_corporate_news_rss` | RSS/Atom | HTTP 403 | Treat as forbidden/access-controlled; do not bypass controls |
| `ihg_americas_news_rss` | RSS/Atom | HTTP 403 | Treat as forbidden/access-controlled; do not bypass controls |
| `ihg_emeaa_news_rss` | RSS/Atom | HTTP 403 | Treat as forbidden/access-controlled; do not bypass controls |
| `hotelnewsresource_rss` | RSS/Atom | Timeout on the rig, 404 in production | Verify feed availability before enabling |
| `travel_einnews_rss` | RSS/Atom | No successful observation supplied | Verify feed availability before enabling |

The five successful observations are a conservative starting point for an
opt-in deployment recommendation, not a guarantee of future uptime. The
repository contains no production credential values and does not modify a live
allow-list.

## Failure classification policy

- **403 / forbidden**: non-retryable by default. Do not add stealth, browser
  spoofing, or access-control bypass. Use documented feeds, approved API
  credentials, or permissioned alternatives.
- **404 / not found**: non-retryable until the publisher's current official feed
  location is verified. Do not fabricate replacement URLs.
- **429 / rate limited**: retryable with `Retry-After` when supplied and bounded
  exponential backoff otherwise.
- **5xx / timeout / transport**: retryable with bounded backoff. Common Crawl
  504s may be upstream or query-related and are not evidence that CROWler has a
  parser bug.
- **Malformed response**: parser failure, not an HTTP failure. Keep the response
  body out of logs except for bounded, redacted error summaries.

The in-memory provider limiter is process-local. Multiple engine replicas can
therefore exceed a shared upstream quota even when each process honors its own
`rate_limit`. Use deployment-level coordination or a shared limiter when strict
global politeness is required.

## Common Crawl index refresh

Common Crawl publishes available indexes through
`https://index.commoncrawl.org/collinfo.json`. On 2026-11-11 the newest index was
`CC-MAIN-2026-43` (October 2026). Repository examples use that verified index;
operators should periodically check the metadata endpoint and update their
deployment configuration rather than guessing a future collection ID.

## Operator verification checklist

1. Confirm the deployment configuration and provider allow-list; do not edit
   production seeds or credentials from this repository.
2. Revalidate each enabled endpoint from the production egress path.
3. Verify RSS/Atom XML or GDELT JSON validity separately from HTTP success.
4. Run one authenticated test seed with one known-good feed, then a link-only
   pass, then a new-source pass.
5. Confirm discovered candidates, new Sources created, and existing Sources
   linked are reported separately.
6. Exercise SIGTERM during a run and confirm the seed becomes claimable quickly
   instead of remaining `processing` for 30 minutes.
7. Exercise a controlled lost-heartbeat case and confirm the lease expires and
   the old worker cannot finalize the reclaimed claim.
8. Check retry fairness with new work plus old failures, and confirm the attempt
   ceiling/backoff policy.
9. Keep user-disabled production seeds disabled. Re-enable only with explicit
   operator approval.
