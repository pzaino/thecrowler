# Execution-Profile Simulator Option A Gate

## Decision state

**BLOCKED — no Option A approval exists.**

The reviewed repository records the read-only execution-profile simulator as
not approved. No tracked decision contains all of:

- a named maintainer or approver identity;
- a UTC approval date;
- contract version `WP-04C2N/Option-A-1` or a clearly versioned amendment;
- the exact restricted simulator scope;
- explicit prohibition of remote calls, dispatch writes, state changes,
  workers, claims, leases, retries, reconciliation and schema changes;
- confirmation that external execution remains unapproved.

The existence of an engineering task, a design document or a prior approval
request is not approval.

## Consequence

No simulator source code, package, command, test, endpoint, database column,
migration or configuration flag was added. The implementation reports are
therefore intentionally absent.

The single current request is
`EXECUTION_PROFILE_OPTION_A_APPROVAL_REQUEST.md`. It proposes a
local/test-development, read-only evaluator that:

- consumes trusted typed snapshots and immutable fixtures;
- may use an explicitly approved read-only adapter for durable proposal and
  approval records;
- computes a versioned canonical execution-profile digest;
- emits only `would_refuse`, `validation_passed_for_simulation` or
  `simulated_uncertain_after_send`;
- never writes `MCPMutationDispatchAttempts`;
- never changes proposal, approval, quota or source state;
- never claims a lease, creates a worker, retries or reconciles;
- never contacts a remote mutating MCP tool;
- never uses production credentials or creates a public endpoint;
- keeps execution backend `none` and schema `1.17`.

## Required approval text

The exact scope is recorded in
`EXECUTION_PROFILE_OPTION_A_APPROVAL_REQUEST.md`. A new approval must be a
tracked, attributed decision, not a chat instruction or an inferred intention.

Minimum fields:

```text
Approver:
UTC date:
Contract version: WP-04C2N/Option-A-1
Scope: <exact bounded text>
External execution: not approved
```

## What proceeds without approval

The exact-ownership cleanup repair, fast workflow/cleanup CI, local isolated
E2E and documentation proceed independently. They do not require simulator
approval.

## What remains blocked

- `ExecutionProfileV1` implementation.
- Canonical profile digest implementation.
- Pure evaluator and reason codes.
- Simulator negative/positive fixtures.
- Read-only durable-record adapter.
- Race, cancellation and secret-exposure tests.
- Any GitHub CI for simulator code.

External execution remains separately unapproved. Nothing in this change
authorizes a dispatcher, worker, lease, retry, reconciliation, durable dispatch
attempt or remote mutation.
