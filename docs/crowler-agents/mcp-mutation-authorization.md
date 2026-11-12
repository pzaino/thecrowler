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
approval. The in-memory stores remain test-only. WP-04C2-B adds a durable
PostgreSQL/SQLite proposal and approval store plus authenticated operator
approval routes; it does not dispatch.

## 7. Persistence decision

WP-04C1 used `MCPMutationProposalStore` and `InMemoryMCPMutationProposalStore`
for deterministic tests. WP-04C2-B adds an additive schema 1.17 capability with:

- immutable proposal identity, run/agent/principal/server/tool bindings;
- canonical argument bytes and proposal/argument fingerprints;
- policy ID, version, hash and a bounded policy snapshot;
- source/external-resource scope;
- proposal state and expiry;
- durable approval identity, expiry, revocation and execution allowance;
- a dispatch-attempt table reserved for the future dispatcher;
- append-only audit records and per-run/per-principal quota counters;
- a capability marker that distinguishes older 1.17 databases from databases
  with this additive capability.

`CrawlProposals` and `CrawlApprovals` are crawl-specific and are not reused.
The dispatch-attempt table exists but is never written by this phase: no
dispatcher, worker or remote mutation is enabled. No schema release beyond 1.17 is introduced by this work.

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

WP-04C1 created `approval_required` proposals and verified approval contracts.
WP-04C2-B durably stores `approval_required` and `approved` proposals and adds
operator denial/revocation. It does not enter `dispatching` or perform external
calls.

A future WP-04C2 dispatcher must treat a lost response as `uncertain` or
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
- The in-memory stores are test-only and are not restart-safe. WP-04C2-B adds
  a durable PostgreSQL/SQLite store, but dispatch remains disabled.
- MCP annotations remain untrusted hints.
- Custom NAT64/translation gateways still require deployment-level egress
  controls; this document inherits WP-04B's network limitations.

## 15. Schema 1.17 extension design baseline

The DDL below is the earlier design baseline. The implemented capability uses
exact `BYTEA`/`BLOB` canonical bytes, quota counters and a capability marker as
described in section 21 and
`docs/crowler-agents/reviews/WP-04C2C_SCHEMA_API_REVIEW.md`; the original
local design artifact `artifacts/agentic/WP-04C2A_SCHEMA_1_17_DESIGN.md` is
gitignored. It is present in new
additive migration scripts and the clean-install setup, but has not been
applied to a shared or production database in this worktree. It extends schema
1.17. `CrawlProposals` and `CrawlApprovals`
remain crawl-specific.

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

## 18. Approval API contract (implemented for WP-04C2-B)

Authenticated operator routes are registered under
`/v1/console/mcp/mutations/proposals`:

- list and detail;
- approve;
- deny;
- revoke;
- bounded dispatch-attempt history (always empty while dispatch is disabled).

Every route resolves the current account through `auth.ResolveActiveIdentity`
and reloads roles/scopes from the database. Approval and observation require
either administrator authority or verified ownership of every source in the
proposal scope; external-resource proposals require administrator authority.
The routes bind the exact proposal fingerprint and identity version, clamp the
approval expiry to the proposal lifetime, permit one execution for the initial
non-idempotent mode, record the revoking principal and return only bounded
metadata. Model-supplied approval identities are never accepted.

There is deliberately no propose, dispatch or reconciliation route in this
phase. The proposal tool remains test-registered only.

## 19. WP-04C1 audit vocabulary

Implemented bounded audit fields include:

```text
mcp_mutation_proposal_created
approval_required
mcp_proposal_id
mcp_proposal_sha256
mcp_policy_id
```

WP-04C2-B records durable proposal-created, proposal-denied, proposal-expired,
approval-issued and approval-revoked audit events with bounded redacted detail.
Future dispatch/reconciliation work must add dispatch-claimed, succeeded,
failed, uncertain and reconciliation-required events.

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

The in-memory store is test-only. The durable PostgreSQL/SQLite implementation
is described in section 21; dispatch, uncertain-outcome and reconciliation SQL
in the local gitignored `artifacts/agentic/WP-04C2A_SQL_TRANSACTIONS.md`
remains design-only; the tracked summary is
`docs/crowler-agents/reviews/WP-04C2C_SCHEMA_API_REVIEW.md`.

## 21. Implemented WP-04C2-B durable persistence and approval API

### Schema 1.17 capability

The durable objects are additive within schema 1.17:
`MCPMutationProposals`, `MCPMutationApprovals`,
`MCPMutationDispatchAttempts`, `MCPMutationAudit`,
`MCPMutationRunQuota`, `MCPMutationPrincipalQuota` and
`SchemaCapabilities`.

The capability is named `mcp-mutation-authorization-v1` and carries checksum
`00cb16b3efc2a3a619335c4e3bc2b1c470bb34b2378d249a81e746cddc704eed`.
The additive scripts are:

- `pkg/database/db_migrations/postgresql-mcp-mutation-v1.17.pgsql`
- `pkg/database/db_migrations/sqlite-mcp-mutation-v1.17.sqlite3`

The same DDL is present in the clean-install PostgreSQL and SQLite setup
scripts. `RequiredSchemaVersion` remains `1.17` and the writer generation
is `verified-ownership-v1.17`.

A database that advertises 1.17 but lacks the capability fails closed when a
durable MCP mutation store or route is used. The global startup compatibility
check is unchanged so read-only deployments can still start; the mutation
routes return `503` until the capability is present. There is no automatic
runtime DDL application.

