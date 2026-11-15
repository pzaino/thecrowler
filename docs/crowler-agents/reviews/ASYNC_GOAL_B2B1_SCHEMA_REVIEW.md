# WP-05B2B1 Schema Review

## Scope

This review covers the eight asynchronous-goal tables added to the unreleased
schema 1.17 in PostgreSQL and SQLite. It does not cover a Go store, runtime
execution, retention jobs or MySQL goal persistence.

## Tables

| Table | Role |
| --- | --- |
| `AgentGoalRuns` | One logical goal run, immutable bindings and ceilings, current state, cumulative counters, revision and reserved cancellation metadata. |
| `AgentGoalRunTools` | Frozen allowed-tool set for the run. |
| `AgentGoalRunSources` | Frozen allowed-source set for the run. No foreign key to `Sources`, so source deletion does not erase historical goal references. |
| `AgentGoalRunWork` | Proposal and accepted-operation references with independent submission-validity and completion timestamps. |
| `AgentGoalRunEvidence` | Bounded sanitized evidence references and hashes. |
| `AgentGoalRunVerificationEvidence` | Links a deterministic completion decision to stored evidence references. |
| `AgentGoalRunAuditRefs` | Bounded redacted audit reference IDs. |
| `AgentGoalRunTransitions` | Durable transition identity ledger. |

The canonical PostgreSQL block starts at
`pkg/database/postgresql-setup.pgsql:4277`; the SQLite block starts at
`pkg/database/sqlite-setup.sqlite3:1751`. The same block is used by the
v1.16 -> v1.17 migrations and by the same-version addons. Drift is checked by
`TestAsyncGoalPersistenceCanonicalDDLConverges`
(`pkg/database/async_goal_schema_test.go:305`).

## Root/checkpoint consistency

The relational root and child rows are the authoritative representation. No
canonical checkpoint blob is stored. The future Go adapter must reconstruct a
`goal-async-v2` checkpoint from these rows and run
`validateGoalAsyncCheckpoint` before use.

The mapping covers every field of `goalAsyncCheckpoint`
(`pkg/agent/goal_async_state.go:742`):

- root identity, principal, goal/policy hashes, state, revision, slices,
  timestamps, reason, ledger ID and run nonce hash;
- all immutable ceilings and cumulative used counters;
- allowed tools and sources through child tables;
- work references through `AgentGoalRunWork`, including proposal, approval,
  operation, source, expiry and acceptance/completion timestamps;
- evidence references through `AgentGoalRunEvidence`;
- verification through the root verifier/hash/time columns plus
  `AgentGoalRunVerificationEvidence`;
- audit references through `AgentGoalRunAuditRefs`;
- last transition ID/hash through the root columns and the transition ledger.

Ordering that the checkpoint represents as sorted slices is reconstructed by
ordering unique child keys. The database cannot preserve arbitrary JSON order,
and the kernel already requires sorted unique tools, sources, audit refs and
verification evidence IDs.

## Work timestamp semantics

`AgentGoalRunWork.created_at` means the time the work-reference row was
inserted. It is **not** goal creation time. The DDL therefore does not require
`accepted_at >= work.created_at`; an operation may be accepted before its
reference row is persisted.

The independent timestamps are:

| Field | Meaning |
| --- | --- |
| `AgentGoalRuns.created_at` | Goal creation time. |
| `AgentGoalRunWork.created_at` | Work-reference row insertion time. |
| `accepted_at` | Durable crawl-operation acceptance time. |
| `observed_at` | Authoritative completion observation time. |
| `proposal_expires_at` | Proposal validity limit. |
| `approval_expires_at` | Approval validity limit for new submission. |
| `submit_by` | Last permitted submission time. |
| `AgentGoalRuns.deadline` | Fixed logical goal deadline. |

