# Release 2.3.0 go / no-go

Work package: `WP-REL230-RC1`
Decision owner: the repository maintainer
Prepared: 2026-11-20
**This document does not tag, merge, publish or deploy anything.**

## 1. Gate result

# **GO WITH EXPLICIT DEFERRALS — maintainer decision required**

2.3.0 can proceed to the maintainer-controlled pre-release procedure **only** with
the narrowed, truthful scope in section 3 and **only** with the deferrals in
section 4 accepted and recorded. It must not be presented as complete WP-00..WP-07
agentic acceptance.

If the maintainer's release promise is "the full original agentic plan", the
answer is instead **NO-GO**, because automatic asynchronous resumption and MCP
mutation execution do not exist in this tree. That is a scope decision, not a
defect, and it is not this review's call.

Fenced mode must **not** be enabled in a production deployment of 2.3.0. See
B-01.

## 2. Tested artefact and environment

| Item | Value |
| --- | --- |
| Repository | `pzaino/thecrowler` |
| Branch | `develop` |
| Commit | `9e719acce04d3606c46821e33b4f8dc23baa6b36` |
| Commit subject | `improved some policies` |
| Previous reviewed baseline | `be5572de9064c4e4020d5b8f3adf478de4952aad` |
| Release | `v2.3.0` (`RELEASE_VERSION.txt`) |
| Schema | `1.17` |
| Writer generation | `verified-ownership-v1.17` |
| Working tree at start | clean |
| Isolated worktree | `/tmp/opencode/thecrowler-rel230` |
| Original checkout | clean before and after; not used as an edit target |
| Go | `go1.27.4` (`go.mod` 1.26.6) |
| Docker | `29.9.2-ce` / Compose `5.7.1` |
| PostgreSQL | 17.12, disposable container `rel230-pg` |
| VDI | `zfpsystems/crowler-vdi:latest` digest `sha256:d834e1f0…` |
| Compose project | `rel230rc1` (isolated, removed after the run) |

## 3. What is ready, and in which mode

### Ready and default

- Legacy crawler, indexing, `SearchIndex`/`SourceSearchIndex`/`WebObjects`,
  source lifecycle and the `crawl_completed` event (`create_event_when_done`).
- `crowler-agt` manifest lint / strict validate / convert; 14 shipped examples
  validated (28 checks).
- Schema 1.17 fresh install, the v1.16→v1.17 upgrade path, the same-version
  addons, backup and restore.
- API, Events, engine and VDI containers reach health and readiness and talk to
  a fresh PostgreSQL 17 database.

### Ready, opt-in

- **Local Ollama provider** (`provider: ollama`, OpenAI-compatible
  `/v1/chat/completions`, tool-capability attestation through
  `CROWLER_OLLAMA_TOOL_MODELS`). Code and unit/mock coverage only in this gate.
- **Synchronous acceptance-driven goals** (`AgentGoalRun`): the verifier decides
  completion; a model claiming success without evidence cannot pass.
- **Trusted read-only native tools**: `get_source_status`,
  `search_indexed_pages`, `get_source_crawl_status`,
  `find_scraped_field_metadata`, `find_artifact_metadata`.
- **Approval-gated native crawl scheduling** (`request_source_crawl`): durable
  proposal, operator approval, operator submission. "Scheduled" is not "crawled".
- **Read-only MCP** over `stdio` and `streamable_http`, with operator-owned
  inventory, explicit per-run allowlist, namespacing, argument validation,
  bounded output and no permission from server advertisements.
- **Fenced crawl claim foundation** (`SourceCrawlClaims`, `SourceCrawlAttempts`,
  `SourceCrawlClaimPolicy`): claim, heartbeat, terminal receipt,
  `legacy`/`fenced` operator switch, `source-crawl-attempt-v1` capability.
- **Record-only MCP mutation proposals**: durable proposal, canonical arguments,
  quota, approval/denial/revocation, audit and retention. No execution.

### Experimental / disabled by contract

- **MCP mutation dispatch.** No dispatcher, no dispatch claim, no execution
  backend, no reconciliation. `MCPMutationDispatchAttempts` is empty and
  `execution_backend` is `none`.
- **Automatic asynchronous goal resumption.** The checkpoint kernel, the
  transactional store, the read-only operation observer and the read-only resume
  eligibility service exist. No worker, lease, claim, poller, event consumer,
  crash recovery or cancellation finalization exists.
- **Fenced `refresh_content`.** Implemented and unit/integration tested, but see
  B-01. `refresh_content` defaults to `false`.
