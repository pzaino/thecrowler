# WP-04C2G B2 Security Review

## Scope

B2 trusted user-principal provenance for authenticated manual agent runs.
Production MCP proposal registration remains disabled under B3.

## Threats and controls

| Threat | Control |
| --- | --- |
| Unauthenticated manual run | `auth.FromContext` required; endpoint returns 401 when absent |
| Disabled/deleted account | `auth.ResolveActiveIdentity` current-state lookup rejects it |
| Forged principal in request | Strict top-level request struct with `DisallowUnknownFields`; no principal/owner/user fields accepted. `input` is bounded to 64 KiB and may be any JSON value required by the agent action. |
| Forged principal in manifest | `WithMCPMutationPrincipal` only installs a typed, validated value supplied by the trusted caller; manifests are not read |
| Forged principal in event payload | `agentInputConfig` never sets one; event runtimes remain principal-less |
| Forged runtime map/string | `applyExecutionContext` deletes any existing `mcp_mutation_principal` and publishes only the typed execution-context value |
| Model/tool argument injection | Principal is not part of messages, tool schemas, tool arguments or model output; `ToolRuntime.Principal` is a separate typed field |
| Cross-run principal leakage | Principal lives in one per-invocation `AgentExecutionContext`; parallel groups receive a copy |
| Non-manual agent execution | Endpoint requires a registered agent with a `manual` trigger and filters to manual jobs only |
| Event job executed out of band | `ManualJobConfig` filters event-triggered jobs before execution |
| Missing operator authorization | Endpoint requires `events:*` or `events:agent:run` from current grants |
| Disabled identity enforcement | Endpoint refuses manual runs when `config.Agent.IdentityEnforcement` is false |
| MCP mutation alias without principal | Tool loop and goal action deny explicitly before any proposal adapter exists |
| Sensitive error leakage | Client receives stable messages; detailed errors are logged server-side |
| Schema drift | No schema change; existing 1.17 auth tables only |

## Authorization notes

The endpoint uses the existing coarse `events:*` operator scope, matching the
service that owns the agent engine. A later B3/B5 decision should introduce a
narrower `events:agent:run` scope with denied-admission audit if manual runs are
opened to non-administrative operators.

Manual-run eligibility does not grant MCP proposal authority. It only supplies
a trusted principal to the runtime. B3 still has to register an inert proposal
adapter and enforce the independent MCP alias authority.

## Residual risks

- B3 production registration is not implemented.
- B4 approval-time current-policy revalidation is not implemented.
- B5 denied-admission audit/retention is not implemented.
- The endpoint is synchronous and bounded by the agent's own action budgets;
  HTTP request cancellation does not interrupt an already-running action whose
  implementation is not context-aware. This matches the existing agent engine
  contract.
- The `events:*` scope is broader than a dedicated manual-run scope.
- No database schema change was needed; SQLite FK enforcement remains a
  separate G3 decision.

## Remote-effect conclusion

No mutating MCP `tools/call` is made. No dispatcher, dispatch claim,
`MCPMutationDispatchAttempts` insert, retry, reconciliation worker, execution
endpoint or automatic approval execution is added. The manual endpoint executes
only the registered agent's configured jobs, subject to existing capability,
trust, contract and budget gates.
