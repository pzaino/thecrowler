# WP-04C2-C Schema 1.17 and Operator API Review

**Deployment status: not approved.** The objects and routes exist in the
repository, but this review does not reclassify them as approved for a shared or
production database. No migration was applied outside disposable test
databases.

## 1. Schema inventory

Additive capability files:

- `pkg/database/db_migrations/postgresql-mcp-mutation-v1.17.pgsql`
- `pkg/database/db_migrations/sqlite-mcp-mutation-v1.17.sqlite3`
- Matching blocks in `pkg/database/postgresql-setup.pgsql` and
  `pkg/database/sqlite-setup.sqlite3`.

Objects:

| Object | Purpose |
| --- | --- |
| `MCPMutationProposals` | Immutable proposal identity, canonical bytes, scope, policy snapshot, state, expiry |
| `MCPMutationApprovals` | Append-only approval history, approver identity, allowance, revocation |
| `MCPMutationDispatchAttempts` | Reserved table; **unused** in this milestone |
| `MCPMutationAudit` | Append-only bounded/redacted transition trail |
| `MCPMutationRunQuota` | Per-run active counter |
| `MCPMutationPrincipalQuota` | Per-principal pending counter |
| `SchemaCapabilities` | Additive capability marker within schema 1.17 |

`RequiredSchemaVersion` remains `1.17`; the writer generation remains
`verified-ownership-v1.17`. The capability marker uses
`mcp-mutation-authorization-v1` and checksum
`00cb16b3efc2a3a619335c4e3bc2b1c470bb34b2378d249a81e746cddc704eed`.

## 2. PostgreSQL/SQLite parity

| Concern | PostgreSQL | SQLite | Review |
| --- | --- | --- | --- |
| Canonical argument bytes | `BYTEA` | `BLOB` | Exact byte preservation on both |
| Policy snapshot | `JSONB` | `TEXT` + `json_valid` | PostgreSQL normalizes key order; typed policy validation re-marshals and checks the hash |
| Source scope | `BIGINT[]` | JSON array text + `json_array_length` | Application scope checks; DB check requires one of source/external scope |
| Timestamps | `TIMESTAMPTZ` | UTC `TIMESTAMP` text | UTC on both; SQLite checks use `julianday` |
| Proposal ID/hash | `CHAR(64)` + regex checks | `CHAR(64)` + length checks | Postgres regex is stricter; application validates hashes |
| Logical-call uniqueness | `uq_mcp_proposal_logical_call` | `UNIQUE (run_id,effective_call_id)` | Exact unique-column probe added |
| Active approval uniqueness | Partial unique index | Partial unique index | Both checked by capability probe |
| Unresolved attempt uniqueness | Partial unique index | Partial unique index | Table unused; index checked |
| Foreign keys | Declared and enforced | Declared; **runtime enforcement depends on `PRAGMA foreign_keys`** | See finding below |
| Execution allowance | Integer checks | Integer checks | Equivalent |
| State/terminal checks | CHECK constraints | CHECK constraints | Equivalent intent |

### SQLite foreign-key enforcement finding

The SQLite schema declares foreign keys, and the migration/setup scripts set
`PRAGMA foreign_keys = ON` on their own connection. The application's
`SQLiteHandler.Connect` connection string is:

```go
fmt.Sprintf("file:%s?cache=shared&mode=rwc", c.Database.DBName)
```

at `pkg/database/sqlite_db.go:56`. It does not pass `_foreign_keys=on` (or
`_fk=1`), and `database/sql` pools multiple connections. Declared SQLite
foreign keys are therefore not guaranteed to be enforced by every runtime
connection. This review does not change global SQLite behavior because that
could expose existing schema/data violations. The maintainer must choose:

1. enable `_foreign_keys=on` in the SQLite DSN and run full regression tests;
2. enforce these MCP relationships in application transactions only and
   document the divergence; or
3. keep the current behavior with an explicit operational caveat.

PostgreSQL foreign keys are enforced by the server.

## 3. Capability detection

`CheckMCPMutationCapability` (`pkg/database/mcp_mutation.go:459`) checks:

- required tables and columns;
- named indexes, including partial unique indexes;
- primary keys on every MCP mutation table;
- exact unique column sets for the logical call, proposal fingerprint and
  attempt submission;
- foreign keys on approvals and dispatch attempts;
- critical PostgreSQL types (`bytea`, `jsonb`, array, timestamps, integers);
- the capability marker and checksum.

