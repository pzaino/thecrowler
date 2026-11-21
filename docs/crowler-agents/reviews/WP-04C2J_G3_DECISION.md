# WP-04C2J G3 Decision

## Status

**APPROVED — G3 only.**

## Approval record

- Approver: repository maintainer (user) in the OpenCode session.
- Date: 2026-11-14 (UTC).
- Exact instruction: `OK, B5 is applied and approved, so please complete with
  G3. Remember to keep the DB schema at release 1.17, so if any DB schema
  changes are required update schema 1.17 (do not add a new version).`
- Baseline HEAD: `479803aa9c74e6b5f0c09ced881a7191c6574afd` (B5 applied).
- Scope: schema-1.17 MCP capability deployment/rollback and SQLite foreign-key
  enforcement.

## Decisions taken

### 1. SQLite foreign keys

Enable `_foreign_keys=on` on every SQLite runtime pooled connection and fail
closed at startup when foreign keys are disabled or when
`PRAGMA foreign_key_check` reports existing orphan rows. Do not silently repair
or delete existing data. Run the full regression suite and a live SQLite
deployment/check test.

### 2. Schema-1.17 capability deployment

Add an operator command, `dbDeploy`, that:

- verifies the base schema is already `1.17`;
- requires an explicit backup before deployment;
- applies the embedded additive MCP capability migration idempotently;
- verifies the capability marker/checksum, table/index/foreign-key probes and
  application-user privileges;
- disables only the capability marker on rollback, preserving all proposal,
  approval, dispatch and audit history.

No new schema version is introduced. `RequiredSchemaVersion` remains `1.17` and
`WriterGeneration` remains `verified-ownership-v1.17`.

### 3. Production build

Enable the CGO SQLite driver in production binaries so the runtime and
`dbDeploy` can actually open SQLite databases. The three production Dockerfiles
set `CGO_ENABLED=1` and install `gcc musl-dev`; `autobuild.sh` honors the
caller's CGO setting so the static cross-build workflow remains CGO=0. Include
`dbDeploy` in the Events, API and Engine images, and include
`postgresql-client` for PostgreSQL backups. CGO-disabled builds remain
compilable but do not include SQLite support.

## Not approved by G3

Schema version `1.18`, table-dropping rollback, automatic destructive repair of
existing foreign-key violations, dispatch, external mutation, retries,
reconciliation, shared/production database migration by this test run, and any
change to the B2/B3/B4/B5 authority contracts.
