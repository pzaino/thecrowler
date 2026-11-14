# WP-04C2M Dispatch Approval Request

## Status

**Proposed future milestone. This is not approval and does not authorize any
implementation.**

## Requested decision

The maintainer is asked to choose one of:

### Option A — read-only validation simulator (recommended)

Build a simulator that:

- claims nothing;
- writes no dispatch attempt;
- makes no remote mutating call;
- evaluates the full execution profile, policy, agent, account, ownership,
  approval and expiry;
- reports exactly what a future executor would do and why it would refuse;
- exercises idempotency/uncertainty logic with a fake backend.

Risk: low. It proves profile/revalidation logic without side effects.

### Option B — one limited opt-in execution backend

Authorize one explicitly named backend for one test MCP server, with:

- no automatic execution;
- operator-triggered claim only;
- one execution per approval;
- no retry of uncertain outcomes;
- explicit tool idempotency/reconciliation contract;
- disposable test credentials;
- full audit and redaction;
- separate schema/design approval if needed.

Risk: medium/high; external side effects are possible.

## Recommended decision

**Option A first.** Do not approve Option B until the simulator proves:

- execution-profile identity and revalidation;
- claim/lease behavior;
- uncertain outcome handling;
- no replay of unsafe mutations;
- no goal success from proposal/approval state.

## Exact authorization requested for Option A

- Add read-only simulator code under test/dev boundaries.
- Do not write `MCPMutationDispatchAttempts`.
- Do not make remote mutating calls.
- Keep execution backend `none`.
- Keep schema 1.17.
- Do not add public API routes without a separate request.
- Produce tests and a security review before any execution backend.

## Exact authorization required for Option B

A later, separate request must name:

- backend and MCP server/tool;
- credential scope and secret handling;
- idempotency/reconciliation contract;
- exact dispatch-attempt state transitions;
- schema change (if any) and migration/rollback;
- operator enablement and kill switch;
- test evidence and incident procedure.

## Risk summary

| Risk | Option A | Option B |
| --- | --- | --- |
| Remote side effect | none | possible |
| Credential exposure | low | medium/high |
| Duplicate execution | simulated | possible without idempotency |
| Unknown outcome | simulated | uncertain state required |
| Schema change | none | possibly required |
| Operator impact | none | new runbook/kill switch |

## Non-authorization

Prior B1/B2/B3/B4/B5/G3 decisions do not authorize external dispatch. This
document is a request for a future decision and must not be treated as approval.
