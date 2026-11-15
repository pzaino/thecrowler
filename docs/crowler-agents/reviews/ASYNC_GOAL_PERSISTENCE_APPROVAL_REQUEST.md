# Async Goal Persistence Approval Request

## Status

**PARTIALLY DECIDED.**

The maintainer's WP-05B2B1 direction freezes the database release at **1.17**
and the writer generation at **`verified-ownership-v1.17`**. The schema objects
and the same-version development addon are prepared under that direction. The
transactional store, resume engine, retention policy and production deployment
remain **pending**. No named approver or approval date is asserted here.

## Requested decision

Approve or reject the following bounded persistence work:

1. The v2 `AgentGoalRuns` logical-goal/checkpoint model.
2. `AgentGoalRunTools` and `AgentGoalRunSources` frozen-scope records.
3. `AgentGoalRunWork` with distinct proposal/approval/submit-by/accepted-at/
   completion timestamps and the corrected SQL checks.
4. `AgentGoalRunEvidence`, `AgentGoalRunVerificationEvidence`,
   `AgentGoalRunAuditRefs` and `AgentGoalRunTransitions`.
5. A same-version additive 1.17 update for development databases already
   stamped 1.17. The database release stays 1.17; no new schema version is
   requested.
6. No writer-generation change. The generation stays
   `verified-ownership-v1.17` unless a separate, evidence-backed
   incompatibility is demonstrated and approved.
7. The PostgreSQL `READ COMMITTED`/row-lock and SQLite `BEGIN IMMEDIATE`
   transaction contract, compare-and-swap update and complete replay ledger.
8. The internal logical-lifetime policy: provisional 15-minute default,
   24-hour maximum, with a separate public/deployed TTL decision.
9. Retention, backup/restore and rollback rules.
10. PostgreSQL 17/15.10 and SQLite compatibility gates, with MySQL explicitly
    unsupported.

## Required approval record

- Approver identity: not asserted
- UTC approval date: not asserted
- Schema version: **1.17 (frozen by WP-05B2B1 direction)**
- Writer generation: **`verified-ownership-v1.17` (frozen by WP-05B2B1
  direction)**
- Logical-lifetime default/maximum: **pending**
- Retention period: **pending**
- Scope decided: 1.17 DDL and same-version development addon preparation
- Scope pending: transactional store, resume worker/claim/lease, production
  migration
- Public agent schema/API/events: **not approved**
- MCP mutation execution: **not approved**

## What the approval does not authorize

- A production durable store, migration application or grants.
- A background worker, queue consumer, claim, lease, heartbeat, retry or
  reconciliation loop.
- Event consumer, endpoint, public YAML/JSON manifest field or example change.
- Any remote mutating MCP call, dispatcher, dispatch-attempt write or MCP
  Option A/B simulator.
- Any change to schema 1.17 or writer generation
  `verified-ownership-v1.17`.
- Application of the same-version addon or any other DDL to a persistent,
  shared, staging or production database without separate deployment
  authorization.

## Readiness evidence

- Pure kernel and adversarial tests are in `pkg/agent`.
- The updated design is in `ASYNC_GOAL_PERSISTENCE_CONTRACT.md`.
- The defect/fix record is in `ASYNC_GOAL_B2A_FIXES.md`.
- The 1.17 DDL is in the PostgreSQL/SQLite setup and v1.16 -> v1.17 migration
  files; the same-version addons are
  `postgresql-agent-goal-v1.17.pgsql` and
  `sqlite-agent-goal-v1.17.sqlite3`.
- `CheckAsyncGoalPersistenceSchema` and its tests are in
  `pkg/database/async_goal_schema.go` and
  `pkg/database/async_goal_schema_test.go`.
- No production store, worker or automatic migration exists in this patch.

## Decision rule

A task prompt, a chat instruction or the existence of this request is not
approval for the pending store, runtime or deployment work. The 1.17 version
freeze is an explicit maintainer direction for this work package; it must not be
read as approval of any remaining phase.
