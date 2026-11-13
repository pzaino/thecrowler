# WP-04C2D Operator Mutation Policy Contract

**STATUS: PROPOSED — NOT APPROVED. No public configuration field was added in
this work package. Production proposal registration remains disabled.**

## 1. Current contract

`pkg/config/mcp.go` exposes `MCPApprovedTool.AllowMutationProposal`, a
discovery/approval flag. It is not sufficient authority to create an external
mutation proposal. The full `MCPMutationPolicy` in
`pkg/agent/mcp_mutation.go` is a Go-only trusted contract with no YAML/JSON
loader, JSON Schema entry, default or operator example.

Adding a public field requires an explicit maintainer decision. This document
is the proposed contract for that decision; it is not approval.

## 2. Proposed location and ownership

- Location: top-level operator configuration `mcp.mutation_policy`, under the
  existing `mcp` section.
- Owner: operator configuration only. It must not be accepted in an agent
  manifest, model arguments, event payload or inherited step property.
- Default: `enabled: false`, empty policy, no operations. Existing read-only MCP
  configurations remain valid and unchanged.
- Unknown fields: must be rejected by a dedicated strict
  `mcp.mutation_policy` subtree decoder. The global legacy YAML/JSON parser is
  permissive and silently ignores unknown nested keys, typos and duplicate
  mappings, as proven in `WP-04C2E_CONFIG_STRICTNESS.md`. Strictness for this
  authorization subtree is **not implemented** and is gated by B1; it must not
  be assumed from the existing loader.

### Minimal default

```yaml
mcp:
  servers: []
  mutation_policy:
    enabled: false
    policy_id: ""
    version: ""
    operations: []
```

### Example operation (aliases derived, not hand-entered)

```yaml
mcp:
  mutation_policy:
    enabled: true
    policy_id: "approved-mutations"
    version: "1"
    operations:
      - server_id: "approved-server"
        tool_name: "approved_tool"
        # tool_alias is derived as MCPToolAlias(server_id, tool_name):
        # mcp_approved-server_approved_tool
        allowed_agents: ["agent-id"]
        required_capabilities: ["tool_execution"]
        min_trust_level: "trusted"
        allowed_sources: [42]
        allowed_resources: []
        required_argument_fields: ["source_id"]
        allowed_argument_fields: ["source_id"]
        destructive: false
        proposal_ttl: "5m"
        max_proposals_per_run: 4
        max_pending_proposals: 8
        max_input_bytes: 65536
        max_output_bytes: 65536
        require_approval: true
        execution_backend: "none"
        allowed_transports: ["stdio"]
        audit_required: true
```

`MCPToolAlias("approved-server", "approved_tool")` returns
`mcp_approved-server_approved_tool` (the `mcp_` prefix, server ID, `_`, and the
sanitized raw tool name). The alias must be derived from trusted
`server_id`/`tool_name`; if an operator-visible alias is retained, exact
equality must be validated. The existing policy validation test covers this
example.

## 3. Go mapping and schema

Proposed types (not implemented):

```go
type MCPMutationPolicyConfig struct {
    Enabled    bool                         `yaml:"enabled" json:"enabled"`
    PolicyID   string                       `yaml:"policy_id" json:"policy_id"`
    Version    string                       `yaml:"version" json:"version"`
    Operations []MCPMutationOperationConfig `yaml:"operations" json:"operations"`
}

type MCPMutationOperationConfig struct {
    ServerID                string   `yaml:"server_id" json:"server_id"`
    ToolName                string   `yaml:"tool_name" json:"tool_name"`
    AllowedAgents           []string `yaml:"allowed_agents" json:"allowed_agents"`
    RequiredCapabilities    []string `yaml:"required_capabilities" json:"required_capabilities"`
    MinTrustLevel           string   `yaml:"min_trust_level" json:"min_trust_level"`
    AllowedSources          []uint64 `yaml:"allowed_sources" json:"allowed_sources"`
    AllowedResources        []string `yaml:"allowed_resources" json:"allowed_resources"`
    RequiredArgumentFields  []string `yaml:"required_argument_fields" json:"required_argument_fields"`
    AllowedArgumentFields   []string `yaml:"allowed_argument_fields" json:"allowed_argument_fields"`
    Destructive             bool     `yaml:"destructive" json:"destructive"`
    ProposalTTL             string   `yaml:"proposal_ttl" json:"proposal_ttl"`
    MaxProposalsPerRun      int      `yaml:"max_proposals_per_run" json:"max_proposals_per_run"`
    MaxPendingProposals     int      `yaml:"max_pending_proposals" json:"max_pending_proposals"`
    MaxInputBytes           int      `yaml:"max_input_bytes" json:"max_input_bytes"`
    MaxOutputBytes          int      `yaml:"max_output_bytes" json:"max_output_bytes"`
    RequireApproval         bool     `yaml:"require_approval" json:"require_approval"`
    ExecutionBackend        string   `yaml:"execution_backend" json:"execution_backend"`
    AllowedTransports       []string `yaml:"allowed_transports" json:"allowed_transports"`
    AuditRequired           bool     `yaml:"audit_required" json:"audit_required"`
}
```

