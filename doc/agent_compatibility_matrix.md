# Agent compatibility matrix (Milestone 0 baseline)

This matrix defines the compatibility contract before the runtime refactor.

## Axes

- **Manifest mode**: legacy (`jobs` only) vs identity-enabled (`agent_identity + jobs`)
- **Input format**: YAML and JSON (first-class, equivalent behavior)
- **Validation mode**: lenient (default) and strict (opt-in, planned in Milestone 2)

## Baseline outcomes

| Manifest | Marker | Format | Lenient mode (default) | Strict mode (planned) |
|---|---|---|---|---|
| Legacy jobs-only | `v1` (explicit or derived) | YAML | ✅ Parse + run unchanged | ✅ Expected to pass |
| Legacy jobs-only | `v1` (explicit or derived) | JSON | ✅ Parse + run unchanged | ✅ Expected to pass |
| Identity-enabled | `v2` (explicit or derived) | YAML | ✅ Parse + run | ✅ Expected to pass |
| Identity-enabled | `v2` (explicit or derived) | JSON | ✅ Parse + run | ✅ Expected to pass |

## Compatibility guarantees

1. Existing `jobs`-only configs must keep working with no required edits.
2. New `agent_identity + jobs` configs must parse and run in both YAML and JSON.
3. Feature flags remain disabled by default to preserve current runtime behavior.
4. Strict-vs-lenient divergence is introduced intentionally in Milestone 2, not before.
