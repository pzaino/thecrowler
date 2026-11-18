# WP-05B3C3B1 B1 implementation: internal fenced result repository

## Status

**B1 Option A implemented internally. The B2 live crawler handoff is implemented
separately. B3 is not approved.**

The attributable approval is recorded in
[`ASYNC_GOAL_B3C3_STAGE_B_APPROVAL_REQUEST.md`](ASYNC_GOAL_B3C3_STAGE_B_APPROVAL_REQUEST.md):
repository maintainer, 2026-11-18, contract `WP-05B3C3/Stage-B-2`, B1 and B2
approved, B3 not requested.

The implementation uses only existing schema-1.17 objects. It adds no table,
column, index, capability marker, migration, default-mode change or runtime
grant.

## Deliberately internal surface

`pkg/database/source_crawl_result.go` provides two unexported operations:

- `renewSourceCrawlAttemptLease` / `renewSourceCrawlAttemptLeaseAt`
- `reportSourceCrawlAttemptTerminal` / `reportSourceCrawlAttemptTerminalAt`

The `...At` variants inject a trusted clock for deterministic tests. Production
code uses fresh database time (`clock_timestamp()` on PostgreSQL, statement-time
`julianday('now')` on SQLite) after the relevant locks.

No package outside `pkg/database` calls them. B2 will add the crawler-side
handoff adapter as a separately reviewed change.

## Closed result vocabulary

Both operations return one of the existing
`sourceCrawlTerminalReportResult` values:

```text
applied
idempotent_replay
stale_fence
conflict
unavailable
unsupported
```

`stale_fence`, `conflict`, `unsupported` and a matching `idempotent_replay` are
classified business outcomes with a nil error. `unavailable` carries the
underlying database error so the caller can log or retry safely.

## Transaction and lock order

Both operations:

1. validate the request/receipt shape before opening a transaction;
2. require PostgreSQL or SQLite and the existing attempt schema;
3. open one bounded transaction;
4. acquire the SQLite writer lock with a no-op update when needed;
5. take the shared PostgreSQL cutover advisory lock and require `fenced` mode;
6. lock the exact source row;
7. load the exact attempt history by attempt ID, source and generation;
8. for a new mutation, lock the current claim and recheck the history under the
   documented source -> current claim -> history order;
9. read fresh database time after the locks;
10. revalidate the fence, source state, operation identity and approval
    chronology;
11. apply compare-and-set updates and commit once.

Proposal, approval and ownership reads remain unlocked. They therefore cannot
form a cycle with the scheduler or ownership-transfer paths. Source deletion or
disablement is checked under the source lock.

## Heartbeat

`renewSourceCrawlAttemptLease` requires the exact source, attempt ID, generation,
claim request and engine. It verifies that the current claim and history rows
agree on every duplicated field.

A renewal:

- requires a positive extension of at most one hour;
- never shortens the lease;
- never extends past 24 hours after the immutable original `claimed_at`;
- rejects a missing, deleted, disabled, expired, superseded or terminal
  attempt;
- rejects a replaced current claim or a corrupt current/history pair;
- updates `SourceCrawlClaims.lease_expires_at` and
  `SourceCrawlAttempts.lease_expires_at` atomically with expected-old-lease
  compare-and-set predicates;
- returns the authoritative resulting lease;
- never changes an operation row, source status or goal state.

If the commit acknowledgment is lost, the repository re-reads both lease copies.
A matching target is reported as an evidenced applied renewal; an unchanged old
lease is a conflict rather than a blind retry. Reusing the original
`ClaimRequestID` after a renewal continues to fail closed because the original
requested lease duration is no longer recoverable from schema 1.17.

## Terminal result

`reportSourceCrawlAttemptTerminal` accepts the existing pure
`sourceCrawlTerminalReceipt` and revalidates it against the locked database
snapshot. First application and read-only replay are distinct:

- The attempt-history CAS moves exactly `claimed` to `completed`, `failed` or
  `cancelled` with `terminal_at` and the closed reason code.
- A bound attempt must still match exactly one submitted proposal, its durable
  approval, the deterministic operation key, the original acceptance chronology
  and the authoritative `Status` row. The bound operation CAS moves
  `accepted`/`processing` to the matching terminal state in the same
  transaction. `Status.details` is never rewritten.
- An unbound attempt updates only its own history row. It never creates a
  `Status` row and never updates `Sources.status`.
- Source-level status is updated only for a bound attempt whose verified owner
  still equals the bound operation principal, the source is live and enabled,
  and the current fence is still valid. A transferred owner or disabled source
  preserves the source row while the operation-specific result is recorded. A
  deleted source rejects a new terminal write.
- A committed terminal result remains read-only replayable after lease expiry,
  after a newer generation replaces the current claim, after source deletion and
  after a new process opens a new connection pool. A changed outcome, reason,
  engine, request, generation, binding or operation result is a conflict.
- A lost commit acknowledgment is reconciled by re-reading the durable attempt
  and operation state. Only an exact stored result becomes
  `idempotent_replay`; absence is not reported as success.

The receipt structural rule from WP-05B3C3B1S remains: `claimed_at <=
observed_at < lease_expires_at` against the receipt-carried lease, independent
of the current clock.

## Source-status mapping

| Outcome | Attempt state | Operation state | Source state when eligible |
| --- | --- | --- | --- |
| `completed` | `completed` | `completed` | `completed` |
| `failed` | `failed` | `failed` | `error` with `crawl_failed` |
| `cancelled` | `cancelled` | `cancelled` | `error` with `crawl_cancelled` |

The source update also requires `status = 'processing'`, so a state changed by
another path is not overwritten. A zero-row source update leaves the operation
result committed and reports `SourceStatusChanged=false`.

## What Option A still cannot prove

- the original `ExpectedState` observed at the first CAS;
- the original `ambiguous`/`not_attributable` class (normalized to `unbound`);
- exact equality of a replayed observation timestamp;
- a canonical persisted receipt digest or receipt ledger;
- a durable claim-request fingerprint or empty-batch acknowledgement.

Those require the separately gated B3 additive extension and are not
implemented.

## B1 limits

- No `cmd/thecrowler`, `pkg/crawler`, agent, event, MCP or public caller.
- No automatic mode switch, migration, deployment or production activation.
- No page-index, browser/VDI, plugin, email or external-effect fencing. B2 must
  define that fencing and cancellation behavior.
- MySQL remains legacy-only and reports unsupported.
- Schema remains `1.17`; writer generation remains
  `verified-ownership-v1.17`; the attempt capability checksum is unchanged.

## Tests

The implementation and test evidence are recorded in
[`ASYNC_GOAL_B3C3B1_B1_TEST_REPORT.md`](ASYNC_GOAL_B3C3B1_B1_TEST_REPORT.md).
