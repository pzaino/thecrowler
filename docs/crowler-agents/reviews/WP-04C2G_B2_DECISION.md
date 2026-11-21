# WP-04C2G B2 Decision

## Status

**APPROVED — B2 only.**

## Approval record

- Approver: repository maintainer (user) in the OpenCode session.
- Date: 2026-11-13 (UTC).
- Exact instruction: `B1 patch is applied, reviewed and approved. So proceed
  with B2 ...`
- Baseline HEAD: `3946fda8510f7bfac2f3fafacfd1c6d40e543ea7` (B1 applied).
- Scope: trusted authenticated user-principal provenance for user-triggered
  agent runs, plus fail-closed background/event behavior.

The approval message did not include a personal name. It is recorded as the
repository maintainer's explicit instruction to proceed with B2.

## Approved implementation

- Resolve a fresh active identity from the real authentication boundary using
  `auth.ResolveActiveIdentity`.
- Derive `MCPMutationPrincipal` only from the resolved account subject,
  username and issuer.
- Add an authenticated manual agent-run entrypoint limited to agents with a
  `manual` trigger and the `events:*` operator scope.
- Thread the typed principal through `AgentExecutionContext`,
  `ToolLoopConfig.Principal`, `goalRunConfig.Principal` and
  `ToolRuntime.Principal` without reading it from manifests, events, model
  output, tool arguments or inherited step values.
- Keep event/background agent runs principal-less and deny explicitly requested
  MCP mutation aliases without a trusted principal.
- Preserve schema release 1.17. No schema change is required for B2.
- Build and test the CROWler containers live.

## Not approved by B2

B3 independent MCP operation authority and production proposal-tool
registration, B4 approval-time policy revalidation, B5 denied-admission audit,
G3 schema deployment/SQLite FK changes, dispatch, remote mutation and any
external execution remain pending or prohibited.

Production proposal registration stays disabled. Event/background runs remain
fail-closed for MCP mutation proposals.
