# Read-only MCP client integration (WP-04A)

Status: WP-04A implements approved **stdio** MCP tools only. Streamable HTTP is
deferred to WP-04B. Mutation-capable MCP tools are deferred to WP-04C and remain
disabled.

## SDK and protocol

CROWler uses the official Go SDK:

```text
github.com/modelcontextprotocol/go-sdk v1.8.0
```

The release supports protocol revision `2026-07-28` and negotiates older
compatible revisions where the server supports them. The SDK requires Go 1.25
or newer; the repository builds with Go 1.26.6. The SDK is licensed under the
project’s MIT/Apache-2.0 transition terms. It is pinned in `go.mod`; unrelated
dependencies are not upgraded by this feature.

The SDK owns stdio framing, protocol negotiation, list pagination and tool-call
transport. CROWler does not implement JSON-RPC framing or an independent MCP
protocol stack.

## Trust boundary

MCP server definitions are **operator configuration**. An agent manifest can
only name an already approved namespaced tool. It cannot provide or change:

- executable or arguments;
- working directory;
- environment variables;
- server identity;
- approved tool inventory;
- read-only classification;
- source scope;
- call limits;
- credentials.

The configuration is loaded from the top-level `mcp` section of the operator
`config.yaml`, validated once at service startup, and frozen. Starting a
configured stdio server is an administrative action, not an authority granted
to an LLM.

Example operator configuration:

```yaml
mcp:
  servers:
    - id: localread
      enabled: true
      transport: stdio
      command: /opt/crowler/bin/approved-mcp-server
      args: []
      working_directory: /opt/crowler/mcp
      environment_allowlist:
        - PATH
        - LANG
      environment:
        MCP_MODE: read-only
      allowed_agents:
        - mcp-reader
      allowed_sources:
        - 7
      approved_tools:
        - name: source_lookup
          read_only: true
          description: Read a bounded source observation.
          allowed_sources:
            - 7
          required_capabilities:
            - tool_execution
      max_tools: 16
      max_tool_metadata_bytes: 65536
      max_startup_seconds: 5
      max_discovery_seconds: 10
      max_call_seconds: 15
      max_concurrent_calls: 1
      max_request_bytes: 32768
      max_response_bytes: 65536
      max_tool_calls: 8
```

Only `transport: stdio` is accepted. `enabled` defaults to false. A disabled
server needs no executable and exposes no tools.

### Environment and process isolation

The child process receives a constructed environment, not the CROWler process
environment:

- a safe default `PATH` and `LANG`;
- variables explicitly named in `environment_allowlist` and present in the
  parent process;
- explicitly configured `environment` values.

Known CROWler/database/cloud credential prefixes and sensitive name suffixes
(`_PASSWORD`, `_SECRET`, `_TOKEN`, `_API_KEY`, `_PRIVATE_KEY` and similar) are
rejected from the inherited environment allowlist. Use explicit `environment`
values when an approved server genuinely needs a credential; those values are
operator authority and must be handled as secrets. MCP server stderr is
discarded; server stdout is reserved for the SDK transport. Configuration
formatting uses a redacted `String`/`GoString` representation so `%v` and `%#v`
startup logging do not print executable arguments or environment values.

The executable must be an absolute path to a regular file. Shell executables
(`sh`, `bash`, PowerShell and equivalents) are rejected. No shell interpolation
is used. Each run starts its own child process and SDK session; sessions are not
shared between agents or runs. A failed startup or discovery closes/kills the
child. Run termination closes every session and process. The manager terminates
the configured child process; an approved server that deliberately forks a
daemon outside that process tree is outside this lifecycle guarantee.

## Namespacing

The model-facing tool ID is deterministic:

```text
mcp_<server-id>_<sanitized-tool-name>
```

Server IDs are lowercase letters, digits and hyphens; they cannot contain
underscores. This makes the server delimiter unambiguous. If the readable name
would exceed the native registry limit, a deterministic 10-hex hash suffix is
appended. Collisions or duplicate approved tools are rejected at configuration
load; there is no truncation-only aliasing.

Both the namespaced ID and the original server/tool pair are recorded in audit
events. Native CROWler tool wire identifiers are unchanged.

