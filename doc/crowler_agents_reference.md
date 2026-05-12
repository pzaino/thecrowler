# CROWler Agents User Guide

A combined authoring and operations guide for users that need to create, validate, deploy, and maintain CROWler Agents.

## 1. Purpose of this guide

This guide explains CROWler Agents from first principles. It is designed to take a user from zero knowledge to being able to write production-grade agent manifests in YAML or JSON.

It covers:

* what CROWler Agents are,
* how agents fit into the CROWler runtime,
* the v1 and v2 manifest formats,
* how jobs, triggers, steps, actions, memory, delegation, and governance work,
* how to call agents from rulesets,
* how to validate, deploy, operate, and troubleshoot agents,
* how to design agents for cybersecurity, marketing, finance, research, and other domains,
* how AI coding agents should generate safe, schema-aware CROWler agent files.

CROWler Agents are part of The CROWler’s broader content discovery and automation platform. The CROWler crawls, detects, scrapes, indexes, enriches, and exposes data through APIs, rulesets, plugins, events, and storage. Agents sit on top of those capabilities as orchestration units.

## 2. What CROWler Agents are

A CROWler Agent is a YAML or JSON-defined orchestration unit executed by the CROWler  runtime (usually either the CROWler Events Manager, the General API or the Engines). An agent describes one or more jobs. Each job contains ordered steps. Each step performs one action, such as an API call, AI interaction, database query, command execution, plugin execution, event creation, or decision branch.

Agents can be simple deterministic workflows or AI-assisted workflows. They can process data, react to events, call plugins, create new events, query databases, delegate to other agents, and integrate with external systems.

A useful mental model is:

> An agent manifest is a declarative workflow file that tells CROWler what jobs exist, when they run, what steps they execute, what data they pass between steps, and what runtime controls apply.

Agents can be triggered by:

* manual invocation,
* rules,
* events,
* intervals,
* signals where supported,
* other agents,
* uploaded manifests or runtime event control flows.

Agents are intended to extend CROWler capabilities without writing Go code. They can still use JavaScript plugins through `PluginExecution`, and they can use AI through `AIInteraction`.

## 3. How agents fit into CROWler

The CROWler is a web content discovery and data collection platform. Its core features include crawling, scraping, action execution, technology detection, network information collection, file and image collection, API integration, rulesets, plugins, data storage, event-driven processing, and AI or traditional agents.

Agents are the orchestration layer across those capabilities.

They can:

* react to crawl lifecycle events,
* enrich scraped data,
* execute AI classification or summarization,
* call external APIs,
* query the CROWler database,
* execute CROWler plugins,
* emit events for downstream workflows,
* make branch decisions,
* delegate work to specialist agents,
* integrate with external agentic systems.

A typical flow looks like this:

1. The CROWler Engine crawls a source.
2. Rulesets scrape, detect, or interact with pages.
3. The runtime emits events, such as a page being crawled, data being extracted, or a security finding being generated.
4. The Events Manager matches those events against registered agents using `trigger_type` and `trigger_name`.
5. Matching jobs execute their steps.
6. Steps pass output forward through `$response` references.
7. Decision steps may branch or delegate to another agent.
8. Agents may emit new events, persist data, or call APIs and plugins.

In short, agents are glue between crawler, database, plugin runtime, events, API, and AI.

## 4. Execution model at a glance

At startup or reload, CROWler loads agent files into an in-memory registry. The registry maps agent names and, in identity-enabled mode, stable agent IDs to executable jobs.

When an incoming event, schedule, manual invocation, or agent call occurs, CROWler evaluates the trigger pair:

```text
trigger_type + trigger_name
```

If a job matches that pair, it is executed according to its `process` field:

* `serial`: execute steps in order,
* `parallel`: execute eligible work in parallel, subject to runtime implementation and action semantics.

Each step has this basic shape:

```yaml
- action: AIInteraction
  params:
    model: gpt-4o-mini
    prompt: "Summarize: $response"
```

The output of a step becomes available to the next step as `$response`. Specific keys can be referenced using paths such as:

```text
$response.status_code
$response.body
$response.severity
$response.user_id
```

Some runtime implementations also support key-value interpolation through:

```text
{{KEY}}
```

This lets authors build dynamic URLs, prompts, commands, event payloads, and decision expressions.

## 5. Agent manifest versions

CROWler supports two manifest styles.

### 5.1 v1 legacy jobs-only manifests

v1 is the legacy format. It defines jobs directly and does not require an explicit identity block.

Characteristics:

* `format_version: v1`, or omitted in legacy usage,
* no required `agent_identity`,
* backward-compatible behavior,
* useful for simple jobs and older deployments.

Minimal v1 example:

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

### 5.2 v2 identity-enabled manifests

v2 adds an explicit identity and governance model. It is the recommended format for all new agents.

Characteristics:

* `format_version: v2`,
* explicit `agent_identity`,
* governance metadata,
* trust level,
* capabilities,
* constraints,
* memory policy,
* self-model,
* contract,
* better delegation and auditability.

Minimal v2 example:

