# Async Goal Maintainer Decisions

## Current state

The internal async goal lifecycle/checkpoint kernel, an internal unexported
transactional store, durable cancellation fencing, stricter transition-ledger
validation, a read-only crawl-operation observer and a hardened read-only
resume eligibility service are implemented and tested. None of this is wired to
production execution, Events, the scheduler, the crawler or MCP. The schema-1.17
goal tables and same-version development addon are prepared. No worker, claim,
lease, event consumer, resume engine or public contract was added.

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

Independent review of WP-05B3C1, followed by a separate design decision for an
operation-aware crawler claim and attempt fence, then durable execution
ownership, claim/lease coordination, cancellation finalization, production store
activation and the public-contract phases. Hosted PostgreSQL CI must be rerun
successfully before this package is described as fully green. Do not apply the
addon to a persistent database or begin those phases automatically.
