# WP-05B3C3A: Authenticated crawler handoff and fenced terminal results

## Status

**Design contract. B1 internal repository implemented; B2 live handoff pending.**

This document defines the contract for the next package, WP-05B3C3. It is
grounded in the claim foundation that already exists in schema 1.17. The
original package added an unexported, pure-Go validator set in
`pkg/database/source_crawl_handoff.go`. The approved B1 Option A implementation
now provides the internal heartbeat and terminal-result repository described in
[`ASYNC_GOAL_B3C3B1_B1_IMPLEMENTATION.md`](ASYNC_GOAL_B3C3B1_B1_IMPLEMENTATION.md);
no crawler caller exists yet. B2 is the separate live handoff package.

WP-05B3C3B1 corrected the authority and replay model: the authoritative claim
and history timestamps, fresh post-lock database clock, complete unbound input
sanitization and the narrower Option A persisted-replay equality are recorded in
[`ASYNC_GOAL_B3C3B1_DESIGN_REVIEW.md`](ASYNC_GOAL_B3C3B1_DESIGN_REVIEW.md). Where
this document and that review differ, the review is authoritative for the
current implementation.

Schema remains **1.17** and writer generation remains
**`verified-ownership-v1.17`**. The `source-crawl-attempt-v1` capability marker
and checksum are unchanged by this package.

## 1. In plain English

A claim is a lease on a source. It says "engine X owns source 7, attempt A,
generation 3, until time T". The claim is necessary, but it is not proof that a
browser actually loaded the page or that a specific approved crawl finished.

The next step is a handoff: the database gives one crawler worker the claim
identity, and that worker carries it through the crawl. When the worker is done,
it sends a terminal receipt back. The database alone decides whether the receipt
is still valid. A receipt from an expired attempt, a superseded generation, a
different engine or a different operation is rejected. A crash or a lost reply
must be safe to retry without inventing success.

Two rules matter most:

1. **A claim is not permission to execute after a later change.** Every
   privileged mutation must re-read the current claim, lease, policy and
   operation binding inside its own transaction.
2. **A finished crawl is not a finished goal.** Recording
   `Status='completed'` for one operation only says that this crawl finished.
   Goal acceptance still requires the deterministic evidence evaluator.

## 2. What exists today

- `SourceCrawlClaims` stores one current claim per source: attempt ID, claim
  request ID, generation, engine, claim time and lease expiry.
- `SourceCrawlAttempts` stores append-only history, including the immutable
  proposal/approval/operation binding when exactly one submitted operation is
  trustworthy.
- `ClaimSourceCrawlAttempts` is an internal opt-in API. It is not called by the
  crawler, `cmd/thecrowler`, an agent, MCP or any public interface.
- `update_sources` remains the legacy PostgreSQL claim path. In `fenced` mode it
  returns no rows. The default mode is `legacy`.
- Replay of a claim request now distinguishes executable `claimed`,
  non-executable `stale_fence` and safely unbound `unattributed`.
- `Status` records the scheduling operation. `CrawlProposals` and
  `CrawlApprovals` record the request and operator approval.

There is still no authenticated handoff and no operation-specific completion
write.

## 3. Handoff identity

The internal handoff would carry exactly:

| Field | Source |
| --- | --- |
| `source_id` | current claim |
| `claim_request_id` | current claim |
| `attempt_id` | current claim |
| `generation` | current claim |
| `engine_id` | current claim |
| `claimed_at` | attempt history |
| `lease_expires_at` | attempt history |
| `attribution` | claim result: `bound`, `unattributed`, `ambiguous` or `not_attributable` |
| optional operation binding | `operation_id`, request ID, run ID, agent ID, owner, principal user ID, approval ID and approval SHA-256 |

Construction and transport:

```text
ClaimSourceCrawlAttempts
    -> cmd/thecrowler source claim loop
    -> pkg/crawler.Pars
    -> pkg/crawler.ProcessContext
    -> future fenced terminal reporter
```

The existing `cdb.Source` field is not enough and must not be overloaded.
Attempt identity must never come from a URL, page metadata, a plugin, an agent
prompt, an event payload, source configuration or model text. It is produced by
the trusted database claim and carried as a separate typed field.

The legacy path stays unchanged: when no fenced handoff exists, the crawler
behaves exactly as it does today and the reporter is never invoked.

