# Async Goal Maintainer Decisions

## Current state

The internal async goal lifecycle/checkpoint kernel, an internal unexported
transactional store, durable cancellation fencing, stricter transition-ledger
validation, a read-only crawl-operation observer, a hardened read-only resume
eligibility service and a pure source-crawl-attempt claim contract are
implemented and tested. None of this is wired to production execution, Events,
the scheduler, the crawler or MCP. The schema-1.17 goal tables and same-version
development addon are prepared. No worker, claim, lease, event consumer, resume
engine or public contract was added.

## Decision 0: schema and writer version freeze

**Status: DECIDED by the WP-05B2B1 task direction.**

The database release remains **1.17** because 1.17 has not shipped. The writer
generation remains **`verified-ownership-v1.17`**. The asynchronous-goal tables
are incorporated into the unreleased 1.17 fresh-install and v1.16 -> v1.17
migration paths. Development databases already stamped 1.17 use the manual,
same-version addon.

No named approver or approval date is asserted. The decision is recorded in the
work-package direction, not in a separate signed approval record.

## Decision 1: persistence activation phase

**Status: PARTIALLY COMPLETE — internal store only; activation PENDING.**

The internal unexported store now implements create, load, optimistic transition,
replay-ledger checks, SQLite/PostgreSQL writer serialization, corruption
validation and commit-outcome reconciliation. It is not constructed by any
production service.

Approval is still required before:

- production wiring and live account/agent/source ownership/policy refresh;
- the internal logical-lifetime default/maximum and any public/deployed TTL;
- a production goal-store readiness gate;
- retention and pruning ownership;
- production deployment and backup/rollback scheduling.

The DDL and internal store are prepared in
`ASYNC_GOAL_PERSISTENCE_CONTRACT.md`. Prepared DDL and the internal store are
not approval of production activation or deployment.

## Decision 1a: cancellation fencing and read-only observation

**Status: IMPLEMENTED internally; activation semantics PENDING.**

`TransitionCheckpoint` now observes `cancel_requested_at` inside the transition
transaction, blocks every new transition with
`ErrGoalAsyncCancellationPending`, and includes `cancel_requested_at IS NULL` in
the root compare-and-swap. Exact replays of stored operations remain idempotent,
and loading for audit still succeeds. There is deliberately no cancellation
finalization API: the actor semantics and a safe terminal transition require a
separate reviewed decision.

`ObserveCrawlOperation` correlates one checkpoint `Work` operation through
`CrawlProposals`, `CrawlApprovals` and a strictly decoded `Status.details` in a
read-only snapshot. It returns `not_found`, `pending`, `processing`,
`reported_completed`, `terminal_non_success`, `uncertain`, `conflict` or
`unavailable`. It does not call `ReconcileCrawlProposalOperation`, schedule
work, create a resume grant, construct a verifier decision or execute anything.
`reported_completed` is never proof that the goal's acceptance criteria are met.

Approval of this internal observation layer does not authorize resumption,
current-authority refresh or a claim/lease worker.

## Decision 1b: read-only resume eligibility

**Status: IMPLEMENTED internally; execution activation PENDING.**

`goalAsyncResumeEligibility` loads the current persisted checkpoint and
cancellation marker, revalidates the current user through
`auth.ResolveActiveIdentity`, verifies every frozen source through verified
`SourceOwnership` without administrator override, and requires an explicit
current-agent authority provider bound to the exact agent, owner, goal spec,
policy hash, tool set and source set. It returns `eligible`, `waiting`,
`denied`, `uncertain` or `unavailable` with stable reason codes. No provider is
installed globally; a nil provider fails closed. The registry-backed provider is
an explicit process-snapshot helper, not proof of manifest freshness.

`eligible` is a diagnostic decision, not an executable grant. The service runs
its checks twice around operation correlation and compares a second checkpoint
snapshot, but no claim/lease exists, so a later change is not covered. The
future execution transaction must repeat the checks under its own lock.

