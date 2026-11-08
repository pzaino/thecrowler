# Crawl scheduling foundation (WP-03B1, hardened by WP-03B2A)

Status: internal foundation, reviewed separately from WP-03A. Not exposed to
models. No model-facing state-changing tool is registered.

## 1. Existing scheduler lifecycle

The existing scheduler remains authoritative:

1. An eligible source is marked `pending` by `ScheduleSourceCrawl()` or the new
   idempotent scheduling API.
2. The engine claims eligible sources through the existing
   `update_sources(...)` database function. A successful claim atomically
   changes `status` to `processing` and records the engine ID.
3. The crawler sets `completed` or `error` (with `last_error` and
   `last_crawled_at`) when the crawl finishes.
4. Disabled sources are never claimed.
5. `processing` is never reset to `pending` by a scheduling hint.

The scheduling foundation does not start crawler goroutines, create an
independent queue, allocate Selenium sessions, or bypass `update_sources`.

Observed source states are `new`, `pending`, `processing`, `completed`,
`error`, plus the synthetic `disabled` and `missing` states used in results.

## 2. Internal API

```go
result, err := cdb.ScheduleSourceCrawlRequest(ctx, &db, cdb.CrawlScheduleRequest{
    RequestID:        "logical-request-1",
    RunID:            "run-1",
    PrincipalUserID:  7,
    AgentID:          "agent-1",
    Owner:            "owner-1",
    SourceID:         42,
    AllowedSources:   []uint64{42}, // approved source scope
    OperationType:    cdb.CrawlOperationType, // "crawl"
    ParametersSHA256: proposalHash,
    ApprovalID:       "approval-1",
    ApprovalSHA256:   approvalHash,
    ApprovedBy:       "operator-1",
    ApprovedAt:       approvedAt,
    ExpiresAt:        expiresAt,
    MaxRequests:      1,          // required trusted quota, 1..100
    BudgetWindow:     time.Hour,  // required, up to 24h
})
```

The request is internal and trusted. It is not a model tool, an HTTP endpoint
or a public capability. `ScheduleSourceCrawl()` remains unchanged for existing
mail-listener and other internal callers.

The exported API reads the trusted runtime clock internally. There is no
caller-supplied `now` field. Deterministic tests use the package-internal
clock-injected scheduling function; production code cannot select the clock.
Approval timestamps and request lifetimes are bounded to 24 hours, and both
`MaxRequests` and `BudgetWindow` must be positive and within the supported
range. There is no way for a model or caller to disable the quota with
`MaxRequests = 0`.

### Result

`CrawlScheduleResult` carries:

- `OperationID` — deterministic durable operation identity;
- `RequestID`, `RunID`, `PrincipalUserID`, `AgentID`, `Owner`, `SourceID`,
  `OperationType`;
- `State` — scheduling-request state;
- `ReasonCode` — stable machine-readable code;
- `SourceState` — the source row observed during this operation;
- `SourceObservedAt`, `ExpiresAt`;
- `Reused` — true when a duplicate request returned an existing operation.

Operation states include: `proposed`, `denied`, `accepted`,
`already_pending`, `processing`, `completed`, `failed`, `cancelled`,
`expired`, `disabled`, `not_found`, `conflict`, `unsupported`.

`completed` and `failed` are reserved for future observation of the crawl
execution; the scheduling foundation does not claim crawl completion from a scheduling
acknowledgment. The operation state and source state are intentionally separate.

Common scheduling reason codes:

| Result | Reason |
| --- | --- |
| accepted | `crawl_scheduled` |
| already pending | `crawl_already_pending` |
| processing | `crawl_processing` |
| disabled | `crawl_source_disabled` |
| missing | `crawl_source_not_found` |
| duplicate with changed parameters | `crawl_request_conflict` |
| quota exhausted | `crawl_request_budget_exhausted` |
| source scope missing | `crawl_source_scope_required` |
| source outside scope | `crawl_source_out_of_scope` |
| source authorizer unavailable | `crawl_source_authorizer_unavailable` |
| source ownership missing | `crawl_source_ownership_missing` |
| source owned by another principal | `crawl_source_forbidden` |
| other source authorization failure | `crawl_source_authorization_error` |
| request lifetime expired | `crawl_request_expired` |
| invalid request | `crawl_request_invalid` |
| cancelled/deadline | `crawl_request_cancelled` / `crawl_transaction_timeout` |
| database failure | `crawl_database_error` (sanitized) |
| backend without durable store | `crawl_store_unsupported` |
| approval unknown to authority | `crawl_approval_unknown` |
| approval issuer untrusted | `crawl_approval_issuer_untrusted` |
| approver not authorized | `crawl_approval_approver_unauthorized` |
| approval store unavailable | `crawl_approval_store_unavailable` |
| approval lifetime invalid | `crawl_approval_lifetime_exceeded` |

Approval-policy reason codes are documented in section 4.

