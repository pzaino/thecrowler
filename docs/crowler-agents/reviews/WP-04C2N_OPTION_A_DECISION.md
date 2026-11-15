# WP-04C2N Option A Decision

## Decision state

**BLOCKED — no Option A approval exists.**

Reviewed source at `d5314ce` still records Option A as pending in
`WP-04C2M_SIMULATOR_GATE.md`. No tracked record contains:

- an approver identity;
- a UTC approval date;
- contract version `WP-04C2N/Option-A-1`;
- the exact restricted simulator scope;
- explicit prohibition of remote calls, dispatch writes, state changes,
  workers, claims, leases, retries/reconciliation and schema changes;
- confirmation that Option B remains unapproved.

The OpenCode task itself is not approval.

## Consequence

No simulator production/dev code, tests, package, command, endpoint or schema
change was added. The only simulator artifact in this work package is the focused
approval request:

`WP-04C2N_OPTION_A_APPROVAL_REQUEST.md`

## What can proceed

Phase A E2E workflow recovery, script tests and local isolated evidence proceed
independently of Option A.

## What remains blocked

- `ExecutionProfileV1` implementation.
- Canonical profile digest implementation.
- Pure evaluator.
- Simulator negative/positive test suite.
- Any read-only adapter to durable proposal/approval records.
- Any GitHub CI for simulator code.

Option B remains separately unapproved and must not be implemented.
