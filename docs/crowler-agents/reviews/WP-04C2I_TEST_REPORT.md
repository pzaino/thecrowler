# WP-04C2I Test Report

## Environment

- Repository: `pzaino/thecrowler`
- Branch: `develop`
- HEAD: `7f4db7f6d684ec51ec9968880735f52d30b0b509` (unchanged; no commit)
- Go: `go1.27.3 linux/amd64`
- Docker: `29.7.2-ce`; Compose `v5.6.1`
- Disposable PostgreSQL stack: `postgres:alpine3.20`
- Release: `v2.3.0`
- Schema: `1.17`, writer generation `verified-ownership-v1.17`

Commands ran in `/home/pzaino/tmp/opencode/thecrowler-wp04c2i`. Logs are under
`/home/pzaino/tmp/opencode/`.

## Go verification

| Command | Result | Log |
| --- | --- | --- |
| `go test ./pkg/config ./pkg/agent ./pkg/database ./services/events ./services/api -count=1` | PASS | `wp04c2i-focused.log` |
| `go test ./... -count=1` | PASS | `wp04c2i-final4-full.log` |
| `go test -race ./pkg/config ./pkg/agent ./pkg/database ./services/events ./services/api -count=1` | PASS, no data race | `wp04c2i-final2-race.log` |
| VDI negative controls `-count=20` | PASS | `wp04c2i-vdi.log` |
| `gofmt -l pkg services cmd` | PASS (no output) | `wp04c2i-final4-full.log` |
| `git diff --check` | PASS | `wp04c2i-final4-full.log` |
| `go vet ./...` | PASS | `wp04c2i-final4-full.log` |
| `CGO_ENABLED=0 go build ./...` | PASS | `wp04c2i-final4-full.log` |
| `CGO_ENABLED=1 go build ./...` | PASS | `wp04c2i-final4-full.log` |
| PostgreSQL audit/retention integration | PASS | `wp04c2i-pg-audit.log` |

The first applied-tree `go test ./...` run hit a transient
`pkg/ws.TestWebSocketConnectionMessageDeliveryAndCleanup` failure. That test
passed in isolation, and the complete applied-tree suite passed on immediate
rerun. The failing package is outside the B5 change set. Both logs are retained:
`wp04c2i-patchcheck-final.log` (transient failure) and
`wp04c2i-patchcheck-final-retry.log` (pass).

## B5 test coverage

`pkg/database/mcp_mutation_audit_test.go`:

- durable denied-admission insert with actor, issuer, run, trace, principal,
  server, alias, reason and flat detail;
- rejection of unsafe event types, actor types, missing actors, raw-error
  reasons, malformed hashes, array/nested/uppercase/oversized detail;
- bounded retention with a 2-row first pass, a 2-row second pass and a 0-row
  final pass;
- unknown event type retained, recent row retained;
- missing cutoff rejected;
- audit insert after database close fails rather than silently succeeding.

`pkg/database/mcp_mutation_audit_postgres_integration_test.go` (build tag
`integration`):

- inserts and reads a denied-admission row on real PostgreSQL;
- decodes JSONB detail and verifies principal/reason;
- deletes one old known row, leaves an unknown old row and a recent row;
- runs in a disposable database created and dropped by the test.

`pkg/agent/mcp_audit_test.go`:

- reason-code mapping for tool, sentinel, context and unknown errors;
- call-ID sanitization for safe, URL, control-character and oversized values;
- proposal denial writes a durable row with actor/principal/run/trace/alias and
  no secret argument;
- audit-write failure preserves the denial and does not attempt admission;
- closed-database proposal admission returns `ErrMCPMutationStoreUnavailable`,
  not success;
- registration denial writes a global principal event and a per-operation
  capability event;
- registration database outage preserves `ErrMCPMutationStoreUnavailable`;
- tool-loop authorization denial writes a `tool_loop` audit row and creates no
  proposal;
- goal authorization denial writes a `goal` audit row and creates no proposal;
- an unregistered but operator-known MCP alias is still denied as `unknown_tool`
  and writes a `tool_loop` or `goal` audit row with the policy identity;
- control characters are removed from audit text.

`services/events/mcp_audit_retention_test.go`:

- empty value uses 90 days;
- native and friendly durations parse;
- values below 24 hours are raised to 24 hours;
- invalid values fall back to the 24-hour floor.

## Live Docker acceptance

A disposable Compose project (`wp04c2i`) used:

- PostgreSQL on host port `55482`, schema 1.17;
- API `18082`, Events `18084`;
- a controlled stdio MCP server with read-only `echo` and mutating
  `mutate_counter` and `mutate_strict_counter`;
- a fake OpenAI-compatible model that calls
  `mcp_approved-server_mutate_counter` with
  `{"resource":"https://secret.example/denied?token=abc"}`;
- `live-mutation-agent` with `tool_execution`, a manual job and the mutation
  alias in its explicit allowlist;
- `live-registration-agent` with `tool_execution`, a manual job and both
  mutation aliases in its allowlist, while the strict operation requires
  `db_read`;
- `live-unknown-alias-agent` with `tool_execution`, a manual job, and only the
  read-only `echo` alias in its allowlist. The mutation alias is known to the
  operator policy but is not registered for this run.

### Denied proposal

The run returned HTTP 200 with a failed tool call:

