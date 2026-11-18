# WP-05B3C3B1S Test Report

## Environment

- Repository: `pzaino/thecrowler`
- Branch: `develop`
- Base: `7f1829cfd6126095655b12cde3d5d21ce25ad9f6`
- Worktree: `/home/pzaino/tmp/opencode/thecrowler-wp05b3c3b1s`
- Go: `go1.27.3 linux/amd64`
- Disposable PostgreSQL: `postgres:17` on host port `55503`
- Runtime role: `crowler`; administrative role: `postgres`
- Release: `v2.3.0`
- Schema: `1.17`
- Writer generation: `verified-ownership-v1.17`
- Attempt capability: `source-crawl-attempt-v1`, checksum unchanged
  `f545b987f0d77fcaac8219deefb70a9c00a354cf90ceef3aca4d3949c830206e`

No shared, staging or production database was touched. No schema object, SQL
migration, capability checksum or writer version changed.

## Baseline CI

The reviewed baseline `7f1829cf` has eight successful GitHub checks, including
`postgres-integration`:

- https://github.com/pzaino/thecrowler/actions/runs/44157304802/job/134301379678

This is baseline evidence only. The WP-05B3C3B1S patch has not been pushed, so
no hosted run exists for it.

## Phase status

**Phase A complete. B1 blocked, approval needed.**

The Stage B approval request remains *Request only. Not approved.* No heartbeat
writer, terminal repository, live crawler handoff, migration, worker or
production activation was implemented.

The baseline commit message describes an "internal fenced terminal repository",
but the actual diff contains only the WP-05B3C3B1R Phase A changes. A
repository-wide search found no heartbeat or terminal writer. The message was
not treated as implementation or approval evidence.

## Implemented Phase A changes

### All-class final freshness

`claimSourceCrawlAttemptsWithClock` already reads fresh database time after each
source row lock. Its final freshness pass previously demoted only bound
`SourceCrawlClaimed` results. It now demotes **every** newly allocated attempt
whose lease expired at the final trusted timestamp, including:

- `unattributed`;
- `ambiguous`;
- `not_attributable`;
- `conflict` (a newly allocated attempt that cannot bind).

The returned outcome becomes `stale_fence`. The committed attempt/current claim
rows are not rewritten, no terminal result is created, and the caller still has
to revalidate the authoritative fence before any privileged mutation. The
exclusive boundary is preserved: `now < lease_expires_at` is live,
`now == lease_expires_at` is stale, and later is stale.

### Observation-time structural replay rule

`sourceCrawlTerminalReceipt.validateStructure` now requires
`claimed_at <= observed_at < lease_expires_at` using the receipt-carried lease.
The rule does not read the current clock:

- an exact historical result remains replayable read-only after its lease has
  long expired;
- an incoming observation exactly at or after the original lease expiry is a
  conflict even when every durable identifier matches;
- `ObservedAt` remains ignored for replay identity because schema 1.17 does not
  persist it. Two different in-lease values replay the same stored result; the
  persisted `terminal_at` remains authoritative.

The live validator continues to require both a live current fence and an
in-lease observation.

### Documentation

Updated:

- `ASYNC_GOAL_B3C3B1_DESIGN_REVIEW.md`
- `ASYNC_GOAL_B3C3A_HANDOFF_AND_FENCED_RESULTS.md`
- `ASYNC_GOAL_B3C3_STAGE_B_APPROVAL_REQUEST.md`
- `ASYNC_GOAL_MAINTAINER_DECISIONS.md` (Decision 1i)
- `pkg/database/db_migrations/README.md`

The B3C3B1R report is marked as historical for the superseded freshness scope.

## New or expanded tests

### SQLite

`TestSourceCrawlAttemptSQLiteFinalFreshnessAllOutcomes` covers 12 subtests:
each of `bound`, `unattributed`, `ambiguous` and `not_attributable` across
`-1µs`, exact expiry and `+1µs`.

For every class and boundary it asserts:

1. the first source is `stale_fence` at or after expiry and retains its live
   outcome only before expiry;
2. the second source remains live with its correct bound or unbound outcome;
3. unique attempt IDs and one generation per source;
4. current and history rows agree on attempt, request, generation, engine and
   lease; returned lease equals the stored lease;
5. exactly one history row per source and no second allocation;
6. bound work has its real operation binding and one `Status` row; unbound work
   has no operation identity and creates no `Status` row;
7. replay at twice the lease returns `stale_fence`, `Reused=true`, the same
   attempt ID/generation and no new history row.

`TestSourceCrawlTerminalReceiptObservationTimeStructure` covers observations
inside the lease, exactly at claim, just before expiry, before claim, exactly at
expiry and after expiry. It also proves the structural rule still accepts an
in-lease observation 24 hours later while the live validator rejects it.

