# WP-04C2G B2 Principal Provenance

## Trusted dataflow

```text
POST /v1/agent/run
  (auth.Middleware -> auth.FromContext)
        |
        v
auth.ResolveActiveIdentity(current DB)
  - numeric subject required
  - current username reloaded
  - disabled/deleted account rejected
  - current roles/scopes reloaded
        |
        v
MCPMutationPrincipalFromAuthIdentity
  - user_id, username, issuer
        |
        v
WithMCPMutationPrincipal(iCfg)
  - typed MCPMutationPrincipal value only
        |
        v
ExecuteManualAgentResult
        |
        v
AgentExecutionContext.MCPMutationPrincipal
        |
        v
applyExecutionContext
  - deletes any forged runtime value
  - publishes the typed trusted value
        |
        +--> ToolLoopConfig.Principal -> ToolRuntime.Principal
        |
        +--> goalRunConfig.Principal -> ToolRuntime.Principal
```

No manifest field, event payload, model output, tool argument, `$response`,
persisted arbitrary job metadata or inherited step value is consulted for the
principal.

## Manual-run endpoint

`POST /v1/agent/run`:

- is wrapped by the Events service authentication middleware;
- requires `events:*` or `events:agent:run`;
- accepts exactly one of `agent_id` or `agent_name`;
- rejects unknown top-level JSON fields, including `principal_user_id`,
  `owner`, `user_id` and similar forged fields;
- accepts a bounded `input` value (object, string, array or other JSON value)
  and an optional `parameters` object;
- requires agent identity enforcement to be enabled;
- looks up a registered agent and requires at least one `manual` trigger;
- executes **only** the manual-trigger jobs, never event-triggered jobs;
- uses the initialized `agt.AgentsEngine` (the live stack exposed a stale
  local-global check, now fixed);
- returns the resolved principal user ID, username and issuer plus the agent's
  terminal result.

Controller errors return stable messages; detailed errors are logged
server-side and are not copied into the response.

## Event/background behavior

`agentInputConfig` does not add a principal. Event metadata cannot inject one.
`applyExecutionContext` deletes any pre-existing `mcp_mutation_principal` before
publishing the typed execution-context value.

`AgentToolLoop` and `AgentGoalRun` explicitly deny a requested MCP mutation
alias when the policy contains that alias and the runtime principal is zero.
This is fail-closed even before B3 registers any production proposal adapter.

## Lifecycle and concurrency

- The principal is bound to one `AgentExecutionContext`, which is created per
  agent invocation and shared with that run's job groups.
- Parallel groups receive the same trusted execution context by value.
- Event runs receive an empty principal.
- A failed request never creates a principal.
- The endpoint does not accept a run ID, principal ID, owner or issuer from the
  request.
- The agent's own budgets and deadlines still bound execution.

## Schema

No schema change was required. The endpoint resolves current account state and
grants through the existing schema-1.17 `Users`, `AuthRoles`, `AuthScopes`,
`UserRoles`, `RoleScopes` and `UserScopes` tables. No migration or new schema
release is introduced.

## Remaining boundaries

B3 production proposal registration, B4 approval-time policy revalidation, B5
denied-admission audit and G3 deployment/SQLite FK decisions remain pending.
The manual-run endpoint prepares trusted provenance; it does not authorize an
external mutation.
