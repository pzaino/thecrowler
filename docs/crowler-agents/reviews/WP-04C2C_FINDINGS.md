# WP-04C2-C Findings

## Baseline and scope

- Local baseline: `cc2461d407678b47bb3de5ed63891400b04c66a9`
  (`develop`, clean).
- Reviewed upstream MCP commit: `3aee6780b7b437d120ad2e8ca08adf878db3a330`.
- The intervening `cc2461d4` commit changes unrelated Information Seed code and
  only documentation wording in the MCP document; no MCP Go code changed
  between `3aee6780` and `cc2461d4`.
- Release `v2.3.0`, schema `1.17` and writer generation
  `verified-ownership-v1.17` remain unchanged.
- No dispatch, execution backend, remote retry or reconciliation worker was
  added. The dispatch-attempt table remains unused.

## Fixed P0/P1 findings

### P0-A — destructive PostgreSQL integration test

**Before:** `pkg/database/mcp_mutation_postgres_integration_test.go` accepted
`CROWLER_MCP_POSTGRES_DSN` and unconditionally ran:

```sql
TRUNCATE MCPMutationAudit, MCPMutationDispatchAttempts,
         MCPMutationApprovals, MCPMutationProposals,
         MCPMutationRunQuota, MCPMutationPrincipalQuota
         RESTART IDENTITY CASCADE
```

**Risk:** a mistyped DSN could delete historical proposals, approvals or audit
records in a real database. An environment-variable name is not an isolation
guarantee.

**Fix:** the integration test now requires both
`CROWLER_MCP_POSTGRES_TEST_DISPOSABLE=1` and a PostgreSQL URL. It creates a
unique database through a maintenance connection, applies the additive
migration there, runs all tests there, and drops only that database in cleanup.
It never truncates or deletes rows in the caller-selected database. A sentinel
table in the maintenance database proves unrelated records are unaffected.
Negative tests cover missing opt-in and missing DSN. `TRUNCATE ... CASCADE` no
longer appears in the test.

**Tests:** `TestMCPPostgresDisposableTargetRequiresExplicitOptIn`,
`TestMCPPostgresDisposableTargetRequiresDSN`,
`TestPostgresMCPMutationDurableStore`.

### P0-B — expired approval reuse and incoherent proposal state

**Before:** `GetActiveMCPMutationApprovalByProposal` selected
`state = 'active'` without checking expiry, remaining execution allowance or
the proposal's live state. `IssueMCPMutationApproval` could return an expired
row as `reused=true`. The partial unique index then blocked a replacement until
an explicit transition. Revocation left the proposal in `approved` with no
usable approval.

**Fix:**

- `GetActive...` now requires a trusted `now`, `expires_at > now`,
  `executions_started < max_executions`, and a live `approved` proposal.
- `Issue...` locks the proposal, classifies a stale active approval as
  `expired`/`exhausted`, audits the transition, and moves an otherwise live
  proposal from `approved` back to `approval_required` before inserting a new
  approval.
- `Revoke...` locks quota rows and the proposal in the same order as issuance,
  revokes only a usable active approval, and reopens the proposal when no
  usable approval remains.
- `Deny...` classifies an expired active approval as `expired` before revoking
  the remaining usable approval, and emits an approval-revoked audit event for
  the active approval it invalidates.
- The API read model never presents `approved` without a currently usable
  active approval; it derives `approval_required` until the next transactional
  reconciliation.
- Approval reuse is idempotent only for the same approver and still-valid
  proposal/hash/identity binding.

**Tests:** `TestMCPMutationExpiredApprovalReapprovalReopensProposal`,
`TestMCPMutationRevocationReopensProposal`,
`TestMCPMutationDenyClassifiesExpiredApproval`,
`TestMCPMutationApprovalAPIExpiredApprovalReapproval`,
plus the existing approval lifecycle and verifier tests.

### P1-C — partially trusted corrupt proposal records

**Before:** `mcpProposalFromDBRow` ignored canonical-argument decode errors and
returned a proposal with `Arguments == nil`. A future dispatch consumer could
receive a partially trusted object.

**Fix:**

- `mcpProposalFromDBRow` now returns `(MCPMutationProposal, error)` and fails
  closed with `ErrMCPMutationProposalCorrupt` on:
  - unsupported identity version;
  - invalid stored hashes/identifiers;
  - invalid/noncanonical canonical arguments (UTF-8, trailing tokens,
    whitespace/order changes, duplicate keys);
  - argument hash mismatch;
  - invalid policy snapshot or policy-hash mismatch;
  - invalid state, lifetime, terminal timestamp/scope consistency;
  - proposal fingerprint mismatch.
- The policy snapshot is decoded into the typed `MCPMutationPolicy` and
  re-marshaled before hashing; this accepts PostgreSQL `JSONB` normalization
  without weakening the hash check.
- `ValidateStoredMCPMutationProposal` is the single API/store validation
  boundary.
- Approval issuance validates the proposal before creating an approval.
- The operator API validates every listed, detailed, denied, revoked or
  approved proposal and returns a bounded `503` for corrupt records.
- Approval rows are validated for hash format, state, approver, lifetime and
  execution allowance.

**Tests:** `TestDatabaseMCPMutationStoreRejectsCorruptProposal` (truncated,
noncanonical whitespace, trailing JSON, duplicate keys, tampered policy),
`TestDatabaseMCPMutationApprovalRejectsCorruptProposal`,
`TestDatabaseMCPMutationStoreAcceptsNormalizedPolicySnapshot`,
`TestMCPMutationApprovalAPIRejectsCorruptProposal`.

### P1-D — tenant-unaware pagination

