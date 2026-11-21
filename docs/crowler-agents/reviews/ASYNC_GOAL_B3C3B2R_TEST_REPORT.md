# WP-05B3C3B2R test report

> **Corrected by WP-05B3C3B2V.** The unbound owner-continuity and quarantine
> expectations recorded here were superseded: unbound source-status mutation is
> now suppressed, and quarantine no longer marks the session as returned or
> clears shared crawl state. See `ASYNC_GOAL_B3C3B2V_TEST_REPORT.md`.

## Environment

| Item | Value |
| --- | --- |
| Repository | `pzaino/thecrowler` |
| Branch | `develop` |
| Reviewed base | `aa906c86671ca1b5dd3e8c31c0210ad40951be90` |
| Worktree | `/home/pzaino/tmp/opencode/thecrowler-b2r` |
| Go | `go1.27.3 linux/amd64` |
| Disposable PostgreSQL | `postgres:17` on host port `55503` |
| Runtime role | `crowler`; administrative role `postgres` |
| Release | `v2.3.0` |
| Schema | `1.17` |
| Writer generation | `verified-ownership-v1.17` |

No shared, staging or production database was touched. No schema object, SQL
migration, capability checksum, default mode or writer version changed.

## Baseline hosted CI

`aa906c86` has eight successful hosted checks:

- build: https://github.com/pzaino/thecrowler/actions/runs/44237120418/job/134575821289
- PostgreSQL integration: https://github.com/pzaino/thecrowler/actions/runs/44237120408/job/134575821858
- SQL parser CGO=0: https://github.com/pzaino/thecrowler/actions/runs/44237120470/job/134575821823
- SQL parser CGO=1: https://github.com/pzaino/thecrowler/actions/runs/44237120470/job/134575821937
- Static linux/amd64: https://github.com/pzaino/thecrowler/actions/runs/44237120470/job/134575821934
- Static linux/arm64: https://github.com/pzaino/thecrowler/actions/runs/44237120470/job/134575821819
- Static darwin/amd64: https://github.com/pzaino/thecrowler/actions/runs/44237120470/job/134575821759
- Static darwin/arm64: https://github.com/pzaino/thecrowler/actions/runs/44237120470/job/134575821452

This is baseline evidence only. The B2R patch has not been pushed, so no hosted
run exists for it.

## Implemented surface

Database:

- `pkg/database/source_crawl_fence.go` (new): `SourceCrawlFenceResult`,
  `SourceCrawlFenceStatus`, `SourceCrawlHandoff.ValidateCurrent`,
  `SourceCrawlHandoff.ValidateTx`, shared validator, read-only and
  transaction-held variants.
- `pkg/database/source_crawl_attempt.go`: `SourceCrawlClaimModePolicy`.
- `pkg/database/source_crawl_attempt_schema.go`:
  `SourceCrawlAttemptSchemaPresent`.
- `pkg/database/source_crawl_result.go`: authoritative operation state,
  unbound owner-continuity proof, `sourceCrawlOwnershipContinuityTx`.
- `pkg/database/source_crawl_runtime.go`: no worker-supplied expected state; the
  separate unbound source-status writer is removed.

Crawler:

- `pkg/crawler/source_crawl_handoff.go`: authoritative validation,
  `decideCrawlTerminalOutcome`, `networkOnlyCrawlResult`,
  `shouldEmitCrawlCompletedEvent`, bounded quiescence helper.
- `pkg/crawler/crawler.go`: network-only failure, timeout shutdown, exactly-once
  finalization, main/network/attribute/metatag/time-series transaction fences,
  fenced `refresh_content` refusal.
- `pkg/crawler/process.go`: quarantine and finalization fields.
- `pkg/crawler/web.go`: cancellation-aware indexing entry points.

Command:

- `cmd/thecrowler/main.go`: fail-closed mode dispatch, pre-VDI validation,
  fenced `null:` quarantine.

Tests and CI:

- `pkg/database/source_crawl_b2r_test.go`
- `pkg/database/source_crawl_b2r_postgres_integration_test.go`
- `pkg/crawler/source_crawl_b2r_test.go`
- `cmd/thecrowler/source_crawl_b2r_test.go`
- `.github/workflows/postgresql-integration.yml` mandatory B2R gate.

## Commands and results

