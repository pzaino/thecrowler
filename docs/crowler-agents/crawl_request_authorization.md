# Crawl request authorization and ownership (WP-03B2B/WP-03B2C)

Status: production-shaped authorization infrastructure plus an opt-in,
operator-controlled model-facing crawl request. The `request_source_crawl`
tool is **not in the default read-only registry**. It is admitted only through
`RegisterMutation` when an explicit mutation policy is enabled. A model can
record a proposal; it can never approve or schedule its own request.

WP-03B2B added authoritative authenticated source ownership, verified
ownership provenance, a durable PostgreSQL/SQLite approval store, issuer and
revoker adapters, operator API endpoints, final-mutation revalidation and
stable proposal identity. WP-03B2C adds durable `CrawlProposals`, the
mutation-capable tool, trusted run/tool-call binding, and the operator
submission endpoint. Schema **1.17** is the runtime contract for the remaining
agentic plan; clean installs and the v1.17 upgrade create `CrawlProposals`,
`CrawlApprovals` and `SourceOwnership`.

## 1. Identity and source ownership

### Authenticated users

The database provides:

- `Users` with `user_id`, `username`, `disabled`, `deleted_at`,
  `external_subject`, `external_issuer` and `credential_version`;
- `AuthRoles`, `AuthScopes`, `UserRoles`, `UserScopes`, `RoleScopes`;
- `AuthRevokedTokens` for token revocation.

`pkg/auth` validates local or external tokens and stores an `auth.Identity` in
the request context. Local login uses the numeric `Users.user_id` as the token
subject.

`auth.ResolveActiveIdentity()` turns that token identity into the current
authoritative local user:

- the subject must parse as a positive numeric `Users.user_id`;
- the user row must exist, be enabled and not be soft-deleted;
- roles and scopes are reloaded from the database, so revoked grants take
  effect without waiting for token expiry;
- a non-numeric external subject or a database lookup failure is rejected.

`CrawlPrincipalFromAuthIdentity()` then produces the `CrawlPrincipal` used by
the crawl-approval and source-authorization paths. `CrawlPrincipal` carries
`UserID`, `Username`, `Issuer`, `Roles` and `Scopes`. It is never populated from
model arguments.

### Agent identity

`AgentIdentity.Owner` remains a manifest string. The trusted runtime must pass a
`CrawlPrincipal` whose `Username` equals the agent owner. The agent cannot
choose its principal by changing model arguments or request parameters. An
explicit agent-to-user mapping table remains a future contract decision.

### Authoritative source ownership

`Sources.usr_id` is the persisted source owner:

- `0` means ownership is unknown and fails closed for ordinary users;
- API source creation derives the owner from the authenticated local user;
- request-body or query-string `usr_id` is ignored when authentication is
  enabled;
- source ownership cannot be changed through add or update;
- authenticated source add/update/remove/vacuum lock the source row and then
  lock/recheck the current `Users` row and, for administrator overrides, the
  current `UserRoles`/`AuthRoles` grant rows; a disablement or role revocation
  which committed first is observed, while a later one waits for the mutation;
- a new-source insert is provisional until that account-row lock succeeds. If
  account deactivation commits first, the source insert is rolled back; if the
  insert obtains the account lock first, deactivation serializes after commit;
- `/v1/source/update`, `/v1/source/remove` and `/v1/source/vacuum` require the
  source owner or the established `admin`/`superadmin` role;
- an existing URL owned by another user cannot be claimed or modified through
  source add;
- disabled or deleted users are rejected even when their token is otherwise
  valid.

### Verified ownership provenance

`Sources.usr_id` alone is not proof of ownership because legacy unauthenticated
source creation accepted a caller-supplied value. Schema 1.17 adds
`SourceOwnership`:

- `source_id` is the provenance primary key;
- `user_id` must match `Sources.usr_id` to be authoritative;
- `verified_at`, `verified_by_user_id`, `verified_by`,
  `verification_method` and `verification_reason` record the explicit
  verification event;
