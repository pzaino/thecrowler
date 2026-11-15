# Async Goal Maintainer Decisions

## Current state

The internal async goal lifecycle/checkpoint kernel is implemented and tested.
It is not wired to production execution, persistence, Events, the scheduler,
the crawler or MCP. No database migration, worker, claim, lease, event consumer
or public contract was added.

## Decision 1: persistence phase

**Status: PENDING.**

Approval is required before adding:

- the durable goal-run/checkpoint v2 schema and migration;
- the proposed schema `1.18` or an approved alternative;
- the writer-generation change and startup compatibility check;
- the internal logical-lifetime default/maximum and any public/deployed TTL;
- the strict canonical checkpoint wire and duplicate-key rejection contract;
- the compare-and-swap transaction, full replay ledger, lock order and conflict
  semantics;
- child work/evidence/audit/transition records;
- retention and pruning ownership;
- PostgreSQL 17/15.10 and SQLite compatibility gates.

The proposal is in `ASYNC_GOAL_PERSISTENCE_CONTRACT.md`. It is not a migration
and does not authorize one.

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

- The persistence proposal is not approval.
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

An independent review of this work package, followed by a separate maintainer
decision on the persistence, resume and public-contract phases. Do not begin
those phases automatically.
