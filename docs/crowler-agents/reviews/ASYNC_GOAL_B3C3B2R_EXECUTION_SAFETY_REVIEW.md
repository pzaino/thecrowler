# WP-05B3C3B2R execution-safety review

## Status

**Implemented in the working tree. Not production-ready. Not deployed.**

> **Corrected by WP-05B3C3B2V.** Independent verification found two defects in
> this review's design: (1) the unbound owner-continuity rule based on
> `SourceOwnership.last_updated_at <= claimed_at` is not sound on PostgreSQL,
> because both the ownership upsert and the trigger write `CURRENT_TIMESTAMP`
> (transaction start), so a transfer that begins before the claim and commits
> after it can carry an older timestamp; unbound source-status mutation is now
> suppressed entirely. (2) Quarantining a session also marked `VDIReturned`,
> and `closeSession` cleared shared crawl state while the worker could still
> run; quarantine now only suppresses the finalizer's release and cleanup.
> See `ASYNC_GOAL_B3C3B2V_INDEPENDENT_REVIEW.md`.

B2R hardens the approved B2 live fenced crawler handoff. It adds no schema
object, migration, default-mode change, public API, configuration field or
agent manifest. Schema remains **1.17**, writer generation remains
**`verified-ownership-v1.17`**, the `source-crawl-attempt-v1` capability marker
and checksum are unchanged, and the default claim mode remains `legacy`.

B3 (additive schema), automatic goal resumption, automatic mode switching and
production activation remain unapproved.

## 1. Correctness findings addressed

| Finding | B2R change |
| --- | --- |
| Network-only crawl logged an index error but returned `nil`, so deferred terminal reporting could record `completed`. | The branch now returns the indexing error through `networkOnlyCrawlResult`; cancellation is classified separately. Terminal reporting maps it to `failed`. |
| Timeout returned while the crawl goroutine and its indexing transactions could still be running, and the deferred finalizer could release the VDI. | Timeout records one stable reason, cancels the crawl context, waits a bounded grace for worker quiescence, quarantines the session if it does not quiesce, and finalizes exactly once. |
| `closeSession` used `pStatus` (also a browser-failure marker) as its finalization guard, so a browser-level failure could skip the wait-group and VDI release entirely. | A dedicated `sessionFinalized` flag, guarded by the existing mutex, makes finalization exactly once. |
| A queued claim could expire, be superseded or lose its source before work started. | `ValidateCurrent` is a bounded, read-only authoritative check called before VDI acquisition and again immediately before work. `ValidateTx` performs the same check while holding the source row lock inside current-state mutation transactions. |
| An unbound attempt could overwrite source status after ownership transfer or an expired claim. | Unbound source-status mutation now requires durable owner-continuity proof and happens inside the terminal transaction. Otherwise it is suppressed. |
| `RequestTerminal` always claimed `ExpectedState: accepted`, so a valid `processing` operation could never terminate. | The worker supplies no expected state. The repository reads the authoritative `Status` row and permits a terminal CAS from `accepted` or `processing`. |
| `refresh_content` deletes old content in a separate transaction before the replacement page transaction. | Fenced runs refuse `refresh_content`; it is an activation blocker rather than an unsafe partial implementation. |
| `null:` source deletion is asynchronous by source ID and cannot prove fence continuity. | Fenced `null:` sources are quarantined: no deletion, a terminal failure is recorded. Legacy behavior is unchanged. |
| A mode-read failure silently fell back to legacy. | Only a genuinely absent optional attempt schema or an unsupported backend (MySQL) uses legacy. A present but unreadable/invalid fenced store fails closed. |
| Fenced completion events ignored `crawler.create_event_when_done`. | The configured flag is honoured; a fenced event is emitted only for an applied, genuinely `completed` terminal result, never a replay. Events are suppressed while a session is quarantined. |
| The crawl worker wrote the shared `err` that the deferred terminal reporter reads (data race and outcome leak), and non-fatal configuration errors could leak into the terminal outcome. | The worker uses a local error variable; configuration load/merge errors are local and never reach the terminal decision. |

