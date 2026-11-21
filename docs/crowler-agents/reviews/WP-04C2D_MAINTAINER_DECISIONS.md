# WP-04C2D Maintainer Decisions

## Status

Phase A safe preflight repairs are implemented and tested:

- VDI legacy-audio negative control made deterministic;
- PostgreSQL integration test no longer creates sentinel DDL in the
  maintenance database;
- operator policy example alias corrected and validated;
- SQLite foreign-key runtime behavior proven without changing runtime DSN
  semantics.

Phase B/C/D production proposal work is **withheld**. Gates B1 (public policy
contract) and B2 (trusted principal provenance) are not approved, and schema
deployment/SQLite FK enforcement (G3) is separately unresolved. No public
config field, production proposal registration, dispatcher or remote mutation
was added.

## Decisions still required

### 1. Public MCP mutation policy contract (Gate B1)

Approve or reject the exact `mcp.mutation_policy` contract in
`WP-04C2D_POLICY_CONTRACT.md`, including:

- location, Go/YAML/JSON tags, defaults and unknown-field handling;
- derived `MCPToolAlias` rule and whether an alias field is exposed;
- operation fields, exact server/tool/agent/transport/scope/argument/TTL/quota
  validation;
- policy fingerprint/snapshot and reload/staleness semantics;
- JSON Schema, config.default and negative fixtures;
- explicit `require_approval:true`, `execution_backend:none`,
  `audit_required:true`, default-disabled behavior.

No public config changes may be made before this approval.

### 2. Trusted principal provenance (Gate B2)

Approve the user-triggered principal propagation mechanism in
`WP-04C2D_IDENTITY_PROVENANCE.md`:

- resolve `auth.Identity` with `ResolveActiveIdentity` at the API/event
  boundary;
- pass a non-zero `MCPMutationPrincipal` explicitly into the tool-loop/goal
  runtime;
- bind it to the actual run and effective call, not manifest owner/event/model
  data;
- revalidate account and source ownership at proposal admission and approval.

Background/event-triggered MCP proposals remain denied until a separately
approved service-principal contract exists.

### 3. Separate MCP operation allowlist

Approve a production path that populates the MCP proposal alias allowlist from
the MCP mutation policy, not from the native crawl `MutationAllowed` flag or
`spec.MutationPolicy.OperationsSet()`.

### 4. Current-policy revalidation at approval

Approve whether policy hash/version must match the current approved policy at
approval time and how a policy change reopens/blocks approvals. Missing or
invalid current policy must fail closed.

### 5. SQLite foreign-key decision (Gate G3)

Current runtime SQLite pooled connections default to `foreign_keys=0`; the
schema declares FKs but does not enforce them on every connection. Choose:

- enable `_foreign_keys=on` and run full regressions for existing data;
- enforce MCP relationships in application transactions only; or
- retain current behavior with an operational caveat.

Do not change global SQLite semantics without this decision.

### 6. Schema 1.17 MCP capability deployment (Gate G3)

Approve the additive migration/clean-install deployment, backup, privileges,
capability-marker verification, upgrade from existing 1.17 and rollback plan.
No shared/production DDL may be run before this approval.

### 7. CI rerun

The VDI negative control is fixed locally (Node 24 count 20; Node 20/22 single
runs). The GitHub `build` check must be rerun after an authorized push. Do not
claim green CI until it is observed.

### 8. Dispatch remains blocked

No dispatcher, dispatch claim, execution endpoint, retry, reconciliation or
remote mutation is authorized. A separate milestone must review the first
external backend and its idempotency/uncertainty contract.

## Exact stopping point

Source activation stops before:

- `pkg/config` / public JSON Schema / config.default mutation policy fields;
- production `MCPMutationProposalTool` registration and principal propagation;
- any database migration on shared/production systems;
- any external mutation execution or dispatch-attempt insert.

The documented Phase A changes may be reviewed and applied independently.
