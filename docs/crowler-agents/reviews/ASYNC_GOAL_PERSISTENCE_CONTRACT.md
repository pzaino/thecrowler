# Async Goal Persistence and Resume Contract

## Status

**The schema objects for this contract are part of the unreleased schema 1.17.
An internal, unexported transactional Go store now implements checkpoint
create, load and optimistic transition. The resume engine, live authorization
refresh, production wiring and public contracts remain unimplemented.**

The pure kernel is in `pkg/agent/goal_async_state.go`; the internal store is in
`pkg/agent/goal_async_store.go`. This document specifies the durable and resume
contract. It is grounded in the current schema and runtime, not in assumed
tables. The database release stays 1.17 and the writer generation stays
`verified-ownership-v1.17`; this document does not authorize a new release
version or a production migration.

## Existing storage evaluated

| Existing structure | What it is | Reuse decision |
| --- | --- | --- |
| `CrawlProposals` | Durable model/operator crawl proposal, state and submitted operation ID | **Do not reuse.** It is a crawl proposal lifecycle, not an agent goal checkpoint. It lacks goal state, counters, evidence refs, deadline and revision. |
| `CrawlApprovals` | Operator approval for a crawl proposal | **Do not reuse.** Approval is not goal state and is not execution proof. The checkpoint may reference its ID/hash. |
| `Status` | Crawl scheduling operation row keyed by `status_sha256`, with `details` JSON | **Do not reuse as the goal row.** It is the external work record. Its operation key is the work reference. |
| `MCPMutationProposals` / `MCPMutationApprovals` | MCP mutation proposal/approval lifecycle | **Do not reuse.** Different lifecycle, different authority, separately gated. |
| `DBSchemaVersion` / `DatabaseWriterCompatibility` | Schema and writer compatibility metadata | **Do not overload.** The goal tables are part of the unreleased 1.17 DDL. The schema version and writer generation stay unchanged; a same-version addon updates development databases already stamped 1.17. |
| `AgentMemory` / memory store | Short/long-term agent memory | **Do not reuse.** Memory is not a durable goal-run state machine. |

## Proposed durable records

The names below are the schema-1.17 tables prepared by WP-05B2B1. The database
release remains 1.17 and the writer generation remains
`verified-ownership-v1.17`. The internal unexported store now uses these
tables; no production service constructs it.

### 1. `AgentGoalRuns`

One logical asynchronous goal run. Primary key: `goal_run_id`.

Required columns:

- `goal_run_id VARCHAR(128) PRIMARY KEY`
- `trace_id VARCHAR(128) NOT NULL`
- `correlation_id VARCHAR(128)`
- `agent_id VARCHAR(128) NOT NULL`
- `owner VARCHAR(256) NOT NULL`
- `principal_kind VARCHAR(16) NOT NULL` (`user` or `system`)
- `principal_user_id BIGINT NOT NULL DEFAULT 0`
- `principal_username VARCHAR(256) NOT NULL DEFAULT ''`
- `principal_issuer VARCHAR(512) NOT NULL DEFAULT ''`
- `goal_id VARCHAR(128) NOT NULL`
- `goal_spec_hash CHAR(64) NOT NULL`
- `contract_version VARCHAR(32) NOT NULL` (`goal-async-v2`)
- `checkpoint_version SMALLINT NOT NULL` (`2`)
- `policy_sha256 CHAR(64) NOT NULL`
- `state VARCHAR(32) NOT NULL`
- `revision BIGINT NOT NULL`
- `created_at TIMESTAMPTZ(6) NOT NULL`
- `updated_at TIMESTAMPTZ(6) NOT NULL`
- `deadline TIMESTAMPTZ(6) NOT NULL`
- `logical_lifetime_ns BIGINT NOT NULL` (effective lifetime
  `deadline - created_at`, derived and checked)
- `reason_code VARCHAR(64) NOT NULL`
- `ledger_id VARCHAR(128) NOT NULL`
- `run_nonce_hash CHAR(64) NOT NULL`
- `last_transition_id VARCHAR(128)`
- `last_transition_hash CHAR(64)`
- `verification_sha256 CHAR(64)`
- `verified_at TIMESTAMPTZ(6)`
- `cancel_requested_at TIMESTAMPTZ(6)` and
  `cancel_requested_by_user_id BIGINT` for the later resume engine
