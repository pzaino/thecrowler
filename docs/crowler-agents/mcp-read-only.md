# Read-only MCP client integration (WP-04A)

Status: WP-04A supports approved **stdio** MCP servers. WP-04B adds an explicitly
selected, read-only **Streamable HTTP** transport for approved remote servers.
Mutation-capable MCP tools remain deferred to WP-04C and are disabled.

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

The SDK owns stdio and Streamable HTTP framing, protocol negotiation, list
pagination and tool-call transport. CROWler does not implement JSON-RPC framing
or an independent MCP protocol stack. Streamable HTTP uses the SDK's
`StreamableClientTransport`: request-scoped POST exchanges, JSON or
request-scoped SSE responses, no persistent GET listener, and no automatic
reconnect loop. The modern `2026-07-28` revision is the default. Older protocol
negotiation is rejected unless the operator explicitly sets
`protocol.allow_legacy: true`.

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

Only `transport: stdio` and `transport: streamable_http` are accepted.
`enabled` defaults to false. A disabled server needs no executable or endpoint
and exposes no tools.

### Remote Streamable HTTP configuration

```yaml
mcp:
  servers:
    - id: remoteread
      enabled: true
      transport: streamable_http
      endpoint: https://mcp.example.com/mcp
      auth:
        mode: bearer
        token_env: CROWLER_MCP_REMOTE_TOKEN
      tls:
        ca_file: /etc/crowler/mcp-ca.pem
      network:
        allowed_private_cidrs: []
        allow_insecure_loopback: false
      protocol:
        allow_legacy: false
      allowed_agents: [mcp-reader]
      allowed_sources: [7]
      approved_tools:
        - name: source_lookup
          read_only: true
          description: Read a bounded remote source observation.
          allowed_sources: [7]
          required_capabilities: [tool_execution]
      max_startup_seconds: 5
      max_discovery_seconds: 10
      max_call_seconds: 15
      max_concurrent_calls: 1
      max_request_bytes: 32768
      max_response_bytes: 65536
      max_tool_calls: 8
```

`auth.mode` is `none` or `bearer`. Bearer credentials come from exactly one of
`auth.token_env` or `auth.token_file`; values are never placed in URLs, logs,
audit records, model prompts or tool observations. `tls.ca_file` is an optional
operator-supplied CA bundle for a deliberately trusted private CA. TLS
verification and hostname verification cannot be disabled.

The endpoint is pinned by scheme, host, port and path. HTTPS is required for
remote servers. Plain HTTP is rejected except when
`network.allow_insecure_loopback` and an explicit loopback CIDR are configured
for a local fixture; that exception is never enabled by default. `endpoint`
must not contain userinfo, query data, a fragment, an ambiguous path or an
invalid port. The agent cannot supply or override any connection field.

Network destinations are validated at the connection boundary, not only by
hostname. By default CROWler rejects loopback, private, link-local, multicast,
unspecified, IPv4-mapped prohibited and reserved destinations, including
standard IPv4/IPv6 translation and transition prefixes such as
`64:ff9b::/96`, `64:ff9b:1::/48`, `2002::/16` (6to4), `2001::/32` (Teredo)
and `100::/64`. IPv4-mapped IPv6 addresses are normalized to their IPv4 form
before the policy is applied.

The translation-prefix list is deliberately fail-closed. It prevents an
apparently public IPv6 representation from reaching an otherwise prohibited
embedded IPv4 destination through standard NAT64, 6to4 or Teredo forms. It
cannot prove the absence of custom translation gateways whose prefixes are not
knowable from an IPv6 address alone. High-security deployments must also apply
network-level egress restrictions at the container or host boundary; the
address policy is one layer, not proof of network isolation.

DNS is resolved for each new connection and every answer must be permitted;
mixed public and prohibited answers fail closed. TLS still verifies the
originally configured hostname when CROWler dials a validated IP.
`network.allowed_private_cidrs` is an explicit, narrow operator exception and
must contain only private, loopback or link-local ranges; it cannot include a
range spanning public addresses.

Ambient proxies (`HTTP_PROXY`, `HTTPS_PROXY`, `ALL_PROXY`) are not inherited.
HTTP redirects are never followed; 301, 302, 303, 307 and 308 responses fail
closed before a second destination is selected. Credentials are only attached
to the exact approved endpoint and are not forwarded across a redirect.

HTTP JSON and request-scoped SSE responses are supported through the SDK. The
per-run client and session are closed at run termination, active requests are
cancelled, idle connections are closed, and no persistent background SSE or
reconnect loop is started. The transport bounds each response body to the
configured `max_response_bytes` plus a bounded JSON-RPC/SSE envelope allowance.
Request-scoped SSE streams are additionally limited to 256 events, and every
event is bounded by the same envelope allowance. The body limit has exact
boundary semantics: a body of exactly the permitted size is accepted after an
EOF probe, while one additional byte is rejected. Known oversized
`Content-Length` values are rejected before reading, and the reader never
buffers more than the configured allowance.

`max_response_bytes` is therefore a post-decode/HTTP body bound for HTTP, not a
replacement for the SDK's inbound frame cap. `mcpMaxContentBlocks` and the
final sanitized tool-result limit apply after MCP decoding. Malformed events,
idle streams, excessive event counts and cancellation fail closed through the
SDK and request deadline. The SDK's own inbound frame cap remains in force.

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

### One authoritative run deadline