```yaml
format_version: v2
agent_identity:
  agent_id: identity-enabled-agent
  name: IdentityEnabledAgent
  version: 1.0.0
  agent_type: executor
  owner: platform
  trust_level: trusted
  capabilities:
    - command_execution
    - emit_event
  constraints:
    max_steps: 10
    time_budget: 30s
  memory:
    scope: ephemeral
    namespace: identity-enabled-agent
    ttl: 10m
    retention: 100
jobs:
  - name: IdentityEnabledAgent
    process: serial
    trigger_type: event
    trigger_name: page_crawled
    steps:
      - action: RunCommand
        params:
          command: echo "identity mode"
      - action: CreateEvent
        params:
          event_name: agent_completed
          event_type: agent.completed
          source: identity-enabled-agent
```

### 5.3 Compatibility and inference rules

Use this compatibility model when authoring:

* If `agent_identity` is omitted, the runtime treats the manifest as legacy v1 style.
* If `agent_identity` exists, the runtime treats the manifest as v2 or identity-enabled style.
* If `agent_identity` is omitted in a deployment that supports normalization, runtime defaults may be derived from `jobs[0].name`, including generated identity fields and fallback governance defaults.
* For v2, keep `agent_identity.name` exactly aligned with `jobs[0].name` unless the runtime explicitly supports looser binding.

### 5.4 Schema compatibility note

The CROWler agent schema and the prose guides may not always be at the same rollout level. For example, the guide-level v2 identity fields may exist in newer code paths while an older schema may still expose the legacy jobs-focused shape. When there is a conflict, use the schema from the exact CROWler build you deploy and run strict validation before merging or uploading an agent.

The safest authoring strategy is:

1. Write v2 manifests for new work.
2. Keep the job shape compatible with the strict schema.
3. Use canonical action names.
4. Use required action parameters exactly as the schema expects.
5. Run `crowler-agt agents lint` and `crowler-agt agents validate --strict`.

## 6. YAML or JSON

CROWler agents can be authored in YAML or JSON. YAML is recommended for humans because it is easier to read and review. JSON is useful for machine generation, APIs, or tools that already emit JSON.

Use CLI conversion utilities for round trips:

```bash
crowler-agt agents convert json2yaml <input.json> [output.yaml]
crowler-agt agents convert yaml2json <input.yaml> [output.json]
```

For AI agents generating manifests, YAML is usually the better target unless the receiving pipeline requires JSON.

## 7. Top-level manifest anatomy

A CROWler agent manifest has these top-level sections:

```yaml
format_version: v2
agent_identity:
  # identity and governance fields
jobs:
  # executable jobs
```

### 7.1 `format_version`

Accepted guide-level values:

* `v1`: legacy jobs-only format,
* `v2`: identity-enabled format.

Use `v2` for new manifests.

### 7.2 `agent_identity`

Optional in v1 style. Expected for first-class v2 design.

This block describes what the agent is, who owns it, what it is allowed to do, and what constraints apply.

### 7.3 `jobs`

Required. An array of executable jobs. Each job defines a trigger, execution process, and step list.

## 8. The `agent_identity` block

The `agent_identity` block is the governance and identity model for a v2 agent.

Recommended fields:

```yaml
agent_identity:
  agent_id: stable-lowercase-id
  name: HumanReadableName
  version: 1.0.0
  agent_type: executor
  owner: team:platform
  trust_level: restricted
  capabilities:
    - api_request
    - ai_interaction
    - emit_event
  constraints:
    max_steps: 10
    time_budget: 30s
    resource_limits:
      cpu_percent: 50
      memory_mb: 256
    event_rate_limit: 10
  reasoning_mode: deterministic-with-ai-assist
  memory:
    scope: ephemeral
    namespace: stable-lowercase-id
    ttl: 10m
    retention: 100
  goal: "Summarize newly crawled pages."
  description: "Extracts structured summaries from crawled page events."
  audit_tags:
    - domain:content
    - pii:none
  self_model:
    can_modify_identity: false
    can_spawn_agents: false
    can_modify_jobs: false
  agent_contract:
    guarantees:
      - "Emits only namespaced event types."
    assumptions:
      - "Input events contain details.url and details.content."
    forbidden_actions:
      - "RunCommand"
      - "delegate:system"
    failure_policy: emit_error_event
```

### 8.1 `agent_id`

A stable runtime identifier. Prefer lowercase hyphenated IDs:

```yaml
agent_id: soc-triage-coordinator
```

Use `agent_id` for durable delegation targets across teams, files, and releases. Do not rename it casually. Treat it as an API contract.

### 8.2 `name`

A human-readable identifier:

```yaml
name: SOCTriageCoordinator
```

For compatibility, keep this equal to the first job name:

```yaml
jobs:
  - name: SOCTriageCoordinator
```

### 8.3 `version`

Semantic version for the manifest:

```yaml
version: 1.0.0
```

Use version bumps when changing behavior, event contracts, or delegation topology.

### 8.4 `agent_type`

A descriptive type marker. Common examples:

* `executor`,
* `coordinator`,
* `planner`,
* `specialist`,
* `enricher`,
* `responder`.