Failure is closed with `ErrMCPMutationSchemaUnavailable`; durable stores/routes
return `503`.

Tests:

- SQLite: missing table, missing column, missing index, stale marker,
  exact-unique-column probe, schema 1.17 without capability.
- PostgreSQL disposable database: missing index, dropped logical-call
  constraint, stale marker, wrong `canonical_arguments` type, clean-install
  block, repeated migration.
- The marker upsert cannot by itself certify a modified schema; the object
  probes above are the substantive guard. Remaining limitation: not every CHECK
  expression or SQLite type affinity is introspected.

## 4. Migration and clean-install testing

Verified in disposable environments:

- SQLite clean-install setup executes; v1.17 migration converges and preserves
  history; the MCP migration is repeatable.
- PostgreSQL: a unique database is created by the integration test; the
  additive migration is applied twice; the clean-install MCP block is applied
  to the same fresh database; capability detection passes after each.
- Partial/failed state: dropping an index, unique constraint, column, table or
  changing the marker makes capability detection fail closed.
- A 1.17 database without the capability fails closed when the feature is used.
- The global startup compatibility check remains unchanged, so read-only
  services can start; MCP mutation routes/stores return `503` until the
  capability is present.

Operational action: apply the additive script explicitly after review, or use
the clean-install setup for a fresh database. There is no runtime DDL, no
automatic destructive repair, and no schema version bump. Dropping the
capability marker disables the feature without deleting historical rows; a
rollback that drops the tables would destroy proposal/approval/audit history and
is not recommended without a backup and maintainer decision.

## 5. Operator API review

Routes registered in `services/api/main.go`:

```text
GET  /v1/console/mcp/mutations/proposals
GET  /v1/console/mcp/mutations/proposals/{proposal_id}
POST /v1/console/mcp/mutations/proposals/{proposal_id}/approve
POST /v1/console/mcp/mutations/proposals/{proposal_id}/deny
POST /v1/console/mcp/mutations/proposals/{proposal_id}/revoke
GET  /v1/console/mcp/mutations/proposals/{proposal_id}/attempts
```

There is no propose, dispatch, execute or reconciliation route.

Verified controls:

- `withAuthenticatedPublicMiddlewares` applies auth, CIDR filtering, CORS,
  security headers, keep-alive and rate limiting.
- `auth.ResolveActiveIdentity` reloads the current account, roles and scopes
  and rejects disabled/deleted users.
- Approval/denial/revocation require current administrator authority or
  verified ownership of every proposal source; external-resource proposals are
  administrator-only.
- The proposal's originating principal is re-resolved and must still be active
  and source-authorized for approval actions.
- Approval requires exact `proposal_sha256` and `identity_version`; execution is
  capped at one; expiry is clamped to the proposal lifetime.
- Request bodies are capped at 16 KiB; reason text is sanitized/truncated;
  list filters and cursor length are capped; list limit is capped at 100.
- Non-admin list visibility is applied in SQL before `LIMIT`; external-resource
  proposals are administrator-only; cross-tenant rows are not fetched.
- Detail/attempts return not-found for unauthorized rows; list omits them.
- Error statuses are bounded: 400/401/403/404/409/410/429/503; corrupted stored
  proposals return a generic `503`.
- Responses expose argument keys and hashes, not raw argument values; external
  resource userinfo/query/fragment is redacted.
- Denial invalidates an active approval in the same transaction.
- Attempts always returns an empty list with `dispatch_enabled:false`.

Remaining API findings:

- No current-policy revalidation exists because no production mutation-policy
  loader is wired into the API. Approval is a stored-binding and current
  account/source check, not dispatch authorization.
- A persisted `approved` proposal whose approval has expired is presented as
  `approval_required` by the read model; the database transition is applied on
  the next approval/revocation/denial action. A maintenance expiry pass remains
  a decision.
- Rate limiting is the existing API-wide middleware, not a separate MCP
  per-user budget.
- Authentication is bearer-token based; cookie/session CSRF behavior follows the
  existing API auth mode.
- Denied-admission audit events are not yet emitted.
- Audit retention is operator policy, not implemented.

## 6. Deployment conclusion

The additive capability and routes are implemented but not deployment-approved.
Before any shared/production migration or operator use:

1. approve the capability/migration route and clean-install strategy;
2. decide the SQLite foreign-key enforcement issue;
3. approve the operator policy/public configuration contract and trusted
   principal provenance;
4. approve audit retention, denied-admission audit and the read-model expiry
   policy;
5. keep dispatch and remote execution disabled.
