# ADR 000: Agent dual-mode baseline, compatibility contract, and rollout guards

- **Status:** Accepted
- **Date:** 2026-03-28
- **Milestone:** 0

## Context

The CROWler currently supports legacy agent/job manifests centered on `jobs`. We are introducing identity-enabled agent execution, but the migration must not break current users.

## Decision

### 1) Two supported manifest modes

1. **Legacy mode (`v1`)**
   - Input shape: `jobs` only (no `agent_identity` required).
   - Runtime behavior: parser derives identity metadata from `jobs[0].name`.
   - Compatibility guarantee: existing legacy configs continue to parse and execute unchanged.

2. **Identity-enabled mode (`v2`)**
   - Input shape: `agent_identity + jobs`.
   - Runtime behavior: explicit identity is used and defaulted for missing optional fields.
   - Compatibility guarantee: JSON and YAML are both supported with equivalent parsing semantics.

### 2) Structured compatibility marker

Agent manifests carry `format_version`:
- `v1`: legacy jobs-only compatible manifest
- `v2`: identity-enabled manifest

When omitted, parser compatibility defaults are applied:
- no `agent_identity` ⇒ treated as `v1`
- with `agent_identity` ⇒ treated as `v2`

### 3) Rollout guards (default-safe)

The global runtime config gains rollout flags under `agent`:
- `agent.identity_enforcement` (default `false`)
- `agent.contract_enforcement` (default `false`)
- `agent.memory_runtime` (default `false`)

These defaults preserve legacy behavior until rollout milestones explicitly enable stricter enforcement.

## Consequences

- Backward compatibility is explicit and testable.
- Future milestones can introduce enforcement without accidental default breakage.
- CI can lock in baseline behavior using golden fixtures for YAML and JSON.

## Verification strategy (Milestone 0)

- Golden fixtures for legacy and identity-enabled manifests in both YAML and JSON.
- Parser regression tests proving legacy manifests still load.
- Feature flag default tests proving all rollout guards are disabled by default.
- Documentation matrix mapping format × strictness mode × compatibility outcome.
