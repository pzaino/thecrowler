# WP-04C2F Strict Mutation-Policy Parser

## Entrypoint matrix

All real operator configuration paths now call one strict extraction boundary,
`applyMCPMutationPolicyStrict`:

| Entrypoint | Handling |
| --- | --- |
| `getConfigFile` | Reads the local file, interpolates environment variables, expands includes, parses normally, then strict-extracts the policy |
| include expansion | Strict extraction runs on the final expanded document |
| `LoadRemoteConfig` | Fetches/interpolates the remote body, parses normally, then strict-extracts |
| `ParseConfig` YAML | Strict-extracts from the same bytes after normal parsing |
| `ParseConfig` JSON | JSON is accepted by the YAML parser; strict extraction still scans the original document |
| `Config.Validate` | Revalidates the typed policy and inventory binding for programmatic configs |
| `agent.SetMCPOperatorConfig` | Normalizes servers, validates the policy and binds it to the normalized inventory before installing the snapshot |

The legacy whole-document parser remains permissive. Strictness is limited to
`mcp.mutation_policy` and its immediate `mcp`/`mutation_policy` ancestors.
Unknown top-level `mcp` keys are rejected; unrelated legacy sections are not.

## Rejected input

The strict boundary rejects:

- unknown keys inside `mutation_policy` and unknown top-level `mcp` keys;
- duplicate `mcp`, duplicate `mutation_policy`, and duplicate keys at any depth
  inside the policy, including duplicate JSON keys;
- YAML aliases, anchors and merge keys inside the policy or on its immediate
  ancestor nodes;
- custom/ambiguous YAML tags;
- null values, wrong scalar types, float-to-int coercion, int-to-string
  coercion, wrong sequence/object shapes and invalid UTF-8;
- invalid duration syntax and values outside 1s..1h;
- unknown trust levels, transports and execution backends;
- empty `allowed_agents`/`required_capabilities`, missing `tool_execution`,
  false `require_approval`/`audit_required`, and any backend other than
  `none`;
- mixed source/resource scope, zero/duplicate sources, invalid or duplicate
  resources, invalid argument-field lists, duplicate operations, duplicate
  derived operation aliases and inventory-wide alias collisions;
- an enabled policy with missing policy ID/version or zero operations;
- an operation that does not bind exactly to an enabled server, an approved
  `effect: mutation` tool, `allow_mutation_proposal: true`, the exact transport,
  the server/tool source scope, the server agent allowlist and the tool's
  required capabilities.

## Type validation

`yaml.v3` alone coerces some values (`1.5` to `1`, `42` to `"42"`, null to
zero). The strict boundary therefore walks the policy node and checks the
expected YAML tag per field before decoding:

- `!!bool` for `enabled`, `destructive`, `require_approval`, `audit_required`;
- `!!int` for quota/byte bounds and source IDs;
- `!!str` for IDs, names, durations, trust, backend and string lists;
- `!!seq` for all list fields and `operations`;
- `!!map` for operations and the policy root.

Decoding then uses `yaml.v3` `KnownFields(true)` as a second boundary. A
positive test proves a real enabled policy loads; negative tests prove the
type, duplicate, anchor/merge, null, enum and inventory cases fail.

## Tests

`pkg/config/mcp_mutation_policy_test.go` covers:

- valid local file, include-expanded, remote, YAML and JSON paths;
- absent and explicitly disabled policy;
- unknown keys, duplicate outer/nested keys, duplicate JSON keys;
- YAML anchor and merge rejection;
- null, bool-as-string, float-as-int and int-as-string rejection;
- invalid durations, transports, positive backends, false approval/audit;
- mixed scopes, source outside inventory, missing/disabled server, missing
  tool, read-only/effect mismatch, missing proposal opt-in, agent/capability
  mismatch, empty operations and invalid server IDs;
- unrelated legacy duplicate/unknown `crawler` fields remain accepted.

`pkg/agent/mcp_mutation_policy_test.go` covers exact inventory binding,
snapshot immutability/concurrency and the runtime policy conversion.

## Limitation

The repository's existing full JSON Schema does not compile under the pinned
`qri-io/jsonschema` version even before this change (a pre-existing `items`
shape limitation). The mutation-policy sub-schema is compiled and validated
against positive/negative fixtures; runtime validation remains mandatory and is
not replaced by schema validation.
