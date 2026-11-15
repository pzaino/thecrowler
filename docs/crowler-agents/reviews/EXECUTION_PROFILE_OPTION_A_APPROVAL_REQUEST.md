# Execution-Profile Option A Approval Request

## Status

**Request only. Not approved.**

This is the single current request for the Option A read-only
execution-profile simulator. It supersedes earlier simulator approval requests
so there is one decision surface rather than competing proposals.

## Exact requested authorization

> APPROVE WP-04C2N Option A only: implement a local/test-development read-only
> MCP execution-profile validation simulator. It may evaluate trusted snapshots,
> durable proposal and approval records through authorized read-only seams,
> deterministic hypothetical live-schema and transport inputs, and a versioned
> canonical profile digest. It may emit bounded simulated validation/refusal
> outcomes. It must not insert `MCPMutationDispatchAttempts`, modify mutation
> proposals or approvals, claim leases, create a dispatcher or worker,
> retry/reconcile, contact remote mutating MCP tools, use production credentials,
> create new public endpoints, or alter schema 1.17. Keep execution backend
> `none`. Option B is not approved.

## Required approval record

- Approver identity:
- UTC approval date:
- Contract version: `WP-04C2N/Option-A-1`
- Scope: the exact text above
- External execution: explicitly not approved

A chat instruction, a new engineering task or an inference from prior work is
not an approval. The decision must be tracked and attributed.

## Proposed implementation boundary

- Pure Go profile/evaluator in a test/dev-only package or build-tagged path.
- No production registration, action rule, tool alias, HTTP/Events route or
  configuration flag.
- No DML, dispatch table writes, mutation-state transitions, leases, workers,
  retries or reconciliation.
- No network client to a mutating MCP endpoint.
- Immutable fixtures or explicitly approved read-only adapters for proposal and
  approval records.
- Bounded canonical JSON, deterministic field order, explicit null handling and
  domain separator `crowler-mcp-execution-profile-v1`.
- Result vocabulary only:
  `would_refuse`, `validation_passed_for_simulation`,
  `simulated_uncertain_after_send`.
- No claim that simulated schema, transport or account evidence is live
  verified.

## Required tests after approval

- Digest determinism, field sensitivity and round-trip fixtures.
- All policy, agent, account, ownership, transport, schema and approval
  negative cases.
- Duplicate hypothetical submission and idempotency/reconciliation refusal.
- Simulated cancellation before send and timeout after hypothetical send.
- Concurrent evaluations under `go test -race`.
- Zero dispatch attempts, zero state changes, zero mutation calls and no false
  goal success.
- Secret/raw-argument leak scan of every artifact.

## Risk

Low if the implementation remains pure and read-only. The main risks are:

- accidentally registering simulator code in production execution;
- treating simulated live-schema or account data as verified;
- embedding raw arguments or secrets in profile/digest output;
- adding a durable profile field without separate schema approval.

## Explicit non-authorization

This request does not authorize a dispatcher, worker, lease claim, retry,
reconciliation, remote mutating MCP call, production credential, public
endpoint or schema change. External execution remains pending and unapproved.
