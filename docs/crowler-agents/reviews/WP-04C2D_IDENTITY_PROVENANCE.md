# WP-04C2D Trusted Principal Provenance

**STATUS: BLOCKED — NO PRODUCTION PROPOSAL REGISTRATION.** Gate B2 is not
approved. This document maps the current trust boundary and the required
implementation when the maintainer approves a principal mechanism.

## 1. Current dataflow

```text
authenticated API request / event trigger
        |
        v
agent action runtime (manifest/operator identity)
        |
        +-- ToolLoopConfig.Identity = AgentIdentity (manifest/operator)
        +-- ToolLoopConfig.Principal = zero value in production constructors
        +-- goalRunConfig.Principal = zero value in production constructors
        |
        v
ToolRuntime.Principal
        |
        v
MCPMutationProposer.Propose
        |
        +-- requires Principal.Validate() when RequireApproval is true
```

Relevant source:

- `pkg/agent/tool_loop.go:118` declares `ToolLoopConfig.Principal`.
- `pkg/agent/tool_loop.go:512` copies it into `ToolRuntime.Principal`.
- `pkg/agent/action_goal_run.go:328` declares `goalRunConfig.Principal`.
- `pkg/agent/action_goal_run.go:542` copies it into `ToolRuntime.Principal`.
- `pkg/agent/action_tool_loop.go:109` `normalizeToolLoopConfig` builds
  `ToolLoopConfig` from manifest/operator values but does not set `Principal`.
- `pkg/agent/action_goal_run.go:198` builds `goalRunConfig` but does not set
  `Principal`.
- `pkg/agent/mcp_mutation.go` proposal creation requires
  `runtime.Principal.Validate()` when approval is required.
- `pkg/agent/tool_policy.go:352` defines `ToolRuntime` with `Principal`.

No production constructor was found that maps an authenticated
`auth.Identity` to `MCPMutationPrincipal`. `AgentIdentity.Owner` is a
manifest/operator label and must never be promoted. Event payload fields are
data, not identity. Tests set `Principal` explicitly and therefore do not prove
production provenance.

## 2. Authentication primitives that do exist

- `auth.FromContext` obtains the authenticated token identity.
- `auth.ResolveActiveIdentity` reloads the local user, rejects missing,
  disabled or deleted accounts and reloads current roles/scopes.
- API handlers use these primitives for crawl approval and MCP mutation
  approval/observation.
- No analogous trusted service-principal contract was found for
  background/event-triggered agent runs.
- Current MCP mutation approval checks current account, source ownership and
  policy binding at the operator boundary; it does not create a proposal.

## 3. Required approved mechanism

### User-triggered runs

At the trusted API/event boundary that starts a user-initiated agent run:

1. Obtain `auth.Identity` from the authenticated middleware context.
2. Call `auth.ResolveActiveIdentity` and reject missing/disabled/deleted
   accounts.
3. Parse the numeric subject into a non-zero `MCPMutationPrincipal.UserID`.
4. Bind current username and issuer from the resolved identity.
5. Pass the resulting principal explicitly into the loop/goal configuration.
6. Keep the principal bound to the actual run and effective call; never
   re-derive it from model dataflow, inherited step state, event fields or a
   resume/retry payload.
7. Revalidate current `SourceOwnership` at proposal admission for source-scoped
   operations, and again at operator approval.
8. Apply context cancellation and the run deadline to MCP initialization,
   model calls and proposal admission.

### Background/event-triggered runs

Default **DENY** MCP proposal creation. No approved service-principal contract
exists. A future service identity must be:

- backed by existing `Users`/`AuthRoles`/`AuthScopes` or an equivalently
  unforgeable trusted store;
- explicitly configured by an operator, not inferred from an event/manifest;
- restricted to exact agents, tools, scopes and operation policies;
- current-account checked at proposal and approval time;
- auditable and non-impersonating.

Until such a contract is approved, background runs must not receive a
principal and must fail closed if the proposal tool is invoked.

## 4. Independence from native crawl authorization

Native `request_source_crawl` uses `CrawlRuntime` / native crawl policy.
MCP proposal authority requires:

- exact `MCPMutationPolicy` operation;
- authenticated `MCPMutationPrincipal`;
- current verified source ownership (or approved external-resource authority);
- explicit MCP alias in the run allowlist.

The generic `MutationAllowed` flag and native crawl operation set must not be
the source of MCP proposal authority. Production currently has no separate
MCP alias allowlist path; this is part of the blocked producer work.

## 5. Required tests when approved

- real authenticated user context propagated unchanged into
  `ToolLoopConfig.Principal`/`goalRunConfig.Principal`;
- missing/expired/disabled/deleted account denied;
- forged manifest `owner`, event `user_id`, issuer, roles, run_id and model
  arguments cannot become principals;
- cross-tenant source ownership mismatch at proposal and approval;
- ownership transfer between discovery and proposal, and between proposal and
  approval;
- admin versus ordinary operator;
- external-resource admin-only behavior;
- background trigger without an approved service identity denied;
- concurrent/resumed runs cannot inherit another run's principal;
- context cancellation/deadline prevents a late admission.

## 6. Gate

Gate B2 is not approved. No production registration, principal fallback,
service identity or public configuration was added. The producer remains
disabled.
