# Async Goal Persistence and Resume Contract

## Status

**Design proposal only. No migration, table, index, query or database write was
implemented in this work package.**

The pure kernel is in `pkg/agent/goal_async_state.go`. This document specifies
what a separately approved durable store and resume engine would need. It is
grounded in the current schema and runtime, not in assumed tables.

## Existing storage evaluated

| Existing structure | What it is | Reuse decision |
| --- | --- | --- |
| `CrawlProposals` | Durable model/operator crawl proposal, state and submitted operation ID | **Do not reuse.** It is a crawl proposal lifecycle, not an agent goal checkpoint. It lacks goal state, counters, evidence refs, deadline and revision. |
| `CrawlApprovals` | Operator approval for a crawl proposal | **Do not reuse.** Approval is not goal state and is not execution proof. The checkpoint may reference its ID/hash. |
| `Status` | Crawl scheduling operation row keyed by `status_sha256`, with `details` JSON | **Do not reuse as the goal row.** It is the external work record. Its operation key is the work reference. |
| `MCPMutationProposals` / `MCPMutationApprovals` | MCP mutation proposal/approval lifecycle | **Do not reuse.** Different lifecycle, different authority, separately gated. |
| `DBSchemaVersion` / `DatabaseWriterCompatibility` | Schema and writer compatibility metadata | **Do not overload.** A new migration and version decision are required. |
| `AgentMemory` / memory store | Short/long-term agent memory | **Do not reuse.** Memory is not a durable goal-run state machine. |

## Proposed durable records

The names below are proposals. The final names, schema version and writer
generation require maintainer approval.

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
- `contract_version VARCHAR(32) NOT NULL`
- `checkpoint_version SMALLINT NOT NULL`
- `policy_sha256 CHAR(64) NOT NULL`
- `state VARCHAR(32) NOT NULL`
- `revision BIGINT NOT NULL`
- `created_at TIMESTAMPTZ(6) NOT NULL`
- `updated_at TIMESTAMPTZ(6) NOT NULL`
- `deadline TIMESTAMPTZ(6) NOT NULL`
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
  `max_scheduled_work_requests INTEGER`, `max_consecutive_denials INTEGER`.

Cumulative used columns:

- `used_slices INTEGER`, `used_model_rounds INTEGER`,
  `used_tool_attempts INTEGER`, `used_result_bytes BIGINT`,
  `used_evidence_records INTEGER`, `used_evidence_bytes BIGINT`,
  `used_retries INTEGER`, `used_scheduled_work_requests INTEGER`.

Constraints:

- `revision > 0`;
- all ceilings and used counters non-negative;
- every used counter `<=` its ceiling, with `used_consecutive_denials`
  allowed to reset to zero but never exceed its ceiling;
- `updated_at >= created_at`;
- `deadline > created_at`;
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
- `expires_at TIMESTAMPTZ(6) NOT NULL`;
- `observed_at TIMESTAMPTZ(6)` nullable;
- `created_at`, `updated_at`;
- primary key `(goal_run_id, request_id)`;
- partial unique indexes on `(goal_run_id, operation_id)` and
  `(goal_run_id, approval_id)` where non-null;
- checks preserving the state/identity matrix:
  - proposal: no approval/operation/observed fields;
  - operation scheduled/processing: approval and operation required, observed
    null;
  - operation completed: approval/operation/observed required, and
    `observed_at < expires_at`;
- `source_id` must be present in `AgentGoalRunSources` (application check or a
  composite FK).

This table is the durable representation of `goalAsyncWorkRef`. The kernel's
16-work bound and scheduled-work ceiling apply.

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

## Indexes

- `AgentGoalRuns(state, deadline)` for eligible resume candidates;
- `AgentGoalRuns(principal_user_id, state)` for user-owned work;
- `AgentGoalRuns(agent_id, state)`;
- `AgentGoalRuns(owner, state)`;
- `AgentGoalRunWork(goal_run_id, kind, state)`;
- `AgentGoalRunWork(operation_id)` where non-null;
- `AgentGoalRunEvidence(goal_run_id, observed_at)`;
- `AgentGoalRunTransitions(goal_run_id, created_at)`.