### 8.5 `owner`

Ownership tag:

```yaml
owner: platform
owner: team:secops
owner: team:research
```

Use this for audit, lifecycle ownership, incident routing, and CI ownership checks.

### 8.6 `trust_level`

A governance level used by policy gates.

Common values:

* `untrusted`,
* `restricted`,
* `trusted`,
* `system`.

General guidance:

* Use `restricted` for agents that call APIs or AI but do not execute sensitive operations.
* Use `trusted` for agents that query sensitive stores, delegate to important responders, or emit high-impact events.
* Reserve `system` for platform-owned agents.
* Treat `RunCommand` and privileged `DBQuery` as sensitive and pair them with explicit trust and contract controls.

### 8.7 `capabilities`

Capabilities are explicit allow-list entries for runtime gates. They should express what the agent is allowed to do.

Common guide-level capabilities:

```yaml
capabilities:
  - api_request
  - ai_interaction
  - db_query
  - command_execution
  - plugin_execution
  - emit_event
  - delegate
```

Some examples may use names such as `api_request`, `ai_reasoning`, or `db_read`. Prefer the exact capability names used by your deployed policy engine. Avoid `all` unless you are creating a tightly controlled platform agent.

### 8.8 `constraints`

Constraints bound runtime behavior.

Common fields:

```yaml
constraints:
  max_steps: 12
  time_budget: 45s
  resource_limits:
    cpu_percent: 50
    memory_mb: 512
  event_rate_limit: 20
```

Use Go duration strings for time budgets:

* `30s`,
* `10m`,
* `1h`.

### 8.9 `reasoning_mode`

A descriptive marker for how the agent reasons or makes decisions. This is useful for audit, review, and AI-generated manifests.

Examples:

```yaml
reasoning_mode: deterministic
reasoning_mode: ai-assisted-json-only
reasoning_mode: coordinator-delegation
```

### 8.10 `memory`

Memory controls how much state the agent may use.

```yaml
memory:
  scope: ephemeral
  namespace: research-paper-triage
  ttl: 24h
  retention: 100
```

Supported guide-level scopes:

* `none`: no memory,
* `ephemeral`: short-lived state,
* `persistent`: cross-run state.

Use ephemeral memory for short loops and persistent memory for cross-run accumulation. Use a namespace that matches the agent ID or domain.

### 8.11 `goal`, `description`, and `audit_tags`

Use these fields to make manifests self-documenting:

```yaml
goal: "Classify crawler-generated security alerts."
description: "Coordinates SOC triage from page-indexed events."
audit_tags:
  - domain:security
  - severity:routing
```

These fields are valuable for human review and AI agents that need to modify manifests safely.

### 8.12 `self_model`

Defines whether the agent can alter itself or spawn more agents.

```yaml
self_model:
  can_modify_identity: false
  can_spawn_agents: false
  can_modify_jobs: false
```

For most production agents, keep all three false.

### 8.13 `agent_contract`

The contract describes guarantees, assumptions, forbidden actions, and failure behavior.

```yaml
agent_contract:
  guarantees:
    - "Never executes shell commands."
    - "Emits only event types under marketing.competitor.*."
  assumptions:
    - "The competitor feed endpoint returns JSON."
  forbidden_actions:
    - "RunCommand"
    - "delegate:system"
  failure_policy: emit_error_event
```

Use contracts to communicate intent, enable policy enforcement, and give AI systems guardrails when editing agents.

## 9. Jobs

A job is an executable workflow inside an agent. Each job has a name, trigger, process mode, and step list.

Canonical shape:

```yaml
jobs:
  - name: ExampleJob
    description: "Optional human-readable description."
    process: serial
    trigger_type: event
    trigger_name: crawler.page_indexed
    steps:
      - action: CreateEvent
        params:
          event_name: example_completed
          event_type: example.completed
          source: "0"
```

Required fields:

* `name`,
* `process`,
* `trigger_type`,
* `trigger_name`,
* `steps`.

Optional fields may include:

* `description`,
* `timeout`,
* `plugins_timeout`,
* runtime-specific settings.

### 9.1 `name`

The job name identifies the workflow. In v2, align the first job name with `agent_identity.name`.

### 9.2 `process`

Valid values:

```yaml
process: serial
process: parallel
```

Use `serial` unless you specifically need parallel behavior and have verified runtime behavior.

### 9.3 `trigger_type`

Guide-level supported trigger types include:

* `manual`,
* `event`,
* `interval`,
* `agent`,
* `signal` where supported by the deployed runtime.

Some schemas may only enumerate `interval`, `event`, and `manual`. Validate against your exact deployed schema.

### 9.4 `trigger_name`

The meaning of `trigger_name` depends on `trigger_type`.

For `event`, it should match the event type:

```yaml
trigger_type: event
trigger_name: crawler.page_indexed
```

For `manual`, it is the manual trigger name:

```yaml
trigger_type: manual
trigger_name: risk_refresh
```

For `interval`, common schema-compatible formats include:

```yaml
trigger_type: interval
trigger_name: every 5 minutes
```