**Before:** the API applied a global `LIMIT` and then removed unauthorized
proposals in Go. A page could be empty or short while authorized rows existed
further down, and the query scanned other tenants' data.

**Fix:**

- `MCPMutationProposalFilter` now carries `ViewerUserID` and `ViewerAdmin`.
- `ListMCPMutationProposals` applies the visibility predicate before `LIMIT`:
  - administrators see all proposals;
  - non-admin viewers see only source-scoped proposals where every source has
    a `SourceOwnership` row for the viewer;
  - external-resource proposals are administrator-only.
- The predicate is dialect-specific: PostgreSQL `unnest`/`SourceOwnership`,
  SQLite `json_each`/`SourceOwnership`.
- Cursor ordering remains `(created_at DESC, proposal_id DESC)`; the next
  cursor is based on the last database row, not the last filtered item.
- Filter and cursor lengths are bounded; limit is capped at 100.
- Administrators can observe and perform emergency deny/revoke on source-scoped
  proposals after ownership provenance changes; non-admin observation and
  approval still require current ownership. Source ownership revocation removes
  the owner's visibility and action authority.

**Tests:** `TestMCPMutationApprovalAPIListTenantPagination` covers unauthorized
proposals before, within and after the page, external resources, source
ownership revocation and oversized cursors. The Postgres integration test
covers the non-admin SQL predicate.

### P1-E — quota parity and restart behavior

**Before:** SQL admission performed an exact run/principal expiry sweep; the
in-memory store counted expired-but-not-yet-marked records. SQL admission used
the wall clock with no test seam.

**Fix:**

- In-memory `Admit` now performs the same exact run/principal expiry sweep
  before quota counting.
- SQL admission has `AdmitMCPMutationProposalAt` with an explicit clock; the
  existing wrapper uses `time.Now`.
- `DatabaseMCPMutationProposalStore` has an optional `Now` function.
- Counter transitions remain transactionally paired with denial/expiry.
- Tests compare stored counters with live proposal states after denial and
  exact-pair expiry, and verify restart persistence with a file-backed SQLite
  database.

**Tests:** `TestMCPMutationInMemoryExpirySweepReleasesQuota`,
`TestMCPMutationAdmissionReleasesExpiredQuota`,
`TestMCPMutationQuotaConsistencyAfterTransitions`,
`TestMCPMutationSQLiteRestartPreservesProposalAndQuota`.

## Remaining confirmed or suspected issues

| Severity | Finding | Evidence | Disposition |
| --- | --- | --- | --- |
| Blocker | No production authenticated principal is wired into agent tool loops | `pkg/agent/action_tool_loop.go:109` builds `ToolLoopConfig` without `Principal`; `pkg/agent/action_goal_run.go:198` builds `goalRunConfig` without `Principal`; the fields exist at `tool_loop.go:118` and `action_goal_run.go:328` but only tests populate them | Production MCP proposal registration stays disabled; design in `WP-04C2C_AUTHORIZATION_MODEL.md` |
| Blocker | No approved public operator policy for the full `MCPMutationPolicy` contract | `pkg/config/mcp.go:89` has only `AllowMutationProposal`; `pkg/agent/mcp_mutation.go:103` defines the full Go policy with no YAML/JSON loader or schema | Stop for maintainer approval of a new public contract; no field was added |
| High | Native crawl mutation gate is shared with the generic mutation gate | `pkg/agent/action_goal_run.go:202` sets `MutationAllowed` from `crawlRuntime != nil`; `action_tool_loop.go:298` populates `AllowedOperations` from the native crawl policy, not the MCP proposal policy | MCP policy is a separate check, but a production allowlist path for MCP aliases does not exist; document before producer wiring |
| High | SQLite foreign-key declarations are not guaranteed to be enforced at runtime | `pkg/database/sqlite_db.go:56` uses `file:...?cache=shared&mode=rwc` without `_foreign_keys=on`; `sqlite-setup.sqlite3:8` only sets the pragma for the setup connection; `sqlite-mcp-mutation-v1.17.sqlite3` sets it per migration connection | Do not silently change global SQLite behavior; maintainer decision and regression review required |
| Medium | Approval does not revalidate a current mutation policy | `services/api/mcp_mutation.go` approval path validates stored binding and current account/source authority, but no production policy loader exists | Future producer/dispatch decision; dispatch must revalidate current policy |
| Medium | Denied admissions are not audited | `pkg/agent/mcp_mutation.go` proposal success path emits `mcp_mutation_proposal_created`; denial/quota/store failures return without an admission audit event | Documented; add stable reason-code audit before production producer enablement |
| Medium | Persisted proposal state can remain `approved` with an expired approval until the next approval action | `GetActive...` filters expired approvals; `Issue`/`Revoke`/`Deny` reconcile; list/detail derive `approval_required` | Read model is coherent; a maintenance expiry pass remains a maintainer decision |
| Medium | Capability checks do not validate every CHECK constraint | `pkg/database/mcp_mutation.go:459` checks tables, columns, indexes, unique columns, foreign keys, Postgres critical types and marker; SQLite type affinity and all CHECK expressions are not introspected | Documented in schema/API review |
| Low | Dispatch-attempt table and attempts route are inert | No code writes `MCPMutationDispatchAttempts`; handler returns an empty list with `dispatch_enabled:false` | Required for this milestone; zero dispatch attempts |
| Low | Audit retention undefined | `MCPMutationAudit` is append-only with no retention job | Maintainer/operator decision |

## Zero-side-effect invariant

No code in this patch calls a mutating remote MCP `tools/call`, no dispatch
claim is written, and no execution backend is enabled. The integration fixture
counter assertions remain in the existing proposal tests. The new PostgreSQL
test exercises durable proposal/approval rows only.