- `created_at`/`updated_at` are UTC microsecond precision.

Immutable ceiling columns:

- `max_slices INTEGER`, `max_model_rounds INTEGER`, `max_tool_attempts INTEGER`,
  `max_result_bytes BIGINT`, `max_evidence_records INTEGER`,
  `max_evidence_bytes BIGINT`, `max_retries INTEGER`,
  `max_scheduled_work_requests INTEGER`, `max_consecutive_denials INTEGER`,
  `max_slice_elapsed_ns BIGINT` (the synchronous slice ceiling, not the
  logical lifetime).

Cumulative used columns:

- `used_slices INTEGER`, `used_model_rounds INTEGER`,
  `used_tool_attempts INTEGER`, `used_result_bytes BIGINT`,
  `used_evidence_records INTEGER`, `used_evidence_bytes BIGINT`,
  `used_retries INTEGER`, `used_scheduled_work_requests INTEGER`,
  `used_consecutive_denials INTEGER`.

Constraints:

- `revision > 0`;
- all ceilings and used counters non-negative;
- every used counter `<=` its ceiling, with `used_consecutive_denials`
  allowed to reset to zero but never exceed its ceiling;
- `updated_at >= created_at`;
- `deadline > created_at` and `logical_lifetime_ns = deadline - created_at`;
- `max_slice_elapsed_ns` is 1..120 seconds; the whole-goal deadline may exceed
  it;
- active and completion states have `updated_at < deadline`; terminal
  cancellation/budget/denial/failure may be at or after the deadline;
- `principal_kind = 'user'` requires a positive user ID and non-empty
  username/issuer; `system` requires zero/empty principal fields;
- `state = 'criteria_met'` requires non-null verification columns and a
  non-null reason code in `criteria_all_met`/`criteria_any_met`;
- other states require null verification columns;
- `state = 'created'` requires revision 1, zero used counters, no work/evidence
  rows and no last-transition identity.

### 2. `AgentGoalRunTools` and `AgentGoalRunSources`

Frozen scope, normalized rather than stored as a mutable map:

- `AgentGoalRunTools(goal_run_id, tool_name, PRIMARY KEY(goal_run_id, tool_name))`
  with a cascade delete from `AgentGoalRuns`.
- `AgentGoalRunSources(goal_run_id, source_id, PRIMARY KEY(goal_run_id, source_id))`
  with a cascade delete from `AgentGoalRuns`.

`source_id` is retained as historical scope. A foreign key to `Sources` is
deliberately not proposed: deleting a source must not destroy the goal audit
trail. Application validation ensures `source_id > 0` and current ownership is
re-read separately at resume.

### 3. `AgentGoalRunWork`

One row per outstanding work reference:

- `goal_run_id` (FK cascade);
- `request_id CHAR(64)`;
- `kind` (`crawl_proposal` or `crawl_operation`);
- `state` (`awaiting_approval`, `scheduled`, `processing`, `completed`);
- `proposal_sha256 CHAR(64)`;
- `approval_id CHAR(64)` nullable;
- `approval_sha256 CHAR(64)` nullable;
- `operation_id CHAR(64)` nullable;
- `source_id BIGINT NOT NULL`;
- `proposal_expires_at TIMESTAMPTZ(6) NOT NULL`;
- `approval_expires_at TIMESTAMPTZ(6)` nullable;
- `submit_by TIMESTAMPTZ(6)` nullable;
- `accepted_at TIMESTAMPTZ(6)` nullable;
- `observed_at TIMESTAMPTZ(6)` nullable;
- `created_at`, `updated_at`;
- primary key `(goal_run_id, request_id)`;
- partial unique indexes on `(goal_run_id, operation_id)` and
  `(goal_run_id, approval_id)` where non-null;
- checks preserving the state/identity matrix:
  - proposal: proposal expiry required; no approval, operation, submit-by,
    accepted-at or observed field;
  - operation scheduled/processing: proposal expiry, approval expiry,
    submit-by, accepted-at, approval and operation required; observed null;
  - operation completed: the same fields plus observed-at required;
  - operation: `accepted_at >= created_at`,
    `accepted_at <= updated_at`, `accepted_at < submit_by`,
    `submit_by <= proposal_expires_at`, `submit_by <= approval_expires_at`,
    `accepted_at < proposal_expires_at`, `accepted_at < approval_expires_at`;
  - completed: `observed_at >= accepted_at`, and `observed_at <= updated_at`;
  - **no** `observed_at < approval_expires_at` or
    `observed_at < proposal_expires_at` check. Those expiries bound
    submission, not later completion;