### Operation completion blocker

No production code advances the specific `Status` operation row to `completed`
after scheduling. The crawler updates `Sources.status` and emits a source-level
`crawl_completed` event, which is not operation-specific proof. Real scheduled
operations therefore remain `pending` for the observer. Automatic resumption is
blocked until the exact durable operation is advanced or a separately approved
authenticated correlation mechanism exists. Do not infer completion from source
status, events or model text.

## Decision 1c: eligibility hardening and completion-provenance boundary

**Status: HARDENING IMPLEMENTED; completion integration BLOCKED.**

Eligibility now rejects a source whose current `Sources.disabled` flag is true
for every frozen allowed source, using a resume-specific read-only helper. It
uses a strict typed current-identity resolver that separates inactive accounts
from unavailable Users/grant storage without changing login or middleware
behavior. The supported principal policy is local: the numeric local user ID
and current username are authoritative, only the canonical `crowler` issuer
proceeds, and external issuer mappings fail closed.
The service also rechecks its clock immediately before returning a decision, so
a deadline that expires during a slow provider cannot produce `eligible`.

No operation-completion helper was implemented. The crawler claims sources only
by `source_id`; `update_sources` does not return an operation ID or fence,
`ProcessContext` carries no attempt identity, and `closeSession` updates
`Sources.status` plus an optional source-level `crawl_completed` event. There is
no authenticated binding from a durable `operationID` to a crawler attempt.
Source status, source events and model text are not operation proof.

The proposed next design is an operation-aware source claim carrying an
operation ID and monotonically increasing attempt fence into the worker, with a
compare-and-set terminal result on the exact `Status` operation. This requires a
separately reviewed schema change and is not part of 1.17.

Hosted PostgreSQL CI is **not verified** for this package. The reviewed job
failed transiently while installing the PostgreSQL 17 client; bounded retries
with normal TLS/signature verification were added, but no hosted rerun was
possible in this environment. Local disposable PostgreSQL 17.12 integration
tests passed.

## Decision 1d: durable crawl-attempt storage

**Status: STAGE B IMPLEMENTED; PRODUCTION ACTIVATION PENDING.**

The maintainer approved the Stage A design and authorized Stage B for the
additive schema-1.17 attempt objects, the `legacy`/`fenced` cutover in
`update_sources`, the internal opt-in claim API and disposable PostgreSQL/SQLite
tests only.

Implemented:

- `SourceCrawlClaims`, `SourceCrawlAttempts` and `SourceCrawlClaimPolicy` in the
  unreleased 1.17 PostgreSQL and SQLite fresh installs and v1.16 -> v1.17
  migrations;
- repeatable same-version development addons with fail-closed structural
  guards;
- the `source-crawl-attempt-v1` capability marker and read-only readiness check;
- PostgreSQL fenced `update_sources` returning no rows to old workers;
- the atomic claim repository with idempotent claim-request identity, monotonic
  per-source generations, append-only history and optional operation binding;
- SQLite and disposable PostgreSQL integration tests.

Production activation is **not approved**. No crawler, worker, event consumer,
agent, MCP client or production service calls the claim API. No terminal result
is recorded, no public configuration is added, and schema 1.18 is not
authorized. WP-05B3C3 must design and review the crawler handoff and fenced
terminal result separately.

## Decision 1e: attempt-foundation hardening

**Status: IMPLEMENTED INTERNALLY; HOSTED CI AND PRODUCTION ACTIVATION PENDING.**

WP-05B3C2C hardens the approved attempt capability without changing schema or
writer generation:

- `SourceCrawlClaims` now references one exact `SourceCrawlAttempts` row through
  `(source_id, generation, attempt_id)`; the capability checksum and readiness
  check were updated accordingly.
