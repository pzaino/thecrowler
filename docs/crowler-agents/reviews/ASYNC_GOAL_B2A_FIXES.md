# Async Goal B2A Fixes

## Scope

The work package changes:

- `pkg/agent/goal_async_state.go`
- `pkg/agent/goal_async_state_test.go`
- the existing state-machine, persistence-contract and maintainer-decision
  reports

and adds the B2A baseline, fixes, security, test and persistence-approval
reports.

No production wiring, schema, migration, worker, event route, public manifest
field or remote MCP path was added.

## Contract version

The internal checkpoint format is now intentionally:

```text
checkpoint_version = 2
contract_version  = goal-async-v2
digest domain     = crowler-goal-async-checkpoint-v2
transition domain = crowler-goal-async-transition-v2
verifier domain   = crowler-goal-async-verifier-v2
scheduling domain = crowler-goal-async-scheduling-v2
resume domain     = crowler-goal-async-resume-v2
```

No durable checkpoint was deployed, so there is no stored-data migration in
this package. The version bump avoids silent ambiguity after the work-reference
timestamp, slice-ceiling and strict-wire changes.

## 1. Logical lifetime versus synchronous slice ceiling

`newGoalAsyncCheckpoint` no longer derives the whole goal deadline from
`spec.Budgets.MaxElapsed`:

- `goalAsyncLimits.LogicalLifetime` is the trusted internal whole-goal limit.
- Provisional default: 15 minutes.
- Provisional internal maximum: 24 hours.
- `GoalBudgets.MaxElapsed` still becomes `ceilings.max_slice_elapsed_ns`, with
  its existing 30-second default and 2-minute hard ceiling.
- `goalAsyncSliceDeadline(cp, now)` returns
  `min(now + MaxSliceElapsed, Deadline)` and rejects a slice at or after the
  logical deadline.
- The logical `Deadline` and `CreatedAt` remain immutable; the effective
  lifetime is their difference.
- Waiting does not reset counters or extend the deadline.
- A caller-supplied deadline beyond `now + LogicalLifetime` is rejected.

The synchronous `AgentGoalRun` path is untouched and keeps its own 30-second /
2-minute slice behavior.

## 2. Strict v2 checkpoint decoding

`marshalGoalAsyncCheckpoint` and `parseGoalAsyncCheckpoint` now operate on a
validated canonical representation:

- `parse` no longer calls `normalizeGoalAsyncCheckpoint` on read.
- It first checks size and UTF-8 validity.
- It scans the JSON token stream and rejects duplicate object keys at any
  depth.
- It then uses `DisallowUnknownFields`, rejects trailing data and validates the
  v2 checkpoint without sorting, deduplicating or discarding anything.
- `marshal` validates before encoding and does not normalize malformed input.
- `goalAsyncCheckpointDigest` hashes the same canonical bytes with the v2
  domain separator; the digest remains an integrity/audit reference, not
  authentication.

Malformed `allowed_sources` (`[0,42,42]`), unsorted sources, duplicate tools,
duplicate evidence source IDs, duplicate verifier IDs, duplicate audit refs,
overlong arrays, illegal timestamps, unknown fields, unknown versions,
duplicate JSON keys, trailing data and oversized input are rejected.

## 3. Trusted scheduling admission

A new unexported, non-serializable `goalAsyncSchedulingPrerequisite` is built
from a `goalAsyncSchedulingObservation` fixture. It binds:

- exact run, goal, goal-spec hash, agent, owner and principal;
- source, request, proposal hash, approval ID/hash and durable operation ID;
- operation state (`scheduled` or `processing`);
- proposal expiry, approval expiry, submit-by expiry and `AcceptedAt`;
- the scheduling observation time;
- an internal prerequisite digest.

`running -> awaiting_external_work` and
`awaiting_approval -> awaiting_external_work` now require this prerequisite.
A bare `goalAsyncWorkRef` is rejected. For `awaiting_approval`, the observation
must match the stored proposal exactly. For `running` without a stored
proposal, the observation still binds the accepted operation to the run,
agent, owner, principal, source, proposal and approval; an operation ID alone
is insufficient.

The constructor validates a trusted fixture but does not query PostgreSQL or
SQLite. The future adapter must construct it only from transactional reads of
`CrawlProposals`, `CrawlApprovals` and `Status`. Model text, MCP result bodies,
caller booleans and source-level `crawl_completed` events are not scheduling
authority.

## 4. Submission expiry versus operation completion

The ambiguous `ExpiresAt` field was replaced by distinct facts:

- `proposal_expires_at`: proposal validity;
- `approval_expires_at`: approval validity at submission;
- `submit_by`: request submit-by validity;
- `accepted_at`: durable operation creation;
- `observed_at`: later completion observation;
- the checkpoint `deadline`: logical goal lifetime.

Validation now requires:

- proposal: proposal expiry only;
- operation scheduled/processing: proposal/approval/submit-by expiries and
  accepted-at; no observed-at;
- operation completed: the same plus observed-at;
- `accepted_at < submit_by`,
  `submit_by <= proposal_expires_at`,
  `submit_by <= approval_expires_at`,
  `accepted_at < proposal_expires_at`,
  `accepted_at < approval_expires_at`;
- `observed_at >= accepted_at` and `observed_at <= checkpoint.updated_at`.

There is deliberately no `observed_at < proposal_expires_at` or
`observed_at < approval_expires_at` requirement. A crawl accepted before
approval expiry may complete afterward, provided the goal's own logical
deadline has not passed.

The resume grant no longer requires the historic approval to remain
unexpired. It still revalidates current account/agent/owner/principal, policy,
tool/source scope and verified sources. Any new privileged action must perform
a fresh live approval check and obtain a new scheduling prerequisite.

## 5. Replay and persistence refinement

The pure kernel still recognizes exact replay of the **last** operation. The
updated persistence contract requires the future store to consult a complete
unique `(goal_run_id, operation_id)` transition ledger before applying a new
revision. A replay of any older stored operation must return the stored result
when the hash matches and a typed conflict when it differs; it must not be
applied as a new transition merely because the current revision matches.

## Source references

- checkpoint version/constants: `pkg/agent/goal_async_state.go` lines 38-55;
- work reference and expiry validation: lines 435-525;
- scheduling observation/prerequisite: lines 527-700;
- logical lifetime and slice ceiling: lines 791-900 and 1406-1430;
- completion/resume authority: lines 1624-1780;
- transition application: lines 1785-2030;
- strict duplicate-key scanner and parse: lines 2201-2276;
- digest: lines 2277-2285.

The exact line numbers move with later edits; the function names are the stable
reference.

## Compatibility

- No existing synchronous `AgentGoalRun`, `AgentToolLoop`, `AIInteraction`,
  goal evaluator, evidence, crawl authorization or database code was changed.
- No public schema or example manifest changed.
- No database migration or production store exists.
- No MCP mutation or external execution path was added.
