# WP-04C2F Security Review

## Scope

This review covers the B1 operator policy and strict configuration boundary
only. Production proposal registration, authenticated principal provenance,
approval-time policy revalidation, denied-admission audit and database
deployment remain outside this change.

## Threats and controls

### Configuration injection

| Threat | Control |
| --- | --- |
| Unknown authorization key typo | Strict `KnownFields` plus explicit `mcp` key allowlist |
| Duplicate outer `mcp`/`mutation_policy` hiding a policy | YAML node duplicate scan before decoding; duplicate JSON keys are preserved by the parser and rejected |
| Duplicate nested key | Recursive mapping duplicate scan |
| YAML merge or alias smuggling | Anchors, aliases and `<<` merge keys rejected in the policy and its immediate ancestors |
| Custom tag/type confusion | Standard YAML tags only; per-field tag validation; `KnownFields` decode |
| Float/int/string coercion | Explicit expected-tag checks reject `1.5` as an integer and `42` as a string |
| Null bypass | Null scalar nodes rejected throughout the policy |
| Invalid UTF-8 | Document and scalar UTF-8 checks |
| Invalid duration | `time.ParseDuration` plus 1s..1h bound |
| Dangerous backend/transport | `execution_backend` only `none`; transports only `stdio`/`streamable_http` |
| Disabled policy silently authorizing | Disabled policy is not converted into an enabled runtime policy; `CurrentMCPMutationPolicy` returns an empty policy |
| Policy omitted from normalized config | Policy cloned into `normalizeMCPOperatorConfig` and `cloneMCPConfig` |

### Authority widening

An operation is admitted only if it binds to:

- an **enabled** server with the exact canonical ID;
- an approved tool with the exact raw name, `effect: mutation` and
  `allow_mutation_proposal: true`;
- the exact server transport;
- the server agent allowlist (when non-empty);
- the effective server/tool source scope (when source-scoped);
- no conflicting source scope when resource-scoped;
- the tool's required capabilities;
- bounded quotas, TTL, argument fields and byte sizes.

The policy is an additional authority layer, never a substitute for tool
discovery, tool source scope or runtime authorization. The server/tool
classification alone is not proposal authority.

### Alias binding

Aliases are derived with the shared `config.MCPToolAlias(server_id, tool_name)`
implementation used by both validation and runtime. The public config has no
operator-entered alias. Configuration validation rejects duplicate derived
operation aliases and inventory-wide alias collisions; global server
normalization and the runtime policy validator recheck them. A proposal cannot
bind to a different advertised tool through an alias substitution.

### Model, manifest and event input

`mcp.mutation_policy` exists only on `pkg/config.MCPConfig`. It is not an agent
manifest field, event field, model argument or inherited step value. The policy
loader reads operator configuration only. `AgentIdentity.Owner` is not a
principal and is not used to derive policy authority.

### Snapshot mutation and concurrency

`SetMCPOperatorConfig` normalizes, binds and converts before locking, then
installs the config and policy atomically. `CurrentMCPOperatorConfig` and
`CurrentMCPMutationPolicy` return deep copies. Tests mutate caller input and
returned slices and prove the global snapshot is unchanged. Concurrent getter
and setter tests run under the race detector.

A previously undiscovered bug in the baseline normalization shared server
slices with the caller; `cloneMCPServerConfig` now prevents that. This was a real
immutability finding, not a theoretical concern.

### Policy changes and staleness

The policy is frozen for the process lifetime. There is no silent hot reload;
an operator must restart. Disabled/removed/malformed policy does not authorize
new admission. Historical proposals retain their stored snapshots. B4
approval-time current-policy revalidation is **not implemented**; approvals
remain restricted to the existing inert behavior until that decision.

### Logging and error redaction

`MCPConfig.String`/`GoString` report only server identity/transport/tool count
and policy enabled/operation count. They do not print policy agents, argument
fields, source IDs, resources, endpoints or credentials. Strict errors identify
field paths and stable reasons without echoing resource values or secret
material. Server/tool names may appear in diagnostics; they are operator
configuration identifiers, not credentials.

## Residual risks

- B2/B3 production registration and principal provenance are not implemented.
- B4 approval-time policy revalidation is pending.
- B5 denied-admission audit is pending.
- G3 schema deployment and SQLite FK enforcement are pending; SQLite pooled
  connections remain FK-off.
- Policy changes require restart; there is no dynamic reload.
- The full legacy JSON Schema does not compile under the pinned
  `qri-io/jsonschema` tooling; the policy sub-schema is validated separately and
  runtime validation is mandatory.
- The strict parser inspects the whole operator document; it does not add a new
  file-size limit. Existing configuration-loading size controls remain in
  effect.

## Remote-effect conclusion

No mutating MCP `tools/call` is made by this change. No dispatcher, dispatch
claim, dispatch-attempt insert, retry, reconciliation worker, execution endpoint
or automatic approval execution exists. B1 only prepares a validated, frozen
operator policy for a later separately approved producer.
