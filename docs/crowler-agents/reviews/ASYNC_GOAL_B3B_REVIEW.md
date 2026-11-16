# WP-05B3B Review: Trusted Resume Eligibility and Current Authority

## Scope

This work package adds three internal capabilities:

1. a historical approval-chronology fix in the one-operation observer;
2. a read-only resume-eligibility service that loads the current persisted
   checkpoint and revalidates current user, source and agent authority;
3. SQLite and disposable PostgreSQL tests, including a real
   scheduler-to-observer acceptance test.

Nothing here activates a worker, a claim/lease, automatic resumption or goal
success. Schema remains **1.17** and writer generation remains
**`verified-ownership-v1.17`**.

## 1. How does CROWler decide whether a paused agent may continue?

`goalAsyncResumeEligibility.Evaluate(ctx, runID, operationID)` accepts only a
run ID and an operation ID. It loads the current stored checkpoint itself; a
caller cannot supply a historical or fabricated checkpoint.

It returns one closed outcome:

| Outcome | Meaning |
| --- | --- |
| `eligible` | The operation is reported completed and every required current check passed. This is **not** an executable grant. |
| `waiting` | The goal is sound but the operation is still `accepted`, `already_pending` or `processing`. |
| `denied` | Cancellation, deadline, budget, current account, source ownership, agent identity, capability, trust, contract, policy, scope or a terminal operation forbids continuation. |
| `uncertain` | Identity, chronology, ledger, operation or checkpoint consistency cannot be established. |
| `unavailable` | A required database or trusted authority source could not be read. It is never permission to continue. |

Every result carries a stable reason code and bounded identifiers/state only.
No raw `Status.details`, policy text, URLs, configuration, arguments or
credentials are returned.

## 2. Where does each authorization fact come from?

| Fact | Trusted source |
| --- | --- |
| Stored goal state and frozen scope | `LoadCheckpointWithCancellation`, which reads the root, children, transition ledger and cancellation marker from one read-only snapshot. |
| Cancellation | `AgentGoalRuns.cancel_requested_at` in the same snapshot. |
| Current user | `auth.ResolveActiveIdentity` over the live `Users` and grant tables (active, not deleted, numeric subject, current username/roles/scopes). The checkpoint username and user ID must match. |
| Current source ownership | The existing `DatabaseCrawlSourceAuthorizer` with verified `SourceOwnership`, no administrator override. Legacy `usr_id` without provenance fails. |
| Current agent identity and goal | `goalAsyncAgentAuthorityProvider`. The production default is absent and fails closed. The explicit registry-backed provider requires the exact agent ID, owner, `GoalID`, `GoalSpecHash`, policy hash, tool set, source set, `goal_execution` capability, trusted standing and a contract that does not forbid `AgentGoalRun`. |
| Current operation | The WP-05B3A read-only observer, with strict identity and chronology checks. |

The registry-backed provider is a process-snapshot helper for trusted internal
callers and tests. It does not prove that an on-disk manifest has not changed;
production activation must supply a provider backed by the deployment's
trusted reload source. No provider is registered globally.

## 3. What happens when account, agent or source changes?

- Disabled, deleted, missing or renamed user: `denied`.
- Transferred, deleted or unverified source ownership: `denied`.
- Missing/disabled agent, changed owner or agent ID: `denied`.
- Revoked `goal_execution`, insufficient trust, forbidden `AgentGoalRun`
  contract: `denied`.
- Changed goal spec, policy hash, tool set or source set: `denied`.
- Missing/unreadable authority provider: `unavailable`.
- System principal: `denied` with `goal_resume_denied_system_principal_unsupported`.
  No user identity is fabricated and no privileged system authority is assumed.

The service performs the user, agent and source checks twice: once before
operation correlation and once after it, then compares the checkpoint revision,
state, update time and cancellation marker against a second snapshot. This
narrows the time-of-check window but does not close it. No claim/lease exists
yet, so a change that commits after the final read is not covered. Any future
executor must repeat the checks under its execution transaction. This is a
time-of-check/time-of-use limitation, not a solved race.

## 4. What happens when the crawl is still processing?

`accepted`, `already_pending` and `processing` produce `waiting` after the
current-authority checks pass. A terminal non-success operation produces
`denied`. Missing, malformed, contradictory or deleted operation records
produce `uncertain`; a database failure produces `unavailable`.

