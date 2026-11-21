# WP-04C2M Lock Order and Database Integrity

## Global order

```
source rows (ascending source_id)
  -> user rows (ascending user_id)
  -> administrative grant rows (same ascending user order)
  -> MCPMutationRunQuota
  -> MCPMutationPrincipalQuota
  -> proposal row
  -> approval/audit writes
```

## Paths

- `VerifySourceOwnershipContext`: source -> users -> verifier grant.
- Proposal admission: authority (sources -> users -> grants) -> quota ->
  proposal insert -> audit.
- Approval issuance: policy evaluation -> authority (sources -> users ->
  grants) -> quota -> proposal -> approval/audit.
- Deny: policy/proposal preflight -> administration authority (sources ->
  actor -> actor grant) -> quota -> proposal -> approval revocation/audit.
- Revoke: approval/proposal preflight -> administration authority -> quota ->
  proposal -> approval/audit.
- `RevokeMCPMutationApproval` / `DenyMCPMutationProposal` no longer perform
  authority only in the API handler.

## Role deletion contention

`TestPostgresMCPMutationAdminRoleCascadeContention` uses a disposable
PostgreSQL database with:

```sql
UserRoles.role_id REFERENCES AuthRoles(role_id) ON DELETE CASCADE
```

One transaction deletes `AuthRoles` admin role 1; a second transaction calls
`lockUserAdminRoleTx` for the same actor. Both use a 2-second lock timeout.

Observed on PostgreSQL 17 and 15.10:

```
role cascade contention outcome:
delete=<nil>
lock=source mutation is forbidden
```

No deadlock or timeout occurred. The cascade delete committed first; the admin
lock then returned `ErrSourceMutationForbidden`, which the application maps to a
fail-closed administration refusal. The test also fails if both operations
succeed without contention or if the deletion commits while the lock
incorrectly succeeds.

This directly addresses the opposing-order risk between `AuthRoles` cascade
deletion and `lockUserAdminRoleTx`. If a future schema or database version
produces a deadlock, the test records both errors and fails the “both succeeded”
invariant; the smallest correction would be to make role administration acquire
the same grant-then-user order under a separate approved change. No trigger or
production privilege change was made.

## Out-of-band ownership writes

Inventory of application write paths to `Sources.usr_id` and
`SourceOwnership`:

- `VerifySourceOwnershipContext` (`pkg/database/source_ownership.go:396`):
  locks source, users and verifier grant.
- `insertAuthenticatedSourceOwnership`
  (`pkg/database/source_authenticated.go:190`): new source creation inside the
  authenticated source transaction.
- `recordPolicySourceOwnershipTx`
  (`pkg/database/source_upsert_policy.go:247`): Information Seed/source-upsert
  provenance inside its source transaction.
- `insertSourceWithPolicyContext` / `insertSourceWithPolicyTx`
  (`pkg/database/source_upsert_policy.go`): source insert with `usr_id`.
- `source_authenticated.go` update path: updates the source row under the
  authenticated source transaction.
- PostgreSQL/SQLite setup and migration files.

The admission/approval serialization guarantee assumes every concurrent writer
takes the `Sources` row lock before changing ownership/provenance. Direct
database-administrator `UPDATE Sources SET usr_id` or `DELETE FROM
SourceOwnership` outside these paths is not serialized. No trigger was added, no
production permission was revoked and schema 1.17 was not changed.

A future least-privilege design could restrict `UPDATE`/`DELETE` on ownership
tables to a dedicated function/role. That requires a separate security and
deployment decision.

## PostgreSQL evidence

`/home/pzaino/tmp/opencode/wp04c2m-pg.log`

- PostgreSQL 17: six MCP tests PASS
- PostgreSQL 15.10: six MCP tests PASS
- No SKIP
- No dispatch attempts
