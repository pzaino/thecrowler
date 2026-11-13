# WP-04C2F B1 Decision

## Status

**APPROVED — B1 only.**

## Approval record

- Approver: repository maintainer (user) in the OpenCode session.
- Date: 2026-11-13 (UTC).
- Exact approval message: `B1 is approved.`
- Contract version: `WP-04C2F/B1-1`.
- Baseline HEAD: `b5e7d020183acfba0e4a7c10834cf6612e3e135d`.
- Scope: operator-owned `mcp.mutation_policy` with default-disabled behavior,
  derived aliases, exact inventory binding, strict YAML/JSON subtree loading,
  immutable per-process snapshots and negative tests.

The approval message did not include a personal name or a longer rationale. It
is recorded as the repository maintainer's explicit approval of the B1 scope
defined by WP-04C2F. It is not an approval of any other gate.

## Approved implementation

- Public `pkg/config` policy types and `mcp.mutation_policy`.
- Strict extraction and validation for local, include-expanded, remote, YAML
  and JSON configuration entrypoints.
- Exact binding to an enabled operator MCP server and an approved mutation tool.
- Derived `MCPToolAlias` names; no operator-entered alias.
- Immutable snapshot preservation through `SetMCPOperatorConfig` and
  `CurrentMCPOperatorConfig`, plus a deep-copied runtime policy view.
- JSON Schema, disabled `config.default` entry and operator documentation.
- Positive/negative parser, schema, immutability and concurrency tests.

## Not approved by B1

B2 principal provenance, B3 independent MCP operation authority/registration,
B4 approval-time policy revalidation, B5 denied-admission audit, G3 schema
deployment/SQLite FK changes, production proposal tool registration, dispatch,
remote mutation, and any shared/production database migration remain pending or
prohibited.

The schema remains `1.17`, writer generation remains
`verified-ownership-v1.17`, and release remains `v2.3.0`.
