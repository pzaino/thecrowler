# WP-04C2-C Baseline

## Repository state

- Repository: `pzaino/thecrowler`
- Local branch: `develop`
- Local HEAD: `cc2461d407678b47bb3de5ed63891400b04c66a9`
- Reviewed GitHub HEAD in the assignment:
  `3aee6780b7b437d120ad2e8ca08adf878db3a330`
- Working tree at inspection: clean.
- Go: `go1.27.3 linux/amd64`.
- Release: `v2.3.0` (`RELEASE_VERSION.txt` unchanged).
- Schema: `RequiredSchemaVersion = "1.17"`,
  `WriterGeneration = "verified-ownership-v1.17"`.

The local `develop` branch is one commit ahead of the reviewed HEAD. Commit
`cc2461d4` contains unrelated Information Seed changes and only documentation
wording changes under `docs/crowler-agents/mcp-mutation-authorization.md`; no
MCP mutation Go code changed between `3aee6780` and `cc2461d4`.

## Baseline test evidence

From the clean isolated checkout at `cc2461d4`:

```sh
go test ./... -count=1
```

Result: PASS. Log:
`/home/pzaino/tmp/opencode/wp04c2c-baseline-full.log`.

Integration-tagged PostgreSQL tests were not run in this baseline step. The
existing `pkg/database/mcp_mutation_postgres_integration_test.go` contains an
unconditional `TRUNCATE` and is treated as a P0 defect in this work package.

## Documentation availability

Present locally:

- `TESTING.md` (gitignored, not in the GitHub tree)
- `docs/test_policy.md`
- `docs/crowler-agents/mcp-mutation-authorization.md`
- `docs/crowler-agents/mcp-read-only.md`
- `docs/crowler-agents/crawl_request_authorization.md`
- `docs/crowler-agents/crawl_scheduling_foundation.md`
- `docs/crowler-agents/crowler_agents_reference.md`
- Master plan:
  `/home/pzaino/Documents/opencode/crowler-tasks/CROWler_Agentic_AI_OpenCode_Master_Plan_2026-11-05.md`

The WP-04C2-A/B documents are present in the local
`artifacts/agentic/` directory, which is gitignored. They are not part of the
Git tree at `cc2461d4` (or `3aee6780`), so the references from tracked docs are
local-only. They are available in the worktree and in the previous delivery
patches under `/home/pzaino/tmp/opencode/wp04c2b-delivery/`.

## What is implemented

- Numeric canonicalization, atomic in-memory admission, approval verifier
  hardening and the WP-04C2-A tests.
- Additive schema-1.17 MCP mutation capability for PostgreSQL and SQLite:
  proposals, approvals, dispatch-attempt table, audit, quota rows and
  capability marker.
- Additive migration scripts and clean-install parity.
- Durable proposal/approval adapters.
- Inert operator list/detail/approve/deny/revoke/attempts routes.
- Capability detection and schema checks for tables, columns, selected indexes
  and the capability checksum.
- Postgres integration test that currently truncates caller-selected MCP
  tables (P0-A defect).

## What is design-only or disabled

- No dispatcher, dispatch claim, execution backend or reconciliation worker.
- No production agent registration of the mutation proposal tool.
- No public YAML/JSON operator policy for the full `MCPMutationPolicy` contract.
- No production authenticated principal wiring into `ToolLoopConfig` or
  `GoalRunConfig` for MCP mutation proposals.
- The dispatch-attempt table is unused.
- Remote MCP mutation counter remains zero.

## Baseline architecture inventory

- Agent runtime: `pkg/agent/tool_loop.go`, `action_tool_loop.go`,
  `action_goal_run.go`, `tool_policy.go`, `tool_registry.go`.
- MCP client and fixtures: `pkg/agent/mcp_manager.go`, `mcp_config.go`,
  `mcp_http.go`, `mcp_fixture_test.go`.
- Durable MCP mutation code: `pkg/agent/mcp_mutation.go`,
  `pkg/agent/mcp_mutation_store.go`, `pkg/database/mcp_mutation.go`,
  `services/api/mcp_mutation.go`.
- Database migrations and clean install:
  `pkg/database/db_migrations/*mcp*`, `postgresql-setup.pgsql`,
  `sqlite-setup.sqlite3`, `pkg/database/startup_compatibility.go`.
- Operator API auth: `services/api/main.go`, `services/api/auth_console.go`,
  `services/api/crawl_approval.go`, `pkg/auth/auth.go`.
- Native crawl authorization: `pkg/agent/crawl_authorization.go`,
  `crawl_approval_store.go`, `crawl_request.go`.

## Initial dispositions

| Area | Baseline disposition |
| --- | --- |
| PostgreSQL test cleanup | Unsafe; P0-A fix required |
| Expired approval reuse/state | Broken; P0-B fix required |
| Corrupt durable proposal conversion | Silently partial; P1-C fix required |
| Tenant-aware pagination | Global limit before filtering; P1-D fix required |
| Quota parity | In-memory lacks SQL expiry sweep; P1-E fix required |
| Schema 1.17 capability | Implemented but not deployment-approved |
| Operator API | Implemented but not deployment-approved |
| Production proposal producer | Blocked on trusted principal and public policy contract |
| Remote dispatch | Not implemented; remains prohibited |
