# Maintainer Decisions

## Completed under authorized work

- Replaced run-label-only outer dind cleanup with exact
  host/name/run/attempt/label/ID ownership.
- Added the `org.crowler.e2e.attempt` label and run-local
  `dind-container.json` metadata.
- Added a fail-closed host context check and a narrow exact-name fallback for
  failure before metadata is recorded.
- Preserved the digest-qualified dind pull/inspect/run identity check, dynamic
  loopback port, `workflow_dispatch`-only trigger and `contents: read`.
- Kept the final cleanup in `always()` and moved artifact upload after cleanup.
- Added 15 fake-Docker cleanup cases, 13 Python workflow/provenance tests and
  mutation checks.
- Added the Python and cleanup regressions to the ordinary `Go` CI job with a
  pinned `PyYAML==6.0.2` and pinned `actionlint@v1.7.7`.
- Re-ran the full Go, race, vet, build, SQLite, VDI, PostgreSQL 17/15.10,
  real-daemon cleanup and isolated E2E matrices.
- Preserved release `v2.3.0`, schema `1.17`, writer generation
  `verified-ownership-v1.17`, default-disabled mutation policy and execution
  backend `none`.

## Pending decisions

### 1. Default-branch registration

The opt-in workflow is absent from protected `main`, and `main` also lacks the
E2E harness, cleanup scripts and MCP mutation packages. Two publication paths
are possible:

- a one-file registration PR to `main`, followed by a manual dispatch on
  `develop` at the exact reviewed SHA;
- a complete release/feature merge to `main`, which is the only way the
  workflow runs self-contained there.

See `E2E_DEFAULT_BRANCH_REGISTRATION.md` for the exact steps and constraints.
No publication, push, merge or dispatch was performed.

### 2. Manual GitHub E2E

After registration, a maintainer must dispatch `MCP authorization E2E
(opt-in)` on the exact applied SHA and record:

- run URL, run ID, attempt, head SHA and timestamps;
- dind image pull and image-ID equality;
- host/nested endpoint provenance;
- safety suite PASS;
- `results.json` counts, completion marker and cleanup status;
- zero dispatch attempts and zero mutation states;
- sanitized artifact ID/URL;
- no surviving host or nested resources.

The gate remains **BLOCKED** until that run exists.

### 3. Option A read-only simulator

No approval exists. The gate remains **BLOCKED**. A valid approval must be a
tracked decision containing a named approver, UTC date, the versioned Option A
contract, the exact bounded scope and explicit confirmation that external
execution remains unapproved.

The single current approval request is
`EXECUTION_PROFILE_OPTION_A_APPROVAL_REQUEST.md`. The design-only contract is in
`EXECUTION_PROFILE_CONTRACT.md`. Neither is an implementation authorization.

### 4. External execution

Still not approved. Do not add a dispatcher, worker, lease, claim, heartbeat,
retry, reconciliation, durable dispatch-attempt write or remote mutating MCP
call.

## Compatibility

- Product release: `v2.3.0`
- Schema: `1.17`
- Writer generation: `verified-ownership-v1.17`
- Execution backend: `none`
- Public API and Events request/response contracts: unchanged
- Native crawl and Information Seed behavior: unchanged
- No new migration or marker

## Rollback

Revert this change. No schema, migration, production data or execution-backend
change is included. The opt-in workflow remains manual-only.

## Next action

The maintainer should choose a publication path, authorize a manual dispatch if
desired, and separately decide the Option A simulator gate. Until then, the
safe phases are complete and the hosted E2E and simulator gates remain
explicitly pending.
