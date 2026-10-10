# Advanced v2 Agent Examples

This directory includes advanced CROWler `format_version: v2` agent manifests designed to stress-test identity-aware orchestration, delegation, contracts, and runtime memory.

Every manifest here strict-validates against `schemas/crowler-agent-schema.json`
and loads through the production normalizer (see `pkg/agent` example tests).
`agent_identity.name` always matches the entry-point job, delegating agents
carry the `delegate` capability, and `CreateEvent` steps use numeric sources.

## Event prerequisites

- `crawl_completed` jobs fire only with `crawler.create_event_when_done: true`
  (see `config.default`); the crawler emits nothing otherwise.
- `ingest.market_news` (finance example) is a hypothetical deployment-local
  event: no in-repo service emits it. Repoint it at a real feeder event type
  or treat the file as wiring documentation.
- AI steps need operator-provided endpoints and credentials (`url`, keys);
  the `https://api.openai.com/...` URLs and `${...}` placeholders are
  intentionally non-functional without them.

## New advanced scenarios

- `soc-autonomous-triage-v2.agent.yaml`
  - Security operations coordinator with delegated specialist paths.
  - Exercises: `Decision`-based delegation, persistent memory, mixed actions (`APIRequest`, `DBQuery`, `AIInteraction`, `PluginExecution`, `RunCommand`, `CreateEvent`), and contract/audit metadata.

- `compliance-evidence-fabric-v2.agent.yaml`
  - Governance workflow for control coverage and evidence pack publishing.
  - Exercises: interval trigger syntax, multi-hop delegation, persistent memory handoffs, policy-oriented contracts, and deterministic event outputs.

- `revenue-growth-experiment-lab-v2.agent.yaml`
  - Experiment lifecycle orchestration for growth teams.
  - Exercises: manual trigger orchestration, statistical decision routing, ephemeral memory usage with TTL, and launch/backlog branch fan-out.

## Why these are useful for capability benchmarking

Together these examples probe:

1. **Identity model richness** (`agent_id`, `trust_level`, `reasoning_mode`, `self_model`, `agent_contract`, `audit_tags`).
2. **Governance semantics** (`forbidden_actions`, failure policies, constraints and resource limits).
3. **Action coverage** across all available action types and mixed workflows.
4. **Delegation depth** with coordinator/specialist patterns and deterministic trigger wiring.
5. **Memory runtime behaviors** using `memory_read_key` / `memory_write_key`, persistent and ephemeral scopes.
6. **Operational output quality** via structured event emission suitable for downstream analytics.

## Validation commands

Use the CLI to lint and validate examples:

```bash
# lint
crowler-agt agents lint agents/examples/soc-autonomous-triage-v2.agent.yaml
crowler-agt agents lint agents/examples/compliance-evidence-fabric-v2.agent.yaml
crowler-agt agents lint agents/examples/revenue-growth-experiment-lab-v2.agent.yaml

# strict validation
crowler-agt agents validate --strict agents/examples/soc-autonomous-triage-v2.agent.yaml
crowler-agt agents validate --strict agents/examples/compliance-evidence-fabric-v2.agent.yaml
crowler-agt agents validate --strict agents/examples/revenue-growth-experiment-lab-v2.agent.yaml
```
