# WP-04C2H B3 Security Review

## Scope

B3 production registration of inert MCP mutation proposal adapters. No
external dispatch, remote mutation or automatic approval execution is in scope.

## Threats and controls

| Threat | Control |
| --- | --- |
| Native crawl authority authorizes an MCP proposal | Separate MCP authority fields and proposal marker; native fields are never consulted for MCP adapters |
| Broad `all` or a native operation grants MCP authority | Exact derived MCP alias must be in the run allowlist and the operator policy |
| First-match descriptor selection | Exact per-alias constructor; ambiguous legacy selection fails |
| Semantic alias collision | Shared alias derivation, policy uniqueness, inventory collision checks and exact descriptor matching |
| Wrong server/tool/transport/schema | Frozen descriptor identity and exact policy operation; transport and schema identity are bound before registration |
| Unapproved agent/capability/trust/contract | Registration rejects unless all exact identity gates pass |
| Source/resource scope widening | Effective inventory scope and run scope checked before registration; runtime scope rechecked during proposal creation |
| Missing principal / event origin | Registration requires a validated principal; background/event runs remain principal-less and fail closed |
| Partial proposal on a pre-admission failure | Owner audit label and policy/store checks run before admission; admission is atomic |
| In-memory fallback | Production registration requires the durable database store and schema-1.17 capability |
| Model/manifest/event supplied authority | MCP authority fields are trusted runtime values derived from policy, allowlist and identity, not model data |
| Approval treated as execution | Approval records remain inert; attempts route is empty and `dispatch_enabled:false` |
| Argument/secret leakage | Proposal responses expose only fingerprints, IDs, aliases and expiry; list/detail expose argument keys, not values |
| Duplicate/quota bypass | Existing durable store quotas and run/call fingerprint conflict checks remain in force |
| Remote mutation during tests | Controlled fixture counter stayed absent; no MCP mutation tool was called |

## Live evidence

The disposable live stack produced exactly one durable proposal with state
`approval_required`, one approval record, then a revoked approval. The
`MCPMutationDispatchAttempts` table stayed empty, the mutation fixture counter
file stayed absent, and the MCP transport was never called with the mutation
tool. The agent tool call completed with status `executed`.

The first live attempt exposed two real integration defects, both fixed:

1. the frozen registry wrapper lost the MCP proposal marker, causing the
   proposal adapter to be authorized as a native mutation and denied;
2. an agent with no `owner` audit label could insert a row and then fail
   conversion. Registration and admission now require the non-empty audit label
   before durable admission.

## Residual risks

- B4 current-policy revalidation at approval is pending. An approval currently
  relies on the existing approval contract and stored snapshot, not a new
  current-policy hash check.
- B5 denied-admission audit/retention is pending.
- G3 schema deployment and SQLite foreign-key enforcement are pending.
- The manual-run scope remains the coarse `events:*`; external-resource
  proposals require an administrator role in the existing API.
- No production dispatcher exists; this is proposal-only by design.

## Conclusion

B3 activates inert proposal creation only. It does not grant external mutation
authority, create dispatch attempts or provide completion evidence. A separate
security-reviewed milestone is required before any first external submission.
