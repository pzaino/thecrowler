# Release 2.3.0 RC3 baseline and source review

Work package: `WP-REL230-RC3`
Repository: `pzaino/thecrowler`
Branch: `develop`
Frozen SHA: `ef28d7672fe7134f4d277f673fe28a9d2f4424a3`
Date: 2026-11-20
Release: `v2.3.0`
Database schema: `1.17` (`RequiredSchemaVersion`)
Writer generation: `verified-ownership-v1.17`

This document records the baseline, the disposition of every RC1/RC2 finding,
and a source-grounded review of the release-risk code paths. It is not an
approval and does not tag, merge, push or deploy anything.

## 1. Baseline

| Item | Value |
| --- | --- |
| Working tree | clean (`git status --porcelain` empty) |
| Branch / tracking | `develop` → `origin/develop` |
| Local HEAD | `ef28d7672fe7134f4d277f673fe28a9d2f4424a3` |
| `origin/develop` | `ef28d7672fe7134f4d277f673fe28a9d2f4424a3` (identical) |
| `RELEASE_VERSION.txt` | `v2.3.0` |
| `RequiredSchemaVersion` | `1.17` |
| `WriterGeneration` | `verified-ownership-v1.17` |
| Default claim policy | `legacy` (seeded row) |
| Default `crawler.refresh_content` | `false` |

The branch has **not** moved since the RC2 delivery. The only commit between the
RC2 baseline (`e6a41d31`) and the frozen SHA is the RC2 delivery itself:

```
ef28d767 added Shared-Object Integrity, Shipping-Image Validation, and Final 2.3.0 Release Gate
```

Its 21 changed files are exactly the RC2 patch set previously reviewed:

```
 .github/workflows/postgresql-integration.yml                       |  39 +-
 Dockerfile.events / Dockerfile.searchapi / Dockerfile.thecrowler   |   9 + each
 docs/crowler-agents/reviews/ASYNC_GOAL_MAINTAINER_DECISIONS.md     |  97 +
 docs/crowler-agents/reviews/RELEASE_230_GO_NO_GO.md                |  17 +
 docs/crowler-agents/reviews/RELEASE_230_PRE_RELEASE_RUNBOOK.md     |  23 +-
 docs/crowler-agents/reviews/RELEASE_230_RC2_GO_NO_GO.md            | 164 +
 docs/crowler-agents/reviews/RELEASE_230_RC2_IMPLEMENTATION.md      | 313 +
 docs/crowler-agents/reviews/RELEASE_230_RC2_TEST_REPORT.md         | 246 +
 docs/test_policy.md                                                |  64 +-
 pkg/crawler/crawler.go                                             | 157 +-
 pkg/crawler/fenced_refresh.go                                      |  42 +-
 pkg/crawler/fenced_refresh_concurrency_postgres_integration_test.go| 491 +-
 pkg/crawler/webobject_postgres_integration_test.go                 | 311 +-
 pkg/database/db_migrations/postgresql-migration-v1.17.pgsql        |  88 +
 pkg/database/postgresql-setup.pgsql                                |  46 +-
 pkg/database/schema_sql_test.go                                    |  83 +
 services/events/event_retention_postgres_integration_test.go       | 125 +
 services/events/event_retention_test.go                            |  71 +
 services/events/main.go                                            |  58 +-
```

No intervening commit introduces a new schema version, migration, public API,
configuration default, or execution path. The freeze at `ef28d767` therefore
inherits the RC2 scope exactly.

The delivered RC2 patch (`thecrowler-rc2.patch`, SHA-256
`3932dfde65724b6883e6006a749dc3dc8b8a8cb74a1cb81f4cd7fe732dedc080`) has the
same 21-file, +1931/−531 shape as this commit, so the reviewed patch was applied
verbatim.

## 2. RC2 finding ledger

