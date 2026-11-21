# Execution-Profile Contract

## Status

**Design only. Not implemented.** The Option A gate is blocked; this document
describes the contract that would be implemented only after the tracked Option A
approval.

This is not an authorization to execute anything. The simulator would be a
local/test-development read-only evaluator. External execution remains
unapproved.

## Package and trust boundary

Proposed placement: a pure Go package under `pkg/agent` that:

- is never imported by a production MCP tool manager, action executor, API
  router or Events handler;
- accepts only typed, trusted inputs from an authorized test/dev caller;
- takes an explicit `context.Context`, a deterministic UTC clock and immutable
  snapshots;
- uses immutable fixtures or an approved read-only adapter, never direct DML;
- has no production registry entry, action rule, tool alias, HTTP/Events route
  or configuration flag;
- constructs no principal, policy or approval from model arguments, event
  payloads, manifests or raw JSON;
- initiates no mutating MCP `tools/call`, network side effect, lease, claim,
  retry or reconciliation;
- returns no completed-execution or goal-success indication.

The final package boundary must be chosen only after checking import cycles and
production import graphs.

## `ExecutionProfileV1`

The immutable profile would bind identity and intent, at minimum:

### Proposal and identity

- proposal ID;
- proposal SHA-256;
- identity version;
- run ID and trace ID;
- agent ID, owner and trusted principal;
- principal issuer and current roles;
- approver stable ID and issuer;
- current source authority represented by a trusted snapshot.

### Approval

- approval ID;
- verified fingerprint;
- approver identity;
- approval state;
- expiry;
- revocation state;
- one-shot execution allowance.

### Policy

- operator policy ID;
- policy version;
- normalized canonical policy SHA-256;
- exact allowed operation;
- scopes;
- transport;
- approval and audit flags;
- current backend, fixed to `none`.

### Tool and intent

- MCP server ID;
- raw tool name;
- exact derived alias;
- frozen inventory binding;
- canonical argument SHA-256;
- validated field constraints and bounds;
- source IDs or one unambiguous approved external resource.

### Transport

For stdio:

- executable identity;
- executable digest;
- arguments;
- working directory;
- bounded environment-name allowlist;
- versioned secret-reference identities.

For HTTP:

- endpoint identity;
- auth mode;
- TLS trust;
- redirect policy;
- private-network policy;
- DNS binding assumptions;
- versioned secret-reference identities.

### Schema and submission

- frozen tool-schema SHA-256;
- separately labelled **simulated** live-schema SHA-256;
- hypothetical backend identity;
- tool-specific idempotency capability;
- tool-specific reconciliation capability, as explicit non-defaulted inputs;
- hypothetical submission identity.

## Canonical digest

```text
SHA-256("crowler-mcp-execution-profile-v1\0" || canonical-profile-bytes)
```

Canonicalization rules:

- explicit domain/version separator;
- deterministic field order;
- explicit null/default semantics;
- sorted normalized lists where order is not security-significant;
- preserved order where it is;
- no non-deterministic map iteration;
- no floating-point ambiguity;
- bounded lengths and nesting depth;
- reject unknown fields for any accepted serialized fixture;
- deep-copy or immutable-by-construction values so a caller cannot mutate a
  validated profile;
- no raw secret values, passwords, OAuth tokens, API keys or model arguments in
  the profile bytes, result, logs or errors;
- secret references are versioned opaque references or a properly keyed digest,
  not an unkeyed hash of low-entropy secret contents;
- a change to any authority-bearing field must change the digest.

A simulated live-schema digest must never be presented as a live-verified
observation.

## Evaluator result classes

Only these result classes would be allowed:

- `would_refuse`
- `validation_passed_for_simulation`
- `simulated_uncertain_after_send`

Every result would carry a simulated/not-authorized label, bounded stable reason
codes and sanitized metadata. It would never contain raw credentials, private
account details, raw arguments or remote response bodies.

## Fail-closed checks

The evaluator would refuse on:

1. missing, disabled, invalid, wrong-ID, wrong-version or wrong-hash policy;
2. missing or changed agent identity, owner, capability, trust or forbidden
   action;
3. missing, disabled, deleted or out-of-date proposer or approver authority;
4. unverified, deleted, missing or transferred source ownership;
5. wrong or ambiguous external-resource identity;
6. changed stdio executable, arguments, working directory, environment
   reference or secret version;
7. changed HTTP endpoint, TLS trust, redirect behavior, private-network policy,
   auth mode or DNS identity;
8. changed frozen schema or missing simulated live-schema digest;
9. corrupt stored proposal or approval, wrong fingerprint or wrong identity
   version;
10. expired, revoked, exhausted, missing, duplicate or cross-proposal approval;
11. duplicate hypothetical submission identity;
12. tool without an explicit idempotency/reconciliation contract;
13. cancellation before a hypothetical send;
14. hypothetical crash or timeout after bytes might have been sent, which must
    return uncertainty rather than success or a safe-retry claim;
15. unknown policy/transport fields or inconsistent current/recorded identities.

## Non-mutation proof

Tests would assert:

- zero inserts into `MCPMutationDispatchAttempts`;
- zero proposal, approval, quota or source mutations;
- no `dispatching`, `uncertain`, `succeeded`, `failed` or
  `reconciliation_required` state created by simulation;
- zero MCP mutating calls, including against a fake mutation fixture;
- no worker, lease, claim, retry or reconciliation scheduling;
- no goal marked successful from a proposal, approval or simulation result;
- schema, version and writer generation unchanged.

Any unexpected write or outbound mutation path would be a deliberately
panicking test fake, so an accidental side effect cannot be silently ignored.

## Test matrix after approval

- digest golden fixtures, field sensitivity and round-trip tests;
- every refusal case above;
- mixed source/resource and transport ambiguities;
- concurrency and cancellation under `go test -race`;
- defensive-cloning and caller-mutation tests;
- secret/raw-argument exposure scan of all artifacts;
- comparison against the real read-only policy evaluator without changing
  production gates;
- independent read-only review of the import graph and every storage/network
  interaction.

## Explicit non-authorization

Nothing here authorizes a dispatcher, worker, lease, claim, heartbeat, retry,
reconciliation, remote mutating MCP call, production credential, public
endpoint, schema change or automatic execution. External execution remains
unapproved.
