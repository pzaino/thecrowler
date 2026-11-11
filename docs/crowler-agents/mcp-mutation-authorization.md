# MCP mutation authorization foundation (WP-04C1)

Status: **WP-04C1 is proposal-only.** It classifies mutation tools, defines the
trusted policy and approval contracts, and creates immutable inert proposals.
It does **not** execute remote mutations, expose production proposal storage,
or expose a public approval-to-dispatch endpoint. Actual MCP mutation execution
is deferred to WP-04C2.

## 1. Mutation categories

CROWler distinguishes three separate mutation classes:

1. **Native CROWler mutation** — crawl requests, source changes and
   configuration records. These remain under the existing WP-03 native
   authorization, approval, persistence and scheduling contracts.
2. **External MCP mutation** — an operation on an external system reached
   through an MCP server. A CROWler database transaction cannot make this
   atomic, and a remote server cannot grant CROWler authority.
3. **CROWler mutation exposed through a future MCP server** — must reuse
   native authorization and scheduling semantics. A network adapter must not
   bypass them.

An MCP mutation proposal is never permission to invoke a native CROWler
mutation. `request_source_crawl` remains a separate native workflow.

## 2. Trusted effect classification

`mcp.servers[].approved_tools[]` now contains an explicit effect contract:

```yaml
approved_tools:
  - name: create_record
    effect: mutation
    allow_mutation_proposal: true
    read_only: false
    description: Propose an external record creation.
    required_capabilities: [tool_execution]
```

Rules:

- Existing `read_only: true` configurations remain compatible and normalize to
  `effect: read_only`.
- `read_only: false` without an explicit effect is rejected.
- Unknown effects and contradictory combinations are rejected.
- A mutation tool requires `allow_mutation_proposal: true`; mutation support is
  disabled by default.
- Server annotations (`readOnlyHint`, `destructiveHint`, `idempotentHint`,
  `openWorldHint`) are only hints and never authority.
- A mutation descriptor is frozen during discovery and is never registered
  through `AgentToolRegistry.Register()`.
- The proposal adapter is explicitly `ToolEffectMutation` and must be
  registered with `RegisterMutation()`.
- Reconnection/re-discovery revalidates and freezes metadata; it cannot expand
  the operator-approved inventory.

## 3. Proposal-only execution contract

`MCPMutationProposalTool.Execute()`:

- validates the shared run allowlist, mutation-operation allowlist,
  capabilities, trust, contract, source/resource scope, arguments and budget;
- creates an immutable `MCPMutationProposal`;
- records it in the configured proposal store;
- returns `approval_required` plus a proposal ID and fingerprints;
- **never calls the MCP transport's `tools/call`**.

The mutation descriptor has no `Execute` method. The read-only `mcpAgentTool`
path cannot be used to propose a mutation, and the proposal path cannot be used
to execute one.

## 4. Policy contract

The WP-04C1 policy is a trusted Go contract, not yet a public agent YAML field:

```go
MCPMutationPolicy{
    Enabled: true,
    PolicyID: "operator-policy",
    Version: "1",
    Operations: []MCPMutationOperationPolicy{{
        ServerID: "remotewrite",
        ToolName: "create_record",
        ToolAlias: "mcp_remotewrite_create_record",
        AllowedAgents: []string{"mutation-proposer"},
        RequiredCapabilities: []string{"tool_execution"},
        MinTrustLevel: "trusted",
        AllowedResources: []string{"external-resource-7"},
        RequiredArgumentFields: []string{"resource"},
        AllowedArgumentFields: []string{"resource"},
        Destructive: true,
        ProposalTTL: 30 * time.Minute,
        MaxProposalsPerRun: 4,
        MaxPendingProposals: 8,
        MaxInputBytes: 4096,
        MaxOutputBytes: 4096,
        RequireApproval: true,
        ExecutionBackend: "none",
        AllowedTransports: []string{"stdio", "streamable_http"},
        AuditRequired: true,
    }},
}
```

The policy separates:

- discovery permission (operator inventory);
- proposal permission (exact agent and operation);
- approval permission (trusted operator contract);
- execution permission (**not implemented in WP-04C1**);
- outcome observation permission (future trusted verification).

No single boolean grants all five.

## 5. Proposal identity and canonicalization

A proposal binds trusted run/call identity, agent and owner, authenticated
principal, server/raw tool/namespaced tool, transport identity, frozen schema
hash, canonical arguments, argument hash, policy identity/version/hash,
source or external resource scope, timestamps, proposal fingerprint and
approval requirement.

