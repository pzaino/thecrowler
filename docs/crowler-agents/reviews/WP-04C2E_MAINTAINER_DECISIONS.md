# WP-04C2E Maintainer Decisions

## Status summary

Gate A (safe preflight repairs) is implemented and tested. No production
activation decision was approved. The OpenCode task itself is not an approval,
so every gated item remains **pending** until a maintainer records an explicit
decision here or in an equivalent tracked decision.

| Gate | Decision | Approver | Scope |
| --- | --- | --- | --- |
| A: safe repairs | complete | — | alias/inventory test, parser evidence, CI step, docs reconciliation |
| B1: public policy contract | **approved in WP-04C2F** | repository maintainer (user) | public config/schema/default/loader/examples |
| B2: trusted user principal | **pending** | none recorded | authenticated principal provenance |
| B3: independent MCP authority | **pending** | none recorded | separate MCP alias allowlist |
| B4: current-policy revalidation | **pending** | none recorded | approval-time policy identity |
| B5: denied-admission audit | **pending** | none recorded | bounded audit/retention |
| G3: deployment and SQLite FK | **pending** | none recorded | schema capability deployment, FK enforcement |
| GitHub CI rerun | **pending** | none recorded | fresh workflow result after push |

## B1: public `mcp.mutation_policy` contract

**Approved in WP-04C2F.** The approval is recorded in
`WP-04C2F_B1_DECISION.md`; the exact scope is the WP-04C2F B1 contract. The
pre-approval text below is retained as historical context. It concerns the
contract in `WP-04C2D_POLICY_CONTRACT.md`, corrected by
`WP-04C2E_CONFIG_STRICTNESS.md`.

The decision covers:

- top-level `mcp.mutation_policy` location and operator-only ownership;
- Go/YAML/JSON types/tags, defaults and unknown-field behavior;
- dedicated strict subtree decoding for local, remote, YAML and JSON paths;
- derived alias via `MCPToolAlias`, exact operation identity and bounds;
- `require_approval: true`, `execution_backend: none`,
  `audit_required: true`, default disabled;
- policy fingerprint/snapshot, reload and stale-policy behavior;
- JSON Schema, `config.default`, reference docs and fixtures.

At the time of WP-04C2E no public config field, schema entry, default, loader
or public example was added. WP-04C2F subsequently implemented B1; read-only MCP
behavior remains unchanged.

## B2: trusted user principal provenance

**Pending.** The maintainer must approve or reject the mechanism in
`WP-04C2E_PRINCIPAL_AND_PROPOSAL.md` and
`WP-04C2D_IDENTITY_PROVENANCE.md`.

The decision covers:

- resolving `auth.Identity` at the real authenticated entrypoint;
- `auth.ResolveActiveIdentity` current-state recheck;
- nonzero numeric user ID, current username and issuer;
- explicit typed propagation into `ToolLoopConfig.Principal` and
  `goalRunConfig.Principal`;
- no manifest `owner`, event payload, model output, tool argument, inherited
  step or retry metadata fallback;
- account/source ownership recheck at proposal admission.

Background/event-triggered runs remain denied. A separate service-principal
contract is required before they may propose mutations.

No production principal plumbing or proposal registration was added.

## B3: independent MCP operation authority

**Pending.** The maintainer must approve a separate MCP proposal alias
allowlist sourced from the MCP operator policy.

The decision must confirm that:

- native `MutationAllowed` is not MCP authority;
- `spec.MutationPolicy.OperationsSet()` is not MCP authority;
- `crawlRuntime != nil` is not MCP authority;
- discovered effect/`allow_mutation_proposal` is classification only;
- the MCP alias allowlist, exact policy operation, trusted principal and
  durable capability are all required.

The current goal runtime sets native crawl authority from
`spec.MutationPolicy.OperationsSet()` and `crawlRuntime != nil`. No production
MCP registration path exists to conflate them today, and none may be added
until B3 is resolved.

## B4: current-policy revalidation at approval

**Pending.** The maintainer must decide whether approval requires the current
`(policy_id, version, hash)` to match the stored proposal snapshot, and what
happens when the policy is disabled, removed or changed after proposal
creation.

Recommended fail-closed position: a missing, invalid, disabled or changed
current policy blocks new approval until an explicit operator reapproval
decision. Existing rows remain inert historical records. A stale snapshot must
never silently authorize a newly disabled operation.

No approval-time current-policy revalidation was added in this work package.

## B5: denied-admission audit and retention

**Pending.** The maintainer must approve:

- bounded, stable-reason-code denial events at trusted boundaries;
- actor provenance and tenant-safe fields;
- retention, redaction and expiry maintenance;
- behavior during database outage so a denial never becomes success.

No new denied-admission audit path or retention behavior was added.

## G3: schema deployment and SQLite foreign keys

**Pending.** No shared or production DDL was executed. Schema remains `1.17`
with writer generation `verified-ownership-v1.17`.

Two separate approvals are required:

1. Deployment of the additive 1.17 MCP capability (or clean install), including
   backup, privileges, capability-marker/checksum verification, upgrade from an
   existing 1.17 database and rollback that preserves historical rows.
2. SQLite foreign-key behavior. Current pooled connections default to
   `PRAGMA foreign_keys = 0`; the schema declares FKs but does not enforce them
   on every connection. Options are enabling `_foreign_keys=on` with full
   regression, enforcing relationships in application transactions, or
   retaining the current behavior with an explicit operational limitation.

No global SQLite DSN or schema behavior was changed.

## GitHub CI

The PostgreSQL workflow now contains a dedicated MCP step that proves `RUN` and
`PASS` and fails on `SKIP`. A local `postgres:17` simulation passed. The patch
was not pushed, so GitHub has not executed the step and no job URL exists.
A local pass is not GitHub evidence.

The VDI legacy-audio deterministic fix from WP-04C2D remains intact and passed
repeatedly in this work package.

## Exact stopping point

Source activation stops before:

- public `pkg/config`/JSON Schema/`config.default` mutation-policy changes;
- production MCP proposal tool registration or principal propagation;
- a separate MCP alias authority path;
- current-policy approval revalidation and denied-admission audit;
- any shared/production database migration or SQLite FK change;
- any dispatcher, dispatch claim, dispatch-attempt insert, retry,
  reconciliation worker, execution endpoint or remote mutation.

The safe preflight patch may be reviewed, applied or rejected independently.
