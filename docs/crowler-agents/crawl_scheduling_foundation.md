# Crawl scheduling foundation (WP-03B1)

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
    MaxRequests:      1,          // optional trusted operator quota
    BudgetWindow:     time.Hour,  // required when MaxRequests > 0
    Now:              now,
})
```

The request is internal and trusted. It is not a model tool, an HTTP endpoint
or a public capability. `ScheduleSourceCrawl()` remains unchanged for existing
mail-listener and other internal callers.

### Result

`CrawlScheduleResult` carries:

- `OperationID` — deterministic durable operation identity;
- `RequestID`, `AgentID`, `Owner`, `SourceID`, `OperationType`;
- `State` — scheduling-request state;
- `ReasonCode` — stable machine-readable code;
- `SourceState` — the source row observed during this operation;
- `SourceObservedAt`, `ExpiresAt`;
- `Reused` — true when a duplicate request returned an existing operation.

Operation states include: `proposed`, `denied`, `accepted`,
`already_pending`, `processing`, `completed`, `failed`, `cancelled`,
`expired`, `disabled`, `not_found`, `conflict`, `unsupported`.

`completed` and `failed` are reserved for future observation of the crawl
execution; WP-03B1 does not claim crawl completion from a scheduling
acknowledgment. The operation state and source state are intentionally separate.

Common reason codes:

| Result | Reason |
| --- | --- |
| accepted | `crawl_scheduled` |
| already pending | `crawl_already_pending` |
| processing | `crawl_processing` |
| disabled | `crawl_source_disabled` |
| missing | `crawl_source_not_found` |
| duplicate with changed parameters | `crawl_request_conflict` |
| request lifetime expired | `crawl_request_expired` |
| cancelled/deadline | `crawl_request_cancelled` / `crawl_transaction_timeout` |
| database failure | `crawl_database_error` (sanitized) |
| backend without durable store | `crawl_store_unsupported` |

## 3. Idempotency and concurrency

WP-03B1 uses the existing durable `Status` table. No schema migration or new
table was introduced. The fields used are `status_sha256` (deterministic
primary key), `agent_id`, `source_id`, `operation_type`, `status`, `details`
(JSONB in PostgreSQL, JSON text in SQLite), `created_at` and
`last_updated_at`.

- `status_sha256` is deterministic:
  `owner | agent_id | source_id | operation_type | request_id`.
- `AllowedSources` is required and is rechecked by the database adapter before
  any source mutation. It is populated only after the agent policy layer has
  approved the proposal against the full run scope.
- Same key + identical parameters and approval returns the existing operation
  with `Reused=true`; it does not schedule again.
- Same key + different source, operation, parameters or approval returns
  `conflict`; no mutation occurs.
- Different owners/agents produce different operation IDs even with the same
  logical request ID.
- Concurrent identical requests use a transactional insert reservation; one
  inserts the operation, the other observes the committed row and returns the
  same outcome. A single operation row remains.
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
- Optional `MaxRequests`/`BudgetWindow` fields enforce a trusted operator quota
  for the requesting agent/source/operation. The source row is locked during
  the budget check and decision on PostgreSQL, so concurrent requests cannot
  both slip under the quota. A retry of an already-accepted logical request is
  returned from its idempotency record without consuming another quota slot.
  A budget denial creates no operation row and does not mutate the source.

The `Status` table is present in the PostgreSQL and SQLite schemas. MySQL's
current schema does not contain it, so the new API fails closed with
`crawl_store_unsupported` on MySQL; `ScheduleSourceCrawl()` remains available
for legacy MySQL callers but does not claim durable idempotency.

No migration is required for this implementation. If maintainers prefer a
dedicated crawl-request table instead of the generic existing `Status` table,
that must be a separately approved WP-03B2 migration; the internal API and
result contract can be retained.

## 4. Proposal and approval boundary

`CrawlRequestProposal` is bounded, non-executable data:

- request ID;
- source ID;
- operation type (`crawl` only);
- expiry.

`CrawlRequestApproval` is an explicit trusted-operator decision bound to the
proposal fingerprint, agent ID, owner, source, operation, approval ID,
approver, approval time and expiry.

`AuthorizeCrawlRequest()` requires:

- trusted agent identity and owner;
- non-empty run ID;
- trust level `trusted` or above;
- `tool_execution` and `db_write` capabilities;
- an explicit source scope containing the proposal source;
- no matching `tools` / `tool:request_source_crawl` contract prohibition;
- a current proposal;
- an explicit approval bound to the exact proposal and identity;
- a current approval.

Every agent-originated crawl request requires explicit approval in WP-03B1.
The `all` capability satisfies capability checks but never source scope,
allowlist/approval or contract restrictions. A new operation or modified
proposal cannot reuse an approval.

The existing source-scope mechanism is an operator-configured set of source
IDs. It does not currently enforce `Sources.usr_id` ownership or a general
tenant hierarchy. WP-03B1 binds the request owner and approval owner but does
not invent a new ownership policy. A deployment that requires per-user source
ownership must add that trusted policy before exposing the WP-03B2 tool.

`BuildCrawlScheduleRequest()` refuses to construct a database request from a
denied or mismatched decision. A model cannot authorize its own request; no
model-facing tool is registered in this chunk.

## 5. Audit

`JobEngine.AuditCrawlSchedulingDecision()` records:

- agent ID, owner, run ID, trace ID;
- source ID, request ID, operation type, idempotency/operation key;
- authorization decision and stable reason code;
- scheduling outcome and timestamp.

`AuditEvent` gained additive source/request/operation/idempotency fields with
`omitempty`; existing audit records retain their shape. No prompts, model text,
credentials, SQL or raw configuration are copied into the audit.

## 6. Future WP-03B2 integration

WP-03B2 may expose a single model-facing proposal tool only after review. It
must:

1. validate model arguments into a bounded proposal;
2. obtain an explicit approval bound to the proposal and identity;
3. call `AuthorizeCrawlRequest()`;
4. call `BuildCrawlScheduleRequest()` and then the database API;
5. return only the accepted/already-pending/processing acknowledgement;
6. observe completion later through the existing read-only source-status and
   evidence tools;
7. never let a model supply approval, owner, tenant, DB, scope or budget fields.

The future tool will need an approved capability contract; this document does
not add one. The investigated combination is the existing `tool_execution` and
`db_write` capabilities. A dedicated crawl-request capability, if preferred,
requires schema/documentation approval and is not invented here.