or:

```yaml
trigger_type: interval
trigger_name: at 2026-05-12T10:00:00Z
```

Some prose examples use strings like `every 360 minutes`. Keep to the interval format accepted by your validator.

For `agent`, it should identify the internal agent trigger name if your runtime supports that trigger type.

### 9.5 `steps`

Steps are ordered action invocations. Each step has:

* `action`: one built-in action name,
* `params`: action-specific parameters,
* optional `description`.

The output of one step becomes available to the next step through `$response`.

## 10. Step semantics and data chaining

Each step executes one action. The step output becomes the next step input.

Example:

```yaml
steps:
  - action: APIRequest
    params:
      request_type: GET
      url: "https://example.com/feed.json"

  - action: AIInteraction
    params:
      provider: openai-compatible
      model: gpt-4o-mini
      prompt: "Extract key facts as JSON from $response.body"

  - action: CreateEvent
    params:
      event_name: feed_summary_ready
      event_type: intelligence.feed.summary_ready
      source: "0"
      details:
        summary: "$response"
```

Use `$response.<key>` when referencing a specific field from the previous step.

Examples:

```text
$response.status_code
$response.body
$response.body.items
$response.severity
$response.user_id
```

If your runtime supports key-value interpolation, you can also use:

```text
{{KEY}}
```

Use interpolation carefully in commands, SQL, and URLs. Validate and sanitize input in the producing step or plugin.

## 11. Built-in action catalog

CROWler registers these built-in actions:

* `APIRequest`,
* `AIInteraction`,
* `DBQuery`,
* `RunCommand`,
* `PluginExecution`,
* `CreateEvent`,
* `Decision`.

### 11.1 `APIRequest`

Use `APIRequest` to call HTTP APIs.

Schema-compatible baseline:

```yaml
- action: APIRequest
  params:
    url: "https://example.com/api/feed"
    request_type: GET
    headers:
      Accept: application/json
```

Common parameters:

* `url`,
* `request_type`: `GET`, `POST`, `PUT`, `DELETE`,
* `method`: used in some guide examples, but `request_type` may be required by strict schemas,
* `headers`,
* `body`,
* timeout or retry fields where supported.

Authoring guidance:

* Prefer `request_type` if targeting the uploaded strict schema.
* Prefer stable internal endpoints for production workflows.
* Do not hard-code secrets. Use environment variables or configured secret injection where supported.

### 11.2 `AIInteraction`

Use `AIInteraction` to call an LLM or AI provider.

Minimal prompt-based form:

```yaml
- action: AIInteraction
  params:
    provider: openai-compatible
    model: gpt-4o-mini
    prompt: "Return strict JSON summarizing: $response.body"
```

Messages-based form:

```yaml
- action: AIInteraction
  params:
    provider: openai-compatible
    url: "https://your-llm-endpoint/v1/chat/completions"
    auth: "Bearer ${LLM_API_KEY}"
    model: gpt-4o-mini
    temperature: 0.1
    max_tokens: 800
    messages:
      - role: system
        content: "Return strict JSON only."
      - role: user
        content: "Analyze: $response"
```

Common parameters:

* `provider`,
* `url`,
* `auth`,
* `model`,
* `prompt`,
* `messages`,
* `temperature`,
* `max_tokens`,
* `top_p`,
* `presence_penalty`,
* `frequency_penalty`,
* `stop`,
* `api_key`,
* user or session metadata where supported.

Strict schema baseline usually requires either:

* `model` + `prompt`, or
* `model` + `messages`.

Authoring guidance:

* Ask for strict JSON when the next step depends on structured output.
* Use low temperature for control-plane decisions.
* Keep prompts explicit about expected keys.
* Treat AI as a bounded decision component, not an unconstrained executor.

### 11.3 `DBQuery`

Use `DBQuery` to query a database.

Minimal form:

```yaml
- action: DBQuery
  params:
    query: "SELECT source_id, url, status FROM Sources WHERE status = 'new' LIMIT 10"
```

Optional connection parameters:

```yaml
- action: DBQuery
  params:
    db_type: postgres
    db_host: db.internal
    db_port: 5432
    db_name: SitesIndex
    db_user: crowler
    db_password: ${CROWLER_DB_PASSWORD}
    query: "SELECT ticker, beta, var_95 FROM portfolio_positions"
```

If no external DB connection settings are provided, the runtime may use the current CROWler database.

Authoring guidance:

* Treat `DBQuery` as sensitive.
* Prefer read-only queries unless the agent is explicitly designed for writes.
* Avoid building SQL directly from untrusted `$response` values.
* Use least privilege database credentials.

### 11.4 `RunCommand`

Use `RunCommand` to execute a system command.

Example:

```yaml
- action: RunCommand
  params:
    command: echo "identity mode"
```

Authoring guidance:

* Treat `RunCommand` as highly sensitive.
* Avoid it unless needed.
* Require explicit capability, trust level, constraints, and contract restrictions.
* Never inject untrusted `$response` values into shell commands without validation.
* Prefer `PluginExecution`, `APIRequest`, or `DBQuery` where possible.

