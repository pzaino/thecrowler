# WP-04C2G Baseline

## Repository state

- Repository: `pzaino/thecrowler`
- Branch: `develop`
- Starting HEAD: `3946fda8510f7bfac2f3fafacfd1c6d40e543ea7`
- Working tree at start: clean.
- Go: `go1.27.3 linux/amd64`.
- Node: `v24.22.1`.
- Docker: `29.7.2-ce`; Compose `v5.6.1`.
- SQLite driver: `github.com/mattn/go-sqlite3 v1.14.52`.
- Release: `v2.3.0`.
- Schema: `RequiredSchemaVersion = "1.17"`,
  `WriterGeneration = "verified-ownership-v1.17"`.

`3946fda8` is the B1 operator-policy implementation. The B1 reports,
strict parser, JSON Schema, disabled default and immutable policy snapshot are
present in the baseline.

## B2 approval

B2 was explicitly requested by the repository maintainer with the instruction
to proceed after B1. The tracked record is `WP-04C2G_B2_DECISION.md`.

B3, B4, B5 and G3 remain pending. Production MCP proposal registration remains
disabled.

## Execution-path inventory

The only pre-existing agent execution path is event-driven:

```text
Events API event creation
        |
        v
event queue / processEvent
        |
        v
agentInputConfig(event, metadata)
        |
        v
AgentsEngine.ExecuteJobs
        |
        v
AgentExecutionContext -> ToolLoopConfig / goalRunConfig
```

There is no pre-existing authenticated synchronous agent-run endpoint. The
General API does not own an `AgentsEngine`; the Events service does.

WP-04C2G B2 therefore adds one explicit user-triggered entrypoint,
`POST /v1/agent/run`, limited to registered agents with a `manual` trigger and
the existing `events:*`/future `events:agent:run` operator scope. Event runs
remain principal-less.

## Source boundaries inspected

- `pkg/agent/{setup.go,execution_runtime.go,registry.go,action_tool_loop.go,action_goal_run.go,mcp_mutation.go}`.
- `services/events/{main.go,helpers.go}` and the agent registry/engine setup.
- `pkg/auth/auth.go` identity context, current-state resolution and grants.
- `pkg/database` SQLite/PostgreSQL handler contracts and the existing 1.17 auth
  tables (`Users`, `AuthRoles`, `AuthScopes`, `UserRoles`, `RoleScopes`,
  `UserScopes`).
- `TESTING.md` local test/container workflow.
- `docker-compose` generation and existing Dockerfiles.

## Findings

1. `ToolLoopConfig.Principal` and `goalRunConfig.Principal` existed but no
   production constructor populated them.
2. `applyExecutionContext` published identity snapshots for governance but had
   no principal field; step params could not safely carry one.
3. Event-origin `agentInputConfig` correctly had no principal.
4. The agent registry already indexed `manual` triggers but had no consumer.
5. No database schema change is required for B2: the authenticated identity is
   resolved from the existing Users/grants tables at the manual-run boundary.

## Information Seed and schema boundaries

No Information Seed source, migration, fixture or Compose suite is changed. No
schema version, writer generation, release version or shared/production
database is changed by B2.
