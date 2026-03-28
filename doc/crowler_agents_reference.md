# Using Agents with the CROWler

The CROWler allows you to run multiple agents (either in series or in parallel).
 This is useful when you have a large number of agents that you want to run at
 the same time.
 The CROWler will automatically distribute the agents across multiple cores on
 your machine, allowing you to run many agents at once.

Agents are also useful when a user does not wish to code complex plugins or
wish to leverage AI models to perform tasks such ass data validation,
enrichment, correction, manipulation etc. and combine it with actions.

Agents should be defined in YAML files and stored in the `./agents/` path.

## Compatibility contract (Milestone 0 baseline)

- Legacy manifests with `jobs` only are still valid and continue to run unchanged.
- Identity-enabled manifests with `agent_identity + jobs` are also valid.
- `format_version` is supported as a compatibility marker:
  - `v1` for legacy jobs-only mode
  - `v2` for identity-enabled mode
- If omitted, compatibility mode is derived automatically:
  - no `agent_identity` => `v1`
  - with `agent_identity` => `v2`

Below an example of such YAML file.

## Identity-aware execution guards (Milestone 3)

Identity-aware execution is controlled by runtime flags under `agent`:

- `agent.identity_enforcement` (default `false`)
- `agent.contract_enforcement` (default `false`)
- `agent.memory_runtime` (default `false`)

When `identity_enforcement` is enabled, execution uses `agent_identity` metadata
to enforce:

- action capability checks (`run_command`, `db_query`, `ai_reasoning`,
  `plugin_execution`, `create_event`, `decision`)
- trust-level checks for sensitive actions (`RunCommand`, `DBQuery`,
  `PluginExecution`)
- constraint budgets (`max_steps`, `time_budget`, `event_rate_limit`)

Each run also receives an execution context snapshot with run correlation fields
(`run_id`, `trace_id`, `source`, `owner`, `identity_snapshot`) injected into
step `config.agent_runtime`.

## Decision and delegation model (Milestone 4)

`Decision` branches can now target delegated agents using:

- `agent_id` (preferred when present)
- `agent_name`
- legacy `call_agent` (name alias)

With `agent.identity_enforcement=true`, delegation also enforces:

- caller delegation capability (`delegate`)
- caller/callee trust compatibility
- caller/callee contract checks for delegation restrictions
- target availability checks before execution
- delegation graph tracking with cycle detection to prevent loops

Delegation failures are deterministic and surfaced as explicit errors (for
example, target unavailable, policy denial, or cycle detected).


## AI provider abstraction (Milestone 5)

`AIInteraction` now executes against a provider abstraction (`LLMProvider`)
internally, so orchestration is provider-agnostic.

Resolution order for AI provider settings is deterministic:

1. Step-level `params`
2. Step config `params.config.ai`
3. Step config `params.config`
4. Runtime defaults (provider defaults to `openai-compatible`)

Supported normalized AI fields include `messages`, `prompt`, `model`,
`temperature`, `max_tokens`, `top_p`, and related sampling parameters.

When identity enforcement is enabled, `AIInteraction` requires the
`ai_reasoning` capability (legacy alias `ai_interaction` is still accepted for
backward compatibility) and enforces model/provider policy restrictions from
agent trust level and contract `forbidden_actions` entries
(e.g. `model:gpt-4*`, `provider:example*`).

## Examples of configuring Agents

```yaml

# Examples of a set of agents configuration file in YAML format:

jobs:
  - name: "Serial Agent 1"
    process: "serial"
    trigger_type: event
    trigger_name: "event_name"
    steps:
      - action: "APIRequest"
        params:
          config:
            url: "http://example.com/api/data"
      - action: "AIInteraction"
        params:
          prompt: "Summarize the following data: $response"
          config:
            url: "https://api.openai.com/v1/completions"
            api_key: "your_api_key"

  - name: "Parallel Agent 1"
    process: "parallel"
    trigger_type: event
    trigger_name: "event_name"
    steps:
      - action: "DBQuery"
        params:
          query: "INSERT INTO logs (message) VALUES ('Parallel job 1')"
      - action: "RunCommand"
        params:
          command: "echo 'Parallel action $response.status'"

  - name: "Serial Agent 2"
    process: "serial"
    trigger_type: agent
    trigger_name: "agent_name"
    steps:
      - action: "PluginExecution"
        params:
          plugin_name: "example_plugin"

```
