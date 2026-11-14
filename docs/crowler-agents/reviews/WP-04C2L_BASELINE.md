# WP-04C2L Baseline

## Repository state

- Repository: `pzaino/thecrowler`
- Branch: `develop`
- Actual reviewed HEAD: `904f5684ccd2fbc2650e5a3ffa1ff41f51d35757`
- Previous WP-04C2K baseline: `180bd5f9b69fbb1806366a915435a5f96e514276`
- Working tree at inspection: clean
- Release: `v2.3.0`
- Required schema: `1.17`
- Writer generation: `verified-ownership-v1.17`
- Execution backend: `none`
- In-memory production proposal store: not used
- Remote mutating MCP `tools/call`: absent
- Dispatcher, worker, retry/reconciliation machinery: absent

The only commit between the prior baseline and the actual HEAD is:

```
904f5684 more work on adding Approval-Gated Operator MCP Mutation Policy
```

It contains the merged WP-04C2K implementation and reports. The WP-04C2K
approval boundary was source-reviewed and reproduced before editing.

## Pre-change verification

Commands were run before any change:

```sh
go test ./pkg/agent ./pkg/database ./services/api ./services/events -count=1
go test ./... -count=1
go test -race ./pkg/agent ./pkg/database ./services/api ./services/events -count=1
go vet ./...
go vet -tags=integration ./pkg/database
CGO_ENABLED=0 go build ./...
CGO_ENABLED=1 go build ./...
```

All passed. Logs:

- `/home/pzaino/tmp/opencode/wp04c2l-baseline-tests.log`
- `/home/pzaino/tmp/opencode/wp04c2l-baseline-static.log`

## Existing decisions and boundaries

Read at baseline:

- `docs/crowler-agents/reviews/WP-04C2K_*`
- `docs/crowler-agents/reviews/WP-04C2F_*` through `WP-04C2J_*`
- `docs/crowler-agents/mcp-mutation-authorization.md`
- `docs/crowler-agents/schema-deployment.md`

Existing approvals:

| Gate | Recorded decision |
| --- | --- |
| B1 | Operator-owned strict mutation policy, approved |
| B2 | Trusted principal provenance, approved |
| B3 | Proposal-only registration, approved |
| B4 | Current-policy approval revalidation, recorded as approved in the B5 decision |
| B5 | Denied-admission audit and retention, approved |
| G3 | Schema-1.17 capability deployment and SQLite FK enforcement, approved |

None of those decisions authorizes external dispatch, a remote mutating
`tools/call`, a dispatcher, automatic execution or schema changes.

WP-04C2L treats the following as within the existing record-only approval
lifecycle:

- approval-service authority hardening;
- test-only E2E isolation and safety fixes;
- CI coverage for existing tests;
- documentation and a future-dispatch design request.

External execution, a new execution backend, a new schema column/table or a
public operator API change would require a new explicit maintainer decision.

## Source review of the WP-04C2K E2E harness

The reviewed `tests/integration/mcp-authorization/run.sh` was not run before
this review because it had the following safety defects:

- default fixed Compose project `wp04c2k-e2e`;
- fixed host ports for DB/API/Events/models;
- `PORT:container_port` bindings, not loopback-only;
- unconditional pre-start `docker compose down -v --remove-orphans`;
- unconditional cleanup with the same broad removal;
- caller-defined `CROWLER_E2E_WORK` could be recursively deleted;
- PID files were killed without proving process identity;
- generic `crowler-api`/`crowler-events` images were reused without source
  provenance;
- fake models listened on `0.0.0.0`;
- no structured run identity, ownership manifest or artifact redaction.

These defects are test-environment hazards, not production authorization paths.
They were fixed before any destructive E2E execution.

## Production inertness at baseline

Searches confirmed:

- no production `tools/call` for mutation;
- no write path to `MCPMutationDispatchAttempts` outside schema/tests;
- no dispatcher, worker or reconciliation state machine;
- no automatic execution after approval;
- execution backend remains `none`;
- approval remains record-only.

## Baseline gaps addressed by WP-04C2L

1. E2E run identity, resource ownership, network isolation, dynamic ports,
   provenance and cleanup were unsafe.
2. `DatabaseMCPMutationApprovalStore.Issue` revalidated policy but approver
   authority was only checked by the HTTP handler before the transaction.
3. Proposal admission and approval issuance did not share one documented lock
   order for source, user, quota and proposal rows.
4. The E2E fixture was not in an isolated opt-in CI workflow.
5. The PostgreSQL restore rehearsal was not in the dedicated CI regex.
6. No pre-dispatch execution-profile or uncertainty design existed.

## Isolation

All work is uncommitted in the isolated clone:

`/home/pzaino/tmp/opencode/thecrowler-wp04c2l`

The original checkout remains clean at
`904f5684ccd2fbc2650e5a3ffa1ff41f51d35757`.
