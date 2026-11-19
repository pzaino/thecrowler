# WP-05B3C3B2F1 test report

## 1. Status

**DECISION 2 IMPLEMENTED, TESTED AND LIVE-VERIFIED.**

The maintainer approved Decision 2 for implementation on 2026-11-18
([`ASYNC_GOAL_MAINTAINER_DECISIONS.md`](ASYNC_GOAL_MAINTAINER_DECISIONS.md),
Decision 1m). This package implements atomic fenced `refresh_content` on
schema 1.17 and preserves legacy behaviour.

| Item | Value |
| --- | --- |
| Repository | `pzaino/thecrowler` |
| Branch | `develop` |
| Reviewed HEAD at start | `be5572de9064c4e4020d5b8f3adf478de4952aad` |
| Release | `v2.3.0` |
| Schema | `1.17` |
| Writer generation | `verified-ownership-v1.17` |
| Go | `go1.27.3 linux/amd64` |
| Host | Linux, Docker 29.7.2-ce, Compose 5.6.1 |
| Disposable PostgreSQL | `postgres:17` (17.12) |
| Isolated Compose project | `b2f1-e2e` |
| VDI | `zfpsystems/crowler-vdi:latest` |

Hosted CI for the starting HEAD: 8/8 successful. The implementation patch has
not been pushed, so no hosted run exists for it.

## 2. Implementation

New file `pkg/crawler/fenced_refresh.go`; wiring in
`pkg/crawler/crawler.go` (`indexPageContext`). Details are in
[`ASYNC_GOAL_B3C3B2F1_REFRESH_DESIGN.md`](ASYNC_GOAL_B3C3B2F1_REFRESH_DESIGN.md)
section 5.7.

- `ValidateTx` (source row `FOR UPDATE`) runs at transaction start and again
  before commit.
- The existing page row is locked `FOR UPDATE`; a page linked to another source
  is refused before any write.
- The replacement object is written in the same transaction.
- Only page-scoped `WebObjectsIndex` rows are replaced; a `WebObjects` row is
  deleted only when no page references it.
- PostgreSQL uses a transaction-scoped advisory lock to prevent refresh
  deadlocks; SQLite relies on its writer serialization.
- Any error, expired/superseded claim or cancellation rolls the entire
  replacement back.
- Fenced page-indexing failures are returned by `worker` to the crawl drain
  (`fencedIndexingFailure`), so the attempt reports `failed`; legacy
  non-critical worker errors keep the historical log-and-continue behaviour.
- Legacy refresh and fenced `null:` quarantine are unchanged.

## 3. Local and integration checks

| Command | Result | Log |
| --- | --- | --- |
| `go test ./... -count=1 -timeout 90m -p 1` | PASS | `b2f1-impl-full.log` |
| `go test -race ./pkg/database/... ./pkg/crawler/... ./cmd/thecrowler/... -count=1` | PASS, no race | `b2f1-impl-race.log` |
| `go vet ./...` | PASS | `b2f1-impl-vet.log` |
| `go vet -tags=integration ./pkg/database ./pkg/crawler ./pkg/agent` | PASS | `b2f1-impl-veti.log` |
| `CGO_ENABLED=0 go build ./...` | PASS | local |
| `CGO_ENABLED=1 go build ./...` | PASS | local |
| `gofmt -l pkg cmd services` | PASS (no output) | `b2f1-impl-gofmt.log` |
| `git diff --check` | PASS | local |
| `actionlint .github/workflows/*.yml` | PASS | local |

### 3.1 SQLite (portable link/orphan logic)

`go test ./pkg/crawler -run '^TestFencedRefresh' -count=1` — PASS:

- `TestFencedRefreshReplacesPageLinksSQLite` — page links replaced; orphan
  object removed; a shared object and the other page's link/attributes kept.
- `TestFencedRefreshKeepsReplacementObjectSQLite` — the replacement object is
  never deleted, old object removed.
- `TestFencedRefreshRollbackRestoresOldContentSQLite` — rollback restores
  links and object.
- `TestFencedRefreshRefusesCrossSourcePageSQLite` — a page linked to a second
  source is refused; removing the second link makes it eligible.
- `TestFencedRefreshLocksOnlyExistingPageSQLite` — new page performs no
  cleanup.
- `TestFencedIndexingFailureClassification` — fenced indexing failures are
  fatal; legacy non-critical errors are not.

### 3.2 Disposable PostgreSQL 17

`go test -tags=integration ./pkg/crawler -run '^TestPostgresFencedRefresh' -count=1 -v`
— **5 PASS, 0 FAIL, 0 SKIP** (`b2f1-pg-crawler2.log`):

- `TestPostgresFencedRefreshAtomicReplacement` — real fenced claim; second
  crawl replaces the page object, old orphan removed, one link.
- `TestPostgresFencedRefreshPreservesSharedObject` — two pages with identical
  content share one object; refreshing one keeps the other's link and object.
- `TestPostgresFencedRefreshRefusesCrossSourcePage` — shared page refused;
  content unchanged.
- `TestPostgresFencedRefreshRollbackRestoresOldGeneration` — trigger-injected
  delete failure after link deletion; old links/object/content restored.
- `TestPostgresFencedRefreshRejectsExpiredClaim` — a lease-expired attempt
  cannot perform the refresh.

Legacy regression:
`TestPostgresWebObjectRefreshUsesReplacementPath` — PASS (the legacy
delete-then-replace path is unchanged).

`go test -tags=integration ./pkg/database -run '^TestPostgresSourceCrawl(Attempt|Result|B2R)'`
— 36 PASS, 0 FAIL, 0 SKIP (unchanged claim/terminal suite).

