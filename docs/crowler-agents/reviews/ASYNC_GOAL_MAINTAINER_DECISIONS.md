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

WP-05B3C2C is implemented and locally tested. Hosted PostgreSQL CI must be
green on the exact submitted commit before the package is reported as fully
verified. Production activation, a crawler/worker caller, terminal completion
reporting, execution ownership, cancellation finalization and public
configuration remain pending. WP-05B3C3 must design the authenticated attempt
handoff and fenced terminal result with separate review. Do not apply any addon
to a persistent database or begin those phases automatically.