A handoff is an identity, not authority. A future worker may carry it, but the
database must re-establish current state at the privileged mutation. The
authoritative snapshot must include the joined current/history claim and lease
times; the worker's copy is never used to extend authority.

## 4. Attempt and operation lifecycle

An attempt remains `claimed` while its bound operation is `processing`. The
attempt lease is the execution fence; the operation status is the scheduling
and result record. They are related but not interchangeable.

Attempt and operation states relate as follows:

| Attempt | Bound operation | Meaning |
| --- | --- | --- |
| `claimed` | `accepted` | Claimed, work not yet started. |
| `claimed` | `processing` | Worker has begun; the lease must stay live. |
| `claimed` | `completed` / `failed` / `cancelled` | Terminal result committed; the attempt becomes terminal in the same transaction. |
| `superseded` / `expired` / `abandoned` | any | No further terminal write from that fence. |
| `completed` / `failed` / `cancelled` | matching terminal state | Historical; only exact idempotent replay. |

Authoritative begin-of-work: the first fenced mutation that moves a bound
operation from `accepted` to `processing` also sets `Status.last_updated_at`
from database time. If no begin receipt arrives, a terminal receipt may still
move `accepted` directly to a terminal state; the operation was already
accepted by the scheduler.

### Heartbeat and lease renewal

- Uses fresh database time after the relevant locks, never only the worker's
  wall clock. PostgreSQL `clock_timestamp()` is required where a deliberate lock
  wait can cross an expiry; `CURRENT_TIMESTAMP` is transaction-start time.
- Claim batches use a fresh timestamp per source after its row lock. A final
  freshness check demotes **every** newly allocated attempt whose short lease
  expired during a long batch to `stale_fence`, regardless of whether it is
  bound or `unattributed`/`ambiguous`/`not_attributable`/`conflict`. It is never
  returned in a live-looking outcome. The durable history remains unchanged and
  the caller must revalidate the fence before any privileged mutation.
- Must match the exact `source_id`, `attempt_id`, `generation`,
  `claim_request_id` and `engine_id`.
- Extends by a bounded amount. The proposed maximum is one hour per renewal and
  24 hours after `claimed_at` in total.
- Rejects a stale, expired, superseded or terminal attempt. A terminal attempt
  never returns to `claimed`.
- A source deletion, disablement or ownership transfer is checked under the
  same source lock. Deletion or disablement stops renewal. Ownership transfer
  does not rewrite the immutable operation binding, but it prevents the old
  worker from overwriting source-level status later.
- A heartbeat racing with supersession either commits before the new claim and
  keeps the old lease for its remaining validity, or loses to the source/claim
  lock and is rejected as `stale_fence`.
- Crash recovery is lease expiry, not a background resurrection. A new claim
  allocates a new generation and marks the old attempt `superseded`.

The existing schema can represent these states. No new column is required for
the minimal heartbeat.

## 5. Terminal receipt contract

The future `Report...` entry point returns a closed result:

```text
applied
idempotent_replay
stale_fence
conflict
unavailable
unsupported
```

A receipt carries the exact attempt identity, the attribution class, one of
`completed`, `failed` or `cancelled`, the matching reason code
(`crawl_completed`, `crawl_failed`, `crawl_cancelled`), an observation time, and
— only for a bound attempt — the exact operation binding plus the expected and
resulting `CrawlOperationState`.

One bounded database transaction would:

1. Take the shared policy lock and confirm `fenced`.
2. Lock the source row, then the current claim and attempt history in the
   documented order.
3. Recheck the exact attempt ID, source, generation, engine, claim request and
   lease using database time.
4. Check that the source is not deleted or disabled at the point of mutation.
5. For a bound attempt, read the immutable proposal, approval and `Status`
   rows and verify the deterministic operation key, run, agent, owner,
   principal, approval identity and status chronology. These reads are
   read-only; the reporter does not lock proposal or approval rows.
6. Apply a compare-and-set on the attempt (`attempt_id`, `generation`,
   `state='claimed'`) and on the operation (`status_sha256`, expected
   `accepted` or `processing`). The result may only be `completed`, `failed` or
   `cancelled`.
