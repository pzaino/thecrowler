# WP-04C2-C Authorization Model and Production Proposal Gate

## 1. Trusted identity provenance today

The agent runtime has typed principal fields but no production path populates
them for MCP mutation proposals:

- `AgentToolLoopConfig` / `ToolLoopConfig` has
  `Principal MCPMutationPrincipal` at `pkg/agent/tool_loop.go:118`, and
  `RunToolLoop` copies it into `ToolRuntime.Principal` at
  `pkg/agent/tool_loop.go:512`.
- `goalRunConfig` has the same field at `pkg/agent/action_goal_run.go:328`, and
  `RunGoal` copies it into `ToolRuntime.Principal` at
  `pkg/agent/action_goal_run.go:542`.
- `normalizeToolLoopConfig` at `pkg/agent/action_tool_loop.go:109` constructs a
  loop from manifest/operator values but **does not set `Principal`**.
- The goal action run at `pkg/agent/action_goal_run.go:198` constructs
  `goalRunConfig` but **does not set `Principal`**.
- Only tests set these fields directly.
- `AgentIdentity.Owner` is a manifest/operator label, not an authenticated
  account. It must never be promoted to `MCPMutationPrincipal`.
- Background/event-triggered runs have no service-principal contract in the
  inspected code. They must fail closed rather than manufacture an
  administrator or owner principal.

The operator API already resolves authenticated identity correctly through
`auth.FromContext` + `auth.ResolveActiveIdentity` for approval/observation, but
that principal is not propagated into the agent tool loop that would create a
proposal.

**Conclusion:** a production inert proposal path cannot be safely enabled until
a trusted principal is installed into the loop/goal runtime. This patch leaves
producer registration disabled.

## 2. Independent permissions

The following are distinct and must not be collapsed:

| Permission | Authority | Current state |
| --- | --- | --- |
| MCP server/tool discovery | `MCPConfig` approved server/tool/effect/transport/allowed agents | Implemented/read-only discovery |
| External mutation proposal | `MCPMutationPolicy.Operation` exact server/tool alias, agent, capability, trust, scope, arguments, TTL/quota | Test-only policy; no public loader |
| Approval | Authenticated operator, current roles/scopes, source ownership every source or admin, exact fingerprint/version | Implemented |
| Denial/revocation | Authenticated operator with current authority; administrators retain emergency deny/revoke after ownership changes; revocation cannot undo a sent external effect | Implemented for inert proposals |
| Execution | Separate future dispatch claim and backend | Not implemented |
| Observation | Admin sees all; non-admin sees only source proposals with verified ownership | Implemented |
| Native crawl mutation | `CrawlRequestToolRuntime` / native crawl policy | Separate; not evidence of MCP authority |

`ToolAuthorization.MutationAllowed` is a coarse generic mutation gate. The MCP
proposal path additionally requires the exact `MCPMutationPolicy` operation and
an authenticated principal, so the native crawl flag alone cannot authorize an
MCP proposal. However, production `AllowedOperations` is currently populated
from the native crawl mutation policy at
`pkg/agent/action_tool_loop.go:298` and `pkg/agent/action_goal_run.go:201`; there
is no separate production allowlist path for MCP proposal aliases. That
separation must be implemented with the future producer, not by reusing the
native crawl operation set.

## 3. Public configuration decision gate

### Existing contract is insufficient

`pkg/config/mcp.go:89` has `MCPApprovedTool.AllowMutationProposal bool`. It is a
discovery/approval flag, not the full proposal policy. The full policy is the
Go-only `MCPMutationPolicy` at `pkg/agent/mcp_mutation.go:103`; there is no YAML/
JSON loader, JSON Schema entry, default or operator example for its operation
fields. Agent manifests cannot carry it and must not.

Adding a new public key is a maintainer decision. No key was added in this
patch.

### Minimal proposed contract (for approval only)

Recommended top-level operator key: `mcp.mutation_policy`, default disabled.

```yaml
mcp:
  mutation_policy:
    enabled: false
    policy_id: ""
    version: ""
    operations: []
```

A non-empty operation would use:

```yaml
operations:
  - server_id: "approved-server"
    tool_name: "approved_tool"
    tool_alias: "approved-server__approved_tool" # must be derived exactly
    allowed_agents: ["agent-id"]
    required_capabilities: ["tool_execution"]
    min_trust_level: "trusted"
    allowed_sources: [42]          # exactly one of sources/resources
    allowed_resources: []
    required_argument_fields: ["source_id"]
    allowed_argument_fields: ["source_id"]
    destructive: false
    proposal_ttl: "5m"
    max_proposals_per_run: 4
    max_pending_proposals: 8
    max_input_bytes: 65536
    max_output_bytes: 65536
    require_approval: true         # must remain true
    execution_backend: "none"      # only none is allowed
    allowed_transports: ["stdio"]
    audit_required: true
```

Proposed Go contract (not implemented):

