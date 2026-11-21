# WP-04C2K Admission Provenance

## Problem

A manual run resolves an authenticated principal and source ownership at
request entry. A long-running agent can outlive that state. Ownership can be
revoked or transferred, an account can be disabled or deleted, and a database
outage can occur between discovery and durable admission.

The proposal must not be admitted on the strength of the request-entry snapshot
alone.

## Durable admission boundary

`DatabaseMCPMutationProposalStore.Admit`
(`pkg/agent/mcp_mutation_store.go:277`) now requires a non-nil
`MCPMutationAdmissionAuthorizer`. A nil authorizer fails closed with
`ErrMCPMutationStoreUnavailable`.

The authorizer callback is executed inside the proposal admission transaction by
`AdmitMCPMutationProposalAuthorizedAt`
(`pkg/database/mcp_mutation.go:802`). The callback runs before replay handling
and before the proposal insert (`pkg/database/mcp_mutation.go:856`). An authorization error rolls back the
transaction, so no proposal row is inserted and a replay cannot report success
after the account or ownership has changed.

The legacy `AdmitMCPMutationProposalAt` remains for internal/tests that do not
need authority; production proposal stores always install the database
authorizer in `NewDatabaseMCPMutationProposalStore`.

## Transaction-bound authorizer

`DatabaseMCPMutationAdmissionAuthorizer`
(`pkg/agent/mcp_admission_authorization.go:29`) performs:

1. Validate the trusted principal fields.
2. Reject a proposal with no scope or with mixed source/resource scope.
3. Lock the current `Users` row `FOR SHARE` on PostgreSQL and reject missing,
   disabled or deleted accounts (`lockMCPPrincipalTx`,
   `pkg/agent/mcp_admission_authorization.go:124`).
4. Re-resolve the active identity through `auth.ResolveActiveIdentity` using a
   transaction-backed store, so account and grant reads share the admission
   transaction.
5. Reject any subject, username or issuer mismatch.
6. Rebuild the crawl principal from the freshly resolved roles and scopes.
7. For every affected source, call the existing
   `cdb.AuthorizeSourceMutationTx`, which locks the source row and verifies
   current verified `SourceOwnership` plus current administrative grants.
8. Fail closed on any database or lookup error.

`SourceIDs` is bounded by the mutation scope: a mutation call carries one
source. The extra work is therefore a small, fixed number of indexed lookups.

## Ownership race

`AuthorizeSourceMutationTx` locks the `Sources` row and reads the matching
`SourceOwnership` row in the same transaction. The application's ownership
transfer path (`VerifySourceOwnershipContext`) locks the same source row before
updating `Sources.usr_id` and upserting provenance. Transfer and admission are
therefore serialized on the source row: a transfer either commits before
admission (and is observed) or waits until the admission transaction finishes.

For source-scoped proposals the admission authorizer follows the same lock
order as transfer: it resolves the principal for current roles, then acquires
the source and user locks through `AuthorizeSourceMutationTx`, then re-resolves
the principal under those locks. It does not take a `Users` share lock before
the source lock, which avoids the share-to-update upgrade and the lock-order
inversion against ownership transfer. External-resource proposals have no source
row and lock the account directly.

A direct out-of-band `DELETE`/`UPDATE` on `SourceOwnership` that bypasses the
application's source lock is outside that serialization. That remains an
operator/database-integrity assumption, not a model-controlled path.

## Principal binding

The trusted principal is still created only by
`MCPMutationPrincipalFromAuthIdentity` from a freshly resolved authenticated
identity, carried in `AgentExecutionContext.MCPMutationPrincipal`, and bound to
tool runtime by typed context. It is never read from model arguments, event
payloads, manifests or the `allowed_sources` list.

`toSourceIDArg` was extended to accept `json.Number` because canonical decoded
proposal arguments use JSON numbers. This is an argument-shape fix, not a new
authority source.

## External-resource proposals

Admission rejects a proposal that mixes source and external-resource scope and
requires one scope to be present. Source-scoped proposals receive the full
ownership recheck. External-resource proposals revalidate the active principal
and exact policy scope at approval; administrator-only external-resource
approval remains enforced by the API approval contract. No external-resource
proposal can execute in this work package; `execution_backend` remains `none`.

## Test matrix

`pkg/agent/mcp_admission_authorization_test.go` covers:

- account disabled between run start and admission;
- account deleted between run start and admission;
- ownership revoked;
- ownership transferred;
- ownership provenance changed to another user (unverified for the principal);
- forged principal argument rejected;
- background run without an approved service principal rejected;
- database outage during admission rejected as store-unavailable;
- replay of an already admitted proposal rejected after the account is
  disabled;
- replay rejected after source ownership is transferred;
- unchanged principal and ownership admitted as an inert proposal.

Every denied case asserts zero proposal rows. The successful case asserts state
`approval_required`, source scope `42`, and no execution.

The live E2E fixture additionally transfers source ownership after proposal
creation, then starts a new manual run. That run reaches the agent, is denied at
durable admission, writes `mcp_mutation_admission_denied`, and inserts no
proposal.