| # | Finding | Proof | Status |
| --- | --- | --- | --- |
| 1 | Orphan-cleanup trigger could silently delete another page's committed object link (RC1 B-01, P0 for `refresh_content`) | `handle_shared_entity_deletion()` locks the parent row before counting; `TestPostgresSharedObjectTriggerPreservesConcurrentReference` asserts `object_rows=1`, `page_y_links=1`, attributes intact; 25-iteration stress; zero-reference deletion still works | **CLOSED** |
| 2 | Legacy `refresh_content` deleted hash-shared objects globally (RC1 B-02, P1) | `deleteWebObjects`/`getIndexIDForURL` removed; both modes share the page-scoped, orphan-only in-transaction replacement; `TestPostgresLegacyRefreshPreservesSharedObject` | **CLOSED** |
| 3 | Cross-source page refresh mutated associations another source depends on | Page row `FOR UPDATE` plus link-count refusal under the lock; `TestPostgresLegacyRefreshRefusesCrossSourcePage`, `TestPostgresFencedRefreshRefusesCrossSourcePage`; live scenario S-06 | **CLOSED (behaviour change, documented)** |
| 4 | `refresh_content` serialises page indexing | Single advisory key `crowler:fenced-content-refresh`; RC2 measured ≈0.7 s for 286 links. Correctness unaffected | **ACCEPTED / DOCUMENTED** (P2-1) |
| 5 | Legacy refresh failure reports source `completed` with `last_error` | Source trace in §4; live scenario S-05 | **OPEN DECISION** (P2-2) |
| 6 | SQLite Events janitor uses SQL `now()` | `cleanupExpiredEvents` query fails on SQLite, logs, deletes nothing | **OPEN, P3** (documented, out of RC2/RC3 approval) |
| 7 | `automatic_events_removal: never` does not make events permanent | `eventRemovalAction` plus janitor TTL; `TestEventRemovalActionPolicy`, `TestPostgresEventJanitorDeletesOnlyExpiredRows` | **CLARIFIED / DOCUMENTED** |
| 8 | Startup ruleset/plugin warnings | `crowler-rulesets-schema.json` vs `crowler-ruleset-schema.json`; `./plugins/` directory | **OPEN, P3 cosmetic, pre-existing** |
| 9 | Live Ollama / live external MCP never executed | No endpoint/model configured | **UNVERIFIED, NOT RUN** |
| 10 | MySQL runtime never executed | No server; MySQL remains legacy-only by contract | **UNVERIFIED, NOT RUN** |
| 11 | `mcp-authorization-e2e` never executed | `workflow_dispatch`-only, not registered on the default branch | **BLOCKED, HOSTED** |

Missing evidence is reported as `UNVERIFIED`; it is never counted as `PASS`.

## 3. Authorization confirmation

`docs/crowler-agents/reviews/ASYNC_GOAL_MAINTAINER_DECISIONS.md`, Decision 1o,
records the two approvals for work package `WP-REL230-RC2/RC2-A+RC2-B`
(approver: repository maintainer in session; UTC date 2026-11-20; both binary
answers captured):

- **RC2-A** authorises changing `handle_shared_entity_deletion()` so every
  artifact family locks the parent row `FOR UPDATE` before counting remaining
  links, in the fresh install and the v1.17 migration, including idempotent
  creation of the five triggers.
- **RC2-B** authorises replacing the legacy global object delete with the
  page-scoped unlink plus orphan-only deletion, sharing the fenced path's page
  lock, cross-source refusal and ordering.

Both changes are therefore inside a recorded approval. No ownership-epoch DDL,
no B3 decision, no new schema version and no fenced `null:` deletion was
authorised or implemented.

## 4. Legacy refresh terminal status (P2-2), precise path

This is the one finding RC3 examined most closely because it can mislead a
`Sources.status` consumer.

Source trace at `ef28d767`:

