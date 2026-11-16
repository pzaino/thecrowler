# WP-05B3A Review: Crawl-Operation Observation and Cancellation Fencing

## Scope

This work package adds three internal behaviours to the existing asynchronous
goal store:

1. a durable cancellation request now blocks new checkpoint transitions;
2. the transition ledger is checked against the checkpoint's last reason and
   timestamp, and against the checkpoint's time interval;
3. a read-only observer correlates one specific crawl scheduling operation
   through `CrawlProposals`, `CrawlApprovals` and `Status`.

The code remains internal. It is not constructed by a production service and
does not enable resumption, scheduling, crawling, model calls or MCP execution.

## 1. What can CROWler now verify about one crawl operation?

For one operation ID referenced by a stored goal checkpoint, the observer
reports a closed classification:

| Class | Meaning |
| --- | --- |
| `not_found` | No exact authoritative operation record exists, or the proposal was never submitted. |
| `pending` | The operation is `accepted` or `already_pending`. |
| `processing` | The operation is `processing`. |
| `reported_completed` | The operation record says `completed`. This is **not** goal success. |
| `terminal_non_success` | `denied`, `failed`, `cancelled`, `expired`, `disabled`, `not_found` or `unsupported`. |
| `uncertain` | A required authoritative row is missing or an unrecognised state needs reconciliation. |
| `conflict` | Stored identities or timestamps contradict each other. |
| `unavailable` | The read transaction or a database read failed. |

The result carries only bounded identifiers and timestamps: operation ID,
request ID, source ID, proposal state, approval state, operation state, reason
code, accepted-at and operation-updated-at. It never returns raw
`Status.details`, source URLs, configuration, tool arguments or credentials.

The exact `Status.status` value is preserved in `OperationState`; the
classification is a closed mapping over the existing `CrawlOperationState`
vocabulary.

## 2. How are identities compared?

The observer reads the three authoritative rows in one read-only snapshot
(repeatable read on PostgreSQL, read transaction on SQLite) and requires:

- the checkpoint to pass `validateGoalAsyncCheckpoint`;
- the operation ID to be present in the checkpoint's `Work` references as a
  `crawl_operation` and inside the frozen source scope;
- `CrawlProposals.request_id`, `run_id`, `agent_id`, `owner`,
  `principal_user_id`, `source_id`, `operation_type`, `proposal_sha256` and
  `expires_at` to match the checkpoint/work reference;
- the proposal's submitted `operation_id` to equal the observed operation;
- `CrawlApprovals` to match approval ID and hash, proposal hash, request, run,
  agent, owner, principal, source, operation type and approval expiry;
- `Status.status_sha256`, `agent_id`, `source_id` and `operation_type` to match;
- `Status.created_at` to equal the checkpoint's accepted-at fact;
- `Status.details` to be strict JSON (duplicate keys rejected, unknown fields
  rejected, 64 KiB cap) whose request/run/agent/owner/principal/source/
  operation type/parameter hash/approval identity match the authoritative rows;
- `details.approved_at`, `details.expires_at` and the deterministic
  `CrawlOperationKey(...)` to agree with the stored approval and work reference.

The deterministic key is used only as an additional consistency check. It is
never treated as proof that an operation exists or was authorized.

## 3. What remains unknown?

- `reported_completed` is only the operation record's claim. Goal acceptance
  criteria remain unproven until the existing evidence ledger and
  `EvaluateGoal` run in a later authorized phase.
- A source-level `crawl_completed` event is not read at all and cannot satisfy
  any observation.
- Historical approval expiry or revocation after acceptance does not change the
  observation class. Revocation at or before acceptance is a conflict.
- The observer does not revalidate current account status, source ownership or
  policy. It returns no executable grant, so observation is not permission to
  resume or schedule.
- An operation whose `Status` row is missing while the proposal and approval
  exist is `uncertain`, because absence is not proof of failure.
- The ledger does not store full historical checkpoints. Exact replays return
  the current validated checkpoint plus `replayed`; the store does not promise
  historical snapshot reconstruction or independent verification of every
  intermediate hash.

## 4. What happens when cancellation is pending?

- `AgentGoalRuns.cancel_requested_at` is read inside the same transition
  transaction, under the PostgreSQL root lock or after the SQLite writer lock.
- Any new transition returns `ErrGoalAsyncCancellationPending` before the pure
  transition is applied. This includes terminal transitions; there is
  deliberately no cancellation-finalization API in this package.