### 11.5 `PluginExecution`

Use `PluginExecution` to execute a loaded CROWler plugin.

Example:

```yaml
- action: PluginExecution
  params:
    plugin_name: normalize_advisory_payload
    severity_hint: high
```

Required parameter:

* `plugin_name`.

Authoring guidance:

* Plugins must already be loaded by CROWler.
* Plugins are JavaScript-based in the CROWler plugin model.
* Use plugins for domain-specific transformations, enrichment, integration, or browser/runtime capabilities not directly represented by built-in actions.

### 11.6 `CreateEvent`

Use `CreateEvent` to emit an event into CROWler.

Example:

```yaml
- action: CreateEvent
  params:
    event_name: competitor_digest_ready
    event_type: marketing.competitor.digest
    source: "0"
    details:
      summary: "$response"
```

Common parameters:

* `event_name`,
* `event_type`,
* `source`,
* `details`,
* payload fields supported by your runtime.

Event authoring guidance:

* Namespace event types, for example `security.finding.generated`.
* Keep event contracts stable.
* Include correlation IDs when possible.
* Emit observability events at important workflow boundaries.

### 11.7 `Decision`

Use `Decision` to branch based on an expression and optionally delegate to another agent.

If/else form:

```yaml
- action: Decision
  params:
    condition:
      condition_type: if
      expression: '$response.severity == "critical"'
      on_true:
        agent_id: soc-incident-responder
      on_false:
        agent_id: soc-lowrisk-enricher
```

Strict legacy-compatible form may require `agent_name` in branches:

```yaml
- action: Decision
  params:
    condition:
      condition_type: if
      expression: '$response.status_code == 200'
      on_true:
        agent_name: SuccessHandler
      on_false:
        agent_name: FailureHandler
```

Switch-style decision, where supported:

```yaml
- action: Decision
  params:
    condition:
      condition_type: switch
      expression: "$response.category"
      cases:
        - value: critical
          agent_name: CriticalHandler
        - value: low
          agent_name: LowRiskHandler
```

Branch targets may use:

* `agent_id`, preferred in v2,
* `agent_name`, useful for local or legacy manifests,
* `call_agent`, legacy alias.

When targeting the strict schema, verify which branch target keys are accepted.

## 12. Delegation model

Delegation lets one agent call another agent from a `Decision` branch. This supports coordinator-specialist architectures.

Resolution order in the guide-level model:

1. `agent_id`, if present,
2. `agent_name`, if present,
3. `call_agent`, as legacy fallback.

Example coordinator branch:

```yaml
- action: Decision
  params:
    condition:
      condition_type: if
      expression: '$response.risk_score >= 90'
      on_true:
        agent_id: high-risk-responder
      on_false:
        agent_id: watchlist-enricher
```

Runtime policy gates may enforce:

* caller must have `delegate` capability or equivalent,
* caller trust level must be greater than or equal to callee trust level,
* caller and callee contracts must not forbid delegation,
* forbidden action patterns must not match the delegation path,
* delegation cycles must be detected and denied.

Authoring guidance:

* Prefer `agent_id` for durable cross-file delegation.
* Use `agent_name` for local readability and backward compatibility.
* Give coordinator agents `delegate` capability.
* Keep specialist agents narrow and domain-specific.
* Avoid deep delegation chains unless necessary.
* Design explicit fallback branches.

## 13. Governance and runtime feature flags

Identity, contract, and memory controls may be guarded by runtime rollout flags in `config.yaml`.

Guide-level example:

```yaml
agent:
  identity_enforcement: false
  contract_enforcement: false
  memory_runtime: false
```

These defaults preserve legacy compatibility. Enable them progressively.

### 13.1 Identity enforcement

When enabled, runtime may enforce:

* action capability gates,
* trust-level gates for sensitive actions,
* constraints such as `max_steps`, `time_budget`, and `event_rate_limit`,
* delegation trust compatibility.

### 13.2 Contract enforcement

When enabled, runtime may check:

* `agent_contract.forbidden_actions`,
* delegation restrictions,
* action restrictions,
* declared failure policy.

### 13.3 Memory runtime

When enabled, steps may receive memory context and optional read/write helpers:

```yaml
memory_read_key: previous_summary
memory_write_key: latest_research_map
memory_write_value: "$response"
memory_ttl: 24h
```

Memory modes:

* `none`,
* `ephemeral`,
* `persistent`.

Authoring guidance:

* Use `none` for stateless workflows.
* Use `ephemeral` for short-lived context.
* Use `persistent` only when there is a clear retention need.
* Always set a namespace.
* Use TTLs for bounded data lifecycle.

## 14. AI provider model

`AIInteraction` uses a provider abstraction. The guide-level default provider name is:

```text
openai-compatible
```

Provider settings may be resolved in this precedence order:

1. Step `params`,
2. `params.config.ai`,
3. `params.config`,
4. runtime default.

Normalized AI inputs may include:

* `provider`,
* `url`,
* `auth`,
* `model`,
* `messages`,
* `prompt`,
* `temperature`,
* `max_tokens`,
* `top_p`,
* `presence_penalty`,
* `frequency_penalty`,
* other provider extras.