## 3. Idempotency and concurrency

The scheduling foundation uses the existing durable `Status` table. No schema migration or new
table was introduced. The fields used are `status_sha256` (deterministic
primary key), `agent_id`, `source_id`, `operation_type`, `status`, `details`
(JSONB in PostgreSQL, JSON text in SQLite), `created_at` and
`last_updated_at`.

- `status_sha256` is deterministic:
  `owner | agent_id | source_id | operation_type | request_id`.
- `RunID` and `PrincipalUserID` are stored in the operation details and must
  match on retry. A changed run, principal, approval fingerprint or parameter
  hash for the same key returns `conflict` and does not mutate the source.
- Agent-originated requests require `principal.Username == AgentIdentity.Owner`.
  Because `Users.username` is unique, the owner component of the operation ID
  is a unique principal label.
- `AllowedSources` is required and is rechecked by the database adapter before
  any source mutation. It is populated only after the agent policy layer has
  approved the proposal against the full run scope.
- Same key + identical parameters and approval returns the existing operation
  with `Reused=true`; it does not schedule again.
- Same key + different source, operation, parameters, approval metadata or
  request lifetime returns `conflict`; no mutation occurs.
- Different owners/agents produce different operation IDs even with the same
  logical request ID.
- Concurrent identical requests use a transactional insert reservation; one
  inserts the operation, the other re-reads after taking the source lock and
  returns the same outcome, even when the quota is now exhausted. A single
  operation row remains.
- A processing source remains processing. A pending source returns
  `already_pending`. Neither is reset.
- An accepted operation remains accepted after the source later completes.
  Replaying the same request returns the existing operation and observes the
  current source state; it does not schedule a new crawl. A new request ID and
  a new approval are required for a new logical request.
- Disabled and missing requests are retained as operation records and cannot be
  silently replayed as a new request with the same key.
- Request expiry creates no row and performs no mutation.
- A transaction failure rolls back both the operation row and any source
  update.

### Owner-isolated quotas

`MaxRequests`/`BudgetWindow` enforce a trusted operator quota for the exact
agent, owner, source and operation type. The quota query filters all four
fields in SQL before applying `LIMIT`, using `details->>'owner'` on PostgreSQL
and `json_extract(details, '$.owner')` on SQLite. Other owners' operations
therefore never consume the query's limit and never hide the requesting owner's
records.

Only `accepted`, `already_pending` and `processing` operation records count as
consumed quota. Malformed records or records without an owner for the same
agent/source/operation/window fail the request closed instead of being skipped.
The source row is locked during the budget check and decision on PostgreSQL, so
concurrent requests cannot both slip under the quota; concurrent requests from
different owners each receive their own independently evaluated quota.

A retry of an already-accepted logical request is returned from its idempotency
record without consuming another quota slot, even when the quota is now
exhausted. A budget denial creates no operation row and does not mutate the
source, so it cannot be mistaken for an accepted request.

The quota window uses the trusted runtime clock and compares it with the
database `created_at` column. Deployments should keep the application and
database clocks synchronized; a future-dated operation record still counts
against the quota rather than disappearing from the window.

The `Status` table is present in the PostgreSQL and SQLite schemas. MySQL's
current schema does not contain it, so the new API fails closed with
`crawl_store_unsupported` on MySQL; `ScheduleSourceCrawl()` remains available
for legacy MySQL callers but does not claim durable idempotency.

No migration is required for this implementation. If maintainers prefer a
dedicated crawl-request table instead of the generic existing `Status` table,
that must be a separately approved WP-03B2B migration; the internal API and
result contract can be retained.

## 4. Proposal, allowlist, ownership and approval boundary

`CrawlRequestProposal` is bounded, non-executable data:

- request ID (the trusted runtime generates it for future model-facing use);
- source ID;
- operation type (`crawl` only);
- expiry, bounded to the next 24 hours.

`CrawlRequestPolicy` is trusted run configuration, never populated from model
arguments. It carries:

- `AllowedOperations` — a frozen set of permitted operation names; the trusted
  host must not mutate it for the lifetime of the run;
- `AllowedSources` — the approved source scope;
- `Principal` — the authenticated operator the agent acts for;
- `SourceAuthorizer` — the trusted source-ownership boundary;
- an optional `CrawlApprovalVerifier` trusted authority;
- `MaxRequests` and `BudgetWindow`.

`CrawlRequestApproval` is an approval record presented for verification. It is
bound to the proposal fingerprint, run ID, agent ID, owner, source, operation,
approval ID, issuer, approver, approval time and expiry. Its fields are only a
lookup key; the approval is not trusted until an authoritative verifier
resolves it.

`AuthorizeCrawlRequest()` requires:

