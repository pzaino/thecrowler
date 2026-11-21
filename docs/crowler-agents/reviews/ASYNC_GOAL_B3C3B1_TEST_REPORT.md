# WP-05B3C3B1 Test Report

## Environment

- Repository: `pzaino/thecrowler`
- Branch: `develop`
- Base: `ddf7e2942ae952d80489d00967720238aa2873d3`
- Worktree: `/home/pzaino/tmp/opencode/thecrowler-wp05b3c3b1`
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

The reviewed baseline `ddf7e294` has eight successful GitHub checks, including
`postgres-integration`:

- https://github.com/pzaino/thecrowler/actions/runs/44103689216/job/134110221645

This is baseline evidence only. The WP-05B3C3B1 patch has not been pushed, so no
hosted run exists for it.

## Phase status

**Phase A complete. Phase B blocked.**

The Stage B approval request remains *Request only. Not approved.* No
heartbeat writer, terminal repository, live crawler handoff, migration, worker
or production activation was implemented.

## Implemented Phase A changes

- **Authoritative receipt authority.** The pure authority snapshot now joins the
  current claim and exact history row, including both lease expiries. The
  validator requires them to agree, compares the receipt against the
  authoritative claim time and lease, and never lets a worker-supplied later
  lease increase authority. The closed classifier distinguishes
  `current_live`, `current_expired`, `superseded`, `terminal`, `missing`,
  `corrupt`, `unavailable` and `legacy_mode`.
- **Fresh post-lock clock.** Claim allocation and reconciliation use database
  time after locks: PostgreSQL `clock_timestamp()` (not transaction-start
  `CURRENT_TIMESTAMP`), SQLite statement time. The expiry boundary is exclusive
  and microsecond precision is consistent.
- **Complete unbound sanitization.** Any non-bound claim with any operation
  identity field (`OperationID`, `RequestID`, `RunID`, `AgentID`, `Owner`,
  `PrincipalUserID`, `ApprovalID`, `ApprovalSHA256`) is rejected rather than
  silently normalized.
- **Persisted replay decision table (Option A).** Terminal replay compares the
  exact persisted attempt identity, claim time, lease, binding, outcome, reason
  and operation result. `ObservedAt` is ignored; `ExpectedState` is a
  precondition only; original unbound classes normalize to `unbound`. An exact
  result remains replayable read-only after lease expiry or supersession.
- **Claim time tests.** A PostgreSQL test holds the exclusive policy lock until
  an existing lease expires, proves the waiting claim uses a fresh post-lock
  clock and allocates generation 2. SQLite covers the `-1µs` and exact-expiry
  claim boundary.
- **Real handoff fixture isolation.** The genuine PostgreSQL handoff test now
  gives its source a unique priority and restores the prior global claim mode
  instead of forcing `legacy`.
- **Crawler side-effect audit.** The design review inventories source-state,
  indexing, screenshots/files, time-series, events, email, plugin/agent,
  browser/VDI, early-failure and panic paths and classifies which remain
  unfenced.

## Commands and results

| Command | Result | Log |
| --- | --- | --- |
| `go test ./pkg/database/... -count=1` | PASS | `b3c3b1-db-final6.log` |
| `go test ./pkg/agent/... -count=1` | PASS | `b3c3b1-agent-final6.log` |
| `go test ./pkg/crawler/... -count=1` | PASS | `b3c3b1-crawler-final6.log` |
| `go test ./... -count=1 -timeout 90m -p 1` | PASS | `b3c3b1-full-final6.log` |
| `go test -race ./pkg/database/... -count=1` | PASS | `b3c3b1-race-final6.log` |
| `go vet ./...` | PASS | `b3c3b1-vet-final6.log` |
| `go vet -tags=integration ./pkg/database ./pkg/agent` | PASS | `b3c3b1-veti-final6.log` |
| `CGO_ENABLED=0 go build ./...` | PASS | local |
| `CGO_ENABLED=1 go build ./...` | PASS | local |
| `git diff --check` | PASS | local |
| `actionlint .github/workflows/postgresql-integration.yml` | PASS | `b3c3b1-actionlint-final6.log` |

The full suite used `TMPDIR=/dev/shm/opencode`.

## PostgreSQL runtime/admin separation

`go test -tags=integration ./pkg/database -run '^TestPostgresSourceCrawlAttempt'`
passed **21 tests, 0 failures, 0 skips**
(`b3c3b1-pg-claims-final6.log`). The new test is:

- `TestPostgresSourceCrawlAttemptPostLockClock`

`TestPostgresFullProposalApprovalSchedulerClaimAndObservation` passed with a
unique source priority and prior-mode restoration
(`b3c3b1-pg-handoff-final6.log`).

One late PostgreSQL attempt failed because the disposable container had already
been removed by an earlier cleanup step; every test failed at connection setup.
The container was re-provisioned from the repository setup SQL and the same
commands passed. No production code or test assertion was changed to obtain the
green result.

The complete tagged broad suite passed serially
(`b3c3b1-pg-broad-final6.log`):

```text
ok pkg/database
ok pkg/crawler
ok pkg/agent
ok services/api
```

### Local sequential requirement

The `legacy`/`fenced` mode is a global policy row. Run claim/handoff suites
sequentially against one disposable database. Do not run them concurrently.

## Pure protocol coverage

New or expanded tests cover:

- forged later lease and forged claim time against an authoritative snapshot;
- current/history lease disagreement;
- wrong source, attempt, generation, claim request and engine;
- expired, superseded, terminal, missing, corrupt, unavailable and legacy
  authority states;
- deleted, disabled, transferred and unverified-ownership source distinctions;
- approval revoked before acceptance vs revoked/expired after valid
  acceptance;
- wrong deterministic operation identity and receipt binding;
- every unbound operation-identity field rejected independently;
- first application vs identical replay vs conflicting replay;
- changed engine, claim request, source, generation, attempt, claim time, lease,
  binding, outcome, reason and operation result;
- changed `ObservedAt` accepted as idempotent retry;
- changed `ExpectedState` accepted as a precondition only;
- post-expiry exact replay without a new write;
- normalized unbound attribution;
- no goal transition on any replay path (the pure protocol has no goal code).

## Not run

- Any Phase B heartbeat or terminal repository.
- Any live crawler handoff, worker, event consumer or production execution.
- Any terminal `Status` write, goal resume or goal-success decision.
- Any shared, staging or production database migration.
- Hosted GitHub CI for this patch. It has not been pushed.
- MySQL claim tests. MySQL remains legacy-only.
- A live agent/engine Docker stack.

## Limitations

- Option A cannot prove the original `ExpectedState`, original unbound class,
  exact observation timestamp or a canonical receipt digest. Those require the
  separately requested Option B additive 1.17 extension.
- A terminal CAS alone does not fence crawler indexing, source-state writes,
  plugin/agent external effects or browser/VDI side effects. The audit records
  this explicitly; live handoff remains blocked on a later fencing design.
- A durable request fingerprint and empty-batch acknowledgement are still not
  implemented.
- SQLite has no per-role privileges.
