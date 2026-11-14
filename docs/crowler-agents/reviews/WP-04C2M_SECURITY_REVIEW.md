# WP-04C2M Security Review

## Authority chain

Authenticated user
→ current account and grant rows
→ agent identity
→ policy operation
→ inventory/tool
→ current source ownership/admin authority
→ durable inert proposal
→ transactional approval
→ record-only deny/revoke.

## Changes reviewed

### Approval

The WP-04C2L approval authority remains in place. WP-04C2M adds no weakening.

### Deny/revoke

Deny and revoke now recheck actor authority inside their transactions:

- source rows sorted, actor account, actor admin grant, quota, proposal;
- current admin may act after ownership transfer or principal disablement;
- non-admin must be current verified owner of every source;
- external-resource administration is admin-only;
- actor cancellation is preserved; DB/authority unavailability fails closed;
- public errors do not include source IDs or raw DB messages.

Tests include owner/unauthorized/admin/disabled/role-revoked, concurrent deny,
revocation after principal disable, PostgreSQL transfer/role races and zero
dispatch attempts.

### Deny/revoke audit

Audit writes remain in the same transaction. A failure to write audit aborts the
transaction. No new audit fields expose raw arguments or secrets.

### E2E and artifacts

- Run-scoped, label-verified resources.
- Dynamic loopback-only API/Events ports.
- Internal PostgreSQL/models.
- Dind image pinned by digest in the opt-in workflow.
- Dind API loopback-only and dynamically allocated.
- Artifacts sanitized, including result details.
- Result finalization rejects SKIP/unknown statuses; CI requires cleanup PASS,
  a minimum matrix and the completion marker.
- Tracked Python bytecode removed; digest/copy exclude `__pycache__`/`.pyc`.

### Source provenance

Digest, copy and tar excludes match. Runtime image IDs and image labels are
verified, not just Compose service labels. Stale images are rejected.

## Role deletion contention

`TestPostgresMCPMutationAdminRoleCascadeContention` records the actual cascade
delete vs admin-grant lock behavior. On PostgreSQL 17 and 15.10 the delete
committed and the admin lock then returned forbidden; no deadlock occurred.
The test would fail on an inconsistent success. No application schema or
privilege change was made.

## Out-of-band ownership writes

Application paths are inventoried in `WP-04C2M_LOCK_ORDER.md`. Direct DBA updates
or deletes bypass the `Sources` row lock. No trigger/permission change is made;
a future least-privilege/trigger design needs separate approval.

## Deployment freshness

API/Events freeze policy/agent state at startup. Policy and agent generations
must be deployed together and both processes restarted. Mismatch is fail-closed;
agent uploads after API startup require API restart. No hot reload.

## Zero external execution

Source and tests confirm:

- no production mutating MCP `tools/call`;
- no dispatch-attempt insert;
- no worker/claim/lease/retry/reconciliation;
- no automatic execution;
- `execution_backend` remains `none`;
- E2E asserts zero mutation counter and zero dispatch rows/states.

## Residual risks

- PostgreSQL CI and GitHub E2E have not run on the patch; GitHub execution is
  blocked by no push/token. Local evidence is not a substitute.
- Direct DBA ownership edits bypass application locks.
- Role cascade behavior is version/test-schema dependent; the test records the
  observed 17/15 behavior.
- Base/helper images other than dind are tag-pinned, not digest-pinned.
- Option A simulator is not implemented; Option B is not approved.
- Information Seed Compose and live Ollama were not run.
