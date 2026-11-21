# WP-05B3C2C Test Report

## Environment

- Repository: `pzaino/thecrowler`
- Branch: `develop`
- Base: `aac103388f444a4ead0109aa7ce8f96a1cb460b6`
- Worktree: `/home/pzaino/tmp/opencode/thecrowler-wp05b3c2c`
- Go: `go1.27.3 linux/amd64`
- Disposable PostgreSQL: `postgres:17` on host port `55503`
- Runtime role: `crowler`; administrative role: `postgres`
- Release: `v2.3.0`
- Schema: `1.17`
- Writer generation: `verified-ownership-v1.17`

No shared, staging or production database was touched.

## Implemented repairs

- Current claims reference exactly one history row through
  `(source_id, generation, attempt_id)`. The split foreign keys were removed.
- PostgreSQL claim and legacy `update_sources` paths take a shared advisory
  policy lock. The operator-only `set_source_crawl_claim_mode` function takes the
  exclusive lock. The runtime role receives SELECT only on the policy table and
  cannot execute the operator function.
- Attribution counts all submitted operation candidates before validation. A
  malformed or missing-approval competitor blocks binding.
- Binding verifies `CrawlOperationKey`, authoritative `Status` rows, strict
  `Status.details` with duplicate-key/trailing-data rejection, approval identity
  and acceptance chronology.
- Missing current claims recover from the retained history high-water mark.
  Corrupt or exhausted generation state fails closed.
- Replay verifies engine and lease duration. Limit, priority and interval
  fields are not persisted; full request-hash replay equality remains a
  documented limitation requiring a schema extension.
- PostgreSQL addon upgrades old development tables without deleting rows. The
  SQLite addon rebuilds both attempt tables transactionally and aborts on an
  inconsistent current-to-history mapping.

## Commands and results

| Command | Result |
| --- | --- |
| `go test ./pkg/database/... -count=1` | PASS (`b3c2c-final-db.log`) |
| `go test ./... -count=1 -timeout 90m -p 1` | PASS (`b3c2c-final-full.log`) |
| `go test -race ./pkg/database/... -count=1` | PASS (`b3c2c-final-race.log`) |
| `go vet ./...` | PASS (`b3c2c-final-vet.log`) |
| `go vet -tags=integration ./pkg/database ./pkg/agent` | PASS (`b3c2c-final-veti.log`) |
| `CGO_ENABLED=0 go build ./...` | PASS |
| `CGO_ENABLED=1 go build ./...` | PASS |
| `git diff --check` | PASS |
| `actionlint .github/workflows/postgresql-integration.yml` | PASS |

The full suite used `TMPDIR=/dev/shm/opencode` to avoid unrelated host
temporary-filesystem quota pressure.

### PostgreSQL claim and cutover tests

Run with the runtime `crowler` connection and the `postgres` administrative
connection against the disposable database. Final run:
`b3c2c-final-pg-claim.log`; complete broad suite:
`b3c2c-final-pg-broad.log`.

- `TestPostgresSourceCrawlAttemptExactHistoryFK` — PASS (insert and update of a mixed attempt/generation row rejected)
- `TestPostgresSourceCrawlAttemptRuntimeCannotChangeMode` — PASS
- `TestPostgresSourceCrawlAttemptPolicyLock` — PASS
- `TestPostgresSourceCrawlAttemptLegacyModeCutoverLock` — PASS
- `TestPostgresSourceCrawlAttemptGenerationRecovery` — PASS
- `TestPostgresSourceCrawlAttemptReplayValidation` — PASS
- `TestPostgresSourceCrawlAttemptAttributionFailClosed` — PASS
- `TestPostgresSourceCrawlAttemptForgedAndDuplicateDetails` — PASS
- `TestPostgresSourceCrawlAttemptWrongChronology` — PASS
- `TestPostgresSourceCrawlAttemptGenuineSchedulerBinding` — PASS
- `TestPostgresSourceCrawlAttemptRollback` — PASS
- `TestPostgresSourceCrawlAttemptSchemaAndFencedCutover` — PASS
- `TestPostgresSourceCrawlAttemptBindingAndRejections` — PASS
- `TestPostgresSourceCrawlAttemptConcurrentClaimers` — PASS
- `TestPostgresSourceCrawlAttemptAddonRepeatable` — PASS
- `TestPostgresSourceCrawlAttemptMySQLAndLegacyStartupCompatibility` — PASS

The rollback test creates its failure trigger through the administrative
connection and executes the claim through the runtime role. The addon test
applies the addon through the administrative connection and then verifies
runtime readiness, claiming, preserved rows and preservation of `fenced`.

### SQLite tests

`go test ./pkg/database -run '^TestSourceCrawlAttempt' -count=1` passes,
including fresh install, v1.16 -> v1.17 migration, repeatable addon,
incompatible addon rejection, exact history FK rejection, generation recovery,
replay conflicts, duplicate details key rejection, genuine scheduler binding,
rollback and startup compatibility.

### Schema paths

- Fresh PostgreSQL setup: applied, claim tests PASS.
- PostgreSQL v1.16 -> v1.17 migration: applied after removing attempt objects,
  claim tests PASS.
- PostgreSQL same-version addon: applied twice, rows and `fenced` preserved.
- SQLite fresh setup, v1.16 -> v1.17 migration and addon rebuild: PASS.
- A SQLite database created from the reviewed Stage B baseline schema (old
  split foreign keys) was upgraded with the new addon: one current claim and
  one history row were preserved, `fenced` was preserved, and
  `PRAGMA foreign_key_check` was clean.
- Fresh PostgreSQL setup (`crowler_fresh4`) and v1.16 -> v1.17 migration
  (`crowler_mig4`) passed the full claim test suite after final SQL changes.
- MySQL remains legacy-only; no attempt tables.

## CI status

The permission failures are reproduced with the old test code under the runtime
role (`42501` for trigger creation and addon DDL) and are fixed by using the
administrative connection for those operations.

The reported agent failure in
`TestPostgresCrawlProposalApprovalSubmissionChain` was not reproduced locally.
The exact broad PostgreSQL suite (`pkg/database`, `pkg/crawler`, `pkg/agent`,
`services/api`) passed locally after the claim-test repairs, and the agent test
also passed when run alone and after the full database package. One early broad
run hit an unrelated disposable-database cleanup timeout in the MCP suite;
the MCP suite passed immediately on rerun and the complete broad suite passed
on the next run. No production code was changed to hide either failure.

**Hosted GitHub CI for this patch has not run.** The patch has not been pushed;
the hosted PostgreSQL job on the exact submitted commit is still the required
gate. Local results are not hosted evidence.

## Limitations

- The direct database UPDATE path for `SourceCrawlClaimPolicy` bypasses the
  operator function and advisory lock. It is unsupported; operators must call
  `set_source_crawl_claim_mode`.
- Replay cannot prove equality of limit, priority or timing fields because they
  are not persisted. A full immutable request hash would require a schema
  extension and separate approval.
- SQLite has no per-role table privileges; its writer lock and file permissions
  provide the deployment boundary.
- No crawler, worker, terminal result, resume, model, browser, MCP or public API
  path is activated by this package.