- The root compare-and-swap includes `AND cancel_requested_at IS NULL` as the
  final race fence. A zero-row CAS re-reads the cancellation marker and returns
  `ErrGoalAsyncCancellationPending` instead of a misleading revision conflict.
- Exact replays of already stored operations are still recognized before the
  cancellation check and return `replayed` without writing.
- Loading for audit and diagnostics still succeeds and preserves the marker.
- The marker is never erased by a blocked transition.

## 5. Which corrupt ledger changes now fail closed?

`LoadCheckpoint` rejects the following with `errGoalAsyncStoreCorrupt`:

- a ledger row count that does not match the checkpoint revision;
- non-contiguous revisions or a broken state chain;
- an invalid state pair (not present in the closed transition table);
- any transition timestamp outside `created_at ... updated_at`;
- transition timestamps that run backwards;
- a last transition whose reason code, timestamp, operation ID, hash or target
  state disagrees with the checkpoint.

The loader never normalises or repairs corrupt rows. The bounded error messages
contain only revision numbers/field categories, not raw metadata.

## 6. Commands and results (local)

| Command | Result |
| --- | --- |
| `go test ./pkg/agent/... -count=1` | PASS |
| `go test ./pkg/database/... -count=1` | PASS |
| `go test ./... -count=1` | PASS |
| `go vet ./...` | PASS |
| `go vet -tags=integration ./pkg/agent ./pkg/database` | PASS |
| `go test -race ./pkg/agent -run 'TestAsyncGoal\|TestGoalAsyncStore\|TestGoalAsyncObservation' -count=1` | PASS |
| `go test -race ./pkg/database -run 'TestAsyncGoal\|TestSplitPostgresSQLStatements' -count=1` | PASS |
| `CGO_ENABLED=0 go build ./...` | PASS |
| `CGO_ENABLED=1 go build ./...` | PASS |
| `git diff --check` | PASS |
| `TestPostgresGoalAsync*` on PostgreSQL 17.12 disposable database | PASS (10 tests, no SKIP) |
| `TestPostgresMCPMutation*` six-test suite on PostgreSQL 17.12 | PASS (no SKIP) |
| Existing 1.17 startup/schema-protection tests | PASS |
| `actionlint` on `.github/workflows/postgresql-integration.yml` | PASS |

The PostgreSQL suites create a unique child database and drop only that
database. No shared or production database was touched.

GitHub Actions has **not** run this patch. Hosted CI is **PENDING**, not passed.
The dedicated workflow step now checks every new PostgreSQL test by name and
fails on `--- SKIP`.

## 7. What is not enabled?

- No production activation, resume engine or resume grant.
- No worker, claim, lease, heartbeat, poller or event consumer.
- No scheduler call, crawl submission, model call, tool call or MCP mutation.
- No call to `ReconcileCrawlProposalOperation`, `ScheduleSourceCrawlRequest`,
  `MarkCrawlProposalSubmitted` or `TransitionCheckpoint` from observation.
- No `goalAsyncVerifierDecision` is constructed, and no evidence is appended.
- No public API, manifest field, event type or configuration key.
- No schema change: schema remains **1.17** and writer generation remains
  **`verified-ownership-v1.17`**.
- No retention job, pruning or migration execution.

## 8. Pending maintainer decisions

Recorded in `ASYNC_GOAL_MAINTAINER_DECISIONS.md` without inventing an approval:

- production store activation and readiness gate;
- current-authority refresh and guarded resume admission (WP-05B3B);
- claim/lease worker, bounded trigger and crash recovery;
- cancellation finalization semantics and actor authority;
- retention/pruning policy;
- public async-goal contract;
- deployment and migration scheduling.

## 9. Residual risks

- A database owner can rewrite ledger or identity rows; the store validates
  internal consistency, not provenance against a signed manifest.
- A direct out-of-band `UPDATE` of `Status` or `CrawlApprovals` can make a
  coherent but false history that passes structural correlation.
- `ObserveCrawlOperation` reads the wall clock to label approval state
  (`active`/`expired`/`revoked`); this label is informational and never grants
  authority.
- Cancellation is fail-closed for all new transitions. A legitimate operator
  cancellation must be finalized by a later, separately reviewed API that
  defines actor semantics and a safe terminal transition.
- The observer's `Status.created_at` equality assumes the future adapter records
  the durable `Status.created_at` as the checkpoint's accepted-at fact.