- Claim transactions and the legacy `update_sources` share an advisory policy
  lock. The operator-only `set_source_crawl_claim_mode` function takes the
  exclusive lock. The runtime role has SELECT only on the policy table and
  cannot execute the operator function.
- Attribution counts all submitted operation candidates before validation, so a
  malformed or missing-approval competitor blocks a partial binding.
- Binding verifies `CrawlOperationKey`, strict `Status.details` (including
  duplicate keys and trailing data), approval identity and acceptance
  chronology.
- Missing current claims recover the next generation from retained history;
  corrupt or exhausted history fails closed.
- Replay verifies engine and lease duration. Limit, priority and interval fields
  are not persisted, so complete request-hash replay equality would require a
  schema extension and remains a documented limitation.

The PostgreSQL addon is an administrator operation and repairs earlier
development tables without deleting rows. The SQLite addon rebuilds the attempt
tables transactionally and aborts on inconsistent current-to-history data.
Hosted PostgreSQL CI for this exact patch has not run. The existing agent
submission-chain failure was not reproduced in the local exact broad suite; it
remains a hosted-CI gate to verify, not a claimed pass.

Production activation, crawler/worker callers, terminal result reporting,
resume workers and public configuration remain unapproved.

## Decision 1f: pre-handoff replay repair and terminal-result design

**Status: REPAIRS IMPLEMENTED; LIVE HANDOFF/TERMINAL WRITES EXPLICITLY NOT
APPROVED.**

WP-05B3C3A changes only internal claim/replay behaviour and documentation:

- Every `submitted` proposal is counted before attribution, including null or
  blank operation IDs. A malformed competitor blocks binding.
- Replay uses an explicit trusted clock and database time for reconciliation.
  Expired, superseded and terminal attempts classify as `stale_fence`; unbound
  attempts replay as `unattributed` and never as a fabricated bound `claimed`
  success. No replay allocates a new generation.
- The full proposal/approval/scheduler-to-claim path is tested on disposable
  PostgreSQL, including a real observation that remains `pending`/`accepted`.
- A pure, unexported handoff and terminal-receipt validator set is added with no
  production caller or database write.
- The known request-idempotency limits (no durable request digest, no durable
  empty-batch acknowledgement) are documented. A future schema extension
  requires separate approval.

**Not approved by this decision:** a terminal-result repository, heartbeat
writer, live crawler handoff, attempt-result migration, worker activation,
automatic async goal resume, or any change to schema 1.17 / writer generation.
Those require the explicit approval record requested in
`ASYNC_GOAL_B3C3_STAGE_B_APPROVAL_REQUEST.md` and the contract in
`ASYNC_GOAL_B3C3A_HANDOFF_AND_FENCED_RESULTS.md`.

## Decision 1g: fenced receipt authority and Phase A boundary

**Status: PURE HARDENING IMPLEMENTED; STAGE B REMAINS UNAPPROVED.**

WP-05B3C3B1 corrects the pure contract without adding a mutating repository:

- The terminal authority snapshot now carries the joined current/history claim
  identity and both lease values, requires them to agree, and compares the
  worker receipt against the authoritative claim time and lease.
- The pure classifier distinguishes `current_live`, `current_expired`,
  `superseded`, `terminal`, `missing`, `corrupt`, `unavailable` and
  `legacy_mode`.
- Claim allocation and reconciliation use fresh database time after locks:
  PostgreSQL `clock_timestamp()`, SQLite statement time. The expiry boundary is
  exclusive.
- Unbound handoffs reject every operation-identity field.
- Persisted terminal replay uses Option A: exact attempt identity, binding,
  outcome, reason and operation result; `ObservedAt` is ignored and
  `ExpectedState` is a precondition only. The original unbound class normalizes
  to `unbound`. Fields that 1.17 cannot prove are documented, not invented.
- A crawler side-effect audit records which writes and external effects remain
  unfenced. A terminal CAS alone is explicitly not claimed to protect them.

