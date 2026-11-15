# Async Goal B2A Security Review

## Scope

The review covers the v2 pure-Go checkpoint kernel and its tests:

- `pkg/agent/goal_async_state.go`
- `pkg/agent/goal_async_state_test.go`

No production database, scheduler, crawler, model, MCP, API or Events path
imports the kernel.

## Trust boundaries

### Model text and tool results

The checkpoint has no prompt, model message, tool argument, result body,
`approval_required` flag or caller completion boolean. Completion still requires
the unexported `goalAsyncVerifierDecision` produced from the existing
`EvaluateGoal`, and every verifier evidence ID must be present in the bounded
checkpoint evidence refs.

### Scheduling admission

A work reference alone is no longer scheduling authority. The unexported,
non-serializable `goalAsyncSchedulingPrerequisite` must bind the exact run,
goal, goal-spec hash, agent, owner, principal, source, request, proposal hash,
approval ID/hash, accepted operation ID, operation state, submission validity
windows and accepted-at observation. It is constructed from a trusted
`goalAsyncSchedulingObservation` fixture and validated again at transition
time.

The pure constructor cannot query PostgreSQL or SQLite. A future adapter must
build it only after transactional reads of `CrawlProposals`,
`CrawlApprovals` and `Status`. A source-level `crawl_completed` event, proposal
`approval_required` result, model text or MCP body cannot create it.

### Submission versus completion

Proposal, approval and request expiries bound submission. `AcceptedAt` records
the durable operation creation and must precede those expiries. `ObservedAt`
records later completion and must be at or after `AcceptedAt` and at or before
the checkpoint update, but may be after proposal/approval expiry. The logical
goal deadline still forbids a post-deadline success.

This prevents two opposite errors:

- accepting a fresh operation under an expired approval;
- rejecting a legitimately accepted operation merely because its approval
  expired before the crawl completed.

### Current authority at resume

The resume grant no longer treats an expired approval as a reason to refuse
reading completion evidence. It still requires exact current agent, owner,
principal, policy, tool and source-scope binding and verified source ownership.
A new privileged action must perform a fresh live authorization and approval
check; the stored checkpoint is not a grant.

## Serialized-data safety

- Size is bounded before decoding.
- Invalid UTF-8 is rejected.
- Duplicate object keys at any depth are rejected before struct decoding.
- Unknown fields are rejected with `DisallowUnknownFields`.
- Trailing values and oversized input are rejected.
- The read boundary does not normalize, sort, deduplicate or discard data.
- Unknown checkpoint/contract versions fail closed.
- Timestamps must be UTC microsecond values; malformed timestamps fail.
- IDs, source lists, work/evidence/verifier/audit arrays are bounded and
  duplicate/zero entries fail.
- Diagnostics are generic field/category errors; raw bytes, credentials, source
  configuration and free-form content are not printed.

The canonical digest is unkeyed. It is an integrity/audit reference, not
authentication. A trusted store and transactional CAS remain the authority
boundary.

## Logical lifetime

The whole-goal lifetime is separate from the synchronous slice timeout:

- provisional internal default 15 minutes, maximum 24 hours;
- slice ceiling remains the existing 1-120 second `GoalBudgets.MaxElapsed`;
- `goalAsyncSliceDeadline` caps each slice at `min(now + slice ceiling,
  logical deadline)`;
- the logical deadline is immutable and never refreshed by waiting;
- negative, sub-second, oversized, backwards-clock and deadline-extension
  inputs fail closed.

The 15-minute/24-hour values are internal provisional limits, not public
execution authority. Deployed TTL policy requires a separate decision.

## Residual risks

- Package-internal code can construct scheduling/verifier/resume prerequisites
  directly. The boundary is code review, not a cryptographic signature.
- The pure constructor does not prove that the scheduling fixture came from
  PostgreSQL/SQLite; the future adapter must enforce that.
- No durable transition ledger, claim, lease, worker or crash recovery exists.
- Hashes and IDs are not authentication.
- No tenant ID exists in the current runtime.
- Source ownership can be changed out of band by a database administrator,
  bypassing application locks.
- Agent/policy snapshots are process-frozen; new uploads may require a restart.
- Record-only approval binds configured transport identity, not a fully
  verified live execution profile.
- The async contract is source-scoped; source-neutral external-resource goals
  are not authorized.

## Conclusion

The v2 kernel fixes the pre-persistence lifetime, parsing, scheduling-authority
and expiry-boundary defects without adding persistence, production resume,
public contracts or external execution. The remaining risks are documented
and belong to the separately gated persistence/resume phases.
