# Crawl request authorization and ownership (WP-03B2A)

Status: internal authorization boundary for the future `request_source_crawl`
tool. The tool is **not registered**. CROWler does not yet have a durable
operator-approval service or a fully wired authenticated source-ownership
lifecycle; this work implements the fail-closed interfaces and adapters so
that those production contracts can be added without weakening the scheduler.

## 1. Identity and source ownership in the current repository

### Authenticated users

The database already has an authenticated user model:

- `Users` with `user_id`, `username`, `external_subject`, `external_issuer`,
  `disabled` and `credential_version`;
- `AuthRoles`, `AuthScopes`, `UserRoles`, `UserScopes`, `RoleScopes`;
- `AuthRevokedTokens` for token revocation.

`pkg/auth` validates local or external tokens and stores an `auth.Identity`
in the request context. Local login uses the numeric `Users.user_id` as the
token subject; external issuers may use a non-numeric subject.

`CrawlPrincipalFromAuthIdentity()` adapts an authenticated `auth.Identity` to
the crawl authorization principal. It requires a numeric subject, username
and issuer. A non-numeric external subject fails closed until an explicit
user-mapping contract is approved.

### Agent identity

`AgentIdentity.Owner` is a manifest string. It is not a foreign key and the
repository does not currently map it to `Users.user_id`. WP-03B2A therefore
requires the trusted runtime to pass a `CrawlPrincipal` whose `Username`
matches the agent owner. The agent cannot choose this principal by changing
model arguments or request parameters.

This is a deliberate interim contract. A future implementation may replace it
with an explicit agent-to-user mapping table after maintainer approval.

### Source ownership

`Sources.usr_id` is the only persisted source-to-user value:

- it defaults to `0`;
- the source creation API accepts `usr_id` from the request body;
- the runtime does not currently derive it from the authenticated principal;
- `0` means ownership is unknown.

`Owners` and `SourceOwnerIndex` exist in the schema, but the source lifecycle
does not maintain them. The console owner handler also writes a `parent_id`
column that is not present in the current `Owners` schema. They are **not**
treated as authoritative.

`GetSourceOwner()` reads `Sources.source_id` and `Sources.usr_id` for a live,
non-deleted source. `DatabaseCrawlSourceAuthorizer` grants access only when:

1. the principal is valid;
2. the source exists and is not deleted;
3. `Sources.usr_id` is non-zero;
4. `Sources.usr_id == principal.UserID`.

Missing, deleted or zero ownership returns `ErrCrawlSourceOwnershipMissing`.
A different owner returns `ErrCrawlSourceForbidden`. A database error returns
`ErrCrawlSourceAuthorizationUnavailable` and is never treated as a grant.

### Missing production wiring

Production ownership enforcement still requires an approved contract change:

- `/v1/source/add` and source update must set `usr_id` from the authenticated
  `auth.Identity.Subject` and must not accept a caller-supplied `usr_id`;
- the agent runtime must resolve the authenticated principal that the agent
  acts for and pass it as `CrawlRequestPolicy.Principal`;
- a deployment that needs shared or tenant ownership must add an explicit,
  maintained ownership mapping. WP-03B2A does not invent a tenant schema.

Until that wiring exists, the default policy has no principal and no source
authorizer, so authorization fails closed.

## 2. Source authorization interface

```go
type CrawlPrincipal struct {
    UserID   uint64
    Username string
    Issuer   string
}

type CrawlSourceAuthorizer interface {
    AuthorizeCrawlSource(ctx context.Context, principal CrawlPrincipal, sourceID uint64) error
}
```

`CrawlRequestPolicy` carries both the trusted `Principal` and the
`SourceAuthorizer`. `AuthorizeCrawlRequest()` requires the principal to be
valid and its username to equal `AgentIdentity.Owner`, then calls the source
authorizer for the same source that passed the run allowlist.

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

## 3. Trusted approval authority

A matching approval record is not proof of approval. The following interfaces
define the issuance, lookup, verification and revocation boundary.

### Approval store and verifier

```go
type CrawlApprovalRecord struct {
    Approval          CrawlRequestApproval
    ApproverPrincipal CrawlPrincipal
    Revoked           bool
}

type CrawlApprovalStore interface {
    LookupCrawlApproval(ctx context.Context, approvalID string) (CrawlApprovalRecord, bool, error)
}

type CrawlApprovalVerifier interface {
    VerifyCrawlApproval(ctx context.Context, verification CrawlApprovalVerification) error
}
```

`StoreBackedCrawlApprovalVerifier` is the concrete verifier. It requires:

- a non-nil store and source authorizer;
- a trusted issuer configured in `TrustedIssuers`;
- an authoritative record for the presented approval ID;
- a non-revoked record;
- an exact fingerprint match between the presented approval and the stored
  approval;
- matching proposal fingerprint, run ID, agent, agent principal username,
  owner, source and operation;