The separate B1 (internal repository), B2 (live crawler caller) and B3 (optional
additive schema) decisions are still pending. See
`ASYNC_GOAL_B3C3B1_DESIGN_REVIEW.md`,
`ASYNC_GOAL_B3C3_STAGE_B_APPROVAL_REQUEST.md` and
`ASYNC_GOAL_B3C3B1_TEST_REPORT.md`.

**Not approved by this decision:** heartbeat writer, terminal repository, live
handoff, any schema/migration/checksum change, worker activation, automatic goal
resume or production execution.

## Decision 1h: receipt authority closure and B1 gate

**Status: PHASE A PURE HARDENING IMPLEMENTED; B1 REMAINS PENDING.**

WP-05B3C3B1R closes the remaining pure-contract gaps:

- Source-status mutation now requires full authority preflight success. A
  revoked-before-acceptance snapshot, expired fence, missing/corrupt authority,
  wrong binding or unbound attempt cannot update source status.
- Current claim and history rows must agree on source, attempt, generation,
  claim request, engine, claim time and lease. Any disagreement is corrupt.
- Claim batches read a per-source timestamp after each source row lock and a
  final freshness timestamp. A short lease that expires during a long batch is
  returned as `stale_fence`, never executable `claimed`.
- An original `ClaimRequestID` reused after heartbeat renewal fails closed with
  `conflict`; the lease is not reset and full request equality is not claimed.
- Read-only terminal replay now validates incoming receipt structure without
  requiring a live lease, so malformed receipts cannot be idempotent and exact
  post-expiry replay remains possible.
- Unbound source-status mutation is denied because 1.17 stores no unbound
  operation principal or owner-continuity proof.

No B1 repository, heartbeat, terminal writer, DDL, live caller or production
activation is approved. The B1 gate remains exactly as requested in
`ASYNC_GOAL_B3C3_STAGE_B_APPROVAL_REQUEST.md`.

## Decision 1i: all-class final freshness and observation-time replay

**Status: PHASE A PURE HARDENING IMPLEMENTED; B1 REMAINS PENDING.**

WP-05B3C3B1S closes the two remaining Phase A defects and does not implement the
terminal repository:

- The final claim-batch freshness check now demotes every newly allocated
  attempt whose lease expired at the final trusted timestamp, including
  `unattributed`, `ambiguous`, `not_attributable` and `conflict` outcomes. No
  expired attempt is returned in a live-looking outcome; the durable history is
  unchanged and the caller must revalidate the fence.
- The terminal receipt structural validator now requires
  `claimed_at <= observed_at < lease_expires_at` against the receipt-carried
  lease. The rule does not consult the current clock, so an exact historical
  result remains replayable after expiry, while an observation exactly at or
  after the original lease expiry is a conflict even on the read-only path.
- `ObservedAt` remains ignored for replay identity because schema 1.17 does not
  persist it. Two different in-lease observation values replay the same stored
  result; the persisted `terminal_at` remains authoritative.
- The per-source claim clock, all-class freshness and the replay rule are tested
  on SQLite and disposable PostgreSQL using the deterministic clock seam.

B1 is still exactly as requested in `ASYNC_GOAL_B3C3_STAGE_B_APPROVAL_REQUEST.md`
and remains unapproved. No heartbeat writer, terminal repository, DDL, live
caller or production activation is authorized.

## Decision 1j: B1 Option A and B2 approval

**Status: B1 AND B2 IMPLEMENTED; B3 NOT APPROVED.**

On 2026-11-18 the repository maintainer approved B1 Option A and B2. The
attributable record is in `ASYNC_GOAL_B3C3_STAGE_B_APPROVAL_REQUEST.md`:
approver, UTC date, `WP-05B3C3/Stage-B-2`, exact instruction and scope.

Approved:

- B1 internal, opt-in, test-only lease-renewal and terminal-result repository on
  existing schema 1.17, with no crawler, agent, event, MCP or public caller.
