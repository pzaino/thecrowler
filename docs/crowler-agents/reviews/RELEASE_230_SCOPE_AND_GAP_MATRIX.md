# Release 2.3.0 scope and gap matrix

Work package: `WP-REL230-RC1`
Repository: `pzaino/thecrowler`
Branch: `develop`
Reviewed commit: `9e719acce04d3606c46821e33b4f8dc23baa6b36`
Previous reviewed baseline: `be5572de9064c4e4020d5b8f3adf478de4952aad` (B2F1 start)
Release under review: `v2.3.0`
Schema: `1.17`
Writer generation: `verified-ownership-v1.17`
Date: 2026-11-20

This matrix compares the **original agentic plan** with **current code**. It does
not treat any prior review report as evidence; every "current implementation"
cell was checked against source at the reviewed commit.

## 1. Which plan is being compared

The plan referenced by the work package is
`CROWler_Agentic_AI_OpenCode_Master_Plan_2026-11-05.md`. It contains exactly
seven work packages, `WP-00` to `WP-07`, and **no sub-packages**. The
`WP-03B*`, `WP-04C2*`, `WP-05B*` identifiers that appear throughout this
repository are separate sibling task documents that extend individual packages
(for example `The_CROWler_WP-05B3C3B2F1_OpenCode_Task.md`). They are not part of
the master plan's acceptance text.

That distinction matters for a release decision:

- The **master plan** defines what the agentic feature set was supposed to do.
- The **sibling packages** define how parts of it were staged, gated and
  hardened.

A claim of "WP-00..WP-07 complete" must satisfy the master plan, not merely the
sibling packages that were implemented.

## 2. Legenda

- **Ship** — implemented, exercised, safe under the stated default, and covered
  by the release gate.
- **Ship (opt-in)** — implemented and tested but not active by default; enabling
  it is an operator decision with documented prerequisites.
- **Experimental (disabled)** — code exists but a required production
  enablement (config loader, principal provenance, durable dispatch, or a schema
  change) is deliberately absent. It cannot be turned on through a supported
  public contract.
- **Absent** — not implemented.

## 3. WP-by-WP matrix

### WP-01 — local LLM provider architecture, Ollama first

| Field | Value |
| --- | --- |
| Planned acceptance | A v2 agent can send a request to a user-configured Ollama instance, receive valid structured tool calls, return correlated tool results and receive another response. Legacy providers and inert `AIInteraction` remain compatible. Live support is verified only after an opt-in run. |
| Current implementation | `pkg/agent/llm_provider_ollama.go` registers a first-class `ollama` provider (`ollamaProviderName`, `ollamaDefaultChatPath = "/v1/chat/completions"`). It reuses the OpenAI-compatible transport, normalizes base URLs and rejects `/api/chat` explicitly. `pkg/agent/llm_capabilities.go` and `llm_endpoint.go` provide capability reporting and endpoint policy. `pkg/agent/llm_provider.go` registers `ollama` alongside `openai-compatible`/`openai`. |
| Default enabled? | The provider is available but no agent uses it unless the manifest names it. Endpoint, model and credentials are operator/manifest configuration. |
| End-to-end usable? | Yes for `AgentToolLoop`/`AgentGoalRun`/`AIInteraction`. Live verification is opt-in (`CROWLER_OLLAMA_SMOKE_URL`, `CROWLER_OLLAMA_SMOKE_MODEL`). |
| Security limitation | Tool capability is not inferred from the provider name; operators attest tool-capable models through `CROWLER_OLLAMA_TOOL_MODELS`. Unset means "unknown" and keeps historical permissive behaviour. Endpoints must be `http(s)`; redirects are capped and cross-origin redirects drop `Authorization`. |
| 2.3.0 proposal | **Ship (opt-in)**. The code path and tests are present. Live Ollama was **not** executed in this release gate because no Ollama endpoint was configured in the environment; that is an unverified-evidence item, not a code gap. |
| Blocker | None for the code. Release note must state that live Ollama is operator-verified, not release-verified. |

### WP-02 — explicit goal, evidence and acceptance-criteria contract