## 2. Execution flow after B2R

```text
operator: set_source_crawl_claim_mode('fenced')
        |
        v
cmd/thecrowler.retrieveAvailableSources
  schema absent / MySQL  -> legacy update_sources (documented)
  present + policy error -> fail closed (no legacy work)
  legacy                 -> legacy update_sources
  fenced                 -> ClaimSourceCrawlAttempts (256-bit request id, 30 min lease)
        |
        v
claimedSource{Source, Handoff} queue
        |
        v
startCrawling
  ValidateFencedClaimStartup  (before the blocking VDI acquisition)
  vdiPool.Acquire
  CrawlWebsite
    ValidateFencedClaimStartup (immediately before work)
    heartbeat (5 min renewal / 5 min extension)
    protocol branch
      web:    ConnectToVDI -> navigation/actions -> indexPageContext
      email:  mail runner -> extracted links
      network: GetNetInfo -> IndexNetInfo
    timeout: reason -> abort -> bounded quiescence -> quarantine if needed
    closeSession (exactly once)
      terminal transaction (attempt CAS + optional Status CAS + optional source status)
      optional completion event (configured, applied only)
```

## 3. Trust boundaries

| Fact | Trusted source | Rejected input |
| --- | --- | --- |
| Operator mode | `SourceCrawlClaimPolicy` row read by the engine | worker, event, model, manifest |
| Attempt identity, generation, claim request, engine | `SourceCrawlClaims` + `SourceCrawlAttempts` under lock | handoff-only fields are compared, never authority |
| Lease | fresh database clock after the source lock | worker wall clock, cached lease |
| Source liveness | `Sources.deleted_at`, `disabled`, `status` | URL, event, configuration |
| Operation state | `Status.status` read under the terminal transaction | worker-supplied `ExpectedState` |
| Bound ownership | verified `SourceOwnership` for the immutable operation principal | current `usr_id` alone |
| Unbound owner continuity | verified `SourceOwnership.user_id == Sources.usr_id` **and** `last_updated_at <= claimed_at` | current `usr_id` alone |
| Terminal outcome | closed vocabulary + outbox/CAS | model text, event payload, source-level event |

An unbound attempt has no persisted operation principal in schema 1.17. The
provenance timestamp is the only durable owner-continuity evidence: a transfer
or any later verification rewrites `last_updated_at`, so the check fails
closed. Manufacturing an owner from the current `usr_id` is never done.

## 4. Lock order

```text
shared policy advisory lock (claim allocation, legacy update_sources)
  -> Sources row
      -> SourceCrawlClaims row
      -> SourceCrawlAttempts row
          -> read-only CrawlProposals / CrawlApprovals / Status
              -> Status CAS / attempt CAS / source status write
                  -> commit
```

- Claim allocation: policy shared lock -> source -> current claim -> history.
- Heartbeat: source -> current claim -> history.
- Terminal report: source -> history -> current claim -> history (locked) ->
  proposal/approval/Status reads -> attempt CAS -> Status CAS -> source write.
- B2R `ValidateTx`: policy read -> source `FOR UPDATE` -> current claim ->
  history -> database clock. It holds the source row until the caller commits.
- `refresh_content` is refused before any lock is taken.

Because the terminal reporter and `ValidateTx` both take the source row, a
current-state write that validated first commits before a terminal report can
proceed, and a terminal report that committed first makes any later indexing
transaction fail its validation. No SQL lock spans a browser, email, plugin or
network call.

## 5. Current-state and external-side-effect inventory