- B2 live crawler handoff through `cmd/thecrowler`, `pkg/crawler.Pars` and
  `ProcessContext`, including the approved fencing/cancellation requirements.
  B2 must preserve the legacy path and may run only under an explicit operator
  `fenced` mode.

Not approved:

- B3 additive DDL, canonical receipt digest, persisted `ExpectedState`, original
  unbound class, request fingerprint or empty-batch acknowledgement.
- Automatic mode switching, migration application, deployment, production
  activation, automatic agent-goal resume, or a new public API/config/manifest.

The B1 implementation is recorded in
[`ASYNC_GOAL_B3C3B1_B1_IMPLEMENTATION.md`](ASYNC_GOAL_B3C3B1_B1_IMPLEMENTATION.md).
The B2 live crawler handoff, heartbeat, cancellation and fenced write behavior
are recorded in
[`ASYNC_GOAL_B3C3B2_IMPLEMENTATION.md`](ASYNC_GOAL_B3C3B2_IMPLEMENTATION.md).
B2 adds a separate fenced operational source-status update for unbound attempts
so ordinary fenced sources do not remain stuck in `processing`; it never creates
a `Status` operation or goal evidence and does not relax the bound-owner policy.

## Decision 1k: B2R execution-safety hardening

**Status: IMPLEMENTED AND TESTED; NOT PRODUCTION-READY; B3 STILL NOT APPROVED.**

B2R corrects the reviewed B2 live-execution defects without schema changes:

- a failed network-only index is a failed crawl and can no longer be reported
  as completed;
- timeout records one reason, cancels the crawl, waits a bounded interval for
  worker/indexing quiescence, quarantines the VDI when quiescence is not
  established, and finalizes exactly once;
- `closeSession` no longer uses the browser-failure `pStatus` marker as its
  finalization guard;
- authoritative `ValidateCurrent`/`ValidateTx` re-establish the fenced policy,
  exact attempt, generation, lease, source liveness and processing state before
  external work and inside current-state database transactions;
- unbound source-status mutation requires durable owner-continuity proof and is
  performed inside the terminal transaction; otherwise it is suppressed;
- the terminal repository derives the operation state from the authoritative
  `Status` row, so `accepted` and `processing` both permit a proper terminal CAS;
- fenced `refresh_content` and fenced `null:` deletion are refused/quarantined
  until an atomic or exact-fence contract exists;
- an unreadable present fenced claim store fails closed; only a genuinely absent
  optional schema (or MySQL) keeps the legacy path;
- the configured completion-event flag is honoured for fenced runs.

**Not approved by this decision:** B3 additive schema, a durable unbound
claim-owner field, `refresh_content` atomic replacement, exact-fence `null:`
deletion, production activation, automatic mode switching or automatic goal
resumption. Those require separate approval. The fenced path is a candidate for
controlled integration testing only after independent review and a live
VDI/browser run.

## Decision 1l: B2F1 refresh design and ownership-continuity correction

**Status: DESIGN ONLY. DECISION 2 NOT APPROVED. NO IMPLEMENTATION.**

WP-05B3C3B2F1 audited the proposed atomic fenced `refresh_content` design and
corrected the Decision 1 ownership proposal. No runtime code, DDL, test
enabling fenced refresh, or live destructive test was added.

- The proposed "move the delete inside the main indexing transaction" is not
  sufficient. On a disposable PostgreSQL 17 database, the exact
  `deleteWebObjects` SQL removed a hash-shared `WebObjects` row still
  referenced by another page, cascading away that page's link and its
  `ObjectAttributes`; one `SearchIndex` row was also shown to be linked to two
  `Sources`. The design now requires a page row lock, a cross-source page
  refusal, page-scoped link deletion and orphan-only object deletion, with
  deterministic and PostgreSQL concurrency proof before activation.
