# WP-05B2B2 Test Report

## Environment

| Item | Value |
| --- | --- |
| Worktree | `/home/pzaino/tmp/opencode/thecrowler-wp05b2b2` |
| Base | `6045b2b8d04237026a4e90eb8da4b90a6c3677f2` |
| Go | `go1.27.3 linux/amd64` |
| PostgreSQL | 17.12 disposable Docker container |
| SQLite | repository `mattn/go-sqlite3` driver |
| Schema | `1.17` |
| Writer generation | `verified-ownership-v1.17` |

All database tests used disposable SQLite files or a unique database created and
dropped inside the disposable PostgreSQL container. No shared or production
database was touched.

## Repository-wide gates

| Command | Result |
| --- | --- |
| `go test ./pkg/agent/... -count=1` | PASS |
| `go test ./pkg/database/... -count=1` | PASS |
| `go test ./pkg/config/... -count=1` | PASS |
| `go test ./... -count=1` | PASS |
| `go test -race ./pkg/agent -run 'TestAsyncGoal|TestGoalAsyncStore' -count=1` | PASS |
| `go test -race ./pkg/database -run 'TestAsyncGoal|TestSplitPostgresSQLStatements' -count=1` | PASS |
| `go vet ./...` | PASS |
| `go vet -tags=integration ./pkg/agent ./pkg/database` | PASS |
| `CGO_ENABLED=0 go build ./...` | PASS |
| `CGO_ENABLED=1 go build ./...` | PASS |
| `git diff --check` | PASS |

## MCP PostgreSQL regression

The original CI failure was a statement-splitting bug. The shared splitter is
covered by `TestSplitPostgresSQLStatements`; the full MCP test was then run
against a disposable PostgreSQL database:

```bash
CROWLER_MCP_POSTGRES_TEST_DISPOSABLE=1 \
CROWLER_MCP_POSTGRES_DSN='postgres://postgres:...@127.0.0.1:.../crowler_b2b2?sslmode=disable' \
go test -tags=integration ./pkg/database \
  -run '^TestPostgresMCPMutation' -count=1 -v
```

Result: PASS.

- `TestPostgresMCPMutationAuditAndRetention`
- `TestPostgresMCPMutationDeploymentApplyDisable`
- `TestPostgresMCPMutationDurableStore`
- `TestPostgresMCPMutationApprovalAuthorityLockOrder`
- `TestPostgresMCPMutationAdminRoleCascadeContention`
- `TestPostgresMCPMutationBackupRestoreRehearsal`

The previously failing `TestPostgresMCPMutationDurableStore` passes and no test
skipped.

## New store tests

### SQLite

```bash
go test ./pkg/agent -run 'TestGoalAsyncStore' -count=1 -v
```

Result: PASS.

Coverage includes:

- create/load identical checkpoint after closing and reopening the database;
- lifecycle through running, awaiting approval and awaiting external work;
- preserved counters, deadlines and frozen scope after restart;
- exact replay of the latest and older operations;
- different-hash replay conflict and stale-revision conflict;
- two competing writers: one applied, one conflict;
- injected transition-ledger failure: no partial root or work state;
- commit-acknowledgement-loss reconciliation;
- corrupt revision/ledger, last-hash mismatch, contradictory work state,
  malformed evidence and unsupported checkpoint version rejection;
- acceptance before approval expiry with completion observed after approval
  expiry, without extending the goal deadline;
- evidence with empty source IDs;
- no persistence of prompts, credentials, messages or raw evidence payloads;
- create idempotency, incompatible create conflict, malformed create, missing
  run and invalid run ID;
- MySQL typed unsupported result;
- missing schema refusal;
- no execution imports.

### PostgreSQL

```bash
THECROWLER_POSTGRES_INTEGRATION=1 \
CROWLER_ASYNC_GOAL_POSTGRES_TEST_DISPOSABLE=1 \
CROWLER_ASYNC_GOAL_POSTGRES_DSN='postgres://postgres:...@127.0.0.1:.../crowler_b2b2?sslmode=disable' \
go test -tags=integration ./pkg/agent \
  -run 'TestPostgresGoalAsyncStore' -count=1 -v
```

Result: PASS.

- `TestPostgresGoalAsyncStoreRoundTripAfterRestart`
- `TestPostgresGoalAsyncStoreLifecycleAndConcurrency`
- `TestPostgresGoalAsyncStoreReplayOlderTransition`
- `TestPostgresGoalAsyncStoreRejectsCorruptLedger`
- `TestPostgresGoalAsyncStorePreservesAcceptanceAndLateCompletion`
- `TestPostgresGoalAsyncStoreEvidenceAndVerification`

No test skipped. The database helper creates a unique child database, applies
the schema-1.17 marker tables and the asynchronous-goal DDL, runs the test, and
drops only that database. A dedicated
`.github/workflows/postgresql-integration.yml` step now runs the same six tests
with the disposable service credentials and fails on RUN/PASS absence or SKIP.

## Existing compatibility checks

```bash
go test ./pkg/database \
  -run '^(TestAsyncGoalPersistenceReadinessDoesNotAffectStartup|TestNoSchemaVersionAfter117|TestSchemaVersion117LedgerCoverage)$' \
  -count=1 -v
```

Result: PASS.

This confirms ordinary 1.17 startup still accepts a database without the
optional goal tables, while `CheckAsyncGoalPersistenceSchema` refuses that
database, and the schema-version ledger remains frozen at 1.17.

The ordinary Go test workflow also runs `TestSplitPostgresSQLStatements` because
the splitter lives in a non-integration test file.

## Not run

- GitHub Actions CI for the patch. The patch has not been pushed.
- PostgreSQL 15.10. Local PostgreSQL rehearsal used 17.12 only.
- The complete `-tags=integration` repository suite. The MCP six-test suite and
  the six new PostgreSQL store tests were run.
- Information Seed Compose acceptance suite.
- Live Ollama or a real external MCP endpoint.
- Full Docker Compose application stack.
- Any production or shared database migration.
- Any worker, goal resume, scheduler, claim, lease, event consumer, model,
  crawler, tool or MCP execution.

## Evidence classification

All results above are local results on the reviewed base plus this uncommitted
patch. The historical GitHub failure is repaired locally and verified against
PostgreSQL 17.12; hosted CI remains pending until the patch is pushed and the
workflow reruns.

## Patch applicability

The patch is generated from the isolated worktree against
`6045b2b8d04237026a4e90eb8da4b90a6c3677f2` and checked in a fresh clone with
`git apply --check`. The clone applied cleanly, every changed-file checksum
matched the worktree, and `go test ./... -count=1` passed in the applied tree.
The final patch hash is reported in the delivery message because a tracked
report cannot contain its own final hash without changing the patch.
