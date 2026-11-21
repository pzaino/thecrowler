# WP-05B3C3A Test Report

## Environment

- Repository: `pzaino/thecrowler`
- Branch: `develop`
- Base: `381ca9dd1ce10de710b600cba11191454ba75a69`
- Worktree: `/home/pzaino/tmp/opencode/thecrowler-wp05b3c3a`
- Go: `go1.27.3 linux/amd64`
- Disposable PostgreSQL: `postgres:17` on host port `55503`
- Runtime role: `crowler`; administrative role: `postgres`
- Release: `v2.3.0`
- Schema: `1.17`
- Writer generation: `verified-ownership-v1.17`
- Capability checksum: unchanged `f545b987f0d77fcaac8219deefb70a9c00a354cf90ceef3aca4d3949c830206e`

No shared, staging or production database was touched. No schema object or SQL
migration file changed in this package.

## Baseline CI

The reviewed baseline `381ca9dd` has eight successful GitHub checks, including
`postgres-integration`:

- https://github.com/pzaino/thecrowler/actions/runs/44002101830/job/133768440523

This is baseline evidence only. The WP-05B3C3A patch has not been pushed, so no
hosted run exists for it.

## Implemented repairs

- **Malformed submitted proposals compete for the source.** The candidate query
  no longer filters out rows with null or blank `operation_id`. Every
  `submitted` proposal participates in the ambiguity decision before any
  validation. A single malformed row is `not_attributable`; two or more
  submitted rows are `ambiguous`; pending and terminal proposals do not compete.
- **Replay is lease-aware and attribution-safe.** Replay uses an explicit
  trusted clock in-process and database time for commit reconciliation. A
  replay before lease expiry of the exact current `claimed` attempt returns
  `claimed`; at or after expiry it returns `stale_fence`. Superseded, terminal,
  failed and abandoned attempts are stale. A persisted unbound attempt replays
  as `unattributed`; it is never upgraded to a bound `claimed` result. Replay
  never allocates a new generation or history row.
- **Known idempotency limits are documented.** `SourceCrawlClaimRequest` and
  `ClaimSourceCrawlAttempts` now explain that an empty batch leaves no durable
  acknowledgement and that priority, limits and timing fields are not persisted.
  A pure `sourceCrawlClaimRequestFingerprint` digest exists for a future,
  separately approved schema extension; it is not stored.
- **Full real-path test.** The new PostgreSQL agent integration test creates a
  proposal through the production crawl tool, issues the approval through the
  durable approval store, schedules through
  `ScheduleSourceCrawlRequestForProposal`, claims the source from the runtime
  role, compares the persisted attempt binding with the proposal, approval and
  `Status` rows, and runs the existing goal observer. The observer reports
  `pending`/`accepted`, not completion.
- **Pure handoff/terminal contract.** `pkg/database/source_crawl_handoff.go`
  adds unexported handoff, lease-extension, terminal-receipt, authority and
  replay validators. It has no production caller and performs no database
  access.

## Commands and results

| Command | Result | Log |
| --- | --- | --- |
| `go test ./pkg/database/... -count=1` | PASS | `b3c3a-final3-db.log` |
| `go test ./pkg/agent/... -count=1` | PASS | `b3c3a-final3-agent.log` |
| `go test ./pkg/crawler/... -count=1` | PASS | `b3c3a-final3-crawler.log` |
| `go test ./... -count=1 -timeout 90m -p 1` | PASS | `b3c3a-final3-full.log` |
| `go test -race ./pkg/database/... -count=1` | PASS | `b3c3a-final3-race.log` |
| `go vet ./...` | PASS | `b3c3a-final3-vet.log` |
| `go vet -tags=integration ./pkg/database ./pkg/agent` | PASS | `b3c3a-final3-veti.log` |
| `CGO_ENABLED=0 go build ./...` | PASS | local |
| `CGO_ENABLED=1 go build ./...` | PASS | local |
| `git diff --check` | PASS | local |
| `actionlint .github/workflows/postgresql-integration.yml` | PASS | `b3c3a-final3-actionlint.log` |

The full suite used `TMPDIR=/dev/shm/opencode`.

## PostgreSQL runtime/admin separation

The claim suite runs every query through the restricted runtime role and uses the
administrative connection only for mode changes, malformed fixture inserts,
privilege revocation, terminal-state mutation and cleanup.

