# WP-05B2B1 Maintainer Decisions

## Decided by task direction

### Schema and writer version freeze

**Status: DECIDED.**

The database release remains **1.17** because 1.17 has not shipped. The writer
generation remains **`verified-ownership-v1.17`**. The asynchronous-goal tables
are part of the unreleased 1.17 fresh-install and v1.16 -> v1.17 migration
paths. Development databases already stamped 1.17 use a manual, same-version
addon.

The decision is recorded in the WP-05B2B1 task direction. No named approver, UTC
approval date or separate signed record is asserted.

### DDL preparation

**Status: COMPLETE for this work package.**

The following are prepared and tested:

- PostgreSQL/SQLite goal tables, indexes and constraints;
- fresh 1.17 setup integration;
- v1.16 -> v1.17 migration integration;
- same-version development addons;
- read-only `CheckAsyncGoalPersistenceSchema`;
- SQLite, static and PostgreSQL integration tests;
- correction of the stale 1.18 proposal documents.

Prepared DDL is not approval to apply it to a persistent, shared, staging or
production database.

## Pending decisions

### Transactional Go store

**Status: PENDING.**

Still required:

- the durable repository and CAS adapter;
- checkpoint hydration and serialization;
- duplicate-operation conflict handling and replay outcome;
- transaction/lock ordering;
- cross-table kernel validation;
- integration with the future goal-store startup gate.

### Resume and execution

**Status: PENDING.**

Still required:

- claim/lease, heartbeat and expiry;
- event trigger or bounded poller;
- current-authority refresh;
- cancellation, shutdown and preemption;
- retry, reconciliation and uncertain-write recovery;
- any worker or event consumer.

### Retention and pruning

**Status: PENDING.**

The DDL intentionally implements no retention job. Parent goal runs and child
rows cascade together; a scheduled parent delete while transition history is
still required would violate the audit/replay contract. The final retention
period, archive model and pruning owner require an explicit policy decision.

### Deployment

**Status: PENDING.**

Still required:

- backup and restore rehearsal on a disposable database;
- staging rehearsal;
- deployment scheduling and writer stop/start procedure;
- separate authorization to apply the v1.17 migration or addon to a persistent
  database;
- GitHub CI evidence for this patch.

### Public contract

**Status: PENDING / OUT OF SCOPE.**

No public manifest field, API endpoint, Events payload or example was changed.
Any future public async-goal request/approval/resume contract requires a
separate review.

## Explicit non-approvals

- The existence of the DDL files is not deployment approval.
- The readiness check is not a runtime enablement.
- The kernel tests are not approval of a durable store.
- A chat request, task prompt or inferred intention is not approval of the
  pending phases.
- MySQL goal persistence is not approved and remains unsupported.

## Acceptance checklist for this package

- [x] Actual schema and migration SQL changed, not only design documents.
- [x] Every database release marker remains 1.17.
- [x] `WriterGeneration` remains `verified-ownership-v1.17`.
- [x] PostgreSQL and SQLite fresh, migration and addon paths converge.
- [x] The same-version addon is re-runnable, preserves data and refuses
      incompatible objects.
- [x] No retention promise conflicts with cascading foreign keys.
- [x] Work timestamps distinguish creation, acceptance, row insertion,
      observation, expiry and the logical deadline.
- [x] Transition idempotency is enforceable without a worker.
- [x] Existing MySQL 1.17 startup remains valid and goal persistence is
      explicitly unsupported.
- [x] Goal readiness is fail-closed and is not wired to unrelated startup.
- [x] Existing v1.17 safety tests still pass.
- [x] No deployment, production writer, runner, worker, event consumer or MCP
      mutation path was enabled.
- [x] Remaining risks and pending approvals are recorded without invented
      sign-off.

## Questions still requiring a maintainer decision

1. What retention period applies to terminal goal runs and transitions, and
   who owns the pruning job?
2. Should the future store return historical transition metadata or a
   `replayed` outcome for an exact replay?
3. What is the approved logical-lifetime default and maximum for live runs?
4. Which deployment window and backup/restore procedure applies to the 1.17
   migration and addon?
5. Is MySQL goal persistence permanently unsupported or deferred to a later
   separately approved phase?
