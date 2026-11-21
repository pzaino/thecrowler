# WP-04C2L Approval Service

## Contract

Approval remains record-only. The service issues or reuses an active approval
record and moves the proposal to `approved`. It never dispatches, never writes a
dispatch attempt and never satisfies goal completion.

## Typed command

`MCPMutationApprovalCommand`
(`pkg/agent/mcp_mutation_store.go:341`) accepts:

- authenticated `auth.Identity` from middleware;
- proposal ID and verified proposal fingerprint;
- identity version;
- requested expiry;
- max executions;
- bounded reason.

`DatabaseMCPMutationApprovalStore.Approve`
(`pkg/agent/mcp_mutation_store.go:495`) derives the `MCPMutationPrincipal`
only through `MCPMutationPrincipalFromAuthIdentity`. Request JSON, manifests,
model output and arbitrary maps cannot supply the approver.
`IdentityVersion <= 0` is rejected.

## Explicit current snapshot

`MCPMutationApprovalSnapshot` contains the current policy, inventory and agent
lookup. The API handler installs it with `SetSnapshot`
(`pkg/agent/mcp_mutation_store.go:476`) before calling `Approve`:

```go
approvalStore.SetSnapshot(agentpkg.MCPMutationApprovalSnapshot{
    Policy:    agentpkg.CurrentMCPMutationPolicy(),
    Inventory: agentpkg.CurrentMCPOperatorConfig(),
    Agent:     agentpkg.CurrentAgentIdentityForMCPApproval,
})
```

`SetSnapshot` preserves test-injected non-nil fields. Production fields are nil
until installed, so the API uses its frozen process snapshot.

## Transactional authority

`Issue` pre-validates the durable proposal and current policy, then calls
`cdb.IssueMCPMutationApprovalAuthorized`
(`pkg/database/mcp_mutation.go:1273`).

The mandatory authority callback runs inside the approval transaction, after
the proposal is loaded and before quota/proposal locks:

1. `DatabaseMCPMutationApprovalAuthorizer.AuthorizeMCPMutationApproval`
   (`pkg/agent/mcp_mutation_approval_authorization.go:31`) builds a typed
   `MCPMutationApprovalAuthorityRequest`.
2. `AuthorizeMCPMutationApprovalTx`
   (`pkg/database/mcp_mutation.go:1150`) locks source rows, then user rows in
   ascending ID order, then administrative grant rows.
3. It verifies:
   - approver account is current, enabled and non-deleted;
   - proposal principal is current, enabled and non-deleted;
   - source-scoped principal is verified owner or current admin;
   - approver is verified owner or current admin;
   - external-resource approver is a current admin;
   - a source or provenance row is present and not deleted.
4. The agent authority re-resolves both identities under the locks and rejects
   username/issuer changes.
5. Policy identity, exact operation, agent binding, inventory, scope,
   arguments, transport, `execution_backend: none`, approval/audit flags and
   proposal fingerprint were already evaluated against the current snapshot.
6. The database then locks quota and proposal rows, checks identity version,
   fingerprint, expiry and state, reuses a usable active approval only after
   authority, or inserts one approval and the audit event atomically.

## Fail-closed behavior

| Condition | Result |
| --- | --- |
| Missing authority callback | `ErrMCPMutationApprovalAuthorityUnavailable`, HTTP 503 |
| Approver disabled/deleted | `ErrMCPMutationApprovalForbidden`, HTTP 403 |
| Approver role revoked | forbidden, 403 |
| Approver is not owner/admin | forbidden, 403 |
| Proposal principal disabled/deleted | forbidden, 403 |
| Source ownership revoked/transferred/unverified | forbidden, 403 |
| Forged typed identity | `ErrMCPMutationPrincipal`, 403 |
| Policy mismatch/hash/operation change | mismatch/operation unauthorized, 409 |
| Disabled/unavailable policy | 503 |
| Proposal fingerprint/version mismatch | conflict, 409 |
| Expired proposal | gone, 410 |
| Cancellation/deadline | context error, no row |
| DB outage | store unavailable, no row |

A denied authority check never returns a reused active approval and never
changes proposal state.

## HTTP mapping

`mcpMutationErrorCode` maps
`agentpkg.ErrMCPMutationApprovalForbidden` to HTTP 403. Existing 401/409/410/503
mappings are unchanged. Public bodies contain stable error text, not source
ownership details, policy resources, tokens or raw database errors.

## Tests

- `TestMCPMutationApprovalAuthorityRejectsChangedAuthority`
- `TestMCPMutationApprovalAuthorityAcceptsOwnerAndAdmin`
- `TestMCPMutationApprovalReplayRevalidatesApprover`
- `TestMCPMutationApprovalServiceRequiresTrustedIdentity`
- `TestMCPMutationApprovalAuthorityPreservesCancellation`
- existing approve/revoke/deny API tests
- `TestPostgresMCPMutationApprovalAuthorityLockOrder`
- full isolated E2E positive approval + ownership/principal negatives

## Compatibility

No HTTP route, request/response field, operator policy field, database schema or
in-memory production fallback changed.

The exported database helper `IssueMCPMutationApproval` was replaced by
`IssueMCPMutationApprovalAuthorized`. The repository had one production caller
(the approval store) and no external callers in-tree. Package consumers must
migrate by supplying an `MCPMutationApprovalAuthority`; there is deliberately no
insecure no-authority wrapper. In-tree database tests use a test-only helper
that supplies an explicit allow callback.

`MCPMutationApprovalRequest` gained `IdentityVersion`. Callers that omit it still
work for lower-level tests, but the typed production path requires a positive
version. The store's `Issue` remains available for lower-level tests and still
enforces the mandatory authority callback.
