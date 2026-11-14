#!/usr/bin/env python3
"""Prepare a disposable CROWler config for the MCP authorization E2E fixture."""
import re
import sys

source, target = sys.argv[1], sys.argv[2]
text = open(source, encoding="utf-8").read()

text = re.sub(r"(?m)^debug_level:.*$", "debug_level: 5", text)
text = text.replace("  retry_time: 10", "  retry_time: 1", 1)
text = text.replace("  ping_time: 10", "  ping_time: 1", 1)

# Manual runs require runtime identity enforcement. Keep contract enforcement
# on so the fixture exercises the same trusted identity path as production.
agent_start = text.index("\nagent:\n")
agent_end = text.index("\n# --------------------------", agent_start)
agent_block = text[agent_start:agent_end]
agent_block = agent_block.replace("identity_enforcement: false", "identity_enforcement: true")
agent_block = agent_block.replace("contract_enforcement: false", "contract_enforcement: true")
text = text[:agent_start] + agent_block + text[agent_end:]

api_auth = """  auth:
    enabled: true
    mode: local
    issuer: crowler
    audience: crowler-api
    hmac_secret: ${CROWLER_E2E_AUTH_SECRET}
    token_ttl: 3600
    local:
      enabled: true
"""
events_auth = """  auth:
    enabled: true
    mode: local
    issuer: crowler
    audience: crowler-events
    hmac_secret: ${CROWLER_E2E_AUTH_SECRET}
    token_ttl: 3600
    local:
      enabled: true
"""
if "api:\n" not in text or "events:\n" not in text:
    raise SystemExit("default config is missing api/events sections")
text = text.replace("api:\n", "api:\n" + api_auth, 1)
text = text.replace("events:\n", "events:\n" + events_auth, 1)

start = text.index("mcp:\n")
end = text.index("\nagents:\n", start)
mcp = """mcp:
  servers:
    - id: approved-server
      enabled: true
      transport: stdio
      command: /opt/crowler/bin/mcp-fixture
      environment:
        CROWLER_MCP_MUTATION_COUNTER: /tmp/crowler-mcp-state/counter.log
      allowed_agents: [mcp-e2e-agent, mcp-e2e-slow-agent]
      approved_tools:
        - name: mutate_counter
          effect: mutation
          allow_mutation_proposal: true
          description: E2E fixture mutation.
          required_capabilities: [tool_execution]
          allowed_sources: [42]
  mutation_policy:
    enabled: true
    policy_id: e2e-mutation-policy
    version: "1"
    operations:
      - server_id: approved-server
        tool_name: mutate_counter
        allowed_agents: [mcp-e2e-agent, mcp-e2e-slow-agent]
        required_capabilities: [tool_execution]
        min_trust_level: trusted
        allowed_sources: [42]
        allowed_resources: []
        required_argument_fields: [source_id]
        allowed_argument_fields: [source_id]
        destructive: false
        proposal_ttl: 5m
        max_proposals_per_run: 8
        max_pending_proposals: 16
        max_input_bytes: 65536
        max_output_bytes: 65536
        require_approval: true
        execution_backend: none
        allowed_transports: [stdio]
        audit_required: true
"""
text = text[:start] + mcp + text[end:]
open(target, "w", encoding="utf-8").write(text)
