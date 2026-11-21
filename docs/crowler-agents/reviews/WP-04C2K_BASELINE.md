# WP-04C2K Baseline

## Repository state

- Repository: `pzaino/thecrowler`
- Branch: `develop`
- Reviewed HEAD: `180bd5f9b69fbb1806366a915435a5f96e514276`
- Previous baseline: `b5e7d020183acfba0e4a7c10834cf6612e3e135d`
- Working tree at inspection: clean (`git status --short` produced no output)
- Release contract: `v2.3.0`
- Required schema: `1.17`
- Writer generation: `verified-ownership-v1.17`
- Execution backend: `none`
- Remote MCP mutation execution: absent and forbidden by this work package

The five commits between the previous baseline and the reviewed HEAD are:

```
180bd5f9 more work on adding Approval-Gated Operator MCP Mutation Policy
479803aa more work on adding Approval-Gated Operator MCP Mutation Policy
7f4db7f6 more work on adding Approval-Gated Operator MCP Mutation Policy
408fc808 more work on adding Approval-Gated Operator MCP Mutation Policy
3946fda8 pushing a chunk of verified work done to improve MCP support and operator policy
```

These commits contain the B2/B3/B4-decision/B5/G3 work described by the tracked
reports. They do not contain a B4 approval-time current-policy implementation.

## B4 search result

Searches were run across the current source, tracked reports and all reachable
Git history:

- `git log --all --oneline --regexp-ignore-case --grep='B4|current.policy|revalidat'`
- `git log --all --oneline -S 'CurrentMCPMutationPolicy' -- services/api pkg/agent`
- `git log --all --oneline -S 'PolicySHA256' -- services/api`
- repository-wide greps for approval policy checks

No B4 implementation exists in this checkout. The only recorded approval is in
`docs/crowler-agents/reviews/WP-04C2I_B5_DECISION.md`:

> `OK, B4 applied, tested, reviewed and approved. Please continue with B5.`

That report also states that the B4 implementation was not present in the
checkout examined for B5. The approval is therefore treated as a maintainer
decision for the fail-closed B4 contract, not as evidence that source code was
present. `WP-04C2K_B4_DECISION.md` records the reconciliation.

## Authorization dataflow at the reviewed HEAD

| # | Check | Location at baseline | Observation |
| --- | --- | --- | --- |
| 1 | Initial HTTP authentication | `services/events/agent_run.go`, `services/api/mcp_mutation.go` middleware | Authenticated identity came from the request context. |
| 2 | Active-account verification | `auth.ResolveActiveIdentity` | Re-fetched `Users` state at request entry. |
| 3 | Agent identity/capability/trust | `pkg/agent/setup.go`, `tool_loop.go` | Runtime enforcement existed when `agent.identity_enforcement` was enabled. |
| 4 | Source ownership verification | `pkg/agent/crawl_authorization.go` | Verified `SourceOwnership` at request/approval time. |
| 5 | Operator policy matching | proposal creation only | No current-policy comparison at approval. |
| 6 | Tool registration | `mcp_registration.go` | Proposal tools registered only for approved mutation inventory. |
| 7 | Durable proposal admission | `pkg/database/mcp_mutation.go` | Inserted the inert proposal, but did not revalidate principal/ownership inside the insert transaction. |
| 8 | Operator approval issuance | `services/api/mcp_mutation.go`, `DatabaseMCPMutationApprovalStore.Issue` | No policy identity or exact-operation revalidation. |
| 9 | Denial auditing | `pkg/agent/mcp_audit.go` | B5 denied-admission audit existed. |
| 10 | Context cancellation | `services/events/agent_run.go` | Checked before starting, then execution used `context.Background()` for group, tool-loop and goal-run deadlines. |

The API process did not load `mcp.mutation_policy` or the agent registry. Only
`services/events/main.go` called `SetMCPOperatorConfig`. An approval could
therefore be issued by an API process that had no current-policy view at all.

## Baseline gaps confirmed

1. Approval did not compare `policy_id`, `version` or canonical SHA-256.
2. Approval did not re-check the exact operation, transport, scope, argument
   constraints, agent capability/trust or `execution_backend: none`.
3. The API process had no frozen trusted operator policy or agent registry.
4. Durable admission trusted the request-entry principal and did not re-resolve
   the account or lock current source ownership in the admission transaction.
5. Manual HTTP cancellation stopped at the Events handler boundary.
6. The dedicated PostgreSQL MCP CI regex omitted
   `TestPostgresMCPMutationAuditAndRetention`.

## Baseline commands

```sh
git rev-parse HEAD
git status --short
git log --oneline b5e7d020183acfba0e4a7c10834cf6612e3e135d..HEAD
```

Observed: clean tree at `180bd5f9b69fbb1806366a915435a5f96e514276`; five
intervening commits; no B4 source.

## Isolation

All changes are uncommitted in an isolated clone at
`/home/pzaino/tmp/opencode/thecrowler-wp04c2k`. The original checkout remains
untouched at `180bd5f9`.
