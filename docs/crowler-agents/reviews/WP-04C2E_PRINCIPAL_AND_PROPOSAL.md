# WP-04C2E Principal Provenance and Proposal Registration Status

## Gates B2 and B3: pending

No maintainer approval exists for trusted user-principal propagation (B2) or a
separate MCP proposal alias authority (B3). No production proposal registration
or principal plumbing was added.

A repository-wide search found no non-test use of
`NewMCPMutationProposalTool` or `AgentToolRegistry.RegisterMutation`. The only
registrations are test harnesses. The production inert proposal producer
remains disabled.

## Current dataflow

```text
authenticated API request / event trigger
        |
        v
agent runtime
        |
        +-- ToolLoopConfig.Identity  = AgentIdentity (manifest/operator label)
        +-- ToolLoopConfig.Principal = zero value in production constructors
        +-- goalRunConfig.Principal  = zero value in production constructors
        |
        v
ToolRuntime.Principal
        |
        v
MCPMutationProposer.Propose
        |
        +-- requires Principal.Validate() when RequireApproval is true
```

Verified source anchors at `5846ef53`:

- `pkg/agent/tool_loop.go:118` declares `ToolLoopConfig.Principal`;
  `tool_loop.go:512` copies it into `ToolRuntime.Principal`.
- `pkg/agent/action_goal_run.go:328` declares `goalRunConfig.Principal`;
  `action_goal_run.go:542` copies it into `ToolRuntime.Principal`.
- `pkg/agent/action_tool_loop.go:110` builds `ToolLoopConfig` from
  manifest/operator values and does not set `Principal`.
- `pkg/agent/action_goal_run.go:198` builds `goalRunConfig` and does not set
  `Principal`.
- `pkg/agent/tool_policy.go:358` defines `ToolRuntime.Principal`.

The goal runtime currently sets:

```go
AllowedOperations: spec.MutationPolicy.OperationsSet(),
MutationAllowed:   crawlRuntime != nil,
```

That is native crawl authority. It must not become MCP proposal authority.

## Trusted authentication primitives

`pkg/auth/auth.go` provides:

- `FromContext` (line 51) to read the middleware-resolved identity;
- `ResolveActiveIdentity` (line 81) to re-read `Users`, reject a missing,
  disabled or deleted account, parse a nonzero numeric subject, reload current
  roles/scopes and normalize the issuer;
- `Middleware` (line 205), which validates the bearer token and scopes before
  storing the identity in the request context.

Important fail-closed detail: when authentication is disabled,
`auth.Middleware` calls the next handler without an identity. A missing
identity must therefore deny MCP proposal creation; it is not an implicit
administrator or service principal.

`AgentIdentity.Owner` is a manifest/operator label. It is not an authenticated
principal. Event payload fields, model output, tool arguments, inherited step
configuration and retry metadata are all untrusted for this purpose.

## Required B2 mechanism

For user-triggered runs, at the real authenticated entrypoint that starts the
agent:

1. Read `auth.Identity` from the middleware context.
2. Call `auth.ResolveActiveIdentity` with current database state.
3. Require a nonzero numeric subject, current username and issuer.
4. Construct `MCPMutationPrincipal` explicitly.
5. Pass it as a typed value into `ToolLoopConfig.Principal` or
   `goalRunConfig.Principal`; never reconstruct it from serialized job data.
6. Recheck account state and source ownership at proposal admission, not only
   at initial authentication.
7. Keep cancellation, run deadline and concurrent-run binding intact.

Background/event runs remain denied until a separate, approved, unforgeable
service-principal contract exists. No administrator impersonation or automatic
role assignment is acceptable.

## Required B3 mechanism

MCP proposal authority must be a separate explicit set of MCP policy aliases:

- not `spec.MutationPolicy.OperationsSet()`;
- not the native `MutationAllowed` boolean;
- not `crawlRuntime != nil`;
- not a discovered tool effect or `allow_mutation_proposal` alone.

Registration should require all of: feature enabled, exact approved discovery
classification, exact server/tool/alias, frozen schema/transport identity,
agent identity/capability/trust, authenticated principal, source/resource
scope, explicit MCP alias allowlist, durable capability and budget/deadline
availability.

Each approved operation should be registered independently with unambiguous
policy and server binding. A failed database store must never fall back to the
in-memory test store. The proposal tool's `Execute` must only create an inert
durable proposal and return `approval_required`, proposal ID, fingerprint and
expiry. It must never call a mutating MCP `tools/call`.

## Tests that remain blocked

The following cannot be claimed until B1/B2/B3 are approved and implemented:

- real authenticated user propagation into the runtime;
- forged owner/event/model/run-ID rejection at the entrypoint;
- cross-tenant ownership transfer before proposal and approval;
- background-run denial with an explicitly requested mutation alias;
- an end-to-end durable proposal from a real authenticated run;
- operator approval remaining inert with zero mutating fixture calls and zero
  dispatch-attempt rows.

Existing unit tests already prove the proposal tool stays inert under explicit
test principals, and that source-scope mismatch is rejected. They do not prove
production provenance.

## Stopping point

No production proposal registration, principal fallback, service identity,
public config or dispatcher was added. The producer remains disabled until B1,
B2 and B3 are recorded as approved and the B4/B5 behavior is decided.