### 14.1 OpenAI-compatible endpoint path

Recommended current pattern:

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

### 14.2 Claude-style or other backend via gateway

If a backend does not natively match the expected request or response shape, use a gateway/proxy:

1. Keep the manifest using `provider: openai-compatible`.
2. Point `url` to an internal gateway.
3. Have the gateway translate between CROWler’s expected shape and the backend provider.
4. Preserve stable manifest shape while swapping backends internally.

### 14.3 AI step design rules

For production use:

* request strict JSON,
* include expected output keys,
* set low temperature for decisions,
* keep prompts short but specific,
* do not let AI output directly become shell commands,
* validate AI output before using it in DB queries, commands, or high-impact events.

## 15. Ruleset integration with `agent_call`

CROWler rules can invoke agents directly through `agent_call` blocks. This lets a rules execution phase enrich extraction output by calling an agent at that point in the pipeline.

Guide-level `agent_call` fields:

* `agent_name`: required,
* `params`: object,
* `timeout`: integer seconds, must be at least 1,
* `on_error`: `fail`, `continue`, or `ignore`,
* `store_as`: result key,
* `merge_strategy`: `replace`, `merge`, `append`, or `ignore`.

Defaults when omitted:

```yaml
on_error: fail
merge_strategy: replace
```

Ruleset snippet:

```yaml
selectors:
  - selector: "a.security-advisory"
    agent_call:
      agent_name: CyberIntelSummarizer
      timeout: 30
      on_error: continue
      store_as: advisory_summary
      merge_strategy: merge
      params:
        severity_hint: high
```

Authoring guidance:

* Use `on_error: continue` for enrichment that should not block scraping.
* Use `on_error: fail` for mandatory processing.
* Use `store_as` to keep outputs deterministic.
* Use `merge_strategy: merge` when adding structured enrichment to existing extraction results.

## 16. Events and event contracts

Agents commonly use events as both triggers and outputs.

A CROWler event generally contains:

```yaml
source_id: 0
event_type: crawler.page_indexed
event_severity: low
event_timestamp: "2026-05-12T10:00:00Z"
details:
  url: "https://example.com/page"
  content: "..."
```

Important event fields:

* `source_id`: source identifier, or `0` when no source applies,
* `event_type`: event routing key,
* `event_severity`: severity label,
* `event_timestamp`: timestamp from the generating system,
* `details`: custom JSON passed to plugins and agents.

Design event contracts before writing agents.

Recommended event naming:

```text
domain.subject.verb
```

Examples:

```text
crawler.page_indexed
security.finding.generated
security.incident.recommended
marketing.competitor.digest
finance.risk.signal
research.map.updated
agent.completed
agent.failed
```

## 17. How to create a CROWler Agent

### Step 1: Define the purpose

Write one sentence:

```text
When <trigger> happens, the agent should <collect>, <reason>, and <emit or act>.
```

Example:

```text
When a security alert event is created, classify its severity, delegate critical alerts to an incident responder, and delegate all others to a low-risk enricher.
```

### Step 2: Choose v1 or v2

Use v2 for new work.

Use v1 only for:

* legacy deployments,
* compatibility tests,
* quick local experiments,
* deployments that do not yet support identity blocks.

### Step 3: Choose the trigger strategy

Choose based on source of truth:

* `event`: automatic event-driven workflows,
* `manual`: operator-controlled runs,
* `interval`: scheduled loops,
* `agent`: internal delegation chains where supported,
* `signal`: runtime signal handling where supported.

### Step 4: Design event contracts

Before writing steps, define:

* input event type,
* required `details` fields,
* emitted event types,
* output payload shape,
* error event shape.

### Step 5: Compose steps

A common pattern is:

1. collect: `APIRequest`, `DBQuery`, or `PluginExecution`,
2. reason: `AIInteraction` or `Decision`,
3. act: `CreateEvent`, `PluginExecution`, `RunCommand`, or delegation.

### Step 6: Add governance

For v2, add:

* stable `agent_id`,
* owner,
* trust level,
* narrow capabilities,
* constraints,
* memory policy,
* contract.

### Step 7: Validate

Run:

```bash
crowler-agt agents lint <file>
crowler-agt agents validate --strict <file>
```

Fix schema and semantic errors before deployment.

### Step 8: Deploy

Common deployment options:

* place files under configured `agents.path` glob patterns,
* reload or restart services so the registry refreshes,
* upload at runtime through the events API upload endpoint where available.

### Step 9: Trigger a test

Trigger manually or submit a test event.

Example flow:

1. upload or reload agent,
2. create test event,
3. inspect emitted events,
4. check logs,
5. tighten policy,
6. add CI validation.

## 18. CLI workflow

Use the `crowler-agt agents` workflow:

```bash
crowler-agt agents lint <file>
crowler-agt agents validate --strict <file>
crowler-agt agents convert json2yaml <input.json> [output.yaml]
crowler-agt agents convert yaml2json <input.yaml> [output.json]
```

### 18.1 `lint`

