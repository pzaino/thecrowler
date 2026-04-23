# CROWler Agents: Omnicomprehensive Authoring and Operations Guide (v1 + v2)

This guide is a **self-contained**, practical reference for writing, operating, and integrating CROWler Agents with other agentic systems (including OpenAI Codex and Anthropic Claude-style workflows).

---

## 1) What CROWler Agents are

CROWler Agents are YAML/JSON-defined orchestration units executed by the CROWler events runtime. They let you compose deterministic and AI-assisted workflows from reusable actions (API calls, DB queries, command execution, plugin calls, event emission, branching, and delegation).

### Code-grounded definition (quoted)

> “The CROWler is the foundation; the user builds and deploys autonomous or semi-autonomous agents that leverage CROWler's capabilities…”  

### Execution model at a glance

- Agents are loaded into an in-memory registry at service startup/reload.
- Incoming events are matched by `trigger_type` + `trigger_name`.
- Matching jobs execute serially or in parallel.
- `Decision` steps can branch and delegate to other agents.
- Runtime policy gates (identity, contract, memory) can be toggled via `config.yaml`.

---

## 2) Agent versions and compatibility contract

CROWler supports two manifest styles that coexist:

## v1 (legacy, jobs-only)

- `format_version: v1` (or omitted)
- No explicit `agent_identity`
- Backward-compatible behavior preserved

## v2 (identity-enabled)

- `format_version: v2` (or inferred when `agent_identity` exists)
- Adds governance metadata, trust/capabilities, constraints, memory, and contract
- Enables stronger runtime controls when rollout flags are enabled

### Automatic inference rules

- No `agent_identity` => runtime treats manifest as v1
- With `agent_identity` => runtime treats manifest as v2

---

## 3) Manifest anatomy (authoring model)

A CROWler agent file has two top-level blocks:

1. `agent_identity` (optional in v1, explicit in v2)
2. `jobs` (required)

### 3.1 `agent_identity` (v2)

Typical fields:

- `agent_id`, `name`, `version`, `agent_type`, `owner`
- `trust_level`: `untrusted|restricted|trusted|system`
- `capabilities`: policy-granted capabilities
- `constraints`:
  - `max_steps`
  - `time_budget` (Go duration, e.g., `30s`, `10m`)
  - `resource_limits.cpu_percent`, `resource_limits.memory_mb`
  - `event_rate_limit`
- `memory`: `scope`, `namespace`, `ttl`, `retention`
- `reasoning_mode`, `goal`, `description`, `audit_tags`
- `self_model`
- `agent_contract` (`guarantees`, `assumptions`, `forbidden_actions`, `failure_policy`)

### 3.2 `jobs`

Each job includes:

- `name`
- `process`: `serial|parallel`
- `trigger_type`: `interval|event|manual|signal|agent`
- `trigger_name`
- `steps`: ordered action list

Step shape:

```yaml
- action: AIInteraction
  params:
    model: gpt-4o-mini
    prompt: "Summarize: $response"
```

---

## 4) Action catalog and step semantics

CROWler registers these built-in actions:

- `APIRequest`
- `CreateEvent`
- `RunCommand`
- `AIInteraction`
- `DBQuery`
- `PluginExecution`
- `Decision`

### 4.1 Token resolution and variable interpolation

Supported resolution patterns in step params:

- `$response.field.path` from prior step output
- `{{KEY}}` from key-value store

This allows building dynamic URLs, prompts, commands, and event payloads.

### 4.2 `Decision` + delegation

`Decision` can branch via condition expressions and delegate using:

- `agent_id` (preferred)
- `agent_name`
- `call_agent` (legacy alias)

On delegated execution, runtime can enforce:

- caller capability to delegate
- trust compatibility
- contract restrictions
- cycle detection in delegation graph

---

## 5) Governance and runtime feature flags (important)

In `config.yaml` top-level `agent` section:

```yaml
agent:
  identity_enforcement: false
  contract_enforcement: false
  memory_runtime: false
```

These are rollout guards with safe defaults (`false`) for legacy compatibility.

### 5.1 Identity enforcement

When enabled, the runtime enforces:

- action capability gates
- trust-level gates for sensitive actions
- constraint budgets (`max_steps`, `time_budget`, `event_rate_limit`)

### 5.2 Contract enforcement

When enabled, runtime checks `agent_contract.forbidden_actions` and applies `failure_policy` behavior.

### 5.3 Memory runtime

When enabled, each step gets memory context and optional read/write helpers:

- `memory_read_key` -> inject prior value
- `memory_write_key`, `memory_write_value`
- optional `memory_ttl`

Memory modes: `none`, `ephemeral`, `persistent`.

---

## 6) AI provider model (OpenAI, Claude-compatible patterns)

`AIInteraction` uses provider abstraction (`LLMProvider`) with default provider name `openai-compatible`.