`tool_alias` is intentionally not a required public field; deriving it avoids
operator-entry mismatch. If exposed for diagnostics, it must exactly equal
`MCPToolAlias(server_id, tool_name)`.

Validation:

- `enabled` default false; disabled policy creates no proposal tool.
- `policy_id`/`version` required when enabled; operations 1..32, aliases unique.
- exact server/tool alias equality; non-empty server/tool IDs.
- `allowed_agents` non-empty; `required_capabilities` includes
  `tool_execution`; trust trusted-or-higher.
- exactly one of `allowed_sources`/`allowed_resources`, non-empty when used.
- source IDs positive; resource strings bounded and control-character free.
- TTL 1s..1h; quotas within existing hard bounds; input/output 1..64 KiB.
- `require_approval: true` only; `execution_backend: none` only.
- transports exactly `stdio`/`streamable_http`.
- `audit_required: true` required for mutation operations.
- unknown/contradictory values fail config load; no partial acceptance.

JSON Schema: add the equivalent `mcp.mutation_policy` object to
`schemas/crowler-config-schema.json` with `additionalProperties: false`,
defaults, required fields, enums and min/max bounds. Add a validated example
under `examples/` and a negative fixture for each rejection class.

## 4. Policy identity, snapshots and reload

- Policy identity is `(policy_id, version, SHA-256(canonical policy))`.
- The admission snapshot is an immutable copy of the validated policy
  operation plus the exact `MCPToolAlias`, server/tool names, transport,
  argument constraints, scope and quotas.
- A proposal is bound to the policy hash/snapshot at admission. Later policy
  changes do not mutate the stored proposal.
- New admission requires the current policy to be enabled and contain the exact
  operation. A removed/disabled/changed operation blocks new proposals.
- Existing proposals remain inert historical records. Approval must revalidate
  the current policy hash and authority; if the current policy no longer
  authorizes the operation, approval is blocked pending a reviewed reapproval
  decision.
- The repository has no approved dynamic reload mechanism. The recommended
  model is process-start configuration plus explicit restart for policy
  changes. If a reload is later supported, it must either reject in-flight
  policy changes or apply a clearly documented generation boundary; silent
  immediacy is not acceptable.
- Missing, invalid, stale or unreadable policy fails closed. No fallback to a
  previous policy, native crawl policy or in-memory defaults.

## 5. Negative cases to test before implementation

- absent/disabled policy; empty operations; duplicate operation/alias;
  unknown key; malformed duration; quota/size zero or excessive.
- wrong derived alias; server/tool mismatch; unapproved transport; unsupported
  execution backend; `require_approval:false`; `audit_required:false`.
- mixed source/resource scope; zero source; empty/control-character resource;
  missing agent/capability/trust.
- model/manifest/event attempts to set policy, principal, expiry, endpoint,
  approval ID, operation allowance or idempotency key.
- reload disable/remove/agent change/server/tool/transport change during an
  active run; existing proposal remains readable but new admission/approval
  fails closed as defined.
- exact example loader and JSON Schema round-trip.

## 6. Gate

No public field, schema, default, loader or example has been changed. Gate B1
is **not approved**. Implementation is withheld until the maintainer records an
explicit decision in `WP-04C2D_MAINTAINER_DECISIONS.md`.
