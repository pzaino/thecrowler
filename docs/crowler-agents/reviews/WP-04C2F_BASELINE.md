# WP-04C2F Baseline

## Repository state

- Repository: `pzaino/thecrowler`
- Branch: `develop`
- Starting HEAD: `b5e7d020183acfba0e4a7c10834cf6612e3e135d`
- Working tree at start: clean.
- Go: `go1.27.3 linux/amd64`.
- Node: `v24.22.1`.
- SQLite driver: `github.com/mattn/go-sqlite3 v1.14.52`.
- PostgreSQL integration: `postgres:17` disposable container.
- Release: `v2.3.0`.
- Schema: `RequiredSchemaVersion = "1.17"`,
  `WriterGeneration = "verified-ownership-v1.17"`.

The isolated implementation worktree is
`/home/pzaino/tmp/opencode/thecrowler-wp04c2f-impl`, checked out at
`b5e7d020`. The original checkout remains clean and is not an edit target.

## Approval

B1 was explicitly approved by the repository maintainer in the session with
the message `B1 is approved.` The tracked record is
`WP-04C2F_B1_DECISION.md`. The approval is limited to B1: operator-owned
`mcp.mutation_policy`, strict subtree loading, exact inventory binding,
immutable snapshots, schema/default/docs and tests.

B2 (trusted principal provenance), B3 (independent MCP operation authority and
production registration), B4 (approval-time policy revalidation), B5
(denied-admission audit) and G3 (schema deployment/SQLite FK) remain pending.

## Boundaries

- `5846ef53` is the unrelated Information Seed follow-up and was preserved
  exactly.
- `b5e7d020` is the WP-04C2E commit containing the `.github` PostgreSQL MCP
  step, the test-policy corrections and the WP-04C2E reports.
- No Information Seed source, migration, fixture, Compose runner or
  `tests/integration/infoseed-compose/` file is changed.
- No schema, migration, writer generation or release file is changed.
- No dispatcher, dispatch claim, `MCPMutationDispatchAttempts` insert, worker,
  retry, reconciliation or execution route is added.

## Source boundaries inspected

- `pkg/config/{config.go,types.go,mcp.go,config_test.go}`.
- `schemas/crowler-config-schema.json`, `config.default` and configuration
  reference/YAML docs.
- `pkg/agent/{mcp_config.go,mcp_manager.go,mcp_mutation.go,mcp_mutation_store.go}`.
- `pkg/database/{mcp_mutation.go,sqlite_db.go,startup_compatibility.go}` and the
  additive v1.17 MCP migrations.
- `services/api/mcp_mutation.go`, `services/events/main.go` and the
  `SetMCPOperatorConfig` startup call site.
- `.github/workflows/postgresql-integration.yml` and the WP-04C2E reports.

## Baseline findings

1. `MCPConfig` had no mutation-policy field, so the public contract did not
   exist.
2. `normalizeMCPOperatorConfig` and `cloneMCPConfig` rebuilt only the server
   inventory. Adding a policy without updating both would silently drop it.
3. Normalization also returned server slices sharing caller backing arrays; a
   caller for which `AllowedAgents`/`AllowedSources` slices could mutate the
   frozen global snapshot. Automated hardening found this and it was fixed with
   `cloneMCPServerConfig`.
4. `pkg/config/config.go` used permissive `yaml.v2` unmarshalling in every
   entrypoint. Unknown keys, duplicates and float-to-int/string conversions
   required a dedicated strict subtree decoder.
5. `MCPMutationPolicy.Validate` enforced runtime invariants but had no public
   config counterpart and no inventory binding.
6. No production `NewMCPMutationProposalTool` or MCP `RegisterMutation` call
   exists; this remains true after B1.

## CI context

The reviewed SHA was reported by the maintainer as having eight successful
GitHub checks, including the dedicated MCP PostgreSQL integration step. The
`gh` CLI was not available in the environment, so this report does not
independently claim a GitHub run URL. Local disposable PostgreSQL evidence is
in `WP-04C2F_TEST_REPORT.md`.
