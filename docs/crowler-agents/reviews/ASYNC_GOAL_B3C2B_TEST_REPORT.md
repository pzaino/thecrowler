> **Superseded by WP-05B3C2C.** The capability checksum, exact history
> reference, policy locking and PostgreSQL test harness described here were
> hardened later; see `ASYNC_GOAL_B3C2C_TEST_REPORT.md`.

# WP-05B3C2 Stage B Test Report

## Environment

- Repository: `pzaino/thecrowler`
- Branch: `develop`
- Base: `16d7f06068c6fcc65a75d691d687bc61f7770e9a`
- Worktree: `/home/pzaino/tmp/opencode/thecrowler-wp05b3c2b`
- Go: `go1.27.3 linux/amd64`
- Disposable PostgreSQL: `postgres:17` on host port `55501`
- Release: `v2.3.0`
- Schema: `1.17`
- Writer generation: `verified-ownership-v1.17`

No shared, staging or production database was touched. The disposable
PostgreSQL container was removed after the runs.

## Implemented behavior

- Additive 1.17 `SourceCrawlClaims`, `SourceCrawlAttempts` and
  `SourceCrawlClaimPolicy` in PostgreSQL/SQLite fresh installs and the
  v1.16 -> v1.17 migrations.
- Repeatable same-version development addons with fail-closed structural
  guards.
- `source-crawl-attempt-v1` capability marker checksum
  `abb79c543a11ed355c6e76d42c235261b024bef01a9b507a5bf0263db89248bd`.
- Read-only `CheckSourceCrawlAttemptSchema`, not wired to ordinary startup.
- Internal `ClaimSourceCrawlAttempts` with trusted claim-request identity,
  monotonic per-source generation, append-only history, operation binding and
  commit-outcome reconciliation.
- PostgreSQL `update_sources(...)` keeps its seven arguments and eight result
  columns, and returns zero rows while the policy mode is `fenced`.

## Commands and results

| Command | Result | Log |
| --- | --- | --- |
| `go test ./pkg/database/... -count=1` | PASS | `wp05b3c2b-db-final.log` |
| `go test ./pkg/agent/... -count=1` | PASS | `wp05b3c2b-agent-final.log` |
| `go test ./pkg/auth/... -count=1` | PASS | `wp05b3c2b-auth-final.log` |
| `go test ./... -count=1` | PASS | `wp05b3c2b-full7.log` |
| `go test -race ./pkg/database/... -count=1` | PASS | `wp05b3c2b-race-final.log` |
| `go vet ./...` | PASS | `wp05b3c2b-vet-final.log` |
| `go vet -tags=integration ./pkg/database ./pkg/agent` | PASS | `wp05b3c2b-veti-final.log` |
| `CGO_ENABLED=0 go build ./...` | PASS | local |
| `CGO_ENABLED=1 go build ./...` | PASS | local |
| `git diff --check` | PASS | local |

### PostgreSQL tagged tests

Against the disposable database initialized from `postgresql-setup.pgsql`:

- `TestPostgresSourceCrawlAttemptRollback` — PASS
- `TestPostgresSourceCrawlAttemptSchemaAndFencedCutover` — PASS
- `TestPostgresSourceCrawlAttemptBindingAndRejections` — PASS
- `TestPostgresSourceCrawlAttemptConcurrentClaimers` — PASS
- `TestPostgresSourceCrawlAttemptAddonRepeatable` — PASS
- `TestPostgresSourceCrawlAttemptMySQLAndLegacyStartupCompatibility` — PASS

Log: `wp05b3c2b-pg-final.log`. Nine tests PASS, no SKIP.

The binding test proves a single verified `submitted` + `accepted` operation is
bound, the previous attempt is superseded before rebinding, and a transferred
source becomes `not_attributable` rather than bound. The ambiguity test in the
SQLite suite proves two accepted candidates produce `ambiguous` with no binding.

The concurrency test starts two claimers with independent claim request IDs
on separate PostgreSQL pools against one source. Exactly one claim owns the
source and exactly one attempt row is stored. The fenced-cutover test first
proves an unexpired lease blocks a second claim, then expires the lease and
proves the next generation supersedes the previous attempt. The rollback test
injects a PostgreSQL trigger failure and proves the source status, current
claim and attempt history are unchanged.

The addon test applies the PostgreSQL addon twice after a real claim, preserves
the `fenced` mode and attempt rows, and verifies the schema/writer markers stay
`1.17`/`verified-ownership-v1.17`.

Existing PostgreSQL suites were also run against the same disposable database:

- `TestPostgresGoalAsync*` — PASS (15 tests)
- `TestPostgresMCPMutation*` — PASS (6 tests)
- `TestPostgresV117RuntimeCompatibility` — PASS
- `TestPostgresCrawlSchedulingFoundation` — PASS
- `TestPostgresLegacyScheduleSourceCrawl` — PASS
- `TestPostgresV117RuntimeCompatibility` — PASS
- `TestPostgresCrawlApprovalPersistenceAndScheduling` — PASS
- `TestPostgresDefaultGoalEvidenceProviderSourceLookup` — PASS
- `TestPostgresReadOnlyMetadataTools` — PASS

## SQLite coverage

- Fresh-install readiness.
- Legacy mode returns `ErrSourceCrawlClaimLegacyMode`.
- Claim, idempotent replay and generation increment.
- Operation binding and rebinding.
- Ambiguous candidates, `already_pending`, transferred ownership and disabled
  sources.
- Injected failure rolls back the source, current claim and history.
- Missing optional storage fails the opt-in API closed and leaves the source
  untouched; ordinary startup compatibility still passes.
- Missing/corrupt capability marker and missing table are rejected.
- v1.16 -> v1.17 migration recreates the attempt objects and keeps the markers.
- Addon is repeatable, preserves `fenced` mode and existing rows, and rejects an
  incompatible pre-existing table.

## Not run

- Hosted GitHub CI for this patch. The reviewed WP-05B3C1 baseline had eight
  successful checks, but this patch has not been pushed.
- A live crawler/worker, VDI, browser, model, agent or MCP mutation call.
- A production or shared database migration.
- A direct PostgreSQL replay of the full v1.16 -> v1.17 base migration as an
  automated test. The v1.17 migration was applied manually to the disposable
  database and converged to `1.17`; the SQLite migration path is automated.
- An explicit PostgreSQL guard-rejection test for an incompatible same-named
  table. The equivalent SQLite guard test passes; the PostgreSQL addon guard is
  exercised by the repeated addon application.

## Evidence limits

Local tests prove the tested properties only. They do not prove hosted CI,
production concurrency, or future crawler handoff correctness. Commit-outcome
uncertainty reconciliation exists in code and is not directly induced in the
PostgreSQL test. WP-05B3C3 remains responsible for the authenticated attempt
handoff and the fenced terminal result.

## Patch applicability

The patch is generated from the isolated worktree against
`16d7f06068c6fcc65a75d691d687bc61f7770e9a` with
`git diff --binary --full-index --no-ext-diff`. A fresh clone passed
`git apply --check` and applied cleanly; every changed file's SHA-256 matched
the worktree.

The applied tree passed `go test ./... -count=1 -timeout 90m -p 1`
(`wp05b3c2b-patch-full.log`), `go vet ./...`, the integration-tagged vet, both
CGO builds, the SQLite claim tests and the disposable PostgreSQL claim tests.
The final patch SHA-256 is reported in the delivery message because a report
inside the patch cannot contain its own final hash.