| Field | Value |
| --- | --- |
| Planned acceptance | A deterministic, policy-owned verifier decides completion. A final model sentence never implies success. New YAML/JSON is schema-compliant and approved before publication. |
| Current implementation | `pkg/agent/action_goal_run.go` (`AgentGoalRun`), `goal_spec.go`, `goal_eval.go`, `goal_evidence.go`. Terminal states and predicate composition live in the evaluator. `schemas/crowler-agent-schema.json` contains `AgentGoalRun`, `acceptance`, `evidence` and `allowlist`. Examples: `agents/examples/goal-source-reader.agent.yaml`, `goal-source-any.agent.yaml`, `goal-crawl-metadata-reader.agent.yaml`. |
| Default enabled? | Opt-in action. Existing `AIInteraction` and `AgentToolLoop` semantics are unchanged. |
| End-to-end usable? | Yes, synchronously, including the case where the model claims success but evidence is missing (the run ends `incomplete`). |
| Security limitation | `any` composition is fail-closed against `denied`/`error` results. `event_observed` is defined but disabled in v1. `information_seed` resource status is disabled in v1. Free text is never proof. |
| 2.3.0 proposal | **Ship (opt-in)**. |
| Blocker | None. |

### WP-03 — trusted native CROWler tool catalog

| Field | Value |
| --- | --- |
| Planned acceptance | An approved agent can retrieve CROWler evidence and request a narrowly authorized existing CROWler operation. Arbitrary shell, SQL, plugin and network execution remain unavailable to the model. |
| Current implementation | `pkg/agent/tools_read.go` registers five read-only tools: `get_source_status`, `search_indexed_pages`, `get_source_crawl_status`, `find_scraped_field_metadata`, `find_artifact_metadata`. `DefaultToolRegistry()` registers only those. Side-effect requests use `pkg/agent/crawl_request_tool.go`, admitted through `AgentToolRegistry.RegisterMutation` and a `mutation_policy`, and only persist a durable proposal with `approval_required`. |
| Default enabled? | Read-only tools: available when an agent opts in with an explicit `allowlist`. Mutation tool: **not in the default registry** and additionally gated by `mutation_policy.enabled`, an explicit operation allowlist, a non-empty source scope, `tool_execution` + `db_write`, trusted standing, and schema 1.17 `CrawlProposals`/`CrawlApprovals`. |
| End-to-end usable? | Read-only: yes. Side-effect: yes as **proposal + operator approval + operator submission**, which is a scheduling acknowledgement, not crawl completion. |
| Security limitation | No dispatcher exists; the model cannot approve its own request; `all` never satisfies the allowlist or source scope. MySQL fails closed. |
| 2.3.0 proposal | **Ship (read-only)** and **Ship (opt-in) for the approval-gated scheduling request**, with the caveat that "scheduled" is not "crawled". |
| Blocker | None for the shipped scope. |

### WP-04 — MCP client integration

Read-only and mutation are separated because they have different maturity.

#### WP-04 read-only

| Field | Value |
| --- | --- |
| Planned acceptance | An agent can call an individually authorized MCP tool alongside an authorized native tool, using the same model loop, independent of the server's advertised preferences. |
| Current implementation | `pkg/agent/mcp_manager.go`, `mcp_config.go`, `mcp_http.go`, `mcp_registration.go`. Both transports are implemented and mutually exclusive per server: `stdio` and `streamable_http`. Tools are namespaced `mcp_<server>_<tool>`. Only operator-approved `read_only: true` pairs are admitted, and only when the run allowlists the exact alias. |
| Default enabled? | No server is configured by default. `mcp.servers` is empty in `config.default`. |
| End-to-end usable? | Yes with a deterministic local fixture. Live external MCP endpoints were not used in this gate. |
| Security limitation | Server advertisements are untrusted; discovery is not authorization; `IsError=true` is a failed call and cannot produce goal evidence; HTTP enforces HTTPS, pinned endpoint, TLS verification, DNS/address policy, redirect rejection, no ambient proxy, bearer isolation and bounded responses. |
| 2.3.0 proposal | **Ship (opt-in)**. |
| Blocker | None. |

