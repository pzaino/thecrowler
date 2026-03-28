# Agent compatibility matrix (Milestone 0 baseline)

This matrix defines the compatibility contract before the runtime refactor.

## Axes

- **Manifest mode**: legacy (`jobs` only) vs identity-enabled (`agent_identity + jobs`)
- **Input format**: YAML and JSON (first-class, equivalent behavior)
- **Validation mode**: lenient (default) and strict (opt-in)

## Baseline outcomes

| Manifest | Marker | Format | Lenient mode (default) | Strict mode |
|---|---|---|---|---|
| Legacy jobs-only | `v1` (explicit or derived) | YAML | ✅ Parse + run unchanged | ✅ Schema + semantic validation |
| Legacy jobs-only | `v1` (explicit or derived) | JSON | ✅ Parse + run unchanged | ✅ Schema + semantic validation |
| Identity-enabled | `v2` (explicit or derived) | YAML | ✅ Parse + run | ✅ Schema + semantic validation |
| Identity-enabled | `v2` (explicit or derived) | JSON | ✅ Parse + run | ✅ Schema + semantic validation |

## Compatibility guarantees

1. Existing `jobs`-only configs must keep working with no required edits.
2. New `agent_identity + jobs` configs must parse and run in both YAML and JSON.
3. Feature flags remain disabled by default to preserve current runtime behavior.
4. Lenient mode enforces schema validation only; strict mode additionally enforces semantic rules (naming + decision target resolvability).

## Milestone 1 runtime model notes

- Runtime now normalizes loaded manifests into **AgentDefinition** records and stores them in an **AgentRegistry**.
- Legacy APIs (`GetAgentByName`, event-trigger lookup) continue to work via registry-backed adapters.
- Trigger selector indexing keeps deterministic insertion order for lookups that return multiple agents.

### Duplicate conflict strategy (runtime registry)

- Duplicate `agent_id`: **rejected** at registration time.
- Duplicate `agent_identity.name`: **rejected** at registration time.
- Duplicate trigger selectors (`trigger_type + trigger_name`): **allowed**; all matching agents are returned in registration order.


## Milestone 2 validation notes

- `ValidateAgentConfig()` validates **both YAML and JSON** against `schemas/crowler-agent-schema.json` after normalizing YAML to JSON-equivalent structure.
- Validation errors are emitted with path-level messages (for example `jobs[0].steps[0]...`) to make fixes actionable.
- Strict mode adds semantic checks for identity/job naming constraints, trigger sanity, and `Decision` target resolvability.
- Legacy jobs-only manifests remain accepted in lenient mode for backward compatibility.
