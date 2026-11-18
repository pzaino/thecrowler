# WP-05B3C3B1 design review: fenced receipt authority

## Status

**Phase A complete. B1 Option A and B2 implemented. B3 not approved.**

The maintainer approved B1 Option A and B2 on 2026-11-18 under
`WP-05B3C3/Stage-B-2`. B1 is implemented in
[`ASYNC_GOAL_B3C3B1_B1_IMPLEMENTATION.md`](ASYNC_GOAL_B3C3B1_B1_IMPLEMENTATION.md)
with no crawler or service caller. B2 is implemented as the live crawler
handoff in
[`ASYNC_GOAL_B3C3B2_IMPLEMENTATION.md`](ASYNC_GOAL_B3C3B2_IMPLEMENTATION.md).
B3 remains not approved.

WP-05B3C3B1R closed the remaining authority, current/history and claim-timing
gaps. WP-05B3C3B1S closed the final-freshness and observation-time replay gaps.
This document records the corrected authority model, the persisted replay
equivalence that schema 1.17 can actually prove, the remaining limitations, the
crawler side-effect audit, and the exact boundary still required for B2.

## 1. In plain English

The database owns the claim. A worker's receipt is only a report. Therefore:

- a worker cannot extend its own authority by putting a later lease in the
  receipt;
- the database compares the receipt with the claim and history rows it reads
  under its own locks;
- an expired or superseded attempt cannot write a new result;
- an already committed result can be read and replayed idempotently even after
  its lease expired, but that read path never renews a lease or writes again;
- a crawl completion is not an agent-goal success.

Schema 1.17 can prove the attempt identity, the authoritative claim and lease
times, the immutable operation binding, the terminal outcome/reason and the
operation's terminal status. It cannot prove the original `ExpectedState`, the
original ambiguous/not-attributable class, or an exact receipt digest because
those values were never persisted. This package documents that boundary rather
than inventing equality.

## 2. Authoritative snapshot and clock

`sourceCrawlTerminalAuthority` now models the joined current claim and exact
history row:

- source, attempt, generation, claim request, engine;
- authoritative history `claimed_at`;
- both current-claim and history lease expiry, required to be equal;
- attempt state;
- immutable operation binding;
- source deleted/disabled flags;
- verified-ownership flag and current owner user ID;
- explicit approval history: valid at acceptance, revoked before acceptance,
  revoked after acceptance or expired after acceptance. Only revocation before
  acceptance conflicts; later revocation/expiry does not retroactively void an
  accepted operation;
- duplicate current-claim and history fields for source, attempt, generation,
  claim request, engine, claim time and lease. Every pair must agree exactly or
  the snapshot is corrupt.

The pure classifier returns one closed state:

| State | New terminal write |
| --- | --- |
| `current_live` | Eligible, subject to identity/binding/ownership checks |
| `current_expired` | `stale_fence` |
| `superseded` | `stale_fence` |
| `terminal` | `stale_fence` for a new write; exact replay uses the separate read path |
| `missing` | `stale_fence` |
| `corrupt` | `conflict` / corrupt-authority failure |
| `unavailable` | `unavailable` |
| `legacy_mode` | `unsupported` |

A receipt must match the authoritative source, attempt, generation, claim
request, engine, claim time, history lease and immutable binding exactly.
Mismatched attempt identity is `stale_fence`; a forged later lease or claim time
is `conflict`. The receipt's own timestamps never increase authority.

For source-level status mutation, eligibility now requires the full authority
preflight to succeed, not just a live fence and matching shape. A revoked-before
acceptance, corrupt snapshot, expired fence, missing source, unbound attempt or
wrong binding denies source-status mutation. An unbound attempt has no stored
operation principal, so it cannot prove owner continuity; a future B1 repository
may record its history result but must not overwrite source status.

The decision clock must be fresh database time read **after** locks. PostgreSQL
`CURRENT_TIMESTAMP` is transaction-start time, so claim allocation and
reconciliation use `clock_timestamp()`. SQLite uses `julianday('now')`, which is
evaluated by the statement. The expiry boundary is exclusive:
`now == lease_expires_at` is expired. Both are truncated to microseconds.

Claim batches read a selection timestamp after the policy lock, then a fresh
per-source timestamp after each source row is locked and before its claim
mutation. A final fresh check demotes **every** newly allocated attempt whose
short lease expired during a long batch to `stale_fence`, including
`unattributed`, `ambiguous`, `not_attributable` and `conflict` outcomes. No
expired attempt is returned in a live-looking outcome. The durable history is
not rewritten; the caller must still revalidate the authoritative fence before
any privileged mutation.

The pure snapshot is not a lock, a lease or execution authorization. The future
repository must re-read it and re-evaluate the clock inside its mutation
transaction.

## 3. Persisted terminal replay decision table

Option A is used for the pure contract: no new DDL. The persisted result is
reconstructed from `SourceCrawlAttempts` plus the exact `Status` operation state.

