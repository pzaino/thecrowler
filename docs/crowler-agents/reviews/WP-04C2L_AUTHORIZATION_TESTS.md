# WP-04C2L Authorization Tests

## Unit and focused tests

`pkg/agent/mcp_mutation_approval_authorization_test.go`:

| Test | Coverage |
| --- | --- |
| `TestMCPMutationApprovalAuthorityRejectsChangedAuthority` | approver disabled, approver deleted, proposal principal disabled, source transferred, provenance revoked, forged approver username |
| `TestMCPMutationApprovalAuthorityAcceptsOwnerAndAdmin` | source owner approval and administrator external-resource approval; non-admin external approval denied |
| `TestMCPMutationApprovalReplayRevalidatesApprover` | active approval replay after admin role revocation fails; one approval row remains |
| `TestMCPMutationApprovalServiceRequiresTrustedIdentity` | typed service rejects a malformed authenticated subject |
| `TestMCPMutationApprovalAuthorityPreservesCancellation` | cancellation is preserved, not converted to an authority error |

`pkg/agent/mcp_mutation_approval_policy_test.go` continues to cover:

- disabled policy;
- policy ID/version/hash mismatch;
- operation/agent/capability/trust/contract changes;
- inventory and transport changes;
- scope and canonical argument changes;
- corrupt fingerprint;
- concurrent issue idempotency.

`services/api/mcp_mutation_test.go` continues to cover:

- authorized approve/revoke/deny;
- duplicate approval idempotency;
- cross-tenant denial;
- disabled proposal principal;
- expired approval and reapproval;
- tenant list/detail pagination;
- missing capability fail-closed;
- method/precondition handling.

## PostgreSQL concurrency

`TestPostgresMCPMutationApprovalAuthorityLockOrder` covers:

- transfer-first contention with refusal and no approval row;
- approval-first contention with no deadlock;
- concurrent administrative-role revocation with refusal and no approval row.

Existing `TestPostgresMCPMutationDurableStore` covers concurrent admission,
duplicate approval and issue/revoke races.

Results:

- PostgreSQL 17: PASS
- PostgreSQL 15.10: PASS

Log: `/home/pzaino/tmp/opencode/wp04c2l-pg-final2.log`

## Full E2E authorization matrix

The isolated fixture (`wp04c2l-e2e-dind7.log`, 60 checks) covers:

- authenticated owner and admin;
- positive inert proposal;
- current-policy approval;
- proposal-created and approval-issued audit;
- request cancellation and slow-agent positive control;
- API/Events policy ID mismatch;
- same-version canonical hash mismatch;
- disabled current policy;
- source transfer;
- denied admission after transfer and matching denial audit;
- incorrect fingerprint;
- expired proposal;
- corrupted policy snapshot;
- disabled proposal principal;
- database outage;
- zero mutation and zero dispatch attempt rows;
- no dispatch/uncertain/reconciliation states;
- audit argument redaction.

## Evidence rules

- A denied approval is never counted as a success because an old active approval
  exists; authority runs before reuse.
- A skipped required test is a failure in CI.
- No unit test is described as proof that the live fixture ran.
- The full fixture ran on a disposable Docker-in-Docker daemon, not a shared
  daemon.
