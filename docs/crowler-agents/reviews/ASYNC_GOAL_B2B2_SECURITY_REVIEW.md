# WP-05B2B2 Security Review

## Scope

The review covers the internal checkpoint store and the shared PostgreSQL
statement splitter. It does not cover a production store activation, resume
engine, worker, scheduler, event consumer, public API or external execution.

## Trust boundary

- The store is explicitly constructed by trusted package-internal code or
  isolated tests.
- Stored checkpoints are historical data, not current authority.
- The store does not derive an account, source ownership, agent policy,
  approval or resume grant from persisted values.
- The caller supplies the pure transition request, including any scheduling or
  resume prerequisite; the existing kernel validates it.
- Model text, event payloads, tool arguments/results and MCP responses have no
  path into the store.

## Data minimization

The store persists only the schema-1.17 goal tables:

- root identity, hashes, state, counters and timestamps;
- frozen tool names and source IDs;
- work references with expiry/acceptance/observation timestamps;
- evidence metadata, payload/record hashes and bounded source IDs;
- verification evidence IDs;
- redacted audit reference IDs;
- transition ledger metadata.

It never persists prompts, model messages, tool arguments, tool results,
evidence payloads, credentials, bearer tokens, DSNs, cookies or source
configuration. A SQLite security test builds a checkpoint from a spec containing
secret prompt/credential fields and stores an evidence record with a raw payload
marker, then scans the database files and confirms none of those values appear.

## SQL handling and injection

- Table and column names are fixed internal identifiers.
- All runtime values are passed as SQL parameters.
- PostgreSQL JSON arguments use a positional `::jsonb` cast; SQLite receives
  the same JSON text as a parameter.
- No caller-supplied table, column, sort or SQL fragment is interpolated.
- The shared statement splitter is test-only migration infrastructure and does
  not parse untrusted runtime SQL.

## Schema and backend enforcement

The store requires `CheckAsyncGoalPersistenceSchema` at construction. It fails
closed for MySQL and other unsupported backends with the existing typed database
error. It has no in-memory persistence fallback.

## Transaction and conflict safety

- Root and child writes plus the transition ledger row commit atomically.
- A root compare-and-swap on `revision` and `state` prevents lost updates.
- Unique `(goal_run_id, operation_id)` prevents duplicate transition identity.
- Exact replays do not write; different hashes are conflicts.
- PostgreSQL uses root row locking; SQLite acquires a writer lock before reads.
- A commit error is reconciled by reading the persisted operation identity.
- A failed child/transition write rolls the whole transaction back; a SQLite
  trigger-injection test proves no partial root or work state survives.

## Load integrity

The loader validates the reconstructed checkpoint through the same pure
validator used by transitions. It also checks transition-ledger count, revision
sequence, state chain, last operation ID and last hash, and rejects contradictory
child/root states. Malformed evidence JSON, unsupported versions and missing
ledger rows fail closed.

The loader does not normalize or repair checkpoint structure. Database timestamp
normalization is limited to timezone/precision canonicalization.

## Concurrency

Tests cover:

- two competing SQLite transitions at one revision: one applied, one conflict;
- two competing PostgreSQL transitions at one revision: one applied, one
  conflict;
- exact replay of the most recent and older operations;
- commit-acknowledgement-loss reconciliation;
- transaction rollback after an injected ledger-insert failure.

No worker, lease or claim mechanism exists, so these guarantees protect
checkpoint writes only; they do not serialize future goal execution.

## Residual risks

- A database owner can rewrite immutable identity, scope or historical ledger
  fields. The store validates internal consistency, not provenance against an
  external signed manifest.
- Intermediate transition hashes are historical audit values. The ledger does
  not store whole historical checkpoints, so the store does not promise
  historical snapshot reconstruction or independent verification of every
  intermediate hash.
- Live account status, source ownership, policy refresh and approval validity
  remain future activation responsibilities. Loading a checkpoint is not an
  authorization decision.
- Direct database edits that bypass the application's root-lock protocol are
  outside the store's serialization guarantees.
- No retention job exists; operators must not delete parent rows while child
  audit/replay history is still required.
- GitHub CI has not yet run this patch.

## No execution proof

`TestGoalAsyncStoreHasNoExecutionImports` checks the store's import block for
`net/http`, `os/exec`, crawler/browser/VDI packages, `services/` and the MCP
client. The store imports only the database abstraction and standard library
packages.