Canonical argument bytes are stored as `BYTEA`/`BLOB`; `JSONB`/`TEXT` is used
only for bounded policy snapshots and redacted audit detail. This preserves the
exact bytes a future dispatcher must submit.

### Atomic admission and quota counters

`DatabaseMCPMutationProposalStore.Admit` uses one transaction:

1. ensure and lock the run/principal quota rows in a fixed order;
2. expire a matching expired proposal and decrement its counters;
3. return an identical fingerprint replay without refreshing expiry;
4. reject a changed fingerprint for the same run/effective-call ID;
5. check the per-run and pending-per-principal counters;
6. insert the immutable proposal and increment both counters;
7. append a bounded audit event.

The in-memory store remains the deterministic test implementation. The durable
store is the PostgreSQL/SQLite production-shaped implementation. MySQL is not
supported for durable MCP mutation persistence in this release.

Quota counters are decremented on denial and on the expiry path exercised by a
lookup/admission/approval. There is no background expiry reaper in this phase.
If a proposal expires without a later transition, its counters may remain
temporarily strict; the implementation never over-admits. A maintenance reaper
is a future decision.

### Approval lifecycle

Approval issuance locks the quota rows and the proposal, verifies the exact
proposal fingerprint/identity version and state, rejects expired proposals,
inserts one active approval, moves the proposal to `approved` and appends an
audit event. Re-approval after revocation is allowed; a second active approval
for the same proposal is rejected. Approval expiry is clamped to the proposal
expiry and the initial mode permits one execution.

Denial moves the proposal to `denied`, decrements counters and revokes any
active approval in the same transaction. Revocation records the operator and
reason without deleting approval history. The verifier remains a binding check,
not dispatch authorization.

### Operator routes

The implemented routes are listed in section 18. There is no propose, dispatch,
execute or reconciliation route. The attempts route returns an empty list and
`dispatch_enabled: false`. The mutation proposal tool remains test-registered
only; no agent manifest or production agent path can create a durable proposal
in this phase.

### Operational note

Apply the v1.17 migration explicitly to an existing 1.16 database after
maintainer approval, or use the clean-install setup for a fresh database. The
script is idempotent and does not alter crawl proposals, crawl approvals,
sources, events or ownership history. Do not apply it to a shared or production
database without the normal deployment review.

## 22. WP-04C2-C stabilization

This section records the review and hardening applied after the first durable
persistence/API implementation. The additive schema and operator routes remain
**not deployment-approved**; no shared or production database was migrated.

### PostgreSQL integration isolation

The PostgreSQL integration test no longer truncates caller-selected tables. It
requires `CROWLER_MCP_POSTGRES_TEST_DISPOSABLE=1`, creates a uniquely named
throwaway database, applies the additive migration there, runs the tests, and
drops only that database. A maintenance-database sentinel proves unrelated rows
are not affected. Missing opt-in or DSN fails/skips closed.

### Approval expiry and proposal reopening

An "active" approval now means an unrevoked, unexpired approval with remaining
execution allowance, attached to a live proposal in `approved`. Reads require a
trusted `now`. Approval issuance classifies a stale active row as
`expired`/`exhausted`, audits the transition and reopens a live proposal to
`approval_required` before inserting a replacement. Revocation and denial use
the same lock ordering and leave no usable approval behind. Read responses
derive `approval_required` when a stored `approved` proposal has no usable
approval; the transaction persists that state on the next approval action.

### Corrupt durable proposals

Stored proposals are now validated at the agent/store/API boundary. Invalid
identity versions, hashes, canonical arguments, trailing/duplicate/noncanonical
JSON, policy snapshots, scopes, lifetimes, terminal timestamps or fingerprints
produce `ErrMCPMutationProposalCorrupt` instead of a partially constructed
proposal. The operator API returns a bounded `503` and refuses approval. The
policy snapshot is validated by decoding into the typed policy and re-marshaling
before hash comparison, which tolerates PostgreSQL `JSONB` key normalization.

### Tenant-aware pagination

Listing applies the caller's visibility predicate in SQL before `LIMIT`.
Non-admin viewers see only source-scoped proposals where every source has
verified ownership; external-resource proposals are administrator-only.
Administrators can observe all proposals, including after ownership provenance
changes, and retain emergency deny/revoke authority. Approval still requires
current proposal-principal and source ownership. Cursor ordering remains
`(created_at, proposal_id)` and filter/cursor lengths are bounded.

### Quota parity

In-memory admission now performs the same exact run/principal expiry sweep as
SQL admission. SQL admission has an explicit clock seam for deterministic tests.
Counter consistency is checked after denial/expiry, and SQLite restart
persistence is tested.

### Production proposal gate

A production inert proposal producer is **not enabled**. The runtime has typed
`Principal` fields, but no production constructor installs an authenticated
principal; manifest owner strings are not principals, and no background
service-principal contract exists. There is also no approved public YAML/JSON
loader for the full `MCPMutationPolicy`; the existing
`allow_mutation_proposal` flag is only a discovery/approval flag. The proposed
minimal public contract and principal-provenance design are in
`docs/crowler-agents/reviews/WP-04C2C_AUTHORIZATION_MODEL.md`; they require
maintainer approval before implementation.

### SQLite foreign keys

The SQLite schema declares foreign keys, but the runtime connection string does
not enable `_foreign_keys=on`. The setup/migration pragma applies only to those
connections. This is a maintainer decision; this patch does not change global
SQLite behavior.

No dispatcher, dispatch claim, remote mutation, retry or reconciliation worker
was added. The dispatch-attempt table remains unused.
