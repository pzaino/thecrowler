# WP-05B2B2 Baseline

## Repository state

| Item | Value |
| --- | --- |
| Repository | `pzaino/thecrowler` |
| Branch | `develop` |
| Reviewed HEAD | `6045b2b8d04237026a4e90eb8da4b90a6c3677f2` |
| Original checkout status at start | clean |
| Go | `go1.27.3 linux/amd64` |
| PostgreSQL used for rehearsal | 17.12 disposable container |
| SQLite | system driver via `mattn/go-sqlite3` |
| Schema | `1.17` |
| Writer generation | `verified-ownership-v1.17` |

The reviewed HEAD is the commit that applies the WP-05B2B1 asynchronous-goal
schema. The original checkout was not used as an edit target. All implementation
work was performed in:

```text
/home/pzaino/tmp/opencode/thecrowler-wp05b2b2
```

No commit, push, branch, tag, pull request or merge was created.

## Documents and source inspected

- `CONTRIBUTING.md`
- `TESTING.md`
- `docs/test_policy.md`
- `pkg/agent/goal_async_state.go`
- `pkg/agent/goal_async_state_test.go`
- `pkg/agent/goal_types.go`, `goal_eval.go`, `goal_evidence.go`
- `pkg/database/database.go`, `transaction.go`, `async_goal_schema.go`
- `pkg/database/sqlite_db.go`, `postgres_db.go`
- `pkg/database/postgresql-setup.pgsql`, `sqlite-setup.sqlite3`
- `docs/crowler-agents/reviews/ASYNC_GOAL_PERSISTENCE_CONTRACT.md`
- `docs/crowler-agents/reviews/ASYNC_GOAL_B2B1_SCHEMA_REVIEW.md`
- `docs/crowler-agents/reviews/ASYNC_GOAL_B2B1_MAINTAINER_DECISIONS.md`
- Existing PostgreSQL MCP integration helpers and fixtures

## Pre-change finding

The reviewed HEAD had a PostgreSQL MCP integration regression:
`applyMCPMutationPostgresSQL` split the setup script with
`strings.Split(scriptText, ";")`. A semicolon in the new asynchronous-goal DDL
comment became an invalid statement. The shared splitter in
`pkg/database/postgres_sqlsplit_test.go` now replaces that naive split and is
covered by regression tests for comments, quoted literals and dollar-quoted
bodies.

## Files added

| File | Purpose |
| --- | --- |
| `pkg/agent/goal_async_store.go` | Internal transactional create/load/transition repository. |
| `pkg/agent/goal_async_store_test.go` | SQLite persistence, lifecycle, replay, concurrency, rollback, corruption, expiry, security and backend-policy tests. |
| `pkg/agent/goal_async_store_postgres_integration_test.go` | Disposable PostgreSQL integration rehearsals. |
| `pkg/database/postgres_sqlsplit_test.go` | Shared PostgreSQL statement splitter and regression tests. |
| `docs/crowler-agents/reviews/ASYNC_GOAL_B2B2_*.md` | This work package's review records. |

## Files modified

| File | Purpose |
| --- | --- |
| `.github/workflows/postgresql-integration.yml` | Adds a dedicated opt-in step for the six PostgreSQL store tests, with RUN/PASS and no-SKIP enforcement. |
| `pkg/database/mcp_mutation_postgres_integration_test.go` | Uses the shared splitter; preserves the full MCP migration test. |
| `pkg/database/async_goal_schema_postgres_integration_test.go` | Uses the shared splitter instead of a private copy. |
| `docs/crowler-agents/reviews/ASYNC_GOAL_PERSISTENCE_CONTRACT.md` | Records the internal store boundary while keeping resume and activation future. |
| `docs/crowler-agents/reviews/ASYNC_GOAL_MAINTAINER_DECISIONS.md` | Separates the completed internal store from pending production activation. |

## Deliberate boundaries

- No schema version change, new table or migration.
- No automatic migration.
- No production construction of the store.
- No worker, scheduler, event consumer, claim, lease, heartbeat, retry or resume.
- No model, crawler, MCP or network execution.
- No public API, manifest, event or configuration change.
- No retention/pruning job.
- No commit, push, deployment or shared database operation.
