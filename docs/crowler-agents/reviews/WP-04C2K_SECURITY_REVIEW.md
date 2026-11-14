# WP-04C2K Security Review

## Complete authority chain

Authenticated HTTP user
→ current `Users` account and grants
→ trusted agent manifest identity
→ current operator policy operation allowlist
→ frozen MCP inventory descriptor
→ current policy identity and exact operation
→ current verified source ownership at approval and admission
→ durable inert proposal
→ durable operator approval

Each link is enforced from a trusted source:

| Link | Trusted source | Lower-trust data rejected |
| --- | --- | --- |
| Authenticated user | Auth middleware + `ResolveActiveIdentity` | Request body cannot supply a principal |
| Current account | `Users` row re-read at request entry and admission | Event payload, manifest owner, model output |
| Agent identity | Agent registry loaded at startup | Proposal-supplied agent fields |
| Operation allowlist | Current normalized `MCPMutationPolicy` | Stored proposal snapshot |
| MCP descriptor | Frozen normalized inventory | Model-supplied tool metadata |
| Policy identity | Current API process policy | Proposal `policy_id`/version/hash alone |
| Source ownership | `SourceOwnership` + `Sources` under transaction lock | `allowed_sources`, model arguments, legacy `usr_id` alone |
| Proposal | Durable database store | In-memory fallback |
| Approval | Durable approval store issuing transaction | HTTP request fields alone |

## Cross-tenant isolation

- Source-scoped proposals require verified ownership of every source unless the
  current principal holds an active administrative role.
- Non-admin listing/detail filters by current verified ownership before `LIMIT`.
- An ownership transfer removes the former owner's visibility.
- The live E2E transfers source 42 from user 42 to user 43, then proves the
  former owner cannot approve and cannot see the proposal.
- External-resource proposals remain administrator-only at approval.

## Account suspension and ownership transfer races

- Source-scoped admission follows the transfer lock order: source lock, then
  user lock, then a re-resolve under those locks. It does not take a user share
  lock before the source lock, avoiding the share-to-update upgrade.
- External-resource admission locks the account directly.
- Admission uses `AuthorizeSourceMutationTx`, which locks the `Sources` row and
  reads verified provenance in the same transaction.
- The application's ownership transfer path locks the same `Sources` row before
  updating owner and provenance, serializing transfer against admission.
- A direct out-of-band provenance mutation that bypasses the source lock remains
  a database-integrity assumption.

## Policy consistency across services

- API and Events are separate processes.
- Each freezes its own normalized policy and inventory at startup.
- SIGHUP does not hot-reload MCP authority.
- A policy deployment must restart both processes.
- If API and Events differ in policy ID, version or canonical hash, approval
  fails closed with HTTP 409.
- If the API policy is disabled or unavailable, approval fails closed with HTTP
  503.

The live fixture verifies policy ID mismatch, same-version canonical hash change
and disabled API policy.

## Disabled policy behavior

`buildMCPMutationPolicy` returns a zero policy when `mutation_policy.enabled` is
false. `EvaluateMCPMutationApproval` calls `Validate()`, which rejects a
disabled or empty policy as `ErrMCPMutationPolicyUnavailable`. The handler maps
that to HTTP 503. No stored snapshot is substituted.

## Context cancellation

The HTTP request context is carried through `AgentExecutionContext`, group
budget contexts, tool-loop and goal-run deadlines, action dispatch, retries,
MCP initialization, provider calls, registration and durable admission. The
effective deadline is the earliest trusted deadline. Background event runs keep
independent contexts and cannot manufacture a mutation principal.

## Authorization cache and snapshot lifetime

- MCP policy and inventory are process-frozen once; they are immutable for the
  process lifetime.
- Agent identity is read from the frozen registry at approval time.
- The proposal row is revalidated on every approval attempt.
- Source ownership and account state are re-read from the database at approval
  and admission; they are not cached across a run.
- No model-controlled cache or policy override exists.

## Database outage handling

- Admission authorizer errors, including connection failures, abort the
  admission transaction and insert no proposal.
- Approval store construction and issuance errors fail closed.
- The live E2E stops PostgreSQL, observes a non-success approval response, then
  restarts the database and confirms no approval row exists.
- An auth lookup failure during outage yields HTTP 401/503; neither is treated
  as success.

## Audit integrity and redaction

- Proposal creation and approval issue durable audit rows.
- Denied proposal admission writes `mcp_mutation_admission_denied` with bounded
  reason codes and actor provenance.
- Audit detail is a flat map of stable identifiers, hashes and reason codes; raw
  arguments are not written.
- The E2E asserts no audit detail contains `source_id` or `fixture-resource`
  argument values.
- Audit rows are append-only from the application's perspective; no production
  delete path was added. The disposable E2E deletes its own control rows.

## Retention behavior

The B5 retention contract is unchanged. Approval and proposal history is not
deleted by this work. The E2E deletes one deliberately corrupted proposal only
to keep the disposable fixture's later list calls usable; that is test cleanup,
not a production retention path.

## Repeated proposals and idempotency

- Identical logical proposals replay to the same proposal ID and fingerprint.
- Duplicate approval returns the existing active approval with `reused: true`
  and inserts no second row.
- Replayed proposal admission revalidates current authority before returning the
  existing row.
- Concurrent approval issuance is serialized by the proposal row lock; a test
  confirms exactly one inserted approval and one row.
- Proposal quota and pending limits remain enforced in the admission
  transaction.

## Zero external mutation

- No `tools/call` is issued for a mutation.
- The mutating MCP fixture counter is asserted empty for every positive and
  negative case.
- `MCPMutationDispatchAttempts` has zero rows.
- No proposal enters `dispatching`, `uncertain` or `reconciliation_required`.
- No dispatcher, worker, retry or reconciliation machinery was added.
- Execution backend remains `none`.

## Findings by evidence class

Source-verified:

- all authorization and cancellation code paths above;
- no dispatcher or remote mutation call exists;
- schema version and writer generation are unchanged.

Test-verified:

- all focused unit tests, race tests, PostgreSQL 15/17 integration tests and
  the live E2E matrix;
- zero mutation counter activity and zero dispatch rows;
- fail-closed behavior for changed, disabled, mismatched, expired, corrupt and
  unavailable policy state.

Remaining assumptions and residual risks:

- operators deploy policy changes by restarting both API and Events;
- agent uploads that occur after API startup are not visible to the API approval
  registry until the API is restarted; approval fails closed in the meantime;
- the approval store is the authoritative policy and state-transition boundary,
  while approver identity/source authorization is enforced by the API handler
  before `Issue`; there is one production caller today, but a future in-process
  caller would need to reuse the same checks;
- approval binds the configured transport identity (command or endpoint), not
  every stdio argument, environment value, token-file path or tool schema;
  future dispatch must revalidate the full profile and live schema;
- direct database changes that bypass the application's source-lock protocol
  are not serialized against admission;
- the E2E fixture uses a local fake model and a controlled stdio MCP server, not
  a production model or production MCP endpoint.
