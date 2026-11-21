# WP-05B2B2 Store Contract

## Status

Internal implementation only. The store is unexported, is never constructed by a
production service, and does not enable goal execution.

## Public surface (package-internal)

`pkg/agent/goal_async_store.go`:

- `newGoalAsyncStore(ctx, *database.Handler)` at line 76.
- `CreateCheckpoint(ctx, goalAsyncCheckpoint)` at line 97.
- `LoadCheckpoint(ctx, runID)` at line 179.
- `TransitionCheckpoint(ctx, runID, goalAsyncTransitionRequest)` at line 212.

All types and methods remain unexported. No `pkg/agent` public API was added.

## Construction and schema gate

Construction validates:

- the handler is non-nil;
- the backend is PostgreSQL or SQLite through
  `database.AsyncGoalPersistenceSupported`;
- `CheckAsyncGoalPersistenceSchema` succeeds for the backend.

MySQL and every other backend return
`database.ErrAsyncGoalPersistenceUnsupported`. A missing or incompatible goal
schema returns `database.ErrAsyncGoalPersistenceSchemaUnavailable`. There is no
in-memory fallback.

## Create

`CreateCheckpoint`:

1. validates the checkpoint through `validateGoalAsyncCheckpoint`;
2. requires revision 1 in the `created` state;
3. normalizes only after validation, never as a repair for malformed input;
4. begins one bounded transaction;
5. on SQLite, performs a no-op root update to acquire the writer lock before
   the first read;
6. returns success for an existing run ID only when the validated checkpoint
   digest is identical;
7. returns a typed store conflict when the existing contents differ;
8. inserts the root, frozen tools and frozen sources;
9. commits atomically.

No work, evidence, verification or audit rows exist for a valid `created`
checkpoint; the pure validator enforces that state.

## Load

`LoadCheckpoint`:

- validates the run ID;
- uses a read-only repeatable-read transaction on PostgreSQL and a read
  transaction on SQLite;
- reads the root, tools, sources, work, evidence, verification evidence and
  audit refs from one snapshot;
- reconstructs a `goal-async-v2` checkpoint, including verification IDs;
- normalizes database timestamps to UTC microseconds without normalizing or
  repairing the checkpoint structure;
- validates the checkpoint through `validateGoalAsyncCheckpoint`;
- verifies the transition ledger is complete, contiguous and consistent with
  the root's last transition;
- returns `errGoalAsyncStoreNotFound` for a missing run and
  `errGoalAsyncStoreCorrupt` for inconsistent persisted state.

Loading does not create a resume grant, schedule work, or call a runner, model,
crawler, tool or MCP client.

## Transition

`TransitionCheckpoint`:

1. starts one bounded transaction;
2. on SQLite, acquires the writer lock with a no-op root update;
3. loads and validates the authoritative current checkpoint with root locking
   on PostgreSQL;
4. consults the unique operation ledger `(goal_run_id, operation_id)` before
   applying anything;
5. returns the current validated checkpoint and `replayed` for an exact
   historical replay without writes;
6. returns `ErrGoalAsyncReplayConflict` for the same operation ID with a
   different request hash;
7. applies the existing pure `applyGoalAsyncTransition` function, which
   enforces expected revision, lifecycle state, deadlines and budgets;
8. updates the root with an optimistic `revision` and `state` compare-and-swap;
9. updates work, evidence, verification evidence and audit references;
10. inserts the transition row;
11. commits atomically.

A zero-row root update returns `ErrGoalAsyncRevisionConflict`. A commit error is
reconciled by loading the persisted operation identity; a matching committed
row returns the current checkpoint and `replayed`, while an absent row returns
the original failure.

## Replay semantics

- New operation with the correct revision: applied once.
- Same operation ID and identical request hash: current checkpoint plus
  `replayed`; no write.
- Same operation ID with a different hash: replay conflict.
- New operation with a stale revision: revision conflict.
- Concurrent writers: one commits; the other receives a typed conflict.
- Exact replay of an older operation: recognized through the ledger, not only
  through the last-transition fields.
- The ledger does not contain whole historical checkpoints. A replay returns
  the current validated checkpoint, not a historical snapshot.

The request hash includes the operation content but deliberately excludes the
caller's expected revision and wall-clock `Now`; those are call-time
concurrency/time inputs, not operation identity. A replay therefore remains
recognizable after later revisions.

## Work and timestamp mapping

- Root `created_at` is the goal creation time.
- Work `created_at` is the row insertion time.
- `accepted_at` is the durable operation acceptance time and may predate the
  work-row insertion.
- `observed_at` is the later authoritative completion observation and may
  postdate proposal/approval expiry.
- `proposal_expires_at`, `approval_expires_at`, `submit_by` and the root
  `deadline` are persisted independently.
- Acceptance before approval expiry with completion after approval expiry is
  covered by both SQLite and PostgreSQL tests.
- Loading never resets counters or extends the fixed deadline.

## Concurrency and transaction behavior

- PostgreSQL: `SELECT ... FOR UPDATE` on the root during transitions, plus
  `revision`/`state` compare-and-swap and unique operation constraints.
- SQLite: a no-op `UPDATE AgentGoalRuns SET revision=revision` before the first
  read acquires a write transaction without issuing nested `BEGIN IMMEDIATE`
  inside `sql.Tx`.
- All root and child writes, plus the transition ledger row, share one
  transaction.
- Any failure rolls back the entire transition.
- SQLite `database is locked` is mapped to a typed revision conflict.
- Commit outcomes are reconciled through the persisted operation identity.

## Corruption policy

The loader fails closed when:

- the root is missing required identity, version, state, timestamp or budget
  fields;
- the checkpoint validator rejects the reconstructed state;
- a work/evidence/audit row contradicts the root state;
- evidence source IDs are not a JSON array of unsigned integers;
- verification evidence exists without root verification fields;
- the transition ledger count, revision sequence, state chain, last operation
  ID or last hash does not match the root;
- an unsupported checkpoint version is present.

The loader never silently repairs or normalizes malformed records.
Intermediate transition hashes are historical audit values and are not
independently recomputable from the persisted ledger, which does not store full
historical checkpoints. The last transition hash is bound to the root.

## Deliberate omissions

The store does not:

- infer live authorization from stored identities, approvals or source IDs;
- reconstruct a live resume grant;
- treat crawl status or events as completion proof;
- execute a model, crawler, tool or MCP call;
- run a worker, scheduler, claim, lease or event consumer;
- prune or retain-delete goal history;
- expose a public repository, command or API.
