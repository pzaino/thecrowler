# WP-04C2H Baseline

## Repository state

- Repository: `pzaino/thecrowler`
- Branch: `develop`
- Starting HEAD: `408fc80840527c53c3b301771a89c974fb8f42f6`
- Working tree at start: clean.
- Go: `go1.27.3 linux/amd64`.
- Node: `v24.22.1`.
- Docker: `29.7.2-ce`; Compose `v5.6.1`.
- PostgreSQL test stack: `postgres:15.10-bookworm`.
- Release: `v2.3.0`.
- Schema: `1.17`, writer generation `verified-ownership-v1.17`.

The B2 manual-run endpoint and principal propagation are present at this HEAD:
`services/events/agent_run.go`, `pkg/agent/mcp_principal.go`, typed
`AgentExecutionContext` principal and runtime propagation, and the B2 review
reports.

## B3 approval

The repository maintainer explicitly instructed `continue to B3` after B2 was
applied, tested, reviewed and approved. The tracked record is
`WP-04C2H_B3_DECISION.md`.

B4, B5 and G3 remain pending. No dispatcher or external mutation is approved.

## Execution boundaries

The production inert proposal path is:

```text
authenticated manual agent run (B2)
        |
        v
AgentToolLoop / AgentGoalRun
        |
        v
CurrentMCPMutationPolicy()
        |
        v
registerMCPMutationProposalTools
        |
        +-- exact policy operation and exact frozen descriptor
        +-- explicit run alias allowlist
        +-- authenticated principal
        +-- agent/capability/trust/contract
        +-- source/resource scope and transport
        +-- durable schema-1.17 store capability
        |
        v
MCPMutationProposalToolForOperation
        |
        v
DatabaseMCPMutationProposalStore.Admit
        |
        v
inert approval_required proposal
```

Native crawl `MutationAllowed` and `AllowedOperations` are not part of this
path. The MCP authority has separate runtime fields and a separate registry
marker.

## Findings before the fix

1. `NewMCPMutationProposalTool` selected the first policy-matching descriptor.
   That was unsafe for multi-operation policies.
2. `AgentToolRegistry` wrapped every tool in `frozenTool`, which initially lost
   the MCP proposal marker, so the durable proposal path could be denied as a
   native mutation.
3. The durable row contract requires a non-empty agent `owner` audit label.
   Registration now checks it before admission so a tool error cannot leave a
   partial proposal row.
4. The first live attempt also exposed an agent-action input requirement:
   `AgentToolLoop` requires an `input` parameter. The test request supplies one.

## Source boundaries inspected

- `pkg/agent/{mcp_mutation.go,mcp_mutation_store.go,mcp_config.go,mcp_manager.go,mcp_registration.go}`.
- `pkg/agent/{tool_policy.go,tool_registry.go,tool_loop.go,action_tool_loop.go,action_goal_run.go,execution_runtime.go,setup.go}`.
- `pkg/config/mcp_mutation_policy.go`, `pkg/config/mcp_mutation_policy_strict.go`.
- `pkg/database/mcp_mutation.go` and the additive schema-1.17 MCP migrations.
- `services/events/{agent_run.go,main.go}`, `pkg/auth`.
- `.github/workflows/postgresql-integration.yml`, `TESTING.md` and Dockerfiles.

## Information Seed and schema

No Information Seed source, migration, fixture, Compose runner or
`tests/integration/infoseed-compose/` file is changed. No schema version,
writer generation or production/shared database is changed.
