# WP-04C2L Maintainer Decisions

## Recorded existing decisions

- B1 operator policy: approved.
- B2 trusted principal provenance: approved.
- B3 proposal-only registration: approved.
- B4 approval current-policy revalidation: recorded as approved in the B5
  decision.
- B5 denied-admission audit/retention: approved.
- G3 schema-1.17 capability deployment and SQLite FK enforcement: approved.

None authorizes external dispatch or a mutating remote call.

## Decisions taken in this work package

### Approval authority moves inside the transaction

`DatabaseMCPMutationApprovalStore.Approve` accepts a typed authenticated
identity and calls `Issue`. `Issue` requires a non-nil in-transaction
`MCPMutationApprovalAuthorizer`. The authorizer locks source rows, user rows and
admin grant rows, then re-resolves the approver and proposal principal.

### One global lock order

Source rows → users ascending → admin grants → quota → proposal →
approval/audit. Proposal admission was moved to authority-before-quota to match
approval issuance. This is documented and PostgreSQL-tested.

### E2E is run-scoped and isolated

The fixture no longer has a fixed project, fixed ports, broad `down`, PID-file
kills, generic images or host-exposed models. Cleanup is exact-label based.
`CROWLER_E2E_KEEP=1` preserves only identified resources and prints the cleanup
command.

### E2E CI remains opt-in

The new workflow is `workflow_dispatch` only. It uses a disposable Docker
daemon, uploads sanitized evidence and is not part of ordinary PR checks until
cost/reliability are reviewed.

### PostgreSQL restore and lock-order tests are mandatory in CI

The dedicated PostgreSQL MCP step installs client 17 and requires visible
RUN/PASS for all five tests, including restore and approval lock order. Any
SKIP fails the step.

### Future dispatch is design-only

No dispatcher, claim, remote call or schema change was added. The dispatch
design proposes a versioned execution profile and uncertainty/reconciliation
requirements.

## Decisions still required

### First external execution backend

A separate explicit decision is required. The default recommendation is a
read-only validation simulator. See
`WP-04C2M_DISPATCH_APPROVAL_REQUEST.md`.

### Execution-profile schema

A future executor likely needs a dedicated execution-profile digest and
snapshot. Whether that fits additive schema 1.17 or a later schema line is not
decided here.

### Direct database ownership enforcement

Application serialization assumes all ownership writers lock the `Sources` row.
A decision on database triggers, restricted grants or a separate integrity
service is required if direct out-of-band edits must be prevented.

### Agent registry freshness

The API freezes its registry at startup. Uploaded agents require API restart for
approval. A future shared durable registry or approved reload protocol would
need a separate design.

## Rollback

Code-only rollback is to revert the WP-04C2L patch. No migration, table drop,
schema marker or production data change is part of this work. The E2E workflow
is opt-in and can be removed independently.
