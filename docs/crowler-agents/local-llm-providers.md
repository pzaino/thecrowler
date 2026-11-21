# Local LLM providers

CROWler agents reach models through the `LLMProvider` interface in
`pkg/agent/llm_provider.go`. This document describes the built-in local
provider, how to point an agent at it, and how to add another local engine
without changing the agent loop, tool policy or acceptance logic.

## Built-in providers

| Name | Transport | Notes |
| --- | --- | --- |
| `openai-compatible` (default) | OpenAI-style `POST` | Any compatible endpoint, including a gateway or a proxy in front of another engine. |
| `openai` | Same as `openai-compatible` | Registered alias kept for shipped manifests and earlier documentation. |
| `ollama` | Ollama's OpenAI-compatible `POST /v1/chat/completions` | First-class local provider. Base URLs are normalized. Native `/api/chat` is not implemented. |

Both providers share the same request builder, response normalization,
cancellation handling, size limits and redirect policy. The difference is
endpoint handling and capability reporting.

## Configuring Ollama

```yaml
format_version: v2
agent_identity:
  agent_id: local-reader
  name: Local Reader
  trust_level: trusted
  capabilities:
    - ai_reasoning
    - db_read
    - tool_execution
jobs:
  - name: Local Reader
    process: serial
    trigger_type: manual
    trigger_name: run_local_reader
    steps:
      - action: AgentToolLoop
        params:
          provider: ollama
          url: "http://ollama:11434"
          model: "qwen3:8b"
          prompt: "Check source 7 and report its indexed pages."
          allowlist:
            - get_source_status
            - search_indexed_pages
          allowed_sources:
            - 7
```

The `url` may be:

* a base URL: `http://ollama:11434` or `http://localhost:11434`;
* the version root: `http://ollama:11434/v1`;
* the full compatible endpoint: `http://ollama:11434/v1/chat/completions`;
* a proxy path ending in `/chat/completions`.

`/api/chat`, `/api/generate` and unrelated paths are rejected with an
actionable error rather than sent to the wrong handler.

Ollama's compatible endpoint is a chat API. A legacy prompt-only request
(`prompt` without `messages`) is therefore serialized as one `user` chat
message. Explicit `messages` always take precedence, and prompt-plus-tools
requests are converted the same way. The `openai-compatible` provider keeps
its historical completion-style `prompt` payload unchanged.

## Tool-capable models

Ollama installs many models, and not every model implements tool calling.
CROWler does not infer tool support from the provider name. Operators attest
it explicitly:

```bash
# Only these models may use tools
export CROWLER_OLLAMA_TOOL_MODELS="qwen3:8b,llama3.1:8b"

# Every model is attested (use only when the whole deployment is controlled)
export CROWLER_OLLAMA_TOOL_MODELS="*"

# Explicitly deny tool calls for every model
export CROWLER_OLLAMA_TOOL_MODELS=""
```

Unset means "unknown": historical permissive behaviour is preserved for
compatibility. `AgentToolLoop` denies before any HTTP exchange only when the
provider reports `unsupported`. `AIInteraction` continues to send declarative
tool schemas (they are inert data) regardless of the attestation.

Embedding applications can bypass the environment variable and register a
provider instance directly:

```go
agent.RegisterOllamaProvider([]string{"qwen3:8b"})
```

## Capability reporting

Providers may implement either optional interface:

```go
type ProviderCapabilityReporter interface {
    Capabilities() LLMProviderCapabilities
}

type ModelCapabilityReporter interface {
    CapabilitiesForModel(model string) LLMProviderCapabilities
}
```

`LLMProviderCapabilities` reports chat, tool calls, structured output,
streaming, tool-result messages, cancellation and optional context/output
limits. Each state is `supported`, `unsupported` or `unknown`. Providers that
implement neither interface are reported as all-unknown and keep the existing
behaviour. The legacy two-method `LLMProvider` interface is unchanged.

## Endpoint and transport policy

* Only `http` and `https` schemes are accepted.
* A host is required. `localhost`, single-label container names and IP
  literals (including IPv6) are valid.
* Embedded URL credentials and fragments are rejected.
* Redirect chains are capped at five.
* A redirect to a different scheme or `host:port` is blocked, and
  `Authorization` is not forwarded. Reconfigure the endpoint at its final
  origin instead of relying on redirects.
* Provider output is capped at 8 MiB. Error text carries only the HTTP status
  and a fixed category; it never echoes response bodies, prompts, endpoint
  URLs, hostnames, query strings or credentials.
* Endpoints are operator configuration. `AgentToolLoop` rejects dataflow
  references in `provider`, `url`, `auth` and `model` in direct step params,
  in the `config.ai` fallback, and in config inherited from a previous step,
  so model output cannot select an inference endpoint.
* Unix domain socket endpoints are not supported in this stage. Expose the
  local engine on a loopback or container TCP listener instead.

## Adding another local engine

1. Implement `LLMProvider` (`Name`, `Execute`). Implement
   `ContextAwareProvider` as well if the transport supports cancellation.
2. Optionally implement `ProviderCapabilityReporter` and/or
   `ModelCapabilityReporter`.
3. Register the provider during deployment with `RegisterLLMProvider`.
4. Reuse `NormalizeChatCompletion` only if the engine speaks OpenAI-style
   chat completions; otherwise normalize into the same `LLMNormalizedResponse`
   shape so the tool loop stays provider-agnostic.
5. Add mock-HTTP tests for text, tool calls, malformed payloads, non-2xx,
   timeouts, cancellation and redirects. Keep live-engine tests opt-in and
   report a skipped test as unverified.

Do not add provider-specific behaviour to `tool_loop.go` or to tool handlers.
Provider differences belong behind the provider interface.