- a missing row means **legacy/unverified**, even when `usr_id` is nonzero.

Authenticated source creation writes a provenance row with method
`authenticated_creation` in the same transaction that creates the source.
Legacy and internal source creation do not claim verification. Agent scheduling
and ordinary authenticated owner mutations require a matching verified row;
administrators may manage an unverified source but cannot turn the claim into
verified ownership except through the explicit verification endpoint.

`POST /v1/console/source/ownership/verify` is the administrative verification
or transfer operation. It requires an authenticated `admin`/`superadmin`
principal, locks the source row, updates `Sources.usr_id`, upserts
`SourceOwnership`, and records the verifier and reason. It is never derived from
model arguments, source add, source update or a request-body `usr_id`.

`Owners` and `SourceOwnerIndex` are not maintained by the source lifecycle and
are not treated as authoritative. The console owner handler also writes a
`parent_id` column that is not in the current `Owners` schema. They remain
unused until a separate maintained ownership contract is approved.

When API authentication is disabled, legacy unauthenticated source management
continues and a newly created source may retain the request-supplied `usr_id`.
That value is not an authenticated principal. Agent-originated scheduling only
accepts a principal resolved from an authenticated user, so legacy or
caller-supplied `usr_id` values do not automatically become eligible for agent
mutations. Deployments that enable model-driven scheduling must enable API
authentication and populate ownership accordingly.

A deployment with historical sources whose owners are `0` or otherwise
incorrect needs an explicit ownership migration or administrative population
step. CROWler does not automatically claim or reassign historical sources.

## 2. Source authorization interface

```go
type CrawlPrincipal struct {
    UserID   uint64
    Username string
    Issuer   string
    Roles    []string
    Scopes   []string
}

type CrawlSourceAuthorizer interface {
    AuthorizeCrawlSource(ctx context.Context, principal CrawlPrincipal, sourceID uint64) error
}
```

`DatabaseCrawlSourceAuthorizer` reads `Sources.source_id`, `usr_id`,
`deleted_at` and matching `SourceOwnership` provenance. It grants access only
when:

1. the principal is valid;
2. the source exists and is not deleted;
3. `Sources.usr_id` is non-zero and has a matching verified `SourceOwnership`
   row;
4. `Sources.usr_id == principal.UserID`, or the authorizer is configured with
   `AllowAdmin` and the principal holds `admin`/`superadmin`.

Agent-originated scheduling uses `AllowAdmin: false`: the agent acts for the
source owner, so the mutation boundary requires an exact owner match.
Approval issuance and revocation use `AllowAdmin: true` so an established
administrator can operate on another owner's source without transferring
ownership.

Stable reasons:

| Outcome | Reason |
| --- | --- |
| missing principal | `crawl_principal_required` |
| principal does not match owner | `crawl_principal_mismatch` |
| no authorizer | `crawl_source_authorizer_unavailable` |
| missing/zero ownership | `crawl_source_ownership_missing` |
| different owner | `crawl_source_forbidden` |
| other lookup failure | `crawl_source_authorization_error` |
| cancelled/deadline | `crawl_authorization_cancelled` / `crawl_authorization_timeout` |

## 3. Durable approval authority

A matching approval record is not proof of approval. Approvals are resolved in
the `CrawlApprovals` table and checked against the stored record.

### Interfaces

```go
type CrawlApprovalRecord struct {
    Approval          CrawlRequestApproval
    Proposal          CrawlRequestProposal
    IssuerPrincipal   CrawlPrincipal
    ApproverPrincipal CrawlPrincipal
    Revoked           bool
    RevokedAt         *time.Time
    RevokedByUserID   uint64
    RevokedBy         string
    RevocationReason  string
}

type CrawlApprovalStore interface {
    LookupCrawlApproval(ctx context.Context, approvalID string) (CrawlApprovalRecord, bool, error)
    LookupCrawlApprovalByRequest(ctx context.Context, runID, requestID string, principalUserID, sourceID uint64, operationType string) (CrawlApprovalRecord, bool, error)
}

type CrawlApprovalIssuanceRequest struct {
    Proposal        CrawlRequestProposal
    Identity        AgentIdentity
    Principal       CrawlPrincipal
    RunID           string
    IssuerPrincipal CrawlPrincipal
    Approver        CrawlPrincipal
    Issuer          string
    ApprovedAt      time.Time
    ExpiresAt       time.Time
}

type CrawlApprovalIssuer interface {
    IssueCrawlApproval(ctx context.Context, request CrawlApprovalIssuanceRequest) (CrawlRequestApproval, error)
}

type CrawlApprovalRevoker interface {
    RevokeCrawlApproval(ctx context.Context, principal CrawlPrincipal, approvalID, reason string, revokedAt time.Time) error
}
```

