# WP-04C2H B3 Decision

## Status

**APPROVED — B3 only.**

## Approval record

- Approver: repository maintainer (user) in the OpenCode session.
- Date: 2026-11-13 (UTC).
- Exact instruction: `OK B2 applied, tested, reviewed and approved, please
  continue to B3 and also continue to test live build using docker.`
- Baseline HEAD: `408fc80840527c53c3b301771a89c974fb8f42f6` (B2 applied).
- Scope: independent MCP proposal operation authority and production
  registration of inert, durable proposal-only tools.

The approval message did not include a personal name. It is recorded as the
repository maintainer's explicit instruction to proceed with B3.

## Approved implementation

- Separate MCP proposal authority from native crawl `MutationAllowed` and
  native crawl `AllowedOperations`.
- Require an explicit per-run MCP alias allowlist and an exact matching
  operator policy operation.
- Register one exact proposal adapter per approved MCP operation; no
  first-match descriptor selection.
- Require authenticated principal, agent identity/capability/trust/contract,
  exact server/tool/alias, frozen transport/schema identity, source/resource
  scope, durable schema-1.17 capability, quotas and deadlines.
- Return only inert proposal references; never call a mutating MCP tool.
- Keep `MCPMutationDispatchAttempts` empty and no dispatcher/retry/execution
  path.
- Test the production path live with a controlled MCP fixture and fake model.

## Not approved by B3

B4 approval-time current-policy revalidation, B5 denied-admission audit,
G3 schema deployment/SQLite FK changes, dispatch, remote mutation and any
external execution remain pending or prohibited.

Schema remains 1.17 with writer generation `verified-ownership-v1.17`.
No new schema release is introduced.
