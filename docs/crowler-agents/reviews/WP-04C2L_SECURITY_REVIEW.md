# WP-04C2L Security Review

## Authority chain after this work

Authenticated HTTP user
→ current `Users` account/grant rows
→ trusted agent manifest identity
→ current operator policy operation
→ frozen MCP inventory descriptor
→ current policy identity and exact operation
→ current verified source ownership and administrator authority
→ durable inert proposal
→ transactional record-only approval
→ audit.

Each authority source is trusted:

| Link | Trusted source | Lower-trust data rejected |
| --- | --- | --- |
| Approver | Middleware `auth.Identity` + current DB re-resolution | Request JSON, model, manifest, arbitrary map |
| Proposal principal | Durable proposal + current DB re-resolution | Event payload, model output |
| Source ownership | Locked `Sources` + `SourceOwnership` rows | `allowed_sources`, arguments |
| Admin role | Locked `UserRoles`/`AuthRoles` rows | Token role without current grant |
| Policy | API process snapshot | Stored proposal snapshot |
| Inventory/agent | API process snapshot/registry | Proposal fields |
| Approval | Durable approval transaction | HTTP request fields |

## Transactional approval

The approval authority runs inside the approval transaction before quota and
proposal locks. It locks sources, users and admin grants in a documented order,
then re-resolves both identities. A concurrent disable, delete, role revocation
or ownership transfer either commits before the authority and is observed, or
waits for the approval transaction.

Replayed approvals are rechecked; an existing active approval is not returned
successfully after the approver loses authority.

## Lock order

Documented in `WP-04C2L_LOCK_ORDER.md`:

`source(s) → users (ascending) → admin grants → quota → proposal → approval/audit`.

Admission was moved to the same source/user-before-quota order. PostgreSQL tests
cover transfer-first, approval-first and role-revocation contention.

## Network and test isolation

- E2E resources are run-labelled and cryptographically unique.
- PostgreSQL publishes no host port.
- API/Events bind only `127.0.0.1` with dynamic ports.
- Fake models are internal Compose services.
- Cleanup removes only exact label-verified resources.
- A foreign project, populated work root, stale PID file and stale image are
  preserved/rejected.
- Full E2E and safety tests ran on a disposable Docker-in-Docker daemon.

## Redaction

- Approval errors expose stable codes, not source ownership details, policy
  resources, tokens or raw DB errors.
- Audit detail remains bounded and does not contain raw argument values.
- E2E artifacts are sanitized; the final scan found no password, DSN, JWT,
  bearer token, `source_id` value or `fixture-resource` value.
- Build logs are sanitized before upload.

## Disabled/mismatched policy

- Disabled/missing current policy fails approval with 503.
- ID/version/hash/operation mismatch fails with 409.
- API and Events process snapshots are independent; deployment requires
  restarting both. A mismatch fails closed.
- SIGHUP does not hot-reload MCP authority.

## Database outage and cancellation

- Authority callback errors abort the approval transaction.
- Cancellation/deadline errors are preserved, not converted to success.
- DB outage tests assert no approval row and no dispatch state.
- The E2E outage case restarts the DB and verifies zero approvals.

## Direct database ownership edits

Application write paths that update `Sources.usr_id` and `SourceOwnership`:

- `VerifySourceOwnershipContext` (`pkg/database/source_ownership.go:396`);
- `recordPolicySourceOwnershipTx` (`pkg/database/source_upsert_policy.go:247`);
- `insertAuthenticatedSourceOwnership` (`pkg/database/source_authenticated.go:190`);
- migration/setup scripts.

The admission/approval serialization assumes every concurrent writer takes the
`Sources` row lock. Direct out-of-band `UPDATE`/`DELETE` by a database
administrator or another tool can bypass that protocol and is not serialized.
WP-04C2L documents this and does not add global triggers or change production
privileges. A future enforcement decision would need a separate review.

## API agent registry freshness

The API freezes its agent registry at startup. An agent uploaded after startup
is not approvable until API restart. This is fail-closed availability, not an
authorization bypass. Deployment/readiness must check that API and Events loaded
the same agent identity and policy generation. No hot reload is enabled.

## Zero external mutation

- No dispatcher, worker, claim or `MCPMutationDispatchAttempts` insert.
- No remote mutating `tools/call`.
- No automatic execution after approval.
- No retry/reconciliation machinery.
- Execution backend remains `none`.
- E2E asserts zero mutation fixture calls, zero dispatch rows and no dispatch
  states.

## Findings by evidence class

Source-verified:

- approval authority and lock order;
- no production dispatch path;
- schema and writer generation unchanged;
- test-only harness changes.

Test-verified:

- unit authority matrix;
- PostgreSQL 15/17 lock-order and restore tests;
- full isolated E2E with 60 checks;
- harness safety tests on host and dind;
- no secret patterns in artifacts.

Remaining risks:

- deny and revoke still perform the actor authority check in the API handler
  before the deny/revoke transaction. They are terminal, safety-reducing actions,
  not execution grants, but a role/ownership change in that narrow window is not
  observed transactionally. A future hardening should add the same in-transaction
  administration authority used by approval issuance;
- `lockUserAdminRoleTx` locks grant and role rows; a direct
  `DELETE FROM AuthRoles ... ON DELETE CASCADE` may acquire them in the opposite
  order and should be covered by a future concurrency test;
- approver/principal authorization is now transactional, but a future in-process
  caller must still use the typed `Approve`/`Issue` path;
- approval binds configured transport identity, not every arg/env/secret or live
  schema; future dispatch must use the full execution profile;
- uploaded agents require API restart for approval;
- direct DB ownership edits bypass application locks unless database
  privileges/triggers are separately hardened;
- E2E safety is proven on the tested Docker versions; Docker Engine defects are
  out of scope.
