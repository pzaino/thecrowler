# WP-04C2M Deployment Freshness

## Current behavior

- API and Events each freeze their MCP policy, inventory and agent registry at
  process start.
- SIGHUP does not hot-reload MCP authority.
- Agent uploads after API startup are not visible to the API approval registry.
- A policy or agent generation mismatch between API and Events fails closed:
  policy identity/hash mismatch at approval returns 409; unavailable or disabled
  API policy returns 503; a missing/changed agent fails approval.

This is an availability limitation, not an authorization bypass.

## Deploy-time readiness check

No new public endpoint or hot reload is added. The operator procedure is:

1. Deploy the same config and agent source to both API and Events.
2. Restart both services as one deployment unit.
3. Verify both processes loaded the intended `mcp.mutation_policy` generation
   and agent files:
   - record the config/agent source revision (for example the deployment SHA);
   - record `policy_id`, `version` and canonical hash from the trusted
     deployment artifact;
   - confirm API/Events startup logs show successful MCP authority
     initialization;
   - confirm an approval probe for a proposal created by Events succeeds only
     after both processes are on the same generation.
4. If an agent was uploaded after API startup, restart API before expecting that
   agent to be approvable.
5. If API and Events are intentionally on different generations, expect 409/503
   and do not treat it as a transient.

Non-secret readiness evidence: deployment SHA, policy ID/version/hash, agent
file digests, process start time and readiness status. Do not log tokens,
credentials or raw policy resources.

## Mismatch behavior

- Policy ID/version/hash mismatch: fail closed (409).
- Missing/disabled/unavailable API policy: fail closed (503).
- Missing/changed agent identity: fail closed.
- Current account/source ownership changes: revalidated per approval.

## Not implemented

- No silent policy hot reload.
- No shared live registry.
- No new readiness API.
- No schema change.

A future approved reload or durable registry design would need its own
consistency and rollback review.