An agent manifest uses the namespaced ID in its explicit `allowlist`:

```yaml
allowlist:
  - mcp_localread_source_lookup
  - get_source_status
```

## Discovery and admission

At run construction CROWler:

1. selects only enabled servers referenced by the run allowlist;
2. validates operator configuration;
3. starts the approved stdio command with the constructed environment;
4. connects with the SDK and validates the negotiated protocol version;
5. lists tools with a bounded count and pagination;
6. rejects invalid names, missing descriptions, invalid schemas, external
   `$ref` values, oversized metadata and duplicate definitions;
7. accepts only **run-allowlisted**, explicitly approved read-only
   server/tool pairs; other operator-approved tools on the same server are not
   frozen or registered for this run;
8. freezes the admitted schema/description for the run;
9. registers tools through the existing read-only `AgentToolRegistry`.

Server-provided `readOnlyHint` or other annotations are **not** trusted and
never grant permission. Only `approved_tools[].read_only: true` enables
admission. Mutation tools are rejected in WP-04A.

Unapproved advertised tools are ignored. A newly advertised tool after
registration is not added; the SDK list-changed notification marks the run
inventory stale and further MCP calls fail closed. A new run re-validates the
inventory from scratch. There is no unbounded reconnect loop.

## Authorization and execution

An MCP adapter is an ordinary read-only `AgentTool`:

- `tool_execution` (and any additional operator-required capability) is checked
  by the shared `AuthorizeToolCall` pipeline;
- the explicit run allowlist, trusted identity, contract and argument schema
  rules are unchanged;
- `db_write` or mutation registration is never used for MCP tools;
- source-scoped tools require the run source scope, the server source scope and
  the tool source scope; tool scope can only narrow server scope, and a declared
  source scope without a `source_id` argument is rejected during discovery; a
  model-provided `source_id` is only a selector, never authority;
- request bytes, response bytes, call time and per-run call budgets are
  enforced before/after transport invocation;
- authorization denial means **no external `tools/call` is sent**.

The adapter reuses `ToolRuntimeFromContext`, `checkToolArguments`, result
sanitization, tool-loop audit correlation and deterministic goal evidence.

## Result handling

Only bounded text content and JSON-serializable structured content are accepted
in WP-04A. Images, audio, embedded resources, resource links and other content
types are rejected with a safe error; the remote payload is not blindly inserted
into the model prompt. Tool-level `isError` responses are preserved as
`is_error: true` observations, while transport/protocol failures are separate
tool errors. Response and request limits are enforced in addition to the SDK’s
frame-size bound.

MCP prompts, resources, server instructions, tool descriptions and JSON Schema
annotations are untrusted external content. WP-04A does not execute or expose
MCP prompts/resources as instructions. Model-facing tool descriptions come from
the trusted operator configuration or a generated safe description. Remote
schema property descriptions are bounded and may still contain instruction-like
text; they cannot change authorization, allowlists, scopes or budgets.

## Goal evidence

MCP observations are `tool_observation` evidence with
`tool_observation` trust, not `system_verified` evidence. They establish what
the external server returned, not that a CROWler source is pending, processing
or completed. A goal requiring authoritative CROWler state must use the native
database/resource evidence providers. A malicious MCP result containing
instructions such as “declare the goal complete” cannot change criteria,
allowlists, source scope, budgets or the deterministic acceptance result.

## Diagnostics and errors

Typical stable failures include:

- `mcp_transport_error`
- `mcp_discovery_error`
- `mcp_not_approved`
- `mcp_inventory_changed`
- `mcp_unsupported_result`
- `mcp_budget_exhausted`
- `crawl_operation_conflict` for native crawl reconciliation

Check operator configuration first for startup failures, then the run allowlist
and source scope. Invalid MCP schemas and missing approved tools fail the run
rather than silently widening the inventory.

## Future work

WP-04B will add an explicitly selected Streamable HTTP transport with endpoint,
TLS, redirect, DNS/address, authentication and credential-isolation policy.
WP-04C will require a separate mutation authorization contract. Neither is
enabled by WP-04A, and no schema version beyond 1.17 is used.