```go
type MCPMutationPolicyConfig struct {
    Enabled    bool                          `yaml:"enabled" json:"enabled"`
    PolicyID   string                        `yaml:"policy_id" json:"policy_id"`
    Version    string                        `yaml:"version" json:"version"`
    Operations []MCPMutationOperationConfig  `yaml:"operations" json:"operations"`
}

type MCPMutationOperationConfig struct {
    ServerID               string   `yaml:"server_id" json:"server_id"`
    ToolName               string   `yaml:"tool_name" json:"tool_name"`
    ToolAlias              string   `yaml:"tool_alias" json:"tool_alias"`
    AllowedAgents          []string `yaml:"allowed_agents" json:"allowed_agents"`
    RequiredCapabilities   []string `yaml:"required_capabilities" json:"required_capabilities"`
    MinTrustLevel          string   `yaml:"min_trust_level" json:"min_trust_level"`
    AllowedSources         []uint64 `yaml:"allowed_sources" json:"allowed_sources"`
    AllowedResources       []string `yaml:"allowed_resources" json:"allowed_resources"`
    RequiredArgumentFields []string `yaml:"required_argument_fields" json:"required_argument_fields"`
    AllowedArgumentFields  []string `yaml:"allowed_argument_fields" json:"allowed_argument_fields"`
    Destructive            bool     `yaml:"destructive" json:"destructive"`
    ProposalTTL            string   `yaml:"proposal_ttl" json:"proposal_ttl"`
    MaxProposalsPerRun     int      `yaml:"max_proposals_per_run" json:"max_proposals_per_run"`
    MaxPendingProposals    int      `yaml:"max_pending_proposals" json:"max_pending_proposals"`
    MaxInputBytes          int      `yaml:"max_input_bytes" json:"max_input_bytes"`
    MaxOutputBytes         int      `yaml:"max_output_bytes" json:"max_output_bytes"`
    RequireApproval        bool     `yaml:"require_approval" json:"require_approval"`
    ExecutionBackend       string   `yaml:"execution_backend" json:"execution_backend"`
    AllowedTransports      []string `yaml:"allowed_transports" json:"allowed_transports"`
    AuditRequired          bool     `yaml:"audit_required" json:"audit_required"`
}
```

Required validation:

- default `enabled:false`; empty policy cannot authorize anything;
- `tool_alias` must equal `MCPToolAlias(server_id, tool_name)`;
- exactly one of `allowed_sources`/`allowed_resources`, both non-empty when used;
- `allowed_agents` non-empty; `required_capabilities` contains
  `tool_execution`; minimum trust is trusted-or-higher;
- source IDs positive; resources bounded and free of control characters;
- TTL 1s..1h; quotas within existing hard bounds; input/output 1..64 KiB;
- `require_approval` must be true; `execution_backend` must be `none`;
- transports only `stdio`/`streamable_http`;
- unknown fields rejected; agent manifest schema unchanged;
- operator-only; never accepted from model output or agent manifests;
- config load must fail closed on validation errors.

Required tests before implementation:

- default/disabled policy creates no proposal;
- exact alias mismatch, duplicate operation, mixed source/resource, missing
  agent/capability, invalid transport/backend/TTL/quota are rejected;
- agent manifest cannot add mutation policy or approval identity;
- config examples and JSON Schema round-trip;
- loader reload cannot grant stale approval authority.

**STOP:** obtain maintainer approval for this public contract before adding the
structs, JSON Schema, defaults, loader, examples or tests.

## 4. Future inert producer wiring

When the config and principal provenance are approved:

1. Resolve the authenticated principal at the API/event boundary and pass it
   explicitly into `ToolLoopConfig.Principal` / `goalRunConfig.Principal`.
   Never use `AgentIdentity.Owner`, model output or manifest values.
2. Require `MCPMutationPolicy.Enabled` and the exact operation.
3. Require the frozen MCP mutation descriptor: server/tool/effect/transport,
   endpoint or executable identity, schema hash.
4. Require agent allowlist, capabilities, trust, source/resource scope and
   argument schema.
5. Require a non-zero authenticated principal and the durable capability check.
6. Populate the operation allowlist for the MCP alias from the **MCP policy**,
   not from the native crawl mutation policy.
7. Register `MCPMutationProposalTool` through
   `AgentToolRegistry.RegisterMutation()` only after all gates pass.
8. Admit through `DatabaseMCPMutationProposalStore.Admit`.
9. Return only `approval_required`, proposal ID/fingerprint and expiry. Never
   return an approval token or completion evidence.
10. Emit a bounded audit event. Do not call a mutating remote `tools/call`.
11. Keep approval/execution/observation as separate permissions.

Failure to resolve any gate must leave the tool unregistered or deny the call.
No fallback to the in-memory store is allowed in production.

## 5. Required end-to-end acceptance test when approved

Use a local fixture MCP server exposing one read-only and one mutating tool, a
fake/real local model response, the durable SQLite/PostgreSQL store and the
operator API harness:

```text
authenticated scoped agent
  -> approved discovery
  -> proposal-only adapter
  -> atomic durable admission
  -> operator list/detail
  -> approve/deny/revoke
  -> audit/history
```

Assert:

- fixture mutating `tools/call` counter remains zero for success, duplicate,
  approval, denial, revocation, timeout, malformed args, unauthorized principal
  and exhausted budget;
- read-only MCP calls still reach the read-only fixture;
- the in-memory store is not used;
- no dispatch-attempt row exists;
- proposal/approval evidence does not satisfy goal completion.

## 6. Current disposition

The producer remains disabled. No new public YAML/JSON field, endpoint,
dispatcher or execution path was added. The next work package must first
approve the public policy contract and principal provenance above.