#### WP-04 mutation

| Field | Value |
| --- | --- |
| Planned acceptance (master plan) | State-changing tools require their own explicit capability, scoped allowlist and operator approval policy. |
| Current implementation | Effect classification, immutable canonical arguments, durable proposals, quota, approval/denial/revocation, audit and retention are implemented. Operator routes exist under `/v1/console/mcp/mutations/proposals`. There is **no dispatcher, no dispatch claim, no execution backend and no reconciliation**. `MCPMutationDispatchAttempts` is created and privilege-checked but never written. |
| Default enabled? | `mcp.mutation_policy.enabled: false`. A production proposal producer requires a trusted authenticated principal from the manual-run path; background/event runs are principal-less and fail closed. |
| End-to-end usable? | Record-only: propose → approve → revoke/deny, all durable and audited. **No external mutation is ever performed.** |
| Security limitation | The approval is a record, not an execution grant. There is no idempotency/reconciliation contract for a remote system, so dispatch remains out of scope by design. |
| 2.3.0 proposal | **Ship as experimental/disabled** (record-only). Must not be described as "MCP mutation support". |
| Blocker | None for the record-only scope. Any statement implying execution would be false. |

### WP-05 — goal-oriented orchestration and long-running work

| Field | Value |
| --- | --- |
| Planned acceptance | The agent runs toward an objective, performs permitted actions, awaits authorized asynchronous work when required, and declares success only after the trusted verifier confirms its criteria. Behaviour remains bounded and auditable. |
| Current implementation — synchronous | `AgentGoalRun` is implemented and is the supported execution path. |
| Current implementation — durable kernel | `pkg/agent/goal_async_state.go` (pure state machine), `goal_async_store.go` (unexported transactional store), `goal_async_observation.go` (read-only operation observer), `goal_async_resume_eligibility.go` (read-only eligibility). Schema-1.17 goal tables and the same-version addon exist. |
| Current implementation — resume worker | **Absent.** No claim, lease, heartbeat, poller, event consumer, crash recovery, cancellation finalization, retention job or automatic resume exists. Nothing constructs the store in a production service. |
| Default enabled? | N/A: the async path has no activation surface at all. |
| End-to-end usable? | Synchronous goals: yes. Asynchronous resumption: **no**. |
| Security limitation | The async kernel is read-only or test-only. Resume eligibility is diagnostic, not a grant. Loading a checkpoint is not authorization. |
| 2.3.0 proposal | **Ship the synchronous goal loop. Defer the asynchronous layer.** The durable kernel is shipped as internal, inert code with docs marking it inactive. |
| Blocker | This is the WP-00..WP-07 acceptance gap. See section 5. |

### WP-06 — user-defined manifests and compatibility

| Field | Value |
| --- | --- |
| Planned acceptance | A user can author and validate an agent from documentation without editing Go code. New tool/server registration still requires operator approval. |
| Current implementation | `schemas/crowler-agent-schema.json`; `cmd/crowler-agt` with `agents lint`, `agents validate [--strict]`, `agents convert`; 15 example manifests in `agents/examples/` including v2 identity, goal, tool-loop, MCP stdio and MCP HTTP examples. |
| Default enabled? | N/A. |
| End-to-end usable? | Yes: validate with the CLI, then load or upload. |
| Security limitation | v1 jobs-only manifests still parse. Authority-bearing fields reject dataflow references. |
| 2.3.0 proposal | **Ship**. |
| Blocker | None. |

### WP-07 — end-to-end validation, security and operational gates

| Field | Value |
| --- | --- |
| Planned acceptance | A reproducible local environment demonstrates an Ollama-backed authorized agent invoking a vetted native tool and a vetted MCP tool, then proving its acceptance criteria. Negative tests establish that natural-language claims alone do not complete a run. |
| Current implementation | Unit, race, PostgreSQL integration, MCP fixture, VDI camouflage and Compose E2E harnesses exist. This release gate adds a real PostgreSQL 17 migration rehearsal and a real Docker/WebDriver run. |
| Default enabled? | N/A. |
| End-to-end usable? | Partially: this gate did not run a live Ollama model, so the full "Ollama + native tool + MCP tool + criteria" chain is not release-verified end to end. |
| Security limitation | Live model and external MCP evidence remain operator-verified. |
| 2.3.0 proposal | **Ship the automated gates**, with the live-model chain recorded as unverified. |
| Blocker | None for the code; evidence gap for the live-model chain. |