## Transaction and CAS contract

Use one transaction per checkpoint revision:

1. PostgreSQL: `READ COMMITTED` plus explicit row locks; SQLite:
   `BEGIN IMMEDIATE` and `PRAGMA foreign_keys=ON`.
2. Lock current-authority rows in the established global order:
   source rows ascending `source_id`, user rows ascending `user_id`,
   administrative grant rows, then the goal-run row. This preserves the
   existing source/user/grant lock order.
3. Re-read the live account, agent, source ownership, policy and approval
   checks. Compare the checkpoint's immutable bindings. Any mismatch denies
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
5. Insert the transition row with unique `(goal_run_id, operation_id)`. If the
   row already exists, compare the hash: same hash is a replay; different hash
   is a conflict.
6. Apply child work/evidence/audit changes in the same transaction. Work
   replacement (proposal -> operation) is a delete plus insert. Evidence rows
   are insert-only with unique identity/hash.
7. Commit. Any error rolls back the entire checkpoint revision.

The pure kernel's `expected_revision`, last operation ID and transition hash
map directly to this contract. The kernel does not claim that a database
transaction exists.

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
  owner/principal and is not deleted/disabled;
- the current operator policy ID/version/canonical hash exactly matches
  `policy_sha256`; no expansion or narrowing is silently accepted;
- the referenced crawl approval is current, unexpired, unrevoked and issued by
  an authorized approver;
- the referenced proposal/operation still matches the checkpoint's IDs and
  hashes;
- the goal spec hash still matches the frozen manifest.

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

The plan's `event_observed` predicate remains disabled in v1. Completion must
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

The CAS in the persistence phase prevents duplicate checkpoint revisions, but it does not by
itself prevent two workers from executing the same slice or submitting the
same external operation. the resume phase must add a claim/lease with a unique owner,
expiry, heartbeat and cancellation check, or an equivalent transactional claim
with `FOR UPDATE SKIP LOCKED` on PostgreSQL and a serialized SQLite claim.
The existing scheduler's operation ID/request ID idempotency is a second line
of defense, not a replacement for a goal claim.

## Schema, migration and compatibility

The current startup contract requires schema `1.17` and writer generation
`verified-ownership-v1.17`. Adding these tables without a version change would
make the new storage invisible to the existing startup check. Do not silently
extend 1.17.

Recommended approval scope for the persistence phase:

- a new additive migration, proposed as schema `1.18`;
- a new writer generation, proposed as `goal-checkpoint-v1.18` (the final name
  is a maintainer decision);
- updated `DBSchemaVersion`, `DatabaseWriterCompatibility`, startup
  compatibility checks and deployment backup/rollback procedure;
- PostgreSQL 17 and PostgreSQL 15.10 validation;
- SQLite migration and foreign-key/startup validation;
- explicit unsupported MySQL behavior.

The new tables are additive. No existing column, index or row is changed.
Rollback requires stopping writers, exporting the new tables, dropping only the
new tables, and restoring the prior writer generation. A production migration
must be backed up and rehearsed on a disposable database.

## Retention and privacy

- Terminal goal runs: retain for an operator-approved period (proposed 30
  days), then prune child work/evidence/audit refs/transitions in cascade
  order.
- Transition records: proposed 90 days for replay/audit, or the repository
  audit-retention period if longer.
- No raw prompts, model messages, tool arguments, results, passwords, bearer
  tokens, API keys, DSNs, MCP credentials, cookies or source configuration
  blobs.
- Evidence queries return IDs, trust class, producer, hashes and timestamps;
  raw observations remain in their trusted source/tool systems.

## Exact approvals still required

1. **Persistence phase:** approve or reject the `AgentGoalRuns` model, new
   migration, schema `1.18` (or an approved alternative), writer-generation
   change, CAS/lock/transaction contract and retention.
2. **Resume phase:** approve or reject the claim/lease, event or bounded-poll
   trigger, current-authority refresh, cancellation/shutdown and recovery
   engine.
3. **Public contract phase:** approve or reject any new public YAML/JSON
   configuration field, event type, endpoint, example or user contract.

None of these is approved by this document.