`DatabaseCrawlApprovalStore` implements all three interfaces. It:

- re-resolves the issuer, approver and agent principal from `Users` and current
  grants before issuance and lookup; disabled/deleted users fail closed;
- requires `principal.Username == identity.Owner` and
  `principal.UserID == identity/approval principal`;
- verifies the approver's source authority through
  `DatabaseCrawlSourceAuthorizer`;
- normalizes approval timestamps to PostgreSQL's microsecond precision before
  fingerprinting;
- generates a 64-hex approval ID;
- inserts with a unique logical key and returns the existing approval for an
  identical retry;
- returns `crawl_approval_conflict` when the same run/request/source/operation
  already has a different proposal;
- returns `crawl_approval_revoked` when a retry targets a revoked approval;
- persists revocation without deleting history;
- rechecks the caller's current account, verified source ownership and current
  administrative grant before an idempotent revocation succeeds; an already
  revoked approval never bypasses authorization.

`StoreBackedCrawlApprovalVerifier` checks the trusted issuer, authoritative
record, revocation state, exact approval fingerprint, proposal/run/agent/
principal/owner/source/operation bindings, approval lifetime, approver user ID
and current approver source authority. A database error is always a denial.

### `CrawlApprovals` schema

The clean-install PostgreSQL and SQLite setup files include the table. The
PostgreSQL definition is:

```sql
CREATE TABLE CrawlApprovals (
    approval_id CHAR(64) PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    proposal_sha256 CHAR(64) NOT NULL,
    request_id TEXT NOT NULL,
    run_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    owner TEXT NOT NULL,
    principal_user_id BIGINT NOT NULL,
    source_id BIGINT NOT NULL,
    operation_type TEXT NOT NULL,

    issuer TEXT NOT NULL,
    issued_by_user_id BIGINT NOT NULL,
    issued_by TEXT NOT NULL,
    issued_by_issuer TEXT NOT NULL,
    approved_by_user_id BIGINT NOT NULL,
    approved_by TEXT NOT NULL,
    approved_by_issuer TEXT NOT NULL,
    approval_sha256 CHAR(64) NOT NULL,

    proposal_expires_at TIMESTAMPTZ NOT NULL,
    approved_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,

    revoked_at TIMESTAMPTZ,
    revoked_by_user_id BIGINT,
    revoked_by TEXT,
    revocation_reason TEXT,

    CONSTRAINT uq_crawlapprovals_logical UNIQUE
        (request_id, run_id, principal_user_id, source_id, operation_type),
    CONSTRAINT chk_crawlapprovals_hashes CHECK (
        approval_id ~ '^[0-9a-f]{64}$' AND
        proposal_sha256 ~ '^[0-9a-f]{64}$' AND
        approval_sha256 ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT chk_crawlapprovals_lifetime CHECK (expires_at > approved_at),
    CONSTRAINT chk_crawlapprovals_max_lifetime
        CHECK (expires_at <= approved_at + INTERVAL '24 hours')
);
```

Indexes cover `(source_id, expires_at)`, `(principal_user_id, expires_at)` and
`(run_id, request_id)`. The table has no foreign keys to `Sources` or `Users`:
historical approvals must survive source/user lifecycle changes, while issuance
validates those references while they are live.

### Migration status

