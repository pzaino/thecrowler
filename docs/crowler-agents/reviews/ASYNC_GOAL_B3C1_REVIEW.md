# WP-05B3C1 Review: Eligibility Hardening and Completion Provenance

## Scope

This package hardens the internal read-only resume-eligibility check and
investigates the boundary between a durable scheduled crawl operation and an
actual crawler attempt. It does not activate resumption or execution.

Schema remains **1.17**; writer generation remains
**`verified-ownership-v1.17`**. No table, column, migration, public contract or
production path was added.

## 1. Eligibility fixes

### Disabled sources

`SourceEnabledForResume` is a new read-only database helper scoped to resume
eligibility. It reads the current `Sources.disabled` flag for a live source and
is called for **every** frozen allowed source after verified ownership passes.
A disabled, deleted or missing source denies eligibility; a database/read
failure is `unavailable`, never `eligible` or an ownership denial.

Verified ownership, soft-delete filtering and the no-administrator-override
policy are unchanged. Scheduling and ordinary source-owner lookups are not
modified.

### Strict current-account authority

`auth.ResolveActiveIdentityStrict` is a separate typed resolver. Existing
login, middleware, token-validation and `ResolveActiveIdentity` behavior is
unchanged.

| Situation | Strict result | Eligibility |
| --- | --- | --- |
| Missing, disabled or soft-deleted user | `ErrIdentityInactive` | `denied` |
| Subject invalid or username changed | `ErrIdentityInvalid` / `ErrIdentityMismatch` | `denied` |
| Account has an external subject/issuer mapping | `ErrIdentityExternalUnsupported` | `denied` |
| Users table missing, SQL failure, grant table failure, closed database | `ErrIdentityStoreUnavailable` | `unavailable` |
| Context cancelled/deadline | wrapped unavailable | `unavailable` with context reason |
| Active local account with readable grants | resolved identity | continues |

The supported principal policy is explicitly **local**: the numeric local
`Users.user_id` and current username are authoritative, and only the canonical
local issuer `crowler` proceeds. Any other checkpoint issuer string fails closed
because this read-only path has no verified external-issuer mapping; the stored
issuer is never accepted as proof. Accounts carrying an external subject/issuer
mapping also fail closed. The shipped SQLite setup has no auth tables, so SQLite
eligibility fails closed with `unavailable` unless a deployment supplies them.

Grant reads are strict: a missing `AuthRoles`/`AuthScopes`/related table is an
availability failure, not an empty grant set.

### End-of-evaluation deadline check

Eligibility now has an explicit clock. It reads the clock at the start, performs
all authority/correlation/snapshot work, then reads it again immediately before
returning a decision. A goal whose deadline expires while a provider is running
becomes `denied` / `goal_resume_denied_deadline_expired`, and `CheckedAt`
reflects the final check. It is still a diagnostic decision, not an enduring
grant.

## 2. Operation-completion provenance

### What is authoritative today

| Fact | Authoritative source | Status |
| --- | --- | --- |
| Scheduled request ID | `CrawlProposals.request_id` | Available |
| Operation ID | `CrawlProposals.operation_id`, `Status.status_sha256` | Available |
| Source, owner, agent, principal | `CrawlProposals`, `CrawlApprovals`, `Status.details` | Available and correlated |
| Original approval | `CrawlApprovals` | Available |
| Actual crawler-attempt identity | **None** | Missing |
| Fencing token / attempt generation | **None** | Missing |
| Attempt start / terminal outcome | Source-level `Sources.status` and `crawl_completed` event only | Not operation-specific |
| `accepted` / `already_pending` | `Status.status` and proposal state | Available |
| Concurrent requests for one source | Source claim plus operation quota; no attempt owner | Ambiguous |
| Stale worker / late completion | Source status can be overwritten by a newer claim | No operation protection |
| Bounded completion metadata | Proposal/approval/`Status.details` | Available, but not attempt-bound |

`update_sources(...)` claims sources by `source_id` and returns only source
columns. The crawler's `ProcessContext` carries the source, not an operation or
fence. `closeSession` updates `Sources.status` and can emit a source-level
`crawl_completed` event; neither identifies the durable operation. Multiple
requests for one source can be `accepted` or `already_pending`, and no existing
column maps one operation to one crawler attempt.

