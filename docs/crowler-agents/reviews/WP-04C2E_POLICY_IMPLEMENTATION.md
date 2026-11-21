# WP-04C2E Operator Policy Implementation Status

## Status: superseded by WP-04C2F

**B1 was approved and implemented in WP-04C2F.** See
`WP-04C2F_B1_DECISION.md` and `WP-04C2F_POLICY_IMPLEMENTATION.md`. The text
below records the pre-approval WP-04C2E position and is retained as historical
context.

At the time of WP-04C2E no maintainer approval existed for a public
`mcp.mutation_policy` contract, and the work package did **not** add:

- `MCPMutationPolicyConfig` or operation config types to `pkg/config`;
- a `mutation_policy` field to `MCPConfig`;
- a `schemas/crowler-config-schema.json` entry;
- a `config.default` example or default-enabled behavior;
- a production config loader or strict decoder for the subtree;
- an operator-facing policy example outside the review documents.

`pkg/config/mcp.go` still contains only `MCPConfig.Servers`. The existing
`MCPApprovedTool.AllowMutationProposal` remains a discovery/classification flag,
not proposal authority. `agent.MCPMutationPolicy` remains a Go-only trusted
contract without YAML/JSON tags or a loader.

## Actual parser behavior

`pkg/config/config.go` uses `gopkg.in/yaml.v2` in `getConfigFile`,
`LoadRemoteConfig` and `ParseConfig`. `TestMutationPolicyUnknownKeysAreCurrentlyIgnored`
proves that all three paths, plus JSON input through `ParseConfig`, currently:

- ignore unknown keys in an `mcp.mutation_policy` subtree;
- ignore authorization typos such as `require_aproval` and `allowed_resouces`;
- ignore an invalid duration such as `proposal_ttl: not-a-duration`;
- accept duplicate mapping keys with last-value-wins behavior;
- reject a wrong scalar shape for a known typed field such as
  `mcp.servers: not-a-list`.

The WP-04C2D contract statement that unknown fields would be rejected by the
"strict config decoder" was therefore inaccurate. It has been corrected; the
detailed evidence and the narrow strict-subtree design are in
`WP-04C2E_CONFIG_STRICTNESS.md`. The design keeps unrelated legacy sections
permissive and adds strict validation only for the authorization-bearing
subtree at every actual load path.

## Internal hardening completed without B1

`validateMCPMutationOperation` in `pkg/agent/mcp_mutation.go` now:

- requires `server_id` to match the production
  `^[a-z][a-z0-9-]{0,31}$` grammar;
- bounds `tool_name` to the production 128-byte limit and rejects control
  characters;
- still requires the exact derived alias.

This is internal-only validation. It prevents a policy from accepting an
operation that the operator inventory cannot name, without exposing any new
public configuration.

## Contract required from the maintainer

The exact proposal remains `WP-04C2D_POLICY_CONTRACT.md`, with the corrections
above. B1 must decide:

- location, types, YAML/JSON tags, unknown-field handling, defaults and
  `config.default` example;
- derived alias policy and whether any operator-visible alias is exposed;
- exact operation fields, limits, transports, scope, quotas, TTL and argument
  constraints;
- policy fingerprint/snapshot and reload/staleness semantics;
- JSON Schema and positive/negative fixtures;
- `require_approval: true`, `execution_backend: none`, `audit_required: true`
  and default-disabled behavior;
- ownership of the loader/runtime conversion and the prohibition on agent
  manifest, event, model or inherited-step policy input.

## Implementation plan if B1 is approved

1. Add operator-only config types and tags with deny-by-default initialization.
2. Add a strict subtree decoder and duplicate-key scan around the existing
   permissive whole-document parse, applied to local file, remote fetch,
   includes/environment interpolation and JSON input.
3. Map the validated config explicitly into an immutable
   `agent.MCPMutationPolicy`, deriving aliases with `MCPToolAlias` and reusing
   the production server/tool grammar.
4. Add JSON Schema, default config, reference documentation and positive and
   negative fixtures.
5. Add restart/reload tests: disabled/removed/changed operations stop new
   admissions; existing rows remain inert historical records; approval
   revalidation follows B4.
6. Keep read-only MCP configurations and native crawl policy unchanged.

## Stopping point

Source activation stops before public config/schema/default/loader changes.
Tests and the internal validator change in this work package are safe to review
independently. No policy is enabled by default and no production proposal tool
is registered.
