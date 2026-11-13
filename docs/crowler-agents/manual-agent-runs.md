# Authenticated manual agent runs

The Events service owns the agent engine. WP-04C2G B2 adds one
user-triggered entrypoint for registered agents that declare a `manual`
trigger.

## Endpoint

`POST /v1/agent/run` (Events service, default port 8082)

Authentication is required. The caller must have the `events:*` scope or the
future `events:agent:run` scope. The handler re-fetches current account state
with `auth.ResolveActiveIdentity`, so disabled and deleted accounts are
rejected even if a token is still valid.

Request:

```json
{
  "agent_id": "manual-agent",
  "input": "echo live-manual-ok",
  "parameters": {}
}
```

`input` may be any bounded JSON value required by the agent's first action;
`parameters` is an optional object.

Use `agent_name` instead of `agent_id` if needed; exactly one may be supplied.
Unknown top-level fields are rejected. In particular, the request cannot set
`principal_user_id`, `username`, `issuer`, `owner`, `user_id` or any other
principal/authority field.

The agent must be registered and have at least one `manual` trigger. Only its
manual-trigger jobs execute; event-triggered jobs are not invoked out of band.
Agent identity enforcement must be enabled.

Response:

```json
{
  "status": "success",
  "agent_id": "manual-agent",
  "agent_name": "Manual Agent",
  "principal_user_id": 7,
  "principal_username": "owner-1",
  "principal_issuer": "crowler",
  "result": {}
}
```

The principal fields are informational evidence of the authenticated runtime
provenance; they do not grant MCP proposal authority by themselves. Production
MCP proposal registration remains disabled until the separate B3 decision.

## Background/event runs

Event-triggered agent runs remain principal-less. Event payloads, metadata,
manifests, model output and tool arguments cannot supply a principal. A
requested MCP mutation alias without a trusted principal is denied by the tool
loop and goal action even before B3 registers a production proposal adapter.

## Live testing

Follow `TESTING.md`: generate the Compose configuration, build the CROWler
services, start the stack, authenticate as a user with `events:*`, upload a
manual-trigger agent, and call the endpoint. Verify the returned principal,
the manual-only job execution and the Events service logs.
