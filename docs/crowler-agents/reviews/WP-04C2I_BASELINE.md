# WP-04C2I Baseline

## Repository state

- Repository: `pzaino/thecrowler`
- Branch: `develop`
- Starting HEAD: `7f4db7f6d684ec51ec9968880735f52d30b0b509`
- Working tree at start: clean.
- Go: `go1.27.3 linux/amd64`.
- Docker: `29.7.2-ce`; Compose `v5.6.1`.
- Disposable PostgreSQL stack: `postgres:alpine3.20` (PostgreSQL 15 family).
- Release: `v2.3.0`.
- Schema: `1.17`, writer generation `verified-ownership-v1.17`.

The starting HEAD contains the B3 change set: independent MCP proposal
authority, exact alias registration, inert durable proposals, and the B3 review
reports. No B4-specific code or `WP-04C2I` report is present in this checkout.
The B5 approval states that B4 was applied and reviewed elsewhere; B5 is
independent and does not modify approval-time policy revalidation.

## Existing audit contract

`MCPMutationAudit` already exists in schema 1.17 with:

- `event_id`, `occurred_at`, `event_type`, `actor_type`, `actor_id`,
  `actor_issuer`;
- `proposal_id`, `proposal_sha256`, `approval_id`, `attempt_id`;
- `run_id`, `trace_id`, `agent_id`, `principal_user_id`;
- `server_id`, `tool_alias`, `reason_code`, `redacted_detail`.

Existing durable events cover proposal creation, expiry, reopening, operator
denial, approval issue/expiry/exhaustion and revocation. They are written in
the admission/approval transaction through `insertMCPMutationAuditTx`.

Before B5, denied proposal admissions and denied production registrations were
only visible in the in-memory `AgentsEngine` audit. `emitMCPMutationAudit`
writes that in-memory run trace; it is not the durable compliance record.

## Source boundaries inspected

- `pkg/database/mcp_mutation.go`, the additive schema-1.17 MCP migrations and
  the database tests.
- `pkg/agent/mcp_mutation.go`, `mcp_mutation_store.go`, `mcp_registration.go`,
  `tool_policy.go`, `tool_registry.go`, `tool_loop.go`,
  `action_tool_loop.go`, `action_goal_run.go` and `governance.go`.
- `pkg/config/types.go`, `config.default` and
  `schemas/crowler-config-schema.json`.
- `services/events/main.go`, `services/api/mcp_mutation.go`.
- `docs/crowler-agents/mcp-mutation-authorization.md`,
  `docs/crowler-agents/manual-agent-runs.md` and `docs/test_policy.md`.
- `Dockerfile.events`, `Dockerfile.searchapi`, `Dockerfile.thecrowler` and the
  disposable Compose workflow.

## Schema and deployment boundaries

No schema version, migration file, capability checksum, writer generation,
release version, Information Seed source, VDI behaviour or `.github` workflow
is changed. B5 uses the existing additive schema-1.17 audit table and its
indexes. The live test used a disposable database and removed its containers
and volumes.

## Findings before the fix

1. Proposal denials returned an error but left no durable audit row.
2. Registration denials before proposal-tool creation had no durable record.
3. Tool-loop and goal authorization denials happened before
   `MCPMutationProposalTool.Execute`, so a proposal-only audit inside `Execute`
   would miss them.
4. Nothing bounded the reason, detail, actor or call-ID fields written to the
   durable audit.
5. There was no retention policy for durable MCP mutation audit rows.
6. An audit write failure had no documented interaction with the admission
   decision.