### Cross-cutting items

| Item | Current implementation | Default | 2.3.0 proposal |
| --- | --- | --- | --- |
| `null:` pipeline-testing protocol | `cmd/thecrowler` claims the source, skips navigation/scraping/indexing, and deletes the source asynchronously in legacy mode. Fenced mode quarantines and records a terminal failure. | Legacy behaviour unchanged | **Ship**, documented as a pipeline test, not a deletion request |
| Fenced crawl claim (`SourceCrawlClaims`/`Attempts`/`Policy`) | `pkg/database/source_crawl_attempt.go`, `source_crawl_fence.go`, `source_crawl_result.go`; capability `source-crawl-attempt-v1` | `legacy` | **Ship (opt-in, operator mode switch)** |
| Fenced `refresh_content` | `pkg/crawler/fenced_refresh.go` | `refresh_content: false`; fenced only | **Do not recommend for production** — see the trigger race blocker |
| Legacy `refresh_content` | `pkg/crawler/crawler.go` `deleteWebObjects` | `false` | **Ship as-is**, documented hazard |
| Source ownership epoch (B3 Decision 1) | Semantic model approved only; **no DDL** | N/A | **Defer** |
| Fenced `null:` deletion (B3 Decision 3) | Quarantined | N/A | **Defer** |
| Schema 1.17 goal tables | Present in fresh install, v1.16→v1.17 migration and same-version addon | Present | **Ship** |
| MySQL | Legacy-only; fenced and durable-approval paths fail closed | N/A | **Ship as legacy-only** |

## 4. Default-state summary for 2.3.0

The following are the effective defaults a deployment gets without editing
configuration:

```text
crawler.refresh_content      false
SourceCrawlClaimPolicy.mode  legacy
mcp.servers                  []
mcp.mutation_policy.enabled  false
information_seed.enabled     false
timeseries.enabled           false
agent.identity_enforcement   false
agent.contract_enforcement   false
agent.memory_runtime         false
api.auth.enabled             false
events.heartbeat_enabled     false
```

Consequently, a default 2.3.0 deployment:

- crawls with the legacy claim path;
- does not refresh content;
- has no MCP servers and no mutation policy;
- does not run information-seed discovery or time-series emitters;
- does not enforce agent identity/contract/memory;
- exposes the API without authentication (operator responsibility);
- cannot resume an asynchronous goal because no resume mechanism exists.

## 5. The acceptance gap that decides the release

The master plan's WP-05 and WP-07 acceptance requires an agent to *await
authorized asynchronous work and resume*. Its own `Definition of overall
completion` uses the hedged phrase "resume after asynchronous work **when
supported**", but WP-05's acceptance sentence does not, and WP-07's mandatory
scenario table includes "Async run restart/replay — no duplicate side effects,
consistent checkpoint".

Current code has the durable kernel and the read-only observer/eligibility
services but **no resume worker, no claim/lease, no crash recovery**. Therefore:

- **WP-00..WP-07 is not complete.**
- A release that claims full agentic acceptance would be false.

The honest 2.3.0 scope is:

> local Ollama provider + synchronous acceptance-driven goals + trusted
> read-only native tools + approval-gated native crawl scheduling + read-only
> MCP (stdio and Streamable HTTP) + record-only MCP mutation proposals +
> operator-gated fenced crawl foundation (claims, terminal receipts, atomic
> fenced refresh)

with these explicit deferrals:

> automatic asynchronous goal resumption, MCP mutation dispatch/reconciliation,
> ownership-epoch DDL and its unbound source-status/`null:` enablement, fenced
> `null:` deletion, and production activation of fenced mode.

That scope decision belongs to the maintainer, not to this review. See
`RELEASE_230_GO_NO_GO.md`.