`pkg/database/db_migrations/postgresql-migration-v1.17.pgsql`,
`pkg/database/db_migrations/sqlite-migration-v1.17.sqlite3` and
`pkg/database/db_migrations/mysql-migration-v1.17.mysql` are the additive,
re-runnable v1.17 upgrades. They:

- are transactional where the backend supports it;
- create `CrawlApprovals` (PostgreSQL/SQLite) and `SourceOwnership`
  (all three backends);
- create the required indexes and PostgreSQL triggers/ownership grants;
- advance `DBSchemaVersion` and `DatabaseWriterCompatibility` together.

`RequiredSchemaVersion` is `1.17` and `WriterGeneration` is
`verified-ownership-v1.17`. A v1.16 database must stop old writers, apply the
v1.17 migration, and only then start v1.17 writers. Applying the migration to an
existing persistent deployment database requires maintainer authorization.

The migration never bulk-marks existing `usr_id` values as verified. Existing
sources remain legacy until an administrator verifies each one. Approval and
ownership history is preserved by `IF NOT EXISTS`/upsert semantics and by
re-running the same v1.17 migration, which safely converges partially migrated
databases.

MySQL keeps the ownership-provenance table for schema compatibility but does
not support durable crawl approvals or the authenticated atomic source-mutation
contract; those features fail closed with an explicit unsupported result.

Rollback is additive and destructive only to proposal/approval/ownership
provenance:

```sql
DROP TABLE IF EXISTS CrawlProposals;
DROP TABLE IF EXISTS CrawlApprovals;
DROP TABLE IF EXISTS SourceOwnership;
UPDATE DBSchemaVersion SET is_current = (version = '1.16');
UPDATE DatabaseWriterCompatibility SET writer_generation = 'reservation-v1.15' WHERE lock_id = 1;
```

### Operator API

The console registers:

| Method | Endpoint | Purpose |
| --- | --- | --- |
| POST | `/v1/console/crawl/approvals/issue` | Issue an approval for a recorded proposal. |
| POST | `/v1/console/crawl/approvals/revoke` | Revoke an approval. |
| GET | `/v1/console/crawl/approvals/status` | Read an approval. |
| POST | `/v1/console/crawl/approvals/submit` | Submit an approved proposal through the scheduler. |
| POST | `/v1/console/source/ownership/verify` | Verify or explicitly transfer source ownership (admin). |

All require `api.enable_console: true` and enabled API authentication. The
approver/issuer/owner/principal are derived from the authenticated request, not
the body.

The issue endpoint now loads the durable `CrawlProposals` row named by
`request_id` and derives the agent, run, principal, source, operation and
proposal expiry from that row. Caller-supplied fields are optional but must
match the recorded proposal exactly. The authenticated operator becomes the
approver and issuer; the proposal's recorded owner remains the run principal,
so an administrator may approve a proposal for another owner's run without
being able to substitute an arbitrary principal user ID.

Issue request:

```json
{
  "request_id": "logical-request-1",
  "source_id": 42,
  "agent_id": "crawl-agent",
  "run_id": "run-1",
  "operation_type": "crawl",
  "proposal_expires_at": "2026-11-08T13:00:00Z",
  "approval_expires_at": "2026-11-08T12:30:00Z"
}
```

Issue:

```bash
curl -sS -X POST http://127.0.0.1:8080/v1/console/crawl/approvals/issue \
  -H "Authorization: Bearer $CROWLER_TOKEN" \
  -H "Content-Type: application/json" \
  --data @approval.json
```

The response contains the approval ID, proposal fingerprint, request ID, source,
agent, run, operation, approver, issuance/expiry timestamps and revocation
state.

Revoke:

```bash
curl -sS -X POST http://127.0.0.1:8080/v1/console/crawl/approvals/revoke \
  -H "Authorization: Bearer $CROWLER_TOKEN" \
  -H "Content-Type: application/json" \
  --data '{"approval_id":"...","reason":"operator request"}'
```

Status:

```bash
curl -sS -G http://127.0.0.1:8080/v1/console/crawl/approvals/status \
  -H "Authorization: Bearer $CROWLER_TOKEN" \
  --data-urlencode "approval_id=..."
```

An approval is single-proposal, not single-call. The same run/request/source/
operation may retry the same approval idempotently. A new request ID, source,
operation, owner or run requires a new approval. No atomic single-use counter is
claimed.

## 4. Approval lifetime rules

- Proposal expiry must be in the future and no more than 24 hours from the
  trusted runtime clock.
- Approval issuance must not be in the future or more than 24 hours old.
- Approval expiry must be in the future and no more than 24 hours from the
  trusted runtime clock.
- Approval expiry must be strictly after issuance.
- `ExpiresAt - ApprovedAt` must not exceed 24 hours, even when both timestamps
  are individually inside their allowed ranges.
- The scheduling request expires no later than either the proposal or the
  approval.
- The scheduling request also expires no later than 24 hours after approval
  issuance.
- Exported APIs read the runtime clock. Clock-injected variants are
  package-internal for deterministic tests.

## 5. Authorization-to-submission (TOCTOU) integrity

Authorization is not permanently valid. The final database mutation revalidates
the authoritative approval and source ownership inside the scheduling
transaction:

1. `TransactionContextForHandler` bounds the transaction.
2. The existing operation row is read for idempotent retries.
3. The source row is locked (`FOR UPDATE` on PostgreSQL).
4. The operation row is re-read after the lock.
5. The approval row is locked (`FOR UPDATE`) after the source row and checked:
   approval ID, approval fingerprint, proposal fingerprint, request ID, run,
   agent, owner, principal user ID, source, operation, approver user ID,
   revocation state and expiry.
6. Source `usr_id`, `deleted_at` and the principal user ID are rechecked under
   the same source lock.
7. Quota, operation insertion and the conditional source update follow.

Source mutations lock the source row first, then the authoritative account/grant
rows. Approval issuance locks source, then account/grant rows, then inserts the
approval. Revocation locks source, then account/grant rows, then the approval
row. Scheduling locks source, then the approval row, then the account/grant
rows. Sharing the source-row lock serializes competing operations on the same
source; account rows are locked in ascending user-ID order. This produces
linearizable outcomes:

- revocation before submission → submission denied;
- submission before revocation → accepted operation, then revocation;
- transfer before submission → ownership denial;
- submission before transfer → accepted operation, then transfer;
- disablement/grant revocation before the authoritative mutation check → denial;
- an authorized idempotent revocation remains successful, while an unauthorized
  retry is denied even after the approval is already revoked.

A missing `CrawlApprovals` table fails closed with
`crawl_approval_unavailable`; a missing approval or stale binding returns
`crawl_approval_rejected`; a changed/zero source owner returns
`crawl_source_ownership_rejected`. No operation row or source mutation is
committed on denial.

SQLite is supported for deterministic tests but does not provide PostgreSQL's
row-level locking guarantees. Production mutation must use PostgreSQL.

## 6. Stable proposal identity and retries

The trusted runtime generates the logical request ID from:

- run ID;
- actual provider tool-call correlation ID;
- source ID.

The tool writes a durable `CrawlProposals` row on the first call. A duplicate
call loads that row and reuses its request ID, proposal fingerprint and expiry,
even if the process restarted or the local clock advanced. A duplicate call
does not extend the original proposal lifetime. A changed source, operation or
tool call produces a different request ID.

`CrawlProposalFactory` and `MemoryCrawlProposalRegistry` remain bounded
process-local helpers for the earlier inert proposal contract and tests; they
are not the restart-safe anchor for the model-facing tool. The durable proposal
row is the anchor before approval, and the durable approval row becomes the
additional binding after approval. Proposal and approval fingerprints normalize
timestamps to the database's microsecond precision, so a row reconstructed after
a restart remains comparable.

A retry of an already accepted logical request remains idempotent even if the
approval was later revoked. A new request ID cannot use the old approval.

## 7. Model-facing tool contract (implemented, opt-in)