| Path | Kind | Fenced behavior | Residual |
| --- | --- | --- | --- |
| `SearchIndex`, `SourceSearchIndex` | current state (upsert) | `ValidateTx` at transaction start and again before commit | none for the main row |
| `WebObjects`, `WebObjectsIndex` | current state (replace) | inside the same validated main transaction | none |
| Keyword persistence | current state | PostgreSQL transaction takes `ValidateTx`; portable path pre-checks | SQLite fenced execution is test-only by documented policy |
| Meta tags | current state | `ValidateTx` on its transaction | none |
| Object attributes | append/current | `ValidateTx` on its transaction | none |
| Time-series emission | append-only projection | `ValidateTx` on its transaction | append-only rows are not rolled back |
| Network/HTTP information | current state | `ValidateTx` on the `indexNetInfo` transaction | none |
| Screenshots and files | append-only / external storage | not transactionally rolled back | documented |
| Email-derived indexes | current state | crawl context + `ValidateTx` on page indexing | mailbox reads are external |
| Source status | current state | only the terminal transaction writes it | unbound suppresses without continuity |
| Completion events | audit | configured flag, applied terminal only | not completion proof |
| Plugin/agent rule calls | external | crawl-context check before each call | an in-flight call is not interrupted |
| Browser/VDI operations | external | checks before connect, navigation, retry | an in-flight WebDriver call is not interrupted |
| `refresh_content` delete | destructive | refused for fenced runs | activation blocker |
| `null:` source deletion | destructive | quarantined for fenced runs | activation blocker |

## 6. Failure classification

`decideCrawlTerminalOutcome` is the single terminal decision:

1. crawl context aborted (fence loss, timeout, shutdown) -> `cancelled`;
2. error is `context.Canceled`/`context.DeadlineExceeded` -> `cancelled`;
3. any other error -> `failed`;
4. otherwise -> `completed`.

`networkOnlyCrawlResult` classifies a network-only indexing error the same way
before it reaches that decision, so an ordinary indexing failure is `failed`
and never `completed`.

The handoff marks itself terminal on the first report; the database CAS rejects
a second different outcome. A late completion report after `failed` or
`cancelled` is a read-only replay conflict.

## 7. VDI quarantine

If the crawl worker does not quiesce within `crawlShutdownGrace` (30 s), the
session is neither returned to the pool nor closed:

- `VDIReturned` is set so the finishing worker's own return is a no-op;
- `closeSession` skips `ReturnVDIInstance` and `QuitSelenium`;
- the pool slot is deliberately consumed until the operator recycles the VDI.

This trades capacity for safety. A WebDriver call cannot be interrupted, so a
timeout is a bound, not a guarantee of quiescence.

## 8. Activation blockers and residual risks

1. **`refresh_content`** is refused in fenced mode. Atomic delete-and-replace
   needs a single transaction contract (or attempt provenance on objects) that
   schema 1.17 does not provide. This is an activation blocker for sources that
   rely on refresh.
2. **`null:` deletion** is quarantined in fenced mode. An exact-fence deletion
   protocol is required before it can be enabled.
3. **Unbound source status** is suppressed when provenance continuity cannot be
   proven. The source then remains `processing` until the existing
   processing-timeout/lease reclaim path makes it eligible again. A durable
   claim-owner field would remove this limitation; it requires B3 approval.
4. **Append-only writes** (screenshots, files, time-series rows) are not rolled
   back if the fence is lost after their transaction commits. They are not
   completion evidence.
5. **In-flight external calls** (WebDriver, email, plugin, agent) cannot be
   interrupted; only subsequent operations are stopped.
6. **SQLite** has no row locks. `ValidateTx` is serialized by the SQLite writer
   transaction, but production fenced mutation continues to rely on PostgreSQL
   row locking, as documented for B1.
7. **Queued claims** are protected by the 30-minute claim lease, not by an
   active heartbeat. The pre-start check is what prevents an expired queued
   claim from starting work.
8. **Live VDI/WebDriver end-to-end** was not executed in this work package.

## 9. What is deliberately unchanged

- Default claim mode `legacy`; cutover is operator-only through
  `set_source_crawl_claim_mode`.
- MySQL remains legacy-only.
- `Sources.status` for bound attempts keeps the B1 verified-owner and immutable
  operation-binding policy.
- Async goal observation and eligibility remain read-only.
- No dispatcher, remote mutation, agent resumption or automatic approval
  execution exists.
