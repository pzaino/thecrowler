# WP-05B3C3B2 test report

## Environment

- Repository: `pzaino/thecrowler`
- Branch: `develop`
- Base: `3443beebd5186047f89f748b1873c2250cff12b8`
- Worktree: `/home/pzaino/tmp/opencode/thecrowler-b2`
- Go: `go1.27.3 linux/amd64`
- Disposable PostgreSQL: `postgres:17` on host port `55503`
- Runtime role: `crowler`; administrative role: `postgres`
- Release: `v2.3.0`
- Schema: `1.17`
- Writer generation: `verified-ownership-v1.17`
- Attempt capability: `source-crawl-attempt-v1`, checksum unchanged

No shared, staging or production database was touched. No schema object, SQL
migration, capability checksum, default mode or writer version changed.

## Baseline CI

At `3443beeb`, eight checks were reported: seven succeeded and
`postgres-integration` was **cancelled**:

- https://github.com/pzaino/thecrowler/actions/runs/44227042509/job/134542629517

This is baseline evidence only. The B2 patch has not been pushed, so no hosted
run exists for it. Local PostgreSQL results are not hosted CI evidence.

## Implemented surface

- `pkg/database/source_crawl_runtime.go`
  - exported `SourceCrawlHandoff`, result types and outcome vocabulary
  - `RenewLease`, `ReportTerminal`, `UpdateUnboundSourceStatus`
  - test clock seams for the adapter
- `pkg/crawler/source_crawl_handoff.go`
  - coordinator interface, heartbeat, cancellation and terminal helpers
- `pkg/crawler/source_crawl_handoff_test.go`
- `pkg/crawler/{types.go,process.go,crawler.go,web.go,action_rules.go,rule_call_runtime.go}`
  - handoff transport, context/cancellation and write fences
- `cmd/thecrowler/main.go`
  - fenced claim branch, queue handoff, VDI-failure reporting, batch drain
- `pkg/database/source_crawl_runtime_test.go`
- `cmd/thecrowler/main_test.go`

No public API, configuration field, event schema, agent manifest or automatic
mode switch was added.

## Local commands and results

| Command | Result | Log |
| --- | --- | --- |
| `go test ./pkg/database/... -count=1` | PASS | `b2-db-final2.log` |
| `go test ./pkg/crawler/... -count=1` | PASS | `b2-crawler-final2.log` |
| `go test ./cmd/thecrowler/... -count=1` | PASS | `b2-cmd-final2.log` |
| `go test ./... -count=1 -timeout 90m -p 1` | PASS | `b2-full-final3.log` |
| `go test -race ./pkg/crawler/... ./pkg/database/... ./cmd/thecrowler/... -count=1` | PASS | `b2-race-final3.log` |
| `go vet ./...` | PASS | `b2-vet-final3.log` |
| `go vet -tags=integration ./pkg/database ./pkg/agent` | PASS | `b2-veti-final3.log` |
| `CGO_ENABLED=0 go build ./...` | PASS | local |
| `CGO_ENABLED=1 go build ./...` | PASS | local |
| `git diff --check` | PASS | local |
| `actionlint .github/workflows/*.yml` | PASS | `b2-actionlint-final3.log` |

The full suite used `TMPDIR=/dev/shm/opencode`.

## Database adapter tests

`go test ./pkg/database -run '^TestSourceCrawlHandoffSQLite' -count=1` passes:

| Test | Coverage |
| --- | --- |
| `TestSourceCrawlHandoffSQLiteBound` | handoff identity, lease renewal updates both rows, completed terminal result, source status, read-only replay, terminal heartbeat refusal, bound unbound-status refusal |
| `TestSourceCrawlHandoffSQLiteUnbound` | lease renewal, history-only terminal report, no `Status` row, separate fenced source-status update, idempotent replay |
| `TestSourceCrawlHandoffSQLiteRejectsInvalidInput` | invalid outcome and stale claim refusal |
| `TestSourceCrawlHandoffSQLiteLegacyMode` | unsupported renewal, terminal and unbound status in legacy mode |

## Crawler tests

`go test ./pkg/crawler -run '^Test(StartSourceCrawlHeartbeat|ReportSourceCrawlTerminalOutcomes|UpdateSourceStateForCrawlSkipsFenced|CheckCrawlFence)' -count=1`
passes:

- heartbeat aborts the crawl on `stale_fence` with a stable `lease_lost` reason;
- heartbeat stops with context cancellation;
- completed/failed/cancelled terminal outcomes are selected correctly;
- unbound source status is updated through the separate fenced path;
- fenced `UpdateSourceState` calls return without touching a nil database;
- fence checks reject a canceled crawl context.

## Command tests

`go test ./cmd/thecrowler -count=1` passes, including:

- `TestRetrieveAvailableSourcesPreservesClaimSubPriority` — legacy
  `update_sources` behavior remains unchanged;
- `TestRetrieveAvailableSourcesFencedSQLite` — a real SQLite schema, fenced
  mode, one claimed source, handoff creation, terminal report, separate unbound
  source-status update and final `completed` source state;
- `TestParseSourceCrawlConfigDuration` — config interval normalization;
- `TestNewSourceCrawlClaimRequestID` — 256-bit request identity;
- `TestDrainFencedSourcesReportsCancelled` — queued source drain reports
  cancelled.

## PostgreSQL integration

The disposable database was initialized from `postgresql-setup.pgsql`.
Application operations used the restricted `crowler` role; `postgres` was used
only for fixtures, mode changes, trigger injection and cleanup.

`go test -tags=integration ./pkg/database -run '^TestPostgresSourceCrawl(Attempt|Result)'`
passed **30 tests, 0 failures, 0 skips** (`b2-pg-all2.log`): the 22 claim tests
and 8 B1 result tests. `TestPostgresFullProposalApprovalSchedulerClaimAndObservation`
also passed (`b2-pg-handoff2.log`).

The B2 patch adds no PostgreSQL-specific DDL or test; the existing B1
PostgreSQL suites continue to cover the database repository used by the B2
adapter.

## Not run

- A live VDI/Selenium browser crawl through the fenced path. No VDI image or
  Docker Compose stack was started in this environment.
- A live email connector, plugin, agent, MCP or Ollama run.
- The full Docker Compose application stack.
- Hosted GitHub CI for this patch. It has not been pushed.
- MySQL fenced mode. MySQL remains legacy-only.
- A shared, staging or production database.

## Limitations

- The heartbeat starts in `CrawlWebsite`; queued sources rely on the 30-minute
  claim lease while waiting.
- In-flight WebDriver, email, plugin and agent calls cannot be interrupted
  mid-call; the next fence check stops further work.
- Indexing rows do not carry attempt provenance. The pre-commit check leaves a
  narrow race between the final check and commit.
- The B2 unbound source-status update is an operational status record, not
  owner-continuity or operation evidence.
- Hosted PostgreSQL CI at the baseline was cancelled; local PostgreSQL results
  are not a substitute for a hosted run on the pushed commit.
