# Async Goal State Machine

## Status

Internal design and implementation only. The kernel is not wired into
production execution, API, Events, scheduler, crawler, MCP, a worker or a
database. It is a pure-Go component in `pkg/agent/goal_async_state.go`.

## States

| State | Meaning | Terminal |
| --- | --- | --- |
| `created` | Admitted logical goal run; no execution slice started | No |
| `running` | One authorized bounded execution slice is in progress | No |
| `awaiting_approval` | A real operator decision is outstanding | No |
| `awaiting_external_work` | An approved operation is durably scheduled/processing and the goal awaits work evidence | No |
| `criteria_met` | The deterministic verifier proved the acceptance criteria | Yes |
| `incomplete` | No further work is eligible and criteria were not met | Yes |
| `budget_exhausted` | A total-life ceiling or the fixed deadline was reached | Yes |
| `cancelled` | Explicitly cancelled or ended by the applicable deadline | Yes |
| `denied` | Current authority denied the work | Yes |
| `failed` | Unrecoverable operational or verification failure | Yes |

The vocabulary is deliberately separate from `GoalTerminalStatus`. It does not
change the synchronous `AgentGoalRun` result contract.

## Closed transition table

| From | Transition | To |
| --- | --- | --- |
| `created` | `start` | `running` |
| `created` | `cancelled`, `denied`, `budget_exhausted`, `failed` | terminal |
| `running` | `await_approval` | `awaiting_approval` |
| `running` | `await_external_work` | `awaiting_external_work` |
| `running` | `criteria_met` | `criteria_met` |
| `running` | `incomplete`, `budget_exhausted`, `cancelled`, `denied`, `failed` | terminal |
| `awaiting_approval` | `await_external_work` | `awaiting_external_work` |
| `awaiting_approval` | `cancelled`, `denied`, `budget_exhausted`, `failed` | terminal |
| `awaiting_external_work` | `resume` | `running` |
| `awaiting_external_work` | `cancelled`, `denied`, `budget_exhausted`, `failed` | terminal |
| any terminal | none | rejected |

No self-transition is accepted. Unknown states and unknown transition kinds
fail with `ErrGoalAsyncTransitionInvalid` or
`ErrGoalAsyncCheckpointInvalid`. A terminal state has no outgoing transition.

Implementation:

- state vocabulary: `pkg/agent/goal_async_state.go` lines 119-154;
- transition vocabulary and targets: lines 157-208;
- table and lookup: lines 210-252;
- terminal predicate: lines 145-154.

## Checkpoint model

`goalAsyncCheckpoint` binds:

- checkpoint version 2 and contract `goal-async-v2`, plus monotonic revision;
- run, trace, correlation, agent, owner and goal IDs;
- frozen goal-spec hash and policy hash;
- a typed principal reference (user or system), never a token or tenant field;
- sorted allowed tools and source IDs;
- lifecycle state, slice count, UTC timestamps and absolute deadline;
- immutable ceilings and cumulative used counters;
- typed work references carrying proposal, approval, submission and accepted
  timestamps; bounded evidence references and audit references;
- a verifier reference only for `criteria_met`, including the bounded verifier
  evidence IDs;
- last transition identity/hash for replay detection;
- ledger ID and run-nonce hash for evidence binding.

The type is unexported. It has no prompt, message, argument, result payload,
token, password, DSN, MCP credential or source configuration field.

### Canonical wire v2

The serialized v2 checkpoint is strict. `parseGoalAsyncCheckpoint` rejects
unknown fields, duplicate object keys at any depth, trailing data, invalid
UTF-8, non-UTC or non-microsecond timestamps, unsorted or duplicate identity
lists, duplicate/zero IDs, inconsistent verification evidence, overlong
arrays/strings and oversized input. It does **not** normalize a persisted
snapshot on read. Normalization is reserved for newly constructed trusted
snapshots and transitions.

Required fields must be present and non-null; a missing or null required value
fails validation. Optional timestamp fields use explicit zero values, and the
writer never silently substitutes a different value. `marshalGoalAsyncCheckpoint`
and `goalAsyncCheckpointDigest` operate on the same validated canonical bytes. The digest is an unkeyed integrity/audit
reference, not authentication.

## Identity and authority

### User principal