- **Ownership epoch (B3 Decision 1).** Approved as a semantic model only; no DDL.
- **Fenced `null:` deletion (B3 Decision 3).** Quarantined by design.

### Absent

- Asynchronous goal resume worker and its operational surface.
- MCP mutation execution, dispatch and reconciliation.
- Retention/pruning for the schema-1.17 goal tables.
- A durable, shared agent registry usable across API instances without restart.

## 4. Explicit deferrals the maintainer must accept

If these are accepted and recorded, 2.3.0's public promise is:

> local Ollama + synchronous acceptance-driven goals + trusted read-only native
> tools + approval-gated native crawl scheduling + read-only MCP + record-only MCP
> mutation proposals + an operator-gated fenced crawl foundation.

Deferred, and **not** part of 2.3.0:

1. Automatic asynchronous goal resumption.
2. MCP mutation dispatch, execution and reconciliation.
3. Ownership-epoch schema and the unbound source-status/`null:` enablement that
   depends on it.
4. Fenced `null:` deletion.
5. Production activation of `fenced` claim mode.
6. Production use of `refresh_content` until B-01/B-02 are decided.

If the maintainer rejects any deferral, the corresponding work must be completed
under a separate approved package before release; the gate then becomes NO-GO for
2.3.0 as scoped.

## 5. Blockers, ranked

### B-01 — P0 for `refresh_content`: pre-existing orphan trigger can silently delete another page's committed object link

**Reproducer:** `TestPostgresSharedObjectTriggerRaceCharacterization`
(`pkg/crawler/fenced_refresh_concurrency_postgres_integration_test.go`). Also
reproducible by hand with two sessions as described in
`RELEASE_230_INDEPENDENT_RISK_REVIEW.md` R-01.

**Required fix:** change `handle_shared_entity_deletion()` to lock the parent
`WebObjects` row `FOR UPDATE` before the reference count (or retire the trigger in
favour of lock-guarded application code). This is **DDL on schema 1.17** and is
outside the B3 Decision 2 approval.

**Owner/scope:** maintainer decision + a separate schema-function package.

**Acceptance criterion:** the characterization test is updated to assert
`object_rows=1` and `page_y_links=1` for the interleaving above, and the full
PostgreSQL crawler suite stays green.

**Release effect:** blocks any production enablement of `refresh_content` and
therefore of fenced refresh. Does **not** affect default 2.3.0, where
`refresh_content: false`.

### B-02 — P1 for `refresh_content`: legacy refresh deletes hash-shared objects for every page

**Reproducer:** `TestPostgresLegacyRefreshSharedObjectHazardReproducer`.

**Required fix:** make the legacy path page-scoped (delete only the page's
`WebObjectsIndex` rows, then delete only objects with no remaining reference),
mirroring the fenced path. This changes default-path semantics for deployments
that enabled `refresh_content` and needs its own approval and migration note.

**Owner/scope:** maintainer decision + a narrowly scoped crawler package.

**Acceptance criterion:** a shared object survives a legacy refresh of one of the
pages, and `TestPostgresLegacyRefreshSharedObjectHazardReproducer` is replaced by
a positive regression test.

**Release effect:** P1 today only for deployments that turned `refresh_content`
on. At default configuration the path is not executed.

### B-03 — P2: fenced refresh serialises globally

The single advisory lock key makes all fenced refreshes serial. Measured cost for
286 links is ~0.7 s, so it is a throughput limit, not a correctness problem. No
change required for 2.3.0; document it.

### B-04 — P2: unbound fenced sources stay `processing` until lease expiry

Schema 1.17 has no claim-time owner, so unbound source-status mutation is
suppressed. The consequence, observed live (sources 2 and 4 remained
`processing`), is a repeat crawl after the lease expires. This is the approved
fail-closed behaviour; the fix is the ownership-epoch DDL, which is deferred.

### B-05 — P3: `Events` rows disappear despite `automatic_events_removal: never`

`crawl_completed` rows were observed at creation and were gone on later reads.
The janitors only delete rows with a past `expires_at`, and
`SetEventAsProcessed` sets `expires_at = NOW() + 1 minute`, so something marks
them processed. Independent of the agentic scope; needs its own check.

### B-06 — P3: documentation drift (fixed in this package)

`ASYNC_GOAL_B3C3B2F1_REFRESH_DESIGN.md` claimed fenced refresh was refused and
untested while also containing an implemented subsection. Now split into dated
`Original design (2026-11-18)` and `Implemented result (2026-11-20)` sections.

### B-07 — P3: engine startup warnings

