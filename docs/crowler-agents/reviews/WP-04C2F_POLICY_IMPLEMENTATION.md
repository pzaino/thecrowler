# WP-04C2F Operator Policy Implementation

## Public contract

`pkg/config.MCPConfig` now has an operator-only `MutationPolicy` field:

```go
type MCPConfig struct {
    Servers        []MCPServerConfig        `yaml:"servers" json:"servers"`
    MutationPolicy MCPMutationPolicyConfig `yaml:"mutation_policy" json:"mutation_policy"`
}
```

`MCPMutationPolicyConfig` contains `enabled`, `policy_id`, `version` and
`operations`. Each `MCPMutationOperationConfig` contains the approved operation
fields. There is deliberately no `tool_alias` field; aliases are derived.

Defaults are fail-closed:

- absent policy is the zero value: disabled, no operations, no authority;
- explicitly disabled policy is accepted but grants nothing;
- enabled policy requires a canonical policy ID/version and 1..32 operations;
- every operation requires `require_approval: true`, `audit_required: true`,
  `execution_backend: "none"`, a trusted-or-higher level, `tool_execution`,
  bounded quotas/bytes, one exact transport and exactly one source/resource
  scope.

## Validation layers

1. Strict subtree extraction rejects malformed authorization input at every
   configuration load path.
2. `MCPMutationPolicyConfig.Validate` enforces structural and safety bounds.
3. `MCPMutationPolicyConfig.ValidateInventory` binds each operation to the
   operator inventory:
   - exact enabled server ID;
   - exact approved tool name with `effect: mutation` and
     `allow_mutation_proposal: true`;
   - exact server transport;
   - policy agents within a non-empty server agent allowlist;
   - policy source scope within the effective server/tool source scope;
   - resource scope only when the tool is not source-scoped;
   - policy required capabilities include the tool's required capabilities.
4. `agent.buildMCPMutationPolicy` converts to the trusted
   `MCPMutationPolicy`, derives `MCPToolAlias`, parses the TTL to
   `time.Duration`, and runs the runtime validator.
5. `agent.MCPMutationPolicy.Validate` rechecks the runtime invariants.

`config.EffectiveMCPToolSources` is shared with the manager's discovery path,
so the config binding and runtime scope use the same intersection rule.

## Snapshot and fingerprint

`SetMCPOperatorConfig` performs all normalization, binding and conversion before
taking the global lock. It then atomically stores both the normalized
`cfg.MCPConfig` and the converted `MCPMutationPolicy`. A failed set leaves the
previous snapshot unchanged.

`CurrentMCPOperatorConfig` returns a deep copy of servers and policy.
`CurrentMCPMutationPolicy` returns a deep copy of the runtime policy.
`MCPMutationPolicyFromConfig` validates/converts a config without installing it.

The runtime policy identity is the existing canonical
`MCPMutationPolicy.SHA256()` over the normalized policy, including derived
aliases and parsed durations. The hash is computed after normalization, not from
raw operator text.

## Immutability finding and fix

The original normalization rebuilt only `Servers`, so adding
`MutationPolicy` would have silently dropped it. It also appended normalized
servers whose `AllowedAgents`, `AllowedSources` and approved-tool slices still
shared caller backing arrays.

WP-04C2F fixes both:

- `normalizeMCPOperatorConfig` clones the policy and appends
  `cloneMCPServerConfig(normalized)`;
- `cloneMCPConfig` clones the policy and every server via
  `cloneMCPServerConfig`;
- the runtime policy getter deep-copies every operation slice.

The regression test mutates caller input and getter results and proves the
global snapshot does not change.

## Process lifecycle

The policy is process-start configuration. There is no silent hot reload. A
change requires restart. Disabled, removed or malformed policy stops new
admission authority; historical durable proposals keep their stored snapshot.
Approval-time current-policy revalidation is B4 and remains pending.

## Schema and defaults

`schemas/crowler-config-schema.json` now defines a strict `mcp` object,
`mcpMutationPolicy` and `mcpMutationPolicyOperation` with
`additionalProperties: false`, bounds, enums and the source/resource `oneOf`.
`config.default` contains a disabled policy block. The configuration reference
and YAML documentation describe defaults, strict errors, restart semantics,
alias derivation and the fact that production registration is still disabled.
`examples/mcp-mutation-policy.yaml` is a validated, non-deploying placeholder
example, and `TestMCPMutationPolicyExampleLoads` proves it parses through the
real loader.

The policy sub-schema passes positive and negative fixtures through the
repository's pinned `qri-io/jsonschema` tooling. The full legacy schema has a
pre-existing compilation limitation in that tooling; runtime validation is
mandatory regardless.

## Production boundary

No production `NewMCPMutationProposalTool` call, MCP `RegisterMutation`, agent
loop registration, principal propagation or dispatcher was added.
`CurrentMCPMutationPolicy` is not consumed by a production proposal path. The
feature remains configuration and trusted-snapshot preparation for the
separately gated B2/B3 work.