- an approval issuance time not in the future, a future expiry, expiry after
  issuance, and total duration no greater than the configured maximum
  (capped at 24 hours);
- a valid approver principal whose username matches `ApprovedBy`;
- successful source authorization for the approver and the approved source.

Failure reasons:

| Outcome | Reason |
| --- | --- |
| unknown approval | `crawl_approval_unknown` |
| issuer not trusted | `crawl_approval_issuer_untrusted` |
| forged/changed approval | `crawl_approval_unverified` |
| revoked | `crawl_approval_revoked` |
| expired | `crawl_approval_expired` |
| invalid/oversized lifetime | `crawl_approval_lifetime_exceeded` |
| approver not authorized | `crawl_approval_approver_unauthorized` |
| store/approver lookup unavailable | `crawl_approval_store_unavailable` |
| no verifier | `crawl_approval_verifier_unavailable` |

### Issuance and revocation contracts

```go
type CrawlApprovalIssuanceRequest struct {
    Proposal   CrawlRequestProposal
    Identity   AgentIdentity
    Principal  CrawlPrincipal
    RunID      string
    Approver   CrawlPrincipal
    Issuer     string
    ApprovedAt time.Time
    ExpiresAt  time.Time
}

type CrawlApprovalIssuer interface {
    IssueCrawlApproval(ctx context.Context, request CrawlApprovalIssuanceRequest) (CrawlRequestApproval, error)
}

type CrawlApprovalRevoker interface {
    RevokeCrawlApproval(ctx context.Context, approvalID, reason string, revokedAt time.Time) error
}
```

An issuer must:

1. require a trusted authenticated approver principal;
2. verify that the approver is authorized for the proposed source;
3. bind every approval field to the proposal, agent, owner, principal and run,
   including `principal.Username == identity.Owner`;
4. enforce the 24-hour approval lifetime;
5. persist the record durably before returning it;
6. audit the issuance decision without storing prompts or secrets.

A revoker must persist a revocation timestamp/state and make subsequent
verification fail with `crawl_approval_revoked`. A revocation must not delete
the historical record.

No production issuer, revoker, durable store, signing service, approval UI or
API is implemented in WP-03B2A. The in-memory implementations used by tests
exist only in `pkg/agent/*_test.go`.

An approval is single-proposal, not single-call. Reusing it for the same
proposal, run and principal is a legitimate idempotent retry. A new request
ID, source, operation, owner or run requires a new approval. WP-03B2A does not
claim atomic approval consumption; no durable single-use counter exists.

### Proposed durable approval schema (not applied)

A production approval store needs a schema migration approved by maintainers.
The following is a proposal only; it is not present in the repository setup
files:

```sql
CREATE TABLE CrawlApprovals (
    approval_id          CHAR(64) PRIMARY KEY,
    proposal_sha256      CHAR(64) NOT NULL,
    run_id               TEXT NOT NULL,
    agent_id             TEXT NOT NULL,
    owner                TEXT NOT NULL,
    principal_user_id    BIGINT NOT NULL,
    source_id            BIGINT NOT NULL,
    operation_type       TEXT NOT NULL,
    issuer               TEXT NOT NULL,
    approved_by_user_id  BIGINT NOT NULL,
    approved_by          TEXT NOT NULL,
    approved_at          TIMESTAMPTZ NOT NULL,
    expires_at           TIMESTAMPTZ NOT NULL,
    revoked_at           TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (expires_at > approved_at),
    CHECK (expires_at <= approved_at + INTERVAL '24 hours')
);

CREATE INDEX idx_crawlapprovals_source ON CrawlApprovals (source_id, expires_at);
CREATE INDEX idx_crawlapprovals_principal ON CrawlApprovals (principal_user_id, expires_at);
```

The store adapter must use bound parameters, reject ambiguous records and
never interpret a database error as an approval.

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
- `BuildCrawlScheduleRequest()` rechecks expiry against the trusted runtime
  clock, so a decision verified earlier cannot be submitted after expiry.
- Exported APIs read the runtime clock. Clock-injected variants are
  package-internal for deterministic tests.

## 5. Authorization-to-submission binding

`CrawlRequestDecision` now keeps every authorization-relevant field
unexported:

- allowed state and verified state;
- reason code and stage;
- proposal and approval fingerprints;
- required capabilities;
- run ID and principal user ID;
- quota and budget window;
- approval and source-ownership outcomes.

Read-only accessors expose audit and reporting data. `RequiredCapabilities()`
returns a copy. A caller outside the package cannot fabricate a verified
decision, change its run or principal, widen its quota or alter its
fingerprints.

`BuildCrawlScheduleRequest()` rechecks:

- the decision is allowed and verified;
- the decision run equals the submission run;
- the decision principal user ID is non-zero;
- proposal and approval fingerprints match the supplied values;
- approval proposal, run, agent, owner, source and operation bindings match;
- the quota is positive;
- the request expiry is after the runtime clock, after approval issuance and
  within the approval-bound 24-hour lifetime.

