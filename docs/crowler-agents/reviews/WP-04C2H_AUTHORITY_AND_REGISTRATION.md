# WP-04C2H B3 Authority and Registration

## Independent runtime authority

`ToolAuthContext`, `ToolLoopConfig` and `goalRunConfig` now carry:

```go
MCPMutationAllowed   bool
MCPAllowedOperations map[string]bool
```

These are separate from native crawl:

```go
MutationAllowed   bool
AllowedOperations map[string]bool
```

`AuthorizeToolCall` recognizes the MCP proposal adapter through
`MCPMutationProposalMarker`. MCP proposal tools use only the MCP fields; native
crawl tools continue to use the native fields. Neither path can authorize the
other.

The frozen registry wrapper preserves the marker, so registered proposal tools
still take the MCP branch after admission.

## Exact registration

`registerMCPMutationProposalTools` is the production admission boundary. An
operation is registered only when all of the following hold:

- the global operator policy is enabled;
- the exact derived alias appears in the run allowlist;
- an authenticated `MCPMutationPrincipal` is valid;
- the agent has a non-empty owner audit label;
- the agent ID is listed in `allowed_agents`;
- every required capability is held;
- the agent trust level meets `min_trust_level`;
- the agent contract does not forbid the alias;
- the frozen descriptor exists and its server/tool identity matches the policy;
- the exact transport is approved;
- the policy source scope is within the effective inventory scope and the run
  source scope;
- a resource-scoped operation does not conflict with a source-scoped tool;
- the durable schema-1.17 MCP capability is available.

Registration creates one `MCPMutationProposalTool` per exact alias through
`NewMCPMutationProposalToolForOperation`. The legacy constructor now rejects
ambiguous multi-operation selection and directs callers to the exact
constructor.

No in-memory proposal store is used in production. A missing durable capability
fails registration; there is no fallback.

## Inert proposal behavior

`MCPMutationProposalTool.Execute` remains proposal-only. It delegates to
`MCPMutationProposer.Propose`, which:

- re-authorizes the call through the MCP authority fields;
- rechecks the exact policy operation and frozen descriptor;
- checks agent, capability, trust, contract, transport and argument bounds;
- checks source/resource scope and the authenticated principal;
- creates an immutable canonical fingerprint and policy snapshot;
- admits the row atomically through
  `DatabaseMCPMutationProposalStore.Admit`.

It never invokes the MCP transport. The response contains only
`approval_required`, proposal ID, proposal fingerprint, argument fingerprint,
server/tool alias and expiry. Raw arguments and credentials are not returned.

## Tool loop and goal integration

- `AgentToolLoop` registers proposal tools after read-only MCP tools and before
  execution. It sets the MCP authority fields from the exact registered alias
  set.
- `AgentGoalRun` performs the same registration before its allowlist validation
  and passes the MCP authority fields into `RunGoal`.
- Native crawl policy remains untouched. `MutationAllowed` is still set only
  from a native crawl runtime.

## Remaining gates

B4 current-policy revalidation at approval is not implemented. B5
denied-admission audit and G3 schema deployment/SQLite FK decisions remain
pending. Approval records are inert; no dispatcher or execution path exists.
