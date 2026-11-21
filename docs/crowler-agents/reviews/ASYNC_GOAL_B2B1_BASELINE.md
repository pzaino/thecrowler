# WP-05B2B1 Baseline

## Task

Unreleased database schema 1.17 asynchronous-goal persistence DDL and
compatibility foundation.

## Repository baseline

| Item | Value |
| --- | --- |
| Repository | `github.com/pzaino/thecrowler` |
| Branch | `develop` |
| Required baseline | `c505cf28d380535ec2057497bc8f3f0b42765158` |
| Isolated worktree HEAD | `c505cf28d380535ec2057497bc8f3f0b42765158` |
| Original checkout HEAD | `c505cf28d380535ec2057497bc8f3f0b42765158` |
| Original checkout status | clean before and after the work |
| Go | `go1.27.3 linux/amd64` |
| PostgreSQL used for rehearsal | PostgreSQL 17.12 in a disposable container |
| SQLite used for rehearsal | system `sqlite3` plus the repository's cgo SQLite driver |

The implementation was prepared in the isolated worktree:

```text
/home/pzaino/tmp/opencode/thecrowler-wp05b2b1
```

The original checkout at `/home/pzaino/workspace/github/thecrowler` was not
used for the final edits. No commit, push, branch, tag, pull request or merge
was created.

## Baseline evidence

The specified baseline commit exists and matches `develop` at task start.
`git diff --check` was clean. No pre-change repository test baseline was
captured; the original checkout was clean, so the post-change full suite is
reported against the specified commit rather than against a dirty tree.

## Version-freeze evidence

The database and writer markers remain unchanged:

- `pkg/database/startup_compatibility.go:12` —
  `RequiredSchemaVersion = "1.17"`
- `pkg/database/startup_compatibility.go:14` —
  `WriterGeneration = "verified-ownership-v1.17"`

The fresh PostgreSQL setup still stamps `1.17` at
`pkg/database/postgresql-setup.pgsql:133` and the writer generation at
`pkg/database/postgresql-setup.pgsql:2460`. The fresh SQLite setup stamps
`1.17` at `pkg/database/sqlite-setup.sqlite3:770` and the writer generation at
`pkg/database/sqlite-setup.sqlite3:1058`. The v1.17 migration stamps the same
values at `pkg/database/db_migrations/postgresql-migration-v1.17.pgsql:713`
and `:719`, and at
`pkg/database/db_migrations/sqlite-migration-v1.17.sqlite3:450`.

The MySQL setup and v1.17 migration still advertise `1.17` and
`verified-ownership-v1.17`; neither contains an `AgentGoal` table.

## Changed-file inventory

### Modified

| File | Purpose |
| --- | --- |
| `pkg/database/postgresql-setup.pgsql` | Adds the canonical goal block to fresh PostgreSQL 1.17 installs and grants the runtime role. |
| `pkg/database/sqlite-setup.sqlite3` | Adds the canonical goal block to fresh SQLite 1.17 installs. |
| `pkg/database/db_migrations/postgresql-migration-v1.17.pgsql` | Adds the same block to the v1.16 -> v1.17 PostgreSQL upgrade. |
| `pkg/database/db_migrations/sqlite-migration-v1.17.sqlite3` | Adds the same block to the v1.16 -> v1.17 SQLite upgrade. |
| `pkg/database/db_migrations/README.md` | Documents the 1.17 goal tables, the manual same-version addons and MySQL non-support. |
| `docs/crowler-agents/reviews/ASYNC_GOAL_PERSISTENCE_CONTRACT.md` | Replaces the stale 1.18 proposal with the 1.17 version freeze and the implemented DDL boundary. |
| `docs/crowler-agents/reviews/ASYNC_GOAL_PERSISTENCE_APPROVAL_REQUEST.md` | Separates the decided version freeze from the still-pending store, runtime and deployment approvals. |
| `docs/crowler-agents/reviews/ASYNC_GOAL_MAINTAINER_DECISIONS.md` | Records the version freeze as decided by task direction and keeps the store/runtime phases pending. |

### Added

| File | Lines | Purpose |
| --- | ---: | --- |
| `pkg/database/async_goal_schema.go` | 433 | Read-only, backend-aware structural readiness check and backend support policy. |
| `pkg/database/async_goal_schema_test.go` | 475 | SQLite readiness, addon, migration, drift, version, MySQL and timestamp tests. |
| `pkg/database/async_goal_schema_postgres_integration_test.go` | 492 | PostgreSQL positive, rollback and fail-closed integration rehearsals. |
| `pkg/database/db_migrations/postgresql-agent-goal-v1.17.pgsql` | 802 | Manual same-version PostgreSQL addon with a fail-closed guard. |
| `pkg/database/db_migrations/sqlite-agent-goal-v1.17.sqlite3` | 520 | Manual same-version SQLite addon with a fail-closed guard. |

### Removed

None.

## Explicit non-actions

- No persistent, shared, staging or production database was migrated.
- No database DDL was applied outside disposable databases.
- No Go durable store, CAS adapter, goal worker, claim, lease, heartbeat,
  resume, retry or reconciliation loop was added.
- No public API, Events endpoint, event type, manifest field or example was
  changed.
- No automatic startup migration was added.
- No pruning or retention job was added.
- No asynchronous execution was enabled.
