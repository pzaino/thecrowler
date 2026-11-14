# WP-04C2L Gate Decisions

## Status

**Permitted work completed. External dispatch remains blocked.**

## Existing approvals used

| Decision | Scope used here |
| --- | --- |
| B1 | Strict operator mutation policy remains the authority source |
| B2 | Trusted principal provenance and manual-run identity |
| B3 | Proposal-only registration and inert approval lifecycle |
| B4 | Current-policy revalidation at approval |
| B5 | Bounded denied-admission audit and retention |
| G3 | Schema-1.17 capability and SQLite foreign-key enforcement |

WP-04C2L does not claim that those decisions authorize a dispatcher, external
mutation execution, automatic execution, a new execution backend or a schema
change.

## Changes made under existing approval

### Approval service

The approval lifecycle remains record-only. Changes are limited to:

- typed `MCPMutationApprovalCommand` derived from authenticated middleware
  identity;
- a mandatory in-transaction `MCPMutationApprovalAuthorizer`;
- current approver, proposal principal, role, source and ownership checks;
- current policy/inventory/agent snapshot installation;
- stable fail-closed error mapping.

No HTTP route, JSON field, response shape, operator policy field or database
schema was added or changed.

### E2E harness

The fixture remains test-only. Changes are limited to:

- per-run identity and ownership labels;
- dynamic loopback ports;
- private Compose network and internal model services;
- source/digest-labelled run-scoped images;
- exact-label cleanup;
- structured, redacted artifacts;
- safety tests and an opt-in workflow.

### CI

The dedicated PostgreSQL MCP step now includes the restore rehearsal and the
approval lock-order test, with mandatory RUN/PASS and no-SKIP checks. The E2E
workflow is `workflow_dispatch` only and uses a disposable Docker daemon.

## Changes requiring a new maintainer decision

| Change | Required decision |
| --- | --- |
| Mutating remote MCP `tools/call` | Explicit first-execution approval |
| Dispatcher, worker, dispatch claim or reconciliation | Explicit dispatch design approval |
| New execution backend | Separate design/security review |
| New schema version/column/table | Explicit schema decision on the 1.17 line |
| Public approval or operator API change | Separate API contract review |
| Policy hot reload across API/Events | Separate consistency design |
| Production database migration | Normal deployment review, not this task |

## Dispatch design gate

`WP-04C2L_DISPATCH_DESIGN.md` is a design only. It proposes a versioned
execution-profile identity and describes claim, uncertainty, idempotency and
reconciliation requirements. No code, table write, state transition or remote
call was added.

`WP-04C2M_DISPATCH_APPROVAL_REQUEST.md` is a request for a future decision. It
is not approval. Its default recommendation is a dry-run validation simulator
with no mutating remote call.

## Explicitly not done

- No `MCPMutationDispatchAttempts` insert.
- No `dispatching`, `uncertain`, `succeeded`, `failed` or
  `reconciliation_required` production transition.
- No retry or reconciliation worker.
- No service-principal impersonation.
- No model-controlled policy.
- No schema 1.18.
- No commit, push, merge, tag or pull request.