| Situation | Decision |
| --- | --- |
| First application | Requires `current_live`, exact attempt/binding identity, source not deleted, and a CAS from `accepted` or `processing` to one terminal state. Source status changes only if ownership/disablement eligibility still holds. |
| Identical terminal replay | Read-only. Compare attempt ID, claim request, source, generation, engine, claim time, lease, binding, outcome, reason and operation result. Return `idempotent_replay`; no lease renewal and no write. Works after lease expiry or supersession. |
| Conflicting terminal replay | Any changed engine, claim request, source, generation, attempt, claim time, lease, binding, outcome, reason or operation result returns `conflict`. Never overwrite the first result. |
| Expired pending attempt | A new write is `stale_fence`; no result is created. |
| Superseded attempt | A new write is `stale_fence`. An already committed result remains replayable read-only. |
| Lost commit response | Re-read the persisted attempt/operation identity. Matching result is `idempotent_replay`; conflicting result is `conflict`; read failure is `unavailable`. Never retry the mutation blindly. |

Equivalence details:

- `ObservedAt` must be structurally plausible for the original attempt:
  `observed_at >= claimed_at` and `observed_at < lease_expires_at`. The bound
  is compared against the receipt-carried lease, not the current clock, so an
  exact historical result remains replayable long after that lease expired.
- `ObservedAt` is ignored for replay identity. Two different in-lease
  observation values replay the same persisted result; the persisted
  `terminal_at` is authoritative and is never changed by a retry. An incoming
  observation exactly at or after the original lease expiry is a conflict even
  when every durable identifier matches.
- `ExpectedState` is a first-write precondition only. It is not persisted, so a
  retry with a different `ExpectedState` is still idempotent when the stored
  result matches.
- Original `unattributed`, `ambiguous` and `not_attributable` all normalize to
  the persisted `unbound` class. A retry may use any unbound class; a bound
  receipt against unbound history conflicts.
- The comparator validates the stored attempt through the same terminal
  constructor, so a corrupt or contradictory persisted result is a conflict.
- The incoming receipt must also pass the first-write structural validator
  (without current-lease eligibility). A malformed receipt cannot return
  `idempotent_replay` merely because its durable identifiers match.

### What Option A does not prove

- the exact `ExpectedState` observed at the first CAS;
- the original unbound reason;
- a canonical receipt digest or a durable receipt ledger;
- exact equality of a replayed observation timestamp (the value must be
  in-lease but need not equal the originally stored one);
- a durable request fingerprint or an empty-batch acknowledgement.

Those require a separately approved additive 1.17 extension (Option B). No such
DDL is implemented or executed here.

## 4. Lock order and transaction boundary

Proposed reporter order remains:

```text
shared policy advisory lock
  -> Sources row
  -> SourceCrawlClaims / SourceCrawlAttempts
  -> read-only CrawlProposals / CrawlApprovals
  -> Status compare-and-set
  -> audit and source status
```

Scheduling locks proposal -> source -> approval -> users -> status. The reporter
never locks proposal or approval rows, so it cannot form a proposal/source cycle.
The source row serializes scheduling, approval administration and terminal
writes for one source. Goal checkpoints are not touched.

Database locks are held for milliseconds. A crawl lease is held for the crawl.
No SQL transaction spans browser, VDI, email, plugin, model or network work.

## 5. Source policy

- Deleted source: reject a new terminal application as `stale_fence`.
- Lost fence: reject as `stale_fence`.
- Disabled source: a valid historical operation result may still be recorded,
  but the source-level status is not overwritten.
- Transferred owner: the operation result remains historically valid, but the
  old worker cannot overwrite source-level status.
- Source-level status requires `current_live`, verified ownership, an
  unchanged owner for a bound operation, and a non-disabled/non-deleted source.
- Unbound work never writes a `Status` operation result.
- Unbound source-level status is denied by default: there is no persisted
  operation principal that proves owner continuity. A future B1 repository may
  record the attempt-history result only; changing source status requires a
  separately approved owner-continuity mechanism.

## 6. Claim-request replay after heartbeat

Once a heartbeat renews a lease, `lease_expires_at - claimed_at` no longer
equals the original requested lease duration. The existing replay check cannot
prove full request equality from those fields. The decision is fail-closed:
reusing the original `ClaimRequestID` after a renewal returns a typed
`conflict`; the renewed lease is never reset and no success is manufactured.

A future B1 implementation must preserve this decision. Stronger idempotency
requires the separately requested B3 durable request fingerprint/empty-batch
record. The pure fingerprint is not stored.

## 7. Heartbeat bounds (future repository)

- Extend by at most one hour per call.
- Never beyond 24 hours from the immutable original claim time.
- Update `SourceCrawlClaims.lease_expires_at` and
  `SourceCrawlAttempts.lease_expires_at` for the exact current fence in one
  transaction with affected-row checks.
- Reject expired, superseded, missing and terminal attempts.
- Use fresh database time after locks.
- Reconcile an uncertain commit by re-reading the same attempt identity; never
  blindly extend twice.