### Decision: BLOCKED

No authenticated operation-to-attempt binding exists. Therefore no
operation-result recording helper was implemented. A helper accepting only a
source ID, URL, generic event payload or caller-supplied operation ID would
create false completion proof, which is prohibited.

### Proposed binding for a separately reviewed phase

1. Make source claiming operation-aware: `update_sources` must select pending
   operations (or return an operation ID plus a newly allocated attempt/fence)
   instead of only a source row.
2. Define the authoritative owner of an `already_pending` request and how a
   retry/newer attempt supersedes an older one.
3. Persist one attempt record per execution with `operation_id`, source,
   attempt ID, monotonically increasing fence, start time, terminal state and
   bounded sanitized error category.
4. Pass the operation ID and fence into the crawler execution context.
5. Record terminal results with a compare-and-set on operation ID, expected
   state and fence. Reject stale, duplicated-conflicting, ambiguous or
   wrong-run/request/agent/owner/source receipts.
6. Treat a source-level `crawl_completed` event as a hint only, never as proof.

This requires a separately approved schema change. It is not implemented in
1.17 by this package.

## 3. Negative provenance tests

The tests prove that:

- the real scheduler creates `accepted` operations that the observer reports as
  pending, and the database-generated `Status.created_at` is used unchanged;
- setting `Sources.status='completed'` does not change the target operation or
  make eligibility pass;
- inserting a generic `crawl_completed` event for the source does not change the
  target operation or make eligibility pass;
- a different operation ID completed for the same source does not complete the
  target operation;
- forged/mismatched run, owner, principal, source, proposal or operation
  identities still fail closed through the existing observer;
- failed/cancelled operations remain terminal non-success;
- no report helper exists, so identical/conflicting terminal-report idempotency
  is explicitly not claimed.

## 4. Verification

| Command | Result |
| --- | --- |
| `go test ./pkg/auth/... -count=1` | PASS |
| `go test ./pkg/agent/... -count=1` | PASS |
| `go test ./pkg/database/... -count=1` | PASS |
| `go test ./... -count=1` | PASS |
| `go test -race ./pkg/agent/... -count=1` | PASS |
| `go vet ./...` | PASS |
| `go vet -tags=integration ./pkg/agent ./pkg/database` | PASS |
| `CGO_ENABLED=0 go build ./...` | PASS |
| `CGO_ENABLED=1 go build ./...` | PASS |
| `git diff --check` | PASS |
| `TestPostgresGoalAsync*` on disposable PostgreSQL 17.12 | PASS, 15 tests, 0 fail, 0 skip |
| `TestPostgresMCPMutation*` on disposable PostgreSQL 17.12 | PASS, 6 tests, 0 fail, 0 skip |
| `actionlint` on `.github/workflows/postgresql-integration.yml` | PASS |

The PostgreSQL suites created unique child databases and dropped only those
databases. No shared, staging or production database was touched.

### Hosted CI

At reviewed commit `83623264`, 7 of 8 checks passed. The hosted
`postgres-integration` job
(`https://github.com/pzaino/thecrowler/actions/runs/43931451399/job/133524639629`)
failed in **step 5, "Install and select PostgreSQL 17 client"**; every schema
and integration step was skipped. This is the reported transient TLS failure
while fetching the PGDG key, not a test failure.

The install step now retries the signed key download, `apt-get update` and
`apt-get install` a bounded three times with normal TLS and signature
verification. No `--insecure`, ignored error, unverified key or skip was
introduced.

**Hosted CI is NOT VERIFIED.** No `gh` client or GitHub token is available in
this environment and no push was performed, so the job could not be rerun for
this patch. Local PostgreSQL 17.12 results are not hosted results.

## 5. Not enabled

- No worker, claim, lease, heartbeat, poller or event consumer.
- No automatic resume, resume grant or checkpoint resume transition.
- No crawler, model, browser, tool or MCP execution.
- No operation completion helper or fabricated `completed` state.
- No public API, manifest, event, configuration or schema change.
- No production/staging/shared database mutation.
