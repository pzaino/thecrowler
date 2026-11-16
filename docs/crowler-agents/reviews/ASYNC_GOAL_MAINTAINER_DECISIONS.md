# Async Goal Maintainer Decisions

## Current state

The internal async goal lifecycle/checkpoint kernel, an internal unexported
transactional store, durable cancellation fencing, stricter transition-ledger
validation and a read-only crawl-operation observer are implemented and tested.
None of this is wired to production execution, Events, the scheduler, the
crawler or MCP. The schema-1.17 goal tables and same-version development addon
are prepared. No worker, claim, lease, event consumer, resume engine or public
contract was added.

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

Independent review of WP-05B3A, followed by a separate maintainer decision on
current-authority refresh and guarded resume admission (WP-05B3B), cancellation
finalization semantics, production store activation and the public-contract
phases. Do not apply the addon to a persistent database or begin those phases
automatically.
