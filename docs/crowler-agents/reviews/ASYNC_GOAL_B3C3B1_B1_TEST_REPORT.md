# WP-05B3C3B1 B1 test report

## Environment

- Repository: `pzaino/thecrowler`
- Branch: `develop`
- Base: `5457f666e2a0ea0ea93237cfa7d1db0fe3292603`
- Worktree: `/home/pzaino/tmp/opencode/thecrowler-b1`
- Go: `go1.27.3 linux/amd64`
- Disposable PostgreSQL: `postgres:17` on host port `55503`
- Runtime role: `crowler`; administrative role: `postgres`
- Release: `v2.3.0`
- Schema: `1.17`
- Writer generation: `verified-ownership-v1.17`
- Attempt capability: `source-crawl-attempt-v1`, checksum unchanged
  `f545b987f0d77fcaac8219deefb70a9c00a354cf90ceef3aca4d3949c830206e`

No shared, staging or production database was touched. No schema object, SQL
migration, capability checksum, default mode or writer version changed.

## Baseline CI

The reviewed baseline `5457f666` has eight successful GitHub checks, including
`postgres-integration`:

- https://github.com/pzaino/thecrowler/actions/runs/44210494166/job/134487286235

This is baseline evidence only. The B1 patch has not been pushed, so no hosted
run exists for it.

## Approval

B1 Option A and B2 were approved by the repository maintainer on 2026-11-18
under contract `WP-05B3C3/Stage-B-2`. The record is in
[`ASYNC_GOAL_B3C3_STAGE_B_APPROVAL_REQUEST.md`](ASYNC_GOAL_B3C3_STAGE_B_APPROVAL_REQUEST.md).
B3 remains not approved. This report covers B1 only; B2 is a separate follow-up.

## Implemented surface

- `pkg/database/source_crawl_result.go`
  - `renewSourceCrawlAttemptLease` / `...At`
  - `reportSourceCrawlAttemptTerminal` / `...At`
- `pkg/database/source_crawl_result_test.go`
- `pkg/database/source_crawl_result_postgres_integration_test.go`
- `.github/workflows/postgresql-integration.yml` mandatory gates
- design, handoff, approval and decision documents

No runtime caller was added. A repository-wide search for the new method names
returns only the implementation and tests.

## Local commands and results

| Command | Result | Log |
| --- | --- | --- |
| `go test ./pkg/database/... -count=1` | PASS | `b1-db-final3.log` |
| `go test ./pkg/agent/... -count=1` | PASS | `b1-agent-final3.log` |
| `go test ./pkg/crawler/... -count=1` | PASS | `b1-crawler-final3.log` |
| `go test ./... -count=1 -timeout 90m -p 1` | PASS | `b1-full-final3.log` |
| `go test -race ./pkg/database/... -count=1` | PASS | `b1-race-final3.log` |
| `go vet ./...` | PASS | `b1-vet-final3.log` |
| `go vet -tags=integration ./pkg/database ./pkg/agent` | PASS | `b1-veti-final3.log` |
| `CGO_ENABLED=0 go build ./...` | PASS | local |
| `CGO_ENABLED=1 go build ./...` | PASS | local |
| `git diff --check` | PASS | local |
| `actionlint .github/workflows/postgresql-integration.yml` | PASS | `b1-actionlint-final3.log` |

The full suite used `TMPDIR=/dev/shm/opencode`.

## SQLite coverage

`go test ./pkg/database -run '^TestSourceCrawlResultSQLite' -count=1` passes.

