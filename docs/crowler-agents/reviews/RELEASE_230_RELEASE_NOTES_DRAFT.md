# The CROWler 2.3.0 — release notes draft

Status: draft for maintainer review. Nothing in this file publishes, tags or
deploys a release.

Release: `v2.3.0`
Database schema: `1.17`
Writer generation: `verified-ownership-v1.17`

## 1. What 2.3.0 is

2.3.0 is the release in which the CROWler gains a governed agent and tool layer
on top of the existing crawler: a local model provider, acceptance-driven goal
execution, trusted read-only tools, an approval-gated crawl request, read-only
MCP clients, and a record-only MCP mutation proposal workflow. The crawler,
rulesets, plugins, Information Seed discovery, email ingestion, time-series
projection, APIs and search behave as before.

**2.3.0 does not include autonomous asynchronous goal resumption and does not
execute MCP mutations.** Both are deliberately absent, not merely switched off
in configuration.

## 2. Implemented

### Crawler and index integrity

- The shared-artifact cleanup trigger (`handle_shared_entity_deletion()`) now
  runs once per `DELETE` statement instead of once per deleted link. It still
  locks the parent artifact `FOR UPDATE` before re-checking its references — the
  2.3.0 data-integrity fix is unchanged — but it locks each parent once and
  processes parents in ascending id order. Measured effect on a disposable
  PostgreSQL 17 fixture: 2.8× faster vacuum-shaped cleanup, 4.8× faster
  concurrent vacua, and about 25% slower on the opt-in single-link
  `refresh_content` path, which is off by default and already serialised.

- `refresh_content` is now page-scoped and orphan-only in **both** claim modes.
  A refresh replaces the current page's object links and deletes an artifact row
  only when no page still references it. A refresh no longer destroys an object
  that another page shares through the same content hash.
- Refreshing a page that is linked to more than one source is refused with an
  explicit error (`content refresh refused: page is shared with another
  source`) instead of silently rewriting associations another source depends on.
- The refresh replacement runs inside the main indexing transaction, so a
  failure, an expired fenced claim or a cancelled crawl rolls the whole
  replacement back and leaves the previous generation intact.

### Agents and tools

- Local Ollama provider (`provider: ollama`, OpenAI-compatible
  `/v1/chat/completions`), with endpoint normalisation, redirect policy and
  operator-attested tool-capable models through `CROWLER_OLLAMA_TOOL_MODELS`.
- Synchronous, acceptance-driven goals (`AgentGoalRun`): a run succeeds only when
  the deterministic evaluator proves the declared criteria from trusted
  evidence. A model's final sentence cannot mark a goal successful.
- Trusted read-only native tools: `get_source_status`, `search_indexed_pages`,
  `get_source_crawl_status`, `find_scraped_field_metadata`,
  `find_artifact_metadata`.
- Approval-gated native crawl requests (`request_source_crawl`): the model can
  only record a durable proposal; an authenticated operator issues the approval
  and submits it. Submission schedules the source; it does not prove a crawl
  completed.

### MCP

- Read-only MCP over `stdio` and Streamable HTTP with operator-owned inventory,
  exact per-run allowlists, namespaced tool IDs, argument schema validation,
  bounded output, TLS verification, DNS/SSRF policy, redirect rejection and no
  ambient proxy inheritance.
- Record-only MCP mutation proposals: effect classification, canonical argument
  bytes and fingerprints, durable proposals, quotas, operator approval, denial,
  revocation, audit and retention. No dispatcher and no execution backend exist.

### Authentication and operations

- Shared bearer-token authentication and authorization for the API and Events
  services, with users, roles, scopes, grants and token revocation in
  PostgreSQL; Argon2id local passwords.
- Runtime OpenAPI document, WebSocket streams, time-series API, Information Seed
  administration and email-source configuration.
- `dbDeploy` for additive schema-capability deployment with a mandatory backup
  and a non-destructive rollback; SQLite foreign keys are enforced on runtime
  connections and verified at startup.

## 3. Opt-in and experimental

- **Fenced crawl claim mode.** `SourceCrawlClaims` / `SourceCrawlAttempts` /
  `SourceCrawlClaimPolicy` provide lease-based claims, append-only attempt
  history, vertical fencing and fenced terminal receipts. The default mode is
  `legacy`; switching to `fenced` is an explicit operator action
  (`set_source_crawl_claim_mode('fenced')`) after draining legacy work. Not
  activated by default and not required for the crawler to function.
- **`refresh_content`.** Still `false` by default. When enabled it now
  serialises page indexing across the deployment through one advisory lock.