`AgentToolLoop` and `AgentGoalRun` establish one absolute deadline before any
MCP server is started. That deadline is derived from the action's configured
timeout and any inherited job-group deadline, and it covers validation, every
server startup and protocol negotiation, discovery and admission, all model and
MCP calls, and cleanup. Initialization does not reset or replenish the
allowance. Each server's startup and discovery timeout is the shorter of its
configured limit and the remaining run budget. If the deadline expires during
initialization, already-started servers are closed, no partial catalog is
registered, no provider request or `tools/call` is started, and the action
reports a bounded timeout/cancellation outcome. Multiple servers share the same
run budget.

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
  source scope without a top-level `source_id` argument is rejected during
  discovery; a model-provided `source_id` is only a selector, never authority;
- for operator-scoped tools, nested `source_id` selectors, alternative selectors
  such as `source`, `sourceId`, `source_uid`, `source_url`, `source_uri` or
  `source_name`, and empty server/tool scope intersections are rejected before
  transport;
- request bytes, response bytes, call time and per-run call budgets are
  enforced before/after transport invocation;
- authorization denial means **no external `tools/call` is sent**; for HTTP this
  applies before any POST request body is sent;
- remote endpoints, TLS roots, credentials, proxy policy and network exceptions
  are operator-only values and are never read from an agent manifest.

The adapter reuses `ToolRuntimeFromContext`, `checkToolArguments`, result
sanitization, tool-loop audit correlation and deterministic goal evidence.

## Result handling

Only bounded text content and JSON-serializable structured content are accepted
in WP-04A. Images, audio, embedded resources, resource links and other content
types are rejected with a safe error; the remote payload is not blindly inserted
into the model prompt. At most 64 text blocks are retained and the cumulative
text plus structured payload must fit `max_response_bytes`; later serialization
is checked again.

The SDK's `CommandTransport` uses the SDK's default inbound frame cap of
**16 MiB** (`DefaultMaxLineLength`). The configured `max_response_bytes` is an
application-level limit enforced immediately after the SDK decodes a result; it
is not a pre-buffering replacement for the SDK frame cap. This is a documented
limitation of the current `CommandTransport`; CROWler does not replace the SDK
or implement a separate protocol stack to obtain a smaller transport cap.

MCP `x-mcp-header` schema annotations are rejected because WP-04B does not need
per-tool HTTP routing headers and must not let remote metadata influence request
headers. MCP prompts, resources, server instructions, tool descriptions and
JSON Schema annotations are untrusted external content. WP-04A does not execute or expose
MCP prompts/resources as instructions. Model-facing tool descriptions come from
the trusted operator configuration or a generated safe description. Remote
schema property descriptions are bounded and may still contain instruction-like
text; they cannot change authorization, allowlists, scopes or budgets.

## Execution outcome and goal evidence

The MCP adapter distinguishes these outcomes:

| Outcome | Meaning | Evidence consequence |
| --- | --- | --- |
| transport/protocol success | the SDK completed the exchange | eligible for successful evidence |
| tool execution success | `IsError` is false | eligible for successful evidence |
| tool execution failure | `IsError` is true | failed call; diagnostics only, no success evidence |
| transport failure | connection/process/protocol failure | failed call; no success evidence |
| timeout | call or run deadline expired | terminal/bounded result; no success evidence |
| cancellation | caller or run context canceled | terminal/bounded result; no success evidence |
| unsupported result | malformed, oversized or unsupported content | failed call; no success evidence |

An `IsError` response is returned as a typed tool execution failure with bounded
sanitized diagnostics preserved for the model. The authoritative flag is the
SDK `CallToolResult.IsError` envelope. A text string or structured field named
`is_error` in remote content is ordinary untrusted data and cannot upgrade or
reclassify the call.

`AgentToolLoop` records the failed call as `failed` and preserves its stable
reason code. `StopOnToolError` terminates the loop with `tool_error` when set.
`AgentGoalRun` sends the same diagnostics back to the model but does not call
`recordToolEvidence()` for a failed invocation. Therefore a
`PredicateEvidenceExists` criterion cannot be satisfied by a failed MCP call.
Successful calls still produce `tool_observation` evidence with
`tool_observation` trust, never `system_verified` evidence. MCP text cannot
change criteria, allowlists, source scope, budgets or the deterministic
acceptance result.

A goal requiring authoritative CROWler state must use the native database or
resource evidence providers. MCP output is evidence of what the external server
returned, not proof that a CROWler source is pending, processing or completed.

## Diagnostics and errors

Typical stable failures include:

- `mcp_transport_error`
- `mcp_discovery_error`
- `mcp_not_approved`
- `mcp_inventory_changed`
- `mcp_unsupported_result`
- `mcp_budget_exhausted`
- `mcp_tool_error` for an `IsError=true` tool result
- `MCP HTTP redirect is not permitted`
- `MCP HTTP destination is not permitted`
- `MCP HTTP response exceeds configured limit`
- `MCP TLS`/certificate verification failures
- `legacy`/`unsupported protocol version` when HTTP negotiation is outside policy
- `mcp_call_timeout` for a bounded call timeout
- `deadline_exceeded` / `budget_elapsed` for goal initialization or execution
  that exhausts the shared run deadline
- `crawl_operation_conflict` for native crawl reconciliation

Check operator configuration first for startup failures, then the run allowlist
and source scope. Invalid MCP schemas and missing approved tools fail the run
rather than silently widening the inventory.

## Future work

WP-04B adds the read-only Streamable HTTP transport described above, with
endpoint, TLS, redirect, DNS/address, authentication and credential-isolation
policy. WP-04C will require a separate mutation authorization contract. No
schema version beyond 1.17 is used.

Limitations: HTTP OAuth authorization-code flows, dynamic client registration,
arbitrary proxies, persistent sessions, resource subscriptions, MCP Tasks,
prompts/resources execution and mutation tools are not implemented. Private
network exceptions are explicit trust expansions and should be used only for
deliberately approved deployments. A configured server remains responsible for
its own authorization and read-only behaviour.