- Decision 1 was corrected: a single `claim_owner_user_id` cannot prove
  continuous tenure because A -> B -> A restores equality. The request now asks
  for an explicit ownership semantic (continuous tenure vs reauthorization) and
  proposes a monotonic `Sources.ownership_epoch` captured on the attempt, or an
  append-only ownership history.

Documents:
[`ASYNC_GOAL_B3C3B2F1_REFRESH_DESIGN.md`](ASYNC_GOAL_B3C3B2F1_REFRESH_DESIGN.md)
and
[`ASYNC_GOAL_B3C3B2F1_TEST_REPORT.md`](ASYNC_GOAL_B3C3B2F1_TEST_REPORT.md).

**Not approved by this decision:** Decision 2 implementation, Decision 1 DDL,
Decision 3, Decision 4, Decision 5, fenced refresh activation, production
activation, automatic mode switching, automatic goal resumption and
`null:` deletion. The existence of this note is not approval.

## Decision 1m: WP-05B3C3B2F1 / B3 maintainer decision (2026-11-18)

**Status: Decision 1a ownership model APPROVED (model only, no DDL).
Decision 2 atomic fenced `refresh_content` APPROVED FOR IMPLEMENTATION.
Decision 3 DEFERRED with `null:` semantics preserved. Decisions 4 and 5
DEFERRED. No production activation.**

The repository maintainer recorded the following decision on 2026-11-18 under
work package `WP-05B3C3B2F1 / B3`.

### Decision 1 — ownership semantics

Option 1a, a monotonically increasing ownership epoch, is the selected design.
Continuous, verifiable ownership is required for an in-flight fenced attempt:
a transfer A -> B -> A invalidates the original attempt, because equality of
claim-time and current owner identifiers does not prove uninterrupted
ownership. The implementation work package must review every ownership-changing
pathway (transfer, revocation, re-verification, administrative update) and
establish whether the epoch must also advance when verified authority changes
without a change to `Sources.usr_id`. This decision approves the model only;
no Decision 1 DDL or runtime activation is authorised.

### Decision 2 — atomic fenced `refresh_content`

Approved for implementation subject to
`ASYNC_GOAL_B3C3B2F1_REFRESH_DESIGN.md`:

1. authoritative fenced-claim validation inside the database transaction;
2. page-row locking and refusal to refresh a page shared by another source;
3. page-scoped `WebObjectsIndex` replacement;
4. `WebObjects` deletion only when no other page references the object;
5. correct FK, cleanup-trigger, concurrency and rollback behaviour;
6. protection against expired or superseded claims, cancellation and indexing
   failures;
7. deterministic SQLite and PostgreSQL integration tests;
8. real Docker engine/PostgreSQL/API/Events/Selenium-VDI testing;
9. full regression, race, vet, build and CI verification.

Legacy refresh behaviour must not silently change. Existing legacy
cross-source/shared-object data-loss risks must be documented separately.

### Decision 3 — `null:` protocol

Deferred for fenced-mode implementation. The `null:` protocol is an intentional
pipeline-testing mechanism that exercises processing pathways without actual
content collection; it is not an ordinary crawl, an invalid source or merely a
source deletion request. Legacy `null:` behaviour must be preserved. No fenced
`null:` execution, source deletion or cleanup change is authorised. For the
Decision 2 package, the refresh changes must not regress legacy `null:` tests or
alter fenced `null:` quarantine.

### Decisions 4 and 5

Deferred. No terminal receipt ledger/digest, claim-request fingerprint,
acknowledgement table or empty-batch persistence in this work package.

### Schema and compatibility

Decision 2 uses existing schema 1.17 and writer generation
`verified-ownership-v1.17`. Decision 1 DDL is not authorised; whether it stays
in unreleased 1.17 or becomes a later migration is a separate determination.
Default `legacy` claim mode, MySQL legacy-only behaviour, public API/Events/
configuration/agent contracts, synchronous agent and MCP behaviour, native crawl
and Information Seed behaviour, and `null:` testing semantics are preserved.