The database adapter additionally requires `PrincipalUserID > 0`, stores it
in the durable operation details and treats a changed principal ID, run,
approval ID, approval hash, expiry or parameter hash as
`crawl_request_conflict`.

The operation identity remains:

```text
SHA256(owner | agent_id | source_id | operation_type | request_id)
```

Agent-originated requests enforce `owner == principal.Username`, and
`Users.username` is unique, so two principals cannot collide by sharing an
owner label. The durable operation details also store the principal user ID
for audit and conflict detection.

## 6. Proposed model-facing tool contract

The tool remains unregistered. The proposed input contains only:

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

The trusted runtime generates the logical request ID from the run ID, the
provider tool-call correlation ID and the source ID:

```go
requestID, err := CrawlToolRequestID(runID, toolCallID, sourceID)
```

The model never chooses the request ID, approval, issuer, approver, agent,
owner, principal, tenant, database, allowlist, source scope, quota, clock or
engine identifiers. Repeated execution of the same tool call reuses the same
request ID, so legitimate retries remain idempotent. A new tool call requires
a new proposal and a new approval.

The proposed safe result is:

```json
{
  "operation_id": "...",
  "request_id": "...",
  "source_id": 42,
  "state": "accepted",
  "reason_code": "crawl_scheduled",
  "reused": false,
  "observed_at": "2026-11-08T00:00:00Z"
}
```

It contains no approval, principal, owner, quota, configuration or raw error
data. `accepted`, `already_pending` and `processing` are scheduling states,
not crawl-completion proof.

### Effect classification

The current `AgentTool` interface has no read-only/mutation classification.
WP-03B2A does not change that public interface. The proposed WP-03B2B design
is a separate mutation-specific interface or an explicit effect field, with
the registry refusing mutation tools until the authorization path is enabled.
The crawl-request tool must never be advertised as read-only.

## 7. Deterministic evidence semantics

| Observation | Trusted evidence | Satisfies a submission goal? | Satisfies a completion goal? |
| --- | --- | --- | --- |
| Proposal accepted by policy | authorization decision | No, it is not a scheduling record | No |
| Request denied | denial decision with stable reason | No | No |
| Source scheduled | `accepted` / `crawl_scheduled` scheduling record | Yes, if the goal is explicitly submission | No |
| Source already pending | `already_pending` / `crawl_already_pending` | Yes, if the goal is explicitly submission | No |
| Source processing | `processing` / `crawl_processing` | Yes, if the goal is explicitly submission | No |
| Crawl completed | trusted source-status/completion evidence | No | Yes |
| Crawl failed | trusted source-status/error evidence | No | No |

An agent whose objective is to submit an authorized request may use a
verified scheduling acknowledgement as evidence under an approved goal
contract. An agent whose objective is to complete a crawl must require
independently verified crawl-completion evidence. Model text can never
convert a scheduling acknowledgement into completion proof. Asynchronous goal
persistence is out of scope for WP-03B2A.

## 8. Audit and diagnostics

`AuditCrawlSchedulingDecision()` now records:

- stable authorization stage: proposal received, policy checked, approval
  required/verified/rejected, source verified/denied, authorized, submitted,
  reused, submission denied or submission failed;
- authorization outcome and reason;
- approval outcome: not requested, verified, rejected, revoked, expired or
  unavailable;
- source-ownership outcome: not checked, verified, missing, denied or
  unavailable;
- source, request, run, trace, agent, owner, principal user ID, operation,
  idempotency key and trusted timestamp.

The audit is in-memory governance telemetry. The durable scheduling record is
the `Status` row. No prompts, secrets, credentials, SQL or raw configuration
are copied into the audit.

## 9. Outstanding decisions before WP-03B2B

1. Approve and implement the durable approval store migration, issuer,
   revoker and operator approval workflow.
2. Wire `/v1/source/add` and source updates to set `usr_id` from the
   authenticated principal and ignore caller-supplied owner fields.
3. Wire the agent runtime to resolve the authenticated principal and pass it
   with the source authorizer.
4. Approve the model-facing tool schema, mutation effect classification and
   goal evidence contract.
5. Decide whether shared/tenant source ownership needs an explicit mapping
   table. Do not infer it from `Owners` or `SourceOwnerIndex` until those
   tables are maintained.
6. Keep the tool unregistered until 1–5 are reviewed and approved.

## 10. WP-03B2B activation checklist

- Production `CrawlApprovalStore` + issuer + revoker are durable and audited.
- Source ownership is populated from authenticated identity, not request body.
- Agent principal resolution is trusted and run-bound.
- Mutation tool effect classification is approved and enforced.
- Live model tests prove absent, forged, revoked and cross-bound approvals
  cannot schedule work.
- A test-only approved path verifies scheduling without exposing production
  mutation to the model.
