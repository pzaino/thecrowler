# WP-04C2M Maintainer Decisions

## Existing decisions preserved

- B1 operator policy: approved.
- B2 principal provenance: approved.
- B3 proposal-only registration: approved.
- B4 current-policy revalidation: recorded approval.
- B5 denied-admission audit/retention: approved.
- G3 schema deployment/SQLite FK: approved.

None authorizes external execution.

## Decisions taken inside granted scope

### PostgreSQL CI repair

Select PostgreSQL 17 binaries from the versioned package, persist the path via
`$GITHUB_PATH`, and require both client and server major 17. This preserves the
existing disposable service and test contracts.

### Artifact hygiene

Remove the two tracked `.pyc` files, ignore future bytecode, and exclude
`__pycache__`/`.pyc`/`.pyo` from source digest, copy and image context.

### Deny/revoke in-transaction authority

Deny and revoke now require a mandatory `AdministrationAuthority` callback in
their database transactions. Current admins retain emergency authority after
ownership transfer or principal disablement; non-admins must be current verified
owners. Public HTTP routes and response shapes are unchanged.

### Role deletion contention

Added a disposable PostgreSQL test for `AuthRoles` cascade deletion vs
`lockUserAdminRoleTx`. No deadlock was observed on PostgreSQL 17/15; deletion
committed and the admin lock then failed closed.

### GitHub evidence

The opt-in E2E workflow remains `workflow_dispatch` only. No token/push was
available, so no GitHub run is claimed.

## Decisions still required

### PostgreSQL CI green gate

A maintainer must review, commit/push and run the workflow on the applied commit.
The current GitHub status is 7/8 with the install step failure; local success is
not a substitute.

### Opt-in GitHub E2E run

A maintainer must manually run `MCP authorization E2E (opt-in)` after pushing
and inspect the uploaded artifacts.

### Option A simulator

Explicit approval is required using the scope in
`WP-04C2M_SIMULATOR_GATE.md`. Without it, no simulator code is allowed.

### Option B execution

Still not approved. Do not implement a dispatcher, worker, lease, remote
mutating call or execution backend.

### Direct ownership write enforcement

A future decision is needed for database triggers, restricted grants or a
separate integrity service if out-of-band ownership edits must be prevented.

## Rollback

Code-only rollback is to revert the WP-04C2M patch. No schema migration, table
drop, marker change or production data operation is included. The opt-in E2E
workflow can be removed independently.
