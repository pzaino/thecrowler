# WP-04C2M Administration Authority

## Scope

Deny and revoke remain record-only terminal operations. They never execute,
claim, retry or dispatch a mutation.

## Production callers inventoried

- `services/api/mcp_mutation.go:mcpMutationDenyHandler`
- `services/api/mcp_mutation.go:mcpMutationRevokeHandler`
- `pkg/agent/mcp_mutation_store.go:Deny` (new typed store method)
- `pkg/agent/mcp_mutation_store.go:Revoke`
- `pkg/database/mcp_mutation.go:DenyMCPMutationProposalAuthorized`
- `pkg/database/mcp_mutation.go:RevokeMCPMutationApprovalAuthorized`
- `pkg/database/mcp_mutation.go:AuthorizeMCPMutationAdministrationTx`

## Contract

The API handler still authenticates the request, resolves the current account
and checks proposal fingerprint/corruption before entering the store. The
authority that can race is now rechecked inside the terminal transaction:

`source rows sorted -> actor user -> actor admin grant -> quota -> proposal`
then the existing deny/revoke transition and audit write.

`AuthorizeMCPMutationAdministrationTx`:

1. rejects zero actor;
2. validates source/external scope and rejects mixed/empty scope;
3. locks all source rows in ascending order and checks they exist and are not
   deleted;
4. locks the actor account;
5. reads and locks the actor's current admin grant when present;
6. for external-resource proposals requires current admin;
7. for source-scoped proposals allows current admin, or requires current
   verified ownership of every source;
8. returns bounded fail-closed errors.

This preserves the emergency rule: a current administrator can deny or revoke
after ownership transfer or principal disablement. A former non-admin owner
cannot.

## Agent boundary

`DatabaseMCPMutationAdministrationAuthorizer` maps any authority failure to
`ErrMCPMutationAdministrationForbidden`, except cancellation (preserved) and
database/authority unavailability (`ErrMCPMutationStoreUnavailable`).

The API maps the forbidden error to HTTP 403. Public bodies do not contain source
IDs or raw database errors.

## Typed store

`DatabaseMCPMutationApprovalStore` now has:

- `AdministrationAuthority` (mandatory; constructor installs the database
  authorizer);
- `Deny(ctx, proposalID, actor MCPMutationPrincipal, reason)`;
- existing `Revoke` uses the same in-transaction authority.

A nil authority fails closed. The API handler no longer relies on the
pre-transaction `authorizeMCPMutationAdministration` helper for deny/revoke.

## Tests

`pkg/agent/mcp_mutation_administration_authorization_test.go`:

- current owner may deny;
- unrelated user cannot deny;
- admin may deny after transfer;
- disabled actor cannot deny;
- revoked admin role cannot deny;
- admin may revoke after proposal principal is disabled;
- concurrent deny is idempotent and leaves one denied state;
- cancellation and DB outage fail closed;
- zero dispatch attempts.

PostgreSQL `TestPostgresMCPMutationApprovalAuthorityLockOrder` also covers:

- admin deny after source transfer;
- non-admin deny after transfer rejected;
- admin revoke after principal disable;
- role revocation contending with deny;
- zero dispatch attempts.

## Compatibility

The exported database helpers `DenyMCPMutationProposal` and
`RevokeMCPMutationApproval` were replaced by
`DenyMCPMutationProposalAuthorized` and
`RevokeMCPMutationApprovalAuthorized`. In-tree production callers now go through
the typed store and supply the mandatory authority. Database tests use explicit
allow callbacks. There is deliberately no insecure no-authority wrapper.
External Go package consumers must migrate to the authorized variants. No HTTP
route, request/response field, operator policy field or database schema changed.

## Preserved semantics

- No public HTTP route or request/response field changed.
- No new role or privilege policy.
- Deny/revoke remain available to current administrators in an emergency.
- No mutation execution or dispatch attempt is introduced.
