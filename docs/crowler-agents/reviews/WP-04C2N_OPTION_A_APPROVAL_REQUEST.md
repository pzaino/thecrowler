# WP-04C2N Option A Approval Request

## Status

**Request only. Not approved.**

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
- Option B status: explicitly pending/not approved

## Proposed implementation boundary

- Pure Go profile/evaluator in a test/dev-only package or build-tagged path.
- No production registration, action rule, tool alias, HTTP/Events route or
  config flag.
- No DML, dispatch table writes, mutation-state transitions, leases, workers,
  retries or reconciliation.
- No network client to a mutating MCP endpoint.
- Immutable fixtures or explicitly approved read-only adapters for proposal and
  approval records.
- Bounded canonical JSON, deterministic field order, explicit null handling,
  domain separator `crowler-mcp-execution-profile-v1`.
- Result vocabulary only:
  `would_refuse`, `validation_passed_for_simulation`,
  `simulated_uncertain_after_send`.
- No claim that simulated schema/transport/account evidence is live-verified.

## Required tests after approval

- Digest determinism and field sensitivity.
- All policy/agent/account/ownership/transport/schema/approval negative cases.
- Duplicate hypothetical submission and idempotency/reconciliation refusal.
- Simulated cancellation before send and timeout after hypothetical send.
- Concurrent evaluations.
- Zero dispatch attempts, zero state changes, zero mutation calls, no false goal
  success.
- Secret/raw-argument leak scan.

## Risk

Low if the implementation remains pure and read-only. The main risks are:

- accidentally registering simulator code in production execution;
- treating simulated live-schema/account data as verified;
- embedding raw arguments or secrets in profile/digest output;
- adding a durable profile field without separate schema approval.

## Explicit non-authorization

This request does not authorize a dispatcher, worker, lease claim, retry or
reconciliation, remote mutating MCP call, production credentials, public
endpoint or schema change. Option B remains pending.
