# WP-05B2B1 Security Review

## Scope and trust boundary

This work adds database objects and a read-only structural check. It does not
add a store, worker, network path, model call, tool dispatch, credential flow or
public API. The relevant trust boundaries are:

- the database administrator who applies the migration or additive script;
- the runtime database role that will later read and write goal rows;
- untrusted goal/evidence data that the future store will validate before
  persistence.

## Data minimization

The goal tables deliberately store references and hashes, not raw content.
There are no columns for:

- prompts, model messages or model text;
- raw tool arguments or results;
- evidence payloads or request/response bodies;
- passwords, bearer tokens, API keys, DSNs, cookies or MCP credentials;
- source configuration blobs.

The forbidden-column list in
`pkg/database/async_goal_schema.go:69` is checked by the readiness function, and
the same list is embedded in both addon guards. A table that gains a column such
as `payload`, `prompt`, `arguments`, `token`, `password`, `dsn` or `cookie` is
reported as unavailable or rejected by the addon.

Evidence rows contain `payload_sha256`, `record_sha256`, `size_bytes`, trust
class, producer, source IDs and timestamps. The raw observation remains in its
trusted source/tool system.

## Identity and replay integrity

- Hashes are constrained to 64 lowercase hexadecimal characters.
- Evidence IDs are constrained to `ev_` plus 32 lowercase hexadecimal
  characters.
- `AgentGoalRunTransitions` uniquely enforces
  `(goal_run_id, operation_id)`.
- `AgentGoalRunWork` uniquely enforces `(goal_run_id, operation_id)` and
  `(goal_run_id, approval_id)`.
- `AgentGoalRunEvidence` uniquely enforces
  `(goal_run_id, record_sha256)`.

The future store must treat the same operation ID with a different transition
hash as a conflict. The database prevents duplicate rows; it cannot compare a
new hash against the stored hash on its own.

## SQL and input handling

The readiness check uses parameterized queries for table, column, constraint
and index values derived from the fixed internal lists. The addon guard values
are static SQL literals generated with the canonical block; no caller-supplied
text is interpolated into DDL.

The SQLite guard uses a temporary CHECK-backed table and trigger rather than
dynamic SQL. The PostgreSQL guard uses a `DO` block with fixed `VALUES` lists.

## Privilege model

PostgreSQL fresh installs and the v1.17 migration transfer ownership of the goal
tables to `:CROWLER_DB_USER` and grant:

| Table | Runtime privileges |
| --- | --- |
| `AgentGoalRuns` | `SELECT, INSERT, UPDATE` |
| `AgentGoalRunTools` | `SELECT, INSERT` |
| `AgentGoalRunSources` | `SELECT, INSERT` |
| `AgentGoalRunWork` | `SELECT, INSERT, UPDATE, DELETE` |
| `AgentGoalRunEvidence` | `SELECT, INSERT` |
| `AgentGoalRunVerificationEvidence` | `SELECT, INSERT` |
| `AgentGoalRunAuditRefs` | `SELECT, INSERT` |
| `AgentGoalRunTransitions` | `SELECT, INSERT` |

The runtime role does not receive `CREATE` on the database or schema. The
same-version addon is therefore an administrator operation: it creates the
objects and transfers ownership. The readiness check verifies the runtime
role's table privileges and fails closed if they are missing.

SQLite privileges are filesystem-enforced; the readiness check verifies
`PRAGMA foreign_keys` and runs `PRAGMA foreign_key_check`.

## Failure handling and rollback

- PostgreSQL DDL is transactional. The addon takes a transaction-scoped
  advisory lock, creates the block, runs the guard and commits. A guard
  exception rolls the whole transaction back.
- SQLite uses `BEGIN IMMEDIATE`; a guard `RAISE(ABORT)` rolls back the
  transaction.
- Reapplying the addon does not delete or rewrite goal rows.
- The addon never updates `DBSchemaVersion` or
  `DatabaseWriterCompatibility`.
- The readiness check is fail-closed: missing columns, indexes, unique sets,
  primary keys, foreign keys, CHECK constraints, forbidden columns or wrong
  markers all produce an error.

## Concurrency

The unique indexes provide the database-level replay and work-identity
guarantees under concurrent inserts. The future store still needs the documented
compare-and-swap revision update and must handle a unique-violation race by
reloading the existing transition and comparing hashes.

No claim, lease, heartbeat or worker concurrency was added in this phase.

## Residual risks

- A database owner can create a same-named table with different CHECK
  expressions or a malicious foreign-key target. The structural check verifies
  names, columns, index shape and constraint presence, not full semantic
  equivalence. The database owner is inside the trust boundary.
- Cross-table kernel invariants (`accepted_at >= goal.created_at`,
  `observed_at <= checkpoint.updated_at`, evidence source scope, sorted unique
  lists) remain the future adapter's responsibility.
- No retention job exists. Operators must not delete parent rows while child
  audit/replay history is still required.
- The addon is manual. An operator can apply it incorrectly; the documented
  procedure requires a backup, a disposable rehearsal and stopped writers.
- No production or GitHub CI security run exists for this patch yet.