### Provider setting precedence

1. Step `params`
2. `params.config.ai`
3. `params.config`
4. Runtime default (`openai-compatible`)

### Normalized AI inputs

- `provider`, `url`, `auth`, `model`
- `messages` or `prompt`
- `temperature`, `max_tokens`, `top_p`
- optional extras (`presence_penalty`, `frequency_penalty`, etc.)

### OpenAI Codex / Anthropic Claude interfacing guidance

Because runtime is provider-agnostic, you can integrate in two common ways:

1. **OpenAI-compatible endpoint path (recommended in current codebase):**
   - Use `AIInteraction` with `provider: openai-compatible`
   - Point `url` to your selected chat/completions endpoint
   - Pass bearer credential in `auth`

2. **Gateway/proxy path for Claude-style backends:**
   - Keep CROWler on `openai-compatible`
   - Route through an internal gateway that translates request/response format
   - Preserve stable manifest shape while swapping backend implementation

### Minimal AI step template

```yaml
- action: AIInteraction
  params:
    provider: openai-compatible
    url: "https://your-llm-endpoint/v1/chat/completions"
    auth: "Bearer ${LLM_API_KEY}"
    model: "gpt-4o-mini"
    messages:
      - role: system
        content: "Return strict JSON."
      - role: user
        content: "Analyze: $response"
```

---

## 7) How to create agents (end-to-end workflow)

## Step 1: Start from templates

Use:

- `agents/templates/legacy-job-only.yaml`
- `agents/templates/identity-enabled-agent.agent.yaml`
- `agents/templates/multi-agent-delegation.agent.yaml`

## Step 2: Set trigger strategy

Choose based on source of truth:

- `event` for automatic event-driven workflows
- `manual` for operator/invoker controlled runs
- `interval` for scheduled loops
- `agent` for internal delegation chains

## Step 3: Compose steps

Typical order:

1. collect (API/DB/plugin)
2. reason (AIInteraction/Decision)
3. act (CreateEvent/PluginExecution/RunCommand)

## Step 4: Validate and lint

Use CLI:

```bash
crowler-agt agents lint <file>
crowler-agt agents validate --strict <file>
crowler-agt agents convert json2yaml <input.json> [output.yaml]
crowler-agt agents convert yaml2json <input.yaml> [output.json]
```

## Step 5: deploy

- Place files under configured `agents.path` glob(s)
- Reload services (or restart) so registry refreshes
- Optionally upload at runtime via events API upload endpoint

---

## 8) Using agents through `crowler-events` and `crowler-api`

## 8.1 `crowler-events` (control plane for events + upload)

Key endpoints:

- `POST /v1/event/create`
- `POST /v1/event/schedule`
- `GET  /v1/event/status`
- `GET  /v1/event/list`
- `POST /v1/upload/agent` (multipart form, file field `agent`)

Operational flow:

1. submit event
2. event worker routes event type to plugins and agents
3. matched agents execute
4. downstream events emitted and persisted

## 8.2 `crowler-api` (data plane / retrieval)

`crowler-api` provides search/console capabilities that agent workflows can consume through `APIRequest` steps (e.g., source status, content retrieval, search endpoints).

Practical pattern:

- Use agents in `crowler-events` to automate orchestration.
- Use `crowler-api` for discovery, query, and operational data retrieval.

---

## 9) How agents leverage broader CROWler capabilities

Agents can orchestrate core CROWler features by combining actions:

- **Crawler intelligence** via event triggers emitted from crawling lifecycle
- **Database** reads/writes with `DBQuery` and event persistence
- **Plugin runtime** with `PluginExecution`
- **VDI/browser-aware flows** indirectly through plugins
- **Event bus orchestration** with `CreateEvent`
- **Policy/audit** with identity+contract enforcement and runtime audit trail

In short: agents are orchestration glue across crawler, DB, plugins, events, and AI.

---

## 10) Best practices

1. **Design event contracts first.**

   - Keep `event_type` stable and namespaced (`security.finding.generated`).

2. **Prefer v2 manifests for new work.**

   - Add `agent_identity`, constraints, and contract from day one.

3. **Use least privilege capabilities.**

   - Avoid `all` unless needed.

4. **Treat `RunCommand` and `DBQuery` as sensitive.**

   - Pair with higher trust and strict contracts.

5. **Enable strict validation in CI.**

   - Catch unresolved delegation and malformed TTL/durations early.

6. **Use memory intentionally.**

   - Ephemeral for short loops; persistent for cross-run accumulation.

7. **Write deterministic AI prompts.**

   - Ask for strict JSON, include schema hints, and set low temperature for control-plane decisions.

8. **Instrument for auditability.**

   - Populate `owner`, `audit_tags`, contract text, and event correlation details.