7. Persist terminal attempt state, `terminal_at`, a bounded reason code and the
   operation result in the same transaction. The immutable scheduling identity
   in `Status.details` is not rewritten; `Status.status` and
   `Status.last_updated_at` carry the result.
8. Update source-level status only when the same fence is still current. A
   later generation, transferred owner or disabled source preserves the source
   row and records only the operation-specific result.
9. Commit once. Any failure rolls back the attempt state, the operation state
   and the source update together.

An unbound attempt must never fabricate a `Status` operation completion,
because it has no operation identity. Its source-level status update is denied
by default: no persisted operation principal proves owner continuity. A future
B1 repository may record only its attempt-history result unless a separately
approved owner-continuity mechanism exists.

`Status='completed'` is reported completion of that crawl only. It is not goal
acceptance and must not be used as completion evidence without the existing
goal evaluator.

### Replay and commit uncertainty

- The same persisted attempt identity, outcome, reason and operation result is
  `idempotent_replay`, including after lease expiry or supersession. This is a
  separate read-only path and never renews a lease or writes again.
- A different engine, claim request, source, generation, claim time, lease,
  binding, outcome, reason or operation result is `conflict`.
- A changed observation time for the same result is an explicitly ignored
  retry field; the stored `terminal_at` remains authoritative. The incoming
  value must still satisfy `claimed_at <= observed_at < lease_expires_at`
  against the receipt-carried lease. An observation exactly at or after the
  original lease expiry conflicts even on the read-only path.
- `ExpectedState` is a first-write precondition, not a persisted replay-identity
  field. The original unbound class is normalized to `unbound`.
- The incoming receipt must pass first-write structural validation even on the
  read-only replay path. Matching durable IDs alone are insufficient.
- Reusing an original `ClaimRequestID` after a heartbeat renewed its lease
  returns `conflict`: the original requested duration can no longer be proven
  from the renewed fields. The lease is never reset.
- Where exact equality cannot be reconstructed from 1.17 tables, the limitation
  is documented rather than invented. See the B3C3B1 decision table.
- If a commit reply is lost, the worker re-reads the exact attempt/operation
  identity and returns `idempotent_replay` if the stored result matches.
  Otherwise it returns `conflict`, never invented success.
- A lookup failure is `unavailable`; absence is not proof of failure.

## 6. Difficult cases

| Case | Decision |
| --- | --- |
| Two accepted operations for one source, unbound attempt | The claim is `ambiguous`. Only a source-level result is allowed; no operation completion. |
| Proposal arrives after the claim was allocated | The attempt stays unbound. A new attempt is needed to bind it; do not retroactively bind committed history. |
| Older attempt finishes after a newer generation exists | `stale_fence`. The old worker cannot write a result or source status. The operation remains available to the current fence. |
| Lease expires mid-crawl | Every privileged mutation fails `stale_fence`. The crawl may still be running, but it can no longer write a result. |
| Heartbeat races with supersession | Source/claim lock serializes them; one wins, the other sees `stale_fence`. |
| Owner transfer after claim, before completion | The operation result remains historically valid, but the old worker cannot overwrite source-level status. |
| User or agent revoked while crawling | Account revocation blocks new authorization, not a factual result for an already accepted operation. The receipt still needs the exact fence. Any new privileged action needs fresh authorization. |
| Approval expires after accepted scheduling | Irrelevant to terminal recording; expiry bounds submission, not later completion. |
| Worker restarts and replays its last receipt | Exact replay is idempotent if the stored receipt matches. |
| Two identical results | `idempotent_replay`. |
| Conflicting result arrives later | `conflict`; never overwrite the first terminal result. |
| Commit succeeds, reply lost | Re-read and reconcile; `idempotent_replay` or `conflict`, never fabricated success. |
| Browser/VDI/email failure or panic during shutdown | `failed` with a bounded reason, if the lease is still valid. A panic after the lease is lost is `stale_fence`. |
| `closeSession` currently writes `Sources.status` by URL | The future fenced path must branch on a non-nil handoff and call the fenced reporter by source ID. It must not call the URL-based legacy update. The legacy path is unchanged when no handoff exists. |
| No operation binding | Source-level result only. No fabricated `Status` completion. |
| Old binary alive across cutover | Drain before switching. Old workers receive no new work in `fenced` mode; in-flight legacy work is not retroactively fenced. |