The work checks mirror `goalAsyncWorkRef.validate`
(`pkg/agent/goal_async_state.go:456`): accepted time must be before
`submit_by`, proposal expiry and approval expiry; completion observation must
be at or after acceptance and at or before the row update. Proposal/approval
expiry does not bound later completion, so an operation accepted while valid
can complete after approval expiry but before the logical goal deadline.

The database cannot enforce cross-table invariants such as
`accepted_at >= AgentGoalRuns.created_at` or
`observed_at <= checkpoint.updated_at`; those remain adapter validation
requirements.

## Replay ledger

`AgentGoalRunTransitions` has:

- primary key `(goal_run_id, revision)`;
- named unique index `uq_agent_goal_transition_operation` on
  `(goal_run_id, operation_id)`;
- `transition_hash`, `from_state`, `to_state`, `reason_code` and `created_at`.

A later revision cannot insert the same operation identity twice. A replay with
the same operation ID and same hash can return the stored transition metadata
or a `replayed` outcome; a replay with a different hash is a conflict that the
future store must detect by reading the existing row. The database does not
store whole historical checkpoints and the implementation must not promise
historical snapshot reconstruction.

## Unique and lookup indexes

| Object | Columns | Purpose |
| --- | --- | --- |
| `uq_agent_goal_work_operation` | `(goal_run_id, operation_id)` | One work row per durable operation. |
| `uq_agent_goal_work_approval` | `(goal_run_id, approval_id)` | One work row per approval. |
| `uq_agent_goal_evidence_record` | `(goal_run_id, record_sha256)` | Evidence deduplication by content identity. |
| `uq_agent_goal_transition_operation` | `(goal_run_id, operation_id)` | Replay identity. |
| `idx_agent_goal_work_state` | `(goal_run_id, kind, state)` | Work-state lookup. |
| `idx_agent_goal_evidence_observed` | `(goal_run_id, observed_at)` | Evidence ordering. |
| `idx_agent_goal_transitions_created` | `(goal_run_id, created_at)` | Transition ordering. |
| `idx_agent_goal_runs_state_deadline` | `(state, deadline)` | Future expiry/reconciliation scan. |
| `idx_agent_goal_runs_principal` | `(principal_user_id, state)` | Principal lookup. |
| `idx_agent_goal_runs_agent` | `(agent_id, state)` | Agent lookup. |
| `idx_agent_goal_runs_owner` | `(owner, state)` | Owner lookup. |

No further indexes were added for hypothetical query plans.

## Dialect differences

- PostgreSQL uses `TIMESTAMPTZ(6)`, `BIGINT`, `JSONB`, regular-expression hash
  checks and `INTERVAL '24 hours'`.
- SQLite uses text timestamps with `julianday` validity and ordering checks,
  integer counters, JSON validity checks and named CHECK constraints.
- SQLite explicitly rejects invalid timestamp text; without the validity
  predicates, `julianday()` would return NULL and a CHECK would pass.
- Both dialects use named primary, unique, foreign-key and CHECK constraints
  where the readiness/addon checks need a stable name.

## Retrieval and privacy

No table stores prompts, model messages, tool arguments or results, evidence
payloads, credentials, tokens, DSNs or cookies. Evidence is represented by
hashes, source IDs, producer, trust class, timestamps and a bounded size.
`AgentGoalRunEvidence.source_ids` is a JSON array of source IDs, not a payload.
Root identity strings, tool names, producers and audit references have explicit
upper bounds, and evidence source arrays are limited to 32 entries.

## Known limitations

- Structural checks verify columns, forbidden columns, indexes, unique-column
  sets, primary keys, foreign-key parent/column/cascade targets and named CHECK
  constraints. They do not prove that a CHECK expression has identical
  semantics; a database owner can construct a same-named object with different
  behavior.
- Cross-table kernel invariants remain the adapter's responsibility.
- No retention or pruning job is implemented. Parent and child rows must be
  retained together for the longest approved audit/replay period.
- MySQL has no goal tables and is reported as unsupported.
