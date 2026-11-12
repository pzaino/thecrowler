# WP-04C2-C Maintainer Decisions

## Status

The P0/P1 correctness and test-safety fixes are implemented and tested. The
additive schema 1.17 capability and operator API remain **not approved for
deployment**. The production MCP mutation proposal producer remains disabled
because trusted principal provenance and a public operator policy contract are
not approved. No dispatcher or remote mutation was enabled.

## Decisions required before deployment or producer wiring

### 1. Schema 1.17 capability deployment

Approve or reject deployment of:

- `MCPMutationProposals`
- `MCPMutationApprovals`
- `MCPMutationDispatchAttempts` (unused)
- `MCPMutationAudit`
- `MCPMutationRunQuota`
- `MCPMutationPrincipalQuota`
- `SchemaCapabilities`

Approve the additive migration + clean-install route, the capability marker and
checksum, and the operational requirement that the script is applied explicitly
before the routes are used. `RequiredSchemaVersion` stays `1.17`; no writer
generation change is proposed.

### 2. SQLite foreign-key enforcement

Choose one:

- enable `_foreign_keys=on` in the SQLite runtime DSN and run the full
  regression suite; or
- enforce MCP mutation foreign-key relationships in application transactions
  only and document the divergence; or
- retain the current behavior with an explicit operational caveat.

This patch does not change global SQLite behavior.

### 3. Public MCP mutation policy contract

Approve the minimal operator-only `mcp.mutation_policy` contract proposed in
`WP-04C2C_AUTHORIZATION_MODEL.md` (or provide an equivalent approved contract).
It must include exact server/tool/agent/transport/scope/argument/TTL/quota
policy, default-disabled behavior, validation and tests. No public field was
added in this patch.

### 4. Trusted principal provenance

Decide how an authenticated principal reaches the agent tool loop and goal
runtime:

- user-triggered runs: propagate the resolved `auth.Identity` as
  `MCPMutationPrincipal` at the API/event boundary;
- background/event runs: either approve a service-principal contract backed by
  existing auth semantics or explicitly leave them unsupported for proposals.

Manifest `owner` strings and model output must never become principals.

### 5. Separate MCP proposal allowlist from native crawl mutation

Approve a distinct production path that populates the MCP proposal tool alias
from the approved MCP mutation policy, not from
`spec.MutationPolicy.OperationsSet()` / `crawlRuntime != nil`. The generic
`MutationAllowed` flag is not sufficient MCP authority.

### 6. Current-policy and authority revalidation

Approve the requirement that approval and any future dispatch re-read current
policy, account status, source/resource authority and backend identity. The API
currently checks stored bindings and current account/source authority, but no
production policy loader exists for MCP mutation policy.

### 7. Expiry reconciliation and read model

Choose whether to add a maintenance expiry reaper for active approvals/proposals
or accept the current behavior: reads derive `approval_required` and the next
approval/revocation/denial action persists the transition. Counter consistency
is currently fail-strict rather than automatically repaired.

### 8. Audit and retention

Approve denied-admission audit events and the retention/redaction policy for
`MCPMutationAudit`. Current events cover created, denied, expired, approval
issued/revoked/expired/exhausted and proposal reopened; denied admission
attempts are not yet audited.

### 9. Deployment and rollback

Approve the deployment procedure and backup/rollback plan. Dropping the
capability marker disables the feature without deleting history; dropping the
tables would destroy proposal/approval/audit history and is not recommended
without a backup. No runtime DDL or destructive repair is implemented.

### 10. Dispatch remains blocked

Do not authorize a dispatcher, dispatch claim, execution backend, remote retry
or reconciliation worker in this milestone. A separate review must approve the
first external backend and its authoritative idempotency/reconciliation
contract.
