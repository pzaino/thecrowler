# WP-05B2B2 Maintainer Decisions

## Completed under this work package

### Internal transactional store

The internal, unexported store in `pkg/agent/goal_async_store.go` now:

- requires the schema-1.17 goal DDL at explicit construction;
- rejects MySQL and other unsupported backends with the existing typed error;
- creates revision-1 checkpoints and frozen tool/source scope atomically;
- loads a validated checkpoint from one snapshot;
- applies optimistic transitions with root compare-and-swap;
- recognizes exact replays of any older operation through the unique
  `(goal_run_id, operation_id)` ledger;
- rejects different-hash replays and stale revisions;
- serializes SQLite writers without nested `BEGIN IMMEDIATE`;
- uses PostgreSQL root row locking;
- reconciles commit-outcome uncertainty through the persisted operation identity;
- rejects corrupt roots, children, evidence and transition ledgers.

### PostgreSQL CI regression repair

The shared PostgreSQL statement splitter replaces the naive semicolon split in
the MCP integration test. It recognizes comments, quoted strings and
dollar-quoted bodies. The full six-test MCP PostgreSQL suite passes locally.

### Compatibility

- Schema remains **1.17**.
- Writer generation remains **`verified-ownership-v1.17`**.
- No new table, migration or marker.
- Ordinary 1.17 startup without the optional goal tables remains valid.
- MySQL goal persistence remains unsupported.

## What is still pending

### Production store activation

**Status: PENDING.**

A separate decision is required before any production service constructs the
store. That decision must cover:

- the production readiness/startup gate;
- live account, agent, source ownership and policy refresh;
- integration with the existing manual/event runtime;
- deployment and rollback procedure;
- operational observability and failure policy.

### Resume, event correlation and crash recovery

**Status: PENDING.**

WP-05B3 and later work must add:

- event/operation correlation;
- live authorization refresh;
- safe resumption and crash recovery;
- claim/lease or equivalent single-executor coordination;
- cancellation, shutdown and preemption semantics;
- bounded retry/reconciliation policy.

The internal store deliberately does not implement any of these.

### Retention and pruning

**Status: PENDING.**

No retention job exists. Parent and child rows must be retained together for the
longest approved audit/replay period. The final retention numbers and deletion
owner still require an explicit maintainer policy decision.

### Public contract

**Status: PENDING / OUT OF SCOPE.**

No manifest, configuration, API/Events endpoint, event type or example changed.
A user-facing async-goal request/approval/resume contract requires a separate
review.

## Explicit non-approvals

- The internal store is not production activation.
- Loading a checkpoint is not authorization to resume.
- Stored approvals and operation IDs are not live execution grants.
- No worker, scheduler, event consumer, lease, claim or MCP dispatch is approved
  by this package.
- No production or shared database migration is approved by this package.

## Acceptance checklist

- [x] A checkpoint can be created, saved and loaded after a simulated restart.
- [x] PostgreSQL and SQLite both persist and reconstruct the validated
      checkpoint.
- [x] Transitions are atomic and use optimistic revision checks.
- [x] Exact older replays are recognized through the operation ledger.
- [x] Duplicate operation IDs with different request hashes conflict.
- [x] Concurrent writers cannot both commit the same revision.
- [x] Corrupt roots, children, evidence and ledgers fail closed.
- [x] Accepted-before-approval-expiry and completed-after-approval-expiry is
      representable without extending the goal deadline.
- [x] Counters and deadlines survive restart.
- [x] Prompt, credential, message and raw evidence content is not persisted.
- [x] MySQL returns the existing unsupported error.
- [x] Existing 1.17 startup and schema-version protections remain green.
- [x] The previously failing PostgreSQL MCP test passes locally.
- [x] No production execution path was enabled.

## Next action

Independent review of WP-05B2B2, followed by a separate maintainer decision on
production store activation and WP-05B3 resume/event-correlation work. Do not
construct the store in a production service or apply DDL to a persistent
database automatically.
