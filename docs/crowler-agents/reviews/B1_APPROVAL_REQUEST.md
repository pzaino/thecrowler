# WP-04C2F B1 Approval Request

**Status: BLOCKED ON B1. No public configuration or production code was
changed.**

## Why this is blocked

- Reviewed HEAD: `b5e7d020183acfba0e4a7c10834cf6612e3e135d`, branch `develop`,
  working tree clean.
- `docs/crowler-agents/reviews/WP-04C2E_MAINTAINER_DECISIONS.md` records
  **B1: pending** with **no approver recorded**.
- No explicit `APPROVE B1` decision exists in the tracked tree.
- The task request and the existing design documents are not approval.
- B2, B3, B4, B5 and G3 remain separately pending.

## Required approval

A maintainer must record this exact statement, or an equivalent explicit
decision, with approver, UTC date and contract version:

> **APPROVE B1 for WP-04C2F only:** Implement an operator-owned
> `mcp.mutation_policy`, default disabled, using the corrected proposed
> contract from `WP-04C2D_POLICY_CONTRACT.md` and strict-subtree approach from
> `WP-04C2E_CONFIG_STRICTNESS.md`, with derived aliases, exact inventory
> binding, YAML/JSON strictness, immutable snapshots and all negative tests.
> This approval does not include production proposal tool registration,
> principal propagation, SQLite FK changes, schema deployment,
> operator-policy-bypassing defaults, dispatch or remote MCP mutation
> execution.

Record it in this file or in
`docs/crowler-agents/reviews/WP-04C2E_MAINTAINER_DECISIONS.md`:

```text
Approver:
Date (UTC):
Contract version: WP-04C2F/B1-1
```

## Proposed contract for approval

### Location and default

Operator configuration only:

```yaml
mcp:
  servers: []
  mutation_policy:
    enabled: false
    policy_id: ""
    version: ""
    operations: []
```

The policy is never accepted from an agent manifest, job-spec mutation policy,
event payload, model output, tool argument or inherited step. A missing policy
remains disabled and grants nothing.

### Operation fields

`server_id`, `tool_name`, `allowed_agents` (non-empty), and
`required_capabilities` (must include `tool_execution`) are required.
`min_trust_level` must be trusted-or-higher. Exactly one of
`allowed_sources` (positive IDs) or `allowed_resources` (bounded,
control-character-free) must be non-empty; they are mutually exclusive.
`required_argument_fields` and `allowed_argument_fields` are bounded string
lists. `destructive` is explicit.

`proposal_ttl` is a bounded duration string parsed internally to
`time.Duration`, 1s..1h. `max_proposals_per_run`, `max_pending_proposals`,
`max_input_bytes` and `max_output_bytes` must be positive and within the
existing hard bounds. Zero, negatives, excessive sizes, duplicates, malformed
types and nulls fail.

Every enabled operation must have:

- `require_approval: true`
- `audit_required: true`
- `execution_backend: "none"`
- `allowed_transports` limited to `stdio` or `streamable_http`

Missing or explicit false values fail; they must not silently default to true.
`enabled: true` requires a nonblank policy ID/version and 1..32 operations.

### Exact inventory binding

The server must exist, be enabled, and already be normalized. The tool must be
an approved `effect: mutation` tool with `allow_mutation_proposal: true`, a
matching agent/capability/scope/transport, and no duplicate or colliding
alias. The alias is derived, not operator-entered:

```text
MCPToolAlias("approved-server", "approved_tool")
= mcp_approved-server_approved_tool
```

`approved_server` is invalid. A proposal must never silently bind to a
different advertised tool.

### Strict loading

One shared strict extraction and validation boundary must cover:

- local `getConfigFile`, including `recursiveInclude` and environment
  interpolation;
- `LoadRemoteConfig`;
- `ParseConfig` for YAML and JSON;
- startup validation and any other real entrypoint found during inventory.

The boundary must reject unknown authorization keys at any depth, duplicate
YAML/JSON keys inside the policy and for the outer `mcp`/`mutation_policy`
ancestors, unsafe or ambiguous aliases/anchors/merges, wrong scalar/array/object
shapes, nulls where forbidden, invalid duration syntax, unknown enum values,
duplicate operations and duplicate aliases. JSON `DisallowUnknownFields` alone
is insufficient because duplicate JSON keys need an explicit scan.

A present invalid policy fails configuration load. An absent policy stays
disabled. Unrelated legacy config sections keep their existing permissive
behavior; the global parser must not be made strict.

### Snapshot, versioning and reload

The policy identity is `(policy_id, version, SHA-256(canonical normalized
policy))`, computed after normalization. The loaded policy is an immutable
per-process snapshot:

- `SetMCPOperatorConfig` validates and installs atomically; failure preserves
  the previous snapshot.
- `CurrentMCPOperatorConfig` returns a deep copy; caller or getter mutation
  cannot alter the global snapshot.
- Servers, operations, agents, capabilities, sources, resources and argument
  keys are all deep-copied.
- Changes require an explicit process restart. This package does not implement
  silent hot reload.
- Historical proposals retain their stored snapshot. A disabled, removed or
  changed current policy cannot authorize a stale operation at admission.
- Approval-time current-policy revalidation is **B4** and is not included here.
- `MCPConfig.String`/`GoString` must not dump policy scopes, resources,
  credentials or endpoints.

### Schema and documentation

Add the subtree to `schemas/crowler-config-schema.json` with
`additionalProperties: false`, correct required fields, enums and bounds, and
source/resource mutual exclusion. Add a disabled `config.default` entry and one
safe operator example. Update the configuration reference with default-disabled
behavior, strict-error semantics, restart requirements, fingerprinting and
alias derivation. Validate the real YAML/JSON and JSON Schema tooling.

## Tradeoffs and exclusions

- Previously ignored typos inside this authorization subtree will become
  startup errors. That is intentional fail-closed behavior.
- Changes require restart; there is no dynamic reload.
- The strict parser and duplicate-key detection add implementation and test
  complexity, especially for JSON.
- B1 does not register a production proposal tool. B2/B3 remain pending, so no
  authenticated proposal admission is activated.
- B4 is absent, so approval-time current-policy revalidation is not claimed.
- G3 is absent: no schema 1.17 deployment, no shared/production migration and no
  global SQLite `_foreign_keys=on` change. SQLite FK-off remains a deployment
  limitation for authorization.
- No dispatcher, dispatch claim, `MCPMutationDispatchAttempts` insert, retry,
  reconciliation worker, execution endpoint or remote mutation is authorized.
- Release `v2.3.0`, schema `1.17` and writer generation
  `verified-ownership-v1.17` remain unchanged.

## After approval

Only B1 implementation may proceed: public operator types, strict loaders,
normalization/clone preservation, immutable snapshot, schema/default/docs and
the full negative/concurrency test matrix. Production proposal registration and
all B2/B3/B4/B5/G3 work remain disabled until separately approved.

**Current result: BLOCKED ON B1.**