The engine logs `Failed to read ruleset's schema file:
./schemas/crowler-rulesets-schema.json` (the file is
`crowler-ruleset-schema.json`) and `Failed to load plugin from ./plugins/: is a
directory`. Both are pre-existing, non-fatal, and the crawl works. Cosmetic.

## 6. The four questions, answered separately

### Can 2.3.0 begin pre-release tests?

**Yes.** Static gates, the full unit suite, the race suite, the complete
PostgreSQL integration suite, a fresh install, the migration/addon/backup/restore
rehearsal, and a real Docker + WebDriver run all pass on
`9e719acce04d3606c46821e33b4f8dc23baa6b36`.

### Can 2.3.0 ship on default configuration?

**Yes, with the deferrals accepted.** At defaults
(`refresh_content: false`, claim mode `legacy`, no MCP servers, mutation policy
disabled, information seed and time series disabled, agent enforcement off),
neither B-01 nor B-02 is reachable. The release note must state the default
posture and the deferred features plainly.

### Can fenced mode be enabled in production?

**No.** B-01 is a silent cross-page link loss that the fenced path inherits from
a pre-existing trigger. Do not enable `fenced` mode, and do not enable
`refresh_content`, until B-01 is fixed and B-02 is decided.

### Are the original WP-00..WP-07 goals all complete?

**No.** WP-05's asynchronous layer and WP-07's full end-to-end chain are not
delivered:

- no resume worker, claim/lease or crash recovery;
- no MCP mutation execution;
- live Ollama and live external MCP were not executed in this gate.

## 7. Evidence summary

Executed on the tested commit:

| Gate | Result |
| --- | --- |
| `go test ./... -count=1 -timeout 90m -p 1` | PASS (pre-change baseline and post-change; see the log names below) |
| `go test -race ./pkg/agent/... ./pkg/database/... ./pkg/crawler/... ./cmd/thecrowler/...` | PASS, exit 0 (`agent 307s`, `database 68s`, `crawler 8.9s`, `cmd 1.8s`) |
| `go vet ./...` and integration-tagged vet | PASS |
| `CGO_ENABLED=0/1 go build ./...` | PASS |
| `gofmt -l pkg cmd services` | clean |
| `actionlint` on every workflow | PASS |
| `git diff --check` | PASS |
| PostgreSQL 17 fresh install | PASS: 1.17, writer, both capabilities, 78 tables |
| v1.17 + agent-goal + mcp addons, two passes each | PASS, fixture row preserved |
| `pg_dump`/`pg_restore` rehearsal | PASS, 77 table-data entries, restored counts match |
| Full PostgreSQL integration suite (4 packages) | PASS, exit 0 |
| Candidate manifests (14 files × lint + strict) | PASS, 28/28 |
| Docker stack readiness | PASS |
| Crawl scenarios S-01, S-01b, S-03..S-09 | PASS |
| Negative guarantees (no dispatch, no resume) | PASS |
| S-02, S-10 | Covered by integration tests, not re-run through the UI |
| Hosted GitHub CI for this tree | **Unverified** — not pushed |
| Live Ollama / live external MCP | **Unverified** — not configured |

Logs are under `/tmp/opencode/rel230/`:
`full-baseline.log`, `full-final.log`, `race.log`, `static.log`,
`actionlint.log`, `pg-full.log`, `pg-fenced-conc5.log`, `kw-fixed.log`,
`mig-*.log`, `restore.log`, `up.log`, `artifacts/stack.log`.

## 8. Known skips and pending CI

| Item | Status |
| --- | --- |
| Hosted GitHub `Go` workflow | Not run on this tree |
| Hosted GitHub `postgres-integration` workflow | Not run on this tree |
| Opt-in `mcp-authorization-e2e` workflow | Not registered on the default branch; not run |
| Information Seed Compose acceptance suite | Not run |
| Live Ollama connectivity and tool calling | Not run (no endpoint/model) |
| Live external MCP endpoint | Not run |
| MySQL runtime | Not run |

Do not mark the release green on hosted CI until the workflows have run on the
pushed candidate commit.

## 9. Approval record required before tagging

Before a pre-release tag, a production migration or a deployment, the maintainer
must record, in a tracked decision:

1. Acceptance or rejection of the six deferrals in section 4.
2. Acknowledgment that B-01 blocks `refresh_content` and fenced mode, and B-02
   blocks nothing at default configuration but must be decided.
3. Confirmation that the release notes state the default posture and the deferred
   features.
4. Confirmation that hosted CI has run on the exact candidate commit, or an
   explicit decision to proceed without it.
5. If fenced mode is to be enabled: a separate fix for B-01 under its own
   approved package, with the characterization test updated.

Nothing in this document constitutes that approval.