### Explicit non-approvals

No Decision 1 DDL or runtime activation, no Decision 3 fenced `null:`
implementation or deletion, no Decisions 4/5, no production fenced-mode
activation, no automatic mode switching, no automatic asynchronous goal
resumption, no new public API/configuration contracts, and no MCP mutation
execution or expanded agent privileges.

## Decision 2: resume phase

**Status: PENDING.**

Approval is required before adding:

- a background resume claim/lease;
- an event trigger or bounded poller;
- live account, agent, source ownership and policy refresh;
- fresh approval checks for new privileged actions (historic acceptance does
  not require an unexpired approval merely to read completion evidence);
- cancellation, shutdown and preemption semantics;
- crash/replay reconciliation and uncertain-write recovery;
- a durable worker or event consumer.

The kernel models current-authority checks through a non-serializable resume
grant, but it does not perform them. A stored checkpoint is not proof of
current authority.

## Decision 3: public contract phase

**Status: PENDING.**

Approval is required before adding or changing:

- public YAML/JSON configuration fields;
- new event types or payloads;
- API/Events endpoint changes;
- agent example manifests;
- any user-facing async goal request/approval/resume contract.

No public schema or example was changed.

## What is already authorized and complete

- Schema-1.17 PostgreSQL/SQLite goal tables in fresh installs and the v1.16 ->
  v1.17 migration.
- Manual same-version addons for development databases already stamped 1.17.
- Read-only `CheckAsyncGoalPersistenceSchema`, deliberately not wired to normal
  startup.
- Internal unexported transactional store for checkpoint create, load and
  optimistic transition, with SQLite/PostgreSQL concurrency, replay and
  corruption tests.
- Durable cancellation fencing, last-transition reason/timestamp ledger checks,
  and a read-only one-operation crawl observer for PostgreSQL and SQLite.
- Historical approval-chronology and operation-timestamp validation.
- Read-only resume eligibility with current user, verified source ownership and
  explicit agent/policy authority for PostgreSQL and SQLite.
- Disabled-source rejection, strict auth availability/identity classification,
  final deadline recheck, and source-level-completion negative tests.
- A real scheduler-to-observer acceptance test using authentic persisted rows.
- A pure source-crawl-attempt identity, binding and fence contract with
  structural negative tests; no persistence or caller.
- Pure-Go internal checkpoint and lifecycle model.
- Closed transition table and terminal predicate.
- Verifier-backed completion binding to `EvaluateGoal`.
- Typed proposal/approval/submission/accepted/completed work references.
- Separate logical lifetime and synchronous slice ceiling.
- Trusted scheduling-admission prerequisite.
- Strict v2 wire decoding without read-boundary normalization.
- Cumulative budget/deadline/replay validation.
- Defensive copying, strict serialization, bounded references.
- Adversarial unit and race tests.
- Internal store database access is explicit-construction only; no production
  wiring, network, MCP, crawler, scheduler, worker or model calls.

## Explicit non-approvals

- Prepared 1.17 DDL and the internal store are not approval of production
  activation or a production migration.
- The resume design is not approval.
- The public-contract discussion is not approval.
- A chat request, a new engineering task or an inferred intention is not
  approval.
- External execution and MCP Option A/B remain outside this work and are not
  authorized.

## Compatibility

- Release `v2.3.0`
- Schema `1.17`
- Writer generation `verified-ownership-v1.17`
- Execution backend `none`
- Existing synchronous goal/action behavior unchanged
- Existing API/Events contracts unchanged
- Native crawl and Information Seed unchanged

## Next action

B1 Option A and B2 are implemented and tested as recorded in Decision 1j. B3,
automatic mode switching, migration application, deployment, production
activation and automatic goal resume remain unapproved. Do not apply any addon
to a persistent database or begin unapproved phases automatically.
