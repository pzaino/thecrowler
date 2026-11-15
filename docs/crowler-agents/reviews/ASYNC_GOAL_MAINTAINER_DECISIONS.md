# Async Goal Maintainer Decisions

## Current state

The internal async goal lifecycle/checkpoint kernel is implemented and tested.
It is not wired to production execution, persistence, Events, the scheduler,
the crawler or MCP. The schema-1.17 goal tables and same-version development
addon are prepared. No Go store, worker, claim, lease, event consumer or public
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

## Decision 1: transactional store and runtime phase

**Status: PENDING.**

Approval is required before adding:

- the Go durable goal-run/checkpoint store and CAS adapter;
- the internal logical-lifetime default/maximum and any public/deployed TTL;
- the strict canonical checkpoint wire and duplicate-key rejection contract;
- the compare-and-swap transaction, full replay ledger, lock order and conflict
  semantics;
- runtime record hydration and load/save operations;
- retention and pruning ownership;
- production deployment and backup/rollback scheduling.

The DDL is prepared in `ASYNC_GOAL_PERSISTENCE_CONTRACT.md`. Prepared DDL is not
approval of the store, runtime or deployment.

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
- No production wiring, DB, network, MCP, crawler or model calls.

## Explicit non-approvals

- Prepared 1.17 DDL is not approval of the Go store or a production migration.
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

Independent review of WP-05B2B1, followed by a separate maintainer decision on
the transactional store, resume and public-contract phases. Do not apply the
addon to a persistent database or begin those phases automatically.
