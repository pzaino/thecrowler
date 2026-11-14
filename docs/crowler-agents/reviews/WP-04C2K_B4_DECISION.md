# WP-04C2K B4 Decision

## Status

**B4 approval reconciled; fail-closed contract implemented.**

## Approval record

- Approver: repository maintainer (user) in the OpenCode session.
- Date recorded by the repository: 2026-11-14 (UTC).
- Source: `docs/crowler-agents/reviews/WP-04C2I_B5_DECISION.md`.
- Exact instruction:

> `OK, B4 applied, tested, reviewed and approved. Please continue with B5.`

- Baseline named by that decision: `7f4db7f6d684ec51ec9968880735f52d30b0b509`.
- Scope of the recorded decision: approval-time current-policy revalidation for
  MCP mutation proposals.

## What the approval authorizes

The approval authorizes the documented fail-closed B4 contract:

- an operator approval may be issued only when the proposal still matches the
  current trusted operator policy identity;
- `policy_id`, `version` and canonical SHA-256 of the normalized policy must all
  match;
- the exact operation must still exist and remain authorized, including server,
  raw tool, alias, transport, agent restrictions, capabilities, trust, source or
  resource scope, argument constraints, approval and audit requirements, and
  `execution_backend: none`;
- missing, disabled, removed, changed or unloadable policy state must deny new
  approvals;
- existing historical proposals and approvals remain observable and auditable.

## What the approval does not authorize

It does not authorize:

- remote mutating MCP `tools/call`;
- a dispatcher, execution worker, dispatch claims or automatic execution;
- retry or reconciliation machinery;
- a new execution backend;
- a schema version change or writer-generation change;
- a public operator-policy contract change;
- commits, pushes or pull requests.

None of those were introduced.

## Evidence reconciliation

The current checkout at `180bd5f9` contains no B4 source. The decision report
itself states that B4 was not present in the checkout examined for B5. Several
tracked reports (WP-04C2C, WP-04C2D, WP-04C2E) still describe approval-time
policy revalidation as pending.

WP-04C2K therefore treats the maintainer message as the authorization for the
B4 contract and implements that contract directly. It does not claim that a
prior B4 implementation was found, reviewed or reused.

## Implemented boundary

The authoritative approval service boundary is
`DatabaseMCPMutationApprovalStore.Issue` in
`pkg/agent/mcp_mutation_store.go`. The HTTP handler
`mcpMutationApproveHandler` delegates to it. A test seam
(`mcpMutationApprovalStoreFactory`) exists in `services/api/mcp_mutation.go`,
but production always uses the durable store.

The current-policy evaluator is
`EvaluateMCPMutationApproval` in
`pkg/agent/mcp_mutation_approval_policy.go`. It is called from `Issue` before
the database approval transaction is entered.

The API process freezes its policy and agent registry once at startup through
`initializeMCPAuthority` in `services/api/main.go`, using
`SetMCPOperatorConfigForApproval` in `pkg/agent/mcp_config.go`. That variant
validates policy and inventory structure but does not require stdio command
binaries, token files, CA files or working directories to exist in the API
container. The API never launches MCP servers; requiring those local files would
couple approval deployment to the Events execution container.

## Approval gaps not silently extended

The prior approval did not cover a production dispatcher, remote mutation
execution or policy hot reload. This work does not add them. Policy changes
require restarting both API and Events processes, and mismatched processes fail
closed at approval time.
