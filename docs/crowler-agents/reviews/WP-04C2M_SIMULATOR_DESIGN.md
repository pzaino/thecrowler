# WP-04C2M Read-Only Simulator Design

## Status

Design only. Option A is not approved; no simulator code was added.

## Purpose

Prove, before a real executor exists, that the authorization chain and a
versioned execution-profile evaluator reject stale or unsafe states without any
external side effect.

## Suggested package boundary

A future `pkg/agent` (or explicitly test/dev-only) component may expose:

```go
type SimulationResult struct {
    Outcome       string   // would_refuse | validation_passed_for_simulation
    ReasonCodes   []string
    ProfileSHA256 string
    Redacted      map[string]any
}
```

It must accept only trusted typed inputs:

- an authenticated actor/identity from an approved test/dev boundary;
- a durable proposal ID and verified proposal fingerprint;
- an approval ID and verified approval fingerprint;
- an explicitly supplied current policy/inventory/agent snapshot;
- an explicitly supplied simulated live-schema digest and transport assumptions;
- a deterministic clock.

It must not read a principal from JSON maps, model output or manifests.

## Versioned execution-profile digest

Use a domain separator and canonical JSON:

```text
profile = SHA-256(
  "crowler-mcp-execution-profile-v1\0" ||
  canonical-json({
    proposal_id, proposal_sha256, identity_version,
    approval_id, approval_sha256, approval_state, approval_expiry,
    run_id, agent_id, owner, principal_user_id, principal_username,
    approver_user_id, approver_username, issuer,
    server_id, raw_tool_name, tool_alias, transport,
    policy_id, policy_version, policy_sha256,
    source_ids | external_resource,
    canonical_arguments_sha256, required_argument_fields,
    allowed_argument_fields,
    stdio: {executable_identity, args, working_directory,
            environment_allowlist, secret_reference_ids},
    http: {endpoint, tls_trust, redirect_policy,
           private_network_policy, auth_mode, dns_policy},
    frozen_schema_sha256, simulated_live_schema_sha256,
    backend_identity, execution_backend
  })
)
```

Rules:

- sorted keys; fixed number encoding; bounded depth/bytes;
- no plaintext secrets, tokens or raw model arguments in output;
- secret values represented by versioned reference IDs or keyed hashes;
- `simulated_live_schema_sha256` is explicitly labelled simulated, never
  “verified live”;
- changing any bound field changes the digest.

## Evaluator flow

1. Load proposal/approval through a trusted read-only boundary.
2. Validate stored proposal/approval hashes, identity version, state, expiry,
   revocation and execution allowance.
3. Revalidate current policy ID/version/hash and exact operation.
4. Revalidate current agent identity, owner, capability, trust and contract.
5. Revalidate current account state for proposer/approver and current source
   ownership or external-resource scope.
6. Compare frozen profile attributes with supplied simulated live attributes.
7. Detect duplicate submission intent using pure test data.
8. Return `would_refuse` with bounded reason codes, or
   `validation_passed_for_simulation`.

## Mandatory refusal cases

- missing/disabled/wrong-version/wrong-hash policy;
- changed/removed agent, capability, trust, contract or owner;
- disabled/deleted/revoked principal or approver;
- missing/unverified/transferred source ownership;
- wrong external resource;
- changed transport executable/args/env reference;
- changed HTTP URL/TLS/redirect/private-network identity;
- changed frozen or simulated live-schema digest;
- expired/revoked/exhausted/missing approval;
- duplicate submission intent;
- tool without idempotency/reconciliation support;
- corrupt snapshot, malformed canonical arguments, unknown identity version;
- simulated crash before send, after hypothetical send, response timeout;
- concurrent evaluation of one approval;
- cancellation before a hypothetical send.

## What simulation may not do

- write `MCPMutationDispatchAttempts`;
- change proposal to `dispatching`, `uncertain`, `succeeded`, `failed` or
  `reconciliation_required`;
- claim a lease, start a worker, retry or reconcile;
- call a mutating `tools/call`, even against a fixture;
- issue/revoke real approvals;
- use production credentials;
- expose a public operator API;
- change schema 1.17;
- present simulation success as approval, dispatch success or goal completion.

## Hypothetical uncertainty states

Simulation may label:

- `simulated_refused_before_send`;
- `simulated_would_send_once`;
- `simulated_uncertain_after_send`;
- `simulated_duplicate_intent`.

These are pure result labels, never durable dispatch states.

## Tests to write after approval

- all refusal cases above;
- profile digest determinism and field sensitivity;
- secret redaction;
- concurrent evaluations;
- zero dispatch attempts, zero dispatch states, zero mutation counter;
- no false goal evidence;
- schema/release unchanged.

## Schema assessment

No schema change is made. A future real executor likely needs a durable
execution-profile digest/snapshot, but that is outside Option A and requires a
separate schema decision.