Arguments are canonicalized recursively with sorted object keys, normalized
numeric representations, bounded depth/field/array counts and a configured
input-byte limit. SHA-256 is an identity/integrity binding, not evidence of
approval or external completion.

The model supplies only schema-declared operation arguments. Approval IDs,
principals, run IDs, call IDs, endpoints, executables, credentials, policy,
scope, quotas, expiry and idempotency tokens are trusted runtime/operator
values.

## 6. Approval contract

`MCPMutationApproval` binds:

- approval ID and proposal fingerprint;
- run, agent, server, tool and argument fingerprint;
- policy fingerprint;
- approver user ID, username and issuer;
- approval and expiry timestamps;
- revocation timestamp;
- permitted execution count.

`MCPMutationApprovalVerifier` recomputes the proposal fingerprint and verifies
the retained canonical argument bytes before checking exact proposal/run/agent/
server/tool/argument/policy binding, approver identity, approval timestamps,
proposal and approval expiry, revocation and a positive execution allowance.
It rejects approvals that predate the proposal or are future-dated. It still
does not check current account status, a durable consumed-execution counter,
current policy or external target state, and it must never be treated as
dispatch authorization by itself.

A model cannot approve its own proposal, and a remote server cannot issue an
approval. The in-memory approval store is test-only; no public operator API or
production approval persistence is enabled.

## 7. Persistence decision

WP-04C1 deliberately uses `MCPMutationProposalStore` and `InMemoryMCPMutationProposalStore`
for deterministic tests. It does **not** claim restart recovery or production
durability. The existing `CrawlProposals` and `CrawlApprovals` tables are
crawl-specific and are not repurposed.

Production WP-04C2 persistence requires an approved additive schema 1.17
design, likely including:

- proposal identity, run/agent/principal/server/tool bindings;
- canonical argument and proposal fingerprints;
- policy snapshot and version;
- source/external-resource scope;
- state and expiry;
- approval identity and revocation;
- dispatch attempt, external correlation and reconciliation state;
- terminal outcome and audit timestamps.

That design is not applied here. No schema 1.18 is introduced.

## 8. State and uncertainty model

Semantic states:

```text
proposed
approval_required
approved
denied
expired
dispatching
succeeded
failed
uncertain
reconciliation_required
```

WP-04C1 only creates `approval_required` proposals and verifies approval
contracts. It does not enter `dispatching` or perform external calls.

WP-04C2 must treat a lost response after dispatch as `uncertain` or
`reconciliation_required`, not as failure or success. A remote mutation must
not be retried automatically after an uncertain outcome unless the external
system exposes an authoritative idempotency/reconciliation mechanism. The MCP
`idempotentHint` alone is insufficient. Exactly-once execution must not be
claimed where the remote system cannot provide it.

## 9. Evidence semantics

A proposal is not completion evidence. Approval metadata is not completion
evidence. A future server-reported success is still not necessarily
independent evidence of the target system's final state.

WP-04C1 proposal tools are not `EvidenceSource` implementations. Pending or
approved proposal metadata cannot satisfy a `tool_observation` criterion
requiring successful external mutation, and model text cannot establish
completion. WP-04C2 will need a separately approved execution/reconciliation
evidence contract.

## 10. Audit contract

The foundation records bounded audit fields:

- run ID, trace ID, agent ID and owner;
- server ID, namespaced tool alias and raw tool name;
- effective call ID;
- proposal ID, proposal fingerprint and policy ID;
- outcome/reason code and trusted timestamp.

Raw arguments, credentials, tokens and sensitive external values are not
written to audit events. The in-memory proposal/approval stores are
deterministic test storage, not a durable audit history.

## 11. Transport boundary

WP-04C1 does not change the approved WP-04B transport boundary: HTTPS,
certificate verification, exact endpoint binding, DNS/SSRF and translation
prefix filtering, redirect rejection, proxy isolation, bearer-token isolation,
response/SSE limits and per-run session ownership remain in force.

A permitted endpoint is network permission only. It does not authorize a
mutation proposal or execution.

## 12. Live Ollama proposal test

`TestMCPLiveOllamaMutationProposalOptional` uses a real Ollama model and a
local stdio MCP fixture. The model is asked to call the proposal tool; the
fixture's mutation counter must remain empty. The production action/manifest
path does not expose mutation proposals until durable storage and approval
APIs are approved.

## 13. Remaining prerequisites for WP-04C2

- Approved additive schema 1.17 proposal/approval/dispatch/reconciliation
  persistence and migration design.
- Public operator approval/revocation API with authenticated principals and
  role checks.