Use for schema and compatibility-oriented checks.

### 18.2 `validate --strict`

Use for semantic validation beyond the schema. Strict validation may check:

* naming quality,
* trigger completeness,
* `trigger_type` and `trigger_name` pairing,
* decision target resolvability,
* memory TTL duration format,
* agent identity and first job name alignment,
* delegation graph errors,
* unsupported action parameters.

### 18.3 CI recommendation

Run strict validation in CI for every manifest:

```bash
for file in agents/**/*.yaml; do
  crowler-agt agents lint "$file"
  crowler-agt agents validate --strict "$file"
done
```

## 19. Deployment and runtime integration

### 19.1 Agent file loading

CROWler configuration can define agent load locations. Agents may be loaded from local or remote paths depending on deployment configuration.

Typical local pattern:

```yaml
agents:
  - type: local
    path:
      - "./agents/*.yaml"
    agents_timeout: 60
    plugins_timeout: 60
```

Common location settings include:

* `path`,
* `type`: `local`, `http`, `s3`, or deployment-supported values,
* `host`,
* `port`,
* `region`,
* `token`,
* `secret`,
* `timeout`,
* `agents_timeout`,
* `plugins_timeout`,
* `sslmode`,
* `refresh` where supported.

### 19.2 Reloading

Depending on deployment mode, agents may be reloaded by:

* service restart,
* service reload,
* configuration/rules reload mechanisms,
* runtime upload endpoint.

Use the deployment method supported by your CROWler version.

## 20. Using agents through `crowler-events` and `crowler-api`

### 20.1 `crowler-events`

`crowler-events` acts as the control plane for events and uploads.

Guide-level endpoints:

```text
POST /v1/event/create
POST /v1/event/schedule
GET  /v1/event/status
GET  /v1/event/list
POST /v1/upload/agent
```

For agent upload, use multipart form with file field:

```text
agent
```

Operational flow:

1. Submit an event.
2. Event worker routes the event type to plugins and agents.
3. Matching agents execute.
4. Downstream events are emitted and persisted.
5. Event list/status endpoints are used for inspection.

### 20.2 `crowler-api`

`crowler-api` is the data plane for retrieval, search, and console capabilities. Agent workflows can use it through `APIRequest` steps.

Practical pattern:

* Use `crowler-events` for orchestration.
* Use `crowler-api` for discovery, query, and operational data retrieval.

Example:

```yaml
- action: APIRequest
  params:
    request_type: GET
    url: "http://crowler-api:8080/v1/search?q=title:security"
```

## 21. Migration from v1 to v2

To migrate a legacy jobs-only manifest:

1. Keep existing `jobs` unchanged.
2. Add `format_version: v2`.
3. Add `agent_identity`.
4. Set at least:

   * `agent_id`,
   * `name`,
   * `trust_level`,
   * `capabilities`.
5. Ensure `agent_identity.name == jobs[0].name`.
6. Add constraints.
7. Add memory policy.
8. Add contract.
9. Run lint and strict validation.
10. Test with identity enforcement disabled first, then progressively enable enforcement.

Legacy input:

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

Migrated v2:

```yaml
format_version: v2
agent_identity:
  agent_id: legacy-discovery-job
  name: LegacyDiscoveryJob
  version: 1.0.0
  agent_type: executor
  owner: platform
  trust_level: restricted
  capabilities:
    - command_execution
  constraints:
    max_steps: 3
    time_budget: 10s
  memory:
    scope: none
    namespace: legacy-discovery-job
  agent_contract:
    guarantees:
      - "Executes only the declared diagnostic command."
    assumptions:
      - "Manual trigger is operator-controlled."
    forbidden_actions:
      - "delegate:*"
    failure_policy: fail
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

## 22. Production checklist

Use this checklist before deploying a new agent.

Identity and naming:

* Use `format_version: v2`.
* Set a stable lowercase-hyphen `agent_id`.
* Ensure `agent_identity.name` matches `jobs[0].name`.
* Set `version`.
* Set `owner`.
* Add `description` and `goal`.

Triggers and events:

* Define trigger pair explicitly.
* Ensure emitted event types are namespaced.
* Define input and output event contracts.
* Include correlation IDs when possible.

Actions:

* Use canonical action names.
* Use schema-compatible parameters.
* Avoid `RunCommand` unless necessary.
* Treat `DBQuery` as sensitive.
* Use `PluginExecution` for reusable domain logic.

Governance:

* Use least privilege capabilities.
* Set conservative trust level.
* Define constraints.
* Define memory policy.
* Define forbidden actions.
* Define failure policy.

AI:

* Ask for strict JSON.
* Include schema hints in prompts.
* Use low temperature for decisions.
* Validate AI output before high-impact actions.

Delegation:

* Prefer `agent_id`.
* Add `delegate` capability to coordinators.
* Ensure target agents exist.
* Avoid cycles.
* Keep specialists narrow.

Validation and deployment:

* Run lint.
* Run strict validation.
* Test with sample events.
* Inspect emitted events.
* Add CI checks.
* Roll out enforcement flags gradually.

## 23. Common authoring errors and fixes

### `trigger_type and trigger_name must both be set`

Fix: define both fields on every job.

```yaml
trigger_type: event
trigger_name: crawler.page_indexed
```

### `agent_identity.name must match jobs[0].name`

Fix: align names exactly.

```yaml
agent_identity:
  name: SOCTriageCoordinator