- `source_id` must be present in `AgentGoalRunSources` (application check or a
  composite FK).

This table is the durable representation of `goalAsyncWorkRef`. The kernel's
16-work bound and scheduled-work ceiling apply. `proposal_expires_at`,
`approval_expires_at` and `submit_by` are distinct submission-validity facts;
`accepted_at` is the durable operation creation; `observed_at` is the later
completion observation.

### 4. `AgentGoalRunEvidence`

Bounded evidence references only:

- primary key `(goal_run_id, evidence_id)`;
- `kind`, `trust_class` (`system_verified` or `tool_observation` only),
  `producer`;
- `source_ids_json` (sorted) or a child table if later needed;
- `observed_at`, `expires_at` (UTC microseconds);
- `payload_sha256 CHAR(64)`;
- `record_sha256 CHAR(64)`, unique per goal run;
- `size_bytes INTEGER NOT NULL CHECK (size_bytes > 0)`;
- no payload, observed value, prompt, argument or response column.

The row points to a trusted observation; the verifier re-reads live evidence
and validates the payload hash/record hash. Evidence IDs and hashes do not
become authorization.

`AgentGoalRunVerificationEvidence(goal_run_id, evidence_id)` records the
bounded verifier evidence IDs for a `criteria_met` checkpoint, with a composite
foreign key to `AgentGoalRunEvidence`. It keeps the stored completion
self-auditing without storing observed values.

### 5. `AgentGoalRunAuditRefs`

- `(goal_run_id, audit_ref)` primary key;
- `created_at`.

Only redacted audit reference IDs. No reason text, tool arguments, model text
or raw source configuration.

### 6. `AgentGoalRunTransitions`

Idempotency and audit ledger:

- `goal_run_id` (FK cascade);
- `revision BIGINT NOT NULL`;
- `operation_id VARCHAR(128) NOT NULL`;
- `transition_hash CHAR(64) NOT NULL`;
- `from_state`, `to_state`, `reason_code`;
- `created_at`;
- primary key `(goal_run_id, revision)`;
- unique `(goal_run_id, operation_id)`.

The main-row CAS is the concurrency guard. The transition row is the replay
record: the same operation ID and hash is `replayed`; the same ID with a
different hash is a conflict; a stale expected revision is a conflict.

## Canonical checkpoint wire v2

The internal store and any future durable adapter must store and validate the
canonical v2 checkpoint representation:

- `checkpoint_version = 2`, `contract_version = goal-async-v2`;
- deterministic field ordering and sorted unique identity lists;
- UTC microsecond timestamps; no local timezone or sub-microsecond values;
- explicit zero values for absent optional timestamps, never a silently
  normalized alternative; required fields must be present and non-null, and a
  missing/null required value fails validation;
- bounded arrays, strings, work/evidence/audit references and total bytes;
- `max_slice_elapsed_ns` for the synchronous slice ceiling and
  `deadline - created_at` for the logical lifetime;
- no normalization on the read boundary. Parsing rejects unknown fields,
  duplicate JSON object keys at any depth, trailing data, invalid UTF-8,
  unknown versions, unsorted/duplicate lists, zero/duplicate IDs, malformed
  timestamps and inconsistent verification/work/evidence references;
- the canonical bytes used by `goalAsyncCheckpointDigest` include the v2
  domain separator. The digest is an unkeyed integrity/audit reference, not
  authentication.

No production durable checkpoint exists yet, so there is no data migration in
this package. The version bump is deliberate because the work-reference
timestamps, slice ceiling and canonical strictness changed.

## Indexes

- `AgentGoalRuns(state, deadline)` for eligible resume candidates;
- `AgentGoalRuns(principal_user_id, state)` for user-owned work;
- `AgentGoalRuns(agent_id, state)`;
- `AgentGoalRuns(owner, state)`;
- `AgentGoalRunWork(goal_run_id, kind, state)`;
- `AgentGoalRunWork(operation_id)` where non-null;
- `AgentGoalRunEvidence(goal_run_id, observed_at)`;
- `AgentGoalRunTransitions(goal_run_id, created_at)`.

