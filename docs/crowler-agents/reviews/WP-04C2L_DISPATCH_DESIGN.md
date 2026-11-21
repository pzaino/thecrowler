# WP-04C2L Pre-Dispatch Security Design

## Status

**Design only. No dispatch code, state transition or remote mutation call was
added.** `MCPMutationDispatchAttempts` remains unused. This document specifies
what a future execution milestone must prove before the first backend is
approved.

## What an approval would authorize

An approval currently binds a proposal identity, policy identity, approver,
principal, source/resource scope, canonical arguments and expiry. That is not
yet sufficient to authorize a live remote side effect.

A future execution profile must bind, at minimum:

### Identity and target

- approval ID, proposal ID and proposal fingerprint;
- run ID, trace ID, agent ID and owner;
- principal user/issuer and approver user/issuer;
- server ID, raw tool name and exact derived alias.

### Transport

- transport type;
- stdio executable path or verified image/binary identity;
- arguments, working directory and environment allowlist;
- effective security-relevant environment values represented by keyed hashes or
  versioned secret references, never plaintext;
- HTTP URL, redirect policy, TLS requirements, auth mode, private-network
  restrictions, certificate trust and DNS-rebinding controls.

### Tool and policy

- frozen tool JSON Schema hash;
- live schema hash and effect classification at revalidation;
- policy ID, version and canonical hash;
- operation constraints, allowed resources/sources and transports;
- approval and audit requirements;
- `execution_backend` and backend identity.

### Scope and fingerprint

- canonical argument bytes/hash and argument field constraints;
- source IDs or exact external resource;
- proposal fingerprint and approval fingerprint;
- current approval state, expiry, revocation and execution count;
- idempotency/submission identity.

## Proposed execution-profile identity

Use a versioned digest:

```text
execution_profile = SHA-256(
  domain="crowler-mcp-execution-profile-v1" ||
  canonical JSON of every field above
)
```

The canonical JSON must use sorted keys, fixed numeric forms, explicit nulls and
bounded lengths. Secret values are represented as keyed hashes or references,
not plaintext. The digest must be stored where a dispatcher can compare the
authorized profile with the live profile.

The existing schema has `submission_identity` and `backend_identity` on
`MCPMutationDispatchAttempts`, but no dedicated execution-profile column. A
future additive schema change may be required. It must be separately approved
and remain on the schema-1.17 line unless that decision changes.

## Revalidation points

A future executor must revalidate immediately before the remote call and again
after any wait/lease acquisition:

1. proposal still exists, state is approved or dispatch-claimed as allowed;
2. approval is active, unexpired, unrevoked and not exhausted;
3. current policy ID/version/hash and operation still match;
4. current agent identity/owner/capability/trust still match;
5. approver and principal accounts are current, enabled and non-deleted;
6. source ownership is still verified and current;
7. live server/tool inventory and schema hash match the profile;
8. secret references still resolve and transport profile matches;
9. approval execution count and idempotency state permit one attempt;
10. no unresolved dispatch attempt exists for the proposal.

Any mismatch fails closed with no remote call.

## Claim and lease design

Do not implement until approved:

- one additive dispatch-attempt row per submission identity;
- a unique unresolved-attempt constraint for the proposal;
- `SELECT ... FOR UPDATE`/conditional update to claim;
- short lease with owner identity and expiry;
- heartbeat only if the execution protocol supports it;
- a claimed row cannot be re-claimed by another worker before lease expiry;
- expired lease becomes `uncertain`, never silently retried.

## Exactly-once intent versus side effects

The system can guarantee at most **exactly-once durable submission intent**. It
cannot prove exactly-once external effects unless the remote tool exposes a
trusted idempotency key and queryable result.

Required behavior:

- tools with no idempotency/reconciliation support are refused by default;
- a remote timeout or connection loss becomes `uncertain`, not `failed` and not
  `succeeded`;
- `uncertain` is never replayed automatically;
- reconciliation requires an operator or a tool-specific trusted lookup;
- an execution-count increment happens only at the same commit that creates the
  dispatch claim;
- duplicate submissions with the same idempotency key reuse the existing
  attempt row.

## Approval expiry, revocation and cancellation

- If approval expires or is revoked before claim, no remote call starts.
- If revocation happens during an in-flight call, the result is recorded but
  the attempt is not treated as newly authorized.
- Cancellation before the remote request is sent prevents the call.
- Cancellation after bytes may have reached the remote server becomes
  `uncertain` unless the protocol can prove no side effect.
- A timeout does not become a false success.

## Secrets and least privilege

- Executor gets only the server-level secret references needed for one call.
- Secrets are never written to audit, logs, proposal rows or artifacts.
- Environment values are allowlisted and hashed in the profile.
- TLS trust, redirects and private-network restrictions are enforced.
- DNS resolution is pinned or rechecked to prevent rebinding.
- The executor identity is separate from the approval operator and has no
  broader source/account administrative authority.

## Audit and evidence

Additive future audit events:

```text
mcp_mutation_dispatch_claimed
mcp_mutation_dispatch_succeeded
mcp_mutation_dispatch_failed
mcp_mutation_dispatch_uncertain
mcp_mutation_reconciliation_required
mcp_mutation_reconciled
```

Each record must include attempt ID, proposal/approval IDs, profile hash,
backend identity, bounded reason code and timestamps. No raw arguments, secrets
or response bodies.

A goal must never be marked complete from proposal or approval state alone. Only
a verified external result, or an explicit non-mutating validation result, can
satisfy completion evidence.

## Schema assessment

The existing capability already has:

- proposal and approval lifecycle;
- `MCPMutationDispatchAttempts` with claim/lease/reconciliation columns;
- unique unresolved-attempt index;
- audit vocabulary space.

Missing or insufficient for a real executor:

- a stable execution-profile digest;
- a policy/inventory/profile snapshot at dispatch time;
- explicit idempotency/reconciliation capabilities per tool;
- a durable submission-intent record for non-idempotent refusal policy.

A future design must decide whether these fit in additive 1.17 columns/tables or
require a later schema line. **No schema change is made in WP-04C2L.**

## Required future tests

- two workers claim one proposal: one claim, one `uncertain`/no call;
- approval revoke before claim;
- policy/agent/ownership change before claim;
- secret revocation before call;
- timeout after send: `uncertain`, no automatic replay;
- remote duplicate detection;
- cancellation before send vs after send;
- executor crash before/after remote call;
- audit redaction;
- no dispatch attempt on denied/expired/revoked approval;
- no goal success from approval state.
