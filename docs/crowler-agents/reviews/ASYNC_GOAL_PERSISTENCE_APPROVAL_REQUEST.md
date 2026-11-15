# Async Goal Persistence Approval Request

## Status

**PENDING. Not approved.**

No attributed, in-scope prior approval for the asynchronous goal persistence
migration was located in the repository. This document is a request, not a
decision.

## Requested decision

Approve or reject the following bounded persistence work:

1. The v2 `AgentGoalRuns` logical-goal/checkpoint model.
2. `AgentGoalRunTools` and `AgentGoalRunSources` frozen-scope records.
3. `AgentGoalRunWork` with distinct proposal/approval/submit-by/accepted-at/
   completion timestamps and the corrected SQL checks.
4. `AgentGoalRunEvidence`, `AgentGoalRunVerificationEvidence`,
   `AgentGoalRunAuditRefs` and `AgentGoalRunTransitions`.
5. A new additive migration, proposed as schema `1.18` (or an approved
   alternative).
6. A new writer generation, proposed as `goal-checkpoint-v1.18` (final name to
   be decided).
7. The PostgreSQL `READ COMMITTED`/row-lock and SQLite `BEGIN IMMEDIATE`
   transaction contract, compare-and-swap update and complete replay ledger.
8. The internal logical-lifetime policy: provisional 15-minute default,
   24-hour maximum, with a separate public/deployed TTL decision.
9. Retention, backup/restore and rollback rules.
10. PostgreSQL 17/15.10 and SQLite compatibility gates, with MySQL explicitly
    unsupported.

## Required approval record

- Approver identity:
- UTC approval date:
- Schema version approved:
- Writer generation approved:
- Logical-lifetime default/maximum approved:
- Retention period approved:
- Scope: the bounded persistence work above
- Resume worker/claim/lease: **not approved**
- Public agent schema/API/events: **not approved**
- MCP mutation execution: **not approved**

## What the approval does not authorize

- A production durable store, migration application or grants.
- A background worker, queue consumer, claim, lease, heartbeat, retry or
  reconciliation loop.
- Event consumer, endpoint, public YAML/JSON manifest field or example change.
- Any remote mutating MCP call, dispatcher, dispatch-attempt write or MCP
  Option A/B simulator.
- Any change to schema `1.17` or writer generation
  `verified-ownership-v1.17` before the approval is recorded and the migration
  is separately applied.

## Readiness evidence

- Pure kernel and adversarial tests are in `pkg/agent`.
- The updated design is in `ASYNC_GOAL_PERSISTENCE_CONTRACT.md`.
- The defect/fix record is in `ASYNC_GOAL_B2A_FIXES.md`.
- No migration, table or production store exists in this patch.

## Decision rule

A task prompt, a chat instruction or the existence of this request is not
approval. Persistence work may begin only after an attributed maintainer
decision with a UTC date and the exact approved scope is recorded.