The user principal carries a numeric user ID, username and issuer. The
username must equal the agent owner, matching the existing crawl authorization
rule. No tenant ID exists in the current goal runtime, so no tenant field is
invented. A future tenancy model requires a separate decision.

### System principal

A principal-less background event run is represented as `principal_kind =
system` with an empty user ID, username and issuer. It still carries the agent
owner and a verified goal/agent/policy binding. This is a reference for audit,
not a grant.

### Frozen scope

`allowed_tools` and `allowed_sources` are sorted copies of the frozen
`AgentGoalSpec`. They are checkpoint data, not current authority. The future
resume engine must re-read the live agent, account, source ownership and policy
state. A stored checkpoint cannot expand or create authority. The checkpoint
deliberately does not store or bind `AgentGoalSpec.Auth`; the current trusted
configuration supplies any credential at resume, and no token enters the
checkpoint.

## Logical lifetime and slice budget

The whole logical goal lifetime is separate from one synchronous model/tool
slice:

- `goalAsyncLimits.LogicalLifetime` is the trusted internal whole-goal limit.
  The provisional default is 15 minutes and the provisional internal maximum is
  24 hours. Both are internal decisions; deployed/public TTL policy requires a
  separate approval.
- `goalAsyncCeilings.MaxSliceElapsed` is the frozen `GoalBudgets.MaxElapsed`
  slice ceiling, bounded by the existing 30-second default and 2-minute hard
  limit.
- `goalAsyncSliceDeadline` returns `min(now + MaxSliceElapsed, Deadline)` and
  refuses a slice at or after the logical deadline.
- Waiting consumes wall-clock time but does not refresh counters or extend the
  logical deadline.
- Active state advancement is refused at or after the logical deadline; a
  post-deadline success is impossible.

The effective logical lifetime is `Deadline - CreatedAt`. The absolute
`CreatedAt` and `Deadline` are immutable across revisions. `Auth` is not stored;
the trusted resume configuration supplies the current credential.

## Source scope

The async contract is source-scoped only. `AllowedSources` must contain at least one
positive source ID, and work/evidence source IDs must be within that frozen
scope. Source-neutral external-resource goals are not silently authorized by
this kernel; they require a separate design and approval.

## Work references

The work model distinguishes:

- `crawl_proposal` / `awaiting_approval`: a durable `CrawlProposals.RequestID`,
  proposal hash, source and proposal expiry, with no approval, operation,
  submit-by, accepted-at or completion field;
- `crawl_operation` / `scheduled` or `processing`: a durable `Status`
  operation key (`OperationID`), proposal identity, approval ID/hash, source,
  proposal expiry, approval expiry, submit-by expiry and `AcceptedAt`;
- `crawl_operation` / `completed`: the same accepted operation identity plus an
  `ObservedAt` completion observation.

Proposal expiry and approval expiry bound **submission**. They do not bound
later operation completion. `AcceptedAt` must be before the submit-by and
approval expiry; `ObservedAt` must be at or after `AcceptedAt` and at or before
the checkpoint update, but may be after proposal/approval expiry. The logical
goal deadline still bounds every successful transition.

A proposal awaiting approval cannot be represented as a scheduled operation.
An issued approval alone cannot create a work reference without the accepted
operation identity. A scheduled operation cannot enter `criteria_met`. A
completed operation can be recorded only through the resume path after a
current-authority grant. The resume grant does not require the historic
approval to remain unexpired; any new privileged action still requires fresh
live authorization.

## Scheduling admission

`running -> awaiting_external_work` and
`awaiting_approval -> awaiting_external_work` require a
`goalAsyncSchedulingPrerequisite`, not a bare work reference. The prerequisite
is unexported and non-serializable and is constructed by a trusted caller from
a `goalAsyncSchedulingObservation` fixture. It binds:

- exact run, goal, goal-spec hash, agent, owner and principal;
- source, request, proposal hash, approval ID/hash and durable operation ID;
- operation state (`scheduled`/`processing`), proposal/approval/submit-by
  expiries and `AcceptedAt`;
- the scheduling observation time.