## 7. Lock order and isolation

Database locks are held for milliseconds. A crawl lease is held for the length
of a crawl. They are different mechanisms and must not be conflated.

Proposed reporter order:

```text
shared policy advisory lock
  -> Sources row
  -> SourceCrawlClaims / SourceCrawlAttempts
  -> read-only CrawlProposals / CrawlApprovals
  -> Status compare-and-set
  -> audit and source status
```

Comparison with existing paths:

- Scheduler: proposal row -> source -> approval -> users -> `Status`.
- Approval issuance/revocation: source -> users/grants -> quota -> proposal.
- New claim: shared policy lock -> source -> current claim/history -> read-only
  proposals/approvals/`Status`.
- Legacy `update_sources`: shared policy lock -> sources.
- Ownership transfer: source -> users/grants.
- Async goal checkpoint: goal row only. The reporter never locks goal rows;
  a later goal transition re-reads its own evidence.

The reporter does not lock proposal or approval rows, so it cannot form a
proposal/source cycle with scheduling. The source row serializes scheduling and
terminal writes for the same source. Approval issuance and the reporter share
the source lock and never wait on each other's quota/proposal rows.

SQLite uses its writer transaction. A bounded retry is allowed only for
definite serialization/deadlock failures (for example SQLSTATE `40001` or
`40P01`). Business conflicts (`stale_fence`, `conflict`, expired lease) are
never retried blindly.

No SQL transaction may remain open during a browser, model, email or network
operation.

## 8. Compatibility and rollout

1. Deploy a handoff-capable worker while `legacy` mode is active. With no
   handoff it must follow the legacy path exactly.
2. Drain or expire every `processing` source that has no attempt row. Those
   in-flight legacy claims cannot be fenced after the fact.
3. Call the operator-only `set_source_crawl_claim_mode('fenced')`.
4. Start fenced workers. They claim through `ClaimSourceCrawlAttempts`.
5. Rollback requires stopping/draining fenced workers first, then calling
   `set_source_crawl_claim_mode('legacy')`. A fenced worker that outlives the
   rollback fails closed; it must not write a terminal result in legacy mode.
6. MySQL remains on its documented legacy path. It has no attempt tables and
   must not receive this protocol without a separate approval.

No automatic mode flip exists.

## 9. Schema assessment

The existing schema supports the minimal protocol:

- `SourceCrawlClaims` provides the current fence and lease.
- `SourceCrawlAttempts` provides append-only terminal state, `terminal_at` and
  `reason_code`.
- `Status` provides the operation state and update time.
- The immutable proposal/approval binding is already stored on the attempt.

A future durable request fingerprint and empty-batch acknowledgment are **not**
implemented. They would require an additive schema change (a canonical request
digest keyed by claim request ID, plus a durable record for an empty result)
and a separately approved migration/addon. A computed in-memory digest without
durable storage does not fix replay after restart.

If a future implementation needs a separate terminal-receipt ledger with an
explicit receipt digest and source-status outcome, that is a second additive
decision. It is not required to define the protocol above.

## 10. Pure implementation boundary

`pkg/database/source_crawl_handoff.go` contains unexported validators for the
handoff, lease extension, terminal receipt, authority snapshot, state tables and
replay classification. It has no production caller, does not open a database,
does not write `Status`, does not start a worker and does not register an event,
API or configuration field.

`sourceCrawlClaimRequestFingerprint` is a pure canonical digest for future use.
It is deliberately not persisted.

## 11. Approval required before Stage B

Before implementing a terminal-result repository, heartbeat writer, live
crawler handoff, attempt-result migration, worker activation or automatic goal
resume, the maintainer must record a separate explicit decision covering:

- the exact terminal transaction and CAS contract;
- whether the minimal schema is sufficient or an additive receipt/fingerprint
  record is approved;
- the heartbeat cadence, extension and maximum lifetime;
- source status ownership and owner-transfer behaviour;
- rollout/drain/rollback authorization;
- tests and operational evidence.

Until that decision exists, this package stops at the pure contract. The
approval record is requested in
[`ASYNC_GOAL_B3C3_STAGE_B_APPROVAL_REQUEST.md`](ASYNC_GOAL_B3C3_STAGE_B_APPROVAL_REQUEST.md).