A source-level `crawl_completed` event is never read and cannot prove the
status of this specific operation.

## 5. Why does a completed crawl not mean the goal is successful?

`reported_completed` only says the operation record reports completion. Goal
acceptance requires the existing evidence ledger and deterministic
`EvaluateGoal` in a later authorized execution phase. This package constructs
no `goalAsyncVerifierDecision`, appends no evidence and never transitions the
checkpoint.

## 6. Historical approval chronology fix

The observer now rejects an operation whose acceptance time precedes
`CrawlApprovals.approved_at`, in addition to requiring acceptance before the
proposal and approval expiries. Revocation at or before acceptance remains a
conflict; revocation or expiry strictly after valid acceptance remains a
historical fact and does not retroactively invalidate the accepted operation.
`Status.last_updated_at` before `Status.created_at` is rejected as inconsistent.

## 7. Real scheduler compatibility

A new acceptance test creates real `Users`, `Sources`, `SourceOwnership`,
`CrawlProposals` and `CrawlApprovals` rows, calls
`ScheduleSourceCrawlRequestForProposal`, reads the authoritative persisted rows,
takes `Status.created_at` from the database, builds the internal work reference
from those values, and runs the observer and eligibility service. It passes on
SQLite and PostgreSQL 17.12.

## 8. Blocker for automatic resumption

No production code advances the specific `Status` operation row after
scheduling. The scheduler writes `accepted`/`already_pending`; the crawler
updates `Sources.status` and emits a source-level `crawl_completed` event, but
it does not update this operation record to `completed`. Consequently, a real
operation remains `pending` from the observer's perspective.

This is a blocker for automatic resumption. Manufacturing completion from a
source-level event, source status or model text would violate the operation
identity requirement. The next execution phase must either make the crawler
update the exact durable operation record or add a separately reviewed,
authenticated correlation mechanism.

## 9. What is still required before an agent can actually resume?

- Durable execution ownership: claim/lease with fencing, heartbeat and expiry.
- An execution transaction that revalidates user, agent, policy and source
  authority, cancellation and the exact operation under the claim.
- Operation completion correlation (see the blocker above).
- Cancellation finalization semantics and actor authority.
- Bounded trigger/polling and crash-safe recovery.
- Retention, production activation and deployment approval.

## 10. Verification (local)

| Command | Result |
| --- | --- |
| `go test ./pkg/agent/... -count=1` | PASS |
| `go test ./pkg/database/... -count=1` | PASS |
| `go test ./... -count=1` | PASS |
| `go test -race ./pkg/agent/... -count=1` | PASS |
| `go vet ./...` | PASS |
| `go vet -tags=integration ./pkg/agent ./pkg/database` | PASS |
| `CGO_ENABLED=0 go build ./...` | PASS |
| `CGO_ENABLED=1 go build ./...` | PASS |
| `git diff --check` | PASS |
| `TestPostgresGoalAsync*` (13 tests) on disposable PostgreSQL 17.12 | PASS, no SKIP |
| `TestPostgresMCPMutation*` (6 tests) on disposable PostgreSQL 17.12 | PASS, no SKIP |
| `actionlint` on `.github/workflows/postgresql-integration.yml` | PASS |

GitHub Actions has **not** run this patch. Hosted CI is **PENDING**, not
passed. The dedicated workflow step now asserts every new PostgreSQL test by
name and fails on `--- SKIP`.

## 11. Additional limitations

- The shipped SQLite setup does not create the `Users`/`AuthRoles` tables used
  by `auth.ResolveActiveIdentity`. SQLite eligibility therefore fails closed
  unless the deployment supplies that auth schema. PostgreSQL is the production
  verification path exercised by the real-scheduler test.
- The registry-backed agent provider is a process snapshot. A changed on-disk
  manifest is not observed until the deployment reloads and supplies a new
  provider; production activation must use the deployment's trusted source.
- The final authority re-check still precedes the function return. A commit
  after that read requires the future execution transaction to revalidate.

## 12. What is not enabled

- No worker, claim, lease, heartbeat, poller or event consumer.
- No automatic resume, resume grant or checkpoint resume transition.
- No scheduling, crawl, model, tool, browser or MCP execution.
- No goal-success decision or evidence append.
- No public API, manifest field, event type or configuration key.
- No schema, migration or writer-version change.
- No production activation or shared-database change.
