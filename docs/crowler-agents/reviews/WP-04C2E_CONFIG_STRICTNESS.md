# WP-04C2E Config Parser Strictness Review

## Status

Observed behavior is documented and tested. No public mutation-policy field or
loader was added. Gate B1 remains pending.

## Actual loader behavior

Source paths:

- `getConfigFile` (`pkg/config/config.go`) reads the local file, interpolates
  environment variables, processes includes, and calls `yaml.Unmarshal`.
- `LoadRemoteConfig` fetches the remote body and calls `yaml.Unmarshal`.
- `ParseConfig` calls `yaml.Unmarshal` directly.

The dependency is `gopkg.in/yaml.v2`, whose default decoder:

- ignores unknown fields;
- accepts duplicate mapping keys with last-value-wins semantics;
- still returns type errors for a known field with an incompatible scalar
  shape.

`ParseConfig` also accepts JSON because YAML is a superset; unknown JSON fields
are likewise ignored.

### Proven tests

`TestMutationPolicyUnknownKeysAreCurrentlyIgnored` in `pkg/config` proves:

- local `getConfigFile`, remote `LoadRemoteConfig`, YAML `ParseConfig` and JSON
  `ParseConfig` all silently ignore an unknown nested
  `mcp.mutation_policy` subtree, including typos `require_aproval` and
  `allowed_resouces`;
- an unknown `proposal_ttl: not-a-duration` is ignored;
- duplicate `crawler.workers` keys are accepted with the last value winning;
- a wrong scalar type for typed `mcp.servers` returns an error.

This means the WP-04C2D statement that unknown authorization keys would be
rejected is not true of the current parser. It must not be relied upon.

## Narrow strict-subtree strategy (proposed, gated by B1)

Do not make the global legacy parser strict: unrelated legacy sections depend
on permissive behavior.

Recommended implementation when B1 is approved:

1. Keep the existing permissive whole-document parse for backward compatibility.
2. Extract the raw `mcp.mutation_policy` mapping node/subdocument from the same
   input.
3. If present, decode it through a dedicated strict boundary:
   - YAML: `yaml.v3` `Decoder.KnownFields(true)` into the new struct, after a
     `yaml.Node` duplicate-key scan;
   - JSON: `json.Decoder.DisallowUnknownFields` into the new struct;
   - reject duplicate keys, unknown keys, wrong types, malformed durations and
     invalid enums before conversion.
4. Apply the same boundary to local file, remote fetch, includes/environment
   interpolation and any supported JSON config input. The strict validator
   runs after interpolation/include expansion so a typo cannot be hidden.
5. Validate the resulting typed policy with the same exact server/tool
   normalization and `MCPMutationPolicy.Validate` checks.
6. Fail startup if the key is present but invalid. No partial policy, silent
   fallback or default-enabled behavior.
7. Keep JSON Schema as an independent test/validation contract; do not assume it
   runs automatically at runtime.

## Required validation rules when implemented

- `enabled` default false; empty/absent policy authorizes nothing.
- Exact `server_id` grammar (`^[a-z][a-z0-9-]{0,31}$`) and derived alias via
  `MCPToolAlias`; reject `approved_server`.
- Exact operation uniqueness and no alias collisions.
- Exactly one source/resource scope; positive source IDs; bounded resource
  strings without control characters.
- `require_approval` must be true; `execution_backend` must be `none`;
  `audit_required` must be true for mutation operations.
- `proposal_ttl` parsed with `time.ParseDuration` and bounded 1s..1h.
- quotas and byte limits within existing hard bounds; zero/negative rejected.
- transports only `stdio`/`streamable_http`.
- unknown nested keys, duplicate keys and wrong scalar types rejected.

## Tests required with B1 implementation

- positive real inventory + policy through actual loader and JSON Schema;
- typo/duplicate/wrong-type/invalid-duration cases for every load path;
- defaults vs explicit false/zero semantics;
- agent manifest/event/model cannot set policy;
- invalid policy disables proposal registration and fails closed;
- restart/reload behavior per the approved contract.

Gate B1 is not approved; no public config changes were made.