1. `pkg/crawler/web.go` `CrawlInitialURL` calls `ctx.IndexPage(pageInfo)`.
2. On error it calls `ctx.updateSourceState(err)`
   (`pkg/crawler/source_crawl_handoff.go:291`) and then
   `if ctx.sourceCrawl != nil { return err }`.
   - `updateSourceState` is a no-op for fenced crawls and calls
     `UpdateSourceState(*ctx.db, ctx.source.URL, crawlErr)` only for legacy.
   - Legacy therefore records `status='error'`, `last_error`, `last_error_at`
     and then **continues** with no returned error.
3. `pkg/crawler/crawler.go` `closeSession` finishes the legacy path with
   `UpdateSourceState(args.DB, args.Src.URL, err)` where `err` is the outer
   crawl error. For a non-critical page-indexing failure that value is `nil`.
4. `UpdateSourceState` (`pkg/crawler/crawler.go:1118`) executes, for a nil
   error, `UPDATE Sources SET last_crawled_at = NOW(), status = 'completed'` —
   it deliberately does **not** clear `last_error`.

Result: `status='completed'` with a populated `last_error`. This is the
long-standing legacy "non-critical page error does not fail the crawl" policy
(`fencedIndexingFailure(nil, …) == false`, pinned by
`TestFencedIndexingFailureClassification`), combined with a completion update
that does not reset the error column. It is **not** introduced by RC2:
RC2-B only moved the destructive refresh delete into the main transaction and
made refresh failures surface as `errIndexingFailure`, which the legacy worker
drain still classifies as non-fatal by design.

Impact assessment:

- Fenced crawls are unaffected: the terminal receipt maps an indexing failure to
  `failed` and `Sources.status='error'`.
- A legacy consumer that treats `status='completed'` as "every page indexed"
  can be misled by the retained `last_error`. A consumer that checks both
  fields, or the indexed rows, is not.
- The indexed data is correct: the refresh rolled back, the old generation is
  intact (live S-05 confirmed old object present, new object absent).

Disposition: **do not change it in RC3.** Altering legacy terminal
classification changes an established public behaviour and would need its own
narrow approval plus a documented compatibility note. The sign-off request asks
the maintainer to accept-and-document it (recommended) or schedule a separate
change. Operators are told to treat a non-empty `Sources.last_error` as
significant regardless of `status`.

## 5. Source review of the release-risk paths

### 5.1 `handle_shared_entity_deletion()`

Reviewed `pkg/database/postgresql-setup.pgsql` and the v1.17 migration body.

- All five families (`MetaTags`, `WebObjects`, `Keywords`, `NetInfo`,
  `HTTPInfo`) take `PERFORM 1 FROM <parent> WHERE <id> = OLD.<id> FOR UPDATE`
  and only count/delete when `FOUND`.
- `FOR UPDATE` (not `FOR NO KEY UPDATE`) is required: a foreign-key insertion
  into the link table takes `KEY SHARE` on the parent, which only conflicts with
  `FOR UPDATE`.
- The zero-reference delete is still performed, and is idempotent because the
  `FOUND` guard skips an already-deleted parent.
- `TestPostgresSharedEntityDeletionConcurrencyContract` pins the fresh-install
  and migration bodies as identical and asserts SQLite keeps its documented
  omission (no stored function, no equivalent trigger).
- RC3 recomputed the function MD5 independently on a fresh install and on a
  database migrated from a simulated pre-RC2 body: both
  `6d33f3492e828ea1334de93baeba5c95`.

### 5.2 Refresh flow (`crawler.go`, `fenced_refresh.go`)

- `indexPageContext` computes `fencedRefresh := config.Crawler.RefreshContent &&
  ctx.sourceCrawl != nil`, then routes both modes through the same
  page-lock/cross-source/link-replacement routine inside the main indexing
  transaction. There is no longer a separate destructive pre-transaction.
- `refresh_content == false` takes no refresh branch at all; legacy indexing is
  byte-for-byte the pre-RC2 path apart from the removed helper functions.
- The page row is locked `FOR UPDATE` before the cross-source count, so a
  concurrent `SourceSearchIndex` insert either commits first (and the refresh is
  refused) or waits.