- Durable dispatch idempotency and external correlation.
- Explicit reconciliation policy for `uncertain` outcomes.
- Independent evidence or trusted external state verification.
- Audit durability and retention.
- Operator feature gate enabling actual mutation execution; impossible to
  activate through a model/manifest field alone.
- Full negative, concurrency, crash/restart and external-failure testing.

## 14. Limitations

- No external MCP mutation executes in WP-04C1.
- No production proposal/approval persistence or public approval API is
  implemented.
- No mutation-capable MCP tool is available through the production agent
  manifest path.
- The in-memory stores are test-only and are not restart-safe.
- MCP annotations remain untrusted hints.
- Custom NAT64/translation gateways still require deployment-level egress
  controls; this document inherits WP-04B's network limitations.

## 15. Proposed schema 1.17 extension for WP-04C2 (not applied)

The following is a **design proposal only**. It is not in any migration or
clean-install schema and must not be applied without maintainer approval.
It extends schema 1.17 and does not create schema 1.18. `CrawlProposals` and
`CrawlApprovals` remain crawl-specific.

Proposed additive tables:

```sql
CREATE TABLE MCPMutationProposals (
    proposal_id             TEXT PRIMARY KEY,
    proposal_sha256         TEXT NOT NULL UNIQUE,
    run_id                  TEXT NOT NULL,
    trace_id                TEXT NOT NULL,
    effective_call_id       TEXT NOT NULL,
    agent_id                TEXT NOT NULL,
    owner                   TEXT NOT NULL,
    principal_user_id       BIGINT NOT NULL,
    principal_username      TEXT NOT NULL,
    principal_issuer        TEXT NOT NULL,
    server_id               TEXT NOT NULL,
    raw_tool_name           TEXT NOT NULL,
    tool_alias              TEXT NOT NULL,
    transport               TEXT NOT NULL,
    transport_identity      TEXT NOT NULL,
    schema_sha256           TEXT NOT NULL,
    arguments_json          JSONB NOT NULL,
    arguments_sha256        TEXT NOT NULL,
    policy_id               TEXT NOT NULL,
    policy_version          TEXT NOT NULL,
    policy_sha256           TEXT NOT NULL,
    source_ids              BIGINT[] NOT NULL DEFAULT '{}',
    external_resource       TEXT,
    destructive             BOOLEAN NOT NULL DEFAULT FALSE,
    state                   TEXT NOT NULL,
    created_at              TIMESTAMPTZ NOT NULL,
    expires_at              TIMESTAMPTZ NOT NULL,
    approved_at             TIMESTAMPTZ,
    dispatch_attempt_id     TEXT,
    external_operation_id   TEXT,
    reconciliation_state    TEXT,
    terminal_outcome        TEXT,
    UNIQUE (run_id, effective_call_id)
);

CREATE TABLE MCPMutationApprovals (
    approval_id              TEXT PRIMARY KEY,
    proposal_sha256          TEXT NOT NULL REFERENCES MCPMutationProposals(proposal_sha256),
    principal_user_id        BIGINT NOT NULL,
    approved_by_user_id      BIGINT NOT NULL,
    approved_by              TEXT NOT NULL,
    approved_by_issuer       TEXT NOT NULL,
    policy_sha256            TEXT NOT NULL,
    approved_at              TIMESTAMPTZ NOT NULL,
    expires_at               TIMESTAMPTZ NOT NULL,
    revoked_at               TIMESTAMPTZ,
    revoked_by_user_id       BIGINT,
    revocation_reason        TEXT,
    max_executions           INTEGER NOT NULL,
    executions_started       INTEGER NOT NULL DEFAULT 0
);
```

A corresponding SQLite schema would use `TEXT` JSON and normalized approval
columns; SQLite support is required only if execution is enabled there. The
migration must be repeatable, preserve all historical records and remain within
schema 1.17. Migration and clean-install changes require separate review before
implementation.

## 16. Proposed state transitions

```text
proposed -> approval_required | denied | expired
approval_required -> approved | denied | expired
approved -> dispatching | expired | reconciliation_required
dispatching -> succeeded | failed | uncertain | reconciliation_required
uncertain -> succeeded | failed | reconciliation_required
reconciliation_required -> succeeded | failed | manual_action_required
```

WP-04C1 only creates `approval_required` and verifies approval objects. It does
not implement `dispatching` or later transitions.

## 17. Dispatch uncertainty matrix

| External scenario | Proposed state | Retry policy |
|---|---|---|
| request never left client | failed / not dispatched | retry only after new approval or safe dispatch proof |
| server rejected request | failed | no automatic retry |
| server executed and returned success | succeeded | no duplicate dispatch |
| server executed but response lost | uncertain | no automatic retry without authoritative lookup |
| client timeout while server processing | uncertain | reconcile before any retry |
| operator revokes during dispatch | reconciliation_required | no new dispatch; reconcile |
| target changed concurrently | reconciliation_required | human/authoritative verification |
| server result cannot be independently verified | reconciliation_required | do not claim success |

