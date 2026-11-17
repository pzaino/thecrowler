# WP-05B3C3B1R Test Report

## Environment

- Repository: `pzaino/thecrowler`
- Branch: `develop`
- Base: `ce947dab6cad7f3a373818490018a87f69d12e8c`
- Worktree: `/home/pzaino/tmp/opencode/thecrowler-wp05b3c3b1r`
- Go: `go1.27.3 linux/amd64`
- Disposable PostgreSQL: `postgres:17` on host port `55503`
- Runtime role: `crowler`; administrative role: `postgres`
- Release: `v2.3.0`
- Schema: `1.17`
- Writer generation: `verified-ownership-v1.17`
- Capability checksum: unchanged `f545b987f0d77fcaac8219deefb70a9c00a354cf90ceef3aca4d3949c830206e`

No shared, staging or production database was touched. No schema object, SQL
migration, capability checksum or writer version changed.

## Baseline CI

The reviewed baseline `ce947dab` has eight successful GitHub checks, including
`postgres-integration`:

- https://github.com/pzaino/thecrowler/actions/runs/44134566092/job/134220466103

This is baseline evidence only. The WP-05B3C3B1R patch has not been pushed, so
no hosted run exists for it.

## Phase status

**Phase A complete. B1 blocked, approval needed.**

The Stage B approval request remains *Request only. Not approved.* No heartbeat
writer, terminal repository, live crawler handoff, migration, worker or
production activation was implemented.

## Implemented Phase A changes

- **Source-status eligibility follows full authority.** Source-status mutation
  now requires successful full terminal-authority validation. A
  revoked-before-acceptance approval, expired fence, missing/corrupt authority,
  wrong binding or unbound attempt cannot update source status. Later
  revocation/expiry after valid acceptance remains recordable subject to every
  other guard.
- **Complete current/history equality.** The authority snapshot now represents
  both rows for source, attempt, generation, claim request, engine, claim time
  and lease. Any disagreement is corrupt; `current_live` is not issued.
- **Unbound owner continuity.** Unbound source-status mutation is denied because
  schema 1.17 stores no unbound operation principal. A future B1 repository may
  record its attempt-history result but must not overwrite source status.
- **Per-source claim timing.** A batch reads a selection timestamp after the
  policy lock, a fresh timestamp after each source row lock before its claim
  mutation, and a final freshness timestamp. A short lease that expires during a
  long batch is returned as `stale_fence`, not executable `claimed`.
- **Heartbeat replay decision.** Reusing an original `ClaimRequestID` after a
  heartbeat renewed the lease returns `conflict`; the lease is not reset and
  full request equality is not claimed.
- **Structural replay validation.** Read-only replay now validates the incoming
  receipt structure independently of current-lease eligibility. A malformed
  receipt cannot be idempotent merely because durable identifiers match, while
  an exact post-expiry replay remains allowed.
- **Documentation.** Updated the design review, handoff contract, Stage B
  request and maintainer decisions with the corrected rules and explicit B1
  gate.

## Commands and results

| Command | Result | Log |
| --- | --- | --- |
| `go test ./pkg/database/... -count=1` | PASS | `b3c3b1r-db-final2.log` |
| `go test ./pkg/agent/... -count=1` | PASS | `b3c3b1r-agent-final2.log` |
| `go test ./pkg/crawler/... -count=1` | PASS | `b3c3b1r-crawler-final2.log` |
| `go test ./... -count=1 -timeout 90m -p 1` | PASS | `b3c3b1r-full-final2.log` |
| `go test -race ./pkg/database/... -count=1` | PASS | `b3c3b1r-race-final2.log` |
| `go vet ./...` | PASS | `b3c3b1r-vet-final2.log` |
| `go vet -tags=integration ./pkg/database ./pkg/agent` | PASS | `b3c3b1r-veti-final2.log` |
| `CGO_ENABLED=0 go build ./...` | PASS | local |
| `CGO_ENABLED=1 go build ./...` | PASS | local |
| `git diff --check` | PASS | local |
| `actionlint .github/workflows/postgresql-integration.yml` | PASS | `b3c3b1r-actionlint-final2.log` |

The full suite used `TMPDIR=/dev/shm/opencode`.

## PostgreSQL runtime/admin separation

`go test -tags=integration ./pkg/database -run '^TestPostgresSourceCrawlAttempt'`
passed **21 tests, 0 failures, 0 skips** (`b3c3b1r-pg-claims-final2.log`).

`TestPostgresFullProposalApprovalSchedulerClaimAndObservation` passed with its
isolated priority and prior-mode restoration (`b3c3b1r-pg-handoff-final2.log`).

The complete tagged broad suite passed serially (`b3c3b1r-pg-broad-final2.log`):

```text
ok pkg/database
ok pkg/crawler
ok pkg/agent
ok services/api
```

### Local sequential requirement

The `legacy`/`fenced` mode is a global policy row. Run claim/handoff suites
sequentially against one disposable database. Do not run them concurrently.

## New or expanded pure tests

- revoked-before-acceptance authority denies source status;
- expired, missing and corrupt authority denies source status;
- unbound attempt with current verified ownership still denies source status;
- history/current mismatch for source, attempt, generation, claim request,
  engine, claim time and lease is corrupt;
- per-source claim timing across a two-source SQLite batch: first short lease
  expires during the batch and returns `stale_fence`, second remains `claimed`,
  current/history leases match and no duplicate generation appears;
- original claim replay passes, heartbeat-renewed and changed-engine replay
  conflicts;
- malformed incoming replay receipt (terminal shape, observation before claim,
  missing binding) conflicts;
- corrupt stored terminal result conflicts;
- exact post-expiry replay remains idempotent;
- changed `ObservedAt` is ignored; changed valid `ExpectedState` is a
  precondition only.

## Not run

- Any B1 heartbeat or terminal repository.
- Any live crawler handoff, worker, event consumer or production execution.
- Any terminal `Status` write, goal resume or goal-success decision.
- Any B3 DDL, migration, addon or capability change.
- Any shared, staging or production database operation.
- Hosted GitHub CI for this patch. It has not been pushed.
- MySQL claim tests. MySQL remains legacy-only.
- A live agent/engine Docker stack.

## Limitations

- B1 remains blocked pending an attributable Option A approval.
- Option A cannot prove original `ExpectedState`, original unbound class, exact
  observation timestamp or a canonical receipt digest.
- Crawler indexing/source-state writes and plugin/agent/browser/VDI external
  effects remain unfenced; live B2 handoff requires a later fencing design.
- A durable request fingerprint and empty-batch acknowledgement remain B3
  design-only.
- SQLite has no per-role privileges.