jobs:
  - name: SOCTriageCoordinator
```

### `Decision branch must include one of agent_id, agent_name, or call_agent`

Fix: add a concrete target in each branch.

```yaml
on_true:
  agent_id: high-risk-responder
on_false:
  agent_id: watchlist-enricher
```

If strict schema requires `agent_name`, use:

```yaml
on_true:
  agent_name: HighRiskResponder
on_false:
  agent_name: WatchlistEnricher
```

### `Decision target is not resolvable`

Fix: ensure the target exists locally or in the registered agent registry.

### `agent_identity.memory.ttl must be a valid Go duration`

Fix: use Go duration strings:

```yaml
ttl: 30s
ttl: 10m
ttl: 1h
ttl: 24h
```

### `timeout must be >= 1` in rules `agent_call`

Fix:

```yaml
timeout: 30
```

### `APIRequest missing request_type`

Fix: strict schemas may require `request_type` rather than `method`.

```yaml
params:
  request_type: GET
  url: "https://example.com"
```

### `AIInteraction missing prompt or messages`

Fix: include either `prompt` or `messages` with `model`.

```yaml
params:
  model: gpt-4o-mini
  prompt: "Summarize $response as JSON"
```

### Agent not executing

Check:

* the agent is loaded,
* the trigger pair matches exactly,
* the event type is correct,
* service reload occurred,
* validation passed,
* logs show no policy denial.

### Delegation failing

Check:

* target exists,
* target ID or name matches,
* caller has delegation capability,
* trust level allows the call,
* contract does not forbid delegation,
* no cycle was introduced.

### Memory not present

Check:

* memory runtime flag is enabled,
* memory scope is not `none`,
* namespace is set,
* TTL is valid,
* step memory keys are supported by your runtime.

## 24. Domain examples

### 24.1 Cybersecurity: SOC triage coordinator

Use case: deterministic escalation path with auditable branching.

```yaml
format_version: v2
agent_identity:
  agent_id: soc-triage-coordinator
  name: SOCTriageCoordinator
  version: 1.0.0
  agent_type: coordinator
  owner: team:secops
  trust_level: trusted
  capabilities:
    - delegate
    - emit_event
    - ai_interaction
  constraints:
    max_steps: 8
    time_budget: 30s
  memory:
    scope: persistent
    namespace: soc-triage
    retention: 500
  agent_contract:
    guarantees:
      - "Routes critical alerts to incident response."
      - "Routes non-critical alerts to enrichment."
    assumptions:
      - "Input event details contain alert_payload."
    forbidden_actions:
      - "RunCommand"
    failure_policy: emit_error_event
jobs:
  - name: SOCTriageCoordinator
    process: serial
    trigger_type: event
    trigger_name: security.alert.created
    steps:
      - action: AIInteraction
        params:
          provider: openai-compatible
          model: gpt-4o-mini
          temperature: 0.1
          prompt: >
            Classify alert severity from $response.alert_payload.
            Return strict JSON with keys severity and rationale.
      - action: Decision
        params:
          condition:
            condition_type: if
            expression: '$response.severity == "critical"'
            on_true:
              agent_id: soc-incident-responder
            on_false:
              agent_id: soc-lowrisk-enricher
```

Compatibility variant for strict branch schemas:

```yaml
on_true:
  agent_name: SOCIncidentResponder
on_false:
  agent_name: SOCLowRiskEnricher
```

### 24.2 Cybersecurity: surface monitor pattern

Pattern:

1. Trigger on `crawler.page_indexed`.
2. Enrich with threat intelligence through `APIRequest`.
3. Score with `AIInteraction`.
4. Branch with `Decision`.
5. Create incident through plugin or emit `security.incident.recommended`.

Skeleton:

```yaml
format_version: v2
agent_identity:
  agent_id: cybersecurity-surface-monitor
  name: CybersecuritySurfaceMonitor
  version: 1.0.0
  agent_type: coordinator
  owner: team:secops
  trust_level: trusted
  capabilities:
    - api_request
    - ai_interaction
    - plugin_execution
    - emit_event
    - delegate
  constraints:
    max_steps: 10
    time_budget: 45s
  memory:
    scope: ephemeral
    namespace: cybersecurity-surface-monitor
    ttl: 1h
  agent_contract:
    guarantees:
      - "Emits namespaced security events only."
    assumptions:
      - "Input page events contain URL and extracted content."
    forbidden_actions:
      - "RunCommand"
    failure_policy: emit_error_event
jobs:
  - name: CybersecuritySurfaceMonitor
    process: serial
    trigger_type: event
    trigger_name: crawler.page_indexed
    steps:
      - action: APIRequest
        params:
          request_type: GET
          url: "https://example-threat-intel.local/lookup?url=$re
```