- trusted agent identity and owner;
- non-empty run ID;
- a valid principal whose username equals the agent owner;
- trust level `trusted` or above;
- `tool_execution` and `db_write` capabilities;
- an explicit operation allowlist containing `request_source_crawl`;
- an explicit source scope containing the proposal source;
- no matching `tools` / `tool:request_source_crawl` contract prohibition;
- a positive bounded quota and budget window;
- successful source-ownership authorization for the same source;
- a current proposal;
- an explicit approval bound to the exact proposal, run and identity;
- a current approval;
- successful verification by the trusted approval verifier, including the
  approver's source authority.

Contract prohibition overrides the allowlist. A missing or empty operation
allowlist fails closed. The `all` capability satisfies capability checks but
never the operation allowlist, source scope, ownership, approval or contract
restrictions. A new run, operation or modified proposal/approval cannot reuse
an approval.

### Trusted approval and ownership

WP-03B2A adds:

- `CrawlPrincipal` and `CrawlPrincipalFromAuthIdentity()`;
- `CrawlSourceAuthorizer` and `DatabaseCrawlSourceAuthorizer`;
- `CrawlApprovalStore`, `CrawlApprovalIssuer`, `CrawlApprovalRevoker`;
- `StoreBackedCrawlApprovalVerifier`.

A nil source authorizer or approval verifier fails closed. A missing or zero
source owner fails closed. A database lookup failure is never a permission
grant. A matching approval structure is not sufficient: the verifier must
resolve the approval in an authoritative store, match every binding, check
revocation and verify the approver's source authority.

There is no durable operator approval store, signing service, API or UI in
this chunk, and `/v1/source/add` still accepts a caller-supplied `usr_id`.
Those are documented production contracts in
[`crawl_request_authorization.md`](crawl_request_authorization.md). Until they
are approved and wired, the model-facing scheduling tool must remain
unregistered.

### Decision integrity

All authorization-relevant decision fields are unexported, including the
verified flag, run, principal user ID, fingerprints, quota and outcomes.
Read-only accessors expose audit data. `BuildCrawlScheduleRequest()` rechecks
the decision, proposal, approval, identity, run, principal and expiry
bindings before constructing a database request. A denied, mismatched,
cross-run, expired or altered decision cannot produce a submittable request.

`AuthorizeCrawlRequest()` and `BuildCrawlScheduleRequest()` read the trusted
runtime clock and expose no caller-controlled time argument. Deterministic
tests use unexported clock-injected variants, so future model-facing code
cannot supply a model clock.

A model cannot authorize its own request; no model-facing tool is registered in
this chunk.

## 5. Audit

`JobEngine.AuditCrawlSchedulingDecision()` records:

- trusted wall-clock timestamp;
- agent ID, owner, principal user ID, run ID, trace ID;
- source ID, request ID, operation type, idempotency/operation key;
- authorization stage and outcome;
- approval outcome and authorization reason code;
- source-ownership outcome;
- scheduling state and overall scheduling outcome;
- stable reason code.

Stable authorization stages include `proposal_received`, `policy_checked`,
`source_verified`, `source_denied`, `approval_required`,
`approval_verified`, `approval_rejected`, `authorized`, `submitted`,
`reused`, `submission_denied` and `submission_failed`. Approval outcomes are
`not_requested`, `verified`, `rejected`, `revoked`, `expired` and
`unavailable`. Source outcomes are `not_checked`, `verified`, `missing`,
`denied` and `unavailable`.

`AuditEvent` gained additive `authorization_stage`, `approval_outcome`,
`source_ownership_outcome` and earlier correlation fields with `omitempty`;
existing audit serialization retains its shape. The timestamp uses the
runtime clock at audit time. In-memory audit events are not durable storage.
No prompts, model text, credentials, SQL or raw configuration are copied into
the audit.

## 6. Future WP-03B2B integration

WP-03B2B may expose a single model-facing proposal tool only after review. It
must:

1. accept only the proposed `source_id`;
2. generate the logical request ID from trusted run and tool-call correlation;
3. obtain an explicit approval from a trusted authority bound to the proposal,
   identity, principal and run;
4. build a trusted `CrawlRequestPolicy` with a frozen operation allowlist,
   source scope, principal, source authorizer, positive quota and approval
   verifier;
5. call `AuthorizeCrawlRequest()`;
6. call `BuildCrawlScheduleRequest()` and then the database API;
7. return only the accepted/already-pending/processing acknowledgement;
8. observe completion later through the existing read-only source-status and
   evidence tools;
9. never let a model supply approval, issuer, approver, owner, principal,
   tenant, DB, scope, allowlist, clock or budget fields.

The proposed input and result contract, effect classification and evidence
semantics are documented in
[`crawl_request_authorization.md`](crawl_request_authorization.md). The tool is
not registered.

Before WP-03B2B, maintainers must also approve:

- the durable `Status`-table reuse or a dedicated migration;
- a durable, trusted operator approval store, issuer and revocation policy;
- authenticated source-ownership population and principal resolution;
- the exact model-facing tool schema and source-scope rules.