`TestPostgresKeywordSetPersistence` failed on timing in this environment. The
test is an existing 30-second suppression-window test; on the disposable
database used here its body took 38-56 s, so the final insert fell outside the
window and the timestamp changed. It was reproduced on the **clean base commit**
`be5572de` in `/tmp/opencode/b2f1-basecheck` (56.61 s, same failure), so it is a
pre-existing environment/timing limitation, not a regression from this change.
No keyword code was modified.

## 4. CI gate

`.github/workflows/postgresql-integration.yml` gains a dedicated
"Run fenced refresh PostgreSQL integration" step. It requires a visible
`=== RUN` and `--- PASS` for every `TestPostgresFencedRefresh*` test and fails
on any `--- SKIP`. The existing broad suite continues to run the same tests.

## 5. Live Docker / Selenium verification

Infrastructure: isolated Compose project `b2f1-e2e` (never the maintainer's
`crowler-*` project), PostgreSQL 17, rebuilt `crowler-api`,
`crowler-events`, `crowler-engine-1`, real `zfpsystems/crowler-vdi` browser
(Chromium 132) and two fixture aliases (`b2f1-fixture`,
`b2f1-fixture-alt`). Images were built from the patched tree with
`docker build --network=host`; the isolated database was initialised from
`postgresql-setup.pgsql`.

| ID | Scenario | Result | Evidence |
| --- | --- | --- | --- |
| PREPARE | Health, readiness, schema 1.17, writer, default `legacy`, services | PASS | 13 assertions |
| REF-01 | Legacy baseline refresh | PASS | legacy crawl created no fenced attempt; second crawl replaced the child object and deleted the old one |
| REF-02 | Fenced successful refresh | PASS | attempt generation 1 then 2, both `completed`; exactly one child object after refresh; old orphan removed |
| REF-03 | Forced mid-transaction replacement error | PASS | trigger-injected delete failure: attempt `failed`, old object/link/content restored |
| REF-04 | Fence loss during active refresh | PASS | claim expired during navigation; attempt never `completed`; old page content preserved |
| REF-05 | Two sources sharing a page | PASS | target linked to sources 13 and 14; refresh of source 13 `failed` with cross-source refusal; target content and both links preserved |
| REF-06 | Engine/VDI restart during refresh | PASS | interrupted attempt `superseded`, never `completed`; generation 3 reclaimed and completed with new content |
| REF-07 | Back-to-back refresh | PASS | two consecutive refreshes completed; one page link, latest content, intermediate object removed |
| REF-08 | Bound operation `accepted` and `processing` | PASS | both attempts `completed` with matching operation IDs; `Status` moved to `completed`; source `completed` |
| REF-09 | Fenced `null:` regression | PASS | fenced source retained, attempt not `completed`; legacy `null:` source deleted |
| REF-10 | Two concurrent fenced sources | PASS | distinct attempts, both completed, exactly one attempt per source, no deadlock |

Raw evidence: `/tmp/opencode/b2f1-e2e/results.ndjson` and the per-scenario
`/tmp/opencode/b2f1-e2e/ref*.out` files; service logs:
`docker compose -p b2f1-e2e logs`.

### 5.1 Live-discovered product defect (fixed)

The first REF-03 attempt completed despite the injected indexing failure. Root
cause: `worker` returned an indexing error only when its text contained the
legacy `[critical]` marker; a non-critical fenced indexing failure was logged
and swallowed before the terminal reporter. `fencedIndexingFailure` now
classifies the error at the worker boundary and returns it, so the attempt
reports `failed` and rollback preserves the old generation. This is a
correctness fix inside Decision 2's "indexing failures" requirement; the live
re-run passed. Regression:
`TestFencedIndexingFailureClassification`.

### 5.2 Harness-only corrections

These were test-environment issues, not product defects:

- the fixture now sends `Cache-Control: no-store` so browser HTTP caching
  cannot mask a content change;
- the test engine config disables `prevent_duplicate_urls` and sets a long
  `crawling_interval`/`crawling_if_error`; otherwise the crawler intentionally
  skips already-known URLs and re-queues completed sources;
- each scenario starts from an empty crawl database, and the delay control file
  is reset to zero, so one scenario cannot leak page/source state into the
  next;
- the harness waits for a *new* attempt generation after resetting a source
  instead of reading the previous terminal attempt;
- REF-06 also restarts the VDI container, because a navigation interrupted by
  an engine restart can leave the single Selenium session unavailable until
  its own timeout.

## 6. Compatibility and limitations

- Release `v2.3.0`, schema `1.17`, writer generation
  `verified-ownership-v1.17`, default claim mode `legacy` and MySQL
  legacy-only behaviour are unchanged.
- Legacy `refresh_content` still uses the independent delete transaction and
  retains its pre-existing cross-source/shared-object data-loss risk; that risk
  is documented in the design review, not changed here.
- Fenced refresh is refused for a page linked to another source; such sources
  keep their content but the refresh attempt fails.
- Post-commit keyword/metatag/attribute/time-series phases remain separate;
  `completed` means the page/object replacement committed, not whole-page
  replacement.
- `null:` fenced execution remains quarantined (Decision 3 deferred).
- The KeywordSetPersistence timing failure described above is pre-existing and
  reproduced on the base commit.

## 7. Evidence classification

Executed locally on the patched tree: sections 3, 4 and 5. The implementation
patch has not been pushed, so no hosted CI run exists for it. Live results were
obtained on the isolated `b2f1-e2e` stack, not on a shared deployment.
