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

- state vocabulary: `pkg/agent/goal_async_state.go` lines 108-143;
- transition vocabulary and targets: lines 146-197;
- table and lookup: lines 199-241;
- terminal predicate: lines 134-143.

## Checkpoint model

`goalAsyncCheckpoint` binds:

- checkpoint/contract version and monotonic revision;
- run, trace, correlation, agent, owner and goal IDs;
- frozen goal-spec hash and policy hash;
- a typed principal reference (user or system), never a token or tenant field;
- sorted allowed tools and source IDs;
- lifecycle state, slice count, UTC timestamps and absolute deadline;
- immutable ceilings and cumulative used counters;
- typed work references, bounded evidence references and audit references;
- a verifier reference only for `criteria_met`, including the bounded verifier
  evidence IDs;
- last transition identity/hash for replay detection;
- ledger ID and run-nonce hash for evidence binding.

The type is unexported. It has no prompt, message, argument, result payload,
token, password, DSN, MCP credential or source configuration field.

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

## Work references

The work model distinguishes:

- `crawl_proposal` / `awaiting_approval`: a durable `CrawlProposals.RequestID`
  and proposal hash, with no operation or approval ID;
- `crawl_operation` / `scheduled` or `processing`: a durable `Status`
  operation key (`OperationID`), proposal identity, approval ID/hash and source;
- `crawl_operation` / `completed`: the same operation identity plus an
  `ObservedAt` completion observation.

A proposal awaiting approval cannot be represented as a scheduled operation.
An issued approval alone cannot create a work reference without the operation
identity. A scheduled operation cannot enter `criteria_met`. A completed
operation can be recorded only through the resume path after a current-authority
grant.

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

The deadline is one absolute UTC timestamp. It cannot be changed by a
transition. `start`, `resume`, `await_*`, `criteria_met` and `incomplete` are
rejected at or after the deadline. `cancelled`, `budget_exhausted`, `denied` and
`failed` may be recorded at or after it. `budget_exhausted` is accepted only
when a ceiling is reached or the deadline passed. `incomplete` is rejected
when a ceiling is reached.

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

This is a pure model of compare-and-swap. It does not claim database
concurrency safety. The persistence proposal requires a transactional
`UPDATE ... WHERE revision = expected` guard.

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