```json
{
  "status": "success",
  "agent_id": "live-mutation-agent",
  "principal_user_id": 1,
  "principal_username": "wp04c2i-operator",
  "principal_issuer": "crowler",
  "result": {
    "content": "proposal denied or recorded",
    "status": "completed",
    "tool_call_count": 1,
    "tool_calls": [{
      "id": "live-denied-call-1",
      "name": "mcp_approved-server_mutate_counter",
      "reason_code": "tool_execution_failed",
      "status": "failed"
    }]
  }
}
```

Durable audit row:

```text
event_type        = mcp_mutation_admission_denied
actor_type        = agent
actor_id          = live-mutation-agent
actor_issuer      = crowler
run_id            = 2638e00c-860f-48be-a2f4-fc7e8b916f63
trace_id          = b07e643a-8c5d-41c0-9b49-35da8e09147d
agent_id          = live-mutation-agent
principal_user_id = 1
server_id         = approved-server
tool_alias        = mcp_approved-server_mutate_counter
reason_code       = invalid_arguments
redacted_detail   = {"stage": "proposal", "call_id": "live-denied-call-1", "policy_id": "live-mutation-policy"}
```

### Registration denial

The run returned HTTP 502 (`agent execution failed`). The Events log recorded
the original denial: `agent lacks capability "db_read" for MCP operation
"mcp_approved-server_mutate_strict_counter"`.

Durable audit row:

```text
event_type        = mcp_mutation_registration_denied
actor_type        = agent
actor_id          = live-registration-agent
actor_issuer      = crowler
run_id            = 051ccb7c-ff53-4559-9054-fb038087fb80
trace_id          = c34da131-97df-4cea-8084-576d2b16f157
agent_id          = live-registration-agent
principal_user_id = 1
server_id         = approved-server
tool_alias        = mcp_approved-server_mutate_strict_counter
reason_code       = capability_missing
redacted_detail   = {"stage": "registration", "policy_id": "live-mutation-policy"}
```

### Unregistered but policy-known alias

The run returned HTTP 200 with a denied tool call and no execution:

```json
{
  "status": "success",
  "agent_id": "live-unknown-alias-agent",
  "principal_user_id": 1,
  "principal_username": "wp04c2i-operator",
  "principal_issuer": "crowler",
  "result": {
    "content": "proposal denied or recorded",
    "status": "completed",
    "tool_call_count": 0,
    "tool_calls": [{
      "id": "live-denied-call-1",
      "name": "mcp_approved-server_mutate_counter",
      "reason_code": "unknown_tool",
      "status": "denied"
    }]
  }
}
```

Durable audit row:

```text
event_type        = mcp_mutation_admission_denied
actor_type        = agent
actor_id          = live-unknown-alias-agent
actor_issuer      = crowler
run_id            = ed340356-d119-42f3-94c7-3dfc089aa566
trace_id          = 27f9f228-6409-4d77-8011-f52afd28c5f3
agent_id          = live-unknown-alias-agent
principal_user_id = 1
server_id         = approved-server
tool_alias        = mcp_approved-server_mutate_counter
reason_code       = unknown_tool
redacted_detail   = {"stage": "tool_loop", "call_id": "live-denied-call-1", "policy_id": "live-mutation-policy"}
```

### Safety evidence

- `MCPMutationProposals`: `0`.
- `MCPMutationApprovals`: `0`.
- `MCPMutationDispatchAttempts`: `0`.
- `MCPMutationAudit`: `3` denied-admission rows.
- Controlled mutation counter file: absent.
- Secret scan of `MCPMutationAudit`: `0` matches for `secret.example` or
  `token=abc`.
- Secret scan of the Events log: `0` matches for `secret.example` or
  `token=abc`.
- The disposable stack and its volumes were removed after the test.

Live logs: `wp04c2i-final-live-denied.log`,
`wp04c2i-final-live-registration.log`,
`wp04c2i-final-live-unknown2.log` and `wp04c2i-final-live-evidence.log`.

## Container build

The final binaries were built from the B5 source in a Go Alpine builder with
the production `/app` source path:

- `services/events` -> `/app/events`;
- `services/api` -> `/app/api`;
- `cmd/thecrowler` -> `/app/thecrowler`.

They were layered into the existing CROWler images and tagged
`crowler-events:latest`, `crowler-api:latest` and `crowler-engine:latest`. The
standard Dockerfile build path remains network-dependent on
`proxy.golang.org`; the host module cache was used to avoid transient
Docker-internal TLS failures.

## Skips and limitations

- Information Seed Compose acceptance: not run; separate opt-in suite and not
  touched by B5.
- Live Ollama: not used; the deterministic model was a controlled fake
  provider.
- Full engine/VDI application stack: not run; B5 changes the API/Events/DB audit
  path, not VDI.
- B4 approval-time current-policy revalidation: not present in this checkout and
  not reimplemented.
- G3 schema deployment and SQLite foreign-key enforcement: pending.
- A database outage during the live test would also prevent the manual-run
  endpoint from resolving the authenticated principal. The proposal-path
  outage contract is therefore covered by the closed-database unit tests and the
  audit-write-failure test rather than a live outage run.

## Patch validation

The patch is generated with:

```sh
git diff --binary --full-index --no-ext-diff 7f4db7f6
```

It is applied to a clean `git archive 7f4db7f6` tree. The applied-tree full
suite, vet, gofmt, static builds and focused race results are reported in the
handoff. The tracked report does not embed the final patch hash because adding
it would change the patch it describes.
