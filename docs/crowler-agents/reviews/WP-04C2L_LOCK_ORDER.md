# WP-04C2L Lock Order

## Global order

All code paths that touch source ownership, approval quota and proposal state
must acquire locks in this order:

1. **Source rows** in ascending `source_id` order.
2. **User rows** in ascending `user_id` order.
3. **Administrative grant rows** for those users in the same ascending user
   order.
4. **Quota rows**: `MCPMutationRunQuota`, then `MCPMutationPrincipalQuota`.
5. **Proposal row** (`FOR UPDATE` on PostgreSQL).
6. **Approval/audit writes**.

No path may take a user lock before a source lock, or a quota lock before a
source/user lock.

## Paths

### Source ownership transfer

`VerifySourceOwnershipContext`:

- source row lock;
- active users sorted (`lockActiveUsersTx`);
- verifier administrative grant lock;
- update `Sources.usr_id`, upsert `SourceOwnership`.

This is the reference order.

### Proposal admission

`AdmitMCPMutationProposalAuthorizedAt`:

1. load proposal inputs;
2. authority callback:
   - source rows sorted;
   - users sorted;
   - administrative grant rows;
3. quota rows;
4. proposal insert;
5. audit.

The authority callback was moved ahead of quota locking in WP-04C2L to match
this order.

### Approval issuance

`IssueMCPMutationApprovalAuthorized`:

1. load proposal;
2. authority callback:
   - source rows sorted;
   - approver + proposal principal users sorted;
   - administrative grant rows for those users;
3. quota rows;
4. proposal row lock;
5. identity version/fingerprint/state/expiry checks;
6. reuse or insert approval;
7. audit.

### Revoke and deny

`RevokeMCPMutationApproval` and `DenyMCPMutationProposal` acquire quota rows,
then the proposal row, then approval rows. They do not touch source or user
rows, so they cannot form a cycle with source transfer or approval issuance.

## Why this order

- Source transfer locks source before users.
- Approval authority must verify source ownership before trusting user/grant
  state.
- Admission and approval both need quota rows and a proposal row after
  authority.
- Two approvals on the same proposal serialize on the source/user locks and then
  the proposal row.
- External-resource approval has no source row and locks users directly; it
  cannot wait on a source lock held by an admission that is itself waiting on a
  user held by the external approval.

Avoiding a `Users` share lock before the source lock also removes the
share-to-update upgrade pattern that caused the WP-04C2K review finding.

## Tested contention

`TestPostgresMCPMutationApprovalAuthorityLockOrder` covers:

1. **Transfer first**: a transaction holds source 42, approval waits; transfer
   commits owner 8; approval fails `ErrMCPMutationProposalPrincipalForbidden`;
   zero approval rows.
2. **Approval first**: approval authority holds source 42 and blocks; transfer
   waits; approval commits; transfer then commits owner 8; both complete
   without deadlock. The surviving approval is historical evidence and must be
   revalidated by any future dispatcher.
3. **Role revocation**: a transaction deletes the approver's admin role and
   holds the role row; approval waits; after commit approval fails
   `ErrMCPMutationApproverForbidden`; zero approval rows.

Result on PostgreSQL 17 and PostgreSQL 15.10: PASS.

## Remaining race

A transfer that commits immediately after an approval transaction commits is a
later ownership change. The approval record is not retroactively invalidated;
it is historical. A future executor must revalidate current ownership, policy,
account, approval state and execution profile immediately before a remote call.
This is a dispatch design requirement, not an execution feature of WP-04C2L.