## 8. Operation-state CAS

Bound operation transitions allowed by the pure contract:

- expected `accepted` or `processing` -> `completed`, `failed` or `cancelled`;
- `already_pending` is never execution ownership;
- terminal operation states accept only an exact read-only replay;
- the attempt CAS and operation CAS must commit or roll back together;
- the immutable scheduling identity inside `Status.details` is never rewritten.

## 9. Crawler side-effect audit

This audit is source-level, based on the reviewed HEAD. It does not claim that a
final terminal CAS protects these writes.

| Area | Current write point | Fence at commit? | Classification |
| --- | --- | --- | --- |
| Legacy source claim | `cmd/thecrowler/main.go` `retrieveAvailableSources` calls `update_sources`, sets `processing`/engine | No attempt fence | Legacy path; class 1 for a future fenced claim |
| Early source state | `CrawlWebsite` failure paths and `UpdateSourceState` update by URL | No | Class 1; stale worker can overwrite |
| Crawl completion source state | `closeSession` calls `UpdateSourceState` by URL, then optional source event | No | Class 1; future fenced path must branch on handoff and use source ID |
| Page index | `IndexPageContext`: `SearchIndex`, `SourceSearchIndex`, web objects, object attributes, metatags, keywords/index | No attempt check | Class 2 append-only/current-state; destructive `refresh_content` replacement needs fencing or provenance |
| Screenshots/files | Screenshot and file persistence during page indexing | No | Class 2 metadata; class 3 for external upload |
| Time series | Emitted in indexing transactions | No attempt check | Class 2 derived projection; must not be treated as completion proof |
| Crawl event | `CreateCrawlCompletedEvent` creates source-level event | No operation ID | Class 2 audit; not operation proof |
| Email ingestion | Mail checkpoints/leases in `pkg/mail`; documents indexed through `IndexPage` | No crawler attempt fence | Class 1/2 for indexed output; mailbox reads are class 3 external effects |
| Plugins/agents/action rules | Rule runtime can call plugins, agents, HTTP APIs and external databases | No fence | Class 3 external effect; cannot be rolled back |
| Browser/VDI | Navigation, clicks, form input, downloads, screenshots | No fence | Class 3 external effect; stop on cancellation where possible |
| VDI acquisition failure | `startCrawling` returns without a source-state update | No | Class 1 gap |
| Panic/shutdown | Limited recovery; terminal receipt not written | No | Class 1; no operation result |
| Legacy path | No handoff, current behaviour | Unchanged | Class 4; must remain unchanged |

Conclusion: a terminal CAS alone does **not** prevent stale indexing or external
side effects. A live handoff package must either fence the destructive/current
state writes and stop external work on lease loss, or explicitly retain them
with attempt provenance in a later work package.

## 10. Schema assessment and approval split

Existing 1.17 is sufficient for the minimal internal heartbeat/terminal
repository described above, but not for the stronger Option B equality.

The maintainer must approve separately:

1. **Internal repository and heartbeat (Option A or B):** transactional
   heartbeat and terminal writes, no production caller.
2. **Live crawler caller:** constructing and transporting the handoff, and
   fencing or stopping crawler writes/external effects.
3. **Optional DDL (Option B):** canonical receipt digest/ledger, persisted
   `ExpectedState`, original unbound class, request fingerprint and empty-batch
   acknowledgement.

No option is approved by this document. See
[`ASYNC_GOAL_B3C3_STAGE_B_APPROVAL_REQUEST.md`](ASYNC_GOAL_B3C3_STAGE_B_APPROVAL_REQUEST.md).

## 11. Rollout and rollback restrictions

- Deploy a handoff-capable worker only after approval, initially in `legacy`
  mode with the legacy path unchanged.
- Drain `processing` sources without attempts before switching to `fenced`.
- Use only `set_source_crawl_claim_mode` for PostgreSQL cutover.
- Stop/drain fenced workers before rolling back to `legacy`; a fenced worker
  that outlives rollback fails closed.
- MySQL remains legacy-only.
- No automatic mode flip, migration or startup DDL.

## 12. What is enabled now, and what is not

Enabled internally:

- The B1 Option A heartbeat and terminal-result repository described in
  `ASYNC_GOAL_B3C3B1_B1_IMPLEMENTATION.md`.
- Read-only exact terminal replay after lease expiry, replacement, source
  deletion and a new process connection pool.
- Bound operation-result recording with source-status updates only under the
  strict bound-owner policy.

Not enabled:

- Any automatic mode switch or default-mode change. B2 is implemented but only
  runs when the operator has explicitly selected `fenced`.
- Automatic goal resume, event consumer or goal-success decision from a crawl
  result.
- Page-index, browser/VDI, plugin, email or external-effect fencing. B2 must
  define it.
- Any schema change, migration, capability checksum change or writer-version
  change.
- Any public API, configuration or agent-manifest field.
- Production crawler, model, browser, MCP or external execution.