9. **Separate coordinator and specialists.**

   - Keep orchestrator lightweight; delegate heavy domain logic.

10. **Keep rollback path.**

   - Route low-confidence branches to watchlist/backlog instead of hard fail.

---

## 11) Domain examples

## 11.1 Cyber Security (SOC triage)

Pattern:

- Trigger on `crawler.page_indexed`
- Enrich with threat intel (`APIRequest`)
- Score with `AIInteraction`
- Branch with `Decision`
- Create incident via plugin + emit `security.incident.recommended`

See shape in `soc-autonomous-triage-v2.agent.yaml` and `cybersecurity-surface-monitor.agent.yaml`.

## 11.2 Marketing (competitor intelligence)

Pattern:

- Manual trigger for campaign analysts
- Pull competitor feed
- AI extract launch/theme positioning deltas
- Emit structured snapshot event

See `marketing-competitor-intel.agent.yaml`.

## 11.3 Scientific Research (new template)

You can model literature + experiment orchestration as:

```yaml
format_version: v2
agent_identity:
  agent_id: scientific-research-orchestrator
  name: Scientific Research Orchestrator
  agent_type: planner
  owner: team:research
  trust_level: restricted
  capabilities: [api_requests, ai_reasoning, emit_event, db_read]
  memory:
    scope: persistent
    namespace: research-lab
    retention: 500
jobs:
  - name: Research Intake
    process: serial
    trigger_type: manual
    trigger_name: run_research_cycle
    steps:
      - action: APIRequest
        params:
          url: "https://internal-repo.local/publications/latest"
          request_type: GET
      - action: AIInteraction
        params:
          model: gpt-4o-mini
          prompt: "Extract hypotheses, methods, and unresolved questions as JSON: $response"
          memory_write_key: latest_research_map
      - action: CreateEvent
        params:
          source: "agent.scientific-research-orchestrator"
          event_name: "research.map.updated"
          event_type: "research.map.updated"
          severity: "info"
          details:
            summary: "$response"
```

## 11.4 Finance/Risk

Pattern:

- Trigger on news ingestion
- AI classify macro/sector/company risk
- Emit risk signal event for downstream trading/risk dashboards

See `finance-news-risk-signal.agent.yaml`.

---

## 12) Practical integration playbooks for MCPs and external agents

For Codex/Claude-like orchestrators that generate or maintain CROWler agents:

1. **Generate manifest from intent**
   - choose v2, explicit identity, explicit constraints
2. **Run local checks**
   - lint + strict validate
3. **Deploy**
   - commit file under `agents/` or upload via `/v1/upload/agent`
4. **Trigger test event**
   - `POST /v1/event/create`
5. **Observe outputs**
   - `GET /v1/event/list` and inspect resulting `event_type`s
6. **Tighten policy**
   - enable `identity_enforcement`, then `contract_enforcement`, then `memory_runtime`

### Recommended machine-friendly conventions

- Enforce lowercase/hyphen `agent_id`
- Namespace event names (`domain.subject.verb`)
- Require every AI step to request strict JSON
- Include correlation IDs in event details
- Keep one coordinator + N specialists per domain workflow

---

## 13) Troubleshooting checklist

- Agent not executing?
  - Verify trigger pair (`trigger_type`, `trigger_name`) matches emitted event.
- Delegation failing?
  - Ensure target `agent_id`/`agent_name` exists and no cycle introduced.
- AI step failing?
  - Check `url`, `auth`, provider name, and `prompt/messages` presence.
- Policy denials?
  - Review trust level, capabilities, and `forbidden_actions` patterns.
- Memory not present?
  - Confirm `agent.memory_runtime: true` and memory scope not `none`.

---

## 14) Reference snippets

## 14.1 Minimal v1

```yaml
format_version: v1
jobs:
  - name: LegacyDiscoveryJob
    process: serial
    trigger_type: manual
    trigger_name: legacy_discovery
    steps:
      - action: RunCommand
        params:
          command: echo "legacy mode"
```

## 14.2 Minimal v2

```yaml
format_version: v2
agent_identity:
  agent_id: identity-enabled-agent
  name: IdentityEnabledAgent
  trust_level: trusted
  capabilities: [all]
jobs:
  - name: IdentityEnabledAgent
    process: serial
    trigger_type: event
    trigger_name: page_crawled
    steps:
      - action: CreateEvent
        params:
          source: "0"
          event_name: agent_completed
          event_type: agent.completed
```

---

## 15) Final recommendations

If you are building new automations today:

- Use v2 manifests.
- Start with strict validation in CI.
- Keep contracts explicit and capabilities narrow.
- Favor event-driven composition over monolithic job chains.
- Treat AI as a controlled decision component, not an unconstrained executor.

That approach gives you portable manifests for humans, MCPs, and autonomous coding agents while staying aligned with CROWler runtime guarantees.