## Internal store boundary (WP-05B2B2)

The internal store exposes three unexported operations:

- `CreateCheckpoint` writes the revision-1 root, frozen tools and frozen
  sources in one transaction. An existing run ID with the same validated
  checkpoint is an idempotent success; different contents are a typed conflict.
- `LoadCheckpoint` reads the root and all children from one consistent
  snapshot, reconstructs a `goal-async-v2` checkpoint, validates it and verifies
  that the transition ledger is complete, contiguous and consistent with the
  root's last transition. It does not create a resume grant or authorize
  execution.
- `TransitionCheckpoint` locks the root, consults the unique operation ledger,
  applies the existing pure transition function, updates the root and children,
  appends the transition row and commits atomically. An exact replay of any
  stored operation returns the current validated checkpoint and a `replayed`
  outcome without writing. A different hash for the same operation is a replay
  conflict; a stale expected revision is a revision conflict.

SQLite writer transactions are serialized by a no-op update on the goal root
before the first read. This acquires the SQLite writer lock without issuing a
nested `BEGIN IMMEDIATE` inside an active `sql.Tx`. PostgreSQL uses row locks
and compare-and-swap conditions. A commit error is reconciled by reading the
persisted operation identity before a retry is considered.

The store does not reconstruct current account, agent, source ownership or
policy authority from stored fields. Those checks remain the responsibility of
the future resume/activation adapter. Intermediate transition hashes are
historical audit data; only the last transition hash is bound to the root, so
the store does not promise historical whole-checkpoint reconstruction.

## Transaction and CAS contract

Use one transaction per checkpoint revision:

1. PostgreSQL: `READ COMMITTED` plus explicit row locks; SQLite: a
   write-acquiring transaction (the internal store performs a no-op root update
   before its first read) and `PRAGMA foreign_keys=ON`.
2. Lock current-authority rows in the established global order:
   source rows ascending `source_id`, user rows ascending `user_id`,
   administrative grant rows, then the goal-run row. This preserves the
   existing source/user/grant lock order.
3. Re-read the live account, agent and source ownership, and the exact current
   policy. Compare the checkpoint's immutable bindings. For a new scheduling
   admission, require a trusted scheduling prerequisite with approval valid at
   submission. For resume of an already accepted operation, do **not** require
   the historic approval to remain unexpired; any new privileged action later
   requires a fresh approval and authorization check. Any mismatch denies
   resume before a write.
4. Update the goal row with a compare-and-swap:

   ```sql
   UPDATE AgentGoalRuns
      SET revision = revision + 1,
          state = :next_state,
          updated_at = :now,
          used_* = :new_used_*,
          last_transition_id = :operation_id,
          last_transition_hash = :transition_hash
    WHERE goal_run_id = :goal_run_id
      AND revision = :expected_revision
      AND state = :expected_state
      AND cancel_requested_at IS NULL
   ```

   A zero-row result is a revision/state/cancellation conflict; reload and
   return a typed conflict.
5. Consult the unique transition ledger `(goal_run_id, operation_id)` before
   applying a new revision. A later replay of **any** stored operation, not
   only the last one, must return the stored result when the hash matches and
   a typed conflict when it differs. Do not apply an older operation ID as a
   new transition merely because the current revision matches. Insert the new
   transition row in the same transaction; the unique key is the durable
   idempotency boundary.
6. Apply child work/evidence/audit changes in the same transaction. Work
   replacement (proposal -> operation) is a delete plus insert. Evidence rows
   are insert-only with unique identity/hash.
7. Commit. Any error rolls back the entire checkpoint revision.

The pure kernel's `expected_revision`, last operation ID and transition hash
map to this contract. The kernel only recognizes the last operation for
in-process replay; the durable store must use the complete transition ledger
to recognize older replays. The kernel does not claim that a database
transaction exists.

## Scheduling prerequisite construction

The future trusted adapter constructs the unexported scheduling prerequisite
only after transactional reads that establish all of:

- the `CrawlProposals` row: request ID, run ID, agent ID, owner,
  principal user ID, source ID, proposal SHA-256, state and proposal expiry;