- **Information Seed discovery, time-series emission and aggregation, MCP
  servers, the MCP mutation policy, and agent identity/contract/memory
  enforcement** are all disabled or absent by default.

## 4. Deferred and unavailable

The following are **not** part of 2.3.0 and must not be documented as delivered:

1. Automatic asynchronous goal resumption. The durable checkpoint kernel, the
   transactional store, the read-only crawl-operation observer and the read-only
   resume-eligibility service ship inert. No worker, claim, lease, poller,
   resume loop or crash recovery exists.
2. MCP mutation dispatch, execution and reconciliation. Proposals and approvals
   are records only; `MCPMutationDispatchAttempts` is never written.
3. Ownership-epoch schema, and therefore the unbound fenced source-status update
   and exact-fence `null:` deletion that depend on it.
4. Fenced `null:` deletion. A fenced `null:` source is quarantined and reported
   as a terminal failure; it is not deleted.
5. Production activation of `fenced` claim mode.
6. A durable shared agent registry; agent uploads require a process restart to
   become visible to approval.
7. The read-only execution-profile simulator and any external execution backend.

## 5. Behaviour changes to know about

| Change | Who is affected |
| --- | --- |
| A refresh refuses a page linked to more than one source | Deployments with `refresh_content: true` and overlapping page ownership |
| A refresh no longer deletes content-hash-shared objects for other pages | Deployments with `refresh_content: true`; this is the fix |
| With `refresh_content: true`, page indexing serialises across sources | Deployments with `refresh_content: true`; throughput, not correctness |
| `automatic_events_removal: never` no longer implies permanent events | Events carry a finite `expires_at`; the janitor still deletes expired rows |
| Legacy page-indexing failures keep `Sources.status='completed'` with `last_error` populated | Legacy crawl consumers that read `status` alone |

`null:` remains an intentional pipeline-path test protocol: it claims the
source, skips content collection and browsing, and (legacy only) deletes the
source afterwards. In fenced mode it is quarantined.

## 6. Upgrade and rollback

### Database

Required schema is `1.17`. Upgrade from `1.16` by stopping every writer,
applying `pkg/database/db_migrations/postgresql-migration-v1.17.pgsql`, and then
starting only `1.17`-compatible services. The migration is additive and
idempotent; it does not backfill or bulk-verify historical source ownership.

The additive optional capabilities (`mcp-mutation-authorization-v1`,
`source-crawl-attempt-v1`, the agent-goal tables) are deployed with `dbDeploy`,
which requires an explicit backup, verifies the capability marker, checksum and
object probes, and can disable only the marker while preserving history.

Rollback:

- code: redeploy the previous image; the schema remains compatible because the
  change is additive;
- fenced mode: drain fenced workers, then
  `set_source_crawl_claim_mode('legacy')`;
- `refresh_content`: set it to `false`;
- capability rollback: `dbDeploy -disable` (marker only, history preserved);
  a full restore should use the `pg_dump` backup taken by `dbDeploy`.

**Never** drop the MCP or goal tables as a routine rollback: they hold
proposal, approval and audit history.

### Configuration

New optional sections (`mcp`, agent rollout flags, `information_seed`,
`timeseries`, `email`) default to disabled/empty. Existing `config.yaml` files
remain valid. `selenium` is still accepted; `vdi` is the canonical name.

## 7. Known limitations

- `refresh_content` serialises page indexing when enabled.
- Legacy (non-fenced) refresh failures preserve data but leave
  `Sources.status='completed'` with `last_error` set.
- The SQLite Events janitor uses a PostgreSQL-only `now()` expression and
  therefore deletes nothing on SQLite; SQLite is not a supported Events
  deployment in this release.
- MySQL is legacy-only: fenced claims and durable crawl approvals fail closed.
- Agent uploads are visible to approval only after an API restart; policy and
  agent snapshots are frozen per process.
- Engine startup logs two cosmetic warnings (a ruleset schema filename
  mismatch and a `./plugins/` directory notice).

## 8. Verification posture

Everything in this release note was exercised locally on the frozen commit:
unit and race suites, the static gates, a fresh PostgreSQL 17 install, a
`1.16 → 1.17` migration run twice against a simulated pre-fix database,
backup/restore comparison, the full tagged PostgreSQL integration suite, the
repository's own shipping Dockerfiles, and a real browser acceptance run on an
isolated stack.

Hosted CI ran on the frozen commit and passed all eight checks including
`postgres-integration`. Live Ollama inference, live external MCP endpoints,
MySQL runtime and the opt-in MCP authorization E2E workflow were **not**
executed in this environment; see the test report for the exact status of each.