`go test -tags=integration ./pkg/database -run '^TestPostgresSourceCrawlAttempt'`
passed **20 tests, 0 failures, 0 skips** (`b3c3a-final3-pg-claims.log`). The new tests
are:

- `TestPostgresSourceCrawlAttemptSubmittedProposalCompetition` (null, blank
  and forged competitors, plus a pending non-competitor)
- `TestPostgresSourceCrawlAttemptReplayClassification` (lease boundaries,
  concurrent replay, attribution classes, terminal and superseded attempts)
- `TestPostgresSourceCrawlAttemptCandidateLoadFailure`
- `TestPostgresSourceCrawlAttemptDatabaseClockReconciliation`

`TestPostgresSourceCrawlAttemptCandidateLoadFailure` revokes `SELECT` on
`CrawlProposals` from the runtime role, proves the claim fails closed with no
attempt row and no source mutation, then restores the grant. The runtime role
was never granted DDL or policy-update authority.

`TestPostgresFullProposalApprovalSchedulerClaimAndObservation` passed on the
disposable database (`b3c3a-final3-pg-handoff.log`).

The complete tagged broad suite passed serially against the same disposable
server (`b3c3a-final3-pg-broad.log`):

```text
ok pkg/database
ok pkg/crawler
ok pkg/agent
ok services/api
```

It includes the asynchronous goal store/observation/eligibility tests, crawl
scheduling and approval tests, MCP mutation tests and schema compatibility.

### Concurrency caveat for local PostgreSQL runs

The fenced/legacy mode is a global policy row. Running the claim suite and the
agent handoff test **in parallel against the same database** races on that row:
one suite's legacy cleanup can be observed by the other suite's next call. This
was observed once during a parallel local run, then the claim suite passed three
consecutive times alone and the handoff test passed alone. The workflow runs the
steps sequentially, and the tests are not parallel within a package. Do not run
these two integration commands concurrently against one database.

## SQLite coverage

`TestSourceCrawlAttemptSQLite...` covers:

- submitted competition with null and blank operation IDs;
- single malformed proposal, pending non-competitor, forged operation ID;
- candidate-load failure with rollback and no attempt/source mutation;
- lease replay at `-1µs`, exactly expiry and `+1µs`;
- bound, unattributed, ambiguous, transferred-owner and `already_pending`
  replay classes with `Reused=true` and no new history;
- terminal and superseded replay;
- concurrent replay;
- database-clock reconciliation;
- the full genuine proposal/approval/scheduler/claim path, including exact
  persisted binding comparison.

## Pure protocol tests

`TestSourceCrawlHandoff...`, `TestSourceCrawlTerminal...` and
`TestSourceCrawlClaimRequestFingerprint` cover:

- bound and unbound handoff construction;
- stale/conflict outcomes rejected;
- source, engine, attempt, generation, claim request, lease and clock errors;
- deterministic operation identity and no fabricated unbound binding;
- bounded lease extension and 24-hour cap;
- bound/unbound terminal result shape;
- wrong operation, run, owner, principal and approval;
- `already_pending` and terminal expected states rejected;
- closed reason vocabulary and oversized reasons;
- observation time, lease expiry and future-time rejection;
- fenced mode, current claim, deleted/disabled source, owner transfer and
  revocation-before-acceptance authority decisions;
- duplicate identical vs conflicting receipt classification;
- closed attempt and operation transition tables;
- canonical request fingerprint determinism and field sensitivity.

## Not run

- Hosted GitHub CI for this patch. It has not been pushed; the exact submitted
  commit remains the required hosted gate.
- Any terminal-result database write, heartbeat writer, live crawler handoff,
  worker, resume transition, model/browser/MCP execution or public API change.
- Any shared, staging or production database migration.
- MySQL claim tests. MySQL remains on its documented legacy path and has no
  attempt tables.
- A live agent/engine Docker stack. This package activates no runtime path.

## Limitations

- Request replay cannot prove equality of priority, limits or the non-lease
  timing intervals because they are not persisted. Full immutable request
  idempotency needs a separately approved additive schema change.
- An empty claim batch has no durable acknowledgement, so the same request ID
  can later claim newly eligible work. Workers must use a fresh 256-bit request
  ID per batch.
- SQLite has no per-role privileges; file permissions and its writer lock remain
  the deployment boundary.
- Context cancellation cannot roll back a transaction that already committed;
  reconciliation is by authoritative identity, not by pretending the commit did
  not happen.
