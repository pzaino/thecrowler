# WP-04C2K Current-Policy Revalidation

## Purpose

A stored proposal is evidence of what was requested under a past policy. It is
not proof that the operation remains authorized today. New approvals must be
evaluated against the current trusted operator policy and the current operator
inventory.

## Authoritative boundary

`DatabaseMCPMutationApprovalStore.Issue`
(`pkg/agent/mcp_mutation_store.go:444`) is the single production approval
issuance path. `mcpMutationApproveHandler`
(`services/api/mcp_mutation.go:561`) delegates to it through
`mcpMutationApprovalStoreFactory` (`services/api/mcp_mutation.go:556`). The
factory is a test seam; production uses
`agentpkg.NewDatabaseMCPMutationApprovalStore`.

Placing the check in the durable store, rather than only in the HTTP route,
prevents another production caller from issuing an approval without
revalidation.

## Current-policy evaluator

`EvaluateMCPMutationApproval`
(`pkg/agent/mcp_mutation_approval_policy.go:72`) validates:

1. Current policy `Validate()` succeeds and is enabled.
2. `proposal.PolicyID == current.Policy.PolicyID`.
3. `proposal.PolicyVersion == current.Policy.Version`.
4. `proposal.PolicySHA256 == current.Policy.SHA256()`.
5. The exact operation still exists for alias, server and raw tool.
6. Approval, audit and `execution_backend: none` requirements still hold.
7. Destructive classification still matches.
8. The agent is still in `allowed_agents`.
9. The current agent identity still exists, belongs to the same owner, grants
   every required capability, meets the trust floor and does not forbid the
   tool by contract.
10. The current inventory still enables the server, transport and transport
    identity, and still allows the agent.
11. The current inventory still approves the exact mutation tool and alias.
12. The policy still allows the proposal transport.
13. The proposal remains inside the current source or resource scope.
14. Canonical arguments still match the stored canonical bytes and hash, and
    still satisfy current argument fields and size limits.
15. Current scope extraction reproduces the stored source/resource scope.
16. The proposal fingerprint is still valid.

The evaluator does not trust the stored policy snapshot as proof of current
authorization. The snapshot is still parsed and hash-checked by
`mcpProposalFromDBRow` to reject corruption, but the authorization decision is
made only from the current process policy and inventory.

## Stored fields

The database proposal row does not persist a redundant `AuditRequired` column.
`Issue` checks the current operation's `AuditRequired` and the policy hash binds
that flag to the stored snapshot. The proposal row does persist
`ApprovalRequired` and `ExecutionBackend`; both are checked directly.

## Fail-closed matrix

| Condition | Result |
| --- | --- |
| Current policy missing or disabled | `ErrMCPMutationPolicyUnavailable`, HTTP 503 |
| Current policy cannot be validated | unavailable, HTTP 503 |
| Policy ID differs | `ErrMCPMutationPolicyMismatch`, HTTP 409 |
| Policy version differs | mismatch, HTTP 409 |
| Canonical hash differs with same ID/version | mismatch, HTTP 409 |
| Operation removed or renamed | mismatch/operation unauthorized, HTTP 409 |
| Source, resource or transport changed | mismatch/operation unauthorized, HTTP 409 |
| Agent restrictions, capability or trust changed | operation unauthorized, HTTP 409 |
| Inventory server/tool no longer approves the mutation | operation unauthorized, HTTP 409 |
| Stored policy snapshot or arguments corrupt | corrupt proposal, HTTP 503 |
| Proposal fingerprint or request fingerprint differs | conflict, HTTP 409 |
| Proposal expired | gone, HTTP 410 |
| Approval store or database unavailable | unavailable, HTTP 503 |

No denial path writes an approval row or changes the proposal to `approved`.

## API process policy snapshot

`services/api/main.go:83` defines `initializeMCPAuthority`. `initAll` calls it
after configuration load. It:

- installs the operator MCP policy and inventory with
  `agt.SetMCPOperatorConfigForApproval`;
- loads the trusted agent registry from `config.Agents`;
- freezes both for the API process.

`SetMCPOperatorConfigForApproval`
(`pkg/agent/mcp_config.go:69`) uses the same normalization, policy build and
inventory cross-validation as Events, but skips local file existence checks for
stdio commands, token files, CA files and working directories. The API never
launches an MCP server. This keeps approval deployment independent while still
validating command shape, absolute paths, shell rejection, transports, auth
shape, limits and policy/inventory agreement.

SIGHUP re-runs `initAll` for other API configuration, but
`mcpAuthorityReady` prevents MCP authority hot reload. A policy change requires
restarting both API and Events. The E2E fixture proves that an API on one policy
and Events on another fails closed.

## Concurrency and ordering

The policy evaluator runs before the database approval transaction. Policy
state is frozen for the lifetime of the API process, so it cannot change
mid-request. The durable issuance transaction then locks the proposal row and
handles existing active approvals, expiry, reapproval and conflicts. Concurrent
issuance tests confirm one approval row and one `inserted=true` result.

## Tests and live evidence

- `TestEvaluateMCPMutationApprovalAcceptsCurrentPolicy`
- `TestEvaluateMCPMutationApprovalFailsClosed` (disabled policy, ID, version,
  hash, operation, agents, capabilities, trust, contract, inventory, transport,
  scope, arguments, fingerprint)
- `TestApprovalStoreFailsClosedWhenPolicyChanges`
- `TestApprovalStoreConcurrentIssueIsIdempotent`
- `TestMCPMutationApprovalAPIApproveRevokeDeny` (duplicate approval is
  idempotent)
- E2E: policy ID mismatch, same-version canonical hash change, disabled current
  policy, wrong proposal hash, corrupt policy snapshot, expired proposal.

All negative cases observed zero approvals and zero mutation dispatch attempts.