- Old links are deleted page-scoped; an artifact row is deleted only when it has
  no remaining link. On PostgreSQL the pre-existing
  `trg_after_delete_webobjectsindex` normally performs that orphan delete during
  the link delete; the explicit helper remains authoritative on SQLite.
- `ValidateTx` is executed at transaction start and again immediately before
  commit for fenced crawls, so an expired/superseded claim or a disabled source
  cannot commit a replacement.
- Ordering: page-scoped object IDs are walked in ascending `object_id` order, in
  bounded batches of 128.
- `TestPostgresRefreshAndVacuumRemainConsistent` proves a refresh racing
  `VacuumSource` leaves the object existing exactly when a link remains.

### 5.3 `null:` protocol

`cmd/thecrowler/main.go` classifies a source whose URL starts with `null:`
(case-insensitive, trimmed) **after** the claim and the VDI slot assignment, and
skips crawling entirely:

- legacy: queues `source.Source.ID` for asynchronous deletion; no navigation, no
  indexing (`handleNullSourceDeletion`, `deleteChan`).
- fenced: logs a quarantine warning and reports a terminal failure through the
  handoff; **no deletion** (Decision 3 deferred).

This matches the documented intent: `null:` exercises claim, queue, VDI-slot and
lifecycle stages while skipping content collection. It is not a synonym for
"delete this source" and not an ordinary crawl.

### 5.4 Events retention

`eventRemovalAction(policy, success)` was extracted as a pure function; the
consumer path is unchanged. The janitor deletes only rows whose `expires_at` has
passed. `automatic_events_removal: never` means the consumer never removes or
expires a row — it does not suspend the janitor. `crawl_completed` events carry
a two-minute `expires_at`; default events carry five minutes. Both behaviours are
now covered by tests and stated in the operator documentation.

### 5.5 Shipping image provenance

The three Dockerfiles build inside the pinned
`golang@sha256:3889b425…` builder on `alpine@sha256:a8560b36…` and add:

```dockerfile
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}
```

`GOSUMDB` is untouched, `GOFLAGS` is untouched, `GONOSUMDB`/`GOPRIVATE`/
`GOINSECURE` are not set, and `go.sum` is not modified. Module integrity is
therefore still enforced by the checksum database and `go.sum`; the argument
only selects which proxy serves the modules. A build without the argument uses
the documented public default.

## 6. Residual risk register

| ID | Risk | Severity | Disposition |
| --- | --- | --- | --- |
| P2-1 | With `refresh_content: true`, all page indexing serialises on one advisory lock | P2 | Accept and document; revisit only for a deployment that needs refresh at scale |
| P2-2 | Legacy refresh failure leaves `status='completed'` with `last_error` | P2 | Maintainer decision requested (accept/document recommended) |
| P3-1 | SQLite Events janitor `now()` is not portable | P3 | Pre-existing; SQLite is not a supported Events deployment in 2.3.0 |
| P3-2 | Engine startup warnings about the ruleset schema filename and `./plugins/` | P3 | Cosmetic, pre-existing |
| P3-3 | Event TTL may surprise operators | P3 | Documented and tested |
| R-1 | Hosted CI covers `ef28d767`; any further change needs a rerun | Release process | Handled in `RELEASE_230_RC3_CI_AND_RELEASE_GATE.md` |
| R-2 | Live Ollama / live external MCP unverified | Evidence gap | Named `NOT RUN`; sign-off decision requested |
| R-3 | MySQL runtime unverified | Evidence gap | Legacy-only by contract; static/portable coverage only |

No new P0 or P1 defect was found in this review. The one failing test observed
during the RC3 full-suite run
(`TestSchedulerProcessesNotifiedSeedBeforePollingInterval`) is a load-sensitive
scheduler test unrelated to RC2: it passes 10/10 in isolation on the same tree
and the hosted `build` job passed on the same SHA. It is recorded as flaky, not
as a regression; see the test report.
