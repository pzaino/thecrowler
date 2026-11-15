# Async Goal Security Review

## Scope

This review covers the new pure-Go files:

- `pkg/agent/goal_async_state.go`
- `pkg/agent/goal_async_state_test.go`

No existing production file was modified. No database, network, crawler,
model, MCP, scheduler or API/Events code imports the new kernel.

## Authority and trust boundaries

### Model text is not authority

The checkpoint has no model-text, message, prompt, tool-argument, result or
`criteria_met` boolean field. Completion requires a
`goalAsyncVerifierDecision` produced by `newGoalAsyncVerifierDecision`, which
calls the existing `EvaluateGoal`. A model's final text, a tool result, a
proposal's `approval_required` value or a caller-supplied boolean has no path
into that decision.

The verifier decision is unexported and non-serializable. It binds:

- run, trace, goal, goal-spec hash, ledger and run-nonce hash;
- agent ID and owner;
- evaluation digest;
- verification time;
- the bounded evidence IDs used by the evaluator.

The transition requires every verifier evidence ID to be present in the
checkpoint's bounded evidence refs, and the stored verification reference keeps
those IDs for a self-auditing `criteria_met` checkpoint. A decision from another
run, goal, spec, ledger, agent or owner is rejected.

### Approval is not execution

Work references are typed data:

- a crawl proposal awaiting approval cannot claim an operation or approval ID;
- an operation requires an approval ID/hash and an operation ID;
- a completed operation requires an observation timestamp;
- a scheduled operation cannot enter `criteria_met` directly;
- a completed observation cannot be recorded without the resume grant.

The kernel does not call `AuthorizeCrawlRequest`, does not schedule work and
does not treat an approval record as a current grant.

### Stored data is not current authority

The checkpoint's `allowed_tools`, `allowed_sources`, `policy_sha256`,
`goal_spec_hash` and principal are immutable references. The resume grant is
constructed only from a `goalAsyncCurrentAuthority` snapshot supplied by a
trusted future adapter. It requires:

- exact agent and owner match;
- exact principal match;
- exact policy hash, tool set and source set match;
- every original source still verified;
- agent enabled;
- the work operation's approval ID/hash match and approval valid;
- a check time after the checkpoint update and before the deadline.

A stored checkpoint cannot expand or recreate authority. A future resume
engine must perform live account, source ownership, agent policy and approval
reads; the kernel only models the decision boundary.

## Replay and TOCTOU

- Every checkpoint has a monotonic revision.
- A transition requires an expected revision.
- A new operation increments revision by one.
- The same operation ID with the same canonical request hash returns
  `replayed` and an unchanged copy.
- The same operation ID with different inputs is a replay conflict.
- A new operation on a stale revision is a revision conflict.
- The request hash excludes the caller's clock and expected revision, so an
  exact operation replay is deterministic; it includes the operation ID,
  transition kind, reason, counters, work, evidence, audit refs, verifier
  digest and resume-grant digest.
- The kernel deep-copies the previous checkpoint and request before applying.
  A caller mutating a returned slice cannot mutate the prior snapshot.

This is a pure model, not database atomicity. The persistence proposal requires
a transactional `UPDATE ... WHERE revision = expected` and a unique
`(goal_run_id, operation_id)` transition record. Until the persistence phase is approved and
implemented, no cross-process replay protection exists.

## Deadline and budget safety

- One absolute UTC deadline, immutable across revisions.
- Active, awaiting and completion transitions are rejected at or after the
  deadline.
- A post-deadline success is impossible.
- Cumulative used counters are non-negative, monotonic and bounded by immutable
  ceilings; the consecutive-denial streak may reset but cannot exceed its
  ceiling.
- A new slice increments only the slice count; the caller supplies cumulative
  counters.
- A counter reset or regression is rejected.
- `budget_exhausted` requires a reached ceiling or the passed deadline.
- `incomplete` is rejected when a ceiling is reached.
- Cancellation is terminal and cannot return to active work.
- Clock movement backwards relative to the checkpoint update is rejected.

## Evidence integrity

- Evidence refs carry only IDs, kind, trust class, producer, source IDs,
  timestamps, a bounded payload size and SHA-256 hashes.
- No raw payload, observed value, prompt, model message, tool argument or
  result is stored.
- Trust classes are limited to `system_verified` and `tool_observation`.
- Evidence source IDs must be within the frozen source scope.
- Evidence IDs and record hashes are unique per checkpoint.
- The verifier decision’s evaluation digest covers criterion IDs, results,
  reason codes and evidence IDs, not observed values.
- The checkpoint serialization test places secrets in `AgentGoalSpec.Auth`,
  `Prompt` and `Messages` and confirms they never appear in the encoded
  checkpoint. `Auth` is deliberately not bound; the trusted resume
  configuration supplies the current credential.

## Serialization

- Stable v1 JSON with explicit field names.
- Strict decoder with `DisallowUnknownFields`.
- Unknown fields, malformed data, trailing data and oversized input are
  rejected with a generic serialization error that does not echo attacker
  content.
- Checkpoint version and contract version are exact.
- Timestamps are normalized to UTC microseconds.
- Slices are sorted and bounded; work/evidence/audit counts are bounded.
- A SHA-256 checkpoint digest with an explicit domain separator is provided
  for future store comparison. It is an unkeyed integrity fingerprint, **not**
  authentication. Only a trusted store and transactional CAS provide trust.

## No accidental production dispatch

- The kernel imports only standard-library packages.
- A source-import test rejects imports from `pkg/database`, `pkg/crawler`,
  `services/`, `net/http`, `os/exec` or any external module.
- No production registry, action rule, tool alias, API/Events route, config
  flag or worker imports the kernel.
- The kernel has no DB, network, model, MCP, crawler, timer, retry or
  reconciliation dependency.
- Existing `AgentGoalRun`, `AgentToolLoop` and `AIInteraction` files are
  unchanged.

## Residual risks

- **Trusted package code can forge prerequisites.** The verifier and resume
  types are unexported and non-serializable, but code inside `pkg/agent` can
  construct them directly. The boundary is package-internal code review, not a
  cryptographic signature. A future exported adapter must not expose raw
  construction.
- **In-memory metadata is not durable.** The checkpoint model does not prove
  crash recovery, database atomicity, event correlation or resume security.
- **Hashes are not authentication.** The checkpoint digest, evidence hashes
  and IDs are integrity/audit references. A trusted store must enforce CAS and
  immutability.
- **No live authorization adapter.** Resume grant inputs are supplied by a
  trusted caller; the kernel does not read the database.
- **No event correlation.** `crawl_completed` events do not carry goal/request/
  operation IDs and are not trusted for specific completion.
- **No claim/lease.** Two future workers could attempt the same slice unless
  the resume phase adds a claim mechanism. Scheduler idempotency is not a substitute.
- **Source ownership out-of-band writes.** Direct database administrator writes
  can bypass the source lock protocol, as documented in the existing
  lock-order review.
- **Frozen process snapshots.** Agent and policy snapshots are per process;
  agent uploads may require an API restart.
- **Record-only approval.** Approval binds configured transport identity, not a
  fully verified live execution profile.
- **No tenant ID.** The current runtime has no first-class tenant reference;
  the checkpoint deliberately omits one. A future tenancy model needs a
  separate decision.
- **None of the later persistence, resume and public-contract phases is approved.** The persistence document is a
  proposal, not a migration or authorization.

## Conclusion

The kernel is a pure, bounded, fail-closed state machine with no production
side effects. It establishes the authority and completion invariants that a
future durable store and resume engine must preserve. It does not itself
provide durable asynchronous execution.