| Test | Coverage |
| --- | --- |
| `TestSourceCrawlResultSQLiteTerminalBoundLifecycle` | accepted->completed, processing->failed, accepted->cancelled; reason, terminal time, Status and source state; replay after expiry; conflicting outcome/engine |
| `TestSourceCrawlResultSQLiteTerminalUnboundOnly` | unbound history write, no `Status` row, no source change, replay |
| `TestSourceCrawlResultSQLiteTerminalPolicy` | revoked before/after acceptance, approval expiry after acceptance, competing proposal, invalid proposal |
| `TestSourceCrawlResultSQLiteSourceStatusPolicy` | owner transfer, disabled source, deleted source |
| `TestSourceCrawlResultSQLiteHeartbeat` | both leases atomically extended, 2h rejection, 24h lifetime, wrong engine/generation/attempt, exact-expiry stale, original claim replay conflict |
| `TestSourceCrawlResultSQLiteUnboundHeartbeat` | unbound heartbeat returns the unattributed class, extends both leases and creates no `Status` row |
| `TestSourceCrawlResultSQLiteSupersededAndTerminalFences` | superseded attempt returns stale for terminal and heartbeat |
| `TestSourceCrawlResultSQLiteLegacyMode` | both operations return unsupported without mutation |
| `TestSourceCrawlResultSQLiteRollback` | Status CAS failure rolls back attempt; history lease failure rolls back current lease |
| `TestSourceCrawlResultSQLiteTerminalReplayAfterReplacement` | read-only replay after a new generation, no generation change |
| `TestSourceCrawlResultSQLiteClaimHistoryMismatch` | claim request, engine, claimed-at and lease mismatches conflict with zero writes |
| `TestSourceCrawlResultSQLiteAlreadyPendingAndMissingProposal` | already-pending is unbound history only; missing proposal and invalid chronology conflict |
| `TestSourceCrawlResultSQLiteReconcilePaths` | terminal and heartbeat commit reconciliation paths |
| `TestSourceCrawlResultSQLiteReplayAfterSourceDeletion` | exact terminal replay remains read-only after source deletion |
| `TestSourceCrawlResultSQLiteCorruptStoredReplay` | a corrupt stored operation binding is a conflict, not a replay |

## PostgreSQL coverage

The disposable database was initialized from `postgresql-setup.pgsql`.
Application operations used the restricted `crowler` role; `postgres` was used
only for fixtures, mode changes, trigger injection and cleanup.

`go test -tags=integration ./pkg/database -run '^TestPostgresSourceCrawl(Attempt|Result)'`
passed **30 tests, 0 failures, 0 skips** (`b1-pg-all-final3.log`): the 22 existing
claim tests plus 8 new B1 result tests.

| New test | Coverage |
| --- | --- |
| `TestPostgresSourceCrawlResultLifecycle` | bound completed result, state/operation/source updates, replay, restart through a second connection pool, conflicting outcome |
| `TestPostgresSourceCrawlResultHeartbeat` | atomic lease extension, 24h limit, expired boundary, claim-request replay conflict |
| `TestPostgresSourceCrawlResultConcurrency` | concurrent identical terminal receipts: one applied and one replay; concurrent conflicting receipts: one applied and one conflict |
| `TestPostgresSourceCrawlResultRestrictedRole` | runtime role cannot change policy, execute the operator mode function or delete attempt history |
| `TestPostgresSourceCrawlResultRollback` | injected Status failure rolls back the attempt result |
| `TestPostgresSourceCrawlResultReplayAfterReplacement` | read-only replay after a newer generation |
| `TestPostgresSourceCrawlResultConcurrentHeartbeat` | concurrent renewals leave equal current/history leases within the ordered extension range |
| `TestPostgresSourceCrawlResultLockTimeout` | held source lock with a bounded 200 ms lock timeout leaves all state unchanged |

The real proposal/approval/scheduler/claim/observation test also passed
(`b1-pg-handoff-final3.log`). The broad tagged suite over `pkg/database`,
`pkg/crawler`, `pkg/agent` and `services/api` passed
(`b1-pg-broad-final3.log`).

### Local sequential requirement

The `legacy`/`fenced` mode is a global policy row. Run claim and handoff suites
sequentially against one disposable database. Do not run them concurrently.

## CI gates

The existing source-crawl PostgreSQL step now runs
`^TestPostgresSourceCrawl(Attempt|Result)` and requires visible `RUN`/`PASS`
lines for the 8 new B1 tests, in addition to the existing claim tests. It still
fails on any `--- SKIP`.

## Not run

- B2 live crawler handoff. It is approved but deliberately separate from B1.
- Any live crawler, VDI, agent, MCP, plugin, event consumer or production
  execution.
- Any automatic goal resume or goal-success decision.
- Any B3 DDL, migration, addon or capability change.
- Any shared, staging or production database operation.
- Hosted GitHub CI for this patch. It has not been pushed.
- MySQL claim/result tests. MySQL remains legacy-only and reports unsupported.
- A live Docker Compose application stack.

## Limitations

- B2 must add the crawler-side handoff adapter and define index-write,
  cancellation and external-effect fencing. B1 does not fence those effects.
- Option A cannot prove the original `ExpectedState`, original unbound class,
  exact replayed observation time or a canonical receipt digest.
- The source-status update is intentionally conservative: unbound attempts never
  change it, and a transferred or disabled source preserves it.
- A concurrent pair of heartbeats may both succeed in order (extending twice) or
  one may conflict; the lease CAS guarantees a consistent pair, not a
  single-use heartbeat token.
- SQLite has no per-role database privileges.