The tool is registered only through an explicit mutation policy. The input
contains only:

```json
{
  "type": "object",
  "properties": {
    "source_id": {"type": "integer", "minimum": 1}
  },
  "required": ["source_id"],
  "additionalProperties": false
}
```

Phase-A proposal result:

```json
{
  "request_id": "...64 hex...",
  "source_id": 42,
  "state": "approval_required",
  "reason_code": "crawl_approval_required",
  "approval_required": true,
  "expires_at": "2026-11-09T15:00:00Z",
  "observed_at": "2026-11-09T14:30:00Z"
}
```

The operator submission endpoint returns the scheduling result separately. The
model-facing result contains no approval ID, principal, owner, quota,
configuration or raw error data. `accepted`, `already_pending` and `processing`
are scheduling states, not crawl-completion proof.

WP-03B2C adds an explicit `ToolEffect` classification. Read-only tools keep
their existing interface and admission path; mutation tools must implement
`Effect() == ToolEffectMutation` and must be admitted through
`RegisterMutation()` and an explicit mutation policy. The crawl-request tool is
never advertised as read-only.

## 8. Deterministic evidence semantics

| Observation | Trusted evidence | Satisfies a submission goal? | Satisfies a completion goal? |
| --- | --- | --- | --- |
| Proposal accepted by policy | authorization decision | No, it is not a scheduling record | No |
| Request denied | denial decision with stable reason | No | No |
| Source scheduled | `accepted` / `crawl_scheduled` scheduling record | Yes, if the goal is explicitly submission | No |
| Source already pending | `already_pending` / `crawl_already_pending` | Yes, if the goal is explicitly submission | No |
| Source processing | `processing` / `crawl_processing` | Yes, if the goal is explicitly submission | No |
| Crawl completed | trusted source-status/completion evidence | No | Yes |
| Crawl failed | trusted source-status/error evidence | No | No |

An agent whose objective is to submit an authorized request may use a verified
scheduling acknowledgement under an approved goal contract. A completion goal
must require independently verified crawl-completion evidence. Asynchronous
goal persistence remains out of scope.

## 9. Audit and diagnostics

`AuditCrawlSchedulingDecision()` records:

- authorization stage: proposal received, policy checked, source
  verified/denied, approval required/verified/rejected, authorized, submitted,
  reused, submission denied or submission failed;
- authorization outcome/reason;
- approval outcome: not requested, verified, rejected, revoked, expired or
  unavailable;
- source-ownership outcome: not checked, verified, missing, denied or
  unavailable;
- source, request, run, trace, agent, owner, principal user ID, operation,
  idempotency key and trusted timestamp.

The audit is in-memory governance telemetry; the durable scheduling record is
the `Status` row and the durable approval is the `CrawlApprovals` row. No
prompts, secrets, credentials, SQL or raw configuration are copied into the
audit.

## 10. Controlled model-facing crawl requests (WP-03B2C)

### Enabling the operation

`request_source_crawl` is disabled unless **all** of the following hold:

- the step declares a `mutation_policy` with `enabled: true`;
- `mutation_policy.allowed_operations` explicitly contains
  `request_source_crawl` (wildcards are rejected);
- the step's tool `allowlist` also contains `request_source_crawl`;
- `allowed_sources` is non-empty and contains the proposed source;
- the agent identity holds `tool_execution` and `db_write`, has trusted
  standing, and its contract does not forbid the tool;
- the configured owner maps to an active local `Users.username`;
- `max_requests` is 1..100 and `budget_window` is positive and bounded;
- the database is PostgreSQL or SQLite on schema 1.17 and both
  `CrawlProposals` and `CrawlApprovals` are available.

The default runtime, deployment templates and `DefaultToolRegistry()` do not
register the tool. `AgentToolRegistry.Register()` rejects mutation tools;
only `RegisterMutation()` admits this one, and only after the policy above has
been validated.

### Model-facing input and result

The model may send only:

```json
{"source_id": 42}
```