An `idempotentHint` is not an idempotency contract. Exactly-once behavior is
not claimed unless the external system provides an authoritative operation ID
and reconciliation API.

## 18. Approval API contract (future)

No public approval endpoint is implemented in WP-04C1. A future authenticated
operator endpoint must:

- require an active, authorized principal distinct from the proposing agent;
- bind the exact proposal fingerprint, server/tool, run/agent, argument hash,
  policy hash and source/external resource;
- enforce a bounded approval lifetime and execution count;
- support revocation and record the revoking principal;
- re-check current principal status and policy at approval and dispatch;
- never accept a model-supplied approval identity;
- return only bounded approval metadata and reason codes.

## 19. WP-04C1 audit vocabulary

Implemented bounded audit fields include:

```text
mcp_mutation_proposal_created
approval_required
mcp_proposal_id
mcp_proposal_sha256
mcp_policy_id
```

Future WP-04C2 audit events should add proposal rejected/accepted, approval
verified/denied/revoked, proposal expired, operation conflict, store unavailable,
dispatch uncertain and reconciliation-required reason codes. Durable audit
history is not claimed until the schema 1.17 persistence design is approved.

## 20. Implemented WP-04C2-A hardening

### Exact numeric canonicalization

The WP-04C1 canonicalizer converted non-integral JSON numbers to Go strings
before marshaling, which made JSON `1.25` and JSON string `"1.25"` collide. The
hardened canonicalizer retains numbers as `json.Number` values and JSON strings
as strings. The canonical bytes are the exact bytes retained in the proposal
for future dispatch.

Equivalence policy:

- `1`, `1.0`, `1e0` and `1000e-3` normalize to the JSON number `1`.
- `-0`, `0.0` and `0e5` normalize to the JSON number `0`.
- `1.25`, `1.250` and `125e-2` normalize to `1.25`.
- Numeric tokens are parsed with arbitrary-precision decimal arithmetic;
  leading zeros, trailing dots, non-JSON forms, non-finite values, excessive
  exponents, excessive precision and values outside the supported bounds are
  rejected.
- Integer-valued `float64` values outside the exact IEEE-754 integer range are
  rejected rather than silently rounded. Callers requiring exact large numbers
  must use `json.Number`.
- Unicode strings and keys are preserved as valid UTF-8; invalid UTF-8 is
  rejected. Object keys are deterministically sorted by JSON marshaling.
- The stored `Arguments` map is decoded from the canonical bytes with
  `UseNumber`; `CanonicalArguments` is the immutable byte sequence that a
  future dispatcher must use.
- Source IDs are normalized to an empty list for resource-scoped proposals, so
  a database round-trip of an empty array cannot change the proposal
  fingerprint.

Fingerprint/identity versioning is explicitly new for this hardening; WP-04C1
proposals were test-only and there is no durability migration of old
fingerprints.

### Atomic admission

`MCPMutationProposalStore.Admit` is the sole admission API. Under one store
transaction/mutex it performs:

1. exact fingerprint lookup;
2. `(run_id, effective_call_id)` conflict detection;
3. per-run active-proposal quota;
4. pending-per-principal quota;
5. immutable creation.

Separate check-then-create sequences are not used by the proposer. Admission
also rejects non-initial proposal states, a proposal ID reused for different
content, a missing trusted principal when approval is required, a proposal
fingerprint that does not match its reconstructed content, and canonical
argument bytes whose SHA-256 does not match `ArgumentsSHA256`. Those checks
prevent a caller from bypassing approval state or binding a future dispatch to
arguments that differ from the fingerprinted bytes.

Quota semantics for WP-04C2-A:

- active run quota includes `proposed`, `approval_required`, `approved`,
  `dispatching`, `uncertain` and `reconciliation_required`;
- terminal `denied`, `expired`, `succeeded` and `failed` do not consume quota;
- pending-per-principal includes `proposed`, `approval_required`, `approved`
  and `dispatching`;
- an identical fingerprint replay returns the existing immutable proposal and
  does not refresh expiry or quotas;
- an expired replay fails with `ErrMCPMutationProposalExpired`;
- the same run/effective call ID with changed arguments fails with
  `ErrMCPMutationProposalConflict`.

The in-memory store is test-only. The SQL transaction design is documented in
`artifacts/agentic/WP-04C2A_SQL_TRANSACTIONS.md` and is not applied.
