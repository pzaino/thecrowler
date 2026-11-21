# WP-05B3C3B2V independent review

## 1. Baseline

| Item | Value |
| --- | --- |
| Repository | `pzaino/thecrowler` |
| Branch | `develop` |
| Reviewed commit | `f0f4fc97da6e2e5975b4cf26e5b8b4610324d4e2` |
| Predecessor | `aa906c86671ca1b5dd3e8c31c0210ad40951be90` |
| Release | `v2.3.0` |
| Schema | `1.17` |
| Writer generation | `verified-ownership-v1.17` |
| Working tree at start | clean |

`f0f4fc97` is the B2R commit on top of `aa906c86`. Its 25 changed files match
the B2R patch: no schema, migration, capability checksum, default mode or
public contract changed.

Hosted CI on this exact commit: 8/8 successful, including
`postgres-integration`
(https://github.com/pzaino/thecrowler/actions/runs/44305245883/job/134804995471).

## 2. Verification method

- Source tracing: claim queue -> `startCrawling` -> VDI acquisition ->
  `CrawlWebsite` -> worker goroutines -> indexing transactions ->
  `closeSession` -> terminal repository.
- Race/ownership review of every read/write of `sessionFinalized`,
  `sessionQuarantined`, `VDIReturned`, crawl contexts, WaitGroups and the VDI
  pool slot.
- Transaction/lock review of every current-state write and the complete
  ownership-provenance write path.
- Deterministic Go tests, the full suite, `-race`, vet and both CGO builds.
- Disposable PostgreSQL 17 integration (35 source-crawl tests plus the real
  proposal/approval/scheduler/claim/observation test).
- Isolated Docker Compose stack build and live crawler scenarios; see
  `ASYNC_GOAL_B3C3B2V_TEST_REPORT.md`.

## 3. Verified execution ownership and cancellation graph

```text
retrieveAvailableSources (engine)
  -> legacy: update_sources
  -> fenced: ClaimSourceCrawlAttempts (policy lock -> Sources -> claim -> history)
       -> claimedSource{Source, Handoff}
            -> startCrawling goroutine (wg.Add)
                 -> ValidateFencedClaimStartup (pre-VDI)
                 -> vdiPool.Acquire (blocks; slot reserved)
                 -> CrawlWebsite goroutine
                      -> second ValidateFencedClaimStartup
                      -> heartbeat goroutine (renews exact attempt)
                      -> protocol branch
                           -> worker goroutines / indexing transactions
                           -> netinfo goroutine (wgNetInfo)
                      -> done channel
                 <- releaseVDI channel
                 -> vdiPool.Release
```

- `closeSession` is deferred from `CrawlWebsite` and is the only place that
  finalizes the attempt and calls `WG.Done` after the crawl starts.
- Early `CrawlWebsite` exits before `closeSession` is registered release the
  VDI and call `WG.Done` themselves.
- `startCrawling` calls `WG.Done` for its own pre-VDI/VDI-acquisition failures.
- The terminal repository writes the attempt CAS, optional `Status` CAS and
  optional source status in one transaction.

`ValidateTx` holds the `Sources` row lock (PostgreSQL `FOR UPDATE`) until the
caller commits. The terminal reporter takes the same row lock first. This is
the serialization point that makes "validation then write" atomic with respect
to terminal reporting, supersession, heartbeat and ownership transfer.

## 4. Findings

### B2V-01 — BLOCKER (fixed): unbound owner continuity was not sound

**Where:** `pkg/database/source_crawl_result.go`
(`sourceCrawlOwnershipContinuityTx`, terminal ownership block),
`pkg/database/source_crawl_handoff.go`
(`sourceCrawlTerminalSourceStatusEligible`), `pkg/database/source_ownership.go`
and `pkg/database/postgresql-setup.pgsql`.

**Defect:** B2R authorised unbound source-status mutation when
`SourceOwnership.user_id == Sources.usr_id` and
`SourceOwnership.last_updated_at <= claimed_at`. Both the ownership upsert and
the `BEFORE UPDATE` trigger write `CURRENT_TIMESTAMP`, which PostgreSQL
evaluates at **transaction start**, not commit time. A transfer transaction
that starts before the claim and commits after it records a timestamp older
than the claim while changing the owner. The comparison therefore passed for
an owner who did not exist at claim time.

**Reproduction:**
`TestPostgresSourceCrawlB2RProvenanceTimestampIsNotContinuity` starts the
transfer transaction, claims the source, then performs the transfer and
commits. It asserts that the stored `last_updated_at` is **not later than**
`claimed_at` while the owner is `8`. Under the old rule, the terminal report
would have updated the new owner's source status; the test requires the
mutation to be suppressed.

**Fix:** schema 1.17 has no durable claim-time owner for an unbound attempt.
Unbound source-status mutation is suppressed unconditionally. The attempt
terminal result is still recorded; the source stays `processing` and becomes
claimable again when the claim lease expires
(`selectSourceCrawlClaimSourcesTx` admits `processing` sources whose current
claim lease has expired).

**Regression tests:**
`TestSourceCrawlB2RUnboundSourceStatusSQLite` (including lease-expiry reclaim),
`TestPostgresSourceCrawlB2RUnboundOwnershipTransfer`,
`TestPostgresSourceCrawlB2RUnboundSuppressionThenTransfer`,
`TestPostgresSourceCrawlB2RProvenanceTimestampIsNotContinuity`,
`TestRetrieveAvailableSourcesFencedSQLite`.

**Residual:** B3 Decision 1 proposes a durable `claim_owner_user_id`.

### B2V-02 — HIGH (fixed): quarantine could race and panic the worker

**Where:** `pkg/crawler/source_crawl_handoff.go`
(`quarantineSession`), `pkg/crawler/crawler.go` (`closeSession`).

**Defect:** B2R quarantine set `VDIReturned = true` from the timeout goroutine
while the worker could read that field (for example in the performance-log
path) and the worker's own return later saw the flag and became a no-op.
`closeSession` then also cleared `newLinks`, `visitedLinks` and
`CollectedCookies`, cleaned the KV session, set `Status.EndTime` and set
`pStatus = 10` while the quarantined worker could still be running.
`CrawlInitialURL` performs an unconditional `ctx.visitedLinks[fURL] = true`,
so clearing the map could make a still-running worker panic with
"assignment to entry in nil map"; the other field writes were data races.

**Fix:** quarantine only marks the session so the finalizer skips
`ReturnVDIInstance` and skips all shared-state cleanup and status writes. It no
longer touches `VDIReturned`. If the worker eventually finishes, its own
return safely closes and returns the session; if it never finishes, the slot
stays out of the pool by design.

**Regression tests:** `TestQuarantineSession`,
`TestCloseSessionDoesNotReturnQuarantinedVDI` (asserts the release channel is
empty, `VDIReturned` stays false, shared maps and `pStatus` are preserved, and
a late map write does not panic).

**Residual:** an uninterruptible WebDriver call is still not cancellable; only
subsequent operations stop. Documented as an activation limitation.

### B2V-03 — MEDIUM (fixed): failed network-information collection

**Where:** `pkg/crawler/crawler.go` (`SourceProtocolNetwork`),
`pkg/crawler/source_crawl_handoff.go`.

**Defect:** B2R fixed a failed `IndexNetInfo`, but `GetNetInfo` records its
error in `Status.NetInfoRunning` and returns without an error value. A
network-only source whose only work (DNS/whois/netlookup) failed could still
insert an empty artifact and be reported `completed`.

**Fix:** the network-only branch treats collector state `3` as a failed crawl
(`networkInfoCollectionFailed`), records the error and returns it, so the
terminal outcome is `failed`.

**Test:** `TestNetworkInfoCollectionFailed` plus the live network-only
scenario.

### B2V-04 — HIGH (fixed): claim binder refused a `processing` operation

**Where:** `pkg/database/source_crawl_attempt.go`
(`resolveSourceCrawlCandidateTx`).

**Defect:** terminal validation accepted `Status='accepted'` or
`'processing'`, but the claim binder only bound `accepted`. A scheduled
operation that was already `processing` when the crawler claimed its source was
marked `not_attributable`, so the attempt stayed unbound and could never send a
terminal receipt for that operation. Live scenario LIVE-08 reproduced this: the
accepted fixture bound and completed, while the processing fixture was crawled
unbound and its operation remained `processing`.

**Fix:** the binder accepts `accepted` and `processing`. `already_pending`
remains a scheduling acknowledgement and is never bound. The immutable binding,
proposal/approval chronology and deterministic operation key checks are
unchanged, and the terminal CAS still reads the authoritative row.

**Tests:** `TestSourceCrawlB2RClaimBindsProcessingOperationSQLite`,
`TestPostgresSourceCrawlB2RClaimBindsProcessingOperation`, and live LIVE-08.

### B2V-05 — HIGH (fixed): fenced page-indexing failures could still report completed

**Where:** `pkg/crawler/crawler.go` (`indexPageContext`, worker error
drain), `pkg/crawler/web.go` (`CrawlInitialURL`).

**Defect:** page workers recorded indexing errors in the worker error channel,
but only errors containing the legacy `errCriticalError` marker failed the
crawl. A fenced crawl whose pages could not be indexed — for example the
fenced `refresh_content` refusal — logged the error, indexed nothing, and still
reached a `completed` terminal result. Live LIVE-10-REFRESH reproduced this
before the fix: source 31, attempt `completed`, logs
`Error indexing page ...: refresh_content is not supported`, no indexed page.

**Fix:** `indexPageContext` wraps every error with the `errIndexingFailure`
sentinel. The worker drain fails a fenced crawl on such an error instead of
logging it as non-fatal, and `CrawlInitialURL` propagates an initial-page
indexing failure for fenced crawls. Legacy behaviour is unchanged.

**Tests:** `TestFencedRefreshContentIsRejected` and live LIVE-10-REFRESH.
A fenced crawl now fails if any in-scope page cannot be indexed; that is the
truthful completion rule for evidence-bearing work.

### B2V-06 — MEDIUM (documented): terminal delivery is single-shot

`SourceCrawlHandoff.ReportTerminal` marks the handoff terminal and performs one
bounded database attempt. If the database is unavailable, the terminal result
is not persisted and the attempt remains `claimed` until the lease expires.
The failure is logged at error level; no completion event or evidence is
emitted. This is safe but not recoverable within the same run. Replay semantics
were not changed.

### B2V-07 — MEDIUM (documented): append-only and external effects

- Screenshot capture/insert (`TakeScreenshot`,
  `insertScreenshotWithTimeSeries`) is not transaction-fenced and can run
  after a timeout if the worker reaches it. Screenshots are optional metadata,
  not completion evidence. `source_screenshot`/`full_site_screenshot` are
  false in the default configuration.
- Time-series rows, files and screenshots are append-only projections; they are
  not rolled back when a fence is lost after their transaction commits.
- Plugins, agents, email and WebDriver calls already in flight are not
  interrupted; only subsequent operations are stopped.

### B2V-08 — LOW (documented): fail-closed activation blockers

- Fenced `refresh_content` is refused because delete and replace are separate
  transactions.
- Fenced `null:` source deletion is quarantined because asynchronous deletion
  by source ID cannot prove the fence.

Both remain fail-closed; B3 Decisions 2 and 3 cover the safe designs.

### B2V-09 — LOW (documented): claim request idempotency limits

A claim-request replay proves the persisted attempts, not the original limit,
priority or timing parameters; an empty batch has no durable acknowledgement.
Workers use fresh 256-bit request IDs per batch. B3 Decision 5 covers this.

## 5. False-positive corrections against B2R documentation

1. B2R claimed the provenance timestamp proves unbound owner continuity. It
   does not (B2V-01). The B2R review/test reports now carry a correction note.
2. B2R described quarantine as setting `VDIReturned` so the worker's return is
   a no-op. That is the defect fixed in B2V-02.
3. B2R described unbound source status as written inside the terminal
   transaction "when owner continuity is proven". That path no longer exists;
   unbound status is suppressed.

## 6. Current-state and destructive-write inventory

| Path | Write | `ValidateTx` in the same transaction? | Fence loss / failure | Evidence role |
| --- | --- | --- | --- | --- |
| `SearchIndex`, `SourceSearchIndex` | upsert | yes (main tx, start and pre-commit) | rolls back | current state |
| `WebObjects`, `WebObjectsIndex` | replace | yes (same tx) | rolls back | current state |
| Keywords | upsert | yes for PostgreSQL (`upsertPostgresKeywords`); portable path pre-checks only | PostgreSQL rolls back; SQLite test-only | current state |
| Meta tags | upsert | yes | rolls back | current state |
| Object attributes | insert/upsert | yes | rolls back | current state |
| Time series | append | yes | rolls back for the row | projection only |
| `NetInfo`, `HTTPInfo` | insert | yes (`indexNetInfo`) | rolls back | current state |
| Screenshots/files | insert + external storage | no | may append after timeout | metadata only |
| Source status (bound) | terminal tx | terminal source lock | suppressed unless verified owner | current state |
| Source status (unbound) | suppressed | n/a | never written | n/a |
| Completion event | audit insert | only after applied + completed | suppressed on quarantine | not acceptance proof |
| `refresh_content` delete | destructive | refused in fenced mode | no write | blocker |
| `null:` deletion | destructive | quarantined | no delete | blocker |
| Browser/plugin/agent/email calls | external | context checks before each operation | in-flight not interruptible | no authority |

## 7. Lock order (verified)

```text
shared policy advisory lock (claim allocation, legacy update_sources)
  -> Sources row (FOR UPDATE)
      -> SourceCrawlClaims row
      -> SourceCrawlAttempts row
          -> read-only CrawlProposals / CrawlApprovals / Status
              -> attempt CAS / Status CAS / source status write
                  -> commit
```

- Claim: policy shared lock -> source -> current claim -> history.
- Heartbeat: source -> current claim -> history.
- Terminal: source -> history (initial) -> current claim -> history (locked) ->
  proposal/approval/Status reads -> attempt CAS -> Status CAS -> source status.
- `ValidateTx`: policy read -> source `FOR UPDATE` -> current claim -> history
  -> database clock; the source lock is held to commit.
- Ownership transfer: source -> users/grants.
- No SQL lock spans a browser, email, plugin or network call.

Deadlock analysis: all paths take `Sources` first and claim/history in the same
order; `ValidateTx` is invoked as the first statement of each indexing
transaction. `refresh_content` and `null:` deletion are refused before locks.

## 8. Live readiness

Code-level review and deterministic/PostgreSQL verification are complete. The
full-stack Docker build, service startup and live VDI scenarios were executed
on the isolated `b2v-e2e` project; see
`ASYNC_GOAL_B3C3B2V_TEST_REPORT.md`.

Live results: PREPARE, LIVE-01, LIVE-02, LIVE-03 (enabled/disabled), LIVE-04,
LIVE-05, LIVE-07, LIVE-08, LIVE-09, LIVE-10 (null and refresh), LIVE-11 and
LIVE-12 passed with persisted DB assertions. LIVE-06 (a claim queued past its
lease while waiting for a VDI) is **BLOCKED**: the engine claims only when a
pipeline slot is free, so the supported scheduling path cannot hold a claim
behind a VDI. The expiry/supersession boundary is covered by deterministic
SQLite and PostgreSQL tests instead.

Two defects were found during live testing and fixed: the claim binder refused
`processing` operations (B2V-04), and fenced page-indexing failures could still
report `completed` (B2V-05). The stack is not production-ready; LIVE-06 remains
unverified live.

Unresolved before production activation:

- B3 Decision 1 (unbound owner), Decision 2 (atomic refresh) and Decision 3
  (exact-fence `null:` deletion) are not implemented.
- Terminal delivery has no retry; lease expiry is the recovery path.
- In-flight external calls cannot be interrupted.
- SQLite fenced execution remains test-only.