The strict schema rejects additional properties, zero, negative and
non-integer values. The model cannot supply run, agent, owner, principal,
request, approval, expiry, quota, URL or operation values.

A successful proposal returns a safe result such as:

```json
{
  "request_id": "…64 hex…",
  "source_id": 42,
  "state": "approval_required",
  "reason_code": "crawl_approval_required",
  "approval_required": true,
  "expires_at": "2026-11-09T15:00:00Z",
  "observed_at": "2026-11-09T14:30:00Z"
}
```

`state` may also be `approval_recorded` when an exact durable approval already
exists, `submitted` after an operator submission, or `expired`. None of these
states means the crawl completed. A scheduling acknowledgment only means the
source became eligible for the existing engine claim.

### Trusted identity and idempotency

The request ID is `SHA-256(run_id + NUL + effective_tool_call_id + NUL +
source_id)`. The tool loop assigns the effective call ID before executing any
call, rejects duplicate IDs for the whole batch, and passes the effective ID to
the tool through `ToolRuntime.CallID`. The same logical invocation therefore
keeps the same durable proposal and expiry across retries and process
restarts.

The proposal records the run, tool-call correlation ID, agent, owner,
authoritative principal user ID, source, operation, proposal fingerprint,
trace ID, quota, expiry and a frozen policy snapshot. The snapshot is used at
submission time to recheck capabilities, trust, contract, operation allowlist
and source scope instead of trusting the current request.

### Operator approval and submission

An operator first inspects the proposal (by `request_id`) and issues an
approval with `/v1/console/crawl/approvals/issue`. The endpoint requires the
recorded proposal to exist, be pending and unexpired, and to match any supplied
source, agent, run, operation or expiry fields. The approval binds the exact
proposal fingerprint, run, agent, owner, principal, source, operation, issuer
and approver.

After approval, the operator calls:

```json
POST /v1/console/crawl/approvals/submit
{"request_id": "…", "approval_id": "…"}
```

The submission path:

1. reloads the durable proposal and approval;
2. verifies the proposal fingerprint, principal, run, agent, owner, source and
   operation;
3. rechecks the caller's current source authority and that the caller is the
   recorded approver or issuer;
4. re-runs the frozen capability, trust, contract, allowlist, scope, quota,
   expiry and verified-ownership policy;
5. reuses `ScheduleSourceCrawlRequest()`, which revalidates the approval and
   ownership transactionally and applies the existing source lock, status
   transition and idempotency checks.

Repeating the submission is safe: the durable crawler operation is reused and
no second crawl is created. Revocation makes new submissions fail. A transfer
of ownership, disabled principal, revoked administrator grant or expired
proposal also fails closed; no unauthorized source status or scheduling record
is written.

### Evidence semantics

`request_source_crawl` is mutation-capable and is not an evidence provider. A
tool observation from it is never evidence that a crawl was scheduled or
completed. In `AgentGoalRun`:

- a proposal is evidence that the agent proposed an operation;
- an accepted scheduling request is evidence that the source became eligible;
- only trusted source-status observations can establish that a source is
  processing or completed.

A model's final text response cannot change any of those states. A goal that
requires `pending`, `processing` or completed crawl evidence stays `incomplete`
when only a proposal exists.

### Backends, diagnostics and limitations

PostgreSQL and SQLite support proposals, approvals, authenticated source
mutation, ownership verification and scheduling. MySQL retains ownership
provenance only; mutation tools fail closed with an unsupported-backend error.

Denied requests return stable reason codes such as `crawl_source_out_of_scope`,
`crawl_source_ownership_missing`, `crawl_operation_not_allowlisted`,
`crawl_approval_required`, `crawl_approval_rejected`,
`crawl_request_budget_exhausted`, `crawl_source_processing` and
`crawl_source_ownership_rejected`. Audit events carry the run, trace, agent,
owner, principal, source, request, approval, operation and scheduling state;
model prompts and result payloads are not copied into audit records.

The operator API must have authentication enabled. The v1.17 migration must be
applied during a stop-old-writers window before enabling the mutation policy.