- the `CrawlApprovals` row: approval ID, proposal/request/run/agent/owner/
  principal/source binding, approval SHA-256, approver identity, approved-at,
  expiry and revocation state;
- the `Status` row keyed by `status_sha256`: durable operation ID, request ID,
  run ID, principal user ID, agent ID, owner, source ID, operation type,
  approval ID/hash, approved-at, submit-by expiry, state and `created_at`;
- current source ownership/verified provenance and current agent/policy
  authority.

The prerequisite binds exact run, goal, goal-spec hash, agent, owner,
principal, source, request, proposal hash, approval ID/hash, operation ID,
operation state (`scheduled` or `processing`), proposal/approval/submit-by
expiries, accepted-at and observation time. A proposal `approval_required`
result, model text, MCP result body, caller boolean or `crawl_completed` event
is never sufficient. Approval must be valid at submission; later expiry does
not invalidate the accepted operation's provenance.

## Resume authorization

### Initial identity

`services/events/agent_run.go` currently derives the user principal from the
authenticated middleware identity, resolves the active account, requires the
`events:agent:run` (or `events:*`) scope and passes
`MCPMutationPrincipalFromAuthIdentity` into the manual agent run. A checkpoint
for that path records `principal_kind=user`.

Background event runs have no authenticated user principal. They must be
recorded as `principal_kind=system`; a stored user principal must never be
invented for them. The current goal runtime has no first-class tenant ID, so no
tenant field is proposed.

### Checks at resume

Before any new slice or external submission, from live trusted sources:

- current user/account active, current roles/scopes;
- current agent exists, is enabled, has the required trust/capability/contract;
- every source in the checkpoint is currently verified-owned by the bound
  owner/principal and is not deleted/disabled; the async contract is source-scoped and
  does not authorize source-neutral external-resource goals;
- the current operator policy ID/version/canonical hash exactly matches
  `policy_sha256`; no expansion or narrowing is silently accepted;
- the referenced proposal/operation still matches the checkpoint's IDs,
  proposal/approval hashes and accepted-at provenance;
- the goal spec hash still matches the frozen manifest;
- for a **new** scheduling action, a fresh approval is current, unexpired,
  unrevoked and issued by an authorized approver. For reading or evaluating an
  already accepted operation's completion evidence, the historic approval need
  not remain unexpired; new privileged actions still require the fresh check.

A revoked user, transferred source, disabled source/agent, changed policy,
changed approval or mismatched operation denies resume. The checkpoint's
`allowed_tools`/`allowed_sources` are data about the original frozen scope, not
a current grant.

### Lock order and cancellation

The goal row is locked after the source/user/grant authority rows. Cancellation
only needs the goal row; it does not acquire source locks. Therefore:

- cancellation and resume serialize on the goal row;
- if cancellation commits first, resume CAS fails and no new work is prepared;
- if resume commits first, cancellation still marks the goal cancelled and the
  later claim/dispatch phase must not start new work;
- a `cancel_requested_at` check is required before any claim, submission or
  model/tool call.

## Event and operation correlation

`pkg/crawler/crawler.go` `CreateCrawlCompletedEvent` creates a
`crawl_completed` event with `SourceID`, type, severity and crawler status
details. It does **not** carry the goal run ID, request ID, proposal hash or
operation ID. It is not a reliable correlation for a specific goal work item.

The plan's `event_observed` predicate remains disabled in the current async
contract. Completion must
use trusted evidence:

- the `Status` operation row keyed by `status_sha256`, whose `details` JSON
  contains `request_id`, `run_id`, `principal_user_id`, `agent_id`, `owner`,
  `source_id`, `operation_type`, `approval_id`, `approval_sha256`,
  `approved_by_user_id`, `approved_at`, `expires_at`, state and reason;
- `CrawlProposals` state/operation binding;
- `CrawlApprovals` state/revocation/expiry;
- trusted source-status/metadata evidence through the existing evidence
  providers.

A source-level `crawl_completed` event or a source status of completed is not
proof that a particular operation completed. If bounded polling is needed, it
must be defined in the resume phase with a bounded interval, no permanently held model,
MCP or Selenium connection, and explicit timeout semantics.

## Crash and replay recovery