| Command | Result | Log |
| --- | --- | --- |
| `go test ./... -count=1 -timeout 90m -p 1` | PASS | `b2r-full1.log` |
| `go test -race ./pkg/database/... ./pkg/crawler/... ./cmd/thecrowler/... -count=1` | PASS | `b2r-race1.log` |
| `go vet ./...` | PASS | `b2r-vet1.log` |
| `go vet -tags=integration ./pkg/database ./pkg/agent` | PASS | `b2r-veti1.log` |
| `CGO_ENABLED=0 go build ./...` | PASS | local |
| `CGO_ENABLED=1 go build ./...` | PASS | local |
| `gofmt -l pkg cmd services` | PASS (no output) | local |
| `git diff --check` | PASS | local |
| `actionlint .github/workflows/*.yml` | PASS | `b2r-actionlint.log` |

The full suite used `TMPDIR=/dev/shm/opencode`.

## PostgreSQL integration

Disposable database initialized from `postgresql-setup.pgsql`; application
operations used the restricted `crowler` role.

```text
go test -tags=integration ./pkg/database \
  -run '^TestPostgresSourceCrawl(Attempt|Result|B2R)' -count=1 -v
```

Result: **34 PASS, 0 FAIL, 0 SKIP** (`b2r-pg-all.log`). This includes the 22
claim tests, 8 B1 result tests and the 4 new B2R tests:

- `TestPostgresSourceCrawlB2RFenceBoundary` — active before expiry, stale
  exactly at expiry, stale after expiry, stale when the source is disabled.
- `TestPostgresSourceCrawlB2RValidateTxSupersession` — `ValidateTx` accepts the
  current claim under a real transaction and rejects a superseded attempt.
- `TestPostgresSourceCrawlB2RUnboundOwnershipTransfer` — ownership transfer
  before terminal: attempt completed, source stays `processing`, source status
  suppressed.
- `TestPostgresSourceCrawlB2RContinuityThenTransfer` — terminal with proven
  continuity updates source status; a later transfer plus replay is read-only
  and does not rewrite the completed source.

The real scheduler/approval/claim/observation test also passed:

```text
go test -tags=integration ./pkg/agent \
  -run '^TestPostgresFullProposalApprovalSchedulerClaimAndObservation$' -count=1
```

Result: PASS (`b2r-pg-handoff.log`).

## New SQLite and unit coverage

`TestSourceCrawlB2RFenceValidationSQLite`:

- active, just before expiry, exactly at expiry, superseded/terminal history,
  disabled source, deleted source, legacy mode, transaction-held validation,
  terminal handoff refusal.

`TestSourceCrawlB2RUnboundOwnerContinuitySQLite`:

- continuity proven -> source completed;
- ownership transfer before terminal -> attempt completed, source unchanged;
- changed source status not overwritten;
- disabled source -> terminal stale;
- deleted source -> terminal stale and attempt untouched.

`TestSourceCrawlB2ROperationStateCompatibilitySQLite`:

- accepted/processing to completed, failed and cancelled;
- `already_pending` rejected;
- changed binding rejected.

`TestSourceCrawlB2RLateWorkerCannotReplaceTerminalSQLite`:

- failed first, late completed report is a conflict; durable state remains
  failed/failed/error.

`pkg/crawler`:

- terminal outcome decision table; network-only error mapping; bounded
  quiescence; quarantine idempotence; `closeSession` does not return a
  quarantined VDI; pre-start validation blocks a stale claim and admits a
  current one; transaction fence failure propagates; fenced `refresh_content`
  is rejected with no write; completion-event policy table.

Two defects found during the B2R self-review are source-verified but not
exercised by a deterministic concurrent test:

- the crawl worker wrote the shared `err` read by the deferred terminal
  reporter; it now uses a local worker error, removing both the data race and
  the outcome leak;
- non-fatal configuration load/merge errors were assigned to the terminal error
  variable; they are now local and cannot classify a successful crawl as
  failed.

Their definitive validation requires the live VDI/browser run listed under
"Tests not run".

`cmd/thecrowler`:

- present-but-missing policy fails closed and is not treated as legacy;
- absent optional schema keeps the legacy path;
- fenced `null:` deletion is quarantined and reports a terminal failure;
- legacy `null:` deletion still queues the source;
- fenced claim through a real SQLite schema still reaches `completed` with the
  unbound source-status update inside the terminal transaction.

## Tests not run

- Live VDI/WebDriver end-to-end crawl through the fenced path. No VDI image or
  Docker Compose stack was started.
- Live email, plugin, agent, MCP or Ollama execution.
- The full Docker Compose application stack.
- Hosted GitHub CI for this patch. It has not been pushed.
- MySQL fenced mode. MySQL remains legacy-only.
- A shared, staging or production database.

## Evidence classification

All results above are local, on the reviewed base plus the uncommitted patch.
Hosted CI at the base is green but does not exercise this patch. The live
VDI/browser path and production rollout verification remain outstanding, as
required by the work package. The fenced path is a candidate for controlled
integration testing, not a production-ready feature.