The constructor validates the fixture; it does **not** claim to have queried
PostgreSQL or SQLite. A future trusted adapter must construct it only after
transactional reads of `CrawlProposals`, `CrawlApprovals` and `Status`. A
proposal `approval_required` result, model text, MCP result body, caller
boolean or source-level `crawl_completed` notification is not scheduling
authority.

For `awaiting_approval`, the prerequisite must match the stored proposal
exactly. For `running` without a prior proposal, the observation still binds
the accepted operation to the run, agent, owner, principal, source, proposal
and approval; a durable-looking operation ID alone is insufficient.

Duplicate request, operation, approval or exact work identities are rejected.
Contradictory source/run/proposal/approval bindings are rejected. Work IDs are
bounded to 16 and the configured scheduled-work ceiling.

## Completion proof

`criteria_met` requires a `goalAsyncVerifierDecision` produced by
`newGoalAsyncVerifierDecision`, which:

1. accepts `GoalEvalInput`;
2. verifies the spec/run/goal/spec-hash/ledger/nonce/agent/owner binding
   against the checkpoint;
3. requires the evaluation time to be at or after the checkpoint update and
   before the fixed deadline;
4. calls the existing `EvaluateGoal`;
5. requires `EvaluationResult.State == "met"`;
6. computes a bounded digest over criterion IDs, results, reason codes and
   evidence IDs, without observed values or raw evidence payloads;
7. records the evidence IDs used by the verifier.

The transition additionally requires every verifier evidence ID to be present
in the checkpoint's bounded evidence refs. A model final text, crawl scheduling
acknowledgment, proposal `approval_required` result or caller boolean has no
path into this decision. The verifier decision is unexported and
non-serializable.

A stored `criteria_met` checkpoint is data about a prior verification. It is
not a reusable verifier decision and cannot transition back to `running` or
authorize new work.

## Budgets and deadline

Ceilings are immutable after construction:

- slices;
- model rounds;
- tool attempts;
- result bytes;
- evidence records;
- evidence bytes;
- retries;
- scheduled-work requests;
- consecutive denials.

Used counters are non-negative and bounded. Cumulative counters are monotonic
across revisions; consecutive denials are a streak that may reset to zero but
may never exceed its ceiling. A transition cannot reset a cumulative counter,
exceed a ceiling or shrink a cumulative counter. A new slice increments only
the slice count; the caller supplies the other counters.

The logical deadline is one absolute UTC timestamp. It cannot be changed by a
transition. `start`, `resume`, `await_*`, `criteria_met` and `incomplete` are
rejected at or after the deadline. `cancelled`, `budget_exhausted`, `denied` and
`failed` may be recorded at or after it. `budget_exhausted` is accepted only
when a ceiling is reached or the deadline passed. `incomplete` is rejected
when a ceiling is reached. The synchronous `AgentGoalRun` keeps its own
30-second/2-minute slice path and is not changed by the logical lifetime.

## Replay and revision

Each checkpoint has a monotonic `revision`. A transition request has an
operation ID, expected revision and canonical request hash. The kernel:

- applies a new operation only when `expected_revision` matches;
- increments revision by one;
- stores the last operation ID and request hash;
- returns `replayed` and an unchanged copy for an exact same-operation replay;
- returns `ErrGoalAsyncReplayConflict` for the same operation ID with different
  inputs;
- returns `ErrGoalAsyncRevisionConflict` for a new operation on a stale
  revision.

This is a pure model of compare-and-swap for the **last** transition. It does
not claim database concurrency safety. The persistence contract requires a
transactional `UPDATE ... WHERE revision = expected` guard and a complete
unique `(goal_run_id, operation_id)` transition ledger so a replay of any
older stored operation cannot duplicate work after later revisions.

## Deadline, cancellation and terminal behavior

- Cancellation is terminal and takes precedence over future active work.
- A cancelled or otherwise terminal checkpoint never returns to an active
  state.
- A post-deadline completion is rejected.
- A clock that moves backwards relative to the checkpoint update is rejected.
- Snapshot inputs and outputs are deep-copied; the previous checkpoint is never
  mutated, including on error.

## Deliberately not wired

No production action, registry entry, API/Events route, scheduler, crawler,
MCP manager, worker, timer, poll loop, lease or database store imports this
kernel. The source-import test verifies the kernel imports only standard
library packages. A future persistence/resume decision must add the durable
adapter and resume engine.