`TestSourceCrawlTerminalReplayClassification` now covers:

- a historical expired lease with an in-lease observation replaying read-only;
- changed valid in-lease observation values replaying idempotently;
- observations exactly at and after the original lease expiry conflicting;
- malformed terminal shape, observation before claim and missing binding
  conflicting even when durable identifiers match;
- corrupt stored terminal results conflicting.

### PostgreSQL

`TestPostgresSourceCrawlAttemptFinalFreshnessAllOutcomes` mirrors the SQLite
matrix using the deterministic clock seam (no sleeps) on a disposable database.
It creates two sources per subtest, injects selection/per-source/final
timestamps, and verifies outcomes, unique attempts, current/history lease
equality, binding presence or absence, one history row and read-only stale
replay.

## Commands and results

| Command | Result | Log |
| --- | --- | --- |
| `go test ./pkg/database/... -count=1` | PASS | `b3c3b1s-db-final.log` |
| `go test ./pkg/agent/... -count=1` | PASS | `b3c3b1s-agent-final.log` |
| `go test ./pkg/crawler/... -count=1` | PASS | `b3c3b1s-crawler-final.log` |
| `go test ./... -count=1 -timeout 90m -p 1` | PASS | `b3c3b1s-full-final.log` |
| `go test -race ./pkg/database/... -count=1` | PASS | `b3c3b1s-race-final.log` |
| `go vet ./...` | PASS | `b3c3b1s-vet-final.log` |
| `go vet -tags=integration ./pkg/database ./pkg/agent` | PASS | `b3c3b1s-veti-final.log` |
| `CGO_ENABLED=0 go build ./...` | PASS | local |
| `CGO_ENABLED=1 go build ./...` | PASS | local |
| `git diff --check` | PASS | local |
| `actionlint .github/workflows/postgresql-integration.yml` | PASS | `b3c3b1s-actionlint-final.log` |

The full suite used `TMPDIR=/dev/shm/opencode`.

## PostgreSQL runtime/admin separation

The disposable database was initialized from `postgresql-setup.pgsql`.
Application operations ran through the restricted `crowler` role; the `postgres`
role was used only for fixtures, mode changes and cleanup.

| Command | Result | Log |
| --- | --- | --- |
| `go test -tags=integration ./pkg/database -run '^TestPostgresSourceCrawlAttempt'` | **22 PASS, 0 FAIL, 0 SKIP** | `b3c3b1s-pg-claims-final.log` |
| `TestPostgresSourceCrawlAttemptFinalFreshnessAllOutcomes` alone | 12/12 subtests PASS | `b3c3b1s-pg-freshness-final.log` |
| `TestPostgresFullProposalApprovalSchedulerClaimAndObservation` | PASS | `b3c3b1s-pg-handoff-final.log` |
| Broad tagged suite over `pkg/database`, `pkg/crawler`, `pkg/agent`, `services/api` | PASS | `b3c3b1s-pg-broad-final.log` |

The new test is listed in the dedicated workflow gate, and the existing
`^TestPostgresSourceCrawlAttempt` regex covers it. No `--- SKIP` occurred.

### Local sequential requirement

The `legacy`/`fenced` mode is a global policy row. Run claim and handoff suites
sequentially against one disposable database. Do not run them concurrently.

## Schema and capability

- `RequiredSchemaVersion`: `1.17` (unchanged)
- `WriterGeneration`: `verified-ownership-v1.17` (unchanged)
- `source-crawl-attempt-v1` checksum: unchanged
- No DDL, migration, addon, grant or default-mode change
- No MySQL change; MySQL remains legacy-only
- No runtime PostgreSQL role privilege change

## Not run

- Any B1 heartbeat or terminal repository. B1 has no attributable approval.
- Any live crawler handoff, worker, event consumer or production execution.
- Any terminal `Status` write, source-status write, goal resume or goal-success
  decision.
- Any B3 DDL, migration, addon or capability change.
- Any shared, staging or production database operation.
- Hosted GitHub CI for this patch. It has not been pushed.
- MySQL claim tests.
- A live agent/engine Docker stack.

## Limitations

- B1 remains blocked pending an attributable Option A approval with approver,
  UTC date, `WP-05B3C3/Stage-B-2` contract version and explicit heartbeat and
  terminal scope.
- Option A cannot prove the original `ExpectedState`, the original unbound
  class, exact observation-time equality or a canonical receipt digest.
- The final freshness check is a pre-commit decision. A worker must still
  re-read the authoritative fence inside its privileged mutation transaction;
  one prior check cannot guarantee authority later.
- Crawler indexing/source-state writes and plugin/agent/browser/VDI external
  effects remain unfenced. Live B2 handoff requires a later approved package.
- A durable request fingerprint and empty-batch acknowledgement remain B3
  design-only.
- SQLite has no per-role database privileges.