| Failure point | Required behavior |
| --- | --- |
| Crash before scheduling | No work row or proposal remains pending. Retry with the same request/tool-call identity; the scheduler's idempotency key prevents duplicate work. |
| Crash after scheduler commit, before checkpoint update | Read the authoritative `Status` operation by operation/request identity, then CAS the checkpoint to `awaiting_external_work`. Do not schedule again. |
| Crash after evidence recorded, before checkpoint update | Evidence references must be written in the same transaction as the checkpoint, or re-read by trusted evidence IDs/hashes and revalidated. If that is impossible, mark the transition uncertain and re-verify; do not claim completion. |
| Duplicate event | Use the operation/request identity and transition operation ID. An exact replay returns unchanged; a different hash is a conflict. |
| Timeout after uncertain scheduler write | The scheduler returns `ErrCrawlSchedulingUncertain`; reconcile the operation record. Do not mark success or failure until the operation is authoritative. |
| Concurrent resume | One CAS wins; the other reloads. No checkpoint writes are lost. External work claim/lease is the resume phase, not this proposal. |

## Work claiming and double execution

The CAS in the persistence phase prevents duplicate checkpoint revisions, but
it does not by itself prevent two workers from executing the same slice or
submitting the same external operation. The resume phase must add a claim/lease
with a unique owner, expiry, heartbeat and cancellation check, or an equivalent
transactional claim with `FOR UPDATE SKIP LOCKED` on PostgreSQL and a
serialized SQLite claim. The existing scheduler's operation ID/request ID
idempotency is a second line of defense, not a replacement for a goal claim.

## Schema, migration and compatibility

The database release schema remains **1.17** because 1.17 has not shipped. The
writer generation remains **`verified-ownership-v1.17`**. The asynchronous-goal
tables are part of the unreleased 1.17 fresh-install and v1.16 -> v1.17
migration paths, and a same-version additive script updates development
databases already stamped 1.17:

- PostgreSQL: `db_migrations/postgresql-agent-goal-v1.17.pgsql`
- SQLite: `db_migrations/sqlite-agent-goal-v1.17.sqlite3`

Those scripts are manual, opt-in, administrator operations. They do not change
`DBSchemaVersion` or `DatabaseWriterCompatibility`, are safe to re-run, preserve
goal history, and fail closed when a same-named object is structurally
incompatible. They are not applied at service startup. The migration files are
source artifacts, not approval to apply production DDL.

MySQL keeps its existing 1.17 startup contract. Asynchronous goal persistence is
explicitly unsupported on MySQL in this phase; no MySQL goal tables are added.
`CheckAsyncGoalPersistenceSchema` returns the typed unsupported result there.

The internal store calls the read-only `CheckAsyncGoalPersistenceSchema` during
explicit construction, before permitting create, load or transition. That check
is deliberately not wired into ordinary agent, crawler or Events startup,
because development databases already stamped 1.17 may not have the addon yet.

The new tables are additive. No existing column, index or row is changed.
Rollback requires stopping writers and dropping only the new tables, or
restoring the prior backup. A production migration must be backed up and
rehearsed on a disposable database, and remains subject to separate deployment
approval.

## Retention and privacy

No pruning or retention job is implemented in this work package. Parent goal
runs and their child rows use cascading foreign keys, so the parent and children
must be kept together for at least the longest approved audit/replay period. A
scheduled prune of a parent while transition history is still required would be
a contract violation. The final retention numbers and the pruning owner remain
pending maintainer policy confirmation; do not encode a 30-day parent / 90-day
transition split in code or tests.

- No raw prompts, model messages, tool arguments, results, passwords, bearer
  tokens, API keys, DSNs, MCP credentials, cookies or source configuration
  blobs.
- Evidence queries return IDs, trust class, producer, hashes and timestamps;
  raw observations remain in their trusted source/tool systems.

## Exact approvals still required

1. **Persistence activation phase:** the internal transactional store is
   implemented and tested, but approval is still required before production
   wiring, live authorization refresh, logical-lifetime policy, retention or
   deployment. The 1.17 DDL and same-version addon do not authorize a
   production migration or runtime execution.
2. **Resume phase:** approve or reject the claim/lease, event or bounded-poll
   trigger, current-authority refresh, cancellation/shutdown and recovery
   engine.
3. **Public contract phase:** approve or reject any new public YAML/JSON
   configuration field, event type, endpoint, example or user contract.

None of these is approved by this document.
